package server_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
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

func setupChallengeTest(t *testing.T) (*httptest.Server, *pgxpool.Pool, string, func(*http.Request)) {
	ctx := context.Background()
	pool := db.SetupTestDB(t)
	t.Cleanup(func() { pool.Close() })

	_, err := pool.Exec(ctx, `
		CREATE TABLE IF NOT EXISTS users (
			id TEXT PRIMARY KEY,
			name TEXT NOT NULL,
			email TEXT NOT NULL UNIQUE,
			email_verified BOOLEAN DEFAULT false NOT NULL,
			image TEXT,
			created_at TIMESTAMP NOT NULL,
			updated_at TIMESTAMP NOT NULL,
			role TEXT,
			banned BOOLEAN DEFAULT false,
			ban_reason TEXT,
			ban_expires TIMESTAMP
		)
	`)
	require.NoError(t, err)

	_, err = pool.Exec(ctx, `
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

	adminID := "test-admin-001"
	sessionToken := "test-session-admin"

	_, err = pool.Exec(ctx, `
		INSERT INTO users (id, name, email, created_at, updated_at, role)
		VALUES ($1, 'Admin User', 'admin@test.com', now(), now(), 'super_admin')
	`, adminID)
	require.NoError(t, err)

	_, err = pool.Exec(ctx, `
		INSERT INTO sessions (id, expires_at, token, created_at, updated_at, user_id)
		VALUES ($1, now() + interval '1 day', $2, now(), now(), $3)
	`, "sess-admin-1", sessionToken, adminID)
	require.NoError(t, err)

	cfg := &config.Config{
		Port:         "8080",
		DatabaseURL:  "ignored",
		AdminJWKSURL: "http://test.invalid/jwks",
		CORSOrigins:  "*",
	}

	r := server.New(cfg, pool)
	ts := httptest.NewServer(r)

	adminAuth := func(req *http.Request) {
		req.Header.Set("Authorization", "Bearer "+sessionToken)
	}

	return ts, pool, adminID, adminAuth
}

func doJSON(t *testing.T, ts *httptest.Server, method, path string, body interface{}, auth func(*http.Request)) *http.Response {
	t.Helper()
	var b []byte
	if body != nil {
		b, _ = json.Marshal(body)
	}
	req, err := http.NewRequest(method, ts.URL+path, bytes.NewReader(b))
	require.NoError(t, err)
	if auth != nil {
		auth(req)
	}
	if b != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := ts.Client().Do(req)
	require.NoError(t, err)
	return resp
}

func decodeBody(t *testing.T, resp *http.Response) map[string]interface{} {
	t.Helper()
	defer resp.Body.Close()
	var result map[string]interface{}
	json.NewDecoder(resp.Body).Decode(&result)
	return result
}

func TestChallengeCreateValidation(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}

	ts, _, _, adminAuth := setupChallengeTest(t)
	defer ts.Close()

	t.Run("missing title returns 400", func(t *testing.T) {
		resp := doJSON(t, ts, "POST", "/v1/challenges", map[string]string{
			"badgeName":   "Test Badge",
			"description": "Test Description",
		}, adminAuth)
		assert.Equal(t, http.StatusBadRequest, resp.StatusCode)
		resp.Body.Close()
	})

	t.Run("missing badgeName returns 400", func(t *testing.T) {
		resp := doJSON(t, ts, "POST", "/v1/challenges", map[string]string{
			"title":       "Test Challenge",
			"description": "Test Description",
		}, adminAuth)
		assert.Equal(t, http.StatusBadRequest, resp.StatusCode)
		resp.Body.Close()
	})

	t.Run("missing description returns 400", func(t *testing.T) {
		resp := doJSON(t, ts, "POST", "/v1/challenges", map[string]string{
			"title":     "Test Challenge",
			"badgeName": "Test Badge",
		}, adminAuth)
		assert.Equal(t, http.StatusBadRequest, resp.StatusCode)
		resp.Body.Close()
	})
}

func TestChallengeCreateSuccess(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}

	ts, _, _, adminAuth := setupChallengeTest(t)
	defer ts.Close()

	t.Run("create with all fields returns 201", func(t *testing.T) {
		resp := doJSON(t, ts, "POST", "/v1/challenges", map[string]string{
			"title":        "Eco Challenge",
			"description":  "Collect trash in your neighborhood",
			"badgeName":    "Eco Warrior",
			"rules":        "Take a photo of collected trash",
			"badgeIconUrl": "media/icon123",
			"eligibility":  `{"minDestinations": 5}`,
			"startDate":    "2026-08-01",
			"endDate":      "2026-08-31",
		}, adminAuth)

		assert.Equal(t, http.StatusCreated, resp.StatusCode)
		result := decodeBody(t, resp)
		assert.Equal(t, "draft", result["status"])
		assert.NotEmpty(t, result["id"])
	})

	t.Run("create without dates stores NULL", func(t *testing.T) {
		resp := doJSON(t, ts, "POST", "/v1/challenges", map[string]string{
			"title":       "Perpetual Challenge",
			"description": "Always active",
			"badgeName":   "Evergreen",
		}, adminAuth)
		assert.Equal(t, http.StatusCreated, resp.StatusCode)
		resp.Body.Close()
	})
}

func TestChallengeDelete(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}

	ts, _, _, adminAuth := setupChallengeTest(t)
	defer ts.Close()

	t.Run("delete transitions status to ended", func(t *testing.T) {
		resp := doJSON(t, ts, "POST", "/v1/challenges", map[string]string{
			"title":       "Delete Me",
			"description": "To be deleted",
			"badgeName":   "Doomed",
		}, adminAuth)
		require.Equal(t, http.StatusCreated, resp.StatusCode)
		created := decodeBody(t, resp)

		resp = doJSON(t, ts, "DELETE", fmt.Sprintf("/v1/challenges/%s", created["id"]), nil, adminAuth)
		result := decodeBody(t, resp)
		assert.Equal(t, "ended", result["status"])
	})

	t.Run("delete non-existent returns 404", func(t *testing.T) {
		resp := doJSON(t, ts, "DELETE", "/v1/challenges/00000000-0000-0000-0000-000000000000", nil, adminAuth)
		assert.Equal(t, http.StatusNotFound, resp.StatusCode)
		resp.Body.Close()
	})
}

func TestChallengeUpdateValidation(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}

	ts, _, _, adminAuth := setupChallengeTest(t)
	defer ts.Close()

	resp := doJSON(t, ts, "POST", "/v1/challenges", map[string]string{
		"title":       "Update Me",
		"description": "To be updated",
		"badgeName":   "Updatable",
	}, adminAuth)
	require.Equal(t, http.StatusCreated, resp.StatusCode)
	created := decodeBody(t, resp)
	id := created["id"].(string)

	t.Run("empty title returns 400", func(t *testing.T) {
		resp := doJSON(t, ts, "PATCH", fmt.Sprintf("/v1/challenges/%s", id), map[string]string{
			"title": "",
		}, adminAuth)
		assert.Equal(t, http.StatusBadRequest, resp.StatusCode)
		resp.Body.Close()
	})

	t.Run("invalid status returns 400", func(t *testing.T) {
		resp := doJSON(t, ts, "PATCH", fmt.Sprintf("/v1/challenges/%s", id), map[string]string{
			"status": "bogus",
		}, adminAuth)
		assert.Equal(t, http.StatusBadRequest, resp.StatusCode)
		resp.Body.Close()
	})
}

func TestChallengeEvidence(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}

	ts, pool, _, adminAuth := setupChallengeTest(t)
	defer ts.Close()

	ctx := context.Background()
	userID := "test-evidence-user"
	challengeID := "00000000-0000-0000-0000-000000000001"
	evidenceID := "00000000-0000-0000-0000-000000000002"

	_, err := pool.Exec(ctx,
		`INSERT INTO challenges (id, title, description, badge_name, status)
		 VALUES ($1, 'Evidence Challenge', 'Test desc', 'Test Badge', 'active')`,
		challengeID)
	require.NoError(t, err)

	_, err = pool.Exec(ctx,
		`INSERT INTO challenge_progress (id, user_id, challenge_id, status)
		 VALUES ($1, $2, $3, 'submitted')`,
		evidenceID, userID, challengeID)
	require.NoError(t, err)

	t.Run("list evidence returns submitted items", func(t *testing.T) {
		resp := doJSON(t, ts, "GET", "/v1/challenges/evidence", nil, adminAuth)
		assert.Equal(t, http.StatusOK, resp.StatusCode)
		result := decodeBody(t, resp)

		items, ok := result["items"].([]interface{})
		require.True(t, ok)
		require.NotEmpty(t, items)

		item := items[0].(map[string]interface{})
		assert.Equal(t, evidenceID, item["id"])
		assert.Equal(t, "submitted", item["status"])
		assert.Equal(t, "Evidence Challenge", item["challengeTitle"])
	})

	t.Run("approve evidence sets approved and badge_awarded_at", func(t *testing.T) {
		resp := doJSON(t, ts, "POST", fmt.Sprintf("/v1/challenges/evidence/%s/review", evidenceID), map[string]string{
			"action": "approve",
			"note":   "good work",
		}, adminAuth)
		assert.Equal(t, http.StatusOK, resp.StatusCode)
		result := decodeBody(t, resp)
		assert.Equal(t, "approved", result["status"])

		var dbStatus string
		var badgeAwardedAt *string
		err := pool.QueryRow(ctx,
			`SELECT status, badge_awarded_at::text FROM challenge_progress WHERE id = $1`,
			evidenceID,
		).Scan(&dbStatus, &badgeAwardedAt)
		require.NoError(t, err)
		assert.Equal(t, "approved", dbStatus)
		assert.NotNil(t, badgeAwardedAt)
	})

	t.Run("reject evidence sets status to in_progress", func(t *testing.T) {
		rejectID := "00000000-0000-0000-0000-000000000003"
		_, err := pool.Exec(ctx,
			`INSERT INTO challenge_progress (id, user_id, challenge_id, status)
			 VALUES ($1, $2, $3, 'submitted')`,
			rejectID, userID, challengeID)
		require.NoError(t, err)

		resp := doJSON(t, ts, "POST", fmt.Sprintf("/v1/challenges/evidence/%s/review", rejectID), map[string]string{
			"action": "reject",
			"note":   "try again",
		}, adminAuth)
		assert.Equal(t, http.StatusOK, resp.StatusCode)
		result := decodeBody(t, resp)
		assert.Equal(t, "in_progress", result["status"])

		var dbStatus string
		var moderationNote *string
		err = pool.QueryRow(ctx,
			`SELECT status, moderation_note FROM challenge_progress WHERE id = $1`,
			rejectID,
		).Scan(&dbStatus, &moderationNote)
		require.NoError(t, err)
		assert.Equal(t, "in_progress", dbStatus)
		assert.NotNil(t, moderationNote)
		assert.Equal(t, "try again", *moderationNote)
	})
}
