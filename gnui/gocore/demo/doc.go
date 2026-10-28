// Package demo is the Go backend's demo core: a port of zfcore's
// (zfcore/src/demo.rs) as a gocore.Engine. It has no account and no
// network: it plays upstream's demo samples and answers commands the way
// the Rust demo does, record for record.
//
//   - The data: seven chats (Dorothy Vaughan's locked), their contacts, and
//     the media zfcore files after the chat list: Ada's photos, clip,
//     documents and links; a sticker, a GIF, a sticker pack; Margaret's
//     poll, contact card, location, round video, edited and forwarded
//     lines and reactions; four synthesized voice notes; the sticker
//     shelves, five seeded calls, privacy and storage figures.
//   - The answers: the same per-command disposition as zfcore's
//     (Disposition: Answers, Accepts, or Refuses with ZF_E_STATE before any
//     blob is taken), and the same events for each, through the same
//     conversions (zfcore/src/dto.rs). A send goes pending, then sent,
//     delivered and read after 20, 60 and 150 ms.
//   - DemoIncoming and DemoTyping play after their delay; the texts
//     core.DemoCallText and core.DemoVideoCallText ring a call instead.
//     DemoPanic panics in Send (ZF_E_PANIC, the Conn poisoned) or on the
//     demo's goroutine (a Fatal event).
//   - Calls are simulated as zfcore simulates them, with I420 test-pattern
//     frames at about 15 a second (Conn.Frame), and filed when they end.
//
// Its files are written once into the system's temporary directory
// (zapfast-demo-media and zapfast-demo-avatars), the same folders and
// names as zfcore's, never the account's. The pictures, clips and stickers
// are embedded (assets/, from the repository's assets/demo and
// zfcore/assets/posters; see assets/SOURCES.md); the voice notes are
// encoded at startup with gocore/av.
//
// [Open] opens a demo Conn; [New] is the bare Engine for gocore.Open.
// [OpenChosen] opens the demo core $ZAPFAST_ENGINE picks ("go" for this
// one; zfcore's by default, for now), which is how the Demo tests choose.
package demo
