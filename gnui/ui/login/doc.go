// Package login is the login and pairing screen: the QR code to scan and the
// phone-code linking, shown while the account on screen is not linked (and
// while an account is being added).
//
// It ports zf:src/ui/login.rs (linking: the QR code, pairing with a phone
// number, the states and errors, and Add account's Cancel) with the two
// confirmations it opens, Start over and Remove this account, and the phone
// number's entry (upstream's Link with a phone number dialog), which are drawn
// here because the dialogs' overlay is not built over the linking screen.
//
// Entry points (the shell's slots, wired in cmd/zapfast/screens.go):
//
//	New() *Screen  // screen.Pane: Set.Login
package login
