package push

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bradleygichuru/ytci-go/internal/db"
)

func TestParseTargetAudience(t *testing.T) {
	audience := ParseTargetAudience(`{"countyGids":[1,2],"language":"sw","important":true,"interests":["wildlife"],"guests":true}`)
	assert.Equal(t, []int{1, 2}, audience.CountyGIDs)
	assert.Equal(t, "sw", audience.Language)
	assert.True(t, audience.Important)
	assert.Equal(t, []string{"wildlife"}, audience.Interests)
	assert.True(t, audience.Guests)

	assert.Equal(t, TargetAudience{}, ParseTargetAudience(""))
	assert.Equal(t, TargetAudience{}, ParseTargetAudience("not json"))
}

func TestQuietHoursAllowNeverBypasses(t *testing.T) {
	eat := time.FixedZone("EAT", 3*3600)
	target := ProgramTarget{
		Timezone: "Africa/Nairobi", QuietHoursEnabled: true,
		QuietHoursStart: "22:00", QuietHoursEnd: "06:00",
	}
	assert.False(t, quietHoursAllow(target, time.Date(2026, 11, 16, 23, 0, 0, 0, eat)))
	assert.True(t, quietHoursAllow(target, time.Date(2026, 11, 16, 12, 0, 0, 0, eat)))
	target.QuietHoursEnabled = false
	assert.True(t, quietHoursAllow(target, time.Date(2026, 11, 16, 23, 0, 0, 0, eat)))
}

func TestResolveProgramTargetsHonoursConsentCategoryAndCounty(t *testing.T) {
	pool := db.SetupTestDB(t)
	ctx := context.Background()
	_, err := pool.Exec(ctx, `INSERT INTO kenya_counties (gid, adm1_name, adm1_pcode) VALUES
		(1, 'Nairobi', 'KE047'), (2, 'Mombasa', 'KE001')`)
	require.NoError(t, err)

	insertUser := func(id, name string, county int, program bool, importantOnly bool, preferences string) {
		_, err := pool.Exec(ctx, `INSERT INTO users (id, name, email) VALUES ($1,$2,$3)`, id, name, name+"@example.com")
		require.NoError(t, err)
		_, err = pool.Exec(ctx, `INSERT INTO user_profiles (user_id, consent_notifications, created_by, preferences)
			VALUES ($1,true,$1,$2)`, id, preferences)
		require.NoError(t, err)
		categories := []string{"Met"}
		if program {
			categories = append(categories, "Program")
		}
		_, err = pool.Exec(ctx, `INSERT INTO alert_preferences
			(user_id, home_county_gid, categories, os_permission_ok, program_important_only)
			VALUES ($1,$2,$3,true,$4)`, id, county, categories, importantOnly)
		require.NoError(t, err)
		_, err = pool.Exec(ctx, `INSERT INTO push_tokens (user_id, token, platform) VALUES ($1,$2,'android')`, id, "token-"+id)
		require.NoError(t, err)
	}

	insertUser("00000000-0000-0000-0000-000000000011", "Nairobi Important", 1, true, true, `["wildlife"]`)
	insertUser("00000000-0000-0000-0000-000000000012", "Mombasa Normal", 2, true, false, `["beaches"]`)
	insertUser("00000000-0000-0000-0000-000000000013", "Program Off", 1, false, false, `["wildlife"]`)
	insertUser("00000000-0000-0000-0000-000000000014", "Nairobi Normal", 1, true, false, `["wildlife"]`)
	insertUser("00000000-0000-0000-0000-000000000015", "Security Only", 2, true, false, `["security"]`)
	_, err = pool.Exec(ctx, `UPDATE alert_preferences SET categories=ARRAY['Program','Security']
		WHERE user_id='00000000-0000-0000-0000-000000000015'`)
	require.NoError(t, err)

	_, err = pool.Exec(ctx, `INSERT INTO push_tokens (user_id, token, platform) VALUES (NULL,'guest-token','android')`)
	require.NoError(t, err)

	targets, err := ResolveProgramTargets(ctx, pool, TargetAudience{CountyGIDs: []int{1}})
	require.NoError(t, err)
	require.Len(t, targets, 1)
	assert.Equal(t, "token-00000000-0000-0000-0000-000000000014", targets[0].Token)

	targets, err = ResolveProgramTargets(ctx, pool, TargetAudience{Important: true})
	require.NoError(t, err)
	assert.Len(t, targets, 4)

	targets, err = ResolveProgramTargets(ctx, pool, TargetAudience{Interests: []string{"beaches"}, Important: true})
	require.NoError(t, err)
	require.Len(t, targets, 1)
	assert.Equal(t, "token-00000000-0000-0000-0000-000000000012", targets[0].Token)

	targets, err = ResolveProgramTargets(ctx, pool, TargetAudience{Important: true, Categories: []string{"Met"}})
	require.NoError(t, err)
	assert.Len(t, targets, 3, "category targeting excludes users without a matching preference")

	targets, err = ResolveProgramTargets(ctx, pool, TargetAudience{Guests: true})
	require.NoError(t, err)
	require.Len(t, targets, 4)
	tokens := make([]string, 0, len(targets))
	for _, target := range targets {
		tokens = append(tokens, target.Token)
	}
	assert.Contains(t, tokens, "guest-token")
}

func TestUpsertCampaignProgramUpdateIsIdempotent(t *testing.T) {
	pool := db.SetupTestDB(t)
	ctx := context.Background()
	userID := "00000000-0000-0000-0000-000000000021"
	_, err := pool.Exec(ctx, `INSERT INTO users (id, name, email) VALUES ($1,'Admin','admin@example.com')`, userID)
	require.NoError(t, err)

	first, err := UpsertCampaignProgramUpdate(
		ctx, pool, "00000000-0000-0000-0000-0000000000aa",
		"Coastal cleanup", "Moved to Saturday", "", `{"route":"/campaigns/abc"}`,
		`{"countyGids":[1],"important":true}`, userID,
	)
	require.NoError(t, err)
	require.NotEmpty(t, first.ID)
	assert.Equal(t, "Important", first.Priority)
	assert.Equal(t, "/campaigns/abc", first.Route)
	assert.Equal(t, []int{1}, first.Audience.CountyGIDs)
	assert.True(t, first.Audience.Important)

	second, err := UpsertCampaignProgramUpdate(
		ctx, pool, "00000000-0000-0000-0000-0000000000aa",
		"Coastal cleanup", "Moved to Sunday", "", `{"route":"/campaigns/abc"}`,
		`{"countyGids":[1],"important":true}`, userID,
	)
	require.NoError(t, err)
	assert.Equal(t, first.ID, second.ID)

	var count int
	require.NoError(t, pool.QueryRow(ctx, `SELECT count(*) FROM program_updates WHERE source='campaign'`).Scan(&count))
	assert.Equal(t, 1, count)
}

func TestProgramCapCountsNonExtremeAlertsAndPrograms(t *testing.T) {
	pool := db.SetupTestDB(t)
	ctx := context.Background()
	for i := 0; i < 4; i++ {
		var alertID string
		require.NoError(t, pool.QueryRow(ctx, `
			INSERT INTO alerts (source_id, source_identifier, event, severity, urgency, certainty, sent_at, content_hash)
			VALUES ('kmd',$1,'Rain','Severe','Expected','Likely',now(),$2) RETURNING id`,
			"cap-"+string(rune('a'+i)), "hash-"+string(rune('a'+i))).Scan(&alertID))
		var tokenID string
		require.NoError(t, pool.QueryRow(ctx, `INSERT INTO push_tokens (token, platform) VALUES ($1,'android') RETURNING id`,
			"cap-token-"+string(rune('a'+i))).Scan(&tokenID))
		_, err := pool.Exec(ctx, `INSERT INTO alert_deliveries (alert_id,user_id,push_token_id,reason,status,created_at)
			VALUES ($1,'cap-user',$2,'home','sent',now())`, alertID, tokenID)
		require.NoError(t, err)
	}
	assert.True(t, programCapExceeded(ctx, pool, "cap-user"))
	assert.False(t, programCapExceeded(ctx, pool, "other-user"))
}

func TestSendProgramUpdatePayloadAndDelivery(t *testing.T) {
	pool := db.SetupTestDB(t)
	ctx := context.Background()
	_, err := pool.Exec(ctx, `INSERT INTO kenya_counties (gid, adm1_name, adm1_pcode) VALUES (1, 'Nairobi', 'KE047')`)
	require.NoError(t, err)
	userID := "00000000-0000-0000-0000-000000000031"
	_, err = pool.Exec(ctx, `INSERT INTO users (id, name, email) VALUES ($1,'Program','program@example.com')`, userID)
	require.NoError(t, err)
	_, err = pool.Exec(ctx, `INSERT INTO user_profiles (user_id, consent_notifications, created_by)
		VALUES ($1,true,$1)`, userID)
	require.NoError(t, err)
	_, err = pool.Exec(ctx, `INSERT INTO alert_preferences
		(user_id, home_county_gid, categories, os_permission_ok, program_important_only)
		VALUES ($1,1,ARRAY['Met','Program'],true,false)`, userID)
	require.NoError(t, err)
	_, err = pool.Exec(ctx, `INSERT INTO push_tokens (user_id, token, platform)
		VALUES ($1,'ExponentPushToken[program]','android')`, userID)
	require.NoError(t, err)

	var programID string
	require.NoError(t, pool.QueryRow(ctx, `
		INSERT INTO program_updates (title, body, priority, status, created_by)
		VALUES ('Cleanup moved','Starts at 08:00','Important','published','test') RETURNING id`).Scan(&programID))

	var captured []ExpoPushMessage
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.NoError(t, json.NewDecoder(r.Body).Decode(&captured))
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"data":[{"status":"ok","id":"ticket-1"}]}`))
	}))
	defer server.Close()

	client := &Client{httpClient: server.Client(), pushAPIURL: server.URL}
	sent, err := SendProgramUpdate(ctx, pool, client, ProgramPush{
		ID: programID, Title: "Cleanup moved", Body: "Starts at 08:00", Priority: "Important",
		Route: "/alerts/program/" + programID,
	})
	require.NoError(t, err)
	assert.Equal(t, 1, sent)
	require.Len(t, captured, 1)
	assert.Equal(t, ProgramChannelID, captured[0].ChannelID)
	assert.Equal(t, "default", captured[0].Priority)
	data, ok := captured[0].Data.(map[string]any)
	require.True(t, ok)
	assert.Equal(t, programID, data["programUpdateId"])

	var status string
	require.NoError(t, pool.QueryRow(ctx, `
		SELECT status FROM program_update_deliveries WHERE program_update_id=$1`, programID).Scan(&status))
	assert.Equal(t, "sent", status)
}
