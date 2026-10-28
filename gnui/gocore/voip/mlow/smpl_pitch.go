// Ported from whatsapp-rust 5e72baf wacore/src/voip/mlow/smpl_pitch.rs, MIT.
//
// MLow pitch/LTP decode, run when the LSF stage-1 selector is 1 (voiced), which is every active
// 0x50 capture frame. Two parts: LTP gains (per-subframe gain index + optional 35-sym filter index,
// served from the logical `CcTables`) and the lag block (primary lag absolute/delta, contour-map
// search, optional 64-sym fine read, per-segment fractional loop) which reads the contour window
// built from the pitch seed.

package mlow

// smplPitchResult holds the decoded LTP/pitch parameters for one internal frame.
type smplPitchResult struct {
	gainIdx [4]int32
	filtIdx [4]int32
	lag     int32
	contour int32
	// Per-segment reconstructed pitch lag in Q6 (1/64-sample).
	sampleLagQ6 [8]int32
	numSeg      int32
	// Per-subframe pitch lag in Q6 (the LTP lag table). Kept at 4 entries to match the
	// `pitch_match_go` vectors; the 8 per-40-block lags the ACB/LTP synthesis actually consumes live
	// in `block_lags`.
	intLagQ6 [4]int32
	// The per-40-sample-block `laginds` (8 per 20 ms frame), in the same index domain as
	// `int_lag_q6`. A single 80-sample subframe spans two blocks that can carry different fractional
	// lags; synthesis maps each as `lag = block_lags*0.5 + SMPL_MIN_PITCH_LAG`.
	blockLags [8]int32
	numSubfr  int32
}

// defaultSmplPitchResult is the derived Default: every field zero.
func defaultSmplPitchResult() smplPitchResult { return smplPitchResult{} }

// decodeCdfMem is `decode_cdf` on the n-entry CDF at heap address `addr`. Reads the region bytes in
// place when the window fits one region (the common case), falling back to the zero-filling
// `cdf_at` copy only at a region boundary, where the out-of-range entries must read 0.
func decodeCdfMem(dec *rangeDecoder, mem *smplMem, addr uint32, n int) int32 {
	if bytes, ok := mem.cdfBytes(addr, n); ok {
		return dec.decodeCdfLe16(bytes)
	}
	return dec.decodeCdf(mem.cdfAt(addr, n))
}

func decodeSmplPitch(
	dec *rangeDecoder,
	mem *smplMem,
	cc *ccTables,
	st *smplLsfState,
	_ int32, // _p2
	p3 int32,
	p6 int32,
	subfrCounts [4]int32,
) smplPitchResult {
	res := defaultSmplPitchResult()
	res.filtIdx = [4]int32{-1, -1, -1, -1}
	// LTP gains loop: both selects key on p6 (active WB path is p6==0 HR, the p6!=0 LR variant), both
	// served from the logical `CcTables`. The filter CDFs are shared across p6.
	var gainAccum int32
	for sf := 0; sf < gainsAsUsizeMin(p3, 4); sf++ {
		cnt := subfrCounts[sf]
		var gi int32
		if p6 != 0 {
			gi = dec.decodeCdf(cc.acbgainRowLr(st.prevGainIdx))
		} else {
			gi = dec.decodeCdf(cc.acbgainRow(st.prevGainIdx))
		}
		res.gainIdx[sf] = gi
		st.prevGainIdx = gi

		var w0, w2 int32
		if p6 != 0 {
			w0, w2 = cc.acbgainWeightsLr(gi)
		} else {
			w0, w2 = cc.acbgainWeights(gi)
		}
		gainAccum += w0 + 2*w2

		if cnt > 0 {
			var fi int32
			if st.prevFiltIdx == -1 {
				fi = dec.decodeCdf(cc.fcbgainV())
			} else {
				fi = dec.decodeCdf(cc.fcbgainVDelta(st.prevFiltIdx))
			}
			res.filtIdx[sf] = fi
			st.prevFiltIdx = fi
		}
	}
	avgGain := gainAccum / p3 // drives the fractional-lag segment select

	// --- Lag block ---
	pcfg := mem.gClk + 0x5704
	numContours := int32(mem.u32(pcfg + 22240))
	lagCdf := mem.u32(pcfg + 22248)
	contourMap := mem.u32(pcfg + 22244)
	fracBase := mem.u32(pcfg + 22252)
	deltaCdf := mem.u32(pcfg + 22268)

	// primary lag:
	var lag int32
	if st.prevLag < 0 {
		lag = decodeCdfMem(dec, mem, lagCdf, int(max(numContours+1, 0)))
	} else {
		di := decodeCdfMem(
			dec,
			mem,
			deltaCdf+uint32(st.prevLag)*20,
			10,
		)
		lo := int32(mem.u8(0xe7ef0 + uint32(di)*2))
		hi := int32(mem.u8(0xe7ef0 + uint32(di)*2 + 1))
		rN := (hi - lo) + 2
		if rN < 2 {
			res.lag = -1
			return res // malformed delta interval
		}
		sym := decodeCdfMem(
			dec,
			mem,
			lagCdf+uint32(lo)*2,
			int(rN),
		)
		lag = sym + lo
	}

	// contour-map search: find index where contour_map[i] == lag+1.
	target := lag + 1
	contour := int32(-1)
	for i := int32(0); i < 217; i++ {
		if int32(mem.u8(contourMap+uint32(i))) == target {
			contour = i
			break
		}
	}
	res.lag = lag
	res.contour = contour
	if contour < 0 || contour >= numContours {
		return res // out-of-range; stop consuming pitch bits
	}

	ctrBase := pcfg + uint32(contour)*0x44
	baseLag := mem.i32(ctrBase + 0x1d38) // contour base lag

	// (a) 64-symbol fine lag; read UNLESS prev_lag>=0 && -1 <= (base_lag-prev_lag) < 3.
	curLag2 := baseLag
	readFine := true
	if st.prevLag >= 0 {
		delta := baseLag - st.prevLag
		if delta >= -1 && delta < 3 {
			readFine = false
		}
	}
	var subfrW int32
	if readFine {
		sym := dec.decode64FineSym()
		curLag2 = (baseLag << 6) + sym
		st.prevFracLag = curLag2
		st.prevLag = baseLag
		segLen0 := mem.i32(ctrBase + 0x1d58)
		for range max(segLen0, 0) {
			if uint32(subfrW) < 4 {
				res.intLagQ6[subfrW] = curLag2
			}
			if uint32(subfrW) < 8 {
				res.blockLags[subfrW] = curLag2
			}
			subfrW++
		}
		if uint32(subfrW) < 4 {
			res.intLagQ6[subfrW] = curLag2 // trailing write, subfr_w not incremented
		}
		if uint32(subfrW) < 8 {
			res.blockLags[subfrW] = curLag2
		}
	}

	// (b) fractional per-segment loop:
	cnt2 := mem.i32(ctrBase + 0x1d78)
	var segSel int32
	if avgGain >= 10007 {
		if avgGain < 14085 {
			segSel = 1
		} else {
			segSel = 2
		}
	} else {
		segSel = 0
	}
	fracSegBase := fracBase + uint32(segSel)*0x280
	l3 := st.prevFracLag
	l2 := curLag2
	startSeg := int32(0)
	if readFine {
		startSeg = 1
	}
	res.numSeg = cnt2
	for seg := startSeg; seg < cnt2; seg++ {
		segLag := mem.i32(ctrBase + 0x1d38 + uint32(seg)*4)
		nl2 := ((l2 << 6) - l3) + ((segLag - l2) << 6)
		off := fracSegBase + uint32(nl2*2) + 0xfe
		sym := decodeCdfMem(dec, mem, off, 65)
		l3 = sym + st.prevFracLag + nl2
		if uint32(seg) < 8 {
			res.sampleLagQ6[seg] = l3
		}
		segLen := mem.i32(ctrBase + 0x1d58 + uint32(seg)*4)
		for range max(segLen, 0) {
			if uint32(subfrW) < 4 {
				res.intLagQ6[subfrW] = l3
			}
			if uint32(subfrW) < 8 {
				res.blockLags[subfrW] = l3
			}
			subfrW++
		}
		l2 = segLag
		st.prevFracLag = l3
		st.prevLag = segLag
	}
	res.numSubfr = subfrW
	// (Rust's log::trace of the lag/contour is omitted.)
	return res
}
