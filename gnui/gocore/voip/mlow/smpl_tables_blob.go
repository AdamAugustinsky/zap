// Ported from whatsapp-rust 5e72baf wacore/src/voip/mlow/smpl_tables_blob.rs, MIT.
//
// Shared codec for the MLow runtime constant tables: each table is stored on disk as a
// zlib-compressed protobuf blob (`testdata/<x>.bin`, schema in `tables.proto`) inflated + decoded
// once at load. Protobuf throughout so the byte-identical blobs stay portable across consumers.
//
// The JSON dumps (`testdata/<x>.json`, gitignored) are the source of truth (see
// `testdata/PROVENANCE.md`); the `.bin` are generated from them by the `VOIP_GEN_TABLES` test in
// this module. The load path is byte-identical to deserializing the JSON, so the codec output does
// not change.
//
// Go deviation: Rust decodes with buffa-generated types (`mod tables`). Here the `tables.proto`
// messages are plain Go structs (heapWindow, region, lsfSeed, pitchSeed, ccSeed) with hand-written
// protowire (un)marshalers. The decoder accepts packed and unpacked repeated scalars and skips
// unknown fields; the encoder writes proto3 canonical form (field order, defaults omitted,
// repeated scalars packed).

package mlow

import (
	"bytes"
	"compress/zlib"
	"errors"
	"io"
	"math"

	"google.golang.org/protobuf/encoding/protowire"
)

// heapWindow (`tables.proto` HeapWindow): the embedded MLow heap window, heap byte regions at their
// absolute addresses plus the table-base pointers. Carries the residual pitch-contour window (the
// only reads still served from the heap). Stored zlib-compressed.
type heapWindow struct {
	regions []region // 1
	gCc     uint32   // 2
	gNrg    uint32   // 3
	gPitch  uint32   // 4
	gClk    uint32   // 5
}

// region (`tables.proto` Region): one contiguous heap region, its absolute base address and bytes.
type region struct {
	base uint32 // 1
	data []byte // 2
}

// lsfSeed (`tables.proto` LsfSeed): irreducible LSF seed ROM. The expanded LSF runtime tables are
// derived from this at load. Flat row-major; the shape of each field is fixed by the codec.
// u8/i8 arrays are bytes; f32 are packed repeated float; u16 are repeated uint32.
type lsfSeed struct {
	rot8            []byte    // 1  [2][16][16][16]
	rotCond8        []byte    // 2  [2][2][16][16]
	st2AllQlvls8    []byte    // 3  [9593]
	st2AllQlvlDcmfs []byte    // 4  [9593]
	st2MinQi        []byte    // 5  [2][2][17][16] i8
	st2MaxQi        []byte    // 6  [2][2][17][16] i8
	cb16            []uint32  // 7  [2][16][16] u16
	cinv16          []uint32  // 8  [2][136]    u16
	cmf             []uint32  // 9  [2][17]     u16
	cmfCond         []uint32  // 10 [2][18]     u16
	lsfSel          []uint32  // 11 [3][3]      u16
	lsfExtra        []uint32  // 12 [3]         u16
	mean            []float32 // 13 [2][16]
	minDist         []float32 // 14 [2][17]
	regCond         []float32 // 15 [2]
	qstep           []float32 // 16 [2][2]
	// grid16 tables (17..21) dropped: derived at load from mean/reg_cond/rot_cond_8.
}

// pitchSeed (`tables.proto` PitchSeed): irreducible pitch seed ROM. The expanded pitch runtime
// tables are derived from this at load.
type pitchSeed struct {
	blocksegsBitstream  []byte // 1 [676]
	blocksegs2idx       []byte // 2 [217]
	blocksegsIx         []byte // 3 [187][2]
	firstblockRange     []byte // 4 [9][2]
	blocksegIdxDcmf     []byte // 5 [217]
	deltaLagDcmfs       []byte // 6 [3][319]
	blockTransitionDcmf []byte // 7 [9][9]
}

// ccSeed (`tables.proto` CcSeed): irreducible nrgres/gains + LTP gain seed ROM. The runtime CDFs
// are derived from the DCMFs at load (integer dcmf_to_cmf). gainRecon carries the static
// gain-reconstruction int16 rodata verbatim, addressed from gainReconBase.
type ccSeed struct {
	nrgresGain4Dcmf    []byte  // 1  [84]
	nrgresShape4Dcmf   []byte  // 2  [98]
	fcbgOffsetDcmf     []byte  // 3  [3][4][176]
	acbgainsHrDcmf     []byte  // 4  [17][16]
	fcbgainsVDcmf      []byte  // 5  [34]
	fcbgainsVDeltaDcmf []byte  // 6  [67]
	acbgainsCbHrQ14    []int32 // 7  [16][2] i16 (sint32)
	gainReconBase      uint32  // 8  0xf35e0
	gainRecon          []byte  // 9  gain-reconstruction int16 rodata
	nPulsesDcmfBgn     []byte  // 10 [17] pulse-count CDF seed (background noise)
	nPulsesDcmfUv      []byte  // 11 [33] pulse-count CDF seed (unvoiced)
	nPulsesDcmfV       []byte  // 12 [33] pulse-count CDF seed (voiced)
}

// tablesBlobMessage is what `load_blob_buffa` needs of a message type (buffa::Message).
type tablesBlobMessage[T any] interface {
	*T
	decodeFromSlice(b []byte) error
	encodeToVec() []byte
}

// maxTableBytes is the largest table blob we will inflate. The committed `.bin` set tops out well
// under this; the cap only exists because the pooled inflater requires one.
const maxTableBytes uint64 = 4 * 1024 * 1024

// tablesBlobInflate inflates a zlib blob (the runtime load path for the table `.bin`).
func tablesBlobInflate(compressed []byte) []byte {
	zr, err := zlib.NewReader(bytes.NewReader(compressed))
	if err != nil {
		panic("mlow table blob must zlib-inflate: " + err.Error())
	}
	out, err := io.ReadAll(io.LimitReader(zr, int64(maxTableBytes)+1))
	if err == nil {
		err = zr.Close()
	}
	if err != nil {
		panic("mlow table blob must zlib-inflate: " + err.Error())
	}
	if uint64(len(out)) > maxTableBytes {
		panic("mlow table blob must zlib-inflate: exceeds maxTableBytes")
	}
	return out
}

// loadBlobBuffa loads a protobuf table from its embedded zlib blob:
// `loadBlobBuffa[lsfSeed](lsfSeedBin)`.
func loadBlobBuffa[T any, PT tablesBlobMessage[T]](compressed []byte) T {
	b := tablesBlobInflate(compressed)
	var msg T
	if err := PT(&msg).decodeFromSlice(b); err != nil {
		panic("mlow table blob must protobuf-decode: " + err.Error())
	}
	return msg
}

// protowire helpers.

var errTablesBlobProto = errors.New("malformed protobuf")

// tablesBlobFields walks the top-level fields of a message, calling f with each field's number,
// wire type and raw value bytes (varint / fixed values are passed already parsed in v).
func tablesBlobFields(b []byte, f func(num protowire.Number, typ protowire.Type, v uint64, raw []byte) error) error {
	for len(b) > 0 {
		num, typ, n := protowire.ConsumeTag(b)
		if n < 0 {
			return protowire.ParseError(n)
		}
		b = b[n:]
		var v uint64
		var raw []byte
		switch typ {
		case protowire.VarintType:
			v, n = protowire.ConsumeVarint(b)
		case protowire.Fixed32Type:
			var v32 uint32
			v32, n = protowire.ConsumeFixed32(b)
			v = uint64(v32)
		case protowire.Fixed64Type:
			v, n = protowire.ConsumeFixed64(b)
		case protowire.BytesType:
			raw, n = protowire.ConsumeBytes(b)
		default:
			n = protowire.ConsumeFieldValue(num, typ, b)
		}
		if n < 0 {
			return protowire.ParseError(n)
		}
		b = b[n:]
		if err := f(num, typ, v, raw); err != nil {
			return err
		}
	}
	return nil
}

// tablesBlobBytes reads a `bytes` field (last one wins, like proto3).
func tablesBlobBytes(typ protowire.Type, raw []byte) ([]byte, error) {
	if typ != protowire.BytesType {
		return nil, errTablesBlobProto
	}
	return append([]byte(nil), raw...), nil
}

// tablesBlobVarint reads a singular varint field.
func tablesBlobVarint(typ protowire.Type, v uint64) (uint64, error) {
	if typ != protowire.VarintType {
		return 0, errTablesBlobProto
	}
	return v, nil
}

// tablesBlobRepeatedVarint appends a repeated varint field, packed or unpacked.
func tablesBlobRepeatedVarint(dst []uint64, typ protowire.Type, v uint64, raw []byte) ([]uint64, error) {
	switch typ {
	case protowire.VarintType:
		return append(dst, v), nil
	case protowire.BytesType:
		for len(raw) > 0 {
			x, n := protowire.ConsumeVarint(raw)
			if n < 0 {
				return nil, protowire.ParseError(n)
			}
			dst = append(dst, x)
			raw = raw[n:]
		}
		return dst, nil
	}
	return nil, errTablesBlobProto
}

// tablesBlobRepeatedFixed32 appends a repeated fixed32 (float) field, packed or unpacked.
func tablesBlobRepeatedFixed32(dst []float32, typ protowire.Type, v uint64, raw []byte) ([]float32, error) {
	switch typ {
	case protowire.Fixed32Type:
		return append(dst, math.Float32frombits(uint32(v))), nil
	case protowire.BytesType:
		if len(raw)%4 != 0 {
			return nil, errTablesBlobProto
		}
		for len(raw) > 0 {
			x, n := protowire.ConsumeFixed32(raw)
			if n < 0 {
				return nil, protowire.ParseError(n)
			}
			dst = append(dst, math.Float32frombits(x))
			raw = raw[n:]
		}
		return dst, nil
	}
	return nil, errTablesBlobProto
}

func tablesBlobU32s(dst []uint32, typ protowire.Type, v uint64, raw []byte) ([]uint32, error) {
	var tmp []uint64
	tmp, err := tablesBlobRepeatedVarint(tmp, typ, v, raw)
	if err != nil {
		return nil, err
	}
	for _, x := range tmp {
		dst = append(dst, uint32(x))
	}
	return dst, nil
}

func tablesBlobSint32s(dst []int32, typ protowire.Type, v uint64, raw []byte) ([]int32, error) {
	var tmp []uint64
	tmp, err := tablesBlobRepeatedVarint(tmp, typ, v, raw)
	if err != nil {
		return nil, err
	}
	for _, x := range tmp {
		dst = append(dst, int32(protowire.DecodeZigZag(x&math.MaxUint32)))
	}
	return dst, nil
}

func tablesBlobAppendBytes(b []byte, num protowire.Number, v []byte) []byte {
	if len(v) == 0 {
		return b
	}
	b = protowire.AppendTag(b, num, protowire.BytesType)
	return protowire.AppendBytes(b, v)
}

func tablesBlobAppendVarint(b []byte, num protowire.Number, v uint64) []byte {
	if v == 0 {
		return b
	}
	b = protowire.AppendTag(b, num, protowire.VarintType)
	return protowire.AppendVarint(b, v)
}

func tablesBlobAppendPackedU32(b []byte, num protowire.Number, v []uint32) []byte {
	if len(v) == 0 {
		return b
	}
	var p []byte
	for _, x := range v {
		p = protowire.AppendVarint(p, uint64(x))
	}
	return tablesBlobAppendBytes(b, num, p)
}

func tablesBlobAppendPackedSint32(b []byte, num protowire.Number, v []int32) []byte {
	if len(v) == 0 {
		return b
	}
	var p []byte
	for _, x := range v {
		p = protowire.AppendVarint(p, protowire.EncodeZigZag(int64(x)))
	}
	return tablesBlobAppendBytes(b, num, p)
}

func tablesBlobAppendPackedF32(b []byte, num protowire.Number, v []float32) []byte {
	if len(v) == 0 {
		return b
	}
	var p []byte
	for _, x := range v {
		p = protowire.AppendFixed32(p, math.Float32bits(x))
	}
	return tablesBlobAppendBytes(b, num, p)
}

// region / heapWindow.

func (m *region) decodeFromSlice(b []byte) error {
	*m = region{}
	return tablesBlobFields(b, func(num protowire.Number, typ protowire.Type, v uint64, raw []byte) error {
		var err error
		switch num {
		case 1:
			var x uint64
			x, err = tablesBlobVarint(typ, v)
			m.base = uint32(x)
		case 2:
			m.data, err = tablesBlobBytes(typ, raw)
		}
		return err
	})
}

func (m *region) encodeToVec() []byte {
	var b []byte
	b = tablesBlobAppendVarint(b, 1, uint64(m.base))
	b = tablesBlobAppendBytes(b, 2, m.data)
	return b
}

func (m *heapWindow) decodeFromSlice(b []byte) error {
	*m = heapWindow{}
	return tablesBlobFields(b, func(num protowire.Number, typ protowire.Type, v uint64, raw []byte) error {
		var err error
		var x uint64
		switch num {
		case 1:
			if typ != protowire.BytesType {
				return errTablesBlobProto
			}
			var r region
			if err = r.decodeFromSlice(raw); err == nil {
				m.regions = append(m.regions, r)
			}
		case 2:
			x, err = tablesBlobVarint(typ, v)
			m.gCc = uint32(x)
		case 3:
			x, err = tablesBlobVarint(typ, v)
			m.gNrg = uint32(x)
		case 4:
			x, err = tablesBlobVarint(typ, v)
			m.gPitch = uint32(x)
		case 5:
			x, err = tablesBlobVarint(typ, v)
			m.gClk = uint32(x)
		}
		return err
	})
}

func (m *heapWindow) encodeToVec() []byte {
	var b []byte
	for i := range m.regions {
		// Sub-messages are always written, even when empty.
		b = protowire.AppendTag(b, 1, protowire.BytesType)
		b = protowire.AppendBytes(b, m.regions[i].encodeToVec())
	}
	b = tablesBlobAppendVarint(b, 2, uint64(m.gCc))
	b = tablesBlobAppendVarint(b, 3, uint64(m.gNrg))
	b = tablesBlobAppendVarint(b, 4, uint64(m.gPitch))
	b = tablesBlobAppendVarint(b, 5, uint64(m.gClk))
	return b
}

// lsfSeed.

func (m *lsfSeed) decodeFromSlice(b []byte) error {
	*m = lsfSeed{}
	return tablesBlobFields(b, func(num protowire.Number, typ protowire.Type, v uint64, raw []byte) error {
		var err error
		switch num {
		case 1:
			m.rot8, err = tablesBlobBytes(typ, raw)
		case 2:
			m.rotCond8, err = tablesBlobBytes(typ, raw)
		case 3:
			m.st2AllQlvls8, err = tablesBlobBytes(typ, raw)
		case 4:
			m.st2AllQlvlDcmfs, err = tablesBlobBytes(typ, raw)
		case 5:
			m.st2MinQi, err = tablesBlobBytes(typ, raw)
		case 6:
			m.st2MaxQi, err = tablesBlobBytes(typ, raw)
		case 7:
			m.cb16, err = tablesBlobU32s(m.cb16, typ, v, raw)
		case 8:
			m.cinv16, err = tablesBlobU32s(m.cinv16, typ, v, raw)
		case 9:
			m.cmf, err = tablesBlobU32s(m.cmf, typ, v, raw)
		case 10:
			m.cmfCond, err = tablesBlobU32s(m.cmfCond, typ, v, raw)
		case 11:
			m.lsfSel, err = tablesBlobU32s(m.lsfSel, typ, v, raw)
		case 12:
			m.lsfExtra, err = tablesBlobU32s(m.lsfExtra, typ, v, raw)
		case 13:
			m.mean, err = tablesBlobRepeatedFixed32(m.mean, typ, v, raw)
		case 14:
			m.minDist, err = tablesBlobRepeatedFixed32(m.minDist, typ, v, raw)
		case 15:
			m.regCond, err = tablesBlobRepeatedFixed32(m.regCond, typ, v, raw)
		case 16:
			m.qstep, err = tablesBlobRepeatedFixed32(m.qstep, typ, v, raw)
		}
		return err
	})
}

func (m *lsfSeed) encodeToVec() []byte {
	var b []byte
	b = tablesBlobAppendBytes(b, 1, m.rot8)
	b = tablesBlobAppendBytes(b, 2, m.rotCond8)
	b = tablesBlobAppendBytes(b, 3, m.st2AllQlvls8)
	b = tablesBlobAppendBytes(b, 4, m.st2AllQlvlDcmfs)
	b = tablesBlobAppendBytes(b, 5, m.st2MinQi)
	b = tablesBlobAppendBytes(b, 6, m.st2MaxQi)
	b = tablesBlobAppendPackedU32(b, 7, m.cb16)
	b = tablesBlobAppendPackedU32(b, 8, m.cinv16)
	b = tablesBlobAppendPackedU32(b, 9, m.cmf)
	b = tablesBlobAppendPackedU32(b, 10, m.cmfCond)
	b = tablesBlobAppendPackedU32(b, 11, m.lsfSel)
	b = tablesBlobAppendPackedU32(b, 12, m.lsfExtra)
	b = tablesBlobAppendPackedF32(b, 13, m.mean)
	b = tablesBlobAppendPackedF32(b, 14, m.minDist)
	b = tablesBlobAppendPackedF32(b, 15, m.regCond)
	b = tablesBlobAppendPackedF32(b, 16, m.qstep)
	return b
}

// pitchSeed.

func (m *pitchSeed) decodeFromSlice(b []byte) error {
	*m = pitchSeed{}
	return tablesBlobFields(b, func(num protowire.Number, typ protowire.Type, v uint64, raw []byte) error {
		var err error
		switch num {
		case 1:
			m.blocksegsBitstream, err = tablesBlobBytes(typ, raw)
		case 2:
			m.blocksegs2idx, err = tablesBlobBytes(typ, raw)
		case 3:
			m.blocksegsIx, err = tablesBlobBytes(typ, raw)
		case 4:
			m.firstblockRange, err = tablesBlobBytes(typ, raw)
		case 5:
			m.blocksegIdxDcmf, err = tablesBlobBytes(typ, raw)
		case 6:
			m.deltaLagDcmfs, err = tablesBlobBytes(typ, raw)
		case 7:
			m.blockTransitionDcmf, err = tablesBlobBytes(typ, raw)
		}
		return err
	})
}

func (m *pitchSeed) encodeToVec() []byte {
	var b []byte
	b = tablesBlobAppendBytes(b, 1, m.blocksegsBitstream)
	b = tablesBlobAppendBytes(b, 2, m.blocksegs2idx)
	b = tablesBlobAppendBytes(b, 3, m.blocksegsIx)
	b = tablesBlobAppendBytes(b, 4, m.firstblockRange)
	b = tablesBlobAppendBytes(b, 5, m.blocksegIdxDcmf)
	b = tablesBlobAppendBytes(b, 6, m.deltaLagDcmfs)
	b = tablesBlobAppendBytes(b, 7, m.blockTransitionDcmf)
	return b
}

// ccSeed.

func (m *ccSeed) decodeFromSlice(b []byte) error {
	*m = ccSeed{}
	return tablesBlobFields(b, func(num protowire.Number, typ protowire.Type, v uint64, raw []byte) error {
		var err error
		switch num {
		case 1:
			m.nrgresGain4Dcmf, err = tablesBlobBytes(typ, raw)
		case 2:
			m.nrgresShape4Dcmf, err = tablesBlobBytes(typ, raw)
		case 3:
			m.fcbgOffsetDcmf, err = tablesBlobBytes(typ, raw)
		case 4:
			m.acbgainsHrDcmf, err = tablesBlobBytes(typ, raw)
		case 5:
			m.fcbgainsVDcmf, err = tablesBlobBytes(typ, raw)
		case 6:
			m.fcbgainsVDeltaDcmf, err = tablesBlobBytes(typ, raw)
		case 7:
			m.acbgainsCbHrQ14, err = tablesBlobSint32s(m.acbgainsCbHrQ14, typ, v, raw)
		case 8:
			var x uint64
			x, err = tablesBlobVarint(typ, v)
			m.gainReconBase = uint32(x)
		case 9:
			m.gainRecon, err = tablesBlobBytes(typ, raw)
		case 10:
			m.nPulsesDcmfBgn, err = tablesBlobBytes(typ, raw)
		case 11:
			m.nPulsesDcmfUv, err = tablesBlobBytes(typ, raw)
		case 12:
			m.nPulsesDcmfV, err = tablesBlobBytes(typ, raw)
		}
		return err
	})
}

func (m *ccSeed) encodeToVec() []byte {
	var b []byte
	b = tablesBlobAppendBytes(b, 1, m.nrgresGain4Dcmf)
	b = tablesBlobAppendBytes(b, 2, m.nrgresShape4Dcmf)
	b = tablesBlobAppendBytes(b, 3, m.fcbgOffsetDcmf)
	b = tablesBlobAppendBytes(b, 4, m.acbgainsHrDcmf)
	b = tablesBlobAppendBytes(b, 5, m.fcbgainsVDcmf)
	b = tablesBlobAppendBytes(b, 6, m.fcbgainsVDeltaDcmf)
	b = tablesBlobAppendPackedSint32(b, 7, m.acbgainsCbHrQ14)
	b = tablesBlobAppendVarint(b, 8, uint64(m.gainReconBase))
	b = tablesBlobAppendBytes(b, 9, m.gainRecon)
	b = tablesBlobAppendBytes(b, 10, m.nPulsesDcmfBgn)
	b = tablesBlobAppendBytes(b, 11, m.nPulsesDcmfUv)
	b = tablesBlobAppendBytes(b, 12, m.nPulsesDcmfV)
	return b
}

// Compile-time checks that every table message satisfies tablesBlobMessage.
var (
	_ = loadBlobBuffa[heapWindow, *heapWindow]
	_ = loadBlobBuffa[region, *region]
	_ = loadBlobBuffa[lsfSeed, *lsfSeed]
	_ = loadBlobBuffa[pitchSeed, *pitchSeed]
	_ = loadBlobBuffa[ccSeed, *ccSeed]
)
