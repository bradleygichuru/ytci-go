package admin

import (
	"encoding/json"
	"net/http"
	"time"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/bradleygichuru/ytci-go/internal/db/gen"
	"github.com/bradleygichuru/ytci-go/internal/handler"
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
	pr := pagination.ParseRequest(r)
	limit := int32(pr.Limit)

	var stories []gen.Story
	var err error

	if pr.Cursor != nil {
		var ts pgtype.Timestamp
		var id pgtype.UUID
		ts.Scan(pr.Cursor.SortValue)
		id.Scan(pr.Cursor.ID)

		stories, err = queries.ListStoriesAfter(r.Context(), &gen.ListStoriesAfterParams{
			CreatedAt: ts,
			ID:        id,
			Limit:     limit + 1,
		})
	} else {
		stories, err = queries.ListStories(r.Context(), limit+1)
	}
	if err != nil {
		handler.WriteError(w, http.StatusInternalServerError, "INTERNAL_ERROR", "failed to list stories")
		return
	}

	hasMore := len(stories) > int(limit)
	items := stories
	if hasMore {
		items = stories[:limit]
	}

	resp := model.Paginated[gen.Story]{
		Items:   items,
		HasMore: hasMore,
	}
	if hasMore && len(items) > 0 {
		last := items[len(items)-1]
		ts := last.CreatedAt.Time.UTC().Format(time.RFC3339Nano)
		next := pagination.EncodeCursor(ts, pagination.UUIDString(last.ID.Bytes))
		resp.NextCursor = &next
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(resp)
}
