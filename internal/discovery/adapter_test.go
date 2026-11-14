package discovery

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bradleygichuru/ytci-go/internal/db"
	"github.com/bradleygichuru/ytci-go/internal/places"
)

type fakeSearcher struct {
	mu       sync.Mutex
	calls    int
	places   []places.NearbyPlace
	byCenter map[string][]places.NearbyPlace
	err      error
}

func (f *fakeSearcher) NearbySearchWithOptions(ctx context.Context, opts places.NearbySearchOptions) ([]places.NearbyPlace, error) {
	key := fmt.Sprintf("%.5f,%.5f", opts.Center.Latitude, opts.Center.Longitude)
	f.mu.Lock()
	f.calls++
	f.mu.Unlock()
	if f.err != nil {
		return nil, f.err
	}
	if f.byCenter != nil {
		if results, ok := f.byCenter[key]; ok {
			return results, nil
		}
	}
	return f.places, nil
}

func (f *fakeSearcher) callCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.calls
}

func setupAdapter(t *testing.T, budget int) (*Adapter, *pgxpool.Pool, *fakeSearcher) {
	t.Helper()
	pool := db.SetupTestDB(t)
	t.Cleanup(pool.Close)
	searcher := &fakeSearcher{}
	adapter := NewAdapter(pool, searcher, budget)
	adapter.now = func() time.Time {
		return time.Date(2026, 11, 14, 12, 0, 0, 0, time.UTC)
	}
	return adapter, pool, searcher
}

func nearbyPlace(id, name, address string, lat, lng float64, types ...string) places.NearbyPlace {
	return places.NearbyPlace{
		PlaceID:          id,
		DisplayName:      places.DisplayName{Text: name},
		FormattedAddress: address,
		Location:         &places.LatLng{Latitude: lat, Longitude: lng},
		Types:            types,
	}
}

func TestCountySearchPlan(t *testing.T) {
	ctx := context.Background()
	pool := db.SetupTestDB(t)
	defer pool.Close()

	tests := []struct {
		countyCode string
		wantPoints int
		minRadius  int
		maxRadius  int
	}{
		{countyCode: "047", wantPoints: 1, minRadius: 5000, maxRadius: 20000},  // Nairobi
		{countyCode: "003", wantPoints: 6, minRadius: 20000, maxRadius: 50000}, // Kilifi
		{countyCode: "023", wantPoints: 9, minRadius: 40000, maxRadius: 50000}, // Turkana
		{countyCode: "010", wantPoints: 9, minRadius: 40000, maxRadius: 50000}, // Marsabit
	}

	for _, tt := range tests {
		t.Run(tt.countyCode, func(t *testing.T) {
			plan, err := CountySearchPlan(ctx, pool, tt.countyCode)
			require.NoError(t, err)
			assert.Len(t, plan.Points, tt.wantPoints)
			assert.GreaterOrEqual(t, plan.RadiusMeters, tt.minRadius)
			assert.LessOrEqual(t, plan.RadiusMeters, tt.maxRadius)

			for _, point := range plan.Points {
				var inside bool
				require.NoError(t, pool.QueryRow(ctx, `
					SELECT ST_Covers(geom, ST_SetSRID(ST_MakePoint($1, $2), 4326))
					FROM kenya_counties WHERE county_code = $3
				`, point.Longitude, point.Latitude, tt.countyCode).Scan(&inside))
				assert.True(t, inside, "point %+v must be inside %s", point, tt.countyCode)
			}
		})
	}

	_, err := CountySearchPlan(ctx, pool, "999")
	require.ErrorIs(t, err, ErrNoCounty)
}

func TestDiscoverCountyStoresUnverifiedCandidates(t *testing.T) {
	ctx := context.Background()
	adapter, pool, searcher := setupAdapter(t, 20)

	photo := places.Photo{
		Name: "places/ChIJ-beach/photos/beach-1",
		AuthorAttributions: []places.AuthorAttribution{{
			DisplayName: "Beach Photographer",
		}},
	}
	hours := &places.OpeningHours{WeekdayDescriptions: []string{"Monday: 6:00 AM – 6:00 PM"}}
	beach := nearbyPlace("ChIJ-beach", "Watamu Beach", "Watamu, Kilifi, Kenya",
		-3.356, 40.020, "beach")
	beach.Rating = 4.7
	beach.UserRatingCount = 210
	beach.PriceLevel = "PRICE_LEVEL_FREE"
	beach.Photos = []places.Photo{photo}
	beach.CurrentOpeningHours = hours

	church := nearbyPlace("ChIJ-church", "Local Church", "Malindi, Kilifi, Kenya",
		-3.22, 40.12, "church", "tourist_attraction")
	genericChurch := nearbyPlace("ChIJ-generic-church", "Redeemed Gospel Church",
		"Malindi, Kilifi, Kenya", -3.23, 40.13, "tourist_attraction")
	guestHouse := nearbyPlace("ChIJ-guest-house", "Pwani Complex Guest House",
		"Malindi, Kilifi, Kenya", -3.24, 40.14, "tourist_attraction")
	closedPark := nearbyPlace("ChIJ-closed", "Closed Park", "Malindi, Kilifi, Kenya",
		-3.25, 40.15, "national_park")
	closedPark.BusinessStatus = "CLOSED_PERMANENTLY"
	fort := nearbyPlace("ChIJ-fort", "Fort Jesus Museum", "Mombasa, Kenya",
		-4.0625, 39.6794, "museum")

	searcher.places = []places.NearbyPlace{beach, church, genericChurch, guestHouse, closedPark, fort}

	result, err := adapter.DiscoverCounty(ctx, "003")
	require.NoError(t, err)
	assert.False(t, result.Cached)
	assert.Equal(t, "county", result.ScopeKind)
	assert.Equal(t, "003", result.ScopeKey)
	assert.Equal(t, 6, result.RequestCount) // Kilifi search plan
	require.Len(t, result.Candidates, 2)

	byID := map[string]Candidate{}
	for _, c := range result.Candidates {
		byID[c.PlaceID] = c
	}

	gotBeach := byID["ChIJ-beach"]
	assert.Equal(t, "Watamu Beach", gotBeach.Name)
	assert.Equal(t, "003", gotBeach.CountyCode)
	assert.Equal(t, "Kilifi", gotBeach.County)
	assert.Equal(t, "Beach", gotBeach.Category)
	assert.ElementsMatch(t, []string{"Beach", "Rest"}, gotBeach.Interests)
	require.NotNil(t, gotBeach.Rating)
	assert.Equal(t, 4.7, *gotBeach.Rating)
	require.NotNil(t, gotBeach.ReviewCount)
	assert.Equal(t, 210, *gotBeach.ReviewCount)
	require.NotNil(t, gotBeach.PriceLevel)
	assert.Equal(t, "PRICE_LEVEL_FREE", *gotBeach.PriceLevel)
	require.NotNil(t, gotBeach.HeroImageURL)
	assert.Contains(t, *gotBeach.HeroImageURL, "/v1/mobile/places/photo?ref=")
	require.NotNil(t, gotBeach.HeroImageAttribution)
	assert.Equal(t, "Beach Photographer", *gotBeach.HeroImageAttribution)
	assert.NotEmpty(t, gotBeach.OpeningHours)
	require.NotNil(t, gotBeach.Address)

	gotFort := byID["ChIJ-fort"]
	assert.Equal(t, "001", gotFort.CountyCode)
	assert.Equal(t, "Mombasa", gotFort.County)
	assert.Equal(t, "Culture", gotFort.Category)

	_, hasChurch := byID["ChIJ-church"]
	assert.False(t, hasChurch, "churches must be rejected")

	var unverified int
	require.NoError(t, pool.QueryRow(ctx,
		`SELECT count(*) FROM composer_unverified_candidates`).Scan(&unverified))
	assert.Equal(t, 2, unverified)

	var destinations int
	require.NoError(t, pool.QueryRow(ctx, `SELECT count(*) FROM destinations`).Scan(&destinations))
	assert.Equal(t, 0, destinations, "discovery must never create catalog drafts")

	var viewRows int
	require.NoError(t, pool.QueryRow(ctx, `
		SELECT count(*) FROM composer_catalog WHERE provenance = 'google_places'
	`).Scan(&viewRows))
	assert.Equal(t, 2, viewRows)

	var leaked int
	require.NoError(t, pool.QueryRow(ctx, `
		SELECT count(*) FROM composer_catalog
		WHERE provenance = 'google_places'
		  AND (fee_status IS NOT NULL OR duration_min_minutes IS NOT NULL
		       OR verified_at IS NOT NULL OR cardinality(transport_modes) > 0
		       OR cardinality(best_seasons) > 0)
	`).Scan(&leaked))
	assert.Equal(t, 0, leaked)

	var runStatus string
	var requestCount, candidateCount int
	require.NoError(t, pool.QueryRow(ctx, `
		SELECT status, request_count, candidate_count
		FROM composer_discovery_runs
		WHERE bucket_date = '2026-11-14' AND scope_kind = 'county' AND scope_key = '003'
	`).Scan(&runStatus, &requestCount, &candidateCount))
	assert.Equal(t, "success", runStatus)
	assert.Equal(t, 6, requestCount)
	assert.Equal(t, 2, candidateCount)

	callsBefore := searcher.callCount()
	cached, err := adapter.DiscoverCounty(ctx, "003")
	require.NoError(t, err)
	assert.True(t, cached.Cached)
	assert.Equal(t, 0, cached.RequestCount)
	assert.Len(t, cached.Candidates, 2)
	assert.Equal(t, callsBefore, searcher.callCount(), "same-day run must be cached")
}

func TestDiscoverCountySkipsCuratedDuplicates(t *testing.T) {
	ctx := context.Background()
	adapter, pool, searcher := setupAdapter(t, 20)

	_, err := pool.Exec(ctx, `
		INSERT INTO destinations (name, slug, county, county_code, category, status, is_curated, google_place_id)
		VALUES ('Curated Lamu Museum', 'curated-lamu-museum', 'Lamu', '005', 'Culture', 'draft', true, 'ChIJ-curated'),
		       ('Lamu Fort', 'lamu-fort', 'Lamu', '005', 'Culture', 'draft', true, NULL)
	`)
	require.NoError(t, err)

	searcher.places = []places.NearbyPlace{
		nearbyPlace("ChIJ-curated", "Curated Lamu Museum", "Lamu, Kenya",
			-2.27, 40.90, "museum"),
		nearbyPlace("ChIJ-lamu-fort", "Lamu Fort", "Lamu, Kenya",
			-2.27, 40.91, "historical_landmark"),
		nearbyPlace("ChIJ-new", "New Lamu Site", "Lamu, Kenya",
			-2.28, 40.91, "historical_landmark"),
	}

	result, err := adapter.DiscoverCounty(ctx, "005")
	require.NoError(t, err)
	require.Len(t, result.Candidates, 1)
	assert.Equal(t, "ChIJ-new", result.Candidates[0].PlaceID)

	var curatedRows int
	require.NoError(t, pool.QueryRow(ctx, `
		SELECT count(*) FROM composer_unverified_candidates WHERE place_id = 'ChIJ-curated'
	`).Scan(&curatedRows))
	assert.Equal(t, 0, curatedRows)
}

func TestDiscoverCountyRespectsDailyQuota(t *testing.T) {
	ctx := context.Background()
	adapter, pool, searcher := setupAdapter(t, 1)

	_, err := adapter.DiscoverCounty(ctx, "023") // Turkana needs 9 points
	require.ErrorIs(t, err, ErrQuotaExceeded)
	assert.Equal(t, 0, searcher.callCount())

	var runs int
	require.NoError(t, pool.QueryRow(ctx,
		`SELECT count(*) FROM composer_discovery_runs`).Scan(&runs))
	assert.Equal(t, 0, runs)
}

func TestDiscoverRouteDedupesAndCaches(t *testing.T) {
	ctx := context.Background()
	adapter, pool, searcher := setupAdapter(t, 20)

	nairobi := places.LatLng{Latitude: -1.2921, Longitude: 36.8219}
	mombasa := places.LatLng{Latitude: -4.0435, Longitude: 39.6682}
	nairobiPlace := nearbyPlace("ChIJ-route-nairobi", "Nairobi Museum", "Nairobi, Kenya",
		-1.286, 36.817, "museum")
	mombasaPlace := nearbyPlace("ChIJ-route-mombasa", "Mombasa Fort", "Mombasa, Kenya",
		-4.06, 39.67, "historical_landmark")
	searcher.byCenter = map[string][]places.NearbyPlace{
		fmt.Sprintf("%.5f,%.5f", nairobi.Latitude, nairobi.Longitude): []places.NearbyPlace{nairobiPlace},
		fmt.Sprintf("%.5f,%.5f", mombasa.Latitude, mombasa.Longitude): []places.NearbyPlace{mombasaPlace},
	}

	result, err := adapter.DiscoverRoute(ctx, []places.LatLng{nairobi, mombasa, nairobi}, 15000)
	require.NoError(t, err)
	assert.False(t, result.Cached)
	assert.Equal(t, "route", result.ScopeKind)
	assert.Equal(t, 2, result.RequestCount, "duplicate waypoints collapse")
	require.Len(t, result.Candidates, 2)

	byID := map[string]Candidate{}
	for _, c := range result.Candidates {
		byID[c.PlaceID] = c
	}
	assert.Equal(t, "047", byID["ChIJ-route-nairobi"].CountyCode)
	assert.Equal(t, "001", byID["ChIJ-route-mombasa"].CountyCode)

	var scopeKind string
	var requestCount, candidateCount int
	require.NoError(t, pool.QueryRow(ctx, `
		SELECT scope_kind, request_count, candidate_count
		FROM composer_discovery_runs
		WHERE bucket_date = '2026-11-14' AND scope_key = $1
	`, result.ScopeKey).Scan(&scopeKind, &requestCount, &candidateCount))
	assert.Equal(t, "route", scopeKind)
	assert.Equal(t, 2, requestCount)
	assert.Equal(t, 2, candidateCount)

	callsBefore := searcher.callCount()
	cached, err := adapter.DiscoverRoute(ctx, []places.LatLng{nairobi, mombasa}, 15000)
	require.NoError(t, err)
	assert.True(t, cached.Cached)
	assert.Len(t, cached.Candidates, 2)
	assert.Equal(t, callsBefore, searcher.callCount())
}

func TestDiscoverCountySkipsPlacesWithoutCounty(t *testing.T) {
	ctx := context.Background()
	adapter, pool, searcher := setupAdapter(t, 20)
	searcher.places = []places.NearbyPlace{
		nearbyPlace("ChIJ-ocean", "Ocean Beach", "At sea", 0, 0, "beach"),
	}

	result, err := adapter.DiscoverCounty(ctx, "047")
	require.NoError(t, err)
	assert.Empty(t, result.Candidates)

	var rows int
	require.NoError(t, pool.QueryRow(ctx,
		`SELECT count(*) FROM composer_unverified_candidates`).Scan(&rows))
	assert.Equal(t, 0, rows)
}

func TestFailedRunRequestsStillCountAgainstQuota(t *testing.T) {
	ctx := context.Background()
	adapter, pool, searcher := setupAdapter(t, 20)
	searcher.err = fmt.Errorf("google unavailable")

	_, err := adapter.DiscoverCounty(ctx, "047")
	require.Error(t, err)

	var status string
	var requests int
	require.NoError(t, pool.QueryRow(ctx, `
		SELECT status, request_count FROM composer_discovery_runs
		WHERE bucket_date = '2026-11-14' AND scope_kind = 'county' AND scope_key = '047'
	`).Scan(&status, &requests))
	assert.Equal(t, "failed", status)
	assert.Equal(t, 1, requests)

	searcher.err = nil
	searcher.places = []places.NearbyPlace{
		nearbyPlace("ChIJ-retry", "Retry Museum", "Nairobi, Kenya", -1.286, 36.817, "museum"),
	}
	result, err := adapter.DiscoverCounty(ctx, "047")
	require.NoError(t, err)
	require.Len(t, result.Candidates, 1)

	require.NoError(t, pool.QueryRow(ctx, `
		SELECT status, request_count FROM composer_discovery_runs
		WHERE bucket_date = '2026-11-14' AND scope_kind = 'county' AND scope_key = '047'
	`).Scan(&status, &requests))
	assert.Equal(t, "success", status)
	assert.Equal(t, 2, requests, "retry must add to the day's request count")
}
