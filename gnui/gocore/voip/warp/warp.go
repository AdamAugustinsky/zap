// Ported from whatsapp-rust 5e72baf wacore/src/voip/warp.rs, MIT.

// Package warp holds WhatsApp's WARP RTP extension constants and the WARP
// MESSAGE-INTEGRITY tag that authenticates every SRTP packet.
//
// The MI tag is keyed by the per-participant SRTP auth key (KAT-pinned), not
// by a separate callKey-derived "warp auth key".
package warp

import (
	"crypto/hmac"
	"crypto/sha1"
	"crypto/subtle"
	"encoding/binary"
)

// AudioPiggybackExt is the audio piggyback extension word, 0x30010000.
const AudioPiggybackExt uint32 = 0x3001_0000

// MITagLen is the MI tag length every live call negotiates.
const MITagLen = 4

// MITagMaxLen is HMAC-SHA1's digest size, the largest possible MI tag.
const MITagMaxLen = 20

// PiggybackStartPacket: packets #1-2 carry an empty extension; #3+ (0-based
// index >= 2) piggyback.
const PiggybackStartPacket = 2

// AudioPiggybackExtensionFor is the audio piggyback extension word for
// packetIndex, or false for the first packets.
func AudioPiggybackExtensionFor(packetIndex int, enabled bool, startPacket int) (uint32, bool) {
	if !enabled || packetIndex < startPacket {
		return 0, false
	}
	return AudioPiggybackExt, true
}

// MITag is HMAC-SHA1(authKey, packet || roc_be32). The tag on the wire is its
// first tagLen bytes.
func MITag(authKey, packetWithoutTag []byte, roc uint32) [MITagMaxLen]byte {
	mac := hmac.New(sha1.New, authKey)
	mac.Write(packetWithoutTag)
	var r [4]byte
	binary.BigEndian.PutUint32(r[:], roc)
	mac.Write(r[:])
	var tag [MITagMaxLen]byte
	mac.Sum(tag[:0])
	return tag
}

// VerifyMITag checks a received tag in constant time. A length mismatch is a
// rejection. Callers must reject a packet whose tag fails before folding any
// receive ROC state (RFC 3711 3.3.1).
func VerifyMITag(authKey, packetWithoutTag []byte, roc uint32, tagLen int, received []byte) bool {
	if tagLen > MITagMaxLen || len(received) != tagLen {
		return false
	}
	expected := MITag(authKey, packetWithoutTag, roc)
	return subtle.ConstantTimeCompare(expected[:tagLen], received) == 1
}

// AppendMITag appends the tag over everything already in packet. tagLen is
// clamped to 1..20, as the Rust send path does.
func AppendMITag(authKey, packet []byte, roc uint32, tagLen int) []byte {
	tag := MITag(authKey, packet, roc)
	return append(packet, tag[:min(max(tagLen, 1), MITagMaxLen)]...)
}
