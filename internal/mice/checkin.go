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

// staffFor reports whether the actor is an active scanner or manager for the
// organizer that owns the event (ADR 0011 §9).
func (h *Handler) staffFor(ctx context.Context, actor, organizerID string) bool {
	if actor == "" || organizerID == "" {
		return false
	}
	var ok bool
	err := h.pool.QueryRow(ctx, `
		SELECT EXISTS(
			SELECT 1 FROM organizer_staff
			 WHERE organizer_id=$1 AND user_id=$2 AND revoked_at IS NULL
			   AND staff_role IN ('scanner','manager'))`, organizerID, actor).Scan(&ok)
	return err == nil && ok
}

type checkInRequest struct {
	QRPayload      string `json:"qrPayload"`
	RegistrationID string `json:"registrationId"`
	Method         string `json:"method"`
	OverrideReason string `json:"overrideReason"`
	DeviceID       string `json:"deviceId"`
	ClientTS       string `json:"clientTs"`
}

func (c checkInRequest) method() string {
	switch c.Method {
	case "qr", "manual", "staff_override":
		return c.Method
	}
	if c.QRPayload != "" {
		return "qr"
	}
	return "manual"
}

// resolveCheckInRegistration maps a QR token or registration id to its row.
func (h *Handler) resolveCheckInRegistration(ctx context.Context, eventID string, req checkInRequest) (registrationID, attendeeID, status string, err error) {
	if req.QRPayload != "" {
		tokenEvent, regID, attendee, verr := h.ticketRegistration(ctx, req.QRPayload)
		if verr != nil {
			return "", "", "", ErrInvalidTicket
		}
		if tokenEvent != eventID {
			return "", "", "", ErrInvalidTicket
		}
		var st string
		if err := h.pool.QueryRow(ctx, `SELECT status FROM event_registrations WHERE id=$1`, regID).Scan(&st); err != nil {
			return "", "", "", ErrInvalidTicket
		}
		return regID, attendee, st, nil
	}
	if !validUUID(req.RegistrationID) {
		return "", "", "", ErrInvalidTicket
	}
	err = h.pool.QueryRow(ctx, `
		SELECT id::text, user_id, status FROM event_registrations WHERE id=$1 AND event_id=$2`,
		req.RegistrationID, eventID).Scan(&registrationID, &attendeeID, &status)
	if err != nil {
		return "", "", "", ErrInvalidTicket
	}
	return registrationID, attendeeID, status, nil
}

func (h *Handler) handleCheckIn(w http.ResponseWriter, r *http.Request) {
	if !requireUser(w, r) {
		return
	}
	eventID := chi.URLParam(r, "id")
	var body checkInRequest
	if !decodeJSON(w, r, &body) {
		return
	}
	if body.QRPayload == "" && body.RegistrationID == "" {
		writeValidation(w, ValidationError{Field: "qrPayload", Message: "qrPayload or registrationId is required"})
		return
	}
	ctx := r.Context()
	actor := userID(r)
	row, err := h.loadEventRow(ctx, eventID)
	if err != nil {
		if isNoRows(err) {
			writeErr(w, r, http.StatusNotFound, "EVENT_NOT_FOUND", "event not found", nil)
			return
		}
		writeServer(w, r, err)
		return
	}
	method := body.method()
	if method == "staff_override" {
		if !requirePermission(w, r, "events.override_checkin") {
			return
		}
		if strings.TrimSpace(body.OverrideReason) == "" || len(body.OverrideReason) > 500 {
			writeValidation(w, ValidationError{Field: "overrideReason", Message: "an override reason (max 500 chars) is required"})
			return
		}
	} else if !h.staffFor(ctx, actor, nullText(row.OrganizerID)) && !isAdmin(r) {
		writeErr(w, r, http.StatusForbidden, "FORBIDDEN", "you are not assigned staff for this organizer", nil)
		return
	}
	if nullText(row.OrgStatus) == "revoked" {
		writeErr(w, r, http.StatusForbidden, "CHECKIN_DISABLED", "check-in is disabled for this organizer", nil)
		return
	}
	now := h.clock()
	if method != "staff_override" {
		if open, reason := checkInWindow(now, row.startsAt(), row.endsAt()); !open {
			writeErr(w, r, http.StatusConflict, reason, "check-in is not open", nil)
			return
		}
	}

	registrationID, attendeeID, status, err := h.resolveCheckInRegistration(ctx, eventID, body)
	if err != nil {
		writeErr(w, r, http.StatusConflict, "INVALID_TICKET", "the ticket is not valid for this event", nil)
		return
	}
	if status == "cancelled" {
		writeErr(w, r, http.StatusConflict, "NOT_CONFIRMED", "this registration is cancelled", nil)
		return
	}

	tx, err := h.pool.Begin(ctx)
	if err != nil {
		writeServer(w, r, err)
		return
	}
	defer tx.Rollback(ctx)

	// Lock the registration; first write wins.
	var lockedStatus string
	if err := tx.QueryRow(ctx, `SELECT status FROM event_registrations WHERE id=$1 FOR UPDATE`, registrationID).Scan(&lockedStatus); err != nil {
		writeServer(w, r, err)
		return
	}
	existing, err := h.checkInFor(ctx, registrationID)
	if err != nil {
		writeServer(w, r, err)
		return
	}
	if existing != nil {
		_ = tx.Rollback(ctx)
		writeErr(w, r, http.StatusConflict, "DUPLICATE_CHECKIN",
			"this ticket is already checked in at "+existing.CheckedInAt.In(nairobi).Format(time.RFC3339),
			map[string]any{"checkIn": existing})
		return
	}

	var checkedInAt any
	if method == "staff_override" && body.ClientTS != "" {
		if t, err := time.Parse(time.RFC3339, body.ClientTS); err == nil {
			checkedInAt = t
		}
	}
	if checkedInAt == nil {
		checkedInAt = now
	}
	var checkInID string
	err = tx.QueryRow(ctx, `
		INSERT INTO event_checkins (registration_id, event_id, user_id, checked_in_at, method,
			staff_user_id, device_id, client_ts, override_reason)
		VALUES ($1,$2,$3,$4,$5,$6,NULLIF($7,''),$8,NULLIF($9,''))
		RETURNING id::text`,
		registrationID, eventID, attendeeID, checkedInAt, method, actor, body.DeviceID,
		nullableTime(body.ClientTS), body.OverrideReason).Scan(&checkInID)
	if err != nil {
		if strings.Contains(err.Error(), "event_checkins_registration_id_key") {
			_ = tx.Rollback(ctx)
			writeErr(w, r, http.StatusConflict, "DUPLICATE_CHECKIN", "this ticket is already checked in", nil)
			return
		}
		writeServer(w, r, err)
		return
	}
	if _, err := tx.Exec(ctx, `
		UPDATE event_registrations SET status='checked_in', checked_in_at=$2, updated_at=now()
		 WHERE id=$1`, registrationID, checkedInAt); err != nil {
		writeServer(w, r, err)
		return
	}
	if err := tx.Commit(ctx); err != nil {
		writeServer(w, r, err)
		return
	}
	out, err := h.checkInFor(ctx, registrationID)
	if err != nil {
		writeServer(w, r, err)
		return
	}
	out.AttendeeName = h.attendeeName(ctx, attendeeID)
	writeJSON(w, http.StatusCreated, out)
}

// recordRejectedScan keeps an audit row for offline scans the server refuses,
// so the review queue can explain every queued scan.
func (h *Handler) recordRejectedScan(ctx context.Context, eventID, deviceID, actor string, scan checkInScan, clientTS time.Time, reason string) {
	_, _ = h.pool.Exec(ctx, `
		INSERT INTO event_checkin_scans (event_id, device_id, client_scan_id, token_hash,
			staff_user_id, client_ts, status, reason, resolved_at)
		VALUES ($1,$2,$3,$4,$5,$6,'rejected',$7,now())
		ON CONFLICT (device_id, client_scan_id) DO NOTHING`,
		eventID, deviceID, scan.ClientScanID, ticketHash(scan.QRPayload), actor, clientTS, reason)
}

func nullableTime(raw string) any {
	if raw == "" {
		return nil
	}
	if t, err := time.Parse(time.RFC3339, raw); err == nil {
		return t
	}
	return nil
}

func (h *Handler) attendeeName(ctx context.Context, userID string) string {
	var name pgtype.Text
	_ = h.pool.QueryRow(ctx, `SELECT name FROM users WHERE id=$1`, userID).Scan(&name)
	return nullText(name)
}

// ---------------------------------------------------------------------------
// Offline scanner batch
// ---------------------------------------------------------------------------

type checkInScan struct {
	ClientScanID string `json:"clientScanId"`
	QRPayload    string `json:"qrPayload"`
	ClientTS     string `json:"clientTs"`
}

type checkInScanBatch struct {
	DeviceID string        `json:"deviceId"`
	Scans    []checkInScan `json:"scans"`
}

func (h *Handler) handleSyncOfflineScans(w http.ResponseWriter, r *http.Request) {
	if !requireUser(w, r) {
		return
	}
	eventID := chi.URLParam(r, "id")
	var body checkInScanBatch
	if !decodeJSON(w, r, &body) {
		return
	}
	if strings.TrimSpace(body.DeviceID) == "" {
		writeValidation(w, ValidationError{Field: "deviceId", Message: "deviceId is required"})
		return
	}
	if len(body.Scans) > 200 {
		writeValidation(w, ValidationError{Field: "scans", Message: "at most 200 scans per batch"})
		return
	}
	ctx := r.Context()
	actor := userID(r)
	row, err := h.loadEventRow(ctx, eventID)
	if err != nil {
		if isNoRows(err) {
			writeErr(w, r, http.StatusNotFound, "EVENT_NOT_FOUND", "event not found", nil)
			return
		}
		writeServer(w, r, err)
		return
	}
	if !h.staffFor(ctx, actor, nullText(row.OrganizerID)) && !isAdmin(r) {
		writeErr(w, r, http.StatusForbidden, "FORBIDDEN", "you are not assigned staff for this organizer", nil)
		return
	}

	result := CheckInScanBatchResult{Results: []CheckInScanResult{}}
	for _, scan := range body.Scans {
		res := h.resolveOfflineScan(ctx, eventID, actor, body.DeviceID, scan, row)
		switch res.Status {
		case "applied":
			result.Applied++
		case "duplicate":
			result.Duplicates++
		default:
			result.Rejected++
		}
		result.Results = append(result.Results, res)
	}
	writeJSON(w, http.StatusOK, result)
}

// resolveOfflineScan applies first-write-wins. The first scan to reach the
// server creates the check-in; every later scan for that registration is a
// duplicate and is kept for review.
func (h *Handler) resolveOfflineScan(ctx context.Context, eventID, actor, deviceID string, scan checkInScan, row eventRow) CheckInScanResult {
	res := CheckInScanResult{ClientScanID: scan.ClientScanID, Status: "rejected"}
	if scan.ClientScanID == "" || scan.QRPayload == "" {
		res.Reason = "MISSING_FIELDS"
		return res
	}
	clientTS, err := time.Parse(time.RFC3339, scan.ClientTS)
	if err != nil {
		res.Reason = "INVALID_CLIENT_TS"
		h.recordRejectedScan(ctx, eventID, deviceID, actor, scan, h.clock(), res.Reason)
		return res
	}
	registrationID, attendeeID, status, err := h.resolveCheckInRegistration(ctx, eventID, checkInRequest{QRPayload: scan.QRPayload})
	if err != nil {
		res.Reason = "INVALID_TICKET"
		h.recordRejectedScan(ctx, eventID, deviceID, actor, scan, clientTS, res.Reason)
		return res
	}
	if status == "cancelled" {
		res.Reason = "NOT_CONFIRMED"
		h.recordRejectedScan(ctx, eventID, deviceID, actor, scan, clientTS, res.Reason)
		return res
	}

	tx, err := h.pool.Begin(ctx)
	if err != nil {
		res.Reason = "INTERNAL_ERROR"
		return res
	}
	defer tx.Rollback(ctx)

	var scanID string
	err = tx.QueryRow(ctx, `
		INSERT INTO event_checkin_scans (event_id, device_id, client_scan_id, token_hash,
			registration_id, staff_user_id, client_ts, status)
		VALUES ($1,$2,$3,$4,$5,$6,$7,'pending')
		ON CONFLICT (device_id, client_scan_id) DO NOTHING
		RETURNING id::text`, eventID, deviceID, scan.ClientScanID, ticketHash(scan.QRPayload),
		registrationID, actor, clientTS).Scan(&scanID)
	if err != nil {
		if isNoRows(err) {
			// Replayed scan: report the stored resolution.
			var storedStatus, reason pgtype.Text
			_ = tx.QueryRow(ctx, `
				SELECT status, reason FROM event_checkin_scans
				 WHERE device_id=$1 AND client_scan_id=$2`, deviceID, scan.ClientScanID).Scan(&storedStatus, &reason)
			if ci, _ := h.checkInFor(ctx, registrationID); ci != nil {
				res.Status = "duplicate"
				res.CheckIn = ci
			} else {
				res.Status = "rejected"
				res.Reason = nullText(reason)
			}
			return res
		}
		res.Reason = "INTERNAL_ERROR"
		return res
	}

	if existing, _ := h.checkInFor(ctx, registrationID); existing != nil {
		_, _ = tx.Exec(ctx, `UPDATE event_checkin_scans SET status='duplicate', reason='already_checked_in', resolved_at=now() WHERE id=$1`, scanID)
		if err := tx.Commit(ctx); err != nil {
			res.Reason = "INTERNAL_ERROR"
			return res
		}
		res.Status = "duplicate"
		res.Reason = "already_checked_in"
		res.CheckIn = existing
		return res
	}
	if open, reason := checkInWindow(clientTS, row.startsAt(), row.endsAt()); !open {
		_, _ = tx.Exec(ctx, `UPDATE event_checkin_scans SET status='rejected', reason=$2, resolved_at=now() WHERE id=$1`, scanID, reason)
		if err := tx.Commit(ctx); err != nil {
			res.Reason = "INTERNAL_ERROR"
			return res
		}
		res.Reason = reason
		return res
	}

	var checkInID string
	err = tx.QueryRow(ctx, `
		INSERT INTO event_checkins (registration_id, event_id, user_id, checked_in_at, method,
			staff_user_id, device_id, client_ts)
		VALUES ($1,$2,$3,$4,'offline_queue',$5,$6,$4)
		RETURNING id::text`, registrationID, eventID, attendeeID, clientTS, actor, deviceID).Scan(&checkInID)
	if err != nil {
		if strings.Contains(err.Error(), "event_checkins_registration_id_key") {
			_, _ = tx.Exec(ctx, `UPDATE event_checkin_scans SET status='duplicate', reason='already_checked_in', resolved_at=now() WHERE id=$1`, scanID)
			_ = tx.Commit(ctx)
			res.Status = "duplicate"
			res.Reason = "already_checked_in"
			res.CheckIn, _ = h.checkInFor(ctx, registrationID)
			return res
		}
		res.Reason = "INTERNAL_ERROR"
		return res
	}
	if _, err := tx.Exec(ctx, `
		UPDATE event_registrations SET status='checked_in', checked_in_at=$2, updated_at=now()
		 WHERE id=$1`, registrationID, clientTS); err != nil {
		res.Reason = "INTERNAL_ERROR"
		return res
	}
	if _, err := tx.Exec(ctx, `
		UPDATE event_checkin_scans SET status='applied', resolved_at=now() WHERE id=$1`, scanID); err != nil {
		res.Reason = "INTERNAL_ERROR"
		return res
	}
	if err := tx.Commit(ctx); err != nil {
		res.Reason = "INTERNAL_ERROR"
		return res
	}
	ci, _ := h.checkInFor(ctx, registrationID)
	res.Status = "applied"
	res.CheckIn = ci
	return res
}

// ---------------------------------------------------------------------------
// Admin views
// ---------------------------------------------------------------------------

func (h *Handler) handleAdminListRegistrations(w http.ResponseWriter, r *http.Request) {
	if !requirePermission(w, r, "events.view_registrations") {
		return
	}
	eventID := chi.URLParam(r, "id")
	limit, cursor := parsePage(r)
	args := []any{eventID}
	q := `SELECT ` + registrationColumns + `, u.name, u.email
	        FROM event_registrations r
	        LEFT JOIN users u ON u.id = r.user_id
	       WHERE r.event_id=$1`
	if status := r.URL.Query().Get("status"); status != "" {
		args = append(args, status)
		q += fmt.Sprintf(" AND r.status=$%d", len(args))
	}
	if cursor != nil {
		if sortValue, id, ok := paginationDecode(*cursor); ok {
			args = append(args, sortValue, id)
			q += fmt.Sprintf(" AND (r.created_at, r.id) < ($%d::timestamptz, $%d::uuid)", len(args)-1, len(args))
		}
	}
	args = append(args, limit+1)
	q += fmt.Sprintf(" ORDER BY r.created_at DESC, r.id DESC LIMIT $%d", len(args))

	rows, err := h.pool.Query(r.Context(), q, args...)
	if err != nil {
		writeServer(w, r, err)
		return
	}
	defer rows.Close()
	items := []AdminRegistration{}
	for rows.Next() {
		var a AdminRegistration
		var answers []byte
		var reminder, position pgtype.Int4
		var confirmBy, cancelledAt pgtype.Timestamptz
		var name, email pgtype.Text
		if err := rows.Scan(&a.ID, &a.EventID, &a.Status, &answers, &reminder, &position,
			&confirmBy, &a.CreatedAt, &cancelledAt, &name, &email); err != nil {
			writeServer(w, r, err)
			return
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
		items = append(items, a)
	}
	writePage(w, limit, items, func(a AdminRegistration) (string, bool) {
		return paginationEncode(a.CreatedAt.UTC().Format(time.RFC3339Nano), a.ID), a.ID != ""
	})
}

func (h *Handler) handleAdminListCheckIns(w http.ResponseWriter, r *http.Request) {
	if !requirePermission(w, r, "events.view_registrations") {
		return
	}
	eventID := chi.URLParam(r, "id")
	limit, cursor := parsePage(r)
	args := []any{eventID}
	q := `SELECT c.id::text, c.registration_id::text, c.event_id::text, c.checked_in_at, c.method,
	             c.staff_user_id, attendee.name
	        FROM event_checkins c
	        JOIN event_registrations reg ON reg.id = c.registration_id
	        LEFT JOIN users attendee ON attendee.id = reg.user_id
	       WHERE c.event_id=$1`
	if cursor != nil {
		if sortValue, id, ok := paginationDecode(*cursor); ok {
			args = append(args, sortValue, id)
			q += fmt.Sprintf(" AND (c.checked_in_at, c.id) < ($%d::timestamptz, $%d::uuid)", len(args)-1, len(args))
		}
	}
	args = append(args, limit+1)
	q += fmt.Sprintf(" ORDER BY c.checked_in_at DESC, c.id DESC LIMIT $%d", len(args))
	rows, err := h.pool.Query(r.Context(), q, args...)
	if err != nil {
		writeServer(w, r, err)
		return
	}
	defer rows.Close()
	items := []CheckIn{}
	for rows.Next() {
		var c CheckIn
		var staff, name pgtype.Text
		if err := rows.Scan(&c.ID, &c.RegistrationID, &c.EventID, &c.CheckedInAt, &c.Method, &staff, &name); err != nil {
			writeServer(w, r, err)
			return
		}
		c.StaffRef = nullText(staff)
		c.AttendeeName = nullText(name)
		items = append(items, c)
	}
	writePage(w, limit, items, func(c CheckIn) (string, bool) {
		return paginationEncode(c.CheckedInAt.UTC().Format(time.RFC3339Nano), c.ID), c.ID != ""
	})
}

var _ = pgx.ErrNoRows
