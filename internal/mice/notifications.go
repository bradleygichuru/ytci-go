package mice

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
)

const notificationDisclosure = "Event updates appear under Program & events in Alerts & updates."

// notificationPayload is stored in event_notifications.payload.
type notificationPayload struct {
	Title    string `json:"title"`
	Body     string `json:"body"`
	DeepLink string `json:"deepLink"`
}

type enqueueParams struct {
	EventID      string
	UserID       string
	Kind         string
	Title        string
	Body         string
	DeepLink     string
	EventVersion int
	DeliverAfter time.Time
	EventStart   time.Time
}

// enqueueNotification writes one durable Program-channel notification. Caps:
// one reminder per event (enforced by the unique key and a count), one change
// notice per event per day per user, and quiet hours 22:00-06:00 Africa/Nairobi
// shifting delivery to 06:00 (ADR 0011 §7).
func (h *Handler) enqueueNotification(ctx context.Context, tx dbTX, p enqueueParams) error {
	if !h.notificationsEnabled {
		return nil
	}
	if p.Kind == "" || p.EventID == "" || p.UserID == "" {
		return nil
	}
	deliver := p.DeliverAfter
	if deliver.IsZero() {
		deliver = h.clock()
	}
	deliver = maybeQuiet(deliver)
	status := "queued"
	if p.Kind == "reminder" && !p.EventStart.IsZero() && !deliver.Before(p.EventStart) {
		// A reminder that would land after the event starts is recorded as
		// skipped so the cap still holds and the inbox explains why.
		status = "skipped"
	}
	if p.Kind == "change_notice" {
		var today int
		err := tx.QueryRow(ctx, `
			SELECT count(*) FROM event_notifications
			 WHERE event_id=$1 AND user_id=$2 AND kind='change_notice'
			   AND created_at >= date_trunc('day', now())`, p.EventID, p.UserID).Scan(&today)
		if err != nil {
			return err
		}
		if today >= 1 {
			return nil
		}
	}
	payload, err := json.Marshal(notificationPayload{Title: p.Title, Body: p.Body, DeepLink: p.DeepLink})
	if err != nil {
		return err
	}
	_, err = tx.Exec(ctx, `
		INSERT INTO event_notifications
			(event_id, user_id, kind, event_version, deliver_after, status, payload)
		VALUES ($1,$2,$3,$4,$5,$6,$7)
		ON CONFLICT (event_id, user_id, kind, event_version) DO NOTHING`,
		p.EventID, p.UserID, p.Kind, p.EventVersion, deliver, status, payload)
	return err
}

// enqueueEventChangeNotice notifies every active registrant of one event.
func (h *Handler) enqueueEventChangeNotice(ctx context.Context, tx dbTX, eventID, title, body string, important bool) error {
	if !h.notificationsEnabled {
		return nil
	}
	var version int
	if err := tx.QueryRow(ctx, `SELECT COALESCE(version,1) FROM events WHERE id=$1`, eventID).Scan(&version); err != nil {
		return err
	}
	rows, err := tx.Query(ctx, `
		SELECT user_id FROM event_registrations
		 WHERE event_id=$1 AND status IN ('confirmed','checked_in')`, eventID)
	if err != nil {
		return err
	}
	users := []string{}
	for rows.Next() {
		var uid string
		if err := rows.Scan(&uid); err != nil {
			rows.Close()
			return err
		}
		users = append(users, uid)
	}
	rows.Close()
	for _, uid := range users {
		err := h.enqueueNotification(ctx, tx, enqueueParams{
			EventID:      eventID,
			UserID:       uid,
			Kind:         "change_notice",
			Title:        title,
			Body:         body,
			DeepLink:     "ytci://events/" + eventID,
			EventVersion: version,
		})
		if err != nil {
			return err
		}
	}
	_ = important
	return nil
}

// scheduleReminder writes the opt-in reminder for one registration.
func (h *Handler) scheduleReminder(ctx context.Context, tx dbTX, eventID, uid string, offsetMinutes *int, start time.Time) error {
	if offsetMinutes == nil {
		return nil
	}
	var version int
	if err := tx.QueryRow(ctx, `SELECT COALESCE(version,1) FROM events WHERE id=$1`, eventID).Scan(&version); err != nil {
		return err
	}
	return h.enqueueNotification(ctx, tx, enqueueParams{
		EventID:      eventID,
		UserID:       uid,
		Kind:         "reminder",
		Title:        "Event reminder",
		Body:         "Your event is coming up.",
		DeepLink:     "ytci://events/" + eventID,
		EventVersion: version,
		DeliverAfter: start.Add(-time.Duration(*offsetMinutes) * time.Minute),
		EventStart:   start,
	})
}

func (h *Handler) listNotifications(ctx context.Context, uid, eventID string, limit int, cursor *string) ([]EventNotification, error) {
	args := []any{uid, eventID}
	q := `SELECT id::text, kind, status, event_version, payload, created_at, read_at
	        FROM event_notifications WHERE user_id=$1 AND event_id=$2`
	if cursor != nil {
		if sortValue, id, ok := paginationDecode(*cursor); ok {
			args = append(args, sortValue, id)
			q += fmt.Sprintf(" AND (created_at, id) < ($%d::timestamptz, $%d::uuid)", len(args)-1, len(args))
		}
	}
	args = append(args, limit)
	q += fmt.Sprintf(" ORDER BY created_at DESC, id DESC LIMIT $%d", len(args))
	rows, err := h.pool.Query(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []EventNotification{}
	for rows.Next() {
		var n EventNotification
		var raw []byte
		var read pgtype.Timestamptz
		if err := rows.Scan(&n.ID, &n.Kind, &n.Status, &n.EventVersion, &raw, &n.CreatedAt, &read); err != nil {
			return nil, err
		}
		var p notificationPayload
		_ = json.Unmarshal(raw, &p)
		n.Title = p.Title
		n.Body = p.Body
		n.DeepLink = p.DeepLink
		n.ReadAt = nullTime(read)
		out = append(out, n)
	}
	return out, rows.Err()
}

func (h *Handler) handleListEventNotifications(w http.ResponseWriter, r *http.Request) {
	if !requireUser(w, r) {
		return
	}
	limit, cursor := parsePage(r)
	items, err := h.listNotifications(r.Context(), userID(r), chi.URLParam(r, "id"), limit+1, cursor)
	if err != nil {
		writeServer(w, r, err)
		return
	}
	writePage(w, limit, items, func(n EventNotification) (string, bool) {
		return paginationEncode(n.CreatedAt.UTC().Format(time.RFC3339Nano), n.ID), n.ID != ""
	})
}

// ---------------------------------------------------------------------------
// Change notices
// ---------------------------------------------------------------------------

type changeNoticeWrite struct {
	MaterialFields []string `json:"materialFields"`
	CopyEn         string   `json:"copyEn"`
	CopySw         string   `json:"copySw"`
	PreviewOnly    *bool    `json:"previewOnly"`
}

func (h *Handler) handleDraftChangeNotice(w http.ResponseWriter, r *http.Request) {
	if !requirePermission(w, r, "events.change_notice_draft") {
		return
	}
	eventID := chi.URLParam(r, "id")
	row, err := h.loadEventRow(r.Context(), eventID)
	if err != nil {
		if isNoRows(err) {
			writeErr(w, r, http.StatusNotFound, "EVENT_NOT_FOUND", "event not found", nil)
			return
		}
		writeServer(w, r, err)
		return
	}
	var body changeNoticeWrite
	if !decodeJSON(w, r, &body) {
		return
	}
	var failures []ValidationError
	if len(body.MaterialFields) == 0 {
		failures = append(failures, ValidationError{Field: "materialFields", Message: "at least one material field is required"})
	}
	for _, f := range body.MaterialFields {
		if !contains(materialFields, f) {
			failures = append(failures, ValidationError{Field: "materialFields", Message: "unknown material field " + f})
		}
	}
	if strings.TrimSpace(body.CopyEn) == "" {
		failures = append(failures, ValidationError{Field: "copyEn", Message: "English copy is required"})
	}
	if strings.TrimSpace(body.CopySw) == "" {
		failures = append(failures, ValidationError{Field: "copySw", Message: "Swahili copy is required"})
	}
	if len(failures) > 0 {
		writeValidation(w, failures...)
		return
	}
	previewOnly := true
	if body.PreviewOnly != nil {
		previewOnly = *body.PreviewOnly
	}
	if previewOnly {
		var lastVersion int
		_ = h.pool.QueryRow(r.Context(), `
			SELECT COALESCE(MAX(to_version),0) FROM event_change_notices WHERE event_id=$1`, eventID).Scan(&lastVersion)
		if row.Version <= lastVersion {
			writeErr(w, r, http.StatusConflict, "NO_MATERIAL_CHANGE",
				"the event has no material change since the last notice", nil)
			return
		}
	}

	status := "draft"
	if !previewOnly {
		// Sending needs the approve permission on an existing draft, and the
		// approver must differ from the drafter (reviewer is not the uploader).
		if !requirePermission(w, r, "events.change_notice_approve") {
			return
		}
		var drafter pgtype.Text
		var draftID pgtype.Text
		_ = h.pool.QueryRow(r.Context(), `
			SELECT id::text, created_by FROM event_change_notices
			 WHERE event_id=$1 AND status='draft'
			 ORDER BY created_at DESC LIMIT 1`, eventID).Scan(&draftID, &drafter)
		if nullText(draftID) == "" {
			writeErr(w, r, http.StatusConflict, "DRAFT_REQUIRED",
				"draft the change notice before approving it", nil)
			return
		}
		if nullText(drafter) == userID(r) {
			writeErr(w, r, http.StatusConflict, "REVIEWER_IS_UPLOADER",
				"the approver must differ from the notice drafter", nil)
			return
		}
		status = "approved"
	}

	tx, err := h.pool.Begin(r.Context())
	if err != nil {
		writeServer(w, r, err)
		return
	}
	defer tx.Rollback(r.Context())

	var notice ChangeNotice
	var copySw pgtype.Text
	err = tx.QueryRow(r.Context(), `
		INSERT INTO event_change_notices
			(event_id, from_version, to_version, material_fields, copy_en, copy_sw, status, created_by, approved_by)
		VALUES ($1,$2,$3,$4,$5,NULLIF($6,''),$7,$8,$9)
		RETURNING id::text, event_id::text, from_version, to_version, material_fields, copy_en, copy_sw, status`,
		eventID, row.Version, row.Version+1, body.MaterialFields, body.CopyEn, body.CopySw, status,
		userID(r), nullableActor(status, userID(r))).Scan(
		&notice.ID, &notice.EventID, &notice.FromVersion, &notice.ToVersion,
		&notice.MaterialFields, &notice.CopyEn, &copySw, &notice.Status)
	if err != nil {
		writeServer(w, r, err)
		return
	}
	notice.CopySw = nullText(copySw)

	if status == "approved" {
		// Mark sent immediately and fan out to registrants.
		if err := h.enqueueEventChangeNotice(r.Context(), tx, eventID, "Event changed", body.CopyEn, true); err != nil {
			writeServer(w, r, err)
			return
		}
		_, err = tx.Exec(r.Context(), `
			UPDATE event_change_notices SET status='sent', sent_at=now() WHERE id=$1`, notice.ID)
		if err != nil {
			writeServer(w, r, err)
			return
		}
		if _, err := tx.Exec(r.Context(), `
			UPDATE events SET version=GREATEST(version,$2), updated_at=now() WHERE id=$1`,
			eventID, notice.ToVersion); err != nil {
			writeServer(w, r, err)
			return
		}
		notice.Status = "sent"
	}
	if err := tx.Commit(r.Context()); err != nil {
		writeServer(w, r, err)
		return
	}
	_ = h.pool.QueryRow(r.Context(), `
		SELECT count(*) FROM event_registrations WHERE event_id=$1 AND status IN ('confirmed','checked_in')`,
		eventID).Scan(&notice.RecipientCount)
	writeJSON(w, http.StatusCreated, notice)
}

func nullableActor(status, actor string) any {
	if status == "approved" {
		return actor
	}
	return nil
}

var _ = pgx.ErrNoRows
