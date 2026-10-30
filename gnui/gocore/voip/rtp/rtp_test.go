// Ported from whatsapp-rust 5e72baf wacore/src/voip/rtp.rs (tests), MIT.

package rtp

import (
	"bytes"
	"encoding/hex"
	"testing"

	pionrtp "github.com/pion/rtp"

	"zapfast/gocore/voip/internal/kat"
)

func TestEncodeHeadersMatchKAT(t *testing.T) {
	k := kat.KATs(t)
	ssrc := uint32(k.Uint(t, "inputs", "ssrc"))
	speech := Header{Marker: true, PayloadType: 120, SequenceNumber: 1, SSRC: ssrc}
	if got := hex.EncodeToString(speech.Encode()); got != k.Str(t, "rtp", "speechHeader16") {
		t.Fatalf("speech %s", got)
	}
	dtx := Header{PayloadType: 120, SequenceNumber: 2, Timestamp: 320, SSRC: ssrc, HasExtensionWord: true, ExtensionWord: 0x3001_0000}
	if got := hex.EncodeToString(dtx.Encode()); got != k.Str(t, "rtp", "dtxHeader20") {
		t.Fatalf("dtx %s", got)
	}
}

func TestParseRoundTripsFixedFields(t *testing.T) {
	h := Header{Marker: true, PayloadType: 120, SequenceNumber: 0x1234, Timestamp: 0xdead_beef, SSRC: 0x0102_0304}
	b := h.Encode()
	if n, ok := HeaderByteLength(b); !ok || n != 16 {
		t.Fatalf("length %d", n)
	}
	if got, ok := ParseHeader(b); !ok || got != h {
		t.Fatalf("%+v", got)
	}
}

// The standard fields agree with pion/rtp, which is what this package does
// not use for the WhatsApp-specific extension block.
func TestStandardFieldsAgreeWithPion(t *testing.T) {
	for _, h := range []Header{
		{Marker: true, PayloadType: 120, SequenceNumber: 9, Timestamp: 960, SSRC: 0x1234_5678},
		{PayloadType: 120, SequenceNumber: 2, Timestamp: 320, SSRC: 7, HasExtensionWord: true, ExtensionWord: ExtensionDTXWord},
		{Marker: true, PayloadType: PayloadTypeH264, SequenceNumber: 3, Timestamp: 6000, SSRC: 1, HasVideo: true, Video: VideoExtension{MediaFrameInfo: 8, TransportSequence: 3}},
	} {
		b := append(h.Encode(), 0x50, 1, 2)
		var p pionrtp.Packet
		if err := p.Unmarshal(b); err != nil {
			t.Fatal(err)
		}
		if p.Marker != h.Marker || p.PayloadType != h.PayloadType || p.SequenceNumber != h.SequenceNumber ||
			p.Timestamp != h.Timestamp || p.SSRC != h.SSRC || !p.Extension || p.ExtensionProfile != WhatsAppExtensionProfile ||
			!bytes.Equal(p.Payload, []byte{0x50, 1, 2}) {
			t.Fatalf("pion read %+v for %+v", p.Header, h)
		}
	}
}

func TestEstimateWireBytesMatchKAT(t *testing.T) {
	k := kat.KATs(t)
	payload := k.Hex(t, "inputs", "payload")
	if got := EstimateSRTPWireBytes(payload); uint64(got) != k.Uint(t, "rtp", "estimateSpeech12") {
		t.Fatal(got)
	}
	if got := EstimateSRTPWireBytes([]byte{0x10}); uint64(got) != k.Uint(t, "rtp", "estimateDtx1") {
		t.Fatal(got)
	}
	if got := EstimateSRTPWireBytes(OpusPrimingFrame2); uint64(got) != k.Uint(t, "rtp", "estimatePriming2") {
		t.Fatal(got)
	}
}

func TestClassifiers(t *testing.T) {
	check := func(name string, got, want bool) {
		t.Helper()
		if got != want {
			t.Errorf("%s: got %v", name, got)
		}
	}
	check("dtx 0x10", IsOpusDTXPayload([]byte{0x10}), true)
	check("dtx 0x90", IsOpusDTXPayload([]byte{0x90}), true)
	check("dtx bb03", IsOpusDTXPayload([]byte{0xbb, 0x03}), true)
	check("dtx empty", IsOpusDTXPayload(nil), false)
	check("mlow dtx", isMLowDTXPayload([]byte{0x90}), true)
	check("mlow dtx speech", isMLowDTXPayload([]byte{0x48, 0}), false)
	check("priming1", IsOpusPrimingPayload(OpusPrimingFrame1), true)
	check("priming2", IsOpusPrimingPayload(OpusPrimingFrame2), true)
	check("priming short", IsOpusPrimingPayload([]byte{0x12, 0x36}), false)
	check("mlow speech", IsOpusMLowSpeechPayload(bytes.Repeat([]byte{0x48}, 20)), true)
	check("mlow speech short", IsOpusMLowSpeechPayload(bytes.Repeat([]byte{0x48}, 4)), false)
	for pt, want := range map[uint8]bool{111: true, 120: true, 121: true, 122: true, 96: false} {
		check("pt", IsWhatsAppOpusPayloadType(pt), want)
	}
}

func TestAudioStreamPayloadTypeIsConfigurableWithoutResettingTiming(t *testing.T) {
	s := NewStream(0x1122_3344, 2880, false)
	first := s.NextPacket(bytes.Repeat([]byte{0x48}, 20), false)
	if first.PayloadType != PayloadTypeMLow || first.Timestamp != 0 {
		t.Fatalf("%+v", first)
	}
	if !s.SetPayloadType(PayloadTypeOpus) {
		t.Fatal("set 111")
	}
	second := s.NextPacket(bytes.Repeat([]byte{0x48}, 20), false)
	if second.PayloadType != PayloadTypeOpus || second.SequenceNumber != 2 || second.Timestamp != 2880 {
		t.Fatalf("%+v", second)
	}
	if s.SetPayloadType(128) {
		t.Fatal("128 accepted")
	}
}

func TestVideoStreamMarkerAndTimestampAdvancePerAU(t *testing.T) {
	s, _ := NewVideoStream(0x1122_3344, VideoTSStride15FPS)
	p0 := s.NextVideoPacket(false, VideoMediaFrameInfoIDR)
	p1 := s.NextVideoPacket(false, VideoMediaFrameInfoIDR)
	p2 := s.NextVideoPacket(true, VideoMediaFrameInfoIDR)
	ps := []Header{p0, p1, p2}
	for i, p := range ps {
		if p.SequenceNumber != uint16(i) || p.Timestamp != 0 || p.Marker != (i == 2) ||
			p.Video.MediaFrameInfo != VideoMediaFrameInfoIDR || p.Video.TransportSequence != uint16(i) {
			t.Fatalf("p%d %+v", i, p)
		}
	}
	if s.RTPTimestamp() != 0 {
		t.Fatal("SR timestamp")
	}
	p3 := s.NextVideoPacket(true, VideoMediaFrameInfoDelta)
	if p3.Timestamp != VideoTSStride15FPS || p3.SequenceNumber != 3 || s.RTPTimestamp() != VideoTSStride15FPS ||
		p3.Video.MediaFrameInfo != VideoMediaFrameInfoDelta || p3.Video.TransportSequence != 3 {
		t.Fatalf("p3 %+v", p3)
	}
	for _, p := range append(ps, p3) {
		if p.PayloadType != PayloadTypeH264 || p.HasExtensionWord || p.ByteSize() != VideoHeaderSize {
			t.Fatalf("%+v", p)
		}
	}
}

func TestVideoStreamChangesCadenceWithoutResettingTimestamp(t *testing.T) {
	s, _ := NewVideoStream(0x1122_3344, VideoTSStride15FPS)
	if s.NextVideoPacket(true, 0).Timestamp != 0 {
		t.Fatal("first")
	}
	if !s.SetTimestampStride(VideoTSStride20FPS) {
		t.Fatal("stride")
	}
	second, third := s.NextVideoPacket(true, 0), s.NextVideoPacket(true, 0)
	if second.Timestamp != VideoTSStride15FPS || third.Timestamp != VideoTSStride15FPS+VideoTSStride20FPS {
		t.Fatal(second.Timestamp, third.Timestamp)
	}
	if s.SetTimestampStride(0) {
		t.Fatal("zero stride accepted")
	}
	if _, ok := NewVideoStream(1, 0); ok {
		t.Fatal("zero stride stream")
	}
}

func TestVideoSourceTimestampAcceptsRepeatAndWrapButRejectsLate(t *testing.T) {
	s, _ := NewVideoStream(0x1122_3344, VideoTSStride15FPS)
	s.NextVideoPacket(true, 0)
	if !s.SetTimestamp(0) || !s.SetTimestamp(50) || s.SetTimestamp(^uint32(0)-50) {
		t.Fatal("late timestamp")
	}
	w, _ := NewVideoStream(0x1122_3344, VideoTSStride15FPS)
	if !w.SetTimestamp(^uint32(0) - 100) {
		t.Fatal("first")
	}
	w.NextVideoPacket(true, 0)
	if !w.SetTimestamp(50) {
		t.Fatal("wrap")
	}
}

func TestVideoHeaderRoundTripsThroughParse(t *testing.T) {
	s, _ := NewVideoStream(0xdead_beef, VideoTSStride15FPS)
	s.NextVideoPacket(true, VideoMediaFrameInfoIDR)
	h := s.NextVideoPacket(true, VideoMediaFrameInfoDelta)
	b := h.Encode()
	if n, _ := HeaderByteLength(b); n != 28 {
		t.Fatal(n)
	}
	if got, ok := ParseHeader(b); !ok || got != h {
		t.Fatalf("%+v", got)
	}
}

func TestFrameInfoIsIndependentOfOptionalExtensions(t *testing.T) {
	for info := range 256 {
		for length := 1; length <= 16; length++ {
			ext := make([]byte, length+3)
			copy(ext, []byte{0, 0x0f, 0x30 | byte(length-1), byte(info)})
			for len(ext)%4 != 0 {
				ext = append(ext, 0)
			}
			packet := []byte{0x91, 97, 0, 1, 0, 0, 0, 1, 0, 0, 0, 1, 0, 0, 0, 2, 0xde, 0xbe, 0, byte(len(ext) / 4)}
			packet = append(packet, ext...)
			if got, ok := ParseMediaFrameInfo(packet); !ok || got != byte(info) {
				t.Fatalf("info %d len %d", info, length)
			}
			if _, ok := ParseVideoExtension(packet); ok {
				t.Fatal("exact video extension")
			}
			packet[16], packet[17] = 0xbe, 0xde
			if _, ok := ParseMediaFrameInfo(packet); ok {
				t.Fatal("other profile")
			}
		}
	}
}

func TestFrameInfoPaddingDuplicatesAndTruncationMatchWASM(t *testing.T) {
	type tc struct {
		ext  []byte
		want int
	}
	for _, c := range []tc{
		{[]byte{0x30, 0, 0, 0}, 0},
		{[]byte{0, 0x0f, 0, 0}, -1},
		{[]byte{0xf0, 0xff, 0x30, 3}, 3},
		{[]byte{0x30, 1, 0x30, 3}, 3},
		{[]byte{0x30, 3, 0x30, 1}, 1},
		{[]byte{0x30, 1, 0x37, 3, 0, 0, 0, 0, 0, 0, 0, 0}, 1},
		{[]byte{0x37, 3, 0, 0, 0, 0, 0, 0, 0, 0x30, 1, 0}, 1},
		{[]byte{0x32, 1, 0, 0, 0x35, 3, 0, 0, 0, 0, 0, 0}, 1},
		{[]byte{0x30, 3, 0xff, 0}, 3},
		{[]byte{0xff, 0x30, 3, 0}, -1},
		{[]byte{0x30, 1, 0x3f, 3}, 1},
		{[]byte{0x3f, 3, 0, 0}, -1},
		{[]byte{0, 0, 0, 0x30}, -1},
	} {
		packet := append([]byte{0x90, 97, 0, 1, 0, 0, 0, 1, 0, 0, 0, 1, 0xde, 0xbe, 0, byte(len(c.ext) / 4)}, c.ext...)
		got, ok := ParseMediaFrameInfo(packet)
		if (c.want < 0 && ok) || (c.want >= 0 && (!ok || int(got) != c.want)) {
			t.Fatalf("%x: got %d %v", c.ext, got, ok)
		}
		packet[15]++
		if _, ok := ParseMediaFrameInfo(packet); ok {
			t.Fatal("truncated block")
		}
	}
	for _, p := range [][]byte{nil, bytes.Repeat([]byte{0x80}, 11), bytes.Repeat([]byte{0x90}, 12), bytes.Repeat([]byte{0x80}, 12), bytes.Repeat([]byte{0x8f}, 12)} {
		if _, ok := ParseMediaFrameInfo(p); ok {
			t.Fatalf("%x", p)
		}
	}
}

func TestVideoHeaderUsesWASMExtensionLayout(t *testing.T) {
	s, _ := NewVideoStream(0x1122_3344, VideoTSStride15FPS)
	b := s.NextVideoPacket(true, VideoMediaFrameInfoIDR).Encode()
	want := []byte{0x90, 0xe1, 0, 0, 0, 0, 0, 0, 0x11, 0x22, 0x33, 0x44, 0xde, 0xbe, 0, 3,
		0x30, 0x08, 0x51, 0, 0, 0x61, 0, 0, 0x91, 0, 0, 0}
	if !bytes.Equal(b, want) {
		t.Fatalf("%x", b)
	}
	profile, ext, has, ok := ExtensionProfileAndData(b)
	if !ok || !has || profile != WhatsAppExtensionProfile || !bytes.Equal(ext, b[16:28]) {
		t.Fatal("extension view")
	}
}

func checkCapture(t *testing.T, hdrKey, extKey string, want Header) {
	t.Helper()
	k := kat.KATs(t)
	captured := k.Hex(t, "rtp", hdrKey)
	if n, _ := HeaderByteLength(captured); n != 28 {
		t.Fatal(n)
	}
	_, ext, _, _ := ExtensionProfileAndData(captured)
	if hex.EncodeToString(ext) != k.Str(t, "rtp", extKey) {
		t.Fatal("extension")
	}
	if got, ok := ParseHeader(captured); !ok || got != want {
		t.Fatalf("%+v", got)
	}
}

func TestVideoHeaderMatchesAndroidCapture(t *testing.T) {
	checkCapture(t, "androidVideoHeader28", "androidVideoExtension12", Header{
		Marker: true, PayloadType: PayloadTypeH264, SequenceNumber: 1, Timestamp: 114_120, SSRC: 0x49c5_fb8c,
		HasVideo: true, Video: VideoExtension{MediaFrameInfo: 0x09, ShortOffset: 29, TransportSequence: 0x0c3f},
	})
}

func TestDeltaVideoHeaderMatchesAndroidCapture(t *testing.T) {
	checkCapture(t, "androidVideoDeltaHeader28", "androidVideoDeltaExtension12", Header{
		Marker: true, PayloadType: PayloadTypeH264, SequenceNumber: 7, Timestamp: 143_010, SSRC: 0x9b19_59f0,
		HasVideo: true, Video: VideoExtension{MediaFrameInfo: 0x01, ShortOffset: 36, TransportSequence: 0x0a6a},
	})
	if VideoMediaFrameInfoIDR != VideoMediaFrameInfoDelta|0x08 {
		t.Fatal("IDR bit")
	}
}

func videoHeaderWithPiggyback(offset uint16, padded bool) []byte {
	b0 := byte(0x90)
	if padded {
		b0 = 0xb0
	}
	h := []byte{b0, 0xe1, 0, 7, 0, 0x02, 0x2e, 0xa2, 0x9b, 0x19, 0x59, 0xf0, 0xde, 0xbe, 0, 4}
	return append(h, 0x30, 0x01, 0x51, 0, 0, 0x61, 0, 0x24, 0x71, byte(offset>>8), byte(offset), 0x91, 0x0a, 0x6a, 0, 0)
}

func TestTheAudioPiggybackOffsetIsReadAmongTheOtherVideoElements(t *testing.T) {
	h := videoHeaderWithPiggyback(321, false)
	if off, ok := ParseAudioPiggybackOffset(h); !ok || off != 321 {
		t.Fatal(off)
	}
	if _, ok := ParseVideoExtension(h); ok {
		t.Fatal("exact shape")
	}
	if info, ok := ParseMediaFrameInfo(h); !ok || info != 1 {
		t.Fatal(info)
	}
	captured := kat.KATs(t).Hex(t, "rtp", "androidVideoDeltaHeader28")
	if _, ok := ParseAudioPiggybackOffset(captured); ok {
		t.Fatal("capture has no piggyback")
	}
}

func TestOnlyAWellFormedPiggybackElementIsBelieved(t *testing.T) {
	rfc := videoHeaderWithPiggyback(40, false)
	rfc[12], rfc[13] = 0xbe, 0xde
	if off, ok := ParseAudioPiggybackOffset(rfc); !ok || off != 40 {
		t.Fatal("RFC 8285 profile")
	}
	other := videoHeaderWithPiggyback(40, false)
	other[12], other[13] = 0x10, 0x00
	if _, ok := ParseAudioPiggybackOffset(other); ok {
		t.Fatal("other profile")
	}
	wrong := videoHeaderWithPiggyback(40, false)
	wrong[24] = 0x72
	if _, ok := ParseAudioPiggybackOffset(wrong); ok {
		t.Fatal("wrong length")
	}
	stopped := videoHeaderWithPiggyback(40, false)
	stopped[21] = 0xf0
	if _, ok := ParseAudioPiggybackOffset(stopped); ok {
		t.Fatal("id 15 must stop")
	}
	if _, ok := ParseAudioPiggybackOffset([]byte{0x80, 0xe1, 0, 1, 0, 0, 0, 1, 0, 0, 0, 1}); ok {
		t.Fatal("no extension")
	}
}

func TestAPiggybackedAudioPacketIsSplitOffTheMediaItRodeWith(t *testing.T) {
	media := []byte{0x41, 1, 2, 3, 4}
	audio := []byte{0x80, 0xfa, 0, 9, 0, 0, 0x21, 0xc0, 0x12, 0x34, 0x56, 0x78, 0x50, 7, 7}
	gotMedia, gotAudio, ok := SplitAudioPiggyback(videoHeaderWithPiggyback(5, false), append(bytes.Clone(media), audio...))
	if !ok || !bytes.Equal(gotAudio, audio) || !bytes.Equal(gotMedia, media) {
		t.Fatal("split")
	}
	for _, off := range []uint16{0, 20, 21} {
		payload := append(bytes.Clone(media), audio...)
		m, _, ok := SplitAudioPiggyback(videoHeaderWithPiggyback(off, false), payload)
		if ok || len(m) != len(media)+len(audio) {
			t.Fatalf("offset %d", off)
		}
	}
}

func TestPaddingBelongsToTheMediaPartOfAPiggybackedPacket(t *testing.T) {
	audio := []byte{0x80, 0xfa, 0, 9, 0, 0, 0x21, 0xc0, 0x12, 0x34, 0x56, 0x78, 0x50}
	payload := append([]byte{0x41, 1, 2, 3, 0, 0, 3}, audio...)
	media, got, ok := SplitAudioPiggyback(videoHeaderWithPiggyback(7, true), payload)
	if !ok || !bytes.Equal(got, audio) || !bytes.Equal(media, []byte{0x41, 1, 2, 3}) {
		t.Fatalf("%x %x", media, got)
	}
}

func TestAPlainRTPPayloadSkipsTheHeaderExtensionAndPadding(t *testing.T) {
	packet := Header{PayloadType: PayloadTypeMLowSeparate, SequenceNumber: 9, Timestamp: 960, SSRC: 0x1234_5678}.Encode()
	if len(packet) != HeaderSize {
		t.Fatal(len(packet))
	}
	packet = append(packet, 0x50, 1, 2)
	if p, ok := PlainPayload(packet); !ok || !bytes.Equal(p, []byte{0x50, 1, 2}) {
		t.Fatal("plain")
	}
	packet[0] |= 0x20
	packet = append(packet, 0, 2)
	if p, ok := PlainPayload(packet); !ok || !bytes.Equal(p, []byte{0x50, 1, 2}) {
		t.Fatal("padded")
	}
	if _, ok := PlainPayload(Header{PayloadType: PayloadTypeMLow, SequenceNumber: 1, SSRC: 1}.Encode()); ok {
		t.Fatal("header only")
	}
}

func TestExtensionViewHandlesOneByteProfileAndMalformedLengths(t *testing.T) {
	packet := []byte{0x90, 97, 0, 1, 0, 0, 0, 2, 0, 0, 0, 3, 0xbe, 0xde, 0, 1, 0x31, 1, 2, 3, 0x65}
	profile, ext, has, ok := ExtensionProfileAndData(packet)
	if !ok || !has || profile != 0xbede || !bytes.Equal(ext, packet[16:20]) {
		t.Fatal("view")
	}
	if n, _ := HeaderByteLength(packet); n != 20 {
		t.Fatal(n)
	}
	malformed := bytes.Clone(packet)
	malformed[15] = 2
	if _, _, _, ok := ExtensionProfileAndData(malformed); ok {
		t.Fatal("malformed")
	}
}

func TestVideoStreamWrapsSeqAndTimestamp(t *testing.T) {
	s, _ := NewVideoStream(1, ^uint32(0))
	s.seq, s.timestamp = 0xffff, ^uint32(0)
	if p := s.NextVideoPacket(true, VideoMediaFrameInfoIDR); p.SequenceNumber != 0xffff {
		t.Fatal(p.SequenceNumber)
	}
	p := s.NextVideoPacket(false, VideoMediaFrameInfoDelta)
	if p.SequenceNumber != 0 || p.Timestamp != ^uint32(0)-1 {
		t.Fatalf("%+v", p)
	}
}

func TestStreamSequenceAndMarker(t *testing.T) {
	s := NewStream(0xabcd, 320, false)
	p0 := s.NextPacket(OpusPrimingFrame2, false)
	if p0.SequenceNumber != 1 || p0.Timestamp != 0 || p0.Marker {
		t.Fatalf("%+v", p0)
	}
	d1 := s.NextPacket([]byte{0x90}, false)
	if d1.SequenceNumber != 2 || d1.Timestamp != 320 || d1.Marker || !d1.HasExtensionWord || d1.ExtensionWord != ExtensionDTXWord {
		t.Fatalf("%+v", d1)
	}
	if s.RTPTimestamp() != 320 {
		t.Fatal("ts")
	}
	speech := bytes.Repeat([]byte{0x48}, 40)
	sp := s.NextPacket(speech, false)
	if sp.SequenceNumber != 3 || sp.Timestamp != 640 || !sp.Marker || s.RTPTimestamp() != 640 {
		t.Fatalf("%+v", sp)
	}
	if s.NextPacket(speech, false).Marker {
		t.Fatal("second speech marked")
	}
	if s.NextPacket([]byte{0x90}, false).Marker {
		t.Fatal("dtx marked")
	}
	if !s.NextPacket(speech, false).Marker {
		t.Fatal("speech after DTX must re-arm the marker")
	}
}

func TestNativeOpusCanDisableMLowSpeechMarkers(t *testing.T) {
	s := NewStream(0xabcd, 960, false)
	s.SetMLowProfile(false)
	if s.NextPacket(bytes.Repeat([]byte{0x58}, 80), false).Marker {
		t.Fatal("marker")
	}
	s.NextPacket([]byte{0x10}, false)
	if s.NextPacket(bytes.Repeat([]byte{0x58}, 80), false).Marker {
		t.Fatal("marker after dtx")
	}
}

func TestShortMLowSpeechIsNotClassifiedAsDTX(t *testing.T) {
	s := NewStream(0xabcd, 960, false)
	if !s.NextPacket([]byte{0x48, 0}, false).Marker {
		t.Fatal("first")
	}
	next := s.NextPacket([]byte{0x48, 0}, false)
	if next.Marker || next.HasExtensionWord {
		t.Fatalf("%+v", next)
	}
}

func TestPiggybackStreamExtensionWords(t *testing.T) {
	s := NewStream(1, 960, true)
	speech := bytes.Repeat([]byte{0x48}, 40)
	for i := range 4 {
		h := s.NextPacket(speech, false)
		if h.HasExtensionWord != (i >= 2) {
			t.Fatalf("packet %d %+v", i, h)
		}
	}
}
