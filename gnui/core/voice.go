package core

import (
	"encoding/binary"
	"errors"
	"fmt"
	"math"
	"slices"
	"sync"
	"time"
	"unsafe"

	"gonativeui/clib"
	"zapfast/abi"
)

// Voice notes (zfcore.h, "Voice-note playback", and the schema's SendVoice):
// both directions are the same PCM, mono at 48 kHz, each sample a
// little-endian float32 in about -1 to 1. That is the framework's
// media.Format{SampleRate: VoiceRate, Channels: 1, Sample: media.Float32},
// so a decoded blob's bytes play as they are.
const (
	// VoiceRate is the samples a second of both directions.
	VoiceRate = 48000
	// VoiceBars is the number of bars in a voice note's waveform.
	VoiceBars = 64
	// VoiceLongestSend is the longest recording SendVoice takes
	// (172,800,000 bytes); a longer one is refused with ERefused.
	VoiceLongestSend = 15 * time.Minute
	// VoiceLongestDecode is the longest note DecodeVoice decodes; a longer
	// one, or a file over 64 MiB, is ETooLarge.
	VoiceLongestDecode = 30 * time.Minute
)

// ErrUnsupported is returned by a call the loaded library is too old for
// (DecodeVoice without zf_voice_decode). It wraps errors.ErrUnsupported.
var ErrUnsupported = fmt.Errorf("zapfast core: the library is older than this call: %w", errors.ErrUnsupported)

// VoiceFormat describes a decoded voice note's PCM.
type VoiceFormat struct {
	Rate     int   // VoiceRate
	Channels int   // 1
	Frames   int64 // samples; the blob is Frames*4 bytes
	// Waveform is the note's bars, 0 to 100, as the core computes them for
	// a note it sends.
	Waveform [VoiceBars]byte
}

// Duration is how long the note plays at 1×.
func (f VoiceFormat) Duration() time.Duration {
	if f.Rate <= 0 {
		return 0
	}
	return time.Duration(f.Frames) * time.Second / time.Duration(f.Rate)
}

// VoiceDecoder decodes voice notes for playback: *Core, and fake.Core.
type VoiceDecoder interface {
	// DecodeVoice decodes the Ogg/Opus file at path (absolute: a message's
	// media path) into a new blob of PCM in VoiceFormat. The blob is the
	// caller's: read it with ReadBlob, then FreeBlob it. It blocks while it
	// decodes (some milliseconds a minute of audio): call it on a worker
	// goroutine, never the UI thread. Concurrent calls decode in parallel.
	DecodeVoice(path string) (abi.BlobRef, VoiceFormat, error)
}

var _ VoiceDecoder = (*Core)(nil)

// zfVoice is zf_voice, filled by zf_voice_decode.
type zfVoice struct {
	blob, len, frames uint64
	rate, channels    uint32
	waveform          [VoiceBars]byte
}

// voiceCall is one zf_voice_decode in flight: its clib.Call and everything
// it passes by pointer, in one heap struct. Decodes run in parallel, so each
// takes its own from the pool.
type voiceCall struct {
	call *clib.Call
	err  zfErr
	out  zfVoice
	path []byte
}

var voiceCalls = sync.Pool{New: func() any { return &voiceCall{call: clib.NewCall()} }}

// DecodeVoice decodes a voice note of the account's (zf_voice_decode); see
// VoiceDecoder. Only files under the account's own folders are read (its
// state and cache, the download folder SetDownloadFolder last named; the
// demo core's media folder): any other path is EArg. A missing file is
// ENotFound, a damaged one EMalformed. Against a library built before
// zf_voice_decode it returns ErrUnsupported. Close waits for decodes in
// progress before it closes the core.
func (c *Core) DecodeVoice(path string) (abi.BlobRef, VoiceFormat, error) {
	if c.lib.fn.voiceDecode == (clib.Func{}) {
		return abi.BlobRef{}, VoiceFormat{}, ErrUnsupported
	}
	s := &c.voiceS
	s.mu.Lock()
	h := c.h
	if h != nil {
		c.decoding.Add(1)
	}
	s.mu.Unlock()
	if h == nil {
		return abi.BlobRef{}, VoiceFormat{}, ErrClosed
	}
	defer c.decoding.Done()

	v := voiceCalls.Get().(*voiceCall)
	defer voiceCalls.Put(v)
	v.path = append(v.path[:0], path...)
	v.out = zfVoice{}
	var p unsafe.Pointer
	if len(v.path) > 0 {
		p = unsafe.Pointer(&v.path[0])
	}
	st := v.call.Reset().Ptr(h).Ptr(p).Uint(uint64(len(v.path))).
		Ptr(unsafe.Pointer(&v.out)).Ptr(unsafe.Pointer(&v.err)).Do(c.lib.fn.voiceDecode).Int32()
	if st != StatusOK {
		return abi.BlobRef{}, VoiceFormat{}, v.err.error("zf_voice_decode", st)
	}
	return abi.BlobRef{ID: v.out.blob, Len: v.out.len}, VoiceFormat{
		Rate:     int(v.out.rate),
		Channels: int(v.out.channels),
		Frames:   int64(v.out.frames),
		Waveform: v.out.waveform,
	}, nil
}

// AppendVoicePCM16 appends to dst the samples SendVoice takes (mono, 48 kHz,
// little-endian float32) for pcm: 16-bit little-endian signed samples,
// interleaved by channel, at rate. Each sample becomes s/32768; channels are
// averaged to mono, and another rate is resampled to 48 kHz linearly, as
// upstream's voice::mono_at_rate does. The framework's media.Recorder
// records mono 48 kHz Int16 by default, which is only the scaling. The core
// normalizes the level itself. Put the result with PutBlob for SendVoice;
// keep it under VoiceLongestSend.
func AppendVoicePCM16(dst, pcm []byte, rate, channels int) []byte {
	channels = max(channels, 1)
	frames := len(pcm) / (2 * channels)
	mono := func(i int) float32 {
		var sum int32
		for ch := range channels {
			o := 2 * (i*channels + ch)
			sum += int32(int16(binary.LittleEndian.Uint16(pcm[o:])))
		}
		return float32(sum) / float32(channels) / 32768
	}
	put := func(dst []byte, s float32) []byte {
		return binary.LittleEndian.AppendUint32(dst, math.Float32bits(s))
	}
	if rate == VoiceRate || rate <= 0 || frames == 0 {
		dst = slices.Grow(dst, 4*frames)
		for i := range frames {
			dst = put(dst, mono(i))
		}
		return dst
	}
	ratio := float64(rate) / VoiceRate
	count := int(math.Floor(float64(frames) / ratio))
	dst = slices.Grow(dst, 4*count)
	for i := range count {
		pos := float64(i) * ratio
		left := int(math.Floor(pos))
		t := float32(pos - float64(left))
		a := mono(min(left, frames-1))
		b := a
		if left+1 < frames {
			b = mono(left + 1)
		}
		dst = put(dst, a+(b-a)*t)
	}
	return dst
}
