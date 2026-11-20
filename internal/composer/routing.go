package composer

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"net/http"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// ORSRouter calls the OpenRouteService Matrix API. The engine caps the matrix
// at 25 points; a larger pool must be prefiltered by the caller.
type ORSRouter struct {
	APIKey  string
	BaseURL string
	HTTP    *http.Client
}

func NewORSRouter(apiKey string) *ORSRouter {
	return &ORSRouter{
		APIKey:  apiKey,
		BaseURL: "https://api.openrouteservice.org",
		HTTP:    &http.Client{Timeout: 2500 * time.Millisecond},
	}
}

func (r *ORSRouter) Matrix(ctx context.Context, points []Point, mode string) (TravelMatrix, error) {
	if r == nil || r.APIKey == "" {
		return TravelMatrix{}, errors.New("composer: ORS API key is missing")
	}
	if len(points) == 0 {
		return TravelMatrix{Minutes: [][]int{}, DistanceKm: [][]float64{}, Estimated: false, Provider: "openrouteservice"}, nil
	}
	if len(points) > 25 {
		return TravelMatrix{}, errors.New("composer: ORS matrix is capped at 25 points")
	}

	locations := make([][2]float64, 0, len(points))
	for _, p := range points {
		locations = append(locations, [2]float64{p.Longitude, p.Latitude})
	}
	body, err := json.Marshal(map[string]any{
		"locations": locations,
		"metrics":   []string{"duration", "distance"},
		"units":     "km",
	})
	if err != nil {
		return TravelMatrix{}, fmt.Errorf("composer: ORS encode: %w", err)
	}

	base := r.BaseURL
	if base == "" {
		base = "https://api.openrouteservice.org"
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		base+"/v2/matrix/driving-car", bytes.NewReader(body))
	if err != nil {
		return TravelMatrix{}, fmt.Errorf("composer: ORS request: %w", err)
	}
	req.Header.Set("Authorization", r.APIKey)
	req.Header.Set("Content-Type", "application/json")

	httpClient := r.HTTP
	if httpClient == nil {
		httpClient = &http.Client{Timeout: 2500 * time.Millisecond}
	}
	resp, err := httpClient.Do(req)
	if err != nil {
		return TravelMatrix{}, fmt.Errorf("composer: ORS: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		return TravelMatrix{}, fmt.Errorf("composer: ORS returned %d", resp.StatusCode)
	}

	var out struct {
		Durations [][]float64 `json:"durations"`
		Distances [][]float64 `json:"distances"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return TravelMatrix{}, fmt.Errorf("composer: ORS decode: %w", err)
	}
	minutes := make([][]int, len(points))
	distanceKm := make([][]float64, len(points))
	for i := range points {
		minutes[i] = make([]int, len(points))
		distanceKm[i] = make([]float64, len(points))
		for j := range points {
			if i < len(out.Durations) && j < len(out.Durations[i]) {
				minutes[i][j] = int(math.Round(out.Durations[i][j] / 60))
			}
			if i < len(out.Distances) && j < len(out.Distances[i]) {
				distanceKm[i][j] = math.Round(out.Distances[i][j]*10) / 10
			}
		}
	}
	return TravelMatrix{
		Minutes: minutes, DistanceKm: distanceKm,
		Estimated: false, Provider: "openrouteservice",
	}, nil
}

// CachedRouter caches matrices for 7 days and falls back to a 30-day stale
// copy when the live provider is unavailable.
type CachedRouter struct {
	Router Router
	Pool   *pgxpool.Pool
}

func (r *CachedRouter) Matrix(ctx context.Context, points []Point, mode string) (TravelMatrix, error) {
	if r.Pool == nil || r.Router == nil {
		if r.Router == nil {
			return StraightLineRouter{}.Matrix(ctx, points, mode)
		}
		return r.Router.Matrix(ctx, points, mode)
	}
	key := matrixCacheKey(points, mode)
	if cached, ok := r.get(ctx, key, false); ok {
		return cached, nil
	}
	matrix, err := r.Router.Matrix(ctx, points, mode)
	if err != nil {
		if stale, ok := r.get(ctx, key, true); ok {
			return stale, nil
		}
		return TravelMatrix{}, err
	}
	r.set(ctx, key, mode, matrix)
	return matrix, nil
}

func (r *CachedRouter) get(ctx context.Context, key string, stale bool) (TravelMatrix, bool) {
	query := `SELECT response FROM routing_cache WHERE cache_key = $1 AND expires_at > now()`
	if stale {
		query = `SELECT response FROM routing_cache WHERE cache_key = $1 AND created_at > now() - interval '30 days'`
	}
	var raw []byte
	if err := r.Pool.QueryRow(ctx, query, key).Scan(&raw); err != nil {
		if err == pgx.ErrNoRows {
			return TravelMatrix{}, false
		}
		return TravelMatrix{}, false
	}
	var matrix TravelMatrix
	if err := json.Unmarshal(raw, &matrix); err != nil {
		return TravelMatrix{}, false
	}
	return matrix, true
}

func (r *CachedRouter) set(ctx context.Context, key, mode string, matrix TravelMatrix) {
	raw, err := json.Marshal(matrix)
	if err != nil {
		return
	}
	_, _ = r.Pool.Exec(ctx, `
		INSERT INTO routing_cache (cache_key, mode, response, provider, estimated, created_at, expires_at)
		VALUES ($1, $2, $3, $4, $5, now(), now() + interval '7 days')
		ON CONFLICT (cache_key) DO UPDATE SET
			response = EXCLUDED.response,
			provider = EXCLUDED.provider,
			estimated = EXCLUDED.estimated,
			created_at = now(),
			expires_at = EXCLUDED.expires_at
	`, key, mode, raw, matrix.Provider, matrix.Estimated)
}

func matrixCacheKey(points []Point, mode string) string {
	h := sha256.New()
	fmt.Fprintf(h, "%s|", mode)
	for _, p := range points {
		fmt.Fprintf(h, "%.5f,%.5f;", p.Latitude, p.Longitude)
	}
	return hex.EncodeToString(h.Sum(nil))
}

// ORSQuotaRouter is a best-effort circuit breaker: once 80% of the daily ORS
// matrix quota is used, it falls back to the deterministic straight-line
// estimate instead of risking a hard provider limit.
type ORSQuotaRouter struct {
	Router     Router
	Pool       *pgxpool.Pool
	DailyLimit int
	Now        func() time.Time
}

func (r *ORSQuotaRouter) Matrix(ctx context.Context, points []Point, mode string) (TravelMatrix, error) {
	if r.Router == nil {
		return StraightLineRouter{}.Matrix(ctx, points, mode)
	}
	limit := r.DailyLimit
	if limit <= 0 {
		limit = 500
	}
	if r.Pool != nil {
		if count, err := r.increment(ctx); err == nil && count > limit*80/100 {
			return StraightLineRouter{}.Matrix(ctx, points, mode)
		}
	}
	return r.Router.Matrix(ctx, points, mode)
}

func (r *ORSQuotaRouter) increment(ctx context.Context) (int, error) {
	now := time.Now
	if r.Now != nil {
		now = r.Now
	}
	var count int
	err := r.Pool.QueryRow(ctx, `
		INSERT INTO routing_usage (usage_date, count)
		VALUES ($1::date, 1)
		ON CONFLICT (usage_date) DO UPDATE
		SET count = routing_usage.count + 1
		RETURNING count
	`, now().UTC().Format("2006-01-02")).Scan(&count)
	if err != nil {
		return 0, err
	}
	return count, nil
}
