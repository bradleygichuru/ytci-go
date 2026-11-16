package admin

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/bradleygichuru/ytci-go/internal/handler"
	"github.com/bradleygichuru/ytci-go/internal/middleware"
	"github.com/bradleygichuru/ytci-go/internal/model"
	"github.com/bradleygichuru/ytci-go/internal/push"
)

type PushHandler struct {
	pool   *pgxpool.Pool
	client *push.Client
}

func NewPushHandler(pool *pgxpool.Pool, client *push.Client) *PushHandler {
	return &PushHandler{pool: pool, client: client}
}

type pushRequest struct {
	Title          string          `json:"title"`
	Body           string          `json:"body"`
	ImageURL       string          `json:"imageUrl,omitempty"`
	Data           json.RawMessage `json:"data,omitempty"`
	TargetAudience json.RawMessage `json:"targetAudience,omitempty"`
	Audience       json.RawMessage `json:"audience,omitempty"`
	CampaignID     string          `json:"campaignId,omitempty"`
	ScheduledAt    string          `json:"scheduledAt,omitempty"`
}

func jsonFieldString(raw json.RawMessage) string {
	if len(raw) == 0 {
		return ""
	}
	var text string
	if err := json.Unmarshal(raw, &text); err == nil {
		return text
	}
	return string(raw)
}

// audienceToTarget accepts the admin app's {type, value} shape and converts it
// to the push targeting contract. Unsupported types (role) fall back to the
// global Program audience.
func (h *PushHandler) audienceToTarget(ctx context.Context, kind, value string) string {
	target := map[string]any{}
	switch kind {
	case "county":
		if value != "" {
			var gid int
			if err := h.pool.QueryRow(ctx,
				`SELECT gid FROM kenya_counties WHERE lower(adm1_name)=lower($1) LIMIT 1`, value).Scan(&gid); err == nil {
				target["countyGids"] = []int{gid}
			} else {
				// A county that does not exist must not silently become a global send.
				target["countyGids"] = []int{-1}
			}
		}
	case "interest":
		if value != "" {
			target["interests"] = []string{value}
		}
	}
	if len(target) == 0 {
		return ""
	}
	encoded, _ := json.Marshal(target)
	return string(encoded)
}

func (h *PushHandler) normalizeRequest(ctx context.Context, req pushRequest) (dataRaw, targetRaw string) {
	dataRaw = jsonFieldString(req.Data)
	targetRaw = jsonFieldString(req.TargetAudience)
	if len(req.Audience) > 0 {
		var audience struct {
			Type  string `json:"type"`
			Value string `json:"value"`
		}
		if err := json.Unmarshal(req.Audience, &audience); err == nil {
			targetRaw = h.audienceToTarget(ctx, audience.Type, audience.Value)
		}
	}
	if dataRaw == "" && req.CampaignID != "" {
		dataRaw = fmt.Sprintf(`{"route":"/campaigns/%s"}`, req.CampaignID)
	}
	return dataRaw, targetRaw
}

func (h *PushHandler) Send(w http.ResponseWriter, r *http.Request) {
	var req pushRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		handler.WriteError(w, http.StatusBadRequest, "INVALID_REQUEST", "invalid request body")
		return
	}

	userID := middleware.UserID(r.Context())
	dataRaw, targetRaw := h.normalizeRequest(r.Context(), req)

	var notificationID string
	err := h.pool.QueryRow(r.Context(),
		`INSERT INTO push_notifications (title, body, image_url, data, target_audience, campaign_id, status, sent_by)
		 VALUES ($1, $2, $3, NULLIF($4,''), NULLIF($5,''), NULLIF($6,'')::uuid, 'sending', NULLIF($7,''))
		 RETURNING id`,
		req.Title, req.Body, req.ImageURL, dataRaw, targetRaw, req.CampaignID, userID,
	).Scan(&notificationID)
	if err != nil {
		handler.WriteServerError(w, r, "INTERNAL_ERROR", "failed to create notification", err)
		return
	}

	programUpdate, err := push.UpsertCampaignProgramUpdate(
		r.Context(), h.pool, notificationID, req.Title, req.Body, req.ImageURL,
		dataRaw, targetRaw, userID,
	)
	if err != nil {
		_, _ = h.pool.Exec(r.Context(),
			`UPDATE push_notifications SET status = 'failed' WHERE id = $1`, notificationID)
		handler.WriteServerError(w, r, "INTERNAL_ERROR", "failed to create program update", err)
		return
	}

	sent, err := push.SendProgramUpdate(r.Context(), h.pool, h.client, programUpdate)
	if err != nil {
		_, _ = h.pool.Exec(r.Context(),
			`UPDATE push_notifications SET status = 'failed' WHERE id = $1`, notificationID)
		handler.WriteServerError(w, r, "INTERNAL_ERROR", "push delivery failed", err)
		return
	}

	_, _ = h.pool.Exec(r.Context(),
		`UPDATE push_notifications SET status = 'sent', sent_at = now(), recipient_count = $2 WHERE id = $1`,
		notificationID, sent)

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	_ = json.NewEncoder(w).Encode(map[string]any{
		"notificationId":  notificationID,
		"programUpdateId": programUpdate.ID,
		"recipientCount":  sent,
	})
}

func (h *PushHandler) Schedule(w http.ResponseWriter, r *http.Request) {
	var req pushRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		handler.WriteError(w, http.StatusBadRequest, "INVALID_REQUEST", "invalid request body")
		return
	}
	dataRaw, targetRaw := h.normalizeRequest(r.Context(), req)

	var notificationID string
	err := h.pool.QueryRow(r.Context(),
		`INSERT INTO push_notifications
		   (title, body, image_url, data, target_audience, campaign_id, status, scheduled_at, sent_by)
		 VALUES ($1,$2,$3,NULLIF($4,''),NULLIF($5,''),NULLIF($6,'')::uuid,'scheduled',
		         NULLIF($7,'')::timestamp, NULLIF($8,''))
		 RETURNING id`,
		req.Title, req.Body, req.ImageURL, dataRaw, targetRaw, req.CampaignID, req.ScheduledAt,
		middleware.UserID(r.Context()),
	).Scan(&notificationID)
	if err != nil {
		handler.WriteServerError(w, r, "INTERNAL_ERROR", "failed to schedule notification", err)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	_ = json.NewEncoder(w).Encode(map[string]string{
		"notificationId": notificationID,
		"scheduledAt":    req.ScheduledAt,
	})
}

func (h *PushHandler) History(w http.ResponseWriter, r *http.Request) {
	rows, err := h.pool.Query(r.Context(),
		`SELECT id, title, status, recipient_count, sent_at, created_at FROM push_notifications
		 ORDER BY created_at DESC LIMIT 50`)
	if err != nil {
		handler.WriteServerError(w, r, "INTERNAL_ERROR", "failed to list history", err)
		return
	}
	defer rows.Close()

	type item struct {
		ID             string  `json:"id"`
		Title          string  `json:"title"`
		Status         string  `json:"status"`
		RecipientCount *int    `json:"recipientCount"`
		SentAt         *string `json:"sentAt,omitempty"`
		CreatedAt      string  `json:"createdAt"`
	}
	var items []item
	for rows.Next() {
		var i item
		var sentAt *time.Time
		var createdAt time.Time
		if err := rows.Scan(&i.ID, &i.Title, &i.Status, &i.RecipientCount, &sentAt, &createdAt); err != nil {
			continue
		}
		i.CreatedAt = createdAt.Format(time.RFC3339)
		if sentAt != nil {
			s := sentAt.Format(time.RFC3339)
			i.SentAt = &s
		}
		items = append(items, i)
	}
	if items == nil {
		items = []item{}
	}

	resp := model.Paginated[item]{
		Items:   items,
		HasMore: false,
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(resp)
}

func (h *PushHandler) HistoryDetail(w http.ResponseWriter, r *http.Request) {
	notificationID := r.PathValue("id")

	var status string
	var title string
	var sentAt *time.Time
	var recipientCount int

	err := h.pool.QueryRow(r.Context(),
		`SELECT title, status, sent_at, COALESCE(recipient_count, 0) FROM push_notifications WHERE id = $1`,
		notificationID).Scan(&title, &status, &sentAt, &recipientCount)
	if err != nil {
		handler.WriteError(w, http.StatusNotFound, "NOT_FOUND", "push notification not found")
		return
	}

	resp := map[string]any{
		"id":             notificationID,
		"title":          title,
		"status":         status,
		"recipientCount": recipientCount,
	}
	if sentAt != nil {
		resp["sentAt"] = sentAt.Format(time.RFC3339)
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(resp)
}
