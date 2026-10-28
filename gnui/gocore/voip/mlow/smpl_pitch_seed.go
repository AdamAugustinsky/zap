// Ported from whatsapp-rust 5e72baf wacore/src/voip/mlow/smpl_pitch_seed.rs, MIT.
//
// Build-from-seed for the MLow pitch runtime tables. The expanded pitch table is the expansion of a
// small packed seed (the blocksegs bitstream + the index maps + the DCMF arrays), so we store the
// seed and rerun the init at load: range-decode of the blocksegs (via `RangeDecoder`), the
// per-track contour expansion, and the integer CDF expansion. Only the 20 ms / 8-subframe config is
// built (the active MLow path).
//
// The on-disk packed pitch ROM (`tables.proto` `PitchSeed`, Rust's re-exported
// `smpl_tables_blob::tables::PitchSeed`) is smpl_tables_blob.go's pitchSeed; its `bytes` fields
// reshape row-major at build.

package mlow

// PITCH_NUM_BLOCKS (9) is shared.go's pitchNumBlocks.
const (
	numBlocksegs   = 217
	numBlocktracks = 187
)

// ecDecodeUniform decodes a uniform symbol in `[0, N)`.
func ecDecodeUniform(dec *rangeDecoder, n uint32) uint32 {
	cmfLow := dec.decode(n)
	dec.update(cmfLow, cmfLow+1, n)
	return cmfLow
}

// decodeBlocksegs decodes one blockseg: `len = uniform(6)+1`, then `len` pairs of
// `(uniform(9), uniform(4)+1)`.
func decodeBlocksegs(dec *rangeDecoder) blockSeg {
	const nLen uint32 = 6
	const nBlock uint32 = 9
	const nSeglen uint32 = 4
	ln := int(ecDecodeUniform(dec, nLen) + 1)
	blocks := make([]int, 0, ln)
	seglens := make([]int, 0, ln)
	for range ln {
		blocks = append(blocks, int(ecDecodeUniform(dec, nBlock)))
		seglens = append(seglens, int(ecDecodeUniform(dec, nSeglen)+1))
	}
	return blockSeg{
		nblocks: ln,
		blocks:  blocks,
		seglens: seglens,
	}
}

// genBlocktrack expands each track's blockseg into the per-subframe track + mean/deltas.
func genBlocktrack(blocksegs []blockSeg, blocksegsIx [][2]int) []blockTrack {
	out := make([]blockTrack, 0, numBlocktracks)
	for trackIdx := 0; trackIdx < numBlocktracks; trackIdx++ {
		seg := &blocksegs[blocksegsIx[trackIdx][0]]
		var track [numSubframes]int
		segIdx := 0
		meanblock := float32(0.0)
		trackdeltas := float32(0.0)
		for b := 0; b < seg.nblocks; b++ {
			for range seg.seglens[b] {
				track[segIdx] = seg.blocks[b]
				segIdx++
			}
			meanblock += float32(seg.blocks[b] * seg.seglens[b])
			if b != 0 {
				trackdeltas += float32(absI32(int32(seg.blocks[b-1]) - int32(seg.blocks[b])))
			}
		}
		meanblock /= float32(numSubframes)
		out = append(out, blockTrack{
			track:       track,
			meanblock:   meanblock,
			trackdeltas: trackdeltas,
		})
	}
	return out
}

// pitchSeedDcmfToCmf is the integer expansion of a DCMF to a cumulative CDF of length `len+1`.
func pitchSeedDcmfToCmf(dcmf []uint8) []uint32 {
	dcmfLen := len(dcmf)
	cmf := make([]uint32, dcmfLen+1)
	var sum int64
	for n := 0; n < dcmfLen; n++ {
		tmp := int32(dcmf[n]) + 1
		tmp *= tmp
		if tmp > 65535 {
			tmp = 65535
		}
		cmf[n+1] = uint32(tmp)
		sum += int64(tmp)
	}
	cmf[0] = 0
	for n := 1; n < dcmfLen+1; n++ {
		prev := int64(cmf[n-1])
		add := (int64(cmf[n])*(32767-int64(dcmfLen)))/sum + 1
		cmf[n] = uint32(prev + add)
	}
	return cmf
}

// pitchSeedPairs is Rust's `.chunks_exact(2).map(|p| [p[0] as usize, p[1] as usize]).collect()`.
func pitchSeedPairs(b []byte) [][2]int {
	out := make([][2]int, 0, len(b)/2)
	for i := 0; i+2 <= len(b); i += 2 {
		out = append(out, [2]int{int(b[i]), int(b[i+1])})
	}
	return out
}

// pitchSeedCmfRows is Rust's `.chunks_exact(n).map(dcmf_to_cmf).collect()`.
func pitchSeedCmfRows(b []byte, n int) [][]uint32 {
	out := make([][]uint32, 0, len(b)/n)
	for i := 0; i+n <= len(b); i += n {
		out = append(out, pitchSeedDcmfToCmf(b[i:i+n]))
	}
	return out
}

func (s *pitchSeed) build() pitchTables {
	dec := newRangeDecoder(s.blocksegsBitstream)
	blocksegs := make([]blockSeg, 0, numBlocksegs)
	for range numBlocksegs {
		blocksegs = append(blocksegs, decodeBlocksegs(&dec))
	}

	blocksegsIx := pitchSeedPairs(s.blocksegsIx)
	firstblockRange := pitchSeedPairs(s.firstblockRange)

	blocktracks := genBlocktrack(blocksegs, blocksegsIx)

	blocksegs2idx := make([]int, len(s.blocksegs2idx))
	for i, x := range s.blocksegs2idx {
		blocksegs2idx[i] = int(x)
	}
	blocksegIdxCmf := pitchSeedDcmfToCmf(s.blocksegIdxDcmf)
	deltaLagCmfs := pitchSeedCmfRows(s.deltaLagDcmfs, 319)
	blockTransitionCmf := pitchSeedCmfRows(s.blockTransitionDcmf, pitchNumBlocks)

	// Rust: debug_assert_eq!(block_transition_cmf.len(), PITCH_NUM_BLOCKS) (debug-only; omitted).

	return pitchTablesFromParts(
		blocksegs,
		blocktracks,
		blocksegs2idx,
		blocksegIdxCmf,
		deltaLagCmfs,
		blocksegsIx,
		firstblockRange,
		blockTransitionCmf,
	)
}

// contourWindowParts is the pitch lag/contour heap window built from the same seed: 217
// per-contour records, the contour map, the lag/frac/delta CDFs, and the delta-lag bounds; all
// expansions of the blocksegs bitstream + index maps + DCMFs. `smpl_mem` lays these out at the
// fixed WASM addresses Group D reads.
type contourWindowParts struct {
	// Per contour: (base/seg lags = blockseg `blocks`, seg lens = blockseg `seglens`). Rust's tuple
	// `(Vec<usize>, Vec<usize>)` is `[2][]int{blocks, seglens}`.
	records [][2][]int
	// `contour_map[217]` (== `blocksegs2idx`).
	contourMap []uint8
	// Delta-lag bounds (== `firstblock_range` flattened).
	firstblockRange [][2]int
	// Primary-lag CDF (`dcmf_to_cmf(blockseg_idx_dcmf)`), 218 entries.
	lagCdf []uint32
	// 3 fractional-lag CDFs (`dcmf_to_cmf(delta_lag_dcmfs)`), 320 entries each.
	fracCmfs [][]uint32
	// 9 delta-lag transition CDFs (`dcmf_to_cmf(block_transition_dcmf)`), 10 entries each.
	deltaCmfs [][]uint32
}

// buildContourWindow builds the contour-window half: re-decode the blocksegs and expand the index
// maps + DCMFs into the exact tables Group D's pointer-chase reads.
func (s *pitchSeed) buildContourWindow() contourWindowParts {
	dec := newRangeDecoder(s.blocksegsBitstream)
	records := make([][2][]int, 0, numBlocksegs)
	for range numBlocksegs {
		bs := decodeBlocksegs(&dec)
		records = append(records, [2][]int{bs.blocks, bs.seglens})
	}
	firstblockRange := pitchSeedPairs(s.firstblockRange)
	return contourWindowParts{
		records:         records,
		contourMap:      append([]uint8(nil), s.blocksegs2idx...),
		firstblockRange: firstblockRange,
		lagCdf:          pitchSeedDcmfToCmf(s.blocksegIdxDcmf),
		fracCmfs:        pitchSeedCmfRows(s.deltaLagDcmfs, 319),
		deltaCmfs:       pitchSeedCmfRows(s.blockTransitionDcmf, pitchNumBlocks),
	}
}
