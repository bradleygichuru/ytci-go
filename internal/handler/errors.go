package handler

import (
	"context"
	"database/sql"
	"encoding/json"
	"log/slog"
	"net/http"

	"github.com/jackc/pgx/v5/pgxpool"
	chimw "github.com/go-chi/chi/v5/middleware"

	"github.com/bradleygichuru/ytci-go/internal/model"
)

func WriteError(w http.ResponseWriter, statusCode int, code, message string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(statusCode)
	json.NewEncoder(w).Encode(model.ErrorResponse{
		Error: model.ErrorDetail{Code: code, Message: message},
	})
}

// WriteServerError is for genuine internal-failure (500-level) paths: it logs
// the underlying err (with the request id from the chi RequestID middleware)
// before writing the same sanitized client envelope as WriteError. The client
// never sees the err — only the server log does. Use WriteError for 4xx paths
// where there is no diagnostic err to record.
func WriteServerError(w http.ResponseWriter, r *http.Request, code, message string, err error) {
	slog.Error("handler error",
		"code", code,
		"message", message,
		"error", err,
		"request_id", chimw.GetReqID(r.Context()),
	)
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusInternalServerError)
	json.NewEncoder(w).Encode(model.ErrorResponse{
		Error: model.ErrorDetail{Code: code, Message: message},
	})
}

func WriteValidationError(w http.ResponseWriter, errors []model.ValidationError) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusBadRequest)
	json.NewEncoder(w).Encode(model.ValidationErrorResponse{Errors: errors})
}

func NullDate(val string) sql.NullString {
	if val == "" {
		return sql.NullString{Valid: false}
	}
	return sql.NullString{String: val, Valid: true}
}

func AwardBadge(ctx context.Context, pool *pgxpool.Pool, userID string, badgeName *string, badgeIconURL *string, sourceType, sourceID, sourceTitle string) {
	if badgeName == nil || *badgeName == "" {
		return
	}
	bIcon := ""
	if badgeIconURL != nil {
		bIcon = *badgeIconURL
	}
	_, _ = pool.Exec(ctx,
		`INSERT INTO badges (user_id, badge_name, badge_icon_url, source_type, source_id, source_title)
		 VALUES ($1, $2, $3, $4, $5, $6)
		 ON CONFLICT DO NOTHING`,
		userID, *badgeName, bIcon, sourceType, sourceID, sourceTitle)
}
