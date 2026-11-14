package discovery

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"sort"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/bradleygichuru/ytci-go/internal/catalog"
	"github.com/bradleygichuru/ytci-go/internal/places"
)

// ErrQuotaExceeded means today's discovery request budget is exhausted.
var ErrQuotaExceeded = errors.New("discovery: daily request budget exceeded")

// Searcher is the subset of places.Client used by discovery, which keeps the
// adapter testable with canned Google responses.
type Searcher interface {
	NearbySearchWithOptions(ctx context.Context, opts places.NearbySearchOptions) ([]places.NearbyPlace, error)
}

// Adapter discovers unverified Google Places candidates for a county or a
// route corridor. It never writes to the destinations catalog.
type Adapter struct {
	pool               *pgxpool.Pool
	searcher           Searcher
	dailyRequestBudget int
	now                func() time.Time
}

func NewAdapter(pool *pgxpool.Pool, searcher Searcher, dailyRequestBudget int) *Adapter {
	if dailyRequestBudget <= 0 {
		dailyRequestBudget = DefaultDailyRequestBudget
	}
	return &Adapter{
		pool:               pool,
		searcher:           searcher,
		dailyRequestBudget: dailyRequestBudget,
		now:                time.Now,
	}
}

// Candidate is one unverified snapshot returned to the composer.
type Candidate struct {
	PlaceID              string          `json:"placeId"`
	Name                 string          `json:"name"`
	Address              *string         `json:"address,omitempty"`
	CountyCode           string          `json:"countyCode"`
	County               string          `json:"county"`
	Category             string          `json:"category"`
	Interests            []string        `json:"interests"`
	Latitude             float64         `json:"latitude"`
	Longitude            float64         `json:"longitude"`
	Rating               *float64        `json:"rating,omitempty"`
	ReviewCount          *int            `json:"reviewCount,omitempty"`
	PriceLevel           *string         `json:"priceLevel,omitempty"`
	HeroImageURL         *string         `json:"heroImageUrl,omitempty"`
	HeroImageAttribution *string         `json:"heroImageAttribution,omitempty"`
	OpeningHours         json.RawMessage `json:"openingHours,omitempty"`
	GoogleTypes          []string        `json:"googleTypes,omitempty"`
}

// Result describes one discovery run.
type Result struct {
	ScopeKind    string      `json:"scopeKind"`
	ScopeKey     string      `json:"scopeKey"`
	CountyCode   *string     `json:"countyCode,omitempty"`
	County       *string     `json:"county,omitempty"`
	Candidates   []Candidate `json:"candidates"`
	RequestCount int         `json:"requestCount"`
	Cached       bool        `json:"cached"`
}

// DiscoverCounty searches inside the county polygon from one or more points.
// A successful run is reused for the UTC date bucket.
func (a *Adapter) DiscoverCounty(ctx context.Context, countyCode string) (Result, error) {
	bucket := a.bucketDate()
	if cached, ok, err := a.cachedRun(ctx, bucket, "county", countyCode); err != nil {
		return Result{}, err
	} else if ok {
		cached.CountyCode = &countyCode
		return cached, nil
	}

	plan, err := CountySearchPlan(ctx, a.pool, countyCode)
	if err != nil {
		return Result{}, err
	}
	if err := a.reserveQuota(ctx, bucket, len(plan.Points)); err != nil {
		return Result{}, err
	}
	if err := a.beginRun(ctx, bucket, "county", countyCode, &countyCode); err != nil {
		return Result{}, err
	}

	candidates, requests, searchErr := a.searchPoints(ctx, plan.Points, plan.RadiusMeters)
	if searchErr != nil && len(candidates) == 0 {
		a.finishRun(ctx, bucket, "county", countyCode, "failed", requests, nil, searchErr.Error())
		return Result{
			ScopeKind: "county", ScopeKey: countyCode, CountyCode: &countyCode,
			County: &plan.County, RequestCount: requests,
		}, searchErr
	}

	ids, err := a.upsertCandidates(ctx, candidates)
	if err != nil {
		a.finishRun(ctx, bucket, "county", countyCode, "failed", requests, nil, err.Error())
		return Result{
			ScopeKind: "county", ScopeKey: countyCode, CountyCode: &countyCode,
			County: &plan.County, RequestCount: requests,
		}, err
	}

	errText := ""
	if searchErr != nil {
		errText = searchErr.Error()
	}
	a.finishRun(ctx, bucket, "county", countyCode, "success", requests, ids, errText)

	loaded, err := a.loadCandidates(ctx, ids)
	if err != nil {
		return Result{}, err
	}
	return Result{
		ScopeKind: "county", ScopeKey: countyCode, CountyCode: &countyCode,
		County: &plan.County, Candidates: loaded, RequestCount: requests,
	}, nil
}

// DiscoverRoute searches along a caller-supplied route corridor. The composer
// engine supplies waypoints from the routing provider; discovery never calls
// a routing service itself.
func (a *Adapter) DiscoverRoute(ctx context.Context, waypoints []places.LatLng, radiusMeters int) (Result, error) {
	points := uniqueWaypoints(waypoints)
	if len(points) == 0 {
		return Result{}, fmt.Errorf("discovery: route needs at least one waypoint")
	}
	if radiusMeters <= 0 {
		radiusMeters = 25000
	}
	if radiusMeters < 1000 {
		radiusMeters = 1000
	}
	if radiusMeters > 50000 {
		radiusMeters = 50000
	}

	scopeKey := routeScopeKey(points, radiusMeters)
	bucket := a.bucketDate()
	if cached, ok, err := a.cachedRun(ctx, bucket, "route", scopeKey); err != nil {
		return Result{}, err
	} else if ok {
		return cached, nil
	}
	if err := a.reserveQuota(ctx, bucket, len(points)); err != nil {
		return Result{}, err
	}
	if err := a.beginRun(ctx, bucket, "route", scopeKey, nil); err != nil {
		return Result{}, err
	}

	candidates, requests, searchErr := a.searchPoints(ctx, points, radiusMeters)
	if searchErr != nil && len(candidates) == 0 {
		a.finishRun(ctx, bucket, "route", scopeKey, "failed", requests, nil, searchErr.Error())
		return Result{ScopeKind: "route", ScopeKey: scopeKey, RequestCount: requests}, searchErr
	}

	ids, err := a.upsertCandidates(ctx, candidates)
	if err != nil {
		a.finishRun(ctx, bucket, "route", scopeKey, "failed", requests, nil, err.Error())
		return Result{ScopeKind: "route", ScopeKey: scopeKey, RequestCount: requests}, err
	}

	errText := ""
	if searchErr != nil {
		errText = searchErr.Error()
	}
	a.finishRun(ctx, bucket, "route", scopeKey, "success", requests, ids, errText)

	loaded, err := a.loadCandidates(ctx, ids)
	if err != nil {
		return Result{}, err
	}
	return Result{
		ScopeKind: "route", ScopeKey: scopeKey, Candidates: loaded,
		RequestCount: requests,
	}, nil
}

func (a *Adapter) searchPoints(ctx context.Context, points []SearchPoint, radiusMeters int) ([]Candidate, int, error) {
	seen := make(map[string]places.NearbyPlace, len(points)*10)
	requests := 0
	var lastErr error

	for _, point := range points {
		results, err := a.searcher.NearbySearchWithOptions(ctx, places.NearbySearchOptions{
			Center:         places.LatLng{Latitude: point.Latitude, Longitude: point.Longitude},
			RadiusMeters:   float64(radiusMeters),
			MaxResultCount: 20,
			IncludedTypes:  catalog.GoogleDiscoveryTypes(),
			ExcludedTypes:  catalog.GoogleDiscoveryExcludedTypes(),
		})
		requests++
		if err != nil {
			lastErr = err
			continue
		}
		for _, p := range results {
			if p.PlaceID == "" {
				continue
			}
			if _, ok := seen[p.PlaceID]; !ok {
				seen[p.PlaceID] = p
			}
		}
	}

	out := make([]Candidate, 0, len(seen))
	for _, p := range seen {
		candidate, err := a.candidateFromPlace(ctx, p)
		if err != nil {
			lastErr = err
			continue
		}
		if candidate != nil {
			out = append(out, *candidate)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].PlaceID < out[j].PlaceID })
	return out, requests, lastErr
}

func (a *Adapter) candidateFromPlace(ctx context.Context, p places.NearbyPlace) (*Candidate, error) {
	if p.Location == nil || p.DisplayName.Text == "" {
		return nil, nil
	}
	if p.BusinessStatus != "" && p.BusinessStatus != "OPERATIONAL" {
		return nil, nil
	}
	if catalog.IsDiscoveryNonDestination(p.DisplayName.Text) {
		return nil, nil
	}
	classification := catalog.ClassifyGoogleTypes(p.Types)
	if !classification.IsDestination {
		return nil, nil
	}
	county, err := catalog.ResolveCounty(ctx, a.pool, &p.Location.Latitude, &p.Location.Longitude, "")
	if err != nil {
		if errors.Is(err, catalog.ErrCountyNotFound) {
			return nil, nil
		}
		return nil, err
	}

	c := &Candidate{
		PlaceID:     p.PlaceID,
		Name:        p.DisplayName.Text,
		CountyCode:  county.Code,
		County:      county.Name,
		Category:    string(classification.Category),
		Interests:   classification.Interests,
		Latitude:    p.Location.Latitude,
		Longitude:   p.Location.Longitude,
		GoogleTypes: append([]string{}, p.Types...),
	}
	if p.FormattedAddress != "" {
		address := p.FormattedAddress
		c.Address = &address
	}
	if p.Rating > 0 {
		rating := p.Rating
		c.Rating = &rating
	}
	if p.UserRatingCount > 0 {
		reviews := int(p.UserRatingCount)
		c.ReviewCount = &reviews
	}
	if p.PriceLevel != "" {
		price := p.PriceLevel
		c.PriceLevel = &price
	}
	if len(p.Photos) > 0 && places.ValidPhotoRef(p.Photos[0].Name) {
		photo := p.Photos[0]
		proxy := "/v1/mobile/places/photo?ref=" + url.QueryEscape(photo.Name)
		c.HeroImageURL = &proxy
		attribution := "Google Places"
		if len(photo.AuthorAttributions) > 0 && photo.AuthorAttributions[0].DisplayName != "" {
			attribution = photo.AuthorAttributions[0].DisplayName
		}
		c.HeroImageAttribution = &attribution
	}
	if p.CurrentOpeningHours != nil {
		raw, err := json.Marshal(p.CurrentOpeningHours)
		if err == nil {
			c.OpeningHours = raw
		}
	}
	return c, nil
}

func (a *Adapter) upsertCandidates(ctx context.Context, candidates []Candidate) ([]string, error) {
	ids := make([]string, 0, len(candidates))
	for _, c := range candidates {
		var openingHours any
		if len(c.OpeningHours) > 0 {
			openingHours = string(c.OpeningHours)
		}
		var id string
		err := a.pool.QueryRow(ctx, `
			INSERT INTO composer_unverified_candidates (
				place_id, name, address, county_code, locality, category,
				interest_tags, location, rating, review_count, price_level,
				hero_image_url, hero_image_attribution, opening_hours,
				google_types, source, fetched_at, expires_at
			)
			SELECT $1, $2, $3, $4, NULL, $5, $6,
			       ST_SetSRID(ST_MakePoint($7, $8), 4326),
			       $9, $10, $11, $12, $13, $14::jsonb, $15, 'google_places',
			       now(), now() + interval '30 days'
			WHERE NOT EXISTS (
				SELECT 1 FROM destinations d
				WHERE d.is_curated
				  AND (
				      d.google_place_id = $1
				      OR (
				          d.county_code = $4
				          AND regexp_replace(lower(d.name), '[^a-z0-9]', '', 'g') =
				              regexp_replace(lower($2), '[^a-z0-9]', '', 'g')
				      )
				  )
			)
			ON CONFLICT (place_id) DO UPDATE SET
				name = EXCLUDED.name,
				address = EXCLUDED.address,
				county_code = EXCLUDED.county_code,
				category = EXCLUDED.category,
				interest_tags = EXCLUDED.interest_tags,
				location = EXCLUDED.location,
				rating = EXCLUDED.rating,
				review_count = EXCLUDED.review_count,
				price_level = EXCLUDED.price_level,
				hero_image_url = EXCLUDED.hero_image_url,
				hero_image_attribution = EXCLUDED.hero_image_attribution,
				opening_hours = EXCLUDED.opening_hours,
				google_types = EXCLUDED.google_types,
				fetched_at = now(),
				expires_at = now() + interval '30 days'
			RETURNING place_id
		`, c.PlaceID, c.Name, c.Address, c.CountyCode, c.Category, c.Interests,
			c.Longitude, c.Latitude, c.Rating, c.ReviewCount, c.PriceLevel,
			c.HeroImageURL, c.HeroImageAttribution, openingHours, c.GoogleTypes).Scan(&id)
		if errors.Is(err, pgx.ErrNoRows) {
			// A curated destination already owns this Google place id.
			continue
		}
		if err != nil {
			return nil, fmt.Errorf("discovery: store candidate %s: %w", c.PlaceID, err)
		}
		ids = append(ids, id)
	}

	// If a place was curated after a previous discovery run, remove the stale
	// unverified snapshot.
	_, _ = a.pool.Exec(ctx, `
		DELETE FROM composer_unverified_candidates c
		USING destinations d
		WHERE d.is_curated
		  AND (
		      d.google_place_id = c.place_id
		      OR (
		          d.county_code = c.county_code
		          AND regexp_replace(lower(d.name), '[^a-z0-9]', '', 'g') =
		              regexp_replace(lower(c.name), '[^a-z0-9]', '', 'g')
		      )
		  )
	`)
	return ids, nil
}

func (a *Adapter) loadCandidates(ctx context.Context, ids []string) ([]Candidate, error) {
	out := make([]Candidate, 0, len(ids))
	if len(ids) == 0 {
		return out, nil
	}
	rows, err := a.pool.Query(ctx, `
		SELECT c.place_id, c.name, c.address, c.county_code, k.adm1_name,
		       c.category, c.interest_tags, ST_Y(c.location), ST_X(c.location),
		       c.rating, c.review_count, c.price_level, c.hero_image_url,
		       c.hero_image_attribution, c.opening_hours, c.google_types
		FROM composer_unverified_candidates c
		JOIN kenya_counties k ON k.county_code = c.county_code
		WHERE c.place_id = ANY($1::text[]) AND c.expires_at > now()
		ORDER BY c.place_id
	`, ids)
	if err != nil {
		return nil, fmt.Errorf("discovery: load candidates: %w", err)
	}
	defer rows.Close()

	for rows.Next() {
		var (
			c                                             Candidate
			openingHours                                  []byte
			rating                                        *float64
			reviewCount                                   *int
			priceLevel, heroURL, heroAttribution, address *string
			interests, googleTypes                        []string
		)
		if err := rows.Scan(
			&c.PlaceID, &c.Name, &address, &c.CountyCode, &c.County,
			&c.Category, &interests, &c.Latitude, &c.Longitude,
			&rating, &reviewCount, &priceLevel, &heroURL,
			&heroAttribution, &openingHours, &googleTypes,
		); err != nil {
			return nil, fmt.Errorf("discovery: scan candidate: %w", err)
		}
		c.Address = address
		c.Interests = interests
		c.Rating = rating
		c.ReviewCount = reviewCount
		c.PriceLevel = priceLevel
		c.HeroImageURL = heroURL
		c.HeroImageAttribution = heroAttribution
		c.GoogleTypes = googleTypes
		if len(openingHours) > 0 {
			c.OpeningHours = json.RawMessage(openingHours)
		}
		out = append(out, c)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("discovery: load candidates: %w", err)
	}
	return out, nil
}

func (a *Adapter) cachedRun(ctx context.Context, bucket, scopeKind, scopeKey string) (Result, bool, error) {
	var (
		status     string
		requests   int
		ids        []string
		countyCode *string
		countyName string
	)
	err := a.pool.QueryRow(ctx, `
		SELECT r.status, r.request_count, r.candidate_place_ids,
		       r.county_code, COALESCE(k.adm1_name, '')
		FROM composer_discovery_runs r
		LEFT JOIN kenya_counties k ON k.county_code = r.county_code
		WHERE r.bucket_date = $1::date
		  AND r.scope_kind = $2
		  AND r.scope_key = $3
	`, bucket, scopeKind, scopeKey).Scan(&status, &requests, &ids, &countyCode, &countyName)
	if errors.Is(err, pgx.ErrNoRows) {
		return Result{}, false, nil
	}
	if err != nil {
		return Result{}, false, fmt.Errorf("discovery: read run cache: %w", err)
	}
	if status != "success" {
		return Result{}, false, nil
	}

	candidates, err := a.loadCandidates(ctx, ids)
	if err != nil {
		return Result{}, false, err
	}
	result := Result{
		ScopeKind: scopeKind, ScopeKey: scopeKey, Candidates: candidates,
		RequestCount: 0, Cached: true,
	}
	if countyCode != nil {
		result.CountyCode = countyCode
		result.County = &countyName
	}
	return result, true, nil
}

func (a *Adapter) beginRun(ctx context.Context, bucket, scopeKind, scopeKey string, countyCode *string) error {
	_, err := a.pool.Exec(ctx, `
		INSERT INTO composer_discovery_runs (
			bucket_date, scope_kind, scope_key, county_code, status, started_at
		) VALUES ($1::date, $2, $3, $4, 'running', now())
		ON CONFLICT (bucket_date, scope_kind, scope_key) DO UPDATE SET
			county_code = EXCLUDED.county_code,
			status = 'running',
			candidate_count = 0,
			candidate_place_ids = '{}',
			error = NULL,
			started_at = now(),
			finished_at = NULL
	`, bucket, scopeKind, scopeKey, countyCode)
	if err != nil {
		return fmt.Errorf("discovery: begin run: %w", err)
	}
	return nil
}

func (a *Adapter) finishRun(ctx context.Context, bucket, scopeKind, scopeKey, status string, requests int, ids []string, errText string) {
	if ids == nil {
		ids = []string{}
	}
	var errArg any
	if errText != "" {
		errArg = errText
	}
	_, _ = a.pool.Exec(ctx, `
		UPDATE composer_discovery_runs
		SET status = $4,
		    request_count = request_count + $5,
		    candidate_count = $6,
		    candidate_place_ids = $7,
		    error = $8,
		    finished_at = now()
		WHERE bucket_date = $1::date
		  AND scope_kind = $2
		  AND scope_key = $3
	`, bucket, scopeKind, scopeKey, status, requests, len(ids), ids, errArg)
}

func (a *Adapter) reserveQuota(ctx context.Context, bucket string, planned int) error {
	var used int
	if err := a.pool.QueryRow(ctx, `
		SELECT COALESCE(sum(request_count), 0)
		FROM composer_discovery_runs
		WHERE bucket_date = $1::date
	`, bucket).Scan(&used); err != nil {
		return fmt.Errorf("discovery: read quota: %w", err)
	}
	if used+planned > a.dailyRequestBudget {
		return fmt.Errorf("%w: used=%d planned=%d budget=%d",
			ErrQuotaExceeded, used, planned, a.dailyRequestBudget)
	}
	return nil
}

func (a *Adapter) bucketDate() string {
	return a.now().UTC().Format("2006-01-02")
}

func uniqueWaypoints(waypoints []places.LatLng) []SearchPoint {
	seen := make(map[string]bool, len(waypoints))
	out := make([]SearchPoint, 0, len(waypoints))
	for _, w := range waypoints {
		key := fmt.Sprintf("%.5f,%.5f", w.Latitude, w.Longitude)
		if seen[key] {
			continue
		}
		seen[key] = true
		out = append(out, SearchPoint{Latitude: w.Latitude, Longitude: w.Longitude})
	}
	return out
}

func routeScopeKey(points []SearchPoint, radiusMeters int) string {
	h := sha256.New()
	for _, p := range points {
		fmt.Fprintf(h, "%.5f,%.5f;", p.Latitude, p.Longitude)
	}
	fmt.Fprintf(h, "%d", radiusMeters)
	return "route:" + hex.EncodeToString(h.Sum(nil))[:32]
}
