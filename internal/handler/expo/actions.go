package expo

import (
	"encoding/json"
	"net/http"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/bradleygichuru/ytci-go/internal/handler"
	"github.com/bradleygichuru/ytci-go/internal/middleware"
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

	var storyID string
	err := h.pool.QueryRow(r.Context(),
		`INSERT INTO stories (creator_id, destination_id, caption, journal, tags, status)
		 VALUES ($1, $2, $3, $4, $5, 'pending') RETURNING id`,
		middleware.UserID(r.Context()), req.DestinationID, req.Caption, req.Journal, tags,
	).Scan(&storyID)
	if err != nil {
		handler.WriteError(w, http.StatusInternalServerError, "INTERNAL_ERROR", "failed to create story")
		return
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
	json.NewDecoder(r.Body).Decode(&req)

	tag, err := h.pool.Exec(r.Context(),
		`DELETE FROM story_interactions WHERE user_id = $1 AND story_id = $2 AND interaction_type = $3`,
		middleware.UserID(r.Context()), req.StoryID, interactionType)
	if err != nil {
		handler.WriteError(w, http.StatusInternalServerError, "INTERNAL_ERROR", "failed to toggle")
		return
	}

	if tag.RowsAffected() == 0 {
		_, err = h.pool.Exec(r.Context(),
			`INSERT INTO story_interactions (user_id, story_id, interaction_type) VALUES ($1, $2, $3)`,
			middleware.UserID(r.Context()), req.StoryID, interactionType)
		if err != nil {
			handler.WriteError(w, http.StatusInternalServerError, "INTERNAL_ERROR", "failed to toggle")
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

	_, err := h.pool.Exec(r.Context(),
		`INSERT INTO challenge_progress (user_id, challenge_id, status)
		 VALUES ($1, $2, 'joined') ON CONFLICT DO NOTHING`,
		middleware.UserID(r.Context()), challengeID)
	if err != nil {
		handler.WriteError(w, http.StatusInternalServerError, "INTERNAL_ERROR", "failed to join challenge")
		return
	}

	w.WriteHeader(http.StatusCreated)
	json.NewEncoder(w).Encode(map[string]string{"status": "joined"})
}

func (h *ActionsHandler) JoinConservation(w http.ResponseWriter, r *http.Request) {
	activityID := r.PathValue("id")
	if activityID == "" {
		handler.WriteError(w, http.StatusBadRequest, "INVALID_ID", "activity id is required")
		return
	}

	_, err := h.pool.Exec(r.Context(),
		`UPDATE conservation_activities SET current_participants = current_participants + 1 WHERE id = $1`, activityID)
	if err != nil {
		handler.WriteError(w, http.StatusInternalServerError, "INTERNAL_ERROR", "failed to join")
		return
	}

	w.WriteHeader(http.StatusCreated)
	json.NewEncoder(w).Encode(map[string]string{"status": "joined"})
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
		handler.WriteError(w, http.StatusInternalServerError, "INTERNAL_ERROR", "failed to enroll")
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
	json.NewDecoder(r.Body).Decode(&req)

	_, err := h.pool.Exec(r.Context(),
		`INSERT INTO app_opens (user_id, platform, app_version) VALUES ($1, $2, $3)`,
		middleware.UserID(r.Context()), req.Platform, req.AppVersion)
	if err != nil {
		handler.WriteError(w, http.StatusInternalServerError, "INTERNAL_ERROR", "failed to record app open")
		return
	}

	w.WriteHeader(http.StatusOK)
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
		handler.WriteError(w, http.StatusInternalServerError, "INTERNAL_ERROR", "failed to save event")
		return
	}

	w.WriteHeader(http.StatusCreated)
	json.NewEncoder(w).Encode(map[string]string{"status": "saved"})
}

type evidenceRequest struct {
	MediaIDs    string `json:"mediaIds"`
	Description string `json:"description,omitempty"`
}

func (h *ActionsHandler) SubmitConservationEvidence(w http.ResponseWriter, r *http.Request) {
	activityID := r.PathValue("id")
	if activityID == "" {
		handler.WriteError(w, http.StatusBadRequest, "INVALID_ID", "activity id is required")
		return
	}
	var req evidenceRequest
	json.NewDecoder(r.Body).Decode(&req)

	var evidenceID string
	err := h.pool.QueryRow(r.Context(),
		`INSERT INTO conservation_evidence (user_id, activity_id, description, media_ids)
		 VALUES ($1, $2, $3, $4) RETURNING id`,
		middleware.UserID(r.Context()), activityID, req.Description, req.MediaIDs).Scan(&evidenceID)
	if err != nil {
		handler.WriteError(w, http.StatusInternalServerError, "INTERNAL_ERROR", "failed to submit evidence")
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
	json.NewDecoder(r.Body).Decode(&req)

	var progressID string
	err := h.pool.QueryRow(r.Context(),
		`UPDATE challenge_progress SET
		 status = 'submitted',
		 evidence = COALESCE($3::jsonb, evidence),
		 updated_at = now()
		 WHERE user_id = $1 AND challenge_id = $2 RETURNING id`,
		middleware.UserID(r.Context()), challengeID, req.Description).Scan(&progressID)
	if err != nil {
		handler.WriteError(w, http.StatusInternalServerError, "INTERNAL_ERROR", "failed to submit evidence")
		return
	}

	w.WriteHeader(http.StatusCreated)
	json.NewEncoder(w).Encode(map[string]string{"id": progressID, "status": "submitted"})
}
