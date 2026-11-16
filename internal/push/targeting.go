package push

import (
	"context"
	"encoding/json"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// TargetAudience is the JSON contract stored in push_notifications.target_audience.
// An empty or partial object means "Program audience globally" for campaigns.
type TargetAudience struct {
	CountyGIDs []int    `json:"countyGids,omitempty"`
	Categories []string `json:"categories,omitempty"`
	Interests  []string `json:"interests,omitempty"`
	Language   string   `json:"language,omitempty"`
	Guests     bool     `json:"guests,omitempty"`
	Important  bool     `json:"important,omitempty"`
}

func ParseTargetAudience(raw string) TargetAudience {
	if strings.TrimSpace(raw) == "" {
		return TargetAudience{}
	}
	var audience TargetAudience
	if err := json.Unmarshal([]byte(raw), &audience); err != nil {
		return TargetAudience{}
	}
	return audience
}

type ProgramTarget struct {
	UserID               string
	PushTokenID          string
	Token                string
	Timezone             string
	QuietHoursEnabled    bool
	QuietHoursStart      string
	QuietHoursEnd        string
	ProgramImportantOnly bool
}

// ResolveProgramTargets returns active tokens for Program Updates. It requires
// legal consent, OS permission, an anchor, and the Program category for
// signed-in users. Guest tokens only receive global campaigns because there is
// no guest county preference in v1.
func ResolveProgramTargets(ctx context.Context, pool *pgxpool.Pool, audience TargetAudience) ([]ProgramTarget, error) {
	rows, err := pool.Query(ctx, `
		SELECT COALESCE(pt.user_id::text, ''), pt.id::text, pt.token,
		       COALESCE(p.timezone, 'Africa/Nairobi'),
		       COALESCE(p.quiet_hours_enabled, false),
		       COALESCE(to_char(p.quiet_hours_start, 'HH24:MI'), '22:00'),
		       COALESCE(to_char(p.quiet_hours_end, 'HH24:MI'), '06:00'),
		       COALESCE(p.program_important_only, true)
		FROM push_tokens pt
		LEFT JOIN user_profiles up ON up.user_id = pt.user_id::text
		LEFT JOIN alert_preferences p ON p.user_id = pt.user_id::text
		WHERE pt.is_active = true
		  AND pt.deactivated_at IS NULL
		  AND (
		    (pt.user_id IS NULL AND $5::boolean
		      AND COALESCE(cardinality($1::int[]), 0) = 0
		      AND $2::text = ''
		      AND COALESCE(cardinality($4::text[]), 0) = 0)
		    OR
		    (pt.user_id IS NOT NULL
		      AND up.consent_notifications = true
		      AND p.enabled = true
		      AND p.os_permission_ok = true
		      AND p.categories @> ARRAY['Program']
		      AND (p.home_county_gid IS NOT NULL
		           OR COALESCE(cardinality(p.watch_county_gids), 0) > 0
		           OR EXISTS (SELECT 1 FROM user_alert_location l
		                      WHERE l.user_id = p.user_id
		                        AND l.updated_at > now() - interval '24 hours'
		                        AND up.consent_location = true))
		      AND (COALESCE(cardinality($1::int[]), 0) = 0
		           OR p.home_county_gid = ANY($1)
		           OR p.watch_county_gids && $1)
		      AND ($2::text = '' OR p.language = $2)
		      AND ($3::boolean OR p.program_important_only = false)
		      AND (COALESCE(cardinality($4::text[]), 0) = 0
		           OR (up.preferences IS NOT NULL
		               AND up.preferences ~ '^\s*\['
		               AND (up.preferences::jsonb) ?| $4::text[])))
		  )
		ORDER BY pt.id`,
		audience.CountyGIDs, audience.Language, audience.Important, audience.Interests, audience.Guests)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	targets := make([]ProgramTarget, 0)
	for rows.Next() {
		var target ProgramTarget
		if err := rows.Scan(
			&target.UserID, &target.PushTokenID, &target.Token,
			&target.Timezone, &target.QuietHoursEnabled,
			&target.QuietHoursStart, &target.QuietHoursEnd,
			&target.ProgramImportantOnly,
		); err != nil {
			return nil, err
		}
		targets = append(targets, target)
	}
	return targets, rows.Err()
}

func quietHoursAllow(target ProgramTarget, now time.Time) bool {
	if !target.QuietHoursEnabled {
		return true
	}
	start, okStart := parseClock(target.QuietHoursStart)
	end, okEnd := parseClock(target.QuietHoursEnd)
	if !okStart || !okEnd || start == end {
		return true
	}
	loc := loadLocation(target.Timezone)
	local := now.In(loc)
	minutes := time.Duration(local.Hour())*time.Hour + time.Duration(local.Minute())*time.Minute
	if start < end {
		return !(minutes >= start && minutes < end)
	}
	return !(minutes >= start || minutes < end)
}

func parseClock(value string) (time.Duration, bool) {
	parsed, err := time.Parse("15:04", strings.TrimSpace(value))
	if err != nil {
		return 0, false
	}
	return time.Duration(parsed.Hour())*time.Hour + time.Duration(parsed.Minute())*time.Minute, true
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
