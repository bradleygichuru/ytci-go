package mice

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5/pgtype"

	authmw "github.com/bradleygichuru/ytci-go/internal/middleware"
)

// ---------------------------------------------------------------------------
// Append-only admin audit
// ---------------------------------------------------------------------------

type auditEntry struct {
	EntityType string
	EntityID   string
	EventID    string
	Action     string
	Summary    string
	Details    map[string]any
}

// writeAudit appends one row to mice_audit_events. It is called inside the
// same transaction as the write it records, so an action either lands with its
// audit row or not at all.
func (h *Handler) writeAudit(ctx context.Context, tx dbTX, entry auditEntry) error {
	details := entry.Details
	if details == nil {
		details = map[string]any{}
	}
	raw, err := json.Marshal(details)
	if err != nil {
		return err
	}
	_, err = tx.Exec(ctx, `
		INSERT INTO mice_audit_events (entity_type, entity_id, event_id, action, actor_id, summary, details)
		VALUES ($1,$2,NULLIF($3,'')::uuid,$4,NULLIF($5,''),$6,$7)`,
		entry.EntityType, entry.EntityID, entry.EventID, entry.Action, userIDFromContext(ctx), entry.Summary, raw)
	return err
}

// userIDFromContext reads the authenticated actor stored by the auth
// middleware so audit helpers do not need a second parameter.
func userIDFromContext(ctx context.Context) string {
	return authmw.UserID(ctx)
}

// ---------------------------------------------------------------------------
// Shared helpers
// ---------------------------------------------------------------------------

// loadAdminRegistration returns one registration with its attendee row.
func (h *Handler) loadAdminRegistration(ctx context.Context, db dbTX, eventID, registrationID string) (*AdminRegistration, error) {
	var a AdminRegistration
	var answers []byte
	var reminder, position pgtype.Int4
	var confirmBy, cancelledAt pgtype.Timestamptz
	var name, email pgtype.Text
	err := db.QueryRow(ctx, `
		SELECT `+registrationColumns+`, u.name, u.email
		  FROM event_registrations r
		  LEFT JOIN users u ON u.id = r.user_id
		 WHERE r.event_id=$1 AND r.id=$2`, eventID, registrationID).Scan(
		&a.ID, &a.EventID, &a.Status, &answers, &reminder, &position,
		&confirmBy, &a.CreatedAt, &cancelledAt, &name, &email)
	if err != nil {
		return nil, err
	}
	if len(answers) > 0 {
		_ = json.Unmarshal(answers, &a.Answers)
	}
	a.ReminderOffsetMinutes = nullInt(reminder)
	a.WaitlistPosition = nullInt(position)
	a.ConfirmBy = nullTime(confirmBy)
	a.CancelledAt = nullTime(cancelledAt)
	a.Attendee = &AttendeeRef{DisplayName: nullText(name), Email: nullText(email)}
	if len(a.Answers) > 0 {
		parts := []string{}
		for k, v := range a.Answers {
			parts = append(parts, k+": "+v)
		}
		a.AnswersSummary = strings.Join(parts, "; ")
	}
	return &a, nil
}

func (h *Handler) eventTitle(ctx context.Context, db dbTX, eventID string) string {
	var title string
	_ = db.QueryRow(ctx, `SELECT title FROM events WHERE id=$1`, eventID).Scan(&title)
	return title
}

func validateReason(reason, field string, min int) []ValidationError {
	trimmed := strings.TrimSpace(reason)
	if len(trimmed) < min {
		return []ValidationError{{Field: field, Message: fmt.Sprintf("a reason of at least %d characters is required", min)}}
	}
	if len(trimmed) > 500 {
		return []ValidationError{{Field: field, Message: "must be at most 500 characters"}}
	}
	return nil
}

// ---------------------------------------------------------------------------
// Event lifecycle
// ---------------------------------------------------------------------------

type lifecycleRequest struct {
	Action string `json:"action"`
	Reason string `json:"reason"`
}

var lifecycleActions = []string{"cancel", "unpublish", "archive", "publish", "restore"}

func (h *Handler) handleAdminEventLifecycle(w http.ResponseWriter, r *http.Request) {
	if !requirePermission(w, r, "events.publish") {
		return
	}
	id := chi.URLParam(r, "id")
	var body lifecycleRequest
	if !decodeJSON(w, r, &body) {
		return
	}
	if !contains(lifecycleActions, body.Action) {
		writeValidation(w, ValidationError{Field: "action", Message: "must be one of cancel, unpublish, archive, publish, restore"})
		return
	}
	if failures := validateReason(body.Reason, "reason", 3); len(failures) > 0 {
		writeValidation(w, failures...)
		return
	}
	row, err := h.loadEventRow(r.Context(), id)
	if err != nil {
		if isNoRows(err) {
			writeErr(w, r, http.StatusNotFound, "EVENT_NOT_FOUND", "event not found", nil)
			return
		}
		writeServer(w, r, err)
		return
	}
	status := normaliseStatus(row.Status)

	var next string
	switch body.Action {
	case "cancel":
		if status == "cancelled" {
			writeErr(w, r, http.StatusConflict, "EVENT_ALREADY_CANCELLED", "this event is already cancelled", nil)
			return
		}
		next = "cancelled"
	case "unpublish":
		if status == "draft" {
			writeErr(w, r, http.StatusConflict, "EVENT_ALREADY_DRAFT", "this event is already unpublished", nil)
			return
		}
		next = "draft"
	case "archive":
		if status == "archived" {
			writeErr(w, r, http.StatusConflict, "EVENT_ALREADY_ARCHIVED", "this event is already archived", nil)
			return
		}
		next = "archived"
	case "publish", "restore":
		if status == "published" {
			writeErr(w, r, http.StatusConflict, "EVENT_ALREADY_PUBLISHED", "this event is already published", nil)
			return
		}
		if failures := readinessFailures(row); len(failures) > 0 {
			writeErr(w, r, http.StatusConflict, failures[0].Field, failures[0].Message, nil)
			return
		}
		paid := row.PriceAmount.Valid || row.RegistrationMode == "ticket_handoff"
		if paid && !can(r, "events.publish_paid") {
			writeErr(w, r, http.StatusForbidden, "FORBIDDEN", "paid events require events.publish_paid", nil)
			return
		}
		next = "published"
	}

	tx, err := h.pool.Begin(r.Context())
	if err != nil {
		writeServer(w, r, err)
		return
	}
	defer tx.Rollback(r.Context())

	tag, err := tx.Exec(r.Context(), `
		UPDATE events SET status=$2, version=version+1, updated_at=now(),
		       published_at=CASE WHEN $2='published' THEN COALESCE(published_at, now()) ELSE published_at END
		 WHERE id=$1`, id, next)
	if err != nil {
		writeServer(w, r, err)
		return
	}
	if tag.RowsAffected() == 0 {
		writeErr(w, r, http.StatusNotFound, "EVENT_NOT_FOUND", "event not found", nil)
		return
	}
	action := "event." + body.Action
	summary := fmt.Sprintf("Event %s: %s", body.Action, next)
	if body.Action == "publish" {
		summary = "Event published"
	} else if body.Action == "restore" {
		summary = "Event restored to published"
	}
	if err := h.writeAudit(r.Context(), tx, auditEntry{
		EntityType: "event", EntityID: id, EventID: id, Action: action,
		Summary: summary,
		Details: map[string]any{"from": status, "to": next, "reason": strings.TrimSpace(body.Reason)},
	}); err != nil {
		writeServer(w, r, err)
		return
	}
	if err := tx.Commit(r.Context()); err != nil {
		writeServer(w, r, err)
		return
	}
	updated, err := h.loadEventRow(r.Context(), id)
	if err != nil {
		writeServer(w, r, err)
		return
	}
	detail, err := h.buildEventDetail(r.Context(), updated, userID(r), false)
	if err != nil {
		writeServer(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, detail)
}

// ---------------------------------------------------------------------------
// Waitlist offers
// ---------------------------------------------------------------------------

type waitlistOfferRequest struct {
	RegistrationID string `json:"registrationId"`
	ExpiresAt      string `json:"expiresAt"`
	Note           string `json:"note"`
}

func (h *Handler) handleAdminCreateWaitlistOffer(w http.ResponseWriter, r *http.Request) {
	if !requirePermission(w, r, "events.edit_draft") {
		return
	}
	eventID := chi.URLParam(r, "id")
	var body waitlistOfferRequest
	if !decodeJSON(w, r, &body) {
		return
	}
	row, err := h.loadEventRow(r.Context(), eventID)
	if err != nil {
		if isNoRows(err) {
			writeErr(w, r, http.StatusNotFound, "EVENT_NOT_FOUND", "event not found", nil)
			return
		}
		writeServer(w, r, err)
		return
	}
	now := h.clock()
	expires := minTime(now.Add(24*time.Hour), row.startsAt())
	if body.ExpiresAt != "" {
		parsed, err := time.Parse(time.RFC3339, body.ExpiresAt)
		if err != nil {
			writeValidation(w, ValidationError{Field: "expiresAt", Message: "must be an RFC 3339 timestamp"})
			return
		}
		expires = parsed
	}
	if !expires.After(now) {
		writeValidation(w, ValidationError{Field: "expiresAt", Message: "must be in the future"})
		return
	}
	if expires.After(row.startsAt()) {
		expires = row.startsAt()
	}
	if body.Note != "" && len(body.Note) > 500 {
		writeValidation(w, ValidationError{Field: "note", Message: "must be at most 500 characters"})
		return
	}

	tx, err := h.pool.Begin(r.Context())
	if err != nil {
		writeServer(w, r, err)
		return
	}
	defer tx.Rollback(r.Context())

	var targetID, attendeeID, attendeeName string
	existingOffer := false
	if body.RegistrationID != "" {
		if !validUUID(body.RegistrationID) {
			writeValidation(w, ValidationError{Field: "registrationId", Message: "must be a UUID"})
			return
		}
		err = tx.QueryRow(r.Context(), `
			SELECT r.id::text, r.user_id, COALESCE(u.name, u.email, r.user_id),
			       (r.confirm_by IS NOT NULL AND r.confirm_by > $3)
			  FROM event_registrations r
			  LEFT JOIN users u ON u.id = r.user_id
			 WHERE r.event_id=$1 AND r.id=$2 AND r.status='waitlisted'
			 FOR UPDATE OF r`, eventID, body.RegistrationID, now).Scan(&targetID, &attendeeID, &attendeeName, &existingOffer)
	} else {
		err = tx.QueryRow(r.Context(), `
			SELECT r.id::text, r.user_id, COALESCE(u.name, u.email, r.user_id),
			       (r.confirm_by IS NOT NULL AND r.confirm_by > $2)
			  FROM event_registrations r
			  LEFT JOIN users u ON u.id = r.user_id
			 WHERE r.event_id=$1 AND r.status='waitlisted'
			 ORDER BY r.waitlist_position ASC NULLS LAST, r.created_at ASC
			 LIMIT 1 FOR UPDATE OF r SKIP LOCKED`, eventID, now).Scan(&targetID, &attendeeID, &attendeeName, &existingOffer)
	}
	if err != nil {
		if isNoRows(err) {
			writeErr(w, r, http.StatusConflict, "NO_WAITLIST_ENTRY", "there is no waitlisted registration to offer", nil)
			return
		}
		writeServer(w, r, err)
		return
	}
	_ = attendeeID
	if existingOffer {
		writeErr(w, r, http.StatusConflict, "OFFER_ALREADY_OPEN",
			"this registration already has an open offer", nil)
		return
	}
	// One open offer per event: an earlier promise must be accepted or
	// withdrawn before another is sent.
	var openOffer bool
	if err := tx.QueryRow(r.Context(), `
		SELECT EXISTS(
			SELECT 1 FROM event_registrations
			 WHERE event_id=$1 AND status='waitlisted' AND confirm_by IS NOT NULL
			   AND confirm_by > $2)`, eventID, now).Scan(&openOffer); err != nil {
		writeServer(w, r, err)
		return
	}
	if openOffer {
		writeErr(w, r, http.StatusConflict, "OFFER_ALREADY_OPEN",
			"an offer is already open for this event; accept or withdraw it first", nil)
		return
	}

	// A seat must be free. The confirmed count excludes the target (still
	// waitlisted) and every other waitlisted row.
	var going int
	if err := tx.QueryRow(r.Context(), `
		SELECT count(*) FROM event_registrations
		 WHERE event_id=$1 AND status IN ('confirmed','checked_in')`, eventID).Scan(&going); err != nil {
		writeServer(w, r, err)
		return
	}
	if row.Capacity.Valid && going >= int(row.Capacity.Int32) {
		writeErr(w, r, http.StatusConflict, "NO_SEAT_AVAILABLE",
			"there is no free seat to offer", map[string]any{"capacity": row.Capacity.Int32, "confirmed": going})
		return
	}

	if _, err := tx.Exec(r.Context(), `
		UPDATE event_registrations SET confirm_by=$2, updated_at=now()
		 WHERE id=$1`, targetID, expires); err != nil {
		writeServer(w, r, err)
		return
	}
	if err := h.writeAudit(r.Context(), tx, auditEntry{
		EntityType: "registration", EntityID: targetID, EventID: eventID,
		Action:  "waitlist.offer_sent",
		Summary: fmt.Sprintf("Waitlist offer sent to %s", attendeeName),
		Details: map[string]any{"expiresAt": expires.UTC().Format(time.RFC3339), "note": strings.TrimSpace(body.Note)},
	}); err != nil {
		writeServer(w, r, err)
		return
	}
	if err := tx.Commit(r.Context()); err != nil {
		writeServer(w, r, err)
		return
	}
	writeJSON(w, http.StatusCreated, WaitlistOffer{
		EventID:        eventID,
		RegistrationID: targetID,
		AttendeeName:   attendeeName,
		OfferedAt:      now,
		ExpiresAt:      expires,
		OfferedBy:      userID(r),
		Note:           strings.TrimSpace(body.Note),
	})
}

type waitlistDecisionRequest struct {
	Decision string `json:"decision"`
	Reason   string `json:"reason"`
}

func (h *Handler) handleAdminWaitlistOfferDecision(w http.ResponseWriter, r *http.Request) {
	if !requirePermission(w, r, "events.edit_draft") {
		return
	}
	eventID := chi.URLParam(r, "id")
	registrationID := chi.URLParam(r, "registrationId")
	var body waitlistDecisionRequest
	if !decodeJSON(w, r, &body) {
		return
	}
	if body.Decision != "accept" && body.Decision != "withdraw" {
		writeValidation(w, ValidationError{Field: "decision", Message: "must be accept or withdraw"})
		return
	}
	if body.Decision == "withdraw" && len(strings.TrimSpace(body.Reason)) > 500 {
		writeValidation(w, ValidationError{Field: "reason", Message: "must be at most 500 characters"})
		return
	}
	ctx := r.Context()
	tx, err := h.pool.Begin(ctx)
	if err != nil {
		writeServer(w, r, err)
		return
	}
	defer tx.Rollback(ctx)

	var status string
	var confirmBy pgtype.Timestamptz
	err = tx.QueryRow(ctx, `
		SELECT status, confirm_by FROM event_registrations
		 WHERE event_id=$1 AND id=$2 FOR UPDATE`, eventID, registrationID).Scan(&status, &confirmBy)
	if err != nil {
		if isNoRows(err) {
			writeErr(w, r, http.StatusNotFound, "REGISTRATION_NOT_FOUND", "registration not found", nil)
			return
		}
		writeServer(w, r, err)
		return
	}
	now := h.clock()
	switch body.Decision {
	case "accept":
		if status != "waitlisted" || !confirmBy.Valid {
			writeErr(w, r, http.StatusConflict, "NO_OPEN_OFFER", "this registration has no open offer", nil)
			return
		}
		if !confirmBy.Time.After(now) {
			writeErr(w, r, http.StatusConflict, "OFFER_EXPIRED", "the offer window has closed", nil)
			return
		}
		if _, err := tx.Exec(ctx, `
			UPDATE event_registrations SET status='confirmed', waitlist_position=NULL,
				confirm_by=NULL, promotion_notified_at=now(), cancelled_at=NULL,
				cancel_reason=NULL, updated_at=now()
			 WHERE id=$1`, registrationID); err != nil {
			writeServer(w, r, err)
			return
		}
	case "withdraw":
		if status == "waitlisted" {
			if !confirmBy.Valid {
				writeErr(w, r, http.StatusConflict, "NO_OPEN_OFFER", "this registration has no open offer", nil)
				return
			}
			if _, err := tx.Exec(ctx, `
				UPDATE event_registrations SET confirm_by=NULL, updated_at=now()
				 WHERE id=$1`, registrationID); err != nil {
				writeServer(w, r, err)
				return
			}
		} else if status == "confirmed" && confirmBy.Valid {
			// An automatically promoted seat is withdrawn: return it to the
			// queue and promote the next person.
			if _, err := tx.Exec(ctx, `
				UPDATE event_registrations SET status='cancelled', cancelled_at=now(),
					cancel_reason='offer_withdrawn', confirm_by=NULL, waitlist_position=NULL, updated_at=now()
				 WHERE id=$1`, registrationID); err != nil {
				writeServer(w, r, err)
				return
			}
			event, err := lockEvent(ctx, tx, eventID)
			if err != nil {
				writeServer(w, r, err)
				return
			}
			if err := h.promoteWaitlist(ctx, tx, eventID, event, now); err != nil {
				writeServer(w, r, err)
				return
			}
		} else {
			writeErr(w, r, http.StatusConflict, "NO_OPEN_OFFER", "this registration has no open offer", nil)
			return
		}
	}
	action := "waitlist.offer_" + body.Decision + "ed"
	summary := fmt.Sprintf("Waitlist offer %s", body.Decision+"ed")
	if body.Decision == "accept" {
		summary = "Waitlist offer accepted"
	} else {
		summary = "Waitlist offer withdrawn"
	}
	if err := h.writeAudit(ctx, tx, auditEntry{
		EntityType: "registration", EntityID: registrationID, EventID: eventID,
		Action: action, Summary: summary,
		Details: map[string]any{"decision": body.Decision, "reason": strings.TrimSpace(body.Reason)},
	}); err != nil {
		writeServer(w, r, err)
		return
	}
	if err := tx.Commit(ctx); err != nil {
		writeServer(w, r, err)
		return
	}
	reg, err := h.loadAdminRegistration(ctx, h.pool, eventID, registrationID)
	if err != nil {
		writeServer(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, reg)
}

// ---------------------------------------------------------------------------
// Check-in undo
// ---------------------------------------------------------------------------

type checkInUndoRequest struct {
	Reason string `json:"reason"`
}

func (h *Handler) handleAdminUndoCheckIn(w http.ResponseWriter, r *http.Request) {
	if !requirePermission(w, r, "events.override_checkin") {
		return
	}
	eventID := chi.URLParam(r, "id")
	checkInID := chi.URLParam(r, "checkInId")
	var body checkInUndoRequest
	if !decodeJSON(w, r, &body) {
		return
	}
	if failures := validateReason(body.Reason, "reason", 3); len(failures) > 0 {
		writeValidation(w, failures...)
		return
	}
	ctx := r.Context()
	tx, err := h.pool.Begin(ctx)
	if err != nil {
		writeServer(w, r, err)
		return
	}
	defer tx.Rollback(ctx)

	var registrationID string
	err = tx.QueryRow(ctx, `
		DELETE FROM event_checkins WHERE id=$1 AND event_id=$2
		RETURNING registration_id::text`, checkInID, eventID).Scan(&registrationID)
	if err != nil {
		if isNoRows(err) {
			writeErr(w, r, http.StatusNotFound, "CHECKIN_NOT_FOUND", "check-in not found", nil)
			return
		}
		writeServer(w, r, err)
		return
	}
	if _, err := tx.Exec(ctx, `
		UPDATE event_registrations SET status='confirmed', checked_in_at=NULL, updated_at=now()
		 WHERE id=$1 AND status='checked_in'`, registrationID); err != nil {
		writeServer(w, r, err)
		return
	}
	var attendeeName string
	_ = tx.QueryRow(ctx, `
		SELECT COALESCE(u.name, u.email, r.user_id)
		  FROM event_registrations r LEFT JOIN users u ON u.id = r.user_id
		 WHERE r.id=$1`, registrationID).Scan(&attendeeName)
	if err := h.writeAudit(ctx, tx, auditEntry{
		EntityType: "checkin", EntityID: checkInID, EventID: eventID,
		Action:  "checkin.undone",
		Summary: "Check-in undone",
		Details: map[string]any{
			"registrationId": registrationID,
			"reason":         strings.TrimSpace(body.Reason),
			"attendeeName":   attendeeName,
		},
	}); err != nil {
		writeServer(w, r, err)
		return
	}
	if err := tx.Commit(ctx); err != nil {
		writeServer(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, Acknowledgement{OK: true, ID: registrationID, Message: "check-in undone"})
}

// ---------------------------------------------------------------------------
// Registration correction
// ---------------------------------------------------------------------------

type registrationWrite struct {
	Status  *string           `json:"status"`
	Answers map[string]string `json:"answers"`
	Reason  string            `json:"reason"`
}

func (h *Handler) handleAdminUpdateRegistration(w http.ResponseWriter, r *http.Request) {
	if !requirePermission(w, r, "events.edit_draft") {
		return
	}
	eventID := chi.URLParam(r, "id")
	registrationID := chi.URLParam(r, "registrationId")
	var body registrationWrite
	if !decodeJSON(w, r, &body) {
		return
	}
	if failures := validateReason(body.Reason, "reason", 3); len(failures) > 0 {
		writeValidation(w, failures...)
		return
	}
	if body.Status != nil && !contains([]string{"confirmed", "waitlisted", "cancelled"}, *body.Status) {
		writeValidation(w, ValidationError{Field: "status", Message: "must be confirmed, waitlisted or cancelled"})
		return
	}
	if body.Status == nil && body.Answers == nil {
		writeValidation(w, ValidationError{Field: "status", Message: "status or answers is required"})
		return
	}
	ctx := r.Context()

	tx, err := h.pool.Begin(ctx)
	if err != nil {
		writeServer(w, r, err)
		return
	}
	defer tx.Rollback(ctx)

	var currentStatus string
	var currentAnswersRaw []byte
	err = tx.QueryRow(ctx, `
		SELECT status, answers FROM event_registrations
		 WHERE event_id=$1 AND id=$2 FOR UPDATE`, eventID, registrationID).Scan(&currentStatus, &currentAnswersRaw)
	if err != nil {
		if isNoRows(err) {
			writeErr(w, r, http.StatusNotFound, "REGISTRATION_NOT_FOUND", "registration not found", nil)
			return
		}
		writeServer(w, r, err)
		return
	}
	currentAnswers := map[string]string{}
	if len(currentAnswersRaw) > 0 {
		_ = json.Unmarshal(currentAnswersRaw, &currentAnswers)
	}

	var answers map[string]string
	if body.Answers != nil {
		questions, qerr := h.loadRegistrationQuestions(ctx, eventID)
		if qerr != nil {
			writeServer(w, r, qerr)
			return
		}
		byID := map[string]RegistrationQuestion{}
		for _, q := range questions {
			byID[q.ID] = q
		}
		merged := map[string]string{}
		for k, v := range currentAnswers {
			merged[k] = v
		}
		var failures []ValidationError
		for key, value := range body.Answers {
			q, known := byID[key]
			if !known {
				// Legacy answers predate the question table. Correcting an
				// existing key is allowed; inventing a new one is not.
				if _, legacy := currentAnswers[key]; !legacy {
					failures = append(failures, ValidationError{Field: "answers." + key, Message: "unknown question"})
				}
				merged[key] = value
				continue
			}
			if q.AnswerKind == "single_choice" && len(q.Choices) > 0 && !contains(q.Choices, value) {
				failures = append(failures, ValidationError{Field: "answers." + key, Message: "must be one of the offered choices"})
			}
			if q.AnswerKind == "boolean" && value != "true" && value != "false" {
				failures = append(failures, ValidationError{Field: "answers." + key, Message: "must be true or false"})
			}
			merged[key] = value
		}
		for _, q := range questions {
			if q.Required && merged[q.ID] == "" {
				failures = append(failures, ValidationError{Field: "answers." + q.ID, Message: "this question is required"})
			}
		}
		if len(failures) > 0 {
			writeValidation(w, failures...)
			return
		}
		answers = merged
	}
	next := currentStatus
	if body.Status != nil {
		next = *body.Status
	}
	if currentStatus == "checked_in" && next != "checked_in" {
		writeErr(w, r, http.StatusConflict, "UNDO_CHECKIN_REQUIRED",
			"undo the check-in before changing this registration", nil)
		return
	}

	promote := false
	switch {
	case body.Status != nil && currentStatus != *body.Status:
		switch *body.Status {
		case "cancelled":
			if _, err := tx.Exec(ctx, `
				UPDATE event_registrations SET status='cancelled', cancelled_at=now(),
					cancel_reason='admin_correction', waitlist_position=NULL, confirm_by=NULL, updated_at=now()
				 WHERE id=$1`, registrationID); err != nil {
				writeServer(w, r, err)
				return
			}
			promote = currentStatus == "confirmed"
		case "waitlisted":
			var position int
			if err := tx.QueryRow(ctx, `
				SELECT COALESCE(MAX(waitlist_position),0)+1 FROM event_registrations
				 WHERE event_id=$1 AND status='waitlisted'`, eventID).Scan(&position); err != nil {
				writeServer(w, r, err)
				return
			}
			if _, err := tx.Exec(ctx, `
				UPDATE event_registrations SET status='waitlisted', waitlist_position=$2,
					confirm_by=NULL, cancelled_at=NULL, cancel_reason=NULL, updated_at=now()
				 WHERE id=$1`, registrationID, position); err != nil {
				writeServer(w, r, err)
				return
			}
		case "confirmed":
			if _, err := tx.Exec(ctx, `
				UPDATE event_registrations SET status='confirmed', waitlist_position=NULL,
					confirm_by=NULL, cancelled_at=NULL, cancel_reason=NULL, updated_at=now()
				 WHERE id=$1`, registrationID); err != nil {
				writeServer(w, r, err)
				return
			}
		}
	}
	if body.Answers != nil {
		if _, err := tx.Exec(ctx, `
			UPDATE event_registrations SET answers=$2::jsonb, updated_at=now()
			 WHERE id=$1`, registrationID, answerJSON(answers)); err != nil {
			writeServer(w, r, err)
			return
		}
	}
	if promote {
		event, err := lockEvent(ctx, tx, eventID)
		if err != nil {
			writeServer(w, r, err)
			return
		}
		if err := h.promoteWaitlist(ctx, tx, eventID, event, h.clock()); err != nil {
			writeServer(w, r, err)
			return
		}
	}
	if err := h.writeAudit(ctx, tx, auditEntry{
		EntityType: "registration", EntityID: registrationID, EventID: eventID,
		Action:  "registration." + map[bool]string{true: "cancelled", false: "updated"}[next == "cancelled"],
		Summary: "Registration " + map[bool]string{true: "cancelled", false: "corrected"}[next == "cancelled"],
		Details: map[string]any{
			"from":   currentStatus,
			"to":     next,
			"reason": strings.TrimSpace(body.Reason),
			"answers": func() any {
				if answers == nil {
					return nil
				}
				return answers
			}(),
		},
	}); err != nil {
		writeServer(w, r, err)
		return
	}
	if err := tx.Commit(ctx); err != nil {
		writeServer(w, r, err)
		return
	}
	reg, err := h.loadAdminRegistration(ctx, h.pool, eventID, registrationID)
	if err != nil {
		writeServer(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, reg)
}

// ---------------------------------------------------------------------------
// Speaker edit / consent / remove
// ---------------------------------------------------------------------------

func (h *Handler) handleAdminUpdatePerson(w http.ResponseWriter, r *http.Request) {
	if !requirePermission(w, r, "events.edit_draft") {
		return
	}
	eventID := chi.URLParam(r, "id")
	personID := chi.URLParam(r, "personId")
	if !h.eventExists(r, eventID) {
		writeErr(w, r, http.StatusNotFound, "EVENT_NOT_FOUND", "event not found", nil)
		return
	}
	var body personWrite
	if !decodeJSON(w, r, &body) {
		return
	}
	if strings.TrimSpace(body.DisplayName) == "" {
		writeValidation(w, ValidationError{Field: "displayName", Message: "displayName is required"})
		return
	}
	isPublic := true
	if body.IsPublic != nil {
		isPublic = *body.IsPublic
	}
	ctx := r.Context()
	tx, err := h.pool.Begin(ctx)
	if err != nil {
		writeServer(w, r, err)
		return
	}
	defer tx.Rollback(ctx)

	var s Speaker
	var title, org, photo pgtype.Text
	err = tx.QueryRow(ctx, `
		UPDATE event_people SET display_name=$2, title=NULLIF($3,''), organization=NULLIF($4,''),
			bio=NULLIF($5,''), photo_url=NULLIF($6,''), is_public=$7, updated_at=now()
		 WHERE id=$1
		RETURNING id::text, display_name, title, organization, photo_url, consent_status`,
		personID, body.DisplayName, body.Title, body.Organization, body.Bio, body.PhotoURL, isPublic).Scan(
		&s.ID, &s.DisplayName, &title, &org, &photo, &s.ConsentStatus)
	if err != nil {
		if isNoRows(err) {
			writeErr(w, r, http.StatusNotFound, "PERSON_NOT_FOUND", "speaker not found", nil)
			return
		}
		writeServer(w, r, err)
		return
	}
	s.Title = nullText(title)
	s.Organization = nullText(org)
	s.PhotoURL = nullText(photo)
	if err := h.writeAudit(ctx, tx, auditEntry{
		EntityType: "speaker", EntityID: personID, EventID: eventID,
		Action:  "speaker.updated",
		Summary: "Speaker record updated",
		Details: map[string]any{"displayName": s.DisplayName, "isPublic": isPublic},
	}); err != nil {
		writeServer(w, r, err)
		return
	}
	if err := tx.Commit(ctx); err != nil {
		writeServer(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, s)
}

type speakerConsentRequest struct {
	Consent string `json:"consent"`
	Note    string `json:"note"`
}

func (h *Handler) handleAdminSetSpeakerConsent(w http.ResponseWriter, r *http.Request) {
	if !requirePermission(w, r, "events.edit_draft") {
		return
	}
	eventID := chi.URLParam(r, "id")
	personID := chi.URLParam(r, "personId")
	if !h.eventExists(r, eventID) {
		writeErr(w, r, http.StatusNotFound, "EVENT_NOT_FOUND", "event not found", nil)
		return
	}
	var body speakerConsentRequest
	if !decodeJSON(w, r, &body) {
		return
	}
	if body.Consent == "withdrawn" {
		body.Consent = "declined"
	}
	if !contains([]string{"pending", "granted", "declined"}, body.Consent) {
		writeValidation(w, ValidationError{Field: "consent", Message: "must be pending, granted or declined"})
		return
	}
	if len(strings.TrimSpace(body.Note)) > 500 {
		writeValidation(w, ValidationError{Field: "note", Message: "must be at most 500 characters"})
		return
	}
	ctx := r.Context()
	tx, err := h.pool.Begin(ctx)
	if err != nil {
		writeServer(w, r, err)
		return
	}
	defer tx.Rollback(ctx)

	var s Speaker
	var title, org, photo pgtype.Text
	isPublic := body.Consent != "declined"
	err = tx.QueryRow(ctx, `
		UPDATE event_people SET consent_status=$2, consent_updated_at=now(), consent_updated_by=$3,
			is_public=$4, updated_at=now()
		 WHERE id=$1
		RETURNING id::text, display_name, title, organization, photo_url, consent_status`,
		personID, body.Consent, userID(r), isPublic).Scan(
		&s.ID, &s.DisplayName, &title, &org, &photo, &s.ConsentStatus)
	if err != nil {
		if isNoRows(err) {
			writeErr(w, r, http.StatusNotFound, "PERSON_NOT_FOUND", "speaker not found", nil)
			return
		}
		writeServer(w, r, err)
		return
	}
	s.Title = nullText(title)
	s.Organization = nullText(org)
	s.PhotoURL = nullText(photo)
	if err := h.writeAudit(ctx, tx, auditEntry{
		EntityType: "speaker", EntityID: personID, EventID: eventID,
		Action:  "speaker.consent_" + body.Consent,
		Summary: "Speaker consent " + body.Consent,
		Details: map[string]any{"consent": body.Consent, "note": strings.TrimSpace(body.Note)},
	}); err != nil {
		writeServer(w, r, err)
		return
	}
	if err := tx.Commit(ctx); err != nil {
		writeServer(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, s)
}

func (h *Handler) handleAdminRemoveSpeaker(w http.ResponseWriter, r *http.Request) {
	if !requirePermission(w, r, "events.edit_draft") {
		return
	}
	eventID := chi.URLParam(r, "id")
	personID := chi.URLParam(r, "personId")
	if !h.eventExists(r, eventID) {
		writeErr(w, r, http.StatusNotFound, "EVENT_NOT_FOUND", "event not found", nil)
		return
	}
	ctx := r.Context()
	tx, err := h.pool.Begin(ctx)
	if err != nil {
		writeServer(w, r, err)
		return
	}
	defer tx.Rollback(ctx)

	// Remove the speaker from every session that belongs to this event, and
	// keep the global person record for reuse in other events.
	tag, err := tx.Exec(ctx, `
		DELETE FROM session_speakers sp
		 USING event_sessions s
		 WHERE sp.session_id = s.id AND s.event_id=$1 AND sp.person_id=$2`, eventID, personID)
	if err != nil {
		writeServer(w, r, err)
		return
	}
	if tag.RowsAffected() == 0 {
		writeErr(w, r, http.StatusNotFound, "SPEAKER_NOT_LINKED", "this speaker is not linked to the event", nil)
		return
	}
	if err := h.writeAudit(ctx, tx, auditEntry{
		EntityType: "speaker", EntityID: personID, EventID: eventID,
		Action:  "speaker.removed",
		Summary: "Speaker removed from the event sessions",
		Details: map[string]any{"sessions": tag.RowsAffected()},
	}); err != nil {
		writeServer(w, r, err)
		return
	}
	if err := tx.Commit(ctx); err != nil {
		writeServer(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, Acknowledgement{OK: true, ID: personID, Message: "speaker removed from the event"})
}

// ---------------------------------------------------------------------------
// Audit read
// ---------------------------------------------------------------------------

func (h *Handler) handleAdminListMiceAudit(w http.ResponseWriter, r *http.Request) {
	if !requirePermission(w, r, "events.read") {
		return
	}
	limit, cursor := parsePage(r)
	args := []any{}
	q := `SELECT a.id::text, a.entity_type, a.entity_id, COALESCE(a.event_id::text,''),
	             a.action, COALESCE(a.actor_id,''), COALESCE(u.name, u.email, ''),
	             a.summary, a.details, a.created_at
	        FROM mice_audit_events a
	        LEFT JOIN users u ON u.id = a.actor_id
	       WHERE 1=1`
	if v := r.URL.Query().Get("entityType"); v != "" {
		args = append(args, v)
		q += fmt.Sprintf(" AND a.entity_type=$%d", len(args))
	}
	if v := r.URL.Query().Get("entityId"); v != "" {
		args = append(args, v)
		q += fmt.Sprintf(" AND a.entity_id=$%d", len(args))
	}
	if v := r.URL.Query().Get("eventId"); v != "" {
		args = append(args, v)
		q += fmt.Sprintf(" AND a.event_id=$%d::uuid", len(args))
	}
	if cursor != nil {
		if sortValue, id, ok := paginationDecode(*cursor); ok {
			args = append(args, sortValue, id)
			q += fmt.Sprintf(" AND (a.created_at, a.id) < ($%d::timestamptz, $%d::uuid)", len(args)-1, len(args))
		}
	}
	args = append(args, limit+1)
	q += fmt.Sprintf(" ORDER BY a.created_at DESC, a.id DESC LIMIT $%d", len(args))

	rows, err := h.pool.Query(r.Context(), q, args...)
	if err != nil {
		writeServer(w, r, err)
		return
	}
	defer rows.Close()
	items := []MiceAuditEvent{}
	for rows.Next() {
		var a MiceAuditEvent
		var raw []byte
		if err := rows.Scan(&a.ID, &a.EntityType, &a.EntityID, &a.EventID, &a.Action,
			&a.ActorID, &a.ActorName, &a.Summary, &raw, &a.CreatedAt); err != nil {
			writeServer(w, r, err)
			return
		}
		if len(raw) > 0 {
			_ = json.Unmarshal(raw, &a.Details)
		}
		items = append(items, a)
	}
	writePage(w, limit, items, func(a MiceAuditEvent) (string, bool) {
		return paginationEncode(a.CreatedAt.UTC().Format(time.RFC3339Nano), a.ID), a.ID != ""
	})
}
