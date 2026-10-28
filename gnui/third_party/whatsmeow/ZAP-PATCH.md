# Zap's patch to whatsmeow

This is go.mau.fi/whatsmeow v0.0.0-20261007111105-c386243a72ba, copied
unchanged from the module cache, plus the change below (every line is marked
`ZAP PATCH`). gnui/go.mod points the module here with a `replace`. To move to
a newer whatsmeow, copy the new version over this one and reapply the patch,
or drop it once whatsmeow carries the same two things.

The call bridge (gnui/gocore/calls, docs/go-backend/calls-bridge.md) does
1:1 call signaling over whatsmeow and needs two things whatsmeow hides:

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

Files: `types/call.go` (the field), `call.go` (the field's value and
`typedCallAck`).
