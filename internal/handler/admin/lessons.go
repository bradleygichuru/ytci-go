package admin

import (
	"encoding/json"
	"net/http"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/bradleygichuru/ytci-go/internal/handler"
)

type LessonHandler struct {
	pool *pgxpool.Pool
}

func NewLessonHandler(pool *pgxpool.Pool) *LessonHandler {
	return &LessonHandler{pool: pool}
}

func (h *LessonHandler) Create(w http.ResponseWriter, r *http.Request) {
	courseID := r.PathValue("id")

	var req struct {
		Title        string  `json:"title"`
		Description  *string `json:"description,omitempty"`
		ContentType  string  `json:"contentType"`
		ContentURL   *string `json:"contentUrl,omitempty"`
		Duration     *int    `json:"duration,omitempty"`
		DisplayOrder int     `json:"displayOrder"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		handler.WriteError(w, http.StatusBadRequest, "INVALID_REQUEST", "invalid request body")
		return
	}
	if req.ContentType != "video" && req.ContentType != "pdf" && req.ContentType != "text" {
		handler.WriteError(w, http.StatusBadRequest, "INVALID_REQUEST", "contentType must be video, pdf, or text")
		return
	}

	var id string
	err := h.pool.QueryRow(r.Context(),
		`INSERT INTO lessons (course_id, title, description, content_type, content_url, duration, display_order)
		 VALUES ($1, $2, $3, $4, $5, $6, $7) RETURNING id`,
		courseID, req.Title, req.Description, req.ContentType, req.ContentURL, req.Duration, req.DisplayOrder,
	).Scan(&id)
	if err != nil {
		handler.WriteServerError(w, r, "INTERNAL_ERROR", "failed to create lesson", err)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	json.NewEncoder(w).Encode(map[string]string{"id": id, "status": "created"})
}

func (h *LessonHandler) Update(w http.ResponseWriter, r *http.Request) {
	lessonID := r.PathValue("lessonId")

	var req struct {
		Title        *string `json:"title,omitempty"`
		Description  *string `json:"description,omitempty"`
		ContentType  *string `json:"contentType,omitempty"`
		ContentURL   *string `json:"contentUrl,omitempty"`
		Duration     *int    `json:"duration,omitempty"`
		DisplayOrder *int    `json:"displayOrder,omitempty"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		handler.WriteError(w, http.StatusBadRequest, "INVALID_REQUEST", "invalid request body")
		return
	}

	_, err := h.pool.Exec(r.Context(),
		`UPDATE lessons SET
		 title = COALESCE($2::text, title),
		 description = COALESCE($3::text, description),
		 content_type = COALESCE($4::text, content_type),
		 content_url = COALESCE($5::text, content_url),
		 duration = COALESCE($6::int, duration),
		 display_order = COALESCE($7::int, display_order),
		 updated_at = now()
		 WHERE id = $1`,
		lessonID, req.Title, req.Description, req.ContentType, req.ContentURL, req.Duration, req.DisplayOrder)
	if err != nil {
		handler.WriteServerError(w, r, "INTERNAL_ERROR", "failed to update lesson", err)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{"status": "updated"})
}

func (h *LessonHandler) Delete(w http.ResponseWriter, r *http.Request) {
	lessonID := r.PathValue("lessonId")

	tag, err := h.pool.Exec(r.Context(), `DELETE FROM lessons WHERE id = $1`, lessonID)
	if err != nil {
		handler.WriteServerError(w, r, "INTERNAL_ERROR", "failed to delete lesson", err)
		return
	}
	if tag.RowsAffected() == 0 {
		handler.WriteError(w, http.StatusNotFound, "NOT_FOUND", "lesson not found")
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{"status": "deleted"})
}
