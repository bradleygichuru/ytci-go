// Command contracts-mock serves the frozen execution-contract fixtures so the
// MICE, recognition, species and analytics UI streams can build without waiting
// for feature handlers.
//
//	go run ./cmd/contracts-mock -addr :8099
//
// Every route is listed in fixtures/routes.go and must exist in
// api/openapi/contracts.v1.yaml. Request a non-default state with
// ?state=empty|error|stale or the X-Fixture-State header. Error fixtures return
// 503 with the standard error envelope; stale fixtures set X-Data-Stale.
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"net/http"
	"sort"
	"strings"
	"time"

	"github.com/bradleygichuru/ytci-go/fixtures"
)

func main() {
	addr := flag.String("addr", ":8099", "listen address")
	flag.Parse()

	mux := newMux()
	srv := &http.Server{
		Addr:              *addr,
		Handler:           cors(mux),
		ReadHeaderTimeout: 5 * time.Second,
	}
	log.Printf("contracts mock listening on %s (%d routes)", *addr, len(fixtures.Routes))
	log.Fatal(srv.ListenAndServe())
}

func newMux() *http.ServeMux {
	mux := http.NewServeMux()
	mux.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusOK, map[string]any{
			"ok":            true,
			"contract":      "1.0.0",
			"fixtureStates": fixtures.States,
			"routes":        len(fixtures.Routes),
		})
	})
	mux.HandleFunc("/__fixtures", func(w http.ResponseWriter, r *http.Request) {
		type entry struct {
			Method  string   `json:"method"`
			Pattern string   `json:"pattern"`
			Base    string   `json:"base,omitempty"`
			States  []string `json:"states"`
		}
		seen := map[string]bool{}
		var out []entry
		for _, route := range fixtures.Routes {
			if route.Base == "" || seen[route.Base] {
				continue
			}
			seen[route.Base] = true
			var states []string
			for _, state := range fixtures.States {
				if fixtures.Has(route.Base, state) {
					states = append(states, state)
				}
			}
			out = append(out, entry{Method: route.Method, Pattern: route.Pattern, Base: route.Base, States: states})
		}
		sort.Slice(out, func(i, j int) bool {
			if out[i].Pattern == out[j].Pattern {
				return out[i].Method < out[j].Method
			}
			return out[i].Pattern < out[j].Pattern
		})
		writeJSON(w, http.StatusOK, map[string]any{"states": fixtures.States, "routes": out})
	})
	mux.HandleFunc("/", serveFixture)
	return mux
}

func serveFixture(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodOptions {
		w.WriteHeader(http.StatusNoContent)
		return
	}
	route, ok := fixtures.Match(r.Method, r.URL.Path)
	if !ok {
		writeError(w, http.StatusNotFound, "MOCK_ROUTE_NOT_FOUND",
			fmt.Sprintf("no contract fixture for %s %s", r.Method, r.URL.Path))
		return
	}

	state := strings.TrimSpace(r.URL.Query().Get("state"))
	if state == "" {
		state = strings.TrimSpace(r.Header.Get("X-Fixture-State"))
	}
	if state == "" {
		state = fixtures.StateDefault
	}
	valid := false
	for _, candidate := range fixtures.States {
		if candidate == state {
			valid = true
			break
		}
	}
	if !valid {
		writeError(w, http.StatusBadRequest, "MOCK_UNKNOWN_STATE",
			fmt.Sprintf("unknown state %q; use %s", state, strings.Join(fixtures.States, ", ")))
		return
	}

	w.Header().Set("X-Contract-Version", "1.0.0")
	w.Header().Set("X-Fixture-Route", route.Method+" "+route.Pattern)

	if route.NoContent {
		w.WriteHeader(http.StatusNoContent)
		return
	}

	if route.Base == "" {
		// Generated text/calendar response for the ICS share link.
		w.Header().Set("Content-Type", "text/calendar; charset=utf-8")
		fmt.Fprintf(w, "BEGIN:VCALENDAR\r\nVERSION:2.0\r\nPRODID:-//YTCI//Contracts Mock//EN\r\nBEGIN:VEVENT\r\nUID:%s\r\nDTSTAMP:%s\r\nSUMMARY:Demo event\r\nEND:VEVENT\r\nEND:VCALENDAR\r\n",
			r.URL.Path, time.Now().UTC().Format("20060102T150405Z"))
		return
	}

	usedState := state
	if !fixtures.Has(route.Base, state) {
		usedState = fixtures.StateDefault
		w.Header().Set("X-Fixture-State-Fallback", state)
	}
	raw, err := fixtures.Load(route.Base, usedState)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "MOCK_FIXTURE_MISSING", err.Error())
		return
	}
	w.Header().Set("X-Fixture-State", usedState)

	status := route.Status
	contentType := "application/json; charset=utf-8"
	switch usedState {
	case fixtures.StateError:
		status = http.StatusServiceUnavailable
	case fixtures.StateStale:
		w.Header().Set("X-Data-Stale", "true")
	}
	if route.Base == "mice/calendar" {
		contentType = "text/calendar; charset=utf-8"
	}

	w.Header().Set("Content-Type", contentType)
	w.WriteHeader(status)
	_, _ = w.Write(raw)
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}

func writeError(w http.ResponseWriter, status int, code, message string) {
	writeJSON(w, status, map[string]any{
		"error": map[string]string{"code": code, "message": message},
	})
}

func cors(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Access-Control-Allow-Methods", "GET, POST, PUT, PATCH, DELETE, OPTIONS")
		w.Header().Set("Access-Control-Allow-Headers", "Authorization, Content-Type, Idempotency-Key, X-Fixture-State")
		w.Header().Set("Access-Control-Expose-Headers", "X-Contract-Version, X-Data-Stale, X-Fixture-Route, X-Fixture-State, X-Fixture-State-Fallback")
		next.ServeHTTP(w, r)
	})
}
