// Ported from whatsapp-rust 5e72baf wacore/src/voip/e2e_srtp.rs and the
// protect/unprotect steps of wacore/src/voip/session.rs, MIT.

// Package srtp is WhatsApp's media encryption with Go's crypto: end-to-end
// 1:1 SRTP (keys from the 32-byte callKey and a participant LID through
// HKDF-SHA256 and the libsrtp AES-CM PRF, AES-128-CTR payloads, a 4-byte
// WARP MESSAGE-INTEGRITY tag instead of the RFC 3711 auth tag), SRTCP
// (RFC 3711 3.4, HMAC-SHA1-80), and hop-by-hop SRTP/SRTCP keyed from the
// relay's hbh_key through WhatsApp's two-stage SFU HKDF and libsrtp's
// AES-ICM with its two-byte counter carry.
//
// pion/srtp does not fit: it has no WARP MI tag, no WhatsApp HKDF and no
// libsrtp ICM carry, and authenticates with the 10-byte tag on RTP.
//
// The receive path's only authentication is the MI tag, so round trips pass
// even when both directions are wrong identically; the known-answer and
// golden vectors are the real guard.
package srtp

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/hmac"
	"crypto/sha1"
	"crypto/subtle"
	"encoding/binary"
	"errors"

	"zapfast/gocore/voip/kdf"
	"zapfast/gocore/voip/rtp"
	"zapfast/gocore/voip/warp"
)

// Keys is one direction's session keys.
type Keys struct {
	CipherKey [16]byte
	Salt      [14]byte
	AuthKey   [20]byte
}

// String redacts the keys.
func (Keys) String() string { return "srtp.Keys([redacted])" }

// GoString redacts the keys.
func (Keys) GoString() string { return "srtp.Keys([redacted])" }

func ctrStream(key, iv []byte) cipher.Stream {
	block, err := aes.NewCipher(key)
	if err != nil {
		panic("srtp: " + err.Error())
	}
	return cipher.NewCTR(block, iv)
}

// aesCMKDF is the libsrtp AES-CM PRF: IV = master salt with label XORed into
// byte 7, zero-padded to 16, AES-128-CTR keystream over len zero bytes.
func aesCMKDF(masterKey, masterSalt []byte, label byte, out []byte) {
	var iv [16]byte
	copy(iv[:14], masterSalt[:14])
	iv[7] ^= label
	clear(out)
	ctrStream(masterKey, iv[:]).XORKeyStream(out, out)
}

func keysFromMaster(master []byte, base byte) Keys {
	var k Keys
	aesCMKDF(master[:16], master[16:30], base, k.CipherKey[:])
	aesCMKDF(master[:16], master[16:30], base+1, k.AuthKey[:])
	aesCMKDF(master[:16], master[16:30], base+2, k.Salt[:])
	return k
}

func e2eMaster(callKey []byte, participantLID string) ([]byte, bool) {
	if len(callKey) < 32 {
		return nil, false
	}
	return kdf.HKDFSHA256(make([]byte, 32), callKey[:32], []byte(participantLID), 46), true
}

// DeriveE2EKeys derives the 1:1 SRTP keys from callKey (at least 32 bytes)
// and a participant LID, the sender's own: a caller derives its send keys
// from its self LID and its receive keys from the peer LID. False for a
// short callKey.
func DeriveE2EKeys(callKey []byte, participantLID string) (Keys, bool) {
	master, ok := e2eMaster(callKey, participantLID)
	if !ok {
		return Keys{}, false
	}
	return keysFromMaster(master, 0x00), true
}

// DeriveE2EKeysFromRaw derives SRTP keys from one decrypted keygen-v2 group
// epoch.
func DeriveE2EKeysFromRaw(rawEpoch []byte, participantLID string) (Keys, bool) {
	return DeriveE2EKeys(rawEpoch, participantLID)
}

// DeriveSRTCPKeys derives the SRTCP keys with the RFC 3711 RTCP labels.
func DeriveSRTCPKeys(callKey []byte, participantLID string) (Keys, bool) {
	master, ok := e2eMaster(callKey, participantLID)
	if !ok {
		return Keys{}, false
	}
	return keysFromMaster(master, 0x03), true
}

// DeriveSRTCPKeysFromRaw is the SRTCP sibling of DeriveE2EKeysFromRaw.
func DeriveSRTCPKeysFromRaw(rawEpoch []byte, participantLID string) (Keys, bool) {
	return DeriveSRTCPKeys(rawEpoch, participantLID)
}

// E2ERTPIV is the E2E RTP IV: the salt right-aligned into bytes 0..14, the
// SSRC XORed at bytes 4-7, and the 48-bit packet index (roc<<16 | seq) XORed
// at bytes 8-13. An oversized salt is clamped to 14 bytes.
func E2ERTPIV(salt []byte, ssrc, roc uint32, seq uint16) [16]byte {
	var iv [16]byte
	n := min(len(salt), 14)
	copy(iv[14-n:14], salt[:n])
	iv[4] ^= byte(ssrc >> 24)
	iv[5] ^= byte(ssrc >> 16)
	iv[6] ^= byte(ssrc >> 8)
	iv[7] ^= byte(ssrc)
	index := uint64(roc)*0x1_0000 + uint64(seq)
	hi := uint16(index >> 32)
	lo := uint32(index)
	iv[8] ^= byte(hi >> 8)
	iv[9] ^= byte(hi)
	iv[10] ^= byte(lo >> 24)
	iv[11] ^= byte(lo >> 16)
	iv[12] ^= byte(lo >> 8)
	iv[13] ^= byte(lo)
	return iv
}

// CryptPayload AES-128-CTR encrypts or decrypts buf in place.
func CryptPayload(k *Keys, ssrc uint32, seq uint16, roc uint32, buf []byte) {
	iv := E2ERTPIV(k.Salt[:], ssrc, roc, seq)
	ctrStream(k.CipherKey[:], iv[:]).XORKeyStream(buf, buf)
}

// SRTCPAuthTagLen is HMAC-SHA1-80's tag length; with the E-flag/index word it
// makes the 14-byte SRTCP trailer.
const SRTCPAuthTagLen = 10

const rtcpHeaderLen = 8

func hmacSHA1(key, data []byte) []byte {
	m := hmac.New(sha1.New, key)
	m.Write(data)
	return m.Sum(nil)
}

// ProtectSRTCP protects one RTCP packet: AES-CTR over the body (the first 8
// bytes stay clear), the 4-byte E-flag+index word, then the HMAC-SHA1-80 tag
// over everything.
func ProtectSRTCP(k *Keys, senderSSRC, index uint32, rtcp []byte) []byte {
	split := min(len(rtcp), rtcpHeaderLen)
	iv := E2ERTPIV(k.Salt[:], senderSSRC, index>>16, uint16(index))
	out := make([]byte, len(rtcp), len(rtcp)+4+SRTCPAuthTagLen)
	copy(out, rtcp)
	ctrStream(k.CipherKey[:], iv[:]).XORKeyStream(out[split:], out[split:])
	out = binary.BigEndian.AppendUint32(out, 0x8000_0000|index&0x7fff_ffff)
	return append(out, hmacSHA1(k.AuthKey[:], out)[:SRTCPAuthTagLen]...)
}

// UnprotectSRTCP verifies the tag, decrypts the body and returns it with its
// authenticated 31-bit index. False on a bad tag or a short packet.
func UnprotectSRTCP(k *Keys, senderSSRC uint32, packet []byte) ([]byte, uint32, bool) {
	if len(packet) < rtcpHeaderLen+4+SRTCPAuthTagLen {
		return nil, 0, false
	}
	tagStart := len(packet) - SRTCPAuthTagLen
	expected := hmacSHA1(k.AuthKey[:], packet[:tagStart])
	if subtle.ConstantTimeCompare(packet[tagStart:], expected[:SRTCPAuthTagLen]) != 1 {
		return nil, 0, false
	}
	idxStart := tagStart - 4
	index := binary.BigEndian.Uint32(packet[idxStart:]) & 0x7fff_ffff
	iv := E2ERTPIV(k.Salt[:], senderSSRC, index>>16, uint16(index))
	out := append([]byte(nil), packet[:idxStart]...)
	ctrStream(k.CipherKey[:], iv[:]).XORKeyStream(out[rtcpHeaderLen:], out[rtcpHeaderLen:])
	return out, index, true
}

// SendROC is the send-side rollover counter for monotonic sequence numbers.
type SendROC struct {
	roc         uint32
	lastSeq     uint16
	initialized bool
}

// Advance returns the ROC for seq, bumping it on a wrap (a signed gap below
// -32768).
func (r *SendROC) Advance(seq uint16) uint32 {
	if !r.initialized {
		r.lastSeq, r.initialized = seq, true
		return r.roc
	}
	if int32(seq)-int32(r.lastSeq) < -32768 {
		r.roc++
	}
	r.lastSeq = seq
	return r.roc
}

// RecvROC is the receive-side RFC 3711 3.3.1 guess-index estimator. It
// tolerates reorder and loss: a late packet straddling a wrap decrypts under
// the lower ROC without poisoning the state.
type RecvROC struct {
	roc         uint32
	sl          uint16
	initialized bool
}

// Estimate guesses the ROC for seq without changing state.
func (r *RecvROC) Estimate(seq uint16) uint32 {
	if !r.initialized {
		return r.roc
	}
	if r.sl < 0x8000 {
		if int32(seq)-int32(r.sl) > 0x8000 {
			return r.roc - 1
		}
		return r.roc
	}
	if int32(r.sl)-int32(seq) > 0x8000 {
		return r.roc + 1
	}
	return r.roc
}

// Commit folds an authenticated packet's (v, seq) into the state; v must come
// from an Estimate whose tag verified.
func (r *RecvROC) Commit(v uint32, seq uint16) {
	if !r.initialized {
		r.sl, r.initialized = seq, true
		return
	}
	switch v {
	case r.roc:
		if seq > r.sl {
			r.sl = seq
		}
	case r.roc + 1:
		r.roc, r.sl = v, seq
	}
}

// ProtectRTP builds one protected packet: the RTP header, the AES-CTR
// ciphertext of payload, and the WARP MI tag over both.
func ProtectRTP(k *Keys, h rtp.Header, roc uint32, tagLen int, payload []byte) []byte {
	packet := h.AppendTo(make([]byte, 0, h.ByteSize()+len(payload)+tagLen))
	start := len(packet)
	packet = append(packet, payload...)
	CryptPayload(k, h.SSRC, h.SequenceNumber, roc, packet[start:])
	return warp.AppendMITag(k.AuthKey[:], packet, roc, tagLen)
}

// Receive errors.
var (
	ErrInvalidPacket        = errors.New("srtp: invalid packet")
	ErrAuthenticationFailed = errors.New("srtp: authentication failed")
	// ErrRejected: the replay window or the stream cap refused an
	// authenticated packet.
	ErrRejected = errors.New("srtp: packet rejected")
)

const (
	srtpReplayWindowBits = 64
	srtpReplayStreamCap  = 16
)

type replayWindow struct {
	highest uint64
	have    bool
	seen    uint64
}

func (w *replayWindow) accept(index uint64) bool {
	if !w.have {
		w.highest, w.have, w.seen = index, true, 1
		return true
	}
	if index > w.highest {
		forward := index - w.highest
		if forward >= srtpReplayWindowBits {
			w.seen = 1
		} else {
			w.seen = w.seen<<forward | 1
		}
		w.highest = index
		return true
	}
	behind := w.highest - index
	if behind >= srtpReplayWindowBits {
		return false
	}
	bit := uint64(1) << behind
	if w.seen&bit != 0 {
		return false
	}
	w.seen |= bit
	return true
}

type recvStream struct {
	ssrc   uint32
	roc    RecvROC
	replay replayWindow
}

// RecvStreams is the per-SSRC receive state (rollover counter and replay
// window) of one pipeline. The zero value is ready. State for an SSRC is
// created only after a packet of it authenticates, so forged SSRCs cannot
// fill the table; at most 16 streams are tracked and further ones are
// refused rather than evicting a live one.
type RecvStreams struct {
	streams []recvStream
}

func (r *RecvStreams) find(ssrc uint32) *recvStream {
	for i := range r.streams {
		if r.streams[i].ssrc == ssrc {
			return &r.streams[i]
		}
	}
	return nil
}

// EstimateROC is the ROC to authenticate seq against, without allocating.
func (r *RecvStreams) EstimateROC(ssrc uint32, seq uint16) uint32 {
	if s := r.find(ssrc); s != nil {
		return s.roc.Estimate(seq)
	}
	var fresh RecvROC
	return fresh.Estimate(seq)
}

// Accept records an authenticated packet's (ssrc, seq) at roc: false for a
// replay or once the stream cap is reached.
func (r *RecvStreams) Accept(ssrc uint32, seq uint16, roc uint32) bool {
	s := r.find(ssrc)
	if s == nil {
		if len(r.streams) >= srtpReplayStreamCap {
			return false
		}
		r.streams = append(r.streams, recvStream{ssrc: ssrc})
		s = &r.streams[len(r.streams)-1]
	}
	if !s.replay.accept(uint64(roc)<<16 | uint64(seq)) {
		return false
	}
	s.roc.Commit(roc, seq)
	return true
}

// Unprotect verifies the WARP MI tag against the estimated ROC before
// committing anything, then decrypts. The order is load-bearing: an on-path
// relay must not fold unauthenticated packets into the rollover counter.
func (r *RecvStreams) Unprotect(k *Keys, tagLen int, packet []byte) (rtp.Header, []byte, error) {
	if len(packet) < rtp.FixedHeaderLen+tagLen {
		return rtp.Header{}, nil, ErrInvalidPacket
	}
	split := len(packet) - tagLen
	body, tag := packet[:split], packet[split:]
	h, ok := rtp.ParseHeader(body)
	if !ok {
		return rtp.Header{}, nil, ErrInvalidPacket
	}
	n, ok := rtp.HeaderByteLength(body)
	if !ok || len(body) <= n {
		return rtp.Header{}, nil, ErrInvalidPacket
	}
	roc := r.EstimateROC(h.SSRC, h.SequenceNumber)
	if !warp.VerifyMITag(k.AuthKey[:], body, roc, tagLen, tag) {
		return rtp.Header{}, nil, ErrAuthenticationFailed
	}
	if !r.Accept(h.SSRC, h.SequenceNumber, roc) {
		return rtp.Header{}, nil, ErrRejected
	}
	plain := append([]byte(nil), body[n:]...)
	CryptPayload(k, h.SSRC, h.SequenceNumber, roc, plain)
	return h, plain, nil
}

const (
	srtcpIndexMask       = 0x7fff_ffff
	srtcpIndexHalfRange  = 1 << 30
	srtcpReplayWindowBit = 64
	srtcpReplayStreamCap = 16
)

type srtcpWindow struct {
	highest uint32
	have    bool
	seen    uint64
}

func (w *srtcpWindow) accept(index uint32) bool {
	index &= srtcpIndexMask
	if !w.have {
		w.highest, w.have, w.seen = index, true, 1
		return true
	}
	forward := (index - w.highest) & srtcpIndexMask
	if forward == 0 {
		return false
	}
	if forward < srtcpIndexHalfRange {
		if forward >= srtcpReplayWindowBit {
			w.seen = 1
		} else {
			w.seen = w.seen<<forward | 1
		}
		w.highest = index
		return true
	}
	behind := (w.highest - index) & srtcpIndexMask
	if behind >= srtcpReplayWindowBit {
		return false
	}
	bit := uint64(1) << behind
	if w.seen&bit != 0 {
		return false
	}
	w.seen |= bit
	return true
}

// SRTCPReplay is the per-sender-SSRC SRTCP replay state (31-bit index,
// 64-packet window, at most 16 senders). The zero value is ready.
type SRTCPReplay struct {
	ssrcs   []uint32
	windows []srtcpWindow
}

// Accept reports whether an authenticated SRTCP index is new.
func (r *SRTCPReplay) Accept(senderSSRC, index uint32) bool {
	for i, s := range r.ssrcs {
		if s == senderSSRC {
			return r.windows[i].accept(index)
		}
	}
	if len(r.ssrcs) >= srtcpReplayStreamCap {
		return false
	}
	var w srtcpWindow
	ok := w.accept(index)
	r.ssrcs = append(r.ssrcs, senderSSRC)
	r.windows = append(r.windows, w)
	return ok
}
