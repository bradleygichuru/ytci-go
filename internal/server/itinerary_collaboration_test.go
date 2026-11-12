package server_test

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bradleygichuru/ytci-go/internal/config"
	"github.com/bradleygichuru/ytci-go/internal/db"
	"github.com/bradleygichuru/ytci-go/internal/server"
)

type collaborationUser struct {
	ID    string
	Token string
}

func TestItineraryCollaborationAuthorization(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}

	ctx := context.Background()
	pool := db.SetupTestDB(t)
	defer pool.Close()

	cfg := &config.Config{
		Port:         "8080",
		DatabaseURL:  "ignored",
		AdminJWKSURL: "http://test.invalid/jwks",
		CORSOrigins:  "*",
	}
	_, err := pool.Exec(ctx, `
		CREATE TABLE IF NOT EXISTS sessions (
			id TEXT PRIMARY KEY,
			expires_at TIMESTAMP NOT NULL,
			token TEXT NOT NULL UNIQUE,
			created_at TIMESTAMP NOT NULL,
			updated_at TIMESTAMP NOT NULL,
			user_id TEXT NOT NULL,
			ip_address TEXT,
			user_agent TEXT,
			impersonated_by TEXT
		)
	`)
	require.NoError(t, err)

	ts := httptest.NewServer(server.New(cfg, pool))
	defer ts.Close()

	createUser := func(name string) collaborationUser {
		t.Helper()
		var u collaborationUser
		err := pool.QueryRow(ctx,
			`INSERT INTO users (id, name, email, email_verified, created_at, updated_at, role)
			 VALUES (gen_random_uuid()::text, $1, gen_random_uuid()::text || '@example.com', true, now(), now(), 'user')
			 RETURNING id`,
			name,
		).Scan(&u.ID)
		require.NoError(t, err)
		u.Token = "session-" + u.ID
		_, err = pool.Exec(ctx,
			`INSERT INTO sessions (id, expires_at, token, created_at, updated_at, user_id)
			 VALUES (gen_random_uuid()::text, now() + interval '1 day', $1, now(), now(), $2)`,
			u.Token, u.ID)
		require.NoError(t, err)
		return u
	}

	owner := createUser("Owner")
	editor := createUser("Editor")
	viewer := createUser("Viewer")
	outsider := createUser("Outsider")

	request := func(method, path, token string, body any) (int, map[string]any) {
		t.Helper()
		var reader *bytes.Reader
		if body == nil {
			reader = bytes.NewReader(nil)
		} else {
			raw, err := json.Marshal(body)
			require.NoError(t, err)
			reader = bytes.NewReader(raw)
		}
		req, err := http.NewRequest(method, ts.URL+path, reader)
		require.NoError(t, err)
		req.Header.Set("Authorization", "Bearer "+token)
		req.Header.Set("Content-Type", "application/json")
		res, err := http.DefaultClient.Do(req)
		require.NoError(t, err)
		defer res.Body.Close()
		var out map[string]any
		_ = json.NewDecoder(res.Body).Decode(&out)
		return res.StatusCode, out
	}

	status, created := request(http.MethodPost, "/v1/mobile/itineraries", owner.Token, map[string]any{
		"title": "Rift Valley", "origin": "Nairobi", "days": 4, "interests": []string{"Wildlife"},
	})
	require.Equal(t, http.StatusCreated, status)
	itineraryID, _ := created["id"].(string)
	require.NotEmpty(t, itineraryID)

	_, err = pool.Exec(ctx,
		`INSERT INTO itinerary_members (itinerary_id, user_id, role) VALUES ($1, $2, 'editor')`,
		itineraryID, editor.ID)
	require.NoError(t, err)
	_, err = pool.Exec(ctx,
		`INSERT INTO itinerary_members (itinerary_id, user_id, role) VALUES ($1, $2, 'viewer')`,
		itineraryID, viewer.ID)
	require.NoError(t, err)

	for _, token := range []string{owner.Token, editor.Token, viewer.Token} {
		status, body := request(http.MethodGet, "/v1/mobile/itineraries/"+itineraryID, token, nil)
		assert.Equal(t, http.StatusOK, status)
		assert.Equal(t, itineraryID, body["id"])
	}
	status, _ = request(http.MethodGet, "/v1/mobile/itineraries/"+itineraryID, outsider.Token, nil)
	assert.Equal(t, http.StatusNotFound, status)

	status, _ = request(http.MethodGet, "/v1/mobile/itineraries/"+itineraryID+"/stops", outsider.Token, nil)
	assert.Equal(t, http.StatusNotFound, status)

	status, list := request(http.MethodGet, "/v1/mobile/itineraries", editor.Token, nil)
	assert.Equal(t, http.StatusOK, status)
	items, _ := list["items"].([]any)
	require.Len(t, items, 1)
	first, _ := items[0].(map[string]any)
	assert.Equal(t, "editor", first["role"])

	status, _ = request(http.MethodPatch, "/v1/mobile/itineraries/"+itineraryID, viewer.Token, map[string]any{"title": "No"})
	assert.Equal(t, http.StatusForbidden, status)

	status, updated := request(http.MethodPatch, "/v1/mobile/itineraries/"+itineraryID, editor.Token, map[string]any{"title": "Rift Valley 2"})
	assert.Equal(t, http.StatusOK, status)
	assert.Equal(t, float64(2), updated["version"])

	status, _ = request(http.MethodPut, "/v1/mobile/itineraries/"+itineraryID+"/stops", viewer.Token, map[string]any{
		"stops": []map[string]any{{"day": 1, "displayOrder": 0, "title": "Karura Forest"}},
	})
	assert.Equal(t, http.StatusForbidden, status)

	status, stopsUpdated := request(http.MethodPut, "/v1/mobile/itineraries/"+itineraryID+"/stops", editor.Token, map[string]any{
		"stops":           []map[string]any{{"day": 1, "displayOrder": 0, "title": "Karura Forest", "estimatedCost": "300"}},
		"expectedVersion": 2,
	})
	assert.Equal(t, http.StatusOK, status)
	assert.Equal(t, float64(3), stopsUpdated["version"])

	status, conflict := request(http.MethodPut, "/v1/mobile/itineraries/"+itineraryID+"/stops", editor.Token, map[string]any{
		"stops":           []map[string]any{{"day": 1, "displayOrder": 0, "title": "Stale"}},
		"expectedVersion": 2,
	})
	assert.Equal(t, http.StatusConflict, status)
	assert.Equal(t, float64(3), conflict["currentVersion"])
	errObj, _ := conflict["error"].(map[string]any)
	assert.Equal(t, "VERSION_CONFLICT", errObj["code"])

	status, stops := request(http.MethodGet, "/v1/mobile/itineraries/"+itineraryID+"/stops", editor.Token, nil)
	assert.Equal(t, http.StatusOK, status)
	assert.Equal(t, float64(3), stops["version"])
	stopList, _ := stops["stops"].([]any)
	require.Len(t, stopList, 1)
	stop, _ := stopList[0].(map[string]any)
	assert.Equal(t, "Karura Forest", stop["title"])
	assert.NotEmpty(t, stop["id"])

	status, _ = request(http.MethodDelete, "/v1/mobile/itineraries/"+itineraryID, editor.Token, nil)
	assert.Equal(t, http.StatusForbidden, status)
	status, _ = request(http.MethodPost, "/v1/mobile/itineraries/"+itineraryID+"/duplicate", viewer.Token, nil)
	assert.Equal(t, http.StatusForbidden, status)

	status, duplicated := request(http.MethodPost, "/v1/mobile/itineraries/"+itineraryID+"/duplicate", editor.Token, nil)
	assert.Equal(t, http.StatusCreated, status)
	copyID, _ := duplicated["id"].(string)
	require.NotEmpty(t, copyID)

	var copyRole string
	require.NoError(t, pool.QueryRow(ctx,
		`SELECT role FROM itinerary_members WHERE itinerary_id = $1 AND user_id = $2`,
		copyID, editor.ID).Scan(&copyRole))
	assert.Equal(t, "owner", copyRole)
	var copyStops int
	require.NoError(t, pool.QueryRow(ctx,
		`SELECT COUNT(*) FROM itinerary_stops WHERE itinerary_id = $1`, copyID).Scan(&copyStops))
	assert.Equal(t, 1, copyStops)

	status, _ = request(http.MethodDelete, "/v1/mobile/itineraries/"+itineraryID, owner.Token, nil)
	assert.Equal(t, http.StatusOK, status)
	status, _ = request(http.MethodGet, "/v1/mobile/itineraries/"+itineraryID, owner.Token, nil)
	assert.Equal(t, http.StatusNotFound, status)
	status, ownerList := request(http.MethodGet, "/v1/mobile/itineraries", owner.Token, nil)
	assert.Equal(t, http.StatusOK, status)
	ownerItems, _ := ownerList["items"].([]any)
	assert.Empty(t, ownerItems)

	status, editorList := request(http.MethodGet, "/v1/mobile/itineraries", editor.Token, nil)
	assert.Equal(t, http.StatusOK, status)
	editorItems, _ := editorList["items"].([]any)
	require.Len(t, editorItems, 1)
	editorTrip, _ := editorItems[0].(map[string]any)
	assert.Equal(t, copyID, editorTrip["id"])

	var revisionCount, eventCount int
	require.NoError(t, pool.QueryRow(ctx,
		`SELECT COUNT(*) FROM plan_revisions WHERE itinerary_id = $1`, itineraryID).Scan(&revisionCount))
	require.NoError(t, pool.QueryRow(ctx,
		`SELECT COUNT(*) FROM trip_events WHERE itinerary_id = $1`, itineraryID).Scan(&eventCount))
	assert.GreaterOrEqual(t, revisionCount, 3)
	assert.GreaterOrEqual(t, eventCount, 4)

	status, outsiderList := request(http.MethodGet, "/v1/mobile/itineraries", outsider.Token, nil)
	assert.Equal(t, http.StatusOK, status)
	outsiderItems, _ := outsiderList["items"].([]any)
	assert.Empty(t, outsiderItems)
}
