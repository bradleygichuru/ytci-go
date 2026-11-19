package recognition_test

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bradleygichuru/ytci-go/internal/db"
	"github.com/bradleygichuru/ytci-go/internal/recognition"
)

func setup(t *testing.T) (*recognition.Engine, *pgxpool.Pool) {
	t.Helper()
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}
	pool := db.SetupTestDB(t)
	t.Cleanup(pool.Close)
	for _, user := range []struct{ id, role string }{
		{"author-a", "content_editor"},
		{"approver-b", "super_admin"},
		{"admin-a", "administrator"},
		{"admin-b", "super_admin"},
		{"super", "super_admin"},
	} {
		_, err := pool.Exec(context.Background(), `
			INSERT INTO users (id, name, email, role)
			VALUES ($1, $1, $1 || '@example.test', $2)
			ON CONFLICT (id) DO NOTHING`, user.id, user.role)
		require.NoError(t, err)
	}
	return recognition.New(pool), pool
}

func attendanceEvent(subject, eventID string, at time.Time) recognition.Event {
	return recognition.Event{
		Source:  "ke.events.api",
		Type:    "ke.event.attended.v1",
		Subject: subject,
		Time:    at,
		Data: recognition.Payload{
			SourceType:         "event",
			SourceID:           eventID,
			OccurrenceKey:      "event.attendance:" + eventID,
			VerificationMethod: "staff_scan",
			VerificationResult: "verified",
			PrivacyClass:       "private",
			AgeBand:            "18_plus",
		},
	}
}

func TestRecordIsIdempotentAndAwardsOnce(t *testing.T) {
	e, pool := setup(t)
	ctx := context.Background()
	at := time.Now().UTC().Add(-time.Hour)

	first, err := e.Record(ctx, attendanceEvent("reader-1", "event-1", at))
	require.NoError(t, err)
	require.False(t, first.Duplicate)

	// Same transport id: a retry, not a second fact.
	again, err := e.Record(ctx, attendanceEvent("reader-1", "event-1", at))
	require.NoError(t, err)
	assert.True(t, again.Duplicate)
	assert.Equal(t, first.EventID, again.EventID)

	// Same occurrence with a fresh transport id is still the same fact.
	fresh := attendanceEvent("reader-1", "event-1", at)
	fresh.ID = mustUUID(t)
	dupOccurrence, err := e.Record(ctx, fresh)
	require.NoError(t, err)
	assert.True(t, dupOccurrence.Duplicate)
	assert.Equal(t, first.EventID, dupOccurrence.EventID)

	stats, err := e.ProcessDue(ctx, "test-worker", 10)
	require.NoError(t, err)
	assert.Equal(t, 1, stats.Claimed)
	assert.Equal(t, 1, stats.Done)

	var total int64
	require.NoError(t, pool.QueryRow(ctx,
		`SELECT total_xp FROM recognition.xp_totals WHERE subject_id='reader-1'`).Scan(&total))
	assert.Equal(t, int64(50), total)

	var entries int
	require.NoError(t, pool.QueryRow(ctx,
		`SELECT count(*) FROM recognition.xp_entries WHERE subject_id='reader-1'`).Scan(&entries))
	assert.Equal(t, 1, entries)

	var badges int
	require.NoError(t, pool.QueryRow(ctx,
		`SELECT count(*) FROM recognition.badge_awards WHERE subject_id='reader-1'`).Scan(&badges))
	assert.Equal(t, 1, badges)

	// Processing again is a no-op.
	stats, err = e.ProcessDue(ctx, "test-worker", 10)
	require.NoError(t, err)
	assert.Equal(t, 0, stats.Claimed)
}

func TestDuplicateAwardCannotMintSecondXP(t *testing.T) {
	e, pool := setup(t)
	ctx := context.Background()
	at := time.Now().UTC().Add(-time.Hour)

	// Two distinct events for the same source (a re-emitted award) must not
	// create a second ledger row because the award key excludes the event id.
	for i := 0; i < 2; i++ {
		ev := attendanceEvent("reader-2", "event-dup", at)
		ev.ID = mustUUID(t)
		_, err := e.Record(ctx, ev)
		if i == 0 {
			require.NoError(t, err)
			continue
		}
		// The occurrence layer catches this before the ledger does.
		require.NoError(t, err)
	}
	stats, err := e.ProcessDue(ctx, "w", 10)
	require.NoError(t, err)
	assert.Equal(t, 1, stats.Awards)

	var total int64
	require.NoError(t, pool.QueryRow(ctx,
		`SELECT total_xp FROM recognition.xp_totals WHERE subject_id='reader-2'`).Scan(&total))
	assert.Equal(t, int64(50), total)

	// Simulate an at-least-once replay with a different event id that bypassed
	// the occurrence check (historical rows): the partial unique index still
	// protects the award.
	eventID := mustUUID(t)
	_, err = pool.Exec(ctx, `
		INSERT INTO recognition.events
			(id, source, type, subject, time, data, idempotency_key, occurrence_key,
			 verification_method, verification_result)
		VALUES ($1::uuid,'ke.events.api','ke.event.attended.v1','reader-2',now(),
		        jsonb_build_object('source_type','event','source_id','event-dup',
		          'occurrence_key','event.attendance:event-dup-replay',
		          'verification_method','staff_scan','verification_result','verified',
		          'privacy_class','private','sensitivity','standard',
		          'program_scope','ytci'),$1,'event.attendance:event-dup-replay',
		        'staff_scan','verified')`, eventID)
	require.NoError(t, err)
	_, err = pool.Exec(ctx, `
		INSERT INTO recognition.outbox (event_id) VALUES ($1::uuid)`, eventID)
	require.NoError(t, err)
	_, err = e.ProcessDue(ctx, "w", 10)
	require.NoError(t, err)
	require.NoError(t, pool.QueryRow(ctx,
		`SELECT total_xp FROM recognition.xp_totals WHERE subject_id='reader-2'`).Scan(&total))
	assert.Equal(t, int64(50), total)
}

func TestRetryBackoffAndDeadLetter(t *testing.T) {
	e, pool := setup(t)
	ctx := context.Background()

	// An unreadable payload forces the worker to fail. The event fact and its
	// outbox row already exist, which is what a crash mid-batch looks like.
	eventID := mustUUID(t)
	_, err := pool.Exec(ctx, `
		INSERT INTO recognition.events
			(id, source, type, subject, time, data, idempotency_key, occurrence_key)
		VALUES ($1::uuid,'ke.bad.api','ke.event.attended.v1','reader-retry',now(),
		        '[]'::jsonb,$1,'event.attendance:bad')`, eventID)
	require.NoError(t, err)
	_, err = pool.Exec(ctx, `
		INSERT INTO recognition.outbox (event_id) VALUES ($1::uuid)`, eventID)
	require.NoError(t, err)

	for i := 1; i <= 5; i++ {
		_, err := e.ProcessDue(ctx, "retry-worker", 1)
		require.NoError(t, err)
		var attempts int
		var status string
		require.NoError(t, pool.QueryRow(ctx,
			`SELECT attempts, status FROM recognition.outbox WHERE event_id=$1::uuid`,
			eventID).Scan(&attempts, &status))
		assert.Equal(t, i, attempts)
		if i < 5 {
			assert.Equal(t, "pending", status)
			// Ignore the exponential delay in the test clock.
			_, err = pool.Exec(ctx,
				`UPDATE recognition.outbox SET available_at=now() WHERE event_id=$1::uuid`,
				eventID)
			require.NoError(t, err)
		} else {
			assert.Equal(t, "dead", status)
		}
	}
	assert.Greater(t, recognition.Backoff(3), recognition.Backoff(1))
	assert.Equal(t, 5*time.Minute, recognition.Backoff(20))
}

func TestBackfillDryRunAndSecondApprover(t *testing.T) {
	e, pool := setup(t)
	ctx := context.Background()
	occurred := time.Date(2026, 10, 2, 10, 0, 0, 0, time.UTC)

	ev := attendanceEvent("reader-backfill", "event-historic", occurred)
	_, err := e.Record(ctx, ev)
	require.NoError(t, err)

	// Dry run: diff only, no ledger row.
	batch, err := e.CreateBackfill(ctx, "author-a", "event.attendance", 1,
		time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC),
		time.Date(2026, 11, 1, 0, 0, 0, 0, time.UTC))
	require.NoError(t, err)
	require.NotNil(t, batch.Diff)
	assert.Equal(t, 1, batch.Diff.WouldAward)
	assert.Equal(t, "dry_run", batch.Status)

	var before int
	require.NoError(t, pool.QueryRow(ctx,
		`SELECT count(*) FROM recognition.xp_entries WHERE subject_id='reader-backfill'`).Scan(&before))
	assert.Equal(t, 0, before)

	// Same actor cannot approve.
	_, err = e.ApproveBackfill(ctx, "author-a", batch.ID, "")
	assert.ErrorIs(t, err, recognition.ErrSelfApproval)

	approved, err := e.ApproveBackfill(ctx, "approver-b", batch.ID, "")
	require.NoError(t, err)
	assert.Equal(t, "completed", approved.Status)
	assert.Equal(t, 1, approved.Awarded)

	var total int64
	require.NoError(t, pool.QueryRow(ctx,
		`SELECT total_xp FROM recognition.xp_totals WHERE subject_id='reader-backfill'`).Scan(&total))
	assert.Equal(t, int64(50), total)

	// A second batch sees the award as already present.
	second, err := e.CreateBackfill(ctx, "author-a", "event.attendance", 1,
		time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC),
		time.Date(2026, 11, 1, 0, 0, 0, 0, time.UTC))
	require.NoError(t, err)
	require.NotNil(t, second.Diff)
	assert.Equal(t, 0, second.Diff.WouldAward)
	assert.Equal(t, 1, second.Diff.WouldSkip)
}

func TestLeaderboardSuppressionSmallCohort(t *testing.T) {
	e, pool := setup(t)
	ctx := context.Background()

	insertEntry := func(subject string) {
		_, err := pool.Exec(ctx, `
			INSERT INTO recognition.leaderboard_entries
				(season_id, subject_id, display_name, level, public_badge_count,
				 verified_sites, trail_completions, opted_in)
			SELECT id, $1, 'Reader ' || $1, 1, 1, 2, 1, true
			FROM recognition.seasons WHERE season_key='2026-q4'`, subject)
		require.NoError(t, err)
	}

	for i := 0; i < 9; i++ {
		insertEntry("small-" + string(rune('a'+i)))
	}
	board, err := e.PublicLeaderboard(ctx, "2026-q4", nil)
	require.NoError(t, err)
	assert.True(t, board.Suppressed)
	require.NotNil(t, board.SuppressionReason)

	insertEntry("tenth")
	board, err = e.PublicLeaderboard(ctx, "2026-q4", nil)
	require.NoError(t, err)
	assert.False(t, board.Suppressed)
	assert.Equal(t, 10, len(board.Entries))
	assert.Equal(t, 1, board.Entries[0].Rank)
}

func TestPublicLeaderboardKillSwitch(t *testing.T) {
	e, _ := setup(t)
	ctx := context.Background()
	_, err := e.SetKillSwitch(ctx, "super", "public_leaderboard", true,
		"pilot gate", "")
	require.NoError(t, err)
	_, err = e.PublicLeaderboard(ctx, "2026-q4", nil)
	assert.ErrorIs(t, err, recognition.ErrKilled)
}

func TestLedgerReconciliation(t *testing.T) {
	e, pool := setup(t)
	ctx := context.Background()
	at := time.Now().UTC().Add(-time.Hour)

	_, err := e.Record(ctx, attendanceEvent("reader-reconcile", "event-r", at))
	require.NoError(t, err)
	_, err = e.ProcessDue(ctx, "w", 10)
	require.NoError(t, err)

	drift, err := e.Reconcile(ctx)
	require.NoError(t, err)
	assert.Empty(t, drift)

	// Corruption is reported, never auto-corrected.
	_, err = pool.Exec(ctx,
		`UPDATE recognition.xp_totals SET total_xp=999 WHERE subject_id='reader-reconcile'`)
	require.NoError(t, err)
	drift, err = e.Reconcile(ctx)
	require.NoError(t, err)
	require.Len(t, drift, 1)
	assert.Equal(t, int64(999), drift[0].Projected)
	assert.Equal(t, int64(50), drift[0].Ledger)
	var total int64
	require.NoError(t, pool.QueryRow(ctx,
		`SELECT total_xp FROM recognition.xp_totals WHERE subject_id='reader-reconcile'`).Scan(&total))
	assert.Equal(t, int64(999), total)
}

func TestAudioWeeklyCapAndSourceKillSwitch(t *testing.T) {
	e, pool := setup(t)
	ctx := context.Background()
	week := time.Date(2026, 11, 16, 10, 0, 0, 0, time.UTC)

	for i := 0; i < 6; i++ {
		id := "story-" + string(rune('a'+i))
		ev := recognition.Event{
			Source:  "ke.audio.api",
			Type:    "ke.audio.story.completed.v1",
			Subject: "listener-1",
			Time:    week.Add(time.Duration(i) * time.Minute),
			Data: recognition.Payload{
				SourceType:         "audio_story",
				SourceID:           id,
				OccurrenceKey:      "audio.story:" + id,
				VerificationMethod: "server_recorded",
				VerificationResult: "server_recorded",
				PrivacyClass:       "private",
			},
		}
		_, err := e.Record(ctx, ev)
		require.NoError(t, err)
	}
	_, err := e.ProcessDue(ctx, "w", 20)
	require.NoError(t, err)

	var total int64
	require.NoError(t, pool.QueryRow(ctx,
		`SELECT total_xp FROM recognition.xp_totals WHERE subject_id='listener-1'`).Scan(&total))
	assert.Equal(t, int64(125), total, "5 per week cap")
	var badges int
	require.NoError(t, pool.QueryRow(ctx,
		`SELECT count(*) FROM recognition.badge_awards WHERE subject_id='listener-1'`).Scan(&badges))
	assert.Equal(t, 6, badges, "a badge per completed story even when XP is capped")

	// A source kill switch suppresses effects, not the event fact.
	_, err = e.SetKillSwitch(ctx, "super", "source:audio", true, "abuse spike", "")
	require.NoError(t, err)
	ev := recognition.Event{
		Source: "ke.audio.api", Type: "ke.audio.story.completed.v1",
		Subject: "listener-1", Time: week.Add(48 * time.Hour),
		Data: recognition.Payload{
			SourceType: "audio_story", SourceID: "story-z",
			OccurrenceKey: "audio.story:story-z", VerificationMethod: "server_recorded",
			VerificationResult: "server_recorded", PrivacyClass: "private",
		},
	}
	_, err = e.Record(ctx, ev)
	require.NoError(t, err)
	stats, err := e.ProcessDue(ctx, "w", 10)
	require.NoError(t, err)
	assert.Equal(t, 0, stats.Awards)
	var entries int
	require.NoError(t, pool.QueryRow(ctx,
		`SELECT count(*) FROM recognition.xp_entries WHERE subject_id='listener-1'`).Scan(&entries))
	assert.Equal(t, 5, entries)
}

func TestAdjustmentRequiresSecondApproverAndIsAudited(t *testing.T) {
	e, pool := setup(t)
	ctx := context.Background()

	_, err := e.CreateAdjustment(ctx, "admin-a", "admin-a", "reader-x", 20,
		"adjustment", "correcting a missed verified visit", nil, "")
	assert.ErrorIs(t, err, recognition.ErrSelfApproval)

	entry, err := e.CreateAdjustment(ctx, "admin-a", "admin-b", "reader-x", 20,
		"adjustment", "correcting a missed verified visit", nil, "")
	require.NoError(t, err)
	assert.Equal(t, 20, entry.Amount)
	var total int64
	require.NoError(t, pool.QueryRow(ctx,
		`SELECT total_xp FROM recognition.xp_totals WHERE subject_id='reader-x'`).Scan(&total))
	assert.Equal(t, int64(20), total)

	var audited int
	require.NoError(t, pool.QueryRow(ctx,
		`SELECT count(*) FROM recognition.audit WHERE action='xp.adjust' AND entity_id=$1`,
		entry.ID).Scan(&audited))
	assert.Equal(t, 1, audited)
}

func mustUUID(t *testing.T) string {
	t.Helper()
	id, err := uuid.NewV7()
	require.NoError(t, err)
	return id.String()
}
