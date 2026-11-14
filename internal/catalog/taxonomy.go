package catalog

import "strings"

// Category is the controlled destination taxonomy. It is deliberately the
// same vocabulary the mobile app uses for itinerary stops (STOP_CATEGORIES),
// so a destination can become a stop without translation.
type Category string

const (
	CategoryActivity     Category = "Activity"
	CategoryWildlife     Category = "Wildlife"
	CategoryHiking       Category = "Hiking"
	CategoryPhotography  Category = "Photography"
	CategoryBeach        Category = "Beach"
	CategoryCulture      Category = "Culture"
	CategoryFood         Category = "Food"
	CategorySightseeing  Category = "Sightseeing"
	CategoryShopping     Category = "Shopping"
	CategoryTransport    Category = "Transport"
	CategoryRest         Category = "Rest"
	CategoryConservation Category = "Conservation"
)

// AllCategories is the canonical order used by admin forms and validation.
var AllCategories = []string{
	string(CategoryActivity),
	string(CategoryWildlife),
	string(CategoryHiking),
	string(CategoryPhotography),
	string(CategoryBeach),
	string(CategoryCulture),
	string(CategoryFood),
	string(CategorySightseeing),
	string(CategoryShopping),
	string(CategoryTransport),
	string(CategoryRest),
	string(CategoryConservation),
}

var categorySet = func() map[string]struct{} {
	out := make(map[string]struct{}, len(AllCategories))
	for _, c := range AllCategories {
		out[c] = struct{}{}
	}
	return out
}()

// IsCategory reports whether value is one of the controlled categories.
func IsCategory(value string) bool {
	_, ok := categorySet[value]
	return ok
}

// Classification is the result of mapping Google Places types onto the
// catalog taxonomy.
type Classification struct {
	Category      Category
	Interests     []string
	IsDestination bool
	Reason        string
}

type categoryRule struct {
	category Category
	types    []string
}

// Rules are ordered by specificity. The first matching type wins.
var categoryRules = []categoryRule{
	{CategoryWildlife, []string{
		"national_park", "state_park", "nature_preserve", "wildlife_park",
		"wildlife_refuge", "zoo", "aquarium", "botanical_garden", "garden",
		"woods", "lake", "mountain_peak", "scenic_spot", "vineyard", "park",
		"city_park",
	}},
	{CategoryBeach, []string{"beach", "marina"}},
	{CategoryHiking, []string{"hiking_area", "trail"}},
	{CategoryCulture, []string{
		"museum", "art_gallery", "art_museum", "history_museum",
		"historical_landmark", "historical_place", "monument",
		"cultural_landmark", "castle", "performing_arts_theater",
		"concert_hall", "opera_house", "auditorium", "planetarium",
		"visitor_center", "tourist_information_center", "plaza",
	}},
	{CategoryActivity, []string{
		"amusement_park", "water_park", "stadium", "arena", "golf_course",
		"adventure",
	}},
	{CategoryFood, []string{
		"restaurant", "cafe", "bar", "bakery", "food_court", "meal_takeaway",
		"meal_delivery", "winery", "brewery",
	}},
	{CategoryShopping, []string{"shopping_mall", "market"}},
	{CategoryRest, []string{"spa", "resort"}},
	{CategoryTransport, []string{
		"transit_station", "train_station", "airport", "bus_station",
		"ferry_terminal",
	}},
	{CategoryConservation, []string{"conservation", "wildlife_conservation"}},
	{CategorySightseeing, []string{"tourist_attraction", "observation_deck"}},
}

// hardRejectTypes are never destinations, even when Google also attaches a
// generic tourist type. This is what keeps churches, offices, and
// administrative areas out of the composer catalog.
var hardRejectTypes = map[string]string{
	"church":                      "place of worship",
	"mosque":                      "place of worship",
	"synagogue":                   "place of worship",
	"hindu_temple":                "place of worship",
	"buddhist_temple":             "place of worship",
	"shinto_shrine":               "place of worship",
	"place_of_worship":            "place of worship",
	"cemetery":                    "cemetery",
	"funeral_home":                "funeral home",
	"coworking_space":             "coworking space",
	"business_center":             "business centre",
	"office":                      "office",
	"corporate_office":            "office",
	"government_office":           "government office",
	"local_government_office":     "government office",
	"city_hall":                   "government office",
	"courthouse":                  "government office",
	"embassy":                     "government office",
	"administrative_area_level_1": "administrative area",
	"administrative_area_level_2": "administrative area",
	"locality":                    "administrative area",
	"political":                   "administrative area",
	"premises":                    "premises",
	"subpremise":                  "premises",
	"street_address":              "street address",
	"postal_code":                 "postal code",
	"plus_code":                   "plus code",
	"route":                       "road",
	"intersection":                "road",
}

// softRejectTypes describe real places that are not travel destinations on
// their own. They are rejected only when no destination rule matches.
var softRejectTypes = map[string]string{
	"lodging":           "accommodation",
	"hotel":             "accommodation",
	"motel":             "accommodation",
	"hostel":            "accommodation",
	"guest_house":       "accommodation",
	"bed_and_breakfast": "accommodation",
	"campground":        "accommodation",
	"rv_park":           "accommodation",
	"shopping_mall":     "shopping centre",
	"department_store":  "shop",
	"store":             "shop",
	"supermarket":       "shop",
	"grocery_store":     "shop",
	"convenience_store": "shop",
}

// ClassifyGoogleTypes maps Google Places types[] to the controlled taxonomy.
// It rejects non-destinations so no ingest path can silently add them.
func ClassifyGoogleTypes(types []string) Classification {
	normalized := normalizeTypes(types)

	for _, t := range normalized {
		if reason, ok := hardRejectTypes[t]; ok {
			return Classification{IsDestination: false, Reason: reason}
		}
	}

	for _, rule := range categoryRules {
		for _, t := range normalized {
			if contains(rule.types, t) {
				return Classification{
					Category:      rule.category,
					Interests:     InterestTags(rule.category),
					IsDestination: true,
				}
			}
		}
	}

	for _, t := range normalized {
		if reason, ok := softRejectTypes[t]; ok {
			return Classification{IsDestination: false, Reason: reason}
		}
	}

	return Classification{IsDestination: false, Reason: "not a recognised destination type"}
}

// LegacyCategory maps older admin/CSV spellings onto the controlled taxonomy.
// It reports false for non-destinations so import paths can skip them.
func LegacyCategory(value string) (Category, bool) {
	value = strings.TrimSpace(value)
	if value == "" {
		return "", false
	}
	if IsCategory(value) {
		return Category(value), true
	}

	switch strings.ToLower(value) {
	case "attraction", "sightseeing", "tourist":
		return CategorySightseeing, true
	case "nature", "wildlife", "park", "conservation_area":
		return CategoryWildlife, true
	case "culture", "cultural":
		return CategoryCulture, true
	case "food", "dining":
		return CategoryFood, true
	case "shopping":
		return CategoryShopping, true
	case "transport":
		return CategoryTransport, true
	case "rest":
		return CategoryRest, true
	case "activity", "adventure":
		return CategoryActivity, true
	case "hiking":
		return CategoryHiking, true
	case "photography":
		return CategoryPhotography, true
	case "beach":
		return CategoryBeach, true
	}

	classification := ClassifyGoogleTypes([]string{strings.ToLower(value)})
	if classification.IsDestination {
		return classification.Category, true
	}
	return "", false
}

// InterestTags returns the primary category plus the secondary interests the
// composer should consider.
func InterestTags(category Category) []string {
	tags := []string{string(category)}
	switch category {
	case CategoryWildlife:
		tags = append(tags, string(CategoryConservation))
	case CategoryBeach:
		tags = append(tags, string(CategoryRest))
	case CategoryHiking:
		tags = append(tags, string(CategoryActivity))
	case CategoryCulture:
		tags = append(tags, string(CategorySightseeing))
	case CategoryFood:
		tags = append(tags, string(CategoryCulture))
	case CategoryActivity:
		tags = append(tags, string(CategorySightseeing))
	case CategoryConservation:
		tags = append(tags, string(CategoryWildlife))
	}
	return tags
}

func normalizeTypes(types []string) []string {
	out := make([]string, 0, len(types))
	for _, t := range types {
		t = strings.ToLower(strings.TrimSpace(t))
		if t != "" {
			out = append(out, t)
		}
	}
	return out
}

func contains(list []string, value string) bool {
	for _, item := range list {
		if item == value {
			return true
		}
	}
	return false
}
