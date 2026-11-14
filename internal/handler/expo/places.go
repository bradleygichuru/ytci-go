package expo

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/bradleygichuru/ytci-go/internal/catalog"
	"github.com/bradleygichuru/ytci-go/internal/db/gen"
	"github.com/bradleygichuru/ytci-go/internal/handler"
	"github.com/bradleygichuru/ytci-go/internal/places"
)

var nonSlug = regexp.MustCompile(`[^a-z0-9-]+`)
var multiDash = regexp.MustCompile(`-+`)

type PlacesHandler struct {
	pool   *pgxpool.Pool
	client *places.Client
	cache  *places.Cache
}

func NewPlacesHandler(pool *pgxpool.Pool, apiKey string, cache *places.Cache) *PlacesHandler {
	return &PlacesHandler{
		pool:   pool,
		client: places.NewClient(apiKey),
		cache:  cache,
	}
}

func (h *PlacesHandler) queries() *gen.Queries {
	return gen.New(h.pool)
}

func slugify(name string) string {
	s := strings.ToLower(name)
	s = nonSlug.ReplaceAllString(s, "-")
	s = multiDash.ReplaceAllString(s, "-")
	s = strings.Trim(s, "-")
	if len(s) > 100 {
		s = s[:100]
	}
	return s
}

func generateSessionToken() (string, error) {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}

func (h *PlacesHandler) CreateSession(w http.ResponseWriter, r *http.Request) {
	token, err := generateSessionToken()
	if err != nil {
		handler.WriteServerError(w, r, "SESSION_ERROR", "failed to generate session token", err)
		return
	}

	ctx := r.Context()
	if err := h.cache.CreateSessionToken(ctx, token); err != nil {
		handler.WriteServerError(w, r, "SESSION_ERROR", "failed to store session token", err)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{"session_token": token})
}

func (h *PlacesHandler) Autocomplete(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query().Get("q")
	sessionToken := r.URL.Query().Get("session_token")

	if q == "" || sessionToken == "" {
		handler.WriteError(w, http.StatusBadRequest, "INVALID_REQUEST", "q and session_token are required")
		return
	}

	ctx := r.Context()

	valid, err := h.cache.SessionTokenValid(ctx, sessionToken)
	if err != nil {
		handler.WriteServerError(w, r, "SESSION_ERROR", "failed to validate session token", err)
		return
	}
	if !valid {
		handler.WriteError(w, http.StatusUnauthorized, "INVALID_SESSION", "session token not found or expired")
		return
	}

	if results, found, err := h.cache.GetAutocompleteResults(ctx, q); err != nil {
		slog.Warn("places: autocomplete cache read failed", "error", err)
	} else if found {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]interface{}{"suggestions": results})
		return
	}

	results, err := h.client.Autocomplete(ctx, q, sessionToken)
	if err != nil {
		handler.WriteServerError(w, r, "PLACES_ERROR", "failed to fetch autocomplete suggestions", err)
		return
	}

	if err := h.cache.SetAutocompleteResults(ctx, q, results); err != nil {
		slog.Warn("places: autocomplete cache write failed", "error", err)
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{"suggestions": results})
}

func (h *PlacesHandler) Search(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query().Get("q")
	if q == "" {
		handler.WriteError(w, http.StatusBadRequest, "INVALID_REQUEST", "q is required")
		return
	}

	ctx := r.Context()

	if results, found, err := h.cache.GetTextSearchResults(ctx, q); err != nil {
		slog.Warn("places: text search cache read failed", "error", err)
	} else if found {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]interface{}{"results": results})
		return
	}

	results, err := h.client.TextSearch(ctx, q)
	if err != nil {
		handler.WriteServerError(w, r, "PLACES_ERROR", "failed to search places", err)
		return
	}

	if err := h.cache.SetTextSearchResults(ctx, q, results); err != nil {
		slog.Warn("places: text search cache write failed", "error", err)
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{"results": results})
}

func (h *PlacesHandler) GetLocation(w http.ResponseWriter, r *http.Request) {
	placeID := r.URL.Query().Get("place_id")
	if placeID == "" {
		handler.WriteError(w, http.StatusBadRequest, "INVALID_REQUEST", "place_id is required")
		return
	}
	ctx := r.Context()

	// Check cache first
	cached, found, err := h.cache.GetPlace(ctx, placeID)
	if err == nil && found && cached.Lat != nil && cached.Lng != nil {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]interface{}{
			"latitude":  *cached.Lat,
			"longitude": *cached.Lng,
		})
		return
	}

	// Call Place Details API (no session token needed for location lookup)
	details, err := h.client.PlaceDetails(ctx, placeID, "")
	if err != nil {
		handler.WriteServerError(w, r, "PLACES_ERROR", "failed to fetch place details", err)
		return
	}

	// Cache the result
	if err := h.cache.SetPlace(ctx, details); err != nil {
		slog.Warn("places: place details cache write failed", "error", err)
	}

	// Return location
	if details.Location != nil {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]interface{}{
			"latitude":  details.Location.Latitude,
			"longitude": details.Location.Longitude,
		})
	} else {
		handler.WriteError(w, http.StatusNotFound, "NO_LOCATION", "location not available for this place")
	}
}

func (h *PlacesHandler) GetPlaceDetails(w http.ResponseWriter, r *http.Request) {
	placeID := r.URL.Query().Get("place_id")
	if placeID == "" {
		handler.WriteError(w, http.StatusBadRequest, "INVALID_REQUEST", "place_id is required")
		return
	}
	ctx := r.Context()

	// Check cache first (enforce 30-day freshness per Google ToS)
	cached, found, err := h.cache.GetPlaceFresh(ctx, placeID)
	if err == nil && found && cached.Data != nil {
		w.Header().Set("Content-Type", "application/json")
		w.Write(cached.Data)
		return
	}

	// Call Place Details API
	details, err := h.client.PlaceDetails(ctx, placeID, "")
	if err != nil {
		handler.WriteServerError(w, r, "PLACES_ERROR", "failed to fetch place details", err)
		return
	}

	// Cache the result
	if err := h.cache.SetPlace(ctx, details); err != nil {
		slog.Warn("places: place details cache write failed", "error", err)
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(details)
}

func (h *PlacesHandler) Resolve(w http.ResponseWriter, r *http.Request) {
	var req struct {
		PlaceID      string `json:"place_id"`
		SessionToken string `json:"session_token"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		handler.WriteError(w, http.StatusBadRequest, "INVALID_REQUEST", "invalid request body")
		return
	}
	if req.PlaceID == "" || req.SessionToken == "" {
		handler.WriteError(w, http.StatusBadRequest, "INVALID_REQUEST", "place_id and session_token are required")
		return
	}

	ctx := r.Context()

	valid, err := h.cache.SessionTokenValid(ctx, req.SessionToken)
	if err != nil {
		handler.WriteServerError(w, r, "SESSION_ERROR", "failed to validate session token", err)
		return
	}
	if !valid {
		handler.WriteError(w, http.StatusUnauthorized, "INVALID_SESSION", "session token not found or expired")
		return
	}

	existing, err := h.queries().GetDestinationByGooglePlaceID(ctx, &req.PlaceID)
	if err == nil {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]interface{}{"slug": existing.Slug, "id": existing.ID})
		return
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		handler.WriteServerError(w, r, "DB_ERROR", "failed to check existing destination", err)
		return
	}

	details, err := h.client.PlaceDetails(ctx, req.PlaceID, req.SessionToken)
	if err != nil {
		handler.WriteServerError(w, r, "PLACES_ERROR", "failed to fetch place details", err)
		return
	}

	if err := h.cache.SetPlace(ctx, details); err != nil {
		slog.Warn("places: place details cache write failed", "error", err)
	}

	go h.cache.EnsureHeroImage(context.Background(), req.PlaceID, details.DisplayName)

	dest, err := h.createDraftDestination(ctx, details)
	if err != nil {
		switch {
		case errors.Is(err, catalog.ErrNotADestination):
			handler.WriteError(w, http.StatusUnprocessableEntity, "NOT_A_DESTINATION",
				"This place cannot be added as a destination.")
		case errors.Is(err, catalog.ErrCountyNotFound):
			handler.WriteError(w, http.StatusUnprocessableEntity, "COUNTY_NOT_FOUND",
				"Could not resolve a Kenyan county for this place.")
		default:
			handler.WriteServerError(w, r, "CREATE_ERROR", "failed to create draft destination", err)
		}
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	json.NewEncoder(w).Encode(map[string]interface{}{"slug": dest.Slug, "id": dest.ID})
}

type draftDestination struct {
	ID   pgtype.UUID
	Slug string
}

func (h *PlacesHandler) createDraftDestination(ctx context.Context, details *places.PlaceDetails) (draftDestination, error) {
	if details == nil || strings.TrimSpace(details.DisplayName) == "" {
		return draftDestination{}, fmt.Errorf("catalog: place has no name")
	}

	classification := catalog.ClassifyGoogleTypes(details.Types)
	if !classification.IsDestination {
		return draftDestination{}, fmt.Errorf("%w: %s", catalog.ErrNotADestination, classification.Reason)
	}

	var lat, lng *float64
	if details.Location != nil {
		lat = &details.Location.Latitude
		lng = &details.Location.Longitude
	}
	county, err := catalog.ResolveCounty(ctx, h.pool, lat, lng, countyFromAddress(details.FormattedAddress))
	if err != nil {
		return draftDestination{}, err
	}

	// Dedupe on Google's place id first, then on a normalized name inside the
	// same county. Returning the existing row keeps the user's save idempotent.
	existing, err := h.queries().GetDestinationByGooglePlaceID(ctx, &details.PlaceID)
	if err == nil {
		return draftDestination{ID: existing.ID, Slug: existing.Slug}, nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return draftDestination{}, fmt.Errorf("catalog: lookup place: %w", err)
	}

	var existingID pgtype.UUID
	var existingSlug string
	err = h.pool.QueryRow(ctx, `
		SELECT id, slug FROM destinations
		WHERE county_code = $1
		  AND regexp_replace(lower(name), '[^a-z0-9]', '', 'g') =
		      regexp_replace(lower($2), '[^a-z0-9]', '', 'g')
		ORDER BY created_at
		LIMIT 1
	`, county.Code, details.DisplayName).Scan(&existingID, &existingSlug)
	if err == nil {
		return draftDestination{ID: existingID, Slug: existingSlug}, nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return draftDestination{}, fmt.Errorf("catalog: lookup duplicate name: %w", err)
	}

	baseSlug := slugify(details.DisplayName)
	if baseSlug == "" {
		baseSlug = "destination"
	}
	var shortDescription *string
	if details.FormattedAddress != "" {
		shortDescription = &details.FormattedAddress
	}

	for attempt := 0; attempt < 6; attempt++ {
		slug := baseSlug
		if attempt > 0 {
			slug = fmt.Sprintf("%s-%d", baseSlug, attempt+1)
		}

		var id pgtype.UUID
		err = h.pool.QueryRow(ctx, `
			INSERT INTO destinations (
				name, slug, county, county_code, locality, category, interest_tags,
				status, source, google_place_id, location, short_description,
				verification_status
			) VALUES (
				$1, $2, $3, $4, NULL, $5, $6,
				'draft', 'google_places', $7,
				CASE WHEN $8::float8 IS NULL OR $9::float8 IS NULL THEN NULL
				     ELSE ST_SetSRID(ST_MakePoint($8, $9), 4326) END,
				$10, 'unverified'
			)
			ON CONFLICT DO NOTHING
			RETURNING id
		`, details.DisplayName, slug, county.Name, county.Code,
			string(classification.Category), classification.Interests,
			details.PlaceID, lng, lat, shortDescription).Scan(&id)
		if err == nil {
			return draftDestination{ID: id, Slug: slug}, nil
		}
		if !errors.Is(err, pgx.ErrNoRows) {
			return draftDestination{}, fmt.Errorf("catalog: create draft: %w", err)
		}

		// A conflict can be the slug or a concurrent insert of the same place.
		concurrent, lookupErr := h.queries().GetDestinationByGooglePlaceID(ctx, &details.PlaceID)
		if lookupErr == nil {
			return draftDestination{ID: concurrent.ID, Slug: concurrent.Slug}, nil
		}
		if !errors.Is(lookupErr, pgx.ErrNoRows) {
			return draftDestination{}, fmt.Errorf("catalog: lookup conflicting place: %w", lookupErr)
		}
	}

	return draftDestination{}, fmt.Errorf("catalog: could not allocate a unique slug for %q", details.DisplayName)
}

// countyFromAddress takes the most specific county-like component from a
// Google formatted address. Polygon resolution normally wins; this text is
// only the fallback.
func countyFromAddress(address string) string {
	if strings.TrimSpace(address) == "" {
		return ""
	}
	parts := strings.Split(address, ",")
	for i := len(parts) - 1; i >= 0; i-- {
		part := strings.TrimSpace(parts[i])
		if part == "" || strings.EqualFold(part, "Kenya") {
			continue
		}
		return part
	}
	return ""
}

type popularDestination struct {
	ID                   pgtype.UUID     `json:"id"`
	Name                 string          `json:"name"`
	Slug                 string          `json:"slug"`
	County               string          `json:"county"`
	Locality             *string         `json:"locality,omitempty"`
	Category             string          `json:"category"`
	ShortDescription     *string         `json:"shortDescription,omitempty"`
	Media                json.RawMessage `json:"media"`
	SaveCount            int64           `json:"-"`
	HeroImageUrl         *string         `json:"heroImageUrl,omitempty"`
	HeroImageSource      *string         `json:"heroImageSource,omitempty"`
	HeroImageAttribution *string         `json:"heroImageAttribution,omitempty"`
}

type mobilePopularMedia struct {
	ObjectKey    string `json:"objectKey"`
	ThumbnailKey string `json:"thumbnailKey,omitempty"`
	Type         string `json:"type,omitempty"`
	AltText      string `json:"altText,omitempty"`
	URL          string `json:"url,omitempty"`
	ThumbnailURL string `json:"thumbnailUrl,omitempty"`
}

const popularQuery = `SELECT d.id, d.name, d.slug, d.county, d.locality, d.category,
	d.short_description,
	(SELECT COUNT(*) FROM bucket_list_items bli WHERE bli.destination_id = d.id) AS save_count,
	COALESCE(med.media, '[]'::json) AS media
FROM destinations d
LEFT JOIN LATERAL (
	SELECT COALESCE(json_agg(json_build_object(
		'objectKey', ma.object_key,
		'thumbnailKey', ma.thumbnail_key,
		'type', ma.type,
		'altText', ma.alt_text
	) ORDER BY ma.display_order) FILTER (WHERE ma.id IS NOT NULL), '[]') AS media
	FROM media_assets ma
	WHERE ma.entity_type = 'destination' AND ma.entity_id::text = d.id::text
) med ON true
WHERE d.google_place_id IS NOT NULL AND d.status IN ('published', 'draft')
ORDER BY save_count DESC, d.updated_at DESC
LIMIT $1`

const popularNearbyQuery = `SELECT d.id, d.name, d.slug, d.county, d.locality, d.category,
	d.short_description, 0 AS save_count,
	COALESCE(med.media, '[]'::json) AS media,
	gpc.hero_image_url, gpc.hero_image_source, gpc.hero_image_attribution
FROM destinations d
LEFT JOIN LATERAL (
	SELECT COALESCE(json_agg(json_build_object(
		'objectKey', ma.object_key,
		'thumbnailKey', ma.thumbnail_key,
		'type', ma.type,
		'altText', ma.alt_text
	) ORDER BY ma.display_order) FILTER (WHERE ma.id IS NOT NULL), '[]') AS media
	FROM media_assets ma
	WHERE ma.entity_type = 'destination' AND ma.entity_id::text = d.id::text
) med ON true
LEFT JOIN google_places_cache gpc ON gpc.place_id = d.google_place_id
WHERE d.google_place_id = ANY($1::text[]) AND d.status IN ('published', 'draft')
ORDER BY array_position($1::text[], d.google_place_id)
LIMIT 10`

const popularFallbackQuery = `SELECT d.id, d.name, d.slug, d.county, d.locality, d.category,
	d.short_description, 0 AS save_count,
	COALESCE(med.media, '[]'::json) AS media,
	gpc.hero_image_url, gpc.hero_image_source, gpc.hero_image_attribution
FROM destinations d
LEFT JOIN LATERAL (
	SELECT COALESCE(json_agg(json_build_object(
		'objectKey', ma.object_key,
		'thumbnailKey', ma.thumbnail_key,
		'type', ma.type,
		'altText', ma.alt_text
	) ORDER BY ma.display_order) FILTER (WHERE ma.id IS NOT NULL), '[]') AS media
	FROM media_assets ma
	WHERE ma.entity_type = 'destination' AND ma.entity_id::text = d.id::text
) med ON true
LEFT JOIN google_places_cache gpc ON gpc.place_id = d.google_place_id
WHERE d.google_place_id IS NOT NULL
  AND d.status IN ('published', 'draft')
  AND ST_DWithin(d.location::geography, ST_SetSRID(ST_MakePoint($1, $2), 4326)::geography, $3)
ORDER BY d.updated_at DESC
LIMIT 10`

func (h *PlacesHandler) Popular(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	// Parse user's location (default to Nairobi)
	lat := -1.29
	lng := 36.82
	if latStr := r.URL.Query().Get("lat"); latStr != "" {
		if p, err := strconv.ParseFloat(latStr, 64); err == nil {
			lat = p
		}
	}
	if lngStr := r.URL.Query().Get("lng"); lngStr != "" {
		if p, err := strconv.ParseFloat(lngStr, 64); err == nil {
			lng = p
		}
	}

	// Step 1: Check cache (within 24h TTL)
	nearbyPlaces, found, err := h.cache.GetNearbyResults(ctx, lat, lng)
	if err != nil {
		slog.Warn("places: nearby cache read failed", "error", err)
	}

	// Step 2: If cache miss, check stale cache (up to 7 days)
	if !found {
		nearbyPlaces, found, err = h.cache.GetNearbyResultsStale(ctx, lat, lng)
		if err != nil {
			slog.Warn("places: stale cache read failed", "error", err)
		}
		if found {
			go func() {
				freshResults, fErr := h.client.NearbySearch(context.Background(), lat, lng)
				if fErr != nil {
					slog.Warn("places: background revalidation failed", "error", fErr)
					return
				}
				if fErr := h.cache.SetNearbyResults(context.Background(), lat, lng, freshResults); fErr != nil {
					slog.Warn("places: background cache write failed", "error", fErr)
				}
			}()
		}
	}

	// Step 3: If still no results, call Google API with 15-second timeout
	if !found {
		apiCtx, apiCancel := context.WithTimeout(ctx, 15*time.Second)
		nearbyPlaces, err = h.client.NearbySearch(apiCtx, lat, lng)
		apiCancel()

		if err != nil {
			if errors.Is(err, context.DeadlineExceeded) {
				slog.Warn("places: nearby search timed out after 15s")
			} else {
				slog.Warn("places: nearby search failed", "error", err)
			}

			// Try stale cache first, then fallback
			stalePlaces, staleFound, _ := h.cache.GetNearbyResultsStale(ctx, lat, lng)
			if staleFound {
				populars := h.matchNearbyPlaces(ctx, stalePlaces)
				if len(populars) > 0 {
					h.writePopularResponse(w, populars)
					return
				}
			}

			h.fallbackPopular(ctx, w, lat, lng)
			return
		}
		if err := h.cache.SetNearbyResults(ctx, lat, lng, nearbyPlaces); err != nil {
			slog.Warn("places: nearby cache write failed", "error", err)
		}
	}

	// Step 4: Match Nearby results against destinations
	populars := h.matchNearbyPlaces(ctx, nearbyPlaces)

	if len(populars) == 0 {
		h.fallbackPopular(ctx, w, lat, lng)
		return
	}

	h.fireHeroFetches(ctx, populars)
	h.writePopularResponse(w, populars)
}

func (h *PlacesHandler) fireHeroFetches(ctx context.Context, populars []popularDestination) {
	fetched := 0
	for _, d := range populars {
		if fetched >= 2 {
			break
		}
		if d.HeroImageUrl != nil && *d.HeroImageUrl != "" {
			continue
		}
		if d.HeroImageSource != nil && *d.HeroImageSource == "wikipedia_not_found" {
			continue
		}
		var gpid string
		_ = h.pool.QueryRow(ctx,
			`SELECT google_place_id FROM destinations WHERE id = $1`, d.ID).Scan(&gpid)
		if gpid == "" {
			continue
		}
		fetched++
		placeID := gpid
		placeName := d.Name
		go h.cache.EnsureHeroImage(context.Background(), placeID, placeName)
	}
}

func (h *PlacesHandler) matchNearbyPlaces(ctx context.Context, nearbyPlaces []places.NearbyPlace) []popularDestination {
	populars := []popularDestination{}

	// Post-filter with the controlled taxonomy. The popular path never
	// creates catalog rows; it only surfaces destinations that already exist.
	placeIDs := make([]string, 0, len(nearbyPlaces))
	for _, p := range nearbyPlaces {
		if catalog.ClassifyGoogleTypes(p.Types).IsDestination {
			placeIDs = append(placeIDs, p.PlaceID)
		}
	}

	if len(placeIDs) == 0 {
		return populars
	}

	nearbyRows, err := h.pool.Query(ctx, popularNearbyQuery, placeIDs)
	if err != nil {
		slog.Warn("places: nearby destination lookup failed", "error", err)
		return populars
	}
	defer nearbyRows.Close()

	for nearbyRows.Next() {
		var d popularDestination
		var mediaJSON []byte
		if err := nearbyRows.Scan(&d.ID, &d.Name, &d.Slug, &d.County, &d.Locality, &d.Category,
			&d.ShortDescription, &d.SaveCount, &mediaJSON,
			&d.HeroImageUrl, &d.HeroImageSource, &d.HeroImageAttribution); err != nil {
			continue
		}
		if !d.ID.Valid {
			continue
		}
		if mediaJSON != nil {
			d.Media = json.RawMessage(mediaJSON)
		} else {
			d.Media = json.RawMessage(`[]`)
		}
		populars = append(populars, d)
	}

	if len(populars) > 10 {
		populars = populars[:10]
	}

	return populars
}

func (h *PlacesHandler) fallbackPopular(ctx context.Context, w http.ResponseWriter, lat, lng float64) {
	// Progressive radius: 20km → 100km in 10km steps
	for radius := 20000; radius <= 100000; radius += 10000 {
		rows, err := h.pool.Query(ctx, popularFallbackQuery, lng, lat, radius)
		if err != nil {
			slog.Warn("places: fallback query failed", "radius", radius, "error", err)
			continue
		}

		populars := []popularDestination{}
		for rows.Next() {
			var d popularDestination
			var mediaJSON []byte
			if err := rows.Scan(&d.ID, &d.Name, &d.Slug, &d.County, &d.Locality, &d.Category,
				&d.ShortDescription, &d.SaveCount, &mediaJSON,
				&d.HeroImageUrl, &d.HeroImageSource, &d.HeroImageAttribution); err != nil {
				continue
			}
			if !d.ID.Valid {
				continue
			}
			if mediaJSON != nil {
				d.Media = json.RawMessage(mediaJSON)
			} else {
				d.Media = json.RawMessage(`[]`)
			}
			populars = append(populars, d)
		}
		rows.Close()

		if len(populars) > 0 {
			h.writePopularResponse(w, populars)
			return
		}
	}

	// No results within 100km — return empty
	h.writePopularResponse(w, []popularDestination{})
}

func (h *PlacesHandler) writePopularResponse(w http.ResponseWriter, destinations []popularDestination) {
	type popularItem struct {
		ID                   pgtype.UUID     `json:"id"`
		Name                 string          `json:"name"`
		Slug                 string          `json:"slug"`
		County               string          `json:"county"`
		Locality             *string         `json:"locality,omitempty"`
		Category             string          `json:"category"`
		ShortDescription     *string         `json:"shortDescription,omitempty"`
		Media                json.RawMessage `json:"media"`
		HeroImageUrl         *string         `json:"heroImageUrl,omitempty"`
		HeroImageSource      *string         `json:"heroImageSource,omitempty"`
		HeroImageAttribution *string         `json:"heroImageAttribution,omitempty"`
	}

	items := make([]popularItem, 0, len(destinations))
	for _, d := range destinations {
		items = append(items, popularItem{
			ID:                   d.ID,
			Name:                 d.Name,
			Slug:                 d.Slug,
			County:               d.County,
			Locality:             d.Locality,
			Category:             d.Category,
			ShortDescription:     d.ShortDescription,
			Media:                d.Media,
			HeroImageUrl:         d.HeroImageUrl,
			HeroImageSource:      d.HeroImageSource,
			HeroImageAttribution: d.HeroImageAttribution,
		})
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{"destinations": items})
}
