package expo

import (
	"context"
	"encoding/json"
	"errors"
	"math"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/bradleygichuru/ytci-go/internal/handler"
	"github.com/bradleygichuru/ytci-go/internal/middleware"
)

type expenseInput struct {
	Title              string     `json:"title"`
	Category           string     `json:"category"`
	Amount             float64    `json:"amount"`
	Currency           string     `json:"currency"`
	FxRate             float64    `json:"fxRate"`
	Date               string     `json:"date"`
	PayerParticipantID string     `json:"payerParticipantId"`
	Notes              string     `json:"notes"`
	Split              splitInput `json:"split"`
	ExpectedVersion    int        `json:"expectedVersion,omitempty"`
}

func (h *BudgetHandler) ListExpenses(w http.ResponseWriter, r *http.Request) {
	itineraryID := r.PathValue("id")
	if itineraryID == "" {
		handler.WriteError(w, http.StatusBadRequest, "INVALID_ID", "itinerary id is required")
		return
	}
	access, err := requireItineraryRole(r.Context(), h.pool, itineraryID, middleware.UserID(r.Context()), roleViewer)
	if err != nil {
		writeBudgetError(w, r, err)
		return
	}

	includeDeleted := r.URL.Query().Get("includeDeleted") == "true" && roleRank(access.Role) >= roleRank(roleEditor)
	query := `SELECT id::text FROM expenses WHERE itinerary_id = $1`
	if !includeDeleted {
		query += ` AND deleted_at IS NULL`
	}
	query += ` ORDER BY expense_date DESC, created_at DESC`

	rows, err := h.pool.Query(r.Context(), query, itineraryID)
	if err != nil {
		handler.WriteServerError(w, r, "INTERNAL_ERROR", "failed to list expenses", err)
		return
	}
	ids := make([]string, 0)
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			handler.WriteServerError(w, r, "INTERNAL_ERROR", "failed to read expense", err)
			return
		}
		ids = append(ids, id)
	}
	rows.Close()

	expenses := make([]expenseResponse, 0, len(ids))
	for _, id := range ids {
		e, err := loadExpenseResponse(r.Context(), h.pool, itineraryID, id)
		if err != nil {
			handler.WriteServerError(w, r, "INTERNAL_ERROR", "failed to load expense", err)
			return
		}
		expenses = append(expenses, *e)
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]any{
		"expenses": expenses, "version": access.BudgetVersion,
	})
}

func (h *BudgetHandler) GetExpense(w http.ResponseWriter, r *http.Request) {
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
	e, err := loadExpenseResponse(r.Context(), h.pool, itineraryID, expenseID)
	if errors.Is(err, pgx.ErrNoRows) {
		handler.WriteError(w, http.StatusNotFound, "NOT_FOUND", "expense not found")
		return
	}
	if err != nil {
		handler.WriteServerError(w, r, "INTERNAL_ERROR", "failed to load expense", err)
		return
	}
	if e.Deleted && roleRank(access.Role) < roleRank(roleEditor) {
		handler.WriteError(w, http.StatusNotFound, "NOT_FOUND", "expense not found")
		return
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(e)
}

func (h *BudgetHandler) CreateExpense(w http.ResponseWriter, r *http.Request) {
	itineraryID := r.PathValue("id")
	if itineraryID == "" {
		handler.WriteError(w, http.StatusBadRequest, "INVALID_ID", "itinerary id is required")
		return
	}
	var req expenseInput
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		handler.WriteError(w, http.StatusBadRequest, "INVALID_REQUEST", "invalid request body")
		return
	}

	original, amountKes, currency, fxRate, date, err := normalizeExpenseInput(req)
	if err != nil {
		handler.WriteError(w, http.StatusBadRequest, "INVALID_EXPENSE", err.Error())
		return
	}

	userID := middleware.UserID(r.Context())
	tx, err := h.pool.Begin(r.Context())
	if err != nil {
		handler.WriteServerError(w, r, "INTERNAL_ERROR", "failed to create expense", err)
		return
	}
	defer tx.Rollback(r.Context())

	if _, err := requireItineraryRole(r.Context(), tx, itineraryID, userID, roleEditor); err != nil {
		writeBudgetError(w, r, err)
		return
	}
	current, err := lockBudgetVersion(r.Context(), tx, itineraryID, req.ExpectedVersion)
	if errors.Is(err, errBudgetVersionConflict) {
		tx.Rollback(r.Context())
		writeBudgetVersionConflict(w, current)
		return
	}
	if err != nil {
		writeBudgetError(w, r, err)
		return
	}

	participants, err := loadParticipants(r.Context(), tx, itineraryID)
	if err != nil {
		handler.WriteServerError(w, r, "INTERNAL_ERROR", "failed to load participants", err)
		return
	}
	active := activeParticipantIndex(participants)
	if _, ok := active[req.PayerParticipantID]; !ok {
		handler.WriteError(w, http.StatusBadRequest, "INVALID_PAYER", "payer must be an active participant")
		return
	}
	shares, percents, err := resolveSplit(amountKes, req.Split, active)
	if err != nil {
		handler.WriteError(w, http.StatusBadRequest, "INVALID_SPLIT", err.Error())
		return
	}

	var expenseID string
	err = tx.QueryRow(r.Context(),
		`INSERT INTO expenses
		 (itinerary_id, title, category, original_amount, currency, fx_rate, amount_kes,
		  expense_date, payer_participant_id, notes, created_by, updated_by)
		 VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,NULLIF($10,''),$11,$11)
		 RETURNING id`,
		itineraryID, strings.TrimSpace(req.Title), req.Category, original, currency, fxRate, amountKes,
		date, req.PayerParticipantID, req.Notes, userID).Scan(&expenseID)
	if err != nil {
		handler.WriteServerError(w, r, "INTERNAL_ERROR", "failed to create expense", err)
		return
	}
	if err := insertShares(r.Context(), tx, expenseID, shares, percents); err != nil {
		handler.WriteServerError(w, r, "INTERNAL_ERROR", "failed to create shares", err)
		return
	}

	newVersion, err := bumpBudgetVersion(r.Context(), tx, itineraryID)
	if err != nil {
		writeBudgetError(w, r, err)
		return
	}
	if err := recordBudgetEvent(r.Context(), tx, itineraryID, userID, "expense_created",
		"expense", expenseID, "Added "+strings.TrimSpace(req.Title), nil,
		map[string]any{"amountKes": amountKes, "category": req.Category, "split": req.Split.Mode}); err != nil {
		handler.WriteServerError(w, r, "INTERNAL_ERROR", "failed to record expense event", err)
		return
	}
	if err := tx.Commit(r.Context()); err != nil {
		handler.WriteServerError(w, r, "INTERNAL_ERROR", "failed to commit expense", err)
		return
	}
	h.publishBudgetChange(r, "updated")

	e, err := loadExpenseResponse(r.Context(), h.pool, itineraryID, expenseID)
	if err != nil {
		handler.WriteServerError(w, r, "INTERNAL_ERROR", "failed to load expense", err)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	json.NewEncoder(w).Encode(map[string]any{"expense": e, "version": newVersion})
}

func (h *BudgetHandler) UpdateExpense(w http.ResponseWriter, r *http.Request) {
	itineraryID := r.PathValue("id")
	expenseID := r.PathValue("expenseId")
	if itineraryID == "" || expenseID == "" {
		handler.WriteError(w, http.StatusBadRequest, "INVALID_ID", "itinerary and expense ids are required")
		return
	}

	var patch struct {
		Title              *string     `json:"title"`
		Category           *string     `json:"category"`
		Amount             *float64    `json:"amount"`
		Currency           *string     `json:"currency"`
		FxRate             *float64    `json:"fxRate"`
		Date               *string     `json:"date"`
		PayerParticipantID *string     `json:"payerParticipantId"`
		Notes              *string     `json:"notes"`
		Split              *splitInput `json:"split"`
		ExpectedVersion    int         `json:"expectedVersion,omitempty"`
	}
	if err := json.NewDecoder(r.Body).Decode(&patch); err != nil {
		handler.WriteError(w, http.StatusBadRequest, "INVALID_REQUEST", "invalid request body")
		return
	}

	userID := middleware.UserID(r.Context())
	tx, err := h.pool.Begin(r.Context())
	if err != nil {
		handler.WriteServerError(w, r, "INTERNAL_ERROR", "failed to update expense", err)
		return
	}
	defer tx.Rollback(r.Context())
	if _, err := requireItineraryRole(r.Context(), tx, itineraryID, userID, roleEditor); err != nil {
		writeBudgetError(w, r, err)
		return
	}
	current, err := lockBudgetVersion(r.Context(), tx, itineraryID, patch.ExpectedVersion)
	if errors.Is(err, errBudgetVersionConflict) {
		tx.Rollback(r.Context())
		writeBudgetVersionConflict(w, current)
		return
	}
	if err != nil {
		writeBudgetError(w, r, err)
		return
	}

	var (
		oldTitle, oldCategory, oldCurrency, oldDate, oldPayer, oldNotes string
		oldOriginal, oldFxRate                                          float64
		oldAmountKes                                                    int64
		deleted                                                         bool
	)
	err = tx.QueryRow(r.Context(),
		`SELECT title, category, original_amount::float8, currency, fx_rate::float8, amount_kes,
		        expense_date::text, payer_participant_id::text, COALESCE(notes, ''),
		        (deleted_at IS NOT NULL)
		 FROM expenses WHERE id = $1 AND itinerary_id = $2 FOR UPDATE`,
		expenseID, itineraryID,
	).Scan(&oldTitle, &oldCategory, &oldOriginal, &oldCurrency, &oldFxRate, &oldAmountKes,
		&oldDate, &oldPayer, &oldNotes, &deleted)
	if errors.Is(err, pgx.ErrNoRows) {
		handler.WriteError(w, http.StatusNotFound, "NOT_FOUND", "expense not found")
		return
	}
	if err != nil {
		handler.WriteServerError(w, r, "INTERNAL_ERROR", "failed to load expense", err)
		return
	}
	if deleted {
		handler.WriteError(w, http.StatusBadRequest, "EXPENSE_DELETED", "restore the expense before editing")
		return
	}

	input := expenseInput{
		Title: oldTitle, Category: oldCategory, Amount: oldOriginal, Currency: oldCurrency,
		FxRate: oldFxRate, Date: oldDate, PayerParticipantID: oldPayer, Notes: oldNotes,
	}
	if patch.Title != nil {
		input.Title = *patch.Title
	}
	if patch.Category != nil {
		input.Category = *patch.Category
	}
	if patch.Amount != nil {
		input.Amount = *patch.Amount
	}
	if patch.Currency != nil {
		input.Currency = *patch.Currency
	}
	if patch.FxRate != nil {
		input.FxRate = *patch.FxRate
	}
	if patch.Date != nil {
		input.Date = *patch.Date
	}
	if patch.PayerParticipantID != nil {
		input.PayerParticipantID = *patch.PayerParticipantID
	}
	if patch.Notes != nil {
		input.Notes = *patch.Notes
	}
	original, amountKes, currency, fxRate, date, err := normalizeExpenseInput(input)
	if err != nil {
		handler.WriteError(w, http.StatusBadRequest, "INVALID_EXPENSE", err.Error())
		return
	}

	participants, err := loadParticipants(r.Context(), tx, itineraryID)
	if err != nil {
		handler.WriteServerError(w, r, "INTERNAL_ERROR", "failed to load participants", err)
		return
	}
	active := activeParticipantIndex(participants)
	if _, ok := active[input.PayerParticipantID]; !ok {
		handler.WriteError(w, http.StatusBadRequest, "INVALID_PAYER", "payer must be an active participant")
		return
	}

	var shares map[string]int64
	var percents map[string]float64
	if patch.Split != nil {
		shares, percents, err = resolveSplit(amountKes, *patch.Split, active)
		if err != nil {
			handler.WriteError(w, http.StatusBadRequest, "INVALID_SPLIT", err.Error())
			return
		}
	} else {
		existing, err := loadShareMap(r.Context(), tx, expenseID)
		if err != nil {
			handler.WriteServerError(w, r, "INTERNAL_ERROR", "failed to load shares", err)
			return
		}
		shares, err = scaleShares(existing, oldAmountKes, amountKes, participants)
		if err != nil {
			handler.WriteError(w, http.StatusBadRequest, "INVALID_SPLIT", err.Error())
			return
		}
	}

	if _, err := tx.Exec(r.Context(),
		`UPDATE expenses
		 SET title = $3, category = $4, original_amount = $5, currency = $6, fx_rate = $7,
		     amount_kes = $8, expense_date = $9, payer_participant_id = $10,
		     notes = NULLIF($11, ''), version = version + 1, updated_by = $12, updated_at = now()
		 WHERE id = $1 AND itinerary_id = $2`,
		expenseID, itineraryID, strings.TrimSpace(input.Title), input.Category, original, currency, fxRate,
		amountKes, date, input.PayerParticipantID, input.Notes, userID); err != nil {
		handler.WriteServerError(w, r, "INTERNAL_ERROR", "failed to update expense", err)
		return
	}
	if patch.Split != nil || amountKes != oldAmountKes {
		if _, err := tx.Exec(r.Context(), `DELETE FROM expense_shares WHERE expense_id = $1`, expenseID); err != nil {
			handler.WriteServerError(w, r, "INTERNAL_ERROR", "failed to replace shares", err)
			return
		}
		if err := insertShares(r.Context(), tx, expenseID, shares, percents); err != nil {
			handler.WriteServerError(w, r, "INTERNAL_ERROR", "failed to replace shares", err)
			return
		}
	}

	newVersion, err := bumpBudgetVersion(r.Context(), tx, itineraryID)
	if err != nil {
		writeBudgetError(w, r, err)
		return
	}
	if err := recordBudgetEvent(r.Context(), tx, itineraryID, userID, "expense_updated",
		"expense", expenseID, "Updated "+strings.TrimSpace(input.Title),
		map[string]any{"amountKes": oldAmountKes, "category": oldCategory, "payer": oldPayer},
		map[string]any{"amountKes": amountKes, "category": input.Category, "payer": input.PayerParticipantID}); err != nil {
		handler.WriteServerError(w, r, "INTERNAL_ERROR", "failed to record expense event", err)
		return
	}
	if err := tx.Commit(r.Context()); err != nil {
		handler.WriteServerError(w, r, "INTERNAL_ERROR", "failed to commit expense", err)
		return
	}
	h.publishBudgetChange(r, "updated")

	e, err := loadExpenseResponse(r.Context(), h.pool, itineraryID, expenseID)
	if err != nil {
		handler.WriteServerError(w, r, "INTERNAL_ERROR", "failed to load expense", err)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]any{"expense": e, "version": newVersion})
}

func (h *BudgetHandler) DeleteExpense(w http.ResponseWriter, r *http.Request) {
	h.setExpenseDeleted(w, r, true)
}

func (h *BudgetHandler) RestoreExpense(w http.ResponseWriter, r *http.Request) {
	h.setExpenseDeleted(w, r, false)
}

func (h *BudgetHandler) setExpenseDeleted(w http.ResponseWriter, r *http.Request, deleted bool) {
	itineraryID := r.PathValue("id")
	expenseID := r.PathValue("expenseId")
	if itineraryID == "" || expenseID == "" {
		handler.WriteError(w, http.StatusBadRequest, "INVALID_ID", "itinerary and expense ids are required")
		return
	}
	userID := middleware.UserID(r.Context())
	tx, err := h.pool.Begin(r.Context())
	if err != nil {
		handler.WriteServerError(w, r, "INTERNAL_ERROR", "failed to update expense", err)
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

	var status string
	var alreadyDeleted bool
	var deletedAt time.Time
	err = tx.QueryRow(r.Context(),
		`SELECT title, (deleted_at IS NOT NULL), COALESCE(deleted_at, now())
		 FROM expenses WHERE id = $1 AND itinerary_id = $2 FOR UPDATE`,
		expenseID, itineraryID).Scan(&status, &alreadyDeleted, &deletedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		handler.WriteError(w, http.StatusNotFound, "NOT_FOUND", "expense not found")
		return
	}
	if err != nil {
		handler.WriteServerError(w, r, "INTERNAL_ERROR", "failed to load expense", err)
		return
	}

	if !deleted && alreadyDeleted && deletedAt.Before(time.Now().Add(-30*24*time.Hour)) {
		handler.WriteError(w, http.StatusGone, "RESTORE_WINDOW_EXPIRED", "this expense can no longer be restored")
		return
	}
	if deleted {
		if _, err := tx.Exec(r.Context(),
			`UPDATE expenses SET deleted_at = now(), updated_at = now() WHERE id = $1`,
			expenseID); err != nil {
			handler.WriteServerError(w, r, "INTERNAL_ERROR", "failed to delete expense", err)
			return
		}
		if _, err := tx.Exec(r.Context(),
			`UPDATE expense_receipts SET status = 'deleted', deleted_at = now(), updated_at = now()
			 WHERE expense_id = $1 AND deleted_at IS NULL`, expenseID); err != nil {
			handler.WriteServerError(w, r, "INTERNAL_ERROR", "failed to delete receipt", err)
			return
		}
	} else {
		if _, err := tx.Exec(r.Context(),
			`UPDATE expenses SET deleted_at = NULL, updated_at = now() WHERE id = $1`,
			expenseID); err != nil {
			handler.WriteServerError(w, r, "INTERNAL_ERROR", "failed to restore expense", err)
			return
		}
	}

	newVersion, err := bumpBudgetVersion(r.Context(), tx, itineraryID)
	if err != nil {
		writeBudgetError(w, r, err)
		return
	}
	action := "expense_deleted"
	summary := "Deleted " + status
	if !deleted {
		action = "expense_restored"
		summary = "Restored " + status
	}
	if err := recordBudgetEvent(r.Context(), tx, itineraryID, userID, action,
		"expense", expenseID, summary, nil, nil); err != nil {
		handler.WriteServerError(w, r, "INTERNAL_ERROR", "failed to record expense event", err)
		return
	}
	if err := tx.Commit(r.Context()); err != nil {
		handler.WriteServerError(w, r, "INTERNAL_ERROR", "failed to commit expense", err)
		return
	}
	h.publishBudgetChange(r, "updated")
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]any{"status": map[bool]string{true: "deleted", false: "restored"}[deleted], "version": newVersion})
}

func (h *BudgetHandler) GetSettleUp(w http.ResponseWriter, r *http.Request) {
	itineraryID := r.PathValue("id")
	if itineraryID == "" {
		handler.WriteError(w, http.StatusBadRequest, "INVALID_ID", "itinerary id is required")
		return
	}
	access, err := requireItineraryRole(r.Context(), h.pool, itineraryID, middleware.UserID(r.Context()), roleViewer)
	if err != nil {
		writeBudgetError(w, r, err)
		return
	}
	participants, balances, err := loadBalanceData(r.Context(), h.pool, itineraryID)
	if err != nil {
		handler.WriteServerError(w, r, "INTERNAL_ERROR", "failed to load settle-up", err)
		return
	}
	order := participantOrder(participants)
	names := make(map[string]string, len(participants))
	amounts := make(map[string]int64, len(participants))
	list := make([]balanceResponse, 0, len(participants))
	settled := true
	for _, id := range order {
		b := balances[id]
		names[id] = b.Name
		amounts[id] = b.BalanceKes
		if b.BalanceKes != 0 {
			settled = false
		}
		list = append(list, *b)
	}
	rawTransfers := suggestTransfers(amounts, order)
	transfers := make([]suggestedTransfer, 0, len(rawTransfers))
	for _, t := range rawTransfers {
		transfers = append(transfers, suggestedTransfer{
			FromParticipantID: t.FromID, FromName: names[t.FromID],
			ToParticipantID: t.ToID, ToName: names[t.ToID], AmountKes: t.Amount,
		})
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]any{
		"participants": list, "transfers": transfers, "settled": settled,
		"version": access.BudgetVersion,
	})
}

func (h *BudgetHandler) ListSettlements(w http.ResponseWriter, r *http.Request) {
	itineraryID := r.PathValue("id")
	if itineraryID == "" {
		handler.WriteError(w, http.StatusBadRequest, "INVALID_ID", "itinerary id is required")
		return
	}
	if _, err := requireItineraryRole(r.Context(), h.pool, itineraryID, middleware.UserID(r.Context()), roleViewer); err != nil {
		writeBudgetError(w, r, err)
		return
	}
	rows, err := h.pool.Query(r.Context(),
		`SELECT s.id::text, s.from_participant_id::text, f.display_name,
		        s.to_participant_id::text, t.display_name, s.amount_kes,
		        s.settlement_date::text, COALESCE(s.note, '')
		 FROM settlements s
		 JOIN trip_participants f ON f.id = s.from_participant_id
		 JOIN trip_participants t ON t.id = s.to_participant_id
		 WHERE s.itinerary_id = $1 AND s.deleted_at IS NULL
		 ORDER BY s.settlement_date DESC, s.created_at DESC`,
		itineraryID)
	if err != nil {
		handler.WriteServerError(w, r, "INTERNAL_ERROR", "failed to list settlements", err)
		return
	}
	defer rows.Close()
	items := make([]settlementResponse, 0)
	for rows.Next() {
		var s settlementResponse
		if err := rows.Scan(&s.ID, &s.FromParticipantID, &s.FromName,
			&s.ToParticipantID, &s.ToName, &s.AmountKes, &s.Date, &s.Note); err != nil {
			handler.WriteServerError(w, r, "INTERNAL_ERROR", "failed to read settlement", err)
			return
		}
		items = append(items, s)
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]any{"settlements": items})
}

func (h *BudgetHandler) CreateSettlement(w http.ResponseWriter, r *http.Request) {
	itineraryID := r.PathValue("id")
	if itineraryID == "" {
		handler.WriteError(w, http.StatusBadRequest, "INVALID_ID", "itinerary id is required")
		return
	}
	var req struct {
		FromParticipantID string `json:"fromParticipantId"`
		ToParticipantID   string `json:"toParticipantId"`
		AmountKes         int64  `json:"amountKes"`
		Date              string `json:"date"`
		Note              string `json:"note"`
		ExpectedVersion   int    `json:"expectedVersion,omitempty"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		handler.WriteError(w, http.StatusBadRequest, "INVALID_REQUEST", "invalid request body")
		return
	}
	if req.AmountKes <= 0 || req.FromParticipantID == "" || req.ToParticipantID == "" ||
		req.FromParticipantID == req.ToParticipantID {
		handler.WriteError(w, http.StatusBadRequest, "INVALID_SETTLEMENT", "invalid settlement")
		return
	}
	date, err := parseExpenseDate(req.Date)
	if err != nil {
		handler.WriteError(w, http.StatusBadRequest, "INVALID_DATE", "invalid settlement date")
		return
	}

	userID := middleware.UserID(r.Context())
	tx, err := h.pool.Begin(r.Context())
	if err != nil {
		handler.WriteServerError(w, r, "INTERNAL_ERROR", "failed to create settlement", err)
		return
	}
	defer tx.Rollback(r.Context())
	if _, err := requireItineraryRole(r.Context(), tx, itineraryID, userID, roleEditor); err != nil {
		writeBudgetError(w, r, err)
		return
	}
	current, err := lockBudgetVersion(r.Context(), tx, itineraryID, req.ExpectedVersion)
	if errors.Is(err, errBudgetVersionConflict) {
		tx.Rollback(r.Context())
		writeBudgetVersionConflict(w, current)
		return
	}
	if err != nil {
		writeBudgetError(w, r, err)
		return
	}

	participants, err := loadParticipants(r.Context(), tx, itineraryID)
	if err != nil {
		handler.WriteServerError(w, r, "INTERNAL_ERROR", "failed to load participants", err)
		return
	}
	known := knownParticipantIndex(participants)
	if _, ok := known[req.FromParticipantID]; !ok {
		handler.WriteError(w, http.StatusBadRequest, "INVALID_PARTICIPANT", "sender must belong to the trip")
		return
	}
	if _, ok := known[req.ToParticipantID]; !ok {
		handler.WriteError(w, http.StatusBadRequest, "INVALID_PARTICIPANT", "recipient must belong to the trip")
		return
	}

	var settlementID string
	err = tx.QueryRow(r.Context(),
		`INSERT INTO settlements (itinerary_id, from_participant_id, to_participant_id, amount_kes, settlement_date, note, created_by)
		 VALUES ($1,$2,$3,$4,$5,NULLIF($6,''),$7) RETURNING id`,
		itineraryID, req.FromParticipantID, req.ToParticipantID, req.AmountKes, date, req.Note, userID,
	).Scan(&settlementID)
	if err != nil {
		handler.WriteServerError(w, r, "INTERNAL_ERROR", "failed to create settlement", err)
		return
	}
	newVersion, err := bumpBudgetVersion(r.Context(), tx, itineraryID)
	if err != nil {
		writeBudgetError(w, r, err)
		return
	}
	if err := recordBudgetEvent(r.Context(), tx, itineraryID, userID, "settlement_created",
		"settlement", settlementID, "Recorded a settlement", nil,
		map[string]any{"from": req.FromParticipantID, "to": req.ToParticipantID, "amountKes": req.AmountKes}); err != nil {
		handler.WriteServerError(w, r, "INTERNAL_ERROR", "failed to record settlement event", err)
		return
	}
	if err := tx.Commit(r.Context()); err != nil {
		handler.WriteServerError(w, r, "INTERNAL_ERROR", "failed to commit settlement", err)
		return
	}
	h.publishBudgetChange(r, "updated")
	var title string
	_ = h.pool.QueryRow(r.Context(), `SELECT title FROM itineraries WHERE id = $1`, itineraryID).Scan(&title)
	pushItineraryMembers(r.Context(), h.pool, itineraryID, userID,
		"Settlement recorded", "A settlement was recorded on \""+title+"\".")

	_, balances, err := loadBalanceData(r.Context(), h.pool, itineraryID)
	warning := ""
	if err == nil {
		for _, b := range balances {
			if b.BalanceKes < 0 {
				warning = "This settlement creates a negative balance; check the recorded amount."
				break
			}
		}
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	json.NewEncoder(w).Encode(map[string]any{
		"id": settlementID, "version": newVersion, "warning": warning,
	})
}

func (h *BudgetHandler) DeleteSettlement(w http.ResponseWriter, r *http.Request) {
	itineraryID := r.PathValue("id")
	settlementID := r.PathValue("settlementId")
	if itineraryID == "" || settlementID == "" {
		handler.WriteError(w, http.StatusBadRequest, "INVALID_ID", "itinerary and settlement ids are required")
		return
	}
	userID := middleware.UserID(r.Context())
	tx, err := h.pool.Begin(r.Context())
	if err != nil {
		handler.WriteServerError(w, r, "INTERNAL_ERROR", "failed to reverse settlement", err)
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
		`UPDATE settlements SET deleted_at = now() WHERE id = $1 AND itinerary_id = $2 AND deleted_at IS NULL`,
		settlementID, itineraryID)
	if err != nil {
		handler.WriteServerError(w, r, "INTERNAL_ERROR", "failed to reverse settlement", err)
		return
	}
	if cmd.RowsAffected() == 0 {
		handler.WriteError(w, http.StatusNotFound, "NOT_FOUND", "settlement not found")
		return
	}
	newVersion, err := bumpBudgetVersion(r.Context(), tx, itineraryID)
	if err != nil {
		writeBudgetError(w, r, err)
		return
	}
	if err := recordBudgetEvent(r.Context(), tx, itineraryID, userID, "settlement_reversed",
		"settlement", settlementID, "Reversed a settlement", nil, nil); err != nil {
		handler.WriteServerError(w, r, "INTERNAL_ERROR", "failed to record settlement event", err)
		return
	}
	if err := tx.Commit(r.Context()); err != nil {
		handler.WriteServerError(w, r, "INTERNAL_ERROR", "failed to commit settlement", err)
		return
	}
	h.publishBudgetChange(r, "updated")
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]any{"status": "reversed", "version": newVersion})
}

// ---------------------------------------------------------------------------
// Shared expense helpers
// ---------------------------------------------------------------------------

func normalizeExpenseInput(req expenseInput) (original float64, amountKes int64, currency string, fxRate float64, date time.Time, err error) {
	if strings.TrimSpace(req.Title) == "" || len(strings.TrimSpace(req.Title)) > 120 {
		return 0, 0, "", 0, time.Time{}, errors.New("title must be 1–120 characters")
	}
	if !isValidBudgetCategory(req.Category) {
		return 0, 0, "", 0, time.Time{}, errors.New("invalid category")
	}
	if req.Amount <= 0 || math.IsNaN(req.Amount) || math.IsInf(req.Amount, 0) {
		return 0, 0, "", 0, time.Time{}, errors.New("amount must be positive")
	}
	currency = strings.ToUpper(strings.TrimSpace(req.Currency))
	if currency == "" {
		currency = "KES"
	}
	if !isValidCurrency(currency) {
		return 0, 0, "", 0, time.Time{}, errors.New("unsupported currency")
	}
	fxRate = req.FxRate
	if currency == "KES" {
		fxRate = 1
	}
	if fxRate <= 0 || math.IsNaN(fxRate) || math.IsInf(fxRate, 0) {
		return 0, 0, "", 0, time.Time{}, errors.New("fx rate must be positive")
	}
	amountKes = roundKES(req.Amount, fxRate)
	if amountKes <= 0 {
		return 0, 0, "", 0, time.Time{}, errors.New("converted amount must be positive")
	}
	date, err = parseExpenseDate(req.Date)
	if err != nil {
		return 0, 0, "", 0, time.Time{}, errors.New("invalid expense date")
	}
	return req.Amount, amountKes, currency, fxRate, date, nil
}

func activeParticipantIndex(participants []participantRecord) map[string]participantRecord {
	active := make(map[string]participantRecord, len(participants))
	for _, p := range participants {
		if !p.Removed {
			active[p.ID] = p
		}
	}
	return active
}

func knownParticipantIndex(participants []participantRecord) map[string]participantRecord {
	known := make(map[string]participantRecord, len(participants))
	for _, p := range participants {
		known[p.ID] = p
	}
	return known
}

func resolveSplit(amountKes int64, split splitInput, active map[string]participantRecord) (map[string]int64, map[string]float64, error) {
	switch split.Mode {
	case "equal":
		if len(split.ParticipantIDs) == 0 {
			return nil, nil, errNoParticipants
		}
		for _, id := range split.ParticipantIDs {
			if _, ok := active[id]; !ok {
				return nil, nil, errors.New("split participant is not active")
			}
		}
		shares, err := splitEqual(amountKes, split.ParticipantIDs)
		return shares, nil, err
	case "custom", "percentage":
		if len(split.Shares) == 0 {
			return nil, nil, errNoParticipants
		}
		for _, s := range split.Shares {
			if _, ok := active[s.ParticipantID]; !ok {
				return nil, nil, errors.New("split participant is not active")
			}
		}
		if split.Mode == "custom" {
			shares, percents, err := splitCustom(amountKes, split.Shares)
			return shares, percents, err
		}
		return splitPercent(amountKes, split.Shares)
	default:
		return nil, nil, errors.New("split mode must be equal, custom, or percentage")
	}
}

func insertShares(ctx context.Context, tx pgx.Tx, expenseID string, shares map[string]int64, percents map[string]float64) error {
	for participantID, amount := range shares {
		var percent any
		if percents != nil {
			if p, ok := percents[participantID]; ok {
				percent = p
			}
		}
		if _, err := tx.Exec(ctx,
			`INSERT INTO expense_shares (expense_id, participant_id, share_kes, share_percent)
			 VALUES ($1, $2, $3, $4)`,
			expenseID, participantID, amount, percent); err != nil {
			return err
		}
	}
	return nil
}

func loadShareMap(ctx context.Context, q dbQuerier, expenseID string) (map[string]int64, error) {
	rows, err := q.Query(ctx,
		`SELECT participant_id::text, share_kes FROM expense_shares WHERE expense_id = $1`, expenseID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make(map[string]int64)
	for rows.Next() {
		var id string
		var amount int64
		if err := rows.Scan(&id, &amount); err != nil {
			return nil, err
		}
		out[id] = amount
	}
	return out, rows.Err()
}

func scaleShares(existing map[string]int64, oldAmount, newAmount int64, participants []participantRecord) (map[string]int64, error) {
	if oldAmount <= 0 || newAmount <= 0 || len(existing) == 0 {
		return nil, errors.New("cannot scale an empty split")
	}
	type entry struct {
		id    string
		base  int64
		rem   int64
		index int
	}
	order := participantOrder(participants)
	index := make(map[string]int, len(order))
	for i, id := range order {
		index[id] = i
	}
	entries := make([]entry, 0, len(existing))
	var distributed int64
	for id, oldShare := range existing {
		base := oldShare * newAmount / oldAmount
		rem := (oldShare * newAmount) % oldAmount
		entries = append(entries, entry{id: id, base: base, rem: rem, index: index[id]})
		distributed += base
	}
	remainder := newAmount - distributed
	sort.SliceStable(entries, func(i, j int) bool {
		if entries[i].rem == entries[j].rem {
			return entries[i].index < entries[j].index
		}
		return entries[i].rem > entries[j].rem
	})
	out := make(map[string]int64, len(entries))
	for _, e := range entries {
		out[e.id] = e.base
	}
	for i := int64(0); i < remainder; i++ {
		out[entries[i%int64(len(entries))].id]++
	}
	return out, nil
}

func loadExpenseResponse(ctx context.Context, q dbQuerier, itineraryID, expenseID string) (*expenseResponse, error) {
	var e expenseResponse
	var deleted bool
	err := q.QueryRow(ctx,
		`SELECT e.id::text, e.title, e.category, e.original_amount::float8, e.currency,
		        e.fx_rate::float8, e.amount_kes, e.expense_date::text,
		        e.payer_participant_id::text, p.display_name, COALESCE(e.notes, ''),
		        e.version, (e.deleted_at IS NOT NULL)
		 FROM expenses e
		 JOIN trip_participants p ON p.id = e.payer_participant_id
		 WHERE e.id = $1 AND e.itinerary_id = $2`,
		expenseID, itineraryID,
	).Scan(&e.ID, &e.Title, &e.Category, &e.OriginalAmount, &e.Currency, &e.FxRate,
		&e.AmountKes, &e.Date, &e.PayerParticipantID, &e.PayerName, &e.Notes, &e.Version, &deleted)
	if err != nil {
		return nil, err
	}
	e.Deleted = deleted
	e.Shares = make([]shareResponse, 0)

	rows, err := q.Query(ctx,
		`SELECT s.participant_id::text, s.share_kes, COALESCE(s.share_percent, 0)::float8
		 FROM expense_shares s
		 JOIN trip_participants p ON p.id = s.participant_id
		 WHERE s.expense_id = $1
		 ORDER BY p.created_at, p.id`,
		expenseID)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var s shareResponse
		var percent float64
		if err := rows.Scan(&s.ParticipantID, &s.AmountKes, &percent); err != nil {
			rows.Close()
			return nil, err
		}
		if percent > 0 {
			s.Percent = &percent
		}
		e.Shares = append(e.Shares, s)
	}
	rows.Close()

	var rm receiptMeta
	var total, confidence float64
	err = q.QueryRow(ctx,
		`SELECT id::text, status, ocr_status, COALESCE(ocr_merchant, ''), COALESCE(ocr_date::text, ''),
		        COALESCE(ocr_total, 0)::float8, COALESCE(ocr_currency, ''), COALESCE(ocr_confidence, 0)::float8
		 FROM expense_receipts
		 WHERE expense_id = $1 AND deleted_at IS NULL
		 ORDER BY created_at DESC LIMIT 1`,
		expenseID,
	).Scan(&rm.ID, &rm.Status, &rm.OCRStatus, &rm.Merchant, &rm.Date, &total, &rm.Currency, &confidence)
	if err == nil {
		rm.Total = &total
		rm.Confidence = &confidence
		e.Receipt = &rm
	} else if !errors.Is(err, pgx.ErrNoRows) {
		return nil, err
	}
	return &e, nil
}

func loadBalanceData(ctx context.Context, q dbQuerier, itineraryID string) ([]participantRecord, map[string]*balanceResponse, error) {
	participants, err := loadParticipants(ctx, q, itineraryID)
	if err != nil {
		return nil, nil, err
	}
	balances := make(map[string]*balanceResponse, len(participants))
	for _, p := range participants {
		balances[p.ID] = &balanceResponse{
			ParticipantID: p.ID, Name: p.Name, Kind: p.Kind, Removed: p.Removed,
		}
	}

	rows, err := q.Query(ctx,
		`SELECT payer_participant_id::text, SUM(amount_kes)::bigint
		 FROM expenses WHERE itinerary_id = $1 AND deleted_at IS NULL
		 GROUP BY payer_participant_id`, itineraryID)
	if err != nil {
		return nil, nil, err
	}
	for rows.Next() {
		var id string
		var amount int64
		if err := rows.Scan(&id, &amount); err != nil {
			rows.Close()
			return nil, nil, err
		}
		if b, ok := balances[id]; ok {
			b.PaidKes += amount
		}
	}
	rows.Close()

	rows, err = q.Query(ctx,
		`SELECT s.participant_id::text, SUM(s.share_kes)::bigint
		 FROM expense_shares s
		 JOIN expenses e ON e.id = s.expense_id
		 WHERE e.itinerary_id = $1 AND e.deleted_at IS NULL
		 GROUP BY s.participant_id`, itineraryID)
	if err != nil {
		return nil, nil, err
	}
	for rows.Next() {
		var id string
		var amount int64
		if err := rows.Scan(&id, &amount); err != nil {
			rows.Close()
			return nil, nil, err
		}
		if b, ok := balances[id]; ok {
			b.OwedKes += amount
		}
	}
	rows.Close()

	rows, err = q.Query(ctx,
		`SELECT from_participant_id::text, to_participant_id::text, amount_kes
		 FROM settlements WHERE itinerary_id = $1 AND deleted_at IS NULL`, itineraryID)
	if err != nil {
		return nil, nil, err
	}
	for rows.Next() {
		var fromID, toID string
		var amount int64
		if err := rows.Scan(&fromID, &toID, &amount); err != nil {
			rows.Close()
			return nil, nil, err
		}
		if b, ok := balances[fromID]; ok {
			b.BalanceKes -= amount
		}
		if b, ok := balances[toID]; ok {
			b.BalanceKes += amount
		}
	}
	rows.Close()

	for _, b := range balances {
		b.BalanceKes += b.PaidKes - b.OwedKes
	}
	return participants, balances, nil
}
