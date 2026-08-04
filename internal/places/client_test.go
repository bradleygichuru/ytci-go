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

	results, err := c.NearbySearch(context.Background())
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
