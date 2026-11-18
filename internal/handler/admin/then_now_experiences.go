package admin

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/bradleygichuru/ytci-go/internal/handler"
	"github.com/bradleygichuru/ytci-go/internal/middleware"
	"github.com/bradleygichuru/ytci-go/internal/thennow"
)

type experienceRecord struct {
	ID        string
	SiteID    string
	SiteName  string
	Title     string
	Status    string
	Live      int
	Open      int
	UpdatedAt pgtype.Timestamp
}

type revisionDTO struct {
	ID                    string          `json:"id"`
	RevisionNumber        int             `json:"revisionNumber"`
	Status                string          `json:"status"`
	Snapshot              json.RawMessage `json:"snapshot"`
	ReviewNotes           json.RawMessage `json:"reviewNotes"`
	ReviewerID            string          `json:"reviewerId"`
	Reviewer              string          `json:"reviewer"`
	SubmittedByID         string          `json:"submittedById"`
	SubmittedBy           string          `json:"submittedBy"`
	UploadedByID          string          `json:"uploadedById"`
	UploadedBy            string          `json:"uploadedBy"`
	SubmittedAt           string          `json:"submittedAt"`
	LastDecision          string          `json:"lastDecision"`
	SecondReviewer        json.RawMessage `json:"secondReviewer"`
	SecondReviewerApprove bool            `json:"secondReviewerApproved"`
	ClientApproval        json.RawMessage `json:"clientApproval"`
	ScheduledAt           string          `json:"scheduledAt"`
	ScheduleNote          string          `json:"scheduleNote"`
	PublishedAt           string          `json:"publishedAt"`
}

type experienceDetail struct {
	ID                 string            `json:"id"`
	SiteID             string            `json:"siteId"`
	SiteName           string            `json:"siteName"`
	Title              string            `json:"title"`
	Status             string            `json:"status"`
	LiveRevisionNumber int               `json:"liveRevisionNumber"`
	OpenRevisionNumber int               `json:"openRevisionNumber"`
	OpenRevision       *revisionDTO      `json:"openRevision"`
	LiveRevision       *revisionDTO      `json:"liveRevision"`
	Site               thennow.Site      `json:"site"`
	Readiness          thennow.Readiness `json:"readiness"`
}

func formatTS(value pgtype.Timestamp) string {
	if !value.Valid {
		return ""
	}
	return value.Time.UTC().Format(time.RFC3339)
}

// formatTSNano keeps sub-second precision so updated_at can be echoed back as
// an exact compare-and-swap precondition on site PATCH.
func formatTSNano(value pgtype.Timestamp) string {
	if !value.Valid {
		return ""
	}
	return value.Time.UTC().Format(time.RFC3339Nano)
}

func (h *ThenNowHandler) loadExperience(ctx context.Context, id string) (experienceRecord, error) {
	var exp experienceRecord
	err := h.pool.QueryRow(ctx, `
		SELECT e.id::text, e.site_id::text, s.name, e.title, e.status,
		       e.live_revision_number, e.open_revision_number, e.updated_at
		FROM then_now_experiences e
		JOIN heritage_sites s ON s.id = e.site_id
		WHERE e.id = $1`, id).Scan(
		&exp.ID, &exp.SiteID, &exp.SiteName, &exp.Title, &exp.Status,
		&exp.Live, &exp.Open, &exp.UpdatedAt,
	)
	return exp, err
}

func (h *ThenNowHandler) loadRevision(ctx context.Context, experienceID string, number int) (*revisionDTO, error) {
	var dto revisionDTO
	var snapshot, notes, secondReviewer, clientApproval []byte
	var submittedAt, scheduledAt, publishedAt pgtype.Timestamp
	err := h.pool.QueryRow(ctx, `
		SELECT r.id::text, r.revision_number, r.status, r.snapshot, r.review_notes,
		       COALESCE(r.reviewer_id, ''), COALESCE(ru.name, ru.email, ''),
		       COALESCE(r.submitted_by, ''), COALESCE(su.name, su.email, ''),
		       COALESCE(r.created_by, ''), COALESCE(cu.name, cu.email, ''),
		       r.submitted_at, COALESCE(r.last_decision, ''),
		       r.second_reviewer, r.second_reviewer_approved, r.client_approval,
		       r.scheduled_at, COALESCE(r.schedule_note, ''), r.published_at
		FROM then_now_revisions r
		LEFT JOIN users ru ON ru.id = r.reviewer_id
		LEFT JOIN users su ON su.id = r.submitted_by
		LEFT JOIN users cu ON cu.id = r.created_by
		WHERE r.experience_id = $1 AND r.revision_number = $2`,
		experienceID, number).Scan(
		&dto.ID, &dto.RevisionNumber, &dto.Status, &snapshot, &notes,
		&dto.ReviewerID, &dto.Reviewer, &dto.SubmittedByID, &dto.SubmittedBy,
		&dto.UploadedByID, &dto.UploadedBy, &submittedAt, &dto.LastDecision,
		&secondReviewer, &dto.SecondReviewerApprove, &clientApproval,
		&scheduledAt, &dto.ScheduleNote, &publishedAt,
	)
	if err != nil {
		return nil, err
	}
	dto.Snapshot = rawJSONOr(snapshot, "{}")
	dto.ReviewNotes = rawJSONOr(notes, "[]")
	dto.SecondReviewer = rawJSONOr(secondReviewer, "null")
	dto.ClientApproval = rawJSONOr(clientApproval, "null")
	dto.SubmittedAt = formatTS(submittedAt)
	dto.ScheduledAt = formatTS(scheduledAt)
	dto.PublishedAt = formatTS(publishedAt)
	return &dto, nil
}

func rawJSONOr(value []byte, fallback string) json.RawMessage {
	if len(value) == 0 {
		return json.RawMessage(fallback)
	}
	return json.RawMessage(value)
}

func snapshotOf(dto *revisionDTO) thennow.Snapshot {
	if dto == nil {
		return thennow.Snapshot{}
	}
	var snapshot thennow.Snapshot
	if err := json.Unmarshal(dto.Snapshot, &snapshot); err != nil {
		return thennow.Snapshot{}
	}
	return thennow.NormalizeSnapshot(snapshot)
}

func (h *ThenNowHandler) buildExperienceDetail(ctx context.Context, id string) (*experienceDetail, error) {
	exp, err := h.loadExperience(ctx, id)
	if err != nil {
		return nil, err
	}
	site, err := h.loadSite(ctx, exp.SiteID)
	if err != nil {
		return nil, err
	}
	open, err := h.loadRevision(ctx, exp.ID, exp.Open)
	if err != nil {
		return nil, err
	}
	var live *revisionDTO
	if exp.Live > 0 {
		live, _ = h.loadRevision(ctx, exp.ID, exp.Live)
	}
	readiness := thennow.ExperienceReadiness(snapshotOf(open), open.Status, exp.Live, exp.Open)
	return &experienceDetail{
		ID:                 exp.ID,
		SiteID:             exp.SiteID,
		SiteName:           exp.SiteName,
		Title:              exp.Title,
		Status:             exp.Status,
		LiveRevisionNumber: exp.Live,
		OpenRevisionNumber: exp.Open,
		OpenRevision:       open,
		LiveRevision:       live,
		Site:               site,
		Readiness:          readiness,
	}, nil
}

func validateSnapshot(snapshot thennow.Snapshot) string {
	if strings.TrimSpace(snapshot.Title) == "" {
		return "title is required"
	}
	if snapshot.Duration < 30 || snapshot.Duration > 180 {
		return "duration must be between 30 and 180 seconds"
	}
	if snapshot.PrimaryMode != "camera_video" && snapshot.PrimaryMode != "video_only" {
		return "primaryMode must be camera_video or video_only"
	}
	if !validSensitivity(snapshot.Sensitivity) {
		return "sensitivity is required"
	}
	if len(snapshot.TextCards) > 3 {
		return "three text cards is the v1 maximum"
	}
	return ""
}

type experienceCreateRequest struct {
	SiteID   string            `json:"siteId"`
	Title    string            `json:"title"`
	Snapshot *thennow.Snapshot `json:"snapshot"`
}

type experienceUpdateRequest struct {
	Title    *string           `json:"title"`
	Snapshot *thennow.Snapshot `json:"snapshot"`
}

func (h *ThenNowHandler) ListExperiences(w http.ResponseWriter, r *http.Request) {
	if !middleware.CanThenNowRead(middleware.RoleFromCtx(r.Context())) {
		handler.WriteError(w, http.StatusForbidden, "FORBIDDEN", "Then & Now access required")
		return
	}
	siteID := r.URL.Query().Get("siteId")
	status := r.URL.Query().Get("status")
	mode := r.URL.Query().Get("mode")
	review := r.URL.Query().Get("review")
	language := r.URL.Query().Get("language")
	search := strings.TrimSpace(r.URL.Query().Get("q"))

	rows, err := h.pool.Query(r.Context(), `
		SELECT e.id::text, e.site_id::text, s.name, e.title, e.status,
		       e.live_revision_number, e.open_revision_number,
		       COALESCE(r.status, ''), COALESCE(r.snapshot->>'primaryMode', 'video_only'),
		       COALESCE(r.snapshot->'languages', '[]'::jsonb),
		       COALESCE(r.snapshot->>'rightsExpiry', ''),
		       COALESCE(r.snapshot->>'sensitivity', 'standard'), e.updated_at
		FROM then_now_experiences e
		JOIN heritage_sites s ON s.id = e.site_id
		LEFT JOIN then_now_revisions r ON r.experience_id = e.id AND r.revision_number = e.open_revision_number
		WHERE ($1 = '' OR e.site_id::text = $1)
		  AND ($2 = '' OR e.status = $2)
		  AND ($3 = '' OR COALESCE(r.snapshot->>'primaryMode', '') = $3)
		  AND ($4 = '' OR r.status = $4)
		  AND ($5 = '' OR r.snapshot->'languages' ? $5)
		  AND ($6 = '' OR e.title ILIKE '%' || $6 || '%')
		ORDER BY e.updated_at DESC`, siteID, status, mode, review, language, search)
	if err != nil {
		handler.WriteServerError(w, r, "INTERNAL_ERROR", "failed to list experiences", err)
		return
	}
	defer rows.Close()
	items := make([]map[string]any, 0)
	for rows.Next() {
		var id, site, siteName, title, statusValue, openStatus, modeValue, rights, sensitivity string
		var live, open int
		var languages []byte
		var updated pgtype.Timestamp
		if err := rows.Scan(&id, &site, &siteName, &title, &statusValue, &live, &open, &openStatus, &modeValue, &languages, &rights, &sensitivity, &updated); err != nil {
			handler.WriteServerError(w, r, "INTERNAL_ERROR", "failed to read experience", err)
			return
		}
		items = append(items, map[string]any{
			"id": id, "siteId": site, "siteName": siteName, "title": title,
			"status": statusValue, "liveRevisionNumber": live, "openRevisionNumber": open,
			"openStatus": openStatus, "primaryMode": modeValue,
			"languages": json.RawMessage(func() []byte {
				if len(languages) == 0 {
					return []byte("[]")
				}
				return languages
			}()),
			"rightsExpiry": rights, "sensitivity": sensitivity, "updatedAt": formatTS(updated),
		})
	}
	writeThenNowJSON(w, http.StatusOK, map[string]any{"items": items})
}

func (h *ThenNowHandler) GetExperience(w http.ResponseWriter, r *http.Request) {
	if !middleware.CanThenNowRead(middleware.RoleFromCtx(r.Context())) {
		handler.WriteError(w, http.StatusForbidden, "FORBIDDEN", "Then & Now access required")
		return
	}
	detail, err := h.buildExperienceDetail(r.Context(), chi.URLParam(r, "id"))
	if err != nil {
		handler.WriteError(w, http.StatusNotFound, "NOT_FOUND", "experience not found")
		return
	}
	writeThenNowJSON(w, http.StatusOK, detail)
}

func (h *ThenNowHandler) CreateExperience(w http.ResponseWriter, r *http.Request) {
	if !middleware.CanThenNowAuthor(middleware.RoleFromCtx(r.Context())) {
		handler.WriteError(w, http.StatusForbidden, "FORBIDDEN", "authoring access required")
		return
	}
	var req experienceCreateRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		handler.WriteError(w, http.StatusBadRequest, "INVALID_BODY", "invalid request body")
		return
	}
	if strings.TrimSpace(req.SiteID) == "" || strings.TrimSpace(req.Title) == "" {
		handler.WriteError(w, http.StatusBadRequest, "VALIDATION_ERROR", "siteId and title are required")
		return
	}
	site, err := h.loadSite(r.Context(), req.SiteID)
	if err != nil {
		handler.WriteError(w, http.StatusNotFound, "NOT_FOUND", "heritage site not found")
		return
	}
	snapshot := thennow.Snapshot{}
	if req.Snapshot != nil {
		snapshot = *req.Snapshot
	}
	snapshot.Title = strings.TrimSpace(req.Title)
	if snapshot.PrimaryMode == "" {
		snapshot.PrimaryMode = "video_only"
	}
	if snapshot.Sensitivity == "" {
		snapshot.Sensitivity = site.Sensitivity
	}
	if snapshot.Duration == 0 {
		snapshot.Duration = 77
	}
	snapshot = thennow.NormalizeSnapshot(snapshot)
	if message := validateSnapshot(snapshot); message != "" {
		handler.WriteError(w, http.StatusBadRequest, "VALIDATION_ERROR", message)
		return
	}
	raw, _ := json.Marshal(snapshot)
	actor := middleware.UserID(r.Context())
	tx, err := h.pool.Begin(r.Context())
	if err != nil {
		handler.WriteServerError(w, r, "INTERNAL_ERROR", "failed to start transaction", err)
		return
	}
	defer tx.Rollback(r.Context())
	var id string
	if err := tx.QueryRow(r.Context(), `
		INSERT INTO then_now_experiences (site_id, title, status, live_revision_number, open_revision_number, created_by, updated_by)
		VALUES ($1, $2, 'draft', 0, 1, $3, $3) RETURNING id::text`,
		req.SiteID, strings.TrimSpace(req.Title), actor).Scan(&id); err != nil {
		handler.WriteServerError(w, r, "INTERNAL_ERROR", "failed to create experience", err)
		return
	}
	if _, err := tx.Exec(r.Context(), `
		INSERT INTO then_now_revisions (experience_id, revision_number, status, snapshot, created_by, updated_by)
		VALUES ($1, 1, 'draft', $2, $3, $3)`, id, raw, actor); err != nil {
		handler.WriteServerError(w, r, "INTERNAL_ERROR", "failed to create revision", err)
		return
	}
	if err := tx.Commit(r.Context()); err != nil {
		handler.WriteServerError(w, r, "INTERNAL_ERROR", "failed to commit experience", err)
		return
	}
	h.audit(r.Context(), "experience", id, "draft", actor, "Experience draft created", nil)
	detail, err := h.buildExperienceDetail(r.Context(), id)
	if err != nil {
		handler.WriteServerError(w, r, "INTERNAL_ERROR", "failed to load experience", err)
		return
	}
	writeThenNowJSON(w, http.StatusCreated, detail)
}

func (h *ThenNowHandler) UpdateExperience(w http.ResponseWriter, r *http.Request) {
	if !middleware.CanThenNowAuthor(middleware.RoleFromCtx(r.Context())) {
		handler.WriteError(w, http.StatusForbidden, "FORBIDDEN", "authoring access required")
		return
	}
	detail, err := h.buildExperienceDetail(r.Context(), chi.URLParam(r, "id"))
	if err != nil {
		handler.WriteError(w, http.StatusNotFound, "NOT_FOUND", "experience not found")
		return
	}
	if detail.OpenRevision == nil || detail.OpenRevision.Status != "draft" {
		handler.WriteError(w, http.StatusConflict, "FROZEN", "only a draft revision can be edited")
		return
	}
	var req experienceUpdateRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		handler.WriteError(w, http.StatusBadRequest, "INVALID_BODY", "invalid request body")
		return
	}
	snapshot := snapshotOf(detail.OpenRevision)
	if req.Snapshot != nil {
		snapshot = *req.Snapshot
	}
	if req.Title != nil {
		snapshot.Title = strings.TrimSpace(*req.Title)
	}
	snapshot = thennow.NormalizeSnapshot(snapshot)
	if message := validateSnapshot(snapshot); message != "" {
		handler.WriteError(w, http.StatusBadRequest, "VALIDATION_ERROR", message)
		return
	}
	raw, _ := json.Marshal(snapshot)
	actor := middleware.UserID(r.Context())
	tx, err := h.pool.Begin(r.Context())
	if err != nil {
		handler.WriteServerError(w, r, "INTERNAL_ERROR", "failed to start transaction", err)
		return
	}
	defer tx.Rollback(r.Context())
	if _, err := tx.Exec(r.Context(), `
		UPDATE then_now_revisions SET snapshot = $3, updated_at = now(), updated_by = $4
		WHERE experience_id = $1 AND revision_number = $2`,
		detail.ID, detail.OpenRevisionNumber, raw, actor); err != nil {
		handler.WriteServerError(w, r, "INTERNAL_ERROR", "failed to update revision", err)
		return
	}
	if _, err := tx.Exec(r.Context(), `
		UPDATE then_now_experiences SET title = $2, updated_at = now(), updated_by = $3 WHERE id = $1`,
		detail.ID, snapshot.Title, actor); err != nil {
		handler.WriteServerError(w, r, "INTERNAL_ERROR", "failed to update experience", err)
		return
	}
	if err := tx.Commit(r.Context()); err != nil {
		handler.WriteServerError(w, r, "INTERNAL_ERROR", "failed to commit experience", err)
		return
	}
	h.audit(r.Context(), "experience", detail.ID, "save", actor, "Draft saved", nil)
	updated, err := h.buildExperienceDetail(r.Context(), detail.ID)
	if err != nil {
		handler.WriteServerError(w, r, "INTERNAL_ERROR", "failed to load experience", err)
		return
	}
	writeThenNowJSON(w, http.StatusOK, updated)
}

func (h *ThenNowHandler) SubmitExperience(w http.ResponseWriter, r *http.Request) {
	if !middleware.CanThenNowAuthor(middleware.RoleFromCtx(r.Context())) {
		handler.WriteError(w, http.StatusForbidden, "FORBIDDEN", "authoring access required")
		return
	}
	detail, err := h.buildExperienceDetail(r.Context(), chi.URLParam(r, "id"))
	if err != nil {
		handler.WriteError(w, http.StatusNotFound, "NOT_FOUND", "experience not found")
		return
	}
	if detail.OpenRevision == nil || detail.OpenRevision.Status != "draft" {
		handler.WriteError(w, http.StatusConflict, "INVALID_STATE", "only a draft revision can be submitted")
		return
	}
	if !detail.Readiness.ReadyToSubmit {
		writeThenNowJSON(w, http.StatusConflict, map[string]any{"error": map[string]string{"code": "NOT_READY", "message": "readiness blockers prevent submission"}, "readiness": detail.Readiness})
		return
	}
	actor := middleware.UserID(r.Context())
	tx, err := h.pool.Begin(r.Context())
	if err != nil {
		handler.WriteServerError(w, r, "INTERNAL_ERROR", "failed to start transaction", err)
		return
	}
	defer tx.Rollback(r.Context())
	if _, err := tx.Exec(r.Context(), `
		UPDATE then_now_revisions SET status = 'in_review', submitted_by = $3, submitted_at = now(),
		       reviewer_id = NULL, last_decision = NULL, updated_at = now(), updated_by = $3
		WHERE experience_id = $1 AND revision_number = $2`,
		detail.ID, detail.OpenRevisionNumber, actor); err != nil {
		handler.WriteServerError(w, r, "INTERNAL_ERROR", "failed to submit revision", err)
		return
	}
	nextStatus := detail.Status
	if detail.LiveRevisionNumber == 0 {
		nextStatus = "in_review"
	}
	if _, err := tx.Exec(r.Context(), `UPDATE then_now_experiences SET status = $2, updated_at = now(), updated_by = $3 WHERE id = $1`, detail.ID, nextStatus, actor); err != nil {
		handler.WriteServerError(w, r, "INTERNAL_ERROR", "failed to update experience status", err)
		return
	}
	if err := tx.Commit(r.Context()); err != nil {
		handler.WriteServerError(w, r, "INTERNAL_ERROR", "failed to commit submission", err)
		return
	}
	h.audit(r.Context(), "experience", detail.ID, "submit", actor, fmt.Sprintf("Revision v%d submitted for review", detail.OpenRevisionNumber), nil)
	updated, _ := h.buildExperienceDetail(r.Context(), detail.ID)
	writeThenNowJSON(w, http.StatusOK, updated)
}

func (h *ThenNowHandler) AssignReviewer(w http.ResponseWriter, r *http.Request) {
	if !middleware.CanThenNowReview(middleware.RoleFromCtx(r.Context())) {
		handler.WriteError(w, http.StatusForbidden, "FORBIDDEN", "review access required")
		return
	}
	detail, err := h.buildExperienceDetail(r.Context(), chi.URLParam(r, "id"))
	if err != nil {
		handler.WriteError(w, http.StatusNotFound, "NOT_FOUND", "experience not found")
		return
	}
	if detail.OpenRevision == nil || detail.OpenRevision.Status != "in_review" {
		handler.WriteError(w, http.StatusConflict, "INVALID_STATE", "assignment is only available in review")
		return
	}
	var body struct {
		ReviewerID string `json:"reviewerId"`
	}
	_ = json.NewDecoder(r.Body).Decode(&body)
	reviewer := strings.TrimSpace(body.ReviewerID)
	if reviewer == "" {
		reviewer = middleware.UserID(r.Context())
	}
	if reviewer == detail.OpenRevision.UploadedByID || reviewer == detail.OpenRevision.SubmittedByID {
		handler.WriteError(w, http.StatusConflict, "SEPARATION", "the uploader cannot review their own revision")
		return
	}
	var role string
	if err := h.pool.QueryRow(r.Context(), `SELECT COALESCE(role, '') FROM users WHERE id = $1`, reviewer).Scan(&role); err != nil || (role != "administrator" && role != "super_admin") {
		handler.WriteError(w, http.StatusBadRequest, "INVALID_REVIEWER", "reviewer must be an administrator")
		return
	}
	if _, err := h.pool.Exec(r.Context(), `
		UPDATE then_now_revisions SET reviewer_id = $3, updated_at = now(), updated_by = $4
		WHERE experience_id = $1 AND revision_number = $2`,
		detail.ID, detail.OpenRevisionNumber, reviewer, middleware.UserID(r.Context())); err != nil {
		handler.WriteServerError(w, r, "INTERNAL_ERROR", "failed to assign reviewer", err)
		return
	}
	h.audit(r.Context(), "experience", detail.ID, "assign", middleware.UserID(r.Context()), "Reviewer assigned", map[string]string{"reviewerId": reviewer})
	updated, _ := h.buildExperienceDetail(r.Context(), detail.ID)
	writeThenNowJSON(w, http.StatusOK, updated)
}

// UnassignReviewer clears the reviewer on a frozen revision and returns it to
// the unassigned queue. Clearing is always safe: a decision still requires a
// reviewer, and the reviewer ≠ uploader check still runs on the next assign
// and on every decision, so unassign cannot be used to bypass separation.
func (h *ThenNowHandler) UnassignReviewer(w http.ResponseWriter, r *http.Request) {
	if !middleware.CanThenNowReview(middleware.RoleFromCtx(r.Context())) {
		handler.WriteError(w, http.StatusForbidden, "FORBIDDEN", "review access required")
		return
	}
	detail, err := h.buildExperienceDetail(r.Context(), chi.URLParam(r, "id"))
	if err != nil {
		handler.WriteError(w, http.StatusNotFound, "NOT_FOUND", "experience not found")
		return
	}
	if detail.OpenRevision == nil || detail.OpenRevision.Status != "in_review" {
		handler.WriteError(w, http.StatusConflict, "INVALID_STATE", "assignment is only available in review")
		return
	}
	previous := detail.OpenRevision.ReviewerID
	if previous == "" {
		// Already unassigned: idempotent success with no duplicate audit event.
		writeThenNowJSON(w, http.StatusOK, detail)
		return
	}
	if _, err := h.pool.Exec(r.Context(), `
		UPDATE then_now_revisions SET reviewer_id = NULL, updated_at = now(), updated_by = $3
		WHERE experience_id = $1 AND revision_number = $2`,
		detail.ID, detail.OpenRevisionNumber, middleware.UserID(r.Context())); err != nil {
		handler.WriteServerError(w, r, "INTERNAL_ERROR", "failed to unassign reviewer", err)
		return
	}
	h.audit(r.Context(), "experience", detail.ID, "unassign", middleware.UserID(r.Context()),
		"Reviewer unassigned", map[string]string{"previousReviewerId": previous})
	updated, _ := h.buildExperienceDetail(r.Context(), detail.ID)
	writeThenNowJSON(w, http.StatusOK, updated)
}

func (h *ThenNowHandler) DecideExperience(w http.ResponseWriter, r *http.Request) {
	if !middleware.CanThenNowReview(middleware.RoleFromCtx(r.Context())) {
		handler.WriteError(w, http.StatusForbidden, "FORBIDDEN", "review access required")
		return
	}
	detail, err := h.buildExperienceDetail(r.Context(), chi.URLParam(r, "id"))
	if err != nil {
		handler.WriteError(w, http.StatusNotFound, "NOT_FOUND", "experience not found")
		return
	}
	open := detail.OpenRevision
	if open == nil {
		handler.WriteError(w, http.StatusConflict, "INVALID_STATE", "there is no open revision to decide")
		return
	}
	var body struct {
		Decision string `json:"decision"`
		Notes    string `json:"notes"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		handler.WriteError(w, http.StatusBadRequest, "INVALID_BODY", "invalid request body")
		return
	}
	notes := strings.TrimSpace(body.Notes)
	if body.Decision != "approve" && body.Decision != "request_changes" && body.Decision != "reject" && body.Decision != "return" {
		handler.WriteError(w, http.StatusBadRequest, "INVALID_DECISION", "decision must be approve, request_changes, reject, or return")
		return
	}
	if body.Decision != "approve" && notes == "" {
		handler.WriteError(w, http.StatusBadRequest, "NOTES_REQUIRED", "a note is required for this decision")
		return
	}
	// An approved revision can be reopened to draft with a reason. This is not
	// an approval decision: it withdraws the approval while preserving the
	// separation rule for every in-review decision below.
	approvedReopen := open.Status == "approved" && body.Decision == "return"
	if open.Status != "in_review" && !approvedReopen {
		handler.WriteError(w, http.StatusConflict, "INVALID_STATE", "only a frozen revision can be decided")
		return
	}
	actor := middleware.UserID(r.Context())
	if !approvedReopen {
		if open.ReviewerID == "" {
			handler.WriteError(w, http.StatusConflict, "UNASSIGNED", "assign a reviewer before deciding")
			return
		}
		if actor == open.UploadedByID || actor == open.SubmittedByID {
			handler.WriteError(w, http.StatusConflict, "SEPARATION", "the uploader cannot review their own revision")
			return
		}
	}
	snapshot := snapshotOf(open)
	if body.Decision == "approve" {
		if snapshot.Sensitivity == "restricted" {
			handler.WriteError(w, http.StatusConflict, "RESTRICTED", "restricted content cannot be approved without resolution")
			return
		}
		if snapshot.Sensitivity != "standard" && !open.SecondReviewerApprove {
			handler.WriteError(w, http.StatusConflict, "SECOND_REVIEWER", "record the second reviewer before approving")
			return
		}
	}
	noteJSON, _ := json.Marshal([]thennow.ReviewNote{{
		Author: middleware.Email(r.Context()),
		Role:   string(middleware.RoleFromCtx(r.Context())),
		At:     time.Now().UTC().Format(time.RFC3339),
		Note:   notes,
	}})
	tx, err := h.pool.Begin(r.Context())
	if err != nil {
		handler.WriteServerError(w, r, "INTERNAL_ERROR", "failed to start transaction", err)
		return
	}
	defer tx.Rollback(r.Context())
	switch body.Decision {
	case "approve":
		if _, err := tx.Exec(r.Context(), `
			UPDATE then_now_revisions SET status = 'approved', last_decision = 'approved',
			       review_notes = review_notes || $3::jsonb, updated_at = now(), updated_by = $4
			WHERE experience_id = $1 AND revision_number = $2`,
			detail.ID, detail.OpenRevisionNumber, noteJSON, actor); err != nil {
			handler.WriteServerError(w, r, "INTERNAL_ERROR", "failed to approve revision", err)
			return
		}
		nextStatus := detail.Status
		if detail.LiveRevisionNumber == 0 {
			nextStatus = "approved"
		}
		if _, err := tx.Exec(r.Context(), `UPDATE then_now_experiences SET status = $2, updated_at = now(), updated_by = $3 WHERE id = $1`, detail.ID, nextStatus, actor); err != nil {
			handler.WriteServerError(w, r, "INTERNAL_ERROR", "failed to update experience status", err)
			return
		}
	case "request_changes", "reject", "return":
		decision := "changes"
		if body.Decision == "reject" {
			decision = "rejected"
		} else if body.Decision == "return" {
			decision = "returned"
		}
		if _, err := tx.Exec(r.Context(), `
			UPDATE then_now_revisions SET status = 'draft', last_decision = $3,
			       reviewer_id = NULL, scheduled_at = NULL, schedule_note = NULL,
			       review_notes = review_notes || $4::jsonb,
			       updated_at = now(), updated_by = $5
			WHERE experience_id = $1 AND revision_number = $2`,
			detail.ID, detail.OpenRevisionNumber, decision, noteJSON, actor); err != nil {
			handler.WriteServerError(w, r, "INTERNAL_ERROR", "failed to return revision", err)
			return
		}
		nextStatus := detail.Status
		if detail.LiveRevisionNumber == 0 {
			nextStatus = "draft"
		}
		if _, err := tx.Exec(r.Context(), `UPDATE then_now_experiences SET status = $2, updated_at = now(), updated_by = $3 WHERE id = $1`, detail.ID, nextStatus, actor); err != nil {
			handler.WriteServerError(w, r, "INTERNAL_ERROR", "failed to update experience status", err)
			return
		}
	}
	if err := tx.Commit(r.Context()); err != nil {
		handler.WriteServerError(w, r, "INTERNAL_ERROR", "failed to commit decision", err)
		return
	}
	h.audit(r.Context(), "experience", detail.ID, body.Decision, actor, fmt.Sprintf("Revision v%d %s", detail.OpenRevisionNumber, body.Decision), map[string]string{"notes": notes, "from": open.Status})
	updated, _ := h.buildExperienceDetail(r.Context(), detail.ID)
	writeThenNowJSON(w, http.StatusOK, updated)
}

func (h *ThenNowHandler) RecordClientApproval(w http.ResponseWriter, r *http.Request) {
	if !middleware.CanThenNowPublish(middleware.RoleFromCtx(r.Context())) {
		handler.WriteError(w, http.StatusForbidden, "FORBIDDEN", "rights access required")
		return
	}
	detail, err := h.buildExperienceDetail(r.Context(), chi.URLParam(r, "id"))
	if err != nil {
		handler.WriteError(w, http.StatusNotFound, "NOT_FOUND", "experience not found")
		return
	}
	var approval thennow.ClientApproval
	if err := json.NewDecoder(r.Body).Decode(&approval); err != nil || strings.TrimSpace(approval.Name) == "" {
		handler.WriteError(w, http.StatusBadRequest, "VALIDATION_ERROR", "name is required")
		return
	}
	if approval.At == "" {
		approval.At = time.Now().UTC().Format(time.RFC3339)
	}
	raw, _ := json.Marshal(approval)
	if _, err := h.pool.Exec(r.Context(), `
		UPDATE then_now_revisions SET client_approval = $3, updated_at = now(), updated_by = $4
		WHERE experience_id = $1 AND revision_number = $2`,
		detail.ID, detail.OpenRevisionNumber, raw, middleware.UserID(r.Context())); err != nil {
		handler.WriteServerError(w, r, "INTERNAL_ERROR", "failed to record client approval", err)
		return
	}
	h.audit(r.Context(), "experience", detail.ID, "client", middleware.UserID(r.Context()), "Client approval recorded", map[string]string{"name": approval.Name})
	updated, _ := h.buildExperienceDetail(r.Context(), detail.ID)
	writeThenNowJSON(w, http.StatusOK, updated)
}

func (h *ThenNowHandler) RecordExternalReviewer(w http.ResponseWriter, r *http.Request) {
	if !middleware.CanThenNowReview(middleware.RoleFromCtx(r.Context())) {
		handler.WriteError(w, http.StatusForbidden, "FORBIDDEN", "review access required")
		return
	}
	detail, err := h.buildExperienceDetail(r.Context(), chi.URLParam(r, "id"))
	if err != nil {
		handler.WriteError(w, http.StatusNotFound, "NOT_FOUND", "experience not found")
		return
	}
	var reviewer thennow.ExternalReviewer
	if err := json.NewDecoder(r.Body).Decode(&reviewer); err != nil || strings.TrimSpace(reviewer.Name) == "" {
		handler.WriteError(w, http.StatusBadRequest, "VALIDATION_ERROR", "name is required")
		return
	}
	approved := reviewer.Decision == "approved"
	raw, _ := json.Marshal(reviewer)
	if _, err := h.pool.Exec(r.Context(), `
		UPDATE then_now_revisions SET second_reviewer = $3, second_reviewer_approved = $4,
		       updated_at = now(), updated_by = $5
		WHERE experience_id = $1 AND revision_number = $2`,
		detail.ID, detail.OpenRevisionNumber, raw, approved, middleware.UserID(r.Context())); err != nil {
		handler.WriteServerError(w, r, "INTERNAL_ERROR", "failed to record external reviewer", err)
		return
	}
	h.audit(r.Context(), "experience", detail.ID, "reviewer", middleware.UserID(r.Context()), "External reviewer recorded", map[string]string{"name": reviewer.Name, "decision": reviewer.Decision})
	updated, _ := h.buildExperienceDetail(r.Context(), detail.ID)
	writeThenNowJSON(w, http.StatusOK, updated)
}

func (h *ThenNowHandler) ScheduleExperience(w http.ResponseWriter, r *http.Request) {
	if !middleware.CanThenNowPublish(middleware.RoleFromCtx(r.Context())) {
		handler.WriteError(w, http.StatusForbidden, "FORBIDDEN", "publishing access required")
		return
	}
	detail, err := h.buildExperienceDetail(r.Context(), chi.URLParam(r, "id"))
	if err != nil {
		handler.WriteError(w, http.StatusNotFound, "NOT_FOUND", "experience not found")
		return
	}
	if detail.OpenRevision == nil || detail.OpenRevision.Status != "approved" {
		handler.WriteError(w, http.StatusConflict, "INVALID_STATE", "only an approved revision can be scheduled")
		return
	}
	if !detail.Readiness.ReadyToPublish {
		writeThenNowJSON(w, http.StatusConflict, map[string]any{"error": map[string]string{"code": "NOT_READY", "message": "readiness blockers prevent scheduling"}, "readiness": detail.Readiness})
		return
	}
	var body struct {
		ScheduledAt string `json:"scheduledAt"`
		Note        string `json:"note"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		handler.WriteError(w, http.StatusBadRequest, "INVALID_BODY", "invalid request body")
		return
	}
	scheduled, err := time.Parse(time.RFC3339, body.ScheduledAt)
	if err != nil || scheduled.Before(time.Now()) {
		handler.WriteError(w, http.StatusBadRequest, "INVALID_SCHEDULE", "scheduledAt must be a future RFC3339 time")
		return
	}
	actor := middleware.UserID(r.Context())
	if _, err := h.pool.Exec(r.Context(), `
		UPDATE then_now_revisions SET scheduled_at = $3, schedule_note = NULLIF($4, ''), updated_at = now(), updated_by = $5
		WHERE experience_id = $1 AND revision_number = $2`,
		detail.ID, detail.OpenRevisionNumber, scheduled.UTC(), body.Note, actor); err != nil {
		handler.WriteServerError(w, r, "INTERNAL_ERROR", "failed to schedule revision", err)
		return
	}
	if detail.LiveRevisionNumber == 0 {
		_, _ = h.pool.Exec(r.Context(), `UPDATE then_now_experiences SET status = 'scheduled', updated_at = now(), updated_by = $2 WHERE id = $1`, detail.ID, actor)
	}
	h.audit(r.Context(), "experience", detail.ID, "schedule", actor, "Publish scheduled", map[string]string{"scheduledAt": scheduled.UTC().Format(time.RFC3339)})
	updated, _ := h.buildExperienceDetail(r.Context(), detail.ID)
	writeThenNowJSON(w, http.StatusOK, updated)
}

// CancelSchedule clears a pending scheduled publish and returns the approved
// revision to the ready-to-publish state. The revision status stays approved.
func (h *ThenNowHandler) CancelSchedule(w http.ResponseWriter, r *http.Request) {
	if !middleware.CanThenNowPublish(middleware.RoleFromCtx(r.Context())) {
		handler.WriteError(w, http.StatusForbidden, "FORBIDDEN", "publishing access required")
		return
	}
	detail, err := h.buildExperienceDetail(r.Context(), chi.URLParam(r, "id"))
	if err != nil {
		handler.WriteError(w, http.StatusNotFound, "NOT_FOUND", "experience not found")
		return
	}
	if detail.OpenRevision == nil || detail.OpenRevision.Status != "approved" || detail.OpenRevision.ScheduledAt == "" {
		handler.WriteError(w, http.StatusConflict, "INVALID_STATE", "there is no pending scheduled publish to cancel")
		return
	}
	actor := middleware.UserID(r.Context())
	scheduledAt := detail.OpenRevision.ScheduledAt
	tx, err := h.pool.Begin(r.Context())
	if err != nil {
		handler.WriteServerError(w, r, "INTERNAL_ERROR", "failed to start transaction", err)
		return
	}
	defer tx.Rollback(r.Context())
	if _, err := tx.Exec(r.Context(), `
		UPDATE then_now_revisions SET scheduled_at = NULL, schedule_note = NULL,
		       updated_at = now(), updated_by = $3
		WHERE experience_id = $1 AND revision_number = $2`,
		detail.ID, detail.OpenRevisionNumber, actor); err != nil {
		handler.WriteServerError(w, r, "INTERNAL_ERROR", "failed to cancel schedule", err)
		return
	}
	if detail.LiveRevisionNumber == 0 {
		if _, err := tx.Exec(r.Context(), `UPDATE then_now_experiences SET status = 'approved', updated_at = now(), updated_by = $2 WHERE id = $1`, detail.ID, actor); err != nil {
			handler.WriteServerError(w, r, "INTERNAL_ERROR", "failed to reset experience status", err)
			return
		}
	}
	if err := tx.Commit(r.Context()); err != nil {
		handler.WriteServerError(w, r, "INTERNAL_ERROR", "failed to commit schedule cancellation", err)
		return
	}
	h.audit(r.Context(), "experience", detail.ID, "schedule_cancel", actor, "Publish schedule cancelled",
		map[string]string{"scheduledAt": scheduledAt})
	updated, _ := h.buildExperienceDetail(r.Context(), detail.ID)
	writeThenNowJSON(w, http.StatusOK, updated)
}

func (h *ThenNowHandler) PublishExperience(w http.ResponseWriter, r *http.Request) {
	if !middleware.CanThenNowPublish(middleware.RoleFromCtx(r.Context())) {
		handler.WriteError(w, http.StatusForbidden, "FORBIDDEN", "publishing access required")
		return
	}
	detail, err := h.buildExperienceDetail(r.Context(), chi.URLParam(r, "id"))
	if err != nil {
		handler.WriteError(w, http.StatusNotFound, "NOT_FOUND", "experience not found")
		return
	}
	if detail.OpenRevision == nil || detail.OpenRevision.Status != "approved" {
		handler.WriteError(w, http.StatusConflict, "INVALID_STATE", "only an approved revision can be published")
		return
	}
	if !detail.Readiness.ReadyToPublish {
		writeThenNowJSON(w, http.StatusConflict, map[string]any{"error": map[string]string{"code": "NOT_READY", "message": "readiness blockers prevent publishing"}, "readiness": detail.Readiness})
		return
	}
	// The approved admin flow publishes the experience first, then the site.
	// The site readiness gate requires a published experience, so checking the
	// site status here would deadlock the first publication.
	actor := middleware.UserID(r.Context())
	tx, err := h.pool.Begin(r.Context())
	if err != nil {
		handler.WriteServerError(w, r, "INTERNAL_ERROR", "failed to start transaction", err)
		return
	}
	defer tx.Rollback(r.Context())
	if _, err := tx.Exec(r.Context(), `
		UPDATE then_now_revisions SET status = 'published', published_at = now(),
		       scheduled_at = NULL, updated_at = now(), updated_by = $3
		WHERE experience_id = $1 AND revision_number = $2`,
		detail.ID, detail.OpenRevisionNumber, actor); err != nil {
		handler.WriteServerError(w, r, "INTERNAL_ERROR", "failed to publish revision", err)
		return
	}
	if _, err := tx.Exec(r.Context(), `
		UPDATE then_now_experiences SET status = 'published', live_revision_number = $2,
		       open_revision_number = $2, updated_at = now(), updated_by = $3 WHERE id = $1`,
		detail.ID, detail.OpenRevisionNumber, actor); err != nil {
		handler.WriteServerError(w, r, "INTERNAL_ERROR", "failed to promote experience", err)
		return
	}
	if err := tx.Commit(r.Context()); err != nil {
		handler.WriteServerError(w, r, "INTERNAL_ERROR", "failed to commit publish", err)
		return
	}
	h.audit(r.Context(), "experience", detail.ID, "publish", actor, fmt.Sprintf("Revision v%d published", detail.OpenRevisionNumber), nil)
	updated, _ := h.buildExperienceDetail(r.Context(), detail.ID)
	writeThenNowJSON(w, http.StatusOK, updated)
}

func (h *ThenNowHandler) NewRevision(w http.ResponseWriter, r *http.Request) {
	if !middleware.CanThenNowAuthor(middleware.RoleFromCtx(r.Context())) {
		handler.WriteError(w, http.StatusForbidden, "FORBIDDEN", "authoring access required")
		return
	}
	detail, err := h.buildExperienceDetail(r.Context(), chi.URLParam(r, "id"))
	if err != nil {
		handler.WriteError(w, http.StatusNotFound, "NOT_FOUND", "experience not found")
		return
	}
	if detail.LiveRevisionNumber == 0 || detail.OpenRevisionNumber > detail.LiveRevisionNumber {
		handler.WriteError(w, http.StatusConflict, "OPEN_REVISION", "there is already an open revision")
		return
	}
	live, err := h.loadRevision(r.Context(), detail.ID, detail.LiveRevisionNumber)
	if err != nil {
		handler.WriteServerError(w, r, "INTERNAL_ERROR", "failed to load live revision", err)
		return
	}
	next := detail.LiveRevisionNumber + 1
	actor := middleware.UserID(r.Context())
	if _, err := h.pool.Exec(r.Context(), `
		INSERT INTO then_now_revisions (experience_id, revision_number, status, snapshot, created_by, updated_by)
		VALUES ($1, $2, 'draft', $3, $4, $4)`, detail.ID, next, live.Snapshot, actor); err != nil {
		handler.WriteServerError(w, r, "INTERNAL_ERROR", "failed to create revision", err)
		return
	}
	if _, err := h.pool.Exec(r.Context(), `UPDATE then_now_experiences SET open_revision_number = $2, updated_at = now(), updated_by = $3 WHERE id = $1`, detail.ID, next, actor); err != nil {
		handler.WriteServerError(w, r, "INTERNAL_ERROR", "failed to open revision", err)
		return
	}
	h.audit(r.Context(), "experience", detail.ID, "draft", actor, fmt.Sprintf("Draft v%d created", next), nil)
	updated, _ := h.buildExperienceDetail(r.Context(), detail.ID)
	writeThenNowJSON(w, http.StatusCreated, updated)
}

func (h *ThenNowHandler) ChangeExperienceStatus(w http.ResponseWriter, r *http.Request) {
	if !middleware.CanThenNowPublish(middleware.RoleFromCtx(r.Context())) && !middleware.CanThenNowTakedown(middleware.RoleFromCtx(r.Context())) {
		handler.WriteError(w, http.StatusForbidden, "FORBIDDEN", "publishing access required")
		return
	}
	var body struct {
		Action string `json:"action"`
		Reason string `json:"reason"`
	}
	_ = json.NewDecoder(r.Body).Decode(&body)
	reason := strings.TrimSpace(body.Reason)
	status := ""
	switch body.Action {
	case "unpublish":
		status = "unpublished"
	case "archive":
		if !middleware.CanThenNowTakedown(middleware.RoleFromCtx(r.Context())) {
			handler.WriteError(w, http.StatusForbidden, "FORBIDDEN", "super-admin access required")
			return
		}
		status = "archived"
	case "revoke":
		if !middleware.CanThenNowTakedown(middleware.RoleFromCtx(r.Context())) {
			handler.WriteError(w, http.StatusForbidden, "FORBIDDEN", "super-admin access required")
			return
		}
		status = "revoked"
	default:
		handler.WriteError(w, http.StatusBadRequest, "INVALID_ACTION", "action must be unpublish, archive, or revoke")
		return
	}
	id := chi.URLParam(r, "id")
	if _, err := h.pool.Exec(r.Context(), `UPDATE then_now_experiences SET status = $2, updated_at = now(), updated_by = $3 WHERE id = $1`, id, status, middleware.UserID(r.Context())); err != nil {
		handler.WriteServerError(w, r, "INTERNAL_ERROR", "failed to change experience status", err)
		return
	}
	if status != "published" {
		_, _ = h.pool.Exec(r.Context(), `
			UPDATE then_now_revisions SET scheduled_at = NULL, updated_at = now(), updated_by = $2
			WHERE experience_id = $1
			  AND revision_number = (SELECT open_revision_number FROM then_now_experiences WHERE id = $1)`,
			id, middleware.UserID(r.Context()))
	}
	h.audit(r.Context(), "experience", id, status, middleware.UserID(r.Context()), "Experience "+status, statusMetadata(reason))
	detail, err := h.buildExperienceDetail(r.Context(), id)
	if err != nil {
		handler.WriteServerError(w, r, "INTERNAL_ERROR", "failed to load experience", err)
		return
	}
	writeThenNowJSON(w, http.StatusOK, detail)
}

func (h *ThenNowHandler) ReviewQueue(w http.ResponseWriter, r *http.Request) {
	if !middleware.CanThenNowReview(middleware.RoleFromCtx(r.Context())) {
		handler.WriteError(w, http.StatusForbidden, "FORBIDDEN", "review access required")
		return
	}
	filter := r.URL.Query().Get("filter")
	if filter == "" {
		filter = "all"
	}
	actor := middleware.UserID(r.Context())
	rows, err := h.pool.Query(r.Context(), `
		SELECT r.id::text, e.id::text, s.id::text, s.name, e.title, r.revision_number,
		       COALESCE(su.name, su.email, ''), COALESCE(r.submitted_at, now()),
		       COALESCE(r.snapshot->>'sensitivity', 'standard'),
		       COALESCE(ru.name, ru.email, '')
		FROM then_now_revisions r
		JOIN then_now_experiences e ON e.id = r.experience_id AND e.open_revision_number = r.revision_number
		JOIN heritage_sites s ON s.id = e.site_id
		LEFT JOIN users su ON su.id = r.submitted_by
		LEFT JOIN users ru ON ru.id = r.reviewer_id
		WHERE r.status = 'in_review'
		  AND ($1 = 'all' OR ($1 = 'unassigned' AND r.reviewer_id IS NULL) OR ($1 = 'mine' AND r.reviewer_id = $2))
		ORDER BY r.reviewer_id IS NOT NULL, (r.reviewer_id = $2) DESC, r.submitted_at ASC`, filter, actor)
	if err != nil {
		handler.WriteServerError(w, r, "INTERNAL_ERROR", "failed to load review queue", err)
		return
	}
	defer rows.Close()
	items := make([]map[string]any, 0)
	for rows.Next() {
		var revisionID, experienceID, siteID, siteName, title, submittedBy, sensitivity, reviewer string
		var revision int
		var submittedAt pgtype.Timestamp
		if err := rows.Scan(&revisionID, &experienceID, &siteID, &siteName, &title, &revision, &submittedBy, &submittedAt, &sensitivity, &reviewer); err != nil {
			handler.WriteServerError(w, r, "INTERNAL_ERROR", "failed to read review row", err)
			return
		}
		if reviewer == "" {
			reviewer = "Unassigned"
		}
		items = append(items, map[string]any{
			"revisionId": revisionID, "experienceId": experienceID, "siteId": siteID,
			"siteName": siteName, "title": title, "revisionNumber": revision,
			"submittedBy": submittedBy, "submittedAt": formatTS(submittedAt),
			"sensitivity": sensitivity, "reviewer": reviewer,
		})
	}
	writeThenNowJSON(w, http.StatusOK, map[string]any{"items": items})
}

func (h *ThenNowHandler) GetReview(w http.ResponseWriter, r *http.Request) {
	if !middleware.CanThenNowReview(middleware.RoleFromCtx(r.Context())) {
		handler.WriteError(w, http.StatusForbidden, "FORBIDDEN", "review access required")
		return
	}
	var experienceID string
	if err := h.pool.QueryRow(r.Context(), `SELECT experience_id::text FROM then_now_revisions WHERE id = $1`, chi.URLParam(r, "revisionId")).Scan(&experienceID); err != nil {
		handler.WriteError(w, http.StatusNotFound, "NOT_FOUND", "revision not found")
		return
	}
	detail, err := h.buildExperienceDetail(r.Context(), experienceID)
	if err != nil {
		handler.WriteError(w, http.StatusNotFound, "NOT_FOUND", "experience not found")
		return
	}
	writeThenNowJSON(w, http.StatusOK, detail)
}

func (h *ThenNowHandler) Audit(w http.ResponseWriter, r *http.Request) {
	if !middleware.CanThenNowRead(middleware.RoleFromCtx(r.Context())) {
		handler.WriteError(w, http.StatusForbidden, "FORBIDDEN", "Then & Now access required")
		return
	}
	entityType := r.URL.Query().Get("entityType")
	entityID := r.URL.Query().Get("entityId")
	rows, err := h.pool.Query(r.Context(), `
		SELECT id::text, entity_type, entity_id::text, event_type, COALESCE(actor_id, ''),
		       summary, COALESCE(metadata, '{}'::jsonb), created_at
		FROM then_now_audit_events
		WHERE ($1 = '' OR entity_type = $1) AND ($2 = '' OR entity_id::text = $2)
		ORDER BY created_at DESC LIMIT 200`, entityType, entityID)
	if err != nil {
		handler.WriteServerError(w, r, "INTERNAL_ERROR", "failed to load audit events", err)
		return
	}
	defer rows.Close()
	items := make([]map[string]any, 0)
	for rows.Next() {
		var id, kind, entity, event, actor, summary string
		var metadata []byte
		var created pgtype.Timestamp
		if err := rows.Scan(&id, &kind, &entity, &event, &actor, &summary, &metadata, &created); err != nil {
			handler.WriteServerError(w, r, "INTERNAL_ERROR", "failed to read audit event", err)
			return
		}
		items = append(items, map[string]any{
			"id": id, "entityType": kind, "entityId": entity, "eventType": event,
			"actorId": actor, "summary": summary, "metadata": rawJSONOr(metadata, "{}"),
			"createdAt": formatTS(created),
		})
	}
	writeThenNowJSON(w, http.StatusOK, map[string]any{"items": items})
}
