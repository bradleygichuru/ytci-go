package composer

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"math"
	"sort"
	"time"

	"github.com/bradleygichuru/ytci-go/internal/catalog"
)

const (
	defaultSuggestionLimit = 10
	suggestionTTL          = 24 * time.Hour
)

type Engine struct {
	Candidates      CandidateProvider
	Coverage        CoverageProvider
	Resolver        CountyResolver
	Router          Router
	Weather         WeatherProvider
	Events          EventProvider
	Discovery       DiscoveryProvider
	Cache           SuggestionCache
	Usage           UsageStore
	Now             func() time.Time
	SuggestionLimit int
}

func NewEngine(
	candidates CandidateProvider,
	coverage CoverageProvider,
	resolver CountyResolver,
	router Router,
	weather WeatherProvider,
	events EventProvider,
	discovery DiscoveryProvider,
	cache SuggestionCache,
	usage UsageStore,
) *Engine {
	return &Engine{
		Candidates:      candidates,
		Coverage:        coverage,
		Resolver:        resolver,
		Router:          router,
		Weather:         weather,
		Events:          events,
		Discovery:       discovery,
		Cache:           cache,
		Usage:           usage,
		Now:             time.Now,
		SuggestionLimit: defaultSuggestionLimit,
	}
}

func (e *Engine) Suggest(ctx context.Context, userID string, brief Brief) (SuggestionResponse, error) {
	now := e.Now().UTC()
	if err := NormalizeBrief(&brief, now); err != nil {
		return SuggestionResponse{}, err
	}

	originText := brief.Origin.CountyCode
	if originText == "" {
		originText = brief.Origin.Name
	}
	originCounty, err := e.Resolver.ResolveCounty(ctx, brief.Origin.Latitude, brief.Origin.Longitude, originText)
	if errors.Is(err, catalog.ErrCountyNotFound) {
		return e.blockedResponse("ORIGIN_OUTSIDE_KENYA",
			"The trip origin must be inside Kenya.", nil, nil, now), nil
	}
	if err != nil {
		return SuggestionResponse{}, err
	}

	limit := e.SuggestionLimit
	if limit <= 0 {
		limit = defaultSuggestionLimit
	}
	used, allowed := 1, true
	if e.Usage != nil {
		used, allowed, err = e.Usage.ConsumeSuggestion(ctx, userID, now.Format("2006-01-02"), limit)
		if err != nil {
			return SuggestionResponse{}, err
		}
		if !allowed {
			return SuggestionResponse{}, &RateLimitError{
				Limit: limit, Used: used, RetryAfterSeconds: RetryAfterSeconds(now),
			}
		}
	}

	candidates, err := e.Candidates.Candidates(ctx)
	if err != nil {
		return SuggestionResponse{}, err
	}
	var coverage []catalog.ComposerCountyCoverage
	if e.Coverage != nil {
		coverage, err = e.Coverage.Coverage(ctx)
		if err != nil {
			return SuggestionResponse{}, err
		}
	} else {
		coverage = coverageFromCandidates(candidates)
	}
	originCoverage := findCoverage(coverage, originCounty.Code)

	if e.Discovery != nil && brief.AllowUnverified && originCoverage.TotalCount == 0 {
		if err := e.Discovery.DiscoverCounty(ctx, originCounty.Code); err == nil {
			candidates, err = e.Candidates.Candidates(ctx)
			if err != nil {
				return SuggestionResponse{}, err
			}
			if e.Coverage != nil {
				coverage, err = e.Coverage.Coverage(ctx)
				if err != nil {
					return SuggestionResponse{}, err
				}
				originCoverage = findCoverage(coverage, originCounty.Code)
			}
		}
	}

	if originCoverage.TotalCount == 0 {
		return e.blockedResponse("NOT_COVERED",
			"YTCI does not have candidates for this county yet. Try a nearby county or check the coverage report.",
			map[string]any{"county": originCounty.Name, "countyCode": originCounty.Code},
			coverageForCounty(coverage, originCounty.Code), now), nil
	}
	if originCoverage.CuratedCount == 0 && !brief.AllowUnverified {
		return e.blockedResponse("UNVERIFIED_OPT_IN_REQUIRED",
			"This county only has unverified Google Places candidates. Opt in to see an unverified plan.",
			map[string]any{"county": originCounty.Name, "countyCode": originCounty.Code},
			coverageForCounty(coverage, originCounty.Code), now), nil
	}
	requireCurated := originCoverage.CuratedCount > 0

	pool, preWarnings := e.candidatePool(brief, candidates, originCounty, coverage)
	if len(pool) == 0 {
		return e.blockedResponse("NO_FEASIBLE_CANDIDATES",
			"No candidates fit this brief. Try more days, a different budget, or a nearby origin.",
			map[string]any{"county": originCounty.Name},
			coverageForCounty(coverage, originCounty.Code), now), nil
	}
	if requireCurated {
		if curated, _ := countProvenance(pool); curated == 0 {
			return e.blockedResponse("CURATED_REQUIRED",
				"Curated candidates exist but none fit the distance envelope.",
				map[string]any{"county": originCounty.Name},
				coverageForCounty(coverage, originCounty.Code), now), nil
		}
	}

	catalogVersion := hashCandidates(pool)
	inputHash := hashBrief(brief, originCounty.Code, catalogVersion)

	if e.Cache != nil {
		if cached, ok, err := e.Cache.Get(ctx, inputHash); err == nil && ok {
			cached.Meta.Cached = true
			cached.Meta.SuggestionsUsed = used
			cached.Meta.SuggestionLimit = limit
			return cached, nil
		}
	}

	matrix, routeWarnings := e.travelMatrix(ctx, brief, originCounty, pool)
	weatherByDate, weatherWarning := e.weatherForBrief(ctx, brief, originCounty, pool)
	events, eventWarning := e.eventsForBrief(ctx, brief, pool, originCounty)

	response := SuggestionResponse{
		Suggestions: make([]Suggestion, 0, len(Profiles)),
		Meta: Meta{
			EngineVersion:   EngineVersion,
			CatalogVersion:  catalogVersion,
			InputHash:       inputHash,
			GeneratedAt:     now,
			SuggestionLimit: limit,
			SuggestionsUsed: used,
		},
	}
	for _, profile := range Profiles {
		plan := e.buildPlan(brief, pool, originCounty, coverage, profile, inputHash, matrix, weatherByDate, events, requireCurated)
		for _, warning := range routeWarnings {
			plan.Warnings = appendWarning(plan.Warnings, warning)
		}
		if weatherWarning != nil {
			plan.Warnings = appendWarning(plan.Warnings, *weatherWarning)
		}
		if eventWarning != nil {
			plan.Warnings = appendWarning(plan.Warnings, *eventWarning)
		}
		plan.Warnings = appendWarnings(plan.Warnings, preWarnings...)
		response.Suggestions = append(response.Suggestions, plan)
	}

	response.Coverage = coverageForPlans(coverage, response.Suggestions, originCounty.Code)
	if len(response.Suggestions) > 0 && allBlocked(response.Suggestions) {
		first := response.Suggestions[0]
		response.Blocked = &Blocked{
			Code:     "NO_FEASIBLE_ALTERNATIVES",
			Message:  "No feasible alternatives for this brief.",
			Coverage: response.Coverage,
		}
		if len(first.Warnings) > 0 {
			response.Blocked.Code = first.Warnings[0].Code
			response.Blocked.Message = first.Warnings[0].Message
			response.Blocked.Params = first.Warnings[0].Params
		}
	}

	if e.Cache != nil {
		_ = e.Cache.Set(ctx, inputHash, catalogVersion, response, suggestionTTL)
	}
	return response, nil
}

func (e *Engine) blockedResponse(code, message string, params map[string]any, coverage []catalog.ComposerCountyCoverage, now time.Time) SuggestionResponse {
	limit := e.SuggestionLimit
	if limit <= 0 {
		limit = defaultSuggestionLimit
	}
	return SuggestionResponse{
		Suggestions: []Suggestion{},
		Coverage:    coverageFromCatalog(coverage),
		Meta: Meta{
			EngineVersion:   EngineVersion,
			GeneratedAt:     now,
			SuggestionLimit: limit,
		},
		Blocked: &Blocked{
			Code:     code,
			Message:  message,
			Params:   params,
			Coverage: coverageFromCatalog(coverage),
		},
	}
}

func (e *Engine) candidatePool(brief Brief, candidates []catalog.ComposerCandidate, origin catalog.County, coverage []catalog.ComposerCountyCoverage) ([]catalog.ComposerCandidate, []Warning) {
	warnings := make([]Warning, 0)
	originCoverage := findCoverage(coverage, origin.Code)
	if originCoverage.CoverageStatus == catalog.CoverageLimited {
		warnings = appendWarning(warnings, Warning{
			Code: "LIMITED_COVERAGE", Severity: "caution",
			Message: "This county has limited curated coverage; nearby counties are included.",
			Params:  map[string]any{"county": origin.Name, "curatedCount": originCoverage.CuratedCount},
		})
	}
	maxDistance := maxDistanceKm(brief)
	must := map[string]bool{}
	for _, m := range brief.MustInclude {
		must[m] = true
	}
	type scored struct {
		candidate catalog.ComposerCandidate
		distance  float64
		score     float64
	}
	scoredPool := make([]scored, 0, len(candidates))
	for _, c := range candidates {
		if c.Latitude == 0 && c.Longitude == 0 {
			continue
		}
		op := originPoint(brief, origin, candidates)
		distance := haversineKm(op, Point{Latitude: c.Latitude, Longitude: c.Longitude})
		if distance > maxDistance && !must[candidateKey(c)] && !must[c.Name] && !must[candidateSlug(c)] {
			continue
		}
		pre := 0.55*interestScore(brief, c) + 0.30*qualityScore(c) + 0.15*(1-clamp01(distance/maxDistance))
		scoredPool = append(scoredPool, scored{candidate: c, distance: distance, score: pre})
	}
	sort.SliceStable(scoredPool, func(i, j int) bool {
		if scoredPool[i].score != scoredPool[j].score {
			return scoredPool[i].score > scoredPool[j].score
		}
		if scoredPool[i].distance != scoredPool[j].distance {
			return scoredPool[i].distance < scoredPool[j].distance
		}
		return candidateKey(scoredPool[i].candidate) < candidateKey(scoredPool[j].candidate)
	})

	const maxCandidates = 24
	pool := make([]catalog.ComposerCandidate, 0, maxCandidates+len(must))
	seen := map[string]bool{}
	for _, s := range scoredPool {
		key := candidateKey(s.candidate)
		if seen[key] {
			continue
		}
		if len(pool) >= maxCandidates && !must[key] && !must[s.candidate.Name] && !must[candidateSlug(s.candidate)] {
			continue
		}
		seen[key] = true
		pool = append(pool, s.candidate)
	}
	// If curated data exists but the distance filter dropped all of it, include
	// the nearest curated candidate. Curated always outranks unverified.
	curatedGlobal, _ := countProvenance(candidates)
	if curatedPool, _ := countProvenance(pool); curatedGlobal > 0 && curatedPool == 0 {
		op := originPoint(brief, origin, candidates)
		nearest := nearestCurated(candidates, op.Latitude, op.Longitude)
		if nearest != nil && !seen[candidateKey(*nearest)] {
			pool = append(pool, *nearest)
		}
	}
	return pool, warnings
}

func nearestCurated(candidates []catalog.ComposerCandidate, lat, lng float64) *catalog.ComposerCandidate {
	var best *catalog.ComposerCandidate
	bestDist := math.MaxFloat64
	for i := range candidates {
		c := candidates[i]
		if c.Provenance != catalog.ProvenanceCurated || (c.Latitude == 0 && c.Longitude == 0) {
			continue
		}
		d := haversineKm(Point{Latitude: lat, Longitude: lng}, Point{Latitude: c.Latitude, Longitude: c.Longitude})
		if d < bestDist {
			bestDist = d
			best = &candidates[i]
		}
	}
	return best
}

func maxDistanceKm(brief Brief) float64 {
	var km float64
	switch {
	case brief.Days <= 2:
		km = 350
	case brief.Days <= 5:
		km = 700
	case brief.Days <= 9:
		km = 1100
	default:
		km = 1500
	}
	if brief.Transport == "public" {
		km *= 0.8
	}
	return km
}

func (e *Engine) travelMatrix(ctx context.Context, brief Brief, origin catalog.County, pool []catalog.ComposerCandidate) (TravelMatrix, []Warning) {
	points := make([]Point, 0, len(pool)+1)
	op := originPoint(brief, origin, pool)
	points = append(points, op)
	for _, c := range pool {
		points = append(points, Point{Latitude: c.Latitude, Longitude: c.Longitude})
	}
	router := e.Router
	if router == nil {
		router = StraightLineRouter{}
	}
	matrix, err := router.Matrix(ctx, points, brief.Transport)
	if err != nil {
		matrix, _ = StraightLineRouter{}.Matrix(ctx, points, brief.Transport)
	}
	if brief.Transport == "public" {
		for i := range matrix.Minutes {
			for j := range matrix.Minutes[i] {
				if i != j && matrix.Minutes[i][j] > 0 {
					matrix.Minutes[i][j] += 30
				}
			}
		}
	}
	warnings := make([]Warning, 0, 2)
	if matrix.Estimated {
		warnings = append(warnings, Warning{
			Code: "ROUTE_ESTIMATED", Severity: "info",
			Message: "Travel times are straight-line estimates; confirm road conditions.",
			Params:  map[string]any{"provider": matrix.Provider},
		})
	}
	if brief.Transport == "public" {
		warnings = append(warnings, Warning{
			Code: "PUBLIC_TRANSPORT_SCHEDULE_UNVERIFIED", Severity: "caution",
			Message: "Public-transport schedules are not verified; allow extra time.",
		})
	}
	return matrix, warnings
}

func (e *Engine) weatherForBrief(ctx context.Context, brief Brief, origin catalog.County, pool []catalog.ComposerCandidate) (map[string]*DayWeather, *Warning) {
	if brief.StartDate == nil || e.Weather == nil {
		return nil, nil
	}
	start, _ := time.Parse("2006-01-02", *brief.StartDate)
	end := start.AddDate(0, 0, brief.Days-1)
	point := originPoint(brief, origin, pool)
	weather, err := e.Weather.Daily(ctx, point, start, end)
	if err != nil || len(weather) == 0 {
		return nil, &Warning{
			Code: "WEATHER_UNAVAILABLE", Severity: "info",
			Message: "Weather data is unavailable; the plan omits weather rationale.",
		}
	}
	out := make(map[string]*DayWeather, len(weather))
	for i := range weather {
		out[weather[i].Date] = &weather[i]
	}
	return out, nil
}

func (e *Engine) eventsForBrief(ctx context.Context, brief Brief, pool []catalog.ComposerCandidate, origin catalog.County) ([]Event, *Warning) {
	if brief.StartDate == nil || e.Events == nil {
		return nil, nil
	}
	start, _ := time.Parse("2006-01-02", *brief.StartDate)
	end := start.AddDate(0, 0, brief.Days-1)
	counties := []string{origin.Name}
	for _, c := range pool {
		counties = append(counties, c.County)
	}
	events, err := e.Events.Events(ctx, uniqueStrings(counties), start, end)
	if err != nil {
		return nil, &Warning{
			Code: "EVENTS_UNAVAILABLE", Severity: "info",
			Message: "Program events are unavailable; the plan omits event rationale.",
		}
	}
	return events, nil
}

func findCoverage(coverage []catalog.ComposerCountyCoverage, countyCode string) catalog.ComposerCountyCoverage {
	for _, c := range coverage {
		if c.CountyCode == countyCode {
			return c
		}
	}
	return catalog.ComposerCountyCoverage{CountyCode: countyCode}
}

func countProvenance(candidates []catalog.ComposerCandidate) (curated, unverified int) {
	for _, c := range candidates {
		if c.Provenance == catalog.ProvenanceCurated {
			curated++
		} else {
			unverified++
		}
	}
	return curated, unverified
}

func candidateKey(c catalog.ComposerCandidate) string {
	if c.DestinationID != nil {
		return *c.DestinationID
	}
	if c.PlaceID != nil {
		return *c.PlaceID
	}
	return c.Name
}

func hashCandidates(candidates []catalog.ComposerCandidate) string {
	sorted := append([]catalog.ComposerCandidate(nil), candidates...)
	sort.Slice(sorted, func(i, j int) bool { return candidateKey(sorted[i]) < candidateKey(sorted[j]) })
	raw, _ := json.Marshal(sorted)
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:])
}

func hashBrief(brief Brief, originCounty, catalogVersion string) string {
	raw, _ := json.Marshal(struct {
		Brief          Brief  `json:"brief"`
		OriginCounty   string `json:"originCounty"`
		CatalogVersion string `json:"catalogVersion"`
		EngineVersion  string `json:"engineVersion"`
	}{brief, originCounty, catalogVersion, EngineVersion})
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:])
}

func seedFor(inputHash string, profile Profile) string {
	sum := sha256.Sum256([]byte(inputHash + ":" + string(profile)))
	return hex.EncodeToString(sum[:])
}

func uniqueStrings(values []string) []string {
	seen := map[string]bool{}
	out := make([]string, 0, len(values))
	for _, v := range values {
		if v == "" || seen[v] {
			continue
		}
		seen[v] = true
		out = append(out, v)
	}
	return out
}

func coverageFromCatalog(coverage []catalog.ComposerCountyCoverage) []CountyCoverage {
	out := make([]CountyCoverage, 0, len(coverage))
	for _, c := range coverage {
		out = append(out, CountyCoverage{
			CountyCode: c.CountyCode, County: c.County,
			CuratedCount: c.CuratedCount, UnverifiedCount: c.UnverifiedCount,
			TotalCount: c.TotalCount, Status: c.CoverageStatus,
		})
	}
	return out
}

func coverageForPlans(coverage []catalog.ComposerCountyCoverage, plans []Suggestion, originCounty string) []CountyCoverage {
	used := map[string]bool{originCounty: true}
	for _, plan := range plans {
		for _, day := range plan.Days {
			for _, stop := range day.Stops {
				used[stop.CountyCode] = true
			}
		}
	}
	out := make([]CountyCoverage, 0)
	for _, c := range coverage {
		if used[c.CountyCode] {
			out = append(out, CountyCoverage{
				CountyCode: c.CountyCode, County: c.County,
				CuratedCount: c.CuratedCount, UnverifiedCount: c.UnverifiedCount,
				TotalCount: c.TotalCount, Status: c.CoverageStatus,
			})
		}
	}
	return out
}

func allBlocked(plans []Suggestion) bool {
	for _, plan := range plans {
		if plan.Status != "blocked" {
			return false
		}
	}
	return len(plans) > 0
}

func appendWarning(warnings []Warning, w Warning) []Warning {
	for _, existing := range warnings {
		if existing.Code == w.Code {
			return warnings
		}
	}
	return append(warnings, w)
}

func appendWarnings(warnings []Warning, extra ...Warning) []Warning {
	for _, w := range extra {
		warnings = appendWarning(warnings, w)
	}
	return warnings
}

func candidateSlug(c catalog.ComposerCandidate) string {
	if c.Slug != nil {
		return *c.Slug
	}
	return ""
}

func coverageFromCandidates(candidates []catalog.ComposerCandidate) []catalog.ComposerCountyCoverage {
	type counts struct {
		county     string
		curated    int
		unverified int
	}
	byCode := map[string]*counts{}
	for _, c := range candidates {
		entry := byCode[c.CountyCode]
		if entry == nil {
			entry = &counts{county: c.County}
			byCode[c.CountyCode] = entry
		}
		if c.Provenance == catalog.ProvenanceCurated {
			entry.curated++
		} else {
			entry.unverified++
		}
	}
	out := make([]catalog.ComposerCountyCoverage, 0, len(byCode))
	for code, entry := range byCode {
		status := catalog.CoverageLimited
		if entry.curated >= 3 {
			status = catalog.CoverageCovered
		}
		out = append(out, catalog.ComposerCountyCoverage{
			CountyCode: code, County: entry.county,
			CuratedCount: entry.curated, UnverifiedCount: entry.unverified,
			TotalCount: entry.curated + entry.unverified, CoverageStatus: status,
		})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].CountyCode < out[j].CountyCode })
	return out
}

func coverageForCounty(coverage []catalog.ComposerCountyCoverage, code string) []catalog.ComposerCountyCoverage {
	for _, c := range coverage {
		if c.CountyCode == code {
			return []catalog.ComposerCountyCoverage{c}
		}
	}
	return nil
}
