package admin

import (
	"encoding/json"
	"net/http"
	"strconv"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/bradleygichuru/ytci-go/internal/db/gen"
	"github.com/bradleygichuru/ytci-go/internal/handler"
	"github.com/bradleygichuru/ytci-go/internal/model"
)

type StoriesHandler struct {
	pool *pgxpool.Pool
}

func NewStoriesHandler(pool *pgxpool.Pool) *StoriesHandler {
	return &StoriesHandler{pool: pool}
}

func (h *StoriesHandler) List(w http.ResponseWriter, r *http.Request) {
	queries := gen.New(h.pool)
	limit, offset := parsePagination(r)

	stories, err := queries.ListStories(r.Context(), &gen.ListStoriesParams{
		Limit:  int32(limit + 1),
		Offset: int32(offset),
	})
	if err != nil {
		handler.WriteError(w, http.StatusInternalServerError, "INTERNAL_ERROR", "failed to list stories")
		return
	}

	hasMore := len(stories) > limit
	items := stories
	if hasMore {
		items = stories[:limit]
	}

	resp := model.Paginated[gen.Story]{
		Items:   items,
		HasMore: hasMore,
	}
	if hasMore {
		next := strconv.Itoa(offset + limit)
		resp.NextCursor = &next
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(resp)
}
