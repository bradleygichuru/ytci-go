package admin

import (
	"encoding/json"
	"net/http"
	"time"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/bradleygichuru/ytci-go/internal/db/gen"
	"github.com/bradleygichuru/ytci-go/internal/handler"
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
	pg := &pagination.CursorPaginator[gen.Destination]{}

	pg.WritePage(w, r,
		func(limit int32) ([]gen.Destination, error) {
			return queries.ListDestinations(r.Context(), limit)
		},
		func(limit int32, sortValue, id string) ([]gen.Destination, error) {
			var ts pgtype.Timestamp
			var uid pgtype.UUID
			ts.Scan(sortValue)
			uid.Scan(id)
			return queries.ListDestinationsAfter(r.Context(), &gen.ListDestinationsAfterParams{
				CreatedAt: ts,
				ID:        uid,
				Limit:     limit,
			})
		},
		func(d gen.Destination) (string, bool) {
			ts := d.CreatedAt.Time.UTC().Format(time.RFC3339Nano)
			return pagination.EncodeCursor(ts, pagination.UUIDString(d.ID.Bytes)), true
		},
	)
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

func (h *DestinationsHandler) Create(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Name     string  `json:"name"`
		Slug     string  `json:"slug"`
		County   string  `json:"county"`
		Category string  `json:"category"`
		Lat      float64 `json:"lat"`
		Lng      float64 `json:"lng"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		handler.WriteError(w, http.StatusBadRequest, "INVALID_REQUEST", "invalid request body")
		return
	}

	queries := gen.New(h.pool)
	dest, err := queries.CreateDestination(r.Context(), &gen.CreateDestinationParams{
		Name:          req.Name,
		Slug:          req.Slug,
		County:        req.County,
		Category:      req.Category,
		Status:        "draft",
		StMakepoint:   req.Lng,
		StMakepoint_2: req.Lat,
	})
	if err != nil {
		handler.WriteError(w, http.StatusInternalServerError, "INTERNAL_ERROR", "failed to create destination")
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	json.NewEncoder(w).Encode(dest)
}
