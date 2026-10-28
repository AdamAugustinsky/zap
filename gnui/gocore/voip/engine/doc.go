// Package engine is Zap's WhatsApp call engine in Go, ported from
// whatsapp-rust 5e72baf's wacore/src/voip (engine.rs, driver.rs,
// session.rs, audio.rs, codec_probe.rs, group_media.rs, group_audio.rs)
// and the host around it that Zap's zfcalls crate was.
//
// Three layers, as in whatsapp-rust:
//
//   - [Engine] is sans-I/O: the relay control plane (the STUN allocate, the
//     one-second keepalive and WhatsApp ping, consent replies, the allocate
//     timeout) and the media plane (MLow, or standard Opus through a
//     [ForeignCodec]; E2E SRTP and SRTCP keyed from the callKey; inbound
//     SFrame; the playout jitter buffer; H.264 RTP with PLI, NACK
//     retransmission and keyframe gating; sender reports; the audio-health
//     watchdog; the codec probe that rescues a peer speaking Opus on MLow's
//     profile). It owns no clock, socket or goroutine.
//   - [Run] drives one engine over a relay.Transport: one input at a time,
//     every output executed, one timer, an outbound queue that keeps or
//     drops whole access units and paces video.
//   - [Open] is the call's host: it builds the [Config] from the <relay>
//     block ([ParseRelay], [NewConfig]), dials the relay, and wires the
//     microphone, speaker, camera, H.264 encoder and decoder ([Devices]) to
//     the driver, reporting [CallEvent]s.
//
// A group call ([Engine.ConfigureGroup], group_engine.go) keys each participant's
// streams from the roster's shared epoch ([GroupMediaRegistry]), decodes
// and mixes up to 16 members ([ParticipantAudioMixer]), puts each member's
// pictures on their own tile, holds media until an epoch is installed, and
// redials the relay when the roster moves it. The encoder's
// bitrate follows the peer's RTCP (loss, REMB, send drops) between fixed
// bounds; Zap's video_rate.rs also moved the picture size, which this does
// not.
package engine
