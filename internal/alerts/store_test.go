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
