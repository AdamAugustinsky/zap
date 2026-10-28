package fake

import (
	"encoding/binary"
	"math"
	"testing"

	"zapfast/core"
)

func TestDecodeVoice(t *testing.T) {
	f := New(nil)
	var d core.VoiceDecoder = f
	ref, vf, err := d.DecodeVoice("/cache/media/note.ogg")
	if err != nil {
		t.Fatal(err)
	}
	if vf.Rate != core.VoiceRate || vf.Channels != 1 || vf.Frames != VoiceSeconds*core.VoiceRate || ref.Len != uint64(vf.Frames)*4 {
		t.Fatalf("%+v, %d bytes", vf, ref.Len)
	}
	if vf.Duration().Seconds() != VoiceSeconds {
		t.Fatalf("%v", vf.Duration())
	}
	lo, hi := byte(100), byte(0)
	for _, b := range vf.Waveform {
		lo, hi = min(lo, b), max(hi, b)
	}
	if hi != 100 || lo > 40 {
		t.Fatalf("the tone swells and fades: %v", vf.Waveform)
	}
	b := make([]byte, 4)
	if n, err := f.ReadBlob(ref.ID, 4*12, b); n != 4 || err != nil {
		t.Fatal(n, err)
	}
	if got, want := math.Float32frombits(binary.LittleEndian.Uint32(b)), Tone(VoiceSeconds, 440)[12]; got != want {
		t.Fatalf("sample 12: %v, want %v", got, want)
	}
	f.FreeBlob(ref.ID)

	f.SetVoice("/cache/a.ogg", []float32{0.5, -0.25})
	f.RefuseVoice("/cache/bad.ogg", core.EMalformed, "not a voice note")
	if ref, vf, err := f.DecodeVoice("/cache/a.ogg"); err != nil || vf.Frames != 2 || ref.Len != 8 || vf.Waveform[0] != 100 {
		t.Fatalf("%v %+v %v", ref, vf, err)
	}
	if _, _, err := f.DecodeVoice("/cache/bad.ogg"); !core.IsCode(err, core.EMalformed) {
		t.Fatal(err)
	}
	if _, _, err := f.DecodeVoice("note.ogg"); !core.IsCode(err, core.EArg) {
		t.Fatal(err)
	}
	if got := f.Decoded(); len(got) != 4 || got[3] != "note.ogg" {
		t.Fatalf("%q", got)
	}
	f.Close(0)
	if _, _, err := f.DecodeVoice("/cache/a.ogg"); err != core.ErrClosed {
		t.Fatal(err)
	}
}
