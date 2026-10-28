package av_test

import (
	"encoding/binary"
	"io/fs"
	"math"
	"testing"
	"time"

	"gonativeui/app"
	"gonativeui/media"
	"gonativeui/platform"
	"gonativeui/ui"
	"gonativeui/widget/widgettest"

	"zapfast/abi"
	"zapfast/core"
	"zapfast/core/fake"
	"zapfast/ui/conversation/av"
)

// A voice note shows play, its waveform, its length and the speed chip;
// Play decodes it on a worker and plays it, the time follows the position,
// Pause holds it and Play resumes it without decoding again. The first play
// sends the played receipt, once.
func TestVoiceNotePlaysPausesAndResumes(t *testing.T) {
	r := newRig(t, voice("v1", 10, false, "/v/7.ogg", 7))
	r.must(ui.RoleSlider, "Voice message")
	r.must(ui.RoleLabel, "0:07")
	r.must(ui.RoleButton, "Playback speed 1x")
	r.Click(ui.RoleButton, "Play")
	p := r.player(0)
	r.until("playing", func() bool { return p.playing })
	if got := r.decodes(); len(got) != 1 || got[0] != "/v/7.ogg" {
		t.Fatalf("decoded %v", got)
	}
	if p.dur != 7*time.Second || p.rate != 1 {
		t.Errorf("player of %v at %v", p.dur, p.rate)
	}
	if mp := r.sent(abi.CommandTagMarkPlayed); len(mp) != 1 || string(mp[0].MarkPlayed.Message) != "v1" ||
		string(mp[0].MarkPlayed.Sender) != chatID || !mp[0].MarkPlayed.Receipts {
		t.Fatalf("played receipts %+v", mp)
	}
	if r.s.Voice.Chat != r.a.Open {
		t.Error("the run is not the open chat's")
	}
	r.must(ui.RoleButton, "Pause")
	p.pos = 3200 * time.Millisecond
	r.Frames(2)
	r.must(ui.RoleLabel, "0:03")
	if n := r.must(ui.RoleSlider, "Voice message"); n.Range[2] < 3.1 || n.Range[2] > 3.3 || n.Range[1] != 7 {
		t.Errorf("slider range %v", n.Range)
	}

	r.Click(ui.RoleButton, "Pause")
	r.Frames(2)
	if p.playing {
		t.Fatal("Pause did not pause")
	}
	r.must(ui.RoleButton, "Play")
	r.must(ui.RoleLabel, "0:03")

	r.Click(ui.RoleButton, "Play")
	r.Frames(2)
	if !p.playing || p.plays != 2 || len(r.players) != 1 || len(r.decodes()) != 1 {
		t.Errorf("resume: playing %v, %d plays, %d players, %d decodes", p.playing, p.plays, len(r.players), len(r.decodes()))
	}
	if p.pos != 3200*time.Millisecond {
		t.Errorf("resumed at %v", p.pos)
	}
	if n := len(r.sent(abi.CommandTagMarkPlayed)); n != 1 {
		t.Errorf("%d played receipts", n)
	}
}

// Our own voice notes send no played receipt; a receipt follows the
// account's read-receipts setting.
func TestPlayedReceiptOnlyForTheirNotes(t *testing.T) {
	r := newRig(t, voice("mine", 10, true, "/v/2.ogg", 2), voice("theirs", 11, false, "/v/3.ogg", 3))
	r.a.Settings.SendReadReceipts = false
	r.Play(widgettest.Click(ui.RoleButton, "Play").Nth(0))
	r.until("playing", func() bool { return len(r.players) == 1 && r.players[0].playing })
	if n := len(r.sent(abi.CommandTagMarkPlayed)); n != 0 {
		t.Fatalf("our own note sent %d receipts", n)
	}
	r.Play(widgettest.Click(ui.RoleButton, "Play").Nth(0)) // the other's: ours shows Pause
	r.until("the second plays", func() bool { return len(r.players) == 2 && r.players[1].playing })
	mp := r.sent(abi.CommandTagMarkPlayed)
	if len(mp) != 1 || string(mp[0].MarkPlayed.Message) != "theirs" || mp[0].MarkPlayed.Receipts {
		t.Errorf("receipts %+v", mp)
	}
	if !r.a.Played.Has([]byte("theirs")) {
		t.Error("the note is not marked played")
	}
}

// One clip at a time: starting another closes the one playing.
func TestStartingAnotherNoteClosesTheFirst(t *testing.T) {
	r := newRig(t, voice("a", 10, false, "/v/4.ogg", 4), voice("b", 11, false, "/v/5.ogg", 5))
	r.Play(widgettest.Click(ui.RoleButton, "Play").Nth(0))
	first := r.player(0)
	r.until("first plays", func() bool { return first.playing })
	r.Play(widgettest.Click(ui.RoleButton, "Play").Nth(0))
	second := r.player(1)
	r.until("second plays", func() bool { return second.playing })
	if !first.closed {
		t.Error("the first player was left open")
	}
	if r.count(ui.RoleButton, "Pause") != 1 || r.count(ui.RoleButton, "Play") != 1 {
		t.Errorf("buttons:\n%s", widgettest.Dump(r.Tree()))
	}
}

// A click on the waveform seeks there and plays: an idle note is decoded
// and starts from there; a playing one jumps; a drag keeps seeking. As
// upstream's, seeking sends no played receipt.
func TestWaveformSeeksOnClickAndDrag(t *testing.T) {
	r := newRig(t, voice("v1", 10, false, "/v/8.ogg", 8))
	r.Click(ui.RoleSlider, "Voice message")
	p := r.player(0)
	r.until("playing", func() bool { return p.playing })
	if len(p.seeks) != 1 || p.seeks[0] < 3500*time.Millisecond || p.seeks[0] > 4500*time.Millisecond {
		t.Fatalf("an idle note started at %v, want about the middle", p.seeks)
	}
	if n := len(r.sent(abi.CommandTagMarkPlayed)); n != 0 {
		t.Errorf("a seek sent %d played receipts", n)
	}
	if r.s.Voice.Chat != r.a.Open {
		t.Error("a seek did not make the run the open chat's")
	}
	// paused, a click plays again from where it lands
	r.Click(ui.RoleButton, "Pause")
	r.Frames(1)
	p.pos = 1 * time.Second
	r.Click(ui.RoleSlider, "Voice message")
	r.Frames(1)
	if !p.playing || p.pos < 3500*time.Millisecond {
		t.Errorf("click while paused: playing %v at %v", p.playing, p.pos)
	}
	before := len(p.seeks)
	r.Play(widgettest.Drag(ui.RoleSlider, "Voice message", 60, 6))
	r.Frames(1)
	if len(p.seeks) <= before || p.pos <= 4500*time.Millisecond {
		t.Errorf("a drag sought %v, now at %v", p.seeks[before:], p.pos)
	}
	if len(r.decodes()) != 1 {
		t.Errorf("seeking decoded again: %v", r.decodes())
	}
}

// The chip cycles 1x, 1.5x, 2x and back, applies to the clip playing, and
// is remembered (Settings.VoiceSpeed) for the next note; a hand-edited
// speed shows as the nearest one.
func TestSpeedChipCyclesAndIsRemembered(t *testing.T) {
	r := newRig(t, voice("a", 10, false, "/v/4.ogg", 4), voice("b", 11, false, "/v/5.ogg", 5))
	r.Play(widgettest.Click(ui.RoleButton, "Play").Nth(0))
	p := r.player(0)
	r.until("playing", func() bool { return p.playing })
	for _, want := range []struct {
		speed float32
		label string
	}{{1.5, "1.5x"}, {2, "2x"}, {1, "1x"}, {1.5, "1.5x"}} {
		r.Play(widgettest.Click(ui.RoleButton, "Playback speed "+speedBefore(want.speed)).Nth(0))
		r.Frames(1)
		if r.s.Settings.VoiceSpeed != want.speed || p.rate != want.speed {
			t.Fatalf("speed %v, player %v, want %v", r.s.Settings.VoiceSpeed, p.rate, want.speed)
		}
		n := r.must(ui.RoleButton, "Playback speed "+want.label)
		if sel := n.State&ui.SemSelected != 0; sel != (want.speed > 1) {
			t.Errorf("%s selected %v", want.label, sel)
		}
	}
	r.Play(widgettest.Click(ui.RoleButton, "Play").Nth(0))
	q := r.player(1)
	r.until("the next plays", func() bool { return q.playing })
	if q.rate != 1.5 {
		t.Errorf("the next note plays at %v", q.rate)
	}
	r.s.Settings.VoiceSpeed = 1.3
	r.Frames(2)
	r.must(ui.RoleButton, "Playback speed 1.25x")
}

func speedBefore(v float32) string {
	switch v {
	case 1.5:
		return "1x"
	case 2:
		return "1.5x"
	}
	return "2x"
}

// A note that plays to its end carries on into the next unheard voice note
// of the chat, passing over ours and those already heard; anything else
// ends the run. A note not downloaded yet is fetched first and plays when
// it lands.
func TestARunPlaysTheNextUnheardNote(t *testing.T) {
	r := newRig(t,
		voice("v1", 10, false, "/v/3.ogg", 3),
		voice("mine", 11, true, "/v/2.ogg", 2),
		voice("v2", 12, false, "/v/4.ogg", 4),
		voice("v3", 13, false, "", 5),
		text("t", 14),
		voice("v4", 15, false, "/v/6.ogg", 6),
	)
	r.Play(widgettest.Click(ui.RoleButton, "Play").Nth(0))
	p1 := r.player(0)
	r.until("v1 plays", func() bool { return p1.playing })
	p1.pos = 2 * time.Second
	r.Frames(1)
	p1.end()
	p2 := r.player(1)
	r.until("v2 plays", func() bool { return p2.playing })
	if got := r.decodes(); len(got) != 2 || got[1] != "/v/4.ogg" {
		t.Fatalf("decoded %v", got)
	}
	if mp := r.sent(abi.CommandTagMarkPlayed); len(mp) != 2 || string(mp[1].MarkPlayed.Message) != "v2" {
		t.Fatalf("receipts %+v", mp)
	}
	r.must(ui.RoleButton, "Pause")

	// v3 is not here: it downloads, and plays once the core has it
	p2.end()
	r.Frames(3)
	dl := r.sent(abi.CommandTagDownload)
	if len(dl) != 1 || string(dl[0].Download.Message) != "v3" || dl[0].Download.Manual {
		t.Fatalf("downloads %+v", dl)
	}
	if len(r.players) != 2 {
		t.Fatalf("%d players before the file came", len(r.players))
	}
	r.f.Emit(ev(abi.EventTagMedia, func(e *abi.Event) {
		e.Media = abi.EventMedia{Chat: []byte(chatID), Message: []byte("v3"),
			Result: abi.PathResult{Tag: abi.PathResultTagOk, Ok: abi.PathResultOk{Path: []byte("/v/5.ogg")}}}
	}))
	p3 := r.player(2)
	r.until("v3 plays", func() bool { return p3.playing })

	// a text after it ends the run
	p3.end()
	r.Frames(4)
	if len(r.players) != 3 || len(r.decodes()) != 3 {
		t.Errorf("the run went past the text: %v", r.decodes())
	}
	r.must(ui.RoleLabel, "0:05") // the finished note shows its length again
}

// An end noticed long after it happened (nothing on screen polled the
// player) does not start the next note.
func TestALateEndDoesNotCarryOn(t *testing.T) {
	r := newRig(t, voice("v1", 10, false, "/v/3.ogg", 3), voice("v2", 11, false, "/v/4.ogg", 4))
	r.Play(widgettest.Click(ui.RoleButton, "Play").Nth(0))
	p := r.player(0)
	r.until("playing", func() bool { return p.playing })
	p.end()
	r.now = r.now.Add(time.Minute)
	r.Frames(4)
	if len(r.players) != 1 {
		t.Error("a stale end started the next note")
	}
	r.must(ui.RoleButton, "Play")
}

// Locking the app stops the clip and closes its player.
func TestLockStopsPlayback(t *testing.T) {
	r := newRig(t, voice("v1", 10, false, "/v/3.ogg", 3))
	r.Click(ui.RoleButton, "Play")
	p := r.player(0)
	r.until("playing", func() bool { return p.playing })
	r.s.Locked()
	r.Frames(2)
	if !p.closed || p.playing {
		t.Errorf("after the lock: closed %v, playing %v", p.closed, p.playing)
	}
	r.must(ui.RoleButton, "Play")
	r.must(ui.RoleLabel, "0:03")
}

// A system that pauses the player (an output gone) leaves it paused, not
// finished: no run carries on.
func TestASystemPauseIsAPause(t *testing.T) {
	r := newRig(t, voice("v1", 10, false, "/v/3.ogg", 3), voice("v2", 11, false, "/v/4.ogg", 4))
	r.Play(widgettest.Click(ui.RoleButton, "Play").Nth(0))
	p := r.player(0)
	r.until("playing", func() bool { return p.playing })
	p.pos, p.playing = time.Second, false
	r.Frames(3)
	if len(r.players) != 1 {
		t.Error("a pause started the next note")
	}
	if r.count(ui.RoleButton, "Play") != 2 {
		t.Errorf("not shown paused:\n%s", widgettest.Dump(r.Tree()))
	}
	r.must(ui.RoleLabel, "0:01")
}

// A note not downloaded has a Download button; it asks the core, and a
// spinner shows while the file comes. With automatic downloads on, a note
// in view is asked for by itself, once.
func TestUndownloadedNoteDownloads(t *testing.T) {
	r := newRig(t, voice("v1", 10, false, "", 3))
	if _, ok := r.Find(ui.RoleButton, "Playback speed 1x"); ok {
		t.Error("a speed chip before the file is here")
	}
	r.Click(ui.RoleButton, "Download")
	r.Frames(2)
	dl := r.sent(abi.CommandTagDownload)
	if len(dl) != 1 || !dl[0].Download.Manual || string(dl[0].Download.Message) != "v1" || string(dl[0].Download.Chat) != chatID {
		t.Fatalf("downloads %+v", dl)
	}
	r.must(ui.RoleProgressBar, "Downloading")

	r2 := newRig(t, voice("v2", 10, false, "", 3))
	r2.a.Settings.AutoDownload = true
	r2.Frames(6)
	if dl := r2.sent(abi.CommandTagDownload); len(dl) != 1 || dl[0].Download.Manual {
		t.Errorf("automatic downloads %+v", dl)
	}
}

// A failed download says why, and the button tries again.
func TestFailedDownloadSaysWhy(t *testing.T) {
	m := voice("v1", 10, false, "", 3)
	m.Content.Audio.Media.State = abi.MediaState{Tag: abi.MediaStateTagFailed, Failed: abi.MediaStateFailed{Error: []byte("Expired")}}
	r := newRig(t, m)
	r.must(ui.RoleLabel, "Expired. Click to retry.")
	r.Click(ui.RoleButton, "Download")
	r.Frames(2)
	if dl := r.sent(abi.CommandTagDownload); len(dl) != 1 || !dl[0].Download.Manual {
		t.Errorf("retry %+v", dl)
	}
}

// An audio file has a track and a music badge instead of a waveform, the
// same controls, and no run after it.
func TestAudioFileHasATrackAndNoRun(t *testing.T) {
	song := voice("song", 10, false, "/v/9.ogg", 9)
	song.Content.Audio.VoiceNote = false
	r := newRig(t, song, voice("v2", 11, false, "/v/4.ogg", 4))
	r.must(ui.RoleImage, "Audio")
	r.must(ui.RoleSlider, "Audio")
	r.Play(widgettest.Click(ui.RoleButton, "Play").Nth(0))
	p := r.player(0)
	r.until("playing", func() bool { return p.playing })
	p.end()
	r.Frames(4)
	if len(r.players) != 1 {
		t.Error("a run carried on after an audio file")
	}
}

// Switching account stops the clip.
func TestSwitchingAccountStops(t *testing.T) {
	r := newRig(t, voice("v1", 10, false, "/v/3.ogg", 3))
	r.Click(ui.RoleButton, "Play")
	p := r.player(0)
	r.until("playing", func() bool { return p.playing })
	r.a.OpenChat(0)
	r.Frames(1)
	r.s.AddAccount("2", nil, fake.New(nil))
	r.s.SwitchAccount(1)
	r.Frames(2)
	r.s.SwitchAccount(0)
	r.a.OpenChat(r.a.Model.Chats.Find([]byte(chatID)))
	r.Frames(3)
	if !p.closed {
		t.Error("playback outlived the account switch")
	}
}

// Has, Size and NoBubble: audio and video are this package's; a round note
// has no bubble.
func TestHasAndNoBubble(t *testing.T) {
	r := newRig(t, voice("v", 10, false, "", 3), video("vid", 11, "", false, false), video("note", 12, "", true, false), text("t", 13))
	for _, c := range []struct {
		id         string
		has, plain bool
	}{{"v", true, true}, {"vid", true, true}, {"note", true, false}, {"t", false, true}} {
		m := r.view(c.id)
		if got := av.Has(m); got != c.has {
			t.Errorf("%s: Has %v", c.id, got)
		}
		if got := av.NoBubble(m); got == c.plain {
			t.Errorf("%s: NoBubble %v", c.id, got)
		}
	}
}

// Through the core: the fake core's DecodeVoice decodes the file into a
// blob, which is read on the worker, played as it is (mono float32 at
// 48 kHz) and freed; the core's waveform stands in for a note that carried
// none.
func TestNotesDecodeThroughTheCore(t *testing.T) {
	m := voice("v1", 10, false, "/v/core.ogg", 1)
	m.Content.Audio.Waveform = nil
	r := newRigWith(t, true, m)
	samples := make([]float32, core.VoiceRate/2)
	for i := range samples {
		samples[i] = float32(i%100) / 100
	}
	r.f.SetVoice("/v/core.ogg", samples)
	r.Click(ui.RoleButton, "Play")
	p := r.player(0)
	r.until("playing", func() bool { return p.playing })
	if got := r.f.Decoded(); len(got) != 1 || got[0] != "/v/core.ogg" {
		t.Fatalf("decoded %v", got)
	}
	if p.dur != 500*time.Millisecond || p.format != (media.Format{SampleRate: 48000, Channels: 1, Sample: media.Float32}) || len(p.pcm) != 4*len(samples) {
		t.Fatalf("player of %v, %+v, %d bytes", p.dur, p.format, len(p.pcm))
	}
	if v := math.Float32frombits(binary.LittleEndian.Uint32(p.pcm[4*37:])); v != samples[37] {
		t.Errorf("sample 37 is %v, want %v", v, samples[37])
	}
	for id := uint64(1); id < 8; id++ {
		if _, err := r.f.ReadBlob(id, 0, make([]byte, 4)); err == nil {
			t.Errorf("blob %d was not freed", id)
		}
	}
}

// A file the core cannot read says so, as upstream's player does; the note
// can be played again.
func TestADecodeFailureSaysWhy(t *testing.T) {
	r := newRigWith(t, true, voice("v1", 10, false, "/v/gone.ogg", 1))
	r.f.RefuseVoice("/v/gone.ogg", core.ENotFound, "no such file")
	r.Click(ui.RoleButton, "Play")
	r.until("the toast", func() bool { return len(r.s.Toasts) > 0 })
	if got := string(r.s.Toasts[len(r.s.Toasts)-1].Text); got != "Could not read the audio: no such file" {
		t.Errorf("toast %q", got)
	}
	r.Frames(2)
	r.must(ui.RoleButton, "Play")
	if len(r.players) != 0 {
		t.Error("a player for a file that failed")
	}
}

// An audio file the core does not decode (an MP3: not Ogg/Opus) plays in
// the app from its file (media.OpenPlayer), with the voice notes' seek bar
// and speed chip (MK-020); a spinner shows while the file opens. One the
// system cannot decode either goes to the system's player; one that cannot
// be read says why.
func TestAnAudioFileTheCoreCannotDecodePlaysFromItsFile(t *testing.T) {
	song := voice("song", 10, false, "/v/song.mp3", 200)
	song.Content.Audio.VoiceNote = false
	odd := voice("odd", 11, false, "/v/odd.xyz", 20)
	odd.Content.Audio.VoiceNote = false
	gone := voice("gone", 12, false, "/v/gone.m4a", 20)
	gone.Content.Audio.VoiceNote = false
	r := newRigWith(t, true, song, odd, gone)
	for _, p := range []string{"/v/song.mp3", "/v/odd.xyz", "/v/gone.m4a"} {
		r.f.RefuseVoice(p, core.EMalformed, "not an Ogg stream")
	}
	r.Play(clickPlay(0))
	r.until("a file player", func() bool { return len(r.files) == 1 })
	f := r.files[0]
	r.Frames(2)
	if f.path != "/v/song.mp3" || len(r.players) != 0 {
		t.Fatalf("opened %q; %d PCM players", f.path, len(r.players))
	}
	r.must(ui.RoleProgressBar, "Loading…")
	f.opening, f.dur = false, 200*time.Second
	r.until("it plays", func() bool { return f.playing })
	r.Frames(1)
	r.must(ui.RoleButton, "Pause")
	r.Click(ui.RoleSlider, "Audio")
	if n := len(f.seeks); n == 0 || f.seeks[n-1] < 90*time.Second || f.seeks[n-1] > 110*time.Second {
		t.Errorf("seeks %v", f.seeks)
	}
	r.Click(ui.RoleButton, "Playback speed 1x")
	if f.rate != 1.5 {
		t.Errorf("rate %v", f.rate)
	}
	if len(r.sh.opened()) != 0 {
		t.Errorf("opened %v", r.sh.opened())
	}

	r.Play(clickPlay(0)) // odd's: the song's button says Pause
	r.until("a second file player", func() bool { return len(r.files) == 2 })
	if !f.closed {
		t.Error("the song kept playing")
	}
	r.files[1].err = media.ErrFormat
	// The system gets only a file of a known format (state.OpenFile): this
	// one is shown in its folder with the note, never launched.
	r.until("the safety note", func() bool {
		n := len(r.s.Toasts)
		return n > 0 && string(r.s.Toasts[n-1].Text) == "For safety, open this file yourself from its folder"
	})
	if got := r.sh.opened(); len(got) != 0 {
		t.Errorf("a file of an unknown format was opened: %q", got)
	}

	r.Play(clickPlay(2))
	r.until("a third file player", func() bool { return len(r.files) == 3 })
	r.files[2].err = fs.ErrNotExist
	r.until("the toast", func() bool { return len(r.s.Toasts) > 0 })
	if got := string(r.s.Toasts[len(r.s.Toasts)-1].Text); got != "Could not read the audio: file does not exist" {
		t.Errorf("toast %q", got)
	}
}

// A run carries on as soon as the note's position reaches its end, without
// waiting for the player to say it stopped (as upstream's carries on once
// its sink has drained).
func TestARunCarriesOnAtTheLastSample(t *testing.T) {
	r := newRig(t, voice("v1", 10, false, "/v/3.ogg", 3), voice("v2", 11, false, "/v/4.ogg", 4))
	r.Play(clickPlay(0))
	p := r.player(0)
	r.until("playing", func() bool { return p.playing })
	p.pos = p.dur // still playing: the system's tail
	q := r.player(1)
	r.until("the next plays", func() bool { return q.playing })
	if !p.closed {
		t.Error("the first note kept its player")
	}
}

// The framework's Ended event for the playing note ends it at once (the
// shell may pass app events to Event); another player's is not ours.
func TestTheEndedEventEndsTheNote(t *testing.T) {
	r := newRig(t, voice("v1", 10, false, "/v/3.ogg", 3), voice("v2", 11, false, "/v/4.ogg", 4))
	r.Play(widgettest.Click(ui.RoleButton, "Play").Nth(0))
	p := r.player(0)
	r.until("playing", func() bool { return p.playing })
	ended := func(id uint64) *app.Event {
		return &app.Event{Kind: app.EvService, Source: platform.SourceMedia, A: int64(media.Ended), B: int64(id)}
	}
	if av.Event(r.s, ended(p.id+100)) {
		t.Error("another player's end was taken")
	}
	if av.Event(r.s, &app.Event{Kind: app.EvPost}) {
		t.Error("a post was taken")
	}
	if !av.Event(r.s, ended(p.id)) {
		t.Fatal("the note's end was not taken")
	}
	q := r.player(1)
	r.until("the run carries on", func() bool { return q.playing })
}
