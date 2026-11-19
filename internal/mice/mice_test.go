package mice

import (
	"strconv"
	"strings"
	"testing"
	"time"
)

func newTestHandler(now time.Time) *Handler {
	return NewWithOptions(nil, Options{TicketSecret: "test-secret", Now: func() time.Time { return now }})
}

func TestTicketRoundTrip(t *testing.T) {
	now := time.Date(2026, 11, 19, 9, 0, 0, 0, time.UTC)
	h := newTestHandler(now)
	expiry := now.Add(24 * time.Hour)

	token := h.ticketPayload("event-1", "user-1", expiry)
	parts := strings.Split(token, ".")
	if len(parts) != 4 {
		t.Fatalf("ticket must have four dot-separated parts, got %q", token)
	}
	if parts[0] != "event-1" || parts[1] != "user-1" {
		t.Fatalf("payload = %q", token)
	}

	eventID, attendee, gotExpiry, err := h.verifyTicket(token)
	if err != nil {
		t.Fatalf("verify: %v", err)
	}
	if eventID != "event-1" || attendee != "user-1" || !gotExpiry.Equal(expiry) {
		t.Fatalf("verified %s %s %s", eventID, attendee, gotExpiry)
	}

	// Deterministic: re-issuing the same ticket yields the same token.
	if again := h.ticketPayload("event-1", "user-1", expiry); again != token {
		t.Fatal("ticket payload is not deterministic")
	}
}

func TestTicketRejectsTampering(t *testing.T) {
	now := time.Date(2026, 11, 19, 9, 0, 0, 0, time.UTC)
	h := newTestHandler(now)
	token := h.ticketPayload("event-1", "user-1", now.Add(time.Hour))

	parts := strings.Split(token, ".")
	tamperedExpiry := parts[0] + "." + parts[1] + "." + strconv.FormatInt(now.Add(48*time.Hour).Unix(), 10) + "." + parts[3]
	cases := map[string]string{
		"wrong signature": strings.TrimSuffix(token, token[len(token)-4:]) + "AAAA",
		"tampered expiry": tamperedExpiry,
		"garbage":         "not-a-ticket",
	}
	for name, candidate := range cases {
		t.Run(name, func(t *testing.T) {
			if _, _, _, err := h.verifyTicket(candidate); err == nil {
				t.Fatal("expected the ticket to be rejected")
			}
		})
	}
}

func TestTicketExpires(t *testing.T) {
	now := time.Date(2026, 11, 19, 9, 0, 0, 0, time.UTC)
	h := newTestHandler(now)
	token := h.ticketPayload("event-1", "user-1", now.Add(-time.Minute))
	if _, _, _, err := h.verifyTicket(token); err == nil {
		t.Fatal("expired ticket accepted")
	}
}

func TestQuietHours(t *testing.T) {
	// 22:30 EAT (19:30 UTC) is quiet.
	quiet := time.Date(2026, 11, 19, 19, 30, 0, 0, time.UTC)
	if !inQuietHours(quiet) {
		t.Fatal("22:30 EAT should be quiet")
	}
	end := maybeQuiet(quiet)
	local := end.In(nairobi)
	if local.Hour() != 6 || local.Minute() != 0 {
		t.Fatalf("quiet hours should defer to 06:00 EAT, got %s", local)
	}
	// 09:00 EAT is not quiet and is unchanged.
	day := time.Date(2026, 11, 19, 6, 0, 0, 0, time.UTC)
	if inQuietHours(day) {
		t.Fatal("09:00 EAT should not be quiet")
	}
	if !maybeQuiet(day).Equal(day) {
		t.Fatal("daytime delivery must not shift")
	}
}

func TestEventWindowNairobi(t *testing.T) {
	date := time.Date(2026, 11, 21, 0, 0, 0, 0, time.UTC)
	start := eventStart(date, "09:00", "Africa/Nairobi")
	if start.Format(time.RFC3339) != "2026-11-21T06:00:00Z" {
		t.Fatalf("start = %s, want 06:00Z (09:00 EAT)", start.Format(time.RFC3339))
	}
	end := eventEnd(date, nil, "17:00", "Africa/Nairobi")
	if end.Format(time.RFC3339) != "2026-11-21T14:00:00Z" {
		t.Fatalf("end = %s, want 14:00Z (17:00 EAT)", end.Format(time.RFC3339))
	}
}

func TestCheckInWindow(t *testing.T) {
	start := time.Date(2026, 11, 21, 6, 0, 0, 0, time.UTC)
	end := start.Add(8 * time.Hour)
	if open, _ := checkInWindow(start.Add(-3*time.Hour), start, end); open {
		t.Fatal("check-in opened more than two hours early")
	}
	if open, _ := checkInWindow(start.Add(-time.Hour), start, end); !open {
		t.Fatal("check-in should be open one hour before start")
	}
	if open, _ := checkInWindow(end.Add(time.Minute), start, end); open {
		t.Fatal("check-in should be closed after the event ends")
	}
}

func TestTrustLabels(t *testing.T) {
	cases := []struct {
		tier, status, want string
	}{
		{"programme_partner", "verified", "YTCI programme partner"},
		{"verified_organizer", "verified", "Verified organizer"},
		{"community_organizer", "verified", "Community organizer"},
		{"verified_organizer", "unverified", ""},
		{"", "unverified", ""},
	}
	for _, c := range cases {
		if got := trustLabel(c.tier, c.status); got != c.want {
			t.Errorf("trustLabel(%q,%q) = %q, want %q", c.tier, c.status, got, c.want)
		}
	}
}

func TestICSExport(t *testing.T) {
	start := time.Date(2026, 11, 21, 6, 0, 0, 0, time.UTC)
	ics := buildICS(icsEvent{
		UID: "event-1@ytci", Summary: "Nairobi; Conference",
		Location: "KICC, Nairobi", Start: start, End: start.Add(2 * time.Hour), AlarmMin: 60,
	})
	for _, want := range []string{
		"BEGIN:VCALENDAR", "BEGIN:VEVENT", "DTSTART:20261121T060000Z",
		"DTEND:20261121T080000Z", "BEGIN:VALARM", "TRIGGER:-PT60M", "END:VCALENDAR",
	} {
		if !strings.Contains(ics, want) {
			t.Errorf("ICS missing %q:\n%s", want, ics)
		}
	}
	if !strings.Contains(ics, "SUMMARY:Nairobi\\; Conference") {
		t.Errorf("ICS did not escape the summary:\n%s", ics)
	}
	if !strings.HasSuffix(ics, "END:VCALENDAR\r\n") {
		t.Error("ICS must use CRLF line endings")
	}
}

func TestSlugify(t *testing.T) {
	cases := map[string]string{
		"Nairobi National Museum": "nairobi-national-museum",
		"KICC / Kenyatta Centre":  "kicc-kenyatta-centre",
		"  Sarova  Panafric  ":    "sarova-panafric",
	}
	for in, want := range cases {
		if got := slugify(in); got != want {
			t.Errorf("slugify(%q) = %q, want %q", in, got, want)
		}
	}
}
