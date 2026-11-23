package competition

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"time"

	"github.com/bradleygichuru/ytci-go/internal/handler"
)

// ---------------------------------------------------------------------------
// Vote monitoring
// ---------------------------------------------------------------------------

type voteSummaryRow struct {
	EntryID      string `json:"entryId"`
	Title        string `json:"title"`
	CategoryKey  string `json:"categoryKey"`
	LiveVotes    int    `json:"liveVotes"`
	InvalidVotes int    `json:"invalidVotes"`
}

func (h *Handler) VoteSummary(w http.ResponseWriter, r *http.Request) {
	editionID := strings.TrimSpace(r.PathValue("editionId"))
	rows, err := h.pool.Query(r.Context(), `
SELECT ce.id::text, re.title, cc.key,
       count(v.id) FILTER (WHERE v.invalidated_at IS NULL)::int,
       count(v.id) FILTER (WHERE v.invalidated_at IS NOT NULL)::int
FROM competition_entries ce
JOIN repository_entries re ON re.id = ce.repository_entry_id
JOIN competition_categories cc ON cc.id = ce.category_id
LEFT JOIN competition_votes v ON v.entry_id = ce.id
WHERE ce.edition_id = $1
GROUP BY ce.id, re.title, cc.key, cc.sort_order
ORDER BY cc.sort_order, re.title`, editionID)
	if err != nil {
		handler.WriteServerError(w, r, "INTERNAL_ERROR", "failed to read vote summary", err)
		return
	}
	defer rows.Close()
	entries := []voteSummaryRow{}
	for rows.Next() {
		var row voteSummaryRow
		if err := rows.Scan(&row.EntryID, &row.Title, &row.CategoryKey, &row.LiveVotes, &row.InvalidVotes); err != nil {
			handler.WriteServerError(w, r, "INTERNAL_ERROR", "failed to read vote summary", err)
			return
		}
		entries = append(entries, row)
	}
	var live, invalid, voters int
	if err := h.pool.QueryRow(r.Context(), `
SELECT count(*) FILTER (WHERE invalidated_at IS NULL)::int,
       count(*) FILTER (WHERE invalidated_at IS NOT NULL)::int,
       count(DISTINCT voter_user_id)::int
FROM competition_votes WHERE edition_id = $1`, editionID).Scan(&live, &invalid, &voters); err != nil {
		handler.WriteServerError(w, r, "INTERNAL_ERROR", "failed to count votes", err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"entries": entries, "liveVotes": live, "invalidVotes": invalid, "distinctVoters": voters,
	})
}

type reviewCaseRequest struct {
	EntryID *string `json:"entryId"`
	Signal  string  `json:"signal"`
}

func (h *Handler) ListReviewCases(w http.ResponseWriter, r *http.Request) {
	editionID := strings.TrimSpace(r.PathValue("editionId"))
	status := strings.TrimSpace(r.URL.Query().Get("status"))
	rows, err := h.pool.Query(r.Context(), reviewCaseColumns+`
WHERE edition_id = $1 AND ($2::text = '' OR status = $2)
ORDER BY opened_at DESC`, editionID, status)
	if err != nil {
		handler.WriteServerError(w, r, "INTERNAL_ERROR", "failed to list review cases", err)
		return
	}
	defer rows.Close()
	out := []reviewCase{}
	for rows.Next() {
		c, err := scanReviewCase(rows)
		if err != nil {
			handler.WriteServerError(w, r, "INTERNAL_ERROR", "failed to read review cases", err)
			return
		}
		out = append(out, c)
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": out})
}

func (h *Handler) CreateReviewCase(w http.ResponseWriter, r *http.Request) {
	editionID := strings.TrimSpace(r.PathValue("editionId"))
	var req reviewCaseRequest
	if !decodeBody(w, r, 16<<10, &req) {
		return
	}
	signal := strings.TrimSpace(req.Signal)
	if signal == "" {
		handler.WriteError(w, http.StatusBadRequest, "INVALID_REQUEST", "signal is required")
		return
	}
	var entryID *string
	if req.EntryID != nil && validUUID(*req.EntryID) {
		entryID = req.EntryID
	}
	c, err := scanReviewCase(h.pool.QueryRow(r.Context(), `
INSERT INTO competition_vote_review_cases (edition_id, entry_id, signal)
VALUES ($1,$2,$3)
RETURNING id::text, edition_id::text, entry_id::text, signal, status, opened_at,
          resolved_at, resolution, reviewer_id`, editionID, entryID, signal))
	if err != nil {
		handler.WriteServerError(w, r, "INTERNAL_ERROR", "failed to open review case", err)
		return
	}
	writeJSON(w, http.StatusCreated, c)
}

type resolveCaseRequest struct {
	Status     string  `json:"status"`
	Resolution *string `json:"resolution"`
}

func (h *Handler) ResolveReviewCase(w http.ResponseWriter, r *http.Request) {
	var req resolveCaseRequest
	if !decodeBody(w, r, 16<<10, &req) {
		return
	}
	status := normalizeKey(req.Status)
	if status != "resolved" && status != "dismissed" {
		handler.WriteError(w, http.StatusBadRequest, "INVALID_REQUEST", "status must be resolved or dismissed")
		return
	}
	c, err := scanReviewCase(h.pool.QueryRow(r.Context(), `
UPDATE competition_vote_review_cases
SET status = $2, resolution = $3, reviewer_id = $4, resolved_at = now()
WHERE id = $1
RETURNING id::text, edition_id::text, entry_id::text, signal, status, opened_at,
          resolved_at, resolution, reviewer_id`, strings.TrimSpace(r.PathValue("caseId")), status, textPtr(req.Resolution), actor(r.Context())))
	if err != nil {
		if isNoRows(err) {
			handler.WriteError(w, http.StatusNotFound, "NOT_FOUND", "review case not found")
			return
		}
		handler.WriteServerError(w, r, "INTERNAL_ERROR", "failed to resolve review case", err)
		return
	}
	writeJSON(w, http.StatusOK, c)
}

type invalidateVoteRequest struct {
	ReasonCode string `json:"reasonCode"`
}

// InvalidateVote stamps a ledger row. The row is never deleted; the recount
// recomputes totals from the remaining live rows.
func (h *Handler) InvalidateVote(w http.ResponseWriter, r *http.Request) {
	var req invalidateVoteRequest
	if !decodeBody(w, r, 16<<10, &req) {
		return
	}
	reason := normalizeKey(req.ReasonCode)
	if reason == "" || len(reason) > 64 {
		handler.WriteError(w, http.StatusBadRequest, "INVALID_REQUEST", "reasonCode is required and must be at most 64 characters")
		return
	}
	v, err := scanVote(h.pool.QueryRow(r.Context(), `
UPDATE competition_votes
SET invalidated_at = now(), invalidated_by = $2, reason_code = $3
WHERE id = $1 AND invalidated_at IS NULL
RETURNING id::text, edition_id::text, category_id::text, entry_id::text,
          created_at, invalidated_at, reason_code`,
		strings.TrimSpace(r.PathValue("voteId")), actor(r.Context()), reason))
	if err != nil {
		if isNoRows(err) {
			handler.WriteError(w, http.StatusNotFound, "NOT_FOUND", "vote not found or already invalidated")
			return
		}
		handler.WriteServerError(w, r, "INTERNAL_ERROR", "failed to invalidate vote", err)
		return
	}
	writeJSON(w, http.StatusOK, v)
}

// ---------------------------------------------------------------------------
// Freeze and recount
// ---------------------------------------------------------------------------

type snapshotPayload struct {
	CategoryID *string            `json:"categoryId"`
	Entries    []leaderboardEntry `json:"entries"`
	TotalVotes int                `json:"totalVotes"`
}

// Freeze writes the final snapshot and closes the edition. Subsequent counts
// use the ledger, not a cached number.
func (h *Handler) Freeze(w http.ResponseWriter, r *http.Request) {
	h.writeSnapshots(w, r, true)
}

// Recount rewrites the final snapshot from the current ledger without changing
// the edition status.
func (h *Handler) Recount(w http.ResponseWriter, r *http.Request) {
	h.writeSnapshots(w, r, false)
}

func (h *Handler) writeSnapshots(w http.ResponseWriter, r *http.Request, closeEdition bool) {
	editionID := strings.TrimSpace(r.PathValue("editionId"))
	ed, err := h.getEdition(r.Context(), "id = $1", editionID)
	if err != nil {
		writeEditionError(w, r, err)
		return
	}
	snapshots, err := h.computeSnapshots(r.Context(), ed)
	if err != nil {
		handler.WriteServerError(w, r, "INTERNAL_ERROR", "failed to compute snapshots", err)
		return
	}
	tx, err := h.pool.Begin(r.Context())
	if err != nil {
		handler.WriteServerError(w, r, "INTERNAL_ERROR", "failed to start snapshot transaction", err)
		return
	}
	defer tx.Rollback(r.Context())
	if closeEdition {
		if _, err := tx.Exec(r.Context(), `
UPDATE competition_editions SET status = 'closed', updated_at = now()
WHERE id = $1 AND status <> 'published'`, ed.ID); err != nil {
			handler.WriteServerError(w, r, "INTERNAL_ERROR", "failed to close edition", err)
			return
		}
		// A published result must not move because a judge edited after freeze.
		if _, err := tx.Exec(r.Context(), `
UPDATE competition_scores SET locked_at = coalesce(locked_at, now()), updated_at = now()
WHERE entry_id IN (SELECT id FROM competition_entries WHERE edition_id = $1)`, ed.ID); err != nil {
			handler.WriteServerError(w, r, "INTERNAL_ERROR", "failed to lock scores at freeze", err)
			return
		}
	}
	if _, err := tx.Exec(r.Context(), `
UPDATE competition_leaderboard_snapshots SET is_final = false
WHERE edition_id = $1 AND is_final = true`, ed.ID); err != nil {
		handler.WriteServerError(w, r, "INTERNAL_ERROR", "failed to retire previous snapshot", err)
		return
	}
	out := []snapshotSummary{}
	for _, s := range snapshots {
		payload, _ := json.Marshal(s.payload)
		var id string
		var createdAt time.Time
		if err := tx.QueryRow(r.Context(), `
INSERT INTO competition_leaderboard_snapshots
(edition_id, category_id, payload, ledger_hash, is_final)
VALUES ($1,$2,$3,$4,true)
RETURNING id::text, created_at`, ed.ID, s.categoryID, payload, s.hash).Scan(&id, &createdAt); err != nil {
			handler.WriteServerError(w, r, "INTERNAL_ERROR", "failed to write snapshot", err)
			return
		}
		out = append(out, snapshotSummary{
			ID: id, EditionID: ed.ID, CategoryID: s.categoryID,
			LedgerHash: s.hash, CreatedAt: createdAt, IsFinal: true, Payload: s.payload,
		})
	}
	if err := tx.Commit(r.Context()); err != nil {
		handler.WriteServerError(w, r, "INTERNAL_ERROR", "failed to commit snapshot", err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": out, "frozen": true})
}

type computedSnapshot struct {
	categoryID *string
	hash       string
	payload    snapshotPayload
}

func (h *Handler) computeSnapshots(ctx context.Context, ed edition) ([]computedSnapshot, error) {
	entries, err := h.loadScoredEntries(ctx, ed.ID)
	if err != nil {
		return nil, err
	}
	voteRows, err := h.pool.Query(ctx, `
SELECT id::text FROM competition_votes WHERE edition_id = $1 AND invalidated_at IS NULL`, ed.ID)
	if err != nil {
		return nil, err
	}
	defer voteRows.Close()
	ids := []string{}
	for voteRows.Next() {
		var id string
		if err := voteRows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	hash := LedgerHash(ids)
	categories, err := h.listCategories(ctx, ed.ID)
	if err != nil {
		return nil, err
	}
	grouped := groupByCategory(entries)
	out := []computedSnapshot{}
	total := 0
	for _, c := range categories {
		rows := LeaderboardRows(grouped[c.ID])
		categoryID := c.ID
		count := 0
		for _, row := range rows {
			count += row.Votes
		}
		out = append(out, computedSnapshot{
			categoryID: &categoryID, hash: hash,
			payload: snapshotPayload{CategoryID: &categoryID, Entries: rows, TotalVotes: count},
		})
		total += count
	}
	overall := LeaderboardRows(entries)
	out = append(out, computedSnapshot{
		hash:    hash,
		payload: snapshotPayload{Entries: overall, TotalVotes: total},
	})
	return out, nil
}

// AdminResults is the preview; public results require a frozen snapshot.
func (h *Handler) AdminResults(w http.ResponseWriter, r *http.Request) {
	ed, err := h.getEdition(r.Context(), "id = $1", strings.TrimSpace(r.PathValue("editionId")))
	if err != nil {
		writeEditionError(w, r, err)
		return
	}
	results, err := h.buildResults(r.Context(), ed)
	if err != nil {
		handler.WriteServerError(w, r, "INTERNAL_ERROR", "failed to build results", err)
		return
	}
	writeJSON(w, http.StatusOK, results)
}

// ---------------------------------------------------------------------------
// Awards
// ---------------------------------------------------------------------------

type awardRequest struct {
	CategoryID *string `json:"categoryId"`
	EntryID    string  `json:"entryId"`
	AwardType  string  `json:"awardType"`
	AwardForm  *string `json:"awardForm"`
	Citation   *string `json:"citation"`
}

func (h *Handler) ListAwards(w http.ResponseWriter, r *http.Request) {
	editionID := strings.TrimSpace(r.PathValue("editionId"))
	awards, err := h.listAwards(r.Context(), editionID)
	if err != nil {
		handler.WriteServerError(w, r, "INTERNAL_ERROR", "failed to list awards", err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": awards})
}

func (h *Handler) CreateAward(w http.ResponseWriter, r *http.Request) {
	editionID := strings.TrimSpace(r.PathValue("editionId"))
	var req awardRequest
	if !decodeBody(w, r, 32<<10, &req) {
		return
	}
	var v validationCollector
	if !validUUID(req.EntryID) {
		v.add("entryId", "must be a UUID")
	}
	awardType := normalizeKey(req.AwardType)
	if !awardTypes[awardType] {
		v.add("awardType", "must be category_winner, overall or people_choice")
	}
	form := (*string)(nil)
	if req.AwardForm != nil && strings.TrimSpace(*req.AwardForm) != "" {
		f := normalizeKey(*req.AwardForm)
		if !awardForms[f] {
			v.add("awardForm", "must be grant, commission, certificate, feature or recognition")
		} else {
			form = &f
		}
	}
	var categoryID *string
	if awardType == "category_winner" {
		if req.CategoryID == nil || !validUUID(*req.CategoryID) {
			v.add("categoryId", "is required for a category winner")
		} else {
			categoryID = req.CategoryID
		}
	}
	if v.write(w) {
		return
	}
	var entryEdition string
	if err := h.pool.QueryRow(r.Context(), `SELECT edition_id::text FROM competition_entries WHERE id = $1`, req.EntryID).Scan(&entryEdition); err != nil || entryEdition != editionID {
		handler.WriteError(w, http.StatusBadRequest, "INVALID_ENTRY", "entry does not belong to the edition")
		return
	}
	var existing bool
	if err := h.pool.QueryRow(r.Context(), `
SELECT EXISTS (
    SELECT 1 FROM competition_awards
    WHERE edition_id = $1 AND award_type = $2
      AND (($3::uuid IS NULL AND category_id IS NULL) OR category_id = $3::uuid)
)`, editionID, awardType, categoryID).Scan(&existing); err != nil {
		handler.WriteServerError(w, r, "INTERNAL_ERROR", "failed to check award slot", err)
		return
	}
	if existing {
		handler.WriteError(w, http.StatusConflict, "AWARD_EXISTS", "that award slot is already filled")
		return
	}
	a, err := scanAward(h.pool.QueryRow(r.Context(), `
INSERT INTO competition_awards (edition_id, category_id, entry_id, award_type, award_form, citation)
VALUES ($1,$2,$3,$4,$5,$6)
RETURNING id::text, edition_id::text, category_id::text, entry_id::text,
          award_type, award_form, citation, published_at, created_at,
          (SELECT re.title FROM competition_entries ce JOIN repository_entries re ON re.id = ce.repository_entry_id WHERE ce.id = $3),
          (SELECT re.author_name FROM competition_entries ce JOIN repository_entries re ON re.id = ce.repository_entry_id WHERE ce.id = $3),
          (SELECT cc.key FROM competition_categories cc WHERE cc.id = $2)`,
		editionID, categoryID, req.EntryID, awardType, form, textPtr(req.Citation)))
	if err != nil {
		handler.WriteServerError(w, r, "INTERNAL_ERROR", "failed to create award", err)
		return
	}
	writeJSON(w, http.StatusCreated, a)
}

type awardUpdateRequest struct {
	EntryID   *string `json:"entryId"`
	AwardForm *string `json:"awardForm"`
	Citation  *string `json:"citation"`
}

func (h *Handler) UpdateAward(w http.ResponseWriter, r *http.Request) {
	id := strings.TrimSpace(r.PathValue("awardId"))
	var req awardUpdateRequest
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
	if req.EntryID != nil {
		if !validUUID(*req.EntryID) {
			v.add("entryId", "must be a UUID")
		} else {
			add("entry_id", *req.EntryID)
		}
	}
	if req.AwardForm != nil {
		form := normalizeKey(*req.AwardForm)
		if form != "" && !awardForms[form] {
			v.add("awardForm", "unknown award form")
		} else if form == "" {
			add("award_form", nil)
		} else {
			add("award_form", form)
		}
	}
	if req.Citation != nil {
		add("citation", textPtr(req.Citation))
	}
	if len(sets) == 0 {
		v.add("body", "no fields to update")
	}
	if v.write(w) {
		return
	}
	a, err := scanAward(h.pool.QueryRow(r.Context(),
		"UPDATE competition_awards a SET "+strings.Join(sets, ", ")+` WHERE a.id = $1
RETURNING a.id::text, a.edition_id::text, a.category_id::text, a.entry_id::text,
          a.award_type, a.award_form, a.citation, a.published_at, a.created_at,
          (SELECT re.title FROM competition_entries ce JOIN repository_entries re ON re.id = ce.repository_entry_id WHERE ce.id = a.entry_id),
          (SELECT re.author_name FROM competition_entries ce JOIN repository_entries re ON re.id = ce.repository_entry_id WHERE ce.id = a.entry_id),
          (SELECT cc.key FROM competition_categories cc WHERE cc.id = a.category_id)`, args...))
	if err != nil {
		if isNoRows(err) {
			handler.WriteError(w, http.StatusNotFound, "NOT_FOUND", "award not found")
			return
		}
		handler.WriteServerError(w, r, "INTERNAL_ERROR", "failed to update award", err)
		return
	}
	writeJSON(w, http.StatusOK, a)
}

func (h *Handler) PublishAward(w http.ResponseWriter, r *http.Request) {
	a, err := scanAward(h.pool.QueryRow(r.Context(), `
UPDATE competition_awards SET published_at = coalesce(published_at, now())
WHERE id = $1
RETURNING id::text, edition_id::text, category_id::text, entry_id::text,
          award_type, award_form, citation, published_at, created_at,
          (SELECT re.title FROM competition_entries ce JOIN repository_entries re ON re.id = ce.repository_entry_id WHERE ce.id = competition_awards.entry_id),
          (SELECT re.author_name FROM competition_entries ce JOIN repository_entries re ON re.id = ce.repository_entry_id WHERE ce.id = competition_awards.entry_id),
          (SELECT cc.key FROM competition_categories cc WHERE cc.id = competition_awards.category_id)`,
		strings.TrimSpace(r.PathValue("awardId"))))
	if err != nil {
		if isNoRows(err) {
			handler.WriteError(w, http.StatusNotFound, "NOT_FOUND", "award not found")
			return
		}
		handler.WriteServerError(w, r, "INTERNAL_ERROR", "failed to publish award", err)
		return
	}
	writeJSON(w, http.StatusOK, a)
}

func (h *Handler) DeleteAward(w http.ResponseWriter, r *http.Request) {
	if _, err := h.pool.Exec(r.Context(), `DELETE FROM competition_awards WHERE id = $1`, strings.TrimSpace(r.PathValue("awardId"))); err != nil {
		handler.WriteServerError(w, r, "INTERNAL_ERROR", "failed to delete award", err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
