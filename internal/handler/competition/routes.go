package competition

import (
	"net/http"

	"github.com/go-chi/chi/v5"

	"github.com/bradleygichuru/ytci-go/internal/contracts"
	"github.com/bradleygichuru/ytci-go/internal/handler"
	"github.com/bradleygichuru/ytci-go/internal/middleware"
)

// RegisterPublic mounts the unauthenticated competition reads.
func (h *Handler) RegisterPublic(r chi.Router) {
	r.Route("/competition", func(pub chi.Router) {
		pub.Get("/settings", h.Settings)
		pub.Post("/consultation", h.SubmitConsultation)
		pub.Get("/editions", h.ListEditions)
		pub.Get("/editions/{slug}", h.GetEdition)
		pub.Get("/editions/{slug}/categories", h.ListCategories)
		pub.Get("/editions/{slug}/entries", h.ListEntries)
		pub.Get("/editions/{slug}/leaderboard", h.Leaderboard)
		pub.Get("/editions/{slug}/results", h.Results)
		pub.Get("/editions/{slug}/winners", h.Winners)
	})
}

// RegisterMobile mounts the signed-in vote and judge surface. The caller wraps
// the router in AuthGate; per-object judge authority is checked in the handler.
func (h *Handler) RegisterMobile(r chi.Router) {
	r.Route("/competition", func(m chi.Router) {
		m.Get("/editions", h.ListEditions)
		m.Get("/editions/{slug}", h.GetEdition)
		m.Get("/editions/{slug}/leaderboard", h.Leaderboard)
		m.Get("/editions/{slug}/results", h.Results)
		m.Get("/editions/{slug}/my-votes", h.MyVotes)
		m.Post("/editions/{slug}/votes", h.CastVote)
		m.Delete("/votes/{voteId}", h.WithdrawVote)

		// Entrant self-entry. The repository submission and admin approval
		// remain the only way to add new work; this surface links an existing
		// published entry to one or two competition categories.
		m.Get("/editions/{slug}/eligible-repository-entries", h.EligibleRepositoryEntries)
		m.Get("/editions/{slug}/my-entries", h.MyEntries)
		m.Post("/editions/{slug}/entries", h.CreateMyEntry)
		m.Patch("/entries/{entryId}", h.UpdateMyEntry)
		m.Delete("/entries/{entryId}", h.WithdrawMyEntry)

		m.With(ScoreGate).Get("/judging/assignments", h.MyJudgingAssignments)
		m.With(ScoreGate).Put("/judging/assignments/{assignmentId}/entries/{entryId}/score", h.SubmitScore)
	})
}

// RegisterAdmin mounts the operations surface. The caller applies AdminGate
// (competition.read), which is intentionally narrower than platform AdminGate so
// county officers can read without receiving the platform admin shell.
func (h *Handler) RegisterAdmin(r chi.Router) {
	// Read surface: competition.read.
	r.Get("/editions", h.ListAdminEditions)
	r.Get("/editions/{editionId}", h.GetAdminEdition)
	r.Get("/editions/{editionId}/categories", h.ListAdminCategories)
	r.Get("/editions/{editionId}/entries", h.ListAdminEntries)
	r.Get("/entries/{entryId}", h.GetAdminEntry)
	r.Get("/editions/{editionId}/judges", h.ListJudges)
	r.Get("/editions/{editionId}/assignments", h.ListAssignments)
	r.Get("/editions/{editionId}/conflicts", h.ListConflicts)
	r.Get("/editions/{editionId}/scores", h.ListScores)
	r.Get("/editions/{editionId}/votes/summary", h.VoteSummary)
	r.Get("/editions/{editionId}/vote-review-cases", h.ListReviewCases)
	r.Get("/editions/{editionId}/results", h.AdminResults)
	r.Get("/editions/{editionId}/awards", h.ListAwards)
	r.Get("/editions/{editionId}/consultation-responses", h.ListConsultationResponses)
	r.Get("/editions/{editionId}/consultation-responses/export", h.ExportConsultationResponses)

	// Edit surface: competition.edit.
	r.Group(func(er chi.Router) {
		er.Use(EditGate)
		er.Post("/editions", h.CreateEdition)
		er.Patch("/editions/{editionId}", h.UpdateEdition)
		er.Post("/editions/{editionId}/categories", h.CreateCategory)
		er.Patch("/categories/{categoryId}", h.UpdateCategory)
		er.Post("/editions/{editionId}/entries", h.CreateEntry)
		er.Patch("/entries/{entryId}", h.UpdateEntry)
		er.Post("/entries/{entryId}/eligibility", h.SetEligibility)
		er.Post("/editions/{editionId}/judges", h.CreateJudge)
		er.Patch("/judges/{judgeId}", h.UpdateJudge)
		er.Post("/editions/{editionId}/assignments", h.CreateAssignment)
		er.Patch("/assignments/{assignmentId}", h.UpdateAssignment)
		er.Post("/editions/{editionId}/conflicts", h.CreateConflict)
		er.Put("/scores", h.UpsertScoreAdmin)
		er.Post("/editions/{editionId}/scores/lock", h.LockScores)
		er.Post("/editions/{editionId}/vote-review-cases", h.CreateReviewCase)
		er.Post("/vote-review-cases/{caseId}/resolve", h.ResolveReviewCase)
	})

	// Publish surface: competition.publish.
	r.Group(func(pr chi.Router) {
		pr.Use(PublishGate)
		pr.Delete("/editions/{editionId}", h.DeleteEdition)
		pr.Delete("/categories/{categoryId}", h.DeleteCategory)
		pr.Delete("/entries/{entryId}", h.DeleteEntry)
		pr.Delete("/judges/{judgeId}", h.DeleteJudge)
		pr.Delete("/assignments/{assignmentId}", h.DeleteAssignment)
		pr.Delete("/conflicts/{conflictId}", h.DeleteConflict)
		pr.Post("/votes/{voteId}/invalidate", h.InvalidateVote)
		pr.Post("/editions/{editionId}/freeze", h.Freeze)
		pr.Post("/editions/{editionId}/recount", h.Recount)
		pr.Post("/editions/{editionId}/awards", h.CreateAward)
		pr.Patch("/awards/{awardId}", h.UpdateAward)
		pr.Post("/awards/{awardId}/publish", h.PublishAward)
		pr.Delete("/awards/{awardId}", h.DeleteAward)
	})
}

// AdminGate admits competition.read holders. Platform AdminGate would exclude
// county_officer, which the role contract explicitly grants read access.
func AdminGate(next http.Handler) http.Handler {
	return gate(contracts.PermCompetitionRead, "competition admin access required")(next)
}

func EditGate(next http.Handler) http.Handler {
	return gate(contracts.PermCompetitionEdit, "competition edit access required")(next)
}

func PublishGate(next http.Handler) http.Handler {
	return gate(contracts.PermCompetitionPublish, "competition publish access required")(next)
}

func ScoreGate(next http.Handler) http.Handler {
	return gate(contracts.PermCompetitionScore, "judging access required")(next)
}

func gate(permission, message string) func(http.Handler) http.Handler {
	rules := contracts.MustRoles()
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if rules.Can(string(middleware.RoleFromCtx(r.Context())), permission) {
				next.ServeHTTP(w, r)
				return
			}
			handler.WriteError(w, http.StatusForbidden, "FORBIDDEN", message)
		})
	}
}
