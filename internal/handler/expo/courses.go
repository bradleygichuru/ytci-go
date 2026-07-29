package expo

import (
	"encoding/json"
	"net/http"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/bradleygichuru/ytci-go/internal/handler"
	"github.com/bradleygichuru/ytci-go/internal/middleware"
)

type CourseHandler struct {
	pool *pgxpool.Pool
}

func NewCourseHandler(pool *pgxpool.Pool) *CourseHandler {
	return &CourseHandler{pool: pool}
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
		delete(questions[i], "correctAnswer")
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
			if correct, ok := q["correctAnswer"].(string); ok {
				isCorrect = submitted == correct
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

	resp := map[string]any{
		"score":          score,
		"passed":         score >= passThreshold,
		"totalQuestions": totalQuestions,
		"correctAnswers": correctAnswers,
		"breakdown":      breakdown,
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(resp)
}

func (h *CourseHandler) GetCertificate(w http.ResponseWriter, r *http.Request) {
	userID := middleware.UserID(r.Context())
	courseID := r.PathValue("id")

	var enrollID, courseTitle, completedAt string
	var certURL *string
	var userName string
	err := h.pool.QueryRow(r.Context(),
		`SELECT e.id, c.title, e.completed_at, e.certificate_url,
			COALESCE(up.display_name, 'Explorer')
		 FROM course_enrollments e
		 JOIN courses c ON c.id = e.course_id
		 LEFT JOIN user_profiles up ON up.user_id = e.user_id
		 WHERE e.course_id = $1 AND e.user_id = $2 AND e.completed_at IS NOT NULL
		 LIMIT 1`, courseID, userID,
	).Scan(&enrollID, &courseTitle, &completedAt, &certURL, &userName)
	if err == pgx.ErrNoRows {
		handler.WriteError(w, http.StatusNotFound, "NOT_FOUND", "certificate not found")
		return
	}
	if err != nil {
		handler.WriteError(w, http.StatusInternalServerError, "INTERNAL_ERROR", "failed to get certificate")
		return
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
