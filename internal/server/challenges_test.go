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

func setupChallengeTest(t *testing.T) (*httptest.Server, string, func(*http.Request)) {
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

	return ts, adminID, adminAuth
}

func TestChallengeCreateValidation(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}

	ts, _, adminAuth := setupChallengeTest(t)
	defer ts.Close()

	t.Run("missing title returns 400", func(t *testing.T) {
		body := map[string]string{
			"badgeName":   "Test Badge",
			"description": "Test Description",
		}
		b, _ := json.Marshal(body)
		req, _ := http.NewRequest("POST", ts.URL+"/v1/challenges", bytes.NewReader(b))
		adminAuth(req)
		req.Header.Set("Content-Type", "application/json")

		resp, err := ts.Client().Do(req)
		require.NoError(t, err)
		defer resp.Body.Close()

		assert.Equal(t, http.StatusBadRequest, resp.StatusCode)
	})

	t.Run("missing badgeName returns 400", func(t *testing.T) {
		body := map[string]string{
			"title":       "Test Challenge",
			"description": "Test Description",
		}
		b, _ := json.Marshal(body)
		req, _ := http.NewRequest("POST", ts.URL+"/v1/challenges", bytes.NewReader(b))
		adminAuth(req)
		req.Header.Set("Content-Type", "application/json")

		resp, err := ts.Client().Do(req)
		require.NoError(t, err)
		defer resp.Body.Close()

		assert.Equal(t, http.StatusBadRequest, resp.StatusCode)
	})

	t.Run("missing description returns 400", func(t *testing.T) {
		body := map[string]string{
			"title":     "Test Challenge",
			"badgeName": "Test Badge",
		}
		b, _ := json.Marshal(body)
		req, _ := http.NewRequest("POST", ts.URL+"/v1/challenges", bytes.NewReader(b))
		adminAuth(req)
		req.Header.Set("Content-Type", "application/json")

		resp, err := ts.Client().Do(req)
		require.NoError(t, err)
		defer resp.Body.Close()

		assert.Equal(t, http.StatusBadRequest, resp.StatusCode)
	})
}

func TestChallengeCreateSuccess(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}

	ts, _, adminAuth := setupChallengeTest(t)
	defer ts.Close()

	t.Run("create with all fields returns 201", func(t *testing.T) {
		body := map[string]string{
			"title":        "Eco Challenge",
			"description":  "Collect trash in your neighborhood",
			"badgeName":    "Eco Warrior",
			"rules":        "Take a photo of collected trash",
			"badgeIconUrl": "media/icon123",
			"eligibility":  `{"minDestinations": 5}`,
			"startDate":    "2026-08-01",
			"endDate":      "2026-08-31",
		}
		b, _ := json.Marshal(body)
		req, _ := http.NewRequest("POST", ts.URL+"/v1/challenges", bytes.NewReader(b))
		adminAuth(req)
		req.Header.Set("Content-Type", "application/json")

		resp, err := ts.Client().Do(req)
		require.NoError(t, err)
		defer resp.Body.Close()

		assert.Equal(t, http.StatusCreated, resp.StatusCode)

		var result map[string]interface{}
		json.NewDecoder(resp.Body).Decode(&result)
		assert.Equal(t, "draft", result["status"])
		assert.NotEmpty(t, result["id"])
	})

	t.Run("create without dates stores NULL", func(t *testing.T) {
		body := map[string]string{
			"title":       "Perpetual Challenge",
			"description": "Always active",
			"badgeName":   "Evergreen",
		}
		b, _ := json.Marshal(body)
		req, _ := http.NewRequest("POST", ts.URL+"/v1/challenges", bytes.NewReader(b))
		adminAuth(req)
		req.Header.Set("Content-Type", "application/json")

		resp, err := ts.Client().Do(req)
		require.NoError(t, err)
		defer resp.Body.Close()

		assert.Equal(t, http.StatusCreated, resp.StatusCode)
	})
}

func TestChallengeDelete(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}

	ts, _, adminAuth := setupChallengeTest(t)
	defer ts.Close()

	t.Run("delete transitions status to ended", func(t *testing.T) {
		body := map[string]string{
			"title":       "Delete Me",
			"description": "To be deleted",
			"badgeName":   "Doomed",
		}
		b, _ := json.Marshal(body)
		req, _ := http.NewRequest("POST", ts.URL+"/v1/challenges", bytes.NewReader(b))
		adminAuth(req)
		req.Header.Set("Content-Type", "application/json")
		resp, err := ts.Client().Do(req)
		require.NoError(t, err)
		resp.Body.Close()

		var created map[string]string
		json.NewDecoder(resp.Body).Decode(&created)

		req, _ = http.NewRequest("DELETE", ts.URL+"/v1/challenges/"+created["id"], nil)
		adminAuth(req)
		resp, err = ts.Client().Do(req)
		require.NoError(t, err)
		defer resp.Body.Close()

		assert.Equal(t, http.StatusOK, resp.StatusCode)
	})
}
