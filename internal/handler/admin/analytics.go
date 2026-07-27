package admin

import (
	"encoding/json"
	"net/http"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/bradleygichuru/ytci-go/internal/model"
)

type AnalyticsHandler struct {
	pool *pgxpool.Pool
}

func NewAnalyticsHandler(pool *pgxpool.Pool) *AnalyticsHandler {
	return &AnalyticsHandler{pool: pool}
}

func (h *AnalyticsHandler) Summary(w http.ResponseWriter, r *http.Request) {
	resp := map[string]any{
		"dau":                   0,
		"wau":                   0,
		"mau":                   0,
		"newRegistrations":      0,
		"itinerariesGenerated":  0,
		"storiesSubmitted":      0,
		"courseEnrollments":     0,
		"conservationParticipants": 0,
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(resp)
}

func (h *AnalyticsHandler) ReportsList(w http.ResponseWriter, r *http.Request) {
	resp := model.Paginated[json.RawMessage]{
		Items:   []json.RawMessage{},
		HasMore: false,
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(resp)
}

func (h *AnalyticsHandler) Export(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Format   string   `json:"format"`
		DateFrom string   `json:"dateFrom"`
		DateTo   string   `json:"dateTo"`
		Sections []string `json:"sections"`
	}
	json.NewDecoder(r.Body).Decode(&req)

	result := map[string]string{"reportId": "", "status": "queued"}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusAccepted)
	json.NewEncoder(w).Encode(result)
}
