package expo

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bradleygichuru/ytci-go/internal/db"
)

func TestComposerStopsPersistProvenance(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}
	pool := db.SetupTestDB(t)
	defer pool.Close()

	userID := newCollaborationTestUser(t, pool, "Composer Owner")
	body := map[string]any{
		"title":      "Composer Trip",
		"origin":     "Nairobi",
		"days":       2,
		"budgetBand": "mid-range",
		"interests":  []string{"Wildlife"},
		"stops": []map[string]any{{
			"day":           1,
			"displayOrder":  0,
			"title":         "Karura Forest",
			"destinationId": "",
			"placeId":       "ChIJ-karura-test",
			"provenance":    "unverified",
			"lat":           -1.2333,
			"lng":           36.8333,
			"county":        "Nairobi",
			"verified":      false,
			"kind":          "stop",
			"category":      "Wildlife",
			"estimatedCost": "",
		}},
	}

	createReq := budgetTestRequest(t, http.MethodPost, "/v1/mobile/itineraries", userID, body)
	createRec := httptest.NewRecorder()
	NewItinerariesHandler(pool).Create(createRec, createReq)
	require.Equal(t, http.StatusCreated, createRec.Code, createRec.Body.String())

	var created struct {
		ID string `json:"id"`
	}
	require.NoError(t, json.Unmarshal(createRec.Body.Bytes(), &created))
	require.NotEmpty(t, created.ID)

	stopsReq := budgetTestRequest(t, http.MethodGet, "/v1/mobile/itineraries/"+created.ID+"/stops", userID, nil)
	stopsReq.SetPathValue("id", created.ID)
	stopsRec := httptest.NewRecorder()
	NewItineraryStopsHandler(pool).GetStops(stopsRec, stopsReq)
	require.Equal(t, http.StatusOK, stopsRec.Code, stopsRec.Body.String())

	var stopsResponse struct {
		Stops []StopResponse `json:"stops"`
	}
	require.NoError(t, json.Unmarshal(stopsRec.Body.Bytes(), &stopsResponse))
	require.Len(t, stopsResponse.Stops, 1)
	stop := stopsResponse.Stops[0]

	assert.Equal(t, "ChIJ-karura-test", stop.PlaceID)
	assert.Equal(t, "unverified", stop.Provenance)
	assert.Equal(t, "Nairobi", stop.County)
	assert.Equal(t, "stop", stop.Kind)
	require.NotNil(t, stop.Verified)
	assert.False(t, *stop.Verified)
	require.NotNil(t, stop.Latitude)
	require.NotNil(t, stop.Longitude)
	assert.InDelta(t, -1.2333, *stop.Latitude, 0.0001)
	assert.InDelta(t, 36.8333, *stop.Longitude, 0.0001)
	require.NotNil(t, stop.DestinationName)
	assert.Equal(t, "Karura Forest", *stop.DestinationName)
	assert.Empty(t, stop.DestinationID)
}
