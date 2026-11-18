package thennow

import (
	"context"
	"encoding/json"
	"log/slog"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// PublishDue promotes approved revisions whose scheduled_at has passed.
// It is intentionally small and idempotent: a revision is promoted once,
// and a rights-expired revision is skipped and the live experience is hidden.
func PublishDue(ctx context.Context, pool *pgxpool.Pool) (int, error) {
	rows, err := pool.Query(ctx, `
		SELECT r.experience_id::text, r.revision_number, r.snapshot
		FROM then_now_revisions r
		JOIN then_now_experiences e
		  ON e.id = r.experience_id AND e.open_revision_number = r.revision_number
		WHERE r.status = 'approved'
		  AND r.scheduled_at IS NOT NULL
		  AND r.scheduled_at <= now()
		  AND e.status NOT IN ('archived', 'revoked')`)
	if err != nil {
		return 0, err
	}
	defer rows.Close()

	type due struct {
		experienceID string
		revision     int
		snapshot     []byte
	}
	dueRevisions := make([]due, 0)
	for rows.Next() {
		var item due
		if err := rows.Scan(&item.experienceID, &item.revision, &item.snapshot); err != nil {
			return 0, err
		}
		dueRevisions = append(dueRevisions, item)
	}
	if err := rows.Err(); err != nil {
		return 0, err
	}
	rows.Close()

	published := 0
	for _, item := range dueRevisions {
		var snapshot Snapshot
		_ = json.Unmarshal(item.snapshot, &snapshot)
		if snapshot.RightsExpiry != "" {
			if expiry, err := time.Parse("2006-01-02", snapshot.RightsExpiry); err == nil && expiry.Before(time.Now().UTC()) {
				_, _ = pool.Exec(ctx, `
					UPDATE then_now_revisions SET scheduled_at = NULL, updated_at = now()
					WHERE experience_id = $1 AND revision_number = $2`, item.experienceID, item.revision)
				_, _ = pool.Exec(ctx, `
					UPDATE then_now_experiences SET status = 'unpublished', updated_at = now()
					WHERE id = $1 AND status = 'published'`, item.experienceID)
				insertScheduledAudit(ctx, pool, item.experienceID, "schedule_blocked", "Scheduled publish blocked: rights expired")
				continue
			}
		}
		tx, err := pool.Begin(ctx)
		if err != nil {
			return published, err
		}
		tag, err := tx.Exec(ctx, `
			UPDATE then_now_revisions SET status = 'published', published_at = now(), scheduled_at = NULL, updated_at = now()
			WHERE experience_id = $1 AND revision_number = $2 AND status = 'approved'`,
			item.experienceID, item.revision)
		if err != nil {
			_ = tx.Rollback(ctx)
			return published, err
		}
		if tag.RowsAffected() == 0 {
			_ = tx.Rollback(ctx)
			continue
		}
		if _, err := tx.Exec(ctx, `
			UPDATE then_now_experiences SET status = 'published', live_revision_number = $2,
			       open_revision_number = $2, updated_at = now() WHERE id = $1`,
			item.experienceID, item.revision); err != nil {
			_ = tx.Rollback(ctx)
			return published, err
		}
		if err := tx.Commit(ctx); err != nil {
			return published, err
		}
		insertScheduledAudit(ctx, pool, item.experienceID, "schedule_publish", "Scheduled revision published")
		published++
	}
	return published, nil
}

func insertScheduledAudit(ctx context.Context, pool *pgxpool.Pool, experienceID, eventType, summary string) {
	_, _ = pool.Exec(ctx, `
		INSERT INTO then_now_audit_events (entity_type, entity_id, event_type, summary)
		VALUES ('experience', $1, $2, $3)`, experienceID, eventType, summary)
}

// StartScheduler runs PublishDue every interval until ctx is cancelled.
func StartScheduler(ctx context.Context, pool *pgxpool.Pool, interval time.Duration) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			count, err := PublishDue(ctx, pool)
			if err != nil {
				slog.Error("then-now scheduled publish failed", "error", err)
				continue
			}
			if count > 0 {
				slog.Info("then-now scheduled revisions published", "count", count)
			}
		}
	}
}
