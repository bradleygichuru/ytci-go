package expo

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/bradleygichuru/ytci-go/internal/collab"
	"github.com/bradleygichuru/ytci-go/internal/handler"
	"github.com/bradleygichuru/ytci-go/internal/middleware"
)

const (
	defaultHistoryLimit = 30
	maxHistoryLimit     = 100
)

type historyActor struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

type itineraryHistoryEvent struct {
	ID         string          `json:"id"`
	Scope      string          `json:"scope"`
	Action     string          `json:"action"`
	Summary    string          `json:"summary"`
	EntityType string          `json:"entityType"`
	EntityID   string          `json:"entityId"`
	Actor      historyActor    `json:"actor"`
	Before     json.RawMessage `json:"before"`
	After      json.RawMessage `json:"after"`
	RevisionID string          `json:"revisionId"`
	CreatedAt  string          `json:"createdAt"`
}

type itineraryHistoryResponse struct {
	Events     []itineraryHistoryEvent `json:"events"`
	NextBefore string                  `json:"nextBefore,omitempty"`
}

func (h *ItinerariesHandler) ListHistory(w http.ResponseWriter, r *http.Request) {
	itineraryID := r.PathValue("id")
	if itineraryID == "" {
		handler.WriteError(w, http.StatusBadRequest, "INVALID_ID", "itinerary id is required")
		return
	}

	userID := middleware.UserID(r.Context())
	if _, err := requireItineraryRole(r.Context(), h.pool, itineraryID, userID, roleViewer); err != nil {
		writeItineraryAccessError(w, r, err)
		return
	}

	query := r.URL.Query()
	scope := strings.TrimSpace(query.Get("scope"))
	switch scope {
	case "", "plan", "budget", "members":
	default:
		handler.WriteError(w, http.StatusBadRequest, "INVALID_SCOPE", "scope must be plan, budget, or members")
		return
	}

	limit := defaultHistoryLimit
	if raw := query.Get("limit"); raw != "" {
		parsed, err := strconv.Atoi(raw)
		if err != nil || parsed <= 0 {
			handler.WriteError(w, http.StatusBadRequest, "INVALID_LIMIT", "limit must be a positive integer")
			return
		}
		if parsed > maxHistoryLimit {
			parsed = maxHistoryLimit
		}
		limit = parsed
	}

	var before *time.Time
	if raw := query.Get("before"); raw != "" {
		parsed, err := time.Parse(time.RFC3339, raw)
		if err != nil {
			handler.WriteError(w, http.StatusBadRequest, "INVALID_BEFORE", "before must be an RFC3339 timestamp")
			return
		}
		before = &parsed
	}

	args := []any{itineraryID}
	where := []string{"e.itinerary_id = $1"}
	if scope != "" {
		args = append(args, scope)
		where = append(where, fmt.Sprintf("e.scope = $%d", len(args)))
	}
	if query.Get("actor") == "me" {
		args = append(args, userID)
		where = append(where, fmt.Sprintf("e.actor_id = $%d", len(args)))
	}
	if before != nil {
		args = append(args, *before)
		where = append(where, fmt.Sprintf("e.created_at < $%d", len(args)))
	}
	args = append(args, limit+1)

	stmt := `SELECT e.id::text, e.scope, e.action, COALESCE(e.summary, ''),
	               COALESCE(e.entity_type, ''), COALESCE(e.entity_id, ''),
	               COALESCE(e.actor_id, ''), COALESCE(u.name, 'Former member'),
	               e.before_json, e.after_json, COALESCE(e.revision_id::text, ''),
	               e.created_at
	        FROM trip_events e
	        LEFT JOIN users u ON u.id = e.actor_id
	        WHERE ` + strings.Join(where, " AND ") + `
	        ORDER BY e.created_at DESC, e.id DESC
	        LIMIT $` + strconv.Itoa(len(args))

	rows, err := h.pool.Query(r.Context(), stmt, args...)
	if err != nil {
		handler.WriteServerError(w, r, "INTERNAL_ERROR", "failed to load trip history", err)
		return
	}
	defer rows.Close()

	events := make([]itineraryHistoryEvent, 0, limit)
	for rows.Next() {
		var (
			event     itineraryHistoryEvent
			beforeRaw []byte
			afterRaw  []byte
			createdAt time.Time
		)
		if err := rows.Scan(&event.ID, &event.Scope, &event.Action, &event.Summary,
			&event.EntityType, &event.EntityID, &event.Actor.ID, &event.Actor.Name,
			&beforeRaw, &afterRaw, &event.RevisionID, &createdAt); err != nil {
			handler.WriteServerError(w, r, "INTERNAL_ERROR", "failed to read trip history", err)
			return
		}
		if len(beforeRaw) > 0 {
			event.Before = json.RawMessage(beforeRaw)
		}
		if len(afterRaw) > 0 {
			event.After = json.RawMessage(afterRaw)
		}
		event.CreatedAt = createdAt.UTC().Format(time.RFC3339Nano)
		events = append(events, event)
	}
	if err := rows.Err(); err != nil {
		handler.WriteServerError(w, r, "INTERNAL_ERROR", "failed to iterate trip history", err)
		return
	}

	resp := itineraryHistoryResponse{Events: events}
	if len(events) > limit {
		resp.Events = events[:limit]
		resp.NextBefore = resp.Events[limit-1].CreatedAt
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(resp)
}

func (h *ItinerariesHandler) RestoreRevision(w http.ResponseWriter, r *http.Request) {
	itineraryID := r.PathValue("id")
	if itineraryID == "" {
		handler.WriteError(w, http.StatusBadRequest, "INVALID_ID", "itinerary id is required")
		return
	}
	sourceRevisionID := r.PathValue("revisionId")
	if sourceRevisionID == "" {
		handler.WriteError(w, http.StatusNotFound, "NOT_FOUND", "revision not found")
		return
	}
	var revisionUUID pgtype.UUID
	if err := revisionUUID.Scan(sourceRevisionID); err != nil {
		handler.WriteError(w, http.StatusNotFound, "NOT_FOUND", "revision not found")
		return
	}

	var req struct {
		ExpectedVersion int `json:"expectedVersion,omitempty"`
	}
	if r.ContentLength > 0 {
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			handler.WriteError(w, http.StatusBadRequest, "INVALID_REQUEST", "invalid request body")
			return
		}
	}

	userID := middleware.UserID(r.Context())
	tx, err := h.pool.Begin(r.Context())
	if err != nil {
		handler.WriteServerError(w, r, "INTERNAL_ERROR", "failed to restore revision", err)
		return
	}
	defer tx.Rollback(r.Context())

	if _, err := requireItineraryRole(r.Context(), tx, itineraryID, userID, roleEditor); err != nil {
		writeItineraryAccessError(w, r, err)
		return
	}

	var currentVersion int
	err = tx.QueryRow(r.Context(),
		`SELECT plan_version FROM itineraries WHERE id = $1 AND deleted_at IS NULL FOR UPDATE`,
		itineraryID).Scan(&currentVersion)
	if errors.Is(err, pgx.ErrNoRows) {
		handler.WriteError(w, http.StatusNotFound, "NOT_FOUND", "itinerary not found")
		return
	}
	if err != nil {
		handler.WriteServerError(w, r, "INTERNAL_ERROR", "failed to lock itinerary", err)
		return
	}
	if req.ExpectedVersion > 0 && req.ExpectedVersion != currentVersion {
		tx.Rollback(r.Context())
		writeVersionConflictFor(w, "plan", currentVersion)
		return
	}

	var snapshotVersion int
	var snapshotJSON []byte
	err = tx.QueryRow(r.Context(),
		`SELECT version, snapshot::text FROM plan_revisions WHERE id = $1 AND itinerary_id = $2`,
		sourceRevisionID, itineraryID).Scan(&snapshotVersion, &snapshotJSON)
	if errors.Is(err, pgx.ErrNoRows) {
		handler.WriteError(w, http.StatusNotFound, "NOT_FOUND", "revision not found")
		return
	}
	if err != nil {
		handler.WriteServerError(w, r, "INTERNAL_ERROR", "failed to load revision", err)
		return
	}

	var snapshot planSnapshot
	if err := json.Unmarshal(snapshotJSON, &snapshot); err != nil {
		handler.WriteServerError(w, r, "INTERNAL_ERROR", "failed to decode revision snapshot", err)
		return
	}
	if len(snapshot.Inputs) == 0 {
		snapshot.Inputs = json.RawMessage(`{}`)
	}

	if _, err := tx.Exec(r.Context(),
		`UPDATE itineraries
		 SET title = $2, status = $3, inputs = $4::jsonb, updated_at = now()
		 WHERE id = $1`,
		itineraryID, snapshot.Title, snapshot.Status, string(snapshot.Inputs)); err != nil {
		handler.WriteServerError(w, r, "INTERNAL_ERROR", "failed to restore itinerary", err)
		return
	}

	if _, err := tx.Exec(r.Context(),
		`DELETE FROM itinerary_stops WHERE itinerary_id = $1`, itineraryID); err != nil {
		handler.WriteServerError(w, r, "INTERNAL_ERROR", "failed to clear stops", err)
		return
	}
	for _, stop := range snapshot.Stops {
		_, err := tx.Exec(r.Context(),
			`INSERT INTO itinerary_stops
			 (id, itinerary_id, destination_id, day, display_order, title, description,
			  start_time, category, image_url, estimated_cost)
			 VALUES (COALESCE(NULLIF($2, '')::uuid, gen_random_uuid()), $1, NULLIF($3, '')::uuid,
			         $4, $5, $6, $7, NULLIF($8, ''), NULLIF($9, ''), NULLIF($10, ''), $11)`,
			itineraryID, stop.ID, stop.DestinationID, stop.Day, stop.DisplayOrder,
			stop.Title, stop.Description, stop.StartTime, stop.Category, stop.ImageURL,
			stop.EstimatedCost)
		if err != nil {
			handler.WriteServerError(w, r, "INTERNAL_ERROR", "failed to restore stops", err)
			return
		}
	}

	newVersion, newRevisionID, err := bumpPlanVersionAndRevision(r.Context(), tx, itineraryID, userID, "restore")
	if err != nil {
		handler.WriteServerError(w, r, "INTERNAL_ERROR", "failed to snapshot restored trip", err)
		return
	}
	if err := recordTripEvent(r.Context(), tx, itineraryID, userID, "plan", "restored",
		"itinerary", itineraryID,
		fmt.Sprintf("Restored the plan to version %d", snapshotVersion),
		map[string]any{"version": currentVersion}, map[string]any{"version": newVersion},
		newRevisionID); err != nil {
		handler.WriteServerError(w, r, "INTERNAL_ERROR", "failed to record trip event", err)
		return
	}

	if err := tx.Commit(r.Context()); err != nil {
		handler.WriteServerError(w, r, "INTERNAL_ERROR", "failed to commit restore", err)
		return
	}
	_ = collab.PublishChange(r.Context(), h.pool, itineraryID, "plan", "restored", newVersion, userID)

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{"status": "restored", "version": newVersion})
}
