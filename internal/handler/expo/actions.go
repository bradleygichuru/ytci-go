package expo

import (
	"encoding/json"
	"net/http"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/bradleygichuru/ytci-go/internal/handler"
	"github.com/bradleygichuru/ytci-go/internal/middleware"
	"github.com/bradleygichuru/ytci-go/internal/model"
)

type ActionsHandler struct {
	pool *pgxpool.Pool
}

func NewActionsHandler(pool *pgxpool.Pool) *ActionsHandler {
	return &ActionsHandler{pool: pool}
}

type createStoryRequest struct {
	DestinationID string   `json:"destinationId"`
	Caption       string   `json:"caption"`
	Journal       string   `json:"journal"`
	Tags          []string `json:"tags"`
	MediaIDs      []string `json:"mediaIds"`
}

func (h *ActionsHandler) CreateStory(w http.ResponseWriter, r *http.Request) {
	var req createStoryRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		handler.WriteError(w, http.StatusBadRequest, "INVALID_REQUEST", "invalid request body")
		return
	}

	tags := ""
	for i, t := range req.Tags {
		if i > 0 {
			tags += ","
		}
		tags += t
	}

	// destinationId is optional — a youth story may describe the journey rather
	// than a specific site. Coerce an empty string to NULL so we don't feed
	// Postgres an invalid UUID literal ("22P02: invalid input syntax for
	// type uuid: \"\"").
	var destinationID any
	if req.DestinationID != "" {
		destinationID = req.DestinationID
	}

	var storyID string
	err := h.pool.QueryRow(r.Context(),
		`INSERT INTO stories (creator_id, destination_id, caption, journal, tags, status)
		 VALUES ($1, $2, $3, $4, $5, 'pending') RETURNING id`,
		middleware.UserID(r.Context()), destinationID, req.Caption, req.Journal, tags,
	).Scan(&storyID)
	if err != nil {
		handler.WriteServerError(w, r, "INTERNAL_ERROR", "failed to create story", err)
		return
	}

	for _, mediaID := range req.MediaIDs {
		_, err := h.pool.Exec(r.Context(),
			`UPDATE media_assets SET entity_type = 'story', entity_id = $1 WHERE id = $2 AND entity_type = 'unlinked'`,
			storyID, mediaID)
		if err != nil {
			// Log and continue — don't fail the whole request for orphaned media
			continue
		}
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	json.NewEncoder(w).Encode(map[string]string{"id": storyID, "status": "pending"})
}

type toggleRequest struct {
	StoryID string `json:"storyId"`
}

func (h *ActionsHandler) toggleInteraction(w http.ResponseWriter, r *http.Request, interactionType string) {
	var req toggleRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		handler.WriteError(w, http.StatusBadRequest, "INVALID_REQUEST", "invalid request body")
		return
	}
	if req.StoryID == "" {
		handler.WriteError(w, http.StatusBadRequest, "INVALID_REQUEST", "storyId is required")
		return
	}

	tag, err := h.pool.Exec(r.Context(),
		`DELETE FROM story_interactions WHERE user_id = $1 AND story_id = $2 AND interaction_type = $3`,
		middleware.UserID(r.Context()), req.StoryID, interactionType)
	if err != nil {
		handler.WriteServerError(w, r, "INTERNAL_ERROR", "failed to toggle", err)
		return
	}

	if tag.RowsAffected() == 0 {
		_, err = h.pool.Exec(r.Context(),
			`INSERT INTO story_interactions (user_id, story_id, interaction_type) VALUES ($1, $2, $3)`,
			middleware.UserID(r.Context()), req.StoryID, interactionType)
		if err != nil {
			handler.WriteServerError(w, r, "INTERNAL_ERROR", "failed to toggle", err)
			return
		}
	}

	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(map[string]string{"status": "toggled"})
}

func (h *ActionsHandler) ToggleLike(w http.ResponseWriter, r *http.Request) {
	h.toggleInteraction(w, r, "like")
}

func (h *ActionsHandler) ToggleSave(w http.ResponseWriter, r *http.Request) {
	h.toggleInteraction(w, r, "save")
}

func (h *ActionsHandler) JoinChallenge(w http.ResponseWriter, r *http.Request) {
	challengeID := r.PathValue("id")
	if challengeID == "" {
		handler.WriteError(w, http.StatusBadRequest, "INVALID_ID", "challenge id is required")
		return
	}

	tag, err := h.pool.Exec(r.Context(),
		`INSERT INTO challenge_progress (user_id, challenge_id, status)
		 VALUES ($1, $2, 'joined') ON CONFLICT DO NOTHING`,
		middleware.UserID(r.Context()), challengeID)
	if err != nil {
		handler.WriteServerError(w, r, "INTERNAL_ERROR", "failed to join challenge", err)
		return
	}

	// RowsAffected is 0 when the user had already joined — the frontend uses
	// alreadyJoined to avoid re-sending and to show the joined state.
	alreadyJoined := tag.RowsAffected() == 0

	w.WriteHeader(http.StatusCreated)
	json.NewEncoder(w).Encode(map[string]any{
		"status":        "joined",
		"alreadyJoined": alreadyJoined,
	})
}

func (h *ActionsHandler) JoinConservation(w http.ResponseWriter, r *http.Request) {
	activityID := r.PathValue("id")
	if activityID == "" {
		handler.WriteError(w, http.StatusBadRequest, "INVALID_ID", "activity id is required")
		return
	}
	userID := middleware.UserID(r.Context())

	var participantLimit *int
	var currentParticipants int
	err := h.pool.QueryRow(r.Context(),
		`SELECT participant_limit, COALESCE(current_participants, 0)
		 FROM conservation_activities WHERE id = $1`, activityID,
	).Scan(&participantLimit, &currentParticipants)
	if err != nil {
		handler.WriteError(w, http.StatusNotFound, "NOT_FOUND", "activity not found")
		return
	}
	if participantLimit != nil && currentParticipants >= *participantLimit {
		handler.WriteError(w, http.StatusConflict, "ACTIVITY_FULL", "this activity has reached its participant limit")
		return
	}

	tag, err := h.pool.Exec(r.Context(),
		`INSERT INTO conservation_participants (user_id, activity_id)
		 VALUES ($1, $2) ON CONFLICT (user_id, activity_id) DO NOTHING`,
		userID, activityID)
	if err != nil {
		handler.WriteServerError(w, r, "INTERNAL_ERROR", "failed to join", err)
		return
	}

	if tag.RowsAffected() > 0 {
		_, _ = h.pool.Exec(r.Context(),
			`UPDATE conservation_activities SET current_participants = current_participants + 1 WHERE id = $1`,
			activityID)
	}

	// RowsAffected is 0 when the user had already joined — the frontend uses
	// alreadyJoined to avoid re-sending and to show the joined state.
	alreadyJoined := tag.RowsAffected() == 0

	w.WriteHeader(http.StatusCreated)
	json.NewEncoder(w).Encode(map[string]any{
		"status":        "joined",
		"alreadyJoined": alreadyJoined,
	})
}

func (h *ActionsHandler) EnrollCourse(w http.ResponseWriter, r *http.Request) {
	courseID := r.PathValue("id")
	if courseID == "" {
		handler.WriteError(w, http.StatusBadRequest, "INVALID_ID", "course id is required")
		return
	}

	_, err := h.pool.Exec(r.Context(),
		`INSERT INTO course_enrollments (user_id, course_id) VALUES ($1, $2) ON CONFLICT DO NOTHING`,
		middleware.UserID(r.Context()), courseID)
	if err != nil {
		handler.WriteServerError(w, r, "INTERNAL_ERROR", "failed to enroll", err)
		return
	}

	w.WriteHeader(http.StatusCreated)
	json.NewEncoder(w).Encode(map[string]string{"status": "enrolled"})
}

func (h *ActionsHandler) RecordAppOpen(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Platform   string `json:"platform"`
		AppVersion string `json:"appVersion"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		handler.WriteError(w, http.StatusBadRequest, "INVALID_REQUEST", "invalid request body")
		return
	}

	userID := middleware.UserID(r.Context())

	var recent int
	err := h.pool.QueryRow(r.Context(),
		`SELECT COUNT(*) FROM app_opens WHERE user_id = $1 AND opened_at > now() - interval '5 minutes'`,
		userID).Scan(&recent)
	if err != nil {
		handler.WriteServerError(w, r, "INTERNAL_ERROR", "failed to check recent app opens", err)
		return
	}
	if recent > 0 {
		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(map[string]string{"status": "recorded"})
		return
	}

	_, err = h.pool.Exec(r.Context(),
		`INSERT INTO app_opens (user_id, platform, app_version) VALUES ($1, $2, $3)`,
		userID, req.Platform, req.AppVersion)
	if err != nil {
		handler.WriteServerError(w, r, "INTERNAL_ERROR", "failed to record app open", err)
		return
	}

	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(map[string]string{"status": "recorded"})
}

func (h *ActionsHandler) RecordEvent(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Event      string                 `json:"event"`
		Properties map[string]interface{} `json:"properties"`
		Platform   string                 `json:"platform"`
		AppVersion string                 `json:"appVersion"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		handler.WriteError(w, http.StatusBadRequest, "INVALID_REQUEST", "invalid request body")
		return
	}
	if req.Event == "" {
		handler.WriteError(w, http.StatusBadRequest, "INVALID_REQUEST", "event is required")
		return
	}

	userID := middleware.UserID(r.Context())

	_, err := h.pool.Exec(r.Context(),
		`INSERT INTO analytics_events (user_id, event, properties, platform, app_version) VALUES ($1, $2, $3, $4, $5)`,
		userID, req.Event, req.Properties, req.Platform, req.AppVersion)
	if err != nil {
		handler.WriteServerError(w, r, "INTERNAL_ERROR", "failed to record analytics event", err)
		return
	}

	w.WriteHeader(http.StatusCreated)
	json.NewEncoder(w).Encode(map[string]string{"status": "recorded"})
}

func (h *ActionsHandler) SaveEvent(w http.ResponseWriter, r *http.Request) {
	eventID := r.PathValue("id")
	if eventID == "" {
		handler.WriteError(w, http.StatusBadRequest, "INVALID_ID", "event id is required")
		return
	}

	_, err := h.pool.Exec(r.Context(),
		`INSERT INTO event_saves (user_id, event_id) VALUES ($1, $2) ON CONFLICT DO NOTHING`,
		middleware.UserID(r.Context()), eventID)
	if err != nil {
		handler.WriteServerError(w, r, "INTERNAL_ERROR", "failed to save event", err)
		return
	}

	w.WriteHeader(http.StatusCreated)
	json.NewEncoder(w).Encode(map[string]string{"status": "saved"})
}

func (h *ActionsHandler) ListSavedEvents(w http.ResponseWriter, r *http.Request) {
	userID := middleware.UserID(r.Context())
	rows, err := h.pool.Query(r.Context(),
		`SELECT e.id, e.title, e.organizer, e.county, e.venue, e.event_date::text, e.type, e.image_url
		 FROM event_saves es
		 JOIN events e ON e.id = es.event_id
		 WHERE es.user_id = $1
		 ORDER BY e.event_date ASC
		 LIMIT 50`, userID)
	if err != nil {
		handler.WriteServerError(w, r, "INTERNAL_ERROR", "failed to list saved events", err)
		return
	}
	defer rows.Close()

	type savedEvent struct {
		ID        string  `json:"id"`
		Title     string  `json:"title"`
		Organizer string  `json:"organizer"`
		County    string  `json:"county"`
		Venue     *string `json:"venue,omitempty"`
		EventDate string  `json:"eventDate"`
		Type      string  `json:"type"`
		ImageURL  *string `json:"imageUrl,omitempty"`
	}

	var items []savedEvent
	for rows.Next() {
		var i savedEvent
		rows.Scan(&i.ID, &i.Title, &i.Organizer, &i.County, &i.Venue, &i.EventDate, &i.Type, &i.ImageURL)
		items = append(items, i)
	}
	if err := rows.Err(); err != nil {
		handler.WriteServerError(w, r, "INTERNAL_ERROR", "failed to iterate events", err)
		return
	}
	if items == nil {
		items = []savedEvent{}
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(model.Paginated[savedEvent]{Items: items, HasMore: false})
}

type evidenceRequest struct {
	MediaIDs    string   `json:"mediaIds"`
	Description string   `json:"description,omitempty"`
	Lat         *float64 `json:"lat,omitempty"`
	Lng         *float64 `json:"lng,omitempty"`
}

type conservationEvidenceRequest struct {
	MediaIDs     string   `json:"mediaIds"`
	Description  string   `json:"description,omitempty"`
	TreesPlanted *int     `json:"treesPlanted,omitempty"`
	HoursSpent   *float64 `json:"hoursSpent,omitempty"`
	Lat          *float64 `json:"lat,omitempty"`
	Lng          *float64 `json:"lng,omitempty"`
}

func (h *ActionsHandler) SubmitConservationEvidence(w http.ResponseWriter, r *http.Request) {
	activityID := r.PathValue("id")
	if activityID == "" {
		handler.WriteError(w, http.StatusBadRequest, "INVALID_ID", "activity id is required")
		return
	}
	var req conservationEvidenceRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		handler.WriteError(w, http.StatusBadRequest, "INVALID_REQUEST", "invalid request body")
		return
	}
	if req.Description == "" && req.MediaIDs == "" &&
		(req.TreesPlanted == nil || *req.TreesPlanted <= 0) &&
		(req.HoursSpent == nil || *req.HoursSpent <= 0) {
		handler.WriteError(w, http.StatusBadRequest, "INVALID_REQUEST", "description, mediaIds, treesPlanted, or hoursSpent is required")
		return
	}

	var evidenceID string
	err := h.pool.QueryRow(r.Context(),
		`INSERT INTO conservation_evidence (user_id, activity_id, description, media_ids, trees_planted, hours_spent, lat, lng)
		 VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
		 ON CONFLICT (user_id, activity_id) DO UPDATE SET
		 status = 'pending',
		 description = EXCLUDED.description,
		 media_ids = EXCLUDED.media_ids,
		 trees_planted = COALESCE(EXCLUDED.trees_planted, conservation_evidence.trees_planted),
		 hours_spent = COALESCE(EXCLUDED.hours_spent, conservation_evidence.hours_spent),
		 lat = COALESCE(EXCLUDED.lat, conservation_evidence.lat),
		 lng = COALESCE(EXCLUDED.lng, conservation_evidence.lng),
		 moderated_by = NULL,
		 moderation_note = NULL,
		 moderated_at = NULL,
		 updated_at = now()
		 RETURNING id`,
		middleware.UserID(r.Context()), activityID, req.Description, req.MediaIDs, req.TreesPlanted, req.HoursSpent, req.Lat, req.Lng).Scan(&evidenceID)
	if err != nil {
		handler.WriteServerError(w, r, "INTERNAL_ERROR", "failed to submit evidence", err)
		return
	}

	w.WriteHeader(http.StatusCreated)
	json.NewEncoder(w).Encode(map[string]string{"id": evidenceID, "status": "pending"})
}

func (h *ActionsHandler) SubmitChallengeEvidence(w http.ResponseWriter, r *http.Request) {
	challengeID := r.PathValue("id")
	if challengeID == "" {
		handler.WriteError(w, http.StatusBadRequest, "INVALID_ID", "challenge id is required")
		return
	}
	var req evidenceRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		handler.WriteError(w, http.StatusBadRequest, "INVALID_REQUEST", "invalid request body")
		return
	}
	if req.Description == "" && req.MediaIDs == "" {
		handler.WriteError(w, http.StatusBadRequest, "INVALID_REQUEST", "description or mediaIds is required")
		return
	}

	var progressID string
	err := h.pool.QueryRow(r.Context(),
		`UPDATE challenge_progress SET
		 status = 'submitted',
		 evidence = CASE
			WHEN $3::text != '' AND $4::text != '' AND $5::float IS NOT NULL AND $6::float IS NOT NULL THEN jsonb_build_object('description', $3::text, 'mediaIds', $4::text, 'lat', $5::float, 'lng', $6::float)
			WHEN $3::text != '' AND $4::text != '' THEN jsonb_build_object('description', $3::text, 'mediaIds', $4::text)
			WHEN $3::text != '' AND $5::float IS NOT NULL AND $6::float IS NOT NULL THEN jsonb_build_object('description', $3::text, 'lat', $5::float, 'lng', $6::float)
			WHEN $3::text != '' THEN jsonb_build_object('description', $3::text)
			WHEN $4::text != '' AND $5::float IS NOT NULL AND $6::float IS NOT NULL THEN jsonb_build_object('mediaIds', $4::text, 'lat', $5::float, 'lng', $6::float)
			WHEN $4::text != '' THEN jsonb_build_object('mediaIds', $4::text)
			ELSE evidence
		 END,
		 updated_at = now()
		 WHERE user_id = $1 AND challenge_id = $2 RETURNING id`,
		middleware.UserID(r.Context()), challengeID, req.Description, req.MediaIDs, req.Lat, req.Lng).Scan(&progressID)
	if err != nil {
		handler.WriteServerError(w, r, "INTERNAL_ERROR", "failed to submit evidence", err)
		return
	}

	w.WriteHeader(http.StatusCreated)
	json.NewEncoder(w).Encode(map[string]string{"id": progressID, "status": "submitted"})
}

func (h *ActionsHandler) AttendEvent(w http.ResponseWriter, r *http.Request) {
	eventID := r.PathValue("id")
	if eventID == "" {
		handler.WriteError(w, http.StatusBadRequest, "INVALID_ID", "event id is required")
		return
	}

	var req struct {
		Status string `json:"status"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		handler.WriteError(w, http.StatusBadRequest, "INVALID_REQUEST", "invalid request body")
		return
	}

	validStatuses := map[string]bool{"joined": true, "interested": true, "waitlist": true}
	if !validStatuses[req.Status] {
		handler.WriteError(w, http.StatusBadRequest, "INVALID_STATUS", "status must be joined, interested, or waitlist")
		return
	}

	_, err := h.pool.Exec(r.Context(),
		`INSERT INTO event_attendees (event_id, user_id, status)
		 VALUES ($1, $2, $3)
		 ON CONFLICT (user_id, event_id, status)
		 DO UPDATE SET status = EXCLUDED.status`,
		eventID, middleware.UserID(r.Context()), req.Status)
	if err != nil {
		handler.WriteServerError(w, r, "INTERNAL_ERROR", "failed to attend event", err)
		return
	}

	json.NewEncoder(w).Encode(map[string]string{"status": req.Status})
}

func (h *ActionsHandler) LeaveEvent(w http.ResponseWriter, r *http.Request) {
	eventID := r.PathValue("id")
	if eventID == "" {
		handler.WriteError(w, http.StatusBadRequest, "INVALID_ID", "event id is required")
		return
	}

	_, err := h.pool.Exec(r.Context(),
		`DELETE FROM event_attendees WHERE event_id = $1 AND user_id = $2`,
		eventID, middleware.UserID(r.Context()))
	if err != nil {
		handler.WriteServerError(w, r, "INTERNAL_ERROR", "failed to leave event", err)
		return
	}

	json.NewEncoder(w).Encode(map[string]string{"status": "cancelled"})
}

func (h *ActionsHandler) GetMyConservationActivities(w http.ResponseWriter, r *http.Request) {
	userID := middleware.UserID(r.Context())

	rows, err := h.pool.Query(r.Context(),
		`SELECT ca.id, ca.title, ca.organizer, ca.status, ca.event_date::text,
			ca.impact_metric, ca.current_participants, ca.location_label,
			cp.joined_at::text,
			CASE WHEN ce.id IS NOT NULL THEN true ELSE false END AS evidence_submitted,
			ce.status AS evidence_status,
			CASE WHEN b.id IS NOT NULL THEN true ELSE false END AS badge_earned
		 FROM conservation_participants cp
		 JOIN conservation_activities ca ON ca.id = cp.activity_id
		 LEFT JOIN conservation_evidence ce ON ce.user_id = cp.user_id AND ce.activity_id = cp.activity_id
		 LEFT JOIN badges b ON b.user_id = cp.user_id AND b.source_type = 'conservation' AND b.source_id = cp.activity_id
		 WHERE cp.user_id = $1
		 ORDER BY cp.joined_at DESC LIMIT 50`,
		userID,
	)
	if err != nil {
		handler.WriteServerError(w, r, "INTERNAL_ERROR", "failed to list activities", err)
		return
	}
	defer rows.Close()

	type item struct {
		ID                  string  `json:"id"`
		Title               string  `json:"title"`
		Organizer           string  `json:"organizer"`
		Status              string  `json:"status"`
		EventDate           *string `json:"eventDate,omitempty"`
		ImpactMetric        *string `json:"impactMetric,omitempty"`
		CurrentParticipants int     `json:"currentParticipants"`
		LocationLabel       *string `json:"locationLabel,omitempty"`
		JoinedAt            string  `json:"joinedAt"`
		EvidenceSubmitted   bool    `json:"evidenceSubmitted"`
		EvidenceStatus      *string `json:"evidenceStatus,omitempty"`
		BadgeEarned         bool    `json:"badgeEarned"`
	}

	var items []item
	for rows.Next() {
		var i item
		rows.Scan(&i.ID, &i.Title, &i.Organizer, &i.Status, &i.EventDate,
			&i.ImpactMetric, &i.CurrentParticipants, &i.LocationLabel,
			&i.JoinedAt, &i.EvidenceSubmitted, &i.EvidenceStatus, &i.BadgeEarned)
		items = append(items, i)
	}
	if items == nil {
		items = []item{}
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]any{"items": items})
}

func (h *ActionsHandler) GetConservationProgress(w http.ResponseWriter, r *http.Request) {
	userID := middleware.UserID(r.Context())
	activityID := r.PathValue("id")

	var joinedAt *string
	var evidenceDescription *string
	var evidenceStatus *string
	var mediaIds *string
	var moderationNote *string
	var reviewedAt *string
	var submittedAt *string
	var treesPlanted *int
	var hoursSpent *float64
	var badgeEarned bool

	err := h.pool.QueryRow(r.Context(),
		`SELECT cp.joined_at::text,
			ce.description, ce.status, ce.media_ids, ce.moderation_note,
			ce.moderated_at::text, ce.created_at::text,
			ce.trees_planted, ce.hours_spent,
			CASE WHEN b.id IS NOT NULL THEN true ELSE false END
		 FROM conservation_participants cp
		 LEFT JOIN conservation_evidence ce ON ce.user_id = cp.user_id AND ce.activity_id = cp.activity_id
		 LEFT JOIN badges b ON b.user_id = cp.user_id AND b.source_type = 'conservation' AND b.source_id = cp.activity_id
		 WHERE cp.user_id = $1 AND cp.activity_id = $2`,
		userID, activityID,
	).Scan(&joinedAt, &evidenceDescription, &evidenceStatus, &mediaIds,
		&moderationNote, &reviewedAt, &submittedAt, &treesPlanted, &hoursSpent, &badgeEarned)
	if err == pgx.ErrNoRows {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{"joined": false})
		return
	}
	if err != nil {
		handler.WriteServerError(w, r, "INTERNAL_ERROR", "failed to get progress", err)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]any{
		"joined":              true,
		"joinedAt":            joinedAt,
		"evidenceSubmitted":   evidenceDescription != nil,
		"evidenceStatus":      evidenceStatus,
		"evidenceDescription": evidenceDescription,
		"mediaIds":            mediaIds,
		"moderationNote":      moderationNote,
		"reviewedAt":          reviewedAt,
		"submittedAt":         submittedAt,
		"treesPlanted":        treesPlanted,
		"hoursSpent":          hoursSpent,
		"badgeEarned":         badgeEarned,
	})
}

func (h *ActionsHandler) LeaveConservation(w http.ResponseWriter, r *http.Request) {
	activityID := r.PathValue("id")
	if activityID == "" {
		handler.WriteError(w, http.StatusBadRequest, "INVALID_ID", "activity id is required")
		return
	}
	userID := middleware.UserID(r.Context())

	tag, err := h.pool.Exec(r.Context(),
		`DELETE FROM conservation_participants WHERE user_id = $1 AND activity_id = $2`,
		userID, activityID)
	if err != nil {
		handler.WriteServerError(w, r, "INTERNAL_ERROR", "failed to leave activity", err)
		return
	}
	if tag.RowsAffected() == 0 {
		handler.WriteError(w, http.StatusNotFound, "NOT_FOUND", "not joined to this activity")
		return
	}

	_, _ = h.pool.Exec(r.Context(),
		`UPDATE conservation_activities SET current_participants = GREATEST(current_participants - 1, 0) WHERE id = $1`,
		activityID)

	json.NewEncoder(w).Encode(map[string]string{"status": "left"})
}
