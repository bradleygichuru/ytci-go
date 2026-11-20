package repository

import (
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/bradleygichuru/ytci-go/internal/handler"
	"github.com/bradleygichuru/ytci-go/internal/pagination"
)

// ListPublic serves the published catalogue, newest first.
func (h *Handler) ListPublic(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()

	kinds, err := parseKinds(q["kind"])
	if err != nil {
		handler.WriteError(w, http.StatusBadRequest, "INVALID_REQUEST", err.Error())
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

	rows, err := h.pool.Query(r.Context(), entryColumns+`
WHERE e.status = 'published'
  AND (coalesce(cardinality($1::text[]), 0) = 0 OR e.kind = ANY($1::text[]))
  AND ($2::text = '' OR e.county = $2)
  AND ($3::text = '' OR e.category = $3)
  AND ($4::boolean = false OR e.featured = true)
  AND (
    $5::text = ''
    OR (length(btrim($5)) >= 3 AND e.search_vector @@ websearch_to_tsquery('english', $5))
    OR position(lower(btrim($5)) in lower(e.title)) > 0
    OR position(lower(btrim($5)) in lower(e.summary)) > 0
    OR position(lower(btrim($5)) in lower(array_to_string(e.tags, ' '))) > 0
  )
  AND ($6::timestamp IS NULL OR (e.published_at, e.id) < ($6::timestamp, $7::uuid))
ORDER BY e.published_at DESC, e.id DESC
LIMIT $8`,
		kinds, strings.TrimSpace(q.Get("county")), strings.TrimSpace(q.Get("category")),
		featuredOnly, strings.TrimSpace(q.Get("q")), cursorTime, cursorID, limit+1)
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
	items := make([]publicSummary, 0, len(entries))
	for _, e := range entries {
		items = append(items, e.summaryDTO(r.Context(), h))
	}

	resp := publicListResponse{Items: items, HasMore: hasMore}
	if hasMore && len(entries) > 0 {
		last := entries[len(entries)-1]
		if last.PublishedAt != nil {
			cur := pagination.EncodeCursor(last.PublishedAt.UTC().Format(time.RFC3339Nano), last.ID)
			resp.NextCursor = &cur
		}
	}

	writePublicCache(w)
	writeJSON(w, http.StatusOK, resp)
}

// GetPublic serves the published detail. Any non-published status returns the
// same 404 so the response never hints at editorial state.
func (h *Handler) GetPublic(w http.ResponseWriter, r *http.Request) {
	slug := strings.TrimSpace(r.PathValue("slug"))
	if slug == "" {
		handler.WriteError(w, http.StatusNotFound, "NOT_FOUND", "entry not found")
		return
	}
	e, err := h.getEntry(r.Context(), "e.slug = $1 AND e.status = 'published'", slug)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			handler.WriteError(w, http.StatusNotFound, "NOT_FOUND", "entry not found")
			return
		}
		handler.WriteServerError(w, r, "INTERNAL_ERROR", "failed to read repository entry", err)
		return
	}

	writePublicCache(w)
	writeJSON(w, http.StatusOK, h.publicDetail(r.Context(), e))
}

// Facets returns published counts for the filter UI. Zero-count values are
// omitted.
func (h *Handler) Facets(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	resp := facetsResponse{
		Kinds:      []kindFacet{},
		Counties:   []facetValue{},
		Categories: []categoryFacet{},
		Tags:       []facetValue{},
	}

	kindCounts := map[string]int{}
	rows, err := h.pool.Query(ctx, `SELECT kind, count(*)::int FROM repository_entries WHERE status = 'published' GROUP BY kind`)
	if err != nil {
		handler.WriteServerError(w, r, "INTERNAL_ERROR", "failed to read repository facets", err)
		return
	}
	for rows.Next() {
		var kind string
		var count int
		if err := rows.Scan(&kind, &count); err != nil {
			rows.Close()
			handler.WriteServerError(w, r, "INTERNAL_ERROR", "failed to read repository facets", err)
			return
		}
		kindCounts[kind] = count
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		handler.WriteServerError(w, r, "INTERNAL_ERROR", "failed to read repository facets", err)
		return
	}
	for _, kind := range Kinds {
		if kindCounts[kind] > 0 {
			resp.Kinds = append(resp.Kinds, kindFacet{Value: kind, Label: kindLabels[kind], Count: kindCounts[kind]})
		}
	}

	countyRows, err := h.pool.Query(ctx, `
SELECT county, count(*)::int FROM repository_entries
WHERE status = 'published' AND county IS NOT NULL AND btrim(county) <> ''
GROUP BY county ORDER BY count(*) DESC, county ASC`)
	if err != nil {
		handler.WriteServerError(w, r, "INTERNAL_ERROR", "failed to read repository facets", err)
		return
	}
	for countyRows.Next() {
		var value string
		var count int
		if err := countyRows.Scan(&value, &count); err != nil {
			countyRows.Close()
			handler.WriteServerError(w, r, "INTERNAL_ERROR", "failed to read repository facets", err)
			return
		}
		resp.Counties = append(resp.Counties, facetValue{Value: value, Count: count})
	}
	countyRows.Close()
	if err := countyRows.Err(); err != nil {
		handler.WriteServerError(w, r, "INTERNAL_ERROR", "failed to read repository facets", err)
		return
	}

	catRows, err := h.pool.Query(ctx, `
SELECT kind, category, count(*)::int FROM repository_entries
WHERE status = 'published' AND category IS NOT NULL AND btrim(category) <> ''
GROUP BY kind, category ORDER BY count(*) DESC, kind ASC, category ASC`)
	if err != nil {
		handler.WriteServerError(w, r, "INTERNAL_ERROR", "failed to read repository facets", err)
		return
	}
	for catRows.Next() {
		var item categoryFacet
		if err := catRows.Scan(&item.Kind, &item.Value, &item.Count); err != nil {
			catRows.Close()
			handler.WriteServerError(w, r, "INTERNAL_ERROR", "failed to read repository facets", err)
			return
		}
		resp.Categories = append(resp.Categories, item)
	}
	catRows.Close()
	if err := catRows.Err(); err != nil {
		handler.WriteServerError(w, r, "INTERNAL_ERROR", "failed to read repository facets", err)
		return
	}

	tagRows, err := h.pool.Query(ctx, `
SELECT tag, count(*)::int FROM (
  SELECT unnest(tags) AS tag FROM repository_entries WHERE status = 'published'
) t
WHERE tag <> ''
GROUP BY tag ORDER BY count(*) DESC, tag ASC LIMIT 100`)
	if err != nil {
		handler.WriteServerError(w, r, "INTERNAL_ERROR", "failed to read repository facets", err)
		return
	}
	for tagRows.Next() {
		var item facetValue
		if err := tagRows.Scan(&item.Value, &item.Count); err != nil {
			tagRows.Close()
			handler.WriteServerError(w, r, "INTERNAL_ERROR", "failed to read repository facets", err)
			return
		}
		resp.Tags = append(resp.Tags, item)
	}
	tagRows.Close()
	if err := tagRows.Err(); err != nil {
		handler.WriteServerError(w, r, "INTERNAL_ERROR", "failed to read repository facets", err)
		return
	}

	writePublicCache(w)
	writeJSON(w, http.StatusOK, resp)
}

// Stats returns real published counts, never hard-coded figures.
func (h *Handler) Stats(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	var resp statsResponse
	var last *time.Time
	err := h.pool.QueryRow(ctx, `
SELECT count(*)::int,
       count(DISTINCT county) FILTER (WHERE county IS NOT NULL AND btrim(county) <> '')::int,
       count(*) FILTER (WHERE featured)::int,
       max(published_at)
FROM repository_entries WHERE status = 'published'`).
		Scan(&resp.PublishedEntries, &resp.CountiesCovered, &resp.FeaturedEntries, &last)
	if err != nil {
		handler.WriteServerError(w, r, "INTERNAL_ERROR", "failed to read repository stats", err)
		return
	}
	resp.LastPublishedAt = timePtr(last)
	resp.ByKind = map[string]int{}
	for _, kind := range Kinds {
		resp.ByKind[kind] = 0
	}

	rows, err := h.pool.Query(ctx, `SELECT kind, count(*)::int FROM repository_entries WHERE status = 'published' GROUP BY kind`)
	if err != nil {
		handler.WriteServerError(w, r, "INTERNAL_ERROR", "failed to read repository stats", err)
		return
	}
	defer rows.Close()
	for rows.Next() {
		var kind string
		var count int
		if err := rows.Scan(&kind, &count); err != nil {
			handler.WriteServerError(w, r, "INTERNAL_ERROR", "failed to read repository stats", err)
			return
		}
		resp.ByKind[kind] = count
	}
	if err := rows.Err(); err != nil {
		handler.WriteServerError(w, r, "INTERNAL_ERROR", "failed to read repository stats", err)
		return
	}

	// Additive flat aliases for clients that do not read the ByKind map.
	resp.Entries = resp.PublishedEntries
	resp.Counties = resp.CountiesCovered
	resp.Competitions = resp.ByKind["competition_entry"]
	resp.Innovations = resp.ByKind["innovation"]
	resp.ConservationResources = resp.ByKind["conservation_resource"]
	resp.Experiences = resp.ByKind["experience"]

	writePublicCache(w)
	writeJSON(w, http.StatusOK, resp)
}

// Settings returns the public submission configuration.
func (h *Handler) Settings(w http.ResponseWriter, r *http.Request) {
	var (
		open      bool
		intro     *string
		terms     string
		updatedAt *time.Time
	)
	err := h.pool.QueryRow(r.Context(), `
SELECT submissions_open, submission_intro, terms_version, updated_at
FROM repository_settings WHERE id = 'default'`).
		Scan(&open, &intro, &terms, &updatedAt)
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		handler.WriteServerError(w, r, "INTERNAL_ERROR", "failed to read repository settings", err)
		return
	}
	if err != nil {
		// Missing singleton: documented defaults.
		open = false
		terms = "2026-11"
	}

	resp := publicSettings{
		SubmissionsOpen: open,
		SubmissionIntro: deref(intro),
		TermsVersion:    terms,
		UpdatedAt:       formatTime(time.Now()),
	}
	if updatedAt != nil {
		resp.UpdatedAt = formatTime(*updatedAt)
	}

	writePublicCache(w)
	writeJSON(w, http.StatusOK, resp)
}
