package privacy

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5/pgxpool"
)

// AnalyticsBackfillResult records the one-time migration of account-linked
// analytics rows to pseudonymous analytics IDs.
type AnalyticsBackfillResult struct {
	UsersMapped     int64
	AppOpens        int64
	AnalyticsEvents int64
	OrphanRows      int64
}

// BackfillAnalyticsIDs migrates historical app_opens and analytics_events rows
// from the account user_id to a random analytics_id. It is idempotent and is
// intended to run once at the analytics v2 cutover, before the flag is enabled.
//
// Users that still exist get a persistent analytics_id_map entry so account
// deletion, cohort reporting, and audited support access keep working. Rows
// that reference a user_id no longer present in users are reported and left
// untouched; normal account deletion already clears those rows.
//
// apply=false runs the same queries as a dry run and writes nothing.
func BackfillAnalyticsIDs(ctx context.Context, pool *pgxpool.Pool, apply bool) (AnalyticsBackfillResult, error) {
	var result AnalyticsBackfillResult

	if !apply {
		if err := pool.QueryRow(ctx, `
			WITH old_users AS (
				SELECT user_id::text AS user_id FROM app_opens WHERE user_id IS NOT NULL
				UNION
				SELECT user_id FROM analytics_events WHERE user_id IS NOT NULL
			)
			SELECT count(*) FILTER (
				WHERE EXISTS (SELECT 1 FROM users u WHERE u.id = old_users.user_id)
				  AND NOT EXISTS (SELECT 1 FROM analytics_id_map m WHERE m.user_id = old_users.user_id)
			)
			FROM old_users`).Scan(&result.UsersMapped); err != nil {
			return result, fmt.Errorf("count users to map: %w", err)
		}

		if err := pool.QueryRow(ctx, `
			SELECT count(*) FROM app_opens a
			WHERE a.user_id IS NOT NULL
			  AND (EXISTS (SELECT 1 FROM analytics_id_map m WHERE m.user_id = a.user_id::text)
			       OR EXISTS (SELECT 1 FROM users u WHERE u.id = a.user_id::text))`).Scan(&result.AppOpens); err != nil {
			return result, fmt.Errorf("count app opens: %w", err)
		}

		if err := pool.QueryRow(ctx, `
			SELECT count(*) FROM analytics_events e
			WHERE e.user_id IS NOT NULL
			  AND (EXISTS (SELECT 1 FROM analytics_id_map m WHERE m.user_id = e.user_id)
			       OR EXISTS (SELECT 1 FROM users u WHERE u.id = e.user_id))`).Scan(&result.AnalyticsEvents); err != nil {
			return result, fmt.Errorf("count analytics events: %w", err)
		}

		if err := pool.QueryRow(ctx, `
			SELECT
				(SELECT count(*) FROM app_opens a
				 WHERE a.user_id IS NOT NULL
				   AND NOT EXISTS (SELECT 1 FROM analytics_id_map m WHERE m.user_id = a.user_id::text)
				   AND NOT EXISTS (SELECT 1 FROM users u WHERE u.id = a.user_id::text))
				+
				(SELECT count(*) FROM analytics_events e
				 WHERE e.user_id IS NOT NULL
				   AND NOT EXISTS (SELECT 1 FROM analytics_id_map m WHERE m.user_id = e.user_id)
				   AND NOT EXISTS (SELECT 1 FROM users u WHERE u.id = e.user_id))`).Scan(&result.OrphanRows); err != nil {
			return result, fmt.Errorf("count orphan rows: %w", err)
		}

		return result, nil
	}

	tx, err := pool.Begin(ctx)
	if err != nil {
		return result, err
	}
	defer tx.Rollback(ctx)

	tag, err := tx.Exec(ctx, `
		INSERT INTO analytics_id_map (user_id)
		SELECT DISTINCT old_users.user_id
		FROM (
			SELECT user_id::text AS user_id FROM app_opens WHERE user_id IS NOT NULL
			UNION
			SELECT user_id FROM analytics_events WHERE user_id IS NOT NULL
		) old_users
		JOIN users u ON u.id = old_users.user_id
		ON CONFLICT (user_id) DO NOTHING`)
	if err != nil {
		return result, fmt.Errorf("create analytics mappings: %w", err)
	}
	result.UsersMapped = tag.RowsAffected()

	tag, err = tx.Exec(ctx, `
		UPDATE app_opens a
		SET analytics_id = COALESCE(a.analytics_id, m.analytics_id),
		    user_id = NULL
		FROM analytics_id_map m
		WHERE a.user_id::text = m.user_id`)
	if err != nil {
		return result, fmt.Errorf("backfill app opens: %w", err)
	}
	result.AppOpens = tag.RowsAffected()

	tag, err = tx.Exec(ctx, `
		UPDATE analytics_events e
		SET analytics_id = COALESCE(e.analytics_id, m.analytics_id),
		    user_id = NULL
		FROM analytics_id_map m
		WHERE e.user_id = m.user_id`)
	if err != nil {
		return result, fmt.Errorf("backfill analytics events: %w", err)
	}
	result.AnalyticsEvents = tag.RowsAffected()

	if err := tx.QueryRow(ctx, `
		SELECT
			(SELECT count(*) FROM app_opens a
			 WHERE a.user_id IS NOT NULL
			   AND NOT EXISTS (SELECT 1 FROM analytics_id_map m WHERE m.user_id = a.user_id::text))
			+
			(SELECT count(*) FROM analytics_events e
			 WHERE e.user_id IS NOT NULL
			   AND NOT EXISTS (SELECT 1 FROM analytics_id_map m WHERE m.user_id = e.user_id))`).Scan(&result.OrphanRows); err != nil {
		return result, fmt.Errorf("count orphan rows: %w", err)
	}

	if err := tx.Commit(ctx); err != nil {
		return result, fmt.Errorf("commit analytics backfill: %w", err)
	}
	return result, nil
}
