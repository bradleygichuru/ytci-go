package expo

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/bradleygichuru/ytci-go/internal/handler"
	"github.com/bradleygichuru/ytci-go/internal/middleware"
)

var receiptContentTypes = map[string]string{
	"image/jpeg": ".jpg",
	"image/png":  ".png",
	"image/webp": ".webp",
}

func (h *BudgetHandler) PresignReceipt(w http.ResponseWriter, r *http.Request) {
	itineraryID := r.PathValue("id")
	expenseID := r.PathValue("expenseId")
	if itineraryID == "" || expenseID == "" {
		handler.WriteError(w, http.StatusBadRequest, "INVALID_ID", "itinerary and expense ids are required")
		return
	}
	if h.r2 == nil {
		handler.WriteError(w, http.StatusServiceUnavailable, "STORAGE_UNAVAILABLE", "receipt storage is not configured")
		return
	}

	var req struct {
		ContentType   string `json:"contentType"`
		FileSizeBytes int    `json:"fileSizeBytes"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		handler.WriteError(w, http.StatusBadRequest, "INVALID_REQUEST", "invalid request body")
		return
	}
	ext, ok := receiptContentTypes[req.ContentType]
	if !ok {
		handler.WriteError(w, http.StatusBadRequest, "INVALID_CONTENT_TYPE", "receipt must be JPEG, PNG, or WebP")
		return
	}
	if req.FileSizeBytes <= 0 || req.FileSizeBytes > 10*1024*1024 {
		handler.WriteError(w, http.StatusBadRequest, "INVALID_FILE_SIZE", "receipt must be 10 MB or smaller")
		return
	}

	userID := middleware.UserID(r.Context())
	tx, err := h.pool.Begin(r.Context())
	if err != nil {
		handler.WriteServerError(w, r, "INTERNAL_ERROR", "failed to prepare receipt upload", err)
		return
	}
	defer tx.Rollback(r.Context())

	if _, err := requireItineraryRole(r.Context(), tx, itineraryID, userID, roleEditor); err != nil {
		writeBudgetError(w, r, err)
		return
	}
	var deleted bool
	err = tx.QueryRow(r.Context(),
		`SELECT (deleted_at IS NOT NULL) FROM expenses WHERE id = $1 AND itinerary_id = $2`,
		expenseID, itineraryID).Scan(&deleted)
	if errors.Is(err, pgx.ErrNoRows) || deleted {
		handler.WriteError(w, http.StatusNotFound, "NOT_FOUND", "expense not found")
		return
	}
	if err != nil {
		handler.WriteServerError(w, r, "INTERNAL_ERROR", "failed to load expense", err)
		return
	}

	if _, err := tx.Exec(r.Context(),
		`UPDATE expense_receipts
		 SET status = 'deleted', deleted_at = now(), updated_at = now()
		 WHERE expense_id = $1 AND deleted_at IS NULL`, expenseID); err != nil {
		handler.WriteServerError(w, r, "INTERNAL_ERROR", "failed to replace receipt", err)
		return
	}

	keyBytes := make([]byte, 16)
	if _, err := rand.Read(keyBytes); err != nil {
		handler.WriteServerError(w, r, "INTERNAL_ERROR", "failed to generate receipt key", err)
		return
	}
	objectKey := "receipts/" + itineraryID + "/" + expenseID + "/" + hex.EncodeToString(keyBytes) + ext

	var receiptID string
	err = tx.QueryRow(r.Context(),
		`INSERT INTO expense_receipts
		 (expense_id, itinerary_id, object_key, content_type, file_size_bytes, status, ocr_status, created_by)
		 VALUES ($1,$2,$3,$4,$5,'uploading','pending',$6)
		 RETURNING id`,
		expenseID, itineraryID, objectKey, req.ContentType, req.FileSizeBytes, userID).Scan(&receiptID)
	if err != nil {
		handler.WriteServerError(w, r, "INTERNAL_ERROR", "failed to create receipt", err)
		return
	}

	uploadURL, err := h.r2.PresignedPutURL(r.Context(), objectKey, 5*time.Minute)
	if err != nil {
		handler.WriteServerError(w, r, "INTERNAL_ERROR", "failed to sign receipt upload", err)
		return
	}
	if err := tx.Commit(r.Context()); err != nil {
		handler.WriteServerError(w, r, "INTERNAL_ERROR", "failed to commit receipt", err)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]any{
		"receiptId": receiptID,
		"uploadUrl": uploadURL,
		"expiresAt": time.Now().Add(5 * time.Minute).Format(time.RFC3339),
	})
}

func (h *BudgetHandler) CompleteReceipt(w http.ResponseWriter, r *http.Request) {
	itineraryID := r.PathValue("id")
	expenseID := r.PathValue("expenseId")
	if itineraryID == "" || expenseID == "" {
		handler.WriteError(w, http.StatusBadRequest, "INVALID_ID", "itinerary and expense ids are required")
		return
	}
	var req struct {
		ReceiptID string `json:"receiptId"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.ReceiptID == "" {
		handler.WriteError(w, http.StatusBadRequest, "INVALID_REQUEST", "receiptId is required")
		return
	}

	userID := middleware.UserID(r.Context())
	tx, err := h.pool.Begin(r.Context())
	if err != nil {
		handler.WriteServerError(w, r, "INTERNAL_ERROR", "failed to complete receipt", err)
		return
	}
	defer tx.Rollback(r.Context())

	if _, err := requireItineraryRole(r.Context(), tx, itineraryID, userID, roleEditor); err != nil {
		writeBudgetError(w, r, err)
		return
	}
	cmd, err := tx.Exec(r.Context(),
		`UPDATE expense_receipts
		 SET status = 'processing', ocr_status = 'pending', updated_at = now()
		 WHERE id = $1 AND expense_id = $2 AND itinerary_id = $3
		   AND status = 'uploading' AND deleted_at IS NULL AND created_by = $4`,
		req.ReceiptID, expenseID, itineraryID, userID)
	if err != nil {
		handler.WriteServerError(w, r, "INTERNAL_ERROR", "failed to complete receipt", err)
		return
	}
	if cmd.RowsAffected() == 0 {
		handler.WriteError(w, http.StatusNotFound, "NOT_FOUND", "receipt upload not found")
		return
	}
	newVersion, err := bumpBudgetVersion(r.Context(), tx, itineraryID)
	if err != nil {
		writeBudgetError(w, r, err)
		return
	}
	if err := recordBudgetEvent(r.Context(), tx, itineraryID, userID, "receipt_attached",
		"receipt", req.ReceiptID, "Attached a receipt", nil, nil); err != nil {
		handler.WriteServerError(w, r, "INTERNAL_ERROR", "failed to record receipt event", err)
		return
	}
	if err := tx.Commit(r.Context()); err != nil {
		handler.WriteServerError(w, r, "INTERNAL_ERROR", "failed to commit receipt", err)
		return
	}

	go h.processReceipt(req.ReceiptID)
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]any{"status": "processing", "version": newVersion})
}

func (h *BudgetHandler) GetReceipt(w http.ResponseWriter, r *http.Request) {
	itineraryID := r.PathValue("id")
	expenseID := r.PathValue("expenseId")
	if itineraryID == "" || expenseID == "" {
		handler.WriteError(w, http.StatusBadRequest, "INVALID_ID", "itinerary and expense ids are required")
		return
	}
	access, err := requireItineraryRole(r.Context(), h.pool, itineraryID, middleware.UserID(r.Context()), roleViewer)
	if err != nil {
		writeBudgetError(w, r, err)
		return
	}

	var (
		id, status, ocrStatus, merchant, date, currency, objectKey string
		total, confidence                                          float64
	)
	err = h.pool.QueryRow(r.Context(),
		`SELECT id::text, status, ocr_status, COALESCE(ocr_merchant, ''), COALESCE(ocr_date::text, ''),
		        COALESCE(ocr_total, 0)::float8, COALESCE(ocr_currency, ''),
		        COALESCE(ocr_confidence, 0)::float8, object_key
		 FROM expense_receipts
		 WHERE expense_id = $1 AND itinerary_id = $2 AND deleted_at IS NULL
		 ORDER BY created_at DESC LIMIT 1`,
		expenseID, itineraryID,
	).Scan(&id, &status, &ocrStatus, &merchant, &date, &total, &currency, &confidence, &objectKey)
	if errors.Is(err, pgx.ErrNoRows) {
		handler.WriteError(w, http.StatusNotFound, "NOT_FOUND", "receipt not found")
		return
	}
	if err != nil {
		handler.WriteServerError(w, r, "INTERNAL_ERROR", "failed to load receipt", err)
		return
	}

	meta := receiptMeta{
		ID: id, Status: status, OCRStatus: ocrStatus, Merchant: merchant,
		Date: date, Currency: currency,
	}
	if total > 0 {
		meta.Total = &total
	}
	if confidence > 0 {
		meta.Confidence = &confidence
	}
	if roleRank(access.Role) >= roleRank(roleEditor) {
		meta.URL = receiptURL(r.Context(), h.r2, objectKey)
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]any{"receipt": meta})
}

func (h *BudgetHandler) PatchReceipt(w http.ResponseWriter, r *http.Request) {
	itineraryID := r.PathValue("id")
	expenseID := r.PathValue("expenseId")
	if itineraryID == "" || expenseID == "" {
		handler.WriteError(w, http.StatusBadRequest, "INVALID_ID", "itinerary and expense ids are required")
		return
	}
	var req struct {
		Merchant *string  `json:"merchant"`
		Date     *string  `json:"date"`
		Total    *float64 `json:"total"`
		Currency *string  `json:"currency"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		handler.WriteError(w, http.StatusBadRequest, "INVALID_REQUEST", "invalid request body")
		return
	}
	if req.Merchant == nil && req.Date == nil && req.Total == nil && req.Currency == nil {
		handler.WriteError(w, http.StatusBadRequest, "INVALID_REQUEST", "no corrections supplied")
		return
	}
	if req.Total != nil && *req.Total < 0 {
		handler.WriteError(w, http.StatusBadRequest, "INVALID_AMOUNT", "total cannot be negative")
		return
	}
	if req.Date != nil && *req.Date != "" {
		if _, err := time.Parse("2006-01-02", *req.Date); err != nil {
			handler.WriteError(w, http.StatusBadRequest, "INVALID_DATE", "date must be YYYY-MM-DD")
			return
		}
	}
	if req.Currency != nil && *req.Currency != "" && !isValidCurrency(strings.ToUpper(*req.Currency)) {
		handler.WriteError(w, http.StatusBadRequest, "INVALID_CURRENCY", "unsupported currency")
		return
	}

	userID := middleware.UserID(r.Context())
	tx, err := h.pool.Begin(r.Context())
	if err != nil {
		handler.WriteServerError(w, r, "INTERNAL_ERROR", "failed to correct receipt", err)
		return
	}
	defer tx.Rollback(r.Context())
	if _, err := requireItineraryRole(r.Context(), tx, itineraryID, userID, roleEditor); err != nil {
		writeBudgetError(w, r, err)
		return
	}
	expected, _ := strconv.Atoi(r.URL.Query().Get("expectedVersion"))
	current, err := lockBudgetVersion(r.Context(), tx, itineraryID, expected)
	if errors.Is(err, errBudgetVersionConflict) {
		tx.Rollback(r.Context())
		writeBudgetVersionConflict(w, current)
		return
	}
	if err != nil {
		writeBudgetError(w, r, err)
		return
	}

	var merchant, date, currency any
	if req.Merchant != nil {
		merchant = *req.Merchant
	}
	if req.Date != nil {
		date = *req.Date
	}
	if req.Currency != nil {
		currency = strings.ToUpper(*req.Currency)
	}
	cmd, err := tx.Exec(r.Context(),
		`UPDATE expense_receipts
		 SET ocr_merchant = COALESCE($3, ocr_merchant),
		     ocr_date = COALESCE(NULLIF($4, '')::date, ocr_date),
		     ocr_total = COALESCE($5, ocr_total),
		     ocr_currency = COALESCE(NULLIF($6, ''), ocr_currency),
		     updated_at = now()
		 WHERE expense_id = $1 AND itinerary_id = $2 AND deleted_at IS NULL`,
		expenseID, itineraryID, merchant, date, req.Total, currency)
	if err != nil {
		handler.WriteServerError(w, r, "INTERNAL_ERROR", "failed to correct receipt", err)
		return
	}
	if cmd.RowsAffected() == 0 {
		handler.WriteError(w, http.StatusNotFound, "NOT_FOUND", "receipt not found")
		return
	}
	newVersion, err := bumpBudgetVersion(r.Context(), tx, itineraryID)
	if err != nil {
		writeBudgetError(w, r, err)
		return
	}
	if err := recordBudgetEvent(r.Context(), tx, itineraryID, userID, "receipt_corrected",
		"receipt", expenseID, "Corrected receipt details", nil, nil); err != nil {
		handler.WriteServerError(w, r, "INTERNAL_ERROR", "failed to record receipt event", err)
		return
	}
	if err := tx.Commit(r.Context()); err != nil {
		handler.WriteServerError(w, r, "INTERNAL_ERROR", "failed to commit receipt", err)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]any{"status": "corrected", "version": newVersion})
}

func (h *BudgetHandler) DeleteReceipt(w http.ResponseWriter, r *http.Request) {
	itineraryID := r.PathValue("id")
	expenseID := r.PathValue("expenseId")
	if itineraryID == "" || expenseID == "" {
		handler.WriteError(w, http.StatusBadRequest, "INVALID_ID", "itinerary and expense ids are required")
		return
	}
	userID := middleware.UserID(r.Context())
	tx, err := h.pool.Begin(r.Context())
	if err != nil {
		handler.WriteServerError(w, r, "INTERNAL_ERROR", "failed to remove receipt", err)
		return
	}
	defer tx.Rollback(r.Context())
	if _, err := requireItineraryRole(r.Context(), tx, itineraryID, userID, roleEditor); err != nil {
		writeBudgetError(w, r, err)
		return
	}
	expected, _ := strconv.Atoi(r.URL.Query().Get("expectedVersion"))
	current, err := lockBudgetVersion(r.Context(), tx, itineraryID, expected)
	if errors.Is(err, errBudgetVersionConflict) {
		tx.Rollback(r.Context())
		writeBudgetVersionConflict(w, current)
		return
	}
	if err != nil {
		writeBudgetError(w, r, err)
		return
	}
	cmd, err := tx.Exec(r.Context(),
		`UPDATE expense_receipts
		 SET status = 'deleted', deleted_at = now(), updated_at = now()
		 WHERE expense_id = $1 AND itinerary_id = $2 AND deleted_at IS NULL`,
		expenseID, itineraryID)
	if err != nil {
		handler.WriteServerError(w, r, "INTERNAL_ERROR", "failed to remove receipt", err)
		return
	}
	if cmd.RowsAffected() == 0 {
		handler.WriteError(w, http.StatusNotFound, "NOT_FOUND", "receipt not found")
		return
	}
	newVersion, err := bumpBudgetVersion(r.Context(), tx, itineraryID)
	if err != nil {
		writeBudgetError(w, r, err)
		return
	}
	if err := recordBudgetEvent(r.Context(), tx, itineraryID, userID, "receipt_deleted",
		"receipt", expenseID, "Removed a receipt", nil, nil); err != nil {
		handler.WriteServerError(w, r, "INTERNAL_ERROR", "failed to record receipt event", err)
		return
	}
	if err := tx.Commit(r.Context()); err != nil {
		handler.WriteServerError(w, r, "INTERNAL_ERROR", "failed to commit receipt", err)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]any{"status": "deleted", "version": newVersion})
}

func (h *BudgetHandler) processReceipt(receiptID string) {
	ctx := context.Background()
	var objectKey, contentType, status string
	if err := h.pool.QueryRow(ctx,
		`SELECT object_key, content_type, status FROM expense_receipts
		 WHERE id = $1 AND deleted_at IS NULL`, receiptID,
	).Scan(&objectKey, &contentType, &status); err != nil {
		return
	}
	if status != "processing" {
		return
	}

	if h.r2 == nil || h.ocr == nil {
		_, _ = h.pool.Exec(ctx,
			`UPDATE expense_receipts
			 SET status = 'ready', ocr_status = 'skipped', ocr_error = 'OCR unavailable',
			     updated_at = now()
			 WHERE id = $1`, receiptID)
		return
	}

	image, err := h.r2.GetObject(ctx, objectKey)
	if err != nil {
		h.failReceipt(ctx, receiptID, "could not read receipt image")
		return
	}
	ocrCtx, cancel := context.WithTimeout(ctx, 45*time.Second)
	defer cancel()
	result, err := h.ocr.Extract(ocrCtx, image, contentType)
	if err != nil {
		h.failReceipt(ctx, receiptID, "could not read receipt text")
		return
	}

	raw := result.RawText
	if len(raw) > 60000 {
		raw = raw[:60000]
	}
	ocrDate := ""
	if result.Date != "" {
		if _, err := time.Parse("2006-01-02", result.Date); err == nil {
			ocrDate = result.Date
		}
	}
	_, _ = h.pool.Exec(ctx,
		`UPDATE expense_receipts
		 SET status = 'ready', ocr_status = 'ready',
		     ocr_merchant = NULLIF($2, ''), ocr_date = NULLIF($3, '')::date, ocr_total = NULLIF($4, 0),
		     ocr_currency = NULLIF($5, ''), ocr_confidence = $6, ocr_text = $7,
		     ocr_error = NULL, updated_at = now()
		 WHERE id = $1`,
		receiptID, strings.TrimSpace(result.Merchant), ocrDate, result.Total,
		result.Currency, result.Confidence, raw)
}

func (h *BudgetHandler) failReceipt(ctx context.Context, receiptID, message string) {
	_, _ = h.pool.Exec(ctx,
		`UPDATE expense_receipts
		 SET status = 'failed', ocr_status = 'failed', ocr_error = $2, updated_at = now()
		 WHERE id = $1`, receiptID, message)
}
