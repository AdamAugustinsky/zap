// Package gocore is Zap's backend: a core.Conn that runs in the process,
// over an Engine (the Go demo, or whatsmeow). It took the place of
// ZapFast's Rust core (zfcore, removed in WP9), whose behaviour it keeps
// where the notes below say "the Rust core". The plan and the research behind it are in
// ../../docs/go-backend/.
//
// # The Conn
//
// [Open] opens one account's [Conn] over an [Engine], with a core.Config.
// The Conn speaks the ABI (gnui/abi) the Rust core spoke: the UI drives it
// exactly as it drove the Rust core, and gets the same errors (core.Error with the ZF_E_* codes, core.ErrClosed).
//
//   - Send never blocks on the engine. It encodes the command (a field over
//     its bound fails as it would with the Rust core), asks the engine's
//     Accept, checks the fields zfcore checks (an RGBA picture's size, a
//     voice note's length, finite coordinates), takes the blob the command
//     names, and queues a decoded copy for the engine goroutine.
//     PickAnswer goes to the Conn's own broker instead.
//   - Events wait in a queue of encoded records. The first one queued after
//     the UI drained to empty calls Config.Wake (in process, Wake is a
//     direct call from the goroutine that emitted, where the Rust core
//     needs a pipe and a goroutine in the poller). Drain decodes each into
//     one reused abi.Event for the Handler, within its time budget.
//   - Close stops the engine, answers every waiting Pick with no paths,
//     and waits up to its timeout (ETimeout past it).
//   - A panic in the engine, on any of its goroutines or in Accept,
//     becomes a Fatal event and poisons the Conn: Send then refuses with
//     ZF_E_POISONED, while Drain and Close still work. (The Rust core
//     poisons only for a panic inside a call; a core thread that panics
//     there just stops, so its commands go nowhere.)
//
// # The Engine
//
// An engine implements:
//
//	type Engine interface {
//		Name() string                  // "the <Name> core does not answer X"
//		Accept(cmd *abi.Command) error // on Send's goroutine: nil, ErrUnsupported or Refuse(msg)
//		Open(h *Host) error            // once, at Open: keep h; may emit and h.Go already
//		Start()                        // the startup permit, on the engine goroutine
//		Command(cmd *Command)          // each accepted command, in order, on the engine goroutine
//		Stop()                         // once, at Close, on the engine goroutine
//	}
//
// The Conn calls Start, Command and Stop on one goroutine of its own, one
// at a time and in Send's order, so an engine's state needs no lock for
// them. A Command owns its decoded copy of the command and the bytes of the
// blob it named (Command.Blob). Work that takes long goes to h.Go, whose
// goroutines get a context cancelled at Close, and are waited for.
//
// Accept answers before any blob is taken, so a refused command leaves the
// caller's blob in place. A [Table] (Answers(tags...)) is the usual Accept:
// every other command is refused with ZF_E_STATE, so a partial engine is
// safe. The demo-only commands (DemoIncoming, DemoTyping, DemoPanic)
// refused so say "that command needs the demo core", as zfcore's real core
// does.
//
// Engines emit through the [Host], from any goroutine:
//
//	h.Out().Emit(&ev)                 // one record; over a bound, EventDropped
//	r := h.Out().Record()             // one record that names event blobs
//	thumb := r.Blob(jpeg)             // shared by content, freed if dropped
//	r.Emit(&ev)
//	out.Chunks(h.Out(), rows, convert, record) // a chunk(N, B) list: one batch, last once
//	out.Clip(name, 1024)              // display text to its bound
//	paths := h.Pick(ctx, &req)        // a PickRequest, waiting for its PickAnswer
//
// Event blobs live in the Conn's blobs.Table (package gocore/blobs: the 64
// MiB budget, the 30 s grace, dedup by content); the UI's PutBlob, ReadBlob
// and FreeBlob use the same table and ids.
package gocore
