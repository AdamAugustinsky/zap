// Ported from whatsapp-rust 5e72baf wacore/src/voip/rtcp.rs (tests), MIT.

package rtcp

import (
	"bytes"
	"encoding/binary"
	"encoding/hex"
	"reflect"
	"slices"
	"testing"

	pionrtcp "github.com/pion/rtcp"

	"zapfast/gocore/voip/internal/kat"
)

const (
	clock = 16_000
	sspan = uint32(0x5741_0001)
)

func obs(s *ReceptionStats, seq uint16, ts, ssrc uint32) {
	s.Observe(ssrc, seq, ts, uint64(seq)*60, clock)
}

func span(t *testing.T, s *ReceptionStats, want int) {
	t.Helper()
	got, ok := s.FrameSpan()
	if (want < 0 && ok) || (want >= 0 && (!ok || int(got) != want)) {
		t.Fatalf("span %d %v, want %d", got, ok, want)
	}
}

func TestTwoConsecutivePacketsYieldTheStepBetweenThem(t *testing.T) {
	var s ReceptionStats
	obs(&s, 1, 1000, sspan)
	span(t, &s, -1)
	obs(&s, 2, 1960, sspan)
	span(t, &s, 960)
	obs(&s, 3, 2920, sspan)
	span(t, &s, 960)
}

func TestABackwardsTimestampLeavesNoCadenceToBorrow(t *testing.T) {
	var s ReceptionStats
	obs(&s, 1, 1000, sspan)
	obs(&s, 2, 1960, sspan)
	obs(&s, 3, 1000, sspan)
	span(t, &s, -1)
	obs(&s, 4, 1960, sspan)
	span(t, &s, 960)
}

func TestThePacketAfterAReorderedOneStillMeasuresTheRealCadence(t *testing.T) {
	var s ReceptionStats
	obs(&s, 1, 1000, sspan)
	obs(&s, 3, 2920, sspan)
	obs(&s, 2, 1960, sspan)
	obs(&s, 4, 3880, sspan)
	span(t, &s, 960)
}

func TestAnAbsurdForwardJumpStatesNoCadence(t *testing.T) {
	var s ReceptionStats
	obs(&s, 1, 1000, sspan)
	obs(&s, 2, 1960, sspan)
	obs(&s, 3, 1960+clock+1, sspan)
	span(t, &s, -1)
}

func TestANewSSRCResetsTheSpan(t *testing.T) {
	var s ReceptionStats
	obs(&s, 1, 1000, sspan)
	obs(&s, 2, 1960, sspan)
	obs(&s, 3, 5000, 0xdead_beef)
	span(t, &s, -1)
}

func TestTheSpanSurvivesATimestampWrap(t *testing.T) {
	var s ReceptionStats
	obs(&s, 1, ^uint32(0)-400, sspan)
	obs(&s, 2, 559, sspan)
	span(t, &s, 960)
}

func TestARepeatedTimestampDoesNotProduceAZeroSpan(t *testing.T) {
	var s ReceptionStats
	obs(&s, 1, 1000, sspan)
	obs(&s, 2, 1000, sspan)
	span(t, &s, -1)
}

func TestFeedbackPaddingIsNotControlInformation(t *testing.T) {
	nack := []byte{0xa1, PTRTPFB, 0, 4, 0, 0, 0, 1, 0, 0, 0, 2, 0, 10, 0, 0, 0, 0, 0, 4}
	s, ok := Summarize(nack)
	if !ok || !bytes.Equal(s.Feedback[0].FCI, []byte{0, 10, 0, 0}) {
		t.Fatal("padded NACK")
	}
	unpadded := bytes.Clone(nack[:16])
	unpadded[0] &^= 0x20
	unpadded[3] = 3
	u, _ := Summarize(unpadded)
	if !reflect.DeepEqual(u.Feedback, s.Feedback) {
		t.Fatal("padding changed the feedback")
	}
	for _, c := range []byte{0, 1, 16, 255} {
		nack[len(nack)-1] = c
		if _, ok := Summarize(nack); ok {
			t.Fatalf("padding count %d", c)
		}
	}
	nack[len(nack)-1] = 4
	if _, ok := Summarize(append(nack, unpadded...)); ok {
		t.Fatal("only the final member may be padded")
	}
}

func TestCompactReportsMatchKAT(t *testing.T) {
	k := kat.KATs(t)
	ssrc, remote := uint32(k.Uint(t, "inputs", "ssrc")), uint32(k.Uint(t, "rtcp", "remoteSsrc"))
	c208, c209 := Compact208(ssrc, remote), Compact209(ssrc)
	if hex.EncodeToString(c208[:]) != k.Str(t, "rtcp", "compact208") || hex.EncodeToString(c209[:]) != k.Str(t, "rtcp", "compact209") {
		t.Fatal("compact")
	}
}

func TestSenderReportMatchesKAT(t *testing.T) {
	k := kat.KATs(t)
	sr := SenderReport(uint32(k.Uint(t, "inputs", "ssrc")), SenderStats{
		PacketsSent:  uint32(k.Uint(t, "rtcp", "stats", "packetsSent")),
		OctetsSent:   uint32(k.Uint(t, "rtcp", "stats", "octetsSent")),
		RTPTimestamp: uint32(k.Uint(t, "rtcp", "stats", "rtpTimestamp")),
	}, k.Uint(t, "rtcp", "nowMs"))
	if hex.EncodeToString(sr[:]) != k.Str(t, "rtcp", "senderReport") {
		t.Fatal(hex.EncodeToString(sr[:]))
	}
	// pion/rtcp reads the standard SR the same way.
	var p pionrtcp.SenderReport
	if err := p.Unmarshal(sr[:]); err != nil || p.SSRC != uint32(k.Uint(t, "inputs", "ssrc")) ||
		p.PacketCount != uint32(k.Uint(t, "rtcp", "stats", "packetsSent")) {
		t.Fatal("pion", err)
	}
}

func TestNativeCNameAndSDESMatchWireLayout(t *testing.T) {
	cname := WhatsAppCName([12]byte{0x00, 0x11, 0x22, 0x33, 0x44, 0x55, 0x66, 0x77, 0x88, 0x99, 0xaa, 0xbb})
	if string(cname[:]) != "66778@pj899aab.org" {
		t.Fatal(string(cname[:]))
	}
	sdes := SourceDescription(0x1234_5678, cname)
	if hex.EncodeToString(sdes[:]) != "81ca0007123456780112363637373840706a3839396161622e6f726700000000" {
		t.Fatal(hex.EncodeToString(sdes[:]))
	}
	s, ok := Summarize(sdes[:])
	if !ok || !slices.Equal(s.PacketTypes, []uint8{PTSDES}) || !slices.Equal(s.SDESCNameLens, []int{WhatsAppCNameLen}) {
		t.Fatal("summary")
	}
	var p pionrtcp.SourceDescription
	if err := p.Unmarshal(sdes[:]); err != nil || string(p.Chunks[0].Items[0].Text) != string(cname[:]) {
		t.Fatal("pion", err)
	}
}

func TestEmptySDESIsOnePaddedChunk(t *testing.T) {
	sdes := EmptySourceDescription(0x1234_5678)
	if hex.EncodeToString(sdes[:]) != "81ca00021234567800000000" {
		t.Fatal("bytes")
	}
	if s, ok := Summarize(sdes[:]); !ok || len(s.SDESCNameLens) != 0 {
		t.Fatal("summary")
	}
}

func TestSenderReportCompoundIncludesSDES(t *testing.T) {
	var cname [WhatsAppCNameLen]byte
	copy(cname[:], "66778@pj899aab.org")
	c := SenderReportWithSDES(0x1234_5678, SenderStats{5, 600, 1600}, 1_718_000_000_000, cname)
	s, ok := Summarize(c)
	if !ok || !slices.Equal(s.PacketTypes, []uint8{PTSR, PTSDES}) || !slices.Equal(s.SDESCNameLens, []int{WhatsAppCNameLen}) {
		t.Fatal("summary")
	}
}

func cn(s string) (c [WhatsAppCNameLen]byte) {
	copy(c[:], s)
	return c
}

func TestStandardSenderReportEncodesTwoReportBlocks(t *testing.T) {
	reports := []ReceptionReport{
		{SSRC: 0x1111_2222, FractionLost: 0x40, CumulativeLost: 2, ExtendedHighestSequence: 0x0001_0002, Jitter: 123, LastSenderReport: 0x3344_5566, DelaySinceLastSenderReport: 0x7788_99aa},
		{SSRC: 0x3333_4444, CumulativeLost: -1, ExtendedHighestSequence: 7, Jitter: 8},
	}
	c := SenderReportWithSDESAndReports(0x5555_6666, SenderStats{9, 4096, 90_000}, 1_700_000_000_000, cn("00000@pj000000.org"), reports)
	if len(c) != 76+32 || !bytes.Equal(c[:4], []byte{0x82, PTSR, 0, 18}) ||
		binary.BigEndian.Uint32(c[28:]) != reports[0].SSRC || !bytes.Equal(c[32:36], []byte{0x40, 0, 0, 2}) ||
		binary.BigEndian.Uint32(c[52:]) != reports[1].SSRC || !bytes.Equal(c[56:60], []byte{0, 0xff, 0xff, 0xff}) ||
		!bytes.Equal(c[76:80], []byte{0x81, PTSDES, 0, 7}) {
		t.Fatalf("%x", c)
	}
	s, ok := Summarize(c)
	if !ok || !slices.Equal(s.PacketTypes, []uint8{PTSR, PTSDES}) || !slices.Equal(s.ReferencedSSRCs, []uint32{reports[0].SSRC, reports[1].SSRC}) {
		t.Fatal("summary")
	}
	// The standard compound is something pion/rtcp reads.
	pkts, err := pionrtcp.Unmarshal(c)
	if err != nil || len(pkts) != 2 || len(pkts[0].(*pionrtcp.SenderReport).Reports) != 2 {
		t.Fatal("pion", err)
	}
}

func TestParsesNativeVideoProfileReportAndSDESLayouts(t *testing.T) {
	sender, target := uint32(0x1111_2222), uint32(0x3333_4444)
	sr := []byte{0x91, PTSR, 0, 18}
	sr = binary.BigEndian.AppendUint32(sr, sender)
	sr = append(sr, make([]byte, 20)...)
	sr = binary.BigEndian.AppendUint32(sr, target)
	sr = append(sr, 0x20, 0xff, 0xff, 0xfe)
	sr = binary.BigEndian.AppendUint32(sr, 0x0001_2345)
	sr = binary.BigEndian.AppendUint32(sr, 77)
	sr = binary.BigEndian.AppendUint32(sr, 0x5566_7788)
	sr = binary.BigEndian.AppendUint32(sr, 0x0000_8000)
	ext := make([]byte, waReportExtLen)
	for i := range ext {
		ext[i] = byte(i)
	}
	sr = append(sr, ext...)
	if len(sr) != 76 {
		t.Fatal(len(sr))
	}
	full := SourceDescription(sender, cn("00000@pj000000.org"))
	full[0] = 0x91
	s, ok := Summarize(append(bytes.Clone(sr), full[:]...))
	want := []ReportBlock{{SSRC: target, FractionLost: 0x20, CumulativeLost: -2, ExtendedHighestSequence: 0x0001_2345,
		Jitter: 77, LastSenderReport: 0x5566_7788, DelaySinceLastSenderReport: 0x8000, ProfileExtension: ext}}
	if !ok || !slices.Equal(s.PacketTypes, []uint8{PTSR, PTSDES}) || !slices.Equal(s.ReferencedSSRCs, []uint32{target}) ||
		!s.UsesWhatsAppProfileExtension || !slices.Equal(s.SDESCNameLens, []int{WhatsAppCNameLen}) || !reflect.DeepEqual(s.ReportBlocks, want) {
		t.Fatalf("%+v", s)
	}
	empty := EmptySourceDescription(sender)
	empty[0] = 0x91
	s, ok = Summarize(append(bytes.Clone(sr), empty[:]...))
	if !ok || len(s.SDESCNameLens) != 0 || !s.UsesWhatsAppProfileExtension {
		t.Fatal("empty SDES")
	}
}

func TestNativeVideoSenderReportMatchesProfileStructure(t *testing.T) {
	r := ReceptionReport{SSRC: 0x3333_4444, FractionLost: 5, CumulativeLost: 2, ExtendedHighestSequence: 91, Jitter: 7, LastSenderReport: 8, DelaySinceLastSenderReport: 9}
	c := WhatsAppSenderReportWithSDES(0x1111_2222, SenderStats{3, 400, 90_000}, 1_700_000_000_000, cn("00000@pj000000.org"), &r, true)
	if len(c) != 108 || !bytes.Equal(c[:4], []byte{0x91, PTSR, 0, 18}) || binary.BigEndian.Uint32(c[28:]) != r.SSRC ||
		!bytes.Equal(c[52:76], make([]byte, 24)) || !bytes.Equal(c[76:80], []byte{0x91, PTSDES, 0, 7}) {
		t.Fatalf("%x", c)
	}
	s, ok := Summarize(c)
	if !ok || !slices.Equal(s.ReferencedSSRCs, []uint32{r.SSRC}) || !bytes.Equal(s.ReportBlocks[0].ProfileExtension, make([]byte, 24)) || !s.UsesWhatsAppProfileExtension {
		t.Fatal("summary")
	}
}

func TestReceptionStatsTrackLossWrapLSRAndDLSR(t *testing.T) {
	var r ReceptionStats
	r.Observe(0x1234_5678, 65_534, 90_000, 1_000, 90_000)
	r.Observe(0x1234_5678, 65_535, 95_400, 1_060, 90_000)
	r.Observe(0x1234_5678, 1, 106_200, 1_180, 90_000)
	r.ObserveSenderReport(0x1234_5678, 0x0102_0304, 0x5060_7080, 1_180)
	rep, ok := r.Report(1_680)
	want := ReceptionReport{SSRC: 0x1234_5678, FractionLost: 64, CumulativeLost: 1, ExtendedHighestSequence: 0x0001_0001,
		LastSenderReport: 0x0304_5060, DelaySinceLastSenderReport: 32_768}
	if !ok || rep != want {
		t.Fatalf("%+v", rep)
	}
	next, _ := r.Report(1_680)
	if next.FractionLost != 0 || next.CumulativeLost != 1 {
		t.Fatalf("%+v", next)
	}
}

func TestParsesSenderReportNTPTimingFromCompound(t *testing.T) {
	sr := SenderReport(0x1234_5678, SenderStats{}, 1_700_000_000_250)
	ssrc, sec, frac, ok := SenderReportTiming(sr[:])
	if !ok || ssrc != 0x1234_5678 || sec != uint32(ntpUnixOffsetSecs+1_700_000_000) || frac != 1<<30 {
		t.Fatal(ssrc, sec, frac)
	}
}

func TestRTCPClassification(t *testing.T) {
	k := kat.KATs(t)
	sr := k.Hex(t, "rtcp", "senderReport")
	if pt, ok := PayloadType(sr); !IsRTCP(sr) || !ok || pt != 200 {
		t.Fatal("SR")
	}
	if IsRTCP([]byte{0x80, 0xe1, 0, 1, 0, 0, 0, 0, 0, 0, 0, 1}) || IsRTCP(k.Hex(t, "rtp", "speechHeader16")) {
		t.Fatal("RTP taken for RTCP")
	}
}

func TestSummarizesRRAndFeedbackTargets(t *testing.T) {
	sender, audio, video := uint32(0x1111_2222), uint32(0x3333_4444), uint32(0x5555_6666)
	c := []byte{0x81, PTRR, 0, 7}
	c = binary.BigEndian.AppendUint32(c, sender)
	c = binary.BigEndian.AppendUint32(c, audio)
	c = append(c, make([]byte, 20)...)
	c = append(c, 0x81, PTPSFB, 0, 2)
	c = binary.BigEndian.AppendUint32(c, sender)
	c = binary.BigEndian.AppendUint32(c, video)
	s, ok := Summarize(c)
	if !ok || !slices.Equal(s.PacketTypes, []uint8{PTRR, PTPSFB}) || s.SenderSSRC != sender ||
		!slices.Equal(s.ReferencedSSRCs, []uint32{audio, video}) ||
		!reflect.DeepEqual(s.Feedback, []Feedback{{PacketType: PTPSFB, FMT: 1, SenderSSRC: sender, MediaSSRC: video, FCI: []byte{}}}) ||
		len(s.SDESCNameLens) != 0 {
		t.Fatalf("%+v", s)
	}
}

func TestWhatsAppFeedbackProfileBitIsNotPartOfFMT(t *testing.T) {
	pli := WhatsAppPictureLossIndication(0x1111_2222, 0x5555_6666, true)
	s, ok := Summarize(pli[:])
	if !ok || !s.UsesWhatsAppProfileExtension || s.Feedback[0].FMT != 1 || !slices.Equal(s.ReferencedSSRCs, []uint32{0x5555_6666}) {
		t.Fatal("profile PLI")
	}
}

func TestPictureLossIndicationsMatchTheWireLayout(t *testing.T) {
	sender, video := uint32(0x1111_2222), uint32(0x5555_6666)
	plain := PictureLossIndication(sender, video)
	if !bytes.Equal(plain[:4], []byte{0x81, PTPSFB, 0, 2}) || binary.BigEndian.Uint32(plain[4:]) != sender || binary.BigEndian.Uint32(plain[8:]) != video {
		t.Fatal("layout")
	}
	if WhatsAppPictureLossIndication(sender, video, false) != plain {
		t.Fatal("off-profile spelling")
	}
	profile := WhatsAppPictureLossIndication(sender, video, true)
	if profile[0] != 0x91 || !bytes.Equal(profile[1:], plain[1:]) {
		t.Fatal("profile bit")
	}
	for _, p := range [][12]byte{plain, profile} {
		s, ok := Summarize(p[:])
		if !ok || s.Feedback[0].FMT != 1 || s.Feedback[0].MediaSSRC != video || len(s.Feedback[0].FCI) != 0 {
			t.Fatal("parse")
		}
	}
	if s, _ := Summarize(plain[:]); s.UsesWhatsAppProfileExtension {
		t.Fatal("plain flagged")
	}
	var p pionrtcp.PictureLossIndication
	if err := p.Unmarshal(plain[:]); err != nil || p.MediaSSRC != video || p.SenderSSRC != sender {
		t.Fatal("pion", err)
	}
}

func TestSummarizesFIRTargetFromFeedbackControlInformation(t *testing.T) {
	sender, video := uint32(0x1111_2222), uint32(0x5555_6666)
	fir := []byte{0x84, PTPSFB, 0, 4}
	fir = binary.BigEndian.AppendUint32(fir, sender)
	fir = binary.BigEndian.AppendUint32(fir, 0)
	fir = binary.BigEndian.AppendUint32(fir, video)
	fir = append(fir, 7, 0, 0, 0)
	s, ok := Summarize(fir)
	if !ok || !slices.Equal(s.ReferencedSSRCs, []uint32{video}) || s.Feedback[0].FMT != 4 || s.Feedback[0].MediaSSRC != 0 ||
		!bytes.Equal(s.Feedback[0].FCI, append(binary.BigEndian.AppendUint32(nil, video), 7, 0, 0, 0)) {
		t.Fatalf("%+v", s)
	}
}

func TestMalformedCompoundRTCPIsRejected(t *testing.T) {
	if _, ok := Summarize([]byte{0x80, PTRR, 0, 7, 0, 0, 0, 1}); ok {
		t.Fatal("accepted")
	}
}

func TestMalformedSDESIsRejected(t *testing.T) {
	sdes := SourceDescription(1, cn("00000@pj000000.org"))
	sdes[len(sdes)-1] = 1
	if _, ok := Summarize(sdes[:]); ok {
		t.Fatal("accepted")
	}
}
