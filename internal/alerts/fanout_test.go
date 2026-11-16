package alerts

import (
	"context"
	"strconv"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bradleygichuru/ytci-go/internal/db"
	"github.com/bradleygichuru/ytci-go/internal/push"
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
	assert.Equal(t, "location", reason, "reason priority is location, then watch, then home")
	assert.Equal(t, int32(2), *gid)
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

type captureSender struct {
	messages []push.ExpoPushMessage
}

func (c *captureSender) SendMessages(ctx context.Context, messages []push.ExpoPushMessage) (*push.SendResult, error) {
	c.messages = append(c.messages, messages...)
	result := &push.SendResult{
		TokenErrors:    map[string]string{},
		TicketForToken: map[string]string{},
		TicketForIndex: map[int]string{},
	}
	for i, message := range messages {
		result.Sent++
		ticket := "ticket-" + strconv.Itoa(i)
		result.TicketForToken[message.To] = ticket
		result.TicketForIndex[i] = ticket
	}
	return result, nil
}

func (c *captureSender) GetReceiptMap(ctx context.Context, ticketIDs []string) (map[string]push.Receipt, error) {
	return map[string]push.Receipt{}, nil
}

func seedQueuedDelivery(t *testing.T, pool *pgxpool.Pool, expiresAt time.Time) (alertID, deliveryID string) {
	t.Helper()
	ctx := context.Background()
	err := pool.QueryRow(ctx, `
		INSERT INTO alerts (source_id, source_identifier, event, severity, urgency, certainty,
		  headline, description, instruction, sent_at, expires_at, content_hash)
		VALUES ('kmd',$1,'Heavy rainfall','Severe','Expected','Likely','Heavy rainfall expected',
		  'Avoid flooded roads.','Move to higher ground.',now(),$2,$3) RETURNING id`,
		"payload-"+expiresAt.Format("20060102T150405"), expiresAt, "hash-"+expiresAt.Format("20060102T150405")).Scan(&alertID)
	require.NoError(t, err)

	var tokenID string
	require.NoError(t, pool.QueryRow(ctx, `
		INSERT INTO push_tokens (token, platform) VALUES ($1,'android') RETURNING id`,
		"ExponentPushToken[payload-"+expiresAt.Format("20060102T150405")+"]").Scan(&tokenID))
	require.NoError(t, pool.QueryRow(ctx, `
		INSERT INTO alert_deliveries (alert_id, user_id, push_token_id, county_gid, reason, status, deliver_after)
		VALUES ($1,'payload-user',$2,1,'home','queued',now()) RETURNING id`, alertID, tokenID).Scan(&deliveryID))
	return alertID, deliveryID
}

func TestSendDuePayloadContractAndTTL(t *testing.T) {
	pool := db.SetupTestDB(t)
	ctx := context.Background()
	alertID, deliveryID := seedQueuedDelivery(t, pool, time.Now().Add(90*time.Minute))

	sender := &captureSender{}
	worker := NewWorker(NewStore(pool), sender)
	require.NoError(t, worker.sendDue(ctx))

	require.Len(t, sender.messages, 1)
	message := sender.messages[0]
	data, ok := message.Data.(map[string]any)
	require.True(t, ok)
	assert.Equal(t, alertID, data["alertId"])
	assert.Equal(t, "Severe", data["severity"])
	assert.Equal(t, "kmd", data["sourceId"])
	assert.Equal(t, int32(1), data["countyGid"])
	assert.Equal(t, "safety_alerts", message.ChannelID)
	assert.Equal(t, "high", message.Priority)
	assert.Equal(t, "time-sensitive", message.InterruptionLevel)
	assert.Greater(t, message.TTL, 0)
	assert.LessOrEqual(t, message.TTL, 90*60, "TTL must not outlive expires")

	var status, ticket string
	require.NoError(t, pool.QueryRow(ctx, `SELECT status, expo_ticket_id FROM alert_deliveries WHERE id=$1`, deliveryID).Scan(&status, &ticket))
	assert.Equal(t, "sent", status)
	assert.NotEmpty(t, ticket)
}

func TestSendDueSkipsExpiredAlerts(t *testing.T) {
	pool := db.SetupTestDB(t)
	ctx := context.Background()
	_, deliveryID := seedQueuedDelivery(t, pool, time.Now().Add(-time.Minute))

	sender := &captureSender{}
	worker := NewWorker(NewStore(pool), sender)
	require.NoError(t, worker.sendDue(ctx))

	assert.Empty(t, sender.messages)
	var status string
	require.NoError(t, pool.QueryRow(ctx, `SELECT status FROM alert_deliveries WHERE id=$1`, deliveryID).Scan(&status))
	assert.Equal(t, "skipped_expired", status)
}
