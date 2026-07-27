package admin

import (
	"encoding/json"
	"net/http"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/bradleygichuru/ytci-go/internal/handler"
	"github.com/bradleygichuru/ytci-go/internal/middleware"
)

type CourseCRUD struct {
	pool *pgxpool.Pool
}

func NewCourseCRUD(pool *pgxpool.Pool) *CourseCRUD {
	return &CourseCRUD{pool: pool}
}

func (h *CourseCRUD) Create(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Title       string `json:"title"`
		Description string `json:"description,omitempty"`
		Difficulty  string `json:"difficulty"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		handler.WriteError(w, http.StatusBadRequest, "INVALID_REQUEST", "invalid request body")
		return
	}

	var id, status string
	err := h.pool.QueryRow(r.Context(),
		`INSERT INTO courses (title, description, difficulty, created_by)
		 VALUES ($1, $2, $3, $4) RETURNING id, status`,
		req.Title, req.Description, req.Difficulty, middleware.UserID(r.Context()),
	).Scan(&id, &status)
	if err != nil {
		handler.WriteError(w, http.StatusInternalServerError, "INTERNAL_ERROR", "failed to create course")
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	json.NewEncoder(w).Encode(map[string]string{"id": id, "status": status})
}
