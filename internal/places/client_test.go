package places

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestAutocomplete(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "POST", r.Method)
		assert.Contains(t, r.URL.Path, "autocomplete")
		assert.Equal(t, "test-key", r.Header.Get("X-Goog-Api-Key"))
		assert.Contains(t, r.Header.Get("X-Goog-FieldMask"), "suggestions.placePrediction.placeId")

		resp := autocompleteResponse{
			Suggestions: []autocompleteSuggestion{
				{
					PlacePrediction: autocompletePlacePrediction{
						PlaceID: "ChIJDfiEnPufJkcRAAAA",
						StructuredFormat: &structFormat{
							MainText:      displayNameText{Text: "Hell's Gate"},
							SecondaryText: displayNameText{Text: "Naivasha, Kenya"},
						},
						Types: []string{"national_park", "tourist_attraction"},
					},
				},
			},
		}
		json.NewEncoder(w).Encode(resp)
	}))
	defer ts.Close()

	c := &Client{
		apiKey:  "test-key",
		http:    ts.Client(),
		baseURL: ts.URL + "/",
	}

	results, err := c.Autocomplete(context.Background(), "hells gate", "session-123")
	require.NoError(t, err)
	require.Len(t, results, 1)

	assert.Equal(t, "ChIJDfiEnPufJkcRAAAA", results[0].PlaceID)
	assert.Equal(t, "Hell's Gate", results[0].DisplayName)
	assert.Equal(t, "Naivasha, Kenya", results[0].FormattedAddress)
	assert.Equal(t, "🏕️ National Park", results[0].TypeChip)
}

func TestAutocompleteError(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		w.Write([]byte(`{"error": {"message": "internal"}}`))
	}))
	defer ts.Close()

	c := &Client{apiKey: "test-key", http: ts.Client(), baseURL: ts.URL + "/"}

	_, err := c.Autocomplete(context.Background(), "test", "session-1")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "max retries exceeded")
}

func TestTextSearch(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "POST", r.Method)
		assert.Contains(t, r.URL.Path, "searchText")

		resp := textSearchResponse{
			Places: []textSearchPlace{
				{
					ID:               "ChIJDfiEnPufJkcRAAAA",
					DisplayName:      displayName{Text: "Hell's Gate National Park", LanguageCode: "en"},
					FormattedAddress: "Naivasha, Kenya",
					Types:            []string{"national_park"},
					Rating:           4.5,
					UserRatingCount:  1200,
				},
			},
		}
		json.NewEncoder(w).Encode(resp)
	}))
	defer ts.Close()

	c := &Client{apiKey: "test-key", http: ts.Client(), baseURL: ts.URL + "/"}

	results, err := c.TextSearch(context.Background(), "hells gate")
	require.NoError(t, err)
	require.Len(t, results, 1)

	assert.Equal(t, "Hell's Gate National Park", results[0].DisplayName)
	assert.Equal(t, "Naivasha, Kenya", results[0].FormattedAddress)
	assert.Equal(t, 4.5, results[0].Rating)
	assert.Equal(t, int32(1200), results[0].UserRatingCount)
}

func TestPlaceDetails(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "GET", r.Method)
		assert.Contains(t, r.URL.Path, "ChIJDfiEnPufJkcRAAAA")

		resp := placeDetailsResponse{
			ID:               "ChIJDfiEnPufJkcRAAAA",
			DisplayName:      displayName{Text: "Hell's Gate National Park", LanguageCode: "en"},
			FormattedAddress: "Naivasha, Kenya",
			Types:            []string{"national_park", "tourist_attraction"},
			GoogleMapsURI:    "https://maps.google.com/?q=Hell%27s+Gate",
		}
		json.NewEncoder(w).Encode(resp)
	}))
	defer ts.Close()

	c := &Client{apiKey: "test-key", http: ts.Client(), baseURL: ts.URL + "/"}

	result, err := c.PlaceDetails(context.Background(), "ChIJDfiEnPufJkcRAAAA", "")
	require.NoError(t, err)

	assert.Equal(t, "Hell's Gate National Park", result.DisplayName)
	assert.Equal(t, "Naivasha, Kenya", result.FormattedAddress)
	assert.Contains(t, result.Types, "national_park")
}

func TestNearbySearch(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "POST", r.Method)
		assert.Contains(t, r.URL.Path, "searchNearby")

		resp := NearbySearchResult{
			Places: []NearbyPlace{
				{PlaceID: "ChIJ1", DisplayName: DisplayName{Text: "Place One"}, FormattedAddress: "Nairobi"},
				{PlaceID: "ChIJ2", DisplayName: DisplayName{Text: "Place Two"}, FormattedAddress: "Mombasa"},
			},
		}
		json.NewEncoder(w).Encode(resp)
	}))
	defer ts.Close()

	c := &Client{apiKey: "test-key", http: ts.Client(), baseURL: ts.URL + "/"}

	results, err := c.NearbySearch(context.Background(), 0, 0)
	require.NoError(t, err)
	require.Len(t, results, 2)

	assert.Equal(t, "ChIJ1", results[0].PlaceID)
	assert.Equal(t, "Place One", results[0].GetDisplayName())
	assert.Equal(t, "Nairobi", results[0].FormattedAddress)
}

func TestTypeChip(t *testing.T) {
	tests := []struct {
		types []string
		want  string
	}{
		{[]string{"national_park", "tourist_attraction"}, "🏕️ National Park"},
		{[]string{"museum"}, "🏛️ Museum"},
		{[]string{"restaurant", "cafe"}, "🍽️ Restaurant"},
		{[]string{"beach", "natural_feature"}, "🏖️ Beach"},
		{[]string{"unknown_type"}, "📍 unknown_type"},
		{nil, "📍 Place"},
		{[]string{}, "📍 Place"},
	}

	for _, tt := range tests {
		t.Run(tt.want, func(t *testing.T) {
			got := TypeChip(tt.types)
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestNearbySearchWithOptions(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "POST", r.Method)
		assert.Contains(t, r.URL.Path, "searchNearby")
		assert.Contains(t, r.Header.Get("X-Goog-FieldMask"), "places.rating")
		assert.Contains(t, r.Header.Get("X-Goog-FieldMask"), "places.photos")

		var req NearbySearchRequest
		require.NoError(t, json.NewDecoder(r.Body).Decode(&req))
		assert.Equal(t, 17, req.MaxResultCount)
		assert.Equal(t, 42500.0, req.LocationRestriction.Circle.Radius)
		assert.Equal(t, -1.25, req.LocationRestriction.Circle.Center.Latitude)
		assert.Equal(t, 36.75, req.LocationRestriction.Circle.Center.Longitude)
		assert.Contains(t, req.IncludedTypes, "national_park")
		assert.Contains(t, req.ExcludedTypes, "church")

		json.NewEncoder(w).Encode(NearbySearchResult{
			Places: []NearbyPlace{{
				PlaceID:         "ChIJ-options",
				DisplayName:     DisplayName{Text: "Options Park"},
				Rating:          4.7,
				UserRatingCount: 240,
				PriceLevel:      "PRICE_LEVEL_MODERATE",
				Photos: []Photo{{
					Name: "places/ChIJ-options/photos/photo-1",
					AuthorAttributions: []AuthorAttribution{{
						DisplayName: "Test Photographer",
					}},
				}},
				CurrentOpeningHours: &OpeningHours{
					WeekdayDescriptions: []string{"Monday: 6:00 AM – 6:00 PM"},
				},
			}},
		})
	}))
	defer ts.Close()

	c := &Client{apiKey: "test-key", http: ts.Client(), baseURL: ts.URL + "/"}
	results, err := c.NearbySearchWithOptions(context.Background(), NearbySearchOptions{
		Center:         LatLng{Latitude: -1.25, Longitude: 36.75},
		RadiusMeters:   42500,
		MaxResultCount: 17,
		IncludedTypes:  []string{"national_park"},
		ExcludedTypes:  []string{"church"},
	})
	require.NoError(t, err)
	require.Len(t, results, 1)
	assert.Equal(t, "Options Park", results[0].GetDisplayName())
	assert.Equal(t, 4.7, results[0].Rating)
	assert.Equal(t, int32(240), results[0].UserRatingCount)
	assert.Equal(t, "PRICE_LEVEL_MODERATE", results[0].PriceLevel)
	require.Len(t, results[0].Photos, 1)
	assert.Equal(t, "Test Photographer", results[0].Photos[0].AuthorAttributions[0].DisplayName)
	require.NotNil(t, results[0].CurrentOpeningHours)
}

func TestTextSearchWithOptions(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req textSearchRequest
		require.NoError(t, json.NewDecoder(r.Body).Decode(&req))
		assert.Equal(t, "lake", req.TextQuery)
		assert.Equal(t, 12, req.PageSize)
		assert.Equal(t, "next-page", req.PageToken)
		require.NotNil(t, req.LocationBias)
		require.NotNil(t, req.LocationBias.Circle)
		assert.Equal(t, 2.5, req.LocationBias.Circle.Center.Latitude)
		assert.Equal(t, 36.0, req.LocationBias.Circle.Center.Longitude)
		assert.Equal(t, 30000.0, req.LocationBias.Circle.Radius)

		json.NewEncoder(w).Encode(textSearchResponse{
			Places:        []textSearchPlace{{ID: "ChIJ-text", DisplayName: displayName{Text: "Text Lake"}}},
			NextPageToken: "next-page-2",
		})
	}))
	defer ts.Close()

	c := &Client{apiKey: "test-key", http: ts.Client(), baseURL: ts.URL + "/"}
	results, next, err := c.TextSearchWithOptions(context.Background(), TextSearchOptions{
		Query:          "lake",
		Center:         &LatLng{Latitude: 2.5, Longitude: 36.0},
		RadiusMeters:   30000,
		MaxResultCount: 12,
		PageToken:      "next-page",
	})
	require.NoError(t, err)
	require.Len(t, results, 1)
	assert.Equal(t, "Text Lake", results[0].DisplayName)
	assert.Equal(t, "next-page-2", next)
}

func TestPhotoMedia(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Contains(t, r.URL.Path, "places/ChIJ-photo/photos/photo-1/media")
		assert.Equal(t, "800", r.URL.Query().Get("maxWidthPx"))
		assert.Equal(t, "test-key", r.URL.Query().Get("key"))
		w.Header().Set("Content-Type", "image/jpeg")
		w.Write([]byte("fake-jpeg"))
	}))
	defer ts.Close()

	c := &Client{apiKey: "test-key", http: ts.Client(), baseURL: ts.URL + "/"}
	body, contentType, err := c.PhotoMedia(context.Background(),
		"places/ChIJ-photo/photos/photo-1", 800)
	require.NoError(t, err)
	assert.Equal(t, "image/jpeg", contentType)
	assert.Equal(t, []byte("fake-jpeg"), body)
}

func TestPhotoMediaRejectsInvalidRef(t *testing.T) {
	c := &Client{apiKey: "test-key", http: http.DefaultClient, baseURL: "https://example.com/"}
	for _, ref := range []string{
		"",
		"places/../../secret/photos/abc",
		"https://evil.example/photo",
		"places/ChIJ/photos/../../etc/passwd",
	} {
		_, _, err := c.PhotoMedia(context.Background(), ref, 800)
		require.Error(t, err, ref)
	}
}
