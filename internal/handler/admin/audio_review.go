package admin

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/bradleygichuru/ytci-go/internal/middleware"
)

var (
	errReviewerIneligible = errors.New("reviewer is the uploader or attester")
	errReviewerNotFound   = errors.New("reviewer not found")
)

// ---------------------------------------------------------------------------
// Review types

type audioReviewQueueItem struct {
	StoryID              string     `json:"storyId"`
	StoryTitle           string     `json:"storyTitle"`
	RevisionID           string     `json:"revisionId"`
	RiskTier             string     `json:"riskTier"`
	SubmittedAt          *time.Time `json:"submittedAt"`
	AssignedReviewerID   string     `json:"assignedReviewerId,omitempty"`
	AssignedReviewerName string     `json:"assignedReviewerName,omitempty"`
	SecondReviewerID     string     `json:"secondReviewerId,omitempty"`
	Languages            []string   `json:"languages"`
	CollectionTitle      string     `json:"collectionTitle,omitempty"`
}

type audioReviewQueueCounts struct {
	Unassigned int `json:"unassigned"`
	Mine       int `json:"mine"`
	Sensitive  int `json:"sensitive"`
	Restricted int `json:"restricted"`
}

type audioReviewItem struct {
	ID                          string     `json:"id"`
	ReviewerID                  string     `json:"reviewerId,omitempty"`
	ReviewerName                string     `json:"reviewerName"`
	ExternalReviewerName        string     `json:"externalReviewerName,omitempty"`
	ExternalReviewerAffiliation string     `json:"externalReviewerAffiliation,omitempty"`
	Decision                    string     `json:"decision"`
	RiskTier                    string     `json:"riskTier"`
	Notes                       string     `json:"notes,omitempty"`
	CreatedAt                   *time.Time `json:"createdAt"`
}

type audioReviewAssignRequest struct {
	ReviewerID       string  `json:"reviewerId"`
	SecondReviewerID *string `json:"secondReviewerId"`
	Reason           string  `json:"reason"`
}

type audioReviewRecuseRequest struct {
	Reason string `json:"reason"`
}

type audioReviewDecisionRequest struct {
	Decision                    string `json:"decision"`
	Notes                       string `json:"notes"`
	ExternalReviewerName        string `json:"externalReviewerName"`
	ExternalReviewerAffiliation string `json:"externalReviewerAffiliation"`
}

type audioReviewWithdrawRequest struct {
	Reason string `json:"reason"`
}

// ---------------------------------------------------------------------------
// Role helpers

func audioPublisher(r *http.Request) bool {
	role := middleware.RoleFromCtx(r.Context())
	return role == middleware.RoleAdmin || role == middleware.RoleSuperAdmin
}

func audioSuperAdmin(r *http.Request) bool {
	return middleware.RoleFromCtx(r.Context()) == middleware.RoleSuperAdmin
}

func audioReviewRequiredApprovals(riskTier string) int {
	if riskTier == "sensitive" {
		return 2
	}
	return 1
}

// ---------------------------------------------------------------------------
// Review queue and review history

func (h *AudioStoryHandler) ReviewQueue(w http.ResponseWriter, r *http.Request) {
	status := strings.TrimSpace(r.URL.Query().Get("status"))
	if status == "" {
		status = "all"
	}
	switch status {
	case "all", "unassigned", "mine", "sensitive", "restricted":
	default:
		audioFail(w, http.StatusBadRequest, "VALIDATION_FAILED", "invalid review queue filter")
		return
	}

	actor := middleware.UserID(r.Context())
	rows, err := h.pool.Query(r.Context(),
		`SELECT s.id,s.title,r.id,r.risk_tier,r.submitted_at,
		 COALESCE(r.assigned_reviewer_id,''),COALESCE(u.name,''),COALESCE(r.second_reviewer_id,''),
		 COALESCE((SELECT array_agg(t.language ORDER BY t.language) FROM audio_story_tracks t
		           WHERE t.revision_id=r.id),'{}'),
		 COALESCE(c.title,'')
		 FROM audio_story_revisions r
		 JOIN audio_stories s ON s.id=r.story_id
		 LEFT JOIN audio_story_collections c ON c.id=s.collection_id
		 LEFT JOIN users u ON u.id=r.assigned_reviewer_id
		 WHERE r.status='in_review'
		   AND ($1='all'
		        OR ($1='unassigned' AND r.assigned_reviewer_id IS NULL)
		        OR ($1='mine' AND (r.assigned_reviewer_id=$2 OR r.second_reviewer_id=$2))
		        OR ($1='sensitive' AND r.risk_tier='sensitive')
		        OR ($1='restricted' AND r.risk_tier='restricted'))
		 ORDER BY r.submitted_at ASC NULLS LAST, r.created_at ASC
		 LIMIT 200`, status, actor)
	if err != nil {
		audioFail(w, http.StatusInternalServerError, "INTERNAL_ERROR", "failed to load the review queue")
		return
	}
	defer rows.Close()

	items := make([]audioReviewQueueItem, 0)
	for rows.Next() {
		var item audioReviewQueueItem
		var submitted pgtype.Timestamp
		if err := rows.Scan(&item.StoryID, &item.StoryTitle, &item.RevisionID, &item.RiskTier, &submitted,
			&item.AssignedReviewerID, &item.AssignedReviewerName, &item.SecondReviewerID,
			&item.Languages, &item.CollectionTitle); err != nil {
			audioFail(w, http.StatusInternalServerError, "INTERNAL_ERROR", "failed to read the review queue")
			return
		}
		item.SubmittedAt = tsPtr(submitted)
		item.Languages = nonNilStrings(item.Languages)
		items = append(items, item)
	}
	if err := rows.Err(); err != nil {
		audioFail(w, http.StatusInternalServerError, "INTERNAL_ERROR", "failed to read the review queue")
		return
	}

	var counts audioReviewQueueCounts
	if err := h.pool.QueryRow(r.Context(),
		`SELECT
		 COUNT(*) FILTER (WHERE assigned_reviewer_id IS NULL),
		 COUNT(*) FILTER (WHERE assigned_reviewer_id=$1 OR second_reviewer_id=$1),
		 COUNT(*) FILTER (WHERE risk_tier='sensitive'),
		 COUNT(*) FILTER (WHERE risk_tier='restricted')
		 FROM audio_story_revisions WHERE status='in_review'`, actor).Scan(
		&counts.Unassigned, &counts.Mine, &counts.Sensitive, &counts.Restricted); err != nil {
		audioFail(w, http.StatusInternalServerError, "INTERNAL_ERROR", "failed to count the review queue")
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{"items": items, "counts": counts})
}

func (h *AudioStoryHandler) ListReviews(w http.ResponseWriter, r *http.Request) {
	storyID, revisionID := chi.URLParam(r, "id"), chi.URLParam(r, "revisionId")
	var riskTier string
	err := h.pool.QueryRow(r.Context(),
		`SELECT risk_tier FROM audio_story_revisions WHERE id=$1 AND story_id=$2`,
		revisionID, storyID).Scan(&riskTier)
	if err != nil {
		audioFail(w, http.StatusNotFound, "NOT_FOUND", "revision not found")
		return
	}

	rows, err := h.pool.Query(r.Context(),
		`SELECT rv.id,COALESCE(rv.reviewer_id,''),COALESCE(u.name,''),COALESCE(rv.external_reviewer_name,''),
		 COALESCE(rv.external_reviewer_affiliation,''),rv.decision,rv.risk_tier,COALESCE(rv.notes,''),rv.created_at
		 FROM audio_story_reviews rv LEFT JOIN users u ON u.id=rv.reviewer_id
		 WHERE rv.revision_id=$1 ORDER BY rv.created_at ASC`, revisionID)
	if err != nil {
		audioFail(w, http.StatusInternalServerError, "INTERNAL_ERROR", "failed to load reviews")
		return
	}
	defer rows.Close()

	items := make([]audioReviewItem, 0)
	for rows.Next() {
		var item audioReviewItem
		var created pgtype.Timestamp
		if err := rows.Scan(&item.ID, &item.ReviewerID, &item.ReviewerName, &item.ExternalReviewerName,
			&item.ExternalReviewerAffiliation, &item.Decision, &item.RiskTier, &item.Notes, &created); err != nil {
			audioFail(w, http.StatusInternalServerError, "INTERNAL_ERROR", "failed to read a review")
			return
		}
		if item.ReviewerName == "" {
			item.ReviewerName = item.ExternalReviewerName
		}
		item.CreatedAt = tsPtr(created)
		items = append(items, item)
	}
	if err := rows.Err(); err != nil {
		audioFail(w, http.StatusInternalServerError, "INTERNAL_ERROR", "failed to read reviews")
		return
	}

	approvals, err := h.approvalCount(r.Context(), revisionID)
	if err != nil {
		audioFail(w, http.StatusInternalServerError, "INTERNAL_ERROR", "failed to count approvals")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"items":             items,
		"approvals":         approvals,
		"requiredApprovals": audioReviewRequiredApprovals(riskTier),
	})
}

// ---------------------------------------------------------------------------
// Assignment and recusal

func (h *AudioStoryHandler) AssignReviewer(w http.ResponseWriter, r *http.Request) {
	if !audioPublisher(r) {
		audioFail(w, http.StatusForbidden, "FORBIDDEN", "administrator access required")
		return
	}
	storyID, revisionID := chi.URLParam(r, "id"), chi.URLParam(r, "revisionId")
	var req audioReviewAssignRequest
	if !decodeAudioBody(w, r, &req) {
		return
	}
	reviewerID := strings.TrimSpace(req.ReviewerID)
	if reviewerID == "" {
		audioFail(w, http.StatusBadRequest, "VALIDATION_FAILED", "reviewerId is required")
		return
	}
	secondReviewerID := ""
	if req.SecondReviewerID != nil {
		secondReviewerID = strings.TrimSpace(*req.SecondReviewerID)
	}
	if secondReviewerID != "" && secondReviewerID == reviewerID {
		audioFail(w, http.StatusBadRequest, "VALIDATION_FAILED", "the two reviewers must be different people")
		return
	}

	ctx := r.Context()
	tx, err := h.pool.Begin(ctx)
	if err != nil {
		audioFail(w, http.StatusInternalServerError, "INTERNAL_ERROR", "failed to start transaction")
		return
	}
	defer tx.Rollback(ctx)

	var status, createdBy, attestedBy string
	err = tx.QueryRow(ctx,
		`SELECT status,COALESCE(created_by,''),COALESCE(attested_by,'')
		 FROM audio_story_revisions WHERE id=$1 AND story_id=$2 FOR UPDATE`,
		revisionID, storyID).Scan(&status, &createdBy, &attestedBy)
	if err != nil {
		audioFail(w, http.StatusNotFound, "NOT_FOUND", "revision not found")
		return
	}
	if status != "in_review" {
		audioFail(w, http.StatusConflict, "INVALID_TRANSITION", "revision is not in review")
		return
	}
	if err := audioCheckReviewerEligible(ctx, tx, reviewerID, createdBy, attestedBy); err != nil {
		if errors.Is(err, errReviewerNotFound) {
			audioFail(w, http.StatusBadRequest, "REVIEWER_NOT_FOUND", "the reviewer does not exist")
			return
		}
		audioFail(w, http.StatusForbidden, "SEPARATION_OF_DUTIES", "the reviewer cannot be the uploader or attester")
		return
	}
	if secondReviewerID != "" {
		if err := audioCheckReviewerEligible(ctx, tx, secondReviewerID, createdBy, attestedBy); err != nil {
			if errors.Is(err, errReviewerNotFound) {
				audioFail(w, http.StatusBadRequest, "REVIEWER_NOT_FOUND", "the second reviewer does not exist")
				return
			}
			audioFail(w, http.StatusForbidden, "SEPARATION_OF_DUTIES", "the second reviewer cannot be the uploader or attester")
			return
		}
	}

	actor := middleware.UserID(ctx)
	if _, err := tx.Exec(ctx,
		`UPDATE audio_story_revisions SET assigned_reviewer_id=$3,second_reviewer_id=$4,updated_at=now()
		 WHERE id=$1 AND story_id=$2`,
		revisionID, storyID, reviewerID, nullableString(secondReviewerID)); err != nil {
		audioFail(w, http.StatusBadRequest, "VALIDATION_FAILED", "the reviewer could not be assigned")
		return
	}
	if err := writeAudioEvent(ctx, tx, storyID, revisionID, "", actor,
		"audio_story.reviewer_assigned", "in_review", "in_review", req.Reason,
		map[string]string{"reviewerId": reviewerID, "secondReviewerId": secondReviewerID}); err != nil {
		audioFail(w, http.StatusInternalServerError, "INTERNAL_ERROR", "failed to write audit event")
		return
	}
	if err := tx.Commit(ctx); err != nil {
		audioFail(w, http.StatusInternalServerError, "INTERNAL_ERROR", "failed to commit assignment")
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{
		"status": "assigned", "reviewerId": reviewerID, "secondReviewerId": secondReviewerID,
	})
}

func audioCheckReviewerEligible(ctx context.Context, tx pgx.Tx, reviewerID, createdBy, attestedBy string) error {
	if reviewerID == "" {
		return nil
	}
	if reviewerID == createdBy || reviewerID == attestedBy {
		return errReviewerIneligible
	}
	var exists bool
	if err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM users WHERE id=$1)`, reviewerID).Scan(&exists); err != nil {
		return err
	}
	if !exists {
		return errReviewerNotFound
	}
	return nil
}

func (h *AudioStoryHandler) RecuseReviewer(w http.ResponseWriter, r *http.Request) {
	if !audioPublisher(r) {
		audioFail(w, http.StatusForbidden, "FORBIDDEN", "administrator access required")
		return
	}
	storyID, revisionID := chi.URLParam(r, "id"), chi.URLParam(r, "revisionId")
	var req audioReviewRecuseRequest
	if !decodeAudioBody(w, r, &req) {
		return
	}
	if strings.TrimSpace(req.Reason) == "" {
		audioFail(w, http.StatusBadRequest, "REASON_REQUIRED", "a reason is required to recuse")
		return
	}

	ctx := r.Context()
	tx, err := h.pool.Begin(ctx)
	if err != nil {
		audioFail(w, http.StatusInternalServerError, "INTERNAL_ERROR", "failed to start transaction")
		return
	}
	defer tx.Rollback(ctx)

	actor := middleware.UserID(ctx)
	var status, assigned, second string
	err = tx.QueryRow(ctx,
		`SELECT status,COALESCE(assigned_reviewer_id,''),COALESCE(second_reviewer_id,'')
		 FROM audio_story_revisions WHERE id=$1 AND story_id=$2 FOR UPDATE`,
		revisionID, storyID).Scan(&status, &assigned, &second)
	if err != nil {
		audioFail(w, http.StatusNotFound, "NOT_FOUND", "revision not found")
		return
	}
	if status != "in_review" {
		audioFail(w, http.StatusConflict, "INVALID_TRANSITION", "revision is not in review")
		return
	}
	if actor != assigned && actor != second {
		audioFail(w, http.StatusConflict, "NOT_ASSIGNED", "you are not assigned to this revision")
		return
	}
	if _, err := tx.Exec(ctx,
		`UPDATE audio_story_revisions SET
		 assigned_reviewer_id=CASE WHEN assigned_reviewer_id=$3 THEN NULL ELSE assigned_reviewer_id END,
		 second_reviewer_id=CASE WHEN second_reviewer_id=$3 THEN NULL ELSE second_reviewer_id END,
		 updated_at=now()
		 WHERE id=$1 AND story_id=$2`, revisionID, storyID, actor); err != nil {
		audioFail(w, http.StatusInternalServerError, "INTERNAL_ERROR", "failed to recuse the reviewer")
		return
	}
	if err := writeAudioEvent(ctx, tx, storyID, revisionID, "", actor,
		"audio_story.reviewer_recused", "in_review", "in_review", req.Reason, nil); err != nil {
		audioFail(w, http.StatusInternalServerError, "INTERNAL_ERROR", "failed to write audit event")
		return
	}
	if err := tx.Commit(ctx); err != nil {
		audioFail(w, http.StatusInternalServerError, "INTERNAL_ERROR", "failed to commit recusal")
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "recused"})
}

// ---------------------------------------------------------------------------
// Decisions

func (h *AudioStoryHandler) ReviewDecision(w http.ResponseWriter, r *http.Request) {
	if !audioPublisher(r) {
		audioFail(w, http.StatusForbidden, "FORBIDDEN", "administrator access required")
		return
	}
	storyID, revisionID := chi.URLParam(r, "id"), chi.URLParam(r, "revisionId")
	var req audioReviewDecisionRequest
	if !decodeAudioBody(w, r, &req) {
		return
	}
	switch req.Decision {
	case "approve", "request_changes", "reject":
	default:
		audioFail(w, http.StatusBadRequest, "VALIDATION_FAILED", "decision must be approve, request_changes or reject")
		return
	}
	externalName := strings.TrimSpace(req.ExternalReviewerName)
	if req.Decision != "approve" && strings.TrimSpace(req.Notes) == "" {
		audioFail(w, http.StatusBadRequest, "VALIDATION_FAILED", "notes are required when requesting changes or rejecting")
		return
	}

	ctx := r.Context()
	tx, err := h.pool.Begin(ctx)
	if err != nil {
		audioFail(w, http.StatusInternalServerError, "INTERNAL_ERROR", "failed to start transaction")
		return
	}
	defer tx.Rollback(ctx)

	var status, riskTier, createdBy, attestedBy string
	err = tx.QueryRow(ctx,
		`SELECT status,risk_tier,COALESCE(created_by,''),COALESCE(attested_by,'')
		 FROM audio_story_revisions WHERE id=$1 AND story_id=$2 FOR UPDATE`,
		revisionID, storyID).Scan(&status, &riskTier, &createdBy, &attestedBy)
	if err != nil {
		audioFail(w, http.StatusNotFound, "NOT_FOUND", "revision not found")
		return
	}

	actor := middleware.UserID(ctx)
	duplicate, err := audioReviewExists(ctx, tx, revisionID, actor, externalName)
	if err != nil {
		audioFail(w, http.StatusInternalServerError, "INTERNAL_ERROR", "failed to check existing reviews")
		return
	}
	if duplicate {
		audioFail(w, http.StatusConflict, "REVIEW_CONFLICT", "this reviewer has already decided on this revision")
		return
	}
	if status != "in_review" {
		audioFail(w, http.StatusConflict, "INVALID_TRANSITION", "revision is not in review")
		return
	}
	if externalName == "" && (actor == createdBy || actor == attestedBy) {
		audioFail(w, http.StatusForbidden, "SEPARATION_OF_DUTIES", "the reviewer cannot be the uploader or attester")
		return
	}
	if req.Decision == "approve" && riskTier == "restricted" {
		audioFail(w, http.StatusForbidden, "RESTRICTED_REVIEW", "restricted stories cannot be approved")
		return
	}

	reviewerID := actor
	var externalArg any
	if externalName != "" {
		reviewerID = ""
		externalArg = externalName
	}
	affiliation := nullableString(strings.TrimSpace(req.ExternalReviewerAffiliation))
	if _, err := tx.Exec(ctx,
		`INSERT INTO audio_story_reviews
		 (revision_id,reviewer_id,external_reviewer_name,external_reviewer_affiliation,decision,risk_tier,notes)
		 VALUES ($1,NULLIF($2,''),$3,$4,$5,$6,NULLIF($7,''))`,
		revisionID, reviewerID, externalArg, affiliation, req.Decision, riskTier, req.Notes); err != nil {
		audioFail(w, http.StatusInternalServerError, "INTERNAL_ERROR", "failed to record the review")
		return
	}

	approvals, err := audioApprovalCount(ctx, tx, revisionID)
	if err != nil {
		audioFail(w, http.StatusInternalServerError, "INTERNAL_ERROR", "failed to count approvals")
		return
	}
	required := audioReviewRequiredApprovals(riskTier)
	toStatus := "in_review"
	eventAction := "audio_story.review_changes_requested"
	needsSecondReviewer := false

	switch req.Decision {
	case "approve":
		if approvals >= required {
			toStatus = "approved"
			eventAction = "audio_story.review_approved"
			if _, err := tx.Exec(ctx,
				`UPDATE audio_story_revisions SET status='approved',approved_at=now(),updated_at=now()
				 WHERE id=$1 AND story_id=$2`, revisionID, storyID); err != nil {
				audioFail(w, http.StatusInternalServerError, "INTERNAL_ERROR", "failed to approve the revision")
				return
			}
			if _, err := tx.Exec(ctx,
				`UPDATE audio_stories SET status=CASE WHEN status='published' THEN status ELSE 'in_review' END,
				 updated_by=$2,updated_at=now() WHERE id=$1`, storyID, actor); err != nil {
				audioFail(w, http.StatusInternalServerError, "INTERNAL_ERROR", "failed to update the story")
				return
			}
		} else {
			eventAction = "audio_story.review_approved"
			needsSecondReviewer = riskTier == "sensitive"
			if _, err := tx.Exec(ctx,
				`UPDATE audio_story_revisions SET updated_at=now() WHERE id=$1 AND story_id=$2`,
				revisionID, storyID); err != nil {
				audioFail(w, http.StatusInternalServerError, "INTERNAL_ERROR", "failed to record the approval")
				return
			}
		}
	case "request_changes":
		toStatus = "draft"
		eventAction = "audio_story.review_changes_requested"
		if _, err := tx.Exec(ctx,
			`UPDATE audio_story_revisions SET status='draft',approved_at=NULL,updated_at=now()
			 WHERE id=$1 AND story_id=$2`, revisionID, storyID); err != nil {
			audioFail(w, http.StatusInternalServerError, "INTERNAL_ERROR", "failed to return the revision to draft")
			return
		}
		if _, err := tx.Exec(ctx,
			`UPDATE audio_stories SET status=CASE WHEN status='published' THEN status ELSE 'draft' END,
			 updated_by=$2,updated_at=now() WHERE id=$1`, storyID, actor); err != nil {
			audioFail(w, http.StatusInternalServerError, "INTERNAL_ERROR", "failed to update the story")
			return
		}
	case "reject":
		toStatus = "draft"
		eventAction = "audio_story.review_rejected"
		if _, err := tx.Exec(ctx,
			`UPDATE audio_story_revisions SET status='draft',approved_at=NULL,updated_at=now()
			 WHERE id=$1 AND story_id=$2`, revisionID, storyID); err != nil {
			audioFail(w, http.StatusInternalServerError, "INTERNAL_ERROR", "failed to reject the revision")
			return
		}
		if _, err := tx.Exec(ctx,
			`UPDATE audio_stories SET status=CASE WHEN status='published' THEN status ELSE 'draft' END,
			 updated_by=$2,updated_at=now() WHERE id=$1`, storyID, actor); err != nil {
			audioFail(w, http.StatusInternalServerError, "INTERNAL_ERROR", "failed to update the story")
			return
		}
	}

	metadata := map[string]any{
		"decision":         req.Decision,
		"reviewerId":       reviewerID,
		"externalReviewer": externalName,
		"notes":            req.Notes,
		"approvals":        approvals,
	}
	if err := writeAudioEvent(ctx, tx, storyID, revisionID, "", actor,
		eventAction, "in_review", toStatus, "", metadata); err != nil {
		audioFail(w, http.StatusInternalServerError, "INTERNAL_ERROR", "failed to write audit event")
		return
	}
	if err := tx.Commit(ctx); err != nil {
		audioFail(w, http.StatusInternalServerError, "INTERNAL_ERROR", "failed to commit review")
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"status":              toStatus,
		"decision":            req.Decision,
		"approvals":           approvals,
		"requiredApprovals":   required,
		"needsSecondReviewer": needsSecondReviewer,
	})
}

func (h *AudioStoryHandler) WithdrawRevision(w http.ResponseWriter, r *http.Request) {
	if !audioWriter(r) {
		audioFail(w, http.StatusForbidden, "FORBIDDEN", "audio writer access required")
		return
	}
	storyID, revisionID := chi.URLParam(r, "id"), chi.URLParam(r, "revisionId")
	var req audioReviewWithdrawRequest
	if !decodeAudioBody(w, r, &req) {
		return
	}
	if strings.TrimSpace(req.Reason) == "" {
		audioFail(w, http.StatusBadRequest, "REASON_REQUIRED", "a reason is required to withdraw a revision")
		return
	}

	ctx := r.Context()
	tx, err := h.pool.Begin(ctx)
	if err != nil {
		audioFail(w, http.StatusInternalServerError, "INTERNAL_ERROR", "failed to start transaction")
		return
	}
	defer tx.Rollback(ctx)

	var status string
	err = tx.QueryRow(ctx,
		`SELECT status FROM audio_story_revisions WHERE id=$1 AND story_id=$2 FOR UPDATE`,
		revisionID, storyID).Scan(&status)
	if err != nil {
		audioFail(w, http.StatusNotFound, "NOT_FOUND", "revision not found")
		return
	}
	if status != "in_review" {
		audioFail(w, http.StatusConflict, "INVALID_TRANSITION", "revision is not in review")
		return
	}

	actor := middleware.UserID(ctx)
	if _, err := tx.Exec(ctx,
		`UPDATE audio_story_revisions SET status='draft',assigned_reviewer_id=NULL,second_reviewer_id=NULL,
		 publish_at=NULL,approved_at=NULL,updated_at=now()
		 WHERE id=$1 AND story_id=$2`, revisionID, storyID); err != nil {
		audioFail(w, http.StatusInternalServerError, "INTERNAL_ERROR", "failed to withdraw the revision")
		return
	}
	if _, err := tx.Exec(ctx,
		`UPDATE audio_stories SET status=CASE WHEN status='published' THEN status ELSE 'draft' END,
		 updated_by=$2,updated_at=now() WHERE id=$1`, storyID, actor); err != nil {
		audioFail(w, http.StatusInternalServerError, "INTERNAL_ERROR", "failed to update the story")
		return
	}
	if err := writeAudioEvent(ctx, tx, storyID, revisionID, "", actor,
		"audio_story.revision_withdrawn", "in_review", "draft", req.Reason, nil); err != nil {
		audioFail(w, http.StatusInternalServerError, "INTERNAL_ERROR", "failed to write audit event")
		return
	}
	if err := tx.Commit(ctx); err != nil {
		audioFail(w, http.StatusInternalServerError, "INTERNAL_ERROR", "failed to commit withdrawal")
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "draft"})
}

// ---------------------------------------------------------------------------
// Review queries

func (h *AudioStoryHandler) approvalCount(ctx context.Context, revisionID string) (int, error) {
	return audioApprovalCount(ctx, h.pool, revisionID)
}

type audioReviewQueryer interface {
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
}

func audioApprovalCount(ctx context.Context, q audioReviewQueryer, revisionID string) (int, error) {
	var count int
	err := q.QueryRow(ctx,
		`SELECT COUNT(DISTINCT COALESCE(reviewer_id,lower(btrim(external_reviewer_name))))
		 FROM audio_story_reviews WHERE revision_id=$1 AND decision='approve'`, revisionID).Scan(&count)
	return count, err
}

func audioReviewExists(ctx context.Context, q audioReviewQueryer, revisionID, actor, externalName string) (bool, error) {
	var exists bool
	err := q.QueryRow(ctx,
		`SELECT EXISTS(
		   SELECT 1 FROM audio_story_reviews
		   WHERE revision_id=$1
		     AND (($2<>'' AND reviewer_id=$2)
		          OR ($3<>'' AND lower(btrim(external_reviewer_name))=lower(btrim($3)))))`,
		revisionID, actor, externalName).Scan(&exists)
	return exists, err
}

func nullableString(value string) any {
	if value == "" {
		return nil
	}
	return value
}
