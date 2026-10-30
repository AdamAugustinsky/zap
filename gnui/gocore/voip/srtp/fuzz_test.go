package srtp

// Native fuzz targets for unprotecting received SRTP and SRTCP packets.
//
//	CGO_ENABLED=0 go test ./gocore/voip/srtp -run '^$' -fuzz '^FuzzUnprotect$' -fuzztime 45s

import (
	"bytes"
	"encoding/hex"
	"errors"
	"testing"

	"zapfast/gocore/voip/internal/kat"
	"zapfast/gocore/voip/kdf"
	"zapfast/gocore/voip/rtp"
)

// fuzzKeys are the audio pipeline vectors' keys, and their packets.
func fuzzKeys(f *testing.F) (rtpKeys, rtcpKeys Keys, tagLen int, packets [][]byte) {
	v := kat.Golden(f).List(f, "audio_pipeline")[0]
	ck, self := v.Hex(f, "call_key"), kdf.FormatParticipantID(v.Str(f, "self_lid"))
	rtpKeys, _ = DeriveE2EKeys(ck, self)
	rtcpKeys, _ = DeriveSRTCPKeys(ck, self)
	for _, p := range v.List(f, "packets") {
		b, _ := hex.DecodeString(p.Get(f).(string))
		packets = append(packets, b)
	}
	b, _ := hex.DecodeString(v.Str(f, "source_description"))
	packets = append(packets, b)
	return rtpKeys, rtcpKeys, int(v.Uint(f, "tag_len")), packets
}

// FuzzUnprotect unprotects an arbitrary packet as SRTP and as SRTCP with
// real keys. SRTP: any failure is one of the three receive errors and
// leaves no stream state behind; a success has a header the packet's own
// and a payload as long as what lies between header and tag, and the same
// packet again is a replay. SRTCP: a success is the packet less its
// 14-byte trailer, and protecting it again under the index it carried
// gives the packet back.
func FuzzUnprotect(f *testing.F) {
	rk, ck, tagLen, packets := fuzzKeys(f)
	for _, p := range packets {
		f.Add(p, uint8(tagLen))
	}
	f.Add([]byte{0x80, 0x78, 0, 1, 0, 0, 0, 0, 0, 0, 0, 1, 0xaa}, uint8(4))
	f.Fuzz(func(t *testing.T, packet []byte, tag uint8) {
		tagLen := int(tag % 24) // past 20 is an invalid length, which must be refused
		var rx RecvStreams
		h, plain, err := rx.Unprotect(&rk, tagLen, packet)
		if err != nil {
			if !errors.Is(err, ErrInvalidPacket) && !errors.Is(err, ErrAuthenticationFailed) && !errors.Is(err, ErrRejected) {
				t.Fatalf("error %v", err)
			}
			if plain != nil || len(rx.streams) != 0 {
				t.Fatalf("a refused packet left a payload or %d streams", len(rx.streams))
			}
		} else {
			if tagLen < 1 || tagLen > 20 {
				t.Fatalf("authenticated under a %d-byte tag", tagLen)
			}
			n, _ := rtp.HeaderByteLength(packet[:len(packet)-tagLen])
			want, _ := rtp.ParseHeader(packet[:len(packet)-tagLen])
			if h != want || len(plain) != len(packet)-tagLen-n || len(plain) == 0 {
				t.Fatalf("header %+v, payload %d of %d", h, len(plain), len(packet))
			}
			if _, _, err := rx.Unprotect(&rk, tagLen, packet); !errors.Is(err, ErrRejected) {
				t.Fatalf("a replay: %v", err)
			}
		}

		body, index, ok := UnprotectSRTCP(&ck, 0x12345678, packet)
		if !ok {
			if body != nil {
				t.Fatalf("a refused SRTCP packet with a body")
			}
			return
		}
		if len(body) != len(packet)-4-SRTCPAuthTagLen || index > 0x7fff_ffff {
			t.Fatalf("SRTCP body %d of %d, index %d", len(body), len(packet), index)
		}
		if got := ProtectSRTCP(&ck, 0x12345678, index, body); packet[len(packet)-14]&0x80 != 0 && !bytes.Equal(got, packet) {
			t.Fatalf("re-protecting the SRTCP body gives other bytes")
		}
	})
}

// FuzzRoundTrip protects a packet and unprotects it under every tag length
// 1..20: the header and payload come back, and a second copy is a replay.
// With a tag of at least 4 bytes (what live calls negotiate), a packet with
// any one byte changed is refused without touching the stream state; a
// shorter tag is forged by chance (1 in 256 for one byte), so it is not
// asserted on. The same for SRTCP, whose 10-byte tag always is.
func FuzzRoundTrip(f *testing.F) {
	rk, ck, _, _ := fuzzKeys(f)
	f.Add(uint16(1), uint32(960), uint32(0x12345678), false, []byte{0x50, 1, 2, 3}, uint8(4), uint16(3), byte(1))
	f.Add(uint16(65535), uint32(0), uint32(1), true, bytes.Repeat([]byte{0x48}, 40), uint8(10), uint16(0), byte(0x80))
	f.Fuzz(func(t *testing.T, seq uint16, ts, ssrc uint32, video bool, payload []byte, tag uint8, at uint16, flip byte) {
		if len(payload) == 0 {
			return // Unprotect refuses an empty payload, and the sender never makes one
		}
		tagLen := 1 + int(tag%20)
		h := rtp.Header{PayloadType: 120, SequenceNumber: seq, Timestamp: ts, SSRC: ssrc, HasVideo: video}
		if video {
			h.PayloadType = rtp.PayloadTypeH264
			h.Video = rtp.VideoExtension{MediaFrameInfo: byte(seq), TransportSequence: seq}
		}
		var roc SendROC
		packet := ProtectRTP(&rk, h, roc.Advance(seq), tagLen, payload)
		if len(packet) != h.ByteSize()+len(payload)+tagLen {
			t.Fatalf("%d bytes protected", len(packet))
		}
		var rx RecvStreams
		gotH, plain, err := rx.Unprotect(&rk, tagLen, packet)
		if err != nil || gotH != h || !bytes.Equal(plain, payload) {
			t.Fatalf("round trip: %v, %+v", err, gotH)
		}
		if _, _, err := rx.Unprotect(&rk, tagLen, packet); !errors.Is(err, ErrRejected) {
			t.Fatalf("a replay: %v", err)
		}
		if flip != 0 && tagLen >= 4 {
			var fresh RecvStreams
			bad := append([]byte(nil), packet...)
			bad[int(at)%len(bad)] ^= flip
			if _, _, err := fresh.Unprotect(&rk, tagLen, bad); err == nil || len(fresh.streams) != 0 {
				t.Fatalf("a packet changed at byte %d was accepted (%v)", int(at)%len(bad), err)
			}
		}

		// An RR: the 8-byte header (with the sender SSRC) SRTCP keeps clear,
		// then the payload as its body.
		rtcpPacket := append([]byte{0x81, 0xc9, 0, byte(len(payload) / 4), byte(ssrc >> 24), byte(ssrc >> 16), byte(ssrc >> 8), byte(ssrc)}, payload...)
		index := uint32(seq) | ts&0x7fff_0000
		sp := ProtectSRTCP(&ck, ssrc, index, rtcpPacket)
		body, gotIndex, ok := UnprotectSRTCP(&ck, ssrc, sp)
		if !ok || gotIndex != index || !bytes.Equal(body, rtcpPacket) {
			t.Fatalf("SRTCP round trip: %v, index %d of %d", ok, gotIndex, index)
		}
		if flip != 0 {
			sp[int(at)%len(sp)] ^= flip
			if _, _, ok := UnprotectSRTCP(&ck, ssrc, sp); ok {
				t.Fatalf("an SRTCP packet changed at byte %d was accepted", int(at)%len(sp))
			}
		}
	})
}
