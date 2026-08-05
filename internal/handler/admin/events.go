package admin

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"time"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/bradleygichuru/ytci-go/internal/db/gen"
	"github.com/bradleygichuru/ytci-go/internal/handler"
	"github.com/bradleygichuru/ytci-go/internal/middleware"
	"github.com/bradleygichuru/ytci-go/internal/pagination"
	"github.com/bradleygichuru/ytci-go/internal/r2"
)

type EventsHandler struct {
	pool *pgxpool.Pool
	r2   r2.Store
}

func NewEventsHandler(pool *pgxpool.Pool, r2client r2.Store) *EventsHandler {
	return &EventsHandler{pool: pool, r2: r2client}
}

func (h *EventsHandler) presignImageURL(ctx context.Context, objectKey string) *string {
	if h.r2 == nil || objectKey == "" {
		return nil
	}
	u, err := h.r2.PresignedGetURL(ctx, objectKey, 15*time.Minute)
	if err != nil {
		slog.Warn("presign event image", "object_key", objectKey, "error", err)
		return nil
	}
	return &u
}

type adminEvent struct {
	ID                 pgtype.UUID      `json:"id"`
	Title              string           `json:"title"`
	Organizer          string           `json:"organizer"`
	County             string           `json:"county"`
	Venue              *string          `json:"venue"`
	EventDate          pgtype.Date      `json:"date"`
	EndDate            pgtype.Date      `json:"endDate"`
	Type               string           `json:"type"`
	Status             string           `json:"status"`
	Description        *string          `json:"description"`
	ContactEmail       *string          `json:"contactEmail"`
	ContactPhone       *string          `json:"contactPhone"`
	ImageUrl           *string          `json:"imageUrl"`
	ReminderEnabled    *bool            `json:"reminderEnabled"`
	ReminderMinutes    *int32           `json:"reminderMinutes"`
	CreatedBy          pgtype.UUID      `json:"-"`
	CreatedAt          pgtype.Timestamp `json:"createdAt"`
	UpdatedAt          pgtype.Timestamp `json:"updatedAt"`
	StartTime          *string          `json:"startTime"`
	EndTime            *string          `json:"endTime"`
	EntryFee           *string          `json:"entryFee"`
	LocationLat        pgtype.Numeric   `json:"locationLat"`
	LocationLng        pgtype.Numeric   `json:"locationLng"`
	OrganizerAvatarUrl *string          `json:"organizerAvatarUrl"`
}

func scanEvent(e *adminEvent) []any {
	return []any{
		&e.ID, &e.Title, &e.Organizer, &e.County, &e.Venue, &e.EventDate, &e.EndDate, &e.Type,
		&e.Status, &e.Description, &e.ContactEmail, &e.ContactPhone, &e.ImageUrl,
		&e.ReminderEnabled, &e.ReminderMinutes, &e.CreatedBy, &e.CreatedAt, &e.UpdatedAt,
		&e.StartTime, &e.EndTime, &e.EntryFee, &e.LocationLat, &e.LocationLng, &e.OrganizerAvatarUrl,
	}
}

func (h *EventsHandler) writeEvent(w http.ResponseWriter, r *http.Request, e adminEvent) {
	resp := make(map[string]any)
	out, _ := json.Marshal(e)
	json.Unmarshal(out, &resp)
	if e.ImageUrl != nil && *e.ImageUrl != "" {
		if p := h.presignImageURL(r.Context(), *e.ImageUrl); p != nil {
			resp["imageUrl"] = *p
		}
	}
	resp["imageUrlKey"] = e.ImageUrl
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(resp)
}

func (h *EventsHandler) ListMobile(w http.ResponseWriter, r *http.Request) {
	county := r.URL.Query().Get("county")
	eventType := r.URL.Query().Get("type")

	q := `SELECT id::text, title, organizer, county, venue, event_date::text, end_date::text, type,
	       description, contact_email, contact_phone, image_url, created_at::text,
	       start_time, end_time, entry_fee, location_lat, location_lng, organizer_avatar_url
	       FROM events WHERE status = 'scheduled'`

	if county != "" {
		q += fmt.Sprintf(" AND county = '%s'", county)
	}
	if eventType != "" {
		q += fmt.Sprintf(" AND type = '%s'", eventType)
	}

	q += " ORDER BY event_date ASC LIMIT $1"

	rows, err := h.pool.Query(r.Context(), q, 50)
	if err != nil {
		handler.WriteServerError(w, r, "INTERNAL_ERROR", "failed to list events", err)
		return
	}
	defer rows.Close()

	type mobileEvent struct {
		ID               string   `json:"id"`
		Title            string   `json:"title"`
		Organizer        string   `json:"organizer"`
		County           string   `json:"county"`
		Venue            *string  `json:"venue,omitempty"`
		EventDate        string   `json:"eventDate"`
		EndDate          *string  `json:"endDate,omitempty"`
		Type             string   `json:"type"`
		Description      *string  `json:"description,omitempty"`
		ContactEmail     *string  `json:"contactEmail,omitempty"`
		ContactPhone     *string  `json:"contactPhone,omitempty"`
		ImageURL         *string  `json:"imageUrl,omitempty"`
		CreatedAt        string   `json:"createdAt"`
		StartTime        *string  `json:"startTime,omitempty"`
		EndTime          *string  `json:"endTime,omitempty"`
		EntryFee         *string  `json:"entryFee,omitempty"`
		LocationLat      *float64 `json:"locationLat,omitempty"`
		LocationLng      *float64 `json:"locationLng,omitempty"`
		OrganizerAvatarl *string  `json:"organizerAvatarUrl,omitempty"`
	}

	var items []mobileEvent
	for rows.Next() {
		var e mobileEvent
		if err := rows.Scan(&e.ID, &e.Title, &e.Organizer, &e.County, &e.Venue, &e.EventDate, &e.EndDate, &e.Type,
			&e.Description, &e.ContactEmail, &e.ContactPhone, &e.ImageURL, &e.CreatedAt,
			&e.StartTime, &e.EndTime, &e.EntryFee, &e.LocationLat, &e.LocationLng, &e.OrganizerAvatarl); err != nil {
			handler.WriteServerError(w, r, "INTERNAL_ERROR", "failed to scan event", err)
			return
		}
		if e.ImageURL != nil && *e.ImageURL != "" {
			if p := h.presignImageURL(r.Context(), *e.ImageURL); p != nil {
				e.ImageURL = p
			}
		}
		items = append(items, e)
	}
	if items == nil {
		items = []mobileEvent{}
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(items)
}

func (h *EventsHandler) GetMobile(w http.ResponseWriter, r *http.Request) {
	eventID := r.PathValue("id")
	userID := middleware.UserID(r.Context())

	var (
		id, title, organizer, county, eventDate, etype, createdAt string
		endDate, description, contactEmail, contactPhone, imageURL *string
		venue, endTimeRaw, startTime, entryFee, organizerAvatarURL *string
		locationLat, locationLng                                   *float64
	)
	err := h.pool.QueryRow(r.Context(),
		`SELECT id::text, title, organizer, county, venue, event_date::text, end_date::text,
		        type, description, contact_email, contact_phone, image_url, created_at::text,
		        start_time, end_time, entry_fee, location_lat::text, location_lng::text, organizer_avatar_url
		 FROM events WHERE id = $1`, eventID).Scan(
		&id, &title, &organizer, &county, &venue, &eventDate, &endDate,
		&etype, &description, &contactEmail, &contactPhone, &imageURL, &createdAt,
		&startTime, &endTimeRaw, &entryFee, &locationLat, &locationLng, &organizerAvatarURL,
	)
	if err != nil {
		handler.WriteError(w, http.StatusNotFound, "NOT_FOUND", "event not found")
		return
	}

	presigned := h.presignImageURL(r.Context(), strVal(imageURL))

	hlRows, err := h.pool.Query(r.Context(),
		`SELECT label, icon FROM event_highlights WHERE event_id = $1 ORDER BY display_order`, eventID)
	if err != nil {
		handler.WriteServerError(w, r, "INTERNAL_ERROR", "failed to fetch highlights", err)
		return
	}
	defer hlRows.Close()

	type highlight struct {
		Label string  `json:"label"`
		Icon  *string `json:"icon,omitempty"`
	}
	var highlights []highlight
	for hlRows.Next() {
		var h highlight
		hlRows.Scan(&h.Label, &h.Icon)
		highlights = append(highlights, h)
	}

	var joinedCount, interestedCount int32
	h.pool.QueryRow(r.Context(),
		`SELECT count(*) FILTER (WHERE status = 'joined'),
		        count(*) FILTER (WHERE status = 'interested')
		 FROM event_attendees WHERE event_id = $1`, eventID).Scan(&joinedCount, &interestedCount)

	attRows, err := h.pool.Query(r.Context(),
		`SELECT ea.user_id, COALESCE(up.display_name, 'Anonymous') AS name
		 FROM event_attendees ea
		 LEFT JOIN user_profiles up ON up.user_id = ea.user_id
		 WHERE ea.event_id = $1 AND ea.status = 'joined'
		 ORDER BY ea.created_at ASC
		 LIMIT 20`, eventID)
	if err != nil {
		handler.WriteServerError(w, r, "INTERNAL_ERROR", "failed to fetch attendees", err)
		return
	}
	defer attRows.Close()

	type attendeeInfo struct {
		UserID string `json:"userId"`
		Name   string `json:"name"`
	}
	var attendees []attendeeInfo
	for attRows.Next() {
		var a attendeeInfo
		attRows.Scan(&a.UserID, &a.Name)
		attendees = append(attendees, a)
	}

	var attendeeStatus *string
	h.pool.QueryRow(r.Context(),
		`SELECT status FROM event_attendees WHERE event_id = $1 AND user_id = $2`, eventID, userID).Scan(&attendeeStatus)

	var isSaved bool
	h.pool.QueryRow(r.Context(),
		`SELECT EXISTS(SELECT 1 FROM event_saves WHERE event_id = $1 AND user_id = $2)`, eventID, userID).Scan(&isSaved)

	resp := map[string]any{
		"id":                 id,
		"title":              title,
		"organizer":          organizer,
		"county":             county,
		"venue":              venue,
		"eventDate":          eventDate,
		"endDate":            endDate,
		"type":               etype,
		"description":        description,
		"contactEmail":       contactEmail,
		"contactPhone":       contactPhone,
		"imageUrl":           presigned,
		"imageUrlKey":        imageURL,
		"createdAt":          createdAt,
		"startTime":          startTime,
		"endTime":            endTimeRaw,
		"entryFee":           entryFee,
		"locationLat":        locationLat,
		"locationLng":        locationLng,
		"organizerAvatarUrl": organizerAvatarURL,
		"highlights":         highlights,
		"attendeeCount":      joinedCount + interestedCount,
		"attendees":          attendees,
		"isAttending":        attendeeStatus,
		"isSaved":            isSaved,
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(resp)
}

func (h *EventsHandler) GetPublic(w http.ResponseWriter, r *http.Request) {
	eventID := r.PathValue("id")

	var (
		id, title, organizer, county, eventDate, etype, createdAt string
		endDate, description, contactEmail, contactPhone, imageURL *string
		venue, endTimeRaw, startTime, entryFee, organizerAvatarURL *string
		locationLat, locationLng                                   *float64
	)
	err := h.pool.QueryRow(r.Context(),
		`SELECT id::text, title, organizer, county, venue, event_date::text, end_date::text,
		        type, description, contact_email, contact_phone, image_url, created_at::text,
		        start_time, end_time, entry_fee, location_lat::text, location_lng::text, organizer_avatar_url
		 FROM events WHERE id = $1 AND status = 'scheduled'`, eventID).Scan(
		&id, &title, &organizer, &county, &venue, &eventDate, &endDate,
		&etype, &description, &contactEmail, &contactPhone, &imageURL, &createdAt,
		&startTime, &endTimeRaw, &entryFee, &locationLat, &locationLng, &organizerAvatarURL,
	)
	if err != nil {
		handler.WriteError(w, http.StatusNotFound, "NOT_FOUND", "event not found")
		return
	}

	presigned := h.presignImageURL(r.Context(), strVal(imageURL))

	hlRows, err := h.pool.Query(r.Context(),
		`SELECT label, icon FROM event_highlights WHERE event_id = $1 ORDER BY display_order`, eventID)
	if err != nil {
		handler.WriteServerError(w, r, "INTERNAL_ERROR", "failed to fetch highlights", err)
		return
	}
	defer hlRows.Close()

	type highlight struct {
		Label string  `json:"label"`
		Icon  *string `json:"icon,omitempty"`
	}
	var highlights []highlight
	for hlRows.Next() {
		var h highlight
		hlRows.Scan(&h.Label, &h.Icon)
		highlights = append(highlights, h)
	}

	var joinedCount, interestedCount int32
	h.pool.QueryRow(r.Context(),
		`SELECT count(*) FILTER (WHERE status = 'joined'),
		        count(*) FILTER (WHERE status = 'interested')
		 FROM event_attendees WHERE event_id = $1`, eventID).Scan(&joinedCount, &interestedCount)

	resp := map[string]any{
		"id":                 id,
		"title":              title,
		"organizer":          organizer,
		"county":             county,
		"venue":              venue,
		"eventDate":          eventDate,
		"endDate":            endDate,
		"type":               etype,
		"description":        description,
		"contactEmail":       contactEmail,
		"contactPhone":       contactPhone,
		"imageUrl":           presigned,
		"imageUrlKey":        imageURL,
		"createdAt":          createdAt,
		"startTime":          startTime,
		"endTime":            endTimeRaw,
		"entryFee":           entryFee,
		"locationLat":        locationLat,
		"locationLng":        locationLng,
		"organizerAvatarUrl": organizerAvatarURL,
		"highlights":         highlights,
		"attendeeCount":      joinedCount + interestedCount,
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(resp)
}

func (h *EventsHandler) List(w http.ResponseWriter, r *http.Request) {
	pr := pagination.ParseRequest(r)
	limit := int32(pr.Limit) + 1

	firstPage := func(lim int32) ([]adminEvent, error) {
		rows, err := h.pool.Query(r.Context(),
			`SELECT * FROM events ORDER BY event_date ASC LIMIT $1`, lim)
		if err != nil {
			return nil, err
		}
		defer rows.Close()
		var items []adminEvent
		for rows.Next() {
			var e adminEvent
			if err := rows.Scan(scanEvent(&e)...); err != nil {
				return nil, err
			}
			items = append(items, e)
		}
		return items, rows.Err()
	}

	afterPage := func(lim int32, sortValue, id string) ([]adminEvent, error) {
		var d pgtype.Date
		var uid pgtype.UUID
		d.Scan(sortValue)
		uid.Scan(id)
		rows, err := h.pool.Query(r.Context(),
			`SELECT * FROM events WHERE event_date > $1 OR (event_date = $1 AND id > $2) ORDER BY event_date ASC, id ASC LIMIT $3`,
			d, uid, lim)
		if err != nil {
			return nil, err
		}
		defer rows.Close()
		var items []adminEvent
		for rows.Next() {
			var e adminEvent
			if err := rows.Scan(scanEvent(&e)...); err != nil {
				return nil, err
			}
			items = append(items, e)
		}
		return items, rows.Err()
	}

	encodeCursor := func(e adminEvent) (string, bool) {
		d := e.EventDate.Time.Format(time.RFC3339Nano)
		return pagination.EncodeCursor(d, pagination.UUIDString(e.ID.Bytes)), true
	}

	var items []adminEvent
	var err error
	if pr.Cursor != nil {
		items, err = afterPage(limit, pr.Cursor.SortValue, pr.Cursor.ID)
	} else {
		items, err = firstPage(limit)
	}
	if err != nil {
		handler.WriteServerError(w, r, "INTERNAL_ERROR", "failed to list events", err)
		return
	}

	hasMore := len(items) > int(pr.Limit)
	result := items
	if hasMore {
		result = items[:pr.Limit]
	}

	out := make([]map[string]any, len(result))
	for i, e := range result {
		raw, _ := json.Marshal(e)
		var item map[string]any
		json.Unmarshal(raw, &item)
		if e.ImageUrl != nil && *e.ImageUrl != "" {
			if p := h.presignImageURL(r.Context(), *e.ImageUrl); p != nil {
				item["imageUrl"] = *p
			}
		}
		item["imageUrlKey"] = e.ImageUrl
		out[i] = item
	}

	resp := map[string]any{
		"items":   out,
		"hasMore": hasMore,
	}
	if hasMore && len(out) > 0 {
		last := result[len(result)-1]
		if cursor, ok := encodeCursor(last); ok {
			resp["nextCursor"] = cursor
		}
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(resp)
}

func (h *EventsHandler) Create(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Title           string `json:"title"`
		Organizer       string `json:"organizer"`
		County          string `json:"county"`
		EventDate       string `json:"date"`
		EndDate         string `json:"endDate,omitempty"`
		Type            string `json:"type"`
		Venue           string `json:"venue,omitempty"`
		Desc            string `json:"description,omitempty"`
		Contact         string `json:"contactEmail,omitempty"`
		ContactPhone    string `json:"contactPhone,omitempty"`
		ImageUrl        string `json:"imageUrl,omitempty"`
		ReminderEnabled bool   `json:"reminderEnabled"`
		ReminderMinutes *int32 `json:"reminderMinutes,omitempty"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		handler.WriteError(w, http.StatusBadRequest, "INVALID_REQUEST", "invalid request body")
		return
	}

	var endDate *time.Time
	if req.EndDate != "" {
		t := parseDate(req.EndDate)
		endDate = &t
	}
	var reminderMin *int32
	if req.ReminderEnabled {
		reminderMin = req.ReminderMinutes
	}
	var imgUrl *string
	if req.ImageUrl != "" {
		imgUrl = &req.ImageUrl
	}

	row := h.pool.QueryRow(r.Context(),
		`INSERT INTO events (title, organizer, county, venue, event_date, end_date, type, description, contact_email, contact_phone, image_url, reminder_enabled, reminder_minutes, created_by)
		 VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14) RETURNING *`,
		req.Title, req.Organizer, req.County, strPtr(req.Venue),
		parseDate(req.EventDate), endDate, req.Type, strPtr(req.Desc),
		strPtr(req.Contact), strPtr(req.ContactPhone), imgUrl,
		req.ReminderEnabled, reminderMin, middleware.UserID(r.Context()))

	var e adminEvent
	if err := row.Scan(scanEvent(&e)...); err != nil {
		handler.WriteServerError(w, r, "INTERNAL_ERROR", "failed to create event", err)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	h.writeEvent(w, r, e)
}

func (h *EventsHandler) Update(w http.ResponseWriter, r *http.Request) {
	eventID := r.PathValue("id")
	var req struct {
		Title           *string `json:"title,omitempty"`
		Organizer       *string `json:"organizer,omitempty"`
		County          *string `json:"county,omitempty"`
		Venue           *string `json:"venue,omitempty"`
		EventDate       *string `json:"date,omitempty"`
		EndDate         *string `json:"endDate,omitempty"`
		Type            *string `json:"type,omitempty"`
		Description     *string `json:"description,omitempty"`
		ContactEmail    *string `json:"contactEmail,omitempty"`
		ContactPhone    *string `json:"contactPhone,omitempty"`
		ImageUrl        *string `json:"imageUrl,omitempty"`
		Status          *string `json:"status,omitempty"`
		ReminderEnabled *bool   `json:"reminderEnabled,omitempty"`
		ReminderMinutes *int32  `json:"reminderMinutes,omitempty"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		handler.WriteError(w, http.StatusBadRequest, "INVALID_REQUEST", "invalid request body")
		return
	}

	sets := ""
	args := []any{eventID}
	argIdx := 2

	addStr := func(col string, val *string) {
		if val != nil {
			sets += fmt.Sprintf(", %s = $%d", col, argIdx)
			args = append(args, *val)
			argIdx++
		}
	}
	addBool := func(col string, val *bool) {
		if val != nil {
			sets += fmt.Sprintf(", %s = $%d", col, argIdx)
			args = append(args, *val)
			argIdx++
		}
	}
	addInt := func(col string, val *int32) {
		if val != nil {
			sets += fmt.Sprintf(", %s = $%d", col, argIdx)
			args = append(args, *val)
			argIdx++
		}
	}
	addDate := func(col string, val *string) {
		if val != nil && *val != "" {
			sets += fmt.Sprintf(", %s = $%d", col, argIdx)
			args = append(args, parseDate(*val))
			argIdx++
		}
	}

	addStr("title", req.Title)
	addStr("organizer", req.Organizer)
	addStr("county", req.County)
	addStr("venue", req.Venue)
	addDate("event_date", req.EventDate)
	addDate("end_date", req.EndDate)
	addStr("type", req.Type)
	addStr("description", req.Description)
	addStr("contact_email", req.ContactEmail)
	addStr("contact_phone", req.ContactPhone)
	addStr("image_url", req.ImageUrl)
	addStr("status", req.Status)
	addBool("reminder_enabled", req.ReminderEnabled)
	addInt("reminder_minutes", req.ReminderMinutes)

	if sets == "" {
		handler.WriteError(w, http.StatusBadRequest, "NO_CHANGES", "no fields to update")
		return
	}

	q := "UPDATE events SET updated_at = now()" + sets + " WHERE id = $1 RETURNING *"

	var e adminEvent
	err := h.pool.QueryRow(r.Context(), q, args...).Scan(scanEvent(&e)...)
	if err != nil {
		handler.WriteServerError(w, r, "INTERNAL_ERROR", "failed to update event", err)
		return
	}

	h.writeEvent(w, r, e)
}

func (h *EventsHandler) Delete(w http.ResponseWriter, r *http.Request) {
	eventID := r.PathValue("id")
	var id pgtype.UUID
	id.Scan(eventID)

	queries := gen.New(h.pool)
	_, err := queries.DeleteEvent(r.Context(), id)
	if err != nil {
		handler.WriteServerError(w, r, "INTERNAL_ERROR", "failed to cancel event", err)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{"status": "cancelled"})
}

func (h *EventsHandler) Get(w http.ResponseWriter, r *http.Request) {
	eventID := r.PathValue("id")
	row := h.pool.QueryRow(r.Context(), `SELECT * FROM events WHERE id = $1`, eventID)

	var e adminEvent
	if err := row.Scan(scanEvent(&e)...); err != nil {
		handler.WriteError(w, http.StatusNotFound, "NOT_FOUND", "event not found")
		return
	}

	h.writeEvent(w, r, e)
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

	if _, err := h.pool.Exec(r.Context(),
		`UPDATE events SET status = $2, updated_at = now() WHERE id = $1`, eventID, req.Status); err != nil {
		handler.WriteServerError(w, r, "INTERNAL_ERROR", "failed to update status", err)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{"status": req.Status})
}

func (h *EventsHandler) AddMedia(w http.ResponseWriter, r *http.Request) {
	eventID := r.PathValue("id")
	var req struct {
		HeroMediaID string `json:"heroMediaId,omitempty"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		handler.WriteError(w, http.StatusBadRequest, "INVALID_REQUEST", "invalid request body")
		return
	}

	if req.HeroMediaID != "" {
		tag, err := h.pool.Exec(r.Context(),
			`UPDATE media_assets SET entity_type = 'event', entity_id = $1 WHERE id = $2`,
			eventID, req.HeroMediaID)
		if err != nil {
			handler.WriteServerError(w, r, "INTERNAL_ERROR", "failed to link media", err)
			return
		}
		if tag.RowsAffected() == 0 {
			handler.WriteError(w, http.StatusNotFound, "NOT_FOUND", "media not found")
			return
		}
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	json.NewEncoder(w).Encode(map[string]string{"status": "linked"})
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
