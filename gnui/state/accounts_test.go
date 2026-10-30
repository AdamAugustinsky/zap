package state_test

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"zapfast/abi"
	"zapfast/core"
	"zapfast/core/fake"
	"zapfast/state"
)

// Each account has its own core and model: an account off screen updates
// only its own model, and comes on screen with its own open chat. Its errors
// still show, with its account label in speech (src/app.rs:11317).
func TestAccountsKeepTheirOwnState(t *testing.T) {
	r := newRig(t, 2)
	a, b := r.s.Accounts[0], r.s.Accounts[1]
	a.OpenChat(a.Model.Chats.Find([]byte(ada)))
	r.cores[1].Emit(ev(abi.EventTagChatUpdated, func(e *abi.Event) {
		e.ChatUpdated.Chat = abi.Chat{ID: []byte("9@s.whatsapp.net"), Name: []byte("Only B"), LastActivity: 9}
	}), ev(abi.EventTagError, func(e *abi.Event) { e.Error.Text = []byte("B failed") }))
	r.pump()
	if a.Model.Chats.Find([]byte("9@s.whatsapp.net")) != 0 || b.Model.Chats.Find([]byte("9@s.whatsapp.net")) == 0 {
		t.Error("an event reached the wrong account")
	}
	if len(r.s.Toasts) != 1 || string(r.s.Toasts[0].Text) != "B failed" {
		t.Errorf("the hidden account's error did not show: %+v", r.s.Toasts)
	}
	if got, urgent := r.s.TakeAnnouncement(nil, true); string(got) != "Account 2: B failed" || !urgent {
		t.Errorf("the hidden account's error did not name it: %q, urgent=%v", got, urgent)
	}
	b.Open = b.Model.Chats.Find([]byte(ada)) // remembered from its last run
	r.s.Page = state.PageCalls
	r.s.SwitchAccount(1)
	if r.s.Acct() != b || r.sent(1, abi.CommandTagLoadChat) != 1 || r.sent(1, abi.CommandTagLoadCallLog) != 1 {
		t.Errorf("switching: load %d, call log %d", r.sent(1, abi.CommandTagLoadChat), r.sent(1, abi.CommandTagLoadCallLog))
	}
	if a.Open == 0 {
		t.Error("the account that left the screen lost its open chat")
	}
}

// Adding an account opens a core and shows its login; leaving it before it
// links removes it, and the core's word drops it.
func TestAddingAndRemovingAccounts(t *testing.T) {
	r := newRig(t, 1)
	var added *fake.Core
	r.s.OpenCore = func(id string) (*core.Pending, core.Conn) {
		added = fake.New(nil)
		return nil, added
	}
	b := r.s.AddNewAccount()
	if b == nil || b.ID != "2" || r.s.Acct() != b || !r.s.AddingAccount || len(r.s.Accounts) != 2 {
		t.Fatalf("added %+v, adding %v", b, r.s.AddingAccount)
	}
	if n := countTag(added, abi.CommandTagSetDownloadFolder); n != 1 {
		t.Error("the new core did not get the download folder")
	}
	r.s.CancelAddAccount()
	if r.s.Acct() != r.s.Accounts[0] || r.s.AddingAccount || countTag(added, abi.CommandTagRemoveAccount) != 1 {
		t.Fatalf("cancelling: active %d, remove sent %d", r.s.Active, countTag(added, abi.CommandTagRemoveAccount))
	}
	added.Emit(ev(abi.EventTagAccountRemoved, func(*abi.Event) {}))
	r.pump()
	if len(r.s.Accounts) != 1 || r.s.Accounts[0].Index != 0 || r.s.Active != 0 {
		t.Errorf("after removal: %d accounts, active %d", len(r.s.Accounts), r.s.Active)
	}
	// The last account is only unlinked.
	r.s.RemoveAccount(0)
	if r.sent(0, abi.CommandTagUnlink) != 1 || r.sent(0, abi.CommandTagRemoveAccount) != 0 {
		t.Error("the last account was removed")
	}
}

// A removed account's folders go, and the roster forgets it.
func TestARemovedAccountsFilesGo(t *testing.T) {
	dir := t.TempDir()
	l, ok := state.LoadSettings(state.Paths{Config: dir, Data: dir, Cache: filepath.Join(dir, "cache")}).Wait(5 * time.Second)
	if !ok {
		t.Fatal("not loaded")
	}
	s := state.New()
	woke := make(chan struct{}, 4)
	s.Wake = func() { woke <- struct{}{} }
	s.UseSettings(l)
	one, two := fake.New(nil), fake.New(nil)
	s.AddAccount("1", nil, one)
	s.AddAccount(s.NextAccountID(), nil, two)
	s.Pump(0)
	folder := filepath.Join(dir, "accounts", "2")
	if _, err := os.Stat(filepath.Join(folder, "settings.json")); err != nil {
		os.MkdirAll(folder, 0o700)
		os.WriteFile(filepath.Join(folder, "archive.db"), []byte("x"), 0o600)
	}
	s.RemoveAccount(1)
	two.Emit(ev(abi.EventTagAccountRemoved, func(*abi.Event) {}))
	s.Pump(0)
	select {
	case <-woke:
	case <-time.After(5 * time.Second):
		t.Fatal("the removal never finished")
	}
	if _, err := os.Stat(folder); !os.IsNotExist(err) {
		t.Errorf("the account's folder is still there: %v", err)
	}
	s.CloseSettings()
	b, _ := os.ReadFile(filepath.Join(dir, "accounts.json"))
	if string(b) == "" || contains(string(b), `"2"`) {
		t.Errorf("roster %s", b)
	}
}

func countTag(f *fake.Core, tag abi.CommandTag) int {
	n := 0
	for _, c := range f.Sent() {
		if c.Tag == tag {
			n++
		}
	}
	return n
}

func contains(s, sub string) bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}

// Switching accounts discards an open forward selection and the message
// selection behind it; they name the other account's messages (AC-007).
func TestSwitchingDiscardsTheForwardSelection(t *testing.T) {
	r := newRig(t, 2)
	a := r.s.Accounts[0]
	a.OpenChat(a.Model.Chats.Find([]byte(ada)))
	r.s.Panes.Selecting = true
	r.s.Panes.Selection.Add([]byte("m1"))
	r.s.Panes.Reply.Add([]byte("m1"))
	d := state.Dialog{Kind: state.DialogForward, Chat: a.Open}
	d.IDs.Add([]byte("m1"))
	r.s.OpenDialog(d)
	r.s.Forwarding.Recipients.Add([]byte(ada))
	r.s.SwitchAccount(1)
	if r.s.Dialog.Kind != state.DialogNone || r.s.Panes.Selecting || r.s.Panes.Selection.Len() != 0 || r.s.Panes.Reply.Len() != 0 {
		t.Errorf("after switching: dialog %v, selecting %v, %d selected, %d replying", r.s.Dialog.Kind, r.s.Panes.Selecting, r.s.Panes.Selection.Len(), r.s.Panes.Reply.Len())
	}
}

// A removed account's files that cannot be deleted are reported with an
// error toast (AC-009, zf:src/app.rs:1798).
func TestAFailedFileCleanupSaysSo(t *testing.T) {
	dir := t.TempDir()
	l, ok := state.LoadSettings(state.Paths{Config: dir, Data: dir, Cache: filepath.Join(dir, "cache")}).Wait(5 * time.Second)
	if !ok {
		t.Fatal("not loaded")
	}
	s := state.New()
	woke := make(chan struct{}, 4)
	s.Wake = func() { woke <- struct{}{} }
	s.UseSettings(l)
	one, two := fake.New(nil), fake.New(nil)
	s.AddAccount("1", nil, one)
	s.AddAccount(s.NextAccountID(), nil, two)
	s.Pump(0)
	folder := filepath.Join(dir, "accounts", "2")
	os.MkdirAll(folder, 0o700)
	os.WriteFile(filepath.Join(folder, "archive.db"), []byte("x"), 0o600)
	os.Chmod(folder, 0o500) // its files cannot be unlinked
	t.Cleanup(func() { os.Chmod(folder, 0o700) })
	s.RemoveAccount(1)
	two.Emit(ev(abi.EventTagAccountRemoved, func(*abi.Event) {}))
	s.Pump(0)
	select {
	case <-woke:
	case <-time.After(5 * time.Second):
		t.Fatal("the removal never finished")
	}
	s.Pump(0)
	for _, tt := range s.Toasts {
		if tt.Error && contains(string(tt.Text), "Could not delete the removed account's files: ") && contains(string(tt.Text), "permission denied") {
			return
		}
	}
	t.Errorf("no error toast; toasts %d", len(s.Toasts))
}

// A core that failed to open shows why; Try again (Reconnect) opens it
// again, and it gets its startup permit once open, the cores having
// started.
func TestTryAgainReopensACoreThatFailedToOpen(t *testing.T) {
	s := state.New()
	opens := 0
	var c *fake.Core
	s.OpenCore = func(id string) (*core.Pending, core.Conn) {
		opens++
		if opens == 1 {
			return core.OpenAsync(nil, func(*core.Timing) (core.Conn, error) {
				return nil, errors.New("the account could not move")
			}), nil
		}
		return core.OpenAsync(nil, func(*core.Timing) (core.Conn, error) {
			c = fake.New(nil)
			return c, nil
		}), nil
	}
	p, _ := s.OpenCore("1")
	a := s.AddAccount("1", p, nil)
	p.Wait(5 * time.Second)
	s.Pump(0)
	s.StartCores()
	if a.Conn != nil || a.Err != "the account could not move" {
		t.Fatalf("conn %v, err %q", a.Conn, a.Err)
	}
	if err := a.Send(a.Cmd(abi.CommandTagReconnect)); err != nil || opens != 2 || a.Err != "" || a.Pending == nil {
		t.Fatalf("Try again: %v, %d opens, err %q", err, opens, a.Err)
	}
	a.Pending.Wait(5 * time.Second)
	s.Pump(0)
	if a.Conn != c || c == nil || !c.Started() || !a.Started {
		t.Fatalf("reopened: conn %v, started %v", a.Conn, a.Started)
	}
	// An open core's Reconnect goes to it.
	if a.Send(a.Cmd(abi.CommandTagReconnect)); opens != 2 || countTag(c, abi.CommandTagReconnect) != 1 {
		t.Errorf("%d opens, %d reconnects", opens, countTag(c, abi.CommandTagReconnect))
	}
}

// A core still opening when the cores start (a slow open: an account taken
// over from the Rust core, a Keychain prompt) gets its startup permit once
// it is open. Without it the core never connects: an Unlink then finds it
// offline, and the new device it makes never gets a code to link with.
func TestASlowCoreStartsOnceOpen(t *testing.T) {
	s := state.New()
	release := make(chan struct{})
	var c *fake.Core
	p := core.OpenAsync(nil, func(*core.Timing) (core.Conn, error) {
		<-release
		c = fake.New(nil)
		return c, nil
	})
	a := s.AddAccount("1", p, nil)
	s.Pump(0)
	s.StartCores() // after the first frame, the core still opening
	if a.Started {
		t.Fatal("a core still opening was marked started")
	}
	close(release)
	p.Wait(5 * time.Second)
	s.Pump(0)
	if a.Conn != c || c == nil || !c.Started() || !a.Started {
		t.Fatalf("once open: conn %v, started %v", a.Conn, a.Started)
	}
}
