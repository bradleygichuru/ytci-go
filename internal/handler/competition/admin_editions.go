package competition

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgconn"

	"github.com/bradleygichuru/ytci-go/internal/handler"
)

// optionalDate distinguishes "field absent" from "field present and null".
type optionalDate struct {
	Set   bool
	Value *time.Time
}

func (o *optionalDate) UnmarshalJSON(raw []byte) error {
	o.Set = true
	if string(raw) == "null" {
		o.Value = nil
		return nil
	}
	var s string
	if err := json.Unmarshal(raw, &s); err != nil {
		return err
	}
	s = strings.TrimSpace(s)
	if s == "" {
		o.Value = nil
		return nil
	}
	t, err := time.Parse(time.RFC3339, s)
	if err != nil {
		return err
	}
	o.Value = &t
	return nil
}

type optionalInt struct {
	Set   bool
	Value *int
}

func (o *optionalInt) UnmarshalJSON(raw []byte) error {
	o.Set = true
	if string(raw) == "null" {
		o.Value = nil
		return nil
	}
	var n int
	if err := json.Unmarshal(raw, &n); err != nil {
		return err
	}
	o.Value = &n
	return nil
}

type editionRequest struct {
	Slug           *string      `json:"slug"`
	Name           *string      `json:"name"`
	Theme          *string      `json:"theme"`
	Status         *string      `json:"status"`
	PromotionType  *string      `json:"promotionType"`
	MinAge         optionalInt  `json:"minAge"`
	MaxAge         optionalInt  `json:"maxAge"`
	PublicVoteMode *string      `json:"publicVoteMode"`
	PrizeEnabled   *bool        `json:"prizeEnabled"`
	RulesVersion   *string      `json:"rulesVersion"`
	OpensAt        optionalDate `json:"opensAt"`
	ClosesAt       optionalDate `json:"closesAt"`
	VotingOpensAt  optionalDate `json:"votingOpensAt"`
	VotingClosesAt optionalDate `json:"votingClosesAt"`
	AnnouncementAt optionalDate `json:"announcementAt"`
}

func isUniqueViolation(err error) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == "23505"
}

// ListAdminEditions lists every edition, including drafts.
func (h *Handler) ListAdminEditions(w http.ResponseWriter, r *http.Request) {
	rows, err := h.pool.Query(r.Context(), editionColumns+` ORDER BY coalesce(opens_at, created_at) DESC`)
	if err != nil {
		handler.WriteServerError(w, r, "INTERNAL_ERROR", "failed to list editions", err)
		return
	}
	defer rows.Close()
	out := []edition{}
	for rows.Next() {
		e, err := scanEdition(rows)
		if err != nil {
			handler.WriteServerError(w, r, "INTERNAL_ERROR", "failed to read editions", err)
			return
		}
		out = append(out, e)
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": out})
}

func (h *Handler) GetAdminEdition(w http.ResponseWriter, r *http.Request) {
	ed, err := h.getEdition(r.Context(), "id = $1", strings.TrimSpace(r.PathValue("editionId")))
	if err != nil {
		writeEditionError(w, r, err)
		return
	}
	categories, err := h.listCategories(r.Context(), ed.ID)
	if err != nil {
		handler.WriteServerError(w, r, "INTERNAL_ERROR", "failed to read categories", err)
		return
	}
	writeJSON(w, http.StatusOK, editionDetail{edition: ed, Categories: categories})
}

// CreateEdition creates an edition with no implicit ages or dates. Missing
// optional configuration stays null; nothing assumes a legal band.
func (h *Handler) CreateEdition(w http.ResponseWriter, r *http.Request) {
	var req editionRequest
	if !decodeBody(w, r, 64<<10, &req) {
		return
	}
	var v validationCollector
	slug := ""
	if req.Slug == nil || !validateKey(normalizeKey(*req.Slug)) {
		v.add("slug", "is required and must contain lowercase letters, digits, - or _")
	} else {
		slug = normalizeKey(*req.Slug)
	}
	name := ""
	if req.Name == nil || strings.TrimSpace(*req.Name) == "" {
		v.add("name", "is required")
	} else {
		name = strings.TrimSpace(*req.Name)
	}
	rulesVersion := ""
	if req.RulesVersion == nil || strings.TrimSpace(*req.RulesVersion) == "" {
		v.add("rulesVersion", "is required")
	} else {
		rulesVersion = strings.TrimSpace(*req.RulesVersion)
	}
	status := "draft"
	if req.Status != nil {
		status = normalizeKey(*req.Status)
		if !editionStatuses[status] {
			v.add("status", "unknown status")
		}
	}
	promotion := PromotionAwardProgramme
	if req.PromotionType != nil {
		promotion = normalizeKey(*req.PromotionType)
		if !promotionTypes[promotion] {
			v.add("promotionType", "must be prize_competition, award_programme or licensed_operator")
		}
	}
	mode := VoteModePeopleChoiceOnly
	if req.PublicVoteMode != nil {
		mode = normalizeKey(*req.PublicVoteMode)
		if !voteModes[mode] {
			v.add("publicVoteMode", "must be weighted, people_choice_only or none")
		}
	}
	var minAge, maxAge *int
	if req.MinAge.Set {
		minAge = req.MinAge.Value
	}
	if req.MaxAge.Set {
		maxAge = req.MaxAge.Value
	}
	if err := ValidateAgeRange(minAge, maxAge); err != nil {
		v.add("ageRange", err.Error())
	}
	theme := textPtr(req.Theme)
	prize := false
	if req.PrizeEnabled != nil {
		prize = *req.PrizeEnabled
	}
	if v.write(w) {
		return
	}
	ed, err := scanEdition(h.pool.QueryRow(r.Context(), `
INSERT INTO competition_editions
(slug, name, theme, status, promotion_type, min_age, max_age, public_vote_mode,
 prize_enabled, rules_version, opens_at, closes_at, voting_opens_at,
 voting_closes_at, announcement_at)
VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15)
RETURNING id::text, slug, name, theme, status, promotion_type, min_age, max_age,
          public_vote_mode, prize_enabled, rules_version, opens_at, closes_at,
          voting_opens_at, voting_closes_at, announcement_at, created_at, updated_at`,
		slug, name, theme, status, promotion, minAge, maxAge, mode, prize,
		rulesVersion, req.OpensAt.Value, req.ClosesAt.Value, req.VotingOpensAt.Value,
		req.VotingClosesAt.Value, req.AnnouncementAt.Value))
	if err != nil {
		if isUniqueViolation(err) {
			handler.WriteError(w, http.StatusConflict, "SLUG_TAKEN", "an edition with that slug already exists")
			return
		}
		handler.WriteServerError(w, r, "INTERNAL_ERROR", "failed to create edition", err)
		return
	}
	writeJSON(w, http.StatusCreated, ed)
}

// UpdateEdition applies only the fields present in the body.
func (h *Handler) UpdateEdition(w http.ResponseWriter, r *http.Request) {
	id := strings.TrimSpace(r.PathValue("editionId"))
	if !validUUID(id) {
		handler.WriteError(w, http.StatusNotFound, "NOT_FOUND", "edition not found")
		return
	}
	var req editionRequest
	if !decodeBody(w, r, 64<<10, &req) {
		return
	}
	var v validationCollector
	sets := []string{}
	args := []any{id}
	add := func(column string, value any) {
		args = append(args, value)
		sets = append(sets, column+" = $"+itoa(len(args)))
	}
	if req.Slug != nil {
		slug := normalizeKey(*req.Slug)
		if !validateKey(slug) {
			v.add("slug", "must contain lowercase letters, digits, - or _")
		} else {
			add("slug", slug)
		}
	}
	if req.Name != nil {
		name := strings.TrimSpace(*req.Name)
		if name == "" {
			v.add("name", "must not be empty")
		} else {
			add("name", name)
		}
	}
	if req.Theme != nil {
		add("theme", textPtr(req.Theme))
	}
	if req.Status != nil {
		status := normalizeKey(*req.Status)
		if !editionStatuses[status] {
			v.add("status", "unknown status")
		} else {
			add("status", status)
		}
	}
	if req.PromotionType != nil {
		promotion := normalizeKey(*req.PromotionType)
		if !promotionTypes[promotion] {
			v.add("promotionType", "must be prize_competition, award_programme or licensed_operator")
		} else {
			add("promotion_type", promotion)
		}
	}
	if req.MinAge.Set {
		add("min_age", req.MinAge.Value)
	}
	if req.MaxAge.Set {
		add("max_age", req.MaxAge.Value)
	}
	if req.PublicVoteMode != nil {
		mode := normalizeKey(*req.PublicVoteMode)
		if !voteModes[mode] {
			v.add("publicVoteMode", "must be weighted, people_choice_only or none")
		} else {
			add("public_vote_mode", mode)
		}
	}
	if req.PrizeEnabled != nil {
		add("prize_enabled", *req.PrizeEnabled)
	}
	if req.RulesVersion != nil {
		version := strings.TrimSpace(*req.RulesVersion)
		if version == "" {
			v.add("rulesVersion", "must not be empty")
		} else {
			add("rules_version", version)
		}
	}
	if req.OpensAt.Set {
		add("opens_at", req.OpensAt.Value)
	}
	if req.ClosesAt.Set {
		add("closes_at", req.ClosesAt.Value)
	}
	if req.VotingOpensAt.Set {
		add("voting_opens_at", req.VotingOpensAt.Value)
	}
	if req.VotingClosesAt.Set {
		add("voting_closes_at", req.VotingClosesAt.Value)
	}
	if req.AnnouncementAt.Set {
		add("announcement_at", req.AnnouncementAt.Value)
	}
	if len(sets) == 0 {
		v.add("body", "no fields to update")
	}
	if v.write(w) {
		return
	}
	if req.MinAge.Set || req.MaxAge.Set {
		current, err := h.getEdition(r.Context(), "id = $1", id)
		if err != nil {
			writeEditionError(w, r, err)
			return
		}
		minAge, maxAge := current.MinAge, current.MaxAge
		if req.MinAge.Set {
			minAge = req.MinAge.Value
		}
		if req.MaxAge.Set {
			maxAge = req.MaxAge.Value
		}
		if err := ValidateAgeRange(minAge, maxAge); err != nil {
			handler.WriteError(w, http.StatusBadRequest, "INVALID_REQUEST", err.Error())
			return
		}
	}
	sets = append(sets, "updated_at = now()")
	query := "UPDATE competition_editions SET " + strings.Join(sets, ", ") + " WHERE id = $1"
	ed, err := scanEdition(h.pool.QueryRow(r.Context(), query+`
RETURNING id::text, slug, name, theme, status, promotion_type, min_age, max_age,
          public_vote_mode, prize_enabled, rules_version, opens_at, closes_at,
          voting_opens_at, voting_closes_at, announcement_at, created_at, updated_at`, args...))
	if err != nil {
		if isNoRows(err) {
			handler.WriteError(w, http.StatusNotFound, "NOT_FOUND", "edition not found")
			return
		}
		if isUniqueViolation(err) {
			handler.WriteError(w, http.StatusConflict, "SLUG_TAKEN", "an edition with that slug already exists")
			return
		}
		handler.WriteServerError(w, r, "INTERNAL_ERROR", "failed to update edition", err)
		return
	}
	writeJSON(w, http.StatusOK, ed)
}

func (h *Handler) DeleteEdition(w http.ResponseWriter, r *http.Request) {
	id := strings.TrimSpace(r.PathValue("editionId"))
	var votes int
	if err := h.pool.QueryRow(r.Context(), `SELECT count(*)::int FROM competition_votes WHERE edition_id = $1`, id).Scan(&votes); err != nil {
		handler.WriteServerError(w, r, "INTERNAL_ERROR", "failed to check vote history", err)
		return
	}
	if votes > 0 {
		handler.WriteError(w, http.StatusConflict, "VOTE_HISTORY_EXISTS", "an edition with vote history cannot be deleted")
		return
	}
	if _, err := h.pool.Exec(r.Context(), `DELETE FROM competition_editions WHERE id = $1`, id); err != nil {
		handler.WriteServerError(w, r, "INTERNAL_ERROR", "failed to delete edition", err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// ---------------------------------------------------------------------------
// Categories
// ---------------------------------------------------------------------------

type categoryRequest struct {
	Key         *string `json:"key"`
	Label       *string `json:"label"`
	Description *string `json:"description"`
	SortOrder   *int    `json:"sortOrder"`
}

func (h *Handler) ListAdminCategories(w http.ResponseWriter, r *http.Request) {
	editionID := strings.TrimSpace(r.PathValue("editionId"))
	if !validUUID(editionID) {
		handler.WriteError(w, http.StatusNotFound, "NOT_FOUND", "edition not found")
		return
	}
	categories, err := h.listCategories(r.Context(), editionID)
	if err != nil {
		handler.WriteServerError(w, r, "INTERNAL_ERROR", "failed to read categories", err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": categories})
}

func (h *Handler) CreateCategory(w http.ResponseWriter, r *http.Request) {
	editionID := strings.TrimSpace(r.PathValue("editionId"))
	if !validUUID(editionID) || !h.editionExists(r, editionID) {
		handler.WriteError(w, http.StatusNotFound, "NOT_FOUND", "edition not found")
		return
	}
	var req categoryRequest
	if !decodeBody(w, r, 32<<10, &req) {
		return
	}
	var v validationCollector
	key := ""
	if req.Key == nil || !validateKey(normalizeKey(*req.Key)) {
		v.add("key", "is required and must contain lowercase letters, digits, - or _")
	} else {
		key = normalizeKey(*req.Key)
	}
	label := ""
	if req.Label == nil || strings.TrimSpace(*req.Label) == "" {
		v.add("label", "is required")
	} else {
		label = strings.TrimSpace(*req.Label)
	}
	sortOrder := 0
	if req.SortOrder != nil {
		sortOrder = *req.SortOrder
	}
	if v.write(w) {
		return
	}
	c, err := scanCategory(h.pool.QueryRow(r.Context(), `
INSERT INTO competition_categories (edition_id, key, label, description, sort_order)
VALUES ($1,$2,$3,$4,$5)
RETURNING id::text, edition_id::text, key, label, description, sort_order`,
		editionID, key, label, textPtr(req.Description), sortOrder))
	if err != nil {
		if isUniqueViolation(err) {
			handler.WriteError(w, http.StatusConflict, "CATEGORY_EXISTS", "that category key already exists for the edition")
			return
		}
		handler.WriteServerError(w, r, "INTERNAL_ERROR", "failed to create category", err)
		return
	}
	writeJSON(w, http.StatusCreated, c)
}

func (h *Handler) UpdateCategory(w http.ResponseWriter, r *http.Request) {
	id := strings.TrimSpace(r.PathValue("categoryId"))
	if !validUUID(id) {
		handler.WriteError(w, http.StatusNotFound, "NOT_FOUND", "category not found")
		return
	}
	var req categoryRequest
	if !decodeBody(w, r, 32<<10, &req) {
		return
	}
	var v validationCollector
	sets := []string{}
	args := []any{id}
	add := func(col string, val any) {
		args = append(args, val)
		sets = append(sets, col+" = $"+itoa(len(args)))
	}
	if req.Key != nil {
		key := normalizeKey(*req.Key)
		if !validateKey(key) {
			v.add("key", "must contain lowercase letters, digits, - or _")
		} else {
			add("key", key)
		}
	}
	if req.Label != nil {
		label := strings.TrimSpace(*req.Label)
		if label == "" {
			v.add("label", "must not be empty")
		} else {
			add("label", label)
		}
	}
	if req.Description != nil {
		add("description", textPtr(req.Description))
	}
	if req.SortOrder != nil {
		add("sort_order", *req.SortOrder)
	}
	if len(sets) == 0 {
		v.add("body", "no fields to update")
	}
	if v.write(w) {
		return
	}
	c, err := scanCategory(h.pool.QueryRow(r.Context(),
		"UPDATE competition_categories SET "+strings.Join(sets, ", ")+` WHERE id = $1
RETURNING id::text, edition_id::text, key, label, description, sort_order`, args...))
	if err != nil {
		if isNoRows(err) {
			handler.WriteError(w, http.StatusNotFound, "NOT_FOUND", "category not found")
			return
		}
		if isUniqueViolation(err) {
			handler.WriteError(w, http.StatusConflict, "CATEGORY_EXISTS", "that category key already exists for the edition")
			return
		}
		handler.WriteServerError(w, r, "INTERNAL_ERROR", "failed to update category", err)
		return
	}
	writeJSON(w, http.StatusOK, c)
}

func (h *Handler) DeleteCategory(w http.ResponseWriter, r *http.Request) {
	id := strings.TrimSpace(r.PathValue("categoryId"))
	var votes int
	if err := h.pool.QueryRow(r.Context(), `SELECT count(*)::int FROM competition_votes WHERE category_id = $1`, id).Scan(&votes); err != nil {
		handler.WriteServerError(w, r, "INTERNAL_ERROR", "failed to check vote history", err)
		return
	}
	if votes > 0 {
		handler.WriteError(w, http.StatusConflict, "VOTE_HISTORY_EXISTS", "a category with vote history cannot be deleted")
		return
	}
	if _, err := h.pool.Exec(r.Context(), `DELETE FROM competition_categories WHERE id = $1`, id); err != nil {
		handler.WriteServerError(w, r, "INTERNAL_ERROR", "failed to delete category", err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// ---------------------------------------------------------------------------
// Entries
// ---------------------------------------------------------------------------

type entryRequest struct {
	RepositoryEntryID string          `json:"repositoryEntryId"`
	CategoryID        string          `json:"categoryId"`
	LeadUserID        *string         `json:"leadUserId"`
	TeamName          *string         `json:"teamName"`
	TeamMembers       json.RawMessage `json:"teamMembers"`
	EligibilityStatus *string         `json:"eligibilityStatus"`
	EligibilityNote   *string         `json:"eligibilityNote"`
}

func (h *Handler) ListAdminEntries(w http.ResponseWriter, r *http.Request) {
	editionID := strings.TrimSpace(r.PathValue("editionId"))
	if !validUUID(editionID) {
		handler.WriteError(w, http.StatusNotFound, "NOT_FOUND", "edition not found")
		return
	}
	categoryKey := strings.TrimSpace(r.URL.Query().Get("category"))
	status := strings.TrimSpace(r.URL.Query().Get("eligibility"))
	rows, err := h.pool.Query(r.Context(), entryAdminColumns+`
WHERE ce.edition_id = $1
  AND ($2::text = '' OR cc.key = $2)
  AND ($3::text = '' OR ce.eligibility_status = $3)
ORDER BY cc.sort_order, ce.created_at`, editionID, categoryKey, status)
	if err != nil {
		handler.WriteServerError(w, r, "INTERNAL_ERROR", "failed to list entries", err)
		return
	}
	defer rows.Close()
	out := []entryAdmin{}
	for rows.Next() {
		e, err := scanEntryAdmin(rows)
		if err != nil {
			handler.WriteServerError(w, r, "INTERNAL_ERROR", "failed to read entries", err)
			return
		}
		out = append(out, e)
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": out})
}

func (h *Handler) GetAdminEntry(w http.ResponseWriter, r *http.Request) {
	id := strings.TrimSpace(r.PathValue("entryId"))
	if !validUUID(id) {
		handler.WriteError(w, http.StatusNotFound, "NOT_FOUND", "entry not found")
		return
	}
	e, err := h.getEntryAdmin(r.Context(), id)
	if err != nil {
		if isNoRows(err) {
			handler.WriteError(w, http.StatusNotFound, "NOT_FOUND", "entry not found")
			return
		}
		handler.WriteServerError(w, r, "INTERNAL_ERROR", "failed to read entry", err)
		return
	}
	writeJSON(w, http.StatusOK, e)
}

func (h *Handler) CreateEntry(w http.ResponseWriter, r *http.Request) {
	editionID := strings.TrimSpace(r.PathValue("editionId"))
	if !validUUID(editionID) || !h.editionExists(r, editionID) {
		handler.WriteError(w, http.StatusNotFound, "NOT_FOUND", "edition not found")
		return
	}
	var req entryRequest
	if !decodeBody(w, r, 128<<10, &req) {
		return
	}
	var v validationCollector
	if !validUUID(req.RepositoryEntryID) {
		v.add("repositoryEntryId", "must be a UUID")
	}
	if !validUUID(req.CategoryID) {
		v.add("categoryId", "must be a UUID")
	}
	status := "pending"
	if req.EligibilityStatus != nil {
		status = normalizeKey(*req.EligibilityStatus)
		if !eligibilityStatuses[status] {
			v.add("eligibilityStatus", "must be pending, eligible or ineligible")
		}
	}
	if status == "ineligible" && (req.EligibilityNote == nil || strings.TrimSpace(*req.EligibilityNote) == "") {
		v.add("eligibilityNote", "is required when an entry is ineligible")
	}
	leadUserID := textPtr(req.LeadUserID)
	if leadUserID != nil && !h.userExists(r, *leadUserID) {
		v.add("leadUserId", "does not match a user")
	}
	if v.write(w) {
		return
	}
	var categoryEdition string
	if err := h.pool.QueryRow(r.Context(), `SELECT edition_id::text FROM competition_categories WHERE id = $1`, req.CategoryID).Scan(&categoryEdition); err != nil {
		handler.WriteError(w, http.StatusBadRequest, "INVALID_CATEGORY", "category does not exist")
		return
	}
	if categoryEdition != editionID {
		handler.WriteError(w, http.StatusBadRequest, "INVALID_CATEGORY", "category belongs to another edition")
		return
	}
	var repositoryExists bool
	if err := h.pool.QueryRow(r.Context(), `SELECT EXISTS (SELECT 1 FROM repository_entries WHERE id = $1)`, req.RepositoryEntryID).Scan(&repositoryExists); err != nil || !repositoryExists {
		handler.WriteError(w, http.StatusBadRequest, "INVALID_ENTRY", "repository entry does not exist")
		return
	}
	if leadUserID != nil {
		count := 0
		if err := h.pool.QueryRow(r.Context(), `
SELECT count(DISTINCT category_id) FROM competition_entries
WHERE edition_id = $1 AND lead_user_id = $2 AND category_id <> $3`,
			editionID, *leadUserID, req.CategoryID).Scan(&count); err != nil {
			handler.WriteServerError(w, r, "INTERNAL_ERROR", "failed to check category limit", err)
			return
		}
		if count >= MaxCategoriesPerEntrant {
			handler.WriteError(w, http.StatusConflict, "CATEGORY_LIMIT", "an entrant can enter at most two categories")
			return
		}
	}
	created, err := h.insertEntry(r.Context(), editionID, req, status)
	if err != nil {
		if isUniqueViolation(err) {
			handler.WriteError(w, http.StatusConflict, "ENTRY_EXISTS", "this work is already entered in that category")
			return
		}
		handler.WriteServerError(w, r, "INTERNAL_ERROR", "failed to create entry", err)
		return
	}
	writeJSON(w, http.StatusCreated, created)
}

func (h *Handler) insertEntry(ctx context.Context, editionID string, req entryRequest, status string) (entryAdmin, error) {
	var id string
	var teamMembers any
	if len(req.TeamMembers) > 0 {
		teamMembers = string(req.TeamMembers)
	}
	err := h.pool.QueryRow(ctx, `
INSERT INTO competition_entries
(edition_id, category_id, repository_entry_id, lead_user_id, team_name,
 team_members, eligibility_status, eligibility_note)
VALUES ($1,$2,$3,$4,$5,$6,$7,$8)
RETURNING id::text`,
		editionID, req.CategoryID, req.RepositoryEntryID, textPtr(req.LeadUserID),
		textPtr(req.TeamName), teamMembers, status, textPtr(req.EligibilityNote)).Scan(&id)
	if err != nil {
		return entryAdmin{}, err
	}
	return h.getEntryAdmin(ctx, id)
}

func (h *Handler) UpdateEntry(w http.ResponseWriter, r *http.Request) {
	id := strings.TrimSpace(r.PathValue("entryId"))
	if !validUUID(id) {
		handler.WriteError(w, http.StatusNotFound, "NOT_FOUND", "entry not found")
		return
	}
	var req entryRequest
	if !decodeBody(w, r, 128<<10, &req) {
		return
	}
	var v validationCollector
	sets := []string{}
	args := []any{id}
	add := func(col string, val any) {
		args = append(args, val)
		sets = append(sets, col+" = $"+itoa(len(args)))
	}
	if req.LeadUserID != nil {
		lead := textPtr(req.LeadUserID)
		if lead != nil && !h.userExists(r, *lead) {
			v.add("leadUserId", "does not match a user")
		} else {
			add("lead_user_id", lead)
		}
	}
	if req.TeamName != nil {
		add("team_name", textPtr(req.TeamName))
	}
	if len(req.TeamMembers) > 0 {
		add("team_members", string(req.TeamMembers))
	}
	if req.EligibilityStatus != nil {
		status := normalizeKey(*req.EligibilityStatus)
		if !eligibilityStatuses[status] {
			v.add("eligibilityStatus", "must be pending, eligible or ineligible")
		} else {
			if status == "ineligible" && req.EligibilityNote == nil {
				v.add("eligibilityNote", "is required when an entry is ineligible")
			}
			add("eligibility_status", status)
		}
	}
	if req.EligibilityNote != nil {
		add("eligibility_note", textPtr(req.EligibilityNote))
	}
	if len(sets) == 0 {
		v.add("body", "no fields to update")
	}
	if v.write(w) {
		return
	}
	sets = append(sets, "updated_at = now()")
	if _, err := h.pool.Exec(r.Context(),
		"UPDATE competition_entries SET "+strings.Join(sets, ", ")+" WHERE id = $1", args...); err != nil {
		handler.WriteServerError(w, r, "INTERNAL_ERROR", "failed to update entry", err)
		return
	}
	e, err := h.getEntryAdmin(r.Context(), id)
	if err != nil {
		handler.WriteServerError(w, r, "INTERNAL_ERROR", "failed to read entry", err)
		return
	}
	writeJSON(w, http.StatusOK, e)
}

type eligibilityRequest struct {
	Status string  `json:"status"`
	Note   *string `json:"note"`
}

func (h *Handler) SetEligibility(w http.ResponseWriter, r *http.Request) {
	id := strings.TrimSpace(r.PathValue("entryId"))
	if !validUUID(id) {
		handler.WriteError(w, http.StatusNotFound, "NOT_FOUND", "entry not found")
		return
	}
	var req eligibilityRequest
	if !decodeBody(w, r, 16<<10, &req) {
		return
	}
	status := normalizeKey(req.Status)
	var v validationCollector
	if !eligibilityStatuses[status] {
		v.add("status", "must be pending, eligible or ineligible")
	}
	if status == "ineligible" && (req.Note == nil || strings.TrimSpace(*req.Note) == "") {
		v.add("note", "is required when an entry is ineligible")
	}
	if v.write(w) {
		return
	}
	if _, err := h.pool.Exec(r.Context(), `
UPDATE competition_entries
SET eligibility_status = $2, eligibility_note = $3, updated_at = now()
WHERE id = $1`, id, status, textPtr(req.Note)); err != nil {
		handler.WriteServerError(w, r, "INTERNAL_ERROR", "failed to update eligibility", err)
		return
	}
	e, err := h.getEntryAdmin(r.Context(), id)
	if err != nil {
		handler.WriteServerError(w, r, "INTERNAL_ERROR", "failed to read entry", err)
		return
	}
	writeJSON(w, http.StatusOK, e)
}

func (h *Handler) DeleteEntry(w http.ResponseWriter, r *http.Request) {
	id := strings.TrimSpace(r.PathValue("entryId"))
	var votes int
	if err := h.pool.QueryRow(r.Context(), `SELECT count(*)::int FROM competition_votes WHERE entry_id = $1`, id).Scan(&votes); err != nil {
		handler.WriteServerError(w, r, "INTERNAL_ERROR", "failed to check vote history", err)
		return
	}
	if votes > 0 {
		handler.WriteError(w, http.StatusConflict, "VOTE_HISTORY_EXISTS", "an entry with vote history cannot be deleted")
		return
	}
	if _, err := h.pool.Exec(r.Context(), `DELETE FROM competition_entries WHERE id = $1`, id); err != nil {
		handler.WriteServerError(w, r, "INTERNAL_ERROR", "failed to delete entry", err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// ---------------------------------------------------------------------------
// Consultation responses
// ---------------------------------------------------------------------------

func (h *Handler) ListConsultationResponses(w http.ResponseWriter, r *http.Request) {
	editionID := strings.TrimSpace(r.PathValue("editionId"))
	if !validUUID(editionID) {
		handler.WriteError(w, http.StatusNotFound, "NOT_FOUND", "edition not found")
		return
	}
	limit := parseLimit(r.URL.Query().Get("limit"), 50, 200)
	offset := parseOffset(r.URL.Query().Get("offset"))
	rows, err := h.pool.Query(r.Context(), `
SELECT id::text, edition_id::text, name, email, answers, consent, created_at
FROM competition_consultation_responses
WHERE edition_id = $1
ORDER BY created_at DESC
LIMIT $2 OFFSET $3`, editionID, limit, offset)
	if err != nil {
		handler.WriteServerError(w, r, "INTERNAL_ERROR", "failed to list consultation responses", err)
		return
	}
	defer rows.Close()
	out := []consultationResponse{}
	for rows.Next() {
		var c consultationResponse
		var answers []byte
		if err := rows.Scan(&c.ID, &c.EditionID, &c.Name, &c.Email, &answers, &c.Consent, &c.CreatedAt); err != nil {
			handler.WriteServerError(w, r, "INTERNAL_ERROR", "failed to read consultation responses", err)
			return
		}
		c.Answers = decodeJSONB(answers)
		out = append(out, c)
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": out})
}

func (h *Handler) ExportConsultationResponses(w http.ResponseWriter, r *http.Request) {
	editionID := strings.TrimSpace(r.PathValue("editionId"))
	if !validUUID(editionID) {
		handler.WriteError(w, http.StatusNotFound, "NOT_FOUND", "edition not found")
		return
	}
	rows, err := h.pool.Query(r.Context(), `
SELECT id::text, coalesce(name, ''), coalesce(email, ''), answers, consent, created_at
FROM competition_consultation_responses
WHERE edition_id = $1
ORDER BY created_at`, editionID)
	if err != nil {
		handler.WriteServerError(w, r, "INTERNAL_ERROR", "failed to export consultation responses", err)
		return
	}
	defer rows.Close()
	w.Header().Set("Content-Type", "text/csv; charset=utf-8")
	w.Header().Set("Content-Disposition", `attachment; filename="consultation-responses.csv"`)
	_, _ = w.Write([]byte("id,name,email,consent,created_at,answers\n"))
	for rows.Next() {
		var id, name, email string
		var answers []byte
		var consent bool
		var createdAt time.Time
		if err := rows.Scan(&id, &name, &email, &answers, &consent, &createdAt); err != nil {
			return
		}
		line := strings.Join([]string{
			id, csvCell(name), csvCell(email), boolCell(consent),
			createdAt.UTC().Format(time.RFC3339), csvCell(string(answers)),
		}, ",") + "\n"
		_, _ = w.Write([]byte(line))
	}
}

// ---------------------------------------------------------------------------
// Small helpers
// ---------------------------------------------------------------------------

func (h *Handler) editionExists(r *http.Request, id string) bool {
	var exists bool
	if err := h.pool.QueryRow(r.Context(), `SELECT EXISTS (SELECT 1 FROM competition_editions WHERE id = $1)`, id).Scan(&exists); err != nil {
		return false
	}
	return exists
}

func (h *Handler) userExists(r *http.Request, id string) bool {
	var exists bool
	if err := h.pool.QueryRow(r.Context(), `SELECT EXISTS (SELECT 1 FROM users WHERE id = $1)`, id).Scan(&exists); err != nil {
		return false
	}
	return exists
}

func csvCell(raw string) string {
	return `"` + strings.ReplaceAll(raw, `"`, `""`) + `"`
}

func boolCell(v bool) string {
	if v {
		return "true"
	}
	return "false"
}

func itoa(n int) string {
	return strconv.Itoa(n)
}
