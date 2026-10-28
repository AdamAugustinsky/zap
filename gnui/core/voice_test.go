package core

import (
	"encoding/binary"
	"math"
	"testing"
)

func TestAppendVoicePCM16(t *testing.T) {
	f32 := func(b []byte) []float32 {
		out := make([]float32, len(b)/4)
		for i := range out {
			out[i] = math.Float32frombits(binary.LittleEndian.Uint32(b[4*i:]))
		}
		return out
	}
	le := func(v ...int16) []byte {
		var b []byte
		for _, x := range v {
			b = binary.LittleEndian.AppendUint16(b, uint16(x))
		}
		return b
	}
	if got := f32(AppendVoicePCM16(nil, le(0, 16384, -32768, 32767), VoiceRate, 1)); len(got) != 4 ||
		got[0] != 0 || got[1] != 0.5 || got[2] != -1 || got[3] != 32767.0/32768 {
		t.Fatalf("mono 48 kHz: %v", got)
	}
	if got := f32(AppendVoicePCM16([]byte{1, 2, 3, 4}, le(16384, 0, -16384, -16384), VoiceRate, 2)); len(got) != 3 ||
		got[1] != 0.25 || got[2] != -0.5 {
		t.Fatalf("stereo, after dst's 4 bytes: %v", got)
	}
	// 24 kHz doubles, interpolated between neighbours.
	if got := f32(AppendVoicePCM16(nil, le(0, 16384, 16384), 24000, 1)); len(got) != 6 ||
		got[0] != 0 || got[1] != 0.25 || got[2] != 0.5 || got[5] != 0.5 {
		t.Fatalf("24 kHz: %v", got)
	}
	if got := AppendVoicePCM16(nil, le(1), VoiceRate, 2); len(got) != 0 {
		t.Fatalf("half a frame: %v", got)
	}
}
