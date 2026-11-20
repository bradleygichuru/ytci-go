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
	PageSize     int           `json:"pageSize,omitempty"`
	PageToken    string        `json:"pageToken,omitempty"`
}

type textSearchResponse struct {
	Places        []textSearchPlace `json:"places"`
	NextPageToken string            `json:"nextPageToken,omitempty"`
}

// TextSearchOptions configures a text search with an explicit bias instead of
// the historical hard-coded Nairobi centre.
type TextSearchOptions struct {
	Query          string
	Center         *LatLng
	RadiusMeters   float64
	MaxResultCount int
	PageToken      string
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
	results, _, err := c.TextSearchWithOptions(ctx, TextSearchOptions{
		Query:        query,
		Center:       &LatLng{Latitude: 0.0236, Longitude: 36.8888},
		RadiusMeters: 50000,
	})
	return results, err
}

// TextSearchWithOptions returns one page of results; use PageToken with the
// returned token for the next page. The client method historically returned
// only results, so keep this page-shaped companion explicit.
func (c *Client) TextSearchWithOptions(ctx context.Context, opts TextSearchOptions) ([]TextSearchResult, string, error) {
	req := textSearchRequest{
		TextQuery: opts.Query,
		PageSize:  opts.MaxResultCount,
		PageToken: opts.PageToken,
	}
	if opts.Center != nil {
		radius := opts.RadiusMeters
		if radius <= 0 {
			radius = 50000
		}
		if radius > 50000 {
			radius = 50000
		}
		req.LocationBias = &locationBias{
			Circle: &circle{
				Center: *opts.Center,
				Radius: radius,
			},
		}
	}

	respBytes, err := c.post(ctx, "places:searchText", req, textSearchFieldMask)
	if err != nil {
		return nil, "", fmt.Errorf("places: text search: %w", err)
	}

	var resp textSearchResponse
	if err := json.Unmarshal(respBytes, &resp); err != nil {
		return nil, "", fmt.Errorf("places: text search unmarshal: %w", err)
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

	return results, resp.NextPageToken, nil
}
