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

type EventsHandler struct {
	pool *pgxpool.Pool
}

func NewEventsHandler(pool *pgxpool.Pool) *EventsHandler {
	return &EventsHandler{pool: pool}
}

func (h *EventsHandler) List(w http.ResponseWriter, r *http.Request) {
	queries := gen.New(h.pool)
	pr := pagination.ParseRequest(r)
	limit := int32(pr.Limit)

	var events []gen.Event
	var err error

	if pr.Cursor != nil {
		var d pgtype.Date
		var id pgtype.UUID
		d.Scan(pr.Cursor.SortValue)
		id.Scan(pr.Cursor.ID)

		events, err = queries.ListEventsAfter(r.Context(), &gen.ListEventsAfterParams{
			EventDate: d,
			ID:        id,
			Limit:     limit + 1,
		})
	} else {
		events, err = queries.ListEvents(r.Context(), limit+1)
	}
	if err != nil {
		handler.WriteError(w, http.StatusInternalServerError, "INTERNAL_ERROR", "failed to list events")
		return
	}

	hasMore := len(events) > int(limit)
	items := events
	if hasMore {
		items = events[:limit]
	}

	resp := model.Paginated[gen.Event]{
		Items:   items,
		HasMore: hasMore,
	}
	if hasMore && len(items) > 0 {
		last := items[len(items)-1]
		d := last.EventDate.Time.Format(time.RFC3339Nano)
		next := pagination.EncodeCursor(d, pagination.UUIDString(last.ID.Bytes))
		resp.NextCursor = &next
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(resp)
}
