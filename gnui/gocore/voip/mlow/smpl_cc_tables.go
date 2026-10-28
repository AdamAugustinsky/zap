// Ported from whatsapp-rust 5e72baf wacore/src/voip/mlow/smpl_cc_tables.rs, MIT.
//
// Logical seed-built tables for the nrgres/gains (Group A/E) and LTP gain (Group C) decode, built
// from a small DCMF seed instead of read by absolute pointer off the cc_blob heap window. The CDFs
// are the integer `dcmf_to_cmf` expansion; the gain-reconstruction rodata is carried verbatim. The
// pulse (Group B) and pitch lag/contour (Group D) reads still use the heap window (`smplMem`).
//
// Go deviation: a Go type cannot have a field and a method of the same name, so the private
// fields whose Rust accessor shares their name carry a `Tab` suffix (nrgresGain4Tab,
// nrgresShape4Tab, fcbgOffsetTab, gainReconTab, runlenTab) and runlenCmfs.max_samples is
// maxSamplesN. The accessors keep the mechanical names.

package mlow

import (
	_ "embed"
	"math"
	"math/bits"
	"sync"
)

// SILK fixed-point primitives (zero-ULP integer lin2log/log2lin/sigm_Q15).

// ccTablesSmulbb is silk_SMULBB: (int16)a * (int16)b (32-bit wrapping).
func ccTablesSmulbb(a, b int32) int32 {
	return int32(int16(a)) * int32(int16(b))
}

// ccTablesSmlawb is silk_SMLAWB: a + ((b * (int16)c) >> 16), 64-bit intermediate.
func ccTablesSmlawb(a, b, c int32) int32 {
	return int32(int64(a) + ((int64(b) * int64(int16(c))) >> 16))
}

// ccTablesClzFrac is silk_CLZ_FRAC: leading-zero count + Q7 fractional mantissa.
func ccTablesClzFrac(inp int32) (int32, int32) {
	lz := int32(bits.LeadingZeros32(uint32(inp)))
	fracQ7 := int32(bits.RotateLeft32(uint32(inp), -int((24-lz)&31))) & 0x7f
	return lz, fracQ7
}

// ccTablesLin2log is silk_lin2log: approximation of 128 * log2().
func ccTablesLin2log(inLin int32) int32 {
	lz, fracQ7 := ccTablesClzFrac(inLin)
	return ccTablesSmlawb(fracQ7, fracQ7*(128-fracQ7), 179) + ((31 - lz) << 7)
}

// log2lin is silk_log2lin: approximation of 2^() (inverse of lin2log).
func log2lin(inLogQ7 int32) int32 {
	if inLogQ7 < 0 {
		return 0
	}
	if inLogQ7 >= 3967 {
		return math.MaxInt32
	}
	out := int32(1) << (inLogQ7 >> 7)
	fracQ7 := inLogQ7 & 0x7f
	inner := ccTablesSmlawb(fracQ7, ccTablesSmulbb(fracQ7, 128-fracQ7), -174)
	if inLogQ7 < 2048 {
		out = out + ((out * inner) >> 7)
	} else {
		out = out + ((out >> 7) * inner)
	}
	return out
}

// ccTablesSigmQ15 is silk_sigm_Q15: piecewise-linear sigmoid.
func ccTablesSigmQ15(inQ5 int32) int32 {
	slope := [6]int32{237, 153, 73, 30, 12, 7}
	pos := [6]int32{16384, 23955, 28861, 31213, 32178, 32548}
	neg := [6]int32{16384, 8812, 3906, 1554, 589, 219}
	if inQ5 < 0 {
		inQ5 := -inQ5
		if inQ5 >= 6*32 {
			return 0
		}
		ind := int(inQ5 >> 5)
		return neg[ind] - ccTablesSmulbb(slope[ind], inQ5&0x1f)
	} else if inQ5 >= 6*32 {
		return 32767
	}
	ind := int(inQ5 >> 5)
	return pos[ind] + ccTablesSmulbb(slope[ind], inQ5&0x1f)
}

// Pulse-coding table builders, all integer/deterministic.

// pdfToCmf is `smpl_pdf_to_CMF` (maxval == -1 path): truncating-int normalize into a cumulative
// u16 CDF of length `len(pdf) + 1`. The PDF entries here are always non-negative.
func pdfToCmf(pdf []int32) []uint16 {
	n := int64(len(pdf))
	var maxval int64 = 32767
	var sump int64
	for _, x := range pdf {
		sump += int64(x)
	}
	cmf := make([]uint16, len(pdf)+1)
	for i := range pdf {
		p := ((int64(pdf[i]) * (maxval - n)) / sump) + 1
		cmf[i+1] = uint16(int32(cmf[i]) + int32(p))
	}
	return cmf
}

const (
	log2Exp1Q15 int32 = 47274
	log22piQ14  int32 = 43442
)

// stirling is the Stirling-approximation log-factorial term used by the split-probability model.
func stirling(n int32) int32 {
	if n == 0 {
		return 0
	}
	ret := ((n << 1) + 1) * (ccTablesLin2log(n) << 7)
	ret = ret - log2Exp1Q15*n
	ret = ret + log22piQ14
	return ret + log2Exp1Q15/(12*n)
}

// probSplitFast is `smpl_prob_split_fast`: 2^(stirling(N) - stirling(k) - stirling(N-k) - N), as
// a Q-scaled int.
func probSplitFast(k, n int32) int32 {
	tmp := stirling(n) - stirling(k) - stirling(n-k) - n*(1<<15)
	if tmp == 0 {
		return 1 << 30
	}
	ret := log2lin((-tmp) >> 8)
	return (1 << 30) / ret
}

// createSplitCmfs is `create_split_CMFs`: split_CMFs[num_pulses-1] for num_pulses in
// 1..SMPL_MAX_PULSES_PER_SF*4.
func createSplitCmfs() [][]uint16 {
	out := make([][]uint16, 0, splitNumTables)
	for numPulses := int32(1); numPulses <= int32(splitNumTables); numPulses++ {
		minSplit := max(numPulses-int32(smplMaxPulsesPerSf)*2, 0)
		maxSplit := numPulses - minSplit
		p := make([]int32, 0, maxSplit-minSplit+1)
		for k := minSplit; k <= maxSplit; k++ {
			p = append(p, probSplitFast(k, numPulses))
		}
		out = append(out, pdfToCmf(p))
	}
	return out
}

const oneQ31 int64 = 1 << 31

// createRunlenTable is `create_runlen_table` for one octave bucket: the per-`nump` magnitude CDFs
// (full length `max_samples + 1`), Q31 sigmoid/log2lin geometric model.
func createRunlenTable(maxSamples int32) runlenCmfs {
	ms := maxSamples
	cmfs := make([][]uint16, 0, smplMaxPulsesPerSf)
	for nump := int32(1); nump <= int32(smplMaxPulsesPerSf); nump++ {
		plongerQ31 := oneQ31
		p := make([]int32, ms)
		for nums := int32(1); nums <= ms; nums++ {
			tmp := oneQ31 - (oneQ31 / int64(ms-nums+1))
			p1Q31 := tmp
			for range nump - 1 {
				p1Q31 = (p1Q31 * tmp) >> 31
			}
			p1Q31 = oneQ31 - p1Q31
			p1Q31 = min(p1Q31, 2147376274) // 0.99995
			var logOutQ7 int32
			if nump > ms {
				logOutQ7 = ccTablesLin2log((nump<<10)/ms) - 10*128
			} else {
				logOutQ7 = -(ccTablesLin2log((ms<<10)/nump) - 10*128)
			}
			const sigmBiasQ5 int32 = 146
			const scaleMaxQ15 int32 = 36000
			const scaleMinQ15 int32 = 26000
			scaleFacQ15 := scaleMaxQ15 -
				(((scaleMaxQ15 - scaleMinQ15) *
					ccTablesSigmQ15((logOutQ7>>2)+sigmBiasQ5)) >>
					15)
			p1Q31 = oneQ31 -
				int64(log2lin(
					((scaleFacQ15*(ccTablesLin2log(int32(oneQ31-p1Q31))-31*128))>>15)+
						31*128,
				))
			p1Q31 = min(p1Q31, 2147376274)
			p[nums-1] = int32((plongerQ31 * p1Q31) >> 31)
			plongerQ31 = (plongerQ31 * (oneQ31 - p1Q31)) >> 31
		}
		cmfs = append(cmfs, pdfToCmf(p))
	}
	return runlenCmfs{
		maxSamplesN: ms,
		cmfs:        cmfs,
	}
}

const (
	nrgresGain4N      = 84
	nrgresShape4N     = 98
	fcbgOffsetSteps   = 176 // dcmf len; cmf row len is +1
	fcbgOffsetBuckets = 4
	acbgN             = 16 // acbgain cmf row len is +1
	acbgRows          = acbgN + 1
	fcbgVN            = 34 // fcbgains_v cmf len is +1
	fcbgVDeltaN       = 67 // fcbgains_v_delta cmf len is +1
)

// Pulse-coding (Group B) shape constants. SMPL_MAX_PULSES_PER_SF (40) is shared.go's
// smplMaxPulsesPerSf.
const (
	runlengthStep  = 8
	numRunlenCmfs  = 20                       // SMPL_MAX_SF_LEN(160) / RUNLENGTH_STEP
	splitNumTables = smplMaxPulsesPerSf*4 - 1 // num_pulses 1..160
)

// ccTables holds the runtime tables. CDFs are `u16` (the integer cmf fits 16 bits) to feed
// `decodeCdf`/`encodeCdf` directly, byte-identical to the old heap u16 reads. The accessor takes
// the LOGICAL index. The on-disk packed seed is `ccSeed` (smpl_tables_blob.go); its `bytes`
// fields reshape row-major at build.
type ccTables struct {
	nrgresGain4Tab  []uint16   // [85]
	nrgresShape4Tab []uint16   // [99]
	fcbgOffsetTab   [][]uint16 // [3*4][177]
	acbgainsHr      []uint16   // [17*17] row-major rows of 17
	acbgainsLr      []uint16   // [17*17] LR variant (p6!=0), built from the LR DCMF const
	fcbgainsV       []uint16   // [35]
	fcbgainsVDelta  []uint16   // [68]
	acbgainsCbHrQ14 []int16    // [16*2]
	acbgainsCbLrQ14 []int16    // [16*2] LR weight table (p6!=0)
	gainReconTab    []int16    // rodata, addressed from gainReconBase
	gainReconBase   uint32
	// Group B (pulses): pulse-count CDFs [bgn,uv,v]; split CDFs by num_pulses-1; run-length CDFs by oct-1.
	nPulseCmfs [3][]uint16
	splitCmfs  [][]uint16   // [splitNumTables]; row i = split_CMFs[i] (num_pulses = i+1)
	runlenTab  []runlenCmfs // [numRunlenCmfs]; row i = oct i+1
}

// runlenCmfs holds the run-length CDFs for one octave bucket (`max_samples = oct *
// RUNLENGTH_STEP`). `cmfs[c-1]` is the full cumulative CDF for `c` pulses-left, length
// `max_samples + 1`.
type runlenCmfs struct {
	maxSamplesN int32
	cmfs        [][]uint16 // [SMPL_MAX_PULSES_PER_SF]
}

// ccTablesDebugAssertEq mirrors a Rust debug_assert_eq! on a table length.
func ccTablesDebugAssertEq(got, want int, what string) {
	if got != want {
		panic("smpl_cc_tables: bad " + what + " length")
	}
}

// ccTablesFlatChunks is `.chunks_exact(n).flat_map(dcmf_to_cmf).collect()`.
func ccTablesFlatChunks(dcmf []uint8, n int) []uint16 {
	var out []uint16
	for i := 0; i+n <= len(dcmf); i += n {
		out = append(out, celpDcmfToCmf(dcmf[i:i+n])...)
	}
	return out
}

func (s *ccSeed) build() ccTables {
	nrgresGain4 := celpDcmfToCmf(s.nrgresGain4Dcmf)
	nrgresShape4 := celpDcmfToCmf(s.nrgresShape4Dcmf)
	var fcbgOffset [][]uint16
	for i := 0; i+fcbgOffsetSteps <= len(s.fcbgOffsetDcmf); i += fcbgOffsetSteps {
		fcbgOffset = append(fcbgOffset, celpDcmfToCmf(s.fcbgOffsetDcmf[i:i+fcbgOffsetSteps]))
	}
	acbgainsHr := ccTablesFlatChunks(s.acbgainsHrDcmf, acbgN)
	// LR (p6!=0) variant: same expansion from the LR DCMF const (no seed bytes).
	acbgainsLr := ccTablesFlatChunks(smplAcbgainsDcmfLr[:], acbgN)
	fcbgainsV := celpDcmfToCmf(s.fcbgainsVDcmf)
	fcbgainsVDelta := celpDcmfToCmf(s.fcbgainsVDeltaDcmf)
	acbgainsCbHrQ14 := make([]int16, len(s.acbgainsCbHrQ14))
	for i, x := range s.acbgainsCbHrQ14 {
		acbgainsCbHrQ14[i] = int16(x)
	}
	acbgainsCbLrQ14 := append([]int16(nil), smplCbAcbgainsLrQ14[:]...)
	gainRecon := make([]int16, 0, len(s.gainRecon)/2)
	for i := 0; i+2 <= len(s.gainRecon); i += 2 {
		gainRecon = append(gainRecon, int16(uint16(s.gainRecon[i])|uint16(s.gainRecon[i+1])<<8))
	}

	ccTablesDebugAssertEq(len(nrgresGain4), nrgresGain4N+1, "nrgres_gain4")
	ccTablesDebugAssertEq(len(nrgresShape4), nrgresShape4N+1, "nrgres_shape4")
	ccTablesDebugAssertEq(len(fcbgOffset), 3*fcbgOffsetBuckets, "fcbg_offset")
	ccTablesDebugAssertEq(len(acbgainsHr), acbgRows*(acbgN+1), "acbgains_hr")
	ccTablesDebugAssertEq(len(acbgainsLr), acbgRows*(acbgN+1), "acbgains_lr")
	ccTablesDebugAssertEq(len(fcbgainsV), fcbgVN+1, "fcbgains_v")
	ccTablesDebugAssertEq(len(fcbgainsVDelta), fcbgVDeltaN+1, "fcbgains_v_delta")

	// Group B: pulse-count from the DCMF seed, split/runlen computed from consts (no seed).
	nPulseCmfs := [3][]uint16{
		celpDcmfToCmf(s.nPulsesDcmfBgn),
		celpDcmfToCmf(s.nPulsesDcmfUv),
		celpDcmfToCmf(s.nPulsesDcmfV),
	}
	splitCmfs := createSplitCmfs()
	runlen := make([]runlenCmfs, 0, numRunlenCmfs)
	for oct := int32(1); oct <= numRunlenCmfs; oct++ {
		runlen = append(runlen, createRunlenTable(oct*runlengthStep))
	}

	ccTablesDebugAssertEq(len(splitCmfs), splitNumTables, "split_cmfs")
	ccTablesDebugAssertEq(len(runlen), numRunlenCmfs, "runlen")

	return ccTables{
		nrgresGain4Tab:  nrgresGain4,
		nrgresShape4Tab: nrgresShape4,
		fcbgOffsetTab:   fcbgOffset,
		acbgainsHr:      acbgainsHr,
		acbgainsLr:      acbgainsLr,
		fcbgainsV:       fcbgainsV,
		fcbgainsVDelta:  fcbgainsVDelta,
		acbgainsCbHrQ14: acbgainsCbHrQ14,
		acbgainsCbLrQ14: acbgainsCbLrQ14,
		gainReconTab:    gainRecon,
		gainReconBase:   s.gainReconBase,
		nPulseCmfs:      nPulseCmfs,
		splitCmfs:       splitCmfs,
		runlenTab:       runlen,
	}
}

//go:embed testdata/cc_seed.bin
var ccTablesCcSeedBin []byte

var ccTablesTables = sync.OnceValue(func() *ccTables {
	seed := loadBlobBuffa[ccSeed](ccTablesCcSeedBin)
	t := seed.build()
	return &t
})

func loadCcTables() *ccTables {
	return ccTablesTables()
}

// nrgresGain4 is the nrgres main-gain CDF (n=85).
func (c *ccTables) nrgresGain4() []uint16 {
	return c.nrgresGain4Tab
}

// nrgresShape4 is the nrgres delta-gain / shape CDF (n=99).
func (c *ccTables) nrgresShape4() []uint16 {
	return c.nrgresShape4Tab
}

// fcbgOffset is the fcbg-offset CDF row `[tableIx][bucket]` sliced at `minOffset` (n=92).
// `minOffset` is the non-negative entry shift the old `-2*neg_part` pointer offset encoded.
func (c *ccTables) fcbgOffset(tableIx, bucket, minOffset int) []uint16 {
	row := c.fcbgOffsetTab[tableIx*fcbgOffsetBuckets+bucket]
	return row[minOffset : minOffset+92]
}

// acbgainRow is the HR ACB-gain CDF for predictor `prev` (row `prev+1`), n=17.
func (c *ccTables) acbgainRow(prev int32) []uint16 {
	r := int(prev + 1)
	base := r * (acbgN + 1)
	return c.acbgainsHr[base : base+acbgN+1]
}

// acbgainRowLr is the LR ACB-gain CDF for predictor `prev` (the p6!=0 variant of `acbgainRow`),
// n=17.
func (c *ccTables) acbgainRowLr(prev int32) []uint16 {
	r := int(prev + 1)
	base := r * (acbgN + 1)
	return c.acbgainsLr[base : base+acbgN+1]
}

// acbgainWeightsLr is the LR ACB-gain weights (w0, w2) for gain index `gi` (the p6!=0 variant).
func (c *ccTables) acbgainWeightsLr(gi int32) (int32, int32) {
	i := int(gi) * 2
	return int32(c.acbgainsCbLrQ14[i]), int32(c.acbgainsCbLrQ14[i+1])
}

// fcbgainV is the FCB voiced-gain CDF (prev_filt == -1), n=35.
func (c *ccTables) fcbgainV() []uint16 {
	return c.fcbgainsV
}

// fcbgainVDelta is the FCB voiced-gain delta CDF sliced at `[FCBG_V_N-1 - prevFilt]` (n=35).
func (c *ccTables) fcbgainVDelta(prevFilt int32) []uint16 {
	start := int(int32(fcbgVN) - 1 - prevFilt)
	return c.fcbgainsVDelta[start : start+35]
}

// acbgainWeights is the HR ACB-gain weights (w0, w2) for gain index `gi`.
func (c *ccTables) acbgainWeights(gi int32) (int32, int32) {
	i := int(gi) * 2
	return int32(c.acbgainsCbHrQ14[i]), int32(c.acbgainsCbHrQ14[i+1])
}

// nrgStep is the nrg-step const for config `cfg` (gain-reconstruction base scale).
func (c *ccTables) nrgStep(cfg int32) int32 {
	return c.gainReconAt(c.gainReconBase + uint32(cfg)*2)
}

// gainRecon is the unvoiced gain-reconstruction codebook entry. `p4` selects the p3==4 table;
// `idx` is the (possibly out-of-the-visible-array) rodata index the WASM reads as adjacent
// constants.
func (c *ccTables) gainRecon(p4 bool, idx int32) int32 {
	var base uint32 = 0xf3970
	if p4 {
		base = 0xf35f0
	}
	return c.gainReconAt(base + uint32(idx)*2)
}

// gainReconAt reads an int16 from the gain-reconstruction rodata at WASM address `addr`.
// Out-of-region reads fall back to 0, matching the old heap window.
func (c *ccTables) gainReconAt(addr uint32) int32 {
	off := uint(addr - c.gainReconBase)
	if off/2 < uint(len(c.gainReconTab)) && off%2 == 0 {
		return int32(c.gainReconTab[off/2])
	}
	return 0
}

// nPulseCount is the pulse-count CDF for table `idx` (0 = background, 1 = unvoiced, 2 = voiced).
// The whole CDF is the decode interval (the old heap read used the stored `cmfLen`).
func (c *ccTables) nPulseCount(idx int32) []uint16 {
	return c.nPulseCmfs[idx]
}

// splitCmf is the split CDF base for `total` pulses (the heap `split_CMFs[total-1].cmf`). The
// caller windows it at its `min_split` offset; this returns the full row so out-of-range slices
// fall back to 0.
func (c *ccTables) splitCmf(total int32) []uint16 {
	i := uint(int(total) - 1)
	if i < uint(len(c.splitCmfs)) {
		return c.splitCmfs[i]
	}
	return nil
}

// runlen is the run-length CDF bucket for octave `oct` (`max_samples = oct * RUNLENGTH_STEP`).
func (c *ccTables) runlen(oct int32) *runlenCmfs {
	return &c.runlenTab[oct-1]
}

// maxSamples is `max_samples` for this bucket (the heap stored this as the per-row base value the
// call site uses to shift into `cmf`).
func (r *runlenCmfs) maxSamples() int32 {
	return r.maxSamplesN
}

// cmf is the full magnitude CDF for `c` pulses-left (the heap `cmfs[c-1]`, length
// `max_samples + 1`). The caller windows it by `(max_samples - pos)` and reads `pos + 1` entries.
func (r *runlenCmfs) cmf(c int32) []uint16 {
	return r.cmfs[c-1]
}
