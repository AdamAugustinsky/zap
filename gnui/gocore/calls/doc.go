// Package calls is the Go backend's 1:1 WhatsApp calls (WP7's call
// bridge): the signaling on whatsmeow, the media on the Go call engine.
//
// whatsmeow keeps the socket and the Signal sessions. This package builds
// and reads the <call> stanzas as whatsapp-rust 5e72baf's
// wacore/src/stanza/call.rs does (stanza.go, parse.go), decrypts an offer's
// callKey or makes and encrypts one for each of the peer's devices
// (whatsmeow.go), and runs the call's state machine (calls.go): the offer
// and its receipt, preaccept and accept, the dismissal of the callee's
// other devices, rejects and terminates and the outcome each gives, mute,
// and the video upgrade. The media (relay, SRTP, audio and video codecs,
// devices, frames) is gocore/voip/engine behind the Media interface
// (media.go, engine_media.go): Go hands it the callKey and the <relay>, it
// hands back events, and the decoded pictures are voip/video's Frames.
//
// Group calls and call links are not bridged: a group's offer is refused,
// and gocore/wa refuses JoinCallLink. Without media every call is refused
// (NotAvailable).
package calls
