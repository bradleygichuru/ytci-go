package alerts

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bradleygichuru/ytci-go/internal/db"
)

func TestStoreUpsertAlertAndFanout(t *testing.T) {
	pool := db.SetupTestDB(t)
	ctx := context.Background()
	_, err := pool.Exec(ctx, `INSERT INTO kenya_counties (gid, adm1_name, adm1_pcode, geom)
		VALUES (1, 'Nairobi', 'KE047', ST_GeomFromText('MULTIPOLYGON(((36.7 -1.2, 36.9 -1.2, 36.9 -1.4, 36.7 -1.4, 36.7 -1.2)))', 4326))`)
	require.NoError(t, err)

	store := NewStore(pool)
	mapper, err := NewMapper(ctx, store)
	require.NoError(t, err)
	mapping := mapper.index.MatchAreaDesc("Nairobi")
	require.Len(t, mapping.Counties, 1)

	n := NormalizedAlert{
		SourceID: "kmd", SourceIdentifier: "test-alert-1", MsgType: "Alert",
		Category: "Met", Categories: []string{"Met"}, Event: "Heavy rainfall",
		Severity: "Severe", Urgency: "Expected", Certainty: "Likely", Language: "en",
		Headline: "Heavy rainfall expected", Description: "Avoid flooded roads.",
		Instruction: "Move to higher ground.", SentAt: time.Now().UTC(),
		ContentHash: "hash-1", RawAreaDesc: "Nairobi", MappingMethod: MethodAreaDesc,
	}
	expires := time.Now().Add(6 * time.Hour).UTC()
	n.ExpiresAt = &expires

	result, err := store.UpsertAlert(ctx, n, mapping.Counties, true)
	require.NoError(t, err)
	assert.True(t, result.Created)
	assert.NotEmpty(t, result.ID)

	again, err := store.UpsertAlert(ctx, n, mapping.Counties, true)
	require.NoError(t, err)
	assert.False(t, again.Created)
	assert.False(t, again.Updated)

	updated := n
	updated.Instruction = "Move to higher ground immediately."
	updated.ContentHash = "hash-2"
	changed, err := store.UpsertAlert(ctx, updated, mapping.Counties, true)
	require.NoError(t, err)
	assert.True(t, changed.Updated)
	assert.True(t, changed.Material)

	// Seed a user, consent, preference, and token; the worker should create one home delivery.
	userID := "00000000-0000-0000-0000-000000000001"
	_, err = pool.Exec(ctx, `INSERT INTO users (id, name, email) VALUES ($1, 'Test', 'test@example.com')`, userID)
	require.NoError(t, err)
	_, err = pool.Exec(ctx, `INSERT INTO user_profiles (user_id, consent_notifications, created_by) VALUES ($1, true, $1)`, userID)
	require.NoError(t, err)
	_, err = pool.Exec(ctx, `INSERT INTO alert_preferences (user_id, home_county_gid) VALUES ($1, 1)`, userID)
	require.NoError(t, err)
	_, err = pool.Exec(ctx, `INSERT INTO push_tokens (user_id, token, platform) VALUES ($1::uuid, 'ExponentPushToken[test]', 'android')`, userID)
	require.NoError(t, err)

	worker := NewWorker(store, nil)
	worker.SetDryRun(true)
	require.NoError(t, worker.RunOnce(ctx))

	var count int
	require.NoError(t, pool.QueryRow(ctx, `SELECT count(*) FROM alert_deliveries WHERE alert_id=$1 AND reason='home'`, result.ID).Scan(&count))
	assert.Equal(t, 1, count)
}

func TestUpdatePushCooldownAndBypass(t *testing.T) {
	pool := db.SetupTestDB(t)
	ctx := context.Background()
	_, err := pool.Exec(ctx, `INSERT INTO kenya_counties (gid, adm1_name, adm1_pcode, geom)
		VALUES (1, 'Nairobi', 'KE047', ST_GeomFromText('MULTIPOLYGON(((36.7 -1.2, 36.9 -1.2, 36.9 -1.4, 36.7 -1.4, 36.7 -1.2)))', 4326))`)
	require.NoError(t, err)

	store := NewStore(pool)
	expires := time.Now().Add(6 * time.Hour).UTC()
	n := NormalizedAlert{
		SourceID: "kmd", SourceIdentifier: "cooldown-1", MsgType: "Alert",
		Category: "Met", Categories: []string{"Met"}, Event: "Heavy rainfall",
		Severity: "Severe", Urgency: "Expected", Certainty: "Likely", Language: "en",
		Headline: "Heavy rainfall expected", Instruction: "Move to higher ground.",
		SentAt: time.Now().UTC(), ExpiresAt: &expires, ContentHash: "cooldown-hash-1",
		RawAreaDesc: "Nairobi", MappingMethod: MethodAreaDesc,
	}
	created, err := store.UpsertAlert(ctx, n, []CountyMatch{{County: County{GID: 1, Name: "Nairobi"}, Method: MethodAreaDesc}}, true)
	require.NoError(t, err)

	var tokenID string
	require.NoError(t, pool.QueryRow(ctx, `INSERT INTO push_tokens (token, platform) VALUES ('ExponentPushToken[cooldown]', 'android') RETURNING id`).Scan(&tokenID))
	_, err = pool.Exec(ctx, `INSERT INTO alert_deliveries (alert_id, user_id, push_token_id, reason, status, sent_at)
		VALUES ($1, 'cooldown-user', $2, 'home', 'sent', now())`, created.ID, tokenID)
	require.NoError(t, err)

	jobCount := func() int {
		var c int
		require.NoError(t, pool.QueryRow(ctx, `SELECT count(*) FROM alert_fanout_jobs WHERE alert_id=$1`, created.ID).Scan(&c))
		return c
	}
	require.Equal(t, 1, jobCount())

	instruction := n
	instruction.Instruction = "Move to higher ground immediately."
	instruction.ContentHash = "cooldown-hash-2"
	update, err := store.UpsertAlert(ctx, instruction, []CountyMatch{{County: County{GID: 1, Name: "Nairobi"}, Method: MethodAreaDesc}}, true)
	require.NoError(t, err)
	require.True(t, update.Material)
	require.Equal(t, 1, jobCount(), "instruction-only update within six hours must not push")

	escalated := instruction
	escalated.Severity = "Extreme"
	escalated.ContentHash = "cooldown-hash-3"
	_, err = store.UpsertAlert(ctx, escalated, []CountyMatch{{County: County{GID: 1, Name: "Nairobi"}, Method: MethodAreaDesc}}, true)
	require.NoError(t, err)
	require.Equal(t, 2, jobCount(), "severity escalation must break the cooldown")

	cancelled := escalated
	cancelled.MsgType = "Cancel"
	cancelled.ContentHash = "cooldown-hash-4"
	_, err = store.UpsertAlert(ctx, cancelled, []CountyMatch{{County: County{GID: 1, Name: "Nairobi"}, Method: MethodAreaDesc}}, true)
	require.NoError(t, err)
	require.Equal(t, 3, jobCount(), "cancellation must break the cooldown")
}

func TestCancelByCAPReferencePreservesRecord(t *testing.T) {
	pool := db.SetupTestDB(t)
	ctx := context.Background()
	_, err := pool.Exec(ctx, `INSERT INTO kenya_counties (gid, adm1_name, adm1_pcode, geom)
		VALUES (1, 'Nairobi', 'KE047', ST_GeomFromText('MULTIPOLYGON(((36.7 -1.2, 36.9 -1.2, 36.9 -1.4, 36.7 -1.4, 36.7 -1.2)))', 4326))`)
	require.NoError(t, err)

	store := NewStore(pool)
	expires := time.Now().Add(6 * time.Hour).UTC()
	original := NormalizedAlert{
		SourceID: "kmd", SourceIdentifier: "original-1", MsgType: "Alert",
		Category: "Met", Categories: []string{"Met"}, Event: "Heavy rainfall",
		Severity: "Severe", Urgency: "Expected", Certainty: "Likely", Language: "en",
		Headline: "Heavy rainfall expected", Instruction: "Move to higher ground.",
		SentAt: time.Now().UTC(), ExpiresAt: &expires, ContentHash: "original-hash",
		RawAreaDesc: "Nairobi", MappingMethod: MethodAreaDesc,
	}
	created, err := store.UpsertAlert(ctx, original, []CountyMatch{{County: County{GID: 1, Name: "Nairobi"}, Method: MethodAreaDesc}}, true)
	require.NoError(t, err)

	cancel, _, ok := normalizeKMD(&CAPAlert{
		Identifier: "cancel-2", Sender: "director@meteo.go.ke", Sent: time.Now().UTC(),
		Status: "Actual", MsgType: "Cancel", Scope: "Public",
		References: "director@meteo.go.ke,original-1," + time.Now().UTC().Format(time.RFC3339),
	})
	require.True(t, ok)
	result, err := store.UpsertAlert(ctx, cancel, nil, true)
	require.NoError(t, err)
	assert.Equal(t, created.ID, result.ID, "the cancel must update the original alert, not create a second row")
	assert.Equal(t, "cancelled", result.Status)

	var count int
	require.NoError(t, pool.QueryRow(ctx, `SELECT count(*) FROM alerts`).Scan(&count))
	assert.Equal(t, 1, count)

	var status, headline, severity string
	require.NoError(t, pool.QueryRow(ctx, `SELECT status, headline, severity FROM alerts WHERE id=$1`, created.ID).Scan(&status, &headline, &severity))
	assert.Equal(t, "cancelled", status)
	assert.Equal(t, "Heavy rainfall expected", headline, "the tombstone keeps the original content")
	assert.Equal(t, "Severe", severity)

	require.NoError(t, pool.QueryRow(ctx, `SELECT count(*) FROM alert_areas WHERE alert_id=$1`, created.ID).Scan(&count))
	assert.Equal(t, 1, count, "areas survive the cancellation")

	require.NoError(t, pool.QueryRow(ctx, `SELECT count(*) FROM alert_fanout_jobs WHERE alert_id=$1 AND reason='cancel'`, created.ID).Scan(&count))
	assert.Equal(t, 1, count)

	var linked string
	require.NoError(t, pool.QueryRow(ctx, `SELECT alert_id FROM alert_source_messages WHERE source_identifier='cancel-2'`).Scan(&linked))
	assert.Equal(t, created.ID, linked)
}
