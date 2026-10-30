// Ported from whatsapp-rust 5e72baf wacore/src/voip/stun.rs, MIT.

// Package stun is WhatsApp's relay STUN framing: the RFC 5389 TLV encoder
// with WhatsApp's MESSAGE-INTEGRITY (HMAC-SHA1 over a pseudo-header whose
// length already counts the MI attribute) and FINGERPRINT, the Allocate
// requests of the WASM and Android clients with their hand-encoded protobuf
// attributes (testdata/stun_proto.md), the WhatsApp ping, and the response
// parsers.
//
// pion/stun is not used: the relay's MI and allocate attributes, the 0x0801
// ping and the exact attribute order are WhatsApp's, and the builders here
// are pinned byte for byte to the Rust ones. Transaction ids are passed in.
package stun

import (
	"crypto/hmac"
	"crypto/sha1"
	"encoding/binary"
	"hash/crc32"
	"slices"
	"strconv"
	"strings"

	"zapfast/gocore/voip/kdf"
)

const (
	magic          uint32 = 0x2112_a442
	fingerprintXOR uint32 = 0x5354_554e
	xorPort        uint16 = 0x2112

	attrMessageIntegrity      uint16 = 0x0008
	attrFingerprint           uint16 = 0x8028
	attrErrorCode             uint16 = 0x0009
	attrRelayToken            uint16 = 0x4000
	attrStreamDescriptors     uint16 = 0x4024
	attrReceiverSubscriptions uint16 = 0x4021
	attrSenderSubscriptionsV2 uint16 = 0x4025
	attrParticipantCount      uint16 = 0x805a
	attrWASMRelayEndpoint     uint16 = 0x0016
)

var xorAddr = [4]byte{0x21, 0x12, 0xa4, 0x42}

// Message types.
const (
	MsgBindingRequest  uint16 = 0x0001
	MsgAllocateRequest uint16 = 0x0003
	MsgBindingSuccess  uint16 = 0x0101
	MsgBindingError    uint16 = 0x0111
	MsgAllocateSuccess uint16 = 0x0103
	MsgAllocateError   uint16 = 0x0113
	MsgWhatsAppPing    uint16 = 0x0801
	MsgWhatsAppPong    uint16 = 0x0802
)

func pad4(n int) int { return (4 - n%4) % 4 }

func appendAttr(out []byte, typ uint16, value []byte) []byte {
	out = binary.BigEndian.AppendUint16(out, typ)
	out = binary.BigEndian.AppendUint16(out, uint16(len(value)))
	out = append(out, value...)
	return append(out, make([]byte, pad4(len(value)))...)
}

func pseudoHeader(typ, length uint16, txid *[12]byte) []byte {
	h := make([]byte, 20)
	binary.BigEndian.PutUint16(h, typ)
	binary.BigEndian.PutUint16(h[2:], length)
	binary.BigEndian.PutUint32(h[4:], magic)
	copy(h[8:], txid[:])
	return h
}

// EncodeRequest encodes a request: header and attrs, then the optional
// MESSAGE-INTEGRITY (when integrityKey is non-nil) and FINGERPRINT.
func EncodeRequest(typ uint16, txid *[12]byte, attrs []byte, integrityKey []byte, fingerprint bool) []byte {
	body := slices.Clone(attrs)
	if integrityKey != nil {
		mac := hmac.New(sha1.New, integrityKey)
		mac.Write(pseudoHeader(typ, uint16(len(body)+24), txid))
		mac.Write(body)
		body = appendAttr(body, attrMessageIntegrity, mac.Sum(nil))
	}
	if fingerprint {
		crc := crc32.ChecksumIEEE(append(pseudoHeader(typ, uint16(len(body)+8), txid), body...))
		body = appendAttr(body, attrFingerprint, binary.BigEndian.AppendUint32(nil, crc^fingerprintXOR))
	}
	out := make([]byte, 0, 20+len(body))
	out = binary.BigEndian.AppendUint16(out, typ)
	out = binary.BigEndian.AppendUint16(out, uint16(len(body)))
	out = binary.BigEndian.AppendUint32(out, magic)
	out = append(out, txid[:]...)
	return append(out, body...)
}

// NativeSenderSubscription is the native sender subscription (attr 0x4023):
// a count byte of 1 and the big-endian SSRC.
func NativeSenderSubscription(ssrc uint32) [5]byte {
	var b [5]byte
	b[0] = 1
	binary.BigEndian.PutUint32(b[1:], ssrc)
	return b
}

// XORRelayEndpoint XOR-encodes an IPv4 address and port (6 bytes) for the
// WASM relay endpoint attribute.
func XORRelayEndpoint(ipv4 string, port uint16) ([6]byte, bool) {
	var octets []byte
	for part := range strings.SplitSeq(ipv4, ".") {
		// Rust's u8 parse also takes one leading '+'.
		if rest, ok := strings.CutPrefix(part, "+"); ok && rest != "" && rest[0] != '+' && rest[0] != '-' {
			part = rest
		}
		if n, err := strconv.ParseUint(part, 10, 8); err == nil {
			octets = append(octets, byte(n))
		}
	}
	var b [6]byte
	if len(octets) != 4 {
		return b, false
	}
	binary.BigEndian.PutUint16(b[:], port^xorPort)
	for i := range 4 {
		b[2+i] = octets[i] ^ xorAddr[i]
	}
	return b, true
}

func wasmRelayEndpointAttr(endpointXOR *[6]byte) []byte {
	return append([]byte{0, 1}, endpointXOR[:]...)
}

// wasmStreamSlots is (stream index, sub-type, slot word) in WASM wire order:
// streams audio/video0/video1, sub-types media/FEC/NACK.
var wasmStreamSlots = [9][3]uint32{
	{0, 0, 0}, {0, 1, 1}, {0, 2, 4},
	{1, 0, 2}, {1, 1, 3}, {1, 2, 5},
	{2, 0, 7}, {2, 1, 8}, {2, 2, 6},
}

func wasmStreamSlotWords() [9]uint32 {
	var out [9]uint32
	for i, s := range wasmStreamSlots {
		out[i] = s[2]
	}
	return out
}

func pbTag(out []byte, field, wire uint32) []byte {
	return kdf.AppendVarint(out, uint64(field<<3|wire))
}

func pbZigzag(n int64) uint64 { return uint64(n<<1 ^ n>>63) }

func pbLenDelim(out []byte, field uint32, b []byte) []byte {
	out = pbTag(out, field, 2)
	out = kdf.AppendVarint(out, uint64(len(b)))
	return append(out, b...)
}

// WASMStreamDescriptors is the call-specific WASM StreamDescriptors (0x4024).
func WASMStreamDescriptors(callID, selfParticipantID string) []byte {
	var ssrcs [9]uint32
	for i, s := range wasmStreamSlots {
		ssrcs[i] = kdf.ParticipantSSRC(callID, selfParticipantID, s[2])
	}
	return WASMStreamDescriptorsFromSSRCs(&ssrcs, &[2]uint32{})
}

// WASMStreamDescriptorsFromSSRCs builds descriptors from an explicit SSRC
// plan; zero SSRCs are skipped.
func WASMStreamDescriptorsFromSSRCs(streamSSRCs *[9]uint32, hbhFECSSRCs *[2]uint32) []byte {
	var out []byte
	for i, slot := range wasmStreamSlots {
		ssrc := streamSSRCs[i]
		if ssrc == 0 {
			continue
		}
		var d []byte
		if slot[0] != 0 {
			d = pbTag(d, 1, 0)
			d = kdf.AppendVarint(d, uint64(slot[0]))
		}
		if slot[1] != 0 {
			d = pbTag(d, 2, 0)
			d = kdf.AppendVarint(d, uint64(slot[1]))
		}
		d = pbTag(d, 3, 0)
		d = kdf.AppendVarint(d, uint64(ssrc))
		out = pbLenDelim(out, 1, d)
	}
	for i, ssrc := range hbhFECSSRCs {
		if ssrc == 0 {
			continue
		}
		var d []byte
		d = pbTag(d, 1, 0)
		d = kdf.AppendVarint(d, uint64(i+3))
		d = pbTag(d, 2, 0)
		d = kdf.AppendVarint(d, 3)
		d = pbTag(d, 3, 0)
		d = kdf.AppendVarint(d, uint64(ssrc))
		out = pbLenDelim(out, 1, d)
	}
	return out
}

// WASMGroupSenderSubscriptions is the group sender subscription protobuf
// for video, secondary video, audio and app-data, in that order.
func WASMGroupSenderSubscriptions(streamSSRCs *[9]uint32, appDataSSRC uint32, participantPIDs []uint32) []byte {
	pids := normalizedPIDs(participantPIDs)
	var out []byte
	out = append(out, wasmSenderSubscription(streamSSRCs[3:6], pids, true)...)
	out = append(out, wasmSenderSubscription(streamSSRCs[6:9], nil, false)...)
	out = append(out, wasmSenderSubscription(streamSSRCs[0:3], pids, false)...)
	return append(out, wasmSenderSubscription([]uint32{appDataSSRC}, pids, false)...)
}

// WASMGroupReceiverSubscriptions selects the connected remote PIDs.
func WASMGroupReceiverSubscriptions(participantPIDs []uint32) []byte {
	var out []byte
	for _, pid := range normalizedPIDs(participantPIDs) {
		var p []byte
		p = pbTag(p, 1, 0)
		p = kdf.AppendVarint(p, uint64(pid))
		out = pbLenDelim(out, 2, p)
	}
	return out
}

func wasmSenderSubscription(ssrcs, pids []uint32, video bool) []byte {
	var packed []byte
	for _, s := range ssrcs {
		if s != 0 {
			packed = kdf.AppendVarint(packed, uint64(s))
		}
	}
	sub := pbLenDelim(nil, 1, packed)
	for _, pid := range pids {
		var p []byte
		p = pbTag(p, 1, 0)
		p = kdf.AppendVarint(p, uint64(pid))
		if video {
			p = pbTag(p, 2, 0)
			p = kdf.AppendVarint(p, 1)
		}
		sub = pbLenDelim(sub, 2, p)
	}
	return pbLenDelim(nil, 1, pbLenDelim(nil, 1, sub))
}

func normalizedPIDs(pids []uint32) []uint32 {
	out := slices.Clone(pids)
	slices.Sort(out)
	return slices.Compact(out)
}

// WASMGroupAllocate is the input of a group relay Allocate.
type WASMGroupAllocate struct {
	TransactionID   *[12]byte
	RelayToken      []byte
	EndpointXOR     *[6]byte
	IntegrityKey    []byte
	StreamSSRCs     *[9]uint32
	AppDataSSRC     uint32
	HBHFECSSRCs     *[2]uint32
	ParticipantPIDs []uint32
}

// BuildWASMGroupAllocate builds the group relay Allocate, including the
// multi-PID hop-by-hop FEC descriptors.
func BuildWASMGroupAllocate(r *WASMGroupAllocate) []byte {
	pids := normalizedPIDs(r.ParticipantPIDs)
	fec := &[2]uint32{}
	if len(pids) > 1 {
		fec = r.HBHFECSSRCs
	}
	descriptors := WASMStreamDescriptorsFromSSRCs(r.StreamSSRCs, fec)
	attrs := appendAttr(nil, attrRelayToken, r.RelayToken)
	if len(pids) > 0 {
		attrs = appendAttr(attrs, attrSenderSubscriptionsV2, WASMGroupSenderSubscriptions(r.StreamSSRCs, r.AppDataSSRC, pids))
		attrs = appendAttr(attrs, attrReceiverSubscriptions, WASMGroupReceiverSubscriptions(pids))
	}
	attrs = appendAttr(attrs, attrStreamDescriptors, descriptors)
	if len(pids) > 0 {
		attrs = appendAttr(attrs, attrParticipantCount, kdf.AppendVarint(nil, uint64(len(pids))))
	}
	attrs = appendAttr(attrs, attrWASMRelayEndpoint, wasmRelayEndpointAttr(r.EndpointXOR))
	return EncodeRequest(MsgAllocateRequest, r.TransactionID, attrs, r.IntegrityKey, false)
}

// BuildWASMAllocate is the WASM/Web DataChannel Allocate: 0x4000 token,
// 0x4024 stream descriptors with this call's derived SSRCs, 0x0016 endpoint,
// MESSAGE-INTEGRITY, no FINGERPRINT.
func BuildWASMAllocate(txid *[12]byte, relayToken []byte, endpointXOR *[6]byte, integrityKey []byte, callID, selfParticipantID string) []byte {
	attrs := appendAttr(nil, attrRelayToken, relayToken)
	attrs = appendAttr(attrs, attrStreamDescriptors, WASMStreamDescriptors(callID, selfParticipantID))
	attrs = appendAttr(attrs, attrWASMRelayEndpoint, wasmRelayEndpointAttr(endpointXOR))
	return EncodeRequest(MsgAllocateRequest, txid, attrs, integrityKey, false)
}

// WhatsAppPing is the consent ping (type 0x0801, empty body).
func WhatsAppPing(txid *[12]byte) [20]byte {
	var b [20]byte
	binary.BigEndian.PutUint16(b[:], MsgWhatsAppPing)
	binary.BigEndian.PutUint32(b[4:], magic)
	copy(b[8:], txid[:])
	return b
}

// IsSTUN reports a STUN-prefixed packet (top two bits zero).
func IsSTUN(data []byte) bool { return len(data) >= 2 && data[0]&0xc0 == 0 }

// MessageType is the 14-bit message type.
func MessageType(data []byte) (uint16, bool) {
	if len(data) < 2 {
		return 0, false
	}
	return uint16(data[0]&0x3f)<<8 | uint16(data[1]), true
}

// TransactionID is bytes 8..20.
func TransactionID(data []byte) ([]byte, bool) {
	if len(data) < 20 {
		return nil, false
	}
	return data[8:20], true
}

// isComplete: STUN-prefixed, a 20-byte header with the magic cookie, a
// 4-aligned body length, and the whole declared body present.
func isComplete(data []byte) bool {
	if !IsSTUN(data) || len(data) < 20 || binary.BigEndian.Uint32(data[4:]) != magic {
		return false
	}
	n := int(binary.BigEndian.Uint16(data[2:]))
	return n%4 == 0 && len(data) >= 20+n
}

// IsAllocateOrBindingSuccess reports a complete Allocate or Binding success.
func IsAllocateOrBindingSuccess(data []byte) bool {
	t, _ := MessageType(data)
	return isComplete(data) && (t == MsgAllocateSuccess || t == MsgBindingSuccess)
}

// IsAllocateError reports a complete Allocate error.
func IsAllocateError(data []byte) bool {
	t, _ := MessageType(data)
	return isComplete(data) && t == MsgAllocateError
}

// IsWhatsAppPong reports a pong, matching txid when it is non-empty.
func IsWhatsAppPong(data []byte, txid []byte) bool {
	if t, ok := MessageType(data); !IsSTUN(data) || !ok || t != MsgWhatsAppPong {
		return false
	}
	if len(txid) == 0 {
		return true
	}
	got, ok := TransactionID(data)
	return ok && string(got) == string(txid)
}

// Attribute is one parsed attribute; Value aliases the packet.
type Attribute struct {
	Type  uint16
	Value []byte
}

// ParseAttributes parses the attributes after the 20-byte header.
func ParseAttributes(data []byte) []Attribute {
	if !IsSTUN(data) || len(data) < 20 {
		return nil
	}
	var out []Attribute
	off := 20
	for off+4 <= len(data) {
		typ := binary.BigEndian.Uint16(data[off:])
		n := int(binary.BigEndian.Uint16(data[off+2:]))
		off += 4
		if off+n > len(data) {
			break
		}
		out = append(out, Attribute{Type: typ, Value: data[off : off+n]})
		off += n + pad4(n)
	}
	return out
}

// ParseErrorCode is an Allocate/Binding error's code (class*100 + number),
// read within the declared body only.
func ParseErrorCode(data []byte) (uint16, bool) {
	if !isComplete(data) {
		return 0, false
	}
	t, _ := MessageType(data)
	if t != MsgAllocateError && t != MsgBindingError {
		return 0, false
	}
	end := min(20+int(binary.BigEndian.Uint16(data[2:])), len(data))
	for off := 20; off+4 <= end; {
		typ := binary.BigEndian.Uint16(data[off:])
		n := int(binary.BigEndian.Uint16(data[off+2:]))
		if typ == attrErrorCode && n >= 4 && off+8 <= end {
			return uint16(data[off+6])*100 + uint16(data[off+7]), true
		}
		off += 4 + n + pad4(n)
	}
	return 0, false
}

// VoIPSenderSubscriptions is voip.SenderSubscriptions (WASM): one audio
// sender, ssrc as uint32.
func VoIPSenderSubscriptions(ssrc uint32) []byte {
	var s []byte
	s = pbTag(s, 3, 0)
	s = kdf.AppendVarint(s, uint64(ssrc))
	s = pbTag(s, 5, 0)
	s = kdf.AppendVarint(s, 0)
	s = pbTag(s, 6, 0)
	s = kdf.AppendVarint(s, 0)
	return pbLenDelim(nil, 1, s)
}

// APKSenderSubscriptions is wa.voip.SenderSubscriptions (Android, 0x4025):
// one audio ssrc as sint64, and the optional pid with layer "audio".
func APKSenderSubscriptions(ssrc uint32, pid *uint32) []byte {
	var layers []byte
	layers = pbTag(layers, 1, 0)
	layers = kdf.AppendVarint(layers, pbZigzag(int64(ssrc)))
	if pid != nil {
		var p []byte
		p = pbTag(p, 1, 0)
		p = kdf.AppendVarint(p, pbZigzag(int64(*pid)))
		p = pbLenDelim(p, 2, []byte("audio"))
		layers = pbLenDelim(layers, 2, p)
	}
	return pbLenDelim(nil, 1, pbLenDelim(nil, 1, layers))
}

// APKStreamDescriptors is wa.voip.StreamDescriptors (Android, 0x4024): one
// audio/OPUS descriptor.
func APKStreamDescriptors(ssrc uint32) []byte {
	var d []byte
	d = pbLenDelim(d, 1, []byte("audio"))
	d = pbLenDelim(d, 2, []byte("OPUS"))
	d = pbTag(d, 3, 0)
	d = kdf.AppendVarint(d, pbZigzag(int64(ssrc)))
	d = pbTag(d, 4, 0)
	d = kdf.AppendVarint(d, 0)
	return pbLenDelim(nil, 1, d)
}

// BuildAndroidAllocate is the Android Allocate: 0x4000 token, 0x4025 sender
// subscriptions, 0x4024 stream descriptors, MESSAGE-INTEGRITY and the
// optional FINGERPRINT.
func BuildAndroidAllocate(txid *[12]byte, relayToken []byte, ssrc uint32, pid *uint32, integrityKey []byte, fingerprint bool) []byte {
	attrs := appendAttr(nil, attrRelayToken, relayToken)
	attrs = appendAttr(attrs, attrSenderSubscriptionsV2, APKSenderSubscriptions(ssrc, pid))
	attrs = appendAttr(attrs, attrStreamDescriptors, APKStreamDescriptors(ssrc))
	return EncodeRequest(MsgAllocateRequest, txid, attrs, integrityKey, fingerprint)
}
