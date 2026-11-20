package admin

import (
	"context"
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/bradleygichuru/ytci-go/internal/middleware"
)

type audioCollectionCreateRequest struct {
	Slug         string `json:"slug"`
	Title        string `json:"title"`
	Description  string `json:"description"`
	CoverMediaID string `json:"coverMediaId"`
}

type audioCollectionUpdateRequest struct {
	Title        *string `json:"title"`
	Description  *string `json:"description"`
	CoverMediaID *string `json:"coverMediaId"`
	DisplayOrder *int    `json:"displayOrder"`
	Featured     *bool   `json:"featured"`
}

type audioCollectionStoriesRequest struct {
	StoryIDs []string `json:"storyIds"`
}

type audioCollectionDetail struct {
	ID                  string     `json:"id"`
	Slug                string     `json:"slug"`
	Title               string     `json:"title"`
	Description         string     `json:"description"`
	CoverMediaID        string     `json:"coverMediaId,omitempty"`
	CoverObjectKey      string     `json:"coverObjectKey,omitempty"`
	Status              string     `json:"status"`
	DisplayOrder        int        `json:"displayOrder"`
	Featured            bool       `json:"featured"`
	PublishAt           *time.Time `json:"publishAt,omitempty"`
	FirstPublishedAt    *time.Time `json:"firstPublishedAt,omitempty"`
	StoryCount          int        `json:"storyCount"`
	PublishedStoryCount int        `json:"publishedStoryCount"`
	CreatedAt           *time.Time `json:"createdAt"`
	UpdatedAt           *time.Time `json:"updatedAt"`
}

func (h *AudioStoryHandler) loadCollection(ctx context.Context, id string) (*audioCollectionDetail, error) {
	return loadAudioCollection(ctx, h.pool, id)
}

func loadAudioCollection(ctx context.Context, pool *pgxpool.Pool, id string) (*audioCollectionDetail, error) {
	var c audioCollectionDetail
	var cover, coverKey pgtype.Text
	var publishAt, firstPublished, created, updated pgtype.Timestamp
	err := pool.QueryRow(ctx,
		`SELECT c.id,c.slug,c.title,c.description,COALESCE(c.cover_media_id::text,''),
		 COALESCE(m.object_key,''),c.status,c.display_order,c.featured,c.publish_at,c.first_published_at,
		 c.created_at,c.updated_at,
		 (SELECT count(*) FROM audio_stories s WHERE s.collection_id=c.id),
		 (SELECT count(*) FROM audio_stories s
		  JOIN audio_story_revisions lr ON lr.id=s.live_revision_id
		  WHERE s.collection_id=c.id AND s.status='published' AND lr.status='published'
		    AND lr.revoked_at IS NULL
		    AND (lr.rights_expires_at IS NULL OR lr.rights_expires_at > now()))
		 FROM audio_story_collections c
		 LEFT JOIN media_assets m ON m.id=c.cover_media_id
		 WHERE c.id=$1`, id).Scan(
		&c.ID, &c.Slug, &c.Title, &c.Description, &cover, &coverKey, &c.Status, &c.DisplayOrder,
		&c.Featured, &publishAt, &firstPublished, &created, &updated, &c.StoryCount, &c.PublishedStoryCount)
	if err != nil {
		return nil, err
	}
	c.CoverMediaID = textPtr(cover)
	c.CoverObjectKey = textPtr(coverKey)
	c.PublishAt = tsPtr(publishAt)
	c.FirstPublishedAt = tsPtr(firstPublished)
	c.CreatedAt = tsPtr(created)
	c.UpdatedAt = tsPtr(updated)
	return &c, nil
}

func (h *AudioStoryHandler) CreateCollection(w http.ResponseWriter, r *http.Request) {
	if !audioWriter(r) {
		audioFail(w, http.StatusForbidden, "FORBIDDEN", "audio writer access required")
		return
	}
	var req audioCollectionCreateRequest
	if !decodeAudioBody(w, r, &req) {
		return
	}
	req.Slug = strings.TrimSpace(req.Slug)
	req.Title = strings.TrimSpace(req.Title)
	if req.Slug == "" || req.Title == "" {
		audioFail(w, http.StatusBadRequest, "VALIDATION_FAILED", "slug and title are required")
		return
	}
	ctx := r.Context()
	actor := middleware.UserID(ctx)
	tx, err := h.pool.Begin(ctx)
	if err != nil {
		audioFail(w, http.StatusInternalServerError, "INTERNAL_ERROR", "failed to start transaction")
		return
	}
	defer tx.Rollback(ctx)

	var id string
	err = tx.QueryRow(ctx,
		`INSERT INTO audio_story_collections (slug,title,description,cover_media_id,status,created_by,updated_by)
		 VALUES ($1,$2,$3,NULLIF($4,'')::uuid,'draft',$5,$5) RETURNING id`,
		req.Slug, req.Title, req.Description, req.CoverMediaID, actor).Scan(&id)
	if err != nil {
		audioFail(w, http.StatusConflict, "SLUG_TAKEN", "the collection slug is already in use or the payload is invalid")
		return
	}
	if err := writeAudioCollectionEvent(ctx, tx, id, actor,
		"audio_story_collection.created", "", "draft", "", nil); err != nil {
		audioFail(w, http.StatusInternalServerError, "INTERNAL_ERROR", "failed to write audit event")
		return
	}
	if err := tx.Commit(ctx); err != nil {
		audioFail(w, http.StatusInternalServerError, "INTERNAL_ERROR", "failed to commit collection")
		return
	}
	writeJSON(w, http.StatusCreated, map[string]string{"id": id, "slug": req.Slug, "status": "draft"})
}

func (h *AudioStoryHandler) GetCollection(w http.ResponseWriter, r *http.Request) {
	collection, err := h.loadCollection(r.Context(), chi.URLParam(r, "id"))
	if err != nil {
		audioFail(w, http.StatusNotFound, "NOT_FOUND", "audio story collection not found")
		return
	}
	stories, err := h.collectionStories(r.Context(), collection.ID)
	if err != nil {
		audioFail(w, http.StatusInternalServerError, "INTERNAL_ERROR", "failed to load collection stories")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"collection": collection, "stories": stories})
}

func (h *AudioStoryHandler) collectionStories(ctx context.Context, collectionID string) ([]map[string]any, error) {
	rows, err := h.pool.Query(ctx,
		`SELECT s.id,s.slug,s.title,s.status,s.collection_order,s.featured,s.content_version,
		 COALESCE(s.live_revision_id::text,''),COALESCE(lr.status,''),
		 COALESCE(s.open_revision_id::text,''),COALESCE(orv.status,''),
		 COALESCE(orv.risk_tier,lr.risk_tier,''),s.updated_at
		 FROM audio_stories s
		 LEFT JOIN audio_story_revisions lr ON lr.id=s.live_revision_id
		 LEFT JOIN audio_story_revisions orv ON orv.id=s.open_revision_id
		 WHERE s.collection_id=$1
		 ORDER BY s.collection_order,s.created_at`, collectionID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := make([]map[string]any, 0)
	for rows.Next() {
		var id, slug, title, status, liveID, liveStatus, openID, openStatus, risk string
		var order, version int
		var featured bool
		var updated pgtype.Timestamp
		if err := rows.Scan(&id, &slug, &title, &status, &order, &featured, &version,
			&liveID, &liveStatus, &openID, &openStatus, &risk, &updated); err != nil {
			return nil, err
		}
		items = append(items, map[string]any{
			"id": id, "slug": slug, "title": title, "status": status,
			"collectionOrder": order, "featured": featured, "contentVersion": version,
			"liveRevisionId": liveID, "liveRevisionStatus": liveStatus,
			"openRevisionId": openID, "openRevisionStatus": openStatus,
			"riskTier": risk, "updatedAt": tsPtr(updated),
		})
	}
	return items, rows.Err()
}

func (h *AudioStoryHandler) UpdateCollection(w http.ResponseWriter, r *http.Request) {
	if !audioWriter(r) {
		audioFail(w, http.StatusForbidden, "FORBIDDEN", "audio writer access required")
		return
	}
	id := chi.URLParam(r, "id")
	var req audioCollectionUpdateRequest
	if !decodeAudioBody(w, r, &req) {
		return
	}
	if req.Title != nil && strings.TrimSpace(*req.Title) == "" {
		audioFail(w, http.StatusBadRequest, "VALIDATION_FAILED", "title cannot be empty")
		return
	}
	ctx := r.Context()
	collection, err := h.loadCollection(ctx, id)
	if err != nil {
		audioFail(w, http.StatusNotFound, "NOT_FOUND", "audio story collection not found")
		return
	}
	if collection.Status == "archived" {
		audioFail(w, http.StatusConflict, "COLLECTION_ARCHIVED", "an archived collection cannot be updated")
		return
	}
	actor := middleware.UserID(ctx)
	_, err = h.pool.Exec(ctx,
		`UPDATE audio_story_collections SET
		 title=CASE WHEN $2 THEN $3 ELSE title END,
		 description=CASE WHEN $4 THEN $5 ELSE description END,
		 cover_media_id=CASE WHEN $6 THEN NULLIF($7,'')::uuid ELSE cover_media_id END,
		 display_order=CASE WHEN $8 THEN $9 ELSE display_order END,
		 featured=CASE WHEN $10 THEN $11 ELSE featured END,
		 updated_by=$12,updated_at=now()
		 WHERE id=$1`,
		id, req.Title != nil, valueOr(req.Title, ""), req.Description != nil, valueOr(req.Description, ""),
		req.CoverMediaID != nil, valueOr(req.CoverMediaID, ""), req.DisplayOrder != nil, valueOr(req.DisplayOrder, 0),
		req.Featured != nil, valueOr(req.Featured, false), actor)
	if err != nil {
		audioFail(w, http.StatusBadRequest, "VALIDATION_FAILED", "the collection could not be updated")
		return
	}
	_ = writeAudioCollectionEvent(ctx, h.pool, id, actor,
		"audio_story_collection.updated", collection.Status, collection.Status, "", nil)
	writeJSON(w, http.StatusOK, map[string]string{"status": "saved"})
}

func (h *AudioStoryHandler) SetCollectionStories(w http.ResponseWriter, r *http.Request) {
	if !audioWriter(r) {
		audioFail(w, http.StatusForbidden, "FORBIDDEN", "audio writer access required")
		return
	}
	id := chi.URLParam(r, "id")
	var req audioCollectionStoriesRequest
	if !decodeAudioBody(w, r, &req) {
		return
	}
	seen := make(map[string]bool, len(req.StoryIDs))
	for _, storyID := range req.StoryIDs {
		if strings.TrimSpace(storyID) == "" || seen[storyID] || !isUUID(storyID) {
			audioFail(w, http.StatusBadRequest, "VALIDATION_FAILED", "storyIds must be unique, non-empty UUIDs")
			return
		}
		seen[storyID] = true
	}

	ctx := r.Context()
	tx, err := h.pool.Begin(ctx)
	if err != nil {
		audioFail(w, http.StatusInternalServerError, "INTERNAL_ERROR", "failed to start transaction")
		return
	}
	defer tx.Rollback(ctx)

	var status string
	if err := tx.QueryRow(ctx,
		`SELECT status FROM audio_story_collections WHERE id=$1 FOR UPDATE`, id).Scan(&status); err != nil {
		audioFail(w, http.StatusNotFound, "NOT_FOUND", "audio story collection not found")
		return
	}
	if status == "archived" {
		audioFail(w, http.StatusConflict, "COLLECTION_ARCHIVED", "an archived collection cannot be reordered")
		return
	}

	if len(req.StoryIDs) == 0 {
		if _, err := tx.Exec(ctx,
			`UPDATE audio_stories SET collection_id=NULL,collection_order=0,updated_at=now()
			 WHERE collection_id=$1`, id); err != nil {
			audioFail(w, http.StatusInternalServerError, "INTERNAL_ERROR", "failed to clear collection stories")
			return
		}
	} else {
		if _, err := tx.Exec(ctx,
			`UPDATE audio_stories SET collection_id=NULL,collection_order=0,updated_at=now()
			 WHERE collection_id=$1 AND id <> ALL($2::uuid[])`, id, req.StoryIDs); err != nil {
			audioFail(w, http.StatusInternalServerError, "INTERNAL_ERROR", "failed to clear removed stories")
			return
		}
		for index, storyID := range req.StoryIDs {
			tag, err := tx.Exec(ctx,
				`UPDATE audio_stories SET collection_id=$1,collection_order=$3,updated_at=now()
				 WHERE id=$2`, id, storyID, index+1)
			if err != nil {
				audioFail(w, http.StatusBadRequest, "VALIDATION_FAILED", "storyIds contains an invalid id")
				return
			}
			if tag.RowsAffected() == 0 {
				audioFail(w, http.StatusNotFound, "NOT_FOUND", "audio story not found: "+storyID)
				return
			}
		}
	}

	actor := middleware.UserID(ctx)
	if err := writeAudioCollectionEvent(ctx, tx, id, actor,
		"audio_story_collection.stories_ordered", status, status, "",
		map[string]any{"storyIds": req.StoryIDs, "count": len(req.StoryIDs)}); err != nil {
		audioFail(w, http.StatusInternalServerError, "INTERNAL_ERROR", "failed to write audit event")
		return
	}
	if err := tx.Commit(ctx); err != nil {
		audioFail(w, http.StatusInternalServerError, "INTERNAL_ERROR", "failed to commit story ordering")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"status": "saved", "count": len(req.StoryIDs)})
}

func (h *AudioStoryHandler) collectionHasPublishableStory(ctx context.Context, collectionID string) (bool, error) {
	var exists bool
	err := h.pool.QueryRow(ctx,
		`SELECT EXISTS(
		   SELECT 1 FROM audio_stories s
		   WHERE s.collection_id=$1 AND s.status NOT IN ('archived','revoked')
		     AND (s.status='published'
		          OR EXISTS (SELECT 1 FROM audio_story_revisions r
		                     WHERE r.story_id=s.id
		                       AND r.status IN ('approved','published','scheduled')
		                       AND r.revoked_at IS NULL)))`, collectionID).Scan(&exists)
	return exists, err
}

func (h *AudioStoryHandler) PublishCollection(w http.ResponseWriter, r *http.Request) {
	if !audioPublisher(r) {
		audioFail(w, http.StatusForbidden, "FORBIDDEN", "administrator access required")
		return
	}
	id := chi.URLParam(r, "id")
	ctx := r.Context()
	collection, err := h.loadCollection(ctx, id)
	if err != nil {
		audioFail(w, http.StatusNotFound, "NOT_FOUND", "audio story collection not found")
		return
	}
	if collection.Status == "archived" {
		audioFail(w, http.StatusConflict, "COLLECTION_ARCHIVED", "an archived collection cannot be published")
		return
	}
	publishable, err := h.collectionHasPublishableStory(ctx, id)
	if err != nil {
		audioFail(w, http.StatusInternalServerError, "INTERNAL_ERROR", "failed to check collection stories")
		return
	}
	if !publishable {
		audioFail(w, http.StatusConflict, "COLLECTION_EMPTY", "a collection needs at least one approved or published story")
		return
	}

	tx, err := h.pool.Begin(ctx)
	if err != nil {
		audioFail(w, http.StatusInternalServerError, "INTERNAL_ERROR", "failed to start transaction")
		return
	}
	defer tx.Rollback(ctx)
	if _, err := tx.Exec(ctx,
		`UPDATE audio_story_collections SET status='published',publish_at=NULL,
		 first_published_at=COALESCE(first_published_at,now()),updated_by=$2,updated_at=now()
		 WHERE id=$1`, id, middleware.UserID(ctx)); err != nil {
		audioFail(w, http.StatusInternalServerError, "INTERNAL_ERROR", "failed to publish the collection")
		return
	}
	if err := writeAudioCollectionEvent(ctx, tx, id, middleware.UserID(ctx),
		"audio_story_collection.published", collection.Status, "published", "", nil); err != nil {
		audioFail(w, http.StatusInternalServerError, "INTERNAL_ERROR", "failed to write audit event")
		return
	}
	if err := tx.Commit(ctx); err != nil {
		audioFail(w, http.StatusInternalServerError, "INTERNAL_ERROR", "failed to commit publication")
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "published"})
}

func (h *AudioStoryHandler) ScheduleCollection(w http.ResponseWriter, r *http.Request) {
	if !audioPublisher(r) {
		audioFail(w, http.StatusForbidden, "FORBIDDEN", "administrator access required")
		return
	}
	id := chi.URLParam(r, "id")
	var req audioScheduleRequest
	if !decodeAudioBody(w, r, &req) {
		return
	}
	publishAt, err := time.Parse(time.RFC3339, strings.TrimSpace(req.PublishAt))
	if err != nil || !publishAt.After(time.Now()) {
		audioFail(w, http.StatusBadRequest, "VALIDATION_FAILED", "publishAt must be a future RFC3339 timestamp")
		return
	}
	ctx := r.Context()
	collection, err := h.loadCollection(ctx, id)
	if err != nil {
		audioFail(w, http.StatusNotFound, "NOT_FOUND", "audio story collection not found")
		return
	}
	if collection.Status == "archived" {
		audioFail(w, http.StatusConflict, "COLLECTION_ARCHIVED", "an archived collection cannot be scheduled")
		return
	}
	publishable, err := h.collectionHasPublishableStory(ctx, id)
	if err != nil {
		audioFail(w, http.StatusInternalServerError, "INTERNAL_ERROR", "failed to check collection stories")
		return
	}
	if !publishable {
		audioFail(w, http.StatusConflict, "COLLECTION_EMPTY", "a collection needs at least one approved or published story")
		return
	}

	tx, err := h.pool.Begin(ctx)
	if err != nil {
		audioFail(w, http.StatusInternalServerError, "INTERNAL_ERROR", "failed to start transaction")
		return
	}
	defer tx.Rollback(ctx)
	if _, err := tx.Exec(ctx,
		`UPDATE audio_story_collections SET status='scheduled',publish_at=$2,updated_by=$3,updated_at=now()
		 WHERE id=$1`, id, publishAt.UTC(), middleware.UserID(ctx)); err != nil {
		audioFail(w, http.StatusInternalServerError, "INTERNAL_ERROR", "failed to schedule the collection")
		return
	}
	if err := writeAudioCollectionEvent(ctx, tx, id, middleware.UserID(ctx),
		"audio_story_collection.scheduled", collection.Status, "scheduled", "",
		map[string]string{"publishAt": publishAt.UTC().Format(time.RFC3339)}); err != nil {
		audioFail(w, http.StatusInternalServerError, "INTERNAL_ERROR", "failed to write audit event")
		return
	}
	if err := tx.Commit(ctx); err != nil {
		audioFail(w, http.StatusInternalServerError, "INTERNAL_ERROR", "failed to commit schedule")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"status": "scheduled", "publishAt": publishAt.UTC()})
}

func (h *AudioStoryHandler) UnpublishCollection(w http.ResponseWriter, r *http.Request) {
	if !audioPublisher(r) {
		audioFail(w, http.StatusForbidden, "FORBIDDEN", "administrator access required")
		return
	}
	id := chi.URLParam(r, "id")
	var req audioReasonRequest
	if !decodeAudioBody(w, r, &req) {
		return
	}
	if strings.TrimSpace(req.Reason) == "" {
		audioFail(w, http.StatusBadRequest, "REASON_REQUIRED", "a reason is required to unpublish a collection")
		return
	}
	ctx := r.Context()
	collection, err := h.loadCollection(ctx, id)
	if err != nil {
		audioFail(w, http.StatusNotFound, "NOT_FOUND", "audio story collection not found")
		return
	}
	if collection.Status != "published" && collection.Status != "scheduled" {
		audioFail(w, http.StatusConflict, "INVALID_TRANSITION", "the collection is not published or scheduled")
		return
	}
	tx, err := h.pool.Begin(ctx)
	if err != nil {
		audioFail(w, http.StatusInternalServerError, "INTERNAL_ERROR", "failed to start transaction")
		return
	}
	defer tx.Rollback(ctx)
	if _, err := tx.Exec(ctx,
		`UPDATE audio_story_collections SET status='unpublished',publish_at=NULL,updated_by=$2,updated_at=now()
		 WHERE id=$1`, id, middleware.UserID(ctx)); err != nil {
		audioFail(w, http.StatusInternalServerError, "INTERNAL_ERROR", "failed to unpublish the collection")
		return
	}
	if err := writeAudioCollectionEvent(ctx, tx, id, middleware.UserID(ctx),
		"audio_story_collection.unpublished", collection.Status, "unpublished", req.Reason, nil); err != nil {
		audioFail(w, http.StatusInternalServerError, "INTERNAL_ERROR", "failed to write audit event")
		return
	}
	if err := tx.Commit(ctx); err != nil {
		audioFail(w, http.StatusInternalServerError, "INTERNAL_ERROR", "failed to commit unpublish")
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "unpublished"})
}

func (h *AudioStoryHandler) ArchiveCollection(w http.ResponseWriter, r *http.Request) {
	if !audioSuperAdmin(r) {
		audioFail(w, http.StatusForbidden, "FORBIDDEN", "super administrator access required")
		return
	}
	id := chi.URLParam(r, "id")
	var req audioReasonRequest
	if !decodeAudioBody(w, r, &req) {
		return
	}
	if strings.TrimSpace(req.Reason) == "" {
		audioFail(w, http.StatusBadRequest, "REASON_REQUIRED", "a reason is required to archive a collection")
		return
	}
	ctx := r.Context()
	collection, err := h.loadCollection(ctx, id)
	if err != nil {
		audioFail(w, http.StatusNotFound, "NOT_FOUND", "audio story collection not found")
		return
	}
	if collection.Status == "archived" {
		audioFail(w, http.StatusConflict, "INVALID_TRANSITION", "the collection is already archived")
		return
	}
	tx, err := h.pool.Begin(ctx)
	if err != nil {
		audioFail(w, http.StatusInternalServerError, "INTERNAL_ERROR", "failed to start transaction")
		return
	}
	defer tx.Rollback(ctx)
	if _, err := tx.Exec(ctx,
		`UPDATE audio_story_collections SET status='archived',publish_at=NULL,updated_by=$2,updated_at=now()
		 WHERE id=$1`, id, middleware.UserID(ctx)); err != nil {
		audioFail(w, http.StatusInternalServerError, "INTERNAL_ERROR", "failed to archive the collection")
		return
	}
	if err := writeAudioCollectionEvent(ctx, tx, id, middleware.UserID(ctx),
		"audio_story_collection.archived", collection.Status, "archived", req.Reason, nil); err != nil {
		audioFail(w, http.StatusInternalServerError, "INTERNAL_ERROR", "failed to write audit event")
		return
	}
	if err := tx.Commit(ctx); err != nil {
		audioFail(w, http.StatusInternalServerError, "INTERNAL_ERROR", "failed to commit archive")
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "archived"})
}

func (h *AudioStoryHandler) DeleteCollection(w http.ResponseWriter, r *http.Request) {
	if !audioWriter(r) {
		audioFail(w, http.StatusForbidden, "FORBIDDEN", "audio writer access required")
		return
	}
	id := chi.URLParam(r, "id")
	ctx := r.Context()
	tx, err := h.pool.Begin(ctx)
	if err != nil {
		audioFail(w, http.StatusInternalServerError, "INTERNAL_ERROR", "failed to start transaction")
		return
	}
	defer tx.Rollback(ctx)

	var status string
	var firstPublished pgtype.Timestamp
	var storyCount int
	err = tx.QueryRow(ctx,
		`SELECT c.status,c.first_published_at,
		 (SELECT count(*) FROM audio_stories s WHERE s.collection_id=c.id)
		 FROM audio_story_collections c WHERE c.id=$1 FOR UPDATE`, id).Scan(&status, &firstPublished, &storyCount)
	if err != nil {
		audioFail(w, http.StatusNotFound, "NOT_FOUND", "audio story collection not found")
		return
	}
	if status != "draft" || firstPublished.Valid || storyCount > 0 {
		audioFail(w, http.StatusConflict, "COLLECTION_NOT_DELETABLE", "only an empty, never-published draft collection can be deleted")
		return
	}
	if _, err := tx.Exec(ctx, `DELETE FROM audio_story_collections WHERE id=$1`, id); err != nil {
		audioFail(w, http.StatusInternalServerError, "INTERNAL_ERROR", "failed to delete the collection")
		return
	}
	if err := writeAudioCollectionEvent(ctx, tx, id, middleware.UserID(ctx),
		"audio_story_collection.deleted", "draft", "", "", nil); err != nil {
		audioFail(w, http.StatusInternalServerError, "INTERNAL_ERROR", "failed to write audit event")
		return
	}
	if err := tx.Commit(ctx); err != nil {
		audioFail(w, http.StatusInternalServerError, "INTERNAL_ERROR", "failed to commit deletion")
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "deleted"})
}
