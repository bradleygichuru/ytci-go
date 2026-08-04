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

func (h *PlacesHandler) Popular(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	populars, err := h.queries().GetPopularDestinations(ctx, 10)
	if err != nil {
		handler.WriteServerError(w, r, "DB_ERROR", "failed to fetch popular destinations", err)
		return
	}

	if len(populars) >= 3 {
		h.writePopularResponse(w, populars)
		return
	}

	nearbyPlaces, found, err := h.cache.GetNearbyResults(ctx)
	if err != nil {
		slog.Warn("places: nearby cache read failed", "error", err)
	}
	if !found {
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

	placeIDs := make([]string, 0, len(nearbyPlaces))
	for _, p := range nearbyPlaces {
		placeIDs = append(placeIDs, p.PlaceID)
	}

	if len(placeIDs) > 0 {
		rows, err := h.pool.Query(ctx,
			`SELECT d.*, 0 AS save_count FROM destinations d WHERE d.google_place_id IS NOT NULL AND d.status = 'published' ORDER BY d.updated_at DESC LIMIT 10`)
		if err != nil {
			slog.Warn("places: nearby destination lookup failed", "error", err)
		} else {
			defer rows.Close()
			var nearbyDestinations []gen.GetPopularDestinationsRow
			for rows.Next() {
				var d gen.GetPopularDestinationsRow
				if err := h.scanPopularRow(&d, rows); err != nil {
					continue
				}
				nearbyDestinations = append(nearbyDestinations, d)
			}
			seen := make(map[string]bool)
			for _, d := range populars {
				if d.ID.Valid {
					seen[fmt.Sprintf("%v", d.ID)] = true
				}
			}
			for _, d := range nearbyDestinations {
				if d.ID.Valid && !seen[fmt.Sprintf("%v", d.ID)] {
					populars = append(populars, d)
					seen[fmt.Sprintf("%v", d.ID)] = true
				}
			}
			if len(populars) > 10 {
				populars = populars[:10]
			}
		}
	}

	h.writePopularResponse(w, populars)
}

func (h *PlacesHandler) scanPopularRow(d *gen.GetPopularDestinationsRow, rows interface{ Scan(...interface{}) error }) error {
	return rows.Scan(
		&d.ID,
		&d.Name,
		&d.Slug,
		&d.County,
		&d.Locality,
		&d.Category,
		&d.Status,
		&d.Location,
		&d.MapLabel,
		&d.AccessRoute,
		&d.DistanceReference,
		&d.ShortDescription,
		&d.FullDescription,
		&d.Significance,
		&d.History,
		&d.ThingsToDo,
		&d.SuitableAudiences,
		&d.Duration,
		&d.Difficulty,
		&d.Seasonality,
		&d.IndicativeFees,
		&d.OpeningInfo,
		&d.TransportNotes,
		&d.Accessibility,
		&d.Facilities,
		&d.SafetyNotes,
		&d.Source,
		&d.ContentOwner,
		&d.VerificationStatus,
		&d.LastUpdated,
		&d.ReviewDate,
		&d.CreatedBy,
		&d.CreatedAt,
		&d.UpdatedAt,
		&d.GooglePlaceID,
		&d.SaveCount,
	)
}

func (h *PlacesHandler) writePopularResponse(w http.ResponseWriter, destinations []gen.GetPopularDestinationsRow) {
	type popularItem struct {
		ID              pgtype.UUID      `json:"id"`
		Name            string           `json:"name"`
		Slug            string           `json:"slug"`
		County          string           `json:"county"`
		Locality        *string          `json:"locality"`
		Category        string           `json:"category"`
		ShortDescription *string         `json:"short_description"`
	}

	items := make([]popularItem, 0, len(destinations))
	for _, d := range destinations {
		items = append(items, popularItem{
			ID:              d.ID,
			Name:            d.Name,
			Slug:            d.Slug,
			County:          d.County,
			Locality:        d.Locality,
			Category:        d.Category,
			ShortDescription: d.ShortDescription,
		})
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{"destinations": items})
}
