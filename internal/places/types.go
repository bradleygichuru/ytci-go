package places

import (
	"fmt"
)

type LatLng struct {
	Latitude  float64 `json:"latitude"`
	Longitude float64 `json:"longitude"`
}

type AutocompleteRequest struct {
	Input        string `json:"input"`
	SessionToken string `json:"sessionToken,omitempty"`
}

type AutocompleteSuggestion struct {
	PlaceID          string  `json:"placeId"`
	DisplayName      string  `json:"displayName"`
	FormattedAddress string  `json:"formattedAddress"`
	Location         *LatLng `json:"location,omitempty"`
	Types            []string `json:"types,omitempty"`
	TypeChip         string  `json:"typeChip"`
}

type TextSearchRequest struct {
	TextQuery string `json:"textQuery"`
}

type TextSearchResult struct {
	PlaceID          string  `json:"placeId"`
	DisplayName      string  `json:"displayName"`
	FormattedAddress string  `json:"formattedAddress"`
	Location         *LatLng `json:"location,omitempty"`
	Types            []string `json:"types,omitempty"`
	Rating           float64 `json:"rating,omitempty"`
	UserRatingCount  int32   `json:"userRatingCount,omitempty"`
}

type AccessibilityOptions struct {
	WheelchairAccessibleParking  *bool `json:"wheelchairAccessibleParking,omitempty"`
	WheelchairAccessibleEntrance *bool `json:"wheelchairAccessibleEntrance,omitempty"`
	WheelchairAccessibleRestroom *bool `json:"wheelchairAccessibleRestroom,omitempty"`
	WheelchairAccessibleSeating  *bool `json:"wheelchairAccessibleSeating,omitempty"`
}

type ContainingPlace struct {
	Name string `json:"name"`
	ID   string `json:"id"`
}

type SubDestination struct {
	Name string `json:"name"`
	ID   string `json:"id"`
}

type PlaceDetails struct {
	PlaceID             string                `json:"placeId"`
	DisplayName         string                `json:"displayName"`
	FormattedAddress    string                `json:"formattedAddress"`
	Location            *LatLng               `json:"location,omitempty"`
	Types               []string              `json:"types,omitempty"`
	GoogleMapsURI       string                `json:"googleMapsUri,omitempty"`
	NationalPhoneNumber string                `json:"nationalPhoneNumber,omitempty"`
	WebsiteURI          string                `json:"websiteUri,omitempty"`
	AccessibilityOptions *AccessibilityOptions `json:"accessibilityOptions,omitempty"`
	ContainingPlaces    []ContainingPlace     `json:"containingPlaces,omitempty"`
	SubDestinations     []SubDestination      `json:"subDestinations,omitempty"`
}

type NearbySearchRequest struct {
	LocationRestriction *LocationRestriction `json:"locationRestriction"`
	MaxResultCount      int                  `json:"maxResultCount"`
}

type LocationRestriction struct {
	Circle *Circle `json:"circle"`
}

type Circle struct {
	Center *LatLng `json:"center"`
	Radius float64 `json:"radius"`
}

type NearbySearchResult struct {
	Places []NearbyPlace `json:"places"`
}

type NearbyPlace struct {
	PlaceID          string `json:"id"`
	DisplayName      DisplayName `json:"displayName"`
	FormattedAddress string `json:"formattedAddress"`
}

type DisplayName struct {
	Text string `json:"text"`
}

func (np NearbyPlace) GetPlaceID() string {
	return np.PlaceID
}

func (np NearbyPlace) GetDisplayName() string {
	return np.DisplayName.Text
}

var typeLabelMap = map[string]string{
	"national_park":               "🏕️ National Park",
	"park":                        "🏕️ Park",
	"museum":                      "🏛️ Museum",
	"art_gallery":                 "🏛️ Art Gallery",
	"hotel":                       "🏨 Hotel",
	"lodging":                     "🏨 Lodging",
	"restaurant":                  "🍽️ Restaurant",
	"cafe":                        "☕ Cafe",
	"bar":                         "🍸 Bar",
	"natural_feature":             "🌿 Nature",
	"beach":                       "🏖️ Beach",
	"lake":                        "🏞️ Lake",
	"mountain":                    "⛰️ Mountain",
	"hiking_area":                 "🥾 Hiking",
	"tourist_attraction":          "📍 Attraction",
	"point_of_interest":           "📍 Place",
	"historical_landmark":         "🏛️ Landmark",
	"shopping_mall":               "🛍️ Shopping",
	"zoo":                         "🦁 Zoo",
	"aquarium":                    "🐠 Aquarium",
	"amusement_park":              "🎢 Amusement Park",
	"place_of_worship":            "⛪ Worship",
	"university":                  "🎓 University",
	"airport":                     "✈️ Airport",
	"train_station":               "🚂 Station",
	"bus_station":                 "🚌 Bus Station",
	"stadium":                     "🏟️ Stadium",
	"night_club":                  "🎵 Night Club",
	"store":                       "🏪 Store",
	"market":                      "🏪 Market",
	"campground":                  "🏕️ Campground",
	"rv_park":                     "🚐 RV Park",
	"tourist_information_center":  "ℹ️ Tourist Info",
	"visitor_center":              "ℹ️ Visitor Center",
}

func TypeChip(types []string) string {
	for _, t := range types {
		if label, ok := typeLabelMap[t]; ok {
			return label
		}
	}
	if len(types) > 0 {
		return fmt.Sprintf("📍 %s", types[0])
	}
	return "📍 Place"
}

func AllTypeChips(types []string) []string {
	seen := make(map[string]bool)
	chips := make([]string, 0, len(types))
	for _, t := range types {
		if seen[t] {
			continue
		}
		seen[t] = true
		if label, ok := typeLabelMap[t]; ok {
			chips = append(chips, label)
		}
	}
	if len(chips) == 0 && len(types) > 0 {
		chips = append(chips, fmt.Sprintf("📍 %s", types[0]))
	}
	return chips
}
