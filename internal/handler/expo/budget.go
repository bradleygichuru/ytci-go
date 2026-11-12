package expo

import (
	"encoding/json"
	"errors"
	"net/http"
	"strings"

	"github.com/jackc/pgx/v5"

	"github.com/bradleygichuru/ytci-go/internal/handler"
	"github.com/bradleygichuru/ytci-go/internal/middleware"
)

func (h *BudgetHandler) GetBudget(w http.ResponseWriter, r *http.Request) {
	itineraryID := r.PathValue("id")
	if itineraryID == "" {
		handler.WriteError(w, http.StatusBadRequest, "INVALID_ID", "itinerary id is required")
		return
	}

	if _, err := requireItineraryRole(r.Context(), h.pool, itineraryID, middleware.UserID(r.Context()), roleViewer); err != nil {
		writeBudgetError(w, r, err)
		return
	}

	var totalKes int64
	var hasTotal bool
	var planEstimateRaw string
	var budgetVersion, membersVersion int
	err := h.pool.QueryRow(r.Context(),
		`SELECT COALESCE(budget_total_kes, 0), budget_total_kes IS NOT NULL,
		        plan_estimate::text, budget_version, members_version
		 FROM itineraries WHERE id = $1`,
		itineraryID,
	).Scan(&totalKes, &hasTotal, &planEstimateRaw, &budgetVersion, &membersVersion)
	if errors.Is(err, pgx.ErrNoRows) {
		handler.WriteError(w, http.StatusNotFound, "NOT_FOUND", "itinerary not found")
		return
	}
	if err != nil {
		handler.WriteServerError(w, r, "INTERNAL_ERROR", "failed to load budget", err)
		return
	}

	targets := map[string]int64{}
	rows, err := h.pool.Query(r.Context(),
		`SELECT category, target_kes FROM trip_budget_targets WHERE itinerary_id = $1`, itineraryID)
	if err != nil {
		handler.WriteServerError(w, r, "INTERNAL_ERROR", "failed to load budget targets", err)
		return
	}
	for rows.Next() {
		var category string
		var target int64
		if err := rows.Scan(&category, &target); err != nil {
			rows.Close()
			handler.WriteServerError(w, r, "INTERNAL_ERROR", "failed to read budget target", err)
			return
		}
		targets[category] = target
	}
	rows.Close()

	planEstimate := map[string]int64{}
	_ = json.Unmarshal([]byte(planEstimateRaw), &planEstimate)

	participants, err := loadParticipants(r.Context(), h.pool, itineraryID)
	if err != nil {
		handler.WriteServerError(w, r, "INTERNAL_ERROR", "failed to load participants", err)
		return
	}
	balances := make(map[string]*balanceResponse, len(participants))
	for _, p := range participants {
		balances[p.ID] = &balanceResponse{
			ParticipantID: p.ID, Name: p.Name, Kind: p.Kind, Removed: p.Removed,
		}
	}
	activeCount := 0
	for _, p := range participants {
		if !p.Removed {
			activeCount++
		}
	}

	byCategory := map[string]int64{}
	byDay := map[string]int64{}
	var spent int64
	expenseCount := 0
	expenseRows, err := h.pool.Query(r.Context(),
		`SELECT id::text, amount_kes, category, expense_date::text, payer_participant_id::text
		 FROM expenses WHERE itinerary_id = $1 AND deleted_at IS NULL`,
		itineraryID)
	if err != nil {
		handler.WriteServerError(w, r, "INTERNAL_ERROR", "failed to load expenses", err)
		return
	}
	for expenseRows.Next() {
		var id, category, date, payerID string
		var amount int64
		if err := expenseRows.Scan(&id, &amount, &category, &date, &payerID); err != nil {
			expenseRows.Close()
			handler.WriteServerError(w, r, "INTERNAL_ERROR", "failed to read expense", err)
			return
		}
		spent += amount
		expenseCount++
		byCategory[category] += amount
		byDay[date] += amount
		if b, ok := balances[payerID]; ok {
			b.PaidKes += amount
		}
	}
	expenseRows.Close()

	shareRows, err := h.pool.Query(r.Context(),
		`SELECT s.participant_id::text, s.share_kes
		 FROM expense_shares s
		 JOIN expenses e ON e.id = s.expense_id
		 WHERE e.itinerary_id = $1 AND e.deleted_at IS NULL`,
		itineraryID)
	if err != nil {
		handler.WriteServerError(w, r, "INTERNAL_ERROR", "failed to load expense shares", err)
		return
	}
	for shareRows.Next() {
		var participantID string
		var share int64
		if err := shareRows.Scan(&participantID, &share); err != nil {
			shareRows.Close()
			handler.WriteServerError(w, r, "INTERNAL_ERROR", "failed to read expense share", err)
			return
		}
		if b, ok := balances[participantID]; ok {
			b.OwedKes += share
		}
	}
	shareRows.Close()

	settlementCount := 0
	settlementRows, err := h.pool.Query(r.Context(),
		`SELECT from_participant_id::text, to_participant_id::text, amount_kes
		 FROM settlements WHERE itinerary_id = $1 AND deleted_at IS NULL`,
		itineraryID)
	if err != nil {
		handler.WriteServerError(w, r, "INTERNAL_ERROR", "failed to load settlements", err)
		return
	}
	for settlementRows.Next() {
		var fromID, toID string
		var amount int64
		if err := settlementRows.Scan(&fromID, &toID, &amount); err != nil {
			settlementRows.Close()
			handler.WriteServerError(w, r, "INTERNAL_ERROR", "failed to read settlement", err)
			return
		}
		settlementCount++
		if b, ok := balances[fromID]; ok {
			b.BalanceKes -= amount
		}
		if b, ok := balances[toID]; ok {
			b.BalanceKes += amount
		}
	}
	settlementRows.Close()

	balanceList := make([]balanceResponse, 0, len(participants))
	settled := expenseCount > 0
	for _, p := range participants {
		b := balances[p.ID]
		b.BalanceKes += b.PaidKes - b.OwedKes
		if b.BalanceKes != 0 {
			settled = false
		}
		balanceList = append(balanceList, *b)
	}

	resp := map[string]any{
		"version":         budgetVersion,
		"membersVersion":  membersVersion,
		"spentKes":        spent,
		"totalKes":        nil,
		"remainingKes":    nil,
		"perPersonKes":    nil,
		"planEstimate":    planEstimate,
		"targets":         targets,
		"byCategory":      byCategory,
		"byDay":           byDay,
		"participants":    balanceList,
		"expenseCount":    expenseCount,
		"settlementCount": settlementCount,
		"settled":         settled,
	}
	if hasTotal {
		resp["totalKes"] = totalKes
		remaining := totalKes - spent
		resp["remainingKes"] = remaining
		if activeCount > 0 {
			resp["perPersonKes"] = totalKes / int64(activeCount)
		}
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(resp)
}

func (h *BudgetHandler) UpdateBudget(w http.ResponseWriter, r *http.Request) {
	itineraryID := r.PathValue("id")
	if itineraryID == "" {
		handler.WriteError(w, http.StatusBadRequest, "INVALID_ID", "itinerary id is required")
		return
	}

	var req struct {
		TotalKes        *int64           `json:"totalKes"`
		CategoryTargets map[string]int64 `json:"categoryTargets"`
		PlanEstimate    map[string]int64 `json:"planEstimate"`
		ExpectedVersion int              `json:"expectedVersion,omitempty"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		handler.WriteError(w, http.StatusBadRequest, "INVALID_REQUEST", "invalid request body")
		return
	}
	if req.TotalKes != nil && *req.TotalKes < 0 {
		handler.WriteError(w, http.StatusBadRequest, "INVALID_AMOUNT", "total budget cannot be negative")
		return
	}
	for category, target := range req.CategoryTargets {
		if !isValidBudgetCategory(category) || target < 0 {
			handler.WriteError(w, http.StatusBadRequest, "INVALID_CATEGORY", "invalid category or target")
			return
		}
	}
	for category, estimate := range req.PlanEstimate {
		if !isValidBudgetCategory(category) || estimate < 0 {
			handler.WriteError(w, http.StatusBadRequest, "INVALID_CATEGORY", "invalid plan estimate")
			return
		}
	}

	userID := middleware.UserID(r.Context())
	tx, err := h.pool.Begin(r.Context())
	if err != nil {
		handler.WriteServerError(w, r, "INTERNAL_ERROR", "failed to update budget", err)
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

	if req.TotalKes != nil {
		if _, err := tx.Exec(r.Context(),
			`UPDATE itineraries SET budget_total_kes = $2, updated_at = now() WHERE id = $1`,
			itineraryID, *req.TotalKes); err != nil {
			handler.WriteServerError(w, r, "INTERNAL_ERROR", "failed to set budget total", err)
			return
		}
	}
	if req.PlanEstimate != nil {
		raw, _ := json.Marshal(req.PlanEstimate)
		if _, err := tx.Exec(r.Context(),
			`UPDATE itineraries SET plan_estimate = $2::jsonb, updated_at = now() WHERE id = $1`,
			itineraryID, string(raw)); err != nil {
			handler.WriteServerError(w, r, "INTERNAL_ERROR", "failed to set plan estimate", err)
			return
		}
	}
	if req.CategoryTargets != nil {
		if _, err := tx.Exec(r.Context(),
			`DELETE FROM trip_budget_targets WHERE itinerary_id = $1`, itineraryID); err != nil {
			handler.WriteServerError(w, r, "INTERNAL_ERROR", "failed to clear targets", err)
			return
		}
		for category, target := range req.CategoryTargets {
			if _, err := tx.Exec(r.Context(),
				`INSERT INTO trip_budget_targets (itinerary_id, category, target_kes)
				 VALUES ($1, $2, $3)`,
				itineraryID, category, target); err != nil {
				handler.WriteServerError(w, r, "INTERNAL_ERROR", "failed to set target", err)
				return
			}
		}
	}

	newVersion, err := bumpBudgetVersion(r.Context(), tx, itineraryID)
	if err != nil {
		writeBudgetError(w, r, err)
		return
	}
	if err := recordBudgetEvent(r.Context(), tx, itineraryID, userID, "budget_updated",
		"budget", itineraryID, "Updated the budget", nil,
		map[string]any{"version": newVersion, "totalKes": req.TotalKes, "targets": req.CategoryTargets}); err != nil {
		handler.WriteServerError(w, r, "INTERNAL_ERROR", "failed to record budget event", err)
		return
	}
	if err := tx.Commit(r.Context()); err != nil {
		handler.WriteServerError(w, r, "INTERNAL_ERROR", "failed to commit budget", err)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]any{"version": newVersion})
}

func (h *BudgetHandler) ListParticipants(w http.ResponseWriter, r *http.Request) {
	itineraryID := r.PathValue("id")
	if itineraryID == "" {
		handler.WriteError(w, http.StatusBadRequest, "INVALID_ID", "itinerary id is required")
		return
	}
	if _, err := requireItineraryRole(r.Context(), h.pool, itineraryID, middleware.UserID(r.Context()), roleViewer); err != nil {
		writeBudgetError(w, r, err)
		return
	}
	participants, err := loadParticipants(r.Context(), h.pool, itineraryID)
	if err != nil {
		handler.WriteServerError(w, r, "INTERNAL_ERROR", "failed to load participants", err)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]any{"participants": participantResponses(participants)})
}

func (h *BudgetHandler) CreateParticipant(w http.ResponseWriter, r *http.Request) {
	itineraryID := r.PathValue("id")
	if itineraryID == "" {
		handler.WriteError(w, http.StatusBadRequest, "INVALID_ID", "itinerary id is required")
		return
	}
	var req struct {
		Name string `json:"name"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		handler.WriteError(w, http.StatusBadRequest, "INVALID_REQUEST", "invalid request body")
		return
	}
	name := strings.TrimSpace(req.Name)
	if name == "" || len(name) > 60 {
		handler.WriteError(w, http.StatusBadRequest, "INVALID_NAME", "participant name must be 1–60 characters")
		return
	}

	userID := middleware.UserID(r.Context())
	tx, err := h.pool.Begin(r.Context())
	if err != nil {
		handler.WriteServerError(w, r, "INTERNAL_ERROR", "failed to add participant", err)
		return
	}
	defer tx.Rollback(r.Context())
	if _, err := requireItineraryRole(r.Context(), tx, itineraryID, userID, roleEditor); err != nil {
		writeBudgetError(w, r, err)
		return
	}

	participants, err := countActiveParticipants(r.Context(), tx, itineraryID)
	if err != nil {
		handler.WriteServerError(w, r, "INTERNAL_ERROR", "failed to count participants", err)
		return
	}
	if participants >= maxItineraryParticipants {
		handler.WriteError(w, http.StatusConflict, "TRIP_FULL", "this trip already has 20 participants")
		return
	}

	var id string
	err = tx.QueryRow(r.Context(),
		`INSERT INTO trip_participants (itinerary_id, display_name, kind, created_by)
		 VALUES ($1, $2, 'offline', $3)
		 RETURNING id`,
		itineraryID, name, userID).Scan(&id)
	if err != nil {
		handler.WriteError(w, http.StatusConflict, "PARTICIPANT_EXISTS", "a participant with that name already exists")
		return
	}
	if err := bumpMembersVersion(r.Context(), tx, itineraryID); err != nil {
		handler.WriteServerError(w, r, "INTERNAL_ERROR", "failed to bump members version", err)
		return
	}
	if err := recordTripEvent(r.Context(), tx, itineraryID, userID, "members", "participant_added",
		"participant", id, "Added "+name, nil, map[string]any{"name": name, "kind": "offline"}, ""); err != nil {
		handler.WriteServerError(w, r, "INTERNAL_ERROR", "failed to record participant event", err)
		return
	}
	if err := tx.Commit(r.Context()); err != nil {
		handler.WriteServerError(w, r, "INTERNAL_ERROR", "failed to commit participant", err)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	json.NewEncoder(w).Encode(participantResponse{ID: id, Name: name, Kind: "offline"})
}

func (h *BudgetHandler) RemoveParticipant(w http.ResponseWriter, r *http.Request) {
	itineraryID := r.PathValue("id")
	participantID := r.PathValue("participantId")
	if itineraryID == "" || participantID == "" {
		handler.WriteError(w, http.StatusBadRequest, "INVALID_ID", "itinerary and participant ids are required")
		return
	}

	userID := middleware.UserID(r.Context())
	tx, err := h.pool.Begin(r.Context())
	if err != nil {
		handler.WriteServerError(w, r, "INTERNAL_ERROR", "failed to remove participant", err)
		return
	}
	defer tx.Rollback(r.Context())
	if _, err := requireItineraryRole(r.Context(), tx, itineraryID, userID, roleEditor); err != nil {
		writeBudgetError(w, r, err)
		return
	}

	var kind string
	var removed bool
	err = tx.QueryRow(r.Context(),
		`SELECT kind, (removed_at IS NOT NULL) FROM trip_participants
		 WHERE id = $1 AND itinerary_id = $2`,
		participantID, itineraryID).Scan(&kind, &removed)
	if errors.Is(err, pgx.ErrNoRows) {
		handler.WriteError(w, http.StatusNotFound, "NOT_FOUND", "participant not found")
		return
	}
	if err != nil {
		handler.WriteServerError(w, r, "INTERNAL_ERROR", "failed to load participant", err)
		return
	}
	if kind != "offline" {
		handler.WriteError(w, http.StatusBadRequest, "MEMBER_PARTICIPANT", "remove members from Travelers instead")
		return
	}
	if removed {
		handler.WriteError(w, http.StatusNotFound, "NOT_FOUND", "participant not found")
		return
	}
	if _, err := tx.Exec(r.Context(),
		`UPDATE trip_participants SET removed_at = now() WHERE id = $1`, participantID); err != nil {
		handler.WriteServerError(w, r, "INTERNAL_ERROR", "failed to remove participant", err)
		return
	}
	if err := bumpMembersVersion(r.Context(), tx, itineraryID); err != nil {
		handler.WriteServerError(w, r, "INTERNAL_ERROR", "failed to bump members version", err)
		return
	}
	if err := recordTripEvent(r.Context(), tx, itineraryID, userID, "members", "participant_removed",
		"participant", participantID, "Removed an offline participant", nil, nil, ""); err != nil {
		handler.WriteServerError(w, r, "INTERNAL_ERROR", "failed to record participant event", err)
		return
	}
	if err := tx.Commit(r.Context()); err != nil {
		handler.WriteServerError(w, r, "INTERNAL_ERROR", "failed to commit participant", err)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{"status": "removed"})
}
