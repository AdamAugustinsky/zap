// Package stickermaker is the sticker maker, over the window while state.DialogStickerMaker.
//
// It ports zf:src/ui/dialogs.rs:1110 (the sticker maker: choose a picture, drag the square and Size, transparency, emojis, Send or Add to favorites); encoding stays in the core's MakeSticker.
//
// Entry points (the shell's slots, wired in cmd/zapfast/screens.go):
//
//	New() *Screen  // screen.Overlay: Set.StickerMaker
//
// This is the foundation's stub: a placeholder the shell can call. The
// package's owner replaces the body, keeping the entry points (see
// ui/CONTRACT.md).
package stickermaker
