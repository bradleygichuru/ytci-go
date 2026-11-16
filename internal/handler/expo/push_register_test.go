package expo

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

func TestPushRegisterAcceptsBetterAuthTextUserID(t *testing.T) {
	pool := db.SetupTestDB(t)
	ctx := context.Background()
	userID := "xyWoDKwNt4OEK8IOhZaBfrh3BEkppzE9"
	_, err := pool.Exec(ctx, `INSERT INTO users (id, name, email) VALUES ($1,'Alice','alice@example.com')`, userID)
	require.NoError(t, err)
	_, err = pool.Exec(ctx, `INSERT INTO user_profiles (user_id, county, created_by)
		VALUES ($1,'Nairobi',$1)`, userID)
	require.NoError(t, err)
	_, err = pool.Exec(ctx, `INSERT INTO kenya_counties (gid, adm1_name, adm1_pcode)
		VALUES (1,'Nairobi','KE047')`)
	require.NoError(t, err)

	h := NewPushRegisterHandler(pool)
	body := `{"token":"ExponentPushToken[text-id]","platform":"android"}`
	req := httptest.NewRequest(http.MethodPost, "/v1/mobile/push/register", strings.NewReader(body))
	req = req.WithContext(context.WithValue(req.Context(), middleware.CtxUserID, userID))
	rec := httptest.NewRecorder()
	h.Register(rec, req)
	require.Equal(t, http.StatusCreated, rec.Code, rec.Body.String())

	var storedUser string
	require.NoError(t, pool.QueryRow(ctx, `SELECT user_id FROM push_tokens WHERE token='ExponentPushToken[text-id]'`).Scan(&storedUser))
	assert.Equal(t, userID, storedUser)

	var prefUser string
	var home *int
	require.NoError(t, pool.QueryRow(ctx, `SELECT user_id, home_county_gid FROM alert_preferences WHERE user_id=$1`, userID).Scan(&prefUser, &home))
	assert.Equal(t, userID, prefUser)
	require.NotNil(t, home)
	assert.Equal(t, 1, *home)

	var response map[string]string
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &response))
	assert.Equal(t, "registered", response["status"])
}
