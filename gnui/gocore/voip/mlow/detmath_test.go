package mlow

import (
	"math"
	"math/rand/v2"
	"testing"
)

// detmath.go against Go's math package: every function within a few float64
// ulps over the codec's domain and beyond, and their special cases.

func dmUlps(got, want float64) float64 {
	if got == want {
		return 0
	}
	return math.Abs(got-want) / math.Abs(math.Nextafter(want, math.Inf(1))-want)
}

func dmRefLog2(x float64) float64 { return math.Log(x) / math.Ln2 }

func TestDetMath_Accuracy(t *testing.T) {
	rng := rand.New(rand.NewPCG(1, 2))
	cases := []struct {
		name   string
		f, ref func(float64) float64
		gen    func() float64
		maxUlp float64
	}{
		{"exp", dmExp, math.Exp, func() float64 { return rng.Float64()*1400 - 700 }, 2},
		{"exp small", dmExp, math.Exp, func() float64 { return rng.Float64()*40 - 20 }, 2},
		{"expm1", dmExpm1, math.Expm1, func() float64 { return rng.Float64()*2 - 1 }, 4},
		{"log", dmLog, math.Log, func() float64 { return math.Exp(rng.Float64()*1400 - 700) }, 2},
		{"log near 1", dmLog, math.Log, func() float64 { return 0.5 + rng.Float64() }, 2},
		// math.Log2 cancels near 1 (it adds log(frac)/ln2 to the exponent), so the reference is
		// log(x)/ln2, itself good to about 1.5 ulps.
		{"log2", dmLog2, dmRefLog2, func() float64 { return math.Exp(rng.Float64()*1400 - 700) }, 4},
		{"log2 near 1", dmLog2, dmRefLog2, func() float64 { return 0.5 + rng.Float64() }, 4},
		{"log10", dmLog10, math.Log10, func() float64 { return math.Exp(rng.Float64()*1400 - 700) }, 2},
		{"sin", dmSin, math.Sin, func() float64 { return rng.Float64()*200 - 100 }, 2},
		{"cos", dmCos, math.Cos, func() float64 { return rng.Float64()*200 - 100 }, 2},
		{"sin small", dmSin, math.Sin, func() float64 { return rng.Float64()*2 - 1 }, 2},
		{"cos [0, pi]", dmCos, math.Cos, func() float64 { return rng.Float64() * math.Pi }, 2},
		{"tanh", dmTanh, math.Tanh, func() float64 { return rng.Float64()*10 - 5 }, 3},
	}
	for _, c := range cases {
		worst, at := 0.0, 0.0
		for range 200000 {
			x := c.gen()
			if u := dmUlps(c.f(x), c.ref(x)); u > worst {
				worst, at = u, x
			}
		}
		if worst > c.maxUlp {
			t.Errorf("%s: %.2f ulps at %v (want <= %.0f)", c.name, worst, at, c.maxUlp)
		}
	}
	// pow over the codec's uses: 2^y, 10^y, x^3, x^4, e^y and cos(w)^y.
	worst := 0.0
	for range 200000 {
		x, y := rng.Float64()*100, rng.Float64()*40-20
		for _, p := range [][2]float64{{x, 3}, {x, 4}, {2, y}, {10, y}, {math.E, y}, {rng.Float64(), y + 20}} {
			worst = max(worst, dmUlps(dmPow(p[0], p[1]), math.Pow(p[0], p[1])))
		}
	}
	// 1024 float64 ulps is 2^-42 relative, 2^-18 of a float32 ulp.
	if worst > 1024 {
		t.Errorf("pow: %.1f ulps", worst)
	}
}

// The f32 wrappers round the float64 results, so they almost always agree
// with Go's float64 math rounded to f32 (a correctly rounded f32 function
// except for rare double roundings).
func TestDetMath_F32AgreesWithMath(t *testing.T) {
	rng := rand.New(rand.NewPCG(3, 4))
	fns := []struct {
		name string
		f    func(float32) float32
		ref  func(float64) float64
		lo   float64
		hi   float64
	}{
		{"exp", expF32, math.Exp, -80, 80},
		{"ln", lnF32, math.Log, 1e-30, 1e30},
		{"log2", log2F32, math.Log2, 1e-30, 1e30},
		{"log10", log10F32, math.Log10, 1e-30, 1e30},
		{"sin", sinF32, math.Sin, -10, 10},
		{"cos", cosF32, math.Cos, -10, 10},
	}
	for _, fn := range fns {
		diff := 0
		const n = 200000
		for range n {
			x := float32(fn.lo + rng.Float64()*(fn.hi-fn.lo))
			got, want := fn.f(x), float32(fn.ref(float64(x)))
			if got != want {
				if d := ulpDiff32(got, want); d > 1 {
					t.Fatalf("%s(%v) = %v, math %v: %d ulps", fn.name, x, got, want, d)
				}
				diff++
			}
		}
		if diff > n/10000 {
			t.Errorf("%s: %d of %d results differ from math by an ulp", fn.name, diff, n)
		}
	}
	diff := 0
	for range 200000 {
		x, y := float32(rng.Float64()*100), float32(rng.Float64()*40-20)
		for _, p := range [][2]float32{{x, 3}, {x, 4}, {2, y}, {10, y}, {smplE, y}} {
			got, want := powF32(p[0], p[1]), float32(math.Pow(float64(p[0]), float64(p[1])))
			if got != want {
				if d := ulpDiff32(got, want); d > 1 {
					t.Fatalf("pow(%v, %v) = %v, math %v: %d ulps", p[0], p[1], got, want, d)
				}
				diff++
			}
		}
	}
	if diff > 100 {
		t.Errorf("pow: %d of 1000000 results differ from math by an ulp", diff)
	}
	for _, x := range []float32{0.01, 0.5, 1, 2, 3.14159} {
		s, c := sincosF32(x)
		if s != sinF32(x) || c != cosF32(x) {
			t.Errorf("sincosF32(%v) = %v, %v; sin, cos %v, %v", x, s, c, sinF32(x), cosF32(x))
		}
	}
}

func TestDetMath_SpecialCases(t *testing.T) {
	inf, nan := math.Inf(1), math.NaN()
	negZero := math.Copysign(0, -1)
	same := func(a, b float64) bool {
		return a == b && math.Signbit(a) == math.Signbit(b) || a != a && b != b
	}
	for _, c := range []struct {
		name      string
		got, want float64
	}{
		{"exp(+inf)", dmExp(inf), inf}, {"exp(-inf)", dmExp(-inf), 0}, {"exp(nan)", dmExp(nan), nan},
		{"exp(0)", dmExp(0), 1}, {"exp(1000)", dmExp(1000), inf}, {"exp(-1000)", dmExp(-1000), 0},
		{"log(0)", dmLog(0), -inf}, {"log(-1)", dmLog(-1), nan}, {"log(+inf)", dmLog(inf), inf},
		{"log(1)", dmLog(1), 0}, {"log2(8)", dmLog2(8), 3}, {"log2(0x1p-1074)", dmLog2(0x1p-1074), -1074},
		{"log10(nan)", dmLog10(nan), nan},
		{"sin(-0)", dmSin(negZero), negZero}, {"sin(inf)", dmSin(inf), nan}, {"cos(nan)", dmCos(nan), nan},
		{"cos(0)", dmCos(0), 1},
		{"tanh(-0)", dmTanh(negZero), negZero}, {"tanh(30)", dmTanh(30), 1}, {"tanh(-inf)", dmTanh(-inf), -1},
		{"pow(x, 0)", dmPow(nan, 0), 1}, {"pow(1, nan)", dmPow(1, nan), 1}, {"pow(-2, 3)", dmPow(-2, 3), -8},
		{"pow(-2, 0.5)", dmPow(-2, 0.5), nan}, {"pow(0, -1)", dmPow(0, -1), inf},
		{"pow(-0, 3)", dmPow(negZero, 3), negZero}, {"pow(-0, -3)", dmPow(negZero, -3), -inf},
		{"pow(0.5, inf)", dmPow(0.5, inf), 0}, {"pow(2, -inf)", dmPow(2, -inf), 0},
		{"pow(-1, inf)", dmPow(-1, inf), 1}, {"pow(-inf, 3)", dmPow(-inf, 3), -inf},
		{"pow(inf, -2)", dmPow(inf, -2), 0}, {"pow(2, 10)", dmPow(2, 10), 1024},
	} {
		if !same(c.got, c.want) {
			t.Errorf("%s = %v, want %v", c.name, c.got, c.want)
		}
	}
}
