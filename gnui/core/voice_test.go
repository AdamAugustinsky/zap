package core

import (
	"encoding/binary"
	"errors"
	"math"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"unsafe"

	"gonativeui/clib"
	"zapfast/abi"
)

// voiceNotes is a Handler noting the voice notes with a file that drained
// messages carry, by message id.
type voiceNotes struct {
	*recorder
	paths map[string]string
	bars  map[string]int
}

func (v *voiceNotes) Event(e *abi.Event) {
	if e.Tag == abi.EventTagMessages {
		for i := range e.Messages.Messages {
			m := &e.Messages.Messages[i]
			if a := &m.Content.Audio; m.Content.Tag == abi.ContentTagAudio && a.VoiceNote && a.Media.HasPath {
				v.paths[string(m.ID)] = string(a.Media.Path)
				v.bars[string(m.ID)] = len(a.Waveform)
			}
		}
	}
	v.recorder.Event(e)
}

func readAll(t *testing.T, c Conn, ref abi.BlobRef) []byte {
	t.Helper()
	out := make([]byte, ref.Len)
	for off := 0; off < len(out); {
		n, err := c.ReadBlob(ref.ID, uint64(off), out[off:min(off+1<<20, len(out))])
		if err != nil || n == 0 {
			t.Fatalf("read at %d: %d, %v", off, n, err)
		}
		off += n
	}
	return out
}

func TestDemoVoiceNotesDecode(t *testing.T) {
	s := openDemo(t)
	notes := &voiceNotes{recorder: s.h, paths: map[string]string{}, bars: map[string]int{}}
	var cmd abi.Command
	cmd.Tag = abi.CommandTagLoadChat
	cmd.LoadChat = abi.CommandLoadChat{Chat: []byte(ada)}
	s.send(&cmd)
	s.h.match, s.h.matched = func(*abi.Event) bool { return notes.paths["DEMOVOICE0"] != "" }, false
	for !s.h.matched {
		if _, err := s.c.Drain(notes, 0); err != nil {
			t.Fatal(err)
		}
		if !s.h.matched {
			<-s.w.ch
		}
	}
	path := notes.paths["DEMOVOICE0"]
	if notes.bars["DEMOVOICE0"] != VoiceBars {
		t.Fatalf("%d waveform bars", notes.bars["DEMOVOICE0"])
	}

	// In parallel, as worker goroutines would.
	var wg sync.WaitGroup
	refs := make([]abi.BlobRef, 4)
	formats := make([]VoiceFormat, 4)
	errs := make([]error, 4)
	for i := range refs {
		wg.Go(func() { refs[i], formats[i], errs[i] = s.c.DecodeVoice(path) })
	}
	wg.Wait()
	for i := range refs {
		f := formats[i]
		if errs[i] != nil {
			t.Fatal(errs[i])
		}
		if f.Rate != VoiceRate || f.Channels != 1 || refs[i].Len != uint64(f.Frames)*4 {
			t.Fatalf("%+v, blob %d bytes", f, refs[i].Len)
		}
		if d := f.Duration(); d < 4e9 || d > 5e9 {
			t.Fatalf("Ada's note is about 4.2 s, got %v", d)
		}
	}
	pcm := readAll(t, s.c, refs[0])
	var peak float32
	for i := 0; i < len(pcm); i += 4 {
		peak = max(peak, float32(math.Abs(float64(math.Float32frombits(binary.LittleEndian.Uint32(pcm[i:]))))))
	}
	if peak < 0.1 || peak > 1.1 {
		t.Fatalf("peak %v", peak)
	}
	for _, r := range refs {
		s.c.FreeBlob(r.ID)
	}
	if _, err := s.c.ReadBlob(refs[0].ID, 0, pcm[:4]); !IsCode(err, ENotFound) {
		t.Fatalf("a freed note's blob: %v", err)
	}

	for _, refused := range []struct {
		path string
		code int32
	}{
		{"/etc/hosts", EArg},
		{"relative/note.ogg", EArg},
		{"", EArg},
		{filepath.Join(filepath.Dir(path), "gone.ogg"), ENotFound},
	} {
		if _, _, err := s.c.DecodeVoice(refused.path); !IsCode(err, refused.code) {
			t.Errorf("%q: %v, want %s", refused.path, err, CodeName(refused.code))
		}
	}
	if err := s.c.Close(5e9); err != nil {
		t.Fatal(err)
	}
	if _, _, err := s.c.DecodeVoice(path); err != ErrClosed {
		t.Fatalf("after Close: %v", err)
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("the demo's own notes stay: %v", err)
	}
}

func TestDemoSentVoiceNotePlaysBack(t *testing.T) {
	s := openDemo(t)
	notes := &voiceNotes{recorder: s.h, paths: map[string]string{}, bars: map[string]int{}}
	// A second of a 16-bit stereo 24 kHz recording, converted.
	rec := make([]byte, 0, 24000*4)
	for i := range 24000 {
		v := int16(12000 * math.Sin(2*math.Pi*300*float64(i)/24000))
		rec = binary.LittleEndian.AppendUint16(rec, uint16(v))
		rec = binary.LittleEndian.AppendUint16(rec, uint16(v))
	}
	samples := AppendVoicePCM16(nil, rec, 24000, 2)
	ref, err := s.c.PutBlob(samples)
	if err != nil {
		t.Fatal(err)
	}
	var cmd abi.Command
	cmd.Tag = abi.CommandTagSendVoice
	cmd.SendVoice = abi.CommandSendVoice{Chat: []byte(ada), Samples: ref}
	s.send(&cmd)
	var sent string
	s.h.match, s.h.matched = func(*abi.Event) bool {
		for id, p := range notes.paths {
			if !strings.HasPrefix(id, "DEMOVOICE") {
				sent = p
			}
		}
		return sent != ""
	}, false
	for !s.h.matched {
		if _, err := s.c.Drain(notes, 0); err != nil {
			t.Fatal(err)
		}
		if !s.h.matched {
			<-s.w.ch
		}
	}
	ref, f, err := s.c.DecodeVoice(sent)
	if err != nil {
		t.Fatal(err)
	}
	if d := f.Duration(); d < 1e9 || d > 1.1e9 {
		t.Fatalf("a second sent, %v decoded", d)
	}
	s.c.FreeBlob(ref.ID)

	cmd.SendVoice.Samples = abi.BlobRef{ID: 1 << 40, Len: uint64(VoiceLongestSend.Seconds()*VoiceRate)*4 + 4}
	if err := s.c.Send(&cmd); !IsCode(err, ERefused) {
		t.Fatalf("over 15 minutes: %v", err)
	}
}

func TestDecodeVoiceUnsupportedByAnOldLibrary(t *testing.T) {
	old := &Core{lib: &Lib{}} // a library without zf_voice_decode
	if old.lib.fn.voiceDecode != (clib.Func{}) {
		t.Fatal("not an old library")
	}
	if _, _, err := old.DecodeVoice("/x.ogg"); err != ErrUnsupported || !errors.Is(err, errors.ErrUnsupported) {
		t.Fatalf("%v", err)
	}
}

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

func TestZfVoiceLayout(t *testing.T) {
	if unsafe.Sizeof(zfVoice{}) != 96 || unsafe.Offsetof(zfVoice{}.waveform) != 32 {
		t.Fatalf("zf_voice is 96 bytes, waveform at 32; got %d, %d", unsafe.Sizeof(zfVoice{}), unsafe.Offsetof(zfVoice{}.waveform))
	}
}
