package places

import (
	"context"
	"encoding/json"
	"fmt"
)

const textSearchFieldMask = "places.id,places.displayName,places.formattedAddress,places.location,places.types,places.rating,places.userRatingCount"

type textSearchRequest struct {
	TextQuery    string        `json:"textQuery"`
	LocationBias *locationBias `json:"locationBias,omitempty"`
}

type textSearchResponse struct {
	Places []textSearchPlace `json:"places"`
}

type textSearchPlace struct {
	ID               string       `json:"id"`
	DisplayName      displayName  `json:"displayName"`
	FormattedAddress string       `json:"formattedAddress"`
	Location         *rawLocation `json:"location,omitempty"`
	Types            []string     `json:"types,omitempty"`
	Rating           float64      `json:"rating,omitempty"`
	UserRatingCount  int32        `json:"userRatingCount,omitempty"`
}

type displayName struct {
	Text         string `json:"text"`
	LanguageCode string `json:"languageCode,omitempty"`
}

func (c *Client) TextSearch(ctx context.Context, query string) ([]TextSearchResult, error) {
	req := textSearchRequest{
		TextQuery: query,
		LocationBias: &locationBias{
			Circle: &circle{
				Center: LatLng{Latitude: 0.0236, Longitude: 36.8888},
				Radius: 500000,
			},
		},
	}

	respBytes, err := c.post(ctx, "places:searchText", req, textSearchFieldMask)
	if err != nil {
		return nil, fmt.Errorf("places: text search: %w", err)
	}

	var resp textSearchResponse
	if err := json.Unmarshal(respBytes, &resp); err != nil {
		return nil, fmt.Errorf("places: text search unmarshal: %w", err)
	}

	results := make([]TextSearchResult, 0, len(resp.Places))
	for _, p := range resp.Places {
		r := TextSearchResult{
			PlaceID:          p.ID,
			DisplayName:      p.DisplayName.Text,
			FormattedAddress: p.FormattedAddress,
			Types:            p.Types,
			Rating:           p.Rating,
			UserRatingCount:  p.UserRatingCount,
		}
		if p.Location != nil {
			r.Location = &LatLng{
				Latitude:  p.Location.Latitude,
				Longitude: p.Location.Longitude,
			}
		}
		results = append(results, r)
	}

	return results, nil
}
