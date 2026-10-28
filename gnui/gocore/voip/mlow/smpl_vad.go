// Ported from whatsapp-rust 5e72baf wacore/src/voip/mlow/smpl_vad.rs, MIT.
//
// SILK VAD: per-internal-frame speech-activity probability and the `coded_as_active_voice` flag the
// bitrate controller and voiced/unvoiced classifier read. Fixed-point implementation of the SA_Q8
// measure plus the noise-level tracker, the 2-band allpass filterbank, and the per-packet hangover.
// Runs on the raw int16 input PCM (no encoder HP), 320 samples per internal frame at 16 kHz.

package mlow

import "math/bits"

// SILK fixed-point primitives.

const silkInt32Max int32 = 0x7FFF_FFFF

// silkInt16Max is in shared.go.
const silkInt16Min int32 = -0x8000
const silkUint8Max int32 = 0xFF

func sat16(a int32) int32 {
	if a < silkInt16Min {
		return silkInt16Min
	}
	if a > silkInt16Max {
		return silkInt16Max
	}
	return a
}

// smulwb is silk_SMULWB: (a * (int16)b) >> 16, 64-bit intermediate.
func smulwb(a, b int32) int32 {
	return int32((int64(a) * int64(int16(b))) >> 16)
}

// vadSmlawb is silk_SMLAWB: a + ((b * (int16)c) >> 16).
func vadSmlawb(a, b, c int32) int32 {
	return int32(int64(a) + ((int64(b) * int64(int16(c))) >> 16))
}

// smulww is silk_SMULWW: (a * b) >> 16, 64-bit intermediate.
func smulww(a, b int32) int32 {
	return int32((int64(a) * int64(b)) >> 16)
}

// vadSmulbb is silk_SMULBB: (int16)a * (int16)b.
func vadSmulbb(a, b int32) int32 {
	return int32(int16(a)) * int32(int16(b))
}

// smlabb is silk_SMLABB: a + (int16)b * (int16)c.
func smlabb(a, b, c int32) int32 {
	return a + int32(int16(b))*int32(int16(c))
}

// addPosSat32 is silk_ADD_POS_SAT32: saturating add of two non-negative values.
func addPosSat32(a, b int32) int32 {
	if (uint32(a)+uint32(b))&0x8000_0000 != 0 {
		return silkInt32Max
	}
	return a + b
}

func div32(a, b int32) int32 {
	return a / b
}

// clz32 is silk_CLZ32: leading zeros (silk returns 32 for 0).
func clz32(x int32) int32 {
	return int32(bits.LeadingZeros32(uint32(x)))
}

// ror32 is silk_ROR32: rotate right by `rot` (negative `rot` rotates left). `rot & 31` folds both directions
// onto a single rotate_right (rotate_right by -5 == rotate_right by 27).
func ror32(a32, rot int32) int32 {
	return int32(bits.RotateLeft32(uint32(a32), -int(rot&31)))
}

// vadClzFrac is silk_CLZ_FRAC.
func vadClzFrac(inp int32) (int32, int32) {
	lz := clz32(inp)
	fracQ7 := ror32(inp, 24-lz) & 0x7f
	return lz, fracQ7
}

// vadLin2log is silk_lin2log: approximation of 128 * log2().
func vadLin2log(inLin int32) int32 {
	lz, fracQ7 := vadClzFrac(inLin)
	return vadSmlawb(fracQ7, fracQ7*(128-fracQ7), 179) + ((31 - lz) << 7)
}

// sqrtApprox is silk_SQRT_APPROX.
func sqrtApprox(x int32) int32 {
	if x <= 0 {
		return 0
	}
	lz, fracQ7 := vadClzFrac(x)
	var y int32
	if lz&1 != 0 {
		y = 32768
	} else {
		y = 46214
	}
	y >>= lz >> 1
	return vadSmlawb(y, y, vadSmulbb(213, fracQ7))
}

// vadSigmQ15 is silk_sigm_Q15: piecewise-linear sigmoid approximation.
func vadSigmQ15(inQ5 int32) int32 {
	slope := [6]int32{237, 153, 73, 30, 12, 7}
	pos := [6]int32{16384, 23955, 28861, 31213, 32178, 32548}
	neg := [6]int32{16384, 8812, 3906, 1554, 589, 219}
	if inQ5 < 0 {
		inQ5 := -inQ5
		if inQ5 >= 6*32 {
			return 0
		}
		ind := int(inQ5 >> 5)
		return neg[ind] - vadSmulbb(slope[ind], inQ5&0x1F)
	} else if inQ5 >= 6*32 {
		return 32767
	}
	ind := int(inQ5 >> 5)
	return pos[ind] + vadSmulbb(slope[ind], inQ5&0x1F)
}

// VAD constants.
const (
	vadNBands                        = 4
	vadInternalSubframesLog2         = 2
	vadInternalSubframes             = 1 << vadInternalSubframesLog2
	vadNoiseLevelSmoothCoefQ16       = 1024
	vadNoiseLevelsBias               = 50
	vadNegativeOffsetQ5              = 128
	vadSnrFactorQ16                  = 45000
	aFb120                     int32 = 3894 << 1 // i16
	aFb121                     int32 = -29322    // i16
)

var tiltWeights = [vadNBands]int32{30000, 6000, -12000, -12000}

// speechActivityDtxThresQ8 is SILK_FIX_CONST(0.05, 8) = 12: `(0.05 * 256.0) as i32`.
const speechActivityDtxThresQ8 int32 = 12

// smplVadState is the persistent SILK VAD state, carried across packets.
type smplVadState struct {
	anaState       [2]int32
	anaState1      [2]int32
	anaState2      [2]int32
	xnrgSubfr      [vadNBands]int32
	nl             [vadNBands]int32
	invNl          [vadNBands]int32
	noiseLevelBias [vadNBands]int32
	counter        int32
	hpState        int32
	// VAD tuning params (default 0 in this config).
	noiseLvlUpdateSpeed int32
	nonBinariness       int32
	highpassSharpness   int32
	// DTX hangover (per-packet).
	remainingDtxHangover int32
	hangoverMs           int32
}

// vadType is the per-internal-frame VAD type used by the hangover logic.
type vadType uint8

const (
	vadTypeActive vadType = iota
	vadTypeInactive
	vadTypeHangover
)

// vadPacketResult is the VAD output for one 60 ms packet: per-internal-frame speech-activity
// probability, the packet-level `coded_as_active_voice` flag, and the raw decision behind it.
type vadPacketResult struct {
	vadResults [3]float32
	// Any internal frame active OR within the DTX hangover: the frame is CODED as active voice,
	// which is what gates the two LSF symbols. Equals `MlowToc::active` (`vad || bit1`).
	codedAsActiveVoice bool
	// Any internal frame active before the hangover promotion: the TOC's VAD bit itself. A packet
	// that is only active through the hangover sets bit 1 instead, which is the reference's
	// `0x12`.
	speechDetected bool
}

// newSmplVadState initializes the VAD state.
func newSmplVadState() smplVadState {
	var noiseLevelBias, nl, invNl [vadNBands]int32
	for b := 0; b < vadNBands; b++ {
		noiseLevelBias[b] = max(vadNoiseLevelsBias/(int32(b)+1), 1)
		nl[b] = 100 * noiseLevelBias[b]
		invNl[b] = silkInt32Max / nl[b]
	}
	return smplVadState{
		nl:                   nl,
		invNl:                invNl,
		noiseLevelBias:       noiseLevelBias,
		counter:              15,
		remainingDtxHangover: 60, // SMPL_DEFAULT_HANGOVER_MS
		hangoverMs:           60,
	}
}

// filtHp is the first-order ARMA HP filter with zero at DC, in place over `len` samples.
func (s *smplVadState) filtHp(x []int32, bQ16, aNegQ16 int32, n int) {
	if n > len(x) {
		n = len(x)
	}
	for i := 0; i < n; i++ {
		inval := smulwb(bQ16, x[i])
		outval := sat16(s.hpState - inval)
		s.hpState = vadSmlawb(inval, aNegQ16, outval)
		x[i] = outval
	}
}

// smplVadStateAnaFiltBank1 is the 2-band split via first-order allpass filters. Writes low band to
// `out_l[0..N/2]` and high band to `out_h[0..N/2]`. `s` is the carried 2-element state.
func smplVadStateAnaFiltBank1(inp []int32, s *[2]int32, outL, outH []int32, n int) {
	n2 := n >> 1
	for k := 0; k < n2; k++ {
		in32 := inp[2*k] << 10
		y := in32 - s[0]
		x := vadSmlawb(y, y, aFb121)
		out1 := s[0] + x
		s[0] = in32 + x

		in32 = inp[2*k+1] << 10
		y = in32 - s[1]
		x = smulwb(y, aFb120)
		out2 := s[1] + x
		s[1] = in32 + x

		outL[k] = sat16(rshiftRound(out2+out1, 11))
		outH[k] = sat16(rshiftRound(out2-out1, 11))
	}
}

// getNoiseLevels smooths the per-band noise-level estimate toward the current band energies.
func (s *smplVadState) getNoiseLevels(pX *[vadNBands]int32) {
	var minCoef int32
	if s.counter < 1000 {
		minCoef = div32(silkInt16Max, (s.counter>>4)+1)
		s.counter++
	}
	for b := 0; b < vadNBands; b++ {
		px := pX[b]
		nl := s.nl[b]
		nrg := addPosSat32(px, s.noiseLevelBias[b])
		invNrg := div32(silkInt32Max, nrg)
		var coef int32
		if nrg > (nl << 3) {
			coef = vadNoiseLevelSmoothCoefQ16 >> 3
		} else if nrg < nl {
			coef = vadNoiseLevelSmoothCoefQ16
		} else {
			coef = smulwb(smulww(invNrg, nl), vadNoiseLevelSmoothCoefQ16<<1)
		}
		coef = (coef * (100 + s.noiseLvlUpdateSpeed)) / 100
		coef = max(coef, minCoef)
		s.invNl[b] = vadSmlawb(s.invNl[b], invNrg-s.invNl[b], coef)
		s.nl[b] = min(div32(silkInt32Max, s.invNl[b]), 0x00FF_FFFF)
	}
}

// getSaQ8 returns speech_activity_Q8 for one `framelen`-sample int16 frame.
func (s *smplVadState) getSaQ8(pIn []int32, framelen int) int32 {
	decFl1 := framelen >> 1
	decFl2 := framelen >> 2
	decFl3 := framelen >> 3

	var xOffset [vadNBands]int
	xOffset[0] = 0
	xOffset[1] = decFl3 + decFl2
	xOffset[2] = xOffset[1] + decFl3
	xOffset[3] = xOffset[2] + decFl2
	xTotal := xOffset[3] + decFl1
	// Rust allocates `vec![0i32; x_total]`; 400 covers the codec's 320-sample frames.
	var xBuf [400]int32
	var x []int32
	if xTotal <= len(xBuf) {
		x = xBuf[:xTotal]
	} else {
		x = make([]int32, xTotal)
	}

	// 0-8 kHz -> 0-4 kHz (X[0..]) and 4-8 kHz (X[offset3..])
	{
		lo, hi := x[:xOffset[3]], x[xOffset[3]:]
		st := s.anaState
		smplVadStateAnaFiltBank1(pIn, &st, lo, hi, framelen)
		s.anaState = st
	}
	// 0-4 kHz -> 0-2 kHz (X[0..]) and 2-4 kHz (X[offset2..]); reads/writes within X[0..]
	{
		st := s.anaState1
		smplVadStateAnaFiltBank1Inplace(x, xOffset[2], &st, decFl1)
		s.anaState1 = st
	}
	// 0-2 kHz -> 0-1 kHz (X[0..]) and 1-2 kHz (X[offset1..])
	{
		st := s.anaState2
		smplVadStateAnaFiltBank1Inplace(x, xOffset[1], &st, decFl2)
		s.anaState2 = st
	}

	// HP filter on the lowest band, -3 dB @ 66 Hz.
	aNegQ16 := int32(53084)
	aNegQ16 = (aNegQ16 * (100 - s.highpassSharpness)) / 100
	bQ16 := (65536 + aNegQ16) / 2
	// Rust filters a copy of X[..dec_fl3] and copies it back; filtering in place is identical.
	s.filtHp(x[:decFl3], bQ16, aNegQ16, decFl3)

	// Energy in each band.
	var xnrg [vadNBands]int32
	for b := 0; b < vadNBands; b++ {
		dec := framelen >> min(vadNBands-b, vadNBands-1)
		decSubfrLen := dec >> vadInternalSubframesLog2
		decSubfrOffset := 0
		xnrg[b] = s.xnrgSubfr[b]
		sumSquared := int32(0)
		for sf := 0; sf < vadInternalSubframes; sf++ {
			sumSquared = 0
			for i := 0; i < decSubfrLen; i++ {
				xTmp := x[xOffset[b]+i+decSubfrOffset] >> 3
				sumSquared = smlabb(sumSquared, xTmp, xTmp)
			}
			if sf < vadInternalSubframes-1 {
				xnrg[b] = addPosSat32(xnrg[b], sumSquared)
			} else {
				xnrg[b] = addPosSat32(xnrg[b], sumSquared>>1)
			}
			decSubfrOffset += decSubfrLen
		}
		s.xnrgSubfr[b] = sumSquared
	}

	s.getNoiseLevels(&xnrg)

	// Signal-plus-noise to noise ratio.
	sumSquared := int32(0)
	inputTilt := int32(0)
	for b := 0; b < vadNBands; b++ {
		speechNrg := xnrg[b] - s.nl[b]
		if speechNrg > 0 {
			var ratioQ8 int32
			if xnrg[b]&^0x007F_FFFF == 0 { // xnrg & 0xFF80_0000u32 as i32
				ratioQ8 = div32(xnrg[b]<<8, s.nl[b]+1)
			} else {
				ratioQ8 = div32(xnrg[b], (s.nl[b]>>8)+1)
			}
			snrQ7 := vadLin2log(ratioQ8) - 8*128
			sumSquared = smlabb(sumSquared, snrQ7, snrQ7)
			if speechNrg < (1 << 20) {
				snrQ7 = smulwb(sqrtApprox(speechNrg)<<6, snrQ7)
			}
			inputTilt = vadSmlawb(inputTilt, tiltWeights[b], snrQ7)
		}
	}
	sumSquared = div32(sumSquared, vadNBands)
	pSnrDbQ7 := int32(int16(3 * sqrtApprox(sumSquared)))

	vadSnrFactorQ16v := (vadSnrFactorQ16 * (150 - s.nonBinariness)) / 150
	saQ15 := vadSigmQ15(smulwb(vadSnrFactorQ16v, pSnrDbQ7) - vadNegativeOffsetQ5)

	_ = inputTilt // input_tilt_Q15 unused downstream here

	return min(saQ15>>7, silkUint8Max)
}

// smplVadStateAnaFiltBank1Inplace is the in-place 2-band split: reads X[0..n], writes low band to
// X[0..n/2] and high band to X[hi_off..hi_off+n/2]. Input and low-out overlap; `in[2k]/in[2k+1]`
// are read before `out[k]` is written, so a forward scan is safe since k <= 2k.
func smplVadStateAnaFiltBank1Inplace(x []int32, hiOff int, s *[2]int32, n int) {
	n2 := n >> 1
	for k := 0; k < n2; k++ {
		in32 := x[2*k] << 10
		y := in32 - s[0]
		xx := vadSmlawb(y, y, aFb121)
		out1 := s[0] + xx
		s[0] = in32 + xx

		in32 = x[2*k+1] << 10
		y = in32 - s[1]
		xx = smulwb(y, aFb120)
		out2 := s[1] + xx
		s[1] = in32 + xx

		x[hiOff+k] = sat16(rshiftRound(out2-out1, 11))
		x[k] = sat16(rshiftRound(out2+out1, 11))
	}
}

// processPacket processes one 60 ms packet (3 internal frames of `framelen` int16 samples).
// `activity` mirrors the Opus VAD decision; we use NO_DECISION (-1), so the type is
// threshold-driven, then hangover runs.
func (s *smplVadState) processPacket(pcmI16 []int16, framelen int) vadPacketResult {
	const framesPerPacket = 3
	const packetMs int32 = 60
	var vadResults [3]float32
	// Reject short packets up front so the frame loop's fixed-stride indexing can't panic.
	expectedLen := framesPerPacket * framelen
	if len(pcmI16) < expectedLen {
		return vadPacketResult{vadResults: vadResults}
	}
	vt := [3]vadType{vadTypeInactive, vadTypeInactive, vadTypeInactive}
	// Rust collects each frame into a fresh Vec<i32>; 320 covers the codec's frames.
	var frameBuf [320]int32
	for i := 0; i < framesPerPacket; i++ {
		t := i * framelen
		var frame []int32
		if framelen <= len(frameBuf) {
			frame = frameBuf[:framelen]
		} else {
			frame = make([]int32, framelen)
		}
		for j, smp := range pcmI16[t : t+framelen] {
			frame[j] = int32(smp)
		}
		saQ8 := s.getSaQ8(frame, framelen)
		vadResults[i] = float32(saQ8) / 256.0
		// activity == NO_DECISION path.
		if saQ8 > speechActivityDtxThresQ8 {
			vt[i] = vadTypeActive
		} else {
			vt[i] = vadTypeInactive
		}
	}

	codedAsActiveVoice := false
	speechDetected := false
	for i := range vt {
		if vt[i] == vadTypeActive {
			s.remainingDtxHangover = s.hangoverMs
			speechDetected = true
		} else if s.remainingDtxHangover > 0 {
			vt[i] = vadTypeHangover
			s.remainingDtxHangover -= packetMs / framesPerPacket
		}
		if vt[i] != vadTypeInactive {
			codedAsActiveVoice = true
		}
	}

	return vadPacketResult{
		vadResults:         vadResults,
		codedAsActiveVoice: codedAsActiveVoice,
		speechDetected:     speechDetected,
	}
}

// rshiftRound is silk_RSHIFT_ROUND.
func rshiftRound(a, shift int32) int32 {
	if shift == 1 {
		return (a >> 1) + (a & 1)
	}
	return ((a >> (shift - 1)) + 1) >> 1
}
