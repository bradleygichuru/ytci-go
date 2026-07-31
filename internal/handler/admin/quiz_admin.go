package admin

import (
	"encoding/json"
	"net/http"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/bradleygichuru/ytci-go/internal/handler"
)

type QuizAdminHandler struct {
	pool *pgxpool.Pool
}

func NewQuizAdminHandler(pool *pgxpool.Pool) *QuizAdminHandler {
	return &QuizAdminHandler{pool: pool}
}

func (h *QuizAdminHandler) Upsert(w http.ResponseWriter, r *http.Request) {
	courseID := r.PathValue("id")

	var req struct {
		Title         string `json:"title"`
		Questions     []any  `json:"questions"`
		PassThreshold *int   `json:"passThreshold,omitempty"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		handler.WriteError(w, http.StatusBadRequest, "INVALID_REQUEST", "invalid request body")
		return
	}
	if req.Title == "" {
		handler.WriteError(w, http.StatusBadRequest, "INVALID_REQUEST", "title is required")
		return
	}
	if len(req.Questions) == 0 {
		handler.WriteError(w, http.StatusBadRequest, "INVALID_REQUEST", "at least one question is required")
		return
	}

	questionsJSON, err := json.Marshal(req.Questions)
	if err != nil {
		handler.WriteError(w, http.StatusBadRequest, "INVALID_REQUEST", "invalid questions format")
		return
	}

	threshold := 70
	if req.PassThreshold != nil {
		threshold = *req.PassThreshold
	}

	var quizID string
	err = h.pool.QueryRow(r.Context(),
		`INSERT INTO quizzes (course_id, title, questions, pass_threshold)
		 VALUES ($1, $2, $3, $4)
		 ON CONFLICT (course_id) DO UPDATE SET
		 title = EXCLUDED.title,
		 questions = EXCLUDED.questions,
		 pass_threshold = EXCLUDED.pass_threshold
		 RETURNING id`,
		courseID, req.Title, questionsJSON, threshold,
	).Scan(&quizID)
	if err != nil {
		handler.WriteError(w, http.StatusInternalServerError, "INTERNAL_ERROR", "failed to upsert quiz")
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{"id": quizID, "status": "created"})
}

func (h *QuizAdminHandler) Delete(w http.ResponseWriter, r *http.Request) {
	courseID := r.PathValue("id")

	_, err := h.pool.Exec(r.Context(), `DELETE FROM quizzes WHERE course_id = $1`, courseID)
	if err == pgx.ErrNoRows {
		handler.WriteError(w, http.StatusNotFound, "NOT_FOUND", "quiz not found")
		return
	}
	if err != nil {
		handler.WriteError(w, http.StatusInternalServerError, "INTERNAL_ERROR", "failed to delete quiz")
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{"status": "deleted"})
}
