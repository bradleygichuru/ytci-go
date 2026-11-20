package composer

import (
	"context"
	"errors"
	"math"
	"time"

	"github.com/bradleygichuru/ytci-go/internal/catalog"
)

type Point struct {
	Latitude  float64 `json:"latitude"`
	Longitude float64 `json:"longitude"`
}

type TravelMatrix struct {
	Minutes    [][]int     `json:"minutes"`
	DistanceKm [][]float64 `json:"distanceKm"`
	Estimated  bool        `json:"estimated"`
	Provider   string      `json:"provider"`
}

type Router interface {
	Matrix(ctx context.Context, points []Point, mode string) (TravelMatrix, error)
}

// FallbackRouter tries the primary provider and falls back to a local estimate.
type FallbackRouter struct {
	Primary  Router
	Fallback Router
}

func (r *FallbackRouter) Matrix(ctx context.Context, points []Point, mode string) (TravelMatrix, error) {
	if r.Primary != nil {
		matrix, err := r.Primary.Matrix(ctx, points, mode)
		if err == nil {
			return matrix, nil
		}
	}
	if r.Fallback == nil {
		return TravelMatrix{}, errors.New("composer: no routing provider")
	}
	return r.Fallback.Matrix(ctx, points, mode)
}

// StraightLineRouter is the deterministic, always-available fallback:
// straight-line distance × 1.4 / 40 km/h + 15 minutes per leg.
type StraightLineRouter struct{}

func (StraightLineRouter) Matrix(_ context.Context, points []Point, mode string) (TravelMatrix, error) {
	n := len(points)
	matrix := TravelMatrix{
		Minutes:    make([][]int, n),
		DistanceKm: make([][]float64, n),
		Estimated:  true,
		Provider:   "straight-line",
	}
	for i := 0; i < n; i++ {
		matrix.Minutes[i] = make([]int, n)
		matrix.DistanceKm[i] = make([]float64, n)
		for j := 0; j < n; j++ {
			if i == j {
				continue
			}
			km := haversineKm(points[i], points[j])
			minutes := int(math.Round(km*1.4/40.0*60.0)) + 15
			matrix.Minutes[i][j] = minutes
			matrix.DistanceKm[i][j] = math.Round(km*10) / 10
		}
	}
	return matrix, nil
}

func haversineKm(a, b Point) float64 {
	const earthRadiusKm = 6371.0
	lat1 := a.Latitude * math.Pi / 180
	lat2 := b.Latitude * math.Pi / 180
	dLat := (b.Latitude - a.Latitude) * math.Pi / 180
	dLng := (b.Longitude - a.Longitude) * math.Pi / 180
	h := math.Sin(dLat/2)*math.Sin(dLat/2) +
		math.Cos(lat1)*math.Cos(lat2)*math.Sin(dLng/2)*math.Sin(dLng/2)
	return 2 * earthRadiusKm * math.Asin(math.Sqrt(h))
}

type WeatherProvider interface {
	Daily(ctx context.Context, point Point, start, end time.Time) ([]DayWeather, error)
}

type NoopWeather struct{}

func (NoopWeather) Daily(context.Context, Point, time.Time, time.Time) ([]DayWeather, error) {
	return nil, nil
}

type Event struct {
	ID      string  `json:"id"`
	Title   string  `json:"title"`
	County  string  `json:"county"`
	Type    string  `json:"type"`
	Date    string  `json:"date"`
	EndDate *string `json:"endDate,omitempty"`
}

type EventProvider interface {
	Events(ctx context.Context, counties []string, start, end time.Time) ([]Event, error)
}

type CandidateProvider interface {
	Candidates(ctx context.Context) ([]catalog.ComposerCandidate, error)
}

type CoverageProvider interface {
	Coverage(ctx context.Context) ([]catalog.ComposerCountyCoverage, error)
}

// DiscoveryProvider triggers a county discovery run; the engine re-queries
// the catalog afterwards. Implementations must never create catalog drafts.
type DiscoveryProvider interface {
	DiscoverCounty(ctx context.Context, countyCode string) error
}

type CountyResolver interface {
	ResolveCounty(ctx context.Context, lat, lng *float64, countyText string) (catalog.County, error)
}
