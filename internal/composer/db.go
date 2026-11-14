package composer

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/bradleygichuru/ytci-go/internal/catalog"
	"github.com/bradleygichuru/ytci-go/internal/discovery"
)

// DBCatalogProvider reads the composer_catalog and coverage views.
type DBCatalogProvider struct {
	Pool *pgxpool.Pool
}

func (p *DBCatalogProvider) Candidates(ctx context.Context) ([]catalog.ComposerCandidate, error) {
	return catalog.QueryComposerCandidates(ctx, p.Pool, catalog.ComposerFilter{Limit: 500})
}

func (p *DBCatalogProvider) Coverage(ctx context.Context) ([]catalog.ComposerCountyCoverage, error) {
	return catalog.QueryComposerCountyCoverage(ctx, p.Pool)
}

// DBCountyResolver resolves the brief origin polygon-first.
type DBCountyResolver struct {
	Pool *pgxpool.Pool
}

func (r *DBCountyResolver) ResolveCounty(ctx context.Context, lat, lng *float64, countyText string) (catalog.County, error) {
	return catalog.ResolveCounty(ctx, r.Pool, lat, lng, countyText)
}

// DBEventProvider reads scheduled program events in the plan counties.
type DBEventProvider struct {
	Pool *pgxpool.Pool
}

func (p *DBEventProvider) Events(ctx context.Context, counties []string, start, end time.Time) ([]Event, error) {
	if len(counties) == 0 {
		return nil, nil
	}
	rows, err := p.Pool.Query(ctx, `
		SELECT e.id::text, e.title, e.county, e.type,
		       e.event_date::text, e.end_date::text
		FROM events e
		WHERE e.status = 'scheduled'
		  AND e.county = ANY($1::text[])
		  AND e.event_date <= $3::date
		  AND COALESCE(e.end_date, e.event_date) >= $2::date
		ORDER BY e.event_date, e.id
	`, counties, start.Format("2006-01-02"), end.Format("2006-01-02"))
	if err != nil {
		return nil, fmt.Errorf("composer: events: %w", err)
	}
	defer rows.Close()

	out := make([]Event, 0)
	for rows.Next() {
		var e Event
		var endDate *string
		if err := rows.Scan(&e.ID, &e.Title, &e.County, &e.Type, &e.Date, &endDate); err != nil {
			return nil, fmt.Errorf("composer: scan event: %w", err)
		}
		e.EndDate = endDate
		out = append(out, e)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("composer: events: %w", err)
	}
	return out, nil
}

// DiscoveryAdapter adapts the Google Places discovery adapter to the engine's
// provider interface. It only writes composer_unverified_candidates.
type DiscoveryAdapter struct {
	Adapter *discovery.Adapter
}

func (d DiscoveryAdapter) DiscoverCounty(ctx context.Context, countyCode string) error {
	if d.Adapter == nil {
		return nil
	}
	_, err := d.Adapter.DiscoverCounty(ctx, countyCode)
	return err
}
