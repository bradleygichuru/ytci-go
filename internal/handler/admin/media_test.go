package admin

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/stretchr/testify/assert"
)

type mockR2Store struct {
	presignedPutURL string
	presignedGetURL string
	putErr          error
	getErr          error
	deleteErr       error
}

func (m *mockR2Store) PresignedPutURL(ctx context.Context, key string, ttl time.Duration) (string, error) {
	if m.putErr != nil {
		return "", m.putErr
	}
	return m.presignedPutURL, nil
}
func (m *mockR2Store) PresignedGetURL(ctx context.Context, key string, ttl time.Duration) (string, error) {
	if m.getErr != nil {
		return "", m.getErr
	}
	return m.presignedGetURL, nil
}
func (m *mockR2Store) PutObject(ctx context.Context, key string, data io.Reader, contentType string) error {
	return nil
}
func (m *mockR2Store) DeleteObject(ctx context.Context, key string) error {
	return m.deleteErr
}

func TestMediaPresignImageTooLarge(t *testing.T) {
	h := NewMediaHandler(nil, &mockR2Store{})

	body, _ := json.Marshal(map[string]any{
		"contentType": "image/jpeg", "fileSizeBytes": 11 * 1024 * 1024, "fileName": "test.jpg",
	})
	req := httptest.NewRequest("POST", "/", bytes.NewReader(body))
	rec := httptest.NewRecorder()
	h.Presign(rec, req)
	assert.Equal(t, http.StatusBadRequest, rec.Code)
}

func TestMediaPresignVideoTooLarge(t *testing.T) {
	h := NewMediaHandler(nil, &mockR2Store{})
	body, _ := json.Marshal(map[string]any{
		"contentType": "video/mp4", "fileSizeBytes": 200 * 1024 * 1024, "fileName": "test.mp4",
	})
	req := httptest.NewRequest("POST", "/", bytes.NewReader(body))
	rec := httptest.NewRecorder()
	h.Presign(rec, req)
	assert.Equal(t, http.StatusBadRequest, rec.Code)
}

func TestMediaPresignWrongContentType(t *testing.T) {
	h := NewMediaHandler(nil, &mockR2Store{})
	body, _ := json.Marshal(map[string]any{
		"contentType": "application/xml", "fileSizeBytes": 1000, "fileName": "test.xml",
	})
	req := httptest.NewRequest("POST", "/", bytes.NewReader(body))
	rec := httptest.NewRecorder()
	h.Presign(rec, req)
	assert.Equal(t, http.StatusBadRequest, rec.Code)
}

func TestMediaPresignValid(t *testing.T) {
	h := NewMediaHandler(nil, &mockR2Store{
		presignedPutURL: "https://r2.example.com/upload/test.jpg",
	})
	body, _ := json.Marshal(map[string]any{
		"contentType": "image/jpeg", "fileSizeBytes": 5 * 1024 * 1024, "fileName": "test.jpg",
	})
	req := httptest.NewRequest("POST", "/", bytes.NewReader(body))
	rec := httptest.NewRecorder()
	h.Presign(rec, req)
	assert.Equal(t, http.StatusOK, rec.Code)
}

func TestMediaGetURLFallback(t *testing.T) {
	h := NewMediaHandler(nil, &mockR2Store{getErr: errors.New("r2 error")})
	r := chi.NewRouter()
	r.Get("/media/{id}/url", h.GetURL)
	ts := httptest.NewServer(r)
	defer ts.Close()

	resp, _ := http.Get(ts.URL + "/media/object123/url")
	var result map[string]string
	json.NewDecoder(resp.Body).Decode(&result)
	assert.Contains(t, result["url"], "placeholder-media.png")
}
