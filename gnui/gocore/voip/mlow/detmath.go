// Deterministic transcendentals for the port: exp, expm1, log, sin, cos,
// tanh and pow in float64, written for this package from the textbook
// definitions (Taylor series, the atanh series for log, Cody-Waite argument
// reduction with constants derived from the digits of pi and ln 2).
//
// Why not Go's math package: math.Exp and math.Log have assembly versions on
// some architectures, and the Go compiler may fuse x*y+z into one FMA inside
// the pure-Go versions on others, so their last bits differ between
// platforms. These functions keep every product that feeds a sum in an
// explicit float64() conversion, which the Go spec guarantees rounds and
// keeps from being fused, and use only exactly rounded operations (+, -, *,
// /, sqrt, Ldexp, Mod), so they return the same bits on every platform Go
// runs on. The codec's f32 helpers in rustmath.go round them to float32.
//
// Accuracy: a few float64 ulps for exp, log, sin, cos and tanh, and about
// 2^-43 relative for pow with a non-integer exponent (TestDetMath_* checks
// them against the math package), far finer than the float32 results the
// codec rounds them to.

package mlow

import "math"

const (
	// pi/2 in three parts: dmPio2Hi and dmPio2Mid carry 33 significant bits
	// each, so n*dmPio2Hi and n*dmPio2Mid are exact for |n| < 2^20.
	dmPio2Hi  = 1.5707963267341256
	dmPio2Mid = 6.077100506303966e-11
	dmPio2Lo  = 2.0222662487959506e-21
	dmInvPio2 = 0.6366197723675814
	// dmTrigMax bounds the three-part reduction (n < 2^19).
	dmTrigMax = 8.2e5
	dmTwoPi   = 6.283185307179586

	// ln 2 in two parts: dmLn2Hi carries 32 significant bits, so n*dmLn2Hi
	// is exact for any exponent n.
	dmLn2Hi   = 0.6931471803691238
	dmLn2Lo   = 1.9082149292705877e-10
	dmLn2     = 0.6931471805599453
	dmInvLn2  = 1.4426950408889634
	dmInvLn10 = 0.4342944819032518
	dmLog10o2 = 0.3010299956639812
	dmSqrt2   = 1.4142135623730951

	// exp overflows above dmExpMax and underflows to zero below dmExpMin.
	dmExpMax = 709.782712893384
	dmExpMin = -745.1332191019412
)

// dmPoly evaluates c[0] + z*(c[1] + z*(c[2] + ...)) by Horner's rule with
// every product rounded on its own.
func dmPoly(z float64, c []float64) float64 {
	p := c[len(c)-1]
	for i := len(c) - 2; i >= 0; i-- {
		p = float64(p*z) + c[i]
	}
	return p
}

// dmExpTaylor holds 1/(k+1)! for k = 0..13: exp(r) - 1 = r*P(r).
var dmExpTaylor = [...]float64{
	1, 1.0 / 2, 1.0 / 6, 1.0 / 24, 1.0 / 120, 1.0 / 720, 1.0 / 5040, 1.0 / 40320,
	1.0 / 362880, 1.0 / 3628800, 1.0 / 39916800, 1.0 / 479001600,
	1.0 / 6227020800, 1.0 / 87178291200,
}

// dmExpReduce splits x into n*ln2 + r with |r| <= ln2/2 (Cody-Waite).
func dmExpReduce(x float64) (n float64, r float64) {
	n = math.Round(float64(x * dmInvLn2))
	r = float64(x-float64(n*dmLn2Hi)) - float64(n*dmLn2Lo)
	return n, r
}

// dmExpm1Small is exp(r) - 1 for |r| <= ln2/2, from the Taylor series.
func dmExpm1Small(r float64) float64 { return float64(r * dmPoly(r, dmExpTaylor[:])) }

// dmExp is e^x.
func dmExp(x float64) float64 {
	switch {
	case x != x:
		return x
	case x > dmExpMax:
		return math.Inf(1)
	case x < dmExpMin:
		return 0
	}
	n, r := dmExpReduce(x)
	return math.Ldexp(1+dmExpm1Small(r), int(n))
}

// dmExpm1 is e^x - 1, accurate near zero.
func dmExpm1(x float64) float64 {
	if x >= -0.5*dmLn2 && x <= 0.5*dmLn2 {
		return dmExpm1Small(x)
	}
	return dmExp(x) - 1
}

// dmLogOdd holds 1/(2k+3) for k = 0..10: log(m) = 2s + 2s*z*Q(z), with
// s = (m-1)/(m+1) and z = s^2 (the series of 2*atanh(s)).
var dmLogOdd = [...]float64{
	1.0 / 3, 1.0 / 5, 1.0 / 7, 1.0 / 9, 1.0 / 11, 1.0 / 13, 1.0 / 15, 1.0 / 17,
	1.0 / 19, 1.0 / 21, 1.0 / 23,
}

// dmLogParts splits a positive finite x into e and log(m), x = m*2^e with
// m in [sqrt(1/2), sqrt(2)).
func dmLogParts(x float64) (e float64, logm float64) {
	frac, exp := math.Frexp(x) // x = frac*2^exp, frac in [0.5, 1)
	m := frac * 2
	exp--
	if m >= dmSqrt2 {
		m = frac
		exp++
	}
	f := m - 1 // exact
	s := f / (2 + f)
	z := float64(s * s)
	logm = float64(2*s) + float64(float64(2*s)*float64(z*dmPoly(z, dmLogOdd[:])))
	return float64(exp), logm
}

// dmLogSpecial returns log's result for NaN, zero, negative and +inf.
func dmLogSpecial(x float64) (float64, bool) {
	switch {
	case x != x || x < 0:
		return math.NaN(), true
	case x == 0:
		return math.Inf(-1), true
	case math.IsInf(x, 1):
		return x, true
	}
	return 0, false
}

// dmLog is the natural logarithm.
func dmLog(x float64) float64 {
	if v, ok := dmLogSpecial(x); ok {
		return v
	}
	e, logm := dmLogParts(x)
	return float64(e*dmLn2Hi) + (float64(e*dmLn2Lo) + logm)
}

// dmLog2 is log base 2; exact on powers of two.
func dmLog2(x float64) float64 {
	if v, ok := dmLogSpecial(x); ok {
		return v
	}
	e, logm := dmLogParts(x)
	return e + float64(logm*dmInvLn2)
}

// dmLog10 is log base 10.
func dmLog10(x float64) float64 {
	if v, ok := dmLogSpecial(x); ok {
		return v
	}
	e, logm := dmLogParts(x)
	return float64(e*dmLog10o2) + float64(logm*dmInvLn10)
}

// dmSinTaylor holds (-1)^(k+1)/(2k+3)! for k = 0..8: sin(r) = r + r*z*S(z).
var dmSinTaylor = [...]float64{
	-1.0 / 6, 1.0 / 120, -1.0 / 5040, 1.0 / 362880, -1.0 / 39916800,
	1.0 / 6227020800, -1.0 / 1307674368000, 1.0 / 355687428096000,
	-1.0 / 121645100408832000,
}

// dmCosTaylor holds (-1)^k/(2k+4)! for k = 0..8: cos(r) = 1 - z/2 + z*z*C(z).
var dmCosTaylor = [...]float64{
	1.0 / 24, -1.0 / 720, 1.0 / 40320, -1.0 / 3628800, 1.0 / 479001600,
	-1.0 / 87178291200, 1.0 / 20922789888000, -1.0 / 6402373705728000,
	1.0 / 2432902008176640000,
}

func dmSinKernel(r float64) float64 {
	z := float64(r * r)
	return r + float64(float64(r*z)*dmPoly(z, dmSinTaylor[:]))
}

func dmCosKernel(r float64) float64 {
	z := float64(r * r)
	return (1 - float64(0.5*z)) + float64(float64(z*z)*dmPoly(z, dmCosTaylor[:]))
}

// dmTrigReduce returns the quadrant q (mod 4) and r in [-pi/4, pi/4] with
// x = q*pi/2 + r, for finite x. Past dmTrigMax it first reduces x modulo
// the float64 2*pi, which loses accuracy but stays deterministic; the codec
// never gets there.
func dmTrigReduce(x float64) (int, float64) {
	if x > dmTrigMax || x < -dmTrigMax {
		x = math.Mod(x, dmTwoPi)
	}
	n := math.Round(float64(x * dmInvPio2))
	r := float64(float64(x-float64(n*dmPio2Hi))-float64(n*dmPio2Mid)) - float64(n*dmPio2Lo)
	return int(int64(n) & 3), r
}

// dmSinCos returns sin(x) and cos(x).
func dmSinCos(x float64) (float64, float64) {
	if x != x || math.IsInf(x, 0) {
		return math.NaN(), math.NaN()
	}
	q, r := dmTrigReduce(x)
	s, c := dmSinKernel(r), dmCosKernel(r)
	switch q {
	case 1:
		return c, -s
	case 2:
		return -s, -c
	case 3:
		return -c, s
	}
	return s, c
}

func dmSin(x float64) float64 {
	if x == 0 {
		return x // keeps the sign of zero
	}
	s, _ := dmSinCos(x)
	return s
}

func dmCos(x float64) float64 {
	_, c := dmSinCos(x)
	return c
}

// dmTanh is tanh(x) = expm1(2x) / (expm1(2x) + 2), odd in x.
func dmTanh(x float64) float64 {
	ax := math.Abs(x)
	var t float64
	switch {
	case x != x:
		return x
	case ax > 22:
		t = 1
	case ax < 0x1p-28:
		return x
	default:
		e := dmExpm1(2 * ax)
		t = e / (e + 2)
	}
	return math.Copysign(t, x)
}

// dmIsInt reports whether a finite y is an integer, and if so whether odd.
func dmIsInt(y float64) (isInt, odd bool) {
	if math.Trunc(y) != y {
		return false, false
	}
	if math.Abs(y) >= 0x1p53 {
		return true, false
	}
	return true, int64(y)&1 == 1
}

// dmPow is x^y = exp(y*log|x|) with the IEEE 754 special cases (C99 Annex
// F): a negative base takes an integer exponent's parity and gives NaN for a
// non-integer one.
func dmPow(x, y float64) float64 {
	switch {
	case y == 0 || x == 1:
		return 1
	case x != x || y != y:
		return math.NaN()
	case math.IsInf(y, 0):
		ax := math.Abs(x)
		switch {
		case ax == 1:
			return 1
		case (ax < 1) == (y > 0):
			return 0
		}
		return math.Inf(1)
	}
	isInt, odd := dmIsInt(y)
	neg := math.Signbit(x)
	switch {
	case x == 0 || math.IsInf(x, 0):
		// 0^y and inf^y: y<0 swaps zero and infinity, x's sign survives
		// only an odd integer exponent.
		big := math.IsInf(x, 0) == (y > 0)
		r := 0.0
		if big {
			r = math.Inf(1)
		}
		if neg && odd {
			r = -r
		}
		return r
	case neg && !isInt:
		return math.NaN()
	}
	var r float64
	if isInt && math.Abs(y) <= 64 {
		// Small integer exponents by repeated squaring: exact where the
		// power is representable (2^10, (-2)^3), a few ulps elsewhere.
		r = dmPowInt(math.Abs(x), int(math.Abs(y)))
		if y < 0 {
			r = 1 / r
		}
	} else {
		// The rounding of y*log|x| scales the result's relative error by
		// |y*log|x||: at most about 2^-43 inside the float32 range, far
		// below a float32 ulp.
		r = dmExp(float64(y * dmLog(math.Abs(x))))
	}
	if neg && odd {
		r = -r
	}
	return r
}

// dmPowInt is x^n for n >= 0 by repeated squaring.
func dmPowInt(x float64, n int) float64 {
	r := 1.0
	for n > 0 {
		if n&1 != 0 {
			r *= x
		}
		x *= x
		n >>= 1
	}
	return r
}
