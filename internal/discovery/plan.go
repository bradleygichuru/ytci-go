package discovery

import (
	"context"
	"errors"
	"fmt"
	"math"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// ErrNoCounty means the county code is not in kenya_counties.
var ErrNoCounty = errors.New("discovery: county not found")

// SearchPoint is one nearby-search centre.
type SearchPoint struct {
	Latitude  float64 `json:"latitude"`
	Longitude float64 `json:"longitude"`
}

// CountyPlan is the multi-point search plan for one county.
type CountyPlan struct {
	CountyCode   string        `json:"countyCode"`
	County       string        `json:"county"`
	AreaKm2      float64       `json:"areaKm2"`
	RadiusMeters int           `json:"radiusMeters"`
	Points       []SearchPoint `json:"points"`
}

// CountySearchPlan generates points inside the county polygon and a radius
// that covers the county without exceeding Google's 50 km limit.
func CountySearchPlan(ctx context.Context, pool *pgxpool.Pool, countyCode string) (CountyPlan, error) {
	var plan CountyPlan
	plan.CountyCode = countyCode
	err := pool.QueryRow(ctx, `
		SELECT adm1_name, COALESCE(ST_Area(geom::geography) / 1000000.0, 0)
		FROM kenya_counties
		WHERE county_code = $1
	`, countyCode).Scan(&plan.County, &plan.AreaKm2)
	if errors.Is(err, pgx.ErrNoRows) {
		return plan, ErrNoCounty
	}
	if err != nil {
		return plan, fmt.Errorf("discovery: county plan: %w", err)
	}

	count := pointCount(plan.AreaKm2)
	plan.RadiusMeters = searchRadiusMeters(plan.AreaKm2, count)

	rows, err := pool.Query(ctx, `
		SELECT ST_Y(pt.geom), ST_X(pt.geom)
		FROM kenya_counties k
		CROSS JOIN LATERAL ST_Dump(ST_GeneratePoints(k.geom, $2, 42)) AS pt
		WHERE k.county_code = $1
	`, countyCode, count)
	if err != nil {
		return plan, fmt.Errorf("discovery: county points: %w", err)
	}
	defer rows.Close()

	for rows.Next() {
		var p SearchPoint
		if err := rows.Scan(&p.Latitude, &p.Longitude); err != nil {
			return plan, fmt.Errorf("discovery: scan county point: %w", err)
		}
		plan.Points = append(plan.Points, p)
	}
	if err := rows.Err(); err != nil {
		return plan, fmt.Errorf("discovery: county points: %w", err)
	}

	// Degenerate geometry: fall back to the polygon's surface point.
	if len(plan.Points) == 0 {
		var p SearchPoint
		err := pool.QueryRow(ctx, `
			SELECT ST_Y(ST_PointOnSurface(geom)), ST_X(ST_PointOnSurface(geom))
			FROM kenya_counties WHERE county_code = $1
		`, countyCode).Scan(&p.Latitude, &p.Longitude)
		if err != nil {
			return plan, fmt.Errorf("discovery: county surface point: %w", err)
		}
		plan.Points = []SearchPoint{p}
		plan.RadiusMeters = searchRadiusMeters(plan.AreaKm2, 1)
	}

	return plan, nil
}

func pointCount(areaKm2 float64) int {
	switch {
	case areaKm2 < 2500:
		return 1
	case areaKm2 < 10000:
		return 4
	case areaKm2 < 30000:
		return 6
	default:
		return 9
	}
}

func searchRadiusMeters(areaKm2 float64, points int) int {
	if points < 1 {
		points = 1
	}
	if areaKm2 <= 0 {
		return 10000
	}
	radius := math.Sqrt(areaKm2 * 1000000.0 / float64(points) / math.Pi)
	if radius < 5000 {
		radius = 5000
	}
	if radius > 50000 {
		radius = 50000
	}
	return int(math.Round(radius/1000)) * 1000
}
