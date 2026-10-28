package settings

import (
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"

	"gonativeui/app/selfupdate"

	"zapfast/i18n"
	"zapfast/state"
	"zapfast/ui/screen"
)

// The installer (UP-003, UP-004, UP-006): the framework's app/selfupdate,
// behind a small interface so that tests never touch the real one.

// SelfUpdater is the part of app/selfupdate the updater uses; SetSystem
// replaces it (the tests of this package and of cmd/zapfast give a fake).
type SelfUpdater interface {
	// Supported says why this copy cannot replace itself; nil when it can.
	Supported() error
	// Prepare checks and unpacks the archive: the signature must be the
	// running app's own, the bundle its own and the version newer.
	Prepare(zip string) (StagedUpdate, error)
	// Started marks this run healthy, after the first frame.
	Started()
	// LastResult is what the update that led to this run came to.
	LastResult() selfupdate.Result
}

// StagedUpdate is a verified update waiting to be installed.
type StagedUpdate interface {
	// Version is the staged bundle's own version (CFBundleShortVersionString).
	Version() string
	// InstallAndRelaunch hands over to the helper, which waits for this
	// process to quit, swaps the bundles and starts the new one.
	InstallAndRelaunch(args []string) error
	// Discard removes the staged copy.
	Discard() error
}

type osSelf struct{}

func (osSelf) Supported() error { return selfupdate.Supported() }
func (osSelf) Prepare(zip string) (StagedUpdate, error) {
	s, err := selfupdate.Prepare(zip)
	if err != nil {
		return nil, err
	}
	return s, nil
}
func (osSelf) Started()                      { selfupdate.Started() }
func (osSelf) LastResult() selfupdate.Result { return selfupdate.LastResult() }

// SetSystem replaces the self-updater: tests only, so that none calls the real
// app/selfupdate.
func (u *Updates) SetSystem(s SelfUpdater) { u.mu.Lock(); u.sys = s; u.mu.Unlock() }

// SetQuit sets what quits the app through its normal path (the OnQuit hooks
// run, the settings are written); Screen.Init gives it the app's RequestQuit.
func (u *Updates) SetQuit(f func()) { u.mu.Lock(); u.quit = f; u.mu.Unlock() }

// The reasons a copy cannot update itself: upstream's wording
// (fastframe-update, detect.rs Unsupported), then ours for the cases it has no
// word for.
const (
	whyHomebrew     = "Update this installation with Homebrew."
	whyCargo        = "Update this installation with cargo install."
	whyNix          = "Update this installation with Nix."
	whyMove         = "Move the app to Applications, then open it to update."
	whyForeign      = "This app bundle is not a release of this app."
	whyPlatform     = "Use the download page for this operating system or architecture."
	whyInspect      = "Cannot inspect this installation: {reason}"
	whyNotWritable  = "Zap cannot replace itself here. Move it to a folder you can write to, such as Applications, or use the download page."
	whyNotSigned    = "This copy of Zap has no publisher signature, so an update cannot be checked against it. Use the download page."
	whyNotNewer     = "This update is not newer than the version you are running."
	whyRejected     = "The update was not installed: {reason}"
	whyPrepare      = "Could not prepare the update: {reason}"
	whyInstall      = "Could not start the update: {reason}"
	textRestored    = "The update could not start. The previous version has been restored."
	textUpdateFail  = "The update could not be installed: {reason}"
	textUpdated     = "Zap was updated to {version}."
	textRolledBack  = "Updating to {version} failed and Zap went back to the previous version: {reason}"
	textRolledBack2 = "Updating to {version} failed and Zap went back to the previous version."
)

// supportReason says why this copy cannot update itself, "" when it can. exe is
// the resolved executable; prefixes are the Homebrew prefixes whose Caskroom
// is searched (zf:fastframe-update detect.rs and macos.rs, in the same order:
// the paths, then the bundle, then the framework's own checks).
func supportReason(exe string, prefixes []string, supported error) string {
	lower := strings.ToLower(filepath.ToSlash(exe))
	switch {
	case strings.Contains(lower, "/.cargo/"):
		return i18n.T(whyCargo)
	case strings.HasPrefix(lower, "/nix/"):
		return i18n.T(whyNix)
	case strings.Contains(lower, "/cellar/"), strings.Contains(lower, "/caskroom/"):
		return i18n.T(whyHomebrew)
	}
	if bundle := bundleOf(exe); bundle != "" {
		for _, p := range prefixes {
			if caskOwns(filepath.Join(p, "Caskroom", "zapfast"), bundle) {
				return i18n.T(whyHomebrew)
			}
		}
	}
	return whySelf(supported)
}

// whySelf is the wording for what app/selfupdate refuses.
func whySelf(err error) string {
	switch {
	case err == nil:
		return ""
	case errors.Is(err, selfupdate.ErrNotBundled), errors.Is(err, selfupdate.ErrTranslocated), errors.Is(err, selfupdate.ErrReadOnly):
		return i18n.T(whyMove)
	case errors.Is(err, selfupdate.ErrDevBundle):
		return i18n.T(whyForeign)
	case errors.Is(err, selfupdate.ErrNotWritable):
		return i18n.T(whyNotWritable)
	case errors.Is(err, selfupdate.ErrNotSigned):
		return i18n.T(whyNotSigned)
	case errors.Is(err, errors.ErrUnsupported):
		return i18n.T(whyPlatform)
	}
	return i18n.Format(i18n.T(whyInspect), "{reason}", trimPrefix(err))
}

// trimPrefix is err's text without the package's "selfupdate: " prefix.
func trimPrefix(err error) string { return strings.TrimPrefix(err.Error(), "selfupdate: ") }

// bundleOf is the .app folder the executable sits in
// (<name>.app/Contents/MacOS/<exe>), "" otherwise.
func bundleOf(exe string) string {
	root := filepath.Dir(filepath.Dir(filepath.Dir(exe)))
	if filepath.Ext(root) == ".app" && filepath.Base(filepath.Dir(exe)) == "MacOS" {
		return root
	}
	return ""
}

// caskOwns reports whether a Homebrew cask folder links to the bundle: a cask
// moves the app into /Applications and keeps a link to it in each version's
// folder (macos.rs cask_owns).
func caskOwns(cask, bundle string) bool {
	real, err := filepath.EvalSymlinks(bundle)
	if err != nil {
		return false
	}
	versions, err := os.ReadDir(cask)
	if err != nil {
		return false
	}
	for _, v := range versions {
		if p, err := filepath.EvalSymlinks(filepath.Join(cask, v.Name(), "ZapFast.app")); err == nil && p == real {
			return true
		}
	}
	return false
}

// homebrewPrefixes are the prefixes upstream looks in.
func homebrewPrefixes() []string {
	p := []string{"/opt/homebrew", "/usr/local"}
	if h := os.Getenv("HOMEBREW_PREFIX"); h != "" {
		p = append(p, h)
	}
	return p
}

// probe works out, off the main thread, whether this copy can update itself:
// the Restart to update button is only offered when it can.
func (u *Updates) probe() {
	u.mu.Lock()
	if u.probed {
		u.mu.Unlock()
		return
	}
	u.probed = true
	sys := u.sys
	u.mu.Unlock()
	go func() {
		reason := ""
		exe, err := os.Executable()
		if err == nil {
			if p, e := filepath.EvalSymlinks(exe); e == nil {
				exe = p
			}
			reason = supportReason(exe, homebrewPrefixes(), sys.Supported())
		} else {
			reason = i18n.Format(i18n.T(whyInspect), "{reason}", err.Error())
		}
		u.mu.Lock()
		u.snap.Support, u.snap.SupportKnown = reason, true
		u.mu.Unlock()
		u.poke()
	}()
}

// describeInstall is the text for what failed between the verified download
// and the restart.
func describeInstall(err error) string {
	switch {
	case errors.Is(err, selfupdate.ErrNotNewer):
		return i18n.T(whyNotNewer)
	case errors.Is(err, selfupdate.ErrRejected):
		return i18n.Format(i18n.T(whyRejected), "{reason}", trimPrefix(err))
	}
	if why := whySelf(err); why != "" && isRefusal(err) {
		return why
	}
	return i18n.Format(i18n.T(whyPrepare), "{reason}", trimPrefix(err))
}

func isRefusal(err error) bool {
	for _, e := range []error{selfupdate.ErrNotBundled, selfupdate.ErrTranslocated, selfupdate.ErrReadOnly, selfupdate.ErrNotWritable, selfupdate.ErrDevBundle, selfupdate.ErrNotSigned} {
		if errors.Is(err, e) {
			return true
		}
	}
	return false
}

// verifyFile checks that the file at path is the release's, again: its digest
// against the release, and the release's signature under the publisher's key.
// The download is checked when it arrives and once more here, because it sits
// in a folder other processes of this user can write to until it is handed to
// selfupdate.Prepare.
func verifyFile(path string, rel *Release, keys []ed25519.PublicKey) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return err
	}
	return verifyRelease(rel, hex.EncodeToString(h.Sum(nil)), keys)
}

// selfInstall is the installer: the publisher's checks first (the download's
// digest and the signature on it, again), only then app/selfupdate's, then the
// hand-over to its helper and a normal quit.
func (u *Updates) selfInstall(path string) error {
	u.mu.Lock()
	rel, keys, sys, quit := u.verified, u.keys, u.sys, u.quit
	u.mu.Unlock()
	defer os.Remove(path) // the staged copy, not this file, is what installs
	if err := verifyFile(path, &rel, keys); err != nil {
		return err
	}
	if err := sys.Supported(); err != nil {
		return errors.New(describeInstall(err))
	}
	staged, err := sys.Prepare(path)
	if err != nil {
		return errors.New(describeInstall(err))
	}
	// the bundle must be the version the update announced and its signature
	// vouched for through the file's name: an older signed release must not
	// stand in for a newer one
	if got := staged.Version(); newer(got, rel.Version) || newer(rel.Version, got) {
		_ = staged.Discard()
		return errors.New(i18n.Format(i18n.T("The update is version {got}, not the {announced} it announced, so it was not installed."), "{got}", got, "{announced}", rel.Version))
	}
	if err := staged.InstallAndRelaunch(nil); err != nil {
		_ = staged.Discard()
		return errors.New(i18n.Format(i18n.T(whyInstall), "{reason}", trimPrefix(err)))
	}
	if quit != nil {
		quit()
	}
	return nil
}

// Frame is the updater's ticker: a few frames in, it tells selfupdate this run
// is healthy (the new version reports in, or the helper rolls it back) and
// shows what the update that led to this run came to. It runs on every main
// window frame and does nothing once it has run.
func (u *Updates) Frame(e *screen.Env) {
	if u.started.Load() {
		if u.noted.Load() || !u.resultReady.Load() {
			return
		}
		u.noted.Store(true)
		u.mu.Lock()
		r := u.result
		u.mu.Unlock()
		u.note(e.S, r)
		return
	}
	if e.S.Demo { // the demo installs nothing and reads nothing of the app's folders
		u.started.Store(true)
		return
	}
	// two builds reach the glass, as the shell starts the cores after the third
	if u.frames++; u.frames < 4 {
		return
	}
	u.started.Store(true)
	u.mu.Lock()
	sys := u.sys
	u.mu.Unlock()
	go func() { // file I/O: not on the main thread
		sys.Started()
		r := sys.LastResult()
		u.mu.Lock()
		u.result = r
		u.mu.Unlock()
		u.resultReady.Store(true)
		u.poke()
	}()
}

// note tells the user how the update that led to this run ended: upstream
// toasts a restored version (zf:src/main.rs, the helper's message); a version
// that was installed, or an update that stopped before changing anything,
// are ours.
func (u *Updates) note(s *state.State, r selfupdate.Result) {
	var line string
	switch r.Status {
	case selfupdate.Updated:
		line = i18n.Format(i18n.T(textUpdated), "{version}", r.To)
	case selfupdate.RolledBack:
		s.Toast([]byte(i18n.T(textRestored)), true)
		u.mu.Lock()
		u.snap.Last = rolledBackLine(r)
		u.mu.Unlock()
		return
	case selfupdate.Failed:
		s.Toast([]byte(i18n.Format(i18n.T(textUpdateFail), "{reason}", r.Reason)), true)
		u.mu.Lock()
		u.snap.Last = i18n.Format(i18n.T(textUpdateFail), "{reason}", r.Reason)
		u.mu.Unlock()
		return
	default:
		return
	}
	s.Toast([]byte(line), false)
}

// rolledBackLine is the Updates section's text for a rolled back update.
func rolledBackLine(r selfupdate.Result) string {
	if r.Reason == "" {
		return i18n.Format(i18n.T(textRolledBack2), "{version}", r.To)
	}
	return i18n.Format(i18n.T(textRolledBack), "{version}", r.To, "{reason}", r.Reason)
}
