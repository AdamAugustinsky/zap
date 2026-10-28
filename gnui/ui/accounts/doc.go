// Package accounts is the account switcher, a popover at the rail's avatar
// while state.AccountMenu.
//
// It ports zf:src/ui/accounts.rs (the switcher under our avatar: the accounts
// with picture, name or number, unread chats, a check on the one on screen;
// Add account) and the account lifecycle of zf:src/app.rs (package acct, which
// the login screen shares: add, switch, cancel adding, remove, and the
// AccountRemoved event that drops an account).
//
// Entry points (the shell's slots, wired in cmd/zapfast/screens.go):
//
//	New() *Screen  // screen.Popover: Set.Accounts
//
// Adding an account needs the app to say how a core is opened: it sets
// state.State.OpenCore (cmd/zapfast does).
package accounts
