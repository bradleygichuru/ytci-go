package admin

import (
	"encoding/json"
	"net/http"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/bradleygichuru/ytci-go/internal/model"
)

type CoursesHandler struct {
	pool *pgxpool.Pool
}

func NewCoursesHandler(pool *pgxpool.Pool) *CoursesHandler {
	return &CoursesHandler{pool: pool}
}

func (h *CoursesHandler) List(w http.ResponseWriter, r *http.Request) {
	resp := model.Paginated[json.RawMessage]{
		Items:   []json.RawMessage{},
		HasMore: false,
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(resp)
}
