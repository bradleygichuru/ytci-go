package server_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bradleygichuru/ytci-go/internal/config"
	"github.com/bradleygichuru/ytci-go/internal/db"
	"github.com/bradleygichuru/ytci-go/internal/server"
)

// TestAnalyticsRoutesAreWired proves the analytics seam mounts the mobile
// guest surfaces and the permission-gated admin surfaces from the frozen
// OpenAPI document.
func TestAnalyticsRoutesAreWired(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}
	ctx := context.Background()
	pool := db.SetupTestDB(t)
	defer pool.Close()
	setupAuthTables(t, ctx, pool)

	createSession(t, ctx, pool, "reader-a", "reader-a@example.test", "ana-reader")
	createSession(t, ctx, pool, "mod-a", "mod-a@example.test", "ana-mod")
	createSession(t, ctx, pool, "admin-a", "admin-a@example.test", "ana-admin")
	_, err := pool.Exec(ctx, `UPDATE users SET role='moderator' WHERE id='mod-a'`)
	require.NoError(t, err)
	_, err = pool.Exec(ctx, `UPDATE users SET role='administrator' WHERE id='admin-a'`)
	require.NoError(t, err)

	cfg := &config.Config{Port: "8080", DatabaseURL: "ignored",
		AdminJWKSURL: "http://test.invalid/jwks", CORSOrigins: "*"}
	ts := httptest.NewServer(server.New(cfg, pool))
	defer ts.Close()

	// Guest install is minted without a token.
	installID := uuid.NewString()
	resp := doJSON(t, ts, http.MethodPost, "/v1/mobile/analytics/installs",
		map[string]any{"installId": installID, "platform": "android", "appVersion": "1.0.0"}, nil)
	require.Equal(t, http.StatusCreated, resp.StatusCode)
	var identity map[string]any
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&identity))
	resp.Body.Close()
	analyticsID, _ := identity["analyticsId"].(string)
	require.NotEmpty(t, analyticsID)
	assert.Equal(t, "guest", identity["actorType"])

	// A consent write must prove the install that minted the analytics id.
	resp = doJSON(t, ts, http.MethodPut, "/v1/mobile/analytics/consent", map[string]any{
		"analyticsId": analyticsID,
		"installId":   uuid.NewString(),
		"version":     "2026-11-01",
		"state":       "granted",
		"source":      "guest_device",
	}, nil)
	require.Equal(t, http.StatusForbidden, resp.StatusCode)
	assert.Equal(t, "ANALYTICS_ID_UNKNOWN", errorCode(t, resp))

	resp = doJSON(t, ts, http.MethodPut, "/v1/mobile/analytics/consent", map[string]any{
		"analyticsId": analyticsID,
		"version":     "2026-11-01",
		"state":       "granted",
		"source":      "guest_device",
	}, nil)
	require.Equal(t, http.StatusBadRequest, resp.StatusCode)
	assert.Equal(t, "INSTALL_ID_REQUIRED", errorCode(t, resp))

	// Consent, then a batch.
	resp = doJSON(t, ts, http.MethodPut, "/v1/mobile/analytics/consent", map[string]any{
		"analyticsId": analyticsID,
		"installId":   installID,
		"version":     "2026-11-01",
		"state":       "granted",
		"source":      "guest_device",
	}, nil)
	require.Equal(t, http.StatusOK, resp.StatusCode)
	resp.Body.Close()

	sessionID := uuid.NewString()
	resp = doJSON(t, ts, http.MethodPost, "/v1/mobile/analytics/batches", map[string]any{
		"analyticsId": analyticsID, "installId": installID,
		"consentVersion": "2026-11-01", "appVersion": "1.0.0",
		"platform": "android", "actorType": "guest", "sessionId": sessionID,
		"events": []map[string]any{{
			"eventId": uuid.NewString(), "sessionId": sessionID, "sessionSeq": 1,
			"family": "screen_view", "name": "/feed",
			"clientTs": time.Now().UTC().Format(time.RFC3339),
		}},
	}, nil)
	require.Equal(t, http.StatusAccepted, resp.StatusCode)
	var ack map[string]any
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&ack))
	resp.Body.Close()
	assert.EqualValues(t, 1, ack["accepted"])

	// Under-18 identified analytics is gated off until #346.
	under18Body := map[string]any{"installId": uuid.NewString(), "platform": "ios"}
	resp = doJSON(t, ts, http.MethodPost, "/v1/mobile/analytics/installs", under18Body,
		func(r *http.Request) { r.Header.Set("X-Age-Declaration", "under_18") })
	assert.Equal(t, http.StatusUnavailableForLegalReasons, resp.StatusCode)
	resp.Body.Close()

	today := time.Now().UTC().Format("2006-01-02")
	overviewPath := "/v1/admin/analytics/overview?from=" + today + "&to=" + today
	var req *http.Request

	// Moderators may read analytics; readers may not.
	req = authReq(t, http.MethodGet, ts.URL+overviewPath, "ana-mod")
	resp, err = ts.Client().Do(req)
	require.NoError(t, err)
	require.Equal(t, http.StatusOK, resp.StatusCode)
	var overview map[string]any
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&overview))
	resp.Body.Close()
	assert.Contains(t, overview, "cells")

	req = authReq(t, http.MethodGet, ts.URL+overviewPath, "ana-reader")
	resp, err = ts.Client().Do(req)
	require.NoError(t, err)
	assert.Equal(t, http.StatusForbidden, resp.StatusCode)
	resp.Body.Close()

	// Export and erasure need the elevated analytics permissions.
	erasure := map[string]any{"subjectType": "analytics_id", "subjectRef": analyticsID}
	resp = doJSON(t, ts, http.MethodPost, "/v1/admin/analytics/erasure", erasure,
		func(r *http.Request) { r.Header.Set("Authorization", "Bearer ana-mod") })
	require.Equal(t, http.StatusForbidden, resp.StatusCode)
	resp.Body.Close()

	resp = doJSON(t, ts, http.MethodPost, "/v1/admin/analytics/erasure", erasure,
		func(r *http.Request) { r.Header.Set("Authorization", "Bearer ana-admin") })
	require.Equal(t, http.StatusAccepted, resp.StatusCode)
	var request map[string]any
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&request))
	resp.Body.Close()
	assert.Equal(t, "completed", request["status"])

	// Reader export returns their own document.
	req = authReq(t, http.MethodGet, ts.URL+"/v1/mobile/analytics/export", "ana-reader")
	resp, err = ts.Client().Do(req)
	require.NoError(t, err)
	require.Equal(t, http.StatusOK, resp.StatusCode)
	var exported map[string]any
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&exported))
	resp.Body.Close()
	assert.NotEmpty(t, exported["analyticsId"])
}

// TestAnalyticsAdminRequiresDates pins the required from/to parameters.
func TestAnalyticsAdminRequiresDates(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}
	ctx := context.Background()
	pool := db.SetupTestDB(t)
	defer pool.Close()
	setupAuthTables(t, ctx, pool)
	createSession(t, ctx, pool, "mod-b", "mod-b@example.test", "ana-mod-b")
	_, err := pool.Exec(ctx, `UPDATE users SET role='moderator' WHERE id='mod-b'`)
	require.NoError(t, err)

	cfg := &config.Config{Port: "8080", DatabaseURL: "ignored",
		AdminJWKSURL: "http://test.invalid/jwks", CORSOrigins: "*"}
	ts := httptest.NewServer(server.New(cfg, pool))
	defer ts.Close()

	req := authReq(t, http.MethodGet, ts.URL+"/v1/admin/analytics/overview", "ana-mod-b")
	resp, err := ts.Client().Do(req)
	require.NoError(t, err)
	assert.Equal(t, http.StatusBadRequest, resp.StatusCode)
	resp.Body.Close()
}
