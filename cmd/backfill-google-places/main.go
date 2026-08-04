package main

import (
	"context"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"strings"
	"time"

	"github.com/bradleygichuru/ytci-go/internal/config"
	"github.com/bradleygichuru/ytci-go/internal/db"
	"github.com/bradleygichuru/ytci-go/internal/db/gen"
	"github.com/bradleygichuru/ytci-go/internal/places"
)

func main() {
	dryRun := flag.Bool("dry-run", true, "preview changes without writing to DB (default true)")
	apply := flag.Bool("apply", false, "actually write google_place_id to destinations")
	flag.Parse()

	if *apply {
		*dryRun = false
	}

	if *dryRun {
		slog.Info("DRY RUN MODE — no changes will be written. Use --apply to write.")
	}

	cfg, err := config.Load()
	if err != nil {
		slog.Error("failed to load config", "error", err)
		os.Exit(1)
	}

	ctx := context.Background()

	dbpool, err := db.Connect(ctx, cfg)
	if err != nil {
		slog.Error("failed to connect to database", "error", err)
		os.Exit(1)
	}
	defer dbpool.Close()
	slog.Info("connected to database")

	client := places.NewClient(cfg.GooglePlacesAPIKey)
	queries := gen.New(dbpool)

	dests, err := queries.ListPublishedDestinationsWithoutPlaceID(ctx)
	if err != nil {
		slog.Error("failed to query destinations", "error", err)
		os.Exit(1)
	}

	slog.Info("found destinations without google_place_id", "count", len(dests))

	var matched, unmatched, errors int

	for i, dest := range dests {
		query := fmt.Sprintf("%s %s Kenya", dest.Name, dest.County)
		slog.Info("searching", "name", dest.Name, "county", dest.County, "query", query)

		results, err := client.TextSearch(ctx, query)
		if err != nil {
			slog.Error("text search failed", "name", dest.Name, "county", dest.County, "error", err)
			errors++
			time.Sleep(200 * time.Millisecond)
			continue
		}

		if len(results) == 0 {
			slog.Warn("no results", "name", dest.Name, "county", dest.County)
			unmatched++
			time.Sleep(200 * time.Millisecond)
			continue
		}

		best := findBestMatch(dest.Name, dest.County, results)
		if best == nil {
			slog.Warn("no matching result", "name", dest.Name, "county", dest.County)
			unmatched++
			time.Sleep(200 * time.Millisecond)
			continue
		}

		slog.Info("matched",
			"name", dest.Name,
			"google_name", best.DisplayName,
			"google_address", best.FormattedAddress,
			"place_id", best.PlaceID,
		)

		if !*dryRun {
			err = queries.UpdateDestinationPlaceID(ctx, &gen.UpdateDestinationPlaceIDParams{
				ID:            dest.ID,
				GooglePlaceID: &best.PlaceID,
			})
			if err != nil {
				slog.Error("failed to update destination", "id", dest.ID, "name", dest.Name, "error", err)
				errors++
				time.Sleep(200 * time.Millisecond)
				continue
			}
		}

		matched++

		if i < len(dests)-1 {
			time.Sleep(200 * time.Millisecond)
		}
	}

	fmt.Println()
	fmt.Println("=== Backfill Summary ===")
	fmt.Printf("Total:       %d\n", len(dests))
	fmt.Printf("Matched:     %d\n", matched)
	fmt.Printf("Unmatched:   %d\n", unmatched)
	fmt.Printf("Errors:      %d\n", errors)

	if *dryRun {
		fmt.Println("\nDRY RUN — no changes were written. Use --apply to write.")
	}
}

func findBestMatch(name, county string, results []places.TextSearchResult) *places.TextSearchResult {
	for i := range results {
		r := &results[i]
		if placeNamesMatch(name, r.DisplayName) {
			return r
		}
	}
	return nil
}

func placeNamesMatch(dbName, googleName string) bool {
	dbNameLower := strings.ToLower(strings.TrimSpace(dbName))
	googleNameLower := strings.ToLower(strings.TrimSpace(googleName))

	if dbNameLower == googleNameLower {
		return true
	}

	if strings.Contains(dbNameLower, googleNameLower) || strings.Contains(googleNameLower, dbNameLower) {
		return true
	}

	dbWords := strings.Fields(dbNameLower)
	googleWords := strings.Fields(googleNameLower)

	for _, dw := range dbWords {
		for _, gw := range googleWords {
			if dw == gw {
				return true
			}
		}
	}

	return false
}
