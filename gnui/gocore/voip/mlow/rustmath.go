// Rust float and integer semantics the port depends on, where Go's built-ins
// differ. Not a port of a Rust file: these mirror Rust's std (f32::max,
// `as` casts, powi, Sum) as compiled for aarch64.

package mlow

import (
	"math"
	"math/bits"
)

// maxF32 is Rust's f32::max (llvm.maxnum, FMAXNM on arm64): a NaN argument
// yields the other one, and max(-0, +0) is +0.
func maxF32(a, b float32) float32 {
	switch {
	case a != a:
		return b
	case b != b:
		return a
	case a > b:
		return a
	case b > a:
		return b
	case math.Signbit(float64(a)):
		return b
	}
	return a
}

// minF32 is Rust's f32::min: a NaN argument yields the other one, and
// min(-0, +0) is -0.
func minF32(a, b float32) float32 {
	switch {
	case a != a:
		return b
	case b != b:
		return a
	case a < b:
		return a
	case b < a:
		return b
	case math.Signbit(float64(a)):
		return a
	}
	return b
}

// maxF64 is Rust's f64::max.
func maxF64(a, b float64) float64 {
	switch {
	case a != a:
		return b
	case b != b:
		return a
	case a > b:
		return a
	case b > a:
		return b
	case math.Signbit(a):
		return b
	}
	return a
}

// minF64 is Rust's f64::min.
func minF64(a, b float64) float64 {
	switch {
	case a != a:
		return b
	case b != b:
		return a
	case a < b:
		return a
	case b < a:
		return b
	case math.Signbit(a):
		return a
	}
	return b
}

// clampF32 is Rust's f32::clamp: a NaN input passes through.
func clampF32(x, lo, hi float32) float32 {
	if x < lo {
		x = lo
	}
	if x > hi {
		x = hi
	}
	return x
}

// clampF64 is Rust's f64::clamp.
func clampF64(x, lo, hi float64) float64 {
	if x < lo {
		x = lo
	}
	if x > hi {
		x = hi
	}
	return x
}

func absF32(x float32) float32 { return math.Float32frombits(math.Float32bits(x) &^ (1 << 31)) }

// The f32 transcendentals are Rust's calls into the platform libm. Here they
// are the deterministic float64 functions of detmath.go rounded to float32,
// so they agree with a correctly rounded libm except in rare last-bit cases
// and give the same bits on every platform. sqrt, floor, ceil, round and
// trunc are exact.

func sqrtF32(x float32) float32  { return float32(math.Sqrt(float64(x))) }
func sinF32(x float32) float32   { return float32(dmSin(float64(x))) }
func cosF32(x float32) float32   { return float32(dmCos(float64(x))) }
func expF32(x float32) float32   { return float32(dmExp(float64(x))) }
func lnF32(x float32) float32    { return float32(dmLog(float64(x))) }
func log2F32(x float32) float32  { return float32(dmLog2(float64(x))) }
func log10F32(x float32) float32 { return float32(dmLog10(float64(x))) }

// sincosF32 is (sinF32(x), cosF32(x)) with one argument reduction.
func sincosF32(x float32) (float32, float32) {
	if x == 0 {
		return x, 1
	}
	s, c := dmSinCos(float64(x))
	return float32(s), float32(c)
}

// powF32 is f32::powf. A constant exponent of 2 is x*x in Rust (LLVM
// rewrites it), which float32(x*x) matches exactly.
func powF32(x, y float32) float32 {
	if y == 2 {
		return x * x
	}
	return float32(dmPow(float64(x), float64(y)))
}

func floorF32(x float32) float32 { return float32(math.Floor(float64(x))) }
func ceilF32(x float32) float32  { return float32(math.Ceil(float64(x))) }

// roundF32 is Rust's f32::round: half away from zero.
func roundF32(x float32) float32 { return float32(math.Round(float64(x))) }
func truncF32(x float32) float32 { return float32(math.Trunc(float64(x))) }

// powiF32 is Rust's f32::powi (compiler-rt __powisf2): repeated squaring in
// f32, then a reciprocal for a negative exponent.
func powiF32(a float32, b int32) float32 {
	recip := b < 0
	r := float32(1)
	for {
		if b&1 != 0 {
			r *= a
		}
		b /= 2
		if b == 0 {
			break
		}
		a *= a
	}
	if recip {
		return 1 / r
	}
	return r
}

// powiF64 is Rust's f64::powi (__powidf2).
func powiF64(a float64, b int32) float64 {
	recip := b < 0
	r := 1.0
	for {
		if b&1 != 0 {
			r *= a
		}
		b /= 2
		if b == 0 {
			break
		}
		a *= a
	}
	if recip {
		return 1 / r
	}
	return r
}

// sumF32 is Rust's Iterator::sum for f32: a left fold from -0.0.
func sumF32(xs []float32) float32 {
	s := float32(math.Copysign(0, -1))
	for _, x := range xs {
		s += x
	}
	return s
}

// sumF64 is Rust's Iterator::sum for f64.
func sumF64(xs []float64) float64 {
	s := math.Copysign(0, -1)
	for _, x := range xs {
		s += x
	}
	return s
}

// Saturating float-to-integer casts, Rust's `as`: truncate toward zero,
// clamp to the target's range, NaN becomes 0. Go leaves out-of-range
// conversions implementation-defined.

func f64ToI64(x float64) int64 {
	switch {
	case x != x:
		return 0
	case x >= 9223372036854775807:
		return math.MaxInt64
	case x <= -9223372036854775808:
		return math.MinInt64
	}
	return int64(x)
}

func f64ToI32(x float64) int32 {
	switch {
	case x != x:
		return 0
	case x >= math.MaxInt32:
		return math.MaxInt32
	case x <= math.MinInt32:
		return math.MinInt32
	}
	return int32(x)
}

func f64ToI16(x float64) int16 {
	switch {
	case x != x:
		return 0
	case x >= math.MaxInt16:
		return math.MaxInt16
	case x <= math.MinInt16:
		return math.MinInt16
	}
	return int16(x)
}

func f64ToU32(x float64) uint32 {
	switch {
	case x != x || x <= 0:
		return 0
	case x >= math.MaxUint32:
		return math.MaxUint32
	}
	return uint32(x)
}

func f64ToU16(x float64) uint16 {
	switch {
	case x != x || x <= 0:
		return 0
	case x >= math.MaxUint16:
		return math.MaxUint16
	}
	return uint16(x)
}

func f64ToU8(x float64) uint8 {
	switch {
	case x != x || x <= 0:
		return 0
	case x >= math.MaxUint8:
		return math.MaxUint8
	}
	return uint8(x)
}

// f64ToUsize is `as usize` on a 64-bit target, returned as a Go int (the
// port's usize). Values past MaxInt64 cannot occur in the codec.
func f64ToUsize(x float64) int {
	switch {
	case x != x || x <= 0:
		return 0
	case x >= 9223372036854775807:
		return math.MaxInt64
	}
	return int(x)
}

func f32ToI64(x float32) int64    { return f64ToI64(float64(x)) }
func f32ToI32(x float32) int32    { return f64ToI32(float64(x)) }
func f32ToI16(x float32) int16    { return f64ToI16(float64(x)) }
func f32ToU32(x float32) uint32   { return f64ToU32(float64(x)) }
func f32ToU16(x float32) uint16   { return f64ToU16(float64(x)) }
func f32ToU8(x float32) uint8     { return f64ToU8(float64(x)) }
func f32ToUsize(x float32) int    { return f64ToUsize(float64(x)) }
func f32ToIsize(x float32) int    { return int(f64ToI64(float64(x))) }
func f64ToIsize(x float64) int    { return int(f64ToI64(x)) }
func leadingZeros32(x uint32) int { return bits.LeadingZeros32(x) }

// absI32 is Rust's i32::abs (wrapping at MinInt32, as release builds do).
func absI32(x int32) int32 {
	if x < 0 {
		return -x
	}
	return x
}

func absInt(x int) int {
	if x < 0 {
		return -x
	}
	return x
}

// remEuclid and divEuclid are Rust's i32::rem_euclid and div_euclid.
func remEuclid(a, b int32) int32 {
	r := a % b
	if r < 0 {
		if b < 0 {
			return r - b
		}
		return r + b
	}
	return r
}

func divEuclid(a, b int32) int32 {
	q := a / b
	if a%b < 0 {
		if b > 0 {
			return q - 1
		}
		return q + 1
	}
	return q
}

// satAddU8 is Rust's u8::saturating_add.
func satAddU8(a, b uint8) uint8 {
	s := a + b
	if s < a {
		return math.MaxUint8
	}
	return s
}

// minPositiveF32 is Rust's f32::MIN_POSITIVE, the smallest normal f32 (Go's
// math.SmallestNonzeroFloat32 is the smallest subnormal).
const minPositiveF32 float32 = 1.17549435e-38

// epsilonF32 is Rust's f32::EPSILON.
const epsilonF32 float32 = 1.1920929e-7

// b2i is Rust's `bool as usize` / `as i32` and friends.
func b2i(b bool) int {
	if b {
		return 1
	}
	return 0
}

func b2i32(b bool) int32 {
	if b {
		return 1
	}
	return 0
}

func b2f32(b bool) float32 {
	if b {
		return 1
	}
	return 0
}

// satSubInt is Rust's usize::saturating_sub.
func satSubInt(a, b int) int {
	if b > a {
		return 0
	}
	return a - b
}
