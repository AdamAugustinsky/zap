# Zap calls after the whatsmeow move: bridge feasibility (2026-10-28)

Sources: whatsapp-rust `5e72baf` (`$R` = ~/.cargo/git/checkouts/whatsapp-rust-6e26428647c827f3/5e72baf),
whatsmeow `v0.0.0-20261007111105-c386243a72ba` (`$W`), Zap `~/Projects/zap`. Read-only; nothing built or run.
Line counts are non-test lines (stopped at the first inline `#[cfg(test)] mod … {`; `*tests.rs` files excluded).

## 1. Where the VoIP engine gets its inputs

whatsapp-rust has three layers. Only the top one touches the live `Client`.

**Control plane: bound to `Client`, cannot be reused without the Rust socket.**
- `$R/src/voip/facade.rs` (4,838), `src/client/voip.rs` (2,253), `src/handlers/call.rs` (1,684), `wacore/src/voip_control/registry.rs` (3,241).
- Incoming callKey: `facade.rs:510-605` `build_spec`. It takes our LID from `client.lid()` (531), picks our `<enc>` with `MediaOffer::enc_for` (564; type in `wacore/src/types/call.rs:9-75`), decrypts with `client.signal().decrypt_message(call_creator, enc_type, ciphertext)` (569-574), unpads (575), protobuf-decodes `wa.Message.call.call_key` (577-584), then calls `MediaSessionSpec::from_relay(...)` (594).
- Outgoing offer: `facade.rs:1854-2110` `place_call`.
  - Generates a random 32-byte callKey (1892) and wraps it as padded `wa.Message{call{callKey}}`.
  - Encrypts per device through `encrypt_for_devices_with_sessions_raw` under the session locks (1903-1935).
  - Adds `<device-identity>` when any `pkmsg` is present (1960-1975) and the stored tctoken as `<privacy>` (1979).
  - Sends the offer with a stanza id. **The caller's relay comes back on the `<ack type=offer>` for that id** (1981-2005, `register_ack_waiter`).
  - Afterwards it issues a tctoken (2104) and waits for the relay (2110).
- Device lists come from `client.get_user_devices` (`facade.rs:648,708,893`).
- Group epochs are Signal-decrypted the same way (`handlers/call.rs:1435-1447`).
- An offer is acknowledged with `<receipt id=<call stanza id>><offer/></receipt>` (`handlers/call.rs:20-23,1498`).

**Stanza builders and parsers: pure, no `Client`.** These live in `$R/wacore/src/stanza/call.rs` (1,229):
- `parse_call_stanza` :16
- `build_offer_ack_receipt` :468
- `build_offer` :719 (child order is enforced by the server: error 439, :7, :498)
- `build_accept` :812
- `build_preaccept` :956
- `build_terminate` :1096
- `build_video_state` :1122
- `build_mute_v2` :1177
- `build_reject` :1194

Group and call-link builders are in `stanza/group_call.rs` (1,295).

**Media plane: transport-independent, already behind a seam.**
- `wacore/src/voip` (28,975, sans-IO): `CallEngine` (`engine.rs`), `run_call(rt, transport, relay_events, CallChannels, engine)` (`driver.rs:690`, channels at :43), STUN/WARP, E2E and HBH SRTP (AES-CTR plus HMAC-SHA1 MI tag), RTP/RTCP, H.264 RFC 6184 packetization, SFrame, and MLow. It never builds a stanza: the only `NodeBuilder` use is in test code (`voip/mod.rs:151+`).
- `wacore/src/voip_control` (8,567) is the neutral contract.
  - `MediaSessionSpec` (`mod.rs:306`) is the full input: key, direction, `self_lid`, `peer_lid`, 32-byte `call_key`, ssrc, audio spec, `relay_token`, `auth_token`, `relay_ip`/`relay_port`, `integrity_key`, `warp_mi_tag_len`, video/sframe flags, optional group.
  - Spec construction: `spec_build.rs:120` `from_relay`, :46 `for_group`.
  - Relay parsing: `relay_parse.rs:196` `parse_relay_data` and :299 `parse_relay_data_from_ack`. Both take a `NodeRef`.
  - Caller-side answer: `control.rs:548` `PeerAnswer{answering_lid, audio_codec}`.
  - Engine config: `engine_bridge.rs:42`.
  - Relay pipe traits: `transport.rs:68/193/206`.
- Native relay transport (UDP → DTLS → SCTP → DataChannel id 0): `$R/src/voip/transport/native.rs` (1,105). `RelayMediaChannelFactory` (:659, impl :808) is Client-free.

**Existing seams.** The `voip_control` seam (`VoipMediaBackend` :601 / `VoipMediaSession` :649, `src/voip_control/mod.rs:1-9`, `fake_backend.rs`) points the other way from what we need: it lets a foreign *media* backend plug into whatsapp-rust's *control plane*. Nothing lets an outside party inject signaling or keys into the control plane.

The resident backend `src/voip_control/wacore_backend.rs` still holds `Weak<Client>` (100-125) for the runtime, `is_socket_connected`, the registry's video teardown and the relay factory (182-196, 330-345). `examples/voip-cli` (2,725) drives the full facade over a live `Client` (`main.rs:30,1121,1199`).

The usable cut is one level lower, at `MediaSessionSpec → CallEngine::new → run_call` plus `RelayMediaChannelFactory`. That reimplements roughly 300 lines of `wacore_backend.rs` without `Client`. Upstream Zap uses only the high-level facade (`src/calls.rs` via `voip().accept/call`, `CallHandle`), so that layer goes away.

## 2. What a "call engine only" Rust library needs from Go, and what whatsmeow can supply

| Need | whatsmeow support |
|---|---|
| Inbound `<call>` nodes | `call.go:17-109` dispatches `CallOffer/Accept/PreAccept/Transport/Terminate/Reject/OfferNotice/RelayLatency` with `Data: &child`. Unknown tags (`video`, `mute_v2`, `enc_rekey`, group updates) arrive as `UnknownCallEvent{Node}` with the **full** node (:106). **Gap:** the parent `<call id>` is dropped for known tags (`types/call.go:11-18` has no stanza id), and the offer receipt needs it. The handler map is private (`client.go:121,303,875`), so this needs a roughly 5-line fork patch (pass the parent node). |
| Send arbitrary nodes | `DangerousInternals().SendNode` (`internals.go:178`); `RejectCall` exists (`call.go:113`). |
| Outgoing relay (`<ack>` to the offer id) | `WaitResponse(id)` (`internals.go:638`) before `SendNode`. `receiveResponse` routes `ack` tags by id (`request.go:71-89`). |
| Decrypt the offer `<enc>` → callKey | `DangerousInternals().DecryptDM(ctx, encNode, from, isPreKey, serverTS)` (`internals.go:362`; `message.go:586-638`) runs the Signal session or prekey decrypt **and unpads** (633). Go then unmarshals `waE2E.Message.Call.CallKey`. Same path for group `enc_rekey`. |
| Encrypt the callKey per device | `EncryptMessageForDevices(ctx, devices, id, plaintext, nil, attrs)` (`internals.go:742`; `send.go:1286-1360`) pads (1439), fetches prekeys for missing sessions (1340), maps PN→LID and migrates sessions (1304-1323), and returns `<to jid><enc/></to>` nodes plus an `includeIdentity` flag. `MakeDeviceIdentityNode` is at `internals.go:738`. |
| Device lists | `GetUserDevices` (`user.go:479`); `Usync` internal (`internals.go:842`). |
| Own identity | `GetOwnID`/`GetOwnLID` (`internals.go:118/122`); ADV account in `Store.Account`. |
| Privacy tokens | `EnsureTCToken` (`internals.go:798`, `tctoken.go:120`), `IssuePrivacyToken` (`internals.go:810`). |
| LID/PN mapping for `call-creator` | `Store.LIDs`, `MigrateSessionStore` (`internals.go:346`); `call.go:34-39` exposes `caller_pn`/`caller_lid`. |
| Acks | whatsmeow auto-acks every `<call>` generically (`call.go:18`, `receipt.go:115-185`). Typed acks (`type=video`; group raise-hand, screen-share, waiting-room) must be sent extra. The group-only typed acks are meant to *replace* the generic ack (`handlers/call.rs:56-65`), which whatsmeow cannot suppress without the same patch. |

**Rust side, inputs.** One `open` per call:
- call_id, generation, direction
- self LID and peer LID (the answering device's LID on the caller side)
- the 32-byte callKey
- the `<relay>` node (whatsmeow `binary.Marshal` bytes; wacore `unmarshal_ref`, `wacore/binary/src/marshal.rs:36`; mind the leading flag byte) or the parsed fields
- audio format/codec, video flag

Commands after `open`: mute, enable/disable video, keyframe request, `RekeyRecv(PeerAnswer)` from `<accept>`, close.

**Rust side, outputs.** `MediaEvent` values (`RelayAllocated`, `PeerVideoStateChanged`, `AudioSilent`, `Closed`, stats) plus audio/video frames over the existing zfcore frame path (`zfcore/src/frames.rs`, `gnui/core/frames.go`). The engine never asks for a stanza mid-call, so there is no Go↔Rust signaling round trip during a call.

## 3. Feasibility verdict

**Feasible, and Signal ownership stays clean.** whatsmeow owns the socket, the Signal sessions and the store, and Rust only ever sees a plaintext 32-byte key, so the two sides never share ratchet state.

The real cost is that the 1:1 signaling state machine must be rewritten in Go: offer receipt, preaccept/accept, sibling dismiss, terminate/reject, video upgrade/downgrade with typed acks, mute, the outgoing offer, and the timeouts. The Rust control plane is about 12k lines tied to `Client` and cannot be reused.

Main risks:
1. **Wire fidelity of a hand port.** Offer child order (439), capability bytes (`stanza/call.rs:520-540`), MLow capability negotiation, and video-state edge cases. Mitigation: port `stanza/call.rs` line by line and diff against whatsapp-rust's unit-test fixtures.
2. **whatsmeow fork patch** for the call stanza id and typed-ack suppression. Small, but a fork to carry or upstream.
3. **Prekey/LID edge cases.** A pkmsg offer from a PN `call-creator` needs the LID mapping and session migration before `DecryptDM`. A decrypt failure should answer `terminate reason=enc` (`handlers/call.rs:1568-1599`).
4. **Group calls and call links** add `enc_rekey` fanout and roster/epoch logic (`stanza/group_call.rs` 1,295, `voip_control/group.rs` 459, the group parts of `registry.rs`). Keep them out of v1.
5. whatsapp-rust stays a build dependency (for wacore plus the native transport) until the Go port lands.

Rough glue size:
- **Rust:** about 800-1,200 new lines for the C ABI, a Client-free engine host replacing `wacore_backend.rs`, and the event/command marshalling. Zap's `src/call_audio.rs` (2,013) and the openh264 camera/decoder parts of `src/calls.rs` (about 1.5k) are kept. Depend on `wacore` features `voip`/`voip-mlow` plus a copy of `native.rs`, or on `whatsapp-rust` with the `voip` feature and the Client left unused.
- **Go:** about 2,500-3,500 lines for 1:1. That breaks down as:
  - stanza build/parse port: about 900
  - call state machine, replacing `src/backend/worker/calls.rs` (1,361) and the facade logic: about 1,500
  - whatsmeow adapters (decrypt/encrypt/devices/tctoken/ack waiter): about 400
  - purego binding: about 300
  - fork patch: about 10

**Alternative: link the Rust engine as a second companion device.** It needs no bridge and keeps the full upstream call stack. It is worse overall:
- The user has to pair twice (QR or pair code on the phone), and it uses one of the 4 companion slots.
- To receive offers it must stay connected permanently, so it runs a full second WhatsApp client: message, app-state and history sync, a second store, double decryption.
- Both devices ring, so the UI has to merge or suppress duplicates and handle "accepted elsewhere" between our own two devices.
- It keeps the entire whatsapp-rust protocol stack the migration is meant to remove.

It is only reasonable as a short-lived stopgap.

## 4. Size of a full Go port of the call engine

Non-test lines in `$R`:

| Module | Lines | Go library available |
|---|---|---|
| `wacore/src/voip/mlow` (MLow codec, encoder and decoder) | 16,197 | None. Proprietary Opus-forked CELP, validated byte-for-byte against captured WASM vectors (`mlow/mod.rs:1-8`). The largest and riskiest piece. |
| `engine.rs` / `driver.rs` / `session.rs` | 4,230 / 1,496 / 1,134 | None (WhatsApp-specific state machine). |
| `rtp.rs` + `rtcp.rs` | 1,341 | pion/rtp and pion/rtcp for framing; WARP extensions are custom. |
| `stun.rs` + `warp.rs` + `demux.rs` | 777 | pion/stun handles TLV; WhatsApp MI/allocate/ping is custom. |
| `e2e_srtp` + `hbh_srtp` + `sframe` | 922 | pion/srtp does **not** fit (custom HKDF, 4-byte HMAC-SHA1 MI, libsrtp ICM carry). Use Go stdlib aes/ctr/hmac/hkdf. |
| `h264.rs` (packetize/reassemble only) | 715 | pion/rtp/codecs H264 payloader/depacketizer. |
| `group_media` + `group_audio` | 997 | None. |
| `audio`, `opus_packet`, `codec_probe`, `tap`, `mod` | 1,166 | Mostly custom. |
| `src/voip/transport/native.rs` (DTLS, SCTP, DataChannel) | 1,105 | pion/dtls, pion/sctp and pion/datachannel cover it (self-signed client, no cert verification). |
| `wacore/src/voip_control` (contract, registry, relay parse, group) | 8,567 | Mostly not needed in Go; about 1.5k would be ported (relay_parse 488, spec_build, ssrc, kdf, group). |
| Control plane (facade, client/voip, handlers/call, stanza call/group_call, types) | about 12,300 | Covered by the section 3 work for 1:1. |

Codecs:
- **Opus:** Zap links `opus` 0.4 (libopus via the `voip-libopus` feature). In Go the options are hraban/opus (cgo) or loading libopus at runtime with purego; pion/opus is decode-only and incomplete.
- **H.264 encode/decode:** the engine never touches pixels (`h264.rs:1-6`). Zap uses `openh264` 0.9 (`Cargo.toml:122-125`). In Go that means openh264 or VideoToolbox through purego.

Estimate: media engine without MLow is about 13k Rust lines → about 9-11k Go lines. MLow is about 16k more, ported numerically exactly. With MLow skipped and only standard Opus offered (MLow's "in-profile escape", `mlow/mod.rs:5-7`), a Go engine is roughly half the size, but the call-quality and compatibility consequences are unverified.

## Summary
1. The engine's real seam is `MediaSessionSpec → CallEngine → run_call` with `RelayMediaChannelFactory`: Client-free, and the only secret it needs is the plaintext 32-byte callKey.
2. whatsmeow can do every network and Signal step: raw call nodes (events), `SendNode`, `WaitResponse` for the relay ack, `DecryptDM` (unpads), `EncryptMessageForDevices` (prekeys, LID mapping), devices, tctokens. The one gap is a small fork patch to expose the `<call id>` and suppress the generic ack for typed acks.
3. The bridge is feasible. It needs about 1k new Rust lines and about 2.5-3.5k Go lines for 1:1, mostly a faithful port of `wacore/src/stanza/call.rs` and the 1:1 call state machine; group calls and call links come later.
4. A second-companion-device design avoids the bridge but costs a second pairing, a permanent second full client, duplicate ringing, and keeps whatsapp-rust whole, so it is a stopgap at best.
5. A full Go engine is about 29k Rust lines plus about 1.1k transport. pion covers DTLS/SCTP/RTP/RTCP/H.264 packetization; SRTP, STUN integrity and WARP are custom, and the 16k-line MLow codec has no Go equivalent and is the long pole.
