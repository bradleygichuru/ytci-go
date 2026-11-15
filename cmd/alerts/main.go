package main

import (
	"context"
	"log/slog"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/bradleygichuru/ytci-go/internal/alerts"
	"github.com/bradleygichuru/ytci-go/internal/push"
)

func main() {
	mode := "worker"
	if len(os.Args) > 1 {
		mode = os.Args[1]
	}
	if mode == "ingest" {
		runIngest()
		return
	}
	if mode != "worker" {
		slog.Error("usage: alerts [ingest|worker]")
		os.Exit(2)
	}
	runWorker()
}

func connect() *pgxpool.Pool {
	url := os.Getenv("DATABASE_URL")
	if url == "" {
		slog.Error("DATABASE_URL is required")
		os.Exit(1)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	pool, err := pgxpool.New(ctx, url)
	if err != nil {
		slog.Error("connect database", "error", err)
		os.Exit(1)
	}
	if err := pool.Ping(ctx); err != nil {
		slog.Error("ping database", "error", err)
		os.Exit(1)
	}
	return pool
}

func runIngest() {
	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Minute)
	defer cancel()
	pool := connect()
	defer pool.Close()

	store := alerts.NewStore(pool)
	ingester, err := alerts.NewIngester(ctx, store, os.Getenv("ALERTS_KMD_RSS_URL"), os.Getenv("ALERTS_FCDO_URL"))
	if err != nil {
		slog.Error("alerts ingest disabled", "error", err)
		os.Exit(1)
	}
	results, err := ingester.IngestAll(ctx)
	for _, result := range results {
		slog.Info("alerts ingest", "source", result.Source, "seen", result.Seen, "created", result.Created, "updated", result.Updated, "error", result.Err)
	}
	if err != nil {
		os.Exit(1)
	}
}

func runWorker() {
	pool := connect()
	defer pool.Close()

	store := alerts.NewStore(pool)
	var pushClient *push.Client
	if token := os.Getenv("EXPO_PUSH_TOKEN"); token != "" {
		pushClient = push.New(token)
	}
	worker := alerts.NewWorker(store, pushClient)
	if os.Getenv("ALERTS_DRY_RUN") == "true" {
		worker.SetDryRun(true)
	}

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	worker.Start(ctx)
	slog.Info("alerts worker stopped")
}
