// Ported from whatsapp-rust 5e72baf wacore/src/voip/mlow/encode.rs, MIT.
//
// MLow ENTROPY ENCODER: the exact inverse of the byte-exact decoder. Given the analyzed
// `smplFrameParams`, it reproduces the same range-coder symbol stream the decoder consumes, against
// the same config=0 runtime tables in the same field order. Targets config=0, 16 kHz, 60 ms, mono,
// no DTX, p3=4. The TOC's activity bits follow the SILK VAD, as the reference encoder's do: speech
// is `0x50`, a packet kept active only by the DTX hangover is `0x12`, and a settled noise floor is
// coded inactive as `0x10`. An inactive frame omits the LSF voicing selector and interpolation
// index and uses the BACKGROUND_NOISE pulse geometry (`p4 = 0`, half the pulse capacity); an
// active one carries both symbols with `p4 = 1`. Internal frames are voiced (LTP pitch block) or
// unvoiced (gains block) per the analysis, and an inactive frame is always unvoiced.
//
// Go port note: Rust's `encode_into`/`encode_i16_into` REPLACE the caller's Vec. The Go
// `AppendEncode`/`AppendEncodeInt16` APPEND to dst (pass dst[:0] to replace), and
// `encodeSmplFrameInto` likewise appends to its output instead of clearing it.

package mlow

import (
	"errors"
	"fmt"
	"time"
)

// The codec's single operating point. There is no bitrate, complexity or sample-rate knob: MLow as
// WhatsApp 1:1 calls use it runs at exactly one configuration (16 kHz mono, 60 ms packets of three
// 20 ms internal frames, config 0), and the encoder's internal rate control is fixed to it.
const (
	// SampleRate is the only sample rate the codec accepts and produces, in Hz.
	SampleRate = 16000
	// Channels is the channel count (mono).
	Channels = 1
	// FrameSamples is the number of samples in one packet: 60 ms at 16 kHz.
	FrameSamples = 960
	// FrameDuration is the audio duration of one packet.
	FrameDuration = 60 * time.Millisecond
)

// smplEncodeBufBytes is the range coder's output capacity per packet body.
const smplEncodeBufBytes = 512

// OPUS_FRAME_SAMPS (960, 60 ms @ 16 kHz) is shared.go's opusFrameSamps.

// FrameLengthError reports PCM that was not exactly one 60 ms frame (960 samples @ 16 kHz): a
// caller bug (wrong PCM chunk size). Rust MlowError::FrameLength.
type FrameLengthError struct {
	Expected, Got int
}

func (e FrameLengthError) Error() string {
	return fmt.Sprintf("mlow encode: expected %d samples (60 ms @16 kHz), got %d", e.Expected, e.Got)
}

// ErrBufferOverflow reports that the range coder's output buffer overflowed encoding this frame:
// an internal limit the next frame may not hit. Rust MlowError::BufferOverflow.
var ErrBufferOverflow = errors.New("mlow encode: range-encoder buffer overflow")

// Encoder is the stateful pure-Go MLow encoder for 60 ms mono PCM frames at 16 kHz (Rust
// MlowEncoder). It accepts normalized float32 samples through [Encoder.Encode] or
// [Encoder.AppendEncode], and signed 16-bit samples through [Encoder.EncodeInt16] or
// [Encoder.AppendEncodeInt16]. It emits config=0 wire frames whose TOC is 0x50 for speech, 0x12
// through the DTX hangover and 0x10 for background noise coded inactive, choosing voiced or
// unvoiced per internal frame via analysis-by-synthesis.
//
// There is no bitrate or sample-rate setting: the codec has one operating point (16 kHz mono,
// 60 ms, config 0). An Encoder is not safe for concurrent use.
type Encoder struct {
	state smplEncoderState
	clean []float32
	rng   rangeEncoder
}

// NewEncoder returns an encoder with fresh analysis history. There are no options: the codec has
// one operating point (16 kHz mono, 60 ms, config 0).
func NewEncoder() *Encoder {
	return &Encoder{
		state: defaultSmplEncoderState(),
		clean: make([]float32, 0, opusFrameSamps),
		rng:   newRangeEncoder(1 + smplEncodeBufBytes),
	}
}

// Reset clears the cross-frame analysis history (call at a stream discontinuity).
func (e *Encoder) Reset() {
	e.state = defaultSmplEncoderState()
	e.clean = e.clean[:0]
	e.rng.reset()
}

// Encode encodes one 60 ms frame into a newly allocated packet. It expects exactly
// [FrameSamples] normalized samples (nominally [-1, 1]; NaN is read as 0 and the rest clamped).
func (e *Encoder) Encode(pcm []float32) ([]byte, error) {
	out, err := e.AppendEncode(make([]byte, 0, 1+smplEncodeBufBytes), pcm)
	if err != nil {
		return nil, err
	}
	return out, nil
}

// AppendEncode encodes one 60 ms frame and appends the packet to dst, so a realtime stream can
// reuse its buffer: it does not allocate when dst has room for 1+512 bytes. Pass dst[:0] to
// replace dst's contents. On error it returns dst unchanged.
//
// It expects exactly [FrameSamples] samples; any other length is a [FrameLengthError] and leaves
// the codec state unchanged. A range-buffer overflow ([ErrBufferOverflow]) still advances the
// analysis history.
func (e *Encoder) AppendEncode(dst []byte, pcm []float32) ([]byte, error) {
	if len(pcm) != opusFrameSamps {
		return dst, FrameLengthError{Expected: opusFrameSamps, Got: len(pcm)}
	}
	// Sanitize: NaN -> 0, clamp to [-1,1] (the LPC analysis degenerates on non-finite input).
	e.clean = e.clean[:0]
	for _, s := range pcm {
		if s != s {
			e.clean = append(e.clean, 0.0)
		} else {
			e.clean = append(e.clean, clampF32(s, -1.0, 1.0))
		}
	}
	return e.encodeClean(nil, dst)
}

// EncodeInt16 encodes exactly [FrameSamples] signed 16-bit mono samples at 16 kHz into a newly
// allocated packet.
func (e *Encoder) EncodeInt16(pcm []int16) ([]byte, error) {
	out, err := e.AppendEncodeInt16(make([]byte, 0, 1+smplEncodeBufBytes), pcm)
	if err != nil {
		return nil, err
	}
	return out, nil
}

// AppendEncodeInt16 encodes exactly [FrameSamples] signed 16-bit mono samples at 16 kHz and
// appends the packet to dst (pass dst[:0] to replace).
//
// It normalizes each sample as float32(sample) / 32768 in encoder-owned scratch; VAD reads the
// original samples, avoiding an exact but redundant conversion back to int16, so the output is
// byte-identical to [Encoder.AppendEncode] on the normalized samples. An error returns dst
// unchanged. A frame-length error also leaves codec state unchanged; a range-buffer overflow
// advances analysis state as in [Encoder.AppendEncode]. Successive calls carry analysis history
// until [Encoder.Reset].
func (e *Encoder) AppendEncodeInt16(dst []byte, pcm []int16) ([]byte, error) {
	if len(pcm) != opusFrameSamps {
		return dst, FrameLengthError{Expected: opusFrameSamps, Got: len(pcm)}
	}
	e.clean = e.clean[:0]
	for _, s := range pcm {
		e.clean = append(e.clean, float32(s)/32768.0)
	}
	return e.encodeClean(pcm, dst)
}

// encodeClean consumes exactly one frame of normalized PCM already staged in `e.clean`. Both input
// APIs validate and fill that scratch before entering here. Analysis advances before entropy
// encoding, so an encoding error does not roll back history, although the caller's output remains
// unchanged.
func (e *Encoder) encodeClean(originalPcm []int16, output []byte) ([]byte, error) {
	fp := smplAnalyzeFrameSt(&e.state, e.clean, originalPcm)
	return encodeSmplFrameInto(&fp, &e.rng, output)
}

// encodeSmplFrameInto range-codes `fp` and appends TOC + body to `output` (Rust clears `output`
// first). On error `output` is returned unchanged.
func encodeSmplFrameInto(
	fp *smplFrameParams,
	enc *rangeEncoder,
	output []byte,
) ([]byte, error) {
	// `p4 + s1` is the frame type the pulse geometry is indexed by: 0 BACKGROUND_NOISE,
	// 1 UNVOICED, 2 VOICED, the same row `smpl_perc` caps the pulse budget with
	// (`SMPL_MAX_PULSES_PER_FRAME`). So it is the packet's activity flag, not a constant: a frame
	// coded inactive has half the pulse capacity (80 against 160), and `decodeSmplPulses` reads
	// it as `i32::from(coded_as_active_voice)`. Writing 1 here for an inactive frame encodes the
	// splits under a geometry the reader does not use, and the body ends in the wrong place.
	p2, p3 := int32(320), int32(4)
	p4 := b2i32(fp.codedAsActiveVoice)
	p6 := int32(fp.config)
	tbl := loadSmplTables()
	mem := loadSmplMem()
	cc := loadCcTables()
	enc.reset()
	st := defaultSmplLsfState()
	for f := 0; f < 3; f++ {
		ip := &fp.internal[f]
		encodeSmplLsf(
			enc,
			tbl,
			&st,
			fp.config,
			f,
			&ip.lsf,
			fp.codedAsActiveVoice,
		)
		encodeSmplPulses(enc, cc, p2, p3, p4, p6, ip.lsf.stage1, &ip.pulses)
		// Voiced internal frames emit a pitch block; unvoiced emit a gains block (never both).
		if ip.lsf.stage1 == 1 {
			encodeSmplPitch(
				enc,
				mem,
				cc,
				&st,
				p2,
				p3,
				p6,
				ip.pulses.subfr,
				&ip.pitch,
			)
		} else {
			encodeSmplGains(enc, cc, p3, ip.pulses.subfr, &ip.gains)
		}
	}
	enc.done()
	if enc.err() != 0 {
		return output, ErrBufferOverflow
	}
	n := enc.consumedLen()
	body := enc.bytes()
	output = append(output, fp.toc)
	output = append(output, body[:n]...)
	return output, nil
}

// encodeSmplLsf is the inverse of `decodeSmplLsf`: mirror the selector/grid/16-residual/extra
// reads, mutating `st`.
//
// The voicing selector and the interpolation index are on the wire only for a frame coded as
// active voice, exactly as `decodeSmplLsf` gates its reads: writing them for an inactive frame
// would leave two symbols the reader never consumes and desync everything after.
func encodeSmplLsf(
	enc *rangeEncoder,
	t *smplTables,
	st *smplLsfState,
	config int,
	intf int,
	lsf *smplLsfParams,
	codedAsActiveVoice bool,
) {
	// Rust debug_asserts here that an inactive frame is unvoiced with interpolation index 0 by
	// definition.
	var sel int
	if intf == 0 {
		sel = 0
	} else if st.prevStage1 != 0 {
		sel = 2
	} else {
		sel = 1
	}
	stage1 := lsf.stage1
	if codedAsActiveVoice {
		enc.encodeCdf(stage1, t.lsfSel[sel])
	}

	enterMatch := intf != 0
	m := enterMatch && (stage1 == st.prevStage1)
	if !m {
		st.prevGainIdx = -1
		st.prevFiltIdx = -1
		st.prevLag = -1
		st.prevFracLag = -1
		st.prevLagblk = -1
		st.prevLagidx = -1
	}
	st.prevStage1 = stage1

	var gridCdf []uint16
	if m {
		if stage1 != 0 {
			gridCdf = t.lsfGrid.match1
		} else {
			gridCdf = t.lsfGrid.match1Alt
		}
	} else if stage1 != 0 {
		gridCdf = t.lsfGrid.match0Alt
	} else {
		gridCdf = t.lsfGrid.match0
	}
	enc.encodeCdf(lsf.grid, gridCdf)
	st.prevMatch = m
	st.havePrev = true

	st2 := t.lsfStage2[stage1][config][lsf.grid]
	for k, c := range st2 {
		if k >= 16 {
			break
		}
		enc.encodeCdf(lsf.stage2[k], c)
	}
	if codedAsActiveVoice {
		enc.encodeCdf(lsf.extra, t.lsfExtra)
	}
}

// encodeSmplPulses is the inverse of `decodeSmplPulses` (config=0 NB count, p3=4): re-derive the
// count interval and the split symbols from the per-subframe counts, then replay the recorded
// magnitude/sign symbols.
func encodeSmplPulses(
	enc *rangeEncoder,
	cc *ccTables,
	p2 int32,
	p3 int32,
	p4 int32,
	p6 int32,
	s1 int32,
	pp *smplPulseParams,
) {
	idx := p4 + s1
	bByte := int32(mem8Static(0xe8990 + uint32(p6*3+idx)))
	frameLen4k := bByte * p2 / 320
	subfrLen16 := frameLen4k / p3
	total := pp.total

	// pulse COUNT (NB triangular; config=0)
	l := uint32(frameLen4k)
	triT := func(k uint32) uint32 {
		a := (k + 2) * (l + 1)
		b := ((k - 1) * (k + 131070)) >> 1
		return (a - b) & 0xffff
	}
	ft := triT(l)
	if ft == 0 {
		ft = 1
	}
	var fl uint32
	if total > 0 {
		fl = triT(uint32(total - 1))
	}
	fh := triT(uint32(total))
	enc.encode(fl, fh, ft)

	if total == 0 {
		return
	}

	// recursive binary SPLIT
	finalSum := pp.subfr[0] + pp.subfr[1]
	initSum := max(total-subfrLen16*2, 0)
	lo := max(total-80, 0)
	if initSum < lo {
		return
	}
	hiBound := total - lo
	if initSum < hiBound {
		n := int((hiBound - initSum) + 2)
		enc.encodeCdfWindow(
			finalSum-initSum,
			cc.splitCmf(total),
			int(initSum-lo),
			n,
		)
	}
	if finalSum > 0 {
		encodeSplit3537(enc, cc, finalSum, subfrLen16, pp.subfr[0])
	}
	if finalSum < total {
		encodeSplit3537(enc, cc, total-finalSum, subfrLen16, pp.subfr[2])
	}

	// MAGNITUDE block: replay recorded run-length symbols through the same loop
	posPer := p2 / p3
	magIdx := 0
	for subfr := int32(0); subfr < p3; subfr++ {
		cnt := pp.subfr[subfr]
		if cnt <= 0 {
			continue
		}
		pos := posPer
		c := cnt
		k := int32(0)
		for k < cnt {
			oct := (pos + 7) / 8
			bucket := cc.runlen(oct)
			start := int(bucket.maxSamples() - pos)
			m := pp.magRuns[magIdx]
			magIdx++
			enc.encodeCdfWindow(m, bucket.cmf(c), start, int(pos+1))
			if m > 0 || k == 0 {
				pos -= m
			}
			c--
			k++
		}
	}

	// SIGN block: replay recorded raw sign symbols
	for _, rs := range pp.signSyms {
		enc.encodeRawSymbol(rs.sym, rs.nbits)
	}
}

// encodeSplit3537 is the inverse of `smplSplit3537`: encode the first-half count `s0` against the
// same CDF.
func encodeSplit3537(enc *rangeEncoder, cc *ccTables, count int32, granularity int32, s0 int32) {
	lo := min(count, granularity)
	minSplit := max(count-granularity, 0)
	if lo < minSplit || minSplit == lo {
		return
	}
	n := int((lo - minSplit) + 2)
	enc.encodeCdfWindow(s0-minSplit, cc.splitCmf(count), int(minSplit), n)
}

// encodeSmplGains is the inverse of `decodeSmplGains`: encode main/delta gain, then per-subframe
// nrgres with the same gain-derived address shift.
func encodeSmplGains(
	enc *rangeEncoder,
	cc *ccTables,
	p3 int32,
	subfrCounts [4]int32,
	gp *smplGainParams,
) {
	gainMain := gp.gainMain
	gainDelta := gp.gainDelta
	enc.encodeCdf(gainMain, cc.nrgresGain4())
	enc.encodeCdf(gainDelta, cc.nrgresShape4())
	cfgSel := int32(2)

	off6 := p3 * gainDelta
	base7 := gainMain*cc.nrgStep(cfgSel) - 0x154000
	var gainQ [4]int32
	for sf := 0; sf < len(gainQ) && sf < int(p3); sf++ {
		cbv := cc.gainRecon(p3 == 4, int32(sf)+off6)
		gainQ[sf] = base7 + (cbv << 4)
	}

	for sf := 0; sf < len(subfrCounts) && sf < int(p3); sf++ {
		cnt := subfrCounts[sf]
		if cnt <= 0 {
			continue
		}
		var bucket int32
		if cnt >= 30 {
			bucket = 3
		} else {
			bucket = (cnt & 0xffff) / 10
		}
		g := (gainQ[sf] + 8192) >> 14
		if g < -85 {
			g = -85
		}
		negPart := (g >> 31) & g
		minOffset := int(-negPart)
		enc.encodeCdf(
			gp.nrgRes[sf],
			cc.fcbgOffset(int(cfgSel), int(bucket), minOffset),
		)
	}
}

// encodeSmplPitch is the inverse of `decodeSmplPitch`: encode the LTP gains, primary lag
// (abs/delta), optional 64-fine, and the per-segment fractional symbols, mutating the predictor
// state identically.
func encodeSmplPitch(
	enc *rangeEncoder,
	mem *smplMem,
	cc *ccTables,
	st *smplLsfState,
	_ int32, // _p2
	p3 int32,
	p6 int32,
	subfrCounts [4]int32,
	pp *smplPitchParams,
) {
	gp := mem.gPitch
	// The active WB path is p6==0 (HR tables, served by the logical seed); the p6!=0 LR variant
	// stays on the heap window.
	var weightTab uint32
	var gainCdfBase uint32
	if p6 != 0 {
		weightTab = 0xe8460
		gainCdfBase = gp + 0xc0
	} else {
		weightTab = 0xe85b0
		gainCdfBase = gp + 0x302
	}
	filtCdf0 := gp + 0xdc4
	filtCdf1 := gp + 0xe4c

	gainAccum := int32(0)
	for sf := 0; sf < min(int(p3), 4); sf++ {
		cnt := subfrCounts[sf]
		gi := pp.gainIdx[sf]
		if p6 != 0 {
			row := gainCdfBase + uint32(st.prevGainIdx*0x22) + 0x22
			enc.encodeCdf(gi, mem.cdfAt(row, 17))
		} else {
			enc.encodeCdf(gi, cc.acbgainRow(st.prevGainIdx))
		}
		st.prevGainIdx = gi
		var w0, w2 int32
		if p6 != 0 {
			w0 = int32(mem.i16(weightTab + uint32(gi)*4))
			w2 = int32(mem.i16(weightTab + uint32(gi)*4 + 2))
		} else {
			w0, w2 = cc.acbgainWeights(gi)
		}
		gainAccum += w0 + 2*w2
		if cnt > 0 {
			fi := pp.filtIdx[sf]
			if p6 != 0 {
				if st.prevFiltIdx == -1 {
					enc.encodeCdf(fi, mem.cdfAt(filtCdf0, 35))
				} else {
					enc.encodeCdf(fi, mem.cdfAt(filtCdf1-uint32(st.prevFiltIdx)*2, 35))
				}
			} else if st.prevFiltIdx == -1 {
				enc.encodeCdf(fi, cc.fcbgainV())
			} else {
				enc.encodeCdf(fi, cc.fcbgainVDelta(st.prevFiltIdx))
			}
			st.prevFiltIdx = fi
		}
	}
	avgGain := gainAccum / p3

	// Lag block: write the estimator's chosen contour (`blocksegIdx`) + per-40-block lag indices
	// (`laginds`) via `smplEncodeLagsWire`, NOT a single-lag contour-map flattening. The
	// delta-lag CMF `mode` is selected by the mean-ACB-gain thresholds below.
	var mode int
	if avgGain < 10007 {
		mode = 0
	} else if avgGain < 14085 {
		mode = 1
	} else {
		mode = 2
	}
	tab := loadPitchTables()
	smplEncodeLagsWire(
		tab,
		enc,
		pp.blocksegIdx,
		&pp.laginds,
		st.prevLagblk,
		st.prevLagidx,
		mode,
	)
	nblk, nidx := smplLagsPredictorAfter(tab, pp.blocksegIdx, &pp.laginds)
	st.prevLagblk = nblk
	st.prevLagidx = nidx
}
