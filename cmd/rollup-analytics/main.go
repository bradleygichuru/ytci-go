// Command rollup-analytics recomputes the analytics rollups for a range of
// UTC days. It is idempotent and used for production backfills.
package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/bradleygichuru/ytci-go/internal/analytics"
)

func main() {
	days := flag.Int("days", 7, "number of UTC days ending yesterday to recompute")
	flag.Parse()

	databaseURL := os.Getenv("DATABASE_URL")
	if databaseURL == "" {
		fmt.Fprintln(os.Stderr, "DATABASE_URL is required")
		os.Exit(1)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Minute)
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

	for i := *days; i >= 1; i-- {
		day := time.Now().UTC().AddDate(0, 0, -i)
		if err := analytics.RunRollupForDay(ctx, pool, day); err != nil {
			fmt.Fprintf(os.Stderr, "rollup %s: %v\n", day.Format("2006-01-02"), err)
			os.Exit(1)
		}
		fmt.Printf("rolled up %s\n", day.Format("2006-01-02"))
	}
}
