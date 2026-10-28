package state

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"gonativeui/app/prefs"
)

func TestTransientSettingsFailurePreservesFileAndRetry(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "settings.json")
	body := []byte(`{"app_lock_hash":"saved-verifier","interface_language":"de"}`)
	if err := os.WriteFile(path, body, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(path, 0); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.Chmod(path, 0o600) })
	if _, err := os.ReadFile(path); !errors.Is(err, os.ErrPermission) {
		t.Skip("this host does not enforce fixture file permissions")
	}
	p := Paths{Config: dir, Data: dir}
	ld := LoadSettings(p)
	for attempt := range 2 { // retry and a new process's load remain transient
		l, ok := ld.Wait(5 * time.Second)
		if !ok || !errors.Is(l.ReadErr, os.ErrPermission) || errors.Is(l.ReadErr, prefs.ErrCorrupt) {
			t.Fatalf("attempt %d: transient error was not fatal: %+v", attempt, l)
		}
		if len(l.Settings.AppLockHash) != 0 || l.roster != nil || len(l.accounts) != 0 {
			t.Fatal("a failed global read continued loading accounts")
		}
		if _, err := os.Stat(path + ".corrupt"); !errors.Is(err, os.ErrNotExist) {
			t.Fatal("transient failure was renamed as corruption")
		}
		ld.Retry()
	}
	restarted := LoadSettings(p)
	if l, ok := restarted.Wait(5 * time.Second); !ok || l.ReadErr == nil {
		t.Fatal("a fresh load bypassed the failed settings")
	}
	// Complete the pending retry before repairing access, then retry the same loader.
	if l, ok := ld.Wait(5 * time.Second); !ok || l.ReadErr == nil {
		t.Fatal("retry accepted unreadable settings")
	}
	if err := os.Chmod(path, 0o600); err != nil {
		t.Fatal(err)
	}
	if got, err := os.ReadFile(path); err != nil || string(got) != string(body) {
		t.Fatal("transient failure changed the original file")
	}
	ld.Retry()
	l, ok := ld.Wait(5 * time.Second)
	if !ok || l.ReadErr != nil || string(l.Settings.AppLockHash) != "saved-verifier" {
		t.Fatal("retry did not recover the verifier")
	}
	s := New()
	s.UseSettings(l)
	s.CloseSettings()
}

func TestMalformedStartupSettingsKeepUpstreamDefaultsAndBackup(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "settings.json")
	body := []byte(`{"app_lock_hash":`)
	if err := os.WriteFile(path, body, 0o600); err != nil {
		t.Fatal(err)
	}
	l := loadDir(t, dir)
	if l.ReadErr != nil || len(l.Errs) != 1 || !errors.Is(l.Errs[0], prefs.ErrCorrupt) {
		t.Fatalf("malformed settings changed upstream policy: %+v", l)
	}
	defaults := DefaultSettings()
	if hashOf(&l.Settings) != hashOf(&defaults) {
		t.Fatal("malformed settings did not use defaults")
	}
	if got, err := os.ReadFile(path + ".corrupt"); err != nil || string(got) != string(body) {
		t.Fatal("malformed settings were not preserved")
	}
}

func TestTransientAccountSettingsFailureBlocksStartupAndRetries(t *testing.T) {
	dir := t.TempDir()
	p := Paths{Config: dir, Data: dir}
	if err := os.WriteFile(p.roster(), []byte(`{"next_id":3,"active":"2","order":["1","2"]}`), 0o600); err != nil {
		t.Fatal(err)
	}
	path := p.account("2")
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	body := []byte(`{"send_read_receipts":false,"send_typing":false,"notifications":false,"auto_download":false,"save_contacts_to_phone":false,"limit_notifications":"both"}`)
	if err := os.WriteFile(path, body, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(path, 0); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.Chmod(path, 0o600) })
	if _, err := os.ReadFile(path); !errors.Is(err, os.ErrPermission) {
		t.Skip("this host does not enforce fixture file permissions")
	}
	ld := LoadSettings(p)
	for range 2 {
		l, ok := ld.Wait(5 * time.Second)
		if !ok || !errors.Is(l.ReadErr, os.ErrPermission) || errors.Is(l.ReadErr, prefs.ErrCorrupt) {
			t.Fatal("an unreadable account was replaced with usable default settings")
		}
		if _, err := os.Stat(path + ".corrupt"); !errors.Is(err, os.ErrNotExist) {
			t.Fatal("a transient account error was classified as corruption")
		}
		ld.Retry()
	}
	if l, ok := ld.Wait(5 * time.Second); !ok || l.ReadErr == nil {
		t.Fatal("retry accepted unreadable account settings")
	}
	if err := os.Chmod(path, 0o600); err != nil {
		t.Fatal(err)
	}
	if got, err := os.ReadFile(path); err != nil || string(got) != string(body) {
		t.Fatal("the failed read overwrote the account's privacy settings")
	}
	ld.Retry()
	l, ok := ld.Wait(5 * time.Second)
	if !ok || l.ReadErr != nil {
		t.Fatal("retry did not recover the readable account settings")
	}
	s := New()
	s.UseSettings(l)
	t.Cleanup(s.CloseSettings)
	a := s.AddAccount("2", nil, nil)
	if a.Settings.SendReadReceipts || a.Settings.SendTyping || a.Settings.Notifications || a.Settings.AutoDownload || a.Settings.SaveContactsToPhone || a.Settings.LimitNotifications != LimitBoth {
		t.Fatalf("retry did not apply the private choices: %+v", a.Settings)
	}
}

func TestMalformedAccountSettingsKeepUpstreamDefaultsAndBackup(t *testing.T) {
	dir := t.TempDir()
	p := Paths{Config: dir, Data: dir}
	path := p.account("1")
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	body := []byte(`{"send_read_receipts":`)
	if err := os.WriteFile(path, body, 0o600); err != nil {
		t.Fatal(err)
	}
	l := loadDir(t, dir)
	if l.ReadErr != nil {
		t.Fatal("malformed account settings changed upstream policy")
	}
	defaults := DefaultAccountSettings()
	if hashOf(&l.accounts["1"].st) != hashOf(&defaults) {
		t.Fatal("malformed account settings did not use defaults")
	}
	if got, err := os.ReadFile(path + ".corrupt"); err != nil || string(got) != string(body) {
		t.Fatal("malformed account settings were not preserved")
	}
}

func TestNewAccountReadFailureUsesPrivateOptionsWithoutAWriter(t *testing.T) {
	dir := t.TempDir()
	l := loadDir(t, dir)
	s := New()
	s.UseSettings(l)
	path := l.paths.account("2")
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
	if _, err := os.ReadFile(path); !errors.Is(err, os.ErrPermission) {
		t.Skip("this host does not enforce fixture file permissions")
	}
	a := s.AddAccount("2", nil, nil)
	if a.Settings.SendReadReceipts || a.Settings.SendTyping || a.Settings.Notifications || a.Settings.AutoDownload || a.Settings.SaveContactsToPhone || a.Settings.HistoryPrefetch || a.prefs != nil {
		t.Fatal("a newly added account used public defaults or attached a writer after a read failure")
	}
	s.CloseSettings()
	if err := os.Chmod(path, 0o600); err != nil {
		t.Fatal(err)
	}
	if got, err := os.ReadFile(path); err != nil || string(got) != string(body) {
		t.Fatal("closing the new account overwrote unreadable privacy settings")
	}
}

func TestTransientRosterFailureBlocksStartupAndPreservesFile(t *testing.T) {
	dir := t.TempDir()
	p := Paths{Config: dir, Data: dir}
	body := []byte(`{"next_id":8,"active":"7","order":["7"]}`)
	if err := os.WriteFile(p.roster(), body, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(p.roster(), 0); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.Chmod(p.roster(), 0o600) })
	if _, err := os.ReadFile(p.roster()); !errors.Is(err, os.ErrPermission) {
		t.Skip("this host does not enforce fixture file permissions")
	}
	ld := LoadSettings(p)
	for range 2 {
		l, ok := ld.Wait(5 * time.Second)
		if !ok || !errors.Is(l.ReadErr, os.ErrPermission) {
			t.Fatal("a transient roster failure selected usable default accounts")
		}
		if l.roster != nil || len(l.Roster.Order) != 0 || len(l.accounts) != 0 {
			t.Fatal("a failed roster retained a writer or loaded accounts")
		}
		if _, err := os.Stat(filepath.Join(dir, "accounts")); !errors.Is(err, os.ErrNotExist) {
			t.Fatal("a failed roster created default account settings")
		}
		if _, err := os.Stat(p.roster() + ".corrupt"); !errors.Is(err, os.ErrNotExist) {
			t.Fatal("a transient roster failure renamed the original file")
		}
		ld.Retry()
	}
	if l, ok := ld.Wait(5 * time.Second); !ok || l.ReadErr == nil {
		t.Fatal("retry accepted unreadable accounts.json")
	}
	if l, ok := LoadSettings(p).Wait(5 * time.Second); !ok || l.ReadErr == nil {
		t.Fatal("a fresh load accepted unreadable accounts.json")
	}
	if err := os.Chmod(p.roster(), 0o600); err != nil {
		t.Fatal(err)
	}
	if got, err := os.ReadFile(p.roster()); err != nil || string(got) != string(body) {
		t.Fatal("failed reads or retries changed the original roster")
	}
	ld.Retry()
	l, ok := ld.Wait(5 * time.Second)
	if !ok || l.ReadErr != nil || l.Roster.Active != "7" || len(l.Roster.Order) != 1 || l.Roster.Order[0] != "7" {
		t.Fatal("retry failed to restore the real roster")
	}
	s := New()
	s.UseSettings(l)
	s.AddAccount("7", nil, nil)
	s.CloseSettings()
	if got, err := os.ReadFile(p.roster()); err != nil || string(got) != string(body) {
		t.Fatal("recovery or shutdown overwrote the unchanged real roster")
	}
}

func TestMalformedRosterStaysBlockedUntilRepaired(t *testing.T) {
	for name, body := range map[string]string{
		"syntax":             `{"next_id":8,"active":"7","order":[`,
		"type":               `{"next_id":"invalid","active":"7","order":["7"]}`,
		"null":               `null`,
		"empty object":       `{}`,
		"top-level array":    `[]`,
		"top-level string":   `"roster"`,
		"missing next_id":    `{"active":"7","order":["7"]}`,
		"missing active":     `{"next_id":8,"order":["7"]}`,
		"missing order":      `{"next_id":8,"active":"7"}`,
		"null next_id":       `{"next_id":null,"active":"7","order":["7"]}`,
		"null active":        `{"next_id":8,"active":null,"order":["7"]}`,
		"null order":         `{"next_id":8,"active":"7","order":null}`,
		"null account id":    `{"next_id":8,"active":"7","order":[null]}`,
		"numeric account id": `{"next_id":8,"active":"7","order":[7]}`,
		"numeric active":     `{"next_id":8,"active":7,"order":["7"]}`,
		"object order":       `{"next_id":8,"active":"7","order":{}}`,
		"negative next_id":   `{"next_id":-1,"active":"7","order":["7"]}`,
		"fractional next_id": `{"next_id":1.5,"active":"7","order":["7"]}`,
		"overflow next_id":   `{"next_id":4294967296,"active":"7","order":["7"]}`,
		"wrong field case":   `{"NEXT_ID":8,"active":"7","order":["7"]}`,
		"duplicate next_id":  `{"next_id":8,"next_id":9,"active":"7","order":["7"]}`,
		"duplicate active":   `{"next_id":8,"active":"7","active":"8","order":["7"]}`,
		"duplicate order":    `{"next_id":8,"active":"7","order":["7"],"order":[]}`,
	} {
		t.Run(name, func(t *testing.T) {
			dir := t.TempDir()
			p := Paths{Config: dir, Data: dir}
			if err := os.WriteFile(p.roster(), []byte(body), 0o600); err != nil {
				t.Fatal(err)
			}
			ld := LoadSettings(p)
			for attempt := range 3 {
				l, ok := ld.Wait(5 * time.Second)
				if !ok {
					t.Fatal("roster read did not complete")
				}
				if l.ReadErr == nil {
					s := New()
					s.UseSettings(l)
					s.CloseSettings()
				}
				if !errors.Is(l.ReadErr, ErrRosterRead) || !errors.Is(l.ReadErr, prefs.ErrCorrupt) {
					t.Fatalf("attempt %d accepted a malformed roster: ready=%v, error=%v", attempt, ok, l.ReadErr)
				}
				if len(l.Errs) != 1 || !errors.Is(l.ReadErr, l.Errs[0]) || errors.Is(l.ReadErr, prefs.ErrNotLoaded) {
					t.Fatal("closing the failed store lost the original parse error")
				}
				if l.roster != nil || len(l.Roster.Order) != 0 || len(l.accounts) != 0 {
					t.Fatal("malformed roster attached a writer or selected accounts")
				}
				if got, err := os.ReadFile(p.roster()); err != nil || string(got) != body {
					t.Fatal("loading or retrying changed the original malformed roster")
				}
				for _, path := range []string{p.roster() + ".corrupt", filepath.Join(dir, "accounts")} {
					if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
						t.Fatalf("failed startup created %s", filepath.Base(path))
					}
				}
				if attempt == 0 {
					ld.Retry()
				} else if attempt == 1 {
					ld = LoadSettings(p) // restart must not turn corruption into absence
				}
			}
			valid := []byte(`{"next_id":8,"active":"7","order":["7"]}`)
			if err := os.WriteFile(p.roster(), valid, 0o600); err != nil {
				t.Fatal(err)
			}
			ld.Retry()
			l, ok := ld.Wait(5 * time.Second)
			if !ok || l.ReadErr != nil || l.Roster.Active != "7" || len(l.Roster.Order) != 1 || l.Roster.Order[0] != "7" {
				t.Fatal("retry did not recover the repaired roster")
			}
			s := New()
			s.UseSettings(l)
			s.AddAccount("7", nil, nil)
			s.CloseSettings()
			if got, err := os.ReadFile(p.roster()); err != nil || string(got) != string(valid) {
				t.Fatal("recovery or shutdown rewrote the unchanged repaired roster")
			}
		})
	}
}

func TestMissingRosterStillStartsAndPersistsTheFirstAccount(t *testing.T) {
	dir := t.TempDir()
	l := loadDir(t, dir)
	if l.ReadErr != nil || l.roster == nil || l.Roster.Active != "1" || len(l.Roster.Order) != 1 || l.Roster.Order[0] != "1" {
		t.Fatal("a missing roster stopped legitimate first-run startup")
	}
	s := New()
	s.UseSettings(l)
	s.AddAccount("1", nil, nil)
	s.CloseSettings()
	if got := readJSON(t, l.paths.roster()); got["active"] != "1" || got["next_id"] != float64(2) {
		t.Fatalf("first-run roster was not saved: %v", got)
	}
}

func TestValidRosterShapesRetainUpstreamSanitization(t *testing.T) {
	for _, tc := range []struct {
		name, body, active string
		next               uint32
	}{
		{"ordinary", `{"next_id":8,"active":"7","order":["7"]}`, "7", 8},
		{"reordered and extended", `{"future":{"enabled":true},"order":["7"],"active":"7","next_id":8,"future":null}`, "7", 8},
		{"case variants are unknown", `{"next_id":8,"active":"7","order":["7"],"NEXT_ID":null,"ACTIVE":null,"ORDER":null}`, "7", 8},
		{"empty roster", `{"next_id":0,"active":"","order":[]}`, "1", 2},
		{"unsafe and duplicate IDs", `{"next_id":1,"active":"missing","order":["7","7","../escape","09",""]}`, "7", 8},
		{"maximum next_id", `{"next_id":4294967295,"active":"7","order":["7"]}`, "7", 4294967295},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			if err := os.WriteFile(filepath.Join(dir, "accounts.json"), []byte(tc.body), 0o600); err != nil {
				t.Fatal(err)
			}
			l, ok := LoadSettings(Paths{Config: dir, Data: dir}).Wait(5 * time.Second)
			if !ok {
				t.Fatal("roster read did not complete")
			}
			if l.ReadErr != nil {
				t.Fatal(l.ReadErr)
			}
			s := New()
			s.UseSettings(l)
			t.Cleanup(s.CloseSettings)
			if l.Roster.Active != tc.active || l.Roster.NextID != tc.next || len(l.Roster.Order) != 1 || l.Roster.Order[0] != tc.active {
				t.Fatalf("valid roster rejected or sanitized differently: %+v, %v", l.Roster, l.ReadErr)
			}
		})
	}
}
