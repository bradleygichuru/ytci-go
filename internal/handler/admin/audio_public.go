package admin

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/bradleygichuru/ytci-go/internal/model"
	"github.com/bradleygichuru/ytci-go/internal/pagination"
)

const audioPlaybackTTL = 6 * time.Hour

type publicAudioStorySummary struct {
	Slug            string     `json:"slug"`
	Title           string     `json:"title"`
	Subtitle        string     `json:"subtitle"`
	Summary         string     `json:"summary"`
	Theme           string     `json:"theme"`
	Tags            []string   `json:"tags"`
	CoverURL        string     `json:"coverUrl"`
	Producer        string     `json:"producer"`
	CommunitySource string     `json:"communitySource"`
	Counties        []string   `json:"counties"`
	Languages       []string   `json:"languages"`
	PublishedAt     *time.Time `json:"publishedAt"`

	id       string
	coverKey string
}

type publicAudioTrack struct {
	Language        string `json:"language"`
	DurationSeconds int    `json:"durationSeconds"`
	Narrator        string `json:"narrator"`
	URL             string `json:"url"`
	Format          string `json:"format"`
	SizeBytes       int64  `json:"sizeBytes"`
}

type publicAudioTranscript struct {
	Language   string                `json:"language"`
	Paragraphs []transcriptParagraph `json:"paragraphs"`
}

type publicAudioContentNotes struct {
	Categories []string `json:"categories"`
	Text       string   `json:"text"`
}

type publicAudioCollectionRef struct {
	ID    string `json:"id"`
	Slug  string `json:"slug"`
	Title string `json:"title"`
}

type publicAudioStoryDetail struct {
	ID              string                    `json:"id"`
	Slug            string                    `json:"slug"`
	Title           string                    `json:"title"`
	Subtitle        string                    `json:"subtitle"`
	Summary         string                    `json:"summary"`
	Theme           string                    `json:"theme"`
	Tags            []string                  `json:"tags"`
	CoverURL        string                    `json:"coverUrl"`
	Producer        string                    `json:"producer"`
	CommunitySource string                    `json:"communitySource"`
	RecordedAt      string                    `json:"recordedAt"`
	RightsLine      string                    `json:"rightsLine"`
	ReviewedAt      *time.Time                `json:"reviewedAt"`
	PublishedAt     *time.Time                `json:"publishedAt"`
	ContentVersion  int                       `json:"contentVersion"`
	Collection      *publicAudioCollectionRef `json:"collection,omitempty"`
	Tracks          []publicAudioTrack        `json:"tracks"`
	Transcripts     []publicAudioTranscript   `json:"transcripts"`
	ContentNotes    publicAudioContentNotes   `json:"contentNotes"`
}

type publicAudioCollection struct {
	Slug                string `json:"slug"`
	Title               string `json:"title"`
	Description         string `json:"description"`
	CoverURL            string `json:"coverUrl"`
	StoryCount          int    `json:"storyCount"`
	PublishedStoryCount int    `json:"publishedStoryCount"`
}

type publicStoryQuery struct {
	CollectionSlug string
	Theme          string
	County         string
	Search         string
	Language       string
	ExcludeID      string
	CursorTime     string
	CursorID       string
	Limit          int
}

func (h *AudioStoryHandler) presignAudio(ctx context.Context, objectKey string, ttl time.Duration) string {
	if h.store == nil || strings.TrimSpace(objectKey) == "" {
		return ""
	}
	url, err := h.store.PresignedGetURL(ctx, objectKey, ttl)
	if err != nil {
		slog.Warn("presign audio story media", "key", objectKey, "error", err)
		return ""
	}
	return url
}

// ---------------------------------------------------------------------------
// Public stories

func (h *AudioStoryHandler) PublicListStories(w http.ResponseWriter, r *http.Request) {
	pr := pagination.ParseRequest(r)
	q := publicStoryQuery{
		CollectionSlug: strings.TrimSpace(r.URL.Query().Get("collection")),
		Theme:          strings.TrimSpace(r.URL.Query().Get("theme")),
		County:         strings.TrimSpace(r.URL.Query().Get("county")),
		Search:         strings.TrimSpace(r.URL.Query().Get("q")),
		Language:       strings.TrimSpace(r.URL.Query().Get("language")),
		Limit:          pr.Limit + 1,
	}
	if pr.Cursor != nil {
		q.CursorTime = pr.Cursor.SortValue
		q.CursorID = pr.Cursor.ID
	}
	items, err := h.listPublicStories(r.Context(), q)
	if err != nil {
		audioFail(w, http.StatusInternalServerError, "INTERNAL_ERROR", "failed to list audio stories")
		return
	}
	hasMore := len(items) > pr.Limit
	if hasMore {
		items = items[:pr.Limit]
	}
	resp := model.Paginated[publicAudioStorySummary]{Items: items, HasMore: hasMore}
	if hasMore && len(items) > 0 {
		last := items[len(items)-1]
		if last.PublishedAt != nil {
			cursor := pagination.EncodeCursor(last.PublishedAt.UTC().Format(time.RFC3339Nano), last.id)
			resp.NextCursor = &cursor
		}
	}
	writeJSON(w, http.StatusOK, resp)
}

func (h *AudioStoryHandler) listPublicStories(ctx context.Context, q publicStoryQuery) ([]publicAudioStorySummary, error) {
	if q.Limit <= 0 {
		q.Limit = 20
	}
	rows, err := h.pool.Query(ctx,
		`SELECT s.id,s.slug,s.title,COALESCE(s.subtitle,''),s.summary,s.theme,s.tags,
		 COALESCE(m.object_key,''),s.producer,s.community_source,lr.counties,
		 COALESCE((SELECT array_agg(t.language ORDER BY t.language) FROM audio_story_tracks t
		           WHERE t.revision_id=lr.id),'{}'),
		 COALESCE(s.first_published_at,lr.published_at,s.created_at)
		 FROM audio_stories s
		 JOIN audio_story_revisions lr ON lr.id=s.live_revision_id
		 JOIN audio_story_collections c ON c.id=s.collection_id
		 LEFT JOIN media_assets m ON m.id=s.cover_media_id
		 WHERE s.status='published'
		   AND lr.status='published'
		   AND lr.revoked_at IS NULL
		   AND c.status='published'
		   AND (lr.rights_expires_at IS NULL OR lr.rights_expires_at > now())
		   AND ($1='' OR c.slug=$1)
		   AND ($2='' OR s.theme=$2)
		   AND ($3='' OR $3=ANY(lr.counties))
		   AND ($4='' OR s.title ILIKE '%'||$4||'%' OR s.summary ILIKE '%'||$4||'%')
		   AND ($5='' OR EXISTS (SELECT 1 FROM audio_story_tracks t
		                          WHERE t.revision_id=lr.id AND t.language=$5))
		   AND ($6='' OR s.id<>NULLIF($6,'')::uuid)
		   AND (NULLIF($7,'')::timestamptz IS NULL
		        OR (COALESCE(s.first_published_at,lr.published_at,s.created_at),s.id)
		           < (NULLIF($7,'')::timestamptz AT TIME ZONE 'UTC',NULLIF($8,'')::uuid))
		 ORDER BY COALESCE(s.first_published_at,lr.published_at,s.created_at) DESC,s.id DESC
		 LIMIT $9`,
		q.CollectionSlug, q.Theme, q.County, q.Search, q.Language, q.ExcludeID,
		q.CursorTime, q.CursorID, q.Limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	items := make([]publicAudioStorySummary, 0)
	for rows.Next() {
		var item publicAudioStorySummary
		var published pgtype.Timestamp
		if err := rows.Scan(&item.id, &item.Slug, &item.Title, &item.Subtitle, &item.Summary,
			&item.Theme, &item.Tags, &item.coverKey, &item.Producer, &item.CommunitySource,
			&item.Counties, &item.Languages, &published); err != nil {
			return nil, err
		}
		item.Tags = nonNilStrings(item.Tags)
		item.Counties = nonNilStrings(item.Counties)
		item.Languages = nonNilStrings(item.Languages)
		item.PublishedAt = tsPtr(published)
		item.CoverURL = h.presignAudio(ctx, item.coverKey, audioPlaybackTTL)
		items = append(items, item)
	}
	return items, rows.Err()
}

func (h *AudioStoryHandler) PublicGetStory(w http.ResponseWriter, r *http.Request) {
	slug := chi.URLParam(r, "slug")
	ctx := r.Context()
	var detail publicAudioStoryDetail
	var storyID, coverKey, collectionID, collectionSlug, collectionTitle string
	var rightsBasis, rightsHolder, licence, noteText string
	var counties, noteCategories []string
	var recorded pgtype.Date
	var approved, published pgtype.Timestamp
	var tags []string

	err := h.pool.QueryRow(ctx,
		`SELECT s.id,s.slug,s.title,COALESCE(s.subtitle,''),s.summary,s.theme,s.tags,
		 COALESCE(m.object_key,''),s.producer,s.community_source,s.recorded_at,
		 c.id,c.slug,c.title,lr.counties,lr.content_note_categories,
		 COALESCE(lr.content_note_text,''),lr.rights_basis,lr.rights_holder,
		 COALESCE(lr.licence_reference,''),lr.approved_at,
		 COALESCE(s.first_published_at,lr.published_at,s.created_at),s.content_version
		 FROM audio_stories s
		 JOIN audio_story_revisions lr ON lr.id=s.live_revision_id
		 JOIN audio_story_collections c ON c.id=s.collection_id
		 LEFT JOIN media_assets m ON m.id=s.cover_media_id
		 WHERE s.slug=$1
		   AND s.status='published'
		   AND lr.status='published'
		   AND lr.revoked_at IS NULL
		   AND c.status='published'
		   AND (lr.rights_expires_at IS NULL OR lr.rights_expires_at > now())`, slug).Scan(
		&storyID, &detail.Slug, &detail.Title, &detail.Subtitle, &detail.Summary, &detail.Theme,
		&tags, &coverKey, &detail.Producer, &detail.CommunitySource, &recorded,
		&collectionID, &collectionSlug, &collectionTitle, &counties, &noteCategories,
		&noteText, &rightsBasis, &rightsHolder, &licence, &approved, &published, &detail.ContentVersion)
	if err != nil {
		audioFail(w, http.StatusNotFound, "NOT_FOUND", "audio story not found")
		return
	}

	detail.ID = storyID
	detail.Tags = nonNilStrings(tags)
	detail.RecordedAt = dateString(recorded)
	detail.RightsLine = audioRightsLine(rightsBasis, rightsHolder, licence)
	detail.ReviewedAt = tsPtr(approved)
	detail.PublishedAt = tsPtr(published)
	detail.CoverURL = h.presignAudio(ctx, coverKey, audioPlaybackTTL)
	detail.Collection = &publicAudioCollectionRef{ID: collectionID, Slug: collectionSlug, Title: collectionTitle}
	detail.ContentNotes = publicAudioContentNotes{
		Categories: nonNilStrings(noteCategories),
		Text:       noteText,
	}

	tracks, err := h.loadPublicTracks(ctx, storyID)
	if err != nil {
		audioFail(w, http.StatusInternalServerError, "INTERNAL_ERROR", "failed to load audio tracks")
		return
	}
	transcripts, err := h.loadPublicTranscripts(ctx, storyID)
	if err != nil {
		audioFail(w, http.StatusInternalServerError, "INTERNAL_ERROR", "failed to load transcripts")
		return
	}
	detail.Tracks = tracks
	detail.Transcripts = transcripts

	related, err := h.listPublicStories(ctx, publicStoryQuery{
		Theme: detail.Theme, ExcludeID: storyID, Limit: 3,
	})
	if err != nil {
		related = []publicAudioStorySummary{}
	}
	if related == nil {
		related = []publicAudioStorySummary{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"story": detail, "related": related})
}

func (h *AudioStoryHandler) loadPublicTracks(ctx context.Context, storyID string) ([]publicAudioTrack, error) {
	rows, err := h.pool.Query(ctx,
		`SELECT t.language,t.duration_seconds,t.narrator,t.audio_object_key,
		 COALESCE(t.format,''),COALESCE(t.size_bytes,0)
		 FROM audio_story_tracks t
		 JOIN audio_stories s ON s.live_revision_id=t.revision_id
		 WHERE s.id=$1 ORDER BY t.language`, storyID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := make([]publicAudioTrack, 0)
	for rows.Next() {
		var item publicAudioTrack
		var objectKey string
		if err := rows.Scan(&item.Language, &item.DurationSeconds, &item.Narrator, &objectKey,
			&item.Format, &item.SizeBytes); err != nil {
			return nil, err
		}
		item.URL = h.presignAudio(ctx, objectKey, audioPlaybackTTL)
		items = append(items, item)
	}
	return items, rows.Err()
}

func (h *AudioStoryHandler) loadPublicTranscripts(ctx context.Context, storyID string) ([]publicAudioTranscript, error) {
	rows, err := h.pool.Query(ctx,
		`SELECT tr.language,tr.paragraphs
		 FROM audio_story_transcripts tr
		 JOIN audio_stories s ON s.live_revision_id=tr.revision_id
		 WHERE s.id=$1 ORDER BY tr.language`, storyID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := make([]publicAudioTranscript, 0)
	for rows.Next() {
		var item publicAudioTranscript
		var raw []byte
		if err := rows.Scan(&item.Language, &raw); err != nil {
			return nil, err
		}
		_ = json.Unmarshal(raw, &item.Paragraphs)
		item.Paragraphs = nonNilParagraphs(item.Paragraphs)
		items = append(items, item)
	}
	return items, rows.Err()
}

func nonNilParagraphs(values []transcriptParagraph) []transcriptParagraph {
	if values == nil {
		return []transcriptParagraph{}
	}
	return values
}

func audioRightsLine(basis, holder, licence string) string {
	switch basis {
	case "ytci_owned":
		return "© YTCI. All rights reserved."
	case "public_domain":
		return "Public domain."
	case "partner_permission":
		line := "Published with permission from " + holder + "."
		if strings.TrimSpace(licence) != "" {
			line += " Reference: " + licence + "."
		}
		return line
	case "licensed":
		line := "Licensed from " + holder + "."
		if strings.TrimSpace(licence) != "" {
			line += " Licence: " + licence + "."
		}
		return line
	}
	if strings.TrimSpace(holder) != "" {
		return "Rights holder: " + holder + "."
	}
	return ""
}

// ---------------------------------------------------------------------------
// Public collections

func (h *AudioStoryHandler) PublicListCollections(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	rows, err := h.pool.Query(ctx,
		`SELECT c.slug,c.title,c.description,COALESCE(m.object_key,''),
		 (SELECT count(*) FROM audio_stories s WHERE s.collection_id=c.id),
		 (SELECT count(*) FROM audio_stories s
		  JOIN audio_story_revisions lr ON lr.id=s.live_revision_id
		  WHERE s.collection_id=c.id AND s.status='published' AND lr.status='published'
		    AND lr.revoked_at IS NULL
		    AND (lr.rights_expires_at IS NULL OR lr.rights_expires_at > now()))
		 FROM audio_story_collections c
		 LEFT JOIN media_assets m ON m.id=c.cover_media_id
		 WHERE c.status='published'
		   AND EXISTS (SELECT 1 FROM audio_stories s
		               JOIN audio_story_revisions lr ON lr.id=s.live_revision_id
		               WHERE s.collection_id=c.id AND s.status='published' AND lr.status='published'
		                 AND lr.revoked_at IS NULL
		                 AND (lr.rights_expires_at IS NULL OR lr.rights_expires_at > now()))
		 ORDER BY c.display_order,c.title`)
	if err != nil {
		audioFail(w, http.StatusInternalServerError, "INTERNAL_ERROR", "failed to list audio story collections")
		return
	}
	defer rows.Close()
	items := make([]publicAudioCollection, 0)
	for rows.Next() {
		var item publicAudioCollection
		var coverKey string
		if err := rows.Scan(&item.Slug, &item.Title, &item.Description, &coverKey,
			&item.StoryCount, &item.PublishedStoryCount); err != nil {
			audioFail(w, http.StatusInternalServerError, "INTERNAL_ERROR", "failed to read an audio story collection")
			return
		}
		item.CoverURL = h.presignAudio(ctx, coverKey, audioPlaybackTTL)
		items = append(items, item)
	}
	if err := rows.Err(); err != nil {
		audioFail(w, http.StatusInternalServerError, "INTERNAL_ERROR", "failed to list audio story collections")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items})
}

func (h *AudioStoryHandler) PublicGetCollection(w http.ResponseWriter, r *http.Request) {
	slug := chi.URLParam(r, "slug")
	ctx := r.Context()
	var collection publicAudioCollection
	var coverKey string
	err := h.pool.QueryRow(ctx,
		`SELECT c.slug,c.title,c.description,COALESCE(m.object_key,''),
		 (SELECT count(*) FROM audio_stories s WHERE s.collection_id=c.id),
		 (SELECT count(*) FROM audio_stories s
		  JOIN audio_story_revisions lr ON lr.id=s.live_revision_id
		  WHERE s.collection_id=c.id AND s.status='published' AND lr.status='published'
		    AND lr.revoked_at IS NULL
		    AND (lr.rights_expires_at IS NULL OR lr.rights_expires_at > now()))
		 FROM audio_story_collections c
		 LEFT JOIN media_assets m ON m.id=c.cover_media_id
		 WHERE c.slug=$1 AND c.status='published'`, slug).Scan(
		&collection.Slug, &collection.Title, &collection.Description, &coverKey,
		&collection.StoryCount, &collection.PublishedStoryCount)
	if err != nil || collection.PublishedStoryCount == 0 {
		audioFail(w, http.StatusNotFound, "NOT_FOUND", "audio story collection not found")
		return
	}
	collection.CoverURL = h.presignAudio(ctx, coverKey, audioPlaybackTTL)
	stories, err := h.listPublicStories(ctx, publicStoryQuery{CollectionSlug: slug, Limit: 100})
	if err != nil {
		audioFail(w, http.StatusInternalServerError, "INTERNAL_ERROR", "failed to load collection stories")
		return
	}
	if stories == nil {
		stories = []publicAudioStorySummary{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"collection": collection, "stories": stories})
}
