package recognition

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/bradleygichuru/ytci-go/internal/contracts"
)

type levelRow struct {
	Level     int
	Name      string
	Threshold int
}

func loadLevels(ctx context.Context, q interface {
	Query(context.Context, string, ...any) (pgx.Rows, error)
}) ([]levelRow, error) {
	rows, err := q.Query(ctx, `SELECT level, name_en, threshold FROM recognition.levels ORDER BY threshold`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]levelRow, 0, 5)
	for rows.Next() {
		var l levelRow
		if err := rows.Scan(&l.Level, &l.Name, &l.Threshold); err != nil {
			return nil, err
		}
		out = append(out, l)
	}
	return out, rows.Err()
}

func levelFor(total int64, levels []levelRow) Level {
	var current levelRow
	var next *levelRow
	for i := range levels {
		if int64(levels[i].Threshold) <= total {
			current = levels[i]
			if i+1 < len(levels) {
				next = &levels[i+1]
			}
		}
	}
	out := Level{
		Value:       current.Level,
		Name:        current.Name,
		Threshold:   current.Threshold,
		XPIntoLevel: int(total) - current.Threshold,
	}
	if next != nil {
		n := next.Name
		toNext := next.Threshold - int(total)
		out.NextName = &n
		out.XPToNext = &toNext
	}
	return out
}

func (e *Engine) levelForSubject(ctx context.Context, subject string) (Level, int64, error) {
	var total int64
	err := e.pool.QueryRow(ctx,
		`SELECT total_xp FROM recognition.xp_totals WHERE subject_id=$1`, subject).Scan(&total)
	if errors.Is(err, pgx.ErrNoRows) {
		total = 0
	} else if err != nil {
		return Level{}, 0, err
	}
	levels, err := loadLevels(ctx, e.pool)
	if err != nil {
		return Level{}, 0, err
	}
	return levelFor(total, levels), total, nil
}

// Overview builds the private recognition home payload.
func (e *Engine) Overview(ctx context.Context, subject string) (RecognitionOverview, error) {
	level, total, err := e.levelForSubject(ctx, subject)
	if err != nil {
		return RecognitionOverview{}, err
	}
	out := RecognitionOverview{Level: level, TotalXP: total, RecentAwards: []BadgeAward{}, AdjustmentNotices: []XpEntry{}}

	out.BadgeCounts, err = e.badgeCounts(ctx, subject)
	if err != nil {
		return out, err
	}
	out.RecentAwards, err = e.recentAwards(ctx, subject, 5)
	if err != nil {
		return out, err
	}

	var seasonKey, seasonName string
	var endsAt time.Time
	err = e.pool.QueryRow(ctx, `
		SELECT season_key, name_en, ends_at FROM recognition.seasons
		WHERE status='active' AND now() BETWEEN starts_at AND ends_at
		ORDER BY starts_at DESC LIMIT 1`).Scan(&seasonKey, &seasonName, &endsAt)
	if err == nil {
		percent, err := e.seasonProgressPercent(ctx, subject, seasonKey)
		if err != nil {
			return out, err
		}
		out.Season = &OverviewSeason{Key: seasonKey, NameEn: seasonName, EndsAt: endsAt, ProgressPercent: percent}
	} else if !errors.Is(err, pgx.ErrNoRows) {
		return out, err
	}

	out.Passport, err = e.passportCounts(ctx, subject)
	if err != nil {
		return out, err
	}

	// Opt-in state for the current season, if any.
	if seasonKey != "" {
		opted, eligible, reason, err := e.leaderboardStateFlags(ctx, subject, seasonKey)
		if err != nil {
			return out, err
		}
		out.Leaderboard = OverviewLeaderboard{OptedIn: opted, Eligible: eligible, Reason: reason}
	} else {
		out.Leaderboard = OverviewLeaderboard{Eligible: false, Reason: strPtr("no_active_season")}
	}

	out.AdjustmentNotices, err = e.adjustmentNotices(ctx, subject)
	return out, err
}

func (e *Engine) badgeCounts(ctx context.Context, subject string) (BadgeCounts, error) {
	var counts BadgeCounts
	err := e.pool.QueryRow(ctx, `
		SELECT count(*) FROM recognition.badge_definitions WHERE status <> 'retired'`,
	).Scan(&counts.Total)
	if err != nil {
		return counts, err
	}
	err = e.pool.QueryRow(ctx, `
		SELECT count(DISTINCT ba.badge_key) FROM recognition.badge_awards ba
		WHERE ba.subject_id=$1 AND ba.revoked_at IS NULL`, subject,
	).Scan(&counts.Earned)
	if err != nil {
		return counts, err
	}
	err = e.pool.QueryRow(ctx, `
		SELECT count(DISTINCT ba.badge_key) FROM recognition.badge_awards ba
		JOIN recognition.badge_definitions bd ON bd.badge_key=ba.badge_key
		WHERE ba.subject_id=$1 AND ba.revoked_at IS NULL
		  AND bd.public_eligible AND bd.privacy_class='public'`, subject,
	).Scan(&counts.Public)
	return counts, err
}

func (e *Engine) recentAwards(ctx context.Context, subject string, limit int) ([]BadgeAward, error) {
	rows, err := e.pool.Query(ctx, `
		SELECT bd.badge_key, bd.name_en, bd.name_sw, bd.family, bd.criteria,
		       ba.awarded_at, ba.hidden, ba.privacy_class, ba.source_type, ba.source_id
		FROM recognition.badge_awards ba
		JOIN recognition.badge_definitions bd ON bd.badge_key=ba.badge_key
		WHERE ba.subject_id=$1 AND ba.revoked_at IS NULL
		ORDER BY ba.awarded_at DESC
		LIMIT $2`, subject, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]BadgeAward, 0, limit)
	for rows.Next() {
		var b BadgeAward
		var criteria []byte
		var awarded time.Time
		var sourceType, sourceID string
		var nameSw pgtype.Text
		if err := rows.Scan(&b.BadgeKey, &b.NameEn, &nameSw, &b.Family, &criteria,
			&awarded, &b.Hidden, &b.PrivacyClass, &sourceType, &sourceID); err != nil {
			return nil, err
		}
		if nameSw.Valid {
			b.NameSw = &nameSw.String
		}
		_ = json.Unmarshal(criteria, &b.Criteria)
		b.Earned = true
		t := awarded
		b.EarnedAt = &t
		b.SourceType = &sourceType
		b.SourceID = &sourceID
		out = append(out, b)
	}
	return out, rows.Err()
}

func (e *Engine) adjustmentNotices(ctx context.Context, subject string) ([]XpEntry, error) {
	rows, err := e.pool.Query(ctx, `
		SELECT id::text, amount, reason, source_type, source_id, note, created_at
		FROM recognition.xp_entries
		WHERE subject_id=$1 AND reason <> 'award'
		ORDER BY created_at DESC LIMIT 5`, subject)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]XpEntry, 0)
	for rows.Next() {
		var x XpEntry
		if err := rows.Scan(&x.ID, &x.Amount, &x.Reason, &x.SourceType, &x.SourceID, &x.Note, &x.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, x)
	}
	return out, rows.Err()
}

func (e *Engine) seasonProgressPercent(ctx context.Context, subject, seasonKey string) (int, error) {
	var total, done int
	err := e.pool.QueryRow(ctx, `
		SELECT count(*),
		       count(*) FILTER (WHERE ps.completed_at IS NOT NULL)
		FROM recognition.progress_tracks pt
		LEFT JOIN recognition.progress_snapshots ps
		  ON ps.track_key=pt.track_key AND ps.subject_id=$1
		WHERE pt.status='published'`, subject).Scan(&total, &done)
	if err != nil || total == 0 {
		return 0, err
	}
	return done * 100 / total, nil
}

func (e *Engine) passportCounts(ctx context.Context, subject string) (OverviewPassport, error) {
	var p OverviewPassport
	err := e.pool.QueryRow(ctx, `
		SELECT count(DISTINCT data->>'source_id') FROM recognition.events
		WHERE subject=$1 AND type='ke.heritage.county.collected.v1'`, subject).Scan(&p.Counties)
	if err != nil {
		return p, err
	}
	err = e.pool.QueryRow(ctx, `
		SELECT count(DISTINCT data->>'source_id') FROM recognition.events
		WHERE subject=$1 AND type='ke.heritage.visit.verified.v1'`, subject).Scan(&p.Sites)
	return p, err
}

// ListBadges renders the catalogue with earned/locked state. Cursor is the
// last badge key (stable and opaque enough for a paginated catalogue).
func (e *Engine) ListBadges(ctx context.Context, subject, family, state, cursor string, limit int) ([]BadgeAward, bool, string, BadgeCounts, error) {
	if limit <= 0 || limit > 100 {
		limit = 50
	}
	args := []any{subject, family, state, cursor, limit + 1}
	rows, err := e.pool.Query(ctx, `
		SELECT bd.badge_key, bd.name_en, bd.name_sw, bd.family, bd.criteria,
		       bd.privacy_class, ba.awarded_at, COALESCE(ba.hidden, false),
		       ba.source_type, ba.source_id
		FROM recognition.badge_definitions bd
		LEFT JOIN recognition.badge_awards ba
		  ON ba.badge_key=bd.badge_key AND ba.subject_id=$1 AND ba.revoked_at IS NULL
		WHERE bd.status='published'
		  AND ($2='' OR bd.family=$2)
		  AND (bd.hidden=false OR ba.id IS NOT NULL)
		  AND ($3='all' OR ($3='earned' AND ba.id IS NOT NULL) OR ($3='locked' AND ba.id IS NULL))
		  AND ($4='' OR bd.badge_key > $4)
		ORDER BY bd.badge_key
		LIMIT $5`, args...)
	if err != nil {
		return nil, false, "", BadgeCounts{}, err
	}
	defer rows.Close()
	items := make([]BadgeAward, 0, limit)
	next := ""
	for rows.Next() {
		var b BadgeAward
		var criteria []byte
		var awarded pgtype.Timestamptz
		var sourceType, sourceID, nameSw pgtype.Text
		if err := rows.Scan(&b.BadgeKey, &b.NameEn, &nameSw, &b.Family, &criteria,
			&b.PrivacyClass, &awarded, &b.Hidden, &sourceType, &sourceID); err != nil {
			return nil, false, "", BadgeCounts{}, err
		}
		_ = json.Unmarshal(criteria, &b.Criteria)
		if nameSw.Valid {
			b.NameSw = &nameSw.String
		}
		if awarded.Valid {
			b.Earned = true
			t := awarded.Time
			b.EarnedAt = &t
		}
		if sourceType.Valid {
			b.SourceType = &sourceType.String
		}
		if sourceID.Valid {
			b.SourceID = &sourceID.String
		}
		items = append(items, b)
	}
	if err := rows.Err(); err != nil {
		return nil, false, "", BadgeCounts{}, err
	}
	hasMore := len(items) > limit
	if hasMore {
		items = items[:limit]
	}
	if hasMore && len(items) > 0 {
		next = items[len(items)-1].BadgeKey
	}
	counts, err := e.badgeCounts(ctx, subject)
	return items, hasMore, next, counts, err
}

// Badge returns one catalogue row for the reader.
func (e *Engine) Badge(ctx context.Context, subject, badgeKey string) (BadgeAward, error) {
	var b BadgeAward
	var criteria []byte
	var awarded pgtype.Timestamptz
	var sourceType, sourceID, nameSw pgtype.Text
	err := e.pool.QueryRow(ctx, `
		SELECT bd.badge_key, bd.name_en, bd.name_sw, bd.family, bd.criteria,
		       bd.privacy_class, ba.awarded_at, COALESCE(ba.hidden, false),
		       ba.source_type, ba.source_id
		FROM recognition.badge_definitions bd
		LEFT JOIN recognition.badge_awards ba
		  ON ba.badge_key=bd.badge_key AND ba.subject_id=$1 AND ba.revoked_at IS NULL
		WHERE bd.badge_key=$2
		  AND (bd.status='published' OR ba.id IS NOT NULL)
		  AND (bd.hidden=false OR ba.id IS NOT NULL)`, subject, badgeKey,
	).Scan(&b.BadgeKey, &b.NameEn, &nameSw, &b.Family, &criteria,
		&b.PrivacyClass, &awarded, &b.Hidden, &sourceType, &sourceID)
	if err != nil {
		return b, err
	}
	_ = json.Unmarshal(criteria, &b.Criteria)
	if nameSw.Valid {
		b.NameSw = &nameSw.String
	}
	if awarded.Valid {
		b.Earned = true
		t := awarded.Time
		b.EarnedAt = &t
	}
	if sourceType.Valid {
		b.SourceType = &sourceType.String
	}
	if sourceID.Valid {
		b.SourceID = &sourceID.String
	}
	return b, nil
}

// SetBadgeVisibility only flips the reader's own award.
func (e *Engine) SetBadgeVisibility(ctx context.Context, subject, badgeKey string, hidden bool) (BadgeAward, error) {
	tag, err := e.pool.Exec(ctx, `
		UPDATE recognition.badge_awards SET hidden=$3
		WHERE subject_id=$1 AND badge_key=$2 AND revoked_at IS NULL`,
		subject, badgeKey, hidden)
	if err != nil {
		return BadgeAward{}, err
	}
	if tag.RowsAffected() == 0 {
		return BadgeAward{}, pgx.ErrNoRows
	}
	return e.Badge(ctx, subject, badgeKey)
}

// Missions renders deterministic progress tracks. There are no daily quests.
func (e *Engine) Missions(ctx context.Context, subject, state string) ([]Mission, error) {
	rows, err := e.pool.Query(ctx, `
		SELECT pt.track_key, pt.name_en, pt.name_sw, pt.family, pt.xp_reward,
		       pt.badge_key, pt.target_value, pt.criteria,
		       COALESCE(ps.current_value, 0), ps.completed_at
		FROM recognition.progress_tracks pt
		LEFT JOIN recognition.progress_snapshots ps
		  ON ps.track_key=pt.track_key AND ps.subject_id=$1
		WHERE pt.status='published'
		ORDER BY pt.track_key`, subject)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]Mission, 0)
	for rows.Next() {
		var m Mission
		var criteria []byte
		var completedAt pgtype.Timestamptz
		if err := rows.Scan(&m.TrackKey, &m.NameEn, &m.NameSw, &m.Family, &m.XPReward,
			&m.BadgeKey, &m.Target, &criteria, &m.Current, &completedAt); err != nil {
			return nil, err
		}
		m.Completed = completedAt.Valid || (m.Target > 0 && m.Current >= m.Target)
		if completedAt.Valid {
			t := completedAt.Time
			m.CompletedAt = &t
		}
		if state == "active" && m.Completed {
			continue
		}
		if state == "completed" && !m.Completed {
			continue
		}
		out = append(out, m)
	}
	return out, rows.Err()
}

// Season returns one season by key.
func (e *Engine) Season(ctx context.Context, key string) (Season, error) {
	var s Season
	var nameSw, badgeKey pgtype.Text
	var families []string
	err := e.pool.QueryRow(ctx, `
		SELECT season_key, name_en, name_sw, starts_at, ends_at, status, badge_key,
		       xp_award, mission_families
		FROM recognition.seasons WHERE season_key=$1`, key,
	).Scan(&s.Key, &s.NameEn, &nameSw, &s.StartsAt, &s.EndsAt, &s.Status,
		&badgeKey, &s.XPAward, &families)
	if err != nil {
		return s, err
	}
	if nameSw.Valid {
		s.NameSw = &nameSw.String
	}
	if badgeKey.Valid {
		s.BadgeKey = &badgeKey.String
	}
	if families == nil {
		families = []string{}
	}
	s.MissionFamilies = families
	return s, nil
}

// SeasonProgress adds the reader's private progress.
func (e *Engine) SeasonProgress(ctx context.Context, subject, key string) (SeasonProgress, error) {
	s, err := e.Season(ctx, key)
	if err != nil {
		return SeasonProgress{}, err
	}
	out := SeasonProgress{Season: s}
	var total, done int
	err = e.pool.QueryRow(ctx, `
		SELECT count(*), count(*) FILTER (WHERE ps.completed_at IS NOT NULL)
		FROM recognition.progress_tracks pt
		LEFT JOIN recognition.progress_snapshots ps
		  ON ps.track_key=pt.track_key AND ps.subject_id=$1
		WHERE pt.status='published'`, subject).Scan(&total, &done)
	if err != nil {
		return out, err
	}
	out.Progress.MissionsTotal = total
	out.Progress.MissionsCompleted = done
	err = e.pool.QueryRow(ctx, `
		SELECT COALESCE(SUM(amount),0) FROM recognition.xp_entries
		WHERE subject_id=$1 AND occurred_at >= $2 AND occurred_at <= $3`,
		subject, s.StartsAt, s.EndsAt).Scan(&out.Progress.XP)
	if err != nil {
		return out, err
	}
	if s.BadgeKey != nil {
		err = e.pool.QueryRow(ctx, `
			SELECT EXISTS(SELECT 1 FROM recognition.badge_awards
			              WHERE subject_id=$1 AND badge_key=$2
			                AND awarded_at BETWEEN $3 AND $4)`,
			subject, *s.BadgeKey, s.StartsAt, s.EndsAt).Scan(&out.Progress.BadgeEarned)
		if err != nil {
			return out, err
		}
	}
	err = e.pool.QueryRow(ctx, `
		SELECT COALESCE(bool_or(opted_in), false) FROM recognition.leaderboard_entries le
		JOIN recognition.seasons s ON s.id=le.season_id
		WHERE le.subject_id=$1 AND s.season_key=$2`, subject, key).Scan(&out.OptIn)
	return out, err
}

// Passport is the private county and heritage collection. Exact site names
// live in the owning heritage module; the passport exposes an opaque ref.
func (e *Engine) Passport(ctx context.Context, subject string) (Passport, error) {
	out := Passport{Counties: []PassportCounty{}, Sites: []PassportSite{}}
	rows, err := e.pool.Query(ctx, `
		SELECT (e.data->>'source_id')::int AS gid,
		       COALESCE(c.adm1_name, '') AS name,
		       1 AS sites
		FROM recognition.events e
		LEFT JOIN kenya_counties c ON c.gid = (e.data->>'source_id')::int
		WHERE e.subject=$1 AND e.type='ke.heritage.county.collected.v1'
		ORDER BY gid`, subject)
	if err != nil {
		return out, err
	}
	for rows.Next() {
		var c PassportCounty
		if err := rows.Scan(&c.CountyGID, &c.Name, &c.Sites); err != nil {
			rows.Close()
			return out, err
		}
		c.Complete = true
		out.Counties = append(out.Counties, c)
	}
	rows.Close()

	siteRows, err := e.pool.Query(ctx, `
		SELECT data->>'source_id', time,
		       COALESCE((data->>'privacy_class')='sensitive' OR
		                (data->>'sensitivity') IN ('sensitive','restricted'), false)
		FROM recognition.events
		WHERE subject=$1 AND type='ke.heritage.visit.verified.v1'
		ORDER BY time DESC`, subject)
	if err != nil {
		return out, err
	}
	defer siteRows.Close()
	for siteRows.Next() {
		var s PassportSite
		var name string
		var visited time.Time
		if err := siteRows.Scan(&name, &visited, &s.Sensitive); err != nil {
			return out, err
		}
		s.SiteRef = name
		s.Name = name
		t := visited
		s.VisitedAt = &t
		out.Sites = append(out.Sites, s)
	}
	return out, siteRows.Err()
}

// PublicLeaderboard renders the opt-in heritage board. Returns ErrKilled when
// the board is disabled, and a suppressed board when the cohort is too small.
func (e *Engine) PublicLeaderboard(ctx context.Context, seasonKey string, countyGID *int) (Leaderboard, error) {
	disabled, err := e.switchEnabled(ctx, "public_leaderboard")
	if err != nil {
		return Leaderboard{}, err
	}
	if !disabled {
		disabled, err = e.switchEnabled(ctx, "global")
		if err != nil {
			return Leaderboard{}, err
		}
	}
	if disabled {
		return Leaderboard{}, ErrKilled
	}
	var seasonID string
	err = e.pool.QueryRow(ctx, `SELECT id::text FROM recognition.seasons WHERE season_key=$1`, seasonKey).Scan(&seasonID)
	if errors.Is(err, pgx.ErrNoRows) {
		return Leaderboard{}, pgx.ErrNoRows
	}
	if err != nil {
		return Leaderboard{}, err
	}
	return e.boardQuery(ctx, seasonKey, seasonID, countyGID, "")
}

const minLeaderboardCohort = 10

func (e *Engine) boardQuery(ctx context.Context, seasonKey, seasonID string, countyGID *int, selfSubject string) (Leaderboard, error) {
	out := Leaderboard{
		SeasonKey:   seasonKey,
		CountyGID:   countyGID,
		Entries:     []LeaderboardEntry{},
		RankingNote: "Ranked by verified trail completions, verified sites, then earliest completion. Sensitive sites never appear.",
	}
	var cohort int
	err := e.pool.QueryRow(ctx, `
		SELECT count(*) FROM recognition.leaderboard_entries
		WHERE season_id=$1::uuid AND opted_in AND NOT suppressed AND NOT disqualified
		  AND ($2::int IS NULL OR county_gid=$2)`, seasonID, countyGID).Scan(&cohort)
	if err != nil {
		return out, err
	}
	if cohort < minLeaderboardCohort {
		out.Suppressed = true
		out.SuppressionReason = strPtr("Not enough participants yet.")
		return out, nil
	}
	rows, err := e.pool.Query(ctx, `
		SELECT le.subject_id, le.display_name, kc.adm1_name, le.level,
		       le.public_badge_count, le.verified_sites, le.trail_completions, le.updated_at
		FROM recognition.leaderboard_entries le
		LEFT JOIN recognition.xp_totals t ON t.subject_id=le.subject_id
		LEFT JOIN kenya_counties kc ON kc.gid=le.county_gid
		WHERE le.season_id=$1::uuid AND le.opted_in AND NOT le.suppressed
		  AND NOT le.disqualified
		  AND ($2::int IS NULL OR le.county_gid=$2)
		ORDER BY le.trail_completions DESC, le.verified_sites DESC,
		         COALESCE(t.total_xp,0) DESC, le.last_completion_at ASC NULLS LAST,
		         le.id ASC
		LIMIT 50`, seasonID, countyGID)
	if err != nil {
		return out, err
	}
	defer rows.Close()
	rank := 0
	var updated *time.Time
	for rows.Next() {
		var subject, displayName string
		var county pgtype.Text
		var e LeaderboardEntry
		var rowUpdated time.Time
		if err := rows.Scan(&subject, &displayName, &county, &e.Level,
			&e.PublicBadgeCount, &e.VerifiedSites, &e.TrailCompletions, &rowUpdated); err != nil {
			return out, err
		}
		rank++
		e.Rank = rank
		e.DisplayName = displayName
		if county.Valid {
			e.County = &county.String
		}
		e.IsSelf = selfSubject != "" && subject == selfSubject
		if updated == nil || rowUpdated.After(*updated) {
			u := rowUpdated
			updated = &u
		}
		out.Entries = append(out.Entries, e)
	}
	out.UpdatedAt = updated
	return out, rows.Err()
}

// LeaderboardState tells the reader whether they may join and where they rank.
func (e *Engine) LeaderboardState(ctx context.Context, subject, seasonKey string) (LeaderboardState, error) {
	out := LeaderboardState{SeasonKey: seasonKey}
	opted, eligible, reason, err := e.leaderboardStateFlags(ctx, subject, seasonKey)
	if err != nil {
		return out, err
	}
	out.OptedIn = opted
	out.Eligible = eligible
	out.IneligibleReason = reason
	out.DisplayName, err = e.displayName(ctx, subject)
	if err != nil {
		return out, err
	}
	if !opted {
		return out, nil
	}
	var seasonID string
	if err := e.pool.QueryRow(ctx,
		`SELECT id::text FROM recognition.seasons WHERE season_key=$1`, seasonKey).Scan(&seasonID); err != nil {
		return out, err
	}
	board, err := e.boardQuery(ctx, seasonKey, seasonID, nil, subject)
	if err != nil && !errors.Is(err, ErrKilled) {
		return out, err
	}
	if err == nil {
		out.Board = &board
		for i := range board.Entries {
			if board.Entries[i].IsSelf {
				own := board.Entries[i]
				out.OwnEntry = &own
				break
			}
		}
		if out.OwnEntry == nil {
			// Own rank still exists outside the top 50.
			own, err := e.ownRank(ctx, seasonID, subject)
			if err != nil && !errors.Is(err, pgx.ErrNoRows) {
				return out, err
			}
			if err == nil {
				out.OwnEntry = &own
			}
		}
	}
	return out, nil
}

func (e *Engine) ownRank(ctx context.Context, seasonID, subject string) (LeaderboardEntry, error) {
	var own LeaderboardEntry
	var displayName string
	var county pgtype.Text
	err := e.pool.QueryRow(ctx, `
		WITH ranked AS (
		  SELECT le.*, row_number() OVER (
		    ORDER BY le.trail_completions DESC, le.verified_sites DESC,
		             COALESCE(t.total_xp,0) DESC, le.last_completion_at ASC NULLS LAST,
		             le.id ASC) AS rank
		  FROM recognition.leaderboard_entries le
		  LEFT JOIN recognition.xp_totals t ON t.subject_id=le.subject_id
		  WHERE le.season_id=$1::uuid AND le.opted_in AND NOT le.suppressed
		    AND NOT le.disqualified
		)
		SELECT r.rank, r.display_name, kc.adm1_name, r.level, r.public_badge_count,
		       r.verified_sites, r.trail_completions
		FROM ranked r
		LEFT JOIN kenya_counties kc ON kc.gid=r.county_gid
		WHERE r.subject_id=$2`, seasonID, subject,
	).Scan(&own.Rank, &displayName, &county, &own.Level, &own.PublicBadgeCount,
		&own.VerifiedSites, &own.TrailCompletions)
	if err != nil {
		return own, err
	}
	own.DisplayName = displayName
	own.IsSelf = true
	if county.Valid {
		own.County = &county.String
	}
	return own, nil
}

func (e *Engine) leaderboardStateFlags(ctx context.Context, subject, seasonKey string) (bool, bool, *string, error) {
	var status string
	var startsAt, endsAt time.Time
	err := e.pool.QueryRow(ctx,
		`SELECT status, starts_at, ends_at FROM recognition.seasons WHERE season_key=$1`,
		seasonKey).Scan(&status, &startsAt, &endsAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return false, false, strPtr("no_active_season"), nil
	}
	if err != nil {
		return false, false, nil, err
	}
	var opted bool
	err = e.pool.QueryRow(ctx, `
		SELECT COALESCE(bool_or(le.opted_in), false)
		FROM recognition.leaderboard_entries le
		JOIN recognition.seasons s ON s.id=le.season_id
		WHERE le.subject_id=$1 AND s.season_key=$2`, subject, seasonKey).Scan(&opted)
	if err != nil {
		return false, false, nil, err
	}

	disabled, err := e.switchEnabled(ctx, "public_leaderboard")
	if err != nil {
		return false, false, nil, err
	}
	if !disabled {
		disabled, err = e.switchEnabled(ctx, "global")
		if err != nil {
			return false, false, nil, err
		}
	}
	if disabled {
		return opted, false, strPtr("disabled"), nil
	}
	if status != "active" || time.Now().Before(startsAt) || time.Now().After(endsAt) {
		return opted, false, strPtr("disabled"), nil
	}
	consent, err := e.hasLeaderboardConsent(ctx, subject)
	if err != nil {
		return false, false, nil, err
	}
	if !consent {
		return opted, false, strPtr("consent_missing"), nil
	}
	ageBand, err := e.latestAgeBand(ctx, subject)
	if err != nil {
		return false, false, nil, err
	}
	if ageBand == "under_18" {
		return opted, false, strPtr("under_18"), nil
	}
	return opted, true, nil, nil
}

func (e *Engine) hasLeaderboardConsent(ctx context.Context, subject string) (bool, error) {
	var granted bool
	err := e.pool.QueryRow(ctx, `
		SELECT EXISTS(
		  SELECT 1 FROM recognition.consents
		  WHERE subject_id=$1 AND scope IN ('recognition','leaderboard')
		    AND state='granted' AND withdrawn_at IS NULL
		)`, subject).Scan(&granted)
	return granted, err
}

func (e *Engine) latestAgeBand(ctx context.Context, subject string) (string, error) {
	var band pgtype.Text
	err := e.pool.QueryRow(ctx, `
		SELECT age_band FROM recognition.events
		WHERE subject=$1 AND age_band IS NOT NULL
		ORDER BY time DESC LIMIT 1`, subject).Scan(&band)
	if errors.Is(err, pgx.ErrNoRows) {
		return "unknown", nil
	}
	if err != nil {
		return "", err
	}
	if !band.Valid {
		return "unknown", nil
	}
	return band.String, nil
}

func (e *Engine) displayName(ctx context.Context, subject string) (*DisplayName, error) {
	var d DisplayName
	var prev pgtype.Text
	var next pgtype.Timestamptz
	err := e.pool.QueryRow(ctx, `
		SELECT display_name, status, previous_approved, next_change_allowed
		FROM recognition.display_names WHERE subject_id=$1`, subject,
	).Scan(&d.DisplayName, &d.Status, &prev, &next)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if prev.Valid {
		d.PreviousApproved = &prev.String
	}
	if next.Valid {
		t := next.Time
		d.NextChangeAllowed = &t
	}
	return &d, nil
}

// JoinLeaderboard records the second opt-in: consent plus an explicit season
// join after the display-name preview.
func (e *Engine) JoinLeaderboard(ctx context.Context, subject, seasonKey, displayName, consentVersion string, countyGID *int) (LeaderboardState, error) {
	season, err := e.Season(ctx, seasonKey)
	if err != nil {
		return LeaderboardState{}, err
	}
	if season.Status != "active" {
		return LeaderboardState{}, ErrNotEligible
	}
	// The join body carries the consent version. Record it before the
	// eligibility check so the first join is not asked to join twice.
	if consentVersion != "" {
		if err := e.RecordConsent(ctx, subject, "leaderboard", consentVersion, "granted"); err != nil {
			return LeaderboardState{}, err
		}
	}
	_, eligible, reason, err := e.leaderboardStateFlags(ctx, subject, seasonKey)
	if err != nil {
		return LeaderboardState{}, err
	}
	if !eligible {
		if reason != nil && *reason == "consent_missing" {
			return LeaderboardState{}, ErrConsentMissing
		}
		return LeaderboardState{}, ErrNotEligible
	}
	name := strings.TrimSpace(displayName)
	if err := ValidateDisplayName(name); err != nil {
		return LeaderboardState{}, err
	}

	tx, err := e.pool.Begin(ctx)
	if err != nil {
		return LeaderboardState{}, err
	}
	defer tx.Rollback(ctx)

	if _, err := tx.Exec(ctx, `
		INSERT INTO recognition.display_names
			(subject_id, display_name, status, changed_at, next_change_allowed)
		VALUES ($1,$2,'pending',now(), now() + interval '30 days')
		ON CONFLICT (subject_id) DO UPDATE
		SET display_name=EXCLUDED.display_name, status='pending',
		    changed_at=now(), next_change_allowed=EXCLUDED.next_change_allowed
		WHERE recognition.display_names.status <> 'approved'
		   OR recognition.display_names.next_change_allowed IS NULL
		   OR recognition.display_names.next_change_allowed <= now()`,
		subject, name); err != nil {
		return LeaderboardState{}, err
	}
	_, err = tx.Exec(ctx, `
		INSERT INTO recognition.moderation_cases (kind, subject_id, reason, evidence)
		VALUES ('display_name', $1, 'Display name submitted for the seasonal leaderboard',
		        jsonb_build_object('displayName', $2::text))`, subject, name)
	if err != nil {
		return LeaderboardState{}, err
	}
	id, err := upsertLeaderboardEntry(ctx, tx, seasonKey, subject, name, countyGID, true)
	if err != nil {
		return LeaderboardState{}, err
	}
	_ = id
	if err := tx.Commit(ctx); err != nil {
		return LeaderboardState{}, err
	}
	return e.LeaderboardState(ctx, subject, seasonKey)
}

func upsertLeaderboardEntry(ctx context.Context, tx pgx.Tx, seasonKey, subject, displayName string, countyGID *int, optedIn bool) (string, error) {
	var id string
	err := tx.QueryRow(ctx, `
		INSERT INTO recognition.leaderboard_entries
			(season_id, subject_id, county_gid, display_name, level, public_badge_count,
			 verified_sites, trail_completions, opted_in, suppressed, updated_at)
		SELECT s.id, $2, $3, $4,
		       COALESCE((SELECT level FROM recognition.xp_totals WHERE subject_id=$2), 0),
		       (SELECT count(*) FROM recognition.badge_awards ba
		        JOIN recognition.badge_definitions bd ON bd.badge_key=ba.badge_key
		        WHERE ba.subject_id=$2 AND ba.revoked_at IS NULL
		          AND bd.public_eligible AND bd.privacy_class='public'),
		       (SELECT count(DISTINCT data->>'source_id') FROM recognition.events
		        WHERE subject=$2 AND type='ke.heritage.visit.verified.v1'
		          AND COALESCE(data->>'privacy_class','private') <> 'sensitive'
		          AND COALESCE(data->>'sensitivity','standard')='standard'),
		       (SELECT count(DISTINCT data->>'source_id') FROM recognition.events
		        WHERE subject=$2 AND type='ke.heritage.trail.completed.v1'
		          AND COALESCE(data->>'privacy_class','private') <> 'sensitive'),
		       $5, false, now()
		FROM recognition.seasons s WHERE s.season_key=$1
		ON CONFLICT (season_id, subject_id) DO UPDATE
		SET display_name=EXCLUDED.display_name,
		    county_gid=COALESCE(EXCLUDED.county_gid, recognition.leaderboard_entries.county_gid),
		    opted_in=EXCLUDED.opted_in,
		    suppressed=CASE WHEN EXCLUDED.opted_in THEN false
		                    ELSE recognition.leaderboard_entries.suppressed END,
		    updated_at=now()
		RETURNING id::text`, seasonKey, subject, countyGID, displayName, optedIn).Scan(&id)
	return id, err
}

// LeaveLeaderboard hides the entry immediately and keeps private progress.
func (e *Engine) LeaveLeaderboard(ctx context.Context, subject, seasonKey string) (LeaderboardState, error) {
	_, err := e.pool.Exec(ctx, `
		UPDATE recognition.leaderboard_entries le
		SET opted_in=false, suppressed=true, updated_at=now()
		FROM recognition.seasons s
		WHERE s.id=le.season_id AND s.season_key=$2 AND le.subject_id=$1`,
		subject, seasonKey)
	if err != nil {
		return LeaderboardState{}, err
	}
	return e.LeaderboardState(ctx, subject, seasonKey)
}

// UpdateDisplayName enforces the 30-day change window and queue moderation.
func (e *Engine) UpdateDisplayName(ctx context.Context, subject, displayName string) (DisplayName, error) {
	name := strings.TrimSpace(displayName)
	if err := ValidateDisplayName(name); err != nil {
		return DisplayName{}, err
	}
	tx, err := e.pool.Begin(ctx)
	if err != nil {
		return DisplayName{}, err
	}
	defer tx.Rollback(ctx)

	var current, status string
	var next pgtype.Timestamptz
	err = tx.QueryRow(ctx, `
		SELECT display_name, status, next_change_allowed
		FROM recognition.display_names WHERE subject_id=$1 FOR UPDATE`, subject,
	).Scan(&current, &status, &next)
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return DisplayName{}, err
	}
	if next.Valid && next.Time.After(time.Now()) {
		return DisplayName{}, ErrChangeWindow
	}
	var previous pgtype.Text
	err = tx.QueryRow(ctx, `
		INSERT INTO recognition.display_names AS dn
			(subject_id, display_name, status, previous_approved, changed_at, next_change_allowed)
		VALUES ($1,$2,'pending',
		        CASE WHEN $3='approved' THEN $4 ELSE NULL END,
		        now(), now() + interval '30 days')
		ON CONFLICT (subject_id) DO UPDATE
		SET display_name=EXCLUDED.display_name,
		    status='pending',
		    previous_approved=CASE WHEN dn.status='approved' THEN dn.display_name
		                           ELSE dn.previous_approved END,
		    changed_at=now(),
		    next_change_allowed=EXCLUDED.next_change_allowed
		RETURNING previous_approved`,
		subject, name, status, current).Scan(&previous)
	if err != nil {
		return DisplayName{}, err
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO recognition.moderation_cases (kind, subject_id, reason, evidence)
		VALUES ('display_name', $1, 'Display name change submitted',
		        jsonb_build_object('displayName', $2::text))`, subject, name); err != nil {
		return DisplayName{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return DisplayName{}, err
	}
	out := DisplayName{DisplayName: name, Status: "pending"}
	if previous.Valid {
		out.PreviousApproved = &previous.String
	}
	nextAllowed := time.Now().Add(30 * 24 * time.Hour)
	out.NextChangeAllowed = &nextAllowed
	return out, nil
}

// ReportLeaderboardEntry accepts a report and opens a moderation case. The
// reporter identity stays private; the acknowledgment never leaks whether the
// entry exists.
func (e *Engine) ReportLeaderboardEntry(ctx context.Context, reporter, seasonKey, subjectRef, reason, details string) (string, error) {
	var caseID string
	var subject pgtype.Text
	err := e.pool.QueryRow(ctx, `
		SELECT subject_id FROM recognition.leaderboard_entries
		WHERE id=$1::uuid AND season_id=(SELECT id FROM recognition.seasons WHERE season_key=$2)`,
		subjectRef, seasonKey).Scan(&subject)
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return "", err
	}
	err = e.pool.QueryRow(ctx, `
		INSERT INTO recognition.moderation_cases (kind, subject_id, reported_by, reason, evidence)
		VALUES ('leaderboard_report', NULLIF($1,''), NULLIF($2,''), $3,
		        jsonb_build_object('season', $4::text, 'details', $5::text))
		RETURNING id::text`,
		subject.String, reporter, reason, seasonKey, details).Scan(&caseID)
	return caseID, err
}

// ValidateDisplayName mirrors ADR 0015: 2-24 characters, letters/numbers and
// basic punctuation only.
func ValidateDisplayName(name string) error {
	if len([]rune(name)) < 2 || len([]rune(name)) > 24 {
		return fmt.Errorf("display name must be 2-24 characters")
	}
	for _, r := range name {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9',
			r == ' ', r == '.', r == ',', r == '\'', r == '-':
		default:
			return fmt.Errorf("display name contains an unsupported character")
		}
	}
	return nil
}

func (e *Engine) switchEnabled(ctx context.Context, key string) (bool, error) {
	var enabled bool
	err := e.pool.QueryRow(ctx,
		`SELECT enabled FROM recognition.kill_switches WHERE key=$1`, key).Scan(&enabled)
	if errors.Is(err, pgx.ErrNoRows) {
		return false, nil
	}
	return enabled, err
}

func strPtr(s string) *string { return &s }

// ErrNotEligible, ErrConsentMissing and ErrChangeWindow map to frozen 403/409
// responses in the HTTP layer.
var (
	ErrNotEligible    = errors.New("recognition: not eligible")
	ErrConsentMissing = errors.New("recognition: consent missing")
	ErrChangeWindow   = errors.New("recognition: display-name change window")
)

// compile-time guard: the contract constants stay referenced here.
var _ = contracts.PermRecognitionRead
