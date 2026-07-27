package admin

import (
	"encoding/json"
	"net/http"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/bradleygichuru/ytci-go/internal/model"
)

type CampaignsHandler struct {
	pool *pgxpool.Pool
}

func NewCampaignsHandler(pool *pgxpool.Pool) *CampaignsHandler {
	return &CampaignsHandler{pool: pool}
}

func (h *CampaignsHandler) List(w http.ResponseWriter, r *http.Request) {
	resp := model.Paginated[json.RawMessage]{
		Items:   []json.RawMessage{},
		HasMore: false,
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(resp)
}
