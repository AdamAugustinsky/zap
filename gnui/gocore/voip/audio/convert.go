package audio

// The engine's audio clock and frame (call_audio.rs): 16 kHz mono, 960
// samples (60 ms) a frame, int16.
const (
	Rate         = 16_000
	FrameSamples = 960
)

// Format is how a device stream's samples come: interleaved float32 at a
// rate, so many channels.
type Format struct {
	Rate, Channels int
}

// capture turns a device's interleaved samples into whole engine frames.
// A device delivers what it likes per callback; the remainder is carried,
// never padded or dropped, so no frame boundary drifts.
type capture struct {
	f       Format
	rs      *Resampler
	mono    []float32
	pending []float32
}

func newCapture(f Format) *capture {
	return &capture{f: f, rs: NewResampler(f.Rate, Rate)}
}

// push converts interleaved and hands each whole frame to frame. The
// frames are new slices the receiver keeps.
func (c *capture) push(interleaved []float32, frame func([]int16)) {
	ch := max(c.f.Channels, 1)
	c.mono = c.mono[:0]
	if ch == 1 {
		c.mono = append(c.mono, interleaved...)
	} else {
		for i := 0; i+ch <= len(interleaved); i += ch {
			var s float32
			for _, v := range interleaved[i : i+ch] {
				s += v
			}
			c.mono = append(c.mono, s/float32(ch))
		}
	}
	c.pending = c.rs.Process(c.pending, c.mono)
	at := 0
	for len(c.pending)-at >= FrameSamples {
		out := make([]int16, FrameSamples)
		for i, v := range c.pending[at : at+FrameSamples] {
			out[i] = toInt16(v)
		}
		frame(out)
		at += FrameSamples
	}
	if at > 0 {
		n := copy(c.pending, c.pending[at:])
		c.pending = c.pending[:n]
	}
}

func toInt16(v float32) int16 {
	switch {
	case v >= 1:
		return 32767
	case v <= -1:
		return -32767
	}
	return int16(v * 32767)
}

// render fills a device's interleaved buffer from the playout buffer,
// resampling 16 kHz mono up to the device's rate and channels.
type render struct {
	f     Format
	rs    *Resampler
	src   *Playout
	pull  []int16
	pullF []float32
	ready []float32 // resampled, not yet played
}

// pullBlock is how much a render reads from the playout buffer at a time:
// 10 ms.
const pullBlock = Rate / 100

func newRender(f Format, src *Playout) *render {
	return &render{f: f, rs: NewResampler(Rate, f.Rate), src: src,
		pull: make([]int16, pullBlock), pullF: make([]float32, pullBlock)}
}

// fill writes len(out)/channels frames of interleaved samples.
func (r *render) fill(out []float32) {
	ch := max(r.f.Channels, 1)
	frames := len(out) / ch
	for len(r.ready) < frames {
		r.src.Read(r.pull)
		for i, v := range r.pull {
			r.pullF[i] = float32(v) / 32767
		}
		r.ready = r.rs.Process(r.ready, r.pullF)
	}
	for i := range frames {
		v := r.ready[i]
		for c := range ch {
			out[i*ch+c] = v
		}
	}
	clear(out[frames*ch:])
	n := copy(r.ready, r.ready[frames:])
	r.ready = r.ready[:n]
}
