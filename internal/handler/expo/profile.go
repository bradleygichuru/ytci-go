package expo

import (
	"encoding/json"
	"net/http"

	"github.com/jackc/pgx/v5/pgxpool"
)

type ProfileHandler struct {
	pool *pgxpool.Pool
}

func NewProfileHandler(pool *pgxpool.Pool) *ProfileHandler {
	return &ProfileHandler{pool: pool}
}

func (h *ProfileHandler) Get(w http.ResponseWriter, r *http.Request) {
	resp := map[string]any{
		"countiesVisited": 0,
		"storiesSubmitted": 0,
		"challengesCompleted": 0,
		"coursesEnrolled": 0,
		"conservationHours": 0,
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(resp)
}
