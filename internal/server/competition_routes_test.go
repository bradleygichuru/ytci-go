package server_test

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bradleygichuru/ytci-go/internal/config"
	"github.com/bradleygichuru/ytci-go/internal/db"
	"github.com/bradleygichuru/ytci-go/internal/server"
)

// TestCompetitionRoutesMounted proves the public surface answers without auth,
// the app surface requires a token, and the admin surface is guarded even
// though it uses its own competition gate rather than platform AdminGate.
func TestCompetitionRoutesMounted(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}
	pool := db.SetupTestDB(t)
	defer pool.Close()

	cfg := &config.Config{
		Port:         "8080",
		DatabaseURL:  "ignored",
		AdminJWKSURL: "http://test.invalid/jwks",
		CORSOrigins:  "*",
	}
	ts := httptest.NewServer(server.New(cfg, pool))
	defer ts.Close()

	for _, path := range []string{
		"/v1/public/competition/settings",
		"/v1/public/competition/editions",
	} {
		resp, err := http.Get(ts.URL + path)
		require.NoError(t, err)
		assert.Equal(t, http.StatusOK, resp.StatusCode, path)
		resp.Body.Close()
	}

	for _, path := range []string{
		"/v1/mobile/competition/editions",
		"/v1/admin/competition/editions",
	} {
		resp, err := http.Get(ts.URL + path)
		require.NoError(t, err)
		assert.Equal(t, http.StatusUnauthorized, resp.StatusCode, path)
		resp.Body.Close()
	}
}
