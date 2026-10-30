// Ported from whatsapp-rust 5e72baf wacore/src/voip/mlow/smpl_mem.rs, MIT.
//
// MLow heap window for the pitch lag/contour reads (`smpl_pitch.rs` Group D), the only consumer
// still addressing the WASM heap by absolute pointer. Groups A/B/C/E moved to the logical
// `ccTables` (`cc_seed.bin`), as did the p6!=0 LR gain/filter/weight tables. The contour window
// proper (pcfg header + per-contour records, lag/frac/delta CDFs, contour_map, delta-lag bounds)
// is BUILT from `pitch_seed.bin` at load (`buildSmplMem`), so nothing of it is stored. Everything
// sits at the original absolute addresses so the existing pointer arithmetic still lands.

package mlow

import (
	_ "embed"
	"encoding/binary"
	"sync"
)

// smplMemRegion is `tables.proto` Region.
type smplMemRegion = region

// smplMem is `tables.proto` HeapWindow; the runtime window built by `buildSmplMem`.
type smplMem = heapWindow

// Fixed WASM-build globals for the Group-D heap layout. The window is built at these absolute
// addresses so `smpl_pitch.rs`'s pointer arithmetic lands unchanged.
const (
	gClk   uint32 = 0xb9f9a8
	gPitch uint32 = 0xb9d378
	pcfg   uint32 = gClk + 0x5704
)

// pcfg header pointers (@pcfg+0x56e0): num_contours, contour_map, lag_cdf, frac_base, then three
// the consumer never reads, then delta_cdf. Fixed addresses for this build.
const (
	hdrContourMap   uint32 = 0xe7c10
	hdrLagCdf       uint32 = 0xbaa7b0
	hdrFracBase     uint32 = 0xbaa9be
	hdrDeltaCdf     uint32 = 0xbab13e
	deltaBoundsAddr uint32 = 0xe7ef0
	numContours            = 217
)

var hdrUnused = [3]uint32{0xe7d20, 0xe7ef0, 0xe8096}

//go:embed testdata/pitch_seed.bin
var smplMemPitchSeedBin []byte

// smplMemU16Bytes is the `u16_bytes` closure: each u32 truncated to u16, little-endian.
func smplMemU16Bytes(v []uint32) []byte {
	out := make([]byte, 0, 2*len(v))
	for _, x := range v {
		out = binary.LittleEndian.AppendUint16(out, uint16(x))
	}
	return out
}

// buildSmplMem builds the pitch lag/contour window from the seed. Reproduces the carved window
// byte-for-byte at every address the consumer reads.
func buildSmplMem() smplMem {
	seed := loadBlobBuffa[pitchSeed](smplMemPitchSeedBin)
	w := seed.buildContourWindow()

	regions := make([]smplMemRegion, 0, 6)
	push := func(base uint32, data []byte) {
		regions = append(regions, smplMemRegion{base: base, data: data})
	}

	// region 0 @pcfg+0x1d38: 217 records (lags[8] | seglens[8] | seg_count, 0x44 each), a 4-byte
	// gap (=NUM_BLOCKTRACKS), then the 8-pointer header.
	r0 := make([]byte, 0, numContours*0x44+4+32)
	for _, rec := range w.records {
		lags, seglens := rec[0], rec[1]
		sc := len(lags)
		for i := 0; i < 8; i++ {
			var x int
			if i < len(lags) {
				x = lags[i]
			}
			r0 = binary.LittleEndian.AppendUint32(r0, uint32(int32(x)))
		}
		for i := 0; i < 8; i++ {
			var x int
			if i < len(seglens) {
				x = seglens[i]
			}
			r0 = binary.LittleEndian.AppendUint32(r0, uint32(int32(x)))
		}
		r0 = binary.LittleEndian.AppendUint32(r0, uint32(int32(sc)))
	}
	r0 = binary.LittleEndian.AppendUint32(r0, 187)
	for _, h := range [8]uint32{
		numContours,
		hdrContourMap,
		hdrLagCdf,
		hdrFracBase,
		hdrUnused[0],
		hdrUnused[1],
		hdrUnused[2],
		hdrDeltaCdf,
	} {
		r0 = binary.LittleEndian.AppendUint32(r0, h)
	}
	push(pcfg+0x1d38, r0)

	// CDF tables (each its own region; the dead inter-table gaps the carve included are unread).
	push(hdrLagCdf, smplMemU16Bytes(w.lagCdf))
	var frac []uint32
	for _, row := range w.fracCmfs {
		frac = append(frac, row...)
	}
	push(hdrFracBase, smplMemU16Bytes(frac))
	var delta []uint32
	for _, row := range w.deltaCmfs {
		delta = append(delta, row...)
	}
	push(hdrDeltaCdf, smplMemU16Bytes(delta))

	// contour_map[217].
	push(hdrContourMap, append([]byte(nil), w.contourMap...))

	// delta-lag bounds: firstblock_range u8 pairs, then the carve's 2 trailing pad bytes.
	bounds := make([]byte, 0, 2*len(w.firstblockRange)+2)
	for _, p := range w.firstblockRange {
		bounds = append(bounds, uint8(p[0]), uint8(p[1]))
	}
	bounds = append(bounds, 0, 0)
	push(deltaBoundsAddr, bounds)

	return smplMem{
		regions: regions,
		gCc:     0,
		gNrg:    0,
		gPitch:  gPitch,
		gClk:    gClk,
	}
}

var smplMemOnce = sync.OnceValue(func() *smplMem {
	m := buildSmplMem()
	return &m
})

func loadSmplMem() *smplMem {
	return smplMemOnce()
}

// regionFor returns the region containing `[addr, addr+n)` and the byte offset of `addr` within
// it; ok is false when no region holds it (Rust `None`).
func (m *heapWindow) regionFor(addr uint32, n int) (data []byte, off int, ok bool) {
	for i := range m.regions {
		r := &m.regions[i]
		if addr >= r.base && int(addr-r.base)+n <= len(r.data) {
			return r.data, int(addr - r.base), true
		}
	}
	return nil, 0, false
}

func (m *heapWindow) u8(addr uint32) uint8 {
	if d, off, ok := m.regionFor(addr, 1); ok {
		return d[off]
	}
	return 0
}

func (m *heapWindow) u16(addr uint32) uint16 {
	if d, off, ok := m.regionFor(addr, 2); ok {
		return binary.LittleEndian.Uint16(d[off:])
	}
	return 0
}

func (m *heapWindow) i16(addr uint32) int16 {
	return int16(m.u16(addr))
}

func (m *heapWindow) u32(addr uint32) uint32 {
	if d, off, ok := m.regionFor(addr, 4); ok {
		return binary.LittleEndian.Uint32(d[off:])
	}
	return 0
}

func (m *heapWindow) i32(addr uint32) int32 {
	return int32(m.u32(addr))
}

// cdfAt materializes the n-entry cumulative u16 CDF at WASM address `addr` (for `decodeCdf`).
// Entries outside the window read as 0, matching the WASM's out-of-region fallback. Allocates,
// like the Rust `Vec<u16>`.
func (m *heapWindow) cdfAt(addr uint32, n int) []uint16 {
	out := make([]uint16, n)
	for i := range out {
		out[i] = m.u16(addr + uint32(i)*2)
	}
	return out
}

// cdfBytes is the raw `[addr, addr+2n)` byte slice when the whole CDF window sits inside one
// region (the common case), so callers can read it in place instead of allocating a `[]uint16`.
// ok is false (Rust `None`) at a region boundary or out of range, where the zero-fill semantics of
// `cdfAt` must apply; callers fall back to `cdfAt` there.
func (m *heapWindow) cdfBytes(addr uint32, n int) ([]byte, bool) {
	data, off, ok := m.regionFor(addr, n*2)
	if !ok {
		return nil, false
	}
	return data[off : off+n*2], true
}
