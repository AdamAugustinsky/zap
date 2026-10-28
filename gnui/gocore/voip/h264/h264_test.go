// Ported from whatsapp-rust 5e72baf wacore/src/voip/h264.rs, MIT.

package h264

import (
	"bytes"
	"encoding/hex"
	"os"
	"path/filepath"
	"slices"
	"testing"

	"zapfast/gocore/voip/internal/kat"
)

func nal(t uint8, n int) []byte {
	out := []byte{0x60 | t}
	for i := range max(n-1, 0) {
		out = append(out, byte(i%251))
	}
	return out
}

func auFromNALs(nals ...[]byte) []byte {
	var au []byte
	for _, n := range nals {
		au = append(au, startCode[:]...)
		au = append(au, n...)
	}
	return au
}

func concat(parts ...[]byte) []byte {
	var out []byte
	for _, p := range parts {
		out = append(out, p...)
	}
	return out
}

func payloadList(p *PacketizedAU) [][]byte {
	var out [][]byte
	for x := range p.All() {
		out = append(out, bytes.Clone(x))
	}
	return out
}

// depacketizeAll drives a depacketizer with consecutive sequence numbers
// and one shared timestamp, returning the last AU produced.
func depacketizeAll(payloads [][]byte) []byte {
	var d Depacketizer
	var au []byte
	for i, p := range payloads {
		if _, got, ok := d.Push(uint16(i), 9000, p, i == len(payloads)-1); ok {
			au = got
		}
	}
	return au
}

func types(payloads [][]byte) []uint8 {
	var out []uint8
	for _, p := range payloads {
		out = append(out, NALUnitType(p))
	}
	return out
}

func nalTypes(au []byte) []uint8 {
	var out []uint8
	for n := range SplitAnnexB(au) {
		out = append(out, NALUnitType(n))
	}
	return out
}

func nals(au []byte) [][]byte { return slices.Collect(SplitAnnexB(au)) }

func packetized(au []byte) [][]byte {
	var p PacketizedAU
	Packetize(au, &p)
	return payloadList(&p)
}

func eqAU(t *testing.T, got, want []byte, msg string) {
	t.Helper()
	if !bytes.Equal(got, want) {
		t.Fatalf("%s:\n got %x\nwant %x", msg, got, want)
	}
}

func eqTypes(t *testing.T, got []uint8, want ...uint8) {
	t.Helper()
	if !slices.Equal(got, want) {
		t.Fatalf("types %v, want %v", got, want)
	}
}

func TestSplitAnnexbHandles3And4ByteStartCodes(t *testing.T) {
	data := concat([]byte{0, 0, 1}, nal(1, 4), startCode[:], nal(5, 6))
	got := nals(data)
	if len(got) != 2 || NALUnitType(got[0]) != 1 || NALUnitType(got[1]) != 5 {
		t.Fatalf("got %x", got)
	}
}

func TestSplitAnnexbSkipsEmptyNalsAndGarbagePrefix(t *testing.T) {
	data := concat([]byte{0xaa, 0xbb}, startCode[:], startCode[:], nal(1, 3))
	if n := len(nals(data)); n != 1 {
		t.Fatalf("got %d NALs", n)
	}
	if len(nals(nil)) != 0 || len(nals([]byte{0, 0})) != 0 {
		t.Fatal("empty input yields NALs")
	}
}

func TestKeyframeDetection(t *testing.T) {
	if !AUIsKeyframe(auFromNALs(nal(7, 4), nal(8, 4), nal(5, 100))) {
		t.Fatal("SPS/PPS/IDR")
	}
	if !AUIsKeyframe(auFromNALs(nal(9, 2), nal(5, 10))) {
		t.Fatal("AUD/IDR")
	}
	if AUIsKeyframe(auFromNALs(nal(9, 2), nal(1, 100))) {
		t.Fatal("delta")
	}
	if AUIsKeyframe(nil) {
		t.Fatal("empty")
	}
}

func TestAnOutlierAccessUnitDoesNotPinItsBufferForTheCall(t *testing.T) {
	var p PacketizedAU
	big := auFromNALs(nal(5, 512*1024))
	Packetize(big, &p)
	if cap(p.data) <= packetizedRetainedBytes {
		t.Fatal("the large AU should have grown the buffer past the retained cap")
	}
	small := auFromNALs(nal(1, 50))
	Packetize(small, &p)
	if cap(p.data) > packetizedRetainedBytes {
		t.Fatal("an outlier's capacity must not survive the next frame")
	}
	eqAU(t, depacketizeAll(payloadList(&p)), small, "small AU")
	steady := cap(p.data)
	for range 4 {
		Packetize(auFromNALs(nal(1, 400)), &p)
		if cap(p.data) != steady {
			t.Fatal("an ordinary frame must not reallocate")
		}
	}
	Packetize(big, &p)
	large := cap(p.data)
	Packetize(big, &p)
	if cap(p.data) != large {
		t.Fatal("consecutive large frames must not thrash the allocator")
	}
}

func TestSingleNalRoundTrips(t *testing.T) {
	au := auFromNALs(nal(1, 100))
	p := packetized(au)
	if len(p) != 1 || len(p[0]) > SingleNALMax {
		t.Fatalf("payloads %d", len(p))
	}
	eqAU(t, depacketizeAll(p), au, "round trip")
}

func TestWhatsappPacketizationOmitsAudBeforeParameterSets(t *testing.T) {
	sps, pps, idr := nal(7, 20), nal(8, 8), nal(5, 100)
	p := packetized(auFromNALs(nal(9, 2), sps, pps, idr))
	eqTypes(t, types(p), nalSTAPA, nalIDR)
	eqAU(t, depacketizeAll(p), auFromNALs(sps, pps, idr), "reassembled")
}

func TestParameterSetsAggregateIntoStapA(t *testing.T) {
	sps, pps, idr := nal(7, 20), nal(8, 8), nal(5, 100)
	p := packetized(auFromNALs(nal(9, 2), sps, pps, idr))
	if len(p) != 2 {
		t.Fatalf("STAP-A(SPS,PPS) plus the IDR slice, got %d", len(p))
	}
	if NALUnitType(p[0]) != nalSTAPA {
		t.Fatal("first payload is not STAP-A")
	}
	rest := p[0][1:]
	for _, want := range [][]byte{sps, pps} {
		n := int(rest[0])<<8 | int(rest[1])
		eqAU(t, rest[2:2+n], want, "STAP-A member")
		rest = rest[2+n:]
	}
	if len(rest) != 0 {
		t.Fatal("STAP-A holds exactly SPS then PPS")
	}
	if NALUnitType(p[1]) != nalIDR {
		t.Fatal("second payload is not IDR")
	}
	eqAU(t, depacketizeAll(p), auFromNALs(sps, pps, idr), "reassembled")
}

func TestStapAOverflowOpensANewAggregate(t *testing.T) {
	p := packetized(auFromNALs(nal(7, 400), nal(8, 400), nal(5, 60)))
	eqTypes(t, types(p), nalSTAPA, nalSTAPA, nalIDR)
	eqTypes(t, nalTypes(depacketizeAll(p)), 7, 8, 5)
}

func TestSmallSlicesAreNeverAggregated(t *testing.T) {
	au := auFromNALs(nal(1, 40), nal(1, 50))
	p := packetized(au)
	eqTypes(t, types(p), 1, 1)
	eqAU(t, depacketizeAll(p), au, "round trip")
}

func TestProductionMultisliceIdrKeepsSlicePackets(t *testing.T) {
	au := auFromNALs(nal(7, 18), nal(8, 4), nal(5, 900), nal(5, 1200), nal(5, 800), nal(5, 950))
	p := packetized(au)
	ty := types(p)
	if ty[0] != nalSTAPA {
		t.Fatalf("params aggregate, got %v", ty)
	}
	for _, x := range ty[1:] {
		if x != nalFUA && x != nalIDR {
			t.Fatalf("slices never join the STAP-A, got %v", ty)
		}
	}
	eqTypes(t, nalTypes(depacketizeAll(p)), 7, 8, 5, 5, 5, 5)
}

func TestProductionParameterSetsRoundTripByteIdentical(t *testing.T) {
	sps, _ := hex.DecodeString("6742c01f8c6805005ba6a0202020f08846a0")
	pps, _ := hex.DecodeString("68ce3c80")
	if NALUnitType(sps) != nalSPS || NALUnitType(pps) != nalPPS {
		t.Fatal("types")
	}
	p := packetized(auFromNALs(sps, pps, nal(5, 60)))
	if NALUnitType(p[0]) != nalSTAPA {
		t.Fatal("not STAP-A")
	}
	got := nals(depacketizeAll(p))
	eqAU(t, got[0], sps, "SPS bytes intact")
	eqAU(t, got[1], pps, "PPS bytes intact")
}

func TestSmallPeerKeyframeRoundTrips(t *testing.T) {
	au := auFromNALs(nal(7, 14), nal(8, 4), nal(5, 200))
	p := packetized(au)
	eqTypes(t, types(p), nalSTAPA, nalIDR)
	eqAU(t, depacketizeAll(p), au, "round trip")
}

func TestAudOnlyAccessUnitProducesNoRtpPayload(t *testing.T) {
	var p PacketizedAU
	Packetize(auFromNALs(nal(1, 40)), &p)
	Packetize(auFromNALs(nal(9, 2)), &p)
	if p.Len() != 0 {
		t.Fatal("payloads left")
	}
}

func TestBoundary800StaysSingleAnd801Fragments(t *testing.T) {
	var p PacketizedAU
	Packetize(auFromNALs(nal(1, SingleNALMax)), &p)
	if p.Len() != 1 {
		t.Fatal("800-byte NAL must not fragment")
	}
	au := auFromNALs(nal(1, SingleNALMax+1))
	Packetize(au, &p)
	if p.Len() != 2 {
		t.Fatal("801-byte NAL must fragment")
	}
	if NALUnitType(p.At(0)) != nalFUA || p.At(0)[1]&0x80 != 0x80 || p.At(1)[1]&0x40 != 0x40 {
		t.Fatal("FU-A S/E bits")
	}
	eqAU(t, depacketizeAll(payloadList(&p)), au, "round trip")
}

func TestLargeIdrRoundTripsViaFua(t *testing.T) {
	au := auFromNALs(nal(7, 20), nal(8, 8), nal(5, 3000))
	p := packetized(au)
	if len(p) < 5 {
		t.Fatalf("payloads %d", len(p))
	}
	got := depacketizeAll(p)
	eqAU(t, got, au, "AU must reassemble")
	if !AUIsKeyframe(got) {
		t.Fatal("keyframe")
	}
}

func TestStapAUnpacksBothNals(t *testing.T) {
	a, b := nal(7, 5), nal(8, 3)
	stap := []byte{0x60 | nalSTAPA}
	for _, n := range [][]byte{a, b} {
		stap = append(stap, byte(len(n)>>8), byte(len(n)))
		stap = append(stap, n...)
	}
	eqAU(t, depacketizeAll([][]byte{stap}), auFromNALs(a, b), "STAP-A must unpack")
}

func TestLostMiddleFragmentDropsTheTruncatedNal(t *testing.T) {
	p := packetized(auFromNALs(nal(5, 3000)))
	if len(p) < 3 {
		t.Fatal("payloads")
	}
	var d Depacketizer
	var seq uint16
	for i, x := range p {
		if i == 1 {
			seq++
			continue
		}
		marker := i == len(p)-1
		_, _, ok := d.Push(seq, 7000, x, marker)
		if marker && ok {
			t.Fatal("a NAL with a lost interior fragment must be dropped, not emitted truncated")
		}
		seq++
	}
	au2 := auFromNALs(nal(1, 50))
	eqAU(t, depacketizeAll(packetized(au2)), au2, "next AU")
}

func TestReorderedFuFragmentDropsThePartial(t *testing.T) {
	p := packetized(auFromNALs(nal(5, 2500)))
	var d Depacketizer
	if _, _, ok := d.Push(0, 7000, p[0], false); ok {
		t.Fatal("start")
	}
	if _, _, ok := d.Push(5, 7000, p[1], false); ok {
		t.Fatal("a gapped fragment must not extend the partial NAL")
	}
}

func TestTimestampChangeClearsIncompleteFuBeforeMiddleFragment(t *testing.T) {
	p := packetized(auFromNALs(nal(5, 2500)))
	if len(p) < 3 {
		t.Fatal("payloads")
	}
	var d Depacketizer
	if _, _, ok := d.Push(0, 1000, p[0], false); ok {
		t.Fatal("0")
	}
	if _, _, ok := d.Push(1, 2000, p[1], false); ok {
		t.Fatal("1")
	}
	if _, _, ok := d.Push(2, 2000, p[2], true); ok {
		t.Fatal("2")
	}
	next := nal(1, 40)
	ts, au, ok := d.Push(3, 3000, next, true)
	if !ok || ts != 3000 {
		t.Fatalf("got %d %v", ts, ok)
	}
	eqAU(t, au, auFromNALs(next), "next")
}

func TestLostEndFragmentDiscardsPartialAndKeepsNextNal(t *testing.T) {
	p := packetized(auFromNALs(nal(5, 2000)))
	var d Depacketizer
	for i, x := range p[:len(p)-1] {
		if _, _, ok := d.Push(uint16(i), 7000, x, false); ok {
			t.Fatal("early AU")
		}
	}
	tail := nal(1, 40)
	ts, au, ok := d.Push(100, 13000, tail, true)
	if !ok || ts != 13000 {
		t.Fatal("fresh NAL must flush")
	}
	eqAU(t, au, auFromNALs(tail), "tail")
}

func TestDepacketizerSurvivesGarbage(t *testing.T) {
	bufs := [][]byte{
		{},
		{0x7c},
		{0x7c, 0x00},
		{0x7c, 0xc0},
		{0x78, 0x00, 0xff, 0xaa},
		{0x78},
		{0x1f, 0x1f, 0x1f},
		make([]byte, 8),
		bytes.Repeat([]byte{0xff}, 900),
	}
	var d Depacketizer
	for i, b := range bufs {
		d.Push(uint16(i), uint32(i), b, false)
		d.Push(uint16(i), uint32(i), b, true)
	}
	au := auFromNALs(nal(1, 10))
	eqAU(t, depacketizeAll(packetized(au)), au, "still functional")
}

func TestLostMarkerDoesNotMergeFramesAcrossATimestampChange(t *testing.T) {
	var d Depacketizer
	a1 := nal(1, 40)
	if _, _, ok := d.Push(0, 1000, a1, false); ok {
		t.Fatal("a1")
	}
	b1 := nal(1, 50)
	ts, au, ok := d.Push(2, 2000, b1, false)
	if !ok || ts != 1000 {
		t.Fatal("a new timestamp must flush the previous AU whose marker was lost")
	}
	eqAU(t, au, auFromNALs(a1), "AU1")
	b2 := nal(5, 30)
	ts, au, ok = d.Push(3, 2000, b2, true)
	if !ok || ts != 2000 {
		t.Fatal("AU2 completes on its marker")
	}
	eqAU(t, au, auFromNALs(b1, b2), "AU2")
}

func TestReorderedOlderTimestampPacketIsDiscardedNotFlushed(t *testing.T) {
	var d Depacketizer
	a1 := nal(1, 20)
	ts, au, ok := d.Push(0, 1000, a1, true)
	if !ok || ts != 1000 {
		t.Fatal("AU1")
	}
	eqAU(t, au, auFromNALs(a1), "AU1")
	b1 := nal(1, 30)
	if _, _, ok := d.Push(1, 2000, b1, false); ok {
		t.Fatal("b1")
	}
	if _, _, ok := d.Push(2, 1000, nal(1, 10), false); ok {
		t.Fatal("a stale reordered packet must not flush the in-progress AU")
	}
	b2 := nal(5, 25)
	ts, au, ok = d.Push(3, 2000, b2, true)
	if !ok || ts != 2000 {
		t.Fatal("AU2")
	}
	eqAU(t, au, auFromNALs(b1, b2), "the in-progress AU survives the reordered packet")
}

func TestLatePacketAfterMarkerCannotSeedAStaleAu(t *testing.T) {
	var d Depacketizer
	completed := nal(5, 20)
	ts, au, ok := d.Push(10, 1000, completed, true)
	if !ok || ts != 1000 {
		t.Fatal("completed")
	}
	eqAU(t, au, auFromNALs(completed), "completed")
	if _, _, ok := d.Push(9, 1000, nal(1, 15), false); ok {
		t.Fatal("late")
	}
	next := nal(1, 25)
	ts, au, ok = d.Push(11, 2000, next, true)
	if !ok || ts != 2000 {
		t.Fatal("next")
	}
	eqAU(t, au, auFromNALs(next), "a late packet from the completed timestamp must not leak into the next AU")
}

func TestBoundaryAndMarkerInOnePushSurfaceBothAus(t *testing.T) {
	var d Depacketizer
	a1 := nal(1, 20)
	if _, _, ok := d.Push(0, 1000, a1, false); ok {
		t.Fatal("a1")
	}
	b1 := nal(5, 25)
	ts, au, ok := d.Push(1, 2000, b1, true)
	if !ok || ts != 1000 {
		t.Fatal("boundary flush returns AU1")
	}
	eqAU(t, au, auFromNALs(a1), "AU1")
	ts, au, ok = d.PopReady()
	if !ok || ts != 2000 {
		t.Fatal("the second completed AU is ready without another packet")
	}
	eqAU(t, au, auFromNALs(b1), "AU2")
	if _, _, ok := d.PopReady(); ok {
		t.Fatal("extra AU")
	}
}

func TestEmptyAuYieldsNoPayloadsAndMarkerAloneYieldsNone(t *testing.T) {
	var p PacketizedAU
	Packetize(auFromNALs(nal(1, 40)), &p)
	Packetize(nil, &p)
	if p.Len() != 0 {
		t.Fatal("Packetize must clear stale output")
	}
	var d Depacketizer
	if _, _, ok := d.Push(0, 0, nil, true); ok {
		t.Fatal("marker alone")
	}
}

func splitAll(s *AnnexBAUSplitter, data []byte) [][]byte { return s.Push(data) }

func eqAUs(t *testing.T, got [][]byte, want ...[]byte) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("got %d AUs, want %d", len(got), len(want))
	}
	for i := range got {
		eqAU(t, got[i], want[i], "AU")
	}
}

func finish(t *testing.T, s *AnnexBAUSplitter, want []byte) {
	t.Helper()
	got, ok := s.Finish()
	if want == nil {
		if ok {
			t.Fatalf("Finish returned %x", got)
		}
		return
	}
	if !ok {
		t.Fatal("Finish returned nothing")
	}
	eqAU(t, got, want, "Finish")
}

func TestAuSplitterCutsOnSpsWithoutAud(t *testing.T) {
	group := func(id byte) []byte {
		au := auFromNALs(nal(7, 4), nal(8, 4), nal(5, 60))
		au[6] = id
		return au
	}
	delta := auFromNALs(nal(1, 40))
	stream := concat(group(1), delta, group(2))
	var s AnnexBAUSplitter
	eqAUs(t, splitAll(&s, stream), concat(group(1), delta))
	finish(t, &s, group(2))
}

func TestAuSplitterCutsOnIdrAfterACompleteGroup(t *testing.T) {
	idr := func(n int) []byte {
		out := []byte{0x65, 0x80}
		for i := range n - 2 {
			out = append(out, byte(i%251))
		}
		return out
	}
	group := auFromNALs(nal(7, 4), nal(8, 4), idr(60))
	delta := auFromNALs(nal(1, 40))
	lone := auFromNALs(idr(60))
	var s AnnexBAUSplitter
	eqAUs(t, splitAll(&s, concat(group, delta, lone)), concat(group, delta))
	finish(t, &s, lone)
}

func TestAuSplitterCutsOnAud(t *testing.T) {
	au1 := auFromNALs(nal(9, 2), nal(7, 4), nal(5, 60))
	au2 := auFromNALs(nal(9, 2), nal(1, 40))
	var s AnnexBAUSplitter
	eqAUs(t, splitAll(&s, concat(au1, au2)), au1)
	finish(t, &s, au2)
	finish(t, &s, nil)
}

func TestAuSplitterHandlesStartCodeSplitAcrossChunks(t *testing.T) {
	au1 := auFromNALs(nal(9, 2), nal(1, 30))
	au2 := auFromNALs(nal(9, 2), nal(1, 20))
	var s AnnexBAUSplitter
	var out [][]byte
	for _, b := range concat(au1, au2) {
		out = append(out, s.Push([]byte{b})...)
	}
	eqAUs(t, out, au1)
	finish(t, &s, au2)
}

func TestAuSplitterWithoutAudDoesNotGrowUnbounded(t *testing.T) {
	var s AnnexBAUSplitter
	chunk := auFromNALs(nal(1, 64*1024))
	for range 80 {
		if len(s.Push(chunk)) != 0 {
			t.Fatal("emitted")
		}
	}
	if len(s.buf) > MaxAUBytes {
		t.Fatalf("buffer %d", len(s.buf))
	}
}

func TestAuSplitterPureGarbageNoStartCodeIsCapped(t *testing.T) {
	var s AnnexBAUSplitter
	chunk := bytes.Repeat([]byte{0xab}, 64*1024)
	for range 80 {
		if len(s.Push(chunk)) != 0 {
			t.Fatal("emitted")
		}
	}
	if len(s.buf) > MaxAUBytes {
		t.Fatalf("no-start-code stream must be capped, got %d", len(s.buf))
	}
}

func TestFirstMbInSliceReadsExpGolomb(t *testing.T) {
	check := func(nal []byte, want uint32, wantOK bool) {
		t.Helper()
		got, ok := firstMBInSlice(nal)
		if ok != wantOK || got != want {
			t.Fatalf("%x: got %d %v", nal, got, ok)
		}
	}
	check([]byte{0x41, 0x80}, 0, true)
	check([]byte{0x41, 0x40}, 1, true)
	a, aok := firstMBInSlice([]byte{0x41, 0x00, 0x00, 0x80})
	b, bok := firstMBInSlice([]byte{0x41, 0x00, 0x00, 0x03, 0x80})
	if a != b || aok != bok {
		t.Fatal("emulation prevention byte must be skipped")
	}
	check([]byte{0x41}, 0, false)
	check(nil, 0, false)
}

func pic(firstMB []byte, n int) []byte {
	out := append([]byte{0x41}, firstMB...)
	for i := range max(n-1-len(firstMB), 0) {
		out = append(out, byte(i%251))
	}
	return out
}

func vcl(firstMB []byte) []byte {
	out := append([]byte{0x41}, firstMB...)
	for i := range 30 {
		out = append(out, byte(i%251))
	}
	return out
}

func TestAuSplitterCutsOnVclPictureStart(t *testing.T) {
	au1 := auFromNALs(pic([]byte{0x80}, 30))
	au2 := auFromNALs(pic([]byte{0x80}, 30))
	var s AnnexBAUSplitter
	eqAUs(t, splitAll(&s, concat(au1, au2)), au1)
	finish(t, &s, au2)

	multi := auFromNALs([]byte{0x41, 0x80, 0x01}, []byte{0x41, 0x08, 0x80, 0x02})
	var s2 AnnexBAUSplitter
	if len(s2.Push(multi)) != 0 {
		t.Fatal("one picture stays one AU")
	}
	finish(t, &s2, multi)
}

func TestAuSplitterRechecksPartialSliceHeader(t *testing.T) {
	au1 := auFromNALs(pic([]byte{0x80}, 30))
	au2 := auFromNALs(pic([]byte{0x80}, 30))
	stream := concat(au1, au2)
	cut := len(au1) + 3 + 1
	var s AnnexBAUSplitter
	if len(s.Push(stream[:cut])) != 0 {
		t.Fatal("nothing decidable yet")
	}
	eqAUs(t, s.Push(stream[cut:]), au1)
	finish(t, &s, au2)
}

func TestAuSplitterCorruptSliceHeaderIsCapped(t *testing.T) {
	corrupt := []byte{0x41, 0x00, 0x00, 0x00, 0x00, 0x40}
	if _, ok := firstMBInSlice(corrupt); ok {
		t.Fatal("corrupt header decoded")
	}
	var s AnnexBAUSplitter
	if len(s.Push(concat(startCode[:], corrupt))) != 0 {
		t.Fatal("emitted")
	}
	chunk := bytes.Repeat([]byte{0xab}, 64*1024)
	for range 80 {
		if len(s.Push(chunk)) != 0 {
			t.Fatal("emitted")
		}
	}
	if len(s.buf) > MaxAUBytes {
		t.Fatalf("corrupt-header stream must be capped, got %d", len(s.buf))
	}
	group := auFromNALs(nal(7, 4), nal(8, 4), nal(5, 60))
	out := append(s.Push(group), s.Push(group)...)
	if len(out) != 1 {
		t.Fatalf("emissions must resume after the cap drop, got %d", len(out))
	}
	if !bytes.HasSuffix(out[0], group) {
		t.Fatal("the cut lands on the second group's SPS")
	}
}

func TestAuSplitterLeadingSpsWithoutVclStaysTogether(t *testing.T) {
	first := concat(auFromNALs(nal(7, 4)), auFromNALs(nal(7, 4), nal(8, 4), nal(5, 60)))
	var s AnnexBAUSplitter
	if len(s.Push(first)) != 0 {
		t.Fatal("leading SPS must not split before any picture")
	}
	group := auFromNALs(nal(7, 4), nal(8, 4), nal(5, 60))
	eqAUs(t, s.Push(group), first)
	finish(t, &s, group)
}

func TestAuSplitterDataPartitionsStayWithPartitionA(t *testing.T) {
	partA := []byte{0x42, 0x80}
	partB := []byte{0x43, 0x80}
	for i := range 28 {
		partA = append(partA, byte(i%251))
		partB = append(partB, byte(i%251))
	}
	stream := auFromNALs(partA, partB)
	var s AnnexBAUSplitter
	if len(s.Push(stream)) != 0 {
		t.Fatal("partitions B/C ride with their partition A")
	}
	finish(t, &s, stream)
}

func TestProductionMultisliceIdrGroupRoundTrips(t *testing.T) {
	slice := func(first []byte) []byte {
		n := append([]byte{0x65}, first...)
		for i := range 60 {
			n = append(n, byte(i%251))
		}
		return n
	}
	stream := auFromNALs(nal(7, 4), nal(8, 4), slice([]byte{0x80}),
		slice([]byte{0x08, 0x80}), slice([]byte{0x08, 0x80}), slice([]byte{0x08, 0x80}))
	var s AnnexBAUSplitter
	if len(s.Push(stream)) != 0 {
		t.Fatal("multi-slice IDR stays one AU")
	}
	au, ok := s.Finish()
	if !ok {
		t.Fatal("trailing AU")
	}
	eqAU(t, au, stream, "AU")
	got := depacketizeAll(packetized(au))
	eqAU(t, got, au, "AU must reassemble")
	if !AUIsKeyframe(got) {
		t.Fatal("keyframe")
	}
}

func TestAuSplitterSeiRunSurvivesPrefixNals(t *testing.T) {
	first, second := vcl([]byte{0x80}), vcl([]byte{0x80})
	sei, pps := []byte{0x06, 0x05, 0x11, 0x22}, nal(8, 4)
	var s AnnexBAUSplitter
	eqAUs(t, s.Push(auFromNALs(first, sei, pps, second)), auFromNALs(first))
	finish(t, &s, auFromNALs(sei, pps, second))
}

func TestAuSplitterSeiRidesWithGroupOpener(t *testing.T) {
	idr := []byte{0x65, 0x80, 0x01, 0x02}
	first, sei, sps, pps := vcl([]byte{0x80}), []byte{0x06, 0x05, 0x11, 0x22}, nal(7, 4), nal(8, 4)
	var s AnnexBAUSplitter
	eqAUs(t, s.Push(auFromNALs(first, sei, sps, pps, idr)), auFromNALs(first))
	rest, ok := s.Finish()
	if !ok {
		t.Fatal("trailing AU")
	}
	eqAU(t, rest, auFromNALs(sei, sps, pps, idr), "rest")
	eqAU(t, depacketizeAll(packetized(rest)), auFromNALs(sps, pps, idr), "SEI dropped")
	if !AUIsKeyframe(rest) {
		t.Fatal("keyframe")
	}
}

func TestAuSplitterSeiRidesWithFollowingPicture(t *testing.T) {
	first, second := vcl([]byte{0x80}), vcl([]byte{0x80})
	sei := []byte{0x06, 0x05, 0x11, 0x22}
	var s AnnexBAUSplitter
	eqAUs(t, s.Push(auFromNALs(first, sei, second)), auFromNALs(first))
	rest, ok := s.Finish()
	if !ok {
		t.Fatal("trailing AU")
	}
	eqAU(t, rest, auFromNALs(sei, second), "rest")
	eqAU(t, depacketizeAll(packetized(rest)), auFromNALs(second), "SEI dropped")
}

func TestAuSplitterTrailingStartCodeIsCapped(t *testing.T) {
	var s AnnexBAUSplitter
	chunk := concat(bytes.Repeat([]byte{0xab}, 64*1024), startCode[:])
	for range 80 {
		if len(s.Push(chunk)) != 0 {
			t.Fatal("emitted")
		}
	}
	if len(s.buf) > MaxAUBytes {
		t.Fatalf("trailing-start-code stream must be capped, got %d", len(s.buf))
	}
}

func TestAuSplitterHandoffKeepsStartCodeOverlap(t *testing.T) {
	a, b, c := vcl([]byte{0x80}), vcl([]byte{0x80}), vcl([]byte{0x80})
	sei := []byte{0x06, 0x05, 0x11, 0x22}
	part1 := concat(auFromNALs(a, sei), startCode[:], b, []byte{0, 0})
	part2 := concat([]byte{0, 1}, c)
	var s AnnexBAUSplitter
	firstAU, secondAU := auFromNALs(a), auFromNALs(sei, b)
	out := s.Push(part1)
	eqAUs(t, out, firstAU)
	out = append(out, s.Push(part2)...)
	eqAUs(t, out, firstAU, secondAU)
	finish(t, &s, auFromNALs(c))
}

func TestAuSplitterPictureAfterSeiHandoffStillCuts(t *testing.T) {
	a, b, c := vcl([]byte{0x80}), vcl([]byte{0x80}), vcl([]byte{0x80})
	sei := []byte{0x06, 0x05, 0x11, 0x22}
	var s AnnexBAUSplitter
	eqAUs(t, s.Push(auFromNALs(a, sei, b, c)), auFromNALs(a), auFromNALs(sei, b))
	finish(t, &s, auFromNALs(c))
}

func TestAuSplitterFinishResetsFramingState(t *testing.T) {
	var s AnnexBAUSplitter
	s.Push(auFromNALs(nal(9, 2), nal(7, 4), nal(5, 60)))
	if !s.seenAUD || !s.bufHasVCL {
		t.Fatal("state before finish")
	}
	s.Finish()
	if s.seenAUD || s.bufHasVCL {
		t.Fatal("state after finish")
	}
	group := auFromNALs(nal(7, 4), nal(8, 4), nal(5, 60))
	out := append(s.Push(group), s.Push(group)...)
	eqAUs(t, out, group)
	finish(t, &s, group)
}

func TestAuSplitterRunawayResetRestoresAudlessFraming(t *testing.T) {
	var s AnnexBAUSplitter
	var out [][]byte
	out = append(out, s.Push(auFromNALs(nal(9, 2), nal(7, 4), nal(5, 60)))...)
	if !s.seenAUD {
		t.Fatal("AUD not seen")
	}
	chunk := auFromNALs(nal(1, 64*1024))
	for range 80 {
		out = append(out, s.Push(chunk)...)
	}
	group := auFromNALs(nal(7, 4), nal(8, 4), nal(5, 60))
	out = append(out, s.Push(group)...)
	out = append(out, s.Push(group)...)
	if len(out) != 2 {
		t.Fatalf("AUD-less groups must frame after the reset, got %d", len(out))
	}
	eqAU(t, out[1], group, "the cut lands on the second group's SPS")
	finish(t, &s, group)
}

func TestOutboundIdrGroupsOpenSpsPpsIdrWithConstantSps(t *testing.T) {
	sps, pps := nal(7, 24), nal(8, 8)
	groups := [][]byte{
		auFromNALs(sps, pps, nal(5, 2000)),
		auFromNALs(nal(1, 60)),
		auFromNALs(sps, pps, nal(5, 900)),
	}
	for _, au := range groups {
		got := depacketizeAll(packetized(au))
		if got == nil {
			t.Fatal("reassembled AU")
		}
		ty := nalTypes(got)
		if AUHasIDR(au) {
			if len(ty) < 3 || ty[0] != 7 || ty[1] != 8 || !slices.Contains(ty, 5) || slices.Contains(ty, 9) {
				t.Fatalf("IDR group order %v", ty)
			}
			eqAU(t, nals(got)[0], sps, "SPS bytes must stay constant across the call")
		} else {
			eqTypes(t, ty, 1)
		}
	}
}

func TestOutboundAudFirstInputStillOpensSps(t *testing.T) {
	ty := nalTypes(depacketizeAll(packetized(auFromNALs(nal(9, 2), nal(7, 24), nal(8, 8), nal(5, 100)))))
	if ty[0] != 7 {
		t.Fatalf("AUD dropped, SPS opens, got %v", ty)
	}
}

func TestOutboundSeiFirstInputGoesOutSpsFirst(t *testing.T) {
	sps := nal(7, 24)
	got := depacketizeAll(packetized(auFromNALs(nal(6, 12), sps, nal(8, 8), nal(5, 100))))
	ty := nalTypes(got)
	if ty[0] != 7 || slices.Contains(ty, 6) {
		t.Fatalf("SEI stripped, SPS opens, got %v", ty)
	}
	eqAU(t, nals(got)[0], sps, "SPS bytes intact")
}

func TestKeepSeiVariantPreservesSei(t *testing.T) {
	var p PacketizedAU
	PacketizeKeepSEI(auFromNALs(nal(6, 12), nal(7, 24), nal(8, 8), nal(5, 100)), &p)
	ty := nalTypes(depacketizeAll(payloadList(&p)))
	if ty[0] != 6 {
		t.Fatalf("keep variant preserves SEI, got %v", ty)
	}
}

func TestOutboundTrailingSeiIsPreserved(t *testing.T) {
	ty := nalTypes(depacketizeAll(packetized(auFromNALs(nal(7, 24), nal(8, 8), nal(5, 100), nal(6, 12)))))
	eqTypes(t, ty, 7, 8, 5, 6)
}

// TestPacketizeMatchesRustGolden packetizes the access units the real
// Rust packetize_au packetized (zfcalls/tests/voip_golden.rs) and compares
// the payloads byte for byte.
func TestPacketizeMatchesRustGolden(t *testing.T) {
	if _, err := os.Stat(filepath.Join(kat.Dir(), "golden.json")); err != nil {
		t.Skip("no golden.json")
	}
	vectors := kat.Golden(t).List(t, "h264")
	if len(vectors) == 0 {
		t.Fatal("no h264 vectors")
	}
	for i, v := range vectors {
		got := packetized(v.Hex(t, "au"))
		want := v.List(t, "payloads")
		if len(got) != len(want) {
			t.Fatalf("vector %d: %d payloads, Rust %d", i, len(got), len(want))
		}
		for j := range want {
			if w := want[j].Hex(t); !bytes.Equal(got[j], w) {
				t.Fatalf("vector %d payload %d:\n  go %x\nrust %x", i, j, got[j], w)
			}
		}
	}
}
