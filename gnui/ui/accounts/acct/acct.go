// Package acct is the account switcher's logic without an interface: adding,
// switching, cancelling the adding of, and removing accounts, and the names
// the switcher shows. The login screen and the switcher share it.
//
// The account lifecycle is the state's (State.AddNewAccount, SwitchAccount,
// CancelAddAccount, RemoveAccount; the app sets State.OpenCore, and the state
// drops an account when its core says AccountRemoved); this package names
// those actions for the screens.
//
// It ports zf:src/account.rs display_label, phone and unread_chat_count.
package acct

import (
	"strconv"

	"zapfast/format"
	"zapfast/i18n"
	"zapfast/model"
	"zapfast/state"
)

// ErrCannotAdd is why Add failed when the app has not said how to open a core
// (in English; Add toasts it translated).
const ErrCannotAdd = "Could not add an account"

// Several reports whether more than one account is here.
func Several(s *state.State) bool { return len(s.Accounts) >= 2 }

// NextID is the folder name of the next account: the smallest number nobody
// has (zf:src/roster.rs allocate).
func NextID(s *state.State) string {
	for n := 1; ; n++ {
		id := strconv.Itoa(n)
		taken := false
		for _, a := range s.Accounts {
			if a.ID == id {
				taken = true
				break
			}
		}
		if !taken {
			return id
		}
	}
}

// Add starts linking another number (Action::AddAccount): the state opens a
// new account (State.OpenCore), puts it on screen, and the login screen shows
// over the chats until it is linked or cancelled.
func Add(s *state.State) error {
	if s.AddNewAccount() == nil {
		s.Toast([]byte(i18n.T("Could not add an account")), true)
	}
	return nil
}

// Switch puts account i on screen (Action::SwitchAccount). An account left
// before it was linked was never really added: the state removes it.
func Switch(s *state.State, i int) { s.SwitchAccount(i) }

// CancelAdd goes back from the linking screen to the account the window
// showed before (Action::CancelAddAccount); the unlinked new account goes.
func CancelAdd(s *state.State) { s.CancelAddAccount() }

// Remove unlinks the number of account a on this computer and deletes its
// local chats (Action::RemoveAccount). The last account is only unlinked: the
// window always shows one, even if it is waiting to be linked. The account
// leaves the list when its core says it has gone (the state does that).
func Remove(s *state.State, a *state.Account) { s.RemoveAccount(a.Index) }

// Unread is the number of chats of account a that look unread and make
// noise: unarchived, unmuted, unlocked (zf:src/account.rs unread_chat_count;
// WhatsApp counts chats here, not the messages inside them).
func Unread(a *state.Account, now int64) int {
	n := 0
	chats := &a.Model.Chats
	for _, ref := range chats.Order() {
		c := chats.At(ref)
		if c.Archived() || c.Locked() || c.Muted(now) {
			continue
		}
		if c.Unread() > 0 || c.MarkedUnread() {
			n++
		}
	}
	return n
}

// UnreadElsewhere is the unread chats of the accounts that are not on screen,
// for the mark on the account switcher.
func UnreadElsewhere(s *state.State, now int64) int {
	n := 0
	for i, a := range s.Accounts {
		if i != s.Active {
			n += Unread(a, now)
		}
	}
	return n
}

// Phone is the account's own number as the switcher shows it ("+1 555 000
// 0000"), empty when its JID is not known or not a phone's.
func Phone(a *state.Account) string {
	if !a.Model.Me.Known() {
		return ""
	}
	d := model.Phone(a.Model.Me.ID())
	if d == nil {
		return ""
	}
	return format.Phone(model.S(d))
}

// Label is the name the switcher shows for a: the profile name, else the
// number, else "Account" and its number here (zf:src/account.rs
// display_label). accountFmt is the translated "Account {id}".
func Label(a *state.Account, accountFmt string) string {
	if a.Model.Me.Known() {
		if n := trim(string(a.Model.Me.Name())); n != "" {
			return n
		}
	}
	if p := Phone(a); p != "" {
		return p
	}
	return replace(accountFmt, "{id}", a.ID)
}

func trim(s string) string {
	b, e := 0, len(s)
	for b < e && isSpace(s[b]) {
		b++
	}
	for e > b && isSpace(s[e-1]) {
		e--
	}
	return s[b:e]
}

func isSpace(c byte) bool { return c == ' ' || c == '\t' || c == '\n' || c == '\r' }

func replace(s, old, new string) string {
	for i := 0; i+len(old) <= len(s); i++ {
		if s[i:i+len(old)] == old {
			return s[:i] + new + s[i+len(old):]
		}
	}
	return s
}
