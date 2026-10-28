package av_test

import (
	"path/filepath"
	"testing"
	"time"

	"gonativeui/gpu"
	"gonativeui/ui"
	"gonativeui/widget/widgettest"

	"zapfast/abi"
	"zapfast/core/fake"
	"zapfast/logic"
	"zapfast/state"
	ztheme "zapfast/theme"
	"zapfast/ui/conversation"
	"zapfast/ui/conversation/av"
	"zapfast/ui/screen"
	"zapfast/ui/shell"
)

// In the real transcript (the shell, ui/conversation and the reducers on
// the fake core) a voice note sits in its bubble and plays from it.
func TestInTheTranscript(t *testing.T) {
	av.Forget()
	r := &rig{t: t, now: time.Unix(1_792_000_000, 0)}
	restore := av.SetHooks(r.hooks(false))
	t.Cleanup(func() { av.Forget(); restore() })
	s := state.New()
	s.Shell = &opener{}
	s.Now = func() time.Time { return r.now }
	sh, err := shell.New(s, &screen.Set{Conversation: conversation.New(), Logic: logic.New()})
	if err != nil {
		t.Fatal(err)
	}
	f := fake.New(nil)
	a := s.AddAccount("1", nil, f)
	a.Model.Now = s.Now
	r.s, r.f, r.a = s, f, a
	f.Emit(ev(abi.EventTagLink, func(e *abi.Event) { e.Link.Status.Tag = abi.LinkStatusTagConnected }),
		ev(abi.EventTagChats, func(e *abi.Event) {
			e.Chats = abi.EventChats{Batch: 1, Last: true, Chats: []abi.Chat{{ID: []byte(chatID), Name: []byte("Ada"), LastActivity: 20}}}
		}))
	cfg := shell.Config(s)
	r.Rig = widgettest.Open(t, cfg, int(cfg.Width), int(cfg.Height), 1, sh)
	r.Offscreen().UI.SetSystemAppearance(ui.Appearance{Known: true})
	r.Frames(3)
	a.OpenChat(a.Model.Chats.Find([]byte(chatID)))
	r.Frames(2)
	f.Emit(ev(abi.EventTagMessages, func(e *abi.Event) {
		e.Messages = abi.EventMessages{Chat: []byte(chatID), Batch: 1, Last: true, Complete: true,
			Messages: []abi.Message{voice("v1", 1_791_990_000, false, "/v/6.ogg", 6)}}
	}))
	r.Frames(4)
	// the button and the waveform are inside the bubble (a group named by
	// its sender and time), within its bounds
	inBubble := func(role ui.Role, name string) bool {
		tr := r.Tree()
		for i := range tr.Nodes {
			n := &tr.Nodes[i]
			if n.Role != role || string(tr.Text(n.Name)) != name {
				continue
			}
			for p := n.Parent; p >= 0; p = tr.Nodes[p].Parent {
				g := &tr.Nodes[p]
				if g.Role == ui.RoleGroup && len(tr.Text(g.Name)) > 0 {
					b, o := n.Bounds, g.Bounds
					return b[0] >= o[0] && b[1] >= o[1] && b[2] <= o[2] && b[3] <= o[3]
				}
			}
		}
		return false
	}
	if !inBubble(ui.RoleButton, "Play") || !inBubble(ui.RoleSlider, "Voice message") || !inBubble(ui.RoleButton, "Playback speed 1x") {
		t.Errorf("the player is not inside its bubble:\n%s", widgettest.Dump(r.Tree()))
	}
	r.Click(ui.RoleButton, "Play")
	p := r.player(0)
	r.until("playing", func() bool { return p.playing })
	r.must(ui.RoleButton, "Pause")
	p.pos = 2 * time.Second
	r.Frames(2)
	r.shot("transcript")
	if mp := r.sent(abi.CommandTagMarkPlayed); len(mp) != 1 {
		t.Errorf("receipts %+v", mp)
	}
}

// CV-019: exercise the real transcript, not only the kind classifiers.
// AV_SHOT saves each bare item for visual review alongside a bubbled control.
func TestBareMediaInTheTranscript(t *testing.T) {
	for _, kind := range []string{"sticker", "carousel", "round-note", "text"} {
		t.Run(kind, func(t *testing.T) {
			av.Forget()
			t.Cleanup(av.Forget)
			s := state.New()
			s.Shell = &opener{}
			s.Now = func() time.Time { return time.Unix(1_792_000_000, 0) }
			set := &screen.Set{Conversation: conversation.New(), Theme: ztheme.New()}
			t.Cleanup(func() { ztheme.New() })
			sh, err := shell.New(s, set)
			if err != nil {
				t.Fatal(err)
			}
			f := fake.New(nil)
			a := s.AddAccount("1", nil, f)
			a.Settings.AutoDownload = false
			a.Model.Now = s.Now
			f.Emit(ev(abi.EventTagLink, func(e *abi.Event) { e.Link.Status.Tag = abi.LinkStatusTagConnected }),
				ev(abi.EventTagChats, func(e *abi.Event) {
					e.Chats = abi.EventChats{Last: true, Chats: []abi.Chat{{ID: []byte(chatID), Name: []byte("Ada"), LastActivity: 20}}}
				}))
			cfg := shell.Config(s)
			r := &rig{t: t, s: s, a: a, f: f}
			r.Rig = widgettest.Open(t, cfg, 900, 700, 1, sh)
			r.Frames(3)
			a.OpenChat(a.Model.Chats.Find([]byte(chatID)))
			m := text("bare", 1_791_990_000)
			path, err := filepath.Abs("../../../../assets/demo/stickers/astronaut.webp")
			if err != nil {
				t.Fatal(err)
			}
			md := abi.Media{Path: []byte(path), HasPath: true, Width: 512, Height: 512, HasWidth: true, HasHeight: true}
			role, name := ui.RoleLabel, "hello"
			switch kind {
			case "sticker":
				m.Content = abi.Content{Tag: abi.ContentTagSticker, Sticker: abi.ContentSticker{Media: md}}
				role, name = ui.RoleButton, "Sticker"
			case "carousel":
				m.Content = abi.Content{Tag: abi.ContentTagInteractive, Interactive: abi.ContentInteractive{HasCard: true,
					Card: abi.InteractiveCard{Carousel: []abi.CarouselCard{{Body: []byte("First card")}, {Body: []byte("Second card")}}}}}
				name = "First card"
			case "round-note":
				m = video("bare", m.Timestamp, "", true, false)
				r.poster(&m)
				role, name = ui.RoleButton, "Video message"
			}
			r.load(m)
			r.Frames(20)
			tr := r.Tree()
			n := r.must(role, name)
			var bounds [4]float32
			for p := n.Parent; p >= 0; p = tr.Nodes[p].Parent {
				g := tr.Nodes[p]
				if g.Role == ui.RoleGroup && len(tr.Text(g.Name)) > 0 {
					bounds = g.Bounds
					break
				}
			}
			if bounds == [4]float32{} {
				t.Fatal("no message group")
			}
			filled := false
			for _, q := range r.Quads() {
				if q.Mode != gpu.ModeShape || q.Soft > 0 || q.Color[0]&0xFF == 0 {
					continue
				}
				d := float32(0)
				for i := range 4 {
					d = max(d, max(q.Rect[i]-bounds[i], bounds[i]-q.Rect[i]))
				}
				filled = filled || d < 1
			}
			if filled != (kind == "text") {
				t.Errorf("%s background filled = %v", kind, filled)
			}
			r.shot("bare-" + kind)
		})
	}
}
