package admin

import (
	"context"
	"encoding/json"
	"log/slog"
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

type pushSendRequest struct {
	Title          string `json:"title"`
	Body           string `json:"body"`
	ImageURL       string `json:"imageUrl,omitempty"`
	Data           string `json:"data,omitempty"`
	TargetAudience string `json:"targetAudience,omitempty"`
}

func (h *PushHandler) Send(w http.ResponseWriter, r *http.Request) {
	var req pushSendRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		handler.WriteError(w, http.StatusBadRequest, "INVALID_REQUEST", "invalid request body")
		return
	}

	userID := middleware.UserID(r.Context())

	var notificationID string
	err := h.pool.QueryRow(r.Context(),
		`INSERT INTO push_notifications (title, body, image_url, data, target_audience, status, sent_by)
		 VALUES ($1, $2, $3, $4, $5, 'sending', $6) RETURNING id`,
		req.Title, req.Body, req.ImageURL, req.Data, req.TargetAudience, userID,
	).Scan(&notificationID)
	if err != nil {
		handler.WriteServerError(w, r, "INTERNAL_ERROR", "failed to create notification", err)
		return
	}

	tokens, err := h.resolveTokens(r.Context())
	if err != nil {
		slog.Warn("push send: resolve tokens", "error", err)
		h.pool.Exec(r.Context(),
			`UPDATE push_notifications SET status = 'failed' WHERE id = $1`, notificationID)
		handler.WriteServerError(w, r, "INTERNAL_ERROR", "failed to resolve tokens", err)
		return
	}

	if len(tokens) == 0 {
		h.pool.Exec(r.Context(),
			`UPDATE push_notifications SET status = 'sent', sent_at = now(), recipient_count = 0 WHERE id = $1`,
			notificationID)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		json.NewEncoder(w).Encode(map[string]string{
			"notificationId": notificationID,
			"recipientCount": "0",
		})
		return
	}

	messages := make([]push.ExpoPushMessage, len(tokens))
	for i, tok := range tokens {
		messages[i] = push.ExpoPushMessage{To: tok, Title: req.Title, Body: req.Body, Sound: "default"}
	}

	result, err := h.client.SendMessages(r.Context(), messages)
	if err != nil {
		slog.Error("push send: expo api", "error", err)
		h.pool.Exec(r.Context(),
			`UPDATE push_notifications SET status = 'failed' WHERE id = $1`, notificationID)
		handler.WriteServerError(w, r, "INTERNAL_ERROR", "push delivery failed", err)
		return
	}

	h.pool.Exec(r.Context(),
		`UPDATE push_notifications SET status = 'sent', sent_at = now(), recipient_count = $2 WHERE id = $1`,
		notificationID, result.Sent)

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	json.NewEncoder(w).Encode(map[string]any{
		"notificationId": notificationID,
		"recipientCount": result.Sent,
	})
}

func (h *PushHandler) resolveTokens(ctx context.Context) ([]string, error) {
	return push.ResolveActiveTokens(ctx, h.pool)
}

func (h *PushHandler) Schedule(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Title          string `json:"title"`
		Body           string `json:"body"`
		ImageURL       string `json:"imageUrl,omitempty"`
		Data           string `json:"data,omitempty"`
		TargetAudience string `json:"targetAudience,omitempty"`
		ScheduledAt    string `json:"scheduledAt"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		handler.WriteError(w, http.StatusBadRequest, "INVALID_REQUEST", "invalid request body")
		return
	}

	var notificationID string
	err := h.pool.QueryRow(r.Context(),
		`INSERT INTO push_notifications (title, body, image_url, data, target_audience, status, scheduled_at, sent_by)
		 VALUES ($1, $2, $3, $4, $5, 'scheduled', $6, $7) RETURNING id`,
		req.Title, req.Body, req.ImageURL, req.Data, req.TargetAudience, req.ScheduledAt, middleware.UserID(r.Context()),
	).Scan(&notificationID)
	if err != nil {
		handler.WriteServerError(w, r, "INTERNAL_ERROR", "failed to schedule notification", err)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	json.NewEncoder(w).Encode(map[string]string{"notificationId": notificationID})
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
