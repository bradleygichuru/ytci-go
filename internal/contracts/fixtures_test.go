package contracts_test

import (
	"bytes"
	"encoding/json"
	"fmt"
	"sort"
	"strconv"
	"testing"

	"github.com/getkin/kin-openapi/openapi3"

	"github.com/bradleygichuru/ytci-go/fixtures"
	"github.com/bradleygichuru/ytci-go/internal/contracts"
)

func TestFixtureRouteTableMatchesOpenAPI(t *testing.T) {
	doc, err := contracts.LoadSpec()
	if err != nil {
		t.Fatal(err)
	}
	// Every JSON operation in the frozen spec must have a mock route.
	for path, item := range doc.Paths.Map() {
		for method, op := range item.Operations() {
			if isICS(op) {
				continue
			}
			if _, ok := fixtures.Match(method, concrete(path)); !ok {
				t.Errorf("no mock route for %s %s", method, path)
			}
		}
	}
	// Every mock route must be frozen in the spec.
	for _, route := range fixtures.Routes {
		item := doc.Paths.Find(route.Pattern)
		if item == nil {
			t.Errorf("route %s %s is not in the OpenAPI contract", route.Method, route.Pattern)
			continue
		}
		if operationFor(item, route.Method) == nil {
			t.Errorf("route %s %s is not in the OpenAPI contract", route.Method, route.Pattern)
		}
	}
}

func TestPrimaryFixturesShipAllStates(t *testing.T) {
	for _, route := range fixtures.Routes {
		if route.Base == "" || route.NoContent {
			continue
		}
		if !fixtures.Has(route.Base, fixtures.StateDefault) {
			t.Errorf("%s %s: missing default fixture %s", route.Method, route.Pattern, route.Base)
		}
		if route.Primary {
			for _, state := range fixtures.States {
				if !fixtures.Has(route.Base, state) {
					t.Errorf("%s %s: primary fixture %s is missing state %s", route.Method, route.Pattern, route.Base, state)
				}
			}
		}
	}
}

func TestFixturesValidateAgainstSchemas(t *testing.T) {
	doc, err := contracts.LoadSpec()
	if err != nil {
		t.Fatal(err)
	}
	for _, route := range fixtures.Routes {
		if route.Base == "" || route.NoContent {
			continue
		}
		for _, state := range fixtures.States {
			if !fixtures.Has(route.Base, state) {
				continue
			}
			t.Run(fmt.Sprintf("%s_%s_%s", route.Method, route.Pattern, state), func(t *testing.T) {
				raw, err := fixtures.Load(route.Base, state)
				if err != nil {
					t.Fatal(err)
				}
				var value any
				decoder := json.NewDecoder(bytes.NewReader(raw))
				decoder.UseNumber()
				if err := decoder.Decode(&value); err != nil {
					t.Fatalf("invalid JSON: %v", err)
				}
				schema := responseSchema(t, doc, route)
				if state == fixtures.StateError {
					schema = doc.Components.Schemas["Error"].Value
				}
				if err := schema.VisitJSON(value); err != nil {
					t.Fatalf("fixture %s.%s.json: %v", route.Base, state, err)
				}
			})
		}
	}
}

func TestNoOrphanFixtures(t *testing.T) {
	used := map[string]bool{}
	for _, route := range fixtures.Routes {
		if route.Base != "" {
			used[route.Base] = true
		}
	}
	var orphans []string
	for _, base := range fixtures.Bases() {
		if !used[base] {
			orphans = append(orphans, base)
		}
	}
	sort.Strings(orphans)
	if len(orphans) > 0 {
		t.Fatalf("fixtures not referenced by a route: %v", orphans)
	}
}

func responseSchema(t *testing.T, doc *openapi3.T, route fixtures.Route) *openapi3.Schema {
	t.Helper()
	item := doc.Paths.Find(route.Pattern)
	if item == nil {
		t.Fatalf("missing path %s", route.Pattern)
	}
	op := operationFor(item, route.Method)
	if op == nil {
		t.Fatalf("missing operation %s %s", route.Method, route.Pattern)
	}
	resp := op.Responses.Value(strconv.Itoa(route.Status))
	if resp == nil {
		// Fall back to the first 2xx response.
		for code, r := range op.Responses.Map() {
			if len(code) == 3 && code[0] == '2' {
				resp = r
				break
			}
		}
	}
	if resp == nil || resp.Value == nil {
		t.Fatalf("no success response for %s %s", route.Method, route.Pattern)
	}
	media := resp.Value.Content.Get("application/json")
	if media == nil || media.Schema == nil || media.Schema.Value == nil {
		t.Fatalf("no application/json schema for %s %s", route.Method, route.Pattern)
	}
	return media.Schema.Value
}

func operationFor(item *openapi3.PathItem, method string) *openapi3.Operation {
	switch method {
	case "GET":
		return item.Get
	case "POST":
		return item.Post
	case "PUT":
		return item.Put
	case "PATCH":
		return item.Patch
	case "DELETE":
		return item.Delete
	}
	return nil
}

func concrete(pattern string) string {
	out := ""
	for _, part := range splitPath(pattern) {
		if len(part) > 0 && part[0] == '{' {
			out += "/demo-id"
			continue
		}
		out += "/" + part
	}
	return out
}

func splitPath(path string) []string {
	var parts []string
	start := 0
	for i := 0; i <= len(path); i++ {
		if i == len(path) || path[i] == '/' {
			if i > start {
				parts = append(parts, path[start:i])
			}
			start = i + 1
		}
	}
	return parts
}

func isICS(op *openapi3.Operation) bool {
	for _, r := range op.Responses.Map() {
		if r.Value == nil {
			continue
		}
		if r.Value.Content.Get("text/calendar") != nil {
			return true
		}
	}
	return false
}
