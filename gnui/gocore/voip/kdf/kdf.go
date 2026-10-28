// Ported from whatsapp-rust 5e72baf wacore/src/voip_control/kdf.rs and
// wacore/src/voip_control/ssrc.rs, MIT.

// Package kdf holds the small pure helpers every WhatsApp VoIP key
// derivation and stream id reduces to: HKDF-SHA256, the device-qualified
// participant id used as HKDF info, the LEB128 varint of the SFrame header
// and the STUN protobuf attributes, and the per-participant SSRC derivation.
package kdf

import (
	"crypto/hkdf"
	"crypto/sha256"
	"encoding/binary"
	"strings"
)

// HKDFSHA256 is HKDF-SHA256 (extract with salt, expand with info) to n
// bytes. n must not exceed 255*32.
func HKDFSHA256(salt, ikm, info []byte, n int) []byte {
	out, err := hkdf.Key(sha256.New, ikm, salt, string(info), n)
	if err != nil {
		panic("kdf: HKDF length out of bounds: " + err.Error())
	}
	return out
}

// FormatParticipantID is the device-qualified participant id used as HKDF
// info by both E2E SRTP and SFrame: strip the resource, keep an existing
// ":N@lid" device suffix, give a bare "@lid" an implicit ":0", and pass
// everything else through unchanged.
func FormatParticipantID(jid string) string {
	bare := jid
	if i := strings.IndexByte(bare, '/'); i >= 0 {
		bare = bare[:i]
	}
	bare = strings.TrimSpace(bare)
	at := strings.LastIndexByte(bare, '@')
	if at <= 0 {
		return bare
	}
	user, domain := bare[:at], bare[at+1:]
	if domain == "lid" && !strings.Contains(user, ":") {
		return user + ":0@" + domain
	}
	return bare
}

// AppendVarint appends value as an LEB128 varint.
func AppendVarint(out []byte, value uint64) []byte {
	for value > 0x7f {
		out = append(out, byte(value&0x7f)|0x80)
		value >>= 7
	}
	return append(out, byte(value))
}

// Relay stream slot words of the WhatsApp WASM slot grid.
const (
	// VideoSSRCSlotWord derives the video SSRC.
	VideoSSRCSlotWord uint32 = 2
	// AppDataSSRCSlotWord derives the RTC app-data stream (reactions).
	AppDataSSRCSlotWord uint32 = 6
	// HBHFECTxSSRCSlotWord and HBHFECRxSSRCSlotWord are the hop-by-hop FEC
	// relay streams used once more than one remote PID is subscribed.
	HBHFECTxSSRCSlotWord uint32 = 7
	HBHFECRxSSRCSlotWord uint32 = 8
)

// WASMRelayStreamSlotWords is the relay stream order: audio
// media/FEC/NACK, video media/FEC/NACK, hop-by-hop FEC transmit/receive,
// app-data.
var WASMRelayStreamSlotWords = [9]uint32{0, 1, 4, 2, 3, 5, 7, 8, 6}

// ParticipantSSRC is a participant's stream SSRC:
// HKDF-SHA256(salt = slotWord as LE32, ikm = callID, info = lid, 4), read
// back as a little-endian uint32.
func ParticipantSSRC(callID, lid string, slotWord uint32) uint32 {
	var salt [4]byte
	binary.LittleEndian.PutUint32(salt[:], slotWord)
	return binary.LittleEndian.Uint32(HKDFSHA256(salt[:], []byte(callID), []byte(lid), 4))
}

// VideoParticipantSSRC is the participant's video SSRC.
func VideoParticipantSSRC(callID, lid string) uint32 {
	return ParticipantSSRC(callID, lid, VideoSSRCSlotWord)
}

// RelayStreamSSRCs derives the SSRC of every relay stream, in
// WASMRelayStreamSlotWords order.
func RelayStreamSSRCs(callID, lid string) [9]uint32 {
	var out [9]uint32
	for i, slot := range WASMRelayStreamSlotWords {
		out[i] = ParticipantSSRC(callID, lid, slot)
	}
	return out
}
