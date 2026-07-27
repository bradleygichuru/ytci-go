package handler

import (
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
