// Package msginfo is the message info pane beside the conversation (state.PaneMessageInfo).
//
// It ports zf:src/ui/message_info.rs (a message's receipts: delivered, read and played, per recipient).
//
// Entry points (the shell's slots, wired in cmd/zapfast/screens.go):
//
//	New() *Screen  // screen.SidePane: Set.MessageInfo
//
// This is the foundation's stub: a placeholder the shell can call. The
// package's owner replaces the body, keeping the entry points (see
// ui/CONTRACT.md).
package msginfo
