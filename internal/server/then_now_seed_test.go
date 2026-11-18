package server_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bradleygichuru/ytci-go/internal/config"
	"github.com/bradleygichuru/ytci-go/internal/db"
	"github.com/bradleygichuru/ytci-go/internal/server"
)

func TestThenNowSeedIsIdempotentAndPublishedReadsWork(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}
	ctx := context.Background()
	pool := db.SetupTestDB(t)
	defer pool.Close()

	seed, err := os.ReadFile("../../cmd/seed-then-now/seed.sql")
	require.NoError(t, err)
	_, err = pool.Exec(ctx, string(seed))
	require.NoError(t, err)
	_, err = pool.Exec(ctx, string(seed))
	require.NoError(t, err)

	var sites, experiences, revisions int
	require.NoError(t, pool.QueryRow(ctx, `SELECT count(*) FROM heritage_sites`).Scan(&sites))
	require.NoError(t, pool.QueryRow(ctx, `SELECT count(*) FROM then_now_experiences`).Scan(&experiences))
	require.NoError(t, pool.QueryRow(ctx, `SELECT count(*) FROM then_now_revisions`).Scan(&revisions))
	assert.Equal(t, 4, sites)
	assert.Equal(t, 4, experiences)
	assert.Equal(t, 6, revisions)

	cfg := &config.Config{Port: "8080", DatabaseURL: "ignored", AdminJWKSURL: "http://test.invalid/jwks", CORSOrigins: "*"}
	ts := httptest.NewServer(server.New(cfg, pool))
	defer ts.Close()

	resp, err := http.Get(ts.URL + "/v1/public/then-now/sites")
	require.NoError(t, err)
	defer resp.Body.Close()
	require.Equal(t, http.StatusOK, resp.StatusCode)
	var body struct {
		Items []map[string]any `json:"items"`
	}
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&body))
	assert.Len(t, body.Items, 2)

	detail, err := http.Get(ts.URL + "/v1/public/then-now/sites/nairobi-railway-museum")
	require.NoError(t, err)
	defer detail.Body.Close()
	require.Equal(t, http.StatusOK, detail.StatusCode)
	var site struct {
		Site map[string]any `json:"site"`
	}
	require.NoError(t, json.NewDecoder(detail.Body).Decode(&site))
	assert.Equal(t, "Nairobi Railway Museum", site.Site["name"])
	experience := site.Site["experience"].(map[string]any)
	assert.Equal(t, "The railway that built Nairobi", experience["title"])
	renditionKeys := experience["renditionKeys"].(map[string]any)
	assert.Contains(t, renditionKeys["1080"], "1080.mp4")
	assert.NotEmpty(t, experience["posterKey"])
}
