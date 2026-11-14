package catalog

import "testing"

func TestClassifyGoogleTypesMapsControlledCategories(t *testing.T) {
	tests := []struct {
		name     string
		types    []string
		category Category
		reject   bool
	}{
		{name: "national park", types: []string{"national_park", "tourist_attraction"}, category: CategoryWildlife},
		{name: "beach", types: []string{"beach"}, category: CategoryBeach},
		{name: "hiking", types: []string{"hiking_area"}, category: CategoryHiking},
		{name: "museum", types: []string{"museum", "point_of_interest"}, category: CategoryCulture},
		{name: "restaurant", types: []string{"restaurant", "food"}, category: CategoryFood},
		{name: "golf", types: []string{"golf_course"}, category: CategoryActivity},
		{name: "attraction", types: []string{"tourist_attraction"}, category: CategorySightseeing},
		{name: "conservation", types: []string{"conservation"}, category: CategoryConservation},
		{name: "church", types: []string{"church", "tourist_attraction"}, reject: true},
		{name: "mosque", types: []string{"mosque", "place_of_worship"}, reject: true},
		{name: "coworking", types: []string{"coworking_space"}, reject: true},
		{name: "office", types: []string{"corporate_office"}, reject: true},
		{name: "administrative area", types: []string{"administrative_area_level_2"}, reject: true},
		{name: "premises", types: []string{"premises"}, reject: true},
		{name: "lodging only", types: []string{"lodging"}, reject: true},
		{name: "unknown", types: []string{"establishment"}, reject: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := ClassifyGoogleTypes(tt.types)
			if got.IsDestination == tt.reject {
				t.Fatalf("ClassifyGoogleTypes(%v) IsDestination = %v, want reject=%v (%s)",
					tt.types, got.IsDestination, tt.reject, got.Reason)
			}
			if !tt.reject && got.Category != tt.category {
				t.Fatalf("ClassifyGoogleTypes(%v) category = %q, want %q", tt.types, got.Category, tt.category)
			}
			if !tt.reject && len(got.Interests) == 0 {
				t.Fatalf("ClassifyGoogleTypes(%v) returned no interests", tt.types)
			}
		})
	}
}

func TestInterestTagsAlwaysIncludePrimary(t *testing.T) {
	for _, category := range AllCategories {
		tags := InterestTags(Category(category))
		if len(tags) == 0 || tags[0] != category {
			t.Fatalf("InterestTags(%q) = %v, want primary first", category, tags)
		}
	}
}

func TestLegacyCategoryMapsAdminSpellings(t *testing.T) {
	tests := map[string]Category{
		"attraction":         CategorySightseeing,
		"nature":             CategoryWildlife,
		"wildlife":           CategoryWildlife,
		"museum":             CategoryCulture,
		"adventure":          CategoryActivity,
		"beach":              CategoryBeach,
		"conservation":       CategoryConservation,
		"Wildlife":           CategoryWildlife,
		"tourist_attraction": CategorySightseeing,
	}
	for input, want := range tests {
		got, ok := LegacyCategory(input)
		if !ok || got != want {
			t.Fatalf("LegacyCategory(%q) = (%q, %v), want (%q, true)", input, got, ok, want)
		}
	}

	for _, rejected := range []string{"church", "coworking_space", "premises", "unknown-kind", ""} {
		if got, ok := LegacyCategory(rejected); ok {
			t.Fatalf("LegacyCategory(%q) = (%q, true), want rejected", rejected, got)
		}
	}
}

func TestIsCategory(t *testing.T) {
	for _, category := range AllCategories {
		if !IsCategory(category) {
			t.Fatalf("IsCategory(%q) = false", category)
		}
	}
	if IsCategory("wildlife") {
		t.Fatal("IsCategory must be case-sensitive")
	}
	if IsCategory("church") {
		t.Fatal("church is not in the controlled taxonomy")
	}
}
