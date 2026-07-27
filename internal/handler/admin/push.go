package admin

import (
	"encoding/json"
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

	now := time.Now()
	userID := middleware.UserID(r.Context())

	var notificationID string
	err := h.pool.QueryRow(r.Context(),
		`INSERT INTO push_notifications (title, body, image_url, data, target_audience, status, sent_at, sent_by)
		 VALUES ($1, $2, $3, $4, $5, 'sent', $6, $7) RETURNING id`,
		req.Title, req.Body, req.ImageURL, req.Data, req.TargetAudience, now, userID,
	).Scan(&notificationID)
	if err != nil {
		handler.WriteError(w, http.StatusInternalServerError, "INTERNAL_ERROR", "failed to create notification")
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	json.NewEncoder(w).Encode(map[string]string{
		"notificationId": notificationID,
		"recipientCount": "0",
	})
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
		handler.WriteError(w, http.StatusInternalServerError, "INTERNAL_ERROR", "failed to schedule notification")
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	json.NewEncoder(w).Encode(map[string]string{"notificationId": notificationID})
}

func (h *PushHandler) History(w http.ResponseWriter, r *http.Request) {
	resp := model.Paginated[json.RawMessage]{
		Items:   []json.RawMessage{},
		HasMore: false,
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(resp)
}

func (h *PushHandler) HistoryDetail(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{"id": r.PathValue("id"), "status": "sent"})
}
