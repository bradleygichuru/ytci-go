package competition

import (
	"net/http"
	"strings"
	"time"

	"github.com/bradleygichuru/ytci-go/internal/handler"
)

// ---------------------------------------------------------------------------
// Judges
// ---------------------------------------------------------------------------

type judgeRequest struct {
	UserID      *string `json:"userId"`
	DisplayName *string `json:"displayName"`
	Email       *string `json:"email"`
	Status      *string `json:"status"`
}

func (h *Handler) ListJudges(w http.ResponseWriter, r *http.Request) {
	editionID := strings.TrimSpace(r.PathValue("editionId"))
	rows, err := h.pool.Query(r.Context(), judgeColumns+` WHERE edition_id = $1 ORDER BY created_at`, editionID)
	if err != nil {
		handler.WriteServerError(w, r, "INTERNAL_ERROR", "failed to list judges", err)
		return
	}
	defer rows.Close()
	out := []judge{}
	for rows.Next() {
		j, err := scanJudge(rows)
		if err != nil {
			handler.WriteServerError(w, r, "INTERNAL_ERROR", "failed to read judges", err)
			return
		}
		out = append(out, j)
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": out})
}

func (h *Handler) CreateJudge(w http.ResponseWriter, r *http.Request) {
	editionID := strings.TrimSpace(r.PathValue("editionId"))
	if !validUUID(editionID) || !h.editionExists(r, editionID) {
		handler.WriteError(w, http.StatusNotFound, "NOT_FOUND", "edition not found")
		return
	}
	var req judgeRequest
	if !decodeBody(w, r, 32<<10, &req) {
		return
	}
	var v validationCollector
	name := ""
	if req.DisplayName == nil || strings.TrimSpace(*req.DisplayName) == "" {
		v.add("displayName", "is required")
	} else {
		name = strings.TrimSpace(*req.DisplayName)
	}
	status := "invited"
	if req.Status != nil {
		status = normalizeKey(*req.Status)
		if !judgeStatuses[status] {
			v.add("status", "must be invited, active, recused or inactive")
		}
	}
	var userID *string
	if req.UserID != nil && strings.TrimSpace(*req.UserID) != "" {
		userID = textPtr(req.UserID)
		if !h.userExists(r, *userID) {
			v.add("userId", "does not match a user")
		}
	}
	if v.write(w) {
		return
	}
	j, err := scanJudge(h.pool.QueryRow(r.Context(), `
INSERT INTO competition_judges (edition_id, user_id, display_name, email, status)
VALUES ($1,$2,$3,$4,$5)
RETURNING id::text, edition_id::text, user_id, display_name, email, status, created_at`,
		editionID, userID, name, textPtr(req.Email), status))
	if err != nil {
		handler.WriteServerError(w, r, "INTERNAL_ERROR", "failed to create judge", err)
		return
	}
	writeJSON(w, http.StatusCreated, j)
}

func (h *Handler) UpdateJudge(w http.ResponseWriter, r *http.Request) {
	id := strings.TrimSpace(r.PathValue("judgeId"))
	if !validUUID(id) {
		handler.WriteError(w, http.StatusNotFound, "NOT_FOUND", "judge not found")
		return
	}
	var req judgeRequest
	if !decodeBody(w, r, 32<<10, &req) {
		return
	}
	sets := []string{}
	args := []any{id}
	add := func(col string, val any) {
		args = append(args, val)
		sets = append(sets, col+" = $"+itoa(len(args)))
	}
	var v validationCollector
	if req.DisplayName != nil {
		name := strings.TrimSpace(*req.DisplayName)
		if name == "" {
			v.add("displayName", "must not be empty")
		} else {
			add("display_name", name)
		}
	}
	if req.Email != nil {
		add("email", textPtr(req.Email))
	}
	if req.UserID != nil {
		userID := textPtr(req.UserID)
		if userID != nil && !h.userExists(r, *userID) {
			v.add("userId", "does not match a user")
		} else {
			add("user_id", userID)
		}
	}
	if req.Status != nil {
		status := normalizeKey(*req.Status)
		if !judgeStatuses[status] {
			v.add("status", "must be invited, active, recused or inactive")
		} else {
			add("status", status)
		}
	}
	if len(sets) == 0 {
		v.add("body", "no fields to update")
	}
	if v.write(w) {
		return
	}
	j, err := scanJudge(h.pool.QueryRow(r.Context(),
		"UPDATE competition_judges SET "+strings.Join(sets, ", ")+` WHERE id = $1
RETURNING id::text, edition_id::text, user_id, display_name, email, status, created_at`, args...))
	if err != nil {
		if isNoRows(err) {
			handler.WriteError(w, http.StatusNotFound, "NOT_FOUND", "judge not found")
			return
		}
		handler.WriteServerError(w, r, "INTERNAL_ERROR", "failed to update judge", err)
		return
	}
	writeJSON(w, http.StatusOK, j)
}

func (h *Handler) DeleteJudge(w http.ResponseWriter, r *http.Request) {
	if _, err := h.pool.Exec(r.Context(), `DELETE FROM competition_judges WHERE id = $1`, strings.TrimSpace(r.PathValue("judgeId"))); err != nil {
		handler.WriteServerError(w, r, "INTERNAL_ERROR", "failed to delete judge", err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// ---------------------------------------------------------------------------
// Assignments
// ---------------------------------------------------------------------------

type assignmentRequest struct {
	CategoryID string  `json:"categoryId"`
	JudgeID    string  `json:"judgeId"`
	Status     *string `json:"status"`
}

func (h *Handler) ListAssignments(w http.ResponseWriter, r *http.Request) {
	editionID := strings.TrimSpace(r.PathValue("editionId"))
	rows, err := h.pool.Query(r.Context(), assignmentColumns+` WHERE edition_id = $1 ORDER BY assigned_at`, editionID)
	if err != nil {
		handler.WriteServerError(w, r, "INTERNAL_ERROR", "failed to list assignments", err)
		return
	}
	defer rows.Close()
	out := []assignment{}
	for rows.Next() {
		a, err := scanAssignment(rows)
		if err != nil {
			handler.WriteServerError(w, r, "INTERNAL_ERROR", "failed to read assignments", err)
			return
		}
		out = append(out, a)
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": out})
}

func (h *Handler) CreateAssignment(w http.ResponseWriter, r *http.Request) {
	editionID := strings.TrimSpace(r.PathValue("editionId"))
	if !validUUID(editionID) || !h.editionExists(r, editionID) {
		handler.WriteError(w, http.StatusNotFound, "NOT_FOUND", "edition not found")
		return
	}
	var req assignmentRequest
	if !decodeBody(w, r, 32<<10, &req) {
		return
	}
	var v validationCollector
	if !validUUID(req.CategoryID) {
		v.add("categoryId", "must be a UUID")
	}
	if !validUUID(req.JudgeID) {
		v.add("judgeId", "must be a UUID")
	}
	status := "assigned"
	if req.Status != nil {
		status = normalizeKey(*req.Status)
		if !assignmentStatuses[status] {
			v.add("status", "must be assigned, recused or completed")
		}
	}
	if v.write(w) {
		return
	}
	var categoryEdition, judgeEdition string
	if err := h.pool.QueryRow(r.Context(), `SELECT edition_id::text FROM competition_categories WHERE id = $1`, req.CategoryID).Scan(&categoryEdition); err != nil || categoryEdition != editionID {
		handler.WriteError(w, http.StatusBadRequest, "INVALID_CATEGORY", "category belongs to another edition")
		return
	}
	if err := h.pool.QueryRow(r.Context(), `SELECT edition_id::text FROM competition_judges WHERE id = $1`, req.JudgeID).Scan(&judgeEdition); err != nil || judgeEdition != editionID {
		handler.WriteError(w, http.StatusBadRequest, "INVALID_JUDGE", "judge belongs to another edition")
		return
	}
	a, err := scanAssignment(h.pool.QueryRow(r.Context(), `
INSERT INTO competition_judge_assignments (edition_id, category_id, judge_id, status)
VALUES ($1,$2,$3,$4)
RETURNING id::text, edition_id::text, category_id::text, judge_id::text, status, assigned_at`,
		editionID, req.CategoryID, req.JudgeID, status))
	if err != nil {
		if isUniqueViolation(err) {
			handler.WriteError(w, http.StatusConflict, "ASSIGNMENT_EXISTS", "this judge is already assigned to the category")
			return
		}
		handler.WriteServerError(w, r, "INTERNAL_ERROR", "failed to create assignment", err)
		return
	}
	writeJSON(w, http.StatusCreated, a)
}

func (h *Handler) UpdateAssignment(w http.ResponseWriter, r *http.Request) {
	id := strings.TrimSpace(r.PathValue("assignmentId"))
	var req assignmentRequest
	if !decodeBody(w, r, 32<<10, &req) {
		return
	}
	if req.Status == nil {
		handler.WriteError(w, http.StatusBadRequest, "INVALID_REQUEST", "status is required")
		return
	}
	status := normalizeKey(*req.Status)
	if !assignmentStatuses[status] {
		handler.WriteError(w, http.StatusBadRequest, "INVALID_REQUEST", "must be assigned, recused or completed")
		return
	}
	a, err := scanAssignment(h.pool.QueryRow(r.Context(), `
UPDATE competition_judge_assignments SET status = $2 WHERE id = $1
RETURNING id::text, edition_id::text, category_id::text, judge_id::text, status, assigned_at`, id, status))
	if err != nil {
		if isNoRows(err) {
			handler.WriteError(w, http.StatusNotFound, "NOT_FOUND", "assignment not found")
			return
		}
		handler.WriteServerError(w, r, "INTERNAL_ERROR", "failed to update assignment", err)
		return
	}
	writeJSON(w, http.StatusOK, a)
}

func (h *Handler) DeleteAssignment(w http.ResponseWriter, r *http.Request) {
	if _, err := h.pool.Exec(r.Context(), `DELETE FROM competition_judge_assignments WHERE id = $1`, strings.TrimSpace(r.PathValue("assignmentId"))); err != nil {
		handler.WriteServerError(w, r, "INTERNAL_ERROR", "failed to delete assignment", err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// ---------------------------------------------------------------------------
// Conflicts
// ---------------------------------------------------------------------------

type conflictRequest struct {
	JudgeID string `json:"judgeId"`
	EntryID string `json:"entryId"`
	Reason  string `json:"reason"`
}

func (h *Handler) ListConflicts(w http.ResponseWriter, r *http.Request) {
	editionID := strings.TrimSpace(r.PathValue("editionId"))
	rows, err := h.pool.Query(r.Context(), `
SELECT c.id::text, c.judge_id::text, c.entry_id::text, c.reason, c.declared_at
FROM competition_conflicts c
JOIN competition_judges j ON j.id = c.judge_id
WHERE j.edition_id = $1
ORDER BY c.declared_at DESC`, editionID)
	if err != nil {
		handler.WriteServerError(w, r, "INTERNAL_ERROR", "failed to list conflicts", err)
		return
	}
	defer rows.Close()
	out := []conflict{}
	for rows.Next() {
		c, err := scanConflict(rows)
		if err != nil {
			handler.WriteServerError(w, r, "INTERNAL_ERROR", "failed to read conflicts", err)
			return
		}
		out = append(out, c)
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": out})
}

func (h *Handler) CreateConflict(w http.ResponseWriter, r *http.Request) {
	editionID := strings.TrimSpace(r.PathValue("editionId"))
	var req conflictRequest
	if !decodeBody(w, r, 32<<10, &req) {
		return
	}
	var v validationCollector
	if !validUUID(req.JudgeID) || !validUUID(req.EntryID) {
		v.add("body", "judgeId and entryId are required")
	}
	if strings.TrimSpace(req.Reason) == "" {
		v.add("reason", "is required")
	}
	if v.write(w) {
		return
	}
	var judgeEdition, entryEdition string
	if err := h.pool.QueryRow(r.Context(), `SELECT edition_id::text FROM competition_judges WHERE id = $1`, req.JudgeID).Scan(&judgeEdition); err != nil {
		handler.WriteError(w, http.StatusBadRequest, "INVALID_JUDGE", "judge does not exist")
		return
	}
	var entryCategory string
	if err := h.pool.QueryRow(r.Context(), `SELECT edition_id::text, category_id::text FROM competition_entries WHERE id = $1`, req.EntryID).Scan(&entryEdition, &entryCategory); err != nil {
		handler.WriteError(w, http.StatusBadRequest, "INVALID_ENTRY", "entry does not exist")
		return
	}
	if judgeEdition != editionID || entryEdition != editionID {
		handler.WriteError(w, http.StatusBadRequest, "INVALID_SCOPE", "judge and entry must belong to the edition")
		return
	}
	c, err := scanConflict(h.pool.QueryRow(r.Context(), `
INSERT INTO competition_conflicts (judge_id, entry_id, reason)
VALUES ($1,$2,$3)
RETURNING id::text, judge_id::text, entry_id::text, reason, declared_at`,
		req.JudgeID, req.EntryID, strings.TrimSpace(req.Reason)))
	if err != nil {
		if isUniqueViolation(err) {
			handler.WriteError(w, http.StatusConflict, "CONFLICT_EXISTS", "this conflict is already declared")
			return
		}
		handler.WriteServerError(w, r, "INTERNAL_ERROR", "failed to declare conflict", err)
		return
	}
	// Recusal is immediate: the judge cannot score this category afterwards.
	_, _ = h.pool.Exec(r.Context(), `
UPDATE competition_judge_assignments SET status = 'recused'
WHERE judge_id = $1 AND category_id = $2 AND status <> 'recused'`, req.JudgeID, entryCategory)
	writeJSON(w, http.StatusCreated, c)
}

func (h *Handler) DeleteConflict(w http.ResponseWriter, r *http.Request) {
	if _, err := h.pool.Exec(r.Context(), `DELETE FROM competition_conflicts WHERE id = $1`, strings.TrimSpace(r.PathValue("conflictId"))); err != nil {
		handler.WriteServerError(w, r, "INTERNAL_ERROR", "failed to delete conflict", err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// ---------------------------------------------------------------------------
// Scores and locks
// ---------------------------------------------------------------------------

func (h *Handler) ListScores(w http.ResponseWriter, r *http.Request) {
	editionID := strings.TrimSpace(r.PathValue("editionId"))
	rows, err := h.pool.Query(r.Context(), scoreColumns+`
WHERE entry_id IN (SELECT id FROM competition_entries WHERE edition_id = $1)
ORDER BY entry_id, judge_id`, editionID)
	if err != nil {
		handler.WriteServerError(w, r, "INTERNAL_ERROR", "failed to list scores", err)
		return
	}
	defer rows.Close()
	out := []score{}
	for rows.Next() {
		s, err := scanScore(rows)
		if err != nil {
			handler.WriteServerError(w, r, "INTERNAL_ERROR", "failed to read scores", err)
			return
		}
		out = append(out, s)
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": out})
}

type adminScoreRequest struct {
	AssignmentID string  `json:"assignmentId"`
	EntryID      string  `json:"entryId"`
	Relevance    int     `json:"relevance"`
	Craft        int     `json:"craft"`
	Originality  int     `json:"originality"`
	Impact       int     `json:"impact"`
	Notes        *string `json:"notes"`
}

func (h *Handler) UpsertScoreAdmin(w http.ResponseWriter, r *http.Request) {
	var req adminScoreRequest
	if !decodeBody(w, r, 32<<10, &req) {
		return
	}
	criteria := Criteria{Relevance: req.Relevance, Craft: req.Craft, Originality: req.Originality, Impact: req.Impact}
	var v validationCollector
	if !validUUID(req.AssignmentID) || !validUUID(req.EntryID) {
		v.add("body", "assignmentId and entryId are required")
	}
	if !validCriteria(criteria) {
		v.add("criteria", "each rubric score must be between 1 and 10")
	}
	if v.write(w) {
		return
	}
	a, err := h.getAssignment(r.Context(), req.AssignmentID)
	if err != nil {
		handler.WriteError(w, http.StatusNotFound, "NOT_FOUND", "assignment not found")
		return
	}
	var entryEdition, entryCategory string
	if err := h.pool.QueryRow(r.Context(), `SELECT edition_id::text, category_id::text FROM competition_entries WHERE id = $1`, req.EntryID).Scan(&entryEdition, &entryCategory); err != nil {
		handler.WriteError(w, http.StatusNotFound, "NOT_FOUND", "entry not found")
		return
	}
	if entryEdition != a.EditionID || entryCategory != a.CategoryID {
		handler.WriteError(w, http.StatusForbidden, "FORBIDDEN", "entry is outside this assignment")
		return
	}
	var conflicted bool
	_ = h.pool.QueryRow(r.Context(), `SELECT EXISTS (SELECT 1 FROM competition_conflicts WHERE judge_id = $1 AND entry_id = $2)`, a.JudgeID, req.EntryID).Scan(&conflicted)
	if conflicted {
		handler.WriteError(w, http.StatusForbidden, "CONFLICT_DECLARED", "judge declared a conflict for this entry")
		return
	}
	var lockedAt *time.Time
	if err := h.pool.QueryRow(r.Context(), `SELECT locked_at FROM competition_scores WHERE assignment_id = $1 AND entry_id = $2`, a.ID, req.EntryID).Scan(&lockedAt); err != nil && !isNoRows(err) {
		handler.WriteServerError(w, r, "INTERNAL_ERROR", "failed to read score state", err)
		return
	}
	if lockedAt != nil {
		handler.WriteError(w, http.StatusConflict, "SCORE_LOCKED", "this score is locked")
		return
	}
	s, err := scanScore(h.pool.QueryRow(r.Context(), `
INSERT INTO competition_scores
(assignment_id, entry_id, judge_id, relevance, craft, originality, impact, total, notes)
VALUES ($1,$2,$3,$4,$5,$6,$7,$8,NULLIF($9,''))
ON CONFLICT (assignment_id, entry_id) DO UPDATE
SET relevance = EXCLUDED.relevance, craft = EXCLUDED.craft,
    originality = EXCLUDED.originality, impact = EXCLUDED.impact,
    total = EXCLUDED.total, notes = EXCLUDED.notes, updated_at = now()
RETURNING id::text, assignment_id::text, entry_id::text, judge_id::text,
          relevance, craft, originality, impact, total::float8, notes, locked_at,
          created_at, updated_at`,
		a.ID, req.EntryID, a.JudgeID, criteria.Relevance, criteria.Craft,
		criteria.Originality, criteria.Impact, WeightedTotal(criteria), req.Notes))
	if err != nil {
		handler.WriteServerError(w, r, "INTERNAL_ERROR", "failed to save score", err)
		return
	}
	writeJSON(w, http.StatusOK, s)
}

type lockScoresRequest struct {
	CategoryID *string `json:"categoryId"`
	EntryID    *string `json:"entryId"`
	Lock       *bool   `json:"lock"`
}

func (h *Handler) LockScores(w http.ResponseWriter, r *http.Request) {
	editionID := strings.TrimSpace(r.PathValue("editionId"))
	var req lockScoresRequest
	if !decodeBody(w, r, 16<<10, &req) {
		return
	}
	lock := true
	if req.Lock != nil {
		lock = *req.Lock
	}
	if !lock {
		var status string
		if err := h.pool.QueryRow(r.Context(), `SELECT status FROM competition_editions WHERE id = $1`, editionID).Scan(&status); err != nil {
			writeEditionError(w, r, err)
			return
		}
		if status == "closed" || status == "published" {
			handler.WriteError(w, http.StatusConflict, "SCORES_FROZEN", "scores cannot be unlocked after the board freezes")
			return
		}
	}
	query := `
UPDATE competition_scores SET locked_at = CASE WHEN $2 THEN now() ELSE NULL END,
       updated_at = now()
WHERE entry_id IN (
    SELECT id FROM competition_entries
    WHERE edition_id = $1
      AND ($3::uuid IS NULL OR category_id = $3::uuid)
      AND ($4::uuid IS NULL OR id = $4::uuid)
)`
	var categoryID, entryID *string
	if req.CategoryID != nil && validUUID(*req.CategoryID) {
		categoryID = req.CategoryID
	}
	if req.EntryID != nil && validUUID(*req.EntryID) {
		entryID = req.EntryID
	}
	tag, err := h.pool.Exec(r.Context(), query, editionID, lock, categoryID, entryID)
	if err != nil {
		handler.WriteServerError(w, r, "INTERNAL_ERROR", "failed to lock scores", err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"updated": tag.RowsAffected(), "locked": lock})
}
