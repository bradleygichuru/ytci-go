package competition

import (
	"context"
	"net/http"
	"strings"
	"time"

	"github.com/bradleygichuru/ytci-go/internal/handler"
)

var judgingStatuses = map[string]bool{
	"open": true, "judging": true, "voting": true, "closed": true,
}

type judgeEntryView struct {
	EntryID  string `json:"entryId"`
	Title    string `json:"title"`
	Summary  string `json:"summary"`
	Conflict bool   `json:"conflict"`
	Score    *score `json:"score"`
}

// judgeAssignmentView is intentionally blind: no credit name, county, team or
// contact detail is selected.
type judgeAssignmentView struct {
	Assignment  assignment       `json:"assignment"`
	EditionSlug string           `json:"editionSlug"`
	EditionName string           `json:"editionName"`
	Category    category         `json:"category"`
	Entries     []judgeEntryView `json:"entries"`
}

// MyJudgingAssignments lists active assignments for the signed-in judge.
func (h *Handler) MyJudgingAssignments(w http.ResponseWriter, r *http.Request) {
	userID := actor(r.Context())
	slug := strings.TrimSpace(r.URL.Query().Get("edition"))
	rows, err := h.pool.Query(r.Context(), `
SELECT a.id::text, a.edition_id::text, a.category_id::text, a.judge_id::text,
       a.status, a.assigned_at, e.slug, e.name,
       cc.key, cc.label, cc.description, cc.sort_order
FROM competition_judge_assignments a
JOIN competition_judges j ON j.id = a.judge_id
JOIN competition_editions e ON e.id = a.edition_id
JOIN competition_categories cc ON cc.id = a.category_id
WHERE j.user_id = $1 AND a.status <> 'recused'
  AND ($2::text = '' OR e.slug = $2)
ORDER BY coalesce(e.opens_at, e.created_at) DESC, cc.sort_order`, userID, slug)
	if err != nil {
		handler.WriteServerError(w, r, "INTERNAL_ERROR", "failed to list judging assignments", err)
		return
	}
	defer rows.Close()

	out := []judgeAssignmentView{}
	for rows.Next() {
		var view judgeAssignmentView
		if err := rows.Scan(&view.Assignment.ID, &view.Assignment.EditionID,
			&view.Assignment.CategoryID, &view.Assignment.JudgeID,
			&view.Assignment.Status, &view.Assignment.AssignedAt,
			&view.EditionSlug, &view.EditionName,
			&view.Category.Key, &view.Category.Label, &view.Category.Description,
			&view.Category.SortOrder); err != nil {
			handler.WriteServerError(w, r, "INTERNAL_ERROR", "failed to read judging assignments", err)
			return
		}
		view.Category.ID = view.Assignment.CategoryID
		view.Category.EditionID = view.Assignment.EditionID
		entries, err := h.assignmentEntries(r.Context(), view.Assignment)
		if err != nil {
			handler.WriteServerError(w, r, "INTERNAL_ERROR", "failed to read assigned entries", err)
			return
		}
		view.Entries = entries
		out = append(out, view)
	}
	if err := rows.Err(); err != nil {
		handler.WriteServerError(w, r, "INTERNAL_ERROR", "failed to read judging assignments", err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": out})
}

func (h *Handler) assignmentEntries(ctx context.Context, a assignment) ([]judgeEntryView, error) {
	rows, err := h.pool.Query(ctx, `
SELECT ce.id::text, re.title, re.summary,
       (c.id IS NOT NULL) AS conflicted,
       cs.id::text, cs.assignment_id::text, cs.entry_id::text, cs.judge_id::text,
       coalesce(cs.relevance, 0), coalesce(cs.craft, 0),
       coalesce(cs.originality, 0), coalesce(cs.impact, 0),
       coalesce(cs.total, 0)::float8, cs.notes, cs.locked_at, cs.created_at, cs.updated_at
FROM competition_entries ce
JOIN repository_entries re ON re.id = ce.repository_entry_id
LEFT JOIN competition_conflicts c ON c.entry_id = ce.id AND c.judge_id = $3
LEFT JOIN competition_scores cs ON cs.entry_id = ce.id AND cs.assignment_id = $2
WHERE ce.edition_id = $1 AND ce.category_id = $4 AND ce.eligibility_status = 'eligible'
ORDER BY re.title`, a.EditionID, a.ID, a.JudgeID, a.CategoryID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []judgeEntryView{}
	for rows.Next() {
		var e judgeEntryView
		var scoreID, assignmentID, entryID, judgeID *string
		var relevance, craft, originality, impact int
		var total float64
		var notes *string
		var lockedAt *time.Time
		var createdAt, updatedAt *time.Time
		if err := rows.Scan(&e.EntryID, &e.Title, &e.Summary, &e.Conflict,
			&scoreID, &assignmentID, &entryID, &judgeID,
			&relevance, &craft, &originality, &impact, &total, &notes,
			&lockedAt, &createdAt, &updatedAt); err != nil {
			return nil, err
		}
		if scoreID != nil {
			s := score{
				ID: *scoreID, EntryID: e.EntryID, JudgeID: a.JudgeID,
				AssignmentID: a.ID, Relevance: relevance, Craft: craft,
				Originality: originality, Impact: impact, Total: total,
				Notes: notes, LockedAt: lockedAt,
			}
			if assignmentID != nil {
				s.AssignmentID = *assignmentID
			}
			if entryID != nil {
				s.EntryID = *entryID
			}
			if judgeID != nil {
				s.JudgeID = *judgeID
			}
			if createdAt != nil {
				s.CreatedAt = *createdAt
			}
			if updatedAt != nil {
				s.UpdatedAt = *updatedAt
			}
			e.Score = &s
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

type submitScoreRequest struct {
	Relevance   int     `json:"relevance"`
	Craft       int     `json:"craft"`
	Originality int     `json:"originality"`
	Impact      int     `json:"impact"`
	Notes       *string `json:"notes"`
}

// SubmitScore upserts one judge score. Assignment ownership, conflict and lock
// state are checked server side; the judge is never trusted to be scoped.
func (h *Handler) SubmitScore(w http.ResponseWriter, r *http.Request) {
	userID := actor(r.Context())
	assignmentID := strings.TrimSpace(r.PathValue("assignmentId"))
	entryID := strings.TrimSpace(r.PathValue("entryId"))
	if !validUUID(assignmentID) || !validUUID(entryID) {
		handler.WriteError(w, http.StatusNotFound, "NOT_FOUND", "assignment or entry not found")
		return
	}
	var req submitScoreRequest
	if !decodeBody(w, r, 16<<10, &req) {
		return
	}
	criteria := Criteria{Relevance: req.Relevance, Craft: req.Craft, Originality: req.Originality, Impact: req.Impact}
	var v validationCollector
	if !validCriteria(criteria) {
		v.add("criteria", "relevance, craft, originality and impact must each be between 1 and 10")
	}
	if req.Notes != nil && len(*req.Notes) > 4000 {
		v.add("notes", "must be at most 4000 characters")
	}
	if v.write(w) {
		return
	}

	ctx := r.Context()
	var a assignment
	var judgeUser *string
	var editionStatus string
	if err := h.pool.QueryRow(ctx, `
SELECT a.id::text, a.edition_id::text, a.category_id::text, a.judge_id::text,
       a.status, a.assigned_at, j.user_id, e.status
FROM competition_judge_assignments a
JOIN competition_judges j ON j.id = a.judge_id
JOIN competition_editions e ON e.id = a.edition_id
WHERE a.id = $1`, assignmentID).Scan(
		&a.ID, &a.EditionID, &a.CategoryID, &a.JudgeID, &a.Status, &a.AssignedAt,
		&judgeUser, &editionStatus); err != nil {
		if isNoRows(err) {
			handler.WriteError(w, http.StatusNotFound, "NOT_FOUND", "assignment not found")
			return
		}
		handler.WriteServerError(w, r, "INTERNAL_ERROR", "failed to read assignment", err)
		return
	}
	if judgeUser == nil || *judgeUser != userID {
		handler.WriteError(w, http.StatusForbidden, "FORBIDDEN", "not your judging assignment")
		return
	}
	if a.Status == "recused" {
		handler.WriteError(w, http.StatusForbidden, "JUDGE_RECUSED", "this assignment is recused")
		return
	}
	if !judgingStatuses[editionStatus] {
		handler.WriteError(w, http.StatusConflict, "JUDGING_CLOSED", "judging is not open")
		return
	}

	var entryEdition, entryCategory string
	if err := h.pool.QueryRow(ctx, `
SELECT edition_id::text, category_id::text FROM competition_entries WHERE id = $1`, entryID).
		Scan(&entryEdition, &entryCategory); err != nil {
		if isNoRows(err) {
			handler.WriteError(w, http.StatusNotFound, "NOT_FOUND", "entry not found")
			return
		}
		handler.WriteServerError(w, r, "INTERNAL_ERROR", "failed to read entry", err)
		return
	}
	if entryEdition != a.EditionID || entryCategory != a.CategoryID {
		handler.WriteError(w, http.StatusForbidden, "FORBIDDEN", "entry is outside this assignment")
		return
	}
	var conflicted bool
	if err := h.pool.QueryRow(ctx, `
SELECT EXISTS (SELECT 1 FROM competition_conflicts WHERE judge_id = $1 AND entry_id = $2)`,
		a.JudgeID, entryID).Scan(&conflicted); err != nil {
		handler.WriteServerError(w, r, "INTERNAL_ERROR", "failed to check conflicts", err)
		return
	}
	if conflicted {
		handler.WriteError(w, http.StatusForbidden, "CONFLICT_DECLARED", "you declared a conflict for this entry")
		return
	}
	var lockedAt *time.Time
	if err := h.pool.QueryRow(ctx, `
SELECT locked_at FROM competition_scores WHERE assignment_id = $1 AND entry_id = $2`,
		assignmentID, entryID).Scan(&lockedAt); err != nil && !isNoRows(err) {
		handler.WriteServerError(w, r, "INTERNAL_ERROR", "failed to read score state", err)
		return
	}
	if lockedAt != nil {
		handler.WriteError(w, http.StatusConflict, "SCORE_LOCKED", "this score is locked")
		return
	}

	s, err := scanScore(h.pool.QueryRow(ctx, `
INSERT INTO competition_scores
(assignment_id, entry_id, judge_id, relevance, craft, originality, impact, total, notes)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, NULLIF($9, ''))
ON CONFLICT (assignment_id, entry_id) DO UPDATE
SET relevance = EXCLUDED.relevance,
    craft = EXCLUDED.craft,
    originality = EXCLUDED.originality,
    impact = EXCLUDED.impact,
    total = EXCLUDED.total,
    notes = EXCLUDED.notes,
    updated_at = now()
RETURNING id::text, assignment_id::text, entry_id::text, judge_id::text,
          relevance, craft, originality, impact, total::float8, notes, locked_at,
          created_at, updated_at`,
		assignmentID, entryID, a.JudgeID, criteria.Relevance, criteria.Craft,
		criteria.Originality, criteria.Impact, WeightedTotal(criteria), req.Notes))
	if err != nil {
		handler.WriteServerError(w, r, "INTERNAL_ERROR", "failed to save score", err)
		return
	}
	writeJSON(w, http.StatusOK, s)
}
