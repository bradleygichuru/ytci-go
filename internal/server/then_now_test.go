package server_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bradleygichuru/ytci-go/internal/config"
	"github.com/bradleygichuru/ytci-go/internal/db"
	"github.com/bradleygichuru/ytci-go/internal/server"
	"github.com/bradleygichuru/ytci-go/internal/thennow"
)

func setupThenNowTest(t *testing.T) (*httptest.Server, *pgxpool.Pool, map[string]string) {
	t.Helper()
	ctx := context.Background()
	pool := db.SetupTestDB(t)
	t.Cleanup(func() { pool.Close() })
	setupAuthTables(t, ctx, pool)

	users := []struct {
		id, email, role, token string
	}{
		{"tn-super", "tn-super@test.com", "super_admin", "tn-super-token"},
		{"tn-admin", "tn-admin@test.com", "administrator", "tn-admin-token"},
		{"tn-editor", "tn-editor@test.com", "content_editor", "tn-editor-token"},
		{"tn-moderator", "tn-moderator@test.com", "moderator", "tn-moderator-token"},
	}
	tokens := map[string]string{}
	for _, user := range users {
		_, err := pool.Exec(ctx, `
			INSERT INTO users (id, name, email, created_at, updated_at, role)
			VALUES ($1, $2, $3, now(), now(), $4)`, user.id, user.role, user.email, user.role)
		require.NoError(t, err)
		_, err = pool.Exec(ctx, `
			INSERT INTO sessions (id, expires_at, token, created_at, updated_at, user_id)
			VALUES ($1, now() + interval '1 day', $2, now(), now(), $3)`,
			"sess-"+user.token, user.token, user.id)
		require.NoError(t, err)
		tokens[user.role] = user.token
	}

	cfg := &config.Config{
		Port:         "8080",
		DatabaseURL:  "ignored",
		AdminJWKSURL: "http://test.invalid/jwks",
		CORSOrigins:  "*",
	}
	ts := httptest.NewServer(server.New(cfg, pool))
	t.Cleanup(ts.Close)
	return ts, pool, tokens
}

func thenNowAuth(token string) func(*http.Request) {
	return func(req *http.Request) {
		req.Header.Set("Authorization", "Bearer "+token)
	}
}

func thenNowSiteBody(name, slug string) map[string]any {
	return map[string]any{
		"name":              name,
		"slug":              slug,
		"county":            "Nairobi",
		"locality":          "Nairobi",
		"siteType":          "historical",
		"accessMode":        "camera_video",
		"sensitivity":       "standard",
		"managingAuthority": "National Museums of Kenya",
		"photographyPolicy": "Allowed with attribution",
		"culturalAuthority": "National Museums of Kenya",
		"consentRecord":     "Community review recorded",
		"consentDate":       "2026-09-12",
		"siteRules":         "Stay behind the marked line.",
		"safetyNotes":       "The yard is uneven.",
		"shortDescription":  "The railway that built Nairobi.",
		"fullDescription":   "A current-scene portrait.",
		"sources":           "NMK labels",
		"factCheck":         "Signed off",
		"publicRightsLine":  "Footage used with permission.",
		"latitude":          -1.293613,
		"longitude":         36.822641,
		"geofenceRadiusM":   150,
		"standingPointNote": "Main yard entrance.",
	}
}

func thenNowSnapshot() map[string]any {
	return map[string]any{
		"title":               "The railway that built Nairobi",
		"promise":             "See the museum yard, the locomotives, and the workshop as they are today.",
		"story":               "The Uganda Railway reached Nairobi in 1899. This is a current-scene portrait.",
		"period":              "1899–1971",
		"duration":            77,
		"contentNotes":        "Current-scene footage only.",
		"safetyNotes":         "Stay behind the marked line.",
		"sources":             "NMK labels",
		"factCheck":           "Signed off",
		"primaryMode":         "camera_video",
		"languages":           []string{"EN", "SW"},
		"rightsRecord":        "Client footage, cleared",
		"rightsEvidence":      "Signed release",
		"rightsExpiry":        "2027-06-30",
		"consentRecord":       "Community review recorded",
		"sensitivity":         "standard",
		"videoMaster":         "then-now/railway/master.mp4",
		"videoSize":           "1.4 GB",
		"renditions":          map[string]string{"1080": "Ready", "720": "Ready", "540": "Ready"},
		"renditionKeys":       map[string]string{"1080": "then-now/railway/1080.mp4", "720": "then-now/railway/720.mp4", "540": "then-now/railway/540.mp4"},
		"poster":              true,
		"posterKey":           "then-now/railway/poster.jpg",
		"captions":            map[string]bool{"en": true, "sw": true},
		"captionKeys":         map[string]string{"en": "then-now/railway/en.vtt", "sw": "then-now/railway/sw.vtt"},
		"transcript":          true,
		"transcriptKey":       "then-now/railway/transcript.txt",
		"audioDescription":    true,
		"audioDescriptionKey": "then-now/railway/ad.vtt",
		"textCards": []map[string]any{
			{"at": 6, "text": "1899 — the Uganda Railway reaches Nairobi."},
			{"at": 22, "text": "The railway helped Nairobi grow."},
		},
	}
}

func TestThenNowAuthorReviewPublishFlow(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}
	ts, pool, tokens := setupThenNowTest(t)
	editor := thenNowAuth(tokens["content_editor"])
	admin := thenNowAuth(tokens["administrator"])
	superAdmin := thenNowAuth(tokens["super_admin"])
	moderator := thenNowAuth(tokens["moderator"])

	siteResp := doJSON(t, ts, "POST", "/v1/then-now/sites", thenNowSiteBody("Nairobi Railway Museum", "nairobi-railway-museum"), editor)
	require.Equal(t, http.StatusCreated, siteResp.StatusCode)
	site := decodeBody(t, siteResp)
	siteID := site["id"].(string)
	require.NotEmpty(t, siteID)
	assert.Equal(t, "draft", site["status"])

	// A moderator can read but not write.
	listResp := doJSON(t, ts, "GET", "/v1/then-now/sites", nil, moderator)
	assert.Equal(t, http.StatusOK, listResp.StatusCode)
	listResp.Body.Close()
	modResp := doJSON(t, ts, "POST", "/v1/then-now/sites", thenNowSiteBody("Blocked Site", "blocked-site"), moderator)
	assert.Equal(t, http.StatusForbidden, modResp.StatusCode)
	modResp.Body.Close()

	// Create the experience with no media, then complete the draft.
	createResp := doJSON(t, ts, "POST", "/v1/then-now/experiences", map[string]any{
		"siteId": siteID,
		"title":  "The railway that built Nairobi",
	}, editor)
	require.Equal(t, http.StatusCreated, createResp.StatusCode)
	created := decodeBody(t, createResp)
	experienceID := created["id"].(string)
	require.NotEmpty(t, experienceID)
	readiness := created["readiness"].(map[string]any)
	assert.Equal(t, false, readiness["readyToSubmit"])

	patchResp := doJSON(t, ts, "PATCH", "/v1/then-now/experiences/"+experienceID, map[string]any{
		"snapshot": thenNowSnapshot(),
	}, editor)
	require.Equal(t, http.StatusOK, patchResp.StatusCode)
	patched := decodeBody(t, patchResp)
	readiness = patched["readiness"].(map[string]any)
	assert.Equal(t, true, readiness["readyToSubmit"])

	// The editor submits, but cannot publish.
	submitResp := doJSON(t, ts, "POST", "/v1/then-now/experiences/"+experienceID+"/submit", nil, editor)
	require.Equal(t, http.StatusOK, submitResp.StatusCode)
	submitted := decodeBody(t, submitResp)
	assert.Equal(t, "in_review", submitted["openRevision"].(map[string]any)["status"])
	editorPublish := doJSON(t, ts, "POST", "/v1/then-now/experiences/"+experienceID+"/publish", nil, editor)
	assert.Equal(t, http.StatusForbidden, editorPublish.StatusCode)
	editorPublish.Body.Close()

	// The queue contains the frozen revision; an admin assigns and approves.
	queueResp := doJSON(t, ts, "GET", "/v1/then-now/review?filter=all", nil, admin)
	require.Equal(t, http.StatusOK, queueResp.StatusCode)
	queue := decodeBody(t, queueResp)
	assert.Len(t, queue["items"], 1)

	assignResp := doJSON(t, ts, "POST", "/v1/then-now/experiences/"+experienceID+"/assign", nil, admin)
	require.Equal(t, http.StatusOK, assignResp.StatusCode)
	assigned := decodeBody(t, assignResp)
	assert.Equal(t, "tn-admin", assigned["openRevision"].(map[string]any)["reviewerId"])

	// Reviewer ≠ uploader / author.
	selfAssign := doJSON(t, ts, "POST", "/v1/then-now/experiences/"+experienceID+"/assign", map[string]string{"reviewerId": "tn-editor"}, admin)
	assert.Equal(t, http.StatusConflict, selfAssign.StatusCode)
	selfAssign.Body.Close()

	approveResp := doJSON(t, ts, "POST", "/v1/then-now/experiences/"+experienceID+"/decisions", map[string]string{"decision": "approve", "notes": "Ready."}, admin)
	require.Equal(t, http.StatusOK, approveResp.StatusCode)
	approved := decodeBody(t, approveResp)
	assert.Equal(t, true, approved["readiness"].(map[string]any)["readyToPublish"])
	assert.Equal(t, "approved", approved["openRevision"].(map[string]any)["status"])

	publishResp := doJSON(t, ts, "POST", "/v1/then-now/experiences/"+experienceID+"/publish", nil, admin)
	require.Equal(t, http.StatusOK, publishResp.StatusCode)
	published := decodeBody(t, publishResp)
	assert.Equal(t, "published", published["status"])
	assert.Equal(t, float64(1), published["liveRevisionNumber"])

	sitePublish := doJSON(t, ts, "POST", "/v1/then-now/sites/"+siteID+"/publish", nil, admin)
	require.Equal(t, http.StatusOK, sitePublish.StatusCode)
	sitePublish.Body.Close()

	mobileResp := doJSON(t, ts, "GET", "/v1/mobile/then-now/sites", nil, nil)
	require.Equal(t, http.StatusOK, mobileResp.StatusCode)
	mobile := decodeBody(t, mobileResp)
	require.Len(t, mobile["items"], 1)
	mobileSite := mobile["items"].([]any)[0].(map[string]any)
	assert.Equal(t, "Nairobi Railway Museum", mobileSite["name"])
	assert.Equal(t, "The railway that built Nairobi", mobileSite["experience"].(map[string]any)["title"])

	publicResp := doJSON(t, ts, "GET", "/v1/public/then-now/sites/nairobi-railway-museum", nil, nil)
	require.Equal(t, http.StatusOK, publicResp.StatusCode)
	public := decodeBody(t, publicResp)
	assert.Equal(t, "Nairobi Railway Museum", public["site"].(map[string]any)["name"])

	// A new revision stays staged: the live version keeps serving.
	newRevision := doJSON(t, ts, "POST", "/v1/then-now/experiences/"+experienceID+"/new-revision", nil, admin)
	require.Equal(t, http.StatusCreated, newRevision.StatusCode)
	staged := decodeBody(t, newRevision)
	assert.Equal(t, "published", staged["status"])
	assert.Equal(t, "draft", staged["openRevision"].(map[string]any)["status"])
	assert.Equal(t, float64(1), staged["liveRevisionNumber"])

	submitV2 := doJSON(t, ts, "POST", "/v1/then-now/experiences/"+experienceID+"/submit", nil, editor)
	require.Equal(t, http.StatusOK, submitV2.StatusCode)
	submittedV2 := decodeBody(t, submitV2)
	assert.Equal(t, "published", submittedV2["status"], "submitting a replacement must not hide the live version")

	assignV2 := doJSON(t, ts, "POST", "/v1/then-now/experiences/"+experienceID+"/assign", map[string]string{"reviewerId": "tn-super"}, admin)
	require.Equal(t, http.StatusOK, assignV2.StatusCode)
	assignV2.Body.Close()
	approveV2 := doJSON(t, ts, "POST", "/v1/then-now/experiences/"+experienceID+"/decisions", map[string]string{"decision": "approve", "notes": "Replacement approved."}, superAdmin)
	require.Equal(t, http.StatusOK, approveV2.StatusCode)
	approveV2.Body.Close()

	scheduleResp := doJSON(t, ts, "POST", "/v1/then-now/experiences/"+experienceID+"/schedule", map[string]any{
		"scheduledAt": time.Now().Add(24 * time.Hour).UTC().Format(time.RFC3339),
		"note":        "Morning release",
	}, admin)
	require.Equal(t, http.StatusOK, scheduleResp.StatusCode)
	scheduled := decodeBody(t, scheduleResp)
	assert.Equal(t, "published", scheduled["status"])
	assert.NotEmpty(t, scheduled["openRevision"].(map[string]any)["scheduledAt"])

	// Move the schedule into the past and let the scheduler promote it.
	_, err := pool.Exec(context.Background(), `
		UPDATE then_now_revisions SET scheduled_at = now() - interval '1 minute'
		WHERE experience_id = $1 AND revision_number = 2`, experienceID)
	require.NoError(t, err)
	count, err := thennow.PublishDue(context.Background(), pool)
	require.NoError(t, err)
	assert.Equal(t, 1, count)

	detailResp := doJSON(t, ts, "GET", "/v1/then-now/experiences/"+experienceID, nil, admin)
	require.Equal(t, http.StatusOK, detailResp.StatusCode)
	detail := decodeBody(t, detailResp)
	assert.Equal(t, "published", detail["status"])
	assert.Equal(t, float64(2), detail["liveRevisionNumber"])
}

func TestThenNowSensitiveReviewNeedsSecondReviewer(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}
	ts, _, tokens := setupThenNowTest(t)
	editor := thenNowAuth(tokens["content_editor"])
	admin := thenNowAuth(tokens["administrator"])

	siteResp := doJSON(t, ts, "POST", "/v1/then-now/sites", thenNowSiteBody("Nairobi War Cemetery", "nairobi-war-cemetery"), editor)
	require.Equal(t, http.StatusCreated, siteResp.StatusCode)
	siteID := decodeBody(t, siteResp)["id"].(string)

	snapshot := thenNowSnapshot()
	snapshot["title"] = "A place of remembrance"
	snapshot["sensitivity"] = "sensitive"
	createResp := doJSON(t, ts, "POST", "/v1/then-now/experiences", map[string]any{
		"siteId": siteID, "title": "A place of remembrance", "snapshot": snapshot,
	}, editor)
	require.Equal(t, http.StatusCreated, createResp.StatusCode)
	experienceID := decodeBody(t, createResp)["id"].(string)

	submitResp := doJSON(t, ts, "POST", "/v1/then-now/experiences/"+experienceID+"/submit", nil, editor)
	require.Equal(t, http.StatusOK, submitResp.StatusCode)
	assignResp := doJSON(t, ts, "POST", "/v1/then-now/experiences/"+experienceID+"/assign", nil, admin)
	require.Equal(t, http.StatusOK, assignResp.StatusCode)
	assignResp.Body.Close()

	blocked := doJSON(t, ts, "POST", "/v1/then-now/experiences/"+experienceID+"/decisions", map[string]string{"decision": "approve"}, admin)
	assert.Equal(t, http.StatusConflict, blocked.StatusCode)
	blocked.Body.Close()

	external := doJSON(t, ts, "POST", "/v1/then-now/experiences/"+experienceID+"/external-reviewer", map[string]string{
		"name": "Wanjiku M.", "org": "Community reviewer", "decision": "approved",
	}, admin)
	require.Equal(t, http.StatusOK, external.StatusCode)
	external.Body.Close()

	approved := doJSON(t, ts, "POST", "/v1/then-now/experiences/"+experienceID+"/decisions", map[string]string{"decision": "approve", "notes": "Custodian confirmed."}, admin)
	require.Equal(t, http.StatusOK, approved.StatusCode)
	approved.Body.Close()
}

func TestThenNowReviewerCannotReviewOwnRevision(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}
	ts, _, tokens := setupThenNowTest(t)
	admin := thenNowAuth(tokens["administrator"])
	editor := thenNowAuth(tokens["content_editor"])

	siteResp := doJSON(t, ts, "POST", "/v1/then-now/sites", thenNowSiteBody("Reviewer Site", "reviewer-site"), editor)
	require.Equal(t, http.StatusCreated, siteResp.StatusCode)
	siteID := decodeBody(t, siteResp)["id"].(string)

	createResp := doJSON(t, ts, "POST", "/v1/then-now/experiences", map[string]any{
		"siteId": siteID, "title": "Admin-created experience", "snapshot": thenNowSnapshot(),
	}, admin)
	require.Equal(t, http.StatusCreated, createResp.StatusCode)
	experienceID := decodeBody(t, createResp)["id"].(string)
	submitResp := doJSON(t, ts, "POST", "/v1/then-now/experiences/"+experienceID+"/submit", nil, admin)
	require.Equal(t, http.StatusOK, submitResp.StatusCode)
	submitResp.Body.Close()

	assignResp := doJSON(t, ts, "POST", "/v1/then-now/experiences/"+experienceID+"/assign", nil, admin)
	assert.Equal(t, http.StatusConflict, assignResp.StatusCode)
	assignResp.Body.Close()
}
