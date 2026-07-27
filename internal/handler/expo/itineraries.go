package expo

import (
	"context"
	"encoding/json"
	"net/http"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/bradleygichuru/ytci-go/internal/handler"
	"github.com/bradleygichuru/ytci-go/internal/middleware"
	"github.com/bradleygichuru/ytci-go/internal/model"
)

type ItinerariesHandler struct {
	pool *pgxpool.Pool
}

func NewItinerariesHandler(pool *pgxpool.Pool) *ItinerariesHandler {
	return &ItinerariesHandler{pool: pool}
}

func (h *ItinerariesHandler) List(w http.ResponseWriter, r *http.Request) {
	userID := middleware.UserID(r.Context())
	rows, err := h.pool.Query(r.Context(),
		`SELECT id, title, status, created_at FROM itineraries WHERE user_id = $1 ORDER BY created_at DESC`, userID)
	if err != nil {
		handler.WriteError(w, http.StatusInternalServerError, "INTERNAL_ERROR", "failed to list itineraries")
		return
	}
	defer rows.Close()

	type item struct {
		ID        string `json:"id"`
		Title     string `json:"title"`
		Status    string `json:"status"`
		CreatedAt string `json:"createdAt"`
	}
	var items []item
	for rows.Next() {
		var i item
		rows.Scan(&i.ID, &i.Title, &i.Status, &i.CreatedAt)
		items = append(items, i)
	}
	if items == nil {
		items = []item{}
	}

	resp := model.Paginated[item]{Items: items, HasMore: false}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(resp)
}

func (h *ItinerariesHandler) Create(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Title       string      `json:"title"`
		Origin      string      `json:"origin,omitempty"`
		Days        int         `json:"days,omitempty"`
		BudgetBand  string      `json:"budgetBand,omitempty"`
		Interests   []string    `json:"interests,omitempty"`
		Stops       []StopInput `json:"stops,omitempty"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		handler.WriteError(w, http.StatusBadRequest, "INVALID_REQUEST", "invalid request body")
		return
	}

	userID := middleware.UserID(r.Context())

	inputsRaw, err := json.Marshal(map[string]any{
		"origin": req.Origin, "days": req.Days,
		"budgetBand": req.BudgetBand, "interests": req.Interests,
	})
	if err != nil {
		handler.WriteError(w, http.StatusInternalServerError, "INTERNAL_ERROR", "failed to serialize inputs")
		return
	}

	tx, err := h.pool.Begin(r.Context())
	if err != nil {
		handler.WriteError(w, http.StatusInternalServerError, "INTERNAL_ERROR", "failed to create itinerary")
		return
	}
	defer tx.Rollback(context.Background())

	var itineraryID string
	err = tx.QueryRow(r.Context(),
		`INSERT INTO itineraries (user_id, title, inputs) VALUES ($1, $2, $3::jsonb) RETURNING id`,
		userID, req.Title, string(inputsRaw)).Scan(&itineraryID)
	if err != nil {
		handler.WriteError(w, http.StatusInternalServerError, "INTERNAL_ERROR", "failed to create itinerary")
		return
	}

	for _, s := range req.Stops {
		_, err = tx.Exec(r.Context(),
			`INSERT INTO itinerary_stops (itinerary_id, destination_id, day, display_order, title, description, estimated_cost)
			 VALUES ($1, $2, $3, $4, $5, $6, $7)`,
			itineraryID, s.DestinationID, s.Day, s.DisplayOrder, s.Title, s.Description, s.EstimatedCost)
		if err != nil {
			handler.WriteError(w, http.StatusInternalServerError, "INTERNAL_ERROR", "failed to create stops")
			return
		}
	}

	if err := tx.Commit(r.Context()); err != nil {
		handler.WriteError(w, http.StatusInternalServerError, "INTERNAL_ERROR", "failed to finalize")
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	json.NewEncoder(w).Encode(map[string]string{"id": itineraryID, "status": "draft"})
}

func (h *ItinerariesHandler) Get(w http.ResponseWriter, r *http.Request) {
	itineraryID := r.PathValue("id")
	if itineraryID == "" {
		handler.WriteError(w, http.StatusBadRequest, "INVALID_ID", "itinerary id is required")
		return
	}

	var title, status, inputs string
	err := h.pool.QueryRow(r.Context(),
		`SELECT title, status, inputs::text FROM itineraries WHERE id = $1 AND user_id = $2`,
		itineraryID, middleware.UserID(r.Context())).Scan(&title, &status, &inputs)
	if err != nil {
		handler.WriteError(w, http.StatusNotFound, "NOT_FOUND", "itinerary not found")
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{
		"id": itineraryID, "title": title, "status": status, "inputs": inputs,
	})
}

func (h *ItinerariesHandler) Update(w http.ResponseWriter, r *http.Request) {
	itineraryID := r.PathValue("id")
	if itineraryID == "" {
		handler.WriteError(w, http.StatusBadRequest, "INVALID_ID", "itinerary id is required")
		return
	}
	var req struct {
		Title  *string `json:"title,omitempty"`
		Status *string `json:"status,omitempty"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		handler.WriteError(w, http.StatusBadRequest, "INVALID_REQUEST", "invalid request body")
		return
	}

	_, err := h.pool.Exec(r.Context(),
		`UPDATE itineraries SET
		 title = COALESCE($2::text, title),
		 status = COALESCE($3::text, status),
		 updated_at = now()
		 WHERE id = $1 AND user_id = $4`,
		itineraryID, deref(req.Title), deref(req.Status), middleware.UserID(r.Context()))
	if err != nil {
		handler.WriteError(w, http.StatusInternalServerError, "INTERNAL_ERROR", "failed to update")
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{"status": "updated"})
}

func deref(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}

func (h *ItinerariesHandler) Delete(w http.ResponseWriter, r *http.Request) {
	itineraryID := r.PathValue("id")
	if itineraryID == "" {
		handler.WriteError(w, http.StatusBadRequest, "INVALID_ID", "itinerary id is required")
		return
	}

	_, err := h.pool.Exec(r.Context(),
		`DELETE FROM itineraries WHERE id = $1 AND user_id = $2`,
		itineraryID, middleware.UserID(r.Context()))
	if err != nil {
		handler.WriteError(w, http.StatusInternalServerError, "INTERNAL_ERROR", "failed to delete")
		return
	}
	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(map[string]string{"status": "deleted"})
}

func (h *ItinerariesHandler) Duplicate(w http.ResponseWriter, r *http.Request) {
	itineraryID := r.PathValue("id")
	if itineraryID == "" {
		handler.WriteError(w, http.StatusBadRequest, "INVALID_ID", "itinerary id is required")
		return
	}

	var newID string
	err := h.pool.QueryRow(r.Context(),
		`INSERT INTO itineraries (user_id, title, inputs, status)
		 SELECT user_id, title || ' (copy)', inputs, 'draft'
		 FROM itineraries WHERE id = $1 AND user_id = $2
		 RETURNING id`,
		itineraryID, middleware.UserID(r.Context())).Scan(&newID)
	if err != nil {
		handler.WriteError(w, http.StatusInternalServerError, "INTERNAL_ERROR", "failed to duplicate")
		return
	}

	w.WriteHeader(http.StatusCreated)
	json.NewEncoder(w).Encode(map[string]string{"id": newID, "status": "copied"})
}
