// Package abi holds the records that cross Zap's core ABI, generated from
// zf.schema (abi_gen.go), and their encoding. It is pure Go: no FFI.
//
// Decoding allocates nothing once warm: every byte field is a sub-slice of
// the record being decoded (valid while that buffer is unchanged), and lists
// reuse the capacity the destination already has. Encoding appends to a
// caller's buffer and allocates only when that buffer must grow.
package abi

import (
	"encoding/binary"
	"errors"
	"math"
	"unicode/utf8"
)

// Decoding and encoding errors. They are values, so returning one allocates
// nothing.
var (
	ErrShort    = errors.New("abi: record ends early")
	ErrTrailing = errors.New("abi: bytes after the record")
	ErrBound    = errors.New("abi: a length or count over its bound")
	ErrBadTag   = errors.New("abi: unknown variant tag")
	ErrBadEnum  = errors.New("abi: unknown enum value")
	ErrBadFlag  = errors.New("abi: a flag that is neither 0 nor 1")
	ErrUTF8     = errors.New("abi: invalid UTF-8")
	ErrReserved = errors.New("abi: reserved header bits set")
	ErrTooLarge = errors.New("abi: a field over its bound")
	ErrFrame    = errors.New("abi: a frame length over MaxRecord")
)

// BlobRef names bytes the core holds: read them with zf_blob_read and free
// them with zf_blob_free.
type BlobRef struct {
	ID  uint64
	Len uint64
}

// FrameHeader is the length prefix zf_drain writes before each record.
const FrameHeader = 4

// NextFrame splits the first record off a zf_drain buffer. A length over
// MaxRecord is ErrFrame: the stream cannot be resynchronised, so the core
// must be treated as failed.
func NextFrame(buf []byte) (record, rest []byte, err error) {
	if len(buf) < FrameHeader {
		return nil, nil, ErrShort
	}
	n := binary.LittleEndian.Uint32(buf)
	if n > MaxRecord {
		return nil, nil, ErrFrame
	}
	if uint64(len(buf)-FrameHeader) < uint64(n) {
		return nil, nil, ErrShort
	}
	end := FrameHeader + int(n)
	return buf[FrameHeader:end], buf[end:], nil
}

// AppendFrame appends a length prefix and record, as zf_drain lays them out.
func AppendFrame(dst, record []byte) []byte {
	dst = binary.LittleEndian.AppendUint32(dst, uint32(len(record)))
	return append(dst, record...)
}

// grow returns s with length n, keeping its elements (and so their own
// slices) when it has the capacity. It grows as append does, so a count
// that creeps up by one (a chat's page as messages arrive) does not
// allocate at every decode.
func grow[T any](s []T, n int) []T {
	if cap(s) >= n {
		return s[:n]
	}
	return append(s[:cap(s)], make([]T, n-cap(s))...)
}

type reader struct {
	b   []byte
	off int
	err error
}

func (r *reader) fail(err error) {
	if r.err == nil {
		r.err = err
	}
}

func (r *reader) finish() error {
	if r.err == nil && r.off != len(r.b) {
		r.err = ErrTrailing
	}
	return r.err
}

func (r *reader) take(n int) []byte {
	if r.err != nil {
		return nil
	}
	if len(r.b)-r.off < n {
		r.fail(ErrShort)
		return nil
	}
	b := r.b[r.off : r.off+n : r.off+n]
	r.off += n
	return b
}

func (r *reader) u8() uint8 {
	b := r.take(1)
	if b == nil {
		return 0
	}
	return b[0]
}

func (r *reader) flag() bool {
	switch r.u8() {
	case 0:
		return false
	case 1:
		return true
	}
	r.fail(ErrBadFlag)
	return false
}

func (r *reader) u16() uint16 {
	b := r.take(2)
	if b == nil {
		return 0
	}
	return binary.LittleEndian.Uint16(b)
}

func (r *reader) u32() uint32 {
	b := r.take(4)
	if b == nil {
		return 0
	}
	return binary.LittleEndian.Uint32(b)
}

func (r *reader) u64() uint64 {
	b := r.take(8)
	if b == nil {
		return 0
	}
	return binary.LittleEndian.Uint64(b)
}

func (r *reader) i32() int32   { return int32(r.u32()) }
func (r *reader) i64() int64   { return int64(r.u64()) }
func (r *reader) f32() float32 { return math.Float32frombits(r.u32()) }
func (r *reader) f64() float64 { return math.Float64frombits(r.u64()) }

// count reads an element count: at most max, and no more than the remaining
// bytes hold at minSize each, so a hostile count cannot make grow allocate.
func (r *reader) count(max, minSize int) int {
	n := r.u32()
	if r.err != nil {
		return 0
	}
	if uint64(n) > uint64(max) {
		r.fail(ErrBound)
		return 0
	}
	if minSize < 1 {
		minSize = 1
	}
	if uint64(n)*uint64(minSize) > uint64(len(r.b)-r.off) {
		r.fail(ErrShort)
		return 0
	}
	return int(n)
}

// budget fails a chunked list whose elements, read from start, took more
// than budget bytes: the encoder would not have written it.
func (r *reader) budget(start, budget int) {
	if r.err == nil && r.off-start > budget {
		r.fail(ErrBound)
	}
}

func (r *reader) bytes(max int) []byte {
	n := r.count(max, 1)
	return r.take(n)
}

func (r *reader) str(max int) []byte {
	b := r.bytes(max)
	if r.err == nil && !utf8.Valid(b) {
		r.fail(ErrUTF8)
	}
	return b
}

func (r *reader) blob() BlobRef {
	return BlobRef{ID: r.u64(), Len: r.u64()}
}

type writer struct {
	b   []byte
	err error
}

func (w *writer) fail(err error) {
	if w.err == nil {
		w.err = err
	}
}

func (w *writer) u8(v uint8) { w.b = append(w.b, v) }

func (w *writer) bool(v bool) {
	if v {
		w.b = append(w.b, 1)
	} else {
		w.b = append(w.b, 0)
	}
}

func (w *writer) u16(v uint16)  { w.b = binary.LittleEndian.AppendUint16(w.b, v) }
func (w *writer) u32(v uint32)  { w.b = binary.LittleEndian.AppendUint32(w.b, v) }
func (w *writer) u64(v uint64)  { w.b = binary.LittleEndian.AppendUint64(w.b, v) }
func (w *writer) i32(v int32)   { w.u32(uint32(v)) }
func (w *writer) i64(v int64)   { w.u64(uint64(v)) }
func (w *writer) f32(v float32) { w.u32(math.Float32bits(v)) }
func (w *writer) f64(v float64) { w.u64(math.Float64bits(v)) }

func (w *writer) count(n, max int) {
	if n > max {
		w.fail(ErrTooLarge)
	}
	w.u32(uint32(n))
}

func (w *writer) bytes(v []byte, max int) {
	w.count(len(v), max)
	w.b = append(w.b, v...)
}

func (w *writer) str(v []byte, max int) {
	if !utf8.Valid(v) {
		w.fail(ErrUTF8)
	}
	w.bytes(v, max)
}

func (w *writer) blob(v BlobRef) {
	w.u64(v.ID)
	w.u64(v.Len)
}

// budget refuses a chunked list whose elements, written from start, exceed
// limit bytes.
func (w *writer) budget(start, limit int) {
	if len(w.b)-start > limit {
		w.fail(ErrTooLarge)
	}
}
