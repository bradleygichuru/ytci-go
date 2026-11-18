package fixtures

import "strings"

// Route maps one contract operation to a fixture base and success status.
type Route struct {
	Method  string
	Pattern string
	Base    string
	Status  int
	// NoContent routes return 204 and ignore the fixture body. An empty Base
	// with NoContent false marks a generated response (text/calendar).
	NoContent bool
	// Primary marks routes that must ship default, empty, error and stale fixtures.
	Primary bool
}

// Routes is the mock-server route table. It is validated against the frozen
// OpenAPI document in internal/contracts.
var Routes = []Route{
	// MICE — public
	{"GET", "/v1/public/events", "mice/events", 200, false, true},
	{"GET", "/v1/public/events/{id}", "mice/event", 200, false, false},
	{"GET", "/v1/public/events/{id}/calendar.ics", "", 200, false, false},
	{"GET", "/v1/public/organizers/{slug}", "mice/organizer", 200, false, false},
	{"GET", "/v1/public/venues/{slug}", "mice/venue", 200, false, false},
	// MICE — account
	{"POST", "/v1/mobile/events/{id}/registrations", "mice/registration", 201, false, false},
	{"POST", "/v1/mobile/events/{id}/ticket-handoffs", "mice/ticket-handoff", 201, false, false},
	{"GET", "/v1/mobile/events/{id}/registration", "mice/registration", 200, false, false},
	{"DELETE", "/v1/mobile/events/{id}/registration", "mice/registration", 200, false, false},
	{"GET", "/v1/mobile/registrations", "mice/registrations", 200, false, false},
	{"GET", "/v1/mobile/events/{id}/ticket", "mice/ticket", 200, false, false},
	{"POST", "/v1/mobile/events/{id}/checkins", "mice/checkin", 201, false, false},
	{"POST", "/v1/mobile/events/{id}/checkin-scans", "mice/checkin-scans", 200, false, false},
	{"POST", "/v1/mobile/events/{id}/reports", "mice/ack", 202, false, false},
	{"GET", "/v1/mobile/events/{id}/notifications", "mice/notifications", 200, false, false},
	{"PUT", "/v1/mobile/events/{id}/reminder", "mice/registration", 200, false, false},
	// MICE — admin
	{"GET", "/v1/admin/venues", "mice/admin-venues", 200, false, false},
	{"POST", "/v1/admin/venues", "mice/venue", 201, false, false},
	{"PATCH", "/v1/admin/venues/{id}", "mice/venue", 200, false, false},
	{"GET", "/v1/admin/organizers", "mice/admin-organizers", 200, false, false},
	{"POST", "/v1/admin/organizers", "mice/organizer", 201, false, false},
	{"PATCH", "/v1/admin/organizers/{id}", "mice/organizer", 200, false, false},
	{"POST", "/v1/admin/organizers/{id}/verification", "mice/organizer", 200, false, false},
	{"GET", "/v1/admin/events", "mice/admin-events", 200, false, false},
	{"POST", "/v1/admin/events/{id}/publish", "mice/event", 200, false, false},
	{"POST", "/v1/admin/events/{id}/change-notices", "mice/change-notice", 201, false, false},
	{"GET", "/v1/admin/events/{id}/registrations", "mice/admin-registrations", 200, false, false},
	{"GET", "/v1/admin/events/{id}/checkins", "mice/admin-checkins", 200, false, false},
	{"GET", "/v1/admin/event-reports", "mice/admin-event-reports", 200, false, false},
	{"POST", "/v1/admin/event-reports/{id}/decision", "mice/event-report", 200, false, false},
	// Recognition — public/account
	{"GET", "/v1/public/leaderboard", "recognition/leaderboard", 200, false, true},
	{"GET", "/v1/public/seasons/{key}", "recognition/season", 200, false, false},
	{"GET", "/v1/mobile/recognition", "recognition/overview", 200, false, true},
	{"GET", "/v1/mobile/recognition/badges", "recognition/badges", 200, false, true},
	{"GET", "/v1/mobile/recognition/badges/{key}", "recognition/badge", 200, false, false},
	{"PATCH", "/v1/mobile/recognition/badges/{key}", "recognition/badge", 200, false, false},
	{"GET", "/v1/mobile/recognition/missions", "recognition/missions", 200, false, false},
	{"GET", "/v1/mobile/recognition/seasons/{key}", "recognition/season-progress", 200, false, false},
	{"GET", "/v1/mobile/recognition/passport", "recognition/passport", 200, false, false},
	{"GET", "/v1/mobile/recognition/leaderboard", "recognition/leaderboard-state", 200, false, false},
	{"POST", "/v1/mobile/recognition/leaderboard/join", "recognition/leaderboard-state", 201, false, false},
	{"DELETE", "/v1/mobile/recognition/leaderboard/join", "recognition/leaderboard-state", 200, false, false},
	{"PATCH", "/v1/mobile/recognition/display-name", "recognition/display-name", 200, false, false},
	{"POST", "/v1/mobile/recognition/reports", "recognition/ack", 202, false, false},
	// Recognition — admin
	{"GET", "/v1/admin/recognition/rules", "recognition/admin-rules", 200, false, false},
	{"POST", "/v1/admin/recognition/rules", "recognition/rule", 201, false, false},
	{"POST", "/v1/admin/recognition/rules/{id}/publish", "recognition/rule", 200, false, false},
	{"POST", "/v1/admin/recognition/rules/{id}/simulate", "recognition/simulation", 200, false, false},
	{"GET", "/v1/admin/recognition/badges", "recognition/admin-badges", 200, false, false},
	{"POST", "/v1/admin/recognition/badges", "recognition/badge-definition", 201, false, false},
	{"GET", "/v1/admin/recognition/seasons", "recognition/admin-seasons", 200, false, false},
	{"POST", "/v1/admin/recognition/seasons", "recognition/season", 201, false, false},
	{"POST", "/v1/admin/recognition/seasons/{id}/close", "recognition/season", 200, false, false},
	{"GET", "/v1/admin/recognition/moderation", "recognition/admin-moderation", 200, false, false},
	{"POST", "/v1/admin/recognition/moderation/{id}/decision", "recognition/moderation-case", 200, false, false},
	{"GET", "/v1/admin/recognition/backfill", "recognition/admin-backfill", 200, false, false},
	{"POST", "/v1/admin/recognition/backfill", "recognition/backfill", 201, false, false},
	{"POST", "/v1/admin/recognition/backfill/{id}/approve", "recognition/backfill", 200, false, false},
	{"POST", "/v1/admin/recognition/adjustments", "recognition/xp-entry", 201, false, false},
	{"GET", "/v1/admin/recognition/kill-switches", "recognition/kill-switches", 200, false, false},
	{"PATCH", "/v1/admin/recognition/kill-switches/{key}", "recognition/kill-switch", 200, false, false},
	{"GET", "/v1/admin/recognition/audit", "recognition/audit", 200, false, false},
	// Species — public/account
	{"GET", "/v1/public/species", "species/list", 200, false, true},
	{"GET", "/v1/public/species/{slug}", "species/detail", 200, false, false},
	{"GET", "/v1/public/species/{slug}/languages", "species/languages", 200, false, false},
	{"GET", "/v1/mobile/species/saved", "species/saved", 200, false, false},
	{"POST", "/v1/mobile/species/{slug}/save", "species/ack", 201, false, false},
	{"DELETE", "/v1/mobile/species/{slug}/save", "", 204, true, false},
	{"GET", "/v1/mobile/species/packs/{language}", "species/pack", 200, false, false},
	// Species — admin
	{"GET", "/v1/admin/species", "species/admin-list", 200, false, false},
	{"POST", "/v1/admin/species", "species/detail", 201, false, false},
	{"GET", "/v1/admin/species/{id}", "species/detail", 200, false, false},
	{"PATCH", "/v1/admin/species/{id}", "species/detail", 200, false, false},
	{"POST", "/v1/admin/species/{id}/variants", "species/variant", 200, false, false},
	{"POST", "/v1/admin/species/{id}/publish", "species/detail", 200, false, false},
	{"POST", "/v1/admin/species/{id}/unpublish", "species/detail", 200, false, false},
	{"GET", "/v1/admin/species/review", "species/admin-review", 200, false, false},
	{"POST", "/v1/admin/species/review/{id}/decision", "species/review-item", 200, false, false},
	{"GET", "/v1/admin/species/sources", "species/sources", 200, false, false},
	{"POST", "/v1/admin/species/sources", "species/source", 201, false, false},
	{"GET", "/v1/admin/species/rights", "species/rights", 200, false, false},
	{"POST", "/v1/admin/species/rights", "species/rights-record", 201, false, false},
	// Analytics
	{"POST", "/v1/mobile/analytics/installs", "analytics/identity", 201, false, false},
	{"POST", "/v1/mobile/analytics/batches", "analytics/batch-ack", 202, false, false},
	{"PUT", "/v1/mobile/analytics/consent", "analytics/consent", 200, false, false},
	{"DELETE", "/v1/mobile/analytics/consent", "analytics/consent", 200, false, false},
	{"GET", "/v1/mobile/analytics/export", "analytics/export", 200, false, false},
	{"GET", "/v1/admin/analytics/overview", "analytics/overview", 200, false, true},
	{"GET", "/v1/admin/analytics/journeys", "analytics/journeys", 200, false, false},
	{"GET", "/v1/admin/analytics/funnels", "analytics/funnels", 200, false, false},
	{"GET", "/v1/admin/analytics/retention", "analytics/retention", 200, false, false},
	{"GET", "/v1/admin/analytics/adoption", "analytics/adoption", 200, false, false},
	{"GET", "/v1/admin/analytics/quality", "analytics/quality", 200, false, false},
	{"POST", "/v1/admin/analytics/export", "analytics/ack", 202, false, false},
	{"POST", "/v1/admin/analytics/erasure", "analytics/erasure", 202, false, false},
	// Platform
	{"GET", "/v1/public/contracts", "platform/contracts", 200, false, false},
}

// Match resolves a method and concrete path to a route.
func Match(method, path string) (Route, bool) {
	for _, route := range Routes {
		if route.Method != method {
			continue
		}
		if matchPattern(route.Pattern, path) {
			return route, true
		}
	}
	return Route{}, false
}

func matchPattern(pattern, path string) bool {
	p := strings.Split(strings.Trim(pattern, "/"), "/")
	a := strings.Split(strings.Trim(path, "/"), "/")
	if len(p) != len(a) {
		return false
	}
	for i := range p {
		if strings.HasPrefix(p[i], "{") && strings.HasSuffix(p[i], "}") {
			if a[i] == "" {
				return false
			}
			continue
		}
		if p[i] != a[i] {
			return false
		}
	}
	return true
}
