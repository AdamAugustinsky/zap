package audio

import (
	"fmt"
	"math"
	"slices"
	"sync"
	"sync/atomic"
	"time"
)

// fake is a backend of simulated devices: a microphone plays a tone at its
// own rate and channels, a speaker records what it is handed. Streams run
// on their own goroutines at about five times real time.
type fake struct {
	mu      sync.Mutex
	ins     []*fakeDev
	outs    []*fakeDev
	defIn   string
	defOut  string
	duplex  bool // has voice processing
	watchFn func()
	log     []string // "in:a", "out:s1", "duplex:a+s1", "close:…"
}

type fakeDev struct {
	id       string
	f        Format
	tone     float64 // a microphone's frequency
	failOpen bool
	stall    atomic.Bool // stops calling its stream's callback

	mu       sync.Mutex
	recorded []float32 // a speaker's first channel
}

func (d *fakeDev) heard() []float32 {
	d.mu.Lock()
	defer d.mu.Unlock()
	return slices.Clone(d.recorded)
}

func newFake() *fake {
	return &fake{
		ins: []*fakeDev{
			{id: "a", f: Format{48000, 2}, tone: 440},
			{id: "b", f: Format{44100, 1}, tone: 1000},
		},
		outs: []*fakeDev{
			{id: "s1", f: Format{48000, 2}},
			{id: "s2", f: Format{16000, 1}},
		},
		defIn: "a", defOut: "s1",
	}
}

func (f *fake) dev(list []*fakeDev, id string) *fakeDev {
	for _, d := range list {
		if d.id == id {
			return d
		}
	}
	return nil
}

func (f *fake) devices(input bool) ([]Device, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	list := f.outs
	if input {
		list = f.ins
	}
	var out []Device
	for _, d := range list {
		out = append(out, Device{ID: d.id, Label: "Fake " + d.id})
	}
	return out, nil
}

func (f *fake) defaultDevice(input bool) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if input {
		return f.defIn, nil
	}
	return f.defOut, nil
}

func (f *fake) pick(input bool, id string) (*fakeDev, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	list, def := f.outs, f.defOut
	if input {
		list, def = f.ins, f.defIn
	}
	if id == "" {
		id = def
	}
	d := f.dev(list, id)
	if d == nil {
		return nil, fmt.Errorf("%w: %s", ErrNoDevice, id)
	}
	if d.failOpen {
		return nil, &StatusError{Op: "fake open", Status: -1}
	}
	return d, nil
}

func (f *fake) record(s string) {
	f.mu.Lock()
	f.log = append(f.log, s)
	f.mu.Unlock()
}

func (f *fake) opened() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return slices.Clone(f.log)
}

func (f *fake) openInput(id string, deliver func([]float32, Format)) (stream, error) {
	d, err := f.pick(true, id)
	if err != nil {
		return nil, err
	}
	f.record("in:" + d.id)
	return runFake([2]*fakeDev{d, nil}, deliver, nil), nil
}

func (f *fake) openOutput(id string, fill func([]float32, Format)) (stream, error) {
	d, err := f.pick(false, id)
	if err != nil {
		return nil, err
	}
	f.record("out:" + d.id)
	return runFake([2]*fakeDev{nil, d}, nil, fill), nil
}

func (f *fake) openDuplex(in, out string, deliver, fill func([]float32, Format)) (stream, error) {
	if !f.duplex {
		return nil, &UnavailableError{What: "fake voice processing"}
	}
	di, err := f.pick(true, in)
	if err != nil {
		return nil, err
	}
	do, err := f.pick(false, out)
	if err != nil {
		return nil, err
	}
	f.record("duplex:" + di.id + "+" + do.id)
	return runFake([2]*fakeDev{di, do}, deliver, fill), nil
}

func (f *fake) watch(changed func()) func() {
	f.mu.Lock()
	f.watchFn = changed
	f.mu.Unlock()
	return func() {
		f.mu.Lock()
		f.watchFn = nil
		f.mu.Unlock()
	}
}

// notify is the system saying the devices changed.
func (f *fake) notify() {
	f.mu.Lock()
	fn := f.watchFn
	f.mu.Unlock()
	if fn != nil {
		fn()
	}
}

func (f *fake) remove(id string) {
	f.mu.Lock()
	f.ins = slices.DeleteFunc(f.ins, func(d *fakeDev) bool { return d.id == id })
	f.outs = slices.DeleteFunc(f.outs, func(d *fakeDev) bool { return d.id == id })
	f.mu.Unlock()
	f.notify()
}

type fakeStream struct {
	devs [2]*fakeDev
	stop chan struct{}
	done chan struct{}
}

func runFake(devs [2]*fakeDev, deliver, fill func([]float32, Format)) *fakeStream {
	s := &fakeStream{devs: devs, stop: make(chan struct{}), done: make(chan struct{})}
	go func() {
		defer close(s.done)
		tick := time.NewTicker(2 * time.Millisecond) // 10 ms of audio each: five times real time
		defer tick.Stop()
		var phase float64
		for {
			select {
			case <-s.stop:
				return
			case <-tick.C:
			}
			if in := devs[0]; in != nil && deliver != nil && !in.stall.Load() {
				n := in.f.Rate / 100
				buf := make([]float32, n*in.f.Channels)
				for i := range n {
					v := float32(0.5 * math.Sin(phase))
					phase += 2 * math.Pi * in.tone / float64(in.f.Rate)
					for c := range in.f.Channels {
						buf[i*in.f.Channels+c] = v
					}
				}
				deliver(buf, in.f)
			}
			if out := devs[1]; out != nil && fill != nil && !out.stall.Load() {
				n := out.f.Rate / 100
				buf := make([]float32, n*out.f.Channels)
				fill(buf, out.f)
				out.mu.Lock()
				for i := range n {
					out.recorded = append(out.recorded, buf[i*out.f.Channels])
				}
				out.mu.Unlock()
			}
		}
	}()
	return s
}

func (s *fakeStream) devices() [2]string {
	var ids [2]string
	for i, d := range s.devs {
		if d != nil {
			ids[i] = d.id
		}
	}
	return ids
}

func (s *fakeStream) close() {
	close(s.stop)
	<-s.done
}

var fastTiming = timing{
	tick:      5 * time.Millisecond,
	micSilent: 100 * time.Millisecond,
	stalled:   60 * time.Millisecond,
	startup:   200 * time.Millisecond,
	retry:     30 * time.Millisecond,
	unplayed:  3,
}
