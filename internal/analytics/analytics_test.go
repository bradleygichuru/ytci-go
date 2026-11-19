package analytics_test

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bradleygichuru/ytci-go/internal/analytics"
	"github.com/bradleygichuru/ytci-go/internal/db"
)

func setup(t *testing.T) (*pgxpool.Pool, context.Context) {
	t.Helper()
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}
	pool := db.SetupTestDB(t)
	t.Cleanup(pool.Close)
	return pool, context.Background()
}

func newUser(t *testing.T, ctx context.Context, pool *pgxpool.Pool, id string) {
	t.Helper()
	_, err := pool.Exec(ctx, `
		INSERT INTO users (id, name, email, created_at, updated_at, role)
		VALUES ($1, 'Test', $2, now(), now(), 'user')`, id, id+"@example.test")
	require.NoError(t, err)
}

func newIdentity(t *testing.T, ctx context.Context, pool *pgxpool.Pool, actor, userID string) (string, string) {
	t.Helper()
	installID := uuid.NewString()
	id, err := analytics.NewStore(pool).UpsertIdentity(ctx, analytics.InstallRequest{
		InstallID:  installID,
		Platform:   "android",
		AppVersion: "1.2.3",
	}, actor, userID)
	require.NoError(t, err)
	return installID, id.AnalyticsID
}

func grantConsent(t *testing.T, ctx context.Context, pool *pgxpool.Pool, analyticsID, installID, version string) {
	t.Helper()
	_, err := analytics.NewStore(pool).SetConsent(ctx, analytics.ConsentWrite{
		AnalyticsID: analyticsID,
		InstallID:   installID,
		Version:     version,
		State:       analytics.ConsentGranted,
		Source:      "guest_device",
	}, "")
	require.NoError(t, err)
}

func screenEvent(sessionID string, seq int, route string, props map[string]any) analytics.Event {
	return analytics.Event{
		EventID:    uuid.NewString(),
		SessionID:  sessionID,
		SessionSeq: seq,
		Family:     analytics.FamilyScreenView,
		Name:       route,
		ClientTS:   time.Now().UTC().Format(time.RFC3339),
		Properties: props,
	}
}

func journeyEvent(sessionID string, seq int, props map[string]any) analytics.Event {
	return analytics.Event{
		EventID:    uuid.NewString(),
		SessionID:  sessionID,
		SessionSeq: seq,
		Family:     analytics.FamilyJourneyStep,
		Name:       "onboarding_step",
		ClientTS:   time.Now().UTC().Format(time.RFC3339),
		Properties: props,
	}
}

func makeBatch(analyticsID, installID, sessionID, consentVersion string, events []analytics.Event) analytics.Batch {
	return analytics.Batch{
		AnalyticsID:    analyticsID,
		InstallID:      installID,
		ConsentVersion: consentVersion,
		AppVersion:     "1.2.3",
		Platform:       "android",
		ActorType:      analytics.ActorGuest,
		SessionID:      sessionID,
		Events:         events,
	}
}

func TestConsentGatesIngest(t *testing.T) {
	pool, ctx := setup(t)
	store := analytics.NewStore(pool)
	installID, analyticsID := newIdentity(t, ctx, pool, analytics.ActorGuest, "")
	sessionID := uuid.NewString()
	version := analytics.ConsentVersion()

	_, err := store.IngestBatch(ctx, "", &analytics.Batch{
		AnalyticsID: analyticsID, InstallID: installID, ConsentVersion: version,
		AppVersion: "1", Platform: "android", ActorType: analytics.ActorGuest,
		SessionID: sessionID,
		Events:    []analytics.Event{screenEvent(sessionID, 1, "/feed", nil)},
	}, time.Now().UTC())
	var rej *analytics.RejectError
	require.ErrorAs(t, err, &rej)
	assert.Equal(t, http.StatusForbidden, rej.Status)
	assert.Equal(t, "CONSENT_REQUIRED", rej.Code)

	grantConsent(t, ctx, pool, analyticsID, installID, version)
	// A stale version is rejected even with a grant on file.
	_, err = store.IngestBatch(ctx, "", &analytics.Batch{
		AnalyticsID: analyticsID, InstallID: installID, ConsentVersion: "2020-01-01",
		AppVersion: "1", Platform: "android", ActorType: analytics.ActorGuest,
		SessionID: sessionID,
		Events:    []analytics.Event{screenEvent(sessionID, 1, "/feed", nil)},
	}, time.Now().UTC())
	require.ErrorAs(t, err, &rej)
	assert.Equal(t, "CONSENT_STALE", rej.Code)

	batch := makeBatch(analyticsID, installID, sessionID, version,
		[]analytics.Event{screenEvent(sessionID, 1, "/feed", nil)})
	ack, err := store.IngestBatch(ctx, "", &batch, time.Now().UTC())
	require.NoError(t, err)
	assert.Equal(t, 1, ack.Accepted)

	_, err = store.WithdrawConsent(ctx, analyticsID, installID, version, "")
	require.NoError(t, err)
	batch2 := makeBatch(analyticsID, installID, sessionID, version,
		[]analytics.Event{screenEvent(sessionID, 2, "/feed", nil)})
	_, err = store.IngestBatch(ctx, "", &batch2, time.Now().UTC())
	require.ErrorAs(t, err, &rej)
	assert.Equal(t, "CONSENT_WITHDRAWN", rej.Code)
}

func TestBatchIdempotencyAndOfflineQueue(t *testing.T) {
	pool, ctx := setup(t)
	store := analytics.NewStore(pool)
	installID, analyticsID := newIdentity(t, ctx, pool, analytics.ActorGuest, "")
	version := analytics.ConsentVersion()
	grantConsent(t, ctx, pool, analyticsID, installID, version)
	sessionID := uuid.NewString()

	seenBatchIDs := make([]string, 0, 5)
	var firstEvents []analytics.Event
	for batchIndex := 0; batchIndex < 5; batchIndex++ {
		events := make([]analytics.Event, 0, 100)
		for i := 0; i < 100; i++ {
			events = append(events, screenEvent(sessionID, batchIndex*100+i+1, "/destinations/[id]", nil))
		}
		if batchIndex == 0 {
			firstEvents = events
		}
		batch := makeBatch(analyticsID, installID, sessionID, version, events)
		ack, err := store.IngestBatch(ctx, "", &batch, time.Now().UTC())
		require.NoError(t, err)
		assert.Equal(t, 100, ack.Accepted)
		seenBatchIDs = append(seenBatchIDs, ack.BatchID)
	}

	var count int
	require.NoError(t, pool.QueryRow(ctx,
		`SELECT count(*) FROM analytics_events WHERE analytics_id = $1`, analyticsID).Scan(&count))
	assert.Equal(t, 500, count)

	// Retrying one flushed batch is idempotent and returns the original ack.
	resend := makeBatch(analyticsID, installID, sessionID, version, firstEvents)
	ack, err := store.IngestBatch(ctx, "", &resend, time.Now().UTC())
	require.NoError(t, err)
	assert.Contains(t, seenBatchIDs, ack.BatchID)
	assert.Equal(t, 100, ack.Accepted)

	require.NoError(t, pool.QueryRow(ctx,
		`SELECT count(*) FROM analytics_events WHERE analytics_id = $1`, analyticsID).Scan(&count))
	assert.Equal(t, 500, count)
}

func TestPropertyAllowListAndRoutePrivacy(t *testing.T) {
	pool, ctx := setup(t)
	store := analytics.NewStore(pool)
	installID, analyticsID := newIdentity(t, ctx, pool, analytics.ActorGuest, "")
	version := analytics.ConsentVersion()
	grantConsent(t, ctx, pool, analyticsID, installID, version)
	sessionID := uuid.NewString()

	good := screenEvent(sessionID, 1, "/events/[id]", map[string]any{
		"route":    "/events/[id]",
		"email":    "someone@example.test",
		"free":     "raw text",
		"surprise": 1,
	})
	badRoute := screenEvent(sessionID, 2, "/events/123", nil)
	badJourney := journeyEvent(sessionID, 3, map[string]any{"journey": "onboarding", "step": "welcome"})

	batch := makeBatch(analyticsID, installID, sessionID, version,
		[]analytics.Event{good, badRoute, badJourney})
	ack, err := store.IngestBatch(ctx, "", &batch, time.Now().UTC())
	require.NoError(t, err)
	assert.Equal(t, 1, ack.Accepted)
	assert.Equal(t, 2, ack.Dropped)
	assert.Equal(t, 3, ack.UnknownKeys)
	assert.ElementsMatch(t, []string{badRoute.EventID, badJourney.EventID}, ack.RejectedEventIDs)

	var props []byte
	require.NoError(t, pool.QueryRow(ctx,
		`SELECT properties FROM analytics_events WHERE client_event_id = $1`, good.EventID).Scan(&props))
	var stored map[string]any
	require.NoError(t, json.Unmarshal(props, &stored))
	assert.Equal(t, map[string]any{"route": "/events/[id]"}, stored)
}

func TestUnder18IsGated(t *testing.T) {
	// The gate is decided before persistence, so this test needs no database.
	h := analytics.NewHandler(nil)
	body := map[string]any{
		"installId": uuid.NewString(),
		"platform":  "ios",
	}
	raw, _ := json.Marshal(body)
	req := httptest.NewRequest(http.MethodPost, "/v1/mobile/analytics/installs", bytes.NewReader(raw))
	req.Header.Set("X-Age-Declaration", "under_18")
	rec := httptest.NewRecorder()
	h.RegisterInstall(rec, req)
	assert.Equal(t, http.StatusUnavailableForLegalReasons, rec.Code)
	assert.Contains(t, rec.Body.String(), "UNDER_18_IDENTIFIED_DISABLED")

	sessionID := uuid.NewString()
	batch := makeBatch(uuid.NewString(), uuid.NewString(), sessionID, analytics.ConsentVersion(),
		[]analytics.Event{screenEvent(sessionID, 1, "/feed", nil)})
	batch.ActorType = analytics.ActorUnder18
	raw, _ = json.Marshal(batch)
	req = httptest.NewRequest(http.MethodPost, "/v1/mobile/analytics/batches", bytes.NewReader(raw))
	rec = httptest.NewRecorder()
	h.IngestBatch(rec, req)
	assert.Equal(t, http.StatusForbidden, rec.Code)
	assert.Contains(t, rec.Body.String(), "UNDER_18_IDENTIFIED_DISABLED")
}

func TestErasureTombstonesIdentity(t *testing.T) {
	pool, ctx := setup(t)
	store := analytics.NewStore(pool)
	newUser(t, ctx, pool, "reader-erase")
	installID, analyticsID := newIdentity(t, ctx, pool, analytics.ActorAccount, "reader-erase")
	version := analytics.ConsentVersion()
	grantConsent(t, ctx, pool, analyticsID, installID, version)
	sessionID := uuid.NewString()
	batch := makeBatch(analyticsID, installID, sessionID, version,
		[]analytics.Event{screenEvent(sessionID, 1, "/feed", nil)})
	_, err := store.IngestBatch(ctx, "reader-erase", &batch, time.Now().UTC())
	require.NoError(t, err)

	req, err := store.RequestErasure(ctx, "user", "reader-erase")
	require.NoError(t, err)
	assert.Equal(t, "completed", req.Status)

	var events, sessions, mappings int
	require.NoError(t, pool.QueryRow(ctx,
		`SELECT count(*) FROM analytics_events WHERE analytics_id = $1`, analyticsID).Scan(&events))
	require.NoError(t, pool.QueryRow(ctx,
		`SELECT count(*) FROM analytics_sessions WHERE analytics_id = $1`, analyticsID).Scan(&sessions))
	require.NoError(t, pool.QueryRow(ctx,
		`SELECT count(*) FROM analytics_id_map WHERE user_id = 'reader-erase'`).Scan(&mappings))
	assert.Zero(t, events)
	assert.Zero(t, sessions)
	assert.Zero(t, mappings)

	var status string
	require.NoError(t, pool.QueryRow(ctx,
		`SELECT status FROM analytics_erasure_requests WHERE id = $1`, req.ID).Scan(&status))
	assert.Equal(t, "completed", status)
}

func TestRollupsAndSuppression(t *testing.T) {
	pool, ctx := setup(t)
	store := analytics.NewStore(pool)
	installID, analyticsID := newIdentity(t, ctx, pool, analytics.ActorGuest, "")
	version := analytics.ConsentVersion()
	grantConsent(t, ctx, pool, analyticsID, installID, version)
	sessionID := uuid.NewString()
	batch := makeBatch(analyticsID, installID, sessionID, version, []analytics.Event{
		screenEvent(sessionID, 1, "/events/[id]", nil),
		journeyEvent(sessionID, 2, map[string]any{
			"journey": "event_registration", "step": "detail_viewed",
			"step_index": 0, "outcome": "started",
		}),
	})
	_, err := store.IngestBatch(ctx, "", &batch, time.Now().UTC())
	require.NoError(t, err)

	today := time.Now().UTC()
	require.NoError(t, analytics.RunRollupForDay(ctx, pool, today))

	var value float64
	var suppressed bool
	require.NoError(t, pool.QueryRow(ctx, `
		SELECT value, suppressed FROM analytics_daily
		WHERE day = $1::date AND metric = 'dau' AND dimension_key = ''`, today).Scan(&value, &suppressed))
	assert.EqualValues(t, 1, value)
	assert.True(t, suppressed, "a cell below ten actors must be suppressed")

	var reached int
	require.NoError(t, pool.QueryRow(ctx, `
		SELECT reached FROM analytics_journey_daily
		WHERE day = $1::date AND journey = 'event_registration'`, today).Scan(&reached))
	assert.Equal(t, 1, reached)

	overview, err := store.Overview(ctx, today, today)
	require.NoError(t, err)
	dau := findCell(t, overview, "dau")
	assert.True(t, dau.Suppressed)
	assert.Nil(t, dau.Value)

	// A healthy cell survives; a small one is nulled.
	_, err = pool.Exec(ctx, `
		INSERT INTO analytics_daily (day, metric, dimension_key, value, suppressed, sample_size)
		VALUES ($1::date, 'mau', '', 42, false, 42),
		       ($1::date, 'new_accounts', '', 4, true, 4)
		ON CONFLICT (day, metric, dimension_key) DO UPDATE
		SET value = EXCLUDED.value, suppressed = EXCLUDED.suppressed, sample_size = EXCLUDED.sample_size`,
		today)
	require.NoError(t, err)
	overview, err = store.Overview(ctx, today, today)
	require.NoError(t, err)
	mau := findCell(t, overview, "mau")
	require.NotNil(t, mau.Value)
	assert.EqualValues(t, 42, *mau.Value)
	assert.False(t, mau.Suppressed)
	n := findCell(t, overview, "new_accounts")
	assert.True(t, n.Suppressed)
	assert.Nil(t, n.Value)
}

func findCell(t *testing.T, overview analytics.Overview, metric string) analytics.MetricCell {
	t.Helper()
	for _, cell := range overview.Cells {
		if cell.Metric == metric {
			return cell
		}
	}
	t.Fatalf("metric %s not found", metric)
	return analytics.MetricCell{}
}

func TestRetentionDeletesExpiredRows(t *testing.T) {
	pool, ctx := setup(t)
	analyticsID := uuid.NewString()
	_, err := pool.Exec(ctx, `
		INSERT INTO analytics_events (event, analytics_id, received_at)
		VALUES ('screen_view', $1, now() - interval '15 months')`, analyticsID)
	require.NoError(t, err)
	_, err = pool.Exec(ctx, `
		INSERT INTO analytics_daily (day, metric, dimension_key, value, suppressed, sample_size)
		VALUES (CURRENT_DATE - INTERVAL '25 months', 'dau', '', 1, true, 1)`)
	require.NoError(t, err)

	result, err := analytics.RunRetention(ctx, pool, time.Now().UTC())
	require.NoError(t, err)
	assert.GreaterOrEqual(t, result.Events, int64(1))
	assert.GreaterOrEqual(t, result.Aggregates, int64(1))

	var events, daily int
	require.NoError(t, pool.QueryRow(ctx,
		`SELECT count(*) FROM analytics_events WHERE analytics_id = $1`, analyticsID).Scan(&events))
	require.NoError(t, pool.QueryRow(ctx,
		`SELECT count(*) FROM analytics_daily WHERE day < CURRENT_DATE - INTERVAL '24 months'`).Scan(&daily))
	assert.Zero(t, events)
	assert.Zero(t, daily)
}

func TestConsentWritesAreBoundToTheCaller(t *testing.T) {
	pool, ctx := setup(t)
	store := analytics.NewStore(pool)
	version := analytics.ConsentVersion()

	installA, analyticsA := newIdentity(t, ctx, pool, analytics.ActorGuest, "")
	installB, _ := newIdentity(t, ctx, pool, analytics.ActorGuest, "")
	newUser(t, ctx, pool, "acct-1")
	_, accountAnalytics := newIdentity(t, ctx, pool, analytics.ActorAccount, "acct-1")

	var rej *analytics.RejectError

	// A guest cannot write consent for an analytics_id using another install.
	_, err := store.SetConsent(ctx, analytics.ConsentWrite{
		AnalyticsID: analyticsA, InstallID: installB, Version: version,
		State: analytics.ConsentGranted, Source: "guest_device",
	}, "")
	require.ErrorAs(t, err, &rej)
	assert.Equal(t, http.StatusForbidden, rej.Status)
	assert.Equal(t, "ANALYTICS_ID_UNKNOWN", rej.Code)

	// A guest write without an install cannot be bound at all.
	_, err = store.SetConsent(ctx, analytics.ConsentWrite{
		AnalyticsID: analyticsA, Version: version,
		State: analytics.ConsentGranted, Source: "guest_device",
	}, "")
	require.ErrorAs(t, err, &rej)
	assert.Equal(t, http.StatusBadRequest, rej.Status)
	assert.Equal(t, "INSTALL_ID_REQUIRED", rej.Code)

	// An account cannot write consent for an id it does not own.
	_, err = store.SetConsent(ctx, analytics.ConsentWrite{
		AnalyticsID: analyticsA, Version: version,
		State: analytics.ConsentGranted, Source: "account",
	}, "acct-1")
	require.ErrorAs(t, err, &rej)
	assert.Equal(t, http.StatusForbidden, rej.Status)
	assert.Equal(t, "ANALYTICS_ID_UNKNOWN", rej.Code)

	// The real bindings work.
	_, err = store.SetConsent(ctx, analytics.ConsentWrite{
		AnalyticsID: analyticsA, InstallID: installA, Version: version,
		State: analytics.ConsentGranted, Source: "guest_device",
	}, "")
	require.NoError(t, err)
	_, err = store.SetConsent(ctx, analytics.ConsentWrite{
		AnalyticsID: accountAnalytics, Version: version,
		State: analytics.ConsentGranted, Source: "account",
	}, "acct-1")
	require.NoError(t, err)

	// Withdrawal is bound by the same rule.
	_, err = store.WithdrawConsent(ctx, analyticsA, installB, version, "")
	require.ErrorAs(t, err, &rej)
	assert.Equal(t, http.StatusForbidden, rej.Status)
	assert.Equal(t, "ANALYTICS_ID_UNKNOWN", rej.Code)
	_, err = store.WithdrawConsent(ctx, analyticsA, installA, version, "")
	require.NoError(t, err)
}
