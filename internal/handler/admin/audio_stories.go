package admin

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/bradleygichuru/ytci-go/internal/middleware"
)

// AudioStoryHandler serves the admin draft workflow for Audio Stories.
// Review, publish, scheduling and public reads live in #266.
type AudioStoryHandler struct{ pool *pgxpool.Pool }

func NewAudioStoryHandler(pool *pgxpool.Pool) *AudioStoryHandler {
	return &AudioStoryHandler{pool: pool}
}

// ---------------------------------------------------------------------------
// Requests

type audioStoryCreateRequest struct {
	Slug            string   `json:"slug"`
	Title           string   `json:"title"`
	Subtitle        string   `json:"subtitle"`
	Summary         string   `json:"summary"`
	Theme           string   `json:"theme"`
	Tags            []string `json:"tags"`
	CoverMediaID    string   `json:"coverMediaId"`
	Producer        string   `json:"producer"`
	CommunitySource string   `json:"communitySource"`
	RecordedAt      string   `json:"recordedAt"`
	CollectionID    string   `json:"collectionId"`
	PlaceScope      string   `json:"placeScope"`
	Counties        []string `json:"counties"`
	RightsBasis     string   `json:"rightsBasis"`
	RightsHolder    string   `json:"rightsHolder"`
	LicenceRef      string   `json:"licenceReference"`
	RiskTier        string   `json:"riskTier"`
}

type audioStoryUpdateRequest struct {
	Title           *string   `json:"title"`
	Subtitle        *string   `json:"subtitle"`
	Summary         *string   `json:"summary"`
	Theme           *string   `json:"theme"`
	Tags            *[]string `json:"tags"`
	CoverMediaID    *string   `json:"coverMediaId"`
	Producer        *string   `json:"producer"`
	CommunitySource *string   `json:"communitySource"`
	RecordedAt      *string   `json:"recordedAt"`
	CollectionID    *string   `json:"collectionId"`
	CollectionOrder *int      `json:"collectionOrder"`
	Featured        *bool     `json:"featured"`
	ExpectedUpdated string    `json:"expectedUpdatedAt"`
}

type audioRevisionCreateRequest struct {
	SourceRevisionID string `json:"sourceRevisionId"`
}

type audioRevisionUpdateRequest struct {
	PlaceScope            *string   `json:"placeScope"`
	Counties              *[]string `json:"counties"`
	DestinationIDs        *[]string `json:"destinationIds"`
	ContentNoteCategories *[]string `json:"contentNoteCategories"`
	ContentNoteText       *string   `json:"contentNoteText"`
	RightsBasis           *string   `json:"rightsBasis"`
	RightsHolder          *string   `json:"rightsHolder"`
	LicenceReference      *string   `json:"licenceReference"`
	RightsExpiresAt       *string   `json:"rightsExpiresAt"`
	RiskTier              *string   `json:"riskTier"`
	ExpectedUpdated       string    `json:"expectedUpdatedAt"`
}

type audioTrackRequest struct {
	Language                 string `json:"language"`
	MediaAssetID             string `json:"mediaAssetId"`
	AudioObjectKey           string `json:"audioObjectKey"`
	DurationSeconds          int    `json:"durationSeconds"`
	Narrator                 string `json:"narrator"`
	NarratorConsentDate      string `json:"narratorConsentDate"`
	NarratorConsentReference string `json:"narratorConsentReference"`
	Format                   string `json:"format"`
	RecordedAt               string `json:"recordedAt"`
	SizeBytes                int64  `json:"sizeBytes"`
	BitrateKbps              int    `json:"bitrateKbps"`
}

type audioTrackUpdateRequest struct {
	DurationSeconds          *int    `json:"durationSeconds"`
	Narrator                 *string `json:"narrator"`
	NarratorConsentDate      *string `json:"narratorConsentDate"`
	NarratorConsentReference *string `json:"narratorConsentReference"`
	Format                   *string `json:"format"`
	RecordedAt               *string `json:"recordedAt"`
	SizeBytes                *int64  `json:"sizeBytes"`
	BitrateKbps              *int    `json:"bitrateKbps"`
}

type transcriptParagraph struct {
	Text         string   `json:"text"`
	Speaker      string   `json:"speaker,omitempty"`
	StartSeconds *float64 `json:"startSeconds,omitempty"`
}

type transcriptRequest struct {
	Paragraphs []transcriptParagraph `json:"paragraphs"`
}

// ---------------------------------------------------------------------------
// Responses

type readinessItem struct {
	Code    string `json:"code"`
	Label   string `json:"label"`
	Section string `json:"section"`
}

type audioReadiness struct {
	Ready    bool            `json:"ready"`
	Blocking []readinessItem `json:"blocking"`
	Warnings []readinessItem `json:"warnings"`
}

type audioStorySummary struct {
	ID                   string     `json:"id"`
	Slug                 string     `json:"slug"`
	Title                string     `json:"title"`
	Summary              string     `json:"summary"`
	Theme                string     `json:"theme"`
	Status               string     `json:"status"`
	CollectionID         string     `json:"collectionId,omitempty"`
	CollectionTitle      string     `json:"collectionTitle,omitempty"`
	CollectionOrder      int        `json:"collectionOrder"`
	Featured             bool       `json:"featured"`
	ContentVersion       int        `json:"contentVersion"`
	LiveRevisionID       string     `json:"liveRevisionId,omitempty"`
	LiveStatus           string     `json:"liveRevisionStatus,omitempty"`
	OpenRevisionID       string     `json:"openRevisionId,omitempty"`
	OpenRevisionStat     string     `json:"openRevisionStatus,omitempty"`
	RiskTier             string     `json:"riskTier,omitempty"`
	Languages            []string   `json:"languages"`
	AssignedReviewerID   string     `json:"assignedReviewerId,omitempty"`
	AssignedReviewerName string     `json:"assignedReviewerName,omitempty"`
	RightsExpiresAt      *time.Time `json:"rightsExpiresAt,omitempty"`
	UpdatedAt            *time.Time `json:"updatedAt"`
}

type audioStoryDetail struct {
	audioStorySummary
	Subtitle        string   `json:"subtitle,omitempty"`
	Tags            []string `json:"tags"`
	CoverMediaID    string   `json:"coverMediaId,omitempty"`
	CoverObjectKey  string   `json:"coverObjectKey,omitempty"`
	Producer        string   `json:"producer"`
	CommunitySource string   `json:"communitySource"`
	RecordedAt      string   `json:"recordedAt,omitempty"`
	CreatedAt       string   `json:"createdAt,omitempty"`
}

type audioTrackDetail struct {
	ID                       string  `json:"id"`
	Language                 string  `json:"language"`
	MediaAssetID             string  `json:"mediaAssetId,omitempty"`
	AudioObjectKey           string  `json:"audioObjectKey"`
	DurationSeconds          int     `json:"durationSeconds"`
	Narrator                 string  `json:"narrator"`
	NarratorConsentDate      string  `json:"narratorConsentDate,omitempty"`
	NarratorConsentReference string  `json:"narratorConsentReference,omitempty"`
	SizeBytes                int64   `json:"sizeBytes,omitempty"`
	Format                   string  `json:"format,omitempty"`
	BitrateKbps              int     `json:"bitrateKbps,omitempty"`
	RecordedAt               string  `json:"recordedAt,omitempty"`
	CreatedAt                string  `json:"createdAt,omitempty"`
	UpdatedAt                *string `json:"updatedAt,omitempty"`
}

type audioTranscriptDetail struct {
	Language   string                `json:"language"`
	Paragraphs []transcriptParagraph `json:"paragraphs"`
	Source     string                `json:"source,omitempty"`
	ReviewedAt string                `json:"reviewedAt,omitempty"`
	ReviewedBy string                `json:"reviewedBy,omitempty"`
}

type audioRevisionDetail struct {
	ID                    string                  `json:"id"`
	StoryID               string                  `json:"storyId"`
	Status                string                  `json:"status"`
	ContentVersion        int                     `json:"contentVersion"`
	PlaceScope            string                  `json:"placeScope"`
	Counties              []string                `json:"counties"`
	DestinationIDs        []string                `json:"destinationIds"`
	ContentNoteCategories []string                `json:"contentNoteCategories"`
	ContentNoteText       string                  `json:"contentNoteText,omitempty"`
	RightsBasis           string                  `json:"rightsBasis"`
	RightsHolder          string                  `json:"rightsHolder"`
	LicenceReference      string                  `json:"licenceReference,omitempty"`
	RightsExpiresAt       *time.Time              `json:"rightsExpiresAt,omitempty"`
	RiskTier              string                  `json:"riskTier"`
	AttestedBy            string                  `json:"attestedBy,omitempty"`
	AttestedAt            *time.Time              `json:"attestedAt,omitempty"`
	AssignedReviewerID    string                  `json:"assignedReviewerId,omitempty"`
	AssignedReviewerName  string                  `json:"assignedReviewerName,omitempty"`
	SecondReviewerID      string                  `json:"secondReviewerId,omitempty"`
	SubmittedAt           *time.Time              `json:"submittedAt,omitempty"`
	ApprovedAt            *time.Time              `json:"approvedAt,omitempty"`
	PublishAt             *time.Time              `json:"publishAt,omitempty"`
	PublishedAt           *time.Time              `json:"publishedAt,omitempty"`
	RevokedAt             *time.Time              `json:"revokedAt,omitempty"`
	RevocationReason      string                  `json:"revocationReason,omitempty"`
	CreatedBy             string                  `json:"createdBy,omitempty"`
	CreatedAt             *time.Time              `json:"createdAt"`
	UpdatedAt             *time.Time              `json:"updatedAt"`
	Tracks                []audioTrackDetail      `json:"tracks"`
	Transcripts           []audioTranscriptDetail `json:"transcripts"`
}

type audioEventDetail struct {
	ID           string          `json:"id"`
	RevisionID   string          `json:"revisionId,omitempty"`
	TrackID      string          `json:"trackId,omitempty"`
	CollectionID string          `json:"collectionId,omitempty"`
	ActorID      string          `json:"actorId,omitempty"`
	ActorName    string          `json:"actorName,omitempty"`
	Action       string          `json:"action"`
	FromStatus   string          `json:"fromStatus,omitempty"`
	ToStatus     string          `json:"toStatus,omitempty"`
	Reason       string          `json:"reason,omitempty"`
	Metadata     json.RawMessage `json:"metadata,omitempty"`
	CreatedAt    *time.Time      `json:"createdAt"`
}

// ---------------------------------------------------------------------------
// Snapshots used by readiness

type storySnapshot struct {
	Title, Summary, Producer, Community, CoverID string
	CollectionID, CollectionState                string
}

type revisionSnapshot struct {
	PlaceScope, RightsBasis, RightsHolder, RiskTier, Note, LicenceReference string
	Counties                                                                []string
	RightsExpiresAt                                                         *time.Time
}

type trackSnapshot struct {
	Language, Narrator, ObjectKey string
	ConsentDate, ConsentRef       string
	Duration                      int
}

type transcriptSnapshot struct {
	Language   string
	Paragraphs []transcriptParagraph
}

// ---------------------------------------------------------------------------
// Lookups and helpers

var audioThemes = map[string]bool{
	"culture_heritage": true, "nature_wildlife": true, "history_places": true,
	"conservation_action": true, "youth_perspectives": true,
}

var audioRights = map[string]bool{
	"ytci_owned": true, "partner_permission": true, "licensed": true, "public_domain": true,
}

func audioWriter(r *http.Request) bool {
	switch string(middleware.RoleFromCtx(r.Context())) {
	case "super_admin", "administrator", "content_editor":
		return true
	}
	return false
}

func audioFail(w http.ResponseWriter, status int, code, message string) {
	writeJSON(w, status, map[string]any{"error": map[string]string{"code": code, "message": message}})
}

func decodeAudioBody(w http.ResponseWriter, r *http.Request, dst any) bool {
	if err := json.NewDecoder(r.Body).Decode(dst); err != nil {
		audioFail(w, http.StatusBadRequest, "INVALID_REQUEST", "invalid request body")
		return false
	}
	return true
}

func valueOr[T any](p *T, fallback T) T {
	if p == nil {
		return fallback
	}
	return *p
}

func tsPtr(t pgtype.Timestamp) *time.Time {
	if !t.Valid {
		return nil
	}
	v := t.Time.UTC()
	return &v
}

func dateString(t pgtype.Date) string {
	if !t.Valid {
		return ""
	}
	return t.Time.Format("2006-01-02")
}

func textPtr(t pgtype.Text) string { return t.String }

type audioExecer interface {
	Exec(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error)
}

func writeAudioEvent(ctx context.Context, execer audioExecer, storyID, revisionID, trackID, actor, action, from, to, reason string, metadata any) error {
	raw := ""
	if metadata != nil {
		encoded, _ := json.Marshal(metadata)
		raw = string(encoded)
	}
	_, err := execer.Exec(ctx,
		`INSERT INTO audio_story_events (story_id, revision_id, track_id, actor_id, action, from_status, to_status, reason, metadata)
		 VALUES ($1,NULLIF($2,'')::uuid,NULLIF($3,'')::uuid,NULLIF($4,'')::text,$5,NULLIF($6,''),NULLIF($7,''),NULLIF($8,''),NULLIF($9,'')::jsonb)`,
		storyID, revisionID, trackID, actor, action, from, to, reason, raw)
	return err
}

// ---------------------------------------------------------------------------
// Stories

func (h *AudioStoryHandler) List(w http.ResponseWriter, r *http.Request) {
	status := r.URL.Query().Get("status")
	collectionID := r.URL.Query().Get("collectionId")
	search := r.URL.Query().Get("q")
	rows, err := h.pool.Query(r.Context(),
		`SELECT s.id,s.slug,s.title,s.summary,s.theme,s.status,COALESCE(s.collection_id::text,''),
		 COALESCE(c.title,''),s.collection_order,s.featured,s.content_version,
		 COALESCE(s.live_revision_id::text,''),COALESCE(lr.status,''),
		 COALESCE(s.open_revision_id::text,''),COALESCE(orv.status,''),
		 COALESCE(orv.risk_tier,lr.risk_tier,''),COALESCE(orv.assigned_reviewer_id::text,''),
		 COALESCE(ru.name,''),COALESCE(orv.rights_expires_at,lr.rights_expires_at),
		 COALESCE((SELECT array_agg(t.language ORDER BY t.language) FROM audio_story_tracks t
		           WHERE t.revision_id=COALESCE(s.open_revision_id,s.live_revision_id)),'{}'),
		 s.updated_at
		 FROM audio_stories s
		 LEFT JOIN audio_story_collections c ON c.id=s.collection_id
		 LEFT JOIN audio_story_revisions lr ON lr.id=s.live_revision_id
		 LEFT JOIN audio_story_revisions orv ON orv.id=s.open_revision_id
		 LEFT JOIN users ru ON ru.id=orv.assigned_reviewer_id
		 WHERE ($1='' OR s.status=$1)
		   AND ($2='' OR s.collection_id=NULLIF($2,'')::uuid)
		   AND ($3='' OR s.title ILIKE '%'||$3||'%' OR s.slug ILIKE '%'||$3||'%')
		 ORDER BY s.updated_at DESC LIMIT 200`, status, collectionID, search)
	if err != nil {
		audioFail(w, http.StatusInternalServerError, "INTERNAL_ERROR", "failed to list audio stories")
		return
	}
	defer rows.Close()

	items := make([]audioStorySummary, 0)
	for rows.Next() {
		var item audioStorySummary
		var updated pgtype.Timestamp
		var expires pgtype.Timestamp
		if err := rows.Scan(&item.ID, &item.Slug, &item.Title, &item.Summary, &item.Theme, &item.Status,
			&item.CollectionID, &item.CollectionTitle, &item.CollectionOrder, &item.Featured, &item.ContentVersion,
			&item.LiveRevisionID, &item.LiveStatus, &item.OpenRevisionID, &item.OpenRevisionStat,
			&item.RiskTier, &item.AssignedReviewerID, &item.AssignedReviewerName, &expires, &item.Languages, &updated); err != nil {
			audioFail(w, http.StatusInternalServerError, "INTERNAL_ERROR", "failed to read audio story")
			return
		}
		item.RightsExpiresAt = tsPtr(expires)
		item.Languages = nonNilStrings(item.Languages)
		item.UpdatedAt = tsPtr(updated)
		items = append(items, item)
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items})
}

func (h *AudioStoryHandler) ListCollections(w http.ResponseWriter, r *http.Request) {
	rows, err := h.pool.Query(r.Context(),
		`SELECT id,slug,title,description,status,display_order,featured
		 FROM audio_story_collections ORDER BY display_order,title`)
	if err != nil {
		audioFail(w, http.StatusInternalServerError, "INTERNAL_ERROR", "failed to list audio story collections")
		return
	}
	defer rows.Close()
	items := make([]map[string]any, 0)
	for rows.Next() {
		var id, slug, title, description, status string
		var order int
		var featured bool
		if err := rows.Scan(&id, &slug, &title, &description, &status, &order, &featured); err != nil {
			audioFail(w, http.StatusInternalServerError, "INTERNAL_ERROR", "failed to read audio story collection")
			return
		}
		items = append(items, map[string]any{
			"id": id, "slug": slug, "title": title, "description": description,
			"status": status, "displayOrder": order, "featured": featured,
		})
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items})
}

func (h *AudioStoryHandler) Create(w http.ResponseWriter, r *http.Request) {
	if !audioWriter(r) {
		audioFail(w, http.StatusForbidden, "FORBIDDEN", "audio writer access required")
		return
	}
	var req audioStoryCreateRequest
	if !decodeAudioBody(w, r, &req) {
		return
	}
	if req.Slug == "" || req.Title == "" || req.Summary == "" || !audioThemes[req.Theme] ||
		req.Producer == "" || req.CommunitySource == "" {
		audioFail(w, http.StatusBadRequest, "VALIDATION_FAILED", "slug, title, summary, theme, producer and community source are required")
		return
	}
	if req.RightsBasis == "" {
		req.RightsBasis = "unverified"
	}
	if req.RightsBasis != "unverified" && !audioRights[req.RightsBasis] {
		audioFail(w, http.StatusBadRequest, "VALIDATION_FAILED", "invalid rights basis")
		return
	}
	if req.PlaceScope == "" {
		req.PlaceScope = "national"
	}
	if req.RiskTier == "" {
		req.RiskTier = "standard"
	}
	if !validPlace(req.PlaceScope, req.Counties) {
		audioFail(w, http.StatusBadRequest, "VALIDATION_FAILED", "place scope and counties are inconsistent")
		return
	}
	if !validRisk(req.RiskTier) {
		audioFail(w, http.StatusBadRequest, "VALIDATION_FAILED", "invalid risk tier")
		return
	}
	if req.RightsBasis != "unverified" && !rightsEvidenceOK(req.RightsBasis, req.LicenceRef) {
		audioFail(w, http.StatusBadRequest, "VALIDATION_FAILED", "a licence or permission reference is required for this rights basis")
		return
	}

	actor := middleware.UserID(r.Context())
	tx, err := h.pool.Begin(r.Context())
	if err != nil {
		audioFail(w, http.StatusInternalServerError, "INTERNAL_ERROR", "failed to start transaction")
		return
	}
	defer tx.Rollback(r.Context())

	var storyID, revisionID string
	if err := tx.QueryRow(r.Context(),
		`INSERT INTO audio_stories (slug,title,subtitle,summary,theme,tags,cover_media_id,producer,
		 community_source,recorded_at,collection_id,status,content_version,created_by,updated_by)
		 VALUES ($1,$2,NULLIF($3,''),$4,$5,$6,NULLIF($7,'')::uuid,$8,$9,NULLIF($10,'')::date,
		 NULLIF($11,'')::uuid,'draft',0,$12,$12) RETURNING id`,
		req.Slug, req.Title, req.Subtitle, req.Summary, req.Theme, nonNilStrings(req.Tags),
		req.CoverMediaID, req.Producer, req.CommunitySource, req.RecordedAt,
		req.CollectionID, actor).Scan(&storyID); err != nil {
		audioFail(w, http.StatusConflict, "SLUG_TAKEN", "slug is already in use or the payload is invalid")
		return
	}
	if err := tx.QueryRow(r.Context(),
		`INSERT INTO audio_story_revisions (story_id,status,place_scope,counties,destination_ids,
		 content_note_categories,content_note_text,rights_basis,rights_holder,licence_reference,risk_tier,created_by)
		 VALUES ($1,'draft',$2,$3,'{}'::uuid[],'{}'::text[],'',$4,$5,NULLIF($6,''),$7,$8) RETURNING id`,
		storyID, req.PlaceScope, nonNilStrings(req.Counties), req.RightsBasis, req.RightsHolder,
		req.LicenceRef, req.RiskTier, actor).Scan(&revisionID); err != nil {
		audioFail(w, http.StatusInternalServerError, "INTERNAL_ERROR", "failed to create draft revision")
		return
	}
	if _, err := tx.Exec(r.Context(),
		`UPDATE audio_stories SET open_revision_id=$1 WHERE id=$2`, revisionID, storyID); err != nil {
		audioFail(w, http.StatusInternalServerError, "INTERNAL_ERROR", "failed to link draft revision")
		return
	}
	if err := writeAudioEvent(r.Context(), tx, storyID, revisionID, "", actor,
		"audio_story.created", "", "draft", "", nil); err != nil {
		audioFail(w, http.StatusInternalServerError, "INTERNAL_ERROR", "failed to write audit event")
		return
	}
	if err := tx.Commit(r.Context()); err != nil {
		audioFail(w, http.StatusInternalServerError, "INTERNAL_ERROR", "failed to commit story")
		return
	}
	writeJSON(w, http.StatusCreated, map[string]string{
		"id": storyID, "revisionId": revisionID, "status": "draft",
	})
}

func (h *AudioStoryHandler) Get(w http.ResponseWriter, r *http.Request) {
	story, err := h.loadStory(r.Context(), chi.URLParam(r, "id"))
	if err != nil {
		audioFail(w, http.StatusNotFound, "NOT_FOUND", "audio story not found")
		return
	}
	var openRevision *audioRevisionDetail
	var liveRevision *audioRevisionDetail
	if story.OpenRevisionID != "" {
		if detail, err := h.loadRevision(r.Context(), story.ID, story.OpenRevisionID); err == nil {
			openRevision = detail
		}
	}
	if story.LiveRevisionID != "" {
		if detail, err := h.loadRevision(r.Context(), story.ID, story.LiveRevisionID); err == nil {
			liveRevision = detail
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"story": story, "openRevision": openRevision, "liveRevision": liveRevision,
	})
}

func (h *AudioStoryHandler) Update(w http.ResponseWriter, r *http.Request) {
	if !audioWriter(r) {
		audioFail(w, http.StatusForbidden, "FORBIDDEN", "audio writer access required")
		return
	}
	id := chi.URLParam(r, "id")
	var req audioStoryUpdateRequest
	if !decodeAudioBody(w, r, &req) {
		return
	}
	if req.Title != nil && strings.TrimSpace(*req.Title) == "" {
		audioFail(w, http.StatusBadRequest, "VALIDATION_FAILED", "title cannot be empty")
		return
	}
	if req.Theme != nil && !audioThemes[*req.Theme] {
		audioFail(w, http.StatusBadRequest, "VALIDATION_FAILED", "invalid theme")
		return
	}
	exists, err := h.storyExists(r.Context(), id)
	if err != nil {
		audioFail(w, http.StatusInternalServerError, "INTERNAL_ERROR", "failed to load audio story")
		return
	}
	if !exists {
		audioFail(w, http.StatusNotFound, "NOT_FOUND", "audio story not found")
		return
	}

	var updatedAt pgtype.Timestamp
	err = h.pool.QueryRow(r.Context(),
		`UPDATE audio_stories SET
		 title=CASE WHEN $2 THEN $3 ELSE title END,
		 subtitle=CASE WHEN $4 THEN NULLIF($5,'') ELSE subtitle END,
		 summary=CASE WHEN $6 THEN $7 ELSE summary END,
		 theme=CASE WHEN $8 THEN $9 ELSE theme END,
		 tags=CASE WHEN $10 THEN $11::text[] ELSE tags END,
		 cover_media_id=CASE WHEN $12 THEN NULLIF($13,'')::uuid ELSE cover_media_id END,
		 producer=CASE WHEN $14 THEN $15 ELSE producer END,
		 community_source=CASE WHEN $16 THEN $17 ELSE community_source END,
		 recorded_at=CASE WHEN $18 THEN NULLIF($19,'')::date ELSE recorded_at END,
		 collection_id=CASE WHEN $20 THEN NULLIF($21,'')::uuid ELSE collection_id END,
		 collection_order=CASE WHEN $22 THEN $23 ELSE collection_order END,
		 featured=CASE WHEN $24 THEN $25 ELSE featured END,
		 updated_by=$26, updated_at=now()
		 WHERE id=$1
		   AND (NULLIF($27,'')::timestamptz IS NULL OR updated_at=(NULLIF($27,'')::timestamptz AT TIME ZONE 'UTC'))
		 RETURNING updated_at`,
		id,
		req.Title != nil, valueOr(req.Title, ""),
		req.Subtitle != nil, valueOr(req.Subtitle, ""),
		req.Summary != nil, valueOr(req.Summary, ""),
		req.Theme != nil, valueOr(req.Theme, ""),
		req.Tags != nil, nonNilStrings(valueOr(req.Tags, nil)),
		req.CoverMediaID != nil, valueOr(req.CoverMediaID, ""),
		req.Producer != nil, valueOr(req.Producer, ""),
		req.CommunitySource != nil, valueOr(req.CommunitySource, ""),
		req.RecordedAt != nil, valueOr(req.RecordedAt, ""),
		req.CollectionID != nil, valueOr(req.CollectionID, ""),
		req.CollectionOrder != nil, valueOr(req.CollectionOrder, 0),
		req.Featured != nil, valueOr(req.Featured, false),
		middleware.UserID(r.Context()), req.ExpectedUpdated).Scan(&updatedAt)
	if err != nil == false {
		_ = writeAudioEvent(r.Context(), h.pool, id, "", "", middleware.UserID(r.Context()),
			"audio_story.metadata_updated", "", "", "", nil)
		writeJSON(w, http.StatusOK, map[string]any{"status": "saved", "updatedAt": tsPtr(updatedAt)})
		return
	}
	if err != nil && err != pgx.ErrNoRows {
		audioFail(w, http.StatusInternalServerError, "INTERNAL_ERROR", "failed to update audio story")
		return
	}
	audioFail(w, http.StatusConflict, "VERSION_CONFLICT", "the story changed since it was loaded")
}

func (h *AudioStoryHandler) Audit(w http.ResponseWriter, r *http.Request) {
	storyID := chi.URLParam(r, "id")
	rows, err := h.pool.Query(r.Context(),
		`SELECT e.id,COALESCE(e.revision_id::text,''),COALESCE(e.track_id::text,''),COALESCE(e.collection_id::text,''),
		 COALESCE(e.actor_id,''),COALESCE(u.name,''),e.action,COALESCE(e.from_status,''),COALESCE(e.to_status,''),
		 COALESCE(e.reason,''),e.metadata,e.created_at
		 FROM audio_story_events e LEFT JOIN users u ON u.id=e.actor_id
		 WHERE e.story_id=$1 ORDER BY e.created_at DESC LIMIT 200`, storyID)
	if err != nil {
		audioFail(w, http.StatusInternalServerError, "INTERNAL_ERROR", "failed to load audit events")
		return
	}
	defer rows.Close()
	items := make([]audioEventDetail, 0)
	for rows.Next() {
		var item audioEventDetail
		var metadata []byte
		var created pgtype.Timestamp
		if err := rows.Scan(&item.ID, &item.RevisionID, &item.TrackID, &item.CollectionID,
			&item.ActorID, &item.ActorName, &item.Action, &item.FromStatus, &item.ToStatus,
			&item.Reason, &metadata, &created); err != nil {
			audioFail(w, http.StatusInternalServerError, "INTERNAL_ERROR", "failed to read audit event")
			return
		}
		if len(metadata) > 0 {
			item.Metadata = json.RawMessage(metadata)
		}
		item.CreatedAt = tsPtr(created)
		items = append(items, item)
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items})
}

func (h *AudioStoryHandler) loadStory(ctx context.Context, id string) (*audioStoryDetail, error) {
	var s audioStoryDetail
	var subtitle, cover pgtype.Text
	var tags []string
	var recordedDate pgtype.Date
	var created, updated pgtype.Timestamp
	var liveStatus, openStatus pgtype.Text
	err := h.pool.QueryRow(ctx,
		`SELECT s.id,s.slug,s.title,COALESCE(s.subtitle,''),s.summary,s.theme,s.tags,
		 COALESCE(s.cover_media_id::text,''),COALESCE(m.object_key,''),s.producer,s.community_source,s.recorded_at,
		 s.status,COALESCE(s.collection_id::text,''),s.collection_order,s.featured,s.content_version,
		 COALESCE(s.live_revision_id::text,''),COALESCE(lr.status,''),
		 COALESCE(s.open_revision_id::text,''),COALESCE(orv.status,''),
		 s.created_at,s.updated_at
		 FROM audio_stories s
		 LEFT JOIN media_assets m ON m.id=s.cover_media_id
		 LEFT JOIN audio_story_revisions lr ON lr.id=s.live_revision_id
		 LEFT JOIN audio_story_revisions orv ON orv.id=s.open_revision_id
		 WHERE s.id=$1`, id).Scan(
		&s.ID, &s.Slug, &s.Title, &subtitle, &s.Summary, &s.Theme, &tags,
		&cover, &s.CoverObjectKey, &s.Producer, &s.CommunitySource, &recordedDate,
		&s.Status, &s.CollectionID, &s.CollectionOrder, &s.Featured, &s.ContentVersion,
		&s.LiveRevisionID, &liveStatus, &s.OpenRevisionID, &openStatus,
		&created, &updated)
	if err != nil {
		return nil, err
	}
	s.Subtitle = textPtr(subtitle)
	s.CoverMediaID = textPtr(cover)
	s.Tags = nonNilStrings(tags)
	s.RecordedAt = dateString(recordedDate)
	s.LiveStatus = textPtr(liveStatus)
	s.OpenRevisionStat = textPtr(openStatus)
	s.UpdatedAt = tsPtr(updated)
	if c := tsPtr(created); c != nil {
		s.CreatedAt = c.Format(time.RFC3339)
	}
	return &s, nil
}

func (h *AudioStoryHandler) storyExists(ctx context.Context, id string) (bool, error) {
	var exists bool
	err := h.pool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM audio_stories WHERE id=$1)`, id).Scan(&exists)
	return exists, err
}

// ---------------------------------------------------------------------------
// Revisions

func (h *AudioStoryHandler) ListRevisions(w http.ResponseWriter, r *http.Request) {
	storyID := chi.URLParam(r, "id")
	rows, err := h.pool.Query(r.Context(),
		`SELECT id,status,content_version,risk_tier,COALESCE(assigned_reviewer_id::text,''),
		 submitted_at,approved_at,published_at,created_at,updated_at
		 FROM audio_story_revisions WHERE story_id=$1 ORDER BY created_at DESC`, storyID)
	if err != nil {
		audioFail(w, http.StatusInternalServerError, "INTERNAL_ERROR", "failed to list revisions")
		return
	}
	defer rows.Close()
	items := make([]map[string]any, 0)
	for rows.Next() {
		var id, status, risk, reviewer string
		var version int
		var submitted, approved, published, created, updated pgtype.Timestamp
		if err := rows.Scan(&id, &status, &version, &risk, &reviewer,
			&submitted, &approved, &published, &created, &updated); err != nil {
			audioFail(w, http.StatusInternalServerError, "INTERNAL_ERROR", "failed to read revision")
			return
		}
		items = append(items, map[string]any{
			"id": id, "status": status, "contentVersion": version, "riskTier": risk,
			"assignedReviewerId": reviewer, "submittedAt": tsPtr(submitted),
			"approvedAt": tsPtr(approved), "publishedAt": tsPtr(published),
			"createdAt": tsPtr(created), "updatedAt": tsPtr(updated),
		})
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items})
}

func (h *AudioStoryHandler) GetRevision(w http.ResponseWriter, r *http.Request) {
	detail, err := h.loadRevision(r.Context(), chi.URLParam(r, "id"), chi.URLParam(r, "revisionId"))
	if err != nil {
		audioFail(w, http.StatusNotFound, "NOT_FOUND", "revision not found")
		return
	}
	writeJSON(w, http.StatusOK, detail)
}

func (h *AudioStoryHandler) CreateRevision(w http.ResponseWriter, r *http.Request) {
	if !audioWriter(r) {
		audioFail(w, http.StatusForbidden, "FORBIDDEN", "audio writer access required")
		return
	}
	storyID := chi.URLParam(r, "id")
	var req audioRevisionCreateRequest
	_ = json.NewDecoder(r.Body).Decode(&req) // body is optional

	var status, liveID, openID string
	var contentVersion int
	err := h.pool.QueryRow(r.Context(),
		`SELECT status,COALESCE(live_revision_id::text,''),COALESCE(open_revision_id::text,''),content_version
		 FROM audio_stories WHERE id=$1`, storyID).Scan(&status, &liveID, &openID, &contentVersion)
	if err != nil {
		audioFail(w, http.StatusNotFound, "NOT_FOUND", "audio story not found")
		return
	}
	if openID != "" {
		var openStatus string
		if err := h.pool.QueryRow(r.Context(),
			`SELECT status FROM audio_story_revisions WHERE id=$1`, openID).Scan(&openStatus); err == nil {
			switch openStatus {
			case "draft", "in_review", "approved", "scheduled":
				audioFail(w, http.StatusConflict, "OPEN_REVISION_EXISTS", "this story already has an open revision")
				return
			}
		}
	}
	sourceID := req.SourceRevisionID
	if sourceID == "" {
		sourceID = liveID
	}
	if sourceID == "" {
		_ = h.pool.QueryRow(r.Context(),
			`SELECT id FROM audio_story_revisions WHERE story_id=$1 ORDER BY created_at DESC LIMIT 1`,
			storyID).Scan(&sourceID)
	}
	if sourceID == "" {
		audioFail(w, http.StatusConflict, "NO_SOURCE_REVISION", "there is no revision to clone")
		return
	}
	var sourceBelongs bool
	if err := h.pool.QueryRow(r.Context(),
		`SELECT EXISTS(SELECT 1 FROM audio_story_revisions WHERE id=$1 AND story_id=$2)`,
		sourceID, storyID).Scan(&sourceBelongs); err != nil || !sourceBelongs {
		audioFail(w, http.StatusNotFound, "NOT_FOUND", "source revision not found")
		return
	}

	actor := middleware.UserID(r.Context())
	tx, err := h.pool.Begin(r.Context())
	if err != nil {
		audioFail(w, http.StatusInternalServerError, "INTERNAL_ERROR", "failed to start transaction")
		return
	}
	defer tx.Rollback(r.Context())

	var newID string
	if err := tx.QueryRow(r.Context(),
		`INSERT INTO audio_story_revisions (story_id,status,content_version,place_scope,counties,
		 destination_ids,content_note_categories,content_note_text,rights_basis,rights_holder,
		 licence_reference,rights_expires_at,risk_tier,created_by)
		 SELECT story_id,'draft',$3,place_scope,counties,destination_ids,content_note_categories,
		 content_note_text,rights_basis,rights_holder,licence_reference,rights_expires_at,risk_tier,$4
		 FROM audio_story_revisions WHERE id=$1 AND story_id=$2 RETURNING id`,
		sourceID, storyID, contentVersion, actor).Scan(&newID); err != nil {
		audioFail(w, http.StatusInternalServerError, "INTERNAL_ERROR", "failed to clone revision")
		return
	}
	if _, err := tx.Exec(r.Context(),
		`INSERT INTO audio_story_tracks (revision_id,language,media_asset_id,audio_object_key,
		 duration_seconds,narrator,narrator_consent_date,narrator_consent_reference,size_bytes,
		 format,bitrate_kbps,recorded_at)
		 SELECT $1,language,media_asset_id,audio_object_key,duration_seconds,narrator,
		 narrator_consent_date,narrator_consent_reference,size_bytes,format,bitrate_kbps,recorded_at
		 FROM audio_story_tracks WHERE revision_id=$2`, newID, sourceID); err != nil {
		audioFail(w, http.StatusInternalServerError, "INTERNAL_ERROR", "failed to clone tracks")
		return
	}
	if _, err := tx.Exec(r.Context(),
		`INSERT INTO audio_story_transcripts (revision_id,language,paragraphs,source)
		 SELECT $1,language,paragraphs,source FROM audio_story_transcripts WHERE revision_id=$2`,
		newID, sourceID); err != nil {
		audioFail(w, http.StatusInternalServerError, "INTERNAL_ERROR", "failed to clone transcripts")
		return
	}
	newStoryStatus := status
	if status == "unpublished" || status == "revoked" {
		newStoryStatus = "draft"
	}
	if _, err := tx.Exec(r.Context(),
		`UPDATE audio_stories SET open_revision_id=$1,status=$2,updated_by=$3,updated_at=now() WHERE id=$4`,
		newID, newStoryStatus, actor, storyID); err != nil {
		audioFail(w, http.StatusInternalServerError, "INTERNAL_ERROR", "failed to link revision")
		return
	}
	if err := writeAudioEvent(r.Context(), tx, storyID, newID, "", actor,
		"audio_story.revision_created", "", "draft", "", map[string]string{"sourceRevisionId": sourceID}); err != nil {
		audioFail(w, http.StatusInternalServerError, "INTERNAL_ERROR", "failed to write audit event")
		return
	}
	if err := tx.Commit(r.Context()); err != nil {
		audioFail(w, http.StatusInternalServerError, "INTERNAL_ERROR", "failed to commit revision")
		return
	}
	writeJSON(w, http.StatusCreated, map[string]string{
		"id": newID, "revisionId": newID, "status": "draft", "sourceRevisionId": sourceID,
	})
}

func (h *AudioStoryHandler) UpdateRevision(w http.ResponseWriter, r *http.Request) {
	if !audioWriter(r) {
		audioFail(w, http.StatusForbidden, "FORBIDDEN", "audio writer access required")
		return
	}
	storyID, revisionID := chi.URLParam(r, "id"), chi.URLParam(r, "revisionId")
	var req audioRevisionUpdateRequest
	if !decodeAudioBody(w, r, &req) {
		return
	}
	var status, placeScope string
	var counties []string
	var rightsBasis, rightsHolder, riskTier, licenceRef string
	err := h.pool.QueryRow(r.Context(),
		`SELECT status,place_scope,counties,rights_basis,rights_holder,risk_tier,COALESCE(licence_reference,'')
		 FROM audio_story_revisions WHERE id=$1 AND story_id=$2`, revisionID, storyID).Scan(
		&status, &placeScope, &counties, &rightsBasis, &rightsHolder, &riskTier, &licenceRef)
	if err != nil {
		audioFail(w, http.StatusNotFound, "NOT_FOUND", "revision not found")
		return
	}
	if status != "draft" {
		audioFail(w, http.StatusConflict, "REVISION_FROZEN", "only a draft revision can be edited")
		return
	}

	effectiveScope := valueOr(req.PlaceScope, placeScope)
	effectiveCounties := counties
	if req.Counties != nil {
		effectiveCounties = *req.Counties
	}
	if !validPlace(effectiveScope, effectiveCounties) {
		audioFail(w, http.StatusBadRequest, "VALIDATION_FAILED", "place scope and counties are inconsistent")
		return
	}
	effectiveRights := valueOr(req.RightsBasis, rightsBasis)
	if !audioRights[effectiveRights] {
		audioFail(w, http.StatusBadRequest, "VALIDATION_FAILED", "invalid rights basis")
		return
	}
	effectiveLicence := valueOr(req.LicenceReference, licenceRef)
	if !rightsEvidenceOK(effectiveRights, effectiveLicence) {
		audioFail(w, http.StatusBadRequest, "VALIDATION_FAILED", "a licence or permission reference is required for this rights basis")
		return
	}
	effectiveHolder := valueOr(req.RightsHolder, rightsHolder)
	if strings.TrimSpace(effectiveHolder) == "" {
		audioFail(w, http.StatusBadRequest, "VALIDATION_FAILED", "rights holder is required")
		return
	}
	effectiveRisk := valueOr(req.RiskTier, riskTier)
	if !validRisk(effectiveRisk) {
		audioFail(w, http.StatusBadRequest, "VALIDATION_FAILED", "invalid risk tier")
		return
	}

	var updatedAt pgtype.Timestamp
	err = h.pool.QueryRow(r.Context(),
		`UPDATE audio_story_revisions SET
		 place_scope=$3,
		 counties=$4::text[],
		 destination_ids=CASE WHEN $5 THEN $6::uuid[] ELSE destination_ids END,
		 content_note_categories=CASE WHEN $7 THEN $8::text[] ELSE content_note_categories END,
		 content_note_text=CASE WHEN $9 THEN NULLIF($10,'') ELSE content_note_text END,
		 rights_basis=$11,
		 rights_holder=$12,
		 licence_reference=CASE WHEN $13 THEN NULLIF($14,'') ELSE licence_reference END,
		 rights_expires_at=CASE WHEN $15 THEN NULLIF($16,'')::timestamp ELSE rights_expires_at END,
		 risk_tier=$17,
		 updated_at=now()
		 WHERE id=$1 AND story_id=$2 AND status='draft'
		   AND (NULLIF($18,'')::timestamptz IS NULL OR updated_at=(NULLIF($18,'')::timestamptz AT TIME ZONE 'UTC'))
		 RETURNING updated_at`,
		revisionID, storyID,
		effectiveScope, nonNilStrings(effectiveCounties),
		req.DestinationIDs != nil, nonNilStrings(valueOr(req.DestinationIDs, nil)),
		req.ContentNoteCategories != nil, nonNilStrings(valueOr(req.ContentNoteCategories, nil)),
		req.ContentNoteText != nil, valueOr(req.ContentNoteText, ""),
		effectiveRights, effectiveHolder,
		req.LicenceReference != nil, valueOr(req.LicenceReference, ""),
		req.RightsExpiresAt != nil, valueOr(req.RightsExpiresAt, ""),
		effectiveRisk, req.ExpectedUpdated).Scan(&updatedAt)
	if err != nil {
		if err == pgx.ErrNoRows {
			audioFail(w, http.StatusConflict, "VERSION_CONFLICT", "the revision changed since it was loaded")
			return
		}
		audioFail(w, http.StatusInternalServerError, "INTERNAL_ERROR", "failed to update revision")
		return
	}
	if err := writeAudioEvent(r.Context(), h.pool, storyID, revisionID, "", middleware.UserID(r.Context()),
		"audio_story.revision_updated", "draft", "draft", "", nil); err != nil {
		audioFail(w, http.StatusInternalServerError, "INTERNAL_ERROR", "failed to write audit event")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"status": "saved", "updatedAt": tsPtr(updatedAt)})
}

func (h *AudioStoryHandler) loadRevision(ctx context.Context, storyID, revisionID string) (*audioRevisionDetail, error) {
	var rev audioRevisionDetail
	var counties, destinations, noteCategories []string
	var note, licence, attestedBy, assigned, second, revocation string
	var expires, attested, submitted, approved, publishAt, publishedAt, revoked, created, updated pgtype.Timestamp
	err := h.pool.QueryRow(ctx,
		`SELECT r.id,r.story_id,r.status,r.content_version,r.place_scope,r.counties,r.destination_ids,
		 r.content_note_categories,COALESCE(r.content_note_text,''),r.rights_basis,r.rights_holder,
		 COALESCE(r.licence_reference,''),r.rights_expires_at,r.risk_tier,COALESCE(r.attested_by::text,''),
		 r.attested_at,COALESCE(r.assigned_reviewer_id::text,''),COALESCE(u.name,''),
		 COALESCE(r.second_reviewer_id::text,''),r.submitted_at,r.approved_at,r.publish_at,r.published_at,
		 r.revoked_at,COALESCE(r.revocation_reason,''),COALESCE(r.created_by::text,''),r.created_at,r.updated_at
		 FROM audio_story_revisions r LEFT JOIN users u ON u.id=r.assigned_reviewer_id
		 WHERE r.id=$1 AND r.story_id=$2`, revisionID, storyID).Scan(
		&rev.ID, &rev.StoryID, &rev.Status, &rev.ContentVersion, &rev.PlaceScope, &counties,
		&destinations, &noteCategories, &note, &rev.RightsBasis, &rev.RightsHolder,
		&licence, &expires, &rev.RiskTier, &attestedBy, &attested, &assigned, &rev.AssignedReviewerName,
		&second, &submitted, &approved, &publishAt, &publishedAt, &revoked, &revocation,
		&rev.CreatedBy, &created, &updated)
	if err != nil {
		return nil, err
	}
	rev.Counties = nonNilStrings(counties)
	rev.DestinationIDs = nonNilStrings(destinations)
	rev.ContentNoteCategories = nonNilStrings(noteCategories)
	rev.ContentNoteText = note
	rev.LicenceReference = licence
	rev.RightsExpiresAt = tsPtr(expires)
	rev.AttestedBy = attestedBy
	rev.AttestedAt = tsPtr(attested)
	rev.AssignedReviewerID = assigned
	rev.SecondReviewerID = second
	rev.SubmittedAt = tsPtr(submitted)
	rev.ApprovedAt = tsPtr(approved)
	rev.PublishAt = tsPtr(publishAt)
	rev.PublishedAt = tsPtr(publishedAt)
	rev.RevokedAt = tsPtr(revoked)
	rev.RevocationReason = revocation
	rev.CreatedAt = tsPtr(created)
	rev.UpdatedAt = tsPtr(updated)

	tracks, err := h.loadTracks(ctx, revisionID)
	if err != nil {
		return nil, err
	}
	transcripts, err := h.loadTranscripts(ctx, revisionID)
	if err != nil {
		return nil, err
	}
	rev.Tracks = tracks
	rev.Transcripts = transcripts
	return &rev, nil
}

func nonNilStrings(values []string) []string {
	if values == nil {
		return []string{}
	}
	return values
}

func (h *AudioStoryHandler) loadTracks(ctx context.Context, revisionID string) ([]audioTrackDetail, error) {
	rows, err := h.pool.Query(ctx,
		`SELECT id,language,COALESCE(media_asset_id::text,''),audio_object_key,duration_seconds,
		 narrator,COALESCE(narrator_consent_date::text,''),COALESCE(narrator_consent_reference,''),
		 COALESCE(size_bytes,0),COALESCE(format,''),COALESCE(bitrate_kbps,0),
		 COALESCE(recorded_at::text,''),created_at,updated_at
		 FROM audio_story_tracks WHERE revision_id=$1 ORDER BY language`, revisionID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := make([]audioTrackDetail, 0)
	for rows.Next() {
		var t audioTrackDetail
		var created, updated pgtype.Timestamp
		if err := rows.Scan(&t.ID, &t.Language, &t.MediaAssetID, &t.AudioObjectKey, &t.DurationSeconds,
			&t.Narrator, &t.NarratorConsentDate, &t.NarratorConsentReference, &t.SizeBytes,
			&t.Format, &t.BitrateKbps, &t.RecordedAt, &created, &updated); err != nil {
			return nil, err
		}
		if c := tsPtr(created); c != nil {
			t.CreatedAt = c.Format(time.RFC3339)
		}
		if u := tsPtr(updated); u != nil {
			s := u.Format(time.RFC3339)
			t.UpdatedAt = &s
		}
		items = append(items, t)
	}
	return items, rows.Err()
}

func (h *AudioStoryHandler) loadTranscripts(ctx context.Context, revisionID string) ([]audioTranscriptDetail, error) {
	rows, err := h.pool.Query(ctx,
		`SELECT language,paragraphs,COALESCE(source,''),COALESCE(reviewed_at::text,''),COALESCE(reviewed_by::text,'')
		 FROM audio_story_transcripts WHERE revision_id=$1 ORDER BY language`, revisionID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := make([]audioTranscriptDetail, 0)
	for rows.Next() {
		var t audioTranscriptDetail
		var raw []byte
		if err := rows.Scan(&t.Language, &raw, &t.Source, &t.ReviewedAt, &t.ReviewedBy); err != nil {
			return nil, err
		}
		_ = json.Unmarshal(raw, &t.Paragraphs)
		if t.Paragraphs == nil {
			t.Paragraphs = []transcriptParagraph{}
		}
		items = append(items, t)
	}
	return items, rows.Err()
}

// ---------------------------------------------------------------------------
// Tracks and transcripts

func (h *AudioStoryHandler) UpsertTrack(w http.ResponseWriter, r *http.Request) {
	if !audioWriter(r) {
		audioFail(w, http.StatusForbidden, "FORBIDDEN", "audio writer access required")
		return
	}
	storyID, revisionID := chi.URLParam(r, "id"), chi.URLParam(r, "revisionId")
	var req audioTrackRequest
	if !decodeAudioBody(w, r, &req) {
		return
	}
	if err := validateTrack(req.Language, req.AudioObjectKey, req.Narrator, req.DurationSeconds); err != nil {
		audioFail(w, http.StatusBadRequest, "VALIDATION_FAILED", err.Error())
		return
	}
	status, err := h.revisionStatus(r.Context(), storyID, revisionID)
	if err != nil {
		audioFail(w, http.StatusNotFound, "NOT_FOUND", "revision not found")
		return
	}
	if status != "draft" {
		audioFail(w, http.StatusConflict, "REVISION_FROZEN", "only a draft revision can be edited")
		return
	}
	var trackID string
	err = h.pool.QueryRow(r.Context(),
		`INSERT INTO audio_story_tracks (revision_id,language,media_asset_id,audio_object_key,
		 duration_seconds,narrator,narrator_consent_date,narrator_consent_reference,size_bytes,
		 format,bitrate_kbps,recorded_at)
		 VALUES ($1,$2,NULLIF($3,'')::uuid,$4,$5,$6,NULLIF($7,'')::date,NULLIF($8,''),$9,
		 NULLIF($10,''),NULLIF($11,0),NULLIF($12,'')::date)
		 ON CONFLICT (revision_id,language) DO UPDATE SET
		 media_asset_id=EXCLUDED.media_asset_id,audio_object_key=EXCLUDED.audio_object_key,
		 duration_seconds=EXCLUDED.duration_seconds,narrator=EXCLUDED.narrator,
		 narrator_consent_date=EXCLUDED.narrator_consent_date,
		 narrator_consent_reference=EXCLUDED.narrator_consent_reference,size_bytes=EXCLUDED.size_bytes,
		 format=EXCLUDED.format,bitrate_kbps=EXCLUDED.bitrate_kbps,recorded_at=EXCLUDED.recorded_at,
		 updated_at=now()
		 RETURNING id`,
		revisionID, req.Language, req.MediaAssetID, req.AudioObjectKey, req.DurationSeconds,
		req.Narrator, req.NarratorConsentDate, req.NarratorConsentReference, req.SizeBytes,
		req.Format, req.BitrateKbps, req.RecordedAt).Scan(&trackID)
	if err != nil {
		audioFail(w, http.StatusInternalServerError, "INTERNAL_ERROR", "failed to save track")
		return
	}
	if err := writeAudioEvent(r.Context(), h.pool, storyID, revisionID, trackID, middleware.UserID(r.Context()),
		"audio_story.track_saved", "", "", "", map[string]string{"language": req.Language}); err != nil {
		audioFail(w, http.StatusInternalServerError, "INTERNAL_ERROR", "failed to write audit event")
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "saved", "trackId": trackID})
}

func (h *AudioStoryHandler) UpdateTrack(w http.ResponseWriter, r *http.Request) {
	if !audioWriter(r) {
		audioFail(w, http.StatusForbidden, "FORBIDDEN", "audio writer access required")
		return
	}
	storyID, revisionID, trackID := chi.URLParam(r, "id"), chi.URLParam(r, "revisionId"), chi.URLParam(r, "trackId")
	var req audioTrackUpdateRequest
	if !decodeAudioBody(w, r, &req) {
		return
	}
	status, err := h.revisionStatus(r.Context(), storyID, revisionID)
	if err != nil {
		audioFail(w, http.StatusNotFound, "NOT_FOUND", "revision not found")
		return
	}
	if status != "draft" {
		audioFail(w, http.StatusConflict, "REVISION_FROZEN", "only a draft revision can be edited")
		return
	}
	if req.DurationSeconds != nil && (*req.DurationSeconds < 30 || *req.DurationSeconds > 3600) {
		audioFail(w, http.StatusBadRequest, "VALIDATION_FAILED", "track duration must be 30s–60min")
		return
	}
	if req.Narrator != nil && strings.TrimSpace(*req.Narrator) == "" {
		audioFail(w, http.StatusBadRequest, "VALIDATION_FAILED", "narrator cannot be empty")
		return
	}
	tag, err := h.pool.Exec(r.Context(),
		`UPDATE audio_story_tracks SET
		 duration_seconds=CASE WHEN $3 THEN $4 ELSE duration_seconds END,
		 narrator=CASE WHEN $5 THEN $6 ELSE narrator END,
		 narrator_consent_date=CASE WHEN $7 THEN NULLIF($8,'')::date ELSE narrator_consent_date END,
		 narrator_consent_reference=CASE WHEN $9 THEN NULLIF($10,'') ELSE narrator_consent_reference END,
		 format=CASE WHEN $11 THEN NULLIF($12,'') ELSE format END,
		 recorded_at=CASE WHEN $13 THEN NULLIF($14,'')::date ELSE recorded_at END,
		 size_bytes=CASE WHEN $15 THEN $16 ELSE size_bytes END,
		 bitrate_kbps=CASE WHEN $17 THEN $18 ELSE bitrate_kbps END,
		 updated_at=now()
		 WHERE id=$1 AND revision_id=$2`,
		trackID, revisionID,
		req.DurationSeconds != nil, valueOr(req.DurationSeconds, 0),
		req.Narrator != nil, valueOr(req.Narrator, ""),
		req.NarratorConsentDate != nil, valueOr(req.NarratorConsentDate, ""),
		req.NarratorConsentReference != nil, valueOr(req.NarratorConsentReference, ""),
		req.Format != nil, valueOr(req.Format, ""),
		req.RecordedAt != nil, valueOr(req.RecordedAt, ""),
		req.SizeBytes != nil, valueOr(req.SizeBytes, int64(0)),
		req.BitrateKbps != nil, valueOr(req.BitrateKbps, 0))
	if err != nil {
		audioFail(w, http.StatusInternalServerError, "INTERNAL_ERROR", "failed to update track")
		return
	}
	if tag.RowsAffected() == 0 {
		audioFail(w, http.StatusNotFound, "NOT_FOUND", "track not found")
		return
	}
	if err := writeAudioEvent(r.Context(), h.pool, storyID, revisionID, trackID, middleware.UserID(r.Context()),
		"audio_story.track_updated", "", "", "", nil); err != nil {
		audioFail(w, http.StatusInternalServerError, "INTERNAL_ERROR", "failed to write audit event")
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "saved"})
}

func (h *AudioStoryHandler) DeleteTrack(w http.ResponseWriter, r *http.Request) {
	if !audioWriter(r) {
		audioFail(w, http.StatusForbidden, "FORBIDDEN", "audio writer access required")
		return
	}
	storyID, revisionID, trackID := chi.URLParam(r, "id"), chi.URLParam(r, "revisionId"), chi.URLParam(r, "trackId")
	reason := strings.TrimSpace(r.URL.Query().Get("reason"))
	if reason == "" {
		audioFail(w, http.StatusBadRequest, "REASON_REQUIRED", "a reason is required to remove a track")
		return
	}
	status, err := h.revisionStatus(r.Context(), storyID, revisionID)
	if err != nil {
		audioFail(w, http.StatusNotFound, "NOT_FOUND", "revision not found")
		return
	}
	if status != "draft" {
		audioFail(w, http.StatusConflict, "REVISION_FROZEN", "only a draft revision can be edited")
		return
	}
	var language string
	err = h.pool.QueryRow(r.Context(),
		`DELETE FROM audio_story_tracks WHERE id=$1 AND revision_id=$2 RETURNING language`,
		trackID, revisionID).Scan(&language)
	if err != nil {
		audioFail(w, http.StatusNotFound, "NOT_FOUND", "track not found")
		return
	}
	if err := writeAudioEvent(r.Context(), h.pool, storyID, revisionID, trackID, middleware.UserID(r.Context()),
		"audio_story.track_removed", "", "", reason, map[string]string{"language": language}); err != nil {
		audioFail(w, http.StatusInternalServerError, "INTERNAL_ERROR", "failed to write audit event")
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "removed"})
}

func (h *AudioStoryHandler) PutTranscript(w http.ResponseWriter, r *http.Request) {
	if !audioWriter(r) {
		audioFail(w, http.StatusForbidden, "FORBIDDEN", "audio writer access required")
		return
	}
	storyID, revisionID := chi.URLParam(r, "id"), chi.URLParam(r, "revisionId")
	language := chi.URLParam(r, "language")
	if language != "en" && language != "sw" {
		audioFail(w, http.StatusBadRequest, "VALIDATION_FAILED", "language must be en or sw")
		return
	}
	var req transcriptRequest
	if !decodeAudioBody(w, r, &req) {
		return
	}
	if err := validateParagraphs(req.Paragraphs, nil); err != nil {
		audioFail(w, http.StatusBadRequest, "VALIDATION_FAILED", err.Error())
		return
	}
	status, err := h.revisionStatus(r.Context(), storyID, revisionID)
	if err != nil {
		audioFail(w, http.StatusNotFound, "NOT_FOUND", "revision not found")
		return
	}
	if status != "draft" {
		audioFail(w, http.StatusConflict, "REVISION_FROZEN", "only a draft revision can be edited")
		return
	}
	var duration *int
	_ = h.pool.QueryRow(r.Context(),
		`SELECT duration_seconds FROM audio_story_tracks WHERE revision_id=$1 AND language=$2`,
		revisionID, language).Scan(&duration)
	if duration != nil {
		if err := validateParagraphs(req.Paragraphs, duration); err != nil {
			audioFail(w, http.StatusBadRequest, "VALIDATION_FAILED", err.Error())
			return
		}
	}
	raw, _ := json.Marshal(req.Paragraphs)
	if _, err := h.pool.Exec(r.Context(),
		`INSERT INTO audio_story_transcripts (revision_id,language,paragraphs,source)
		 VALUES ($1,$2,$3::jsonb,'human') ON CONFLICT (revision_id,language) DO UPDATE
		 SET paragraphs=EXCLUDED.paragraphs,updated_at=now()`, revisionID, language, raw); err != nil {
		audioFail(w, http.StatusInternalServerError, "INTERNAL_ERROR", "failed to save transcript")
		return
	}
	if err := writeAudioEvent(r.Context(), h.pool, storyID, revisionID, "", middleware.UserID(r.Context()),
		"audio_story.transcript_saved", "", "", "", map[string]string{"language": language}); err != nil {
		audioFail(w, http.StatusInternalServerError, "INTERNAL_ERROR", "failed to write audit event")
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "saved"})
}

func (h *AudioStoryHandler) revisionStatus(ctx context.Context, storyID, revisionID string) (string, error) {
	var status string
	err := h.pool.QueryRow(ctx,
		`SELECT status FROM audio_story_revisions WHERE id=$1 AND story_id=$2`,
		revisionID, storyID).Scan(&status)
	return status, err
}

func validateTrack(language, objectKey, narrator string, duration int) error {
	if language != "en" && language != "sw" {
		return fmt.Errorf("language must be en or sw")
	}
	if strings.TrimSpace(objectKey) == "" {
		return fmt.Errorf("audio is required")
	}
	if strings.TrimSpace(narrator) == "" {
		return fmt.Errorf("narrator is required")
	}
	if duration < 30 || duration > 3600 {
		return fmt.Errorf("track duration must be 30s–60min")
	}
	return nil
}

// ---------------------------------------------------------------------------
// Readiness and submit

func (h *AudioStoryHandler) Readiness(w http.ResponseWriter, r *http.Request) {
	result, err := h.computeReadiness(r.Context(), chi.URLParam(r, "id"), chi.URLParam(r, "revisionId"))
	if err != nil {
		audioFail(w, http.StatusNotFound, "NOT_FOUND", "revision not found")
		return
	}
	writeJSON(w, http.StatusOK, result)
}

func (h *AudioStoryHandler) Submit(w http.ResponseWriter, r *http.Request) {
	if !audioWriter(r) {
		audioFail(w, http.StatusForbidden, "FORBIDDEN", "audio writer access required")
		return
	}
	storyID, revisionID := chi.URLParam(r, "id"), chi.URLParam(r, "revisionId")
	result, err := h.computeReadiness(r.Context(), storyID, revisionID)
	if err != nil {
		audioFail(w, http.StatusNotFound, "NOT_FOUND", "revision not found")
		return
	}
	if !result.Ready {
		writeJSON(w, http.StatusUnprocessableEntity, map[string]any{
			"error":     map[string]string{"code": "READINESS_BLOCKED", "message": "revision is not ready for review"},
			"readiness": result,
		})
		return
	}
	actor := middleware.UserID(r.Context())
	tx, err := h.pool.Begin(r.Context())
	if err != nil {
		audioFail(w, http.StatusInternalServerError, "INTERNAL_ERROR", "failed to start transaction")
		return
	}
	defer tx.Rollback(r.Context())

	tag, err := tx.Exec(r.Context(),
		`UPDATE audio_story_revisions SET status='in_review',submitted_at=now(),
		 assigned_reviewer_id=NULL,second_reviewer_id=NULL,updated_at=now()
		 WHERE id=$1 AND story_id=$2 AND status='draft'`, revisionID, storyID)
	if err != nil || tag.RowsAffected() == 0 {
		audioFail(w, http.StatusConflict, "INVALID_TRANSITION", "revision is not a draft")
		return
	}
	if _, err := tx.Exec(r.Context(),
		`UPDATE audio_stories SET
		 status=CASE WHEN status='published' THEN status ELSE 'in_review' END,
		 open_revision_id=$1,updated_by=$2,updated_at=now() WHERE id=$3`,
		revisionID, actor, storyID); err != nil {
		audioFail(w, http.StatusInternalServerError, "INTERNAL_ERROR", "failed to submit story")
		return
	}
	if err := writeAudioEvent(r.Context(), tx, storyID, revisionID, "", actor,
		"audio_story.submitted_for_review", "draft", "in_review", "", nil); err != nil {
		audioFail(w, http.StatusInternalServerError, "INTERNAL_ERROR", "failed to write audit event")
		return
	}
	if err := tx.Commit(r.Context()); err != nil {
		audioFail(w, http.StatusInternalServerError, "INTERNAL_ERROR", "failed to commit submit")
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "in_review"})
}

func (h *AudioStoryHandler) computeReadiness(ctx context.Context, storyID, revisionID string) (*audioReadiness, error) {
	var story storySnapshot
	var collectionState pgtype.Text
	if err := h.pool.QueryRow(ctx,
		`SELECT s.title,s.summary,s.producer,s.community_source,COALESCE(s.cover_media_id::text,''),
		 COALESCE(s.collection_id::text,''),c.status
		 FROM audio_stories s LEFT JOIN audio_story_collections c ON c.id=s.collection_id
		 WHERE s.id=$1`, storyID).Scan(&story.Title, &story.Summary, &story.Producer,
		&story.Community, &story.CoverID, &story.CollectionID, &collectionState); err != nil {
		return nil, err
	}
	story.CollectionState = collectionState.String

	var rev revisionSnapshot
	var expires pgtype.Timestamp
	if err := h.pool.QueryRow(ctx,
		`SELECT place_scope,counties,rights_basis,rights_holder,risk_tier,
		 COALESCE(content_note_text,''),COALESCE(licence_reference,''),rights_expires_at
		 FROM audio_story_revisions WHERE id=$1 AND story_id=$2`, revisionID, storyID).Scan(
		&rev.PlaceScope, &rev.Counties, &rev.RightsBasis, &rev.RightsHolder, &rev.RiskTier,
		&rev.Note, &rev.LicenceReference, &expires); err != nil {
		return nil, err
	}
	rev.RightsExpiresAt = tsPtr(expires)

	tracks, transcripts, err := h.loadSnapshots(ctx, revisionID)
	if err != nil {
		return nil, err
	}
	result := evaluateReadiness(story, rev, tracks, transcripts)
	return &result, nil
}

func (h *AudioStoryHandler) loadSnapshots(ctx context.Context, revisionID string) ([]trackSnapshot, []transcriptSnapshot, error) {
	rows, err := h.pool.Query(ctx,
		`SELECT language,duration_seconds,narrator,audio_object_key,
		 COALESCE(narrator_consent_date::text,''),COALESCE(narrator_consent_reference,'')
		 FROM audio_story_tracks WHERE revision_id=$1`, revisionID)
	if err != nil {
		return nil, nil, err
	}
	var tracks []trackSnapshot
	for rows.Next() {
		var t trackSnapshot
		if err := rows.Scan(&t.Language, &t.Duration, &t.Narrator, &t.ObjectKey,
			&t.ConsentDate, &t.ConsentRef); err != nil {
			rows.Close()
			return nil, nil, err
		}
		tracks = append(tracks, t)
	}
	rows.Close()

	trs, err := h.pool.Query(ctx,
		`SELECT language,paragraphs FROM audio_story_transcripts WHERE revision_id=$1`, revisionID)
	if err != nil {
		return nil, nil, err
	}
	var transcripts []transcriptSnapshot
	for trs.Next() {
		var t transcriptSnapshot
		var raw []byte
		if err := trs.Scan(&t.Language, &raw); err != nil {
			trs.Close()
			return nil, nil, err
		}
		_ = json.Unmarshal(raw, &t.Paragraphs)
		transcripts = append(transcripts, t)
	}
	trs.Close()
	return tracks, transcripts, nil
}

func evaluateReadiness(story storySnapshot, rev revisionSnapshot, tracks []trackSnapshot, transcripts []transcriptSnapshot) audioReadiness {
	var blocking, warnings []readinessItem
	add := func(code, label, section string) {
		blocking = append(blocking, readinessItem{Code: code, Label: label, Section: section})
	}
	warn := func(code, label, section string) {
		warnings = append(warnings, readinessItem{Code: code, Label: label, Section: section})
	}

	if strings.TrimSpace(story.Title) == "" {
		add("missing_title", "Title is required", "story")
	}
	if strings.TrimSpace(story.Summary) == "" {
		add("missing_summary", "Summary is required", "story")
	}
	if strings.TrimSpace(story.Producer) == "" {
		add("missing_producer", "Producer is required", "credits")
	}
	if strings.TrimSpace(story.Community) == "" {
		add("missing_community", "Community/source is required", "credits")
	}
	if story.CoverID == "" {
		add("missing_cover", "Cover image is required", "story")
	}
	if story.CollectionID == "" {
		add("missing_collection", "A primary collection is required", "story")
	} else if story.CollectionState != "published" {
		warn("collection_unpublished", "Collection is not published yet; it must be published before this story can go live", "collection")
	}
	if !validPlace(rev.PlaceScope, rev.Counties) {
		add("invalid_place", "Place scope and counties are inconsistent", "placement")
	}
	if rev.RightsBasis == "" || rev.RightsBasis == "unverified" || rev.RightsHolder == "" {
		add("missing_rights", "Rights basis and holder are required", "rights")
	} else if !rightsEvidenceOK(rev.RightsBasis, rev.LicenceReference) {
		add("missing_rights_evidence", "A licence or permission reference is required for this rights basis", "rights")
	}
	if !validRisk(rev.RiskTier) {
		add("invalid_risk", "Risk tier is required", "rights")
	}
	if rev.RightsExpiresAt != nil && rev.RightsExpiresAt.Before(time.Now()) {
		add("rights_expired", "Rights have expired", "rights")
	}

	if len(tracks) == 0 {
		add("missing_track", "At least one language track is required", "tracks")
	}
	for _, track := range tracks {
		if strings.TrimSpace(track.Narrator) == "" {
			add("missing_narrator_"+track.Language, "Narrator is required for "+languageLabel(track.Language), "tracks")
		}
		if track.Duration < 30 || track.Duration > 3600 {
			add("invalid_duration_"+track.Language, "Track duration must be 30s–60min", "tracks")
		}
		if strings.TrimSpace(track.ObjectKey) == "" {
			add("missing_audio_"+track.Language, "Audio is required for "+languageLabel(track.Language), "tracks")
		}
		if track.ConsentDate == "" || strings.TrimSpace(track.ConsentRef) == "" {
			add("missing_narrator_consent_"+track.Language, "Narrator consent date and release reference are required for "+languageLabel(track.Language), "rights")
		}
		found := false
		for _, transcript := range transcripts {
			if transcript.Language != track.Language {
				continue
			}
			found = true
			if err := validateParagraphs(transcript.Paragraphs, &track.Duration); err != nil {
				add("invalid_transcript_"+track.Language, "Transcript: "+err.Error(), "tracks")
			}
			lastCue := -1.0
			for _, paragraph := range transcript.Paragraphs {
				if paragraph.StartSeconds != nil && *paragraph.StartSeconds > lastCue {
					lastCue = *paragraph.StartSeconds
				}
			}
			if lastCue < 0 {
				warn("transcript_uncued_"+track.Language, "Transcript has no cue points for "+languageLabel(track.Language), "tracks")
			} else if lastCue < float64(track.Duration)*0.8 {
				warn("transcript_coverage_"+track.Language, "Transcript cues end before 80% of the "+languageLabel(track.Language)+" track; confirm it is complete", "tracks")
			}
		}
		if !found {
			add("missing_transcript_"+track.Language, "Transcript is required for "+languageLabel(track.Language), "tracks")
		}
	}
	for _, transcript := range transcripts {
		if err := validateParagraphs(transcript.Paragraphs, nil); err != nil {
			add("invalid_transcript_"+transcript.Language, "Transcript: "+err.Error(), "tracks")
		}
	}

	if len(tracks) == 1 {
		warn("one_language", "Only one language track; the story will show one audio language", "tracks")
	}
	if rev.Note == "" && rev.RiskTier == "sensitive" {
		warn("no_content_note", "No content note on a sensitive story; the reviewer decides whether one is needed", "rights")
	}
	if rev.RightsExpiresAt != nil && rev.RightsExpiresAt.Before(time.Now().AddDate(0, 0, 30)) {
		warn("rights_expiring", "Rights expire within 30 days", "rights")
	}

	if blocking == nil {
		blocking = []readinessItem{}
	}
	if warnings == nil {
		warnings = []readinessItem{}
	}
	return audioReadiness{Ready: len(blocking) == 0, Blocking: blocking, Warnings: warnings}
}

func validateParagraphs(paragraphs []transcriptParagraph, duration *int) error {
	if len(paragraphs) == 0 {
		return fmt.Errorf("at least one paragraph is required")
	}
	last := -1.0
	for i, p := range paragraphs {
		if strings.TrimSpace(p.Text) == "" {
			return fmt.Errorf("paragraph %d is empty", i+1)
		}
		if p.StartSeconds != nil {
			if *p.StartSeconds < 0 {
				return fmt.Errorf("cue points cannot be negative")
			}
			if *p.StartSeconds < last {
				return fmt.Errorf("cue points must increase")
			}
			if duration != nil && *p.StartSeconds >= float64(*duration) {
				return fmt.Errorf("cue point is beyond the track duration")
			}
			last = *p.StartSeconds
		}
	}
	return nil
}

func validPlace(scope string, counties []string) bool {
	switch scope {
	case "county":
		return len(counties) == 1
	case "multi_county":
		return len(counties) >= 2
	case "national":
		return len(counties) == 0
	}
	return false
}

func validRisk(risk string) bool {
	return risk == "standard" || risk == "sensitive" || risk == "restricted"
}

func languageLabel(code string) string {
	if code == "sw" {
		return "Kiswahili"
	}
	return "English"
}

func rightsEvidenceOK(basis, licenceRef string) bool {
	switch basis {
	case "partner_permission", "licensed":
		return strings.TrimSpace(licenceRef) != ""
	}
	return true
}

func writeJSON(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(body)
}
