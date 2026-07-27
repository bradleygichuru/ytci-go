package admin

import (
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/bradleygichuru/ytci-go/internal/handler"
	"github.com/bradleygichuru/ytci-go/internal/middleware"
)

type question struct {
	Q          string   `json:"q"`
	Options    []string `json:"options"`
	CorrectIdx int      `json:"correct_index"`
}

type QuizHandler struct {
	pool *pgxpool.Pool
}

func NewQuizHandler(pool *pgxpool.Pool) *QuizHandler {
	return &QuizHandler{pool: pool}
}

func (h *QuizHandler) Evaluate(w http.ResponseWriter, r *http.Request) {
	var req struct {
		QuizID  string `json:"quizId"`
		Answers []struct {
			QuestionIndex int `json:"questionIndex"`
			ChosenIndex   int `json:"chosenIndex"`
		} `json:"answers"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		handler.WriteError(w, http.StatusBadRequest, "INVALID_REQUEST", "invalid request body")
		return
	}

	var questionsJSON string
	err := h.pool.QueryRow(r.Context(),
		`SELECT questions::text FROM quizzes WHERE id = $1`, req.QuizID,
	).Scan(&questionsJSON)
	if err != nil {
		handler.WriteError(w, http.StatusNotFound, "NOT_FOUND", "quiz not found")
		return
	}

	var storedQuestions []question
	if err := json.Unmarshal([]byte(questionsJSON), &storedQuestions); err != nil {
		handler.WriteError(w, http.StatusInternalServerError, "INTERNAL_ERROR", "invalid quiz data")
		return
	}

	userID := middleware.UserID(r.Context())
	correct := 0
	total := len(storedQuestions)

	for _, a := range req.Answers {
		if a.QuestionIndex < len(storedQuestions) && storedQuestions[a.QuestionIndex].CorrectIdx == a.ChosenIndex {
			correct++
		}
	}

	score := 0
	if total > 0 {
		score = correct * 100 / total
	}
	passed := score >= 70

	now := time.Now().UTC().Format(time.RFC3339)
	h.pool.Exec(r.Context(),
		`UPDATE course_enrollments
		 SET quiz_attempts = jsonb_set(COALESCE(quiz_attempts, '{}'::jsonb), array[$1::text], $2::jsonb, true)
		 WHERE user_id = $3 AND course_id IN (SELECT course_id FROM quizzes WHERE id = $1)`,
		req.QuizID,
		fmt.Sprintf(`{"score":%d,"passed":%v,"attempted_at":"%s"}`, score, passed, now),
		userID)

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]any{
		"score": fmt.Sprintf("%d/%d", correct, total), "passed": passed,
		"correct": correct, "total": total,
	})
}
