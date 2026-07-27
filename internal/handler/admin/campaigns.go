package admin

import (
	"encoding/json"
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
		`SELECT id, title, type, status, start_date, end_date FROM campaigns ORDER BY created_at DESC LIMIT 50`)
	if err != nil {
		handler.WriteError(w, http.StatusInternalServerError, "INTERNAL_ERROR", "failed to list campaigns")
		return
	}
	defer rows.Close()

	type item struct {
		ID     string  `json:"id"`
		Title  string  `json:"title"`
		Type   string  `json:"type"`
		Status string  `json:"status"`
		Start  *string `json:"startDate,omitempty"`
		End    *string `json:"endDate,omitempty"`
	}
	var items []item
	for rows.Next() {
		var i item
		rows.Scan(&i.ID, &i.Title, &i.Type, &i.Status, &i.Start, &i.End)
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
		`SELECT id, title, type, status, start_date, end_date, target_url, audience, banner_url
		 FROM campaigns WHERE id = $1`, campaignID)

	var id, title, ctype, status, target, audience, banner string
	var start, end *string
	err := row.Scan(&id, &title, &ctype, &status, &start, &end, &target, &audience, &banner)
	if err != nil {
		handler.WriteError(w, http.StatusNotFound, "NOT_FOUND", "campaign not found")
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]any{
		"id": id, "title": title, "type": ctype, "status": status,
		"startDate": start, "endDate": end, "targetUrl": target,
		"audience": audience, "bannerUrl": banner,
	})
}

func (h *CampaignAdminHandler) Create(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Title       string  `json:"title"`
		Type        string  `json:"type"`
		StartDate   string  `json:"startDate"`
		EndDate     string  `json:"endDate,omitempty"`
		BannerURL   string  `json:"bannerUrl,omitempty"`
		TargetURL   string  `json:"targetUrl,omitempty"`
		DestinationID string `json:"destinationId,omitempty"`
		Audience    string  `json:"audience,omitempty"`
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

	var id string
	err := h.pool.QueryRow(r.Context(),
		`INSERT INTO campaigns (title, banner_url, type, start_date, end_date, target_url, audience, created_by)
		 VALUES ($1, $2, $3, $4, $5, $6, $7, $8) RETURNING id`,
		req.Title, req.BannerURL, req.Type, req.StartDate, req.EndDate, req.TargetURL, req.Audience,
		middleware.UserID(r.Context()),
	).Scan(&id)
	if err != nil {
		handler.WriteError(w, http.StatusInternalServerError, "INTERNAL_ERROR", "failed to create campaign")
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	json.NewEncoder(w).Encode(map[string]string{"id": id, "status": "draft"})
}

func (h *CampaignAdminHandler) Update(w http.ResponseWriter, r *http.Request) {
	campaignID := r.PathValue("id")
	var req struct {
		Title     *string `json:"title,omitempty"`
		Status    *string `json:"status,omitempty"`
		TargetURL *string `json:"targetUrl,omitempty"`
		Audience  *string `json:"audience,omitempty"`
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

	_, err := h.pool.Exec(r.Context(),
		`UPDATE campaigns SET
		 title = CASE WHEN $2::text != '' THEN $2 ELSE title END,
		 status = CASE WHEN $3::text != '' THEN $3 ELSE status END,
		 target_url = CASE WHEN $4::text != '' THEN $4 ELSE target_url END,
		 updated_at = now()
		 WHERE id = $1`,
		campaignID, valOrEmpty(req.Title), valOrEmpty(req.Status), valOrEmpty(req.TargetURL))
	if err != nil {
		handler.WriteError(w, http.StatusInternalServerError, "INTERNAL_ERROR", "failed to update campaign")
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{"status": "updated"})
}

func (h *CampaignAdminHandler) Delete(w http.ResponseWriter, r *http.Request) {
	campaignID := r.PathValue("id")
	_, err := h.pool.Exec(r.Context(),
		`UPDATE campaigns SET status = 'ended', updated_at = now() WHERE id = $1`, campaignID)
	if err != nil {
		handler.WriteError(w, http.StatusInternalServerError, "INTERNAL_ERROR", "failed to end campaign")
		return
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{"status": "ended"})
}

func valOrEmpty(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}
