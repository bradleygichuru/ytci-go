package admin

import (
	"encoding/json"
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
		Name               *string `json:"name,omitempty"`
		ShortDescription   *string `json:"shortDescription,omitempty"`
		FullDescription    *string `json:"fullDescription,omitempty"`
		Significance       *string `json:"significance,omitempty"`
		History            *string `json:"history,omitempty"`
		ThingsToDo         *string `json:"thingsToDo,omitempty"`
		SuitableAudiences  *string `json:"suitableAudiences,omitempty"`
		Duration           *string `json:"duration,omitempty"`
		Difficulty         *string `json:"difficulty,omitempty"`
		Seasonality        *string `json:"seasonality,omitempty"`
		IndicativeFees     *string `json:"indicativeFees,omitempty"`
		OpeningInfo        *string `json:"openingInfo,omitempty"`
		TransportNotes     *string `json:"transportNotes,omitempty"`
		Accessibility      *string `json:"accessibility,omitempty"`
		Facilities         *string `json:"facilities,omitempty"`
		SafetyNotes        *string `json:"safetyNotes,omitempty"`
		Status             *string `json:"status,omitempty"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		handler.WriteError(w, http.StatusBadRequest, "INVALID_REQUEST", "invalid request body")
		return
	}

	_, err := h.pool.Exec(r.Context(),
		`UPDATE destinations SET
		 name = CASE WHEN $2::text != '' THEN $2 ELSE name END,
		 short_description = COALESCE($3::text, short_description),
		 full_description = COALESCE($4::text, full_description),
		 significance = COALESCE($5::text, significance),
		 history = COALESCE($6::text, history),
		 things_to_do = COALESCE($7::text, things_to_do),
		 suitable_audiences = COALESCE($8::text, suitable_audiences),
		 duration = COALESCE($9::text, duration),
		 difficulty = COALESCE($10::text, difficulty),
		 seasonality = COALESCE($11::text, seasonality),
		 indicative_fees = COALESCE($12::text, indicative_fees),
		 opening_info = COALESCE($13::text, opening_info),
		 transport_notes = COALESCE($14::text, transport_notes),
		 accessibility = COALESCE($15::text, accessibility),
		 facilities = COALESCE($16::text, facilities),
		 safety_notes = COALESCE($17::text, safety_notes),
		 status = CASE WHEN $18::text != '' THEN $18 ELSE status END,
		 updated_at = now()
		 WHERE id = $1`,
		destID, valOrEmpty(req.Name), req.ShortDescription, req.FullDescription,
		req.Significance, req.History, req.ThingsToDo, req.SuitableAudiences,
		req.Duration, req.Difficulty, req.Seasonality, req.IndicativeFees,
		req.OpeningInfo, req.TransportNotes, req.Accessibility, req.Facilities,
		req.SafetyNotes, valOrEmpty(req.Status))
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

func (h *DestinationsHandler) AddMedia(w http.ResponseWriter, r *http.Request) {
	destID := r.PathValue("id")
	var req struct {
		ObjectKey    string `json:"objectKey"`
		Type         string `json:"type"`
		Caption      string `json:"caption,omitempty"`
		AltText      string `json:"altText,omitempty"`
		Credit       string `json:"credit,omitempty"`
		DisplayOrder int    `json:"displayOrder,omitempty"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		handler.WriteError(w, http.StatusBadRequest, "INVALID_REQUEST", "invalid request body")
		return
	}

	var mediaID string
	err := h.pool.QueryRow(r.Context(),
		`INSERT INTO media_assets (entity_type, entity_id, object_key, type, caption, alt_text, credit, display_order, status)
		 VALUES ('destination', $1, $2, $3, $4, $5, $6, $7, 'ready') RETURNING id`,
		destID, req.ObjectKey, req.Type, req.Caption, req.AltText, req.Credit, req.DisplayOrder,
	).Scan(&mediaID)
	if err != nil {
		handler.WriteError(w, http.StatusInternalServerError, "INTERNAL_ERROR", "failed to add media")
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	json.NewEncoder(w).Encode(map[string]string{"id": mediaID, "status": "linked"})
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
