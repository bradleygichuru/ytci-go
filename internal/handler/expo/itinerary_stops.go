package expo

import (
	"encoding/json"
	"errors"
	"net/http"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/bradleygichuru/ytci-go/internal/collab"
	"github.com/bradleygichuru/ytci-go/internal/handler"
	"github.com/bradleygichuru/ytci-go/internal/middleware"
)

type StopInput struct {
	DestinationID string `json:"destinationId,omitempty"`
	Day           int    `json:"day"`
	DisplayOrder  int    `json:"displayOrder"`
	Title         string `json:"title,omitempty"`
	Description   string `json:"description,omitempty"`
	StartTime     string `json:"startTime,omitempty"`
	Category      string `json:"category,omitempty"`
	ImageURL      string `json:"imageUrl,omitempty"`
	EstimatedCost string `json:"estimatedCost,omitempty"`
}

type StopResponse struct {
	ID              string  `json:"id"`
	DestinationID   string  `json:"destinationId,omitempty"`
	DestinationName *string `json:"destinationName,omitempty"`
	DestinationSlug *string `json:"destinationSlug,omitempty"`
	Day             int     `json:"day"`
	DisplayOrder    int     `json:"displayOrder"`
	Title           string  `json:"title"`
	Description     string  `json:"description,omitempty"`
	StartTime       string  `json:"startTime,omitempty"`
	Category        string  `json:"category,omitempty"`
	ImageURL        string  `json:"imageUrl,omitempty"`
	EstimatedCost   string  `json:"estimatedCost,omitempty"`
}

type ItineraryStopsHandler struct {
	pool *pgxpool.Pool
}

func NewItineraryStopsHandler(pool *pgxpool.Pool) *ItineraryStopsHandler {
	return &ItineraryStopsHandler{pool: pool}
}

func (h *ItineraryStopsHandler) UpsertStops(w http.ResponseWriter, r *http.Request) {
	itineraryID := r.PathValue("id")
	if itineraryID == "" {
		handler.WriteError(w, http.StatusBadRequest, "INVALID_ID", "itinerary id is required")
		return
	}
	var req struct {
		Stops           []StopInput `json:"stops"`
		ExpectedVersion int         `json:"expectedVersion,omitempty"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		handler.WriteError(w, http.StatusBadRequest, "INVALID_REQUEST", "invalid request body")
		return
	}

	userID := middleware.UserID(r.Context())
	tx, err := h.pool.Begin(r.Context())
	if err != nil {
		handler.WriteServerError(w, r, "INTERNAL_ERROR", "failed to begin transaction", err)
		return
	}
	defer tx.Rollback(r.Context())

	if _, err := requireItineraryRole(r.Context(), tx, itineraryID, userID, roleEditor); err != nil {
		writeItineraryAccessError(w, r, err)
		return
	}

	var currentVersion int
	err = tx.QueryRow(r.Context(),
		`SELECT plan_version FROM itineraries WHERE id = $1 AND deleted_at IS NULL FOR UPDATE`,
		itineraryID).Scan(&currentVersion)
	if errors.Is(err, pgx.ErrNoRows) {
		handler.WriteError(w, http.StatusNotFound, "NOT_FOUND", "itinerary not found")
		return
	}
	if err != nil {
		handler.WriteServerError(w, r, "INTERNAL_ERROR", "failed to lock itinerary", err)
		return
	}
	if req.ExpectedVersion > 0 && req.ExpectedVersion != currentVersion {
		tx.Rollback(r.Context())
		writeVersionConflict(w, currentVersion)
		return
	}

	if _, err := tx.Exec(r.Context(),
		`DELETE FROM itinerary_stops WHERE itinerary_id = $1`, itineraryID); err != nil {
		handler.WriteServerError(w, r, "INTERNAL_ERROR", "failed to clear stops", err)
		return
	}

	for _, s := range req.Stops {
		if s.Day < 1 || s.DisplayOrder < 0 {
			handler.WriteError(w, http.StatusBadRequest, "INVALID_STOP", "day must be at least 1 and display order cannot be negative")
			return
		}
		_, err = tx.Exec(r.Context(),
			`INSERT INTO itinerary_stops (itinerary_id, destination_id, day, display_order, title, description, start_time, category, image_url, estimated_cost)
			 VALUES ($1, NULLIF($2, '')::uuid, $3, $4, $5, $6, NULLIF($7, ''), NULLIF($8, ''), NULLIF($9, ''), $10)`,
			itineraryID, s.DestinationID, s.Day, s.DisplayOrder, s.Title, s.Description,
			s.StartTime, s.Category, s.ImageURL, s.EstimatedCost)
		if err != nil {
			handler.WriteServerError(w, r, "INTERNAL_ERROR", "failed to insert stop", err)
			return
		}
	}

	newVersion, revisionID, err := bumpPlanVersionAndRevision(r.Context(), tx, itineraryID, userID, "stops_saved")
	if err != nil {
		handler.WriteServerError(w, r, "INTERNAL_ERROR", "failed to snapshot trip", err)
		return
	}
	if err := recordTripEvent(r.Context(), tx, itineraryID, userID, "plan", "stops_updated",
		"stops", "", "Updated the trip plan",
		map[string]any{"version": currentVersion}, map[string]any{"stops": len(req.Stops)}, revisionID); err != nil {
		handler.WriteServerError(w, r, "INTERNAL_ERROR", "failed to record trip event", err)
		return
	}

	if err := tx.Commit(r.Context()); err != nil {
		handler.WriteServerError(w, r, "INTERNAL_ERROR", "failed to commit", err)
		return
	}
	_ = collab.PublishChange(r.Context(), h.pool, itineraryID, "plan", "stops_updated", newVersion, userID)

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]any{"status": "updated", "version": newVersion})
}

func (h *ItineraryStopsHandler) GetStops(w http.ResponseWriter, r *http.Request) {
	itineraryID := r.PathValue("id")
	if itineraryID == "" {
		handler.WriteError(w, http.StatusBadRequest, "INVALID_ID", "itinerary id is required")
		return
	}

	access, err := requireItineraryRole(r.Context(), h.pool, itineraryID, middleware.UserID(r.Context()), roleViewer)
	if err != nil {
		writeItineraryAccessError(w, r, err)
		return
	}

	rows, err := h.pool.Query(r.Context(),
		`SELECT s.id::text, s.day, s.display_order,
			COALESCE(s.title, ''), COALESCE(s.description, ''), COALESCE(s.start_time, ''), COALESCE(s.category, ''), COALESCE(s.image_url, ''),
			COALESCE(s.estimated_cost, ''),
			COALESCE(s.destination_id::text, ''), d.name, d.slug
		 FROM itinerary_stops s
		 LEFT JOIN destinations d ON d.id = s.destination_id
		 WHERE s.itinerary_id = $1 ORDER BY s.day, s.display_order`, itineraryID)
	if err != nil {
		handler.WriteServerError(w, r, "INTERNAL_ERROR", "failed to get stops", err)
		return
	}
	defer rows.Close()

	var stops []StopResponse
	for rows.Next() {
		var s StopResponse
		if err := rows.Scan(&s.ID, &s.Day, &s.DisplayOrder, &s.Title, &s.Description, &s.StartTime, &s.Category, &s.ImageURL,
			&s.EstimatedCost, &s.DestinationID, &s.DestinationName, &s.DestinationSlug); err != nil {
			handler.WriteServerError(w, r, "INTERNAL_ERROR", "failed to read stop", err)
			return
		}
		stops = append(stops, s)
	}
	if err := rows.Err(); err != nil {
		handler.WriteServerError(w, r, "INTERNAL_ERROR", "failed to iterate stops", err)
		return
	}
	if stops == nil {
		stops = []StopResponse{}
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]any{"stops": stops, "version": access.PlanVersion})
}
