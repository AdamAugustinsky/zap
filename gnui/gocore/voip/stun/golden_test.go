package stun

import (
	"encoding/hex"
	"testing"

	"zapfast/gocore/voip/internal/kat"
)

// Requests built by whatsapp-rust 5e72baf's stun.rs from the same inputs.
func TestGoldenRequests(t *testing.T) {
	g := kat.Golden(t).Sub(t, "stun")
	var tx [12]byte
	copy(tx[:], g.Hex(t, "tx"))
	token, key := g.Hex(t, "token"), g.Hex(t, "key")
	ep, ok := XORRelayEndpoint(g.Str(t, "endpoint", "ip"), uint16(g.Uint(t, "endpoint", "port")))
	if !ok || hex.EncodeToString(ep[:]) != g.Str(t, "endpoint", "xor") {
		t.Fatal("endpoint")
	}
	var ssrcs [9]uint32
	for i, s := range g.List(t, "stream_ssrcs") {
		ssrcs[i] = uint32(s.Get(t).(float64))
	}
	pid := uint32(9)
	ping := WhatsAppPing(&tx)
	for name, got := range map[string][]byte{
		"wasm_allocate":           BuildWASMAllocate(&tx, token, &ep, key, g.Str(t, "call_id"), g.Str(t, "lid")),
		"android_allocate":        BuildAndroidAllocate(&tx, token, 0x1234_5678, nil, key, false),
		"android_allocate_pid_fp": BuildAndroidAllocate(&tx, token, 0x1234_5678, &pid, key, true),
		"group_allocate": BuildWASMGroupAllocate(&WASMGroupAllocate{
			TransactionID: &tx, RelayToken: token, EndpointXOR: &ep, IntegrityKey: key, StreamSSRCs: &ssrcs,
			AppDataSSRC: 0xb31d_ed3e, HBHFECSSRCs: &[2]uint32{0xc1a1_7938, 0x1bb2_0c84}, ParticipantPIDs: []uint32{5, 3, 5, 9},
		}),
		"ping":             ping[:],
		"token_attr":       appendAttr(nil, attrRelayToken, token),
		"binding_mi_fp":    EncodeRequest(MsgBindingRequest, &tx, appendAttr(nil, attrRelayToken, token), key, true),
		"allocate_fp_only": EncodeRequest(MsgAllocateRequest, &tx, appendAttr(nil, attrRelayToken, token), nil, true),
	} {
		if hex.EncodeToString(got) != g.Str(t, name) {
			t.Errorf("%s:\n got  %x\n want %s", name, got, g.Str(t, name))
		}
	}
}
