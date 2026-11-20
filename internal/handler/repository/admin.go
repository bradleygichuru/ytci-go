package repository

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/bradleygichuru/ytci-go/internal/handler"
	"github.com/bradleygichuru/ytci-go/internal/middleware"
	"github.com/bradleygichuru/ytci-go/internal/pagination"
)

var (
	repoAdminRoles = map[middleware.Role]bool{
		middleware.RoleSuperAdmin: true,
		middleware.RoleAdmin:      true,
		middleware.RoleModerator:  true,
	}
	statuses = map[string]bool{
		"draft": true, "submitted": true, "approved": true,
		"published": true, "rejected": true, "archived": true,
	}
	slugPattern = regexp.MustCompile(`^[a-z0-9]+(?:-[a-z0-9]+)*$`)
)

// AdminGate narrows the platform AdminGate to the roles the repository
// contract allows: super_admin, administrator and moderator.
func AdminGate(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if repoAdminRoles[middleware.RoleFromCtx(r.Context())] {
			next.ServeHTTP(w, r)
			return
		}
		handler.WriteError(w, http.StatusForbidden, "FORBIDDEN", "repository admin access required")
	})
}

func isModerator(ctx context.Context) bool {
	return middleware.RoleFromCtx(ctx) == middleware.RoleModerator
}

func actor(ctx context.Context) string {
	return middleware.UserID(ctx)
}

// ---------------------------------------------------------------------------
// List
// ---------------------------------------------------------------------------

func (h *Handler) ListAdmin(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()

	status := strings.TrimSpace(q.Get("status"))
	if status != "" && !statuses[status] {
		handler.WriteError(w, http.StatusBadRequest, "INVALID_REQUEST", "unknown status")
		return
	}
	kinds, err := parseKinds(q["kind"])
	if err != nil {
		handler.WriteError(w, http.StatusBadRequest, "INVALID_REQUEST", err.Error())
		return
	}
	source := strings.TrimSpace(q.Get("source"))
	if source != "" && source != "admin" && source != "public" {
		handler.WriteError(w, http.StatusBadRequest, "INVALID_REQUEST", "source must be admin or public")
		return
	}
	featuredOnly := false
	switch strings.ToLower(strings.TrimSpace(q.Get("featured"))) {
	case "", "false", "0":
	case "true", "1":
		featuredOnly = true
	default:
		handler.WriteError(w, http.StatusBadRequest, "INVALID_REQUEST", "featured must be true or false")
		return
	}

	orderCol := "e.updated_at"
	switch q.Get("sort") {
	case "", "updatedAt":
	case "publishedAt":
		orderCol = "e.published_at"
	default:
		handler.WriteError(w, http.StatusBadRequest, "INVALID_REQUEST", "sort must be updatedAt or publishedAt")
		return
	}

	cursor, err := parseCursor(q.Get("cursor"))
	if err != nil {
		handler.WriteError(w, http.StatusBadRequest, "INVALID_REQUEST", err.Error())
		return
	}
	limit := parseLimit(q.Get("limit"))

	var cursorTime *time.Time
	var cursorID *string
	if cursor != nil {
		t, err := time.Parse(time.RFC3339Nano, cursor.SortValue)
		if err != nil {
			handler.WriteError(w, http.StatusBadRequest, "INVALID_REQUEST", "invalid cursor")
			return
		}
		cursorTime = &t
		cursorID = &cursor.ID
	}

	rows, err := h.pool.Query(r.Context(), entryColumns+fmt.Sprintf(`
WHERE ($1::text = '' OR e.status = $1)
  AND (coalesce(cardinality($2::text[]), 0) = 0 OR e.kind = ANY($2::text[]))
  AND ($3::text = '' OR e.county = $3)
  AND ($4::text = '' OR e.category = $4)
  AND ($5::text = '' OR e.source = $5)
  AND ($6::boolean = false OR e.featured = true)
  AND (
    $7::text = ''
    OR (length(btrim($7)) >= 3 AND e.search_vector @@ websearch_to_tsquery('english', $7))
    OR position(lower(btrim($7)) in lower(e.title)) > 0
    OR position(lower(btrim($7)) in lower(e.summary)) > 0
    OR position(lower(btrim($7)) in lower(coalesce(e.reference_code, ''))) > 0
  )
  AND ($8::timestamp IS NULL OR (%s, e.id) < ($8::timestamp, $9::uuid))
ORDER BY %s DESC NULLS LAST, e.id DESC
LIMIT $10`, orderCol, orderCol),
		status, kinds, strings.TrimSpace(q.Get("county")), strings.TrimSpace(q.Get("category")),
		source, featuredOnly, strings.TrimSpace(q.Get("q")), cursorTime, cursorID, limit+1)
	if err != nil {
		handler.WriteServerError(w, r, "INTERNAL_ERROR", "failed to list repository entries", err)
		return
	}
	defer rows.Close()

	var entries []entryRecord
	for rows.Next() {
		e, err := scanEntry(rows)
		if err != nil {
			handler.WriteServerError(w, r, "INTERNAL_ERROR", "failed to read repository entries", err)
			return
		}
		entries = append(entries, e)
	}
	if err := rows.Err(); err != nil {
		handler.WriteServerError(w, r, "INTERNAL_ERROR", "failed to read repository entries", err)
		return
	}

	hasMore := len(entries) > limit
	if hasMore {
		entries = entries[:limit]
	}
	items := h.adminDTOs(r.Context(), entries)

	resp := adminListResponse{Items: items, HasMore: hasMore}
	if hasMore && len(entries) > 0 {
		last := entries[len(entries)-1]
		var sortValue time.Time
		if orderCol == "e.published_at" {
			if last.PublishedAt != nil {
				sortValue = *last.PublishedAt
			}
		} else if last.UpdatedAt != nil {
			sortValue = *last.UpdatedAt
		}
		if !sortValue.IsZero() {
			cur := pagination.EncodeCursor(sortValue.UTC().Format(time.RFC3339Nano), last.ID)
			resp.NextCursor = &cur
		}
	}

	writeJSON(w, http.StatusOK, resp)
}

// ---------------------------------------------------------------------------
// Read
// ---------------------------------------------------------------------------

func (h *Handler) GetAdmin(w http.ResponseWriter, r *http.Request) {
	e, ok := h.entryByPathID(w, r)
	if !ok {
		return
	}
	writeJSON(w, http.StatusOK, h.adminDTO(r.Context(), e))
}

func (h *Handler) entryByPathID(w http.ResponseWriter, r *http.Request) (entryRecord, bool) {
	id := strings.TrimSpace(r.PathValue("id"))
	if _, err := uuid.Parse(id); err != nil {
		handler.WriteError(w, http.StatusNotFound, "NOT_FOUND", "entry not found")
		return entryRecord{}, false
	}
	e, err := h.getEntry(r.Context(), "e.id = $1", id)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			handler.WriteError(w, http.StatusNotFound, "NOT_FOUND", "entry not found")
			return entryRecord{}, false
		}
		handler.WriteServerError(w, r, "INTERNAL_ERROR", "failed to read repository entry", err)
		return entryRecord{}, false
	}
	return e, true
}

// ---------------------------------------------------------------------------
// Create
// ---------------------------------------------------------------------------

func (h *Handler) CreateAdmin(w http.ResponseWriter, r *http.Request) {
	if isModerator(r.Context()) {
		handler.WriteError(w, http.StatusForbidden, "FORBIDDEN", "moderators cannot create entries")
		return
	}
	var req adminCreateRequest
	if !decodeBody(w, r, 1024*1024, &req) {
		return
	}

	var v validationCollector
	kind := strings.ToLower(strings.TrimSpace(req.Kind))
	if !knownKind(kind) {
		v.add("kind", "kind is not recognised")
	}
	status := strings.TrimSpace(req.Status)
	if status == "" {
		status = "draft"
	}
	if status != "draft" && status != "published" {
		v.add("status", "status must be draft or published")
	}
	title := strings.TrimSpace(req.Title)
	if title == "" {
		v.add("title", "title is required")
	} else if len([]rune(title)) > 140 {
		v.add("title", "title must be 140 characters or fewer")
	}
	summary := strings.TrimSpace(req.Summary)
	if summary == "" {
		v.add("summary", "summary is required")
	} else if len([]rune(summary)) > 320 {
		v.add("summary", "summary must be 320 characters or fewer")
	}
	body := strings.TrimSpace(req.BodyMD)
	if len([]rune(body)) > 40000 {
		v.add("bodyMd", "bodyMd must be 40,000 characters or fewer")
	}
	if status == "published" && body == "" {
		v.add("bodyMd", "bodyMd is required before publishing")
	}
	tags, ok := normalizeTags(req.Tags)
	if !ok {
		v.add("tags", "tags must contain at most 10 values of 40 characters or fewer")
	}
	team, ok := normalizeTeam(req.TeamMembers)
	if !ok {
		v.add("teamMembers", "teamMembers must contain at most 10 named people")
	}
	externalURL := strings.TrimSpace(req.ExternalURL)
	if externalURL != "" && !validHTTPURL(externalURL) {
		v.add("externalUrl", "externalUrl must be an http(s) URL")
	}
	embedURL := strings.TrimSpace(req.EmbedURL)
	embedName := ""
	if embedURL != "" {
		var embedOK bool
		embedName, embedOK = embedProvider(embedURL)
		if !embedOK {
			handler.WriteError(w, http.StatusBadRequest, "INVALID_EMBED_PROVIDER", "embed hosts must be YouTube, Vimeo or Matterport")
			return
		}
	}

	slug := strings.TrimSpace(req.Slug)
	if slug == "" {
		slug = slugify(title)
	} else if !slugPattern.MatchString(slug) || len(slug) > 100 {
		v.add("slug", "slug must be lowercase words separated by hyphens")
	}

	county := strings.TrimSpace(req.County)
	category := strings.TrimSpace(req.Category)
	if status == "published" {
		if category == "" {
			v.add("category", "category is required before publishing")
		}
		if req.CoverMediaID == nil && externalURL == "" && embedURL == "" {
			v.add("coverMediaId", "published entries need cover media, an external URL or an embed")
		}
	}
	if v.empty() {
		if county != "" {
			canonical, err := h.canonicalCounty(r.Context(), county)
			if err != nil {
				handler.WriteServerError(w, r, "INTERNAL_ERROR", "failed to validate county", err)
				return
			}
			if canonical == "" {
				v.add("county", "county must match a Kenyan county")
			} else {
				county = canonical
			}
		}
		if category != "" {
			canonical, err := h.canonicalCategory(r.Context(), kind, category)
			if err != nil {
				handler.WriteServerError(w, r, "INTERNAL_ERROR", "failed to validate category", err)
				return
			}
			if canonical == "" {
				v.add("category", "category is not valid for this kind")
			} else {
				category = canonical
			}
		}
	}

	galleryIDs := dedupeStrings(req.GalleryMediaIDs)
	if len(galleryIDs) > maxGalleryImages {
		v.add("galleryMediaIds", "at most 5 gallery images are allowed")
	}
	if req.CoverMediaID != nil && !h.mediaExists(r.Context(), *req.CoverMediaID) {
		v.add("coverMediaId", "media asset not found")
	}
	if req.DocumentMediaID != nil && !h.mediaExists(r.Context(), *req.DocumentMediaID) {
		v.add("documentMediaId", "media asset not found")
	}
	for _, id := range galleryIDs {
		if !h.mediaExists(r.Context(), id) {
			v.add("galleryMediaIds", "media asset not found")
			break
		}
	}
	if v.write(w) {
		return
	}

	teamJSON, err := json.Marshal(team)
	if err != nil {
		handler.WriteServerError(w, r, "INTERNAL_ERROR", "failed to encode team", err)
		return
	}

	ctx := r.Context()
	tx, err := h.pool.Begin(ctx)
	if err != nil {
		handler.WriteServerError(w, r, "INTERNAL_ERROR", "failed to start entry", err)
		return
	}
	defer tx.Rollback(ctx)

	var id string
	var publishedAt *time.Time
	if status == "published" {
		now := time.Now()
		publishedAt = &now
	}
	for attempt := 0; attempt < 8; attempt++ {
		candidate := slug
		if attempt > 0 {
			candidate = slug + "-" + strings.ToLower(randomCode(4))
		}
		err = tx.QueryRow(ctx, `
INSERT INTO repository_entries
(kind, slug, title, summary, body_md, county, category, tags,
 author_name, author_org, team_members, competition_name, competition_category,
 cover_media_id, document_media_id, external_url, embed_url, embed_provider,
 status, source, featured, published_at)
VALUES ($1, $2, $3, $4, $5, NULLIF($6, ''), NULLIF($7, ''), $8,
        NULLIF($9, ''), NULLIF($10, ''), NULLIF($11::jsonb, 'null'::jsonb), NULLIF($12, ''), NULLIF($13, ''),
        $14, $15, NULLIF($16, ''), NULLIF($17, ''), NULLIF($18, ''),
        $19, 'admin', $20, $21)
ON CONFLICT DO NOTHING
RETURNING id::text`,
			kind, candidate, title, summary, body, county, category, tags,
			strings.TrimSpace(req.AuthorName), strings.TrimSpace(req.AuthorOrg), string(teamJSON),
			strings.TrimSpace(req.CompetitionName), strings.TrimSpace(req.CompetitionCategory),
			req.CoverMediaID, req.DocumentMediaID, externalURL, embedURL, embedName,
			status, req.Featured, publishedAt).
			Scan(&id)
		if err == nil {
			break
		}
		if !errors.Is(err, pgx.ErrNoRows) {
			handler.WriteServerError(w, r, "INTERNAL_ERROR", "failed to create entry", err)
			return
		}
	}
	if id == "" {
		handler.WriteError(w, http.StatusConflict, "SLUG_CONFLICT", "could not allocate a unique slug")
		return
	}

	if err := h.replaceGallery(ctx, tx, id, galleryIDs); err != nil {
		handler.WriteServerError(w, r, "INTERNAL_ERROR", "failed to set gallery", err)
		return
	}
	if err := tx.Commit(ctx); err != nil {
		handler.WriteServerError(w, r, "INTERNAL_ERROR", "failed to save entry", err)
		return
	}

	e, err := h.getEntry(ctx, "e.id = $1", id)
	if err != nil {
		handler.WriteServerError(w, r, "INTERNAL_ERROR", "failed to read created entry", err)
		return
	}
	writeJSON(w, http.StatusCreated, h.adminDTO(ctx, e))
}

// ---------------------------------------------------------------------------
// Update
// ---------------------------------------------------------------------------

func (h *Handler) UpdateAdmin(w http.ResponseWriter, r *http.Request) {
	var req adminUpdateRequest
	if !decodeBody(w, r, 1024*1024, &req) {
		return
	}
	current, ok := h.entryByPathID(w, r)
	if !ok {
		return
	}

	kind := current.Kind
	if req.Kind != nil {
		kind = strings.ToLower(strings.TrimSpace(*req.Kind))
		if !knownKind(kind) {
			handler.WriteError(w, http.StatusBadRequest, "VALIDATION_ERROR", "kind is not recognised")
			return
		}
	}
	title := current.Title
	if req.Title != nil {
		title = strings.TrimSpace(*req.Title)
	}
	summary := current.Summary
	if req.Summary != nil {
		summary = strings.TrimSpace(*req.Summary)
	}
	body := current.BodyMD
	if req.BodyMD != nil {
		body = strings.TrimSpace(*req.BodyMD)
	}
	if len([]rune(title)) > 140 {
		handler.WriteError(w, http.StatusBadRequest, "VALIDATION_ERROR", "title must be 140 characters or fewer")
		return
	}
	if len([]rune(summary)) > 320 {
		handler.WriteError(w, http.StatusBadRequest, "VALIDATION_ERROR", "summary must be 320 characters or fewer")
		return
	}
	if len([]rune(body)) > 40000 {
		handler.WriteError(w, http.StatusBadRequest, "VALIDATION_ERROR", "bodyMd must be 40,000 characters or fewer")
		return
	}
	status := current.Status
	if req.Status != nil {
		next := strings.TrimSpace(*req.Status)
		if next != current.Status {
			if !((current.Status == "draft" && next == "archived") || (current.Status == "archived" && next == "draft")) {
				handler.WriteError(w, http.StatusConflict, "INVALID_STATUS_TRANSITION",
					fmt.Sprintf("cannot move an entry from %s to %s here", current.Status, next))
				return
			}
		}
		status = next
	}

	slug := current.Slug
	if req.Slug != nil {
		slug = strings.TrimSpace(*req.Slug)
		if !slugPattern.MatchString(slug) || len(slug) > 100 {
			handler.WriteError(w, http.StatusBadRequest, "VALIDATION_ERROR", "slug must be lowercase words separated by hyphens")
			return
		}
	}

	county := deref(current.County)
	if req.County != nil {
		county = strings.TrimSpace(*req.County)
	}
	category := deref(current.Category)
	if req.Category != nil {
		category = strings.TrimSpace(*req.Category)
	}
	if county != "" {
		canonical, err := h.canonicalCounty(r.Context(), county)
		if err != nil {
			handler.WriteServerError(w, r, "INTERNAL_ERROR", "failed to validate county", err)
			return
		}
		if canonical == "" {
			handler.WriteError(w, http.StatusBadRequest, "VALIDATION_ERROR", "county must match a Kenyan county")
			return
		}
		county = canonical
	}
	if category != "" {
		canonical, err := h.canonicalCategory(r.Context(), kind, category)
		if err != nil {
			handler.WriteServerError(w, r, "INTERNAL_ERROR", "failed to validate category", err)
			return
		}
		if canonical == "" {
			handler.WriteError(w, http.StatusBadRequest, "VALIDATION_ERROR", "category is not valid for this kind")
			return
		}
		category = canonical
	}

	tags := current.Tags
	if req.Tags != nil {
		normalized, ok := normalizeTags(*req.Tags)
		if !ok {
			handler.WriteError(w, http.StatusBadRequest, "VALIDATION_ERROR", "tags must contain at most 10 values of 40 characters or fewer")
			return
		}
		tags = normalized
	}
	team := current.team()
	if req.TeamMembers != nil {
		normalized, ok := normalizeTeam(*req.TeamMembers)
		if !ok {
			handler.WriteError(w, http.StatusBadRequest, "VALIDATION_ERROR", "teamMembers must contain at most 10 named people")
			return
		}
		team = normalized
	}
	teamJSON, err := json.Marshal(team)
	if err != nil {
		handler.WriteServerError(w, r, "INTERNAL_ERROR", "failed to encode team", err)
		return
	}

	externalURL := deref(current.ExternalURL)
	if req.ExternalURL != nil {
		externalURL = strings.TrimSpace(*req.ExternalURL)
	}
	if externalURL != "" && !validHTTPURL(externalURL) {
		handler.WriteError(w, http.StatusBadRequest, "VALIDATION_ERROR", "externalUrl must be an http(s) URL")
		return
	}
	embedURL := deref(current.EmbedURL)
	embedName := deref(current.EmbedProvider)
	if req.EmbedURL != nil {
		embedURL = strings.TrimSpace(*req.EmbedURL)
		embedName = ""
		if embedURL != "" {
			var ok bool
			embedName, ok = embedProvider(embedURL)
			if !ok {
				handler.WriteError(w, http.StatusBadRequest, "INVALID_EMBED_PROVIDER", "embed hosts must be YouTube, Vimeo or Matterport")
				return
			}
		}
	}
	authorName := deref(current.AuthorName)
	if req.AuthorName != nil {
		authorName = strings.TrimSpace(*req.AuthorName)
	}
	authorOrg := deref(current.AuthorOrg)
	if req.AuthorOrg != nil {
		authorOrg = strings.TrimSpace(*req.AuthorOrg)
	}
	competitionName := deref(current.CompetitionName)
	if req.CompetitionName != nil {
		competitionName = strings.TrimSpace(*req.CompetitionName)
	}
	competitionCategory := deref(current.CompetitionCategory)
	if req.CompetitionCategory != nil {
		competitionCategory = strings.TrimSpace(*req.CompetitionCategory)
	}

	coverMediaID := current.CoverMediaID
	if req.CoverMediaID != nil {
		value := strings.TrimSpace(*req.CoverMediaID)
		coverMediaID = nil
		if value != "" {
			if !h.mediaExists(r.Context(), value) {
				handler.WriteError(w, http.StatusBadRequest, "VALIDATION_ERROR", "coverMediaId media asset not found")
				return
			}
			coverMediaID = &value
		}
	}
	documentMediaID := current.DocumentMediaID
	if req.DocumentMediaID != nil {
		value := strings.TrimSpace(*req.DocumentMediaID)
		documentMediaID = nil
		if value != "" {
			if !h.mediaExists(r.Context(), value) {
				handler.WriteError(w, http.StatusBadRequest, "VALIDATION_ERROR", "documentMediaId media asset not found")
				return
			}
			documentMediaID = &value
		}
	}

	if status == "published" {
		if title == "" || summary == "" || body == "" || category == "" {
			handler.WriteError(w, http.StatusBadRequest, "VALIDATION_ERROR", "published entries need title, summary, body and category")
			return
		}
		if coverMediaID == nil && externalURL == "" && embedURL == "" {
			handler.WriteError(w, http.StatusBadRequest, "VALIDATION_ERROR", "published entries need cover media, an external URL or an embed")
			return
		}
	}

	featured := current.Featured
	if req.Featured != nil {
		featured = *req.Featured
	}

	ctx := r.Context()
	tx, err := h.pool.Begin(ctx)
	if err != nil {
		handler.WriteServerError(w, r, "INTERNAL_ERROR", "failed to start update", err)
		return
	}
	defer tx.Rollback(ctx)

	_, err = tx.Exec(ctx, `
UPDATE repository_entries SET
  kind = $2, slug = $3, title = $4, summary = $5, body_md = $6,
  county = NULLIF($7, ''), category = NULLIF($8, ''), tags = $9,
  author_name = NULLIF($10, ''), author_org = NULLIF($11, ''),
  team_members = NULLIF($12::jsonb, 'null'::jsonb),
  competition_name = NULLIF($13, ''), competition_category = NULLIF($14, ''),
  cover_media_id = $15, document_media_id = $16,
  external_url = NULLIF($17, ''), embed_url = NULLIF($18, ''), embed_provider = NULLIF($19, ''),
  status = $20, featured = $21, updated_at = now()
WHERE id = $1`,
		current.ID, kind, slug, title, summary, body,
		county, category, tags,
		authorName, authorOrg, string(teamJSON),
		competitionName, competitionCategory,
		coverMediaID, documentMediaID,
		externalURL, embedURL, embedName,
		status, featured)
	if err != nil {
		if isUniqueViolation(err) {
			handler.WriteError(w, http.StatusConflict, "SLUG_CONFLICT", "slug already exists")
			return
		}
		handler.WriteServerError(w, r, "INTERNAL_ERROR", "failed to update entry", err)
		return
	}

	if req.GalleryMediaIDs != nil {
		ids := dedupeStrings(*req.GalleryMediaIDs)
		if len(ids) > maxGalleryImages {
			handler.WriteError(w, http.StatusBadRequest, "VALIDATION_ERROR", "at most 5 gallery images are allowed")
			return
		}
		for _, id := range ids {
			if !h.mediaExists(ctx, id) {
				handler.WriteError(w, http.StatusBadRequest, "VALIDATION_ERROR", "galleryMediaIds media asset not found")
				return
			}
		}
		if err := h.replaceGallery(ctx, tx, current.ID, ids); err != nil {
			handler.WriteServerError(w, r, "INTERNAL_ERROR", "failed to set gallery", err)
			return
		}
	}

	if err := tx.Commit(ctx); err != nil {
		handler.WriteServerError(w, r, "INTERNAL_ERROR", "failed to save entry", err)
		return
	}

	e, err := h.getEntry(ctx, "e.id = $1", current.ID)
	if err != nil {
		handler.WriteServerError(w, r, "INTERNAL_ERROR", "failed to read updated entry", err)
		return
	}
	writeJSON(w, http.StatusOK, h.adminDTO(ctx, e))
}

// ---------------------------------------------------------------------------
// Delete
// ---------------------------------------------------------------------------

func (h *Handler) DeleteAdmin(w http.ResponseWriter, r *http.Request) {
	if isModerator(r.Context()) {
		handler.WriteError(w, http.StatusForbidden, "FORBIDDEN", "moderators cannot delete entries")
		return
	}
	current, ok := h.entryByPathID(w, r)
	if !ok {
		return
	}
	if current.Status != "draft" && current.Status != "archived" {
		handler.WriteError(w, http.StatusConflict, "INVALID_STATUS_TRANSITION",
			"only draft or archived entries can be deleted")
		return
	}
	ctx := r.Context()
	tx, err := h.pool.Begin(ctx)
	if err != nil {
		handler.WriteServerError(w, r, "INTERNAL_ERROR", "failed to start delete", err)
		return
	}
	defer tx.Rollback(ctx)
	// Unlink the entry's media records; the R2 objects are left in place.
	if _, err := tx.Exec(ctx, `
UPDATE media_assets SET entity_type = 'unlinked', entity_id = NULL, updated_at = now()
WHERE entity_type = 'repository_entry' AND entity_id = $1`, current.ID); err != nil {
		handler.WriteServerError(w, r, "INTERNAL_ERROR", "failed to unlink entry media", err)
		return
	}
	if _, err := tx.Exec(ctx, `DELETE FROM repository_entries WHERE id = $1`, current.ID); err != nil {
		handler.WriteServerError(w, r, "INTERNAL_ERROR", "failed to delete entry", err)
		return
	}
	if err := tx.Commit(ctx); err != nil {
		handler.WriteServerError(w, r, "INTERNAL_ERROR", "failed to delete entry", err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// ---------------------------------------------------------------------------
// Moderation actions
// ---------------------------------------------------------------------------

func (h *Handler) Approve(w http.ResponseWriter, r *http.Request) {
	var req moderationRequest
	if !decodeBodyOptional(w, r, &req) {
		return
	}
	e, ok := h.entryByPathID(w, r)
	if !ok {
		return
	}
	switch e.Status {
	case "draft", "submitted", "rejected":
	default:
		handler.WriteError(w, http.StatusConflict, "INVALID_STATUS_TRANSITION",
			"only draft, submitted or rejected entries can be approved")
		return
	}

	note := strings.TrimSpace(req.Note)
	if len([]rune(note)) > 2000 {
		handler.WriteError(w, http.StatusBadRequest, "VALIDATION_ERROR", "note must be 2000 characters or fewer")
		return
	}
	_, err := h.pool.Exec(r.Context(), `
UPDATE repository_entries SET
  status = 'approved',
  reviewed_by = (SELECT id FROM users WHERE id = $2),
  reviewed_at = now(),
  moderation_note = COALESCE(NULLIF($3, ''), moderation_note),
  updated_at = now()
WHERE id = $1`, e.ID, actor(r.Context()), note)
	if err != nil {
		handler.WriteServerError(w, r, "INTERNAL_ERROR", "failed to approve entry", err)
		return
	}
	h.respondAdmin(w, r, e.ID)
}

func (h *Handler) Publish(w http.ResponseWriter, r *http.Request) {
	e, ok := h.entryByPathID(w, r)
	if !ok {
		return
	}
	switch e.Status {
	case "draft", "approved":
	default:
		handler.WriteError(w, http.StatusConflict, "INVALID_STATUS_TRANSITION",
			"only draft or approved entries can be published")
		return
	}
	if msg := publishBlocker(e); msg != "" {
		handler.WriteError(w, http.StatusBadRequest, "VALIDATION_ERROR", msg)
		return
	}
	_, err := h.pool.Exec(r.Context(), `
UPDATE repository_entries SET status = 'published', published_at = COALESCE(published_at, now()),
  updated_at = now()
WHERE id = $1`, e.ID)
	if err != nil {
		handler.WriteServerError(w, r, "INTERNAL_ERROR", "failed to publish entry", err)
		return
	}
	// Media uploaded with the submission becomes part of the published record.
	_, _ = h.pool.Exec(r.Context(), `
UPDATE media_assets SET status = 'ready', updated_at = now()
WHERE entity_type = 'repository_entry' AND entity_id = $1 AND status = 'pending_review'`, e.ID)
	h.respondAdmin(w, r, e.ID)
}

func (h *Handler) Unpublish(w http.ResponseWriter, r *http.Request) {
	e, ok := h.entryByPathID(w, r)
	if !ok {
		return
	}
	if e.Status != "published" {
		handler.WriteError(w, http.StatusConflict, "INVALID_STATUS_TRANSITION", "only published entries can be unpublished")
		return
	}
	if _, err := h.pool.Exec(r.Context(), `
UPDATE repository_entries SET status = 'approved', updated_at = now() WHERE id = $1`, e.ID); err != nil {
		handler.WriteServerError(w, r, "INTERNAL_ERROR", "failed to unpublish entry", err)
		return
	}
	h.respondAdmin(w, r, e.ID)
}

func (h *Handler) Feature(w http.ResponseWriter, r *http.Request) {
	var req moderationRequest
	if !decodeBodyOptional(w, r, &req) {
		return
	}
	if req.Featured == nil {
		handler.WriteError(w, http.StatusBadRequest, "VALIDATION_ERROR", "featured is required")
		return
	}
	e, ok := h.entryByPathID(w, r)
	if !ok {
		return
	}
	if e.Status != "published" {
		handler.WriteError(w, http.StatusConflict, "INVALID_STATUS_TRANSITION", "only published entries can be featured")
		return
	}
	if _, err := h.pool.Exec(r.Context(), `
UPDATE repository_entries SET featured = $2, updated_at = now() WHERE id = $1`, e.ID, *req.Featured); err != nil {
		handler.WriteServerError(w, r, "INTERNAL_ERROR", "failed to feature entry", err)
		return
	}
	h.respondAdmin(w, r, e.ID)
}

func (h *Handler) Reject(w http.ResponseWriter, r *http.Request) {
	var req moderationRequest
	if !decodeBodyOptional(w, r, &req) {
		return
	}
	note := strings.TrimSpace(req.Note)
	if len([]rune(note)) < 10 || len([]rune(note)) > 2000 {
		v := validationCollector{}
		v.add("note", "a rejection note of 10 to 2000 characters is required")
		v.write(w)
		return
	}
	e, ok := h.entryByPathID(w, r)
	if !ok {
		return
	}
	switch e.Status {
	case "submitted", "approved":
	default:
		handler.WriteError(w, http.StatusConflict, "INVALID_STATUS_TRANSITION",
			"only submitted or approved entries can be rejected")
		return
	}
	_, err := h.pool.Exec(r.Context(), `
UPDATE repository_entries SET
  status = 'rejected',
  reviewed_by = (SELECT id FROM users WHERE id = $2),
  reviewed_at = now(),
  moderation_note = $3,
  updated_at = now()
WHERE id = $1`, e.ID, actor(r.Context()), note)
	if err != nil {
		handler.WriteServerError(w, r, "INTERNAL_ERROR", "failed to reject entry", err)
		return
	}
	h.respondAdmin(w, r, e.ID)
}

func (h *Handler) respondAdmin(w http.ResponseWriter, r *http.Request, id string) {
	e, err := h.getEntry(r.Context(), "e.id = $1", id)
	if err != nil {
		handler.WriteServerError(w, r, "INTERNAL_ERROR", "failed to read entry", err)
		return
	}
	writeJSON(w, http.StatusOK, h.adminDTO(r.Context(), e))
}

// ---------------------------------------------------------------------------
// Settings
// ---------------------------------------------------------------------------

func (h *Handler) AdminGetSettings(w http.ResponseWriter, r *http.Request) {
	s, err := h.loadSettings(r.Context())
	if err != nil {
		handler.WriteServerError(w, r, "INTERNAL_ERROR", "failed to read repository settings", err)
		return
	}
	resp := adminSettings{
		ID:              "default",
		SubmissionsOpen: s.Open,
		SubmissionIntro: s.Intro,
		TermsVersion:    s.Terms,
	}
	if s.UpdatedAt != nil {
		resp.UpdatedAt = formatTime(*s.UpdatedAt)
	}
	writeJSON(w, http.StatusOK, resp)
}

func (h *Handler) AdminUpdateSettings(w http.ResponseWriter, r *http.Request) {
	var req settingsPatch
	if !decodeBody(w, r, 32*1024, &req) {
		return
	}
	if req.TermsVersion != nil && strings.TrimSpace(*req.TermsVersion) == "" {
		handler.WriteError(w, http.StatusBadRequest, "VALIDATION_ERROR", "termsVersion cannot be empty")
		return
	}
	if req.SubmissionIntro != nil && len([]rune(*req.SubmissionIntro)) > 2000 {
		handler.WriteError(w, http.StatusBadRequest, "VALIDATION_ERROR", "submissionIntro must be 2000 characters or fewer")
		return
	}

	_, err := h.pool.Exec(r.Context(), `
UPDATE repository_settings SET
  submissions_open = COALESCE($1, submissions_open),
  submission_intro = COALESCE($2, submission_intro),
  terms_version = COALESCE($3, terms_version),
  updated_at = now()
WHERE id = 'default'`,
		req.SubmissionsOpen, req.SubmissionIntro, req.TermsVersion)
	if err != nil {
		handler.WriteServerError(w, r, "INTERNAL_ERROR", "failed to update repository settings", err)
		return
	}
	h.AdminGetSettings(w, r)
}

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

// decodeBodyOptional allows an empty body for action routes.
func decodeBodyOptional(w http.ResponseWriter, r *http.Request, dst any) bool {
	r.Body = http.MaxBytesReader(w, r.Body, 64*1024)
	err := json.NewDecoder(r.Body).Decode(dst)
	if err != nil && !errors.Is(err, io.EOF) {
		handler.WriteError(w, http.StatusBadRequest, "INVALID_REQUEST", "invalid request body")
		return false
	}
	return true
}

func publishBlocker(e entryRecord) string {
	if strings.TrimSpace(e.Title) == "" || strings.TrimSpace(e.Summary) == "" ||
		strings.TrimSpace(e.BodyMD) == "" || deref(e.Category) == "" {
		return "published entries need title, summary, body and category"
	}
	if e.CoverMediaID == nil && deref(e.ExternalURL) == "" && deref(e.EmbedURL) == "" {
		return "published entries need cover media, an external URL or an embed"
	}
	return ""
}

func (h *Handler) mediaExists(ctx context.Context, id string) bool {
	if _, err := uuid.Parse(id); err != nil {
		return false
	}
	var n int
	if err := h.pool.QueryRow(ctx, `SELECT count(*)::int FROM media_assets WHERE id = $1`, id).Scan(&n); err != nil {
		return false
	}
	return n > 0
}

func (h *Handler) replaceGallery(ctx context.Context, tx pgx.Tx, entryID string, ids []string) error {
	if _, err := tx.Exec(ctx, `DELETE FROM repository_entry_media WHERE entry_id = $1`, entryID); err != nil {
		return err
	}
	for i, id := range ids {
		if _, err := tx.Exec(ctx, `
INSERT INTO repository_entry_media (entry_id, media_id, sort_order) VALUES ($1, $2, $3)
ON CONFLICT (entry_id, media_id) DO UPDATE SET sort_order = EXCLUDED.sort_order`,
			entryID, id, i); err != nil {
			return err
		}
	}
	return nil
}

func dedupeStrings(values []string) []string {
	seen := map[string]bool{}
	out := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" || seen[value] {
			continue
		}
		seen[value] = true
		out = append(out, value)
	}
	return out
}

func isUniqueViolation(err error) bool {
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) {
		return pgErr.Code == "23505"
	}
	return false
}
