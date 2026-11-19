package species

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/bradleygichuru/ytci-go/internal/model"
)

var (
	slugPattern = regexp.MustCompile(`[^a-z0-9]+`)
	validIUCN   = map[string]bool{"CR": true, "EN": true, "VU": true, "NT": true, "LC": true, "DD": true, "NE": true}
	validTiers  = map[string]bool{"full": true, "summary": true, "names": true, "audio": true}
	validFields = map[string]bool{"missing": true, "draft": true, "reviewed": true, "stale": true}
	validUses   = map[string]bool{"taxonomy_only": true, "occurrence": true, "prose": true, "media": true, "audio": true, "link_only": true}
)

// CreateSpecies inserts a draft and returns the full admin record.
func (r *Repo) CreateSpecies(ctx context.Context, actor string, w SpeciesWrite) (*Detail, error) {
	if strings.TrimSpace(w.ScientificName) == "" {
		return nil, &ValidationError{Field: "scientificName", Message: "scientificName is required"}
	}
	if err := validateSpeciesWrite(w, true); err != nil {
		return nil, err
	}
	slug := ""
	if w.Slug != nil && strings.TrimSpace(*w.Slug) != "" {
		slug = slugify(*w.Slug)
	} else {
		slug = slugify(w.ScientificName)
	}
	status := "accepted"
	if w.TaxonomicStatus != nil {
		status = *w.TaxonomicStatus
	}
	rank := "species"
	if w.TaxonomicRank != nil {
		rank = *w.TaxonomicRank
	}

	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx)

	var id string
	err = tx.QueryRow(ctx, `
		INSERT INTO species (slug, scientific_name, authorship, taxonomic_rank, taxonomic_status,
			accepted_species_id, gbif_key, iucn_taxon_id, wikidata_qid, iucn_category,
			iucn_assessment_id, red_list_version, status_as_of, created_by)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14)
		RETURNING id`,
		slug, w.ScientificName, nullableText(w.Authorship), rank, status, nullableText(w.AcceptedSpeciesID),
		nullableText(w.GBIFKey), nullableText(w.IUCNTaxonID), nullableText(w.WikidataQID), nullableText(w.IUCNCategory),
		nullableText(w.IUCNAssessmentID), nullableText(w.RedListVersion), dateOrNil(w.StatusAsOf), actor).Scan(&id)
	if err != nil {
		if isUniqueViolation(err) {
			return nil, ErrConflict
		}
		return nil, err
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, err
	}
	return r.GetAdmin(ctx, id)
}

// UpdateSpecies applies the frozen SpeciesWrite fields. A taxonomy or status
// change bumps the content version and stales every translation.
func (r *Repo) UpdateSpecies(ctx context.Context, id string, w SpeciesWrite) (*Detail, error) {
	current, err := r.GetAdmin(ctx, id)
	if err != nil {
		return nil, err
	}
	if w.ScientificName == "" {
		w.ScientificName = current.ScientificName
	}
	w.Slug = nil
	if err := validateSpeciesWrite(w, false); err != nil {
		return nil, err
	}

	// Merge: a missing optional field keeps the stored value.
	authorship := current.Authorship
	if w.Authorship != nil {
		authorship = w.Authorship
	}
	rank := current.Rank
	if w.TaxonomicRank != nil {
		rank = *w.TaxonomicRank
	}
	status := current.TaxonomicStatus
	if w.TaxonomicStatus != nil {
		status = *w.TaxonomicStatus
	}
	gbifKey := current.GBIFKey
	if w.GBIFKey != nil {
		gbifKey = w.GBIFKey
	}
	iucnCat := current.IUCNCategory
	if w.IUCNCategory != nil {
		iucnCat = w.IUCNCategory
	}
	assessment, redVersion, asOf := "", "", ""
	if current.IUCN != nil {
		assessment, redVersion, asOf = current.IUCN.AssessmentID, current.IUCN.RedListVersion, current.IUCN.AsOf
	}
	if w.IUCNAssessmentID != nil {
		assessment = *w.IUCNAssessmentID
	}
	if w.RedListVersion != nil {
		redVersion = *w.RedListVersion
	}
	if w.StatusAsOf != nil {
		asOf = *w.StatusAsOf
	}
	if iucnCat != nil && *iucnCat != "" && (assessment == "" || asOf == "") {
		return nil, &ValidationError{Field: "iucnAssessmentId", Message: "IUCN category requires assessmentId and statusAsOf"}
	}

	contentChanged := w.ScientificName != current.ScientificName ||
		diverged(status, current.TaxonomicStatus) ||
		divergedPtr(gbifKey, current.GBIFKey) ||
		divergedPtr(iucnCat, current.IUCNCategory) ||
		w.IUCNAssessmentID != nil || w.RedListVersion != nil || w.StatusAsOf != nil

	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx)

	_, err = tx.Exec(ctx, `
		UPDATE species SET scientific_name = $2, authorship = $3, taxonomic_rank = $4,
		       taxonomic_status = $5, accepted_species_id = COALESCE($6, accepted_species_id),
		       gbif_key = $7, iucn_taxon_id = COALESCE($8, iucn_taxon_id),
		       wikidata_qid = COALESCE($9, wikidata_qid), iucn_category = $10,
		       iucn_assessment_id = $11, red_list_version = $12, status_as_of = $13,
		       updated_at = now()
		WHERE id = $1`,
		id, w.ScientificName, nullableText(authorship), rank, status, nullableText(w.AcceptedSpeciesID),
		nullableText(gbifKey), nullableText(w.IUCNTaxonID), nullableText(w.WikidataQID), nullableText(iucnCat), assessment,
		redVersion, dateOrNil(ptrOrNil(asOf)))
	if err != nil {
		return nil, err
	}
	if contentChanged {
		if err := bumpContentVersion(ctx, tx, id, "source_changed"); err != nil {
			return nil, err
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, err
	}
	return r.GetAdmin(ctx, id)
}

// UpsertVariant writes one language document and opens the right review gate.
func (r *Repo) UpsertVariant(ctx context.Context, speciesID, actor string, w VariantWrite) (*Variant, error) {
	language := normaliseLanguage(w.Language)
	if !validTiers[w.CoverageTier] {
		return nil, &ValidationError{Field: "coverageTier", Message: "coverageTier must be full, summary, names or audio"}
	}
	if err := r.requireLanguage(ctx, language); err != nil {
		return nil, err
	}
	if err := validateFieldStatus(w.FieldStatus); err != nil {
		return nil, err
	}
	var contentVersion int
	if err := r.pool.QueryRow(ctx, `SELECT content_version FROM species WHERE id = $1`, speciesID).Scan(&contentVersion); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	sourceVersion := contentVersion
	if w.SourceVersion != nil {
		sourceVersion = *w.SourceVersion
	}
	translator := sql.NullString{}
	if language != "en" {
		ref := actor
		if w.TranslatorRef != nil && *w.TranslatorRef != "" {
			ref = *w.TranslatorRef
		}
		translator = sql.NullString{String: ref, Valid: true}
	} else if w.TranslatorRef != nil {
		translator = sql.NullString{String: *w.TranslatorRef, Valid: true}
	}

	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx)

	fieldStatus := w.FieldStatus
	if fieldStatus == nil {
		fieldStatus = map[string]string{}
	}
	raw, _ := json.Marshal(fieldStatus)

	var id string
	err = tx.QueryRow(ctx, `
		INSERT INTO species_content (species_id, language_tag, coverage_tier, summary, description,
			habitat, range_text, where_to_see_text, conservation_text, field_status, source_version,
			translator_id, review_status, stale_reason, published_at)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,'draft',NULL,NULL)
		ON CONFLICT (species_id, language_tag) DO UPDATE SET
			coverage_tier = EXCLUDED.coverage_tier,
			summary = EXCLUDED.summary,
			description = EXCLUDED.description,
			habitat = EXCLUDED.habitat,
			range_text = EXCLUDED.range_text,
			where_to_see_text = EXCLUDED.where_to_see_text,
			conservation_text = EXCLUDED.conservation_text,
			field_status = EXCLUDED.field_status,
			source_version = EXCLUDED.source_version,
			translator_id = EXCLUDED.translator_id,
			reviewer_id = NULL,
			review_status = 'draft',
			stale_reason = NULL,
			published_at = NULL,
			updated_at = now()
		RETURNING id`,
		speciesID, language, w.CoverageTier, w.Summary, w.Description, w.Habitat,
		w.Range, w.WhereToSee, w.Conservation, raw, sourceVersion, translator).Scan(&id)
	if err != nil {
		return nil, err
	}

	stage := "language"
	if language == "en" {
		stage = "expert"
	}
	if err := enqueueReview(ctx, tx, speciesID, "content", id, language, stage, translator); err != nil {
		return nil, err
	}
	if _, err := tx.Exec(ctx, `UPDATE species SET updated_at = now() WHERE id = $1`, speciesID); err != nil {
		return nil, err
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, err
	}
	v, err := r.loadVariants(ctx, speciesID)
	if err != nil {
		return nil, err
	}
	out := v[language]
	return &out, nil
}

// UpsertName inserts one sourced name and opens expert review for it.
func (r *Repo) UpsertName(ctx context.Context, speciesID string, w NameWrite) (*Name, error) {
	language := normaliseLanguage(w.Language)
	if strings.TrimSpace(w.Name) == "" {
		return nil, &ValidationError{Field: "name", Message: "name is required"}
	}
	if err := r.requireLanguage(ctx, language); err != nil {
		return nil, err
	}
	nameType := w.NameType
	if nameType == "" {
		nameType = "common"
	}
	if nameType != "common" && nameType != "local" && nameType != "scientific" && nameType != "alternate" {
		return nil, &ValidationError{Field: "nameType", Message: "invalid nameType"}
	}
	if w.CountyGID != nil && (w.Region == nil || *w.Region == "") {
		return nil, &ValidationError{Field: "region", Message: "region is required with countyGid"}
	}
	// A local or common name must carry a source. There is no unsourced folk name.
	if w.SourceID == nil || *w.SourceID == "" {
		if nameType != "scientific" {
			return nil, &ValidationError{Field: "sourceId", Message: "a source is required for every non-scientific name"}
		}
	}
	script := w.Script
	if script == "" {
		script = "Latn"
	}
	preferred := w.Preferred != nil && *w.Preferred

	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx)

	var id string
	err = tx.QueryRow(ctx, `
		INSERT INTO species_names (species_id, language_tag, script, name, name_type, region,
			county_gid, source_id, review_status, preferred)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,'draft',$9)
		RETURNING id`,
		speciesID, language, script, w.Name, nameType, nullableText(w.Region), nullableInt(w.CountyGID), nullableText(w.SourceID), preferred).Scan(&id)
	if err != nil {
		if isUniqueViolation(err) {
			return nil, ErrConflict
		}
		return nil, err
	}
	if err := enqueueReview(ctx, tx, speciesID, "name", id, language, "expert", sql.NullString{}); err != nil {
		return nil, err
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, err
	}

	var n Name
	var region sql.NullString
	var county sql.NullInt64
	err = r.pool.QueryRow(ctx, `
		SELECT language_tag, script, name, name_type, region, county_gid, review_status, preferred
		FROM species_names WHERE id = $1`, id).Scan(
		&n.Language, &n.Script, &n.Name, &n.NameType, &region, &county, &n.ReviewStatus, &n.Preferred)
	if err != nil {
		return nil, err
	}
	n.Region = nullString(region)
	n.CountyGID = nullInt(county)
	return &n, nil
}

// CreateMedia registers an asset. Uploader provenance is recorded so the
// rights reviewer can be checked against it.
func (r *Repo) CreateMedia(ctx context.Context, speciesID, actor string, w MediaWrite) (*Media, error) {
	if w.MediaKind == "" {
		w.MediaKind = "image"
	}
	if w.MediaKind != "image" && w.MediaKind != "video" && w.MediaKind != "map" {
		return nil, &ValidationError{Field: "mediaKind", Message: "invalid mediaKind"}
	}
	if strings.TrimSpace(w.StorageKey) == "" {
		return nil, &ValidationError{Field: "storageKey", Message: "storageKey is required"}
	}
	if strings.HasPrefix(w.StorageKey, "http") {
		return nil, &ValidationError{Field: "storageKey", Message: "hotlinking is not allowed; upload a managed object"}
	}
	if w.Credit == "" || w.RightsHolder == "" || w.LicenceCode == "" {
		return nil, &ValidationError{Field: "licenceCode", Message: "credit, rightsHolder and licenceCode are required"}
	}
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx)

	var id string
	err = tx.QueryRow(ctx, `
		INSERT INTO species_media (species_id, media_kind, storage_key, width, height, rights_id,
			credit, rights_holder, licence_code, licence_url, sensitive_location, created_by)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12)
		RETURNING id`,
		speciesID, w.MediaKind, w.StorageKey, nullableInt(w.Width), nullableInt(w.Height), nullableText(w.RightsID), w.Credit,
		w.RightsHolder, w.LicenceCode, nullableText(w.LicenceURL), w.SensitiveLocation, actor).Scan(&id)
	if err != nil {
		return nil, err
	}
	for lang, text := range w.Alt {
		lang = normaliseLanguage(lang)
		if err := r.requireLanguage(ctx, lang); err != nil {
			return nil, err
		}
		if _, err := tx.Exec(ctx, `
			INSERT INTO species_media_alt (media_id, language_tag, alt_text, review_status)
			VALUES ($1,$2,$3,'draft')`, id, lang, text); err != nil {
			return nil, err
		}
	}
	if err := enqueueReview(ctx, tx, speciesID, "media", id, "", "rights", sql.NullString{}); err != nil {
		return nil, err
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, err
	}
	media, err := r.loadMedia(ctx, speciesID, false)
	if err != nil {
		return nil, err
	}
	for i := range media {
		if media[i].ID == id {
			return &media[i], nil
		}
	}
	return nil, ErrNotFound
}

// CreateAudio registers a per-language track with narrator, transcript and
// rights. Synthetic narration is out of scope for v1.
func (r *Repo) CreateAudio(ctx context.Context, speciesID, actor string, w AudioWrite) (*Audio, error) {
	language := normaliseLanguage(w.Language)
	if err := r.requireLanguage(ctx, language); err != nil {
		return nil, err
	}
	if w.StorageKey == "" || strings.HasPrefix(w.StorageKey, "http") {
		return nil, &ValidationError{Field: "storageKey", Message: "a managed storageKey is required"}
	}
	if w.Narrator == "" || w.Transcript == "" {
		return nil, &ValidationError{Field: "transcript", Message: "narrator and transcript are required"}
	}
	if w.DurationSeconds <= 0 {
		return nil, &ValidationError{Field: "durationSeconds", Message: "durationSeconds must be positive"}
	}
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx)

	var id string
	err = tx.QueryRow(ctx, `
		INSERT INTO species_audio (species_id, language_tag, storage_key, narrator, transcript,
			duration_seconds, rights_id, created_by)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8)
		RETURNING id`,
		speciesID, language, w.StorageKey, w.Narrator, w.Transcript, w.DurationSeconds,
		nullableText(w.RightsID), actor).Scan(&id)
	if err != nil {
		return nil, err
	}
	if err := enqueueReview(ctx, tx, speciesID, "audio", id, language, "rights", sql.NullString{}); err != nil {
		return nil, err
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, err
	}
	audio, err := r.loadAudio(ctx, speciesID, false)
	if err != nil {
		return nil, err
	}
	for i := range audio {
		if audio[i].Language == language && audio[i].URL == r.mediaURLString(ctx, w.StorageKey) {
			return &audio[i], nil
		}
	}
	if len(audio) > 0 {
		return &audio[len(audio)-1], nil
	}
	return nil, ErrNotFound
}

// CreateSource adds a citation. A second source for an already-published
// species is a re-check: it bumps the content version and stales dependants.
func (r *Repo) CreateSource(ctx context.Context, w SourceWrite) (*Source, error) {
	if err := validateSourceWrite(w); err != nil {
		return nil, err
	}
	accessed, err := time.Parse("2006-01-02", w.AccessedAt)
	if err != nil {
		return nil, &ValidationError{Field: "accessedAt", Message: "accessedAt must be YYYY-MM-DD"}
	}

	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx)

	var prior int
	if w.SpeciesID != nil && *w.SpeciesID != "" {
		if err := tx.QueryRow(ctx, `SELECT count(*) FROM species_sources WHERE species_id = $1`, *w.SpeciesID).Scan(&prior); err != nil {
			return nil, err
		}
	}
	var id string
	err = tx.QueryRow(ctx, `
		INSERT INTO species_sources (species_id, publisher, citation, url, accessed_at, licence_code,
			licence_url, permitted_use, attribution, snapshot_hash)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10)
		RETURNING id`,
		nullableText(w.SpeciesID), w.Publisher, w.Citation, nullableText(w.URL), accessed, w.LicenceCode, nullableText(w.LicenceURL),
		w.PermittedUse, w.Attribution, nullableText(w.SnapshotHash)).Scan(&id)
	if err != nil {
		return nil, err
	}
	if prior > 0 && w.SpeciesID != nil {
		if err := bumpContentVersion(ctx, tx, *w.SpeciesID, "source_changed"); err != nil {
			return nil, err
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, err
	}
	s := Source{
		ID:           id,
		Publisher:    w.Publisher,
		Citation:     w.Citation,
		URL:          w.URL,
		AccessedAt:   accessed.Format("2006-01-02"),
		LicenceCode:  w.LicenceCode,
		LicenceURL:   w.LicenceURL,
		PermittedUse: w.PermittedUse,
		Attribution:  w.Attribution,
		SnapshotHash: w.SnapshotHash,
		ReviewStatus: "reviewed",
	}
	return &s, nil
}

// CreateRights records licensing and consent for one media or audio asset.
func (r *Repo) CreateRights(ctx context.Context, actor string, w RightsWrite) (*Rights, error) {
	if w.SubjectType != "media" && w.SubjectType != "audio" {
		return nil, &ValidationError{Field: "subjectType", Message: "subjectType must be media or audio"}
	}
	if w.SubjectID == "" || w.RightsHolder == "" || w.LicenceCode == "" || w.Attribution == "" {
		return nil, &ValidationError{Field: "subjectId", Message: "subjectId, rightsHolder, licenceCode and attribution are required"}
	}
	if strings.Contains(strings.ToUpper(w.LicenceCode), "NC") {
		return nil, &ValidationError{Field: "licenceCode", Message: "non-commercial licences are not publishable"}
	}
	if isKWS(w.RightsHolder) && (w.ConsentRecord == nil || strings.TrimSpace(*w.ConsentRecord) == "") {
		return nil, &ValidationError{Field: "consentRecord", Message: "KWS rights require a recorded written permission reference"}
	}
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx)

	var id string
	err = tx.QueryRow(ctx, `
		INSERT INTO species_rights (subject_type, subject_id, rights_holder, licence_code,
			licence_url, permitted_use, attribution, consent_record, expires_at, reviewed_by)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10)
		RETURNING id`,
		w.SubjectType, w.SubjectID, w.RightsHolder, w.LicenceCode, nullableText(w.LicenceURL),
		w.PermittedUse, w.Attribution, nullableText(w.ConsentRecord), nullableTime(w.ExpiresAt), actor).Scan(&id)
	if err != nil {
		if isUniqueViolation(err) {
			return nil, ErrConflict
		}
		return nil, err
	}
	if w.SubjectType == "media" {
		_, err = tx.Exec(ctx, `UPDATE species_media SET rights_id = $1 WHERE id = $2`, id, w.SubjectID)
	} else {
		_, err = tx.Exec(ctx, `UPDATE species_audio SET rights_id = $1 WHERE id = $2`, id, w.SubjectID)
	}
	if err != nil {
		return nil, err
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, err
	}
	return r.getRights(ctx, id)
}

func (r *Repo) getRights(ctx context.Context, id string) (*Rights, error) {
	var rs rightsScan
	err := r.pool.QueryRow(ctx, `
		SELECT id, subject_type, subject_id::text, rights_holder, licence_code, licence_url,
		       permitted_use, attribution, consent_record, expires_at, withdrawn_at
		FROM species_rights WHERE id = $1`, id).Scan(
		&rs.ID, &rs.SubjectType, &rs.SubjectID, &rs.RightsHolder, &rs.LicenceCode,
		&rs.LicenceURL, &rs.PermittedUse, &rs.Attribution, &rs.ConsentRecord,
		&rs.ExpiresAt, &rs.WithdrawnAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	return rs.toRights(), nil
}

// ListSources returns the source registry, optionally for one species.
func (r *Repo) ListSources(ctx context.Context, speciesID string) ([]Source, error) {
	rows, err := r.pool.Query(ctx, `
		SELECT id, publisher, citation, url, accessed_at, licence_code, licence_url,
		       permitted_use, attribution, snapshot_hash, review_status
		FROM species_sources
		WHERE ($1 = '' OR species_id::text = $1)
		ORDER BY created_at DESC`, speciesID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Source{}
	for rows.Next() {
		var s sourceScan
		if err := rows.Scan(&s.ID, &s.Publisher, &s.Citation, &s.URL, &s.AccessedAt,
			&s.LicenceCode, &s.LicenceURL, &s.PermittedUse, &s.Attribution,
			&s.SnapshotHash, &s.ReviewStatus); err != nil {
			return nil, err
		}
		if src := s.toSource(); src != nil {
			out = append(out, *src)
		}
	}
	return out, rows.Err()
}

// ListRights returns every rights record.
func (r *Repo) ListRights(ctx context.Context) ([]Rights, error) {
	rows, err := r.pool.Query(ctx, `
		SELECT id, subject_type, subject_id::text, rights_holder, licence_code, licence_url,
		       permitted_use, attribution, consent_record, expires_at, withdrawn_at
		FROM species_rights ORDER BY created_at DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Rights{}
	for rows.Next() {
		var rs rightsScan
		if err := rows.Scan(&rs.ID, &rs.SubjectType, &rs.SubjectID, &rs.RightsHolder,
			&rs.LicenceCode, &rs.LicenceURL, &rs.PermittedUse, &rs.Attribution,
			&rs.ConsentRecord, &rs.ExpiresAt, &rs.WithdrawnAt); err != nil {
			return nil, err
		}
		if v := rs.toRights(); v != nil {
			out = append(out, *v)
		}
	}
	return out, rows.Err()
}

// ReviewQueue lists pending items by stage.
func (r *Repo) ReviewQueue(ctx context.Context, stage, cursor string, limit int) ([]ReviewItem, bool, string, error) {
	limit = clampLimit(limit)
	args := []any{}
	where := []string{"q.status = 'pending'"}
	if stage != "" {
		args = append(args, stage)
		where = append(where, fmt.Sprintf("q.stage = $%d", len(args)))
	}
	if cursor != "" {
		args = append(args, cursor)
		where = append(where, fmt.Sprintf("q.id::text > $%d", len(args)))
	}
	rows, err := r.pool.Query(ctx, reviewSelect+`
		WHERE `+strings.Join(where, " AND ")+`
		ORDER BY q.submitted_at, q.id
		LIMIT `+fmt.Sprint(limit+1), args...)
	if err != nil {
		return nil, false, "", err
	}
	defer rows.Close()
	out := []ReviewItem{}
	for rows.Next() {
		item, err := scanReview(rows)
		if err != nil {
			return nil, false, "", err
		}
		out = append(out, item)
	}
	if err := rows.Err(); err != nil {
		return nil, false, "", err
	}
	hasMore := len(out) > limit
	next := ""
	if hasMore {
		out = out[:limit]
		next = out[len(out)-1].ID
	}
	return out, hasMore, next, nil
}

const reviewSelect = `
	SELECT q.id, q.entity_type, q.entity_id, s.slug, q.language_tag, q.stage, q.status,
	       q.assigned_to, q.translator_ref, q.reviewer_ref, q.submitted_at
	FROM species_review_queue q
	JOIN species s ON s.id = q.species_id`

type rowScanner interface {
	Scan(dest ...any) error
}

func scanReview(row rowScanner) (ReviewItem, error) {
	var (
		item                 ReviewItem
		language, assigned   sql.NullString
		translator, reviewer sql.NullString
		submitted            sql.NullTime
	)
	err := row.Scan(&item.ID, &item.EntityType, &item.EntityID, &item.SpeciesSlug,
		&language, &item.Stage, &item.Status, &assigned, &translator, &reviewer, &submitted)
	if err != nil {
		return item, err
	}
	item.Language = nullString(language)
	item.AssignedTo = nullString(assigned)
	item.TranslatorRef = nullString(translator)
	item.ReviewerRef = nullString(reviewer)
	if submitted.Valid {
		t := submitted.Time
		item.SubmittedAt = &t
	}
	return item, nil
}

// DecideReview records a decision and advances or returns the entity.
func (r *Repo) DecideReview(ctx context.Context, itemID, actor, decision, notes string) (*ReviewItem, error) {
	if decision != "approved" && decision != "changes_requested" && decision != "rejected" {
		return nil, &ValidationError{Field: "decision", Message: "invalid decision"}
	}
	var (
		speciesID, entityType, entityID, stage string
		status                                 string
		language, translator                   sql.NullString
	)
	err := r.pool.QueryRow(ctx, `
		SELECT species_id, entity_type, entity_id, language_tag, stage, status, translator_ref
		FROM species_review_queue WHERE id = $1`, itemID).Scan(
		&speciesID, &entityType, &entityID, &language, &stage, &status, &translator)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	if status != "pending" {
		return nil, &GateError{Code: "REVIEW_ALREADY_DECIDED", Message: "this review item is no longer pending"}
	}
	if stage == "language" && translator.Valid && translator.String == actor {
		return nil, &GateError{Code: "SELF_REVIEW", Message: "the language reviewer must differ from the translator"}
	}
	if stage == "rights" {
		uploader, err := r.uploaderOf(ctx, entityType, entityID)
		if err != nil {
			return nil, err
		}
		if uploader != "" && uploader == actor {
			return nil, &GateError{Code: "SELF_REVIEW", Message: "the rights reviewer must differ from the uploader"}
		}
	}

	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx)

	if _, err := tx.Exec(ctx, `
		UPDATE species_review_queue SET status = $2, reviewer_ref = $3, notes = $4,
		       decided_at = now(), assigned_to = $3
		WHERE id = $1`, itemID, decision, actor, nullText(notes)); err != nil {
		return nil, err
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO species_reviews (entity_type, entity_id, stage, reviewer_id, decision, notes)
		VALUES ($1,$2,$3,$4,$5,$6)`, entityType, entityID, stage, actor, decision, nullText(notes)); err != nil {
		return nil, err
	}

	if decision == "approved" {
		if err := approveEntity(ctx, tx, entityType, entityID, actor); err != nil {
			return nil, err
		}
		if entityType == "content" {
			lang := "en"
			if language.Valid {
				lang = language.String
			}
			if err := enqueueReview(ctx, tx, speciesID, "content", entityID, lang, "publish", translator); err != nil {
				return nil, err
			}
		}
	} else {
		if err := returnEntity(ctx, tx, entityType, entityID, decision); err != nil {
			return nil, err
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, err
	}
	return r.getReviewItem(ctx, itemID)
}

func (r *Repo) getReviewItem(ctx context.Context, id string) (*ReviewItem, error) {
	row := r.pool.QueryRow(ctx, reviewSelect+` WHERE q.id = $1`, id)
	item, err := scanReview(row)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	return &item, nil
}

// ReviewItem returns one queue row so the handler can pick the stage permission.
func (r *Repo) ReviewItem(ctx context.Context, id string) (*ReviewItem, error) {
	return r.getReviewItem(ctx, id)
}

func (r *Repo) uploaderOf(ctx context.Context, entityType, entityID string) (string, error) {
	var uploader sql.NullString
	var err error
	switch entityType {
	case "media":
		err = r.pool.QueryRow(ctx, `SELECT created_by FROM species_media WHERE id = $1`, entityID).Scan(&uploader)
	case "audio":
		err = r.pool.QueryRow(ctx, `SELECT created_by FROM species_audio WHERE id = $1`, entityID).Scan(&uploader)
	default:
		return "", nil
	}
	if errors.Is(err, pgx.ErrNoRows) {
		return "", nil
	}
	return uploader.String, err
}

// Publish runs the gate and makes one language variant public.
func (r *Repo) Publish(ctx context.Context, speciesID, language, actor string) (*Detail, error) {
	language = normaliseLanguage(language)
	if err := r.checkPublish(ctx, speciesID, language); err != nil {
		return nil, err
	}
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx)

	if _, err := tx.Exec(ctx, `
		UPDATE species SET publication_state = 'published', updated_at = now() WHERE id = $1`,
		speciesID); err != nil {
		return nil, err
	}
	if _, err := tx.Exec(ctx, `
		UPDATE species_content SET published_at = now()
		 WHERE species_id = $1 AND language_tag = $2 AND review_status = 'reviewed'`,
		speciesID, language); err != nil {
		return nil, err
	}
	if _, err := tx.Exec(ctx, `
		UPDATE species_media SET published_at = now()
		 WHERE species_id = $1 AND review_status = 'reviewed'`, speciesID); err != nil {
		return nil, err
	}
	if _, err := tx.Exec(ctx, `
		UPDATE species_audio SET published_at = now()
		 WHERE species_id = $1 AND review_status = 'reviewed'`, speciesID); err != nil {
		return nil, err
	}
	if _, err := tx.Exec(ctx, `
		UPDATE species_review_queue SET status = 'approved', reviewer_ref = $3,
		       decided_at = now(), assigned_to = $3
		 WHERE species_id = $1 AND stage = 'publish' AND status = 'pending'
		   AND (language_tag IS NULL OR language_tag = $2)`,
		speciesID, language, actor); err != nil {
		return nil, err
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, err
	}
	return r.GetAdmin(ctx, speciesID)
}

// checkPublish is the no-source-no-publish gate.
func (r *Repo) checkPublish(ctx context.Context, speciesID, language string) error {
	var state string
	err := r.pool.QueryRow(ctx, `SELECT publication_state FROM species WHERE id = $1`, speciesID).Scan(&state)
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrNotFound
	}
	if err != nil {
		return err
	}
	if state == "tombstoned" {
		return &GateError{Code: "SPECIES_TOMBSTONED", Message: "a tombstoned record cannot be published"}
	}

	var reviewStatus string
	var published sql.NullTime
	err = r.pool.QueryRow(ctx, `
		SELECT review_status, published_at FROM species_content
		 WHERE species_id = $1 AND language_tag = $2`, speciesID, language).Scan(&reviewStatus, &published)
	if errors.Is(err, pgx.ErrNoRows) {
		return &GateError{Code: "CONTENT_MISSING", Message: "no " + language + " variant exists"}
	}
	if err != nil {
		return err
	}
	if reviewStatus != "reviewed" {
		return &GateError{Code: "CONTENT_NOT_REVIEWED", Message: "the " + language + " variant has not passed review"}
	}
	if language != "en" {
		var translator, reviewer sql.NullString
		err := r.pool.QueryRow(ctx, `
			SELECT translator_id, reviewer_id FROM species_content
			 WHERE species_id = $1 AND language_tag = $2`, speciesID, language).Scan(&translator, &reviewer)
		if err != nil {
			return err
		}
		if !translator.Valid || !reviewer.Valid || translator.String == reviewer.String {
			return &GateError{Code: "REVIEW_SEPARATION", Message: "translator and language reviewer must differ"}
		}
	}

	var reviewedSources int
	if err := r.pool.QueryRow(ctx, `
		SELECT count(*) FROM species_sources
		 WHERE species_id = $1 AND review_status = 'reviewed'`, speciesID).Scan(&reviewedSources); err != nil {
		return err
	}
	if reviewedSources == 0 {
		return &GateError{Code: "SOURCE_MISSING", Message: "no reviewed source is attached to this species"}
	}

	badMedia, err := r.unsafeAssets(ctx, "species_media", speciesID)
	if err != nil {
		return err
	}
	if badMedia > 0 {
		return &GateError{Code: "RIGHTS_MISSING", Message: "every published media asset needs a valid rights record"}
	}
	badAudio, err := r.unsafeAssets(ctx, "species_audio", speciesID)
	if err != nil {
		return err
	}
	if badAudio > 0 {
		return &GateError{Code: "RIGHTS_MISSING", Message: "every published audio track needs a valid rights record"}
	}

	var pending int
	if err := r.pool.QueryRow(ctx, `
		SELECT count(*) FROM species_review_queue
		 WHERE species_id = $1 AND status = 'pending' AND stage <> 'publish'
		   AND (language_tag IS NULL OR language_tag IN ('en', $2))`, speciesID, language).Scan(&pending); err != nil {
		return err
	}
	if pending > 0 {
		return &GateError{Code: "REVIEW_PENDING", Message: "review is still pending for this variant"}
	}
	return nil
}

func (r *Repo) unsafeAssets(ctx context.Context, table, speciesID string) (int, error) {
	var n int
	err := r.pool.QueryRow(ctx, `
		SELECT count(*) FROM `+table+` a
		LEFT JOIN species_rights rt ON rt.id = a.rights_id
		WHERE a.species_id = $1 AND a.review_status = 'reviewed'
		  AND (rt.id IS NULL OR rt.withdrawn_at IS NOT NULL
		       OR (rt.expires_at IS NOT NULL AND rt.expires_at <= now())
		       OR upper(rt.licence_code) LIKE '%NC%')`, speciesID).Scan(&n)
	return n, err
}

// Unpublish takes a species down. A rights withdrawal also tombstones it and
// propagates staleness to every translation without deleting audit history.
func (r *Repo) Unpublish(ctx context.Context, speciesID, actor, reason string, tombstone bool) (*Detail, error) {
	if len(strings.TrimSpace(reason)) < 10 {
		return nil, &ValidationError{Field: "reason", Message: "reason must be at least 10 characters"}
	}
	if _, err := r.GetAdmin(ctx, speciesID); err != nil {
		return nil, err
	}
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx)

	state := "unpublished"
	if tombstone {
		state = "tombstoned"
		staleReason := "rights_withdrawn"
		_, err = tx.Exec(ctx, `
			UPDATE species_rights SET withdrawn_at = now()
			 WHERE subject_id IN (
			   SELECT id FROM species_media WHERE species_id = $1
			   UNION ALL
			   SELECT id FROM species_audio WHERE species_id = $1)
			   AND withdrawn_at IS NULL`, speciesID)
		if err != nil {
			return nil, err
		}
		if _, err := tx.Exec(ctx, `
			UPDATE species_media SET review_status = 'stale', published_at = NULL
			 WHERE species_id = $1`, speciesID); err != nil {
			return nil, err
		}
		if _, err := tx.Exec(ctx, `
			UPDATE species_audio SET review_status = 'stale', published_at = NULL
			 WHERE species_id = $1`, speciesID); err != nil {
			return nil, err
		}
		if _, err := tx.Exec(ctx, `
			UPDATE species_content SET review_status = 'stale', stale_reason = $2, published_at = NULL
			 WHERE species_id = $1`, speciesID, staleReason); err != nil {
			return nil, err
		}
	}
	_, err = tx.Exec(ctx, `
		UPDATE species SET publication_state = $2, updated_at = now() WHERE id = $1`,
		speciesID, state)
	if err != nil {
		return nil, err
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO species_reviews (entity_type, entity_id, stage, reviewer_id, decision, notes)
		VALUES ('species', $1, 'publish', $2, 'approved', $3)`,
		speciesID, actor, state+": "+reason); err != nil {
		return nil, err
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, err
	}
	return r.GetAdmin(ctx, speciesID)
}

// SavedSpecies lists the reader's saved, still-published species.
func (r *Repo) SavedSpecies(ctx context.Context, userID string) ([]Summary, error) {
	items, _, _, err := r.ListPublic(ctx, ListFilter{SavedBy: userID, Limit: model.MaxLimit}, userID)
	return items, err
}

// SaveSpecies is idempotent.
func (r *Repo) SaveSpecies(ctx context.Context, slug, userID string) (string, error) {
	var id string
	err := r.pool.QueryRow(ctx, `
		SELECT id FROM species WHERE slug = $1 AND publication_state = 'published'`, slug).Scan(&id)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", ErrNotFound
	}
	if err != nil {
		return "", err
	}
	_, err = r.pool.Exec(ctx, `
		INSERT INTO species_saves (user_id, species_id) VALUES ($1,$2)
		ON CONFLICT DO NOTHING`, userID, id)
	if err != nil {
		return "", err
	}
	return id, nil
}

// UnsaveSpecies is idempotent.
func (r *Repo) UnsaveSpecies(ctx context.Context, slug, userID string) error {
	_, err := r.pool.Exec(ctx, `
		DELETE FROM species_saves WHERE user_id = $1
		 AND species_id = (SELECT id FROM species WHERE slug = $2)`, userID, slug)
	return err
}

// PackManifest builds the explicit local-language download manifest.
func (r *Repo) PackManifest(ctx context.Context, language string) (*Pack, error) {
	language = normaliseLanguage(language)
	var count int
	if err := r.pool.QueryRow(ctx, `
		SELECT count(*) FROM content_languages WHERE tag = $1 AND status = 'active'`, language).Scan(&count); err != nil {
		return nil, err
	}
	if count == 0 {
		return nil, ErrNotFound
	}
	rows, err := r.pool.Query(ctx, `
		SELECT s.slug, s.content_version,
		       EXISTS (SELECT 1 FROM species_audio a WHERE a.species_id = s.id
		               AND a.language_tag = $1 AND a.review_status = 'reviewed') AS has_audio,
		       COALESCE((SELECT length(c.summary) + length(c.description) + length(c.habitat)
		                        + length(c.range_text) + length(c.where_to_see_text)
		                        + length(c.conservation_text)
		                 FROM species_content c WHERE c.species_id = s.id AND c.language_tag = $1
		                 AND c.review_status = 'reviewed'), 0) AS bytes
		FROM species s
		WHERE s.publication_state = 'published'
		  AND (EXISTS (SELECT 1 FROM species_content c WHERE c.species_id = s.id
		               AND c.language_tag = $1 AND c.review_status = 'reviewed')
		    OR EXISTS (SELECT 1 FROM species_names n WHERE n.species_id = s.id
		               AND n.language_tag = $1 AND n.review_status = 'reviewed'))
		ORDER BY s.scientific_name`, language)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	entries := []PackEntry{}
	var size int64
	maxVersion := 0
	for rows.Next() {
		var e PackEntry
		var bytes int64
		if err := rows.Scan(&e.Slug, &e.ContentVersion, &e.HasAudio, &bytes); err != nil {
			return nil, err
		}
		size += bytes
		if e.ContentVersion > maxVersion {
			maxVersion = e.ContentVersion
		}
		entries = append(entries, e)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if size < 1 {
		size = 1
	}
	now := time.Now().UTC()
	return &Pack{
		Language:    language,
		Version:     fmt.Sprintf("%s.%d", now.Format("2006.01"), maxVersion),
		SizeBytes:   size,
		GeneratedAt: now,
		Entries:     entries,
	}, nil
}

// ---------------------------------------------------------------------------
// internal helpers
// ---------------------------------------------------------------------------

func (r *Repo) requireLanguage(ctx context.Context, tag string) error {
	var exists bool
	if err := r.pool.QueryRow(ctx, `
		SELECT EXISTS (SELECT 1 FROM content_languages WHERE tag = $1)`, tag).Scan(&exists); err != nil {
		return err
	}
	if !exists {
		return &ValidationError{Field: "language", Message: "unknown BCP 47 content language: " + tag}
	}
	return nil
}

func enqueueReview(ctx context.Context, tx pgx.Tx, speciesID, entityType, entityID, language, stage string, translator sql.NullString) error {
	var lang any
	if language != "" {
		lang = language
	}
	_, err := tx.Exec(ctx, `
		INSERT INTO species_review_queue (species_id, entity_type, entity_id, language_tag, stage, translator_ref)
		VALUES ($1,$2,$3,$4,$5,$6)
		ON CONFLICT (entity_type, entity_id, stage, coalesce(language_tag, ''))
		DO UPDATE SET status = 'pending', submitted_at = now(), decided_at = NULL,
		              translator_ref = EXCLUDED.translator_ref, reviewer_ref = NULL, notes = NULL`,
		speciesID, entityType, entityID, lang, stage, translator)
	return err
}

func bumpContentVersion(ctx context.Context, tx pgx.Tx, speciesID, reason string) error {
	if _, err := tx.Exec(ctx, `
		UPDATE species SET content_version = content_version + 1, updated_at = now()
		 WHERE id = $1`, speciesID); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `
		UPDATE species_content SET review_status = 'stale', stale_reason = $2, published_at = NULL
		 WHERE species_id = $1`, speciesID, reason); err != nil {
		return err
	}
	rows, err := tx.Query(ctx, `
		SELECT id, language_tag, translator_id FROM species_content
		 WHERE species_id = $1 AND language_tag <> 'en'`, speciesID)
	if err != nil {
		return err
	}
	defer rows.Close()
	type pending struct {
		id    string
		lang  string
		trans sql.NullString
	}
	items := []pending{}
	for rows.Next() {
		var p pending
		if err := rows.Scan(&p.id, &p.lang, &p.trans); err != nil {
			return err
		}
		items = append(items, p)
	}
	if err := rows.Err(); err != nil {
		return err
	}
	for _, p := range items {
		if err := enqueueReview(ctx, tx, speciesID, "content", p.id, p.lang, "language", p.trans); err != nil {
			return err
		}
	}
	return nil
}

func approveEntity(ctx context.Context, tx pgx.Tx, entityType, entityID, actor string) error {
	switch entityType {
	case "content":
		_, err := tx.Exec(ctx, `
			UPDATE species_content SET review_status = 'reviewed', reviewer_id = $2,
			       stale_reason = NULL, published_at = NULL, updated_at = now()
			WHERE id = $1`, entityID, actor)
		return err
	case "name":
		_, err := tx.Exec(ctx, `UPDATE species_names SET review_status = 'reviewed', updated_at = now() WHERE id = $1`, entityID)
		return err
	case "media":
		if _, err := tx.Exec(ctx, `UPDATE species_media SET review_status = 'reviewed' WHERE id = $1`, entityID); err != nil {
			return err
		}
		_, err := tx.Exec(ctx, `UPDATE species_media_alt SET review_status = 'reviewed' WHERE media_id = $1`, entityID)
		return err
	case "audio":
		_, err := tx.Exec(ctx, `UPDATE species_audio SET review_status = 'reviewed' WHERE id = $1`, entityID)
		return err
	case "where_to_see":
		_, err := tx.Exec(ctx, `UPDATE species_where_to_see SET review_status = 'reviewed' WHERE id = $1`, entityID)
		return err
	}
	return nil
}

func returnEntity(ctx context.Context, tx pgx.Tx, entityType, entityID, decision string) error {
	status := "draft"
	switch entityType {
	case "content":
		_, err := tx.Exec(ctx, `
			UPDATE species_content SET review_status = $2, published_at = NULL, updated_at = now()
			 WHERE id = $1`, entityID, status)
		return err
	case "name":
		_, err := tx.Exec(ctx, `UPDATE species_names SET review_status = 'draft' WHERE id = $1`, entityID)
		return err
	case "media":
		_, err := tx.Exec(ctx, `UPDATE species_media SET review_status = 'draft' WHERE id = $1`, entityID)
		return err
	case "audio":
		_, err := tx.Exec(ctx, `UPDATE species_audio SET review_status = 'draft' WHERE id = $1`, entityID)
		return err
	}
	return nil
}

func validateSpeciesWrite(w SpeciesWrite, creating bool) error {
	if w.TaxonomicStatus != nil {
		switch *w.TaxonomicStatus {
		case "accepted", "synonym", "provisional", "uncertain":
		default:
			return &ValidationError{Field: "taxonomicStatus", Message: "invalid taxonomicStatus"}
		}
	}
	if w.IUCNCategory != nil && *w.IUCNCategory != "" && !validIUCN[*w.IUCNCategory] {
		return &ValidationError{Field: "iucnCategory", Message: "invalid IUCN category"}
	}
	if w.IUCNCategory != nil && *w.IUCNCategory != "" {
		if w.IUCNAssessmentID == nil || *w.IUCNAssessmentID == "" {
			return &ValidationError{Field: "iucnAssessmentId", Message: "IUCN category requires an assessment id"}
		}
		if w.StatusAsOf == nil || *w.StatusAsOf == "" {
			return &ValidationError{Field: "statusAsOf", Message: "IUCN category requires statusAsOf"}
		}
	}
	if w.StatusAsOf != nil && *w.StatusAsOf != "" {
		if _, err := time.Parse("2006-01-02", *w.StatusAsOf); err != nil {
			return &ValidationError{Field: "statusAsOf", Message: "statusAsOf must be YYYY-MM-DD"}
		}
	}
	if creating && w.Slug != nil && *w.Slug != "" {
		if slugify(*w.Slug) == "" {
			return &ValidationError{Field: "slug", Message: "slug must contain letters or digits"}
		}
	}
	return nil
}

func validateFieldStatus(fs map[string]string) error {
	for field, status := range fs {
		if !validFields[status] {
			return &ValidationError{Field: "fieldStatus." + field, Message: "status must be missing, draft, reviewed or stale"}
		}
	}
	return nil
}

func slugify(in string) string {
	s := strings.ToLower(strings.TrimSpace(in))
	s = slugPattern.ReplaceAllString(s, "-")
	return strings.Trim(s, "-")
}

func dateOrNil(v *string) any {
	if v == nil || strings.TrimSpace(*v) == "" {
		return nil
	}
	t, err := time.Parse("2006-01-02", *v)
	if err != nil {
		return nil
	}
	return t
}

func ptrOrNil(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}

func nullableText(v *string) any {
	if v == nil || *v == "" {
		return nil
	}
	return *v
}

func nullText(v string) any {
	if v == "" {
		return nil
	}
	return v
}

func diverged(a, b string) bool { return a != b }

func divergedPtr(a, b *string) bool {
	av, bv := "", ""
	if a != nil {
		av = *a
	}
	if b != nil {
		bv = *b
	}
	return av != bv
}

// isKWS reports whether a publisher or rights holder is the Kenya Wildlife
// Service, whose material needs written permission (ADR 0013 §8).
func isKWS(v string) bool {
	up := strings.ToUpper(strings.TrimSpace(v))
	return strings.Contains(up, "KWS") || strings.Contains(up, "KENYA WILDLIFE")
}

func isUniqueViolation(err error) bool {
	return err != nil && strings.Contains(err.Error(), "23505")
}

func nullableInt(v *int) any {
	if v == nil {
		return nil
	}
	return *v
}

func nullableTime(v *time.Time) any {
	if v == nil {
		return nil
	}
	return *v
}

// validateSourceWrite is the licensing boundary from api/contracts/species.v1.yaml
// and ADR 0013 §8. It runs before any row is written.
func validateSourceWrite(w SourceWrite) error {
	if w.Publisher == "" || w.Citation == "" || w.LicenceCode == "" || w.Attribution == "" {
		return &ValidationError{Field: "citation", Message: "publisher, citation, licenceCode and attribution are required"}
	}
	if !validUses[w.PermittedUse] {
		return &ValidationError{Field: "permittedUse", Message: "invalid permittedUse"}
	}
	if strings.EqualFold(w.Publisher, "IUCN") && w.PermittedUse != "taxonomy_only" {
		return &ValidationError{Field: "permittedUse", Message: "IUCN permits category and assessment metadata only; link out to the assessment"}
	}
	if strings.EqualFold(w.Publisher, "Wikipedia") && w.PermittedUse != "link_only" {
		return &ValidationError{Field: "permittedUse", Message: "Wikipedia is link-out only; prose copy is not permitted"}
	}
	if isKWS(w.Publisher) && w.PermittedUse != "link_only" &&
		!strings.HasPrefix(strings.ToUpper(strings.TrimSpace(w.LicenceCode)), "PERMISSION-") {
		return &ValidationError{Field: "licenceCode", Message: "KWS material requires written permission; record it as PERMISSION-<ref> or link out"}
	}
	if strings.EqualFold(w.Publisher, "GBIF") && w.PermittedUse == "occurrence" {
		code := strings.ToUpper(strings.TrimSpace(w.LicenceCode))
		if !strings.HasPrefix(code, "CC0") && !strings.HasPrefix(code, "CC-BY-") && code != "CC-BY" {
			return &ValidationError{Field: "licenceCode", Message: "GBIF occurrences are published only under CC0 or CC BY"}
		}
	}
	if strings.Contains(strings.ToUpper(w.LicenceCode), "NC") && w.PermittedUse == "occurrence" {
		return &ValidationError{Field: "licenceCode", Message: "CC BY-NC occurrence records are excluded"}
	}
	return nil
}
