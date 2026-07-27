package expo

import (
	"encoding/json"
	"net/http"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/bradleygichuru/ytci-go/internal/model"
)

type BucketHandler struct {
	pool *pgxpool.Pool
}

func NewBucketHandler(pool *pgxpool.Pool) *BucketHandler {
	return &BucketHandler{pool: pool}
}

func (h *BucketHandler) List(w http.ResponseWriter, r *http.Request) {
	resp := model.Paginated[json.RawMessage]{
		Items:   []json.RawMessage{},
		HasMore: false,
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(resp)
}

func (h *BucketHandler) Add(w http.ResponseWriter, r *http.Request) {
	w.WriteHeader(http.StatusCreated)
	json.NewEncoder(w).Encode(map[string]string{"status": "added"})
}
