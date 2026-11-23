package server_test

import (
	"context"
	"fmt"
	"net/http"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ---------------------------------------------------------------------------
// Admin MICE operations — additive contract operations
// ---------------------------------------------------------------------------

func TestMiceAdminEventLifecycle(t *testing.T) {
	f := setupMiceFixture(t)

	// Cancel a published event, then restore it.
	resp, raw := f.do(t, "POST", "/v1/admin/events/"+f.futureID+"/lifecycle", map[string]any{
		"action": "cancel", "reason": "Venue withdrew the booking",
	}, f.admin)
	require.Equal(t, http.StatusOK, resp.StatusCode, string(raw))
	f.contract(t, "POST", "/v1/admin/events/{id}/lifecycle", resp.StatusCode, raw)
	assert.Equal(t, "cancelled", decodeMap(t, raw)["status"])

	resp, raw = f.do(t, "POST", "/v1/admin/events/"+f.futureID+"/lifecycle", map[string]any{
		"action": "restore", "reason": "Venue booking reinstated",
	}, f.admin)
	require.Equal(t, http.StatusOK, resp.StatusCode, string(raw))
	f.contract(t, "POST", "/v1/admin/events/{id}/lifecycle", resp.StatusCode, raw)
	assert.Equal(t, "published", decodeMap(t, raw)["status"])

	// Unpublish: the public detail must stop resolving.
	resp, raw = f.do(t, "POST", "/v1/admin/events/"+f.futureID+"/lifecycle", map[string]any{
		"action": "unpublish", "reason": "Material correction pending",
	}, f.admin)
	require.Equal(t, http.StatusOK, resp.StatusCode, string(raw))
	assert.Equal(t, "draft", decodeMap(t, raw)["status"])

	resp, _ = f.do(t, "GET", "/v1/public/events/"+f.futureID, nil, "")
	assert.Equal(t, http.StatusNotFound, resp.StatusCode)

	// Archive is a distinct state and is listable.
	resp, raw = f.do(t, "POST", "/v1/admin/events/"+f.futureID+"/lifecycle", map[string]any{
		"action": "archive", "reason": "Series retired for 2027",
	}, f.admin)
	require.Equal(t, http.StatusOK, resp.StatusCode, string(raw))
	assert.Equal(t, "archived", decodeMap(t, raw)["status"])

	resp, raw = f.do(t, "GET", "/v1/admin/events?status=archived", nil, f.admin)
	require.Equal(t, http.StatusOK, resp.StatusCode, string(raw))
	items := decodeMap(t, raw)["items"].([]any)
	require.Len(t, items, 1)
	assert.Equal(t, f.futureID, items[0].(map[string]any)["id"])

	// Validation, permissions and invalid transitions.
	resp, raw = f.do(t, "POST", "/v1/admin/events/"+f.liveID+"/lifecycle", map[string]any{
		"action": "cancel", "reason": "",
	}, f.admin)
	assert.Equal(t, http.StatusBadRequest, resp.StatusCode)
	f.contract(t, "POST", "/v1/admin/events/{id}/lifecycle", resp.StatusCode, raw)

	resp, raw = f.do(t, "POST", "/v1/admin/events/"+f.liveID+"/lifecycle", map[string]any{
		"action": "cancel", "reason": "Testing permissions",
	}, f.reader)
	assert.Equal(t, http.StatusForbidden, resp.StatusCode)
	f.contract(t, "POST", "/v1/admin/events/{id}/lifecycle", resp.StatusCode, raw)

	resp, raw = f.do(t, "POST", "/v1/admin/events/"+f.liveID+"/lifecycle", map[string]any{
		"action": "archive", "reason": "Archive to test a repeat",
	}, f.admin)
	require.Equal(t, http.StatusOK, resp.StatusCode, string(raw))
	resp, raw = f.do(t, "POST", "/v1/admin/events/"+f.liveID+"/lifecycle", map[string]any{
		"action": "archive", "reason": "Repeat archive attempt",
	}, f.admin)
	assert.Equal(t, http.StatusConflict, resp.StatusCode)
	f.contract(t, "POST", "/v1/admin/events/{id}/lifecycle", resp.StatusCode, raw)
}

func TestMiceAdminWaitlistOffer(t *testing.T) {
	f := setupMiceFixture(t)
	ctx := context.Background()

	// Capacity leaves one free seat while reader-2 waits.
	_, err := f.pool.Exec(ctx, `UPDATE events SET capacity=2 WHERE id=$1`, f.futureID)
	require.NoError(t, err)
	f.insertRegistration(t, f.futureID, "reader-1")
	var waitlistID string
	err = f.pool.QueryRow(ctx, `
		INSERT INTO event_registrations (event_id, user_id, status, waitlist_position)
		VALUES ($1,'reader-2','waitlisted',1) RETURNING id::text`, f.futureID).Scan(&waitlistID)
	require.NoError(t, err)

	// An admin sends the offer (the default target is the FIFO head).
	resp, raw := f.do(t, "POST", "/v1/admin/events/"+f.futureID+"/waitlist-offers", map[string]any{
		"note": "A seat opened up",
	}, f.admin)
	require.Equal(t, http.StatusCreated, resp.StatusCode, string(raw))
	f.contract(t, "POST", "/v1/admin/events/{id}/waitlist-offers", resp.StatusCode, raw)
	offer := decodeMap(t, raw)
	assert.Equal(t, waitlistID, offer["registrationId"])
	assert.NotEmpty(t, offer["expiresAt"])

	// One open offer per event.
	resp, raw = f.do(t, "POST", "/v1/admin/events/"+f.futureID+"/waitlist-offers", map[string]any{
		"registrationId": waitlistID,
	}, f.admin)
	assert.Equal(t, http.StatusConflict, resp.StatusCode, string(raw))
	f.contract(t, "POST", "/v1/admin/events/{id}/waitlist-offers", resp.StatusCode, raw)

	// Accept promotes the registration.
	resp, raw = f.do(t, "POST", fmt.Sprintf("/v1/admin/events/%s/waitlist-offers/%s/decision", f.futureID, waitlistID), map[string]any{
		"decision": "accept",
	}, f.admin)
	require.Equal(t, http.StatusOK, resp.StatusCode, string(raw))
	f.contract(t, "POST", "/v1/admin/events/{id}/waitlist-offers/{registrationId}/decision", resp.StatusCode, raw)
	assert.Equal(t, "confirmed", decodeMap(t, raw)["status"])

	// Withdraw returns the next person to the queue without cancelling them.
	_, err = f.pool.Exec(ctx, `UPDATE events SET capacity=3 WHERE id=$1`, f.futureID)
	require.NoError(t, err)
	var secondID string
	err = f.pool.QueryRow(ctx, `
		INSERT INTO event_registrations (event_id, user_id, status, waitlist_position)
		VALUES ($1,'reader-3','waitlisted',1) RETURNING id::text`, f.futureID).Scan(&secondID)
	require.NoError(t, err)
	resp, raw = f.do(t, "POST", "/v1/admin/events/"+f.futureID+"/waitlist-offers", map[string]any{
		"registrationId": secondID,
	}, f.admin)
	require.Equal(t, http.StatusCreated, resp.StatusCode, string(raw))

	resp, raw = f.do(t, "POST", fmt.Sprintf("/v1/admin/events/%s/waitlist-offers/%s/decision", f.futureID, secondID), map[string]any{
		"decision": "withdraw", "reason": "Reader asked to stay in the queue",
	}, f.admin)
	require.Equal(t, http.StatusOK, resp.StatusCode, string(raw))
	body := decodeMap(t, raw)
	assert.Equal(t, "waitlisted", body["status"])

	// A reader cannot operate the queue.
	resp, raw = f.do(t, "POST", "/v1/admin/events/"+f.futureID+"/waitlist-offers", map[string]any{}, f.reader)
	assert.Equal(t, http.StatusForbidden, resp.StatusCode)
	f.contract(t, "POST", "/v1/admin/events/{id}/waitlist-offers", resp.StatusCode, raw)
}

func TestMiceAdminRegistrationCorrectionAndCheckInUndo(t *testing.T) {
	f := setupMiceFixture(t)
	ctx := context.Background()

	var regID string
	err := f.pool.QueryRow(ctx, `
		INSERT INTO event_registrations (event_id, user_id, status, answers)
		VALUES ($1,'reader-1','confirmed','{"accessibility":"Step-free access required"}')
		RETURNING id::text`, f.futureID).Scan(&regID)
	require.NoError(t, err)
	var waitingID string
	err = f.pool.QueryRow(ctx, `
		INSERT INTO event_registrations (event_id, user_id, status, waitlist_position, answers)
		VALUES ($1,'reader-2','waitlisted',1,'{"accessibility":"Step-free access required"}')
		RETURNING id::text`, f.futureID).Scan(&waitingID)
	require.NoError(t, err)

	// Cancel a confirmed registration; the next waitlisted reader is promoted.
	resp, raw := f.do(t, "PATCH", fmt.Sprintf("/v1/admin/events/%s/registrations/%s", f.futureID, regID), map[string]any{
		"status": "cancelled", "reason": "Attendee asked to withdraw",
	}, f.admin)
	require.Equal(t, http.StatusOK, resp.StatusCode, string(raw))
	f.contract(t, "PATCH", "/v1/admin/events/{id}/registrations/{registrationId}", resp.StatusCode, raw)
	assert.Equal(t, "cancelled", decodeMap(t, raw)["status"])

	var promoted string
	require.NoError(t, f.pool.QueryRow(ctx, `SELECT status FROM event_registrations WHERE id=$1`, waitingID).Scan(&promoted))
	assert.Equal(t, "confirmed", promoted)

	// Invalid answers are rejected by the question validator.
	resp, raw = f.do(t, "PATCH", fmt.Sprintf("/v1/admin/events/%s/registrations/%s", f.futureID, waitingID), map[string]any{
		"answers": map[string]string{"unknown-question": "yes"},
		"reason":  "Correct the access needs",
	}, f.admin)
	assert.Equal(t, http.StatusBadRequest, resp.StatusCode, string(raw))

	// Correct the answers.
	resp, raw = f.do(t, "PATCH", fmt.Sprintf("/v1/admin/events/%s/registrations/%s", f.futureID, waitingID), map[string]any{
		"answers": map[string]string{"accessibility": "Wheelchair space"},
		"reason":  "Attendee updated their access needs",
	}, f.admin)
	require.Equal(t, http.StatusOK, resp.StatusCode, string(raw))
	assert.Equal(t, "Wheelchair space", decodeMap(t, raw)["answers"].(map[string]any)["accessibility"])

	// Check-in undo: seed a check-in row against the live event.
	var undoReg string
	err = f.pool.QueryRow(ctx, `
		INSERT INTO event_registrations (event_id, user_id, status, checked_in_at)
		VALUES ($1,'reader-3','checked_in',now()) RETURNING id::text`, f.liveID).Scan(&undoReg)
	require.NoError(t, err)
	var checkInID string
	err = f.pool.QueryRow(ctx, `
		INSERT INTO event_checkins (registration_id, event_id, user_id, method, staff_user_id)
		VALUES ($1,$2,'reader-3','manual','admin-1') RETURNING id::text`, undoReg, f.liveID).Scan(&checkInID)
	require.NoError(t, err)

	resp, raw = f.do(t, "POST", fmt.Sprintf("/v1/admin/events/%s/checkins/%s/undo", f.liveID, checkInID), map[string]any{
		"reason": "Scanned the wrong attendee",
	}, f.admin)
	require.Equal(t, http.StatusOK, resp.StatusCode, string(raw))
	f.contract(t, "POST", "/v1/admin/events/{id}/checkins/{checkInId}/undo", resp.StatusCode, raw)

	var status string
	var checkedInAt *time.Time
	require.NoError(t, f.pool.QueryRow(ctx, `
		SELECT status, checked_in_at FROM event_registrations WHERE id=$1`, undoReg).Scan(&status, &checkedInAt))
	assert.Equal(t, "confirmed", status)
	assert.Nil(t, checkedInAt)

	var remaining int
	require.NoError(t, f.pool.QueryRow(ctx, `SELECT count(*) FROM event_checkins WHERE id=$1`, checkInID).Scan(&remaining))
	assert.Zero(t, remaining)

	// A checked-in registration must be un-checked before it can be cancelled.
	err = f.pool.QueryRow(ctx, `
		INSERT INTO event_registrations (event_id, user_id, status, checked_in_at)
		VALUES ($1,'reader-2','checked_in',now()) RETURNING id::text`, f.liveID).Scan(&undoReg)
	require.NoError(t, err)
	err = f.pool.QueryRow(ctx, `
		INSERT INTO event_checkins (registration_id, event_id, user_id, method)
		VALUES ($1,$2,'reader-2','manual') RETURNING id::text`, undoReg, f.liveID).Scan(&checkInID)
	require.NoError(t, err)

	resp, raw = f.do(t, "PATCH", fmt.Sprintf("/v1/admin/events/%s/registrations/%s", f.liveID, undoReg), map[string]any{
		"status": "cancelled", "reason": "Attempted cancel before undo",
	}, f.admin)
	assert.Equal(t, http.StatusConflict, resp.StatusCode, string(raw))
	f.contract(t, "PATCH", "/v1/admin/events/{id}/registrations/{registrationId}", resp.StatusCode, raw)

	// A reader cannot correct a registration.
	resp, raw = f.do(t, "PATCH", fmt.Sprintf("/v1/admin/events/%s/registrations/%s", f.liveID, undoReg), map[string]any{
		"status": "cancelled", "reason": "Reader attempt",
	}, f.reader)
	assert.Equal(t, http.StatusForbidden, resp.StatusCode)
}

func TestMiceAdminSpeakerLifecycle(t *testing.T) {
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
	sessionID := decodeMap(t, raw)["id"].(string)
	resp, raw = f.do(t, "POST", "/v1/admin/sessions/"+sessionID+"/speakers", map[string]any{
		"personId": personID, "role": "speaker",
	}, f.admin)
	require.Equal(t, http.StatusOK, resp.StatusCode, string(raw))

	// Edit.
	resp, raw = f.do(t, "PATCH", fmt.Sprintf("/v1/admin/events/%s/people/%s", f.futureID, personID), map[string]any{
		"displayName":  "Dr Wanjiku Kamau-Njoroge",
		"title":        "Conservation lead",
		"organization": "Tourism Board",
		"isPublic":     true,
	}, f.admin)
	require.Equal(t, http.StatusOK, resp.StatusCode, string(raw))
	f.contract(t, "PATCH", "/v1/admin/events/{id}/people/{personId}", resp.StatusCode, raw)
	assert.Equal(t, "Dr Wanjiku Kamau-Njoroge", decodeMap(t, raw)["displayName"])

	// Consent withdrawn hides the speaker from the public agenda but keeps the
	// record readable to the admin.
	resp, raw = f.do(t, "POST", fmt.Sprintf("/v1/admin/events/%s/people/%s/consent", f.futureID, personID), map[string]any{
		"consent": "declined", "note": "Speaker asked to be removed from the public programme",
	}, f.admin)
	require.Equal(t, http.StatusOK, resp.StatusCode, string(raw))
	f.contract(t, "POST", "/v1/admin/events/{id}/people/{personId}/consent", resp.StatusCode, raw)
	assert.Equal(t, "declined", decodeMap(t, raw)["consentStatus"])

	resp, raw = f.do(t, "GET", "/v1/public/events/"+f.futureID, nil, "")
	require.Equal(t, http.StatusOK, resp.StatusCode)
	agenda := decodeMap(t, raw)["agenda"].([]any)
	require.Len(t, agenda, 1)
	assert.Empty(t, agenda[0].(map[string]any)["speakers"])

	resp, raw = f.do(t, "GET", "/v1/admin/events/"+f.futureID, nil, f.admin)
	require.Equal(t, http.StatusOK, resp.StatusCode, string(raw))
	adminAgenda := decodeMap(t, raw)["agenda"].([]any)
	require.Len(t, adminAgenda, 1)
	adminSpeakers := adminAgenda[0].(map[string]any)["speakers"].([]any)
	require.Len(t, adminSpeakers, 1)
	assert.Equal(t, "declined", adminSpeakers[0].(map[string]any)["consentStatus"])

	// Granting restores the public projection.
	resp, raw = f.do(t, "POST", fmt.Sprintf("/v1/admin/events/%s/people/%s/consent", f.futureID, personID), map[string]any{
		"consent": "granted",
	}, f.admin)
	require.Equal(t, http.StatusOK, resp.StatusCode, string(raw))
	resp, raw = f.do(t, "GET", "/v1/public/events/"+f.futureID, nil, "")
	require.Equal(t, http.StatusOK, resp.StatusCode)
	agenda = decodeMap(t, raw)["agenda"].([]any)
	require.Len(t, agenda[0].(map[string]any)["speakers"].([]any), 1)

	// Removing the speaker clears the session links but keeps the person row.
	resp, raw = f.do(t, "DELETE", fmt.Sprintf("/v1/admin/events/%s/people/%s", f.futureID, personID), nil, f.admin)
	require.Equal(t, http.StatusOK, resp.StatusCode, string(raw))
	f.contract(t, "DELETE", "/v1/admin/events/{id}/people/{personId}", resp.StatusCode, raw)

	var people int
	require.NoError(t, f.pool.QueryRow(context.Background(), `SELECT count(*) FROM event_people WHERE id=$1`, personID).Scan(&people))
	assert.Equal(t, 1, people)

	resp, raw = f.do(t, "GET", "/v1/admin/events/"+f.futureID, nil, f.admin)
	require.Equal(t, http.StatusOK, resp.StatusCode)
	adminAgenda = decodeMap(t, raw)["agenda"].([]any)
	require.Len(t, adminAgenda, 1)
	assert.Empty(t, adminAgenda[0].(map[string]any)["speakers"])
}

func TestMiceAdminAuditRead(t *testing.T) {
	f := setupMiceFixture(t)

	resp, raw := f.do(t, "POST", "/v1/admin/events/"+f.futureID+"/lifecycle", map[string]any{
		"action": "cancel", "reason": "Audit trail check",
	}, f.admin)
	require.Equal(t, http.StatusOK, resp.StatusCode, string(raw))

	resp, raw = f.do(t, "GET", "/v1/admin/mice/audit?entityType=event&entityId="+f.futureID, nil, f.admin)
	require.Equal(t, http.StatusOK, resp.StatusCode, string(raw))
	f.contract(t, "GET", "/v1/admin/mice/audit", resp.StatusCode, raw)
	page := decodeMap(t, raw)
	items := page["items"].([]any)
	require.Len(t, items, 1)
	entry := items[0].(map[string]any)
	assert.Equal(t, "event.cancel", entry["action"])
	assert.Equal(t, "event", entry["entityType"])
	assert.Equal(t, f.futureID, entry["entityId"])
	assert.Equal(t, "admin-1", entry["actorId"])
	assert.Equal(t, "Test administrator", entry["actorName"])
	assert.Equal(t, "Audit trail check", entry["details"].(map[string]any)["reason"])

	// Readers cannot read the admin audit.
	resp, raw = f.do(t, "GET", "/v1/admin/mice/audit", nil, f.reader)
	assert.Equal(t, http.StatusForbidden, resp.StatusCode)
	f.contract(t, "GET", "/v1/admin/mice/audit", resp.StatusCode, raw)
}

func TestMiceAdminVenueEvidenceAndRetire(t *testing.T) {
	f := setupMiceFixture(t)

	// A second venue to merge into.
	resp, raw := f.do(t, "POST", "/v1/admin/venues", map[string]any{
		"name": "KICC Lenana Wing", "county": "Nairobi", "countyGid": 47,
	}, f.admin)
	require.Equal(t, http.StatusCreated, resp.StatusCode, string(raw))
	targetID := decodeMap(t, raw)["id"].(string)

	// Evidence is appended and returned newest first.
	resp, raw = f.do(t, "POST", "/v1/admin/venues/"+f.venueID+"/evidence", map[string]any{
		"claim": "Step-free entrance", "note": "Ramp inspected at the Harambee gate",
		"reference": "SITE-2026-11-01",
	}, f.admin)
	require.Equal(t, http.StatusCreated, resp.StatusCode, string(raw))
	f.contract(t, "POST", "/v1/admin/venues/{id}/evidence", resp.StatusCode, raw)

	resp, raw = f.do(t, "POST", "/v1/admin/venues/"+f.venueID+"/evidence", map[string]any{
		"claim": "Accessible toilet", "note": "Ground-floor toilet checked",
	}, f.admin)
	require.Equal(t, http.StatusCreated, resp.StatusCode, string(raw))
	evidence := decodeMap(t, raw)["evidence"].([]any)
	require.Len(t, evidence, 2)
	assert.Equal(t, "Accessible toilet", evidence[0].(map[string]any)["claim"])
	assert.Equal(t, "Step-free entrance", evidence[1].(map[string]any)["claim"])
	assert.Equal(t, "admin-1", evidence[1].(map[string]any)["recordedBy"])

	// Admin read carries the retirement fields and the evidence.
	resp, raw = f.do(t, "GET", "/v1/admin/venues/"+f.venueID, nil, f.admin)
	require.Equal(t, http.StatusOK, resp.StatusCode, string(raw))
	f.contract(t, "GET", "/v1/admin/venues/{id}", resp.StatusCode, raw)

	// Validation and permission denial.
	resp, raw = f.do(t, "POST", "/v1/admin/venues/"+f.venueID+"/evidence", map[string]any{
		"claim": "", "note": "no claim",
	}, f.admin)
	assert.Equal(t, http.StatusBadRequest, resp.StatusCode)
	f.contract(t, "POST", "/v1/admin/venues/{id}/evidence", resp.StatusCode, raw)

	resp, raw = f.do(t, "POST", "/v1/admin/venues/"+f.venueID+"/evidence", map[string]any{
		"claim": "Step-free entrance", "note": "reader attempt",
	}, f.reader)
	assert.Equal(t, http.StatusForbidden, resp.StatusCode)
	f.contract(t, "POST", "/v1/admin/venues/{id}/evidence", resp.StatusCode, raw)

	// A merge re-points the events and records the surviving venue.
	resp, raw = f.do(t, "POST", "/v1/admin/venues/"+f.venueID+"/retire", map[string]any{
		"reason": "Duplicate KICC record", "mergedIntoVenueId": targetID,
	}, f.admin)
	require.Equal(t, http.StatusOK, resp.StatusCode, string(raw))
	f.contract(t, "POST", "/v1/admin/venues/{id}/retire", resp.StatusCode, raw)
	retired := decodeMap(t, raw)
	assert.NotEmpty(t, retired["retiredAt"])
	assert.Equal(t, targetID, retired["mergedIntoVenueId"])

	var repointed int
	require.NoError(t, f.pool.QueryRow(context.Background(), `
		SELECT count(*) FROM events WHERE venue_id=$1`, targetID).Scan(&repointed))
	assert.Equal(t, 2, repointed)

	resp, raw = f.do(t, "GET", "/v1/public/events/"+f.futureID, nil, "")
	require.Equal(t, http.StatusOK, resp.StatusCode, string(raw))
	publicDetail := decodeMap(t, raw)
	assert.Equal(t, targetID, publicDetail["venueDetail"].(map[string]any)["id"])
	assert.NotEqual(t, "kicc", publicDetail["venue"].(map[string]any)["slug"])

	// The retired source is gone from the public catalogue.
	resp, _ = f.do(t, "GET", "/v1/public/venues/kicc", nil, "")
	assert.Equal(t, http.StatusNotFound, resp.StatusCode)

	// Repeat, self-merge, unknown target and permission denials.
	resp, raw = f.do(t, "POST", "/v1/admin/venues/"+f.venueID+"/retire", map[string]any{
		"reason": "Already retired",
	}, f.admin)
	assert.Equal(t, http.StatusConflict, resp.StatusCode)
	f.contract(t, "POST", "/v1/admin/venues/{id}/retire", resp.StatusCode, raw)

	resp, raw = f.do(t, "POST", "/v1/admin/venues/"+targetID+"/retire", map[string]any{
		"reason": "Self merge", "mergedIntoVenueId": targetID,
	}, f.admin)
	assert.Equal(t, http.StatusConflict, resp.StatusCode)

	resp, raw = f.do(t, "POST", "/v1/admin/venues/"+targetID+"/retire", map[string]any{
		"reason": "Unknown target", "mergedIntoVenueId": "0a5b6c7d-9999-4a2b-9c3d-000000000099",
	}, f.admin)
	assert.Equal(t, http.StatusBadRequest, resp.StatusCode)
	f.contract(t, "POST", "/v1/admin/venues/{id}/retire", resp.StatusCode, raw)

	resp, raw = f.do(t, "POST", "/v1/admin/venues/"+targetID+"/retire", map[string]any{
		"reason": "Reader attempt",
	}, f.reader)
	assert.Equal(t, http.StatusForbidden, resp.StatusCode)

	// Retiring without a merge keeps a published event listable but hides the
	// venue from its public payload.
	resp, raw = f.do(t, "POST", "/v1/admin/venues", map[string]any{
		"name": "Mara Serena Annex", "county": "Narok",
	}, f.admin)
	require.Equal(t, http.StatusCreated, resp.StatusCode, string(raw))
	annexID := decodeMap(t, raw)["id"].(string)
	tomorrow := time.Now().In(time.FixedZone("EAT", 3*3600)).AddDate(0, 0, 2).Format("2006-01-02")
	resp, raw = f.do(t, "POST", "/v1/admin/events", map[string]any{
		"title": "Annex Field Day", "organizerProfileId": f.organizerID,
		"venueId": annexID, "eventClass": "community", "format": "in_person",
		"access": "public", "type": "community", "eventDate": tomorrow,
		"startTime": "09:00", "endTime": "17:00", "timezone": "Africa/Nairobi",
		"description": "A field day at the annex.", "capacity": 20,
		"registrationMode": "rsvp",
		"accessibility":    map[string]any{"stepFreeEntrance": "yes"},
	}, f.admin)
	require.Equal(t, http.StatusCreated, resp.StatusCode, string(raw))
	annexEventID := decodeMap(t, raw)["id"].(string)
	resp, raw = f.do(t, "POST", "/v1/admin/events/"+annexEventID+"/publish", nil, f.admin)
	require.Equal(t, http.StatusOK, resp.StatusCode, string(raw))

	resp, raw = f.do(t, "POST", "/v1/admin/venues/"+annexID+"/retire", map[string]any{
		"reason": "Annex is no longer used",
	}, f.admin)
	require.Equal(t, http.StatusOK, resp.StatusCode, string(raw))
	f.contract(t, "POST", "/v1/admin/venues/{id}/retire", resp.StatusCode, raw)

	resp, raw = f.do(t, "GET", "/v1/public/events/"+annexEventID, nil, "")
	require.Equal(t, http.StatusOK, resp.StatusCode, string(raw))
	annexDetail := decodeMap(t, raw)
	_, hasVenue := annexDetail["venue"]
	assert.False(t, hasVenue)
	_, hasVenueDetail := annexDetail["venueDetail"]
	assert.False(t, hasVenueDetail)

	resp, raw = f.do(t, "GET", "/v1/public/events?limit=50", nil, "")
	require.Equal(t, http.StatusOK, resp.StatusCode, string(raw))
	found := false
	for _, item := range decodeMap(t, raw)["items"].([]any) {
		event := item.(map[string]any)
		if event["id"] == annexEventID {
			found = true
			_, leaked := event["venue"]
			assert.False(t, leaked)
		}
	}
	assert.True(t, found, "the retired-venue event must remain publicly listable")

	// The audit trail names the evidence rows and the merge.
	resp, raw = f.do(t, "GET", "/v1/admin/mice/audit?entityType=venue&entityId="+f.venueID, nil, f.admin)
	require.Equal(t, http.StatusOK, resp.StatusCode, string(raw))
	actions := map[string]int{}
	for _, item := range decodeMap(t, raw)["items"].([]any) {
		actions[item.(map[string]any)["action"].(string)]++
	}
	assert.Equal(t, 2, actions["venue.evidence_added"])
	assert.Equal(t, 1, actions["venue.merged"])
}

func TestMiceAdminOrganizerEvidenceRequest(t *testing.T) {
	f := setupMiceFixture(t)

	firstNote := "Please send the current business registration certificate."
	resp, raw := f.do(t, "POST", "/v1/admin/organizers/"+f.organizerID+"/evidence-requests", map[string]any{
		"note": firstNote,
	}, f.admin2)
	require.Equal(t, http.StatusCreated, resp.StatusCode, string(raw))
	f.contract(t, "POST", "/v1/admin/organizers/{id}/evidence-requests", resp.StatusCode, raw)
	result := decodeMap(t, raw)
	request := result["request"].(map[string]any)
	assert.Equal(t, firstNote, request["note"])
	assert.Equal(t, f.organizerID, request["organizerId"])
	assert.Nil(t, request["resolvedAt"])

	requests := result["organizer"].(map[string]any)["evidenceRequests"].([]any)
	require.Len(t, requests, 1)

	secondNote := "Also send the venue access audit from last quarter."
	resp, raw = f.do(t, "POST", "/v1/admin/organizers/"+f.organizerID+"/evidence-requests", map[string]any{
		"note": secondNote,
	}, f.admin2)
	require.Equal(t, http.StatusCreated, resp.StatusCode, string(raw))

	// The admin organizer read is newest first and carries both requests.
	resp, raw = f.do(t, "GET", "/v1/admin/organizers", nil, f.admin)
	require.Equal(t, http.StatusOK, resp.StatusCode, string(raw))
	var listed []any
	for _, item := range decodeMap(t, raw)["items"].([]any) {
		organizer := item.(map[string]any)
		if organizer["id"] == f.organizerID {
			listed = organizer["evidenceRequests"].([]any)
		}
	}
	require.Len(t, listed, 2)
	assert.Equal(t, secondNote, listed[0].(map[string]any)["note"])
	assert.Equal(t, firstNote, listed[1].(map[string]any)["note"])
	assert.Equal(t, "admin-2", listed[0].(map[string]any)["requestedBy"])

	// Validation and permissions.
	resp, raw = f.do(t, "POST", "/v1/admin/organizers/"+f.organizerID+"/evidence-requests", map[string]any{
		"note": "short",
	}, f.admin2)
	assert.Equal(t, http.StatusBadRequest, resp.StatusCode)
	f.contract(t, "POST", "/v1/admin/organizers/{id}/evidence-requests", resp.StatusCode, raw)

	resp, raw = f.do(t, "POST", "/v1/admin/organizers/"+f.organizerID+"/evidence-requests", map[string]any{
		"note": "A reader cannot request evidence",
	}, f.reader)
	assert.Equal(t, http.StatusForbidden, resp.StatusCode)
	f.contract(t, "POST", "/v1/admin/organizers/{id}/evidence-requests", resp.StatusCode, raw)

	// A recorded decision resolves the outstanding requests.
	resp, raw = f.do(t, "POST", "/v1/admin/organizers/"+f.organizerID+"/verification", map[string]any{
		"action": "verify", "trustTier": "verified_organizer",
		"reason": "Evidence received and reviewed by the panel",
	}, f.admin2)
	require.Equal(t, http.StatusOK, resp.StatusCode, string(raw))
	resolved := decodeMap(t, raw)["evidenceRequests"].([]any)
	require.Len(t, resolved, 2)
	for _, item := range resolved {
		assert.NotNil(t, item.(map[string]any)["resolvedAt"])
	}

	// The audit row names the request.
	resp, raw = f.do(t, "GET", "/v1/admin/mice/audit?entityType=organizer&entityId="+f.organizerID, nil, f.admin)
	require.Equal(t, http.StatusOK, resp.StatusCode, string(raw))
	actions := map[string]int{}
	for _, item := range decodeMap(t, raw)["items"].([]any) {
		actions[item.(map[string]any)["action"].(string)]++
	}
	assert.Equal(t, 2, actions["organizer.evidence_requested"])
}
