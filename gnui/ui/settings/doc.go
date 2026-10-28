// Package settings is the Settings window (state.WinSettings, ⌘,).
//
// It ports zf:src/ui/settings.rs and zf:src/ui/update.rs (the Settings window: every section but the wallpaper, app lock, updates against the local test feed).
//
// Entry points (the shell's slots, wired in cmd/zapfast/screens.go):
//
//	New() *Screen  // screen.WindowScreen: Set.Settings
//
// This is the foundation's stub: a placeholder the shell can call. The
// package's owner replaces the body, keeping the entry points (see
// ui/CONTRACT.md).
package settings
