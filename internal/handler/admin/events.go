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

type EventsHandler struct {
	pool *pgxpool.Pool
}

func NewEventsHandler(pool *pgxpool.Pool) *EventsHandler {
	return &EventsHandler{pool: pool}
}

func (h *EventsHandler) List(w http.ResponseWriter, r *http.Request) {
	queries := gen.New(h.pool)
	limit, offset := parsePagination(r)

	events, err := queries.ListEvents(r.Context(), &gen.ListEventsParams{
		Limit:  int32(limit + 1),
		Offset: int32(offset),
	})
	if err != nil {
		handler.WriteError(w, http.StatusInternalServerError, "INTERNAL_ERROR", "failed to list events")
		return
	}

	hasMore := len(events) > limit
	items := events
	if hasMore {
		items = events[:limit]
	}

	resp := model.Paginated[gen.Event]{
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
