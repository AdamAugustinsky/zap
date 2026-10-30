// Ported from whatsapp-rust 5e72baf wacore/src/voip/sframe/implementation.rs (tests), MIT.

package sframe

import (
	"bytes"
	"encoding/hex"
	"math"
	"testing"

	"zapfast/gocore/voip/internal/kat"
)

func TestParticipantKeyAndLabelMatchKAT(t *testing.T) {
	k := kat.KATs(t)
	peerID := FormatParticipantID(k.Str(t, "inputs", "peerLid"))
	if peerID != k.Str(t, "sframe", "participantPeerId") || InfoLabel(peerID) != k.Str(t, "sframe", "infoLabelPeer") {
		t.Fatal("ids")
	}
	key, ok := DeriveKey(k.Hex(t, "inputs", "callKey"), peerID)
	if !ok || hex.EncodeToString(key) != k.Str(t, "sframe", "peerKey32") {
		t.Fatal("peer key")
	}
	selfKey, _ := DeriveKey(k.Hex(t, "inputs", "callKey"), FormatParticipantID(k.Str(t, "inputs", "selfLid")))
	if hex.EncodeToString(selfKey) != k.Str(t, "sframe", "selfKey32") {
		t.Fatal("self key")
	}
}

func TestCounterIVAndHeaderMatchKAT(t *testing.T) {
	k := kat.KATs(t)
	iv := counterToIV(5)
	if hex.EncodeToString(iv[:]) != k.Str(t, "sframe", "counterToIv_5") {
		t.Fatal("iv")
	}
	h := appendHeader(nil, 5, 0)
	if hex.EncodeToString(h) != k.Str(t, "sframe", "header_5_0") {
		t.Fatal("header")
	}
	if c, id, ok := parseHeader(h); !ok || c != 5 || id != 0 {
		t.Fatal("round trip")
	}
}

func TestMaxVarintHeaderFitsTheBoundAndRoundTrips(t *testing.T) {
	h := appendHeader(nil, math.MaxUint64, math.MaxUint64)
	if len(h) != headerMax || int(h[len(h)-1]) != headerMax {
		t.Fatal(len(h))
	}
	if c, id, ok := parseHeader(h); !ok || c != math.MaxUint64 || id != math.MaxUint64 {
		t.Fatal("maximal header")
	}
}

func session(t *testing.T, selfFirst bool, mutate func([]byte)) *Session {
	k := kat.KATs(t)
	callKey := k.Hex(t, "inputs", "callKey")
	if mutate != nil {
		mutate(callKey)
	}
	self, peer := k.Str(t, "inputs", "selfLid"), k.Str(t, "inputs", "peerLid")
	if !selfFirst {
		self, peer = peer, self
	}
	s, ok := NewSession(callKey, self, peer)
	if !ok {
		t.Fatal("session")
	}
	return s
}

func TestEncryptMatchesKAT(t *testing.T) {
	k := kat.KATs(t)
	s := session(t, true, nil)
	s.SetCounter(k.Uint(t, "inputs", "sframeCounter"))
	if got := hex.EncodeToString(s.Encrypt(k.Hex(t, "inputs", "payload"))); got != k.Str(t, "sframe", "encrypt_out") {
		t.Fatal(got)
	}
}

func TestEncryptDecryptRoundTrip(t *testing.T) {
	frame := session(t, true, nil).Encrypt([]byte("hello sframe payload"))
	if plain, r := session(t, false, nil).Decrypt(frame); r != Decrypted || string(plain) != "hello sframe payload" {
		t.Fatal(r)
	}
}

func TestWrongKeyDoesNotForgePlaintext(t *testing.T) {
	frame := session(t, true, nil).Encrypt(bytes.Repeat([]byte{0xaa}, 24))
	if _, r := session(t, false, func(b []byte) { b[0] ^= 0xff }).Decrypt(frame); r != AuthFailed {
		t.Fatal(r)
	}
}

func TestPlainOpusPassesThroughUnchanged(t *testing.T) {
	r := session(t, true, nil)
	for _, h := range []string{"00", "90b81414c4", "12101a759d3399bbaefb874fd75a004af7c0", "9036ba6ffa40", "1236262b4ac920b1206166637b5af2"} {
		f, _ := hex.DecodeString(h)
		if _, res := r.Decrypt(f); res != Plaintext {
			t.Fatalf("%s: %v", h, res)
		}
	}
}

func TestDecodeVarintRejectsShiftOverflow(t *testing.T) {
	if _, _, ok := decodeVarint(bytes.Repeat([]byte{0xff}, 10), 0); ok {
		t.Fatal("overflow accepted")
	}
}

func TestRecvPathNeverPanicsOnTruncationOrGarbage(t *testing.T) {
	recv := session(t, false, nil)
	frame := session(t, true, nil).Encrypt([]byte("truncation fuzz payload"))
	for n := 0; n <= len(frame); n++ {
		recv.Decrypt(frame[:n])
		parseHeader(frame[:n])
	}
	for _, j := range [][]byte{{}, {0}, {0xff}, bytes.Repeat([]byte{0xff}, 16), {0x02, 0xff}, make([]byte, 64), bytes.Repeat([]byte{0xff}, 64)} {
		recv.Decrypt(j)
		parseHeader(j)
	}
}

func TestNewSessionRequires32ByteKey(t *testing.T) {
	if _, ok := NewSession(make([]byte, 40), "a@lid", "b@lid"); ok {
		t.Fatal("40-byte key accepted")
	}
}
