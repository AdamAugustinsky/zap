// Ported from whatsapp-rust 5e72baf wacore/src/voip/mlow/smpl_pitch_seed.rs, MIT.

package mlow

import (
	"encoding/json"
	"testing"
)

// pitchSeedRawSeed mirrors the Rust test's serde RawSeed. JSON byte arrays are read as []int
// (encoding/json would expect base64 for []byte) and narrowed like serde's u8.
type pitchSeedRawSeed struct {
	BlocksegsBitstream  []int   `json:"blocksegs_bitstream"`
	Blocksegs2idx       []int   `json:"blocksegs2idx"`
	BlocksegsIx         [][]int `json:"blocksegs_ix"`
	FirstblockRange     [][]int `json:"firstblock_range"`
	BlocksegIdxDcmf     []int   `json:"blockseg_idx_dcmf"`
	DeltaLagDcmfs       [][]int `json:"delta_lag_dcmfs"`
	BlockTransitionDcmf [][]int `json:"block_transition_dcmf"`
}

func pitchSeedBytes(v []int) []byte {
	out := make([]byte, len(v))
	for i, x := range v {
		if x < 0 || x > 255 {
			panic("pitch_seed.json: value out of u8 range")
		}
		out[i] = byte(x)
	}
	return out
}

func pitchSeedFlat(v [][]int) []byte {
	var out []byte
	for _, row := range v {
		out = append(out, pitchSeedBytes(row)...)
	}
	return out
}

// pitchSeedSeedFromJson parses the (gitignored) pitch_seed.json dump into the packed seed
// (Rust's #[cfg(test)] seed_from_json; the VOIP_GEN_TABLES generator calls it).
func pitchSeedSeedFromJson(s string) pitchSeed {
	var r pitchSeedRawSeed
	if err := json.Unmarshal([]byte(s), &r); err != nil {
		panic("pitch_seed.json: " + err.Error())
	}
	return pitchSeed{
		blocksegsBitstream:  pitchSeedBytes(r.BlocksegsBitstream),
		blocksegs2idx:       pitchSeedBytes(r.Blocksegs2idx),
		blocksegsIx:         pitchSeedFlat(r.BlocksegsIx),
		firstblockRange:     pitchSeedFlat(r.FirstblockRange),
		blocksegIdxDcmf:     pitchSeedBytes(r.BlocksegIdxDcmf),
		deltaLagDcmfs:       pitchSeedFlat(r.DeltaLagDcmfs),
		blockTransitionDcmf: pitchSeedFlat(r.BlockTransitionDcmf),
	}
}

// Permanent no-regression tripwire for the pitch init: a checksum over the seed-built tables.
func TestSmplPitchSeed_PitchSeedBuildGoldenChecksum(t *testing.T) {
	built := loadPitchTables()
	if got := built.debugChecksum(); got != 0x21ce_965d_809c_8048 {
		t.Fatalf("pitch seed-built struct checksum: got %#x want %#x", got, uint64(0x21ce_965d_809c_8048))
	}
}
