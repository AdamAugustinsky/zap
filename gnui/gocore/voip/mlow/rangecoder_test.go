// Ported from whatsapp-rust 5e72baf wacore/src/voip/mlow/rangecoder.rs, MIT.

package mlow

import (
	"bytes"
	"testing"
)

// oracleState is the ec_dec words after the buffer pointer, for comparison with guest snapshots.
func (d *rangeDecoder) oracleState() [11]uint32 {
	return [11]uint32{
		d.storageLen,
		d.endOffs,
		d.endWindow,
		uint32(d.nendBits),
		uint32(d.nbitsTotal),
		d.offs,
		d.rng,
		d.val,
		d.ext,
		uint32(d.rem),
		uint32(d.err),
	}
}

func rangecoderVectors(t *testing.T) val {
	t.Helper()
	return loadPacked(t, "rc_vectors.cbor.zst")
}

func rangecoderCdfTables(v val) [][]uint16 {
	var tables [][]uint16
	for _, tv := range v.Get("cdfTables").Arr() {
		var row []uint16
		for _, x := range tv.Arr() {
			row = append(row, uint16(x.U64()))
		}
		tables = append(tables, row)
	}
	return tables
}

// Replays a deterministic mixed script (icdf / raw back-bits / bit_logp / uint) from the
// captured vectors, requiring identical decoded values; proves ec_dec parity bit-for-bit.
func TestRangecoder_RangeDecoderMatchesGoVectors(t *testing.T) {
	v := rangecoderVectors(t)
	// The dumped `icdf` is the fixed table the captured script encoded against (ftb=8).
	icdf := [6]uint8{255, 200, 150, 90, 30, 0}
	b := v.Get("bytesHex").Hex()
	d := newRangeDecoder(b)
	for i, op := range v.Get("ops").Arr() {
		kind := op.Get("kind").U64()
		a := uint32(op.Get("a").U64())
		bb := uint32(op.Get("b").U64())
		switch kind {
		case 0:
			if got := d.decodeIcdf(icdf[:], 8); got != int32(a) {
				t.Fatalf("op %d icdf: got %d want %d", i, got, a)
			}
		case 1:
			if got := d.bitsN(a); got != bb {
				t.Fatalf("op %d bits(%d): got %d want %d", i, a, got, bb)
			}
		case 2:
			if got := uint32(d.bitLogp(a)); got != bb {
				t.Fatalf("op %d bit_logp(%d): got %d want %d", i, a, got, bb)
			}
		case 3:
			if got := d.decodeUint(a); got != bb {
				t.Fatalf("op %d uint(ft=%d): got %d want %d", i, a, got, bb)
			}
		default:
			t.Fatalf("bad op kind %d", kind)
		}
	}
	if d.err != 0 {
		t.Fatalf("no decode error: err=%d", d.err)
	}
}

// Exercises decodeCDF against cumulative tables (including non-zero-base ones).
func TestRangecoder_RangeDecoderCdfMatchesGoVectors(t *testing.T) {
	v := rangecoderVectors(t)
	tables := rangecoderCdfTables(v)
	b := v.Get("cdfBytesHex").Hex()
	d := newRangeDecoder(b)
	for i, op := range v.Get("cdfOps").Arr() {
		ti := int(op.Get("kind").U64())
		sym := int32(op.Get("a").U64())
		if got := d.decodeCdf(tables[ti]); got != sym {
			t.Fatalf("cdf op %d table %d: got %d want %d", i, ti, got, sym)
		}
	}
	if d.err != 0 {
		t.Fatalf("no decode error: err=%d", d.err)
	}
}

// Re-encodes the captured script and requires byte-identical output; proves our ec_enc matches
// the WASM range encoder bit-for-bit (the foundation of the mlow encoder).
func TestRangecoder_RangeEncoderMatchesGoBytes(t *testing.T) {
	v := rangecoderVectors(t)
	icdf := [6]uint8{255, 200, 150, 90, 30, 0}
	want := v.Get("bytesHex").Hex()
	e := newRangeEncoder(len(want))
	for _, op := range v.Get("ops").Arr() {
		kind := op.Get("kind").U64()
		a := uint32(op.Get("a").U64())
		b := uint32(op.Get("b").U64())
		switch kind {
		case 0:
			e.encodeIcdf(int32(a), icdf[:], 8)
		case 1:
			e.bitsN(b, a)
		case 2:
			e.bitLogp(int32(b), a)
		case 3:
			e.encodeUint(b, a)
		default:
			t.Fatalf("unreachable op kind %d", kind)
		}
	}
	e.done()
	if e.err() != 0 {
		t.Fatalf("encoder error: %d", e.err())
	}
	if !bytes.Equal(e.bytes(), want) {
		t.Fatalf("encoder output differs from captured vectors")
	}
}

type rangecoderOpKind uint8

const (
	rangecoderOpKindIcdf rangecoderOpKind = iota
	rangecoderOpKindCdf0
	rangecoderOpKindCdf1
	rangecoderOpKindBits
	rangecoderOpKindRawSym
	rangecoderOpKindFine
	rangecoderOpKindUint
)

// rangecoderOp is the test's data-carrying `Op` enum: `s` is the symbol of Icdf/Cdf0/Cdf1/Fine,
// (`a`, `b`) are (fl, n) for Bits, (sym, nbits) for RawSym and (fl, ft0) for Uint.
type rangecoderOp struct {
	kind rangecoderOpKind
	s    int32
	a, b uint32
}

// Inverse property over a long LCG-random script mixing every primitive (icdf / cdf with zero
// and non-zero base / raw back-bits / range-stream raw symbol / 64-fine / uint small+large): a
// rangeEncoder run then a rangeDecoder run must recover every original symbol with err==0. A
// broad self-contained complement to the fixed captured vectors that needs no external data.
func TestRangecoder_RangecoderRoundTripsRandomScripts(t *testing.T) {
	// ftb=8 icdf (strictly decreasing, terminating 0): symbols 0..=4.
	icdf := [6]uint8{255, 200, 150, 90, 30, 0}
	// Cumulative u16 CDFs: a zero-base one (symbols 0..=3) and a non-zero-base one (0..=2).
	cdf0 := [5]uint16{0, 3, 7, 12, 20}
	cdf1 := [4]uint16{5, 8, 14, 30}

	// Build one deterministic script of mixed ops from an LCG seed.
	build := func(seed0 uint32) []rangecoderOp {
		s := seed0
		next := func() uint32 {
			s = s*1664525 + 1013904223
			return s
		}
		ops := make([]rangecoderOp, 0, 256)
		for range 256 {
			r := next()
			var op rangecoderOp
			switch r % 7 {
			case 0:
				op = rangecoderOp{kind: rangecoderOpKindIcdf, s: int32(r>>8) % 5}
			case 1:
				op = rangecoderOp{kind: rangecoderOpKindCdf0, s: int32(r>>8) % 4}
			case 2:
				op = rangecoderOp{kind: rangecoderOpKindCdf1, s: int32(r>>8) % 3}
			case 3:
				n := 1 + (r>>8)%16 // 1..=16 raw back-bits
				op = rangecoderOp{kind: rangecoderOpKindBits, a: next() & ((uint32(1) << n) - 1), b: n}
			case 4:
				nbits := 1 + (r>>8)%6 // range-stream uniform symbol
				op = rangecoderOp{kind: rangecoderOpKindRawSym, a: next() & ((uint32(1) << nbits) - 1), b: nbits}
			case 5:
				op = rangecoderOp{kind: rangecoderOpKindFine, s: int32(r>>8) % 64} // 64-symbol uniform: 0..=63
			default:
				// Alternate small (ftb<=8) and large (ftb>8) uint branches.
				var ft0 uint32 = 2000
				if r&1 == 0 {
					ft0 = 200
				}
				op = rangecoderOp{kind: rangecoderOpKindUint, a: next() % ft0, b: ft0}
			}
			ops = append(ops, op)
		}
		return ops
	}

	for _, seed := range []uint32{0x1357_9bdf, 0x2468_ace0, 0xdead_beef, 0x0badf00d} {
		ops := build(seed)
		e := newRangeEncoder(4096)
		for _, op := range ops {
			switch op.kind {
			case rangecoderOpKindIcdf:
				e.encodeIcdf(op.s, icdf[:], 8)
			case rangecoderOpKindCdf0:
				e.encodeCdf(op.s, cdf0[:])
			case rangecoderOpKindCdf1:
				e.encodeCdf(op.s, cdf1[:])
			case rangecoderOpKindBits:
				e.bitsN(op.a, op.b)
			case rangecoderOpKindRawSym:
				e.encodeRawSymbol(op.a, op.b)
			case rangecoderOpKindFine:
				e.encode64FineSym(op.s)
			case rangecoderOpKindUint:
				e.encodeUint(op.a, op.b)
			}
		}
		e.done()
		if e.err() != 0 {
			t.Fatalf("seed %#x: encoder error", seed)
		}
		// Decode the full buffer: the encoder lays raw back-bits at the physical end of `storage`,
		// so the decoder must see the same storage size to mirror the front/back read offsets.
		// Truncating to `consumedLen()` is only valid when no back-bit op is used.
		b := append([]byte(nil), e.bytes()...)

		d := newRangeDecoder(b)
		for i, op := range ops {
			var got, want int64
			switch op.kind {
			case rangecoderOpKindIcdf:
				got, want = int64(d.decodeIcdf(icdf[:], 8)), int64(op.s)
			case rangecoderOpKindCdf0:
				got, want = int64(d.decodeCdf(cdf0[:])), int64(op.s)
			case rangecoderOpKindCdf1:
				got, want = int64(d.decodeCdf(cdf1[:])), int64(op.s)
			case rangecoderOpKindBits:
				got, want = int64(d.bitsN(op.b)), int64(op.a)
			case rangecoderOpKindRawSym:
				got, want = int64(d.decodeRawSymbol(op.b)), int64(op.a)
			case rangecoderOpKindFine:
				got, want = int64(d.decode64FineSym()), int64(op.s)
			case rangecoderOpKindUint:
				got, want = int64(d.decodeUint(op.b)), int64(op.a)
			}
			if got != want {
				t.Fatalf("seed %#x op %d: got %d want %d", seed, i, got, want)
			}
		}
		if d.err != 0 {
			t.Fatalf("seed %#x: decoder error", seed)
		}
	}
}

func TestRangecoder_RangeEncoderCdfMatchesGoBytes(t *testing.T) {
	v := rangecoderVectors(t)
	tables := rangecoderCdfTables(v)
	want := v.Get("cdfBytesHex").Hex()
	e := newRangeEncoder(len(want))
	for _, op := range v.Get("cdfOps").Arr() {
		ti := int(op.Get("kind").U64())
		sym := int32(op.Get("a").U64())
		e.encodeCdf(sym, tables[ti])
	}
	e.done()
	if e.err() != 0 {
		t.Fatalf("encoder error: %d", e.err())
	}
	if !bytes.Equal(e.bytes(), want) {
		t.Fatalf("cdf encoder output differs from captured vectors")
	}
}
