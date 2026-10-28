// Package core is what the UI uses of an account's backend: the [Conn] it
// sends commands to and drains events from, with [Config] to open one,
// [OpenAsync] and [Pending] to open it off the UI thread, and the errors,
// call frames ([Frames], [CallNumbers]) and voice notes ([VoiceDecoder])
// screens use besides. The backend itself is gocore (the demo's, in
// gocore/demo, or WhatsApp's, in gocore/wa); tests run without one on
// core/fake. It speaks the records of package abi (abi/zf.schema), which
// began as the C ABI of ZapFast's Rust core: the status codes and call names
// in errors keep that ABI's names.
//
// # Opening
//
// The first frame must never wait for the backend: loading SQLCipher,
// taking an account over from the Rust core and opening the stores take
// longer than the 150 ms startup budget. So an app calls [OpenAsync] first
// thing in main, keeps building frames (skeleton rows), and takes the core
// from [Pending] when its wake arrives. The startup permit, Conn.Start,
// comes after the first frame.
//
// # Events
//
// The core calls the Wake function of the [Config] (platform.Wake in an
// app) when events wait. On the UI thread, Conn.Drain decodes each record
// into one reused abi.Event, which it hands to a [Handler]. The event's byte
// fields are valid during the Handler call only, so anything kept is
// copied.
//
// # Commands, blobs and errors
//
// Conn.Send takes a command; a refusal is an [*Error] with its code
// ([IsCode]). Blobs are read in ranges into the caller's buffer
// (Conn.ReadBlob) and put with Conn.PutBlob for outgoing images and voice
// notes.
//
// Who frees blobs: one the UI puts is taken by the command that names it,
// or freed with Conn.FreeBlob when the command fails. One an event names
// (a thumbnail, a decoded picture) is the core's: screens need not free it.
// The core keeps the ones touched last and frees older ones itself; a read
// after that fails with ENotFound, and loading the message again gives a
// fresh one. FreeBlob on an event blob only lets go early.
//
// # Calls
//
// Frame and ReleaseFrame (the [Frames] interface) hand out a call's video
// frames, I420, on [StreamLocal] (our camera), [StreamRemote] (a one-to-one
// peer) and [StreamMember]+i (group participant i of the newest Call
// event). Take one per stream, draw it, give it back before taking that
// stream again. CallGeneration (the [CallNumbers] interface) numbers the
// calls the UI places. On the demo core, [DemoCall] rings a call.
//
// # Closing
//
// Conn.Close blocks for up to its timeout: never call it on the UI thread
// while the app runs.
package core
