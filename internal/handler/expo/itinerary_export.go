package expo

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/bradleygichuru/ytci-go/internal/handler"
	"github.com/bradleygichuru/ytci-go/internal/middleware"
)

const exportFormatVersion = 1

type itineraryExport struct {
	Format     string              `json:"format"`
	Version    int                 `json:"version"`
	ExportedAt time.Time           `json:"exportedAt"`
	Checksum   string              `json:"checksum"`
	Data       itineraryExportData `json:"data"`
}

type itineraryExportData struct {
	Itinerary exportItinerary  `json:"itinerary"`
	Members   []exportMember   `json:"members"`
	Stops     []exportStop     `json:"stops"`
	Budget    exportBudget     `json:"budget"`
	History   []exportEvent    `json:"history"`
	Revisions []exportRevision `json:"revisions"`
	Meta      exportMeta       `json:"meta"`
}

type exportItinerary struct {
	ID             string          `json:"id"`
	OwnerID        string          `json:"ownerId"`
	Title          string          `json:"title"`
	Status         string          `json:"status"`
	Inputs         json.RawMessage `json:"inputs"`
	PlanVersion    int             `json:"planVersion"`
	BudgetVersion  int             `json:"budgetVersion"`
	MembersVersion int             `json:"membersVersion"`
	BudgetTotalKes *int64          `json:"budgetTotalKes"`
	PlanEstimate   json.RawMessage `json:"planEstimate"`
	CreatedAt      time.Time       `json:"createdAt"`
	UpdatedAt      time.Time       `json:"updatedAt"`
	DeletedAt      *time.Time      `json:"deletedAt,omitempty"`
}

type exportMember struct {
	UserID   string    `json:"userId"`
	Name     string    `json:"name"`
	Email    string    `json:"email,omitempty"`
	Role     string    `json:"role"`
	JoinedAt time.Time `json:"joinedAt"`
}

type exportStop struct {
	ID                string `json:"id"`
	DestinationID     string `json:"destinationId,omitempty"`
	Day               int    `json:"day"`
	DisplayOrder      int    `json:"displayOrder"`
	Title             string `json:"title,omitempty"`
	Description       string `json:"description,omitempty"`
	EstimatedCost     string `json:"estimatedCost,omitempty"`
	EstimatedDuration string `json:"estimatedDuration,omitempty"`
	TravelFrom        string `json:"travelFrom,omitempty"`
	Notes             string `json:"notes,omitempty"`
	StartTime         string `json:"startTime,omitempty"`
	Category          string `json:"category,omitempty"`
	ImageURL          string `json:"imageUrl,omitempty"`
}

type exportBudget struct {
	TotalKes     *int64              `json:"totalKes"`
	SpentKes     int64               `json:"spentKes"`
	RemainingKes *int64              `json:"remainingKes"`
	PerPersonKes *int64              `json:"perPersonKes"`
	PlanEstimate map[string]int64    `json:"planEstimate"`
	Targets      map[string]int64    `json:"targets"`
	Participants []exportParticipant `json:"participants"`
	Expenses     []exportExpense     `json:"expenses"`
	Settlements  []exportSettlement  `json:"settlements"`
	Balances     []balanceResponse   `json:"balances"`
	Settled      bool                `json:"settled"`
	ExpenseCount int                 `json:"expenseCount"`
}

type exportParticipant struct {
	ID        string    `json:"id"`
	UserID    string    `json:"userId,omitempty"`
	Name      string    `json:"name"`
	Kind      string    `json:"kind"`
	Removed   bool      `json:"removed"`
	CreatedAt time.Time `json:"createdAt"`
}

type exportExpense struct {
	ID                 string          `json:"id"`
	Title              string          `json:"title"`
	Category           string          `json:"category"`
	OriginalAmount     float64         `json:"originalAmount"`
	Currency           string          `json:"currency"`
	FxRate             float64         `json:"fxRate"`
	AmountKes          int64           `json:"amountKes"`
	Date               string          `json:"date"`
	PayerParticipantID string          `json:"payerParticipantId"`
	PayerName          string          `json:"payerName"`
	Notes              string          `json:"notes,omitempty"`
	Version            int             `json:"version"`
	Deleted            bool            `json:"deleted"`
	CreatedBy          string          `json:"createdBy,omitempty"`
	UpdatedBy          string          `json:"updatedBy,omitempty"`
	CreatedAt          time.Time       `json:"createdAt"`
	UpdatedAt          time.Time       `json:"updatedAt"`
	DeletedAt          *time.Time      `json:"deletedAt,omitempty"`
	Shares             []exportShare   `json:"shares"`
	Receipts           []exportReceipt `json:"receipts"`
}

type exportShare struct {
	ParticipantID   string   `json:"participantId"`
	ParticipantName string   `json:"participantName"`
	AmountKes       int64    `json:"amountKes"`
	Percent         *float64 `json:"percent,omitempty"`
}

type exportReceipt struct {
	ID         string     `json:"id"`
	Status     string     `json:"status"`
	OCRStatus  string     `json:"ocrStatus"`
	Merchant   string     `json:"merchant,omitempty"`
	Date       string     `json:"date,omitempty"`
	Total      *float64   `json:"total,omitempty"`
	Currency   string     `json:"currency,omitempty"`
	Confidence *float64   `json:"confidence,omitempty"`
	CreatedBy  string     `json:"createdBy,omitempty"`
	CreatedAt  time.Time  `json:"createdAt"`
	UpdatedAt  time.Time  `json:"updatedAt"`
	DeletedAt  *time.Time `json:"deletedAt,omitempty"`
}

type exportSettlement struct {
	ID                string     `json:"id"`
	FromParticipantID string     `json:"fromParticipantId"`
	FromName          string     `json:"fromName"`
	ToParticipantID   string     `json:"toParticipantId"`
	ToName            string     `json:"toName"`
	AmountKes         int64      `json:"amountKes"`
	Date              string     `json:"date"`
	Note              string     `json:"note,omitempty"`
	CreatedBy         string     `json:"createdBy,omitempty"`
	CreatedAt         time.Time  `json:"createdAt"`
	DeletedAt         *time.Time `json:"deletedAt,omitempty"`
}

type exportEvent struct {
	ID         string          `json:"id"`
	Scope      string          `json:"scope"`
	Action     string          `json:"action"`
	Summary    string          `json:"summary,omitempty"`
	EntityType string          `json:"entityType,omitempty"`
	EntityID   string          `json:"entityId,omitempty"`
	ActorID    string          `json:"actorId,omitempty"`
	ActorName  string          `json:"actorName"`
	Before     json.RawMessage `json:"before"`
	After      json.RawMessage `json:"after"`
	RevisionID string          `json:"revisionId,omitempty"`
	RequestID  string          `json:"requestId,omitempty"`
	CreatedAt  time.Time       `json:"createdAt"`
}

type exportRevision struct {
	ID        string          `json:"id"`
	Version   int             `json:"version"`
	Reason    string          `json:"reason"`
	Checksum  string          `json:"checksum,omitempty"`
	ActorID   string          `json:"actorId,omitempty"`
	ActorName string          `json:"actorName"`
	Snapshot  json.RawMessage `json:"snapshot"`
	CreatedAt time.Time       `json:"createdAt"`
}

type exportMeta struct {
	ExportedBy      string `json:"exportedBy"`
	Source          string `json:"source"`
	IncludesDeleted bool   `json:"includesDeleted"`
	Note            string `json:"note"`
}

func (h *ItinerariesHandler) Export(w http.ResponseWriter, r *http.Request) {
	itineraryID := r.PathValue("id")
	if itineraryID == "" {
		handler.WriteError(w, http.StatusBadRequest, "INVALID_ID", "itinerary id is required")
		return
	}
	userID := middleware.UserID(r.Context())
	access, err := requireItineraryRole(r.Context(), h.pool, itineraryID, userID, roleEditor)
	if err != nil {
		writeItineraryAccessError(w, r, err)
		return
	}

	data, err := h.buildExport(r.Context(), itineraryID, userID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			handler.WriteError(w, http.StatusNotFound, "NOT_FOUND", "itinerary not found")
			return
		}
		handler.WriteServerError(w, r, "INTERNAL_ERROR", "failed to build itinerary export", err)
		return
	}
	data.Itinerary.PlanVersion = access.PlanVersion
	data.Itinerary.BudgetVersion = access.BudgetVersion
	data.Itinerary.MembersVersion = access.MembersVersion

	payload, err := json.Marshal(data)
	if err != nil {
		handler.WriteServerError(w, r, "INTERNAL_ERROR", "failed to encode itinerary export", err)
		return
	}
	sum := sha256.Sum256(payload)
	envelope := itineraryExport{
		Format:     "ytci.itinerary.export",
		Version:    exportFormatVersion,
		ExportedAt: time.Now().UTC(),
		Checksum:   "sha256:" + hex.EncodeToString(sum[:]),
		Data:       data,
	}

	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Content-Disposition", fmt.Sprintf(`attachment; filename="trip-%s.json"`, itineraryID))
	json.NewEncoder(w).Encode(envelope)
}

func (h *ItinerariesHandler) buildExport(ctx context.Context, itineraryID, userID string) (out itineraryExportData, err error) {
	stage := "base"
	defer func() {
		if err != nil {
			err = fmt.Errorf("%s [%s]: %w", stage, itineraryID, err)
		}
	}()

	var inputs, planEstimate string
	err = h.pool.QueryRow(ctx,
		`SELECT id::text, COALESCE(user_id::text, ''), title, status, inputs::text,
		        plan_version, budget_version, members_version, budget_total_kes,
		        plan_estimate::text, created_at, updated_at, deleted_at
		 FROM itineraries WHERE id = $1`, itineraryID,
	).Scan(
		&out.Itinerary.ID, &out.Itinerary.OwnerID, &out.Itinerary.Title,
		&out.Itinerary.Status, &inputs, &out.Itinerary.PlanVersion,
		&out.Itinerary.BudgetVersion, &out.Itinerary.MembersVersion,
		&out.Itinerary.BudgetTotalKes, &planEstimate, &out.Itinerary.CreatedAt,
		&out.Itinerary.UpdatedAt, &out.Itinerary.DeletedAt,
	)
	if err != nil {
		return out, err
	}
	out.Itinerary.Inputs = json.RawMessage(inputs)
	if len(out.Itinerary.Inputs) == 0 {
		out.Itinerary.Inputs = json.RawMessage(`{}`)
	}
	out.Itinerary.PlanEstimate = json.RawMessage(planEstimate)
	if len(out.Itinerary.PlanEstimate) == 0 {
		out.Itinerary.PlanEstimate = json.RawMessage(`{}`)
	}

	stage = "itinerary"
	rows, err := h.pool.Query(ctx,
		`SELECT m.user_id, COALESCE(u.name, 'Former member'), COALESCE(u.email, ''), m.role, m.joined_at
		 FROM itinerary_members m LEFT JOIN users u ON u.id = m.user_id
		 WHERE m.itinerary_id = $1
		 ORDER BY CASE m.role WHEN 'owner' THEN 0 WHEN 'editor' THEN 1 ELSE 2 END, m.joined_at`,
		itineraryID)
	if err != nil {
		return out, err
	}
	out.Members = make([]exportMember, 0)
	for rows.Next() {
		var m exportMember
		if err := rows.Scan(&m.UserID, &m.Name, &m.Email, &m.Role, &m.JoinedAt); err != nil {
			rows.Close()
			return out, err
		}
		out.Members = append(out.Members, m)
	}
	rows.Close()

	stage = "members"
	rows, err = h.pool.Query(ctx,
		`SELECT id::text, COALESCE(destination_id::text, ''), day, display_order,
		        COALESCE(title, ''), COALESCE(description, ''), COALESCE(estimated_cost, ''),
		        COALESCE(estimated_duration, ''), COALESCE(travel_from, ''), COALESCE(notes, ''),
		        COALESCE(start_time, ''), COALESCE(category, ''), COALESCE(image_url, '')
		 FROM itinerary_stops WHERE itinerary_id = $1 ORDER BY day, display_order`,
		itineraryID)
	if err != nil {
		return out, err
	}
	out.Stops = make([]exportStop, 0)
	for rows.Next() {
		var s exportStop
		if err := rows.Scan(&s.ID, &s.DestinationID, &s.Day, &s.DisplayOrder, &s.Title,
			&s.Description, &s.EstimatedCost, &s.EstimatedDuration, &s.TravelFrom,
			&s.Notes, &s.StartTime, &s.Category, &s.ImageURL); err != nil {
			rows.Close()
			return out, err
		}
		out.Stops = append(out.Stops, s)
	}
	rows.Close()

	targets := map[string]int64{}
	stage = "stops"
	rows, err = h.pool.Query(ctx,
		`SELECT category, target_kes FROM trip_budget_targets WHERE itinerary_id = $1`, itineraryID)
	if err != nil {
		return out, err
	}
	for rows.Next() {
		var category string
		var target int64
		if err := rows.Scan(&category, &target); err != nil {
			rows.Close()
			return out, err
		}
		targets[category] = target
	}
	rows.Close()

	var planEstimateMap map[string]int64
	_ = json.Unmarshal([]byte(planEstimate), &planEstimateMap)
	if planEstimateMap == nil {
		planEstimateMap = map[string]int64{}
	}
	out.Budget.PlanEstimate = planEstimateMap
	out.Budget.Targets = targets

	stage = "targets"
	participants, err := loadParticipants(ctx, h.pool, itineraryID)
	if err != nil {
		return out, err
	}
	out.Budget.Participants = make([]exportParticipant, 0, len(participants))
	for _, p := range participants {
		out.Budget.Participants = append(out.Budget.Participants, exportParticipant{
			ID: p.ID, UserID: p.UserID, Name: p.Name, Kind: p.Kind,
			Removed: p.Removed, CreatedAt: p.CreatedAt,
		})
	}

	stage = "participants"
	rows, err = h.pool.Query(ctx,
		`SELECT e.id::text, e.title, e.category, e.original_amount::float8, e.currency,
		        e.fx_rate::float8, e.amount_kes, e.expense_date::text,
		        e.payer_participant_id::text, COALESCE(p.display_name, 'Former participant'),
		        COALESCE(e.notes, ''), e.version, (e.deleted_at IS NOT NULL),
		        COALESCE(e.created_by, ''), COALESCE(e.updated_by, ''),
		        e.created_at, e.updated_at, e.deleted_at
		 FROM expenses e
		 LEFT JOIN trip_participants p ON p.id = e.payer_participant_id
		 WHERE e.itinerary_id = $1
		 ORDER BY e.expense_date DESC, e.created_at DESC`, itineraryID)
	if err != nil {
		return out, err
	}
	out.Budget.Expenses = make([]exportExpense, 0)
	expenseIndex := map[string]int{}
	for rows.Next() {
		var e exportExpense
		if err := rows.Scan(&e.ID, &e.Title, &e.Category, &e.OriginalAmount, &e.Currency,
			&e.FxRate, &e.AmountKes, &e.Date, &e.PayerParticipantID, &e.PayerName,
			&e.Notes, &e.Version, &e.Deleted, &e.CreatedBy, &e.UpdatedBy,
			&e.CreatedAt, &e.UpdatedAt, &e.DeletedAt); err != nil {
			rows.Close()
			return out, err
		}
		e.Shares = []exportShare{}
		e.Receipts = []exportReceipt{}
		expenseIndex[e.ID] = len(out.Budget.Expenses)
		out.Budget.Expenses = append(out.Budget.Expenses, e)
	}
	rows.Close()

	stage = "expenses"
	rows, err = h.pool.Query(ctx,
		`SELECT s.expense_id::text, s.participant_id::text, COALESCE(p.display_name, 'Former participant'),
		        s.share_kes, s.share_percent::float8
		 FROM expense_shares s
		 JOIN expenses e ON e.id = s.expense_id
		 LEFT JOIN trip_participants p ON p.id = s.participant_id
		 WHERE e.itinerary_id = $1`, itineraryID)
	if err != nil {
		return out, err
	}
	for rows.Next() {
		var expenseID string
		var share exportShare
		var percent *float64
		if err := rows.Scan(&expenseID, &share.ParticipantID, &share.ParticipantName,
			&share.AmountKes, &percent); err != nil {
			rows.Close()
			return out, err
		}
		share.Percent = percent
		if idx, ok := expenseIndex[expenseID]; ok {
			out.Budget.Expenses[idx].Shares = append(out.Budget.Expenses[idx].Shares, share)
		}
	}
	rows.Close()

	stage = "shares"
	rows, err = h.pool.Query(ctx,
		`SELECT r.id::text, r.expense_id::text, r.status, r.ocr_status,
		        COALESCE(r.ocr_merchant, ''), COALESCE(r.ocr_date::text, ''),
		        r.ocr_total::float8, COALESCE(r.ocr_currency, ''), r.ocr_confidence::float8,
		        COALESCE(r.created_by, ''), r.created_at, r.updated_at, r.deleted_at
		 FROM expense_receipts r
		 WHERE r.itinerary_id = $1
		 ORDER BY r.created_at DESC`, itineraryID)
	if err != nil {
		return out, err
	}
	for rows.Next() {
		var expenseID string
		var rec exportReceipt
		var total, confidence *float64
		if err := rows.Scan(&rec.ID, &expenseID, &rec.Status, &rec.OCRStatus,
			&rec.Merchant, &rec.Date, &total, &rec.Currency, &confidence,
			&rec.CreatedBy, &rec.CreatedAt, &rec.UpdatedAt, &rec.DeletedAt); err != nil {
			rows.Close()
			return out, err
		}
		rec.Total = total
		rec.Confidence = confidence
		if idx, ok := expenseIndex[expenseID]; ok {
			out.Budget.Expenses[idx].Receipts = append(out.Budget.Expenses[idx].Receipts, rec)
		}
	}
	rows.Close()

	stage = "receipts"
	rows, err = h.pool.Query(ctx,
		`SELECT s.id::text, s.from_participant_id::text, COALESCE(f.display_name, 'Former participant'),
		        s.to_participant_id::text, COALESCE(t.display_name, 'Former participant'),
		        s.amount_kes, s.settlement_date::text, COALESCE(s.note, ''),
		        COALESCE(s.created_by, ''), s.created_at, s.deleted_at
		 FROM settlements s
		 LEFT JOIN trip_participants f ON f.id = s.from_participant_id
		 LEFT JOIN trip_participants t ON t.id = s.to_participant_id
		 WHERE s.itinerary_id = $1
		 ORDER BY s.settlement_date DESC, s.created_at DESC`, itineraryID)
	if err != nil {
		return out, err
	}
	out.Budget.Settlements = make([]exportSettlement, 0)
	for rows.Next() {
		var s exportSettlement
		if err := rows.Scan(&s.ID, &s.FromParticipantID, &s.FromName,
			&s.ToParticipantID, &s.ToName, &s.AmountKes, &s.Date, &s.Note,
			&s.CreatedBy, &s.CreatedAt, &s.DeletedAt); err != nil {
			rows.Close()
			return out, err
		}
		out.Budget.Settlements = append(out.Budget.Settlements, s)
	}
	rows.Close()

	stage = "settlements"
	_, balances, err := loadBalanceData(ctx, h.pool, itineraryID)
	if err != nil {
		return out, err
	}
	out.Budget.Balances = make([]balanceResponse, 0, len(balances))
	out.Budget.Settled = true
	for _, participant := range participants {
		b := balances[participant.ID]
		if b == nil {
			continue
		}
		out.Budget.SpentKes += b.PaidKes
		if b.BalanceKes != 0 {
			out.Budget.Settled = false
		}
		out.Budget.Balances = append(out.Budget.Balances, *b)
	}
	out.Budget.ExpenseCount = len(out.Budget.Expenses)
	if out.Itinerary.BudgetTotalKes != nil {
		remaining := *out.Itinerary.BudgetTotalKes - out.Budget.SpentKes
		out.Budget.RemainingKes = &remaining
		active := 0
		for _, p := range participants {
			if !p.Removed {
				active++
			}
		}
		if active > 0 {
			perPerson := *out.Itinerary.BudgetTotalKes / int64(active)
			out.Budget.PerPersonKes = &perPerson
		}
	}

	stage = "balances"
	rows, err = h.pool.Query(ctx,
		`SELECT e.id::text, e.scope, e.action, COALESCE(e.summary, ''),
		        COALESCE(e.entity_type, ''), COALESCE(e.entity_id, ''),
		        COALESCE(e.actor_id, ''), COALESCE(u.name, 'Former member'),
		        e.before_json, e.after_json, COALESCE(e.revision_id::text, ''),
		        COALESCE(e.request_id, ''), e.created_at
		 FROM trip_events e LEFT JOIN users u ON u.id = e.actor_id
		 WHERE e.itinerary_id = $1 ORDER BY e.created_at DESC, e.id DESC`, itineraryID)
	if err != nil {
		return out, err
	}
	out.History = make([]exportEvent, 0)
	for rows.Next() {
		var ev exportEvent
		var before, after []byte
		if err := rows.Scan(&ev.ID, &ev.Scope, &ev.Action, &ev.Summary,
			&ev.EntityType, &ev.EntityID, &ev.ActorID, &ev.ActorName,
			&before, &after, &ev.RevisionID, &ev.RequestID, &ev.CreatedAt); err != nil {
			rows.Close()
			return out, err
		}
		if len(before) > 0 {
			ev.Before = json.RawMessage(before)
		}
		if len(after) > 0 {
			ev.After = json.RawMessage(after)
		}
		out.History = append(out.History, ev)
	}
	rows.Close()

	stage = "history"
	rows, err = h.pool.Query(ctx,
		`SELECT r.id::text, r.version, r.reason, COALESCE(r.checksum, ''),
		        COALESCE(r.actor_id, ''), COALESCE(u.name, 'Former member'),
		        r.snapshot::text, r.created_at
		 FROM plan_revisions r LEFT JOIN users u ON u.id = r.actor_id
		 WHERE r.itinerary_id = $1 ORDER BY r.version DESC`, itineraryID)
	if err != nil {
		return out, err
	}
	out.Revisions = make([]exportRevision, 0)
	for rows.Next() {
		var rev exportRevision
		var snapshot string
		if err := rows.Scan(&rev.ID, &rev.Version, &rev.Reason, &rev.Checksum,
			&rev.ActorID, &rev.ActorName, &snapshot, &rev.CreatedAt); err != nil {
			rows.Close()
			return out, err
		}
		rev.Snapshot = json.RawMessage(snapshot)
		out.Revisions = append(out.Revisions, rev)
	}
	rows.Close()

	out.Meta = exportMeta{
		ExportedBy:      userID,
		Source:          "ytci-go",
		IncludesDeleted: true,
		Note:            "Receipt binaries are not included. Deleted rows are retained in the audit export.",
	}
	return out, nil
}
