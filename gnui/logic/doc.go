// Package logic is upstream's reducers, free of any UI, each naming its
// source in zf:src/app.rs and carrying upstream's tests in Go:
//
//   - read state while the window has the focus, and the presence each
//     core reports (read.go);
//   - voice messages playing on into the next unheard one (voice.go);
//   - forwarding batches: recipients, the core's limit, the confirmation
//     (forward.go);
//   - selection mode: clicks, shift-clicks, sweeps, pruning (selection.go);
//   - deleting for us (pending until the core confirms) and for everyone
//     (shown at once); upstream has no undo (deletes.go);
//   - the notification policy: whether an incoming message notifies, and
//     with what title, body, picture and sound; package sys posts
//     State.Notices and State.NoticeClears (notify.go).
//
// Its state lives in package state (State.Forwarding, State.Voice,
// State.Panes' selection and deletes, State.Notices): screens read it and
// call this package's functions. New() is the slot's reducer
// (screen.Reducer: Set.Logic), which listens for the events and runs once
// per frame.
package logic
