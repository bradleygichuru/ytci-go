// Package fixtures embeds the contract mock fixtures used by the UI streams.
//
// Every file is named <base>.<state>.json where state is default, empty, error
// or stale. The mock server and the contract tests share this package so a
// fixture can never drift from the route it serves.
package fixtures

import (
	"embed"
	"fmt"
	"io/fs"
	"strings"
)

//go:embed */*.json
var FS embed.FS

// States supported by the mock server.
const (
	StateDefault = "default"
	StateEmpty   = "empty"
	StateError   = "error"
	StateStale   = "stale"
)

// States is the canonical state list.
var States = []string{StateDefault, StateEmpty, StateError, StateStale}

// Load reads a fixture for a base name and state.
func Load(base, state string) ([]byte, error) {
	if state == "" {
		state = StateDefault
	}
	if !validState(state) {
		return nil, fmt.Errorf("unknown fixture state %q", state)
	}
	name := base + "." + state + ".json"
	raw, err := fs.ReadFile(FS, name)
	if err != nil {
		if state != StateDefault {
			// Fall back to the default fixture; the mock marks the response so
			// UI streams can tell the requested state was not available.
			return Load(base, StateDefault)
		}
		return nil, err
	}
	return raw, nil
}

// Has reports whether a fixture exists.
func Has(base, state string) bool {
	_, err := fs.Stat(FS, base+"."+state+".json")
	return err == nil
}

// Bases returns every fixture base name.
func Bases() []string {
	entries, err := fs.Glob(FS, "*/*.json")
	if err != nil {
		return nil
	}
	seen := map[string]bool{}
	var out []string
	for _, entry := range entries {
		name := strings.TrimSuffix(entry, ".json")
		idx := strings.LastIndex(name, ".")
		if idx < 0 {
			continue
		}
		base := name[:idx]
		if !seen[base] {
			seen[base] = true
			out = append(out, base)
		}
	}
	return out
}

func validState(state string) bool {
	for _, s := range States {
		if s == state {
			return true
		}
	}
	return false
}
