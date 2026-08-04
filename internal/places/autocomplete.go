package places

import (
	"context"
	"encoding/json"
	"fmt"
)

const autocompleteFieldMask = "places.id,places.displayName,places.formattedAddress,places.location,places.types"

type autocompleteRequest struct {
	Input           string             `json:"input"`
	SessionToken    string             `json:"sessionToken,omitempty"`
	LocationBias    *locationBias      `json:"locationBias,omitempty"`
}

type locationBias struct {
	Circle *circle `json:"circle"`
}

type circle struct {
	Center LatLng `json:"center"`
	Radius float64 `json:"radius"`
}

type autocompleteResponse struct {
	Suggestions []autocompleteSuggestion `json:"suggestions"`
}

type autocompleteSuggestion struct {
	PlacePrediction autocompletePlacePrediction `json:"placePrediction"`
}

type autocompletePlacePrediction struct {
	PlaceID          string        `json:"placeId"`
	StructuredFormat *structFormat `json:"structuredFormat,omitempty"`
	Text             *textFormat   `json:"text,omitempty"`
	Types            []string      `json:"types,omitempty"`
	Location         *rawLocation  `json:"location,omitempty"`
}

type structFormat struct {
	MainText      displayNameText `json:"mainText"`
	SecondaryText displayNameText `json:"secondaryText"`
}

type displayNameText struct {
	Text string `json:"text"`
}

type textFormat struct {
	Text string `json:"text"`
}

type rawLocation struct {
	Latitude  float64 `json:"latitude"`
	Longitude float64 `json:"longitude"`
}

func (c *Client) Autocomplete(ctx context.Context, input, sessionToken string) ([]AutocompleteSuggestion, error) {
	req := autocompleteRequest{
		Input:        input,
		SessionToken: sessionToken,
		LocationBias: &locationBias{
			Circle: &circle{
				Center: LatLng{Latitude: 0.0236, Longitude: 36.8888},
				Radius: 500000,
			},
		},
	}

	respBytes, err := c.post(ctx, "places:autocomplete", req, autocompleteFieldMask)
	if err != nil {
		return nil, fmt.Errorf("places: autocomplete: %w", err)
	}

	var resp autocompleteResponse
	if err := json.Unmarshal(respBytes, &resp); err != nil {
		return nil, fmt.Errorf("places: autocomplete unmarshal: %w", err)
	}

	results := make([]AutocompleteSuggestion, 0, len(resp.Suggestions))
	for _, s := range resp.Suggestions {
		r := AutocompleteSuggestion{
			PlaceID: s.PlacePrediction.PlaceID,
			Types:   s.PlacePrediction.Types,
		}
		if s.PlacePrediction.StructuredFormat != nil {
			r.DisplayName = s.PlacePrediction.StructuredFormat.MainText.Text
			r.FormattedAddress = s.PlacePrediction.StructuredFormat.SecondaryText.Text
		} else if s.PlacePrediction.Text != nil {
			r.DisplayName = s.PlacePrediction.Text.Text
		}
		if s.PlacePrediction.Location != nil {
			r.Location = &LatLng{
				Latitude:  s.PlacePrediction.Location.Latitude,
				Longitude: s.PlacePrediction.Location.Longitude,
			}
		}
		r.TypeChip = TypeChip(r.Types)
		results = append(results, r)
	}

	return results, nil
}
