package places

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
)

const placeDetailsFieldMask = "id,displayName,formattedAddress,location,types,googleMapsUri,nationalPhoneNumber,websiteUri"

type placeDetailsResponse struct {
	ID                  string       `json:"id"`
	DisplayName         displayName  `json:"displayName"`
	FormattedAddress    string       `json:"formattedAddress"`
	Location            *rawLocation `json:"location,omitempty"`
	Types               []string     `json:"types,omitempty"`
	GoogleMapsURI       string       `json:"googleMapsUri,omitempty"`
	NationalPhoneNumber string       `json:"nationalPhoneNumber,omitempty"`
	WebsiteURI          string       `json:"websiteUri,omitempty"`
}

func (c *Client) PlaceDetails(ctx context.Context, placeID, sessionToken string) (*PlaceDetails, error) {
	path := fmt.Sprintf("places/%s", url.PathEscape(placeID))
	if sessionToken != "" {
		path += "?sessionToken=" + url.QueryEscape(sessionToken)
	}

	respBytes, err := c.get(ctx, path, placeDetailsFieldMask)
	if err != nil {
		return nil, fmt.Errorf("places: place details: %w", err)
	}

	var resp placeDetailsResponse
	if err := json.Unmarshal(respBytes, &resp); err != nil {
		return nil, fmt.Errorf("places: place details unmarshal: %w", err)
	}

	result := &PlaceDetails{
		PlaceID:             resp.ID,
		DisplayName:         resp.DisplayName.Text,
		FormattedAddress:    resp.FormattedAddress,
		Types:               resp.Types,
		GoogleMapsURI:       resp.GoogleMapsURI,
		NationalPhoneNumber: resp.NationalPhoneNumber,
		WebsiteURI:          resp.WebsiteURI,
	}
	if resp.Location != nil {
		result.Location = &LatLng{
			Latitude:  resp.Location.Latitude,
			Longitude: resp.Location.Longitude,
		}
	}

	return result, nil
}
