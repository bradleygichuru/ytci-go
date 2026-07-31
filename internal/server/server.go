package server

import (
	"net/http"
	"strings"
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
	jwks := middleware.NewJWKSCache(cfg.AdminJWKSURL, cfg.JWKSCacheTTL, pool)
	return newRouter(cfg, pool, jwks, nil, nil)
}

func NewWithClients(cfg *config.Config, pool *pgxpool.Pool, r2client *r2.Client, pushClient *push.Client) (*chi.Mux, *middleware.JWKSCache) {
	jwks := middleware.NewJWKSCache(cfg.AdminJWKSURL, cfg.JWKSCacheTTL, pool)
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
		AllowedOrigins:   splitOrigins(cfg.CORSOrigins),
		AllowedMethods:   []string{"GET", "POST", "PATCH", "DELETE", "OPTIONS"},
		AllowedHeaders:   []string{"Accept", "Authorization", "Content-Type", "Idempotency-Key"},
		AllowCredentials: true,
		MaxAge:           300,
	}))

	r.Get("/health", handler.Health)

	h := mountHandlers(pool, r2client, pushClient)

	publicLimiter := middleware.PublicRateLimiter()

	r.Route("/v1/public", func(pub chi.Router) {
		pub.Use(publicLimiter.Middleware)
		mountPublicRoutes(pub, h, r2client)
	})

	authLimiter := middleware.AuthenticatedRateLimiter()

	r.Route("/v1", func(sub chi.Router) {
		sub.Use(middleware.OptionalAuth(jwks, cfg.JWTExpectedIss, cfg.JWTExpectedAud))
		mountAdminRoutes(sub, h, r2client, jwks, cfg.JWTExpectedIss, cfg.JWTExpectedAud)
		mountMobileRoutes(sub, h, r2client, authLimiter)
	})

	return r
}

type handlers struct {
	dest      *admin.DestinationsHandler
	events    *admin.EventsHandler
	stories   *admin.StoriesHandler
	course    *admin.CourseHandler
	challenge *admin.ChallengeAdminHandler
	conserv   *admin.ConservationAdminHandler
	campaign  *admin.CampaignAdminHandler
	analytics *admin.AnalyticsHandler
	lessonAdmin *admin.LessonHandler
	quizAdmin   *admin.QuizAdminHandler
	bulkImport *admin.BulkImport
	itinStops *expo.ItineraryStopsHandler
	feed      *expo.FeedHandler
	bucket    *expo.BucketHandler
	profile   *expo.ProfileHandler
	itin      *expo.ItinerariesHandler
	pushReg   *expo.PushRegisterHandler
	actions   *expo.ActionsHandler
	mStories  *expo.StoriesHandler
	media     *admin.MediaHandler
	pushAdmin *admin.PushHandler
	comments  *expo.CommentHandler
	adminComments *admin.AdminCommentHandler
	account   *expo.AccountHandler
	mobileCourses *expo.CourseHandler
}

func mountHandlers(pool *pgxpool.Pool, r2client r2.Store, pushClient *push.Client) *handlers {
	h := &handlers{
		dest:      admin.NewDestinationsHandler(pool, r2client),
		events:    admin.NewEventsHandler(pool, r2client),
		stories:   admin.NewStoriesHandler(pool, r2client),
		course:    admin.NewCourseHandler(pool),
		challenge: admin.NewChallengeAdminHandler(pool),
		conserv:   admin.NewConservationAdminHandler(pool),
		campaign:  admin.NewCampaignAdminHandler(pool),
		analytics: admin.NewAnalyticsHandler(pool),
		lessonAdmin: admin.NewLessonHandler(pool),
		quizAdmin:   admin.NewQuizAdminHandler(pool),
		bulkImport: admin.NewBulkImport(pool),
		itinStops: expo.NewItineraryStopsHandler(pool),
		feed:      expo.NewFeedHandler(pool),
		bucket:    expo.NewBucketHandler(pool),
		profile:   expo.NewProfileHandler(pool),
		itin:      expo.NewItinerariesHandler(pool),
		pushReg:   expo.NewPushRegisterHandler(pool),
		actions:   expo.NewActionsHandler(pool),
		mStories:  expo.NewStoriesHandler(pool),
		media:     admin.NewMediaHandler(pool, r2client),
		comments:  expo.NewCommentHandler(pool),
		adminComments: admin.NewAdminCommentHandler(pool),
		account:   expo.NewAccountHandler(pool, r2client),
		mobileCourses: expo.NewCourseHandler(pool, r2client),
	}
	if pushClient != nil {
		h.pushAdmin = admin.NewPushHandler(pool, pushClient)
	}
	return h
}

func mountAdminRoutes(sub chi.Router, h *handlers, r2client r2.Store, jwks *middleware.JWKSCache, expectedIssuer, expectedAudience string) {
	sub.Group(func(aR chi.Router) {
		aR.Use(middleware.JWTAuth(jwks, expectedIssuer, expectedAudience))
		aR.Use(middleware.AdminGate)

		aR.Get("/destinations", h.dest.List)
		aR.Post("/destinations", h.dest.Create)
		aR.Patch("/destinations/{id}", h.dest.Update)
		aR.Delete("/destinations/{id}", h.dest.Delete)
		aR.Get("/events", h.events.List)
		aR.Get("/events/{id}", h.events.Get)
		aR.Post("/events", h.events.Create)
		aR.Patch("/events/{id}", h.events.Update)
		aR.Patch("/events/{id}/status", h.events.UpdateStatus)
		aR.Delete("/events/{id}", h.events.Delete)
		aR.Get("/stories", h.stories.List)
		aR.Get("/stories/moderation", h.stories.ModerationList)
		aR.Post("/stories/{id}/moderation", h.stories.Moderate)
		aR.Post("/stories/{id}/report", h.stories.Report)
		aR.Get("/courses", h.course.List)
		aR.Get("/courses/{id}", h.course.Get)
		aR.Post("/courses", h.course.Create)
		aR.Patch("/courses/{id}", h.course.Update)
		aR.Delete("/courses/{id}", h.course.Delete)
		aR.Post("/courses/{id}/lessons", h.lessonAdmin.Create)
		aR.Patch("/courses/{id}/lessons/{lessonId}", h.lessonAdmin.Update)
		aR.Delete("/courses/{id}/lessons/{lessonId}", h.lessonAdmin.Delete)
		aR.Post("/courses/{id}/quiz", h.quizAdmin.Upsert)
		aR.Delete("/courses/{id}/quiz", h.quizAdmin.Delete)
		aR.Get("/challenges", h.challenge.List)
		aR.Post("/challenges", h.challenge.Create)
		aR.Patch("/challenges/{id}", h.challenge.Update)
		aR.Delete("/challenges/{id}", h.challenge.Delete)
		aR.Get("/challenges/evidence", h.challenge.ListEvidence)
		aR.Post("/challenges/evidence/{id}/review", h.challenge.ReviewEvidence)
		aR.Get("/challenges/{id}/leaderboard", h.challenge.Leaderboard)
		aR.Get("/conservation/activities", h.conserv.List)
		aR.Post("/conservation/activities", h.conserv.Create)
		aR.Patch("/conservation/activities/{id}", h.conserv.Update)
		aR.Get("/conservation/evidence", h.conserv.ListEvidence)
		aR.Post("/conservation/evidence/{id}/review", h.conserv.ReviewEvidence)
		aR.Get("/campaigns", h.campaign.List)
		aR.Get("/campaigns/{id}", h.campaign.Get)
		aR.Post("/campaigns", h.campaign.Create)
		aR.Patch("/campaigns/{id}", h.campaign.Update)
		aR.Delete("/campaigns/{id}", h.campaign.Delete)

		aR.Get("/analytics/summary", h.analytics.Summary)
		aR.Get("/analytics/reports", h.analytics.ReportsList)
		aR.Post("/analytics/reports/export", h.analytics.Export)

		aR.Post("/destinations/{id}/media", h.dest.AddMedia)
		aR.Post("/events/{id}/media", h.events.AddMedia)

		aR.Patch("/media/{id}", h.media.UpdateMetadata)
		aR.Get("/media", h.media.List)
		if r2client != nil {
			aR.Post("/media/presign", h.media.Presign)
			aR.Post("/media/complete", h.media.Complete)
			aR.Delete("/media/{id}", h.media.Delete)
			aR.Get("/media/{id}/url", h.media.GetURL)
			aR.Get("/media/url", h.media.GetURL)
		}
		if h.pushAdmin != nil {
			aR.Post("/push/send", h.pushAdmin.Send)
			aR.Post("/push/schedule", h.pushAdmin.Schedule)
			aR.Get("/push/history", h.pushAdmin.History)
			aR.Get("/push/history/{id}", h.pushAdmin.HistoryDetail)
		}

		aR.Get("/comments/moderation", h.adminComments.ModerationList)
		aR.Post("/comments/moderation/{cid}", h.adminComments.Moderate)
	})
}

func mountMobileRoutes(sub chi.Router, h *handlers, r2client r2.Store, authLimiter *middleware.RateLimiter) {
	sub.Route("/mobile", func(m chi.Router) {
		m.Use(authLimiter.MiddlewareKeyed(func(r *http.Request) string {
			return middleware.UserID(r.Context())
		}))
		m.Get("/feed", h.feed.GetFeed)
		m.Get("/destinations", h.dest.ListMobile)
		m.Get("/destinations/{slug}", h.dest.GetMobile)
		m.Get("/destinations/nearby", h.dest.NearbyMobile)
		m.Get("/events", h.events.ListMobile)
		m.Get("/events/{id}", h.events.GetMobile)
		m.Get("/stories", h.mStories.ListEnriched)
		m.Get("/courses", h.course.ListMobile)
		m.Get("/challenges", h.challenge.ListMobile)
		m.Get("/conservation", h.conserv.ListMobile)
		m.Get("/courses/{id}", h.mobileCourses.GetCourseDetail)
		m.Get("/courses/{id}/lessons", h.mobileCourses.ListLessons)
		m.Get("/courses/{id}/lessons/{lessonId}", h.mobileCourses.GetLesson)
		m.Get("/challenges/{id}", h.challenge.ChallengeDetail)
		m.Get("/conservation/{id}", h.conserv.ConservationDetail)
		m.Get("/stories/{id}", h.mStories.StoryDetail)

		m.Group(func(aR chi.Router) {
			aR.Use(middleware.AuthGate)

			aR.Get("/bucket", h.bucket.List)
			aR.Post("/bucket", h.bucket.Add)
			aR.Delete("/bucket/{destinationId}", h.bucket.Remove)
			aR.Post("/bucket/{destinationId}/visited", h.bucket.MarkVisited)
			aR.Get("/profile", h.profile.Get)
			aR.Patch("/profile", h.profile.Update)
			aR.Get("/itineraries", h.itin.List)
			aR.Post("/itineraries", h.itin.Create)
			aR.Get("/itineraries/{id}", h.itin.Get)
			aR.Patch("/itineraries/{id}", h.itin.Update)
			aR.Delete("/itineraries/{id}", h.itin.Delete)
			aR.Post("/itineraries/{id}/duplicate", h.itin.Duplicate)
			aR.Get("/itineraries/{id}/stops", h.itinStops.GetStops)
			aR.Put("/itineraries/{id}/stops", h.itinStops.UpsertStops)
			aR.Post("/push/register", h.pushReg.Register)

			if r2client != nil {
				aR.Post("/media/presign", h.media.Presign)
				aR.Post("/media/complete", h.media.Complete)
			}
			aR.Get("/media/{objectKey}", h.media.GetURL)

			aR.Post("/stories", h.actions.CreateStory)
			aR.Post("/stories/like", h.actions.ToggleLike)
			aR.Post("/stories/save", h.actions.ToggleSave)
			aR.Post("/stories/{id}/report", h.stories.Report)
			aR.Post("/challenges/{id}/join", h.actions.JoinChallenge)
			aR.Post("/challenges/{id}/evidence", h.actions.SubmitChallengeEvidence)
			aR.Get("/conservation/my", h.actions.GetMyConservationActivities)
			aR.Post("/conservation/{id}/join", h.actions.JoinConservation)
			aR.Post("/conservation/{id}/evidence", h.actions.SubmitConservationEvidence)
			aR.Get("/conservation/{id}/progress", h.actions.GetConservationProgress)
			aR.Delete("/conservation/{id}/leave", h.actions.LeaveConservation)
			aR.Post("/courses/{id}/enroll", h.actions.EnrollCourse)
			aR.Post("/courses/{id}/lessons/{lessonId}/complete", h.mobileCourses.MarkLessonComplete)
			aR.Get("/courses/enrolled", h.mobileCourses.GetEnrolledCourses)
			aR.Get("/courses/{id}/progress", h.mobileCourses.GetCourseProgress)
			aR.Post("/events/{id}/save", h.actions.SaveEvent)
			aR.Post("/events/{id}/attend", h.actions.AttendEvent)
			aR.Post("/events/{id}/leave", h.actions.LeaveEvent)
			aR.Post("/analytics/app-open", h.actions.RecordAppOpen)
			aR.Post("/account/delete", h.account.Delete)
			aR.Get("/courses/{id}/quiz", h.mobileCourses.GetQuiz)
			aR.Post("/courses/{id}/quiz/submit", h.mobileCourses.SubmitQuiz)
			aR.Get("/courses/{id}/certificate", h.mobileCourses.GetCertificate)
			aR.Get("/challenges/{id}/leaderboard", h.challenge.Leaderboard)
			aR.Get("/profile/badges", h.profile.Badges)
			aR.Get("/profile/consent", h.profile.ConsentGet)
			aR.Patch("/profile/consent", h.profile.ConsentUpdate)
			aR.Get("/stories/mine", h.mStories.MyStories)
			aR.Get("/stories/saved", h.mStories.SavedStories)
			aR.Get("/events/saved", h.actions.ListSavedEvents)

			aR.Post("/stories/{id}/comments", h.comments.CreateComment)
			aR.Post("/stories/{id}/comments/{cid}/replies", h.comments.CreateReply)
			aR.Patch("/stories/{id}/comments/{cid}", h.comments.UpdateComment)
			aR.Delete("/stories/{id}/comments/{cid}", h.comments.DeleteComment)
			aR.Post("/stories/{id}/comments/{cid}/like", h.comments.ToggleLike)
			aR.Post("/stories/{id}/comments/{cid}/report", h.comments.ReportComment)
		})
	})
}

func mountPublicRoutes(pub chi.Router, h *handlers, r2client r2.Store) {
	pub.Get("/feed", h.feed.GetFeed)
	pub.Get("/destinations", h.dest.ListMobile)
	pub.Get("/destinations/{slug}", h.dest.GetMobile)
	pub.Get("/destinations/nearby", h.dest.NearbyMobile)
	pub.Get("/events", h.events.ListMobile)
	pub.Get("/stories", h.mStories.ListEnriched)
	pub.Get("/courses", h.course.ListMobile)
	pub.Get("/challenges", h.challenge.ListMobile)
	pub.Get("/conservation", h.conserv.ListMobile)

	pub.Get("/stories/{id}/comments", h.comments.ListComments)

	if r2client != nil {
		pub.Get("/media/url", h.media.GetURL)
	}
}

func splitOrigins(s string) []string {
	if s == "" {
		return []string{}
	}
	parts := strings.Split(s, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		trimmed := strings.TrimSpace(p)
		if trimmed != "" {
			out = append(out, trimmed)
		}
	}
	return out
}
