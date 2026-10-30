package video

import (
	"bytes"
	"errors"
	"math"
	"os/exec"
	"runtime"
	"testing"
)

// synthetic is a moving test picture: a diagonal luma gradient sliding by
// frame, a bright square crossing it, and slow chroma ramps. Smooth enough
// that an encoder at a call's bitrate keeps it well.
func synthetic(w, h, frame int) *I420 {
	p := NewI420(w, h)
	y := p.Y()
	for r := range h {
		for c := range w {
			y[r*p.Stride[0]+c] = byte(32 + (c+r+3*frame)%192)
		}
	}
	side := h / 4
	bx, by := (frame*5)%(w-side), (frame*3)%(h-side)
	for r := by; r < by+side; r++ {
		for c := bx; c < bx+side; c++ {
			y[r*p.Stride[0]+c] = 230
		}
	}
	u, v := p.U(), p.V()
	for r := range p.ChromaH() {
		for c := range p.ChromaW() {
			u[r*p.Stride[1]+c] = byte(96 + c*64/p.ChromaW())
			v[r*p.Stride[2]+c] = byte(160 - r*64/p.ChromaH())
		}
	}
	return p
}

// psnr is the luma PSNR of b against a, in dB.
func psnr(a, b *I420) float64 {
	var sum float64
	for r := range a.H {
		for c := range a.W {
			d := float64(a.Y()[r*a.Stride[0]+c]) - float64(b.Y()[r*b.Stride[0]+c])
			sum += d * d
		}
	}
	mse := sum / float64(a.W*a.H)
	if mse == 0 {
		return math.Inf(1)
	}
	return 10 * math.Log10(255*255/mse)
}

func newCodec(t *testing.T, cfg EncoderConfig) (*Encoder, *Decoder) {
	t.Helper()
	enc, err := NewEncoder(cfg)
	if runtime.GOOS != "darwin" {
		if !errors.Is(err, ErrUnavailable) || !errors.Is(err, errors.ErrUnsupported) {
			t.Fatalf("NewEncoder off macOS = %v, want ErrUnavailable", err)
		}
		t.Skip("no VideoToolbox here")
	}
	if err != nil {
		t.Fatalf("NewEncoder: %v", err)
	}
	t.Cleanup(enc.Close)
	dec, err := NewDecoder()
	if err != nil {
		t.Fatalf("NewDecoder: %v", err)
	}
	t.Cleanup(dec.Close)
	return enc, dec
}

func nalTypes(unit []byte) []int {
	var ts []int
	for _, u := range SplitAnnexB(unit) {
		ts = append(ts, NALType(u))
	}
	return ts
}

func TestRoundTrip(t *testing.T) {
	for _, size := range [][2]int{{640, 360}, {320, 180}, {360, 640}} {
		w, h := size[0], size[1]
		enc, dec := newCodec(t, EncoderConfig{Width: w, Height: h, FPS: 15, Bitrate: 1_500_000})
		var worst = math.Inf(1)
		decoded := 0
		for i := range 30 {
			src := synthetic(w, h, i)
			unit, err := enc.Encode(src, false)
			if err != nil {
				t.Fatalf("%dx%d frame %d: Encode: %v", w, h, i, err)
			}
			if unit == nil {
				continue
			}
			ts := nalTypes(unit)
			if ts[0] != NALAUD {
				t.Fatalf("frame %d starts with NAL %d, want an AUD", i, ts[0])
			}
			if i == 0 {
				if !IsKeyframe(unit) || !contains(ts, NALSPS) || !contains(ts, NALPPS) {
					t.Fatalf("the first unit's NALs are %v, want a keyframe with SPS and PPS", ts)
				}
			}
			pic, err := dec.Decode(unit)
			if err != nil {
				t.Fatalf("%dx%d frame %d (NALs %v): Decode: %v", w, h, i, ts, err)
			}
			if pic == nil {
				t.Fatalf("frame %d decoded to nothing", i)
			}
			if pic.W != w || pic.H != h || !pic.Valid() {
				t.Fatalf("decoded %dx%d (valid %v), want %dx%d", pic.W, pic.H, pic.Valid(), w, h)
			}
			if pic.Stride != [3]int{w, w / 2, w / 2} || len(pic.Px) != I420Len(w, h) {
				t.Fatalf("decoded strides %v, len %d: not packed I420", pic.Stride, len(pic.Px))
			}
			decoded++
			worst = min(worst, psnr(src, pic))
		}
		if decoded < 25 {
			t.Errorf("%dx%d: %d of 30 frames decoded", w, h, decoded)
		}
		if worst < 30 {
			t.Errorf("%dx%d: worst luma PSNR %.1f dB, want at least 30", w, h, worst)
		}
		t.Logf("%dx%d: %d frames, worst PSNR %.1f dB", w, h, decoded, worst)
	}
}

func contains(ts []int, t int) bool {
	for _, x := range ts {
		if x == t {
			return true
		}
	}
	return false
}

func TestKeyframeRequestAndBitrate(t *testing.T) {
	enc, dec := newCodec(t, EncoderConfig{Width: 320, Height: 240, FPS: 15, Bitrate: 400_000})
	encode := func(i int, key bool) []byte {
		t.Helper()
		unit, err := enc.Encode(synthetic(320, 240, i), key)
		if err != nil || unit == nil {
			t.Fatalf("frame %d: %v (unit nil: %v)", i, err, unit == nil)
		}
		return unit
	}
	if !IsKeyframe(encode(0, false)) {
		t.Fatal("the first frame is not a keyframe")
	}
	if IsKeyframe(encode(1, false)) {
		t.Fatal("the second frame is a keyframe unasked")
	}
	enc.RequestKeyframe()
	if !IsKeyframe(encode(2, false)) {
		t.Fatal("a requested keyframe did not come")
	}
	if IsKeyframe(encode(3, false)) {
		t.Fatal("the request stuck")
	}
	if !IsKeyframe(encode(4, true)) {
		t.Fatal("Encode(keyframe) made a delta frame")
	}
	if err := enc.SetBitrate(150_000); err != nil {
		t.Fatalf("SetBitrate: %v", err)
	}
	if got := enc.Config().Bitrate; got != 150_000 {
		t.Fatalf("Config().Bitrate = %d", got)
	}
	// The rate moves without a keyframe.
	if IsKeyframe(encode(5, false)) {
		t.Fatal("a bitrate change made a keyframe")
	}
	if err := enc.SetBitrate(0); err == nil {
		t.Fatal("SetBitrate(0) accepted")
	}
	_ = dec
}

func TestDecoderWaitsForKeyframe(t *testing.T) {
	enc, dec := newCodec(t, EncoderConfig{Width: 320, Height: 240})
	var units [][]byte
	for i := range 3 {
		u, err := enc.Encode(synthetic(320, 240, i), false)
		if err != nil {
			t.Fatal(err)
		}
		units = append(units, u)
	}
	if _, err := dec.Decode(units[1]); !errors.Is(err, ErrNeedKeyframe) {
		t.Fatalf("a delta frame first: %v, want ErrNeedKeyframe", err)
	}
	if p, err := dec.Decode(units[0]); err != nil || p == nil {
		t.Fatalf("the keyframe: %v", err)
	}
	if p, err := dec.Decode(units[1]); err != nil || p == nil {
		t.Fatalf("the delta after it: %v", err)
	}
	// A unit of parameter sets alone is no picture and no error.
	var params []byte
	for _, u := range SplitAnnexB(units[0]) {
		if t := NALType(u); t == NALSPS || t == NALPPS {
			params = appendAnnexB(params, u)
		}
	}
	if p, err := dec.Decode(params); err != nil || p != nil {
		t.Fatalf("parameter sets alone: %v, %v", p, err)
	}
}

func TestDecoderFollowsANewSize(t *testing.T) {
	enc1, dec := newCodec(t, EncoderConfig{Width: 320, Height: 240})
	enc2, err := NewEncoder(EncoderConfig{Width: 640, Height: 480})
	if err != nil {
		t.Fatal(err)
	}
	defer enc2.Close()
	for i, enc := range []*Encoder{enc1, enc2, enc1} {
		w := enc.Config().Width
		u, err := enc.Encode(synthetic(w, w*3/4, i), true)
		if err != nil {
			t.Fatal(err)
		}
		p, err := dec.Decode(u)
		if err != nil || p == nil || p.W != w {
			t.Fatalf("step %d: %v, %+v", i, err, p)
		}
	}
}

func TestDamagedFrameIsTyped(t *testing.T) {
	enc, dec := newCodec(t, EncoderConfig{Width: 320, Height: 240})
	key, err := enc.Encode(synthetic(320, 240, 0), false)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := dec.Decode(key); err != nil {
		t.Fatal(err)
	}
	// A slice of garbage behind a delta slice header.
	bad := append([]byte(nil), aud...)
	bad = appendAnnexB(bad, append([]byte{0x41}, make([]byte, 64)...))
	for i := range 64 {
		bad[len(bad)-64+i] = byte(i*37 + 11)
	}
	_, err = dec.Decode(bad)
	var se *StatusError
	if !errors.As(err, &se) || !se.IsBadData() {
		t.Fatalf("a damaged frame: %v, want a bad-data *StatusError", err)
	}
	// After a failure the decoder waits for a keyframe again.
	delta, _ := enc.Encode(synthetic(320, 240, 1), false)
	if _, err := dec.Decode(delta); !errors.Is(err, ErrNeedKeyframe) {
		t.Fatalf("a delta after a failure: %v, want ErrNeedKeyframe", err)
	}
}

// The stream is Constrained Baseline, what WhatsApp's clients decode:
// profile_idc 66 with constraint_set1_flag.
func TestConstrainedBaseline(t *testing.T) {
	enc, _ := newCodec(t, EncoderConfig{Width: 320, Height: 240})
	unit, err := enc.Encode(synthetic(320, 240, 0), false)
	if err != nil {
		t.Fatal(err)
	}
	for _, u := range SplitAnnexB(unit) {
		if NALType(u) == NALSPS {
			if len(u) < 3 || u[1] != 66 || u[2]&0x40 == 0 {
				t.Fatalf("SPS % x: not Constrained Baseline", u[:min(len(u), 4)])
			}
			return
		}
	}
	t.Fatal("no SPS in the first unit")
}

func TestEncoderRejects(t *testing.T) {
	if _, err := NewEncoder(EncoderConfig{Width: 321, Height: 240}); !errors.Is(err, ErrFrame) {
		t.Fatalf("an odd width: %v", err)
	}
	enc, _ := newCodec(t, EncoderConfig{Width: 320, Height: 240})
	if _, err := enc.Encode(NewI420(640, 480), false); !errors.Is(err, ErrFrame) {
		t.Fatalf("a picture of another size: %v", err)
	}
	if _, err := enc.Encode(&I420{W: 320, H: 240}, false); !errors.Is(err, ErrFrame) {
		t.Fatalf("a picture with no pixels: %v", err)
	}
	enc.Close()
	if _, err := enc.Encode(NewI420(320, 240), false); !errors.Is(err, ErrClosed) {
		t.Fatalf("after Close: %v", err)
	}
}

// ffmpeg, when it is installed, checks the stream against another codec:
// x264's Baseline decodes here, and ours decodes there. Synthetic pictures
// only (lavfi's testsrc2).
func TestInteropWithFFmpeg(t *testing.T) {
	ff, err := exec.LookPath("ffmpeg")
	if err != nil {
		t.Skip("no ffmpeg")
	}
	_, dec := newCodec(t, EncoderConfig{Width: 320, Height: 240})
	out, err := exec.Command(ff, "-v", "error", "-f", "lavfi", "-i", "testsrc2=size=320x240:rate=15", "-frames:v", "20",
		"-c:v", "libx264", "-profile:v", "baseline", "-bsf:v", "h264_metadata=aud=insert", "-f", "h264", "-").Output()
	if err != nil {
		t.Skipf("ffmpeg cannot make H.264 here: %v", err)
	}
	units := splitOnAUD(out)
	if len(units) != 20 {
		t.Fatalf("%d access units from ffmpeg", len(units))
	}
	for i, u := range units {
		p, err := dec.Decode(u)
		if err != nil || p == nil || p.W != 320 || p.H != 240 {
			t.Fatalf("x264 unit %d: %v, %+v", i, err, p)
		}
	}

	enc, _ := newCodec(t, EncoderConfig{Width: 320, Height: 240})
	var stream []byte
	for i := range 20 {
		u, err := enc.Encode(synthetic(320, 240, i), false)
		if err != nil {
			t.Fatal(err)
		}
		stream = append(stream, u...)
	}
	cmd := exec.Command(ff, "-v", "error", "-f", "h264", "-i", "-", "-f", "rawvideo", "-pix_fmt", "yuv420p", "-")
	cmd.Stdin = bytes.NewReader(stream)
	raw, err := cmd.Output()
	if err != nil {
		t.Fatalf("ffmpeg decoding ours: %v", err)
	}
	if n := len(raw) / I420Len(320, 240); n != 20 {
		t.Fatalf("ffmpeg decoded %d of our 20 frames", n)
	}
	last := &I420{W: 320, H: 240, Stride: [3]int{320, 160, 160}, Px: raw[19*I420Len(320, 240):]}
	if q := psnr(synthetic(320, 240, 19), last); q < 30 {
		t.Fatalf("ffmpeg's decode of our last frame: %.1f dB", q)
	}
}

func splitOnAUD(b []byte) [][]byte {
	var units [][]byte
	var cur []byte
	for _, u := range SplitAnnexB(b) {
		if NALType(u) == NALAUD && cur != nil {
			units = append(units, cur)
			cur = nil
		}
		cur = appendAnnexB(cur, u)
	}
	if cur != nil {
		units = append(units, cur)
	}
	return units
}
