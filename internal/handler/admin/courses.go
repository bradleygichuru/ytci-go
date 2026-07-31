package admin

import (
	"encoding/json"
	"net/http"
	"time"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/bradleygichuru/ytci-go/internal/db/gen"
	"github.com/bradleygichuru/ytci-go/internal/handler"
	"github.com/bradleygichuru/ytci-go/internal/middleware"
	"github.com/bradleygichuru/ytci-go/internal/model"
)

type CourseHandler struct {
	pool *pgxpool.Pool
}

func NewCourseHandler(pool *pgxpool.Pool) *CourseHandler {
	return &CourseHandler{pool: pool}
}

func (h *CourseHandler) List(w http.ResponseWriter, r *http.Request) {
	rows, err := h.pool.Query(r.Context(),
		`SELECT c.id, c.title, c.description, c.difficulty, c.status,
		 c.image_url, c.pass_threshold, c.badge_name, c.badge_icon_url,
		 c.created_at, c.updated_at,
		 COALESCE(
			(SELECT json_agg(json_build_object(
				'id', l.id, 'title', l.title, 'type', l.content_type,
				'duration', l.duration, 'url', l.content_url
			) ORDER BY l.display_order)
			FROM lessons l WHERE l.course_id = c.id),
			'[]'::json
		 ) AS lessons,
		 COALESCE(
			(SELECT qz.questions FROM quizzes qz WHERE qz.course_id = c.id),
			'[]'::jsonb
		 ) AS quiz_questions,
		 COALESCE((SELECT COUNT(*) FROM course_enrollments WHERE course_id = c.id), 0) AS enrollment_count,
		 COALESCE((SELECT COUNT(*) FROM course_enrollments WHERE course_id = c.id AND completed_at IS NOT NULL), 0) AS completion_count
		 FROM courses c ORDER BY c.created_at DESC LIMIT 50`)
	if err != nil {
		handler.WriteServerError(w, r, "INTERNAL_ERROR", "failed to list courses", err)
		return
	}
	defer rows.Close()

	type item struct {
		ID              string           `json:"id"`
		Title           string           `json:"title"`
		Description     *string          `json:"description"`
		Difficulty      string           `json:"difficulty"`
		Status          string           `json:"status"`
		ImageURL        *string          `json:"imageUrl"`
		PassThreshold   *int             `json:"passThreshold"`
		BadgeName       *string          `json:"badgeName"`
		BadgeIconURL    *string          `json:"badgeIconUrl"`
		CreatedAt       string           `json:"createdAt"`
		UpdatedAt       string           `json:"updatedAt"`
		Lessons         json.RawMessage  `json:"lessons"`
		LessonCount     int              `json:"lessonCount"`
		QuizQuestions   json.RawMessage  `json:"quizQuestions"`
		EnrollmentCount int              `json:"enrollmentCount"`
		CompletionCount int              `json:"completionCount"`
	}
	var items []item
	for rows.Next() {
		var i item
		var createdAt, updatedAt pgtype.Timestamp
		var lessonsJSON, quizJSON json.RawMessage
		rows.Scan(&i.ID, &i.Title, &i.Description, &i.Difficulty, &i.Status,
			&i.ImageURL, &i.PassThreshold, &i.BadgeName, &i.BadgeIconURL,
			&createdAt, &updatedAt,
			&lessonsJSON, &quizJSON, &i.EnrollmentCount, &i.CompletionCount)
		i.CreatedAt = createdAt.Time.Format(time.RFC3339)
		i.UpdatedAt = updatedAt.Time.Format(time.RFC3339)
		i.Lessons = lessonsJSON
		i.QuizQuestions = quizJSON
		// Count lessons from JSON array length
		if len(lessonsJSON) > 2 {
			var arr []any
			if json.Unmarshal(lessonsJSON, &arr) == nil {
				i.LessonCount = len(arr)
			}
		}
		items = append(items, i)
	}
	if err := rows.Err(); err != nil {
		handler.WriteServerError(w, r, "INTERNAL_ERROR", "failed to iterate courses", err)
		return
	}
	if items == nil {
		items = []item{}
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(model.Paginated[item]{Items: items, HasMore: false})
}

func (h *CourseHandler) ListMobile(w http.ResponseWriter, r *http.Request) {
	queries := gen.New(h.pool)
	courses, err := queries.ListPublishedCourses(r.Context(), 50)
	if err != nil {
		handler.WriteError(w, http.StatusInternalServerError, "INTERNAL_ERROR", "failed to list courses")
		return
	}
	if courses == nil {
		courses = []gen.ListPublishedCoursesRow{}
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(courses)
}

func (h *CourseHandler) Get(w http.ResponseWriter, r *http.Request) {
	courseID := r.PathValue("id")
	row := h.pool.QueryRow(r.Context(),
		`SELECT id, title, description, difficulty, status, pass_threshold, created_at FROM courses WHERE id = $1`, courseID)
	var id, title, desc, diff, status string
	var threshold int
	var createdAt string
	if err := row.Scan(&id, &title, &desc, &diff, &status, &threshold, &createdAt); err != nil {
		handler.WriteError(w, http.StatusNotFound, "NOT_FOUND", "course not found")
		return
	}

	lessonRows, err := h.pool.Query(r.Context(),
		`SELECT id, title, content_type, duration, display_order FROM lessons WHERE course_id = $1 ORDER BY display_order`, courseID)
	lessons := []map[string]any{}
	if err == nil {
		defer lessonRows.Close()
		for lessonRows.Next() {
			var lid, ltitle, ctype string
			var dur, order int
			lessonRows.Scan(&lid, &ltitle, &ctype, &dur, &order)
			lessons = append(lessons, map[string]any{
				"id": lid, "title": ltitle, "contentType": ctype,
				"duration": dur, "displayOrder": order,
			})
		}
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]any{
		"id": id, "title": title, "description": desc,
		"difficulty": diff, "status": status,
		"passThreshold": threshold, "createdAt": createdAt,
		"lessons": lessons,
	})
}

func (h *CourseHandler) Create(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Title         string  `json:"title"`
		Description   string  `json:"description,omitempty"`
		Difficulty    string  `json:"difficulty"`
		ImageURL      *string `json:"imageUrl,omitempty"`
		PassThreshold *int    `json:"passThreshold,omitempty"`
		Status        *string `json:"status,omitempty"`
		BadgeName     *string `json:"badgeName,omitempty"`
		BadgeIconURL  *string `json:"badgeIconUrl,omitempty"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		handler.WriteError(w, http.StatusBadRequest, "INVALID_REQUEST", "invalid request body")
		return
	}

	status := "draft"
	if req.Status != nil && *req.Status != "" {
		status = *req.Status
	}
	threshold := 70
	if req.PassThreshold != nil {
		threshold = *req.PassThreshold
	}

	var id string
	if err := h.pool.QueryRow(r.Context(),
		`INSERT INTO courses (title, description, difficulty, image_url, pass_threshold, status, badge_name, badge_icon_url, created_by)
		 VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9) RETURNING id`,
		req.Title, req.Description, req.Difficulty, req.ImageURL, threshold, status,
		req.BadgeName, req.BadgeIconURL, middleware.UserID(r.Context()),
	).Scan(&id); err != nil {
		handler.WriteError(w, http.StatusInternalServerError, "INTERNAL_ERROR", "failed to create course")
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	json.NewEncoder(w).Encode(map[string]string{"id": id, "status": status})
}

func (h *CourseHandler) Update(w http.ResponseWriter, r *http.Request) {
	courseID := r.PathValue("id")
	var req struct {
		Title         *string `json:"title,omitempty"`
		Description   *string `json:"description,omitempty"`
		Difficulty    *string `json:"difficulty,omitempty"`
		Status        *string `json:"status,omitempty"`
		ImageURL      *string `json:"imageUrl,omitempty"`
		PassThreshold *int    `json:"passThreshold,omitempty"`
		BadgeName     *string `json:"badgeName,omitempty"`
		BadgeIconURL  *string `json:"badgeIconUrl,omitempty"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		handler.WriteError(w, http.StatusBadRequest, "INVALID_REQUEST", "invalid request body")
		return
	}

	_, err := h.pool.Exec(r.Context(),
		`UPDATE courses SET
		 title = CASE WHEN $2::text != '' THEN $2 ELSE title END,
		 description = COALESCE($3::text, description),
		 difficulty = CASE WHEN $4::text != '' THEN $4 ELSE difficulty END,
		 status = CASE WHEN $5::text != '' THEN $5 ELSE status END,
		 image_url = COALESCE($6::text, image_url),
		 pass_threshold = COALESCE($7::int, pass_threshold),
		 badge_name = COALESCE($8::text, badge_name),
		 badge_icon_url = COALESCE($9::text, badge_icon_url),
		 updated_at = now()
		 WHERE id = $1`,
		courseID,
		valOrEmpty(req.Title),
		req.Description,
		valOrEmpty(req.Difficulty),
		valOrEmpty(req.Status),
		req.ImageURL,
		req.PassThreshold,
		req.BadgeName,
		req.BadgeIconURL,
	)
	if err != nil {
		handler.WriteError(w, http.StatusInternalServerError, "INTERNAL_ERROR", "failed to update course")
		return
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{"status": "updated"})
}

func (h *CourseHandler) Delete(w http.ResponseWriter, r *http.Request) {
	courseID := r.PathValue("id")
	_, err := h.pool.Exec(r.Context(), `DELETE FROM courses WHERE id = $1`, courseID)
	if err != nil {
		handler.WriteError(w, http.StatusInternalServerError, "INTERNAL_ERROR", "failed to delete course")
		return
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{"status": "deleted"})
}
