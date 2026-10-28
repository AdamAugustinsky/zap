// Ported from whatsapp-rust 5e72baf wacore/src/voip/mlow/smpl_pitch_enc.rs, MIT.
//
// SILK-style multi-stage pitch estimator. It HP-filters and 2x-downsamples the perceptually-weighted
// `ltp_buf`, runs an open-loop block-track survivor search at the coarse (16 kHz upsampled from
// 8 kHz) resolution, refines per-block at full resolution around the survivors, and folds in
// rate/prev-lag/spectral-harmonicity biases. The outputs (`pitchcorr`, per-subframe `lags[8]`,
// `avg_lag`, `harm_strength`) feed the bit-exact `smpl_get_signal_mode` classifier.
//
// The constant tables (blocksegs/blocktracks/CMFs, decoded from a packed bitstream via the range
// decoder + `dcmf_to_cmf` at load time) are loaded from a committed seed blob rather than re-porting
// the decoders, since they are immutable. Only the 20 ms / 8-subframe config is supported (the active
// MLow 1:1 path); 10 ms frames never occur here.

package mlow

import (
	_ "embed"
	"math"
	"sync"
)

// codec constants
const (
	fsKhz          int32 = 16
	stage1FsKhz    int32 = 8
	coarseFsKhz    int32 = 16
	totInterpDelay int32 = 6
	numSubframes         = 8
	minpitchMs     int32 = 2
	maxpitchMs     int32 = 20
	minpitchLen    int32 = minpitchMs * fsKhz // 32
	// MAXPITCH_LEN (320) is maxpitchLen in shared.go.
	minpitchStage1 int32 = minpitchMs*stage1FsKhz - totInterpDelay // 10
	maxpitchStage1 int32 = maxpitchMs*stage1FsKhz + totInterpDelay // 166
)

// J#10736: coarse penalty 0.105, fine penalty 0.0046875 (0.3 / 64).
const pitchDeltawght float32 = 0.3
const pitchShortwght1 float32 = 0.04
const specHarmBias float32 = 2.5

// J#10736 uses 0x3f333333 for the previous-lag bias.
const prevwght float32 = 0.7
const prevwghtSpan float32 = 0.15
const ratewghtHr float32 = 0.022

const (
	lagSubfrlen       int32 = 40
	lagSubfrlenStage1 int32 = stage1FsKhz * lagSubfrlen / fsKhz // 20
	pitchblockMs      int32 = 2
	pitchLookaheadLen       = 7
	maxLtpBufLen            = 659
	fLen                    = 257

	pitchDownsampDelay  = 7
	pitchInterpolDelayC = 4

	// PITCH_NUM_BLOCKS (9) is pitchNumBlocks in shared.go.
	pitchblock    = int(pitchblockMs * fsKhz)                    // 32
	numLagsStage1 = int(maxpitchStage1 - minpitchStage1 + 1)     // 157
	numlagsCoarse = int(coarseFsKhz * (maxpitchMs - minpitchMs)) // 288
	numlagsFs     = int(fsKhz * (maxpitchMs - minpitchMs))       // 288
)

// numstates1 is the `numstates1` block-track survivors at complexity 5-8 (`pitch_numstates1 = 24`);
// overrides the per-stream init default of 8.
const numstates1 = 24

// lowComplexity: complexity-8 is NOT low-complexity (numstates1 > 4), so `low_complexity_mode == false`.
const lowComplexity = false

// lowRate: 20 kbps is the HIGH-rate path (`low_rate == false`).
const lowRate = false

// decoded constant tables (expanded once from the pitch seed ROM)

type blockSeg struct {
	nblocks int
	blocks  []int
	seglens []int
}

type blockTrack struct {
	track       [numSubframes]int
	meanblock   float32
	trackdeltas float32
}

type pitchTables struct {
	blocksegs          []blockSeg
	blocktracks        []blockTrack
	blocksegs2idx      []int
	blocksegIdxCmf     []uint32
	deltaLagCmfs       [][]uint32
	blocksegsIx        [][2]int
	firstblockRange    [][2]int
	blockTransitionCmf [][]uint32
}

//go:embed testdata/pitch_seed.bin
var pitchEncPitchSeedBin []byte

// pitchEncTables is the Rust `TABLES` OnceLock.
var pitchEncTables = sync.OnceValue(func() *pitchTables {
	seed := loadBlobBuffa[pitchSeed](pitchEncPitchSeedBin)
	t := seed.build()
	return &t
})

func loadPitchTables() *pitchTables {
	return pitchEncTables()
}

// pitchTablesFromParts assembles the runtime tables from the expanded parts (the pitch seed
// builder calls this).
func pitchTablesFromParts(
	blocksegs []blockSeg,
	blocktracks []blockTrack,
	blocksegs2idx []int,
	blocksegIdxCmf []uint32,
	deltaLagCmfs [][]uint32,
	blocksegsIx [][2]int,
	firstblockRange [][2]int,
	blockTransitionCmf [][]uint32,
) pitchTables {
	return pitchTables{
		blocksegs:          blocksegs,
		blocktracks:        blocktracks,
		blocksegs2idx:      blocksegs2idx,
		blocksegIdxCmf:     blocksegIdxCmf,
		deltaLagCmfs:       deltaLagCmfs,
		blocksegsIx:        blocksegsIx,
		firstblockRange:    firstblockRange,
		blockTransitionCmf: blockTransitionCmf,
	}
}

// pitchEstState is the per-stream estimator state (the `PitchEstimator` non-scratch fields).
// `prev_lagblk/prev_lagidx` are reset to -1 at frame boundaries by the encoder (`reset_cond`).
type pitchEstState struct {
	prevLag       float32
	prevPitchCorr float32
	prevLagblk    int32
	prevLagidx    int32
	// Go-only: buffers [pitchWithSearch] reuses across calls (Rust allocates them as Vecs per
	// call). Not estimator state: every call overwrites what it reads.
	scratch pitchEncScratch
}

// pitchEncScratch holds pitchWithSearch's per-call vectors so a warm estimator does not allocate.
type pitchEncScratch struct {
	e1, e1Ext       []float32
	e1Fs, e1FsExt   []float32
	utils           []float32
	taken           []bool
	trackIdx        []int
	lagindsSurv     [][numSubframes]int32
	blocksegsIxList []int
}

func defaultPitchEstState() pitchEstState {
	return pitchEstState{
		prevLag:       0.0,
		prevPitchCorr: 0.0,
		prevLagblk:    -1,
		prevLagidx:    -1,
	}
}

// resetCond clears the cross-frame lag-block predictor (called after the last frame of a packet and
// after any unvoiced frame, so cond-coding restarts).
func (s *pitchEstState) resetCond() {
	s.prevLagblk = -1
	s.prevLagidx = -1
}

// pitchResult is the pitch estimator result for one internal frame. `laginds`/`blockseg_idx` are the
// estimator's chosen per-block lag indices and contour; the classifier consumes
// `pitchcorr`/`lags`/`avg_lag`/`harm`, while the wire pitch encoder (downstream) can use the contour
// to carry the per-block lags.
type pitchResult struct {
	pitchcorr    float32
	lags         [numSubframes]float32
	laginds      [numSubframes]int32
	avgLag       float32
	harmStrength float32
	blocksegIdx  int
}

// filters / DSP helpers

// pitchHpFilter is ARMA1 with `pitch_hp_b={1,-1}`, `pitch_hp_a={1,-0.96}`, zero state at call start.
// MA1 (`ma[n] = x[n] - x[n-1]`, `ma[0] = x[0]`) then AR1 in a 5-sample unrolled form using
// precomputed powers of `ar1 = 0.96`, so the float accumulation order is stable.
func pitchHpFilter(x, out []float32) {
	n := len(x)
	// MA1 into `out` (state_ma = x[-1] = 0).
	stateMa := float32(0.0)
	for i := 0; i < n; i++ {
		out[i] = x[i] - stateMa
		stateMa = x[i]
	}
	// AR1: y[n] = ma[n] + 0.96*y[n-1], unrolled by 5.
	ar1 := float32(0.96)
	ar12 := ar1 * ar1
	ar13 := ar1 * ar12
	ar14 := ar1 * ar13
	ar15 := ar1 * ar14
	ytmp := float32(0.0) // y[-1]
	idx := 0
	for idx+4 < n {
		x0 := out[idx]
		x1 := out[idx+1]
		x2 := out[idx+2]
		x3 := out[idx+3]
		x4 := out[idx+4]
		out[idx+4] = x4 + float32(ar1*x3) + float32(ar12*x2) + float32(ar13*x1) + float32(ar14*x0) + float32(ar15*ytmp)
		out[idx] = x0 + float32(ar1*ytmp)
		out[idx+1] = x1 + float32(ar1*x0) + float32(ar12*ytmp)
		out[idx+2] = x2 + float32(ar1*x1) + float32(ar12*x0) + float32(ar13*ytmp)
		out[idx+3] = x3 + float32(ar1*x2) + float32(ar12*x1) + float32(ar13*x0) + float32(ar14*ytmp)
		ytmp = out[idx+4]
		idx += 5
	}
	for idx < n {
		ytmp = out[idx] + float32(ytmp*ar1)
		out[idx] = ytmp
		idx++
	}
}

var downsampFilt = [2*pitchDownsampDelay + 1]float32{
	-0.045472838,
	0.0,
	0.06366198,
	0.0,
	-0.10610329,
	0.0,
	0.31830987,
	0.5,
	0.31830987,
	0.0,
	-0.10610329,
	0.0,
	0.06366198,
	0.0,
	-0.045472838,
}

// pitchDownsample is the 2x decimating FIR. `ptr_in` has `PITCH_DOWNSAMP_DELAY` lead samples
// (offset) already written into `ptr_out[0..offset]`; output length is `(L - 2*delay)/2`.
func pitchDownsample(ptrIn []float32, l int, ptrOut []float32) int {
	d := pitchDownsampDelay
	n := (l - 2*d) / 2
	for j := 0; j < n; j++ {
		tmp := ptrIn[2*j+d] * downsampFilt[d]
		for i := 0; i < d; i += 2 {
			tmp += float32((ptrIn[2*j+i] + ptrIn[2*j+2*d-i]) * downsampFilt[i])
		}
		ptrOut[j] = tmp
	}
	return n
}

var interpolFiltC = [2 * pitchInterpolDelayC]float32{
	-0.0024414062,
	0.023925781,
	-0.119628906,
	0.59814453,
	0.59814453,
	-0.119628906,
	0.023925783,
	-0.0024414062,
}

// upsampECore writes `2*len` samples backwards from `y` using `x` (read backwards). Even taps copy
// `x`, odd taps average adjacent. `y_end`/`x_end` are the indices of the LAST written/read.
func upsampECore(buf []float32, xEnd, yEnd, n int) {
	xi := xEnd
	yi := yEnd
	for range n {
		v := (buf[xi] + buf[xi+1]) * 0.5
		buf[yi] = v
		yi--
		buf[yi] = buf[xi]
		yi--
		xi--
	}
}

// upsampCCore is like `upsamp_e_core` but the interpolated sample uses the 8-tap `INTERPOL_FILT_C`.
func upsampCCore(buf []float32, xEnd, yEnd, n int) {
	xi := xEnd
	yi := yEnd
	for range n {
		tmp := float32(0.0)
		for j := 0; j < pitchInterpolDelayC; j++ {
			a := buf[xi+j-(pitchInterpolDelayC-1)]
			b := buf[xi+pitchInterpolDelayC-j]
			tmp += float32((a + b) * interpolFiltC[j])
		}
		buf[yi] = tmp
		yi--
		buf[yi] = buf[xi]
		yi--
		xi--
	}
}

// pitchEncSmplNrg is `x.iter().map(|&v| v * v).sum()`: a left fold from -0.0.
func pitchEncSmplNrg(x []float32) float32 {
	s := float32(math.Copysign(0, -1))
	for _, v := range x {
		s += float32(v * v)
	}
	return s
}

// getMaxi is the argmax; ties resolve to the FIRST index. A simple strict-`>` scan (lowest index
// wins) matches the reference tree-reduction (validated by a tie-probe harness on this data).
func getMaxi(x []float32) int {
	bi := 0
	best := x[0]
	for n := 1; n < len(x); n++ {
		if x[n] > best {
			best = x[n]
			bi = n
		}
	}
	return bi
}

// pitchEncGetMaxiK returns the K highest-value indices. The ascending masked-max scan (strict `>`,
// lowest-index-wins) is the validated equivalent of the reference tree selection. Returns them in
// selection order (descending value). It allocates; pitchWithSearch uses [pitchEncGetMaxiKInto].
func pitchEncGetMaxiK(x []float32, k int) []int {
	out, _ := pitchEncGetMaxiKInto(make([]int, 0, k), make([]bool, len(x)), x, k)
	return out
}

// pitchEncGetMaxiKInto is Rust `get_maxi_k` with its two Vecs supplied by the caller: the result is
// appended to `out[:0]` and `taken` is resized to len(x) and cleared. Both (possibly grown) buffers
// are returned for reuse.
func pitchEncGetMaxiKInto(out []int, taken []bool, x []float32, k int) ([]int, []bool) {
	if cap(taken) < len(x) {
		taken = make([]bool, len(x))
	}
	taken = taken[:len(x)]
	clear(taken)
	out = out[:0]
	for range k {
		bi := -1
		best := float32(-math.MaxFloat32)
		for n := 0; n < len(x); n++ {
			if !taken[n] && (bi < 0 || x[n] > best) {
				best = x[n]
				bi = n
			}
		}
		if bi < 0 {
			break
		}
		taken[bi] = true
		out = append(out, bi)
	}
	return out, taken
}

// E1 / C / E2 computation

// calcE1Inner is the running energy of `lag_subfrlen`-length windows ending just before lag
// `minpitch`, for each of `numlags` lags. `t` is the window-start anchor in `ltpbuf`.
func calcE1Inner(ltpbuf []float32, t int, minpitch, maxpitch int32, lagSubfrlen int) []float32 {
	return calcE1InnerInto(make([]float32, 0, int(maxpitch-minpitch+1)), ltpbuf, t, minpitch, maxpitch, lagSubfrlen)
}

// calcE1InnerInto is Rust `calc_E1_inner` appending to `dst[:0]` (grown if short) instead of a
// fresh Vec.
func calcE1InnerInto(dst []float32, ltpbuf []float32, t int, minpitch, maxpitch int32, lagSubfrlen int) []float32 {
	numlags := int(maxpitch - minpitch + 1)
	base := int(int32(t) - minpitch) // &ltpbuf[t - minpitch]
	_ = ltpbuf[base-(numlags-1):]    // reg[-i] for i in 0..numlags valid
	// reg points at ltpbuf[t - minpitch]; we index reg[0], reg[-i], reg[lag_subfrlen - i].
	reg0 := base // absolute index of reg[0]
	// Every element is written exactly once, in order, so the buffer is filled as it is built
	// rather than allocated zeroed and overwritten.
	e1 := dst[:0]
	e1 = append(e1, maxF32(pitchEncSmplNrg(ltpbuf[reg0:reg0+lagSubfrlen]), 1e-9))
	for i := 1; i < numlags; i++ {
		rm := ltpbuf[reg0-i]
		rs := ltpbuf[reg0+lagSubfrlen-i]
		e1 = append(e1, maxF32(e1[i-1]+float32(rm*rm)-float32(rs*rs), 1e-9))
	}
	return e1
}

// calcE1 is the per-subframe E1 by computing an extended E1_ once then offsetting per subframe.
// Returns the `numlags * numsubfrs` row-major block, built by copying each subframe's window out of
// the extended row (again: fully written, so never zeroed first).
func calcE1(ltpbuf []float32, ltpbufLen, numsubfrs int, minpitch, maxpitch int32, lagSubfrlen int) []float32 {
	numlags := int(maxpitch - minpitch + 1)
	e1, _ := calcE1Into(make([]float32, 0, numlags*numsubfrs), nil, ltpbuf, ltpbufLen, numsubfrs, minpitch, maxpitch, lagSubfrlen)
	return e1
}

// calcE1Into is Rust `calc_E1` with the result appended to `dst[:0]` and the extended row built in
// `ext[:0]` (each grown if short) instead of fresh Vecs. Returns both for reuse.
func calcE1Into(dst, ext []float32, ltpbuf []float32, ltpbufLen, numsubfrs int, minpitch, maxpitch int32, lagSubfrlen int) ([]float32, []float32) {
	numlags := int(maxpitch - minpitch + 1)
	maxpitch_ := maxpitch + (int32(numsubfrs)-1)*int32(lagSubfrlen)
	numlags_ := int(maxpitch_ - minpitch + 1)
	t := ltpbufLen - lagSubfrlen
	e1Ext := calcE1InnerInto(ext, ltpbuf, t, minpitch, maxpitch_, lagSubfrlen)
	e1 := dst[:0]
	// Subframe `sf` reads the extended row shifted back by `sf` lag subframes; the last one lands
	// exactly on index 0, so the window never runs off the front.
	offset := numlags_ - numlags
	for range numsubfrs {
		e1 = append(e1, e1Ext[offset:offset+numlags]...)
		offset = satSubInt(offset, lagSubfrlen)
	}
	return e1, e1Ext
}

func pitchEncDotProd(a, b []float32, n int) float32 {
	r := float32(0.0)
	for i := 0; i < n; i++ {
		r += float32(a[i] * b[i])
	}
	return r
}

// calcCE2 is the stage-1 cross-correlation `C` (8-sample dot, NUM_LAGS_STAGE1 lags/subframe) and
// per-subframe target energy `E2`.
func calcCE2(c, e2, ltpbuf []float32, ltpbufLen, numsubfrs int) {
	t := ltpbufLen - int(lagSubfrlenStage1)*numsubfrs
	for sf := 0; sf < numsubfrs; sf++ {
		tgt := ltpbuf[t : t+20]
		reg0 := int(int32(t) - minpitchStage1)
		for i := 0; i < numLagsStage1; i++ {
			// dot_prod_20(tgt, &reg[-i]) where reg=&ltpbuf[reg0]
			r := ltpbuf[reg0-i : reg0-i+20]
			c[sf*numLagsStage1+i] = pitchEncDotProd(tgt, r, 20)
		}
		t += int(lagSubfrlenStage1)
		e2[sf] = maxF32(pitchEncDotProd(tgt, tgt, 20), 1e-9)
	}
}

// upsampEFast is the in-place 2x upsample of a per-subframe E array, high subframe first.
func upsampEFast(buf []float32, numsubfrs int, minpitch *int32, numlags *int) {
	nin := *numlags
	nout := (nin - 1) * 2
	for sf := numsubfrs - 1; sf >= 0; sf-- {
		xEnd := sf*nin + nin - 2
		yEnd := sf*nout + nout - 1
		upsampECore(buf, xEnd, yEnd, nin-1)
	}
	*numlags = nout
	*minpitch *= 2
}

// upsampCFast is the in-place 2x upsample of a per-subframe C array using the interpolation filter.
func upsampCFast(buf []float32, numsubfrs int, minpitch *int32, numlags *int) {
	nin := *numlags
	nout := (nin - pitchInterpolDelayC) * 2
	for sf := numsubfrs - 1; sf >= 0; sf-- {
		xEnd := sf*nin + nin - 1 - pitchInterpolDelayC
		yEnd := sf*nout + nout - 1
		upsampCCore(buf, xEnd, yEnd, nin-(pitchInterpolDelayC*2-1))
	}
	*numlags = nout
	*minpitch *= 2
}

// maxSeglen is the longest segment the blockseg table can encode (`uniform(4) + 1`).
const maxSeglen = 4

// lagindCacheLen is one memo slot per (start subframe, segment length, block) triple.
const lagindCacheLen = numSubframes * maxSeglen * pitchNumBlocks

// lagindCacheSlot is the dense slot for the fine-search argmax memo, or ok=false for a triple
// outside the table's declared ranges -- which the shipped ROM never produces, and which then
// simply goes unmemoized rather than indexing out of bounds.
func lagindCacheSlot(startSf, seglen, block int) (int, bool) {
	if startSf >= numSubframes || seglen == 0 || seglen > maxSeglen || block >= pitchNumBlocks {
		return 0, false
	}
	return (startSf*maxSeglen+(seglen-1))*pitchNumBlocks + block, true
}

func dotProd40(a, b []float32) float32 {
	r := float32(0.0)
	for i := 0; i < 40; i++ {
		r += float32(a[i] * b[i])
	}
	return r
}

func sumdeltas(laginds []int32, numsubfrs int) int32 {
	ret := int32(0)
	for i := 1; i < numsubfrs; i++ {
		ret += absI32(laginds[i] - laginds[i-1])
	}
	return ret
}

// encodeLagsBits is the rate (bits) the lag indices would cost, used as a survivor bias.
// Accumulates n_bits with no entropy-coding side-effects (the cost-only path).
func encodeLagsBits(tab *pitchTables, blocksegsIx int, laginds []int32, prevLagblk, prevLagidx int32, mode int) float32 {
	nBits := float32(0.0)
	ixJulia := int32(tab.blocksegs2idx[blocksegsIx])
	blocksize := pitchblockMs * fsKhz * 2 // 64
	pblockseg := &tab.blocksegs[blocksegsIx]

	if prevLagblk < 0 {
		cmf := tab.blocksegIdxCmf
		nBits += ecEncodeBits(
			cmf[ixJulia-1],
			cmf[ixJulia],
			cmf[len(tab.blocksegs)],
		)
	} else {
		cmf := tab.blockTransitionCmf[prevLagblk]
		b0 := pblockseg.blocks[0]
		nBits += ecEncodeBits(cmf[b0], cmf[b0+1], cmf[pitchNumBlocks])
		startIx := int32(tab.firstblockRange[b0][0])
		cmfLen := int32(tab.firstblockRange[b0][1] - tab.firstblockRange[b0][0] + 1)
		cmf = tab.blocksegIdxCmf[startIx:]
		lo := int(ixJulia - startIx - 1)
		hi := int(ixJulia - startIx)
		nBits += ecEncodeBits(
			cmf[lo]-cmf[0],
			cmf[hi]-cmf[0],
			cmf[cmfLen]-cmf[0],
		)
	}

	blk := int32(pblockseg.blocks[0])
	deltaBlk := blk - prevLagblk
	startSeg := 0
	lagindsIx := 0
	if !((prevLagblk > -1) && (deltaBlk >= -1 && deltaBlk <= 2)) {
		nBits += 6.0 // uniform first-lag cost (log2 blocksize)
		prevLagblk = blk
		prevLagidx = laginds[lagindsIx]
		lagindsIx += pblockseg.seglens[0]
		startSeg = 1
	}
	deltaLagCmf := tab.deltaLagCmfs[mode]
	for k := startSeg; k < pblockseg.nblocks; k++ {
		blk = int32(pblockseg.blocks[k])
		idx := laginds[lagindsIx]
		lagindsIx += pblockseg.seglens[k]
		deltaBlk = blk - prevLagblk
		deltaIdx := idx - prevLagidx
		prevLagidxMod := prevLagidx - prevLagblk*blocksize
		deltaRangeStart := -prevLagidxMod + deltaBlk*blocksize
		cmfBase := int(deltaRangeStart + 2*blocksize - 1)
		ix := int(deltaIdx - deltaRangeStart)
		p0 := deltaLagCmf[cmfBase]
		nBits += ecEncodeBits(
			deltaLagCmf[cmfBase+ix]-p0,
			deltaLagCmf[cmfBase+ix+1]-p0,
			deltaLagCmf[cmfBase+int(blocksize)]-p0,
		)
		prevLagblk = blk
		prevLagidx = idx
	}
	return nBits
}

// ecEncodeBits returns the symbol's bit cost `-log2((fh-fl)/ft)` without coding it.
func ecEncodeBits(fl, fh, ft uint32) float32 {
	p := (float32(fh) - float32(fl)) / float32(ft)
	if p <= 0.0 {
		return 0.0
	}
	return -log2F32(p)
}

// smplEncodeLagsWire writes the blockseg selector + the per-40-block lag indices (`laginds`) to the
// range stream. This IS the voiced lag wire encode, the inverse of `decode_smpl_pitch`'s contour
// reconstruction. `prev_lagblk`/`prev_lagidx` are the lag predictor (-1 on the first frame of a
// packet / after a no-match); `mode` selects the delta-lag CMF (0/1/2 by the mean ACB gain). The
// decoder rebuilds the exact per-block contour from these bits, so its voiced ACB basis matches the
// encoder's.
func smplEncodeLagsWire(
	tab *pitchTables,
	enc *rangeEncoder,
	blocksegsIx int,
	laginds *[numSubframes]int32,
	prevLagblk int32,
	prevLagidx int32,
	mode int,
) {
	ixJulia := int32(tab.blocksegs2idx[blocksegsIx])
	blocksize := pitchblockMs * fsKhz * 2 // 64
	pblockseg := &tab.blocksegs[blocksegsIx]

	// Blockseg selector: absolute (CMF over all blocksegs) when no predictor, else block-transition
	// CMF for blocks[0] followed by the first-block-range CMF.
	if prevLagblk < 0 {
		cmf := tab.blocksegIdxCmf
		enc.encode(
			cmf[ixJulia-1],
			cmf[ixJulia],
			cmf[len(tab.blocksegs)],
		)
	} else {
		cmf := tab.blockTransitionCmf[prevLagblk]
		b0 := pblockseg.blocks[0]
		enc.encode(cmf[b0], cmf[b0+1], cmf[pitchNumBlocks])
		startIx := int32(tab.firstblockRange[b0][0])
		cmfLen := int32(tab.firstblockRange[b0][1] - tab.firstblockRange[b0][0] + 1)
		cmf = tab.blocksegIdxCmf[startIx:]
		lo := int(ixJulia - startIx - 1)
		hi := int(ixJulia - startIx)
		enc.encode(
			cmf[lo]-cmf[0],
			cmf[hi]-cmf[0],
			cmf[cmfLen]-cmf[0],
		)
	}

	blk := int32(pblockseg.blocks[0])
	deltaBlk := blk - prevLagblk
	startSeg := 0
	lagindsIx := 0
	if !((prevLagblk > -1) && (deltaBlk >= -1 && deltaBlk <= 2)) {
		// First lag uniform-coded over `blocksize`.
		idxMod := uint32(laginds[lagindsIx] - blk*blocksize)
		enc.encode(idxMod, idxMod+1, uint32(blocksize))
		prevLagblk = blk
		prevLagidx = laginds[lagindsIx]
		lagindsIx += pblockseg.seglens[0]
		startSeg = 1
	}
	deltaLagCmf := tab.deltaLagCmfs[mode]
	for k := startSeg; k < pblockseg.nblocks; k++ {
		blk = int32(pblockseg.blocks[k])
		idx := laginds[lagindsIx]
		lagindsIx += pblockseg.seglens[k]
		deltaBlk = blk - prevLagblk
		deltaIdx := idx - prevLagidx
		prevLagidxMod := prevLagidx - prevLagblk*blocksize
		deltaRangeStart := -prevLagidxMod + deltaBlk*blocksize
		cmfBase := int(deltaRangeStart + 2*blocksize - 1)
		ix := int(deltaIdx - deltaRangeStart)
		p0 := deltaLagCmf[cmfBase]
		enc.encode(
			deltaLagCmf[cmfBase+ix]-p0,
			deltaLagCmf[cmfBase+ix+1]-p0,
			deltaLagCmf[cmfBase+int(blocksize)]-p0,
		)
		prevLagblk = blk
		prevLagidx = idx
	}
}

// smplLagsPredictorAfter is the lag predictor after a voiced encode:
// `prev_lagblk = blocks[nblocks-1]`, `prev_lagidx = laginds[numsubfrs-1]`. Exposed so the analysis
// advances its mirror identically.
func smplLagsPredictorAfter(tab *pitchTables, blocksegsIx int, laginds *[numSubframes]int32) (int32, int32) {
	pblockseg := &tab.blocksegs[blocksegsIx]
	lastBlk := int32(pblockseg.blocks[pblockseg.nblocks-1])
	return lastBlk, laginds[numSubframes-1]
}

// spectralHarmonicityCached is the spectral harmonicity with a per-survivor cache (keyed by
// harmonic bin). Reuses the classifier's recompute via `harm_strength_at` for a single value; the
// loop here threads the cache per survivor.
func spectralHarmonicityCached(avgLag float32, f2w *[fLen]float32, cache []float32, reset bool) float32 {
	const harmUndef float32 = -10000.0
	if reset {
		for i := range cache {
			cache[i] = harmUndef
		}
	}
	invF2StepHz := float32(2.0) * float32(fLen-1) / 16000.0
	harmHz := 16000.0 / avgLag
	harmIx := f32ToI32(roundF32(harmHz * 2.0 * invF2StepHz))
	// The classifier's recompute is the single source of truth: an out-of-range bin only arises
	// from a degenerate lag and is handled there by a clamped recompute.
	if harmIx < 0 || int(harmIx) >= len(cache) {
		return harmStrengthAt(avgLag, f2w)
	}
	if cache[harmIx] > harmUndef {
		return cache[harmIx]
	}
	hs := harmStrengthAt(avgLag, f2w)
	cache[harmIx] = hs
	return hs
}

// smplPitch is the full estimator. `ltp_buf` is the perceptually-weighted speech of length
// `MAX_LTP_BUF_LEN` (the last `PITCH_LOOKAHEAD_LEN` samples are lookahead). `f2` is the LPC power
// spectrum. `coded_as_active_voice` gates the search (false -> unvoiced defaults). Mutates the
// cross-frame predictor in `st`.
func smplPitch(st *pitchEstState, ltpBuf []float32, f2 *[fLen]float32, codedAsActiveVoice bool) pitchResult {
	return pitchWithSearch(
		st,
		ltpBuf,
		f2,
		codedAsActiveVoice,
		pitchSearch{
			survivors:      numstates1,
			lowComplexity:  lowComplexity,
			lowRate:        lowRate,
			previousWeight: prevwght,
			deltaWeight:    pitchDeltawght,
		},
	)
}

// Oracle captures carry their search budget. The forged pjmedia default has
// four survivors/low-complexity mode; our encoder retains its 24-survivor budget.
type pitchSearch struct {
	survivors      int
	lowComplexity  bool
	lowRate        bool
	previousWeight float32
	deltaWeight    float32
}

// Fixed scratch sizes of pitchWithSearch (Rust allocates these as Vecs per call).
const (
	pitchEncStage1Len   = maxLtpBufLen + pitchDownsampDelay + 64 // small slack
	pitchEncStage1DsLen = (maxLtpBufLen+pitchDownsampDelay)/2 + 8
	pitchEncCECap       = int(2*fsKhz/stage1FsKhz)*numLagsStage1*numSubframes + 64
	pitchEncHLen        = numlagsCoarse*numSubframes*2 + 64
)

func pitchWithSearch(st *pitchEstState, ltpBuf []float32, f2 *[fLen]float32, codedAsActiveVoice bool, search pitchSearch) pitchResult {
	tab := loadPitchTables()
	numsubfrs := numSubframes
	l := maxLtpBufLen
	look := pitchLookaheadLen

	if !codedAsActiveVoice {
		minLag := float32(minpitchMs * fsKhz)
		st.prevLag = 0.0
		st.prevPitchCorr = 0.0
		st.prevLagblk = -1
		st.prevLagidx = -1
		var lags [numSubframes]float32
		for i := range lags {
			lags[i] = minLag
		}
		return pitchResult{
			pitchcorr:    0.0,
			lags:         lags,
			avgLag:       minLag,
			harmStrength: 0.0,
			blocksegIdx:  0,
		}
	}

	// HP filter into ltp_buf_stage1[offset..], where offset = PITCH_DOWNSAMP_DELAY leading zeros.
	offset := pitchDownsampDelay
	var stage1 [pitchEncStage1Len]float32
	pitchHpFilter(ltpBuf, stage1[offset:offset+l])
	// ltp_buf_hp = stage1[offset .. offset + (L - look)]
	hpLen := l - look
	ltpBufHp := stage1[offset : offset+hpLen]

	// Downsample stage1[0 .. L+offset] -> stage1_ds. We keep the HP signal already copied out, so a
	// separate output buffer is equivalent to the reference in-place write.
	var stage1Ds [pitchEncStage1DsLen]float32
	stage1Len := pitchDownsample(stage1[:], l+offset, stage1Ds[:])

	numlags0 := numLagsStage1
	sc := &st.scratch
	var e1 []float32
	e1, sc.e1Ext = calcE1Into(
		sc.e1,
		sc.e1Ext,
		stage1Ds[:],
		stage1Len,
		numsubfrs,
		minpitchStage1,
		maxpitchStage1,
		int(lagSubfrlenStage1),
	)
	sc.e1 = e1
	var e2 [numSubframes]float32
	// C / E arrays are over-allocated: the upsample stages expand them in place to full-res widths.
	var c [pitchEncCECap]float32
	var e [pitchEncCECap]float32
	// Stage-1 C is written straight into the head of `c`, which is exactly where the upsample
	// stages expand it from: a separate stage-1 buffer would be one more zeroed allocation and one
	// more full copy per call, for the same bytes.
	calcCE2(
		c[:numlags0*numsubfrs],
		e2[:],
		stage1Ds[:],
		stage1Len,
		numsubfrs,
	)

	// E from sqrt-energy blend (stage 1).
	numlags := numlags0
	for sf := 0; sf < numsubfrs; sf++ {
		sqrtE2 := sqrtF32(e2[sf] + 1e-30)
		for i := 0; i < numlags; i++ {
			sqrtE1 := sqrtF32(e1[sf*numlags+i] + 1e-30)
			tmp := 0.5 * (sqrtE1 + sqrtE2)
			e[sf*numlags+i] = tmp * tmp
		}
	}

	// Upsample to coarse (16 kHz) resolution.
	minpitchC := minpitchStage1
	numlagsC := numlags
	minpitchE := minpitchStage1
	numlagsE := numlags
	if search.lowComplexity {
		upsampEFast(c[:], numsubfrs, &minpitchC, &numlagsC)
	} else {
		upsampCFast(c[:], numsubfrs, &minpitchC, &numlagsC)
	}
	upsampEFast(e[:], numsubfrs, &minpitchE, &numlagsE)

	minpitchCoarse := coarseFsKhz * minpitchMs
	offsetC0 := int(minpitchCoarse - minpitchC)
	offsetE0 := int(minpitchCoarse - minpitchE)

	// H (coarse). The coarse snapshots of C, E and H that used to be built alongside it were never
	// read again -- the search below works off `h` and the full-resolution C/E -- so they are not
	// materialized: three zeroed allocations and three full copies per call, for nothing.
	var h [pitchEncHLen]float32
	for sf := 0; sf < numsubfrs; sf++ {
		for i := 0; i < numlagsCoarse; i++ {
			cv := c[sf*numlagsC+offsetC0+i]
			ev := e[sf*numlagsE+offsetE0+i]
			h[sf*numlagsCoarse+i] = cv / ev
		}
	}

	// Per-block coarse maxima -> Hblk.
	pitchblockCoarse := int(pitchblockMs * coarseFsKhz) // 32
	var hblk [numSubframes][pitchNumBlocks]float32
	for sf := 0; sf < numsubfrs; sf++ {
		for block := 0; block < pitchNumBlocks; block++ {
			base := sf*numlagsCoarse + block*pitchblockCoarse
			hblk[sf][block] = pitchEncSmplMaximum(h[base : base+pitchblockCoarse])
		}
	}

	// Block-track survivor selection.
	blocksizeFs := pitchblock * 2 // BLOCKSIZE = 64
	reductionFactor := float32(0.7)
	pitchDeltawght := search.deltaWeight / float32(blocksizeFs)
	var sfWght [numSubframes]float32
	{
		sumE2 := sumF32(e2[:numsubfrs])
		for sf := 0; sf < numsubfrs; sf++ {
			sfWght[sf] = e2[sf] / sumE2
		}
	}
	numBlocktracks := len(tab.blocktracks)
	if cap(sc.utils) < numBlocktracks {
		sc.utils = make([]float32, numBlocktracks)
	}
	// Every element is written below before it is read.
	utils := sc.utils[:numBlocktracks]
	for i := 0; i < numBlocktracks; i++ {
		bt := &tab.blocktracks[i]
		corr := float32(0.0)
		for sf := 0; sf < numsubfrs; sf++ {
			corr += float32(hblk[sf][bt.track[sf]] * sfWght[sf])
		}
		shortlagbias1 := float32((float32(maxpitchLen)/((bt.meanblock+1.5)*float32(pitchblock)) - 1.0) *
			pitchShortwght1)
		utils[i] = 1.0/(1.1-corr) -
			float32(reductionFactor*float32(pitchblock)*pitchDeltawght*bt.trackdeltas) +
			shortlagbias1
	}
	var trackIdx []int
	trackIdx, sc.taken = pitchEncGetMaxiKInto(sc.trackIdx, sc.taken, utils, search.survivors)
	sc.trackIdx = trackIdx

	// Recompute full-res E1 over the HP signal.
	var e1Fs []float32
	e1Fs, sc.e1FsExt = calcE1Into(
		sc.e1Fs,
		sc.e1FsExt,
		ltpBufHp,
		l-look,
		numsubfrs,
		minpitchE,
		minpitchE+int32(numlagsE)-1,
		int(lagSubfrlen),
	)
	sc.e1Fs = e1Fs

	// uniqueblocks bitmask per subframe from the survivor tracks.
	var uniqueblocks [numSubframes]uint16
	for _, ti := range trackIdx {
		track := &tab.blocktracks[ti].track
		for sf := 0; sf < numsubfrs; sf++ {
			uniqueblocks[sf] |= uint16(1) << track[sf]
		}
	}

	var hThres float32
	if search.lowComplexity {
		hThres = 0.0
	} else {
		hThres = 0.25
	}
	offsetC := int(minpitchMs*fsKhz - minpitchC)
	offsetE := int(minpitchMs*fsKhz - minpitchE)
	// Update C and E around survivor block peaks at full resolution.
	for sf := 0; sf < numsubfrs; sf++ {
		mask := uint16(1)
		cPtr := offsetC + sf*numlagsC
		ePtr := offsetE + sf*numlagsE
		e1Ptr := offsetE + sf*numlagsE
		hPtr := sf * numlagsFs
		// ltp_buf_ptr = &ltp_buf_hp[L - look + (sf - numsubfrs)*LAG_SUBFRLEN]
		ltpOff := int(int32(l-look) + (int32(sf)-int32(numsubfrs))*lagSubfrlen)
		e2Sf := maxF32(dotProd40(ltpBufHp[ltpOff:], ltpBufHp[ltpOff:]), 1e-9)
		e2[sf] = e2Sf
		sqrtE2 := sqrtF32(e2Sf + 1e-30)
		for block := 0; block < pitchNumBlocks; block++ {
			if uniqueblocks[sf]&mask != 0 {
				var sqrtE1 [pitchblock + 1]float32
				for i := 0; i < pitchblock+1; i++ {
					sqrtE1[i] = sqrtF32(e1Fs[e1Ptr+block*pitchblock+i] + 1e-30)
				}
				for i := 0; i < pitchblock+1; i++ {
					tmp := 0.5 * (sqrtE1[i] + sqrtE2)
					e[ePtr+block*pitchblock+i] = 0.5 * tmp * tmp
				}
				for i := 0; i < pitchblock; i++ {
					if h[hPtr+block*pitchblock+i] > hThres {
						lag := int(minpitchLen) + block*pitchblock + i
						a := ltpBufHp[ltpOff:]
						b := ltpBufHp[ltpOff-lag:]
						c[cPtr+block*pitchblock+i] = 0.5 * dotProd40(a, b)
					}
				}
			}
			mask <<= 1
		}
	}

	// Upsample C and E around survivor peaks to half-sample resolution and compute H (high to low).
	strideC := pitchNumBlocks*2*pitchblock + offsetC // per-subframe frac stride
	strideE := pitchNumBlocks*2*pitchblock + offsetE
	for sf := numsubfrs - 1; sf >= 0; sf-- {
		cPtr := offsetC + sf*numlagsC
		cPtrFrac := offsetC + sf*strideC
		ePtr := offsetE + sf*numlagsE
		ePtrFrac := offsetE + sf*strideE
		hPtr := sf * 2 * pitchblock * pitchNumBlocks
		mask := uint16(1) << (pitchNumBlocks - 1)
		for block := pitchNumBlocks - 1; block >= 0; block-- {
			if uniqueblocks[sf]&mask != 0 {
				ein := ePtr + block*pitchblock
				eout := ePtrFrac + block*2*pitchblock
				upsampECore(
					e[:],
					ein+pitchblock-1,
					eout+2*pitchblock-1,
					pitchblock,
				)
				cin := cPtr + block*pitchblock
				cout := cPtrFrac + block*2*pitchblock
				if search.lowComplexity {
					upsampECore(
						c[:],
						cin+pitchblock-1,
						cout+2*pitchblock-1,
						pitchblock,
					)
				} else {
					upsampCCore(
						c[:],
						cin+pitchblock-1,
						cout+2*pitchblock-1,
						pitchblock,
					)
				}
				for i := 0; i < 2*pitchblock; i++ {
					h[hPtr+block*2*pitchblock+i] = c[cout+i] / e[eout+i]
				}
			}
			mask >>= 1
		}
	}

	// Fine search: per survivor, per blockseg, per block: combine H over the seg's subframes, argmax.
	// Rust sizes these Vecs with `len(trackIdx) * 2` as a capacity hint; the reused buffers keep
	// whatever capacity earlier calls grew them to.
	lagindsSurv := sc.lagindsSurv[:0]
	blocksegsIxList := sc.blocksegsIxList[:0]
	var hComb [2 * pitchblock]float32
	// The memo key is (start subframe, segment length, block), each of them bounded by the table
	// format: a dense array indexed by that triple replaces a `HashMap` whose hashing and rehashing
	// cost more than the argmax it memoizes -- and whose `RandomState` made the cost vary per
	// process.
	var lagindCache [lagindCacheLen]int32
	for i := range lagindCache {
		lagindCache[i] = -1
	}
	for _, idx := range trackIdx {
		rng := tab.blocksegsIx[idx]
		for j := 0; j < rng[1]; j++ {
			bsx := rng[0] + j
			pblockseg := &tab.blocksegs[bsx]
			var lagindsRow [numSubframes]int32
			startSf := 0
			for n := 0; n < pblockseg.nblocks; n++ {
				slot, slotOK := lagindCacheSlot(startSf, pblockseg.seglens[n], pblockseg.blocks[n])
				var bestI int32
				if slotOK && lagindCache[slot] >= 0 {
					bestI = lagindCache[slot]
				} else {
					for i := range hComb {
						hComb[i] = 0.0
					}
					for sf := startSf; sf < startSf+pblockseg.seglens[n]; sf++ {
						hPtr := sf*2*pitchblock*pitchNumBlocks + pblockseg.blocks[n]*2*pitchblock
						for i := 0; i < 2*pitchblock; i++ {
							hComb[i] += float32(h[hPtr+i] * e2[sf])
						}
					}
					bi := int32(getMaxi(hComb[:]))
					if slotOK {
						lagindCache[slot] = bi
					}
					bestI = bi
				}
				for sf := startSf; sf < startSf+pblockseg.seglens[n]; sf++ {
					lagindsRow[sf] = bestI + int32(pblockseg.blocks[n]*2*pitchblock)
				}
				startSf += pblockseg.seglens[n]
			}
			lagindsSurv = append(lagindsSurv, lagindsRow)
			blocksegsIxList = append(blocksegsIxList, bsx)
		}
	}
	sc.lagindsSurv, sc.blocksegsIxList = lagindsSurv, blocksegsIxList
	nlaginds := len(lagindsSurv)

	// Final search.
	var pitchRatewght float32
	if search.lowRate {
		pitchRatewght = 0.028
	} else {
		pitchRatewght = ratewghtHr
	}
	f2w := buildF2w(f2)
	maxIx := getMaxi(sfWght[:numsubfrs])
	var spectralHarmCache [50]float32

	bestUtil := float32(0.0)
	bestPitchcorr := float32(0.0)
	bestSurv := 0
	pitchDeltawghtFs := search.deltaWeight / float32(blocksizeFs)

	for surv := 0; surv < nlaginds; surv++ {
		sumC := float32(0.0)
		sumE := float32(0.0)
		for sf := 0; sf < numsubfrs; sf++ {
			cBase := offsetC + sf*strideC
			eBase := offsetE + sf*strideE
			li := int(lagindsSurv[surv][sf])
			sumC += c[cBase+li]
			sumE += e[eBase+li]
		}
		rateBias := float32(encodeLagsBits(
			tab,
			blocksegsIxList[surv],
			lagindsSurv[surv][:],
			st.prevLagblk,
			st.prevLagidx,
			1,
		) * pitchRatewght)
		meanLag := float32(float32(lagindsSurv[surv][maxIx])*0.5) + float32(minpitchLen)
		pitchcorr := sumC / sumE
		firstLag := float32(0.5*float32(lagindsSurv[surv][0])) + float32(minpitchLen)
		prevLagBias := getPrevLagBias(st, firstLag, search.previousWeight)
		spectralHarmBias := float32(specHarmBias *
			spectralHarmonicityCached(meanLag, &f2w, spectralHarmCache[:], surv == 0))
		util := 1.0/(1.1-pitchcorr) -
			float32(pitchDeltawghtFs*float32(sumdeltas(lagindsSurv[surv][:], numsubfrs))) +
			spectralHarmBias +
			prevLagBias -
			rateBias
		if surv == 0 || util > bestUtil {
			bestUtil = util
			bestSurv = surv
		}
		if surv == 0 || pitchcorr > bestPitchcorr {
			bestPitchcorr = pitchcorr
		}
	}

	var lags [numSubframes]float32
	var lagindsOut [numSubframes]int32
	for sf := 0; sf < numsubfrs; sf++ {
		lags[sf] = float32(float32(lagindsSurv[bestSurv][sf])*0.5) + float32(minpitchLen)
		lagindsOut[sf] = lagindsSurv[bestSurv][sf]
	}
	avgLag := float32(float32(lagindsSurv[bestSurv][maxIx])*0.5) + float32(minpitchLen)
	// With SMPL_PITCH_SPEC_HARM_BIAS defined, the final harmonicity reuses the survivor-loop cache.
	harmStrength := spectralHarmonicityCached(avgLag, &f2w, spectralHarmCache[:], false)

	st.prevLag = lags[numsubfrs-1]
	st.prevPitchCorr = bestPitchcorr
	st.prevLagidx = lagindsSurv[bestSurv][numsubfrs-1]
	st.prevLagblk = st.prevLagidx / (2 * int32(pitchblock))

	return pitchResult{
		pitchcorr:    bestPitchcorr,
		lags:         lags,
		laginds:      lagindsOut,
		avgLag:       avgLag,
		harmStrength: harmStrength,
		blocksegIdx:  blocksegsIxList[bestSurv],
	}
}

func pitchEncSmplMaximum(x []float32) float32 {
	m := x[0]
	for _, v := range x[1:] {
		if v > m {
			m = v
		}
	}
	return m
}

func getPrevLagBias(st *pitchEstState, lag, previousWeight float32) float32 {
	lagDiff := absF32(lag - st.prevLag)
	diffThres := prevwghtSpan * st.prevLag
	if lagDiff < diffThres {
		return float32(st.prevPitchCorr * (1.0 - lagDiff/diffThres) * previousWeight)
	}
	return 0.0
}
