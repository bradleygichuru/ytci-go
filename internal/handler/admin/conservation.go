package admin

import (
	"encoding/json"
	"net/http"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/bradleygichuru/ytci-go/internal/db/gen"
	"github.com/bradleygichuru/ytci-go/internal/handler"
	"github.com/bradleygichuru/ytci-go/internal/middleware"
	"github.com/bradleygichuru/ytci-go/internal/model"
)

type ConservationAdminHandler struct {
	pool *pgxpool.Pool
}

func NewConservationAdminHandler(pool *pgxpool.Pool) *ConservationAdminHandler {
	return &ConservationAdminHandler{pool: pool}
}

func (h *ConservationAdminHandler) ListMobile(w http.ResponseWriter, r *http.Request) {
	queries := gen.New(h.pool)
	activities, err := queries.ListPublicConservation(r.Context(), 50)
	if err != nil {
		handler.WriteError(w, http.StatusInternalServerError, "INTERNAL_ERROR", "failed to list conservation activities")
		return
	}
	if activities == nil {
		activities = []gen.ListPublicConservationRow{}
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(activities)
}

func (h *ConservationAdminHandler) List(w http.ResponseWriter, r *http.Request) {
	rows, err := h.pool.Query(r.Context(),
		`SELECT id, title, organizer, event_date, status, current_participants FROM conservation_activities
		 WHERE privacy_level = 'public' ORDER BY created_at DESC LIMIT 50`)
	if err != nil {
		handler.WriteError(w, http.StatusInternalServerError, "INTERNAL_ERROR", "failed to list activities")
		return
	}
	defer rows.Close()

	type item struct {
		ID                  string  `json:"id"`
		Title               string  `json:"title"`
		Organizer           string  `json:"organizer"`
		EventDate           *string `json:"eventDate,omitempty"`
		Status              string  `json:"status"`
		CurrentParticipants int     `json:"currentParticipants"`
	}
	var items []item
	for rows.Next() {
		var i item
		rows.Scan(&i.ID, &i.Title, &i.Organizer, &i.EventDate, &i.Status, &i.CurrentParticipants)
		items = append(items, i)
	}
	if items == nil {
		items = []item{}
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(model.Paginated[item]{Items: items, HasMore: false})
}

func (h *ConservationAdminHandler) Create(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Title        string   `json:"title"`
		Organizer    string   `json:"organizer"`
		Description  string   `json:"description,omitempty"`
		Lat          *float64 `json:"lat,omitempty"`
		Lng          *float64 `json:"lng,omitempty"`
		EventDate    string   `json:"eventDate,omitempty"`
		ImpactMetric string   `json:"impactMetric,omitempty"`
		BadgeName    *string  `json:"badgeName,omitempty"`
		BadgeIconURL *string  `json:"badgeIconUrl,omitempty"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		handler.WriteError(w, http.StatusBadRequest, "INVALID_REQUEST", "invalid request body")
		return
	}

	var id string
	err := h.pool.QueryRow(r.Context(),
		`INSERT INTO conservation_activities (title, organizer, description, location, event_date, impact_metric, badge_name, badge_icon_url, created_by)
		 VALUES ($1, $2, $3, ST_SetSRID(ST_MakePoint($4, $5), 4326), $6, $7, $8, $9, $10) RETURNING id`,
		req.Title, req.Organizer, req.Description, valOrNilFloat(req.Lng), valOrNilFloat(req.Lat),
		req.EventDate, req.ImpactMetric, req.BadgeName, req.BadgeIconURL, middleware.UserID(r.Context()),
	).Scan(&id)
	if err != nil {
		handler.WriteError(w, http.StatusInternalServerError, "INTERNAL_ERROR", "failed to create activity")
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	json.NewEncoder(w).Encode(map[string]string{"id": id, "status": "open"})
}

func (h *ConservationAdminHandler) Update(w http.ResponseWriter, r *http.Request) {
	activityID := r.PathValue("id")
	var req struct {
		Title        *string `json:"title,omitempty"`
		Description  *string `json:"description,omitempty"`
		Status       *string `json:"status,omitempty"`
		BadgeName    *string `json:"badgeName,omitempty"`
		BadgeIconURL *string `json:"badgeIconUrl,omitempty"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		handler.WriteError(w, http.StatusBadRequest, "INVALID_REQUEST", "invalid request body")
		return
	}

	_, err := h.pool.Exec(r.Context(),
		`UPDATE conservation_activities SET
		 title = CASE WHEN $2::text != '' THEN $2 ELSE title END,
		 description = COALESCE($3::text, description),
		 status = CASE WHEN $4::text != '' THEN $4 ELSE status END,
		 badge_name = COALESCE($5::text, badge_name),
		 badge_icon_url = COALESCE($6::text, badge_icon_url),
		 updated_at = now()
		 WHERE id = $1`,
		activityID, valOrEmpty(req.Title), req.Description, valOrEmpty(req.Status), req.BadgeName, req.BadgeIconURL)
	if err != nil {
		handler.WriteError(w, http.StatusInternalServerError, "INTERNAL_ERROR", "failed to update activity")
		return
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{"status": "updated"})
}

func (h *ConservationAdminHandler) ListEvidence(w http.ResponseWriter, r *http.Request) {
	rows, err := h.pool.Query(r.Context(),
		`SELECT ce.id, ce.user_id, ce.activity_id, ce.description, ce.status, ce.moderated_by, ce.created_at,
			ca.title as activity_title
		 FROM conservation_evidence ce
		 LEFT JOIN conservation_activities ca ON ca.id = ce.activity_id
		 ORDER BY ce.created_at DESC LIMIT 50`)
	if err != nil {
		handler.WriteError(w, http.StatusInternalServerError, "INTERNAL_ERROR", "failed to list evidence")
		return
	}
	defer rows.Close()

	type item struct {
		ID            string  `json:"id"`
		UserID        string  `json:"userId"`
		ActivityID    string  `json:"activityId"`
		ActivityTitle *string `json:"activityTitle,omitempty"`
		Description   *string `json:"description,omitempty"`
		Status        string  `json:"status"`
		ModeratedBy   *string `json:"moderatedBy,omitempty"`
		CreatedAt     string  `json:"createdAt"`
	}
	var items []item
	for rows.Next() {
		var i item
		rows.Scan(&i.ID, &i.UserID, &i.ActivityID, &i.Description, &i.Status, &i.ModeratedBy, &i.CreatedAt, &i.ActivityTitle)
		items = append(items, i)
	}
	if items == nil {
		items = []item{}
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(model.Paginated[item]{Items: items, HasMore: false})
}

func (h *ConservationAdminHandler) ReviewEvidence(w http.ResponseWriter, r *http.Request) {
	evidenceID := r.PathValue("id")
	var req struct {
		Action string `json:"action"`
		Note   string `json:"note"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		handler.WriteError(w, http.StatusBadRequest, "INVALID_REQUEST", "invalid request body")
		return
	}

	moderatorID := middleware.UserID(r.Context())
	status := "in_progress"
	if req.Action == "approve" {
		status = "approved"
	}

	_, err := h.pool.Exec(r.Context(),
		`UPDATE conservation_evidence SET status = $2, moderated_by = $3, moderation_note = $4, moderated_at = now(), updated_at = now() WHERE id = $1`,
		evidenceID, status, moderatorID, req.Note)
	if err != nil {
		handler.WriteError(w, http.StatusInternalServerError, "INTERNAL_ERROR", "failed to review evidence")
		return
	}

	if status == "approved" {
		var userID, activityID, activityTitle string
		var badgeName, badgeIconURL *string
		err := h.pool.QueryRow(r.Context(),
			`SELECT ce.user_id, ce.activity_id, ca.title, ca.badge_name, ca.badge_icon_url
			 FROM conservation_evidence ce
			 JOIN conservation_activities ca ON ca.id = ce.activity_id
			 WHERE ce.id = $1`, evidenceID,
		).Scan(&userID, &activityID, &activityTitle, &badgeName, &badgeIconURL)
		if err == nil {
			handler.AwardBadge(r.Context(), h.pool, userID, badgeName, badgeIconURL, "conservation", activityID, activityTitle)
		}
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{"status": status})
}

func (h *ConservationAdminHandler) ConservationDetail(w http.ResponseWriter, r *http.Request) {
	activityID := r.PathValue("id")

	var id, title, organizer, privacyLevel, status, createdAt string
	var description, locationLabel, eventDate, impactMetric *string
	var impactTarget, participantLimit, currentParticipants *int
	var lng, lat *float64

	err := h.pool.QueryRow(r.Context(),
		`SELECT id, title, organizer, description,
			ST_X(location::geometry) AS lng, ST_Y(location::geometry) AS lat,
			location_label, privacy_level, event_date::text,
			impact_metric, impact_target, participant_limit, current_participants,
			status, created_at::text
		 FROM conservation_activities
		 WHERE id = $1 AND privacy_level = 'public'`, activityID,
	).Scan(&id, &title, &organizer, &description, &lng, &lat, &locationLabel, &privacyLevel,
		&eventDate, &impactMetric, &impactTarget, &participantLimit, &currentParticipants,
		&status, &createdAt)
	if err == pgx.ErrNoRows {
		handler.WriteError(w, http.StatusNotFound, "NOT_FOUND", "activity not found")
		return
	}
	if err != nil {
		handler.WriteError(w, http.StatusInternalServerError, "INTERNAL_ERROR", "failed to get activity")
		return
	}

	resp := map[string]any{
		"id":                  id,
		"title":               title,
		"organizer":           organizer,
		"description":         description,
		"lng":                 lng,
		"lat":                 lat,
		"locationLabel":       locationLabel,
		"privacyLevel":        privacyLevel,
		"eventDate":           eventDate,
		"impactMetric":        impactMetric,
		"impactTarget":        impactTarget,
		"participantLimit":    participantLimit,
		"currentParticipants": currentParticipants,
		"status":              status,
		"createdAt":           createdAt,
		"participants":        []any{},
	}

	participantRows, err := h.pool.Query(r.Context(),
		`SELECT cp.user_id, COALESCE(up.display_name, 'Explorer')
		 FROM conservation_participants cp
		 LEFT JOIN user_profiles up ON up.user_id = cp.user_id
		 WHERE cp.activity_id = $1`, activityID)
	if err == nil {
		defer participantRows.Close()
		var participants []map[string]any
		for participantRows.Next() {
			var pUserID, pName string
			participantRows.Scan(&pUserID, &pName)
			participants = append(participants, map[string]any{
				"userId": pUserID,
				"name":   pName,
			})
		}
		if participants != nil {
			resp["participants"] = participants
		}
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(resp)
}

func valOrNilFloat(f *float64) interface{} {
	if f == nil {
		return nil
	}
	return *f
}
