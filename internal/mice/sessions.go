package mice

import (
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5/pgtype"
)

type sessionWrite struct {
	Day          string `json:"day"`
	StartsAt     string `json:"startsAt"`
	EndsAt       string `json:"endsAt"`
	Room         string `json:"room"`
	Track        string `json:"track"`
	Title        string `json:"title"`
	Description  string `json:"description"`
	Capacity     *int   `json:"capacity"`
	DisplayOrder *int   `json:"displayOrder"`
}

func (s sessionWrite) validate() (ValidationError, bool) {
	if strings.TrimSpace(s.Title) == "" {
		return ValidationError{Field: "title", Message: "title is required"}, false
	}
	if _, err := time.Parse("2006-01-02", s.Day); err != nil {
		return ValidationError{Field: "day", Message: "must be a date (YYYY-MM-DD)"}, false
	}
	start, err := time.Parse(time.RFC3339, s.StartsAt)
	if err != nil {
		return ValidationError{Field: "startsAt", Message: "must be an RFC 3339 timestamp"}, false
	}
	end, err := time.Parse(time.RFC3339, s.EndsAt)
	if err != nil {
		return ValidationError{Field: "endsAt", Message: "must be an RFC 3339 timestamp"}, false
	}
	if !end.After(start) {
		return ValidationError{Field: "endsAt", Message: "endsAt must be after startsAt"}, false
	}
	if s.Capacity != nil && *s.Capacity < 0 {
		return ValidationError{Field: "capacity", Message: "must be zero or greater"}, false
	}
	return ValidationError{}, true
}

func (h *Handler) handleAdminCreateSession(w http.ResponseWriter, r *http.Request) {
	if !requirePermission(w, r, "events.edit_draft") {
		return
	}
	eventID := chi.URLParam(r, "id")
	if !h.eventExists(r, eventID) {
		writeErr(w, r, http.StatusNotFound, "EVENT_NOT_FOUND", "event not found", nil)
		return
	}
	var body sessionWrite
	if !decodeJSON(w, r, &body) {
		return
	}
	if failure, ok := body.validate(); !ok {
		writeValidation(w, failure)
		return
	}
	order := 0
	if body.DisplayOrder != nil {
		order = *body.DisplayOrder
	}
	var id string
	err := h.pool.QueryRow(r.Context(), `
		INSERT INTO event_sessions (event_id, day, starts_at, ends_at, room, track, title,
			description, capacity, display_order)
		VALUES ($1,$2::date,$3,$4,NULLIF($5,''),NULLIF($6,''),$7,NULLIF($8,''),$9,$10)
		RETURNING id::text`,
		eventID, body.Day, body.StartsAt, body.EndsAt, body.Room, body.Track, body.Title,
		body.Description, body.Capacity, order).Scan(&id)
	if err != nil {
		writeServer(w, r, err)
		return
	}
	session, err := h.loadSession(r, id)
	if err != nil {
		writeServer(w, r, err)
		return
	}
	writeJSON(w, http.StatusCreated, session)
}

func (h *Handler) handleAdminUpdateSession(w http.ResponseWriter, r *http.Request) {
	if !requirePermission(w, r, "events.edit_draft") {
		return
	}
	eventID := chi.URLParam(r, "id")
	sessionID := chi.URLParam(r, "sessionId")
	var body sessionWrite
	if !decodeJSON(w, r, &body) {
		return
	}
	if failure, ok := body.validate(); !ok {
		writeValidation(w, failure)
		return
	}
	order := 0
	if body.DisplayOrder != nil {
		order = *body.DisplayOrder
	}
	tag, err := h.pool.Exec(r.Context(), `
		UPDATE event_sessions SET day=$3::date, starts_at=$4, ends_at=$5,
			room=NULLIF($6,''), track=NULLIF($7,''), title=$8, description=NULLIF($9,''),
			capacity=$10, display_order=$11, updated_at=now()
		WHERE id=$1 AND event_id=$2`,
		sessionID, eventID, body.Day, body.StartsAt, body.EndsAt, body.Room, body.Track,
		body.Title, body.Description, body.Capacity, order)
	if err != nil {
		writeServer(w, r, err)
		return
	}
	if tag.RowsAffected() == 0 {
		writeErr(w, r, http.StatusNotFound, "SESSION_NOT_FOUND", "session not found", nil)
		return
	}
	session, err := h.loadSession(r, sessionID)
	if err != nil {
		writeServer(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, session)
}

func (h *Handler) loadSession(r *http.Request, id string) (*Session, error) {
	var s Session
	var room, track, description pgtype.Text
	var capacity pgtype.Int4
	var day string
	err := h.pool.QueryRow(r.Context(), `
		SELECT id::text, day::text, starts_at, ends_at, room, track, title, description, capacity
		  FROM event_sessions WHERE id=$1`, id).Scan(
		&s.ID, &day, &s.StartsAt, &s.EndsAt, &room, &track, &s.Title, &description, &capacity)
	if err != nil {
		return nil, err
	}
	s.Day = day
	s.Room = nullText(room)
	s.Track = nullText(track)
	s.Description = nullText(description)
	s.Capacity = nullInt(capacity)
	speakers, err := h.loadSessionSpeakers(r.Context(), s.ID, false)
	if err != nil {
		return nil, err
	}
	s.Speakers = speakers
	return &s, nil
}

type personWrite struct {
	DisplayName  string `json:"displayName"`
	Title        string `json:"title"`
	Organization string `json:"organization"`
	Bio          string `json:"bio"`
	PhotoURL     string `json:"photoUrl"`
	IsPublic     *bool  `json:"isPublic"`
}

func (h *Handler) handleAdminCreatePerson(w http.ResponseWriter, r *http.Request) {
	if !requirePermission(w, r, "events.edit_draft") {
		return
	}
	eventID := chi.URLParam(r, "id")
	if !h.eventExists(r, eventID) {
		writeErr(w, r, http.StatusNotFound, "EVENT_NOT_FOUND", "event not found", nil)
		return
	}
	var body personWrite
	if !decodeJSON(w, r, &body) {
		return
	}
	if strings.TrimSpace(body.DisplayName) == "" {
		writeValidation(w, ValidationError{Field: "displayName", Message: "displayName is required"})
		return
	}
	isPublic := true
	if body.IsPublic != nil {
		isPublic = *body.IsPublic
	}
	var s Speaker
	var title, org, photo pgtype.Text
	err := h.pool.QueryRow(r.Context(), `
		INSERT INTO event_people (display_name, title, organization, bio, photo_url, is_public)
		VALUES ($1,NULLIF($2,''),NULLIF($3,''),NULLIF($4,''),NULLIF($5,''),$6)
		RETURNING id::text, display_name, title, organization, photo_url`,
		body.DisplayName, body.Title, body.Organization, body.Bio, body.PhotoURL, isPublic).Scan(
		&s.ID, &s.DisplayName, &title, &org, &photo)
	if err != nil {
		writeServer(w, r, err)
		return
	}
	s.Title = nullText(title)
	s.Organization = nullText(org)
	s.PhotoURL = nullText(photo)
	s.ConsentStatus = "granted"
	writeJSON(w, http.StatusCreated, s)
}

func (h *Handler) handleAdminLinkSpeaker(w http.ResponseWriter, r *http.Request) {
	if !requirePermission(w, r, "events.edit_draft") {
		return
	}
	sessionID := chi.URLParam(r, "sessionId")
	var body struct {
		PersonID string `json:"personId"`
		Role     string `json:"role"`
	}
	if !decodeJSON(w, r, &body) {
		return
	}
	if !validUUID(body.PersonID) {
		writeValidation(w, ValidationError{Field: "personId", Message: "a valid person id is required"})
		return
	}
	role := body.Role
	if role == "" {
		role = "speaker"
	}
	if !contains([]string{"speaker", "moderator", "panelist", "host"}, role) {
		writeValidation(w, ValidationError{Field: "role", Message: "unknown speaker role"})
		return
	}
	_, err := h.pool.Exec(r.Context(), `
		INSERT INTO session_speakers (session_id, person_id, speaker_role)
		VALUES ($1,$2,$3)
		ON CONFLICT (session_id, person_id) DO UPDATE SET speaker_role=EXCLUDED.speaker_role`,
		sessionID, body.PersonID, role)
	if err != nil {
		writeServer(w, r, err)
		return
	}
	session, err := h.loadSession(r, sessionID)
	if err != nil {
		if isNoRows(err) {
			writeErr(w, r, http.StatusNotFound, "SESSION_NOT_FOUND", "session not found", nil)
			return
		}
		writeServer(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, session)
}

func (h *Handler) eventExists(r *http.Request, id string) bool {
	if !validUUID(id) {
		return false
	}
	var exists bool
	if err := h.pool.QueryRow(r.Context(), `SELECT EXISTS(SELECT 1 FROM events WHERE id=$1)`, id).Scan(&exists); err != nil {
		return false
	}
	return exists
}
