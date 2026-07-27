package admin

import (
	"encoding/json"
	"fmt"
	"net/http"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/bradleygichuru/ytci-go/internal/handler"
	"github.com/bradleygichuru/ytci-go/internal/middleware"
	"github.com/bradleygichuru/ytci-go/internal/model"
)

type ChallengeAdminHandler struct {
	pool *pgxpool.Pool
}

func NewChallengeAdminHandler(pool *pgxpool.Pool) *ChallengeAdminHandler {
	return &ChallengeAdminHandler{pool: pool}
}

func (h *ChallengeAdminHandler) List(w http.ResponseWriter, r *http.Request) {
	rows, err := h.pool.Query(r.Context(),
		`SELECT id, title, badge_name, status, start_date, end_date FROM challenges ORDER BY created_at DESC LIMIT 50`)
	if err != nil {
		handler.WriteError(w, http.StatusInternalServerError, "INTERNAL_ERROR", "failed to list challenges")
		return
	}
	defer rows.Close()

	type item struct {
		ID     string  `json:"id"`
		Title  string  `json:"title"`
		Badge  *string `json:"badgeName,omitempty"`
		Status string  `json:"status"`
		Start  *string `json:"startDate,omitempty"`
		End    *string `json:"endDate,omitempty"`
	}
	var items []item
	for rows.Next() {
		var i item
		rows.Scan(&i.ID, &i.Title, &i.Badge, &i.Status, &i.Start, &i.End)
		items = append(items, i)
	}
	if items == nil {
		items = []item{}
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(model.Paginated[item]{Items: items, HasMore: false})
}

func (h *ChallengeAdminHandler) Create(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Title       string `json:"title"`
		Description string `json:"description,omitempty"`
		BadgeName   string `json:"badgeName,omitempty"`
		StartDate   string `json:"startDate,omitempty"`
		EndDate     string `json:"endDate,omitempty"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		handler.WriteError(w, http.StatusBadRequest, "INVALID_REQUEST", "invalid request body")
		return
	}

	var id string
	if err := h.pool.QueryRow(r.Context(),
		`INSERT INTO challenges (title, description, badge_name, status, start_date, end_date, created_by)
		 VALUES ($1, $2, $3, 'draft', $4, $5, $6) RETURNING id`,
		req.Title, req.Description, req.BadgeName, req.StartDate, req.EndDate, middleware.UserID(r.Context()),
	).Scan(&id); err != nil {
		handler.WriteError(w, http.StatusInternalServerError, "INTERNAL_ERROR", "failed to create challenge")
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	json.NewEncoder(w).Encode(map[string]string{"id": id, "status": "draft"})
}

func (h *ChallengeAdminHandler) Update(w http.ResponseWriter, r *http.Request) {
	challengeID := r.PathValue("id")
	var req struct {
		Title       *string `json:"title,omitempty"`
		Description *string `json:"description,omitempty"`
		BadgeName   *string `json:"badgeName,omitempty"`
		StartDate   *string `json:"startDate,omitempty"`
		EndDate     *string `json:"endDate,omitempty"`
		Status      *string `json:"status,omitempty"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		handler.WriteError(w, http.StatusBadRequest, "INVALID_REQUEST", "invalid request body")
		return
	}

	query := `UPDATE challenges SET`
	args := []any{challengeID}
	n := 2

	if req.Title != nil {
		query += fmt.Sprintf(` title = $%d,`, n); args = append(args, *req.Title); n++
	}
	if req.Description != nil {
		query += fmt.Sprintf(` description = $%d,`, n); args = append(args, *req.Description); n++
	}
	if req.BadgeName != nil {
		query += fmt.Sprintf(` badge_name = $%d,`, n); args = append(args, *req.BadgeName); n++
	}
	if req.StartDate != nil {
		query += fmt.Sprintf(` start_date = $%d::date,`, n); args = append(args, *req.StartDate); n++
	}
	if req.EndDate != nil {
		query += fmt.Sprintf(` end_date = $%d::date,`, n); args = append(args, *req.EndDate); n++
	}
	if req.Status != nil {
		query += fmt.Sprintf(` status = $%d,`, n); args = append(args, *req.Status); n++
	}
	query += ` updated_at = now() WHERE id = $1`

	_, err := h.pool.Exec(r.Context(), query, args...)
	if err != nil {
		handler.WriteError(w, http.StatusInternalServerError, "INTERNAL_ERROR", "failed to update challenge")
		return
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{"status": "updated"})
}

func (h *ChallengeAdminHandler) Delete(w http.ResponseWriter, r *http.Request) {
	challengeID := r.PathValue("id")
	_, err := h.pool.Exec(r.Context(),
		`UPDATE challenges SET status = 'cancelled', updated_at = now() WHERE id = $1`, challengeID)
	if err != nil {
		handler.WriteError(w, http.StatusInternalServerError, "INTERNAL_ERROR", "failed to cancel challenge")
		return
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{"status": "cancelled"})
}
