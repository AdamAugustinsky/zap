// Ported from whatsapp-rust 5e72baf wacore/src/voip/demux.rs and the parser
// fuzz test of wacore/src/voip/mod.rs (tests), MIT.

package demux

import (
	"bytes"
	"encoding/hex"
	"errors"
	"testing"

	"zapfast/gocore/voip/rtcp"
	"zapfast/gocore/voip/rtp"
	"zapfast/gocore/voip/stun"
)

func TestUnwrapsCapturePinnedGroupForwardingEnvelopes(t *testing.T) {
	for _, v := range []struct {
		packet, want string
		n            int
	}{
		{"0902336e0100018890e1001300120c96772e6aa9", "90e1001300120c96772e6aa9", 8},
		{"09047eb5410001000000001090e100010013fbf08f1df407", "90e100010013fbf08f1df407", 12},
		{"0907338f2900020a00c801e000000000000090780001000331808cd481d5", "90780001000331808cd481d5", 18},
	} {
		p, _ := hex.DecodeString(v.packet)
		want, _ := hex.DecodeString(v.want)
		got, err := UnwrapGroupForwarding(p)
		if err != nil || !bytes.Equal(got.Payload, want) || !got.GroupForwarded || got.HeaderLen != v.n || Classify(got.Payload) != KindRtp {
			t.Fatalf("%s: %+v %v", v.packet, got, err)
		}
	}
}

func TestGroupForwardingPassesPlainPacketsAndRejectsMalformedEnvelopes(t *testing.T) {
	r := []byte{0x90, 0xe1, 0, 1, 0, 0, 0, 1, 0, 0, 0, 2}
	if got, err := UnwrapGroupForwarding(r); err != nil || !bytes.Equal(got.Payload, r) || got.GroupForwarded || got.HeaderLen != 0 {
		t.Fatal("plain packet")
	}
	for _, p := range [][]byte{
		{0x09}, {0x09, 0xff}, {0x09, 0x02, 0, 0, 0, 0, 0, 0},
		{0x09, 0x02, 0, 0, 0, 0, 0, 0, 0x10, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0},
	} {
		if _, err := UnwrapGroupForwarding(p); !errors.Is(err, ErrGroupForwarding) {
			t.Fatalf("%x", p)
		}
	}
}

func TestClassifyFirstByte(t *testing.T) {
	video := make([]byte, 12)
	video[0], video[1] = 0x80, 0x61
	audio := make([]byte, 16)
	audio[0], audio[1] = 0x90, 0x78
	cases := []struct {
		b    []byte
		want Kind
	}{
		{[]byte{0, 1}, KindStun}, {[]byte{0, 3}, KindStun},
		{[]byte{0x80, 0xc8, 0, 1, 0, 0, 0, 1}, KindRtcp},
		{[]byte{0x8f, 0xce, 0, 2, 0, 0, 0, 1, 0, 0, 0, 2}, KindRtcp},
		{video, KindRtp}, {audio, KindRtp},
		{[]byte{0xff, 0xff}, KindOther}, {[]byte{0}, KindOther},
	}
	for _, c := range cases {
		if got := Classify(c.b); got != c.want {
			t.Fatalf("%x: %v", c.b, got)
		}
	}
	video[1] = 0xe1
	if Classify(video) != KindRtp {
		t.Fatal("marker video")
	}
}

func garbage() [][]byte {
	seed := []byte{0x80, 0xc8, 0x00, 0x06, 0xde, 0xad, 0xbe, 0xef, 0x21, 0x12, 0xa4, 0x42, 0x01, 0x02,
		0x03, 0x04, 0x05, 0x06, 0x07, 0x08, 0x00, 0x09, 0x00, 0x08, 0x00, 0x00, 0x04, 0x01}
	var out [][]byte
	for i := 0; i <= len(seed); i++ {
		out = append(out, seed[:i])
	}
	for _, n := range []int{0, 1, 2, 3, 7, 8, 11, 12, 19, 20, 21, 64, 255} {
		out = append(out, bytes.Repeat([]byte{0xff}, n))
	}
	return append(out,
		[]byte{0x00, 0x01, 0xff, 0xff, 0x21, 0x12, 0xa4, 0x42, 1, 2, 3, 4, 5, 6, 7, 8},
		[]byte{0x00, 0x01, 0x00, 0x10, 0x21, 0x12, 0xa4, 0x42, 1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12, 0x40, 0x00, 0xff, 0xff, 0xaa},
		[]byte{0x90, 0x78, 0x12, 0x34, 0, 0, 0, 0, 0, 0, 0, 0, 0xde, 0xbe, 0xff, 0xff},
		[]byte{0x90, 0x78, 0x12, 0x34, 0, 0, 0, 0, 0, 0, 0, 0, 0x10},
	)
}

// Every byte-level parser rejects garbage by returning, never panicking.
func TestParsersDontPanicOnGarbage(t *testing.T) {
	for _, b := range garbage() {
		stun.IsSTUN(b)
		stun.MessageType(b)
		stun.TransactionID(b)
		stun.ParseAttributes(b)
		stun.ParseErrorCode(b)
		stun.IsAllocateOrBindingSuccess(b)
		stun.IsAllocateError(b)
		stun.IsWhatsAppPong(b, nil)
		rtp.IsVersion2(b)
		rtp.HeaderByteLength(b)
		rtp.ParseHeader(b)
		rtp.ParseMediaFrameInfo(b)
		rtp.ParseAudioPiggybackOffset(b)
		rtp.PlainPayload(b)
		rtcp.IsRTCP(b)
		rtcp.PayloadType(b)
		rtcp.SenderSSRC(b)
		rtcp.Summarize(b)
		rtcp.SenderReportTiming(b)
		Classify(b)
		UnwrapGroupForwarding(b)
	}
}
