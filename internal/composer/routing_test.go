package composer

import (
	"context"
	"encoding/json"
	"errors"
	"math"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bradleygichuru/ytci-go/internal/db"
)

func TestORSRouterMatrix(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, http.MethodPost, r.Method)
		assert.Equal(t, "/v2/matrix/driving-car", r.URL.Path)
		assert.Equal(t, "test-key", r.Header.Get("Authorization"))
		var req struct {
			Locations [][2]float64 `json:"locations"`
			Metrics   []string     `json:"metrics"`
		}
		require.NoError(t, json.NewDecoder(r.Body).Decode(&req))
		require.Len(t, req.Locations, 2)
		assert.Equal(t, []string{"duration", "distance"}, req.Metrics)
		json.NewEncoder(w).Encode(map[string]any{
			"durations": [][]float64{{0, 600}, {600, 0}},
			"distances": [][]float64{{0, 10}, {10, 0}},
		})
	}))
	defer ts.Close()

	router := &ORSRouter{
		APIKey:  "test-key",
		BaseURL: ts.URL,
		HTTP:    ts.Client(),
	}
	matrix, err := router.Matrix(context.Background(), []Point{
		{Latitude: -1.29, Longitude: 36.82},
		{Latitude: -1.49, Longitude: 35.14},
	}, "car")
	require.NoError(t, err)
	assert.Equal(t, 10, matrix.Minutes[0][1])
	assert.Equal(t, 10.0, matrix.DistanceKm[0][1])
	assert.False(t, matrix.Estimated)
	assert.Equal(t, "openrouteservice", matrix.Provider)
}

func TestORSRouterError(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer ts.Close()
	router := &ORSRouter{APIKey: "test-key", BaseURL: ts.URL, HTTP: ts.Client()}
	_, err := router.Matrix(context.Background(), []Point{{Latitude: 1, Longitude: 2}}, "car")
	require.Error(t, err)
}

func TestCachedRouterFreshAndStale(t *testing.T) {
	ctx := context.Background()
	pool := db.SetupTestDB(t)
	defer pool.Close()

	primary := &fakeRouter{matrix: TravelMatrix{
		Minutes:    [][]int{{0, 10}, {10, 0}},
		DistanceKm: [][]float64{{0, 5}, {5, 0}},
		Estimated:  false,
		Provider:   "test-router",
	}}
	cached := &CachedRouter{Router: primary, Pool: pool}
	points := []Point{{Latitude: -1.29, Longitude: 36.82}, {Latitude: -1.49, Longitude: 35.14}}

	first, err := cached.Matrix(ctx, points, "car")
	require.NoError(t, err)
	assert.Equal(t, 10, first.Minutes[0][1])

	// Fresh cache wins even when the live provider fails.
	cached.Router = &fakeRouter{err: errors.New("down")}
	second, err := cached.Matrix(ctx, points, "car")
	require.NoError(t, err)
	assert.Equal(t, 10, second.Minutes[0][1])

	// Expire the fresh entry; the stale copy still rescues the request.
	_, err = pool.Exec(ctx, `
		UPDATE routing_cache SET created_at = now() - interval '10 days',
		expires_at = now() - interval '1 day'
	`)
	require.NoError(t, err)
	third, err := cached.Matrix(ctx, points, "car")
	require.NoError(t, err)
	assert.Equal(t, 10, third.Minutes[0][1])
}

func TestStraightLineRouterEstimate(t *testing.T) {
	matrix, err := StraightLineRouter{}.Matrix(context.Background(), []Point{
		{Latitude: -1.2921, Longitude: 36.8219},
		{Latitude: -1.2333, Longitude: 36.8333},
	}, "car")
	require.NoError(t, err)
	assert.True(t, matrix.Estimated)
	assert.Equal(t, "straight-line", matrix.Provider)
	assert.Greater(t, matrix.Minutes[0][1], 15)
	assert.Less(t, matrix.Minutes[0][1], 60)
	assert.True(t, math.Abs(matrix.DistanceKm[0][1]-7.0) < 2.0)
}

func TestORSQuotaRouterFallsBackAt80Percent(t *testing.T) {
	ctx := context.Background()
	pool := db.SetupTestDB(t)
	defer pool.Close()

	primary := &fakeRouter{matrix: TravelMatrix{
		Minutes:    [][]int{{0, 5}, {5, 0}},
		DistanceKm: [][]float64{{0, 2}, {2, 0}},
		Provider:   "ors-primary",
	}}
	quota := &ORSQuotaRouter{
		Router:     primary,
		Pool:       pool,
		DailyLimit: 10,
		Now: func() time.Time {
			return time.Date(2026, 11, 14, 12, 0, 0, 0, time.UTC)
		},
	}
	points := []Point{{Latitude: 1, Longitude: 1}, {Latitude: 2, Longitude: 2}}
	for i := 0; i < 8; i++ {
		matrix, err := quota.Matrix(ctx, points, "car")
		require.NoError(t, err)
		assert.Equal(t, "ors-primary", matrix.Provider, "request %d", i+1)
	}
	matrix, err := quota.Matrix(ctx, points, "car")
	require.NoError(t, err)
	assert.Equal(t, "straight-line", matrix.Provider)
}
