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

func TestProgramUpdateCreatePublishList(t *testing.T) {
	pool := db.SetupTestDB(t)
	ctx := context.Background()
	userID := "00000000-0000-0000-0000-000000000003"
	_, err := pool.Exec(ctx, `INSERT INTO users (id, name, email) VALUES ($1,'Admin','admin@example.com')`, userID)
	require.NoError(t, err)

	h := NewProgramUpdateHandler(pool)
	body := `{"title":"Cleanup moved","body":"The Kilifi cleanup starts at 08:00.","priority":"Important","countyGids":[1]}`
	req := httptest.NewRequest(http.MethodPost, "/v1/admin/program-updates", strings.NewReader(body))
	req = req.WithContext(context.WithValue(req.Context(), middleware.CtxUserID, userID))
	rec := httptest.NewRecorder()
	h.Create(rec, req)
	require.Equal(t, http.StatusCreated, rec.Code)

	var created struct {
		ID string `json:"id"`
	}
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &created))
	require.NotEmpty(t, created.ID)

	req = httptest.NewRequest(http.MethodPost, "/v1/admin/program-updates/"+created.ID+"/publish", nil)
	req.SetPathValue("id", created.ID)
	rec = httptest.NewRecorder()
	h.Publish(rec, req)
	require.Equal(t, http.StatusOK, rec.Code)

	req = httptest.NewRequest(http.MethodGet, "/v1/admin/program-updates?status=published", nil)
	rec = httptest.NewRecorder()
	h.List(rec, req)
	require.Equal(t, http.StatusOK, rec.Code)
	var list struct {
		Items []map[string]any `json:"items"`
	}
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &list))
	require.Len(t, list.Items, 1)
	assert.Equal(t, "Cleanup moved", list.Items[0]["title"])
	assert.Equal(t, "published", list.Items[0]["status"])
}
