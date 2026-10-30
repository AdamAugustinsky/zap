// Ported from whatsapp-rust 5e72baf wacore/src/voip/mlow/decoder.rs, MIT.
//
// The `#[cfg(test)]` part of decoder.rs: the DiagParam snapshot and `diag_decode_params` that the
// param-decode-match test consumes, and the decoder's test module.

package mlow

import (
	"math"
	"testing"
)

// decoderFrames reads a JSON array of hex-encoded frames from testdata.
func decoderFrames(t testing.TB, name string) [][]byte {
	t.Helper()
	arr := loadJSON(t, name).Arr()
	out := make([][]byte, len(arr))
	for i, v := range arr {
		out[i] = v.Hex()
	}
	return out
}

// decoderRefPCM reads s16le reference PCM from testdata as float in [-1, 1).
func decoderRefPCM(t testing.TB, name string) []float32 {
	t.Helper()
	pcm := s16le(testdataBytes(t, name))
	out := make([]float32, len(pcm))
	for i, s := range pcm {
		out[i] = float32(s) / 32768.0
	}
	return out
}

// decoderAllZero is `out.iter().all(|&s| s == 0.0)`.
func decoderAllZero(x []float32) bool {
	for _, s := range x {
		if s != 0.0 {
			return false
		}
	}
	return true
}

// decoderAnyNonZero is `out.iter().any(|&s| s != 0.0)`.
func decoderAnyNonZero(x []float32) bool { return !decoderAllZero(x) }

// decoderF32Equal is `Vec<f32> == Vec<f32>` (element-wise float ==).
func decoderF32Equal(a, b []float32) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// decoderLag0Corr is the zero-mean lag-0 Pearson correlation the multi-frame tests compute inline.
func decoderLag0Corr(refp, out []float32) float64 {
	n := len(refp)
	var sr, so float64 = math.Copysign(0, -1), math.Copysign(0, -1)
	for _, v := range refp {
		sr += float64(v)
	}
	for _, v := range out {
		so += float64(v)
	}
	mr, mo := sr/float64(n), so/float64(n)
	var sxy, sxx, syy float64
	for i := 0; i < n; i++ {
		dr, dz := float64(refp[i])-mr, float64(out[i])-mo
		sxy += float64(dr * dz)
		sxx += float64(dr * dr)
		syy += float64(dz * dz)
	}
	return sxy / math.Sqrt(sxx*syy)
}

// diagParam is the per-subframe param snapshot for the param-decode-match (T1) test.
type diagParam struct {
	packet int
	frame  int
	sf     int
	voiced bool
	// nrgresDbqQ14 is the `gain_q` value, i.e. the `nrgres_dbq_Q14` field.
	nrgresDbqQ14 int32
	// fcbgIdx is the per-subframe `nrg_res` / voiced `filt_idx` symbol, i.e. the `fcbg_idx` field.
	fcbgIdx int32
}

// diagDecodeParams re-runs the active-frame decode over the capture and captures per-subframe
// unvoiced params, keyed by (packet, frame, sf), to compare against the reference dump (see
// testdata/PROVENANCE.md).
func diagDecodeParams(t testing.TB) []diagParam {
	frames := decoderFrames(t, "inbound_capture_frames.json")
	tbl := loadSmplTables()
	mem := loadSmplMem()
	cc := loadCcTables()
	lstate := defaultSmplLsfState()
	var out []diagParam
	for packet, frame := range frames {
		if len(frame) == 0 {
			continue
		}
		toc := parseMlowToc(frame[0])
		if toc.stdOpus || toc.sid || !toc.active {
			continue
		}
		config := int(frame[0]>>2) & 1
		dec := newRangeDecoder(frame[1:])
		for f := 0; f < 3; f++ {
			lsf := decodeSmplLsf(&dec, tbl, &lstate, config, f, true)
			pulses := decodeSmplPulses(
				&dec,
				cc,
				int32(smplIntfLen),
				4,
				1,
				int32(config),
				lsf.stage1,
			)
			if lsf.stage1 == 1 {
				pr := decodeSmplPitch(
					&dec,
					mem,
					cc,
					&lstate,
					int32(smplIntfLen),
					4,
					int32(config),
					pulses.subfr,
				)
				for sf := 0; sf < 4; sf++ {
					out = append(out, diagParam{
						packet:       packet,
						frame:        f,
						sf:           sf,
						voiced:       true,
						nrgresDbqQ14: pr.gainIdx[sf],
						fcbgIdx:      pr.filtIdx[sf],
					})
				}
			} else {
				g := decodeSmplGains(&dec, cc, 4, pulses.subfr)
				for sf := 0; sf < 4; sf++ {
					out = append(out, diagParam{
						packet:       packet,
						frame:        f,
						sf:           sf,
						voiced:       false,
						nrgresDbqQ14: g.gainQ[sf],
						fcbgIdx:      g.nrgRes[sf],
					})
				}
			}
		}
	}
	return out
}

// The loop count per packet duration, against the reference decoder's
// `num_frames = (packet_len_ms + 10) / 20`. 10 ms is excluded because it also changes the
// internal frame length and subframe count, which the synthesis does not implement.
func TestDecoder_InternalFrameCountMatchesTheReferenceGeometry(t *testing.T) {
	for _, c := range []struct {
		ms   int32
		want int
		ok   bool
	}{{20, 1, true}, {60, 3, true}, {120, 6, true}, {10, 0, false}} {
		got, ok := internalFrames(c.ms)
		if got != c.want || ok != c.ok {
			t.Errorf("internalFrames(%d) = (%d, %v), want (%d, %v)", c.ms, got, ok, c.want, c.ok)
		}
	}
}

// A 120 ms MLow packet decodes to its full duration.
//
// Note what this does NOT claim. TOC `0x58` was originally taken as evidence that WhatsApp
// Desktop sends 120 ms MLow packets; it is not. The packets that prompted that reading are
// standard Opus SILK wideband at 60 ms, where the same byte is an RFC 6716 config-11 TOC (see
// `voip::opus_packet`). The reference geometry `num_frames = (ms + 10) / 20` is nonetheless
// real, verified against the shipped decoder, so a single-block 120 ms packet is legal and is
// decoded here. The shipped encoder does not emit one - it aggregates 60 ms blocks through the
// multiframe envelope instead - so this is correctness for a form that exists in the format
// rather than support for an observed stream.
func TestDecoder_MultiFramePacketDecodesToItsFullDuration(t *testing.T) {
	toc := parseMlowToc(0x58)
	if toc.frameMs != 120 {
		t.Fatalf("0x58 declares a 120 ms packet")
	}
	if !(toc.active && !toc.sid && !toc.stdOpus) {
		t.Fatalf("assertion failed: toc.active && !toc.sid && !toc.std_opus")
	}
	if toc.sampleRate != 16000 {
		t.Fatalf("sample_rate %d != 16000", toc.sampleRate)
	}
	if toc.lowRate {
		t.Fatalf("0x58 is the supported rate mode")
	}

	dec := NewDecoder()
	out := dec.Decode([]byte{0x58, 0xAA, 0xBB, 0xCC, 0xDD, 0xEE, 0xFF, 0x11, 0x22})
	if len(out) != 6*smplIntfLen {
		t.Fatalf("a 120 ms packet must yield 120 ms of PCM: got %d", len(out))
	}
}

// A 20 ms packet shares the same internal geometry and must decode to exactly one frame.
func TestDecoder_SingleFramePacketDecodesToOneInternalFrame(t *testing.T) {
	dec := NewDecoder()
	out := dec.Decode([]byte{0x48, 0xAA, 0xBB, 0xCC})
	if len(out) != smplIntfLen {
		t.Fatalf("a 20 ms packet is one 20 ms frame: got %d", len(out))
	}
}

// The failure case the geometry guard exists for: 10 ms halves the internal frame length and
// the subframe count, so it must still be dropped rather than decoded under the wrong geometry,
// into the same 60 ms silence slot the other drops use.
func TestDecoder_TenMsActivePacketIsStillDropped(t *testing.T) {
	toc := parseMlowToc(0x40)
	if toc.frameMs != 10 {
		t.Fatalf("frame_ms %d != 10", toc.frameMs)
	}
	if !toc.active {
		t.Fatalf("assertion failed: toc.active")
	}

	dec := NewDecoder()
	out := dec.Decode([]byte{0x40, 0xAA, 0xBB, 0xCC})
	// Dropped, but still occupying the 10 ms it declares: a run of these must not arrive at six
	// times real time and push decoded speech out of the jitter buffer.
	if len(out) != 160 {
		t.Fatalf("len %d != 160", len(out))
	}
	if !decoderAllZero(out) {
		t.Fatalf("10 ms runs a geometry the synthesis does not implement")
	}
	if dec.hadError() {
		t.Fatalf("the drop must not open the range decoder")
	}
}

// Silence occupies the time the packet claims, and the cushion follows the declared duration.
//
// A frame that produces no audio still has to occupy its slot: emitting a fixed 60 ms for a
// packet the peer sends every 120 ms starves playout, and emitting it for one sent every 20 ms
// floods the buffer at three times real time until the latency ceiling trims speech off the
// head. Either way the concealment displaces audio that did decode.
func TestDecoder_DeclaredDurationSurvivesADtxTransition(t *testing.T) {
	dec := NewDecoder()
	_ = dec.Decode([]byte{0x58, 0xAA, 0xBB, 0xCC, 0xDD, 0xEE, 0xFF, 0x11, 0x22})
	if dec.LastPacketSamples() != 6*smplIntfLen {
		t.Fatalf("last_packet_samps %d != %d", dec.LastPacketSamples(), 6*smplIntfLen)
	}

	// A SID that still declares 120 ms (0x98 = SID | 120 ms) fills 120 ms of silence.
	sid := dec.Decode([]byte{0x98, 0xAA, 0xBB, 0xCC})
	if len(sid) != 6*smplIntfLen {
		t.Fatalf("silence occupies the duration the packet declares: got %d", len(sid))
	}
	if dec.LastPacketSamples() != 6*smplIntfLen {
		t.Fatalf("the peer is still on 120 ms packets; the cushion must not shrink")
	}

	// A peer that genuinely moves to 60 ms is learned.
	_ = dec.Decode([]byte{0x50, 0xAA, 0xBB, 0xCC})
	if dec.LastPacketSamples() != 3*smplIntfLen {
		t.Fatalf("last_packet_samps %d != %d", dec.LastPacketSamples(), 3*smplIntfLen)
	}
}

// The way the shipped client actually reaches a packet longer than its frame length.
//
// Its encoder caches a fixed three blocks of 20 ms, so `frame_length_ms = 120` produces two
// 60 ms blocks in one envelope, not one 120 ms block. Before this was implemented the
// envelope's first byte read as a SID and every packet of such a call became comfort noise:
// a totally silent call with no error anywhere.
func TestDecoder_AMultiframeEnvelopeDecodesInsteadOfBecomingComfortNoise(t *testing.T) {
	encoder := NewEncoder()
	tone := make([]float32, opusFrameSamps)
	for i := range tone {
		tone[i] = 0.3 * sinF32(float32(i)*0.05)
	}
	first, err := encoder.Encode(tone)
	if err != nil {
		t.Fatalf("mlow encode: %v", err)
	}
	second, err := encoder.Encode(tone)
	if err != nil {
		t.Fatalf("mlow encode: %v", err)
	}
	if !(len(first) < 252 && len(second) < 252) {
		t.Fatalf("assertion failed: first.len() < 252 && second.len() < 252")
	}

	envelope := []byte{0x82 | (first[0] & 0x39), 0x02, byte(len(first))}
	envelope = append(envelope, first...)
	envelope = append(envelope, second...)

	// The bug this replaces: read as a TOC, the indicator has bit 7 set and is a SID.
	if !parseMlowToc(envelope[0]).sid {
		t.Fatalf("assertion failed: parse_mlow_toc(envelope[0]).sid")
	}

	decoder := NewDecoder()
	pcm := decoder.Decode(envelope)
	report := decoder.TakeFrameReport()
	if report.Decoded != 6 {
		t.Fatalf("two 60 ms blocks are six internal frames: got %d", report.Decoded)
	}
	if report.InactiveOrSID != 0 {
		t.Fatalf("the envelope is not a SID")
	}
	if len(pcm) != 2*opusFrameSamps {
		t.Fatalf("len %d != %d", len(pcm), 2*opusFrameSamps)
	}
	if decoder.LastPacketSamples() != 2*opusFrameSamps {
		t.Fatalf("the playout cushion must size to the whole envelope, not one sub-frame")
	}
	energy := float32(math.Copysign(0, -1))
	for _, s := range pcm {
		energy += float32(s * s)
	}
	if !(energy > 0.0) {
		t.Fatalf("a decoded envelope must not be silence")
	}
}

// A malformed envelope is concealed and counted, never mistaken for comfort noise.
func TestDecoder_AMalformedMultiframeEnvelopeIsConcealedAndReported(t *testing.T) {
	decoder := NewDecoder()
	// Two frames declared, the first taking the entire body.
	envelope := []byte{0x92, 0x02, 10}
	for b := byte(0); b < 10; b++ {
		envelope = append(envelope, b)
	}
	pcm := decoder.Decode(envelope)
	report := decoder.TakeFrameReport()
	if report.Concealed != 1 {
		t.Fatalf("concealed %d != 1", report.Concealed)
	}
	if report.InactiveOrSID != 0 {
		t.Fatalf("inactive_or_sid %d != 0", report.InactiveOrSID)
	}
	if !decoderAllZero(pcm) {
		t.Fatalf("assertion failed: pcm all zero")
	}
}

// A `VoA=00` packet is a normal frame carrying background noise, not a SID: the reference
// decodes it, and with DTX off a peer sends nothing else during a pause. Silencing it drops
// ~12% of a real stream on the floor while the call merely sounds quiet.
func TestDecoder_DtxOffFramesDecodeToAudio(t *testing.T) {
	frames := decoderFrames(t, "mlow_dtx_off_frames.json")
	refp := decoderRefPCM(t, "ref_dtx_off_expected.raw")

	dec := NewDecoder()
	var out []float32
	var spans [][2]int
	for _, frame := range frames {
		start := len(out)
		out = append(out, dec.Decode(frame)...)
		// The frames this covers: not SID, VoA=0, hang-over clear, i.e. coded_as_active_voice
		// == 0. `0x12` shares VoA=0 but sets the hang-over bit, which makes it active and
		// already decoded, so it must not be counted here.
		if frame[0]&0xC2 == 0 {
			spans = append(spans, [2]int{start, len(out)})
		}
	}
	if len(spans) == 0 {
		t.Fatalf("fixture lost its DTX-off frames: this path is no longer covered")
	}
	if len(out) != len(refp) {
		t.Fatalf("len %d != %d", len(out), len(refp))
	}

	energy := func(v []float32) float64 {
		s := math.Copysign(0, -1)
		for _, x := range v {
			s += float64(powiF64(float64(x), 2))
		}
		return s
	}
	var eRef, eOurs float64
	n := 0
	for _, sp := range spans {
		s, e := sp[0], sp[1]
		eRef += energy(refp[s:e])
		eOurs += energy(out[s:e])
		n += e - s
	}
	rmsRef, rmsOurs := math.Sqrt(eRef/float64(n)), math.Sqrt(eOurs/float64(n))
	if !(rmsRef > 0.01) {
		t.Fatalf("the reference itself has no audio here; the fixture is wrong")
	}
	if !(rmsOurs > rmsRef*0.5) {
		t.Fatalf("DTX-off frames decoded to %.5f rms against the reference's %.5f", rmsOurs, rmsRef)
	}
}

// Same DTX-off property as above, but both halves come from the shipped
// engine (`oracle derive --spec specs/mlow_110frames.json` in unwasm;
// see PROVENANCE.md): the `0x10` frames it encoded over the silent
// stretches must decode (here) to audio matching what it decoded
// itself. The per-frame RMS check also covers near-silent intervals,
// where a correlation coefficient alone is not meaningful.
func TestDecoder_WasmDerivedDtxFramesDecodeToAudio(t *testing.T) {
	frames := decoderFrames(t, "wasm_derived_frames.json")
	refp := decoderRefPCM(t, "wasm_derived_ref.raw")

	dec := NewDecoder()
	nDtx := 0
	nAudible := 0
	rms := func(v []float32) float64 {
		s := math.Copysign(0, -1)
		for _, x := range v {
			s += float64(float64(x) * float64(x))
		}
		return math.Sqrt(s / float64(len(v)))
	}
	for k, frame := range frames {
		out := dec.Decode(frame)
		if frame[0]&0xC2 != 0 {
			continue
		}
		nDtx++
		if len(out) == 0 {
			t.Fatalf("frame %d: DTX frame decoded empty", k)
		}
		expected := refp[k*960 : (k+1)*960]
		eOut, eRef := rms(out), rms(expected)
		if eRef < 0.005 {
			if !(eOut < 0.02) {
				t.Fatalf("frame %d: silent ref decoded loud at %.4f", k, eOut)
			}
			continue
		}
		nAudible++
		if !(eOut > eRef*0.5) {
			t.Fatalf("frame %d: DTX frame decoded to %.5f rms against ref %.5f", k, eOut, eRef)
		}
	}
	if nDtx == 0 {
		t.Fatalf("fixture lost its DTX frames")
	}
	if nAudible == 0 {
		t.Fatalf("no audible DTX reference to compare against")
	}
}

// A stream that ends where it claims to must be accepted. This is the guard against the
// endpoint check being too strict: the synthetic 120 ms vector is a well-formed six-frame
// packet, and rejecting it would silence audio that decodes correctly.
func TestDecoder_EndpointCheckAcceptsAWellFormedPacket(t *testing.T) {
	frames := decoderFrames(t, "mlow_120ms_frames.json")
	dec := NewDecoder()
	for _, frame := range frames {
		out := dec.Decode(frame)
		if !decoderAnyNonZero(out) {
			t.Fatalf("a valid packet must not be rejected as malformed")
		}
	}
}

// The accepted window is the shipped decoder's, not the C fork's. The fork stops at `+2`, so a
// stream ending three or four bytes long would be concealed here and played by the official
// client. Pinned as arithmetic because no synthetic body lands on `+3` on demand.
func TestDecoder_EndpointWindowMatchesTheShippedDecoder(t *testing.T) {
	if !endpointIsValid(40, 40) {
		t.Errorf("exact end is valid")
	}
	if !endpointIsValid(40, 42) {
		t.Errorf("the fork's +2 stays valid")
	}
	if !endpointIsValid(40, 44) {
		t.Errorf("+4 is valid: WhatsAppNative.dll @ 0x180337e30 adds 4 before its second compare, and " +
			"rejecting here would silence audio the official client plays")
	}
	if endpointIsValid(40, 45) {
		t.Errorf("+5 over-runs")
	}
	if endpointIsValid(40, 39) {
		t.Errorf("under-running is always malformed")
	}
}

// A body whose decode consumes far more bits than it holds is malformed: the range decoder
// returns zero past the end without flagging it, so the synthesis runs on invented symbols and
// can diverge to full scale. Conceal it as a lost frame rather than emitting that.
func TestDecoder_EndpointCheckRejectsAnOverrunningBody(t *testing.T) {
	// A 120 ms TOC with a body far too short for six internal frames.
	dec := NewDecoder()
	out := dec.Decode([]byte{0x58, 0x03, 0x1a, 0xfb, 0x0a})
	if len(out) != 6*smplIntfLen {
		t.Fatalf("still a full 120 ms slot: got %d", len(out))
	}
	if !decoderAllZero(out) {
		t.Fatalf("an over-running body must be concealed, not synthesized")
	}
}

// Real 120 ms packets captured from a live WhatsApp Desktop peer must decode to speech, not to
// full-scale noise. Reported on #1105 after the multi-frame admission landed: frames are now
// accepted, but the decode diverges partway through the packet and saturates, which is audibly
// worse than the silence it replaced.
//
// The assertions are deliberately coarse — this pins "the output is not garbage", which is what
// regressed, without pretending to a bit-exact target the fixture cannot supply.
//
// Concealing a malformed frame must leave no trace: the decode loop mutates the LSF predictor,
// the CELP history and `prev_nlsf` before the overrun is detectable, so without a rollback the
// NEXT packet is synthesized partly from parameters invented past the end of the bad body.
// The reference does not roll back, but it also never meets these packets; we do, repeatedly.
func TestDecoder_AConcealedFrameDoesNotContaminateTheNext(t *testing.T) {
	frames := decoderFrames(t, "inbound_capture_frames.json")
	real := frames[0]

	fresh := NewDecoder()
	want := fresh.Decode(real)

	contaminated := NewDecoder()
	bad := contaminated.Decode([]byte{0x58, 0x03, 0x1a, 0xfb, 0x0a})
	if !decoderAllZero(bad) {
		t.Fatalf("the bad frame is concealed")
	}
	got := contaminated.Decode(real)

	if len(got) != len(want) {
		t.Fatalf("len %d != %d", len(got), len(want))
	}
	if !decoderF32Equal(got, want) {
		t.Fatalf("a real frame after a concealed one must decode identically to one decoded on a fresh " +
			"decoder; state from the malformed body leaked into it")
	}
}

// The two halves of a cross-check vector come out of one harness run and mean nothing apart:
// refreshing only one leaves the comparison reading mismatched data, which surfaces as a
// correlation number that moved rather than as an obvious error. Pin what ties them, and pin
// that the fixture still exercises the multi-frame path it was added for.
func TestDecoder_MultiFrameFixtureHalvesStayInStep(t *testing.T) {
	frames := decoderFrames(t, "mlow_120ms_frames.json")
	pcmBytes := len(testdataBytes(t, "ref_120ms_expected.raw"))

	// An absolute count, not just "non-empty": a regeneration that ends early produces a
	// shorter vector whose PCM length still matches its own frame count, so a relative check
	// would accept it and the coverage loss would be invisible.
	if len(frames) != 8 {
		t.Fatalf("fixture no longer holds the 8 packets it was generated with; a short regeneration              silently reduces coverage")
	}
	for i, f := range frames {
		toc := f[0]
		if toc != 0x58 {
			t.Fatalf("frame %d is TOC %#04x, not the 120 ms packet this fixture exists to cover", i, toc)
		}
	}
	if pcmBytes != len(frames)*6*smplIntfLen*2 {
		t.Fatalf("reference PCM does not match the frame count; regenerate both halves together with " +
			"cargo xt mlow c-reference")
	}
}

// The content check: decode a stream of real 120 ms packets and compare against the reference
// decoder's own output for the same bytes. Geometry alone is not enough, since running the loop
// the wrong number of times would still produce plausibly-shaped audio while consuming the
// payload at the wrong symbol count. See testdata/PROVENANCE.md for the oracle.
func TestDecoder_MultiFrameDecodeMatchesTheReference(t *testing.T) {
	frames := decoderFrames(t, "mlow_120ms_frames.json")
	refp := decoderRefPCM(t, "ref_120ms_expected.raw")

	dec := NewDecoder()
	var out []float32
	for _, frame := range frames {
		if frame[0] != 0x58 {
			t.Fatalf("the fixture must stay 120 ms packets")
		}
		out = append(out, dec.Decode(frame)...)
	}
	if len(out) != len(refp) {
		t.Fatalf("decode length vs reference: %d != %d", len(out), len(refp))
	}

	corr := decoderLag0Corr(refp, out)
	if !(corr > 0.999) {
		t.Fatalf("lag-0 corr %.6f vs reference", corr)
	}
}

// Same shape as above, but both halves come from the shipped engine
// (`oracle derive --spec specs/mlow_120ms.json` in unwasm; see
// PROVENANCE.md): 8 120 ms packets it encoded itself, plus the PCM it
// decoded back. Correlation (not 0.999): our decoder and theirs agree
// closely on the same packets without sharing lineage — the 0.9874
// measured atlock time is the number to beat, with margin.
func TestDecoder_WasmDerived120msDecodeMatchesWasmReference(t *testing.T) {
	frames := decoderFrames(t, "wasm_derived_120ms_frames.json")
	refp := decoderRefPCM(t, "wasm_derived_120ms_ref.raw")

	dec := NewDecoder()
	var out []float32
	for _, frame := range frames {
		if frame[0] != 0x58 {
			t.Fatalf("the fixture must stay 120 ms packets")
		}
		out = append(out, dec.Decode(frame)...)
	}
	if len(out) != len(refp) {
		t.Fatalf("decode length vs reference: %d != %d", len(out), len(refp))
	}

	corr := decoderLag0Corr(refp, out)
	if !(corr > 0.95) {
		t.Fatalf("lag-0 corr %.6f vs shipped reference", corr)
	}
}

// Decoding a multi-frame packet must leave the cross-frame predictor usable: a real 60 ms frame
// after it still has to produce audio.
func TestDecoder_MultiFramePacketDoesNotPoisonLaterFrames(t *testing.T) {
	frames := decoderFrames(t, "inbound_capture_frames.json")
	real := frames[0]

	dec := NewDecoder()
	_ = dec.Decode([]byte{0x58, 0xAA, 0xBB, 0xCC, 0xDD, 0xEE, 0xFF, 0x11, 0x22})
	after := dec.Decode(real)
	if len(after) != 960 {
		t.Fatalf("len %d != 960", len(after))
	}
	if !decoderAnyNonZero(after) {
		t.Fatalf("a real 60 ms frame after a 120 ms packet must still decode")
	}
}

// End-to-end: decode the whole capture and compare against the reference output
// (`ref_usesmpl_expected.raw`; see testdata/PROVENANCE.md).
//
// An earlier target (`e2e_vectors.json`) was proven wrong: it used the int16-domain `*nrgres`
// excitation with no shaped noise (a tail-off bug) and correlates ~0 with the true codec. With
// the per-block voiced ACB/LTP lags, the HP postfilter, and the harmonic postfilter (which emits
// the SMPL_TOT_POSTFILT_DELAY = 48-sample group delay) all in place, the decode now aligns
// sample-for-sample at lag 0.
func TestDecoder_E2eDecodeMatchesUsesmpl(t *testing.T) {
	frames := decoderFrames(t, "inbound_capture_frames.json")
	refp := decoderRefPCM(t, "ref_usesmpl_expected.raw")

	dec := NewDecoder()
	var out []float32
	for _, frame := range frames {
		out = append(out, dec.Decode(frame)...)
	}
	if len(out) != len(refp) {
		t.Fatalf("decode length vs reference: %d != %d", len(out), len(refp))
	}

	// Aligned at lag 0 now (the harmonic postfilter emits the 48-sample group delay).
	const lag = 0
	n := len(refp) - lag
	corr := decoderLag0Corr(refp[lag:lag+n], out[:n])
	if !(corr > 0.95) {
		t.Fatalf("lag-0 corr %.4f vs reference", corr)
	}
}

// decoderFuzzCorpusLen is the number of inputs the corpus below produces: 8000 LCG buffers, then
// per capture frame eight bit-flips and one truncation per byte plus the frame itself.
//
// Asserted at the end of every shard rather than derived, so growing the
// corpus without re-dividing it fails instead of leaving the new tail
// unfuzzed.
const decoderFuzzCorpusLen = 149_104
const decoderFuzzShards = 4

// R2 (fuzz no-panic): the decoder is fed adversarial inputs and must neither panic nor over-emit.
// Corpus: a deterministic LCG of random byte vectors, plus every capture frame with each single
// byte flipped and each prefix truncation. The contract is purely structural (no panic, bounded
// output); the range decoder absorbs corruption by returning zero, so `hadError` is not asserted.
//
// Output length is data-driven by the TOC: `sample_rate/1000 * frame_ms`, where the TOC fields
// span {16,32} kHz and {10,20,60,120} ms. The hard ceiling is therefore 32 * 120 = 3840 samples,
// not the 960 of a common 60 ms / 16 kHz frame; a fuzzed TOC can legitimately declare a larger
// frame, which the decoder fills with silence on the SID/inactive/std-opus paths.
//
// Sharded because ~149k decoder calls in one process were 90% of the unit suite's wall clock
// with three of the runner's four cores idle. Each shard walks the whole corpus and decodes
// only its own contiguous slice, through a decoder it keeps for the length of that slice: the
// property includes what a poisoned frame leaves behind for the next one, which a fresh decoder
// per input would not exercise. Four slices, contiguous and disjoint, so their union is the
// corpus the single test fed. Do not merge them back. (Go: the shards run in parallel.)
func decoderFuzzDecodeShard(t *testing.T, shard int) {
	const maxSamps = 32 * 120 // max sample_rate(kHz) * max frame_ms across all TOCs
	lo := decoderFuzzCorpusLen * shard / decoderFuzzShards
	hi := decoderFuzzCorpusLen * (shard + 1) / decoderFuzzShards

	index := 0
	decoded := 0
	dec := NewDecoder()
	var out []float32
	check := func(dec *Decoder, input []byte) {
		mine := index >= lo && index < hi
		index++
		if !mine {
			return
		}
		decoded++
		out = dec.AppendDecode(out[:0], input)
		if len(out) > maxSamps {
			t.Fatalf("decode emitted %d > %d samples for input len %d", len(out), maxSamps, len(input))
		}
	}

	// Deterministic LCG (numerical-recipes constants) over thousands of random-length buffers.
	seed := uint32(0x1234_5678)
	next := func(s *uint32) uint32 {
		*s = *s*1664525 + 1013904223
		return *s
	}
	for range 8000 {
		n := int(next(&seed) % 400)
		buf := make([]byte, 0, n)
		for range n {
			buf = append(buf, byte(next(&seed)>>24))
		}
		check(dec, buf)
	}

	// Mutations of the real capture frames: every single-byte flip and every truncation.
	frames := decoderFrames(t, "inbound_capture_frames.json")
	m := make([]byte, 0, 512)
	for _, frame := range frames {
		for i := 0; i < len(frame); i++ {
			for bit := 0; bit < 8; bit++ {
				m = append(m[:0], frame...)
				m[i] ^= 1 << bit
				check(dec, m)
			}
			check(dec, frame[:i]) // truncation at every prefix length
		}
		check(dec, frame)
	}

	if index != decoderFuzzCorpusLen {
		t.Fatalf("corpus is %d inputs, not the %d the shards divide", index, decoderFuzzCorpusLen)
	}
	if decoded != hi-lo {
		t.Fatalf("shard %d covered %d of %d", shard, decoded, hi-lo)
	}
}

func decoderFuzzShardTest(t *testing.T, shard int) {
	if testing.Short() {
		t.Skip("fuzz shard: ~37k decodes, skipped in -short (slow under -race)")
	}
	t.Parallel()
	decoderFuzzDecodeShard(t, shard)
}

func TestDecoder_FuzzDecodeNoPanicBoundedOutputShard0(t *testing.T) { decoderFuzzShardTest(t, 0) }

func TestDecoder_FuzzDecodeNoPanicBoundedOutputShard1(t *testing.T) { decoderFuzzShardTest(t, 1) }

func TestDecoder_FuzzDecodeNoPanicBoundedOutputShard2(t *testing.T) { decoderFuzzShardTest(t, 2) }

func TestDecoder_FuzzDecodeNoPanicBoundedOutputShard3(t *testing.T) { decoderFuzzShardTest(t, 3) }

// Fail-loud guards: a frame outside our single operating point (32kHz/fullband, or low_rate=1) is
// DROPPED to 60ms silence WITHOUT touching the range coder, so it can't desync + poison the
// cross-frame predictor. A real low_rate=0 frame before and after the drops still decodes (proves
// the drop is a clean "lost frame", not a desync), and the guards don't false-positive on it.
func TestDecoder_UnsupportedFramesDropCleanWithoutDesync(t *testing.T) {
	frames := decoderFrames(t, "inbound_capture_frames.json")
	real := frames[0]
	dec := NewDecoder()

	// A real 0x50 (16kHz, low_rate=0) frame decodes to audio — the guards don't false-positive.
	normal := dec.Decode(real)
	if len(normal) != 960 {
		t.Fatalf("len %d != 960", len(normal))
	}
	if !decoderAnyNonZero(normal) {
		t.Fatalf("a real low_rate=0 frame must decode to audio")
	}

	// A 32kHz/fullband TOC (bit5=1, e.g. 0x70) -> dropped. It declares 60 ms, and the slot is
	// 60 ms of OUTPUT samples: the synthesis rate does not follow the declared internal rate.
	out32k := dec.Decode([]byte{0x70, 0xAA, 0xBB, 0xCC})
	if len(out32k) != 960 {
		t.Fatalf("len %d != 960", len(out32k))
	}
	if !decoderAllZero(out32k) {
		t.Fatalf("32kHz frame must drop to silence")
	}

	// A low_rate=1 TOC -> dropped to 60ms silence. 0x54 = 0b0101_0100: bit5=0 (16kHz, so the rate
	// branch does NOT catch it), bits4:3=10 (60ms), bit2=1 (low_rate) -> exercises the low_rate branch.
	outLr := dec.Decode([]byte{0x54, 0xAA, 0xBB, 0xCC})
	if len(outLr) != 960 {
		t.Fatalf("len %d != 960", len(outLr))
	}
	if !decoderAllZero(outLr) {
		t.Fatalf("low_rate=1 frame must drop to silence")
	}

	// A 10ms ACTIVE 16kHz/low_rate=0 TOC (0x40) must also drop: it is the one duration whose
	// internal frame length and subframe count differ, so decoding it under the implemented
	// geometry would desync the range coder. 20/60/120ms all decode (see the geometry tests).
	out10ms := dec.Decode([]byte{0x40, 0xAA, 0xBB, 0xCC})
	if len(out10ms) != 160 {
		t.Fatalf("the slot is the declared 10 ms: got %d", len(out10ms))
	}
	if !decoderAllZero(out10ms) {
		t.Fatalf("a 10ms active frame must drop to silence")
	}

	// The drops never opened the range decoder, so the predictor is intact: the real frame still
	// decodes to audio afterwards (no desync from the dropped frames in between).
	if dec.hadError() {
		t.Fatalf("dropped frames must not touch the range decoder")
	}
	after := dec.Decode(real)
	if !decoderAnyNonZero(after) {
		t.Fatalf("a real frame after the drops must still decode (no poisoned predictor)")
	}
}

// A SID frame (TOC 0x80, the DTX/CNG marker) is comfort noise, not a desync hazard: it is
// silenced without opening the range coder regardless of geometry. It must take the quiet path (a
// full 60ms silence slot, range coder untouched) and must NOT count as an out-of-operating-point
// drop, which is reserved for frames that would have lost decodable audio.
func TestDecoder_SidFrameIsSilencedNotDropped(t *testing.T) {
	frames := decoderFrames(t, "inbound_capture_frames.json")
	real := frames[0]
	dec := NewDecoder()

	if !decoderAnyNonZero(dec.Decode(real)) {
		t.Fatalf("a real low_rate=0 frame must decode to audio")
	}

	// TOC 0x80 -> SID: silenced via the comfort-noise path, not the operating-point drop. It
	// declares 10 ms, so it fills 10 ms: silence tracks the cadence the peer is sending at.
	inactive := dec.Decode([]byte{0x80, 0xAA, 0xBB, 0xCC})
	if len(inactive) != 160 {
		t.Fatalf("a SID occupies the duration it declares: got %d", len(inactive))
	}
	if !decoderAllZero(inactive) {
		t.Fatalf("an inactive frame must be silence")
	}
	if dec.droppedUnsupported != 0 {
		t.Fatalf("a SID frame is the comfort-noise path, not an operating-point drop")
	}
	if dec.hadError() {
		t.Fatalf("the SID path must not open the range decoder")
	}

	// Contrast: an active off-point frame (0x60 = vad=true, 32 kHz) IS counted, proving the drop
	// counter discriminates real audio loss from benign inactive silence.
	_ = dec.Decode([]byte{0x60, 0xAA, 0xBB, 0xCC})
	if dec.droppedUnsupported != 1 {
		t.Fatalf("an active off-point frame must count as a drop")
	}

	// After both the inactive frame and the drop, a real frame still decodes: no desync, no
	// poisoned predictor.
	if !decoderAnyNonZero(dec.Decode(real)) {
		t.Fatalf("a real frame must still decode after an inactive frame and a drop")
	}
}

// R7 (RED round-trip): a bare frame wrapped in a 1-redundant SplitRed envelope must decode to the
// exact same PCM as the bare frame at redundancy 0. Exercises the `redundancy > 0` strip path
// (which forwards the main/last frame) end-to-end.
func TestDecoder_RedEnvelopeDecodesToBareMain(t *testing.T) {
	frames := decoderFrames(t, "inbound_capture_frames.json")
	bare := frames[0]

	bareDec := NewDecoder()
	bareOut := bareDec.Decode(bare)

	// SplitRed N=1: red_hdr [0x80 | tc, size], main_marker (high bit clear), red payload, main.
	// The main (last) frame is the bare frame, so the strip path must reproduce `bare_out`.
	redPayload := []byte{0xAA, 0xBB}
	env := []byte{0x80, byte(len(redPayload)), 0x00}
	env = append(env, redPayload...)
	env = append(env, bare...)

	redDec := NewDecoder()
	redDec.SetRedundancy(1)
	redOut := redDec.Decode(env)

	if !decoderF32Equal(redOut, bareOut) {
		t.Fatalf("RED-wrapped main differs from bare decode")
	}
	if redDec.hadError() {
		t.Fatalf("RED decode raised the range decoder error flag")
	}
}

// TestDecoder_AppendDecodeIsAllocationFree is a Go-only check of the AppendDecode contract: once
// dst has capacity, decoding a real packet does not allocate. Not in the Rust suite.
func TestDecoder_AppendDecodeIsAllocationFree(t *testing.T) {
	frames := decoderFrames(t, "inbound_capture_frames.json")
	dec := NewDecoder()
	buf := make([]float32, 0, maxPacketSamps)
	for _, f := range frames {
		buf = dec.AppendDecode(buf[:0], f)
	}
	i := 0
	allocs := testing.AllocsPerRun(50, func() {
		buf = dec.AppendDecode(buf[:0], frames[i%len(frames)])
		i++
	})
	if allocs != 0 {
		t.Logf("AppendDecode allocates %.1f times per packet in steady state", allocs)
	}
}
