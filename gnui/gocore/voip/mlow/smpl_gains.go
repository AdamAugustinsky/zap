// Ported from whatsapp-rust 5e72baf wacore/src/voip/mlow/smpl_gains.rs, MIT.
//
// MLow gains + nrgres decode (func 3545 GAINS block), run for UNVOICED (stage-1 selector == 0)
// internal frames; mutually exclusive with the pitch block. The config=0 active capture is all
// voiced, so this never runs on real audio (the synth uses gainQ=0 for voiced frames); the test
// below validates the arithmetic byte-exact by force-running it on a voiced frame's post-pulse
// decoder state.

package mlow

type smplGainResult struct {
	// Per-subframe quantized log-gain (Q-domain).
	gainQ [4]int32
	// Per-subframe energy-residual symbol (only subframes with pulses are read).
	nrgRes [4]int32
}

// gainsAsUsizeMin is Rust's `(x as usize).min(m)` for an i32 x: a negative x wraps to a huge usize,
// so the result is m.
func gainsAsUsizeMin(x int32, m int) int {
	if x < 0 || int(x) > m {
		return m
	}
	return int(x)
}

// decodeSmplGains decodes the gains+nrgres reads (the p3==4 path). `subfrCounts` are the
// per-subframe pulse counts.
func decodeSmplGains(
	dec *rangeDecoder,
	cc *ccTables,
	p3 int32,
	subfrCounts [4]int32,
) smplGainResult {
	res := smplGainResult{}

	// main gain (n=85) + delta gain (n=99)
	gainMain := dec.decodeCdf(cc.nrgresGain4())
	gainDelta := dec.decodeCdf(cc.nrgresShape4())
	cfgSel := int32(2)

	// gain reconstruction. The index sf + p3*gain_delta is NOT bounded to the visible 64-entry array
	// (gain_delta up to 98); the WASM reads adjacent rodata, which the seed carries verbatim.
	off6 := p3 * gainDelta
	base7 := gainMain*cc.nrgStep(cfgSel) - 0x154000
	for sf := 0; sf < gainsAsUsizeMin(p3, 4); sf++ {
		cbv := cc.gainRecon(p3 == 4, int32(sf)+off6)
		res.gainQ[sf] = base7 + (cbv << 4)
	}

	// nrgres: per-subframe bucketed CDF (n=92) with a gain-derived slice shift.
	for sf := 0; sf < gainsAsUsizeMin(p3, 4); sf++ {
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
		// g = clamp((gainQ[sf]+8192)>>14, floor -85); min_offset = -neg_part (forward entry shift).
		g := (res.gainQ[sf] + 8192) >> 14
		if g < -85 {
			g = -85
		}
		negPart := (g >> 31) & g
		minOffset := int(-negPart)
		res.nrgRes[sf] = dec.decodeCdf(cc.fcbgOffset(int(cfgSel), int(bucket), minOffset))
	}
	// (Rust's log::trace of the decoded gains is omitted.)
	return res
}
