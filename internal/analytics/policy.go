package analytics

import (
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/google/uuid"
)

// bannedKeys are rejected outright by ADR 0016 §6, in addition to any key the
// per-event schema does not list. Matching is case-insensitive.
var bannedKeys = map[string]bool{
	"email":          true,
	"name":           true,
	"phone":          true,
	"token":          true,
	"latitude":       true,
	"longitude":      true,
	"query":          true,
	"free_text":      true,
	"freetext":       true,
	"search_text":    true,
	"message":        true,
	"address":        true,
	"access_token":   true,
	"refresh_token":  true,
	"password":       true,
	"user_id":        true,
	"userid":         true,
	"device_id":      true,
	"advertising_id": true,
	"idfa":           true,
	"gaid":           true,
}

var bannedSuffixes = []string{"_email", "_phone", "_token", "_latitude", "_longitude", "_password"}

// allowedParamsKeys is the allow-listed dynamic-segment parameter set for a
// screen_view. ADR 0016 §2 permits ids and slugs only.
var allowedParamsKeys = map[string]bool{
	"id":       true,
	"slug":     true,
	"county":   true,
	"category": true,
	"locale":   true,
	"mode":     true,
}

var journeys = map[string]bool{
	"onboarding":           true,
	"first_itinerary":      true,
	"event_registration":   true,
	"challenge_submission": true,
	"conservation_checkin": true,
	"course_completion":    true,
	"heritage_visit":       true,
	"audio_playback":       true,
	"recognition_optin":    true,
}

var (
	routeUUIDRe  = regexp.MustCompile(`^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$`)
	routeHexRe   = regexp.MustCompile(`^[0-9a-fA-F]{16,}$`)
	routeDigits  = regexp.MustCompile(`^[0-9]+$`)
	maxStringLen = 128
	maxParamKeys = 12
)

// eventSchema is the subset of analytics_event_schemas.property_schema we
// enforce. additionalProperties is always false for new schemas.
type eventSchema struct {
	Properties           map[string]schemaProperty `json:"properties"`
	Required             []string                  `json:"required"`
	AdditionalProperties *bool                     `json:"additionalProperties"`
}

type schemaProperty struct {
	Type string `json:"type"`
}

// schemaForEvent selects the schema row for an event. screen_view and
// journey_step use the reserved family rows; actions use their verb.
func schemaForEvent(name, family string) string {
	switch family {
	case FamilyScreenView, FamilyJourneyStep:
		return family
	default:
		return name
	}
}

// validateRoute enforces template-only routes: no query string, no fragment,
// no scheme and no resolved identifier segments.
func validateRoute(route string) error {
	if route == "" {
		return fmt.Errorf("route is empty")
	}
	if len(route) > 200 {
		return fmt.Errorf("route is too long")
	}
	if !strings.HasPrefix(route, "/") {
		return fmt.Errorf("route must start with /")
	}
	if strings.ContainsAny(route, "?#") {
		return fmt.Errorf("route must not contain a query string or fragment")
	}
	if strings.Contains(route, "://") {
		return fmt.Errorf("route must be a template path")
	}
	for _, seg := range strings.Split(route, "/") {
		if seg == "" || strings.HasPrefix(seg, "[") {
			continue
		}
		if routeUUIDRe.MatchString(seg) || routeDigits.MatchString(seg) || routeHexRe.MatchString(seg) {
			return fmt.Errorf("route must use template segments, not resolved identifiers")
		}
	}
	return nil
}

// validateEvent checks one envelope event and returns the parsed timestamp.
// Errors are per-event: the batch is partially accepted and the id is listed
// in rejectedEventIds.
func validateEvent(ev *Event, batch *Batch, now time.Time) (time.Time, string) {
	if _, err := uuid.Parse(ev.EventID); err != nil {
		return time.Time{}, "invalid_event_id"
	}
	if _, err := uuid.Parse(ev.SessionID); err != nil {
		return time.Time{}, "invalid_session_id"
	}
	if ev.SessionSeq < 1 {
		return time.Time{}, "invalid_session_seq"
	}
	if ev.SessionID != batch.SessionID {
		return time.Time{}, "session_mismatch"
	}
	switch ev.Family {
	case FamilyScreenView, FamilyAction, FamilyJourneyStep:
	default:
		return time.Time{}, "unknown_event_family"
	}
	if ev.Name == "" || len(ev.Name) > maxStringLen {
		return time.Time{}, "invalid_event_name"
	}
	ts, err := time.Parse(time.RFC3339, ev.ClientTS)
	if err != nil {
		ts, err = time.Parse(time.RFC3339Nano, ev.ClientTS)
		if err != nil {
			return time.Time{}, "invalid_client_ts"
		}
	}
	if ts.After(now.Add(5 * time.Minute)) {
		return time.Time{}, "client_ts_in_future"
	}
	// Offline queue rows expire after seven days (ADR 0016 §7).
	if ts.Before(now.AddDate(0, 0, -7)) {
		return time.Time{}, "expired"
	}

	if ev.Family == FamilyScreenView {
		if err := validateRoute(ev.Name); err != nil {
			return time.Time{}, "invalid_route"
		}
		if ev.Route == nil {
			route := ev.Name
			ev.Route = &route
		}
	}
	if ev.Route != nil {
		if err := validateRoute(*ev.Route); err != nil {
			return time.Time{}, "invalid_route"
		}
	}
	if ev.Family == FamilyJourneyStep {
		journey, _ := ev.Properties["journey"].(string)
		step, _ := ev.Properties["step"].(string)
		outcome, _ := ev.Properties["outcome"].(string)
		if !journeys[journey] {
			return time.Time{}, "invalid_journey"
		}
		if step == "" || len(step) > maxStringLen {
			return time.Time{}, "invalid_journey_step"
		}
		if _, ok := numericProperty(ev.Properties["step_index"]); !ok {
			return time.Time{}, "invalid_journey_step_index"
		}
		if outcome == "" || len(outcome) > 32 {
			return time.Time{}, "invalid_journey_outcome"
		}
	}
	return ts, ""
}

// filterProperties applies the per-event allow-list. Unknown keys are dropped
// and counted; banned keys and over-long free-text values are always dropped.
func filterProperties(props map[string]any, schema eventSchema, rowBanned []string) (map[string]any, []string) {
	if len(props) == 0 {
		return nil, nil
	}
	allowed := schema.Properties
	dropped := make([]string, 0)
	clean := make(map[string]any, len(props))
	for key, value := range props {
		if isBanned(key, rowBanned) {
			dropped = append(dropped, key)
			continue
		}
		if _, ok := allowed[key]; !ok {
			dropped = append(dropped, key)
			continue
		}
		if key == "params" {
			params, ok := filterParams(value)
			if !ok {
				dropped = append(dropped, key)
				continue
			}
			clean[key] = params
			continue
		}
		normalized, ok := normaliseProperty(value)
		if !ok {
			dropped = append(dropped, key)
			continue
		}
		clean[key] = normalized
	}
	if len(clean) == 0 {
		clean = nil
	}
	return clean, dropped
}

func isBanned(key string, rowBanned []string) bool {
	lower := strings.ToLower(key)
	if bannedKeys[lower] {
		return true
	}
	for _, suffix := range bannedSuffixes {
		if strings.HasSuffix(lower, suffix) {
			return true
		}
	}
	for _, b := range rowBanned {
		if strings.EqualFold(b, key) {
			return true
		}
	}
	return false
}

func filterParams(value any) (map[string]any, bool) {
	raw, ok := value.(map[string]any)
	if !ok || len(raw) > maxParamKeys {
		return nil, false
	}
	out := make(map[string]any, len(raw))
	for key, v := range raw {
		if !allowedParamsKeys[key] {
			continue
		}
		normalized, ok := normaliseProperty(v)
		if !ok {
			continue
		}
		out[key] = normalized
	}
	return out, true
}

// normaliseProperty accepts short scalars and small arrays of scalars. It
// refuses anything long enough to be free text.
func normaliseProperty(value any) (any, bool) {
	switch v := value.(type) {
	case nil:
		return nil, true
	case bool:
		return v, true
	case float64:
		return v, true
	case json.Number:
		return v, true
	case string:
		if len(v) > maxStringLen || strings.ContainsAny(v, "\n\r") {
			return nil, false
		}
		return v, true
	case []any:
		if len(v) > 20 {
			return nil, false
		}
		out := make([]any, 0, len(v))
		for _, item := range v {
			switch item.(type) {
			case string, float64, bool, json.Number:
				out = append(out, item)
			default:
				return nil, false
			}
		}
		return out, true
	default:
		return nil, false
	}
}

func numericProperty(value any) (float64, bool) {
	switch v := value.(type) {
	case float64:
		return v, true
	case int:
		return float64(v), true
	case json.Number:
		f, err := v.Float64()
		return f, err == nil
	default:
		return 0, false
	}
}
