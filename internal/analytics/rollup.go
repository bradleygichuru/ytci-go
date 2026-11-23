package analytics

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// SuppressionThreshold is the k-anonymity floor from ADR 0016 §9. Any cell
// below ten actors is suppressed before it reaches the admin dashboard.
const SuppressionThreshold = 10

// Rollup names, also used as analytics_rollup_runs.rollup values.
const (
	RollupDaily     = "analytics_daily"
	RollupJourney   = "analytics_journey_daily"
	RollupRetention = "analytics_retention_cohorts"
)

type rollupMetric struct {
	metric string
	dimKey string
	dim    map[string]any
	value  float64
	sample int
}

// RunRollupForDay computes the nightly aggregates for one UTC day. It is
// idempotent: re-running a day replaces its cells.
func RunRollupForDay(ctx context.Context, pool *pgxpool.Pool, day time.Time) error {
	day = time.Date(day.Year(), day.Month(), day.Day(), 0, 0, 0, 0, time.UTC)
	for _, name := range []string{RollupDaily, RollupJourney, RollupRetention} {
		if err := runRollup(ctx, pool, name, day); err != nil {
			return fmt.Errorf("%s: %w", name, err)
		}
	}
	return nil
}

func runRollup(ctx context.Context, pool *pgxpool.Pool, name string, day time.Time) error {
	if _, err := pool.Exec(ctx, `
		INSERT INTO analytics_rollup_runs (rollup, day, status, started_at)
		VALUES ($1, $2, 'running', now())
		ON CONFLICT (rollup, day) DO UPDATE
		SET status = 'running', started_at = now(), finished_at = NULL, error = NULL`,
		name, day); err != nil {
		return err
	}

	var rows int64
	var err error
	switch name {
	case RollupDaily:
		rows, err = rollupDaily(ctx, pool, day)
	case RollupJourney:
		rows, err = rollupJourney(ctx, pool, day)
	case RollupRetention:
		rows, err = rollupRetention(ctx, pool, day)
	}
	if err != nil {
		_, _ = pool.Exec(ctx, `
			UPDATE analytics_rollup_runs SET status='failed', error=$3, finished_at=now()
			WHERE rollup=$1 AND day=$2`, name, day, err.Error())
		return err
	}
	_, err = pool.Exec(ctx, `
		UPDATE analytics_rollup_runs SET status='ok', rows_written=$3, finished_at=now()
		WHERE rollup=$1 AND day=$2`, name, day, rows)
	return err
}

func rollupDaily(ctx context.Context, pool *pgxpool.Pool, day time.Time) (int64, error) {
	start := day
	end := day.Add(24 * time.Hour)
	metrics := make([]rollupMetric, 0, 16)

	rows, err := pool.Query(ctx, `
		SELECT COALESCE(actor_type,'guest'), count(DISTINCT analytics_id)
		FROM analytics_events
		WHERE received_at >= $1 AND received_at < $2
		  AND analytics_id IS NOT NULL
		  AND COALESCE(actor_type,'guest') <> 'under_18_private'
		GROUP BY 1`, start, end)
	if err != nil {
		return 0, err
	}
	var guests, accounts int
	for rows.Next() {
		var actor string
		var n int
		if err := rows.Scan(&actor, &n); err != nil {
			rows.Close()
			return 0, err
		}
		switch actor {
		case ActorAccount:
			accounts = n
		default:
			guests = n
		}
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return 0, err
	}

	total := guests + accounts
	metrics = append(metrics,
		rollupMetric{"dau", "", map[string]any{}, float64(total), total},
		rollupMetric{"dau", "actor=guest", map[string]any{"actor": "guest"}, float64(guests), guests},
		rollupMetric{"dau", "actor=account", map[string]any{"actor": "account"}, float64(accounts), accounts},
	)

	var newAccounts int
	if err := pool.QueryRow(ctx, `
		SELECT count(*) FROM analytics_id_map
		WHERE created_at >= $1 AND created_at < $2
		  AND actor_type = 'account' AND erased_at IS NULL`, start, end).Scan(&newAccounts); err != nil {
		return 0, err
	}
	metrics = append(metrics, rollupMetric{"new_accounts", "", map[string]any{}, float64(newAccounts), newAccounts})

	for _, window := range []struct {
		metric string
		days   int
	}{{"wau", 7}, {"mau", 30}} {
		var n int
		if err := pool.QueryRow(ctx, `
			SELECT count(DISTINCT analytics_id) FROM analytics_events
			WHERE received_at >= $1 AND received_at < $2
			  AND analytics_id IS NOT NULL
			  AND COALESCE(actor_type,'guest') <> 'under_18_private'`,
			end.AddDate(0, 0, -window.days), end).Scan(&n); err != nil {
			return 0, err
		}
		metrics = append(metrics, rollupMetric{window.metric, "", map[string]any{}, float64(n), n})
	}

	ratio := 0.0
	ratioSample := total
	if accounts > 0 {
		ratio = float64(guests) / float64(accounts)
	}
	metrics = append(metrics, rollupMetric{"guest_to_account_ratio", "", map[string]any{}, ratio, ratioSample})

	var activated, newLastWeek int
	_ = pool.QueryRow(ctx, `
		SELECT count(DISTINCT e.analytics_id)
		FROM analytics_events e
		JOIN analytics_id_map m ON m.analytics_id = e.analytics_id
		WHERE e.received_at >= $1 AND e.received_at < $2
		  AND m.actor_type = 'account' AND m.erased_at IS NULL`, end.AddDate(0, 0, -7), end).Scan(&activated)
	_ = pool.QueryRow(ctx, `
		SELECT count(*) FROM analytics_id_map
		WHERE created_at >= $1 AND created_at < $2
		  AND actor_type='account' AND erased_at IS NULL`, end.AddDate(0, 0, -7), end).Scan(&newLastWeek)
	activation := 0.0
	activationSample := activated
	if newLastWeek > 0 {
		activation = float64(activated) / float64(newLastWeek)
	}
	metrics = append(metrics, rollupMetric{"activation_rate", "", map[string]any{}, activation, activationSample})

	writeDaily := func(m rollupMetric) (int64, error) {
		dim, _ := json.Marshal(m.dim)
		suppressed := m.sample < SuppressionThreshold
		tag, err := pool.Exec(ctx, `
			INSERT INTO analytics_daily
				(day, metric, dimension_key, dimension, value, suppressed, sample_size, computed_at)
			VALUES ($1,$2,$3,$4,$5,$6,$7,now())
			ON CONFLICT (day, metric, dimension_key) DO UPDATE SET
				dimension = EXCLUDED.dimension,
				value = EXCLUDED.value,
				suppressed = EXCLUDED.suppressed,
				sample_size = EXCLUDED.sample_size,
				computed_at = now()`,
			day, m.metric, m.dimKey, dim, m.value, suppressed, m.sample)
		if err != nil {
			return 0, err
		}
		return tag.RowsAffected(), nil
	}

	var written int64
	for _, m := range metrics {
		n, err := writeDaily(m)
		if err != nil {
			return written, err
		}
		written += n
	}

	// Feature adoption is derived from template routes, never resolved URLs.
	adoption, err := pool.Query(ctx, `
		WITH per_actor AS (
			SELECT split_part(trim(leading '/' from route), '/', 1) AS feature,
			       analytics_id, count(*) AS n
			FROM analytics_events
			WHERE received_at >= $1 AND received_at < $2
			  AND route IS NOT NULL AND route <> ''
			  AND analytics_id IS NOT NULL
			  AND COALESCE(actor_type,'guest') <> 'under_18_private'
			GROUP BY 1, 2
		)
		SELECT feature, count(*) AS reach, sum(n) AS depth,
		       count(*) FILTER (WHERE n > 1) AS repeat
		FROM per_actor
		WHERE feature <> ''
		GROUP BY feature`, start, end)
	if err != nil {
		return written, err
	}
	defer adoption.Close()
	for adoption.Next() {
		var feature string
		var reach, depth, repeat int
		if err := adoption.Scan(&feature, &reach, &depth, &repeat); err != nil {
			return written, err
		}
		depthPerActor := 0.0
		if reach > 0 {
			depthPerActor = float64(depth) / float64(reach)
		}
		repeatRate := 0.0
		if reach > 0 {
			repeatRate = float64(repeat) / float64(reach)
		}
		for _, m := range []rollupMetric{
			{"feature_reach", "feature=" + feature, map[string]any{"feature": feature}, float64(reach), reach},
			{"feature_depth", "feature=" + feature, map[string]any{"feature": feature}, depthPerActor, reach},
			{"feature_repeat", "feature=" + feature, map[string]any{"feature": feature}, repeatRate, reach},
		} {
			n, err := writeDaily(m)
			if err != nil {
				return written, err
			}
			written += n
		}
	}
	return written, adoption.Err()
}

func rollupJourney(ctx context.Context, pool *pgxpool.Pool, day time.Time) (int64, error) {
	start := day
	end := day.Add(24 * time.Hour)
	rows, err := pool.Query(ctx, `
		SELECT properties->>'journey',
		       properties->>'step',
		       COALESCE(NULLIF(properties->>'step_index','')::int, 0),
		       count(DISTINCT analytics_id),
		       count(DISTINCT analytics_id) FILTER (WHERE properties->>'outcome' = 'completed')
		FROM analytics_events
		WHERE received_at >= $1 AND received_at < $2
		  AND event_family = 'journey_step'
		  AND analytics_id IS NOT NULL
		  AND properties->>'journey' IS NOT NULL
		GROUP BY 1,2,3`, start, end)
	if err != nil {
		return 0, err
	}
	defer rows.Close()

	var written int64
	for rows.Next() {
		var journey, step string
		var index, reached, completed int
		if err := rows.Scan(&journey, &step, &index, &reached, &completed); err != nil {
			return written, err
		}
		suppressed := reached < SuppressionThreshold
		tag, err := pool.Exec(ctx, `
			INSERT INTO analytics_journey_daily
				(day, journey, step, step_index, reached, completed, suppressed, computed_at)
			VALUES ($1,$2,$3,$4,$5,$6,$7,now())
			ON CONFLICT (day, journey, step_index, step) DO UPDATE SET
				reached = EXCLUDED.reached,
				completed = EXCLUDED.completed,
				suppressed = EXCLUDED.suppressed,
				computed_at = now()`,
			day, journey, step, index, reached, completed, suppressed)
		if err != nil {
			return written, err
		}
		written += tag.RowsAffected()
	}
	return written, rows.Err()
}

func rollupRetention(ctx context.Context, pool *pgxpool.Pool, day time.Time) (int64, error) {
	// Weekly cohorts for the last 13 weeks, at week 0 and the 4/8/12 checkpoints.
	tag, err := pool.Exec(ctx, `
		WITH firsts AS (
			SELECT analytics_id, date_trunc('week', min(started_at))::date AS cohort
			FROM analytics_sessions
			WHERE analytics_id IS NOT NULL
			  AND COALESCE(actor_type,'guest') <> 'under_18_private'
			GROUP BY analytics_id
		),
		cohorts AS (
			SELECT cohort, week_index
			FROM firsts, generate_series(0, 12) AS week_index
			WHERE cohort + (week_index * 7) <= $1::date
		),
		measured AS (
			SELECT c.cohort, c.week_index, count(DISTINCT f.analytics_id) AS actors,
			       count(DISTINCT f.analytics_id) FILTER (
			           WHERE EXISTS (
			               SELECT 1 FROM analytics_sessions s
			               WHERE s.analytics_id = f.analytics_id
			                 AND date_trunc('week', s.started_at)::date = c.cohort + (c.week_index * 7)
			           )) AS retained
			FROM cohorts c
			JOIN firsts f ON f.cohort = c.cohort
			GROUP BY c.cohort, c.week_index
		)
		INSERT INTO analytics_retention_cohorts
			(cohort_week, week_index, actors, retained, suppressed, computed_at)
		SELECT cohort, week_index, actors, retained,
		       actors < $2, now()
		FROM measured
		ON CONFLICT (cohort_week, week_index) DO UPDATE SET
			actors = EXCLUDED.actors,
			retained = EXCLUDED.retained,
			suppressed = EXCLUDED.suppressed,
			computed_at = now()`,
		day, SuppressionThreshold)
	if err != nil {
		return 0, err
	}
	return tag.RowsAffected(), nil
}

// RetentionResult records how many rows each retention rule removed.
type RetentionResult struct {
	Events      int64
	Sessions    int64
	Batches     int64
	Diagnostics int64
	Aggregates  int64
}

// RunRetention applies the privacy-pack retention schedule: raw events and
// sessions for 13 months, daily aggregates for 26 months.
func RunRetention(ctx context.Context, pool *pgxpool.Pool, now time.Time) (RetentionResult, error) {
	var result RetentionResult
	rawCutoff := now.AddDate(0, -13, 0)
	aggregateCutoff := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, time.UTC).AddDate(0, -26, 0)

	steps := []struct {
		dest *int64
		sql  string
		args []any
	}{
		{&result.Events, `DELETE FROM analytics_events WHERE received_at < $1`, []any{rawCutoff}},
		{&result.Sessions, `DELETE FROM analytics_sessions WHERE started_at < $1`, []any{rawCutoff}},
		{&result.Batches, `DELETE FROM analytics_ingest_batches WHERE received_at < $1`, []any{rawCutoff}},
		{&result.Diagnostics, `DELETE FROM analytics_service_diagnostics WHERE received_at < $1`, []any{rawCutoff}},
		{&result.Aggregates, `DELETE FROM analytics_daily WHERE day < $1::date`, []any{aggregateCutoff}},
		{&result.Aggregates, `DELETE FROM analytics_journey_daily WHERE day < $1::date`, []any{aggregateCutoff}},
		{&result.Aggregates, `DELETE FROM analytics_retention_cohorts WHERE cohort_week < $1::date`, []any{aggregateCutoff}},
	}
	for _, step := range steps {
		tag, err := pool.Exec(ctx, step.sql, step.args...)
		if err != nil {
			return result, err
		}
		*step.dest += tag.RowsAffected()
	}
	return result, nil
}

// ExecutePendingErasures retries erasure requests that did not complete.
func ExecutePendingErasures(ctx context.Context, pool *pgxpool.Pool) (int, error) {
	store := NewStore(pool)
	rows, err := pool.Query(ctx, `
		SELECT id::text FROM analytics_erasure_requests
		WHERE status IN ('pending','failed')
		ORDER BY requested_at LIMIT 50`)
	if err != nil {
		return 0, err
	}
	ids := make([]string, 0)
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return 0, err
		}
		ids = append(ids, id)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return 0, err
	}
	done := 0
	for _, id := range ids {
		if _, err := store.processErasure(ctx, id, ""); err != nil {
			slog.Error("analytics erasure failed", "request", id, "error", err)
			continue
		}
		done++
	}
	return done, nil
}

var workerOnce sync.Once

// StartWorker starts the nightly rollup, retention and erasure retry loop.
// It is process-scoped and intentionally waits one interval before its first
// run so test binaries never observe background writes.
func StartWorker(pool *pgxpool.Pool) {
	if pool == nil {
		return
	}
	workerOnce.Do(func() {
		go func() {
			ticker := time.NewTicker(time.Hour)
			defer ticker.Stop()
			for range ticker.C {
				ctx, cancel := context.WithTimeout(context.Background(), 15*time.Minute)
				day := time.Now().UTC().AddDate(0, 0, -1)
				if err := RunRollupForDay(ctx, pool, day); err != nil {
					slog.Error("analytics rollup failed", "day", day.Format("2006-01-02"), "error", err)
				}
				if _, err := RunRetention(ctx, pool, time.Now().UTC()); err != nil {
					slog.Error("analytics retention failed", "error", err)
				}
				if _, err := ExecutePendingErasures(ctx, pool); err != nil {
					slog.Error("analytics erasure retry failed", "error", err)
				}
				cancel()
			}
		}()
	})
}
