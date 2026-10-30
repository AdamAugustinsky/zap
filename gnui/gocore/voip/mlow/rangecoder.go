// Ported from whatsapp-rust 5e72baf wacore/src/voip/mlow/rangecoder.rs, MIT.
//
// Opus/CELT range decoder (`ec_dec`, RFC 6716 §4.1), the entropy coder MLow's smpl_audio_codec
// reuses verbatim. The symbols must match the WhatsApp WASM bit-for-bit. Range-coded symbols come
// from the front of the buffer, raw bits from the back. `wrapping_*` is used wherever uint32
// modular arithmetic is required (Go's uint32 arithmetic always wraps).
//
// This is a COMPLETE ec_dec implementation: the raw-bits / icdf / uint primitives are validated by
// the round-trip vector test but the mlow decode path itself only exercises a subset, so the unused
// primitives are allowed rather than removed (keeping the entropy coder faithful and reusable).
//
// Go deviations: a Go type cannot have a field and a method of the same name, so the decoder's
// `storage` field is `storageLen` (method `storage()` kept) and the encoder's `err` field is
// `errCode` (method `err()` kept). The decoder's `err` field keeps its name.

package mlow

const (
	ecSymBits    uint32 = 8
	ecCodeBits   uint32 = 32
	ecSymMax     uint32 = (1 << ecSymBits) - 1 // 255
	ecCodeTop    uint32 = uint32(1) << (ecCodeBits - 1)
	ecCodeBot    uint32 = ecCodeTop >> ecSymBits
	ecCodeExtra  uint32 = (ecCodeBits-2)%ecSymBits + 1 // 7
	ecWindowSize uint32 = 32
	ecUintBits   int32  = 8
	ecCodeShift  uint32 = ecCodeBits - ecSymBits - 1 // 23
)

// ilog is EC_ILOG: floor(log2(x))+1 for x>0, 0 for x==0.
func ilog(x uint32) int32 {
	return int32(ecCodeBits - uint32(leadingZeros32(x)))
}

// rangecoderShl1 is Rust's `1u32 << n` in a release build (shift amount masked to 5 bits).
func rangecoderShl1(n uint32) uint32 {
	return uint32(1) << (n & 31)
}

type rangeDecoder struct {
	buf        []byte
	storageLen uint32
	endOffs    uint32
	endWindow  uint32
	nendBits   int32
	nbitsTotal int32
	offs       uint32
	rng        uint32
	val        uint32
	ext        uint32
	rem        int32
	// Sticky decode error (degenerate/malformed table or exhausted bits). Inspectable so the
	// higher layers can fail loud instead of synthesizing from garbage.
	err int32
}

// newRangeDecoder is RFC 6716 `ec_dec_init`.
func newRangeDecoder(buf []byte) rangeDecoder {
	d := rangeDecoder{
		buf:        buf,
		storageLen: uint32(len(buf)),
		endOffs:    0,
		endWindow:  0,
		nendBits:   0,
		nbitsTotal: int32(ecCodeBits) + 1 -
			int32(((ecCodeBits-ecCodeExtra)/ecSymBits)*ecSymBits),
		offs: 0,
		rng:  uint32(1) << ecCodeExtra,
		val:  0,
		ext:  0,
		rem:  0,
		err:  0,
	}
	d.rem = int32(d.readByte())
	d.val = d.rng - 1 - uint32(d.rem>>(ecSymBits-ecCodeExtra))
	d.normalize()
	return d
}

func (d *rangeDecoder) readByte() uint32 {
	if d.offs < d.storageLen {
		b := d.buf[d.offs]
		d.offs++
		return uint32(b)
	}
	return 0
}

func (d *rangeDecoder) readByteFromEnd() uint32 {
	if d.endOffs < d.storageLen {
		d.endOffs++
		return uint32(d.buf[d.storageLen-d.endOffs])
	}
	return 0
}

func (d *rangeDecoder) normalize() {
	for d.rng <= ecCodeBot {
		d.nbitsTotal += int32(ecSymBits)
		d.rng <<= ecSymBits
		sym0 := d.rem
		d.rem = int32(d.readByte())
		sym := ((sym0 << ecSymBits) | d.rem) >> (ecSymBits - ecCodeExtra)
		d.val = ((d.val << ecSymBits) + (ecSymMax & ^uint32(sym))) & (ecCodeTop - 1)
	}
}

// decode returns the cumulative frequency in [0, ft) for the next symbol; caller locates the
// symbol and calls `update`.
func (d *rangeDecoder) decode(ft uint32) uint32 {
	if ft == 0 {
		d.err = 1
		d.ext = 1
		return 0
	}
	d.ext = d.rng / ft
	if d.ext == 0 {
		d.err = 1
		d.ext = 1
		return 0
	}
	s := d.val / d.ext
	return ft - min(s+1, ft)
}

func (d *rangeDecoder) decodeBin(bitsN uint32) uint32 {
	d.ext = d.rng >> bitsN
	if d.ext == 0 {
		d.err = 1
		d.ext = 1
		return 0
	}
	s := d.val / d.ext
	ft := rangecoderShl1(bitsN)
	return ft - min(s+1, ft)
}

// decodeRawSymbol is a uniform `nbits`-bit symbol decoded directly off the range stream (sign
// coder).
func (d *rangeDecoder) decodeRawSymbol(nbits uint32) uint32 {
	sym := d.decodeBin(nbits)
	d.update(sym, sym+1, rangecoderShl1(nbits))
	return sym
}

// update advances past the symbol with cumulative range [fl,fh) out of ft.
func (d *rangeDecoder) update(fl, fh, ft uint32) {
	s := d.ext * (ft - fh)
	d.val = d.val - s
	if fl > 0 {
		d.rng = d.ext * (fh - fl)
	} else {
		d.rng = d.rng - s
	}
	d.normalize()
}

// bitLogp decodes one bit with P(0) = 1/2^logp (`ec_dec_bit_logp`).
func (d *rangeDecoder) bitLogp(logp uint32) int32 {
	r := d.rng
	dv := d.val
	s := r >> logp
	var ret int32
	if dv < s {
		ret = 1
	}
	if ret == 0 {
		d.val = dv - s
		d.rng = r - s
	} else {
		d.rng = s
	}
	d.normalize()
	return ret
}

// decodeIcdf decodes a symbol against an inverse-CDF table (`ec_dec_icdf`); `ftb = log2(ft)`.
func (d *rangeDecoder) decodeIcdf(icdf []uint8, ftb uint32) int32 {
	if len(icdf) == 0 {
		d.err = 1
		return 0
	}
	s0 := d.rng
	dv := d.val
	r := s0 >> ftb
	var ret int32 = -1
	var t uint32
	s := s0
	for {
		t = s
		ret++
		s = r * uint32(icdf[ret])
		if dv >= s || int(ret) >= len(icdf)-1 {
			break
		}
	}
	d.val = dv - s
	d.rng = t - s
	d.normalize()
	return ret
}

// decodeCdf decodes a symbol against a u16 CUMULATIVE CDF table (the smpl primitive). Effective
// total is `cdf[n-1] - cdf[0]` (a non-zero base is subtracted out).
func (d *rangeDecoder) decodeCdf(cdf []uint16) int32 {
	n := len(cdf)
	if n < 2 {
		d.err = 1
		return 0
	}
	base := uint32(cdf[0])
	if uint32(cdf[n-1]) <= base {
		d.err = 1
		return 0
	}
	ft := uint32(cdf[n-1]) - base
	fs := d.decode(ft)
	target := base + fs
	k := 0
	for k < n-1 {
		if uint32(cdf[k+1]) > target {
			break
		}
		k++
	}
	d.update(uint32(cdf[k])-base, uint32(cdf[k+1])-base, ft)
	return int32(k)
}

// rangecoderAt is the `base.get(start + i).copied().unwrap_or(0) as u32` closure of the window
// readers.
func rangecoderAt(base []uint16, start, i int) uint32 {
	j := start + i
	if j >= 0 && j < len(base) {
		return uint32(base[j])
	}
	return 0
}

// decodeCdfWindow is `decodeCdf` against a length-`n` window of `base` starting at `start`,
// without materializing the window. Out-of-range positions read 0 (the old `cdf_window`
// zero-fill), so the math is identical to `decodeCdf(cdfWindow(base, start, n))`.
func (d *rangeDecoder) decodeCdfWindow(base []uint16, start, n int) int32 {
	if n < 2 {
		d.err = 1
		return 0
	}
	cbase := rangecoderAt(base, start, 0)
	last := rangecoderAt(base, start, n-1)
	if last <= cbase {
		d.err = 1
		return 0
	}
	ft := last - cbase
	fs := d.decode(ft)
	target := cbase + fs
	k := 0
	for k < n-1 {
		if rangecoderAt(base, start, k+1) > target {
			break
		}
		k++
	}
	d.update(rangecoderAt(base, start, k)-cbase, rangecoderAt(base, start, k+1)-cbase, ft)
	return int32(k)
}

// decodeCdfLe16 is `decodeCdf` reading the n-entry u16 CDF directly from a little-endian byte
// slice (`len(bytes) == 2*n`), avoiding a `[]uint16` for the in-region heap reads.
func (d *rangeDecoder) decodeCdfLe16(bytes []byte) int32 {
	n := len(bytes) / 2
	at := func(i int) uint32 { return uint32(bytes[2*i]) | uint32(bytes[2*i+1])<<8 }
	if n < 2 {
		d.err = 1
		return 0
	}
	base := at(0)
	if at(n-1) <= base {
		d.err = 1
		return 0
	}
	ft := at(n-1) - base
	fs := d.decode(ft)
	target := base + fs
	k := 0
	for k < n-1 {
		if at(k+1) > target {
			break
		}
		k++
	}
	d.update(at(k)-base, at(k+1)-base, ft)
	return int32(k)
}

// bitsN reads raw `n` bits from the BACK of the buffer (`ec_dec_bits`), LSB-first.
func (d *rangeDecoder) bitsN(n uint32) uint32 {
	window := d.endWindow
	available := d.nendBits
	if uint32(available) < n {
		for {
			window |= d.readByteFromEnd() << uint32(available)
			available += int32(ecSymBits)
			if uint32(available) > ecWindowSize-ecSymBits {
				break
			}
		}
	}
	ret := window & (rangecoderShl1(n) - 1)
	window >>= n
	available -= int32(n)
	d.endWindow = window
	d.nendBits = available
	d.nbitsTotal += int32(n)
	return ret
}

// decodeUint decodes an integer uniformly distributed in [0, ft) for ft>1 (`ec_dec_uint`).
func (d *rangeDecoder) decodeUint(ft0 uint32) uint32 {
	ft := ft0 - 1
	ftb := ilog(ft)
	if ftb > ecUintBits {
		ftb -= ecUintBits
		t := (ft >> uint32(ftb)) + 1
		s := d.decode(t)
		d.update(s, s+1, t)
		v := (s << uint32(ftb)) | d.bitsN(uint32(ftb))
		if v <= ft {
			return v
		}
		d.err = 1
		return ft
	}
	ft = ft + 1
	s := d.decode(ft)
	d.update(s, s+1, ft)
	return s
}

// decode64FineSym is the 64-symbol uniform fine-lag read: `ext = rng>>6`,
// `sym = clamp(63 - val/ext, 0, 64)`, then `update(sym, sym+1, 64)`.
func (d *rangeDecoder) decode64FineSym() int32 {
	d.ext = d.rng >> 6
	if d.ext == 0 {
		d.err = 1
		d.ext = 1
		return 0
	}
	s := d.val / d.ext
	sym := int32(min(max(int64(63)-int64(s), 0), 64))
	d.update(uint32(sym), uint32(sym)+1, 64)
	return sym
}

// tell is the bits consumed so far, rounded up (`ec_tell`).
func (d *rangeDecoder) tell() int32 {
	return d.nbitsTotal - ilog(d.rng)
}

// storage is the bytes the payload actually holds. Paired with [tell] this is how a caller checks
// that a stream ended where it claimed to: reads past either end silently return zero here, so an
// impossible length is only visible by comparing the two.
func (d *rangeDecoder) storage() uint32 {
	return d.storageLen
}

// rangeEncoder is the Opus/CELT range ENCODER (`ec_enc`), the exact inverse of `rangeDecoder`,
// used by the mlow ENCODER. Writes range-coded symbols toward the front and raw bits toward the
// back; `done()` flushes and merges them, after which `bytes()` is the finished payload.
type rangeEncoder struct {
	buf        []byte
	storage    uint32
	endOffs    uint32
	endWindow  uint32
	nendBits   int32
	nbitsTotal int32
	offs       uint32
	rng        uint32
	val        uint32
	ext        uint32
	rem        int32
	errCode    int32
}

func newRangeEncoder(size int) rangeEncoder {
	return rangeEncoder{
		buf:        make([]byte, size),
		storage:    uint32(size),
		endOffs:    0,
		endWindow:  0,
		nendBits:   0,
		nbitsTotal: int32(ecCodeBits) + 1,
		offs:       0,
		rng:        ecCodeTop,
		val:        0,
		ext:        0,
		rem:        -1,
		errCode:    0,
	}
}

// reset starts a new stream while retaining the fixed output allocation.
func (e *rangeEncoder) reset() {
	e.endOffs = 0
	e.endWindow = 0
	e.nendBits = 0
	e.nbitsTotal = int32(ecCodeBits) + 1
	e.offs = 0
	e.rng = ecCodeTop
	e.val = 0
	e.ext = 0
	e.rem = -1
	e.errCode = 0
}

func (e *rangeEncoder) err() int32 {
	return e.errCode
}

func (e *rangeEncoder) writeByte(b uint32) {
	if e.offs+e.endOffs < e.storage {
		e.buf[e.offs] = uint8(b)
		e.offs++
	} else {
		e.errCode = -1
	}
}

func (e *rangeEncoder) writeByteAtEnd(b uint32) {
	if e.offs+e.endOffs < e.storage {
		e.endOffs++
		e.buf[e.storage-e.endOffs] = uint8(b)
	} else {
		e.errCode = -1
	}
}

func (e *rangeEncoder) carryOut(c int32) {
	if uint32(c) != ecSymMax {
		carry := c >> ecSymBits
		if e.rem >= 0 {
			e.writeByte(uint32(e.rem + carry))
		}
		if e.ext > 0 {
			sym := uint32((int32(ecSymMax) + carry) & int32(ecSymMax))
			for {
				e.writeByte(sym)
				e.ext--
				if e.ext == 0 {
					break
				}
			}
		}
		e.rem = c & int32(ecSymMax)
	} else {
		e.ext++
	}
}

func (e *rangeEncoder) normalize() {
	for e.rng <= ecCodeBot {
		e.carryOut(int32(e.val >> ecCodeShift))
		e.val = (e.val << ecSymBits) & (ecCodeTop - 1)
		e.rng <<= ecSymBits
		e.nbitsTotal += int32(ecSymBits)
	}
}

func (e *rangeEncoder) encode(fl, fh, ft uint32) {
	if ft == 0 {
		e.errCode = -1
		return
	}
	r := e.rng / ft
	if fl > 0 {
		e.val = e.val + (e.rng - r*(ft-fl))
		e.rng = r * (fh - fl)
	} else {
		e.rng = e.rng - r*(ft-fh)
	}
	e.normalize()
}

func (e *rangeEncoder) bitLogp(val int32, logp uint32) {
	r := e.rng
	l := e.val
	s := r >> logp
	r2 := r - s
	if val != 0 {
		e.val = l + r2
		e.rng = s
	} else {
		e.rng = r2
	}
	e.normalize()
}

func (e *rangeEncoder) encodeIcdf(s int32, icdf []uint8, ftb uint32) {
	r := e.rng >> ftb
	if s > 0 {
		e.val = e.val + (e.rng - r*uint32(icdf[s-1]))
		e.rng = r * uint32(icdf[s-1]-icdf[s])
	} else {
		e.rng = e.rng - r*uint32(icdf[s])
	}
	e.normalize()
}

// encodeCdf is the inverse of `decodeCdf`: encode symbol `s` against a u16 cumulative CDF
// (`ft = cdf[n-1]-cdf[0]`).
func (e *rangeEncoder) encodeCdf(s int32, cdf []uint16) {
	n := len(cdf)
	if n < 2 || s < 0 || int(s+1) >= n {
		e.errCode = -1
		return
	}
	base := uint32(cdf[0])
	if uint32(cdf[n-1]) <= base {
		e.errCode = -1
		return
	}
	ft := uint32(cdf[n-1]) - base
	e.encode(
		uint32(cdf[s])-base,
		uint32(cdf[s+1])-base,
		ft,
	)
}

// encodeCdfWindow is `encodeCdf` against a length-`n` window of `base` starting at `start`,
// without materializing the window. Out-of-range positions read 0, matching
// `encodeCdf(cdfWindow(base, start, n))`.
func (e *rangeEncoder) encodeCdfWindow(s int32, base []uint16, start, n int) {
	if n < 2 || s < 0 || int(s+1) >= n {
		e.errCode = -1
		return
	}
	cbase := rangecoderAt(base, start, 0)
	last := rangecoderAt(base, start, n-1)
	if last <= cbase {
		e.errCode = -1
		return
	}
	ft := last - cbase
	e.encode(rangecoderAt(base, start, int(s))-cbase, rangecoderAt(base, start, int(s+1))-cbase, ft)
}

// bitsN writes raw `n` bits toward the back of the buffer.
func (e *rangeEncoder) bitsN(fl, n uint32) {
	window := e.endWindow
	used := e.nendBits
	if used+int32(n) > int32(ecWindowSize) {
		for {
			e.writeByteAtEnd(window & ecSymMax)
			window >>= ecSymBits
			used -= int32(ecSymBits)
			if used < int32(ecSymBits) {
				break
			}
		}
	}
	window |= fl << (uint32(used) & 31) // wrapping_shl
	used += int32(n)
	e.endWindow = window
	e.nendBits = used
	e.nbitsTotal += int32(n)
}

func (e *rangeEncoder) encodeUint(fl, ft0 uint32) {
	ft := ft0 - 1
	ftb := ilog(ft)
	if ftb > ecUintBits {
		ftb := uint32(ftb - ecUintBits)
		t := (ft >> ftb) + 1
		e.encode(fl>>ftb, (fl>>ftb)+1, t)
		e.bitsN(fl&(rangecoderShl1(ftb)-1), ftb)
	} else {
		e.encode(fl, fl+1, ft+1)
	}
}

// encodeRawSymbol is the inverse of `decodeRawSymbol`: encode a uniform `nbits`-bit symbol on the
// range stream.
func (e *rangeEncoder) encodeRawSymbol(sym, nbits uint32) {
	e.encode(sym, sym+1, rangecoderShl1(nbits))
}

// encode64FineSym is the inverse of `decode64FineSym`: encode the 64-symbol uniform fine-lag
// value.
func (e *rangeEncoder) encode64FineSym(sym int32) {
	e.encode(uint32(sym), uint32(sym)+1, 64)
}

// done flushes the range coder and merges the back raw-bit stream. After this, `bytes()` is the
// payload.
func (e *rangeEncoder) done() {
	l := int32(ecCodeBits) - ilog(e.rng)
	msk := (ecCodeTop - 1) >> uint32(l)
	end := (e.val + msk) & ^msk
	if (end | msk) >= e.val+e.rng {
		l++
		msk >>= 1
		end = (e.val + msk) & ^msk
	}
	for l > 0 {
		e.carryOut(int32(end >> ecCodeShift))
		end = (end << ecSymBits) & (ecCodeTop - 1)
		l -= int32(ecSymBits)
	}
	if e.rem >= 0 || e.ext > 0 {
		e.carryOut(0)
	}
	window := e.endWindow
	used := e.nendBits
	for used >= int32(ecSymBits) {
		e.writeByteAtEnd(window & ecSymMax)
		window >>= ecSymBits
		used -= int32(ecSymBits)
	}
	if e.errCode == 0 {
		for i := e.offs; i < e.storage-e.endOffs; i++ {
			e.buf[i] = 0
		}
		if used > 0 {
			if e.endOffs >= e.storage-e.offs {
				e.errCode = -1
			} else {
				e.buf[e.storage-e.endOffs-1] |= uint8(window)
			}
		}
	}
}

func (e *rangeEncoder) bytes() []byte {
	return e.buf
}

// consumedLen is the meaningful body length = front (range) bytes + back (raw-bit) bytes; the gap
// between is zero-fill padding that `done()` wrote. The smpl encode path issues no back-bit ops, so
// `endOffs` stays 0 and this is just the front length; if a back-bit op is ever added, the
// front/back layout must be compacted (per the C `ec_enc_shrink`) before this stays correct.
func (e *rangeEncoder) consumedLen() int {
	return int(e.offs + e.endOffs)
}
