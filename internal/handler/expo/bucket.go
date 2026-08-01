package expo

import (
	"encoding/json"
	"net/http"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/bradleygichuru/ytci-go/internal/handler"
	"github.com/bradleygichuru/ytci-go/internal/middleware"
	"github.com/bradleygichuru/ytci-go/internal/model"
)

type BucketHandler struct {
	pool *pgxpool.Pool
}

func NewBucketHandler(pool *pgxpool.Pool) *BucketHandler {
	return &BucketHandler{pool: pool}
}

func (h *BucketHandler) List(w http.ResponseWriter, r *http.Request) {
	userID := middleware.UserID(r.Context())
	rows, err := h.pool.Query(r.Context(),
		`SELECT d.id, d.name, d.slug, d.county, d.short_description, b.visited, b.visited_at::text
		 FROM bucket_list_items b JOIN destinations d ON d.id = b.destination_id
		 WHERE b.user_id = $1 ORDER BY b.created_at DESC`, userID)
	if err != nil {
		handler.WriteServerError(w, r, "INTERNAL_ERROR", "failed to list bucket", err)
		return
	}
	defer rows.Close()

	type item struct {
		ID             string  `json:"id"`
		Name           string  `json:"name"`
		Slug           string  `json:"slug"`
		County         string  `json:"county"`
		Description    *string `json:"shortDescription,omitempty"`
		Visited        bool    `json:"visited"`
		VisitedAt      *string `json:"visitedAt,omitempty"`
	}

	var items []item
	for rows.Next() {
		var i item
		if err := rows.Scan(&i.ID, &i.Name, &i.Slug, &i.County, &i.Description, &i.Visited, &i.VisitedAt); err != nil {
			continue
		}
		items = append(items, i)
	}

	resp := model.Paginated[item]{
		Items:   items,
		HasMore: false,
	}
	if len(items) == 0 {
		resp.Items = []item{}
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(resp)
}

func (h *BucketHandler) Add(w http.ResponseWriter, r *http.Request) {
	var req struct {
		DestinationID string `json:"destinationId"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.DestinationID == "" {
		handler.WriteError(w, http.StatusBadRequest, "INVALID_REQUEST", "destinationId is required")
		return
	}

	_, err := h.pool.Exec(r.Context(),
		`INSERT INTO bucket_list_items (user_id, destination_id) VALUES ($1, $2) ON CONFLICT DO NOTHING`,
		middleware.UserID(r.Context()), req.DestinationID)
	if err != nil {
		handler.WriteServerError(w, r, "INTERNAL_ERROR", "failed to add", err)
		return
	}

	w.WriteHeader(http.StatusCreated)
	json.NewEncoder(w).Encode(map[string]string{"status": "added"})
}

func (h *BucketHandler) Remove(w http.ResponseWriter, r *http.Request) {
	destID := r.PathValue("destinationId")
	if destID == "" {
		handler.WriteError(w, http.StatusBadRequest, "INVALID_ID", "destination id is required")
		return
	}

	_, err := h.pool.Exec(r.Context(),
		`DELETE FROM bucket_list_items WHERE user_id = $1 AND destination_id = $2`,
		middleware.UserID(r.Context()), destID)
	if err != nil {
		handler.WriteServerError(w, r, "INTERNAL_ERROR", "failed to remove", err)
		return
	}

	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(map[string]string{"status": "removed"})
}

func (h *BucketHandler) MarkVisited(w http.ResponseWriter, r *http.Request) {
	destID := r.PathValue("destinationId")
	if destID == "" {
		handler.WriteError(w, http.StatusBadRequest, "INVALID_ID", "destination id is required")
		return
	}

	_, err := h.pool.Exec(r.Context(),
		`UPDATE bucket_list_items SET visited = true, visited_at = now() WHERE user_id = $1 AND destination_id = $2`,
		middleware.UserID(r.Context()), destID)
	if err != nil {
		handler.WriteServerError(w, r, "INTERNAL_ERROR", "failed to mark visited", err)
		return
	}

	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(map[string]string{"status": "visited"})
}
