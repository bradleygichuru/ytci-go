package expo

import (
	"context"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/bradleygichuru/ytci-go/internal/push"
)

// pushItineraryMembers queues a per-user notification for every member of a
// trip, excluding the actor. It is intentionally fire-and-forget: a push
// failure must never fail the write that triggered it.
func pushItineraryMembers(ctx context.Context, pool *pgxpool.Pool, itineraryID, excludeUserID, title, body string) {
	rows, err := pool.Query(ctx,
		`SELECT user_id FROM itinerary_members WHERE itinerary_id = $1 AND user_id <> $2`,
		itineraryID, excludeUserID)
	if err != nil {
		return
	}
	defer rows.Close()
	for rows.Next() {
		var userID string
		if err := rows.Scan(&userID); err != nil {
			continue
		}
		_ = push.EnqueueUser(ctx, pool, userID, title, body, map[string]any{
			"type":        "trip",
			"itineraryId": itineraryID,
			"route":       "/itinerary/" + itineraryID,
		})
	}
}
