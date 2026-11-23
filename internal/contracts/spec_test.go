package contracts_test

import (
	"sort"
	"testing"

	"github.com/getkin/kin-openapi/openapi3"

	"github.com/bradleygichuru/ytci-go/internal/contracts"
)

// frozenOperations is the change-control list. Adding, removing or renaming an
// operation requires a contract change ticket and a version bump.
var frozenOperations = map[string][]string{
	"/v1/public/events":                    {"get"},
	"/v1/public/events/{id}":               {"get"},
	"/v1/public/events/{id}/calendar.ics":  {"get"},
	"/v1/public/organizers/{slug}":         {"get"},
	"/v1/public/venues/{slug}":             {"get"},
	"/v1/mobile/events/{id}/registrations": {"post"},

	"/v1/mobile/events/{id}/registration":        {"get", "delete"},
	"/v1/mobile/registrations":                   {"get"},
	"/v1/mobile/events/{id}/ticket":              {"get"},
	"/v1/mobile/events/{id}/checkins":            {"post"},
	"/v1/mobile/events/{id}/checkin-scans":       {"post"},
	"/v1/mobile/events/{id}/reports":             {"post"},
	"/v1/mobile/events/{id}/notifications":       {"get"},
	"/v1/mobile/events/{id}/reminder":            {"put"},
	"/v1/admin/venues":                           {"get", "post"},
	"/v1/admin/venues/{id}":                      {"patch"},
	"/v1/admin/organizers":                       {"get", "post"},
	"/v1/admin/organizers/{id}":                  {"patch"},
	"/v1/admin/organizers/{id}/verification":     {"post"},
	"/v1/admin/events":                           {"get", "post"},
	"/v1/admin/events/{id}":                      {"get", "patch"},
	"/v1/admin/events/{id}/sessions":             {"post"},
	"/v1/admin/events/{id}/sessions/{sessionId}": {"patch"},
	"/v1/admin/events/{id}/people":               {"post"},
	"/v1/admin/sessions/{sessionId}/speakers":    {"post"},
	"/v1/mobile/events/{id}/save":                {"post"},
	"/v1/mobile/events/saved":                    {"get"},
	"/v1/mobile/events/{id}/ticket-handoffs":     {"post"},
	"/v1/admin/recognition/badges/{key}/publish": {"post"},
	"/v1/admin/recognition/seasons/{id}/publish": {"post"},
	"/v1/admin/recognition/seasons/{id}/freeze":  {"post"},
	"/v1/admin/species/{id}/names":               {"post"},
	"/v1/admin/species/{id}/media":               {"post"},
	"/v1/admin/species/{id}/audio":               {"post"},

	"/v1/admin/events/{id}/publish":                                   {"post"},
	"/v1/admin/events/{id}/change-notices":                            {"post"},
	"/v1/admin/events/{id}/registrations":                             {"get"},
	"/v1/admin/events/{id}/checkins":                                  {"get"},
	"/v1/admin/events/{id}/lifecycle":                                 {"post"},
	"/v1/admin/events/{id}/registrations/{registrationId}":            {"patch"},
	"/v1/admin/events/{id}/waitlist-offers":                           {"post"},
	"/v1/admin/events/{id}/waitlist-offers/{registrationId}/decision": {"post"},
	"/v1/admin/events/{id}/checkins/{checkInId}/undo":                 {"post"},
	"/v1/admin/events/{id}/people/{personId}":                         {"patch", "delete"},
	"/v1/admin/events/{id}/people/{personId}/consent":                 {"post"},
	"/v1/admin/mice/audit":                                            {"get"},
	"/v1/admin/event-reports":                                         {"get"},
	"/v1/admin/event-reports/{id}/decision":                           {"post"},
	"/v1/public/leaderboard":                                          {"get"},
	"/v1/public/seasons/{key}":                                        {"get"},
	"/v1/mobile/recognition":                                          {"get"},
	"/v1/mobile/recognition/badges":                                   {"get"},
	"/v1/mobile/recognition/badges/{key}":                             {"get", "patch"},
	"/v1/mobile/recognition/missions":                                 {"get"},
	"/v1/mobile/recognition/seasons/{key}":                            {"get"},
	"/v1/mobile/recognition/passport":                                 {"get"},
	"/v1/mobile/recognition/leaderboard":                              {"get"},
	"/v1/mobile/recognition/leaderboard/join":                         {"post", "delete"},
	"/v1/mobile/recognition/display-name":                             {"patch"},
	"/v1/mobile/recognition/reports":                                  {"post"},
	"/v1/admin/recognition/rules":                                     {"get", "post"},
	"/v1/admin/recognition/rules/{id}/publish":                        {"post"},
	"/v1/admin/recognition/rules/{id}/simulate":                       {"post"},
	"/v1/admin/recognition/badges":                                    {"get", "post"},
	"/v1/admin/recognition/seasons":                                   {"get", "post"},
	"/v1/admin/recognition/seasons/{id}/close":                        {"post"},
	"/v1/admin/recognition/moderation":                                {"get"},
	"/v1/admin/recognition/moderation/{id}/decision":                  {"post"},
	"/v1/admin/recognition/backfill":                                  {"get", "post"},
	"/v1/admin/recognition/backfill/{id}/approve":                     {"post"},
	"/v1/admin/recognition/adjustments":                               {"post"},
	"/v1/admin/recognition/kill-switches":                             {"get"},
	"/v1/admin/recognition/kill-switches/{key}":                       {"patch"},
	"/v1/admin/recognition/audit":                                     {"get"},
	"/v1/public/species":                                              {"get"},
	"/v1/public/species/{slug}":                                       {"get"},
	"/v1/public/species/{slug}/languages":                             {"get"},
	"/v1/mobile/species/saved":                                        {"get"},
	"/v1/mobile/species/{slug}/save":                                  {"post", "delete"},
	"/v1/mobile/species/packs/{language}":                             {"get"},
	"/v1/admin/species":                                               {"get", "post"},
	"/v1/admin/species/{id}":                                          {"get", "patch"},
	"/v1/admin/species/{id}/variants":                                 {"post"},
	"/v1/admin/species/{id}/publish":                                  {"post"},
	"/v1/admin/species/{id}/unpublish":                                {"post"},
	"/v1/admin/species/review":                                        {"get"},
	"/v1/admin/species/review/{id}/decision":                          {"post"},
	"/v1/admin/species/sources":                                       {"get", "post"},
	"/v1/admin/species/rights":                                        {"get", "post"},
	"/v1/mobile/analytics/installs":                                   {"post"},
	"/v1/mobile/analytics/batches":                                    {"post"},
	"/v1/mobile/analytics/consent":                                    {"put", "delete"},
	"/v1/mobile/analytics/export":                                     {"get"},
	"/v1/admin/analytics/overview":                                    {"get"},
	"/v1/admin/analytics/journeys":                                    {"get"},
	"/v1/admin/analytics/funnels":                                     {"get"},
	"/v1/admin/analytics/retention":                                   {"get"},
	"/v1/admin/analytics/adoption":                                    {"get"},
	"/v1/admin/analytics/quality":                                     {"get"},
	"/v1/admin/analytics/export":                                      {"post"},
	"/v1/admin/analytics/erasure":                                     {"post"},
	"/v1/public/contracts":                                            {"get"},
}

var allowedTags = map[string]bool{
	"mice": true, "recognition": true, "species": true, "analytics": true, "platform": true,
}

func TestOpenAPIValid(t *testing.T) {
	doc, err := contracts.LoadSpec()
	if err != nil {
		t.Fatal(err)
	}
	if doc.Info == nil || doc.Info.Version != "1.0.0" {
		t.Fatalf("unexpected info version: %+v", doc.Info)
	}
	if len(doc.Tags) != len(allowedTags) {
		t.Fatalf("expected %d tags, got %d", len(allowedTags), len(doc.Tags))
	}
	if len(doc.Paths.Map()) != len(frozenOperations) {
		t.Fatalf("path count = %d, want %d (change control?)", len(doc.Paths.Map()), len(frozenOperations))
	}
	opIDs := map[string]string{}
	count := 0
	for path, item := range doc.Paths.Map() {
		want, ok := frozenOperations[path]
		if !ok {
			t.Fatalf("unfrozen path %q", path)
		}
		got := methodsOn(item)
		if len(got) != len(want) {
			t.Fatalf("%s methods = %v, want %v", path, got, want)
		}
		sort.Strings(got)
		sort.Strings(want)
		for i := range want {
			if got[i] != want[i] {
				t.Fatalf("%s methods = %v, want %v", path, got, want)
			}
		}
		for _, op := range item.Operations() {
			count++
			if op.OperationID == "" {
				t.Fatalf("%s has an operation without operationId", path)
			}
			if prev, dup := opIDs[op.OperationID]; dup {
				t.Fatalf("duplicate operationId %q at %s and %s", op.OperationID, prev, path)
			}
			opIDs[op.OperationID] = path
			if len(op.Tags) == 0 {
				t.Fatalf("%s %s has no tag", path, op.OperationID)
			}
			for _, tag := range op.Tags {
				if !allowedTags[tag] {
					t.Fatalf("%s has unknown tag %q", path, tag)
				}
			}
			if len(op.Responses.Map()) == 0 {
				t.Fatalf("%s has no responses", path)
			}
			if op.Responses.Value("200") == nil {
				if op.Responses.Value("201") == nil {
					if op.Responses.Value("202") == nil {
						if op.Responses.Value("204") == nil {
							t.Fatalf("%s %s has no success response", path, op.OperationID)
						}
					}
				}
			}
			if op.OperationID != "getContractVersions" && isAccountPath(path) {
				has401 := op.Responses.Value("401") != nil
				has403 := op.Responses.Value("403") != nil
				if isAdminPath(path) {
					if !has401 && !has403 {
						t.Fatalf("%s %s has neither 401 nor 403", path, op.OperationID)
					}
				} else if !has401 {
					t.Fatalf("%s %s has no 401 response", path, op.OperationID)
				}
			}
		}
	}
	if count != 120 {
		t.Fatalf("operation count = %d, want 120", count)
	}
	for path := range frozenOperations {
		if _, ok := doc.Paths.Map()[path]; !ok {
			t.Fatalf("missing frozen path %q", path)
		}
	}
}

func TestOpenAPIErrorShapes(t *testing.T) {
	doc, err := contracts.LoadSpec()
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"Error", "ValidationError", "Page", "EventDetail", "RecognitionOverview", "SpeciesDetail", "AnalyticsBatch"} {
		if doc.Components.Schemas[name] == nil {
			t.Fatalf("missing schema %q", name)
		}
	}
}

func methodsOn(item *openapi3.PathItem) []string {
	var out []string
	if item.Get != nil {
		out = append(out, "get")
	}
	if item.Post != nil {
		out = append(out, "post")
	}
	if item.Put != nil {
		out = append(out, "put")
	}
	if item.Patch != nil {
		out = append(out, "patch")
	}
	if item.Delete != nil {
		out = append(out, "delete")
	}
	if item.Head != nil {
		out = append(out, "head")
	}
	if item.Options != nil {
		out = append(out, "options")
	}
	return out
}

func isAccountPath(path string) bool {
	return len(path) >= 10 && (path[:10] == "/v1/mobile" || path[:9] == "/v1/admin")
}

func isAdminPath(path string) bool {
	return len(path) >= 9 && path[:9] == "/v1/admin"
}
