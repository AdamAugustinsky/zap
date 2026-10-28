package audio

import (
	"slices"
	"sync"
	"sync/atomic"
	"time"
)

// Device is a microphone or speaker: the system's own id for it, which
// stays when it is renamed and tells apart two of the same name, and the
// name the picker shows.
type Device struct {
	ID, Label string
}

// Side is the microphone or the speaker.
type Side int

const (
	Microphone Side = iota
	Speaker
)

func (s Side) String() string {
	if s == Microphone {
		return "microphone"
	}
	return "speaker"
}

// Config is what a call's audio opens.
type Config struct {
	// Microphone and Speaker are device ids (Device.ID); empty is the
	// system default, followed when it changes.
	Microphone, Speaker string
	// EchoCancel opens both through the platform's voice processing
	// (macOS's voice-processing I/O unit: echo cancellation, noise
	// suppression, gain control) when it has it; otherwise, or when it
	// will not open, the devices are opened plainly (Stats.EchoCancel).
	EchoCancel bool
	// Playout is the speaker's buffer; a default one when nil.
	Playout *Playout
}

// Audio is one call's microphone and speaker. The engine reads 16 kHz
// frames from Microphone and writes them with Play; the channel and the
// buffer behind Play are made once and stay while devices are picked,
// lost or restarted, so nothing of the call's codec or stream state is
// rebuilt for a device change (call_audio.rs's AudioInput and
// AudioOutput). A device that will not open, goes away, or stops moving is
// reopened, and given up for the system default when it never moved;
// FellBack says so. Its methods are safe from any goroutine.
type Audio struct {
	b    backend
	t    timing
	mic  chan []int16
	play *Playout
	fell chan Side
	cmds chan command
	stop chan struct{}
	done chan struct{}
	once sync.Once

	micDropped atomic.Uint64
	mu         sync.Mutex // stats and selection, for Stats and Selected
	stats      Stats
	selected   [2]string
}

// Stats are what a call's audio did, for diagnostics and tests.
type Stats struct {
	// MicOpens and SpeakerOpens count the device streams opened.
	MicOpens, SpeakerOpens int
	// MicRestarts and SpeakerRestarts count the streams reopened because
	// they stopped moving (not for a device change).
	MicRestarts, SpeakerRestarts int
	// MicDropped counts the frames let go because the engine did not read
	// them in time.
	MicDropped uint64
	// EchoCancel says the streams open now run through voice processing.
	EchoCancel bool
	// Playout is the speaker buffer's counters.
	Playout PlayoutStats
}

// timing is the pump's clocks (call_audio.rs's), shorter in tests.
type timing struct {
	tick      time.Duration // how often health is checked
	micSilent time.Duration // a microphone that moved, silent this long, is reopened
	stalled   time.Duration // a speaker that played, stuck this long, is reopened
	startup   time.Duration // a stream that never moved is given this long
	retry     time.Duration // a device that would not open is tried again after this
	// unplayed is how many restarts of a speaker that never played before
	// its device is given up for the default.
	unplayed int
}

var defaultTiming = timing{
	tick:      100 * time.Millisecond,
	micSilent: time.Second,
	stalled:   500 * time.Millisecond,
	startup:   2 * time.Second,
	retry:     500 * time.Millisecond,
	unplayed:  3,
}

// micQueue is the engine frames the microphone channel holds (240 ms).
const micQueue = 4

// Open opens a call's audio on this system's devices: CoreAudio on macOS;
// an error matching ErrUnavailable elsewhere. A device that will not open
// is not an error: the default is used, or the open is retried.
func Open(cfg Config) (*Audio, error) {
	b, err := system()
	if err != nil {
		return nil, err
	}
	return start(b, cfg, defaultTiming), nil
}

func start(b backend, cfg Config, t timing) *Audio {
	play := cfg.Playout
	if play == nil {
		play = NewPlayout(DefaultPlayoutCapacity, DefaultPlayoutTarget)
	}
	a := &Audio{
		b: b, t: t, play: play,
		mic:  make(chan []int16, micQueue),
		fell: make(chan Side, 2),
		cmds: make(chan command, 16),
		stop: make(chan struct{}),
		done: make(chan struct{}),
	}
	a.selected = [2]string{cfg.Microphone, cfg.Speaker}
	p := &pump{a: a, echo: cfg.EchoCancel}
	p.sides[Microphone] = &side{kind: Microphone, want: cfg.Microphone}
	p.sides[Speaker] = &side{kind: Speaker, want: cfg.Speaker}
	p.openAll(time.Now())
	go p.run()
	return a
}

// Microphone is the engine's microphone channel: 960-sample frames at
// 16 kHz mono. The same channel for the whole call; closed by Close.
func (a *Audio) Microphone() <-chan []int16 { return a.mic }

// Play queues one engine frame (or any number of 16 kHz samples) for the
// speaker; it never blocks.
func (a *Audio) Play(samples []int16) { a.play.Write(samples) }

// Playout is the speaker's buffer.
func (a *Audio) Playout() *Playout { return a.play }

// FellBack receives the side whose picked device was given up for the
// system default (gone, would not open, or never moved).
func (a *Audio) FellBack() <-chan Side { return a.fell }

// SetMicrophone moves the microphone to device id (empty: the default),
// behind the same channel.
func (a *Audio) SetMicrophone(id string) { a.send(command{side: Microphone, id: id}) }

// SetSpeaker moves the speaker to device id (empty: the default), reading
// on from the same buffer.
func (a *Audio) SetSpeaker(id string) { a.send(command{side: Speaker, id: id}) }

// DevicesChanged says the system's devices may have changed (the UI's
// RefreshCallDevices): a picked device that is gone is let go for the
// default, and a default that moved is followed. The system's own
// notifications do the same.
func (a *Audio) DevicesChanged() { a.send(command{changed: true}) }

// Selected is the devices picked now: empty for the default.
func (a *Audio) Selected() (mic, speaker string) {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.selected[Microphone], a.selected[Speaker]
}

// Stats are the counters now.
func (a *Audio) Stats() Stats {
	a.mu.Lock()
	s := a.stats
	a.mu.Unlock()
	s.MicDropped = a.micDropped.Load()
	s.Playout = a.play.Stats()
	return s
}

// Close stops both devices and closes the microphone channel.
func (a *Audio) Close() {
	a.once.Do(func() { close(a.stop) })
	<-a.done
}

func (a *Audio) send(c command) {
	select {
	case a.cmds <- c:
	case <-a.done:
	}
}

func (a *Audio) setStats(f func(*Stats)) {
	a.mu.Lock()
	f(&a.stats)
	a.mu.Unlock()
}

// deliver is a microphone stream's frames into the engine's channel. When
// the engine is behind, the oldest frame is let go: the call hears what
// is said now.
func (a *Audio) deliver(f []int16) {
	for {
		select {
		case a.mic <- f:
			return
		default:
		}
		select {
		case <-a.mic:
			a.micDropped.Add(1)
		default:
		}
	}
}

type command struct {
	side    Side
	id      string
	changed bool
}

// pump owns the streams; one goroutine.
type pump struct {
	a      *Audio
	echo   bool // voice processing wanted
	duplex stream
	sides  [2]*side
}

// side is one direction's stream and its health.
type side struct {
	kind     Side
	want     string // the picked device; empty for the default
	st       stream // nil while none is open
	resolved string // the device the stream opened (the default's id then)
	moves    *atomic.Uint64
	last     uint64
	since    time.Time // when the stream opened or last moved
	moved    bool
	unplayed int
	retryAt  time.Time
}

func (p *pump) run() {
	a := p.a
	defer close(a.done)
	stopWatch := a.b.watch(func() {
		select {
		case a.cmds <- command{changed: true}:
		default:
		}
	})
	tick := time.NewTicker(a.t.tick)
	defer tick.Stop()
	for {
		select {
		case <-a.stop:
			stopWatch()
			p.closeAll()
			close(a.mic)
			return
		case c := <-a.cmds:
			now := time.Now()
			if c.changed {
				p.devicesChanged(now)
				continue
			}
			s := p.sides[c.side]
			s.want = c.id
			a.mu.Lock()
			a.selected[c.side] = c.id
			a.mu.Unlock()
			s.unplayed = 0
			p.reopen(s, now)
		case now := <-tick.C:
			p.health(now)
		}
	}
}

func (p *pump) openAll(now time.Time) {
	if p.echo && p.openDuplex(now) {
		return
	}
	p.open(p.sides[Microphone], now)
	p.open(p.sides[Speaker], now)
}

// openDuplex opens both sides through voice processing; false when the
// platform has none or it would not open.
func (p *pump) openDuplex(now time.Time) bool {
	in, out := p.sides[Microphone], p.sides[Speaker]
	inMoves, outMoves := new(atomic.Uint64), new(atomic.Uint64)
	st, err := p.a.b.openDuplex(in.want, out.want, p.deliverer(inMoves), p.filler(outMoves))
	if err != nil {
		p.a.setStats(func(s *Stats) { s.EchoCancel = false })
		return false
	}
	p.duplex = st
	ids := st.devices()
	for i, s := range []*side{in, out} {
		s.st, s.resolved = st, ids[i]
		s.moves = []*atomic.Uint64{inMoves, outMoves}[i]
		s.last, s.since, s.moved = 0, now, false
	}
	p.a.setStats(func(s *Stats) { s.MicOpens++; s.SpeakerOpens++; s.EchoCancel = true })
	return true
}

// open opens one side's stream: the picked device, or the default when it
// will not open (said on FellBack); when even the default will not, it is
// tried again after timing.retry.
func (p *pump) open(s *side, now time.Time) {
	moves := new(atomic.Uint64)
	try := func(id string) (stream, error) {
		if s.kind == Microphone {
			return p.a.b.openInput(id, p.deliverer(moves))
		}
		return p.a.b.openOutput(id, p.filler(moves))
	}
	st, err := try(s.want)
	if err != nil && s.want != "" {
		p.fallBack(s)
		st, err = try("")
	}
	if err != nil {
		s.st, s.retryAt = nil, now.Add(p.a.t.retry)
		return
	}
	s.st, s.resolved, s.moves = st, st.devices()[s.kind], moves
	s.last, s.since, s.moved = 0, now, false
	p.a.setStats(func(st *Stats) {
		if s.kind == Microphone {
			st.MicOpens++
		} else {
			st.SpeakerOpens++
		}
	})
}

// fallBack lets the picked device go for the default.
func (p *pump) fallBack(s *side) {
	s.want, s.unplayed = "", 0
	p.a.mu.Lock()
	p.a.selected[s.kind] = ""
	p.a.mu.Unlock()
	select {
	case p.a.fell <- s.kind:
	default:
	}
}

func (p *pump) deliverer(moves *atomic.Uint64) func([]float32, Format) {
	var c *capture
	return func(pcm []float32, f Format) {
		if c == nil || c.f != f {
			c = newCapture(f)
		}
		moves.Add(1)
		c.push(pcm, p.a.deliver)
	}
}

func (p *pump) filler(moves *atomic.Uint64) func([]float32, Format) {
	var r *render
	return func(out []float32, f Format) {
		if r == nil || r.f != f {
			r = newRender(f, p.a.play)
		}
		moves.Add(1)
		r.fill(out)
	}
}

// reopen closes s's stream and opens it again on s.want. With voice
// processing wanted both sides go together, so a side that had to open
// plainly gets it back once both devices open through it again.
func (p *pump) reopen(s *side, now time.Time) {
	if p.echo {
		p.closeAll()
		p.openAll(now)
		return
	}
	if s.st != nil {
		s.st.close()
		s.st = nil
	}
	p.open(s, now)
}

func (p *pump) closeAll() {
	if p.duplex != nil {
		p.duplex.close()
		p.duplex = nil
		for _, s := range p.sides {
			s.st = nil
		}
	}
	for _, s := range p.sides {
		if s.st != nil {
			s.st.close()
		}
		s.st = nil
	}
}

// health reopens a stream that stopped moving and retries one that would
// not open.
func (p *pump) health(now time.Time) {
	t := p.a.t
	for _, s := range p.sides {
		if s.st == nil {
			if now.After(s.retryAt) {
				p.open(s, now)
			}
			continue
		}
		if n := s.moves.Load(); n != s.last {
			s.last, s.since, s.moved, s.unplayed = n, now, true, 0
			continue
		}
		limit := t.startup
		if s.moved {
			limit = t.stalled
			if s.kind == Microphone {
				limit = t.micSilent
			}
		}
		if now.Sub(s.since) < limit {
			continue
		}
		// Stuck. A picked device that never moved is not there any more,
		// whatever its name says: a microphone at once, a speaker after a
		// few tries (a Bluetooth headset may take its time).
		if !s.moved && s.want != "" {
			s.unplayed++
			if s.kind == Microphone || s.unplayed >= t.unplayed {
				p.fallBack(s)
			}
		}
		p.a.setStats(func(st *Stats) {
			if s.kind == Microphone {
				st.MicRestarts++
			} else {
				st.SpeakerRestarts++
			}
		})
		p.reopen(s, now)
	}
}

// devicesChanged lets a picked device that is gone go for the default, and
// follows a default that moved.
func (p *pump) devicesChanged(now time.Time) {
	var stale []*side
	for _, s := range p.sides {
		input := s.kind == Microphone
		if s.want != "" {
			list, err := p.a.b.devices(input)
			if err == nil && !slices.ContainsFunc(list, func(d Device) bool { return d.ID == s.want }) {
				p.fallBack(s)
				stale = append(stale, s)
			}
			continue
		}
		if def, err := p.a.b.defaultDevice(input); err == nil && s.st != nil && def != s.resolved {
			stale = append(stale, s)
		}
	}
	if p.echo && len(stale) > 0 {
		stale = stale[:1] // one reopen moves both
	}
	for _, s := range stale {
		p.reopen(s, now)
	}
}

// backend is a platform's devices: CoreAudio, or a test's fake. The
// callbacks run on the platform's audio threads, one at a time a stream,
// never after close returns.
type backend interface {
	// devices lists the inputs or the outputs.
	devices(input bool) ([]Device, error)
	// defaultDevice is the id of the system's default input or output.
	defaultDevice(input bool) (string, error)
	// openInput opens input id ("" the default); deliver gets its
	// samples.
	openInput(id string, deliver func([]float32, Format)) (stream, error)
	// openOutput opens output id; fill writes every sample it is handed.
	openOutput(id string, fill func([]float32, Format)) (stream, error)
	// openDuplex opens both through voice processing; ErrUnavailable
	// when there is none.
	openDuplex(in, out string, deliver, fill func([]float32, Format)) (stream, error)
	// watch calls changed when the devices or the defaults change, until
	// the returned func.
	watch(changed func()) func()
}

// stream is one open device stream.
type stream interface {
	// devices are the ids opened: [Microphone] and [Speaker], the one
	// that does not apply empty.
	devices() [2]string
	close()
}

// Microphones lists this system's inputs.
func Microphones() ([]Device, error) {
	b, err := system()
	if err != nil {
		return nil, err
	}
	return b.devices(true)
}

// Speakers lists this system's outputs.
func Speakers() ([]Device, error) {
	b, err := system()
	if err != nil {
		return nil, err
	}
	return b.devices(false)
}

// WatchDevices calls changed (on a system thread) when a device comes or
// goes or a default moves, until the returned func is called: for the
// UI's CallDevices list.
func WatchDevices(changed func()) (stop func(), err error) {
	b, err := system()
	if err != nil {
		return nil, err
	}
	return b.watch(changed), nil
}
