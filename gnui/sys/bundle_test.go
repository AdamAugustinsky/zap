package sys

import (
	"encoding/json"
	"os"
	"slices"
	"testing"

	"zapfast/state"
)

// The bundle gnui builds is described by gnui.json: the app's identity, the
// links and the login item this package serves, and the permissions calls
// need.
func TestTheBundleDescriptionServesWhatTheServicesNeed(t *testing.T) {
	b, err := os.ReadFile("../gnui.json")
	if err != nil {
		t.Fatal(err)
	}
	var c struct {
		ID          string
		Name        string
		Main        string
		Version     string
		Build       string
		URLSchemes  []string
		LoginAgents []struct {
			Name      string
			Arguments []string
		}
		Frameworks   []string
		Usage        map[string]string
		Entitlements map[string]any
	}
	if err := json.Unmarshal(b, &c); err != nil {
		t.Fatal(err)
	}
	// CFBundleVersion is the version too, as in upstream's Info.plist.
	if c.ID != state.AppID || c.Name != "ZapFast" || c.Main != "./cmd/zapfast" || c.Version != state.Version || c.Build != state.Version {
		t.Errorf("%s %s %s %s %s", c.ID, c.Name, c.Main, c.Version, c.Build)
	}
	if !slices.Equal(c.URLSchemes, []string{"whatsapp", "wa"}) {
		t.Errorf("schemes %v", c.URLSchemes)
	}
	// The login item runs hidden, under the name this package asks for.
	if len(c.LoginAgents) != 1 || c.LoginAgents[0].Name != agentName || !slices.Equal(c.LoginAgents[0].Arguments, []string{"--start-hidden"}) {
		t.Errorf("login agents %+v", c.LoginAgents)
	}
	// The Rust core (the fallback), the call media library, and the Go
	// backend's SQLCipher and WebP libraries.
	frameworks := []string{"../target/core-release/libzapfast_core.dylib", "../target/core-release/libzfcalls.dylib", "../target/sqlcipher/libsqlcipher.dylib", "../target/libwebp/libzfwebp.dylib"}
	if !slices.Equal(c.Frameworks, frameworks) || c.Usage["NSMicrophoneUsageDescription"] == "" || c.Usage["NSCameraUsageDescription"] == "" {
		t.Errorf("frameworks %v usage %v", c.Frameworks, c.Usage)
	}
	if c.Entitlements["com.apple.security.device.audio-input"] != true || c.Entitlements["com.apple.security.device.camera"] != true {
		t.Errorf("entitlements %v", c.Entitlements)
	}
}
