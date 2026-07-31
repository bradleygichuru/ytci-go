package handler

import (
	"context"

	"github.com/jackc/pgx/v5/pgxpool"
)

func AwardBadge(ctx context.Context, pool *pgxpool.Pool, userID string, badgeName *string, badgeIconURL *string, sourceType, sourceID, sourceTitle string) {
	if badgeName == nil || *badgeName == "" {
		return
	}
	bIcon := ""
	if badgeIconURL != nil {
		bIcon = *badgeIconURL
	}
	_, _ = pool.Exec(ctx,
		`INSERT INTO badges (user_id, badge_name, badge_icon_url, source_type, source_id, source_title)
		 VALUES ($1, $2, $3, $4, $5, $6)
		 ON CONFLICT DO NOTHING`,
		userID, *badgeName, bIcon, sourceType, sourceID, sourceTitle)
}
