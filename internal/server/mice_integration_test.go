package server_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/getkin/kin-openapi/openapi3"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bradleygichuru/ytci-go/api"
	"github.com/bradleygichuru/ytci-go/internal/config"
	"github.com/bradleygichuru/ytci-go/internal/db"
	"github.com/bradleygichuru/ytci-go/internal/server"
)

// ---------------------------------------------------------------------------
// Harness
// ---------------------------------------------------------------------------

type miceFixture struct {
	ts   *httptest.Server
	pool *pgxpool.Pool
	doc  *openapi3.T

	admin   string
	admin2  string
	super   string
	reader  string
	reader2 string
	reader3 string
	scanner string

	organizerID string
	venueID     string
	futureID    string
	liveID      string
}

func createSessionRole(t *testing.T, ctx context.Context, pool *pgxpool.Pool, userID, email, token, role string) {
	t.Helper()
	_, err := pool.Exec(ctx, `
		INSERT INTO users (id, name, email, created_at, updated_at, role)
		VALUES ($1, $2, $3, now(), now(), $4)`, userID, "Test "+role, email, role)
	require.NoError(t, err)
	_, err = pool.Exec(ctx, `
		INSERT INTO sessions (id, expires_at, token, created_at, updated_at, user_id)
		VALUES ($1, now()+interval '1 day', $2, now(), now(), $3)`, "sess-"+token, token, userID)
	require.NoError(t, err)
}

func loadOpenAPI(t *testing.T) *openapi3.T {
	t.Helper()
	raw, err := api.FS.ReadFile(api.OpenAPIPath)
	require.NoError(t, err)
	loader := openapi3.NewLoader()
	doc, err := loader.LoadFromData(raw)
	require.NoError(t, err)
	require.NoError(t, doc.Validate(context.Background()))
	return doc
}

func (f *miceFixture) do(t *testing.T, method, path string, body any, token string) (*http.Response, []byte) {
	t.Helper()
	var rdr io.Reader
	if body != nil {
		raw, err := json.Marshal(body)
		require.NoError(t, err)
		rdr = bytes.NewReader(raw)
	}
	req, err := http.NewRequest(method, f.ts.URL+path, rdr)
	require.NoError(t, err)
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := f.ts.Client().Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()
	raw, err := io.ReadAll(resp.Body)
	require.NoError(t, err)
	return resp, raw
}

// contract asserts a JSON response matches the frozen OpenAPI schema.
func (f *miceFixture) contract(t *testing.T, method, pattern string, status int, raw []byte) {
	t.Helper()
	item := f.doc.Paths.Find(pattern)
	require.NotNilf(t, item, "missing path %s", pattern)
	var op *openapi3.Operation
	switch method {
	case "GET":
		op = item.Get
	case "POST":
		op = item.Post
	case "PUT":
		op = item.Put
	case "PATCH":
		op = item.Patch
	case "DELETE":
		op = item.Delete
	}
	require.NotNilf(t, op, "missing operation %s %s", method, pattern)
	resp := op.Responses.Value(strconv.Itoa(status))
	if resp == nil {
		for code, candidate := range op.Responses.Map() {
			if len(code) == 3 && code[0] == '2' {
				resp = candidate
				break
			}
		}
	}
	require.NotNilf(t, resp, "no response for %s %s %d", method, pattern, status)
	media := resp.Value.Content.Get("application/json")
	require.NotNilf(t, media, "no JSON schema for %s %s", method, pattern)

	var decoded any
	require.NoError(t, json.Unmarshal(raw, &decoded))
	require.NoErrorf(t, media.Schema.Value.VisitJSON(decoded, openapi3.MultiErrors()),
		"contract mismatch for %s %s:\n%s", method, pattern, raw)
}

func decodeMap(t *testing.T, raw []byte) map[string]any {
	t.Helper()
	var m map[string]any
	require.NoError(t, json.Unmarshal(raw, &m))
	return m
}

func setupMiceFixture(t *testing.T) *miceFixture {
	t.Helper()
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}
	ctx := context.Background()
	pool := db.SetupTestDB(t)
	t.Cleanup(pool.Close)
	setupAuthTables(t, ctx, pool)

	createSessionRole(t, ctx, pool, "admin-1", "admin1@test.local", "token-admin-1", "administrator")
	createSessionRole(t, ctx, pool, "admin-2", "admin2@test.local", "token-admin-2", "administrator")
	createSessionRole(t, ctx, pool, "super-1", "super1@test.local", "token-super-1", "super_admin")
	createSessionRole(t, ctx, pool, "reader-1", "reader1@test.local", "token-reader-1", "user")
	createSessionRole(t, ctx, pool, "reader-2", "reader2@test.local", "token-reader-2", "user")
	createSessionRole(t, ctx, pool, "reader-3", "reader3@test.local", "token-reader-3", "user")
	createSessionRole(t, ctx, pool, "scanner-1", "scanner1@test.local", "token-scanner-1", "organizer")
	_, err := pool.Exec(ctx, `INSERT INTO kenya_counties (gid, adm1_name) VALUES (47, 'Nairobi') ON CONFLICT DO NOTHING`)
	require.NoError(t, err)

	cfg := &config.Config{
		Port: "8080", DatabaseURL: "ignored", AdminJWKSURL: "http://test.invalid/jwks",
		CORSOrigins: "*", MiceTicketSecret: "integration-test-secret",
		MiceNotificationsEnabled: true,
	}
	ts := httptest.NewServer(server.New(cfg, pool))
	t.Cleanup(ts.Close)

	f := &miceFixture{
		ts: ts, pool: pool, doc: loadOpenAPI(t),
		admin: "token-admin-1", admin2: "token-admin-2", super: "token-super-1",
		reader: "token-reader-1", reader2: "token-reader-2", reader3: "token-reader-3",
		scanner: "token-scanner-1",
	}

	// Organizer: admin-1 requests, admin-2 verifies (reviewer != requester).
	resp, raw := f.do(t, "POST", "/v1/admin/organizers", map[string]any{
		"displayName": "Nairobi Events Trust",
		"trustTier":   "verified_organizer",
		"about":       "Conference and exhibition organiser",
	}, f.admin)
	require.Equal(t, http.StatusCreated, resp.StatusCode, string(raw))
	f.contract(t, "POST", "/v1/admin/organizers", resp.StatusCode, raw)
	f.organizerID = decodeMap(t, raw)["id"].(string)

	resp, raw = f.do(t, "POST", "/v1/admin/organizers/"+f.organizerID+"/verification", map[string]any{
		"action": "verify", "trustTier": "verified_organizer",
		"reason": "Registration documents checked and verified",
	}, f.admin2)
	require.Equal(t, http.StatusOK, resp.StatusCode, string(raw))
	f.contract(t, "POST", "/v1/admin/organizers/{id}/verification", resp.StatusCode, raw)
	require.Equal(t, "verified", decodeMap(t, raw)["verificationStatus"])

	resp, raw = f.do(t, "POST", "/v1/admin/venues", map[string]any{
		"name": "KICC", "county": "Nairobi", "countyGid": 47,
		"addressLine1": "Harambee Avenue", "capacityInPerson": 500,
		"accessibility": map[string]any{
			"stepFreeEntrance": "yes", "stepFreeRoute": "yes", "accessibleToilet": "yes",
		},
	}, f.admin)
	require.Equal(t, http.StatusCreated, resp.StatusCode, string(raw))
	f.contract(t, "POST", "/v1/admin/venues", resp.StatusCode, raw)
	f.venueID = decodeMap(t, raw)["id"].(string)

	tomorrow := time.Now().In(time.FixedZone("EAT", 3*3600)).AddDate(0, 0, 1).Format("2006-01-02")
	f.futureID = f.createEvent(t, tomorrow, "09:00", "17:00", 1)
	f.liveID = f.createEvent(t, time.Now().In(time.FixedZone("EAT", 3*3600)).Format("2006-01-02"), "00:00", "23:59", 10)

	// scanner-1 is assigned to the organizer.
	_, err = pool.Exec(ctx, `
		INSERT INTO organizer_staff (organizer_id, user_id, staff_role, assigned_by)
		VALUES ($1, 'scanner-1', 'scanner', 'admin-1')`, f.organizerID)
	require.NoError(t, err)

	return f
}

// insertRegistration seeds a confirmed registration for an event that has
// already started (the public API only accepts registrations before start).
func (f *miceFixture) insertRegistration(t *testing.T, eventID, userID string) string {
	t.Helper()
	var id string
	err := f.pool.QueryRow(context.Background(), `
		INSERT INTO event_registrations (event_id, user_id, status)
		VALUES ($1,$2,'confirmed')
		RETURNING id::text`, eventID, userID).Scan(&id)
	require.NoError(t, err)
	return id
}

func (f *miceFixture) createEvent(t *testing.T, date, start, end string, capacity int) string {
	t.Helper()
	resp, raw := f.do(t, "POST", "/v1/admin/events", map[string]any{
		"title": "Nairobi Conservation Conference", "organizerProfileId": f.organizerID,
		"venueId": f.venueID, "eventClass": "conference", "format": "in_person",
		"access": "public", "type": "conference", "eventDate": date,
		"startTime": start, "endTime": end, "timezone": "Africa/Nairobi",
		"description": "A day of conservation practice.", "capacity": capacity,
		"waitlistCapacity": 5, "registrationMode": "rsvp",
		"accessibility": map[string]any{"stepFreeEntrance": "yes"},
	}, f.admin)
	require.Equal(t, http.StatusCreated, resp.StatusCode, string(raw))
	f.contract(t, "POST", "/v1/admin/events", resp.StatusCode, raw)
	id := decodeMap(t, raw)["id"].(string)

	resp, raw = f.do(t, "POST", "/v1/admin/events/"+id+"/publish", nil, f.admin)
	require.Equal(t, http.StatusOK, resp.StatusCode, string(raw))
	f.contract(t, "POST", "/v1/admin/events/{id}/publish", resp.StatusCode, raw)
	return id
}

// ---------------------------------------------------------------------------
// Public discovery and authoring
// ---------------------------------------------------------------------------

func TestMicePublicDiscovery(t *testing.T) {
	f := setupMiceFixture(t)

	resp, raw := f.do(t, "GET", "/v1/public/events?eventClass=conference&format=in_person", nil, "")
	require.Equal(t, http.StatusOK, resp.StatusCode, string(raw))
	f.contract(t, "GET", "/v1/public/events", resp.StatusCode, raw)
	page := decodeMap(t, raw)
	assert.Equal(t, true, page["hasMore"] == false)
	assert.GreaterOrEqual(t, len(page["items"].([]any)), 2)

	resp, raw = f.do(t, "GET", "/v1/public/events?eventClass=meeting", nil, "")
	require.Equal(t, http.StatusOK, resp.StatusCode)
	assert.Empty(t, decodeMap(t, raw)["items"])

	resp, raw = f.do(t, "GET", "/v1/public/events/"+f.futureID, nil, "")
	require.Equal(t, http.StatusOK, resp.StatusCode, string(raw))
	f.contract(t, "GET", "/v1/public/events/{id}", resp.StatusCode, raw)
	detail := decodeMap(t, raw)
	assert.Equal(t, "Nairobi Conservation Conference", detail["title"])
	assert.Equal(t, "published", detail["status"])
	assert.Equal(t, "reported", detail["accessibility"].(map[string]any)["state"])
	assert.Equal(t, 1, int(detail["capacity"].(float64)))

	resp, raw = f.do(t, "GET", "/v1/public/events/00000000-0000-0000-0000-000000000000", nil, "")
	assert.Equal(t, http.StatusNotFound, resp.StatusCode)
	f.contract(t, "GET", "/v1/public/events/{id}", resp.StatusCode, raw)

	resp, raw = f.do(t, "GET", "/v1/public/events/"+f.futureID+"/calendar.ics?alarm_minutes=60", nil, "")
	require.Equal(t, http.StatusOK, resp.StatusCode)
	assert.Contains(t, resp.Header.Get("Content-Type"), "text/calendar")
	assert.Contains(t, string(raw), "BEGIN:VEVENT")
	assert.Contains(t, string(raw), "TRIGGER:-PT60M")

	resp, raw = f.do(t, "GET", "/v1/public/venues/kicc", nil, "")
	require.Equal(t, http.StatusOK, resp.StatusCode, string(raw))
	f.contract(t, "GET", "/v1/public/venues/{slug}", resp.StatusCode, raw)
	venue := decodeMap(t, raw)
	assert.Equal(t, "KICC", venue["name"])
	fields := venue["accessibility"].(map[string]any)["fields"].(map[string]any)
	assert.Equal(t, "yes", fields["stepFreeEntrance"])
	assert.Equal(t, "unknown", fields["captioning"])
	assert.Equal(t, 47, int(venue["countyGid"].(float64)))

	resp, raw = f.do(t, "GET", "/v1/public/organizers/nairobi-events-trust", nil, "")
	require.Equal(t, http.StatusOK, resp.StatusCode, string(raw))
	f.contract(t, "GET", "/v1/public/organizers/{slug}", resp.StatusCode, raw)
	org := decodeMap(t, raw)
	assert.Equal(t, "Verified organizer", org["label"])
	assert.Equal(t, "verified", org["verificationStatus"])
}

func TestMiceAgendaAndSpeakers(t *testing.T) {
	f := setupMiceFixture(t)
	day := time.Now().In(time.FixedZone("EAT", 3*3600)).AddDate(0, 0, 1).Format("2006-01-02")

	resp, raw := f.do(t, "POST", "/v1/admin/events/"+f.futureID+"/people", map[string]any{
		"displayName": "Dr Wanjiku Kamau", "title": "Conservation lead",
	}, f.admin)
	require.Equal(t, http.StatusCreated, resp.StatusCode, string(raw))
	f.contract(t, "POST", "/v1/admin/events/{id}/people", resp.StatusCode, raw)
	personID := decodeMap(t, raw)["id"].(string)

	resp, raw = f.do(t, "POST", "/v1/admin/events/"+f.futureID+"/sessions", map[string]any{
		"day": day, "startsAt": day + "T10:00:00+03:00", "endsAt": day + "T11:00:00+03:00",
		"title": "Opening keynote", "room": "Tsavo Hall",
	}, f.admin)
	require.Equal(t, http.StatusCreated, resp.StatusCode, string(raw))
	f.contract(t, "POST", "/v1/admin/events/{id}/sessions", resp.StatusCode, raw)
	sessionID := decodeMap(t, raw)["id"].(string)

	resp, raw = f.do(t, "POST", "/v1/admin/sessions/"+sessionID+"/speakers", map[string]any{
		"personId": personID, "role": "speaker",
	}, f.admin)
	require.Equal(t, http.StatusOK, resp.StatusCode, string(raw))
	f.contract(t, "POST", "/v1/admin/sessions/{sessionId}/speakers", resp.StatusCode, raw)

	resp, raw = f.do(t, "GET", "/v1/public/events/"+f.futureID, nil, "")
	require.Equal(t, http.StatusOK, resp.StatusCode)
	detail := decodeMap(t, raw)
	agenda := detail["agenda"].([]any)
	require.Len(t, agenda, 1)
	speakers := agenda[0].(map[string]any)["speakers"].([]any)
	require.Len(t, speakers, 1)
	assert.Equal(t, "Dr Wanjiku Kamau", speakers[0].(map[string]any)["displayName"])
	assert.Equal(t, 1, int(detail["speakerCount"].(float64)))
}

// ---------------------------------------------------------------------------
// Registration, capacity, waitlist and cancellation
// ---------------------------------------------------------------------------

func TestMiceRegistrationCapacityAndWaitlist(t *testing.T) {
	f := setupMiceFixture(t)
	path := "/v1/mobile/events/" + f.futureID

	// First reader takes the only seat.
	resp, raw := f.do(t, "POST", path+"/registrations", map[string]any{
		"reminderOffsetMinutes": 1440,
	}, f.reader)
	require.Equal(t, http.StatusCreated, resp.StatusCode, string(raw))
	f.contract(t, "POST", "/v1/mobile/events/{id}/registrations", resp.StatusCode, raw)
	reg := decodeMap(t, raw)
	assert.Equal(t, "confirmed", reg["status"])
	assert.NotNil(t, reg["ticket"])
	ticketPayload := reg["ticket"].(map[string]any)["qrPayload"].(string)
	assert.Len(t, strings.Split(ticketPayload, "."), 4)
	assert.NotContains(t, ticketPayload, reg["id"].(string), "QR payload must not embed the registration id")

	// Replaying the registration is idempotent, not a duplicate.
	resp, raw = f.do(t, "POST", path+"/registrations", map[string]any{}, f.reader)
	require.Equal(t, http.StatusCreated, resp.StatusCode, string(raw))
	assert.Equal(t, reg["id"], decodeMap(t, raw)["id"])

	// Second and third readers join the FIFO waitlist.
	resp, raw = f.do(t, "POST", path+"/registrations", map[string]any{}, f.reader2)
	require.Equal(t, http.StatusCreated, resp.StatusCode, string(raw))
	second := decodeMap(t, raw)
	assert.Equal(t, "waitlisted", second["status"])
	assert.Equal(t, 1, int(second["waitlistPosition"].(float64)))

	resp, raw = f.do(t, "POST", path+"/registrations", map[string]any{}, f.reader3)
	require.Equal(t, http.StatusCreated, resp.StatusCode, string(raw))
	third := decodeMap(t, raw)
	assert.Equal(t, "waitlisted", third["status"])
	assert.Equal(t, 2, int(third["waitlistPosition"].(float64)))

	// A full event refuses new registrations when the waitlist is refused.
	resp, raw = f.do(t, "POST", path+"/registrations", map[string]any{"acceptWaitlist": false}, "token-admin-1")
	require.Equal(t, http.StatusConflict, resp.StatusCode)
	f.contract(t, "POST", "/v1/mobile/events/{id}/registrations", resp.StatusCode, raw)
	assert.Equal(t, "EVENT_CAPACITY_FULL", decodeMap(t, raw)["error"].(map[string]any)["code"])

	// Cancelling the confirmed seat promotes the FIFO head with a window.
	resp, raw = f.do(t, "DELETE", path+"/registration", nil, f.reader)
	require.Equal(t, http.StatusOK, resp.StatusCode, string(raw))
	f.contract(t, "DELETE", "/v1/mobile/events/{id}/registration", resp.StatusCode, raw)
	assert.Equal(t, "cancelled", decodeMap(t, raw)["status"])

	resp, raw = f.do(t, "GET", path+"/registration", nil, f.reader2)
	require.Equal(t, http.StatusOK, resp.StatusCode, string(raw))
	promoted := decodeMap(t, raw)
	assert.Equal(t, "confirmed", promoted["status"])
	assert.NotNil(t, promoted["confirmBy"])

	// The promotion created a Program-channel notification.
	var kind string
	require.NoError(t, f.pool.QueryRow(context.Background(), `
		SELECT kind FROM event_notifications
		 WHERE event_id=$1 AND user_id='reader-2' AND kind='waitlist_promotion'`, f.futureID).Scan(&kind))
	assert.Equal(t, "waitlist_promotion", kind)

	// An unconfirmed promotion expires and moves the next person up.
	_, err := f.pool.Exec(context.Background(), `
		UPDATE event_registrations SET confirm_by = now() - interval '1 hour'
		 WHERE event_id=$1 AND user_id='reader-2'`, f.futureID)
	require.NoError(t, err)

	resp, raw = f.do(t, "GET", path+"/registration", nil, f.reader2)
	require.Equal(t, http.StatusOK, resp.StatusCode, string(raw))
	assert.Equal(t, "cancelled", decodeMap(t, raw)["status"])

	resp, raw = f.do(t, "GET", path+"/registration", nil, f.reader3)
	require.Equal(t, http.StatusOK, resp.StatusCode, string(raw))
	assert.Equal(t, "confirmed", decodeMap(t, raw)["status"])

	// The reminder opt-in is durable and capped at one per event.
	var reminders int
	require.NoError(t, f.pool.QueryRow(context.Background(), `
		SELECT count(*) FROM event_notifications
		 WHERE event_id=$1 AND user_id='reader-1' AND kind='reminder'`, f.futureID).Scan(&reminders))
	assert.Equal(t, 1, reminders)

	// The wallet lists the cancelled registration only for `all`.
	resp, raw = f.do(t, "GET", "/v1/mobile/registrations?state=all", nil, f.reader)
	require.Equal(t, http.StatusOK, resp.StatusCode, string(raw))
	f.contract(t, "GET", "/v1/mobile/registrations", resp.StatusCode, raw)
	assert.GreaterOrEqual(t, len(decodeMap(t, raw)["items"].([]any)), 1)
}

func TestMiceCancellationClosesAtStart(t *testing.T) {
	f := setupMiceFixture(t)
	f.insertRegistration(t, f.liveID, "reader-1")

	resp, raw := f.do(t, "DELETE", "/v1/mobile/events/"+f.liveID+"/registration", nil, f.reader)
	require.Equal(t, http.StatusConflict, resp.StatusCode)
	f.contract(t, "DELETE", "/v1/mobile/events/{id}/registration", resp.StatusCode, raw)
	assert.Equal(t, "EVENT_STARTED", decodeMap(t, raw)["error"].(map[string]any)["code"])
}

// ---------------------------------------------------------------------------
// Tickets, check-in and offline sync
// ---------------------------------------------------------------------------

func TestMiceTicketAndCheckIn(t *testing.T) {
	f := setupMiceFixture(t)
	ctx := context.Background()

	// The live event is open for check-in and holds both readers.
	regID := f.insertRegistration(t, f.liveID, "reader-2")
	reg := map[string]any{"id": regID}

	resp, raw := f.do(t, "GET", "/v1/mobile/events/"+f.liveID+"/ticket", nil, f.reader2)
	require.Equal(t, http.StatusOK, resp.StatusCode, string(raw))
	f.contract(t, "GET", "/v1/mobile/events/{id}/ticket", resp.StatusCode, raw)
	ticket := decodeMap(t, raw)
	assert.Equal(t, "valid", ticket["state"])
	payload := ticket["qrPayload"].(string)

	// A guest cannot fetch a ticket.
	resp, _ = f.do(t, "GET", "/v1/mobile/events/"+f.liveID+"/ticket", nil, "")
	assert.Equal(t, http.StatusUnauthorized, resp.StatusCode)

	// A reader with no registration gets 404, not a ticket.
	resp, _ = f.do(t, "GET", "/v1/mobile/events/"+f.liveID+"/ticket", nil, f.reader3)
	assert.Equal(t, http.StatusNotFound, resp.StatusCode)

	// Non-staff cannot check anyone in.
	resp, raw = f.do(t, "POST", "/v1/mobile/events/"+f.liveID+"/checkins", map[string]any{
		"qrPayload": payload,
	}, f.reader3)
	assert.Equal(t, http.StatusForbidden, resp.StatusCode, string(raw))

	// Assigned scanner checks the attendee in.
	resp, raw = f.do(t, "POST", "/v1/mobile/events/"+f.liveID+"/checkins", map[string]any{
		"qrPayload": payload, "deviceId": "scanner-device-1",
	}, f.scanner)
	require.Equal(t, http.StatusCreated, resp.StatusCode, string(raw))
	f.contract(t, "POST", "/v1/mobile/events/{id}/checkins", resp.StatusCode, raw)
	checkIn := decodeMap(t, raw)
	assert.Equal(t, "qr", checkIn["method"])
	assert.Equal(t, reg["id"], checkIn["registrationId"])
	assert.Equal(t, "Test user", checkIn["attendeeName"])

	// A duplicate scan is rejected and returns the first check-in.
	resp, raw = f.do(t, "POST", "/v1/mobile/events/"+f.liveID+"/checkins", map[string]any{
		"qrPayload": payload,
	}, f.scanner)
	require.Equal(t, http.StatusConflict, resp.StatusCode)
	f.contract(t, "POST", "/v1/mobile/events/{id}/checkins", resp.StatusCode, raw)
	env := decodeMap(t, raw)["error"].(map[string]any)
	assert.Equal(t, "DUPLICATE_CHECKIN", env["code"])
	assert.NotNil(t, env["details"].(map[string]any)["checkIn"])

	// A tampered ticket is rejected.
	tampered := payload[:len(payload)-4] + "AAAA"
	resp, _ = f.do(t, "POST", "/v1/mobile/events/"+f.liveID+"/checkins", map[string]any{
		"qrPayload": tampered,
	}, f.scanner)
	assert.Equal(t, http.StatusConflict, resp.StatusCode)

	var checkIns int
	require.NoError(t, f.pool.QueryRow(ctx, `SELECT count(*) FROM event_checkins WHERE event_id=$1`, f.liveID).Scan(&checkIns))
	assert.Equal(t, 1, checkIns)

	// Manual check-in by registration id is allowed for staff.
	reg3 := map[string]any{"id": f.insertRegistration(t, f.liveID, "reader-3")}
	resp, raw = f.do(t, "POST", "/v1/mobile/events/"+f.liveID+"/checkins", map[string]any{
		"registrationId": reg3["id"], "method": "manual",
	}, f.scanner)
	require.Equal(t, http.StatusCreated, resp.StatusCode, string(raw))
	assert.Equal(t, "manual", decodeMap(t, raw)["method"])

	// Admin check-in listing includes the duplicates queue view.
	resp, raw = f.do(t, "GET", "/v1/admin/events/"+f.liveID+"/checkins", nil, f.admin)
	require.Equal(t, http.StatusOK, resp.StatusCode, string(raw))
	f.contract(t, "GET", "/v1/admin/events/{id}/checkins", resp.StatusCode, raw)
	assert.Equal(t, 2, len(decodeMap(t, raw)["items"].([]any)))
}

func TestMiceOfflineScanQueue(t *testing.T) {
	f := setupMiceFixture(t)
	ctx := context.Background()

	f.insertRegistration(t, f.liveID, "reader-2")
	resp, raw := f.do(t, "GET", "/v1/mobile/events/"+f.liveID+"/ticket", nil, f.reader2)
	require.Equal(t, http.StatusOK, resp.StatusCode)
	payload := decodeMap(t, raw)["qrPayload"].(string)

	now := time.Now().UTC()
	batch := map[string]any{
		"deviceId": "offline-device-9",
		"scans": []any{
			map[string]any{"clientScanId": "scan-1", "qrPayload": payload, "clientTs": now.Add(-5 * time.Minute).Format(time.RFC3339)},
			map[string]any{"clientScanId": "scan-2", "qrPayload": payload, "clientTs": now.Add(-4 * time.Minute).Format(time.RFC3339)},
			map[string]any{"clientScanId": "scan-3", "qrPayload": "garbage", "clientTs": now.Format(time.RFC3339)},
		},
	}
	resp, raw = f.do(t, "POST", "/v1/mobile/events/"+f.liveID+"/checkin-scans", batch, f.scanner)
	require.Equal(t, http.StatusOK, resp.StatusCode, string(raw))
	f.contract(t, "POST", "/v1/mobile/events/{id}/checkin-scans", resp.StatusCode, raw)
	result := decodeMap(t, raw)
	assert.Equal(t, 1, int(result["applied"].(float64)))
	assert.Equal(t, 1, int(result["duplicates"].(float64)))
	assert.Equal(t, 1, int(result["rejected"].(float64)))
	results := result["results"].([]any)
	assert.Equal(t, "applied", results[0].(map[string]any)["status"])
	assert.Equal(t, "duplicate", results[1].(map[string]any)["status"])
	assert.Equal(t, "rejected", results[2].(map[string]any)["status"])

	// Replaying the same device queue is idempotent: no second check-in.
	resp, raw = f.do(t, "POST", "/v1/mobile/events/"+f.liveID+"/checkin-scans", batch, f.scanner)
	require.Equal(t, http.StatusOK, resp.StatusCode, string(raw))
	replay := decodeMap(t, raw)
	assert.Equal(t, 0, int(replay["applied"].(float64)))
	assert.Equal(t, 2, int(replay["duplicates"].(float64)))

	var checkIns, scans int
	require.NoError(t, f.pool.QueryRow(ctx, `SELECT count(*) FROM event_checkins WHERE event_id=$1`, f.liveID).Scan(&checkIns))
	require.NoError(t, f.pool.QueryRow(ctx, `SELECT count(*) FROM event_checkin_scans WHERE event_id=$1`, f.liveID).Scan(&scans))
	assert.Equal(t, 1, checkIns)
	assert.Equal(t, 3, scans)

	// Non-staff cannot use the offline queue.
	resp, _ = f.do(t, "POST", "/v1/mobile/events/"+f.liveID+"/checkin-scans", batch, f.reader)
	assert.Equal(t, http.StatusForbidden, resp.StatusCode)
}

func TestMiceTicketOnlyForRegistered(t *testing.T) {
	f := setupMiceFixture(t)
	// Registration for the future event is confirmed but check-in is not open.
	resp, raw := f.do(t, "POST", "/v1/mobile/events/"+f.futureID+"/registrations", map[string]any{}, f.reader)
	require.Equal(t, http.StatusCreated, resp.StatusCode, string(raw))
	resp, raw = f.do(t, "GET", "/v1/mobile/events/"+f.futureID+"/ticket", nil, f.reader)
	require.Equal(t, http.StatusOK, resp.StatusCode, string(raw))
	resp, raw = f.do(t, "POST", "/v1/mobile/events/"+f.futureID+"/checkins", map[string]any{
		"qrPayload": decodeMap(t, raw)["qrPayload"],
	}, f.scanner)
	require.Equal(t, http.StatusConflict, resp.StatusCode)
	assert.Equal(t, "CHECKIN_NOT_OPEN", decodeMap(t, raw)["error"].(map[string]any)["code"])
}

// ---------------------------------------------------------------------------
// Reports and change notices
// ---------------------------------------------------------------------------

func TestMiceReports(t *testing.T) {
	f := setupMiceFixture(t)

	resp, raw := f.do(t, "POST", "/v1/mobile/events/"+f.futureID+"/reports", map[string]any{
		"reason": "inaccurate_information", "details": "The venue changed last week.",
	}, f.reader)
	require.Equal(t, http.StatusAccepted, resp.StatusCode, string(raw))
	f.contract(t, "POST", "/v1/mobile/events/{id}/reports", resp.StatusCode, raw)

	// The same reporter cannot open the same report twice.
	resp, _ = f.do(t, "POST", "/v1/mobile/events/"+f.futureID+"/reports", map[string]any{
		"reason": "inaccurate_information",
	}, f.reader)
	require.Equal(t, http.StatusAccepted, resp.StatusCode)

	var open int
	require.NoError(t, f.pool.QueryRow(context.Background(), `
		SELECT count(*) FROM event_reports WHERE event_id=$1 AND status='open'`, f.futureID).Scan(&open))
	assert.Equal(t, 1, open)

	// Reports never create an Alert.
	var alerts int
	require.NoError(t, f.pool.QueryRow(context.Background(), `SELECT count(*) FROM alerts`).Scan(&alerts))
	assert.Equal(t, 0, alerts)

	resp, raw = f.do(t, "GET", "/v1/admin/event-reports?status=open", nil, f.admin)
	require.Equal(t, http.StatusOK, resp.StatusCode, string(raw))
	f.contract(t, "GET", "/v1/admin/event-reports", resp.StatusCode, raw)
	items := decodeMap(t, raw)["items"].([]any)
	require.Len(t, items, 1)
	reportID := items[0].(map[string]any)["id"].(string)

	// Readers cannot decide reports.
	resp, _ = f.do(t, "POST", "/v1/admin/event-reports/"+reportID+"/decision", map[string]any{
		"decision": "resolve", "note": "checked with the organizer",
	}, f.reader)
	assert.Equal(t, http.StatusForbidden, resp.StatusCode)

	resp, raw = f.do(t, "POST", "/v1/admin/event-reports/"+reportID+"/decision", map[string]any{
		"decision": "resolve", "note": "checked with the organizer",
	}, f.admin)
	require.Equal(t, http.StatusOK, resp.StatusCode, string(raw))
	f.contract(t, "POST", "/v1/admin/event-reports/{id}/decision", resp.StatusCode, raw)
	assert.Equal(t, "resolved", decodeMap(t, raw)["status"])

	// Reviewer is not the reporter.
	resp, raw = f.do(t, "POST", "/v1/mobile/events/"+f.futureID+"/reports", map[string]any{
		"reason": "spam",
	}, f.admin)
	require.Equal(t, http.StatusAccepted, resp.StatusCode, string(raw))
	var selfReportID string
	require.NoError(t, f.pool.QueryRow(context.Background(), `
		SELECT id::text FROM event_reports WHERE reporter_id='admin-1' AND status='open'`).Scan(&selfReportID))
	resp, raw = f.do(t, "POST", "/v1/admin/event-reports/"+selfReportID+"/decision", map[string]any{
		"decision": "dismiss", "note": "self-reviewed report",
	}, f.admin)
	require.Equal(t, http.StatusConflict, resp.StatusCode)
	assert.Equal(t, "REVIEWER_IS_REPORTER", decodeMap(t, raw)["error"].(map[string]any)["code"])
}

func TestMiceChangeNotice(t *testing.T) {
	f := setupMiceFixture(t)
	ctx := context.Background()

	resp, raw := f.do(t, "POST", "/v1/mobile/events/"+f.futureID+"/registrations", map[string]any{}, f.reader)
	require.Equal(t, http.StatusCreated, resp.StatusCode, string(raw))

	body := map[string]any{
		"materialFields": []string{"date_time"},
		"copyEn":         "The conference now starts at 10:00.",
		"copySw":         "Mkutano sasa unaanza saa nne asubuhi.",
	}
	resp, raw = f.do(t, "POST", "/v1/admin/events/"+f.futureID+"/change-notices", body, f.admin)
	require.Equal(t, http.StatusCreated, resp.StatusCode, string(raw))
	f.contract(t, "POST", "/v1/admin/events/{id}/change-notices", resp.StatusCode, raw)
	draft := decodeMap(t, raw)
	assert.Equal(t, "draft", draft["status"])
	assert.Equal(t, 1, len(draft["materialFields"].([]any)))
	assert.Equal(t, 1, int(draft["recipientCount"].(float64)))

	// Drafting again without a material edit is refused.
	resp, raw = f.do(t, "POST", "/v1/admin/events/"+f.futureID+"/change-notices", body, f.admin)
	require.Equal(t, http.StatusConflict, resp.StatusCode)
	assert.Equal(t, "NO_MATERIAL_CHANGE", decodeMap(t, raw)["error"].(map[string]any)["code"])

	approve := map[string]any{
		"materialFields": []string{"date_time"},
		"copyEn":         "The conference now starts at 10:00.",
		"copySw":         "Mkutano sasa unaanza saa nne asubuhi.",
		"previewOnly":    false,
	}
	// The drafter cannot approve their own notice.
	resp, raw = f.do(t, "POST", "/v1/admin/events/"+f.futureID+"/change-notices", approve, f.admin)
	require.Equal(t, http.StatusConflict, resp.StatusCode)
	assert.Equal(t, "REVIEWER_IS_UPLOADER", decodeMap(t, raw)["error"].(map[string]any)["code"])

	// A different administrator approves and fans out to registrants.
	resp, raw = f.do(t, "POST", "/v1/admin/events/"+f.futureID+"/change-notices", approve, f.admin2)
	require.Equal(t, http.StatusCreated, resp.StatusCode, string(raw))
	assert.Equal(t, "sent", decodeMap(t, raw)["status"])

	var notices int
	require.NoError(t, f.pool.QueryRow(ctx, `
		SELECT count(*) FROM event_notifications
		 WHERE event_id=$1 AND user_id='reader-1' AND kind='change_notice'`, f.futureID).Scan(&notices))
	assert.Equal(t, 1, notices)

	resp, raw = f.do(t, "GET", "/v1/mobile/events/"+f.futureID+"/notifications", nil, f.reader)
	require.Equal(t, http.StatusOK, resp.StatusCode, string(raw))
	f.contract(t, "GET", "/v1/mobile/events/{id}/notifications", resp.StatusCode, raw)
	page := decodeMap(t, raw)
	require.Len(t, page["items"].([]any), 1)
	notification := page["items"].([]any)[0].(map[string]any)
	assert.Equal(t, "change_notice", notification["kind"])
	assert.Equal(t, "queued", notification["status"])
	assert.NotEmpty(t, notification["deepLink"])
}

// ---------------------------------------------------------------------------
// Organizer verification boundaries
// ---------------------------------------------------------------------------

func TestMiceOrganizerVerificationBoundaries(t *testing.T) {
	f := setupMiceFixture(t)
	ctx := context.Background()

	// The requester cannot verify their own organizer.
	resp, raw := f.do(t, "POST", "/v1/admin/organizers/"+f.organizerID+"/verification", map[string]any{
		"action": "verify", "trustTier": "verified_organizer",
		"reason": "self verification attempt",
	}, f.admin)
	require.Equal(t, http.StatusConflict, resp.StatusCode)
	assert.Equal(t, "REVIEWER_IS_REQUESTER", decodeMap(t, raw)["error"].(map[string]any)["code"])

	// Revocation unpublishes upcoming events and notifies registrants.
	resp, raw = f.do(t, "POST", "/v1/mobile/events/"+f.futureID+"/registrations", map[string]any{}, f.reader)
	require.Equal(t, http.StatusCreated, resp.StatusCode, string(raw))

	resp, raw = f.do(t, "POST", "/v1/admin/organizers/"+f.organizerID+"/verification", map[string]any{
		"action": "revoke", "reason": "impersonation confirmed during review",
	}, f.super)
	require.Equal(t, http.StatusOK, resp.StatusCode, string(raw))
	assert.Equal(t, "revoked", decodeMap(t, raw)["verificationStatus"])

	var status string
	require.NoError(t, f.pool.QueryRow(ctx, `SELECT status FROM events WHERE id=$1`, f.futureID).Scan(&status))
	assert.Equal(t, "draft", status, "revocation must unpublish upcoming events")

	var notices int
	require.NoError(t, f.pool.QueryRow(ctx, `
		SELECT count(*) FROM event_notifications
		 WHERE event_id=$1 AND user_id='reader-1' AND kind='change_notice'`, f.futureID).Scan(&notices))
	assert.Equal(t, 1, notices)
}

func TestMiceExpiredOrganizerCannotPublish(t *testing.T) {
	f := setupMiceFixture(t)
	ctx := context.Background()
	_, err := f.pool.Exec(ctx, `
		UPDATE organizer_profiles SET verification_expires_at = now() - interval '1 day'
		 WHERE id=$1`, f.organizerID)
	require.NoError(t, err)

	tomorrow := time.Now().In(time.FixedZone("EAT", 3*3600)).AddDate(0, 0, 2).Format("2006-01-02")
	resp, raw := f.do(t, "POST", "/v1/admin/events", map[string]any{
		"title": "Blocked event", "organizerProfileId": f.organizerID, "venueId": f.venueID,
		"eventClass": "conference", "format": "in_person", "access": "public",
		"type": "conference", "eventDate": tomorrow, "startTime": "09:00", "endTime": "17:00",
		"registrationMode": "rsvp", "capacity": 10,
	}, f.admin)
	require.Equal(t, http.StatusCreated, resp.StatusCode, string(raw))
	eventID := decodeMap(t, raw)["id"].(string)

	// The admin list triggers lazy expiry.
	resp, raw = f.do(t, "GET", "/v1/admin/organizers?status=expired", nil, f.admin)
	require.Equal(t, http.StatusOK, resp.StatusCode, string(raw))
	f.contract(t, "GET", "/v1/admin/organizers", resp.StatusCode, raw)
	require.Len(t, decodeMap(t, raw)["items"].([]any), 1)

	resp, raw = f.do(t, "POST", "/v1/admin/events/"+eventID+"/publish", nil, f.admin)
	require.Equal(t, http.StatusConflict, resp.StatusCode)
	assert.Equal(t, "ORGANIZER_NOT_VERIFIED", decodeMap(t, raw)["error"].(map[string]any)["code"])
}

func TestMiceCommunityTierCannotPublishMiceClass(t *testing.T) {
	f := setupMiceFixture(t)
	resp, raw := f.do(t, "POST", "/v1/admin/organizers", map[string]any{
		"displayName": "Community Group", "trustTier": "community_organizer",
	}, f.admin)
	require.Equal(t, http.StatusCreated, resp.StatusCode, string(raw))
	communityID := decodeMap(t, raw)["id"].(string)
	resp, raw = f.do(t, "POST", "/v1/admin/organizers/"+communityID+"/verification", map[string]any{
		"action": "verify", "trustTier": "community_organizer",
		"reason": "identity and phone verified in person",
	}, f.admin2)
	require.Equal(t, http.StatusOK, resp.StatusCode, string(raw))

	tomorrow := time.Now().In(time.FixedZone("EAT", 3*3600)).AddDate(0, 0, 3).Format("2006-01-02")
	resp, raw = f.do(t, "POST", "/v1/admin/events", map[string]any{
		"title": "Community conference", "organizerProfileId": communityID, "venueId": f.venueID,
		"eventClass": "conference", "format": "in_person", "access": "public",
		"type": "culture", "eventDate": tomorrow, "startTime": "09:00", "endTime": "17:00",
		"registrationMode": "rsvp", "capacity": 10,
	}, f.admin)
	require.Equal(t, http.StatusCreated, resp.StatusCode, string(raw))
	eventID := decodeMap(t, raw)["id"].(string)

	resp, raw = f.do(t, "POST", "/v1/admin/events/"+eventID+"/publish", nil, f.admin)
	require.Equal(t, http.StatusConflict, resp.StatusCode)
	assert.Equal(t, "ORGANIZER_TIER", decodeMap(t, raw)["error"].(map[string]any)["code"])

	// A community-class event publishes.
	resp, raw = f.do(t, "POST", "/v1/admin/events", map[string]any{
		"title": "Community meetup", "organizerProfileId": communityID, "venueId": f.venueID,
		"eventClass": "community", "format": "in_person", "access": "public",
		"type": "culture", "eventDate": tomorrow, "startTime": "09:00", "endTime": "12:00",
		"registrationMode": "rsvp", "capacity": 10,
	}, f.admin)
	require.Equal(t, http.StatusCreated, resp.StatusCode, string(raw))
	communityEventID := decodeMap(t, raw)["id"].(string)
	resp, raw = f.do(t, "POST", "/v1/admin/events/"+communityEventID+"/publish", nil, f.admin)
	require.Equal(t, http.StatusOK, resp.StatusCode, string(raw))
}

func TestMicePaidEventHandoff(t *testing.T) {
	f := setupMiceFixture(t)
	ctx := context.Background()
	tomorrow := time.Now().In(time.FixedZone("EAT", 3*3600)).AddDate(0, 0, 4).Format("2006-01-02")

	resp, raw := f.do(t, "POST", "/v1/admin/events", map[string]any{
		"title": "Paid gala", "organizerProfileId": f.organizerID, "venueId": f.venueID,
		"eventClass": "exhibition", "format": "in_person", "access": "public",
		"type": "culture", "eventDate": tomorrow, "startTime": "18:00", "endTime": "22:00",
		"registrationMode": "ticket_handoff", "priceAmount": 2500, "priceCurrency": "KES",
		"ticketUrl": "https://tickets.example.com/gala",
		"salesEnd":  tomorrow + "T12:00:00+03:00",
	}, f.admin)
	require.Equal(t, http.StatusCreated, resp.StatusCode, string(raw))
	eventID := decodeMap(t, raw)["id"].(string)
	resp, raw = f.do(t, "POST", "/v1/admin/events/"+eventID+"/publish", nil, f.admin)
	require.Equal(t, http.StatusOK, resp.StatusCode, string(raw))

	// A ticket handoff records interest only: no registration, no capacity.
	resp, raw = f.do(t, "POST", "/v1/mobile/events/"+eventID+"/ticket-handoffs", map[string]any{
		"priceAcknowledged": true,
	}, f.reader)
	require.Equal(t, http.StatusCreated, resp.StatusCode, string(raw))
	f.contract(t, "POST", "/v1/mobile/events/{id}/ticket-handoffs", resp.StatusCode, raw)
	handoff := decodeMap(t, raw)
	assert.Equal(t, "https://tickets.example.com/gala", handoff["ticketUrl"])
	assert.Equal(t, "YTCI does not process payment.", handoff["disclosure"])

	var registrations int
	require.NoError(t, f.pool.QueryRow(ctx, `
		SELECT count(*) FROM event_registrations WHERE event_id=$1`, eventID).Scan(&registrations))
	assert.Equal(t, 0, registrations)

	// Registering directly is refused: the mode is handoff.
	resp, _ = f.do(t, "POST", "/v1/mobile/events/"+eventID+"/registrations", map[string]any{}, f.reader)
	assert.Equal(t, http.StatusConflict, resp.StatusCode)

	// A handoff on an RSVP event is refused.
	resp, _ = f.do(t, "POST", "/v1/mobile/events/"+f.futureID+"/ticket-handoffs", map[string]any{}, f.reader)
	assert.Equal(t, http.StatusConflict, resp.StatusCode)
}

func TestMiceSavedEvents(t *testing.T) {
	f := setupMiceFixture(t)
	resp, raw := f.do(t, "POST", "/v1/mobile/events/"+f.futureID+"/save", nil, f.reader)
	require.Equal(t, http.StatusCreated, resp.StatusCode, string(raw))
	f.contract(t, "POST", "/v1/mobile/events/{id}/save", resp.StatusCode, raw)

	resp, raw = f.do(t, "GET", "/v1/mobile/events/saved", nil, f.reader)
	require.Equal(t, http.StatusOK, resp.StatusCode, string(raw))
	f.contract(t, "GET", "/v1/mobile/events/saved", resp.StatusCode, raw)
	items := decodeMap(t, raw)["items"].([]any)
	require.Len(t, items, 1)
	assert.Equal(t, true, items[0].(map[string]any)["saved"])
}

func TestMicePublicHidesInviteOnlyAndDrafts(t *testing.T) {
	f := setupMiceFixture(t)
	resp, _ := f.do(t, "POST", "/v1/mobile/events/"+f.futureID+"/registrations", map[string]any{}, f.reader)
	require.Equal(t, http.StatusCreated, resp.StatusCode)

	// Flip the event to invite-only: discovery and detail both hide it.
	_, err := f.pool.Exec(context.Background(), `UPDATE events SET access='invite_only' WHERE id=$1`, f.futureID)
	require.NoError(t, err)
	resp, _ = f.do(t, "GET", "/v1/public/events/"+f.futureID, nil, "")
	assert.Equal(t, http.StatusNotFound, resp.StatusCode)
	resp, raw := f.do(t, "GET", "/v1/public/events?from=2026-01-01", nil, "")
	require.Equal(t, http.StatusOK, resp.StatusCode)
	for _, item := range decodeMap(t, raw)["items"].([]any) {
		assert.NotEqual(t, f.futureID, item.(map[string]any)["id"])
	}
	_ = fmt.Sprint()
}
