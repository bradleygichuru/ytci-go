package expo

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bradleygichuru/ytci-go/internal/collab"
	"github.com/bradleygichuru/ytci-go/internal/db"
	"github.com/bradleygichuru/ytci-go/internal/middleware"
)

func collabRequest(t *testing.T, method, target, userID string, body any) *http.Request {
	t.Helper()
	var reader *bytes.Reader
	if body == nil {
		reader = bytes.NewReader(nil)
	} else {
		raw, err := json.Marshal(body)
		require.NoError(t, err)
		reader = bytes.NewReader(raw)
	}
	req := httptest.NewRequest(method, target, reader)
	req.Header.Set("Content-Type", "application/json")
	req = req.WithContext(context.WithValue(req.Context(), middleware.CtxUserID, userID))
	return req
}

func collabResponse(t *testing.T, rec *httptest.ResponseRecorder) map[string]any {
	t.Helper()
	var out map[string]any
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &out), rec.Body.String())
	return out
}

func TestInviteEndpointsAndPreview(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}
	ctx := context.Background()
	pool := db.SetupTestDB(t)
	defer pool.Close()

	ownerID := newCollaborationTestUser(t, pool, "Owner")
	editorID := newCollaborationTestUser(t, pool, "Editor")
	viewerID := newCollaborationTestUser(t, pool, "Viewer")
	joinerID := newCollaborationTestUser(t, pool, "Joiner")
	itineraryID := newCollaborationTestItinerary(t, pool, ownerID)
	_, err := pool.Exec(ctx,
		`INSERT INTO itinerary_stops
		 (itinerary_id, day, display_order, title, description, start_time, category, estimated_cost)
		 VALUES ($1, 1, 0, 'Morning drive', 'A slow loop.', '09:00', 'Wildlife', 'KES 2,500')`,
		itineraryID)
	require.NoError(t, err)
	require.NoError(t, AddMember(ctx, pool, itineraryID, editorID, roleEditor, ownerID))
	require.NoError(t, AddMember(ctx, pool, itineraryID, viewerID, roleViewer, ownerID))

	h := NewCollaborationHandler(pool, nil)

	// Editor can invite a viewer.
	req := collabRequest(t, http.MethodPost, "/v1/mobile/itineraries/"+itineraryID+"/invites", editorID, map[string]any{
		"role": "viewer", "maxUses": 5,
	})
	req.SetPathValue("id", itineraryID)
	rec := httptest.NewRecorder()
	h.CreateInvite(rec, req)
	require.Equal(t, http.StatusCreated, rec.Code, rec.Body.String())
	created := collabResponse(t, rec)
	code, _ := created["code"].(string)
	require.Len(t, code, 8)
	inviteID, _ := created["inviteId"].(string)
	require.NotEmpty(t, inviteID)

	// Editor cannot invite another editor.
	req = collabRequest(t, http.MethodPost, "/v1/mobile/itineraries/"+itineraryID+"/invites", editorID, map[string]any{
		"role": "editor",
	})
	req.SetPathValue("id", itineraryID)
	rec = httptest.NewRecorder()
	h.CreateInvite(rec, req)
	assert.Equal(t, http.StatusForbidden, rec.Code)

	// Viewer cannot create an invite.
	req = collabRequest(t, http.MethodPost, "/v1/mobile/itineraries/"+itineraryID+"/invites", viewerID, map[string]any{
		"role": "viewer",
	})
	req.SetPathValue("id", itineraryID)
	rec = httptest.NewRecorder()
	h.CreateInvite(rec, req)
	assert.Equal(t, http.StatusForbidden, rec.Code)

	// Signed-out preview is Plan-only.
	req = httptest.NewRequest(http.MethodGet, "/v1/public/itineraries/preview?code="+code, nil)
	rec = httptest.NewRecorder()
	h.Preview(rec, req)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	preview := collabResponse(t, rec)
	require.Equal(t, "Test trip", preview["title"])
	for _, forbidden := range []string{"budget", "expenses", "receipts", "members", "history", "viewers"} {
		_, exists := preview[forbidden]
		assert.Falsef(t, exists, "preview leaked %s", forbidden)
	}
	days, ok := preview["days"].([]any)
	require.True(t, ok)
	assert.NotEmpty(t, days)

	// Redeem as a new member.
	req = collabRequest(t, http.MethodPost, "/v1/mobile/invites/redeem", joinerID, map[string]any{"code": code})
	rec = httptest.NewRecorder()
	h.RedeemInvite(rec, req)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	assert.Equal(t, "viewer", collabResponse(t, rec)["role"])

	var role string
	require.NoError(t, pool.QueryRow(ctx,
		`SELECT role FROM itinerary_members WHERE itinerary_id = $1 AND user_id = $2`,
		itineraryID, joinerID).Scan(&role))
	assert.Equal(t, roleViewer, role)

	// Revocation is immediate.
	req = collabRequest(t, http.MethodDelete, "/v1/mobile/itineraries/"+itineraryID+"/invites/"+inviteID, ownerID, nil)
	req.SetPathValue("id", itineraryID)
	req.SetPathValue("inviteId", inviteID)
	rec = httptest.NewRecorder()
	h.RevokeInvite(rec, req)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())

	otherID := newCollaborationTestUser(t, pool, "Other")
	req = collabRequest(t, http.MethodPost, "/v1/mobile/invites/redeem", otherID, map[string]any{"code": code})
	rec = httptest.NewRecorder()
	h.RedeemInvite(rec, req)
	assert.Equal(t, http.StatusGone, rec.Code)

	// Expired invites are rejected.
	var expiredCode string
	req = collabRequest(t, http.MethodPost, "/v1/mobile/itineraries/"+itineraryID+"/invites", ownerID, map[string]any{
		"role": "viewer", "maxUses": 1,
	})
	req.SetPathValue("id", itineraryID)
	rec = httptest.NewRecorder()
	h.CreateInvite(rec, req)
	require.Equal(t, http.StatusCreated, rec.Code, rec.Body.String())
	expiredCode, _ = collabResponse(t, rec)["code"].(string)
	_, err = pool.Exec(ctx,
		`UPDATE itinerary_invites SET expires_at = now() - interval '1 minute' WHERE code_hash = $1`,
		hashInviteCode(expiredCode))
	require.NoError(t, err)
	req = collabRequest(t, http.MethodPost, "/v1/mobile/invites/redeem", otherID, map[string]any{"code": expiredCode})
	rec = httptest.NewRecorder()
	h.RedeemInvite(rec, req)
	assert.Equal(t, http.StatusGone, rec.Code)
}

func TestMemberRoleMatrixAndVersionConflict(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}
	ctx := context.Background()
	pool := db.SetupTestDB(t)
	defer pool.Close()

	ownerID := newCollaborationTestUser(t, pool, "Owner")
	editorID := newCollaborationTestUser(t, pool, "Editor")
	viewerID := newCollaborationTestUser(t, pool, "Viewer")
	itineraryID := newCollaborationTestItinerary(t, pool, ownerID)
	_, err := pool.Exec(ctx,
		`INSERT INTO itinerary_stops
		 (itinerary_id, day, display_order, title, description, start_time, category, estimated_cost)
		 VALUES ($1, 1, 0, 'Morning drive', 'A slow loop.', '09:00', 'Wildlife', 'KES 2,500')`,
		itineraryID)
	require.NoError(t, err)
	require.NoError(t, AddMember(ctx, pool, itineraryID, editorID, roleEditor, ownerID))
	require.NoError(t, AddMember(ctx, pool, itineraryID, viewerID, roleViewer, ownerID))

	h := NewCollaborationHandler(pool, nil)

	// Editor cannot change roles.
	req := collabRequest(t, http.MethodPatch, "/v1/mobile/itineraries/"+itineraryID+"/members/"+viewerID, editorID, map[string]any{
		"role": "editor",
	})
	req.SetPathValue("id", itineraryID)
	req.SetPathValue("userId", viewerID)
	rec := httptest.NewRecorder()
	h.SetMemberRole(rec, req)
	assert.Equal(t, http.StatusForbidden, rec.Code)

	// Owner can promote the viewer, but a stale membersVersion conflicts.
	var membersVersion int
	require.NoError(t, pool.QueryRow(ctx,
		`SELECT members_version FROM itineraries WHERE id = $1`, itineraryID).Scan(&membersVersion))
	req = collabRequest(t, http.MethodPatch, "/v1/mobile/itineraries/"+itineraryID+"/members/"+viewerID, ownerID, map[string]any{
		"role": "editor", "expectedVersion": membersVersion,
	})
	req.SetPathValue("id", itineraryID)
	req.SetPathValue("userId", viewerID)
	rec = httptest.NewRecorder()
	h.SetMemberRole(rec, req)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())

	req = collabRequest(t, http.MethodPatch, "/v1/mobile/itineraries/"+itineraryID+"/members/"+viewerID, ownerID, map[string]any{
		"role": "viewer", "expectedVersion": membersVersion,
	})
	req.SetPathValue("id", itineraryID)
	req.SetPathValue("userId", viewerID)
	rec = httptest.NewRecorder()
	h.SetMemberRole(rec, req)
	require.Equal(t, http.StatusConflict, rec.Code, rec.Body.String())
	assert.Equal(t, "VERSION_CONFLICT", collabResponse(t, rec)["error"].(map[string]any)["code"])

	// Viewer can leave, owner cannot.
	req = collabRequest(t, http.MethodPost, "/v1/mobile/itineraries/"+itineraryID+"/leave", viewerID, nil)
	req.SetPathValue("id", itineraryID)
	rec = httptest.NewRecorder()
	h.LeaveTrip(rec, req)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())

	req = collabRequest(t, http.MethodPost, "/v1/mobile/itineraries/"+itineraryID+"/leave", ownerID, nil)
	req.SetPathValue("id", itineraryID)
	rec = httptest.NewRecorder()
	h.LeaveTrip(rec, req)
	assert.Equal(t, http.StatusBadRequest, rec.Code)
}

func TestPresenceLifecycle(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}
	ctx := context.Background()
	pool := db.SetupTestDB(t)
	defer pool.Close()

	ownerID := newCollaborationTestUser(t, pool, "Owner")
	viewerID := newCollaborationTestUser(t, pool, "Viewer")
	itineraryID := newCollaborationTestItinerary(t, pool, ownerID)
	require.NoError(t, AddMember(ctx, pool, itineraryID, viewerID, roleViewer, ownerID))

	h := NewCollaborationHandler(pool, nil)
	heartbeat := func(userID, scope string) *httptest.ResponseRecorder {
		req := collabRequest(t, http.MethodPost, "/v1/mobile/itineraries/"+itineraryID+"/presence", userID, map[string]any{"scope": scope})
		req.SetPathValue("id", itineraryID)
		rec := httptest.NewRecorder()
		h.Heartbeat(rec, req)
		return rec
	}

	require.Equal(t, http.StatusOK, heartbeat(ownerID, "plan").Code)
	require.Equal(t, http.StatusOK, heartbeat(viewerID, "day:2").Code)

	req := collabRequest(t, http.MethodGet, "/v1/mobile/itineraries/"+itineraryID+"/presence", ownerID, nil)
	req.SetPathValue("id", itineraryID)
	rec := httptest.NewRecorder()
	h.GetPresence(rec, req)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	viewers := collabResponse(t, rec)["viewers"].([]any)
	require.Len(t, viewers, 2)

	// Presence older than the 90s stale window drops out of the roster.
	_, err := pool.Exec(ctx,
		`UPDATE itinerary_presence SET last_seen_at = now() - interval '2 minutes'
		 WHERE itinerary_id = $1 AND user_id = $2`, itineraryID, viewerID)
	require.NoError(t, err)
	rec = httptest.NewRecorder()
	h.GetPresence(rec, req)
	require.Equal(t, http.StatusOK, rec.Code)
	viewers = collabResponse(t, rec)["viewers"].([]any)
	require.Len(t, viewers, 1)

	// Expired presence is cleaned up.
	require.NoError(t, collab.CleanupPresence(ctx, pool))
	var count int
	require.NoError(t, pool.QueryRow(ctx,
		`SELECT COUNT(*) FROM itinerary_presence WHERE itinerary_id = $1`, itineraryID).Scan(&count))
	assert.Equal(t, 1, count) // owner heartbeat was fresh
}

func TestInvitePreviewRejectsExpiredAndRevoked(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}
	ctx := context.Background()
	pool := db.SetupTestDB(t)
	defer pool.Close()

	ownerID := newCollaborationTestUser(t, pool, "Owner")
	itineraryID := newCollaborationTestItinerary(t, pool, ownerID)
	h := NewCollaborationHandler(pool, nil)

	req := collabRequest(t, http.MethodPost, "/v1/mobile/itineraries/"+itineraryID+"/invites", ownerID, map[string]any{
		"role": "viewer", "maxUses": 1,
	})
	req.SetPathValue("id", itineraryID)
	rec := httptest.NewRecorder()
	h.CreateInvite(rec, req)
	require.Equal(t, http.StatusCreated, rec.Code, rec.Body.String())
	code, _ := collabResponse(t, rec)["code"].(string)

	// Revoked.
	_, err := pool.Exec(ctx,
		`UPDATE itinerary_invites SET revoked_at = now() WHERE code_hash = $1`, hashInviteCode(code))
	require.NoError(t, err)
	req = httptest.NewRequest(http.MethodGet, "/v1/public/itineraries/preview?code="+code, nil)
	rec = httptest.NewRecorder()
	h.Preview(rec, req)
	assert.Equal(t, http.StatusGone, rec.Code)

	// Expired.
	code = ""
	req = collabRequest(t, http.MethodPost, "/v1/mobile/itineraries/"+itineraryID+"/invites", ownerID, map[string]any{
		"role": "viewer", "maxUses": 1,
	})
	req.SetPathValue("id", itineraryID)
	rec = httptest.NewRecorder()
	h.CreateInvite(rec, req)
	require.Equal(t, http.StatusCreated, rec.Code)
	code, _ = collabResponse(t, rec)["code"].(string)
	_, err = pool.Exec(ctx,
		`UPDATE itinerary_invites SET expires_at = now() - interval '1 minute' WHERE code_hash = $1`,
		hashInviteCode(code))
	require.NoError(t, err)
	req = httptest.NewRequest(http.MethodGet, "/v1/public/itineraries/preview?code="+code, nil)
	rec = httptest.NewRecorder()
	h.Preview(rec, req)
	assert.Equal(t, http.StatusGone, rec.Code)
}

func TestLimiterWindow(t *testing.T) {
	l := collab.NewLimiter(2, time.Minute)
	assert.True(t, l.Allow("a"))
	assert.True(t, l.Allow("a"))
	assert.False(t, l.Allow("a"))
	assert.True(t, l.Allow("b"))
}

func TestEventsStreamChangeAndPresence(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	pool := db.SetupTestDB(t)
	defer pool.Close()

	ownerID := newCollaborationTestUser(t, pool, "Owner")
	viewerID := newCollaborationTestUser(t, pool, "Viewer")
	itineraryID := newCollaborationTestItinerary(t, pool, ownerID)
	require.NoError(t, AddMember(ctx, pool, itineraryID, viewerID, roleViewer, ownerID))

	hub := collab.NewHub(pool)
	hub.Start()
	defer hub.Shutdown()
	time.Sleep(500 * time.Millisecond)

	h := NewCollaborationHandler(pool, hub)
	r := chi.NewRouter()
	r.Use(func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
			userID := req.Header.Get("X-Test-User")
			next.ServeHTTP(w, req.WithContext(context.WithValue(req.Context(), middleware.CtxUserID, userID)))
		})
	})
	r.Get("/itineraries/{id}/events", h.Stream)
	ts := httptest.NewServer(r)
	defer ts.Close()

	openStream := func(userID string) (*http.Response, chan string) {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, ts.URL+"/itineraries/"+itineraryID+"/events", nil)
		require.NoError(t, err)
		req.Header.Set("X-Test-User", userID)
		res, err := http.DefaultClient.Do(req)
		require.NoError(t, err)
		require.Equal(t, http.StatusOK, res.StatusCode)
		lines := make(chan string, 64)
		go func() {
			scanner := bufio.NewScanner(res.Body)
			for scanner.Scan() {
				lines <- scanner.Text()
			}
			close(lines)
		}()
		return res, lines
	}

	waitForEvent := func(lines chan string, event string) {
		t.Helper()
		deadline := time.After(5 * time.Second)
		for {
			select {
			case line, ok := <-lines:
				if !ok {
					t.Fatalf("stream closed before %s", event)
				}
				if strings.HasPrefix(line, "event: ") && strings.TrimPrefix(line, "event: ") == event {
					return
				}
			case <-deadline:
				t.Fatalf("timed out waiting for %s", event)
			}
		}
	}

	ownerRes, ownerLines := openStream(ownerID)
	defer ownerRes.Body.Close()
	waitForEvent(ownerLines, "ready")
	waitForEvent(ownerLines, "presence")

	viewerRes, viewerLines := openStream(viewerID)
	defer viewerRes.Body.Close()
	waitForEvent(viewerLines, "ready")
	waitForEvent(viewerLines, "presence")

	// Give the LISTEN bridge a moment if the hub just connected.
	time.Sleep(300 * time.Millisecond)
	require.NoError(t, collab.PublishChange(ctx, pool, itineraryID, "plan", "updated", 2, ownerID))
	waitForEvent(ownerLines, "change")
	waitForEvent(viewerLines, "change")

	// A heartbeat publishes the updated roster to both streams.
	req := collabRequest(t, http.MethodPost, "/v1/mobile/itineraries/"+itineraryID+"/presence", viewerID, map[string]any{"scope": "day:2"})
	req.SetPathValue("id", itineraryID)
	rec := httptest.NewRecorder()
	h.Heartbeat(rec, req)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	waitForEvent(ownerLines, "presence")
}
