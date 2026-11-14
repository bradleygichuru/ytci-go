package expo

import (
	"context"
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

func TestGoldenDestinationFixturesAreComposerReady(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}
	ctx := context.Background()
	pool := db.SetupTestDB(t)
	defer pool.Close()

	_, filename, _, ok := runtime.Caller(0)
	require.True(t, ok)
	seedPath := filepath.Join(filepath.Dir(filename), "..", "..", "..", "testdata", "seeds", "golden_destinations.sql")
	seed, err := os.ReadFile(seedPath)
	require.NoError(t, err)
	_, err = pool.Exec(ctx, string(seed))
	require.NoError(t, err, "golden seed must be idempotent and self-validating")

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

	rows, err := pool.Query(ctx, `
		SELECT slug, county_code, category, interest_tags, short_description,
		       status, is_curated, verification_status, verified_at,
		       visit_duration_min_minutes, visit_duration_max_minutes,
		       fee_status, fee_basis, opening_hours_status, opening_hours,
		       hero_image_attribution, source, content_owner,
		       location IS NOT NULL, ST_IsValid(location)
		FROM destinations
		WHERE is_curated
		ORDER BY slug
	`)
	require.NoError(t, err)
	defer rows.Close()

	seen := map[string]bool{}
	for rows.Next() {
		var (
			slug, countyCode, category, shortDescription string
			interests                                    []string
			status, verificationStatus                   string
			isCurated                                    bool
			verifiedAt                                   *time.Time
			durationMin, durationMax                     int
			feeStatus, feeBasis                          string
			openingStatus                                string
			openingHours                                 []byte
			heroAttribution, source, owner               string
			hasLocation, validLocation                   bool
		)
		require.NoError(t, rows.Scan(
			&slug, &countyCode, &category, &interests, &shortDescription,
			&status, &isCurated, &verificationStatus, &verifiedAt,
			&durationMin, &durationMax,
			&feeStatus, &feeBasis, &openingStatus, &openingHours,
			&heroAttribution, &source, &owner,
			&hasLocation, &validLocation,
		))
		seen[slug] = true

		wantCounty, ok := expected[slug]
		require.True(t, ok, "unexpected curated row %q", slug)
		assert.Equal(t, wantCounty, countyCode, slug)
		assert.True(t, isCurated, slug)
		assert.True(t, catalog.IsCategory(category), "%s category %q", slug, category)
		assert.NotEmpty(t, interests, slug)
		for _, interest := range interests {
			assert.True(t, catalog.IsCategory(interest), "%s interest %q", slug, interest)
		}
		assert.NotEmpty(t, shortDescription, slug)
		assert.NotContains(t, shortDescription, "P.O. Box", slug)
		assert.Equal(t, "published", status, slug)
		assert.Equal(t, "verified", verificationStatus, slug)
		assert.NotNil(t, verifiedAt, slug)
		assert.Greater(t, durationMin, 0, slug)
		assert.GreaterOrEqual(t, durationMax, durationMin, slug)
		assert.Contains(t, []string{"verified", "reported", "not_verified", "free"}, feeStatus, slug)
		assert.NotEmpty(t, feeBasis, slug)
		assert.Contains(t, []string{"verified", "unverified"}, openingStatus, slug)
		if openingStatus == "verified" {
			assert.NotEmpty(t, openingHours, slug)
		}
		assert.NotEmpty(t, heroAttribution, slug)
		assert.NotEmpty(t, source, slug)
		assert.NotEmpty(t, owner, slug)
		assert.True(t, hasLocation, slug)
		assert.True(t, validLocation, slug)
	}
	require.NoError(t, rows.Err())
	assert.Len(t, seen, 10)

	for slug := range expected {
		assert.True(t, seen[slug], "missing golden fixture %q", slug)
	}

	var curatedCount int
	require.NoError(t, pool.QueryRow(ctx, `
		SELECT count(*) FROM catalog_county_coverage WHERE curated_count > 0
	`).Scan(&curatedCount))
	assert.Equal(t, 9, curatedCount, "the 10 fixtures span 9 counties")

	var fixtureTotal int
	require.NoError(t, pool.QueryRow(ctx, `
		SELECT sum(curated_count) FROM catalog_county_coverage
	`).Scan(&fixtureTotal))
	assert.Equal(t, 10, fixtureTotal)
}
