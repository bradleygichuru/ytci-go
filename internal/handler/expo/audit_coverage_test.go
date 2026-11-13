package expo

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bradleygichuru/ytci-go/internal/db"
)

func TestAuditWritesForCoreMutations(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}
	ctx := context.Background()
	pool := db.SetupTestDB(t)
	defer pool.Close()

	ownerID := newCollaborationTestUser(t, pool, "Owner")
	editorID := newCollaborationTestUser(t, pool, "Editor")
	itineraryID := newCollaborationTestItinerary(t, pool, ownerID)
	require.NoError(t, AddMember(ctx, pool, itineraryID, editorID, roleEditor, ownerID))

	var ownerParticipant, editorParticipant string
	require.NoError(t, pool.QueryRow(ctx,
		`SELECT id::text FROM trip_participants WHERE itinerary_id = $1 AND user_id = $2`,
		itineraryID, ownerID).Scan(&ownerParticipant))
	require.NoError(t, pool.QueryRow(ctx,
		`SELECT id::text FROM trip_participants WHERE itinerary_id = $1 AND user_id = $2`,
		itineraryID, editorID).Scan(&editorParticipant))

	itinHandler := NewItinerariesHandler(pool)
	req := budgetTestRequest(t, http.MethodPatch, "/v1/mobile/itineraries/"+itineraryID, editorID, map[string]any{
		"title": "Renamed trip", "expectedVersion": 1,
	})
	req.SetPathValue("id", itineraryID)
	rec := httptest.NewRecorder()
	itinHandler.Update(rec, req)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())

	stopsHandler := NewItineraryStopsHandler(pool)
	req = budgetTestRequest(t, http.MethodPut, "/v1/mobile/itineraries/"+itineraryID+"/stops", editorID, map[string]any{
		"expectedVersion": 2,
		"stops": []map[string]any{{
			"day": 1, "displayOrder": 0, "title": "Morning drive", "category": "Wildlife",
		}},
	})
	req.SetPathValue("id", itineraryID)
	rec = httptest.NewRecorder()
	stopsHandler.UpsertStops(rec, req)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())

	budgetHandler := NewBudgetHandler(pool, nil, nil)
	req = budgetTestRequest(t, http.MethodPost, "/v1/mobile/itineraries/"+itineraryID+"/participants", editorID, map[string]any{
		"name": "Wanjiku",
	})
	req.SetPathValue("id", itineraryID)
	rec = httptest.NewRecorder()
	budgetHandler.CreateParticipant(rec, req)
	require.Equal(t, http.StatusCreated, rec.Code, rec.Body.String())

	req = budgetTestRequest(t, http.MethodPost, "/v1/mobile/itineraries/"+itineraryID+"/expenses", editorID, map[string]any{
		"title": "Park entry", "category": "Park & entry fees", "amount": 2000, "currency": "KES",
		"payerParticipantId": ownerParticipant,
		"split":              map[string]any{"mode": "equal", "participantIds": []string{ownerParticipant, editorParticipant}},
		"expectedVersion":    1,
	})
	req.SetPathValue("id", itineraryID)
	rec = httptest.NewRecorder()
	budgetHandler.CreateExpense(rec, req)
	require.Equal(t, http.StatusCreated, rec.Code, rec.Body.String())

	req = budgetTestRequest(t, http.MethodPost, "/v1/mobile/itineraries/"+itineraryID+"/settlements", editorID, map[string]any{
		"fromParticipantId": editorParticipant, "toParticipantId": ownerParticipant,
		"amountKes": 1000, "date": "2026-11-13",
	})
	req.SetPathValue("id", itineraryID)
	rec = httptest.NewRecorder()
	budgetHandler.CreateSettlement(rec, req)
	require.Equal(t, http.StatusCreated, rec.Code, rec.Body.String())

	rows, err := pool.Query(ctx,
		`SELECT scope, action, COALESCE(actor_id, '') FROM trip_events
		 WHERE itinerary_id = $1 ORDER BY created_at`, itineraryID)
	require.NoError(t, err)
	defer rows.Close()
	actions := map[string]bool{}
	for rows.Next() {
		var scope, action, actor string
		require.NoError(t, rows.Scan(&scope, &action, &actor))
		actions[scope+"."+action] = true
		assert.NotEmpty(t, actor, "event %s must be attributable", scope+"."+action)
	}
	require.NoError(t, rows.Err())

	assert.True(t, actions["plan.updated"], "itinerary edit should be audited")
	assert.True(t, actions["plan.stops_updated"], "stop edit should be audited")
	assert.True(t, actions["members.participant_added"], "offline participant should be audited")
	assert.True(t, actions["budget.expense_created"], "expense should be audited")
	assert.True(t, actions["budget.settlement_created"], "settlement should be audited")

	// Every event also carries the structured before/after payload or a
	// revision link where the action is a plan edit.
	var withPayload int
	require.NoError(t, pool.QueryRow(ctx,
		`SELECT count(*) FROM trip_events
		 WHERE itinerary_id = $1 AND (before_json IS NOT NULL OR after_json IS NOT NULL OR revision_id IS NOT NULL)`,
		itineraryID).Scan(&withPayload))
	assert.Greater(t, withPayload, 0)
}
