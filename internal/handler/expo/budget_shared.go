package expo

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/bradleygichuru/ytci-go/internal/handler"
	"github.com/bradleygichuru/ytci-go/internal/ocr"
	"github.com/bradleygichuru/ytci-go/internal/r2"
)

var errBudgetVersionConflict = errors.New("budget version conflict")

type BudgetHandler struct {
	pool *pgxpool.Pool
	r2   r2.Store
	ocr  ocr.Provider
}

func NewBudgetHandler(pool *pgxpool.Pool, r2client r2.Store, ocrProvider ocr.Provider) *BudgetHandler {
	return &BudgetHandler{pool: pool, r2: r2client, ocr: ocrProvider}
}

type participantRecord struct {
	ID        string
	UserID    string
	Name      string
	Kind      string
	Removed   bool
	CreatedAt time.Time
}

type participantResponse struct {
	ID      string `json:"id"`
	UserID  string `json:"userId,omitempty"`
	Name    string `json:"name"`
	Kind    string `json:"kind"`
	Removed bool   `json:"removed"`
}

type shareResponse struct {
	ParticipantID string   `json:"participantId"`
	AmountKes     int64    `json:"amountKes"`
	Percent       *float64 `json:"percent,omitempty"`
}

type receiptMeta struct {
	ID         string   `json:"id"`
	Status     string   `json:"status"`
	OCRStatus  string   `json:"ocrStatus"`
	Merchant   string   `json:"merchant,omitempty"`
	Date       string   `json:"date,omitempty"`
	Total      *float64 `json:"total,omitempty"`
	Currency   string   `json:"currency,omitempty"`
	Confidence *float64 `json:"confidence,omitempty"`
	URL        string   `json:"url,omitempty"`
}

type expenseResponse struct {
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
	Shares             []shareResponse `json:"shares"`
	Receipt            *receiptMeta    `json:"receipt,omitempty"`
}

type settlementResponse struct {
	ID                string `json:"id"`
	FromParticipantID string `json:"fromParticipantId"`
	FromName          string `json:"fromName"`
	ToParticipantID   string `json:"toParticipantId"`
	ToName            string `json:"toName"`
	AmountKes         int64  `json:"amountKes"`
	Date              string `json:"date"`
	Note              string `json:"note,omitempty"`
}

type balanceResponse struct {
	ParticipantID string `json:"participantId"`
	Name          string `json:"name"`
	Kind          string `json:"kind"`
	Removed       bool   `json:"removed"`
	PaidKes       int64  `json:"paidKes"`
	OwedKes       int64  `json:"owedKes"`
	BalanceKes    int64  `json:"balanceKes"`
}

type suggestedTransfer struct {
	FromParticipantID string `json:"fromParticipantId"`
	FromName          string `json:"fromName"`
	ToParticipantID   string `json:"toParticipantId"`
	ToName            string `json:"toName"`
	AmountKes         int64  `json:"amountKes"`
}

func loadParticipants(ctx context.Context, q dbQuerier, itineraryID string) ([]participantRecord, error) {
	rows, err := q.Query(ctx,
		`SELECT id::text, COALESCE(user_id, ''), display_name, kind,
		        (removed_at IS NOT NULL), created_at
		 FROM trip_participants
		 WHERE itinerary_id = $1
		 ORDER BY created_at, id`,
		itineraryID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := make([]participantRecord, 0)
	for rows.Next() {
		var p participantRecord
		if err := rows.Scan(&p.ID, &p.UserID, &p.Name, &p.Kind, &p.Removed, &p.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

func participantIndex(participants []participantRecord) map[string]participantRecord {
	index := make(map[string]participantRecord, len(participants))
	for _, p := range participants {
		index[p.ID] = p
	}
	return index
}

func participantOrder(participants []participantRecord) []string {
	order := make([]string, 0, len(participants))
	for _, p := range participants {
		order = append(order, p.ID)
	}
	return order
}

func participantResponses(participants []participantRecord) []participantResponse {
	out := make([]participantResponse, 0, len(participants))
	for _, p := range participants {
		out = append(out, participantResponse{
			ID: p.ID, UserID: p.UserID, Name: p.Name, Kind: p.Kind, Removed: p.Removed,
		})
	}
	return out
}

func lockBudgetVersion(ctx context.Context, tx pgx.Tx, itineraryID string, expected int) (int, error) {
	var current int
	err := tx.QueryRow(ctx,
		`SELECT budget_version FROM itineraries WHERE id = $1 AND deleted_at IS NULL FOR UPDATE`,
		itineraryID).Scan(&current)
	if errors.Is(err, pgx.ErrNoRows) {
		return 0, errItineraryNotFound
	}
	if err != nil {
		return 0, err
	}
	if expected > 0 && expected != current {
		return current, errBudgetVersionConflict
	}
	return current, nil
}

func bumpBudgetVersion(ctx context.Context, q dbQuerier, itineraryID string) (int, error) {
	var version int
	err := q.QueryRow(ctx,
		`UPDATE itineraries
		 SET budget_version = budget_version + 1, updated_at = now()
		 WHERE id = $1 AND deleted_at IS NULL
		 RETURNING budget_version`,
		itineraryID).Scan(&version)
	if errors.Is(err, pgx.ErrNoRows) {
		return 0, errItineraryNotFound
	}
	return version, err
}

func writeBudgetVersionConflict(w http.ResponseWriter, current int) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusConflict)
	json.NewEncoder(w).Encode(map[string]any{
		"error": map[string]string{
			"code":    "VERSION_CONFLICT",
			"message": "The budget changed while you were editing.",
		},
		"scope":          "budget",
		"currentVersion": current,
	})
}

func recordBudgetEvent(ctx context.Context, q dbQuerier, itineraryID, actorID, action, entityType, entityID, summary string, before, after any) error {
	return recordTripEvent(ctx, q, itineraryID, actorID, "budget", action, entityType, entityID, summary, before, after, "")
}

func parseExpenseDate(raw string) (time.Time, error) {
	if raw == "" {
		now := time.Now().UTC()
		return time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, time.UTC), nil
	}
	t, err := time.Parse("2006-01-02", raw)
	if err != nil {
		return time.Time{}, fmt.Errorf("invalid date: %w", err)
	}
	return t, nil
}

func receiptURL(ctx context.Context, store r2.Store, objectKey string) string {
	if store == nil || objectKey == "" {
		return ""
	}
	u, err := store.PresignedGetURL(ctx, objectKey, 15*time.Minute)
	if err != nil {
		return ""
	}
	return u
}

func writeBudgetError(w http.ResponseWriter, r *http.Request, err error) {
	switch {
	case errors.Is(err, errItineraryNotFound):
		handler.WriteError(w, http.StatusNotFound, "NOT_FOUND", "itinerary not found")
	case errors.Is(err, errItineraryForbidden):
		handler.WriteError(w, http.StatusForbidden, "FORBIDDEN", "you do not have permission to do that")
	default:
		handler.WriteServerError(w, r, "INTERNAL_ERROR", "budget request failed", err)
	}
}
