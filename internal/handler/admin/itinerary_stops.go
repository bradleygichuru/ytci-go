package admin

import (
	"encoding/json"
	"net/http"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/bradleygichuru/ytci-go/internal/handler"
)

type stopInput struct {
	DestinationID string `json:"destinationId,omitempty"`
	Day           int    `json:"day"`
	DisplayOrder  int    `json:"displayOrder"`
	Title         string `json:"title,omitempty"`
	Description   string `json:"description,omitempty"`
	EstimatedCost string `json:"estimatedCost,omitempty"`
}

type ItineraryStopsHandler struct {
	pool *pgxpool.Pool
}

func NewItineraryStopsHandler(pool *pgxpool.Pool) *ItineraryStopsHandler {
	return &ItineraryStopsHandler{pool: pool}
}

func (h *ItineraryStopsHandler) UpsertStops(w http.ResponseWriter, r *http.Request) {
	itineraryID := r.PathValue("id")
	var req struct{ Stops []stopInput `json:"stops"` }
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		handler.WriteError(w, http.StatusBadRequest, "INVALID_REQUEST", "invalid request body")
		return
	}

	h.pool.Exec(r.Context(), `DELETE FROM itinerary_stops WHERE itinerary_id = $1`, itineraryID)
	for _, s := range req.Stops {
		h.pool.Exec(r.Context(),
			`INSERT INTO itinerary_stops (itinerary_id, day, display_order, title, description, estimated_cost)
			 VALUES ($1, $2, $3, $4, $5, $6)`,
			itineraryID, s.Day, s.DisplayOrder, s.Title, s.Description, s.EstimatedCost)
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{"status": "updated"})
}

func (h *ItineraryStopsHandler) GetStops(w http.ResponseWriter, r *http.Request) {
	itineraryID := r.PathValue("id")
	rows, err := h.pool.Query(r.Context(),
		`SELECT day, display_order, COALESCE(title, ''), COALESCE(description, ''), COALESCE(estimated_cost, '')
		 FROM itinerary_stops WHERE itinerary_id = $1 ORDER BY day, display_order`, itineraryID)
	if err != nil {
		handler.WriteError(w, http.StatusInternalServerError, "INTERNAL_ERROR", "failed to get stops")
		return
	}
	defer rows.Close()

	var stops []stopInput
	for rows.Next() {
		var s stopInput
		rows.Scan(&s.Day, &s.DisplayOrder, &s.Title, &s.Description, &s.EstimatedCost)
		stops = append(stops, s)
	}
	if stops == nil {
		stops = []stopInput{}
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]any{"stops": stops})
}
