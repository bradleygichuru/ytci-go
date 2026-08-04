package places

import (
	"context"
	"encoding/json"
	"fmt"
)

const nearbySearchFieldMask = "places.id,places.displayName,places.formattedAddress"

func (c *Client) NearbySearch(ctx context.Context) ([]NearbyPlace, error) {
	req := NearbySearchRequest{
		MaxResultCount: 20,
		LocationRestriction: &LocationRestriction{
			Circle: &Circle{
				Center: &LatLng{Latitude: 0.0236, Longitude: 36.8888},
				Radius: 500000,
			},
		},
	}

	respBytes, err := c.post(ctx, "places:searchNearby", req, nearbySearchFieldMask)
	if err != nil {
		return nil, fmt.Errorf("places: nearby search: %w", err)
	}

	var resp NearbySearchResult
	if err := json.Unmarshal(respBytes, &resp); err != nil {
		return nil, fmt.Errorf("places: nearby search unmarshal: %w", err)
	}

	return resp.Places, nil
}
