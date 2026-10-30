// Ported from whatsapp-rust 5e72baf wacore/src/voip/e2e_srtp.rs and
// wacore/src/voip/session.rs (tests), MIT.

package srtp

import (
	"bytes"
	"encoding/hex"
	"errors"
	"testing"

	"zapfast/gocore/voip/internal/kat"
	"zapfast/gocore/voip/kdf"
	"zapfast/gocore/voip/rtp"
	"zapfast/gocore/voip/warp"
)

func keysFrom(t *testing.T, k kat.Doc, who string) Keys {
	var keys Keys
	copy(keys.CipherKey[:], k.Hex(t, "e2e_srtp", who+"_cipherKey"))
	copy(keys.Salt[:], k.Hex(t, "e2e_srtp", who+"_salt"))
	copy(keys.AuthKey[:], k.Hex(t, "e2e_srtp", who+"_authKey"))
	return keys
}

func seqBytes(n int) []byte {
	b := make([]byte, n)
	for i := range b {
		b[i] = byte(i)
	}
	return b
}

func TestSRTCPRoundTripsAndAuthenticates(t *testing.T) {
	callKey := seqBytes(40)
	srtpKeys, _ := DeriveE2EKeys(callKey, "12345:0@lid")
	srtcp, _ := DeriveSRTCPKeys(callKey, "12345:0@lid")
	if srtpKeys.CipherKey == srtcp.CipherKey || srtpKeys.Salt == srtcp.Salt {
		t.Fatal("SRTCP must use its own keys")
	}
	ssrc := uint32(0x0a0b_0c0d)
	sr := append([]byte{0x80, 200, 0, 6, 0x0a, 0x0b, 0x0c, 0x0d}, bytes.Repeat([]byte{0x11}, 20)...)
	p := ProtectSRTCP(&srtcp, ssrc, 0, sr)
	if len(p) != len(sr)+4+SRTCPAuthTagLen || !bytes.Equal(p[:8], sr[:8]) || bytes.Equal(p[8:28], sr[8:28]) {
		t.Fatal("layout")
	}
	if p[len(p)-SRTCPAuthTagLen-4]&0x80 == 0 {
		t.Fatal("E flag")
	}
	plain, index, ok := UnprotectSRTCP(&srtcp, ssrc, p)
	if !ok || !bytes.Equal(plain, sr) || index != 0 {
		t.Fatal("unprotect")
	}
	forged := bytes.Clone(p)
	forged[len(forged)-1] ^= 1
	if _, _, ok := UnprotectSRTCP(&srtcp, ssrc, forged); ok {
		t.Fatal("forged accepted")
	}
	p1 := ProtectSRTCP(&srtcp, ssrc, 1, sr)
	if bytes.Equal(p[8:28], p1[8:28]) {
		t.Fatal("index must change the keystream")
	}
	if plain, index, ok := UnprotectSRTCP(&srtcp, ssrc, p1); !ok || !bytes.Equal(plain, sr) || index != 1 {
		t.Fatal("index 1")
	}
}

func TestDeriveE2EKeysMatchesKAT(t *testing.T) {
	k := kat.KATs(t)
	callKey := k.Hex(t, "inputs", "callKey")
	peer, ok := DeriveE2EKeys(callKey, k.Str(t, "inputs", "peerLid"))
	if !ok || peer != keysFrom(t, k, "peer") {
		t.Fatal("peer keys")
	}
	self, _ := DeriveE2EKeys(callKey, k.Str(t, "inputs", "selfLid"))
	want := keysFrom(t, k, "self")
	if self.CipherKey != want.CipherKey || self.AuthKey != want.AuthKey || self.Salt != want.Salt {
		t.Fatal("self keys")
	}
	if _, ok := DeriveE2EKeys(callKey[:31], "x"); ok {
		t.Fatal("short callKey accepted")
	}
}

func TestWAHKDFMatchesKAT(t *testing.T) {
	k := kat.KATs(t)
	okm := kdf.HKDFSHA256(make([]byte, 32), k.Hex(t, "inputs", "callKey"), []byte(k.Str(t, "inputs", "peerLid")), 46)
	if hex.EncodeToString(okm) != k.Str(t, "wahkdf", "okm46") ||
		hex.EncodeToString(okm[:16]) != k.Str(t, "wahkdf", "masterKey") ||
		hex.EncodeToString(okm[16:30]) != k.Str(t, "wahkdf", "masterSalt") {
		t.Fatalf("%x", okm)
	}
}

func TestRTPIVMatchesKAT(t *testing.T) {
	k := kat.KATs(t)
	peer := keysFrom(t, k, "peer")
	iv := E2ERTPIV(peer.Salt[:], uint32(k.Uint(t, "inputs", "ssrc")), uint32(k.Uint(t, "inputs", "roc")), uint16(k.Uint(t, "inputs", "seq")))
	if hex.EncodeToString(iv[:]) != k.Str(t, "e2e_srtp", "rtpIv") {
		t.Fatalf("%x", iv)
	}
}

func TestROCTrackerWraps(t *testing.T) {
	var tx SendROC
	steps := []struct {
		seq  uint16
		want uint32
	}{{0xfffe, 0}, {0xffff, 0}, {0, 1}, {1, 1}, {0, 1}, {1, 1}}
	for _, s := range steps {
		if got := tx.Advance(s.seq); got != s.want {
			t.Fatalf("send %#x: %d", s.seq, got)
		}
	}
	for _, s := range []uint16{0x7000, 0xe000, 0xffff} {
		tx.Advance(s)
	}
	if tx.Advance(0) != 2 {
		t.Fatal("second wrap")
	}

	var rx RecvROC
	guess := func(seq uint16) uint32 {
		v := rx.Estimate(seq)
		rx.Commit(v, seq)
		return v
	}
	for _, s := range []struct {
		seq  uint16
		want uint32
	}{{0xfffe, 0}, {0xffff, 0}, {0, 1}, {1, 1}, {0, 1}, {2, 1}, {0x7000, 1}, {0xe000, 1}, {0xffff, 1}, {0, 2}, {0xfff0, 1}, {1, 2}} {
		if got := guess(s.seq); got != s.want {
			t.Fatalf("recv %#x: %d want %d", s.seq, got, s.want)
		}
	}
}

func TestUnauthenticatedStaircaseCannotAdvanceROCWithoutCommit(t *testing.T) {
	var rx RecvROC
	rx.Commit(rx.Estimate(0x7ffe), 0x7ffe)
	rx.Estimate(0xfffe)
	rx.Estimate(0x7ffd)
	if rx.roc != 0 || rx.Estimate(0x7fff) != 0 {
		t.Fatal("estimate alone advanced the ROC")
	}
	rx.Commit(rx.Estimate(0xfffe), 0xfffe)
	rx.Commit(rx.Estimate(0x7ffd), 0x7ffd)
	if rx.roc != 1 {
		t.Fatal("committing the staircase advances the ROC")
	}
}

func TestCryptPayloadMatchesKAT(t *testing.T) {
	k := kat.KATs(t)
	peer := keysFrom(t, k, "peer")
	ssrc, seq, roc := uint32(k.Uint(t, "inputs", "ssrc")), uint16(k.Uint(t, "inputs", "seq")), uint32(k.Uint(t, "inputs", "roc"))
	payload := k.Hex(t, "inputs", "payload")
	ct := bytes.Clone(payload)
	CryptPayload(&peer, ssrc, seq, roc, ct)
	if hex.EncodeToString(ct) != k.Str(t, "e2e_srtp", "cipher_out") {
		t.Fatalf("%x", ct)
	}
	CryptPayload(&peer, ssrc, seq, roc, ct)
	if !bytes.Equal(ct, payload) {
		t.Fatal("round trip")
	}
}

func TestBuildIVToleratesOversizedSalt(t *testing.T) {
	E2ERTPIV(make([]byte, 16), 0x0102_0304, 0, 0)
	E2ERTPIV(bytes.Repeat([]byte{0xab}, 32), 0xdead_beef, 7, 0xffff)
	iv := E2ERTPIV(bytes.Repeat([]byte{0x11}, 14), 0, 0, 0)
	if !bytes.Equal(iv[:14], bytes.Repeat([]byte{0x11}, 14)) || iv[14] != 0 || iv[15] != 0 {
		t.Fatalf("%x", iv)
	}
}

func TestCryptPayloadRoundtripsAcrossSeqWrap(t *testing.T) {
	keys := Keys{}
	for i := range keys.CipherKey {
		keys.CipherKey[i] = 7
	}
	for i := range keys.Salt {
		keys.Salt[i] = 9
	}
	ssrc := uint32(0x5741_0001)
	seqs := []uint16{0xfffe, 0xffff, 0, 1}
	var send SendROC
	sent := make([][]byte, 4)
	plains := make([][]byte, 4)
	for i, s := range seqs {
		plains[i] = bytes.Repeat([]byte{byte(i * 37)}, 40)
		sent[i] = bytes.Clone(plains[i])
		CryptPayload(&keys, ssrc, s, send.Advance(s), sent[i])
	}
	var rx RecvROC
	for _, i := range []int{0, 1, 3, 2} {
		v := rx.Estimate(seqs[i])
		rx.Commit(v, seqs[i])
		got := bytes.Clone(sent[i])
		CryptPayload(&keys, ssrc, seqs[i], v, got)
		if !bytes.Equal(got, plains[i]) {
			t.Fatalf("seq %#x", seqs[i])
		}
	}
}

// The MediaPipeline composition of session.rs: protect with our self-LID
// keys, unprotect on the other side with its peer-LID keys.
func TestProtectUsesSelfLIDForSendAndPeerUnprotects(t *testing.T) {
	k := kat.KATs(t)
	callKey := k.Hex(t, "inputs", "callKey")
	self, peer := k.Str(t, "inputs", "selfLid"), k.Str(t, "inputs", "peerLid")
	send, _ := DeriveE2EKeys(callKey, kdf.FormatParticipantID(self))
	recvAtPeer, _ := DeriveE2EKeys(callKey, kdf.FormatParticipantID(self))
	wrong, _ := DeriveE2EKeys(callKey, kdf.FormatParticipantID(peer))
	stream := rtp.NewStream(0x1234_5678, 960, false)
	payload := bytes.Repeat([]byte{0x48}, 30)
	h := stream.NextPacket(payload, false)
	var roc SendROC
	packet := ProtectRTP(&send, h, roc.Advance(h.SequenceNumber), warp.MITagLen, payload)

	expect := bytes.Clone(payload)
	CryptPayload(&send, h.SSRC, h.SequenceNumber, 0, expect)
	if !bytes.Equal(packet[rtp.HeaderSize:len(packet)-warp.MITagLen], expect) {
		t.Fatal("ciphertext")
	}
	var r RecvStreams
	if _, _, err := r.Unprotect(&wrong, warp.MITagLen, packet); !errors.Is(err, ErrAuthenticationFailed) {
		t.Fatalf("wrong direction keys: %v", err)
	}
	gh, plain, err := r.Unprotect(&recvAtPeer, warp.MITagLen, packet)
	if err != nil || !bytes.Equal(plain, payload) || gh.SequenceNumber != h.SequenceNumber {
		t.Fatal(err)
	}
	if _, _, err := r.Unprotect(&recvAtPeer, warp.MITagLen, packet); !errors.Is(err, ErrRejected) {
		t.Fatalf("replay: %v", err)
	}
}

func TestInterleavedSSRCsDoNotRejectEachOther(t *testing.T) {
	var r RecvStreams
	for seq := uint16(1); seq < 10; seq++ {
		if !r.Accept(1, seq, 0) || !r.Accept(2, seq+30000, 0) {
			t.Fatal("interleaved stream rejected")
		}
	}
}

func TestAReplayWithinOneStreamIsStillRejected(t *testing.T) {
	var r RecvStreams
	if !r.Accept(1, 5, 0) || r.Accept(1, 5, 0) || !r.Accept(1, 4, 0) || r.Accept(1, 4, 0) {
		t.Fatal("replay window")
	}
	if !r.Accept(1, 200, 0) || r.Accept(1, 5, 0) {
		t.Fatal("packet behind the window")
	}
}

func TestTheStreamTableIsBoundedAndRefusesRatherThanEvicting(t *testing.T) {
	var r RecvStreams
	for ssrc := range uint32(srtpReplayStreamCap) {
		if !r.Accept(ssrc, 1, 0) {
			t.Fatal(ssrc)
		}
	}
	if r.Accept(1000, 1, 0) {
		t.Fatal("over the cap")
	}
	if !r.Accept(0, 2, 0) {
		t.Fatal("a tracked stream was evicted")
	}
}

func TestEachStreamKeepsItsOwnRolloverCounter(t *testing.T) {
	var r RecvStreams
	for _, seq := range []uint16{0xfffe, 0xffff} {
		r.Accept(1, seq, r.EstimateROC(1, seq))
	}
	if roc := r.EstimateROC(1, 0); roc != 1 {
		t.Fatal(roc)
	}
	r.Accept(1, 0, 1)
	r.Accept(2, 5, r.EstimateROC(2, 5))
	if r.EstimateROC(2, 6) != 0 || r.EstimateROC(1, 1) != 1 {
		t.Fatal("ROC shared across SSRCs")
	}
}

func TestSRTCPReplay(t *testing.T) {
	var r SRTCPReplay
	if !r.Accept(1, 1) || r.Accept(1, 1) || !r.Accept(1, 3) || !r.Accept(1, 2) || r.Accept(1, 2) {
		t.Fatal("window")
	}
	if !r.Accept(1, 100) || r.Accept(1, 3) {
		t.Fatal("behind window")
	}
	for s := uint32(2); s <= 16; s++ {
		r.Accept(s, 1)
	}
	if r.Accept(99, 1) {
		t.Fatal("cap")
	}
}
