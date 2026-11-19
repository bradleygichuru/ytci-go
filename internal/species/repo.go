package species

import (
	"context"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/bradleygichuru/ytci-go/internal/r2"
)

// ErrNotFound is returned when a species, variant or review item is absent.
var ErrNotFound = errors.New("species: not found")

// ErrConflict is returned for a unique-value collision, typically a slug.
var ErrConflict = errors.New("species: conflict")

// ValidationError is a field-level input failure rendered as the frozen
// { "errors": [...] } envelope.
type ValidationError struct {
	Field   string
	Message string
}

func (e *ValidationError) Error() string { return e.Field + ": " + e.Message }

// GateError blocks a publish with an actionable reason (HTTP 409).
type GateError struct {
	Code    string
	Message string
}

func (e *GateError) Error() string { return e.Code + ": " + e.Message }

// Repo owns every species query. It is the only place that touches the
// species tables; handlers translate errors into HTTP.
type Repo struct {
	pool *pgxpool.Pool
	r2   r2.Store
}

func NewRepo(pool *pgxpool.Pool, store r2.Store) *Repo {
	return &Repo{pool: pool, r2: store}
}

// ---------------------------------------------------------------------------
// Public catalogue
// ---------------------------------------------------------------------------

// ListFilter is the public index query.
type ListFilter struct {
	Query     string
	Language  string
	IUCN      string
	CountyGID *int
	Cursor    string
	Limit     int
	// SavedBy restricts the index to one reader's saved species.
	SavedBy string
}

type cursorKey struct {
	Name string `json:"n"`
	Slug string `json:"s"`
}

func encodeCursor(name, slug string) string {
	b, _ := json.Marshal(cursorKey{Name: name, Slug: slug})
	return base64.RawURLEncoding.EncodeToString(b)
}

func decodeCursor(raw string) (cursorKey, error) {
	var k cursorKey
	b, err := base64.RawURLEncoding.DecodeString(raw)
	if err != nil {
		return k, err
	}
	if err := json.Unmarshal(b, &k); err != nil {
		return k, err
	}
	if k.Slug == "" {
		return k, errors.New("empty cursor")
	}
	return k, nil
}

// ListPublic returns published species ordered by scientific name.
func (r *Repo) ListPublic(ctx context.Context, f ListFilter, userID string) ([]Summary, bool, string, error) {
	limit := clampLimit(f.Limit)

	var (
		where = []string{"s.publication_state = 'published'"}
		args  []any
	)
	add := func(clause string, v any) {
		args = append(args, v)
		where = append(where, fmt.Sprintf(clause, len(args)))
	}
	if q := strings.TrimSpace(f.Query); q != "" {
		add("(s.scientific_name ILIKE $%d OR EXISTS (SELECT 1 FROM species_names qn WHERE qn.species_id = s.id AND qn.name ILIKE $%[1]d))", "%"+escapeLike(q)+"%")
	}
	if lang := strings.TrimSpace(f.Language); lang != "" && lang != "en" {
		add(`(EXISTS (SELECT 1 FROM species_content lc WHERE lc.species_id = s.id AND lc.language_tag = $%d AND lc.review_status = 'reviewed')
		   OR EXISTS (SELECT 1 FROM species_names ln WHERE ln.species_id = s.id AND ln.language_tag = $%[1]d AND ln.review_status = 'reviewed'))`, lang)
	}
	if f.IUCN != "" {
		add("s.iucn_category = $%d", f.IUCN)
	}
	if f.CountyGID != nil {
		add(`EXISTS (SELECT 1 FROM species_where_to_see w WHERE w.species_id = s.id
		         AND w.review_status = 'reviewed' AND w.county_gid = $%d)`, *f.CountyGID)
	}
	if f.SavedBy != "" {
		add(`EXISTS (SELECT 1 FROM species_saves sv WHERE sv.species_id = s.id AND sv.user_id = $%d)`, f.SavedBy)
	}
	if f.Cursor != "" {
		k, err := decodeCursor(f.Cursor)
		if err != nil {
			return nil, false, "", &ValidationError{Field: "cursor", Message: "invalid cursor"}
		}
		args = append(args, k.Name, k.Slug)
		where = append(where, fmt.Sprintf("(s.scientific_name, s.slug) > ($%d, $%d)", len(args)-1, len(args)))
	}

	sqlText := `SELECT s.id, s.slug, s.scientific_name, s.taxonomic_status, s.publication_state,
	        s.iucn_category,
	        COALESCE((SELECT n.name FROM species_names n WHERE n.species_id = s.id AND n.language_tag = 'en'
	                  AND n.review_status = 'reviewed' ORDER BY n.preferred DESC, n.created_at LIMIT 1), s.scientific_name) AS name_en,
	        (SELECT n.name FROM species_names n WHERE n.species_id = s.id AND n.language_tag = 'sw'
	                  AND n.review_status = 'reviewed' ORDER BY n.preferred DESC, n.created_at LIMIT 1) AS name_sw,
	        (SELECT m.storage_key FROM species_media m WHERE m.species_id = s.id AND m.media_kind = 'image'
	                  AND m.review_status = 'reviewed' ORDER BY m.created_at LIMIT 1) AS hero_key
	 FROM species s
	 WHERE ` + strings.Join(where, " AND ") + `
	 ORDER BY s.scientific_name, s.slug
	 LIMIT ` + fmt.Sprint(limit+1)

	rows, err := r.pool.Query(ctx, sqlText, args...)
	if err != nil {
		return nil, false, "", err
	}
	defer rows.Close()

	items := make([]Summary, 0, limit)
	ids := make([]string, 0, limit)
	var last Summary
	for rows.Next() {
		var (
			id, scientific, status, state string
			slug                          string
			iucn, nameSw, heroKey         sql.NullString
			nameEn                        string
		)
		if err := rows.Scan(&id, &slug, &scientific, &status, &state, &iucn, &nameEn, &nameSw, &heroKey); err != nil {
			return nil, false, "", err
		}
		last = Summary{
			Slug:             slug,
			ScientificName:   scientific,
			NameEn:           nameEn,
			NameSw:           nullString(nameSw),
			IUCNCategory:     nullString(iucn),
			TaxonomicStatus:  status,
			PublicationState: state,
			HeroMediaURL:     r.mediaURL(ctx, heroKey),
			Languages:        []string{},
		}
		items = append(items, last)
		ids = append(ids, id)
	}
	if err := rows.Err(); err != nil {
		return nil, false, "", err
	}

	hasMore := len(items) > limit
	if hasMore {
		items = items[:limit]
		ids = ids[:limit]
		last = items[len(items)-1]
	}
	if err := r.hydrateSummaries(ctx, items, ids, userID); err != nil {
		return nil, false, "", err
	}
	next := ""
	if hasMore && len(items) > 0 {
		next = encodeCursor(last.ScientificName, last.Slug)
	}
	return items, hasMore, next, nil
}

// ListAdmin returns every species regardless of publication state.
func (r *Repo) ListAdmin(ctx context.Context, state, cursor string, limit int) ([]Summary, bool, string, error) {
	limit = clampLimit(limit)
	args := []any{}
	where := []string{"1=1"}
	if state != "" {
		args = append(args, state)
		where = append(where, fmt.Sprintf("s.publication_state = $%d", len(args)))
	}
	if cursor != "" {
		k, err := decodeCursor(cursor)
		if err != nil {
			return nil, false, "", &ValidationError{Field: "cursor", Message: "invalid cursor"}
		}
		args = append(args, k.Name, k.Slug)
		where = append(where, fmt.Sprintf("(s.scientific_name, s.slug) > ($%d, $%d)", len(args)-1, len(args)))
	}

	rows, err := r.pool.Query(ctx, `
		SELECT s.id, s.slug, s.scientific_name, s.taxonomic_status, s.publication_state, s.iucn_category,
		       COALESCE((SELECT n.name FROM species_names n WHERE n.species_id = s.id AND n.language_tag = 'en'
		                 AND n.review_status = 'reviewed' ORDER BY n.preferred DESC, n.created_at LIMIT 1), s.scientific_name),
		       (SELECT n.name FROM species_names n WHERE n.species_id = s.id AND n.language_tag = 'sw'
		                 AND n.review_status = 'reviewed' ORDER BY n.preferred DESC, n.created_at LIMIT 1),
		       (SELECT m.storage_key FROM species_media m WHERE m.species_id = s.id AND m.media_kind = 'image'
		                 AND m.review_status = 'reviewed' ORDER BY m.created_at LIMIT 1)
		FROM species s
		WHERE `+strings.Join(where, " AND ")+`
		ORDER BY s.scientific_name, s.slug
		LIMIT `+fmt.Sprint(limit+1), args...)
	if err != nil {
		return nil, false, "", err
	}
	defer rows.Close()

	items := make([]Summary, 0, limit)
	ids := make([]string, 0, limit)
	for rows.Next() {
		var (
			id, scientific, status, state, slug string
			iucn, nameSw, heroKey               sql.NullString
			nameEn                              string
		)
		if err := rows.Scan(&id, &slug, &scientific, &status, &state, &iucn, &nameEn, &nameSw, &heroKey); err != nil {
			return nil, false, "", err
		}
		items = append(items, Summary{
			Slug:             slug,
			ScientificName:   scientific,
			NameEn:           nameEn,
			NameSw:           nullString(nameSw),
			IUCNCategory:     nullString(iucn),
			TaxonomicStatus:  status,
			PublicationState: state,
			HeroMediaURL:     r.mediaURL(ctx, heroKey),
			Languages:        []string{},
		})
		ids = append(ids, id)
	}
	if err := rows.Err(); err != nil {
		return nil, false, "", err
	}
	hasMore := len(items) > limit
	next := ""
	if hasMore {
		items = items[:limit]
		ids = ids[:limit]
		last := items[len(items)-1]
		next = encodeCursor(last.ScientificName, last.Slug)
	}
	if err := r.hydrateSummaries(ctx, items, ids, ""); err != nil {
		return nil, false, "", err
	}
	return items, hasMore, next, nil
}

func (r *Repo) hydrateSummaries(ctx context.Context, items []Summary, ids []string, userID string) error {
	if len(ids) == 0 {
		return nil
	}
	langs := map[string][]string{}
	rows, err := r.pool.Query(ctx, `
		SELECT species_id::text, language_tag FROM species_content
		 WHERE review_status = 'reviewed' AND species_id::text = ANY($1)
		UNION
		SELECT species_id::text, language_tag FROM species_names
		 WHERE review_status = 'reviewed' AND species_id::text = ANY($1)`, ids)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var id, lang string
		if err := rows.Scan(&id, &lang); err != nil {
			return err
		}
		langs[id] = append(langs[id], lang)
	}
	if err := rows.Err(); err != nil {
		return err
	}
	saved := map[string]bool{}
	if userID != "" {
		srows, err := r.pool.Query(ctx, `
			SELECT species_id::text FROM species_saves
			 WHERE user_id = $1 AND species_id::text = ANY($2)`, userID, ids)
		if err != nil {
			return err
		}
		for srows.Next() {
			var id string
			if err := srows.Scan(&id); err != nil {
				srows.Close()
				return err
			}
			saved[id] = true
		}
		srows.Close()
	}
	for i := range items {
		items[i].Languages = sortedUnique(langs[ids[i]])
		items[i].Saved = saved[ids[i]]
	}
	return nil
}

// GetPublic loads one published species. A tombstoned record is reported by
// the handler as 410; unpublished as 404.
func (r *Repo) GetPublic(ctx context.Context, slug, requested, userID string) (*Detail, error) {
	return r.loadDetail(ctx, "slug", slug, requested, userID, true)
}

// GetAdmin loads one species with drafts, sources and review state.
func (r *Repo) GetAdmin(ctx context.Context, id string) (*Detail, error) {
	return r.loadDetail(ctx, "id", id, "en", "", false)
}

func (r *Repo) loadDetail(ctx context.Context, key, value, requested, userID string, public bool) (*Detail, error) {
	var (
		id, slug, scientific, rank, status, state            string
		authorship, gbifKey, iucnCat, iucnAssess, redVersion sql.NullString
		statusAsOf                                           sql.NullTime
		contentVersion                                       int
	)
	err := r.pool.QueryRow(ctx, `
		SELECT id, slug, scientific_name, authorship, taxonomic_rank, taxonomic_status,
		       gbif_key, iucn_category, iucn_assessment_id, red_list_version, status_as_of,
		       publication_state, content_version
		FROM species WHERE `+key+` = $1`, value).Scan(
		&id, &slug, &scientific, &authorship, &rank, &status,
		&gbifKey, &iucnCat, &iucnAssess, &redVersion, &statusAsOf, &state, &contentVersion)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}

	d := &Detail{
		ID:                id,
		Authorship:        nullString(authorship),
		Rank:              rank,
		GBIFKey:           nullString(gbifKey),
		RequestedLanguage: requested,
		ResolvedLanguage:  "en",
		ContentVersion:    contentVersion,
		Names:             []Name{},
		Media:             []Media{},
		Audio:             []Audio{},
		Sources:           []Source{},
		WhereToSee:        []WhereToSee{},
		Links:             []Link{},
		Coverage:          []Coverage{},
	}
	d.Slug = slug
	d.ScientificName = scientific
	d.TaxonomicStatus = status
	d.PublicationState = state
	d.IUCNCategory = nullString(iucnCat)
	if iucnCat.Valid {
		d.IUCN = &IUCN{
			Category:       iucnCat.String,
			AssessmentID:   iucnAssess.String,
			RedListVersion: redVersion.String,
			Label:          iucnLabels[iucnCat.String],
		}
		if statusAsOf.Valid {
			d.IUCN.AsOf = statusAsOf.Time.Format("2006-01-02")
		}
	}

	if public && state != "published" {
		return nil, ErrNotFound
	}

	variants, err := r.loadVariants(ctx, id)
	if err != nil {
		return nil, err
	}
	if requested == "" {
		requested = "en"
		d.RequestedLanguage = "en"
	}
	content, resolved, fb := resolveVariant(variants, requested, contentVersion)
	d.Content = content
	d.ResolvedLanguage = resolved
	d.Fallback = fb

	names, err := r.loadNames(ctx, id, public)
	if err != nil {
		return nil, err
	}
	d.Names = names
	d.NameEn, d.NameSw = displayNames(names, scientific)

	media, err := r.loadMedia(ctx, id, public)
	if err != nil {
		return nil, err
	}
	d.Media = media
	if len(media) > 0 {
		d.HeroMediaURL = &media[0].URL
	}

	audio, err := r.loadAudio(ctx, id, public)
	if err != nil {
		return nil, err
	}
	d.Audio = audio

	sources, err := r.loadSources(ctx, id, public)
	if err != nil {
		return nil, err
	}
	d.Sources = sources

	where, err := r.loadWhereToSee(ctx, id, public)
	if err != nil {
		return nil, err
	}
	d.WhereToSee = where

	links, err := r.loadLinks(ctx, id)
	if err != nil {
		return nil, err
	}
	d.Links = links

	coverage, err := r.loadCoverage(ctx, id, variants, requested)
	if err != nil {
		return nil, err
	}
	d.Coverage = coverage
	d.Languages = availableLanguages(coverage)

	if userID != "" {
		var exists bool
		if err := r.pool.QueryRow(ctx, `
			SELECT EXISTS (SELECT 1 FROM species_saves WHERE user_id = $1 AND species_id = $2)`,
			userID, id).Scan(&exists); err != nil {
			return nil, err
		}
		d.Saved = exists
	}
	return d, nil
}

// Tombstoned reports whether the slug is a published rights-withdrawal
// tombstone so the handler can answer 410 without leaking content.
func (r *Repo) Tombstoned(ctx context.Context, slug string) (bool, error) {
	var state string
	err := r.pool.QueryRow(ctx, `SELECT publication_state FROM species WHERE slug = $1`, slug).Scan(&state)
	if errors.Is(err, pgx.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return state == "tombstoned", nil
}

func (r *Repo) loadVariants(ctx context.Context, speciesID string) (map[string]Variant, error) {
	rows, err := r.pool.Query(ctx, `
		SELECT language_tag, coverage_tier, summary, description, habitat, range_text,
		       where_to_see_text, conservation_text, field_status, review_status,
		       source_version, stale_reason, translator_id, reviewer_id, published_at
		FROM species_content WHERE species_id = $1`, speciesID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]Variant{}
	for rows.Next() {
		var (
			v                               Variant
			summary, desc, habitat, rng     sql.NullString
			see, conservation, stale, trans sql.NullString
			reviewer                        sql.NullString
			fieldRaw                        []byte
			published                       sql.NullTime
		)
		if err := rows.Scan(&v.Language, &v.CoverageTier, &summary, &desc, &habitat, &rng,
			&see, &conservation, &fieldRaw, &v.ReviewStatus, &v.SourceVersion,
			&stale, &trans, &reviewer, &published); err != nil {
			return nil, err
		}
		v.Summary = nullString(summary)
		v.Description = nullString(desc)
		v.Habitat = nullString(habitat)
		v.Range = nullString(rng)
		v.WhereToSee = nullString(see)
		v.Conservation = nullString(conservation)
		v.StaleReason = nullString(stale)
		v.TranslatorRef = nullString(trans)
		v.ReviewerRef = nullString(reviewer)
		if published.Valid {
			t := published.Time
			v.PublishedAt = &t
		}
		v.FieldStatus = map[string]string{}
		if len(fieldRaw) > 0 {
			_ = json.Unmarshal(fieldRaw, &v.FieldStatus)
		}
		out[v.Language] = v
	}
	return out, rows.Err()
}

// resolveVariant implements the visible fallback path: requested language then
// English. Swahili is never a silent substitute and a stale translated field
// falls back to English with a reason.
func resolveVariant(variants map[string]Variant, requested string, contentVersion int) (*Variant, string, *Fallback) {
	requested = normaliseLanguage(requested)
	candidate, ok := variants[requested]
	if requested == "en" || !ok {
		if requested != "en" && !ok {
			if en, hasEN := variants["en"]; hasEN {
				en := en
				return &en, "en", &Fallback{Used: true, Reason: "missing", Chip: "English"}
			}
		}
		if en, hasEN := variants["en"]; hasEN {
			en := en
			return &en, "en", nil
		}
		return nil, requested, nil
	}

	reason := ""
	switch {
	case candidate.ReviewStatus == "stale" || candidate.SourceVersion < contentVersion:
		reason = "stale"
	case candidate.ReviewStatus == "missing":
		reason = "missing"
	case candidate.ReviewStatus != "reviewed":
		reason = "not_reviewed"
	case candidate.PublishedAt == nil:
		reason = "unpublished"
	}
	if reason == "" {
		c := candidate
		return &c, requested, nil
	}
	if en, hasEN := variants["en"]; hasEN {
		en := en
		return &en, "en", &Fallback{Used: true, Reason: reason, Chip: "English"}
	}
	c := candidate
	return &c, requested, nil
}

func (r *Repo) loadNames(ctx context.Context, speciesID string, public bool) ([]Name, error) {
	filter := ""
	if public {
		filter = " AND n.review_status = 'reviewed'"
	}
	rows, err := r.pool.Query(ctx, `
		SELECT n.language_tag, n.script, n.name, n.name_type, n.region, n.county_gid,
		       n.review_status, n.preferred,
		       src.id, src.publisher, src.citation, src.url, src.accessed_at, src.licence_code,
		       src.licence_url, src.permitted_use, src.attribution, src.snapshot_hash, src.review_status
		FROM species_names n
		LEFT JOIN species_sources src ON src.id = n.source_id
		WHERE n.species_id = $1`+filter+`
		ORDER BY n.language_tag, n.preferred DESC, n.created_at`, speciesID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Name{}
	for rows.Next() {
		var (
			n      Name
			region sql.NullString
			county sql.NullInt64
			src    sourceScan
		)
		if err := rows.Scan(&n.Language, &n.Script, &n.Name, &n.NameType, &region, &county,
			&n.ReviewStatus, &n.Preferred, &src.ID, &src.Publisher, &src.Citation, &src.URL,
			&src.AccessedAt, &src.LicenceCode, &src.LicenceURL, &src.PermittedUse,
			&src.Attribution, &src.SnapshotHash, &src.ReviewStatus); err != nil {
			return nil, err
		}
		n.Region = nullString(region)
		if county.Valid {
			v := int(county.Int64)
			n.CountyGID = &v
		}
		n.Source = src.toSource()
		out = append(out, n)
	}
	return out, rows.Err()
}

func (r *Repo) loadMedia(ctx context.Context, speciesID string, public bool) ([]Media, error) {
	filter := ""
	if public {
		filter = ` AND m.review_status = 'reviewed'
		           AND (rt.id IS NULL OR (rt.withdrawn_at IS NULL AND (rt.expires_at IS NULL OR rt.expires_at > now())))`
	}
	rows, err := r.pool.Query(ctx, `
		SELECT m.id, m.media_kind, m.storage_key, m.width, m.height, m.credit, m.rights_holder,
		       m.licence_code, m.licence_url, m.sensitive_location
		FROM species_media m
		LEFT JOIN species_rights rt ON rt.id = m.rights_id
		WHERE m.species_id = $1`+filter+`
		ORDER BY m.created_at`, speciesID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Media{}
	ids := []string{}
	byID := map[string]int{}
	for rows.Next() {
		var (
			m             Media
			width, height sql.NullInt64
			licenceURL    sql.NullString
			key           string
		)
		if err := rows.Scan(&m.ID, &m.MediaKind, &key, &width, &height, &m.Credit,
			&m.RightsHolder, &m.LicenceCode, &licenceURL, &m.SensitiveLocation); err != nil {
			return nil, err
		}
		if width.Valid {
			v := int(width.Int64)
			m.Width = &v
		}
		if height.Valid {
			v := int(height.Int64)
			m.Height = &v
		}
		m.LicenceURL = nullString(licenceURL)
		m.URL = r.mediaURLString(ctx, key)
		m.Alt = map[string]string{}
		byID[m.ID] = len(out)
		ids = append(ids, m.ID)
		out = append(out, m)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if len(ids) == 0 {
		return out, nil
	}
	altFilter := ""
	if public {
		altFilter = " AND review_status = 'reviewed'"
	}
	arows, err := r.pool.Query(ctx, `
		SELECT media_id::text, language_tag, alt_text FROM species_media_alt
		 WHERE media_id::text = ANY($1)`+altFilter, ids)
	if err != nil {
		return nil, err
	}
	defer arows.Close()
	for arows.Next() {
		var id, lang, text string
		if err := arows.Scan(&id, &lang, &text); err != nil {
			return nil, err
		}
		if i, ok := byID[id]; ok {
			out[i].Alt[lang] = text
		}
	}
	return out, arows.Err()
}

func (r *Repo) loadAudio(ctx context.Context, speciesID string, public bool) ([]Audio, error) {
	filter := ""
	if public {
		filter = ` AND a.review_status = 'reviewed'
		           AND (rt.id IS NULL OR (rt.withdrawn_at IS NULL AND (rt.expires_at IS NULL OR rt.expires_at > now())))`
	}
	rows, err := r.pool.Query(ctx, `
		SELECT a.language_tag, a.storage_key, a.narrator, a.transcript, a.duration_seconds,
		       a.review_status,
		       rt.id, rt.subject_type, rt.subject_id::text, rt.rights_holder, rt.licence_code,
		       rt.licence_url, rt.permitted_use, rt.attribution, rt.consent_record,
		       rt.expires_at, rt.withdrawn_at
		FROM species_audio a
		LEFT JOIN species_rights rt ON rt.id = a.rights_id
		WHERE a.species_id = $1`+filter+`
		ORDER BY a.language_tag`, speciesID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Audio{}
	for rows.Next() {
		var (
			a   Audio
			key string
			rt  rightsScan
		)
		if err := rows.Scan(&a.Language, &key, &a.Narrator, &a.Transcript,
			&a.DurationSeconds, &a.ReviewStatus,
			&rt.ID, &rt.SubjectType, &rt.SubjectID, &rt.RightsHolder, &rt.LicenceCode,
			&rt.LicenceURL, &rt.PermittedUse, &rt.Attribution, &rt.ConsentRecord,
			&rt.ExpiresAt, &rt.WithdrawnAt); err != nil {
			return nil, err
		}
		a.URL = r.mediaURLString(ctx, key)
		a.Rights = rt.toRights()
		out = append(out, a)
	}
	return out, rows.Err()
}

func (r *Repo) loadSources(ctx context.Context, speciesID string, public bool) ([]Source, error) {
	filter := ""
	if public {
		filter = " AND review_status = 'reviewed'"
	}
	rows, err := r.pool.Query(ctx, `
		SELECT id, publisher, citation, url, accessed_at, licence_code, licence_url,
		       permitted_use, attribution, snapshot_hash, review_status
		FROM species_sources WHERE species_id = $1`+filter+`
		ORDER BY created_at`, speciesID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Source{}
	for rows.Next() {
		var src sourceScan
		if err := rows.Scan(&src.ID, &src.Publisher, &src.Citation, &src.URL, &src.AccessedAt,
			&src.LicenceCode, &src.LicenceURL, &src.PermittedUse, &src.Attribution,
			&src.SnapshotHash, &src.ReviewStatus); err != nil {
			return nil, err
		}
		if s := src.toSource(); s != nil {
			out = append(out, *s)
		}
	}
	return out, rows.Err()
}

func (r *Repo) loadWhereToSee(ctx context.Context, speciesID string, public bool) ([]WhereToSee, error) {
	filter := ""
	if public {
		filter = " AND w.review_status = 'reviewed'"
	}
	rows, err := r.pool.Query(ctx, `
		SELECT w.destination_slug, w.county_gid, kc.adm1_name, w.protected_area,
		       w.evidence_type, w.confidence, w.review_status,
		       src.id, src.publisher, src.citation, src.url, src.accessed_at, src.licence_code,
		       src.licence_url, src.permitted_use, src.attribution, src.snapshot_hash, src.review_status
		FROM species_where_to_see w
		LEFT JOIN kenya_counties kc ON kc.gid = w.county_gid
		LEFT JOIN species_sources src ON src.id = w.source_id
		WHERE w.species_id = $1`+filter+`
		ORDER BY w.created_at`, speciesID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []WhereToSee{}
	for rows.Next() {
		var (
			w                      WhereToSee
			dest, countyName, area sql.NullString
			county                 sql.NullInt64
			src                    sourceScan
		)
		if err := rows.Scan(&dest, &county, &countyName, &area, &w.EvidenceType,
			&w.Confidence, &w.ReviewStatus, &src.ID, &src.Publisher, &src.Citation, &src.URL,
			&src.AccessedAt, &src.LicenceCode, &src.LicenceURL, &src.PermittedUse,
			&src.Attribution, &src.SnapshotHash, &src.ReviewStatus); err != nil {
			return nil, err
		}
		w.DestinationSlug = nullString(dest)
		w.County = nullString(countyName)
		w.ProtectedArea = nullString(area)
		if county.Valid {
			v := int(county.Int64)
			w.CountyGID = &v
		}
		w.Source = src.toSource()
		w.Copy = whereCopy(w.ProtectedArea, w.County, w.DestinationSlug)
		out = append(out, w)
	}
	return out, rows.Err()
}

func (r *Repo) loadLinks(ctx context.Context, speciesID string) ([]Link, error) {
	rows, err := r.pool.Query(ctx, `
		SELECT l.target_type, l.target_id,
		       COALESCE(d.name, c.title, '') AS title
		FROM species_links l
		LEFT JOIN destinations d ON l.target_type = 'destination' AND d.slug = l.target_id
		LEFT JOIN courses c ON l.target_type = 'course' AND c.id::text = l.target_id
		WHERE l.species_id = $1
		ORDER BY l.created_at`, speciesID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Link{}
	for rows.Next() {
		var l Link
		if err := rows.Scan(&l.TargetType, &l.TargetID, &l.Title); err != nil {
			return nil, err
		}
		l.Href = linkHref(l.TargetType, l.TargetID)
		out = append(out, l)
	}
	return out, rows.Err()
}

func (r *Repo) loadCoverage(ctx context.Context, speciesID string, variants map[string]Variant, requested string) ([]Coverage, error) {
	rows, err := r.pool.Query(ctx, `
		SELECT tag, COALESCE(native_name, english_name), coverage_tier
		FROM content_languages WHERE status = 'active'
		ORDER BY launch_phase, tag`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	type langRow struct{ tag, name, tier string }
	langs := []langRow{}
	for rows.Next() {
		var l langRow
		if err := rows.Scan(&l.tag, &l.name, &l.tier); err != nil {
			return nil, err
		}
		langs = append(langs, l)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	reviewedNames := map[string]int{}
	nrows, err := r.pool.Query(ctx, `
		SELECT language_tag, count(*) FROM species_names
		 WHERE species_id = $1 AND review_status = 'reviewed'
		 GROUP BY language_tag`, speciesID)
	if err != nil {
		return nil, err
	}
	for nrows.Next() {
		var lang string
		var count int
		if err := nrows.Scan(&lang, &count); err != nil {
			nrows.Close()
			return nil, err
		}
		reviewedNames[lang] = count
	}
	nrows.Close()

	audioReview, err := r.reviewedAudioLanguages(ctx, speciesID)
	if err != nil {
		return nil, err
	}

	out := []Coverage{}
	seen := map[string]bool{}
	addLang := func(tag, name, tier string) {
		seen[tag] = true
		v, has := variants[tag]
		available := has && v.ReviewStatus == "reviewed"
		cov := Coverage{
			Language:      tag,
			Name:          name,
			Tier:          tier,
			Available:     available || reviewedNames[tag] > 0,
			ReviewedNames: reviewedNames[tag],
			Audio:         audioReview[tag],
		}
		if has {
			cov.Tier = v.CoverageTier
		}
		cov.Fallback = tag != "en" && !available
		out = append(out, cov)
	}
	for _, l := range langs {
		addLang(l.tag, l.name, l.tier)
	}
	if requested != "" && !seen[requested] {
		label, err := r.languageName(ctx, requested)
		if err != nil {
			return nil, err
		}
		addLang(requested, label, "names")
	}
	return out, nil
}

func (r *Repo) reviewedAudioLanguages(ctx context.Context, speciesID string) (map[string]bool, error) {
	rows, err := r.pool.Query(ctx, `
		SELECT DISTINCT language_tag FROM species_audio
		 WHERE species_id = $1 AND review_status = 'reviewed'`, speciesID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]bool{}
	for rows.Next() {
		var lang string
		if err := rows.Scan(&lang); err != nil {
			return nil, err
		}
		out[lang] = true
	}
	return out, rows.Err()
}

func (r *Repo) languageName(ctx context.Context, tag string) (string, error) {
	var name string
	err := r.pool.QueryRow(ctx, `
		SELECT COALESCE(native_name, english_name) FROM content_languages WHERE tag = $1`, tag).Scan(&name)
	if errors.Is(err, pgx.ErrNoRows) {
		return tag, nil
	}
	return name, err
}

// Languages returns reviewed coverage for one published slug.
func (r *Repo) Languages(ctx context.Context, slug string) ([]Coverage, error) {
	d, err := r.loadDetail(ctx, "slug", slug, "en", "", true)
	if err != nil {
		return nil, err
	}
	return d.Coverage, nil
}
