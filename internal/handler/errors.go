package handler

import (
	"database/sql"
	"encoding/json"
	"log/slog"
	"net/http"

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
