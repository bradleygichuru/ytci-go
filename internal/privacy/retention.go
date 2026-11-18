package privacy

import (
	"context"
	"log/slog"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// RetentionResult records how many rows each retention rule removed.
type RetentionResult struct {
	AppOpens        int64
	AnalyticsEvents int64
	AuditLogs       int64
	AnonymousCounts int64
}

// RunOnce applies the retention schedule from the recognition privacy spec.
// It is deliberately limited to non-recognition data because v1 does not store
// server-side heritage visits. Verification and recognition rows join this
// worker when the enablement gate is signed.
func RunOnce(ctx context.Context, pool *pgxpool.Pool) (RetentionResult, error) {
	var result RetentionResult

	tag, err := pool.Exec(ctx, `DELETE FROM app_opens WHERE opened_at < now() - interval '14 months'`)
	if err != nil {
		return result, err
	}
	result.AppOpens = tag.RowsAffected()

	tag, err = pool.Exec(ctx, `DELETE FROM analytics_events WHERE created_at < now() - interval '14 months'`)
	if err != nil {
		return result, err
	}
	result.AnalyticsEvents = tag.RowsAffected()

	var hasAuditLogs bool
	if err := pool.QueryRow(ctx, `SELECT to_regclass('public.audit_logs') IS NOT NULL`).Scan(&hasAuditLogs); err != nil {
		return result, err
	}
	if hasAuditLogs {
		tag, err = pool.Exec(ctx, `DELETE FROM audit_logs WHERE created_at < now() - interval '12 months'`)
		if err != nil {
			return result, err
		}
		result.AuditLogs = tag.RowsAffected()
	}

	tag, err = pool.Exec(ctx, `DELETE FROM anonymous_heritage_counts WHERE day < CURRENT_DATE - INTERVAL '24 months'`)
	if err != nil {
		return result, err
	}
	result.AnonymousCounts = tag.RowsAffected()

	return result, nil
}

// StartRetention runs the retention worker on an interval until ctx is done.
func StartRetention(ctx context.Context, pool *pgxpool.Pool, interval time.Duration) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			result, err := RunOnce(ctx, pool)
			if err != nil {
				slog.Error("privacy retention failed", "error", err)
				continue
			}
			slog.Info("privacy retention ran",
				"app_opens", result.AppOpens,
				"analytics_events", result.AnalyticsEvents,
				"audit_logs", result.AuditLogs,
				"anonymous_counts", result.AnonymousCounts,
			)
		}
	}
}
