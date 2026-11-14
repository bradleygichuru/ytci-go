package expo

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bradleygichuru/ytci-go/internal/catalog"
	"github.com/bradleygichuru/ytci-go/internal/db"
	"github.com/bradleygichuru/ytci-go/internal/places"
)

func TestCatalogCountyResolverAndMigrationConstraints(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}
	ctx := context.Background()
	pool := db.SetupTestDB(t)
	defer pool.Close()

	var counties, coded int
	require.NoError(t, pool.QueryRow(ctx,
		`SELECT count(*), count(county_code) FROM kenya_counties`).Scan(&counties, &coded))
	assert.Equal(t, 47, counties)
	assert.Equal(t, 47, coded)

	pointCases := []struct {
		name     string
		lat, lng float64
		wantCode string
		wantName string
	}{
		{name: "Nairobi", lat: -1.2921, lng: 36.8219, wantCode: "047", wantName: "Nairobi"},
		{name: "Mombasa", lat: -4.0435, lng: 39.6682, wantCode: "001", wantName: "Mombasa"},
		{name: "Diani", lat: -4.3222, lng: 39.5750, wantCode: "002", wantName: "Kwale"},
		{name: "Maasai Mara", lat: -1.49, lng: 35.14, wantCode: "033", wantName: "Narok"},
	}
	for _, tc := range pointCases {
		t.Run(tc.name, func(t *testing.T) {
			var code, name string
			require.NoError(t, pool.QueryRow(ctx, `
				SELECT county_code, county_name
				FROM catalog_resolve_county(ST_SetSRID(ST_MakePoint($1, $2), 4326), '')
			`, tc.lng, tc.lat).Scan(&code, &name))
			assert.Equal(t, tc.wantCode, code)
			assert.Equal(t, tc.wantName, name)
		})
	}

	textCases := map[string]string{
		"Ngong":          "034",
		"Magadi Rd":      "034",
		"Diani":          "002",
		"Narok":          "033",
		"Nairobi County": "047",
		"Taita Taveta":   "006",
	}
	for text, want := range textCases {
		var code string
		require.NoError(t, pool.QueryRow(ctx, `
			SELECT county_code FROM catalog_resolve_county(NULL, $1)
		`, text).Scan(&code))
		assert.Equal(t, want, code, "county text %q", text)
	}

	// A non-destination cannot enter the catalog even through raw SQL.
	_, err := pool.Exec(ctx, `
		INSERT INTO destinations (name, slug, county, category, status)
		VALUES ('A Church', 'a-church', 'Nairobi', 'church', 'draft')
	`)
	require.Error(t, err, "church must violate the category taxonomy check")

	// A published row must have a canonical county code.
	_, err = pool.Exec(ctx, `
		INSERT INTO destinations (name, slug, county, category, status)
		VALUES ('Lost Place', 'lost-place', 'Atlantis', 'Sightseeing', 'published')
	`)
	require.Error(t, err, "published destination without a county code must be rejected")

	_, err = pool.Exec(ctx, `
		INSERT INTO destinations (name, slug, county, category, status, verification_status)
		VALUES ('Verified Place', 'verified-place', 'Nairobi', 'Culture', 'draft', 'verified')
	`)
	require.NoError(t, err)
	var verifiedAt *time.Time
	require.NoError(t, pool.QueryRow(ctx,
		`SELECT verified_at FROM destinations WHERE slug = 'verified-place'`).Scan(&verifiedAt))
	require.NotNil(t, verifiedAt, "verified status must set verified_at")

	_, err = pool.Exec(ctx,
		`UPDATE destinations SET verification_status = 'pending' WHERE slug = 'verified-place'`)
	require.NoError(t, err)
	require.NoError(t, pool.QueryRow(ctx,
		`SELECT verified_at FROM destinations WHERE slug = 'verified-place'`).Scan(&verifiedAt))
	assert.Nil(t, verifiedAt, "leaving verified status must clear verified_at")
}

func TestCatalogBackfillResolvesCountyCounts(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}
	ctx := context.Background()
	pool := db.SetupTestDB(t)
	defer pool.Close()

	// Disable the normal resolver trigger so the one-time backfill function is
	// exercised directly.
	_, err := pool.Exec(ctx, `ALTER TABLE destinations DISABLE TRIGGER USER`)
	require.NoError(t, err)

	_, err = pool.Exec(ctx, `
		INSERT INTO destinations (name, slug, county, category, status, location) VALUES
		('Backfill Nairobi', 'backfill-nairobi', 'Unknown', 'Wildlife', 'draft',
		 ST_SetSRID(ST_MakePoint(36.8219, -1.2921), 4326)),
		('Backfill Diani', 'backfill-diani', 'Unknown', 'Beach', 'draft',
		 ST_SetSRID(ST_MakePoint(39.5750, -4.3222), 4326)),
		('Backfill Ngong', 'backfill-ngong', 'Ngong', 'Culture', 'draft', NULL),
		('Backfill Unknown', 'backfill-unknown', 'Atlantis', 'Sightseeing', 'draft', NULL)
	`)
	require.NoError(t, err)

	_, err = pool.Exec(ctx, `SELECT catalog_backfill_destination_county()`)
	require.NoError(t, err)

	rows, err := pool.Query(ctx, `
		SELECT name, COALESCE(county_code, ''), county
		FROM destinations
		WHERE slug LIKE 'backfill-%'
		ORDER BY name
	`)
	require.NoError(t, err)
	defer rows.Close()

	got := map[string]struct {
		code   string
		county string
	}{}
	for rows.Next() {
		var name, code, county string
		require.NoError(t, rows.Scan(&name, &code, &county))
		got[name] = struct {
			code   string
			county string
		}{code: code, county: county}
	}
	require.NoError(t, rows.Err())

	assert.Equal(t, "047", got["Backfill Nairobi"].code)
	assert.Equal(t, "Nairobi", got["Backfill Nairobi"].county)
	assert.Equal(t, "002", got["Backfill Diani"].code)
	assert.Equal(t, "Kwale", got["Backfill Diani"].county)
	assert.Equal(t, "034", got["Backfill Ngong"].code)
	assert.Equal(t, "Kajiado", got["Backfill Ngong"].county)
	assert.Equal(t, "", got["Backfill Unknown"].code)
	assert.Equal(t, "Atlantis", got["Backfill Unknown"].county)
}

func TestCreateDraftDestinationTaxonomyAndDedupe(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}
	ctx := context.Background()
	pool := db.SetupTestDB(t)
	defer pool.Close()

	h := NewPlacesHandler(pool, "", nil)
	details := &places.PlaceDetails{
		PlaceID:          "catalog-test-place",
		DisplayName:      "Catalog Test Park",
		Types:            []string{"national_park", "tourist_attraction"},
		FormattedAddress: "Nairobi, Kenya",
		Location:         &places.LatLng{Latitude: -1.2921, Longitude: 36.8219},
	}

	first, err := h.createDraftDestination(ctx, details)
	require.NoError(t, err)
	assert.NotEmpty(t, first.Slug)

	second, err := h.createDraftDestination(ctx, details)
	require.NoError(t, err)
	assert.Equal(t, first.ID, second.ID)

	// Same normalized name in the same county dedupes even when Google
	// returns a different place id.
	duplicate := *details
	duplicate.PlaceID = "catalog-test-place-2"
	third, err := h.createDraftDestination(ctx, &duplicate)
	require.NoError(t, err)
	assert.Equal(t, first.ID, third.ID)

	var count int
	require.NoError(t, pool.QueryRow(ctx,
		`SELECT count(*) FROM destinations WHERE slug = $1`, first.Slug).Scan(&count))
	assert.Equal(t, 1, count)

	var countyCode, category string
	var tags []string
	require.NoError(t, pool.QueryRow(ctx, `
		SELECT county_code, category, interest_tags
		FROM destinations WHERE id = $1
	`, first.ID).Scan(&countyCode, &category, &tags))
	assert.Equal(t, "047", countyCode)
	assert.Equal(t, "Wildlife", category)
	assert.ElementsMatch(t, []string{"Wildlife", "Conservation"}, tags)

	_, err = h.createDraftDestination(ctx, &places.PlaceDetails{
		PlaceID:     "catalog-test-church",
		DisplayName: "Catalog Test Church",
		Types:       []string{"church", "tourist_attraction"},
		Location:    &places.LatLng{Latitude: -1.29, Longitude: 36.82},
	})
	require.ErrorIs(t, err, catalog.ErrNotADestination)

	_, err = h.createDraftDestination(ctx, &places.PlaceDetails{
		PlaceID:          "catalog-test-nowhere",
		DisplayName:      "Nowhere",
		Types:            []string{"tourist_attraction"},
		FormattedAddress: "Atlantis",
	})
	require.ErrorIs(t, err, catalog.ErrCountyNotFound)

	require.NoError(t, pool.QueryRow(ctx,
		`SELECT count(*) FROM destinations`).Scan(&count))
	assert.Equal(t, 1, count)
}

func TestPopularPathNeverCreatesDrafts(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}
	ctx := context.Background()
	pool := db.SetupTestDB(t)
	defer pool.Close()

	_, err := pool.Exec(ctx, `
		INSERT INTO destinations (name, slug, county, category, status, source, google_place_id, location)
		VALUES ('Existing Park', 'existing-park', 'Nairobi', 'Wildlife', 'published',
		        'google_places', 'existing-place',
		        ST_SetSRID(ST_MakePoint(36.8219, -1.2921), 4326))
	`)
	require.NoError(t, err)

	h := NewPlacesHandler(pool, "", nil)
	populars := h.matchNearbyPlaces(ctx, []places.NearbyPlace{
		{
			PlaceID:     "existing-place",
			DisplayName: places.DisplayName{Text: "Existing Park"},
			Types:       []string{"national_park"},
		},
		{
			PlaceID:     "new-place",
			DisplayName: places.DisplayName{Text: "New Park"},
			Types:       []string{"national_park"},
		},
		{
			PlaceID:     "church-place",
			DisplayName: places.DisplayName{Text: "Local Church"},
			Types:       []string{"church", "tourist_attraction"},
		},
	})
	require.Len(t, populars, 1)
	assert.Equal(t, "Existing Park", populars[0].Name)

	var created int
	require.NoError(t, pool.QueryRow(ctx,
		`SELECT count(*) FROM destinations WHERE google_place_id = 'new-place'`).Scan(&created))
	assert.Equal(t, 0, created)

	require.NoError(t, pool.QueryRow(ctx,
		`SELECT count(*) FROM destinations WHERE name = 'Local Church'`).Scan(&created))
	assert.Equal(t, 0, created)
}
