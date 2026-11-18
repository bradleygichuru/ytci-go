// Command backfill-analytics migrates historical account-linked analytics rows
// to the pseudonymous analytics_id model. Run it once, with --apply, at the
// analytics v2 cutover before THEN_NOW_ANALYTICS_V2_ENABLED is switched on.
package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/bradleygichuru/ytci-go/internal/privacy"
)

func main() {
	apply := flag.Bool("apply", false, "write the backfill (default is a dry run)")
	flag.Parse()

	databaseURL := os.Getenv("DATABASE_URL")
	if databaseURL == "" {
		fmt.Fprintln(os.Stderr, "DATABASE_URL is required")
		os.Exit(1)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()

	pool, err := pgxpool.New(ctx, databaseURL)
	if err != nil {
		fmt.Fprintf(os.Stderr, "connect: %v\n", err)
		os.Exit(1)
	}
	defer pool.Close()
	if err := pool.Ping(ctx); err != nil {
		fmt.Fprintf(os.Stderr, "ping: %v\n", err)
		os.Exit(1)
	}

	result, err := privacy.BackfillAnalyticsIDs(ctx, pool, *apply)
	if err != nil {
		fmt.Fprintf(os.Stderr, "backfill: %v\n", err)
		os.Exit(1)
	}

	mode := "dry run"
	if *apply {
		mode = "applied"
	}
	fmt.Printf("analytics backfill %s\n", mode)
	fmt.Printf("  users mapped:      %d\n", result.UsersMapped)
	fmt.Printf("  app opens:         %d\n", result.AppOpens)
	fmt.Printf("  analytics events:  %d\n", result.AnalyticsEvents)
	fmt.Printf("  orphan rows left:  %d\n", result.OrphanRows)

	if result.OrphanRows > 0 {
		fmt.Fprintf(os.Stderr,
			"warning: %d rows reference user IDs that no longer exist; they keep user_id and need a separate data-cleanup decision\n",
			result.OrphanRows)
	}
	if !*apply {
		fmt.Println("no changes written; rerun with --apply at the v2 cutover")
	}
}
