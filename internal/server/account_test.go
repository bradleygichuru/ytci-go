package server_test

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bradleygichuru/ytci-go/internal/config"
	"github.com/bradleygichuru/ytci-go/internal/db"
	"github.com/bradleygichuru/ytci-go/internal/server"
)

func TestAccountDeletion(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}

	ctx := context.Background()
	pool := db.SetupTestDB(t)
	defer pool.Close()

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

	_, err = pool.Exec(ctx, `
		CREATE TABLE IF NOT EXISTS accounts (
			id TEXT PRIMARY KEY,
			account_id TEXT NOT NULL,
			provider_id TEXT NOT NULL,
			user_id TEXT NOT NULL,
			access_token TEXT,
			refresh_token TEXT,
			id_token TEXT,
			access_token_expires_at TIMESTAMP,
			refresh_token_expires_at TIMESTAMP,
			scope TEXT,
			password TEXT,
			created_at TIMESTAMP NOT NULL,
			updated_at TIMESTAMP NOT NULL
		)
	`)
	require.NoError(t, err)

	_, err = pool.Exec(ctx, `
		CREATE TABLE IF NOT EXISTS audit_logs (
			id TEXT PRIMARY KEY,
			user_id TEXT NOT NULL,
			action TEXT NOT NULL,
			details TEXT,
			performed_by TEXT NOT NULL,
			created_at TIMESTAMP NOT NULL
		)
	`)
	require.NoError(t, err)

	cfg := &config.Config{
		Port:         "8080",
		DatabaseURL:  "ignored",
		AdminJWKSURL: "http://test.invalid/jwks",
		CORSOrigins:  "*",
	}

	r := server.New(cfg, pool)
	ts := httptest.NewServer(r)
	defer ts.Close()

	userID := "test-user-001"
	sessionToken := "test-session-abc"

	_, err = pool.Exec(ctx, `
		INSERT INTO users (id, name, email, created_at, updated_at, role)
		VALUES ($1, 'Test User', 'test@example.com', now(), now(), 'user')
	`, userID)
	require.NoError(t, err)

	_, err = pool.Exec(ctx, `
		INSERT INTO sessions (id, expires_at, token, created_at, updated_at, user_id)
		VALUES ($1, now() + interval '1 day', $2, now(), now(), $3)
	`, "sess-1", sessionToken, userID)
	require.NoError(t, err)

	_, err = pool.Exec(ctx, `
		INSERT INTO accounts (id, account_id, provider_id, user_id, created_at, updated_at)
		VALUES ($1, 'acct-1', 'email', $2, now(), now())
	`, "acct-1", userID)
	require.NoError(t, err)

	var destID string
	err = pool.QueryRow(ctx, `SELECT id FROM destinations LIMIT 1`).Scan(&destID)
	if err != nil {
		err = pool.QueryRow(ctx, `
			INSERT INTO destinations (id, name, slug, county, category, status)
			VALUES (gen_random_uuid(), 'Test Dest', 'test-dest-' || gen_random_uuid()::text, 'Nairobi', 'nature', 'published')
			RETURNING id
		`).Scan(&destID)
		require.NoError(t, err)
	}

	_, err = pool.Exec(ctx, `
		INSERT INTO bucket_list_items (user_id, destination_id)
		VALUES ($1, $2)
	`, userID, destID)
	require.NoError(t, err)

	_, err = pool.Exec(ctx, `
		INSERT INTO user_profiles (user_id, created_by)
		VALUES ($1, $1)
	`, userID)
	require.NoError(t, err)

	_, err = pool.Exec(ctx, `
		INSERT INTO push_tokens (user_id, token, platform)
		VALUES ($1, 'expo-token-abc', 'ios')
	`, userID)
	require.NoError(t, err)

	_, err = pool.Exec(ctx, `
		INSERT INTO app_opens (user_id, platform, app_version)
		VALUES ($1, 'ios', '1.0')
	`, userID)
	require.NoError(t, err)

	var storyID string
	err = pool.QueryRow(ctx, `
		INSERT INTO stories (id, creator_id, destination_id, caption, status)
		VALUES (gen_random_uuid(), $1, $2, 'My story', 'approved')
		RETURNING id
	`, userID, destID).Scan(&storyID)
	require.NoError(t, err)

	var itinID string
	err = pool.QueryRow(ctx, `
		INSERT INTO itineraries (id, user_id, title, inputs, status)
		VALUES (gen_random_uuid(), $1, 'My Trip', '{}'::jsonb, 'draft')
		RETURNING id
	`, userID).Scan(&itinID)
	require.NoError(t, err)

	var commentID string
	err = pool.QueryRow(ctx, `
		INSERT INTO story_comments (id, story_id, author_id, body)
		VALUES (gen_random_uuid(), $1, $2, 'Great story!')
		RETURNING id
	`, storyID, userID).Scan(&commentID)
	require.NoError(t, err)

	_, err = pool.Exec(ctx, `
		INSERT INTO story_interactions (user_id, story_id, interaction_type)
		VALUES ($1, $2, 'like')
	`, userID, storyID)
	require.NoError(t, err)

	_, err = pool.Exec(ctx, `
		INSERT INTO comment_interactions (user_id, comment_id, interaction_type)
		VALUES ($1, $2, 'like')
	`, userID, commentID)
	require.NoError(t, err)

	_, err = pool.Exec(ctx, `
		INSERT INTO story_reports (story_id, reported_by, reason)
		VALUES ($1, $2, 'spam')
	`, storyID, userID)
	require.NoError(t, err)

	_, err = pool.Exec(ctx, `
		INSERT INTO report_jobs (requested_by, format, date_from, date_to)
		VALUES ($1, 'csv', now()::date, now()::date)
	`, userID)
	require.NoError(t, err)

	var challengeID string
	err = pool.QueryRow(ctx, `
		INSERT INTO challenges (id, title, status)
		VALUES (gen_random_uuid(), 'Test Challenge', 'active')
		RETURNING id
	`).Scan(&challengeID)
	require.NoError(t, err)

	var progressID string
	err = pool.QueryRow(ctx, `
		INSERT INTO challenge_progress (id, user_id, challenge_id, status)
		VALUES (gen_random_uuid(), $1, $2, 'submitted')
		RETURNING id
	`, userID, challengeID).Scan(&progressID)
	require.NoError(t, err)

	var courseID string
	err = pool.QueryRow(ctx, `
		INSERT INTO courses (id, title, difficulty, status)
		VALUES (gen_random_uuid(), 'Test Course', 'beginner', 'published')
		RETURNING id
	`).Scan(&courseID)
	require.NoError(t, err)

	var enrollID string
	err = pool.QueryRow(ctx, `
		INSERT INTO course_enrollments (id, user_id, course_id)
		VALUES (gen_random_uuid(), $1, $2)
		RETURNING id
	`, userID, courseID).Scan(&enrollID)
	require.NoError(t, err)

	var activityID string
	err = pool.QueryRow(ctx, `
		INSERT INTO conservation_activities (id, title, organizer, privacy_level, status)
		VALUES (gen_random_uuid(), 'Test Cleanup', 'Org', 'public', 'open')
		RETURNING id
	`).Scan(&activityID)
	require.NoError(t, err)

	var evidenceID string
	err = pool.QueryRow(ctx, `
		INSERT INTO conservation_evidence (id, user_id, activity_id, description)
		VALUES (gen_random_uuid(), $1, $2, 'Collected 10kg')
		RETURNING id
	`, userID, activityID).Scan(&evidenceID)
	require.NoError(t, err)

	var eventID string
	err = pool.QueryRow(ctx, `SELECT id FROM events LIMIT 1`).Scan(&eventID)
	if err != nil {
		err = pool.QueryRow(ctx, `
			INSERT INTO events (id, title, organizer, county, event_date, type)
			VALUES (gen_random_uuid(), 'Test Event', 'Org', 'Nairobi', now()::date, 'cultural')
			RETURNING id
		`).Scan(&eventID)
		require.NoError(t, err)
	}

	_, err = pool.Exec(ctx, `
		INSERT INTO event_saves (user_id, event_id)
		VALUES ($1, $2)
	`, userID, eventID)
	require.NoError(t, err)

	_, err = pool.Exec(ctx, `
		INSERT INTO pending_media_uploads (object_key, user_id, content_type, file_size)
		VALUES ('test-key-1', $1, 'image/jpeg', 1000)
	`, userID)
	require.NoError(t, err)

	body, _ := json.Marshal(map[string]bool{"confirm": true})
	req, err := http.NewRequest("POST", ts.URL+"/v1/mobile/account/delete", bytes.NewReader(body))
	require.NoError(t, err)
	req.Header.Set("Authorization", "Bearer "+sessionToken)
	req.Header.Set("Content-Type", "application/json")

	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()

	assert.Equal(t, http.StatusOK, resp.StatusCode)

	var result map[string]string
	err = json.NewDecoder(resp.Body).Decode(&result)
	require.NoError(t, err)
	assert.Equal(t, "deleted", result["status"])

	var userExists bool
	err = pool.QueryRow(ctx, "SELECT EXISTS (SELECT 1 FROM users WHERE id = $1)", userID).Scan(&userExists)
	require.NoError(t, err)
	assert.False(t, userExists, "user should be deleted")

	var sessionExists bool
	err = pool.QueryRow(ctx, "SELECT EXISTS (SELECT 1 FROM sessions WHERE user_id = $1)", userID).Scan(&sessionExists)
	require.NoError(t, err)
	assert.False(t, sessionExists, "sessions should be deleted")

	var accountExists bool
	err = pool.QueryRow(ctx, "SELECT EXISTS (SELECT 1 FROM accounts WHERE user_id = $1)", userID).Scan(&accountExists)
	require.NoError(t, err)
	assert.False(t, accountExists, "accounts should be deleted")

	var auditExists bool
	err = pool.QueryRow(ctx, "SELECT EXISTS (SELECT 1 FROM audit_logs WHERE user_id = $1)", userID).Scan(&auditExists)
	require.NoError(t, err)
	assert.False(t, auditExists, "audit logs should be deleted")

	var bucketExists bool
	err = pool.QueryRow(ctx, "SELECT EXISTS (SELECT 1 FROM bucket_list_items WHERE user_id = $1)", userID).Scan(&bucketExists)
	require.NoError(t, err)
	assert.False(t, bucketExists, "bucket list items should be deleted")

	var profileExists bool
	err = pool.QueryRow(ctx, "SELECT EXISTS (SELECT 1 FROM user_profiles WHERE user_id = $1)", userID).Scan(&profileExists)
	require.NoError(t, err)
	assert.False(t, profileExists, "user profile should be deleted")

	var pushTokenExists bool
	err = pool.QueryRow(ctx, "SELECT EXISTS (SELECT 1 FROM push_tokens WHERE user_id = $1)", userID).Scan(&pushTokenExists)
	require.NoError(t, err)
	assert.False(t, pushTokenExists, "push tokens should be deleted")

	var appOpenExists bool
	err = pool.QueryRow(ctx, "SELECT EXISTS (SELECT 1 FROM app_opens WHERE user_id = $1)", userID).Scan(&appOpenExists)
	require.NoError(t, err)
	assert.False(t, appOpenExists, "app opens should be deleted")

	var interactionExists bool
	err = pool.QueryRow(ctx, "SELECT EXISTS (SELECT 1 FROM story_interactions WHERE user_id = $1)", userID).Scan(&interactionExists)
	require.NoError(t, err)
	assert.False(t, interactionExists, "story interactions should be deleted")

	var commentInteractionExists bool
	err = pool.QueryRow(ctx, "SELECT EXISTS (SELECT 1 FROM comment_interactions WHERE user_id = $1)", userID).Scan(&commentInteractionExists)
	require.NoError(t, err)
	assert.False(t, commentInteractionExists, "comment interactions should be deleted")

	var eventSaveExists bool
	err = pool.QueryRow(ctx, "SELECT EXISTS (SELECT 1 FROM event_saves WHERE user_id = $1)", userID).Scan(&eventSaveExists)
	require.NoError(t, err)
	assert.False(t, eventSaveExists, "event saves should be deleted")

	var pendingUploadExists bool
	err = pool.QueryRow(ctx, "SELECT EXISTS (SELECT 1 FROM pending_media_uploads WHERE user_id = $1)", userID).Scan(&pendingUploadExists)
	require.NoError(t, err)
	assert.False(t, pendingUploadExists, "pending media uploads should be deleted")

	var storyCreatorID *string
	err = pool.QueryRow(ctx, "SELECT creator_id FROM stories WHERE id = $1", storyID).Scan(&storyCreatorID)
	require.NoError(t, err)
	assert.Nil(t, storyCreatorID, "story creator_id should be NULL")

	var itineraryUserID *string
	err = pool.QueryRow(ctx, "SELECT user_id FROM itineraries WHERE id = $1", itinID).Scan(&itineraryUserID)
	require.NoError(t, err)
	assert.Nil(t, itineraryUserID, "itinerary user_id should be NULL")

	var commentAuthorID *string
	err = pool.QueryRow(ctx, "SELECT author_id FROM story_comments WHERE id = $1", commentID).Scan(&commentAuthorID)
	require.NoError(t, err)
	assert.Nil(t, commentAuthorID, "comment author_id should be NULL")

	var reporterID *string
	err = pool.QueryRow(ctx, "SELECT reported_by FROM story_reports WHERE story_id = $1", storyID).Scan(&reporterID)
	require.NoError(t, err)
	assert.Nil(t, reporterID, "report reported_by should be NULL")

	var reportReqID *string
	err = pool.QueryRow(ctx, "SELECT requested_by FROM report_jobs WHERE requested_by IS NULL").Scan(&reportReqID)
	require.NoError(t, err)
	assert.Nil(t, reportReqID, "report job requested_by should be NULL")

	var progressUserID *string
	err = pool.QueryRow(ctx, "SELECT user_id FROM challenge_progress WHERE id = $1", progressID).Scan(&progressUserID)
	require.NoError(t, err)
	assert.Nil(t, progressUserID, "challenge progress user_id should be NULL")

	var enrollUserID *string
	err = pool.QueryRow(ctx, "SELECT user_id FROM course_enrollments WHERE id = $1", enrollID).Scan(&enrollUserID)
	require.NoError(t, err)
	assert.Nil(t, enrollUserID, "course enrollment user_id should be NULL")

	var evidenceUserID *string
	err = pool.QueryRow(ctx, "SELECT user_id FROM conservation_evidence WHERE id = $1", evidenceID).Scan(&evidenceUserID)
	require.NoError(t, err)
	assert.Nil(t, evidenceUserID, "conservation evidence user_id should be NULL")
}
