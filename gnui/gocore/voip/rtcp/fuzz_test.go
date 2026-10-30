package rtcp

// Native fuzz targets for the RTCP readers a received (decrypted) compound
// packet goes through, and round trips through the builders.
//
//	CGO_ENABLED=0 go test ./gocore/voip/rtcp -run '^$' -fuzz '^FuzzSummarize$' -fuzztime 45s

import (
	"encoding/binary"
	"encoding/hex"
	"runtime"
	"slices"
	"testing"

	"zapfast/gocore/voip/internal/kat"
)

// fuzzGoldenHex is every hex string under the named golden.json sections.
func fuzzGoldenHex(f *testing.F, sections ...string) [][]byte {
	var out [][]byte
	var walk func(v any)
	walk = func(v any) {
		switch v := v.(type) {
		case string:
			if b, err := hex.DecodeString(v); err == nil && len(b) > 0 && len(b) <= 4096 {
				out = append(out, b)
			}
		case []any:
			for _, e := range v {
				walk(e)
			}
		case map[string]any:
			for _, e := range v {
				walk(e)
			}
		}
	}
	g := kat.Golden(f)
	for _, s := range sections {
		walk(g.Get(f, s))
	}
	return out
}

// fuzzHeapBytes is how many bytes one call of fn allocates, averaged over
// eight calls: runtime.MemStats counts the whole process, and a fuzz
// worker's own goroutines allocate a few KiB now and then, so a single call
// is not a reliable measurement. Callers allow a fixed slack on top of
// their bound for the same reason.
func fuzzHeapBytes(fn func()) uint64 {
	const runs = 8
	var before, after runtime.MemStats
	runtime.ReadMemStats(&before)
	for range runs {
		fn()
	}
	runtime.ReadMemStats(&after)
	return (after.TotalAlloc - before.TotalAlloc) / runs
}

// FuzzSummarize parses an arbitrary compound packet. Properties: an
// accepted packet's sender is the first packet's SSRC; there are no more
// packets than 8-byte headers fit, no more report blocks than 24-byte
// blocks fit, and the copied FCI and report extensions add up to no more
// than the input; the referenced SSRCs are sorted and unique; parsing is
// deterministic and allocates within a small multiple of the input; and
// SenderReportTiming finds the SR Summarize saw, with the same sender.
func FuzzSummarize(f *testing.F) {
	for _, b := range fuzzGoldenHex(f, "rtcp", "srtcp") {
		f.Add(b)
	}
	pli := PictureLossIndication(1, 2)
	c208 := Compact208(3, 4)
	f.Add(append(pli[:], c208[:]...))
	f.Add(WhatsAppSenderReportWithSDES(5, SenderStats{1, 2, 3}, 1_700_000_000_000, WhatsAppCName([12]byte{}), &ReceptionReport{SSRC: 6}, true))
	f.Fuzz(func(t *testing.T, data []byte) {
		var s Summary
		var ok bool
		allocated := fuzzHeapBytes(func() { s, ok = Summarize(data) })
		if limit := uint64(8*len(data)) + 16<<10; allocated > limit {
			t.Fatalf("summarizing %d bytes allocated %d", len(data), allocated)
		}
		again, ok2 := Summarize(data)
		if ok != ok2 || !slices.Equal(s.PacketTypes, again.PacketTypes) || !slices.Equal(s.ReferencedSSRCs, again.ReferencedSSRCs) ||
			len(s.ReportBlocks) != len(again.ReportBlocks) || len(s.Feedback) != len(again.Feedback) {
			t.Fatalf("Summarize is not deterministic")
		}
		if !ok {
			return
		}
		if !IsRTCP(data) {
			t.Fatalf("accepted a compound whose first packet is not RTCP")
		}
		if ssrc, _ := SenderSSRC(data); s.SenderSSRC != ssrc {
			t.Fatalf("sender %d, first packet says %d", s.SenderSSRC, ssrc)
		}
		if pt, _ := PayloadType(data); len(s.PacketTypes) == 0 || s.PacketTypes[0] != pt {
			t.Fatalf("packet types %v, first is %d", s.PacketTypes, pt)
		}
		if len(s.PacketTypes) > len(data)/HeaderLen || len(s.ReportBlocks) > len(data)/24 {
			t.Fatalf("%d packets and %d blocks in %d bytes", len(s.PacketTypes), len(s.ReportBlocks), len(data))
		}
		copied := 0
		for _, b := range s.ReportBlocks {
			copied += len(b.ProfileExtension)
		}
		for _, fb := range s.Feedback {
			copied += len(fb.FCI)
		}
		if copied > len(data) {
			t.Fatalf("%d bytes copied out of %d", copied, len(data))
		}
		for i := 1; i < len(s.ReferencedSSRCs); i++ {
			if s.ReferencedSSRCs[i-1] >= s.ReferencedSSRCs[i] {
				t.Fatalf("referenced SSRCs %v", s.ReferencedSSRCs)
			}
		}
		for _, n := range s.SDESCNameLens {
			if n < 0 || n > 255 {
				t.Fatalf("CNAME length %d", n)
			}
		}
		if slices.Contains(s.PacketTypes, PTSR) {
			ssrc, _, _, tok := SenderReportTiming(data)
			if !tok {
				t.Fatalf("Summarize saw an SR that SenderReportTiming cannot find")
			}
			// The first SR's sender: find it the way Summarize walks.
			for off := 0; off < len(data); off += (int(binary.BigEndian.Uint16(data[off+2:])) + 1) * 4 {
				if data[off+1] == PTSR {
					if want := binary.BigEndian.Uint32(data[off+4:]); ssrc != want {
						t.Fatalf("SR sender %d, want %d", ssrc, want)
					}
					break
				}
			}
		}
	})
}

// FuzzCompoundRoundTrip builds the compounds this client sends from
// arbitrary fields and reads them back: the sender, every report block
// field (cumulative loss as its 24-bit wire value), the CNAME length, the
// PLI target and the compact report's remote SSRC, and the SR timing.
func FuzzCompoundRoundTrip(f *testing.F) {
	f.Add(uint32(0xa1b2c3d4), uint32(0x01020304), uint64(1_700_000_000_123), []byte{1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12, 13, 14, 15, 16, 17, 18, 19, 20, 21, 22, 23, 24}, false)
	f.Add(uint32(1), uint32(2), uint64(0), []byte{}, true)
	f.Add(uint32(7), uint32(0), uint64(1<<40), make([]byte, 31*24+5), true)
	f.Fuzz(func(t *testing.T, local, remote uint32, nowMS uint64, raw []byte, native bool) {
		var reports []ReceptionReport
		for ; len(raw) >= 24; raw = raw[24:] {
			lost := int32(binary.BigEndian.Uint32(raw[4:])<<8) >> 8 // what 24 bits carry
			reports = append(reports, ReceptionReport{
				SSRC: binary.BigEndian.Uint32(raw), FractionLost: raw[8], CumulativeLost: lost,
				ExtendedHighestSequence: binary.BigEndian.Uint32(raw[8:]), Jitter: binary.BigEndian.Uint32(raw[12:]),
				LastSenderReport: binary.BigEndian.Uint32(raw[16:]), DelaySinceLastSenderReport: binary.BigEndian.Uint32(raw[20:]),
			})
		}
		cname := WhatsAppCName([12]byte{byte(local), byte(remote)})
		stats := SenderStats{PacketsSent: local, OctetsSent: remote, RTPTimestamp: local ^ remote}
		var packet []byte
		sent := reports
		if native {
			var r *ReceptionReport
			if len(reports) > 0 {
				r = &reports[0]
			}
			sent = reports[:min(len(reports), 1)]
			packet = WhatsAppSenderReportWithSDES(local, stats, nowMS, cname, r, true)
		} else {
			sent = reports[:min(len(reports), 31)]
			packet = SenderReportWithSDESAndReports(local, stats, nowMS, cname, reports)
		}
		pli := WhatsAppPictureLossIndication(local, remote, native)
		c208 := Compact208(local, remote)
		packet = append(append(packet, pli[:]...), c208[:]...)

		s, ok := Summarize(packet)
		if !ok {
			t.Fatalf("Summarize refused %x", packet)
		}
		if s.SenderSSRC != local || !slices.Equal(s.PacketTypes, []uint8{PTSR, PTSDES, PTPSFB, PTWACompact}) {
			t.Fatalf("sender %d, types %v", s.SenderSSRC, s.PacketTypes)
		}
		if s.UsesWhatsAppProfileExtension != native {
			t.Fatalf("profile extension %v", s.UsesWhatsAppProfileExtension)
		}
		if len(s.ReportBlocks) != len(sent) {
			t.Fatalf("%d report blocks back, sent %d", len(s.ReportBlocks), len(sent))
		}
		for i, b := range s.ReportBlocks {
			r := sent[i]
			if b.SSRC != r.SSRC || b.FractionLost != r.FractionLost || b.CumulativeLost != r.CumulativeLost ||
				b.ExtendedHighestSequence != r.ExtendedHighestSequence || b.Jitter != r.Jitter ||
				b.LastSenderReport != r.LastSenderReport || b.DelaySinceLastSenderReport != r.DelaySinceLastSenderReport {
				t.Fatalf("block %d: %+v, sent %+v", i, b, r)
			}
			if native && len(b.ProfileExtension) != waReportExtLen {
				t.Fatalf("native block carries %d extension bytes", len(b.ProfileExtension))
			}
		}
		if !slices.Equal(s.SDESCNameLens, []int{WhatsAppCNameLen}) {
			t.Fatalf("CNAME lengths %v", s.SDESCNameLens)
		}
		if len(s.Feedback) != 1 || s.Feedback[0].MediaSSRC != remote || s.Feedback[0].FMT != 1 {
			t.Fatalf("feedback %+v", s.Feedback)
		}
		if remote != 0 && !slices.Contains(s.ReferencedSSRCs, remote) {
			t.Fatalf("referenced %v lacks %d", s.ReferencedSSRCs, remote)
		}
		if ssrc, sec, frac, ok := SenderReportTiming(packet); !ok || ssrc != local ||
			sec != binary.BigEndian.Uint32(packet[8:]) || frac != binary.BigEndian.Uint32(packet[12:]) {
			t.Fatalf("timing %d %d %d %v", ssrc, sec, frac, ok)
		}
	})
}
