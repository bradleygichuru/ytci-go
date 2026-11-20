package repository

import (
	"context"
	"errors"
	"fmt"
	"mime"
	"net/http"
	"path"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/bradleygichuru/ytci-go/internal/handler"
	"github.com/bradleygichuru/ytci-go/internal/r2"
)

var (
	errIntentExpired = errors.New("upload intent expired or already used")
	errIntentKind    = errors.New("upload intent kind mismatch")
	errUploadMissing = errors.New("uploaded object not found")
	errUploadType    = errors.New("uploaded object content type does not match the intent")
	errUploadSize    = errors.New("uploaded object size does not match the intent")
)

type uploadIntent struct {
	ID           string
	ObjectKey    string
	ContentType  string
	ExpectedSize int64
	Kind         string
}

// validatePresign checks the declared upload against the strict per-kind
// allowlist and size ceiling.
func validatePresign(req presignRequest) (int64, *validationCollector) {
	var v validationCollector
	kind := strings.TrimSpace(req.Kind)
	contentType := strings.TrimSpace(strings.ToLower(req.ContentType))

	switch kind {
	case "image":
		if !imageTypes[contentType] {
			v.add("contentType", "image uploads must be image/jpeg, image/png or image/webp")
		}
		if req.FileSizeBytes <= 0 {
			v.add("fileSizeBytes", "fileSizeBytes is required")
		} else if req.FileSizeBytes > maxImageBytes {
			v.add("fileSizeBytes", "image exceeds the 8 MB limit")
		}
	case "document":
		if !documentTypes[contentType] {
			v.add("contentType", "document uploads must be application/pdf")
		}
		if req.FileSizeBytes <= 0 {
			v.add("fileSizeBytes", "fileSizeBytes is required")
		} else if req.FileSizeBytes > maxDocumentBytes {
			v.add("fileSizeBytes", "document exceeds the 15 MB limit")
		}
	default:
		v.add("kind", "kind must be image or document")
	}
	if strings.TrimSpace(req.FileName) == "" {
		v.add("fileName", "fileName is required")
	}

	max := int64(maxImageBytes)
	if kind == "document" {
		max = maxDocumentBytes
	}
	return max, &v
}

func badPresign(w http.ResponseWriter, v *validationCollector) {
	if v.empty() {
		return
	}
	first := v.errs[0]
	switch {
	case first.Field == "contentType":
		handler.WriteError(w, http.StatusBadRequest, "INVALID_CONTENT_TYPE", first.Message)
	case strings.Contains(first.Message, "exceeds the"):
		handler.WriteError(w, http.StatusBadRequest, "FILE_TOO_LARGE", first.Message)
	case len(v.errs) > 1:
		handler.WriteValidationError(w, v.errs)
	default:
		handler.WriteError(w, http.StatusBadRequest, "VALIDATION_ERROR", first.Message)
	}
}

// presignObject signs a PUT bound to the declared content type when the store
// supports it.
func presignObject(ctx context.Context, store r2.Store, key, contentType string, ttl time.Duration) (string, error) {
	if p, ok := store.(r2.ContentTypePresigner); ok {
		return p.PresignedPutURLWithContentType(ctx, key, contentType, ttl)
	}
	return store.PresignedPutURL(ctx, key, ttl)
}

// PublicPresign creates a one-use anonymous upload intent.
func (h *Handler) PublicPresign(w http.ResponseWriter, r *http.Request) {
	var req presignRequest
	if !decodeBody(w, r, 8*1024, &req) {
		return
	}
	max, v := validatePresign(req)
	if !v.empty() {
		badPresign(w, v)
		return
	}
	if h.r2 == nil {
		handler.WriteError(w, http.StatusServiceUnavailable, "STORAGE_UNAVAILABLE", "media storage is not configured")
		return
	}

	kind := strings.TrimSpace(req.Kind)
	contentType := strings.ToLower(strings.TrimSpace(req.ContentType))
	objectKey := fmt.Sprintf("repository/submissions/%s/%s", uuid.NewString(), safeFileName(req.FileName))
	expiresAt := time.Now().Add(intentTTL)

	if _, err := h.pool.Exec(r.Context(), `
INSERT INTO repository_upload_intents (object_key, content_type, expected_size_bytes, kind, expires_at)
VALUES ($1, $2, $3, $4, $5)`,
		objectKey, contentType, req.FileSizeBytes, kind, expiresAt); err != nil {
		handler.WriteServerError(w, r, "INTERNAL_ERROR", "failed to create upload intent", err)
		return
	}

	uploadURL, err := presignObject(r.Context(), h.r2, objectKey, contentType, intentTTL)
	if err != nil {
		handler.WriteServerError(w, r, "INTERNAL_ERROR", "failed to generate upload URL", err)
		return
	}

	writeJSON(w, http.StatusCreated, presignResponse{
		UploadURL:    uploadURL,
		ObjectKey:    objectKey,
		ExpiresAt:    formatTime(expiresAt),
		MaxSizeBytes: max,
	})
}

// AdminPresign is the authenticated variant. It also records a ready media
// asset so an entry can reference it via coverMediaId/documentMediaId right
// after the browser PUT completes.
func (h *Handler) AdminPresign(w http.ResponseWriter, r *http.Request) {
	var req presignRequest
	if !decodeBody(w, r, 8*1024, &req) {
		return
	}
	max, v := validatePresign(req)
	if !v.empty() {
		badPresign(w, v)
		return
	}
	if h.r2 == nil {
		handler.WriteError(w, http.StatusServiceUnavailable, "STORAGE_UNAVAILABLE", "media storage is not configured")
		return
	}

	kind := strings.TrimSpace(req.Kind)
	contentType := strings.ToLower(strings.TrimSpace(req.ContentType))
	fileName := safeFileName(req.FileName)
	objectKey := fmt.Sprintf("repository/admin/%s/%s", uuid.NewString(), fileName)
	expiresAt := time.Now().Add(intentTTL)

	mediaType := "image"
	if kind == "document" {
		mediaType = "pdf"
	}

	var mediaID string
	err := h.pool.QueryRow(r.Context(), `
INSERT INTO media_assets (object_key, status, type, original_name, file_size_bytes, entity_type)
VALUES ($1, 'ready', $2, $3, $4, 'repository_entry')
RETURNING id::text`,
		objectKey, mediaType, fileName, req.FileSizeBytes).Scan(&mediaID)
	if err != nil {
		handler.WriteServerError(w, r, "INTERNAL_ERROR", "failed to record media", err)
		return
	}

	uploadURL, err := presignObject(r.Context(), h.r2, objectKey, contentType, intentTTL)
	if err != nil {
		handler.WriteServerError(w, r, "INTERNAL_ERROR", "failed to generate upload URL", err)
		return
	}

	writeJSON(w, http.StatusCreated, presignResponse{
		UploadURL:    uploadURL,
		ObjectKey:    objectKey,
		ExpiresAt:    formatTime(expiresAt),
		MaxSizeBytes: max,
		MediaID:      mediaID,
	})
}

// verifyIntent locks one unconsumed intent and HEAD-checks the uploaded
// object. The caller owns the transaction so consumption is atomic with the
// entry insert.
func (h *Handler) verifyIntent(ctx context.Context, tx pgx.Tx, key, wantKind string) (uploadIntent, error) {
	var intent uploadIntent
	err := tx.QueryRow(ctx, `
SELECT id::text, object_key, content_type, expected_size_bytes, kind
FROM repository_upload_intents
WHERE object_key = $1 AND consumed_at IS NULL AND expires_at > now()
FOR UPDATE`, key).
		Scan(&intent.ID, &intent.ObjectKey, &intent.ContentType, &intent.ExpectedSize, &intent.Kind)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return uploadIntent{}, errIntentExpired
		}
		return uploadIntent{}, err
	}
	if intent.Kind != wantKind {
		return uploadIntent{}, errIntentKind
	}
	if h.r2 == nil {
		return uploadIntent{}, errors.New("storage unavailable")
	}

	info, err := h.r2.HeadObject(ctx, key)
	if err != nil {
		return uploadIntent{}, errUploadMissing
	}
	actualType := strings.ToLower(info.ContentType)
	if mt, _, err := mime.ParseMediaType(actualType); err == nil {
		actualType = mt
	}
	if actualType != intent.ContentType {
		h.cleanupObject(ctx, key)
		return uploadIntent{}, errUploadType
	}
	if info.Size != intent.ExpectedSize {
		h.cleanupObject(ctx, key)
		return uploadIntent{}, errUploadSize
	}
	return intent, nil
}

func (h *Handler) cleanupObject(ctx context.Context, key string) {
	if h.r2 == nil {
		return
	}
	_ = h.r2.DeleteObject(ctx, key)
}

func writeUploadError(w http.ResponseWriter, err error) bool {
	switch {
	case errors.Is(err, errIntentExpired):
		handler.WriteError(w, http.StatusConflict, "UPLOAD_INTENT_EXPIRED", "upload intent is expired or already used")
		return true
	case errors.Is(err, errIntentKind):
		handler.WriteError(w, http.StatusBadRequest, "VALIDATION_ERROR", "upload intent kind does not match the field")
		return true
	case errors.Is(err, errUploadMissing):
		handler.WriteError(w, http.StatusConflict, "UPLOAD_MISMATCH", "uploaded object was not found")
		return true
	case errors.Is(err, errUploadType):
		handler.WriteError(w, http.StatusConflict, "UPLOAD_MISMATCH", "uploaded object content type does not match the intent")
		return true
	case errors.Is(err, errUploadSize):
		handler.WriteError(w, http.StatusConflict, "UPLOAD_MISMATCH", "uploaded object size does not match the intent")
		return true
	}
	return false
}

func baseName(key string) string {
	name := path.Base(strings.TrimSpace(key))
	if name == "." || name == "/" || name == "" {
		return "upload"
	}
	return name
}
