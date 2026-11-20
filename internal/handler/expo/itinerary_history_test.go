package expo

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bradleygichuru/ytci-go/internal/db"
)

func TestItineraryHistoryListsAndFilters(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}
	ctx := context.Background()
	pool := db.SetupTestDB(t)
	defer pool.Close()

	ownerID := newCollaborationTestUser(t, pool, "Wanjiku")
	viewerID := newCollaborationTestUser(t, pool, "Viewer")
	itineraryID := newCollaborationTestItinerary(t, pool, ownerID)
	require.NoError(t, AddMember(ctx, pool, itineraryID, viewerID, roleViewer, ownerID))

	require.NoError(t, recordTripEvent(ctx, pool, itineraryID, ownerID, "plan", "stops_updated",
		"stops", "", "Updated the trip plan",
		map[string]any{"version": 1}, map[string]any{"stops": 7}, ""))
	require.NoError(t, recordTripEvent(ctx, pool, itineraryID, viewerID, "budget", "expense_created",
		"expense", "", "Added an expense", nil, map[string]any{"amount": "KES 1,000"}, ""))
	require.NoError(t, recordTripEvent(ctx, pool, itineraryID, "", "members", "member_removed",
		"member", "", "Removed a member", nil, nil, ""))
	require.NoError(t, recordTripEvent(ctx, pool, itineraryID, viewerID, "plan", "updated",
		"itinerary", itineraryID, "Updated trip details", nil, nil, ""))

	h := NewItinerariesHandler(pool)
	history := func(userID, params string) *httptest.ResponseRecorder {
		t.Helper()
		req := collabRequest(t, http.MethodGet,
			"/v1/mobile/itineraries/"+itineraryID+"/history"+params, userID, nil)
		req.SetPathValue("id", itineraryID)
		rec := httptest.NewRecorder()
		h.ListHistory(rec, req)
		return rec
	}

	// A viewer can read the full history.
	rec := history(viewerID, "")
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	body := collabResponse(t, rec)
	rawEvents, ok := body["events"].([]any)
	require.True(t, ok)
	require.Len(t, rawEvents, 4)

	byAction := map[string]map[string]any{}
	for _, raw := range rawEvents {
		event, ok := raw.(map[string]any)
		require.True(t, ok)
		byAction[event["action"].(string)] = event
	}

	planned := byAction["stops_updated"]
	require.NotNil(t, planned)
	assert.Equal(t, "plan", planned["scope"])
	assert.Equal(t, "stops", planned["entityType"])
	assert.Equal(t, "", planned["entityId"])
	actor, ok := planned["actor"].(map[string]any)
	require.True(t, ok)
	assert.Equal(t, ownerID, actor["id"])
	assert.Equal(t, "Wanjiku", actor["name"])
	before, ok := planned["before"].(map[string]any)
	require.True(t, ok)
	assert.EqualValues(t, 1, before["version"])
	after, ok := planned["after"].(map[string]any)
	require.True(t, ok)
	assert.EqualValues(t, 7, after["stops"])

	former := byAction["member_removed"]
	require.NotNil(t, former)
	formerActor, ok := former["actor"].(map[string]any)
	require.True(t, ok)
	assert.Equal(t, "", formerActor["id"])
	assert.Equal(t, "Former member", formerActor["name"])
	assert.Nil(t, former["before"])
	assert.Nil(t, former["after"])

	// Scope filtering narrows to plan events only.
	rec = history(viewerID, "?scope=plan")
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	body = collabResponse(t, rec)
	rawEvents, ok = body["events"].([]any)
	require.True(t, ok)
	require.Len(t, rawEvents, 2)
	for _, raw := range rawEvents {
		assert.Equal(t, "plan", raw.(map[string]any)["scope"])
	}

	// actor=me keeps only the current user's events.
	rec = history(viewerID, "?actor=me")
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	body = collabResponse(t, rec)
	rawEvents, ok = body["events"].([]any)
	require.True(t, ok)
	require.Len(t, rawEvents, 2)
	for _, raw := range rawEvents {
		event := raw.(map[string]any)
		assert.Equal(t, viewerID, event["actor"].(map[string]any)["id"])
		assert.NotEqual(t, "stops_updated", event["action"])
	}

	// Pagination caps the page and returns a cursor when more remain.
	rec = history(viewerID, "?limit=2")
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	body = collabResponse(t, rec)
	rawEvents, ok = body["events"].([]any)
	require.True(t, ok)
	require.Len(t, rawEvents, 2)
	nextBefore, ok := body["nextBefore"].(string)
	require.True(t, ok)
	assert.NotEmpty(t, nextBefore)

	// The before cursor excludes newer events and keeps older ones.
	rec = history(viewerID, "?before=2999-01-01T00:00:00Z")
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	body = collabResponse(t, rec)
	rawEvents, ok = body["events"].([]any)
	require.True(t, ok)
	assert.Len(t, rawEvents, 4)

	rec = history(viewerID, "?before=2000-01-01T00:00:00Z")
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	body = collabResponse(t, rec)
	rawEvents, ok = body["events"].([]any)
	require.True(t, ok)
	assert.Empty(t, rawEvents)

	// Invalid scope is rejected.
	rec = history(viewerID, "?scope=receipts")
	require.Equal(t, http.StatusBadRequest, rec.Code, rec.Body.String())
	errBody := collabResponse(t, rec)
	errDetail := errBody["error"].(map[string]any)
	assert.Equal(t, "INVALID_SCOPE", errDetail["code"])
}

func TestRestoreRevisionCreatesNewRevision(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}
	ctx := context.Background()
	pool := db.SetupTestDB(t)
	defer pool.Close()

	ownerID := newCollaborationTestUser(t, pool, "Owner")
	itineraryID := newCollaborationTestItinerary(t, pool, ownerID)

	for i, title := range []string{"Lake day", "Sunset drive"} {
		_, err := pool.Exec(ctx,
			`INSERT INTO itinerary_stops (itinerary_id, day, display_order, title, category)
			 VALUES ($1, 1, $2, $3, 'Nature')`,
			itineraryID, i, title)
		require.NoError(t, err)
	}

	var sourceRevisionID string
	withCollaborationTx(t, pool, func(tx pgx.Tx) {
		var err error
		sourceRevisionID, err = insertPlanRevision(ctx, tx, itineraryID, 1, ownerID, "save")
		require.NoError(t, err)
	})
	require.NotEmpty(t, sourceRevisionID)

	// Simulate a later edit: change the trip, replace the stops, and bump the version.
	_, err := pool.Exec(ctx,
		`UPDATE itineraries SET title = 'Changed trip', status = 'saved', plan_version = 2, updated_at = now()
		 WHERE id = $1`, itineraryID)
	require.NoError(t, err)
	_, err = pool.Exec(ctx, `DELETE FROM itinerary_stops WHERE itinerary_id = $1`, itineraryID)
	require.NoError(t, err)
	_, err = pool.Exec(ctx,
		`INSERT INTO itinerary_stops (itinerary_id, day, display_order, title)
		 VALUES ($1, 2, 0, 'Replacement stop')`, itineraryID)
	require.NoError(t, err)

	h := NewItinerariesHandler(pool)
	req := collabRequest(t, http.MethodPost,
		"/v1/mobile/itineraries/"+itineraryID+"/revisions/"+sourceRevisionID+"/restore",
		ownerID, map[string]any{"expectedVersion": 2})
	req.SetPathValue("id", itineraryID)
	req.SetPathValue("revisionId", sourceRevisionID)
	rec := httptest.NewRecorder()
	h.RestoreRevision(rec, req)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	body := collabResponse(t, rec)
	assert.Equal(t, "restored", body["status"])
	assert.EqualValues(t, 3, body["version"])

	var title, status string
	var planVersion int
	require.NoError(t, pool.QueryRow(ctx,
		`SELECT title, status, plan_version FROM itineraries WHERE id = $1`, itineraryID).
		Scan(&title, &status, &planVersion))
	assert.Equal(t, "Test trip", title)
	assert.Equal(t, "draft", status)
	assert.Equal(t, 3, planVersion)

	rows, err := pool.Query(ctx,
		`SELECT title FROM itinerary_stops WHERE itinerary_id = $1 ORDER BY day, display_order`,
		itineraryID)
	require.NoError(t, err)
	defer rows.Close()
	restoredTitles := make([]string, 0)
	for rows.Next() {
		var stopTitle string
		require.NoError(t, rows.Scan(&stopTitle))
		restoredTitles = append(restoredTitles, stopTitle)
	}
	require.NoError(t, rows.Err())
	assert.Equal(t, []string{"Lake day", "Sunset drive"}, restoredTitles)

	var restoreRevisionID, restoreSnapshot string
	require.NoError(t, pool.QueryRow(ctx,
		`SELECT id::text, snapshot::text FROM plan_revisions
		 WHERE itinerary_id = $1 AND reason = 'restore' AND version = 3`,
		itineraryID).Scan(&restoreRevisionID, &restoreSnapshot))
	var snapshot planSnapshot
	require.NoError(t, json.Unmarshal([]byte(restoreSnapshot), &snapshot))
	assert.Equal(t, "Test trip", snapshot.Title)
	assert.Equal(t, "draft", snapshot.Status)
	require.Len(t, snapshot.Stops, 2)
	assert.Equal(t, "Lake day", snapshot.Stops[0].Title)

	var action, summary, beforeJSON, afterJSON, eventRevisionID string
	require.NoError(t, pool.QueryRow(ctx,
		`SELECT action, COALESCE(summary, ''), COALESCE(before_json::text, ''),
		        COALESCE(after_json::text, ''), COALESCE(revision_id::text, '')
		 FROM trip_events
		 WHERE itinerary_id = $1 AND action = 'restored'
		 ORDER BY created_at DESC, id DESC LIMIT 1`,
		itineraryID).Scan(&action, &summary, &beforeJSON, &afterJSON, &eventRevisionID))
	assert.Equal(t, "restored", action)
	assert.Equal(t, "Restored the plan to version 1", summary)
	assert.JSONEq(t, `{"version": 2}`, beforeJSON)
	assert.JSONEq(t, `{"version": 3}`, afterJSON)
	assert.Equal(t, restoreRevisionID, eventRevisionID)
}

func TestRestoreRevisionRejectsViewerAndStaleVersion(t *testing.T) {
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

	var revisionID string
	withCollaborationTx(t, pool, func(tx pgx.Tx) {
		var err error
		revisionID, err = insertPlanRevision(ctx, tx, itineraryID, 1, ownerID, "save")
		require.NoError(t, err)
	})

	h := NewItinerariesHandler(pool)
	restore := func(userID, targetRevision string, expected int) *httptest.ResponseRecorder {
		t.Helper()
		req := collabRequest(t, http.MethodPost,
			"/v1/mobile/itineraries/"+itineraryID+"/revisions/"+targetRevision+"/restore",
			userID, map[string]any{"expectedVersion": expected})
		req.SetPathValue("id", itineraryID)
		req.SetPathValue("revisionId", targetRevision)
		rec := httptest.NewRecorder()
		h.RestoreRevision(rec, req)
		return rec
	}

	rec := restore(viewerID, revisionID, 0)
	assert.Equal(t, http.StatusForbidden, rec.Code, rec.Body.String())

	rec = restore(ownerID, revisionID, 99)
	require.Equal(t, http.StatusConflict, rec.Code, rec.Body.String())
	body := collabResponse(t, rec)
	assert.Equal(t, "plan", body["scope"])
	assert.EqualValues(t, 1, body["currentVersion"])

	rec = restore(ownerID, "00000000-0000-0000-0000-000000000000", 0)
	assert.Equal(t, http.StatusNotFound, rec.Code, rec.Body.String())

	rec = restore(ownerID, "not-a-uuid", 0)
	assert.Equal(t, http.StatusNotFound, rec.Code, rec.Body.String())
}
