// Checks the Go port against vectors the Rust codec wrote
// (zfcalls/tests/mlow_golden.rs, whatsapp-rust 5e72baf) over synthetic input,
// packet loss, multiframe envelopes, SplitRed, corrupted packets, every TOC
// byte, and encoder/decoder resets. The vectors (testdata/rust_golden.json)
// are committed data: zfcalls went with the Rust tree in WP9, so they can no
// longer be regenerated here, and go_encoded.json is no longer decoded by
// Rust's decoder.
//
// What must match exactly: the generated input (its hash), and the framing
// of every decode call (sample count, frame report, declared packet length).
// What matches within a tolerance: the decoded samples, which depend on the
// transcendentals (Rust calls the platform libm, the port its own
// detmath.go). What is held to Rust's quality: the Go encoder, whose
// packets differ from Rust's wherever a last-bit difference flips one of the
// encoder's many tie-breaking decisions. Its packets are written to
// testdata/go_encoded.json, which the Rust test decodes with Rust's decoder.

package mlow

import (
	"bytes"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"slices"
	"testing"
)

// The generators below mirror zfcalls/tests/mlow_golden.rs line for line.
// They use only f32 +, -, *, / and round, with every product that feeds a
// sum wrapped in float32() so Go cannot fuse it, so they produce the same
// bits as Rust.

const (
	goldenFrame         = 960
	goldenRate  float32 = 16000.0
	goldenPi    float32 = math.Pi
	goldenTwoPi         = 2 * goldenPi
)

type goldenLcg uint32

func (l *goldenLcg) next() uint32 {
	*l = *l*1664525 + 1013904223
	return uint32(*l)
}

func (l *goldenLcg) uniform() float32 {
	return float32(float32(l.next()>>8)/8388608.0) - 1.0
}

func goldenSin(x float32) float32 {
	k := roundF32(x / goldenTwoPi)
	r := x - float32(k*goldenTwoPi)
	r2 := r * r
	return r * (1.0 - float32(r2/6.0*(1.0-float32(r2/20.0*(1.0-float32(r2/42.0*(1.0-float32(r2/72.0*(1.0-r2/110.0)))))))))
}

func goldenAdvance(phase *float32, step float32) {
	*phase += step
	if *phase >= goldenPi {
		*phase -= goldenTwoPi
	}
}

func goldenTone(packets int, freq, amp float32) []float32 {
	step := goldenTwoPi * freq / goldenRate
	var ph float32
	out := make([]float32, packets*goldenFrame)
	for i := range out {
		out[i] = amp * goldenSin(ph)
		goldenAdvance(&ph, step)
	}
	return out
}

func goldenSweep(packets int, f0, f1, amp float32) []float32 {
	n := packets * goldenFrame
	var ph float32
	out := make([]float32, n)
	for i := range out {
		out[i] = amp * goldenSin(ph)
		f := f0 + float32((f1-f0)*(float32(i)/float32(n)))
		goldenAdvance(&ph, goldenTwoPi*f/goldenRate)
	}
	return out
}

func goldenNoise(packets int, amp float32, seed uint32) []float32 {
	lcg := goldenLcg(seed)
	out := make([]float32, packets*goldenFrame)
	for i := range out {
		out[i] = amp * lcg.uniform()
	}
	return out
}

func goldenSpeechLike(packets int, gaps []int, seed uint32) []float32 {
	lcg := goldenLcg(seed)
	var f0Ph, vibPh, envPh float32
	out := make([]float32, packets*goldenFrame)
	for i := range out {
		f0 := 140.0 + float32(30.0*goldenSin(vibPh))
		goldenAdvance(&vibPh, goldenTwoPi*3.0/goldenRate)
		goldenAdvance(&f0Ph, goldenTwoPi*f0/goldenRate)
		var v float32
		for k := 1; k <= 8; k++ {
			v += goldenSin(float32(f0Ph*float32(k))) / float32(k)
		}
		env := 0.5 + float32(0.5*goldenSin(envPh))
		goldenAdvance(&envPh, goldenTwoPi*4.0/goldenRate)
		gate := float32(1.0)
		if slices.Contains(gaps, i/goldenFrame) {
			gate = 0
		}
		out[i] = float32(0.12*v*env*gate) + float32(0.003*lcg.uniform())
	}
	return out
}

func goldenLoud(packets int) []float32 {
	step := goldenTwoPi * 200.0 / goldenRate
	var ph float32
	lcg := goldenLcg(7)
	out := make([]float32, packets*goldenFrame)
	for i := range out {
		v := float32(-1.5)
		if goldenSin(ph) >= 0 {
			v = 1.5
		}
		goldenAdvance(&ph, step)
		out[i] = v + float32(0.05*lcg.uniform())
	}
	return out
}

func goldenNonfinite(packets int) []float32 {
	x := goldenSpeechLike(packets, nil, 11)
	for i := range x {
		switch {
		case i%97 == 0:
			x[i] = float32(math.NaN())
		case i%193 == 0:
			x[i] = float32(math.Inf(1))
		case i%389 == 0:
			x[i] = float32(math.Inf(-1))
		}
	}
	return x
}

func goldenDcSteps(packets int) []float32 {
	out := make([]float32, packets*goldenFrame)
	for i := range out {
		switch i / goldenFrame {
		case 0, 1:
			out[i] = 0.2
		case 2:
			out[i] = -0.3
		default:
			if (i/160)%2 == 0 {
				out[i] = 0.25
			} else {
				out[i] = -0.25
			}
		}
	}
	return out
}

func goldenToI16(x []float32) []int16 {
	out := make([]int16, len(x))
	for i, s := range x {
		out[i] = f32ToI16(clampF32(roundF32(s*32767.0), -32768.0, 32767.0))
	}
	return out
}

func goldenFnv(b []byte) string {
	h := uint64(0xcbf29ce484222325)
	for _, c := range b {
		h ^= uint64(c)
		h *= 0x100000001b3
	}
	return fmt.Sprintf("%016x", h)
}

func goldenF32Bytes(x []float32) []byte {
	b := make([]byte, 4*len(x))
	for i, s := range x {
		binary.LittleEndian.PutUint32(b[4*i:], math.Float32bits(s))
	}
	return b
}

func goldenI16Bytes(x []int16) []byte {
	b := make([]byte, 2*len(x))
	for i, s := range x {
		binary.LittleEndian.PutUint16(b[2*i:], uint16(s))
	}
	return b
}

// Decoder tolerance against Rust's decoded samples: the largest absolute
// difference allowed on any sample, and the smallest SNR (Rust's output as
// the signal, the difference as the noise) over each stream or scenario.
const (
	goldenDecodeMaxAbsErr = 1e-4
	goldenDecodeMinSnrDb  = 90.0
)

// goldenPCMCmp compares the Go decoder's samples with Rust's, which
// rust_golden_pcm.f32 holds in record order.
type goldenPCMCmp struct {
	ref          []float32
	off          int
	maxAbs       float64
	sig, err     float64
	diffSamples  int
	totalSamples int
}

func (c *goldenPCMCmp) take(t *testing.T, where string, pcm []float32) {
	t.Helper()
	if c.off+len(pcm) > len(c.ref) {
		t.Fatalf("%s: rust_golden_pcm.f32 ends at sample %d", where, len(c.ref))
	}
	ref := c.ref[c.off : c.off+len(pcm)]
	c.off += len(pcm)
	for i, r := range ref {
		g := pcm[i]
		if math.IsNaN(float64(g)) || math.IsInf(float64(g), 0) {
			t.Fatalf("%s: sample %d is %v", where, i, g)
		}
		d := math.Abs(float64(g) - float64(r))
		if d > goldenDecodeMaxAbsErr {
			t.Fatalf("%s: sample %d is %g, Rust %g (|diff| %.3g > %g)", where, i, g, r, d, goldenDecodeMaxAbsErr)
		}
		c.maxAbs = max(c.maxAbs, d)
		c.sig += float64(r) * float64(r)
		c.err += d * d
		if g != r {
			c.diffSamples++
		}
	}
	c.totalSamples += len(pcm)
}

// snrDb is the SNR of everything taken since the last reset.
func (c *goldenPCMCmp) snrDb() float64 {
	if c.err == 0 {
		return math.Inf(1)
	}
	return 10 * math.Log10(c.sig/c.err)
}

func (c *goldenPCMCmp) report(t *testing.T, name string) {
	t.Helper()
	snr := c.snrDb()
	t.Logf("%-17s %6d samples, %5d not bit-identical, max |diff| %.3g, SNR %.1f dB",
		name, c.totalSamples, c.diffSamples, c.maxAbs, snr)
	if c.sig > 0 && snr < goldenDecodeMinSnrDb {
		t.Errorf("%s: decoder SNR against Rust %.1f dB < %.0f dB", name, snr, goldenDecodeMinSnrDb)
	}
	c.maxAbs, c.sig, c.err, c.diffSamples, c.totalSamples = 0, 0, 0, 0, 0
}

// goldenCheckDecode decodes payload and compares it with one Rust record:
// the framing exactly, the samples through cmp.
func goldenCheckDecode(t *testing.T, where string, dec *Decoder, payload []byte, rec val, cmp *goldenPCMCmp) {
	t.Helper()
	pcm := dec.Decode(payload)
	r := dec.TakeFrameReport()
	if got, want := len(pcm), rec.Get("len").Int(); got != want {
		t.Fatalf("%s: %d samples, Rust %d", where, got, want)
	}
	rep := rec.Get("report")
	got := [4]int{int(r.Decoded), int(r.Concealed), int(r.OffPoint), int(r.InactiveOrSID)}
	want := [4]int{rep.At(0).Int(), rep.At(1).Int(), rep.At(2).Int(), rep.At(3).Int()}
	if got != want {
		t.Errorf("%s: report %v, Rust %v", where, got, want)
	}
	if got, want := dec.LastPacketSamples(), rec.Get("last_packet_samps").Int(); got != want {
		t.Errorf("%s: last packet %d samples, Rust %d", where, got, want)
	}
	cmp.take(t, where, pcm)
}

func goldenPackets(t *testing.T, v val) [][]byte {
	t.Helper()
	var out [][]byte
	for _, p := range v.Arr() {
		out = append(out, p.Hex())
	}
	return out
}

func goldenStreams() map[string][]float32 {
	return map[string][]float32{
		"silence":   make([]float32, 8*goldenFrame),
		"tone440":   goldenTone(10, 440.0, 0.3),
		"sweep":     goldenSweep(16, 100.0, 7000.0, 0.25),
		"noise":     goldenNoise(10, 0.1, 1),
		"speech":    goldenSpeechLike(24, []int{8, 9, 10, 11, 18, 19, 20}, 3),
		"loud":      goldenLoud(6),
		"nonfinite": goldenNonfinite(4),
		"dc_steps":  goldenDcSteps(6),
	}
}

func goldenSpeech16() []int16 {
	x := goldenSpeechLike(12, []int{5, 6}, 5)
	for i := range x {
		x[i] *= 2.5
	}
	return goldenToI16(x)
}

// TestRustGolden_Inputs checks the generators rebuild Rust's input bits.
func TestRustGolden_Inputs(t *testing.T) {
	doc := loadJSON(t, "rust_golden.json")
	streams := goldenStreams()
	for _, s := range doc.Get("streams").Arr() {
		name := s.Get("name").Str()
		var h string
		if s.Get("api").Str() == "i16" {
			h = goldenFnv(goldenI16Bytes(goldenSpeech16()))
		} else {
			h = goldenFnv(goldenF32Bytes(streams[name]))
		}
		if want := s.Get("input_fnv").Str(); h != want {
			t.Errorf("%s: input hash %s, Rust %s: the generator port drifted", name, h, want)
		}
	}
}

// TestRustGolden_DecoderMatchesRust decodes every packet Rust wrote, in the
// order Rust decoded them: its own encoder's streams, then the decoder
// scenarios.
func TestRustGolden_DecoderMatchesRust(t *testing.T) {
	doc := loadJSON(t, "rust_golden.json")
	cmp := &goldenPCMCmp{ref: f32le(testdataBytes(t, "rust_golden_pcm.f32"))}
	var all goldenPCMCmp
	acc := func(c *goldenPCMCmp) {
		all.maxAbs = max(all.maxAbs, c.maxAbs)
		all.sig += c.sig
		all.err += c.err
		all.diffSamples += c.diffSamples
		all.totalSamples += c.totalSamples
	}
	for _, s := range doc.Get("streams").Arr() {
		name := s.Get("name").Str()
		packets := goldenPackets(t, s.Get("packets"))
		dec := NewDecoder()
		for i, rec := range s.Get("decoded").Arr() {
			goldenCheckDecode(t, fmt.Sprintf("%s decode %d", name, i), dec, packets[i], rec, cmp)
		}
		if dec.hadError() {
			t.Errorf("%s: the range decoder raised its error flag", name)
		}
		acc(cmp)
		cmp.report(t, name)
	}
	for _, s := range doc.Get("decoder_scenarios").Arr() {
		name := s.Get("name").Str()
		dec := NewDecoder()
		dec.SetRedundancy(int32(s.Get("redundancy").I64()))
		resetAt := -1
		if !s.Get("reset_at").IsNull() {
			resetAt = s.Get("reset_at").Int()
		}
		payloads := goldenPackets(t, s.Get("payloads"))
		for i, rec := range s.Get("outputs").Arr() {
			if i == resetAt {
				dec.Reset()
			}
			goldenCheckDecode(t, fmt.Sprintf("%s %d", name, i), dec, payloads[i], rec, cmp)
		}
		acc(cmp)
		cmp.report(t, name)
	}
	if cmp.off != len(cmp.ref) {
		t.Errorf("decoded %d samples, rust_golden_pcm.f32 holds %d", cmp.off, len(cmp.ref))
	}
	all.report(t, "all")
}

// goldenCodecDelay is the codec's delay from input to decoded output, in
// samples: where the decoded noise stream correlates best with its input.
const goldenCodecDelay = 80

// goldenSnrMarginDb is how far below Rust's own encoder the Go encoder's
// spectral SNR may land (the Rust test uses the same margin).
const goldenSnrMarginDb = 1.0

// goldenClean is the encoder's view of an input sample: NaN is zero, the
// rest is clamped to full scale.
func goldenClean(x float32) float64 {
	if x != x {
		return 0
	}
	return math.Max(-1, math.Min(1, float64(x)))
}

// goldenSpectralSnrDb is the spectral SNR in dB of output (delayed by
// goldenCodecDelay) against input: 320-sample Hann frames at a 160-sample
// hop, signal = sum of |X|^2, noise = sum of (|X| - |Y|)^2 over bins
// 0..160. A CELP codec with noise fill does not keep the waveform's phase,
// so this compares the magnitude spectra. ok is false for a silent input.
// zfcalls/tests/mlow_golden.rs computes the same measure.
func goldenSpectralSnrDb(input, output []float32) (snr float64, ok bool) {
	const n = 320
	const bins = n/2 + 1
	cosT := make([]float64, n*bins)
	sinT := make([]float64, n*bins)
	for k := range bins {
		for j := range n {
			a := 2 * math.Pi * float64((k*j)%n) / n
			cosT[k*n+j], sinT[k*n+j] = math.Cos(a), math.Sin(a)
		}
	}
	var win, x, y [n]float64
	for j := range win {
		win[j] = 0.5 - 0.5*math.Cos(2*math.Pi*float64(j)/n)
	}
	mag := func(v *[n]float64, k int) float64 {
		var re, im float64
		for j := range n {
			re += v[j] * cosT[k*n+j]
			im += v[j] * sinT[k*n+j]
		}
		return math.Hypot(re, im)
	}
	var sig, noise float64
	for off := 0; off+n <= len(input) && off+goldenCodecDelay+n <= len(output); off += n / 2 {
		for j := range n {
			x[j] = win[j] * goldenClean(input[off+j])
			y[j] = win[j] * float64(output[off+goldenCodecDelay+j])
		}
		for k := range bins {
			mx, my := mag(&x, k), mag(&y, k)
			sig += mx * mx
			noise += (mx - my) * (mx - my)
		}
	}
	if sig == 0 {
		return 0, false
	}
	return 10 * math.Log10(sig/noise), true
}

// goldenDecodeClean decodes bare packets with the Go decoder, requiring each
// to decode as three coded frames with nothing concealed or refused.
func goldenDecodeClean(t *testing.T, who string, packets [][]byte) []float32 {
	t.Helper()
	dec := NewDecoder()
	var pcm []float32
	for i, p := range packets {
		out := dec.Decode(p)
		r := dec.TakeFrameReport()
		if len(out) != goldenFrame || r.Decoded != 3 || r.Concealed != 0 || r.OffPoint != 0 {
			t.Fatalf("%s: packet %d decoded to %d samples, report %+v", who, i, len(out), r)
		}
		pcm = append(pcm, out...)
	}
	if dec.hadError() {
		t.Fatalf("%s: the range decoder raised its error flag", who)
	}
	return pcm
}

// goldenGoEncoded is one entry of testdata/go_encoded.json.
type goldenGoEncoded struct {
	Name    string   `json:"name"`
	Packets []string `json:"packets"`
}

type goldenGoEncodedDoc struct {
	Source  string            `json:"source"`
	Streams []goldenGoEncoded `json:"streams"`
}

func goldenEncodeF32(t *testing.T, enc *Encoder, f []float32) []byte {
	t.Helper()
	p, err := enc.Encode(f)
	if err != nil {
		t.Fatal(err)
	}
	return p
}

func goldenEncodeI16(t *testing.T, enc *Encoder, f []int16) []byte {
	t.Helper()
	p, err := enc.EncodeInt16(f)
	if err != nil {
		t.Fatal(err)
	}
	return p
}

// goldenGoEncodedCases encodes every Rust stream and encoder scenario with
// the Go encoder, returning each one's input as the encoder sees it (for the
// SNR) and its packets.
func goldenGoEncodedCases(t *testing.T, doc val) (names []string, inputs map[string][]float32, packets map[string][][]byte) {
	streams := goldenStreams()
	speech16 := goldenSpeech16()
	speech16Norm := make([]float32, len(speech16))
	for i, s := range speech16 {
		speech16Norm[i] = float32(s) / 32768.0
	}
	inputs = map[string][]float32{}
	packets = map[string][][]byte{}
	for _, s := range doc.Get("streams").Arr() {
		name := s.Get("name").Str()
		enc := NewEncoder()
		var got [][]byte
		if s.Get("api").Str() == "i16" {
			for f := range slices.Chunk(speech16, goldenFrame) {
				got = append(got, goldenEncodeI16(t, enc, f))
			}
			inputs[name] = speech16Norm
		} else {
			for f := range slices.Chunk(streams[name], goldenFrame) {
				got = append(got, goldenEncodeF32(t, enc, f))
			}
			inputs[name] = streams[name]
		}
		names = append(names, name)
		packets[name] = got
	}
	for _, s := range doc.Get("encoder_scenarios").Arr() {
		name := s.Get("name").Str()
		enc := NewEncoder()
		var got [][]byte
		switch name {
		case "reset_at_6":
			speech := streams["speech"]
			for i := range 12 {
				if i == 6 {
					enc.Reset()
				}
				got = append(got, goldenEncodeF32(t, enc, speech[i*goldenFrame:(i+1)*goldenFrame]))
			}
			inputs[name] = speech[:12*goldenFrame]
		case "alternating_apis":
			for i := 0; i*goldenFrame < len(speech16); i++ {
				if i%2 == 0 {
					got = append(got, goldenEncodeI16(t, enc, speech16[i*goldenFrame:(i+1)*goldenFrame]))
				} else {
					got = append(got, goldenEncodeF32(t, enc, speech16Norm[i*goldenFrame:(i+1)*goldenFrame]))
				}
			}
			inputs[name] = speech16Norm
		default:
			t.Fatalf("unknown encoder scenario %q", name)
		}
		names = append(names, name)
		packets[name] = got
	}
	return names, inputs, packets
}

// TestRustGolden_GoEncoder holds the Go encoder to Rust's: on every stream
// and encoder scenario its packets must decode cleanly and reach the
// spectral SNR Rust's packets reach (both through the Go decoder, which
// TestRustGolden_DecoderMatchesRust ties to Rust's), less
// goldenSnrMarginDb. It also checks testdata/go_encoded.json, which
// zfcalls/tests/mlow_golden.rs decoded with Rust's decoder until WP9, is
// current; ZF_WRITE_MLOW_GO=1 rewrites it.
func TestRustGolden_GoEncoder(t *testing.T) {
	doc := loadJSON(t, "rust_golden.json")
	rust := map[string][][]byte{}
	for _, s := range doc.Get("streams").Arr() {
		rust[s.Get("name").Str()] = goldenPackets(t, s.Get("packets"))
	}
	for _, s := range doc.Get("encoder_scenarios").Arr() {
		rust[s.Get("name").Str()] = goldenPackets(t, s.Get("packets"))
	}
	names, inputs, packets := goldenGoEncodedCases(t, doc)

	out := goldenGoEncodedDoc{Source: "the Go port's Encoder (gocore/voip/mlow, TestRustGolden_GoEncoder) over the inputs of rust_golden.json"}
	for _, name := range names {
		e := goldenGoEncoded{Name: name}
		for _, p := range packets[name] {
			e.Packets = append(e.Packets, hex.EncodeToString(p))
		}
		out.Streams = append(out.Streams, e)
	}
	js, err := json.MarshalIndent(out, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	js = append(js, '\n')
	path := filepath.Join("testdata", "go_encoded.json")
	if _, ok := os.LookupEnv("ZF_WRITE_MLOW_GO"); ok {
		if err := os.WriteFile(path, js, 0o644); err != nil {
			t.Fatal(err)
		}
	} else if committed, err := os.ReadFile(path); err != nil || !bytes.Equal(committed, js) {
		t.Errorf("testdata/go_encoded.json is not the Go encoder's current output (%v): rewrite it with ZF_WRITE_MLOW_GO=1 and rerun the Rust test", err)
	}

	for _, name := range names {
		got, want := packets[name], rust[name]
		if len(got) != len(want) {
			t.Fatalf("%s: %d packets, Rust %d", name, len(got), len(want))
		}
		same := 0
		for i := range got {
			if bytes.Equal(got[i], want[i]) {
				same++
			}
		}
		goPCM := goldenDecodeClean(t, name+" (Go encoder)", got)
		rustPCM := goldenDecodeClean(t, name+" (Rust encoder)", want)
		g, okG := goldenSpectralSnrDb(inputs[name], goPCM)
		r, okR := goldenSpectralSnrDb(inputs[name], rustPCM)
		switch {
		case okG && okR:
			t.Logf("%-16s spectral SNR Go encoder %6.2f dB, Rust encoder %6.2f dB; %d/%d packets byte-identical",
				name, g, r, same, len(got))
			if g < r-goldenSnrMarginDb {
				t.Errorf("%s: Go encoder %.2f dB, Rust encoder %.2f dB", name, g, r)
			}
		case !okG && !okR:
			rms := qualityMetricsRms(goPCM)
			t.Logf("%-16s silent input, Go encoder output rms %.2e; %d/%d packets byte-identical", name, rms, same, len(got))
			if rms >= 3.2e-4 { // -70 dBFS
				t.Errorf("%s: silence decoded to rms %.2e, above -70 dBFS", name, rms)
			}
		default:
			t.Fatalf("%s: input silent for one encoder only", name)
		}
	}
}
