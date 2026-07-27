package main

import (
	"context"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/bradleygichuru/ytci-go/internal/config"
	"github.com/bradleygichuru/ytci-go/internal/db"
	"github.com/bradleygichuru/ytci-go/internal/handler/admin"
	"github.com/bradleygichuru/ytci-go/internal/push"
	"github.com/bradleygichuru/ytci-go/internal/r2"
	"github.com/bradleygichuru/ytci-go/internal/server"
)

func main() {
	cfg, err := config.Load()
	if err != nil {
		slog.Error("failed to load config", "error", err)
		os.Exit(1)
	}

	slog.SetLogLoggerLevel(parseLogLevel(cfg.LogLevel))

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	dbpool, err := db.Connect(ctx, cfg)
	if err != nil {
		slog.Error("failed to connect to database", "error", err)
		os.Exit(1)
	}
	defer dbpool.Close()
	slog.Info("connected to database")

	var r2client *r2.Client
	if cfg.R2AccountID != "" && cfg.R2AccessKeyID != "" {
		r2client, err = r2.New(cfg.R2AccountID, cfg.R2AccessKeyID, cfg.R2SecretAccess, cfg.R2Bucket)
		if err != nil {
			slog.Warn("failed to init R2 client, media uploads disabled", "error", err)
			r2client = nil
		} else {
			slog.Info("R2 client initialized")
		}
	}

	var pushClient *push.Client
	if cfg.ExpoPushToken != "" {
		pushClient = push.New(cfg.ExpoPushToken)
		slog.Info("push notification client initialized")
	}

	router, jwks := server.NewWithClients(cfg, dbpool, r2client, pushClient)

	if err := jwks.Ping(); err != nil {
		slog.Error("failed to reach JWKS endpoint at startup", "url", cfg.AdminJWKSURL, "error", err)
		os.Exit(1)
	}
	slog.Info("JWKS endpoint reachable")

	if pushClient != nil {
		workerCtx, workerCancel := context.WithCancel(context.Background())
		defer workerCancel()
		pushWorker := push.NewWorker(dbpool, pushClient)
		pushWorker.Start(workerCtx)
	}

	reportWorker := admin.NewReportWorker(dbpool)
	reportWorker.Start(ctx)
	defer reportWorker.Stop()

	srv := &http.Server{
		Addr:    ":" + cfg.Port,
		Handler: router,
	}

	go func() {
		slog.Info("server starting", "port", cfg.Port)
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			slog.Error("server error", "error", err)
			os.Exit(1)
		}
	}()

	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	<-quit

	slog.Info("shutting down...")
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := srv.Shutdown(shutdownCtx); err != nil {
		slog.Error("shutdown error", "error", err)
		os.Exit(1)
	}
	slog.Info("server stopped")
}

func parseLogLevel(level string) slog.Level {
	switch level {
	case "debug":
		return slog.LevelDebug
	case "info":
		return slog.LevelInfo
	case "warn":
		return slog.LevelWarn
	case "error":
		return slog.LevelError
	default:
		return slog.LevelInfo
	}
}
