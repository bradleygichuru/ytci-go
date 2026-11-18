package admin

import (
	"encoding/json"
	"net/http"
	"strings"

	"github.com/jackc/pgx/v5"

	"github.com/bradleygichuru/ytci-go/internal/handler"
)

var heritageEvents = map[string]bool{
	"portal_opened":  true,
	"video_played":   true,
	"visit_marked":   true,
	"verified_visit": true,
}

type anonymousHeritageEvent struct {
	Event    string `json:"event"`
	SiteSlug string `json:"siteSlug"`
}

// RecordAnonymousEvent increments a daily heritage count with no reader,
// device, or account identifier. Sensitive sites are stored only as a combined
// bucket. It is intentionally public and safe for guests.
func (h *ThenNowHandler) RecordAnonymousEvent(w http.ResponseWriter, r *http.Request) {
	var req anonymousHeritageEvent
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		handler.WriteError(w, http.StatusBadRequest, "INVALID_BODY", "invalid request body")
		return
	}
	req.Event = strings.TrimSpace(req.Event)
	req.SiteSlug = strings.TrimSpace(req.SiteSlug)
	if !heritageEvents[req.Event] || req.SiteSlug == "" {
		handler.WriteError(w, http.StatusBadRequest, "INVALID_EVENT", "a known event and siteSlug are required")
		return
	}

	var siteID, sensitivity string
	err := h.pool.QueryRow(r.Context(), `
		SELECT id::text, sensitivity FROM heritage_sites
		WHERE slug = $1 AND status = 'published'`, req.SiteSlug).Scan(&siteID, &sensitivity)
	if err != nil {
		if err == pgx.ErrNoRows {
			handler.WriteError(w, http.StatusNotFound, "NOT_FOUND", "published site not found")
			return
		}
		handler.WriteServerError(w, r, "INTERNAL_ERROR", "failed to load site for event", err)
		return
	}

	scope := "site"
	storedSiteID := siteID
	if sensitivity != "standard" {
		scope = "sensitive"
		storedSiteID = ""
	}
	if _, err := h.pool.Exec(r.Context(), `
		INSERT INTO anonymous_heritage_counts (day, scope, site_id, event, count)
		VALUES (CURRENT_DATE, $1, NULLIF($2, '')::uuid, $3, 1)
		ON CONFLICT (day, scope, site_id, event)
		DO UPDATE SET count = anonymous_heritage_counts.count + 1`,
		scope, storedSiteID, req.Event); err != nil {
		handler.WriteServerError(w, r, "INTERNAL_ERROR", "failed to record heritage event", err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// Heritage returns aggregate heritage counts for the admin dashboard. Counts
// below 5 are suppressed and sensitive rows never expose a site identity.
func (h *AnalyticsHandler) Heritage(w http.ResponseWriter, r *http.Request) {
	rows, err := h.pool.Query(r.Context(), `
		SELECT c.day::text, c.scope, COALESCE(s.id::text, ''), COALESCE(s.name, ''),
		       c.event, c.count
		FROM anonymous_heritage_counts c
		LEFT JOIN heritage_sites s ON s.id = c.site_id
		WHERE c.day >= CURRENT_DATE - INTERVAL '30 days'
		  AND c.count >= 5
		ORDER BY c.day DESC, c.event, s.name`)
	if err != nil {
		handler.WriteServerError(w, r, "INTERNAL_ERROR", "failed to load heritage analytics", err)
		return
	}
	defer rows.Close()

	items := make([]map[string]any, 0)
	for rows.Next() {
		var day, scope, siteID, siteName, event string
		var count int64
		if err := rows.Scan(&day, &scope, &siteID, &siteName, &event, &count); err != nil {
			handler.WriteServerError(w, r, "INTERNAL_ERROR", "failed to read heritage analytics", err)
			return
		}
		if scope == "sensitive" {
			siteName = "Sensitive sites (combined)"
		}
		items = append(items, map[string]any{
			"day": day, "scope": scope, "siteId": siteID, "siteName": siteName,
			"event": event, "count": count,
		})
	}
	if err := rows.Err(); err != nil {
		handler.WriteServerError(w, r, "INTERNAL_ERROR", "failed to iterate heritage analytics", err)
		return
	}
	writeThenNowJSON(w, http.StatusOK, map[string]any{"items": items})
}
