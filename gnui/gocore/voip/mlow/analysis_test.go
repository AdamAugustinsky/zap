// Ported from whatsapp-rust 5e72baf wacore/src/voip/mlow/analysis.rs (the `bench-internals`
// `stage_bench` module and its `stage_bench_tests`), MIT.
//
// Per-stage bench surface (Rust `wacore/benches/voip_benchmark.rs` rows; Go benchmarks below).
//
// `mlow_encode` is one row, so a change inside the codec moves it without saying WHICH stage
// moved. These harnesses call the SAME production functions the analyzer calls, one stage per row,
// so a stage-local change has a stage-local number.
//
// Setup vs. body is the load-bearing distinction: `newAnalysisStages` runs real frames through
// `smplAnalyzeFrameSt`, which builds every lazily-initialized table, both FFT twiddle sets and
// every pooled buffer, and leaves the CELP/perc/pitch/VAD state in its steady-state regime. What
// the row bodies then execute is per-frame work only -- what production pays every 60 ms, never
// what it resolves once and holds. The per-frame multiplicity of each stage is on its method.
//
// KNOWN LIMITATION, measured rather than assumed. The stateful rows advance the same cross-frame
// state production advances (perc history, pitch predictor, CELP ACB/ZIR, bitrate controller)
// while replaying a fixed three-frame input cycle, so a row's regime is its own history rather
// than a live call's. `TestAnalysis_StageRowsAreThreePeriodic` pins the result: `pitchSearch` and
// `percCorrsFrame` are EXACTLY 3-periodic from the first iteration, so for them the question does
// not arise. `celpSubframesFrame` is not -- its pulse counts wander (28,19,31,... early vs
// 26,16,33,... after 20 iterations) because the excitation history keeps evolving against a
// repeating residual. Its timing spread stays inside 1%, so the cost is dominated by the
// fixed-size beam search rather than the exact pulse count, but that row is a steady-state
// estimate and not a reproduction of one production frame. `analyzeFrame` has no such caveat --
// it drives the real encoder over eight cycling frames -- which is why the stage rows are
// reported against it rather than summed alone.

package mlow

import (
	"math"
	"slices"
	"testing"
)

// analysisWarmupFrames is the frames pushed through the encoder before any input is captured.
// Three leave every cross-frame buffer (`hist`, `lpcHist`, `hpPitchHist`, `ltpBuf`, ACB state)
// filled with real signal rather than the zeros a first frame sees.
const analysisWarmupFrames = 3

// analysisStream is the distinct frames the `analyzeFrame` row cycles, matching `mlow_encode`'s
// own `STREAM`. A single repeated frame lets the adaptive encoder settle onto a cheaper
// low-pulse/low-survivor path, which would under-report the stage and make the row incomparable
// to `mlow_encode`.
const analysisStream = 8

// analysisTone is a 60 ms voiced tone, matching the benchmark's own steady-state input.
func analysisTone(phase int) []float32 {
	out := make([]float32, smplIntfLen*3)
	for i := range out {
		out[i] = 0.3 * sinF32(float32(i+phase)*0.07)
	}
	return out
}

// analysisLsfInputs is one internal frame's LSF-quantizer inputs. Production derives `a`/NLSF anew
// per internal frame and threads the previous frame's committed envelope; `lsfQuantCore` has an
// input-dependent early exit (`abs_qerr < 0.25`), so frozen values would fix how many refinement
// iterations run.
type analysisLsfInputs struct {
	a        [smplLpcOrder + 1]float32
	nlsf     [smplLpcOrder]float32
	prevNlsf []float32
}

// analysisPitchInputs is one internal frame's pitch-estimator inputs, captured from the live
// encoder state after `buildLtpBuf` has rolled that frame's perceptually weighted speech in --
// which production does before every `smplPitch` call. A frozen pair would fix the estimator's
// data-dependent survivor blocks, so resetting the predictor alone would still measure one
// workload.
type analysisPitchInputs struct {
	ltpBuf []float32
	f2     [smplFLen]float32
}

// analysisCelpInputs is one internal frame's CELP inputs, captured from the live encoder state.
type analysisCelpInputs struct {
	predcoefs [smplSubfrCount][smplLpcOrder + 1]float32
	resLpc    []float32
	blockLags [smplSubfrCount][2]float32
	percCorrs [smplSubfrCount][smplPercRLen]float32
}

// analysisStages is a primed encoder plus the captured stage inputs (Rust `Stages`).
type analysisStages struct {
	es smplEncoderState
	// Distinct frames the `analyzeFrame` row cycles through, and the cursor into them.
	pcm   [][]float32
	pcmAt int
	// Snapshot of the last analyzed frame's normalized HP signal (`hpN`), which the perc model and
	// the residual read.
	hp          []float32
	hpPitchHist []float32
	// The `lpcbuf` window each internal frame analyzes. Production advances the start by
	// `f * smplIntfLen`, and the coefficients that fall out drive the data-dependent root search in
	// `smplA2nlsf16`, so cycling only the long/short window flag would still be three passes over
	// one signal.
	lpcbufs [][smplLpcBufLen]float32
	// Zero-padded windowed buffer + output, for the bare FFT row.
	fftIn      []float32
	fftOut     []float32
	fftScratch fftScratch
	// Same, at the perceptual model's size (N=576 = 2^6 * 3^2), for the bare mixed-radix row.
	fft576Time []float32
	fft576Spec []float32
	// Separate destination for the inverse: writing back into `fft576Time` would scale the signal
	// by N on every iteration (the transform pair is unnormalized) and walk the row into inf,
	// changing what it measures.
	fft576Out     []float32
	fft576Scratch fftScratch
	prevNlsf      []float32
	// Per-row cursors over the internal-frame index. Every stage that production runs once per
	// internal frame behaves DIFFERENTLY at `intf` 0, 1 and 2 -- the LPC window length, the
	// perceptual model's last-subframe window, the conditional LSF quantizer and the pitch
	// predictor's packet-boundary reset all key off it. Pinning a row to one index and multiplying
	// by three would report three copies of one path instead of the frame's real mix, so each such
	// row cycles 0, 1, 2 and its x3 is the actual frame. One cursor per row, because the rows run
	// independently.
	intfAt [analysisInterfaceRows]int
	f2     [smplFLen]float32
	// CELP inputs, one set per internal frame. Cycling `intf` alone would still hand
	// `runCelpSubframes` the same predcoefs, residual, lags and perceptual weights every
	// iteration, and the fixed-codebook search inside it is data-dependent -- it would settle on
	// one pulse/survivor path and misreport the largest stage of the encoder.
	celp []analysisCelpInputs
	// Pitch inputs, one set per internal frame. See analysisPitchInputs.
	pitch []analysisPitchInputs
	// LSF inputs, one set per internal frame. See analysisLsfInputs.
	lsf []analysisLsfInputs
	// Entropy-coder inputs: analyzed parameters for each of the `analysisStream` frames, and the
	// cursor over them. One set would make the row re-serialize a single frame forever, and an
	// entropy encode's cost tracks the parameters it writes (pulse counts, voiced vs. unvoiced
	// block).
	fps  []smplFrameParams
	fpAt int
	rng  rangeEncoder
	out  []byte
}

// Row slots in `intfAt`.
const (
	analysisRowLpc        = 0
	analysisRowPerc       = 1
	analysisRowPitch      = 2
	analysisRowLsf        = 3
	analysisRowCelp       = 4
	analysisInterfaceRows = 5
)

// analysisCloneFrameParams deep-copies params whose pulse slices alias encoder scratch (Go-only:
// Rust's params own their Vecs).
func analysisCloneFrameParams(fp smplFrameParams) smplFrameParams {
	for i := range fp.internal {
		fp.internal[i].pulses.magRuns = slices.Clone(fp.internal[i].pulses.magRuns)
		fp.internal[i].pulses.signSyms = slices.Clone(fp.internal[i].pulses.signSyms)
	}
	return fp
}

// newAnalysisStages is SETUP ONLY. Everything here is what production resolves once per stream
// and holds: table loads, twiddle construction, scratch pools, warmed cross-frame state.
func newAnalysisStages() *analysisStages {
	es := defaultSmplEncoderState()
	for k := 0; k < analysisWarmupFrames; k++ {
		_ = smplAnalyzeFrameSt(&es, analysisTone(k*smplIntfLen*3), nil)
	}
	pcm := make([][]float32, analysisStream)
	for k := range pcm {
		pcm[k] = analysisTone((analysisWarmupFrames + k) * smplIntfLen * 3)
	}
	// Analyze the whole stream up front so `entropyEncode` has one parameter set per frame to
	// cycle. This runs before the buffer snapshots below so those stay consistent with the state
	// `es` is left in.
	fps := make([]smplFrameParams, 0, len(pcm))
	for _, f := range pcm {
		fps = append(fps, analysisCloneFrameParams(smplAnalyzeFrameSt(&es, f, nil)))
	}

	hp := slices.Clone(es.hp)
	hpPitchHist := slices.Clone(es.hpPitchHist)

	// LPC front-end inputs for internal frame 0, built exactly as `smplAnalyzeFrameSt` builds them.
	var lpcbuf [smplLpcBufLen]float32
	lpcStart := smplLpcHistLen - smplLpcPre
	copy(lpcbuf[:], es.hpFull[lpcStart:lpcStart+smplLpcBufLen])
	windowed := smplWindowLpc20(&lpcbuf, true)
	fftScratch := newLpcFftScratch()
	fftIn := make([]float32, smplLpcNfft)
	copy(fftIn[:smplLpcBufLen], windowed[:])
	fftOut := make([]float32, smplLpcNfft)
	// Seed the N=576 row from real windowed signal (zero input would still cost the same
	// instructions, but a realistic magnitude keeps any future denormal effect honest).
	fft576Scratch := newFftScratch(percwNfft)
	fft576Time := make([]float32, percwNfft)
	copy(fft576Time[:smplLpcBufLen], windowed[:])
	fft576Spec := make([]float32, percwNfft)
	fft576Out := make([]float32, percwNfft)
	rfftForwardOrderedSc(fft576Time, fft576Spec, &fft576Scratch)
	// `f2` seeds the ctx's voicing-classifier input; the per-frame `a`/NLSF the LSF and CELP rows
	// need are captured in the per-frame loop below.
	_, f2 := smplLpcAnalyzeWithF2(&windowed, &fftScratch)

	prevLsfq := slices.Clone(es.prevLsfq)
	prevNlsf := slices.Clone(prevLsfq)

	s := &analysisStages{
		es:            es,
		pcm:           pcm,
		hp:            hp,
		hpPitchHist:   hpPitchHist,
		fftIn:         fftIn,
		fftOut:        fftOut,
		fftScratch:    fftScratch,
		fft576Time:    fft576Time,
		fft576Spec:    fft576Spec,
		fft576Out:     fft576Out,
		fft576Scratch: fft576Scratch,
		prevNlsf:      prevNlsf,
		f2:            f2,
		fps:           fps,
		rng:           newRangeEncoder(1 + smplEncodeBufBytes),
		out:           make([]byte, 0, 512),
	}
	// Capture one CELP input set per internal frame, each from the live state at that frame's own
	// offset, so the row's inputs vary with the index it cycles.
	synthT := loadSmplSynthTables()
	resLead := smplOrder + smplWinnextWbLen
	// The committed envelope threads forward: frame f's `brec` is frame f+1's `prevNlsf`.
	prevCommitted := slices.Clone(s.prevNlsf)
	for f := 0; f < 3; f++ {
		// This frame's own LPC power spectrum: production recomputes it per internal frame, and
		// `smplPitch` reads it for harmonic strength.
		var lpcbufF [smplLpcBufLen]float32
		start := smplLpcHistLen - smplLpcPre + f*smplIntfLen
		copy(lpcbufF[:], s.es.hpFull[start:start+smplLpcBufLen])
		windowedF := smplWindowLpc20(&lpcbufF, f < 2)
		aF, f2F := smplLpcAnalyzeWithF2(&windowedF, &s.fftScratch)
		nlsfF := smplA2nlsf16(aF[:])
		s.lpcbufs = append(s.lpcbufs, lpcbufF)

		cs := s.ctx(f, [smplSubfrCount][2]float32{})
		percCorrs := computePercCorrs(&cs)
		// Roll this frame's weighted speech into `ltpBuf`, exactly as the analyzer does before each
		// estimator call, then snapshot the pair the row replays.
		cs = s.ctx(f, [smplSubfrCount][2]float32{})
		buildLtpBuf(&cs, &percCorrs)
		s.pitch = append(s.pitch, analysisPitchInputs{
			ltpBuf: slices.Clone(s.es.ltpBuf),
			f2:     f2F,
		})
		pr := smplPitch(&s.es.pitchEst, s.es.ltpBuf, &f2F, true)
		var blockLags [smplSubfrCount][2]float32
		for sf := 0; sf < smplSubfrCount; sf++ {
			blockLags[sf] = [2]float32{pr.lags[2*sf], pr.lags[2*sf+1]}
		}
		// Production sets BOTH `prevLsfq` and `prevNlsf` to the frame's committed NLSF after every
		// internal frame, and the LSF quantizer, the envelope reconstruction and the interpolation
		// that builds the CELP residual all read it. Thread one value through all three rather
		// than pinning them to the packet-start snapshot.
		prevForFrame := slices.Clone(prevCommitted)
		fe := frontEndLsf{
			a:          aF,
			nlsf:       nlsfF,
			prevLsfq:   prevForFrame,
			prevVoiced: s.es.prevVoiced,
			intf:       f,
		}
		_, _, brec, _ := fe.quantize(synthT, 1, prevForFrame)
		predcoefs, _ := smplLpcInterpol(brec, prevForFrame)
		s.lsf = append(s.lsf, analysisLsfInputs{
			a:        aF,
			nlsf:     nlsfF,
			prevNlsf: prevForFrame,
		})
		prevCommitted = brec
		// The residual window for frame `f`, with its 32-sample CELP pre-lead.
		nbase := resLead + f*smplIntfLen
		winN := slices.Clone(s.es.xn[nbase-resLead : nbase+smplIntfLen])
		resLpc := make([]float32, smplIntfLen)
		for sf := 0; sf < smplSubfrCount; sf++ {
			r := smplAnalysisResidualSubfr(&predcoefs[sf], winN, sf)
			copy(resLpc[sf*smplSubfrLen:(sf+1)*smplSubfrLen], r[:])
		}
		s.celp = append(s.celp, analysisCelpInputs{
			predcoefs: predcoefs,
			resLpc:    resLpc,
			blockLags: blockLags,
			percCorrs: percCorrs,
		})
	}
	// Warm the entropy coder's own lazily-built tables. The analysis path above never calls
	// `encodeSmplFrameInto`, so the range-coder CC tables are still cold here: leaving them to the
	// first timed body charged one `entropyEncode` sample with a table build a live stream pays
	// once, reported as if it were per-frame work.
	s.entropyEncode()
	return s
}

// nextIntf advances this row's cursor and returns the internal-frame index to run, 0 -> 1 -> 2 ->
// 0.
func (s *analysisStages) nextIntf(row int) int {
	intf := s.intfAt[row] % 3
	s.intfAt[row]++
	return intf
}

// ctx borrows the live per-stream state as the celpFrameCtx the analyzer builds each internal
// frame (Rust `with_ctx`). Pure borrows (no allocation), so calling it inside a timed body costs
// nothing the analyzer does not also pay.
func (s *analysisStages) ctx(intf int, blockLags [smplSubfrCount][2]float32) celpFrameCtx {
	if s.es.celp == nil || s.es.perc == nil || s.es.bitrate == nil {
		panic("primed by warmup")
	}
	return celpFrameCtx{
		celp:               s.es.celp,
		perc:               s.es.perc,
		percPrev:           &s.es.percPrev,
		bitrate:            s.es.bitrate,
		hpN:                s.hp,
		intf:               intf,
		spActProb:          1.0,
		codedAsActiveVoice: true,
		f2:                 s.f2,
		voicingStrength:    1.0,
		vuv:                &s.es.vuv,
		hpPitchHist:        s.hpPitchHist,
		ltpBuf:             s.es.ltpBuf,
		pitchEst:           &s.es.pitchEst,
		blockLags:          blockLags,
	}
}

// fft512Forward is one forward real FFT at the LPC size (N=512, pure radix-2). Runs 3x per 60 ms
// frame, once per internal frame, inside lpcFrontEnd.
func (s *analysisStages) fft512Forward() float32 {
	rfftForwardOrderedSc(s.fftIn, s.fftOut, &s.fftScratch)
	return s.fftOut[0]
}

// fft576Roundtrip is one forward plus one inverse real FFT at the perceptual-model size (N=576 =
// 2^6 * 3^2, mixed radix 2 and 3). `smplPercModel` runs exactly this pair, 2x per internal frame,
// so 6x per 60 ms frame -- 12 of the frame's 15 FFTs.
func (s *analysisStages) fft576Roundtrip() float32 {
	rfftForwardOrderedSc(s.fft576Time, s.fft576Spec, &s.fft576Scratch)
	rfftBackwardOrderedSc(s.fft576Spec, s.fft576Out, &s.fft576Scratch)
	return s.fft576Out[0]
}

// lpcFrontEnd is the full LPC front-end for one internal frame: window, forward FFT, power
// spectrum, DCT autocorrelation, Levinson, bandwidth expansion, and A->NLSF. Runs 3x per 60 ms
// frame.
//
// Cycles the internal-frame index because `smplAnalyzeFrameSt` passes `f < 2` as
// `useLongWin`: internal frames 0 and 1 take the long trailing window, frame 2 the short one, and
// the two differ in window-generation work.
func (s *analysisStages) lpcFrontEnd() float32 {
	intf := s.nextIntf(analysisRowLpc)
	windowed := smplWindowLpc20(&s.lpcbufs[intf], intf < 2)
	a, _ := smplLpcAnalyzeWithF2(&windowed, &s.fftScratch)
	nlsf := smplA2nlsf16(a[:])
	return nlsf[0]
}

// percCorrsFrame is the perceptual model for one internal frame: two `smplPercModel` calls, each a
// forward plus an inverse FFT at N=576 (the mixed-radix 2/3 size). Runs 1x per internal frame, so
// 3x per 60 ms frame -- 12 of the frame's 15 FFTs. Advances the perc history, as production does.
//
// Cycles the internal-frame index: `computePercCorrs` sets `isLast` only on frame 2's final
// subframe, which switches `smplPercModel` to the short trailing window, so a row pinned to frame
// 1 would never reach one of the frame's six invocations.
func (s *analysisStages) percCorrsFrame() float32 {
	intf := s.nextIntf(analysisRowPerc)
	cs := s.ctx(intf, [smplSubfrCount][2]float32{})
	corrs := computePercCorrs(&cs)
	return corrs[0][0]
}

// pitchSearch is the multi-stage pitch estimator for one internal frame. Runs 3x per 60 ms frame
// and advances the cross-frame lag predictor, as production does.
//
// Resets the lag predictor at each packet boundary, because `smplAnalyzeFrameSt` calls
// `resetCond` after internal frame 2: a packet is one non-conditional search followed by two
// conditional ones, and those take different candidate paths. Without the reset every timed call
// would be conditional and the x3 would not be a frame.
func (s *analysisStages) pitchSearch() float32 {
	intf := s.nextIntf(analysisRowPitch)
	if intf == 0 {
		s.es.pitchEst.resetCond()
	}
	inp := &s.pitch[intf]
	pr := smplPitch(&s.es.pitchEst, inp.ltpBuf, &inp.f2, true)
	return pr.pitchcorr
}

// lsfQuantize is the bit-exact LSF vector quantizer plus the decoder-side envelope reconstruction
// and NLSF->A that follow it (`frontEndLsf.quantize`). Runs 1x per internal frame, 3x per 60 ms
// frame.
//
// The row cycles `intf` 0, 1, 2 because the two paths cost differently and production runs both
// every frame: the conditional quantizer needs `intf > 0`, so internal frame 0 always takes
// `lsfQuant` while 1 and 2 take `lsfQuantCond` (which carries an extra centroid and rotated
// weighting matrices). Pinning the row to one path and multiplying by three would mis-state the
// frame; cycling makes the x3 the real mix. Stateless, so a full cycle measures the same work
// every time.
func (s *analysisStages) lsfQuantize() float32 {
	intf := s.nextIntf(analysisRowLsf)
	inp := &s.lsf[intf]
	fe := frontEndLsf{
		a:          inp.a,
		nlsf:       inp.nlsf,
		prevLsfq:   inp.prevNlsf,
		prevVoiced: true,
		intf:       intf,
	}
	grid, _, _, predcoef := fe.quantize(loadSmplSynthTables(), 1, inp.prevNlsf)
	return predcoef[1] + float32(grid)
}

// celpSubframesFrame is the CELP excitation encoder for one internal frame: the perceptual
// weighting filters plus four `encodeSubframe` calls (ACB gain search, then the fixed-codebook
// pulse search that dominates it). Runs 1x per internal frame, so `encodeSubframe` runs 12x per
// 60 ms frame. Advances the ACB/ZIR state, as production does.
func (s *analysisStages) celpSubframesFrame() float32 {
	intf := s.nextIntf(analysisRowCelp)
	inputs := &s.celp[intf]
	cs := s.ctx(intf, inputs.blockLags)
	outs := runCelpSubframes(
		&cs,
		&inputs.predcoefs,
		inputs.resLpc,
		&inputs.blockLags,
		&inputs.percCorrs,
		smplPercEmphV,
		1,
	)
	sum := int32(0)
	for i := range outs {
		sum += int32(outs[i].nPulses[1])
	}
	return float32(sum)
}

// analyzeFrame is the whole analysis half of `Encode`: everything above, plus the VAD, the input
// high-pass and the voiced/unvoiced decision. 1x per 60 ms frame.
//
// This plus entropyEncode is the CODEC work, not quite all of `AppendEncode`: the public wrapper
// also sanitizes its 960 input samples (NaN -> 0, clamp, copy).
func (s *analysisStages) analyzeFrame() uint8 {
	frame := s.pcm[s.pcmAt%len(s.pcm)]
	s.pcmAt++
	fp := smplAnalyzeFrameSt(&s.es, frame, nil)
	return fp.toc
}

// entropyEncode is the range coder writing the analyzed parameters to the wire. 1x per 60 ms
// frame.
func (s *analysisStages) entropyEncode() int {
	fp := &s.fps[s.fpAt%len(s.fps)]
	s.fpAt++
	out, err := encodeSmplFrameInto(fp, &s.rng, s.out[:0])
	if err != nil {
		panic("analyzed params encode: " + err.Error())
	}
	s.out = out
	return len(s.out)
}

// The two rows whose inputs are fully captured must repeat with period 3 -- one packet -- so their
// measured cost is a property of the frame index, not of how long the benchmark happened to run.
// `celpSubframesFrame` is deliberately excluded: its excitation state keeps evolving against the
// replayed residual, which the file doc records as a known limitation.
func TestAnalysis_StageRowsAreThreePeriodic(t *testing.T) {
	s := newAnalysisStages()
	pitch := make([]uint32, 12)
	for i := range pitch {
		pitch[i] = math.Float32bits(s.pitchSearch())
	}
	perc := make([]uint32, 12)
	for i := range perc {
		perc[i] = math.Float32bits(s.percCorrsFrame())
	}
	for i := 3; i < 12; i++ {
		if pitch[i] != pitch[i-3] {
			t.Errorf("pitch_search drifted at iteration %d", i)
		}
		if perc[i] != perc[i-3] {
			t.Errorf("perc_model_frame drifted at iteration %d", i)
		}
	}
}

// One Go benchmark per stage row.

var analysisBenchSink float64

func analysisBenchRow(b *testing.B, row func(*analysisStages) float64) {
	s := newAnalysisStages()
	b.ReportAllocs()
	b.ResetTimer()
	acc := 0.0
	for i := 0; i < b.N; i++ {
		acc += row(s)
	}
	analysisBenchSink = acc
}

func BenchmarkAnalysis_Fft512Forward(b *testing.B) {
	analysisBenchRow(b, func(s *analysisStages) float64 { return float64(s.fft512Forward()) })
}

func BenchmarkAnalysis_Fft576Roundtrip(b *testing.B) {
	analysisBenchRow(b, func(s *analysisStages) float64 { return float64(s.fft576Roundtrip()) })
}

func BenchmarkAnalysis_LpcFrontEnd(b *testing.B) {
	analysisBenchRow(b, func(s *analysisStages) float64 { return float64(s.lpcFrontEnd()) })
}

func BenchmarkAnalysis_PercCorrsFrame(b *testing.B) {
	analysisBenchRow(b, func(s *analysisStages) float64 { return float64(s.percCorrsFrame()) })
}

func BenchmarkAnalysis_PitchSearch(b *testing.B) {
	analysisBenchRow(b, func(s *analysisStages) float64 { return float64(s.pitchSearch()) })
}

func BenchmarkAnalysis_LsfQuantize(b *testing.B) {
	analysisBenchRow(b, func(s *analysisStages) float64 { return float64(s.lsfQuantize()) })
}

func BenchmarkAnalysis_CelpSubframesFrame(b *testing.B) {
	analysisBenchRow(b, func(s *analysisStages) float64 { return float64(s.celpSubframesFrame()) })
}

func BenchmarkAnalysis_AnalyzeFrame(b *testing.B) {
	analysisBenchRow(b, func(s *analysisStages) float64 { return float64(s.analyzeFrame()) })
}

func BenchmarkAnalysis_EntropyEncode(b *testing.B) {
	analysisBenchRow(b, func(s *analysisStages) float64 { return float64(s.entropyEncode()) })
}
