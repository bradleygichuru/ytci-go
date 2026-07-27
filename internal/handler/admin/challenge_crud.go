package admin

import (
	"encoding/json"
	"net/http"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/bradleygichuru/ytci-go/internal/handler"
	"github.com/bradleygichuru/ytci-go/internal/middleware"
)

type ChallengeCRUD struct {
	pool *pgxpool.Pool
}

func NewChallengeCRUD(pool *pgxpool.Pool) *ChallengeCRUD {
	return &ChallengeCRUD{pool: pool}
}

func (h *ChallengeCRUD) Create(w http.ResponseWriter, r *http.Request) {
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
	err := h.pool.QueryRow(r.Context(),
		`INSERT INTO challenges (title, description, badge_name, status, start_date, end_date, created_by)
		 VALUES ($1, $2, $3, 'draft', $4, $5, $6) RETURNING id`,
		req.Title, req.Description, req.BadgeName, req.StartDate, req.EndDate, middleware.UserID(r.Context()),
	).Scan(&id)
	if err != nil {
		handler.WriteError(w, http.StatusInternalServerError, "INTERNAL_ERROR", "failed to create challenge")
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	json.NewEncoder(w).Encode(map[string]string{"id": id, "status": "draft"})
}
