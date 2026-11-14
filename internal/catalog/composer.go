package catalog

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
)

const (
	ProvenanceCurated      = "curated"
	ProvenanceGooglePlaces = "google_places"

	EligibilityCurated    = "curated_composer_ready"
	EligibilityUnverified = "unverified_google_places"

	CoverageCovered    = "covered"
	CoverageLimited    = "limited"
	CoverageNotCovered = "not_covered"
)

// ComposerCandidate is one row of the composer_catalog read model.
type ComposerCandidate struct {
	DestinationID        *string         `json:"destinationId,omitempty"`
	Provenance           string          `json:"provenance"`
	PlaceID              *string         `json:"placeId,omitempty"`
	Name                 string          `json:"name"`
	Slug                 *string         `json:"slug,omitempty"`
	CountyCode           string          `json:"countyCode"`
	County               string          `json:"county"`
	Locality             *string         `json:"locality,omitempty"`
	Category             string          `json:"category"`
	Interests            []string        `json:"interests"`
	TransportModes       []string        `json:"transportModes"`
	BestSeasons          []string        `json:"bestSeasons"`
	Latitude             float64         `json:"latitude"`
	Longitude            float64         `json:"longitude"`
	ShortDescription     *string         `json:"shortDescription,omitempty"`
	Address              *string         `json:"address,omitempty"`
	DurationMinMinutes   *int            `json:"durationMinMinutes,omitempty"`
	DurationMaxMinutes   *int            `json:"durationMaxMinutes,omitempty"`
	FeeStatus            *string         `json:"feeStatus,omitempty"`
	FeeCurrency          *string         `json:"feeCurrency,omitempty"`
	FeeAmount            *int            `json:"feeAmount,omitempty"`
	FeeBasis             *string         `json:"feeBasis,omitempty"`
	OpeningHoursStatus   *string         `json:"openingHoursStatus,omitempty"`
	OpeningHours         json.RawMessage `json:"openingHours,omitempty"`
	HeroImageURL         *string         `json:"heroImageUrl,omitempty"`
	HeroImageAttribution *string         `json:"heroImageAttribution,omitempty"`
	HeroImageLicense     *string         `json:"heroImageLicense,omitempty"`
	HeroImageSource      *string         `json:"heroImageSource,omitempty"`
	Rating               *float64        `json:"rating,omitempty"`
	ReviewCount          *int            `json:"reviewCount,omitempty"`
	PriceLevel           *string         `json:"priceLevel,omitempty"`
	VerifiedAt           *time.Time      `json:"verifiedAt,omitempty"`
	Source               string          `json:"source"`
	Eligibility          string          `json:"eligibility"`
	CountyCoverageStatus string          `json:"countyCoverageStatus"`
}

// ComposerFilter is the candidate query contract. It filters; it does not
// score. Budget caps apply to entry fees only and are opt-in via the band.
type ComposerFilter struct {
	Interests      []string
	Provenance     string
	CountyCodes    []string
	TransportMode  string
	Season         string
	BudgetBand     string
	MaxEntryFeeKes *int64
	USDToKES       float64
	EURToKES       float64
	GBPToKES       float64
	Limit          int
}

// ComposerCountyCoverage is one row of composer_county_coverage.
type ComposerCountyCoverage struct {
	CountyCode      string `json:"countyCode"`
	County          string `json:"county"`
	CuratedCount    int    `json:"curatedCount"`
	UnverifiedCount int    `json:"unverifiedCount"`
	TotalCount      int    `json:"totalCount"`
	CoverageStatus  string `json:"coverageStatus"`
}

var (
	validTransportModes = map[string]bool{
		"car": true, "4x4": true, "flight": true, "public_transport": true,
		"boat": true, "walking": true, "cycling": true,
	}
	validSeasons = map[string]bool{
		"dry": true, "green": true, "migration": true, "year_round": true,
	}
	validBudgetBands = map[string]bool{
		"": true, "budget-smart": true, "balanced": true, "experience-first": true,
	}
)

// QueryComposerCandidates reads the composer_catalog view with optional
// filters. Curated rows are returned before unverified Google Places rows.
func QueryComposerCandidates(ctx context.Context, q Queryer, filter ComposerFilter) ([]ComposerCandidate, error) {
	query, args, err := composerCandidatesQuery(filter)
	if err != nil {
		return nil, err
	}

	rows, err := q.Query(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("catalog: composer candidates: %w", err)
	}
	defer rows.Close()

	out := make([]ComposerCandidate, 0)
	for rows.Next() {
		var (
			destinationID, placeID, slug, locality, shortDescription, address   *string
			feeStatus, feeCurrency, feeBasis                                    *string
			openingStatus, heroURL, heroAttribution, heroLicense, heroSource    *string
			priceLevel                                                          *string
			provenance, name, countyCode, county, category, source, eligibility string
			countyCoverageStatus                                                string
			interests, transportModes, bestSeasons                              []string
			latitude, longitude                                                 float64
			durationMin, durationMax, feeAmount, reviewCount                    *int
			openingHours                                                        []byte
			rating                                                              *float64
			verifiedAt                                                          *time.Time
		)

		if err := rows.Scan(
			&destinationID, &provenance, &placeID, &name, &slug, &countyCode,
			&county, &locality, &category, &interests, &transportModes, &bestSeasons,
			&latitude, &longitude, &shortDescription, &address,
			&durationMin, &durationMax, &feeStatus, &feeCurrency, &feeAmount,
			&feeBasis, &openingStatus, &openingHours, &heroURL, &heroAttribution,
			&heroLicense, &heroSource, &rating, &reviewCount, &priceLevel,
			&verifiedAt, &source, &eligibility, &countyCoverageStatus,
		); err != nil {
			return nil, fmt.Errorf("catalog: scan composer candidate: %w", err)
		}

		candidate := ComposerCandidate{
			DestinationID:        destinationID,
			Provenance:           provenance,
			PlaceID:              placeID,
			Name:                 name,
			Slug:                 slug,
			CountyCode:           countyCode,
			County:               county,
			Locality:             locality,
			Category:             category,
			Interests:            interests,
			TransportModes:       transportModes,
			BestSeasons:          bestSeasons,
			Latitude:             latitude,
			Longitude:            longitude,
			ShortDescription:     shortDescription,
			Address:              address,
			DurationMinMinutes:   durationMin,
			DurationMaxMinutes:   durationMax,
			FeeStatus:            feeStatus,
			FeeCurrency:          feeCurrency,
			FeeAmount:            feeAmount,
			FeeBasis:             feeBasis,
			OpeningHoursStatus:   openingStatus,
			HeroImageURL:         heroURL,
			HeroImageAttribution: heroAttribution,
			HeroImageLicense:     heroLicense,
			HeroImageSource:      heroSource,
			Rating:               rating,
			ReviewCount:          reviewCount,
			PriceLevel:           priceLevel,
			VerifiedAt:           verifiedAt,
			Source:               source,
			Eligibility:          eligibility,
			CountyCoverageStatus: countyCoverageStatus,
		}
		if len(openingHours) > 0 {
			candidate.OpeningHours = json.RawMessage(openingHours)
		}
		out = append(out, candidate)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("catalog: composer candidates: %w", err)
	}
	return out, nil
}

// QueryComposerCountyCoverage returns all 47 counties with curated/unverified
// counts and the coverage status used by the composer's limited/empty states.
func QueryComposerCountyCoverage(ctx context.Context, q Queryer) ([]ComposerCountyCoverage, error) {
	rows, err := q.Query(ctx, `
		SELECT county_code, county, curated_count, unverified_count,
		       total_count, coverage_status
		FROM composer_county_coverage
		ORDER BY county_code
	`)
	if err != nil {
		return nil, fmt.Errorf("catalog: county coverage: %w", err)
	}
	defer rows.Close()

	out := make([]ComposerCountyCoverage, 0, 47)
	for rows.Next() {
		var c ComposerCountyCoverage
		if err := rows.Scan(&c.CountyCode, &c.County, &c.CuratedCount,
			&c.UnverifiedCount, &c.TotalCount, &c.CoverageStatus); err != nil {
			return nil, fmt.Errorf("catalog: scan county coverage: %w", err)
		}
		out = append(out, c)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("catalog: county coverage: %w", err)
	}
	return out, nil
}

func composerCandidatesQuery(filter ComposerFilter) (string, []any, error) {
	if err := validateComposerFilter(filter); err != nil {
		return "", nil, err
	}

	args := make([]any, 0, 12)
	addArg := func(v any) int {
		args = append(args, v)
		return len(args)
	}
	clauses := make([]string, 0, 8)

	if len(filter.Interests) > 0 {
		clauses = append(clauses, fmt.Sprintf(
			"interest_tags && $%d::text[]", addArg(filter.Interests)))
	}
	if filter.Provenance != "" {
		clauses = append(clauses, fmt.Sprintf(
			"provenance = $%d", addArg(filter.Provenance)))
	}
	if len(filter.CountyCodes) > 0 {
		clauses = append(clauses, fmt.Sprintf(
			"county_code = ANY($%d::text[])", addArg(filter.CountyCodes)))
	}
	if filter.TransportMode != "" {
		clauses = append(clauses, fmt.Sprintf(
			"transport_modes && ARRAY[$%d]::text[]", addArg(filter.TransportMode)))
	}
	if filter.Season != "" {
		if filter.Season == "year_round" {
			clauses = append(clauses, "best_seasons && ARRAY['year_round']::text[]")
		} else {
			clauses = append(clauses, fmt.Sprintf(
				"best_seasons && ARRAY[$%d, 'year_round']::text[]", addArg(filter.Season)))
		}
	}

	maxFee, err := composerMaxEntryFeeKes(filter)
	if err != nil {
		return "", nil, err
	}
	if maxFee != nil {
		usd := filter.USDToKES
		if usd <= 0 {
			usd = 130
		}
		eur := filter.EURToKES
		if eur <= 0 {
			eur = 140
		}
		gbp := filter.GBPToKES
		if gbp <= 0 {
			gbp = 165
		}
		capArg := addArg(*maxFee)
		usdArg := addArg(usd)
		eurArg := addArg(eur)
		gbpArg := addArg(gbp)
		clauses = append(clauses, fmt.Sprintf(`(
			provenance = 'google_places'
			OR fee_status IN ('free', 'not_verified')
			OR fee_amount IS NULL
			OR (
				CASE fee_currency
					WHEN 'KES' THEN fee_amount::numeric
					WHEN 'USD' THEN fee_amount::numeric * $%d::numeric
					WHEN 'EUR' THEN fee_amount::numeric * $%d::numeric
					WHEN 'GBP' THEN fee_amount::numeric * $%d::numeric
					ELSE NULL
				END
			) <= $%d
		)`, usdArg, eurArg, gbpArg, capArg))
	}

	limit := filter.Limit
	if limit == 0 {
		limit = 500
	}

	query := `
		SELECT destination_id::text, provenance, place_id, name, slug, county_code,
		       county, locality, category, interest_tags, transport_modes,
		       best_seasons, ST_Y(location), ST_X(location), short_description,
		       address, duration_min_minutes, duration_max_minutes, fee_status,
		       fee_currency, fee_amount, fee_basis, opening_hours_status,
		       opening_hours, hero_image_url, hero_image_attribution,
		       hero_image_license, hero_image_source, rating, review_count,
		       price_level, verified_at, source, eligibility,
		       (SELECT coverage_status FROM composer_county_coverage cov
		        WHERE cov.county_code = composer_catalog.county_code) AS county_coverage_status
		FROM composer_catalog`
	if len(clauses) > 0 {
		query += "\nWHERE " + strings.Join(clauses, "\n  AND ")
	}
	query += `
		ORDER BY CASE provenance WHEN 'curated' THEN 0 ELSE 1 END,
		         county_code, name`
	query += fmt.Sprintf("\n\t\tLIMIT $%d", addArg(limit))
	return query, args, nil
}

func validateComposerFilter(filter ComposerFilter) error {
	for _, interest := range filter.Interests {
		if !IsCategory(interest) {
			return fmt.Errorf("catalog: interest %q is not a controlled category", interest)
		}
	}
	if filter.Provenance != "" &&
		filter.Provenance != ProvenanceCurated &&
		filter.Provenance != ProvenanceGooglePlaces {
		return fmt.Errorf("catalog: unknown provenance %q", filter.Provenance)
	}
	if filter.TransportMode != "" && !validTransportModes[filter.TransportMode] {
		return fmt.Errorf("catalog: unknown transport mode %q", filter.TransportMode)
	}
	if filter.Season != "" && !validSeasons[filter.Season] {
		return fmt.Errorf("catalog: unknown season %q", filter.Season)
	}
	if !validBudgetBands[filter.BudgetBand] {
		return fmt.Errorf("catalog: unknown budget band %q", filter.BudgetBand)
	}
	if filter.MaxEntryFeeKes != nil && *filter.MaxEntryFeeKes < 0 {
		return errors.New("catalog: max entry fee cannot be negative")
	}
	if filter.Limit < 0 {
		return errors.New("catalog: limit cannot be negative")
	}
	return nil
}

func composerMaxEntryFeeKes(filter ComposerFilter) (*int64, error) {
	if filter.MaxEntryFeeKes != nil {
		return filter.MaxEntryFeeKes, nil
	}
	var cap int64
	switch filter.BudgetBand {
	case "budget-smart":
		cap = 1500
	case "balanced":
		cap = 15000
	case "", "experience-first":
		return nil, nil
	default:
		return nil, fmt.Errorf("catalog: unknown budget band %q", filter.BudgetBand)
	}
	return &cap, nil
}
