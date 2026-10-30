package av

import (
	"bytes"
	"errors"
	"math"
	"os/exec"
	"testing"

	"zapfast/gocore/voip/opus"
)

// A call's Opus round trip at 16 kHz in 60 ms frames: one packet a frame,
// each declaring 60 ms, decoding back to 960 samples that carry the tone.
func TestOpusStreamRoundTrip(t *testing.T) {
	const rate, frameN, seconds = 16000, 960, 2
	enc, err := NewOpusStreamEncoder(rate, frameN, 24000)
	if errors.Is(err, ErrUnavailable) {
		t.Skip(err)
	}
	if err != nil {
		t.Fatal(err)
	}
	defer enc.Close()
	dec, err := NewOpusStreamDecoder(rate)
	if err != nil {
		t.Fatal(err)
	}
	defer dec.Close()

	src := make([]int16, rate*seconds)
	for i := range src {
		x := float64(i) / rate
		src[i] = int16(8000*math.Sin(2*math.Pi*440*x) + 6000*math.Sin(2*math.Pi*1000*x))
	}
	var out []int16
	var size int
	for off := 0; off+frameN <= len(src); off += frameN {
		p, err := enc.Encode(nil, src[off:off+frameN])
		if err != nil {
			t.Fatalf("frame %d: %v", off/frameN, err)
		}
		size += len(p)
		shape, ok := opus.Shape(p)
		if !ok {
			t.Fatalf("frame %d: not an Opus packet: %x", off/frameN, p)
		}
		if n, ok := shape.TotalSamples(rate); !ok || n != frameN {
			t.Fatalf("frame %d: the packet declares %d samples, not %d", off/frameN, n, frameN)
		}
		before := len(out)
		if out, err = dec.Decode(out, p); err != nil {
			t.Fatalf("frame %d: decode: %v", off/frameN, err)
		}
		if got := len(out) - before; got != frameN {
			t.Fatalf("frame %d decoded to %d samples, not %d", off/frameN, got, frameN)
		}
	}
	snr, lag := bestSNR(src, out, 1000)
	t.Logf("%d packets, %.1f kbit/s, SNR %.1f dB at a lag of %d samples", len(src)/frameN,
		float64(size*8)/seconds/1000, snr, lag)
	if snr < 10 {
		t.Fatalf("SNR %.1f dB", snr)
	}
	if _, err := enc.Encode(nil, src[:100]); err == nil {
		t.Fatal("a short frame was encoded")
	}
	if _, err := dec.Decode(nil, []byte{}); !errors.Is(err, ErrMalformed) {
		t.Fatalf("an empty packet: %v", err)
	}
}

// bestSNR is the SNR of got against want delayed by the best lag up to
// maxLag, over the second half (past the codec's start-up).
func bestSNR(want, got []int16, maxLag int) (float64, int) {
	best, bestLag := math.Inf(-1), 0
	start := len(want) / 2
	for lag := 0; lag <= maxLag; lag++ {
		var sig, noise float64
		for i := start; i+lag < len(got) && i < len(want); i++ {
			s, g := float64(want[i]), float64(got[i+lag])
			sig += s * s
			noise += (s - g) * (s - g)
		}
		if noise == 0 {
			return math.Inf(1), lag
		}
		if snr := 10 * math.Log10(sig/noise); snr > best {
			best, bestLag = snr, lag
		}
	}
	return best, bestLag
}

// Packets from libopus, what WhatsApp's peers send (16 kHz, 60 ms, VoIP),
// decode to their whole duration and carry the tone. ffmpeg with libopus
// makes them; without it the test is skipped.
func TestOpusStreamDecodesLibopus(t *testing.T) {
	const rate = 16000
	dec, err := NewOpusStreamDecoder(rate)
	if errors.Is(err, ErrUnavailable) {
		t.Skip(err)
	}
	if err != nil {
		t.Fatal(err)
	}
	defer dec.Close()
	if _, err := exec.LookPath("ffmpeg"); err != nil {
		t.Skip("no ffmpeg")
	}
	src := make([]int16, 2*rate)
	pcm := make([]byte, 0, 2*len(src))
	for i := range src {
		x := float64(i) / rate
		src[i] = int16(8000*math.Sin(2*math.Pi*440*x) + 6000*math.Sin(2*math.Pi*1000*x))
		pcm = append(pcm, byte(src[i]), byte(uint16(src[i])>>8))
	}
	cmd := exec.Command("ffmpeg", "-hide_banner", "-loglevel", "error", "-f", "s16le", "-ar", "16000", "-ac", "1", "-i", "-",
		"-c:a", "libopus", "-application", "voip", "-b:a", "24k", "-frame_duration", "60", "-f", "ogg", "-")
	cmd.Stdin = bytes.NewReader(pcm)
	ogg, err := cmd.Output()
	if err != nil {
		t.Skipf("ffmpeg cannot encode with libopus: %v", err)
	}
	var out []int16
	n := 0
	err = oggPackets(ogg, func(p []byte, _ uint64, _ bool) error {
		if n++; n <= 2 { // OpusHead, OpusTags
			return nil
		}
		before := len(out)
		var err error
		if out, err = dec.Decode(out, p); err != nil {
			return err
		}
		if got := len(out) - before; got != 960 {
			t.Errorf("packet %d decoded to %d samples", n-2, got)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	snr, lag := bestSNR(src, out, 1000)
	t.Logf("%d libopus packets, SNR %.1f dB at a lag of %d samples", n-2, snr, lag)
	if snr < 10 {
		t.Fatalf("SNR %.1f dB", snr)
	}
}
