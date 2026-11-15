package main

import (
	"context"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/bradleygichuru/ytci-go/internal/discovery"
	"github.com/bradleygichuru/ytci-go/internal/places"
)

func main() {
	countiesFlag := flag.String("counties", "003,005,023", "comma-separated county codes (Kilifi, Lamu, Turkana)")
	apply := flag.Bool("apply", false, "clear today's discovery cache and run discovery")
	refresh := flag.Bool("refresh", true, "clear today's cached discovery run before applying")
	minPhotos := flag.Int("min-photos", 0, "exit non-zero when fewer than this many candidates have photos")
	flag.Parse()

	ctx := context.Background()
	dbURL := os.Getenv("DATABASE_URL")
	key := os.Getenv("GOOGLE_PLACES_API_KEY")
	if dbURL == "" || key == "" {
		slog.Error("DATABASE_URL and GOOGLE_PLACES_API_KEY are required")
		os.Exit(1)
	}

	pool, err := pgxpool.New(ctx, dbURL)
	if err != nil {
		slog.Error("connect database", "error", err)
		os.Exit(1)
	}
	defer pool.Close()
	if err := pool.Ping(ctx); err != nil {
		slog.Error("ping database", "error", err)
		os.Exit(1)
	}

	counties := splitCounties(*countiesFlag)
	if len(counties) == 0 {
		slog.Error("no counties supplied")
		os.Exit(1)
	}
	if !*apply {
		fmt.Printf("Dry run: would discover %s. Re-run with --apply to write.\n", strings.Join(counties, ", "))
		return
	}

	adapter := discovery.NewAdapter(pool, places.NewClient(key), discovery.DailyRequestBudgetFromEnv())
	if *refresh {
		if _, err := pool.Exec(ctx,
			`DELETE FROM composer_discovery_runs
			 WHERE bucket_date = current_date AND scope_kind = 'county' AND scope_key = ANY($1)`,
			counties); err != nil {
			slog.Error("clear discovery cache", "error", err)
			os.Exit(1)
		}
	}

	for _, county := range counties {
		started := time.Now()
		result, err := adapter.DiscoverCounty(ctx, county)
		if err != nil {
			slog.Error("discover county", "county", county, "error", err)
			os.Exit(1)
		}
		withPhoto := 0
		for _, c := range result.Candidates {
			if c.HeroImageURL != nil && *c.HeroImageURL != "" {
				withPhoto++
			}
		}
		fmt.Printf("county=%s candidates=%d with_photo=%d requests=%d cached=%v duration=%s\n",
			county, len(result.Candidates), withPhoto, result.RequestCount, result.Cached, time.Since(started).Round(time.Millisecond))
	}

	rows, err := pool.Query(ctx,
		`SELECT county_code, count(*)::int, count(hero_image_url)::int
		 FROM composer_unverified_candidates
		 WHERE county_code = ANY($1) AND expires_at > now()
		 GROUP BY county_code ORDER BY county_code`, counties)
	if err != nil {
		slog.Error("read coverage", "error", err)
		os.Exit(1)
	}
	defer rows.Close()

	total, withPhoto := 0, 0
	for rows.Next() {
		var county string
		var count, photos int
		if err := rows.Scan(&county, &count, &photos); err != nil {
			slog.Error("scan coverage", "error", err)
			os.Exit(1)
		}
		fmt.Printf("stored county=%s candidates=%d with_photo=%d\n", county, count, photos)
		total += count
		withPhoto += photos
	}
	if err := rows.Err(); err != nil {
		slog.Error("iterate coverage", "error", err)
		os.Exit(1)
	}
	fmt.Printf("coverage total=%d with_photo=%d\n", total, withPhoto)
	if total == 0 {
		slog.Error("no candidates stored for the selected counties")
		os.Exit(1)
	}
	if *minPhotos > 0 && withPhoto < *minPhotos {
		slog.Error("photo coverage below minimum", "with_photo", withPhoto, "min", *minPhotos)
		os.Exit(1)
	}
}

func splitCounties(raw string) []string {
	parts := strings.Split(raw, ",")
	out := make([]string, 0, len(parts))
	seen := make(map[string]bool, len(parts))
	for _, part := range parts {
		county := strings.TrimSpace(part)
		if county == "" || seen[county] {
			continue
		}
		seen[county] = true
		out = append(out, county)
	}
	return out
}
