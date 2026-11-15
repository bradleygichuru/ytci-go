package alerts

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bradleygichuru/ytci-go/internal/db"
)

func TestMatchAlertRequiresAnAnchor(t *testing.T) {
	alert := alertRow{ID: "a1"}
	national := []int32(nil)

	reason, _ := matchAlert(alert, national, nil, nil, nil, true)
	assert.Empty(t, reason, "national alerts must not broadcast to anchorless users")

	home := int32(1)
	reason, gid := matchAlert(alert, national, &home, nil, nil, true)
	assert.Equal(t, "national", reason)
	assert.Nil(t, gid)

	reason, _ = matchAlert(alert, []int32{2, 3}, &home, []int32{3}, nil, true)
	assert.Equal(t, "watch", reason, "watch wins over home when home is unaffected")

	location := int32(2)
	reason, gid = matchAlert(alert, []int32{1, 2}, &home, []int32{3}, &location, true)
	assert.Equal(t, "home", reason, "reason priority is home, then watch, then location")
	assert.Equal(t, int32(1), *gid)
}

func TestCapExceededCountsAlertsNotTokens(t *testing.T) {
	pool := db.SetupTestDB(t)
	ctx := context.Background()
	_, err := pool.Exec(ctx, `INSERT INTO push_tokens (token, platform) VALUES
		('ExponentPushToken[caps-1]', 'android'), ('ExponentPushToken[caps-2]', 'android')`)
	require.NoError(t, err)

	insertAlert := func(identifier, severity string) string {
		var id string
		require.NoError(t, pool.QueryRow(ctx, `
			INSERT INTO alerts (source_id, source_identifier, event, severity, urgency, certainty, sent_at, content_hash)
			VALUES ('kmd',$1,'Rain',$2,'Expected','Likely',now(),$3) RETURNING id`,
			identifier, severity, "hash-"+identifier).Scan(&id))
		return id
	}
	insertDelivery := func(alertID, token string, sentAt time.Time) {
		_, err := pool.Exec(ctx, `
			INSERT INTO alert_deliveries (alert_id, user_id, push_token_id, reason, status, sent_at, created_at)
			VALUES ($1,'cap-user',(SELECT id FROM push_tokens WHERE token=$2),'home','sent',$3,$3)`,
			alertID, token, sentAt)
		require.NoError(t, err)
	}

	now := time.Now().UTC()
	ids := make([]string, 0, 4)
	for i := 0; i < 4; i++ {
		id := insertAlert(string(rune('a'+i)), "Severe")
		ids = append(ids, id)
		insertDelivery(id, "ExponentPushToken[caps-1]", now)
		insertDelivery(id, "ExponentPushToken[caps-2]", now) // a second device must not consume a second cap slot
	}

	worker := NewWorker(NewStore(pool), nil)
	assert.True(t, worker.capExceeded(ctx, "cap-user", "Severe"))
	assert.False(t, worker.capExceeded(ctx, "cap-user", "Extreme"), "Extreme bypasses caps")

	_, err = pool.Exec(ctx, `DELETE FROM alert_deliveries WHERE alert_id=$1`, ids[0])
	require.NoError(t, err)
	_, err = pool.Exec(ctx, `UPDATE alert_deliveries SET created_at=now()-interval '2 hours'`)
	require.NoError(t, err)
	assert.False(t, worker.capExceeded(ctx, "cap-user", "Severe"), "three distinct alerts stay under the daily cap")

	_, err = pool.Exec(ctx, `UPDATE alert_deliveries SET created_at=now() WHERE alert_id = ANY($1)`, ids[1:3])
	require.NoError(t, err)
	assert.True(t, worker.capExceeded(ctx, "cap-user", "Severe"), "two alerts in an hour hit the hourly cap")
}
