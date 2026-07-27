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
