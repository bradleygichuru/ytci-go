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
	"github.com/bradleygichuru/ytci-go/internal/handler/admin"
	"github.com/bradleygichuru/ytci-go/internal/server"
)

type audioPublishTestAuth struct {
	editor    func(*http.Request)
	reviewer  func(*http.Request)
	reviewer2 func(*http.Request)
	super     func(*http.Request)
	moderator func(*http.Request)
}

type audioPublishTestEnv struct {
	ts   *httptest.Server
	pool *pgxpool.Pool
	auth audioPublishTestAuth
}

func setupAudioPublishTest(t *testing.T) *audioPublishTestEnv {
	t.Helper()
	ctx := context.Background()
	pool := db.SetupTestDB(t)
	t.Cleanup(pool.Close)

	_, err := pool.Exec(ctx, `
		CREATE TABLE IF NOT EXISTS users (
			id TEXT PRIMARY KEY,
			name TEXT NOT NULL,
			email TEXT NOT NULL UNIQUE,
			email_verified BOOLEAN DEFAULT false NOT NULL,
			image TEXT,
			created_at TIMESTAMP NOT NULL,
			updated_at TIMESTAMP NOT NULL,
			role TEXT,
			banned BOOLEAN DEFAULT false,
			ban_reason TEXT,
			ban_expires TIMESTAMP
		)`)
	require.NoError(t, err)
	_, err = pool.Exec(ctx, `
		CREATE TABLE IF NOT EXISTS sessions (
			id TEXT PRIMARY KEY,
			expires_at TIMESTAMP NOT NULL,
			token TEXT NOT NULL UNIQUE,
			created_at TIMESTAMP NOT NULL,
			updated_at TIMESTAMP NOT NULL,
			user_id TEXT NOT NULL,
			ip_address TEXT,
			user_agent TEXT,
			impersonated_by TEXT
		)`)
	require.NoError(t, err)

	_, err = pool.Exec(ctx,
		`INSERT INTO users (id,name,email,created_at,updated_at,role) VALUES
		 ('audio-editor','Audio Editor','audio-editor@test.com',now(),now(),'content_editor'),
		 ('audio-reviewer','Reviewer One','audio-reviewer@test.com',now(),now(),'administrator'),
		 ('audio-reviewer-2','Reviewer Two','audio-reviewer2@test.com',now(),now(),'administrator'),
		 ('audio-super','Super Admin','audio-super@test.com',now(),now(),'super_admin'),
		 ('audio-moderator','Audio Moderator','audio-moderator@test.com',now(),now(),'moderator')`)
	require.NoError(t, err)
	_, err = pool.Exec(ctx,
		`INSERT INTO sessions (id,expires_at,token,created_at,updated_at,user_id) VALUES
		 ('sess-audio-editor',now()+interval '1 day','audio-token-editor',now(),now(),'audio-editor'),
		 ('sess-audio-reviewer',now()+interval '1 day','audio-token-reviewer',now(),now(),'audio-reviewer'),
		 ('sess-audio-reviewer-2',now()+interval '1 day','audio-token-reviewer-2',now(),now(),'audio-reviewer-2'),
		 ('sess-audio-super',now()+interval '1 day','audio-token-super',now(),now(),'audio-super'),
		 ('sess-audio-moderator',now()+interval '1 day','audio-token-moderator',now(),now(),'audio-moderator')`)
	require.NoError(t, err)

	cfg := &config.Config{
		Port:         "8080",
		DatabaseURL:  "ignored",
		AdminJWKSURL: "http://test.invalid/jwks",
		CORSOrigins:  "*",
	}
	ts := httptest.NewServer(server.New(cfg, pool))
	t.Cleanup(ts.Close)

	bearer := func(token string) func(*http.Request) {
		return func(req *http.Request) { req.Header.Set("Authorization", "Bearer "+token) }
	}
	return &audioPublishTestEnv{
		ts:   ts,
		pool: pool,
		auth: audioPublishTestAuth{
			editor:    bearer("audio-token-editor"),
			reviewer:  bearer("audio-token-reviewer"),
			reviewer2: bearer("audio-token-reviewer-2"),
			super:     bearer("audio-token-super"),
			moderator: bearer("audio-token-moderator"),
		},
	}
}

func audioCall(t *testing.T, env *audioPublishTestEnv, method, path string, body any, auth func(*http.Request)) (int, map[string]any) {
	t.Helper()
	resp := doJSON(t, env.ts, method, path, body, auth)
	return resp.StatusCode, decodeBody(t, resp)
}

func audioSeedCollection(t *testing.T, pool *pgxpool.Pool, slug, status string) string {
	t.Helper()
	var id string
	require.NoError(t, pool.QueryRow(context.Background(),
		`INSERT INTO audio_story_collections (slug,title,description,status)
		 VALUES ($1,$2,'A test collection.',$3) RETURNING id`,
		slug, "Collection "+slug, status).Scan(&id))
	return id
}

func audioSeedMedia(t *testing.T, pool *pgxpool.Pool, key, mediaType string) string {
	t.Helper()
	var id string
	require.NoError(t, pool.QueryRow(context.Background(),
		`INSERT INTO media_assets (entity_type,entity_id,object_key,type,status)
		 VALUES ('audio_story',NULL,$1,$2,'ready') RETURNING id`, key, mediaType).Scan(&id))
	return id
}

func audioCreateReadyStory(t *testing.T, env *audioPublishTestEnv, slug, riskTier, collectionID, coverID, audioID string) (string, string) {
	t.Helper()
	status, created := audioCall(t, env, "POST", "/v1/audio-stories", map[string]any{
		"slug":            slug,
		"title":           "Story " + slug,
		"summary":         "A ready audio story for integration tests.",
		"theme":           "nature_wildlife",
		"coverMediaId":    coverID,
		"producer":        "YTCI",
		"communitySource": "Coastal communities",
		"collectionId":    collectionID,
		"placeScope":      "county",
		"counties":        []string{"Kilifi"},
		"rightsBasis":     "ytci_owned",
		"rightsHolder":    "YTCI",
		"riskTier":        riskTier,
	}, env.auth.editor)
	require.Equal(t, http.StatusCreated, status, created)
	storyID, _ := created["id"].(string)
	revisionID, _ := created["revisionId"].(string)
	require.NotEmpty(t, storyID)
	require.NotEmpty(t, revisionID)

	status, body := audioCall(t, env, "POST",
		"/v1/audio-stories/"+storyID+"/revisions/"+revisionID+"/tracks", map[string]any{
			"language":                 "en",
			"mediaAssetId":             audioID,
			"audioObjectKey":           "audio-stories/" + slug + "-en.m4a",
			"durationSeconds":          720,
			"narrator":                 "Amina Yusuf",
			"narratorConsentDate":      "2026-10-01",
			"narratorConsentReference": "REL-2026-014",
			"format":                   "m4a",
			"bitrateKbps":              96,
			"sizeBytes":                18432000,
		}, env.auth.editor)
	require.Equal(t, http.StatusOK, status, body)

	status, body = audioCall(t, env, "PUT",
		"/v1/audio-stories/"+storyID+"/revisions/"+revisionID+"/transcripts/en", map[string]any{
			"paragraphs": []map[string]any{
				{"text": "The mangrove keeps the coast.", "startSeconds": 0},
				{"text": "The elders replant it.", "startSeconds": 680},
			},
		}, env.auth.editor)
	require.Equal(t, http.StatusOK, status, body)

	status, body = audioCall(t, env, "POST",
		"/v1/audio-stories/"+storyID+"/revisions/"+revisionID+"/submit", nil, env.auth.editor)
	require.Equal(t, http.StatusOK, status, body)
	return storyID, revisionID
}

func audioApprove(t *testing.T, env *audioPublishTestEnv, storyID, revisionID string, auth func(*http.Request)) map[string]any {
	t.Helper()
	status, body := audioCall(t, env, "POST",
		"/v1/audio-stories/"+storyID+"/revisions/"+revisionID+"/review",
		map[string]any{"decision": "approve", "notes": "Reviewed."}, auth)
	require.Equal(t, http.StatusOK, status, body)
	return body
}

func audioItemCount(body map[string]any) int {
	items, _ := body["items"].([]any)
	return len(items)
}

func TestAudioReviewSeparationAndQueue(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}
	env := setupAudioPublishTest(t)
	collectionID := audioSeedCollection(t, env.pool, "review-collection", "published")
	coverID := audioSeedMedia(t, env.pool, "audio-stories/covers/review.jpg", "image")
	audioID := audioSeedMedia(t, env.pool, "audio-stories/review-en.m4a", "audio")
	storyID, revisionID := audioCreateReadyStory(t, env, "review-story", "standard", collectionID, coverID, audioID)

	// The queue is readable by moderators and counts the submitted revision.
	status, queue := audioCall(t, env, "GET", "/v1/audio-stories/review-queue", nil, env.auth.moderator)
	require.Equal(t, http.StatusOK, status, queue)
	require.Equal(t, 1, audioItemCount(queue))
	counts := queue["counts"].(map[string]any)
	assert.Equal(t, float64(1), counts["unassigned"])
	item := queue["items"].([]any)[0].(map[string]any)
	assert.Equal(t, storyID, item["storyId"])
	assert.Equal(t, revisionID, item["revisionId"])
	assert.Equal(t, "standard", item["riskTier"])
	assert.Equal(t, []any{"en"}, item["languages"])

	// Only administrators may assign, and the uploader cannot review.
	status, body := audioCall(t, env, "POST",
		"/v1/audio-stories/"+storyID+"/revisions/"+revisionID+"/assign",
		map[string]any{"reviewerId": "audio-reviewer"}, env.auth.editor)
	assert.Equal(t, http.StatusForbidden, status, body)

	status, body = audioCall(t, env, "POST",
		"/v1/audio-stories/"+storyID+"/revisions/"+revisionID+"/assign",
		map[string]any{"reviewerId": "audio-editor"}, env.auth.reviewer)
	assert.Equal(t, http.StatusForbidden, status, body)
	assert.Equal(t, "SEPARATION_OF_DUTIES", body["error"].(map[string]any)["code"])

	status, body = audioCall(t, env, "POST",
		"/v1/audio-stories/"+storyID+"/revisions/"+revisionID+"/assign",
		map[string]any{"reviewerId": "audio-reviewer"}, env.auth.reviewer)
	require.Equal(t, http.StatusOK, status, body)

	status, mine := audioCall(t, env, "GET", "/v1/audio-stories/review-queue?status=mine", nil, env.auth.reviewer)
	require.Equal(t, http.StatusOK, status, mine)
	require.Equal(t, 1, audioItemCount(mine))
	assert.Equal(t, "Reviewer One", mine["items"].([]any)[0].(map[string]any)["assignedReviewerName"])

	status, body = audioCall(t, env, "POST",
		"/v1/audio-stories/"+storyID+"/revisions/"+revisionID+"/recuse",
		map[string]any{"reason": "Conflict of interest."}, env.auth.reviewer)
	require.Equal(t, http.StatusOK, status, body)

	status, queue = audioCall(t, env, "GET", "/v1/audio-stories/review-queue?status=unassigned", nil, env.auth.reviewer)
	require.Equal(t, http.StatusOK, status, queue)
	require.Equal(t, 1, audioItemCount(queue))

	status, body = audioCall(t, env, "POST",
		"/v1/audio-stories/"+storyID+"/revisions/"+revisionID+"/assign",
		map[string]any{"reviewerId": "audio-reviewer"}, env.auth.reviewer)
	require.Equal(t, http.StatusOK, status, body)

	status, reviews := audioCall(t, env, "GET",
		"/v1/audio-stories/"+storyID+"/revisions/"+revisionID+"/reviews", nil, env.auth.reviewer)
	require.Equal(t, http.StatusOK, status, reviews)
	assert.Equal(t, float64(0), reviews["approvals"])
	assert.Equal(t, float64(1), reviews["requiredApprovals"])

	approved := audioApprove(t, env, storyID, revisionID, env.auth.reviewer)
	assert.Equal(t, "approved", approved["status"])

	// One decision per reviewer includes repeat approvals on the same revision.
	status, body = audioCall(t, env, "POST",
		"/v1/audio-stories/"+storyID+"/revisions/"+revisionID+"/review",
		map[string]any{"decision": "approve", "notes": "Approving twice."}, env.auth.reviewer)
	assert.Equal(t, http.StatusConflict, status, body)
	assert.Equal(t, "REVIEW_CONFLICT", body["error"].(map[string]any)["code"])

	status, reviews = audioCall(t, env, "GET",
		"/v1/audio-stories/"+storyID+"/revisions/"+revisionID+"/reviews", nil, env.auth.moderator)
	require.Equal(t, http.StatusOK, status, reviews)
	assert.Equal(t, float64(1), reviews["approvals"])
	require.Equal(t, 1, audioItemCount(reviews))
	assert.Equal(t, "Reviewer One", reviews["items"].([]any)[0].(map[string]any)["reviewerName"])

	// Withdrawal is an author action on an in-review revision only.
	status, body = audioCall(t, env, "POST",
		"/v1/audio-stories/"+storyID+"/revisions/"+revisionID+"/withdraw",
		map[string]any{"reason": "Too late."}, env.auth.editor)
	assert.Equal(t, http.StatusConflict, status, body)

	story2, revision2 := audioCreateReadyStory(t, env, "withdraw-story", "standard", collectionID, coverID, audioID)
	status, body = audioCall(t, env, "POST",
		"/v1/audio-stories/"+story2+"/revisions/"+revision2+"/withdraw",
		map[string]any{"reason": "Rights clarification needed."}, env.auth.editor)
	require.Equal(t, http.StatusOK, status, body)
	assert.Equal(t, "draft", body["status"])

	status, detail := audioCall(t, env, "GET", "/v1/audio-stories/"+story2, nil, env.auth.editor)
	require.Equal(t, http.StatusOK, status, detail)
	assert.Equal(t, "draft", detail["openRevision"].(map[string]any)["status"])
	assert.Equal(t, "draft", detail["story"].(map[string]any)["status"])

	status, audit := audioCall(t, env, "GET", "/v1/audio-stories/"+story2+"/audit", nil, env.auth.editor)
	require.Equal(t, http.StatusOK, status, audit)
	actions := map[string]bool{}
	for _, raw := range audit["items"].([]any) {
		actions[raw.(map[string]any)["action"].(string)] = true
	}
	assert.True(t, actions["audio_story.revision_withdrawn"])
}

func TestAudioSensitiveSecondReviewerAndRestricted(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}
	env := setupAudioPublishTest(t)
	collectionID := audioSeedCollection(t, env.pool, "sensitive-collection", "published")
	coverID := audioSeedMedia(t, env.pool, "audio-stories/covers/sensitive.jpg", "image")
	audioID := audioSeedMedia(t, env.pool, "audio-stories/sensitive-en.m4a", "audio")
	storyID, revisionID := audioCreateReadyStory(t, env, "sensitive-story", "sensitive", collectionID, coverID, audioID)

	status, body := audioCall(t, env, "POST",
		"/v1/audio-stories/"+storyID+"/revisions/"+revisionID+"/assign",
		map[string]any{"reviewerId": "audio-reviewer", "secondReviewerId": "audio-reviewer-2"}, env.auth.reviewer)
	require.Equal(t, http.StatusOK, status, body)

	first := audioApprove(t, env, storyID, revisionID, env.auth.reviewer)
	assert.Equal(t, "in_review", first["status"])
	assert.Equal(t, true, first["needsSecondReviewer"])
	assert.Equal(t, float64(1), first["approvals"])
	assert.Equal(t, float64(2), first["requiredApprovals"])

	status, body = audioCall(t, env, "POST",
		"/v1/audio-stories/"+storyID+"/revisions/"+revisionID+"/review",
		map[string]any{"decision": "approve", "notes": "Same reviewer again."}, env.auth.reviewer)
	assert.Equal(t, http.StatusConflict, status, body)
	assert.Equal(t, "REVIEW_CONFLICT", body["error"].(map[string]any)["code"])

	status, body = audioCall(t, env, "POST",
		"/v1/audio-stories/"+storyID+"/revisions/"+revisionID+"/publish", nil, env.auth.reviewer)
	assert.Equal(t, http.StatusConflict, status, body)
	assert.Equal(t, "SECOND_REVIEW_REQUIRED", body["error"].(map[string]any)["code"])

	second := audioApprove(t, env, storyID, revisionID, env.auth.reviewer2)
	assert.Equal(t, "approved", second["status"])
	assert.Equal(t, false, second["needsSecondReviewer"])
	assert.Equal(t, float64(2), second["approvals"])

	status, body = audioCall(t, env, "POST",
		"/v1/audio-stories/"+storyID+"/revisions/"+revisionID+"/publish", nil, env.auth.reviewer)
	require.Equal(t, http.StatusOK, status, body)
	assert.Equal(t, float64(1), body["contentVersion"])

	// Restricted stories cannot be approved, but a reviewer can send them back.
	restricted, restrictedRevision := audioCreateReadyStory(t, env, "restricted-story", "restricted", collectionID, coverID, audioID)
	status, body = audioCall(t, env, "POST",
		"/v1/audio-stories/"+restricted+"/revisions/"+restrictedRevision+"/review",
		map[string]any{"decision": "approve", "notes": "Attempt approval."}, env.auth.reviewer)
	assert.Equal(t, http.StatusForbidden, status, body)
	assert.Equal(t, "RESTRICTED_REVIEW", body["error"].(map[string]any)["code"])

	status, body = audioCall(t, env, "POST",
		"/v1/audio-stories/"+restricted+"/revisions/"+restrictedRevision+"/review",
		map[string]any{"decision": "request_changes", "notes": "Needs legal sign-off before publication."}, env.auth.reviewer)
	require.Equal(t, http.StatusOK, status, body)
	assert.Equal(t, "draft", body["status"])

	status, detail := audioCall(t, env, "GET", "/v1/audio-stories/"+restricted, nil, env.auth.reviewer)
	require.Equal(t, http.StatusOK, status, detail)
	assert.Equal(t, "draft", detail["openRevision"].(map[string]any)["status"])

	// Missing stories/revisions are 404s rather than generic conflicts.
	status, _ = audioCall(t, env, "POST",
		"/v1/audio-stories/00000000-0000-0000-0000-000000000000/revisions/"+restrictedRevision+"/publish", nil, env.auth.reviewer)
	assert.Equal(t, http.StatusNotFound, status)
}

func TestAudioPublishVersioningAndSchedule(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}
	env := setupAudioPublishTest(t)
	collectionID := audioSeedCollection(t, env.pool, "versioned-collection", "draft")
	coverID := audioSeedMedia(t, env.pool, "audio-stories/covers/versioned.jpg", "image")
	audioID := audioSeedMedia(t, env.pool, "audio-stories/versioned-en.m4a", "audio")

	storyID, revisionID := audioCreateReadyStory(t, env, "versioned-story", "standard", collectionID, coverID, audioID)
	audioApprove(t, env, storyID, revisionID, env.auth.reviewer)

	// Publication is blocked until the collection itself is published.
	status, body := audioCall(t, env, "POST",
		"/v1/audio-stories/"+storyID+"/revisions/"+revisionID+"/publish", nil, env.auth.reviewer)
	assert.Equal(t, http.StatusConflict, status, body)
	assert.Equal(t, "COLLECTION_NOT_PUBLISHED", body["error"].(map[string]any)["code"])

	// A later readiness failure surfaces the full readiness report.
	_, err := env.pool.Exec(context.Background(),
		`UPDATE audio_stories SET cover_media_id=NULL WHERE id=$1`, storyID)
	require.NoError(t, err)
	status, body = audioCall(t, env, "POST",
		"/v1/audio-stories/"+storyID+"/revisions/"+revisionID+"/publish", nil, env.auth.reviewer)
	assert.Equal(t, http.StatusUnprocessableEntity, status, body)
	assert.Equal(t, "READINESS_BLOCKED", body["error"].(map[string]any)["code"])
	assert.Equal(t, false, body["readiness"].(map[string]any)["ready"])
	_, err = env.pool.Exec(context.Background(),
		`UPDATE audio_stories SET cover_media_id=$2 WHERE id=$1`, storyID, coverID)
	require.NoError(t, err)

	status, body = audioCall(t, env, "POST",
		"/v1/audio-story-collections/"+collectionID+"/publish", nil, env.auth.reviewer)
	require.Equal(t, http.StatusOK, status, body)

	status, body = audioCall(t, env, "POST",
		"/v1/audio-stories/"+storyID+"/revisions/"+revisionID+"/publish", nil, env.auth.reviewer)
	require.Equal(t, http.StatusOK, status, body)
	assert.Equal(t, float64(1), body["contentVersion"])
	assert.Equal(t, true, body["contentChanged"])

	// A metadata/placement-only revision keeps the content version.
	status, body = audioCall(t, env, "POST", "/v1/audio-stories/"+storyID+"/revisions", map[string]any{}, env.auth.editor)
	require.Equal(t, http.StatusCreated, status, body)
	secondRevision, _ := body["revisionId"].(string)
	require.NotEmpty(t, secondRevision)
	status, body = audioCall(t, env, "POST",
		"/v1/audio-stories/"+storyID+"/revisions/"+secondRevision+"/submit", nil, env.auth.editor)
	require.Equal(t, http.StatusOK, status, body)
	audioApprove(t, env, storyID, secondRevision, env.auth.reviewer2)
	status, body = audioCall(t, env, "POST",
		"/v1/audio-stories/"+storyID+"/revisions/"+secondRevision+"/publish", nil, env.auth.reviewer)
	require.Equal(t, http.StatusOK, status, body)
	assert.Equal(t, float64(1), body["contentVersion"])
	assert.Equal(t, false, body["contentChanged"])

	// Changing the audio content increments the content version.
	status, body = audioCall(t, env, "POST", "/v1/audio-stories/"+storyID+"/revisions", map[string]any{}, env.auth.editor)
	require.Equal(t, http.StatusCreated, status, body)
	thirdRevision, _ := body["revisionId"].(string)
	require.NotEmpty(t, thirdRevision)
	status, body = audioCall(t, env, "POST",
		"/v1/audio-stories/"+storyID+"/revisions/"+thirdRevision+"/tracks", map[string]any{
			"language":                 "en",
			"mediaAssetId":             audioID,
			"audioObjectKey":           "audio-stories/versioned-story-en-v2.m4a",
			"durationSeconds":          730,
			"narrator":                 "Amina Yusuf",
			"narratorConsentDate":      "2026-10-01",
			"narratorConsentReference": "REL-2026-014",
			"format":                   "m4a",
			"bitrateKbps":              96,
			"sizeBytes":                18432000,
		}, env.auth.editor)
	require.Equal(t, http.StatusOK, status, body)
	status, body = audioCall(t, env, "POST",
		"/v1/audio-stories/"+storyID+"/revisions/"+thirdRevision+"/submit", nil, env.auth.editor)
	require.Equal(t, http.StatusOK, status, body)
	audioApprove(t, env, storyID, thirdRevision, env.auth.reviewer2)
	status, body = audioCall(t, env, "POST",
		"/v1/audio-stories/"+storyID+"/revisions/"+thirdRevision+"/publish", nil, env.auth.reviewer)
	require.Equal(t, http.StatusOK, status, body)
	assert.Equal(t, float64(2), body["contentVersion"])
	assert.Equal(t, true, body["contentChanged"])

	// Scheduling a revision of an already-published story keeps it published.
	status, body = audioCall(t, env, "POST", "/v1/audio-stories/"+storyID+"/revisions", map[string]any{}, env.auth.editor)
	require.Equal(t, http.StatusCreated, status, body)
	fourthRevision, _ := body["revisionId"].(string)
	require.NotEmpty(t, fourthRevision)
	status, body = audioCall(t, env, "POST",
		"/v1/audio-stories/"+storyID+"/revisions/"+fourthRevision+"/submit", nil, env.auth.editor)
	require.Equal(t, http.StatusOK, status, body)
	audioApprove(t, env, storyID, fourthRevision, env.auth.reviewer)
	future := time.Now().UTC().Add(time.Hour).Format(time.RFC3339)
	status, body = audioCall(t, env, "POST",
		"/v1/audio-stories/"+storyID+"/revisions/"+fourthRevision+"/schedule",
		map[string]any{"publishAt": future}, env.auth.reviewer)
	require.Equal(t, http.StatusOK, status, body)
	assert.Equal(t, "scheduled", body["status"])

	status, detail := audioCall(t, env, "GET", "/v1/audio-stories/"+storyID, nil, env.auth.editor)
	require.Equal(t, http.StatusOK, status, detail)
	assert.Equal(t, "published", detail["story"].(map[string]any)["status"])
	assert.Equal(t, "scheduled", detail["openRevision"].(map[string]any)["status"])

	status, body = audioCall(t, env, "POST",
		"/v1/audio-stories/"+storyID+"/revisions/"+fourthRevision+"/cancel-schedule",
		map[string]any{"reason": "Move the launch."}, env.auth.reviewer)
	require.Equal(t, http.StatusOK, status, body)
	assert.Equal(t, "approved", body["status"])
	assert.Equal(t, "published", body["storyStatus"])

	// On a never-published story, scheduling and cancelling move through scheduled/in_review.
	newStory, newRevision := audioCreateReadyStory(t, env, "first-schedule-story", "standard", collectionID, coverID, audioID)
	audioApprove(t, env, newStory, newRevision, env.auth.reviewer)
	status, body = audioCall(t, env, "POST",
		"/v1/audio-stories/"+newStory+"/revisions/"+newRevision+"/schedule",
		map[string]any{"publishAt": future}, env.auth.reviewer)
	require.Equal(t, http.StatusOK, status, body)

	status, detail = audioCall(t, env, "GET", "/v1/audio-stories/"+newStory, nil, env.auth.editor)
	require.Equal(t, http.StatusOK, status, detail)
	assert.Equal(t, "scheduled", detail["story"].(map[string]any)["status"])

	status, body = audioCall(t, env, "POST",
		"/v1/audio-stories/"+newStory+"/revisions/"+newRevision+"/cancel-schedule",
		map[string]any{"reason": "Launch postponed."}, env.auth.reviewer)
	require.Equal(t, http.StatusOK, status, body)
	assert.Equal(t, "in_review", body["storyStatus"])
}

func TestAudioPublicReadAndCollectionLifecycle(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}
	env := setupAudioPublishTest(t)

	// A draft collection with no stories can be deleted; one with a story cannot.
	status, body := audioCall(t, env, "POST", "/v1/audio-story-collections", map[string]any{
		"slug": "disposable", "title": "Disposable", "description": "Empty draft.",
	}, env.auth.editor)
	require.Equal(t, http.StatusCreated, status, body)
	disposableID, _ := body["id"].(string)
	status, body = audioCall(t, env, "DELETE", "/v1/audio-story-collections/"+disposableID, nil, env.auth.editor)
	require.Equal(t, http.StatusOK, status, body)

	status, body = audioCall(t, env, "POST", "/v1/audio-story-collections", map[string]any{
		"slug": "public-collection", "title": "Public Collection", "description": "Voices from the coast.",
	}, env.auth.editor)
	require.Equal(t, http.StatusCreated, status, body)
	collectionID, _ := body["id"].(string)
	require.NotEmpty(t, collectionID)

	coverID := audioSeedMedia(t, env.pool, "audio-stories/covers/public.jpg", "image")
	audioID := audioSeedMedia(t, env.pool, "audio-stories/public-en.m4a", "audio")
	storyID, revisionID := audioCreateReadyStory(t, env, "public-story", "standard", collectionID, coverID, audioID)

	// Publish is blocked while no story is approved.
	status, body = audioCall(t, env, "POST", "/v1/audio-story-collections/"+collectionID+"/publish", nil, env.auth.reviewer)
	assert.Equal(t, http.StatusConflict, status, body)
	assert.Equal(t, "COLLECTION_EMPTY", body["error"].(map[string]any)["code"])

	audioApprove(t, env, storyID, revisionID, env.auth.reviewer)

	// The collection is still draft, so nothing is public yet.
	resp := doJSON(t, env.ts, "GET", "/v1/public/audio-stories", nil, nil)
	require.Equal(t, http.StatusOK, resp.StatusCode)
	publicList := decodeBody(t, resp)
	assert.Equal(t, 0, audioItemCount(publicList))

	resp = doJSON(t, env.ts, "GET", "/v1/public/audio-stories/public-story", nil, nil)
	assert.Equal(t, http.StatusNotFound, resp.StatusCode)
	resp.Body.Close()

	status, body = audioCall(t, env, "POST", "/v1/audio-story-collections/"+collectionID+"/publish", nil, env.auth.reviewer)
	require.Equal(t, http.StatusOK, status, body)

	// The story itself has not been published yet.
	resp = doJSON(t, env.ts, "GET", "/v1/public/audio-stories", nil, nil)
	require.Equal(t, http.StatusOK, resp.StatusCode)
	assert.Equal(t, 0, audioItemCount(decodeBody(t, resp)))

	status, body = audioCall(t, env, "POST",
		"/v1/audio-stories/"+storyID+"/revisions/"+revisionID+"/publish", nil, env.auth.reviewer)
	require.Equal(t, http.StatusOK, status, body)

	resp = doJSON(t, env.ts, "GET", "/v1/public/audio-stories?language=en&county=Kilifi&q=public", nil, nil)
	require.Equal(t, http.StatusOK, resp.StatusCode)
	publicList = decodeBody(t, resp)
	require.Equal(t, 1, audioItemCount(publicList))
	publicItem := publicList["items"].([]any)[0].(map[string]any)
	assert.Equal(t, "public-story", publicItem["slug"])
	assert.Equal(t, "", publicItem["coverUrl"])
	assert.Equal(t, []any{"Kilifi"}, publicItem["counties"])
	assert.Equal(t, []any{"en"}, publicItem["languages"])
	require.NotNil(t, publicItem["publishedAt"])

	resp = doJSON(t, env.ts, "GET", "/v1/public/audio-stories/public-story", nil, nil)
	require.Equal(t, http.StatusOK, resp.StatusCode)
	publicDetail := decodeBody(t, resp)
	story := publicDetail["story"].(map[string]any)
	assert.Equal(t, "public-story", story["slug"])
	assert.Equal(t, "Public Collection", story["collection"].(map[string]any)["title"])
	assert.Equal(t, []any{"Kilifi"}, story["counties"])
	assert.NotEmpty(t, story["rightsLine"])
	require.Len(t, story["tracks"].([]any), 1)
	track := story["tracks"].([]any)[0].(map[string]any)
	assert.Equal(t, "", track["url"])
	require.Len(t, story["transcripts"].([]any), 1)
	assert.Equal(t, []any{}, publicDetail["related"])

	resp = doJSON(t, env.ts, "GET", "/v1/public/audio-story-collections", nil, nil)
	require.Equal(t, http.StatusOK, resp.StatusCode)
	collections := decodeBody(t, resp)
	require.Equal(t, 1, audioItemCount(collections))
	collection := collections["items"].([]any)[0].(map[string]any)
	assert.Equal(t, "public-collection", collection["slug"])
	assert.Equal(t, float64(1), collection["storyCount"])
	assert.Equal(t, float64(1), collection["publishedStoryCount"])

	resp = doJSON(t, env.ts, "GET", "/v1/public/audio-story-collections/public-collection", nil, nil)
	require.Equal(t, http.StatusOK, resp.StatusCode)
	collectionDetail := decodeBody(t, resp)
	assert.Equal(t, "Public Collection", collectionDetail["collection"].(map[string]any)["title"])
	require.Equal(t, 1, audioItemCount(map[string]any{"items": collectionDetail["stories"]}))

	// A draft story in the same collection is not exposed.
	draftStoryID, draftRevisionID := audioCreateReadyStory(t, env, "draft-only-story", "standard", collectionID, coverID, audioID)
	status, body = audioCall(t, env, "POST", "/v1/audio-story-collections/"+collectionID+"/stories",
		map[string]any{"storyIds": []string{storyID, draftStoryID}}, env.auth.editor)
	require.Equal(t, http.StatusOK, status, body)
	status, adminCollection := audioCall(t, env, "GET", "/v1/audio-story-collections/"+collectionID, nil, env.auth.editor)
	require.Equal(t, http.StatusOK, status, adminCollection)
	ordered := adminCollection["stories"].([]any)
	require.Len(t, ordered, 2)
	assert.Equal(t, storyID, ordered[0].(map[string]any)["id"])
	assert.Equal(t, draftStoryID, ordered[1].(map[string]any)["id"])

	status, detail := audioCall(t, env, "GET", "/v1/audio-stories/"+storyID, nil, env.auth.editor)
	require.Equal(t, http.StatusOK, status, detail)
	resp = doJSON(t, env.ts, "GET", "/v1/public/audio-stories/public-story", nil, nil)
	require.Equal(t, http.StatusOK, resp.StatusCode)
	resp.Body.Close()
	resp = doJSON(t, env.ts, "GET", "/v1/public/audio-story-collections/public-collection", nil, nil)
	require.Equal(t, http.StatusOK, resp.StatusCode)
	draftFiltered := decodeBody(t, resp)
	require.Equal(t, 1, audioItemCount(map[string]any{"items": draftFiltered["stories"]}))
	assert.Equal(t, float64(2), draftFiltered["collection"].(map[string]any)["storyCount"])
	assert.Equal(t, float64(1), draftFiltered["collection"].(map[string]any)["publishedStoryCount"])

	// Unpublishing hides the story, republishing restores it.
	status, body = audioCall(t, env, "POST", "/v1/audio-stories/"+storyID+"/unpublish",
		map[string]any{"reason": "Scheduled editorial review."}, env.auth.reviewer)
	require.Equal(t, http.StatusOK, status, body)
	resp = doJSON(t, env.ts, "GET", "/v1/public/audio-stories/public-story", nil, nil)
	assert.Equal(t, http.StatusNotFound, resp.StatusCode)
	resp.Body.Close()

	status, body = audioCall(t, env, "POST", "/v1/audio-stories/"+storyID+"/republish", nil, env.auth.reviewer)
	require.Equal(t, http.StatusOK, status, body)
	resp = doJSON(t, env.ts, "GET", "/v1/public/audio-stories/public-story", nil, nil)
	require.Equal(t, http.StatusOK, resp.StatusCode)
	resp.Body.Close()

	// Expired rights remove the story from public surfaces.
	_, err := env.pool.Exec(context.Background(),
		`UPDATE audio_story_revisions SET rights_expires_at=now()-interval '1 day' WHERE id=$1`, revisionID)
	require.NoError(t, err)
	resp = doJSON(t, env.ts, "GET", "/v1/public/audio-stories", nil, nil)
	require.Equal(t, http.StatusOK, resp.StatusCode)
	assert.Equal(t, 0, audioItemCount(decodeBody(t, resp)))
	resp = doJSON(t, env.ts, "GET", "/v1/public/audio-stories/public-story", nil, nil)
	assert.Equal(t, http.StatusNotFound, resp.StatusCode)
	resp.Body.Close()

	// Restoring rights and unpublishing the collection hides it again.
	_, err = env.pool.Exec(context.Background(),
		`UPDATE audio_story_revisions SET rights_expires_at=now()+interval '1 year' WHERE id=$1`, revisionID)
	require.NoError(t, err)
	status, body = audioCall(t, env, "POST", "/v1/audio-story-collections/"+collectionID+"/unpublish",
		map[string]any{"reason": "Seasonal collection."}, env.auth.reviewer)
	require.Equal(t, http.StatusOK, status, body)
	resp = doJSON(t, env.ts, "GET", "/v1/public/audio-stories/public-story", nil, nil)
	assert.Equal(t, http.StatusNotFound, resp.StatusCode)
	resp.Body.Close()

	// Moderation is read-only for collection lifecycle writes.
	status, body = audioCall(t, env, "POST", "/v1/audio-story-collections/"+collectionID+"/publish", nil, env.auth.moderator)
	assert.Equal(t, http.StatusForbidden, status, body)

	// Publish the second story and verify cursor pagination over the public list.
	status, body = audioCall(t, env, "POST", "/v1/audio-story-collections/"+collectionID+"/publish", nil, env.auth.reviewer)
	require.Equal(t, http.StatusOK, status, body)
	audioApprove(t, env, draftStoryID, draftRevisionID, env.auth.reviewer2)
	status, body = audioCall(t, env, "POST",
		"/v1/audio-stories/"+draftStoryID+"/revisions/"+draftRevisionID+"/publish", nil, env.auth.reviewer)
	require.Equal(t, http.StatusOK, status, body)

	resp = doJSON(t, env.ts, "GET", "/v1/public/audio-stories?limit=1", nil, nil)
	require.Equal(t, http.StatusOK, resp.StatusCode)
	pageOne := decodeBody(t, resp)
	require.Equal(t, 1, audioItemCount(pageOne))
	assert.Equal(t, true, pageOne["hasMore"])
	nextCursor, _ := pageOne["nextCursor"].(string)
	require.NotEmpty(t, nextCursor)

	resp = doJSON(t, env.ts, "GET", "/v1/public/audio-stories?limit=1&cursor="+nextCursor, nil, nil)
	require.Equal(t, http.StatusOK, resp.StatusCode)
	pageTwo := decodeBody(t, resp)
	require.Equal(t, 1, audioItemCount(pageTwo))
	assert.Equal(t, false, pageTwo["hasMore"])
	firstSlug := pageOne["items"].([]any)[0].(map[string]any)["slug"]
	secondSlug := pageTwo["items"].([]any)[0].(map[string]any)["slug"]
	assert.NotEqual(t, firstSlug, secondSlug)

	resp = doJSON(t, env.ts, "GET", "/v1/public/audio-stories?collection=public-collection", nil, nil)
	require.Equal(t, http.StatusOK, resp.StatusCode)
	assert.Equal(t, 2, audioItemCount(decodeBody(t, resp)))
}

func TestAudioPublishDueAndStoryLifecycle(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}
	env := setupAudioPublishTest(t)
	collectionID := audioSeedCollection(t, env.pool, "due-collection", "published")
	coverID := audioSeedMedia(t, env.pool, "audio-stories/covers/due.jpg", "image")
	audioID := audioSeedMedia(t, env.pool, "audio-stories/due-en.m4a", "audio")
	storyID, revisionID := audioCreateReadyStory(t, env, "due-story", "standard", collectionID, coverID, audioID)
	audioApprove(t, env, storyID, revisionID, env.auth.reviewer)

	var scheduledCollectionID string
	require.NoError(t, env.pool.QueryRow(context.Background(),
		`INSERT INTO audio_story_collections (slug,title,description,status,publish_at)
		 VALUES ('due-collection-scheduled','Scheduled Collection','Due at the next tick.','scheduled',now()-interval '1 minute')
		 RETURNING id`).Scan(&scheduledCollectionID))

	future := time.Now().UTC().Add(time.Hour).Format(time.RFC3339)
	status, body := audioCall(t, env, "POST",
		"/v1/audio-stories/"+storyID+"/revisions/"+revisionID+"/schedule",
		map[string]any{"publishAt": future}, env.auth.reviewer)
	require.Equal(t, http.StatusOK, status, body)

	// Simulate the clock reaching publish_at, then run the scheduler body.
	_, err := env.pool.Exec(context.Background(),
		`UPDATE audio_story_revisions SET publish_at=now()-interval '1 minute' WHERE id=$1`, revisionID)
	require.NoError(t, err)
	due, err := admin.NewAudioStoryHandler(env.pool).PublishDue(context.Background())
	require.NoError(t, err)
	assert.Equal(t, 1, due.Stories)
	assert.Equal(t, 1, due.Collections)

	status, scheduledCollection := audioCall(t, env, "GET", "/v1/audio-story-collections/"+scheduledCollectionID, nil, env.auth.reviewer)
	require.Equal(t, http.StatusOK, status, scheduledCollection)
	assert.Equal(t, "published", scheduledCollection["collection"].(map[string]any)["status"])

	status, detail := audioCall(t, env, "GET", "/v1/audio-stories/"+storyID, nil, env.auth.editor)
	require.Equal(t, http.StatusOK, status, detail)
	assert.Equal(t, "published", detail["story"].(map[string]any)["status"])
	assert.Equal(t, "published", detail["liveRevision"].(map[string]any)["status"])
	assert.Equal(t, float64(1), detail["story"].(map[string]any)["contentVersion"])

	// Revocation requires super_admin and hides the story; restore returns a draft.
	status, body = audioCall(t, env, "POST", "/v1/audio-stories/"+storyID+"/revoke",
		map[string]any{"reason": "Consent withdrawn.", "evidenceReference": "LEGAL-2026-09"}, env.auth.reviewer)
	assert.Equal(t, http.StatusForbidden, status, body)

	status, body = audioCall(t, env, "POST", "/v1/audio-stories/"+storyID+"/revoke",
		map[string]any{"reason": "Consent withdrawn.", "evidenceReference": "LEGAL-2026-09"}, env.auth.super)
	require.Equal(t, http.StatusOK, status, body)

	resp := doJSON(t, env.ts, "GET", "/v1/public/audio-stories/due-story", nil, nil)
	assert.Equal(t, http.StatusNotFound, resp.StatusCode)
	resp.Body.Close()

	status, body = audioCall(t, env, "POST", "/v1/audio-stories/"+storyID+"/restore", nil, env.auth.super)
	require.Equal(t, http.StatusCreated, status, body)
	restoredRevision, _ := body["revisionId"].(string)
	require.NotEmpty(t, restoredRevision)
	assert.Equal(t, "draft", body["status"])

	status, detail = audioCall(t, env, "GET", "/v1/audio-stories/"+storyID, nil, env.auth.editor)
	require.Equal(t, http.StatusOK, status, detail)
	assert.Equal(t, "draft", detail["story"].(map[string]any)["status"])
	assert.Equal(t, "draft", detail["openRevision"].(map[string]any)["status"])
	assert.Equal(t, revisionID, detail["liveRevision"].(map[string]any)["id"])

	// Archive is super_admin-only and cancels open revisions.
	status, body = audioCall(t, env, "POST", "/v1/audio-stories/"+storyID+"/archive",
		map[string]any{"reason": "Archive after revocation."}, env.auth.reviewer)
	assert.Equal(t, http.StatusForbidden, status, body)
	status, body = audioCall(t, env, "POST", "/v1/audio-stories/"+storyID+"/archive",
		map[string]any{"reason": "Archive after revocation."}, env.auth.super)
	require.Equal(t, http.StatusOK, status, body)

	status, detail = audioCall(t, env, "GET", "/v1/audio-stories/"+storyID, nil, env.auth.editor)
	require.Equal(t, http.StatusOK, status, detail)
	assert.Equal(t, "archived", detail["story"].(map[string]any)["status"])
	assert.Nil(t, detail["openRevision"])
}

func TestExpiredRightsUnpublishStory(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}
	env := setupAudioPublishTest(t)
	ctx := context.Background()

	var collectionID, storyID, revisionID string
	require.NoError(t, env.pool.QueryRow(ctx,
		`INSERT INTO audio_story_collections (slug,title,description,status)
		 VALUES ('expiry-collection','Expiry Collection','Test','published') RETURNING id`).Scan(&collectionID))
	require.NoError(t, env.pool.QueryRow(ctx,
		`INSERT INTO audio_stories (slug,title,summary,theme,producer,community_source,collection_id,status,content_version)
		 VALUES ('expiring-story','Expiring Story','Rights lapse test','culture_heritage','YTCI','Test community',$1,'published',1)
		 RETURNING id`, collectionID).Scan(&storyID))
	require.NoError(t, env.pool.QueryRow(ctx,
		`INSERT INTO audio_story_revisions (story_id,status,place_scope,counties,content_note_categories,
		 rights_basis,rights_holder,rights_expires_at,risk_tier,published_at)
		 VALUES ($1,'published','county','{Kilifi}','{}','ytci_owned','YTCI',now() - interval '1 day','standard',now())
		 RETURNING id`, storyID).Scan(&revisionID))
	_, err := env.pool.Exec(ctx,
		`UPDATE audio_stories SET live_revision_id=$1 WHERE id=$2`, revisionID, storyID)
	require.NoError(t, err)

	handler := admin.NewAudioStoryHandler(env.pool)
	expired, err := handler.ExpireRightsDue(ctx)
	require.NoError(t, err)
	assert.GreaterOrEqual(t, expired, 1)

	var status string
	require.NoError(t, env.pool.QueryRow(ctx,
		`SELECT status FROM audio_stories WHERE id=$1`, storyID).Scan(&status))
	assert.Equal(t, "unpublished", status)

	var action string
	require.NoError(t, env.pool.QueryRow(ctx,
		`SELECT action FROM audio_story_events WHERE story_id=$1 ORDER BY created_at DESC LIMIT 1`,
		storyID).Scan(&action))
	assert.Equal(t, "audio_story.rights_expired", action)

	resp, err := http.Get(env.ts.URL + "/v1/public/audio-stories/expiring-story")
	require.NoError(t, err)
	defer resp.Body.Close()
	assert.Equal(t, http.StatusNotFound, resp.StatusCode)
}
