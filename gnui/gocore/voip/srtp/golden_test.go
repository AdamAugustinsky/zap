package srtp

// Byte-for-byte checks against testdata/golden.json, written by
// whatsapp-rust 5e72baf's own MediaPipeline, VideoPipeline, e2e_srtp and
// hbh_srtp (zfcalls/tests/voip_golden.rs). The vectors are committed data: zfcalls went with the Rust tree in WP9, so they can no longer be regenerated here.

import (
	"bytes"
	"encoding/binary"
	"encoding/hex"
	"testing"

	"zapfast/gocore/voip/h264"
	"zapfast/gocore/voip/internal/kat"
	"zapfast/gocore/voip/kdf"
	"zapfast/gocore/voip/rtcp"
	"zapfast/gocore/voip/rtp"
)

func checkKeys(t *testing.T, d kat.Doc, k Keys, what string) {
	t.Helper()
	if hex.EncodeToString(k.CipherKey[:]) != d.Str(t, "cipher") || hex.EncodeToString(k.Salt[:]) != d.Str(t, "salt") ||
		hex.EncodeToString(k.AuthKey[:]) != d.Str(t, "auth") {
		t.Fatalf("%s keys differ from the Rust ones", what)
	}
}

func eq(t *testing.T, got []byte, want, what string) {
	t.Helper()
	if hex.EncodeToString(got) != want {
		t.Fatalf("%s:\n got  %x\n want %s", what, got, want)
	}
}

func TestGoldenE2EKeys(t *testing.T) {
	for _, v := range kat.Golden(t).List(t, "e2e_keys") {
		ck, lid := v.Hex(t, "call_key"), v.Str(t, "lid")
		k, _ := DeriveE2EKeys(ck, lid)
		checkKeys(t, v.Sub(t, "srtp"), k, "srtp "+lid)
		c, _ := DeriveSRTCPKeys(ck, lid)
		checkKeys(t, v.Sub(t, "srtcp"), c, "srtcp "+lid)
	}
}

// The whole audio send path of MediaPipeline: sequencer, WARP header, ROC,
// AES-CTR, MI tag; and its SRTCP-protected SDES.
func TestGoldenAudioPipeline(t *testing.T) {
	for _, v := range kat.Golden(t).List(t, "audio_pipeline") {
		ck, self, peer := v.Hex(t, "call_key"), v.Str(t, "self_lid"), v.Str(t, "peer_lid")
		ssrc, tagLen := uint32(v.Uint(t, "ssrc")), int(v.Uint(t, "tag_len"))
		send, _ := DeriveE2EKeys(ck, kdf.FormatParticipantID(self))
		recv, _ := DeriveE2EKeys(ck, kdf.FormatParticipantID(self)) // the peer's receive keys for us
		s := rtp.NewStream(ssrc, uint32(v.Uint(t, "samples_per_packet")), false)
		if v.Get(t, "payload_type") != nil {
			s.SetPayloadType(uint8(v.Uint(t, "payload_type")))
		}
		s.SetMLowProfile(v.Get(t, "mlow").(bool))
		var roc SendROC
		var rx RecvStreams
		payloads, packets := v.List(t, "payloads"), v.List(t, "packets")
		for i, p := range payloads {
			payload, _ := hex.DecodeString(p.Get(t).(string))
			h := s.NextPacket(payload, false)
			got := ProtectRTP(&send, h, roc.Advance(h.SequenceNumber), tagLen, payload)
			eq(t, got, packets[i].Get(t).(string), "audio packet")
			if _, plain, err := rx.Unprotect(&recv, tagLen, got); err != nil || !bytes.Equal(plain, payload) {
				t.Fatalf("unprotect: %v", err)
			}
		}
		var entropy [12]byte
		binary.BigEndian.PutUint32(entropy[6:], ssrc)
		copy(entropy[10:], ck[:2])
		sdes := rtcp.WhatsAppSourceDescription(ssrc, rtcp.WhatsAppCName(entropy), false)
		keys, _ := DeriveSRTCPKeys(ck, kdf.FormatParticipantID(self))
		eq(t, ProtectSRTCP(&keys, ssrc, 1, sdes[:]), v.Str(t, "source_description"), "SDES")
		_ = peer
	}
}

// VideoPipeline.protect_video: packetization, frame info, the video
// sequencer and SRTP.
func TestGoldenVideoPipeline(t *testing.T) {
	for _, v := range kat.Golden(t).List(t, "video_pipeline") {
		ck := v.Hex(t, "call_key")
		send, _ := DeriveE2EKeys(ck, kdf.FormatParticipantID(v.Str(t, "self_lid")))
		vs, _ := rtp.NewVideoStream(uint32(v.Uint(t, "ssrc")), uint32(v.Uint(t, "ts_stride")))
		tagLen := int(v.Uint(t, "tag_len"))
		var roc SendROC
		var payloads h264.PacketizedAU
		aus, packets := v.List(t, "aus"), v.List(t, "packets")
		for i, a := range aus {
			au, _ := hex.DecodeString(a.Get(t).(string))
			h264.Packetize(au, &payloads)
			info := rtp.VideoMediaFrameInfoDelta
			if h264.AUHasIDR(au) {
				info = rtp.VideoMediaFrameInfoIDR
			}
			want := packets[i].Get(t).([]any)
			if len(want) != payloads.Len() {
				t.Fatalf("AU %d: %d packets, Rust made %d", i, payloads.Len(), len(want))
			}
			for j := range payloads.Len() {
				h := vs.NextVideoPacket(j == payloads.Len()-1, info)
				eq(t, ProtectRTP(&send, h, roc.Advance(h.SequenceNumber), tagLen, payloads.At(j)), want[j].(string), "video packet")
			}
		}
	}
}

func TestGoldenE2ECrypt(t *testing.T) {
	for _, v := range kat.Golden(t).List(t, "e2e_crypt") {
		k, _ := DeriveE2EKeys(v.Hex(t, "call_key"), v.Str(t, "lid"))
		buf := v.Hex(t, "payload")
		CryptPayload(&k, uint32(v.Uint(t, "ssrc")), uint16(v.Uint(t, "seq")), uint32(v.Uint(t, "roc")), buf)
		eq(t, buf, v.Str(t, "out"), "crypt")
	}
}

func TestGoldenSRTCP(t *testing.T) {
	for _, v := range kat.Golden(t).List(t, "srtcp") {
		k, _ := DeriveSRTCPKeys(v.Hex(t, "call_key"), v.Str(t, "lid"))
		ssrc, index := uint32(v.Uint(t, "ssrc")), uint32(v.Uint(t, "index"))
		got := ProtectSRTCP(&k, ssrc, index, v.Hex(t, "rtcp"))
		eq(t, got, v.Str(t, "out"), "srtcp")
		// Like the Rust, protect keys the IV off the full index and
		// unprotect off the 31 bits on the wire, so only indexes below 2^31
		// round-trip; the senders never get there (they start at 1).
		if len(got) >= rtcpHeaderLen+4+SRTCPAuthTagLen && index < 1<<31 {
			plain, idx, ok := UnprotectSRTCP(&k, ssrc, got)
			if !ok || !bytes.Equal(plain, v.Hex(t, "rtcp")) || idx != index&0x7fff_ffff {
				t.Fatal("unprotect")
			}
		}
	}
}

func TestGoldenHBH(t *testing.T) {
	for _, v := range kat.Golden(t).List(t, "hbh") {
		h := v.Hex(t, "hbh_key")
		for name, f := range map[string]func([]byte) ([]byte, bool){
			"uplink30": DeriveHBHSRTCPKeyUplink, "downlink30": DeriveHBHSRTCPKeyDownlink,
			"shared_srtp30": DeriveHBHSRTPKeyShared, "shared_srtcp30": DeriveHBHSRTCPKeyShared,
		} {
			got, _ := f(h)
			eq(t, got, v.Str(t, name), name)
		}
		keying, _ := KeyingFromHBHSRTPKeyShared(h)
		s := ExpandSessionKeys(&keying)
		eq(t, s.SessionKey[:], v.Str(t, "session_key"), "session key")
		eq(t, s.SessionSalt[:], v.Str(t, "session_salt"), "session salt")
		eq(t, s.AuthKey[:], v.Str(t, "session_auth"), "session auth")
		sr := kat.Golden(t).List(t, "srtcp")[8].Hex(t, "rtcp") // the SR+SDES compound
		for _, m := range v.List(t, "modes") {
			mode := HBHDirectional
			if m.Str(t, "mode") == "shared" {
				mode = HBHShared
			}
			up, _ := DeriveHBHSRTCPKeysUplinkForMode(h, mode)
			down, _ := DeriveHBHSRTCPKeysDownlinkForMode(h, mode)
			checkKeys(t, m.Sub(t, "uplink"), up, "uplink")
			checkKeys(t, m.Sub(t, "downlink"), down, "downlink")
			eq(t, ProtectSRTCP(&up, 0x1234_5678, 3, sr), m.Str(t, "protected_uplink_sr"), "hbh SR")
		}
		for _, r := range v.List(t, "rtp") {
			buf := r.Hex(t, "payload")
			CryptHBHRTPPayload(&s, uint32(r.Uint(t, "ssrc")), r.Uint(t, "index"), buf)
			eq(t, buf, r.Str(t, "out"), "hbh rtp")
		}
	}
}
