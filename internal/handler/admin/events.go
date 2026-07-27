package admin

import (
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/bradleygichuru/ytci-go/internal/db/gen"
	"github.com/bradleygichuru/ytci-go/internal/handler"
	"github.com/bradleygichuru/ytci-go/internal/middleware"
	"github.com/bradleygichuru/ytci-go/internal/pagination"
)

type EventsHandler struct {
	pool *pgxpool.Pool
}

func NewEventsHandler(pool *pgxpool.Pool) *EventsHandler {
	return &EventsHandler{pool: pool}
}

func (h *EventsHandler) List(w http.ResponseWriter, r *http.Request) {
	queries := gen.New(h.pool)
	pg := &pagination.CursorPaginator[gen.Event]{}

	pg.WritePage(w, r,
		func(limit int32) ([]gen.Event, error) {
			return queries.ListEvents(r.Context(), limit)
		},
		func(limit int32, sortValue, id string) ([]gen.Event, error) {
			var d pgtype.Date
			var uid pgtype.UUID
			d.Scan(sortValue)
			uid.Scan(id)
			return queries.ListEventsAfter(r.Context(), &gen.ListEventsAfterParams{
				EventDate: d,
				ID:        uid,
				Limit:     limit,
			})
		},
		func(e gen.Event) (string, bool) {
			d := e.EventDate.Time.Format(time.RFC3339Nano)
			return pagination.EncodeCursor(d, pagination.UUIDString(e.ID.Bytes)), true
		},
	)
}

func (h *EventsHandler) Create(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Title     string  `json:"title"`
		Organizer string  `json:"organizer"`
		County    string  `json:"county"`
		EventDate string  `json:"eventDate"`
		Type      string  `json:"type"`
		Venue     string  `json:"venue,omitempty"`
		Desc      string  `json:"description,omitempty"`
		Contact   string  `json:"contactEmail,omitempty"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		handler.WriteError(w, http.StatusBadRequest, "INVALID_REQUEST", "invalid request body")
		return
	}

	var userUUID pgtype.UUID
	userUUID.Scan(middleware.UserID(r.Context()))

	queries := gen.New(h.pool)
	event, err := queries.CreateEvent(r.Context(), &gen.CreateEventParams{
		Title:        req.Title,
		Organizer:    req.Organizer,
		County:       req.County,
		EventDate:    pgtype.Date{Time: parseDate(req.EventDate), Valid: true},
		Type:         req.Type,
		Venue:        strPtr(req.Venue),
		Description:  strPtr(req.Desc),
		ContactEmail: strPtr(req.Contact),
		CreatedBy:    userUUID,
	})
	if err != nil {
		handler.WriteError(w, http.StatusInternalServerError, "INTERNAL_ERROR", "failed to create event")
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	json.NewEncoder(w).Encode(event)
}

func (h *EventsHandler) Update(w http.ResponseWriter, r *http.Request) {
	eventID := r.PathValue("id")
	var req struct {
		Title       *string `json:"title,omitempty"`
		Description *string `json:"description,omitempty"`
		Status      *string `json:"status,omitempty"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		handler.WriteError(w, http.StatusBadRequest, "INVALID_REQUEST", "invalid request body")
		return
	}

	var title, organizer, status string
	if req.Title != nil {
		title = *req.Title
	}
	if req.Status != nil {
		status = *req.Status
	}

	row := h.pool.QueryRow(r.Context(),
		`UPDATE events SET
		 title = CASE WHEN $2::text != '' THEN $2 ELSE title END,
		 organizer = CASE WHEN $3::text != '' THEN $3 ELSE organizer END,
		 description = COALESCE($4::text, description),
		 status = CASE WHEN $5::text != '' THEN $5 ELSE status END,
		 updated_at = now()
		 WHERE id = $1
		 RETURNING *`,
		eventID, title, organizer, req.Description, status)

	var e gen.Event
	err := row.Scan(&e.ID, &e.Title, &e.Organizer, &e.County, &e.Venue, &e.EventDate, &e.EndDate, &e.Type, &e.Status, &e.Description, &e.ContactEmail, &e.ContactPhone, &e.ImageUrl, &e.ReminderEnabled, &e.ReminderMinutes, &e.CreatedBy, &e.CreatedAt, &e.UpdatedAt)
	if err != nil {
		handler.WriteError(w, http.StatusInternalServerError, "INTERNAL_ERROR", "failed to update event")
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(e)
}

func (h *EventsHandler) Delete(w http.ResponseWriter, r *http.Request) {
	eventID := r.PathValue("id")
	var id pgtype.UUID
	id.Scan(eventID)

	queries := gen.New(h.pool)
	_, err := queries.DeleteEvent(r.Context(), id)
	if err != nil {
		handler.WriteError(w, http.StatusInternalServerError, "INTERNAL_ERROR", "failed to cancel event")
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{"status": "cancelled"})
}

func (h *EventsHandler) Get(w http.ResponseWriter, r *http.Request) {
	eventID := r.PathValue("id")
	queries := gen.New(h.pool)
	event, err := queries.GetEventByID(r.Context(), pgtype.UUID{})
	event.ID.Scan(eventID)
	event, err = queries.GetEventByID(r.Context(), event.ID)
	if err != nil {
		handler.WriteError(w, http.StatusNotFound, "NOT_FOUND", "event not found")
		return
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(event)
}

func (h *EventsHandler) UpdateStatus(w http.ResponseWriter, r *http.Request) {
	eventID := r.PathValue("id")
	var req struct{ Status string `json:"status"` }
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		handler.WriteError(w, http.StatusBadRequest, "INVALID_REQUEST", "invalid request body")
		return
	}

	var currentStatus string
	if err := h.pool.QueryRow(r.Context(), `SELECT status FROM events WHERE id = $1`, eventID).Scan(&currentStatus); err != nil {
		handler.WriteError(w, http.StatusNotFound, "NOT_FOUND", "event not found")
		return
	}

	validTransitions := map[string]map[string]bool{
		"scheduled": {"postponed": true},
		"postponed": {"scheduled": true, "cancelled": true},
		"cancelled": {},
	}

	transitions, ok := validTransitions[currentStatus]
	if !ok || !transitions[req.Status] {
		handler.WriteError(w, http.StatusBadRequest, "INVALID_TRANSITION",
			fmt.Sprintf("cannot transition from %s to %s", currentStatus, req.Status))
		return
	}

	h.pool.Exec(r.Context(),
		`UPDATE events SET status = $2, updated_at = now() WHERE id = $1`, eventID, req.Status)
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{"status": req.Status})
}

func parseDate(s string) time.Time {
	t, err := time.Parse("2006-01-02", s)
	if err != nil {
		return time.Now()
	}
	return t
}

func strPtr(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}

func strVal(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}
