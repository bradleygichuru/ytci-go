package expo

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/bradleygichuru/ytci-go/internal/handler"
	"github.com/bradleygichuru/ytci-go/internal/middleware"
	"github.com/bradleygichuru/ytci-go/internal/r2"
)

type AccountHandler struct {
	pool    *pgxpool.Pool
	r2store r2.Store
}

func NewAccountHandler(pool *pgxpool.Pool, r2store r2.Store) *AccountHandler {
	return &AccountHandler{pool: pool, r2store: r2store}
}

type deleteAccountRequest struct {
	Confirm bool `json:"confirm"`
}

type cleanupOp struct {
	sql   string
	label string
}

var deleteOps = []cleanupOp{
	{`DELETE FROM sessions WHERE user_id = $1`, "sessions"},
	{`DELETE FROM accounts WHERE user_id = $1`, "accounts"},
	{`DELETE FROM bucket_list_items WHERE user_id = $1`, "bucket list"},
	{`DELETE FROM push_tokens WHERE user_id = $1`, "push tokens"},
	{`DELETE FROM app_opens WHERE user_id = $1`, "app opens"},
	{`DELETE FROM user_profiles WHERE user_id = $1`, "user profile"},
	{`DELETE FROM story_interactions WHERE user_id = $1`, "interactions"},
	{`DELETE FROM comment_interactions WHERE user_id = $1`, "comment interactions"},
	{`DELETE FROM event_saves WHERE user_id = $1`, "event saves"},
	{`DELETE FROM audit_logs WHERE user_id = $1`, "audit logs"},
	{`DELETE FROM pending_media_uploads WHERE user_id = $1`, "pending uploads"},
}

var anonymizeOps = []cleanupOp{
	{`UPDATE stories SET creator_id = NULL WHERE creator_id = $1`, "stories"},
	{`UPDATE story_comments SET author_id = NULL WHERE author_id = $1`, "comments"},
	{`UPDATE story_reports SET reported_by = NULL WHERE reported_by = $1`, "reports"},
	{`UPDATE report_jobs SET requested_by = NULL WHERE requested_by = $1`, "report jobs"},
	{`UPDATE itineraries SET user_id = NULL WHERE user_id = $1`, "itineraries"},
	{`UPDATE challenge_progress SET user_id = NULL WHERE user_id = $1`, "challenge progress"},
	{`UPDATE course_enrollments SET user_id = NULL WHERE user_id = $1`, "enrollments"},
	{`UPDATE conservation_evidence SET user_id = NULL WHERE user_id = $1`, "conservation evidence"},
}

func (h *AccountHandler) Delete(w http.ResponseWriter, r *http.Request) {
	var req deleteAccountRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		handler.WriteError(w, http.StatusBadRequest, "INVALID_REQUEST", "invalid request body")
		return
	}
	if !req.Confirm {
		handler.WriteError(w, http.StatusBadRequest, "CONFIRMATION_REQUIRED", "set confirm to true to proceed")
		return
	}

	userID := middleware.UserID(r.Context())
	if userID == "" {
		handler.WriteError(w, http.StatusUnauthorized, "UNAUTHORIZED", "authentication required")
		return
	}

	tx, err := h.pool.Begin(r.Context())
	if err != nil {
		handler.WriteError(w, http.StatusInternalServerError, "INTERNAL_ERROR", "failed to begin transaction")
		return
	}
	defer tx.Rollback(r.Context())

	pendingKeys := h.collectPendingMediaKeys(r.Context(), tx, userID)

	if !h.execCleanupOps(r.Context(), tx, w, "delete", deleteOps, userID) {
		return
	}
	if !h.execCleanupOps(r.Context(), tx, w, "anonymize", anonymizeOps, userID) {
		return
	}

	if _, err := tx.Exec(r.Context(), `DELETE FROM users WHERE id = $1`, userID); err != nil {
		handler.WriteError(w, http.StatusInternalServerError, "INTERNAL_ERROR", "failed to delete user")
		return
	}

	if err := tx.Commit(r.Context()); err != nil {
		handler.WriteError(w, http.StatusInternalServerError, "INTERNAL_ERROR", "failed to commit transaction")
		return
	}

	if h.r2store != nil && len(pendingKeys) > 0 {
		go h.cleanupR2(context.Background(), pendingKeys)
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(map[string]string{"status": "deleted"})
}

func (h *AccountHandler) execCleanupOps(ctx context.Context, tx pgx.Tx, w http.ResponseWriter, verb string, ops []cleanupOp, userID string) bool {
	for _, op := range ops {
		if _, err := tx.Exec(ctx, op.sql, userID); err != nil {
			handler.WriteError(w, http.StatusInternalServerError, "INTERNAL_ERROR",
				fmt.Sprintf("failed to %s %s", verb, op.label))
			return false
		}
	}
	return true
}

func (h *AccountHandler) collectPendingMediaKeys(ctx context.Context, tx pgx.Tx, userID string) []string {
	if h.r2store == nil {
		return nil
	}
	rows, err := tx.Query(ctx, `SELECT object_key FROM pending_media_uploads WHERE user_id = $1`, userID)
	if err != nil {
		slog.Warn("failed to query pending media uploads", "user_id", userID, "error", err)
		return nil
	}
	defer rows.Close()

	var keys []string
	for rows.Next() {
		var key string
		if err := rows.Scan(&key); err != nil {
			slog.Warn("failed to scan pending media key", "user_id", userID, "error", err)
			continue
		}
		keys = append(keys, key)
	}
	if err := rows.Err(); err != nil {
		slog.Warn("error iterating pending media uploads", "user_id", userID, "error", err)
	}
	return keys
}

func (h *AccountHandler) cleanupR2(ctx context.Context, keys []string) {
	for _, key := range keys {
		if err := h.r2store.DeleteObject(ctx, key); err != nil {
			slog.Warn("failed to delete pending media from R2", "object_key", key, "error", err)
		}
	}
}
