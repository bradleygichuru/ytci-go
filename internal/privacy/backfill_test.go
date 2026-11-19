package privacy_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bradleygichuru/ytci-go/internal/db"
	"github.com/bradleygichuru/ytci-go/internal/privacy"
)

const (
	backfillUserOne = "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa"
	backfillUserTwo = "bbbbbbbb-bbbb-4bbb-8bbb-bbbbbbbbbbbb"
	backfillOrphan  = "cccccccc-cccc-4ccc-8ccc-cccccccccccc"
)

func TestBackfillAnalyticsIDs(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}
	ctx := context.Background()
	pool := db.SetupTestDB(t)
	defer pool.Close()

	_, err := pool.Exec(ctx, `
		INSERT INTO users (id, name, email, created_at, updated_at, role) VALUES
			($1, 'Backfill One', 'backfill-one@test.com', now(), now(), 'user'),
			($2, 'Backfill Two', 'backfill-two@test.com', now(), now(), 'user')`,
		backfillUserOne, backfillUserTwo)
	require.NoError(t, err)

	_, err = pool.Exec(ctx, `
		INSERT INTO app_opens (user_id, platform, app_version) VALUES
			($1, 'android', '1.0.0'),
			($2, 'android', '1.0.0'),
			($3, 'android', '1.0.0')`,
		backfillUserOne, backfillUserTwo, backfillOrphan)
	require.NoError(t, err)
	_, err = pool.Exec(ctx, `
		INSERT INTO analytics_events (user_id, event) VALUES ($1, 'map_opened'), ($2, 'story_opened')`,
		backfillUserOne, backfillUserTwo)
	require.NoError(t, err)

	// Rows that are already pseudonymous stay untouched.
	_, err = pool.Exec(ctx, `
		INSERT INTO app_opens (analytics_id, platform, app_version)
		VALUES (gen_random_uuid(), 'android', '1.0.0')`)
	require.NoError(t, err)

	dry, err := privacy.BackfillAnalyticsIDs(ctx, pool, false)
	require.NoError(t, err)
	assert.Equal(t, int64(2), dry.UsersMapped)
	assert.Equal(t, int64(2), dry.AppOpens)
	assert.Equal(t, int64(2), dry.AnalyticsEvents)
	assert.Equal(t, int64(1), dry.OrphanRows)

	var mappings int
	require.NoError(t, pool.QueryRow(ctx, `SELECT count(*) FROM analytics_id_map`).Scan(&mappings))
	assert.Equal(t, 0, mappings, "dry run must not write")

	applied, err := privacy.BackfillAnalyticsIDs(ctx, pool, true)
	require.NoError(t, err)
	assert.Equal(t, int64(2), applied.UsersMapped)
	assert.Equal(t, int64(2), applied.AppOpens)
	assert.Equal(t, int64(2), applied.AnalyticsEvents)
	assert.Equal(t, int64(1), applied.OrphanRows)

	require.NoError(t, pool.QueryRow(ctx, `SELECT count(*) FROM analytics_id_map`).Scan(&mappings))
	assert.Equal(t, 2, mappings)

	var linked, anonymous int
	require.NoError(t, pool.QueryRow(ctx, `
		SELECT count(*) FILTER (WHERE user_id IS NOT NULL),
		       count(*) FILTER (WHERE user_id IS NULL AND analytics_id IS NOT NULL)
		FROM app_opens`).Scan(&linked, &anonymous))
	assert.Equal(t, 1, linked, "only the orphan row keeps a user_id")
	assert.Equal(t, 3, anonymous)

	require.NoError(t, pool.QueryRow(ctx, `
		SELECT count(*) FILTER (WHERE user_id IS NOT NULL),
		       count(*) FILTER (WHERE user_id IS NULL AND analytics_id IS NOT NULL)
		FROM analytics_events`).Scan(&linked, &anonymous))
	assert.Equal(t, 0, linked)
	assert.Equal(t, 2, anonymous)

	// Historical rows keep the same pseudonymous identity as their mapping.
	var stable int
	require.NoError(t, pool.QueryRow(ctx, `
		SELECT count(*) FROM app_opens a
		JOIN analytics_id_map m ON m.analytics_id = a.analytics_id
		WHERE a.user_id IS NULL AND m.user_id = $1`, backfillUserOne).Scan(&stable))
	assert.Equal(t, 1, stable)

	again, err := privacy.BackfillAnalyticsIDs(ctx, pool, true)
	require.NoError(t, err)
	assert.Equal(t, int64(0), again.UsersMapped)
	assert.Equal(t, int64(0), again.AppOpens)
	assert.Equal(t, int64(0), again.AnalyticsEvents)
	assert.Equal(t, int64(1), again.OrphanRows)
}
