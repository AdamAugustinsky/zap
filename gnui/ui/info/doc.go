// Package info is the info panel beside the conversation (state.PaneInfo).
//
// It ports zf:src/ui/info.rs (the info panel: a contact's or group's details, members, media, docs and links, the chat's actions).
//
// Entry points (the shell's slots, wired in cmd/zapfast/screens.go):
//
//	New() *Screen  // screen.SidePane: Set.Info
//
// This is the foundation's stub: a placeholder the shell can call. The
// package's owner replaces the body, keeping the entry points (see
// ui/CONTRACT.md).
package info
