// Ported from whatsapp-rust 5e72baf wacore/src/voip/mlow/fixture.rs, MIT.
//
// Test-only readers for the oracle fixtures in testdata: the losslessly
// packed `*.cbor.zst` (zstd-compressed CBOR), `*.raw.zst`, and the JSON ones.
// The Rust tests read every fixture into a serde_json::Value; val mirrors the
// accessors they use (index, get, as_f64, as_i64, as_u64, as_array, ...).

package mlow

import (
	"bytes"
	"crypto/sha256"
	"debug/elf"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"os"
	"path/filepath"
	"strconv"
	"testing"
)

// testdataBytes reads testdata/name.
func testdataBytes(t testing.TB, name string) []byte {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("testdata", name))
	if err != nil {
		t.Fatalf("testdata %s: %v", name, err)
	}
	return b
}

// inflateZstd decompresses one zstd frame. The standard library has a zstd
// decoder but keeps it internal; debug/elf exposes it for SHF_COMPRESSED
// sections, so the data is wrapped in a one-section ELF file in memory.
func inflateZstd(packed []byte) ([]byte, error) {
	const (
		ehdrSize = 64
		shdrSize = 64
		chdrSize = 24
	)
	shstr := []byte("\x00.z\x00.shstrtab\x00")
	shstrOff := ehdrSize + 3*shdrSize
	dataOff := shstrOff + len(shstr)
	var f bytes.Buffer
	le := binary.LittleEndian
	w := func(v any) { _ = binary.Write(&f, le, v) }
	// ELF header.
	f.Write([]byte{0x7f, 'E', 'L', 'F', 2, 1, 1, 0, 0, 0, 0, 0, 0, 0, 0, 0})
	w(uint16(elf.ET_REL))
	w(uint16(elf.EM_AARCH64))
	w(uint32(elf.EV_CURRENT))
	w(uint64(0))        // entry
	w(uint64(0))        // phoff
	w(uint64(ehdrSize)) // shoff
	w(uint32(0))        // flags
	w(uint16(ehdrSize))
	w(uint16(0)) // phentsize
	w(uint16(0)) // phnum
	w(uint16(shdrSize))
	w(uint16(3)) // shnum
	w(uint16(2)) // shstrndx
	shdr := func(name, typ uint32, flags, off, size uint64) {
		w(name)
		w(typ)
		w(flags)
		w(uint64(0)) // addr
		w(off)
		w(size)
		w(uint32(0)) // link
		w(uint32(0)) // info
		w(uint64(1)) // addralign
		w(uint64(0)) // entsize
	}
	shdr(0, 0, 0, 0, 0)
	shdr(1, uint32(elf.SHT_PROGBITS), uint64(elf.SHF_COMPRESSED), uint64(dataOff), uint64(chdrSize+len(packed)))
	shdr(4, uint32(elf.SHT_STRTAB), 0, uint64(shstrOff), uint64(len(shstr)))
	f.Write(shstr)
	w(uint32(elf.COMPRESS_ZSTD))
	w(uint32(0))
	w(uint64(math.MaxInt32)) // uncompressed size: unknown, read to EOF instead
	w(uint64(1))
	f.Write(packed)

	file, err := elf.NewFile(bytes.NewReader(f.Bytes()))
	if err != nil {
		return nil, err
	}
	sec := file.Section(".z")
	if sec == nil {
		return nil, errors.New("zstd wrapper: section missing")
	}
	return io.ReadAll(sec.Open())
}

// loadZstd reads and decompresses testdata/name.
func loadZstd(t testing.TB, name string) []byte {
	t.Helper()
	b, err := inflateZstd(testdataBytes(t, name))
	if err != nil {
		t.Fatalf("testdata %s: zstd: %v", name, err)
	}
	return b
}

// loadPacked is fixture::decode: a zstd-packed CBOR fixture as a val.
func loadPacked(t testing.TB, name string) val {
	t.Helper()
	raw := loadZstd(t, name)
	d := cborDecoder{b: raw}
	v, err := d.value()
	if err != nil {
		t.Fatalf("testdata %s: cbor: %v", name, err)
	}
	if d.off != len(raw) {
		t.Fatalf("testdata %s: trailing bytes after the fixture CBOR value", name)
	}
	return val{v}
}

// loadJSON reads testdata/name as a val, keeping numbers exact.
func loadJSON(t testing.TB, name string) val {
	t.Helper()
	return parseJSON(t, testdataBytes(t, name))
}

func parseJSON(t testing.TB, b []byte) val {
	t.Helper()
	dec := json.NewDecoder(bytes.NewReader(b))
	dec.UseNumber()
	var v any
	if err := dec.Decode(&v); err != nil {
		t.Fatalf("json: %v", err)
	}
	return val{v}
}

// cborDecoder reads the subset of CBOR ciborium writes: definite lengths,
// integers, half/single/double floats, strings, arrays, maps, simple values.
type cborDecoder struct {
	b   []byte
	off int
}

var errCBORShort = errors.New("cbor: truncated")

func (d *cborDecoder) head() (major byte, arg uint64, info byte, err error) {
	if d.off >= len(d.b) {
		return 0, 0, 0, errCBORShort
	}
	c := d.b[d.off]
	d.off++
	major, info = c>>5, c&0x1f
	n := 0
	switch {
	case info < 24:
		return major, uint64(info), info, nil
	case info == 24:
		n = 1
	case info == 25:
		n = 2
	case info == 26:
		n = 4
	case info == 27:
		n = 8
	default:
		return 0, 0, 0, fmt.Errorf("cbor: unsupported additional info %d", info)
	}
	if d.off+n > len(d.b) {
		return 0, 0, 0, errCBORShort
	}
	for _, x := range d.b[d.off : d.off+n] {
		arg = arg<<8 | uint64(x)
	}
	d.off += n
	return major, arg, info, nil
}

func (d *cborDecoder) bytesOf(n uint64) ([]byte, error) {
	if n > uint64(len(d.b)-d.off) {
		return nil, errCBORShort
	}
	s := d.b[d.off : d.off+int(n)]
	d.off += int(n)
	return s, nil
}

func (d *cborDecoder) value() (any, error) {
	major, arg, info, err := d.head()
	if err != nil {
		return nil, err
	}
	switch major {
	case 0:
		if arg <= math.MaxInt64 {
			return int64(arg), nil
		}
		return arg, nil
	case 1:
		return -1 - int64(arg), nil
	case 2:
		s, err := d.bytesOf(arg)
		return bytes.Clone(s), err
	case 3:
		s, err := d.bytesOf(arg)
		return string(s), err
	case 4:
		out := make([]any, 0, min(arg, 1<<20))
		for range arg {
			v, err := d.value()
			if err != nil {
				return nil, err
			}
			out = append(out, v)
		}
		return out, nil
	case 5:
		out := make(map[string]any, min(arg, 1<<16))
		for range arg {
			k, err := d.value()
			if err != nil {
				return nil, err
			}
			v, err := d.value()
			if err != nil {
				return nil, err
			}
			out[fmt.Sprint(k)] = v
		}
		return out, nil
	case 6:
		return d.value()
	}
	switch info {
	case 20:
		return false, nil
	case 21:
		return true, nil
	case 22, 23:
		return nil, nil
	case 25:
		return halfToFloat(uint16(arg)), nil
	case 26:
		return float64(math.Float32frombits(uint32(arg))), nil
	case 27:
		return math.Float64frombits(arg), nil
	}
	return nil, fmt.Errorf("cbor: unsupported simple value %d", info)
}

func halfToFloat(h uint16) float64 {
	exp := int(h>>10) & 0x1f
	mant := float64(h & 0x3ff)
	var v float64
	switch exp {
	case 0:
		v = math.Ldexp(mant, -24)
	case 31:
		if mant == 0 {
			v = math.Inf(1)
		} else {
			v = math.NaN()
		}
	default:
		v = math.Ldexp(mant+1024, exp-25)
	}
	if h&0x8000 != 0 {
		v = -v
	}
	return v
}

// val is a decoded JSON or CBOR value with serde_json::Value's accessors.
// A missing key or index yields the null val, as serde_json's indexing does.
type val struct{ v any }

// Get is value["key"].
func (x val) Get(key string) val {
	if m, ok := x.v.(map[string]any); ok {
		return val{m[key]}
	}
	return val{}
}

// Has reports whether an object has the key (Value::get(...).is_some()).
func (x val) Has(key string) bool {
	m, ok := x.v.(map[string]any)
	if !ok {
		return false
	}
	_, ok = m[key]
	return ok
}

// At is value[i].
func (x val) At(i int) val {
	if a, ok := x.v.([]any); ok && i >= 0 && i < len(a) {
		return val{a[i]}
	}
	return val{}
}

// Len is the length of an array or object (0 otherwise).
func (x val) Len() int {
	switch v := x.v.(type) {
	case []any:
		return len(v)
	case map[string]any:
		return len(v)
	case []byte:
		return len(v)
	}
	return 0
}

func (x val) IsNull() bool { return x.v == nil }

// Arr is as_array; it panics (like unwrap) on a non-array.
func (x val) Arr() []val {
	switch v := x.v.(type) {
	case []any:
		out := make([]val, len(v))
		for i, e := range v {
			out[i] = val{e}
		}
		return out
	case []byte:
		out := make([]val, len(v))
		for i, e := range v {
			out[i] = val{int64(e)}
		}
		return out
	}
	panic(fmt.Sprintf("fixture: not an array: %T", x.v))
}

// Keys returns an object's keys (unordered).
func (x val) Keys() []string {
	m, _ := x.v.(map[string]any)
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}

// F64OK is as_f64: any number, as f64.
func (x val) F64OK() (float64, bool) {
	switch v := x.v.(type) {
	case float64:
		return v, true
	case int64:
		return float64(v), true
	case uint64:
		return float64(v), true
	case json.Number:
		f, err := strconv.ParseFloat(string(v), 64)
		return f, err == nil
	}
	return 0, false
}

func (x val) F64() float64 {
	f, ok := x.F64OK()
	if !ok {
		panic(fmt.Sprintf("fixture: not a number: %#v", x.v))
	}
	return f
}

func (x val) F32() float32 { return float32(x.F64()) }

// I64OK is as_i64: an integer that fits i64.
func (x val) I64OK() (int64, bool) {
	switch v := x.v.(type) {
	case int64:
		return v, true
	case json.Number:
		i, err := strconv.ParseInt(string(v), 10, 64)
		return i, err == nil
	}
	return 0, false
}

func (x val) I64() int64 {
	i, ok := x.I64OK()
	if !ok {
		panic(fmt.Sprintf("fixture: not an i64: %#v", x.v))
	}
	return i
}

// U64OK is as_u64: a non-negative integer.
func (x val) U64OK() (uint64, bool) {
	switch v := x.v.(type) {
	case int64:
		return uint64(v), v >= 0
	case uint64:
		return v, true
	case json.Number:
		u, err := strconv.ParseUint(string(v), 10, 64)
		return u, err == nil
	}
	return 0, false
}

func (x val) U64() uint64 {
	u, ok := x.U64OK()
	if !ok {
		panic(fmt.Sprintf("fixture: not a u64: %#v", x.v))
	}
	return u
}

func (x val) Int() int { return int(x.I64()) }

func (x val) BoolOK() (bool, bool) {
	b, ok := x.v.(bool)
	return b, ok
}

func (x val) Bool() bool {
	b, ok := x.v.(bool)
	if !ok {
		panic(fmt.Sprintf("fixture: not a bool: %#v", x.v))
	}
	return b
}

func (x val) StrOK() (string, bool) {
	s, ok := x.v.(string)
	return s, ok
}

func (x val) Str() string {
	s, ok := x.v.(string)
	if !ok {
		panic(fmt.Sprintf("fixture: not a string: %#v", x.v))
	}
	return s
}

// F32s reads an array of numbers as []float32.
func (x val) F32s() []float32 {
	a := x.Arr()
	out := make([]float32, len(a))
	for i, e := range a {
		out[i] = e.F32()
	}
	return out
}

// F64s reads an array of numbers as []float64.
func (x val) F64s() []float64 {
	a := x.Arr()
	out := make([]float64, len(a))
	for i, e := range a {
		out[i] = e.F64()
	}
	return out
}

// I64s reads an array of integers as []int64.
func (x val) I64s() []int64 {
	a := x.Arr()
	out := make([]int64, len(a))
	for i, e := range a {
		out[i] = e.I64()
	}
	return out
}

// Ints reads an array of integers as []int.
func (x val) Ints() []int {
	a := x.Arr()
	out := make([]int, len(a))
	for i, e := range a {
		out[i] = e.Int()
	}
	return out
}

// I32s reads an array of integers as []int32.
func (x val) I32s() []int32 {
	a := x.Arr()
	out := make([]int32, len(a))
	for i, e := range a {
		out[i] = int32(e.I64())
	}
	return out
}

// U8s reads a byte string, or an array of small integers, as []byte.
func (x val) U8s() []byte {
	if b, ok := x.v.([]byte); ok {
		return b
	}
	a := x.Arr()
	out := make([]byte, len(a))
	for i, e := range a {
		out[i] = byte(e.I64())
	}
	return out
}

// Hex decodes a hex string field (the packet fixtures store frames as hex).
func (x val) Hex() []byte {
	b, err := hexDecode(x.Str())
	if err != nil {
		panic(err)
	}
	return b
}

func hexDecode(s string) ([]byte, error) {
	if len(s)%2 != 0 {
		return nil, fmt.Errorf("hex: odd length %d", len(s))
	}
	out := make([]byte, len(s)/2)
	for i := range out {
		v, err := strconv.ParseUint(s[2*i:2*i+2], 16, 8)
		if err != nil {
			return nil, err
		}
		out[i] = byte(v)
	}
	return out, nil
}

// s16le reads little-endian 16-bit PCM.
func s16le(b []byte) []int16 {
	out := make([]int16, len(b)/2)
	for i := range out {
		out[i] = int16(binary.LittleEndian.Uint16(b[2*i:]))
	}
	return out
}

// f32le reads little-endian f32 samples.
func f32le(b []byte) []float32 {
	out := make([]float32, len(b)/4)
	for i := range out {
		out[i] = math.Float32frombits(binary.LittleEndian.Uint32(b[4*i:]))
	}
	return out
}

// TestFixturesMatchManifests checks every packed and raw fixture against the
// SHA-256s its manifest records, which also proves the zstd path decodes
// exactly.
func TestFixturesMatchManifests(t *testing.T) {
	for _, manifest := range []string{"packed-fixtures.json", "wasm-fixtures.json"} {
		m := loadJSON(t, manifest)
		if m.Has("files") {
			m = m.Get("files")
		}
		for _, name := range m.Keys() {
			entry := m.Get(name)
			packed := testdataBytes(t, name)
			if want, ok := entry.Get("zstd_sha256").StrOK(); ok {
				if got := fmt.Sprintf("%x", sha256.Sum256(packed)); got != want {
					t.Errorf("%s: packed sha256 %s, manifest %s", name, got, want)
				}
				want, ok := entry.Get("cbor_sha256").StrOK()
				if !ok {
					want = entry.Get("raw_sha256").Str()
				}
				if got := fmt.Sprintf("%x", sha256.Sum256(loadZstd(t, name))); got != want {
					t.Errorf("%s: inflated sha256 %s, manifest %s", name, got, want)
				}
			} else if want, ok := entry.Get("sha256").StrOK(); ok {
				if got := fmt.Sprintf("%x", sha256.Sum256(packed)); got != want {
					t.Errorf("%s: sha256 %s, manifest %s", name, got, want)
				}
			}
		}
	}
}

func TestFixturesDecode(t *testing.T) {
	entries, err := os.ReadDir("testdata")
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		name := e.Name()
		switch filepath.Ext(name) {
		case ".zst":
			raw := loadZstd(t, name)
			if filepath.Ext(name[:len(name)-4]) == ".cbor" {
				d := cborDecoder{b: raw}
				if _, err := d.value(); err != nil || d.off != len(raw) {
					t.Errorf("%s: cbor: %v (at %d of %d)", name, err, d.off, len(raw))
				}
			}
		case ".json":
			_ = loadJSON(t, name)
		}
	}
}
