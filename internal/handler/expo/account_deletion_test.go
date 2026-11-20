package expo

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bradleygichuru/ytci-go/internal/db"
)

func ensureAccountAuthTables(t *testing.T, pool *pgxpool.Pool) {
	t.Helper()
	_, err := pool.Exec(context.Background(),
		`CREATE TABLE IF NOT EXISTS sessions (user_id text);
		 CREATE TABLE IF NOT EXISTS accounts (user_id text);
		 CREATE TABLE IF NOT EXISTS bucket_list_items (user_id text);
		 CREATE TABLE IF NOT EXISTS push_tokens (user_id text);
		 CREATE TABLE IF NOT EXISTS app_opens (user_id text);
		 CREATE TABLE IF NOT EXISTS user_profiles (user_id text);
		 CREATE TABLE IF NOT EXISTS story_interactions (user_id text);
		 CREATE TABLE IF NOT EXISTS comment_interactions (user_id text);
		 CREATE TABLE IF NOT EXISTS event_saves (user_id text);
		 CREATE TABLE IF NOT EXISTS audit_logs (user_id text);
		 CREATE TABLE IF NOT EXISTS pending_media_uploads (user_id text);`)
	require.NoError(t, err)
}

func accountPlan(t *testing.T, h *AccountHandler, userID string) deletionPlanResponse {
	t.Helper()
	req := collabRequest(t, http.MethodGet, "/v1/mobile/account/deletion-plan", userID, nil)
	rec := httptest.NewRecorder()
	h.DeletionPlan(rec, req)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	var plan deletionPlanResponse
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &plan))
	return plan
}

func TestDeletionPlanProposesMostActiveMember(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}
	ctx := context.Background()
	pool := db.SetupTestDB(t)
	defer pool.Close()

	owner := newCollaborationTestUser(t, pool, "Owner")
	olderEditor := newCollaborationTestUser(t, pool, "Older editor")
	activeEditor := newCollaborationTestUser(t, pool, "Active editor")
	viewer := newCollaborationTestUser(t, pool, "Viewer")
	itineraryID := newCollaborationTestItinerary(t, pool, owner)
	require.NoError(t, AddMember(ctx, pool, itineraryID, olderEditor, roleEditor, owner))
	require.NoError(t, AddMember(ctx, pool, itineraryID, activeEditor, roleEditor, owner))
	require.NoError(t, AddMember(ctx, pool, itineraryID, viewer, roleViewer, owner))

	for i := 0; i < 2; i++ {
		require.NoError(t, recordTripEvent(ctx, pool, itineraryID, olderEditor, "plan", "updated",
			"plan", itineraryID, "Edited plan", nil, nil, ""))
	}
	for i := 0; i < 5; i++ {
		require.NoError(t, recordTripEvent(ctx, pool, itineraryID, activeEditor, "budget", "updated",
			"budget", itineraryID, "Edited budget", nil, nil, ""))
	}

	h := NewAccountHandler(pool, nil)
	plan := accountPlan(t, h, owner)
	require.Len(t, plan.OwnedTrips, 1)
	trip := plan.OwnedTrips[0]
	assert.Equal(t, itineraryID, trip.ID)
	assert.Equal(t, activeEditor, trip.ProposedSuccessorUserID)
	require.Len(t, trip.Members, 3)
	assert.Equal(t, activeEditor, trip.Members[0].UserID)
	assert.Equal(t, 5, trip.Members[0].WriteActions90Days)
	assert.Equal(t, "editor", trip.Members[0].Role)
}

func TestDeleteAccountTransfersOwnershipAndRetainsLedger(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}
	ctx := context.Background()
	pool := db.SetupTestDB(t)
	defer pool.Close()

	owner := newCollaborationTestUser(t, pool, "Owner")
	viewer := newCollaborationTestUser(t, pool, "Viewer")
	itineraryID := newCollaborationTestItinerary(t, pool, owner)
	require.NoError(t, AddMember(ctx, pool, itineraryID, viewer, roleViewer, owner))

	var ownerParticipant string
	require.NoError(t, pool.QueryRow(ctx,
		`SELECT id::text FROM trip_participants WHERE itinerary_id = $1 AND user_id = $2`,
		itineraryID, owner).Scan(&ownerParticipant))
	var viewerParticipant string
	require.NoError(t, pool.QueryRow(ctx,
		`SELECT id::text FROM trip_participants WHERE itinerary_id = $1 AND user_id = $2`,
		itineraryID, viewer).Scan(&viewerParticipant))

	var expenseID string
	require.NoError(t, pool.QueryRow(ctx,
		`INSERT INTO expenses (itinerary_id, title, category, original_amount, currency, fx_rate,
		                       amount_kes, expense_date, payer_participant_id, created_by)
		 VALUES ($1, 'Park fees', 'Park & entry fees', 1000, 'KES', 1, 1000, current_date, $2, $3)
		 RETURNING id::text`,
		itineraryID, ownerParticipant, owner).Scan(&expenseID))
	_, err := pool.Exec(ctx,
		`INSERT INTO expense_shares (expense_id, participant_id, share_kes)
		 VALUES ($1, $2, 500), ($1, $3, 500)`,
		expenseID, ownerParticipant, viewerParticipant)
	require.NoError(t, err)

	ensureAccountAuthTables(t, pool)
	h := NewAccountHandler(pool, nil)
	req := collabRequest(t, http.MethodPost, "/v1/mobile/account/delete", owner, map[string]any{
		"confirm": true,
		"transfers": []map[string]any{{
			"itineraryId":     itineraryID,
			"successorUserId": viewer,
		}},
	})
	rec := httptest.NewRecorder()
	h.Delete(rec, req)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())

	var ownerID string
	require.NoError(t, pool.QueryRow(ctx, `SELECT user_id FROM itineraries WHERE id = $1`, itineraryID).Scan(&ownerID))
	assert.Equal(t, viewer, ownerID)

	var ownerRole string
	require.NoError(t, pool.QueryRow(ctx,
		`SELECT role FROM itinerary_members WHERE itinerary_id = $1 AND user_id = $2`,
		itineraryID, viewer).Scan(&ownerRole))
	assert.Equal(t, roleOwner, ownerRole)

	var oldOwnerMemberships, users int
	require.NoError(t, pool.QueryRow(ctx,
		`SELECT count(*)::int FROM itinerary_members WHERE itinerary_id = $1 AND user_id = $2`,
		itineraryID, owner).Scan(&oldOwnerMemberships))
	assert.Zero(t, oldOwnerMemberships)
	require.NoError(t, pool.QueryRow(ctx, `SELECT count(*)::int FROM users WHERE id = $1`, owner).Scan(&users))
	assert.Zero(t, users)

	var participantUser *string
	var removedAt any
	var displayName string
	require.NoError(t, pool.QueryRow(ctx,
		`SELECT user_id, removed_at, display_name FROM trip_participants WHERE id = $1`,
		ownerParticipant).Scan(&participantUser, &removedAt, &displayName))
	assert.Nil(t, participantUser)
	assert.NotNil(t, removedAt)
	assert.Equal(t, "Former member", displayName)

	var shares int
	require.NoError(t, pool.QueryRow(ctx,
		`SELECT count(*)::int FROM expense_shares WHERE expense_id = $1`, expenseID).Scan(&shares))
	assert.Equal(t, 2, shares)

	var actor *string
	var summary string
	require.NoError(t, pool.QueryRow(ctx,
		`SELECT actor_id, summary FROM trip_events
		 WHERE itinerary_id = $1 AND action = 'ownership_transferred'`, itineraryID).
		Scan(&actor, &summary))
	assert.Nil(t, actor)
	assert.Contains(t, summary, "Transferred ownership")
}

func TestDeleteAccountRequiresDecisionForOwnedTrip(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}
	ctx := context.Background()
	pool := db.SetupTestDB(t)
	defer pool.Close()

	owner := newCollaborationTestUser(t, pool, "Owner")
	editor := newCollaborationTestUser(t, pool, "Editor")
	itineraryID := newCollaborationTestItinerary(t, pool, owner)
	require.NoError(t, AddMember(ctx, pool, itineraryID, editor, roleEditor, owner))

	h := NewAccountHandler(pool, nil)
	req := collabRequest(t, http.MethodPost, "/v1/mobile/account/delete", owner, map[string]any{"confirm": true})
	rec := httptest.NewRecorder()
	h.Delete(rec, req)
	require.Equal(t, http.StatusBadRequest, rec.Code, rec.Body.String())
	assert.Contains(t, rec.Body.String(), "DECISION_REQUIRED")

	var users int
	require.NoError(t, pool.QueryRow(ctx, `SELECT count(*)::int FROM users WHERE id = $1`, owner).Scan(&users))
	assert.Equal(t, 1, users)
}

func TestDeleteAccountDeletesSoleOwnerTrip(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}
	ctx := context.Background()
	pool := db.SetupTestDB(t)
	defer pool.Close()

	owner := newCollaborationTestUser(t, pool, "Solo owner")
	itineraryID := newCollaborationTestItinerary(t, pool, owner)
	ensureAccountAuthTables(t, pool)
	h := NewAccountHandler(pool, nil)
	req := collabRequest(t, http.MethodPost, "/v1/mobile/account/delete", owner, map[string]any{
		"confirm": true,
		"transfers": []map[string]any{{
			"itineraryId": itineraryID,
			"delete":      true,
		}},
	})
	rec := httptest.NewRecorder()
	h.Delete(rec, req)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())

	var deleted any
	require.NoError(t, pool.QueryRow(ctx, `SELECT deleted_at FROM itineraries WHERE id = $1`, itineraryID).Scan(&deleted))
	assert.NotNil(t, deleted)
}

func TestDeleteAccountRejectsInvalidSuccessor(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}
	ctx := context.Background()
	pool := db.SetupTestDB(t)
	defer pool.Close()

	owner := newCollaborationTestUser(t, pool, "Owner")
	editor := newCollaborationTestUser(t, pool, "Editor")
	stranger := newCollaborationTestUser(t, pool, "Stranger")
	itineraryID := newCollaborationTestItinerary(t, pool, owner)
	require.NoError(t, AddMember(ctx, pool, itineraryID, editor, roleEditor, owner))

	h := NewAccountHandler(pool, nil)
	req := collabRequest(t, http.MethodPost, "/v1/mobile/account/delete", owner, map[string]any{
		"confirm": true,
		"transfers": []map[string]any{{
			"itineraryId":     itineraryID,
			"successorUserId": stranger,
		}},
	})
	rec := httptest.NewRecorder()
	h.Delete(rec, req)
	require.Equal(t, http.StatusBadRequest, rec.Code, rec.Body.String())
	assert.Contains(t, rec.Body.String(), "INVALID_SUCCESSOR")
}

func TestDeleteAccountRetainsSharesOnNonOwnedTrip(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}
	ctx := context.Background()
	pool := db.SetupTestDB(t)
	defer pool.Close()

	otherOwner := newCollaborationTestUser(t, pool, "Other owner")
	member := newCollaborationTestUser(t, pool, "Member")
	itineraryID := newCollaborationTestItinerary(t, pool, otherOwner)
	require.NoError(t, AddMember(ctx, pool, itineraryID, member, roleEditor, otherOwner))

	var memberParticipant string
	require.NoError(t, pool.QueryRow(ctx,
		`SELECT id::text FROM trip_participants WHERE itinerary_id = $1 AND user_id = $2`,
		itineraryID, member).Scan(&memberParticipant))
	var ownerParticipant string
	require.NoError(t, pool.QueryRow(ctx,
		`SELECT id::text FROM trip_participants WHERE itinerary_id = $1 AND user_id = $2`,
		itineraryID, otherOwner).Scan(&ownerParticipant))
	var expenseID string
	require.NoError(t, pool.QueryRow(ctx,
		`INSERT INTO expenses (itinerary_id, title, category, original_amount, currency, fx_rate,
		                       amount_kes, expense_date, payer_participant_id, created_by)
		 VALUES ($1, 'Fuel', 'Transport', 2000, 'KES', 1, 2000, current_date, $2, $3)
		 RETURNING id::text`,
		itineraryID, ownerParticipant, otherOwner).Scan(&expenseID))
	_, err := pool.Exec(ctx,
		`INSERT INTO expense_shares (expense_id, participant_id, share_kes) VALUES ($1, $2, 1000), ($1, $3, 1000)`,
		expenseID, ownerParticipant, memberParticipant)
	require.NoError(t, err)

	ensureAccountAuthTables(t, pool)
	h := NewAccountHandler(pool, nil)
	req := collabRequest(t, http.MethodPost, "/v1/mobile/account/delete", member, map[string]any{"confirm": true})
	rec := httptest.NewRecorder()
	h.Delete(rec, req)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())

	var memberUser *string
	var removedAt any
	require.NoError(t, pool.QueryRow(ctx,
		`SELECT user_id, removed_at FROM trip_participants WHERE id = $1`, memberParticipant).
		Scan(&memberUser, &removedAt))
	assert.Nil(t, memberUser)
	assert.NotNil(t, removedAt)

	var shares int
	require.NoError(t, pool.QueryRow(ctx,
		`SELECT count(*)::int FROM expense_shares WHERE expense_id = $1`, expenseID).Scan(&shares))
	assert.Equal(t, 2, shares)
}
