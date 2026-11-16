package admin

import (
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/bradleygichuru/ytci-go/internal/handler"
	"github.com/bradleygichuru/ytci-go/internal/middleware"
	"github.com/bradleygichuru/ytci-go/internal/push"
)

type ProgramUpdateHandler struct {
	pool   *pgxpool.Pool
	client *push.Client
}

func NewProgramUpdateHandler(pool *pgxpool.Pool, client *push.Client) *ProgramUpdateHandler {
	return &ProgramUpdateHandler{pool: pool, client: client}
}

type programUpdateRequest struct {
	Title       string     `json:"title"`
	Body        string     `json:"body"`
	Priority    string     `json:"priority"`
	CountyGids  []int      `json:"countyGids"`
	Categories  []string   `json:"categories"`
	ImageURL    *string    `json:"imageUrl"`
	LinkURL     *string    `json:"linkUrl"`
	PublishedAt *time.Time `json:"publishedAt"`
	ExpiresAt   *time.Time `json:"expiresAt"`
}

func (h *ProgramUpdateHandler) Create(w http.ResponseWriter, r *http.Request) {
	var req programUpdateRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.Title == "" || req.Body == "" {
		handler.WriteError(w, http.StatusBadRequest, "INVALID_REQUEST", "title and body are required")
		return
	}
	if req.Priority == "" {
		req.Priority = "Normal"
	}
	if req.Categories == nil {
		req.Categories = []string{}
	}
	if req.CountyGids == nil {
		req.CountyGids = []int{}
	}
	if req.Priority != "Normal" && req.Priority != "Important" {
		handler.WriteError(w, http.StatusBadRequest, "INVALID_PRIORITY", "priority must be Normal or Important")
		return
	}
	if len(req.CountyGids) > 0 {
		// Let the FK reject invalid county gids, but bound the payload.
		if len(req.CountyGids) > 47 {
			handler.WriteError(w, http.StatusBadRequest, "INVALID_COUNTIES", "too many counties")
			return
		}
	}
	var id string
	err := h.pool.QueryRow(r.Context(), `
		INSERT INTO program_updates (title, body, priority, status, county_gids, categories, image_url, link_url, published_at, expires_at, created_by)
		VALUES ($1,$2,$3,'draft',$4,$5,$6,$7,$8,$9,$10) RETURNING id`,
		req.Title, req.Body, req.Priority, intsToInt32(req.CountyGids), req.Categories,
		req.ImageURL, req.LinkURL, req.PublishedAt, req.ExpiresAt, middleware.UserID(r.Context())).Scan(&id)
	if err != nil {
		handler.WriteServerError(w, r, "INTERNAL_ERROR", "failed to create program update", err)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	json.NewEncoder(w).Encode(map[string]any{"id": id, "status": "draft"})
}

func (h *ProgramUpdateHandler) List(w http.ResponseWriter, r *http.Request) {
	status := r.URL.Query().Get("status")
	rows, err := h.pool.Query(r.Context(), `
		SELECT id, title, body, priority, status, county_gids, categories, image_url, link_url,
		       published_at, expires_at, created_at, updated_at
		FROM program_updates
		WHERE ($1 = '' OR status=$1)
		ORDER BY created_at DESC LIMIT 200`, status)
	if err != nil {
		handler.WriteServerError(w, r, "INTERNAL_ERROR", "failed to list program updates", err)
		return
	}
	defer rows.Close()
	items := make([]map[string]any, 0)
	for rows.Next() {
		var id, title, body, priority, status string
		var counties []int32
		var categories []string
		var image, link *string
		var published, expires, created, updated pgtype.Timestamptz
		if err := rows.Scan(&id, &title, &body, &priority, &status, &counties, &categories,
			&image, &link, &published, &expires, &created, &updated); err != nil {
			handler.WriteServerError(w, r, "INTERNAL_ERROR", "failed to read program update", err)
			return
		}
		items = append(items, map[string]any{
			"id": id, "title": title, "body": body, "priority": priority, "status": status,
			"countyGids": int32sToInts(counties), "categories": categories,
			"imageUrl": image, "linkUrl": link, "publishedAt": adminTime(published), "expiresAt": adminTime(expires),
			"createdAt": adminTime(created), "updatedAt": adminTime(updated),
		})
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]any{"items": items, "hasMore": false})
}

func (h *ProgramUpdateHandler) Publish(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	var previous string
	err := h.pool.QueryRow(r.Context(),
		`SELECT status FROM program_updates WHERE id=$1`, id).Scan(&previous)
	if errors.Is(err, pgx.ErrNoRows) {
		handler.WriteError(w, http.StatusNotFound, "NOT_FOUND", "program update not found")
		return
	}
	if err != nil {
		handler.WriteServerError(w, r, "INTERNAL_ERROR", "failed to load program update", err)
		return
	}
	if previous != "draft" && previous != "scheduled" {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"id": id, "status": previous, "sent": 0})
		return
	}

	var update push.ProgramPush
	var counties []int32
	var linkURL string
	var expires pgtype.Timestamptz
	err = h.pool.QueryRow(r.Context(), `
		UPDATE program_updates
		SET status='published', published_at=COALESCE(published_at, now()), updated_at=now()
		WHERE id=$1 AND status IN ('draft','scheduled')
		RETURNING title, body, priority, county_gids,
		          COALESCE(image_url,''), COALESCE(link_url,''), expires_at`, id).Scan(
		&update.Title, &update.Body, &update.Priority, &counties,
		&update.ImageURL, &linkURL, &expires)
	if err != nil {
		handler.WriteServerError(w, r, "INTERNAL_ERROR", "failed to publish program update", err)
		return
	}
	update.ID = id
	update.CountyGIDs = int32sToInts(counties)
	if expires.Valid {
		t := expires.Time
		update.ExpiresAt = &t
	}
	update.Route = "/alerts/program/" + id
	sent, sendErr := push.SendProgramUpdate(r.Context(), h.pool, h.client, update)
	if sendErr != nil {
		_, _ = h.pool.Exec(r.Context(),
			`UPDATE program_updates SET status=$2, updated_at=now() WHERE id=$1`, id, previous)
		handler.WriteServerError(w, r, "INTERNAL_ERROR", "failed to send program update", sendErr)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{"id": id, "status": "published", "sent": sent})
}

func (h *ProgramUpdateHandler) Expire(w http.ResponseWriter, r *http.Request) {
	h.setStatus(w, r, "expired", "")
}

func (h *ProgramUpdateHandler) Health(w http.ResponseWriter, r *http.Request) {
	rows, err := h.pool.Query(r.Context(), `
		SELECT id, name, last_checked_at, last_success_at, consecutive_failures,
		       last_http_status, last_error, push_enabled
		FROM alert_sources ORDER BY id`)
	if err != nil {
		handler.WriteServerError(w, r, "INTERNAL_ERROR", "failed to load source health", err)
		return
	}
	defer rows.Close()
	sources := make([]map[string]any, 0)
	for rows.Next() {
		var id, name string
		var checked, success pgtype.Timestamptz
		var failures int
		var httpStatus *int
		var lastError *string
		var pushEnabled bool
		if err := rows.Scan(&id, &name, &checked, &success, &failures, &httpStatus, &lastError, &pushEnabled); err != nil {
			handler.WriteServerError(w, r, "INTERNAL_ERROR", "failed to read source health", err)
			return
		}
		sources = append(sources, map[string]any{
			"id": id, "name": name, "lastCheckedAt": adminTime(checked),
			"lastSuccessAt": adminTime(success), "consecutiveFailures": failures,
			"lastHttpStatus": httpStatus, "lastError": lastError, "pushEnabled": pushEnabled,
		})
	}

	var worker pgtype.Timestamptz
	if err := h.pool.QueryRow(r.Context(), `SELECT last_seen_at FROM alert_worker_heartbeat WHERE worker='alerts-worker'`).Scan(&worker); err != nil && err != pgx.ErrNoRows {
		slog.Warn("alerts: failed to read worker heartbeat", "error", err)
	}
	var pending, running, dead, queued, sent, failed int
	if err := h.pool.QueryRow(r.Context(), `SELECT
		count(*) FILTER (WHERE status='pending'), count(*) FILTER (WHERE status='running'),
		count(*) FILTER (WHERE status='dead') FROM alert_fanout_jobs`).Scan(&pending, &running, &dead); err != nil {
		slog.Warn("alerts: failed to read job health", "error", err)
	}
	if err := h.pool.QueryRow(r.Context(), `SELECT
		count(*) FILTER (WHERE status='queued'), count(*) FILTER (WHERE status='sent'),
		count(*) FILTER (WHERE status='failed') FROM alert_deliveries`).Scan(&queued, &sent, &failed); err != nil {
		slog.Warn("alerts: failed to read delivery health", "error", err)
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]any{
		"sources":          sources,
		"workerLastSeenAt": adminTime(worker),
		"jobs":             map[string]int{"pending": pending, "running": running, "dead": dead},
		"deliveries":       map[string]int{"queued": queued, "sent": sent, "failed": failed},
	})
}

func (h *ProgramUpdateHandler) setStatus(w http.ResponseWriter, r *http.Request, status, extraSet string) {
	id := r.PathValue("id")
	query := `UPDATE program_updates SET status=$2, ` + extraSet + `updated_at=now() WHERE id=$1`
	tag, err := h.pool.Exec(r.Context(), query, id, status)
	if err != nil {
		handler.WriteServerError(w, r, "INTERNAL_ERROR", "failed to update program update", err)
		return
	}
	if tag.RowsAffected() == 0 {
		handler.WriteError(w, http.StatusNotFound, "NOT_FOUND", "program update not found")
		return
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{"id": id, "status": status})
}

func adminTime(t pgtype.Timestamptz) any {
	if !t.Valid {
		return nil
	}
	return t.Time.Format(time.RFC3339)
}

func intsToInt32(in []int) []int32 {
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
