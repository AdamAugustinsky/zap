package core

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func touch(t *testing.T, p string, mod time.Time) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte("x"), 0o755); err != nil {
		t.Fatal(err)
	}
	if !mod.IsZero() {
		os.Chtimes(p, mod, mod)
	}
}

// The loading policy on a fake layout: a bundle, a cargo checkout, an env.
func TestLoadingPolicy(t *testing.T) {
	root := t.TempDir()
	app := filepath.Join(root, "ZapFast.app")
	exe := filepath.Join(app, "Contents", "MacOS", "zapfast")
	bundled := filepath.Join(app, "Contents", "Frameworks", LibName)
	touch(t, exe, time.Time{})
	// A checkout around the working directory, with two builds.
	repo := filepath.Join(root, "repo")
	wd := filepath.Join(repo, "gnui")
	old := time.Now().Add(-time.Hour)
	touch(t, filepath.Join(repo, "target", "core-release", LibName), old)
	debug := filepath.Join(repo, "target", "debug", LibName)
	touch(t, debug, time.Now())
	if err := os.MkdirAll(wd, 0o755); err != nil {
		t.Fatal(err)
	}
	other := filepath.Join(root, "elsewhere", LibName)
	env := func(string) string { return other }
	noEnv := func(string) string { return "" }

	t.Run("bundled without the library fails closed", func(t *testing.T) {
		// Not the environment's, nor the cargo build's.
		if p, err := resolveLibrary(exe, wd, "", env); err == nil || !strings.Contains(err.Error(), "missing from the app bundle") {
			t.Fatalf("got %q, %v", p, err)
		}
	})
	touch(t, bundled, time.Time{})
	t.Run("bundled loads only its own Frameworks", func(t *testing.T) {
		if p, err := resolveLibrary(exe, wd, "", env); err != nil || p != bundled {
			t.Fatalf("got %q, %v; want %q", p, err, bundled)
		}
		if p, err := resolveLibrary(exe, wd, other, noEnv); err == nil {
			t.Fatalf("an explicit path in a bundle: %q", p)
		}
		if p, err := resolveLibrary(exe, wd, bundled, noEnv); err != nil || p != bundled {
			t.Fatalf("its own path, explicitly: %q, %v", p, err)
		}
	})
	devExe := filepath.Join(root, "bin", "zapfast") // go build output: no bundle
	t.Run("unbundled: explicit, then the environment, then the newest cargo build", func(t *testing.T) {
		if p, _ := resolveLibrary(devExe, wd, "/x/explicit.dylib", env); p != "/x/explicit.dylib" {
			t.Errorf("explicit: %q", p)
		}
		if p, _ := resolveLibrary(devExe, wd, "", env); p != other {
			t.Errorf("env: %q", p)
		}
		if p, _ := resolveLibrary(devExe, wd, "", noEnv); p != debug {
			t.Errorf("cargo: %q, want the newest, %q", p, debug)
		}
		if _, err := resolveLibrary(devExe, root, "", noEnv); err == nil {
			t.Error("nothing to find, yet found")
		}
	})
	t.Run("lookalikes are not bundles", func(t *testing.T) {
		for _, e := range []string{
			filepath.Join(root, "Zap", "Contents", "MacOS", "zapfast"),
			filepath.Join(app, "Contents", "Resources", "zapfast"),
			filepath.Join(app, "MacOS", "zapfast"),
		} {
			if _, ok := bundleLibrary(e); ok {
				t.Errorf("%s taken for a bundle's executable", e)
			}
		}
	})
}
