// Ported from whatsapp-rust 5e72baf wacore/src/voip/stun.rs (tests), MIT.

package stun

import (
	"bytes"
	"encoding/binary"
	"encoding/hex"
	"hash/crc32"
	"slices"
	"testing"

	"zapfast/gocore/voip/internal/kat"
	"zapfast/gocore/voip/kdf"
)

func tx12(t *testing.T, k kat.Doc) *[12]byte {
	var tx [12]byte
	copy(tx[:], k.Hex(t, "stun", "tx"))
	return &tx
}

func TestCRC32IsIEEE(t *testing.T) {
	k := kat.KATs(t)
	if uint64(crc32.ChecksumIEEE([]byte("abc"))) != k.Uint(t, "stun", "crc32_abc") || crc32.ChecksumIEEE([]byte("abc")) != 0x3524_41c2 {
		t.Fatal("crc")
	}
}

func TestAttrAndEndpointMatchKAT(t *testing.T) {
	k := kat.KATs(t)
	if hex.EncodeToString(appendAttr(nil, attrRelayToken, k.Hex(t, "stun", "relayToken"))) != k.Str(t, "stun", "attr_token") {
		t.Fatal("attr")
	}
	ep, ok := XORRelayEndpoint("157.240.226.133", 3478)
	if !ok || hex.EncodeToString(ep[:]) != k.Str(t, "stun", "xorEndpoint") {
		t.Fatal("endpoint")
	}
	sub := NativeSenderSubscription(uint32(k.Uint(t, "inputs", "ssrc")))
	if hex.EncodeToString(sub[:]) != k.Str(t, "stun", "nativeSenderSub") {
		t.Fatal("sub")
	}
	if _, ok := XORRelayEndpoint("1.2.3", 1); ok {
		t.Fatal("3 octets")
	}
}

func TestEncodeRequestMIAndFingerprintMatchKAT(t *testing.T) {
	k := kat.KATs(t)
	attrs := appendAttr(nil, attrRelayToken, k.Hex(t, "stun", "relayToken"))
	key := k.Hex(t, "stun", "miKey")
	if hex.EncodeToString(EncodeRequest(MsgAllocateRequest, tx12(t, k), attrs, key, false)) != k.Str(t, "stun", "minimalMi") {
		t.Fatal("minimal")
	}
	if hex.EncodeToString(EncodeRequest(MsgAllocateRequest, tx12(t, k), attrs, key, true)) != k.Str(t, "stun", "withFp") {
		t.Fatal("fingerprint")
	}
}

func TestTruncatedSTUNDrivesNoAllocateDecision(t *testing.T) {
	var tx [12]byte
	ok := EncodeRequest(MsgAllocateSuccess, &tx, nil, nil, false)
	if !IsAllocateOrBindingSuccess(ok) {
		t.Fatal("bare success")
	}
	tr := bytes.Clone(ok)
	tr[2], tr[3] = 0, 0x40
	if IsAllocateOrBindingSuccess(tr) {
		t.Fatal("truncated success")
	}
	te := EncodeRequest(MsgAllocateError, &tx, nil, nil, false)
	te[2], te[3] = 0, 0x40
	if IsAllocateError(te) {
		t.Fatal("truncated error")
	}
}

func TestErrorCodeValueBeyondBodyIsNotParsed(t *testing.T) {
	p := []byte{0x01, 0x13, 0x00, 0x04}
	p = binary.BigEndian.AppendUint32(p, magic)
	p = append(p, make([]byte, 12)...)
	p = append(p, 0x00, 0x09, 0x00, 0x04, 0x00, 0x00, 0x04, 0x01)
	if !IsAllocateError(p) {
		t.Fatal("complete error")
	}
	if _, ok := ParseErrorCode(p); ok {
		t.Fatal("value beyond the body parsed")
	}
	// Inside the body it is read.
	binary.BigEndian.PutUint16(p[2:], 8)
	if c, ok := ParseErrorCode(p); !ok || c != 401 {
		t.Fatal(c)
	}
}

func TestUnalignedSTUNBodyLengthIsRejected(t *testing.T) {
	var tx [12]byte
	p := append(EncodeRequest(MsgAllocateSuccess, &tx, nil, nil, false), 0xaa)
	p[2], p[3] = 0, 1
	if IsAllocateOrBindingSuccess(p) {
		t.Fatal("unaligned accepted")
	}
}

func dv(b []byte, i *int) uint64 {
	var v uint64
	var shift uint
	for {
		c := b[*i]
		*i++
		v |= uint64(c&0x7f) << shift
		if c&0x80 == 0 {
			return v
		}
		shift += 7
	}
}

func decodeStreamDescriptors(t *testing.T, b []byte) map[[2]uint32]uint32 {
	out := map[[2]uint32]uint32{}
	for i := 0; i < len(b); {
		if dv(b, &i) != 1<<3|2 {
			t.Fatal("top-level field 1")
		}
		end := int(dv(b, &i))
		end += i
		var idx, sub, ssrc uint32
		have := false
		for i < end {
			switch dv(b, &i) >> 3 {
			case 1:
				idx = uint32(dv(b, &i))
			case 2:
				sub = uint32(dv(b, &i))
			case 3:
				ssrc, have = uint32(dv(b, &i)), true
			default:
				t.Fatal("unexpected field")
			}
		}
		if !have {
			t.Fatal("no ssrc")
		}
		out[[2]uint32{idx, sub}] = ssrc
	}
	return out
}

func TestWASMAllocateCarriesDynamicStreamDescriptors(t *testing.T) {
	k := kat.KATs(t)
	ep, _ := XORRelayEndpoint("157.240.226.133", 3478)
	participant := kdf.FormatParticipantID("12345:0@lid")
	alloc := BuildWASMAllocate(tx12(t, k), k.Hex(t, "stun", "relayToken"), &ep, k.Hex(t, "stun", "miKey"), "CALL-ID-0001", participant)
	attrs := ParseAttributes(alloc)
	if attrs[0].Type != attrRelayToken {
		t.Fatal("token first")
	}
	var sd, mi []byte
	hasEP := false
	for _, a := range attrs {
		switch a.Type {
		case attrStreamDescriptors:
			sd = a.Value
		case attrMessageIntegrity:
			mi = a.Value
		case attrWASMRelayEndpoint:
			hasEP = true
		}
	}
	if !bytes.Equal(sd, WASMStreamDescriptors("CALL-ID-0001", participant)) || !hasEP || len(mi) != 20 {
		t.Fatal("attrs")
	}
	ping := WhatsAppPing(tx12(t, k))
	if hex.EncodeToString(ping[:]) != k.Str(t, "stun", "ping") {
		t.Fatal("ping")
	}
}

func types(attrs []Attribute) []uint16 {
	var out []uint16
	for _, a := range attrs {
		out = append(out, a.Type)
	}
	return out
}

func TestGroupAllocateMatchesCapturedSubscriptionAndHBHFECShape(t *testing.T) {
	ssrcs := [9]uint32{0x3ea2_6c0c, 0x0bf9_9b28, 0xf42e_4556, 0x14e8_f126, 0xbb16_134f, 0x98b1_4f00, 0xe0e0_4163, 0x74ed_8516, 0xdea8_a613}
	tx := [12]byte{0, 1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11}
	ep := [6]byte{0x2c, 0x84, 0xbc, 0xe2, 0xb5, 0xc7}
	req := func(pids []uint32) []Attribute {
		return ParseAttributes(BuildWASMGroupAllocate(&WASMGroupAllocate{
			TransactionID: &tx, RelayToken: []byte{1, 2, 3}, EndpointXOR: &ep, IntegrityKey: []byte("0123456789abcdef"),
			StreamSSRCs: &ssrcs, AppDataSSRC: 0xb31d_ed3e, HBHFECSSRCs: &[2]uint32{0xc1a1_7938, 0x1bb2_0c84}, ParticipantPIDs: pids,
		}))
	}
	a := req([]uint32{2, 1, 2})
	if !slices.Equal(types(a), []uint16{attrRelayToken, attrSenderSubscriptionsV2, attrReceiverSubscriptions, attrStreamDescriptors, attrParticipantCount, attrWASMRelayEndpoint, attrMessageIntegrity}) {
		t.Fatal(types(a))
	}
	for i, want := range map[int]string{
		1: "0a1f0a1d0a0fa6e2a3a701cfa6d8d80b809ec5c5091204080110011204080210010a130a110a0fe38281870e968ab6a70793cca2f50d0a1a0a180a0e8cd889f503a8b6e65fd68ab9a10f12020801120208020a110a0f0a05bedaf7980b1202080112020802",
		2: "1202080112020802",
		3: "0a06188cd889f5030a07100118a8b6e65f0a08100218d68ab9a10f0a08080118a6e2a3a7010a0a0801100118cfa6d8d80b0a0a0801100218809ec5c5090a08080218e38281870e0a0a0802100118968ab6a7070a0a080210021893cca2f50d0a0a0803100318b8f2858d0c0a0a08041003188499c8dd01",
		4: "02",
	} {
		if hex.EncodeToString(a[i].Value) != want {
			t.Fatalf("attr %d: %x", i, a[i].Value)
		}
	}
	if one := req([]uint32{1}); !bytes.Equal(one[3].Value, WASMStreamDescriptorsFromSSRCs(&ssrcs, &[2]uint32{})) {
		t.Fatal("one pid descriptors")
	}
	if !slices.Equal(types(req(nil)), []uint16{attrRelayToken, attrStreamDescriptors, attrWASMRelayEndpoint, attrMessageIntegrity}) {
		t.Fatal("no pids")
	}
}

func TestStreamDescriptorsAnnounceTheLiveMediaSSRCs(t *testing.T) {
	p := kdf.FormatParticipantID("12345:0@lid")
	e := decodeStreamDescriptors(t, WASMStreamDescriptors("CALL-ID-0001", p))
	if e[[2]uint32{1, 0}] != kdf.VideoParticipantSSRC("CALL-ID-0001", p) || e[[2]uint32{0, 0}] != kdf.ParticipantSSRC("CALL-ID-0001", p, 0) || len(e) != 9 {
		t.Fatal(e)
	}
	if wasmStreamSlotWords() != kdf.WASMRelayStreamSlotWords {
		t.Fatal("slot words drifted from kdf's")
	}
}

func TestParseRoundTripsAttributes(t *testing.T) {
	k := kat.KATs(t)
	m := k.Hex(t, "stun", "minimalMi")
	if typ, ok := MessageType(m); !IsSTUN(m) || !ok || typ != MsgAllocateRequest {
		t.Fatal("type")
	}
	a := ParseAttributes(m)
	if len(a) != 2 || a[0].Type != attrRelayToken || !bytes.Equal(a[0].Value, k.Hex(t, "stun", "relayToken")) || a[1].Type != attrMessageIntegrity || len(a[1].Value) != 20 {
		t.Fatal(a)
	}
}

func TestParseAttributesTruncatedPaddingDoesNotPanic(t *testing.T) {
	p := append(make([]byte, 20), 0x40, 0, 0, 1, 0xab)
	a := ParseAttributes(p)
	if len(a) != 1 || !bytes.Equal(a[0].Value, []byte{0xab}) {
		t.Fatal(a)
	}
}

func TestProtobufPayloadsMatchKAT(t *testing.T) {
	k := kat.KATs(t)
	ssrc := uint32(k.Uint(t, "inputs", "ssrc"))
	pid := uint32(7)
	for name, got := range map[string][]byte{
		"voip_sender_subscriptions":      VoIPSenderSubscriptions(ssrc),
		"apk_sender_subscriptions_nopid": APKSenderSubscriptions(ssrc, nil),
		"apk_sender_subscriptions_pid":   APKSenderSubscriptions(ssrc, &pid),
		"apk_stream_descriptors":         APKStreamDescriptors(ssrc),
	} {
		if hex.EncodeToString(got) != k.Str(t, "stun_proto", name) {
			t.Fatal(name, hex.EncodeToString(got))
		}
	}
}

func TestAndroidAllocateCarriesThreeAttrs(t *testing.T) {
	k := kat.KATs(t)
	ssrc := uint32(k.Uint(t, "inputs", "ssrc"))
	a := ParseAttributes(BuildAndroidAllocate(tx12(t, k), k.Hex(t, "stun", "relayToken"), ssrc, nil, k.Hex(t, "stun", "miKey"), false))
	if !slices.Equal(types(a), []uint16{attrRelayToken, attrSenderSubscriptionsV2, attrStreamDescriptors, attrMessageIntegrity}) || !bytes.Equal(a[2].Value, APKStreamDescriptors(ssrc)) {
		t.Fatal(types(a))
	}
}

func TestPongMatching(t *testing.T) {
	k := kat.KATs(t)
	tx := tx12(t, k)
	ping := WhatsAppPing(tx)
	pong := ping[:]
	binary.BigEndian.PutUint16(pong, MsgWhatsAppPong)
	if !IsWhatsAppPong(pong, tx[:]) || !IsWhatsAppPong(pong, nil) || IsWhatsAppPong(pong, make([]byte, 12)) {
		t.Fatal("pong")
	}
}
