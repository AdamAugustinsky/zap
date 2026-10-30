// Package calls is the Go backend's WhatsApp calls (WP7's call bridge),
// one to one and group: the signaling on whatsmeow, the media on the Go
// call engine.
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
// Group calls (group.go, the stanzas of group_stanza.go): an offer rings
// in its group with its roster, is answered to the call's own address, and
// opens the media on the roster's relay; group_update and enc_rekey keep
// the roster and the epochs (decrypted, or made and sent to every
// connected device when the service asks) flowing to the media; only the
// creator or the service ends one. A call is placed from a group chat to
// its members, and a call link is joined, through its waiting room when it
// has one. A call link is never created. Without media every call is
// refused (NotAvailable).
package calls
