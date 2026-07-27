package server

import (
	"time"

	"github.com/go-chi/chi/v5"
	chimw "github.com/go-chi/chi/v5/middleware"
	"github.com/go-chi/cors"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/bradleygichuru/ytci-go/internal/config"
	"github.com/bradleygichuru/ytci-go/internal/handler"
	"github.com/bradleygichuru/ytci-go/internal/handler/admin"
	"github.com/bradleygichuru/ytci-go/internal/handler/expo"
	"github.com/bradleygichuru/ytci-go/internal/middleware"
)

func New(cfg *config.Config, pool *pgxpool.Pool) *chi.Mux {
	r := chi.NewRouter()

	r.Use(chimw.RequestID)
	r.Use(chimw.RealIP)
	r.Use(middleware.Logger)
	r.Use(chimw.Recoverer)
	r.Use(chimw.Timeout(30 * time.Second))
	r.Use(cors.Handler(cors.Options{
		AllowedOrigins:   []string{cfg.CORSOrigins},
		AllowedMethods:   []string{"GET", "POST", "PATCH", "DELETE", "OPTIONS"},
		AllowedHeaders:   []string{"Accept", "Authorization", "Content-Type", "Idempotency-Key"},
		AllowCredentials: true,
		MaxAge:           300,
	}))

	r.Get("/health", handler.Health)

	destHandler := admin.NewDestinationsHandler(pool)
	feedHandler := expo.NewFeedHandler(pool)

	r.Route("/v1", func(sub chi.Router) {
		sub.Use(middleware.JWTAuth(middleware.NewJWKSCache(cfg.AdminJWKSURL, cfg.JWKSCacheTTL), cfg.JWTExpectedIss, cfg.JWTExpectedAud))

		sub.Group(func(adminR chi.Router) {
			adminR.Use(middleware.AdminGate)
			adminR.Get("/destinations", destHandler.List)
		})

		sub.Route("/mobile", func(mobile chi.Router) {
			mobile.Get("/feed", feedHandler.GetFeed)

			mobile.Group(func(auth chi.Router) {
				auth.Use(middleware.AuthGate)
			})
		})
	})

	return r
}
