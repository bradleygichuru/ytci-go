package server_test

import (
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

// TestRecognitionRoutesAreWired proves the single registration seam mounts
// every recognition surface and that the frozen shapes survive an empty
// database.
func TestRecognitionRoutesAreWired(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}
	ctx := context.Background()
	pool := db.SetupTestDB(t)
	defer pool.Close()
	setupAuthTables(t, ctx, pool)

	createSession(t, ctx, pool, "reader-r", "reader-r@example.test", "rec-reader")
	createSession(t, ctx, pool, "admin-r", "admin-r@example.test", "rec-admin")
	_, err := pool.Exec(ctx,
		`UPDATE users SET role='super_admin' WHERE id='admin-r'`)
	require.NoError(t, err)

	cfg := &config.Config{Port: "8080", DatabaseURL: "ignored",
		AdminJWKSURL: "http://test.invalid/jwks", CORSOrigins: "*"}
	ts := httptest.NewServer(server.New(cfg, pool))
	defer ts.Close()

	// Public season and the suppressed-by-default board.
	resp, err := http.Get(ts.URL + "/v1/public/seasons/2026-q4")
	require.NoError(t, err)
	require.Equal(t, http.StatusOK, resp.StatusCode)
	var season map[string]any
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&season))
	resp.Body.Close()
	assert.Equal(t, "2026-q4", season["key"])

	resp, err = http.Get(ts.URL + "/v1/public/leaderboard?seasonKey=2026-q4")
	require.NoError(t, err)
	require.Equal(t, http.StatusOK, resp.StatusCode)
	var board map[string]any
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&board))
	resp.Body.Close()
	assert.Equal(t, true, board["suppressed"])

	// Private overview.
	req := authReq(t, http.MethodGet, ts.URL+"/v1/mobile/recognition", "rec-reader")
	resp, err = ts.Client().Do(req)
	require.NoError(t, err)
	require.Equal(t, http.StatusOK, resp.StatusCode)
	var overview map[string]any
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&overview))
	resp.Body.Close()
	require.Contains(t, overview, "level")
	assert.EqualValues(t, 0, overview["totalXp"])

	// Second opt-in: consent plus an explicit join.
	body := map[string]any{
		"seasonKey": "2026-q4", "displayName": "Rift Reader",
		"consentVersion": "2026-11-01",
	}
	resp = doJSON(t, ts, http.MethodPost, "/v1/mobile/recognition/leaderboard/join",
		body, func(r *http.Request) { r.Header.Set("Authorization", "Bearer rec-reader") })
	require.Equal(t, http.StatusCreated, resp.StatusCode)
	var state map[string]any
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&state))
	resp.Body.Close()
	assert.Equal(t, true, state["optedIn"])
	assert.Equal(t, true, state["eligible"])

	// Admin surfaces.
	req = authReq(t, http.MethodGet, ts.URL+"/v1/admin/recognition/kill-switches", "rec-admin")
	resp, err = ts.Client().Do(req)
	require.NoError(t, err)
	require.Equal(t, http.StatusOK, resp.StatusCode)
	var switches map[string]any
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&switches))
	resp.Body.Close()
	items, ok := switches["items"].([]any)
	require.True(t, ok)
	assert.Len(t, items, 4)

	req = authReq(t, http.MethodGet, ts.URL+"/v1/admin/recognition/rules", "rec-admin")
	resp, err = ts.Client().Do(req)
	require.NoError(t, err)
	require.Equal(t, http.StatusOK, resp.StatusCode)
	resp.Body.Close()

	// A reader is not an admin.
	req = authReq(t, http.MethodGet, ts.URL+"/v1/admin/recognition/rules", "rec-reader")
	resp, err = ts.Client().Do(req)
	require.NoError(t, err)
	assert.Equal(t, http.StatusForbidden, resp.StatusCode)
	resp.Body.Close()
}
