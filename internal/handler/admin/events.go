package admin

import (
	"encoding/json"
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

	var id pgtype.UUID
	id.Scan(eventID)
	queries := gen.New(h.pool)
	event, err := queries.UpdateEvent(r.Context(), &gen.UpdateEventParams{
		ID:          id,
		Title:       strVal(req.Title),
		Organizer:   "",
		Description: req.Description,
		Status:      strVal(req.Status),
	})
	if err != nil {
		handler.WriteError(w, http.StatusInternalServerError, "INTERNAL_ERROR", "failed to update event")
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(event)
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
