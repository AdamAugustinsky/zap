package core

import (
	"encoding/binary"
	"errors"
	"fmt"
	"math"
	"slices"
	"time"

	"zapfast/abi"
)

// Voice notes (the schema's SendVoice, and DecodeVoice):
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

// ErrUnsupported is returned by a call this build cannot make (DecodeVoice
// without an Opus decoder). It wraps errors.ErrUnsupported.
var ErrUnsupported = fmt.Errorf("zapfast core: not supported here: %w", errors.ErrUnsupported)

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

// VoiceDecoder decodes voice notes for playback: a real account's core, the
// demo's, and fake.Core.
type VoiceDecoder interface {
	// DecodeVoice decodes the Ogg/Opus file at path (absolute: a message's
	// media path) into a new blob of PCM in VoiceFormat. The blob is the
	// caller's: read it with ReadBlob, then FreeBlob it. It blocks while it
	// decodes (some milliseconds a minute of audio): call it on a worker
	// goroutine, never the UI thread. Concurrent calls decode in parallel.
	DecodeVoice(path string) (abi.BlobRef, VoiceFormat, error)
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
