package settings

import (
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"math/rand"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"gonativeui/app/selfupdate"
	"gonativeui/ui"

	"zapfast/core/fake"
	"zapfast/state"
)

// UP-006: what a copy that cannot update itself is told, in upstream's words
// (fastframe-update's Unsupported) where it has them.
func TestUnsupportedWording(t *testing.T) {
	for _, tc := range []struct {
		why  error
		exe  string
		want string
	}{
		{nil, "/Applications/ZapFast.app/Contents/MacOS/zapfast", ""},
		{nil, "/Users/a/.cargo/bin/zapfast", "Update this installation with cargo install."},
		{nil, "/nix/store/x/bin/zapfast", "Update this installation with Nix."},
		{nil, "/opt/homebrew/Cellar/zapfast/1/bin/zapfast", "Update this installation with Homebrew."},
		{nil, "/opt/homebrew/Caskroom/zapfast/1/ZapFast.app/Contents/MacOS/zapfast", "Update this installation with Homebrew."},
		{selfupdate.ErrNotBundled, "/x/zapfast", "Move the app to Applications, then open it to update."},
		{selfupdate.ErrTranslocated, "/x/zapfast", "Move the app to Applications, then open it to update."},
		{selfupdate.ErrReadOnly, "/x/zapfast", "Move the app to Applications, then open it to update."},
		{selfupdate.ErrDevBundle, "/x/zapfast", "This app bundle is not a release of this app."},
		{selfupdate.ErrNotWritable, "/x/zapfast", "Zap cannot replace itself here. Move it to a folder you can write to, such as Applications, or use the download page."},
		{selfupdate.ErrNotSigned, "/x/zapfast", "This copy of Zap has no publisher signature, so an update cannot be checked against it. Use the download page."},
		{selfupdate.ErrUnsupported, "/x/zapfast", "Use the download page for this operating system or architecture."},
		{errors.New("selfupdate: no bundle"), "/x/zapfast", "Cannot inspect this installation: no bundle"},
		// a package manager's reason wins over the copy's own
		{selfupdate.ErrTranslocated, "/opt/homebrew/Cellar/zapfast/1/bin/zapfast", "Update this installation with Homebrew."},
	} {
		if got := supportReason(tc.exe, nil, tc.why); got != tc.want {
			t.Errorf("%v at %s: %q, want %q", tc.why, tc.exe, got, tc.want)
		}
	}
}

// UP-006: a bundle in Applications that a Homebrew cask links to updates
// through Homebrew.
func TestCaskOwnedBundle(t *testing.T) {
	root := t.TempDir()
	bundle := filepath.Join(root, "Applications", "ZapFast.app")
	exe := filepath.Join(bundle, "Contents", "MacOS", "zapfast")
	if err := os.MkdirAll(filepath.Dir(exe), 0o755); err != nil {
		t.Fatal(err)
	}
	prefix := filepath.Join(root, "brew")
	ver := filepath.Join(prefix, "Caskroom", "zapfast", "0.19.0")
	if err := os.MkdirAll(ver, 0o755); err != nil {
		t.Fatal(err)
	}
	if got := supportReason(exe, []string{prefix}, nil); got != "" {
		t.Errorf("a bundle no cask links to: %q", got)
	}
	if err := os.Symlink(bundle, filepath.Join(ver, "ZapFast.app")); err != nil {
		t.Fatal(err)
	}
	if got := supportReason(exe, []string{prefix}, nil); got != "Update this installation with Homebrew." {
		t.Errorf("a cask's bundle: %q", got)
	}
}

// installRig is an update ready to install, on a fake app/selfupdate; quits
// counts the requests to quit.
func installRig(t *testing.T, self *fakeSelf) (r *rig, f *feed, quits *atomic.Int32) {
	t.Helper()
	quits = new(atomic.Int32)
	old := quitApp
	quitApp = func() { quits.Add(1) }
	t.Cleanup(func() { quitApp = old })
	f, pub := makeFeed(t, "0.21.0", []byte("the update"))
	r = newRig(t, 760, 1100, func(s *state.State, _ *fake.Core, scr *Screen) {
		s.Settings.CheckForUpdates = false
		scr.up.SetSystem(self)
		scr.up.SetFeed(NewFeed(f.dir))
		scr.up.SetKey(pub)
		scr.up.SetDir(t.TempDir())
	})
	connect(r)
	return r, f, quits
}

func readyToInstall(r *rig) {
	r.click(ui.RoleButton, "Check now")
	r.wait("the release", func() bool { return r.has(ui.RoleButton, "Download update") })
	r.click(ui.RoleButton, "Download update")
	r.wait("the download", func() bool { return r.has(ui.RoleButton, "Restart to update") })
}

// UP-003: Restart to update checks the copy, prepares the staged update,
// hands over to the helper, and only then quits through the normal path.
func TestRestartInstallsAndQuits(t *testing.T) {
	self := &fakeSelf{}
	r, _, quits := installRig(t, self)
	readyToInstall(r)
	file := r.scr.up.file
	r.click(ui.RoleButton, "Restart to update")
	r.wait("the quit", func() bool { return quits.Load() == 1 })
	if got := strings.Join(self.log(), " "); got != "supported supported prepare install" {
		// the first Supported is the probe's
		t.Errorf("calls %q", got)
	}
	if _, err := os.Stat(file); !os.IsNotExist(err) {
		t.Error("the downloaded archive is left behind")
	}
}

// UP-004: a download that no longer is the verified one never reaches Prepare,
// and the app does not quit.
func TestTamperedDownloadNeverReachesPrepare(t *testing.T) {
	self := &fakeSelf{}
	r, _, quits := installRig(t, self)
	readyToInstall(r)
	if err := os.WriteFile(r.scr.up.file, []byte("swapped after the check"), 0o600); err != nil {
		t.Fatal(err)
	}
	r.click(ui.RoleButton, "Restart to update")
	r.wait("the failure", func() bool { return r.has(ui.RoleButton, "Retry download") })
	if strings.Contains(strings.Join(self.log(), " "), "prepare") || quits.Load() != 0 {
		t.Errorf("calls %v, quits %d", self.log(), quits.Load())
	}
	if !r.screenText("does not match its checksum") {
		t.Error("the failure is not explained")
	}
}

// UP-004: every refusal says what it is, and none quits.
func TestInstallRefusals(t *testing.T) {
	for _, tc := range []struct {
		name string
		self *fakeSelf
		want string
		runs []string // the calls after the probe's
	}{
		{"not newer", &fakeSelf{prepare: selfupdate.ErrNotNewer}, "This update is not newer than the version you are running.", []string{"supported", "prepare"}},
		{"rejected", &fakeSelf{prepare: fmt.Errorf("%w: its signature is not the running app's", selfupdate.ErrRejected)}, "The update was not installed: the update was rejected: its signature is not the running app's", []string{"supported", "prepare"}},
		{"prepare failed", &fakeSelf{prepare: errors.New("no space left")}, "Could not prepare the update: no space left", []string{"supported", "prepare"}},
		{"helper failed", &fakeSelf{install: errors.New("selfupdate: could not start the helper")}, "Could not start the update: could not start the helper", []string{"supported", "prepare", "install", "discard"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r, _, quits := installRig(t, tc.self)
			readyToInstall(r)
			r.click(ui.RoleButton, "Restart to update")
			r.wait("the failure", func() bool { return r.has(ui.RoleButton, "Retry download") })
			if !r.screenText(tc.want) {
				t.Errorf("not shown: %q", tc.want)
			}
			if got := strings.Join(tc.self.log()[1:], " "); got != strings.Join(tc.runs, " ") {
				t.Errorf("calls %q, want %q", got, strings.Join(tc.runs, " "))
			}
			if quits.Load() != 0 {
				t.Error("the app quit with a failed update")
			}
		})
	}
}

// UP-006: a copy that cannot update itself offers no Restart, says why, and
// links to the download page.
func TestUnsupportedCopyOffersTheDownloadPage(t *testing.T) {
	self := &fakeSelf{supported: selfupdate.ErrTranslocated}
	r, _, _ := installRig(t, self)
	r.click(ui.RoleButton, "Check now")
	r.wait("the explanation", func() bool { return r.text("Move the app to Applications, then open it to update.") })
	if r.has(ui.RoleButton, "Download update") || !r.has(ui.RoleLink, "Download from GitHub") {
		t.Error("an unsupported copy is offered the update")
	}
}

// UP-004: after a restart the result is shown, and this run reports healthy
// once.
func TestLastResultAfterARestart(t *testing.T) {
	for _, tc := range []struct {
		name    string
		res     selfupdate.Result
		toast   string
		status  string
		isError bool
	}{
		{"rolled back", selfupdate.Result{Status: selfupdate.RolledBack, To: "0.21.0", Reason: "the new version did not report in"},
			"The update could not start. The previous version has been restored.",
			"Updating to 0.21.0 failed and Zap went back to the previous version: the new version did not report in", true},
		{"failed", selfupdate.Result{Status: selfupdate.Failed, To: "0.21.0", Reason: "the volume is read-only"},
			"The update could not be installed: the volume is read-only", "The update could not be installed: the volume is read-only", true},
		{"updated", selfupdate.Result{Status: selfupdate.Updated, To: "0.21.0"}, "Zap was updated to 0.21.0.", "", false},
		{"none", selfupdate.Result{}, "", "", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			self := &fakeSelf{result: tc.res}
			r := newRig(t, 760, 900, func(s *state.State, _ *fake.Core, scr *Screen) { scr.up.SetSystem(self) })
			connect(r)
			e := r.scr.env
			for i := 0; i < 10; i++ {
				r.scr.up.Frame(e)
				r.pump()
				r.Frames(1)
			}
			r.wait("the result", func() bool { r.scr.up.Frame(e); return r.scr.up.resultReady.Load() })
			r.scr.up.Frame(e)
			r.scr.up.Frame(e)
			if self.started != 1 {
				t.Errorf("Started was called %d times", self.started)
			}
			if got := r.toast(); got != tc.toast {
				t.Errorf("toast %q, want %q", got, tc.toast)
			}
			if n := len(r.s.Toasts); (tc.toast == "") != (n == 0) || n > 1 {
				t.Errorf("%d toasts", n)
			}
			if tc.status != "" {
				r.Frames(2)
				if !r.screenText(tc.status) {
					t.Errorf("the Updates section does not say %q", tc.status)
				}
			}
		})
	}
}

// screenText reports whether any text on screen contains s.
func (r *rig) screenText(s string) bool {
	tr := r.Tree()
	for _, n := range tr.Nodes {
		if strings.Contains(string(tr.Text(n.Name)), s) || strings.Contains(string(tr.Text(n.Value)), s) {
			return true
		}
	}
	return false
}

// UP-004: the release key and the next one are both embedded, a distinct pair:
// A3's (packaging/UPDATE_SIGNING.md), which replaced upstream's.
func TestBothPublisherKeysAreEmbedded(t *testing.T) {
	ks := publisherKeys()
	if len(ks) != 2 || string(ks[0]) == string(ks[1]) {
		t.Fatalf("%d keys, want the current and the next, different", len(ks))
	}
	for _, k := range []struct{ name, got, want string }{
		{"current", publisherKey, "c5adf2e534da47ab5dfd6064a3c7d8eb66f913203f950254d2515c6958edb563"},
		{"next", nextPublisherKey, "c18503262212bbcdfba02846da2cf1caf2b4409f44b4f7c077718a9c502853cf"},
	} {
		if got := strings.TrimSpace(k.got); got != k.want {
			t.Errorf("the %s key is %q, want A3's %q", k.name, got, k.want)
		}
	}
}

// UP-004: a release signed by the next key is accepted at both checks, the
// download's and the one before Prepare; with only the current key it is not.
func TestNextPublisherKeyIsAcceptedAtBothChecks(t *testing.T) {
	f, nextPub := makeFeed(t, "0.21.0", []byte("the update")) // signed by a key standing for the next one
	currentPub, _, _ := ed25519.GenerateKey(nil)
	setup := func(keys ...ed25519.PublicKey) (*rig, *fakeSelf, *atomic.Int32) {
		self := &fakeSelf{}
		quits := new(atomic.Int32)
		old := quitApp
		quitApp = func() { quits.Add(1) }
		t.Cleanup(func() { quitApp = old })
		r := newRig(t, 760, 1100, func(s *state.State, _ *fake.Core, scr *Screen) {
			s.Settings.CheckForUpdates = false
			scr.up.SetSystem(self)
			scr.up.SetFeed(NewFeed(f.dir))
			scr.up.SetKeys(keys...)
			scr.up.SetDir(t.TempDir())
		})
		connect(r)
		return r, self, quits
	}
	t.Run("accepted", func(t *testing.T) {
		r, self, quits := setup(currentPub, nextPub)
		readyToInstall(r)
		r.click(ui.RoleButton, "Restart to update")
		r.wait("the quit", func() bool { return quits.Load() == 1 })
		if !strings.Contains(strings.Join(self.log(), " "), "prepare install") {
			t.Errorf("calls %v", self.log())
		}
	})
	t.Run("refused at the download without the next key", func(t *testing.T) {
		r, self, _ := setup(currentPub)
		r.click(ui.RoleButton, "Check now")
		r.wait("the release", func() bool { return r.has(ui.RoleButton, "Download update") })
		r.click(ui.RoleButton, "Download update")
		r.wait("the refusal", func() bool { return r.has(ui.RoleButton, "Retry download") })
		if !r.screenText("signature is not valid") || strings.Contains(strings.Join(self.log(), " "), "prepare") {
			t.Errorf("not refused: %v", self.log())
		}
	})
	t.Run("refused before Prepare when the keys no longer include it", func(t *testing.T) {
		r, self, quits := setup(currentPub, nextPub)
		readyToInstall(r)
		r.scr.up.SetKeys(currentPub) // the second check is its own
		r.click(ui.RoleButton, "Restart to update")
		r.wait("the refusal", func() bool { return r.has(ui.RoleButton, "Retry download") })
		if !r.screenText("signature is not valid") || strings.Contains(strings.Join(self.log(), " "), "prepare") || quits.Load() != 0 {
			t.Errorf("calls %v, quits %d", self.log(), quits.Load())
		}
	})
}

// UP-004: the signature covers the checksum and the file name, so the file
// name has to be the version's: an older release's authentic package, name
// and signature replayed under a newer version are refused before any download.
func TestAnOlderReleaseReplayedUnderANewerVersionIsRefused(t *testing.T) {
	old, pub := makeFeed(t, "0.20.5", []byte("an older, vulnerable release")) // really signed, by the publisher's key
	for name, mangle := range map[string]func(r *Release){
		"the older package under a newer version": func(r *Release) { r.Version = "0.22.0" },
		"another file name altogether":            func(r *Release) { r.File = "evil.zip" },
		"a path in the name":                      func(r *Release) { r.File = "../ZapFast-0.20.5.zip" },
		"a lower case name":                       func(r *Release) { r.File = "zapfast-0.20.5.zip" },
	} {
		t.Run(name, func(t *testing.T) {
			rf := &recordingFeed{rel: old.rel}
			mangle(&rf.rel)
			dir := t.TempDir()
			r := newRig(t, 760, 1100, func(s *state.State, _ *fake.Core, scr *Screen) {
				s.Settings.CheckForUpdates = false
				scr.up.SetFeed(rf)
				scr.up.SetKey(pub)
				scr.up.SetDir(dir)
			})
			connect(r)
			r.click(ui.RoleButton, "Check now")
			r.wait("the release", func() bool { return r.has(ui.RoleButton, "Download update") })
			r.click(ui.RoleButton, "Download update")
			r.wait("the refusal", func() bool { return r.has(ui.RoleButton, "Retry download") })
			rf.mu.Lock()
			n := rf.fetched
			rf.mu.Unlock()
			if n != 0 {
				t.Errorf("the archive was requested %d times", n)
			}
		})
	}
	// the replay, with a signature over exactly what it names, fails on the name alone
	rel := old.rel
	rel.Version = "0.22.0"
	if err := verifyManifest(&rel, []ed25519.PublicKey{pub}); err == nil || !strings.Contains(err.Error(), "does not belong to the version") {
		t.Errorf("a validly signed older file under a newer version: %v", err)
	}
	if err := verifyManifest(&old.rel, []ed25519.PublicKey{pub}); err != nil {
		t.Errorf("the release as published: %v", err)
	}
	for _, v := range []string{"", "v", "../1", "1/2", "1 2", ".1"} {
		if releaseFileName(v) != "" {
			t.Errorf("releaseFileName(%q) = %q", v, releaseFileName(v))
		}
	}
	if releaseFileName("0.21.0") != "ZapFast-0.21.0.zip" || releaseFileName("v0.21.0") != "ZapFast-0.21.0.zip" {
		t.Error("the name of 0.21.0")
	}
}

// UP-004: after Prepare the staged bundle's own version must be the one the
// update announced; otherwise the staged copy is discarded and nothing installs.
func TestStagedVersionMustBeTheAnnouncedOne(t *testing.T) {
	for _, tc := range []struct {
		staged  string
		install bool
	}{{"0.20.5", false}, {"0.22.0", false}, {"0.21.0", true}, {"0.21", true}} {
		t.Run(tc.staged, func(t *testing.T) {
			self := &fakeSelf{stagedVersion: tc.staged}
			r, _, quits := installRig(t, self)
			readyToInstall(r)
			r.click(ui.RoleButton, "Restart to update")
			if tc.install {
				r.wait("the quit", func() bool { return quits.Load() == 1 })
				return
			}
			r.wait("the refusal", func() bool { return r.has(ui.RoleButton, "Retry download") })
			log := strings.Join(self.log(), " ")
			if !strings.HasSuffix(log, "prepare discard") || strings.Contains(log, "install") || quits.Load() != 0 {
				t.Errorf("calls %q, quits %d", log, quits.Load())
			}
			if !r.screenText("it announced, so it was not installed") {
				t.Error("the refusal is not explained")
			}
		})
	}
}

// UP-004: whatever a feed serves as latest.json, verification accepts it only
// for exactly the signed manifest under the version its file name carries;
// parsing and checking never panic.
func FuzzLatestJSON(f *testing.F) {
	pub, priv, _ := ed25519.GenerateKey(rand.New(rand.NewSource(1)))
	sum := sha256.Sum256([]byte("the update"))
	signed := Release{Version: "0.21.0", File: "ZapFast-0.21.0.zip", SHA256: hex.EncodeToString(sum[:])}
	signed.Signature = hex.EncodeToString(ed25519.Sign(priv, manifestLine(&signed)))
	b, _ := json.Marshal(signed)
	f.Add(b)
	f.Add([]byte(strings.Replace(string(b), "0.21.0", "0.22.0", 1)))
	f.Add([]byte(strings.Replace(string(b), signed.SHA256, strings.ToUpper(signed.SHA256), 1)))
	f.Add([]byte(`{"version":"v0.21.0","file":"ZapFast-0.21.0.zip"}`))
	f.Add([]byte(`null`))
	f.Fuzz(func(t *testing.T, b []byte) {
		rel, err := parseRelease(b)
		if err != nil || rel == nil {
			return
		}
		if verifyManifest(rel, []ed25519.PublicKey{pub}) != nil {
			return
		}
		if string(manifestLine(rel)) != string(manifestLine(&signed)) || rel.File != releaseFileName(rel.Version) {
			t.Fatalf("accepted %q: manifest %q, file %q for version %q", b, manifestLine(rel), rel.File, rel.Version)
		}
	})
}
