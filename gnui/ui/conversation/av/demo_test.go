package av_test

import (
	"testing"
	"time"

	"gonativeui/app"
	"gonativeui/media"
	"gonativeui/ui"
	"gonativeui/widget/widgettest"

	"zapfast/abi"
	"zapfast/core"
	"zapfast/logic"
	"zapfast/state"
	"zapfast/ui/conversation/av"
)

// The demo core end to end (the real library: $ZAPFAST_CORE or a cargo
// build; skipped without one, and with -short): a demo voice note's Play
// decodes its Ogg/Opus file through zf_voice_decode on a worker and plays
// the PCM the core gives, mono float32 at 48 kHz. The Player is the rig's,
// so nothing sounds.
func TestTheDemoCoresVoiceNotesPlay(t *testing.T) {
	if testing.Short() {
		t.Skip("the demo core takes a second")
	}
	if _, err := core.FindLibrary(); err != nil {
		t.Skipf("no core library: %v", err)
	}
	c, err := core.Open(core.Config{Demo: true, Wake: func() {}})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { c.Close(5 * time.Second) })
	av.Forget()
	r := &rig{t: t, sh: &opener{}, now: time.Unix(1_792_000_000, 0)}
	restore := av.SetHooks(av.Hooks{Player: r.newPlayer, OpenVideo: r.openVideo})
	t.Cleanup(func() { av.Forget(); restore() })
	s := state.New()
	s.Shell, s.Demo = r.sh, true
	a := s.AddAccount("1", nil, c)
	r.s, r.a = s, a
	deadline := time.Now().Add(10 * time.Second)
	for !a.Model.Chats.Loaded() {
		if time.Now().After(deadline) {
			t.Fatal("no chats within 10 s")
		}
		s.Pump(5 * time.Millisecond)
		time.Sleep(5 * time.Millisecond)
	}
	l := logic.New()
	l.Init(s)
	r.p = &page{s: s, l: l, maxW: 330}
	cfg := app.Config{Title: "av", Width: 420, Height: 1400, Clear: [4]float32{1, 1, 1, 1}}
	r.Rig = widgettest.Open(t, cfg, 420, 1400, 1, r.p)
	r.Frames(2)
	const ada = "393331234567@s.whatsapp.net" // a chat of the demo's with voice notes
	a.OpenChat(a.Model.Chats.Find([]byte(ada)))
	r.until("a demo voice note with its file", func() bool {
		v := a.Conversation()
		if v == nil {
			return false
		}
		for i := 0; i < v.Len(); i++ {
			if md, ok := v.At(i).Media(); ok && v.At(i).Kind() == abi.ContentTagAudio && md.Voice && len(md.Path) > 0 {
				return true
			}
		}
		return false
	})
	r.Frames(2)
	r.Play(widgettest.Click(ui.RoleButton, "Play").Nth(0))
	p := r.player(0)
	r.until("playing", func() bool { return p.playing })
	if p.format != (media.Format{SampleRate: core.VoiceRate, Channels: 1, Sample: media.Float32}) || p.dur < 500*time.Millisecond {
		t.Errorf("a player of %v in %+v", p.dur, p.format)
	}
	nonzero := false
	for _, b := range p.pcm {
		if b != 0 {
			nonzero = true
			break
		}
	}
	if !nonzero {
		t.Error("the decoded note is silent")
	}
	if len(s.Toasts) > 0 {
		t.Errorf("toast %q", s.Toasts[0].Text)
	}
}
