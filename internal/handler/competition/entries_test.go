package competition_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ---------------------------------------------------------------------------
// Self-entry helpers
// ---------------------------------------------------------------------------

func (h *harness) seedOwnedRepository(t *testing.T, slug, email, status string) string {
	t.Helper()
	var id string
	err := h.pool.QueryRow(context.Background(), `
INSERT INTO repository_entries
(kind, slug, title, summary, body_md, author_name, author_email, author_phone,
 author_age_band, status, source, published_at)
VALUES ('competition_entry', $1, $2, 'A story about a place.', 'Body', 'Credit Name',
        $3, '+254700000000', '18_24', $4, 'public',
        CASE WHEN $4 = 'published' THEN now() ELSE NULL END)
RETURNING id::text`, slug, "Title "+slug, email, status).Scan(&id)
	require.NoError(t, err)
	return id
}

func (h *harness) seedAgeBand(t *testing.T, userID, band string) {
	t.Helper()
	_, err := h.pool.Exec(context.Background(), `
INSERT INTO user_profiles (user_id, age_range, created_by)
VALUES ($1,$2,$1)
ON CONFLICT (user_id) DO UPDATE SET age_range = EXCLUDED.age_range`, userID, band)
	require.NoError(t, err)
}

func (h *harness) createEntryViaAPI(t *testing.T, slug, user, repoID, categoryID string, guardian bool) *httptest.ResponseRecorder {
	t.Helper()
	rec, _ := h.do(t, "POST", "/v1/mobile/competition/editions/"+slug+"/entries", map[string]any{
		"repositoryEntryId": repoID,
		"categoryId":        categoryID,
		"teamName":          "Team One",
		"teamMembers":       []map[string]string{{"name": "Amani"}, {"name": "Wanjiru"}},
		"guardianConsent":   guardian,
	}, "user", user)
	return rec
}

func openWindow() (*time.Time, *time.Time) {
	open := time.Now().UTC().Add(-48 * time.Hour)
	closeAt := time.Now().UTC().Add(48 * time.Hour)
	return &open, &closeAt
}

func decodeRec(t *testing.T, rec *httptest.ResponseRecorder) map[string]any {
	t.Helper()
	var body map[string]any
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &body))
	return body
}

// ---------------------------------------------------------------------------
// Ownership, publication, auth
// ---------------------------------------------------------------------------

func TestEntryRequiresOwnPublishedWork(t *testing.T) {
	h := setup(t)
	now := time.Now().UTC()
	h.seedUser(t, "owner", "user", true, now.Add(-48*time.Hour))
	h.seedUser(t, "other", "user", true, now.Add(-48*time.Hour))
	h.seedUser(t, "unverified", "user", false, now.Add(-48*time.Hour))
	h.seedAgeBand(t, "owner", "18-24")
	h.seedAgeBand(t, "other", "18-24")
	h.seedAgeBand(t, "unverified", "18-24")

	open, closeAt := openWindow()
	editionID := h.seedEdition(t, editionSeed{Slug: "entry-owner", Status: "open", Opens: open, Closes: closeAt})
	photo := h.seedCategory(t, editionID, "photography")
	video := h.seedCategory(t, editionID, "video")
	repo := h.seedOwnedRepository(t, "owner-work", "owner@test.example", "published")
	draftRepo := h.seedOwnedRepository(t, "owner-draft", "owner@test.example", "draft")

	path := "/v1/mobile/competition/editions/entry-owner/entries"

	rec, _ := h.do(t, "POST", path, map[string]any{
		"repositoryEntryId": repo, "categoryId": photo, "guardianConsent": false,
	}, "", "")
	assert.Equal(t, http.StatusUnauthorized, rec.Code, "guests cannot enter")

	rec, _ = h.do(t, "POST", path, map[string]any{
		"repositoryEntryId": repo, "categoryId": photo, "guardianConsent": false,
	}, "user", "unverified")
	assert.Equal(t, http.StatusForbidden, rec.Code)
	assert.Contains(t, rec.Body.String(), "EMAIL_UNVERIFIED")

	rec, _ = h.do(t, "POST", path, map[string]any{
		"repositoryEntryId": repo, "categoryId": photo, "guardianConsent": false,
	}, "user", "other")
	assert.Equal(t, http.StatusForbidden, rec.Code)
	assert.Contains(t, rec.Body.String(), "REPOSITORY_ENTRY_NOT_OWNED")

	rec = h.createEntryViaAPI(t, "entry-owner", "owner", repo, photo, false)
	require.Equal(t, http.StatusCreated, rec.Code)
	body := decodeRec(t, rec)
	assert.Equal(t, "pending", body["eligibilityStatus"])
	assert.Equal(t, "photography", body["categoryKey"])
	assert.Equal(t, "Credit Name", body["creditName"])
	assert.NotContains(t, rec.Body.String(), "owner@test.example")
	assert.NotContains(t, rec.Body.String(), "+254700000000")
	assert.NotContains(t, rec.Body.String(), "18_24")

	rec = h.createEntryViaAPI(t, "entry-owner", "owner", draftRepo, video, false)
	assert.Equal(t, http.StatusConflict, rec.Code)
	assert.Contains(t, rec.Body.String(), "REPOSITORY_ENTRY_NOT_PUBLISHED")

	rec, _ = h.do(t, "POST", path, map[string]any{
		"repositoryEntryId": "00000000-0000-0000-0000-000000000000",
		"categoryId":        video, "guardianConsent": false,
	}, "user", "owner")
	assert.Equal(t, http.StatusNotFound, rec.Code)
	assert.Contains(t, rec.Body.String(), "REPOSITORY_ENTRY_NOT_FOUND")
}

// ---------------------------------------------------------------------------
// Category caps and duplicates
// ---------------------------------------------------------------------------

func TestEntryCategoryCapsAndDuplicates(t *testing.T) {
	h := setup(t)
	now := time.Now().UTC()
	h.seedUser(t, "entrant", "user", true, now.Add(-48*time.Hour))
	h.seedAgeBand(t, "entrant", "18-24")

	open, closeAt := openWindow()
	editionID := h.seedEdition(t, editionSeed{Slug: "entry-caps", Status: "open", Opens: open, Closes: closeAt})
	photo := h.seedCategory(t, editionID, "photography")
	video := h.seedCategory(t, editionID, "video")
	story := h.seedCategory(t, editionID, "storytelling")
	repoA := h.seedOwnedRepository(t, "caps-a", "entrant@test.example", "published")
	repoB := h.seedOwnedRepository(t, "caps-b", "entrant@test.example", "published")
	repoC := h.seedOwnedRepository(t, "caps-c", "entrant@test.example", "published")

	require.Equal(t, http.StatusCreated, h.createEntryViaAPI(t, "entry-caps", "entrant", repoA, photo, false).Code)
	require.Equal(t, http.StatusCreated, h.createEntryViaAPI(t, "entry-caps", "entrant", repoB, video, false).Code)

	// One entry per category.
	rec := h.createEntryViaAPI(t, "entry-caps", "entrant", repoC, photo, false)
	assert.Equal(t, http.StatusConflict, rec.Code)
	assert.Contains(t, rec.Body.String(), "ENTRY_EXISTS")

	// The same work cannot be reused in a second category.
	rec = h.createEntryViaAPI(t, "entry-caps", "entrant", repoA, story, false)
	assert.Equal(t, http.StatusConflict, rec.Code)
	assert.Contains(t, rec.Body.String(), "ENTRY_EXISTS")

	// Two categories is the rulebook maximum.
	rec = h.createEntryViaAPI(t, "entry-caps", "entrant", repoC, story, false)
	assert.Equal(t, http.StatusConflict, rec.Code)
	assert.Contains(t, rec.Body.String(), "CATEGORY_LIMIT")

	rec, list := h.do(t, "GET", "/v1/mobile/competition/editions/entry-caps/eligible-repository-entries", nil, "user", "entrant")
	require.Equal(t, http.StatusOK, rec.Code)
	assert.Equal(t, float64(2), list["maxCategories"])
	assert.Equal(t, float64(2), list["categoriesUsed"])
	assert.Equal(t, float64(0), list["categoriesRemaining"])
	items := list["items"].([]any)
	require.Len(t, items, 3)
	reasons := map[string]string{}
	for _, raw := range items {
		item := raw.(map[string]any)
		if reason, ok := item["ineligibleReason"].(string); ok {
			reasons[item["slug"].(string)] = reason
		}
	}
	assert.Equal(t, "already_entered", reasons["caps-a"])
	assert.Equal(t, "already_entered", reasons["caps-b"])
	assert.Equal(t, "category_limit", reasons["caps-c"])
}

func TestEligibleRepositoryEntriesOwnsOnlyOwnPublishedWork(t *testing.T) {
	h := setup(t)
	now := time.Now().UTC()
	h.seedUser(t, "entrant", "user", true, now.Add(-48*time.Hour))
	h.seedUser(t, "stranger", "user", true, now.Add(-48*time.Hour))
	open, closeAt := openWindow()
	h.seedEdition(t, editionSeed{Slug: "entry-list", Status: "open", Opens: open, Closes: closeAt})

	h.seedOwnedRepository(t, "mine", "entrant@test.example", "published")
	h.seedOwnedRepository(t, "mine-draft", "entrant@test.example", "submitted")
	h.seedOwnedRepository(t, "theirs", "stranger@test.example", "published")

	rec, body := h.do(t, "GET", "/v1/mobile/competition/editions/entry-list/eligible-repository-entries", nil, "user", "entrant")
	require.Equal(t, http.StatusOK, rec.Code)
	items := body["items"].([]any)
	require.Len(t, items, 1)
	assert.Equal(t, "mine", items[0].(map[string]any)["slug"])
	assert.NotContains(t, rec.Body.String(), "entrant@test.example")
	assert.NotContains(t, rec.Body.String(), "theirs")
}

// ---------------------------------------------------------------------------
// Guardian consent and age band
// ---------------------------------------------------------------------------

func TestEntryGuardianConsent(t *testing.T) {
	h := setup(t)
	now := time.Now().UTC()
	h.seedUser(t, "minor", "user", true, now.Add(-48*time.Hour))
	h.seedUser(t, "adult", "user", true, now.Add(-48*time.Hour))
	h.seedUser(t, "noage", "user", true, now.Add(-48*time.Hour))
	h.seedAgeBand(t, "minor", "Under 18")
	h.seedAgeBand(t, "adult", "18-24")

	open, closeAt := openWindow()
	editionID := h.seedEdition(t, editionSeed{Slug: "entry-guardian", Status: "open", Opens: open, Closes: closeAt})
	photo := h.seedCategory(t, editionID, "photography")
	minorRepo := h.seedOwnedRepository(t, "minor-work", "minor@test.example", "published")
	adultRepo := h.seedOwnedRepository(t, "adult-work", "adult@test.example", "published")
	noageRepo := h.seedOwnedRepository(t, "noage-work", "noage@test.example", "published")

	rec := h.createEntryViaAPI(t, "entry-guardian", "minor", minorRepo, photo, false)
	assert.Equal(t, http.StatusConflict, rec.Code)
	assert.Contains(t, rec.Body.String(), "GUARDIAN_CONSENT_REQUIRED")

	rec = h.createEntryViaAPI(t, "entry-guardian", "minor", minorRepo, photo, true)
	require.Equal(t, http.StatusCreated, rec.Code)
	assert.Equal(t, true, decodeRec(t, rec)["guardianConsent"])

	rec = h.createEntryViaAPI(t, "entry-guardian", "adult", adultRepo, photo, false)
	require.Equal(t, http.StatusCreated, rec.Code)
	assert.Equal(t, false, decodeRec(t, rec)["guardianConsent"])

	rec = h.createEntryViaAPI(t, "entry-guardian", "noage", noageRepo, photo, false)
	assert.Equal(t, http.StatusConflict, rec.Code)
	assert.Contains(t, rec.Body.String(), "AGE_BAND_REQUIRED")
}

// ---------------------------------------------------------------------------
// Edit and withdraw windows
// ---------------------------------------------------------------------------

func TestEntryEditAndWithdrawUntilClose(t *testing.T) {
	h := setup(t)
	now := time.Now().UTC()
	h.seedUser(t, "entrant", "user", true, now.Add(-48*time.Hour))
	h.seedUser(t, "other", "user", true, now.Add(-48*time.Hour))
	h.seedAgeBand(t, "entrant", "18-24")
	h.seedAgeBand(t, "other", "18-24")

	open, closeAt := openWindow()
	editionID := h.seedEdition(t, editionSeed{Slug: "entry-edit", Status: "open", Opens: open, Closes: closeAt})
	photo := h.seedCategory(t, editionID, "photography")
	repo := h.seedOwnedRepository(t, "edit-work", "entrant@test.example", "published")

	rec := h.createEntryViaAPI(t, "entry-edit", "entrant", repo, photo, false)
	require.Equal(t, http.StatusCreated, rec.Code)
	entryID := decodeRec(t, rec)["id"].(string)

	editPath := "/v1/mobile/competition/entries/" + entryID
	rec, updated := h.do(t, "PATCH", editPath, map[string]any{
		"teamName":    "New Team Name",
		"teamMembers": []map[string]string{{"name": "Only Member"}},
	}, "user", "entrant")
	require.Equal(t, http.StatusOK, rec.Code)
	assert.Equal(t, "New Team Name", updated["teamName"])
	members := updated["teamMembers"].([]any)
	require.Len(t, members, 1)
	assert.Equal(t, "Only Member", members[0].(map[string]any)["name"])
	assert.NotContains(t, rec.Body.String(), "email")

	rec, _ = h.do(t, "PATCH", editPath, map[string]any{"teamName": "Hijacked"}, "user", "other")
	assert.Equal(t, http.StatusNotFound, rec.Code)

	rec, mine := h.do(t, "GET", "/v1/mobile/competition/editions/entry-edit/my-entries", nil, "user", "entrant")
	require.Equal(t, http.StatusOK, rec.Code)
	assert.Len(t, mine["items"], 1)

	rec, _ = h.do(t, "DELETE", editPath, nil, "user", "other")
	assert.Equal(t, http.StatusNotFound, rec.Code)

	rec, _ = h.do(t, "DELETE", editPath, nil, "user", "entrant")
	assert.Equal(t, http.StatusNoContent, rec.Code)

	rec, _ = h.do(t, "PATCH", editPath, map[string]any{"teamName": "After"}, "user", "entrant")
	assert.Equal(t, http.StatusConflict, rec.Code)
	assert.Contains(t, rec.Body.String(), "ENTRY_WITHDRAWN")

	rec, mine = h.do(t, "GET", "/v1/mobile/competition/editions/entry-edit/my-entries", nil, "user", "entrant")
	require.Equal(t, http.StatusOK, rec.Code)
	assert.Len(t, mine["items"], 0)
}

func TestEntryWindowRejectsBeforeOpenAndAfterClose(t *testing.T) {
	h := setup(t)
	now := time.Now().UTC()
	h.seedUser(t, "entrant", "user", true, now.Add(-48*time.Hour))
	h.seedAgeBand(t, "entrant", "18-24")
	repo := h.seedOwnedRepository(t, "window-work", "entrant@test.example", "published")

	futureOpen := now.Add(24 * time.Hour)
	futureClose := now.Add(72 * time.Hour)
	beforeID := h.seedEdition(t, editionSeed{Slug: "entry-before", Status: "open", Opens: &futureOpen, Closes: &futureClose})
	beforeCat := h.seedCategory(t, beforeID, "photography")
	rec := h.createEntryViaAPI(t, "entry-before", "entrant", repo, beforeCat, false)
	assert.Equal(t, http.StatusConflict, rec.Code)
	assert.Contains(t, rec.Body.String(), "ENTRIES_NOT_OPEN")

	pastOpen := now.Add(-72 * time.Hour)
	pastClose := now.Add(-24 * time.Hour)
	afterID := h.seedEdition(t, editionSeed{Slug: "entry-after", Status: "open", Opens: &pastOpen, Closes: &pastClose})
	afterCat := h.seedCategory(t, afterID, "photography")
	rec = h.createEntryViaAPI(t, "entry-after", "entrant", repo, afterCat, false)
	assert.Equal(t, http.StatusConflict, rec.Code)
	assert.Contains(t, rec.Body.String(), "ENTRIES_CLOSED")

	open, closeAt := openWindow()
	liveID := h.seedEdition(t, editionSeed{Slug: "entry-live", Status: "open", Opens: open, Closes: closeAt})
	liveCat := h.seedCategory(t, liveID, "photography")
	rec = h.createEntryViaAPI(t, "entry-live", "entrant", repo, liveCat, false)
	require.Equal(t, http.StatusCreated, rec.Code)
	entryID := decodeRec(t, rec)["id"].(string)

	// Close the edition after creation: edits and withdrawals stop.
	_, err := h.pool.Exec(context.Background(), `UPDATE competition_editions SET closes_at = $2 WHERE id = $1`, liveID, pastClose)
	require.NoError(t, err)
	rec, _ = h.do(t, "PATCH", "/v1/mobile/competition/entries/"+entryID, map[string]any{"teamName": "Late"}, "user", "entrant")
	assert.Equal(t, http.StatusConflict, rec.Code)
	assert.Contains(t, rec.Body.String(), "ENTRIES_CLOSED")
	rec, _ = h.do(t, "DELETE", "/v1/mobile/competition/entries/"+entryID, nil, "user", "entrant")
	assert.Equal(t, http.StatusConflict, rec.Code)
	assert.Contains(t, rec.Body.String(), "ENTRIES_CLOSED")
}

// ---------------------------------------------------------------------------
// Small decode helpers
// ---------------------------------------------------------------------------
