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

type DestinationsHandler struct {
	pool *pgxpool.Pool
}

func NewDestinationsHandler(pool *pgxpool.Pool) *DestinationsHandler {
	return &DestinationsHandler{pool: pool}
}

func (h *DestinationsHandler) List(w http.ResponseWriter, r *http.Request) {
	queries := gen.New(h.pool)

	limit, offset := parsePagination(r)

	destinations, err := queries.ListDestinations(r.Context(), &gen.ListDestinationsParams{
		Limit:  int32(limit),
		Offset: int32(offset),
	})
	if err != nil {
		handler.WriteError(w, http.StatusInternalServerError, "INTERNAL_ERROR", "failed to list destinations")
		return
	}

	items := make([]gen.Destination, len(destinations))
	for i, d := range destinations {
		items[i] = d
	}

	resp := model.Paginated[gen.Destination]{
		Items:   items,
		HasMore: len(items) >= limit,
	}
	if resp.HasMore {
		next := strconv.Itoa(offset + limit)
		resp.NextCursor = &next
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(resp)
}

func parsePagination(r *http.Request) (limit int, offset int) {
	limit = model.DefaultLimit
	offset = 0

	if l := r.URL.Query().Get("limit"); l != "" {
		if v, err := strconv.Atoi(l); err == nil && v > 0 {
			limit = v
			if limit > model.MaxLimit {
				limit = model.MaxLimit
			}
		}
	}

	if c := r.URL.Query().Get("cursor"); c != "" {
		if v, err := strconv.Atoi(c); err == nil && v > 0 {
			offset = v
		}
	}

	return limit, offset
}
