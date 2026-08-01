package server_test

import (
	"bytes"
	"context"
	"encoding/json"
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

func setupAuthTables(t *testing.T, ctx context.Context, pool *pgxpool.Pool) {
	t.Helper()
	_, err := pool.Exec(ctx, `CREATE TABLE IF NOT EXISTS users (
		id TEXT PRIMARY KEY, name TEXT NOT NULL, email TEXT NOT NULL UNIQUE,
		email_verified BOOLEAN DEFAULT false NOT NULL, image TEXT,
		created_at TIMESTAMP NOT NULL, updated_at TIMESTAMP NOT NULL,
		role TEXT, banned BOOLEAN DEFAULT false, ban_reason TEXT, ban_expires TIMESTAMP
	)`)
	require.NoError(t, err)
	_, err = pool.Exec(ctx, `CREATE TABLE IF NOT EXISTS sessions (
		id TEXT PRIMARY KEY, expires_at TIMESTAMP NOT NULL, token TEXT NOT NULL UNIQUE,
		created_at TIMESTAMP NOT NULL, updated_at TIMESTAMP NOT NULL,
		user_id TEXT NOT NULL, ip_address TEXT, user_agent TEXT, impersonated_by TEXT
	)`)
	require.NoError(t, err)
}

func createSession(t *testing.T, ctx context.Context, pool *pgxpool.Pool, userID, email, token string) {
	t.Helper()
	_, err := pool.Exec(ctx, `INSERT INTO users (id, name, email, created_at, updated_at, role) VALUES ($1, 'Test', $2, now(), now(), 'user')`, userID, email)
	require.NoError(t, err)
	_, err = pool.Exec(ctx, `INSERT INTO sessions (id, expires_at, token, created_at, updated_at, user_id) VALUES ($1, now()+interval '1 day', $2, now(), now(), $3)`, "sess-"+token, token, userID)
	require.NoError(t, err)
}

func authReq(t *testing.T, method, url, token string) *http.Request {
	t.Helper()
	req, err := http.NewRequest(method, url, nil)
	require.NoError(t, err)
	req.Header.Set("Authorization", "Bearer "+token)
	return req
}

func TestCourseDetailAndLessons(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}
	ctx := context.Background()
	pool := db.SetupTestDB(t)
	defer pool.Close()

	setupAuthTables(t, ctx, pool)

	cfg := &config.Config{Port: "8080", DatabaseURL: "ignored", AdminJWKSURL: "http://test.invalid/jwks", CORSOrigins: "*"}
	r := server.New(cfg, pool)
	ts := httptest.NewServer(r)
	defer ts.Close()

	var courseID string
	err := pool.QueryRow(ctx, `
		INSERT INTO courses (id, title, description, difficulty, status, pass_threshold)
		VALUES (gen_random_uuid(), 'Test Course', 'A test', 'beginner', 'published', 70)
		RETURNING id
	`).Scan(&courseID)
	require.NoError(t, err)

	_, err = pool.Exec(ctx, `
		INSERT INTO lessons (course_id, title, content_type, content_url, duration, display_order)
		VALUES ($1, 'Lesson 1', 'video', 'https://example.com/v1', 300, 1)
	`, courseID)
	require.NoError(t, err)

	resp, err := http.Get(ts.URL + "/v1/mobile/courses/" + courseID)
	require.NoError(t, err)
	defer resp.Body.Close()
	assert.Equal(t, http.StatusOK, resp.StatusCode)

	var course map[string]any
	err = json.NewDecoder(resp.Body).Decode(&course)
	require.NoError(t, err)
	assert.Equal(t, "Test Course", course["title"])
	assert.NotNil(t, course["lessons"])

	resp2, err := http.Get(ts.URL + "/v1/mobile/courses/" + courseID + "/lessons")
	require.NoError(t, err)
	defer resp2.Body.Close()
	assert.Equal(t, http.StatusOK, resp2.StatusCode)

	var lessons []any
	err = json.NewDecoder(resp2.Body).Decode(&lessons)
	require.NoError(t, err)
	assert.Len(t, lessons, 1)
}

func TestChallengeDetailAndLeaderboard(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}
	ctx := context.Background()
	pool := db.SetupTestDB(t)
	defer pool.Close()

	setupAuthTables(t, ctx, pool)

	cfg := &config.Config{Port: "8080", DatabaseURL: "ignored", AdminJWKSURL: "http://test.invalid/jwks", CORSOrigins: "*"}
	r := server.New(cfg, pool)
	ts := httptest.NewServer(r)
	defer ts.Close()

	userID := "tu-challenge"
	createSession(t, ctx, pool, userID, "ch@t.com", "token-ch")

	var challengeID string
	err := pool.QueryRow(ctx, `
		INSERT INTO challenges (id, title, description, badge_name, status)
		VALUES (gen_random_uuid(), 'Test Challenge', 'Do it!', 'Gold Star', 'active')
		RETURNING id
	`).Scan(&challengeID)
	require.NoError(t, err)

	_, err = pool.Exec(ctx, `
		INSERT INTO challenge_progress (user_id, challenge_id, status, badge_awarded_at)
		VALUES ($1, $2, 'approved', now())
	`, userID, challengeID)
	require.NoError(t, err)

	resp, err := http.Get(ts.URL + "/v1/mobile/challenges/" + challengeID)
	require.NoError(t, err)
	defer resp.Body.Close()
	assert.Equal(t, http.StatusOK, resp.StatusCode)

	var detail map[string]any
	err = json.NewDecoder(resp.Body).Decode(&detail)
	require.NoError(t, err)
	assert.Equal(t, "Test Challenge", detail["title"])

	req2 := authReq(t, "GET", ts.URL+"/v1/mobile/challenges/"+challengeID+"/leaderboard", "token-ch")
	resp2, err := http.DefaultClient.Do(req2)
	require.NoError(t, err)
	defer resp2.Body.Close()
	assert.Equal(t, http.StatusOK, resp2.StatusCode)

	var board []any
	err = json.NewDecoder(resp2.Body).Decode(&board)
	require.NoError(t, err)
	assert.Len(t, board, 1)
}

func TestConservationDetail(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}
	ctx := context.Background()
	pool := db.SetupTestDB(t)
	defer pool.Close()

	cfg := &config.Config{Port: "8080", DatabaseURL: "ignored", AdminJWKSURL: "http://test.invalid/jwks", CORSOrigins: "*"}
	r := server.New(cfg, pool)
	ts := httptest.NewServer(r)
	defer ts.Close()

	var activityID string
	err := pool.QueryRow(ctx, `
		INSERT INTO conservation_activities (id, title, organizer, privacy_level, status)
		VALUES (gen_random_uuid(), 'Beach Cleanup', 'Eco Group', 'public', 'open')
		RETURNING id
	`).Scan(&activityID)
	require.NoError(t, err)

	resp, err := http.Get(ts.URL + "/v1/mobile/conservation/" + activityID)
	require.NoError(t, err)
	defer resp.Body.Close()
	assert.Equal(t, http.StatusOK, resp.StatusCode)

	var detail map[string]any
	err = json.NewDecoder(resp.Body).Decode(&detail)
	require.NoError(t, err)
	assert.Equal(t, "Beach Cleanup", detail["title"])
}

func TestBadgesAndConsent(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}
	ctx := context.Background()
	pool := db.SetupTestDB(t)
	defer pool.Close()

	setupAuthTables(t, ctx, pool)

	cfg := &config.Config{Port: "8080", DatabaseURL: "ignored", AdminJWKSURL: "http://test.invalid/jwks", CORSOrigins: "*"}
	r := server.New(cfg, pool)
	ts := httptest.NewServer(r)
	defer ts.Close()

	userID := "tu-badges"
	createSession(t, ctx, pool, userID, "b@t.com", "token-b")

	var challengeID string
	err := pool.QueryRow(ctx, `INSERT INTO challenges (id, title, badge_name, status) VALUES (gen_random_uuid(), 'C1', 'Badge1', 'active') RETURNING id`).Scan(&challengeID)
	require.NoError(t, err)
	_, err = pool.Exec(ctx, `INSERT INTO challenge_progress (user_id, challenge_id, status, badge_awarded_at) VALUES ($1, $2, 'approved', now())`, userID, challengeID)
	require.NoError(t, err)

	_, err = pool.Exec(ctx, `INSERT INTO user_profiles (user_id, created_by, consent_granted_at) VALUES ($1, $1, now())`, userID)
	require.NoError(t, err)

	req := authReq(t, "GET", ts.URL+"/v1/mobile/profile/badges", "token-b")
	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()
	assert.Equal(t, http.StatusOK, resp.StatusCode)

	var badges struct {
		Items []any `json:"items"`
	}
	err = json.NewDecoder(resp.Body).Decode(&badges)
	require.NoError(t, err)
	assert.Len(t, badges.Items, 1)

	req2 := authReq(t, "GET", ts.URL+"/v1/mobile/profile/consent", "token-b")
	resp2, err := http.DefaultClient.Do(req2)
	require.NoError(t, err)
	defer resp2.Body.Close()
	assert.Equal(t, http.StatusOK, resp2.StatusCode)

	var consent map[string]any
	err = json.NewDecoder(resp2.Body).Decode(&consent)
	require.NoError(t, err)
	assert.NotNil(t, consent["consentGrantedAt"])

	body := bytes.NewReader([]byte(`{"consentGrantedAt": "2026-01-01T00:00:00Z"}`))
	req3, _ := http.NewRequest("PATCH", ts.URL+"/v1/mobile/profile/consent", body)
	req3.Header.Set("Authorization", "Bearer token-b")
	req3.Header.Set("Content-Type", "application/json")
	resp3, err := http.DefaultClient.Do(req3)
	require.NoError(t, err)
	defer resp3.Body.Close()
	assert.Equal(t, http.StatusOK, resp3.StatusCode)
}

func TestMyStories(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}
	ctx := context.Background()
	pool := db.SetupTestDB(t)
	defer pool.Close()

	setupAuthTables(t, ctx, pool)

	cfg := &config.Config{Port: "8080", DatabaseURL: "ignored", AdminJWKSURL: "http://test.invalid/jwks", CORSOrigins: "*"}
	r := server.New(cfg, pool)
	ts := httptest.NewServer(r)
	defer ts.Close()

	userID := "tu-stories"
	createSession(t, ctx, pool, userID, "s@t.com", "token-s")

	var destID string
	err := pool.QueryRow(ctx, `INSERT INTO destinations (id, name, slug, county, category, status) VALUES (gen_random_uuid(), 'Test Dest', 'test-d-'||gen_random_uuid()::text, 'Nairobi', 'nature', 'published') RETURNING id`).Scan(&destID)
	require.NoError(t, err)

	_, err = pool.Exec(ctx, `INSERT INTO stories (creator_id, destination_id, caption, status) VALUES ($1, $2, 'My post', 'pending')`, userID, destID)
	require.NoError(t, err)

	req := authReq(t, "GET", ts.URL+"/v1/mobile/stories/mine", "token-s")
	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()
	assert.Equal(t, http.StatusOK, resp.StatusCode)

	var stories struct {
		Items []any `json:"items"`
	}
	err = json.NewDecoder(resp.Body).Decode(&stories)
	require.NoError(t, err)
	assert.Len(t, stories.Items, 1)
}

func TestSavedEvents(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}
	ctx := context.Background()
	pool := db.SetupTestDB(t)
	defer pool.Close()

	setupAuthTables(t, ctx, pool)

	cfg := &config.Config{Port: "8080", DatabaseURL: "ignored", AdminJWKSURL: "http://test.invalid/jwks", CORSOrigins: "*"}
	r := server.New(cfg, pool)
	ts := httptest.NewServer(r)
	defer ts.Close()

	userID := "tu-events"
	createSession(t, ctx, pool, userID, "e@t.com", "token-e")

	var eventID string
	err := pool.QueryRow(ctx, `INSERT INTO events (id, title, organizer, county, event_date, type) VALUES (gen_random_uuid(), 'Festival', 'Org', 'Nairobi', now()::date, 'cultural') RETURNING id`).Scan(&eventID)
	require.NoError(t, err)

	_, err = pool.Exec(ctx, `INSERT INTO event_saves (user_id, event_id) VALUES ($1, $2)`, userID, eventID)
	require.NoError(t, err)

	req := authReq(t, "GET", ts.URL+"/v1/mobile/events/saved", "token-e")
	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()
	assert.Equal(t, http.StatusOK, resp.StatusCode)

	var events struct {
		Items []any `json:"items"`
	}
	err = json.NewDecoder(resp.Body).Decode(&events)
	require.NoError(t, err)
	assert.Len(t, events.Items, 1)
}

func TestSubmitConservationEvidenceWithMetrics(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}
	ctx := context.Background()
	pool := db.SetupTestDB(t)
	defer pool.Close()

	setupAuthTables(t, ctx, pool)

	cfg := &config.Config{Port: "8080", DatabaseURL: "ignored", AdminJWKSURL: "http://test.invalid/jwks", CORSOrigins: "*"}
	r := server.New(cfg, pool)
	ts := httptest.NewServer(r)
	defer ts.Close()

	userID := "tu-conserv"
	createSession(t, ctx, pool, userID, "co@t.com", "token-co")

	var activityID string
	err := pool.QueryRow(ctx, `
		INSERT INTO conservation_activities (id, title, organizer, privacy_level, status)
		VALUES (gen_random_uuid(), 'Tree Planting', 'Eco Group', 'public', 'open')
		RETURNING id
	`).Scan(&activityID)
	require.NoError(t, err)

	_, err = pool.Exec(ctx, `
		INSERT INTO conservation_participants (user_id, activity_id, status)
		VALUES ($1, $2, 'joined')
	`, userID, activityID)
	require.NoError(t, err)

	treesPlanted := 15
	hoursSpent := 3.5
	body, _ := json.Marshal(map[string]interface{}{
		"description":  "Planted trees in Karura Forest",
		"treesPlanted": treesPlanted,
		"hoursSpent":   hoursSpent,
		"lat":          -1.2500,
		"lng":          36.8100,
	})
	req, _ := http.NewRequest("POST", ts.URL+"/v1/mobile/conservation/"+activityID+"/evidence", bytes.NewBuffer(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer token-co")
	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()
	assert.Equal(t, http.StatusCreated, resp.StatusCode)

	var result map[string]any
	err = json.NewDecoder(resp.Body).Decode(&result)
	require.NoError(t, err)
	assert.Equal(t, "pending", result["status"])

	// Unauthenticated request should return 401
	body2, _ := json.Marshal(map[string]interface{}{
		"description": "test",
	})
	req2, _ := http.NewRequest("POST", ts.URL+"/v1/mobile/conservation/"+activityID+"/evidence", bytes.NewBuffer(body2))
	req2.Header.Set("Content-Type", "application/json")
	resp2, err := http.DefaultClient.Do(req2)
	require.NoError(t, err)
	defer resp2.Body.Close()
	assert.Equal(t, http.StatusUnauthorized, resp2.StatusCode)
}
