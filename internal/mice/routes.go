package mice

import (
	"github.com/go-chi/chi/v5"
)

// RegisterPublic mounts the guest-visible MICE routes. The caller's router
// already applies the public rate limiter.
func (h *Handler) RegisterPublic(r chi.Router) {
	r.Get("/events", h.handleListPublicEvents)
	r.Get("/events/{id}", h.handleGetPublicEvent)
	r.Get("/events/{id}/calendar.ics", h.handleEventCalendar)
	r.Get("/organizers/{slug}", h.handleGetPublicOrganizer)
	r.Get("/venues/{slug}", h.handleGetPublicVenue)
}

// RegisterAccount mounts the account routes under the authenticated
// /v1/mobile group.
func (h *Handler) RegisterAccount(r chi.Router) {
	r.Post("/events/{id}/registrations", h.handleCreateRegistration)
	r.Get("/events/{id}/registration", h.handleGetMyRegistration)
	r.Delete("/events/{id}/registration", h.handleCancelRegistration)
	r.Get("/registrations", h.handleListMyRegistrations)
	r.Get("/events/{id}/ticket", h.handleGetTicket)
	r.Post("/events/{id}/ticket-handoffs", h.handleTicketHandoff)
	r.Post("/events/{id}/checkins", h.handleCheckIn)
	r.Post("/events/{id}/checkin-scans", h.handleSyncOfflineScans)
	r.Post("/events/{id}/reports", h.handleReportEvent)
	r.Get("/events/{id}/notifications", h.handleListEventNotifications)
	r.Put("/events/{id}/reminder", h.handleSetReminder)
	r.Post("/events/{id}/save", h.handleSaveEvent)
	r.Get("/events/saved", h.handleListSavedEvents)
}

// RegisterAdmin mounts the MICE admin routes under the admin-gated
// /v1/admin group. Fine-grained permissions are enforced per handler.
func (h *Handler) RegisterAdmin(r chi.Router) {
	r.Get("/venues", h.handleAdminListVenues)
	r.Post("/venues", h.handleAdminCreateVenue)
	r.Get("/venues/{id}", h.handleAdminGetVenue)
	r.Patch("/venues/{id}", h.handleAdminUpdateVenue)
	r.Post("/venues/{id}/evidence", h.handleAdminAddVenueEvidence)
	r.Post("/venues/{id}/retire", h.handleAdminRetireVenue)

	r.Get("/organizers", h.handleAdminListOrganizers)
	r.Post("/organizers", h.handleAdminCreateOrganizer)
	r.Patch("/organizers/{id}", h.handleAdminUpdateOrganizer)
	r.Post("/organizers/{id}/verification", h.handleAdminOrganizerVerification)
	r.Post("/organizers/{id}/evidence-requests", h.handleAdminRequestOrganizerEvidence)

	r.Get("/events", h.handleAdminListEvents)
	r.Post("/events", h.handleAdminCreateEvent)
	r.Get("/events/{id}", h.handleAdminGetEvent)
	r.Patch("/events/{id}", h.handleAdminUpdateEvent)
	r.Post("/events/{id}/publish", h.handleAdminPublishEvent)
	r.Post("/events/{id}/lifecycle", h.handleAdminEventLifecycle)
	r.Post("/events/{id}/sessions", h.handleAdminCreateSession)
	r.Patch("/events/{id}/sessions/{sessionId}", h.handleAdminUpdateSession)
	r.Post("/events/{id}/people", h.handleAdminCreatePerson)
	r.Patch("/events/{id}/people/{personId}", h.handleAdminUpdatePerson)
	r.Delete("/events/{id}/people/{personId}", h.handleAdminRemoveSpeaker)
	r.Post("/events/{id}/people/{personId}/consent", h.handleAdminSetSpeakerConsent)
	r.Post("/sessions/{sessionId}/speakers", h.handleAdminLinkSpeaker)
	r.Post("/events/{id}/change-notices", h.handleDraftChangeNotice)
	r.Get("/events/{id}/registrations", h.handleAdminListRegistrations)
	r.Patch("/events/{id}/registrations/{registrationId}", h.handleAdminUpdateRegistration)
	r.Post("/events/{id}/waitlist-offers", h.handleAdminCreateWaitlistOffer)
	r.Post("/events/{id}/waitlist-offers/{registrationId}/decision", h.handleAdminWaitlistOfferDecision)
	r.Get("/events/{id}/checkins", h.handleAdminListCheckIns)
	r.Post("/events/{id}/checkins/{checkInId}/undo", h.handleAdminUndoCheckIn)
	r.Get("/mice/audit", h.handleAdminListMiceAudit)

	r.Get("/event-reports", h.handleAdminListReports)
	r.Post("/event-reports/{id}/decision", h.handleAdminReportDecision)
}
