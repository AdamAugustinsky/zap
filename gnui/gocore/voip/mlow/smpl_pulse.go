// Ported from whatsapp-rust 5e72baf wacore/src/voip/mlow/smpl_pulse.rs, MIT.
//
// MLow PVQ-style pulse decode (post-LSF blocks of WASM func 3545). The active 0x50 capture frames
// are config=0 (p6=0), so the pulse COUNT uses the NB triangular prior; the split/magnitude/sign
// machinery walks the same CDF tables, now read from the logical `CcTables` (built from a seed)
// instead of the cc_blob heap window. `wrapping_*` mirrors the WASM's i32/u32 modular arithmetic
// (Go's fixed-width integer arithmetic wraps the same way).

package mlow

// smplPulseCountByte is the static gain-helper table (rodata 0xe8990): pulse-count byte index
// `[config*3 + (p4+s1)]`.
var smplPulseCountByte = [8]uint8{80, 160, 160, 16, 32, 32, 0, 0}

func mem8Static(addr uint32) uint8 {
	if addr >= 0xe8990 && addr < 0xe8998 {
		return smplPulseCountByte[addr-0xe8990]
	}
	return 0
}

// smplPulseResult is the decoded excitation for one internal (20 ms) frame.
type smplPulseResult struct {
	pulses []int32  // signed pulse magnitudes per sample position (len = p2)
	subfr  [4]int32 // per-subframe pulse counts
}

// smplPulseScratch holds the buffers [decodeSmplPulsesInto] reuses across calls: the returned
// pulse vector and the position/magnitude lists (Rust allocates all three per call).
type smplPulseScratch struct {
	pulses  []int32
	posList []int32
	magList []int32
}

// decodeSmplPulses decodes the pulse blocks of one internal frame. `p2` = frame samples (320), `p3`
// = num subframes (4), `p4` = regular flag (1), `p6` = config (0/1), `s1` = LSF stage-1 selector.
// It allocates the result; the decoder uses [decodeSmplPulsesInto].
func decodeSmplPulses(
	dec *rangeDecoder,
	cc *ccTables,
	p2 int32,
	p3 int32,
	p4 int32,
	p6 int32,
	s1 int32,
) smplPulseResult {
	var s smplPulseScratch
	return decodeSmplPulsesInto(&s, dec, cc, p2, p3, p4, p6, s1)
}

// decodeSmplPulsesInto is Rust `decode_smpl_pulses` with its three Vecs taken from `s`, so a warm
// scratch decodes without allocating. The returned `pulses` aliases `s.pulses` and stays valid
// until the next call with the same scratch.
func decodeSmplPulsesInto(
	s *smplPulseScratch,
	dec *rangeDecoder,
	cc *ccTables,
	p2 int32,
	p3 int32,
	p4 int32,
	p6 int32,
	s1 int32,
) smplPulseResult {
	n := int(max(p2, 0))
	if cap(s.pulses) < n {
		s.pulses = make([]int32, n)
	}
	s.pulses = s.pulses[:n]
	clear(s.pulses)
	res := smplPulseResult{
		pulses: s.pulses,
	}

	idx := p4 + s1
	bByte := int32(mem8Static(0xe8990 + uint32(p6*3+idx)))
	frameLen4k := bByte * p2 / 320
	subfrLen16 := frameLen4k / p3
	posPerSubfr := p2 / p3

	// --- pulse COUNT ---
	var total int32
	if p6 != 0 {
		// WB low-rate: the pulse-count CDF for this voicing class.
		cdf := cc.nPulseCount(idx)
		total = dec.decodeCdf(cdf)
	} else {
		// NB (config=0, our path): a TRIANGULAR prior over [0, frame_len4k].
		l := uint32(frameLen4k)
		triT := func(k uint32) uint32 {
			a := (k + 2) * (l + 1)
			b := (k - 1) * (k + 131070) >> 1
			return (a - b) & 0xffff
		}
		ft := triT(l)
		if ft == 0 {
			ft = 1
		}
		val := dec.decode(ft)
		limit := uint32(frameLen4k) + 1
		var prevCum uint32
		var k uint32
		for {
			if k == limit {
				break
			}
			cum := triT(k)
			// found when prev_cum <= val < cum (the cumulative-triangular interval containing val).
			if prevCum <= val && val < cum {
				dec.update(prevCum, cum, ft)
				break
			}
			prevCum = cum
			k++
		}
		total = int32(k)
	}

	// --- recursive binary SPLIT (p3==4 path) ---
	var split [8]int32
	if total != 0 {
		sum := max(total-subfrLen16*2, 0)
		lo := max(total-80, 0)
		if sum < lo {
			// min_split2 >= min_split assert path; treat as parse error (zeroed subframes).
			return res
		}
		hiBound := total - lo
		if sum < hiBound {
			// window the split CDF at (sum - lo); n entries from the table base.
			n := int((hiBound - sum) + 2)
			sum += dec.decodeCdfWindow(cc.splitCmf(total), int(sum-lo), n)
		}
		if sum > 0 {
			s0 := smplSplit3537(dec, cc, sum, subfrLen16)
			split[0] = s0
			split[1] = sum - s0
		}
		if sum < total {
			s2 := smplSplit3537(dec, cc, total-sum, subfrLen16)
			split[2] = s2
			split[3] = (total - sum) - s2
		}
		// A corrupt -1 from either half zeroes the whole split (and n_pulses).
		if split[0] == -1 || split[2] == -1 {
			split = [8]int32{}
		}
	}

	take := int(pulseClampI32(p3, 0, 4))
	copy(res.subfr[:take], split[:take])

	// --- MAGNITUDE block: per-subframe run-length pulse positions ---
	posPer := posPerSubfr
	posList := s.posList[:0]
	magList := s.magList[:0]
	pulseIdx := int32(-1)
	for subfr := int32(0); subfr < p3; subfr++ {
		cnt := split[subfr]
		if cnt <= 0 {
			continue
		}
		basePos := posPer * subfr
		runPos := basePos
		pos := posPer
		c := cnt
		k := int32(0)
		for k < cnt {
			if pos < 0 {
				break // defensive: malformed frame must not drive a huge CDF length
			}
			oct := (pos + 7) / 8
			bucket := cc.runlen(oct)
			// window the c-pulses CDF by (max_samples - pos), reading pos+1 entries.
			start := int(bucket.maxSamples() - pos)
			m := dec.decodeCdfWindow(bucket.cmf(c), start, int(pos+1))
			if m > 0 || k == 0 {
				pulseIdx++
				runPos += m
				posList = append(posList, runPos)
				magList = append(magList, 1)
				pos -= m
			} else if pulseIdx >= 0 {
				magList[pulseIdx]++
			}
			c--
			k++
		}
	}

	s.posList, s.magList = posList, magList
	numPos := pulseIdx + 1

	// --- SIGN block: batched uniform sign reads (1 bit per position) ---
	if numPos > 0 {
		p := int32(0)
		for p <= pulseIdx {
			nbits := numPos - p
			if nbits >= 15 {
				nbits = 15
			}
			if nbits <= 0 {
				break
			}
			sym := dec.decodeRawSymbol(uint32(nbits))
			bitfield := sym << ((16 - uint32(nbits)) & 31)
			end := p + nbits
			for q := p; q < end; q++ {
				sign := int32((bitfield>>14)&2) - 1 // +1 if MSB set else -1
				magList[q] *= sign
				bitfield <<= 1
			}
			p = end
		}
	}

	// scatter signed magnitudes into the pulse vector at their absolute positions.
	for i := int32(0); i < numPos; i++ {
		pp := posList[i]
		if pp >= 0 && int(pp) < len(res.pulses) {
			res.pulses[pp] = magList[i]
		}
	}
	// (Rust's log::trace of the pulse totals is omitted.)
	return res
}

// pulseClampI32 is Rust's i32::clamp.
func pulseClampI32(x, lo, hi int32) int32 {
	if x < lo {
		return lo
	}
	if x > hi {
		return hi
	}
	return x
}

// smplSplit3537 splits `count` pulses across a range, returning the count assigned to the first
// half (func 3537).
func smplSplit3537(dec *rangeDecoder, cc *ccTables, count int32, granularity int32) int32 {
	lo := min(count, granularity)
	minSplit := max(count-granularity, 0)
	if lo < minSplit {
		return -1
	}
	if minSplit == lo {
		return minSplit
	}
	// window split_CMFs[count-1] at min_split, n entries.
	n := int((lo - minSplit) + 2)
	return dec.decodeCdfWindow(cc.splitCmf(count), int(minSplit), n) + minSplit
}
