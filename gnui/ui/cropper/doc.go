// Package cropper is the picture cropper, over the window while state.DialogCropper.
//
// It ports zf:src/ui/image_editor.rs (cropping a staged picture by edges, corners or whole; turning; Reset; Enter keeps, Escape leaves); the writing stays in the core's ApplyPictureEdit.
//
// Entry points (the shell's slots, wired in cmd/zapfast/screens.go):
//
//	New() *Screen  // screen.Overlay: Set.Cropper
//
// This is the foundation's stub: a placeholder the shell can call. The
// package's owner replaces the body, keeping the entry points (see
// ui/CONTRACT.md).
package cropper
