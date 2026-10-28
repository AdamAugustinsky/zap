package state

import (
	"os"
	"path/filepath"
	"testing"

	"gonativeui/app/prefs"
)

func TestSettingsArrivingAtTheReadKeepTheirLockVerifier(t *testing.T) {
	dir := t.TempDir()
	open := openSettingsFile
	t.Cleanup(func() { openSettingsFile = open })
	var writeErr error
	called := false
	openSettingsFile = func(path string, defaults settingsFile, opt prefs.Options) (*prefs.Store[settingsFile], error) {
		called = true
		// The old Stat saw ENOENT; the actual read now succeeds.
		writeErr = os.WriteFile(path, []byte(`{"version":1,"app_lock_hash":"arriving-verifier","notification_content":false}`), 0o600)
		return open(path, defaults, opt)
	}
	l := loadDir(t, dir)
	if !called || writeErr != nil {
		t.Fatalf("could not inject the arriving file: %v", writeErr)
	}
	if l.ReadErr != nil || string(l.Settings.AppLockHash) != "arriving-verifier" || l.Settings.NotificationText {
		t.Fatal("the earlier absence discarded a successfully read verifier/privacy choice")
	}
}

func TestAccountSettingsArrivingAtTheReadAreNotMigratedOver(t *testing.T) {
	dir := t.TempDir()
	open := openAccountFile
	t.Cleanup(func() { openAccountFile = open })
	body := []byte(`{"send_read_receipts":false,"send_typing":false,"notifications":false,"auto_download":false,"save_contacts_to_phone":false}`)
	var writeErr error
	called := false
	openAccountFile = func(path string, defaults accountFile, opt prefs.Options) (*prefs.Store[accountFile], error) {
		called = true
		if writeErr = os.MkdirAll(filepath.Dir(path), 0o700); writeErr == nil {
			writeErr = os.WriteFile(path, body, 0o600)
		}
		return open(path, defaults, opt)
	}
	l := loadDir(t, dir)
	if !called || writeErr != nil {
		t.Fatalf("could not inject the arriving account file: %v", writeErr)
	}
	as := l.accounts["1"]
	if as.st.SendReadReceipts || as.st.SendTyping || as.st.Notifications || as.st.AutoDownload || as.st.SaveContactsToPhone {
		t.Fatal("the earlier absence replaced existing account privacy choices with legacy defaults")
	}
	if err := as.store.Close(); err != nil {
		t.Fatal(err)
	}
	if got, err := os.ReadFile(l.paths.account("1")); err != nil || string(got) != string(body) {
		t.Fatal("the arriving account file was overwritten by migration")
	}
}

func TestExistingEmptyAccountSettingsDoNotInheritLegacyChoices(t *testing.T) {
	dir := t.TempDir()
	p := Paths{Config: dir, Data: dir}
	if err := os.WriteFile(p.settings(), []byte(`{"version":1,"send_read_receipts":false,"send_typing":false,"notifications":false}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(p.account("1")), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p.account("1"), []byte(`{}`), 0o600); err != nil {
		t.Fatal(err)
	}
	l := loadDir(t, dir)
	if !l.accounts["1"].st.SendReadReceipts || !l.accounts["1"].st.SendTyping || !l.accounts["1"].st.Notifications {
		t.Fatal("a successfully read empty object was mistaken for a missing file")
	}
}
