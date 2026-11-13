package server_test

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bradleygichuru/ytci-go/internal/config"
	"github.com/bradleygichuru/ytci-go/internal/db"
	"github.com/bradleygichuru/ytci-go/internal/server"
)

func TestMarketplacePublicSurfaces(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}
	ctx := context.Background()
	pool := db.SetupTestDB(t)
	defer pool.Close()

	cfg := &config.Config{Port: "8080", DatabaseURL: "ignored", AdminJWKSURL: "http://test.invalid/jwks", CORSOrigins: "*"}
	r := server.New(cfg, pool)
	ts := httptest.NewServer(r)
	defer ts.Close()

	_, err := pool.Exec(ctx, `
		INSERT INTO partner_businesses
			(name, slug, category, tagline, short_description, county, redirect_url, status, is_featured, display_order)
		VALUES
			('Mara Walks', 'mara-walks', 'experiences', 'Walk with the Maasai', 'Guided conservancy walks.', 'Narok', 'https://example.com/mara', 'published', true, 1),
			('Nairobi Bites', 'nairobi-bites', 'food_drink', 'Street food, upgraded', 'A food tour in Kilimani.', 'Nairobi', 'https://example.com/bites', 'published', false, 2),
			('Kisumu Kayaks', 'kisumu-kayaks', 'experiences', 'Paddle the lake', 'Sunset kayak trips.', 'Kisumu', 'https://example.com/kayak', 'published', false, 3),
			('Draft Co', 'draft-co', 'services', 'Not ready', 'Draft listing.', 'Nairobi', 'https://example.com/draft', 'draft', false, 4)
	`)
	require.NoError(t, err)

	resp, err := http.Get(ts.URL + "/v1/public/marketplace/businesses?category=food_drink")
	require.NoError(t, err)
	defer resp.Body.Close()
	assert.Equal(t, http.StatusOK, resp.StatusCode)

	var list struct {
		Items      []map[string]any `json:"items"`
		HasMore    bool             `json:"hasMore"`
		NextCursor *string          `json:"nextCursor"`
	}
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&list))
	require.Len(t, list.Items, 1)
	assert.Equal(t, "Nairobi Bites", list.Items[0]["name"])
	assert.Equal(t, false, list.Items[0]["isFeatured"])

	resp2, err := http.Get(ts.URL + "/v1/public/marketplace/businesses/mara-walks")
	require.NoError(t, err)
	defer resp2.Body.Close()
	assert.Equal(t, http.StatusOK, resp2.StatusCode)
	var detail map[string]any
	require.NoError(t, json.NewDecoder(resp2.Body).Decode(&detail))
	assert.Equal(t, "https://example.com/mara", detail["redirectUrl"])
	assert.NotEmpty(t, detail["shortDescription"])

	resp3, err := http.Get(ts.URL + "/v1/public/marketplace/businesses/draft-co")
	require.NoError(t, err)
	defer resp3.Body.Close()
	assert.Equal(t, http.StatusNotFound, resp3.StatusCode)

	// Pagination: three published rows, two per page.
	resp4, err := http.Get(ts.URL + "/v1/public/marketplace/businesses?limit=2")
	require.NoError(t, err)
	defer resp4.Body.Close()
	var pageOne struct {
		Items      []map[string]any `json:"items"`
		HasMore    bool             `json:"hasMore"`
		NextCursor *string          `json:"nextCursor"`
	}
	require.NoError(t, json.NewDecoder(resp4.Body).Decode(&pageOne))
	require.Len(t, pageOne.Items, 2)
	assert.True(t, pageOne.HasMore)
	require.NotNil(t, pageOne.NextCursor)

	resp5, err := http.Get(ts.URL + "/v1/public/marketplace/businesses?limit=2&cursor=" + *pageOne.NextCursor)
	require.NoError(t, err)
	defer resp5.Body.Close()
	var pageTwo struct {
		Items   []map[string]any `json:"items"`
		HasMore bool             `json:"hasMore"`
	}
	require.NoError(t, json.NewDecoder(resp5.Body).Decode(&pageTwo))
	require.Len(t, pageTwo.Items, 1)
	assert.False(t, pageTwo.HasMore)

	_, err = pool.Exec(ctx, `
		INSERT INTO accommodation_properties
			(name, slug, tier, property_type, short_description, county, price_from_kes, price_note, price_checked_at,
			 distance_note, cancellation_note, payment_note, included_note, badge_label, booking_url, status, is_featured, display_order)
		VALUES
			('Mara River Camp', 'mara-river-camp', 'midrange', 'camp', 'Tented camp on the Mara River.', 'Narok', 12500, 'per night', '2026-11-12',
			 '10 min to the gate', 'Free to cancel until 48 hours before', 'Pay at the stay or by M-PESA', 'Breakfast included', 'Verified partner', 'https://example.com/stay', 'published', true, 1),
			('City Hostel', 'city-hostel', 'budget', 'hostel', 'Bunks in the city.', 'Nairobi', 1800, NULL, NULL,
			 NULL, NULL, NULL, NULL, NULL, 'https://example.com/hostel', 'published', false, 2)
	`)
	require.NoError(t, err)

	resp6, err := http.Get(ts.URL + "/v1/public/stays?tier=midrange")
	require.NoError(t, err)
	defer resp6.Body.Close()
	assert.Equal(t, http.StatusOK, resp6.StatusCode)
	var stays struct {
		Items []map[string]any `json:"items"`
	}
	require.NoError(t, json.NewDecoder(resp6.Body).Decode(&stays))
	require.Len(t, stays.Items, 1)
	assert.Equal(t, "Mara River Camp", stays.Items[0]["name"])
	assert.Equal(t, float64(12500), stays.Items[0]["priceFromKes"])

	resp7, err := http.Get(ts.URL + "/v1/public/stays/mara-river-camp")
	require.NoError(t, err)
	defer resp7.Body.Close()
	assert.Equal(t, http.StatusOK, resp7.StatusCode)
	var stayDetail map[string]any
	require.NoError(t, json.NewDecoder(resp7.Body).Decode(&stayDetail))
	assert.Equal(t, "https://example.com/stay", stayDetail["bookingUrl"])
	assert.NotNil(t, stayDetail["amenities"])
	assert.Equal(t, "Free to cancel until 48 hours before", stayDetail["cancellationNote"])
	assert.Equal(t, "Verified partner", stayDetail["badgeLabel"])
	assert.Equal(t, "2026-11-12", stayDetail["priceCheckedAt"])

	_, err = pool.Exec(ctx, `
		INSERT INTO esim_providers
			(name, slug, headline, description, price_label, redirect_url, status, is_featured, display_order)
		VALUES
			('Airalo', 'airalo', 'Land connected.', 'Install a Kenya eSIM before you fly.', 'From $4.50', 'https://example.com/esim', 'published', true, 1)
	`)
	require.NoError(t, err)

	resp8, err := http.Get(ts.URL + "/v1/public/marketplace/esim")
	require.NoError(t, err)
	defer resp8.Body.Close()
	assert.Equal(t, http.StatusOK, resp8.StatusCode)
	var esim struct {
		Provider map[string]any `json:"provider"`
	}
	require.NoError(t, json.NewDecoder(resp8.Body).Decode(&esim))
	require.NotNil(t, esim.Provider)
	assert.Equal(t, "Airalo", esim.Provider["name"])

	var businessID string
	require.NoError(t, pool.QueryRow(ctx, `SELECT id::text FROM partner_businesses WHERE slug = 'mara-walks'`).Scan(&businessID))

	body, _ := json.Marshal(map[string]string{
		"entityType": "partner_business",
		"entityId":   businessID,
		"source":     "marketplace_detail",
		"platform":   "ios",
	})
	resp9, err := http.Post(ts.URL+"/v1/mobile/marketplace/redirects", "application/json", bytes.NewBuffer(body))
	require.NoError(t, err)
	defer resp9.Body.Close()
	assert.Equal(t, http.StatusCreated, resp9.StatusCode)

	var redirects int
	require.NoError(t, pool.QueryRow(ctx, `SELECT count(*) FROM marketplace_redirects`).Scan(&redirects))
	assert.Equal(t, 1, redirects)

	badBody, _ := json.Marshal(map[string]string{
		"entityType": "partner_business",
		"entityId":   businessID,
		"source":     "marketplace_detail",
		"platform":   "desktop",
	})
	resp10, err := http.Post(ts.URL+"/v1/mobile/marketplace/redirects", "application/json", bytes.NewBuffer(badBody))
	require.NoError(t, err)
	defer resp10.Body.Close()
	assert.Equal(t, http.StatusBadRequest, resp10.StatusCode)
}
