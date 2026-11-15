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
	_, err := pool.Exec(ctx, `INSERT INTO kenya_counties (gid, adm1_name, adm1_pcode, geom)
		VALUES (1, 'Nairobi', 'KE047', ST_GeomFromText('MULTIPOLYGON(((36.7 -1.2, 36.9 -1.2, 36.9 -1.4, 36.7 -1.4, 36.7 -1.2)))', 4326))`)
	require.NoError(t, err)
	_, err = pool.Exec(ctx, `INSERT INTO alerts
		(source_id, source_identifier, event, severity, urgency, certainty, headline, sent_at, content_hash, mapping_method)
		VALUES ('kmd','alert-1','Heavy rainfall','Severe','Expected','Likely','Heavy rainfall expected', now(), 'hash-1', 'area_desc')`)
	require.NoError(t, err)
	var alertID string
	require.NoError(t, pool.QueryRow(ctx, `SELECT id FROM alerts WHERE source_identifier='alert-1'`).Scan(&alertID))
	_, err = pool.Exec(ctx, `INSERT INTO alert_areas (alert_id, county_gid, method, is_primary) VALUES ($1,1,'area_desc',true)`, alertID)
	require.NoError(t, err)

	userID := "00000000-0000-0000-0000-000000000001"
	_, err = pool.Exec(ctx, `INSERT INTO users (id, name, email) VALUES ($1,'Test','test@example.com')`, userID)
	require.NoError(t, err)
	_, err = pool.Exec(ctx, `INSERT INTO user_profiles (user_id, created_by) VALUES ($1,$1)`, userID)
	require.NoError(t, err)

	h := NewAlertsHandler(pool)
	req := httptest.NewRequest(http.MethodGet, "/v1/mobile/alerts", nil)
	rec := httptest.NewRecorder()
	h.List(rec, req)
	require.Equal(t, http.StatusOK, rec.Code)

	var list struct {
		Items []alertJSON `json:"items"`
	}
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &list))
	require.Len(t, list.Items, 1)
	assert.Equal(t, "Severe", list.Items[0].Severity)
	assert.Equal(t, []string{"Nairobi"}, list.Items[0].Counties)
	assert.Equal(t, []int{1}, list.Items[0].CountyGids)

	req = httptest.NewRequest(http.MethodGet, "/v1/mobile/alert-preferences", nil)
	req = req.WithContext(context.WithValue(req.Context(), middleware.CtxUserID, userID))
	rec = httptest.NewRecorder()
	h.GetPreferences(rec, req)
	require.Equal(t, http.StatusOK, rec.Code)
	var prefs preferencesJSON
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &prefs))
	assert.Equal(t, "Severe", prefs.MinSeverity)

	body := `{"minSeverity":"Moderate","homeCountyGid":1,"watchCountyGids":[1]}`
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
}
