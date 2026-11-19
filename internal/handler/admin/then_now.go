package admin

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"regexp"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/bradleygichuru/ytci-go/internal/handler"
	"github.com/bradleygichuru/ytci-go/internal/middleware"
	"github.com/bradleygichuru/ytci-go/internal/r2"
	"github.com/bradleygichuru/ytci-go/internal/thennow"
)

type ThenNowHandler struct {
	pool     *pgxpool.Pool
	media    r2.Store
	mediaTTL time.Duration
}

func NewThenNowHandler(pool *pgxpool.Pool, media r2.Store, mediaTTL time.Duration) *ThenNowHandler {
	if mediaTTL <= 0 {
		mediaTTL = 15 * time.Minute
	}
	return &ThenNowHandler{pool: pool, media: media, mediaTTL: mediaTTL}
}

type scanner interface {
	Scan(dest ...any) error
}

var thenNowSlugPattern = regexp.MustCompile(`^[a-z0-9-]{2,80}$`)

const siteSelect = `SELECT s.id::text, s.name, s.slug, s.county, COALESCE(s.locality, ''),
	s.site_type, s.access_mode, s.sensitivity, s.status,
	COALESCE(s.managing_authority, ''), s.destination_id::text,
	COALESCE(s.photography_policy, ''), COALESCE(s.cultural_authority, ''),
	COALESCE(s.consent_record, ''), COALESCE(to_char(s.consent_date, 'YYYY-MM-DD'), ''),
	COALESCE(s.site_rules, ''), COALESCE(s.safety_notes, ''),
	COALESCE(s.short_description, ''), COALESCE(s.full_description, ''),
	COALESCE(s.sources, ''), COALESCE(s.fact_check, ''),
	COALESCE(s.public_rights_line, ''),
	COALESCE(s.latitude, 0)::float8, COALESCE(s.longitude, 0)::float8,
	s.geofence_radius_m, COALESCE(s.standing_point_note, ''), COALESCE(s.image_key, ''),
	(SELECT count(*)::int FROM then_now_experiences e
	 WHERE e.site_id = s.id AND e.status = 'published'),
	s.updated_at
	FROM heritage_sites s`

func scanSite(row scanner, site *thennow.Site) error {
	var updatedAt pgtype.Timestamp
	if err := row.Scan(
		&site.ID, &site.Name, &site.Slug, &site.County, &site.Locality,
		&site.SiteType, &site.AccessMode, &site.Sensitivity, &site.Status,
		&site.ManagingAuthority, &site.DestinationID, &site.PhotographyPolicy,
		&site.CulturalAuthority, &site.ConsentRecord, &site.ConsentDate,
		&site.SiteRules, &site.SafetyNotes, &site.ShortDescription,
		&site.FullDescription, &site.Sources, &site.FactCheck,
		&site.PublicRightsLine, &site.Latitude, &site.Longitude,
		&site.GeofenceRadiusM, &site.StandingPointNote, &site.ImageKey,
		&site.PublishedExperiences, &updatedAt,
	); err != nil {
		return err
	}
	site.UpdatedAt = formatTSNano(updatedAt)
	return nil
}

func (h *ThenNowHandler) loadSite(ctx context.Context, id string) (thennow.Site, error) {
	var site thennow.Site
	err := scanSite(h.pool.QueryRow(ctx, siteSelect+` WHERE s.id = $1`, id), &site)
	return site, err
}

func (h *ThenNowHandler) loadSiteBySlug(ctx context.Context, slug string) (thennow.Site, error) {
	var site thennow.Site
	err := scanSite(h.pool.QueryRow(ctx, siteSelect+` WHERE s.slug = $1`, slug), &site)
	return site, err
}

type siteRequest struct {
	Name              string   `json:"name"`
	Slug              string   `json:"slug"`
	County            string   `json:"county"`
	Locality          string   `json:"locality"`
	SiteType          string   `json:"siteType"`
	AccessMode        string   `json:"accessMode"`
	Sensitivity       string   `json:"sensitivity"`
	ManagingAuthority string   `json:"managingAuthority"`
	DestinationID     string   `json:"destinationId"`
	PhotographyPolicy string   `json:"photographyPolicy"`
	CulturalAuthority string   `json:"culturalAuthority"`
	ConsentRecord     string   `json:"consentRecord"`
	ConsentDate       string   `json:"consentDate"`
	SiteRules         string   `json:"siteRules"`
	SafetyNotes       string   `json:"safetyNotes"`
	ShortDescription  string   `json:"shortDescription"`
	FullDescription   string   `json:"fullDescription"`
	Sources           string   `json:"sources"`
	FactCheck         string   `json:"factCheck"`
	PublicRightsLine  string   `json:"publicRightsLine"`
	Latitude          *float64 `json:"latitude"`
	Longitude         *float64 `json:"longitude"`
	GeofenceRadiusM   *int     `json:"geofenceRadiusM"`
	StandingPointNote string   `json:"standingPointNote"`
	ImageKey          string   `json:"imageKey"`
}

type sitePatch struct {
	Name              *string  `json:"name"`
	Slug              *string  `json:"slug"`
	County            *string  `json:"county"`
	Locality          *string  `json:"locality"`
	SiteType          *string  `json:"siteType"`
	AccessMode        *string  `json:"accessMode"`
	Sensitivity       *string  `json:"sensitivity"`
	ManagingAuthority *string  `json:"managingAuthority"`
	DestinationID     *string  `json:"destinationId"`
	PhotographyPolicy *string  `json:"photographyPolicy"`
	CulturalAuthority *string  `json:"culturalAuthority"`
	ConsentRecord     *string  `json:"consentRecord"`
	ConsentDate       *string  `json:"consentDate"`
	SiteRules         *string  `json:"siteRules"`
	SafetyNotes       *string  `json:"safetyNotes"`
	ShortDescription  *string  `json:"shortDescription"`
	FullDescription   *string  `json:"fullDescription"`
	Sources           *string  `json:"sources"`
	FactCheck         *string  `json:"factCheck"`
	PublicRightsLine  *string  `json:"publicRightsLine"`
	Latitude          *float64 `json:"latitude"`
	Longitude         *float64 `json:"longitude"`
	GeofenceRadiusM   *int     `json:"geofenceRadiusM"`
	StandingPointNote *string  `json:"standingPointNote"`
	ImageKey          *string  `json:"imageKey"`
	// ExpectedUpdatedAt is an optional compare-and-swap precondition. When it
	// does not match the stored updated_at the PATCH returns 409 STALE_SITE.
	// The same value may be sent in the If-Match header instead.
	ExpectedUpdatedAt *string `json:"expectedUpdatedAt"`
}

func validateSiteRequest(req siteRequest) string {
	if strings.TrimSpace(req.Name) == "" {
		return "site name is required"
	}
	if strings.TrimSpace(req.County) == "" {
		return "county is required"
	}
	if req.SiteType != "historical" && req.SiteType != "cultural" {
		return "siteType must be historical or cultural"
	}
	if !validAccessMode(req.AccessMode) {
		return "accessMode is required"
	}
	if !validSensitivity(req.Sensitivity) {
		return "sensitivity is required"
	}
	return ""
}

func validAccessMode(value string) bool {
	return value == "camera_video" || value == "video_only" || value == "not_available"
}

func validSensitivity(value string) bool {
	return value == "standard" || value == "sensitive" || value == "restricted"
}

func validSiteType(value string) bool {
	return value == "historical" || value == "cultural"
}

func thenNowSlug(value string) string {
	slug := strings.ToLower(strings.TrimSpace(value))
	slug = strings.ReplaceAll(slug, " ", "-")
	slug = regexp.MustCompile(`[^a-z0-9-]`).ReplaceAllString(slug, "")
	slug = regexp.MustCompile(`-+`).ReplaceAllString(slug, "-")
	return strings.Trim(slug, "-")
}

func parseOptionalDate(value string) *time.Time {
	if strings.TrimSpace(value) == "" {
		return nil
	}
	parsed, err := time.Parse("2006-01-02", value)
	if err != nil {
		return nil
	}
	return &parsed
}

func (h *ThenNowHandler) ListSites(w http.ResponseWriter, r *http.Request) {
	if !middleware.CanThenNowRead(middleware.RoleFromCtx(r.Context())) {
		handler.WriteError(w, http.StatusForbidden, "FORBIDDEN", "Then & Now access required")
		return
	}
	county := strings.TrimSpace(r.URL.Query().Get("county"))
	access := strings.TrimSpace(r.URL.Query().Get("accessMode"))
	sensitivity := strings.TrimSpace(r.URL.Query().Get("sensitivity"))
	status := strings.TrimSpace(r.URL.Query().Get("status"))
	search := strings.TrimSpace(r.URL.Query().Get("q"))

	rows, err := h.pool.Query(r.Context(), siteSelect+`
		WHERE ($1 = '' OR s.county = $1)
		  AND ($2 = '' OR s.access_mode = $2)
		  AND ($3 = '' OR s.sensitivity = $3)
		  AND ($4 = '' OR s.status = $4)
		  AND ($5 = '' OR s.name ILIKE '%' || $5 || '%' OR COALESCE(s.locality, '') ILIKE '%' || $5 || '%')
		ORDER BY s.updated_at DESC`, county, access, sensitivity, status, search)
	if err != nil {
		handler.WriteServerError(w, r, "INTERNAL_ERROR", "failed to list heritage sites", err)
		return
	}
	defer rows.Close()

	items := make([]thennow.Site, 0)
	for rows.Next() {
		var site thennow.Site
		if err := scanSite(rows, &site); err != nil {
			handler.WriteServerError(w, r, "INTERNAL_ERROR", "failed to read heritage site", err)
			return
		}
		items = append(items, site)
	}
	writeThenNowJSON(w, http.StatusOK, map[string]any{"items": items})
}

func (h *ThenNowHandler) CreateSite(w http.ResponseWriter, r *http.Request) {
	if !middleware.CanThenNowAuthor(middleware.RoleFromCtx(r.Context())) {
		handler.WriteError(w, http.StatusForbidden, "FORBIDDEN", "authoring access required")
		return
	}
	var req siteRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		handler.WriteError(w, http.StatusBadRequest, "INVALID_BODY", "invalid request body")
		return
	}
	if message := validateSiteRequest(req); message != "" {
		handler.WriteError(w, http.StatusBadRequest, "VALIDATION_ERROR", message)
		return
	}
	slug := req.Slug
	if slug == "" {
		slug = thenNowSlug(req.Name)
	}
	if !thenNowSlugPattern.MatchString(slug) {
		handler.WriteError(w, http.StatusBadRequest, "VALIDATION_ERROR", "slug must be kebab-case")
		return
	}
	radius := 150
	if req.GeofenceRadiusM != nil {
		radius = *req.GeofenceRadiusM
	}
	if radius < 10 || radius > 5000 {
		handler.WriteError(w, http.StatusBadRequest, "VALIDATION_ERROR", "geofenceRadiusM must be between 10 and 5000")
		return
	}
	actor := middleware.UserID(r.Context())
	var id string
	err := h.pool.QueryRow(r.Context(), `
		INSERT INTO heritage_sites (
			name, slug, county, locality, site_type, access_mode, sensitivity, status,
			managing_authority, destination_id, photography_policy, cultural_authority,
			consent_record, consent_date, site_rules, safety_notes, short_description,
			full_description, sources, fact_check, public_rights_line, latitude, longitude,
			geofence_radius_m, standing_point_note, image_key, created_by, updated_by
		) VALUES (
			$1,$2,$3,$4,$5,$6,$7,'draft',$8,NULLIF($9,'')::uuid,$10,$11,$12,$13,$14,$15,$16,
			$17,$18,$19,$20,$21,$22,$23,$24,$25,$26,$27
		) RETURNING id::text`,
		strings.TrimSpace(req.Name), slug, strings.TrimSpace(req.County), nullIfEmpty(req.Locality),
		req.SiteType, req.AccessMode, req.Sensitivity, nullIfEmpty(req.ManagingAuthority),
		strings.TrimSpace(req.DestinationID), nullIfEmpty(req.PhotographyPolicy),
		nullIfEmpty(req.CulturalAuthority), nullIfEmpty(req.ConsentRecord), parseOptionalDate(req.ConsentDate),
		nullIfEmpty(req.SiteRules), nullIfEmpty(req.SafetyNotes), nullIfEmpty(req.ShortDescription),
		nullIfEmpty(req.FullDescription), nullIfEmpty(req.Sources), nullIfEmpty(req.FactCheck),
		nullIfEmpty(req.PublicRightsLine), req.Latitude, req.Longitude, radius,
		nullIfEmpty(req.StandingPointNote), nullIfEmpty(req.ImageKey), actor, actor,
	).Scan(&id)
	if err != nil {
		if isUniqueViolation(err) {
			handler.WriteError(w, http.StatusConflict, "SLUG_EXISTS", "a site with that slug already exists")
			return
		}
		handler.WriteServerError(w, r, "INTERNAL_ERROR", "failed to create heritage site", err)
		return
	}
	site, err := h.loadSite(r.Context(), id)
	if err != nil {
		handler.WriteServerError(w, r, "INTERNAL_ERROR", "failed to load heritage site", err)
		return
	}
	h.audit(r.Context(), "site", id, "site", actor, "Site draft created", nil)
	writeThenNowJSON(w, http.StatusCreated, site)
}

func (h *ThenNowHandler) GetSite(w http.ResponseWriter, r *http.Request) {
	if !middleware.CanThenNowRead(middleware.RoleFromCtx(r.Context())) {
		handler.WriteError(w, http.StatusForbidden, "FORBIDDEN", "Then & Now access required")
		return
	}
	site, err := h.loadSite(r.Context(), chi.URLParam(r, "id"))
	if err != nil {
		handler.WriteError(w, http.StatusNotFound, "NOT_FOUND", "heritage site not found")
		return
	}
	rows, err := h.pool.Query(r.Context(), `
		SELECT e.id::text, e.title, e.status, e.live_revision_number, e.open_revision_number,
		       COALESCE(r.status, ''), COALESCE(r.snapshot->>'primaryMode', 'video_only'),
		       COALESCE(r.snapshot->'languages', '[]'::jsonb), COALESCE(r.snapshot->>'rightsExpiry', '')
		FROM then_now_experiences e
		LEFT JOIN then_now_revisions r ON r.experience_id = e.id AND r.revision_number = e.open_revision_number
		WHERE e.site_id = $1 ORDER BY e.created_at ASC`, site.ID)
	if err != nil {
		handler.WriteServerError(w, r, "INTERNAL_ERROR", "failed to list site experiences", err)
		return
	}
	defer rows.Close()
	experiences := make([]map[string]any, 0)
	for rows.Next() {
		var id, title, status, openStatus, mode, rights string
		var live, open int
		var languages []byte
		if err := rows.Scan(&id, &title, &status, &live, &open, &openStatus, &mode, &languages, &rights); err != nil {
			handler.WriteServerError(w, r, "INTERNAL_ERROR", "failed to read site experience", err)
			return
		}
		experiences = append(experiences, map[string]any{
			"id": id, "title": title, "status": status,
			"liveRevisionNumber": live, "openRevisionNumber": open,
			"openStatus": openStatus, "primaryMode": mode,
			"languages": json.RawMessage(languages), "rightsExpiry": rights,
		})
	}
	writeThenNowJSON(w, http.StatusOK, map[string]any{"site": site, "experiences": experiences, "readiness": thennow.SiteReadiness(site)})
}

func (h *ThenNowHandler) UpdateSite(w http.ResponseWriter, r *http.Request) {
	if !middleware.CanThenNowAuthor(middleware.RoleFromCtx(r.Context())) {
		handler.WriteError(w, http.StatusForbidden, "FORBIDDEN", "authoring access required")
		return
	}
	id := chi.URLParam(r, "id")
	if _, err := h.loadSite(r.Context(), id); err != nil {
		handler.WriteError(w, http.StatusNotFound, "NOT_FOUND", "heritage site not found")
		return
	}
	var patch sitePatch
	if err := json.NewDecoder(r.Body).Decode(&patch); err != nil {
		handler.WriteError(w, http.StatusBadRequest, "INVALID_BODY", "invalid request body")
		return
	}
	expected := ""
	if patch.ExpectedUpdatedAt != nil {
		expected = strings.TrimSpace(*patch.ExpectedUpdatedAt)
	}
	if expected == "" {
		expected = strings.Trim(strings.TrimSpace(r.Header.Get("If-Match")), `"`)
	}
	if expected == "*" {
		expected = ""
	}
	sets := []string{"updated_at = now()"}
	args := []any{id}
	add := func(column string, value any) {
		args = append(args, value)
		sets = append(sets, fmt.Sprintf("%s = $%d", column, len(args)))
	}
	addExpr := func(expr string, value any) {
		args = append(args, value)
		sets = append(sets, fmt.Sprintf(expr, len(args)))
	}
	if patch.Name != nil {
		if strings.TrimSpace(*patch.Name) == "" {
			handler.WriteError(w, http.StatusBadRequest, "VALIDATION_ERROR", "site name is required")
			return
		}
		add("name", strings.TrimSpace(*patch.Name))
	}
	if patch.Slug != nil {
		if !thenNowSlugPattern.MatchString(*patch.Slug) {
			handler.WriteError(w, http.StatusBadRequest, "VALIDATION_ERROR", "slug must be kebab-case")
			return
		}
		add("slug", *patch.Slug)
	}
	if patch.County != nil {
		add("county", strings.TrimSpace(*patch.County))
	}
	if patch.Locality != nil {
		add("locality", nullIfEmpty(*patch.Locality))
	}
	if patch.SiteType != nil {
		if !validSiteType(*patch.SiteType) {
			handler.WriteError(w, http.StatusBadRequest, "VALIDATION_ERROR", "invalid siteType")
			return
		}
		add("site_type", *patch.SiteType)
	}
	if patch.AccessMode != nil {
		if !validAccessMode(*patch.AccessMode) {
			handler.WriteError(w, http.StatusBadRequest, "VALIDATION_ERROR", "invalid accessMode")
			return
		}
		add("access_mode", *patch.AccessMode)
	}
	if patch.Sensitivity != nil {
		if !validSensitivity(*patch.Sensitivity) {
			handler.WriteError(w, http.StatusBadRequest, "VALIDATION_ERROR", "invalid sensitivity")
			return
		}
		add("sensitivity", *patch.Sensitivity)
	}
	if patch.ManagingAuthority != nil {
		add("managing_authority", nullIfEmpty(*patch.ManagingAuthority))
	}
	if patch.DestinationID != nil {
		addExpr("destination_id = NULLIF($%d, '')::uuid", strings.TrimSpace(*patch.DestinationID))
	}
	if patch.PhotographyPolicy != nil {
		add("photography_policy", nullIfEmpty(*patch.PhotographyPolicy))
	}
	if patch.CulturalAuthority != nil {
		add("cultural_authority", nullIfEmpty(*patch.CulturalAuthority))
	}
	if patch.ConsentRecord != nil {
		add("consent_record", nullIfEmpty(*patch.ConsentRecord))
	}
	if patch.ConsentDate != nil {
		add("consent_date", parseOptionalDate(*patch.ConsentDate))
	}
	if patch.SiteRules != nil {
		add("site_rules", nullIfEmpty(*patch.SiteRules))
	}
	if patch.SafetyNotes != nil {
		add("safety_notes", nullIfEmpty(*patch.SafetyNotes))
	}
	if patch.ShortDescription != nil {
		add("short_description", nullIfEmpty(*patch.ShortDescription))
	}
	if patch.FullDescription != nil {
		add("full_description", nullIfEmpty(*patch.FullDescription))
	}
	if patch.Sources != nil {
		add("sources", nullIfEmpty(*patch.Sources))
	}
	if patch.FactCheck != nil {
		add("fact_check", nullIfEmpty(*patch.FactCheck))
	}
	if patch.PublicRightsLine != nil {
		add("public_rights_line", nullIfEmpty(*patch.PublicRightsLine))
	}
	if patch.Latitude != nil {
		add("latitude", *patch.Latitude)
	}
	if patch.Longitude != nil {
		add("longitude", *patch.Longitude)
	}
	if patch.GeofenceRadiusM != nil {
		if *patch.GeofenceRadiusM < 10 || *patch.GeofenceRadiusM > 5000 {
			handler.WriteError(w, http.StatusBadRequest, "VALIDATION_ERROR", "invalid geofenceRadiusM")
			return
		}
		add("geofence_radius_m", *patch.GeofenceRadiusM)
	}
	if patch.StandingPointNote != nil {
		add("standing_point_note", nullIfEmpty(*patch.StandingPointNote))
	}
	if patch.ImageKey != nil {
		add("image_key", nullIfEmpty(*patch.ImageKey))
	}
	where := "id = $1"
	if expected != "" {
		args = append(args, expected)
		where += fmt.Sprintf(" AND updated_at = ((($%d)::timestamptz) AT TIME ZONE 'UTC')", len(args))
	}
	tag, err := h.pool.Exec(r.Context(), fmt.Sprintf("UPDATE heritage_sites SET %s WHERE %s", strings.Join(sets, ", "), where), args...)
	if err != nil {
		if isUniqueViolation(err) {
			handler.WriteError(w, http.StatusConflict, "SLUG_EXISTS", "a site with that slug already exists")
			return
		}
		handler.WriteServerError(w, r, "INTERNAL_ERROR", "failed to update heritage site", err)
		return
	}
	if expected != "" && tag.RowsAffected() == 0 {
		// The record moved on after the caller loaded it. Return the current
		// server copy so the client can reload and merge instead of overwriting.
		current, loadErr := h.loadSite(r.Context(), id)
		if loadErr != nil {
			handler.WriteError(w, http.StatusNotFound, "NOT_FOUND", "heritage site not found")
			return
		}
		writeThenNowJSON(w, http.StatusConflict, map[string]any{
			"error": map[string]string{
				"code":    "STALE_SITE",
				"message": "this site changed after it was loaded; reload before saving",
			},
			"site": current,
		})
		return
	}
	h.audit(r.Context(), "site", id, "save", middleware.UserID(r.Context()), "Site saved", nil)
	site, err := h.loadSite(r.Context(), id)
	if err != nil {
		handler.WriteServerError(w, r, "INTERNAL_ERROR", "failed to load heritage site", err)
		return
	}
	writeThenNowJSON(w, http.StatusOK, site)
}

func (h *ThenNowHandler) PublishSite(w http.ResponseWriter, r *http.Request) {
	role := middleware.RoleFromCtx(r.Context())
	if !middleware.CanThenNowPublish(role) {
		handler.WriteError(w, http.StatusForbidden, "FORBIDDEN", "publishing access required")
		return
	}
	id := chi.URLParam(r, "id")
	site, err := h.loadSite(r.Context(), id)
	if err != nil {
		handler.WriteError(w, http.StatusNotFound, "NOT_FOUND", "heritage site not found")
		return
	}
	readiness := thennow.SiteReadiness(site)
	if !readiness.ReadyToPublish {
		writeThenNowJSON(w, http.StatusConflict, map[string]any{"error": map[string]string{"code": "NOT_READY", "message": "site readiness blockers prevent publishing"}, "readiness": readiness})
		return
	}
	if _, err := h.pool.Exec(r.Context(), `UPDATE heritage_sites SET status = 'published', updated_at = now(), updated_by = $2 WHERE id = $1`, id, middleware.UserID(r.Context())); err != nil {
		handler.WriteServerError(w, r, "INTERNAL_ERROR", "failed to publish site", err)
		return
	}
	h.audit(r.Context(), "site", id, "publish", middleware.UserID(r.Context()), "Site published", nil)
	updated, _ := h.loadSite(r.Context(), id)
	writeThenNowJSON(w, http.StatusOK, updated)
}

func (h *ThenNowHandler) UnpublishSite(w http.ResponseWriter, r *http.Request) {
	h.setSiteStatus(w, r, "unpublished", "Site unpublished")
}

func (h *ThenNowHandler) ArchiveSite(w http.ResponseWriter, r *http.Request) {
	if !middleware.CanThenNowTakedown(middleware.RoleFromCtx(r.Context())) {
		handler.WriteError(w, http.StatusForbidden, "FORBIDDEN", "super-admin access required")
		return
	}
	h.setSiteStatus(w, r, "archived", "Site archived")
}

func (h *ThenNowHandler) setSiteStatus(w http.ResponseWriter, r *http.Request, status, event string) {
	role := middleware.RoleFromCtx(r.Context())
	if status == "archived" && !middleware.CanThenNowTakedown(role) {
		handler.WriteError(w, http.StatusForbidden, "FORBIDDEN", "super-admin access required")
		return
	}
	if !middleware.CanThenNowPublish(role) && status != "archived" {
		handler.WriteError(w, http.StatusForbidden, "FORBIDDEN", "publishing access required")
		return
	}
	var body struct {
		Reason string `json:"reason"`
	}
	_ = json.NewDecoder(r.Body).Decode(&body)
	reason := strings.TrimSpace(body.Reason)
	id := chi.URLParam(r, "id")
	if _, err := h.pool.Exec(r.Context(), `UPDATE heritage_sites SET status = $2, updated_at = now(), updated_by = $3 WHERE id = $1`, id, status, middleware.UserID(r.Context())); err != nil {
		handler.WriteServerError(w, r, "INTERNAL_ERROR", "failed to change site status", err)
		return
	}
	var metadata any
	if reason != "" {
		metadata = map[string]string{"reason": reason}
	}
	h.audit(r.Context(), "site", id, status, middleware.UserID(r.Context()), event, metadata)
	site, err := h.loadSite(r.Context(), id)
	if err != nil {
		handler.WriteServerError(w, r, "INTERNAL_ERROR", "failed to load heritage site", err)
		return
	}
	writeThenNowJSON(w, http.StatusOK, site)
}

// statusMetadata keeps the operator's optional unpublish/archive/revoke reason
// in the audit event. The dialog requires a reason; the API keeps it optional
// for compatibility and omits the key when nothing was supplied.
func statusMetadata(reason string) any {
	if strings.TrimSpace(reason) == "" {
		return nil
	}
	return map[string]string{"reason": strings.TrimSpace(reason)}
}

func writeThenNowJSON(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(body)
}

func (h *ThenNowHandler) audit(ctx context.Context, entityType, entityID, eventType, actor, summary string, metadata any) {
	var raw []byte
	if metadata != nil {
		raw, _ = json.Marshal(metadata)
	}
	_, _ = h.pool.Exec(ctx, `
		INSERT INTO then_now_audit_events (entity_type, entity_id, event_type, actor_id, summary, metadata)
		VALUES ($1, $2, $3, NULLIF($4, ''), $5, $6)`,
		entityType, entityID, eventType, actor, summary, raw)
}
