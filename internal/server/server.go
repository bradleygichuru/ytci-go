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
	"github.com/bradleygichuru/ytci-go/internal/push"
	"github.com/bradleygichuru/ytci-go/internal/r2"
)

func New(cfg *config.Config, pool *pgxpool.Pool) *chi.Mux {
	jwks := middleware.NewJWKSCache(cfg.AdminJWKSURL, cfg.JWKSCacheTTL)
	return newRouter(cfg, pool, jwks, nil, nil)
}

func NewWithClients(cfg *config.Config, pool *pgxpool.Pool, r2client *r2.Client, pushClient *push.Client) (*chi.Mux, *middleware.JWKSCache) {
	jwks := middleware.NewJWKSCache(cfg.AdminJWKSURL, cfg.JWKSCacheTTL)
	r := newRouter(cfg, pool, jwks, r2client, pushClient)
	return r, jwks
}

func newRouter(cfg *config.Config, pool *pgxpool.Pool, jwks *middleware.JWKSCache, r2client *r2.Client, pushClient *push.Client) *chi.Mux {
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
	eventsHandler := admin.NewEventsHandler(pool)
	storiesHandler := admin.NewStoriesHandler(pool)
	coursesHandler := admin.NewCoursesHandler(pool)
	challengesHandler := admin.NewChallengesHandler(pool)
	conservationHandler := admin.NewConservationHandler(pool)
	campaignsHandler := admin.NewCampaignsHandler(pool)
	analyticsHandler := admin.NewAnalyticsHandler(pool)

	feedHandler := expo.NewFeedHandler(pool)
	bucketHandler := expo.NewBucketHandler(pool)
	profileHandler := expo.NewProfileHandler(pool)
	itinerariesHandler := expo.NewItinerariesHandler(pool)
	pushRegisterHandler := expo.NewPushRegisterHandler(pool)

	r.Route("/v1", func(sub chi.Router) {
		sub.Use(middleware.JWTAuth(jwks, cfg.JWTExpectedIss, cfg.JWTExpectedAud))

		sub.Group(func(adminR chi.Router) {
			adminR.Use(middleware.AdminGate)

			adminR.Get("/destinations", destHandler.List)
			adminR.Get("/events", eventsHandler.List)
			adminR.Get("/stories", storiesHandler.List)
			adminR.Get("/courses", coursesHandler.List)
			adminR.Get("/challenges", challengesHandler.List)
			adminR.Get("/conservation/activities", conservationHandler.List)
			adminR.Get("/campaigns", campaignsHandler.List)
			adminR.Get("/analytics/summary", analyticsHandler.Summary)
			adminR.Get("/analytics/reports", analyticsHandler.ReportsList)
			adminR.Post("/analytics/reports/export", analyticsHandler.Export)

			if r2client != nil {
				mediaHandler := admin.NewMediaHandler(pool, r2client)
				adminR.Post("/media/presign", mediaHandler.Presign)
				adminR.Post("/media/complete", mediaHandler.Complete)
			}

			if pushClient != nil {
				pushHandler := admin.NewPushHandler(pool, pushClient)
				adminR.Post("/push/send", pushHandler.Send)
				adminR.Post("/push/schedule", pushHandler.Schedule)
				adminR.Get("/push/history", pushHandler.History)
				adminR.Get("/push/history/{id}", pushHandler.HistoryDetail)
			}
		})

		sub.Route("/mobile", func(mobile chi.Router) {
			mobile.Get("/feed", feedHandler.GetFeed)
			mobile.Get("/destinations", destHandler.List)
			mobile.Get("/destinations/{slug}", destHandler.Get)
			mobile.Get("/events", eventsHandler.List)
			mobile.Get("/stories", storiesHandler.List)
			mobile.Get("/courses", coursesHandler.List)
			mobile.Get("/challenges", challengesHandler.List)
			mobile.Get("/conservation", conservationHandler.List)

			mobile.Group(func(authRouter chi.Router) {
				authRouter.Use(middleware.AuthGate)

				authRouter.Get("/bucket", bucketHandler.List)
				authRouter.Post("/bucket", bucketHandler.Add)
				authRouter.Get("/profile", profileHandler.Get)
				authRouter.Get("/itineraries", itinerariesHandler.List)
				authRouter.Post("/itineraries", itinerariesHandler.Create)
				authRouter.Post("/push/register", pushRegisterHandler.Register)
			})
		})
	})

	return r
}
