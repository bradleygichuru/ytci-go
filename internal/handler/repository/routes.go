package repository

import (
	"github.com/go-chi/chi/v5"

	"github.com/bradleygichuru/ytci-go/internal/middleware"
)

// RegisterPublic mounts the unauthenticated catalogue, submission and upload
// routes. Callers mount it under /v1/public and pass stricter limiters for
// the two write paths.
func (h *Handler) RegisterPublic(r chi.Router, presignLimiter, submissionLimiter *middleware.RateLimiter) {
	r.Route("/repository", func(pub chi.Router) {
		pub.Get("/entries", h.ListPublic)
		pub.Get("/entries/{slug}", h.GetPublic)
		pub.Get("/facets", h.Facets)
		pub.Get("/stats", h.Stats)
		pub.Get("/settings", h.Settings)
		pub.With(presignLimiter.Middleware).Post("/uploads/presign", h.PublicPresign)
		pub.With(submissionLimiter.Middleware).Post("/submissions", h.CreateSubmission)
	})
}

// RegisterAdmin mounts the moderation surface. Callers put it inside the
// JWTAuth + AdminGate group and apply AdminGate for the repository roles.
func (h *Handler) RegisterAdmin(r chi.Router) {
	r.Get("/entries", h.ListAdmin)
	r.Post("/entries", h.CreateAdmin)
	r.Get("/entries/{id}", h.GetAdmin)
	r.Patch("/entries/{id}", h.UpdateAdmin)
	r.Delete("/entries/{id}", h.DeleteAdmin)
	r.Post("/entries/{id}/approve", h.Approve)
	r.Post("/entries/{id}/publish", h.Publish)
	r.Post("/entries/{id}/unpublish", h.Unpublish)
	r.Post("/entries/{id}/feature", h.Feature)
	r.Post("/entries/{id}/reject", h.Reject)
	r.Get("/settings", h.AdminGetSettings)
	r.Patch("/settings", h.AdminUpdateSettings)
	r.Post("/uploads/presign", h.AdminPresign)
}
