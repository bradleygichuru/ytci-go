package places

import (
	"context"
	"encoding/json"
	"fmt"
)

const nearbySearchFieldMask = "places.id,places.displayName,places.formattedAddress,places.location,places.types,places.rating,places.userRatingCount,places.priceLevel,places.photos,places.currentOpeningHours,places.businessStatus"

var includedTouristTypes = []string{
	"tourist_attraction",
	"museum",
	"art_gallery",
	"art_museum",
	"history_museum",
	"historical_landmark",
	"historical_place",
	"monument",
	"cultural_landmark",
	"castle",
	"performing_arts_theater",
	"concert_hall",
	"opera_house",
	"auditorium",
	"national_park",
	"state_park",
	"park",
	"city_park",
	"botanical_garden",
	"garden",
	"hiking_area",
	"nature_preserve",
	"wildlife_park",
	"wildlife_refuge",
	"zoo",
	"aquarium",
	"beach",
	"lake",
	"mountain_peak",
	"scenic_spot",
	"marina",
	"vineyard",
	"woods",
	"amusement_park",
	"water_park",
	"stadium",
	"arena",
	"planetarium",
	"observation_deck",
	"visitor_center",
	"tourist_information_center",
	"plaza",
	"church",
	"synagogue",
	"mosque",
	"hindu_temple",
	"buddhist_temple",
	"shinto_shrine",
}

var excludedNonTouristTypes = []string{
	"apartment_building",
	"apartment_complex",
	"condominium_complex",
	"housing_complex",
	"grocery_store",
	"supermarket",
	"discount_supermarket",
	"convenience_store",
	"shopping_mall",
	"department_store",
	"store",
	"school",
	"preschool",
	"primary_school",
	"secondary_school",
	"university",
	"hospital",
	"general_hospital",
	"pharmacy",
	"bank",
	"atm",
	"gas_station",
	"parking",
	"parking_garage",
	"parking_lot",
	"post_office",
	"fire_station",
	"police",
	"courthouse",
	"city_hall",
	"embassy",
	"government_office",
	"local_government_office",
	"car_repair",
	"car_wash",
	"car_dealer",
	"laundry",
	"hair_salon",
	"beauty_salon",
	"barber_shop",
	"nail_salon",
	"florist",
	"funeral_home",
	"cemetery",
	"storage",
}

func IsTouristPlace(types []string) bool {
	for _, t := range types {
		for _, inc := range includedTouristTypes {
			if t == inc {
				return true
			}
		}
	}
	return false
}

// NearbySearchOptions configures a nearby search. Radius is capped at 50 km by
// Google; large counties should be searched from multiple points.
type NearbySearchOptions struct {
	Center         LatLng
	RadiusMeters   float64
	MaxResultCount int
	IncludedTypes  []string
	ExcludedTypes  []string
}

func (c *Client) NearbySearch(ctx context.Context, lat, lng float64) ([]NearbyPlace, error) {
	return c.NearbySearchWithOptions(ctx, NearbySearchOptions{
		Center:         LatLng{Latitude: lat, Longitude: lng},
		RadiusMeters:   10000,
		MaxResultCount: 20,
		IncludedTypes:  includedTouristTypes,
		ExcludedTypes:  excludedNonTouristTypes,
	})
}

func (c *Client) NearbySearchWithOptions(ctx context.Context, opts NearbySearchOptions) ([]NearbyPlace, error) {
	if opts.RadiusMeters <= 0 {
		opts.RadiusMeters = 10000
	}
	if opts.RadiusMeters > 50000 {
		opts.RadiusMeters = 50000
	}
	if opts.MaxResultCount <= 0 {
		opts.MaxResultCount = 20
	}
	if opts.MaxResultCount > 20 {
		opts.MaxResultCount = 20
	}
	if len(opts.IncludedTypes) == 0 {
		opts.IncludedTypes = includedTouristTypes
	}
	if len(opts.ExcludedTypes) == 0 {
		opts.ExcludedTypes = excludedNonTouristTypes
	}

	req := NearbySearchRequest{
		MaxResultCount: opts.MaxResultCount,
		IncludedTypes:  opts.IncludedTypes,
		ExcludedTypes:  opts.ExcludedTypes,
		LocationRestriction: &LocationRestriction{
			Circle: &Circle{
				Center: &LatLng{
					Latitude:  opts.Center.Latitude,
					Longitude: opts.Center.Longitude,
				},
				Radius: opts.RadiusMeters,
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
