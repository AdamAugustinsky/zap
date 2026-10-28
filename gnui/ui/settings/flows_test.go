package settings

import (
	"context"
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"gonativeui/app/fetch"
	"gonativeui/ui"
	"gonativeui/widget/widgettest"

	"zapfast/core/fake"
	"zapfast/lockhash"
	"zapfast/state"
)

// AL-001: the app lock password: at least six characters typed twice, changed
// with the current one, turned off with it; the explanations; the verifier is
// the one upstream stores.
func TestAppLock(t *testing.T) {
	r := newRig(t, 760, 1000, nil)
	connect(r)
	st := &r.s.Settings
	if r.has(ui.RoleComboBox, "Lock after") {
		t.Error("Lock after without a password")
	}
	r.click(ui.RoleButton, "Set password…")
	if !r.text("Set an app lock password") || r.has(ui.RoleTextField, "Current password") {
		t.Fatal("the set form is not open")
	}
	newName := "New password, at least 6 characters"
	r.typeIn(newName, "abc")
	r.typeIn("Type the new password again", "abc")
	r.click(ui.RoleButton, "Turn on")
	if !r.text("The password needs at least 6 characters.") || len(st.AppLockHash) != 0 {
		t.Error("a short password was accepted")
	}
	r.Play(widgettest.Click(ui.RoleTextField, newName), widgettest.Type("def"), widgettest.Idle(1))
	r.click(ui.RoleButton, "Turn on")
	if !r.text("The two new passwords are different.") {
		t.Error("a mismatch was accepted")
	}
	r.Play(widgettest.Click(ui.RoleTextField, "Type the new password again"), widgettest.Type("def"), widgettest.Idle(1))
	r.click(ui.RoleButton, "Turn on")
	r.wait("the lock", func() bool { return len(st.AppLockHash) > 0 })
	if !lockhash.VerifiesApp(string(st.AppLockHash), []byte("abcdef")) || lockhash.VerifiesApp(string(st.AppLockHash), []byte("abcdeg")) {
		t.Errorf("verifier %q", st.AppLockHash)
	}
	if !strings.HasPrefix(string(st.AppLockHash), "pbkdf2-sha256$600000$") || strings.Contains(string(st.AppLockHash), "abcdef") {
		t.Errorf("verifier %q is not upstream's form", st.AppLockHash)
	}
	if r.toast() != "App lock password saved" || r.has(ui.RoleTextField, newName) {
		t.Errorf("toast %q", r.toast())
	}
	if !r.has(ui.RoleComboBox, "Lock after") {
		t.Error("no Lock after with a password")
	}
	r.choose("Lock after", "After 1 hour")
	if st.AppLockAfter != state.LockOneHour {
		t.Errorf("lock after %v", st.AppLockAfter)
	}

	r.click(ui.RoleButton, "Change password…")
	r.typeIn("Current password", "wrong!")
	r.typeIn(newName, "123456")
	r.typeIn("Type the new password again", "123456")
	for _, name := range []string{"Current password", newName, "Type the new password again"} {
		if v := r.value(name); v != strings.Repeat("•", 6) {
			t.Errorf("%s shows %q", name, v)
		}
	}
	r.click(ui.RoleButton, "Change password")
	r.wait("the refusal", func() bool { return r.text("Wrong password. Try again.") })
	if !lockhash.VerifiesApp(string(st.AppLockHash), []byte("abcdef")) {
		t.Error("the password changed on a wrong current one")
	}
	r.typeIn("Current password", "abcdef")
	r.typeIn(newName, "123456")
	r.typeIn("Type the new password again", "123456")
	r.click(ui.RoleButton, "Change password")
	r.wait("the new password", func() bool { return lockhash.VerifiesApp(string(st.AppLockHash), []byte("123456")) })

	r.click(ui.RoleButton, "Turn off…")
	r.typeIn("Current password", "123456")
	r.click(ui.RoleButton, "Turn off")
	r.wait("the lock off", func() bool { return len(st.AppLockHash) == 0 })
	if r.toast() != "App lock turned off" {
		t.Errorf("toast %q", r.toast())
	}
}

// updateRig is a window whose updater reads a folder feed signed by a key of
// the test's.
type feed struct {
	dir    string
	key    ed25519.PrivateKey
	rel    Release
	reload func()
}

func makeFeed(t *testing.T, version string, body []byte) (*feed, ed25519.PublicKey) {
	t.Helper()
	pub, priv, _ := ed25519.GenerateKey(nil)
	f := &feed{dir: t.TempDir(), key: priv}
	sum := sha256.Sum256(body)
	f.rel = Release{Version: version, URL: "https://example.test/release", Notes: "Faster.", File: "ZapFast-" + version + ".zip", SHA256: hex.EncodeToString(sum[:])}
	f.rel.Signature = hex.EncodeToString(ed25519.Sign(priv, manifestLine(&f.rel)))
	if err := os.WriteFile(filepath.Join(f.dir, f.rel.File), body, 0o600); err != nil {
		t.Fatal(err)
	}
	f.write(t)
	return f, pub
}

func (f *feed) write(t *testing.T) {
	b, _ := json.Marshal(f.rel)
	if err := os.WriteFile(filepath.Join(f.dir, "latest.json"), b, 0o600); err != nil {
		t.Fatal(err)
	}
}

func updRig(t *testing.T, f *feed, pub ed25519.PublicKey, install Installer) *rig {
	return newRig(t, 760, 1100, func(s *state.State, _ *fake.Core, scr *Screen) {
		s.Settings.CheckForUpdates = false // a check is the test's to start
		scr.up.SetFeed(NewFeed(f.dir))
		scr.up.SetKey(pub)
		scr.up.SetDir(t.TempDir())
		scr.up.SetInstaller(install)
	})
}

// UP-001, UP-002, UP-003: a check finds the release; the update block shows
// it; the download is verified; Restart to update installs the file.
func TestUpdateFlow(t *testing.T) {
	body := []byte(strings.Repeat("payload", 1000))
	f, pub := makeFeed(t, "0.20.0", body)
	var got string
	var read []byte
	r := updRig(t, f, pub, func(path string) error { got = path; read, _ = os.ReadFile(path); return nil })
	connect(r)
	if !r.text("Not checked yet") {
		t.Error("no initial status")
	}
	if r.has(ui.RoleButton, "Download update") {
		t.Error("an update before any check")
	}
	if !r.text("Updates come from a local test feed until the first release feed exists.") {
		t.Error("Settings does not say the feed is a test feed")
	}
	r.click(ui.RoleButton, "Check now")
	r.wait("the release", func() bool { return r.text("Update to 0.20.0") && r.has(ui.RoleButton, "Download update") })
	if !r.text("0.19.0 → 0.20.0") || !r.has(ui.RoleLink, "Release notes") {
		t.Error("the update block lacks the versions or the notes")
	}
	// the banner of the main window reads the same
	if rel, ok := r.scr.Updates().Available(); !ok || rel.Version != "0.20.0" {
		t.Errorf("available %v %v", rel, ok)
	}
	r.click(ui.RoleButton, "Download update")
	r.wait("the download", func() bool { return r.has(ui.RoleButton, "Restart to update") })
	if !r.text("Ready to install") {
		t.Error("no Ready to install")
	}
	r.click(ui.RoleButton, "Restart to update")
	r.wait("the installer", func() bool { return got != "" })
	if string(read) != string(body) {
		t.Error("the installer was not given the downloaded file")
	}
	r.wait("preparing", func() bool { return r.text("Preparing to restart…") })
}

// UP-004: a download is installed only if its digest and the signature on it
// check out; otherwise the update stops and can be retried.
func TestUpdateIsVerified(t *testing.T) {
	body := []byte("the real update")
	for _, bad := range []struct {
		name   string
		break_ func(f *feed, t *testing.T)
		want   string
	}{
		{"digest", func(f *feed, t *testing.T) {
			if err := os.WriteFile(filepath.Join(f.dir, f.rel.File), []byte("tampered"), 0o600); err != nil {
				t.Fatal(err)
			}
		}, "does not match its checksum"},
		{"signature", func(f *feed, t *testing.T) {
			_, other, _ := ed25519.GenerateKey(nil)
			f.rel.Signature = hex.EncodeToString(ed25519.Sign(other, manifestLine(&f.rel)))
			f.write(t)
		}, "signature is not valid"},
		{"unsigned", func(f *feed, t *testing.T) { f.rel.Signature = ""; f.write(t) }, "is not signed"},
	} {
		t.Run(bad.name, func(t *testing.T) {
			f, pub := makeFeed(t, "0.20.0", body)
			bad.break_(f, t)
			installed := false
			r := updRig(t, f, pub, func(string) error { installed = true; return nil })
			connect(r)
			r.click(ui.RoleButton, "Check now")
			r.wait("the release", func() bool { return r.has(ui.RoleButton, "Download update") })
			r.click(ui.RoleButton, "Download update")
			r.wait("the failure", func() bool { return r.has(ui.RoleButton, "Retry download") })
			if !r.has(ui.RoleButton, "Retry download") || r.has(ui.RoleButton, "Restart to update") || installed {
				t.Error("an unverified update can be installed")
			}
			found := false
			for _, n := range r.Tree().Nodes {
				found = found || strings.Contains(string(r.Tree().Text(n.Name)), bad.want)
			}
			if !found {
				t.Errorf("the failure is not explained (%q)", bad.want)
			}
		})
	}
}

// UP-001: it asks once a day, only if the setting is on, sending only its
// version; with nothing newer it says it is up to date.
func TestUpdateCheckIsDailyAndSendsOnlyTheVersion(t *testing.T) {
	var mu sync.Mutex
	var agents []string
	var queries []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		mu.Lock()
		agents = append(agents, req.Header.Get("User-Agent"))
		queries = append(queries, req.URL.RawQuery+req.Header.Get("Cookie")+req.Header.Get("Authorization"))
		mu.Unlock()
		w.Write([]byte(`{"version":"` + state.Version + `"}`))
	}))
	defer srv.Close()
	u := NewUpdates()
	u.SetSystem(&fakeSelf{}) // never the real app/selfupdate
	u.SetFeed(NewFeed(srv.URL))
	now := stateTime()
	u.now = func() time.Time { return now }
	st := state.DefaultSettings()
	waitDone := func() {
		for i := 0; i < 500 && u.Snapshot().Checking; i++ {
			time.Sleep(5 * time.Millisecond)
		}
	}
	st.CheckForUpdates = false
	u.CheckIfDue(&st)
	waitDone()
	mu.Lock()
	n := len(agents)
	mu.Unlock()
	if n != 0 {
		t.Fatal("it asked with Check for updates off")
	}
	st.CheckForUpdates = true
	u.CheckIfDue(&st)
	waitDone()
	if s := u.Snapshot(); !s.Checked || s.Release.Version != "" {
		t.Errorf("%+v", s)
	}
	now = now.Add(23 * time.Hour)
	u.CheckIfDue(&st)
	waitDone()
	now = now.Add(2 * time.Hour)
	u.CheckIfDue(&st)
	waitDone()
	mu.Lock()
	defer mu.Unlock()
	if len(agents) != 2 {
		t.Errorf("%d requests; want one a day", len(agents))
	}
	for i, a := range agents {
		if a != "ZapFast/"+state.Version || queries[i] != "" {
			t.Errorf("request %d sent %q %q", i, a, queries[i])
		}
	}
}

// UP-005: Download updates automatically fetches and verifies without a click;
// restarting stays the person's choice.
func TestAutomaticDownload(t *testing.T) {
	f, pub := makeFeed(t, "0.20.0", []byte("update"))
	installed := false
	r := updRig(t, f, pub, func(string) error { installed = true; return nil })
	connect(r)
	st := &r.s.Settings
	st.CheckForUpdates, st.AutoUpdate = true, true
	r.scr.up.CheckIfDue(st)
	r.wait("the release", func() bool { return r.scr.up.Snapshot().Release.Version != "" })
	r.scr.up.CheckIfDue(st)
	r.wait("the download", func() bool { return r.has(ui.RoleButton, "Restart to update") })
	if installed {
		t.Error("it restarted by itself")
	}
}

func stateTime() time.Time { return time.Date(2026, 10, 20, 12, 0, 0, 0, time.UTC) }

// UP-001: with Check for updates on, the window's updater asks by itself.
func TestUpdateChecksByItself(t *testing.T) {
	f, pub := makeFeed(t, "0.20.0", []byte("update"))
	r := updRig(t, f, pub, nil)
	r.s.Settings.CheckForUpdates = true
	connect(r)
	r.wait("the release", func() bool { return r.text("Update to 0.20.0") })
}

// The state saves the settings when they change: what the window changes is
// still there at the next start.
func TestSettingsPersistAcrossARestart(t *testing.T) {
	dir := t.TempDir()
	paths := state.Paths{Config: dir, Data: dir}
	load := func(s *state.State) {
		l, ok := state.LoadSettings(paths).Wait(5 * time.Second)
		if !ok {
			t.Fatal("the settings did not load")
		}
		s.UseSettings(l)
	}
	r := newRigOn(t, 760, 1400, load, nil)
	connect(r)
	was, wasTyping := r.s.Settings.CheckForUpdates, r.acct().Settings.SendTyping
	r.click(ui.RoleSwitch, "Check for updates")
	r.click(ui.RoleSwitch, "Show when you are typing")
	if r.s.Settings.CheckForUpdates == was || r.acct().Settings.SendTyping == wasTyping {
		t.Fatal("the switches did not change the settings")
	}
	now, nowTyping := r.s.Settings.CheckForUpdates, r.acct().Settings.SendTyping
	r.s.CloseSettings() // the quit

	s2 := state.New()
	l, ok := state.LoadSettings(paths).Wait(5 * time.Second)
	if !ok {
		t.Fatal("the settings did not load again")
	}
	s2.UseSettings(l)
	a := s2.AddAccount("1", nil, fake.New(nil))
	if s2.Settings.CheckForUpdates != now || a.Settings.SendTyping != nowTyping {
		t.Errorf("after a restart: check for updates %v (was set to %v), typing %v (was set to %v)",
			s2.Settings.CheckForUpdates, now, a.Settings.SendTyping, nowTyping)
	}
	s2.CloseSettings()
}

// zeroed reports whether every byte the buffer's storage can hold is zero.
func zeroed(b []byte) bool {
	for _, x := range b[:cap(b)] {
		if x != 0 {
			return false
		}
	}
	return true
}

// Every password field is a secure field: bullets only in the semantic tree,
// the secure subrole, secure keyboard entry while it has the keyboard, and
// what was typed is wiped (its storage too) on submit and when the window
// closes.
func TestPasswordFieldsAreSecureAndWiped(t *testing.T) {
	r := newRig(t, 760, 1000, nil)
	connect(r)
	st := &r.s.Settings
	const newName = "New password, at least 6 characters"
	const againName = "Type the new password again"
	secureField := func(name, typed string) {
		t.Helper()
		r.typeIn(name, typed)
		n := r.Must(ui.RoleTextField, name)
		if n.State&ui.SemSecure == 0 {
			t.Errorf("%s: not a secure text field", name)
		}
		if v := r.value(name); v != strings.Repeat("•", len(typed)) || strings.Contains(widgettest.Dump(r.Tree()), typed) {
			t.Errorf("%s: announces %q", name, v)
		}
		if !r.Context().SecureFieldFocused() {
			t.Errorf("%s: no secure keyboard entry with the field in focus", name)
		}
	}
	// The app lock form: submit wipes all three.
	r.click(ui.RoleButton, "Set password…")
	secureField(newName, "abcdef")
	secureField(againName, "abcdef")
	newBuf, againBuf := r.scr.lock.password.Bytes(), r.scr.lock.again.Bytes()
	r.click(ui.RoleButton, "Turn on")
	r.wait("the lock", func() bool { return len(st.AppLockHash) > 0 })
	if r.scr.lock.password.Len() != 0 || r.scr.lock.again.Len() != 0 || !zeroed(newBuf) || !zeroed(againBuf) {
		t.Error("the app lock form kept the passwords after Turn on")
	}
	// Change password: the current password too; Cancel and close wipe.
	r.click(ui.RoleButton, "Change password…")
	secureField("Current password", "abcdef")
	cur := r.scr.lock.current.Bytes()
	r.click(ui.RoleButton, "Cancel")
	if r.scr.lock.current.Len() != 0 || !zeroed(cur) {
		t.Error("Cancel left the current password")
	}
	r.click(ui.RoleButton, "Change password…")
	secureField("Current password", "abcdef")
	secureField(newName, "ghijkl")
	// The locked chats code is secure too.
	secureField("Locked chats code", "secret")
	cur, newBuf, code := r.scr.lock.current.Bytes(), r.scr.lock.password.Bytes(), r.scr.chatLock.Bytes()
	r.win.Closed()
	if r.scr.lock.current.Len() != 0 || r.scr.lock.password.Len() != 0 || !zeroed(cur) || !zeroed(newBuf) {
		t.Error("closing the window left the app lock passwords")
	}
	if r.scr.chatLock.Len() != 0 || !zeroed(code) {
		t.Error("closing the window left the locked chats code")
	}
}

// SE-001, SE-003: Escape steps back: it clears the search, then closes the
// window.
func TestEscapeStepsBack(t *testing.T) {
	r := newRig(t, 760, 900, nil)
	connect(r)
	r.s.Win.Open[state.WinSettings] = true
	esc := func() { r.Play(widgettest.Key(ui.KeyEscape, 0), widgettest.Idle(2)) }
	r.Play(widgettest.Click(ui.RoleSearchField, "Search settings"), widgettest.Type("sound"), widgettest.Idle(2))
	r.Play(widgettest.Key(ui.KeyEscape, 0), widgettest.Idle(2))
	if r.s.Win.Shut[state.WinSettings] {
		t.Fatal("Escape closed the window with a search to clear")
	}
	esc()
	if !r.s.Win.Shut[state.WinSettings] {
		t.Error("Escape did not close the window")
	}
}

// The updater goes through the app's proxy as upstream's does: the setting,
// else ALL_PROXY or HTTPS_PROXY unless NO_PROXY names the feed's host.
func TestUpdaterProxy(t *testing.T) {
	for _, tc := range []struct {
		in   string
		want *fetch.Proxy
	}{
		{"", nil},
		{"proxy.lan:3128", &fetch.Proxy{Kind: fetch.ProxyHTTP, Host: "proxy.lan", Port: 3128}},
		{"http://ada:p%40ss@proxy.lan", &fetch.Proxy{Kind: fetch.ProxyHTTP, Host: "proxy.lan", Port: 80, User: "ada", Password: "p@ss"}},
		{"socks5h://127.0.0.1:9050", &fetch.Proxy{Kind: fetch.ProxySOCKS5, Host: "127.0.0.1", Port: 9050}},
		{"socks5://[::1]", &fetch.Proxy{Kind: fetch.ProxySOCKS5, Host: "::1", Port: 1080}},
		{"ftp://x:1", nil},
	} {
		if got := parseProxy(tc.in); (got == nil) != (tc.want == nil) || got != nil && *got != *tc.want {
			t.Errorf("parseProxy(%q) = %+v, want %+v", tc.in, got, tc.want)
		}
	}
	u := NewUpdates()
	u.SetSystem(&fakeSelf{}) // never the real app/selfupdate
	t.Setenv("ALL_PROXY", "")
	t.Setenv("HTTPS_PROXY", "socks5://env.lan:1080")
	t.Setenv("NO_PROXY", "")
	if p := u.currentProxy("feeds.example.com"); p == nil || p.Host != "env.lan" {
		t.Errorf("environment proxy %+v", p)
	}
	t.Setenv("NO_PROXY", "localhost, .example.com")
	if p := u.currentProxy("feeds.example.com"); p == nil || p.Kind != fetch.ProxyNone {
		t.Errorf("NO_PROXY does not force a direct connection: %+v", p)
	}
	if p := u.currentProxy("other.org"); p == nil || p.Host != "env.lan" {
		t.Errorf("a host NO_PROXY does not name: %+v", p)
	}
	st := state.DefaultSettings()
	st.Proxy = []byte("proxy.lan:3128")
	u.CheckIfDue(&st)
	if p := u.currentProxy("feeds.example.com"); p == nil || p.Host != "proxy.lan" {
		t.Errorf("the setting does not win: %+v", p)
	}
}

// The release page the feed names is opened only if it is an https address:
// the feed's signature does not cover it.
func TestReleaseURLIsHTTPSOrTheReleasesPage(t *testing.T) {
	for _, bad := range []string{
		"file:///Applications/Calculator.app", "javascript:alert(1)", "x-man-page://ls", "tel:+5511999990000",
		"/releases/v0.20.0", "releases/v0.20.0", "http://example.test/release", "", "   ", "https://", "https:///x",
		"https://github.com@evil.example/x", "HTTP://example.test", "mailto:a@example.test", "https:example.test",
	} {
		if got := releaseURL(bad); got != releasesPage {
			t.Errorf("releaseURL(%q) = %q, want the releases page", bad, got)
		}
		rel, err := parseRelease([]byte(`{"version":"0.20.0","url":` + strconv.Quote(bad) + `}`))
		if err != nil || rel == nil || rel.URL != releasesPage {
			t.Errorf("parseRelease with url %q: %+v, %v", bad, rel, err)
		}
	}
	if got := releaseURL("https://github.com/crmne/zapfast/releases/tag/v0.20.0"); got != "https://github.com/crmne/zapfast/releases/tag/v0.20.0" {
		t.Errorf("a GitHub release page: %q", got)
	}
	if got := releaseURL("HTTPS://Example.test/r?x=1"); got != "https://Example.test/r?x=1" {
		t.Errorf("an https address: %q", got)
	}
}

// Release notes opens the releases page when the feed's address is not https.
func TestReleaseNotesNeverOpensAFileURL(t *testing.T) {
	f, pub := makeFeed(t, "0.20.0", []byte("update"))
	f.rel.URL = "file:///Applications/Calculator.app"
	f.write(t)
	r := updRig(t, f, pub, nil)
	connect(r)
	r.click(ui.RoleButton, "Check now")
	r.wait("the release", func() bool { return r.has(ui.RoleLink, "Release notes") })
	r.click(ui.RoleLink, "Release notes")
	sh := r.s.Shell.(*fakeShell)
	r.wait("the link", func() bool { return len(sh.all()) == 1 })
	if got := sh.all()[0]; got != releasesPage {
		t.Errorf("opened %q", got)
	}
}

// recordingFeed is a feed that says what it is asked for.
type recordingFeed struct {
	rel     Release
	mu      sync.Mutex
	fetched int
}

func (f *recordingFeed) Latest(context.Context) (*Release, error) { r := f.rel; return &r, nil }
func (f *recordingFeed) Fetch(ctx context.Context, r *Release, w io.Writer, limit int64, progress func(int64, int64)) error {
	f.mu.Lock()
	f.fetched++
	f.mu.Unlock()
	return errors.New("fetched")
}

// UP-004: a release whose signature is bad (or missing, or on a mangled
// manifest) is never downloaded at all: nothing unauthenticated is fetched.
func TestBadSignatureNeverStartsTheDownload(t *testing.T) {
	good, pub := makeFeed(t, "0.20.0", []byte("update"))
	_, other, _ := ed25519.GenerateKey(nil)
	for name, mangle := range map[string]func(r *Release){
		"signed by another key": func(r *Release) { r.Signature = hex.EncodeToString(ed25519.Sign(other, manifestLine(r))) },
		"unsigned":              func(r *Release) { r.Signature = "" },
		"short signature":       func(r *Release) { r.Signature = r.Signature[:20] },
		"another file name":     func(r *Release) { r.File = "evil.zip" },
		"another checksum":      func(r *Release) { r.SHA256 = strings.Repeat("0", 64) },
		"no checksum":           func(r *Release) { r.SHA256 = "" },
	} {
		t.Run(name, func(t *testing.T) {
			rf := &recordingFeed{rel: good.rel}
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
			if left, _ := os.ReadDir(dir); len(left) != 0 {
				t.Errorf("files written: %v", left)
			}
		})
	}
}

// UP-004: an archive over the cap is stopped at the cap, whether the server
// says how long it is or not, and leaves no file.
func TestOversizedUpdateStopsAtTheCap(t *testing.T) {
	old := maxUpdateBytes
	maxUpdateBytes = 64 << 10
	t.Cleanup(func() { maxUpdateBytes = old })
	body := make([]byte, 1<<20)
	f, pub := makeFeed(t, "0.20.0", body)
	serve := func(announce bool) *httptest.Server {
		return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
			if strings.HasSuffix(req.URL.Path, "latest.json") {
				b, _ := json.Marshal(f.rel)
				w.Write(b)
				return
			}
			if announce {
				w.Header().Set("Content-Length", strconv.Itoa(len(body)))
			}
			for i := 0; i < len(body); i += 16 << 10 { // chunked when the length is not announced
				if _, err := w.Write(body[i : i+16<<10]); err != nil {
					return
				}
				if fl, ok := w.(http.Flusher); ok {
					fl.Flush()
				}
			}
		}))
	}
	for name, feed := range map[string]func(t *testing.T) Feed{
		"announced length": func(t *testing.T) Feed { s := serve(true); t.Cleanup(s.Close); return NewFeed(s.URL) },
		"unknown length":   func(t *testing.T) Feed { s := serve(false); t.Cleanup(s.Close); return NewFeed(s.URL) },
		"a folder":         func(t *testing.T) Feed { return NewFeed(f.dir) },
	} {
		t.Run(name, func(t *testing.T) {
			dir := t.TempDir()
			r := newRig(t, 760, 1100, func(s *state.State, _ *fake.Core, scr *Screen) {
				s.Settings.CheckForUpdates = false
				scr.up.SetFeed(feed(t))
				scr.up.SetKey(pub)
				scr.up.SetDir(dir)
			})
			connect(r)
			r.click(ui.RoleButton, "Check now")
			r.wait("the release", func() bool { return r.has(ui.RoleButton, "Download update") })
			r.click(ui.RoleButton, "Download update")
			r.wait("the refusal", func() bool { return r.has(ui.RoleButton, "Retry download") })
			if !r.screenText("is larger than Zap allows") {
				t.Error("the refusal is not explained")
			}
			if left, _ := os.ReadDir(dir); len(left) != 0 {
				t.Errorf("files left behind: %v", left)
			}
			if r.scr.up.Snapshot().State == DownloadReady {
				t.Error("an oversized update is ready to install")
			}
			if n := r.scr.up.Snapshot().Received; n > maxUpdateBytes {
				t.Errorf("%d bytes were taken in, over the cap of %d", n, maxUpdateBytes)
			}
		})
	}
}
