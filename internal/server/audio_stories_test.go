package server_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bradleygichuru/ytci-go/internal/config"
	"github.com/bradleygichuru/ytci-go/internal/db"
	"github.com/bradleygichuru/ytci-go/internal/server"
)

func setupAudioStoryTest(t *testing.T) (*httptest.Server, *pgxpool.Pool, func(*http.Request), func(*http.Request)) {
	t.Helper()
	ctx := context.Background()
	pool := db.SetupTestDB(t)
	t.Cleanup(func() { pool.Close() })

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
		)
	`)
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
		)
	`)
	require.NoError(t, err)

	const editorID, editorToken = "audio-editor-001", "audio-session-editor"
	const moderatorID, moderatorToken = "audio-moderator-001", "audio-session-moderator"
	_, err = pool.Exec(ctx,
		`INSERT INTO users (id,name,email,created_at,updated_at,role) VALUES
		 ($1,'Audio Editor','audio-editor@test.com',now(),now(),'content_editor'),
		 ($2,'Audio Moderator','audio-moderator@test.com',now(),now(),'moderator')`,
		editorID, moderatorID)
	require.NoError(t, err)
	_, err = pool.Exec(ctx,
		`INSERT INTO sessions (id,expires_at,token,created_at,updated_at,user_id) VALUES
		 ('sess-audio-editor', now() + interval '1 day', $1, now(), now(), $2),
		 ('sess-audio-moderator', now() + interval '1 day', $3, now(), now(), $4)`,
		editorToken, editorID, moderatorToken, moderatorID)
	require.NoError(t, err)

	cfg := &config.Config{
		Port:         "8080",
		DatabaseURL:  "ignored",
		AdminJWKSURL: "http://test.invalid/jwks",
		CORSOrigins:  "*",
	}
	ts := httptest.NewServer(server.New(cfg, pool))
	t.Cleanup(ts.Close)

	editorAuth := func(req *http.Request) { req.Header.Set("Authorization", "Bearer "+editorToken) }
	moderatorAuth := func(req *http.Request) { req.Header.Set("Authorization", "Bearer "+moderatorToken) }
	return ts, pool, editorAuth, moderatorAuth
}

func TestAudioStoryDraftFlow(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}
	ctx := context.Background()
	ts, pool, editorAuth, moderatorAuth := setupAudioStoryTest(t)

	var collectionID, coverID, audioAssetID string
	require.NoError(t, pool.QueryRow(ctx,
		`INSERT INTO audio_story_collections (slug,title,description,status)
		 VALUES ('coastal-voices','Coastal Voices','Stories from the Kenyan coast.','published') RETURNING id`).Scan(&collectionID))
	require.NoError(t, pool.QueryRow(ctx,
		`INSERT INTO media_assets (entity_type,entity_id,object_key,type,status)
		 VALUES ('audio_story',NULL,'audio-stories/covers/mangrove.jpg','image','ready') RETURNING id`).Scan(&coverID))
	require.NoError(t, pool.QueryRow(ctx,
		`INSERT INTO media_assets (entity_type,entity_id,object_key,type,status)
		 VALUES ('audio_story',NULL,'audio-stories/mangrove-en.m4a','audio','ready') RETURNING id`).Scan(&audioAssetID))

	preflight, err := http.NewRequest(http.MethodOptions,
		ts.URL+"/v1/audio-stories/x/revisions/y/transcripts/en", nil)
	require.NoError(t, err)
	preflight.Header.Set("Origin", "http://localhost:3000")
	preflight.Header.Set("Access-Control-Request-Method", "PUT")
	preflightResp, err := ts.Client().Do(preflight)
	require.NoError(t, err)
	assert.Contains(t, preflightResp.Header.Get("Access-Control-Allow-Methods"), "PUT")
	preflightResp.Body.Close()

	createPayload := map[string]any{
		"slug":            "the-last-mangrove",
		"title":           "The Last Mangrove",
		"summary":         "Kilifi elders restore a coastal mangrove.",
		"theme":           "nature_wildlife",
		"coverMediaId":    coverID,
		"producer":        "YTCI",
		"communitySource": "Kilifi elders",
		"collectionId":    collectionID,
		"placeScope":      "county",
		"counties":        []string{"Kilifi"},
		"rightsBasis":     "ytci_owned",
		"rightsHolder":    "YTCI",
	}

	resp := doJSON(t, ts, "POST", "/v1/audio-stories", createPayload, moderatorAuth)
	assert.Equal(t, http.StatusForbidden, resp.StatusCode)
	resp.Body.Close()

	resp = doJSON(t, ts, "POST", "/v1/audio-stories", createPayload, editorAuth)
	require.Equal(t, http.StatusCreated, resp.StatusCode)
	created := decodeBody(t, resp)
	storyID, _ := created["id"].(string)
	revisionID, _ := created["revisionId"].(string)
	require.NotEmpty(t, storyID)
	require.NotEmpty(t, revisionID)

	resp = doJSON(t, ts, "GET", "/v1/audio-story-collections", nil, editorAuth)
	require.Equal(t, http.StatusOK, resp.StatusCode)
	collections := decodeBody(t, resp)
	require.Len(t, collections["items"].([]any), 1)
	assert.Equal(t, "Coastal Voices", collections["items"].([]any)[0].(map[string]any)["title"])

	// Moderators can read the list and the story, but cannot mutate.
	resp = doJSON(t, ts, "GET", "/v1/audio-stories", nil, moderatorAuth)
	require.Equal(t, http.StatusOK, resp.StatusCode)
	list := decodeBody(t, resp)
	require.Len(t, list["items"].([]any), 1)
	summary := list["items"].([]any)[0].(map[string]any)
	assert.Equal(t, "Coastal Voices", summary["collectionTitle"])
	assert.Equal(t, "standard", summary["riskTier"])

	resp = doJSON(t, ts, "GET", "/v1/audio-stories/"+storyID, nil, moderatorAuth)
	require.Equal(t, http.StatusOK, resp.StatusCode)
	story := decodeBody(t, resp)
	require.NotNil(t, story["openRevision"])
	assert.Equal(t, "draft", story["openRevision"].(map[string]any)["status"])

	// Metadata update is a normal editor action.
	resp = doJSON(t, ts, "PATCH", "/v1/audio-stories/"+storyID,
		map[string]any{"title": "The Last Mangrove of Kilifi"}, editorAuth)
	require.Equal(t, http.StatusOK, resp.StatusCode)
	resp.Body.Close()

	// Attach the English track and transcript.
	resp = doJSON(t, ts, "POST", "/v1/audio-stories/"+storyID+"/revisions/"+revisionID+"/tracks",
		map[string]any{
			"language":                 "en",
			"mediaAssetId":             audioAssetID,
			"audioObjectKey":           "audio-stories/mangrove-en.m4a",
			"durationSeconds":          720,
			"narrator":                 "Amina Yusuf",
			"narratorConsentDate":      "2026-10-01",
			"narratorConsentReference": "REL-2026-014",
			"format":                   "m4a",
			"bitrateKbps":              96,
			"sizeBytes":                18432000,
		}, editorAuth)
	require.Equal(t, http.StatusOK, resp.StatusCode)
	resp.Body.Close()

	resp = doJSON(t, ts, "PUT", "/v1/audio-stories/"+storyID+"/revisions/"+revisionID+"/transcripts/en",
		map[string]any{"paragraphs": []map[string]any{
			{"text": "The mangrove keeps the coast.", "startSeconds": 0},
			{"text": "The elders replant it.", "startSeconds": 42},
		}}, editorAuth)
	require.Equal(t, http.StatusOK, resp.StatusCode)
	resp.Body.Close()

	// Readiness is computed server-side and clean.
	resp = doJSON(t, ts, "GET", "/v1/audio-stories/"+storyID+"/revisions/"+revisionID+"/readiness", nil, editorAuth)
	require.Equal(t, http.StatusOK, resp.StatusCode)
	readiness := decodeBody(t, resp)
	assert.Equal(t, true, readiness["ready"])
	assert.Empty(t, readiness["blocking"])

	// Moderators cannot submit.
	resp = doJSON(t, ts, "POST", "/v1/audio-stories/"+storyID+"/revisions/"+revisionID+"/submit", nil, moderatorAuth)
	assert.Equal(t, http.StatusForbidden, resp.StatusCode)
	resp.Body.Close()

	// Editors submit, and the revision freezes.
	resp = doJSON(t, ts, "POST", "/v1/audio-stories/"+storyID+"/revisions/"+revisionID+"/submit", nil, editorAuth)
	require.Equal(t, http.StatusOK, resp.StatusCode)
	resp.Body.Close()

	resp = doJSON(t, ts, "GET", "/v1/audio-stories/"+storyID, nil, editorAuth)
	require.Equal(t, http.StatusOK, resp.StatusCode)
	submitted := decodeBody(t, resp)
	assert.Equal(t, "in_review", submitted["story"].(map[string]any)["status"])
	assert.Equal(t, "in_review", submitted["openRevision"].(map[string]any)["status"])

	resp = doJSON(t, ts, "PATCH", "/v1/audio-stories/"+storyID+"/revisions/"+revisionID,
		map[string]any{"rightsHolder": "Someone Else"}, editorAuth)
	require.Equal(t, http.StatusConflict, resp.StatusCode)
	frozen := decodeBody(t, resp)
	assert.Equal(t, "REVISION_FROZEN", frozen["error"].(map[string]any)["code"])

	resp = doJSON(t, ts, "GET", "/v1/audio-stories/"+storyID+"/audit", nil, editorAuth)
	require.Equal(t, http.StatusOK, resp.StatusCode)
	audit := decodeBody(t, resp)
	actions := make([]string, 0)
	for _, raw := range audit["items"].([]any) {
		actions = append(actions, raw.(map[string]any)["action"].(string))
	}
	assert.Contains(t, actions, "audio_story.created")
	assert.Contains(t, actions, "audio_story.submitted_for_review")

	// Simulate the live state #266 will produce, then confirm a material change
	// clones the published revision and its tracks/transcripts into a new draft.
	_, err = pool.Exec(ctx, `UPDATE audio_story_revisions SET status='published' WHERE id=$1`, revisionID)
	require.NoError(t, err)
	_, err = pool.Exec(ctx,
		`UPDATE audio_stories SET status='published',live_revision_id=$1,open_revision_id=NULL WHERE id=$2`,
		revisionID, storyID)
	require.NoError(t, err)

	resp = doJSON(t, ts, "POST", "/v1/audio-stories/"+storyID+"/revisions", map[string]any{}, editorAuth)
	require.Equal(t, http.StatusCreated, resp.StatusCode)
	cloned := decodeBody(t, resp)
	newRevisionID, _ := cloned["revisionId"].(string)
	require.NotEmpty(t, newRevisionID)
	assert.Equal(t, revisionID, cloned["sourceRevisionId"])

	resp = doJSON(t, ts, "GET", "/v1/audio-stories/"+storyID+"/revisions/"+newRevisionID, nil, editorAuth)
	require.Equal(t, http.StatusOK, resp.StatusCode)
	detail := decodeBody(t, resp)
	assert.Equal(t, "draft", detail["status"])
	require.Len(t, detail["tracks"].([]any), 1)
	require.Len(t, detail["transcripts"].([]any), 1)

	// A draft can be created before rights and placement are known; readiness
	// reports the missing rights instead of blocking the create.
	resp = doJSON(t, ts, "POST", "/v1/audio-stories", map[string]any{
		"slug":            "tidal-memories",
		"title":           "Tidal Memories",
		"summary":         "A fisherman remembers the old harbour tides.",
		"theme":           "culture_heritage",
		"producer":        "YTCI Coastal Unit",
		"communitySource": "Kilifi fishing families",
		"collectionId":    collectionID,
	}, editorAuth)
	require.Equal(t, http.StatusCreated, resp.StatusCode)
	minimal := decodeBody(t, resp)
	minimalID, _ := minimal["id"].(string)
	minimalRevision, _ := minimal["revisionId"].(string)
	require.NotEmpty(t, minimalID)
	require.NotEmpty(t, minimalRevision)

	resp = doJSON(t, ts, "PATCH", "/v1/audio-stories/"+minimalID+"/revisions/"+minimalRevision,
		map[string]any{"placeScope": "county", "counties": []string{"Kilifi"}}, editorAuth)
	require.Equal(t, http.StatusOK, resp.StatusCode)
	resp.Body.Close()

	resp = doJSON(t, ts, "GET", "/v1/audio-stories/"+minimalID+"/revisions/"+minimalRevision+"/readiness", nil, editorAuth)
	require.Equal(t, http.StatusOK, resp.StatusCode)
	minimalReadiness := decodeBody(t, resp)
	assert.Equal(t, false, minimalReadiness["ready"])
	codes := make([]string, 0)
	for _, raw := range minimalReadiness["blocking"].([]any) {
		codes = append(codes, raw.(map[string]any)["code"].(string))
	}
	assert.Contains(t, codes, "missing_rights")

	resp = doJSON(t, ts, "GET", "/v1/audio-stories/"+storyID, nil, editorAuth)
	require.Equal(t, http.StatusOK, resp.StatusCode)
	afterClone := decodeBody(t, resp)
	assert.Equal(t, "published", afterClone["story"].(map[string]any)["status"])
	assert.Equal(t, "draft", afterClone["openRevision"].(map[string]any)["status"])
	assert.NotEmpty(t, afterClone["liveRevision"])
}
