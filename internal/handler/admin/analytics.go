package admin

import (
	"encoding/json"
	"net/http"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/bradleygichuru/ytci-go/internal/handler"
	"github.com/bradleygichuru/ytci-go/internal/middleware"
	"github.com/bradleygichuru/ytci-go/internal/model"
)

type AnalyticsHandler struct {
	pool *pgxpool.Pool
}

func NewAnalyticsHandler(pool *pgxpool.Pool) *AnalyticsHandler {
	return &AnalyticsHandler{pool: pool}
}

func (h *AnalyticsHandler) Summary(w http.ResponseWriter, r *http.Request) {
	today := time.Now().Truncate(24 * time.Hour)

	resp := map[string]any{
		"dau": 0, "wau": 0, "mau": 0, "newRegistrations": 0,
		"itinerariesGenerated": 0, "storiesSubmitted": 0,
		"courseEnrollments": 0, "conservationParticipants": 0,
	}

	var dau int
	if h.pool.QueryRow(r.Context(),
		`SELECT COUNT(DISTINCT user_id) FROM app_opens WHERE opened_at >= $1`, today).Scan(&dau) == nil {
		resp["dau"] = dau
	}
	var wau int
	if h.pool.QueryRow(r.Context(),
		`SELECT COUNT(DISTINCT user_id) FROM app_opens WHERE opened_at >= $1`,
		today.AddDate(0, 0, -7)).Scan(&wau) == nil {
		resp["wau"] = wau
	}
	var mau int
	if h.pool.QueryRow(r.Context(),
		`SELECT COUNT(DISTINCT user_id) FROM app_opens WHERE opened_at >= $1`,
		today.AddDate(0, -1, 0)).Scan(&mau) == nil {
		resp["mau"] = mau
	}
	var regs int
	if h.pool.QueryRow(r.Context(),
		`SELECT COUNT(*) FROM users WHERE created_at >= $1`,
		today.AddDate(0, 0, -30)).Scan(&regs) == nil {
		resp["newRegistrations"] = regs
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

type exportRequest struct {
	Format   string   `json:"format"`
	DateFrom string   `json:"dateFrom"`
	DateTo   string   `json:"dateTo"`
	Sections []string `json:"sections"`
}

func (h *AnalyticsHandler) Export(w http.ResponseWriter, r *http.Request) {
	var req exportRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		handler.WriteError(w, http.StatusBadRequest, "INVALID_REQUEST", "invalid request body")
		return
	}

	if req.Format != "csv" && req.Format != "pdf" {
		handler.WriteError(w, http.StatusBadRequest, "INVALID_FORMAT", "format must be csv or pdf")
		return
	}

	var reportID string
	err := h.pool.QueryRow(r.Context(),
		`INSERT INTO report_jobs (requested_by, format, date_from, date_to, sections, status)
		 VALUES ($1, $2, $3, $4, $5, 'generating') RETURNING id`,
		middleware.UserID(r.Context()), req.Format, req.DateFrom, req.DateTo, strings.Join(req.Sections, ","),
	).Scan(&reportID)
	if err != nil {
		handler.WriteServerError(w, r, "INTERNAL_ERROR", "failed to create report", err)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusAccepted)
	json.NewEncoder(w).Encode(map[string]string{
		"reportId": reportID,
		"status":   "generating",
		"createdAt": time.Now().UTC().Format(time.RFC3339),
	})
}
