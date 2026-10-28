// Package picker is the picker above the composer (state.Panes.Picker) and the reaction picker.
//
// It ports zf:src/ui/picker.rs: an emoji tab (the table is package zapfast/emoji, shared with the composer),
// GIF search through the core, and stickers (packs, favourites, recents, add).
//
// Entry point (the shell's slot, wired in cmd/zapfast/screens.go):
//
//	New() *Screen  // screen.Overlay: Set.Picker
//
// It talks to the other screens through the shared state: it opens above
// Panes.PickerAnchor (the composer's emoji button), opens as the reaction
// picker while Panes.ReactTarget is open, sends a reaction with Account.React,
// hands an emoji to the composer with State.InsertEmoji, opens links through
// State.Shell and the sticker maker with state.DialogStickerMaker. Its
// pictures come from the window's image cache (Kit.Images).
package picker
