package demo_test

import (
	"encoding/binary"
	"math"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"zapfast/abi"
	"zapfast/core"
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

func readAll(t *testing.T, c core.Conn, ref abi.BlobRef) []byte {
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
	if notes.bars["DEMOVOICE0"] != core.VoiceBars {
		t.Fatalf("%d waveform bars", notes.bars["DEMOVOICE0"])
	}

	// In parallel, as worker goroutines would.
	var wg sync.WaitGroup
	refs := make([]abi.BlobRef, 4)
	formats := make([]core.VoiceFormat, 4)
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
		if f.Rate != core.VoiceRate || f.Channels != 1 || refs[i].Len != uint64(f.Frames)*4 {
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
	if _, err := s.c.ReadBlob(refs[0].ID, 0, pcm[:4]); !core.IsCode(err, core.ENotFound) {
		t.Fatalf("a freed note's blob: %v", err)
	}

	for _, refused := range []struct {
		path string
		code int32
	}{
		{"/etc/hosts", core.EArg},
		{"relative/note.ogg", core.EArg},
		{"", core.EArg},
		{filepath.Join(filepath.Dir(path), "gone.ogg"), core.ENotFound},
	} {
		if _, _, err := s.c.DecodeVoice(refused.path); !core.IsCode(err, refused.code) {
			t.Errorf("%q: %v, want %s", refused.path, err, core.CodeName(refused.code))
		}
	}
	if err := s.c.Close(5e9); err != nil {
		t.Fatal(err)
	}
	if _, _, err := s.c.DecodeVoice(path); err != core.ErrClosed {
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
	samples := core.AppendVoicePCM16(nil, rec, 24000, 2)
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

	cmd.SendVoice.Samples = abi.BlobRef{ID: 1 << 40, Len: uint64(core.VoiceLongestSend.Seconds()*core.VoiceRate)*4 + 4}
	if err := s.c.Send(&cmd); !core.IsCode(err, core.ERefused) {
		t.Fatalf("over 15 minutes: %v", err)
	}
}
