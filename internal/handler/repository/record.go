package repository

import (
	"context"
	"encoding/json"
	"log/slog"
	"time"
)

// entryColumns is intentionally explicit: public and admin responses are
// built from one scan so no internal column can leak into the public DTO by
// accident.
const entryColumns = `
SELECT e.id::text, e.kind, e.slug, e.title, e.summary, coalesce(e.body_md, ''),
       e.county, e.category, e.tags,
       e.author_name, e.author_org, e.competition_name, e.competition_category,
       e.author_email, e.author_phone, e.author_age_band, e.team_members,
       e.cover_media_id::text, e.document_media_id::text,
       e.external_url, e.embed_url, e.embed_provider,
       e.status, e.source, e.featured, e.moderation_note, e.reviewed_by,
       e.reviewed_at, e.consent_at, e.consent_version, e.reference_code,
       e.published_at, e.created_at, e.updated_at,
       cm.object_key, cm.thumbnail_key, cm.alt_text, cm.credit,
       dm.object_key, dm.original_name, dm.file_size_bytes
FROM repository_entries e
LEFT JOIN media_assets cm ON cm.id = e.cover_media_id
LEFT JOIN media_assets dm ON dm.id = e.document_media_id`

type entryRecord struct {
	ID                  string
	Kind                string
	Slug                string
	Title               string
	Summary             string
	BodyMD              string
	County              *string
	Category            *string
	Tags                []string
	AuthorName          *string
	AuthorOrg           *string
	CompetitionName     *string
	CompetitionCategory *string
	AuthorEmail         *string
	AuthorPhone         *string
	AuthorAgeBand       *string
	TeamMembers         []byte
	CoverMediaID        *string
	DocumentMediaID     *string
	ExternalURL         *string
	EmbedURL            *string
	EmbedProvider       *string
	Status              string
	Source              string
	Featured            bool
	ModerationNote      *string
	ReviewedBy          *string
	ReviewedAt          *time.Time
	ConsentAt           *time.Time
	ConsentVersion      *string
	ReferenceCode       *string
	PublishedAt         *time.Time
	CreatedAt           *time.Time
	UpdatedAt           *time.Time
	CoverObjectKey      *string
	CoverThumbKey       *string
	CoverAlt            *string
	CoverCredit         *string
	DocObjectKey        *string
	DocOriginalName     *string
	DocSize             *int64
}

// scanner is satisfied by pgx.Row and pgx.Rows.
type scanner interface {
	Scan(dest ...any) error
}

func scanEntry(row scanner) (entryRecord, error) {
	var e entryRecord
	err := row.Scan(
		&e.ID, &e.Kind, &e.Slug, &e.Title, &e.Summary, &e.BodyMD,
		&e.County, &e.Category, &e.Tags,
		&e.AuthorName, &e.AuthorOrg, &e.CompetitionName, &e.CompetitionCategory,
		&e.AuthorEmail, &e.AuthorPhone, &e.AuthorAgeBand, &e.TeamMembers,
		&e.CoverMediaID, &e.DocumentMediaID,
		&e.ExternalURL, &e.EmbedURL, &e.EmbedProvider,
		&e.Status, &e.Source, &e.Featured, &e.ModerationNote, &e.ReviewedBy,
		&e.ReviewedAt, &e.ConsentAt, &e.ConsentVersion, &e.ReferenceCode,
		&e.PublishedAt, &e.CreatedAt, &e.UpdatedAt,
		&e.CoverObjectKey, &e.CoverThumbKey, &e.CoverAlt, &e.CoverCredit,
		&e.DocObjectKey, &e.DocOriginalName, &e.DocSize,
	)
	return e, err
}

func (h *Handler) getEntry(ctx context.Context, where string, arg any) (entryRecord, error) {
	return scanEntry(h.pool.QueryRow(ctx, entryColumns+" WHERE "+where, arg))
}

// sign returns a presigned GET URL and its expiry. Storage being unavailable
// degrades to an empty URL rather than failing the whole read.
func (h *Handler) sign(ctx context.Context, key string) (string, string) {
	if key == "" || h.r2 == nil {
		return "", ""
	}
	ttl := mediaURLTTL
	u, err := h.r2.PresignedGetURL(ctx, key, ttl)
	if err != nil {
		slog.Warn("repository: presign media failed", "error", err)
		return "", ""
	}
	return u, time.Now().Add(ttl).UTC().Format(time.RFC3339)
}

func (h *Handler) coverFields(ctx context.Context, e entryRecord) (*publicImage, string, string) {
	if e.CoverObjectKey == nil || *e.CoverObjectKey == "" {
		return nil, "", ""
	}
	url, expires := h.sign(ctx, *e.CoverObjectKey)
	thumb, _ := h.sign(ctx, deref(e.CoverThumbKey))
	if thumb == "" {
		thumb = url
	}
	img := &publicImage{URL: url, ExpiresAt: expires}
	if e.CoverAlt != nil {
		img.Alt = *e.CoverAlt
	}
	if e.CoverCredit != nil {
		img.Credit = *e.CoverCredit
	}
	return img, url, thumb
}

func (h *Handler) documentField(ctx context.Context, e entryRecord) (*publicDocument, string) {
	if e.DocObjectKey == nil || *e.DocObjectKey == "" {
		return nil, ""
	}
	url, expires := h.sign(ctx, *e.DocObjectKey)
	name := ""
	if e.DocOriginalName != nil {
		name = *e.DocOriginalName
	}
	if name == "" {
		name = *e.DocObjectKey
		if i := lastSlash(name); i >= 0 {
			name = name[i+1:]
		}
	}
	size := int64(0)
	if e.DocSize != nil {
		size = *e.DocSize
	}
	doc := &publicDocument{
		URL:       url,
		FileName:  name,
		MimeType:  "application/pdf",
		SizeBytes: size,
		ExpiresAt: expires,
	}
	return doc, url
}

func lastSlash(s string) int {
	for i := len(s) - 1; i >= 0; i-- {
		if s[i] == '/' {
			return i
		}
	}
	return -1
}

func (e entryRecord) team() []teamMember {
	if len(e.TeamMembers) == 0 {
		return []teamMember{}
	}
	var members []teamMember
	if err := json.Unmarshal(e.TeamMembers, &members); err != nil {
		return []teamMember{}
	}
	out := make([]teamMember, 0, len(members))
	for _, m := range members {
		if m.Name != "" {
			out = append(out, m)
		}
	}
	return out
}

func (e entryRecord) embed() *embedInfo {
	if e.EmbedURL == nil || *e.EmbedURL == "" {
		return nil
	}
	return &embedInfo{Provider: deref(e.EmbedProvider), URL: *e.EmbedURL}
}

func (e entryRecord) summaryDTO(ctx context.Context, h *Handler) publicSummary {
	img, coverURL, thumbURL := h.coverFields(ctx, e)
	tags := e.Tags
	if tags == nil {
		tags = []string{}
	}
	return publicSummary{
		ID:                  e.ID,
		Kind:                e.Kind,
		Slug:                e.Slug,
		Title:               e.Title,
		Summary:             e.Summary,
		County:              e.County,
		Category:            e.Category,
		Tags:                tags,
		AuthorName:          deref(e.AuthorName),
		AuthorOrg:           e.AuthorOrg,
		CompetitionName:     e.CompetitionName,
		CompetitionCategory: e.CompetitionCategory,
		Featured:            e.Featured,
		CoverImage:          img,
		CoverURL:            coverURL,
		CoverThumbURL:       thumbURL,
		PublishedAt:         timePtr(e.PublishedAt),
	}
}

func (h *Handler) publicDetail(ctx context.Context, e entryRecord) publicDetail {
	gallery, err := h.loadGallery(ctx, e.ID)
	if err != nil {
		slog.Warn("repository: load gallery failed", "error", err)
		gallery = []*galleryItem{}
	}
	doc, docURL := h.documentField(ctx, e)
	createdAt, updatedAt := "", ""
	if e.CreatedAt != nil {
		createdAt = formatTime(*e.CreatedAt)
	}
	if e.UpdatedAt != nil {
		updatedAt = formatTime(*e.UpdatedAt)
	}
	return publicDetail{
		publicSummary: e.summaryDTO(ctx, h),
		BodyMD:        e.BodyMD,
		Gallery:       gallery,
		Document:      doc,
		DocumentURL:   docURL,
		ExternalURL:   e.ExternalURL,
		Embed:         e.embed(),
		TeamMembers:   e.team(),
		CreatedAt:     createdAt,
		UpdatedAt:     updatedAt,
	}
}

func (h *Handler) adminDTO(ctx context.Context, e entryRecord) adminEntry {
	return adminEntry{
		publicDetail:    h.publicDetail(ctx, e),
		Status:          e.Status,
		Source:          e.Source,
		ReferenceCode:   e.ReferenceCode,
		AuthorEmail:     e.AuthorEmail,
		AuthorPhone:     e.AuthorPhone,
		AuthorAgeBand:   e.AuthorAgeBand,
		ModerationNote:  e.ModerationNote,
		ReviewedBy:      e.ReviewedBy,
		ReviewedAt:      timePtr(e.ReviewedAt),
		ConsentAt:       timePtr(e.ConsentAt),
		ConsentVersion:  e.ConsentVersion,
		CoverMediaID:    e.CoverMediaID,
		DocumentMediaID: e.DocumentMediaID,
	}
}

// ---------------------------------------------------------------------------
// Gallery
// ---------------------------------------------------------------------------

const galleryQuery = `
SELECT em.entry_id::text, m.id::text, m.object_key, m.alt_text, m.caption, m.credit, em.sort_order
FROM repository_entry_media em
JOIN media_assets m ON m.id = em.media_id
WHERE em.entry_id = ANY($1::uuid[])
ORDER BY em.entry_id, em.sort_order, em.id`

func (h *Handler) loadGallery(ctx context.Context, entryID string) ([]*galleryItem, error) {
	grouped, err := h.loadGalleryBatch(ctx, []string{entryID})
	if err != nil {
		return nil, err
	}
	out := grouped[entryID]
	if out == nil {
		out = []*galleryItem{}
	}
	return out, nil
}

func (h *Handler) loadGalleryBatch(ctx context.Context, entryIDs []string) (map[string][]*galleryItem, error) {
	out := map[string][]*galleryItem{}
	if len(entryIDs) == 0 {
		return out, nil
	}
	rows, err := h.pool.Query(ctx, galleryQuery, entryIDs)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	for rows.Next() {
		var (
			entryID   string
			id        string
			key       string
			alt       *string
			caption   *string
			credit    *string
			sortOrder int
		)
		if err := rows.Scan(&entryID, &id, &key, &alt, &caption, &credit, &sortOrder); err != nil {
			return nil, err
		}
		url, expires := h.sign(ctx, key)
		item := &galleryItem{
			ID:        id,
			URL:       url,
			Alt:       deref(alt),
			Caption:   deref(caption),
			Credit:    deref(credit),
			SortOrder: sortOrder,
			ExpiresAt: expires,
		}
		out[entryID] = append(out[entryID], item)
	}
	return out, rows.Err()
}

// adminDTOs builds admin responses for a page, batching gallery lookups.
func (h *Handler) adminDTOs(ctx context.Context, entries []entryRecord) []adminEntry {
	ids := make([]string, 0, len(entries))
	for _, e := range entries {
		ids = append(ids, e.ID)
	}
	galleries, err := h.loadGalleryBatch(ctx, ids)
	if err != nil {
		slog.Warn("repository: load gallery batch failed", "error", err)
		galleries = map[string][]*galleryItem{}
	}

	out := make([]adminEntry, 0, len(entries))
	for _, e := range entries {
		gallery := galleries[e.ID]
		if gallery == nil {
			gallery = []*galleryItem{}
		}
		doc, docURL := h.documentField(ctx, e)
		createdAt, updatedAt := "", ""
		if e.CreatedAt != nil {
			createdAt = formatTime(*e.CreatedAt)
		}
		if e.UpdatedAt != nil {
			updatedAt = formatTime(*e.UpdatedAt)
		}
		detail := publicDetail{
			publicSummary: e.summaryDTO(ctx, h),
			BodyMD:        e.BodyMD,
			Gallery:       gallery,
			Document:      doc,
			DocumentURL:   docURL,
			ExternalURL:   e.ExternalURL,
			Embed:         e.embed(),
			TeamMembers:   e.team(),
			CreatedAt:     createdAt,
			UpdatedAt:     updatedAt,
		}
		out = append(out, adminEntry{
			publicDetail:    detail,
			Status:          e.Status,
			Source:          e.Source,
			ReferenceCode:   e.ReferenceCode,
			AuthorEmail:     e.AuthorEmail,
			AuthorPhone:     e.AuthorPhone,
			AuthorAgeBand:   e.AuthorAgeBand,
			ModerationNote:  e.ModerationNote,
			ReviewedBy:      e.ReviewedBy,
			ReviewedAt:      timePtr(e.ReviewedAt),
			ConsentAt:       timePtr(e.ConsentAt),
			ConsentVersion:  e.ConsentVersion,
			CoverMediaID:    e.CoverMediaID,
			DocumentMediaID: e.DocumentMediaID,
		})
	}
	return out
}
