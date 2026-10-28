package audio

import "math"

// Resampler converts one channel of float32 samples between two rates as a
// stream: a Kaiser-windowed sinc, its cutoff just under the lower rate's
// Nyquist frequency, so going down to 16 kHz does not fold a device's
// highs back into the voice band. Positions are kept as exact fractions of
// the input, so a long call never drifts by a sample. The zero Resampler
// is not usable; make one with NewResampler.
type Resampler struct {
	from, to int
	pass     bool

	width  float64   // the kernel's half width, in input samples
	scale  float64   // the cutoff, as a fraction of the input's Nyquist
	kernel []float32 // the kernel from 0 to width, kernelRes points a sample
	reach  int       // input samples each side an output reads

	// phases are the kernel's taps for each output fraction, normalised,
	// when the reduced output rate is small enough to keep them all (it is
	// for every rate a device uses); nil otherwise.
	phases [][]float32

	hist []float32 // input not yet consumed, reach-1 samples of history first
	at   int       // the next output's input sample in hist
	frac int       // and its fraction, in 1/to of a sample
}

const (
	// halfTaps is the kernel's half width in samples of the lower rate.
	halfTaps = 24
	// cutoff is where the pass band ends, as a fraction of the lower rate's
	// Nyquist frequency.
	cutoff = 0.9
	// kaiserBeta gives about 90 dB of stop band.
	kaiserBeta = 8.6
	kernelRes  = 128
	// maxPhases bounds the precomputed phases (44.1 kHz from 16 kHz has
	// 441).
	maxPhases = 2048
)

// NewResampler converts from rate from to rate to, both in hertz.
func NewResampler(from, to int) *Resampler {
	r := &Resampler{from: from, to: to}
	if from == to || from <= 0 || to <= 0 {
		r.pass = true
		return r
	}
	g := gcd(from, to)
	r.from, r.to = from/g, to/g
	ratio := 1.0
	if to < from {
		ratio = float64(from) / float64(to)
	}
	r.scale = cutoff / ratio
	r.width = halfTaps * ratio
	r.reach = int(math.Ceil(r.width))
	n := int(r.width*kernelRes) + 2
	r.kernel = make([]float32, n)
	i0b := besselI0(kaiserBeta)
	for i := range n {
		x := float64(i) / kernelRes
		if x > r.width {
			break
		}
		t := x / r.width
		win := besselI0(kaiserBeta*math.Sqrt(1-t*t)) / i0b
		r.kernel[i] = float32(r.scale * sinc(r.scale*x) * win)
	}
	if r.to <= maxPhases {
		r.phases = make([][]float32, r.to)
		for frac := range r.phases {
			r.phases[frac] = r.taps(float64(frac) / float64(r.to))
		}
	}
	r.hist = make([]float32, r.reach-1, 4096)
	r.at = r.reach - 1
	return r
}

// Process appends to dst the output that src (and what came before it)
// makes. An output sample needs a few input samples after it, so the
// output trails the input by the kernel's reach.
func (r *Resampler) Process(dst, src []float32) []float32 {
	if r.pass {
		return append(dst, src...)
	}
	r.hist = append(r.hist, src...)
	for r.at+r.reach < len(r.hist) {
		dst = append(dst, r.sample())
		r.frac += r.from
		r.at += r.frac / r.to
		r.frac %= r.to
	}
	if drop := r.at - (r.reach - 1); drop > 0 {
		n := copy(r.hist, r.hist[drop:])
		r.hist = r.hist[:n]
		r.at -= drop
	}
	return dst
}

// sample is the output at hist[at] + frac/to.
func (r *Resampler) sample() float32 {
	var taps []float32
	if r.phases != nil {
		taps = r.phases[r.frac]
	} else {
		taps = r.taps(float64(r.frac) / float64(r.to))
	}
	x := r.hist[r.at-r.reach+1 : r.at+r.reach+1]
	var acc float32
	for i, c := range taps {
		acc += c * x[i]
	}
	return acc
}

// taps are the kernel's 2*reach taps for an output f of a sample past
// hist[at], normalised by their sum so a constant comes out unchanged.
func (r *Resampler) taps(f float64) []float32 {
	taps := make([]float32, 2*r.reach)
	var norm float64
	for i := range taps {
		k := i - r.reach + 1
		c := r.tap(math.Abs(float64(k) - f))
		taps[i] = float32(c)
		norm += c
	}
	if norm != 0 {
		for i := range taps {
			taps[i] = float32(float64(taps[i]) / norm)
		}
	}
	return taps
}

// tap is the kernel at distance x, interpolated between its table points.
func (r *Resampler) tap(x float64) float64 {
	if x >= r.width {
		return 0
	}
	p := x * kernelRes
	i := int(p)
	if i+1 >= len(r.kernel) {
		return float64(r.kernel[len(r.kernel)-1])
	}
	t := p - float64(i)
	return float64(r.kernel[i])*(1-t) + float64(r.kernel[i+1])*t
}

// Reset forgets the stream so far.
func (r *Resampler) Reset() {
	if r.pass {
		return
	}
	r.hist = r.hist[:r.reach-1]
	clear(r.hist)
	r.at, r.frac = r.reach-1, 0
}

func sinc(x float64) float64 {
	if x == 0 {
		return 1
	}
	return math.Sin(math.Pi*x) / (math.Pi * x)
}

// besselI0 is the modified Bessel function of the first kind, order 0, by
// its series.
func besselI0(x float64) float64 {
	sum, term := 1.0, 1.0
	for k := 1; k < 50; k++ {
		term *= (x / (2 * float64(k))) * (x / (2 * float64(k)))
		sum += term
		if term < 1e-12*sum {
			break
		}
	}
	return sum
}

func gcd(a, b int) int {
	for b != 0 {
		a, b = b, a%b
	}
	return a
}
