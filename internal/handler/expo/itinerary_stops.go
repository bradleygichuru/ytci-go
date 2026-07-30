package expo

import (
	"encoding/json"
	"net/http"

	"github.com/jackc/pgx/v5/pgxpool"

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
	DestinationID    string  `json:"destinationId,omitempty"`
	DestinationName  *string `json:"destinationName,omitempty"`
	DestinationSlug  *string `json:"destinationSlug,omitempty"`
	Day              int     `json:"day"`
	DisplayOrder     int     `json:"displayOrder"`
	Title            string  `json:"title"`
	Description      string  `json:"description,omitempty"`
	StartTime        string  `json:"startTime,omitempty"`
	Category         string  `json:"category,omitempty"`
	ImageURL         string  `json:"imageUrl,omitempty"`
	EstimatedCost    string  `json:"estimatedCost,omitempty"`
}

type ItineraryStopsHandler struct {
	pool *pgxpool.Pool
}

func NewItineraryStopsHandler(pool *pgxpool.Pool) *ItineraryStopsHandler {
	return &ItineraryStopsHandler{pool: pool}
}

func (h *ItineraryStopsHandler) UpsertStops(w http.ResponseWriter, r *http.Request) {
	itineraryID := r.PathValue("id")
	var req struct{ Stops []StopInput `json:"stops"` }
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

	_, err = tx.Exec(r.Context(),
		`DELETE FROM itinerary_stops USING itineraries WHERE itinerary_stops.itinerary_id = $1 AND itineraries.id = $1 AND itineraries.user_id = $2`,
		itineraryID, userID)
	if err != nil {
		handler.WriteServerError(w, r, "INTERNAL_ERROR", "failed to clear stops", err)
		return
	}

	for _, s := range req.Stops {
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

	if err := tx.Commit(r.Context()); err != nil {
		handler.WriteServerError(w, r, "INTERNAL_ERROR", "failed to commit", err)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{"status": "updated"})
}

func (h *ItineraryStopsHandler) GetStops(w http.ResponseWriter, r *http.Request) {
	itineraryID := r.PathValue("id")
	rows, err := h.pool.Query(r.Context(),
		`SELECT s.day, s.display_order,
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
		rows.Scan(&s.Day, &s.DisplayOrder, &s.Title, &s.Description, &s.StartTime, &s.Category, &s.ImageURL,
			&s.EstimatedCost, &s.DestinationID, &s.DestinationName, &s.DestinationSlug)
		stops = append(stops, s)
	}
	if stops == nil {
		stops = []StopResponse{}
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]any{"stops": stops})
}
