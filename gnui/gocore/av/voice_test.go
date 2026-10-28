package av

import (
	"bufio"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"slices"
	"testing"

	"zapfast/abi"
	"zapfast/core"
	"zapfast/core/fake"
)

// lcg is voice_rs_golden.rs's generator: samples exactly representable in
// float32, so Go and Rust start from the same bits.
type lcg uint32

func (r *lcg) next() float32 {
	*r = *r*1664525 + 1013904223
	return float32(uint32(*r)>>8)*(1.0/8388608.0) - 1
}

func (r *lcg) take(n int, scale float32) []float32 {
	out := make([]float32, n)
	for i := range out {
		out[i] = r.next() * scale
	}
	return out
}

func fnv(samples []float32) uint64 {
	h := uint64(0xcbf29ce484222325)
	for _, s := range samples {
		var b [4]byte
		binary.LittleEndian.PutUint32(b[:], math.Float32bits(s))
		for _, c := range b {
			h ^= uint64(c)
			h *= 0x100000001b3
		}
	}
	return h
}

// TestVoiceMatchesUpstream runs Waveform, Normalize and MonoAtRate on the
// inputs testdata/voice_rs_golden.rs gave upstream's voice.rs, and compares
// the results bit for bit.
func TestVoiceMatchesUpstream(t *testing.T) {
	f, err := os.Open("testdata/voice_rs.json")
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	want := map[string]map[string]json.RawMessage{}
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		var row map[string]json.RawMessage
		if err := json.Unmarshal(sc.Bytes(), &row); err != nil {
			t.Fatal(err)
		}
		var name string
		json.Unmarshal(row["case"], &name)
		want[name] = row
	}
	check := func(name, field string, got any) {
		t.Helper()
		row, ok := want[name]
		if !ok {
			t.Fatalf("no golden %q", name)
		}
		g, _ := json.Marshal(got)
		var a, b any
		json.Unmarshal(row[field], &a)
		json.Unmarshal(g, &b)
		if fmt.Sprint(a) != fmt.Sprint(b) {
			t.Errorf("%s %s: got %s, want %s", name, field, g, row[field])
		}
	}
	bars := func(s []float32) []int {
		w := Waveform(s)
		out := make([]int, len(w))
		for i, b := range w {
			out[i] = int(b)
		}
		return out
	}

	r := lcg(1)
	n := 48000
	ramp := r.take(n, 1)
	for i := range ramp {
		ramp[i] *= float32(i) / float32(n)
	}
	check("waveform ramp", "bars", bars(ramp))
	for _, l := range []int{1, 2, 63, 64, 65, 100, 4097} {
		check(fmt.Sprintf("waveform noise %d", l), "bars", bars(r.take(l, 0.3)))
	}
	gaps := r.take(9600, 0.5)
	clear(gaps[2000:5000])
	check("waveform gaps", "bars", bars(gaps))
	check("waveform silence", "bars", bars(make([]float32, 1000)))
	check("waveform empty", "bars", bars(nil))

	for _, c := range []struct {
		name  string
		scale float32
	}{{"quiet", 0.05}, {"faint", 0.001}, {"loud", 0.95}, {"silent", 0}} {
		s := r.take(4800, c.scale)
		Normalize(s)
		check("normalize "+c.name, "len", len(s))
		check("normalize "+c.name, "fnv", fnv(s))
		check("normalize "+c.name, "first", math.Float32bits(s[0]))
	}
	for _, c := range []struct {
		name           string
		channels, rate int
	}{{"stereo 44100", 2, 44100}, {"three 16000", 3, 16000}, {"mono 48000", 1, 48000}, {"mono 96000", 1, 96000}, {"stereo 8000", 2, 8000}} {
		m := MonoAtRate(r.take(9001, 0.8), c.channels, c.rate)
		check("mono "+c.name, "len", len(m))
		check("mono "+c.name, "fnv", fnv(m))
	}
}

func tone(seconds float64) []float32 {
	out := make([]float32, int(seconds*Rate))
	for i := range out {
		out[i] = float32(0.5 * math.Sin(float64(i)*440*2*math.Pi/Rate))
	}
	return out
}

func rms(s []float32) float64 {
	var sum float64
	for _, v := range s {
		sum += float64(v) * float64(v)
	}
	return math.Sqrt(sum / float64(len(s)))
}

// The tests below are upstream's voice.rs tests.

func TestTheWaveformFollowsTheLoudness(t *testing.T) {
	s := tone(1)
	for i := range s {
		s[i] *= float32(i) / float32(len(s))
	}
	b := Waveform(s)
	if b[Bars-1] != 100 || b[0] >= 10 {
		t.Fatal(b)
	}
	for i := 1; i < Bars; i++ {
		if b[i-1] > b[i]+2 {
			t.Fatal(b)
		}
	}
	if Waveform(nil) != [Bars]byte{} {
		t.Fatal("empty")
	}
}

func TestAnyInputBecomesMonoAt48k(t *testing.T) {
	stereo := make([]float32, 44100*2)
	for i := 0; i < len(stereo); i += 2 {
		stereo[i] = 1
	}
	mono := MonoAtRate(stereo, 2, 44100)
	if d := len(mono) - 48000; d < -2 || d > 2 {
		t.Fatal(len(mono))
	}
	for _, v := range mono {
		if math.Abs(float64(v)-0.5) > 1e-6 {
			t.Fatal(v)
		}
	}
	same := []float32{0.25, 0.25, 0.25}
	if got := MonoAtRate(same, 1, Rate); !slices.Equal(got, same) {
		t.Fatal(got)
	}
}

func TestAQuietRecordingIsBroughtUpButNotBlasted(t *testing.T) {
	quiet := tone(0.1)
	for i := range quiet {
		quiet[i] *= 0.2
	}
	Normalize(quiet)
	var peak float32
	for _, v := range quiet {
		peak = max(peak, abs32(v))
	}
	if math.Abs(float64(peak)-0.89) > 0.01 {
		t.Fatal(peak)
	}
	faint := []float32{0.001, -0.002}
	Normalize(faint)
	if math.Abs(float64(faint[1])+0.02) > 1e-6 {
		t.Fatal(faint)
	}
	loud := []float32{0.95}
	Normalize(loud)
	if loud[0] != 0.95 {
		t.Fatal(loud)
	}
	Normalize(nil)
}

func needOpus(t *testing.T) {
	t.Helper()
	if _, err := newOpusEncoder(); errors.Is(err, ErrUnavailable) {
		t.Skip(err)
	}
}

func TestAClipSurvivesTheTripThroughOpus(t *testing.T) {
	needOpus(t)
	original := tone(1)
	ogg, err := EncodeOpus(original)
	if err != nil {
		t.Fatal(err)
	}
	if string(ogg[:4]) != "OggS" {
		t.Fatal("not Ogg")
	}
	back, err := DecodeOpus(ogg)
	if err != nil {
		t.Fatal(err)
	}
	if d := len(back) - len(original); d < -frame || d > frame {
		t.Fatalf("%d samples back", len(back))
	}
	end := min(len(back), len(original))
	if before, after := rms(original[4800:end]), rms(back[4800:end]); math.Abs(before-after) > 0.08 {
		t.Fatalf("%v in, %v out", before, after)
	}
	// Sample by sample, once the codec has settled: the pre-skip lines the
	// output up with the input to within a few samples.
	var diff float64
	for i := 4800; i < end; i++ {
		diff = max(diff, math.Abs(float64(back[i]-original[i])))
	}
	if diff > 0.2 {
		t.Fatalf("the decoded tone is %v off the original", diff)
	}
}

// TestTheStreamIsUpstreams checks the Ogg layout upstream's encode writes:
// the two headers on pages of their own, 50 packets a page, granules in
// 20 ms steps, and the last granule the pre-skip and the samples.
func TestTheStreamIsUpstreams(t *testing.T) {
	needOpus(t)
	samples := tone(1.5)
	ogg, err := EncodeOpus(samples)
	if err != nil {
		t.Fatal(err)
	}
	var packets, pre int
	var granules []uint64
	err = oggPackets(ogg, func(p []byte, g uint64, eos bool) error {
		packets++
		switch packets {
		case 1:
			if string(p[:8]) != "OpusHead" || p[8] != 1 || p[9] != 1 || binary.LittleEndian.Uint32(p[12:]) != Rate {
				t.Fatalf("head %x", p)
			}
			pre = int(binary.LittleEndian.Uint16(p[10:]))
			if g != 0 {
				t.Fatal("head granule", g)
			}
		case 2:
			if string(p) != string(opusTags()) || g != 0 {
				t.Fatalf("tags %q %d", p, g)
			}
		default:
			if n, ok := packetSamples(p); !ok || n != frame {
				t.Fatalf("packet %d: %d samples", packets, n)
			}
			if g != noGranule {
				granules = append(granules, g)
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	audio := packets - 2
	if want := (len(samples) + pre + frame - 1) / frame; audio != want {
		t.Fatalf("%d packets, want %d", audio, want)
	}
	// Pages end after packets 50 and 100 and at the last (76).
	if !slices.Equal(granules, []uint64{50 * frame, uint64(pre + len(samples))}) {
		t.Fatal(granules)
	}
	if pre != 312 {
		t.Logf("pre-skip %d (libopus's VoIP lookahead is 312)", pre)
	}
	if g, ok := lastGranule(ogg); !ok || g != uint64(pre+len(samples)) {
		t.Fatal(g, ok)
	}
}

func TestWhatIsNotOpusIsRefused(t *testing.T) {
	for _, b := range [][]byte{[]byte("not an ogg file at all"), nil} {
		if _, err := DecodeOpus(b); !errors.Is(err, ErrMalformed) {
			t.Fatal(err)
		}
	}
	// Ogg, but not Opus.
	w := &oggWriter{serial: 7}
	w.packet([]byte("\x01vorbis........................"), 0, true, true)
	if _, err := DecodeOpus(w.out); !errors.Is(err, ErrMalformed) {
		t.Fatal(err)
	}
	needOpus(t)
	ogg, err := EncodeOpus(tone(0.2))
	if err != nil {
		t.Fatal(err)
	}
	damaged := slices.Clone(ogg)
	damaged[len(damaged)-10] ^= 0xff
	if _, err := DecodeOpus(damaged); !errors.Is(err, ErrMalformed) {
		t.Fatalf("a flipped byte: %v", err)
	}
}

func TestPrepareVoice(t *testing.T) {
	needOpus(t)
	quiet := tone(2.4)
	for i := range quiet {
		quiet[i] *= 0.2
	}
	v, err := PrepareVoice(quiet)
	if err != nil {
		t.Fatal(err)
	}
	if v.Seconds != 2 {
		t.Fatal(v.Seconds)
	}
	if quiet[100] != tone(2.4)[100]*0.2 {
		t.Fatal("the caller's samples were changed")
	}
	norm := slices.Clone(quiet)
	Normalize(norm)
	if v.Waveform != Waveform(norm) {
		t.Fatal("the waveform is not the normalized recording's")
	}
	back, err := DecodeOpus(v.Ogg)
	if err != nil {
		t.Fatal(err)
	}
	if r := rms(back[4800:100000]); r < 0.5 || r > 0.75 {
		t.Fatalf("normalized to about 0.89 peak, got RMS %v", r)
	}
	if v, err := PrepareVoice(nil); err != nil || v.Seconds != 1 {
		t.Fatal(v.Seconds, err)
	}
	if _, err := PrepareVoice(make([]float32, 15*60*Rate+1)); !errors.Is(err, ErrTooLong) {
		t.Fatal(err)
	}
}

func TestSamplesFromPCM(t *testing.T) {
	s := []float32{0.5, -0.25, 1}
	got, err := SamplesFromPCM(PCM(s))
	if err != nil || !slices.Equal(got, s) {
		t.Fatal(got, err)
	}
	if _, err := SamplesFromPCM(make([]byte, 7)); !errors.Is(err, ErrMalformed) {
		t.Fatal(err)
	}
	if _, err := SamplesFromPCM(make([]byte, 4*15*60*Rate+4)); !errors.Is(err, ErrTooLong) {
		t.Fatal(err)
	}
}

// TestDecodeVoiceFile is zfcore's voice.rs test.
func TestDecodeVoiceFile(t *testing.T) {
	needOpus(t)
	root := t.TempDir()
	account := filepath.Join(root, "account")
	if err := os.MkdirAll(filepath.Join(account, "media"), 0o755); err != nil {
		t.Fatal(err)
	}
	note := filepath.Join(account, "media/note.ogg")
	ogg, err := EncodeOpus(tone(1.5))
	if err != nil {
		t.Fatal(err)
	}
	os.WriteFile(note, ogg, 0o644)
	roots := []string{account}

	d, err := DecodeVoiceFile(note, roots)
	if err != nil {
		t.Fatal(err)
	}
	// 1.5 s at 48 kHz, the pre-skip trimmed; the last 20 ms frame is whole,
	// as upstream's decode leaves it.
	if n := len(d.Samples); n < 72000 || n >= 72960 {
		t.Fatal(n)
	}
	for _, b := range d.Waveform {
		if b <= 50 {
			t.Fatal(d.Waveform)
		}
	}
	var peak float32
	for _, v := range d.Samples[4800 : 4800+48] {
		peak = max(peak, abs32(v))
	}
	if peak < 0.3 || peak > 0.7 {
		t.Fatalf("the tone's level, got %v", peak)
	}

	outside := filepath.Join(root, "elsewhere.ogg")
	os.WriteFile(outside, ogg, 0o644)
	link := filepath.Join(account, "media/link.ogg")
	os.Symlink(outside, link)
	text := filepath.Join(account, "media/note.txt")
	os.WriteFile(text, []byte("not a voice note"), 0o644)
	for _, c := range []struct {
		path string
		want error
	}{
		{outside, ErrOutside},
		{link, ErrOutside},
		{filepath.Join(account, "media/../../elsewhere.ogg"), ErrOutside},
		{filepath.Join(account, "media/gone.ogg"), ErrNotFound},
		{account, ErrOutside},
		{text, ErrMalformed},
	} {
		if _, err := DecodeVoiceFile(c.path, roots); !errors.Is(err, c.want) {
			t.Errorf("%s: %v, want %v", c.path, err, c.want)
		}
	}
}

// TestAStreamThatSaysItIsTooLongIsNotDecoded is zfcore's: the last page's
// granule says 30 minutes and 2 seconds.
func TestAStreamThatSaysItIsTooLongIsNotDecoded(t *testing.T) {
	needOpus(t)
	ogg, err := EncodeOpus(tone(0.1))
	if err != nil {
		t.Fatal(err)
	}
	at := 0
	for i := len(ogg) - 4; i >= 0; i-- {
		if string(ogg[i:i+4]) == "OggS" {
			at = i
			break
		}
	}
	binary.LittleEndian.PutUint64(ogg[at+6:], uint64(30*60+2)*Rate)
	dir := t.TempDir()
	p := filepath.Join(dir, "long.ogg")
	os.WriteFile(p, ogg, 0o644)
	if _, err := DecodeVoiceFile(p, []string{dir}); !errors.Is(err, ErrTooLong) {
		t.Fatal(err)
	}
}

// TestVoiceDecoderContract holds VoiceDecoder to what core/fake's
// TestDecodeVoice expects of a core.VoiceDecoder, on a real note.
func TestVoiceDecoderContract(t *testing.T) {
	needOpus(t)
	dir := t.TempDir()
	note := filepath.Join(dir, "note.ogg")
	samples := fake.Tone(fake.VoiceSeconds, 440)
	ogg, err := EncodeOpus(samples)
	if err != nil {
		t.Fatal(err)
	}
	os.WriteFile(note, ogg, 0o644)
	bad := filepath.Join(dir, "bad.ogg")
	os.WriteFile(bad, []byte("not a voice note"), 0o644)
	blobs := map[uint64][]byte{}
	var d core.VoiceDecoder = &VoiceDecoder{
		Roots: func() []string { return []string{dir} },
		Put: func(pcm []byte) abi.BlobRef {
			id := uint64(len(blobs) + 1)
			blobs[id] = pcm
			return abi.BlobRef{ID: id, Len: uint64(len(pcm))}
		},
	}

	ref, vf, err := d.DecodeVoice(note)
	if err != nil {
		t.Fatal(err)
	}
	if vf.Rate != core.VoiceRate || vf.Channels != 1 || ref.Len != uint64(vf.Frames)*4 {
		t.Fatalf("%+v, %d bytes", vf, ref.Len)
	}
	// The fake gives exactly the tone; a real note is the tone through Opus,
	// its last packet whole.
	if s := vf.Duration().Seconds(); s < fake.VoiceSeconds || s > fake.VoiceSeconds+0.02 {
		t.Fatal(vf.Duration())
	}
	lo, hi := byte(100), byte(0)
	for _, b := range vf.Waveform {
		lo, hi = min(lo, b), max(hi, b)
	}
	if hi != 100 || lo > 40 {
		t.Fatalf("the tone swells and fades: %v", vf.Waveform)
	}
	pcm := blobs[ref.ID]
	got := math.Float32frombits(binary.LittleEndian.Uint32(pcm[4*24000:]))
	if want := samples[24000]; math.Abs(float64(got-want)) > 0.1 {
		t.Fatalf("sample 24000: %v, want about %v", got, want)
	}

	for _, c := range []struct {
		path string
		code int32
	}{
		{"note.ogg", core.EArg},
		{"", core.EArg},
		{bad, core.EMalformed},
		{filepath.Join(dir, "gone.ogg"), core.ENotFound},
		{"/etc/hosts", core.EArg},
	} {
		if _, _, err := d.DecodeVoice(c.path); !core.IsCode(err, c.code) {
			t.Errorf("%q: %v, want %s", c.path, err, core.CodeName(c.code))
		}
	}
	if len(blobs) != 1 {
		t.Fatal("only the decoded note's blob", len(blobs))
	}
}

func TestPacketSamples(t *testing.T) {
	for _, c := range []struct {
		p    []byte
		want int
		ok   bool
	}{
		{[]byte{15 << 3}, 960, true},        // hybrid FB 20 ms
		{[]byte{1 << 3}, 960, true},         // SILK NB 20 ms
		{[]byte{3<<3 | 1}, 5760, true},      // two 60 ms SILK frames
		{[]byte{31<<3 | 3, 6}, 5760, true},  // six 20 ms CELT frames
		{[]byte{31<<3 | 3, 7}, 6720, false}, // over 120 ms
		{[]byte{31<<3 | 3}, 0, false},
		{nil, 0, false},
	} {
		if n, ok := packetSamples(c.p); n != c.want || ok != c.ok {
			t.Errorf("%x: %d %v", c.p, n, ok)
		}
	}
}

// TestConcurrentCodecs runs encodes and decodes in parallel: each has its
// own converter and callback state.
func TestConcurrentCodecs(t *testing.T) {
	needOpus(t)
	errs := make(chan error, 8)
	for i := range 8 {
		go func() {
			s := tone(0.3 + 0.1*float64(i))
			ogg, err := EncodeOpus(s)
			if err == nil {
				var back []float32
				back, err = DecodeOpus(ogg)
				if err == nil && (len(back) < len(s) || len(back) > len(s)+frame) {
					err = fmt.Errorf("%d samples for %d", len(back), len(s))
				}
			}
			errs <- err
		}()
	}
	for range 8 {
		if err := <-errs; err != nil {
			t.Fatal(err)
		}
	}
}
