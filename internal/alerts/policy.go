package alerts

import (
	"strings"
	"time"
)

type AlertPolicyInput struct {
	Severity   string
	Category   string
	Categories []string
	Status     string
	ExpiresAt  *time.Time
}

type DeliveryPolicy struct {
	Enabled           bool
	MinSeverity       string
	Categories        []string
	QuietHoursEnabled bool
	QuietStart        string
	QuietEnd          string
	Timezone          string
	CriticalOverride  bool
}

// SeverityRank maps CAP severity to a monotonic rank.
func SeverityRank(severity string) int {
	switch strings.Title(strings.ToLower(strings.TrimSpace(severity))) {
	case "Extreme":
		return 4
	case "Severe":
		return 3
	case "Moderate":
		return 2
	case "Minor":
		return 1
	default:
		return 0
	}
}

// ShouldPush decides whether a push should be sent now. The returned time is
// the earliest permissible send time when the push is deferred by quiet hours;
// it is zero when allowed or permanently blocked.
func ShouldPush(alert AlertPolicyInput, prefs DeliveryPolicy, now time.Time) (bool, string, time.Time) {
	if !prefs.Enabled {
		return false, "disabled", time.Time{}
	}
	if alert.Status != "" && alert.Status != "active" {
		return false, "inactive", time.Time{}
	}
	if alert.ExpiresAt != nil && !alert.ExpiresAt.After(now) {
		return false, "expired", time.Time{}
	}
	if len(prefs.Categories) > 0 {
		matched := containsString(prefs.Categories, alert.Category)
		if !matched {
			for _, category := range alert.Categories {
				if containsString(prefs.Categories, category) {
					matched = true
					break
				}
			}
		}
		if !matched {
			return false, "category", time.Time{}
		}
	}
	if SeverityRank(alert.Severity) < SeverityRank(prefs.MinSeverity) {
		return false, "severity", time.Time{}
	}

	if !prefs.QuietHoursEnabled {
		return true, "ok", time.Time{}
	}
	start, end, ok := quietWindow(prefs.QuietStart, prefs.QuietEnd)
	if !ok || start == end {
		return true, "ok", time.Time{}
	}

	loc := loadLocation(prefs.Timezone)
	local := now.In(loc)
	inQuiet := isWithinQuiet(local, start, end)
	if !inQuiet {
		return true, "ok", time.Time{}
	}

	switch SeverityRank(alert.Severity) {
	case 4:
		return true, "ok", time.Time{}
	case 3:
		if prefs.CriticalOverride {
			return true, "ok", time.Time{}
		}
	}
	return false, "quiet_hours", nextQuietEnd(local, start, end)
}

// quietHoursBlocked is the send-time guard. It is deliberately checked again
// in the worker so a delivery queued just before quiet hours cannot leak out
// if the worker is delayed.
func quietHoursBlocked(severity string, enabled bool, start, end, timezone string, criticalOverride bool, now time.Time) bool {
	if !enabled {
		return false
	}
	startAt, endAt, ok := quietWindow(start, end)
	if !ok || startAt == endAt {
		return false
	}
	local := now.In(loadLocation(timezone))
	if !isWithinQuiet(local, startAt, endAt) {
		return false
	}
	switch SeverityRank(severity) {
	case 4:
		return false
	case 3:
		return !criticalOverride
	}
	return true
}

func containsString(list []string, value string) bool {
	for _, v := range list {
		if strings.EqualFold(v, value) {
			return true
		}
	}
	return false
}

func loadLocation(name string) *time.Location {
	loc, err := time.LoadLocation(strings.TrimSpace(name))
	if err != nil || loc == nil {
		loc, _ = time.LoadLocation("Africa/Nairobi")
	}
	if loc == nil {
		return time.UTC
	}
	return loc
}

func quietWindow(start, end string) (time.Duration, time.Duration, bool) {
	s, err := parseClock(start)
	if err != nil {
		return 0, 0, false
	}
	e, err := parseClock(end)
	if err != nil {
		return 0, 0, false
	}
	return s, e, true
}

func parseClock(value string) (time.Duration, error) {
	t, err := time.Parse("15:04", strings.TrimSpace(value))
	if err != nil {
		return 0, err
	}
	return time.Duration(t.Hour())*time.Hour + time.Duration(t.Minute())*time.Minute, nil
}

func isWithinQuiet(local time.Time, start, end time.Duration) bool {
	minutes := time.Duration(local.Hour())*time.Hour + time.Duration(local.Minute())*time.Minute
	if start < end {
		return minutes >= start && minutes < end
	}
	return minutes >= start || minutes < end
}

func nextQuietEnd(local time.Time, start, end time.Duration) time.Time {
	midnight := time.Date(local.Year(), local.Month(), local.Day(), 0, 0, 0, 0, local.Location())
	next := midnight.Add(end)
	if !next.After(local) {
		next = next.Add(24 * time.Hour)
	}
	return next.UTC()
}
