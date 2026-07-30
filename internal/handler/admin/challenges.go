package admin

import (
	"encoding/json"
	"fmt"
	"net/http"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/bradleygichuru/ytci-go/internal/db/gen"
	"github.com/bradleygichuru/ytci-go/internal/handler"
	"github.com/bradleygichuru/ytci-go/internal/middleware"
	"github.com/bradleygichuru/ytci-go/internal/model"
)

type ChallengeAdminHandler struct {
	pool *pgxpool.Pool
}

func NewChallengeAdminHandler(pool *pgxpool.Pool) *ChallengeAdminHandler {
	return &ChallengeAdminHandler{pool: pool}
}

func (h *ChallengeAdminHandler) ListMobile(w http.ResponseWriter, r *http.Request) {
	queries := gen.New(h.pool)
	challenges, err := queries.ListActiveChallenges(r.Context(), 50)
	if err != nil {
		handler.WriteError(w, http.StatusInternalServerError, "INTERNAL_ERROR", "failed to list challenges")
		return
	}
	if challenges == nil {
		challenges = []gen.ListActiveChallengesRow{}
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(challenges)
}

func (h *ChallengeAdminHandler) List(w http.ResponseWriter, r *http.Request) {
	rows, err := h.pool.Query(r.Context(),
		`SELECT id, title, description, rules, badge_name, badge_icon_url, eligibility::text, status, start_date::text, end_date::text, created_at::text
		 FROM challenges ORDER BY created_at DESC LIMIT 50`)
	if err != nil {
		handler.WriteError(w, http.StatusInternalServerError, "INTERNAL_ERROR", "failed to list challenges")
		return
	}
	defer rows.Close()

	type item struct {
		ID           string  `json:"id"`
		Title        string  `json:"title"`
		Description  *string `json:"description,omitempty"`
		Rules        *string `json:"rules,omitempty"`
		BadgeName    *string `json:"badgeName,omitempty"`
		BadgeIconURL *string `json:"badgeIconUrl,omitempty"`
		Eligibility  *string `json:"eligibility,omitempty"`
		Status       string  `json:"status"`
		StartDate    *string `json:"startDate,omitempty"`
		EndDate      *string `json:"endDate,omitempty"`
		CreatedAt    string  `json:"createdAt"`
	}
	var items []item
	for rows.Next() {
		var i item
		rows.Scan(&i.ID, &i.Title, &i.Description, &i.Rules, &i.BadgeName, &i.BadgeIconURL, &i.Eligibility, &i.Status, &i.StartDate, &i.EndDate, &i.CreatedAt)
		items = append(items, i)
	}
	if items == nil {
		items = []item{}
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(model.Paginated[item]{Items: items, HasMore: false})
}

func (h *ChallengeAdminHandler) Create(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Title        string `json:"title"`
		Description  string `json:"description,omitempty"`
		BadgeName    string `json:"badgeName,omitempty"`
		Rules        string `json:"rules,omitempty"`
		BadgeIconURL string `json:"badgeIconUrl,omitempty"`
		Eligibility  string `json:"eligibility,omitempty"`
		StartDate    string `json:"startDate,omitempty"`
		EndDate      string `json:"endDate,omitempty"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		handler.WriteError(w, http.StatusBadRequest, "INVALID_REQUEST", "invalid request body")
		return
	}

	var errors []model.ValidationError
	if req.Title == "" {
		errors = append(errors, model.ValidationError{Field: "title", Message: "title is required"})
	}
	if req.BadgeName == "" {
		errors = append(errors, model.ValidationError{Field: "badgeName", Message: "badgeName is required"})
	}
	if req.Description == "" {
		errors = append(errors, model.ValidationError{Field: "description", Message: "description is required"})
	}
	if len(errors) > 0 {
		handler.WriteValidationError(w, errors)
		return
	}

	startDate := handler.NullDate(req.StartDate)
	endDate := handler.NullDate(req.EndDate)

	var id string
	if err := h.pool.QueryRow(r.Context(),
		`INSERT INTO challenges (title, description, badge_name, rules, badge_icon_url, eligibility, status, start_date, end_date, created_by)
		 VALUES ($1, $2, $3, $4, $5, $6::jsonb, 'draft', $7, $8, $9) RETURNING id`,
		req.Title, req.Description, req.BadgeName, req.Rules, req.BadgeIconURL, req.Eligibility,
		startDate, endDate, middleware.UserID(r.Context()),
	).Scan(&id); err != nil {
		handler.WriteError(w, http.StatusInternalServerError, "INTERNAL_ERROR", "failed to create challenge")
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	json.NewEncoder(w).Encode(map[string]string{"id": id, "status": "draft"})
}

func (h *ChallengeAdminHandler) Update(w http.ResponseWriter, r *http.Request) {
	challengeID := r.PathValue("id")
	var req struct {
		Title        *string `json:"title,omitempty"`
		Description  *string `json:"description,omitempty"`
		BadgeName    *string `json:"badgeName,omitempty"`
		Rules        *string `json:"rules,omitempty"`
		BadgeIconURL *string `json:"badgeIconUrl,omitempty"`
		Eligibility  *string `json:"eligibility,omitempty"`
		StartDate    *string `json:"startDate,omitempty"`
		EndDate      *string `json:"endDate,omitempty"`
		Status       *string `json:"status,omitempty"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		handler.WriteError(w, http.StatusBadRequest, "INVALID_REQUEST", "invalid request body")
		return
	}

	var errors []model.ValidationError
	if req.Title != nil && *req.Title == "" {
		errors = append(errors, model.ValidationError{Field: "title", Message: "title is required"})
	}
	if req.BadgeName != nil && *req.BadgeName == "" {
		errors = append(errors, model.ValidationError{Field: "badgeName", Message: "badgeName is required"})
	}
	if req.Description != nil && *req.Description == "" {
		errors = append(errors, model.ValidationError{Field: "description", Message: "description is required"})
	}
	if req.Status != nil && *req.Status != "draft" && *req.Status != "active" && *req.Status != "ended" {
		errors = append(errors, model.ValidationError{Field: "status", Message: "status must be draft, active, or ended"})
	}
	if len(errors) > 0 {
		handler.WriteValidationError(w, errors)
		return
	}

	query := `UPDATE challenges SET`
	args := []any{challengeID}
	n := 2

	if req.Title != nil {
		query += fmt.Sprintf(` title = $%d,`, n); args = append(args, *req.Title); n++
	}
	if req.Description != nil {
		query += fmt.Sprintf(` description = $%d,`, n); args = append(args, *req.Description); n++
	}
	if req.BadgeName != nil {
		query += fmt.Sprintf(` badge_name = $%d,`, n); args = append(args, *req.BadgeName); n++
	}
	if req.Rules != nil {
		query += fmt.Sprintf(` rules = $%d,`, n); args = append(args, *req.Rules); n++
	}
	if req.BadgeIconURL != nil {
		query += fmt.Sprintf(` badge_icon_url = $%d,`, n); args = append(args, *req.BadgeIconURL); n++
	}
	if req.Eligibility != nil {
		query += fmt.Sprintf(` eligibility = $%d::jsonb,`, n); args = append(args, *req.Eligibility); n++
	}
	if req.StartDate != nil {
		query += fmt.Sprintf(` start_date = $%d::date,`, n); args = append(args, handler.NullDate(*req.StartDate)); n++
	}
	if req.EndDate != nil {
		query += fmt.Sprintf(` end_date = $%d::date,`, n); args = append(args, handler.NullDate(*req.EndDate)); n++
	}
	if req.Status != nil {
		query += fmt.Sprintf(` status = $%d,`, n); args = append(args, *req.Status); n++
	}
	query += ` updated_at = now() WHERE id = $1`

	_, err := h.pool.Exec(r.Context(), query, args...)
	if err != nil {
		handler.WriteError(w, http.StatusInternalServerError, "INTERNAL_ERROR", "failed to update challenge")
		return
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{"status": "updated"})
}

func (h *ChallengeAdminHandler) Delete(w http.ResponseWriter, r *http.Request) {
	challengeID := r.PathValue("id")
	tag, err := h.pool.Exec(r.Context(),
		`UPDATE challenges SET status = 'ended', updated_at = now() WHERE id = $1`, challengeID)
	if err != nil {
		handler.WriteError(w, http.StatusInternalServerError, "INTERNAL_ERROR", "failed to end challenge")
		return
	}
	if tag.RowsAffected() == 0 {
		handler.WriteError(w, http.StatusNotFound, "NOT_FOUND", "challenge not found")
		return
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{"status": "ended"})
}

func (h *ChallengeAdminHandler) ChallengeDetail(w http.ResponseWriter, r *http.Request) {
	challengeID := r.PathValue("id")

	var id, title, status, createdAt string
	var description, rules, badgeName, badgeIcon, eligibility, startDate, endDate *string
	var currentParticipants *int

	err := h.pool.QueryRow(r.Context(),
		`SELECT id, title, description, rules, badge_name, badge_icon_url,
			eligibility::text, start_date::text, end_date::text, status, created_at::text
		 FROM challenges WHERE id = $1 AND status = 'active'`, challengeID,
	).Scan(&id, &title, &description, &rules, &badgeName, &badgeIcon,
		&eligibility, &startDate, &endDate, &status, &createdAt)
	if err == pgx.ErrNoRows {
		handler.WriteError(w, http.StatusNotFound, "NOT_FOUND", "challenge not found")
		return
	}
	if err != nil {
		handler.WriteError(w, http.StatusInternalServerError, "INTERNAL_ERROR", "failed to get challenge")
		return
	}

	err = h.pool.QueryRow(r.Context(),
		`SELECT COUNT(*)
		 FROM challenge_progress WHERE challenge_id = $1`, challengeID,
	).Scan(&currentParticipants)
	if err != nil {
		handler.WriteError(w, http.StatusInternalServerError, "INTERNAL_ERROR", "failed to get challenge stats")
		return
	}

	resp := map[string]any{
		"id":                 id,
		"title":              title,
		"description":        description,
		"rules":              rules,
		"badgeName":          badgeName,
		"badgeIconUrl":       badgeIcon,
		"eligibility":        eligibility,
		"startDate":          startDate,
		"endDate":            endDate,
		"status":             status,
		"currentParticipants": currentParticipants,
		"createdAt":          createdAt,
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(resp)
}

type leaderboardEntry struct {
	UserID   string  `json:"userId"`
	UserName string  `json:"userName"`
	Rank     int     `json:"rank"`
	Status   string  `json:"status"`
	BadgeAwardedAt *string `json:"badgeAwardedAt,omitempty"`
}

func (h *ChallengeAdminHandler) Leaderboard(w http.ResponseWriter, r *http.Request) {
	challengeID := r.PathValue("id")
	rows, err := h.pool.Query(r.Context(),
		`WITH ranked AS (
			SELECT cp.user_id, cp.status, cp.badge_awarded_at,
				ROW_NUMBER() OVER (ORDER BY cp.badge_awarded_at ASC NULLS LAST, cp.created_at ASC) AS rank
			FROM challenge_progress cp
			WHERE cp.challenge_id = $1 AND cp.status = 'approved'
		)
		SELECT r.user_id, COALESCE(up.display_name, 'Anonymous'), r.rank, r.status, r.badge_awarded_at::text
		FROM ranked r
		LEFT JOIN user_profiles up ON up.user_id = r.user_id
		ORDER BY r.rank`, challengeID)
	if err != nil {
		handler.WriteError(w, http.StatusInternalServerError, "INTERNAL_ERROR", "failed to get leaderboard")
		return
	}
	defer rows.Close()

	var items []leaderboardEntry
	for rows.Next() {
		var i leaderboardEntry
		rows.Scan(&i.UserID, &i.UserName, &i.Rank, &i.Status, &i.BadgeAwardedAt)
		items = append(items, i)
	}
	if err := rows.Err(); err != nil {
		handler.WriteError(w, http.StatusInternalServerError, "INTERNAL_ERROR", "failed to iterate leaderboard")
		return
	}
	if items == nil {
		items = []leaderboardEntry{}
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(items)
}

func (h *ChallengeAdminHandler) ListEvidence(w http.ResponseWriter, r *http.Request) {
	rows, err := h.pool.Query(r.Context(),
		`SELECT cp.id, cp.user_id, cp.challenge_id, cp.status, cp.moderated_by, cp.moderation_note, cp.badge_awarded_at::text, cp.created_at::text,
			ch.title as challenge_title,
			COALESCE(up.display_name, 'Anonymous') as user_name
		 FROM challenge_progress cp
		 LEFT JOIN challenges ch ON ch.id = cp.challenge_id
		 LEFT JOIN user_profiles up ON up.user_id = cp.user_id
		 WHERE cp.status = 'submitted'
		 ORDER BY cp.created_at DESC LIMIT 50`)
	if err != nil {
		handler.WriteError(w, http.StatusInternalServerError, "INTERNAL_ERROR", "failed to list evidence")
		return
	}
	defer rows.Close()

	type item struct {
		ID             string  `json:"id"`
		UserID         string  `json:"userId"`
		ChallengeID    string  `json:"challengeId"`
		ChallengeTitle string  `json:"challengeTitle"`
		UserName       string  `json:"userName"`
		Status         string  `json:"status"`
		ModeratedBy    *string `json:"moderatedBy,omitempty"`
		ModerationNote *string `json:"moderationNote,omitempty"`
		BadgeAwardedAt *string `json:"badgeAwardedAt,omitempty"`
		CreatedAt      string  `json:"createdAt"`
	}
	var items []item
	for rows.Next() {
		var i item
		rows.Scan(&i.ID, &i.UserID, &i.ChallengeID, &i.Status, &i.ModeratedBy, &i.ModerationNote, &i.BadgeAwardedAt, &i.CreatedAt, &i.ChallengeTitle, &i.UserName)
		items = append(items, i)
	}
	if items == nil {
		items = []item{}
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(model.Paginated[item]{Items: items, HasMore: false})
}

func (h *ChallengeAdminHandler) ReviewEvidence(w http.ResponseWriter, r *http.Request) {
	evidenceID := r.PathValue("id")
	var req struct {
		Action string `json:"action"`
		Note   string `json:"note"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		handler.WriteError(w, http.StatusBadRequest, "INVALID_REQUEST", "invalid request body")
		return
	}

	status := "rejected"
	if req.Action == "approve" {
		status = "approved"
	} else if req.Action == "reject" {
		status = "in_progress"
	} else {
		handler.WriteError(w, http.StatusBadRequest, "INVALID_REQUEST", "action must be approve or reject")
		return
	}

	var query string
	if status == "approved" {
		query = `UPDATE challenge_progress SET status = $2, moderated_by = $3, moderation_note = $4, badge_awarded_at = now(), updated_at = now() WHERE id = $1`
	} else {
		query = `UPDATE challenge_progress SET status = $2, moderated_by = $3, moderation_note = $4, updated_at = now() WHERE id = $1`
	}

	_, err := h.pool.Exec(r.Context(), query, evidenceID, status, middleware.UserID(r.Context()), req.Note)
	if err != nil {
		handler.WriteError(w, http.StatusInternalServerError, "INTERNAL_ERROR", "failed to review evidence")
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{"status": status})
}
