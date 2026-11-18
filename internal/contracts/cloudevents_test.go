package contracts_test

import (
	"sort"
	"strings"
	"testing"

	"github.com/bradleygichuru/ytci-go/internal/contracts"
)

func TestCloudEventTypesAreFrozen(t *testing.T) {
	ce, err := contracts.LoadCloudEvents()
	if err != nil {
		t.Fatal(err)
	}
	if got, want := ce.Types(), append([]string(nil), contracts.CloudEventTypes...); !equalSorted(got, want) {
		t.Fatalf("types = %v, want %v", got, want)
	}
	for _, eventType := range contracts.CloudEventTypes {
		spec, ok := ce.Spec(eventType)
		if !ok {
			t.Fatalf("missing spec for %s", eventType)
		}
		if spec.Category == "" || spec.SubjectType == "" || spec.Description == "" {
			t.Fatalf("%s is missing category/subject/description", eventType)
		}
		if spec.XP == nil {
			t.Fatalf("%s has no xp value", eventType)
		}
		if len(spec.Verification) == 0 || len(spec.Privacy) == 0 {
			t.Fatalf("%s has no verification or privacy classes", eventType)
		}
	}
}

func TestOccurrenceKeys(t *testing.T) {
	ce, err := contracts.LoadCloudEvents()
	if err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		eventType string
		values    map[string]string
		want      string
	}{
		{"ke.event.attended.v1", map[string]string{"event_id": "evt_1"}, "event.attendance:evt_1"},
		{"ke.course.completed.v1", map[string]string{"course_id": "c1", "revision": "3"}, "course:c1:3"},
		{"ke.heritage.county.collected.v1", map[string]string{"county_gid": "47"}, "heritage.county:47"},
	}
	for _, tc := range cases {
		got, err := ce.OccurrenceKey(tc.eventType, tc.values)
		if err != nil {
			t.Fatalf("%s: %v", tc.eventType, err)
		}
		if got != tc.want {
			t.Fatalf("%s = %q, want %q", tc.eventType, got, tc.want)
		}
	}
	if _, err := ce.OccurrenceKey("ke.course.completed.v1", map[string]string{"course_id": "c1"}); err == nil {
		t.Fatal("expected an error for an unfilled placeholder")
	}
	if _, err := ce.OccurrenceKey("unknown", nil); err == nil {
		t.Fatal("expected an error for an unknown event type")
	}
}

func TestAwardKey(t *testing.T) {
	got := contracts.AwardKey("event.attendance", "reader-1", "event", "evt_1")
	if got != "event.attendance|reader-1|event|evt_1" {
		t.Fatalf("award key = %q", got)
	}
	r := contracts.MustRoles()
	_ = r
}

func TestPayloadValidation(t *testing.T) {
	ce, err := contracts.LoadCloudEvents()
	if err != nil {
		t.Fatal(err)
	}
	valid := map[string]any{
		"source_type":         "event",
		"source_id":           "evt_1",
		"occurrence_key":      "event.attendance:evt_1",
		"verification_method": "staff_scan",
		"verification_result": "verified",
	}
	if err := ce.ValidatePayload(valid); err != nil {
		t.Fatalf("valid payload rejected: %v", err)
	}
	for _, banned := range []string{"name", "email", "latitude", "analytics_id", "free_text"} {
		payload := map[string]any{}
		for k, v := range valid {
			payload[k] = v
		}
		payload[banned] = "x"
		if err := ce.ValidatePayload(payload); err == nil {
			t.Fatalf("banned field %q was accepted", banned)
		}
	}
	unknown := map[string]any{"unexpected": "x"}
	if err := ce.ValidatePayload(unknown); err == nil {
		t.Fatal("unknown field was accepted")
	}
	bad := map[string]any{
		"source_type":         "event",
		"source_id":           "evt_1",
		"occurrence_key":      "event.attendance:evt_1",
		"verification_method": "staff_scan",
		"verification_result": "client_says_verified",
	}
	if err := ce.ValidatePayload(bad); err == nil {
		t.Fatal("invalid verification_result was accepted")
	}
	missing := map[string]any{"source_type": "event"}
	if err := ce.ValidatePayload(missing); err == nil {
		t.Fatal("missing required fields were accepted")
	}
}

func TestVerificationEffects(t *testing.T) {
	for _, result := range []string{"verified", "attested", "moderated", "server_recorded"} {
		if !contracts.Awardable(result) {
			t.Fatalf("%s should award XP", result)
		}
	}
	if !contracts.ProgressOnly("self_declared") {
		t.Fatal("self_declared must be progress-only")
	}
	if contracts.Awardable("self_declared") {
		t.Fatal("self_declared must never award XP")
	}
	if contracts.Awardable("rejected") {
		t.Fatal("rejected must never award XP")
	}
}

func TestIdempotencyRules(t *testing.T) {
	ce, err := contracts.LoadCloudEvents()
	if err != nil {
		t.Fatal(err)
	}
	id := ce.Idempotency
	if id.Delivery != "at_least_once" || id.Effect != "exactly_once" {
		t.Fatalf("delivery/effect = %s/%s", id.Delivery, id.Effect)
	}
	if id.TransportKey != "event.id" || !strings.Contains(id.AwardKeyFormat, "rule_key") {
		t.Fatalf("unexpected keys: %+v", id)
	}
	if id.RuleVersionInAwardKey {
		t.Fatal("rule version must not be in the award key")
	}
	if len(id.AwardUniqueness) != 2 {
		t.Fatalf("expected two award uniqueness keys, got %d", len(id.AwardUniqueness))
	}
	if len(id.EventUniqueness) != 2 || id.EventUniqueness[0] != "source" || id.EventUniqueness[1] != "id" {
		t.Fatalf("event uniqueness = %v", id.EventUniqueness)
	}
}

func equalSorted(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	ac := append([]string(nil), a...)
	bc := append([]string(nil), b...)
	sort.Strings(ac)
	sort.Strings(bc)
	for i := range ac {
		if ac[i] != bc[i] {
			return false
		}
	}
	return true
}
