package composer

import (
	"encoding/binary"
	"math"
	"strings"
	"testing"
	"time"

	"gonativeui/app"
	"gonativeui/app/perm"
	"gonativeui/media"
	"gonativeui/platform"
	"gonativeui/ui"
	"gonativeui/widget/widgettest"

	"zapfast/abi"
	"zapfast/core"
	"zapfast/state"
)

// fakeMic is the microphone of the tests: the user's answer, the recorder's
// state and what it recorded are the test's.
type fakeMic struct {
	access    perm.State
	accessErr error
	askErr    error
	asks      int
	settings  int

	state   media.RecorderState
	el      time.Duration
	level   float32
	slow    bool // Stop leaves it Finishing until ready
	pcm     []byte
	startEr error
	starts  int
	cancels int
}

func (f *fakeMic) Access() (perm.State, error) { return f.access, f.accessErr }
func (f *fakeMic) Ask() (perm.Ticket, error) {
	f.asks++
	return perm.Ticket(100 + f.asks), f.askErr
}
func (f *fakeMic) ID() uint64      { return 42 }
func (f *fakeMic) Settings() error { f.settings++; return nil }

func (f *fakeMic) Start() error {
	if f.startEr != nil {
		return f.startEr
	}
	f.starts++
	f.state, f.el = media.Recording, 0
	return nil
}

func (f *fakeMic) Stop() error {
	if f.state != media.Recording {
		return media.ErrState
	}
	f.state = media.Finishing
	if f.pcm == nil {
		f.pcm = pcm16(f.el, 0.25)
	}
	if !f.slow {
		f.state = media.Ready
	}
	return nil
}

func (f *fakeMic) Cancel()                    { f.cancels++; f.state, f.pcm = media.Idle, nil }
func (f *fakeMic) State() media.RecorderState { return f.state }
func (f *fakeMic) Elapsed() time.Duration {
	if f.state != media.Recording {
		return 0
	}
	return f.el
}
func (f *fakeMic) Level() float32 { return f.level }

func (f *fakeMic) Take() ([]byte, media.Format, error) {
	if f.state != media.Ready {
		return nil, media.Format{}, media.ErrState
	}
	p := f.pcm
	f.state, f.pcm = media.Idle, nil
	return p, media.Format{SampleRate: 48000, Channels: 1, Sample: media.Int16}, nil
}

// answer is the system's answer to the fake's last request, as the app's
// handler hands it to the screens (State.AppEvent); it reports whether one
// took it.
func (r *rig) answer(granted bool, st perm.State) bool {
	r.t.Helper()
	ev := &app.Event{Kind: app.EvService, Source: platform.SourcePerm, ID: uint64(100 + r.mic.asks), A: int64(perm.Microphone), B: int64(st)}
	if granted {
		ev.A |= 1 << 8
	}
	if res, ok := perm.ResultOf(ev); !ok || res.Granted != granted || res.State != st || res.Kind != perm.Microphone {
		r.t.Fatalf("the fake answer decodes as %+v", res)
	}
	took := r.s.AppEvent(ev)
	r.Frames(2)
	return took
}

// recorded is the recorder's Recorded event for the recorder id.
func (r *rig) recorded(id uint64) bool {
	ev := &app.Event{Kind: app.EvService, Source: platform.SourceMedia, A: int64(media.Recorded), B: int64(id)}
	if me, ok := media.EventOf(ev); !ok || me.Kind != media.Recorded || me.ID != id {
		r.t.Fatalf("the fake event decodes as %+v", me)
	}
	return r.s.AppEvent(ev)
}

// ready ends a slow Stop.
func (f *fakeMic) ready() { f.state = media.Ready }

// pcm16 is d of mono 48 kHz Int16 at the constant level v.
func pcm16(d time.Duration, v float64) []byte {
	n := int(d.Seconds() * 48000)
	b := make([]byte, 2*n)
	for i := range n {
		binary.LittleEndian.PutUint16(b[2*i:], uint16(int16(v*32767)))
	}
	return b
}

const micName = "Record a voice message"

// waitVoice runs frames until a SendVoice is sent (the conversion runs on a
// goroutine).
func (r *rig) waitVoice() abi.Command {
	r.t.Helper()
	for range 200 {
		if c, ok := r.last(abi.CommandTagSendVoice); ok {
			return c
		}
		time.Sleep(5 * time.Millisecond)
		r.Frames(1)
	}
	r.t.Fatalf("no voice message sent: %v", r.f.SentTags())
	return abi.Command{}
}

// blob is the fake core's copy of a blob, false once freed.
func (r *rig) blob(ref abi.BlobRef) ([]byte, bool) {
	b := make([]byte, ref.Len)
	if _, err := r.f.ReadBlob(ref.ID, 0, b); err != nil {
		return nil, false
	}
	return b, true
}

func (r *rig) toasted(s string) bool {
	for _, t := range r.s.Toasts {
		if strings.Contains(string(t.Text), s) {
			return true
		}
	}
	return false
}

// The audio stack (media.Warm) is started once, on the first frame with a
// chat open: the composer is built on every open chat, so no voice note can
// show before it (ui/conversation/av does not warm it too).
func TestAudioIsWarmedOnce(t *testing.T) {
	r := newRig(t, nil, nil)
	warms := 0
	r.scr.voice.warm = func() { warms++ }
	r.Frames(3)
	if warms != 0 {
		t.Errorf("warmed %d times with no chat open", warms)
	}
	r.open(ada)
	r.open(grace)
	r.Frames(5)
	if warms != 1 {
		t.Errorf("warmed %d times", warms)
	}
}

// CP-023: the microphone records; the bar shows Discard, the time, the
// waveform and Send; Send sends the recording, converted for the core and
// quoting the reply shown, and the composer is back.
func TestRecordingSendsAVoiceMessage(t *testing.T) {
	r := newRig(t, nil, nil)
	r.open(ada)
	r.messages(ada, textMsg("m1", ada, false, 100, "Hi"))
	r.s.Panes.Reply.Add([]byte("m1"))
	r.Frames(2)
	r.Play(widgettest.Click(ui.RoleButton, micName), widgettest.Idle(2))
	if r.mic.starts != 1 || !r.scr.voice.recording() {
		t.Fatal("the microphone did not start recording")
	}
	if r.has(ui.RoleTextField, "Message") {
		t.Error("the field stays beside the recording bar")
	}
	for _, name := range []string{"Discard", "Send"} {
		if !r.has(ui.RoleButton, name) {
			t.Errorf("no %s on the bar", name)
		}
	}
	if !r.hasText("Replying to") {
		t.Error("the reply strip went with the field")
	}
	r.mic.level = 0.2
	r.mic.el = 3*time.Second + 400*time.Millisecond
	r.Frames(2)
	if !r.hasText("0:03") {
		t.Error("the bar's time is not 0:03")
	}
	if r.scr.voice.n != 68 {
		t.Errorf("%d levels for 3.4 s", r.scr.voice.n)
	}
	if !r.s.InUse() {
		t.Error("recording is not using the app")
	}
	// CP-014: nothing is staged while recording.
	r.before(func(c *ui.Context) {
		r.scr.dropped(r.scr.env, ui.DropData{Kind: ui.DropFiles, Data: []byte("/tmp/a.txt\x00"), Items: 1})
	})
	if len(r.s.Staged) != 0 {
		t.Error("a drop staged while recording")
	}
	r.Play(widgettest.Click(ui.RoleButton, "Send"), widgettest.Idle(2))
	c := r.waitVoice()
	if !eq(c.SendVoice.Chat, ada) || !c.SendVoice.HasQuoting || !eq(c.SendVoice.Quoting.ID, "m1") {
		t.Errorf("sent %+v", c.SendVoice)
	}
	samples, ok := r.blob(c.SendVoice.Samples)
	if !ok || len(samples) != 4*int(3.4*core.VoiceRate) {
		t.Fatalf("the blob holds %d bytes", len(samples))
	}
	if v := math.Float32frombits(binary.LittleEndian.Uint32(samples)); math.Abs(float64(v)-0.25) > 0.001 {
		t.Errorf("the first sample is %v, not 0.25", v)
	}
	if r.scr.voice.recording() || !r.has(ui.RoleTextField, "Message") || r.s.Panes.Reply.Len() != 0 {
		t.Error("the composer did not come back without the reply")
	}
	if r.s.InUse() {
		t.Error("still in use after the send")
	}
}

// Enter sends a recording, and a recording still being read goes once it
// is.
func TestEnterSendsTheRecording(t *testing.T) {
	r := newRig(t, nil, nil)
	r.open(ada)
	r.mic.slow = true
	r.Play(widgettest.Click(ui.RoleButton, micName), widgettest.Idle(2))
	r.mic.el = 2 * time.Second
	r.Frames(2)
	r.key(ui.KeyEnter, 0)
	if r.scr.voice.recording() {
		t.Fatal("Enter did not stop the recording")
	}
	r.Frames(3)
	if _, ok := r.last(abi.CommandTagSendVoice); ok {
		t.Fatal("sent before the recording was read")
	}
	// The window waits for the Recorded event, asking for no frames once
	// the bar's going has settled.
	for i := 0; i < 60 && r.Context().Animating(); i++ {
		r.Frame()
	}
	if r.Context().Animating() {
		t.Error("the window polls the recorder while it is read")
	}
	if r.recorded(7) {
		t.Error("another recorder's event was taken")
	}
	r.mic.ready()
	if !r.recorded(42) {
		t.Error("the recorder's Recorded event was not taken")
	}
	if c := r.waitVoice(); c.SendVoice.HasQuoting {
		t.Error("quoted with no reply")
	}
}

// A recording under half a second is dropped unsent (zf:src/app.rs:9091).
func TestShortRecordingsAreDropped(t *testing.T) {
	r := newRig(t, nil, nil)
	r.open(ada)
	r.Play(widgettest.Click(ui.RoleButton, micName), widgettest.Idle(2))
	r.mic.el = 400 * time.Millisecond
	r.Frames(2)
	r.Play(widgettest.Click(ui.RoleButton, "Send"), widgettest.Idle(4))
	time.Sleep(20 * time.Millisecond)
	r.Frames(2)
	if _, ok := r.last(abi.CommandTagSendVoice); ok || len(r.s.Toasts) != 0 {
		t.Errorf("a 0.4 s recording went (%v) or toasted %v", r.f.SentTags(), r.s.Toasts)
	}
	if r.scr.voice.recording() || !r.has(ui.RoleButton, micName) {
		t.Error("the composer did not come back")
	}
}

// CP-024: Discard and Escape drop the recording; so do the lock, and
// another chat.
func TestRecordingsAreDiscarded(t *testing.T) {
	r := newRig(t, nil, nil)
	r.open(ada)
	start := func() {
		t.Helper()
		r.Play(widgettest.Click(ui.RoleButton, micName), widgettest.Idle(2))
		r.mic.el = 2 * time.Second
		r.Frames(2)
		if !r.scr.voice.recording() {
			t.Fatal("not recording")
		}
	}
	start()
	r.Play(widgettest.Click(ui.RoleButton, "Discard"), widgettest.Idle(2))
	if r.scr.voice.recording() || r.mic.cancels != 1 {
		t.Error("Discard kept the recording")
	}
	start()
	r.key(ui.KeyEscape, 0)
	if r.scr.voice.recording() || r.mic.cancels != 2 || r.a.Open == 0 {
		t.Error("Escape kept the recording, or closed the chat")
	}
	start()
	r.s.Locked()
	r.Frames(2)
	if r.scr.voice.recording() || r.mic.cancels != 3 || r.s.InUse() {
		t.Error("the lock kept the recording")
	}
	start()
	r.open(grace)
	if r.scr.voice.recording() || r.mic.cancels != 4 {
		t.Error("the recording followed into another chat")
	}
	if _, ok := r.last(abi.CommandTagSendVoice); ok {
		t.Error("a discarded recording went")
	}
}

// The microphone's permission: asked the first time and started once
// granted; denied, it says so and opens System Settings; a build that
// cannot ask, or a Mac with no microphone, says that.
func TestMicrophonePermission(t *testing.T) {
	r := newRig(t, nil, nil)
	r.open(ada)
	r.mic.access = perm.NotDetermined
	r.Play(widgettest.Click(ui.RoleButton, micName), widgettest.Idle(2))
	if r.mic.asks != 1 || r.mic.starts != 0 {
		t.Fatalf("asked %d, started %d", r.mic.asks, r.mic.starts)
	}
	r.Frames(3)
	if r.scr.voice.recording() {
		t.Fatal("recording before the answer")
	}
	r.mic.access = perm.Authorized // the status alone starts nothing: the answer does
	r.Frames(3)
	if r.scr.voice.recording() {
		t.Fatal("recording before the answer")
	}
	if !r.answer(true, perm.Authorized) || !r.scr.voice.recording() || r.mic.starts != 1 {
		t.Fatal("the granted microphone did not start")
	}
	if r.answer(true, perm.Authorized) {
		t.Error("an answer nobody waits for was taken")
	}
	r.Play(widgettest.Click(ui.RoleButton, "Discard"), widgettest.Idle(2))

	// Asked, then refused.
	r.mic.access = perm.NotDetermined
	r.Play(widgettest.Click(ui.RoleButton, micName), widgettest.Idle(2))
	r.mic.access = perm.Denied
	r.answer(false, perm.Denied)
	if r.scr.voice.recording() || !r.toasted("microphone access is off") || r.mic.settings != 1 {
		t.Errorf("a refusal: recording %v, toasts %v, settings %d", r.scr.voice.recording(), r.s.Toasts, r.mic.settings)
	}

	// Denied before: only System Settings changes it.
	r.s.Toasts = r.s.Toasts[:0]
	r.Play(widgettest.Click(ui.RoleButton, micName), widgettest.Idle(2))
	if r.mic.asks != 2 || r.mic.settings != 2 || !r.toasted("Could not record: microphone access is off") {
		t.Errorf("denied: asks %d, settings %d, toasts %v", r.mic.asks, r.mic.settings, r.s.Toasts)
	}

	// Granted while another chat is open: nothing starts there.
	r.mic.access = perm.NotDetermined
	r.Play(widgettest.Click(ui.RoleButton, micName), widgettest.Idle(2))
	r.open(grace)
	r.mic.access = perm.Authorized
	r.answer(true, perm.Authorized)
	if r.scr.voice.recording() {
		t.Error("the answer started recording in another chat")
	}

	// An unbundled build cannot ask.
	r.s.Toasts = r.s.Toasts[:0]
	r.mic.access, r.mic.askErr = perm.NotDetermined, perm.ErrNoUsageDescription
	r.Play(widgettest.Click(ui.RoleButton, micName), widgettest.Idle(2))
	if r.scr.voice.recording() || !r.toasted("app bundle") {
		t.Errorf("no usage description: toasts %v", r.s.Toasts)
	}
}

// Steady frames while recording allocate nothing: the time, the light and
// the waveform.
func TestRecordingAllocatesNothing(t *testing.T) {
	r := newRig(t, nil, nil)
	r.open(ada)
	r.Play(widgettest.Click(ui.RoleButton, micName), widgettest.Idle(2))
	r.mic.level = 0.1
	for i := range 40 {
		r.mic.el = time.Duration(i) * 100 * time.Millisecond
		r.Frames(1)
	}
	if n := testing.AllocsPerRun(30, func() {
		r.mic.el += 40 * time.Millisecond
		r.Frame()
	}); n != 0 {
		t.Errorf("recording: %v allocations per frame", n)
	}
}

// The recording stops at the longest the core sends and waits for Send or
// Discard.
func TestRecordingStopsAtTheLongest(t *testing.T) {
	r := newRig(t, nil, nil)
	r.open(ada)
	r.Play(widgettest.Click(ui.RoleButton, micName), widgettest.Idle(2))
	r.mic.el = longestRecording
	r.mic.pcm = pcm16(time.Second, 0.1)
	r.Frames(2)
	if r.mic.state != media.Ready || !r.scr.voice.recording() {
		t.Fatalf("state %v, bar %v", r.mic.state, r.scr.voice.recording())
	}
	if !r.hasText("15:00") {
		t.Error("the stopped bar does not read 15:00")
	}
	if _, ok := r.last(abi.CommandTagSendVoice); ok {
		t.Fatal("sent on its own")
	}
	r.Play(widgettest.Click(ui.RoleButton, "Send"), widgettest.Idle(2))
	r.waitVoice()
}

// CP-024: a voice message the core refused waits above the field with its
// length; Send tries it again, quoting the reply shown; Discard frees it.
func TestRefusedVoiceMessage(t *testing.T) {
	r := newRig(t, nil, nil)
	r.open(ada)
	refuse := func() abi.BlobRef {
		t.Helper()
		ref, err := r.f.PutBlob(make([]byte, 4*12*core.VoiceRate))
		if err != nil {
			t.Fatal(err)
		}
		r.f.Emit(ev(abi.EventTagSendRefused, func(e *abi.Event) {
			e.SendRefused.Chat = []byte(ada)
			e.SendRefused.Unsent.Tag = abi.UnsentTagVoice
			e.SendRefused.Unsent.Voice.Samples = ref
		}))
		r.Frames(3)
		return ref
	}
	ref := refuse()
	if !r.hasText("Voice message (0:12) not sent") {
		t.Fatal("no unsent strip")
	}
	if r.has(ui.RoleButton, micName) || !r.has(ui.RoleButton, "Send") {
		t.Error("the microphone did not give way to Send")
	}
	r.open(grace)
	if r.hasText("not sent") {
		t.Error("the refused message shows in another chat")
	}
	r.open(ada)
	r.messages(ada, textMsg("m1", ada, false, 100, "Hi"))
	r.s.Panes.Reply.Add([]byte("m1"))
	r.Frames(2)
	n := len(r.sent(abi.CommandTagSendVoice))
	r.Play(widgettest.Click(ui.RoleButton, "Send"), widgettest.Idle(2))
	s := r.sent(abi.CommandTagSendVoice)
	if len(s) != n+1 || s[n].SendVoice.Samples.ID != ref.ID || !eq(s[n].SendVoice.Quoting.ID, "m1") {
		t.Fatalf("the retry sent %v", r.f.SentTags())
	}
	if r.hasText("not sent") || r.s.Panes.Reply.Len() != 0 {
		t.Error("the strip or the reply stayed after the retry")
	}

	// The send key in the empty composer tries it again too (SendKey: the
	// hosted editor's in the live test).
	ref = refuse()
	if !r.scr.focused {
		t.Fatal("the refusal did not give the composer the keyboard")
	}
	r.key(ui.KeyEnter, 0)
	if s := r.sent(abi.CommandTagSendVoice); len(s) != n+2 || s[n+1].SendVoice.Samples.ID != ref.ID {
		t.Fatalf("Enter sent %v", r.f.SentTags())
	}

	ref = refuse()
	r.Play(widgettest.Click(ui.RoleButton, "Discard voice message"), widgettest.Idle(2))
	if _, ok := r.blob(ref); ok || r.hasText("not sent") {
		t.Error("Discard kept the samples")
	}
	if len(r.sent(abi.CommandTagSendVoice)) != n+2 {
		t.Error("Discard sent it")
	}

	// A second refusal replaces the first, whose samples go.
	first := refuse()
	refuse()
	if _, ok := r.blob(first); ok {
		t.Error("the replaced refusal's samples stay")
	}
}

// DM-007: the demo records a speech-like tone and never asks for the
// microphone.
func TestDemoRecordsATone(t *testing.T) {
	now := time.Unix(1_800_000_000, 0)
	r := newRig(t, nil, func(s *state.State) { s.Demo = true })
	r.s.Now = func() time.Time { return now }
	r.open(ada)
	r.Play(widgettest.Click(ui.RoleButton, micName), widgettest.Idle(2))
	if !r.scr.voice.recording() || r.mic.asks != 0 || r.mic.starts != 0 {
		t.Fatal("the demo did not record on its own")
	}
	now = now.Add(2 * time.Second)
	r.Frames(2)
	if !r.hasText("0:02") {
		t.Error("the demo's time is not 0:02")
	}
	r.Play(widgettest.Click(ui.RoleButton, "Send"), widgettest.Idle(2))
	c := r.waitVoice()
	samples, _ := r.blob(c.SendVoice.Samples)
	if len(samples) != 4*2*core.VoiceRate {
		t.Fatalf("%d bytes for 2 s", len(samples))
	}
	var peak float32
	for i := 0; i < len(samples); i += 4 {
		peak = max(peak, float32(math.Abs(float64(math.Float32frombits(binary.LittleEndian.Uint32(samples[i:]))))))
	}
	if peak < 0.05 || peak > 0.36 {
		t.Errorf("the tone peaks at %v", peak)
	}
}

// CP-024: with ⌘Enter sending, plain Enter in the empty composer leaves a
// refused voice message waiting, and ⌘Enter tries it again.
func TestTheSendKeyRetriesARefusedVoiceMessage(t *testing.T) {
	r := newRig(t, nil, func(s *state.State) { s.Settings.EnterSends = false })
	r.open(ada)
	ref, err := r.f.PutBlob(make([]byte, 4*3*core.VoiceRate))
	if err != nil {
		t.Fatal(err)
	}
	r.f.Emit(ev(abi.EventTagSendRefused, func(e *abi.Event) {
		e.SendRefused.Chat = []byte(ada)
		e.SendRefused.Unsent.Tag = abi.UnsentTagVoice
		e.SendRefused.Unsent.Voice.Samples = ref
	}))
	r.Frames(3)
	if !r.scr.focused || !r.hasText("Voice message (0:03) not sent") {
		t.Fatal("no unsent strip, or the composer lacks the keyboard")
	}
	r.key(ui.KeyEnter, 0)
	if _, ok := r.last(abi.CommandTagSendVoice); ok {
		t.Fatal("Enter retried it with ⌘Enter sending")
	}
	r.key(ui.KeyEnter, ui.PrimaryMod())
	if c, ok := r.last(abi.CommandTagSendVoice); !ok || c.SendVoice.Samples.ID != ref.ID {
		t.Fatalf("⌘Enter sent %v", r.f.SentTags())
	}
	if r.hasText("not sent") {
		t.Error("the strip stayed after the retry")
	}
}
