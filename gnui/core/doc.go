// Package core drives ZapFast's Rust core (libzapfast_core.dylib, the C ABI
// in zfcore/include/zfcore.h) from Go, through gonativeui/clib: no cgo, and
// no allocation per command or per event once warm (D35, D49).
//
// # Opening
//
// The library is loaded once ([Load]) from the app bundle's Frameworks, from
// $ZAPFAST_CORE, or in development from the newest cargo build
// ([FindLibrary]); its ZF_ABI and schema hash must equal the generated
// package abi's, or loading fails. [Open] opens one account's core. The
// first frame must never wait for either: a cold dlopen of the 30 MB library
// takes about 330 ms, over the whole 150 ms startup budget. So an app calls
// [OpenAsync] first thing in main, keeps building frames (skeleton rows),
// and takes the core from [Pending] when its wake arrives. The startup
// permit, [Core.Start], comes after the first frame.
//
// # Events
//
// A goroutine of the core's waits in the network poller for the core's
// event descriptor (it never reads it, as zfcore.h asks), then calls the
// Wake function of the [Config] (platform.Wake in an app) and sleeps until
// the UI has drained the queue. On the UI thread, [Core.Drain] calls
// zf_drain into one buffer allocated at open and decodes each record into
// one reused abi.Event, which it hands to a [Handler]. The event's byte
// fields point into that buffer: they are valid during the Handler call
// only, so anything kept is copied.
//
// # Commands, blobs and errors
//
// [Core.Send] encodes a command into a reused buffer and calls zf_send. Each
// call site keeps its clib.Call and the zf_err it passes in one heap struct,
// guarded by a lock that never parks, so any goroutine may call and an
// error is read from the call that made it, whatever OS thread that ran on.
// Turning a failure into a Go error is the only allocation. Blobs are read
// in ranges into the caller's buffer ([Core.ReadBlob]) and put with
// [Core.PutBlob] for outgoing images and voice notes.
//
// Who frees blobs: one the UI puts is taken by the command that names it,
// or freed with [Core.FreeBlob] when the command fails. One an event names
// (a thumbnail, a decoded picture) is the core's: screens need not free it.
// The core shares one id among records with the same bytes, keeps the 64
// MiB of them touched last (made, named again or read), and frees older
// ones itself, never one touched in the last 30 s; a read after that fails
// with ENotFound, and loading the message again gives a fresh one.
// FreeBlob on an event blob only lets go early.
//
// # Calls
//
// [Core.Frame] and [Core.ReleaseFrame] (the [Frames] interface) hand out a
// call's video frames, I420 in the core's memory, on [StreamLocal] (our
// camera), [StreamRemote] (a one-to-one peer) and [StreamMember]+i (group
// participant i of the newest Call event). Take one per stream, draw it,
// give it back before taking that stream again; neither allocates.
// [Core.CallGeneration] (the [CallNumbers] interface) numbers the calls the
// UI places from the counter the core numbers offered calls with. On the
// demo core, [DemoCall] rings a call.
//
// # Closing
//
// [Core.Close] stops the event goroutine, then zf_core_close, which blocks
// for up to its timeout (upstream's runtime takes up to 3 s): never call it
// on the UI thread while the app runs.
package core
