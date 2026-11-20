package repository

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/bradleygichuru/ytci-go/internal/handler"
)

const submissionsPrefix = "repository/submissions/"

type settingsState struct {
	Open      bool
	Intro     string
	Terms     string
	UpdatedAt *time.Time
}

func (h *Handler) loadSettings(ctx context.Context) (settingsState, error) {
	var s settingsState
	var intro *string
	err := h.pool.QueryRow(ctx, `
SELECT submissions_open, submission_intro, terms_version, updated_at
FROM repository_settings WHERE id = 'default'`).
		Scan(&s.Open, &intro, &s.Terms, &s.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return settingsState{Open: false, Terms: "2026-11"}, nil
	}
	if err != nil {
		return settingsState{}, err
	}
	s.Intro = deref(intro)
	return s, nil
}

// CreateSubmission accepts an anonymous competition entry or innovation.
func (h *Handler) CreateSubmission(w http.ResponseWriter, r *http.Request) {
	var req submissionRequest
	if !decodeBody(w, r, 1024*1024, &req) {
		return
	}

	settings, err := h.loadSettings(r.Context())
	if err != nil {
		handler.WriteServerError(w, r, "INTERNAL_ERROR", "failed to read repository settings", err)
		return
	}
	if !settings.Open {
		handler.WriteError(w, http.StatusConflict, "SUBMISSIONS_CLOSED", "repository submissions are closed")
		return
	}

	// Honeypot: a filled field is a bot. Reject without echoing the value.
	if strings.TrimSpace(req.Honeypot) != "" {
		v := validationCollector{}
		v.add("honeypot", "submission rejected")
		v.write(w)
		return
	}

	var v validationCollector
	kind := strings.ToLower(strings.TrimSpace(req.Kind))
	if !publicKinds[kind] {
		v.add("kind", "public submissions must be competition_entry or innovation")
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
	if body == "" {
		v.add("bodyMd", "bodyMd is required")
	} else if len([]rune(body)) > 40000 {
		v.add("bodyMd", "bodyMd must be 40,000 characters or fewer")
	}
	authorName := strings.TrimSpace(req.AuthorName)
	if authorName == "" {
		v.add("authorName", "authorName is required")
	} else if len([]rune(authorName)) > 120 {
		v.add("authorName", "authorName must be 120 characters or fewer")
	}
	email := strings.TrimSpace(req.AuthorEmail)
	if email == "" {
		v.add("authorEmail", "authorEmail is required")
	} else if !validEmail(email) {
		v.add("authorEmail", "must be a valid email address")
	}
	phone := strings.TrimSpace(req.AuthorPhone)
	if len([]rune(phone)) > 40 {
		v.add("authorPhone", "authorPhone must be 40 characters or fewer")
	}
	org := strings.TrimSpace(req.AuthorOrg)
	if len([]rune(org)) > 160 {
		v.add("authorOrg", "authorOrg must be 160 characters or fewer")
	}
	ageBand := strings.TrimSpace(req.AuthorAgeBand)
	if ageBand == "" {
		v.add("authorAgeBand", "authorAgeBand is required")
	} else if !ageBands[ageBand] {
		v.add("authorAgeBand", "authorAgeBand is not a valid band")
	}
	if !req.Consent {
		v.add("consent", "consent is required")
	}
	if strings.TrimSpace(req.ConsentVersion) != settings.Terms {
		v.add("consentVersion", "consentVersion must match the current terms version")
	}
	if ageBand == "under_18" && !req.GuardianConsent {
		v.add("guardianConsent", "guardian consent is required for under-18 submissions")
	}
	tags, ok := normalizeTags(req.Tags)
	if !ok {
		v.add("tags", "tags must contain at most 10 values of 40 characters or fewer")
	}
	team, ok := normalizeTeam(req.TeamMembers)
	if !ok {
		v.add("teamMembers", "teamMembers must contain at most 10 named people")
	}
	competitionName := strings.TrimSpace(req.CompetitionName)
	if len([]rune(competitionName)) > 200 {
		v.add("competitionName", "competitionName must be 200 characters or fewer")
	}
	competitionCategory := strings.TrimSpace(req.CompetitionCategory)
	if len([]rune(competitionCategory)) > 120 {
		v.add("competitionCategory", "competitionCategory must be 120 characters or fewer")
	}

	externalURL := strings.TrimSpace(req.ExternalURL)
	if externalURL != "" && !validHTTPURL(externalURL) {
		v.add("externalUrl", "externalUrl must be an http(s) URL")
	}
	embedURL := strings.TrimSpace(req.EmbedURL)
	embedName := ""
	if embedURL != "" {
		var ok bool
		embedName, ok = embedProvider(embedURL)
		if !ok {
			handler.WriteError(w, http.StatusBadRequest, "INVALID_EMBED_PROVIDER", "embed hosts must be YouTube, Vimeo or Matterport")
			return
		}
	}

	galleryKeys := make([]string, 0, len(req.GalleryObjectKeys))
	for _, key := range req.GalleryObjectKeys {
		key = strings.TrimSpace(key)
		if key == "" {
			continue
		}
		galleryKeys = append(galleryKeys, key)
	}
	if len(galleryKeys) > maxGalleryImages {
		v.add("galleryObjectKeys", "at most 5 gallery images are allowed")
	}
	coverKey := strings.TrimSpace(req.CoverObjectKey)
	documentKey := strings.TrimSpace(req.DocumentObjectKey)

	if coverKey != "" && !strings.HasPrefix(coverKey, submissionsPrefix) {
		v.add("coverObjectKey", "coverObjectKey is not a repository submission upload")
	}
	if documentKey != "" && !strings.HasPrefix(documentKey, submissionsPrefix) {
		v.add("documentObjectKey", "documentObjectKey is not a repository submission upload")
	}
	seenKeys := map[string]bool{}
	for _, key := range append([]string{coverKey, documentKey}, galleryKeys...) {
		if key == "" {
			continue
		}
		if !strings.HasPrefix(key, submissionsPrefix) {
			v.add("galleryObjectKeys", "galleryObjectKeys contain a non-submission upload")
			break
		}
		if seenKeys[key] {
			v.add("galleryObjectKeys", "upload keys must be distinct")
			break
		}
		seenKeys[key] = true
	}

	county := strings.TrimSpace(req.County)
	if county == "" {
		v.add("county", "county is required")
	}
	category := strings.TrimSpace(req.Category)
	if category == "" {
		v.add("category", "category is required")
	}

	if v.empty() {
		canonicalCounty, err := h.canonicalCounty(r.Context(), county)
		if err != nil {
			handler.WriteServerError(w, r, "INTERNAL_ERROR", "failed to validate county", err)
			return
		}
		if canonicalCounty == "" {
			v.add("county", "county must match a Kenyan county")
		} else {
			county = canonicalCounty
		}
		canonicalCategory, err := h.canonicalCategory(r.Context(), kind, category)
		if err != nil {
			handler.WriteServerError(w, r, "INTERNAL_ERROR", "failed to validate category", err)
			return
		}
		if canonicalCategory == "" {
			v.add("category", "category is not valid for this kind")
		} else {
			category = canonicalCategory
		}
	}
	if v.write(w) {
		return
	}

	if (coverKey != "" || documentKey != "" || len(galleryKeys) > 0) && h.r2 == nil {
		handler.WriteError(w, http.StatusServiceUnavailable, "STORAGE_UNAVAILABLE", "media storage is not configured")
		return
	}

	moderationNote := ""
	if ageBand == "under_18" {
		moderationNote = "Guardian consent declared at submission."
	}

	ctx := r.Context()
	tx, err := h.pool.Begin(ctx)
	if err != nil {
		handler.WriteServerError(w, r, "INTERNAL_ERROR", "failed to start submission", err)
		return
	}
	defer tx.Rollback(ctx)

	intents := map[string]uploadIntent{}
	verify := func(key, wantKind string) bool {
		if key == "" {
			return true
		}
		intent, err := h.verifyIntent(ctx, tx, key, wantKind)
		if err != nil {
			if !writeUploadError(w, err) {
				handler.WriteServerError(w, r, "INTERNAL_ERROR", "failed to verify upload", err)
			}
			return false
		}
		intents[key] = intent
		return true
	}
	if !verify(coverKey, "image") || !verify(documentKey, "document") {
		return
	}
	for _, key := range galleryKeys {
		if !verify(key, "image") {
			return
		}
	}

	entryID, referenceCode, createdAt, err := h.insertSubmittedEntry(ctx, tx, submittedEntry{
		Kind:                kind,
		Title:               title,
		Summary:             summary,
		BodyMD:              body,
		County:              county,
		Category:            category,
		Tags:                tags,
		AuthorName:          authorName,
		AuthorEmail:         email,
		AuthorPhone:         phone,
		AuthorAgeBand:       ageBand,
		AuthorOrg:           org,
		TeamMembers:         team,
		CompetitionName:     competitionName,
		CompetitionCategory: competitionCategory,
		ExternalURL:         externalURL,
		EmbedURL:            embedURL,
		EmbedProvider:       embedName,
		ModerationNote:      moderationNote,
		ConsentVersion:      strings.TrimSpace(req.ConsentVersion),
	})
	if err != nil {
		handler.WriteServerError(w, r, "INTERNAL_ERROR", "failed to create submission", err)
		return
	}

	var coverMediaID, documentMediaID *string
	if coverKey != "" {
		id, err := h.insertEntryMedia(ctx, tx, entryID, coverKey, "image", "pending_review", title, authorName, intents[coverKey].ExpectedSize)
		if err != nil {
			handler.WriteServerError(w, r, "INTERNAL_ERROR", "failed to record cover media", err)
			return
		}
		coverMediaID = &id
	}
	if documentKey != "" {
		id, err := h.insertEntryMedia(ctx, tx, entryID, documentKey, "pdf", "pending_review", "", "", intents[documentKey].ExpectedSize)
		if err != nil {
			handler.WriteServerError(w, r, "INTERNAL_ERROR", "failed to record document media", err)
			return
		}
		documentMediaID = &id
	}
	for i, key := range galleryKeys {
		id, err := h.insertEntryMedia(ctx, tx, entryID, key, "image", "pending_review", title, authorName, intents[key].ExpectedSize)
		if err != nil {
			handler.WriteServerError(w, r, "INTERNAL_ERROR", "failed to record gallery media", err)
			return
		}
		if _, err := tx.Exec(ctx, `
INSERT INTO repository_entry_media (entry_id, media_id, sort_order) VALUES ($1, $2, $3)`,
			entryID, id, i); err != nil {
			handler.WriteServerError(w, r, "INTERNAL_ERROR", "failed to record gallery order", err)
			return
		}
	}
	if coverMediaID != nil || documentMediaID != nil {
		if _, err := tx.Exec(ctx, `
UPDATE repository_entries SET cover_media_id = $2, document_media_id = $3 WHERE id = $1`,
			entryID, coverMediaID, documentMediaID); err != nil {
			handler.WriteServerError(w, r, "INTERNAL_ERROR", "failed to link submission media", err)
			return
		}
	}

	if len(intents) > 0 {
		ids := make([]string, 0, len(intents))
		for _, intent := range intents {
			ids = append(ids, intent.ID)
		}
		if _, err := tx.Exec(ctx, `
UPDATE repository_upload_intents SET consumed_at = now() WHERE id = ANY($1::uuid[])`, ids); err != nil {
			handler.WriteServerError(w, r, "INTERNAL_ERROR", "failed to consume upload intents", err)
			return
		}
	}

	if err := tx.Commit(ctx); err != nil {
		handler.WriteServerError(w, r, "INTERNAL_ERROR", "failed to save submission", err)
		return
	}

	writeJSON(w, http.StatusCreated, submissionResponse{
		ReferenceCode: referenceCode,
		Status:        "submitted",
		SubmittedAt:   formatTime(createdAt),
	})
}

func (h *Handler) canonicalCounty(ctx context.Context, county string) (string, error) {
	var canonical string
	err := h.pool.QueryRow(ctx, `
SELECT adm1_name FROM kenya_counties WHERE lower(adm1_name) = lower($1) LIMIT 1`, county).Scan(&canonical)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", nil
	}
	return canonical, err
}

func (h *Handler) canonicalCategory(ctx context.Context, kind, category string) (string, error) {
	var canonical string
	err := h.pool.QueryRow(ctx, `
SELECT name FROM repository_categories WHERE kind = $1 AND lower(name) = lower($2) LIMIT 1`,
		kind, category).Scan(&canonical)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", nil
	}
	return canonical, err
}

type submittedEntry struct {
	Kind                string
	Title               string
	Summary             string
	BodyMD              string
	County              string
	Category            string
	Tags                []string
	AuthorName          string
	AuthorEmail         string
	AuthorPhone         string
	AuthorAgeBand       string
	AuthorOrg           string
	TeamMembers         []teamMember
	CompetitionName     string
	CompetitionCategory string
	ExternalURL         string
	EmbedURL            string
	EmbedProvider       string
	ModerationNote      string
	ConsentVersion      string
}

// insertSubmittedEntry retries with a random suffix on either unique index so
// a slug or reference-code race never surfaces to the entrant.
func (h *Handler) insertSubmittedEntry(ctx context.Context, tx pgx.Tx, e submittedEntry) (id, referenceCode string, createdAt time.Time, err error) {
	base := slugify(e.Title)
	teamJSON, err := json.Marshal(e.TeamMembers)
	if err != nil {
		return "", "", time.Time{}, err
	}

	for attempt := 0; attempt < 8; attempt++ {
		candidate := base
		if attempt > 0 {
			candidate = base + "-" + strings.ToLower(randomCode(4))
		}
		code := newReferenceCode()

		err = tx.QueryRow(ctx, `
INSERT INTO repository_entries
(kind, slug, title, summary, body_md, county, category, tags,
 author_name, author_email, author_phone, author_age_band, author_org, team_members,
 competition_name, competition_category, external_url, embed_url, embed_provider,
 status, source, featured, moderation_note, consent_at, consent_version, reference_code)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8,
        $9, $10, $11, $12, $13, NULLIF($14::jsonb, 'null'::jsonb),
        $15, $16, NULLIF($17, ''), NULLIF($18, ''), NULLIF($19, ''),
        'submitted', 'public', false, NULLIF($20, ''), now(), $21, $22)
ON CONFLICT DO NOTHING
RETURNING id::text, created_at`,
			e.Kind, candidate, e.Title, e.Summary, e.BodyMD, e.County, e.Category, e.Tags,
			e.AuthorName, e.AuthorEmail, nullIfEmpty(e.AuthorPhone), e.AuthorAgeBand, nullIfEmpty(e.AuthorOrg), string(teamJSON),
			nullIfEmpty(e.CompetitionName), nullIfEmpty(e.CompetitionCategory),
			e.ExternalURL, e.EmbedURL, e.EmbedProvider,
			e.ModerationNote, e.ConsentVersion, code).
			Scan(&id, &createdAt)
		if err == nil {
			return id, code, createdAt, nil
		}
		if !errors.Is(err, pgx.ErrNoRows) {
			return "", "", time.Time{}, err
		}
	}
	return "", "", time.Time{}, fmt.Errorf("could not allocate a unique slug or reference code")
}

// insertEntryMedia records a media_assets row owned by the entry.
func (h *Handler) insertEntryMedia(ctx context.Context, tx pgx.Tx, entryID, objectKey, mediaType, status, alt, credit string, size int64) (string, error) {
	var id string
	err := tx.QueryRow(ctx, `
INSERT INTO media_assets (object_key, type, status, entity_type, entity_id, alt_text, credit, original_name, file_size_bytes)
VALUES ($1, $2, $3, 'repository_entry', $4, NULLIF($5, ''), NULLIF($6, ''), $7, $8)
RETURNING id::text`,
		objectKey, mediaType, status, entryID, alt, credit, baseName(objectKey), size).Scan(&id)
	return id, err
}

func nullIfEmpty(s string) *string {
	if strings.TrimSpace(s) == "" {
		return nil
	}
	return &s
}
