package competition

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/bradleygichuru/ytci-go/internal/handler"
	"github.com/bradleygichuru/ytci-go/internal/middleware"
)

// Handler serves the competition and award domain: public reads, app voting and
// judging, and the admin operations surface.
type Handler struct {
	pool *pgxpool.Pool
}

func New(pool *pgxpool.Pool) *Handler {
	return &Handler{pool: pool}
}

// ---------------------------------------------------------------------------
// Response and request helpers
// ---------------------------------------------------------------------------

func writeJSON(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(body)
}

func writePublicCache(w http.ResponseWriter) {
	w.Header().Set("Cache-Control", "public, s-maxage=30, stale-while-revalidate=120")
}

type validationCollector struct {
	errs []validationField
}

type validationField struct {
	Field   string `json:"field"`
	Message string `json:"message"`
}

func (v *validationCollector) add(field, message string) {
	v.errs = append(v.errs, validationField{Field: field, Message: message})
}

func (v *validationCollector) empty() bool { return len(v.errs) == 0 }

func (v *validationCollector) write(w http.ResponseWriter) bool {
	if v.empty() {
		return false
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusBadRequest)
	_ = json.NewEncoder(w).Encode(map[string]any{"errors": v.errs})
	return true
}

func decodeBody(w http.ResponseWriter, r *http.Request, maxBytes int64, dst any) bool {
	r.Body = http.MaxBytesReader(w, r.Body, maxBytes)
	if err := json.NewDecoder(r.Body).Decode(dst); err != nil {
		handler.WriteError(w, http.StatusBadRequest, "INVALID_REQUEST", "invalid request body")
		return false
	}
	return true
}

func parseLimit(raw string, def, max int) int {
	if raw == "" {
		return def
	}
	n, err := strconv.Atoi(raw)
	if err != nil || n < 1 {
		return def
	}
	if n > max {
		return max
	}
	return n
}

func parseOffset(raw string) int {
	n, err := strconv.Atoi(raw)
	if err != nil || n < 0 {
		return 0
	}
	return n
}

func validUUID(raw string) bool {
	_, err := uuid.Parse(strings.TrimSpace(raw))
	return err == nil
}

func textPtr(raw *string) *string {
	if raw == nil {
		return nil
	}
	trimmed := strings.TrimSpace(*raw)
	if trimmed == "" {
		return nil
	}
	return &trimmed
}

func isNoRows(err error) bool { return errors.Is(err, pgx.ErrNoRows) }

func writeVoteError(w http.ResponseWriter, err *VoteError) {
	status := http.StatusConflict
	switch err.Code {
	case "EMAIL_UNVERIFIED", "ACCOUNT_TOO_NEW", "VOTING_CLOSED", "VOTING_NOT_OPEN", "VOTING_DISABLED":
		status = http.StatusForbidden
	case "SELF_VOTE", "TEAM_VOTE", "JUDGE_BLOCKED":
		status = http.StatusForbidden
	}
	handler.WriteError(w, status, err.Code, err.Message)
}

func actor(ctx context.Context) string { return middleware.UserID(ctx) }

// ---------------------------------------------------------------------------
// Scanners
// ---------------------------------------------------------------------------

type scanner interface {
	Scan(dest ...any) error
}

const editionColumns = `
SELECT id::text, slug, name, theme, status, promotion_type, min_age, max_age,
       public_vote_mode, prize_enabled, rules_version, opens_at, closes_at,
       voting_opens_at, voting_closes_at, announcement_at, created_at, updated_at
FROM competition_editions`

func scanEdition(row scanner) (edition, error) {
	var e edition
	err := row.Scan(&e.ID, &e.Slug, &e.Name, &e.Theme, &e.Status, &e.PromotionType,
		&e.MinAge, &e.MaxAge, &e.PublicVoteMode, &e.PrizeEnabled, &e.RulesVersion,
		&e.OpensAt, &e.ClosesAt, &e.VotingOpensAt, &e.VotingClosesAt,
		&e.AnnouncementAt, &e.CreatedAt, &e.UpdatedAt)
	return e, err
}

const categoryColumns = `
SELECT id::text, edition_id::text, key, label, description, sort_order
FROM competition_categories`

func scanCategory(row scanner) (category, error) {
	var c category
	err := row.Scan(&c.ID, &c.EditionID, &c.Key, &c.Label, &c.Description, &c.SortOrder)
	return c, err
}

const entryAdminColumns = `
SELECT ce.id::text, ce.edition_id::text, ce.category_id::text, cc.key, cc.label,
       ce.repository_entry_id::text, re.slug, re.title, ce.lead_user_id,
       ce.team_name, ce.team_members, ce.eligibility_status, ce.eligibility_note,
       (SELECT count(*) FROM competition_scores cs WHERE cs.entry_id = ce.id),
       (SELECT count(*) FROM competition_votes v WHERE v.entry_id = ce.id AND v.invalidated_at IS NULL),
       ce.created_at, ce.updated_at
FROM competition_entries ce
JOIN competition_categories cc ON cc.id = ce.category_id
JOIN repository_entries re ON re.id = ce.repository_entry_id`

func scanEntryAdmin(row scanner) (entryAdmin, error) {
	var e entryAdmin
	var rawTeam []byte
	err := row.Scan(&e.ID, &e.EditionID, &e.CategoryID, &e.CategoryKey, &e.CategoryLabel,
		&e.RepositoryEntryID, &e.RepositorySlug, &e.Title, &e.LeadUserID,
		&e.TeamName, &rawTeam, &e.EligibilityStatus, &e.EligibilityNote,
		&e.ScoreCount, &e.VoteCount, &e.CreatedAt, &e.UpdatedAt)
	if len(rawTeam) > 0 {
		e.TeamMembers = json.RawMessage(rawTeam)
	}
	return e, err
}

const judgeColumns = `
SELECT id::text, edition_id::text, user_id, display_name, email, status, created_at
FROM competition_judges`

func scanJudge(row scanner) (judge, error) {
	var j judge
	err := row.Scan(&j.ID, &j.EditionID, &j.UserID, &j.DisplayName, &j.Email, &j.Status, &j.CreatedAt)
	return j, err
}

const assignmentColumns = `
SELECT id::text, edition_id::text, category_id::text, judge_id::text, status, assigned_at
FROM competition_judge_assignments`

func scanAssignment(row scanner) (assignment, error) {
	var a assignment
	err := row.Scan(&a.ID, &a.EditionID, &a.CategoryID, &a.JudgeID, &a.Status, &a.AssignedAt)
	return a, err
}

const conflictColumns = `
SELECT id::text, judge_id::text, entry_id::text, reason, declared_at
FROM competition_conflicts`

func scanConflict(row scanner) (conflict, error) {
	var c conflict
	err := row.Scan(&c.ID, &c.JudgeID, &c.EntryID, &c.Reason, &c.DeclaredAt)
	return c, err
}

const scoreColumns = `
SELECT id::text, assignment_id::text, entry_id::text, judge_id::text,
       relevance, craft, originality, impact, total::float8, notes, locked_at,
       created_at, updated_at
FROM competition_scores`

func scanScore(row scanner) (score, error) {
	var s score
	err := row.Scan(&s.ID, &s.AssignmentID, &s.EntryID, &s.JudgeID,
		&s.Relevance, &s.Craft, &s.Originality, &s.Impact, &s.Total, &s.Notes,
		&s.LockedAt, &s.CreatedAt, &s.UpdatedAt)
	return s, err
}

const voteColumns = `
SELECT id::text, edition_id::text, category_id::text, entry_id::text,
       created_at, invalidated_at, reason_code
FROM competition_votes`

func scanVote(row scanner) (vote, error) {
	var v vote
	err := row.Scan(&v.ID, &v.EditionID, &v.CategoryID, &v.EntryID,
		&v.CreatedAt, &v.InvalidatedAt, &v.ReasonCode)
	return v, err
}

const reviewCaseColumns = `
SELECT id::text, edition_id::text, entry_id::text, signal, status, opened_at,
       resolved_at, resolution, reviewer_id
FROM competition_vote_review_cases`

func scanReviewCase(row scanner) (reviewCase, error) {
	var c reviewCase
	err := row.Scan(&c.ID, &c.EditionID, &c.EntryID, &c.Signal, &c.Status,
		&c.OpenedAt, &c.ResolvedAt, &c.Resolution, &c.ReviewerID)
	return c, err
}

const awardColumns = `
SELECT a.id::text, a.edition_id::text, a.category_id::text, a.entry_id::text,
       a.award_type, a.award_form, a.citation, a.published_at, a.created_at,
       re.title, cc.key
FROM competition_awards a
JOIN competition_entries ce ON ce.id = a.entry_id
JOIN repository_entries re ON re.id = ce.repository_entry_id
LEFT JOIN competition_categories cc ON cc.id = a.category_id`

func scanAward(row scanner) (award, error) {
	var a award
	err := row.Scan(&a.ID, &a.EditionID, &a.CategoryID, &a.EntryID,
		&a.AwardType, &a.AwardForm, &a.Citation, &a.PublishedAt, &a.CreatedAt,
		&a.Title, &a.CategoryKey)
	return a, err
}

func (h *Handler) getEdition(ctx context.Context, where string, args ...any) (edition, error) {
	return scanEdition(h.pool.QueryRow(ctx, editionColumns+" WHERE "+where, args...))
}

func (h *Handler) getCategory(ctx context.Context, id string) (category, error) {
	return scanCategory(h.pool.QueryRow(ctx, categoryColumns+" WHERE id = $1", id))
}

func (h *Handler) listCategories(ctx context.Context, editionID string) ([]category, error) {
	rows, err := h.pool.Query(ctx, categoryColumns+" WHERE edition_id = $1 ORDER BY sort_order, label", editionID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []category{}
	for rows.Next() {
		c, err := scanCategory(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

func (h *Handler) getEntryAdmin(ctx context.Context, id string) (entryAdmin, error) {
	return scanEntryAdmin(h.pool.QueryRow(ctx, entryAdminColumns+" WHERE ce.id = $1", id))
}

func (h *Handler) getAssignment(ctx context.Context, id string) (assignment, error) {
	return scanAssignment(h.pool.QueryRow(ctx, assignmentColumns+" WHERE id = $1", id))
}

func normalizeKey(raw string) string {
	return strings.ToLower(strings.TrimSpace(raw))
}

func containsFold(values []string, target string) bool {
	for _, v := range values {
		if strings.EqualFold(v, target) {
			return true
		}
	}
	return false
}

func validateKey(raw string) bool {
	if raw == "" || len(raw) > 64 {
		return false
	}
	for _, r := range raw {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || r == '-' || r == '_' {
			continue
		}
		return false
	}
	return true
}
