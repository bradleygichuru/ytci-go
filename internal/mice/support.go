package mice

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	authmw "github.com/bradleygichuru/ytci-go/internal/middleware"
	"github.com/bradleygichuru/ytci-go/internal/pagination"
)

var (
	eventClasses      = []string{"meeting", "incentive", "conference", "exhibition", "community"}
	eventFormats      = []string{"in_person", "online", "hybrid"}
	eventAccess       = []string{"public", "registered", "invite_only"}
	registrationModes = []string{"rsvp", "ticket_handoff", "none"}
	legacyThemes      = []string{"cultural", "sports", "conservation", "tourism"}
	reportReasons     = []string{
		"impersonation", "safety_concern", "inaccurate_information",
		"spam", "payment_issue", "accessibility_misrepresentation",
	}
	materialFields  = []string{"date_time", "venue", "status", "capacity", "session"}
	reminderOffsets = []int{60, 1440, 10080}
)

func paginationEncode(sortValue, id string) string { return pagination.EncodeCursor(sortValue, id) }

func paginationDecode(raw string) (string, string, bool) {
	c, ok := pagination.DecodeCursor(raw)
	if !ok {
		return "", "", false
	}
	return c.SortValue, c.ID, true
}

func jsonUnmarshal(raw []byte, dst any) error { return json.Unmarshal(raw, dst) }

// ---------------------------------------------------------------------------
// Accessibility
// ---------------------------------------------------------------------------

const accessibilityColumns = `step_free_entrance, step_free_route, accessible_toilet,
	accessible_parking, accessible_seating, captioning, sign_language, hearing_loop,
	quiet_room, assistance_dogs, COALESCE(companion_ticket_policy,''), COALESCE(content_notes,''),
	COALESCE(reported_by,''), verified_at`

func scanAccessibility(row pgx.Row) (*AccessibilityBlock, error) {
	var b AccessibilityBlock
	f := &b.Fields
	var verified pgtype.Timestamptz
	if err := row.Scan(
		&f.StepFreeEntrance, &f.StepFreeRoute, &f.AccessibleToilet,
		&f.AccessibleParking, &f.AccessibleSeating, &f.Captioning, &f.SignLanguage, &f.HearingLoop,
		&f.QuietRoom, &f.AssistanceDogs, &f.CompanionTicketPolicy, &f.ContentNotes,
		&b.ReportedBy, &verified,
	); err != nil {
		return nil, err
	}
	b.VerifiedAt = nullTime(verified)
	b.Label = accessibilityLabel(b.VerifiedAt)
	return &b, nil
}

// loadAccessibility loads the tri-state report for an event or venue. A nil
// block means "no report"; `unknown` is never inferred from a category.
func (h *Handler) loadAccessibility(ctx context.Context, kind, id string) (*AccessibilityBlock, error) {
	var q string
	switch kind {
	case "event":
		q = `SELECT ` + accessibilityColumns + ` FROM event_accessibility WHERE event_id = $1`
	case "venue":
		q = `SELECT ` + accessibilityColumns + ` FROM venue_accessibility WHERE venue_id = $1`
	default:
		return nil, fmt.Errorf("unknown accessibility kind %q", kind)
	}
	block, err := scanAccessibility(h.pool.QueryRow(ctx, q, id))
	if err != nil {
		if isNoRows(err) {
			return nil, nil
		}
		return nil, err
	}
	return block, nil
}

// upsertAccessibility writes only the fields supplied; omitted fields keep
// their previous value (or 'unknown' on first write).
func (h *Handler) upsertAccessibility(ctx context.Context, kind, id string, w *accessibilityWrite, actor string) error {
	if w == nil {
		return nil
	}
	value := func(v string) string {
		switch v {
		case "yes", "no", "unknown":
			return v
		default:
			return "unknown"
		}
	}
	table, key := "event_accessibility", "event_id"
	if kind == "venue" {
		table, key = "venue_accessibility", "venue_id"
	}
	_, err := h.pool.Exec(ctx, fmt.Sprintf(`
		INSERT INTO %s (%s, step_free_entrance, step_free_route, accessible_toilet,
			accessible_parking, accessible_seating, captioning, sign_language, hearing_loop,
			quiet_room, assistance_dogs, companion_ticket_policy, content_notes, reported_by, verified_at)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,NULLIF($12,''),NULLIF($13,''),$14,now())
		ON CONFLICT (%s) DO UPDATE SET
			step_free_entrance=EXCLUDED.step_free_entrance,
			step_free_route=EXCLUDED.step_free_route,
			accessible_toilet=EXCLUDED.accessible_toilet,
			accessible_parking=EXCLUDED.accessible_parking,
			accessible_seating=EXCLUDED.accessible_seating,
			captioning=EXCLUDED.captioning,
			sign_language=EXCLUDED.sign_language,
			hearing_loop=EXCLUDED.hearing_loop,
			quiet_room=EXCLUDED.quiet_room,
			assistance_dogs=EXCLUDED.assistance_dogs,
			companion_ticket_policy=EXCLUDED.companion_ticket_policy,
			content_notes=EXCLUDED.content_notes,
			reported_by=EXCLUDED.reported_by,
			verified_at=now(), updated_at=now()`, table, key, key),
		id, value(w.StepFreeEntrance), value(w.StepFreeRoute), value(w.AccessibleToilet),
		value(w.AccessibleParking), value(w.AccessibleSeating), value(w.Captioning),
		value(w.SignLanguage), value(w.HearingLoop), value(w.QuietRoom), value(w.AssistanceDogs),
		w.CompanionTicketPolicy, w.ContentNotes, actor,
	)
	return err
}

// ---------------------------------------------------------------------------
// ICS export (RFC 5545 VEVENT + VALARM, ADR 0011 §8)
// ---------------------------------------------------------------------------

type icsEvent struct {
	UID         string
	Summary     string
	Description string
	Location    string
	Start       time.Time
	End         time.Time
	AlarmMin    int
}

func buildICS(e icsEvent) string {
	var b strings.Builder
	write := func(format string, args ...any) {
		b.WriteString(fmt.Sprintf(format, args...))
		b.WriteString("\r\n")
	}
	write("BEGIN:VCALENDAR")
	write("VERSION:2.0")
	write("PRODID:-//YTCI//Events//EN")
	write("CALSCALE:GREGORIAN")
	write("METHOD:PUBLISH")
	write("BEGIN:VEVENT")
	write("UID:%s", icsEscape(e.UID))
	write("DTSTAMP:%s", e.Start.UTC().Format("20060102T150405Z"))
	write("DTSTART:%s", e.Start.UTC().Format("20060102T150405Z"))
	write("DTEND:%s", e.End.UTC().Format("20060102T150405Z"))
	write("SUMMARY:%s", icsEscape(e.Summary))
	if e.Description != "" {
		write("DESCRIPTION:%s", icsEscape(e.Description))
	}
	if e.Location != "" {
		write("LOCATION:%s", icsEscape(e.Location))
	}
	write("BEGIN:VALARM")
	write("ACTION:DISPLAY")
	write("TRIGGER:-PT%dM", e.AlarmMin)
	write("DESCRIPTION:%s", icsEscape(e.Summary))
	write("END:VALARM")
	write("END:VEVENT")
	write("END:VCALENDAR")
	return b.String()
}

func icsEscape(s string) string {
	r := strings.NewReplacer("\\", "\\\\", ";", "\\;", ",", "\\,", "\n", "\\n", "\r", "")
	return r.Replace(s)
}

func itoa(n int) string { return strconv.Itoa(n) }

func authmwIsAdmin(r *http.Request) bool {
	return authmw.IsAdmin(authmw.RoleFromCtx(r.Context()))
}
