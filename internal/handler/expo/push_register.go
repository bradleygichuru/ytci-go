package expo

import (
	"encoding/json"
	"net/http"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/bradleygichuru/ytci-go/internal/handler"
	"github.com/bradleygichuru/ytci-go/internal/middleware"
)

type PushRegisterHandler struct {
	pool *pgxpool.Pool
}

func NewPushRegisterHandler(pool *pgxpool.Pool) *PushRegisterHandler {
	return &PushRegisterHandler{pool: pool}
}

type registerRequest struct {
	Token    string `json:"token"`
	Platform string `json:"platform"`
}

func (h *PushRegisterHandler) Register(w http.ResponseWriter, r *http.Request) {
	var req registerRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		handler.WriteError(w, http.StatusBadRequest, "INVALID_REQUEST", "invalid request body")
		return
	}
	if req.Token == "" {
		handler.WriteError(w, http.StatusBadRequest, "INVALID_TOKEN", "token is required")
		return
	}

	userID := middleware.UserID(r.Context())
	_, err := h.pool.Exec(r.Context(),
		`INSERT INTO push_tokens (user_id, token, platform, is_active, last_registered_at)
		 VALUES ($1, $2, $3, true, now())
		 ON CONFLICT (token) DO UPDATE SET is_active = true, platform = EXCLUDED.platform,
		   user_id = EXCLUDED.user_id, last_registered_at = now(), deactivated_at = NULL,
		   deactivation_reason = NULL`,
		userID, req.Token, req.Platform)
	if err != nil {
		handler.WriteServerError(w, r, "INTERNAL_ERROR", "failed to register push token", err)
		return
	}

	w.WriteHeader(http.StatusCreated)
	json.NewEncoder(w).Encode(map[string]string{"status": "registered"})
}
