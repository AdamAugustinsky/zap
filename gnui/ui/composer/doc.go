// Package composer is the composer band at the foot of the conversation.
//
// It ports the composer part of zf:src/ui/conversation.rs (the hosted NSTextView composer on N4's contract: Enter policy, drafts, edits, replies, @ and :emoji: completion, marked text, the attachment row, pasting and dropping, voice recording).
//
// Entry points (the shell's slots, wired in cmd/zapfast/screens.go):
//
//	New() *Screen  // screen.ComposerPane: Set.Composer
//
// The open chat's text is the model's draft (Store.Draft), its staged
// attachments are state.Staged; another chat's mentions, attachments and
// reply wait in its aside. It sets Panes.PickerAnchor, takes the picker's
// emoji (State.TakeInsert) and a link's text (Requests.Prefill), opens the
// cropper on a staged picture, and stores the draft at the app lock
// (State.OnLock).
package composer
