package server_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bradleygichuru/ytci-go/internal/config"
	"github.com/bradleygichuru/ytci-go/internal/db"
	"github.com/bradleygichuru/ytci-go/internal/server"
)

const (
	privacyUserID  = "11111111-1111-1111-1111-111111111111"
	privacyMinorID = "22222222-2222-2222-2222-222222222222"
	privacyAdminID = "33333333-3333-3333-3333-333333333333"
)

func bearer(token string) func(*http.Request) {
	return func(req *http.Request) {
		req.Header.Set("Authorization", "Bearer "+token)
	}
}

func setupPrivacyServer(t *testing.T, analyticsV2 bool) (*httptest.Server, *pgxpool.Pool, string) {
	t.Helper()
	ctx := context.Background()
	pool := db.SetupTestDB(t)
	t.Cleanup(func() { pool.Close() })
	setupAuthTables(t, ctx, pool)

	userID := privacyUserID
	const token = "privacy-token"
	createSession(t, ctx, pool, userID, "privacy@test.com", token)
	_, err := pool.Exec(ctx, `
		INSERT INTO user_profiles (user_id, created_by, age_range, display_name)
		VALUES ($1, $1, '18-24', 'Privacy User')`, userID)
	require.NoError(t, err)

	cfg := &config.Config{
		Port:                      "8080",
		DatabaseURL:               "ignored",
		AdminJWKSURL:              "http://test.invalid/jwks",
		CORSOrigins:               "*",
		ThenNowAnalyticsV2Enabled: analyticsV2,
	}
	ts := httptest.NewServer(server.New(cfg, pool))
	t.Cleanup(ts.Close)
	return ts, pool, token
}

func TestConsentVersioningAndAgeGate(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}
	ts, pool, token := setupPrivacyServer(t, false)
	auth := bearer(token)

	resp := doJSON(t, ts, "GET", "/v1/public/then-now/config", nil, nil)
	require.Equal(t, http.StatusOK, resp.StatusCode)
	configBody := decodeBody(t, resp)
	assert.Equal(t, false, configBody["recognitionEnabled"])
	assert.Equal(t, false, configBody["publicRecognitionEnabled"])
	assert.Equal(t, false, configBody["analyticsV2Enabled"])

	resp = doJSON(t, ts, "GET", "/v1/mobile/profile/consent", nil, auth)
	require.Equal(t, http.StatusOK, resp.StatusCode)
	body := decodeBody(t, resp)
	assert.Equal(t, false, body["recognition"])
	assert.Equal(t, "2026-11-18", body["version"])

	resp = doJSON(t, ts, "PATCH", "/v1/mobile/profile/consent", map[string]any{
		"recognition":          true,
		"locationVerification": true,
		"publicProfile":        true,
		"leaderboard":          true,
		"analytics":            true,
	}, auth)
	require.Equal(t, http.StatusOK, resp.StatusCode)
	resp.Body.Close()

	var recognition, locationVerification, publicProfile, leaderboard, analytics bool
	var version string
	require.NoError(t, pool.QueryRow(context.Background(), `
		SELECT consent_recognition, consent_location_verification, consent_public_profile,
		       consent_leaderboard, consent_analytics, COALESCE(consent_version, '')
		FROM user_profiles WHERE user_id = '11111111-1111-1111-1111-111111111111'`).Scan(
		&recognition, &locationVerification, &publicProfile, &leaderboard, &analytics, &version,
	))
	assert.True(t, recognition)
	assert.True(t, locationVerification)
	assert.True(t, publicProfile)
	assert.True(t, leaderboard)
	assert.True(t, analytics)
	assert.Equal(t, "2026-11-18", version)

	var records int
	require.NoError(t, pool.QueryRow(context.Background(),
		`SELECT count(*) FROM consent_records WHERE user_id = '11111111-1111-1111-1111-111111111111'`).Scan(&records))
	assert.Equal(t, 1, records)

	// Public visibility cannot survive without recognition consent.
	resp = doJSON(t, ts, "PATCH", "/v1/mobile/profile/consent", map[string]any{
		"recognition":   false,
		"publicProfile": true,
	}, auth)
	assert.Equal(t, http.StatusBadRequest, resp.StatusCode)
	resp.Body.Close()

	// Location verification also requires recognition consent.
	resp = doJSON(t, ts, "PATCH", "/v1/mobile/profile/consent", map[string]any{
		"recognition":          false,
		"locationVerification": true,
	}, auth)
	assert.Equal(t, http.StatusBadRequest, resp.StatusCode)
	resp.Body.Close()

	// Under-18s cannot enable server-side recognition.
	ctx := context.Background()
	createSession(t, ctx, pool, privacyMinorID, "minor@test.com", "privacy-minor-token")
	_, err := pool.Exec(ctx, `
		INSERT INTO user_profiles (user_id, created_by, age_range, display_name)
		VALUES ('22222222-2222-2222-2222-222222222222', '22222222-2222-2222-2222-222222222222', 'Under 18', 'Minor')`)
	require.NoError(t, err)
	resp = doJSON(t, ts, "PATCH", "/v1/mobile/profile/consent", map[string]any{
		"recognition": true,
	}, bearer("privacy-minor-token"))
	assert.Equal(t, http.StatusForbidden, resp.StatusCode)
	resp.Body.Close()
}

func TestAnalyticsV2UsesPseudonymousID(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}
	ts, pool, token := setupPrivacyServer(t, true)
	auth := bearer(token)
	ctx := context.Background()

	_, err := pool.Exec(ctx, `UPDATE user_profiles SET consent_analytics = true WHERE user_id = '11111111-1111-1111-1111-111111111111'`)
	require.NoError(t, err)

	resp := doJSON(t, ts, "POST", "/v1/mobile/analytics/app-open", map[string]any{
		"platform": "android", "appVersion": "1.0.0",
	}, auth)
	require.Equal(t, http.StatusOK, resp.StatusCode)
	resp.Body.Close()

	var analyticsID string
	var userID *string
	require.NoError(t, pool.QueryRow(ctx,
		`SELECT analytics_id::text, user_id::text FROM app_opens ORDER BY opened_at DESC LIMIT 1`).Scan(&analyticsID, &userID))
	assert.NotEmpty(t, analyticsID)
	assert.Nil(t, userID)

	var mappings int
	require.NoError(t, pool.QueryRow(ctx, `SELECT count(*) FROM analytics_id_map WHERE user_id = '11111111-1111-1111-1111-111111111111'`).Scan(&mappings))
	assert.Equal(t, 1, mappings)

	// A second app open inside five minutes is deduplicated by analytics ID.
	resp = doJSON(t, ts, "POST", "/v1/mobile/analytics/app-open", map[string]any{
		"platform": "android", "appVersion": "1.0.0",
	}, auth)
	require.Equal(t, http.StatusOK, resp.StatusCode)
	resp.Body.Close()
	var opens int
	require.NoError(t, pool.QueryRow(ctx, `SELECT count(*) FROM app_opens`).Scan(&opens))
	assert.Equal(t, 1, opens)

	resp = doJSON(t, ts, "POST", "/v1/mobile/analytics", map[string]any{
		"event": "map_opened", "properties": map[string]any{"surface": "then-now"}, "platform": "android", "appVersion": "1.0.0",
	}, auth)
	require.Equal(t, http.StatusCreated, resp.StatusCode)
	resp.Body.Close()
	var eventAnalyticsID string
	var eventUserID *string
	require.NoError(t, pool.QueryRow(ctx,
		`SELECT analytics_id::text, user_id::text FROM analytics_events LIMIT 1`).Scan(&eventAnalyticsID, &eventUserID))
	assert.NotEmpty(t, eventAnalyticsID)
	assert.Nil(t, eventUserID)

	// Turning analytics consent off stops future writes.
	resp = doJSON(t, ts, "PATCH", "/v1/mobile/profile/consent", map[string]any{"analytics": false}, auth)
	require.Equal(t, http.StatusOK, resp.StatusCode)
	resp.Body.Close()
	resp = doJSON(t, ts, "POST", "/v1/mobile/analytics/app-open", map[string]any{
		"platform": "android", "appVersion": "1.0.0",
	}, auth)
	require.Equal(t, http.StatusOK, resp.StatusCode)
	assert.Equal(t, "ignored", decodeBody(t, resp)["status"])

	// Account deletion removes the mapping, tombstones raw analytics rows, and
	// anonymises consent history (ADR 0016 §8).
	_, err = pool.Exec(ctx, `CREATE TABLE IF NOT EXISTS audit_logs (
		id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
		user_id TEXT,
		created_at TIMESTAMP NOT NULL DEFAULT now()
	)`)
	require.NoError(t, err)
	_, err = pool.Exec(ctx, `CREATE TABLE IF NOT EXISTS accounts (
		id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
		user_id TEXT NOT NULL
	)`)
	require.NoError(t, err)
	resp = doJSON(t, ts, "POST", "/v1/mobile/account/delete", map[string]any{"confirm": true}, auth)
	require.Equal(t, http.StatusOK, resp.StatusCode)
	resp.Body.Close()

	require.NoError(t, pool.QueryRow(ctx, `SELECT count(*) FROM analytics_id_map`).Scan(&mappings))
	assert.Equal(t, 0, mappings)
	var events int
	require.NoError(t, pool.QueryRow(ctx, `SELECT count(*) FROM analytics_events`).Scan(&events))
	assert.Equal(t, 0, events)
	var consentUser string
	require.NoError(t, pool.QueryRow(ctx, `SELECT user_id FROM consent_records LIMIT 1`).Scan(&consentUser))
	assert.NotEqual(t, privacyUserID, consentUser)
	assert.Contains(t, consentUser, "deleted-")
}

func TestAnonymousHeritageCounts(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}
	ts, pool, _ := setupPrivacyServer(t, false)
	ctx := context.Background()

	_, err := pool.Exec(ctx, `
		INSERT INTO heritage_sites (name, slug, county, site_type, access_mode, sensitivity, status)
		VALUES ('Privacy Standard', 'privacy-standard', 'Nairobi', 'historical', 'video_only', 'standard', 'published'),
		       ('Privacy Sensitive', 'privacy-sensitive', 'Nairobi', 'historical', 'video_only', 'sensitive', 'published')`)
	require.NoError(t, err)

	for i := 0; i < 5; i++ {
		resp := doJSON(t, ts, "POST", "/v1/public/then-now/events", map[string]any{
			"event": "portal_opened", "siteSlug": "privacy-standard",
		}, nil)
		require.Equal(t, http.StatusNoContent, resp.StatusCode)
		resp.Body.Close()
	}
	resp := doJSON(t, ts, "POST", "/v1/public/then-now/events", map[string]any{
		"event": "portal_opened", "siteSlug": "privacy-sensitive",
	}, nil)
	require.Equal(t, http.StatusNoContent, resp.StatusCode)
	resp.Body.Close()

	var standardCount int64
	var standardScope string
	require.NoError(t, pool.QueryRow(ctx, `
		SELECT count, scope FROM anonymous_heritage_counts
		WHERE event = 'portal_opened' AND scope = 'site'`).Scan(&standardCount, &standardScope))
	assert.Equal(t, int64(5), standardCount)
	assert.Equal(t, "site", standardScope)

	var sensitiveCount int64
	var sensitiveSite *string
	require.NoError(t, pool.QueryRow(ctx, `
		SELECT count, site_id::text FROM anonymous_heritage_counts
		WHERE event = 'portal_opened' AND scope = 'sensitive'`).Scan(&sensitiveCount, &sensitiveSite))
	assert.Equal(t, int64(1), sensitiveCount)
	assert.Nil(t, sensitiveSite)

	// An invalid event is rejected.
	resp = doJSON(t, ts, "POST", "/v1/public/then-now/events", map[string]any{
		"event": "raw_location", "siteSlug": "privacy-standard",
	}, nil)
	assert.Equal(t, http.StatusBadRequest, resp.StatusCode)
	resp.Body.Close()

	// Admin read suppresses the sensitive count below 5 and exposes the site count.
	_, err = pool.Exec(ctx, `INSERT INTO users (id, name, email, created_at, updated_at, role)
		VALUES ('33333333-3333-3333-3333-333333333333', 'Admin', 'privacy-admin@test.com', now(), now(), 'administrator')`)
	require.NoError(t, err)
	_, err = pool.Exec(ctx, `INSERT INTO sessions (id, expires_at, token, created_at, updated_at, user_id)
		VALUES ('sess-privacy-admin', now() + interval '1 day', 'privacy-admin-token', now(), now(), '33333333-3333-3333-3333-333333333333')`)
	require.NoError(t, err)
	resp = doJSON(t, ts, "GET", "/v1/analytics/heritage", nil, bearer("privacy-admin-token"))
	require.Equal(t, http.StatusOK, resp.StatusCode)
	body := decodeBody(t, resp)
	items := body["items"].([]any)
	require.Len(t, items, 1)
	item := items[0].(map[string]any)
	assert.Equal(t, "site", item["scope"])
	assert.Equal(t, "Privacy Standard", item["siteName"])
	assert.Equal(t, float64(5), item["count"])

}

func TestPublicHeritageEventRateLimited(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}
	ts, pool, _ := setupPrivacyServer(t, false)
	ctx := context.Background()

	_, err := pool.Exec(ctx, `
		INSERT INTO heritage_sites (name, slug, county, site_type, access_mode, sensitivity, status)
		VALUES ('Rate Limit Site', 'rate-limit-site', 'Nairobi', 'historical', 'video_only', 'standard', 'published')`)
	require.NoError(t, err)

	fromIP := func(req *http.Request) {
		req.Header.Set("X-Forwarded-For", "203.0.113.10")
	}
	payload := map[string]any{"event": "portal_opened", "siteSlug": "rate-limit-site"}

	for i := 0; i < 20; i++ {
		resp := doJSON(t, ts, "POST", "/v1/public/then-now/events", payload, fromIP)
		require.Equal(t, http.StatusNoContent, resp.StatusCode, "request %d should be allowed", i+1)
		resp.Body.Close()
	}

	resp := doJSON(t, ts, "POST", "/v1/public/then-now/events", payload, fromIP)
	require.Equal(t, http.StatusTooManyRequests, resp.StatusCode)
	assert.Equal(t, "60", resp.Header.Get("Retry-After"))
	body := decodeBody(t, resp)
	errBody := body["error"].(map[string]any)
	assert.Equal(t, "RATE_LIMITED", errBody["code"])
}

func TestPrivacyExport(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}
	ts, pool, token := setupPrivacyServer(t, false)
	auth := bearer(token)
	ctx := context.Background()

	resp := doJSON(t, ts, "PATCH", "/v1/mobile/profile/consent", map[string]any{
		"recognition": true,
		"analytics":   true,
	}, auth)
	require.Equal(t, http.StatusOK, resp.StatusCode)
	resp.Body.Close()

	var destinationID string
	require.NoError(t, pool.QueryRow(ctx, `
		INSERT INTO destinations (id, name, slug, county, category, status)
		VALUES (gen_random_uuid(), 'Privacy Destination', 'privacy-destination', 'Nairobi', 'nature', 'published')
		RETURNING id::text`).Scan(&destinationID))
	_, err := pool.Exec(ctx, `
		INSERT INTO bucket_list_items (user_id, destination_id, visited, visited_at)
		VALUES ($1, $2, true, now())`, privacyUserID, destinationID)
	require.NoError(t, err)

	resp = doJSON(t, ts, "GET", "/v1/mobile/privacy/export", nil, auth)
	require.Equal(t, http.StatusOK, resp.StatusCode)
	body := decodeBody(t, resp)

	assert.Equal(t, "ytci@tourism.go.ke", body["contact"])
	consent := body["consent"].(map[string]any)
	assert.Equal(t, true, consent["recognition"])
	assert.Equal(t, true, consent["analytics"])
	assert.Len(t, body["consentHistory"], 1)
	visits := body["destinationVisits"].([]any)
	require.Len(t, visits, 1)
	assert.Equal(t, "Privacy Destination", visits[0].(map[string]any)["name"])
	recognition := body["recognition"].(map[string]any)
	assert.Equal(t, "local_only", recognition["status"])
	analytics := body["analytics"].(map[string]any)
	assert.Equal(t, true, analytics["consent"])
}
