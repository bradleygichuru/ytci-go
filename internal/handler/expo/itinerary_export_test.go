package expo

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bradleygichuru/ytci-go/internal/db"
	"github.com/bradleygichuru/ytci-go/internal/middleware"
)

func TestItineraryExportIncludesLedgerAndAudit(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}
	ctx := context.Background()
	pool := db.SetupTestDB(t)
	defer pool.Close()

	ownerID := newCollaborationTestUser(t, pool, "Owner")
	editorID := newCollaborationTestUser(t, pool, "Editor")
	viewerID := newCollaborationTestUser(t, pool, "Viewer")
	itineraryID := newCollaborationTestItinerary(t, pool, ownerID)
	require.NoError(t, AddMember(ctx, pool, itineraryID, editorID, roleEditor, ownerID))
	require.NoError(t, AddMember(ctx, pool, itineraryID, viewerID, roleViewer, ownerID))

	var ownerParticipant, editorParticipant string
	require.NoError(t, pool.QueryRow(ctx,
		`SELECT id::text FROM trip_participants WHERE itinerary_id = $1 AND user_id = $2`,
		itineraryID, ownerID).Scan(&ownerParticipant))
	require.NoError(t, pool.QueryRow(ctx,
		`SELECT id::text FROM trip_participants WHERE itinerary_id = $1 AND user_id = $2`,
		itineraryID, editorID).Scan(&editorParticipant))

	_, err := pool.Exec(ctx,
		`INSERT INTO itinerary_stops (itinerary_id, day, display_order, title, category)
		 VALUES ($1, 1, 0, 'Morning drive', 'Wildlife')`, itineraryID)
	require.NoError(t, err)

	var expenseID string
	require.NoError(t, pool.QueryRow(ctx,
		`INSERT INTO expenses
		 (itinerary_id, title, category, original_amount, currency, fx_rate, amount_kes,
		  expense_date, payer_participant_id, created_by, updated_by)
		 VALUES ($1, 'Park entry', 'Park & entry fees', 2000, 'KES', 1, 2000,
		         current_date, $2, $3, $3)
		 RETURNING id`, itineraryID, ownerParticipant, ownerID).Scan(&expenseID))
	_, err = pool.Exec(ctx,
		`INSERT INTO expense_shares (expense_id, participant_id, share_kes)
		 VALUES ($1, $2, 1000), ($1, $3, 1000)`, expenseID, ownerParticipant, editorParticipant)
	require.NoError(t, err)
	_, err = pool.Exec(ctx,
		`INSERT INTO settlements (itinerary_id, from_participant_id, to_participant_id, amount_kes, settlement_date, created_by)
		 VALUES ($1, $2, $3, 1000, current_date, $4)`, itineraryID, editorParticipant, ownerParticipant, ownerID)
	require.NoError(t, err)
	withCollaborationTx(t, pool, func(tx pgx.Tx) {
		_, err := insertPlanRevision(ctx, tx, itineraryID, 1, ownerID, "create")
		require.NoError(t, err)
		_, err = tx.Exec(ctx,
			`INSERT INTO trip_events (itinerary_id, actor_id, scope, action, summary)
			 VALUES ($1, $2, 'budget', 'expense_created', 'Added Park entry')`,
			itineraryID, ownerID)
		require.NoError(t, err)
	})

	h := NewItinerariesHandler(pool)
	req := httptest.NewRequest(http.MethodGet, "/v1/mobile/itineraries/"+itineraryID+"/export", nil)
	req.SetPathValue("id", itineraryID)
	req = req.WithContext(context.WithValue(req.Context(), middleware.CtxUserID, editorID))
	rec := httptest.NewRecorder()
	h.Export(rec, req)

	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	var envelope itineraryExport
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &envelope))
	assert.Equal(t, "ytci.itinerary.export", envelope.Format)
	assert.Equal(t, exportFormatVersion, envelope.Version)
	assert.Contains(t, envelope.Checksum, "sha256:")
	assert.Equal(t, itineraryID, envelope.Data.Itinerary.ID)
	assert.Len(t, envelope.Data.Members, 3)
	assert.NotEmpty(t, envelope.Data.Stops)
	require.Len(t, envelope.Data.Budget.Expenses, 1)
	assert.Len(t, envelope.Data.Budget.Expenses[0].Shares, 2)
	assert.Len(t, envelope.Data.Budget.Settlements, 1)
	assert.NotEmpty(t, envelope.Data.History)
	assert.NotEmpty(t, envelope.Data.Revisions)

	// The checksum is over the data payload, so a consumer can verify it after
	// stripping the envelope metadata.
	payload, err := json.Marshal(envelope.Data)
	require.NoError(t, err)
	sum := sha256.Sum256(payload)
	assert.Equal(t, "sha256:"+hex.EncodeToString(sum[:]), envelope.Checksum)
}

func TestItineraryExportViewerForbidden(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}
	ctx := context.Background()
	pool := db.SetupTestDB(t)
	defer pool.Close()

	ownerID := newCollaborationTestUser(t, pool, "Owner")
	viewerID := newCollaborationTestUser(t, pool, "Viewer")
	itineraryID := newCollaborationTestItinerary(t, pool, ownerID)
	require.NoError(t, AddMember(ctx, pool, itineraryID, viewerID, roleViewer, ownerID))

	h := NewItinerariesHandler(pool)
	req := httptest.NewRequest(http.MethodGet, "/v1/mobile/itineraries/"+itineraryID+"/export", nil)
	req.SetPathValue("id", itineraryID)
	req = req.WithContext(context.WithValue(req.Context(), middleware.CtxUserID, viewerID))
	rec := httptest.NewRecorder()
	h.Export(rec, req)
	assert.Equal(t, http.StatusForbidden, rec.Code)
}
