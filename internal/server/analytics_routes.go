package server

import (
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/bradleygichuru/ytci-go/internal/analytics"
	"github.com/bradleygichuru/ytci-go/internal/contracts"
	"github.com/bradleygichuru/ytci-go/internal/handler"
	"github.com/bradleygichuru/ytci-go/internal/middleware"
)

// mountAnalyticsRoutes is the only route table for the analytics vertical.
// Handlers live in internal/analytics; this file is the registration seam so
// parallel streams never edit the same route block.
func mountAnalyticsRoutes(sub chi.Router, pool *pgxpool.Pool, jwks *middleware.JWKSCache, expectedIssuer, expectedAudience string) {
	h := analytics.NewHandler(pool)
	analytics.StartWorker(pool)

	// Guest analytics: install and batch ingest carry no account identity, so
	// the router's OptionalAuth is all they get. The limiter key is the client
	// address because there is no authenticated user to key on.
	ingestLimiter := middleware.NewRateLimiter(10, 200)
	ingest := ingestLimiter.Middleware

	sub.With(ingest).Post("/mobile/analytics/installs", h.RegisterInstall)
	sub.With(ingest).Post("/mobile/analytics/batches", h.IngestBatch)
	sub.With(ingest).Put("/mobile/analytics/consent", h.SetConsent)
	sub.With(ingest).Delete("/mobile/analytics/consent", h.WithdrawConsent)

	sub.Group(func(m chi.Router) {
		m.Use(middleware.JWTAuth(jwks, expectedIssuer, expectedAudience))
		m.Use(middleware.AuthGate)
		m.Get("/mobile/analytics/export", h.Export)
	})

	// Admin surfaces read aggregates only. Permissions come from
	// roles.v1.yaml: analytics.read (moderator+), analytics.export and
	// analytics.erasure (administrator/super_admin).
	sub.Group(func(a chi.Router) {
		a.Use(middleware.JWTAuth(jwks, expectedIssuer, expectedAudience))

		a.Get("/admin/analytics/overview", requireAnalyticsPermission(contracts.PermAnalyticsRead, h.AdminOverview))
		a.Get("/admin/analytics/journeys", requireAnalyticsPermission(contracts.PermAnalyticsRead, h.AdminJourneys))
		a.Get("/admin/analytics/funnels", requireAnalyticsPermission(contracts.PermAnalyticsRead, h.AdminFunnels))
		a.Get("/admin/analytics/retention", requireAnalyticsPermission(contracts.PermAnalyticsRead, h.AdminRetention))
		a.Get("/admin/analytics/adoption", requireAnalyticsPermission(contracts.PermAnalyticsRead, h.AdminAdoption))
		a.Get("/admin/analytics/quality", requireAnalyticsPermission(contracts.PermAnalyticsRead, h.AdminQuality))
		a.Post("/admin/analytics/export", requireAnalyticsPermission(contracts.PermAnalyticsExport, h.AdminExport))
		a.Post("/admin/analytics/erasure", requireAnalyticsPermission(contracts.PermAnalyticsErasure, h.AdminErasure))
	})
}

func requireAnalyticsPermission(permission string, next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		role := middleware.RoleFromCtx(r.Context())
		if !contracts.MustRoles().Can(string(role), permission) {
			handler.WriteError(w, http.StatusForbidden, "FORBIDDEN", "analytics permission required")
			return
		}
		next(w, r)
	}
}
