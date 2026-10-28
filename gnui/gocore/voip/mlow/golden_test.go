// Ported from whatsapp-rust 5e72baf wacore/src/voip/mlow/golden.rs, MIT.
//
// Self-contained no-regression guard for the mlow codec; proves the encode->decode pipeline has
// not drifted WITHOUT any reference fixture file. A deterministic synthetic signal (voiced tone,
// unvoiced noise, silence) runs through the full round-trip and a compact digest, human-readable
// scalars plus an exact byte checksum, is asserted against committed golden constants.
//
// This is a regression tripwire, not a correctness oracle: it locks in the validated codec output,
// so any drift flips the checksum while the scalars stay eyeball-checkable. Bit-exactness is proven
// by the separate per-stage suites; the point here is that the anti-regression guard survives
// without the multi-MB ground-truth dumps.
//
// Regenerate after an intentional codec change:
//
//	VOIP_GOLDEN=1 cargo test -p wacore --features voip golden_roundtrip -- --nocapture
//
// (Go: VOIP_GOLDEN=1 go test -run TestGolden_GoldenRoundtripNoRegression -v.)

package mlow

import (
	"encoding/binary"
	"math"
	"os"
	"testing"
)

const goldenFrameSamps = 960
const goldenPacketCount = 24

// goldenSynthInput is deterministic input cycling voiced tone / unvoiced noise / voiced+noise /
// silence, so the round-trip exercises the voiced and unvoiced encode paths from code alone (no
// data file). The silence cycle is encoded as a low-energy active frame, not DTX (the encoder has
// no DTX/SID path).
func goldenSynthInput() []float32 {
	const pi = smplPi // std::f32::consts::PI
	out := make([]float32, 0, goldenPacketCount*goldenFrameSamps)
	seed := uint32(0x9e37_79b9)
	for i := 0; i < goldenPacketCount*goldenFrameSamps; i++ {
		t := float32(i) / 16000.0
		seed = seed*1664525 + 1013904223
		// The conversion keeps Go from fusing the (power-of-two) division into an FMA.
		noise := float32(float32(seed>>8)/8_388_608.0) - 1.0 // [-1, 1)
		var v float32
		switch (i / goldenFrameSamps) % 4 {
		case 0:
			v = float32(0.30*sinF32(float32(2.0)*pi*150.0*t)) + float32(0.10*sinF32(float32(2.0)*pi*300.0*t))
		case 1:
			v = 0.18 * noise
		case 2:
			v = float32(0.25*sinF32(float32(2.0)*pi*120.0*t)) + float32(0.05*noise)
		default:
			v = 0.0
		}
		out = append(out, v)
	}
	return out
}

// goldenPerturbOneUlp is `synth_input` with the low mantissa bit of about half the samples
// flipped: the smallest change an f32 signal can carry. Which samples are hit is a pure function
// of `(index, seed)`, so each seed is a different, reproducible one-ULP neighbour of the same
// signal.
//
// One quarter of the corpus is exact-zero silence, so about an eighth of it goes from `0.0` to the
// smallest subnormal rather than taking an ordinary mantissa step. That is still one ULP, and it
// is not what drives the divergence the test measures: the silence frames are the ones that stay
// closest to the unperturbed output (70-86 dB), while the frames that move are tone and noise,
// where every flip is a mantissa step on a normal float.
func goldenPerturbOneUlp(x []float32, seed uint32) []float32 {
	out := make([]float32, len(x))
	for i, v := range x {
		h := uint32(i)*0x9e37_79b9 ^ seed*0x85eb_ca6b
		h ^= h >> 15
		h = h * 0xc2b2_ae35
		h ^= h >> 13
		bits := math.Float32bits(v)
		if h&1 == 0 {
			bits ^= 1
		}
		out[i] = math.Float32frombits(bits)
	}
	return out
}

func goldenFnv1a(bytes []byte) uint64 {
	h := uint64(0xcbf2_9ce4_8422_2325)
	for _, b := range bytes {
		h ^= uint64(b)
		h = h * 0x0000_0100_0000_01b3
	}
	return h
}

type goldenDigestT struct {
	samples  int
	rms      float32
	peak     float32
	clipPct  float32
	checksum uint64
}

func goldenDigest(out []int16) goldenDigestT {
	n := max(len(out), 1)
	var sq float64
	var peak int32
	clip := 0
	bytes := make([]byte, 0, len(out)*2)
	for _, s := range out {
		a := absI32(int32(s))
		sq += float64(float64(s) * float64(s))
		peak = max(peak, a)
		if a >= 32767 {
			clip++
		}
		bytes = binary.LittleEndian.AppendUint16(bytes, uint16(s))
	}
	return goldenDigestT{
		samples:  len(out),
		rms:      float32(math.Sqrt(sq/float64(n)) / 32768.0),
		peak:     float32(peak) / 32768.0,
		clipPct:  float32(100.0) * float32(clip) / float32(n),
		checksum: goldenFnv1a(bytes),
	}
}

// Golden digest of the validated codec (regenerate with VOIP_GOLDEN=1; see module docs). The
// scalars are tolerant and platform-robust; the checksum is exact and stable on the CI target.
const goldenSamples = 23040
const goldenClip float32 = 0.0

// The scalars are tolerant and platform-robust; the checksums are exact, and exact means exact for
// one build: the encoder resolves ties on f32 comparisons, so the bitstream is a function of the
// arithmetic the compiler emits, not only of the source. Changing `-C target-cpu` alone moves these
// constants. They are a drift tripwire on the CI target, not a portable identity of the codec.
//
// Go port: Rust's golden.rs pins 0x5f7a_a0e3_d0d9_5e7e / 0x83de_fbfb_b2b8_68b2 (rms 0.155180, peak
// 0.513214), the values of its Linux CI build; its own build on aarch64 macOS already lands
// elsewhere. The port computes its transcendentals with its own deterministic functions
// (detmath.go), so its output is the same on every platform Go runs on, and the constants below
// are the port's: the checksums are its drift tripwire, and the scalars keep Rust's tolerances.
// Regenerate with VOIP_GOLDEN=1 go test -run TestGolden_GoldenRoundtripNoRegression -v.
const goldenRms float32 = 0.155128
const goldenPeak float32 = 0.472565
const goldenChecksum uint64 = 0xaea7_3f00_c237_660b

// goldenFramesChecksum pins the ENCODER bitstream independently of the decoder: fnv1a over the
// concatenation of every emitted packet's wire bytes. Drift here means the encoder changed even if
// decode output did not.
const goldenFramesChecksum uint64 = 0xb360_3295_089c_d068

// goldenRoundtrip encodes then decodes the whole corpus, returning the decoded i16 output and the
// concatenated packet bytes.
func goldenRoundtrip(t testing.TB, input []float32) ([]int16, []byte) {
	t.Helper()
	enc := NewEncoder()
	dec := NewDecoder()
	var out []int16
	var pktBytes []byte
	for off := 0; off < len(input); off += goldenFrameSamps {
		frame := input[off:min(off+goldenFrameSamps, len(input))]
		if len(frame) < goldenFrameSamps {
			break
		}
		pkt, err := enc.Encode(frame)
		if err != nil {
			t.Fatalf("encode: %v", err)
		}
		pktBytes = append(pktBytes, pkt...)
		for _, s := range dec.Decode(pkt) {
			out = append(out, f32ToI16(clampF32(s*32767.0, -32768.0, 32767.0)))
		}
		// The clean synthetic corpus must never make the range decoder raise its error flag.
		if dec.hadError() {
			t.Fatalf("clean corpus raised the range decoder error flag")
		}
	}
	return out, pktBytes
}

// goldenSnrDb is the signal-to-difference ratio in dB, treating `a` as the signal and `a - b` as
// the error.
func goldenSnrDb(a, b []int16) float64 {
	var sig, errSum float64
	for i := 0; i < min(len(a), len(b)); i++ {
		x, y := a[i], b[i]
		sig += float64(float64(x) * float64(x))
		d := float64(x) - float64(y)
		errSum += float64(d * d)
	}
	if errSum == 0.0 {
		return math.Inf(1)
	}
	return 10.0 * math.Log10(sig/errSum)
}

func TestGolden_GoldenRoundtripNoRegression(t *testing.T) {
	input := goldenSynthInput()
	out, pktBytes := goldenRoundtrip(t, input)
	d := goldenDigest(out)
	framesChecksum := goldenFnv1a(pktBytes)

	if _, ok := os.LookupEnv("VOIP_GOLDEN"); ok {
		t.Logf(
			"GOLDEN_SAMPLES=%d GOLDEN_RMS=%.6f GOLDEN_PEAK=%.6f GOLDEN_CLIP=%.4f GOLDEN_CHECKSUM=%#018x GOLDEN_FRAMES_CHECKSUM=%#018x",
			d.samples, d.rms, d.peak, d.clipPct, d.checksum, framesChecksum,
		)
		return
	}

	if d.samples != goldenSamples {
		t.Fatalf("output length drifted: %d != %d", d.samples, goldenSamples)
	}
	if !(absF32(d.rms-goldenRms) < 1e-3) {
		t.Fatalf("RMS drifted %.6f vs golden %.6f", d.rms, goldenRms)
	}
	if !(absF32(d.peak-goldenPeak) < 1e-3) {
		t.Fatalf("peak drifted %.6f vs golden %.6f", d.peak, goldenPeak)
	}
	if !(absF32(d.clipPct-goldenClip) < 0.1) {
		t.Fatalf("clip drifted %.4f vs golden %.4f", d.clipPct, goldenClip)
	}
	if d.checksum != goldenChecksum {
		t.Fatalf("output bytes drifted (codec regression): checksum %#018x, golden %#018x. scalars rms=%.6f peak=%.6f clip=%.4f. If intentional, regenerate the golden with VOIP_GOLDEN=1.",
			d.checksum, goldenChecksum, d.rms, d.peak, d.clipPct)
	}
	if framesChecksum != goldenFramesChecksum {
		t.Fatalf("encoder bitstream drifted (encoder regression): checksum %#018x, golden %#018x. If intentional, regenerate the golden with VOIP_GOLDEN=1.",
			framesChecksum, goldenFramesChecksum)
	}
}

// The encoder resolves discrete choices -- pulse positions, codebook indices, voicing -- on f32
// comparisons that routinely sit on ties, so the last bit of any intermediate decides which branch
// wins for a whole subframe. Perturbing the INPUT by one ULP, with the encoder byte-for-byte
// unchanged, is enough: the decoded output then lands 21-27 dB from the unperturbed one, with
// individual frames as low as ~10 dB, while still being a valid encoding of the same signal.
//
// This is what the exact checksums above do and do not mean. They pin one arithmetic path
// exactly, which is what makes them a useful tripwire; they say nothing about whether a different
// path is worse. Two encodings that land far apart on this measure can be equally good encodings
// of the same signal -- which is why a change of arithmetic is judged against the SOURCE, by
// perceptual measurement, and not against the previous bitstream.
//
// Asserting an upper bound on the SNR looks backwards, but it is the load-bearing half: if a
// one-ULP input change ever stopped moving the output, the tie-breaking would have changed and
// the reasoning above would no longer hold.
func TestGolden_EncoderOutputIsLastBitChaotic(t *testing.T) {
	input := goldenSynthInput()
	base, _ := goldenRoundtrip(t, input)
	for seed := uint32(1); seed <= 4; seed++ {
		near, _ := goldenRoundtrip(t, goldenPerturbOneUlp(input, seed))
		if len(near) != len(base) {
			t.Fatalf("seed %d: output length drifted", seed)
		}
		snr := goldenSnrDb(base, near)
		if !(snr >= 10.0 && snr < 40.0) {
			t.Fatalf("seed %d: one-ULP input change gave %.2f dB against the unperturbed output; "+
				"outside 10-40 dB the encoder is either no longer tie-sensitive or genuinely broken", seed, snr)
		}
	}
}
