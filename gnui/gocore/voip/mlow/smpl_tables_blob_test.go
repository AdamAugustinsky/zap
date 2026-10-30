// Ported from whatsapp-rust 5e72baf wacore/src/voip/mlow/smpl_tables_blob.rs, MIT.

package mlow

import (
	"bytes"
	"compress/zlib"
	"os"
	"path/filepath"
	"testing"
)

// genZlibLevel is the zlib level used by the generator. Fixed so re-running yields identical `.bin`
// bytes. (Go's compress/flate is not byte-compatible with flate2/miniz at the same level, so a
// Go-regenerated blob decodes identically but need not match the committed bytes.)
const genZlibLevel = 9

// makeBlobRaw zlib-compresses already-encoded bytes (for callers that encode protobuf
// themselves). Deterministic.
func makeBlobRaw(b []byte) []byte {
	var buf bytes.Buffer
	enc, err := zlib.NewWriterLevel(&buf, genZlibLevel)
	if err != nil {
		panic("zlib level: " + err.Error())
	}
	if _, err := enc.Write(b); err != nil {
		panic("zlib write: " + err.Error())
	}
	if err := enc.Close(); err != nil {
		panic("zlib finish: " + err.Error())
	}
	return buf.Bytes()
}

// tablesBlobTryReadJson reads a JSON dump if present (cwd is the package directory under
// `go test`); ok is false if absent, so a partial dump set regenerates only the tables whose JSON
// is on disk.
func tablesBlobTryReadJson(t *testing.T, name string) (string, bool) {
	b, err := os.ReadFile(filepath.Join("testdata", name))
	if err != nil {
		t.Logf("skip %s: JSON absent", name)
		return "", false
	}
	return string(b), true
}

func tablesBlobWriteBin(t *testing.T, name string, b []byte) {
	path := filepath.Join("testdata", name)
	if err := os.WriteFile(path, b, 0o644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
	t.Logf("wrote %s (%d bytes)", path, len(b))
}

// Regenerate the runtime-table `.bin` from the (gitignored) JSON dumps. Env-gated so a normal
// `go test` run never touches the committed blobs. Deterministic: re-running yields the same
// bytes (fixed zlib level + protobuf's canonical encoding).
//
//	VOIP_GEN_TABLES=1 go test -run TestSmplTablesBlob_GenRuntimeTables -v ./gocore/voip/mlow/
func TestSmplTablesBlob_GenRuntimeTables(t *testing.T) {
	if _, ok := os.LookupEnv("VOIP_GEN_TABLES"); !ok {
		t.Skip("VOIP_GEN_TABLES not set; skipping table generation")
	}

	// 1. LSF seed ROM: protobuf (tables.proto `LsfSeed`), then zlib. The expanded LSF tables
	//    (synth/lsf-cb/lsf-decode) are derived from this at load.
	if j, ok := tablesBlobTryReadJson(t, "lsf_seed.json"); ok {
		seed := lsfSeedSeedFromJson(j)
		tablesBlobWriteBin(t, "lsf_seed.bin", makeBlobRaw(seed.encodeToVec()))
	}

	// 2. pitch seed ROM: protobuf (tables.proto `PitchSeed`), then zlib.
	if j, ok := tablesBlobTryReadJson(t, "pitch_seed.json"); ok {
		seed := pitchSeedSeedFromJson(j)
		tablesBlobWriteBin(t, "pitch_seed.bin", makeBlobRaw(seed.encodeToVec()))
	}

	// 3. cc seed ROM: protobuf (tables.proto `CcSeed`), then zlib. The nrgres/gains (Group A/E)
	//    and LTP gain (Group C) CDFs are derived from this at load.
	if j, ok := tablesBlobTryReadJson(t, "cc_seed.json"); ok {
		seed := ccTablesSeedFromJson(j)
		tablesBlobWriteBin(t, "cc_seed.bin", makeBlobRaw(seed.encodeToVec()))
	}
}

// Go-only: the hand-written protowire codec must reproduce each committed blob's protobuf bytes
// exactly (decode then canonical re-encode), so no field is dropped, mistyped or misnumbered.
func TestSmplTablesBlob_CommittedBlobsReencodeIdentically(t *testing.T) {
	check := func(name string, decodeEncode func([]byte) []byte) {
		raw := tablesBlobInflate(testdataBytes(t, name))
		if got := decodeEncode(raw); !bytes.Equal(got, raw) {
			t.Errorf("%s: re-encoded protobuf differs (%d vs %d bytes)", name, len(got), len(raw))
		}
	}
	check("lsf_seed.bin", func(b []byte) []byte {
		var m lsfSeed
		if err := m.decodeFromSlice(b); err != nil {
			t.Fatal(err)
		}
		if len(m.rot8) != 2*16*16*16 || len(m.mean) != 2*16 || len(m.cb16) != 2*16*16 {
			t.Errorf("lsf_seed.bin: unexpected field sizes")
		}
		return m.encodeToVec()
	})
	check("pitch_seed.bin", func(b []byte) []byte {
		var m pitchSeed
		if err := m.decodeFromSlice(b); err != nil {
			t.Fatal(err)
		}
		if len(m.blocksegsBitstream) != 676 || len(m.blockTransitionDcmf) != 81 {
			t.Errorf("pitch_seed.bin: unexpected field sizes")
		}
		return m.encodeToVec()
	})
	check("cc_seed.bin", func(b []byte) []byte {
		var m ccSeed
		if err := m.decodeFromSlice(b); err != nil {
			t.Fatal(err)
		}
		if len(m.acbgainsCbHrQ14) != 32 || m.gainReconBase != 0xf35e0 {
			t.Errorf("cc_seed.bin: unexpected fields")
		}
		return m.encodeToVec()
	})
}
