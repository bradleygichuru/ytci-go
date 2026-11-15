package alerts

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

func eat() *time.Location { return time.FixedZone("EAT", 3*3600) }

func defaultPolicy() DeliveryPolicy {
	return DeliveryPolicy{
		Enabled:           true,
		MinSeverity:       "Severe",
		Categories:        []string{"Met", "Safety", "Security"},
		QuietHoursEnabled: true,
		QuietStart:        "22:00",
		QuietEnd:          "06:00",
		Timezone:          "Africa/Nairobi",
		CriticalOverride:  true,
	}
}

func TestShouldPushDuringQuietHours(t *testing.T) {
	now := time.Date(2026, 11, 15, 23, 0, 0, 0, eat())
	alert := AlertPolicyInput{Severity: "Moderate", Category: "Met", Status: "active"}
	prefs := defaultPolicy()
	prefs.MinSeverity = "Moderate"

	allowed, reason, after := ShouldPush(alert, prefs, now)
	assert.False(t, allowed)
	assert.Equal(t, "quiet_hours", reason)
	assert.Equal(t, time.Date(2026, 11, 16, 6, 0, 0, 0, eat()).UTC(), after.UTC())
}

func TestShouldPushSevereOverridesQuietHours(t *testing.T) {
	now := time.Date(2026, 11, 15, 23, 0, 0, 0, eat())
	alert := AlertPolicyInput{Severity: "Severe", Category: "Met", Status: "active"}
	prefs := defaultPolicy()
	prefs.CriticalOverride = true

	allowed, reason, _ := ShouldPush(alert, prefs, now)
	assert.True(t, allowed)
	assert.Equal(t, "ok", reason)
}

func TestShouldPushSevereDoesNotOverrideWhenCriticalOverrideOff(t *testing.T) {
	now := time.Date(2026, 11, 15, 23, 0, 0, 0, eat())
	prefs := defaultPolicy()
	prefs.CriticalOverride = false
	alert := AlertPolicyInput{Severity: "Severe", Category: "Met", Status: "active"}

	allowed, reason, after := ShouldPush(alert, prefs, now)
	assert.False(t, allowed)
	assert.Equal(t, "quiet_hours", reason)
	assert.Equal(t, time.Date(2026, 11, 16, 6, 0, 0, 0, eat()).UTC(), after.UTC())
}

func TestShouldPushExtremeAlwaysOverrides(t *testing.T) {
	now := time.Date(2026, 11, 15, 23, 0, 0, 0, eat())
	prefs := defaultPolicy()
	prefs.CriticalOverride = false
	alert := AlertPolicyInput{Severity: "Extreme", Category: "Met", Status: "active"}

	allowed, reason, _ := ShouldPush(alert, prefs, now)
	assert.True(t, allowed)
	assert.Equal(t, "ok", reason)
}

func TestShouldPushOutsideQuietHours(t *testing.T) {
	now := time.Date(2026, 11, 15, 7, 0, 0, 0, eat())
	alert := AlertPolicyInput{Severity: "Moderate", Category: "Met", Status: "active"}
	prefs := defaultPolicy()
	prefs.MinSeverity = "Moderate"
	allowed, reason, _ := ShouldPush(alert, prefs, now)
	assert.True(t, allowed)
	assert.Equal(t, "ok", reason)
}

func TestShouldPushCategoryAndSeverity(t *testing.T) {
	now := time.Date(2026, 11, 15, 7, 0, 0, 0, eat())
	prefs := defaultPolicy()
	alert := AlertPolicyInput{Severity: "Minor", Category: "Met", Status: "active"}
	allowed, reason, _ := ShouldPush(alert, prefs, now)
	assert.False(t, allowed)
	assert.Equal(t, "severity", reason)

	alert.Severity = "Severe"
	alert.Category = "Program"
	allowed, reason, _ = ShouldPush(alert, prefs, now)
	assert.False(t, allowed)
	assert.Equal(t, "category", reason)
}

func TestShouldPushExpired(t *testing.T) {
	now := time.Date(2026, 11, 15, 7, 0, 0, 0, eat())
	expires := now.Add(-time.Minute)
	alert := AlertPolicyInput{Severity: "Severe", Category: "Met", Status: "active", ExpiresAt: &expires}
	allowed, reason, _ := ShouldPush(alert, defaultPolicy(), now)
	assert.False(t, allowed)
	assert.Equal(t, "expired", reason)
}

func TestShouldPushDisabledOrCancelled(t *testing.T) {
	now := time.Date(2026, 11, 15, 7, 0, 0, 0, eat())
	prefs := defaultPolicy()
	prefs.Enabled = false
	alert := AlertPolicyInput{Severity: "Severe", Category: "Met", Status: "active"}
	allowed, reason, _ := ShouldPush(alert, prefs, now)
	assert.False(t, allowed)
	assert.Equal(t, "disabled", reason)

	prefs.Enabled = true
	alert.Status = "cancelled"
	allowed, reason, _ = ShouldPush(alert, prefs, now)
	assert.False(t, allowed)
	assert.Equal(t, "inactive", reason)
}

func TestSeverityRank(t *testing.T) {
	assert.Greater(t, SeverityRank("Extreme"), SeverityRank("Severe"))
	assert.Greater(t, SeverityRank("Severe"), SeverityRank("Moderate"))
	assert.Equal(t, 0, SeverityRank("Unknown"))
}
