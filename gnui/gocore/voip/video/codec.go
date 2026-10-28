package video

import (
	"fmt"
	"sync"
)

// EncoderConfig is what an Encoder is made for.
type EncoderConfig struct {
	// Width and Height are the pictures' size; both even.
	Width, Height int
	// FPS is the frames a second the camera is sent at (15 when 0); the
	// encoder makes a keyframe every two seconds of them.
	FPS int
	// Bitrate is the average bits a second (600 kbps when 0).
	Bitrate int
}

// Defaults for an EncoderConfig's zero fields.
const (
	DefaultFPS     = 15
	DefaultBitrate = 600_000
)

// Encoder is an H.264 encoder for one camera size: Constrained Baseline
// (what WhatsApp's clients decode), real-time rate control, no frame
// reordering, a keyframe every two seconds and on request. Its access
// units are Annex-B, each led by an access unit delimiter, and a keyframe
// carries its SPS and PPS. Its methods are safe from any goroutine.
type Encoder struct {
	mu      sync.Mutex
	cfg     EncoderConfig
	sys     *sysEncoder
	frame   int64
	wantKey bool
}

// NewEncoder makes an encoder for cfg: VideoToolbox's on macOS, an error
// matching ErrUnavailable elsewhere.
func NewEncoder(cfg EncoderConfig) (*Encoder, error) {
	if cfg.Width <= 0 || cfg.Height <= 0 || cfg.Width%2 != 0 || cfg.Height%2 != 0 {
		return nil, fmt.Errorf("%w: %dx%d is not an even size", ErrFrame, cfg.Width, cfg.Height)
	}
	if cfg.FPS <= 0 {
		cfg.FPS = DefaultFPS
	}
	if cfg.Bitrate <= 0 {
		cfg.Bitrate = DefaultBitrate
	}
	s, err := newSysEncoder(cfg)
	if err != nil {
		return nil, err
	}
	return &Encoder{cfg: cfg, sys: s}, nil
}

// Config is the encoder's configuration, its bitrate the latest set.
func (e *Encoder) Config() EncoderConfig {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.cfg
}

// Encode encodes one picture, of the encoder's size, into an Annex-B access
// unit; a keyframe when keyframe is set or one was requested. A nil unit
// and no error means the encoder dropped the picture to keep its rate.
func (e *Encoder) Encode(p *I420, keyframe bool) ([]byte, error) {
	if !p.Valid() || p.W != e.cfg.Width || p.H != e.cfg.Height {
		return nil, fmt.Errorf("%w: want %dx%d", ErrFrame, e.cfg.Width, e.cfg.Height)
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.sys == nil {
		return nil, ErrClosed
	}
	key := keyframe || e.wantKey || e.frame == 0
	unit, err := e.sys.encode(p, e.frame, key)
	if err != nil {
		return nil, err
	}
	e.frame++
	if key && unit != nil {
		e.wantKey = false
	}
	return unit, nil
}

// RequestKeyframe makes the next Encode a keyframe (the peer lost one).
func (e *Encoder) RequestKeyframe() {
	e.mu.Lock()
	e.wantKey = true
	e.mu.Unlock()
}

// SetBitrate moves the running encoder to bps without a keyframe.
func (e *Encoder) SetBitrate(bps int) error {
	if bps <= 0 {
		return fmt.Errorf("video: a bitrate of %d", bps)
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.sys == nil {
		return ErrClosed
	}
	if err := e.sys.setBitrate(bps); err != nil {
		return err
	}
	e.cfg.Bitrate = bps
	return nil
}

// Close releases the encoder; later calls fail with ErrClosed.
func (e *Encoder) Close() {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.sys != nil {
		e.sys.close()
		e.sys = nil
	}
}

// Decoder decodes one peer's H.264 stream of Annex-B access units into I420
// pictures. It starts at a keyframe: until then (and after a frame it could
// not decode) a delta frame gives ErrNeedKeyframe. A new SPS or PPS mid
// stream (the peer changed size) is followed. Its methods are safe from any
// goroutine.
type Decoder struct {
	mu  sync.Mutex
	sys *sysDecoder
}

// NewDecoder makes a decoder: VideoToolbox's on macOS, an error matching
// ErrUnavailable elsewhere.
func NewDecoder() (*Decoder, error) {
	s, err := newSysDecoder()
	if err != nil {
		return nil, err
	}
	return &Decoder{sys: s}, nil
}

// Decode decodes one access unit. A nil picture and no error is a unit
// with no picture in it (parameter sets only).
func (d *Decoder) Decode(unit []byte) (*I420, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.sys == nil {
		return nil, ErrClosed
	}
	return d.sys.decode(unit)
}

// Close releases the decoder; later calls fail with ErrClosed.
func (d *Decoder) Close() {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.sys != nil {
		d.sys.close()
		d.sys = nil
	}
}
