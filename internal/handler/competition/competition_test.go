package competition_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bradleygichuru/ytci-go/internal/db"
	"github.com/bradleygichuru/ytci-go/internal/handler/competition"
	"github.com/bradleygichuru/ytci-go/internal/middleware"
)

type harness struct {
	pool *pgxpool.Pool
	root http.Handler
}

func setup(t *testing.T) *harness {
	t.Helper()
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}
	pool := db.SetupTestDB(t)
	h := competition.New(pool)
	root := chi.NewRouter()
	root.Route("/v1/public", func(pub chi.Router) { h.RegisterPublic(pub) })
	root.Route("/v1/mobile", func(m chi.Router) {
		m.Use(testAuth)
		m.Use(middleware.AuthGate)
		h.RegisterMobile(m)
	})
	root.Route("/v1/admin/competition", func(a chi.Router) {
		a.Use(testAuth)
		a.Use(competition.AdminGate)
		h.RegisterAdmin(a)
	})
	return &harness{pool: pool, root: root}
}

func testAuth(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ctx := context.WithValue(r.Context(), middleware.CtxRole, r.Header.Get("X-Test-Role"))
		ctx = context.WithValue(ctx, middleware.CtxUserID, r.Header.Get("X-Test-User"))
		ctx = context.WithValue(ctx, middleware.CtxEmail, r.Header.Get("X-Test-Email"))
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

func (h *harness) do(t *testing.T, method, path string, body any, role, user string) (*httptest.ResponseRecorder, map[string]any) {
	t.Helper()
	var reader io.Reader
	if body != nil {
		raw, err := json.Marshal(body)
		require.NoError(t, err)
		reader = bytes.NewReader(raw)
	}
	req := httptest.NewRequest(method, "http://test"+path, reader)
	if role != "" {
		req.Header.Set("X-Test-Role", role)
	}
	if user != "" {
		req.Header.Set("X-Test-User", user)
		req.Header.Set("X-Test-Email", user+"@test.example")
	}
	rec := httptest.NewRecorder()
	h.root.ServeHTTP(rec, req)
	var decoded map[string]any
	if rec.Body.Len() > 0 {
		_ = json.Unmarshal(rec.Body.Bytes(), &decoded)
	}
	return rec, decoded
}

// ---------------------------------------------------------------------------
// Seed helpers
// ---------------------------------------------------------------------------

func (h *harness) seedUser(t *testing.T, id, role string, verified bool, createdAt time.Time) {
	t.Helper()
	_, err := h.pool.Exec(context.Background(), `
INSERT INTO users (id, name, email, email_verified, created_at, updated_at, role)
VALUES ($1,$2,$3,$4,$5,$5,$6)`, id, id, id+"@test.example", verified, createdAt, role)
	require.NoError(t, err)
}

func (h *harness) seedRepositoryEntry(t *testing.T, slug string) string {
	t.Helper()
	var id string
	err := h.pool.QueryRow(context.Background(), `
INSERT INTO repository_entries
(kind, slug, title, summary, body_md, author_name, author_email, author_phone,
 author_age_band, status, source, published_at)
VALUES ('competition_entry', $1, $2, 'A story about a place.', 'Body', 'Credit Name',
        'private@example.com', '+254700000000', '18_24', 'published', 'admin', now())
RETURNING id::text`, slug, "Title "+slug).Scan(&id)
	require.NoError(t, err)
	return id
}

type editionSeed struct {
	Slug        string
	Status      string
	Mode        string
	Opens       *time.Time
	Closes      *time.Time
	VotingOpen  *time.Time
	VotingClose *time.Time
	MinAge      *int
	MaxAge      *int
}

func (h *harness) seedEdition(t *testing.T, s editionSeed) string {
	t.Helper()
	if s.Status == "" {
		s.Status = "voting"
	}
	if s.Mode == "" {
		s.Mode = competition.VoteModePeopleChoiceOnly
	}
	var id string
	err := h.pool.QueryRow(context.Background(), `
INSERT INTO competition_editions
(slug, name, theme, status, promotion_type, min_age, max_age, public_vote_mode,
 prize_enabled, rules_version, opens_at, closes_at, voting_opens_at, voting_closes_at)
VALUES ($1,$2,'Theme',$3,'award_programme',$4,$5,$6,false,'1.0.0',$7,$8,$9,$10)
RETURNING id::text`,
		s.Slug, "Edition "+s.Slug, s.Status, s.MinAge, s.MaxAge, s.Mode,
		s.Opens, s.Closes, s.VotingOpen, s.VotingClose).Scan(&id)
	require.NoError(t, err)
	return id
}

func (h *harness) seedCategory(t *testing.T, editionID, key string) string {
	t.Helper()
	var id string
	err := h.pool.QueryRow(context.Background(), `
INSERT INTO competition_categories (edition_id, key, label, description, sort_order)
VALUES ($1,$2,$3,'Category description',0)
RETURNING id::text`, editionID, key, "Label "+key).Scan(&id)
	require.NoError(t, err)
	return id
}

func (h *harness) seedEntry(t *testing.T, editionID, categoryID, repoID string, lead *string, team json.RawMessage) string {
	t.Helper()
	var id string
	var teamArg any
	if len(team) > 0 {
		teamArg = string(team)
	}
	err := h.pool.QueryRow(context.Background(), `
INSERT INTO competition_entries
(edition_id, category_id, repository_entry_id, lead_user_id, team_members, eligibility_status)
VALUES ($1,$2,$3,$4,$5,'eligible')
RETURNING id::text`, editionID, categoryID, repoID, lead, teamArg).Scan(&id)
	require.NoError(t, err)
	return id
}

func (h *harness) voteViaAPI(t *testing.T, slug, user, entryID, key string) *httptest.ResponseRecorder {
	t.Helper()
	rec, _ := h.do(t, "POST", "/v1/mobile/competition/editions/"+slug+"/votes",
		map[string]any{"entryId": entryID, "idempotencyKey": key}, "user", user)
	return rec
}

func votingWindow() (*time.Time, *time.Time) {
	open := time.Now().UTC().Add(-time.Hour)
	closeAt := time.Now().UTC().Add(time.Hour)
	return &open, &closeAt
}

// ---------------------------------------------------------------------------
// Public reads
// ---------------------------------------------------------------------------

func TestPublicReadsExposeNoPii(t *testing.T) {
	h := setup(t)
	lead := "lead-user"
	h.seedUser(t, lead, "user", true, time.Now().Add(-48*time.Hour))
	repoID := h.seedRepositoryEntry(t, "public-read-entry")
	open, closeAt := votingWindow()
	editionID := h.seedEdition(t, editionSeed{Slug: "public-read", VotingOpen: open, VotingClose: closeAt})
	h.seedCategory(t, editionID, "photography")
	var categoryID string
	require.NoError(t, h.pool.QueryRow(context.Background(), `SELECT id::text FROM competition_categories WHERE edition_id=$1`, editionID).Scan(&categoryID))
	entryID := h.seedEntry(t, editionID, categoryID, repoID, &lead, json.RawMessage(`[{"name":"Team Mate","userId":"mate-1","email":"mate@example.com"}]`))
	_, _ = h.pool.Exec(context.Background(), `
UPDATE competition_entries SET eligibility_note = 'internal note' WHERE id = $1`, entryID)

	for _, path := range []string{
		"/v1/public/competition/editions",
		"/v1/public/competition/editions/public-read",
		"/v1/public/competition/editions/public-read/categories",
		"/v1/public/competition/editions/public-read/entries",
		"/v1/public/competition/editions/public-read/leaderboard",
		"/v1/public/competition/settings",
	} {
		rec, _ := h.do(t, "GET", path, nil, "", "")
		assert.Equal(t, http.StatusOK, rec.Code, path)
		raw := rec.Body.String()
		assert.NotContains(t, raw, "private@example.com", path)
		assert.NotContains(t, raw, "+254700000000", path)
		assert.NotContains(t, raw, "leadUserId", path)
		assert.NotContains(t, raw, "internal note", path)
	}

	rec, body := h.do(t, "GET", "/v1/public/competition/editions/public-read/entries", nil, "", "")
	require.Equal(t, http.StatusOK, rec.Code)
	items := body["items"].([]any)
	require.Len(t, items, 1)
	assert.NotContains(t, items[0], "authorEmail")

	rec, _ = h.do(t, "GET", "/v1/public/competition/editions/public-read/results", nil, "", "")
	assert.Equal(t, http.StatusConflict, rec.Code)

	_, settings := h.do(t, "GET", "/v1/public/competition/settings", nil, "", "")
	promotionTypes := settings["promotionTypes"].([]any)
	assert.Contains(t, promotionTypes, "award_programme")
	assert.Equal(t, float64(5), settings["voteCaps"].(map[string]any)["perCategory"])
}

// ---------------------------------------------------------------------------
// Admin authorization
// ---------------------------------------------------------------------------

func TestAdminAuthorization(t *testing.T) {
	h := setup(t)
	now := time.Now().Add(-48 * time.Hour)
	h.seedUser(t, "county", "county_officer", true, now)
	h.seedUser(t, "moderator", "moderator", true, now)
	h.seedUser(t, "admin", "administrator", true, now)
	h.seedUser(t, "reader", "user", true, now)

	rec, _ := h.do(t, "GET", "/v1/admin/competition/editions", nil, "county_officer", "county")
	assert.Equal(t, http.StatusOK, rec.Code, "county officers keep read access")

	rec, _ = h.do(t, "POST", "/v1/admin/competition/editions", map[string]any{
		"slug": "county-draft", "name": "County Draft", "rulesVersion": "1.0.0",
	}, "county_officer", "county")
	assert.Equal(t, http.StatusForbidden, rec.Code, "county officers cannot edit")

	rec, body := h.do(t, "POST", "/v1/admin/competition/editions", map[string]any{
		"slug": "moderator-draft", "name": "Moderator Draft", "rulesVersion": "1.0.0",
	}, "moderator", "moderator")
	require.Equal(t, http.StatusCreated, rec.Code)
	editionID := body["id"].(string)
	assert.Equal(t, "award_programme", body["promotionType"])
	assert.Equal(t, "people_choice_only", body["publicVoteMode"])
	assert.Equal(t, false, body["prizeEnabled"])
	assert.Nil(t, body["minAge"])

	rec, body = h.do(t, "POST", "/v1/admin/competition/editions/"+editionID+"/categories", map[string]any{
		"key": "photography", "label": "Photography", "sortOrder": 0,
	}, "moderator", "moderator")
	require.Equal(t, http.StatusCreated, rec.Code)
	categoryID := body["id"].(string)

	repoID := h.seedRepositoryEntry(t, "admin-created-entry")
	rec, body = h.do(t, "POST", "/v1/admin/competition/editions/"+editionID+"/entries", map[string]any{
		"repositoryEntryId": repoID, "categoryId": categoryID,
		"teamName": "Team One", "teamMembers": []map[string]any{{"name": "Mate", "userId": "mate-1"}},
	}, "moderator", "moderator")
	require.Equal(t, http.StatusCreated, rec.Code)
	entryID := body["id"].(string)
	assert.Equal(t, "pending", body["eligibilityStatus"])
	assert.NotNil(t, body["teamMembers"])

	rec, _ = h.do(t, "POST", "/v1/admin/competition/entries/"+entryID+"/eligibility", map[string]any{
		"status": "ineligible",
	}, "moderator", "moderator")
	assert.Equal(t, http.StatusBadRequest, rec.Code)

	rec, body = h.do(t, "POST", "/v1/admin/competition/entries/"+entryID+"/eligibility", map[string]any{
		"status": "ineligible", "note": "Rights unclear",
	}, "moderator", "moderator")
	require.Equal(t, http.StatusOK, rec.Code)
	assert.Equal(t, "ineligible", body["eligibilityStatus"])
	assert.Equal(t, "Rights unclear", body["eligibilityNote"])

	rec, _ = h.do(t, "DELETE", "/v1/admin/competition/editions/"+editionID, nil, "moderator", "moderator")
	assert.Equal(t, http.StatusNoContent, rec.Code)

	rec, _ = h.do(t, "GET", "/v1/admin/competition/editions", nil, "user", "reader")
	assert.Equal(t, http.StatusForbidden, rec.Code)
}

// ---------------------------------------------------------------------------
// Voting
// ---------------------------------------------------------------------------

func TestVoteFlows(t *testing.T) {
	h := setup(t)
	now := time.Now().UTC()
	open, closeAt := votingWindow()
	h.seedUser(t, "voter", "user", true, now.Add(-48*time.Hour))
	h.seedUser(t, "voter2", "user", true, now.Add(-48*time.Hour))
	h.seedUser(t, "unverified", "user", false, now.Add(-48*time.Hour))
	h.seedUser(t, "fresh", "user", true, now)
	h.seedUser(t, "judge", "user", true, now.Add(-48*time.Hour))
	h.seedUser(t, "lead", "user", true, now.Add(-48*time.Hour))

	editionID := h.seedEdition(t, editionSeed{Slug: "vote-edition", VotingOpen: open, VotingClose: closeAt})
	categoryID := h.seedCategory(t, editionID, "video")

	openLead := "voter"
	selfRepo := h.seedRepositoryEntry(t, "self-entry")
	selfEntry := h.seedEntry(t, editionID, categoryID, selfRepo, &openLead, nil)

	teamRepo := h.seedRepositoryEntry(t, "team-entry")
	teamEntry := h.seedEntry(t, editionID, categoryID, teamRepo, nil, json.RawMessage(`[{"name":"Voter","userId":"voter"}]`))

	judgeRepo := h.seedRepositoryEntry(t, "judge-entry")
	judgeEntry := h.seedEntry(t, editionID, categoryID, judgeRepo, nil, nil)
	var judgeID string
	require.NoError(t, h.pool.QueryRow(context.Background(), `
INSERT INTO competition_judges (edition_id, user_id, display_name, status)
VALUES ($1,'judge','Judge One','active') RETURNING id::text`, editionID).Scan(&judgeID))
	_, err := h.pool.Exec(context.Background(), `
INSERT INTO competition_judge_assignments (edition_id, category_id, judge_id, status)
VALUES ($1,$2,$3,'assigned')`, editionID, categoryID, judgeID)
	require.NoError(t, err)

	plainRepo := h.seedRepositoryEntry(t, "plain-entry")
	plainEntry := h.seedEntry(t, editionID, categoryID, plainRepo, nil, nil)

	rec := h.voteViaAPI(t, "vote-edition", "voter", selfEntry, "key-self")
	assert.Equal(t, http.StatusForbidden, rec.Code)
	assert.Contains(t, rec.Body.String(), "SELF_VOTE")

	rec = h.voteViaAPI(t, "vote-edition", "voter", teamEntry, "key-team")
	assert.Equal(t, http.StatusForbidden, rec.Code)
	assert.Contains(t, rec.Body.String(), "TEAM_VOTE")

	rec = h.voteViaAPI(t, "vote-edition", "judge", judgeEntry, "key-judge")
	assert.Equal(t, http.StatusForbidden, rec.Code)
	assert.Contains(t, rec.Body.String(), "JUDGE_BLOCKED")

	rec = h.voteViaAPI(t, "vote-edition", "unverified", plainEntry, "key-unverified")
	assert.Equal(t, http.StatusForbidden, rec.Code)
	assert.Contains(t, rec.Body.String(), "EMAIL_UNVERIFIED")

	rec = h.voteViaAPI(t, "vote-edition", "fresh", plainEntry, "key-fresh")
	assert.Equal(t, http.StatusForbidden, rec.Code)
	assert.Contains(t, rec.Body.String(), "ACCOUNT_TOO_NEW")

	rec, body := h.do(t, "POST", "/v1/mobile/competition/editions/vote-edition/votes",
		map[string]any{"entryId": plainEntry, "idempotencyKey": "key-1"}, "user", "voter")
	require.Equal(t, http.StatusCreated, rec.Code)
	voteID := body["id"].(string)

	// Same key replays the original vote; a new key is a duplicate vote.
	rec = h.voteViaAPI(t, "vote-edition", "voter", plainEntry, "key-1")
	assert.Equal(t, http.StatusOK, rec.Code)
	rec = h.voteViaAPI(t, "vote-edition", "voter", plainEntry, "key-2")
	assert.Equal(t, http.StatusConflict, rec.Code)
	assert.Contains(t, rec.Body.String(), "ALREADY_VOTED")

	rec, body = h.do(t, "GET", "/v1/mobile/competition/editions/vote-edition/my-votes", nil, "user", "voter")
	require.Equal(t, http.StatusOK, rec.Code)
	assert.Len(t, body["items"], 1)

	rec, _ = h.do(t, "DELETE", "/v1/mobile/competition/votes/"+voteID, nil, "user", "voter2")
	assert.Equal(t, http.StatusNotFound, rec.Code, "another voter cannot withdraw the vote")

	rec, _ = h.do(t, "DELETE", "/v1/mobile/competition/votes/"+voteID, nil, "user", "voter")
	assert.Equal(t, http.StatusNoContent, rec.Code)

	rec, body = h.do(t, "GET", "/v1/mobile/competition/editions/vote-edition/my-votes", nil, "user", "voter")
	require.Equal(t, http.StatusOK, rec.Code)
	item := body["items"].([]any)[0].(map[string]any)
	assert.NotNil(t, item["invalidatedAt"])
	assert.Equal(t, "withdrawn", item["reasonCode"])
}

func TestVoteWindowAndMode(t *testing.T) {
	h := setup(t)
	now := time.Now().UTC()
	h.seedUser(t, "voter", "user", true, now.Add(-72*time.Hour))
	repo := h.seedRepositoryEntry(t, "window-entry")

	pastOpen := now.Add(-3 * time.Hour)
	pastClose := now.Add(-2 * time.Hour)
	editionID := h.seedEdition(t, editionSeed{Slug: "closed-vote", VotingOpen: &pastOpen, VotingClose: &pastClose})
	categoryID := h.seedCategory(t, editionID, "storytelling")
	entryID := h.seedEntry(t, editionID, categoryID, repo, nil, nil)

	rec := h.voteViaAPI(t, "closed-vote", "voter", entryID, "key-window")
	assert.Equal(t, http.StatusForbidden, rec.Code)
	assert.Contains(t, rec.Body.String(), "VOTING_CLOSED")

	open, closeAt := votingWindow()
	noneID := h.seedEdition(t, editionSeed{Slug: "no-vote", Mode: competition.VoteModeNone, VotingOpen: open, VotingClose: closeAt})
	noneCat := h.seedCategory(t, noneID, "storytelling")
	noneEntry := h.seedEntry(t, noneID, noneCat, repo, nil, nil)
	rec = h.voteViaAPI(t, "no-vote", "voter", noneEntry, "key-none")
	assert.Equal(t, http.StatusForbidden, rec.Code)
	assert.Contains(t, rec.Body.String(), "VOTING_DISABLED")
}

// ---------------------------------------------------------------------------
// Judging
// ---------------------------------------------------------------------------

func TestJudgingScoringAndLocks(t *testing.T) {
	h := setup(t)
	now := time.Now().UTC()
	open, closeAt := votingWindow()
	h.seedUser(t, "judge", "user", true, now.Add(-48*time.Hour))
	h.seedUser(t, "admin", "administrator", true, now.Add(-48*time.Hour))
	editionID := h.seedEdition(t, editionSeed{Slug: "judge-edition", Status: "judging", VotingOpen: open, VotingClose: closeAt})
	categoryID := h.seedCategory(t, editionID, "photography")
	repo := h.seedRepositoryEntry(t, "judged-entry")
	entryID := h.seedEntry(t, editionID, categoryID, repo, nil, nil)

	var judgeID string
	require.NoError(t, h.pool.QueryRow(context.Background(), `
INSERT INTO competition_judges (edition_id, user_id, display_name, status)
VALUES ($1,'judge','Judge One','active') RETURNING id::text`, editionID).Scan(&judgeID))
	var assignmentID string
	require.NoError(t, h.pool.QueryRow(context.Background(), `
INSERT INTO competition_judge_assignments (edition_id, category_id, judge_id, status)
VALUES ($1,$2,$3,'assigned') RETURNING id::text`, editionID, categoryID, judgeID).Scan(&assignmentID))

	rec, body := h.do(t, "GET", "/v1/mobile/competition/judging/assignments", nil, "user", "judge")
	require.Equal(t, http.StatusOK, rec.Code)
	assignments := body["items"].([]any)
	require.Len(t, assignments, 1)
	first := assignments[0].(map[string]any)
	assert.NotContains(t, fmt.Sprint(first), "Credit Name")
	assert.NotContains(t, fmt.Sprint(first), "18_24")

	path := "/v1/mobile/competition/judging/assignments/" + assignmentID + "/entries/" + entryID + "/score"
	rec, body = h.do(t, "PUT", path, map[string]any{
		"relevance": 9, "craft": 8, "originality": 7, "impact": 6, "notes": "Strong work",
	}, "user", "judge")
	require.Equal(t, http.StatusOK, rec.Code)
	assert.Equal(t, 76.0, body["total"])

	rec, _ = h.do(t, "PUT", path, map[string]any{"relevance": 0, "craft": 8, "originality": 7, "impact": 6}, "user", "judge")
	assert.Equal(t, http.StatusBadRequest, rec.Code)

	// A declared conflict recuses the judge's assignment.
	rec, _ = h.do(t, "POST", "/v1/admin/competition/editions/"+editionID+"/conflicts", map[string]any{
		"judgeId": judgeID, "entryId": entryID, "reason": "Family member",
	}, "administrator", "admin")
	require.Equal(t, http.StatusCreated, rec.Code)
	rec, _ = h.do(t, "PUT", path, map[string]any{"relevance": 9, "craft": 8, "originality": 7, "impact": 6}, "user", "judge")
	assert.Equal(t, http.StatusForbidden, rec.Code)
	assert.Contains(t, rec.Body.String(), "JUDGE_RECUSED")

	// A fresh assignment can be locked by an editor.
	rec, _ = h.do(t, "POST", "/v1/admin/competition/editions/"+editionID+"/assignments", map[string]any{
		"categoryId": categoryID, "judgeId": judgeID,
	}, "administrator", "admin")
	assert.Equal(t, http.StatusConflict, rec.Code, "assignment already exists")

	rec, _ = h.do(t, "POST", "/v1/admin/competition/editions/"+editionID+"/scores/lock", map[string]any{"lock": true}, "administrator", "admin")
	assert.Equal(t, http.StatusOK, rec.Code)

	rec, _ = h.do(t, "GET", "/v1/admin/competition/editions/"+editionID+"/scores", nil, "administrator", "admin")
	require.Equal(t, http.StatusOK, rec.Code)
}

func TestJudgeCannotScoreOutsideAssignment(t *testing.T) {
	h := setup(t)
	now := time.Now().UTC()
	h.seedUser(t, "judge", "user", true, now.Add(-48*time.Hour))
	h.seedUser(t, "other", "user", true, now.Add(-48*time.Hour))
	open, closeAt := votingWindow()
	editionID := h.seedEdition(t, editionSeed{Slug: "judge-scope", Status: "judging", VotingOpen: open, VotingClose: closeAt})
	categoryID := h.seedCategory(t, editionID, "video")
	repo := h.seedRepositoryEntry(t, "scope-entry")
	entryID := h.seedEntry(t, editionID, categoryID, repo, nil, nil)
	var judgeID string
	require.NoError(t, h.pool.QueryRow(context.Background(), `
INSERT INTO competition_judges (edition_id, user_id, display_name, status)
VALUES ($1,'other','Other Judge','active') RETURNING id::text`, editionID).Scan(&judgeID))
	var assignmentID string
	require.NoError(t, h.pool.QueryRow(context.Background(), `
INSERT INTO competition_judge_assignments (edition_id, category_id, judge_id, status)
VALUES ($1,$2,$3,'assigned') RETURNING id::text`, editionID, categoryID, judgeID).Scan(&assignmentID))

	rec, _ := h.do(t, "PUT", "/v1/mobile/competition/judging/assignments/"+assignmentID+"/entries/"+entryID+"/score",
		map[string]any{"relevance": 9, "craft": 8, "originality": 7, "impact": 6}, "user", "judge")
	assert.Equal(t, http.StatusForbidden, rec.Code)
}

// ---------------------------------------------------------------------------
// Freeze, results and awards
// ---------------------------------------------------------------------------

func TestFreezeResultsAndAwards(t *testing.T) {
	h := setup(t)
	now := time.Now().UTC()
	open, closeAt := votingWindow()
	h.seedUser(t, "voter", "user", true, now.Add(-48*time.Hour))
	h.seedUser(t, "voter2", "user", true, now.Add(-48*time.Hour))
	h.seedUser(t, "moderator", "moderator", true, now.Add(-48*time.Hour))
	h.seedUser(t, "admin", "administrator", true, now.Add(-48*time.Hour))

	editionID := h.seedEdition(t, editionSeed{Slug: "freeze-edition", VotingOpen: open, VotingClose: closeAt})
	categoryID := h.seedCategory(t, editionID, "photography")
	repoA := h.seedRepositoryEntry(t, "freeze-a")
	repoB := h.seedRepositoryEntry(t, "freeze-b")
	entryA := h.seedEntry(t, editionID, categoryID, repoA, nil, nil)
	entryB := h.seedEntry(t, editionID, categoryID, repoB, nil, nil)

	var judgeID string
	require.NoError(t, h.pool.QueryRow(context.Background(), `
INSERT INTO competition_judges (edition_id, display_name, status)
VALUES ($1,'Panel Judge','active') RETURNING id::text`, editionID).Scan(&judgeID))
	var assignmentID string
	require.NoError(t, h.pool.QueryRow(context.Background(), `
INSERT INTO competition_judge_assignments (edition_id, category_id, judge_id, status)
VALUES ($1,$2,$3,'completed') RETURNING id::text`, editionID, categoryID, judgeID).Scan(&assignmentID))
	_, err := h.pool.Exec(context.Background(), `
INSERT INTO competition_scores
(assignment_id, entry_id, judge_id, relevance, craft, originality, impact, total)
VALUES ($1,$2,$3,9,9,9,9,90), ($1,$4,$3,6,6,6,6,60)`, assignmentID, entryA, judgeID, entryB)
	require.NoError(t, err)

	require.Equal(t, http.StatusCreated, h.voteViaAPI(t, "freeze-edition", "voter", entryA, "freeze-1").Code)
	require.Equal(t, http.StatusCreated, h.voteViaAPI(t, "freeze-edition", "voter2", entryA, "freeze-2").Code)
	require.Equal(t, http.StatusCreated, h.voteViaAPI(t, "freeze-edition", "voter", entryB, "freeze-3").Code)

	rec, body := h.do(t, "POST", "/v1/admin/competition/editions/"+editionID+"/freeze", nil, "moderator", "moderator")
	require.Equal(t, http.StatusOK, rec.Code)
	assert.Equal(t, true, body["frozen"])

	var status string
	require.NoError(t, h.pool.QueryRow(context.Background(), `SELECT status FROM competition_editions WHERE id=$1`, editionID).Scan(&status))
	assert.Equal(t, "closed", status)

	rec, results := h.do(t, "GET", "/v1/public/competition/editions/freeze-edition/results", nil, "", "")
	require.Equal(t, http.StatusOK, rec.Code)
	assert.Equal(t, true, results["frozen"])
	assert.NotContains(t, rec.Body.String(), "judgeId")
	assert.NotContains(t, rec.Body.String(), "individual")

	peopleChoice := results["peopleChoice"].(map[string]any)
	assert.Equal(t, entryA, peopleChoice["entryId"])

	rec, body = h.do(t, "POST", "/v1/admin/competition/editions/"+editionID+"/awards", map[string]any{
		"entryId": entryA, "categoryId": categoryID, "awardType": "category_winner",
		"awardForm": "grant", "citation": "A precise and generous story.",
	}, "administrator", "admin")
	require.Equal(t, http.StatusCreated, rec.Code)
	awardID := body["id"].(string)
	assert.Nil(t, body["publishedAt"])

	rec, _ = h.do(t, "POST", "/v1/admin/competition/editions/"+editionID+"/awards", map[string]any{
		"entryId": entryB, "categoryId": categoryID, "awardType": "category_winner",
	}, "administrator", "admin")
	assert.Equal(t, http.StatusConflict, rec.Code)

	rec, _ = h.do(t, "POST", "/v1/admin/competition/awards/"+awardID+"/publish", nil, "administrator", "admin")
	require.Equal(t, http.StatusOK, rec.Code)

	rec, _ = h.do(t, "PATCH", "/v1/admin/competition/editions/"+editionID, map[string]any{"status": "published"}, "administrator", "admin")
	require.Equal(t, http.StatusOK, rec.Code)

	rec, winners := h.do(t, "GET", "/v1/public/competition/editions/freeze-edition/winners", nil, "", "")
	require.Equal(t, http.StatusOK, rec.Code)
	items := winners["items"].([]any)
	require.Len(t, items, 1)
	assert.Equal(t, "Title freeze-a", items[0].(map[string]any)["title"])

	rec, _ = h.do(t, "POST", "/v1/admin/competition/editions/"+editionID+"/recount", nil, "moderator", "moderator")
	assert.Equal(t, http.StatusOK, rec.Code)
}

func TestConsultationResponsesAreAdminOnly(t *testing.T) {
	h := setup(t)
	now := time.Now().Add(-48 * time.Hour)
	h.seedUser(t, "admin", "administrator", true, now)
	open, closeAt := votingWindow()
	editionID := h.seedEdition(t, editionSeed{Slug: "consult", VotingOpen: open, VotingClose: closeAt})
	_, err := h.pool.Exec(context.Background(), `
INSERT INTO competition_consultation_responses (edition_id, name, email, answers, consent)
VALUES ($1,'Respondent','respondent@example.com','{"q1":"yes"}',true)`, editionID)
	require.NoError(t, err)

	rec, body := h.do(t, "GET", "/v1/admin/competition/editions/"+editionID+"/consultation-responses", nil, "administrator", "admin")
	require.Equal(t, http.StatusOK, rec.Code)
	items := body["items"].([]any)
	require.Len(t, items, 1)
	assert.Equal(t, "respondent@example.com", items[0].(map[string]any)["email"])

	rec, _ = h.do(t, "GET", "/v1/admin/competition/editions/"+editionID+"/consultation-responses/export", nil, "administrator", "admin")
	require.Equal(t, http.StatusOK, rec.Code)
	assert.Contains(t, rec.Header().Get("Content-Type"), "text/csv")
	assert.Contains(t, rec.Body.String(), "respondent@example.com")
}
