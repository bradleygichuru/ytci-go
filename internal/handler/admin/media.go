package admin

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/bradleygichuru/ytci-go/internal/handler"
	"github.com/bradleygichuru/ytci-go/internal/middleware"
	"github.com/bradleygichuru/ytci-go/internal/model"
	"github.com/bradleygichuru/ytci-go/internal/r2"
)

type MediaHandler struct {
	pool *pgxpool.Pool
	r2   r2.Store
}

func NewMediaHandler(pool *pgxpool.Pool, r2client r2.Store) *MediaHandler {
	return &MediaHandler{pool: pool, r2: r2client}
}

type presignRequest struct {
	ContentType   string `json:"contentType"`
	FileSizeBytes int    `json:"fileSizeBytes"`
	FileName      string `json:"fileName"`
}

type presignResponse struct {
	UploadURL string `json:"uploadUrl"`
	ObjectKey string `json:"objectKey"`
	ExpiresAt string `json:"expiresAt"`
}

type completeRequest struct {
	ObjectKey    string `json:"objectKey"`
	Caption      string `json:"caption,omitempty"`
	AltText      string `json:"altText,omitempty"`
	Credit       string `json:"credit,omitempty"`
	ThumbnailKey string `json:"thumbnailKey,omitempty"`
}

func isAllowedType(ct string) bool {
	switch ct {
	case "image/jpeg", "image/png", "image/webp", "video/mp4", "application/pdf":
		return true
	}
	return false
}

func (h *MediaHandler) Presign(w http.ResponseWriter, r *http.Request) {
	var req presignRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		handler.WriteError(w, http.StatusBadRequest, "INVALID_REQUEST", "invalid request body")
		return
	}

	if !isAllowedType(req.ContentType) {
		handler.WriteError(w, http.StatusBadRequest, "INVALID_CONTENT_TYPE",
			fmt.Sprintf("content type %q not allowed", req.ContentType))
		return
	}

	maxSize := 100 * 1024 * 1024
	if req.ContentType != "video/mp4" {
		maxSize = 10 * 1024 * 1024
	}
	if req.FileSizeBytes > maxSize {
		handler.WriteError(w, http.StatusBadRequest, "FILE_TOO_LARGE",
			fmt.Sprintf("file size exceeds maximum of %d bytes", maxSize))
		return
	}

	objectKey := fmt.Sprintf("media/%d/%s", time.Now().Unix(), req.FileName)

	userID := middleware.UserID(r.Context())
	if h.pool != nil {
		_, err := h.pool.Exec(r.Context(),
			`INSERT INTO pending_media_uploads (object_key, user_id, content_type, file_size)
			 VALUES ($1, $2, $3, $4)
			 ON CONFLICT (object_key) DO UPDATE SET
			 user_id = EXCLUDED.user_id,
			 content_type = EXCLUDED.content_type,
			 file_size = EXCLUDED.file_size,
			 expires_at = EXCLUDED.expires_at`,
			objectKey, userID, req.ContentType, req.FileSizeBytes)
		if err != nil {
			slog.Warn("failed to insert pending media record", "error", err)
		}
	}

	uploadURL, err := h.r2.PresignedPutURL(r.Context(), objectKey, 5*time.Minute)
	if err != nil {
		handler.WriteError(w, http.StatusServiceUnavailable, "STORAGE_UNAVAILABLE", "failed to generate upload URL")
		return
	}

	resp := presignResponse{
		UploadURL: uploadURL,
		ObjectKey: objectKey,
		ExpiresAt: time.Now().Add(5 * time.Minute).Format(time.RFC3339),
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(resp)
}

func (h *MediaHandler) Complete(w http.ResponseWriter, r *http.Request) {
	var req completeRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		handler.WriteError(w, http.StatusBadRequest, "INVALID_REQUEST", "invalid request body")
		return
	}

	userID := middleware.UserID(r.Context())

	if h.pool != nil {
		var pendingCount int
		err := h.pool.QueryRow(r.Context(),
			`SELECT COUNT(*) FROM pending_media_uploads
			 WHERE object_key = $1 AND user_id = $2 AND expires_at > now() AND NOT uploaded`,
			req.ObjectKey, userID).Scan(&pendingCount)
		if err != nil || pendingCount == 0 {
			handler.WriteError(w, http.StatusForbidden, "FORBIDDEN", "media upload not authorized or expired")
			return
		}
		h.pool.Exec(r.Context(),
			`DELETE FROM pending_media_uploads WHERE object_key = $1 AND user_id = $2`,
			req.ObjectKey, userID)
	}

	status := "pending_review"
	role := middleware.RoleFromCtx(r.Context())
	if role == "super_admin" || role == "administrator" || role == "moderator" {
		status = "ready"
	}

	row := h.pool.QueryRow(r.Context(),
		`INSERT INTO media_assets (object_key, status, uploaded_by, caption, alt_text, credit, thumbnail_key, type)
		 VALUES ($1, $2, $3, $4, $5, $6, $7, 'image') RETURNING id`,
		req.ObjectKey, status, userID, req.Caption, req.AltText, req.Credit, req.ThumbnailKey)

	var id string
	if err := row.Scan(&id); err != nil {
		handler.WriteError(w, http.StatusInternalServerError, "INTERNAL_ERROR", "failed to record media")
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	json.NewEncoder(w).Encode(map[string]string{"id": id, "status": status})
}

func (h *MediaHandler) List(w http.ResponseWriter, r *http.Request) {
	rows, err := h.pool.Query(r.Context(),
		`SELECT id, object_key, type, status, caption, alt_text, credit, file_size_bytes, created_at
		 FROM media_assets ORDER BY created_at DESC LIMIT 50`)
	if err != nil {
		handler.WriteError(w, http.StatusInternalServerError, "INTERNAL_ERROR", "failed to list media")
		return
	}
	defer rows.Close()

	type item struct {
		ID        string  `json:"id"`
		ObjectKey string  `json:"objectKey"`
		Type      string  `json:"type"`
		Status    string  `json:"status"`
		Caption   *string `json:"caption,omitempty"`
		AltText   *string `json:"altText,omitempty"`
		Credit    *string `json:"credit,omitempty"`
		FileSize  *int    `json:"fileSizeBytes,omitempty"`
		CreatedAt string  `json:"createdAt"`
	}
	var items []item
	for rows.Next() {
		var i item
		rows.Scan(&i.ID, &i.ObjectKey, &i.Type, &i.Status, &i.Caption, &i.AltText, &i.Credit, &i.FileSize, &i.CreatedAt)
		items = append(items, i)
	}
	if items == nil {
		items = []item{}
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(model.Paginated[item]{Items: items, HasMore: false})
}

func (h *MediaHandler) UpdateMetadata(w http.ResponseWriter, r *http.Request) {
	mediaID := r.PathValue("id")
	var req struct {
		Caption *string `json:"caption,omitempty"`
		AltText *string `json:"altText,omitempty"`
		Credit  *string `json:"credit,omitempty"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		handler.WriteError(w, http.StatusBadRequest, "INVALID_REQUEST", "invalid request body")
		return
	}

	_, err := h.pool.Exec(r.Context(),
		`UPDATE media_assets SET
		 caption = COALESCE($2::text, caption),
		 alt_text = COALESCE($3::text, alt_text),
		 credit = COALESCE($4::text, credit),
		 updated_at = now()
		 WHERE id = $1`,
		mediaID, req.Caption, req.AltText, req.Credit)
	if err != nil {
		handler.WriteError(w, http.StatusInternalServerError, "INTERNAL_ERROR", "failed to update media")
		return
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{"status": "updated"})
}

func (h *MediaHandler) Delete(w http.ResponseWriter, r *http.Request) {
	mediaID := r.PathValue("id")

	var objectKey string
	err := h.pool.QueryRow(r.Context(),
		`DELETE FROM media_assets WHERE id = $1 RETURNING object_key`, mediaID).Scan(&objectKey)
	if err != nil {
		handler.WriteError(w, http.StatusNotFound, "NOT_FOUND", "media not found")
		return
	}

	if h.r2 != nil && objectKey != "" {
		if err := h.r2.DeleteObject(r.Context(), objectKey); err != nil {
			slog.Warn("r2 delete failed, blob may leak", "object_key", objectKey, "error", err)
		}
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{"status": "deleted"})
}

func (h *MediaHandler) GetURL(w http.ResponseWriter, r *http.Request) {
	objectKey := r.PathValue("id")

	url, err := h.r2.PresignedGetURL(r.Context(), objectKey, 15*time.Minute)
	if err != nil {
		url = "/placeholder-media.png"
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{
		"url":       url,
		"expiresAt": time.Now().Add(15 * time.Minute).Format(time.RFC3339),
	})
}
