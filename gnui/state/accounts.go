package state

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
	"time"

	"gonativeui/app/secret"

	"zapfast/abi"
	"zapfast/i18n"
	"zapfast/model"
)

// Several linked numbers (zf:src/app.rs:1608-1805): each account has its
// own core and model, and its events update only it (Account.Active); the
// window shows one, State.Active.

// AddNewAccount opens a core for a new account and puts its login screen
// on (Settings → Add account; zf:src/app.rs:1698). It returns nil when the
// state cannot open cores (OpenCore unset).
func (s *State) AddNewAccount() *Account {
	if s.OpenCore == nil {
		return nil
	}
	before := ""
	if a := s.Acct(); a != nil {
		before = a.ID
	}
	id := s.NextAccountID()
	pending, conn := s.OpenCore(id)
	a := s.AddAccount(id, pending, conn)
	s.SwitchAccount(a.Index)
	s.beforeAdding = before
	s.AddingAccount = true
	s.Page = PageChats
	s.gen++
	return a
}

// CancelAddAccount goes back to the account shown before; the new account,
// never linked, is removed (zf:src/app.rs:8273).
func (s *State) CancelAddAccount() {
	if !s.AddingAccount {
		return
	}
	back := -1
	for i, a := range s.Accounts {
		if a.ID == s.beforeAdding || (back < 0 && i != s.Active) {
			back = i
		}
	}
	if back >= 0 {
		s.SwitchAccount(back)
	}
}

// RemoveAccount unlinks account i and, once its core says it is gone
// (AccountRemoved), drops it and deletes its folders. The last account is
// only unlinked: the window always shows one (zf:src/app.rs:1743).
func (s *State) RemoveAccount(i int) {
	if i < 0 || i >= len(s.Accounts) {
		return
	}
	a := s.Accounts[i]
	if len(s.Accounts) <= 1 {
		a.Send(a.Cmd(abi.CommandTagUnlink))
		return
	}
	a.Send(a.Cmd(abi.CommandTagRemoveAccount))
}

// finishRemoved drops an account whose core is gone (zf:src/app.rs:1754).
func (s *State) finishRemoved(a *Account) {
	if len(s.Accounts) <= 1 {
		// Another removal got there first: keep this one, linkable.
		a.Removed = false
		a.Send(a.Cmd(abi.CommandTagUnlink))
		return
	}
	i := a.Index
	a.Model.PinConversations(0, 0)
	active := i == s.Active
	s.Accounts = append(s.Accounts[:i], s.Accounts[i+1:]...)
	for j, b := range s.Accounts {
		b.Index = j
	}
	switch {
	case i < s.Active:
		s.Active--
	case s.Active >= len(s.Accounts):
		s.Active = len(s.Accounts) - 1
	}
	if active {
		s.AddingAccount = false
		s.Page = PageChats
		s.clearAccountUI()
	}
	s.forgetAccount(a)
	s.accountGone(a.Serial)
	conn, dirs, keys := a.Conn, s.accountDirs(a.ID), s.ArchiveKeys
	a.Conn = nil
	// The core stops and its folders go off the main thread.
	go func() {
		if conn != nil {
			if err := conn.Close(5 * time.Second); err != nil {
				slog.Warn("zapfast: closing a removed account's core", "account", a.ID, "err", err)
			}
		}
		// The key outlives the archive only by mistake: forget it once the
		// archive is gone, never before (zf:src/app.rs:1771).
		var key string
		var keyErr error = os.ErrNotExist
		if len(dirs) > 0 {
			key, keyErr = ArchiveKeyIdentity(dirs[0])
		}
		for _, d := range dirs {
			if err := os.RemoveAll(d); err != nil {
				slog.Warn("zapfast: deleting a removed account's files", "dir", d, "err", err)
				msg := err.Error()
				s.removeFailed.CompareAndSwap(nil, &msg)
			}
		}
		if keyErr == nil && keys != nil {
			if _, err := os.Lstat(filepath.Join(dirs[0], "archive.db")); errors.Is(err, fs.ErrNotExist) {
				if err := keys.Delete(key); err != nil && !errors.Is(err, secret.ErrNotFound) {
					slog.Warn("zapfast: could not delete a removed account's archive key", "err", err)
				}
			}
		}
		s.Wake()
	}()
	s.gen++
}

// KeyStore deletes items of the OS keyring the cores keep their archive keys
// in (secret.Store, opened on core.KeyringService). Delete may block and
// returns secret.ErrNotFound for an item already gone.
type KeyStore interface {
	Delete(account string) error
}

// ArchiveKeyIdentity is the keyring account of the archive key of the
// account whose state folder is dir, as the core names it
// (zf:src/archive/encryption.rs identity_for): "archive-" and the SHA-256,
// in lower-case hex, of the folder's canonical path. The folder must exist.
func ArchiveKeyIdentity(dir string) (string, error) {
	p, err := filepath.EvalSymlinks(dir)
	if err == nil {
		p, err = filepath.Abs(p)
	}
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256([]byte(p))
	return "archive-" + hex.EncodeToString(sum[:]), nil
}

// accountDirs are the folders an account's core keeps (its archive, its
// media cache), as the core lays them out under the app's roots; none
// without settings paths (the demo, tests).
func (s *State) accountDirs(id string) []string {
	if s.persist == nil || !SafeAccountID(id) {
		return nil
	}
	p := s.persist.l.paths
	dirs := []string{filepath.Join(p.Data, "accounts", id)}
	if p.Cache != "" {
		dirs = append(dirs, filepath.Join(p.Cache, "accounts", id))
	}
	return dirs
}

// clearAccountUI drops the window's state that pointed into the account
// that left the screen (zf:src/app.rs:1643).
func (s *State) clearAccountUI() {
	s.Panes = Panes{ListHidden: s.Panes.ListHidden}
	s.Dialog = Dialog{}
	s.AccountMenu = false
	s.Visible = s.Visible[:0]
	s.history.clear()
	if s.Win.Want[WinViewer] || s.Win.Open[WinViewer] {
		s.Win.Want[WinViewer] = false
		s.Win.ViewerChat = 0
		s.Win.ViewerMsg.Reset()
		s.Win.ViewerPlay, s.Win.ViewerAt = false, 0
	}
}

// drainRemoved finishes the accounts whose cores said they are gone, after
// a drain (an account is never dropped in the middle of its events).
func (s *State) drainRemoved() {
	for i := len(s.Accounts) - 1; i >= 0; i-- {
		if a := s.Accounts[i]; a.Removed {
			s.finishRemoved(a)
		}
	}
	if why := s.removeFailed.Swap(nil); why != nil {
		s.Toast([]byte(i18n.T("Could not delete the removed account's files")+": "+*why), true)
	}
}

// Name is how the switcher names the account: its profile name, else its
// number, else "Account" and its folder's number (zf:src/account.rs:284).
func (a *Account) Name() string {
	if n := a.Model.Me.Name(); len(n) > 0 {
		return string(n)
	}
	if p := model.Phone(a.Model.Me.ID()); len(p) > 0 {
		return "+" + string(p)
	}
	return i18n.Format(i18n.T("Account {id}"), "{id}", a.ID)
}

// UnreadChats is the badge's count for the account: unarchived, unmuted,
// unlocked chats that look unread (zf:src/account.rs:270).
func (a *Account) UnreadChats(now int64) int {
	n := 0
	ch := &a.Model.Chats
	for _, ref := range ch.Order() {
		c := ch.At(ref)
		if !c.Archived() && !c.Locked() && !c.Muted(now) && (c.Unread() > 0 || c.MarkedUnread()) {
			n++
		}
	}
	return n
}
