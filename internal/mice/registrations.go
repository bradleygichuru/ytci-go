package mice

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
)

type registrationRequest struct {
	Answers               map[string]string `json:"answers"`
	ReminderOffsetMinutes *int              `json:"reminderOffsetMinutes"`
	AcceptWaitlist        *bool             `json:"acceptWaitlist"`
}

// regEvent is the locked event state needed to place a registration.
type regEvent struct {
	Status      string
	Access      string
	Mode        string
	Capacity    pgtype.Int4
	WaitlistCap pgtype.Int4
	EventDate   pgtype.Date
	EndDate     pgtype.Date
	StartTime   pgtype.Text
	EndTime     pgtype.Text
	Timezone    string
	OrganizerID pgtype.Text
	Version     int
	OrgStatus   pgtype.Text
	OrgTier     pgtype.Text
}

func (e regEvent) start() time.Time {
	return eventStart(e.EventDate.Time, nullText(e.StartTime), e.Timezone)
}

func (e regEvent) end() time.Time {
	var end *time.Time
	if e.EndDate.Valid {
		t := e.EndDate.Time
		end = &t
	}
	return eventEnd(e.EventDate.Time, end, nullText(e.EndTime), e.Timezone)
}

func lockEvent(ctx context.Context, tx pgx.Tx, id string) (regEvent, error) {
	var e regEvent
	err := tx.QueryRow(ctx, `
		SELECT e.status, COALESCE(e.access,'public'), COALESCE(e.registration_mode,'none'),
		       e.capacity, e.waitlist_capacity, e.event_date, e.end_date, e.start_time, e.end_time,
		       COALESCE(e.timezone,'Africa/Nairobi'), e.organizer_profile_id::text,
		       COALESCE(e.version,1), o.verification_status, o.trust_tier
		  FROM events e
		  LEFT JOIN organizer_profiles o ON o.id = e.organizer_profile_id
		 WHERE e.id = $1
		 FOR UPDATE OF e`, id).Scan(
		&e.Status, &e.Access, &e.Mode, &e.Capacity, &e.WaitlistCap, &e.EventDate, &e.EndDate,
		&e.StartTime, &e.EndTime, &e.Timezone, &e.OrganizerID, &e.Version, &e.OrgStatus, &e.OrgTier)
	return e, err
}

func scanRegistration(row pgx.Row) (*Registration, error) {
	var r Registration
	var answers []byte
	var reminder, position pgtype.Int4
	var confirmBy, cancelledAt pgtype.Timestamptz
	if err := row.Scan(&r.ID, &r.EventID, &r.Status, &answers, &reminder, &position,
		&confirmBy, &r.CreatedAt, &cancelledAt); err != nil {
		return nil, err
	}
	if len(answers) > 0 {
		_ = json.Unmarshal(answers, &r.Answers)
	}
	r.ReminderOffsetMinutes = nullInt(reminder)
	r.WaitlistPosition = nullInt(position)
	r.ConfirmBy = nullTime(confirmBy)
	r.CancelledAt = nullTime(cancelledAt)
	return &r, nil
}

const registrationColumns = `r.id::text, r.event_id::text, r.status, r.answers,
	r.reminder_offset_minutes, r.waitlist_position, r.confirm_by, r.created_at, r.cancelled_at`

func (h *Handler) registrationFor(ctx context.Context, uid, eventID string) (*Registration, error) {
	r, err := scanRegistration(h.pool.QueryRow(ctx,
		`SELECT `+registrationColumns+` FROM event_registrations r
		  WHERE r.user_id=$1 AND r.event_id=$2`, uid, eventID))
	if err != nil {
		if isNoRows(err) {
			return nil, nil
		}
		return nil, err
	}
	if err := h.attachRegistrationExtras(ctx, r, uid); err != nil {
		return nil, err
	}
	return r, nil
}

// attachRegistrationExtras adds the ticket and check-in state.
func (h *Handler) attachRegistrationExtras(ctx context.Context, r *Registration, uid string) error {
	if r.Status != "confirmed" && r.Status != "checked_in" {
		return nil
	}
	row, err := h.loadEventRow(ctx, r.EventID)
	if err != nil {
		return err
	}
	ticket, err := h.ticketFor(ctx, r.EventID, r.ID, uid, row.endsAt())
	if err != nil {
		return err
	}
	r.Ticket = ticket
	ci, err := h.checkInFor(ctx, r.ID)
	if err != nil {
		return err
	}
	r.CheckIn = ci
	return nil
}

func (h *Handler) checkInFor(ctx context.Context, registrationID string) (*CheckIn, error) {
	var c CheckIn
	var staff, name pgtype.Text
	err := h.pool.QueryRow(ctx, `
		SELECT c.id::text, c.registration_id::text, c.event_id::text, c.checked_in_at, c.method,
		       c.staff_user_id, attendee.name
		  FROM event_checkins c
		  JOIN event_registrations r ON r.id = c.registration_id
		  LEFT JOIN users attendee ON attendee.id = r.user_id
		 WHERE c.registration_id = $1`, registrationID).Scan(
		&c.ID, &c.RegistrationID, &c.EventID, &c.CheckedInAt, &c.Method, &staff, &name)
	if err != nil {
		if isNoRows(err) {
			return nil, nil
		}
		return nil, err
	}
	c.StaffRef = nullText(staff)
	c.AttendeeName = nullText(name)
	return &c, nil
}

// ---------------------------------------------------------------------------
// Create registration
// ---------------------------------------------------------------------------

func (h *Handler) handleCreateRegistration(w http.ResponseWriter, r *http.Request) {
	if !requireUser(w, r) {
		return
	}
	eventID := chi.URLParam(r, "id")
	if !validUUID(eventID) {
		writeErr(w, r, http.StatusNotFound, "EVENT_NOT_FOUND", "event not found", nil)
		return
	}
	var body registrationRequest
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20)).Decode(&body); err != nil && !errors.Is(err, io.EOF) {
		writeErr(w, r, http.StatusBadRequest, "INVALID_JSON", "request body is not valid JSON", nil)
		return
	}
	if body.ReminderOffsetMinutes != nil && !containsInt(reminderOffsets, *body.ReminderOffsetMinutes) {
		writeValidation(w, ValidationError{Field: "reminderOffsetMinutes", Message: "must be 60, 1440 or 10080"})
		return
	}
	uid := userID(r)
	ctx := r.Context()

	// Apply lapsed promotion windows before counting capacity, so a seat
	// released by a non-responding reader is reusable without waiting for
	// that reader to open the app.
	h.expirePromotions(ctx, eventID)

	tx, err := h.pool.Begin(ctx)
	if err != nil {
		writeServer(w, r, err)
		return
	}
	defer tx.Rollback(ctx)

	event, err := lockEvent(ctx, tx, eventID)
	if err != nil {
		if isNoRows(err) {
			writeErr(w, r, http.StatusNotFound, "EVENT_NOT_FOUND", "event not found", nil)
			return
		}
		writeServer(w, r, err)
		return
	}
	if !canonicalStatusRegistrable(event.Status) {
		writeErr(w, r, http.StatusConflict, "REGISTRATION_CLOSED", "registration is closed for this event", nil)
		return
	}
	if event.Access == "invite_only" {
		writeErr(w, r, http.StatusConflict, "REGISTRATION_CLOSED", "this is an invite-only event", nil)
		return
	}
	switch event.Mode {
	case "none":
		writeErr(w, r, http.StatusConflict, "REGISTRATION_CLOSED", "this event does not take registrations", nil)
		return
	case "ticket_handoff":
		writeErr(w, r, http.StatusConflict, "USE_TICKET_HANDOFF", "this event sells tickets through the organizer", nil)
		return
	}
	now := h.clock()
	start := event.start()
	if !now.Before(start) {
		writeErr(w, r, http.StatusConflict, "EVENT_STARTED", "registration closed when the event started", nil)
		return
	}
	answers, failures := h.validateAnswers(ctx, eventID, body.Answers)
	if len(failures) > 0 {
		writeValidation(w, failures...)
		return
	}

	// Idempotent replay: an active registration is returned as-is.
	existing, err := scanRegistration(tx.QueryRow(ctx,
		`SELECT `+registrationColumns+` FROM event_registrations r
		  WHERE r.user_id=$1 AND r.event_id=$2 FOR UPDATE`, uid, eventID))
	if err != nil && !isNoRows(err) {
		writeServer(w, r, err)
		return
	}
	if err == nil {
		switch existing.Status {
		case "checked_in":
			writeErr(w, r, http.StatusConflict, "ALREADY_CHECKED_IN",
				"this registration is already checked in", nil)
			return
		case "confirmed", "waitlisted":
			if err := tx.Commit(ctx); err != nil {
				writeServer(w, r, err)
				return
			}
			full, err := h.registrationFor(ctx, uid, eventID)
			if err != nil {
				writeServer(w, r, err)
				return
			}
			writeJSON(w, http.StatusCreated, full)
			return
		}
	}

	going := 0
	if err := tx.QueryRow(ctx, `
		SELECT count(*) FROM event_registrations
		 WHERE event_id=$1 AND status IN ('confirmed','checked_in') AND user_id<>$2`,
		eventID, uid).Scan(&going); err != nil {
		writeServer(w, r, err)
		return
	}

	status := "confirmed"
	var position *int
	if event.Capacity.Valid && going >= int(event.Capacity.Int32) {
		acceptWaitlist := body.AcceptWaitlist == nil || *body.AcceptWaitlist
		waitlisted := 0
		if err := tx.QueryRow(ctx, `
			SELECT count(*) FROM event_registrations
			 WHERE event_id=$1 AND status='waitlisted'`, eventID).Scan(&waitlisted); err != nil {
			writeServer(w, r, err)
			return
		}
		if !acceptWaitlist || (event.WaitlistCap.Valid && waitlisted >= int(event.WaitlistCap.Int32)) {
			writeErr(w, r, http.StatusConflict, "EVENT_CAPACITY_FULL",
				"this event is full", map[string]any{"capacity": event.Capacity.Int32, "confirmed": going})
			return
		}
		status = "waitlisted"
		var next int
		if err := tx.QueryRow(ctx, `
			SELECT COALESCE(MAX(waitlist_position),0)+1 FROM event_registrations
			 WHERE event_id=$1 AND status='waitlisted'`, eventID).Scan(&next); err != nil {
			writeServer(w, r, err)
			return
		}
		position = &next
	}

	var confirmBy any
	var regID string
	err = tx.QueryRow(ctx, `
		INSERT INTO event_registrations
			(event_id, user_id, status, answers, reminder_offset_minutes, waitlist_position, confirm_by)
		VALUES ($1,$2,$3,$4,$5,$6,$7)
		ON CONFLICT (event_id, user_id) DO UPDATE SET
			status=EXCLUDED.status, answers=EXCLUDED.answers,
			reminder_offset_minutes=EXCLUDED.reminder_offset_minutes,
			waitlist_position=EXCLUDED.waitlist_position, confirm_by=EXCLUDED.confirm_by,
			cancelled_at=NULL, cancel_reason=NULL, updated_at=now()
		RETURNING id::text`,
		eventID, uid, status, answerJSON(answers), body.ReminderOffsetMinutes, position, confirmBy).Scan(&regID)
	if err != nil {
		writeServer(w, r, err)
		return
	}
	if err := h.scheduleReminder(ctx, tx, eventID, uid, body.ReminderOffsetMinutes, start); err != nil {
		writeServer(w, r, err)
		return
	}
	if err := tx.Commit(ctx); err != nil {
		writeServer(w, r, err)
		return
	}

	reg, err := h.registrationFor(ctx, uid, eventID)
	if err != nil {
		writeServer(w, r, err)
		return
	}
	if row, err := h.loadEventRow(ctx, eventID); err == nil {
		summary := row.summary()
		reg.Event = &summary
	}
	writeJSON(w, http.StatusCreated, reg)
}

func containsInt(list []int, v int) bool {
	for _, item := range list {
		if item == v {
			return true
		}
	}
	return false
}

func canonicalStatusRegistrable(status string) bool {
	switch normaliseStatus(status) {
	case "published", "postponed":
		return true
	}
	return false
}

func answerJSON(answers map[string]string) []byte {
	if answers == nil {
		return []byte(`{}`)
	}
	b, err := json.Marshal(answers)
	if err != nil {
		return []byte(`{}`)
	}
	return b
}

// validateAnswers checks answers against the event's questions: unknown keys
// are rejected, required questions must be present and choice values must be
// in the declared list.
func (h *Handler) validateAnswers(ctx context.Context, eventID string, answers map[string]string) (map[string]string, []ValidationError) {
	questions, err := h.loadRegistrationQuestions(ctx, eventID)
	if err != nil {
		return nil, []ValidationError{{Field: "answers", Message: "could not load questions"}}
	}
	byID := map[string]RegistrationQuestion{}
	for _, q := range questions {
		byID[q.ID] = q
	}
	out := map[string]string{}
	for key, value := range answers {
		q, ok := byID[key]
		if !ok {
			return nil, []ValidationError{{Field: "answers", Message: "unknown question " + key}}
		}
		if q.AnswerKind == "single_choice" && len(q.Choices) > 0 && !contains(q.Choices, value) {
			return nil, []ValidationError{{Field: "answers." + key, Message: "must be one of the offered choices"}}
		}
		if q.AnswerKind == "boolean" && value != "true" && value != "false" {
			return nil, []ValidationError{{Field: "answers." + key, Message: "must be true or false"}}
		}
		out[key] = value
	}
	var failures []ValidationError
	for _, q := range questions {
		if q.Required && out[q.ID] == "" {
			failures = append(failures, ValidationError{Field: "answers." + q.ID, Message: "this question is required"})
		}
	}
	return out, failures
}

func minTime(a, b time.Time) time.Time {
	if a.Before(b) {
		return a
	}
	return b
}

// ---------------------------------------------------------------------------
// Read / cancel / list
// ---------------------------------------------------------------------------

func (h *Handler) handleGetMyRegistration(w http.ResponseWriter, r *http.Request) {
	if !requireUser(w, r) {
		return
	}
	eventID := chi.URLParam(r, "id")
	if !validUUID(eventID) {
		writeErr(w, r, http.StatusNotFound, "REGISTRATION_NOT_FOUND", "registration not found", nil)
		return
	}
	h.expirePromotions(r.Context(), eventID)
	uid := userID(r)
	reg, err := h.registrationFor(r.Context(), uid, eventID)
	if err != nil {
		writeServer(w, r, err)
		return
	}
	if reg == nil {
		writeErr(w, r, http.StatusNotFound, "REGISTRATION_NOT_FOUND", "registration not found", nil)
		return
	}
	if row, err := h.loadEventRow(r.Context(), eventID); err == nil {
		summary := row.summary()
		reg.Event = &summary
	}
	writeJSON(w, http.StatusOK, reg)
}

func (h *Handler) handleCancelRegistration(w http.ResponseWriter, r *http.Request) {
	if !requireUser(w, r) {
		return
	}
	eventID := chi.URLParam(r, "id")
	ctx := r.Context()
	uid := userID(r)

	tx, err := h.pool.Begin(ctx)
	if err != nil {
		writeServer(w, r, err)
		return
	}
	defer tx.Rollback(ctx)

	event, err := lockEvent(ctx, tx, eventID)
	if err != nil {
		if isNoRows(err) {
			writeErr(w, r, http.StatusNotFound, "REGISTRATION_NOT_FOUND", "registration not found", nil)
			return
		}
		writeServer(w, r, err)
		return
	}
	var status string
	var regID string
	err = tx.QueryRow(ctx, `
		SELECT id::text, status FROM event_registrations
		 WHERE event_id=$1 AND user_id=$2 FOR UPDATE`, eventID, uid).Scan(&regID, &status)
	if err != nil {
		if isNoRows(err) {
			writeErr(w, r, http.StatusNotFound, "REGISTRATION_NOT_FOUND", "registration not found", nil)
			return
		}
		writeServer(w, r, err)
		return
	}
	now := h.clock()
	if status == "checked_in" {
		writeErr(w, r, http.StatusConflict, "ALREADY_CHECKED_IN", "a checked-in registration cannot be cancelled", nil)
		return
	}
	if !now.Before(event.start()) {
		writeErr(w, r, http.StatusConflict, "EVENT_STARTED", "cancellation closes when the event starts", nil)
		return
	}
	if status != "confirmed" && status != "waitlisted" {
		writeErr(w, r, http.StatusConflict, "NOT_CANCELLABLE", "this registration cannot be cancelled", nil)
		return
	}
	if _, err := tx.Exec(ctx, `
		UPDATE event_registrations SET status='cancelled', cancelled_at=now(),
			cancel_reason='reader_cancelled', waitlist_position=NULL, confirm_by=NULL, updated_at=now()
		 WHERE id=$1`, regID); err != nil {
		writeServer(w, r, err)
		return
	}
	if status == "confirmed" {
		if err := h.promoteWaitlist(ctx, tx, eventID, event, now); err != nil {
			writeServer(w, r, err)
			return
		}
	}
	if err := tx.Commit(ctx); err != nil {
		writeServer(w, r, err)
		return
	}
	reg, err := h.registrationFor(ctx, uid, eventID)
	if err != nil {
		writeServer(w, r, err)
		return
	}
	if row, err := h.loadEventRow(ctx, eventID); err == nil {
		summary := row.summary()
		reg.Event = &summary
	}
	writeJSON(w, http.StatusOK, reg)
}

// promoteWaitlist moves the FIFO head to confirmed with a 24h window capped at
// event start and notifies them (ADR 0011 §3).
func (h *Handler) promoteWaitlist(ctx context.Context, tx dbTX, eventID string, event regEvent, now time.Time) error {
	var regID, uid string
	err := tx.QueryRow(ctx, `
		SELECT id::text, user_id FROM event_registrations
		 WHERE event_id=$1 AND status='waitlisted'
		 ORDER BY waitlist_position ASC NULLS LAST, created_at ASC
		 LIMIT 1 FOR UPDATE SKIP LOCKED`, eventID).Scan(&regID, &uid)
	if err != nil {
		if isNoRows(err) {
			return nil
		}
		return err
	}
	confirmBy := minTime(now.Add(24*time.Hour), event.start())
	_, err = tx.Exec(ctx, `
		UPDATE event_registrations SET status='confirmed', waitlist_position=NULL,
			confirm_by=$2, promotion_notified_at=now(), updated_at=now()
		 WHERE id=$1`, regID, confirmBy)
	if err != nil {
		return err
	}
	return h.enqueueNotification(ctx, tx, enqueueParams{
		EventID:      eventID,
		UserID:       uid,
		Kind:         "waitlist_promotion",
		Title:        "A seat opened up",
		Body:         "Confirm your place by " + confirmBy.In(nairobi).Format("2 Jan 15:04") + " (EAT).",
		DeepLink:     "ytci://events/" + eventID,
		EventVersion: event.Version,
		DeliverAfter: now,
		EventStart:   event.start(),
	})
}

// expirePromotions cancels seats whose 24h confirmation window lapsed and
// moves the next person up. Called lazily by the read paths.
func (h *Handler) expirePromotions(ctx context.Context, eventID string) {
	if !validUUID(eventID) {
		return
	}
	var start time.Time
	var status string
	var tz string
	var date pgtype.Date
	var endDate pgtype.Date
	var startTime, endTime pgtype.Text
	err := h.pool.QueryRow(ctx, `
		SELECT status, event_date, end_date, start_time, end_time, COALESCE(timezone,'Africa/Nairobi')
		  FROM events WHERE id=$1`, eventID).Scan(&status, &date, &endDate, &startTime, &endTime, &tz)
	if err != nil {
		return
	}
	start = eventStart(date.Time, nullText(startTime), tz)
	now := h.clock()
	rows, err := h.pool.Query(ctx, `
		UPDATE event_registrations SET status='cancelled', cancelled_at=now(),
			cancel_reason='promotion_expired', waitlist_position=NULL, updated_at=now()
		 WHERE event_id=$1 AND status='confirmed' AND confirm_by IS NOT NULL AND confirm_by < $2
		RETURNING id::text`, eventID, now)
	if err != nil {
		return
	}
	count := 0
	for rows.Next() {
		count++
	}
	rows.Close()
	if count == 0 {
		return
	}
	if !now.Before(start) {
		return
	}
	// Reuse the promotion path without holding the event lock; the next
	// promoter wins and SKIP LOCKED keeps the queue consistent.
	event := regEvent{EventDate: date, EndDate: endDate, StartTime: startTime, EndTime: endTime, Timezone: tz}
	for i := 0; i < count; i++ {
		_ = h.promoteWaitlist(ctx, h.pool, eventID, event, now)
	}
}

func (h *Handler) handleListMyRegistrations(w http.ResponseWriter, r *http.Request) {
	if !requireUser(w, r) {
		return
	}
	uid := userID(r)
	limit, cursor := parsePage(r)
	state := r.URL.Query().Get("state")
	if state == "" {
		state = "upcoming"
	}
	if !contains([]string{"upcoming", "past", "all"}, state) {
		writeValidation(w, ValidationError{Field: "state", Message: "must be upcoming, past or all"})
		return
	}
	args := []any{uid}
	q := `SELECT ` + registrationColumns + `, e.event_date
	        FROM event_registrations r JOIN events e ON e.id = r.event_id
	       WHERE r.user_id=$1`
	switch state {
	case "upcoming":
		q += " AND r.status <> 'cancelled' AND COALESCE(e.end_date, e.event_date) >= current_date"
	case "past":
		q += " AND r.status <> 'cancelled' AND COALESCE(e.end_date, e.event_date) < current_date"
	}
	if cursor != nil {
		if sortValue, id, ok := paginationDecode(*cursor); ok {
			args = append(args, sortValue, id)
			if state == "past" {
				q += fmt.Sprintf(" AND (e.event_date, r.id) < ($%d::date, $%d::uuid)", len(args)-1, len(args))
			} else {
				q += fmt.Sprintf(" AND (e.event_date, r.id) > ($%d::date, $%d::uuid)", len(args)-1, len(args))
			}
		}
	}
	order := "ASC"
	if state == "past" {
		order = "DESC"
	}
	args = append(args, limit+1)
	q += fmt.Sprintf(" ORDER BY e.event_date %s, r.id %s LIMIT $%d", order, order, len(args))

	rows, err := h.pool.Query(r.Context(), q, args...)
	if err != nil {
		writeServer(w, r, err)
		return
	}
	type item struct {
		reg  Registration
		date string
	}
	items := []item{}
	for rows.Next() {
		var reg Registration
		var answers []byte
		var reminder, position pgtype.Int4
		var confirmBy, cancelledAt pgtype.Timestamptz
		var date time.Time
		if err := rows.Scan(&reg.ID, &reg.EventID, &reg.Status, &answers, &reminder, &position,
			&confirmBy, &reg.CreatedAt, &cancelledAt, &date); err != nil {
			rows.Close()
			writeServer(w, r, err)
			return
		}
		if len(answers) > 0 {
			_ = json.Unmarshal(answers, &reg.Answers)
		}
		reg.ReminderOffsetMinutes = nullInt(reminder)
		reg.WaitlistPosition = nullInt(position)
		reg.ConfirmBy = nullTime(confirmBy)
		reg.CancelledAt = nullTime(cancelledAt)
		items = append(items, item{reg: reg, date: date.Format("2006-01-02")})
	}
	rows.Close()

	out := make([]Registration, 0, len(items))
	for _, it := range items {
		reg := it.reg
		if err := h.attachRegistrationExtras(r.Context(), &reg, uid); err != nil {
			writeServer(w, r, err)
			return
		}
		if row, err := h.loadEventRow(r.Context(), reg.EventID); err == nil {
			summary := row.summary()
			reg.Event = &summary
		}
		out = append(out, reg)
	}
	// Trim to the page and return the cursor for the last kept row.
	hasMore := len(out) > limit
	if hasMore {
		out = out[:limit]
		items = items[:limit]
	}
	page := Page[Registration]{Items: out, HasMore: hasMore}
	if hasMore && len(items) > 0 {
		last := items[len(items)-1]
		c := paginationEncode(last.date, last.reg.ID)
		page.NextCursor = &c
	}
	writeJSON(w, http.StatusOK, page)
}

// ---------------------------------------------------------------------------
// Ticket handoff
// ---------------------------------------------------------------------------

func (h *Handler) handleTicketHandoff(w http.ResponseWriter, r *http.Request) {
	if !requireUser(w, r) {
		return
	}
	eventID := chi.URLParam(r, "id")
	ctx := r.Context()
	row, err := h.loadEventRow(ctx, eventID)
	if err != nil {
		if isNoRows(err) {
			writeErr(w, r, http.StatusNotFound, "EVENT_NOT_FOUND", "event not found", nil)
			return
		}
		writeServer(w, r, err)
		return
	}
	if row.RegistrationMode != "ticket_handoff" || !row.TicketURL.Valid {
		writeErr(w, r, http.StatusConflict, "NOT_TICKET_HANDOFF",
			"this event does not sell tickets through the organizer", nil)
		return
	}
	uid := userID(r)
	var id string
	var createdAt time.Time
	err = h.pool.QueryRow(ctx, `
		INSERT INTO event_ticket_handoffs (event_id, user_id)
		VALUES ($1,$2)
		ON CONFLICT (event_id, user_id) DO UPDATE SET event_id=EXCLUDED.event_id
		RETURNING id::text, created_at`, eventID, uid).Scan(&id, &createdAt)
	if err != nil {
		writeServer(w, r, err)
		return
	}
	out := TicketHandoff{
		ID: id, EventID: eventID, CreatedAt: createdAt,
		TicketURL: row.TicketURL.String, SoldBy: nullText(row.OrgDisplayName),
		Disclosure: "YTCI does not process payment.",
	}
	if row.PriceAmount.Valid {
		amount := nullNumeric(row.PriceAmount)
		if amount != nil {
			currency := nullText(row.PriceCurrency)
			out.Price = &Price{Amount: *amount, Currency: currency, Display: formatPrice(*amount, currency)}
		}
	}
	if out.SoldBy == "" {
		out.SoldBy = row.County
	}
	writeJSON(w, http.StatusCreated, out)
}

// ---------------------------------------------------------------------------
// Ticket read
// ---------------------------------------------------------------------------

func (h *Handler) handleGetTicket(w http.ResponseWriter, r *http.Request) {
	if !requireUser(w, r) {
		return
	}
	eventID := chi.URLParam(r, "id")
	ctx := r.Context()
	uid := userID(r)
	reg, err := h.registrationFor(ctx, uid, eventID)
	if err != nil {
		writeServer(w, r, err)
		return
	}
	if reg == nil {
		writeErr(w, r, http.StatusNotFound, "REGISTRATION_NOT_FOUND", "registration not found", nil)
		return
	}
	if reg.Status != "confirmed" && reg.Status != "checked_in" {
		writeErr(w, r, http.StatusForbidden, "NO_CONFIRMED_REGISTRATION",
			"a confirmed registration is required for a ticket", nil)
		return
	}
	row, err := h.loadEventRow(ctx, eventID)
	if err != nil {
		writeServer(w, r, err)
		return
	}
	if row.OrganizerID.Valid && nullText(row.OrgStatus) == "revoked" {
		writeErr(w, r, http.StatusForbidden, "CHECKIN_DISABLED", "check-in is disabled for this organizer", nil)
		return
	}
	if reg.Ticket == nil {
		ticket, err := h.ticketFor(ctx, eventID, reg.ID, uid, row.endsAt())
		if err != nil {
			writeServer(w, r, err)
			return
		}
		reg.Ticket = ticket
	}
	if reg.CheckIn != nil {
		reg.Ticket.State = "checked_in"
	} else if row.endsAt().Before(h.clock()) {
		reg.Ticket.State = "event_ended"
	}
	writeJSON(w, http.StatusOK, reg.Ticket)
}

// ---------------------------------------------------------------------------
// Reminder opt-in
// ---------------------------------------------------------------------------

func (h *Handler) handleSetReminder(w http.ResponseWriter, r *http.Request) {
	if !requireUser(w, r) {
		return
	}
	eventID := chi.URLParam(r, "id")
	ctx := r.Context()
	uid := userID(r)
	var body struct {
		OffsetMinutes json.RawMessage `json:"offsetMinutes"`
	}
	if !decodeJSON(w, r, &body) {
		return
	}
	if len(body.OffsetMinutes) == 0 {
		writeValidation(w, ValidationError{Field: "offsetMinutes", Message: "is required (number or null)"})
		return
	}
	var offset *int
	if string(body.OffsetMinutes) != "null" {
		var n int
		if err := json.Unmarshal(body.OffsetMinutes, &n); err != nil || !containsInt(reminderOffsets, n) {
			writeValidation(w, ValidationError{Field: "offsetMinutes", Message: "must be 60, 1440, 10080 or null"})
			return
		}
		offset = &n
	}

	tx, err := h.pool.Begin(ctx)
	if err != nil {
		writeServer(w, r, err)
		return
	}
	defer tx.Rollback(ctx)
	event, err := lockEvent(ctx, tx, eventID)
	if err != nil {
		if isNoRows(err) {
			writeErr(w, r, http.StatusNotFound, "EVENT_NOT_FOUND", "event not found", nil)
			return
		}
		writeServer(w, r, err)
		return
	}
	var regID string
	if err := tx.QueryRow(ctx, `
		UPDATE event_registrations SET reminder_offset_minutes=$3, updated_at=now()
		 WHERE event_id=$1 AND user_id=$2 AND status IN ('confirmed','waitlisted','checked_in')
		RETURNING id::text`, eventID, uid, offset).Scan(&regID); err != nil {
		if isNoRows(err) {
			writeErr(w, r, http.StatusNotFound, "REGISTRATION_NOT_FOUND", "registration not found", nil)
			return
		}
		writeServer(w, r, err)
		return
	}
	// Clear the previous queued reminder, then schedule the new offset.
	if _, err := tx.Exec(ctx, `
		DELETE FROM event_notifications
		 WHERE event_id=$1 AND user_id=$2 AND kind='reminder' AND status='queued'`, eventID, uid); err != nil {
		writeServer(w, r, err)
		return
	}
	if err := h.scheduleReminder(ctx, tx, eventID, uid, offset, event.start()); err != nil {
		writeServer(w, r, err)
		return
	}
	if err := tx.Commit(ctx); err != nil {
		writeServer(w, r, err)
		return
	}
	reg, err := h.registrationFor(ctx, uid, eventID)
	if err != nil {
		writeServer(w, r, err)
		return
	}
	if row, err := h.loadEventRow(ctx, eventID); err == nil {
		summary := row.summary()
		reg.Event = &summary
	}
	writeJSON(w, http.StatusOK, reg)
}
