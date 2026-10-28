package state_test

import (
	"os"
	"regexp"
	"testing"

	"zapfast/state"
)

// Version follows upstream's Cargo.toml, which a release bumps.
func TestVersionIsUpstreams(t *testing.T) {
	b, err := os.ReadFile("../../Cargo.toml")
	if err != nil {
		t.Skip("no Cargo.toml beside gnui:", err)
	}
	m := regexp.MustCompile(`(?m)^version\s*=\s*"([^"]+)"`).FindSubmatch(b)
	if m == nil || string(m[1]) != state.Version {
		t.Errorf("Cargo.toml says %q, state.Version %q", m, state.Version)
	}
}
