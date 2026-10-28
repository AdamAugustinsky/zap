package calls

import (
	"testing"
	"time"

	"gonativeui/ui"
	"gonativeui/widget/widgettest"

	"zapfast/abi"
	"zapfast/core"
	"zapfast/state"
	"zapfast/ui/screen"
)

// The demo core end to end (the real library: $ZAPFAST_CORE or a cargo
// build; skipped without one, and with -short): its call log and devices
// reach the hub; a video call placed here comes up under a number from the
// core's counter, with frames on our stream and theirs, which the call window
// takes and gives back; a group call it rings comes after it, and once
// answered streams its members.
func TestTheDemoCoresCalls(t *testing.T) {
	if testing.Short() {
		t.Skip("the demo core's calls take seconds")
	}
	if _, err := core.FindLibrary(); err != nil {
		t.Skipf("no core library: %v", err)
	}
	c, err := core.Open(core.Config{Demo: true, Wake: func() {}})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { c.Close(5 * time.Second) })
	s := state.New()
	s.Shell, s.Demo = noShell{t}, true
	a := s.AddAccount("1", nil, c)
	h := hubOf(s)
	wait := func(what string, ok func() bool) {
		t.Helper()
		deadline := time.Now().Add(10 * time.Second)
		for !ok() {
			if time.Now().After(deadline) {
				t.Fatalf("no %s within 10 s", what)
			}
			s.Pump(5 * time.Millisecond)
			time.Sleep(5 * time.Millisecond)
		}
	}
	// frame waits for a frame on stream and gives it back.
	frame := func(stream uint32) {
		t.Helper()
		wait("frame", func() bool {
			f, ok := h.frames().Frame(stream)
			if ok {
				h.frames().ReleaseFrame(stream)
			}
			return ok && f.W > 0 && f.Format == core.FrameI420
		})
	}
	// window builds a few frames of the call window over s.
	window := func() *widgettest.Rig {
		t.Helper()
		win := NewWindow()
		cfg := win.Config(s)
		r := widgettest.Open(t, cfg, int(cfg.Width), int(cfg.Height), 1, screen.NewWindow(s, state.WinCall, win, nil, nil))
		r.Offscreen().UI.SetSystemAppearance(ui.Appearance{Known: true})
		r.Frames(3)
		return r
	}
	wait("chats", func() bool { return a.Model.Chats.Loaded() })
	var direct, grp []byte
	for _, ref := range a.Model.Chats.Order() {
		ch := a.Model.Chats.At(ref)
		switch {
		case direct == nil && ch.Kind() == abi.ChatKindDirect:
			direct = append([]byte(nil), ch.ID()...)
		case grp == nil && ch.Kind() == abi.ChatKindGroup:
			grp = append([]byte(nil), ch.ID()...)
		}
	}
	if direct == nil || grp == nil {
		t.Fatalf("the demo has no direct chat (%q) or group (%q)", direct, grp)
	}

	h.loadLog(a)
	wait("call log", func() bool { l := &h.acct(a).log; return l.loaded && len(l.e) == 5 })

	h.place(a, direct, true)
	placed := h.call.u.Generation
	wait("live call", func() bool { return h.call.u.Phase == abi.CallPhaseActive })
	if h.call.u.Generation != placed || !h.liveHere(a, direct) {
		t.Errorf("the call up is generation %d, placed %d", h.call.u.Generation, placed)
	}
	wait("devices", func() bool { return len(h.devs.list.Cameras) == 2 })
	frame(core.StreamLocal)
	frame(core.StreamRemote)
	r := window()
	frame(core.StreamLocal) // the window gave both back
	frame(core.StreamRemote)
	// Devices lists the demo's devices, the cameras too on a video call.
	r.Play(widgettest.Click(ui.RoleButton, "Devices"), widgettest.Idle(2))
	shot(t, r, "demo-devices")
	has(t, r, ui.RoleComboBox, "Camera")
	has(t, r, ui.RoleComboBox, "Microphone")
	h.hangup()

	cmd := a.Cmd(abi.CommandTagDemoIncoming)
	core.DemoCall(cmd, string(grp), "441632960123@s.whatsapp.net", true, 0)
	a.Send(cmd)
	wait("group call ringing", func() bool { return h.call.on && h.call.u.Phase == abi.CallPhaseIncoming && h.call.u.HasGroup })
	if h.call.u.Generation <= placed {
		t.Errorf("the core's call %d is not after ours, %d", h.call.u.Generation, placed)
	}
	h.answer()
	wait("group call up", func() bool { return h.call.u.Phase == abi.CallPhaseActive })
	frame(core.StreamMember)
	shot(t, window(), "demo-group")
	frame(core.StreamMember)
	h.hangup()
}
