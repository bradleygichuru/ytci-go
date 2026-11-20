package admin

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bradleygichuru/ytci-go/internal/db"
	"github.com/bradleygichuru/ytci-go/internal/middleware"
)

func TestAdminCampaignSendCreatesProgramUpdate(t *testing.T) {
	pool := db.SetupTestDB(t)
	ctx := context.Background()
	_, _ = pool.Exec(ctx, `TRUNCATE kenya_counties CASCADE`)
	_, err := pool.Exec(ctx, `INSERT INTO kenya_counties (gid, adm1_name, adm1_pcode) VALUES (1, 'Nairobi', 'KE047') ON CONFLICT (gid) DO UPDATE SET adm1_name=EXCLUDED.adm1_name, adm1_pcode=EXCLUDED.adm1_pcode`)
	require.NoError(t, err)
	userID := "admin-push-nanoid-000000000041"
	_, err = pool.Exec(ctx, `INSERT INTO users (id, name, email) VALUES ($1,'Admin','push-admin@example.com')`, userID)
	require.NoError(t, err)

	h := NewPushHandler(pool, nil)
	body := `{
		"title":"Coastal cleanup",
		"body":"Moved to Saturday",
		"audience":{"type":"county","value":"Nairobi"},
		"data":{"route":"/campaigns/abc"}
	}`
	req := httptest.NewRequest(http.MethodPost, "/v1/admin/push/send", strings.NewReader(body))
	req = req.WithContext(context.WithValue(req.Context(), middleware.CtxUserID, userID))
	rec := httptest.NewRecorder()
	h.Send(rec, req)
	require.Equal(t, http.StatusCreated, rec.Code, rec.Body.String())

	var response struct {
		NotificationID  string `json:"notificationId"`
		ProgramUpdateID string `json:"programUpdateId"`
		RecipientCount  int    `json:"recipientCount"`
	}
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &response))
	assert.NotEmpty(t, response.NotificationID)
	assert.NotEmpty(t, response.ProgramUpdateID)
	assert.Equal(t, 0, response.RecipientCount)

	var title, priority, link, source string
	require.NoError(t, pool.QueryRow(ctx, `
		SELECT title, priority, COALESCE(link_url,''), source FROM program_updates WHERE id=$1`,
		response.ProgramUpdateID).Scan(&title, &priority, &link, &source))
	assert.Equal(t, "Coastal cleanup", title)
	assert.Equal(t, "Normal", priority)
	assert.Equal(t, "/campaigns/abc", link)
	assert.Equal(t, "campaign", source)

	var data, target string
	require.NoError(t, pool.QueryRow(ctx, `
		SELECT COALESCE(data,''), COALESCE(target_audience,'') FROM push_notifications WHERE id=$1`,
		response.NotificationID).Scan(&data, &target))
	assert.JSONEq(t, `{"route":"/campaigns/abc"}`, data)
	assert.JSONEq(t, `{"countyGids":[1]}`, target)
}

func TestAdminCampaignSendRejectsUnknownCountyAsGlobal(t *testing.T) {
	pool := db.SetupTestDB(t)
	ctx := context.Background()
	userID := "admin-push-nanoid-000000000042"
	_, err := pool.Exec(ctx, `INSERT INTO users (id, name, email) VALUES ($1,'Admin','push-admin2@example.com')`, userID)
	require.NoError(t, err)

	h := NewPushHandler(pool, nil)
	body := `{"title":"Update","body":"Body","audience":{"type":"county","value":"Atlantis"}}`
	req := httptest.NewRequest(http.MethodPost, "/v1/admin/push/send", strings.NewReader(body))
	req = req.WithContext(context.WithValue(req.Context(), middleware.CtxUserID, userID))
	rec := httptest.NewRecorder()
	h.Send(rec, req)
	require.Equal(t, http.StatusCreated, rec.Code, rec.Body.String())

	var target string
	require.NoError(t, pool.QueryRow(ctx, `SELECT COALESCE(target_audience,'') FROM push_notifications`).Scan(&target))
	assert.JSONEq(t, `{"countyGids":[-1]}`, target)
}

func TestAdminCampaignSendRejectsRoleAudience(t *testing.T) {
	pool := db.SetupTestDB(t)
	ctx := context.Background()
	userID := "admin-role-nanoid-000000000043"
	_, err := pool.Exec(ctx, `INSERT INTO users (id, name, email) VALUES ($1,'Admin','push-role@example.com')`, userID)
	require.NoError(t, err)

	h := NewPushHandler(pool, nil)
	body := `{"title":"Update","body":"Body","audience":{"type":"role","value":"administrator"}}`
	req := httptest.NewRequest(http.MethodPost, "/v1/admin/push/send", strings.NewReader(body))
	req = req.WithContext(context.WithValue(req.Context(), middleware.CtxUserID, userID))
	rec := httptest.NewRecorder()
	h.Send(rec, req)
	require.Equal(t, http.StatusBadRequest, rec.Code)
}
