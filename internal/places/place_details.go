package places

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
)

const placeDetailsFieldMask = "id,displayName,formattedAddress,location,types,googleMapsUri,nationalPhoneNumber,websiteUri,accessibilityOptions,containingPlaces,subDestinations,rating,userRatingCount,currentOpeningHours,primaryTypeDisplayName"

type accessibilityOptions struct {
	WheelchairAccessibleParking  *bool `json:"wheelchairAccessibleParking,omitempty"`
	WheelchairAccessibleEntrance *bool `json:"wheelchairAccessibleEntrance,omitempty"`
	WheelchairAccessibleRestroom *bool `json:"wheelchairAccessibleRestroom,omitempty"`
	WheelchairAccessibleSeating  *bool `json:"wheelchairAccessibleSeating,omitempty"`
}

type containingPlace struct {
	Name string `json:"name"`
	ID   string `json:"id"`
}

type subDestination struct {
	Name string `json:"name"`
	ID   string `json:"id"`
}

type placeDetailsResponse struct {
	ID                     string                `json:"id"`
	DisplayName            displayName           `json:"displayName"`
	FormattedAddress       string                `json:"formattedAddress"`
	Location               *rawLocation          `json:"location,omitempty"`
	Types                  []string              `json:"types,omitempty"`
	GoogleMapsURI          string                `json:"googleMapsUri,omitempty"`
	NationalPhoneNumber    string                `json:"nationalPhoneNumber,omitempty"`
	WebsiteURI             string                `json:"websiteUri,omitempty"`
	AccessibilityOptions   *accessibilityOptions `json:"accessibilityOptions,omitempty"`
	ContainingPlaces       []containingPlace     `json:"containingPlaces,omitempty"`
	SubDestinations        []subDestination      `json:"subDestinations,omitempty"`
	Rating                 float64               `json:"rating,omitempty"`
	UserRatingCount        int32                 `json:"userRatingCount,omitempty"`
	CurrentOpeningHours    *OpeningHours         `json:"currentOpeningHours,omitempty"`
	PrimaryTypeDisplayName string                `json:"primaryTypeDisplayName,omitempty"`
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
		PlaceID:              resp.ID,
		DisplayName:          resp.DisplayName.Text,
		FormattedAddress:     resp.FormattedAddress,
		Types:                resp.Types,
		GoogleMapsURI:        resp.GoogleMapsURI,
		NationalPhoneNumber:  resp.NationalPhoneNumber,
		WebsiteURI:           resp.WebsiteURI,
		Rating:               resp.Rating,
		UserRatingCount:      resp.UserRatingCount,
		PrimaryTypeDisplayName: resp.PrimaryTypeDisplayName,
	}
	if resp.Location != nil {
		result.Location = &LatLng{
			Latitude:  resp.Location.Latitude,
			Longitude: resp.Location.Longitude,
		}
	}
	if resp.AccessibilityOptions != nil {
		result.AccessibilityOptions = &AccessibilityOptions{
			WheelchairAccessibleParking:  resp.AccessibilityOptions.WheelchairAccessibleParking,
			WheelchairAccessibleEntrance: resp.AccessibilityOptions.WheelchairAccessibleEntrance,
			WheelchairAccessibleRestroom: resp.AccessibilityOptions.WheelchairAccessibleRestroom,
			WheelchairAccessibleSeating:  resp.AccessibilityOptions.WheelchairAccessibleSeating,
		}
	}
	if len(resp.ContainingPlaces) > 0 {
		result.ContainingPlaces = make([]ContainingPlace, len(resp.ContainingPlaces))
		for i, cp := range resp.ContainingPlaces {
			result.ContainingPlaces[i] = ContainingPlace{Name: cp.Name, ID: cp.ID}
		}
	}
	if len(resp.SubDestinations) > 0 {
		result.SubDestinations = make([]SubDestination, len(resp.SubDestinations))
		for i, sd := range resp.SubDestinations {
			result.SubDestinations[i] = SubDestination{Name: sd.Name, ID: sd.ID}
		}
	}
	if resp.CurrentOpeningHours != nil {
		result.CurrentOpeningHours = resp.CurrentOpeningHours
	}

	return result, nil
}
