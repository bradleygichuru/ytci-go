package catalog

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
)

// ErrCountyNotFound means the place could not be tied to a Kenyan county by
// polygon, proximity, or normalized county text.
var ErrCountyNotFound = errors.New("catalog: county not found")

// ErrNotADestination means Google's types describe something the catalog
// deliberately does not accept (a place of worship, office, administrative
// area, accommodation, and so on).
var ErrNotADestination = errors.New("catalog: not a destination")

// County is a canonical Kenyan county.
type County struct {
	Code string
	Name string
}

// Queryer is satisfied by *pgxpool.Pool and pgx.Tx.
type Queryer interface {
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
	Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error)
}

// ResolveCounty runs the same polygon-first, text-second resolver the
// database migration uses. Coordinates may be nil when a place has no
// location; countyText is then the fallback.
func ResolveCounty(
	ctx context.Context,
	q Queryer,
	lat, lng *float64,
	countyText string,
) (County, error) {
	var county County
	err := q.QueryRow(ctx, `
		SELECT county_code, county_name
		FROM catalog_resolve_county(
			CASE
				WHEN $1::float8 IS NULL OR $2::float8 IS NULL THEN NULL
				ELSE ST_SetSRID(ST_MakePoint($1, $2), 4326)
			END,
			$3::text
		)
	`, lat, lng, countyText).Scan(&county.Code, &county.Name)
	if errors.Is(err, pgx.ErrNoRows) {
		return County{}, ErrCountyNotFound
	}
	if err != nil {
		return County{}, fmt.Errorf("catalog: resolve county: %w", err)
	}
	return county, nil
}
