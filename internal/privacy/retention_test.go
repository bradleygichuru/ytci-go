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
			(CURRENT_DATE - INTERVAL '27 months', 'sensitive', NULL, 'portal_opened', 1),
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

func TestRunOnceDeletesMiceAttendanceAfterThirtyDays(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}
	ctx := context.Background()
	pool := db.SetupTestDB(t)
	defer pool.Close()

	const (
		userID      = "retention-mice-user"
		oldEventID  = "11111111-1111-1111-1111-111111111111"
		recentEvent = "22222222-2222-2222-2222-222222222222"
		oldRegID    = "33333333-3333-3333-3333-333333333333"
		recentRegID = "44444444-4444-4444-4444-444444444444"
	)
	_, err := pool.Exec(ctx, `
		INSERT INTO users (id, name, email)
		VALUES ($1, 'MICE Retention User', 'retention-mice@test.local')`, userID)
	require.NoError(t, err)

	_, err = pool.Exec(ctx, `
		INSERT INTO events (id, title, organizer, county, event_date, type, status)
		VALUES
			($1, 'Old event', 'YTCI', 'Nairobi', CURRENT_DATE - 45, 'tourism', 'scheduled'),
			($2, 'Recent event', 'YTCI', 'Nairobi', CURRENT_DATE, 'tourism', 'scheduled')`,
		oldEventID, recentEvent)
	require.NoError(t, err)

	_, err = pool.Exec(ctx, `
		INSERT INTO event_registrations (id, event_id, user_id, status)
		VALUES ($1, $2, $4, 'confirmed'), ($3, $5, $4, 'confirmed')`,
		oldRegID, oldEventID, recentRegID, userID, recentEvent)
	require.NoError(t, err)

	_, err = pool.Exec(ctx, `
		INSERT INTO event_tickets (registration_id, token_hash, expires_at)
		VALUES ($1, 'old-token-hash', now() + interval '1 day'),
		       ($2, 'recent-token-hash', now() + interval '1 day')`,
		oldRegID, recentRegID)
	require.NoError(t, err)

	_, err = pool.Exec(ctx, `
		INSERT INTO event_checkins (registration_id, event_id, user_id, method)
		VALUES ($1, $2, $4, 'qr'), ($3, $5, $4, 'qr')`,
		oldRegID, oldEventID, recentRegID, userID, recentEvent)
	require.NoError(t, err)

	_, err = pool.Exec(ctx, `
		INSERT INTO event_checkin_scans (event_id, device_id, client_scan_id, client_ts)
		VALUES ($1, 'device-old', 'scan-old', now()), ($2, 'device-recent', 'scan-recent', now())`,
		oldEventID, recentEvent)
	require.NoError(t, err)

	_, err = pool.Exec(ctx, `
		INSERT INTO event_ticket_handoffs (event_id, user_id)
		VALUES ($1, $2), ($3, $2)`, oldEventID, userID, recentEvent)
	require.NoError(t, err)

	_, err = pool.Exec(ctx, `
		INSERT INTO event_notifications (event_id, user_id, kind, event_version)
		VALUES ($1, $2, 'reminder', 1), ($3, $2, 'reminder', 1)`,
		oldEventID, userID, recentEvent)
	require.NoError(t, err)

	result, err := privacy.RunOnce(ctx, pool)
	require.NoError(t, err)
	assert.Equal(t, int64(1), result.MiceRegistrations)
	assert.Equal(t, int64(1), result.MiceScans)
	assert.Equal(t, int64(1), result.MiceHandoffs)
	assert.Equal(t, int64(1), result.MiceNotifications)

	var registrations, tickets, checkins, scans, handoffs, notifications int
	require.NoError(t, pool.QueryRow(ctx,
		`SELECT count(*) FROM event_registrations WHERE event_id = $1`, oldEventID).Scan(&registrations))
	require.NoError(t, pool.QueryRow(ctx,
		`SELECT count(*) FROM event_tickets WHERE registration_id = $1`, oldRegID).Scan(&tickets))
	require.NoError(t, pool.QueryRow(ctx,
		`SELECT count(*) FROM event_checkins WHERE registration_id = $1`, oldRegID).Scan(&checkins))
	require.NoError(t, pool.QueryRow(ctx,
		`SELECT count(*) FROM event_checkin_scans WHERE event_id = $1`, oldEventID).Scan(&scans))
	require.NoError(t, pool.QueryRow(ctx,
		`SELECT count(*) FROM event_ticket_handoffs WHERE event_id = $1`, oldEventID).Scan(&handoffs))
	require.NoError(t, pool.QueryRow(ctx,
		`SELECT count(*) FROM event_notifications WHERE event_id = $1`, oldEventID).Scan(&notifications))

	assert.Zero(t, registrations)
	assert.Zero(t, tickets)
	assert.Zero(t, checkins)
	assert.Zero(t, scans)
	assert.Zero(t, handoffs)
	assert.Zero(t, notifications)

	// The recent event is inside the 30-day window and must survive.
	require.NoError(t, pool.QueryRow(ctx,
		`SELECT count(*) FROM event_registrations WHERE event_id = $1`, recentEvent).Scan(&registrations))
	assert.Equal(t, 1, registrations)
}
