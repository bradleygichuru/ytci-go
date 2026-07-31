package expo

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/jung-kurt/gofpdf"

	"github.com/bradleygichuru/ytci-go/internal/handler"
	"github.com/bradleygichuru/ytci-go/internal/middleware"
	"github.com/bradleygichuru/ytci-go/internal/r2"
)

type CourseHandler struct {
	pool *pgxpool.Pool
	r2   r2.Store
}

func NewCourseHandler(pool *pgxpool.Pool, r2client r2.Store) *CourseHandler {
	return &CourseHandler{pool: pool, r2: r2client}
}

func (h *CourseHandler) GetCourseDetail(w http.ResponseWriter, r *http.Request) {
	courseID := r.PathValue("id")

	row := h.pool.QueryRow(r.Context(),
		`SELECT id, title, description, difficulty, image_url, pass_threshold, created_at
		 FROM courses WHERE id = $1 AND status = 'published'`, courseID)

	var id, title, difficulty, createdAt string
	var description, imageURL *string
	var passThreshold int
	err := row.Scan(&id, &title, &description, &difficulty, &imageURL, &passThreshold, &createdAt)
	if err == pgx.ErrNoRows {
		handler.WriteError(w, http.StatusNotFound, "NOT_FOUND", "course not found")
		return
	}
	if err != nil {
		handler.WriteError(w, http.StatusInternalServerError, "INTERNAL_ERROR", "failed to get course")
		return
	}

	lessonRows, err := h.pool.Query(r.Context(),
		`SELECT id, title, description, content_type, content_url, duration, display_order
		 FROM lessons WHERE course_id = $1 ORDER BY display_order`, courseID)
	if err != nil {
		handler.WriteError(w, http.StatusInternalServerError, "INTERNAL_ERROR", "failed to list lessons")
		return
	}
	defer lessonRows.Close()

	var lessons []lessonResponse
	for lessonRows.Next() {
		var l lessonResponse
		lessonRows.Scan(&l.ID, &l.Title, &l.Description, &l.ContentType, &l.ContentURL, &l.Duration, &l.DisplayOrder)
		lessons = append(lessons, l)
	}
	if err := lessonRows.Err(); err != nil {
		handler.WriteError(w, http.StatusInternalServerError, "INTERNAL_ERROR", "failed to iterate lessons")
		return
	}
	if lessons == nil {
		lessons = []lessonResponse{}
	}

	resp := map[string]any{
		"id":            id,
		"title":         title,
		"description":   description,
		"difficulty":    difficulty,
		"imageUrl":      imageURL,
		"passThreshold": passThreshold,
		"lessons":       lessons,
		"createdAt":     createdAt,
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(resp)
}

type lessonResponse struct {
	ID           string  `json:"id"`
	Title        string  `json:"title"`
	Description  *string `json:"description,omitempty"`
	ContentType  string  `json:"contentType"`
	ContentURL   *string `json:"contentUrl,omitempty"`
	Duration     *int    `json:"duration,omitempty"`
	DisplayOrder int     `json:"displayOrder"`
}

func (h *CourseHandler) ListLessons(w http.ResponseWriter, r *http.Request) {
	courseID := r.PathValue("id")
	rows, err := h.pool.Query(r.Context(),
		`SELECT id, title, description, content_type, content_url, duration, display_order
		 FROM lessons WHERE course_id = $1 ORDER BY display_order LIMIT 50`, courseID)
	if err != nil {
		handler.WriteError(w, http.StatusInternalServerError, "INTERNAL_ERROR", "failed to list lessons")
		return
	}
	defer rows.Close()

	var items []lessonResponse
	for rows.Next() {
		var i lessonResponse
		rows.Scan(&i.ID, &i.Title, &i.Description, &i.ContentType, &i.ContentURL, &i.Duration, &i.DisplayOrder)
		items = append(items, i)
	}
	if err := rows.Err(); err != nil {
		handler.WriteError(w, http.StatusInternalServerError, "INTERNAL_ERROR", "failed to iterate lessons")
		return
	}
	if items == nil {
		items = []lessonResponse{}
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(items)
}

func (h *CourseHandler) GetLesson(w http.ResponseWriter, r *http.Request) {
	courseID := r.PathValue("id")
	lessonID := r.PathValue("lessonId")

	var i lessonResponse
	err := h.pool.QueryRow(r.Context(),
		`SELECT id, title, description, content_type, content_url, duration, display_order
		 FROM lessons WHERE id = $1 AND course_id = $2`, lessonID, courseID,
	).Scan(&i.ID, &i.Title, &i.Description, &i.ContentType, &i.ContentURL, &i.Duration, &i.DisplayOrder)
	if err == pgx.ErrNoRows {
		handler.WriteError(w, http.StatusNotFound, "NOT_FOUND", "lesson not found")
		return
	}
	if err != nil {
		handler.WriteError(w, http.StatusInternalServerError, "INTERNAL_ERROR", "failed to get lesson")
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(i)
}

func (h *CourseHandler) MarkLessonComplete(w http.ResponseWriter, r *http.Request) {
	userID := middleware.UserID(r.Context())
	courseID := r.PathValue("id")
	lessonID := r.PathValue("lessonId")

	tag, err := h.pool.Exec(r.Context(),
		`UPDATE course_enrollments
		 SET completed_lesson_ids = CASE
		 	WHEN NOT completed_lesson_ids @> to_jsonb($3::text) THEN completed_lesson_ids || to_jsonb($3::text)
		 	ELSE completed_lesson_ids
		 END,
		 updated_at = now()
		 WHERE user_id = $1 AND course_id = $2`,
		userID, courseID, lessonID)
	if err != nil {
		handler.WriteError(w, http.StatusInternalServerError, "INTERNAL_ERROR", "failed to mark lesson complete")
		return
	}
	if tag.RowsAffected() == 0 {
		handler.WriteError(w, http.StatusNotFound, "NOT_FOUND", "enrollment not found")
		return
	}

	h.tryCompleteCourse(r.Context(), userID, courseID)

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{"status": "completed"})
}

func (h *CourseHandler) GetQuiz(w http.ResponseWriter, r *http.Request) {
	courseID := r.PathValue("id")

	var rawQuestions []byte
	var quizTitle string
	err := h.pool.QueryRow(r.Context(),
		`SELECT title, questions FROM quizzes WHERE course_id = $1 LIMIT 1`, courseID,
	).Scan(&quizTitle, &rawQuestions)
	if err == pgx.ErrNoRows {
		handler.WriteError(w, http.StatusNotFound, "NOT_FOUND", "quiz not found")
		return
	}
	if err != nil {
		handler.WriteError(w, http.StatusInternalServerError, "INTERNAL_ERROR", "failed to get quiz")
		return
	}

	var questions []map[string]any
	if err := json.Unmarshal(rawQuestions, &questions); err != nil {
		handler.WriteError(w, http.StatusInternalServerError, "INTERNAL_ERROR", "failed to parse quiz")
		return
	}

	for i := range questions {
		delete(questions[i], "correctIndex")
	}

	resp := map[string]any{
		"title":     quizTitle,
		"questions": questions,
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(resp)
}

type quizSubmitRequest struct {
	Answers []quizAnswer `json:"answers"`
}

type quizAnswer struct {
	QuestionIndex int    `json:"questionIndex"`
	Answer        string `json:"answer"`
}

func (h *CourseHandler) SubmitQuiz(w http.ResponseWriter, r *http.Request) {
	courseID := r.PathValue("id")
	userID := middleware.UserID(r.Context())
	r.Body = http.MaxBytesReader(w, r.Body, 1<<20)

	var req quizSubmitRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		handler.WriteError(w, http.StatusBadRequest, "INVALID_REQUEST", "invalid request body")
		return
	}

	var rawQuestions []byte
	var passThreshold int
	err := h.pool.QueryRow(r.Context(),
		`SELECT questions, pass_threshold FROM quizzes WHERE course_id = $1 LIMIT 1`, courseID,
	).Scan(&rawQuestions, &passThreshold)
	if err == pgx.ErrNoRows {
		handler.WriteError(w, http.StatusNotFound, "NOT_FOUND", "quiz not found")
		return
	}
	if err != nil {
		handler.WriteError(w, http.StatusInternalServerError, "INTERNAL_ERROR", "failed to get quiz")
		return
	}

	var questions []map[string]any
	if err := json.Unmarshal(rawQuestions, &questions); err != nil {
		handler.WriteError(w, http.StatusInternalServerError, "INTERNAL_ERROR", "failed to parse quiz")
		return
	}

	correctAnswers := 0
	totalQuestions := len(questions)
	var breakdown []map[string]any

	for i, q := range questions {
		isCorrect := false
		submitted := ""
		if i < len(req.Answers) {
			submitted = req.Answers[i].Answer
			if idx, ok := getFloat(q, "correctIndex"); ok {
				if opts, ok := q["options"].([]any); ok && int(idx) < len(opts) {
					if correctOpt, ok := opts[int(idx)].(string); ok {
						isCorrect = submitted == correctOpt
					}
				}
			}
		}
		if isCorrect {
			correctAnswers++
		}
		breakdown = append(breakdown, map[string]any{
			"questionIndex": i,
			"correct":       isCorrect,
			"yourAnswer":    submitted,
		})
	}

	score := 0
	if totalQuestions > 0 {
		score = correctAnswers * 100 / totalQuestions
	}
	passed := score >= passThreshold

	attemptKey := time.Now().UTC().Format(time.RFC3339)
	attemptJSON, _ := json.Marshal(map[string]any{
		"score":  score,
		"passed": passed,
	})

	_, err = h.pool.Exec(r.Context(),
		`UPDATE course_enrollments
		 SET quiz_attempts = quiz_attempts || jsonb_build_object($3::text, $4::jsonb),
		     updated_at = now()
		 WHERE user_id = $1 AND course_id = $2`,
		userID, courseID, attemptKey, string(attemptJSON))
	if err != nil {
		handler.WriteError(w, http.StatusInternalServerError, "INTERNAL_ERROR", "failed to record quiz attempt")
		return
	}

	alreadyCompleted := false
	_ = h.pool.QueryRow(r.Context(),
		`SELECT completed_at IS NOT NULL FROM course_enrollments WHERE user_id = $1 AND course_id = $2`,
		userID, courseID,
	).Scan(&alreadyCompleted)

	if passed && !alreadyCompleted {
		h.tryCompleteCourse(r.Context(), userID, courseID)
	}

	resp := map[string]any{
		"score":          score,
		"passed":         passed,
		"totalQuestions": totalQuestions,
		"correctAnswers": correctAnswers,
		"breakdown":      breakdown,
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(resp)
}

func (h *CourseHandler) tryCompleteCourse(ctx context.Context, userID, courseID string) {
	var hasPassingAttempt bool
	_ = h.pool.QueryRow(ctx,
		`SELECT EXISTS(
			SELECT 1 FROM course_enrollments
			WHERE user_id = $1 AND course_id = $2
			AND quiz_attempts IS NOT NULL AND quiz_attempts != '{}'::jsonb
		)`, userID, courseID,
	).Scan(&hasPassingAttempt)

	if !hasPassingAttempt {
		return
	}

	var allLessonsComplete bool
	_ = h.pool.QueryRow(ctx,
		`SELECT (
			SELECT count(*) FROM lessons WHERE course_id = $1
		) = COALESCE(jsonb_array_length(e.completed_lesson_ids), 0)
		FROM course_enrollments e
		WHERE e.user_id = $2 AND e.course_id = $1`,
		courseID, userID,
	).Scan(&allLessonsComplete)

	if !allLessonsComplete {
		return
	}

	var courseTitle string
	var badgeName *string
	var badgeIconURL *string
	var displayName string
	var enrollmentID string
	_ = h.pool.QueryRow(ctx,
		`SELECT c.title, c.badge_name, c.badge_icon_url, COALESCE(up.display_name, 'Explorer'), e.id
		 FROM courses c
		 JOIN course_enrollments e ON e.course_id = c.id AND e.user_id = $1
		 LEFT JOIN user_profiles up ON up.user_id = $1
		 WHERE c.id = $2`,
		userID, courseID,
	).Scan(&courseTitle, &badgeName, &badgeIconURL, &displayName, &enrollmentID)

	var certURL *string
	if h.r2 != nil && enrollmentID != "" {
		certURL = h.generateCertificate(ctx, enrollmentID, displayName, courseTitle)
	}

	_, err := h.pool.Exec(ctx,
		`UPDATE course_enrollments
		 SET completed_at = now(), certificate_url = $3, updated_at = now()
		 WHERE user_id = $1 AND course_id = $2 AND completed_at IS NULL`,
		userID, courseID, certURL)
	if err != nil {
		return
	}

	if badgeName != nil && *badgeName != "" {
		bIcon := ""
		if badgeIconURL != nil {
			bIcon = *badgeIconURL
		}
		_, _ = h.pool.Exec(ctx,
			`INSERT INTO badges (user_id, badge_name, badge_icon_url, source_type, source_id, source_title)
			 VALUES ($1, $2, $3, 'course', $4, $5)`,
			userID, *badgeName, bIcon, courseID, courseTitle)
	}
}

func (h *CourseHandler) generateCertificate(ctx context.Context, enrollmentID, userName, courseTitle string) *string {
	if h.r2 == nil {
		return nil
	}

	pdf := gofpdf.New("L", "mm", "A4", "")
	pdf.AddPage()

	pdf.SetFont("Helvetica", "B", 28)
	pdf.SetTextColor(21, 66, 18)
	pdf.CellFormat(0, 15, "YTC Explorer", "", 1, "C", false, 0, "")

	pdf.SetFont("Helvetica", "B", 22)
	pdf.SetTextColor(120, 89, 0)
	pdf.CellFormat(0, 15, "Certificate of Completion", "", 1, "C", false, 0, "")

	pdf.Ln(10)

	pdf.SetFont("Helvetica", "", 16)
	pdf.SetTextColor(25, 28, 29)
	pdf.CellFormat(0, 10, "This certifies that", "", 1, "C", false, 0, "")

	pdf.SetFont("Helvetica", "B", 24)
	pdf.CellFormat(0, 15, userName, "", 1, "C", false, 0, "")

	pdf.Ln(5)

	pdf.SetFont("Helvetica", "", 16)
	pdf.CellFormat(0, 10, "has successfully completed the course", "", 1, "C", false, 0, "")

	pdf.SetFont("Helvetica", "B", 20)
	pdf.SetTextColor(21, 66, 18)
	pdf.CellFormat(0, 15, courseTitle, "", 1, "C", false, 0, "")

	pdf.Ln(10)

	pdf.SetFont("Helvetica", "", 14)
	pdf.SetTextColor(25, 28, 29)
	completedDate := time.Now().Format("January 2, 2006")
	pdf.CellFormat(0, 10, "Completed on: "+completedDate, "", 1, "C", false, 0, "")

	var buf bytes.Buffer
	if err := pdf.Output(&buf); err != nil {
		return nil
	}

	objectKey := fmt.Sprintf("certificates/%s.pdf", enrollmentID)
	contentType := "application/pdf"
	if err := h.r2.PutObject(ctx, objectKey, &buf, contentType); err != nil {
		return nil
	}

	return &objectKey
}

func (h *CourseHandler) GetCertificate(w http.ResponseWriter, r *http.Request) {
	userID := middleware.UserID(r.Context())
	courseID := r.PathValue("id")

	var enrollID, courseTitle, completedAt string
	var certKey *string
	var userName string
	err := h.pool.QueryRow(r.Context(),
		`SELECT e.id, c.title, e.completed_at::text, e.certificate_url,
			COALESCE(up.display_name, 'Explorer')
		 FROM course_enrollments e
		 JOIN courses c ON c.id = e.course_id
		 LEFT JOIN user_profiles up ON up.user_id = e.user_id
		 WHERE e.course_id = $1 AND e.user_id = $2 AND e.completed_at IS NOT NULL
		 LIMIT 1`, courseID, userID,
	).Scan(&enrollID, &courseTitle, &completedAt, &certKey, &userName)
	if err == pgx.ErrNoRows {
		handler.WriteError(w, http.StatusNotFound, "NOT_FOUND", "certificate not found")
		return
	}
	if err != nil {
		handler.WriteError(w, http.StatusInternalServerError, "INTERNAL_ERROR", "failed to get certificate")
		return
	}

	var certURL *string
	if h.r2 != nil && certKey != nil {
		signedURL, err := h.r2.PresignedGetURL(r.Context(), *certKey, 15*time.Minute)
		if err == nil {
			certURL = &signedURL
		}
	}

	resp := map[string]any{
		"id":             enrollID,
		"courseTitle":    courseTitle,
		"userName":       userName,
		"completedAt":    completedAt,
		"certificateUrl": certURL,
		"expiresAt":      nil,
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(resp)
}

func getFloat(m map[string]any, key string) (float64, bool) {
	v, ok := m[key]
	if !ok {
		return 0, false
	}
	switch n := v.(type) {
	case float64:
		return n, true
	case json.Number:
		f, err := n.Float64()
		return f, err == nil
	}
	return 0, false
}
