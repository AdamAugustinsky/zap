# Zap's patch to whatsmeow

This is go.mau.fi/whatsmeow v0.0.0-20261007111105-c386243a72ba, copied
unchanged from the module cache, plus the change below (every line is marked
`ZAP PATCH`). gnui/go.mod points the module here with a `replace`. To move to
a newer whatsmeow, copy the new version over this one and reapply the patch,
or drop it once whatsmeow carries the same things.

The call bridge (gnui/gocore/calls, docs/go-backend/calls-bridge.md) does
call signaling over whatsmeow and needs what whatsmeow hides:

1. `types.BasicCallMeta.StanzaID`: the id of the `<call>` stanza. An
   incoming offer must be answered with `<receipt id=<that id>><offer/></receipt>`
   (whatsapp-rust `build_offer_ack_receipt`), and whatsmeow passes known call
   actions on with only the action child (`call.go`). The handler map is
   private, so this cannot be added from outside.
2. A `<call><video/></call>` is acknowledged with a typed
   `<ack class="call" type="video">` instead of the generic ack
   (whatsapp-rust `build_call_video_ack`, `handlers/call.rs`): the peer's
   video upgrade waits for that typed ack. whatsmeow acknowledges every
   `<call>` itself and cannot be told to skip one.

3. `types.BasicCallMeta.Participant`: a group call's stanzas reach us from
   the call service (`<call-id>@call`) with the sending device in the
   `<call participant>` attribute. Who rang, who may end the call and whose
   epoch an `<enc_rekey>` carries are all read from it (whatsapp-rust
   `routed_call_sender`).
4. A group call's `<waiting_room_update>`, `<user_action>` or
   `<screen_share>` is acknowledged with a typed ack whose type is the
   child's tag (whatsapp-rust `build_call_control_ack`), in place of the
   generic one.

Files: `types/call.go` (the fields), `call.go` (their values and
`typedCallAck`).
