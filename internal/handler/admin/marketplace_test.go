package admin_test

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bradleygichuru/ytci-go/internal/db"
	"github.com/bradleygichuru/ytci-go/internal/handler/admin"
)

func doJSON(t *testing.T, fn http.HandlerFunc, method, path string, body any) *httptest.ResponseRecorder {
	t.Helper()
	var buf bytes.Buffer
	if body != nil {
		require.NoError(t, json.NewEncoder(&buf).Encode(body))
	}
	req := httptest.NewRequest(method, path, &buf)
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()
	fn(rr, req)
	return rr
}

func TestMarketplaceAdminCRUD(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}
	ctx := context.Background()
	pool := db.SetupTestDB(t)
	defer pool.Close()
	h := admin.NewMarketplaceAdminHandler(pool, nil)

	// Business create.
	rr := doJSON(t, h.CreateBusiness, "POST", "/v1/marketplace/businesses", map[string]any{
		"name":             "Mara Walks",
		"slug":             "mara-walks",
		"category":         "experiences",
		"tagline":          "Walk with the Maasai",
		"shortDescription": "Guided conservancy walks.",
		"county":           "Narok",
		"redirectUrl":      "https://example.com/mara",
		"status":           "published",
		"isFeatured":       true,
		"displayOrder":     1,
	})
	require.Equal(t, http.StatusCreated, rr.Code, rr.Body.String())
	var created struct {
		ID string `json:"id"`
	}
	require.NoError(t, json.NewDecoder(rr.Body).Decode(&created))
	require.NotEmpty(t, created.ID)

	// Business update.
	updateReq := httptest.NewRequest("PATCH", "/v1/marketplace/businesses/"+created.ID, bytes.NewBufferString(`{"tagline":"Walk with Maasai guides"}`))
	updateReq.SetPathValue("id", created.ID)
	updateRR := httptest.NewRecorder()
	h.UpdateBusiness(updateRR, updateReq)
	assert.Equal(t, http.StatusOK, updateRR.Code, updateRR.Body.String())

	var tagline string
	require.NoError(t, pool.QueryRow(ctx, `SELECT tagline FROM partner_businesses WHERE id = $1`, created.ID).Scan(&tagline))
	assert.Equal(t, "Walk with Maasai guides", tagline)

	// Business list.
	listRR := doJSON(t, h.ListBusinesses, "GET", "/v1/marketplace/businesses", nil)
	assert.Equal(t, http.StatusOK, listRR.Code)
	var list struct {
		Items []map[string]any `json:"items"`
	}
	require.NoError(t, json.NewDecoder(listRR.Body).Decode(&list))
	require.Len(t, list.Items, 1)
	assert.Equal(t, "mara-walks", list.Items[0]["slug"])

	// Published stay requires a handoff URL.
	stayRR := doJSON(t, h.CreateStay, "POST", "/v1/stays", map[string]any{
		"name":             "Mara River Camp",
		"slug":             "mara-river-camp",
		"tier":             "midrange",
		"propertyType":     "camp",
		"shortDescription": "Tented camp on the Mara River.",
		"county":           "Narok",
		"status":           "published",
	})
	assert.Equal(t, http.StatusBadRequest, stayRR.Code)

	stayRR = doJSON(t, h.CreateStay, "POST", "/v1/stays", map[string]any{
		"name":             "Mara River Camp",
		"slug":             "mara-river-camp",
		"tier":             "midrange",
		"propertyType":     "camp",
		"shortDescription": "Tented camp on the Mara River.",
		"county":           "Narok",
		"priceFromKes":     12500,
		"locationLat":      -1.5,
		"locationLng":      35.2,
		"priceCheckedAt":   "2026-11-12",
		"distanceNote":     "10 min to the gate",
		"cancellationNote": "Free to cancel until 48 hours before",
		"paymentNote":      "Pay at the stay or by M-PESA",
		"includedNote":     "Breakfast and conservancy fee included",
		"badgeLabel":       "Verified partner",
		"amenities":        []string{"wifi", "breakfast"},
		"highlights":       []string{"River deck"},
		"bookingUrl":       "https://example.com/stay",
		"status":           "published",
		"isFeatured":       true,
	})
	require.Equal(t, http.StatusCreated, stayRR.Code, stayRR.Body.String())
	var createdStay struct {
		ID string `json:"id"`
	}
	require.NoError(t, json.NewDecoder(stayRR.Body).Decode(&createdStay))
	require.NotEmpty(t, createdStay.ID)

	var distanceNote, badgeLabel string
	require.NoError(t, pool.QueryRow(ctx, `SELECT distance_note, badge_label FROM accommodation_properties WHERE slug = 'mara-river-camp'`).Scan(&distanceNote, &badgeLabel))
	assert.Equal(t, "10 min to the gate", distanceNote)
	assert.Equal(t, "Verified partner", badgeLabel)

	// A partial PATCH must preserve omitted price and coordinates.
	partialReq := httptest.NewRequest("PATCH", "/v1/stays/"+createdStay.ID, bytes.NewBufferString(`{"badgeLabel":"YTC pick"}`))
	partialReq.SetPathValue("id", createdStay.ID)
	partialRR := httptest.NewRecorder()
	h.UpdateStay(partialRR, partialReq)
	assert.Equal(t, http.StatusOK, partialRR.Code, partialRR.Body.String())

	var preservedPrice int
	var preservedLat float64
	require.NoError(t, pool.QueryRow(ctx, `SELECT price_from_kes, location_lat FROM accommodation_properties WHERE id = $1`, createdStay.ID).Scan(&preservedPrice, &preservedLat))
	assert.Equal(t, 12500, preservedPrice)
	assert.InDelta(t, -1.5, preservedLat, 0.0001)

	// Clearing the indicative price is allowed; the form sends the full object.
	clearPriceReq := httptest.NewRequest("PATCH", "/v1/stays/"+createdStay.ID, bytes.NewBufferString(`{"priceFromKes":null}`))
	clearPriceReq.SetPathValue("id", createdStay.ID)
	clearPriceRR := httptest.NewRecorder()
	h.UpdateStay(clearPriceRR, clearPriceReq)
	assert.Equal(t, http.StatusOK, clearPriceRR.Code, clearPriceRR.Body.String())

	var priceIsNull bool
	require.NoError(t, pool.QueryRow(ctx, `SELECT price_from_kes IS NULL FROM accommodation_properties WHERE id = $1`, createdStay.ID).Scan(&priceIsNull))
	assert.True(t, priceIsNull)

	// First eSIM provider is featured.
	esimRR := doJSON(t, h.CreateEsimProvider, "POST", "/v1/esim-providers", map[string]any{
		"name":         "Airalo",
		"slug":         "airalo",
		"headline":     "Land connected.",
		"description":  "Install a Kenya eSIM before you fly.",
		"priceLabel":   "From $4.50",
		"redirectUrl":  "https://example.com/esim-a",
		"status":       "published",
		"isFeatured":   true,
		"displayOrder": 1,
	})
	require.Equal(t, http.StatusCreated, esimRR.Code, esimRR.Body.String())

	var firstEsimID string
	require.NoError(t, pool.QueryRow(ctx, `SELECT id::text FROM esim_providers WHERE slug = 'airalo'`).Scan(&firstEsimID))

	// A second featured provider replaces the first in one transaction.
	esimRR = doJSON(t, h.CreateEsimProvider, "POST", "/v1/esim-providers", map[string]any{
		"name":         "Holafly",
		"slug":         "holafly",
		"description":  "Unlimited data eSIM for Kenya.",
		"redirectUrl":  "https://example.com/esim-b",
		"status":       "published",
		"isFeatured":   true,
		"displayOrder": 2,
	})
	require.Equal(t, http.StatusCreated, esimRR.Code, esimRR.Body.String())

	var firstFeatured bool
	require.NoError(t, pool.QueryRow(ctx, `SELECT is_featured FROM esim_providers WHERE id = $1`, firstEsimID).Scan(&firstFeatured))
	assert.False(t, firstFeatured)

	var featuredCount int
	require.NoError(t, pool.QueryRow(ctx, `SELECT count(*) FROM esim_providers WHERE is_featured = true AND status = 'published'`).Scan(&featuredCount))
	assert.Equal(t, 1, featuredCount)

	// Delete is a hard delete.
	deleteReq := httptest.NewRequest("DELETE", "/v1/marketplace/businesses/"+created.ID, nil)
	deleteReq.SetPathValue("id", created.ID)
	deleteRR := httptest.NewRecorder()
	h.DeleteBusiness(deleteRR, deleteReq)
	assert.Equal(t, http.StatusNoContent, deleteRR.Code)

	var count int
	require.NoError(t, pool.QueryRow(ctx, `SELECT count(*) FROM partner_businesses`).Scan(&count))
	assert.Equal(t, 0, count)
}
