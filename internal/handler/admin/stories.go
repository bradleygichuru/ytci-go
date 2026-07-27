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
	"github.com/bradleygichuru/ytci-go/internal/model"
	"github.com/bradleygichuru/ytci-go/internal/pagination"
)

type StoriesHandler struct {
	pool *pgxpool.Pool
}

func NewStoriesHandler(pool *pgxpool.Pool) *StoriesHandler {
	return &StoriesHandler{pool: pool}
}

func (h *StoriesHandler) List(w http.ResponseWriter, r *http.Request) {
	queries := gen.New(h.pool)
	pg := &pagination.CursorPaginator[gen.Story]{}

	pg.WritePage(w, r,
		func(limit int32) ([]gen.Story, error) {
			return queries.ListStories(r.Context(), limit)
		},
		func(limit int32, sortValue, id string) ([]gen.Story, error) {
			var ts pgtype.Timestamp
			var uid pgtype.UUID
			ts.Scan(sortValue)
			uid.Scan(id)
			return queries.ListStoriesAfter(r.Context(), &gen.ListStoriesAfterParams{
				CreatedAt: ts,
				ID:        uid,
				Limit:     limit,
			})
		},
		func(s gen.Story) (string, bool) {
			ts := s.CreatedAt.Time.UTC().Format(time.RFC3339Nano)
			return pagination.EncodeCursor(ts, pagination.UUIDString(s.ID.Bytes)), true
		},
	)
}

func (h *StoriesHandler) Moderate(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Action string `json:"action"`
		Reason string `json:"reason"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		handler.WriteError(w, http.StatusBadRequest, "INVALID_REQUEST", "invalid request body")
		return
	}

	storyID := r.PathValue("id")
	if storyID == "" {
		handler.WriteError(w, http.StatusBadRequest, "INVALID_ID", "story id is required")
		return
	}

	status := "rejected"
	if req.Action == "approve" {
		status = "approved"
	}

	var storyUUID pgtype.UUID
	storyUUID.Scan(storyID)

	moderatorUUID := pgtype.UUID{}
	moderatorUUID.Scan(middleware.UserID(r.Context()))

	queries := gen.New(h.pool)
	_, err := queries.UpdateStoryStatus(r.Context(), &gen.UpdateStoryStatusParams{
		ID:             storyUUID,
		Status:         status,
		ModeratedBy:    moderatorUUID,
		ModerationNote: &req.Reason,
	})
	if err != nil {
		handler.WriteError(w, http.StatusInternalServerError, "INTERNAL_ERROR", "failed to moderate story")
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{"status": status})
}

func (h *StoriesHandler) ModerationList(w http.ResponseWriter, r *http.Request) {
	statusFilter := r.URL.Query().Get("status")

	query := `SELECT id, creator_id, caption, status, like_count, save_count, created_at
		FROM stories`
	args := []any{}
	n := 1

	if statusFilter != "" {
		query += fmt.Sprintf(` WHERE status = $%d`, n)
		args = append(args, statusFilter)
		n++
	}
	query += ` ORDER BY created_at DESC LIMIT 50`

	rows, err := h.pool.Query(r.Context(), query, args...)
	if err != nil {
		handler.WriteError(w, http.StatusInternalServerError, "INTERNAL_ERROR", "failed to list stories")
		return
	}
	defer rows.Close()

	type item struct {
		ID         string `json:"id"`
		CreatorID  string `json:"creatorId"`
		Caption    string `json:"caption"`
		Status     string `json:"status"`
		LikeCount  int    `json:"likeCount"`
		SaveCount  int    `json:"saveCount"`
		CreatedAt  string `json:"createdAt"`
	}
	var items []item
	for rows.Next() {
		var i item
		rows.Scan(&i.ID, &i.CreatorID, &i.Caption, &i.Status, &i.LikeCount, &i.SaveCount, &i.CreatedAt)
		items = append(items, i)
	}
	if items == nil {
		items = []item{}
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(model.Paginated[item]{Items: items, HasMore: false})
}

func (h *StoriesHandler) Report(w http.ResponseWriter, r *http.Request) {
	storyID := r.PathValue("id")
	var req struct {
		Reason  string `json:"reason"`
		Details string `json:"details,omitempty"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		handler.WriteError(w, http.StatusBadRequest, "INVALID_REQUEST", "invalid request body")
		return
	}

	_, err := h.pool.Exec(r.Context(),
		`INSERT INTO story_reports (story_id, reported_by, reason, details) VALUES ($1, $2, $3, $4)`,
		storyID, middleware.UserID(r.Context()), req.Reason, req.Details)
	if err != nil {
		handler.WriteError(w, http.StatusInternalServerError, "INTERNAL_ERROR", "failed to report story")
		return
	}

	w.WriteHeader(http.StatusCreated)
	json.NewEncoder(w).Encode(map[string]string{"status": "reported"})
}
