package expo

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bradleygichuru/ytci-go/internal/composer"
	"github.com/bradleygichuru/ytci-go/internal/db"
	"github.com/bradleygichuru/ytci-go/internal/middleware"
)

func TestComposerSuggestEndpoint(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}
	ctx := context.Background()
	pool := db.SetupTestDB(t)
	defer pool.Close()

	_, filename, _, ok := runtime.Caller(0)
	require.True(t, ok)
	seed, err := os.ReadFile(filepath.Join(filepath.Dir(filename), "..", "..", "..", "testdata", "seeds", "golden_destinations.sql"))
	require.NoError(t, err)
	_, err = pool.Exec(ctx, string(seed))
	require.NoError(t, err)

	h := NewComposerHandler(pool, "", "")

	body := `{
		"origin": {"name": "Nairobi", "latitude": -1.2921, "longitude": 36.8219},
		"days": 3,
		"interests": ["Wildlife", "Culture"],
		"budgetBand": "mid-range",
		"groupType": "couple",
		"groupSize": 2,
		"transport": "car",
		"pace": "balanced",
		"returnToOrigin": true
	}`
	req := httptest.NewRequest(http.MethodPost, "/v1/mobile/itineraries/suggest", strings.NewReader(body))
	req = req.WithContext(context.WithValue(req.Context(), middleware.CtxUserID, "composer-test-user"))
	rec := httptest.NewRecorder()
	h.Suggest(rec, req)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())

	var response composer.SuggestionResponse
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &response))
	require.Len(t, response.Suggestions, 3)
	assert.Equal(t, composer.EngineVersion, response.Meta.EngineVersion)
	assert.NotEmpty(t, response.Meta.InputHash)
	assert.NotEmpty(t, response.Coverage)
	for _, plan := range response.Suggestions {
		assert.NotEmpty(t, plan.Profile)
		assert.NotEmpty(t, plan.Days)
		assert.Equal(t, "KES", plan.EstimatedBudget.Currency)
		assert.True(t, plan.EstimatedBudget.Estimated)
	}

	// Invalid briefs are field-level 400s, not empty plans.
	bad := httptest.NewRequest(http.MethodPost, "/v1/mobile/itineraries/suggest", strings.NewReader(`{"days":0}`))
	bad = bad.WithContext(context.WithValue(bad.Context(), middleware.CtxUserID, "composer-test-user"))
	rec = httptest.NewRecorder()
	h.Suggest(rec, bad)
	assert.Equal(t, http.StatusBadRequest, rec.Code)
	assert.Contains(t, rec.Body.String(), "INVALID_BRIEF")
}

func TestComposerSuggestRateLimit(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}
	ctx := context.Background()
	pool := db.SetupTestDB(t)
	defer pool.Close()

	_, filename, _, ok := runtime.Caller(0)
	require.True(t, ok)
	seed, err := os.ReadFile(filepath.Join(filepath.Dir(filename), "..", "..", "..", "testdata", "seeds", "golden_destinations.sql"))
	require.NoError(t, err)
	_, err = pool.Exec(ctx, string(seed))
	require.NoError(t, err)

	h := NewComposerHandler(pool, "", "")
	body := `{
		"origin": {"name": "Nairobi", "latitude": -1.2921, "longitude": 36.8219},
		"days": 2,
		"interests": ["Wildlife"],
		"budgetBand": "budget",
		"groupType": "solo",
		"groupSize": 1,
		"transport": "public",
		"pace": "relaxed"
	}`
	user := "rate-limit-user"
	for i := 0; i < 10; i++ {
		req := httptest.NewRequest(http.MethodPost, "/v1/mobile/itineraries/suggest", strings.NewReader(body))
		req = req.WithContext(context.WithValue(req.Context(), middleware.CtxUserID, user))
		rec := httptest.NewRecorder()
		h.Suggest(rec, req)
		require.Equal(t, http.StatusOK, rec.Code, "request %d: %s", i+1, rec.Body.String())
	}
	req := httptest.NewRequest(http.MethodPost, "/v1/mobile/itineraries/suggest", strings.NewReader(body))
	req = req.WithContext(context.WithValue(req.Context(), middleware.CtxUserID, user))
	rec := httptest.NewRecorder()
	h.Suggest(rec, req)
	assert.Equal(t, http.StatusTooManyRequests, rec.Code)
	assert.Contains(t, rec.Body.String(), "RATE_LIMITED")
}
