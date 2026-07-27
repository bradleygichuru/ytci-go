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
	h.pool.Exec(r.Context(),
		`DELETE FROM itinerary_stops USING itineraries WHERE itinerary_stops.itinerary_id = $1 AND itineraries.id = $1 AND itineraries.user_id = $2`,
		itineraryID, userID)

	for _, s := range req.Stops {
		h.pool.Exec(r.Context(),
			`INSERT INTO itinerary_stops (itinerary_id, destination_id, day, display_order, title, description, estimated_cost)
			 VALUES ($1, $2, $3, $4, $5, $6, $7)`,
			itineraryID, s.DestinationID, s.Day, s.DisplayOrder, s.Title, s.Description, s.EstimatedCost)
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{"status": "updated"})
}

func (h *ItineraryStopsHandler) GetStops(w http.ResponseWriter, r *http.Request) {
	itineraryID := r.PathValue("id")
	rows, err := h.pool.Query(r.Context(),
		`SELECT is.day, is.display_order,
			COALESCE(is.title, ''), COALESCE(is.description, ''), COALESCE(is.estimated_cost, ''),
			COALESCE(is.destination_id::text, ''), d.name, d.slug
		 FROM itinerary_stops is
		 LEFT JOIN destinations d ON d.id = is.destination_id
		 WHERE is.itinerary_id = $1 ORDER BY is.day, is.display_order`, itineraryID)
	if err != nil {
		handler.WriteError(w, http.StatusInternalServerError, "INTERNAL_ERROR", "failed to get stops")
		return
	}
	defer rows.Close()

	var stops []StopResponse
	for rows.Next() {
		var s StopResponse
		rows.Scan(&s.Day, &s.DisplayOrder, &s.Title, &s.Description, &s.EstimatedCost,
			&s.DestinationID, &s.DestinationName, &s.DestinationSlug)
		stops = append(stops, s)
	}
	if stops == nil {
		stops = []StopResponse{}
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]any{"stops": stops})
}
