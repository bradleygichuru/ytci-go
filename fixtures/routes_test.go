package fixtures

import "testing"

func TestMatchPrefersLiteralOverWildcard(t *testing.T) {
	t.Parallel()

	cases := []struct {
		path    string
		base string
	}{
		{"/v1/admin/species/review", "species/admin-review"},
		{"/v1/admin/species/sources", "species/sources"},
		{"/v1/admin/species/rights", "species/rights"},
	}

	for _, tc := range cases {
		route, ok := Match("GET", tc.path)
		if !ok {
			t.Fatalf("Match(GET %s) found no route", tc.path)
		}
		if route.Base != tc.base {
			t.Fatalf("Match(GET %s) = %q, want %q", tc.path, route.Base, tc.base)
		}
	}
}

func TestMatchFallsBackToWildcard(t *testing.T) {
	t.Parallel()

	route, ok := Match("GET", "/v1/admin/species/panthera-leo")
	if !ok {
		t.Fatal("wildcard species detail route not matched")
	}
	if route.Base != "species/detail" {
		t.Fatalf("fixture = %q, want species/detail", route.Base)
	}
}
