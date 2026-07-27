package admin

import (
	"encoding/json"
	"net/http"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/bradleygichuru/ytci-go/internal/handler"
	"github.com/bradleygichuru/ytci-go/internal/model"
)

type CourseHandler struct {
	pool *pgxpool.Pool
}

func NewCourseHandler(pool *pgxpool.Pool) *CourseHandler {
	return &CourseHandler{pool: pool}
}

func (h *CourseHandler) List(w http.ResponseWriter, r *http.Request) {
	rows, err := h.pool.Query(r.Context(),
		`SELECT id, title, difficulty, status, created_at FROM courses ORDER BY created_at DESC LIMIT 50`)
	if err != nil {
		handler.WriteError(w, http.StatusInternalServerError, "INTERNAL_ERROR", "failed to list courses")
		return
	}
	defer rows.Close()

	type item struct {
		ID         string `json:"id"`
		Title      string `json:"title"`
		Difficulty string `json:"difficulty"`
		Status     string `json:"status"`
		CreatedAt  string `json:"createdAt"`
	}
	var items []item
	for rows.Next() {
		var i item
		rows.Scan(&i.ID, &i.Title, &i.Difficulty, &i.Status, &i.CreatedAt)
		items = append(items, i)
	}
	if items == nil {
		items = []item{}
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(model.Paginated[item]{Items: items, HasMore: false})
}

type ChallengeAdminHandler struct {
	pool *pgxpool.Pool
}

func NewChallengeAdminHandler(pool *pgxpool.Pool) *ChallengeAdminHandler {
	return &ChallengeAdminHandler{pool: pool}
}

func (h *ChallengeAdminHandler) List(w http.ResponseWriter, r *http.Request) {
	rows, err := h.pool.Query(r.Context(),
		`SELECT id, title, badge_name, status, start_date, end_date FROM challenges ORDER BY created_at DESC LIMIT 50`)
	if err != nil {
		handler.WriteError(w, http.StatusInternalServerError, "INTERNAL_ERROR", "failed to list challenges")
		return
	}
	defer rows.Close()

	type item struct {
		ID       string  `json:"id"`
		Title    string  `json:"title"`
		Badge    *string `json:"badgeName,omitempty"`
		Status   string  `json:"status"`
		Start    *string `json:"startDate,omitempty"`
		End      *string `json:"endDate,omitempty"`
	}
	var items []item
	for rows.Next() {
		var i item
		rows.Scan(&i.ID, &i.Title, &i.Badge, &i.Status, &i.Start, &i.End)
		items = append(items, i)
	}
	if items == nil {
		items = []item{}
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(model.Paginated[item]{Items: items, HasMore: false})
}

type ConservationAdminHandler struct {
	pool *pgxpool.Pool
}

func NewConservationAdminHandler(pool *pgxpool.Pool) *ConservationAdminHandler {
	return &ConservationAdminHandler{pool: pool}
}

func (h *ConservationAdminHandler) List(w http.ResponseWriter, r *http.Request) {
	rows, err := h.pool.Query(r.Context(),
		`SELECT id, title, organizer, event_date, status, current_participants FROM conservation_activities ORDER BY created_at DESC LIMIT 50`)
	if err != nil {
		handler.WriteError(w, http.StatusInternalServerError, "INTERNAL_ERROR", "failed to list conservation activities")
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

type CampaignAdminHandler struct {
	pool *pgxpool.Pool
}

func NewCampaignAdminHandler(pool *pgxpool.Pool) *CampaignAdminHandler {
	return &CampaignAdminHandler{pool: pool}
}

func (h *CampaignAdminHandler) List(w http.ResponseWriter, r *http.Request) {
	rows, err := h.pool.Query(r.Context(),
		`SELECT id, title, type, status, start_date, end_date FROM campaigns ORDER BY created_at DESC LIMIT 50`)
	if err != nil {
		handler.WriteError(w, http.StatusInternalServerError, "INTERNAL_ERROR", "failed to list campaigns")
		return
	}
	defer rows.Close()

	type item struct {
		ID    string  `json:"id"`
		Title string  `json:"title"`
		Type  string  `json:"type"`
		Status string `json:"status"`
		Start *string `json:"startDate,omitempty"`
		End   *string `json:"endDate,omitempty"`
	}
	var items []item
	for rows.Next() {
		var i item
		rows.Scan(&i.ID, &i.Title, &i.Type, &i.Status, &i.Start, &i.End)
		items = append(items, i)
	}
	if items == nil {
		items = []item{}
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(model.Paginated[item]{Items: items, HasMore: false})
}

func (h *CampaignAdminHandler) UpdateStatus(w http.ResponseWriter, r *http.Request) {
	campaignID := r.PathValue("id")
	var req struct {
		Status string `json:"status"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		handler.WriteError(w, http.StatusBadRequest, "INVALID_REQUEST", "invalid request body")
		return
	}

	_, err := h.pool.Exec(r.Context(),
		`UPDATE campaigns SET status = $2, updated_at = now() WHERE id = $1`, campaignID, req.Status)
	if err != nil {
		handler.WriteError(w, http.StatusInternalServerError, "INTERNAL_ERROR", "failed to update campaign")
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{"status": req.Status})
}
