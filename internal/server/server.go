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

	destH := admin.NewDestinationsHandler(pool)
	eventsH := admin.NewEventsHandler(pool)
	storiesH := admin.NewStoriesHandler(pool)
	courseH := admin.NewCourseHandler(pool)
	challengeH := admin.NewChallengeAdminHandler(pool)
	conservationH := admin.NewConservationAdminHandler(pool)
	campaignH := admin.NewCampaignAdminHandler(pool)
	analyticsH := admin.NewAnalyticsHandler(pool)

	feedH := expo.NewFeedHandler(pool)
	bucketH := expo.NewBucketHandler(pool)
	profileH := expo.NewProfileHandler(pool)
	itinH := expo.NewItinerariesHandler(pool)
	pushRegH := expo.NewPushRegisterHandler(pool)
	actionsH := expo.NewActionsHandler(pool)

	r.Route("/v1", func(sub chi.Router) {
		sub.Use(middleware.JWTAuth(jwks, cfg.JWTExpectedIss, cfg.JWTExpectedAud))

		sub.Group(func(aR chi.Router) {
			aR.Use(middleware.AdminGate)

			aR.Get("/destinations", destH.List)
			aR.Post("/destinations", destH.Create)
			aR.Get("/events", eventsH.List)
			aR.Post("/events", eventsH.Create)
			aR.Patch("/events/{id}", eventsH.Update)
			aR.Delete("/events/{id}", eventsH.Delete)
			aR.Get("/stories", storiesH.List)
			aR.Post("/stories/{id}/moderation", storiesH.Moderate)
			aR.Get("/courses", courseH.List)
			aR.Get("/challenges", challengeH.List)
			aR.Get("/conservation/activities", conservationH.List)
			aR.Get("/conservation/evidence", conservationH.ListEvidence)
			aR.Post("/conservation/evidence/{id}/review", conservationH.ReviewEvidence)
			aR.Get("/campaigns", campaignH.List)
			aR.Patch("/campaigns/{id}/status", campaignH.UpdateStatus)
			aR.Get("/analytics/summary", analyticsH.Summary)
			aR.Get("/analytics/reports", analyticsH.ReportsList)
			aR.Post("/analytics/reports/export", analyticsH.Export)

			if r2client != nil {
				mediaH := admin.NewMediaHandler(pool, r2client)
				aR.Post("/media/presign", mediaH.Presign)
				aR.Post("/media/complete", mediaH.Complete)
				aR.Get("/media", mediaH.List)
			}
			if pushClient != nil {
				pushH := admin.NewPushHandler(pool, pushClient)
				aR.Post("/push/send", pushH.Send)
				aR.Post("/push/schedule", pushH.Schedule)
				aR.Get("/push/history", pushH.History)
				aR.Get("/push/history/{id}", pushH.HistoryDetail)
			}
		})

		sub.Route("/mobile", func(m chi.Router) {
			m.Get("/feed", feedH.GetFeed)
			m.Get("/destinations", destH.List)
			m.Get("/destinations/{slug}", destH.Get)
			m.Get("/destinations/nearby", destH.Nearby)
			m.Get("/events", eventsH.List)
			m.Get("/stories", storiesH.List)
			m.Get("/courses", courseH.List)
			m.Get("/challenges", challengeH.List)
			m.Get("/conservation", conservationH.List)

			m.Group(func(aR chi.Router) {
				aR.Use(middleware.AuthGate)

				aR.Get("/bucket", bucketH.List)
				aR.Post("/bucket", bucketH.Add)
				aR.Delete("/bucket/{destinationId}", bucketH.Remove)
				aR.Post("/bucket/{destinationId}/visited", bucketH.MarkVisited)
				aR.Get("/profile", profileH.Get)
				aR.Patch("/profile", profileH.Update)
				aR.Get("/itineraries", itinH.List)
				aR.Post("/itineraries", itinH.Create)
				aR.Get("/itineraries/{id}", itinH.Get)
				aR.Delete("/itineraries/{id}", itinH.Delete)
				aR.Post("/itineraries/{id}/duplicate", itinH.Duplicate)
				aR.Post("/push/register", pushRegH.Register)

				aR.Post("/stories", actionsH.CreateStory)
				aR.Post("/stories/like", actionsH.ToggleLike)
				aR.Post("/stories/save", actionsH.ToggleSave)
				aR.Post("/challenges/{id}/join", actionsH.JoinChallenge)
				aR.Post("/challenges/{id}/evidence", actionsH.SubmitChallengeEvidence)
				aR.Post("/conservation/{id}/join", actionsH.JoinConservation)
				aR.Post("/conservation/{id}/evidence", actionsH.SubmitConservationEvidence)
				aR.Post("/courses/{id}/enroll", actionsH.EnrollCourse)
				aR.Post("/events/{id}/save", actionsH.SaveEvent)
				aR.Post("/analytics/app-open", actionsH.RecordAppOpen)
			})
		})
	})

	return r
}
