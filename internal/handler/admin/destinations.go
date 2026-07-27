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

type DestinationsHandler struct {
	pool *pgxpool.Pool
}

func NewDestinationsHandler(pool *pgxpool.Pool) *DestinationsHandler {
	return &DestinationsHandler{pool: pool}
}

func (h *DestinationsHandler) List(w http.ResponseWriter, r *http.Request) {
	queries := gen.New(h.pool)
	pr := pagination.ParseRequest(r)
	limit := int32(pr.Limit)

	var destinations []gen.Destination
	var err error

	if pr.Cursor != nil {
		var ts pgtype.Timestamp
		var id pgtype.UUID
		ts.Scan(pr.Cursor.SortValue)
		id.Scan(pr.Cursor.ID)

		destinations, err = queries.ListDestinationsAfter(r.Context(), &gen.ListDestinationsAfterParams{
			CreatedAt: ts,
			ID:        id,
			Limit:     limit + 1,
		})
	} else {
		destinations, err = queries.ListDestinations(r.Context(), limit+1)
	}
	if err != nil {
		handler.WriteError(w, http.StatusInternalServerError, "INTERNAL_ERROR", "failed to list destinations")
		return
	}

	hasMore := len(destinations) > int(limit)
	items := destinations
	if hasMore {
		items = destinations[:limit]
	}

	resp := model.Paginated[gen.Destination]{
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

func (h *DestinationsHandler) Get(w http.ResponseWriter, r *http.Request) {
	slug := r.PathValue("slug")
	if slug == "" {
		handler.WriteError(w, http.StatusBadRequest, "INVALID_SLUG", "destination slug is required")
		return
	}

	queries := gen.New(h.pool)
	dest, err := queries.GetDestinationBySlug(r.Context(), slug)
	if err != nil {
		handler.WriteError(w, http.StatusNotFound, "NOT_FOUND", "destination not found")
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(dest)
}
