package engine

// The in-process test rig: a fake relay over the relay.Transport seam
// (allocates, pongs, forwards between the two ends of a call), synthetic
// devices (a tone for a microphone, a moving pattern for a camera) and the
// measures (SNR of a tone, PSNR of a picture). Nothing touches a real
// device or network.

import (
	"context"
	"encoding/binary"
	"errors"
	"math"
	"net/netip"
	"sync"
	"testing"
	"time"

	waBinary "go.mau.fi/whatsmeow/binary"

	"zapfast/gocore/voip/camera"
	"zapfast/gocore/voip/mlow"
	"zapfast/gocore/voip/relay"
	"zapfast/gocore/voip/stun"
	"zapfast/gocore/voip/video"
)

const (
	testCallID = "00112233445566778899AABBCCDDEEFF"
	lidA       = "111111111111111:3@lid"
	lidB       = "222222222222222:0@lid"
)

var testCallKey = func() []byte {
	k := make([]byte, 32)
	for i := range k {
		k[i] = byte(i*7 + 1)
	}
	return k
}()

// relayNode is a <relay> block naming addr, as the server sends it.
func relayNode(addr netip.AddrPort) waBinary.Node {
	ip := addr.Addr().As4()
	te2 := binary.BigEndian.AppendUint16(ip[:], addr.Port())
	return waBinary.Node{Tag: "relay", Attrs: waBinary.Attrs{"uuid": "u"}, Content: []waBinary.Node{
		{Tag: "key", Content: []byte("dGVzdC1yZWxheS1rZXktMTIz")},
		{Tag: "token", Attrs: waBinary.Attrs{"id": "0"}, Content: []byte("relay-token-0")},
		{Tag: "te2", Attrs: waBinary.Attrs{"relay_id": "1", "relay_name": "test", "token_id": "0", "auth_token_id": "1"}, Content: te2},
		{Tag: "auth_token", Attrs: waBinary.Attrs{"id": "1"}, Content: []byte("auth-token-1")},
	}}
}

func testRelay(t *testing.T, addr netip.AddrPort) Relay {
	t.Helper()
	n := relayNode(addr)
	return ParseRelay(&n)
}

// --- the in-memory relay -----------------------------------------------------

// memRelay is a fake relay: every transport dialled to it is one end of
// the same call. It answers the STUN allocate (or refuses it, or ignores
// it) and the WhatsApp ping, and forwards everything else to the other
// ends.
type memRelay struct {
	mu        sync.Mutex
	ends      []*memTransport
	refuse    uint16 // a STUN error code to refuse allocates with
	blackhole bool   // swallow every packet
	dialErr   error
	tamper    func(from int, p []byte) []byte
}

type memTransport struct {
	r      *memRelay
	index  int
	events chan relay.Event
	mu     sync.Mutex
	closed bool
	sent   int
}

func (r *memRelay) Dial(ctx context.Context, _ netip.AddrPort) (relay.Transport, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.dialErr != nil {
		return nil, r.dialErr
	}
	t := &memTransport{r: r, index: len(r.ends), events: make(chan relay.Event, 1024)}
	t.events <- relay.Event{Kind: relay.EventConnected}
	r.ends = append(r.ends, t)
	return t, nil
}

func (t *memTransport) deliver(p []byte) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.closed {
		return
	}
	select {
	case t.events <- relay.Event{Kind: relay.EventPacket, Packet: append([]byte(nil), p...)}:
	default:
	}
}

func (t *memTransport) Send(_ context.Context, p []byte) error {
	t.mu.Lock()
	if t.closed {
		t.mu.Unlock()
		return relay.ErrClosed
	}
	t.sent++
	t.mu.Unlock()
	r := t.r
	r.mu.Lock()
	refuse, blackhole, tamper := r.refuse, r.blackhole, r.tamper
	ends := append([]*memTransport(nil), r.ends...)
	r.mu.Unlock()
	if blackhole {
		return nil
	}
	if typ, ok := stun.MessageType(p); ok && stun.IsSTUN(p) {
		id, _ := stun.TransactionID(p)
		var tx [12]byte
		copy(tx[:], id)
		switch typ {
		case stun.MsgAllocateRequest:
			if refuse != 0 {
				code := []byte{0, 0, byte(refuse / 100), byte(refuse % 100)}
				attr := binary.BigEndian.AppendUint16(nil, 0x0009)
				attr = binary.BigEndian.AppendUint16(attr, uint16(len(code)))
				t.deliver(stun.EncodeRequest(stun.MsgAllocateError, &tx, append(attr, code...), nil, false))
			} else {
				t.deliver(stun.EncodeRequest(stun.MsgAllocateSuccess, &tx, nil, nil, false))
			}
		case stun.MsgWhatsAppPing:
			t.deliver(stun.EncodeRequest(stun.MsgWhatsAppPong, &tx, nil, nil, false))
		}
		return nil
	}
	if tamper != nil {
		if p = tamper(t.index, p); p == nil {
			return nil
		}
	}
	for _, o := range ends {
		if o != t {
			o.deliver(p)
		}
	}
	return nil
}

func (t *memTransport) Events() <-chan relay.Event { return t.events }

func (t *memTransport) Close() error {
	t.mu.Lock()
	defer t.mu.Unlock()
	if !t.closed {
		t.closed = true
		close(t.events)
	}
	return nil
}

func (t *memTransport) Reconnect(context.Context, netip.AddrPort) (relay.Transport, error) {
	return nil, errors.New("no reconnect in tests")
}

// --- synthetic devices -----------------------------------------------------------

// toneFreqs are the microphone's tone: three partials in the speech band.
var toneFreqs = []float64{350, 700, 1100}

const toneAmp = 6000.0

func toneSample(n int, freqs []float64) int16 {
	var v float64
	for _, f := range freqs {
		v += math.Sin(2 * math.Pi * f * float64(n) / 16000)
	}
	return int16(toneAmp * v / float64(len(freqs)))
}

// fakeAudio plays a tone into the call at the engine's real-time cadence
// and records what the call plays out.
type fakeAudio struct {
	mic    chan []int16
	stop   chan struct{}
	once   sync.Once
	mu     sync.Mutex
	played []int16
	freqs  []float64
}

func newFakeAudio(freqs []float64) *fakeAudio {
	a := &fakeAudio{mic: make(chan []int16, 4), stop: make(chan struct{}), freqs: freqs}
	go func() {
		tick := time.NewTicker(60 * time.Millisecond)
		defer tick.Stop()
		n := 0
		for {
			select {
			case <-a.stop:
				return
			case <-tick.C:
			}
			f := make([]int16, MicFrameSamples)
			for i := range f {
				if a.freqs != nil {
					f[i] = toneSample(n+i, a.freqs)
				}
			}
			n += MicFrameSamples
			select {
			case a.mic <- f:
			default:
			}
		}
	}()
	return a
}

func (a *fakeAudio) Microphone() <-chan []int16 { return a.mic }
func (a *fakeAudio) Play(s []int16) {
	a.mu.Lock()
	a.played = append(a.played, s...)
	a.mu.Unlock()
}
func (a *fakeAudio) SetMicrophone(string) {}
func (a *fakeAudio) SetSpeaker(string)    {}
func (a *fakeAudio) Close()               { a.once.Do(func() { close(a.stop) }) }

func (a *fakeAudio) playedCopy() []int16 {
	a.mu.Lock()
	defer a.mu.Unlock()
	return append([]int16(nil), a.played...)
}

// toneSNR fits the known partials (amplitude and phase, by least squares)
// to the loudest stretch of what was played and returns the fitted
// signal's power over the residual's, in dB.
func toneSNR(pcm []int16, freqs []float64) float64 {
	const window = 16000 // one second
	if len(pcm) < window {
		return math.Inf(-1)
	}
	// The last full second: past the start-up and the prebuffer.
	x := pcm[len(pcm)-window:]
	n := len(x)
	// Least squares over the basis sin/cos of each partial plus DC.
	k := 2*len(freqs) + 1
	basis := func(j, i int) float64 {
		if j == k-1 {
			return 1
		}
		w := 2 * math.Pi * freqs[j/2] * float64(i) / 16000
		if j%2 == 0 {
			return math.Sin(w)
		}
		return math.Cos(w)
	}
	ata := make([][]float64, k)
	atb := make([]float64, k)
	for a := range k {
		ata[a] = make([]float64, k)
	}
	for i := range n {
		for a := range k {
			ba := basis(a, i)
			atb[a] += ba * float64(x[i])
			for b := range k {
				ata[a][b] += ba * basis(b, i)
			}
		}
	}
	coef := solve(ata, atb)
	var sig, res float64
	for i := range n {
		var fit float64
		for a := range k - 1 {
			fit += coef[a] * basis(a, i)
		}
		r := float64(x[i]) - fit - coef[k-1]
		sig += fit * fit
		res += r * r
	}
	switch {
	case sig < float64(n): // under one LSB of tone: nothing came through
		return math.Inf(-1)
	case res == 0:
		return math.Inf(1)
	}
	return 10 * math.Log10(sig/res)
}

func solve(a [][]float64, b []float64) []float64 {
	n := len(b)
	for c := range n {
		p := c
		for r := c + 1; r < n; r++ {
			if math.Abs(a[r][c]) > math.Abs(a[p][c]) {
				p = r
			}
		}
		a[c], a[p] = a[p], a[c]
		b[c], b[p] = b[p], b[c]
		if a[c][c] == 0 {
			continue
		}
		for r := range n {
			if r == c {
				continue
			}
			f := a[r][c] / a[c][c]
			for cc := c; cc < n; cc++ {
				a[r][cc] -= f * a[c][cc]
			}
			b[r] -= f * b[c]
		}
	}
	x := make([]float64, n)
	for i := range n {
		if a[i][i] != 0 {
			x[i] = b[i] / a[i][i]
		}
	}
	return x
}

// pattern is synthetic frame i: a gradient with a moving square.
func pattern(w, h, i int) *video.I420 {
	p := video.NewI420(w, h)
	y, u, v := p.Y(), p.U(), p.V()
	for r := range h {
		for c := range w {
			y[r*p.Stride[0]+c] = byte((c*255/w + r*64/h) & 0xff)
		}
	}
	x0, y0 := (i*4)%(w-48), (i*2)%(h-48)
	for r := y0; r < y0+48; r++ {
		for c := x0; c < x0+48; c++ {
			y[r*p.Stride[0]+c] = 235
		}
	}
	for r := range p.ChromaH() {
		for c := range p.ChromaW() {
			u[r*p.Stride[1]+c] = byte(96 + c*64/p.ChromaW())
			v[r*p.Stride[2]+c] = byte(160 - r*64/p.ChromaH())
		}
	}
	return p
}

func psnr(a, b *video.I420) float64 {
	if a.W != b.W || a.H != b.H {
		return 0
	}
	var se float64
	for r := range a.H {
		for c := range a.W {
			d := float64(a.Y()[r*a.Stride[0]+c]) - float64(b.Y()[r*b.Stride[0]+c])
			se += d * d
		}
	}
	mse := se / float64(a.W*a.H)
	if mse == 0 {
		return 99
	}
	return 10 * math.Log10(255*255/mse)
}

// fakeCamera sends the pattern at 20 frames a second.
type fakeCamera struct {
	w, h   int
	frames chan camera.Frame
	done   chan struct{}
	once   sync.Once
	mu     sync.Mutex
	sent   []*video.I420
}

func newFakeCamera(w, h int) *fakeCamera {
	c := &fakeCamera{w: w, h: h, frames: make(chan camera.Frame, 2), done: make(chan struct{})}
	go func() {
		tick := time.NewTicker(50 * time.Millisecond)
		defer tick.Stop()
		for i := 0; ; i++ {
			select {
			case <-c.done:
				return
			case at := <-tick.C:
				p := pattern(w, h, i)
				c.mu.Lock()
				c.sent = append(c.sent, p)
				c.mu.Unlock()
				select {
				case c.frames <- camera.Frame{Pic: p, At: at}:
				default:
				}
			}
		}
	}()
	return c
}

func (c *fakeCamera) Frames() <-chan camera.Frame { return c.frames }
func (c *fakeCamera) Done() <-chan struct{}       { return c.done }
func (c *fakeCamera) Close()                      { c.once.Do(func() { close(c.done) }) }

// bestPSNR is a decoded picture's PSNR against the closest frame sent.
func (c *fakeCamera) bestPSNR(p *video.I420) float64 {
	c.mu.Lock()
	defer c.mu.Unlock()
	best := 0.0
	for _, s := range c.sent {
		best = max(best, psnr(s, p))
	}
	return best
}

// frameSink records the pictures a call shows.
type frameSink struct {
	mu            sync.Mutex
	local, remote []*video.I420
}

func (f *frameSink) PutLocal(p *video.I420) { f.mu.Lock(); f.local = append(f.local, p); f.mu.Unlock() }
func (f *frameSink) PutRemote(p *video.I420) {
	f.mu.Lock()
	f.remote = append(f.remote, p)
	f.mu.Unlock()
}

func (f *frameSink) remotes() []*video.I420 {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]*video.I420(nil), f.remote...)
}

// events collects a call's events.
type events struct {
	mu   sync.Mutex
	list []CallEvent
	ch   chan CallEvent
}

func newEvents() *events { return &events{ch: make(chan CallEvent, 256)} }

func (e *events) add(ev CallEvent) {
	e.mu.Lock()
	e.list = append(e.list, ev)
	e.mu.Unlock()
	e.ch <- ev
}

func (e *events) has(k CallEventKind) bool {
	e.mu.Lock()
	defer e.mu.Unlock()
	for _, ev := range e.list {
		if ev.Kind == k {
			return true
		}
	}
	return false
}

func (e *events) wait(t *testing.T, k CallEventKind, d time.Duration) CallEvent {
	t.Helper()
	deadline := time.After(d)
	e.mu.Lock()
	for _, ev := range e.list {
		if ev.Kind == k {
			e.mu.Unlock()
			return ev
		}
	}
	e.mu.Unlock()
	for {
		select {
		case ev := <-e.ch:
			if ev.Kind == k {
				return ev
			}
		case <-deadline:
			t.Fatalf("no event %d within %v; got %v", k, d, e.snapshot())
			return CallEvent{}
		}
	}
}

func (e *events) snapshot() []CallEvent {
	e.mu.Lock()
	defer e.mu.Unlock()
	return append([]CallEvent(nil), e.list...)
}

// side is one end of a test call.
type side struct {
	call   *Call
	ev     *events
	audio  *fakeAudio
	cam    *fakeCamera
	frames *frameSink
}

type sideOpts struct {
	outgoing   bool
	self, peer string
	key        []byte
	video, cam bool
	freqs      []float64
	noAudio    bool
	opus       bool // install standard Opus
	standard   bool // negotiate standard Opus, not MLow
}

func openSide(t *testing.T, r *memRelay, o sideOpts) *side {
	t.Helper()
	s := &side{ev: newEvents(), frames: &frameSink{}}
	if !o.noAudio {
		s.audio = newFakeAudio(o.freqs)
	}
	key := o.key
	if key == nil {
		key = testCallKey
	}
	d := Devices{
		Dial:   r.Dial,
		Frames: s.frames,
		OpenAudio: func(string, string) (Audio, error) {
			if s.audio == nil {
				return nil, errors.New("no audio")
			}
			return s.audio, nil
		},
		OpenCamera: func(string) (Camera, error) {
			s.cam = newFakeCamera(320, 240)
			return s.cam, nil
		},
		NewEncoder: func(w, h, fps int) (VideoEncoder, error) {
			return video.NewEncoder(video.EncoderConfig{Width: w, Height: h, FPS: fps, Bitrate: 500_000})
		},
		NewDecoder: func() (VideoDecoder, error) { return video.NewDecoder() },
	}
	if o.opus {
		d.NewOpus = newOpus
	}
	c, err := Open(Params{
		CallID: testCallID, Generation: 7, Outgoing: o.outgoing, SelfLID: o.self, PeerLID: o.peer,
		CallKey: key, Relay: testRelay(t, netip.MustParseAddrPort("10.0.0.1:3478")), MLow: !o.standard,
		Video: o.video, CameraOn: o.cam,
	}, d, s.ev.add)
	if err != nil {
		t.Fatal(err)
	}
	s.call = c
	t.Cleanup(func() {
		c.Close()
		<-c.Done()
		if s.audio != nil {
			s.audio.Close()
		}
	})
	return s
}

func (s *side) end(t *testing.T) {
	t.Helper()
	s.call.Close()
	select {
	case <-s.call.Done():
	case <-time.After(5 * time.Second):
		t.Fatal("the call did not end")
	}
	s.ev.wait(t, CallEnded, time.Second)
}

// codecSNR is the tone's SNR through codec alone (encode, decode), the
// reference an end-to-end call is held to: the transport must add nothing.
func codecSNR(t *testing.T, freqs []float64, codec ForeignCodec) float64 {
	t.Helper()
	enc, dec := mlow.NewEncoder(), mlow.NewDecoder()
	var out []int16
	n := 0
	for range 80 {
		f := make([]int16, MicFrameSamples)
		for i := range f {
			f[i] = toneSample(n+i, freqs)
		}
		n += MicFrameSamples
		if codec != nil {
			p, err := codec.Encode(nil, f)
			if err != nil {
				t.Fatal(err)
			}
			if out, err = codec.Decode(out, p); err != nil {
				t.Fatal(err)
			}
			continue
		}
		p, err := enc.EncodeInt16(f)
		if err != nil {
			t.Fatal(err)
		}
		for _, s := range dec.Decode(p) {
			out = append(out, floatToInt16(s))
		}
	}
	return toneSNR(out, freqs)
}
