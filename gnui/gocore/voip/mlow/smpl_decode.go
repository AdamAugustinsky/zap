// Ported from whatsapp-rust 5e72baf wacore/src/voip/mlow/smpl_decode.rs, MIT.
//
// MLow per-frame LSF parameter decode ("smpl_core_decode_indices", LSF block) plus the runtime CDF
// tables it reads.
//
// The smpl LSF coding is Meta-specific (NOT stock SILK CB1): a 2-way stage-1 codebook *selector*,
// a stage-1 *grid* index, then 16 stage-2 residuals from `g_lsf[stage1][config][grid][coeff]`. The
// stage-2/gain CDFs are RUNTIME-built (not static rodata) and were captured into `smpl_tables.json`.
// The entropy primitive is `decodeCDF` (u16 cumulative CDFs), not the ICDF path.

package mlow

type lsfGrid struct {
	match1    []uint16
	match1Alt []uint16
	match0    []uint16
	match0Alt []uint16
}

// smplTables holds the runtime CDF tables (the LSF block of func 3559's output), expanded from the
// LSF seed ROM.
type smplTables struct {
	lsfSel  [][]uint16
	lsfGrid lsfGrid
	// `[stage1][config][grid][coeff]` -> cumulative CDF.
	lsfStage2 [][][][][]uint16
	lsfExtra  []uint16
	// The gain CDFs the decoder uses come from the heap window (smpl_mem g_nrg), not these table
	// fields, so the JSON's gain_main/gain_delta are intentionally not deserialized here.
}

func loadSmplTables() *smplTables {
	return &lsfBuilt().tables
}

// smplLsfState is the cross-internal-frame decoder state. The LSF block RESETS the pitch/LTP
// predictor fields to -1 whenever the stage-1 selector does not match the previous internal frame.
type smplLsfState struct {
	prevStage1  int32
	prevMatch   bool
	havePrev    bool
	prevGainIdx int32
	prevFiltIdx int32
	prevLag     int32
	prevFracLag int32
	// Encoder-only: previous internal frame's chosen pitch lag (samples) for the pitch-search
	// continuity bias. Unused by the decoder.
	prevLagSamples float32
	// Encoder-only: the `ParamsEncoder` lag predictor (`prev_lagblk`/`prev_lagidx`) that
	// `smpl_encode_lags` threads to pick the abs-vs-block-transition first lag and the per-block
	// delta-lag CMFs. Reset to -1 on a no-match (`cond_coding == FALSE`). Unused by the decoder
	// (it rebuilds lags from the wire via its own contour predictor).
	prevLagblk int32
	prevLagidx int32
}

// defaultSmplLsfState is the derived Default: every field zero.
func defaultSmplLsfState() smplLsfState { return smplLsfState{} }

// smplAdvanceLsfState advances the LSF predictor mirror exactly as `encode_smpl_lsf`/`decode_smpl_lsf`
// does for an internal frame with the given stage-1 selector: on a no-match (intf 0, or stage1
// differs) reset the pitch/LTP predictor to -1, then record prev_stage1/prev_match. The encoder
// analysis runs this so its `prev_lag` tracks what the entropy encoder will compute (driving the
// abs-vs-delta lag pick).
func smplAdvanceLsfState(st *smplLsfState, intf int, stage1 int32) {
	m := intf != 0 && stage1 == st.prevStage1
	if !m {
		st.prevGainIdx = -1
		st.prevFiltIdx = -1
		st.prevLag = -1
		st.prevFracLag = -1
		st.prevLagblk = -1
		st.prevLagidx = -1
	}
	st.prevStage1 = stage1
	st.prevMatch = m
	st.havePrev = true
}

// smplLsfIndices is the decoded per-internal-frame LSF index set.
type smplLsfIndices struct {
	stage1 int32
	grid   int32
	stage2 [16]int32
	extra  int32
}

// decodeSmplLsf decodes the LSF block of one internal frame (the first block of the core decode).
// `config` is the smpl config (0/1), `intf` the internal-frame index (0,1,2) within the 60 ms
// packet. Mutates `st`.
func decodeSmplLsf(
	dec *rangeDecoder,
	t *smplTables,
	st *smplLsfState,
	config int,
	intf int,
	codedAsActiveVoice bool,
) smplLsfIndices {
	idx := smplLsfIndices{}

	// Read 1: the voicing symbol, whose value doubles as the stage-1 selector. The first internal
	// frame uses the dedicated row 0; later frames pick row 1/2 by the previous frame's result.
	//
	// It is only on the wire when the frame was coded as active voice. A frame coded inactive
	// (DTX off, so the encoder still sends background noise) carries no voicing symbol at all and
	// is unvoiced by definition; reading one would consume a symbol that was never written and
	// desync every field after it.
	var sel int
	if intf == 0 {
		sel = 0
	} else if st.prevStage1 != 0 {
		sel = 2
	} else {
		sel = 1
	}
	var stage1 int32
	if codedAsActiveVoice {
		stage1 = dec.decodeCdf(t.lsfSel[sel])
	}
	idx.stage1 = stage1

	// match := enter_match && stage1 == prev_stage1. enter_match is false for the first internal
	// frame and true afterwards (reset to 1 after each synthesis). On no-match the pitch/LTP
	// predictor resets to -1.
	enterMatch := intf != 0
	m := enterMatch && (stage1 == st.prevStage1)
	if !m {
		st.prevGainIdx = -1
		st.prevFiltIdx = -1
		st.prevLag = -1
		st.prevFracLag = -1
	}
	st.prevStage1 = stage1

	// Read 2: stage-1 grid. Inner select keys on the CURRENT stage1, outer select on match.
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
	grid := dec.decodeCdf(gridCdf)
	idx.grid = grid
	st.prevMatch = m
	st.havePrev = true

	// Read 3: 16 stage-2 residuals, each coeff k from its own CDF g_lsf[stage1][config][grid][k].
	st2 := t.lsfStage2[int(stage1)][config][int(grid)]
	for k, c := range st2 {
		if k >= 16 {
			break
		}
		idx.stage2[k] = dec.decodeCdf(c)
	}

	// The LSF interpolation index, on the wire only for an active-voice frame with more than one
	// subframe (the reference gates it on the same flag as the voicing symbol).
	if codedAsActiveVoice {
		idx.extra = dec.decodeCdf(t.lsfExtra)
	} else {
		idx.extra = 0
	}

	// (Rust's log::trace of the decoded indices is omitted.)
	return idx
}
