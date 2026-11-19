package mice

import (
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5/pgtype"
)

func isAdmin(r *http.Request) bool { return authmwIsAdmin(r) }

type eventReportRequest struct {
	Reason        string `json:"reason"`
	Details       string `json:"details"`
	OrganizerSlug string `json:"organizerSlug"`
}

func (h *Handler) handleReportEvent(w http.ResponseWriter, r *http.Request) {
	if !requireUser(w, r) {
		return
	}
	eventID := chi.URLParam(r, "id")
	if !validUUID(eventID) {
		writeErr(w, r, http.StatusNotFound, "EVENT_NOT_FOUND", "event not found", nil)
		return
	}
	var body eventReportRequest
	if !decodeJSON(w, r, &body) {
		return
	}
	if !contains(reportReasons, body.Reason) {
		writeValidation(w, ValidationError{Field: "reason", Message: "unknown report reason"})
		return
	}
	if len(body.Details) > 2000 {
		writeValidation(w, ValidationError{Field: "details", Message: "must be at most 2000 characters"})
		return
	}
	var organizerID any
	if body.OrganizerSlug != "" && slugPattern.MatchString(body.OrganizerSlug) {
		var id string
		if err := h.pool.QueryRow(r.Context(), `SELECT id::text FROM organizer_profiles WHERE slug=$1`, body.OrganizerSlug).Scan(&id); err == nil {
			organizerID = id
		}
	}
	var id string
	var createdAt time.Time
	err := h.pool.QueryRow(r.Context(), `
		INSERT INTO event_reports (event_id, organizer_id, reporter_id, reason, details)
		VALUES ($1,$2,$3,$4,NULLIF($5,''))
		ON CONFLICT DO NOTHING
		RETURNING id::text, created_at`,
		eventID, organizerID, userID(r), body.Reason, body.Details).Scan(&id, &createdAt)
	if err != nil {
		if isNoRows(err) {
			// The same reporter already has an open report with this reason.
			writeJSON(w, http.StatusAccepted, Acknowledgement{OK: true, Message: "report already open"})
			return
		}
		writeServer(w, r, err)
		return
	}
	writeJSON(w, http.StatusAccepted, Acknowledgement{OK: true, ID: id})
}

func (h *Handler) scanReports(rows interface {
	Next() bool
	Scan(...any) error
	Err() error
}) ([]EventReport, error) {
	out := []EventReport{}
	for rows.Next() {
		var r EventReport
		var details, eventID, organizerID pgtype.Text
		var resolved pgtype.Timestamptz
		if err := rows.Scan(&r.ID, &eventID, &organizerID, &r.Reason, &details, &r.Status, &r.CreatedAt, &resolved); err != nil {
			return nil, err
		}
		r.EventID = nullText(eventID)
		r.OrganizerID = nullText(organizerID)
		r.Details = nullText(details)
		r.ResolvedAt = nullTime(resolved)
		out = append(out, r)
	}
	return out, rows.Err()
}

func (h *Handler) handleAdminListReports(w http.ResponseWriter, r *http.Request) {
	if !requirePermission(w, r, "platform.admin_access") {
		return
	}
	limit, cursor := parsePage(r)
	args := []any{}
	q := `SELECT id::text, event_id::text, organizer_id::text, reason, details, status, created_at, resolved_at
	        FROM event_reports WHERE 1=1`
	if status := r.URL.Query().Get("status"); status != "" {
		args = append(args, status)
		q += " AND status=$1"
	}
	if cursor != nil {
		if sortValue, id, ok := paginationDecode(*cursor); ok {
			args = append(args, sortValue, id)
			q += ` AND (created_at, id) < ($` + itoa(len(args)-1) + `::timestamptz, $` + itoa(len(args)) + `::uuid)`
		}
	}
	args = append(args, limit+1)
	q += " ORDER BY created_at DESC, id DESC LIMIT $" + itoa(len(args))

	rows, err := h.pool.Query(r.Context(), q, args...)
	if err != nil {
		writeServer(w, r, err)
		return
	}
	defer rows.Close()
	items, err := h.scanReports(rows)
	if err != nil {
		writeServer(w, r, err)
		return
	}
	writePage(w, limit, items, func(rep EventReport) (string, bool) {
		return paginationEncode(rep.CreatedAt.UTC().Format(time.RFC3339Nano), rep.ID), rep.ID != ""
	})
}

type reportDecision struct {
	Decision string `json:"decision"`
	Note     string `json:"note"`
}

func (h *Handler) handleAdminReportDecision(w http.ResponseWriter, r *http.Request) {
	if !requirePermission(w, r, "events.report_decide") {
		return
	}
	id := chi.URLParam(r, "id")
	var body reportDecision
	if !decodeJSON(w, r, &body) {
		return
	}
	if !contains([]string{"resolve", "dismiss", "revoke_organizer"}, body.Decision) {
		writeValidation(w, ValidationError{Field: "decision", Message: "must be resolve, dismiss or revoke_organizer"})
		return
	}
	if len(strings.TrimSpace(body.Note)) < 5 || len(body.Note) > 2000 {
		writeValidation(w, ValidationError{Field: "note", Message: "note must be between 5 and 2000 characters"})
		return
	}
	if body.Decision == "revoke_organizer" && !requirePermission(w, r, "events.revoke_organizer") {
		return
	}
	var reporter, eventID, organizerID pgtype.Text
	err := h.pool.QueryRow(r.Context(), `
		SELECT reporter_id, event_id::text, organizer_id::text FROM event_reports WHERE id=$1`, id).Scan(
		&reporter, &eventID, &organizerID)
	if err != nil {
		if isNoRows(err) {
			writeErr(w, r, http.StatusNotFound, "REPORT_NOT_FOUND", "report not found", nil)
			return
		}
		writeServer(w, r, err)
		return
	}
	// Reviewer is not the reporter (ADR 0012 §6).
	if nullText(reporter) != "" && nullText(reporter) == userID(r) {
		writeErr(w, r, http.StatusConflict, "REVIEWER_IS_REPORTER",
			"the reviewer must differ from the reporter", nil)
		return
	}

	tx, err := h.pool.Begin(r.Context())
	if err != nil {
		writeServer(w, r, err)
		return
	}
	defer tx.Rollback(r.Context())

	status := "resolved"
	if body.Decision == "dismiss" {
		status = "dismissed"
	}
	if _, err := tx.Exec(r.Context(), `
		UPDATE event_reports SET status=$2, decision_note=$3, assigned_to=$4, resolved_at=now()
		 WHERE id=$1`, id, status, body.Note, userID(r)); err != nil {
		writeServer(w, r, err)
		return
	}
	if body.Decision == "revoke_organizer" && nullText(organizerID) != "" {
		_, err := tx.Exec(r.Context(), `
			UPDATE organizer_profiles SET verification_status='revoked', revoked_at=now(),
				revoked_reason=$2, appeal_deadline=now()+interval '14 days', updated_at=now()
			 WHERE id=$1`, nullText(organizerID), body.Note)
		if err != nil {
			writeServer(w, r, err)
			return
		}
		_, err = tx.Exec(r.Context(), `
			INSERT INTO organizer_verification_events
				(organizer_id, action, from_status, to_status, actor_id, reason)
			VALUES ($1,'revoked','verified','revoked',$2,$3)`, nullText(organizerID), userID(r), body.Note)
		if err != nil {
			writeServer(w, r, err)
			return
		}
		if err := h.withdrawOrganizerEvents(r.Context(), tx, nullText(organizerID), "revoked", body.Note); err != nil {
			writeServer(w, r, err)
			return
		}
	}
	if err := tx.Commit(r.Context()); err != nil {
		writeServer(w, r, err)
		return
	}
	var rep EventReport
	var details, ev, org pgtype.Text
	var resolved pgtype.Timestamptz
	if err := h.pool.QueryRow(r.Context(), `
		SELECT id::text, event_id::text, organizer_id::text, reason, details, status, created_at, resolved_at
		  FROM event_reports WHERE id=$1`, id).Scan(
		&rep.ID, &ev, &org, &rep.Reason, &details, &rep.Status, &rep.CreatedAt, &resolved); err != nil {
		writeServer(w, r, err)
		return
	}
	rep.EventID = nullText(ev)
	rep.OrganizerID = nullText(org)
	rep.Details = nullText(details)
	rep.ResolvedAt = nullTime(resolved)
	writeJSON(w, http.StatusOK, rep)
}
