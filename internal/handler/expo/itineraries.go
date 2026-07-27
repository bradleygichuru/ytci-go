package expo

import (
	"encoding/json"
	"net/http"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/bradleygichuru/ytci-go/internal/model"
)

type ItinerariesHandler struct {
	pool *pgxpool.Pool
}

func NewItinerariesHandler(pool *pgxpool.Pool) *ItinerariesHandler {
	return &ItinerariesHandler{pool: pool}
}

func (h *ItinerariesHandler) List(w http.ResponseWriter, r *http.Request) {
	resp := model.Paginated[json.RawMessage]{
		Items:   []json.RawMessage{},
		HasMore: false,
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(resp)
}

func (h *ItinerariesHandler) Create(w http.ResponseWriter, r *http.Request) {
	w.WriteHeader(http.StatusCreated)
	json.NewEncoder(w).Encode(map[string]string{"id": "new-itinerary"})
}
