// Package dialogs is every dialog of state.Dialog except the cropper's and the sticker maker's.
//
// It ports zf:src/ui/dialogs.rs and zf:src/ui/polls.rs (forward, delete with NSAlert, new chat, new contact, labels, polls, locations, interactive lists, invites, confirmations, About, Keyboard Shortcuts).
//
// Entry points (the shell's slots, wired in cmd/zapfast/screens.go):
//
//	New() *Screen  // screen.Overlay: Set.Dialogs
//
// This is the foundation's stub: a placeholder the shell can call. The
// package's owner replaces the body, keeping the entry points (see
// ui/CONTRACT.md).
package dialogs
