package demo

import (
	"zapfast/abi"
	"zapfast/core"
	"zapfast/gocore"
	"zapfast/gocore/av"
)

// Conn is a demo core: a gocore.Conn over the demo Engine, with what the
// UI asks of a core besides core.Conn: the calls' frames and numbers, and
// voice notes decoded from the demo's media folder.
type Conn struct {
	*gocore.Conn
	e     *Engine
	voice av.VoiceDecoder
}

var (
	_ core.Conn         = (*Conn)(nil)
	_ core.Frames       = (*Conn)(nil)
	_ core.CallNumbers  = (*Conn)(nil)
	_ core.VoiceDecoder = (*Conn)(nil)
)

// Open opens a demo core with cfg (Demo set, which Open sets): nothing of an
// account's is read or written. Only Wake matters.
func Open(cfg core.Config) (*Conn, error) {
	cfg.Demo = true
	e := New()
	c, err := gocore.Open(cfg, e)
	if err != nil {
		return nil, err
	}
	d := &Conn{Conn: c, e: e}
	d.voice = av.VoiceDecoder{
		Roots: func() []string { return []string{MediaDir()} },
		Put:   func(pcm []byte) abi.BlobRef { return c.Host().Blobs().Keep(pcm) },
	}
	return d, nil
}

// Frame is the newest frame of a call's video stream, valid until
// ReleaseFrame(stream); false while the stream has none, or while its last
// frame is still held.
func (c *Conn) Frame(stream uint32) (core.Frame, bool) {
	f := c.e.frames.acquire(stream)
	if f == nil {
		return core.Frame{}, false
	}
	return f.core(), true
}

// ReleaseFrame gives the frame of stream back.
func (c *Conn) ReleaseFrame(stream uint32) { c.e.frames.release(stream) }

// CallGeneration is a generation no call has had yet: the demo numbers the
// calls it rings in from the same counter.
func (c *Conn) CallGeneration() uint64 { return nextGeneration() }

// DecodeVoice decodes a voice note in the demo's media folder, as zfcore's
// zf_voice_decode does on a demo core.
func (c *Conn) DecodeVoice(path string) (abi.BlobRef, core.VoiceFormat, error) {
	select {
	case <-c.Done():
		return abi.BlobRef{}, core.VoiceFormat{}, core.ErrClosed
	default:
	}
	return c.voice.DecodeVoice(path)
}
