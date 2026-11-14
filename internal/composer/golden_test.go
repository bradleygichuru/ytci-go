package composer

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bradleygichuru/ytci-go/internal/catalog"
	"github.com/bradleygichuru/ytci-go/internal/db"
)

func TestGoldenComposerBriefs(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}
	ctx := context.Background()
	pool := db.SetupTestDB(t)
	defer pool.Close()

	_, filename, _, ok := runtime.Caller(0)
	require.True(t, ok)
	seed, err := os.ReadFile(filepath.Join(filepath.Dir(filename), "..", "..", "testdata", "seeds", "golden_destinations.sql"))
	require.NoError(t, err)
	_, err = pool.Exec(ctx, string(seed))
	require.NoError(t, err)

	_, err = pool.Exec(ctx, `
		INSERT INTO composer_unverified_candidates (
			place_id, name, address, county_code, category, interest_tags,
			location, rating, review_count, source
		) VALUES (
			'golden-turkana-viewpoint', 'Lake Turkana Viewpoint', 'Turkana, Kenya',
			'023', 'Sightseeing', ARRAY['Sightseeing', 'Culture'],
			ST_SetSRID(ST_MakePoint(36.0, 3.5), 4326), 4.4, 120, 'google_places'
		)
	`)
	require.NoError(t, err)

	catalogProvider := &DBCatalogProvider{Pool: pool}
	engine := NewEngine(
		catalogProvider,
		catalogProvider,
		&DBCountyResolver{Pool: pool},
		StraightLineRouter{},
		NoopWeather{},
		nil,
		nil,
		nil,
		nil,
	)
	engine.Now = func() time.Time {
		return time.Date(2026, 11, 14, 12, 0, 0, 0, time.UTC)
	}

	briefs := []struct {
		name  string
		brief Brief
	}{
		{
			name: "Nairobi wildlife and culture",
			brief: Brief{
				Origin: Origin{Name: "Nairobi", Latitude: ptr(-1.2921), Longitude: ptr(36.8219)},
				Days:   3, Interests: []string{"Wildlife", "Culture"},
				BudgetBand: "mid-range", GroupType: "couple", GroupSize: 2,
				Transport: "car", Pace: "balanced",
			},
		},
		{
			name: "Mombasa culture and beach",
			brief: Brief{
				Origin: Origin{Name: "Mombasa", Latitude: ptr(-4.0435), Longitude: ptr(39.6682)},
				Days:   2, Interests: []string{"Culture", "Beaches"},
				BudgetBand: "budget", GroupType: "solo", GroupSize: 1,
				Transport: "public", Pace: "relaxed",
			},
		},
		{
			name: "Turkana unverified opt-in",
			brief: Brief{
				Origin: Origin{Name: "Turkana", Latitude: ptr(3.5), Longitude: ptr(36.0)},
				Days:   4, Interests: []string{"Wildlife", "Culture"},
				BudgetBand: "mid-range", GroupType: "friends", GroupSize: 4,
				Transport: "car", Pace: "balanced", AllowUnverified: true,
			},
		},
	}

	for _, tc := range briefs {
		t.Run(tc.name, func(t *testing.T) {
			response, err := engine.Suggest(ctx, "golden-user", tc.brief)
			require.NoError(t, err)
			require.Len(t, response.Suggestions, 3)
			hasStop := false
			for _, plan := range response.Suggestions {
				if lenStops(plan) > 0 {
					hasStop = true
				}
				assert.Equal(t, "KES", plan.EstimatedBudget.Currency)
				assert.True(t, plan.EstimatedBudget.Estimated)
			}
			assert.True(t, hasStop, "at least one alternative must contain a stop")

			second, err := engine.Suggest(ctx, "golden-user", tc.brief)
			require.NoError(t, err)
			firstJSON, err := json.Marshal(response.Suggestions)
			require.NoError(t, err)
			secondJSON, err := json.Marshal(second.Suggestions)
			require.NoError(t, err)
			assert.JSONEq(t, string(firstJSON), string(secondJSON))
		})
	}
}

func TestQualityBands(t *testing.T) {
	curated := catalog.ComposerCandidate{Provenance: catalog.ProvenanceCurated}
	assert.Equal(t, 1.0, qualityScore(curated))

	unverified := func(rating *float64, reviews *int) catalog.ComposerCandidate {
		return catalog.ComposerCandidate{Provenance: catalog.ProvenanceGooglePlaces, Rating: rating, ReviewCount: reviews}
	}
	assert.Equal(t, 0.70, qualityScore(unverified(ptr(4.6), ptr(200))))
	assert.Equal(t, 0.55, qualityScore(unverified(ptr(4.2), ptr(20))))
	assert.Equal(t, 0.45, qualityScore(unverified(ptr(3.9), ptr(20))))
	assert.Equal(t, 0.40, qualityScore(unverified(nil, nil)))
}

func TestBudgetEstimateCategories(t *testing.T) {
	plan := Suggestion{
		Days: []Day{{
			Stops: []Stop{{
				Category: "Wildlife", Interests: []string{"Wildlife"}, FeeAmountKes: ptr(int64(0)),
			}},
		}},
		TotalTravelMinutes: 120,
	}
	budget := estimateBudget(Brief{
		Days: 2, GroupSize: 2, BudgetBand: "mid-range", Transport: "car",
	}, plan, 120)
	assert.Equal(t, "KES", budget.Currency)
	assert.True(t, budget.Estimated)
	assert.Greater(t, budget.TotalKes, int64(0))
	assert.Equal(t, budget.TotalKes/budget.PerPersonKes, int64(2))
	for _, category := range []string{
		"Transport", "Accommodation", "Food", "Park & entry fees",
		"Activities", "Shopping", "Conservation", "Other",
	} {
		assert.Contains(t, budget.Breakdown, category)
	}
}

func TestProfileWeightsSumToOne(t *testing.T) {
	for _, profile := range Profiles {
		w := profileWeights[profile]
		sum := w.Interest + w.Travel + w.Quality + w.Budget + w.Weather + w.Diversity + w.Event
		assert.InDelta(t, 1.0, sum, 0.0001, string(profile))
	}
}
