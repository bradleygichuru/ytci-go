package ocr

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestParseReceiptKenyanSupermarket(t *testing.T) {
	text := `NAIVAS SUPERMARKET
PIN: P051234567X
TEL: 0700 000 000
DATE: 21/11/2026
MAIZE FLOUR 2KG 240.00
MILK 1L 65.00
TOTAL KSH 1,305.00
CASH 1,500.00
CHANGE 195.00`

	got := ParseReceipt(text)
	assert.Equal(t, "NAIVAS SUPERMARKET", got.Merchant)
	assert.Equal(t, "2026-11-21", got.Date)
	assert.Equal(t, "KES", got.Currency)
	assert.InDelta(t, 1305.00, got.Total, 0.001)
	assert.Equal(t, 1.0, got.Confidence)
}

func TestParseReceiptISOdateAndNoDecimals(t *testing.T) {
	text := `MAMA NGINA CAFE
2026-11-02
TEA 100
MANDASI 50
TOTAL 150`

	got := ParseReceipt(text)
	assert.Equal(t, "MAMA NGINA CAFE", got.Merchant)
	assert.Equal(t, "2026-11-02", got.Date)
	assert.Equal(t, "KES", got.Currency)
	assert.InDelta(t, 150.00, got.Total, 0.001)
	assert.Equal(t, 1.0, got.Confidence)
}

func TestParseReceiptWithoutTotal(t *testing.T) {
	got := ParseReceipt("THANK YOU\nPLEASE COME AGAIN")
	assert.Equal(t, 0.0, got.Total)
	assert.Less(t, got.Confidence, 0.5)
}

func TestVisionClientExtract(t *testing.T) {
	var requestBody map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, "/v1/images:annotate", r.URL.Path)
		require.Equal(t, "test-key", r.URL.Query().Get("key"))
		require.NoError(t, json.NewDecoder(r.Body).Decode(&requestBody))
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"responses":[{"fullTextAnnotation":{"text":"KARURA CAFE\nDATE: 21/11/2026\nTOTAL KES 450.00"}}]}`))
	}))
	defer server.Close()

	client := NewClient("test-key", server.URL)
	result, err := client.Extract(context.Background(), []byte("image"), "image/jpeg")
	require.NoError(t, err)
	assert.Equal(t, "KARURA CAFE", result.Merchant)
	assert.Equal(t, "2026-11-21", result.Date)
	assert.Equal(t, "KES", result.Currency)
	assert.InDelta(t, 450.00, result.Total, 0.001)
	assert.NotEmpty(t, requestBody)
}

func TestVisionClientError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusForbidden)
		w.Write([]byte(`{"error":{"message":"bad key"}}`))
	}))
	defer server.Close()

	client := NewClient("test-key", server.URL)
	_, err := client.Extract(context.Background(), []byte("image"), "image/jpeg")
	assert.Error(t, err)
}

func TestClientDisabledWithoutKey(t *testing.T) {
	var client *Client
	_, err := client.Extract(context.Background(), []byte("image"), "image/jpeg")
	assert.ErrorIs(t, err, ErrDisabled)
}
