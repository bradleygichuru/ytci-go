package composer

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bradleygichuru/ytci-go/internal/catalog"
)

type fakeCandidates struct {
	candidates []catalog.ComposerCandidate
	err        error
	calls      int
}

func (f *fakeCandidates) Candidates(context.Context) ([]catalog.ComposerCandidate, error) {
	f.calls++
	return f.candidates, f.err
}

type fakeCoverage struct {
	coverage []catalog.ComposerCountyCoverage
	err      error
}

func (f *fakeCoverage) Coverage(context.Context) ([]catalog.ComposerCountyCoverage, error) {
	return f.coverage, f.err
}

type fakeResolver struct {
	county catalog.County
	err    error
}

func (f *fakeResolver) ResolveCounty(context.Context, *float64, *float64, string) (catalog.County, error) {
	return f.county, f.err
}

type fakeRouter struct {
	matrix TravelMatrix
	err    error
}

func (f *fakeRouter) Matrix(context.Context, []Point, string) (TravelMatrix, error) {
	return f.matrix, f.err
}

type fakeWeather struct {
	days []DayWeather
	err  error
}

func (f *fakeWeather) Daily(context.Context, Point, time.Time, time.Time) ([]DayWeather, error) {
	return f.days, f.err
}

type fakeEvents struct {
	events []Event
	err    error
}

func (f *fakeEvents) Events(context.Context, []string, time.Time, time.Time) ([]Event, error) {
	return f.events, f.err
}

type fakeCache struct {
	response SuggestionResponse
	hit      bool
	gets     int
	sets     int
}

func (f *fakeCache) Get(context.Context, string) (SuggestionResponse, bool, error) {
	f.gets++
	return f.response, f.hit, nil
}

func (f *fakeCache) Set(context.Context, string, string, SuggestionResponse, time.Duration) error {
	f.sets++
	return nil
}

type fakeUsage struct {
	used int
}

func (f *fakeUsage) ConsumeSuggestion(_ context.Context, _ string, _ string, limit int) (int, bool, error) {
	if f.used >= limit {
		return limit, false, nil
	}
	f.used++
	return f.used, true, nil
}

func ptr[T any](v T) *T { return &v }

func testCandidate(id, slug, name, countyCode, county string, lat, lng float64, provenance, category string, interests ...string) catalog.ComposerCandidate {
	c := catalog.ComposerCandidate{
		Name:       name,
		CountyCode: countyCode,
		County:     county,
		Category:   category,
		Interests:  interests,
		Latitude:   lat,
		Longitude:  lng,
		Provenance: provenance,
		Source:     provenance,
	}
	if provenance == catalog.ProvenanceCurated {
		c.DestinationID = &id
		c.Slug = &slug
		c.DurationMinMinutes = ptr(60)
		c.DurationMaxMinutes = ptr(120)
		c.FeeStatus = ptr("free")
		c.OpeningHoursStatus = ptr("unverified")
		c.HeroImageAttribution = ptr("YTCI")
		c.VerifiedAt = ptr(time.Date(2026, 11, 1, 0, 0, 0, 0, time.UTC))
	} else {
		c.PlaceID = &id
		c.Rating = ptr(4.6)
		c.ReviewCount = ptr(220)
	}
	return c
}

func testCandidates() []catalog.ComposerCandidate {
	return []catalog.ComposerCandidate{
		testCandidate("dest-karura", "karura-forest", "Karura Forest", "047", "Nairobi", -1.2333, 36.8333, catalog.ProvenanceCurated, "Wildlife", "Wildlife", "Hiking", "Conservation"),
		testCandidate("dest-mara", "maasai-mara", "Maasai Mara", "033", "Narok", -1.49, 35.14, catalog.ProvenanceCurated, "Wildlife", "Wildlife", "Conservation", "Photography"),
		testCandidate("dest-amboseli", "amboseli", "Amboseli", "034", "Kajiado", -2.6527, 37.2606, catalog.ProvenanceCurated, "Wildlife", "Wildlife", "Photography"),
		testCandidate("dest-fort", "fort-jesus", "Fort Jesus", "001", "Mombasa", -4.0625, 39.6794, catalog.ProvenanceCurated, "Culture", "Culture", "Sightseeing"),
		testCandidate("dest-diani", "diani-beach", "Diani Beach", "002", "Kwale", -4.3222, 39.5750, catalog.ProvenanceCurated, "Beach", "Beach", "Rest"),
		testCandidate("place-hells", "", "Hell's Kitchen", "003", "Kilifi", -3.0167, 39.8333, catalog.ProvenanceGooglePlaces, "Sightseeing", "Sightseeing", "Hiking"),
		testCandidate("place-turkana", "", "Lake Turkana", "023", "Turkana", 3.5, 36.0, catalog.ProvenanceGooglePlaces, "Sightseeing", "Sightseeing", "Culture"),
	}
}

func testCoverage() []catalog.ComposerCountyCoverage {
	return []catalog.ComposerCountyCoverage{
		{CountyCode: "047", County: "Nairobi", CuratedCount: 1, TotalCount: 1, CoverageStatus: catalog.CoverageLimited},
		{CountyCode: "033", County: "Narok", CuratedCount: 1, TotalCount: 1, CoverageStatus: catalog.CoverageLimited},
		{CountyCode: "034", County: "Kajiado", CuratedCount: 1, TotalCount: 1, CoverageStatus: catalog.CoverageLimited},
		{CountyCode: "001", County: "Mombasa", CuratedCount: 1, TotalCount: 1, CoverageStatus: catalog.CoverageLimited},
		{CountyCode: "002", County: "Kwale", CuratedCount: 1, TotalCount: 1, CoverageStatus: catalog.CoverageLimited},
		{CountyCode: "003", County: "Kilifi", UnverifiedCount: 1, TotalCount: 1, CoverageStatus: catalog.CoverageLimited},
		{CountyCode: "023", County: "Turkana", UnverifiedCount: 1, TotalCount: 1, CoverageStatus: catalog.CoverageLimited},
	}
}

func testBrief() Brief {
	return Brief{
		Origin:         Origin{Name: "Nairobi", Latitude: ptr(-1.2921), Longitude: ptr(36.8219)},
		Days:           3,
		Interests:      []string{"Wildlife", "Culture"},
		BudgetBand:     "mid-range",
		GroupType:      "couple",
		GroupSize:      2,
		Transport:      "car",
		Pace:           "balanced",
		StartDate:      ptr("2026-11-20"),
		ReturnToOrigin: ptr(true),
	}
}

func testEngine(candidates []catalog.ComposerCandidate, coverage []catalog.ComposerCountyCoverage, router Router, weather WeatherProvider, events EventProvider) *Engine {
	engine := NewEngine(
		&fakeCandidates{candidates: candidates},
		&fakeCoverage{coverage: coverage},
		&fakeResolver{county: catalog.County{Code: "047", Name: "Nairobi"}},
		router,
		weather,
		events,
		nil,
		nil,
		nil,
	)
	engine.Now = func() time.Time {
		return time.Date(2026, 11, 14, 12, 0, 0, 0, time.UTC)
	}
	return engine
}

func TestSuggestDeterministic(t *testing.T) {
	ctx := context.Background()
	day := "2026-11-20"
	weather := &fakeWeather{days: []DayWeather{
		{Date: day, Horizon: "forecast", PrecipProbability: ptr(10), TempMaxC: ptr(26.0), Source: "Open-Meteo ECMWF IFS"},
		{Date: "2026-11-21", Horizon: "forecast", PrecipProbability: ptr(20), TempMaxC: ptr(25.0), Source: "Open-Meteo ECMWF IFS"},
		{Date: "2026-11-22", Horizon: "forecast", PrecipProbability: ptr(30), TempMaxC: ptr(24.0), Source: "Open-Meteo ECMWF IFS"},
	}}
	events := &fakeEvents{events: []Event{
		{ID: "evt-1", Title: "Mara Conservation Day", County: "Narok", Type: "conservation", Date: "2026-11-21"},
	}}
	engine := testEngine(testCandidates(), testCoverage(), StraightLineRouter{}, weather, events)

	first, err := engine.Suggest(ctx, "user-1", testBrief())
	require.NoError(t, err)
	second, err := engine.Suggest(ctx, "user-1", testBrief())
	require.NoError(t, err)

	require.Len(t, first.Suggestions, 3)
	require.Len(t, second.Suggestions, 3)
	for i := range first.Suggestions {
		assert.Equal(t, Profiles[i], first.Suggestions[i].Profile)
		assert.NotEmpty(t, first.Suggestions[i].Days)
		assert.NotEmpty(t, first.Suggestions[i].EstimatedBudget.Breakdown)
		assert.True(t, first.Suggestions[i].EstimatedBudget.Estimated)
		assert.Equal(t, "KES", first.Suggestions[i].EstimatedBudget.Currency)
	}
	firstJSON, err := json.Marshal(first.Suggestions)
	require.NoError(t, err)
	secondJSON, err := json.Marshal(second.Suggestions)
	require.NoError(t, err)
	assert.JSONEq(t, string(firstJSON), string(secondJSON), "same brief + data must be deterministic")
}

func TestSuggestCacheHit(t *testing.T) {
	ctx := context.Background()
	cache := &fakeCache{hit: true, response: SuggestionResponse{
		Suggestions: []Suggestion{{Profile: ProfileBalanced, Status: "ok"}},
		Meta:        Meta{EngineVersion: EngineVersion},
	}}
	engine := testEngine(testCandidates(), testCoverage(), StraightLineRouter{}, &fakeWeather{}, &fakeEvents{})
	engine.Cache = cache

	response, err := engine.Suggest(ctx, "user-1", testBrief())
	require.NoError(t, err)
	assert.True(t, response.Meta.Cached)
	assert.Len(t, response.Suggestions, 1)
	assert.Equal(t, 1, cache.gets)
	assert.Equal(t, 0, cache.sets)
}

func TestUnverifiedOptInRequired(t *testing.T) {
	ctx := context.Background()
	coverage := []catalog.ComposerCountyCoverage{
		{CountyCode: "047", County: "Nairobi", UnverifiedCount: 1, TotalCount: 1, CoverageStatus: catalog.CoverageLimited},
	}
	candidates := []catalog.ComposerCandidate{
		testCandidate("place-hells", "", "Hell's Kitchen", "047", "Nairobi", -1.3, 36.82, catalog.ProvenanceGooglePlaces, "Sightseeing", "Sightseeing"),
	}
	engine := testEngine(candidates, coverage, StraightLineRouter{}, &fakeWeather{}, &fakeEvents{})
	brief := testBrief()
	brief.AllowUnverified = false
	response, err := engine.Suggest(ctx, "user-1", brief)
	require.NoError(t, err)
	require.NotNil(t, response.Blocked)
	assert.Equal(t, "UNVERIFIED_OPT_IN_REQUIRED", response.Blocked.Code)

	brief.AllowUnverified = true
	response, err = engine.Suggest(ctx, "user-1", brief)
	require.NoError(t, err)
	require.Len(t, response.Suggestions, 3)
	assert.Equal(t, 0, response.Suggestions[0].CuratedCount)
	for _, plan := range response.Suggestions {
		assert.Contains(t, warningCodes(plan.Warnings), "UNVERIFIED_PLAN")
	}
}

func TestRoutingAndWeatherFallback(t *testing.T) {
	ctx := context.Background()
	engine := testEngine(testCandidates(), testCoverage(), &fakeRouter{err: errors.New("routes down")}, &fakeWeather{err: errors.New("weather down")}, &fakeEvents{})
	response, err := engine.Suggest(ctx, "user-1", testBrief())
	require.NoError(t, err)
	require.Len(t, response.Suggestions, 3)
	for _, plan := range response.Suggestions {
		codes := warningCodes(plan.Warnings)
		assert.Contains(t, codes, "ROUTE_ESTIMATED")
		assert.Contains(t, codes, "WEATHER_UNAVAILABLE")
		assert.NotEmpty(t, plan.Days)
	}
}

func TestBudgetCapBlocksOverBudgetPlan(t *testing.T) {
	ctx := context.Background()
	engine := testEngine(testCandidates(), testCoverage(), StraightLineRouter{}, &fakeWeather{}, &fakeEvents{})
	brief := testBrief()
	brief.BudgetPerPersonKes = ptr(int64(1000))
	response, err := engine.Suggest(ctx, "user-1", brief)
	require.NoError(t, err)
	require.Len(t, response.Suggestions, 3)
	for _, plan := range response.Suggestions {
		assert.Equal(t, "blocked", plan.Status)
		assert.Contains(t, warningCodes(plan.Warnings), "BUDGET_CAP_EXCEEDED")
	}
}

func TestMustIncludeIsPlaced(t *testing.T) {
	ctx := context.Background()
	engine := testEngine(testCandidates(), testCoverage(), StraightLineRouter{}, &fakeWeather{}, &fakeEvents{})
	brief := testBrief()
	brief.MustInclude = []string{"karura-forest"}
	response, err := engine.Suggest(ctx, "user-1", brief)
	require.NoError(t, err)
	assert.True(t, planHasStop(response.Suggestions[1], "Karura Forest"))
	assert.NotContains(t, warningCodes(response.Suggestions[1].Warnings), "MUST_INCLUDE_UNREACHABLE")
}

func TestRateLimit(t *testing.T) {
	ctx := context.Background()
	engine := testEngine(testCandidates(), testCoverage(), StraightLineRouter{}, &fakeWeather{}, &fakeEvents{})
	engine.Usage = &fakeUsage{}
	for i := 0; i < 10; i++ {
		_, err := engine.Suggest(ctx, "user-1", testBrief())
		require.NoError(t, err, "request %d", i+1)
	}
	_, err := engine.Suggest(ctx, "user-1", testBrief())
	var rateErr *RateLimitError
	require.ErrorAs(t, err, &rateErr)
	assert.Equal(t, 10, rateErr.Limit)
	assert.Greater(t, rateErr.RetryAfterSeconds, 0)
}

func TestCoverageBlocked(t *testing.T) {
	ctx := context.Background()
	engine := testEngine(nil, nil, StraightLineRouter{}, &fakeWeather{}, &fakeEvents{})
	response, err := engine.Suggest(ctx, "user-1", testBrief())
	require.NoError(t, err)
	require.NotNil(t, response.Blocked)
	assert.Equal(t, "NOT_COVERED", response.Blocked.Code)
}

func warningCodes(warnings []Warning) []string {
	out := make([]string, 0, len(warnings))
	for _, w := range warnings {
		out = append(out, w.Code)
	}
	return out
}

func planHasStop(plan Suggestion, name string) bool {
	for _, day := range plan.Days {
		for _, stop := range day.Stops {
			if stop.Name == name {
				return true
			}
		}
	}
	return false
}
