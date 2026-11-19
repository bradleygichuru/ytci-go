package contracts

import (
	"fmt"
	"io/fs"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"

	"github.com/bradleygichuru/ytci-go/api"
)

// CloudEventTypes is the frozen v1 recognition event vocabulary.
// It must match recognition.event_types (migration 0030).
var CloudEventTypes = []string{
	"ke.heritage.visit.verified.v1",
	"ke.heritage.trail.completed.v1",
	"ke.heritage.county.collected.v1",
	"ke.conservation.participation.verified.v1",
	"ke.challenge.completed.v1",
	"ke.course.completed.v1",
	"ke.event.attended.v1",
	"ke.audio.story.completed.v1",
	"ke.community.story.approved.v1",
	"ke.explore.destination.progressed.v1",
	"ke.season.completed.v1",
}

// Verification values that may award XP.
var awardableVerification = map[string]bool{
	"verified":        true,
	"attested":        true,
	"moderated":       true,
	"server_recorded": true,
}

// Verification values that only feed private progress.
var progressOnlyVerification = map[string]bool{
	"self_declared": true,
}

// CloudEvents is the machine-readable recognition contract.
type CloudEvents struct {
	Version        string            `yaml:"version"`
	Owner          string            `yaml:"owner"`
	SpecVersion    string            `yaml:"specversion"`
	Envelope       Envelope          `yaml:"envelope"`
	Constraints    Constraints       `yaml:"constraints"`
	Payload        PayloadSchema     `yaml:"payload"`
	OccurrenceKeys map[string]string `yaml:"occurrence_keys"`
	Events         []CloudEventSpec  `yaml:"events"`
	Verification   VerificationSpec  `yaml:"verification"`
	Idempotency    Idempotency       `yaml:"idempotency"`
	Backfill       map[string]any    `yaml:"backfill"`
}

// Envelope is the CloudEvents envelope contract.
type Envelope struct {
	Required   []string       `yaml:"required"`
	Properties map[string]any `yaml:"properties"`
}

// Constraints captures the payload deny-list.
type Constraints struct {
	NeverInPayload            []string `yaml:"never_in_payload"`
	UnknownFields             string   `yaml:"unknown_fields"`
	ClientSuppliedSensitivity string   `yaml:"client_supplied_sensitivity"`
}

// PayloadSchema is the allow-listed data payload.
type PayloadSchema struct {
	Type                 string                     `yaml:"type"`
	AdditionalProperties bool                       `yaml:"additionalProperties"`
	Required             []string                   `yaml:"required"`
	Properties           map[string]PayloadProperty `yaml:"properties"`
}

// PayloadProperty is one allow-listed payload field.
type PayloadProperty struct {
	Type      string   `yaml:"type"`
	Enum      []string `yaml:"enum"`
	Format    string   `yaml:"format"`
	MaxLength int      `yaml:"maxLength"`
}

// CloudEventSpec describes one event type.
type CloudEventSpec struct {
	Type         string   `yaml:"type"`
	Category     string   `yaml:"category"`
	SubjectType  string   `yaml:"subject_type"`
	Description  string   `yaml:"description"`
	XP           any      `yaml:"xp"`
	Badge        *string  `yaml:"badge"`
	Verification []string `yaml:"verification"`
	Privacy      []string `yaml:"privacy"`
}

// VerificationSpec maps verification results to effects.
type VerificationSpec struct {
	AwardsXP     []string `yaml:"awards_xp"`
	ProgressOnly []string `yaml:"progress_only"`
	Rejected     string   `yaml:"rejected"`
}

// Idempotency captures the delivery and effect rules.
type Idempotency struct {
	Delivery                  string     `yaml:"delivery"`
	Effect                    string     `yaml:"effect"`
	TransportKey              string     `yaml:"transport_key"`
	EventUniqueness           []string   `yaml:"event_uniqueness"`
	OccurrenceUniquenessScope []string   `yaml:"occurrence_uniqueness_scope"`
	AwardUniqueness           [][]string `yaml:"award_uniqueness"`
	RuleVersionInAwardKey     bool       `yaml:"rule_version_in_award_key"`
	AwardKeyFormat            string     `yaml:"award_key_format"`
	XPUniqueness              []string   `yaml:"xp_award_uniqueness"`
	OutboxUniqueness          []string   `yaml:"outbox_uniqueness"`
	Concurrency               string     `yaml:"concurrency"`
	WritePattern              string     `yaml:"write_pattern"`
	DedupeWindow              string     `yaml:"dedupe_window"`
}

// LoadCloudEvents parses and validates the embedded CloudEvents contract.
func LoadCloudEvents() (*CloudEvents, error) {
	raw, err := fs.ReadFile(api.FS, api.CloudEventsPath)
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", api.CloudEventsPath, err)
	}
	var ce CloudEvents
	if err := yaml.Unmarshal(raw, &ce); err != nil {
		return nil, fmt.Errorf("parse %s: %w", api.CloudEventsPath, err)
	}
	if err := ce.validate(); err != nil {
		return nil, fmt.Errorf("%s: %w", api.CloudEventsPath, err)
	}
	return &ce, nil
}

func (ce *CloudEvents) validate() error {
	if ce.Version == "" || ce.SpecVersion == "" {
		return fmt.Errorf("version and specversion are required")
	}
	if len(ce.Events) != len(CloudEventTypes) {
		return fmt.Errorf("expected %d event types, found %d", len(CloudEventTypes), len(ce.Events))
	}
	seen := map[string]bool{}
	for _, ev := range ce.Events {
		if seen[ev.Type] {
			return fmt.Errorf("duplicate event type %q", ev.Type)
		}
		seen[ev.Type] = true
		if _, ok := ce.OccurrenceKeys[ev.Type]; !ok {
			return fmt.Errorf("event %q has no occurrence key template", ev.Type)
		}
	}
	for _, want := range CloudEventTypes {
		if !seen[want] {
			return fmt.Errorf("missing event type %q", want)
		}
	}
	for _, req := range ce.Payload.Required {
		if _, ok := ce.Payload.Properties[req]; !ok {
			return fmt.Errorf("required payload field %q is not allow-listed", req)
		}
	}
	if ce.Idempotency.TransportKey != "event.id" {
		return fmt.Errorf("transport key must be event.id")
	}
	if ce.Idempotency.Effect != "exactly_once" {
		return fmt.Errorf("effect must be exactly_once")
	}
	return nil
}

// Spec returns the spec for an event type.
func (ce *CloudEvents) Spec(eventType string) (CloudEventSpec, bool) {
	for _, ev := range ce.Events {
		if ev.Type == eventType {
			return ev, true
		}
	}
	return CloudEventSpec{}, false
}

// Types returns the event types sorted.
func (ce *CloudEvents) Types() []string {
	out := make([]string, 0, len(ce.Events))
	for _, ev := range ce.Events {
		out = append(out, ev.Type)
	}
	sort.Strings(out)
	return out
}

// OccurrenceKey renders the deterministic occurrence key for an event.
func (ce *CloudEvents) OccurrenceKey(eventType string, values map[string]string) (string, error) {
	tmpl, ok := ce.OccurrenceKeys[eventType]
	if !ok {
		return "", fmt.Errorf("unknown event type %q", eventType)
	}
	out := tmpl
	for key, value := range values {
		if value == "" {
			return "", fmt.Errorf("occurrence value %q is empty", key)
		}
		out = strings.ReplaceAll(out, "{"+key+"}", value)
	}
	if strings.Contains(out, "{") || strings.Contains(out, "}") {
		return "", fmt.Errorf("occurrence key %q has unfilled placeholders", out)
	}
	return out, nil
}

// AwardKey is the canonical rule + reader + event identity.
// It must not include the rule version: a new version cannot mint a second award.
func AwardKey(ruleKey, subjectID, sourceType, sourceID string) string {
	return ruleKey + "|" + subjectID + "|" + sourceType + "|" + sourceID
}

// Awardable reports whether a verification result may award XP.
func Awardable(verificationResult string) bool {
	return awardableVerification[verificationResult]
}

// ProgressOnly reports whether a verification result only feeds private progress.
func ProgressOnly(verificationResult string) bool {
	return progressOnlyVerification[verificationResult]
}

// ValidatePayload rejects unknown fields, banned fields and bad enum values.
func (ce *CloudEvents) ValidatePayload(payload map[string]any) error {
	banned := map[string]bool{}
	for _, key := range ce.Constraints.NeverInPayload {
		banned[key] = true
	}
	for key := range payload {
		if banned[key] {
			return fmt.Errorf("payload field %q is never allowed", key)
		}
		spec, ok := ce.Payload.Properties[key]
		if !ok {
			return fmt.Errorf("unknown payload field %q", key)
		}
		if len(spec.Enum) > 0 {
			value, ok := payload[key].(string)
			if !ok {
				return fmt.Errorf("payload field %q must be a string", key)
			}
			if !contains(spec.Enum, value) {
				return fmt.Errorf("payload field %q has invalid value %q", key, value)
			}
		}
	}
	for _, required := range ce.Payload.Required {
		if _, ok := payload[required]; !ok {
			return fmt.Errorf("missing payload field %q", required)
		}
	}
	return nil
}

func contains(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}
