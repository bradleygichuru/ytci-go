package admin

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/bradleygichuru/ytci-go/internal/middleware"
)

var (
	errAudioStoryNotFound     = errors.New("audio story not found")
	errAudioRevisionNotFound  = errors.New("audio revision not found")
	errAudioInvalidTransition = errors.New("invalid audio revision transition")
)

// ---------------------------------------------------------------------------
// Publish request types

type audioScheduleRequest struct {
	PublishAt string `json:"publishAt"`
}

type audioReasonRequest struct {
	Reason string `json:"reason"`
}

type audioRevokeRequest struct {
	Reason            string `json:"reason"`
	EvidenceReference string `json:"evidenceReference"`
}

// AudioPublishDueResult reports the transitions made by one scheduler tick.
type AudioPublishDueResult struct {
	Collections int `json:"collections"`
	Stories     int `json:"stories"`
}

// ---------------------------------------------------------------------------
// Publish gates

type audioPublishCheck struct {
	StoryID          string
	StoryStatus      string
	LiveRevisionID   string
	CollectionID     string
	CollectionStatus string
	RevisionID       string
	RevisionStatus   string
	RiskTier         string
	RightsExpiresAt  *time.Time
	Approvals        int
	Readiness        *audioReadiness
}

func (h *AudioStoryHandler) loadPublishCheck(ctx context.Context, storyID, revisionID string) (*audioPublishCheck, error) {
	pc := &audioPublishCheck{StoryID: storyID, RevisionID: revisionID}
	var expires pgtype.Timestamp
	err := h.pool.QueryRow(ctx,
		`SELECT s.status,COALESCE(s.live_revision_id::text,''),COALESCE(s.collection_id::text,''),
		 COALESCE(c.status,'')
		 FROM audio_stories s LEFT JOIN audio_story_collections c ON c.id=s.collection_id
		 WHERE s.id=$1`, storyID).Scan(
		&pc.StoryStatus, &pc.LiveRevisionID, &pc.CollectionID, &pc.CollectionStatus)
	if err != nil {
		return nil, errAudioStoryNotFound
	}
	err = h.pool.QueryRow(ctx,
		`SELECT status,risk_tier,rights_expires_at FROM audio_story_revisions
		 WHERE id=$1 AND story_id=$2`, revisionID, storyID).Scan(
		&pc.RevisionStatus, &pc.RiskTier, &expires)
	if err != nil {
		return nil, errAudioRevisionNotFound
	}
	pc.RightsExpiresAt = tsPtr(expires)
	pc.Approvals, err = h.approvalCount(ctx, revisionID)
	if err != nil {
		return nil, err
	}
	pc.Readiness, err = h.computeReadiness(ctx, storyID, revisionID)
	if err != nil {
		return nil, err
	}
	return pc, nil
}

// audioPublishGate returns the HTTP status/code/message when publication is
// blocked. A nil result means the revision may go live.
type audioPublishGateError struct {
	Status  int
	Code    string
	Message string
}

func audioPublishGate(pc *audioPublishCheck) *audioPublishGateError {
	if pc.StoryStatus == "archived" || pc.StoryStatus == "revoked" {
		return &audioPublishGateError{http.StatusConflict, "INVALID_TRANSITION", "archived or revoked stories cannot be published"}
	}
	if pc.RiskTier == "restricted" {
		return &audioPublishGateError{http.StatusForbidden, "RESTRICTED_REVIEW", "restricted stories cannot be approved or published"}
	}
	if pc.RiskTier == "sensitive" && pc.Approvals < 2 {
		return &audioPublishGateError{http.StatusConflict, "SECOND_REVIEW_REQUIRED", "a sensitive story needs two distinct approvals"}
	}
	if pc.Readiness == nil || !pc.Readiness.Ready {
		return &audioPublishGateError{http.StatusUnprocessableEntity, "READINESS_BLOCKED", "revision is not ready for publication"}
	}
	if pc.CollectionStatus != "published" {
		return &audioPublishGateError{http.StatusConflict, "COLLECTION_NOT_PUBLISHED", "the story must belong to a published collection"}
	}
	return nil
}

func writeAudioPublishGate(w http.ResponseWriter, pc *audioPublishCheck, gate *audioPublishGateError) {
	if gate == nil {
		return
	}
	body := map[string]any{"error": map[string]string{"code": gate.Code, "message": gate.Message}}
	if gate.Code == "READINESS_BLOCKED" && pc != nil && pc.Readiness != nil {
		body["readiness"] = pc.Readiness
	}
	writeJSON(w, gate.Status, body)
}

// ---------------------------------------------------------------------------
// Publish, schedule and cancel

func (h *AudioStoryHandler) PublishRevision(w http.ResponseWriter, r *http.Request) {
	if !audioPublisher(r) {
		audioFail(w, http.StatusForbidden, "FORBIDDEN", "administrator access required")
		return
	}
	storyID, revisionID := chi.URLParam(r, "id"), chi.URLParam(r, "revisionId")
	ctx := r.Context()

	pc, err := h.loadPublishCheck(ctx, storyID, revisionID)
	if err != nil {
		audioFail(w, http.StatusNotFound, "NOT_FOUND", "revision not found")
		return
	}
	if pc.RevisionStatus != "approved" {
		if pc.RiskTier == "restricted" {
			audioFail(w, http.StatusForbidden, "RESTRICTED_REVIEW", "restricted stories cannot be approved or published")
			return
		}
		if pc.RevisionStatus == "in_review" && pc.RiskTier == "sensitive" && pc.Approvals < 2 {
			writeAudioPublishGate(w, pc, &audioPublishGateError{
				http.StatusConflict, "SECOND_REVIEW_REQUIRED", "a sensitive story needs two distinct approvals",
			})
			return
		}
		audioFail(w, http.StatusConflict, "INVALID_TRANSITION", "only an approved revision can be published")
		return
	}
	if gate := audioPublishGate(pc); gate != nil {
		writeAudioPublishGate(w, pc, gate)
		return
	}

	tx, err := h.pool.Begin(ctx)
	if err != nil {
		audioFail(w, http.StatusInternalServerError, "INTERNAL_ERROR", "failed to start transaction")
		return
	}
	defer tx.Rollback(ctx)

	version, changed, err := h.promoteRevision(ctx, tx, storyID, revisionID, middleware.UserID(ctx))
	if err != nil {
		if errors.Is(err, errAudioInvalidTransition) {
			audioFail(w, http.StatusConflict, "INVALID_TRANSITION", "the revision is no longer publishable")
			return
		}
		audioFail(w, http.StatusInternalServerError, "INTERNAL_ERROR", "failed to publish the revision")
		return
	}
	if err := tx.Commit(ctx); err != nil {
		audioFail(w, http.StatusInternalServerError, "INTERNAL_ERROR", "failed to commit publication")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"status": "published", "revisionId": revisionID,
		"contentVersion": version, "contentChanged": changed,
	})
}

func (h *AudioStoryHandler) ScheduleRevision(w http.ResponseWriter, r *http.Request) {
	if !audioPublisher(r) {
		audioFail(w, http.StatusForbidden, "FORBIDDEN", "administrator access required")
		return
	}
	storyID, revisionID := chi.URLParam(r, "id"), chi.URLParam(r, "revisionId")
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
	pc, err := h.loadPublishCheck(ctx, storyID, revisionID)
	if err != nil {
		audioFail(w, http.StatusNotFound, "NOT_FOUND", "revision not found")
		return
	}
	if pc.RevisionStatus != "approved" {
		audioFail(w, http.StatusConflict, "INVALID_TRANSITION", "only an approved revision can be scheduled")
		return
	}
	if gate := audioPublishGate(pc); gate != nil {
		writeAudioPublishGate(w, pc, gate)
		return
	}

	tx, err := h.pool.Begin(ctx)
	if err != nil {
		audioFail(w, http.StatusInternalServerError, "INTERNAL_ERROR", "failed to start transaction")
		return
	}
	defer tx.Rollback(ctx)

	actor := middleware.UserID(ctx)
	tag, err := tx.Exec(ctx,
		`UPDATE audio_story_revisions SET status='scheduled',publish_at=$3,updated_at=now()
		 WHERE id=$1 AND story_id=$2 AND status='approved'`, revisionID, storyID, publishAt.UTC())
	if err != nil || tag.RowsAffected() == 0 {
		audioFail(w, http.StatusConflict, "INVALID_TRANSITION", "the revision can no longer be scheduled")
		return
	}
	if _, err := tx.Exec(ctx,
		`UPDATE audio_stories SET
		 status=CASE WHEN first_published_at IS NULL THEN 'scheduled' ELSE 'published' END,
		 open_revision_id=$2,updated_by=$3,updated_at=now()
		 WHERE id=$1`, storyID, revisionID, actor); err != nil {
		audioFail(w, http.StatusInternalServerError, "INTERNAL_ERROR", "failed to schedule the story")
		return
	}
	if err := writeAudioEvent(ctx, tx, storyID, revisionID, "", actor,
		"audio_story.publish_scheduled", "approved", "scheduled", "",
		map[string]string{"publishAt": publishAt.UTC().Format(time.RFC3339)}); err != nil {
		audioFail(w, http.StatusInternalServerError, "INTERNAL_ERROR", "failed to write audit event")
		return
	}
	if err := tx.Commit(ctx); err != nil {
		audioFail(w, http.StatusInternalServerError, "INTERNAL_ERROR", "failed to commit schedule")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"status": "scheduled", "publishAt": publishAt.UTC(),
	})
}

func (h *AudioStoryHandler) CancelSchedule(w http.ResponseWriter, r *http.Request) {
	if !audioPublisher(r) {
		audioFail(w, http.StatusForbidden, "FORBIDDEN", "administrator access required")
		return
	}
	storyID, revisionID := chi.URLParam(r, "id"), chi.URLParam(r, "revisionId")
	var req audioReasonRequest
	_ = decodeAudioBody(w, r, &req) // reason is optional for cancel-schedule
	ctx := r.Context()

	tx, err := h.pool.Begin(ctx)
	if err != nil {
		audioFail(w, http.StatusInternalServerError, "INTERNAL_ERROR", "failed to start transaction")
		return
	}
	defer tx.Rollback(ctx)

	var revisionStatus string
	var firstPublished pgtype.Timestamp
	err = tx.QueryRow(ctx,
		`SELECT r.status,s.first_published_at FROM audio_story_revisions r
		 JOIN audio_stories s ON s.id=r.story_id
		 WHERE r.id=$1 AND r.story_id=$2 FOR UPDATE`,
		revisionID, storyID).Scan(&revisionStatus, &firstPublished)
	if err != nil {
		audioFail(w, http.StatusNotFound, "NOT_FOUND", "revision not found")
		return
	}
	if revisionStatus != "scheduled" {
		audioFail(w, http.StatusConflict, "INVALID_TRANSITION", "the revision is not scheduled")
		return
	}

	storyStatus := "published"
	if !firstPublished.Valid {
		storyStatus = "in_review"
	}
	actor := middleware.UserID(ctx)
	if _, err := tx.Exec(ctx,
		`UPDATE audio_story_revisions SET status='approved',publish_at=NULL,updated_at=now()
		 WHERE id=$1 AND story_id=$2`, revisionID, storyID); err != nil {
		audioFail(w, http.StatusInternalServerError, "INTERNAL_ERROR", "failed to cancel the schedule")
		return
	}
	if _, err := tx.Exec(ctx,
		`UPDATE audio_stories SET status=$2,updated_by=$3,updated_at=now() WHERE id=$1`,
		storyID, storyStatus, actor); err != nil {
		audioFail(w, http.StatusInternalServerError, "INTERNAL_ERROR", "failed to restore the story status")
		return
	}
	if err := writeAudioEvent(ctx, tx, storyID, revisionID, "", actor,
		"audio_story.publish_cancelled", "scheduled", "approved", req.Reason, nil); err != nil {
		audioFail(w, http.StatusInternalServerError, "INTERNAL_ERROR", "failed to write audit event")
		return
	}
	if err := tx.Commit(ctx); err != nil {
		audioFail(w, http.StatusInternalServerError, "INTERNAL_ERROR", "failed to commit cancellation")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"status": "approved", "storyStatus": storyStatus})
}

// ---------------------------------------------------------------------------
// Story lifecycle

func (h *AudioStoryHandler) UnpublishStory(w http.ResponseWriter, r *http.Request) {
	if !audioPublisher(r) {
		audioFail(w, http.StatusForbidden, "FORBIDDEN", "administrator access required")
		return
	}
	storyID := chi.URLParam(r, "id")
	var req audioReasonRequest
	if !decodeAudioBody(w, r, &req) {
		return
	}
	if strings.TrimSpace(req.Reason) == "" {
		audioFail(w, http.StatusBadRequest, "REASON_REQUIRED", "a reason is required to unpublish a story")
		return
	}

	ctx := r.Context()
	tx, err := h.pool.Begin(ctx)
	if err != nil {
		audioFail(w, http.StatusInternalServerError, "INTERNAL_ERROR", "failed to start transaction")
		return
	}
	defer tx.Rollback(ctx)

	var status, liveID, openID string
	err = tx.QueryRow(ctx,
		`SELECT status,COALESCE(live_revision_id::text,''),COALESCE(open_revision_id::text,'')
		 FROM audio_stories WHERE id=$1 FOR UPDATE`, storyID).Scan(&status, &liveID, &openID)
	if err != nil {
		audioFail(w, http.StatusNotFound, "NOT_FOUND", "audio story not found")
		return
	}
	if liveID == "" || (status != "published" && status != "scheduled") {
		audioFail(w, http.StatusConflict, "INVALID_TRANSITION", "only a live story can be unpublished")
		return
	}
	actor := middleware.UserID(ctx)
	if err := h.cancelOpenRevision(ctx, tx, storyID, liveID, openID); err != nil {
		audioFail(w, http.StatusInternalServerError, "INTERNAL_ERROR", "failed to cancel the open revision")
		return
	}
	if _, err := tx.Exec(ctx,
		`UPDATE audio_stories SET status='unpublished',open_revision_id=NULL,unpublished_at=now(),
		 updated_by=$2,updated_at=now() WHERE id=$1`, storyID, actor); err != nil {
		audioFail(w, http.StatusInternalServerError, "INTERNAL_ERROR", "failed to unpublish the story")
		return
	}
	if err := writeAudioEvent(ctx, tx, storyID, liveID, "", actor,
		"audio_story.unpublished", status, "unpublished", req.Reason, nil); err != nil {
		audioFail(w, http.StatusInternalServerError, "INTERNAL_ERROR", "failed to write audit event")
		return
	}
	if err := tx.Commit(ctx); err != nil {
		audioFail(w, http.StatusInternalServerError, "INTERNAL_ERROR", "failed to commit unpublish")
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "unpublished"})
}

func (h *AudioStoryHandler) RepublishStory(w http.ResponseWriter, r *http.Request) {
	if !audioPublisher(r) {
		audioFail(w, http.StatusForbidden, "FORBIDDEN", "administrator access required")
		return
	}
	storyID := chi.URLParam(r, "id")
	ctx := r.Context()

	var status, liveID, liveStatus, collectionStatus string
	var revoked, expires pgtype.Timestamp
	err := h.pool.QueryRow(ctx,
		`SELECT s.status,COALESCE(s.live_revision_id::text,''),COALESCE(r.status,''),COALESCE(c.status,''),
		 r.revoked_at,r.rights_expires_at
		 FROM audio_stories s
		 LEFT JOIN audio_story_revisions r ON r.id=s.live_revision_id
		 LEFT JOIN audio_story_collections c ON c.id=s.collection_id
		 WHERE s.id=$1`, storyID).Scan(&status, &liveID, &liveStatus, &collectionStatus, &revoked, &expires)
	if err != nil {
		audioFail(w, http.StatusNotFound, "NOT_FOUND", "audio story not found")
		return
	}
	if liveID == "" || liveStatus != "published" || revoked.Valid {
		audioFail(w, http.StatusConflict, "NO_LIVE_REVISION", "the story has no live revision to republish")
		return
	}
	if status == "archived" || status == "revoked" {
		audioFail(w, http.StatusConflict, "INVALID_TRANSITION", "the story cannot be republished from its current state")
		return
	}
	if collectionStatus != "published" {
		audioFail(w, http.StatusConflict, "COLLECTION_NOT_PUBLISHED", "the story must belong to a published collection")
		return
	}
	if expires.Valid && !expires.Time.After(time.Now()) {
		audioFail(w, http.StatusConflict, "RIGHTS_EXPIRED", "the rights for the live revision have expired")
		return
	}
	actor := middleware.UserID(ctx)
	if _, err := h.pool.Exec(ctx,
		`UPDATE audio_stories SET status='published',unpublished_at=NULL,updated_by=$2,updated_at=now()
		 WHERE id=$1`, storyID, actor); err != nil {
		audioFail(w, http.StatusInternalServerError, "INTERNAL_ERROR", "failed to republish the story")
		return
	}
	_ = writeAudioEvent(ctx, h.pool, storyID, liveID, "", actor,
		"audio_story.republished", status, "published", "", nil)
	writeJSON(w, http.StatusOK, map[string]string{"status": "published"})
}

func (h *AudioStoryHandler) ArchiveStory(w http.ResponseWriter, r *http.Request) {
	if !audioSuperAdmin(r) {
		audioFail(w, http.StatusForbidden, "FORBIDDEN", "super administrator access required")
		return
	}
	storyID := chi.URLParam(r, "id")
	var req audioReasonRequest
	if !decodeAudioBody(w, r, &req) {
		return
	}
	if strings.TrimSpace(req.Reason) == "" {
		audioFail(w, http.StatusBadRequest, "REASON_REQUIRED", "a reason is required to archive a story")
		return
	}

	ctx := r.Context()
	tx, err := h.pool.Begin(ctx)
	if err != nil {
		audioFail(w, http.StatusInternalServerError, "INTERNAL_ERROR", "failed to start transaction")
		return
	}
	defer tx.Rollback(ctx)

	var status, liveID, openID string
	err = tx.QueryRow(ctx,
		`SELECT status,COALESCE(live_revision_id::text,''),COALESCE(open_revision_id::text,'')
		 FROM audio_stories WHERE id=$1 FOR UPDATE`, storyID).Scan(&status, &liveID, &openID)
	if err != nil {
		audioFail(w, http.StatusNotFound, "NOT_FOUND", "audio story not found")
		return
	}
	if status == "archived" {
		audioFail(w, http.StatusConflict, "INVALID_TRANSITION", "the story is already archived")
		return
	}
	actor := middleware.UserID(ctx)
	if err := h.cancelOpenRevision(ctx, tx, storyID, liveID, openID); err != nil {
		audioFail(w, http.StatusInternalServerError, "INTERNAL_ERROR", "failed to cancel the open revision")
		return
	}
	if _, err := tx.Exec(ctx,
		`UPDATE audio_stories SET status='archived',open_revision_id=NULL,archived_at=now(),
		 updated_by=$2,updated_at=now() WHERE id=$1`, storyID, actor); err != nil {
		audioFail(w, http.StatusInternalServerError, "INTERNAL_ERROR", "failed to archive the story")
		return
	}
	if err := writeAudioEvent(ctx, tx, storyID, liveID, "", actor,
		"audio_story.archived", status, "archived", req.Reason, nil); err != nil {
		audioFail(w, http.StatusInternalServerError, "INTERNAL_ERROR", "failed to write audit event")
		return
	}
	if err := tx.Commit(ctx); err != nil {
		audioFail(w, http.StatusInternalServerError, "INTERNAL_ERROR", "failed to commit archive")
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "archived"})
}

func (h *AudioStoryHandler) RevokeStory(w http.ResponseWriter, r *http.Request) {
	if !audioSuperAdmin(r) {
		audioFail(w, http.StatusForbidden, "FORBIDDEN", "super administrator access required")
		return
	}
	storyID := chi.URLParam(r, "id")
	var req audioRevokeRequest
	if !decodeAudioBody(w, r, &req) {
		return
	}
	if strings.TrimSpace(req.Reason) == "" || strings.TrimSpace(req.EvidenceReference) == "" {
		audioFail(w, http.StatusBadRequest, "VALIDATION_FAILED", "reason and evidenceReference are required to revoke a story")
		return
	}

	ctx := r.Context()
	tx, err := h.pool.Begin(ctx)
	if err != nil {
		audioFail(w, http.StatusInternalServerError, "INTERNAL_ERROR", "failed to start transaction")
		return
	}
	defer tx.Rollback(ctx)

	var status, liveID, openID string
	var revoked pgtype.Timestamp
	err = tx.QueryRow(ctx,
		`SELECT status,COALESCE(live_revision_id::text,''),COALESCE(open_revision_id::text,''),revoked_at
		 FROM audio_stories WHERE id=$1 FOR UPDATE`, storyID).Scan(&status, &liveID, &openID, &revoked)
	if err != nil {
		audioFail(w, http.StatusNotFound, "NOT_FOUND", "audio story not found")
		return
	}
	if liveID == "" {
		audioFail(w, http.StatusConflict, "NO_LIVE_REVISION", "the story has no live revision to revoke")
		return
	}
	if revoked.Valid || status == "revoked" {
		audioFail(w, http.StatusConflict, "INVALID_TRANSITION", "the story is already revoked")
		return
	}
	actor := middleware.UserID(ctx)
	if err := h.cancelOpenRevision(ctx, tx, storyID, liveID, openID); err != nil {
		audioFail(w, http.StatusInternalServerError, "INTERNAL_ERROR", "failed to cancel the open revision")
		return
	}
	if _, err := tx.Exec(ctx,
		`UPDATE audio_story_revisions SET revoked_at=now(),revocation_reason=$2,updated_at=now()
		 WHERE id=$1 AND story_id=$3`, liveID, req.Reason, storyID); err != nil {
		audioFail(w, http.StatusInternalServerError, "INTERNAL_ERROR", "failed to revoke the live revision")
		return
	}
	if _, err := tx.Exec(ctx,
		`UPDATE audio_stories SET status='revoked',revoked_at=now(),open_revision_id=NULL,
		 updated_by=$2,updated_at=now() WHERE id=$1`, storyID, actor); err != nil {
		audioFail(w, http.StatusInternalServerError, "INTERNAL_ERROR", "failed to revoke the story")
		return
	}
	if err := writeAudioEvent(ctx, tx, storyID, liveID, "", actor,
		"audio_story.revoked", status, "revoked", req.Reason,
		map[string]string{"evidenceReference": req.EvidenceReference}); err != nil {
		audioFail(w, http.StatusInternalServerError, "INTERNAL_ERROR", "failed to write audit event")
		return
	}
	if err := tx.Commit(ctx); err != nil {
		audioFail(w, http.StatusInternalServerError, "INTERNAL_ERROR", "failed to commit revocation")
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "revoked"})
}

func (h *AudioStoryHandler) RestoreStory(w http.ResponseWriter, r *http.Request) {
	if !audioSuperAdmin(r) {
		audioFail(w, http.StatusForbidden, "FORBIDDEN", "super administrator access required")
		return
	}
	storyID := chi.URLParam(r, "id")
	ctx := r.Context()
	tx, err := h.pool.Begin(ctx)
	if err != nil {
		audioFail(w, http.StatusInternalServerError, "INTERNAL_ERROR", "failed to start transaction")
		return
	}
	defer tx.Rollback(ctx)

	var status, liveID string
	var version int
	err = tx.QueryRow(ctx,
		`SELECT status,COALESCE(live_revision_id::text,''),content_version
		 FROM audio_stories WHERE id=$1 FOR UPDATE`, storyID).Scan(&status, &liveID, &version)
	if err != nil {
		audioFail(w, http.StatusNotFound, "NOT_FOUND", "audio story not found")
		return
	}
	if status != "revoked" {
		audioFail(w, http.StatusConflict, "INVALID_TRANSITION", "only a revoked story can be restored")
		return
	}
	if liveID == "" {
		audioFail(w, http.StatusConflict, "NO_LIVE_REVISION", "the story has no live revision to restore")
		return
	}

	actor := middleware.UserID(ctx)
	var newRevisionID string
	err = tx.QueryRow(ctx,
		`INSERT INTO audio_story_revisions (story_id,status,content_version,place_scope,counties,
		 destination_ids,content_note_categories,content_note_text,rights_basis,rights_holder,
		 licence_reference,rights_expires_at,risk_tier,created_by)
		 SELECT story_id,'draft',$3,place_scope,counties,destination_ids,content_note_categories,
		 content_note_text,rights_basis,rights_holder,licence_reference,rights_expires_at,risk_tier,$4
		 FROM audio_story_revisions WHERE id=$1 AND story_id=$2 RETURNING id`,
		liveID, storyID, version, actor).Scan(&newRevisionID)
	if err != nil {
		audioFail(w, http.StatusInternalServerError, "INTERNAL_ERROR", "failed to clone the revoked revision")
		return
	}
	if err := cloneAudioRevisionContent(ctx, tx, liveID, newRevisionID); err != nil {
		audioFail(w, http.StatusInternalServerError, "INTERNAL_ERROR", "failed to clone revision content")
		return
	}
	if _, err := tx.Exec(ctx,
		`UPDATE audio_stories SET status='draft',open_revision_id=$2,revoked_at=NULL,
		 updated_by=$3,updated_at=now() WHERE id=$1`, storyID, newRevisionID, actor); err != nil {
		audioFail(w, http.StatusInternalServerError, "INTERNAL_ERROR", "failed to restore the story")
		return
	}
	if err := writeAudioEvent(ctx, tx, storyID, newRevisionID, "", actor,
		"audio_story.restored", "revoked", "draft", "", map[string]string{"sourceRevisionId": liveID}); err != nil {
		audioFail(w, http.StatusInternalServerError, "INTERNAL_ERROR", "failed to write audit event")
		return
	}
	if err := tx.Commit(ctx); err != nil {
		audioFail(w, http.StatusInternalServerError, "INTERNAL_ERROR", "failed to commit restore")
		return
	}
	writeJSON(w, http.StatusCreated, map[string]string{
		"status": "draft", "revisionId": newRevisionID, "sourceRevisionId": liveID,
	})
}

// ---------------------------------------------------------------------------
// Promotion

func (h *AudioStoryHandler) promoteRevision(ctx context.Context, tx pgx.Tx, storyID, revisionID, actor string) (int, bool, error) {
	var storyStatus, liveID string
	var currentVersion int
	err := tx.QueryRow(ctx,
		`SELECT status,COALESCE(live_revision_id::text,''),content_version
		 FROM audio_stories WHERE id=$1 FOR UPDATE`, storyID).Scan(&storyStatus, &liveID, &currentVersion)
	if err != nil {
		return 0, false, errAudioStoryNotFound
	}
	var revisionStatus string
	err = tx.QueryRow(ctx,
		`SELECT status FROM audio_story_revisions WHERE id=$1 AND story_id=$2 FOR UPDATE`,
		revisionID, storyID).Scan(&revisionStatus)
	if err != nil {
		return 0, false, errAudioRevisionNotFound
	}
	if revisionStatus != "approved" && revisionStatus != "scheduled" {
		return 0, false, errAudioInvalidTransition
	}

	changed := liveID == ""
	newVersion := currentVersion
	if liveID != "" && liveID != revisionID {
		changed, err = audioContentDiffers(ctx, tx, revisionID, liveID)
		if err != nil {
			return 0, false, err
		}
	}
	if changed {
		newVersion = currentVersion + 1
	}

	if liveID != "" && liveID != revisionID {
		if _, err := tx.Exec(ctx,
			`UPDATE audio_story_revisions SET status='superseded',updated_at=now()
			 WHERE id=$1 AND story_id=$2`, liveID, storyID); err != nil {
			return 0, false, err
		}
	}
	if _, err := tx.Exec(ctx,
		`UPDATE audio_story_revisions SET status='published',content_version=$3,published_at=now(),
		 publish_at=NULL,approved_at=COALESCE(approved_at,now()),updated_at=now()
		 WHERE id=$1 AND story_id=$2`, revisionID, storyID, newVersion); err != nil {
		return 0, false, err
	}
	if _, err := tx.Exec(ctx,
		`UPDATE audio_stories SET status='published',live_revision_id=$2,open_revision_id=NULL,
		 content_version=$3,first_published_at=COALESCE(first_published_at,now()),unpublished_at=NULL,
		 updated_by=NULLIF($4,'')::text,updated_at=now() WHERE id=$1`, storyID, revisionID, newVersion, actor); err != nil {
		return 0, false, err
	}
	if err := writeAudioEvent(ctx, tx, storyID, revisionID, "", actor,
		"audio_story.published", revisionStatus, "published", "",
		map[string]any{
			"contentVersion":         newVersion,
			"contentChanged":         changed,
			"previousLiveRevisionId": liveID,
		}); err != nil {
		return 0, false, err
	}
	return newVersion, changed, nil
}

func audioContentDiffers(ctx context.Context, q audioReviewQueryer, revisionID, liveRevisionID string) (bool, error) {
	var tracksDiffer bool
	err := q.QueryRow(ctx,
		`WITH a AS (
		   SELECT language,COALESCE(media_asset_id::text,'') AS media_asset_id,audio_object_key,
		          duration_seconds,narrator,COALESCE(narrator_consent_date::text,'') AS consent_date,
		          COALESCE(narrator_consent_reference,'') AS consent_ref,COALESCE(size_bytes,0) AS size_bytes,
		          COALESCE(format,'') AS format,COALESCE(bitrate_kbps,0) AS bitrate,
		          COALESCE(recorded_at::text,'') AS recorded_at
		   FROM audio_story_tracks WHERE revision_id=$1
		 ), b AS (
		   SELECT language,COALESCE(media_asset_id::text,'') AS media_asset_id,audio_object_key,
		          duration_seconds,narrator,COALESCE(narrator_consent_date::text,'') AS consent_date,
		          COALESCE(narrator_consent_reference,'') AS consent_ref,COALESCE(size_bytes,0) AS size_bytes,
		          COALESCE(format,'') AS format,COALESCE(bitrate_kbps,0) AS bitrate,
		          COALESCE(recorded_at::text,'') AS recorded_at
		   FROM audio_story_tracks WHERE revision_id=$2
		 )
		 SELECT EXISTS(
		   (SELECT * FROM a EXCEPT SELECT * FROM b)
		   UNION ALL
		   (SELECT * FROM b EXCEPT SELECT * FROM a)
		 )`, revisionID, liveRevisionID).Scan(&tracksDiffer)
	if err != nil || tracksDiffer {
		return tracksDiffer, err
	}

	var transcriptsDiffer bool
	err = q.QueryRow(ctx,
		`WITH a AS (SELECT language,paragraphs FROM audio_story_transcripts WHERE revision_id=$1),
		 b AS (SELECT language,paragraphs FROM audio_story_transcripts WHERE revision_id=$2)
		 SELECT EXISTS(
		   (SELECT * FROM a EXCEPT SELECT * FROM b)
		   UNION ALL
		   (SELECT * FROM b EXCEPT SELECT * FROM a)
		 )`, revisionID, liveRevisionID).Scan(&transcriptsDiffer)
	return transcriptsDiffer, err
}

func cloneAudioRevisionContent(ctx context.Context, tx pgx.Tx, sourceID, targetID string) error {
	if _, err := tx.Exec(ctx,
		`INSERT INTO audio_story_tracks (revision_id,language,media_asset_id,audio_object_key,
		 duration_seconds,narrator,narrator_consent_date,narrator_consent_reference,size_bytes,
		 format,bitrate_kbps,recorded_at)
		 SELECT $1,language,media_asset_id,audio_object_key,duration_seconds,narrator,
		 narrator_consent_date,narrator_consent_reference,size_bytes,format,bitrate_kbps,recorded_at
		 FROM audio_story_tracks WHERE revision_id=$2`, targetID, sourceID); err != nil {
		return err
	}
	_, err := tx.Exec(ctx,
		`INSERT INTO audio_story_transcripts (revision_id,language,paragraphs,source)
		 SELECT $1,language,paragraphs,source FROM audio_story_transcripts WHERE revision_id=$2`,
		targetID, sourceID)
	return err
}

func (h *AudioStoryHandler) cancelOpenRevision(ctx context.Context, tx pgx.Tx, storyID, liveID, openID string) error {
	if openID == "" || openID == liveID {
		return nil
	}
	_, err := tx.Exec(ctx,
		`UPDATE audio_story_revisions SET status='draft',publish_at=NULL,updated_at=now()
		 WHERE id=$1 AND story_id=$2 AND status IN ('draft','in_review','approved','scheduled')`,
		openID, storyID)
	return err
}

// ---------------------------------------------------------------------------
// Scheduled publishing

// PublishDue promotes every scheduled collection and story revision whose time
// has arrived. The server runs it on a one-minute ticker; tests call it
// directly.
func (h *AudioStoryHandler) PublishDue(ctx context.Context) (AudioPublishDueResult, error) {
	var result AudioPublishDueResult
	var failures []error

	collectionRows, err := h.pool.Query(ctx,
		`SELECT id FROM audio_story_collections
		 WHERE status='scheduled' AND publish_at IS NOT NULL AND publish_at <= now()
		 ORDER BY publish_at LIMIT 100`)
	if err != nil {
		return result, err
	}
	collectionIDs := make([]string, 0)
	for collectionRows.Next() {
		var id string
		if err := collectionRows.Scan(&id); err != nil {
			collectionRows.Close()
			return result, err
		}
		collectionIDs = append(collectionIDs, id)
	}
	collectionRows.Close()
	for _, id := range collectionIDs {
		if err := h.publishCollectionDue(ctx, id); err != nil {
			slog.Error("scheduled collection publish failed", "collectionId", id, "error", err)
			failures = append(failures, err)
			continue
		}
		result.Collections++
	}

	rows, err := h.pool.Query(ctx,
		`SELECT id,story_id FROM audio_story_revisions
		 WHERE status='scheduled' AND publish_at IS NOT NULL AND publish_at <= now()
		 ORDER BY publish_at LIMIT 100`)
	if err != nil {
		return result, errors.Join(append(failures, err)...)
	}
	type dueRevision struct{ revisionID, storyID string }
	due := make([]dueRevision, 0)
	for rows.Next() {
		var item dueRevision
		if err := rows.Scan(&item.revisionID, &item.storyID); err != nil {
			rows.Close()
			return result, errors.Join(append(failures, err)...)
		}
		due = append(due, item)
	}
	rows.Close()

	for _, item := range due {
		if err := h.publishScheduledRevision(ctx, item.storyID, item.revisionID); err != nil {
			slog.Error("scheduled audio story publish failed", "storyId", item.storyID,
				"revisionId", item.revisionID, "error", err)
			failures = append(failures, err)
			continue
		}
		result.Stories++
	}
	return result, errors.Join(failures...)
}

func (h *AudioStoryHandler) publishScheduledRevision(ctx context.Context, storyID, revisionID string) error {
	pc, err := h.loadPublishCheck(ctx, storyID, revisionID)
	if err != nil {
		return err
	}
	if pc.RevisionStatus != "scheduled" {
		return nil
	}
	if gate := audioPublishGate(pc); gate != nil {
		return fmt.Errorf("publication blocked: %s", gate.Code)
	}
	tx, err := h.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	if _, _, err := h.promoteRevision(ctx, tx, storyID, revisionID, ""); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func (h *AudioStoryHandler) publishCollectionDue(ctx context.Context, collectionID string) error {
	tx, err := h.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	tag, err := tx.Exec(ctx,
		`UPDATE audio_story_collections SET status='published',publish_at=NULL,
		 first_published_at=COALESCE(first_published_at,now()),updated_at=now()
		 WHERE id=$1 AND status='scheduled'`, collectionID)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return nil
	}
	if err := writeAudioCollectionEvent(ctx, tx, collectionID, "",
		"audio_story_collection.published", "scheduled", "published", "", nil); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

// StartPublishScheduler runs PublishDue on an interval until ctx is cancelled.
func (h *AudioStoryHandler) StartPublishScheduler(ctx context.Context, interval time.Duration) {
	if interval <= 0 {
		interval = time.Minute
	}
	go func() {
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				if _, err := h.PublishDue(ctx); err != nil {
					slog.Error("audio publish scheduler failed", "error", err)
				}
				if _, err := h.ExpireRightsDue(ctx); err != nil {
					slog.Error("audio rights expiry scheduler failed", "error", err)
				}
			}
		}
	}()
}

// ExpireRightsDue unpublishes stories whose live rights have lapsed. It is safe
// to run repeatedly: only currently published stories are selected. Scheduled
// and open revisions are cancelled, and the live revision is retained for audit.
func (h *AudioStoryHandler) ExpireRightsDue(ctx context.Context) (int, error) {
	rows, err := h.pool.Query(ctx,
		`SELECT s.id, COALESCE(s.open_revision_id::text,'')
		 FROM audio_stories s
		 JOIN audio_story_revisions lr ON lr.id=s.live_revision_id
		 WHERE s.status='published'
		   AND lr.rights_expires_at IS NOT NULL
		   AND lr.rights_expires_at <= now()
		 ORDER BY lr.rights_expires_at
		 LIMIT 100`)
	if err != nil {
		return 0, err
	}
	type due struct{ storyID, openID string }
	items := make([]due, 0)
	for rows.Next() {
		var item due
		if err := rows.Scan(&item.storyID, &item.openID); err != nil {
			rows.Close()
			return 0, err
		}
		items = append(items, item)
	}
	rows.Close()

	expired := 0
	for _, item := range items {
		tx, err := h.pool.Begin(ctx)
		if err != nil {
			return expired, err
		}
		tag, err := tx.Exec(ctx,
			`UPDATE audio_stories SET status='unpublished', open_revision_id=NULL, updated_at=now()
			 WHERE id=$1 AND status='published'`, item.storyID)
		if err != nil || tag.RowsAffected() == 0 {
			tx.Rollback(ctx)
			continue
		}
		if _, err := tx.Exec(ctx,
			`UPDATE audio_story_revisions SET status='cancelled', updated_at=now()
			 WHERE story_id=$1 AND status IN ('draft','in_review','approved','scheduled')`,
			item.storyID); err != nil {
			tx.Rollback(ctx)
			return expired, err
		}
		if err := writeAudioEvent(ctx, tx, item.storyID, item.openID, "", "",
			"audio_story.rights_expired", "published", "unpublished", "Rights expired", nil); err != nil {
			tx.Rollback(ctx)
			return expired, err
		}
		if err := tx.Commit(ctx); err != nil {
			return expired, err
		}
		expired++
	}
	return expired, nil
}
