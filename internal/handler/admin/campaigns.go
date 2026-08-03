package admin

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/bradleygichuru/ytci-go/internal/handler"
	"github.com/bradleygichuru/ytci-go/internal/middleware"
	"github.com/bradleygichuru/ytci-go/internal/model"
)

type CampaignAdminHandler struct {
	pool *pgxpool.Pool
}

func NewCampaignAdminHandler(pool *pgxpool.Pool) *CampaignAdminHandler {
	return &CampaignAdminHandler{pool: pool}
}

func (h *CampaignAdminHandler) List(w http.ResponseWriter, r *http.Request) {
	rows, err := h.pool.Query(r.Context(),
		`SELECT id, title, type, status, start_date::text, end_date::text, banner_url, target_url, destination_id, audience, description
		 FROM campaigns ORDER BY created_at DESC LIMIT 50`)
	if err != nil {
		handler.WriteServerError(w, r, "INTERNAL_ERROR", "failed to list campaigns", err)
		return
	}
	defer rows.Close()

	type item struct {
		ID            string  `json:"id"`
		Title         string  `json:"title"`
		Description   *string `json:"description"`
		Type          string  `json:"type"`
		Status        string  `json:"status"`
		StartDate     *string `json:"startDate"`
		EndDate       *string `json:"endDate"`
		BannerUrl     *string `json:"bannerUrl"`
		TargetUrl     *string `json:"targetUrl"`
		DestinationId *string `json:"destinationId"`
		Audience      *string `json:"audience"`
	}
	var items []item
	for rows.Next() {
		var i item
		if err := rows.Scan(&i.ID, &i.Title, &i.Description, &i.Type, &i.Status, &i.StartDate, &i.EndDate, &i.BannerUrl, &i.TargetUrl, &i.DestinationId, &i.Audience); err != nil {
			slog.Warn("scan campaign row", "error", err)
			continue
		}
		items = append(items, i)
	}
	if items == nil {
		items = []item{}
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(model.Paginated[item]{Items: items, HasMore: false})
}

func (h *CampaignAdminHandler) Get(w http.ResponseWriter, r *http.Request) {
	campaignID := r.PathValue("id")
	row := h.pool.QueryRow(r.Context(),
		`SELECT id, title, type, status, start_date::text, end_date::text, banner_url, target_url, destination_id, audience, description
		 FROM campaigns WHERE id = $1`, campaignID)

	var id, title, ctype, status string
	var start, end, banner, target, destID, audience, description *string
	err := row.Scan(&id, &title, &ctype, &status, &start, &end, &banner, &target, &destID, &audience, &description)
	if err != nil {
		handler.WriteError(w, http.StatusNotFound, "NOT_FOUND", "campaign not found")
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]any{
		"id": id, "title": title, "description": description, "type": ctype, "status": status,
		"startDate": start, "endDate": end, "bannerUrl": banner,
		"targetUrl": target, "destinationId": destID, "audience": audience,
	})
}

func (h *CampaignAdminHandler) Create(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Title         string `json:"title"`
		Description   string `json:"description"`
		Type          string `json:"type"`
		StartDate     string `json:"startDate"`
		EndDate       string `json:"endDate,omitempty"`
		BannerURL     string `json:"bannerUrl,omitempty"`
		TargetURL     string `json:"targetUrl,omitempty"`
		DestinationID string `json:"destinationId,omitempty"`
		Audience      string `json:"audience,omitempty"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		handler.WriteError(w, http.StatusBadRequest, "INVALID_REQUEST", "invalid request body")
		return
	}

	if req.Audience != "" && req.Audience != "all" {
		var parsed map[string]any
		if err := json.Unmarshal([]byte(req.Audience), &parsed); err != nil {
			handler.WriteError(w, http.StatusBadRequest, "INVALID_AUDIENCE", "audience must be 'all' or a JSON object")
			return
		}
	}

	var endDate *string
	if req.EndDate != "" {
		endDate = &req.EndDate
	}
	var bannerURL *string
	if req.BannerURL != "" {
		bannerURL = &req.BannerURL
	}
	var targetURL *string
	if req.TargetURL != "" {
		targetURL = &req.TargetURL
	}
	var destID *string
	if req.DestinationID != "" {
		destID = &req.DestinationID
	}
	var audience *string
	if req.Audience != "" {
		audience = &req.Audience
	}

	var description *string
	if req.Description != "" {
		description = &req.Description
	}

	var id string
	err := h.pool.QueryRow(r.Context(),
		`INSERT INTO campaigns (title, banner_url, type, start_date, end_date, target_url, destination_id, audience, created_by, description)
		 VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10) RETURNING id`,
		req.Title, bannerURL, req.Type, req.StartDate, endDate, targetURL, destID, audience,
		middleware.UserID(r.Context()), description,
	).Scan(&id)
	if err != nil {
		handler.WriteServerError(w, r, "INTERNAL_ERROR", "failed to create campaign", err)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	json.NewEncoder(w).Encode(map[string]string{"id": id, "status": "draft"})
}

func (h *CampaignAdminHandler) Update(w http.ResponseWriter, r *http.Request) {
	campaignID := r.PathValue("id")
	var req struct {
		Title         *string `json:"title,omitempty"`
		Description   *string `json:"description,omitempty"`
		Status        *string `json:"status,omitempty"`
		Type          *string `json:"type,omitempty"`
		StartDate     *string `json:"startDate,omitempty"`
		EndDate       *string `json:"endDate,omitempty"`
		BannerURL     *string `json:"bannerUrl,omitempty"`
		TargetURL     *string `json:"targetUrl,omitempty"`
		DestinationID *string `json:"destinationId,omitempty"`
		Audience      *string `json:"audience,omitempty"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		handler.WriteError(w, http.StatusBadRequest, "INVALID_REQUEST", "invalid request body")
		return
	}

	if req.Audience != nil && *req.Audience != "" && *req.Audience != "all" {
		var parsed map[string]any
		if err := json.Unmarshal([]byte(*req.Audience), &parsed); err != nil {
			handler.WriteError(w, http.StatusBadRequest, "INVALID_AUDIENCE", "audience must be 'all' or a JSON object")
			return
		}
	}

	sets := ""
	args := []any{campaignID}
	argIdx := 2

	addStr := func(col string, val *string) {
		if val != nil && *val != "" {
			sets += fmt.Sprintf(", %s = $%d", col, argIdx)
			args = append(args, *val)
			argIdx++
		}
	}

	addStr("title", req.Title)
	addStr("description", req.Description)
	addStr("status", req.Status)
	addStr("type", req.Type)
	addStr("start_date", req.StartDate)
	addStr("end_date", req.EndDate)
	addStr("banner_url", req.BannerURL)
	addStr("target_url", req.TargetURL)
	addStr("destination_id", req.DestinationID)
	addStr("audience", req.Audience)

	if sets == "" {
		handler.WriteError(w, http.StatusBadRequest, "NO_CHANGES", "no fields to update")
		return
	}

	q := "UPDATE campaigns SET updated_at = now()" + sets + " WHERE id = $1"
	result, err := h.pool.Exec(r.Context(), q, args...)
	if err != nil {
		handler.WriteServerError(w, r, "INTERNAL_ERROR", "failed to update campaign", err)
		return
	}
	if result.RowsAffected() == 0 {
		handler.WriteError(w, http.StatusNotFound, "NOT_FOUND", "campaign not found")
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{"status": "updated"})
}

func valOrEmpty(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}

func (h *CampaignAdminHandler) Delete(w http.ResponseWriter, r *http.Request) {
	campaignID := r.PathValue("id")
	_, err := h.pool.Exec(r.Context(),
		`UPDATE campaigns SET status = 'ended', updated_at = now() WHERE id = $1`, campaignID)
	if err != nil {
		handler.WriteServerError(w, r, "INTERNAL_ERROR", "failed to end campaign", err)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{"status": "ended"})
}
