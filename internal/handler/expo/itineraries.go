package expo

import (
	"encoding/json"
	"errors"
	"net/http"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/bradleygichuru/ytci-go/internal/handler"
	"github.com/bradleygichuru/ytci-go/internal/middleware"
	"github.com/bradleygichuru/ytci-go/internal/model"
)

type ItinerariesHandler struct {
	pool *pgxpool.Pool
}

func NewItinerariesHandler(pool *pgxpool.Pool) *ItinerariesHandler {
	return &ItinerariesHandler{pool: pool}
}

func (h *ItinerariesHandler) List(w http.ResponseWriter, r *http.Request) {
	userID := middleware.UserID(r.Context())
	rows, err := h.pool.Query(r.Context(),
		`SELECT i.id::text, i.title, i.status, i.created_at, m.role
		 FROM itineraries i
		 JOIN itinerary_members m ON m.itinerary_id = i.id
		 WHERE m.user_id = $1 AND i.deleted_at IS NULL
		 ORDER BY i.created_at DESC`,
		userID)
	if err != nil {
		handler.WriteServerError(w, r, "INTERNAL_ERROR", "failed to list itineraries", err)
		return
	}
	defer rows.Close()

	type item struct {
		ID        string `json:"id"`
		Title     string `json:"title"`
		Status    string `json:"status"`
		Role      string `json:"role"`
		CreatedAt string `json:"createdAt"`
	}
	var items []item
	for rows.Next() {
		var i item
		var createdAt pgtype.Timestamp
		if err := rows.Scan(&i.ID, &i.Title, &i.Status, &createdAt, &i.Role); err != nil {
			handler.WriteServerError(w, r, "INTERNAL_ERROR", "failed to read itinerary", err)
			return
		}
		i.CreatedAt = createdAt.Time.Format(time.RFC3339)
		items = append(items, i)
	}
	if err := rows.Err(); err != nil {
		handler.WriteServerError(w, r, "INTERNAL_ERROR", "failed to iterate itineraries", err)
		return
	}
	if items == nil {
		items = []item{}
	}

	resp := model.Paginated[item]{Items: items, HasMore: false}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(resp)
}

func (h *ItinerariesHandler) Create(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Title      string      `json:"title"`
		Origin     string      `json:"origin,omitempty"`
		Days       int         `json:"days,omitempty"`
		BudgetBand string      `json:"budgetBand,omitempty"`
		Interests  []string    `json:"interests,omitempty"`
		Stops      []StopInput `json:"stops,omitempty"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		handler.WriteError(w, http.StatusBadRequest, "INVALID_REQUEST", "invalid request body")
		return
	}

	userID := middleware.UserID(r.Context())

	inputsRaw, err := json.Marshal(map[string]any{
		"origin": req.Origin, "days": req.Days,
		"budgetBand": req.BudgetBand, "interests": req.Interests,
	})
	if err != nil {
		handler.WriteServerError(w, r, "INTERNAL_ERROR", "failed to serialize inputs", err)
		return
	}

	tx, err := h.pool.Begin(r.Context())
	if err != nil {
		handler.WriteServerError(w, r, "INTERNAL_ERROR", "failed to create itinerary", err)
		return
	}
	defer tx.Rollback(r.Context())

	var itineraryID string
	err = tx.QueryRow(r.Context(),
		`INSERT INTO itineraries (user_id, title, inputs) VALUES ($1, $2, $3::jsonb) RETURNING id`,
		userID, req.Title, string(inputsRaw)).Scan(&itineraryID)
	if err != nil {
		handler.WriteServerError(w, r, "INTERNAL_ERROR", "failed to create itinerary", err)
		return
	}

	if err := AddMember(r.Context(), tx, itineraryID, userID, roleOwner, ""); err != nil {
		handler.WriteServerError(w, r, "INTERNAL_ERROR", "failed to add trip owner", err)
		return
	}

	for _, s := range req.Stops {
		_, err = tx.Exec(r.Context(),
			`INSERT INTO itinerary_stops (itinerary_id, destination_id, day, display_order, title, description, start_time, category, image_url, estimated_cost)
			 VALUES ($1, NULLIF($2, '')::uuid, $3, $4, $5, $6, NULLIF($7, ''), NULLIF($8, ''), NULLIF($9, ''), $10)`,
			itineraryID, s.DestinationID, s.Day, s.DisplayOrder, s.Title, s.Description,
			s.StartTime, s.Category, s.ImageURL, s.EstimatedCost)
		if err != nil {
			handler.WriteServerError(w, r, "INTERNAL_ERROR", "failed to create stops", err)
			return
		}
	}

	revisionID, err := insertPlanRevision(r.Context(), tx, itineraryID, 1, userID, "create")
	if err != nil {
		handler.WriteServerError(w, r, "INTERNAL_ERROR", "failed to snapshot trip", err)
		return
	}
	if err := recordTripEvent(r.Context(), tx, itineraryID, userID, "plan", "created",
		"itinerary", itineraryID, "Created trip", nil,
		map[string]any{"title": req.Title, "stops": len(req.Stops)}, revisionID); err != nil {
		handler.WriteServerError(w, r, "INTERNAL_ERROR", "failed to record trip event", err)
		return
	}

	if err := tx.Commit(r.Context()); err != nil {
		handler.WriteServerError(w, r, "INTERNAL_ERROR", "failed to finalize", err)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	json.NewEncoder(w).Encode(map[string]any{"id": itineraryID, "status": "draft", "version": 1})
}

func (h *ItinerariesHandler) Get(w http.ResponseWriter, r *http.Request) {
	itineraryID := r.PathValue("id")
	if itineraryID == "" {
		handler.WriteError(w, http.StatusBadRequest, "INVALID_ID", "itinerary id is required")
		return
	}

	access, err := loadItineraryAccess(r.Context(), h.pool, itineraryID, middleware.UserID(r.Context()))
	if err != nil {
		writeItineraryAccessError(w, r, err)
		return
	}

	var title, status, inputs string
	err = h.pool.QueryRow(r.Context(),
		`SELECT title, status, inputs::text FROM itineraries WHERE id = $1`,
		itineraryID).Scan(&title, &status, &inputs)
	if errors.Is(err, pgx.ErrNoRows) {
		handler.WriteError(w, http.StatusNotFound, "NOT_FOUND", "itinerary not found")
		return
	}
	if err != nil {
		handler.WriteServerError(w, r, "INTERNAL_ERROR", "failed to load itinerary", err)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]any{
		"id": itineraryID, "title": title, "status": status, "inputs": inputs,
		"role": access.Role, "planVersion": access.PlanVersion,
	})
}

func (h *ItinerariesHandler) Update(w http.ResponseWriter, r *http.Request) {
	itineraryID := r.PathValue("id")
	if itineraryID == "" {
		handler.WriteError(w, http.StatusBadRequest, "INVALID_ID", "itinerary id is required")
		return
	}
	var req struct {
		Title           *string `json:"title,omitempty"`
		Status          *string `json:"status,omitempty"`
		ExpectedVersion int     `json:"expectedVersion,omitempty"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		handler.WriteError(w, http.StatusBadRequest, "INVALID_REQUEST", "invalid request body")
		return
	}
	if req.Title == nil && req.Status == nil {
		handler.WriteError(w, http.StatusBadRequest, "INVALID_REQUEST", "no changes supplied")
		return
	}

	userID := middleware.UserID(r.Context())
	tx, err := h.pool.Begin(r.Context())
	if err != nil {
		handler.WriteServerError(w, r, "INTERNAL_ERROR", "failed to update itinerary", err)
		return
	}
	defer tx.Rollback(r.Context())

	if _, err := requireItineraryRole(r.Context(), tx, itineraryID, userID, roleEditor); err != nil {
		writeItineraryAccessError(w, r, err)
		return
	}

	var currentVersion int
	var beforeTitle, beforeStatus string
	err = tx.QueryRow(r.Context(),
		`SELECT plan_version, title, status
		 FROM itineraries
		 WHERE id = $1 AND deleted_at IS NULL
		 FOR UPDATE`,
		itineraryID).Scan(&currentVersion, &beforeTitle, &beforeStatus)
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
		writeVersionConflict(w, currentVersion)
		return
	}

	var newVersion int
	err = tx.QueryRow(r.Context(),
		`UPDATE itineraries
		 SET title = COALESCE($2::text, title),
		     status = COALESCE($3::text, status),
		     plan_version = plan_version + 1,
		     updated_at = now()
		 WHERE id = $1
		 RETURNING plan_version`,
		itineraryID, req.Title, req.Status).Scan(&newVersion)
	if err != nil {
		handler.WriteServerError(w, r, "INTERNAL_ERROR", "failed to update itinerary", err)
		return
	}

	revisionID, err := insertPlanRevision(r.Context(), tx, itineraryID, newVersion, userID, "save")
	if err != nil {
		handler.WriteServerError(w, r, "INTERNAL_ERROR", "failed to snapshot trip", err)
		return
	}
	after := map[string]any{}
	if req.Title != nil {
		after["title"] = *req.Title
	}
	if req.Status != nil {
		after["status"] = *req.Status
	}
	if err := recordTripEvent(r.Context(), tx, itineraryID, userID, "plan", "updated",
		"itinerary", itineraryID, "Updated trip details",
		map[string]any{"title": beforeTitle, "status": beforeStatus}, after, revisionID); err != nil {
		handler.WriteServerError(w, r, "INTERNAL_ERROR", "failed to record trip event", err)
		return
	}

	if err := tx.Commit(r.Context()); err != nil {
		handler.WriteServerError(w, r, "INTERNAL_ERROR", "failed to commit update", err)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]any{"status": "updated", "version": newVersion})
}

func (h *ItinerariesHandler) Delete(w http.ResponseWriter, r *http.Request) {
	itineraryID := r.PathValue("id")
	if itineraryID == "" {
		handler.WriteError(w, http.StatusBadRequest, "INVALID_ID", "itinerary id is required")
		return
	}

	userID := middleware.UserID(r.Context())
	tx, err := h.pool.Begin(r.Context())
	if err != nil {
		handler.WriteServerError(w, r, "INTERNAL_ERROR", "failed to delete itinerary", err)
		return
	}
	defer tx.Rollback(r.Context())

	if _, err := requireItineraryRole(r.Context(), tx, itineraryID, userID, roleOwner); err != nil {
		writeItineraryAccessError(w, r, err)
		return
	}

	cmd, err := tx.Exec(r.Context(),
		`UPDATE itineraries
		 SET deleted_at = now(), members_version = members_version + 1, updated_at = now()
		 WHERE id = $1 AND deleted_at IS NULL`,
		itineraryID)
	if err != nil {
		handler.WriteServerError(w, r, "INTERNAL_ERROR", "failed to delete itinerary", err)
		return
	}
	if cmd.RowsAffected() == 0 {
		handler.WriteError(w, http.StatusNotFound, "NOT_FOUND", "itinerary not found")
		return
	}
	if err := recordTripEvent(r.Context(), tx, itineraryID, userID, "plan", "deleted",
		"itinerary", itineraryID, "Deleted trip", nil, nil, ""); err != nil {
		handler.WriteServerError(w, r, "INTERNAL_ERROR", "failed to record trip event", err)
		return
	}
	if err := tx.Commit(r.Context()); err != nil {
		handler.WriteServerError(w, r, "INTERNAL_ERROR", "failed to commit delete", err)
		return
	}

	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(map[string]string{"status": "deleted"})
}

func (h *ItinerariesHandler) Duplicate(w http.ResponseWriter, r *http.Request) {
	itineraryID := r.PathValue("id")
	if itineraryID == "" {
		handler.WriteError(w, http.StatusBadRequest, "INVALID_ID", "itinerary id is required")
		return
	}

	userID := middleware.UserID(r.Context())
	tx, err := h.pool.Begin(r.Context())
	if err != nil {
		handler.WriteServerError(w, r, "INTERNAL_ERROR", "failed to duplicate", err)
		return
	}
	defer tx.Rollback(r.Context())

	if _, err := requireItineraryRole(r.Context(), tx, itineraryID, userID, roleEditor); err != nil {
		writeItineraryAccessError(w, r, err)
		return
	}

	var title, inputs string
	if err := tx.QueryRow(r.Context(),
		`SELECT title, inputs::text FROM itineraries WHERE id = $1 AND deleted_at IS NULL`,
		itineraryID).Scan(&title, &inputs); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			handler.WriteError(w, http.StatusNotFound, "NOT_FOUND", "itinerary not found")
			return
		}
		handler.WriteServerError(w, r, "INTERNAL_ERROR", "failed to load itinerary", err)
		return
	}

	var newID string
	err = tx.QueryRow(r.Context(),
		`INSERT INTO itineraries (user_id, title, inputs, status, plan_version)
		 VALUES ($1, $2, $3::jsonb, 'draft', 1)
		 RETURNING id`,
		userID, title+" (copy)", inputs).Scan(&newID)
	if err != nil {
		handler.WriteServerError(w, r, "INTERNAL_ERROR", "failed to duplicate", err)
		return
	}

	if err := AddMember(r.Context(), tx, newID, userID, roleOwner, ""); err != nil {
		handler.WriteServerError(w, r, "INTERNAL_ERROR", "failed to add trip owner", err)
		return
	}

	_, err = tx.Exec(r.Context(),
		`INSERT INTO itinerary_stops (itinerary_id, destination_id, day, display_order, title, description, start_time, category, image_url, estimated_cost)
		 SELECT $1, destination_id, day, display_order, title, description, start_time, category, image_url, estimated_cost
		 FROM itinerary_stops WHERE itinerary_id = $2`,
		newID, itineraryID)
	if err != nil {
		handler.WriteServerError(w, r, "INTERNAL_ERROR", "failed to duplicate stops", err)
		return
	}

	revisionID, err := insertPlanRevision(r.Context(), tx, newID, 1, userID, "duplicate")
	if err != nil {
		handler.WriteServerError(w, r, "INTERNAL_ERROR", "failed to snapshot trip", err)
		return
	}
	if err := recordTripEvent(r.Context(), tx, newID, userID, "plan", "created",
		"itinerary", newID, "Duplicated trip", nil, map[string]any{"from": itineraryID}, revisionID); err != nil {
		handler.WriteServerError(w, r, "INTERNAL_ERROR", "failed to record trip event", err)
		return
	}

	if err := tx.Commit(r.Context()); err != nil {
		handler.WriteServerError(w, r, "INTERNAL_ERROR", "failed to finish duplicate", err)
		return
	}

	w.WriteHeader(http.StatusCreated)
	json.NewEncoder(w).Encode(map[string]any{"id": newID, "status": "copied", "version": 1})
}
