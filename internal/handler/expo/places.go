package expo

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"regexp"
	"strconv"
	"strings"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/bradleygichuru/ytci-go/internal/db/gen"
	"github.com/bradleygichuru/ytci-go/internal/handler"
	"github.com/bradleygichuru/ytci-go/internal/places"
)

const defaultCategory = "tourist_attraction"
const defaultCounty = "Unknown"

var nonSlug = regexp.MustCompile(`[^a-z0-9-]+`)
var multiDash = regexp.MustCompile(`-+`)

type PlacesHandler struct {
	pool      *pgxpool.Pool
	client    *places.Client
	cache     *places.Cache
}

func NewPlacesHandler(pool *pgxpool.Pool, apiKey string) *PlacesHandler {
	return &PlacesHandler{
		pool:   pool,
		client: places.NewClient(apiKey),
		cache:  places.NewCache(pool),
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
		json.NewEncoder(w).Encode(map[string]string{"slug": existing.Slug})
		return
	}

	// Check if error is "no rows found" vs other error
	if !strings.Contains(err.Error(), "no rows") {
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

	dest, err := h.createDraftDestination(ctx, details)
	if err != nil {
		// Check if error is duplicate key violation (SQLSTATE 23505)
		if strings.Contains(err.Error(), "SQLSTATE 23505") || strings.Contains(err.Error(), "duplicate key") {
			// Destination already exists, fetch and return it
			existing, fetchErr := h.queries().GetDestinationByGooglePlaceID(ctx, &req.PlaceID)
			if fetchErr == nil {
				w.Header().Set("Content-Type", "application/json")
				json.NewEncoder(w).Encode(map[string]string{"slug": existing.Slug})
				return
			}
		}
		handler.WriteServerError(w, r, "CREATE_ERROR", "failed to create draft destination", err)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	json.NewEncoder(w).Encode(map[string]string{"slug": dest.Slug})
}

func (h *PlacesHandler) createDraftDestination(ctx context.Context, details *places.PlaceDetails) (gen.Destination, error) {
	slug := slugify(details.DisplayName)
	county := defaultCounty

	if details.FormattedAddress != "" {
		parts := strings.Split(details.FormattedAddress, ",")
		for _, part := range parts {
			part = strings.TrimSpace(part)
			if strings.EqualFold(part, "Kenya") {
				continue
			}
			region := parts[len(parts)-2]
			if region != "" {
				county = strings.TrimSpace(region)
			}
		}
	}

	category := defaultCategory
	if len(details.Types) > 0 {
		for _, t := range details.Types {
			if t == "lodging" || t == "hotel" {
				continue
			}
			category = t
			break
		}
	}

	params := &gen.CreateGooglePlacesDraftParams{
		Name:          details.DisplayName,
		Slug:          slug,
		County:        county,
		Category:      category,
		GooglePlaceID: &details.PlaceID,
	}
	if details.FormattedAddress != "" {
		params.ShortDescription = &details.FormattedAddress
	}
	if details.Location != nil {
		params.StMakepoint = details.Location.Longitude
		params.StMakepoint_2 = details.Location.Latitude
	}

	dest, err := h.queries().CreateGooglePlacesDraft(ctx, params)
	if err != nil {
		if strings.Contains(err.Error(), "duplicate key") && strings.Contains(err.Error(), "slug") {
			params.Slug = params.Slug + fmt.Sprintf("-%d", dest.CreatedAt.Time.Unix()%10000)
			return h.queries().CreateGooglePlacesDraft(ctx, params)
		}
		return dest, err
	}
	return dest, nil
}

type popularDestination struct {
	ID                 pgtype.UUID      `json:"id"`
	Name               string           `json:"name"`
	Slug               string           `json:"slug"`
	County             string           `json:"county"`
	Locality           *string          `json:"locality,omitempty"`
	Category           string           `json:"category"`
	ShortDescription   *string          `json:"shortDescription,omitempty"`
	Media              json.RawMessage  `json:"media"`
	SaveCount          int64            `json:"-"`
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
	WHERE ma.entity_type = 'destination' AND ma.entity_id = d.id::text
) med ON true
WHERE d.google_place_id IS NOT NULL AND d.status IN ('published', 'draft')
ORDER BY save_count DESC, d.updated_at DESC
LIMIT $1`

const popularNearbyQuery = `SELECT d.id, d.name, d.slug, d.county, d.locality, d.category,
	d.short_description, 0 AS save_count,
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
	WHERE ma.entity_type = 'destination' AND ma.entity_id = d.id::text
) med ON true
WHERE d.google_place_id IS NOT NULL AND d.status IN ('published', 'draft')
ORDER BY d.updated_at DESC
LIMIT 10`

func (h *PlacesHandler) Popular(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	// Get user's location from query params (default to Kenya center)
	latStr := r.URL.Query().Get("lat")
	lngStr := r.URL.Query().Get("lng")
	lat := 0.0236
	lng := 36.8888
	if latStr != "" && lngStr != "" {
		if parsedLat, err := strconv.ParseFloat(latStr, 64); err == nil {
			lat = parsedLat
		}
		if parsedLng, err := strconv.ParseFloat(lngStr, 64); err == nil {
			lng = parsedLng
		}
	}

	// Step 1: Try to get popular destinations from database (with google_place_id)
	rows, err := h.pool.Query(ctx, popularQuery, 10)
	if err != nil {
		handler.WriteServerError(w, r, "DB_ERROR", "failed to fetch popular destinations", err)
		return
	}
	defer rows.Close()

	var populars []popularDestination
	for rows.Next() {
		var d popularDestination
		var mediaJSON []byte
		if err := rows.Scan(&d.ID, &d.Name, &d.Slug, &d.County, &d.Locality, &d.Category,
			&d.ShortDescription, &d.SaveCount, &mediaJSON); err != nil {
			continue
		}
		if mediaJSON != nil {
			d.Media = json.RawMessage(mediaJSON)
		} else {
			d.Media = json.RawMessage(`[]`)
		}
		populars = append(populars, d)
	}

	// Step 2: If we have enough from DB, return them
	if len(populars) >= 3 {
		h.writePopularResponse(w, populars)
		return
	}

	// Step 3: Not enough in DB, use Google Nearby Search
	cacheKey := fmt.Sprintf("popular:%.2f:%.2f", lat, lng)
	nearbyPlaces, found, err := h.cache.GetNearbyResults(ctx)
	if err != nil {
		slog.Warn("places: nearby cache read failed", "error", err)
	}
	if !found {
		// Use user's location for Nearby Search
		_ = cacheKey // Will be used for location-specific caching in future
		nearbyPlaces, err = h.client.NearbySearch(ctx)
		if err != nil {
			slog.Warn("places: nearby search failed, returning curated only", "error", err)
			h.writePopularResponse(w, populars)
			return
		}
		if err := h.cache.SetNearbyResults(ctx, nearbyPlaces); err != nil {
			slog.Warn("places: nearby cache write failed", "error", err)
		}
	}

	// Step 4: Match Nearby results against destinations in DB
	placeIDs := make([]string, 0, len(nearbyPlaces))
	for _, p := range nearbyPlaces {
		placeIDs = append(placeIDs, p.PlaceID)
	}

	if len(placeIDs) > 0 {
		nearbyRows, err := h.pool.Query(ctx, popularNearbyQuery)
		if err != nil {
			slog.Warn("places: nearby destination lookup failed", "error", err)
		} else {
			defer nearbyRows.Close()
			seen := make(map[string]bool)
			for _, d := range populars {
				if d.ID.Valid {
					seen[fmt.Sprintf("%v", d.ID)] = true
				}
			}
			for nearbyRows.Next() {
				var d popularDestination
				var mediaJSON []byte
				if err := nearbyRows.Scan(&d.ID, &d.Name, &d.Slug, &d.County, &d.Locality, &d.Category,
					&d.ShortDescription, &d.SaveCount, &mediaJSON); err != nil {
					continue
				}
				if !d.ID.Valid || seen[fmt.Sprintf("%v", d.ID)] {
					continue
				}
				if mediaJSON != nil {
					d.Media = json.RawMessage(mediaJSON)
				} else {
					d.Media = json.RawMessage(`[]`)
				}
				populars = append(populars, d)
				seen[fmt.Sprintf("%v", d.ID)] = true
			}
			if len(populars) > 10 {
				populars = populars[:10]
			}
		}
	}

	h.writePopularResponse(w, populars)
}

func (h *PlacesHandler) writePopularResponse(w http.ResponseWriter, destinations []popularDestination) {
	type popularItem struct {
		ID               pgtype.UUID      `json:"id"`
		Name             string           `json:"name"`
		Slug             string           `json:"slug"`
		County           string           `json:"county"`
		Locality         *string          `json:"locality,omitempty"`
		Category         string           `json:"category"`
		ShortDescription *string          `json:"shortDescription,omitempty"`
		Media            json.RawMessage  `json:"media"`
	}

	items := make([]popularItem, 0, len(destinations))
	for _, d := range destinations {
		items = append(items, popularItem{
			ID:               d.ID,
			Name:             d.Name,
			Slug:             d.Slug,
			County:           d.County,
			Locality:         d.Locality,
			Category:         d.Category,
			ShortDescription: d.ShortDescription,
			Media:            d.Media,
		})
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{"destinations": items})
}
