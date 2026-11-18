package privacy_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bradleygichuru/ytci-go/internal/db"
	"github.com/bradleygichuru/ytci-go/internal/privacy"
)

func TestRunOnceAppliesRetention(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}
	ctx := context.Background()
	pool := db.SetupTestDB(t)
	defer pool.Close()

	_, err := pool.Exec(ctx, `
		INSERT INTO app_opens (analytics_id, platform, app_version, opened_at) VALUES
			(gen_random_uuid(), 'android', '1.0.0', now() - interval '15 months'),
			(gen_random_uuid(), 'android', '1.0.0', now())`)
	require.NoError(t, err)
	_, err = pool.Exec(ctx, `
		INSERT INTO analytics_events (analytics_id, event, created_at) VALUES
			(gen_random_uuid(), 'map_opened', now() - interval '15 months'),
			(gen_random_uuid(), 'map_opened', now())`)
	require.NoError(t, err)
	_, err = pool.Exec(ctx, `
		INSERT INTO anonymous_heritage_counts (day, scope, site_id, event, count) VALUES
			(CURRENT_DATE - INTERVAL '25 months', 'sensitive', NULL, 'portal_opened', 1),
			(CURRENT_DATE, 'sensitive', NULL, 'portal_opened', 1)`)
	require.NoError(t, err)

	result, err := privacy.RunOnce(ctx, pool)
	require.NoError(t, err)
	assert.Equal(t, int64(1), result.AppOpens)
	assert.Equal(t, int64(1), result.AnalyticsEvents)
	assert.Equal(t, int64(1), result.AnonymousCounts)

	var appOpens, analyticsEvents, anonymousCounts int
	require.NoError(t, pool.QueryRow(ctx, `SELECT count(*) FROM app_opens`).Scan(&appOpens))
	require.NoError(t, pool.QueryRow(ctx, `SELECT count(*) FROM analytics_events`).Scan(&analyticsEvents))
	require.NoError(t, pool.QueryRow(ctx, `SELECT count(*) FROM anonymous_heritage_counts`).Scan(&anonymousCounts))
	assert.Equal(t, 1, appOpens)
	assert.Equal(t, 1, analyticsEvents)
	assert.Equal(t, 1, anonymousCounts)
}
