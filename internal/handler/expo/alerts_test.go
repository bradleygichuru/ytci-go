package expo

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bradleygichuru/ytci-go/internal/db"
	"github.com/bradleygichuru/ytci-go/internal/middleware"
)

func TestAlertsListAndPreferences(t *testing.T) {
	pool := db.SetupTestDB(t)
	ctx := context.Background()
	_, _ = pool.Exec(ctx, `TRUNCATE kenya_counties CASCADE`)
	_, err := pool.Exec(ctx, `INSERT INTO kenya_counties (gid, adm1_name, adm1_pcode, geom)
		VALUES (1, 'Nairobi', 'KE047', ST_GeomFromText('MULTIPOLYGON(((36.7 -1.2, 36.9 -1.2, 36.9 -1.4, 36.7 -1.4, 36.7 -1.2)))', 4326)) ON CONFLICT (gid) DO UPDATE SET adm1_name=EXCLUDED.adm1_name, adm1_pcode=EXCLUDED.adm1_pcode`)
	require.NoError(t, err)
	_, err = pool.Exec(ctx, `INSERT INTO alerts
		(source_id, source_identifier, event, severity, urgency, certainty, headline, sent_at, content_hash, mapping_method)
		VALUES ('kmd','alert-1','Heavy rainfall','Severe','Expected','Likely','Heavy rainfall expected', now(), 'hash-1', 'area_desc')`)
	require.NoError(t, err)
	var alertID string
	require.NoError(t, pool.QueryRow(ctx, `SELECT id FROM alerts WHERE source_identifier='alert-1'`).Scan(&alertID))
	_, err = pool.Exec(ctx, `INSERT INTO alert_areas (alert_id, county_gid, method, is_primary) VALUES ($1,1,'area_desc',true)`, alertID)
	require.NoError(t, err)
	_, err = pool.Exec(ctx, `INSERT INTO alert_updates (alert_id,version,source_identifier,msg_type,content_hash,changed_fields,snapshot,source_sent_at)
		VALUES ($1,2,'alert-1','Update','hash-2','{content}','{}',now())`, alertID)
	require.NoError(t, err)

	_, err = pool.Exec(ctx, `INSERT INTO alerts
		(source_id, source_identifier, event, severity, urgency, certainty, headline, status, sent_at, expires_at, content_hash, mapping_method)
		VALUES ('kmd','expired-1','Old rainfall','Severe','Expected','Likely','Old rainfall watch','expired', now() - interval '1 day', now() - interval '1 hour', 'hash-expired', 'area_desc')`)
	require.NoError(t, err)
	_, err = pool.Exec(ctx, `INSERT INTO alerts
		(source_id, source_identifier, event, severity, urgency, certainty, sent_at, content_hash, is_test)
		VALUES ('kmd','test-1','Test alert','Severe','Expected','Likely', now(), 'hash-test', true)`)
	require.NoError(t, err)

	userID := "00000000-0000-0000-0000-000000000001"
	_, err = pool.Exec(ctx, `INSERT INTO users (id, name, email) VALUES ($1,'Test','test@example.com')`, userID)
	require.NoError(t, err)
	_, err = pool.Exec(ctx, `INSERT INTO user_profiles (user_id, created_by) VALUES ($1,$1)`, userID)
	require.NoError(t, err)

	h := NewAlertsHandler(pool)
	locationReq := httptest.NewRequest(http.MethodPost, "/v1/mobile/alert-location", strings.NewReader(`{"countyGid":1}`))
	locationReq = locationReq.WithContext(context.WithValue(locationReq.Context(), middleware.CtxUserID, userID))
	locationRec := httptest.NewRecorder()
	h.UpdateLocation(locationRec, locationReq)
	require.Equal(t, http.StatusForbidden, locationRec.Code, "location must fail closed without consent")

	_, err = pool.Exec(ctx, `UPDATE user_profiles SET consent_location=true WHERE user_id=$1`, userID)
	require.NoError(t, err)
	locationReq = httptest.NewRequest(http.MethodPost, "/v1/mobile/alert-location", strings.NewReader(`{"countyGid":1}`))
	locationReq = locationReq.WithContext(context.WithValue(locationReq.Context(), middleware.CtxUserID, userID))
	locationRec = httptest.NewRecorder()
	h.UpdateLocation(locationRec, locationReq)
	require.Equal(t, http.StatusNoContent, locationRec.Code)

	var tokenID string
	require.NoError(t, pool.QueryRow(ctx, `INSERT INTO push_tokens (user_id, token, platform) VALUES ($1,'reason-token','android') RETURNING id`, userID).Scan(&tokenID))
	_, err = pool.Exec(ctx, `INSERT INTO alert_deliveries (alert_id,user_id,push_token_id,reason,status)
		VALUES ($1,$2,$3,'home','queued')`, alertID, userID, tokenID)
	require.NoError(t, err)

	req := httptest.NewRequest(http.MethodGet, "/v1/mobile/alerts", nil)
	req = req.WithContext(context.WithValue(req.Context(), middleware.CtxUserID, userID))
	rec := httptest.NewRecorder()
	h.List(rec, req)
	require.Equal(t, http.StatusOK, rec.Code)

	var list struct {
		Items        []alertJSON      `json:"items"`
		SourceHealth []map[string]any `json:"sourceHealth"`
	}
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &list))
	require.Len(t, list.Items, 2)
	assert.Equal(t, "Severe", list.Items[0].Severity)
	assert.Equal(t, "active", list.Items[0].Status)
	assert.Equal(t, []string{"Nairobi"}, list.Items[0].Counties)
	assert.Equal(t, []int{1}, list.Items[0].CountyGids)
	assert.True(t, list.Items[0].HasUpdates)
	require.NotNil(t, list.Items[0].Reason)
	assert.Equal(t, "home", *list.Items[0].Reason)
	assert.Equal(t, "expired", list.Items[1].Status, "expired tombstones stay readable")
	require.NotEmpty(t, list.SourceHealth)
	assert.Equal(t, "unknown", list.SourceHealth[0]["state"])

	req = httptest.NewRequest(http.MethodGet, "/v1/mobile/alert-preferences", nil)
	req = req.WithContext(context.WithValue(req.Context(), middleware.CtxUserID, userID))
	rec = httptest.NewRecorder()
	h.GetPreferences(rec, req)
	require.Equal(t, http.StatusOK, rec.Code)
	var prefs preferencesJSON
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &prefs))
	assert.Equal(t, "Severe", prefs.MinSeverity)
	assert.Equal(t, "22:00", prefs.QuietHoursStart)
	assert.False(t, prefs.OSPermissionOK)

	body := `{"minSeverity":"Moderate","homeCountyGid":1,"watchCountyGids":[1],"programImportantOnly":false}`
	req = httptest.NewRequest(http.MethodPatch, "/v1/mobile/alert-preferences", strings.NewReader(body))
	req = req.WithContext(context.WithValue(req.Context(), middleware.CtxUserID, userID))
	rec = httptest.NewRecorder()
	h.UpdatePreferences(rec, req)
	require.Equal(t, http.StatusOK, rec.Code)
	var updated preferencesJSON
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &updated))
	assert.Equal(t, "Moderate", updated.MinSeverity)
	require.NotNil(t, updated.HomeCountyGid)
	assert.Equal(t, 1, *updated.HomeCountyGid)
	assert.Equal(t, []int{1}, updated.WatchCountyGids)
	assert.False(t, updated.ProgramImportantOnly)
}

func TestAlertReadState(t *testing.T) {
	pool := db.SetupTestDB(t)
	ctx := context.Background()
	_, err := pool.Exec(ctx, `INSERT INTO alerts
		(source_id, source_identifier, event, severity, urgency, certainty, sent_at, content_hash)
		VALUES ('kmd','alert-2','Heavy rainfall','Severe','Expected','Likely', now(), 'hash-2')`)
	require.NoError(t, err)
	var alertID string
	require.NoError(t, pool.QueryRow(ctx, `SELECT id FROM alerts WHERE source_identifier='alert-2'`).Scan(&alertID))
	userID := "00000000-0000-0000-0000-000000000002"
	_, err = pool.Exec(ctx, `INSERT INTO users (id, name, email) VALUES ($1,'Reader','reader@example.com')`, userID)
	require.NoError(t, err)

	h := NewAlertsHandler(pool)
	req := httptest.NewRequest(http.MethodPost, "/v1/mobile/alerts/"+alertID+"/read", nil)
	req.SetPathValue("id", alertID)
	req = req.WithContext(context.WithValue(req.Context(), middleware.CtxUserID, userID))
	rec := httptest.NewRecorder()
	h.MarkRead(rec, req)
	require.Equal(t, http.StatusNoContent, rec.Code)

	var readAt *time.Time
	require.NoError(t, pool.QueryRow(ctx, `SELECT read_at FROM alert_read_state WHERE user_id=$1 AND alert_id=$2`, userID, alertID).Scan(&readAt))
	require.NotNil(t, readAt)

	req = httptest.NewRequest(http.MethodPost, "/v1/mobile/alerts/"+alertID+"/dismiss", nil)
	req.SetPathValue("id", alertID)
	req = req.WithContext(context.WithValue(req.Context(), middleware.CtxUserID, userID))
	rec = httptest.NewRecorder()
	h.Dismiss(rec, req)
	require.Equal(t, http.StatusNoContent, rec.Code)

	req = httptest.NewRequest(http.MethodGet, "/v1/mobile/alerts", nil)
	req = req.WithContext(context.WithValue(req.Context(), middleware.CtxUserID, userID))
	rec = httptest.NewRecorder()
	h.List(rec, req)
	require.Equal(t, http.StatusOK, rec.Code)
	var list struct {
		Items []alertJSON `json:"items"`
	}
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &list))
	assert.Empty(t, list.Items, "dismissed alerts leave the inbox")
}

func TestProgramUpdatesRespectCountyTargeting(t *testing.T) {
	pool := db.SetupTestDB(t)
	ctx := context.Background()
	_, _ = pool.Exec(ctx, `TRUNCATE kenya_counties CASCADE`)
	_, err := pool.Exec(ctx, `INSERT INTO kenya_counties (gid, adm1_name, adm1_pcode, geom)
		VALUES (1, 'Nairobi', 'KE047', ST_GeomFromText('MULTIPOLYGON(((36.7 -1.2, 36.9 -1.2, 36.9 -1.4, 36.7 -1.4, 36.7 -1.2)))', 4326)) ON CONFLICT (gid) DO UPDATE SET adm1_name=EXCLUDED.adm1_name, adm1_pcode=EXCLUDED.adm1_pcode`)
	require.NoError(t, err)

	userID := "00000000-0000-0000-0000-000000000004"
	_, err = pool.Exec(ctx, `INSERT INTO users (id, name, email) VALUES ($1,'Targeted','targeted@example.com')`, userID)
	require.NoError(t, err)
	_, err = pool.Exec(ctx, `INSERT INTO alert_preferences (user_id, home_county_gid) VALUES ($1,1)`, userID)
	require.NoError(t, err)
	_, err = pool.Exec(ctx, `INSERT INTO program_updates (title, body, priority, status, county_gids, published_at, created_by)
		VALUES ('Nairobi cleanup','Nairobi only','Normal','published',ARRAY[1],now(),$1),
		       ('App update','Everyone','Normal','published','{}',now(),$1)`, userID)
	require.NoError(t, err)

	h := NewAlertsHandler(pool)

	req := httptest.NewRequest(http.MethodGet, "/v1/mobile/program-updates", nil)
	rec := httptest.NewRecorder()
	h.ListProgramUpdates(rec, req)
	require.Equal(t, http.StatusOK, rec.Code)
	var guest struct {
		Items []map[string]any `json:"items"`
	}
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &guest))
	require.Len(t, guest.Items, 1)
	assert.Equal(t, "App update", guest.Items[0]["title"])

	req = httptest.NewRequest(http.MethodGet, "/v1/mobile/program-updates", nil)
	req = req.WithContext(context.WithValue(req.Context(), middleware.CtxUserID, userID))
	rec = httptest.NewRecorder()
	h.ListProgramUpdates(rec, req)
	require.Equal(t, http.StatusOK, rec.Code)
	var member struct {
		Items []map[string]any `json:"items"`
	}
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &member))
	require.Len(t, member.Items, 2)
}

func TestListAlertCounties(t *testing.T) {
	pool := db.SetupTestDB(t)
	ctx := context.Background()
	_, _ = pool.Exec(ctx, `TRUNCATE kenya_counties CASCADE`)
	_, err := pool.Exec(ctx, `INSERT INTO kenya_counties (gid, adm1_name, adm1_pcode) VALUES
		(1, 'Nairobi', 'KE047'), (2, 'Mombasa', 'KE001') ON CONFLICT (gid) DO UPDATE SET adm1_name=EXCLUDED.adm1_name, adm1_pcode=EXCLUDED.adm1_pcode`)
	require.NoError(t, err)

	h := NewAlertsHandler(pool)
	req := httptest.NewRequest(http.MethodGet, "/v1/mobile/alert-counties", nil)
	rec := httptest.NewRecorder()
	h.ListCounties(rec, req)
	require.Equal(t, http.StatusOK, rec.Code)

	var body struct {
		Items []countyOptionJSON `json:"items"`
	}
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &body))
	require.Len(t, body.Items, 2)
	assert.Equal(t, "Mombasa", body.Items[0].Name)
	assert.Equal(t, 2, body.Items[0].GID)
	assert.Equal(t, "Nairobi", body.Items[1].Name)
}
