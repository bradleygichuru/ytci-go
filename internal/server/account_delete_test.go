package server_test

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bradleygichuru/ytci-go/internal/handler/expo"
)

func getErrorCode(t *testing.T, body []byte) string {
	t.Helper()
	var result struct {
		Error struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	err := json.Unmarshal(body, &result)
	require.NoError(t, err)
	return result.Error.Code
}

func TestAccountDeleteInputValidation(t *testing.T) {
	tests := []struct {
		name       string
		body       []byte
		wantStatus int
		wantCode   string
	}{
		{
			name:       "confirm false returns 400",
			body:       mustMarshal(t, map[string]bool{"confirm": false}),
			wantStatus: http.StatusBadRequest,
			wantCode:   "CONFIRMATION_REQUIRED",
		},
		{
			name:       "empty body returns 400",
			body:       []byte(`{}`),
			wantStatus: http.StatusBadRequest,
			wantCode:   "CONFIRMATION_REQUIRED",
		},
		{
			name:       "invalid json returns 400",
			body:       []byte(`not json`),
			wantStatus: http.StatusBadRequest,
			wantCode:   "INVALID_REQUEST",
		},
	}

	h := expo.NewAccountHandler(nil, nil)

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			w := httptest.NewRecorder()
			req := httptest.NewRequest(http.MethodPost, "/account/delete", bytes.NewReader(tt.body))
			req.Header.Set("Content-Type", "application/json")

			h.Delete(w, req)

			resp := w.Result()
			assert.Equal(t, tt.wantStatus, resp.StatusCode)

			if tt.wantCode != "" {
				assert.Equal(t, tt.wantCode, getErrorCode(t, w.Body.Bytes()))
			}
		})
	}
}

func TestAccountDeleteUnauthorized(t *testing.T) {
	h := expo.NewAccountHandler(nil, nil)
	w := httptest.NewRecorder()
	body, _ := json.Marshal(map[string]bool{"confirm": true})
	req := httptest.NewRequest(http.MethodPost, "/account/delete", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")

	h.Delete(w, req)

	resp := w.Result()
	assert.Equal(t, http.StatusUnauthorized, resp.StatusCode)
	assert.Equal(t, "UNAUTHORIZED", getErrorCode(t, w.Body.Bytes()))
}

func mustMarshal(t *testing.T, v any) []byte {
	t.Helper()
	b, err := json.Marshal(v)
	require.NoError(t, err)
	return b
}
