package expo

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bradleygichuru/ytci-go/internal/db"
	"github.com/bradleygichuru/ytci-go/internal/middleware"
	"github.com/bradleygichuru/ytci-go/internal/ocr"
)

type fakeReceiptR2 struct {
	data []byte
}

func (f *fakeReceiptR2) PresignedPutURL(context.Context, string, time.Duration) (string, error) {
	return "https://upload.test/receipt", nil
}
func (f *fakeReceiptR2) PresignedGetURL(context.Context, string, time.Duration) (string, error) {
	return "https://view.test/receipt", nil
}
func (f *fakeReceiptR2) PutObject(context.Context, string, io.Reader, string) error { return nil }
func (f *fakeReceiptR2) GetObject(context.Context, string) ([]byte, error)          { return f.data, nil }
func (f *fakeReceiptR2) DeleteObject(context.Context, string) error                 { return nil }

type fakeReceiptOCR struct{}

func (fakeReceiptOCR) Extract(context.Context, []byte, string) (ocr.Result, error) {
	return ocr.Result{
		Merchant: "Naivas Supermarket", Date: "2026-11-21", Total: 1305,
		Currency: "KES", Confidence: 1, RawText: "NAIVAS SUPERMARKET\nTOTAL KES 1,305.00",
	}, nil
}

func budgetTestRequest(t *testing.T, method, target, userID string, body any) *http.Request {
	t.Helper()
	var reader io.Reader
	if body != nil {
		raw, err := json.Marshal(body)
		require.NoError(t, err)
		reader = bytes.NewReader(raw)
	}
	req := httptest.NewRequest(method, target, reader)
	ctx := context.WithValue(req.Context(), middleware.CtxUserID, userID)
	return req.WithContext(ctx)
}

func decodeBody(t *testing.T, rec *httptest.ResponseRecorder) map[string]any {
	t.Helper()
	var out map[string]any
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &out))
	return out
}

func TestBudgetLedgerAndReceipts(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}
	ctx := context.Background()
	pool := db.SetupTestDB(t)
	defer pool.Close()

	ownerID := newCollaborationTestUser(t, pool, "Owner")
	editorID := newCollaborationTestUser(t, pool, "Editor")
	viewerID := newCollaborationTestUser(t, pool, "Viewer")
	outsiderID := newCollaborationTestUser(t, pool, "Outsider")

	var itineraryID string
	require.NoError(t, pool.QueryRow(ctx,
		`INSERT INTO itineraries (user_id, title, inputs) VALUES ($1, 'Budget trip', '{}'::jsonb) RETURNING id`,
		ownerID).Scan(&itineraryID))
	require.NoError(t, AddMember(ctx, pool, itineraryID, ownerID, roleOwner, ""))
	require.NoError(t, AddMember(ctx, pool, itineraryID, editorID, roleEditor, ownerID))
	require.NoError(t, AddMember(ctx, pool, itineraryID, viewerID, roleViewer, ownerID))

	participant := func(userID string) string {
		var id string
		require.NoError(t, pool.QueryRow(ctx,
			`SELECT id::text FROM trip_participants WHERE itinerary_id = $1 AND user_id = $2`,
			itineraryID, userID).Scan(&id))
		return id
	}
	ownerP := participant(ownerID)
	editorP := participant(editorID)

	h := NewBudgetHandler(pool, &fakeReceiptR2{data: []byte("receipt-image")}, fakeReceiptOCR{})

	// Budget setup.
	req := budgetTestRequest(t, "PUT", "/v1/mobile/itineraries/"+itineraryID+"/budget", ownerID, map[string]any{
		"totalKes": 100000, "categoryTargets": map[string]int64{"Food": 20000},
		"planEstimate": map[string]int64{"Food": 15000}, "expectedVersion": 1,
	})
	req.SetPathValue("id", itineraryID)
	rec := httptest.NewRecorder()
	h.UpdateBudget(rec, req)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	assert.Equal(t, float64(2), decodeBody(t, rec)["version"])

	req = budgetTestRequest(t, "GET", "/v1/mobile/itineraries/"+itineraryID+"/budget", viewerID, nil)
	req.SetPathValue("id", itineraryID)
	rec = httptest.NewRecorder()
	h.GetBudget(rec, req)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	budget := decodeBody(t, rec)
	assert.Equal(t, float64(100000), budget["totalKes"])
	assert.Equal(t, float64(0), budget["spentKes"])
	assert.Len(t, budget["participants"], 3)

	// Viewer cannot edit; outsider cannot read.
	req = budgetTestRequest(t, "POST", "/v1/mobile/itineraries/"+itineraryID+"/expenses", viewerID, map[string]any{
		"title": "Nope", "category": "Food", "amount": 100, "currency": "KES",
		"payerParticipantId": ownerP, "split": map[string]any{"mode": "equal", "participantIds": []string{ownerP}},
	})
	req.SetPathValue("id", itineraryID)
	rec = httptest.NewRecorder()
	h.CreateExpense(rec, req)
	assert.Equal(t, http.StatusForbidden, rec.Code)

	req = budgetTestRequest(t, "GET", "/v1/mobile/itineraries/"+itineraryID+"/budget", outsiderID, nil)
	req.SetPathValue("id", itineraryID)
	rec = httptest.NewRecorder()
	h.GetBudget(rec, req)
	assert.Equal(t, http.StatusNotFound, rec.Code)

	// Equal split: owner pays 10,000 for owner + editor.
	req = budgetTestRequest(t, "POST", "/v1/mobile/itineraries/"+itineraryID+"/expenses", editorID, map[string]any{
		"title": "Park fees", "category": "Park & entry fees", "amount": 10000,
		"currency": "KES", "fxRate": 1, "date": "2026-11-21", "payerParticipantId": ownerP,
		"split":           map[string]any{"mode": "equal", "participantIds": []string{ownerP, editorP}},
		"expectedVersion": 2,
	})
	req.SetPathValue("id", itineraryID)
	rec = httptest.NewRecorder()
	h.CreateExpense(rec, req)
	require.Equal(t, http.StatusCreated, rec.Code, rec.Body.String())
	created := decodeBody(t, rec)
	assert.Equal(t, float64(3), created["version"])
	expense := created["expense"].(map[string]any)
	expenseID := expense["id"].(string)
	shares := expense["shares"].([]any)
	require.Len(t, shares, 2)
	assert.Equal(t, float64(5000), shares[0].(map[string]any)["amountKes"])
	assert.Equal(t, float64(5000), shares[1].(map[string]any)["amountKes"])

	req = budgetTestRequest(t, "GET", "/v1/mobile/itineraries/"+itineraryID+"/budget", ownerID, nil)
	req.SetPathValue("id", itineraryID)
	rec = httptest.NewRecorder()
	h.GetBudget(rec, req)
	budget = decodeBody(t, rec)
	assert.Equal(t, float64(10000), budget["spentKes"])
	assert.Equal(t, float64(90000), budget["remainingKes"])

	req = budgetTestRequest(t, "GET", "/v1/mobile/itineraries/"+itineraryID+"/settle-up", ownerID, nil)
	req.SetPathValue("id", itineraryID)
	rec = httptest.NewRecorder()
	h.GetSettleUp(rec, req)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	settle := decodeBody(t, rec)
	assert.Len(t, settle["transfers"], 1)
	assert.Equal(t, float64(5000), settle["transfers"].([]any)[0].(map[string]any)["amountKes"])

	// Offline participant.
	req = budgetTestRequest(t, "POST", "/v1/mobile/itineraries/"+itineraryID+"/participants", editorID, map[string]any{"name": "Wanjiku"})
	req.SetPathValue("id", itineraryID)
	rec = httptest.NewRecorder()
	h.CreateParticipant(rec, req)
	require.Equal(t, http.StatusCreated, rec.Code, rec.Body.String())
	offline := decodeBody(t, rec)
	offlineP := offline["id"].(string)

	// Percentage split.
	req = budgetTestRequest(t, "POST", "/v1/mobile/itineraries/"+itineraryID+"/expenses", editorID, map[string]any{
		"title": "Dinner", "category": "Food", "amount": 10000, "currency": "KES",
		"payerParticipantId": editorP,
		"split": map[string]any{"mode": "percentage", "shares": []map[string]any{
			{"participantId": ownerP, "percent": 33.33},
			{"participantId": editorP, "percent": 33.33},
			{"participantId": offlineP, "percent": 33.34},
		}},
		"expectedVersion": 3,
	})
	req.SetPathValue("id", itineraryID)
	rec = httptest.NewRecorder()
	h.CreateExpense(rec, req)
	require.Equal(t, http.StatusCreated, rec.Code, rec.Body.String())
	dinner := decodeBody(t, rec)["expense"].(map[string]any)
	var shareTotal int64
	for _, s := range dinner["shares"].([]any) {
		shareTotal += int64(s.(map[string]any)["amountKes"].(float64))
	}
	assert.Equal(t, int64(10000), shareTotal)

	// Edit expense: scale shares.
	req = budgetTestRequest(t, "PATCH", "/v1/mobile/itineraries/"+itineraryID+"/expenses/"+expenseID, editorID, map[string]any{
		"title": "Park fees (updated)", "amount": 12000, "expectedVersion": 4,
	})
	req.SetPathValue("id", itineraryID)
	req.SetPathValue("expenseId", expenseID)
	rec = httptest.NewRecorder()
	h.UpdateExpense(rec, req)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	updated := decodeBody(t, rec)["expense"].(map[string]any)
	assert.Equal(t, float64(12000), updated["amountKes"])
	for _, s := range updated["shares"].([]any) {
		assert.Equal(t, float64(6000), s.(map[string]any)["amountKes"])
	}

	// Delete and restore.
	req = budgetTestRequest(t, "DELETE", "/v1/mobile/itineraries/"+itineraryID+"/expenses/"+expenseID, editorID, nil)
	req.SetPathValue("id", itineraryID)
	req.SetPathValue("expenseId", expenseID)
	rec = httptest.NewRecorder()
	h.DeleteExpense(rec, req)
	assert.Equal(t, http.StatusOK, rec.Code)

	req = budgetTestRequest(t, "POST", "/v1/mobile/itineraries/"+itineraryID+"/expenses/"+expenseID+"/restore", editorID, nil)
	req.SetPathValue("id", itineraryID)
	req.SetPathValue("expenseId", expenseID)
	rec = httptest.NewRecorder()
	h.RestoreExpense(rec, req)
	assert.Equal(t, http.StatusOK, rec.Code)

	// Settlement.
	req = budgetTestRequest(t, "POST", "/v1/mobile/itineraries/"+itineraryID+"/settlements", editorID, map[string]any{
		"fromParticipantId": editorP, "toParticipantId": ownerP, "amountKes": 5000,
		"date": "2026-11-21", "note": "M-Pesa",
	})
	req.SetPathValue("id", itineraryID)
	rec = httptest.NewRecorder()
	h.CreateSettlement(rec, req)
	require.Equal(t, http.StatusCreated, rec.Code, rec.Body.String())
	settlementID := decodeBody(t, rec)["id"].(string)

	req = budgetTestRequest(t, "GET", "/v1/mobile/itineraries/"+itineraryID+"/settlements", viewerID, nil)
	req.SetPathValue("id", itineraryID)
	rec = httptest.NewRecorder()
	h.ListSettlements(rec, req)
	assert.Equal(t, http.StatusOK, rec.Code)
	assert.Len(t, decodeBody(t, rec)["settlements"], 1)

	req = budgetTestRequest(t, "DELETE", "/v1/mobile/itineraries/"+itineraryID+"/settlements/"+settlementID, editorID, nil)
	req.SetPathValue("id", itineraryID)
	req.SetPathValue("settlementId", settlementID)
	rec = httptest.NewRecorder()
	h.DeleteSettlement(rec, req)
	assert.Equal(t, http.StatusOK, rec.Code)

	// Receipt flow.
	req = budgetTestRequest(t, "POST", "/v1/mobile/itineraries/"+itineraryID+"/expenses/"+expenseID+"/receipt/presign", editorID, map[string]any{
		"contentType": "image/jpeg", "fileSizeBytes": 1000,
	})
	req.SetPathValue("id", itineraryID)
	req.SetPathValue("expenseId", expenseID)
	rec = httptest.NewRecorder()
	h.PresignReceipt(rec, req)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	receiptID := decodeBody(t, rec)["receiptId"].(string)

	req = budgetTestRequest(t, "POST", "/v1/mobile/itineraries/"+itineraryID+"/expenses/"+expenseID+"/receipt/complete", editorID, map[string]any{"receiptId": receiptID})
	req.SetPathValue("id", itineraryID)
	req.SetPathValue("expenseId", expenseID)
	rec = httptest.NewRecorder()
	h.CompleteReceipt(rec, req)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())

	h.processReceipt(receiptID)

	req = budgetTestRequest(t, "GET", "/v1/mobile/itineraries/"+itineraryID+"/expenses/"+expenseID+"/receipt", editorID, nil)
	req.SetPathValue("id", itineraryID)
	req.SetPathValue("expenseId", expenseID)
	rec = httptest.NewRecorder()
	h.GetReceipt(rec, req)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	receipt := decodeBody(t, rec)["receipt"].(map[string]any)
	assert.Equal(t, "ready", receipt["status"])
	assert.Equal(t, "ready", receipt["ocrStatus"])
	assert.Equal(t, "Naivas Supermarket", receipt["merchant"])
	assert.Equal(t, "https://view.test/receipt", receipt["url"])

	req = budgetTestRequest(t, "PATCH", "/v1/mobile/itineraries/"+itineraryID+"/expenses/"+expenseID+"/receipt", editorID, map[string]any{
		"merchant": "Naivas Supermarket (edited)", "total": 1300, "currency": "KES",
	})
	req.SetPathValue("id", itineraryID)
	req.SetPathValue("expenseId", expenseID)
	rec = httptest.NewRecorder()
	h.PatchReceipt(rec, req)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())

	req = budgetTestRequest(t, "GET", "/v1/mobile/itineraries/"+itineraryID+"/expenses/"+expenseID+"/receipt", editorID, nil)
	req.SetPathValue("id", itineraryID)
	req.SetPathValue("expenseId", expenseID)
	rec = httptest.NewRecorder()
	h.GetReceipt(rec, req)
	require.Equal(t, http.StatusOK, rec.Code)
	corrected := decodeBody(t, rec)["receipt"].(map[string]any)
	assert.Equal(t, "Naivas Supermarket (edited)", corrected["merchant"])
	assert.Equal(t, float64(1300), corrected["total"])

	req = budgetTestRequest(t, "GET", "/v1/mobile/itineraries/"+itineraryID+"/expenses/"+expenseID+"/receipt", viewerID, nil)
	req.SetPathValue("id", itineraryID)
	req.SetPathValue("expenseId", expenseID)
	rec = httptest.NewRecorder()
	h.GetReceipt(rec, req)
	require.Equal(t, http.StatusOK, rec.Code)
	viewerReceipt := decodeBody(t, rec)["receipt"].(map[string]any)
	assert.Empty(t, viewerReceipt["url"])

	req = budgetTestRequest(t, "DELETE", "/v1/mobile/itineraries/"+itineraryID+"/expenses/"+expenseID+"/receipt", editorID, nil)
	req.SetPathValue("id", itineraryID)
	req.SetPathValue("expenseId", expenseID)
	rec = httptest.NewRecorder()
	h.DeleteReceipt(rec, req)
	assert.Equal(t, http.StatusOK, rec.Code)
}
