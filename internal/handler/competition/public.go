package competition

import (
	"context"
	"net/http"
	"strings"
	"time"

	"github.com/bradleygichuru/ytci-go/internal/handler"
)

// ---------------------------------------------------------------------------
// Shared read model
// ---------------------------------------------------------------------------

// loadScoredEntries joins eligible entries, averaged expert scores and live
// vote counts. Individual judge rows never leave this query.
func (h *Handler) loadScoredEntries(ctx context.Context, editionID string) ([]ScoredEntry, error) {
	rows, err := h.pool.Query(ctx, `
SELECT ce.id::text, re.title, cc.id::text, cc.key, cc.label, cc.sort_order,
       coalesce(avg(cs.total), 0)::float8,
       coalesce(avg(cs.craft), 0)::float8,
       coalesce(avg(cs.originality), 0)::float8,
       count(cs.id)::int,
       coalesce(v.votes, 0)::int
FROM competition_entries ce
JOIN repository_entries re ON re.id = ce.repository_entry_id
JOIN competition_categories cc ON cc.id = ce.category_id
LEFT JOIN competition_scores cs ON cs.entry_id = ce.id
LEFT JOIN (
    SELECT entry_id, count(*) AS votes
    FROM competition_votes
    WHERE invalidated_at IS NULL
    GROUP BY entry_id
) v ON v.entry_id = ce.id
WHERE ce.edition_id = $1 AND ce.eligibility_status = 'eligible'
GROUP BY ce.id, re.title, cc.id, cc.key, cc.label, cc.sort_order, v.votes
ORDER BY cc.sort_order, ce.id`, editionID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []ScoredEntry{}
	for rows.Next() {
		var e ScoredEntry
		if err := rows.Scan(&e.EntryID, &e.Title, &e.CategoryID, &e.CategoryKey,
			&e.CategoryLabel, &e.CategorySort, &e.Expert, &e.Craft, &e.Originality,
			&e.ScoreCount, &e.Votes); err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

func groupByCategory(entries []ScoredEntry) map[string][]ScoredEntry {
	out := map[string][]ScoredEntry{}
	for _, e := range entries {
		out[e.CategoryID] = append(out[e.CategoryID], e)
	}
	return out
}

func (h *Handler) categoriesForEntries(ctx context.Context, editionID string) ([]category, error) {
	return h.listCategories(ctx, editionID)
}

// buildLeaderboard returns the top ten per category by raw vote totals.
func (h *Handler) buildLeaderboard(ctx context.Context, ed edition) (leaderboardResponse, error) {
	entries, err := h.loadScoredEntries(ctx, ed.ID)
	if err != nil {
		return leaderboardResponse{}, err
	}
	categories, err := h.categoriesForEntries(ctx, ed.ID)
	if err != nil {
		return leaderboardResponse{}, err
	}
	grouped := groupByCategory(entries)
	out := leaderboardResponse{
		EditionID: ed.ID, Mode: ed.PublicVoteMode, GeneratedAt: time.Now().UTC(),
		Categories: []leaderboardCategory{},
	}
	for _, c := range categories {
		rows := LeaderboardRows(grouped[c.ID])
		for i := range rows {
			if ed.PublicVoteMode == VoteModeWeighted {
				max := maxVotes(grouped[c.ID])
				rows[i].PublicScore = NormalizePublicScore(rows[i].Votes, max)
			}
		}
		if len(rows) > 10 {
			rows = rows[:10]
		}
		if rows == nil {
			rows = []leaderboardEntry{}
		}
		out.Categories = append(out.Categories, leaderboardCategory{Category: c, Entries: rows})
	}
	return out, nil
}

// buildResults ranks categories using the edition's vote mode. In
// people_choice_only the panel decides the awards and the public vote is a
// separate recognition, so final score is the expert score.
func (h *Handler) buildResults(ctx context.Context, ed edition) (resultsResponse, error) {
	entries, err := h.loadScoredEntries(ctx, ed.ID)
	if err != nil {
		return resultsResponse{}, err
	}
	categories, err := h.categoriesForEntries(ctx, ed.ID)
	if err != nil {
		return resultsResponse{}, err
	}
	grouped := groupByCategory(entries)
	out := resultsResponse{
		EditionID: ed.ID, Status: ed.Status, PublicVoteMode: ed.PublicVoteMode,
		Weights: WeightsFor(ed.PublicVoteMode), Categories: []resultCategory{}, Awards: []award{},
	}
	for _, c := range categories {
		rows := RankCategory(grouped[c.ID], ed.PublicVoteMode)
		if rows == nil {
			rows = []resultEntry{}
		}
		out.Categories = append(out.Categories, resultCategory{Category: c, Entries: rows})
	}
	if ed.PublicVoteMode != VoteModeNone {
		out.PeopleChoice = PeopleChoice(entries)
	}
	var hash *string
	if err := h.pool.QueryRow(ctx, `
SELECT ledger_hash FROM competition_leaderboard_snapshots
WHERE edition_id = $1 AND is_final = true
ORDER BY created_at DESC LIMIT 1`, ed.ID).Scan(&hash); err == nil {
		out.Frozen = true
		out.LedgerHash = hash
	}
	awards, err := h.listAwards(ctx, ed.ID)
	if err != nil {
		return resultsResponse{}, err
	}
	out.Awards = awards
	return out, nil
}

func (h *Handler) listAwards(ctx context.Context, editionID string) ([]award, error) {
	rows, err := h.pool.Query(ctx, awardColumns+" WHERE a.edition_id = $1 ORDER BY a.award_type, cc.sort_order, a.created_at", editionID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []award{}
	for rows.Next() {
		a, err := scanAward(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, a)
	}
	return out, rows.Err()
}

// ---------------------------------------------------------------------------
// Public endpoints
// ---------------------------------------------------------------------------

type editionDetail struct {
	edition
	Categories []category `json:"categories"`
}

// ListEditions serves visible editions. Draft is never public.
func (h *Handler) ListEditions(w http.ResponseWriter, r *http.Request) {
	rows, err := h.pool.Query(r.Context(), editionColumns+`
WHERE status <> 'draft'
ORDER BY coalesce(opens_at, created_at) DESC`)
	if err != nil {
		handler.WriteServerError(w, r, "INTERNAL_ERROR", "failed to list competition editions", err)
		return
	}
	defer rows.Close()
	out := []edition{}
	for rows.Next() {
		e, err := scanEdition(rows)
		if err != nil {
			handler.WriteServerError(w, r, "INTERNAL_ERROR", "failed to read competition editions", err)
			return
		}
		out = append(out, e)
	}
	if err := rows.Err(); err != nil {
		handler.WriteServerError(w, r, "INTERNAL_ERROR", "failed to read competition editions", err)
		return
	}
	writePublicCache(w)
	writeJSON(w, http.StatusOK, map[string]any{"items": out})
}

// GetEdition serves one visible edition with its categories.
func (h *Handler) GetEdition(w http.ResponseWriter, r *http.Request) {
	slug := strings.TrimSpace(r.PathValue("slug"))
	ed, err := h.getEdition(r.Context(), "slug = $1 AND status <> 'draft'", slug)
	if err != nil {
		writeEditionError(w, r, err)
		return
	}
	categories, err := h.listCategories(r.Context(), ed.ID)
	if err != nil {
		handler.WriteServerError(w, r, "INTERNAL_ERROR", "failed to read competition categories", err)
		return
	}
	writePublicCache(w)
	writeJSON(w, http.StatusOK, editionDetail{edition: ed, Categories: categories})
}

// ListCategories serves the edition's category taxonomy.
func (h *Handler) ListCategories(w http.ResponseWriter, r *http.Request) {
	ed, err := h.getEdition(r.Context(), "slug = $1 AND status <> 'draft'", strings.TrimSpace(r.PathValue("slug")))
	if err != nil {
		writeEditionError(w, r, err)
		return
	}
	categories, err := h.listCategories(r.Context(), ed.ID)
	if err != nil {
		handler.WriteServerError(w, r, "INTERNAL_ERROR", "failed to read competition categories", err)
		return
	}
	writePublicCache(w)
	writeJSON(w, http.StatusOK, map[string]any{"items": categories})
}

// ListEntries serves public-safe entries. Contact details, age band, team
// roster and eligibility notes never appear here.
func (h *Handler) ListEntries(w http.ResponseWriter, r *http.Request) {
	slug := strings.TrimSpace(r.PathValue("slug"))
	ed, err := h.getEdition(r.Context(), "slug = $1 AND status <> 'draft'", slug)
	if err != nil {
		writeEditionError(w, r, err)
		return
	}
	categoryKey := strings.TrimSpace(r.URL.Query().Get("category"))
	rows, err := h.pool.Query(r.Context(), `
SELECT ce.id::text, cc.key, cc.label, ce.repository_entry_id::text, re.slug,
       re.title, re.summary, re.county, re.author_name, ce.team_name
FROM competition_entries ce
JOIN competition_categories cc ON cc.id = ce.category_id
JOIN repository_entries re ON re.id = ce.repository_entry_id
WHERE ce.edition_id = $1
  AND ce.eligibility_status = 'eligible'
  AND re.status = 'published'
  AND ($2::text = '' OR cc.key = $2)
ORDER BY cc.sort_order, re.published_at DESC NULLS LAST, re.title`, ed.ID, categoryKey)
	if err != nil {
		handler.WriteServerError(w, r, "INTERNAL_ERROR", "failed to list competition entries", err)
		return
	}
	defer rows.Close()
	out := []entryPublic{}
	for rows.Next() {
		var e entryPublic
		if err := rows.Scan(&e.ID, &e.CategoryKey, &e.CategoryLabel,
			&e.RepositoryEntryID, &e.RepositorySlug, &e.Title, &e.Summary,
			&e.County, &e.CreditName, &e.TeamName); err != nil {
			handler.WriteServerError(w, r, "INTERNAL_ERROR", "failed to read competition entries", err)
			return
		}
		out = append(out, e)
	}
	if err := rows.Err(); err != nil {
		handler.WriteServerError(w, r, "INTERNAL_ERROR", "failed to read competition entries", err)
		return
	}
	writePublicCache(w)
	writeJSON(w, http.StatusOK, map[string]any{"items": out})
}

// Leaderboard serves the live top ten per category, without expert scores.
func (h *Handler) Leaderboard(w http.ResponseWriter, r *http.Request) {
	ed, err := h.getEdition(r.Context(), "slug = $1 AND status <> 'draft'", strings.TrimSpace(r.PathValue("slug")))
	if err != nil {
		writeEditionError(w, r, err)
		return
	}
	board, err := h.buildLeaderboard(r.Context(), ed)
	if err != nil {
		handler.WriteServerError(w, r, "INTERNAL_ERROR", "failed to build leaderboard", err)
		return
	}
	writePublicCache(w)
	writeJSON(w, http.StatusOK, board)
}

// Results serves final results only after the freeze has produced a snapshot.
func (h *Handler) Results(w http.ResponseWriter, r *http.Request) {
	ed, err := h.getEdition(r.Context(), "slug = $1 AND status <> 'draft'", strings.TrimSpace(r.PathValue("slug")))
	if err != nil {
		writeEditionError(w, r, err)
		return
	}
	if ed.Status != "closed" && ed.Status != "published" {
		handler.WriteError(w, http.StatusConflict, "RESULTS_NOT_PUBLISHED", "results are not available yet")
		return
	}
	results, err := h.buildResults(r.Context(), ed)
	if err != nil {
		handler.WriteServerError(w, r, "INTERNAL_ERROR", "failed to build results", err)
		return
	}
	if !results.Frozen {
		handler.WriteError(w, http.StatusConflict, "RESULTS_NOT_FROZEN", "results are not frozen yet")
		return
	}
	writePublicCache(w)
	writeJSON(w, http.StatusOK, results)
}

// Winners serves published award records only.
func (h *Handler) Winners(w http.ResponseWriter, r *http.Request) {
	ed, err := h.getEdition(r.Context(), "slug = $1 AND status = 'published'", strings.TrimSpace(r.PathValue("slug")))
	if err != nil {
		writeEditionError(w, r, err)
		return
	}
	rows, err := h.pool.Query(r.Context(), awardColumns+`
WHERE a.edition_id = $1 AND a.published_at IS NOT NULL
ORDER BY a.award_type, cc.sort_order, a.created_at`, ed.ID)
	if err != nil {
		handler.WriteServerError(w, r, "INTERNAL_ERROR", "failed to read winners", err)
		return
	}
	defer rows.Close()
	out := []award{}
	for rows.Next() {
		a, err := scanAward(rows)
		if err != nil {
			handler.WriteServerError(w, r, "INTERNAL_ERROR", "failed to read winners", err)
			return
		}
		out = append(out, a)
	}
	if err := rows.Err(); err != nil {
		handler.WriteServerError(w, r, "INTERNAL_ERROR", "failed to read winners", err)
		return
	}
	writePublicCache(w)
	writeJSON(w, http.StatusOK, map[string]any{"items": out})
}

// Settings publishes machine-readable policy, never prose. It lets clients
// render the vote caps and explain which mode is active.
func (h *Handler) Settings(w http.ResponseWriter, r *http.Request) {
	writePublicCache(w)
	writeJSON(w, http.StatusOK, publicSettings{
		PromotionTypes:  []string{PromotionAwardProgramme, PromotionPrizeCompetition, PromotionLicensedOperator},
		PublicVoteModes: []string{VoteModePeopleChoiceOnly, VoteModeWeighted, VoteModeNone},
		VoteCaps:        voteCaps{PerCategory: VoteCapPerCategory, Total: VoteCapTotal},
		Weights:         weights{Expert: ExpertWeight, Public: PublicWeight},
		Rubric: rubric{
			Relevance: RubricRelevanceWeight, Craft: RubricCraftWeight,
			Originality: RubricOriginalityWeight, Impact: RubricImpactWeight, ScaleMax: 10,
		},
		Requirements: voteChecks{VerifiedEmail: true, AccountBeforeVotingOpen: true},
	})
}

func writeEditionError(w http.ResponseWriter, r *http.Request, err error) {
	if isNoRows(err) {
		handler.WriteError(w, http.StatusNotFound, "NOT_FOUND", "competition edition not found")
		return
	}
	handler.WriteServerError(w, r, "INTERNAL_ERROR", "failed to read competition edition", err)
}
