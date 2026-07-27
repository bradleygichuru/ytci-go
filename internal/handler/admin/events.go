package admin

import (
	"net/http"
	"time"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/bradleygichuru/ytci-go/internal/db/gen"
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
	pg := &pagination.CursorPaginator[gen.Event]{}

	pg.WritePage(w, r,
		func(limit int32) ([]gen.Event, error) {
			return queries.ListEvents(r.Context(), limit)
		},
		func(limit int32, sortValue, id string) ([]gen.Event, error) {
			var d pgtype.Date
			var uid pgtype.UUID
			d.Scan(sortValue)
			uid.Scan(id)
			return queries.ListEventsAfter(r.Context(), &gen.ListEventsAfterParams{
				EventDate: d,
				ID:        uid,
				Limit:     limit,
			})
		},
		func(e gen.Event) (string, bool) {
			d := e.EventDate.Time.Format(time.RFC3339Nano)
			return pagination.EncodeCursor(d, pagination.UUIDString(e.ID.Bytes)), true
		},
	)
}
