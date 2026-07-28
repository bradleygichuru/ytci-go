package admin

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"strconv"
	"time"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/bradleygichuru/ytci-go/internal/db/gen"
	"github.com/bradleygichuru/ytci-go/internal/handler"
	"github.com/bradleygichuru/ytci-go/internal/pagination"
)

type DestinationsHandler struct {
	pool *pgxpool.Pool
}

func NewDestinationsHandler(pool *pgxpool.Pool) *DestinationsHandler {
	return &DestinationsHandler{pool: pool}
}

func (h *DestinationsHandler) List(w http.ResponseWriter, r *http.Request) {
	queries := gen.New(h.pool)
	pg := &pagination.CursorPaginator[gen.Destination]{}

	pg.WritePage(w, r,
		func(limit int32) ([]gen.Destination, error) {
			return queries.ListDestinations(r.Context(), limit)
		},
		func(limit int32, sortValue, id string) ([]gen.Destination, error) {
			var ts pgtype.Timestamp
			var uid pgtype.UUID
			ts.Scan(sortValue)
			uid.Scan(id)
			return queries.ListDestinationsAfter(r.Context(), &gen.ListDestinationsAfterParams{
				CreatedAt: ts,
				ID:        uid,
				Limit:     limit,
			})
		},
		func(d gen.Destination) (string, bool) {
			ts := d.CreatedAt.Time.UTC().Format(time.RFC3339Nano)
			return pagination.EncodeCursor(ts, pagination.UUIDString(d.ID.Bytes)), true
		},
	)
}

func (h *DestinationsHandler) Get(w http.ResponseWriter, r *http.Request) {
	slug := r.PathValue("slug")
	if slug == "" {
		handler.WriteError(w, http.StatusBadRequest, "INVALID_SLUG", "destination slug is required")
		return
	}

	queries := gen.New(h.pool)
	dest, err := queries.GetDestinationBySlug(r.Context(), slug)
	if err != nil {
		handler.WriteError(w, http.StatusNotFound, "NOT_FOUND", "destination not found")
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(dest)
}

func (h *DestinationsHandler) Create(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Name               string   `json:"name"`
		Slug               string   `json:"slug"`
		County             string   `json:"county"`
		Locality           string   `json:"locality,omitempty"`
		Category           string   `json:"category"`
		Lat                *float64 `json:"lat,omitempty"`
		Lng                *float64 `json:"lng,omitempty"`
		MapLabel           string   `json:"mapLabel,omitempty"`
		AccessRoute        string   `json:"accessRoute,omitempty"`
		DistanceReference  string   `json:"distanceReference,omitempty"`
		ShortDescription   string   `json:"shortDescription,omitempty"`
		FullDescription    string   `json:"fullDescription,omitempty"`
		Significance       string   `json:"significance,omitempty"`
		History            string   `json:"history,omitempty"`
		ThingsToDo         string   `json:"thingsToDo,omitempty"`
		SuitableAudiences  string   `json:"suitableAudiences,omitempty"`
		Duration           string   `json:"duration,omitempty"`
		Difficulty         string   `json:"difficulty,omitempty"`
		Seasonality        string   `json:"seasonality,omitempty"`
		IndicativeFees     string   `json:"indicativeFees,omitempty"`
		OpeningInfo        string   `json:"openingInfo,omitempty"`
		TransportNotes     string   `json:"transportNotes,omitempty"`
		Accessibility      string   `json:"accessibility,omitempty"`
		Facilities         string   `json:"facilities,omitempty"`
		SafetyNotes        string   `json:"safetyNotes,omitempty"`
		Source             string   `json:"source,omitempty"`
		ContentOwner       string   `json:"contentOwner,omitempty"`
		VerificationStatus string   `json:"verificationStatus,omitempty"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		handler.WriteError(w, http.StatusBadRequest, "INVALID_REQUEST", "invalid request body")
		return
	}

	queries := gen.New(h.pool)
	dest, err := queries.CreateDestination(r.Context(), &gen.CreateDestinationParams{
		Name:               req.Name,
		Slug:               req.Slug,
		County:             req.County,
		Locality:           strPtr(req.Locality),
		Category:           req.Category,
		Status:             "draft",
		StMakepoint:        req.Lng,
		StMakepoint_2:      req.Lat,
		MapLabel:           strPtr(req.MapLabel),
		AccessRoute:        strPtr(req.AccessRoute),
		DistanceReference:  strPtr(req.DistanceReference),
		ShortDescription:   strPtr(req.ShortDescription),
		FullDescription:    strPtr(req.FullDescription),
		Significance:       strPtr(req.Significance),
		History:            strPtr(req.History),
		ThingsToDo:         strPtr(req.ThingsToDo),
		SuitableAudiences:  strPtr(req.SuitableAudiences),
		Duration:           strPtr(req.Duration),
		Difficulty:         strPtr(req.Difficulty),
		Seasonality:        strPtr(req.Seasonality),
		IndicativeFees:     strPtr(req.IndicativeFees),
		OpeningInfo:        strPtr(req.OpeningInfo),
		TransportNotes:     strPtr(req.TransportNotes),
		Accessibility:      strPtr(req.Accessibility),
		Facilities:         strPtr(req.Facilities),
		SafetyNotes:        strPtr(req.SafetyNotes),
		Source:             strPtr(req.Source),
		ContentOwner:       strPtr(req.ContentOwner),
		VerificationStatus: strPtr(req.VerificationStatus),
	})
	if err != nil {
		handler.WriteError(w, http.StatusInternalServerError, "INTERNAL_ERROR", "failed to create destination")
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	json.NewEncoder(w).Encode(dest)
}

func (h *DestinationsHandler) Update(w http.ResponseWriter, r *http.Request) {
	destID := r.PathValue("id")
	var req struct {
		Name               *string  `json:"name,omitempty"`
		Slug               *string  `json:"slug,omitempty"`
		County             *string  `json:"county,omitempty"`
		Locality           *string  `json:"locality,omitempty"`
		Category           *string  `json:"category,omitempty"`
		Lat                *float64 `json:"lat,omitempty"`
		Lng                *float64 `json:"lng,omitempty"`
		MapLabel           *string  `json:"mapLabel,omitempty"`
		AccessRoute        *string  `json:"accessRoute,omitempty"`
		DistanceReference  *string  `json:"distanceReference,omitempty"`
		ShortDescription   *string  `json:"shortDescription,omitempty"`
		FullDescription    *string  `json:"fullDescription,omitempty"`
		Significance       *string  `json:"significance,omitempty"`
		History            *string  `json:"history,omitempty"`
		ThingsToDo         *string  `json:"thingsToDo,omitempty"`
		SuitableAudiences  *string  `json:"suitableAudiences,omitempty"`
		Duration           *string  `json:"duration,omitempty"`
		Difficulty         *string  `json:"difficulty,omitempty"`
		Seasonality        *string  `json:"seasonality,omitempty"`
		IndicativeFees     *string  `json:"indicativeFees,omitempty"`
		OpeningInfo        *string  `json:"openingInfo,omitempty"`
		TransportNotes     *string  `json:"transportNotes,omitempty"`
		Accessibility      *string  `json:"accessibility,omitempty"`
		Facilities         *string  `json:"facilities,omitempty"`
		SafetyNotes        *string  `json:"safetyNotes,omitempty"`
		Source             *string  `json:"source,omitempty"`
		ContentOwner       *string  `json:"contentOwner,omitempty"`
		VerificationStatus *string  `json:"verificationStatus,omitempty"`
		Status             *string  `json:"status,omitempty"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		handler.WriteError(w, http.StatusBadRequest, "INVALID_REQUEST", "invalid request body")
		return
	}

	_, err := h.pool.Exec(r.Context(),
		`UPDATE destinations SET
		 name = CASE WHEN $2::text != '' THEN $2 ELSE name END,
		 slug = CASE WHEN $3::text != '' THEN $3 ELSE slug END,
		 county = CASE WHEN $4::text != '' THEN $4 ELSE county END,
		 locality = COALESCE($5::text, locality),
		 category = CASE WHEN $6::text != '' THEN $6 ELSE category END,
		 location = CASE WHEN $7 IS NOT NULL AND $8 IS NOT NULL THEN ST_SetSRID(ST_MakePoint($7, $8), 4326) ELSE location END,
		 map_label = COALESCE($9::text, map_label),
		 access_route = COALESCE($10::text, access_route),
		 distance_reference = COALESCE($11::text, distance_reference),
		 short_description = COALESCE($12::text, short_description),
		 full_description = COALESCE($13::text, full_description),
		 significance = COALESCE($14::text, significance),
		 history = COALESCE($15::text, history),
		 things_to_do = COALESCE($16::text, things_to_do),
		 suitable_audiences = COALESCE($17::text, suitable_audiences),
		 duration = COALESCE($18::text, duration),
		 difficulty = COALESCE($19::text, difficulty),
		 seasonality = COALESCE($20::text, seasonality),
		 indicative_fees = COALESCE($21::text, indicative_fees),
		 opening_info = COALESCE($22::text, opening_info),
		 transport_notes = COALESCE($23::text, transport_notes),
		 accessibility = COALESCE($24::text, accessibility),
		 facilities = COALESCE($25::text, facilities),
		 safety_notes = COALESCE($26::text, safety_notes),
		 source = COALESCE($27::text, source),
		 content_owner = COALESCE($28::text, content_owner),
		 verification_status = COALESCE($29::text, verification_status),
		 status = CASE WHEN $30::text != '' THEN $30 ELSE status END,
		 updated_at = now()
		 WHERE id = $1`,
		destID, valOrEmpty(req.Name), valOrEmpty(req.Slug),
		valOrEmpty(req.County), req.Locality, valOrEmpty(req.Category),
		req.Lng, req.Lat,
		req.MapLabel, req.AccessRoute, req.DistanceReference,
		req.ShortDescription, req.FullDescription,
		req.Significance, req.History, req.ThingsToDo, req.SuitableAudiences,
		req.Duration, req.Difficulty, req.Seasonality,
		req.IndicativeFees, req.OpeningInfo, req.TransportNotes,
		req.Accessibility, req.Facilities, req.SafetyNotes,
		req.Source, req.ContentOwner, req.VerificationStatus,
		valOrEmpty(req.Status))
	if err != nil {
		handler.WriteError(w, http.StatusInternalServerError, "INTERNAL_ERROR", "failed to update destination")
		return
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{"status": "updated"})
}

func (h *DestinationsHandler) Delete(w http.ResponseWriter, r *http.Request) {
	destID := r.PathValue("id")
	_, err := h.pool.Exec(r.Context(),
		`UPDATE destinations SET status = 'archived', updated_at = now() WHERE id = $1`, destID)
	if err != nil {
		handler.WriteError(w, http.StatusInternalServerError, "INTERNAL_ERROR", "failed to archive destination")
		return
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{"status": "archived"})
}

func (h *DestinationsHandler) linkMedia(ctx context.Context, destID, mediaID string) error {
	tag, err := h.pool.Exec(ctx,
		`UPDATE media_assets SET entity_type = 'destination', entity_id = $1 WHERE id = $2`,
		destID, mediaID)
	if err != nil {
		return fmt.Errorf("db: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return fmt.Errorf("media %q not found", mediaID)
	}
	return nil
}

func (h *DestinationsHandler) AddMedia(w http.ResponseWriter, r *http.Request) {
	destID := r.PathValue("id")
	var req struct {
		HeroMediaID     string   `json:"heroMediaId,omitempty"`
		GalleryMediaIDs []string `json:"galleryMediaIds,omitempty"`
		VideoMediaID    string   `json:"videoMediaId,omitempty"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		handler.WriteError(w, http.StatusBadRequest, "INVALID_REQUEST", "invalid request body")
		return
	}

	var fails []string
	if req.HeroMediaID != "" {
		if err := h.linkMedia(r.Context(), destID, req.HeroMediaID); err != nil {
			fails = append(fails, fmt.Sprintf("hero: %v", err))
		}
	}
	for _, gid := range req.GalleryMediaIDs {
		if err := h.linkMedia(r.Context(), destID, gid); err != nil {
			fails = append(fails, fmt.Sprintf("gallery %q: %v", gid, err))
		}
	}
	if req.VideoMediaID != "" {
		if err := h.linkMedia(r.Context(), destID, req.VideoMediaID); err != nil {
			fails = append(fails, fmt.Sprintf("video: %v", err))
		}
	}

	total := 0
	if req.HeroMediaID != "" {
		total++
	}
	total += len(req.GalleryMediaIDs)
	if req.VideoMediaID != "" {
		total++
	}

	status := "linked"
	httpStatus := http.StatusCreated
	if len(fails) > 0 {
		for _, f := range fails {
			slog.Warn("add media", "error", f)
		}
		if len(fails) >= total {
			status = "failed"
			httpStatus = http.StatusInternalServerError
		} else {
			status = "partial"
			httpStatus = http.StatusOK
		}
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(httpStatus)
	json.NewEncoder(w).Encode(map[string]any{
		"status": status,
		"errors": fails,
	})
}

func (h *DestinationsHandler) ListMobile(w http.ResponseWriter, r *http.Request) {
	queries := gen.New(h.pool)
	dests, err := queries.ListMobileDestinations(r.Context(), 50)
	if err != nil {
		handler.WriteError(w, http.StatusInternalServerError, "INTERNAL_ERROR", "failed to list destinations")
		return
	}
	if dests == nil {
		dests = []gen.ListMobileDestinationsRow{}
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(dests)
}

func (h *DestinationsHandler) GetMobile(w http.ResponseWriter, r *http.Request) {
	slug := r.PathValue("slug")
	if slug == "" {
		handler.WriteError(w, http.StatusBadRequest, "INVALID_SLUG", "destination slug is required")
		return
	}
	queries := gen.New(h.pool)
	dest, err := queries.GetMobileDestinationBySlug(r.Context(), slug)
	if err != nil {
		handler.WriteError(w, http.StatusNotFound, "NOT_FOUND", "destination not found")
		return
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(dest)
}

func (h *DestinationsHandler) NearbyMobile(w http.ResponseWriter, r *http.Request) {
	latStr := r.URL.Query().Get("lat")
	lngStr := r.URL.Query().Get("lng")
	radiusStr := r.URL.Query().Get("radius_km")

	lat, _ := strconv.ParseFloat(latStr, 64)
	lng, _ := strconv.ParseFloat(lngStr, 64)
	radiusMeters := 50.0 * 1000
	if r, err := strconv.ParseFloat(radiusStr, 64); err == nil && r > 0 {
		radiusMeters = r * 1000
	}

	queries := gen.New(h.pool)
	results, err := queries.FindNearbyMobileDestinations(r.Context(), &gen.FindNearbyMobileDestinationsParams{
		StMakepoint:   lng,
		StMakepoint_2: lat,
		StDwithin:     radiusMeters,
		Limit:         20,
	})
	if err != nil {
		slog.Warn("nearby query failed", "error", err)
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode([]json.RawMessage{})
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(results)
}

func (h *DestinationsHandler) Nearby(w http.ResponseWriter, r *http.Request) {
	latStr := r.URL.Query().Get("lat")
	lngStr := r.URL.Query().Get("lng")
	radiusStr := r.URL.Query().Get("radius_km")

	lat, _ := strconv.ParseFloat(latStr, 64)
	lng, _ := strconv.ParseFloat(lngStr, 64)
	radiusMeters := 50.0 * 1000
	if r, err := strconv.ParseFloat(radiusStr, 64); err == nil && r > 0 {
		radiusMeters = r * 1000
	}

	queries := gen.New(h.pool)
	results, err := queries.FindNearbyDestinations(r.Context(), &gen.FindNearbyDestinationsParams{
		StMakepoint:   lng,
		StMakepoint_2: lat,
		StDwithin:     radiusMeters,
		Limit:         20,
	})
	if err != nil {
		slog.Warn("nearby query failed", "error", err)
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode([]json.RawMessage{})
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(results)
}
