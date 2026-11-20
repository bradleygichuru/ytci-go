package catalog_test

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bradleygichuru/ytci-go/internal/catalog"
	"github.com/bradleygichuru/ytci-go/internal/db"
)

func TestComposerCatalog(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}
	ctx := context.Background()
	pool := db.SetupTestDB(t)
	defer pool.Close()

	_, filename, _, ok := runtime.Caller(0)
	require.True(t, ok)
	seedPath := filepath.Join(filepath.Dir(filename), "..", "..", "testdata", "seeds", "golden_destinations.sql")
	seed, err := os.ReadFile(seedPath)
	require.NoError(t, err)
	_, err = pool.Exec(ctx, string(seed))
	require.NoError(t, err)

	t.Run("curated fixtures satisfy the read model", func(t *testing.T) {
		candidates, err := catalog.QueryComposerCandidates(ctx, pool, catalog.ComposerFilter{})
		require.NoError(t, err)
		require.Len(t, candidates, 10)

		expected := map[string]string{
			"hells-kitchen": "003",
			"chalbi-desert": "010",
			"lake-paradise": "010",
			"lake-turkana":  "023",
			"fort-jesus":    "001",
			"diani-beach":   "002",
			"maasai-mara":   "033",
			"amboseli":      "034",
			"lake-nakuru":   "032",
			"karura-forest": "047",
		}
		for _, c := range candidates {
			require.NotNil(t, c.Slug)
			wantCounty, ok := expected[*c.Slug]
			require.True(t, ok, "unexpected curated slug %q", *c.Slug)
			assert.Equal(t, catalog.ProvenanceCurated, c.Provenance, *c.Slug)
			assert.Equal(t, catalog.EligibilityCurated, c.Eligibility, *c.Slug)
			assert.Equal(t, wantCounty, c.CountyCode, *c.Slug)
			assert.NotNil(t, c.DestinationID, *c.Slug)
			assert.Nil(t, c.PlaceID, *c.Slug)
			assert.NotNil(t, c.VerifiedAt, *c.Slug)
			assert.NotNil(t, c.DurationMinMinutes, *c.Slug)
			assert.NotNil(t, c.DurationMaxMinutes, *c.Slug)
			assert.NotNil(t, c.FeeStatus, *c.Slug)
			assert.NotNil(t, c.OpeningHoursStatus, *c.Slug)
			assert.NotNil(t, c.HeroImageAttribution, *c.Slug)
			assert.NotEmpty(t, c.Source, *c.Slug)
			assert.NotEmpty(t, c.Interests, *c.Slug)
			assert.NotEmpty(t, c.TransportModes, *c.Slug)
			assert.NotEmpty(t, c.BestSeasons, *c.Slug)
			assert.False(t, c.Latitude == 0 && c.Longitude == 0, *c.Slug)
			assert.Nil(t, c.Rating, *c.Slug)
			assert.Nil(t, c.ReviewCount, *c.Slug)
			assert.Equal(t, catalog.CoverageLimited, c.CountyCoverageStatus, *c.Slug)
		}

		var unverified int
		require.NoError(t, pool.QueryRow(ctx,
			`SELECT count(*) FROM composer_catalog WHERE provenance = 'google_places'`).Scan(&unverified))
		assert.Equal(t, 0, unverified)
	})

	t.Run("filters do not score", func(t *testing.T) {
		names := func(filter catalog.ComposerFilter) []string {
			candidates, err := catalog.QueryComposerCandidates(ctx, pool, filter)
			require.NoError(t, err)
			out := make([]string, 0, len(candidates))
			for _, c := range candidates {
				out = append(out, c.Name)
			}
			return out
		}

		assert.ElementsMatch(t, []string{"Diani Beach"}, names(catalog.ComposerFilter{
			Interests: []string{"Beach"},
		}))

		assert.ElementsMatch(t, []string{
			"Lake Paradise", "Lake Turkana", "Diani Beach", "Maasai Mara",
			"Amboseli", "Lake Nakuru", "Karura Forest",
		}, names(catalog.ComposerFilter{Interests: []string{"Wildlife"}}))

		assert.ElementsMatch(t, []string{
			"Lake Turkana", "Diani Beach", "Maasai Mara", "Amboseli",
		}, names(catalog.ComposerFilter{TransportMode: "flight"}))

		assert.ElementsMatch(t, []string{
			"Fort Jesus", "Diani Beach", "Lake Nakuru", "Maasai Mara",
			"Amboseli", "Karura Forest",
		}, names(catalog.ComposerFilter{Season: "migration"}))
		assert.Len(t, names(catalog.ComposerFilter{Season: "dry"}), 10)

		assert.ElementsMatch(t, []string{
			"Hell's Kitchen", "Chalbi Desert", "Lake Turkana", "Fort Jesus",
			"Diani Beach", "Karura Forest",
		}, names(catalog.ComposerFilter{BudgetBand: "budget-smart"}))
		assert.Len(t, names(catalog.ComposerFilter{BudgetBand: "balanced"}), 10)
		assert.Len(t, names(catalog.ComposerFilter{BudgetBand: "experience-first"}), 10)

		assert.ElementsMatch(t, []string{"Hell's Kitchen", "Fort Jesus"}, names(catalog.ComposerFilter{
			CountyCodes: []string{"003", "001"},
		}))
		assert.Len(t, names(catalog.ComposerFilter{Limit: 3}), 3)

		_, err := catalog.QueryComposerCandidates(ctx, pool, catalog.ComposerFilter{
			Interests: []string{"NotACategory"},
		})
		require.Error(t, err)
		_, err = catalog.QueryComposerCandidates(ctx, pool, catalog.ComposerFilter{
			TransportMode: "hovercraft",
		})
		require.Error(t, err)
		_, err = catalog.QueryComposerCandidates(ctx, pool, catalog.ComposerFilter{
			Season: "winter",
		})
		require.Error(t, err)
		_, err = catalog.QueryComposerCandidates(ctx, pool, catalog.ComposerFilter{
			BudgetBand: "luxury",
		})
		require.Error(t, err)
	})

	t.Run("eligibility predicate excludes non-ready rows", func(t *testing.T) {
		insertComposerReady(t, pool, "synthetic-draft", "Synthetic Draft", "047", "draft", true)
		insertComposerReady(t, pool, "synthetic-uncurated", "Synthetic Uncurated", "047", "published", false)
		insertComposerReadyMissingFields(t, pool, "synthetic-incomplete", "Synthetic Incomplete", "047")

		candidates, err := catalog.QueryComposerCandidates(ctx, pool, catalog.ComposerFilter{})
		require.NoError(t, err)
		assert.Len(t, candidates, 10)
	})

	t.Run("unverified candidates carry only the allowed fields", func(t *testing.T) {
		_, err := pool.Exec(ctx, `
			INSERT INTO composer_unverified_candidates (
				place_id, name, address, county_code, locality, category,
				interest_tags, location, rating, review_count, price_level,
				hero_image_url, hero_image_attribution, opening_hours, google_types
			) VALUES (
				'synthetic-google-1', 'Synthetic Google Park', 'Kapenguria, West Pokot',
				'024', 'Kapenguria', 'Wildlife',
				ARRAY['Wildlife', 'Conservation'],
				ST_SetSRID(ST_MakePoint(35.1, 1.2), 4326),
				4.6, 321, 'PRICE_LEVEL_MODERATE',
				'https://example.com/photo.jpg', 'Test Photographer',
				'{"weekdayDescriptions":["6:00-18:00"]}'::jsonb,
				ARRAY['national_park']
			)
		`)
		require.NoError(t, err)

		// Expired candidates are not eligible.
		_, err = pool.Exec(ctx, `
			INSERT INTO composer_unverified_candidates (
				place_id, name, county_code, category, interest_tags, location,
				expires_at
			) VALUES (
				'synthetic-google-expired', 'Expired Google Place', '024', 'Wildlife',
				ARRAY['Wildlife'], ST_SetSRID(ST_MakePoint(35.1, 1.2), 4326),
				now() - interval '1 day'
			)
		`)
		require.NoError(t, err)

		candidates, err := catalog.QueryComposerCandidates(ctx, pool, catalog.ComposerFilter{
			Provenance: catalog.ProvenanceGooglePlaces,
		})
		require.NoError(t, err)
		require.Len(t, candidates, 1)
		c := candidates[0]

		assert.Equal(t, catalog.ProvenanceGooglePlaces, c.Provenance)
		assert.Equal(t, catalog.EligibilityUnverified, c.Eligibility)
		assert.Nil(t, c.DestinationID)
		assert.Nil(t, c.Slug)
		require.NotNil(t, c.PlaceID)
		assert.Equal(t, "synthetic-google-1", *c.PlaceID)
		assert.Equal(t, "West Pokot", c.County)
		assert.Equal(t, "024", c.CountyCode)
		assert.Equal(t, "Wildlife", c.Category)
		assert.ElementsMatch(t, []string{"Wildlife", "Conservation"}, c.Interests)
		assert.Empty(t, c.TransportModes)
		assert.Empty(t, c.BestSeasons)
		assert.Nil(t, c.DurationMinMinutes)
		assert.Nil(t, c.DurationMaxMinutes)
		assert.Nil(t, c.FeeStatus)
		assert.Nil(t, c.FeeCurrency)
		assert.Nil(t, c.FeeAmount)
		assert.Nil(t, c.FeeBasis)
		assert.Nil(t, c.VerifiedAt)
		require.NotNil(t, c.OpeningHoursStatus)
		assert.Equal(t, "unverified", *c.OpeningHoursStatus)
		assert.NotEmpty(t, c.OpeningHours)
		require.NotNil(t, c.Rating)
		assert.Equal(t, 4.6, *c.Rating)
		require.NotNil(t, c.ReviewCount)
		assert.Equal(t, 321, *c.ReviewCount)
		require.NotNil(t, c.PriceLevel)
		assert.Equal(t, "PRICE_LEVEL_MODERATE", *c.PriceLevel)
		require.NotNil(t, c.HeroImageAttribution)
		assert.Equal(t, "Test Photographer", *c.HeroImageAttribution)
		assert.Equal(t, catalog.ProvenanceGooglePlaces, c.Source)
		assert.Equal(t, catalog.CoverageLimited, c.CountyCoverageStatus)
	})

	t.Run("county coverage drives limited and empty states", func(t *testing.T) {
		coverage, err := catalog.QueryComposerCountyCoverage(ctx, pool)
		require.NoError(t, err)
		require.Len(t, coverage, 47)

		byCode := map[string]catalog.ComposerCountyCoverage{}
		for _, c := range coverage {
			byCode[c.CountyCode] = c
		}

		nairobi := byCode["047"]
		assert.Equal(t, 1, nairobi.CuratedCount)
		assert.Equal(t, 0, nairobi.UnverifiedCount)
		assert.Equal(t, 1, nairobi.TotalCount)
		assert.Equal(t, catalog.CoverageLimited, nairobi.CoverageStatus)

		westPokot := byCode["024"]
		assert.Equal(t, 0, westPokot.CuratedCount)
		assert.Equal(t, 1, westPokot.UnverifiedCount)
		assert.Equal(t, 1, westPokot.TotalCount)
		assert.Equal(t, catalog.CoverageLimited, westPokot.CoverageStatus)

		lamu := byCode["005"]
		assert.Equal(t, 0, lamu.CuratedCount)
		assert.Equal(t, 0, lamu.UnverifiedCount)
		assert.Equal(t, 0, lamu.TotalCount)
		assert.Equal(t, catalog.CoverageNotCovered, lamu.CoverageStatus)

		// Two more composer-ready Nairobi rows take the county to 3 curated.
		insertComposerReady(t, pool, "synthetic-nairobi-1", "Synthetic Nairobi One", "047", "published", true)
		insertComposerReady(t, pool, "synthetic-nairobi-2", "Synthetic Nairobi Two", "047", "published", true)

		coverage, err = catalog.QueryComposerCountyCoverage(ctx, pool)
		require.NoError(t, err)
		byCode = map[string]catalog.ComposerCountyCoverage{}
		for _, c := range coverage {
			byCode[c.CountyCode] = c
		}
		nairobi = byCode["047"]
		assert.Equal(t, 3, nairobi.CuratedCount)
		assert.Equal(t, 3, nairobi.TotalCount)
		assert.Equal(t, catalog.CoverageCovered, nairobi.CoverageStatus)
	})
}

func insertComposerReady(t *testing.T, pool *pgxpool.Pool, slug, name, countyCode, status string, curated bool) {
	t.Helper()
	_, err := pool.Exec(context.Background(), `
		INSERT INTO destinations (
			slug, name, county, county_code, locality, category, interest_tags,
			transport_modes, best_seasons, status, is_curated, location,
			short_description, visit_duration_min_minutes, visit_duration_max_minutes,
			fee_status, fee_basis, opening_hours_status, hero_image_attribution,
			source, content_owner, verification_status
		)
		SELECT $1, $2, k.adm1_name, $3, NULL, 'Wildlife',
		       ARRAY['Wildlife', 'Conservation'], ARRAY['car'], ARRAY['year_round'],
		       $4::text, $5::boolean,
		       ST_SetSRID(ST_MakePoint(36.8219, -1.2921), 4326),
		       'Synthetic composer-ready test row.', 60, 120, 'free', 'No fee.',
		       'unverified', 'YTCI', 'test fixture', 'YTCI Content Team', 'verified'
		FROM kenya_counties k WHERE k.county_code = $3
	`, slug, name, countyCode, status, curated)
	require.NoError(t, err)
}

func insertComposerReadyMissingFields(t *testing.T, pool *pgxpool.Pool, slug, name, countyCode string) {
	t.Helper()
	_, err := pool.Exec(context.Background(), `
		INSERT INTO destinations (
			slug, name, county, county_code, locality, category, interest_tags,
			transport_modes, best_seasons, status, is_curated, location,
			short_description, visit_duration_min_minutes, visit_duration_max_minutes,
			fee_status, fee_basis, opening_hours_status, hero_image_attribution,
			source, content_owner, verification_status
		)
		SELECT $1, $2, k.adm1_name, $3, NULL, 'Wildlife',
		       ARRAY['Wildlife'], ARRAY['car'], ARRAY['year_round'],
		       'published', true,
		       ST_SetSRID(ST_MakePoint(36.8219, -1.2921), 4326),
		       'Synthetic incomplete row.', NULL, NULL, NULL, NULL, NULL, NULL,
		       'test fixture', 'YTCI Content Team', 'verified'
		FROM kenya_counties k WHERE k.county_code = $3
	`, slug, name, countyCode)
	require.NoError(t, err)
}
