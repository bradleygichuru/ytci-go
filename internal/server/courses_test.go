package server_test

import (
	"context"
	"encoding/json"
	"fmt"
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

func setupCourseTest(t *testing.T) (*httptest.Server, *pgxpool.Pool, string, func(*http.Request)) {
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

	_, err = pool.Exec(ctx, `
		CREATE TABLE IF NOT EXISTS user_profiles (
			user_id TEXT PRIMARY KEY,
			display_name TEXT,
			bio TEXT,
			created_at TIMESTAMP DEFAULT now()
		)
	`)
	require.NoError(t, err)

	adminID := "course-admin-001"
	sessionToken := "course-session-admin"

	_, err = pool.Exec(ctx,
		`INSERT INTO users (id, name, email, created_at, updated_at, role)
		 VALUES ($1, 'Admin User', 'admin@test.com', now(), now(), 'super_admin')`,
		adminID)
	require.NoError(t, err)

	_, err = pool.Exec(ctx,
		`INSERT INTO sessions (id, expires_at, token, created_at, updated_at, user_id)
		 VALUES ($1, now() + interval '1 day', $2, now(), now(), $3)`,
		"sess-course-admin", sessionToken, adminID)
	require.NoError(t, err)

	cfg := &config.Config{
		Port:         "8080",
		DatabaseURL:  "ignored",
		AdminJWKSURL: "http://test.invalid/jwks",
		CORSOrigins:  "*",
	}

	r := server.New(cfg, pool)
	ts := httptest.NewServer(r)

	adminAuth := func(req *http.Request) {
		req.Header.Set("Authorization", "Bearer "+sessionToken)
	}

	return ts, pool, adminID, adminAuth
}

func TestCourseCreateWithAllFields(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}

	ts, _, _, adminAuth := setupCourseTest(t)
	defer ts.Close()

	t.Run("create course with badge metadata", func(t *testing.T) {
		resp := doJSON(t, ts, "POST", "/v1/courses", map[string]any{
			"title":         "Test Course",
			"description":   "Course description",
			"difficulty":    "beginner",
			"imageUrl":      "media/test.png",
			"passThreshold": 80,
			"badgeName":     "Test Badge",
			"badgeIconUrl":  "media/badge.png",
			"status":        "published",
		}, adminAuth)

		assert.Equal(t, http.StatusCreated, resp.StatusCode)
		result := decodeBody(t, resp)
		assert.NotEmpty(t, result["id"])
	})
}

func TestLessonCRUD(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}

	ts, _, _, adminAuth := setupCourseTest(t)
	defer ts.Close()

	resp := doJSON(t, ts, "POST", "/v1/courses", map[string]string{
		"title":      "Lesson Test Course",
		"difficulty": "beginner",
	}, adminAuth)
	require.Equal(t, http.StatusCreated, resp.StatusCode)
	course := decodeBody(t, resp)
	courseID := course["id"].(string)

	t.Run("create lesson", func(t *testing.T) {
		resp := doJSON(t, ts, "POST", fmt.Sprintf("/v1/courses/%s/lessons", courseID), map[string]any{
			"title":       "Lesson One",
			"contentType": "video",
			"contentUrl":  "https://example.com/video.mp4",
			"duration":    300,
		}, adminAuth)
		assert.Equal(t, http.StatusCreated, resp.StatusCode)
		result := decodeBody(t, resp)
		assert.NotEmpty(t, result["id"])
	})

	t.Run("create lesson with invalid contentType", func(t *testing.T) {
		resp := doJSON(t, ts, "POST", fmt.Sprintf("/v1/courses/%s/lessons", courseID), map[string]string{
			"title":       "Bad Lesson",
			"contentType": "invalid",
		}, adminAuth)
		assert.Equal(t, http.StatusBadRequest, resp.StatusCode)
		resp.Body.Close()
	})
}

func TestQuizUpsert(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}

	ts, _, _, adminAuth := setupCourseTest(t)
	defer ts.Close()

	resp := doJSON(t, ts, "POST", "/v1/courses", map[string]string{
		"title":      "Quiz Test Course",
		"difficulty": "beginner",
	}, adminAuth)
	require.Equal(t, http.StatusCreated, resp.StatusCode)
	course := decodeBody(t, resp)
	courseID := course["id"].(string)

	t.Run("upsert quiz with standardized format", func(t *testing.T) {
		questions := []map[string]any{
			{
				"id":           "q1",
				"question":     "What is 2+2?",
				"options":      []string{"3", "4", "5", "6"},
				"correctIndex": float64(1),
			},
		}
		resp := doJSON(t, ts, "POST", fmt.Sprintf("/v1/courses/%s/quiz", courseID), map[string]any{
			"title":         "Course Quiz",
			"questions":     questions,
			"passThreshold": 70,
		}, adminAuth)
		assert.Equal(t, http.StatusOK, resp.StatusCode)
		result := decodeBody(t, resp)
		assert.NotEmpty(t, result["id"])
	})

	t.Run("delete quiz", func(t *testing.T) {
		resp := doJSON(t, ts, "DELETE", fmt.Sprintf("/v1/courses/%s/quiz", courseID), nil, adminAuth)
		assert.Equal(t, http.StatusOK, resp.StatusCode)
		decodeBody(t, resp)
	})
}

func TestCourseQuizQuestionFormat(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}

	ts, _, _, adminAuth := setupCourseTest(t)
	defer ts.Close()

	resp := doJSON(t, ts, "POST", "/v1/courses", map[string]string{
		"title":      "Quiz Format Test",
		"difficulty": "beginner",
	}, adminAuth)
	require.Equal(t, http.StatusCreated, resp.StatusCode)
	course := decodeBody(t, resp)
	courseID := course["id"].(string)

	questions := []map[string]any{
		{
			"id":           "q1",
			"question":     "Capital of Kenya?",
			"options":      []string{"Mombasa", "Nairobi", "Kisumu", "Eldoret"},
			"correctIndex": float64(1),
		},
	}
	doJSON(t, ts, "POST", fmt.Sprintf("/v1/courses/%s/quiz", courseID), map[string]any{
		"title":     "Format Test Quiz",
		"questions": questions,
	}, adminAuth)

	t.Run("GetQuiz strips correctIndex", func(t *testing.T) {
		resp := doJSON(t, ts, "GET", fmt.Sprintf("/v1/mobile/courses/%s/quiz", courseID), nil, nil)
		assert.Equal(t, http.StatusOK, resp.StatusCode)
		result := decodeBody(t, resp)

		qs, ok := result["questions"].([]interface{})
		require.True(t, ok)
		require.Len(t, qs, 1)

		q := qs[0].(map[string]interface{})
		assert.Equal(t, "Capital of Kenya?", q["question"])
		_, hasCorrectIndex := q["correctIndex"]
		assert.False(t, hasCorrectIndex, "correctIndex should be stripped from client-facing response")
	})
}

func TestCourseCompletionFlow(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}

	ts, pool, adminID, adminAuth := setupCourseTest(t)
	defer ts.Close()

	ctx := context.Background()
	courseID := "00000000-0000-0000-0000-0000000000a1"
	lessonID := "00000000-0000-0000-0000-0000000000b1"
	quizID := "00000000-0000-0000-0000-0000000000c1"

	_, err := pool.Exec(ctx,
		`INSERT INTO courses (id, title, description, difficulty, status, badge_name, pass_threshold)
		 VALUES ($1, 'Completion Test', 'Test course', 'beginner', 'published', 'Course Badge', 60)`,
		courseID)
	require.NoError(t, err)

	_, err = pool.Exec(ctx,
		`INSERT INTO lessons (id, course_id, title, content_type, display_order)
		 VALUES ($1, $2, 'Lesson 1', 'text', 1)`,
		lessonID, courseID)
	require.NoError(t, err)

	questionsJSON, _ := json.Marshal([]map[string]any{
		{
			"id":           "q1",
			"question":     "Test question?",
			"options":      []string{"A", "B"},
			"correctIndex": float64(0),
		},
	})
	_, err = pool.Exec(ctx,
		`INSERT INTO quizzes (id, course_id, title, questions, pass_threshold)
		 VALUES ($1, $2, 'Quiz', $3, 50)`,
		quizID, courseID, questionsJSON)
	require.NoError(t, err)
	_ = adminID

	t.Run("mark lesson complete", func(t *testing.T) {
		resp := doJSON(t, ts, "POST", fmt.Sprintf("/v1/mobile/courses/%s/lessons/%s/complete", courseID, lessonID), nil, adminAuth)
		assert.Equal(t, http.StatusNotFound, resp.StatusCode)
		resp.Body.Close()
	})
}
