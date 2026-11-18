package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestHealth(t *testing.T) {
	srv := httptest.NewServer(newMux())
	defer srv.Close()
	resp, err := http.Get(srv.URL + "/health")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d", resp.StatusCode)
	}
	var body map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	if body["contract"] != "1.0.0" {
		t.Fatalf("contract = %v", body["contract"])
	}
}

func TestServesDefaultFixture(t *testing.T) {
	srv := httptest.NewServer(newMux())
	defer srv.Close()
	resp, err := http.Get(srv.URL + "/v1/public/events")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d", resp.StatusCode)
	}
	if resp.Header.Get("X-Fixture-State") != "default" {
		t.Fatalf("state header = %q", resp.Header.Get("X-Fixture-State"))
	}
	var body struct {
		Items []map[string]any `json:"items"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	if len(body.Items) == 0 {
		t.Fatal("expected fixture items")
	}
}

func TestServesEmptyErrorAndStaleStates(t *testing.T) {
	srv := httptest.NewServer(newMux())
	defer srv.Close()

	empty := get(t, srv.URL+"/v1/public/events?state=empty")
	if empty.StatusCode != http.StatusOK {
		t.Fatalf("empty status = %d", empty.StatusCode)
	}
	var body struct {
		Items []map[string]any `json:"items"`
	}
	if err := json.NewDecoder(empty.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	empty.Body.Close()
	if len(body.Items) != 0 {
		t.Fatalf("expected no items, got %d", len(body.Items))
	}

	errResp := get(t, srv.URL+"/v1/public/events?state=error")
	if errResp.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("error status = %d", errResp.StatusCode)
	}
	errResp.Body.Close()

	stale := get(t, srv.URL+"/v1/public/events?state=stale")
	if stale.StatusCode != http.StatusOK {
		t.Fatalf("stale status = %d", stale.StatusCode)
	}
	if stale.Header.Get("X-Data-Stale") != "true" {
		t.Fatal("stale response must be marked")
	}
	stale.Body.Close()
}

func TestStateHeaderAndFallback(t *testing.T) {
	srv := httptest.NewServer(newMux())
	defer srv.Close()
	req, _ := http.NewRequest(http.MethodGet, srv.URL+"/v1/public/events", nil)
	req.Header.Set("X-Fixture-State", "empty")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.Header.Get("X-Fixture-State") != "empty" {
		t.Fatalf("state = %q", resp.Header.Get("X-Fixture-State"))
	}

	// A route without an empty fixture falls back to default and says so.
	resp2, err := http.Get(srv.URL + "/v1/public/venues/kicc?state=empty")
	if err != nil {
		t.Fatal(err)
	}
	resp2.Body.Close()
	if resp2.Header.Get("X-Fixture-State-Fallback") != "empty" {
		t.Fatalf("fallback header = %q", resp2.Header.Get("X-Fixture-State-Fallback"))
	}
}

func TestUnknownRouteAndState(t *testing.T) {
	srv := httptest.NewServer(newMux())
	defer srv.Close()
	resp := get(t, srv.URL+"/v1/public/nope")
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("status = %d", resp.StatusCode)
	}
	resp.Body.Close()
	resp = get(t, srv.URL+"/v1/public/events?state=bogus")
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("status = %d", resp.StatusCode)
	}
	resp.Body.Close()
}

func TestCalendarIsGenerated(t *testing.T) {
	srv := httptest.NewServer(newMux())
	defer srv.Close()
	resp := get(t, srv.URL+"/v1/public/events/abc/calendar.ics")
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d", resp.StatusCode)
	}
	if got := resp.Header.Get("Content-Type"); got[:13] != "text/calendar" {
		t.Fatalf("content type = %q", got)
	}
}

func get(t *testing.T, url string) *http.Response {
	t.Helper()
	resp, err := http.Get(url)
	if err != nil {
		t.Fatal(err)
	}
	return resp
}
