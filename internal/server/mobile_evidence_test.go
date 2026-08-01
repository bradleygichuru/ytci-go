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

func TestListMyChallenges(t *testing.T) {
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

	userID := "tu-mychallenges"
	createSession(t, ctx, pool, userID, "mc@t.com", "token-mc")

	var challengeID string
	err := pool.QueryRow(ctx, `
		INSERT INTO challenges (id, title, description, badge_name, status)
		VALUES (gen_random_uuid(), 'List My Challenge', 'Desc', 'Explorer', 'active')
		RETURNING id
	`).Scan(&challengeID)
	require.NoError(t, err)

	_, err = pool.Exec(ctx, `
		INSERT INTO challenge_progress (user_id, challenge_id, status)
		VALUES ($1, $2, 'in_progress')
	`, userID, challengeID)
	require.NoError(t, err)

	req := authReq(t, "GET", ts.URL+"/v1/mobile/challenges", "token-mc")
	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()
	assert.Equal(t, http.StatusOK, resp.StatusCode)

	var challenges []map[string]any
	err = json.NewDecoder(resp.Body).Decode(&challenges)
	require.NoError(t, err)
	require.Len(t, challenges, 1)
	assert.Equal(t, "List My Challenge", challenges[0]["title"])
	assert.Equal(t, "in_progress", challenges[0]["userStatus"])

	// Unauthenticated request should return 401
	req2, _ := http.NewRequest("GET", ts.URL+"/v1/mobile/challenges", nil)
	resp2, err := http.DefaultClient.Do(req2)
	require.NoError(t, err)
	defer resp2.Body.Close()
	assert.Equal(t, http.StatusUnauthorized, resp2.StatusCode)
}

func TestSubmitChallengeEvidenceWithLocation(t *testing.T) {
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

	userID := "tu-evidence"
	createSession(t, ctx, pool, userID, "ev@t.com", "token-ev")

	var challengeID string
	err := pool.QueryRow(ctx, `
		INSERT INTO challenges (id, title, description, badge_name, status)
		VALUES (gen_random_uuid(), 'Evidence Challenge', 'Desc', 'Gold', 'active')
		RETURNING id
	`).Scan(&challengeID)
	require.NoError(t, err)

	_, err = pool.Exec(ctx, `
		INSERT INTO challenge_progress (user_id, challenge_id, status)
		VALUES ($1, $2, 'in_progress')
	`, userID, challengeID)
	require.NoError(t, err)

	lat := -1.2921
	lng := 36.8219
	body, _ := json.Marshal(map[string]interface{}{
		"description": "Visited Nairobi National Park",
		"lat":         lat,
		"lng":         lng,
	})
	req, _ := http.NewRequest("POST", ts.URL+"/v1/mobile/challenges/"+challengeID+"/evidence", bytes.NewBuffer(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer token-ev")
	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()
	assert.Equal(t, http.StatusOK, resp.StatusCode)

	var result map[string]any
	err = json.NewDecoder(resp.Body).Decode(&result)
	require.NoError(t, err)
	assert.Equal(t, "submitted", result["status"])

	// Unauthenticated request should return 401
	body2, _ := json.Marshal(map[string]interface{}{
		"description": "test",
	})
	req2, _ := http.NewRequest("POST", ts.URL+"/v1/mobile/challenges/"+challengeID+"/evidence", bytes.NewBuffer(body2))
	req2.Header.Set("Content-Type", "application/json")
	resp2, err := http.DefaultClient.Do(req2)
	require.NoError(t, err)
	defer resp2.Body.Close()
	assert.Equal(t, http.StatusUnauthorized, resp2.StatusCode)
}

func TestListPublishedCoursesWithCategory(t *testing.T) {
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

	var courseID string
	err := pool.QueryRow(ctx, `
		INSERT INTO courses (id, title, description, category, difficulty, status, pass_threshold)
		VALUES (gen_random_uuid(), 'Wildlife Course', 'Learn about wildlife', 'conservation', 'beginner', 'published', 70)
		RETURNING id
	`).Scan(&courseID)
	require.NoError(t, err)

	_, err = pool.Exec(ctx, `
		INSERT INTO lessons (course_id, title, content_type, content_url, duration, display_order)
		VALUES ($1, 'Lesson 1', 'video', 'https://example.com/v1', 300, 1)
	`, courseID)
	require.NoError(t, err)

	_, err = pool.Exec(ctx, `
		INSERT INTO lessons (course_id, title, content_type, content_url, duration, display_order)
		VALUES ($1, 'Lesson 2', 'video', 'https://example.com/v2', 450, 2)
	`, courseID)
	require.NoError(t, err)

	resp, err := http.Get(ts.URL + "/v1/public/courses")
	require.NoError(t, err)
	defer resp.Body.Close()
	assert.Equal(t, http.StatusOK, resp.StatusCode)

	var courses []map[string]any
	err = json.NewDecoder(resp.Body).Decode(&courses)
	require.NoError(t, err)
	require.Len(t, courses, 1)
	assert.Equal(t, "Wildlife Course", courses[0]["title"])
	assert.Equal(t, "conservation", courses[0]["category"])
	assert.Equal(t, float64(750), courses[0]["total_duration_minutes"])
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
	assert.Equal(t, http.StatusOK, resp.StatusCode)

	var result map[string]any
	err = json.NewDecoder(resp.Body).Decode(&result)
	require.NoError(t, err)
	assert.Equal(t, "submitted", result["status"])

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
