package contracts_test

import (
	"sort"
	"testing"

	"github.com/getkin/kin-openapi/openapi3"

	"github.com/bradleygichuru/ytci-go/internal/contracts"
)

func enumValues(t *testing.T, doc *openapi3.T, schemaName, property string) []string {
	t.Helper()
	ref, ok := doc.Components.Schemas[schemaName]
	if !ok || ref.Value == nil {
		t.Fatalf("schema %q not found", schemaName)
	}
	prop, ok := findProperty(ref.Value, property)
	if !ok || prop.Value == nil {
		t.Fatalf("property %q.%q not found", schemaName, property)
	}
	out := make([]string, 0, len(prop.Value.Enum))
	for _, value := range prop.Value.Enum {
		if str, ok := value.(string); ok {
			out = append(out, str)
		}
	}
	sort.Strings(out)
	return out
}

func findProperty(schema *openapi3.Schema, name string) (*openapi3.SchemaRef, bool) {
	if schema == nil {
		return nil, false
	}
	if prop, ok := schema.Properties[name]; ok {
		return prop, true
	}
	for _, parent := range schema.AllOf {
		if prop, ok := findProperty(parent.Value, name); ok {
			return prop, true
		}
	}
	return nil, false
}

func stringsOf(values []string) []string {
	out := append([]string(nil), values...)
	sort.Strings(out)
	return out
}

func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func TestMiceVocabularyMatchesOpenAPI(t *testing.T) {
	doc, err := contracts.LoadSpec()
	if err != nil {
		t.Fatal(err)
	}
	mice, err := contracts.LoadMice()
	if err != nil {
		t.Fatal(err)
	}
	cases := [][2][]string{
		{enumValues(t, doc, "EventSummary", "eventClass"), stringsOf(mice.EventClass)},
		{enumValues(t, doc, "EventSummary", "format"), stringsOf(mice.Format)},
		{enumValues(t, doc, "EventSummary", "access"), stringsOf(mice.Access)},
		{enumValues(t, doc, "Registration", "status"), stringsOf(mice.Registration.Statuses)},
		{enumValues(t, doc, "EventSummary", "registrationMode"), stringsOf(mice.Registration.Modes)},
		{enumValues(t, doc, "CheckIn", "method"), stringsOf(mice.Checkin.Methods)},
		{enumValues(t, doc, "EventNotification", "kind"), stringsOf(mice.Notifications.Kinds)},
		{enumValues(t, doc, "OrganizerRef", "trustTier"), stringsOf(mice.Trust.Tiers)},
		{enumValues(t, doc, "OrganizerProfile", "verificationStatus"), stringsOf(mice.Trust.VerificationStatuses)},
		{enumValues(t, doc, "EventReport", "reason"), stringsOf(mice.Reports.Reasons)},
	}
	for _, tc := range cases {
		if !equalStrings(tc[0], tc[1]) {
			t.Fatalf("OpenAPI %v != contract %v", tc[0], tc[1])
		}
	}
	if mice.Registration.QuestionsMax != 2 {
		t.Fatalf("questions max = %d, want 2", mice.Registration.QuestionsMax)
	}
	if !equalStrings(stringsOf(mice.Lifecycle.DerivedView), stringsOf([]string{"ongoing", "past", "upcoming"})) {
		t.Fatalf("derived view = %v", mice.Lifecycle.DerivedView)
	}
	if mice.Lifecycle.DeprecatedAliases["scheduled"] != "published" {
		t.Fatal("scheduled must be a deprecated alias of published")
	}
	if !equalStrings(stringsOf(mice.Accessibility.Values), stringsOf([]string{"no", "unknown", "yes"})) {
		t.Fatalf("accessibility values = %v", mice.Accessibility.Values)
	}
}

func TestSpeciesVocabularyMatchesOpenAPI(t *testing.T) {
	doc, err := contracts.LoadSpec()
	if err != nil {
		t.Fatal(err)
	}
	species, err := contracts.LoadSpecies()
	if err != nil {
		t.Fatal(err)
	}
	cases := [][2][]string{
		{enumValues(t, doc, "SpeciesVariant", "coverageTier"), stringsOf(species.CoverageTiers)},
		{enumValues(t, doc, "SpeciesVariant", "reviewStatus"), stringsOf(species.ReviewStatuses)},
		{enumValues(t, doc, "SpeciesSummary", "publicationState"), stringsOf(species.PublicationStates)},
		{enumValues(t, doc, "SpeciesReviewItem", "stage"), stringsOf(species.ReviewStages)},
		{enumValues(t, doc, "SpeciesSummary", "iucnCategory"), stringsOf(species.IUCNCategories)},
		{enumValues(t, doc, "WhereToSee", "evidenceType"), stringsOf(species.WhereToSee.EvidenceTypes)},
	}
	for _, tc := range cases {
		if !equalStrings(tc[0], tc[1]) {
			t.Fatalf("OpenAPI %v != contract %v", tc[0], tc[1])
		}
	}
}

func TestAnalyticsVocabularyMatchesOpenAPI(t *testing.T) {
	doc, err := contracts.LoadSpec()
	if err != nil {
		t.Fatal(err)
	}
	analytics, err := contracts.LoadAnalytics()
	if err != nil {
		t.Fatal(err)
	}
	if !equalStrings(enumValues(t, doc, "AnalyticsEvent", "family"), stringsOf(analytics.EventFamilies)) {
		t.Fatalf("event families drifted")
	}
	if !equalStrings(enumValues(t, doc, "AnalyticsConsent", "state"), stringsOf(analytics.Consent.States)) {
		t.Fatalf("consent states drifted")
	}
	journey := doc.Components.Parameters["Journey"]
	if journey == nil || journey.Value == nil || journey.Value.Schema == nil || journey.Value.Schema.Value == nil {
		t.Fatal("Journey parameter is missing")
	}
	var journeyEnums []string
	for _, value := range journey.Value.Schema.Value.Enum {
		if str, ok := value.(string); ok {
			journeyEnums = append(journeyEnums, str)
		}
	}
	if !equalStrings(stringsOf(journeyEnums), stringsOf(analytics.Journeys)) {
		t.Fatalf("journeys drifted: %v vs %v", journey.Value.Schema.Value.Enum, analytics.Journeys)
	}
	if analytics.Retention.RawEventsMonths != 14 || analytics.Retention.DailyAggregateMonths != 24 {
		t.Fatalf("retention drifted: %+v", analytics.Retention)
	}
	if analytics.Admin.SuppressionThreshold != 10 {
		t.Fatalf("suppression threshold = %d", analytics.Admin.SuppressionThreshold)
	}
	if analytics.Queue.MaxEvents != 500 || analytics.Queue.MaxBytes != 204800 || analytics.Queue.MaxAgeDays != 7 {
		t.Fatalf("queue caps drifted: %+v", analytics.Queue)
	}
}

func TestContractVersionsMatchOpenAPI(t *testing.T) {
	doc, err := contracts.LoadSpec()
	if err != nil {
		t.Fatal(err)
	}
	if doc.Info.Version != "1.0.0" {
		t.Fatalf("OpenAPI version = %q", doc.Info.Version)
	}
	mice, _ := contracts.LoadMice()
	species, _ := contracts.LoadSpecies()
	analytics, _ := contracts.LoadAnalytics()
	ce, _ := contracts.LoadCloudEvents()
	roles, _ := contracts.LoadRoles()
	for name, version := range map[string]string{
		"mice":        mice.Version,
		"species":     species.Version,
		"analytics":   analytics.Version,
		"cloudevents": ce.Version,
		"roles":       roles.Version,
	} {
		if version != "1.0.0" {
			t.Fatalf("%s contract version = %q", name, version)
		}
	}
}
