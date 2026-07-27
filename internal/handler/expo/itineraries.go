package expo

import (
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
		Title  string `json:"title"`
		Inputs string `json:"inputs"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		handler.WriteError(w, http.StatusBadRequest, "INVALID_REQUEST", "invalid request body")
		return
	}

	var id string
	err := h.pool.QueryRow(r.Context(),
		`INSERT INTO itineraries (user_id, title, inputs) VALUES ($1, $2, $3::jsonb) RETURNING id`,
		middleware.UserID(r.Context()), req.Title, req.Inputs).Scan(&id)
	if err != nil {
		handler.WriteError(w, http.StatusInternalServerError, "INTERNAL_ERROR", "failed to create itinerary")
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	json.NewEncoder(w).Encode(map[string]string{"id": id, "status": "draft"})
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
