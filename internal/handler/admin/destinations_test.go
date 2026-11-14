package admin

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bradleygichuru/ytci-go/internal/db"
)

func TestDestinationPublishingRequiresVerificationAndCounty(t *testing.T) {
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

	publish := func(id string, body string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodPatch, "/v1/destinations/"+id,
			strings.NewReader(body))
		req.SetPathValue("id", id)
		rec := httptest.NewRecorder()
		h.Update(rec, req)
		return rec
	}

	rec := publish(nairobiID, `{"status":"published"}`)
	assert.Equal(t, http.StatusBadRequest, rec.Code, rec.Body.String())
	assert.Contains(t, rec.Body.String(), "VERIFICATION_REQUIRED")

	rec = publish(nairobiID, `{"status":"published","verificationStatus":"verified"}`)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	var status, verification string
	var verifiedAt *time.Time
	require.NoError(t, pool.QueryRow(ctx,
		`SELECT status, verification_status, verified_at FROM destinations WHERE id = $1`, nairobiID).
		Scan(&status, &verification, &verifiedAt))
	assert.Equal(t, "published", status)
	assert.Equal(t, "verified", verification)
	assert.NotNil(t, verifiedAt)

	rec = publish(unknownID, `{"status":"published","verificationStatus":"verified"}`)
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
