package competition

import (
	"encoding/json"
	"net/http"
	"strings"
	"time"

	"github.com/bradleygichuru/ytci-go/internal/handler"
)

type castVoteRequest struct {
	EntryID        string `json:"entryId"`
	IdempotencyKey string `json:"idempotencyKey"`
}

// CastVote records one app vote. The transaction is serialized per voter by
// locking the user row, so the category and total caps cannot be raced.
func (h *Handler) CastVote(w http.ResponseWriter, r *http.Request) {
	userID := actor(r.Context())
	if userID == "" {
		handler.WriteError(w, http.StatusUnauthorized, "UNAUTHORIZED", "authentication required")
		return
	}
	var req castVoteRequest
	if !decodeBody(w, r, 16<<10, &req) {
		return
	}
	req.EntryID = strings.TrimSpace(req.EntryID)
	req.IdempotencyKey = strings.TrimSpace(req.IdempotencyKey)
	if req.IdempotencyKey == "" {
		req.IdempotencyKey = strings.TrimSpace(r.Header.Get("Idempotency-Key"))
	}
	var v validationCollector
	if !validUUID(req.EntryID) {
		v.add("entryId", "must be a UUID")
	}
	if req.IdempotencyKey == "" || len(req.IdempotencyKey) > 200 {
		v.add("idempotencyKey", "is required and must be at most 200 characters")
	}
	if v.write(w) {
		return
	}

	ctx := r.Context()
	tx, err := h.pool.Begin(ctx)
	if err != nil {
		handler.WriteServerError(w, r, "INTERNAL_ERROR", "failed to start vote transaction", err)
		return
	}
	defer tx.Rollback(ctx)

	// Replays of the same key return the original vote, even after the window
	// closes, so a flaky network never produces a duplicate or a false failure.
	var existing vote
	err = tx.QueryRow(ctx, voteColumns+` WHERE idempotency_key = $1`, req.IdempotencyKey).Scan(
		&existing.ID, &existing.EditionID, &existing.CategoryID, &existing.EntryID,
		&existing.CreatedAt, &existing.InvalidatedAt, &existing.ReasonCode)
	if err == nil {
		var owner string
		if err := tx.QueryRow(ctx, `SELECT voter_user_id FROM competition_votes WHERE id = $1`, existing.ID).Scan(&owner); err != nil {
			handler.WriteServerError(w, r, "INTERNAL_ERROR", "failed to read vote", err)
			return
		}
		if owner != userID {
			handler.WriteError(w, http.StatusConflict, "IDEMPOTENCY_KEY_REUSED", "idempotency key belongs to another voter")
			return
		}
		_ = tx.Commit(ctx)
		writeJSON(w, http.StatusOK, existing)
		return
	}
	if !isNoRows(err) {
		handler.WriteServerError(w, r, "INTERNAL_ERROR", "failed to check idempotency key", err)
		return
	}

	var email string
	var verified bool
	var createdAt time.Time
	if err := tx.QueryRow(ctx, `
SELECT email, email_verified, created_at FROM users WHERE id = $1 FOR UPDATE`, userID).
		Scan(&email, &verified, &createdAt); err != nil {
		if isNoRows(err) {
			handler.WriteError(w, http.StatusUnauthorized, "UNAUTHORIZED", "account not found")
			return
		}
		handler.WriteServerError(w, r, "INTERNAL_ERROR", "failed to read voter account", err)
		return
	}

	ed, err := scanEdition(tx.QueryRow(ctx, editionColumns+` WHERE slug = $1`, strings.TrimSpace(r.PathValue("slug"))))
	if err != nil {
		if isNoRows(err) {
			handler.WriteError(w, http.StatusNotFound, "NOT_FOUND", "competition edition not found")
			return
		}
		handler.WriteServerError(w, r, "INTERNAL_ERROR", "failed to read competition edition", err)
		return
	}

	var leadUserID *string
	var teamMembers []byte
	var eligibility string
	var categoryID string
	if err := tx.QueryRow(ctx, `
SELECT category_id::text, lead_user_id, team_members, eligibility_status
FROM competition_entries WHERE id = $1 AND edition_id = $2`, req.EntryID, ed.ID).
		Scan(&categoryID, &leadUserID, &teamMembers, &eligibility); err != nil {
		if isNoRows(err) {
			handler.WriteError(w, http.StatusNotFound, "NOT_FOUND", "competition entry not found")
			return
		}
		handler.WriteServerError(w, r, "INTERNAL_ERROR", "failed to read competition entry", err)
		return
	}
	if eligibility != "eligible" {
		handler.WriteError(w, http.StatusConflict, "ENTRY_NOT_ELIGIBLE", "this entry is not open for voting")
		return
	}

	isSelf := leadUserID != nil && *leadUserID == userID
	isTeam := containsFold(TeamMemberIDs(teamMembers), userID) ||
		containsFold(TeamMemberEmails(teamMembers), email)

	var judgeBlocked bool
	if err := tx.QueryRow(ctx, `
SELECT EXISTS (
    SELECT 1
    FROM competition_judges j
    JOIN competition_judge_assignments a ON a.judge_id = j.id
    WHERE j.edition_id = $1 AND j.user_id = $2
      AND a.category_id = $3 AND a.status <> 'recused'
)`, ed.ID, userID, categoryID).Scan(&judgeBlocked); err != nil {
		handler.WriteServerError(w, r, "INTERNAL_ERROR", "failed to check judge assignment", err)
		return
	}

	var alreadyVoted bool
	if err := tx.QueryRow(ctx, `
SELECT EXISTS (SELECT 1 FROM competition_votes
WHERE voter_user_id = $1 AND entry_id = $2 AND invalidated_at IS NULL)`,
		userID, req.EntryID).Scan(&alreadyVoted); err != nil {
		handler.WriteServerError(w, r, "INTERNAL_ERROR", "failed to check existing vote", err)
		return
	}
	var inCategory, total int
	if err := tx.QueryRow(ctx, `
SELECT
  count(*) FILTER (WHERE category_id = $3),
  count(*)
FROM competition_votes
WHERE voter_user_id = $1 AND edition_id = $2 AND invalidated_at IS NULL`,
		userID, ed.ID, categoryID).Scan(&inCategory, &total); err != nil {
		handler.WriteServerError(w, r, "INTERNAL_ERROR", "failed to count votes", err)
		return
	}

	if violation := CheckVote(VoteCheck{
		EditionStatus: ed.Status, PublicVoteMode: ed.PublicVoteMode,
		VotingOpensAt: ed.VotingOpensAt, VotingClosesAt: ed.VotingClosesAt,
		Now:              time.Now().UTC(),
		AccountCreatedAt: createdAt, EmailVerified: verified,
		IsSelf: isSelf, IsTeamMember: isTeam, IsJudgeInCategory: judgeBlocked,
		AlreadyVotedEntry: alreadyVoted, VotesInCategory: inCategory, VotesTotal: total,
		MaxPerCategory: VoteCapPerCategory, MaxTotal: VoteCapTotal,
	}); violation != nil {
		writeVoteError(w, violation)
		return
	}

	var created vote
	err = tx.QueryRow(ctx, `
INSERT INTO competition_votes (edition_id, category_id, entry_id, voter_user_id, idempotency_key)
VALUES ($1, $2, $3, $4, $5)
RETURNING id::text, edition_id::text, category_id::text, entry_id::text,
          created_at, invalidated_at, reason_code`,
		ed.ID, categoryID, req.EntryID, userID, req.IdempotencyKey).Scan(
		&created.ID, &created.EditionID, &created.CategoryID, &created.EntryID,
		&created.CreatedAt, &created.InvalidatedAt, &created.ReasonCode)
	if err != nil {
		if isUniqueViolation(err) {
			handler.WriteError(w, http.StatusConflict, "VOTE_CONFLICT", "this vote already exists or the idempotency key was used")
			return
		}
		handler.WriteServerError(w, r, "INTERNAL_ERROR", "failed to record vote", err)
		return
	}
	if err := tx.Commit(ctx); err != nil {
		handler.WriteServerError(w, r, "INTERNAL_ERROR", "failed to commit vote", err)
		return
	}
	writeJSON(w, http.StatusCreated, created)
}

// WithdrawVote soft-invalidates the caller's own vote. The ledger row stays.
func (h *Handler) WithdrawVote(w http.ResponseWriter, r *http.Request) {
	userID := actor(r.Context())
	id := strings.TrimSpace(r.PathValue("voteId"))
	if !validUUID(id) {
		handler.WriteError(w, http.StatusNotFound, "NOT_FOUND", "vote not found")
		return
	}
	ctx := r.Context()
	var editionStatus string
	var closesAt *time.Time
	var voter *string
	var invalidatedAt *time.Time
	if err := h.pool.QueryRow(ctx, `
SELECT v.voter_user_id, v.invalidated_at, e.status, e.voting_closes_at
FROM competition_votes v
JOIN competition_editions e ON e.id = v.edition_id
WHERE v.id = $1`, id).Scan(&voter, &invalidatedAt, &editionStatus, &closesAt); err != nil {
		if isNoRows(err) {
			handler.WriteError(w, http.StatusNotFound, "NOT_FOUND", "vote not found")
			return
		}
		handler.WriteServerError(w, r, "INTERNAL_ERROR", "failed to read vote", err)
		return
	}
	if voter == nil || *voter != userID {
		handler.WriteError(w, http.StatusNotFound, "NOT_FOUND", "vote not found")
		return
	}
	if invalidatedAt != nil {
		handler.WriteError(w, http.StatusConflict, "VOTE_ALREADY_INVALID", "this vote is already withdrawn")
		return
	}
	if !votingStatuses[editionStatus] || (closesAt != nil && time.Now().UTC().After(*closesAt)) {
		handler.WriteError(w, http.StatusConflict, "VOTING_CLOSED", "voting has closed")
		return
	}
	if _, err := h.pool.Exec(ctx, `
UPDATE competition_votes
SET invalidated_at = now(), invalidated_by = $2, reason_code = 'withdrawn'
WHERE id = $1 AND invalidated_at IS NULL`, id, userID); err != nil {
		handler.WriteServerError(w, r, "INTERNAL_ERROR", "failed to withdraw vote", err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// MyVotes lists the caller's votes for one edition, including withdrawals.
func (h *Handler) MyVotes(w http.ResponseWriter, r *http.Request) {
	userID := actor(r.Context())
	ed, err := h.getEdition(r.Context(), "slug = $1 AND status <> 'draft'", strings.TrimSpace(r.PathValue("slug")))
	if err != nil {
		writeEditionError(w, r, err)
		return
	}
	rows, err := h.pool.Query(r.Context(), `
SELECT v.id::text, v.edition_id::text, v.category_id::text, v.entry_id::text,
       v.created_at, v.invalidated_at, v.reason_code, re.title, cc.key
FROM competition_votes v
JOIN competition_entries ce ON ce.id = v.entry_id
JOIN repository_entries re ON re.id = ce.repository_entry_id
JOIN competition_categories cc ON cc.id = v.category_id
WHERE v.voter_user_id = $1 AND v.edition_id = $2
ORDER BY v.created_at DESC`, userID, ed.ID)
	if err != nil {
		handler.WriteServerError(w, r, "INTERNAL_ERROR", "failed to list votes", err)
		return
	}
	defer rows.Close()
	out := []myVote{}
	for rows.Next() {
		var v myVote
		if err := rows.Scan(&v.ID, &v.EditionID, &v.CategoryID, &v.EntryID,
			&v.CreatedAt, &v.InvalidatedAt, &v.ReasonCode, &v.Title, &v.CategoryKey); err != nil {
			handler.WriteServerError(w, r, "INTERNAL_ERROR", "failed to read votes", err)
			return
		}
		out = append(out, v)
	}
	if err := rows.Err(); err != nil {
		handler.WriteServerError(w, r, "INTERNAL_ERROR", "failed to read votes", err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": out})
}

func decodeJSONB(raw []byte) any {
	if len(raw) == 0 {
		return nil
	}
	var v any
	if err := json.Unmarshal(raw, &v); err != nil {
		return nil
	}
	return v
}
