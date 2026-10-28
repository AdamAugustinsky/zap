// Ported from whatsapp-rust 5e72baf wacore/src/voip/mlow/params.rs, MIT.
//
// Encoder-facing per-frame parameters: the structured output of the analysis, consumed by the
// entropy encoder. The pulse/pitch blocks also carry the raw entropy symbols the encoder replays
// (the structured counts alone are lossy w.r.t. the exact bitstream).
//
// Every derived Default here is the all-zero value, which is Go's zero value; the defaultX
// constructors exist so callers can write the mechanical name.

package mlow

type smplLsfParams struct {
	stage1 int32
	grid   int32
	stage2 [16]int32
	extra  int32
}

func defaultSmplLsfParams() smplLsfParams { return smplLsfParams{} }

// smplRawSym is one uniform raw-symbol write (`encode(sym, sym+1, 1<<nbits)`).
type smplRawSym struct {
	sym   uint32
	nbits uint32
}

type smplPulseParams struct {
	total int32
	subfr [4]int32
	// Per-position run-length symbols (decodeCDF results) in read order across all subframes.
	magRuns []int32
	// Per-batch raw sign symbols in order.
	signSyms []smplRawSym
}

func defaultSmplPulseParams() smplPulseParams { return smplPulseParams{} }

type smplGainParams struct {
	gainMain  int32
	gainDelta int32
	nrgRes    [4]int32
}

func defaultSmplGainParams() smplGainParams { return smplGainParams{} }

// smplPitchParams is populated for the voiced path, not read on the unvoiced encode.
type smplPitchParams struct {
	gainIdx [4]int32
	filtIdx [4]int32
	// The estimator's chosen contour (`blockseg_idx`) and per-40-block lag indices (`laginds`, 8
	// entries). These ARE the wire pitch encoding: `smpl_encode_lags` writes the blockseg selector +
	// the per-block (uniform-first / delta) lag indices straight from them, so the decoder rebuilds
	// the full per-block contour instead of a flattened single lag.
	blocksegIdx int
	laginds     [8]int32
}

func defaultSmplPitchParams() smplPitchParams { return smplPitchParams{} }

type smplInternalParams struct {
	lsf    smplLsfParams
	pulses smplPulseParams
	pitch  smplPitchParams
	gains  smplGainParams
}

func defaultSmplInternalParams() smplInternalParams { return smplInternalParams{} }

// smplFrameParams is the full decoded/analyzed parameter set for one 60 ms MLow frame.
type smplFrameParams struct {
	toc    uint8
	config int
	// Whether the packet is coded as active voice (`mlowToc.active`). Two LSF symbols are on the
	// wire only then, so the writer has to agree with the reader about it.
	codedAsActiveVoice bool
	internal           [3]smplInternalParams
}
