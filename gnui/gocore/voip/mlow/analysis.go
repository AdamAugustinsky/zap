// Ported from whatsapp-rust 5e72baf wacore/src/voip/mlow/analysis.rs, MIT.
//
// MLow ENCODER ANALYSIS: PCM -> `smplFrameParams`. Per internal frame the LPC front-end windows the
// 20 ms `lpcbuf`, FFT-autocorrelates it, derives the bandwidth-expanded `A` and its NLSF, and feeds
// the bit-exact LSF quantizer (with the conditional-coding path); the resulting `grid`/`stage2` map
// directly onto the wire and the decoder reconstructs the same envelope. The excitation comes from
// the CELP encoder over the per-subframe interpolated LPC residual. The UNVOICED level is the
// bit-exact `nrg_res` floor (the wire gain block IS the nrgres layout), with the per-subframe FCB
// gain index as `nrg_res`. The VOICED (LTP, stage1=1) path runs the real CELP ACB/LTP encode: pitch
// comes from a perceptually-weighted (`w_speech`) search and the `smpl_get_signal_mode` classifier;
// the CELP's `acb_idx`/`fcb_idx`/pulses drive the wire pitch block (decoder-reconstructed lags feed
// the ACB basis so encode/decode LTP agree). Closed-loop: decode(encode(analyze(pcm))) tracks the
// input.
//
// Go port notes (allocation only; the arithmetic and its order are unchanged):
//   - Rust's per-frame `Vec`s that this file owns (perc_corrs, the CELP outputs, the residual
//     buffers, the autocorrelation, the pulse sign list, the wnrg list) are fixed-size arrays here,
//     so the `mem::take` lending dance around `perc_corrs` disappears: `celpFrameCtx.percCorrs` is
//     an array the callees read through a pointer.
//   - `smplBuildPulseParams` appends into caller-supplied buffers (`smplEncoderState` keeps one
//     pair per internal frame), so the `smplFrameParams` that `smplAnalyzeFrameSt` returns aliases
//     encoder scratch and stays valid only until the next call on the same state.

package mlow

import "math"

// smplLpcHistLen is the HP-history samples to carry for the LPC window buffer. The `lpcbuf` for
// internal frame 0 reaches 96 samples before the current packet; carrying the full `lpc_buf_mem`
// (144) is safe and exact.
const smplLpcHistLen = 144

// smplLpcPre: `lpcbuf` starts 96 samples before each internal frame
// (`-WINNEXT_WB_LEN + framelen + WINNEXT_WB_LONG_LEN - lpcbuf_len`).
const smplLpcPre = 96

// smplLsfSurv is `surv = lsf_surv` for complexity 8 (`update_complexity_setting`).
const smplLsfSurv = 6

// SMPL_WINNEXT_WB_LEN (2 ms analysis lookahead; zero at 16 kHz, no band split) is shared.go's
// smplWinnextWbLen.

// smplLsfRdwAdj is `RDw_adj = sqrt(mainBitRate / 14000)` for the HIGH-rate (lowRate=0) path at
// 20 kbps.
const smplLsfRdwAdj float32 = 1.1952286

// smplEncoderState is the cross-frame analysis state: only the LPC-analysis input history persists
// (the decoder rebuilds synthesis state per 60 ms frame).
type smplEncoderState struct {
	hist []float64
	// Reused for f32 input conversion. The i16 path lends its original PCM to VAD.
	vadPcm []int16
	// Reused to avoid four packet-sized allocations on the realtime encode path.
	hp     []float32
	x      []float64
	xn     []float32
	hpFull []float32
	// Input high-pass (ARMA2, fcorner 35 Hz) coefficients + carried state, matching the real
	// encoder. Rust `Option<([f32; 3], [f32; 3])>`: hpCoefsSet is the Some flag.
	hpCoefsSet bool
	hpMa       [3]float32
	hpAr       [3]float32
	hpState    [4]float32
	// Persistent CELP excitation encoder (acb/zir/prev-idx state carries across subframes &
	// frames). nil = None.
	celp *celpEncoder
	// Perceptual-weighting model state (FFT history) for the per-subframe `perc_wght_resp`.
	perc *percModelState
	// Previous-pair perceptual autocorrelation, for the WB even-subframe interpolation.
	percPrev []float32
	// Bitrate controller (per-subframe pulse budget + importance), carried across frames.
	bitrate *bitrateController
	// HP-filtered input history (normalized, [-1,1]) for the LPC window buffer, mirroring the C
	// `lpc_buf_mem`: the last `smplLpcHistLen` HP samples of the previous packet.
	lpcHist []float32
	// Previous internal frame's committed (reconstructed) NLSF, for conditional LSF coding.
	prevLsfq []float32
	// Whether the previous internal frame was voiced (for the cond-coding condition).
	prevVoiced bool
	// SILK VAD: per-internal-frame speech-activity probability + the coded_as_active_voice flag
	// the bitrate controller and the voiced/unvoiced classifier read.
	vad *smplVadState
	// Voicing-classifier hysteresis + spectral-tilt background tracker (`VUV_Mode`), per stream.
	vuv vuvMode
	// Last `smplPitchLagMax` HP samples of the previous packet, so the first internal frame's
	// pitch search has real history instead of zeros.
	hpPitchHist []float32
	// Persistent perceptually-weighted speech buffer (`ltp_buf`, length `MAX_LTP_BUF_LEN`), shifted
	// left by one internal-frame each call. The full pitch estimator reads its tail.
	ltpBuf []float32
	// Cross-frame pitch-estimator predictor (`PitchEstimator` non-scratch fields).
	pitchEst pitchEstState
	// Reusable scratch for the LPC-analysis FFT: the fixed 512-pt twiddle tables are built once
	// per stream instead of every internal frame. Lazily initialized on first analyze.
	lpcFft *fftScratch

	// Go-only: per-internal-frame buffers the pulse params of the returned smplFrameParams are
	// built in (Rust allocates fresh `mag_runs`/`sign_syms` Vecs per frame).
	magRunsPool  [3][]int32
	signSymsPool [3][]smplRawSym
	// Go-only: the shadow synthesis Rust builds fresh (`SmplFrameSynth::default()`) per packet,
	// reset in place instead so its LTP history is not reallocated.
	shadow smplFrameSynth
	// Go-only: per-internal-frame storage for the committed NLSF each frame hands the next as
	// `prevNlsf`/`prevLsfq` (Rust returns a fresh Vec). One buffer per frame index, so frame f
	// never writes the buffer it reads (frame f-1's, or frame 2's of the previous packet via
	// `prevLsfq`).
	nlsfBufs [3][smplOrder]float32
}

// defaultSmplEncoderState is the derived Default: empty buffers, every Option None, and the
// manual Defaults of vuvMode and pitchEstState.
func defaultSmplEncoderState() smplEncoderState {
	return smplEncoderState{
		vuv:      defaultVuvMode(),
		pitchEst: defaultPitchEstState(),
	}
}

// smplMainBitRate is the assumed encoder bitrate for the active MLow 1:1 config (the recorded
// capture's main rate is not known a priori; this drives the per-subframe pulse budget via the
// bitrate controller).
const smplMainBitRate int32 = 20000
const smplComplexity int32 = 8

const smplCelpLowRate = false
const smplCelpPercRespLen = 32
const smplCelpFcbSubfrlen = 80

// smplCelpSubfrPerPacket: 12 subframes per 60 ms packet (4 subframes/internal frame x 3 internal
// frames).
const smplCelpSubfrPerPacket = 12

// smplPercRLen is `perc_resp_len + SMPL_PERC_EMPH_V_LEN - 1` (= 33 = SMPL_MAX_L_RESP): the
// perceptual autocorrelation length the perc model returns and `smplPercAc2aInto` consumes.
const smplPercRLen = smplCelpPercRespLen + 1

// smplFcbTotSurv20msMax is `smpl_fcb_tot_surv_20ms_max` for complexity 5-8 (the perc_resp_len=32
// path). Drives `tot_surv`.
const smplFcbTotSurv20msMax int32 = 100

// smplEncHpFcornerHz is the encoder input high-pass 3 dB corner (`SMPL_ENC_HP_FCORNER_3DB_HZ`).
const smplEncHpFcornerHz float32 = 35.0

func unvoicedPitch() smplPitchSynth {
	return smplPitchSynth{
		voiced:   false,
		lagSubfr: [4]float64{},
		normGain: 0.0,
	}
}

type candidate struct {
	ip       smplInternalParams
	stage1   int32
	grid     int32
	qsym     [16]int32
	pulseVec [smplIntfLen]int32
	// Per-subframe excitation gainQ used by the synthesis (rate-control gain for unvoiced, 0 for
	// voiced). Must match what `commitCandidate` feeds the shadow synth (warm history).
	gainQ [4]int32
	// LTP parameters for the synthesis (`voiced=false` for unvoiced).
	pitch  smplPitchSynth
	silent bool
}

// celpFrameCtx is the borrowed CELP/perceptual state for one internal frame's excitation
// analysis.
type celpFrameCtx struct {
	celp     *celpEncoder
	perc     *percModelState
	percPrev *[]float32
	bitrate  *bitrateController
	// Full normalized HP frame (960 samples, [-1,1]); the perc model windows slices of it.
	hpN []float32
	// Internal-frame index (0..3) within the 60 ms packet.
	intf int
	// SILK VAD speech-activity probability for this internal frame (bitrate controller input).
	spActProb float32
	// Packet-level coded_as_active_voice (BACKGROUND_NOISE frame_type + voiced gating).
	codedAsActiveVoice bool
	// LPC power spectrum `F2[0..256]` for the voicing classifier's spectral tilt.
	f2 [smplFLen]float32
	// This frame's classifier voicing_strength, fed to the bitrate controller's importance/pulse-
	// budget computation.
	voicingStrength float32
	// Voicing-classifier hysteresis state, threaded across the whole stream.
	vuv *vuvMode
	// Previous packet's HP tail (`smplPitchLagMax` samples) for the intf=0 pitch history.
	hpPitchHist []float32
	// Persistent perceptually-weighted speech buffer (`ltp_buf`), carried across frames; the full
	// pitch estimator reads its tail.
	ltpBuf []float32
	// Cross-frame pitch-estimator predictor.
	pitchEst *pitchEstState
	// Per-subframe perceptual autocorrelation (shared CELP + pitch input), computed once per
	// frame. Rust `Vec<Vec<f32>>`; every row is `smplPercRLen` long.
	percCorrs [smplSubfrCount][smplPercRLen]float32
	// Decoder-reconstructed per-block pitch lags (2 per subframe) for the voiced CELP ACB. The
	// CELP builds its ACB basis from these so the encoder/decoder LTP contributions agree on the
	// wire.
	blockLags [smplSubfrCount][2]float32
	// Go-only: the buffers smplBuildPulseParams appends this frame's pulse symbols to.
	magRunsBuf  []int32
	signSymsBuf []smplRawSym
}

// smplAnalyzeFrameSt turns one 60 ms PCM frame (960 f32 @16 kHz, ~[-1,1]) into params, advancing
// `es`. If non-nil, `originalPcm` must contain the 960 i16 samples whose normalization produced
// `pcm`. VAD borrows them directly; the f32 path instead reuses its conversion buffer.
//
// The returned params' pulse slices alias `es` scratch and are valid until the next call.
func smplAnalyzeFrameSt(es *smplEncoderState, pcm []float32, originalPcm []int16) smplFrameParams {
	need := smplIntfLen * 3
	if len(pcm) < need {
		owned := make([]float32, need)
		copy(owned[:len(pcm)], pcm)
		pcm = owned
	}
	synthT := loadSmplSynthTables()

	// SILK VAD on the int16 input PCM (runs on the raw API samples, before the encoder HP).
	// Produces the per-internal-frame speech-activity probability + the packet
	// coded_as_active_voice.
	var vadPcm []int16
	if originalPcm != nil {
		// Rust debug_asserts here that originalPcm has `need` samples whose normalization is
		// bit-identical to pcm; both public entry points guarantee it.
		vadPcm = originalPcm
	} else {
		es.vadPcm = es.vadPcm[:0]
		for _, s := range pcm[:need] {
			es.vadPcm = append(es.vadPcm, f32ToI16(clampF32(roundF32(s*32768.0), -32768.0, 32767.0)))
		}
		vadPcm = es.vadPcm
	}
	if es.vad == nil {
		v := newSmplVadState()
		es.vad = &v
	}
	vad := es.vad.processPacket(vadPcm, smplIntfLen)
	spActProb := vad.vadResults
	codedAsActiveVoice := vad.codedAsActiveVoice
	speechDetected := vad.speechDetected

	// Encoder input high-pass (ARMA2, fcorner 35 Hz), matching the real encoder. Removes the
	// low-frequency content the decoder's de-emphasis would otherwise over-amplify; the residual
	// the analysis codes is then in the same band the real codec quantizes.
	if !es.hpCoefsSet {
		es.hpMa, es.hpAr = smplGetHpCoefs(smplEncHpFcornerHz)
		es.hpCoefsSet = true
	}
	hpMa, hpAr := es.hpMa, es.hpAr
	es.hp = analysisZeroedF32(es.hp, need)
	smplFiltArma2(pcm[:need], need, hpMa, hpAr, &es.hpState, es.hp)

	// int16-scaled input with smplOrder lead samples of history.
	es.x = analysisZeroedF64(es.x, smplOrder+need)
	if len(es.hist) >= smplOrder {
		copy(es.x[:smplOrder], es.hist[len(es.hist)-smplOrder:])
	}
	for i := 0; i < need; i++ {
		es.x[smplOrder+i] = float64(es.hp[i]) * 32768.0
	}

	// Rust: `let mut shadow = SmplFrameSynth::default();` (reset in place, see `es.shadow`).
	es.shadow.reset()
	shadow := &es.shadow
	var prevNlsf []float32
	// Predictor mirror, fresh per 60 ms frame (mirrors encodeSmplFrameInto's fresh smplLsfState),
	// threaded across the 3 internal frames so the voiced abs-vs-delta lag choice matches the
	// entropy encoder.
	lstate := defaultSmplLsfState()

	// Lazily build the persistent CELP encoder + perceptual model (their state carries across
	// frames).
	if es.celp == nil {
		c := newCelpEncoder(
			smplCelpLowRate,
			smplCelpPercRespLen,
			smplCelpFcbSubfrlen,
			smplCelpSubfrPerPacket,
		)
		es.celp = &c
	}
	if es.perc == nil {
		p := newPercModelState()
		es.perc = &p
	}
	if es.bitrate == nil {
		b := newBitrateController()
		es.bitrate = &b
	}
	if len(es.percPrev) != smplPercRLen {
		es.percPrev = make([]float32, smplPercRLen)
	}

	// Normalized HP input for the CELP residual (the real encoder works in [-1,1], not int16).
	// `xhp_frame` for internal frame 0 starts `smplWinnextWbLen` (32) samples BEFORE the packet's
	// first sample (xhp_frame = xhp_packet_buf + SMPL_LPC_BUF_MEM_LEN, while x_in16k =
	// xhp_packet_buf + SMPL_LPC_BUF_MEM_LEN + SMPL_WINNEXT_WB_LEN), so the excitation it codes
	// leads the input by 32 samples. Carry smplOrder + 32 lead so the residual can read that far
	// back.
	resLead := smplOrder + smplWinnextWbLen
	es.xn = analysisZeroedF32(es.xn, resLead+need)
	if len(es.hist) >= resLead {
		for i := 0; i < resLead; i++ {
			es.xn[i] = float32(es.hist[len(es.hist)-resLead+i] / 32768.0)
		}
	}
	copy(es.xn[resLead:resLead+need], es.hp[:need])

	// Full HP-domain buffer that `lpcbuf` indexes: [history(144)] ++ [current 960 HP] ++
	// [32 zeros]. The 32-sample lookahead tail is zero at 16 kHz (no band split), per the buffer
	// layout.
	es.hpFull = analysisZeroedF32(es.hpFull, smplLpcHistLen+need+smplWinnextWbLen)
	if len(es.lpcHist) == smplLpcHistLen {
		copy(es.hpFull[:smplLpcHistLen], es.lpcHist)
	}
	copy(es.hpFull[smplLpcHistLen:smplLpcHistLen+need], es.hp[:need])

	hp := es.hp
	x := es.x
	xn := es.xn
	hpFull := es.hpFull

	// Snapshot the previous packet's HP tail (pitch history for this packet's intf=0), then
	// refresh it from this packet's tail for the next call.
	hpPitchHist := es.hpPitchHist
	if len(hpPitchHist) != smplPitchLagMax {
		hpPitchHist = analysisResizeF32(hpPitchHist, smplPitchLagMax)
	}

	// Lazily size the persistent perceptually-weighted speech buffer (`ltp_buf`).
	if len(es.ltpBuf) != maxLtpBufLen {
		es.ltpBuf = make([]float32, maxLtpBufLen)
	}

	celp := es.celp
	perc := es.perc
	bitrate := es.bitrate
	ltpBuf := es.ltpBuf
	pitchEst := &es.pitchEst

	prevLsfq := es.prevLsfq
	prevVoiced := es.prevVoiced

	var internal [3]smplInternalParams
	for i := range internal {
		internal[i] = defaultSmplInternalParams()
	}
	for f := 0; f < 3; f++ {
		base := smplOrder + f*smplIntfLen
		win := x[base-smplOrder : base+smplIntfLen]
		// `winN` carries resLead (smplOrder + res_pre) samples before the internal frame so the
		// residual can start res_pre samples early (matching the `xhp_frame` vs `x_in16k` offset).
		nbase := resLead + f*smplIntfLen
		winN := xn[nbase-resLead : nbase+smplIntfLen]

		// Front-end LPC analysis: window `lpcbuf` (448 samples starting 96 before this frame),
		// FFT-autocorrelate it, and derive `A`/NLSF. `use_long_win` is true except the last frame.
		lpcStart := smplLpcHistLen - smplLpcPre + f*smplIntfLen
		var lpcbuf [smplLpcBufLen]float32
		copy(lpcbuf[:], hpFull[lpcStart:lpcStart+smplLpcBufLen])
		windowed := smplWindowLpc20(&lpcbuf, f < 2)
		if es.lpcFft == nil {
			sc := newLpcFftScratch()
			es.lpcFft = &sc
		}
		a, f2 := smplLpcAnalyzeWithF2(&windowed, es.lpcFft)
		nlsf := smplA2nlsf16(a[:])

		cs := celpFrameCtx{
			celp:               celp,
			perc:               perc,
			percPrev:           &es.percPrev,
			bitrate:            bitrate,
			hpN:                hp,
			intf:               f,
			spActProb:          spActProb[f],
			codedAsActiveVoice: codedAsActiveVoice,
			f2:                 f2,
			voicingStrength:    0.0,
			vuv:                &es.vuv,
			hpPitchHist:        hpPitchHist,
			ltpBuf:             ltpBuf,
			pitchEst:           pitchEst,
			magRunsBuf:         es.magRunsPool[f][:0],
			signSymsBuf:        es.signSymsPool[f][:0],
		}
		fe := frontEndLsf{
			a:          a,
			nlsf:       nlsf,
			prevLsfq:   prevLsfq,
			prevVoiced: prevVoiced,
			intf:       f,
		}
		ip, nlsfOut, voicedOut := smplAnalyzeInternal(
			es.nlsfBufs[f][:],
			synthT,
			shadow,
			&lstate,
			f,
			win,
			winN,
			prevNlsf,
			&fe,
			&cs,
		)
		// nlsfOut is never written after this point, so the two predictors share it (Rust clones).
		prevNlsf = nlsfOut
		prevLsfq = nlsfOut
		prevVoiced = voicedOut
		internal[f] = ip
		// Keep the (possibly grown) pulse-symbol buffers for the next packet.
		if cap(ip.pulses.magRuns) > cap(es.magRunsPool[f]) {
			es.magRunsPool[f] = ip.pulses.magRuns[:0]
		}
		if cap(ip.pulses.signSyms) > cap(es.signSymsPool[f]) {
			es.signSymsPool[f] = ip.pulses.signSyms[:0]
		}
		// The C resets the lag-block predictor after the last internal frame of each packet (and
		// after any unvoiced frame, handled in smplAnalyzeInternal), so cond-coding restarts per
		// packet.
		if f == 2 {
			pitchEst.resetCond()
		}
	}

	// Carry smplOrder + smplWinnextWbLen history so the next packet's residual lead is filled.
	es.hist = append(es.hist[:0], x[len(x)-(smplOrder+smplWinnextWbLen):]...)
	// Carry the last 144 HP samples as next packet's LPC window history (mirrors `lpc_buf_mem`).
	es.lpcHist = append(es.lpcHist[:0], hp[need-smplLpcHistLen:need]...)
	copy(hpPitchHist, hp[need-smplPitchLagMax:need])
	es.hpPitchHist = hpPitchHist
	es.prevLsfq = prevLsfq
	es.prevVoiced = prevVoiced
	// 16 kHz, 60 ms, config 0, no SID: bit 4 of the frame-size index, plus the activity bits.
	// Speech detected -> VAD bit (0x50). Only the DTX hangover keeps it coded active -> bit 1
	// instead (0x12). Neither -> coded inactive (0x10), the shape the reference emits over silence
	// with DTX off, and the one a receiver can tell apart from speech.
	const toc60ms16k uint8 = 0x10
	var toc uint8
	if speechDetected {
		toc = toc60ms16k | 0x40
	} else if codedAsActiveVoice {
		toc = toc60ms16k | 0x02
	} else {
		toc = toc60ms16k
	}
	return smplFrameParams{
		toc:                toc,
		config:             0,
		codedAsActiveVoice: codedAsActiveVoice,
		internal:           internal,
	}
}

// analysisZeroedF32 is Rust's `v.resize(n, 0.0); v.fill(0.0)`, reusing the capacity.
func analysisZeroedF32(v []float32, n int) []float32 {
	if cap(v) < n {
		return make([]float32, n)
	}
	v = v[:n]
	clear(v)
	return v
}

// analysisZeroedF64 is analysisZeroedF32 for f64.
func analysisZeroedF64(v []float64, n int) []float64 {
	if cap(v) < n {
		return make([]float64, n)
	}
	v = v[:n]
	clear(v)
	return v
}

// analysisResizeF32 is Rust's `v.resize(n, 0.0)`: keeps the prefix, zero-fills any new tail.
func analysisResizeF32(v []float32, n int) []float32 {
	if len(v) >= n {
		return v[:n]
	}
	old := len(v)
	if cap(v) < n {
		nv := make([]float32, n)
		copy(nv, v)
		return nv
	}
	v = v[:n]
	clear(v[old:])
	return v
}

// frontEndLsf is the front-end LPC/NLSF analysis result for one internal frame, plus the
// conditional-coding context.
type frontEndLsf struct {
	// Post-BWE monic LPC `A[0..16]` (A[0]=1).
	a [smplLpcOrder + 1]float32
	// Analysis NLSF (`smpl_A2NLSF_16(A)`), radians 0..pi.
	nlsf [smplLpcOrder]float32
	// Previous internal frame's committed NLSF (for conditional coding).
	prevLsfq   []float32
	prevVoiced bool
	intf       int
	// Go-only: storage for the committed NLSF `quantize` returns (Rust returns a fresh Vec).
	committed [smplLpcOrder]float32
}

// SMPL_LPC_ORDER (16) is shared.go's smplLpcOrder.

// quantize runs the bit-exact LSF quantizer for `voiced` and the cond-coding condition, returning
// the wire grid + stage2 + the committed (decoder-reconstructed) NLSF + the quantized predcoef.
func (fe *frontEndLsf) quantize(
	synthT *smplSynthTables,
	voiced int,
	prevNlsf []float32,
) (int32, [16]int32, []float32, [17]float32) {
	cond := (fe.prevVoiced == (voiced != 0)) && fe.intf > 0
	var res lsfQuantResult
	if cond && len(fe.prevLsfq) == smplLpcOrder {
		res = lsfQuantCond(
			fe.a[:],
			fe.nlsf[:],
			fe.prevLsfq,
			voiced,
			0,
			smplLsfRdwAdj,
			smplLsfSurv,
		)
	} else {
		res = lsfQuant(
			fe.a[:],
			fe.nlsf[:],
			voiced,
			0,
			smplLsfRdwAdj,
			smplLsfSurv,
		)
	}
	grid := res.qi[0]
	var stage2 [16]int32
	copy(stage2[:], res.qi[1:smplLpcOrder+1])
	// Committed NLSF = the envelope the decoder rebuilds from the wire (proven == C qlsf).
	committed := smplReconstructNlsfInto(fe.committed[:], synthT, voiced, 0, int(grid), &stage2, prevNlsf)
	var aVqBuf [smplLpcOrder + 1]float32
	aVq := smplNlsf2aInto(aVqBuf[:], committed)
	var predcoef [17]float32
	for i, c := range aVq {
		if i >= 17 {
			break
		}
		predcoef[i] = c
	}
	predcoef[0] = 1.0
	return grid, stage2, committed, predcoef
}

// commitCandidate commits `cand` to the shadow synth `st` and returns its NLSF, written into
// `nlsfOut` (Go-only parameter; Rust returns a fresh Vec). `nlsfOut` must not alias `prevNlsf`.
func commitCandidate(
	nlsfOut []float32,
	synthT *smplSynthTables,
	st *smplFrameSynth,
	cand *candidate,
	prevNlsf []float32,
) []float32 {
	if cand.silent {
		nlsf := smplReconstructNlsfInto(
			nlsfOut,
			synthT,
			0,
			0,
			int(cand.ip.lsf.grid),
			&cand.ip.lsf.stage2,
			prevNlsf,
		)
		var pulseVec [smplIntfLen]int32
		// Rust discards this NLSF; it is the one just reconstructed above.
		var discard [smplOrder]float32
		synthInternalFrameInto(
			discard[:],
			synthT,
			st,
			0,
			0,
			int(cand.ip.lsf.grid),
			&cand.ip.lsf.stage2,
			prevNlsf,
			pulseVec[:],
			&cand.gainQ,
			&cand.pitch,
		)
		return nlsf
	}
	_, nlsf := synthInternalFrameInto(
		nlsfOut,
		synthT,
		st,
		int(cand.stage1),
		0,
		int(cand.grid),
		&cand.qsym,
		prevNlsf,
		cand.pulseVec[:],
		&cand.gainQ,
		&cand.pitch,
	)
	return nlsf
}

func smplUnvoicedCandidate(
	synthT *smplSynthTables,
	_ *smplFrameSynth,
	win []float64,
	winN []float32,
	prevNlsf []float32,
	fe *frontEndLsf,
	cs *celpFrameCtx,
) candidate {
	frame := win[smplOrder:]

	var ac [1]float64
	smplAutocorr(frame, 0, ac[:])
	r0 := ac[0]
	if r0 <= 0.0 {
		// Silent frame: still advance the CELP excitation state (zeros) so it stays in sync.
		var flat [smplSubfrCount][17]float32
		for i := range flat {
			flat[i][0] = 1.0
		}
		var zeroRes [smplIntfLen]float32
		var zeroLags [smplSubfrCount][2]float32
		runCelpSubframes(
			cs,
			&flat,
			zeroRes[:],
			&zeroLags,
			&cs.percCorrs,
			smplPercEmphUv,
			0,
		)
		return smplSilentInternal(synthT)
	}

	// LSF: bit-exact C quantizer fed the faithful front-end NLSF. `grid`/`stage2` map directly
	// onto the wire (grid==16 = the cond centroid); `brec` is the decoder-reconstructed envelope
	// (== C qlsf).
	bgrid, bsym, brec, _ := fe.quantize(synthT, 0, prevNlsf)

	// Per-subframe interpolated LPC (smpl_lpc_interpol): early subframes blend the previous
	// frame's committed NLSF with this frame's, smoothing the spectral transition the residual is
	// whitened by. The interpolation search tries idx 1 too and keeps it when it lowers the
	// residual energy.
	var resLpc [smplIntfLen]float32
	predcoefs, interpolIdx := smplLsfInterpolSearch(brec, fe.prevLsfq, winN, cs.codedAsActiveVoice, &resLpc)

	// Run the CELP excitation encoder per subframe (each with its interpolated predcoef).
	var zeroLags [smplSubfrCount][2]float32
	celpOut := runCelpSubframes(
		cs,
		&predcoefs,
		resLpc[:],
		&zeroLags,
		&cs.percCorrs,
		smplPercEmphUv,
		0,
	)

	// Map CELP pulses -> per-position pulse train; collect the per-subframe FCB gain index (= the
	// wire `nrg_res` symbol, which the decoder reads back as `fcbg_idx`).
	var pulseVec [smplIntfLen]int32
	var fcbgIdx [4]int32
	const mainIdx = 1
	for sf := 0; sf < smplSubfrCount; sf++ {
		out := &celpOut[sf]
		for _, v := range out.pulses[mainIdx][:max(out.nPulses[mainIdx], 0)] {
			// Same unpacking as the C: sign = 1 + 2*(v>>15); pos = v*sign - 1; pPulses[pos] += sign.
			sign := 1 + 2*(int32(v)>>15)
			pos := (int32(v) * sign) - 1
			if pos >= 0 && pos < int32(smplSubfrLen) {
				pulseVec[sf*smplSubfrLen+int(pos)] += sign
			}
		}
		fcbgIdx[sf] = int32(out.gainIdx[mainIdx])
	}

	// Unvoiced LEVEL (`nrgres`): bit-exact `smpl_quant_nrg_res` on the per-subframe residual
	// energy. The wire gain block IS the nrgres layout (gain_main=nrgres_frame_qi,
	// gain_delta=nrgres_shape_qi, gain_tab==nrgres_shape_CB, cb1==step) so the decoder reads
	// `gain_q[sf]` back as `nrgres_dbq_Q14`.
	var nrgres [4]float32
	for sf := range nrgres {
		res := resLpc[sf*smplSubfrLen : (sf+1)*smplSubfrLen]
		// `reslpc` (hence `nrgres`) is in the normalized [-1,1] domain (the encoder works in
		// [-1,1]).
		e := analysisNegZero
		for _, v := range res {
			e += float32(v * v)
		}
		nrgres[sf] = e / float32(smplSubfrLen)
	}
	nq := quantNrgRes4(&nrgres)
	gm := nq.frameQi
	gd := nq.shapeQi
	// Synthesis `gain_q[sf]` = the reconstructed per-subframe nrgres floor.
	gainQ := nq.dbqQ14

	pp := smplBuildPulseParams(pulseVec[:], cs.magRunsBuf, cs.signSymsBuf)
	gains := smplGainParams{
		gainMain:  gm,
		gainDelta: gd,
		nrgRes:    [4]int32{-1, -1, -1, -1},
	}
	for sf := 0; sf < 4; sf++ {
		// The wire writes a per-subframe nrg_res (= fcbg_idx) only where pulses exist.
		if pp.subfr[sf] > 0 {
			gains.nrgRes[sf] = fcbgIdx[sf]
		} else {
			gains.nrgRes[sf] = -1
		}
	}

	return candidate{
		ip: smplInternalParams{
			lsf: smplLsfParams{
				stage1: 0,
				grid:   bgrid,
				stage2: bsym,
				// lsf_interpol_idx: the decoder interpolates the per-subframe envelope with this,
				// so it must match the index the residual was whitened under.
				extra: interpolIdx,
			},
			pulses: pp,
			pitch:  defaultSmplPitchParams(),
			gains:  gains,
		},
		stage1:   0,
		grid:     bgrid,
		qsym:     bsym,
		pulseVec: pulseVec,
		gainQ:    gainQ,
		pitch:    unvoicedPitch(),
		silent:   false,
	}
}

// analysisNegZero is the -0.0 seed of Rust's `Iterator::sum::<f32>()`.
var analysisNegZero = float32(math.Copysign(0, -1))

// runCelpSubframes is the per-subframe perceptual weighting + CELP excitation for one internal
// frame (4 subframes of 80). Returns the per-subframe CELP outputs; mutates the CELP/perc state so
// it stays in sync. `blockLags` holds the per-40-sample-block pitch lags in samples (0 =
// unvoiced); `emph` selects the perceptual emphasis (UV vs V) and `voiced` drives the bitrate
// controller.
func runCelpSubframes(
	cs *celpFrameCtx,
	predcoefs *[smplSubfrCount][17]float32,
	resLpc []float32,
	blockLags *[smplSubfrCount][2]float32,
	percCorrs *[smplSubfrCount][smplPercRLen]float32,
	emph [2]float32,
	voiced int32,
) [smplSubfrCount]celpSubframeOut {
	percWght := percCorrsToWght(percCorrs, emph, smplCelpPercRespLen)
	var outs [smplSubfrCount]celpSubframeOut

	// Per-subframe weighted target energy (the bitrate controller's `wnrg`). The C uses the
	// perceptually-weighted speech energy; the residual energy in the int16 domain is a faithful
	// proxy for the relative magnitudes the smoothing + importance ratios consume.
	var wnrgs [smplSubfrCount]float32
	for sf := 0; sf < smplSubfrCount; sf++ {
		res := resLpc[sf*smplSubfrLen : (sf+1)*smplSubfrLen]
		scale := float32(32768.0)
		s := analysisNegZero
		for _, v := range res {
			s += float32(float32(v*scale) * float32(v*scale))
		}
		wnrgs[sf] = s
	}

	enc := bitrateControllerInputs{
		internalSampleRate:       16000,
		payloadSizeMs:            60,
		fecBitRate:               0,
		mainBitRate:              smplMainBitRate,
		complexity:               smplComplexity,
		useFecRateCompensation:   0,
		useDtx:                   0,
		subFrameImportanceFactor: 1.0,
	}

	for sf := 0; sf < smplSubfrCount; sf++ {
		wnrg := wnrgs[sf]
		var wnrgNext float32
		if sf+1 < smplSubfrCount {
			wnrgNext = wnrgs[sf+1]
		} else {
			wnrgNext = wnrgs[sf]
		}
		var nonflatness float32
		if voiced != 0 {
			nonflatness = 0.0
		} else {
			nonflatness = 2.0
		}
		// Real classifier voicing_strength (`voicing_strength_buf`), negative for unvoiced.
		voicingStrength := cs.voicingStrength
		maxPulses, importance := cs.bitrate.control(
			&enc,
			0,
			b2i32(cs.codedAsActiveVoice),
			cs.spActProb,
			nonflatness,
			voicingStrength,
			voiced,
			wnrg,
			wnrgNext,
			0,
			320,
			80,
		)
		var numsurv [smplMaxPulsesPerSf]int16
		for i := range numsurv {
			numsurv[i] = 1
		}
		totSurv := 1000 * (smplFcbTotSurv20msMax * int32(smplCelpFcbSubfrlen)) / (20 * 16000)
		smplDistributeFcbSurv(numsurv[:], int32(maxPulses[1]), totSurv)

		// The two 40-sample sub-blocks of this subframe carry their own (decoder-reconstructed)
		// lags; index 2 is read by the encoder as the trailing lag (`lags[n_lags-1]`).
		lags := [3]float32{blockLags[sf][0], blockLags[sf][1], blockLags[sf][1]}

		res := resLpc[sf*smplSubfrLen : (sf+1)*smplSubfrLen]
		outs[sf] = cs.celp.encodeSubframe(
			res,
			&predcoefs[sf],
			percWght[sf][:],
			lags[:],
			importance,
			maxPulses,
			numsurv[:],
		)
	}
	return outs
}

// SMPL_MAX_PULSES_PER_SF (40) is shared.go's smplMaxPulsesPerSf.

// computePercCorrs is the per-subframe perceptual autocorrelation (`perc_corrs_buf`, length
// `smplPercRLen`), the shared input to BOTH the CELP weighting and the pitch-perceptual
// weighting. The WB path computes the autocorrelation for odd subframes over a subframe-pair
// window and interpolates the even ones. Advances the perc-model state, so it must run EXACTLY
// ONCE per internal frame.
func computePercCorrs(cs *celpFrameCtx) [smplSubfrCount][smplPercRLen]float32 {
	frameMs := int32(20)
	const shorter = 32 // SMPL_WINNEXT_WB_LONG_LEN - SMPL_WINNEXT_WB_LEN
	var corrs [smplSubfrCount][smplPercRLen]float32
	sf := 1
	for sf < smplSubfrCount {
		start := cs.intf*smplIntfLen + (sf-1)*smplSubfrLen
		const xlen = 2*smplSubfrLen + shorter
		var xsubfr [xlen]float32
		for i := 0; i < xlen; i++ {
			idx := start + i
			if idx < len(cs.hpN) {
				xsubfr[i] = cs.hpN[idx]
			} else {
				xsubfr[i] = 0.0
			}
		}
		isLast := b2i32(cs.intf == 2 && sf == smplSubfrCount-1)
		var rBuf [smplPercRLen]float32
		r := smplPercModelInto(cs.perc, xsubfr[:], xlen, frameMs, isLast, smplPercRLen, rBuf[:])
		prevBuf := *cs.percPrev
		for i := 0; i < smplPercRLen; i++ {
			var prev float32
			if i < len(prevBuf) {
				prev = prevBuf[i]
			}
			corrs[sf-1][i] = 0.5 * (r[i] + prev)
		}
		// Refresh the persistent prev-pair buffer in place (reuse its allocation).
		// `percPrev` and `corrs[sf]` hold identical values.
		*cs.percPrev = append(prevBuf[:0], r...)
		copy(corrs[sf][:], r)
		sf += 2
	}
	return corrs
}

// percCorrsToWght derives one weighting response per subframe from precomputed perceptual
// correlations. Only the first `respLen` coefficients are active; the rest of each row remain
// zero. The fixed matrix avoids allocating a response vector for each subframe.
//
// Panics if `respLen` exceeds `smplCelpPercRespLen`. (Rust also asserts `corrs` has
// `smplSubfrCount` rows; the array type guarantees it here.)
func percCorrsToWght(
	corrs *[smplSubfrCount][smplPercRLen]float32,
	emph [2]float32,
	respLen int,
) [smplSubfrCount][smplCelpPercRespLen]float32 {
	var out [smplSubfrCount][smplCelpPercRespLen]float32
	for sf := 0; sf < smplSubfrCount; sf++ {
		smplPercAc2aInto(
			corrs[sf][:],
			smplPercRLen,
			emph[b2i(smplCelpLowRate)],
			respLen,
			smplPercReg,
			out[sf][:respLen],
		)
	}
	return out
}

// smplLsfInterpolSearch computes the per-subframe residual + interpolated predcoef for
// `lsf_interpol_idx` 0, and the alternative idx 1 when it lowers the summed per-subframe residual
// RMS by the 0.998 margin. Returns (predcoefs, chosen idx) and writes the chosen residual into
// `resOut` (Rust returns it as a Vec). At complexity 5-8 this search runs for every active frame.
//
// `codedAsActiveVoice` false pins the index to 0 without searching, because the index is not on
// the wire for such a frame (`decode_smpl_lsf` reads 0 for it): choosing 1 would whiten the
// residual under an interpolation the decoder will not reproduce.
func smplLsfInterpolSearch(
	brec []float32,
	prevLsfq []float32,
	winN []float32,
	codedAsActiveVoice bool,
	resOut *[smplIntfLen]float32,
) ([smplSubfrCount][17]float32, int32) {
	residualFor := func(idx int, res *[smplIntfLen]float32) ([smplSubfrCount][17]float32, float32) {
		predcoefs, _ := smplLpcInterpolIdx(brec, prevLsfq, idx)
		sumRms := float32(0.0)
		for sf := 0; sf < smplSubfrCount; sf++ {
			r := smplAnalysisResidualSubfr(&predcoefs[sf], winN, sf)
			nrg := analysisNegZero
			for _, v := range r {
				nrg += float32(v * v)
			}
			sumRms += sqrtF32(nrg + 1e-30)
			copy(res[sf*smplSubfrLen:(sf+1)*smplSubfrLen], r[:])
		}
		return predcoefs, sumRms
	}

	pc0, rms0 := residualFor(0, resOut)
	// The alt interpolation runs whenever lsf_interpol_search && active && numsubfrs>1.
	if !codedAsActiveVoice {
		return pc0, 0
	}
	var res1 [smplIntfLen]float32
	pc1, rms1 := residualFor(1, &res1)
	if rms1 < rms0*0.998 {
		*resOut = res1
		return pc1, 1
	}
	return pc0, 0
}

// smplAnalysisResidualSubfr is the one-subframe residual under that subframe's interpolated
// predcoef (`smpl_filt_ma16_monic` over the `sf`-th 80-sample block of `winN`, which carries
// smplOrder lead history before the frame).
func smplAnalysisResidualSubfr(
	aSyn *[17]float32,
	winN []float32,
	sf int,
) [smplSubfrLen]float32 {
	var res [smplSubfrLen]float32
	for n := range res {
		idx := smplOrder + sf*smplSubfrLen + n
		acc := winN[idx]
		for j := 1; j <= smplOrder; j++ {
			acc += float32(aSyn[j] * winN[idx-j])
		}
		res[n] = acc
	}
	return res
}

func smplSilentInternal(synthT *smplSynthTables) candidate {
	var sym [16]int32
	for k := range sym {
		sym[k] = int32(len(synthT.valtables[0][0][0][k]) / 2)
	}
	// Silent frame: lowest encodable gain (no pulses, so the exact value is immaterial).
	gm, gd, _ := smplRateControlGains(0.0)
	return candidate{
		ip: smplInternalParams{
			lsf: smplLsfParams{
				stage1: 0,
				grid:   0,
				stage2: sym,
				extra:  0,
			},
			pulses: defaultSmplPulseParams(),
			pitch:  defaultSmplPitchParams(),
			gains: smplGainParams{
				gainMain:  gm,
				gainDelta: gd,
				nrgRes:    [4]int32{-1, -1, -1, -1},
			},
		},
		stage1:   0,
		grid:     0,
		qsym:     sym,
		pulseVec: [smplIntfLen]int32{},
		gainQ:    [4]int32{},
		pitch:    unvoicedPitch(),
		silent:   true,
	}
}

// smplAutocorr writes the autocorrelation lags 0..=order of `x` into `r` (Rust returns a fresh
// `Vec` of `order + 1`; `r` must hold that many).
func smplAutocorr(x []float64, order int, r []float64) {
	n := len(x)
	for lag := 0; lag <= order; lag++ {
		s := float64(0)
		for i := lag; i < n; i++ {
			s += float64(x[i] * x[i-lag])
		}
		r[lag] = s
	}
}

// smplBuildPulseParams builds the pulse block from a per-position pulse train. `magRuns` and
// `signSyms` are empty buffers the run-length and sign symbols are appended to (Rust allocates
// both); the result's slices may alias them.
func smplBuildPulseParams(pulse []int32, magRuns []int32, signSyms []smplRawSym) smplPulseParams {
	const p3 = 4
	const posPer = smplIntfLen / p3 // 80
	pp := defaultSmplPulseParams()
	for sf := 0; sf < p3; sf++ {
		s := int32(0)
		for n := sf * posPer; n < (sf+1)*posPer; n++ {
			s += absI32(pulse[n])
		}
		pp.subfr[sf] = s
	}
	pp.total = pp.subfr[0] + pp.subfr[1] + pp.subfr[2] + pp.subfr[3]

	magRuns = magRuns[:0]
	var signs [smplIntfLen]int32
	numPos := 0
	for sf := 0; sf < p3; sf++ {
		if pp.subfr[sf] <= 0 {
			continue
		}
		basePos := posPer * sf
		runPos := int32(basePos)
		first := true
		// Collected and consumed in the same ascending order, so the two passes fuse.
		for p := basePos; p < basePos+posPer; p++ {
			magv := pulse[p]
			if magv == 0 {
				continue
			}
			mag := absI32(magv)
			var m int32
			if first {
				m = int32(p) - int32(basePos)
			} else {
				m = int32(p) - runPos
			}
			magRuns = append(magRuns, m)
			runPos = int32(p)
			for k := int32(1); k < mag; k++ {
				magRuns = append(magRuns, 0)
			}
			if magv < 0 {
				signs[numPos] = -1
			} else {
				signs[numPos] = 1
			}
			numPos++
			first = false
		}
	}
	pp.magRuns = magRuns

	// SIGN block: batch signs into raw symbols (<=15 bits each, MSB-first).
	signSyms = signSyms[:0]
	p := 0
	for p < numPos {
		nbits := min(numPos-p, 15)
		sym := uint32(0)
		for q := 0; q < nbits; q++ {
			var bit uint32
			if signs[p+q] > 0 {
				bit = 1
			}
			sym |= bit << uint32(nbits-1-q)
		}
		signSyms = append(signSyms, smplRawSym{
			sym:   sym,
			nbits: uint32(nbits),
		})
		p += nbits
	}
	pp.signSyms = signSyms
	return pp
}

// smplRateControlGains finds the (gainMain, gainDelta, reconstructed gainQ) whose linear gain is
// closest to `targetLinear`.
func smplRateControlGains(targetLinear float64) (int32, int32, int32) {
	cc := loadCcTables()
	cfgSel := int32(2)
	cb1 := cc.nrgStep(cfgSel)
	bestD := math.Inf(1)
	var bgm, bgd, bgq int32
	for gm := int32(0); gm < 84; gm++ {
		base7 := gm*cb1 - 0x154000
		for gd := int32(0); gd < 98; gd++ {
			cbv := cc.gainRecon(true, 4*gd)
			gq := base7 + (cbv << 4)
			d := math.Abs(smplGainLin(gq) - targetLinear)
			if d < bestD {
				bestD = d
				bgm = gm
				bgd = gd
				bgq = gq
			}
		}
	}
	return bgm, bgd, bgq
}

// voiced (LTP) encode path

// smplPercEmphPitch is the perceptual emphasis the pitch weighting uses.
const smplPercEmphPitch float32 = -0.82

// smplPitchPercRespLen is `pitch_perc_resp_len` for complexity 5-8 (the 17-tap monic MA
// weighting).
const smplPitchPercRespLen = 17

// smplPitchLagMax is the pitch search history span in samples (`SMPL_MAXPITCH_LEN`), carried for
// the intf=0 estimator.
const smplPitchLagMax = 320

// smplPitchLookaheadLen is the pitch estimator lookahead (`SMPL_PITCH_LOOKAHEAD_LEN`).
const smplPitchLookaheadLen = 7

// buildLtpBuf rolls the persistent perceptually-weighted speech buffer (`ltp_buf`) and writes this
// internal frame's weighted speech into its tail: shift left by `framelen`, then per CELP subframe
// `i` apply the 17-tap monic perceptual MA of the HP frame under `respPitch[i]`, plus the
// `PITCH_LOOKAHEAD_LEN`-sample lookahead under `respPitch[3]`.
// The HP frame (`xhp_frame`) starts `smplWinnextWbLen` samples before the internal frame; the MA
// reads up to `smplLpcOrder` samples of history before that. Built in the normalized HP domain,
// which is scale-invariant for the estimator's pitchcorr/lag outputs.
func buildLtpBuf(cs *celpFrameCtx, percCorrs *[smplSubfrCount][smplPercRLen]float32) {
	respPitch := percCorrsToWght(
		percCorrs,
		[2]float32{smplPercEmphPitch, smplPercEmphPitch},
		smplPitchPercRespLen,
	)
	const maxLen = maxLtpBufLen // 659
	const look = smplPitchLookaheadLen
	const framelen = smplIntfLen // 320
	// Shift existing weighted speech left by one internal frame.
	const keep = maxLen - framelen - look
	copy(cs.ltpBuf[0:keep], cs.ltpBuf[framelen:framelen+keep])
	// HP sample at internal-frame-relative index `idx` (xhp_frame origin), reaching into the
	// previous packet's tail (`hpPitchHist`, entry `k` at relative index `k - smplPitchLagMax`)
	// for idx<0.
	frameStart := cs.intf*smplIntfLen - smplWinnextWbLen
	const hist = smplPitchLagMax
	sample := func(rel int) float32 {
		idx := frameStart + rel
		if idx >= 0 {
			if idx < len(cs.hpN) {
				return cs.hpN[idx]
			}
			return 0.0
		} else if len(cs.hpPitchHist) == hist {
			k := idx + hist
			if k >= 0 {
				return cs.hpPitchHist[k]
			}
			return 0.0
		}
		return 0.0
	}
	// w_speech write origin in ltp_buf (MAX_LTP_BUF_LEN - numsubfrs*subfrlen - lookahead).
	const wOrigin = maxLen - smplSubfrCount*smplSubfrLen - look // 332
	for i := 0; i < smplSubfrCount; i++ {
		coef := &respPitch[i]
		for n := 0; n < smplSubfrLen; n++ {
			pos := i*smplSubfrLen + n
			res := sample(pos) // monic coef[0]==1
			for j := 1; j < smplPitchPercRespLen; j++ {
				res += float32(coef[j] * sample(pos-j))
			}
			cs.ltpBuf[wOrigin+i*smplSubfrLen+n] = res
		}
	}
	// Lookahead tail under the last subframe's response.
	coef := &respPitch[smplSubfrCount-1]
	for n := 0; n < look; n++ {
		pos := framelen + n
		res := sample(pos)
		for j := 1; j < smplPitchPercRespLen; j++ {
			res += float32(coef[j] * sample(pos-j))
		}
		cs.ltpBuf[maxLen-look+n] = res
	}
}

// smplAnalyzeInternal analyzes one internal frame: compute the shared perceptual autocorrelation,
// build the perceptually-weighted `ltp_buf`, run the faithful multi-stage pitch estimator + the
// `smpl_get_signal_mode` voicing classifier, then build the voiced (LTP) or unvoiced candidate,
// commit it to the shadow synth `st`, and advance the entropy predictor mirror. The returned
// NLSF is written into `nlsfOut` (Go-only parameter, see [commitCandidate]).
func smplAnalyzeInternal(
	nlsfOut []float32,
	synthT *smplSynthTables,
	st *smplFrameSynth,
	lstate *smplLsfState,
	intf int,
	win []float64,
	winN []float32,
	prevNlsf []float32,
	fe *frontEndLsf,
	cs *celpFrameCtx,
) (smplInternalParams, []float32, bool) {
	mem := loadSmplMem()

	// Shared perceptual autocorrelation (advances perc state EXACTLY ONCE per frame); both the
	// pitch weighting and the CELP weighting derive from it (matching `perc_corrs_buf`).
	cs.percCorrs = computePercCorrs(cs)

	// Roll the persistent perceptually-weighted speech buffer (`ltp_buf`) and write this frame's
	// weighted speech + lookahead into its tail, then run the faithful multi-stage pitch
	// estimator.
	buildLtpBuf(cs, &cs.percCorrs)
	f2 := cs.f2
	pr := smplPitch(cs.pitchEst, cs.ltpBuf, &f2, cs.codedAsActiveVoice)
	pitchcorr := pr.pitchcorr
	avgLag := pr.avgLag
	harm := pr.harmStrength
	lags8 := pr.lags
	// The single representative lag the voiced encode path uses; the C's wire contour is anchored
	// on the first-subframe lag, so use that as the encode target (the per-block CELP basis is
	// rebuilt from the wire pitch params downstream).
	lagSamples := pr.lags[0]
	sp := cs.spActProb
	vstr := smplGetSignalMode(pitchcorr, lags8[:], avgLag, harm, &f2, sp, cs.vuv)
	cs.voicingStrength = vstr
	isVoicedDecision := vstr > 0.0 && cs.codedAsActiveVoice
	if isVoicedDecision {
		lstate.prevLagSamples = lagSamples
	} else {
		lstate.prevLagSamples = 0.0
	}
	// The C resets the lag-block predictor after an unvoiced frame (and after each packet's last
	// frame, handled at the call site); mirror the unvoiced reset here so cond-coding restarts
	// correctly.
	if !isVoicedDecision {
		cs.pitchEst.resetCond()
		lags8 = [8]float32{}
	}

	// The CELP excitation encoder advances its per-subframe acb/zir/prev-idx state, so it must run
	// EXACTLY ONCE per internal frame with the lags of the committed decision.
	voicedLstate := *lstate
	smplAdvanceLsfState(&voicedLstate, intf, 1)
	var vd voicedDecision
	haveVoiced := false
	if isVoicedDecision {
		vd, haveVoiced = smplVoicedDecisionForLag(pr.blocksegIdx, &pr.laginds, cs, &lags8)
	}

	var chosen candidate
	var chosenLstate smplLsfState
	haveChosenLstate := false
	var isVoiced bool
	if haveVoiced {
		chosen = smplVoicedCandidate(synthT, winN, prevNlsf, fe, cs, &vd)
		chosenLstate = voicedLstate
		haveChosenLstate = true
		isVoiced = true
	} else {
		chosen = smplUnvoicedCandidate(synthT, st, win, winN, prevNlsf, fe, cs)
		isVoiced = false
	}
	committedNlsf := commitCandidate(nlsfOut, synthT, st, &chosen, prevNlsf)
	if chosen.stage1 == 1 {
		if !haveChosenLstate {
			panic("voiced candidate set its lstate")
		}
		*lstate = chosenLstate
		subfr := chosen.ip.pulses.subfr
		smplReplayPitchState(mem, lstate, 4, subfr, &chosen.ip.pitch)
	} else {
		smplAdvanceLsfState(lstate, intf, chosen.stage1)
	}
	return chosen.ip, committedNlsf, isVoiced
}

// smplReplayPitchState advances the predictor mirror exactly as `encodeSmplPitch` does, without
// entropy coding, so the analysis predicts the lag/gain predictor for the next internal frame.
// Threads the lag predictor (`prevLagblk`/`prevLagidx`) from the chosen contour + per-block
// laginds.
func smplReplayPitchState(
	_ *smplMem,
	st *smplLsfState,
	p3 int32,
	subfrCounts [4]int32,
	pp *smplPitchParams,
) {
	for sf := 0; sf < min(int(p3), 4); sf++ {
		st.prevGainIdx = pp.gainIdx[sf]
		if subfrCounts[sf] > 0 {
			st.prevFiltIdx = pp.filtIdx[sf]
		}
	}
	tab := loadPitchTables()
	nblk, nidx := smplLagsPredictorAfter(tab, pp.blocksegIdx, &pp.laginds)
	st.prevLagblk = nblk
	st.prevLagidx = nidx
}

// voicedDecision is the committed voiced decision for one internal frame: the encodable pitch
// params and the per-subframe synthesis lag carried in `pitch`. The LSF comes from the shared
// front-end.
type voicedDecision struct {
	pp    smplPitchParams
	pitch smplPitchSynth
}

// smplVoicedDecisionForLag carries the estimator's full per-block contour (`blocksegIdx` +
// `laginds`) into the voiced decision: the wire pitch encode writes them straight through
// `smpl_encode_lags`, and the CELP ACB basis uses the SAME per-block lags
// (`lag = laginds*0.5 + 32`) so the encoder/decoder LTP contributions agree. The gain/filter
// indices here are placeholders; the voiced candidate overwrites them with the real CELP
// `acb_idx`/`fcb_idx` per subframe. Rust returns `Option` (always Some); the bool is that flag.
func smplVoicedDecisionForLag(
	blocksegIdx int,
	laginds *[8]int32,
	cs *celpFrameCtx,
	lags8 *[8]float32,
) (voicedDecision, bool) {
	// The decoder maps each 40-block lag index `lag = laginds*0.5 + SMPL_MIN_PITCH_LAG`,
	// clamped <=320.
	var blockLags8 [8]float32
	for b := 0; b < 8; b++ {
		blockLags8[b] = minF32(float32(float32(laginds[b])*0.5)+32.0, 320.0)
	}
	*lags8 = blockLags8
	for sf := 0; sf < smplSubfrCount; sf++ {
		cs.blockLags[sf] = [2]float32{blockLags8[2*sf], blockLags8[2*sf+1]}
	}
	meanLag := sumF32(blockLags8[:]) / 8.0

	pp := smplPitchParams{
		gainIdx:     [4]int32{5, 5, 5, 5},
		filtIdx:     [4]int32{},
		blocksegIdx: blocksegIdx,
		laginds:     *laginds,
	}

	ml := float64(meanLag)
	pitch := smplPitchSynth{
		voiced:   true,
		lagSubfr: [4]float64{ml, ml, ml, ml},
		normGain: smplVoicedNormGain,
	}
	return voicedDecision{pp: pp, pitch: pitch}, true
}

// smplVoicedCandidate builds the voiced (stage1=1 + LTP) candidate for one internal frame. The real
// CELP voiced encoder runs with the decoder-reconstructed per-block lags (so its ACB basis matches
// the decoder's), and its outputs drive the wire: `pulses[MAIN]` -> the pulse train,
// `acb_idx[MAIN]` -> the wire `gain_idx` (ACB/LTP gain), `gain_idx[MAIN]` -> the wire `filt_idx`
// (voiced FCB gain). The decoder then adds the ACB contribution and scales the FCB pulses by the
// voiced gain table, reproducing the encoder's excitation instead of the prior gainless greedy
// approximation.
func smplVoicedCandidate(
	synthT *smplSynthTables,
	// Use the caller's window WITH the 32-sample CELP pre-lead (same as the unvoiced path),
	// matching the C: both voiced and unvoiced take the LPC residual from the same pre-lead
	// window.
	winN []float32,
	prevNlsf []float32,
	fe *frontEndLsf,
	cs *celpFrameCtx,
	vd *voicedDecision,
) candidate {
	var gainQ [4]int32 // voiced synthesis uses the ACB+FCB excitation, not a gains block

	// Voiced-grid LSF: bit-exact C quantizer fed the faithful front-end NLSF (voiced codebook).
	bgrid, bsym, brec, _ := fe.quantize(synthT, 1, prevNlsf)

	// Per-subframe interpolated LPC (same as the unvoiced path).
	predcoefs, _ := smplLpcInterpol(brec, fe.prevLsfq)
	var resLpc [smplIntfLen]float32
	for sf := 0; sf < smplSubfrCount; sf++ {
		r := smplAnalysisResidualSubfr(&predcoefs[sf], winN, sf)
		copy(resLpc[sf*smplSubfrLen:(sf+1)*smplSubfrLen], r[:])
	}

	// Real voiced CELP: with nonzero lags the encoder runs the ACB/LTP path (calc_acb_gain ->
	// d_ltp -> FCB deldec on the post-LTP residual -> calc_gains_v), producing the pulse set +
	// acb/fcb indices.
	blockLags := cs.blockLags
	celpOut := runCelpSubframes(
		cs,
		&predcoefs,
		resLpc[:],
		&blockLags,
		&cs.percCorrs,
		smplPercEmphV,
		1,
	)

	// Unpack the MAIN-rate pulses into a per-position train; collect acb/fcb indices per subframe.
	const mainIdx = 1
	var pulseVec [smplIntfLen]int32
	var acbg [4]int32
	var fcbg [4]int32
	for sf := 0; sf < smplSubfrCount; sf++ {
		out := &celpOut[sf]
		for _, v := range out.pulses[mainIdx][:max(out.nPulses[mainIdx], 0)] {
			sign := 1 + 2*(int32(v)>>15)
			pos := (int32(v) * sign) - 1
			if pos >= 0 && pos < int32(smplSubfrLen) {
				pulseVec[sf*smplSubfrLen+int(pos)] += sign
			}
		}
		// acb_idx is always coded; fcb (filt) only where pulses exist. Clamp to the wire ranges.
		acbg[sf] = min(max(int32(out.acbIdx[mainIdx]), 0), 15)
		fcbg[sf] = max(int32(out.gainIdx[mainIdx]), 0)
	}
	ppPulses := smplBuildPulseParams(pulseVec[:], cs.magRunsBuf, cs.signSymsBuf)
	subfr := ppPulses.subfr
	pp := vd.pp
	pp.gainIdx = acbg
	for sf := 0; sf < 4; sf++ {
		if subfr[sf] > 0 {
			pp.filtIdx[sf] = fcbg[sf]
		} else {
			pp.filtIdx[sf] = -1
		}
	}

	return candidate{
		ip: smplInternalParams{
			lsf: smplLsfParams{
				stage1: 1,
				grid:   bgrid,
				stage2: bsym,
				extra:  0,
			},
			pulses: ppPulses,
			pitch:  pp,
			gains:  defaultSmplGainParams(),
		},
		stage1:   1,
		grid:     bgrid,
		qsym:     bsym,
		pulseVec: pulseVec,
		gainQ:    gainQ,
		pitch:    vd.pitch,
		silent:   false,
	}
}

// The Rust `stage_bench` module (behind the `bench-internals` feature) is test-only code in Go:
// see analysis_test.go.
