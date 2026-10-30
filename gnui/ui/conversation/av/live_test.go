package av_test

import (
	"os"
	"testing"
	"time"

	"gonativeui/app"
	"gonativeui/media"
	"gonativeui/ui"
	"gonativeui/widget/widgettest"

	"zapfast/abi"
	"zapfast/core"
	"zapfast/gocore/demo"
	"zapfast/logic"
	"zapfast/state"
	"zapfast/ui/conversation/av"
)

// Live, with AV_LIVE=1 on a Mac with sound (the demo core and the real
// media.Player: it plays through the speakers): a demo voice note plays at
// 2x, its time moves, its end is noticed, and the run carries on into the
// next one, which the lock then stops.
func TestLiveDemoVoiceNote(t *testing.T) {
	if os.Getenv("AV_LIVE") == "" {
		t.Skip("AV_LIVE=1 plays sound")
	}
	c, err := demo.Open(core.Config{Wake: func() {}})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { c.Close(5 * time.Second) })
	av.Forget()
	media.Warm()
	r := &rig{t: t, sh: &opener{}}
	restore := av.SetHooks(av.Hooks{Player: func(pcm []byte, f media.Format) (av.Player, error) {
		t0 := time.Now()
		p, err := media.NewPlayer(pcm, f)
		t.Logf("NewPlayer of %d bytes: %v", len(pcm), time.Since(t0))
		if err != nil {
			return nil, err
		}
		return p, nil
	}})
	t.Cleanup(func() { av.Forget(); restore() })
	s := state.New()
	s.Shell, s.Demo = r.sh, true
	a := s.AddAccount("1", nil, c)
	r.s, r.a = s, a
	for deadline := time.Now().Add(10 * time.Second); !a.Model.Chats.Loaded(); {
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
	a.OpenChat(a.Model.Chats.Find([]byte("393331234567@s.whatsapp.net")))
	var first string
	r.until("a demo voice note with its file", func() bool {
		v := a.Conversation()
		for i := 0; v != nil && i < v.Len(); i++ {
			if md, ok := v.At(i).Media(); ok && v.At(i).Kind() == abi.ContentTagAudio && md.Voice && len(md.Path) > 0 {
				first = string(v.At(i).ID())
				t.Logf("first note %s, %d s, %s", first, md.Seconds, md.Path)
				return true
			}
		}
		return false
	})
	r.s.Settings.VoiceSpeed = 2
	r.Frames(2)
	start := time.Now()
	r.Play(widgettest.Click(ui.RoleButton, "Play").Nth(0))
	t.Logf("clicked after %v", time.Since(start))
	r.until("it plays", func() bool { _, ok := r.Find(ui.RoleButton, "Pause"); return ok })
	t.Logf("playing after %v", time.Since(start))
	time.Sleep(700 * time.Millisecond)
	r.Frames(1)
	sl := r.must(ui.RoleSlider, "Voice message")
	t.Logf("at %.2f s of %.2f s after 0.7 s at 2x", sl.Range[2], sl.Range[1])
	if sl.Range[2] < 1.0 || sl.Range[2] > 1.8 {
		t.Errorf("at %.2f s after 0.7 s at 2x", sl.Range[2])
	}
	// the end, then the run's next note (if the demo has an unheard one
	// after it)
	ended := false
	for i := 0; i < 2000 && !ended; i++ {
		r.Frames(1)
		time.Sleep(10 * time.Millisecond)
		_, playing := r.Find(ui.RoleButton, "Pause")
		ended = a.Played.Len() > 1 || (!playing && time.Since(start) > 3*time.Second)
	}
	if !ended {
		t.Fatal("the end was never noticed")
	}
	t.Logf("after %v: %d notes played, the run's next playing: %v", time.Since(start), a.Played.Len(), a.Played.Len() > 1)
	s.Locked()
	r.Frames(2)
	if _, ok := r.Find(ui.RoleButton, "Pause"); ok {
		t.Error("the lock did not stop it")
	}
}
