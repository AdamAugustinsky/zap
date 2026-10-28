package core

import (
	"sync/atomic"
	"time"
)

// Pending is a core being loaded and opened on a goroutine of its own
// (OpenAsync). The UI thread polls it each frame without blocking or
// allocating, and shows skeleton rows until it is done and the first chat
// list has been drained.
type Pending struct {
	done atomic.Bool
	ch   chan struct{}
	core *Core
	err  error

	// Timing is when each step finished (monotonic times); valid once Done.
	Timing Timing
}

// Timing is when OpenAsync's steps happened.
type Timing struct {
	Library string        // the path loaded
	Began   time.Time     // OpenAsync was called
	Prewarm time.Duration // of loading, the part before dlopen (Lib.PrewarmTime)
	Loaded  time.Time     // dlopen, lookups and the version check done
	Opened  time.Time     // zf_core_open returned and the event goroutine runs
}

// OpenAsync loads the library and opens a core with cfg on a new goroutine,
// so that neither the dlopen (about 330 ms the first time a newly installed
// library loads) nor zf_core_open ever holds up a frame. Call it first thing
// in main, before the window exists. When it is done it calls cfg.Wake.
func OpenAsync(cfg Config) *Pending {
	p := &Pending{ch: make(chan struct{})}
	p.Timing.Began = time.Now()
	go func() {
		p.core, p.err = p.open(cfg)
		p.done.Store(true)
		close(p.ch)
		if cfg.Wake != nil {
			cfg.Wake()
		}
	}()
	return p
}

func (p *Pending) open(cfg Config) (*Core, error) {
	path, err := ResolveLibrary(cfg.Library)
	if err != nil {
		return nil, err
	}
	p.Timing.Library = path
	lib, err := Load(path)
	if err != nil {
		return nil, err
	}
	p.Timing.Loaded = time.Now()
	p.Timing.Prewarm = lib.PrewarmTime
	c, err := lib.Open(cfg)
	p.Timing.Opened = time.Now()
	return c, err
}

// Done reports whether opening has finished, successfully or not.
func (p *Pending) Done() bool { return p.done.Load() }

// Result returns the core, or why it could not be opened. It is valid once
// Done; before that it returns nil, nil.
func (p *Pending) Result() (*Core, error) {
	if !p.done.Load() {
		return nil, nil
	}
	return p.core, p.err
}

// Wait waits up to d for opening to finish and reports whether it has.
// Never wait on the UI thread beyond the first-frame cap.
func (p *Pending) Wait(d time.Duration) bool {
	if p.done.Load() {
		return true
	}
	if d <= 0 {
		return false
	}
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-p.ch:
		return true
	case <-t.C:
		return false
	}
}
