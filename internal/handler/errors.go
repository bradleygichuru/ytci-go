package handler

import (
	"database/sql"
	"encoding/json"
	"net/http"

	"github.com/bradleygichuru/ytci-go/internal/model"
)

func WriteError(w http.ResponseWriter, statusCode int, code, message string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(statusCode)
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
