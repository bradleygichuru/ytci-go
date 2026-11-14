package composer

import (
	"fmt"
	"strings"
	"time"

	"github.com/bradleygichuru/ytci-go/internal/catalog"
)

var (
	validGroupTypes = map[string]bool{"solo": true, "couple": true, "friends": true, "family": true}
	validTransports = map[string]bool{"car": true, "public": true, "tour_van": true}
	validPaces      = map[string]bool{"relaxed": true, "balanced": true, "packed": true}
	validBands      = map[string]bool{"budget": true, "mid-range": true, "luxury": true}
)

// NormalizeBrief trims/defaults/normalizes the input and returns field errors.
func NormalizeBrief(brief *Brief, now time.Time) *ValidationError {
	var errs []FieldError
	add := func(field, code, message string) {
		errs = append(errs, FieldError{Field: field, Code: code, Message: message})
	}

	brief.Origin.Name = strings.TrimSpace(brief.Origin.Name)
	if brief.Origin.Name == "" && (brief.Origin.Latitude == nil || brief.Origin.Longitude == nil) {
		add("origin", "REQUIRED", "origin name or coordinates are required")
	}
	if (brief.Origin.Latitude == nil) != (brief.Origin.Longitude == nil) {
		add("origin", "INCOMPLETE", "origin latitude and longitude must be provided together")
	}

	if brief.Days < 1 || brief.Days > 14 {
		add("days", "RANGE", "days must be between 1 and 14")
	}

	if len(brief.Interests) == 0 {
		add("interests", "REQUIRED", "at least one interest is required")
	} else {
		normalized := make([]string, 0, len(brief.Interests))
		seen := map[string]bool{}
		for _, raw := range brief.Interests {
			interest, ok := normalizeInterest(raw)
			if !ok {
				add("interests", "UNKNOWN", fmt.Sprintf("unknown interest %q", raw))
				continue
			}
			if !seen[interest] {
				seen[interest] = true
				normalized = append(normalized, interest)
			}
		}
		if len(normalized) > 8 {
			add("interests", "TOO_MANY", "at most 8 interests are allowed")
		}
		brief.Interests = normalized
	}

	brief.BudgetBand = strings.ToLower(strings.TrimSpace(brief.BudgetBand))
	switch brief.BudgetBand {
	case "budget", "budget-smart":
		brief.BudgetBand = "budget"
	case "mid", "midrange", "mid-range", "balanced":
		brief.BudgetBand = "mid-range"
	case "luxury", "experience-first":
		brief.BudgetBand = "luxury"
	default:
		add("budgetBand", "INVALID", "budgetBand must be budget, mid-range, or luxury")
	}
	if brief.BudgetPerPersonKes != nil && *brief.BudgetPerPersonKes <= 0 {
		add("budgetPerPersonKes", "INVALID", "budgetPerPersonKes must be positive")
	}

	brief.GroupType = strings.ToLower(strings.TrimSpace(brief.GroupType))
	if !validGroupTypes[brief.GroupType] {
		add("groupType", "INVALID", "groupType must be solo, couple, friends, or family")
	}
	if brief.GroupSize == 0 {
		switch brief.GroupType {
		case "solo":
			brief.GroupSize = 1
		case "couple":
			brief.GroupSize = 2
		}
	}
	if brief.GroupSize < 1 || brief.GroupSize > 20 {
		add("groupSize", "RANGE", "groupSize must be between 1 and 20")
	}
	if brief.GroupType == "solo" && brief.GroupSize != 1 {
		add("groupSize", "SOLO", "solo trips must have groupSize 1")
	}
	if brief.GroupType == "couple" && brief.GroupSize != 2 {
		add("groupSize", "COUPLE", "couple trips must have groupSize 2")
	}

	brief.Transport = strings.ToLower(strings.TrimSpace(brief.Transport))
	switch brief.Transport {
	case "", "car":
		brief.Transport = "car"
	case "public", "public_transport", "public transport":
		brief.Transport = "public"
	case "tour_van", "tour van", "tourvan", "van":
		brief.Transport = "tour_van"
	default:
		add("transport", "INVALID", "transport must be car, public, or tour_van")
	}
	if brief.Transport == "" {
		brief.Transport = "car"
	}

	brief.Pace = strings.ToLower(strings.TrimSpace(brief.Pace))
	if brief.Pace == "" {
		brief.Pace = "balanced"
	}
	if !validPaces[brief.Pace] {
		add("pace", "INVALID", "pace must be relaxed, balanced, or packed")
	}

	if brief.StartDate != nil {
		raw := strings.TrimSpace(*brief.StartDate)
		if raw == "" {
			brief.StartDate = nil
		} else if d, err := time.Parse("2006-01-02", raw); err != nil {
			add("startDate", "FORMAT", "startDate must be YYYY-MM-DD")
		} else if d.Before(startOfDay(now.UTC())) {
			add("startDate", "PAST", "startDate cannot be in the past")
		} else {
			normalized := d.Format("2006-01-02")
			brief.StartDate = &normalized
		}
	}

	if len(brief.MustInclude) > 3 {
		add("mustInclude", "TOO_MANY", "at most 3 must-includes are allowed")
	}
	seenMust := map[string]bool{}
	must := make([]string, 0, len(brief.MustInclude))
	for _, raw := range brief.MustInclude {
		raw = strings.TrimSpace(raw)
		if raw == "" || seenMust[raw] {
			continue
		}
		seenMust[raw] = true
		must = append(must, raw)
	}
	brief.MustInclude = must

	if brief.ReturnToOrigin == nil {
		yes := true
		brief.ReturnToOrigin = &yes
	}
	if brief.RegenerateCount < 0 {
		add("regenerateCount", "INVALID", "regenerateCount cannot be negative")
	}

	if len(errs) > 0 {
		return &ValidationError{Fields: errs}
	}
	return nil
}

func normalizeInterest(raw string) (string, bool) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "", false
	}
	switch strings.ToLower(raw) {
	case "beaches", "beach":
		return string(catalog.CategoryBeach), true
	case "adventure":
		return string(catalog.CategoryActivity), true
	case "sightseeing":
		return string(catalog.CategorySightseeing), true
	case "rest":
		return string(catalog.CategoryRest), true
	case "shopping":
		return string(catalog.CategoryShopping), true
	case "transport":
		return string(catalog.CategoryTransport), true
	}
	if catalog.IsCategory(raw) {
		return raw, true
	}
	if category, ok := catalog.LegacyCategory(raw); ok {
		return string(category), true
	}
	return "", false
}

func startOfDay(t time.Time) time.Time {
	return time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, time.UTC)
}
