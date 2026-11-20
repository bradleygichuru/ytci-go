package server_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bradleygichuru/ytci-go/internal/config"
	"github.com/bradleygichuru/ytci-go/internal/db"
	"github.com/bradleygichuru/ytci-go/internal/server"
)

func repositoryAuth(token string) func(*http.Request) {
	return func(req *http.Request) {
		req.Header.Set("Authorization", "Bearer "+token)
	}
}

// TestRepositoryRoutesMountedAndGuarded proves the server wiring: the public
// routes answer without auth, the admin routes require a token and the
// repository role gate rejects non-repository admins.
func TestRepositoryRoutesMountedAndGuarded(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}
	ctx := context.Background()
	pool := db.SetupTestDB(t)
	defer pool.Close()
	setupAuthTables(t, ctx, pool)

	users := []struct {
		id, role, token string
	}{
		{"repo-user", "user", "repo-user-token"},
		{"repo-officer", "county_officer", "repo-officer-token"},
		{"repo-editor", "content_editor", "repo-editor-token"},
		{"repo-moderator", "moderator", "repo-moderator-token"},
		{"repo-admin", "administrator", "repo-admin-token"},
	}
	for _, user := range users {
		_, err := pool.Exec(ctx, `
INSERT INTO users (id, name, email, created_at, updated_at, role)
VALUES ($1, $1, $1 || '@test.example', now(), now(), $2)`, user.id, user.role)
		require.NoError(t, err)
		_, err = pool.Exec(ctx, `
INSERT INTO sessions (id, expires_at, token, created_at, updated_at, user_id)
VALUES ($1, now() + interval '1 day', $2, now(), now(), $3)`,
			"sess-"+user.id, user.token, user.id)
		require.NoError(t, err)
	}

	cfg := &config.Config{
		Port:         "8080",
		DatabaseURL:  "ignored",
		AdminJWKSURL: "http://test.invalid/jwks",
		CORSOrigins:  "*",
	}
	ts := httptest.NewServer(server.New(cfg, pool))
	defer ts.Close()

	for _, path := range []string{
		"/v1/public/repository/entries",
		"/v1/public/repository/facets",
		"/v1/public/repository/stats",
		"/v1/public/repository/settings",
	} {
		resp := doJSON(t, ts, "GET", path, nil, nil)
		assert.Equal(t, http.StatusOK, resp.StatusCode, path)
		resp.Body.Close()
	}

	// Missing token is 401; a valid non-admin token is 403.
	resp := doJSON(t, ts, "GET", "/v1/admin/repository/entries", nil, nil)
	assert.Equal(t, http.StatusUnauthorized, resp.StatusCode)
	resp.Body.Close()
	for _, token := range []string{"repo-user-token", "repo-officer-token", "repo-editor-token"} {
		resp := doJSON(t, ts, "GET", "/v1/admin/repository/entries", nil, repositoryAuth(token))
		assert.Equal(t, http.StatusForbidden, resp.StatusCode, token)
		resp.Body.Close()
	}

	// Repository admins may read.
	for _, token := range []string{"repo-moderator-token", "repo-admin-token"} {
		resp := doJSON(t, ts, "GET", "/v1/admin/repository/entries", nil, repositoryAuth(token))
		assert.Equal(t, http.StatusOK, resp.StatusCode, token)
		resp.Body.Close()
	}

	// Moderators cannot create.
	resp = doJSON(t, ts, "POST", "/v1/admin/repository/entries", map[string]any{
		"kind": "experience", "title": "Blocked", "summary": "Nope",
	}, repositoryAuth("repo-moderator-token"))
	assert.Equal(t, http.StatusForbidden, resp.StatusCode)
	resp.Body.Close()
}
