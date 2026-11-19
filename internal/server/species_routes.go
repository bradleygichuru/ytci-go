package server

import (
	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/bradleygichuru/ytci-go/internal/middleware"
	"github.com/bradleygichuru/ytci-go/internal/r2"
	"github.com/bradleygichuru/ytci-go/internal/species"
)

// mountSpeciesPublicRoutes is the public half of the species registration
// seam. It lives on the public subrouter because that router is mounted
// separately and already carries the public rate limiter.
func mountSpeciesPublicRoutes(pub chi.Router, pool *pgxpool.Pool, storeClient *r2.Client) {
	h := species.NewWithPool(pool, storeFor(storeClient))
	pub.Get("/species", h.ListPublic)
	pub.Get("/species/{slug}", h.GetPublic)
	pub.Get("/species/{slug}/languages", h.Languages)
}

// mountSpeciesRoutes is the account and admin half of the species vertical.
// Handlers live in internal/species; this file is the registration seam so
// parallel streams never edit the same route block.
func mountSpeciesRoutes(sub chi.Router, pool *pgxpool.Pool, storeClient *r2.Client, jwks *middleware.JWKSCache, expectedIssuer, expectedAudience string) {
	h := species.NewWithPool(pool, storeFor(storeClient))

	// Reader saves and explicit language packs require an account.
	sub.Group(func(m chi.Router) {
		m.Use(middleware.JWTAuth(jwks, expectedIssuer, expectedAudience))
		m.Use(middleware.AuthGate)

		m.Get("/mobile/species/saved", h.ListSaved)
		m.Post("/mobile/species/{slug}/save", h.Save)
		m.Delete("/mobile/species/{slug}/save", h.Unsave)
		m.Get("/mobile/species/packs/{language}", h.Pack)
	})

	// Authoring and review. These roles are not platform admins, so the group
	// authenticates and every handler enforces its own species permission.
	sub.Group(func(a chi.Router) {
		a.Use(middleware.JWTAuth(jwks, expectedIssuer, expectedAudience))
		a.Use(middleware.AuthGate)

		a.Get("/admin/species", h.AdminList)
		a.Post("/admin/species", h.AdminCreate)
		a.Get("/admin/species/{id}", h.AdminGet)
		a.Patch("/admin/species/{id}", h.AdminUpdate)
		a.Post("/admin/species/{id}/variants", h.AdminUpsertVariant)
		a.Post("/admin/species/{id}/names", h.AdminUpsertName)
		a.Post("/admin/species/{id}/media", h.AdminCreateMedia)
		a.Post("/admin/species/{id}/audio", h.AdminCreateAudio)
		a.Post("/admin/species/{id}/publish", h.AdminPublish)
		a.Post("/admin/species/{id}/unpublish", h.AdminUnpublish)

		a.Get("/admin/species/review", h.AdminReviewQueue)
		a.Post("/admin/species/review/{id}/decision", h.AdminDecideReview)

		a.Get("/admin/species/sources", h.AdminListSources)
		a.Post("/admin/species/sources", h.AdminCreateSource)
		a.Get("/admin/species/rights", h.AdminListRights)
		a.Post("/admin/species/rights", h.AdminCreateRights)
	})
}

// storeFor keeps a nil *r2.Client a nil interface so the presigner is never
// invoked on a nil receiver.
func storeFor(client *r2.Client) r2.Store {
	if client == nil {
		return nil
	}
	return client
}
