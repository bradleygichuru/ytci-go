package expo

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/bradleygichuru/ytci-go/internal/handler"
	"github.com/bradleygichuru/ytci-go/internal/middleware"
)

type AlertsHandler struct {
	pool *pgxpool.Pool
}

func NewAlertsHandler(pool *pgxpool.Pool) *AlertsHandler {
	return &AlertsHandler{pool: pool}
}

type alertJSON struct {
	ID            string     `json:"id"`
	SourceID      string     `json:"sourceId"`
	SourceName    string     `json:"sourceName"`
	Event         string     `json:"event"`
	Category      string     `json:"category"`
	Severity      string     `json:"severity"`
	Urgency       string     `json:"urgency"`
	Certainty     string     `json:"certainty"`
	Status        string     `json:"status"`
	Headline      string     `json:"headline"`
	Description   string     `json:"description,omitempty"`
	Instruction   string     `json:"instruction,omitempty"`
	WebURL        string     `json:"webUrl,omitempty"`
	SenderName    string     `json:"senderName"`
	SentAt        time.Time  `json:"sentAt"`
	OnsetAt       *time.Time `json:"onsetAt,omitempty"`
	ExpiresAt     *time.Time `json:"expiresAt,omitempty"`
	Counties      []string   `json:"counties"`
	CountyGids    []int      `json:"countyGids"`
	MappingReview bool       `json:"mappingReview"`
	ReadAt        *time.Time `json:"readAt,omitempty"`
	IsTest        bool       `json:"isTest"`
}

type alertDetailJSON struct {
	alertJSON
	Updates []alertUpdateJSON `json:"updates"`
}

type alertUpdateJSON struct {
	Version      int        `json:"version"`
	MsgType      string     `json:"msgType"`
	Changed      []string   `json:"changedFields"`
	SourceSentAt *time.Time `json:"sourceSentAt,omitempty"`
	ReceivedAt   time.Time  `json:"receivedAt"`
}

func (h *AlertsHandler) List(w http.ResponseWriter, r *http.Request) {
	userID := middleware.UserID(r.Context())
	countyGid := parseOptionalInt(r.URL.Query().Get("countyGid"))

	home, watch := h.userScope(r, userID)
	rows, err := h.pool.Query(r.Context(), `
		SELECT a.id, a.source_id, COALESCE(s.name,''), a.event, a.category, a.severity,
		       a.urgency, a.certainty, a.status, COALESCE(a.headline,''), COALESCE(a.description,''),
		       COALESCE(a.instruction,''), COALESCE(a.web_url,''), COALESCE(a.sender_name,''),
		       a.sent_at, a.onset_at, a.expires_at, a.mapping_review, a.is_test,
		       COALESCE(ARRAY(SELECT c.adm1_name FROM alert_areas ar JOIN kenya_counties c ON c.gid=ar.county_gid WHERE ar.alert_id=a.id), '{}'),
		       COALESCE(ARRAY(SELECT ar.county_gid FROM alert_areas ar WHERE ar.alert_id=a.id), '{}'),
		       rs.read_at
		FROM alerts a
		LEFT JOIN alert_sources s ON s.id=a.source_id
		LEFT JOIN alert_read_state rs ON rs.alert_id=a.id AND rs.user_id=$1
		WHERE a.is_public=true AND a.is_test=false
		  AND (a.status='active'
		       OR (a.status IN ('expired','cancelled') AND a.sent_at > now() - interval '30 days'))
		  AND ($2::int IS NULL OR NOT EXISTS (SELECT 1 FROM alert_areas x WHERE x.alert_id=a.id)
		       OR EXISTS (SELECT 1 FROM alert_areas x WHERE x.alert_id=a.id AND x.county_gid=$2))
		  AND ($1='' OR ($3::int IS NULL AND cardinality($4::int[]) = 0)
		       OR NOT EXISTS (SELECT 1 FROM alert_areas x WHERE x.alert_id=a.id)
		       OR ($3::int IS NOT NULL AND EXISTS (SELECT 1 FROM alert_areas x WHERE x.alert_id=a.id AND x.county_gid=$3))
		       OR EXISTS (SELECT 1 FROM alert_areas x WHERE x.alert_id=a.id AND x.county_gid = ANY($4::int[])))
		ORDER BY (a.status='active') DESC, a.sent_at DESC LIMIT 100`, userID, countyGid, home, watch)
	if err != nil {
		handler.WriteServerError(w, r, "INTERNAL_ERROR", "failed to list alerts", err)
		return
	}
	defer rows.Close()

	items := make([]alertJSON, 0)
	for rows.Next() {
		item, err := scanAlert(rows)
		if err != nil {
			handler.WriteServerError(w, r, "INTERNAL_ERROR", "failed to read alert", err)
			return
		}
		items = append(items, item)
	}
	if err := rows.Err(); err != nil {
		handler.WriteServerError(w, r, "INTERNAL_ERROR", "failed to list alerts", err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"items": items, "hasMore": false, "sourceHealth": h.sourceHealth(r),
	})
}

func (h *AlertsHandler) sourceHealth(r *http.Request) []map[string]any {
	rows, err := h.pool.Query(r.Context(), `
		SELECT id, name, poll_interval_seconds, last_checked_at, last_success_at, consecutive_failures
		FROM alert_sources WHERE enabled = true ORDER BY id`)
	if err != nil {
		return []map[string]any{}
	}
	defer rows.Close()
	out := make([]map[string]any, 0)
	for rows.Next() {
		var id, name string
		var interval, failures int
		var checked, success pgtype.Timestamptz
		if err := rows.Scan(&id, &name, &interval, &checked, &success, &failures); err != nil {
			continue
		}
		out = append(out, map[string]any{
			"id": id, "name": name, "state": healthState(interval, success, failures),
			"lastCheckedAt": timePtrString(checked), "lastSuccessAt": timePtrString(success),
		})
	}
	return out
}

func healthState(intervalSeconds int, lastSuccess pgtype.Timestamptz, failures int) string {
	if !lastSuccess.Valid {
		return "unknown"
	}
	if intervalSeconds <= 0 || failures >= 3 {
		return "unavailable"
	}
	age := time.Since(lastSuccess.Time)
	if age > time.Duration(intervalSeconds)*6*time.Second {
		return "unavailable"
	}
	if failures > 0 || age > time.Duration(intervalSeconds)*2*time.Second {
		return "degraded"
	}
	return "ok"
}

func (h *AlertsHandler) Get(w http.ResponseWriter, r *http.Request) {
	userID := middleware.UserID(r.Context())
	id := r.PathValue("id")
	row := h.pool.QueryRow(r.Context(), `
		SELECT a.id, a.source_id, COALESCE(s.name,''), a.event, a.category, a.severity,
		       a.urgency, a.certainty, a.status, COALESCE(a.headline,''), COALESCE(a.description,''),
		       COALESCE(a.instruction,''), COALESCE(a.web_url,''), COALESCE(a.sender_name,''),
		       a.sent_at, a.onset_at, a.expires_at, a.mapping_review, a.is_test,
		       COALESCE(ARRAY(SELECT c.adm1_name FROM alert_areas ar JOIN kenya_counties c ON c.gid=ar.county_gid WHERE ar.alert_id=a.id), '{}'),
		       COALESCE(ARRAY(SELECT ar.county_gid FROM alert_areas ar WHERE ar.alert_id=a.id), '{}'),
		       rs.read_at
		FROM alerts a
		LEFT JOIN alert_sources s ON s.id=a.source_id
		LEFT JOIN alert_read_state rs ON rs.alert_id=a.id AND rs.user_id=$2
		WHERE a.id=$1 AND a.is_public=true AND a.is_test=false`, id, userID)
	item, err := scanAlert(row)
	if err != nil {
		if err == pgx.ErrNoRows {
			handler.WriteError(w, http.StatusNotFound, "NOT_FOUND", "alert not found")
			return
		}
		handler.WriteServerError(w, r, "INTERNAL_ERROR", "failed to load alert", err)
		return
	}
	detail := alertDetailJSON{alertJSON: item}
	rows, err := h.pool.Query(r.Context(), `
		SELECT version, msg_type, changed_fields, source_sent_at, received_at
		FROM alert_updates WHERE alert_id=$1 ORDER BY version DESC`, id)
	if err == nil {
		for rows.Next() {
			var u alertUpdateJSON
			var sourceSent, received pgtype.Timestamptz
			if err := rows.Scan(&u.Version, &u.MsgType, &u.Changed, &sourceSent, &received); err == nil {
				if sourceSent.Valid {
					t := sourceSent.Time
					u.SourceSentAt = &t
				}
				u.ReceivedAt = received.Time
				detail.Updates = append(detail.Updates, u)
			}
		}
		rows.Close()
	}
	writeJSON(w, http.StatusOK, detail)
}

func (h *AlertsHandler) MarkRead(w http.ResponseWriter, r *http.Request) {
	h.readState(w, r, true)
}

func (h *AlertsHandler) Dismiss(w http.ResponseWriter, r *http.Request) {
	h.readState(w, r, false)
}

func (h *AlertsHandler) readState(w http.ResponseWriter, r *http.Request, read bool) {
	userID := middleware.UserID(r.Context())
	id := r.PathValue("id")
	var field string
	if read {
		field = "read_at"
	} else {
		field = "dismissed_at"
	}
	query := `INSERT INTO alert_read_state (user_id, alert_id, ` + field + `)
		VALUES ($1,$2,now()) ON CONFLICT (user_id, alert_id) DO UPDATE SET ` + field + `=now()`
	if _, err := h.pool.Exec(r.Context(), query, userID, id); err != nil {
		handler.WriteServerError(w, r, "INTERNAL_ERROR", "failed to update alert state", err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (h *AlertsHandler) GetPreferences(w http.ResponseWriter, r *http.Request) {
	userID := middleware.UserID(r.Context())
	prefs := defaultPreferences()
	row := h.pool.QueryRow(r.Context(), `
		SELECT enabled, os_permission_ok, min_severity, categories, program_important_only,
		       quiet_hours_enabled, quiet_hours_start::text, quiet_hours_end::text, timezone,
		       critical_override, home_county_gid, watch_county_gids, follow_current_location,
		       follow_itineraries, follow_saved_destinations, language
		FROM alert_preferences WHERE user_id=$1`, userID)
	var home *int32
	var watch []int32
	err := row.Scan(&prefs.Enabled, &prefs.OSPermissionOK, &prefs.MinSeverity, &prefs.Categories, &prefs.ProgramImportantOnly, &prefs.QuietHoursEnabled,
		&prefs.QuietHoursStart, &prefs.QuietHoursEnd, &prefs.Timezone, &prefs.CriticalOverride,
		&home, &watch, &prefs.FollowCurrentLocation, &prefs.FollowItineraries,
		&prefs.FollowSavedDestinations, &prefs.Language)
	if err != nil && err != pgx.ErrNoRows {
		handler.WriteServerError(w, r, "INTERNAL_ERROR", "failed to load preferences", err)
		return
	}
	if home != nil {
		v := int(*home)
		prefs.HomeCountyGid = &v
	}
	prefs.WatchCountyGids = int32sToInts(watch)
	writeJSON(w, http.StatusOK, prefs)
}

type preferencesJSON struct {
	Enabled                 bool     `json:"enabled"`
	OSPermissionOK          bool     `json:"osPermissionOk"`
	MinSeverity             string   `json:"minSeverity"`
	Categories              []string `json:"categories"`
	ProgramImportantOnly    bool     `json:"programImportantOnly"`
	QuietHoursEnabled       bool     `json:"quietHoursEnabled"`
	QuietHoursStart         string   `json:"quietHoursStart"`
	QuietHoursEnd           string   `json:"quietHoursEnd"`
	Timezone                string   `json:"timezone"`
	CriticalOverride        bool     `json:"criticalOverride"`
	HomeCountyGid           *int     `json:"homeCountyGid,omitempty"`
	WatchCountyGids         []int    `json:"watchCountyGids"`
	FollowCurrentLocation   bool     `json:"followCurrentLocation"`
	FollowItineraries       bool     `json:"followItineraries"`
	FollowSavedDestinations bool     `json:"followSavedDestinations"`
	Language                string   `json:"language"`
}

func defaultPreferences() preferencesJSON {
	return preferencesJSON{
		Enabled: true, OSPermissionOK: true, MinSeverity: "Severe", Categories: []string{"Met", "Safety", "Security"},
		ProgramImportantOnly: true, QuietHoursEnabled: true, QuietHoursStart: "22:00", QuietHoursEnd: "06:00",
		Timezone: "Africa/Nairobi", CriticalOverride: true, FollowCurrentLocation: true,
		WatchCountyGids: []int{}, Language: "en",
	}
}

type preferencesPatch struct {
	Enabled                 *bool     `json:"enabled"`
	OSPermissionOK          *bool     `json:"osPermissionOk"`
	MinSeverity             *string   `json:"minSeverity"`
	Categories              *[]string `json:"categories"`
	ProgramImportantOnly    *bool     `json:"programImportantOnly"`
	QuietHoursEnabled       *bool     `json:"quietHoursEnabled"`
	QuietHoursStart         *string   `json:"quietHoursStart"`
	QuietHoursEnd           *string   `json:"quietHoursEnd"`
	Timezone                *string   `json:"timezone"`
	CriticalOverride        *bool     `json:"criticalOverride"`
	HomeCountyGid           *int      `json:"homeCountyGid"`
	WatchCountyGids         *[]int    `json:"watchCountyGids"`
	FollowCurrentLocation   *bool     `json:"followCurrentLocation"`
	FollowItineraries       *bool     `json:"followItineraries"`
	FollowSavedDestinations *bool     `json:"followSavedDestinations"`
	Language                *string   `json:"language"`
}

func (h *AlertsHandler) UpdatePreferences(w http.ResponseWriter, r *http.Request) {
	userID := middleware.UserID(r.Context())
	var p preferencesPatch
	if err := json.NewDecoder(r.Body).Decode(&p); err != nil {
		handler.WriteError(w, http.StatusBadRequest, "INVALID_REQUEST", "invalid request body")
		return
	}
	if p.WatchCountyGids != nil && len(*p.WatchCountyGids) > 5 {
		handler.WriteError(w, http.StatusBadRequest, "WATCHLIST_LIMIT", "watchlist supports at most 5 counties")
		return
	}
	if p.MinSeverity != nil && !validSeverity(*p.MinSeverity) {
		handler.WriteError(w, http.StatusBadRequest, "INVALID_SEVERITY", "invalid minimum severity")
		return
	}
	var categories any
	if p.Categories != nil {
		categories = *p.Categories
	}
	var watch any
	if p.WatchCountyGids != nil {
		watch = toInt32s(*p.WatchCountyGids)
	}

	_, err := h.pool.Exec(r.Context(), `
		INSERT INTO alert_preferences (
		  user_id, enabled, os_permission_ok, min_severity, categories, program_important_only,
		  quiet_hours_enabled, quiet_hours_start, quiet_hours_end, timezone, critical_override,
		  home_county_gid, watch_county_gids, follow_current_location, follow_itineraries,
		  follow_saved_destinations, language
		) VALUES ($1, COALESCE($2,true), COALESCE($3,true), COALESCE($4,'Severe'), COALESCE($5::text[],ARRAY['Met','Safety','Security']),
		  COALESCE($6,true), COALESCE($7,true), COALESCE($8::time,'22:00'), COALESCE($9::time,'06:00'),
		  COALESCE($10,'Africa/Nairobi'), COALESCE($11,true), $12, COALESCE($13::int[],'{}'),
		  COALESCE($14,true), COALESCE($15,false), COALESCE($16,false), COALESCE($17,'en'))
		ON CONFLICT (user_id) DO UPDATE SET
		  enabled=COALESCE($2, alert_preferences.enabled),
		  os_permission_ok=COALESCE($3, alert_preferences.os_permission_ok),
		  min_severity=COALESCE($4, alert_preferences.min_severity),
		  categories=COALESCE($5::text[], alert_preferences.categories),
		  program_important_only=COALESCE($6, alert_preferences.program_important_only),
		  quiet_hours_enabled=COALESCE($7, alert_preferences.quiet_hours_enabled),
		  quiet_hours_start=COALESCE($8::time, alert_preferences.quiet_hours_start),
		  quiet_hours_end=COALESCE($9::time, alert_preferences.quiet_hours_end),
		  timezone=COALESCE($10, alert_preferences.timezone),
		  critical_override=COALESCE($11, alert_preferences.critical_override),
		  home_county_gid=COALESCE($12, alert_preferences.home_county_gid),
		  watch_county_gids=COALESCE($13::int[], alert_preferences.watch_county_gids),
		  follow_current_location=COALESCE($14, alert_preferences.follow_current_location),
		  follow_itineraries=COALESCE($15, alert_preferences.follow_itineraries),
		  follow_saved_destinations=COALESCE($16, alert_preferences.follow_saved_destinations),
		  language=COALESCE($17, alert_preferences.language), updated_at=now()`,
		userID, p.Enabled, p.OSPermissionOK, p.MinSeverity, categories, p.ProgramImportantOnly,
		p.QuietHoursEnabled, p.QuietHoursStart, p.QuietHoursEnd, p.Timezone, p.CriticalOverride,
		p.HomeCountyGid, watch, p.FollowCurrentLocation, p.FollowItineraries,
		p.FollowSavedDestinations, p.Language)
	if err != nil {
		handler.WriteServerError(w, r, "INTERNAL_ERROR", "failed to save preferences", err)
		return
	}
	h.GetPreferences(w, r)
}

func (h *AlertsHandler) UpdateLocation(w http.ResponseWriter, r *http.Request) {
	userID := middleware.UserID(r.Context())
	var req struct {
		CountyGid int `json:"countyGid"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.CountyGid <= 0 {
		handler.WriteError(w, http.StatusBadRequest, "INVALID_REQUEST", "countyGid is required")
		return
	}
	_, err := h.pool.Exec(r.Context(), `
		INSERT INTO user_alert_location (user_id, county_gid) VALUES ($1,$2)
		ON CONFLICT (user_id) DO UPDATE SET county_gid=EXCLUDED.county_gid, updated_at=now()`, userID, req.CountyGid)
	if err != nil {
		handler.WriteServerError(w, r, "INTERNAL_ERROR", "failed to save location", err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (h *AlertsHandler) ListProgramUpdates(w http.ResponseWriter, r *http.Request) {
	userID := middleware.UserID(r.Context())
	home, watch := h.userScope(r, userID)
	rows, err := h.pool.Query(r.Context(), `
		SELECT p.id, p.title, p.body, p.priority, p.county_gids, p.image_url, p.link_url,
		       p.published_at, p.expires_at, rs.read_at
		FROM program_updates p
		LEFT JOIN program_update_read_state rs ON rs.program_update_id=p.id AND rs.user_id=$1
		WHERE p.status='published' AND (p.published_at IS NULL OR p.published_at <= now())
		  AND (p.expires_at IS NULL OR p.expires_at > now())
		  AND (cardinality(p.county_gids) = 0
		       OR ($2::int IS NOT NULL AND p.county_gids @> ARRAY[$2::int])
		       OR p.county_gids && $3::int[])
		ORDER BY p.published_at DESC NULLS LAST LIMIT 100`, userID, home, watch)
	if err != nil {
		handler.WriteServerError(w, r, "INTERNAL_ERROR", "failed to list program updates", err)
		return
	}
	defer rows.Close()
	items := make([]map[string]any, 0)
	for rows.Next() {
		var id, title, body, priority string
		var counties []int32
		var image, link *string
		var published, expires, read pgtype.Timestamptz
		if err := rows.Scan(&id, &title, &body, &priority, &counties, &image, &link, &published, &expires, &read); err != nil {
			handler.WriteServerError(w, r, "INTERNAL_ERROR", "failed to read program update", err)
			return
		}
		items = append(items, map[string]any{
			"id": id, "title": title, "body": body, "priority": priority,
			"countyGids": int32sToInts(counties), "imageUrl": image, "linkUrl": link,
			"publishedAt": timePtrString(published), "expiresAt": timePtrString(expires), "readAt": timePtrString(read),
		})
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items, "hasMore": false})
}

func (h *AlertsHandler) MarkProgramUpdateRead(w http.ResponseWriter, r *http.Request) {
	userID := middleware.UserID(r.Context())
	id := r.PathValue("id")
	if _, err := h.pool.Exec(r.Context(), `
		INSERT INTO program_update_read_state (user_id, program_update_id, read_at)
		VALUES ($1,$2,now()) ON CONFLICT (user_id, program_update_id) DO UPDATE SET read_at=now()`, userID, id); err != nil {
		handler.WriteServerError(w, r, "INTERNAL_ERROR", "failed to mark program update read", err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (h *AlertsHandler) userScope(r *http.Request, userID string) (*int, []int) {
	if userID == "" {
		return nil, nil
	}
	var home *int32
	var watch []int32
	_ = h.pool.QueryRow(r.Context(), `SELECT home_county_gid, watch_county_gids FROM alert_preferences WHERE user_id=$1`, userID).Scan(&home, &watch)
	if home == nil && len(watch) == 0 {
		return nil, nil
	}
	var hout *int
	if home != nil {
		v := int(*home)
		hout = &v
	}
	return hout, int32sToInts(watch)
}

func scanAlert(row pgx.Row) (alertJSON, error) {
	var a alertJSON
	var countyGids []int32
	var sent, onset, expires, read pgtype.Timestamptz
	err := row.Scan(&a.ID, &a.SourceID, &a.SourceName, &a.Event, &a.Category, &a.Severity,
		&a.Urgency, &a.Certainty, &a.Status, &a.Headline, &a.Description, &a.Instruction,
		&a.WebURL, &a.SenderName, &sent, &onset, &expires, &a.MappingReview, &a.IsTest,
		&a.Counties, &countyGids, &read)
	a.SentAt = sent.Time
	if onset.Valid {
		t := onset.Time
		a.OnsetAt = &t
	}
	if expires.Valid {
		t := expires.Time
		a.ExpiresAt = &t
	}
	if read.Valid {
		t := read.Time
		a.ReadAt = &t
	}
	a.CountyGids = int32sToInts(countyGids)
	if a.Counties == nil {
		a.Counties = []string{}
	}
	return a, err
}

func parseOptionalInt(v string) *int {
	if v == "" {
		return nil
	}
	var n int
	if _, err := fmt.Sscanf(v, "%d", &n); err != nil {
		return nil
	}
	return &n
}

func timePtrString(t pgtype.Timestamptz) any {
	if !t.Valid {
		return nil
	}
	return t.Time.Format(time.RFC3339)
}

func toInt32s(in []int) []int32 {
	out := make([]int32, 0, len(in))
	for _, v := range in {
		out = append(out, int32(v))
	}
	return out
}

func int32sToInts(in []int32) []int {
	out := make([]int, 0, len(in))
	for _, v := range in {
		out = append(out, int(v))
	}
	return out
}

func validSeverity(v string) bool {
	switch strings.Title(strings.ToLower(v)) {
	case "Extreme", "Severe", "Moderate", "Minor":
		return true
	default:
		return false
	}
}
