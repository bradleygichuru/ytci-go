package mice

import (
	"context"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
)

// eventRow is the discovery projection shared by the public, account and admin
// list/detail handlers.
type eventRow struct {
	ID               string
	Title            string
	EventClass       string
	Format           string
	Access           string
	Status           string
	EventDate        pgtype.Date
	EndDate          pgtype.Date
	StartTime        pgtype.Text
	EndTime          pgtype.Text
	Timezone         string
	County           string
	CountyGid        pgtype.Int4
	ImageURL         pgtype.Text
	Capacity         pgtype.Int4
	WaitlistCapacity pgtype.Int4
	RegistrationMode string
	PriceAmount      pgtype.Numeric
	PriceCurrency    pgtype.Text
	SalesStatus      pgtype.Text
	SalesEnd         pgtype.Timestamptz
	TicketURL        pgtype.Text
	Description      pgtype.Text
	Version          int
	PublishedAt      pgtype.Timestamptz
	AuthorRef        pgtype.Text
	EditorialTheme   pgtype.Text

	VenueID          pgtype.Text
	VenueSlug        pgtype.Text
	VenueName        pgtype.Text
	VenueCounty      pgtype.Text
	VenueCountyGid   pgtype.Int4
	OrganizerID      pgtype.Text
	OrgSlug          pgtype.Text
	OrgDisplayName   pgtype.Text
	OrgTrustTier     pgtype.Text
	OrgStatus        pgtype.Text
	OrgVerifiedAt    pgtype.Timestamptz
	OrgExpiresAt     pgtype.Timestamptz
	OrgRevokedReason pgtype.Text

	Latitude      pgtype.Numeric
	Longitude     pgtype.Numeric
	AccReported   bool
	AccVerifiedAt pgtype.Timestamptz
	Going         int
}

const eventSelect = `
SELECT e.id::text, e.title, COALESCE(e.event_class,'community'), COALESCE(e.format,'in_person'),
       COALESCE(e.access,'public'), e.status, e.event_date, e.end_date, e.start_time, e.end_time,
       COALESCE(e.timezone,'Africa/Nairobi'), COALESCE(e.county,''), e.county_gid, e.image_url,
       e.capacity, e.waitlist_capacity, COALESCE(e.registration_mode,'none'),
       e.price_amount, e.price_currency, e.sales_status, e.sales_end, e.ticket_url,
       e.description, COALESCE(e.version,1), e.published_at, e.author_ref, e.editorial_theme,
       e.venue_id::text, v.slug, v.name, v.county, v.county_gid,
       e.organizer_profile_id::text, o.slug, o.display_name, o.trust_tier, o.verification_status,
       o.verified_at, o.verification_expires_at, o.revoked_reason,
       e.location_lat, e.location_lng,
       (ea.event_id IS NOT NULL OR vax.venue_id IS NOT NULL) AS acc_reported,
       COALESCE(ea.verified_at, vax.verified_at) AS acc_verified_at,
       (SELECT count(*) FROM event_registrations r
         WHERE r.event_id = e.id AND r.status IN ('confirmed','checked_in')) AS going
FROM events e
LEFT JOIN venues v ON v.id = e.venue_id
LEFT JOIN venue_accessibility vax ON vax.venue_id = v.id
LEFT JOIN organizer_profiles o ON o.id = e.organizer_profile_id
LEFT JOIN event_accessibility ea ON ea.event_id = e.id`

func scanEventRow(row pgx.Row) (eventRow, error) {
	var e eventRow
	err := row.Scan(
		&e.ID, &e.Title, &e.EventClass, &e.Format, &e.Access, &e.Status, &e.EventDate, &e.EndDate,
		&e.StartTime, &e.EndTime, &e.Timezone, &e.County, &e.CountyGid, &e.ImageURL,
		&e.Capacity, &e.WaitlistCapacity, &e.RegistrationMode,
		&e.PriceAmount, &e.PriceCurrency, &e.SalesStatus, &e.SalesEnd, &e.TicketURL,
		&e.Description, &e.Version, &e.PublishedAt, &e.AuthorRef, &e.EditorialTheme,
		&e.VenueID, &e.VenueSlug, &e.VenueName, &e.VenueCounty, &e.VenueCountyGid,
		&e.OrganizerID, &e.OrgSlug, &e.OrgDisplayName, &e.OrgTrustTier, &e.OrgStatus,
		&e.OrgVerifiedAt, &e.OrgExpiresAt, &e.OrgRevokedReason,
		&e.Latitude, &e.Longitude,
		&e.AccReported, &e.AccVerifiedAt, &e.Going,
	)
	return e, err
}

func (e eventRow) startsAt() time.Time {
	return eventStart(e.EventDate.Time, nullText(e.StartTime), e.Timezone)
}

func (e eventRow) endsAt() time.Time {
	var end *time.Time
	if e.EndDate.Valid {
		t := e.EndDate.Time
		end = &t
	}
	return eventEnd(e.EventDate.Time, end, nullText(e.EndTime), e.Timezone)
}

func (e eventRow) summary() EventSummary {
	s := EventSummary{
		ID:               e.ID,
		Title:            e.Title,
		EventClass:       e.EventClass,
		Format:           e.Format,
		Access:           e.Access,
		Status:           normaliseStatus(e.Status),
		StartsAt:         e.startsAt(),
		EndsAt:           timePtr(e.endsAt()),
		Timezone:         e.Timezone,
		County:           e.County,
		CountyGid:        nullInt(e.CountyGid),
		ImageURL:         nullText(e.ImageURL),
		Capacity:         nullInt(e.Capacity),
		RegistrationMode: e.RegistrationMode,
		WaitlistOpen:     e.RegistrationMode == "rsvp" && (e.WaitlistCapacity.Valid || e.Capacity.Valid),
		Accessibility: AccessibilitySummary{
			State:      "unknown",
			VerifiedAt: nullTime(e.AccVerifiedAt),
		},
		LocationLat: nullNumeric(e.Latitude),
		LocationLng: nullNumeric(e.Longitude),
	}
	if e.AccReported {
		s.Accessibility.State = "reported"
		s.Accessibility.Label = accessibilityLabel(s.Accessibility.VerifiedAt)
	}
	if e.VenueSlug.Valid && e.VenueName.Valid {
		s.Venue = &VenueRef{
			Slug:      e.VenueSlug.String,
			Name:      e.VenueName.String,
			County:    nullText(e.VenueCounty),
			CountyGid: nullInt(e.VenueCountyGid),
		}
	}
	if e.OrgSlug.Valid && e.OrgDisplayName.Valid {
		tier := nullText(e.OrgTrustTier)
		status := nullText(e.OrgStatus)
		if tier == "" {
			tier = "community_organizer"
		}
		s.Organizer = &OrganizerRef{
			Slug:        e.OrgSlug.String,
			DisplayName: e.OrgDisplayName.String,
			TrustTier:   tier,
			Label:       trustLabel(tier, status),
		}
	}
	if e.Capacity.Valid {
		remaining := int(e.Capacity.Int32) - e.Going
		if remaining < 0 {
			remaining = 0
		}
		s.SeatsRemaining = &remaining
	}
	if e.PriceAmount.Valid {
		amount := nullNumeric(e.PriceAmount)
		if amount != nil {
			currency := nullText(e.PriceCurrency)
			s.Price = &Price{Amount: *amount, Currency: currency, Display: formatPrice(*amount, currency)}
		}
	}
	return s
}

func timePtr(t time.Time) *time.Time { return &t }

func accessibilityLabel(verifiedAt *time.Time) string {
	if verifiedAt == nil {
		return "Reported by the organizer"
	}
	return "Reported by the organizer, last verified " + verifiedAt.In(nairobi).Format("2 January 2006")
}

// eventFilters builds the shared WHERE clause for event list queries.
type eventFilters struct {
	Status     string
	EventClass string
	Format     string
	CountyGid  string
	Query      string
	From       string
	Public     bool
	Organizer  string
}

func (f eventFilters) where(uidParam string) (string, []any) {
	clauses := []string{"1=1"}
	args := []any{}
	add := func(clause string, value any) {
		args = append(args, value)
		clauses = append(clauses, fmt.Sprintf(clause, len(args)))
	}
	if f.Public {
		clauses = append(clauses, "e.status IN ('published','scheduled','postponed','cancelled')")
		clauses = append(clauses, "COALESCE(e.access,'public') <> 'invite_only'")
	}
	if f.Status != "" {
		if f.Status == "published" {
			clauses = append(clauses, "e.status IN ('published','scheduled')")
		} else {
			add("e.status = $%d", f.Status)
		}
	}
	if f.EventClass != "" {
		add("COALESCE(e.event_class,'community') = $%d", f.EventClass)
	}
	if f.Format != "" {
		add("COALESCE(e.format,'in_person') = $%d", f.Format)
	}
	if f.CountyGid != "" {
		if gid, err := strconv.Atoi(f.CountyGid); err == nil {
			add("e.county_gid = $%d", gid)
		}
	}
	if f.Query != "" {
		add("e.title ILIKE '%%' || $%d || '%%'", f.Query)
	}
	if f.From != "" {
		add("e.event_date >= $%d::date", f.From)
	}
	if f.Organizer != "" {
		add("e.organizer_profile_id = $%d::uuid", f.Organizer)
	}
	_ = uidParam
	return strings.Join(clauses, " AND "), args
}

func (h *Handler) listEvents(ctx context.Context, f eventFilters, limit int, cursor *string, uid string) ([]EventSummary, bool, error) {
	where, args := f.where("")
	q := eventSelect + " WHERE " + where
	if cursor != nil && *cursor != "" {
		c, ok := decodeEventCursor(*cursor)
		if ok {
			args = append(args, c.date, c.id)
			q += fmt.Sprintf(" AND (e.event_date, e.id) > ($%d::date, $%d::uuid)", len(args)-1, len(args))
		}
	}
	args = append(args, limit+1)
	q += fmt.Sprintf(" ORDER BY e.event_date ASC, e.id ASC LIMIT $%d", len(args))

	rows, err := h.pool.Query(ctx, q, args...)
	if err != nil {
		return nil, false, err
	}
	defer rows.Close()

	out := make([]EventSummary, 0, limit)
	for rows.Next() {
		e, err := scanEventRow(rows)
		if err != nil {
			return nil, false, err
		}
		out = append(out, e.summary())
	}
	if err := rows.Err(); err != nil {
		return nil, false, err
	}
	if uid != "" {
		h.attachSaved(ctx, out, uid)
	}
	return out, len(out) > limit, nil
}

func (h *Handler) attachSaved(ctx context.Context, items []EventSummary, uid string) {
	ids := make([]string, 0, len(items))
	for _, item := range items {
		ids = append(ids, item.ID)
	}
	if len(ids) == 0 {
		return
	}
	rows, err := h.pool.Query(ctx, `
		SELECT event_id::text FROM event_saved_events WHERE user_id = $1 AND event_id::text = ANY($2)
		UNION
		SELECT event_id::text FROM event_saves WHERE user_id::text = $1 AND event_id::text = ANY($2)`, uid, ids)
	if err != nil {
		return
	}
	defer rows.Close()
	saved := map[string]bool{}
	for rows.Next() {
		var id string
		if rows.Scan(&id) == nil {
			saved[id] = true
		}
	}
	for i := range items {
		items[i].Saved = saved[items[i].ID]
	}
}

type eventCursor struct {
	date string
	id   string
}

func encodeEventCursor(e EventSummary) (string, bool) {
	if e.ID == "" {
		return "", false
	}
	return paginationEncode(e.StartsAt.In(nairobi).Format("2006-01-02"), e.ID), true
}

func decodeEventCursor(raw string) (eventCursor, bool) {
	sortValue, id, ok := paginationDecode(raw)
	if !ok {
		return eventCursor{}, false
	}
	return eventCursor{date: sortValue, id: id}, true
}

func (h *Handler) handleListPublicEvents(w http.ResponseWriter, r *http.Request) {
	limit, _ := parsePage(r)
	var raw *string
	if c := r.URL.Query().Get("cursor"); c != "" {
		raw = &c
	}
	f := eventFilters{
		EventClass: r.URL.Query().Get("eventClass"),
		Format:     r.URL.Query().Get("format"),
		CountyGid:  r.URL.Query().Get("countyGid"),
		Query:      r.URL.Query().Get("q"),
		From:       r.URL.Query().Get("from"),
		Public:     true,
	}
	if failures := validateEventFilters(f); len(failures) > 0 {
		writeValidation(w, failures...)
		return
	}
	items, hasMore, err := h.listEvents(r.Context(), f, limit, raw, "")
	if err != nil {
		writeServer(w, r, err)
		return
	}
	writeSummaryPage(w, limit, items, hasMore)
}

func validateEventFilters(f eventFilters) []ValidationError {
	var out []ValidationError
	if f.EventClass != "" && !contains(eventClasses, f.EventClass) {
		out = append(out, ValidationError{Field: "eventClass", Message: "unknown event class"})
	}
	if f.Format != "" && !contains(eventFormats, f.Format) {
		out = append(out, ValidationError{Field: "format", Message: "unknown format"})
	}
	if f.From != "" {
		if _, err := time.Parse("2006-01-02", f.From); err != nil {
			out = append(out, ValidationError{Field: "from", Message: "must be a date (YYYY-MM-DD)"})
		}
	}
	if len(f.Query) > 120 {
		out = append(out, ValidationError{Field: "q", Message: "must be at most 120 characters"})
	}
	return out
}

func writeSummaryPage(w http.ResponseWriter, limit int, items []EventSummary, hasMore bool) {
	if hasMore && len(items) > limit {
		items = items[:limit]
	}
	page := Page[EventSummary]{Items: items, HasMore: hasMore}
	if page.Items == nil {
		page.Items = []EventSummary{}
	}
	if hasMore && len(items) > 0 {
		if c, ok := encodeEventCursor(items[len(items)-1]); ok {
			page.NextCursor = &c
		}
	}
	writeJSON(w, http.StatusOK, page)
}

// loadEventRow fetches one event by id.
func (h *Handler) loadEventRow(ctx context.Context, id string) (eventRow, error) {
	return scanEventRow(h.pool.QueryRow(ctx, eventSelect+" WHERE e.id = $1", id))
}

func (h *Handler) handleGetPublicEvent(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	if !validUUID(id) {
		writeErr(w, r, http.StatusNotFound, "EVENT_NOT_FOUND", "event not found", nil)
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
	if row.Access == "invite_only" || status == "draft" {
		writeErr(w, r, http.StatusNotFound, "EVENT_NOT_FOUND", "event not found", nil)
		return
	}
	detail, err := h.buildEventDetail(r.Context(), row, userID(r), true)
	if err != nil {
		writeServer(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, detail)
}

// buildEventDetail expands the agenda, provenance and registration state.
func (h *Handler) buildEventDetail(ctx context.Context, row eventRow, uid string, public bool) (*EventDetail, error) {
	d := &EventDetail{EventSummary: row.summary()}
	d.EventSummary.Saved = false
	if uid != "" {
		var saved bool
		_ = h.pool.QueryRow(ctx, `
			SELECT EXISTS(SELECT 1 FROM event_saved_events WHERE user_id=$1 AND event_id=$2)
			    OR EXISTS(SELECT 1 FROM event_saves WHERE user_id::text=$1 AND event_id=$2)`, uid, row.ID).Scan(&saved)
		d.EventSummary.Saved = saved
	}
	d.Description = nullText(row.Description)
	d.AttendeeCount = row.Going
	d.Highlights = []string{}
	_ = h.pool.QueryRow(ctx, `SELECT highlights FROM events WHERE id = $1`, row.ID).Scan(&jsonStrings{&d.Highlights})
	if len(d.Highlights) == 0 {
		// Legacy highlights live in their own table; keep them readable until
		// the admin cutover writes events.highlights.
		rows, err := h.pool.Query(ctx, `
			SELECT label FROM event_highlights WHERE event_id=$1 ORDER BY display_order ASC`, row.ID)
		if err == nil {
			for rows.Next() {
				var label string
				if rows.Scan(&label) == nil {
					d.Highlights = append(d.Highlights, label)
				}
			}
			rows.Close()
		}
	}

	if row.Capacity.Valid {
		remaining := int(row.Capacity.Int32) - row.Going
		if remaining < 0 {
			remaining = 0
		}
		d.SeatsRemaining = &remaining
	}
	if row.RegistrationMode != "" {
		d.RegistrationMode = row.RegistrationMode
	}
	d.WaitlistOpen = row.WaitlistCapacity.Valid || row.Capacity.Valid

	agenda, speakers, err := h.loadAgendaFor(ctx, row.ID, public)
	if err != nil {
		return nil, err
	}
	if len(agenda) > 0 {
		d.Agenda = agenda
		d.SessionCount = len(agenda)
		d.SpeakerCount = speakers
	}

	acc, err := h.loadAccessibility(ctx, "event", row.ID)
	if err != nil {
		return nil, err
	}
	d.AccessibilityDetail = acc

	if row.VenueID.Valid {
		venue, err := h.loadVenue(ctx, "id", row.VenueID.String)
		if err != nil {
			return nil, err
		}
		d.VenueDetail = venue
	}
	if row.OrganizerID.Valid {
		org, err := h.loadOrganizer(ctx, row.OrganizerID.String)
		if err != nil {
			return nil, err
		}
		d.OrganizerDetail = org
	}

	questions, err := h.loadRegistrationQuestions(ctx, row.ID)
	if err != nil {
		return nil, err
	}
	d.RegistrationQuestions = questions

	if row.RegistrationMode == "ticket_handoff" {
		url := nullText(row.TicketURL)
		if url != "" {
			soldBy := nullText(row.OrgDisplayName)
			if soldBy == "" {
				soldBy = row.County
			}
			sales := &Sales{
				Status:     nullText(row.SalesStatus),
				TicketURL:  url,
				SoldBy:     soldBy,
				SalesEnd:   nullTime(row.SalesEnd),
				Disclosure: "YTCI does not process payment.",
			}
			if sales.Status == "" {
				sales.Status = "on_sale"
			}
			d.Sales = sales
		}
	}

	if uid != "" && !public {
		reg, err := h.registrationFor(ctx, uid, row.ID)
		if err != nil {
			return nil, err
		}
		if reg != nil {
			summary := row.summary()
			reg.Event = &summary
			d.Registration = reg
		}
		notes, err := h.listNotifications(ctx, uid, row.ID, 20, nil)
		if err != nil {
			return nil, err
		}
		d.Notifications = notes
	}
	return d, nil
}

func (h *Handler) loadAgenda(ctx context.Context, eventID string) ([]Session, int, error) {
	return h.loadAgendaFor(ctx, eventID, true)
}

// loadAgendaFor loads the agenda. The public projection hides speakers who
// withdrew consent; the admin projection keeps them so consent can be restored.
func (h *Handler) loadAgendaFor(ctx context.Context, eventID string, public bool) ([]Session, int, error) {
	rows, err := h.pool.Query(ctx, `
		SELECT s.id::text, s.day::text, s.starts_at, s.ends_at, s.room, s.track, s.title,
		       s.description, s.capacity
		  FROM event_sessions s WHERE s.event_id = $1
		 ORDER BY s.day ASC, s.starts_at ASC, s.display_order ASC`, eventID)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()
	out := []Session{}
	speakerTotal := 0
	for rows.Next() {
		var s Session
		var day string
		var room, track, description pgtype.Text
		var capacity pgtype.Int4
		if err := rows.Scan(&s.ID, &day, &s.StartsAt, &s.EndsAt, &room, &track, &s.Title, &description, &capacity); err != nil {
			return nil, 0, err
		}
		s.Day = day
		s.Room = nullText(room)
		s.Track = nullText(track)
		s.Description = nullText(description)
		s.Capacity = nullInt(capacity)
		speakers, err := h.loadSessionSpeakers(ctx, s.ID, public)
		if err != nil {
			return nil, 0, err
		}
		s.Speakers = speakers
		speakerTotal += len(speakers)
		out = append(out, s)
	}
	return out, speakerTotal, rows.Err()
}

func (h *Handler) loadSessionSpeakers(ctx context.Context, sessionID string, public bool) ([]Speaker, error) {
	q := `
		SELECT p.id::text, p.display_name, p.title, p.organization, sp.speaker_role, p.photo_url,
		       COALESCE(p.consent_status,'granted')
		  FROM session_speakers sp
		  JOIN event_people p ON p.id = sp.person_id
		 WHERE sp.session_id = $1`
	if public {
		q += ` AND p.is_public = true`
	}
	q += ` ORDER BY sp.display_order ASC, p.display_name ASC`
	rows, err := h.pool.Query(ctx, q, sessionID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Speaker{}
	for rows.Next() {
		var s Speaker
		var title, org, photo pgtype.Text
		if err := rows.Scan(&s.ID, &s.DisplayName, &title, &org, &s.Role, &photo, &s.ConsentStatus); err != nil {
			return nil, err
		}
		s.Title = nullText(title)
		s.Organization = nullText(org)
		s.PhotoURL = nullText(photo)
		out = append(out, s)
	}
	return out, rows.Err()
}

func (h *Handler) loadRegistrationQuestions(ctx context.Context, eventID string) ([]RegistrationQuestion, error) {
	rows, err := h.pool.Query(ctx, `
		SELECT id::text, prompt_en, prompt_sw, answer_kind, choices, required
		  FROM event_registration_questions WHERE event_id = $1
		 ORDER BY display_order ASC`, eventID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []RegistrationQuestion{}
	for rows.Next() {
		var q RegistrationQuestion
		var sw pgtype.Text
		if err := rows.Scan(&q.ID, &q.PromptEn, &sw, &q.AnswerKind, &q.Choices, &q.Required); err != nil {
			return nil, err
		}
		q.PromptSw = nullText(sw)
		out = append(out, q)
	}
	return out, rows.Err()
}

func (h *Handler) handleEventCalendar(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	if !validUUID(id) {
		writeErr(w, r, http.StatusNotFound, "EVENT_NOT_FOUND", "event not found", nil)
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
	if normaliseStatus(row.Status) == "draft" || row.Access == "invite_only" {
		writeErr(w, r, http.StatusNotFound, "EVENT_NOT_FOUND", "event not found", nil)
		return
	}
	start := row.startsAt()
	end := row.endsAt()
	alarm := 1440
	if v := r.URL.Query().Get("alarm_minutes"); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			switch n {
			case 60, 1440, 10080:
				alarm = n
			}
		}
	}
	location := row.County
	if row.VenueName.Valid {
		location = row.VenueName.String + ", " + row.County
	}
	desc := nullText(row.Description)
	if desc == "" {
		desc = row.Title
	}
	ics := buildICS(icsEvent{
		UID:         row.ID + "@ytci",
		Summary:     row.Title,
		Description: desc,
		Location:    location,
		Start:       start,
		End:         end,
		AlarmMin:    alarm,
	})
	w.Header().Set("Content-Type", "text/calendar; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte(ics))
}

func validUUID(s string) bool {
	var u pgtype.UUID
	return u.Scan(s) == nil && u.Valid
}

func contains(list []string, v string) bool {
	for _, item := range list {
		if item == v {
			return true
		}
	}
	return false
}

// jsonStrings scans a jsonb array of strings into a []string.
type jsonStrings struct{ dst *[]string }

func (j *jsonStrings) Scan(src any) error {
	var raw []byte
	switch v := src.(type) {
	case []byte:
		raw = v
	case string:
		raw = []byte(v)
	case nil:
		return nil
	default:
		return fmt.Errorf("jsonStrings: unsupported type %T", src)
	}
	var out []string
	if err := jsonUnmarshal(raw, &out); err != nil {
		return err
	}
	*j.dst = out
	return nil
}

// ---------------------------------------------------------------------------
// Admin event authoring
// ---------------------------------------------------------------------------

type eventWrite struct {
	Title              string              `json:"title"`
	OrganizerProfileID *string             `json:"organizerProfileId"`
	VenueID            *string             `json:"venueId"`
	EventClass         string              `json:"eventClass"`
	Format             string              `json:"format"`
	Access             string              `json:"access"`
	Type               string              `json:"type"`
	EventDate          string              `json:"eventDate"`
	EndDate            *string             `json:"endDate"`
	StartTime          string              `json:"startTime"`
	EndTime            string              `json:"endTime"`
	Timezone           string              `json:"timezone"`
	Description        string              `json:"description"`
	Capacity           *int                `json:"capacity"`
	WaitlistCapacity   *int                `json:"waitlistCapacity"`
	RegistrationMode   string              `json:"registrationMode"`
	PriceAmount        *float64            `json:"priceAmount"`
	PriceCurrency      *string             `json:"priceCurrency"`
	SalesEnd           *string             `json:"salesEnd"`
	TicketURL          *string             `json:"ticketUrl"`
	Accessibility      *accessibilityWrite `json:"accessibility"`
}

type accessibilityWrite struct {
	StepFreeEntrance      string `json:"stepFreeEntrance"`
	StepFreeRoute         string `json:"stepFreeRoute"`
	AccessibleToilet      string `json:"accessibleToilet"`
	AccessibleParking     string `json:"accessibleParking"`
	AccessibleSeating     string `json:"accessibleSeating"`
	Captioning            string `json:"captioning"`
	SignLanguage          string `json:"signLanguage"`
	HearingLoop           string `json:"hearingLoop"`
	QuietRoom             string `json:"quietRoom"`
	AssistanceDogs        string `json:"assistanceDogs"`
	CompanionTicketPolicy string `json:"companionTicketPolicy"`
	ContentNotes          string `json:"contentNotes"`
}

func (w eventWrite) validate() []ValidationError {
	var out []ValidationError
	if strings.TrimSpace(w.Title) == "" || len(w.Title) > 200 {
		out = append(out, ValidationError{Field: "title", Message: "title is required (max 200 characters)"})
	}
	if !contains(eventClasses, w.EventClass) {
		out = append(out, ValidationError{Field: "eventClass", Message: "must be one of meeting, incentive, conference, exhibition, community"})
	}
	if !contains(eventFormats, w.Format) {
		out = append(out, ValidationError{Field: "format", Message: "must be one of in_person, online, hybrid"})
	}
	if !contains(eventAccess, w.Access) {
		out = append(out, ValidationError{Field: "access", Message: "must be one of public, registered, invite_only"})
	}
	if strings.TrimSpace(w.Type) == "" {
		out = append(out, ValidationError{Field: "type", Message: "type is required"})
	}
	if _, err := time.Parse("2006-01-02", w.EventDate); err != nil {
		out = append(out, ValidationError{Field: "eventDate", Message: "must be a date (YYYY-MM-DD)"})
	}
	if w.EndDate != nil && *w.EndDate != "" {
		if _, err := time.Parse("2006-01-02", *w.EndDate); err != nil {
			out = append(out, ValidationError{Field: "endDate", Message: "must be a date (YYYY-MM-DD)"})
		}
	}
	if w.StartTime != "" && !validClock(w.StartTime) {
		out = append(out, ValidationError{Field: "startTime", Message: "must be HH:MM"})
	}
	if w.EndTime != "" && !validClock(w.EndTime) {
		out = append(out, ValidationError{Field: "endTime", Message: "must be HH:MM"})
	}
	if w.RegistrationMode != "" && !contains(registrationModes, w.RegistrationMode) {
		out = append(out, ValidationError{Field: "registrationMode", Message: "must be one of rsvp, ticket_handoff, none"})
	}
	if w.Capacity != nil && *w.Capacity < 0 {
		out = append(out, ValidationError{Field: "capacity", Message: "must be zero or greater"})
	}
	if (w.PriceAmount != nil || w.RegistrationMode == "ticket_handoff") &&
		(w.PriceAmount == nil || w.PriceCurrency == nil || w.TicketURL == nil || w.SalesEnd == nil) {
		out = append(out, ValidationError{Field: "registrationMode", Message: "paid events require priceAmount, priceCurrency, ticketUrl and salesEnd"})
	}
	return out
}

func validClock(s string) bool {
	_, err := time.Parse("15:04", s)
	return err == nil
}

func optionalUUID(s *string) any {
	if s == nil || *s == "" {
		return nil
	}
	return *s
}

func (h *Handler) handleAdminCreateEvent(w http.ResponseWriter, r *http.Request) {
	if !requirePermission(w, r, "events.create") {
		return
	}
	var body eventWrite
	if !decodeJSON(w, r, &body) {
		return
	}
	if failures := body.validate(); len(failures) > 0 {
		writeValidation(w, failures...)
		return
	}
	row, err := h.insertEvent(r, body, userID(r))
	if err != nil {
		writeServer(w, r, err)
		return
	}
	detail, err := h.buildEventDetail(r.Context(), row, userID(r), false)
	if err != nil {
		writeServer(w, r, err)
		return
	}
	writeJSON(w, http.StatusCreated, detail)
}

func legacyTheme(theme string) string {
	if contains(legacyThemes, theme) {
		return theme
	}
	return "cultural"
}

func (h *Handler) insertEvent(r *http.Request, body eventWrite, actor string) (eventRow, error) {
	ctx := r.Context()
	tz := body.Timezone
	if tz == "" {
		tz = "Africa/Nairobi"
	}
	organizerName := ""
	county := ""
	if body.OrganizerProfileID != nil && *body.OrganizerProfileID != "" {
		_ = h.pool.QueryRow(ctx, `SELECT display_name FROM organizer_profiles WHERE id=$1`, *body.OrganizerProfileID).Scan(&organizerName)
	}
	if body.VenueID != nil && *body.VenueID != "" {
		_ = h.pool.QueryRow(ctx, `SELECT COALESCE(county,'') FROM venues WHERE id=$1`, *body.VenueID).Scan(&county)
	}
	mode := body.RegistrationMode
	if mode == "" {
		mode = "none"
	}
	var endDate any
	if body.EndDate != nil && *body.EndDate != "" {
		endDate = *body.EndDate
	}
	var salesStatus any
	if body.TicketURL != nil && *body.TicketURL != "" {
		salesStatus = "on_sale"
	}

	var id string
	err := h.pool.QueryRow(ctx, `
		INSERT INTO events (
			title, organizer, county, venue, event_date, end_date, start_time, end_time, type, status,
			event_class, format, access, venue_id, organizer_profile_id, capacity, waitlist_capacity,
			registration_mode, price_amount, price_currency, sales_status, sales_end, ticket_url,
			timezone, description, version, author_ref, editorial_theme, highlights
		) VALUES (
			$1, $2, $3, NULL, $4::date, $5::date, $6, $7, $8, 'draft',
			$9, $10, $11, $12::uuid, $13::uuid, $14, $15,
			$16, $17, $18, $19, $20::timestamptz, $21,
			$22, $23, 1, $24, $25, '[]'::jsonb
		) RETURNING id::text`,
		body.Title, organizerName, county, body.EventDate, endDate, body.StartTime, body.EndTime,
		legacyTheme(body.Type), body.EventClass, body.Format, body.Access,
		optionalUUID(body.VenueID), optionalUUID(body.OrganizerProfileID),
		body.Capacity, body.WaitlistCapacity, mode, body.PriceAmount, body.PriceCurrency,
		salesStatus, body.SalesEnd, body.TicketURL, tz, body.Description, actor, body.Type,
	).Scan(&id)
	if err != nil {
		return eventRow{}, err
	}
	if body.VenueID != nil && *body.VenueID != "" {
		_, _ = h.pool.Exec(ctx, `UPDATE events SET county_gid=(SELECT county_gid FROM venues WHERE id=$2) WHERE id=$1`, id, *body.VenueID)
	}
	if body.Accessibility != nil {
		if err := h.upsertAccessibility(ctx, "event", id, body.Accessibility, actor); err != nil {
			return eventRow{}, err
		}
	}
	return h.loadEventRow(ctx, id)
}

func (h *Handler) handleAdminUpdateEvent(w http.ResponseWriter, r *http.Request) {
	if !requirePermission(w, r, "events.edit_draft") {
		return
	}
	id := chi.URLParam(r, "id")
	row, err := h.loadEventRow(r.Context(), id)
	if err != nil {
		if isNoRows(err) {
			writeErr(w, r, http.StatusNotFound, "EVENT_NOT_FOUND", "event not found", nil)
			return
		}
		writeServer(w, r, err)
		return
	}
	var body eventWrite
	if !decodeJSON(w, r, &body) {
		return
	}
	if failures := body.validate(); len(failures) > 0 {
		writeValidation(w, failures...)
		return
	}
	published := normaliseStatus(row.Status) == "published" || normaliseStatus(row.Status) == "postponed"
	if published && materialChange(row, body) {
		writeErr(w, r, http.StatusConflict, "MATERIAL_EDIT_REQUIRES_REVIEW",
			"material changes to a published event need a change notice and re-publish", nil)
		return
	}
	tz := body.Timezone
	if tz == "" {
		tz = row.Timezone
	}
	organizerName := ""
	county := row.County
	if body.OrganizerProfileID != nil && *body.OrganizerProfileID != "" {
		_ = h.pool.QueryRow(r.Context(), `SELECT display_name FROM organizer_profiles WHERE id=$1`, *body.OrganizerProfileID).Scan(&organizerName)
	}
	if body.VenueID != nil && *body.VenueID != "" {
		_ = h.pool.QueryRow(r.Context(), `SELECT COALESCE(county,'') FROM venues WHERE id=$1`, *body.VenueID).Scan(&county)
	}
	var endDate any
	if body.EndDate != nil && *body.EndDate != "" {
		endDate = *body.EndDate
	}
	mode := body.RegistrationMode
	if mode == "" {
		mode = row.RegistrationMode
	}
	version := row.Version
	if !published {
		version = row.Version + 1
	}
	_, err = h.pool.Exec(r.Context(), `
		UPDATE events SET
			title=$2, organizer=$3, county=$4, event_date=$5::date, end_date=$6::date,
			start_time=$7, end_time=$8, type=$9, event_class=$10, format=$11, access=$12,
			venue_id=$13::uuid, organizer_profile_id=$14::uuid, capacity=$15, waitlist_capacity=$16,
			registration_mode=$17, price_amount=$18, price_currency=$19, sales_end=$20::timestamptz,
			ticket_url=$21, timezone=$22, description=$23, version=$24, editorial_theme=$25,
			updated_at=now()
		WHERE id=$1`,
		id, body.Title, organizerName, county, body.EventDate, endDate, body.StartTime, body.EndTime,
		legacyTheme(body.Type), body.EventClass, body.Format, body.Access,
		optionalUUID(body.VenueID), optionalUUID(body.OrganizerProfileID),
		body.Capacity, body.WaitlistCapacity, mode, body.PriceAmount, body.PriceCurrency,
		body.SalesEnd, body.TicketURL, tz, body.Description, version, body.Type,
	)
	if err != nil {
		writeServer(w, r, err)
		return
	}
	if body.VenueID != nil && *body.VenueID != "" {
		_, _ = h.pool.Exec(r.Context(), `UPDATE events SET county_gid=(SELECT county_gid FROM venues WHERE id=$2) WHERE id=$1`, id, *body.VenueID)
	}
	if body.Accessibility != nil {
		if err := h.upsertAccessibility(r.Context(), "event", id, body.Accessibility, userID(r)); err != nil {
			writeServer(w, r, err)
			return
		}
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

func sameDate(p pgtype.Date, s string) bool {
	if !p.Valid || s == "" {
		return !p.Valid && s == ""
	}
	return p.Time.Format("2006-01-02") == s
}

func sameText(p pgtype.Text, s string) bool { return nullText(p) == s }

func sameInt(p pgtype.Int4, v *int) bool {
	if !p.Valid {
		return v == nil
	}
	return v != nil && int(p.Int32) == *v
}

// materialChange reports whether a published event edit touches a material
// field (ADR 0011 §10).
func materialChange(row eventRow, body eventWrite) bool {
	if !sameDate(row.EventDate, body.EventDate) {
		return true
	}
	if !sameText(row.StartTime, body.StartTime) || !sameText(row.EndTime, body.EndTime) {
		return true
	}
	if body.EndDate != nil && !sameDate(row.EndDate, *body.EndDate) {
		return true
	}
	venue := ""
	if row.VenueID.Valid {
		venue = row.VenueID.String
	}
	nextVenue := ""
	if body.VenueID != nil {
		nextVenue = *body.VenueID
	}
	if venue != nextVenue {
		return true
	}
	if !sameInt(row.Capacity, body.Capacity) {
		return true
	}
	return false
}

func (h *Handler) handleAdminListEvents(w http.ResponseWriter, r *http.Request) {
	if !requirePermission(w, r, "events.read") {
		return
	}
	limit, _ := parsePage(r)
	var cursor *string
	if c := r.URL.Query().Get("cursor"); c != "" {
		cursor = &c
	}
	f := eventFilters{Status: r.URL.Query().Get("status")}
	items, hasMore, err := h.listEvents(r.Context(), f, limit, cursor, userID(r))
	if err != nil {
		writeServer(w, r, err)
		return
	}
	writeSummaryPage(w, limit, items, hasMore)
}

func (h *Handler) handleAdminGetEvent(w http.ResponseWriter, r *http.Request) {
	if !requirePermission(w, r, "events.read") {
		return
	}
	row, err := h.loadEventRow(r.Context(), chi.URLParam(r, "id"))
	if err != nil {
		if isNoRows(err) {
			writeErr(w, r, http.StatusNotFound, "EVENT_NOT_FOUND", "event not found", nil)
			return
		}
		writeServer(w, r, err)
		return
	}
	detail, err := h.buildEventDetail(r.Context(), row, userID(r), false)
	if err != nil {
		writeServer(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, detail)
}

func (h *Handler) handleAdminPublishEvent(w http.ResponseWriter, r *http.Request) {
	if !requirePermission(w, r, "events.publish") {
		return
	}
	id := chi.URLParam(r, "id")
	row, err := h.loadEventRow(r.Context(), id)
	if err != nil {
		if isNoRows(err) {
			writeErr(w, r, http.StatusNotFound, "EVENT_NOT_FOUND", "event not found", nil)
			return
		}
		writeServer(w, r, err)
		return
	}
	if normaliseStatus(row.Status) == "cancelled" {
		writeErr(w, r, http.StatusConflict, "EVENT_CANCELLED", "a cancelled event cannot be published", nil)
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
	_, err = h.pool.Exec(r.Context(), `
		UPDATE events SET status='published', published_at=COALESCE(published_at, now()),
		       version=version+1, updated_at=now()
		 WHERE id=$1`, id)
	if err != nil {
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

// readinessFailures applies the publish readiness gate (ADR 0012 §2).
func readinessFailures(row eventRow) []ValidationError {
	var out []ValidationError
	if strings.TrimSpace(row.Title) == "" {
		out = append(out, ValidationError{Field: "EVENT_NOT_READY", Message: "a title is required"})
	}
	if (row.Format == "in_person" || row.Format == "hybrid") && !row.VenueID.Valid {
		out = append(out, ValidationError{Field: "VENUE_REQUIRED", Message: "an in-person event needs a venue"})
	}
	if row.RegistrationMode == "rsvp" && !row.Capacity.Valid {
		out = append(out, ValidationError{Field: "CAPACITY_REQUIRED", Message: "an RSVP event needs a capacity"})
	}
	if row.OrganizerID.Valid {
		status := nullText(row.OrgStatus)
		tier := nullText(row.OrgTrustTier)
		if status != "verified" {
			out = append(out, ValidationError{Field: "ORGANIZER_NOT_VERIFIED", Message: "the organizer is not verified"})
		}
		if status == "revoked" {
			out = append(out, ValidationError{Field: "ORGANIZER_REVOKED", Message: "the organizer is revoked"})
		}
		if tier == "community_organizer" && row.EventClass != "community" {
			out = append(out, ValidationError{Field: "ORGANIZER_TIER", Message: "community organizers may publish community events only"})
		}
	}
	return out
}

// ---------------------------------------------------------------------------
// Saved events
// ---------------------------------------------------------------------------

func (h *Handler) handleSaveEvent(w http.ResponseWriter, r *http.Request) {
	if !requireUser(w, r) {
		return
	}
	id := chi.URLParam(r, "id")
	if !validUUID(id) {
		writeErr(w, r, http.StatusNotFound, "EVENT_NOT_FOUND", "event not found", nil)
		return
	}
	if !h.eventExists(r, id) {
		writeErr(w, r, http.StatusNotFound, "EVENT_NOT_FOUND", "event not found", nil)
		return
	}
	uid := userID(r)
	if _, err := h.pool.Exec(r.Context(), `INSERT INTO event_saved_events (user_id, event_id) VALUES ($1,$2) ON CONFLICT DO NOTHING`, uid, id); err != nil {
		writeServer(w, r, err)
		return
	}
	if validUUID(uid) {
		_, _ = h.pool.Exec(r.Context(), `INSERT INTO event_saves (user_id, event_id) VALUES ($1::uuid,$2) ON CONFLICT DO NOTHING`, uid, id)
	}
	writeJSON(w, http.StatusCreated, Acknowledgement{OK: true, ID: id})
}

func (h *Handler) handleListSavedEvents(w http.ResponseWriter, r *http.Request) {
	if !requireUser(w, r) {
		return
	}
	uid := userID(r)
	limit, _ := parsePage(r)
	rows, err := h.pool.Query(r.Context(), `
		SELECT event_id::text FROM event_saved_events WHERE user_id = $1
		UNION
		SELECT event_id::text FROM event_saves WHERE user_id::text = $1`, uid)
	if err != nil {
		writeServer(w, r, err)
		return
	}
	ids := []string{}
	for rows.Next() {
		var id string
		if rows.Scan(&id) == nil {
			ids = append(ids, id)
		}
	}
	rows.Close()
	if len(ids) == 0 {
		writeJSON(w, http.StatusOK, Page[EventSummary]{Items: []EventSummary{}})
		return
	}
	where, args := "", []any{}
	for i, id := range ids {
		if i > 0 {
			where += ","
		}
		args = append(args, id)
		where += fmt.Sprintf("$%d::uuid", len(args))
	}
	args = append(args, limit+1)
	q := eventSelect + " WHERE e.id IN (" + where + ")" + fmt.Sprintf(" ORDER BY e.event_date ASC LIMIT $%d", len(args))
	rws, err := h.pool.Query(r.Context(), q, args...)
	if err != nil {
		writeServer(w, r, err)
		return
	}
	defer rws.Close()
	items := []EventSummary{}
	for rws.Next() {
		e, err := scanEventRow(rws)
		if err != nil {
			writeServer(w, r, err)
			return
		}
		item := e.summary()
		item.Saved = true
		items = append(items, item)
	}
	writeSummaryPage(w, limit, items, len(items) > limit)
}
