package state_test

import (
	"encoding/json"
	"os"
	"testing"

	"zapfast/state"
)

// Version is the bundle's (gnui.json's version), which a release bumps.
func TestVersionIsTheBundles(t *testing.T) {
	b, err := os.ReadFile("../gnui.json")
	if err != nil {
		t.Fatal(err)
	}
	var cfg struct{ Version string }
	if err := json.Unmarshal(b, &cfg); err != nil {
		t.Fatal(err)
	}
	if cfg.Version != state.Version {
		t.Errorf("gnui.json says %q, state.Version %q", cfg.Version, state.Version)
	}
}
