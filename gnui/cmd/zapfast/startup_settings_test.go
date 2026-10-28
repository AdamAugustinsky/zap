package main

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"gonativeui/app"
	"gonativeui/ui"
	"gonativeui/widget/widgettest"

	"zapfast/core/fake"
	"zapfast/i18n"
	"zapfast/state"
	"zapfast/ui/shell"
)

// A store held past the old deadline, or failing until the user retries.
type fakeSettingsReader struct {
	ready   chan struct{}
	result  *state.Loaded
	failure *state.Loaded
	retries int
}

func (f *fakeSettingsReader) Wait(d time.Duration) (*state.Loaded, bool) {
	if f.failure != nil && f.retries == 0 {
		return f.failure, true
	}
	select {
	case <-f.ready:
		return f.result, true
	default:
	}
	if d <= 0 {
		return nil, false
	}
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-f.ready:
		return f.result, true
	case <-timer.C:
		return nil, false
	}
}

func (f *fakeSettingsReader) Done() <-chan struct{} {
	if f.failure != nil && f.retries == 0 {
		done := make(chan struct{})
		close(done)
		return done
	}
	return f.ready
}

func (f *fakeSettingsReader) Retry() { f.retries++ }

func startupFixture(t *testing.T, locked bool) *state.Loaded {
	t.Helper()
	t.Cleanup(i18n.SetSystemLanguages(func() []string { return []string{"en"} }))
	i18n.SetLocale(i18n.English)
	t.Cleanup(func() { i18n.SetLocale(i18n.English) })
	dir := t.TempDir()
	body := `{"interface_language":"de"}`
	if locked {
		body = `{"app_lock_hash":"test-verifier","interface_language":"de"}`
	}
	if err := os.WriteFile(filepath.Join(dir, "settings.json"), []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	l, ok := state.LoadSettings(state.Paths{Config: dir, Data: dir}).Wait(5 * time.Second)
	if !ok || l.ReadErr != nil {
		t.Fatal("fixture did not load")
	}
	s := state.New()
	s.UseSettings(l)
	t.Cleanup(s.CloseSettings)
	return l
}

func assertStartupPrivate(t *testing.T, r *widgettest.Rig, v *settingsStartup) {
	t.Helper()
	if v.loaded != nil {
		t.Fatal("settings accepted before a successful read")
	}
	for _, item := range []struct {
		role ui.Role
		name string
	}{{ui.RoleListItem, "Ada Lovelace"}, {ui.RoleButton, "Chats"}, {ui.RoleButton, "Settings"}} {
		if _, ok := r.Find(item.role, item.name); ok {
			t.Fatalf("startup exposed %q", item.name)
		}
	}
}

func assertStartupRoutes(t *testing.T, s *state.State, locked bool) {
	t.Helper()
	if s == nil {
		t.Fatal("startup discarded the settings result")
	}
	if len(s.Accounts) != 0 {
		t.Fatal("startup opened accounts before handing over the loaded state")
	}
	s.Demo = true
	f := fake.New(nil)
	linked(f)
	s.AddAccount("1", nil, f)
	sh, err := shell.New(s, testScreens(true))
	if err != nil {
		t.Fatal(err)
	}
	r := widgettest.Open(t, shell.Config(s), 1100, 720, 1, sh)
	r.Frames(3)
	_, password := r.Find(ui.RoleTextField, "Password")
	_, chats := r.Find(ui.RoleListItem, "Ada Lovelace")
	if s.Lock.Locked != locked || password != locked || chats == locked {
		t.Fatalf("locked=%v, password=%v, private chat=%v; want locked=%v", s.Lock.Locked, password, chats, locked)
	}
}

func TestSlowSettingsNeverBypassTheStartupLock(t *testing.T) {
	f := &fakeSettingsReader{ready: make(chan struct{}), result: startupFixture(t, true)}
	start := time.Now()
	shown := false
	s := startState(f, true, func(v *settingsStartup) error {
		shown = true
		if time.Since(start) < 2*time.Second {
			t.Fatal("test did not cross the old two-second cutoff")
		}
		r := widgettest.Open(t, app.Config{}, 480, 260, 1, v)
		r.Frames(3)
		assertStartupPrivate(t, r, v)
		if _, ok := r.Find(ui.RoleLabel, "Loading…"); !ok {
			t.Fatal("no loading placeholder")
		}
		close(f.ready)
		r.Frames(2)
		return nil
	})
	if !shown || s == nil || string(s.Settings.InterfaceLanguage) != "de" {
		t.Fatal("late settings were discarded instead of applied")
	}
	assertStartupRoutes(t, s, true)
}

func TestFailedSettingsStayPrivateUntilRetry(t *testing.T) {
	f := &fakeSettingsReader{ready: make(chan struct{}), result: startupFixture(t, true), failure: &state.Loaded{ReadErr: os.ErrPermission}}
	close(f.ready)
	shown := false
	s := startState(f, true, func(v *settingsStartup) error {
		shown = true
		r := widgettest.Open(t, app.Config{}, 480, 260, 1, v)
		r.Frames(3)
		assertStartupPrivate(t, r, v)
		if _, ok := r.Find(ui.RoleLabel, "Could not read settings. Try again."); !ok {
			t.Fatal("no recoverable settings error")
		}
		r.Play(widgettest.Click(ui.RoleButton, "Retry"), widgettest.Idle(3))
		return nil
	})
	if !shown || f.retries != 1 || s == nil || string(s.Settings.AppLockHash) != "test-verifier" {
		t.Fatal("failed settings were accepted or retry did not recover")
	}
	assertStartupRoutes(t, s, true)
}

func TestClosingFailedSettingsDoesNotStartAccounts(t *testing.T) {
	f := &fakeSettingsReader{failure: &state.Loaded{ReadErr: os.ErrPermission}}
	if s := startState(f, true, func(*settingsStartup) error { return nil }); s != nil {
		t.Fatal("closing the error window accepted default settings")
	}
}

func TestReadySettingsKeepTheFastStartupPath(t *testing.T) {
	f := &fakeSettingsReader{ready: make(chan struct{}), result: startupFixture(t, false)}
	close(f.ready)
	s := startState(f, true, func(*settingsStartup) error {
		t.Fatal("fast startup opened the placeholder")
		return nil
	})
	if s == nil || string(s.Settings.InterfaceLanguage) != "de" {
		t.Fatal("fast startup did not use the loaded settings")
	}
	assertStartupRoutes(t, s, false)
}

func TestUnreadableAccountSettingsStayBehindStartupRetry(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "accounts", "1", "settings.json")
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	body := []byte(`{"send_read_receipts":false,"send_typing":false,"notifications":false}`)
	if err := os.WriteFile(path, body, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(path, 0); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.Chmod(path, 0o600) })
	if _, err := os.ReadFile(path); err == nil {
		t.Skip("this host does not enforce fixture file permissions")
	}
	t.Cleanup(i18n.SetSystemLanguages(func() []string { return []string{"en"} }))
	shown := false
	s := startState(state.LoadSettings(state.Paths{Config: dir, Data: dir}), true, func(v *settingsStartup) error {
		shown = true
		r := widgettest.Open(t, app.Config{}, 480, 260, 1, v)
		r.Frames(3)
		assertStartupPrivate(t, r, v)
		if _, ok := r.Find(ui.RoleLabel, "Could not read settings. Try again."); !ok {
			t.Fatal("no account settings error")
		}
		if err := os.Chmod(path, 0o600); err != nil {
			t.Fatal(err)
		}
		if got, err := os.ReadFile(path); err != nil || string(got) != string(body) {
			t.Fatal("startup changed the account's unreadable settings")
		}
		r.Play(widgettest.Click(ui.RoleButton, "Retry"))
		for end := time.Now().Add(5 * time.Second); v.loaded == nil && time.Now().Before(end); {
			r.Frames(1)
			time.Sleep(time.Millisecond)
		}
		return nil
	})
	if s != nil {
		t.Cleanup(s.CloseSettings)
	}
	if !shown || s == nil || len(s.Accounts) != 0 {
		t.Fatal("account settings bypassed startup recovery")
	}
	a := s.AddAccount("1", nil, fake.New(nil))
	if a.Settings.SendReadReceipts || a.Settings.SendTyping || a.Settings.Notifications {
		t.Fatal("the recovered account lost its privacy settings")
	}
}

func TestUnreadableRosterStaysBehindStartupRetry(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "accounts.json")
	body := []byte(`{"next_id":8,"active":"7","order":["7"]}`)
	if err := os.WriteFile(path, body, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(path, 0); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.Chmod(path, 0o600) })
	if _, err := os.ReadFile(path); err == nil {
		t.Skip("this host does not enforce fixture file permissions")
	}
	t.Cleanup(i18n.SetSystemLanguages(func() []string { return []string{"en"} }))
	shown := false
	s := startState(state.LoadSettings(state.Paths{Config: dir, Data: dir}), true, func(v *settingsStartup) error {
		shown = true
		r := widgettest.Open(t, app.Config{}, 480, 260, 1, v)
		r.Frames(3)
		assertStartupPrivate(t, r, v)
		if _, ok := r.Find(ui.RoleLabel, "Could not read the account list. Try again."); !ok {
			t.Fatal("no recoverable roster error")
		}
		// Retry while still unreadable must stay behind the same error screen.
		r.Play(widgettest.Click(ui.RoleButton, "Retry"))
		for end := time.Now().Add(5 * time.Second); time.Now().Before(end); {
			r.Frames(1)
			if l, ok := v.loading.Wait(0); ok && l.ReadErr != nil {
				break
			}
			time.Sleep(time.Millisecond)
		}
		if l, ok := v.loading.Wait(0); !ok || l.ReadErr == nil {
			t.Fatal("the unreadable roster did not return to the error state")
		}
		assertStartupPrivate(t, r, v)
		if err := os.Chmod(path, 0o600); err != nil {
			t.Fatal(err)
		}
		if got, err := os.ReadFile(path); err != nil || string(got) != string(body) {
			t.Fatal("failed retry changed accounts.json")
		}
		r.Frames(2)
		r.Play(widgettest.Click(ui.RoleButton, "Retry"))
		for end := time.Now().Add(5 * time.Second); v.loaded == nil && time.Now().Before(end); {
			r.Frames(1)
			time.Sleep(time.Millisecond)
		}
		return nil
	})
	if s != nil {
		t.Cleanup(s.CloseSettings)
	}
	if !shown || s == nil || len(s.Accounts) != 0 || s.Roster().Active != "7" {
		t.Fatal("roster failure bypassed startup recovery or lost the real account")
	}
}

func TestMalformedRosterStaysBehindStartupRetry(t *testing.T) {
	for name, body := range map[string]string{
		"syntax":       `{"next_id":`,
		"null":         `null`,
		"empty object": `{}`,
		"null order":   `{"next_id":8,"active":"7","order":null}`,
	} {
		t.Run(name, func(t *testing.T) { checkMalformedRosterStartup(t, []byte(body)) })
	}
}

func checkMalformedRosterStartup(t *testing.T, body []byte) {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "accounts.json")
	if err := os.WriteFile(path, body, 0o600); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(i18n.SetSystemLanguages(func() []string { return []string{"en"} }))
	shown := false
	s := startState(state.LoadSettings(state.Paths{Config: dir, Data: dir}), true, func(v *settingsStartup) error {
		shown = true
		r := widgettest.Open(t, app.Config{}, 480, 260, 1, v)
		for range 2 {
			for end := time.Now().Add(5 * time.Second); time.Now().Before(end); {
				r.Frames(1)
				if l, ok := v.loading.Wait(0); ok && l.ReadErr != nil {
					break
				}
				time.Sleep(time.Millisecond)
			}
			r.Frames(2)
			assertStartupPrivate(t, r, v)
			if _, ok := r.Find(ui.RoleLabel, "Could not read the account list. Try again."); !ok {
				t.Fatal("malformed roster did not remain on the recoverable error screen")
			}
			if got, err := os.ReadFile(path); err != nil || string(got) != string(body) {
				t.Fatal("startup or Retry changed the malformed roster")
			}
			r.Play(widgettest.Click(ui.RoleButton, "Retry"))
		}
		// Finish the last failed retry before replacing the file for recovery.
		if l, ok := v.loading.Wait(5 * time.Second); !ok || l.ReadErr == nil {
			t.Fatal("malformed roster became usable after retry")
		}
		if err := os.WriteFile(path, []byte(`{"next_id":8,"active":"7","order":["7"]}`), 0o600); err != nil {
			t.Fatal(err)
		}
		r.Frames(2)
		r.Play(widgettest.Click(ui.RoleButton, "Retry"))
		for end := time.Now().Add(5 * time.Second); v.loaded == nil && time.Now().Before(end); {
			r.Frames(1)
			time.Sleep(time.Millisecond)
		}
		return nil
	})
	if s != nil {
		t.Cleanup(s.CloseSettings)
	}
	if !shown || s == nil || len(s.Accounts) != 0 || s.Roster().Active != "7" {
		t.Fatal("malformed roster bypassed recovery or lost the repaired account list")
	}
}
