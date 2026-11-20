package admin

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bradleygichuru/ytci-go/internal/db"
)

func TestDestinationPublishingRequiresCanonicalCounty(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}
	ctx := context.Background()
	pool := db.SetupTestDB(t)
	defer pool.Close()

	h := NewDestinationsHandler(pool, nil, nil)

	var nairobiID, unknownID string
	require.NoError(t, pool.QueryRow(ctx, `
		INSERT INTO destinations (name, slug, county, category, status, location)
		VALUES ('Publishable Park', 'publishable-park', 'Nairobi', 'Wildlife', 'draft',
		        ST_SetSRID(ST_MakePoint(36.8219, -1.2921), 4326))
		RETURNING id::text
	`).Scan(&nairobiID))
	require.NoError(t, pool.QueryRow(ctx, `
		INSERT INTO destinations (name, slug, county, category, status)
		VALUES ('Unplaceable Park', 'unplaceable-park', 'Atlantis', 'Wildlife', 'draft')
		RETURNING id::text
	`).Scan(&unknownID))

	publish := func(id string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodPatch, "/v1/destinations/"+id,
			strings.NewReader(`{"status":"published"}`))
		req.SetPathValue("id", id)
		rec := httptest.NewRecorder()
		h.Update(rec, req)
		return rec
	}

	rec := publish(nairobiID)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	var status string
	require.NoError(t, pool.QueryRow(ctx,
		`SELECT status FROM destinations WHERE id = $1`, nairobiID).Scan(&status))
	assert.Equal(t, "published", status)

	rec = publish(unknownID)
	assert.Equal(t, http.StatusBadRequest, rec.Code, rec.Body.String())
	assert.Contains(t, rec.Body.String(), "COUNTY_NOT_FOUND")

	req := httptest.NewRequest(http.MethodPatch, "/v1/destinations/"+nairobiID,
		strings.NewReader(`{"category":"church"}`))
	req.SetPathValue("id", nairobiID)
	rec = httptest.NewRecorder()
	h.Update(rec, req)
	assert.Equal(t, http.StatusBadRequest, rec.Code)
	assert.Contains(t, rec.Body.String(), "INVALID_CATEGORY")
}
