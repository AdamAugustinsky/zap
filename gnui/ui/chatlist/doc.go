// Package chatlist is the chat list column: its rows, search, chips and folders, and its toolbar items.
//
// It ports zf:src/ui/chats.rs and zf:src/ui/labels.rs (the chat list: fixed rows with avatars, badges, typing, muted, the @ marker, reordering; search; the label chips and the label manager's list side; Archived, Locked, Favorites, Starred; the compact list in the rail).
//
// Entry points (the shell's slots, wired in cmd/zapfast/screens.go):
//
//	New() *Screen  // screen.ListPane: Set.ChatList; also Compact(e, r) for the rail while the list is hidden
//
// This is the foundation's stub: a placeholder the shell can call. The
// package's owner replaces the body, keeping the entry points (see
// ui/CONTRACT.md).
package chatlist
