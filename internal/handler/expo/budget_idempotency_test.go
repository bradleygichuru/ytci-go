package expo

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bradleygichuru/ytci-go/internal/db"
)

func TestCreateExpenseIdempotent(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}
	ctx := context.Background()
	pool := db.SetupTestDB(t)
	defer pool.Close()

	ownerID := newCollaborationTestUser(t, pool, "Owner")
	itineraryID := newCollaborationTestItinerary(t, pool, ownerID)
	var participantID string
	require.NoError(t, pool.QueryRow(ctx,
		`SELECT id::text FROM trip_participants WHERE itinerary_id = $1 AND user_id = $2`,
		itineraryID, ownerID).Scan(&participantID))

	h := NewBudgetHandler(pool, nil, nil)
	payload := map[string]any{
		"title": "Idempotent lunch", "category": "Food", "amount": 1500,
		"currency": "KES", "date": "2026-11-21", "payerParticipantId": participantID,
		"split":           map[string]any{"mode": "equal", "participantIds": []string{participantID}},
		"clientRequestId": "expense-request-1",
		"expectedVersion": 1,
	}
	create := func() *httptest.ResponseRecorder {
		req := budgetTestRequest(t, "POST", "/v1/mobile/itineraries/"+itineraryID+"/expenses", ownerID, payload)
		req.SetPathValue("id", itineraryID)
		rec := httptest.NewRecorder()
		h.CreateExpense(rec, req)
		return rec
	}

	first := create()
	require.Equal(t, http.StatusCreated, first.Code, first.Body.String())
	// The retry keeps the original expectedVersion, which has since moved on.
	second := create()
	require.Equal(t, http.StatusCreated, second.Code, second.Body.String())

	firstBody := decodeBody(t, first)
	secondBody := decodeBody(t, second)
	firstExpense := firstBody["expense"].(map[string]any)
	secondExpense := secondBody["expense"].(map[string]any)
	assert.Equal(t, firstExpense["id"], secondExpense["id"])
	assert.Equal(t, float64(2), secondBody["version"])

	var count int
	var spent int64
	require.NoError(t, pool.QueryRow(ctx,
		`SELECT COUNT(*), COALESCE(SUM(amount_kes), 0)::bigint
		 FROM expenses WHERE itinerary_id = $1 AND deleted_at IS NULL`,
		itineraryID).Scan(&count, &spent))
	assert.Equal(t, 1, count)
	assert.Equal(t, int64(1500), spent)

	req := budgetTestRequest(t, "GET", "/v1/mobile/itineraries/"+itineraryID+"/budget", ownerID, nil)
	req.SetPathValue("id", itineraryID)
	rec := httptest.NewRecorder()
	h.GetBudget(rec, req)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	assert.Equal(t, float64(1500), decodeBody(t, rec)["spentKes"])
}

func TestPresignReceiptIdempotent(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}
	ctx := context.Background()
	pool := db.SetupTestDB(t)
	defer pool.Close()

	ownerID := newCollaborationTestUser(t, pool, "Owner")
	itineraryID := newCollaborationTestItinerary(t, pool, ownerID)
	var participantID string
	require.NoError(t, pool.QueryRow(ctx,
		`SELECT id::text FROM trip_participants WHERE itinerary_id = $1 AND user_id = $2`,
		itineraryID, ownerID).Scan(&participantID))

	budget := NewBudgetHandler(pool, nil, nil)
	req := budgetTestRequest(t, "POST", "/v1/mobile/itineraries/"+itineraryID+"/expenses", ownerID, map[string]any{
		"title": "Receipt lunch", "category": "Food", "amount": 900,
		"currency": "KES", "date": "2026-11-21", "payerParticipantId": participantID,
		"split": map[string]any{"mode": "equal", "participantIds": []string{participantID}},
	})
	req.SetPathValue("id", itineraryID)
	rec := httptest.NewRecorder()
	budget.CreateExpense(rec, req)
	require.Equal(t, http.StatusCreated, rec.Code, rec.Body.String())
	expenseID := decodeBody(t, rec)["expense"].(map[string]any)["id"].(string)

	h := NewBudgetHandler(pool, &fakeReceiptR2{}, nil)
	payload := map[string]any{
		"contentType": "image/jpeg", "fileSizeBytes": 1024,
		"clientRequestId": "receipt-request-1",
	}
	presign := func() *httptest.ResponseRecorder {
		req := budgetTestRequest(t, "POST",
			"/v1/mobile/itineraries/"+itineraryID+"/expenses/"+expenseID+"/receipt/presign",
			ownerID, payload)
		req.SetPathValue("id", itineraryID)
		req.SetPathValue("expenseId", expenseID)
		rec := httptest.NewRecorder()
		h.PresignReceipt(rec, req)
		return rec
	}

	first := presign()
	require.Equal(t, http.StatusOK, first.Code, first.Body.String())
	second := presign()
	require.Equal(t, http.StatusOK, second.Code, second.Body.String())

	firstBody := decodeBody(t, first)
	secondBody := decodeBody(t, second)
	assert.Equal(t, firstBody["receiptId"], secondBody["receiptId"])
	assert.Equal(t, "https://upload.test/receipt", firstBody["uploadUrl"])
	assert.Equal(t, "https://upload.test/receipt", secondBody["uploadUrl"])
	assert.NotEmpty(t, firstBody["expiresAt"])

	var count int
	require.NoError(t, pool.QueryRow(ctx,
		`SELECT COUNT(*) FROM expense_receipts WHERE expense_id = $1`, expenseID).Scan(&count))
	assert.Equal(t, 1, count)

	var active int
	require.NoError(t, pool.QueryRow(ctx,
		`SELECT COUNT(*) FROM expense_receipts
		 WHERE itinerary_id = $1 AND client_request_id = $2 AND deleted_at IS NULL`,
		itineraryID, "receipt-request-1").Scan(&active))
	assert.Equal(t, 1, active)
}
