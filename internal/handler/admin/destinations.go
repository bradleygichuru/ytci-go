package admin

import (
	"encoding/json"
	"net/http"

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

	limit, cursor := parsePagination(r)
	_ = cursor // for cursor-based pagination; offset-based for now
	destinations, err := queries.ListDestinations(r.Context(), &gen.ListDestinationsParams{
		Limit:  int32(limit),
		Offset: 0,
	})
	if err != nil {
		handler.WriteError(w, http.StatusInternalServerError, "INTERNAL_ERROR", "failed to list destinations")
		return
	}

	resp := model.Paginated[gen.Destination]{
		Items:   destinations,
		HasMore: len(destinations) >= limit,
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(resp)
}

func parsePagination(r *http.Request) (int, int) {
	limit := model.DefaultLimit
	offset := 0
	return limit, offset
}
