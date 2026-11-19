package server

import (
	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/bradleygichuru/ytci-go/internal/middleware"
	"github.com/bradleygichuru/ytci-go/internal/recognition"
)

// mountRecognitionRoutes is the only route table for the recognition vertical.
// Handlers live in internal/recognition; this file is the registration seam so
// parallel streams never edit the same route block.
func mountRecognitionRoutes(sub chi.Router, pool *pgxpool.Pool, jwks *middleware.JWKSCache, expectedIssuer, expectedAudience string) {
	h := recognition.NewHandler(pool)

	// Reader surfaces.
	sub.Group(func(m chi.Router) {
		m.Use(middleware.JWTAuth(jwks, expectedIssuer, expectedAudience))
		m.Use(middleware.AuthGate)

		m.Get("/mobile/recognition", h.Overview)
		m.Get("/mobile/recognition/badges", h.ListBadges)
		m.Get("/mobile/recognition/badges/{key}", h.GetBadge)
		m.Patch("/mobile/recognition/badges/{key}", h.SetBadgeVisibility)
		m.Get("/mobile/recognition/missions", h.Missions)
		m.Get("/mobile/recognition/seasons/{key}", h.MySeason)
		m.Get("/mobile/recognition/passport", h.Passport)
		m.Get("/mobile/recognition/leaderboard", h.MyLeaderboardState)
		m.Post("/mobile/recognition/leaderboard/join", h.JoinLeaderboard)
		m.Delete("/mobile/recognition/leaderboard/join", h.LeaveLeaderboard)
		m.Patch("/mobile/recognition/display-name", h.UpdateDisplayName)
		m.Post("/mobile/recognition/reports", h.ReportLeaderboardEntry)
	})

	// Admin surfaces. AdminGate admits the admin-read roles (moderator,
	// administrator, super_admin); individual operations enforce the
	// recognition permission from roles.v1.yaml.
	sub.Group(func(a chi.Router) {
		a.Use(middleware.JWTAuth(jwks, expectedIssuer, expectedAudience))
		a.Use(middleware.AdminGate)

		a.Get("/admin/recognition/rules", h.AdminListRules)
		a.Post("/admin/recognition/rules", h.AdminCreateRule)
		a.Post("/admin/recognition/rules/{id}/publish", h.AdminPublishRule)
		a.Post("/admin/recognition/rules/{id}/simulate", h.AdminSimulateRule)

		a.Get("/admin/recognition/badges", h.AdminListBadges)
		a.Post("/admin/recognition/badges", h.AdminCreateBadge)
		a.Post("/admin/recognition/badges/{key}/publish", h.AdminPublishBadge)

		a.Get("/admin/recognition/seasons", h.AdminListSeasons)
		a.Post("/admin/recognition/seasons", h.AdminCreateSeason)
		a.Post("/admin/recognition/seasons/{id}/publish", h.AdminPublishSeason)
		a.Post("/admin/recognition/seasons/{id}/freeze", h.AdminFreezeSeason)
		a.Post("/admin/recognition/seasons/{id}/close", h.AdminCloseSeason)

		a.Get("/admin/recognition/moderation", h.AdminModeration)
		a.Post("/admin/recognition/moderation/{id}/decision", h.AdminDecideModeration)

		a.Get("/admin/recognition/backfill", h.AdminListBackfill)
		a.Post("/admin/recognition/backfill", h.AdminCreateBackfill)
		a.Post("/admin/recognition/backfill/{id}/approve", h.AdminApproveBackfill)

		a.Post("/admin/recognition/adjustments", h.AdminCreateAdjustment)

		a.Get("/admin/recognition/kill-switches", h.AdminListKillSwitches)
		a.Patch("/admin/recognition/kill-switches/{key}", h.AdminSetKillSwitch)

		a.Get("/admin/recognition/audit", h.AdminAudit)
	})
}

// mountRecognitionPublicRoutes lives on the public subrouter because the
// router mounts /v1/public separately. It is the second and last wiring line.
func mountRecognitionPublicRoutes(pub chi.Router, pool *pgxpool.Pool) {
	h := recognition.NewHandler(pool)
	pub.Get("/leaderboard", h.PublicLeaderboard)
	pub.Get("/seasons/{key}", h.PublicSeason)
}
