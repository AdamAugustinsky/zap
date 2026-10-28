// Ported from whatsapp-rust 5e72baf wacore/src/voip/sframe.rs and
// wacore/src/voip/sframe/implementation.rs, MIT.

// Package sframe is WhatsApp's SFrame end-to-end media encryption: AES-128-GCM
// over a frame, with three byte-level traps pinned by the known-answer
// vectors: the GCM nonce is 16 bytes (8 zero bytes then the counter as a
// little-endian uint64, so J0 is GHASH-derived); the SFrame header follows the
// ciphertext and tag and is not AAD; the wire layout is
// [ciphertext || 16-byte tag || varint header].
package sframe

import (
	"crypto/aes"
	"crypto/cipher"
	"encoding/binary"

	"zapfast/gocore/voip/kdf"
)

// KDFLabel prefixes the participant id in the HKDF info.
const KDFLabel = "e2e sframe key"

const (
	gcmTagLen = 16
	keyLen    = 16
	// headerMax is two maximal uint64 varints plus the length byte.
	headerMax = 2*10 + 1
)

// FormatParticipantID is the participant id SFrame derives keys for (the
// same rule as E2E SRTP's today).
func FormatParticipantID(jid string) string { return kdf.FormatParticipantID(jid) }

// InfoLabel is the HKDF info for participantID: "e2e sframe key<id>".
func InfoLabel(participantID string) string { return KDFLabel + participantID }

// DeriveKey is a participant's 32-byte SFrame key: HKDF-SHA256 with the first
// half of the 32-byte callKey as salt and the second as IKM. False unless
// callKey is exactly 32 bytes.
func DeriveKey(callKey []byte, participantID string) ([]byte, bool) {
	if len(callKey) != 32 {
		return nil, false
	}
	return kdf.HKDFSHA256(callKey[:16], callKey[16:32], []byte(InfoLabel(participantID)), 32), true
}

func decodeVarint(data []byte, off int) (uint64, int, bool) {
	var v uint64
	var shift uint
	for off < len(data) {
		b := data[off]
		off++
		v |= uint64(b&0x7f) << shift
		if b&0x80 == 0 {
			return v, off, true
		}
		shift += 7
		if shift > 63 {
			return 0, 0, false
		}
	}
	return 0, 0, false
}

func counterToIV(counter uint64) [16]byte {
	var iv [16]byte
	binary.LittleEndian.PutUint64(iv[8:], counter)
	return iv
}

func appendHeader(out []byte, counter, keyID uint64) []byte {
	start := len(out)
	out = kdf.AppendVarint(out, counter)
	out = kdf.AppendVarint(out, keyID)
	return append(out, byte(len(out)-start+1))
}

func parseHeader(h []byte) (counter, keyID uint64, ok bool) {
	if len(h) < 2 || int(h[len(h)-1]) != len(h) {
		return 0, 0, false
	}
	body := h[:len(h)-1]
	counter, next, ok := decodeVarint(body, 0)
	if !ok {
		return 0, 0, false
	}
	keyID, _, ok = decodeVarint(body, next)
	return counter, keyID, ok
}

func newGCM(key []byte) cipher.AEAD {
	block, err := aes.NewCipher(key[:keyLen])
	if err != nil {
		panic("sframe: " + err.Error())
	}
	aead, err := cipher.NewGCMWithNonceSize(block, 16)
	if err != nil {
		panic("sframe: " + err.Error())
	}
	return aead
}

// Result is what Decrypt made of a frame.
type Result int

const (
	// Plaintext: no SFrame framing (too short, no readable header); the
	// frame bytes are the payload. A peer that does not wrap is healthy.
	Plaintext Result = iota
	// Decrypted: the header parsed and GCM authenticated.
	Decrypted
	// AuthFailed: a well-formed header whose tag did not authenticate. The
	// bytes are still the payload, but this is worth counting.
	AuthFailed
)

// Session is one call's SFrame state: encrypt with the peer's key, decrypt
// with our own (the opposite of E2E SRTP's convention).
type Session struct {
	encrypt   cipher.AEAD
	decrypt   cipher.AEAD
	txCounter uint64
	// SelfID and PeerID are the formatted participant ids.
	SelfID, PeerID string
}

// NewSession derives both keys from callKey and the self/peer JIDs. False
// unless callKey is 32 bytes.
func NewSession(callKey []byte, selfJID, peerJID string) (*Session, bool) {
	selfID, peerID := FormatParticipantID(selfJID), FormatParticipantID(peerJID)
	send, ok := DeriveKey(callKey, peerID)
	if !ok {
		return nil, false
	}
	recv, _ := DeriveKey(callKey, selfID)
	return &Session{encrypt: newGCM(send), decrypt: newGCM(recv), SelfID: selfID, PeerID: peerID}, true
}

// SetCounter sets the next transmit counter.
func (s *Session) SetCounter(c uint64) { s.txCounter = c }

// Encrypt encrypts one frame to [ciphertext || tag || header].
func (s *Session) Encrypt(plaintext []byte) []byte {
	counter := s.txCounter
	s.txCounter++
	iv := counterToIV(counter)
	out := make([]byte, 0, len(plaintext)+gcmTagLen+headerMax)
	out = s.encrypt.Seal(out, iv[:], plaintext, nil)
	return appendHeader(out, counter, 0)
}

// Decrypt decrypts one frame. Only a frame whose trailing header parses and
// whose GCM tag authenticates is Decrypted; otherwise the caller uses the
// frame bytes as they are.
func (s *Session) Decrypt(frame []byte) ([]byte, Result) {
	if len(frame) < gcmTagLen+3 {
		return nil, Plaintext
	}
	hlen := int(frame[len(frame)-1])
	if hlen < 3 || hlen > len(frame) {
		return nil, Plaintext
	}
	start := len(frame) - hlen
	ciphertext := frame[:start]
	if len(ciphertext) < gcmTagLen+1 {
		return nil, Plaintext
	}
	counter, _, ok := parseHeader(frame[start:])
	if !ok {
		return nil, Plaintext
	}
	iv := counterToIV(counter)
	plain, err := s.decrypt.Open(nil, iv[:], ciphertext, nil)
	if err != nil {
		return nil, AuthFailed
	}
	return plain, Decrypted
}
