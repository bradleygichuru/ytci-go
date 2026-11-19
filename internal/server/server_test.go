package server

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bradleygichuru/ytci-go/internal/config"
	"github.com/bradleygichuru/ytci-go/internal/db"
)

func TestSplitOrigins(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  []string
	}{
		{
			name:  "empty string",
			input: "",
			want:  []string{},
		},
		{
			name:  "wildcard",
			input: "*",
			want:  []string{"*"},
		},
		{
			name:  "single origin",
			input: "http://localhost:3000",
			want:  []string{"http://localhost:3000"},
		},
		{
			name:  "multiple origins",
			input: "http://localhost:3000,https://admin.example.com",
			want:  []string{"http://localhost:3000", "https://admin.example.com"},
		},
		{
			name:  "whitespace around origins",
			input: " http://localhost:3000 , https://admin.example.com ",
			want:  []string{"http://localhost:3000", "https://admin.example.com"},
		},
		{
			name:  "whitespace only",
			input: "   ",
			want:  []string{},
		},
		{
			name:  "commas with empty parts",
			input: "http://localhost:3000,,https://admin.example.com",
			want:  []string{"http://localhost:3000", "https://admin.example.com"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := splitOrigins(tt.input)
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("splitOrigins(%q) = %v, want %v", tt.input, got, tt.want)
			}
		})
	}
}

func TestReadinessWithMigratedDatabase(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping testcontainers readiness check in -short mode")
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
	ts := httptest.NewServer(New(cfg, pool))
	defer ts.Close()

	for _, path := range []string{"/healthz", "/readyz"} {
		resp, err := http.Get(ts.URL + path)
		require.NoError(t, err)
		var body struct {
			Status string            `json:"status"`
			Checks map[string]string `json:"checks"`
		}
		require.NoError(t, json.NewDecoder(resp.Body).Decode(&body))
		resp.Body.Close()
		assert.Equal(t, http.StatusOK, resp.StatusCode, path)
		assert.Equal(t, "ok", body.Status, path)
		if path == "/readyz" {
			assert.Equal(t, "ok", body.Checks["database"])
			assert.Equal(t, "ok", body.Checks["migrations"])
		}
	}

	// A missing contract registry must fail readiness, not report healthy.
	_, err := pool.Exec(ctx, `DROP TABLE platform_contract_versions`)
	require.NoError(t, err)
	resp, err := http.Get(ts.URL + "/readyz")
	require.NoError(t, err)
	defer resp.Body.Close()
	assert.Equal(t, http.StatusServiceUnavailable, resp.StatusCode)
}
