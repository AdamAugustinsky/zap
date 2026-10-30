package av

import (
	"encoding/binary"
	"math"
	"time"
)

// Voice notes, as upstream's voice.rs and zfcore's voice.rs define them.
const (
	// Rate is the samples a second of every voice note, mono.
	Rate = 48000
	// Bars is the number of bars in a voice note's waveform.
	Bars = 64
	// VoiceMIME is the type a voice note is uploaded with.
	VoiceMIME = "audio/ogg; codecs=opus"
	// LongestRecording is the longest recording PrepareVoice takes:
	// upstream's recorder stops there.
	LongestRecording = 15 * time.Minute
	// LongestNote is the longest note DecodeVoiceFile decodes.
	LongestNote = 30 * time.Minute
	// LargestNote is the largest file DecodeVoiceFile reads.
	LargestNote = 64 << 20
)

// Normalize brings a quiet recording up to just below full scale (a peak of
// 0.89), with the gain capped at 10× (20 dB). A recording already that loud
// is left alone. It is upstream's voice::normalize, in float32 as there.
func Normalize(samples []float32) {
	var peak float32
	for _, s := range samples {
		if a := abs32(s); a > peak {
			peak = a
		}
	}
	if peak <= 0 {
		return
	}
	gain := min(max(0.89/peak, 1), 10)
	if gain > 1 {
		for i := range samples {
			samples[i] *= gain
		}
	}
}

// Waveform is WhatsApp's 64 bars of a clip, 0 to 100: the loudness (RMS) of
// 64 equal slices, the loudest 100. A clip shorter than 64 samples leaves
// the bars past its end 0, and silence is all 0. It is upstream's
// voice::waveform, in float32 as there, so it gives the same bars.
func Waveform(samples []float32) (bars [Bars]byte) {
	if len(samples) == 0 {
		return bars
	}
	slice := (len(samples) + Bars - 1) / Bars
	var loud [Bars]float32
	var loudest float32
	n := 0
	for start := 0; start < len(samples); start += slice {
		chunk := samples[start:min(start+slice, len(samples))]
		var sum float32
		for _, s := range chunk {
			sum += float32(s * s) // rounded first, as Rust does: no fused multiply-add
		}
		loud[n] = sqrt32(sum / float32(len(chunk)))
		loudest = max32(loudest, loud[n])
		n++
	}
	if loudest <= 0 {
		return bars
	}
	for i, v := range loud[:n] {
		bars[i] = saturateU8(math.Round(float64(float32(v / loudest * 100))))
	}
	return bars
}

// MonoAtRate mixes interleaved channels to mono and resamples linearly to
// Rate: upstream's voice::mono_at_rate. A rate of 0 or Rate is left as it
// is.
func MonoAtRate(interleaved []float32, channels, rate int) []float32 {
	channels = max(channels, 1)
	mono := make([]float32, len(interleaved)/channels)
	for i := range mono {
		var sum float32
		for _, s := range interleaved[i*channels : (i+1)*channels] {
			sum += s
		}
		mono[i] = sum / float32(channels)
	}
	if rate == Rate || rate <= 0 || len(mono) == 0 {
		return mono
	}
	ratio := float64(rate) / Rate
	out := make([]float32, int(math.Floor(float64(len(mono))/ratio)))
	for i := range out {
		pos := float64(i) * ratio
		left := int(math.Floor(pos))
		t := float32(pos - float64(left))
		a := mono[min(left, len(mono)-1)]
		b := a
		if left+1 < len(mono) {
			b = mono[left+1]
		}
		out[i] = a + float32((b-a)*t)
	}
	return out
}

// Voice is a recording ready to send as a voice note (WhatsApp's push to
// talk): its Ogg/Opus bytes, its length and its waveform.
type Voice struct {
	Ogg      []byte
	Seconds  uint32 // rounded, at least 1
	Waveform [Bars]byte
}

// PrepareVoice is what SendVoice does before the upload: it normalizes a
// copy of samples (mono, Rate, -1 to 1), measures it and encodes it. A
// recording over LongestRecording is ErrTooLong. It blocks for some
// milliseconds a minute of audio.
func PrepareVoice(samples []float32) (Voice, error) {
	if len(samples) > int(LongestRecording/time.Second)*Rate {
		return Voice{}, ErrTooLong
	}
	level := append([]float32(nil), samples...)
	Normalize(level)
	ogg, err := EncodeOpus(level)
	if err != nil {
		return Voice{}, err
	}
	seconds := max(math.Round(float64(len(level))/Rate), 1)
	return Voice{Ogg: ogg, Seconds: uint32(seconds), Waveform: Waveform(level)}, nil
}

// SamplesFromPCM reads SendVoice's blob: little-endian float32 samples. Its
// length must be whole samples (ErrMalformed) and at most LongestRecording
// (ErrTooLong), as zfcore checks it.
func SamplesFromPCM(pcm []byte) ([]float32, error) {
	if len(pcm)%4 != 0 {
		return nil, malformed("samples are whole little-endian float32s")
	}
	if len(pcm) > 4*int(LongestRecording/time.Second)*Rate {
		return nil, ErrTooLong
	}
	out := make([]float32, len(pcm)/4)
	for i := range out {
		out[i] = math.Float32frombits(binary.LittleEndian.Uint32(pcm[4*i:]))
	}
	return out, nil
}

// PCM is samples as little-endian float32 bytes: DecodeVoice's blob.
func PCM(samples []float32) []byte {
	out := make([]byte, 4*len(samples))
	for i, s := range samples {
		binary.LittleEndian.PutUint32(out[4*i:], math.Float32bits(s))
	}
	return out
}

func abs32(v float32) float32 { return math.Float32frombits(math.Float32bits(v) &^ (1 << 31)) }

func sqrt32(v float32) float32 { return float32(math.Sqrt(float64(v))) }

// max32 is Rust's f32::max: a NaN on either side gives the other.
func max32(a, b float32) float32 {
	if b > a || a != a {
		return b
	}
	return a
}

// saturateU8 is Rust's `as u8` of a float: NaN is 0, and it saturates.
func saturateU8(v float64) byte {
	switch {
	case v != v || v <= 0:
		return 0
	case v >= 255:
		return 255
	}
	return byte(v)
}
