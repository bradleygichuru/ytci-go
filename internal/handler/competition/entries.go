package competition

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/bradleygichuru/ytci-go/internal/handler"
)

// Entrant self-entry.
//
// An entrant may enter only their own published repository work. There is no
// author_user_id on the repository schema, so MVP ownership matches the
// verified account email against repository_entries.author_email. The match is
// case-insensitive but never substring: a different account cannot claim work
// by typo or by sharing a domain. Replace this with a stable author_user_id
// when the repository submission flow records one.
//
// Every public-facing response here is entrant-safe: no author email, phone,
// age band or moderation note leaves the API.

const (
	// MaxTeamSize is the rulebook team limit including the lead entrant.
	MaxTeamSize = 5
	// MaxTeamNameLength guards the display-only team name.
	MaxTeamNameLength = 120
	// MaxTeamMemberNameLength guards one member display name.
	MaxTeamMemberNameLength = 120

	AgeBandUnder18 = "Under 18"
)

// EntryError is a machine-readable reason an entry action was rejected.
type EntryError struct {
	Code    string
	Message string
	Status  int
}

func (e *EntryError) Error() string { return e.Message }

// EntryCheck carries every precondition for creating a self-entry. The handler
// fills it from the database so the decision stays deterministic and testable.
type EntryCheck struct {
	EditionStatus string
	OpensAt       *time.Time
	ClosesAt      *time.Time
	Now           time.Time

	EmailVerified   bool
	AgeBand         string
	GuardianConsent bool

	RepositoryOwned     bool
	RepositoryPublished bool

	AlreadyInCategory  bool
	AlreadyEnteredWork bool
	CategoriesUsed     int
	MaxCategories      int
}

// entryWindowStatus is the only edition status that accepts entries. Dates
// narrow the status, never widen it.
var entryWindowStatus = map[string]bool{"open": true}

// CheckEntryWindow returns the window reason an entry action is not allowed.
// Closed and not-yet-open are distinct so the app can say which. A missing
// status is treated as closed; a missing date bound is open on that side.
func CheckEntryWindow(status string, opensAt, closesAt *time.Time, now time.Time) *EntryError {
	if now.IsZero() {
		now = time.Now().UTC()
	}
	if !entryWindowStatus[status] {
		if opensAt != nil && now.Before(*opensAt) {
			return &EntryError{Code: "ENTRIES_NOT_OPEN", Message: "entries have not opened yet", Status: http.StatusConflict}
		}
		return &EntryError{Code: "ENTRIES_CLOSED", Message: "entries are closed for this edition", Status: http.StatusConflict}
	}
	if opensAt != nil && now.Before(*opensAt) {
		return &EntryError{Code: "ENTRIES_NOT_OPEN", Message: "entries have not opened yet", Status: http.StatusConflict}
	}
	if closesAt != nil && now.After(*closesAt) {
		return &EntryError{Code: "ENTRIES_CLOSED", Message: "entries have closed", Status: http.StatusConflict}
	}
	return nil
}

// RequiresGuardianConsent reports whether the stored age band is under 18. The
// second return value is false when the band is missing, which fails closed:
// the entry API never assumes an unknown age is an adult, and the app sends the
// entrant to complete their profile.
func RequiresGuardianConsent(ageBand string) (required, known bool) {
	band := strings.TrimSpace(ageBand)
	if band == "" {
		return false, false
	}
	return strings.EqualFold(band, AgeBandUnder18), true
}

// CheckEntry returns the first reason a self-entry is not allowed. Order is
// deliberate: window, account, age, ownership, publication, then caps. An
// entrant who cannot enter at all never learns category-level detail.
func CheckEntry(c EntryCheck) *EntryError {
	if violation := CheckEntryWindow(c.EditionStatus, c.OpensAt, c.ClosesAt, c.Now); violation != nil {
		return violation
	}
	if !c.EmailVerified {
		return &EntryError{Code: "EMAIL_UNVERIFIED", Message: "verify your email before entering", Status: http.StatusForbidden}
	}
	guardianRequired, ageKnown := RequiresGuardianConsent(c.AgeBand)
	if !ageKnown {
		return &EntryError{Code: "AGE_BAND_REQUIRED", Message: "complete your age range before entering", Status: http.StatusConflict}
	}
	if guardianRequired && !c.GuardianConsent {
		return &EntryError{Code: "GUARDIAN_CONSENT_REQUIRED", Message: "a parent or guardian must give permission for entrants under 18", Status: http.StatusConflict}
	}
	if !c.RepositoryOwned {
		return &EntryError{Code: "REPOSITORY_ENTRY_NOT_OWNED", Message: "this work is not in your repository account", Status: http.StatusForbidden}
	}
	if !c.RepositoryPublished {
		return &EntryError{Code: "REPOSITORY_ENTRY_NOT_PUBLISHED", Message: "this work has not been published in the repository yet", Status: http.StatusConflict}
	}
	if c.AlreadyInCategory || c.AlreadyEnteredWork {
		return &EntryError{Code: "ENTRY_EXISTS", Message: "this work is already entered in this edition", Status: http.StatusConflict}
	}
	maxCategories := c.MaxCategories
	if maxCategories <= 0 {
		maxCategories = MaxCategoriesPerEntrant
	}
	if c.CategoriesUsed >= maxCategories {
		return &EntryError{Code: "CATEGORY_LIMIT", Message: fmt.Sprintf("an entrant can enter at most %d categories", maxCategories), Status: http.StatusConflict}
	}
	return nil
}

// teamMemberView is the only representation of a team member the app receives.
// Emails and user ids stay out of the entrant-facing payload.
type teamMemberView struct {
	Name string `json:"name"`
}

// parseTeamMembers accepts a JSON array of objects with a `name`, or an array
// of strings for older clients. It returns the normalized list and a field
// error message. The lead entrant is not included in this list.
func parseTeamMembers(raw json.RawMessage) ([]teamMemberView, string) {
	if len(raw) == 0 || string(raw) == "null" {
		return nil, ""
	}
	var objects []struct {
		Name string `json:"name"`
	}
	if err := json.Unmarshal(raw, &objects); err != nil {
		var names []string
		if err := json.Unmarshal(raw, &names); err != nil {
			return nil, "must be a list of team members"
		}
		objects = make([]struct {
			Name string `json:"name"`
		}, 0, len(names))
		for _, name := range names {
			objects = append(objects, struct {
				Name string `json:"name"`
			}{Name: name})
		}
	}
	if len(objects) >= MaxTeamSize {
		return nil, fmt.Sprintf("a team can have at most %d people including the lead entrant", MaxTeamSize)
	}
	out := make([]teamMemberView, 0, len(objects))
	for _, member := range objects {
		name := strings.TrimSpace(member.Name)
		if name == "" {
			return nil, "every team member needs a name"
		}
		if len(name) > MaxTeamMemberNameLength {
			return nil, fmt.Sprintf("a team member name must be at most %d characters", MaxTeamMemberNameLength)
		}
		out = append(out, teamMemberView{Name: name})
	}
	if len(out) == 0 {
		return nil, ""
	}
	return out, ""
}

func teamMembersJSON(members []teamMemberView) any {
	if len(members) == 0 {
		return nil
	}
	raw, err := json.Marshal(members)
	if err != nil {
		return nil
	}
	return string(raw)
}

func decodeTeamMembers(raw []byte) []teamMemberView {
	if len(raw) == 0 {
		return []teamMemberView{}
	}
	var members []teamMemberView
	if err := json.Unmarshal(raw, &members); err == nil {
		out := make([]teamMemberView, 0, len(members))
		for _, member := range members {
			if name := strings.TrimSpace(member.Name); name != "" {
				out = append(out, teamMemberView{Name: name})
			}
		}
		return out
	}
	var names []string
	if err := json.Unmarshal(raw, &names); err == nil {
		out := make([]teamMemberView, 0, len(names))
		for _, name := range names {
			if trimmed := strings.TrimSpace(name); trimmed != "" {
				out = append(out, teamMemberView{Name: trimmed})
			}
		}
		return out
	}
	return []teamMemberView{}
}

// myEntry is the entrant's own view of a competition entry. The public-safe
// fields mirror entryPublic, plus the review state the entrant needs.
type myEntry struct {
	ID                string           `json:"id"`
	EditionID         string           `json:"editionId"`
	CategoryID        string           `json:"categoryId"`
	CategoryKey       string           `json:"categoryKey"`
	CategoryLabel     string           `json:"categoryLabel"`
	RepositoryEntryID string           `json:"repositoryEntryId"`
	RepositorySlug    string           `json:"repositorySlug"`
	Title             string           `json:"title"`
	Summary           string           `json:"summary"`
	County            *string          `json:"county"`
	CreditName        *string          `json:"creditName"`
	TeamName          *string          `json:"teamName"`
	TeamMembers       []teamMemberView `json:"teamMembers"`
	EligibilityStatus string           `json:"eligibilityStatus"`
	EligibilityNote   *string          `json:"eligibilityNote"`
	GuardianConsent   bool             `json:"guardianConsent"`
	CreatedAt         time.Time        `json:"createdAt"`
	UpdatedAt         time.Time        `json:"updatedAt"`
}

const myEntryColumns = `
SELECT ce.id::text, ce.edition_id::text, ce.category_id::text, cc.key, cc.label,
       ce.repository_entry_id::text, re.slug, re.title, re.summary, re.county,
       re.author_name, ce.team_name, ce.team_members, ce.eligibility_status,
       ce.eligibility_note, ce.guardian_consent, ce.created_at, ce.updated_at
FROM competition_entries ce
JOIN competition_categories cc ON cc.id = ce.category_id
JOIN repository_entries re ON re.id = ce.repository_entry_id`

func scanMyEntry(row scanner) (myEntry, error) {
	var e myEntry
	var rawTeam []byte
	err := row.Scan(&e.ID, &e.EditionID, &e.CategoryID, &e.CategoryKey, &e.CategoryLabel,
		&e.RepositoryEntryID, &e.RepositorySlug, &e.Title, &e.Summary, &e.County,
		&e.CreditName, &e.TeamName, &rawTeam, &e.EligibilityStatus,
		&e.EligibilityNote, &e.GuardianConsent, &e.CreatedAt, &e.UpdatedAt)
	if err != nil {
		return e, err
	}
	e.TeamMembers = decodeTeamMembers(rawTeam)
	// A note only exists after a reviewer has explained a rejection.
	if e.EligibilityStatus != "ineligible" {
		e.EligibilityNote = nil
	}
	return e, nil
}

// entrantAccount is the subset of the account and profile the entry rules need.
type entrantAccount struct {
	Email    string
	Verified bool
	AgeBand  string
}

type queryRower interface {
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
}

func loadEntrant(ctx context.Context, q queryRower, userID string) (entrantAccount, error) {
	var account entrantAccount
	var ageBand *string
	err := q.QueryRow(ctx, `
SELECT u.email, u.email_verified, up.age_range
FROM users u
LEFT JOIN user_profiles up ON up.user_id = u.id
WHERE u.id = $1`, userID).Scan(&account.Email, &account.Verified, &ageBand)
	if ageBand != nil {
		account.AgeBand = strings.TrimSpace(*ageBand)
	}
	return account, err
}

// ---------------------------------------------------------------------------
// Eligible repository work
// ---------------------------------------------------------------------------

type eligibleRepositoryEntry struct {
	ID                  string     `json:"id"`
	Slug                string     `json:"slug"`
	Title               string     `json:"title"`
	Summary             string     `json:"summary"`
	County              *string    `json:"county"`
	Category            *string    `json:"category"`
	PublishedAt         *time.Time `json:"publishedAt"`
	EnteredCategoryKeys []string   `json:"enteredCategoryKeys"`
	Eligible            bool       `json:"eligible"`
	IneligibleReason    *string    `json:"ineligibleReason"`
}

type eligibleRepositoryResponse struct {
	Items               []eligibleRepositoryEntry `json:"items"`
	MaxCategories       int                       `json:"maxCategories"`
	CategoriesUsed      int                       `json:"categoriesUsed"`
	CategoriesRemaining int                       `json:"categoriesRemaining"`
}

// EligibleRepositoryEntries lists the caller's published repository work and
// marks what can still be entered. Ineligible work stays visible with a reason
// so the app can explain why a card is unavailable instead of hiding it.
func (h *Handler) EligibleRepositoryEntries(w http.ResponseWriter, r *http.Request) {
	userID := actor(r.Context())
	if userID == "" {
		handler.WriteError(w, http.StatusUnauthorized, "UNAUTHORIZED", "authentication required")
		return
	}
	ctx := r.Context()
	account, err := loadEntrant(ctx, h.pool, userID)
	if err != nil {
		if isNoRows(err) {
			handler.WriteError(w, http.StatusUnauthorized, "UNAUTHORIZED", "account not found")
			return
		}
		handler.WriteServerError(w, r, "INTERNAL_ERROR", "failed to read entrant account", err)
		return
	}
	if !account.Verified {
		handler.WriteError(w, http.StatusForbidden, "EMAIL_UNVERIFIED", "verify your email before entering")
		return
	}
	ed, err := h.getEdition(ctx, "slug = $1 AND status <> 'draft'", strings.TrimSpace(r.PathValue("slug")))
	if err != nil {
		writeEditionError(w, r, err)
		return
	}

	rows, err := h.pool.Query(ctx, `
SELECT re.id::text, re.slug, re.title, re.summary, re.county, re.category, re.published_at
FROM repository_entries re
WHERE re.status = 'published'
  AND lower(coalesce(re.author_email, '')) = lower($1)
ORDER BY re.published_at DESC NULLS LAST, re.created_at DESC`, account.Email)
	if err != nil {
		handler.WriteServerError(w, r, "INTERNAL_ERROR", "failed to list repository work", err)
		return
	}
	defer rows.Close()
	items := []eligibleRepositoryEntry{}
	for rows.Next() {
		var item eligibleRepositoryEntry
		if err := rows.Scan(&item.ID, &item.Slug, &item.Title, &item.Summary,
			&item.County, &item.Category, &item.PublishedAt); err != nil {
			handler.WriteServerError(w, r, "INTERNAL_ERROR", "failed to read repository work", err)
			return
		}
		item.EnteredCategoryKeys = []string{}
		items = append(items, item)
	}
	if err := rows.Err(); err != nil {
		handler.WriteServerError(w, r, "INTERNAL_ERROR", "failed to read repository work", err)
		return
	}

	entered, err := h.pool.Query(ctx, `
SELECT ce.repository_entry_id::text, cc.key, ce.category_id::text
FROM competition_entries ce
JOIN competition_categories cc ON cc.id = ce.category_id
WHERE ce.edition_id = $1 AND ce.lead_user_id = $2 AND ce.withdrawn_at IS NULL`, ed.ID, userID)
	if err != nil {
		handler.WriteServerError(w, r, "INTERNAL_ERROR", "failed to read existing entries", err)
		return
	}
	defer entered.Close()
	categoryByRepo := map[string][]string{}
	usedCategories := map[string]bool{}
	for entered.Next() {
		var repoID, key, categoryID string
		if err := entered.Scan(&repoID, &key, &categoryID); err != nil {
			handler.WriteServerError(w, r, "INTERNAL_ERROR", "failed to read existing entries", err)
			return
		}
		categoryByRepo[repoID] = append(categoryByRepo[repoID], key)
		usedCategories[categoryID] = true
	}
	if err := entered.Err(); err != nil {
		handler.WriteServerError(w, r, "INTERNAL_ERROR", "failed to read existing entries", err)
		return
	}

	for i := range items {
		if keys, ok := categoryByRepo[items[i].ID]; ok {
			items[i].EnteredCategoryKeys = keys
			reason := "already_entered"
			items[i].IneligibleReason = &reason
			continue
		}
		if len(usedCategories) >= MaxCategoriesPerEntrant {
			reason := "category_limit"
			items[i].IneligibleReason = &reason
			continue
		}
		items[i].Eligible = true
	}

	writeJSON(w, http.StatusOK, eligibleRepositoryResponse{
		Items:               items,
		MaxCategories:       MaxCategoriesPerEntrant,
		CategoriesUsed:      len(usedCategories),
		CategoriesRemaining: max(0, MaxCategoriesPerEntrant-len(usedCategories)),
	})
}

// ---------------------------------------------------------------------------
// Create, edit and withdraw
// ---------------------------------------------------------------------------

type createEntryRequest struct {
	RepositoryEntryID string          `json:"repositoryEntryId"`
	CategoryID        string          `json:"categoryId"`
	TeamName          *string         `json:"teamName"`
	TeamMembers       json.RawMessage `json:"teamMembers"`
	GuardianConsent   bool            `json:"guardianConsent"`
}

type updateEntryRequest struct {
	TeamName        *string         `json:"teamName"`
	TeamMembers     json.RawMessage `json:"teamMembers"`
	GuardianConsent *bool           `json:"guardianConsent"`
}

// CreateMyEntry records one self-entry from the caller's published repository
// work. The user row is locked so two concurrent requests cannot both take the
// last category slot.
func (h *Handler) CreateMyEntry(w http.ResponseWriter, r *http.Request) {
	userID := actor(r.Context())
	if userID == "" {
		handler.WriteError(w, http.StatusUnauthorized, "UNAUTHORIZED", "authentication required")
		return
	}
	var req createEntryRequest
	if !decodeBody(w, r, 64<<10, &req) {
		return
	}
	var v validationCollector
	req.RepositoryEntryID = strings.TrimSpace(req.RepositoryEntryID)
	req.CategoryID = strings.TrimSpace(req.CategoryID)
	if !validUUID(req.RepositoryEntryID) {
		v.add("repositoryEntryId", "must be a UUID")
	}
	if !validUUID(req.CategoryID) {
		v.add("categoryId", "must be a UUID")
	}
	members, memberErr := parseTeamMembers(req.TeamMembers)
	if memberErr != "" {
		v.add("teamMembers", memberErr)
	}
	teamName := textPtr(req.TeamName)
	if teamName != nil && len(*teamName) > MaxTeamNameLength {
		v.add("teamName", fmt.Sprintf("must be at most %d characters", MaxTeamNameLength))
	}
	if v.write(w) {
		return
	}

	ctx := r.Context()
	tx, err := h.pool.Begin(ctx)
	if err != nil {
		handler.WriteServerError(w, r, "INTERNAL_ERROR", "failed to start entry transaction", err)
		return
	}
	defer tx.Rollback(ctx)

	// Serialize self-entry per account. The row lock is the category-cap lock.
	var lockedID string
	if err := tx.QueryRow(ctx, `SELECT id FROM users WHERE id = $1 FOR UPDATE`, userID).Scan(&lockedID); err != nil {
		if isNoRows(err) {
			handler.WriteError(w, http.StatusUnauthorized, "UNAUTHORIZED", "account not found")
			return
		}
		handler.WriteServerError(w, r, "INTERNAL_ERROR", "failed to lock entrant account", err)
		return
	}
	account, err := loadEntrant(ctx, tx, userID)
	if err != nil {
		handler.WriteServerError(w, r, "INTERNAL_ERROR", "failed to read entrant account", err)
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
	cat, err := scanCategory(tx.QueryRow(ctx, categoryColumns+` WHERE id = $1`, req.CategoryID))
	if err != nil || cat.EditionID != ed.ID {
		handler.WriteError(w, http.StatusBadRequest, "INVALID_CATEGORY", "category does not belong to this edition")
		return
	}

	var repoStatus string
	var repoEmail *string
	if err := tx.QueryRow(ctx, `
SELECT status, author_email FROM repository_entries WHERE id = $1`, req.RepositoryEntryID).
		Scan(&repoStatus, &repoEmail); err != nil {
		if isNoRows(err) {
			handler.WriteError(w, http.StatusNotFound, "REPOSITORY_ENTRY_NOT_FOUND", "repository work not found")
			return
		}
		handler.WriteServerError(w, r, "INTERNAL_ERROR", "failed to read repository work", err)
		return
	}
	owned := repoEmail != nil && strings.EqualFold(strings.TrimSpace(*repoEmail), strings.TrimSpace(account.Email))

	var alreadyInCategory, alreadyEnteredWork bool
	var categoriesUsed int
	if err := tx.QueryRow(ctx, `
SELECT
  EXISTS (SELECT 1 FROM competition_entries
          WHERE edition_id = $1 AND category_id = $2 AND lead_user_id = $3 AND withdrawn_at IS NULL),
  EXISTS (SELECT 1 FROM competition_entries
          WHERE edition_id = $1 AND repository_entry_id = $4 AND withdrawn_at IS NULL),
  (SELECT count(DISTINCT category_id) FROM competition_entries
   WHERE edition_id = $1 AND lead_user_id = $3 AND withdrawn_at IS NULL AND category_id <> $2)`,
		ed.ID, req.CategoryID, userID, req.RepositoryEntryID).
		Scan(&alreadyInCategory, &alreadyEnteredWork, &categoriesUsed); err != nil {
		handler.WriteServerError(w, r, "INTERNAL_ERROR", "failed to check existing entries", err)
		return
	}

	if violation := CheckEntry(EntryCheck{
		EditionStatus: ed.Status, OpensAt: ed.OpensAt, ClosesAt: ed.ClosesAt, Now: time.Now().UTC(),
		EmailVerified: account.Verified, AgeBand: account.AgeBand, GuardianConsent: req.GuardianConsent,
		RepositoryOwned: owned, RepositoryPublished: repoStatus == "published",
		AlreadyInCategory: alreadyInCategory, AlreadyEnteredWork: alreadyEnteredWork,
		CategoriesUsed: categoriesUsed, MaxCategories: MaxCategoriesPerEntrant,
	}); violation != nil {
		handler.WriteError(w, violation.Status, violation.Code, violation.Message)
		return
	}

	var createdID string
	err = tx.QueryRow(ctx, `
INSERT INTO competition_entries
(edition_id, category_id, repository_entry_id, lead_user_id, team_name, team_members,
 eligibility_status, guardian_consent, guardian_consent_at)
VALUES ($1,$2,$3,$4,$5,$6,'pending',$7,CASE WHEN $7 THEN now() ELSE NULL END)
RETURNING id::text`,
		ed.ID, req.CategoryID, req.RepositoryEntryID, userID, teamName,
		teamMembersJSON(members), req.GuardianConsent).Scan(&createdID)
	if err != nil {
		if isUniqueViolation(err) {
			handler.WriteError(w, http.StatusConflict, "ENTRY_EXISTS", "this work is already entered in this edition")
			return
		}
		handler.WriteServerError(w, r, "INTERNAL_ERROR", "failed to create entry", err)
		return
	}
	created, err := scanMyEntry(tx.QueryRow(ctx, myEntryColumns+` WHERE ce.id = $1`, createdID))
	if err != nil {
		handler.WriteServerError(w, r, "INTERNAL_ERROR", "failed to read entry", err)
		return
	}
	if err := tx.Commit(ctx); err != nil {
		handler.WriteServerError(w, r, "INTERNAL_ERROR", "failed to commit entry", err)
		return
	}
	writeJSON(w, http.StatusCreated, created)
}

// loadEditableEntry locks the caller's entry and returns the edition window.
func (h *Handler) loadEditableEntry(ctx context.Context, tx pgx.Tx, entryID, userID string) (edition, *time.Time, *EntryError) {
	var editionID string
	var leadUserID *string
	var withdrawnAt *time.Time
	if err := tx.QueryRow(ctx, `
SELECT edition_id::text, lead_user_id, withdrawn_at
FROM competition_entries WHERE id = $1 FOR UPDATE`, entryID).
		Scan(&editionID, &leadUserID, &withdrawnAt); err != nil {
		if isNoRows(err) {
			return edition{}, nil, &EntryError{Code: "NOT_FOUND", Message: "entry not found", Status: http.StatusNotFound}
		}
		return edition{}, nil, &EntryError{Code: "INTERNAL_ERROR", Message: "failed to read entry", Status: http.StatusInternalServerError}
	}
	// Not-owned and missing are the same answer so an entry id cannot be probed.
	if leadUserID == nil || *leadUserID != userID {
		return edition{}, nil, &EntryError{Code: "NOT_FOUND", Message: "entry not found", Status: http.StatusNotFound}
	}
	if withdrawnAt != nil {
		return edition{}, nil, &EntryError{Code: "ENTRY_WITHDRAWN", Message: "this entry has been withdrawn", Status: http.StatusConflict}
	}
	ed, err := scanEdition(tx.QueryRow(ctx, editionColumns+` WHERE id = $1`, editionID))
	if err != nil {
		return edition{}, nil, &EntryError{Code: "INTERNAL_ERROR", Message: "failed to read edition", Status: http.StatusInternalServerError}
	}
	return ed, withdrawnAt, nil
}

// UpdateMyEntry edits team details and guardian consent until close. The
// category and repository work are intentionally fixed: changing either would
// be a new entry with its own duplicate and cap checks.
func (h *Handler) UpdateMyEntry(w http.ResponseWriter, r *http.Request) {
	userID := actor(r.Context())
	if userID == "" {
		handler.WriteError(w, http.StatusUnauthorized, "UNAUTHORIZED", "authentication required")
		return
	}
	entryID := strings.TrimSpace(r.PathValue("entryId"))
	if !validUUID(entryID) {
		handler.WriteError(w, http.StatusNotFound, "NOT_FOUND", "entry not found")
		return
	}
	var req updateEntryRequest
	if !decodeBody(w, r, 64<<10, &req) {
		return
	}
	var v validationCollector
	var members []teamMemberView
	if len(req.TeamMembers) > 0 {
		var memberErr string
		members, memberErr = parseTeamMembers(req.TeamMembers)
		if memberErr != "" {
			v.add("teamMembers", memberErr)
		}
	}
	teamName := textPtr(req.TeamName)
	if teamName != nil && len(*teamName) > MaxTeamNameLength {
		v.add("teamName", fmt.Sprintf("must be at most %d characters", MaxTeamNameLength))
	}
	if len(req.TeamMembers) == 0 && req.TeamName == nil && req.GuardianConsent == nil {
		v.add("body", "no fields to update")
	}
	if v.write(w) {
		return
	}

	ctx := r.Context()
	tx, err := h.pool.Begin(ctx)
	if err != nil {
		handler.WriteServerError(w, r, "INTERNAL_ERROR", "failed to start entry transaction", err)
		return
	}
	defer tx.Rollback(ctx)

	ed, _, entryErr := h.loadEditableEntry(ctx, tx, entryID, userID)
	if entryErr != nil {
		if entryErr.Code == "INTERNAL_ERROR" {
			handler.WriteServerError(w, r, entryErr.Code, entryErr.Message, entryErr)
			return
		}
		handler.WriteError(w, entryErr.Status, entryErr.Code, entryErr.Message)
		return
	}
	if violation := CheckEntryWindow(ed.Status, ed.OpensAt, ed.ClosesAt, time.Now().UTC()); violation != nil {
		handler.WriteError(w, violation.Status, violation.Code, violation.Message)
		return
	}
	if req.GuardianConsent != nil {
		account, err := loadEntrant(ctx, tx, userID)
		if err != nil {
			handler.WriteServerError(w, r, "INTERNAL_ERROR", "failed to read entrant account", err)
			return
		}
		if required, known := RequiresGuardianConsent(account.AgeBand); known && required && !*req.GuardianConsent {
			handler.WriteError(w, http.StatusConflict, "GUARDIAN_CONSENT_REQUIRED", "a parent or guardian must give permission for entrants under 18")
			return
		}
	}

	sets := []string{"updated_at = now()"}
	args := []any{entryID}
	add := func(column string, value any) {
		args = append(args, value)
		sets = append(sets, column+" = $"+itoa(len(args)))
	}
	if req.TeamName != nil {
		add("team_name", teamName)
	}
	if len(req.TeamMembers) > 0 {
		add("team_members", teamMembersJSON(members))
	}
	if req.GuardianConsent != nil {
		add("guardian_consent", *req.GuardianConsent)
		if *req.GuardianConsent {
			sets = append(sets, "guardian_consent_at = now()")
		} else {
			sets = append(sets, "guardian_consent_at = NULL")
		}
	}
	if _, err := tx.Exec(ctx, "UPDATE competition_entries SET "+strings.Join(sets, ", ")+" WHERE id = $1", args...); err != nil {
		handler.WriteServerError(w, r, "INTERNAL_ERROR", "failed to update entry", err)
		return
	}
	updated, err := scanMyEntry(tx.QueryRow(ctx, myEntryColumns+` WHERE ce.id = $1`, entryID))
	if err != nil {
		handler.WriteServerError(w, r, "INTERNAL_ERROR", "failed to read entry", err)
		return
	}
	if err := tx.Commit(ctx); err != nil {
		handler.WriteServerError(w, r, "INTERNAL_ERROR", "failed to commit entry", err)
		return
	}
	writeJSON(w, http.StatusOK, updated)
}

// WithdrawMyEntry soft-withdraws the caller's entry until close. The row stays
// for audit, while the category and the work become available again.
func (h *Handler) WithdrawMyEntry(w http.ResponseWriter, r *http.Request) {
	userID := actor(r.Context())
	if userID == "" {
		handler.WriteError(w, http.StatusUnauthorized, "UNAUTHORIZED", "authentication required")
		return
	}
	entryID := strings.TrimSpace(r.PathValue("entryId"))
	if !validUUID(entryID) {
		handler.WriteError(w, http.StatusNotFound, "NOT_FOUND", "entry not found")
		return
	}
	ctx := r.Context()
	tx, err := h.pool.Begin(ctx)
	if err != nil {
		handler.WriteServerError(w, r, "INTERNAL_ERROR", "failed to start withdrawal transaction", err)
		return
	}
	defer tx.Rollback(ctx)
	ed, _, entryErr := h.loadEditableEntry(ctx, tx, entryID, userID)
	if entryErr != nil {
		if entryErr.Code == "INTERNAL_ERROR" {
			handler.WriteServerError(w, r, entryErr.Code, entryErr.Message, entryErr)
			return
		}
		handler.WriteError(w, entryErr.Status, entryErr.Code, entryErr.Message)
		return
	}
	if violation := CheckEntryWindow(ed.Status, ed.OpensAt, ed.ClosesAt, time.Now().UTC()); violation != nil {
		handler.WriteError(w, violation.Status, violation.Code, violation.Message)
		return
	}
	if _, err := tx.Exec(ctx, `
UPDATE competition_entries
SET withdrawn_at = now(), withdrawn_by = $2, updated_at = now()
WHERE id = $1 AND withdrawn_at IS NULL`, entryID, userID); err != nil {
		handler.WriteServerError(w, r, "INTERNAL_ERROR", "failed to withdraw entry", err)
		return
	}
	if err := tx.Commit(ctx); err != nil {
		handler.WriteServerError(w, r, "INTERNAL_ERROR", "failed to commit withdrawal", err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// MyEntries lists the caller's live self-entries for one edition. Withdrawn
// rows are omitted; the app drops the card on success.
func (h *Handler) MyEntries(w http.ResponseWriter, r *http.Request) {
	userID := actor(r.Context())
	if userID == "" {
		handler.WriteError(w, http.StatusUnauthorized, "UNAUTHORIZED", "authentication required")
		return
	}
	ed, err := h.getEdition(r.Context(), "slug = $1 AND status <> 'draft'", strings.TrimSpace(r.PathValue("slug")))
	if err != nil {
		writeEditionError(w, r, err)
		return
	}
	rows, err := h.pool.Query(r.Context(), myEntryColumns+`
WHERE ce.edition_id = $1 AND ce.lead_user_id = $2 AND ce.withdrawn_at IS NULL
ORDER BY ce.created_at DESC`, ed.ID, userID)
	if err != nil {
		handler.WriteServerError(w, r, "INTERNAL_ERROR", "failed to list entries", err)
		return
	}
	defer rows.Close()
	items := []myEntry{}
	for rows.Next() {
		entry, err := scanMyEntry(rows)
		if err != nil {
			handler.WriteServerError(w, r, "INTERNAL_ERROR", "failed to read entries", err)
			return
		}
		items = append(items, entry)
	}
	if err := rows.Err(); err != nil {
		handler.WriteServerError(w, r, "INTERNAL_ERROR", "failed to read entries", err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items})
}
