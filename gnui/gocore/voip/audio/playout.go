package audio

import (
	"sync"
	"time"
)

// Playout is the speaker's buffer between the engine and the device: the
// engine writes 16 kHz frames when they arrive, the device reads what it
// needs on its own clock. It absorbs the jitter between the two:
//
//   - it starts (and, after running dry, starts again) only once Target
//     samples are in, so one late frame is not a click every 60 ms;
//   - running dry plays silence and counts an underrun;
//   - past Capacity it lets the oldest samples go and counts them, so a
//     burst, or a device clock slower than the sender's, costs a skip and
//     never a growing delay.
//
// It outlives the devices: a speaker switch reads on from the same buffer.
// Its methods are safe from any goroutine.
type Playout struct {
	mu       sync.Mutex
	buf      []int16 // a ring of capacity samples
	head, n  int
	target   int
	primed   bool
	stats    PlayoutStats
	capacity int
}

// PlayoutStats are a playout buffer's counters.
type PlayoutStats struct {
	// Buffered is the samples waiting now.
	Buffered int
	// Played is the samples the device has taken, silence included.
	Played uint64
	// Underruns counts the times the device found the buffer dry.
	Underruns uint64
	// Dropped is the samples let go past the capacity.
	Dropped uint64
}

// Buffering defaults: up to five engine frames (300 ms) held, playing from
// 40 ms in.
const (
	DefaultPlayoutCapacity = 5 * FrameSamples
	DefaultPlayoutTarget   = 640
)

// NewPlayout holds up to capacity samples and plays from target in.
func NewPlayout(capacity, target int) *Playout {
	capacity = max(capacity, 1)
	return &Playout{buf: make([]int16, capacity), capacity: capacity, target: min(max(target, 0), capacity)}
}

// Write queues samples for the speaker. It never blocks; it returns how
// many older samples it let go to make room.
func (p *Playout) Write(samples []int16) (dropped int) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if len(samples) > p.capacity {
		dropped += len(samples) - p.capacity
		samples = samples[len(samples)-p.capacity:]
	}
	if over := p.n + len(samples) - p.capacity; over > 0 {
		p.head = (p.head + over) % p.capacity
		p.n -= over
		dropped += over
	}
	tail := (p.head + p.n) % p.capacity
	k := copy(p.buf[tail:], samples)
	copy(p.buf, samples[k:])
	p.n += len(samples)
	p.stats.Dropped += uint64(dropped)
	if p.n >= p.target {
		p.primed = true
	}
	return dropped
}

// Read fills dst for the device: buffered samples, then silence.
func (p *Playout) Read(dst []int16) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.stats.Played += uint64(len(dst))
	if !p.primed {
		clear(dst)
		return
	}
	k := min(len(dst), p.n)
	first := copy(dst[:k], p.buf[p.head:])
	copy(dst[first:k], p.buf)
	p.head = (p.head + k) % p.capacity
	p.n -= k
	if k < len(dst) {
		clear(dst[k:])
		p.stats.Underruns++
		p.primed = p.target == 0
	}
}

// Reset empties the buffer, as for another call.
func (p *Playout) Reset() {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.head, p.n, p.primed = 0, 0, p.target == 0
}

// Stats are the buffer's counters now.
func (p *Playout) Stats() PlayoutStats {
	p.mu.Lock()
	defer p.mu.Unlock()
	s := p.stats
	s.Buffered = p.n
	return s
}

// Delay is the audio waiting, as time.
func (p *Playout) Delay() time.Duration {
	return time.Duration(p.Stats().Buffered) * time.Second / Rate
}
