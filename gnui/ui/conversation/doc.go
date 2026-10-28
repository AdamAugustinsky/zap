// Package conversation is the open chat: its header (toolbar items) and its transcript, and the in-chat search pane beside it.
//
// It ports zf:src/ui/conversation.rs without the composer (the header and the transcript: bubbles, markup, mentions, media, reactions, replies, pins, selection mode, the unread divider, jumps), zf:src/transcript.rs (copying), and zf:src/ui/pane.rs (the in-chat search pane).
//
// Entry points (the shell's slots, wired in cmd/zapfast/screens.go):
//
//	New() *Screen        // screen.ContentPane: Set.Conversation
//	NewSearch() *Search  // screen.SidePane: Set.Search
//
// This is the foundation's stub: a placeholder the shell can call. The
// package's owner replaces the body, keeping the entry points (see
// ui/CONTRACT.md).
package conversation
