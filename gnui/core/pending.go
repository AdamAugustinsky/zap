package core

import (
	"sync/atomic"
	"time"
)

// Pending is a core being opened on a goroutine of its own (OpenAsync). The
// UI thread polls it each frame without blocking or allocating, and shows
// skeleton rows until it is done and the first chat list has been drained.
type Pending struct {
	done atomic.Bool
	ch   chan struct{}
	conn Conn
	err  error

	// Timing is when each step finished (monotonic times); valid once Done.
	Timing Timing
}

// Timing is when OpenAsync's steps happened.
type Timing struct {
	Library string    // the native library the backend loaded (SQLCipher), if any
	Began   time.Time // OpenAsync was called
	Loaded  time.Time // the backend's libraries and preparation were done
	Opened  time.Time // open returned
}

// OpenAsync runs open on a new goroutine, so that loading the backend's
// libraries and opening its stores never holds up a frame. open may set t's
// Library and Loaded (zero: Began); Opened is when it returned. When it is
// done it calls wake (nil: none). Call it first thing in main, before the
// window exists.
func OpenAsync(wake func(), open func(t *Timing) (Conn, error)) *Pending {
	p := &Pending{ch: make(chan struct{})}
	p.Timing.Began = time.Now()
	go func() {
		c, err := open(&p.Timing)
		p.Timing.Opened = time.Now()
		if p.Timing.Loaded.IsZero() {
			p.Timing.Loaded = p.Timing.Began
		}
		if err != nil {
			p.err = err
		} else {
			p.conn = c
		}
		p.done.Store(true)
		close(p.ch)
		if wake != nil {
			wake()
		}
	}()
	return p
}

// Done reports whether opening has finished, successfully or not.
func (p *Pending) Done() bool { return p.done.Load() }

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

// Conn returns the open core, or why it could not be opened. It is valid
// once Done; before that, and never otherwise, it returns nil, nil: a failed
// open is never a non-nil Conn.
func (p *Pending) Conn() (Conn, error) {
	if !p.done.Load() {
		return nil, nil
	}
	if p.err != nil {
		return nil, p.err
	}
	return p.conn, nil
}
