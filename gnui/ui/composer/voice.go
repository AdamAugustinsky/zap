package composer

import (
	"bytes"
	"errors"
	"log/slog"
	"math"
	"time"

	"gonativeui/app"
	"gonativeui/app/perm"
	"gonativeui/media"
	"gonativeui/rexui"
	"gonativeui/theme"
	"gonativeui/ui"

	"zapfast/abi"
	"zapfast/core"
	"zapfast/format"
	"zapfast/i18n"
	"zapfast/state"
	"zapfast/ui/screen"
)

// voice is the voice recorder's place in the composer
// (zf:src/ui/conversation.rs:1800-1822, :2100-2133, :9450-9533;
// zf:src/app.rs:7116-7140, :9041-9116): the microphone of an empty composer,
// the recording bar that takes the field's place (Discard, the light and
// the time, the live waveform, Send), Enter and Escape while recording
// (zf:src/ui/keys.rs:171, :248), and a refused voice message waiting above
// the field ("Voice message (0:12) not sent").
type voice struct {
	// newMic makes the microphone (the demo's in the demo); tests replace
	// it. warm starts the audio stack after the first frame.
	newMic func(demo bool, now func() time.Time, wake func()) mic
	warm   func()
	m      mic
	warmed bool

	on     bool // the bar shows: recording, or stopped at the longest
	asking bool // the system asks for the microphone; recording starts once granted
	asked  []byte
	// ticket is the request the system answers (perm.Result through
	// OnAppEvent); answer is its answer, when answered.
	ticket   perm.Ticket
	answer   perm.Result
	answered bool
	// sending: Send was pressed and the recorder is finishing; the
	// recording goes to chat (of acct), quoting quote, once it is read.
	sending bool
	acct    state.AcctRef
	chat    []byte
	quote   quote
	hidden  bool // the quote's chat is hidden (QuoteHidden): it will not go

	// levels are the loudness of each 50 ms recorded, newest last (a ring
	// of the last len(levels)); next is the elapsed time of the next one.
	levels [512]float32
	n      int
	next   time.Duration
	// stopped is the recording's length once it stopped at the longest.
	stopped time.Duration
	tick    *time.Timer
	clock   []byte
	waveK   ui.Key

	done    chan voiceBlob // recordings converted for the core
	nextJob uint64
	pending map[uint64]voiceBlob // cancellable jobs, owned by the UI thread

	unsent unsent
}

// quote is a reply a send carries, copied.
type quote struct {
	id, chat []byte
	on       bool
}

func (q *quote) set(p *state.Panes) {
	q.on = p.Reply.Len() > 0
	q.id, q.chat = q.id[:0], q.chat[:0]
	if q.on {
		q.id = append(q.id, p.Reply.At(0)...)
	}
	if p.ReplyChat.Len() > 0 {
		q.chat = append(q.chat, p.ReplyChat.At(0)...)
	}
}

func (q *quote) target() (abi.ReplyTarget, bool) {
	if !q.on {
		return abi.ReplyTarget{}, false
	}
	return abi.ReplyTarget{ID: q.id, Chat: q.chat, HasChat: len(q.chat) > 0}, true
}

// unsent is a voice message the core refused, which Send tries again
// (zf:src/app.rs:4687): its samples stay in their core blob, or in memory
// until blob preparation succeeds.
type unsent struct {
	acct    state.AcctRef
	conn    core.Conn // the blob's original connection, never a relinked slot's
	chat    []byte
	blob    abi.BlobRef
	samples []byte // converted PCM retained when PutBlob failed
	label   string // "Voice message (0:12) not sent"
}

// voiceBlob is a recording converted for SendVoice and put in a blob.
type voiceBlob struct {
	acct    state.AcctRef
	job     uint64
	conn    core.Conn
	chat    []byte
	quote   quote
	blob    abi.BlobRef
	samples []byte
	err     error
	// hidden: its quote was of a chat the reader could not see.
	hidden bool
}

const (
	// longestRecording is the longest the core sends (core.VoiceLongestSend):
	// the capture stops there and waits for Send or Discard.
	longestRecording = core.VoiceLongestSend
	// shortestRecording: anything shorter is dropped unsent
	// (zf:src/app.rs:9091).
	shortestRecording = 500 * time.Millisecond
	levelEvery        = 50 * time.Millisecond
	// voiceTick is how often the bar's time and waveform step.
	voiceTick = 40 * time.Millisecond
)

// newMic is the microphone for the window: the system's, or the demo's.
func newMic(demo bool, now func() time.Time, wake func()) mic {
	if demo {
		return &demoMic{now: now, wake: wake}
	}
	return &deviceMic{}
}

// init readies the recorder for the window.
func (v *voice) init(e *screen.Env) {
	if v.newMic == nil {
		v.newMic = newMic
	}
	if v.warm == nil {
		v.warm = media.Warm
	}
	st := e.S
	v.tick = time.AfterFunc(time.Hour, func() { st.Wake() })
	v.tick.Stop()
	if v.done == nil {
		v.done = make(chan voiceBlob, 4) // preparation outlives its window
	}
	v.waveK = ui.HashString(0, "zapfast composer waveform")
	v.clock = make([]byte, 0, 16)
}

// device is the microphone, made the first time it is wanted.
func (v *voice) device(e *screen.Env) mic {
	if v.m == nil {
		v.m = v.newMic(e.S.Demo, e.S.Now, e.S.Wake)
	}
	return v.m
}

// recording reports whether the bar shows: nothing is staged meanwhile, and
// the app counts it as in use.
func (v *voice) recording() bool { return v.on }

// frame runs once per frame before the band: the system's answer about the
// microphone, the recorder's progress, and recordings ready for the core.
func (v *voice) frame(s *Screen, e *screen.Env) {
	if !v.warmed {
		v.warmed = true
		if v.warm != nil {
			v.warm()
		}
	}
	v.takeBlobs(s, e)
	if v.answered {
		v.answered = false
		v.takeAnswer(s, e)
	}
	if v.m == nil || !v.on && !v.sending {
		return
	}
	switch v.m.State() {
	case media.Recording:
		el := v.m.Elapsed()
		v.sample(el)
		if el >= longestRecording {
			v.stopped = el
			if err := v.m.Stop(); err != nil {
				v.fail(e, err)
				v.discard(s)
				return
			}
		}
		v.tick.Reset(voiceTick)
	case media.Finishing:
		// The recording is being read: its Recorded event (appEvent)
		// wakes the window when it is Ready.
	case media.Ready:
		if v.sending {
			v.finish(s, e)
		}
	case media.Idle:
		v.on, v.sending = false, false
	}
}

// appEvent takes the system's answer about the microphone and the
// recorder's Recorded event (State.OnAppEvent): it runs on the main thread,
// maybe with no window, so it only notes them and wakes the window.
func (v *voice) appEvent(st *state.State, ev *app.Event) bool {
	if r, ok := perm.ResultOf(ev); ok {
		if !v.asking || r.Kind != perm.Microphone || r.Ticket != v.ticket {
			return false
		}
		v.answer, v.answered = r, true
		st.Wake()
		return true
	}
	if me, ok := media.EventOf(ev); ok && me.Kind == media.Recorded && v.m != nil && me.ID == v.m.ID() {
		st.Wake() // the next frame finds it Ready
		return true
	}
	return false
}

// takeAnswer starts the recording the user just allowed, while its chat is
// open and nothing else took the composer; a refusal says how to allow it.
func (v *voice) takeAnswer(s *Screen, e *screen.Env) {
	if !v.asking {
		return
	}
	v.asking = false
	if !v.answer.Granted {
		v.denied(e)
		return
	}
	if bytes.Equal(v.asked, s.chat) && len(s.edit) == 0 && s.empty() {
		v.begin(s, e)
	}
}

// start is the microphone's click (Action::StartRecording): with the
// user's leave, recording starts; the system asks the first time.
func (v *voice) start(s *Screen, e *screen.Env) {
	s.tools = false
	e.S.Panes.Picker = state.PickerNone
	if v.on || v.sending || v.asking {
		return
	}
	v.acct = s.acct.Ref()
	v.chat = append(v.chat[:0], s.chat...)
	m := v.device(e)
	st, err := m.Access()
	switch {
	case err != nil:
		v.fail(e, err)
	case st == perm.Authorized:
		v.begin(s, e)
	case st == perm.NotDetermined:
		tk, err := m.Ask()
		if err != nil {
			v.fail(e, err)
			return
		}
		v.asking, v.ticket, v.answered = true, tk, false
		v.asked = append(v.asked[:0], s.chat...)
	default:
		v.denied(e)
	}
	e.S.Touch()
}

// begin starts the recorder.
func (v *voice) begin(s *Screen, e *screen.Env) {
	if err := v.m.Start(); err != nil {
		v.fail(e, err)
		return
	}
	v.on, v.n, v.next, v.stopped = true, 0, levelEvery, 0
	s.comp.close(s)
	e.S.Panes.Picker = state.PickerNone
	v.tick.Reset(voiceTick)
	e.S.Touch()
}

// sample keeps a level for each 50 ms recorded.
func (v *voice) sample(el time.Duration) {
	lv := v.m.Level()
	for k := 0; v.next <= el && k < len(v.levels); k++ {
		v.levels[v.n%len(v.levels)] = lv
		v.n++
		v.next += levelEvery
	}
	if v.next <= el { // a long stall: start over from now
		v.next = el + levelEvery
	}
}

// send is Send, or Enter, while recording: the recorder stops, and the
// recording goes once it is read, quoting the reply shown now
// (Action::SendRecording).
func (v *voice) send(s *Screen, e *screen.Env) {
	if !v.on {
		return
	}
	if v.acct != s.acct.Ref() || !bytes.Equal(v.chat, s.chat) {
		v.discard(s)
		return
	}
	if v.m.State() == media.Recording {
		if err := v.m.Stop(); err != nil {
			v.fail(e, err)
			v.discard(s)
			return
		}
	}
	v.on, v.sending = false, true
	p := &e.S.Panes
	// A private reply quoting a chat the reader may not see now sends
	// nothing: the recording waits as a refused one, the reply stays
	// (zf:src/app.rs:9086; MM-007).
	v.hidden = e.S.QuoteHidden(s.acct)
	v.quote.set(p)
	if !v.hidden {
		p.Reply.Reset()
		p.ReplyChat.Reset()
		s.reply, s.replyChat = s.reply[:0], s.replyChat[:0]
	}
	v.tick.Stop()
	if v.m.State() == media.Ready {
		v.finish(s, e)
	}
	s.st.Focus(e.K.Kit)
	e.S.Touch()
}

// finish takes the read recording: one under half a second is dropped; the
// rest is converted for the core on a goroutine (core.AppendVoicePCM16)
// and put in a blob, which takeBlobs sends.
func (v *voice) finish(s *Screen, e *screen.Env) {
	v.sending = false
	pcm, f, err := v.m.Take()
	switch {
	case err != nil:
		v.fail(e, err)
		return
	case f.Sample != media.Int16:
		v.fail(e, errors.New("unexpected sample format"))
		return
	case f.Duration(len(pcm)) < shortestRecording:
		return
	}
	if !v.acct.Valid() {
		return
	}
	b := voiceBlob{acct: v.acct, conn: v.acct.A.Conn, chat: append([]byte(nil), v.chat...), hidden: v.hidden}
	b.quote.id = append([]byte(nil), v.quote.id...)
	b.quote.chat = append([]byte(nil), v.quote.chat...)
	b.quote.on = v.quote.on
	b = v.queueBlob(b)
	done, wake := v.done, e.S.Wake
	go func() {
		b.samples = core.AppendVoicePCM16(nil, pcm, f.SampleRate, f.Channels)
		putVoiceBlob(b, done, wake)
	}()
}

// queueBlob registers a job before its worker starts. Removing its ticket
// cancels delivery, including when the same chat JID is subsequently recreated.
func (v *voice) queueBlob(b voiceBlob) voiceBlob {
	v.nextJob++
	b.job = v.nextJob
	if v.pending == nil {
		v.pending = make(map[uint64]voiceBlob)
	}
	v.pending[b.job] = b
	return b
}

// putVoiceBlob runs on a worker, retaining the PCM on failure for another
// explicit Send. Successful preparation transfers retry ownership to the blob.
func putVoiceBlob(b voiceBlob, done chan<- voiceBlob, wake func()) {
	if b.conn == nil {
		b.err = state.ErrNoCore
	} else {
		b.blob, b.err = b.conn.PutBlob(b.samples)
	}
	if b.err == nil {
		b.samples = nil
	}
	done <- b
	wake()
}

// takeBlobs sends the recordings converted since the last frame; one whose
// quote the reader may not see waits as a refused voice message.
func (v *voice) takeBlobs(s *Screen, e *screen.Env) {
	for {
		select {
		case b := <-v.done:
			_, pending := v.pending[b.job]
			delete(v.pending, b.job)
			if !pending || !b.acct.Valid() || b.acct.A.Model.Chats.Find(b.chat) == 0 {
				if b.conn != nil && b.blob.ID != 0 {
					b.conn.FreeBlob(b.blob.ID)
				}
				continue
			}
			switch {
			case b.err != nil:
				v.refused(s, b.acct.A, b.chat, b.blob, b.samples)
				if q, ok := b.quote.target(); ok {
					s.rearmQuote(b.acct.A, b.chat, &q)
				}
				v.fail(e, b.err)
			case b.hidden:
				v.refused(s, b.acct.A, b.chat, b.blob, nil)
				quoteUnavailable(e.S)
			default:
				if err := v.sendVoice(b.acct.A, b.chat, b.blob, &b.quote); err != nil {
					v.refused(s, b.acct.A, b.chat, b.blob, nil)
					if q, ok := b.quote.target(); ok {
						s.rearmQuote(b.acct.A, b.chat, &q)
					}
					e.S.Toast([]byte(err.Error()), true)
				}
			}
			continue
		default:
		}
		return
	}
}

// sendVoice sends samples (a blob, which the core takes) to chat.
func (v *voice) sendVoice(a *state.Account, chat []byte, blob abi.BlobRef, q *quote) error {
	cmd := a.Cmd(abi.CommandTagSendVoice)
	cmd.SendVoice.Chat, cmd.SendVoice.Samples = chat, blob
	cmd.SendVoice.Quoting, cmd.SendVoice.HasQuoting = q.target()
	return a.Send(cmd) // on failure the caller keeps the blob for retry
}

// discard drops the recording (Discard, Escape, the lock, another chat;
// Action::CancelRecording), and a request for the microphone not yet
// answered.
func (v *voice) discard(s *Screen) {
	if v.on && v.m != nil {
		v.m.Cancel()
	}
	v.on, v.asking = false, false
	if v.tick != nil {
		v.tick.Stop()
	}
}

// forget retires only this account/chat's voice data, including jobs still
// converting, a recorder still finishing, and retained PCM or core blobs.
func (v *voice) forget(s *Screen, a *state.Account, chat []byte) {
	if v.unsent.acct.A == a && (len(chat) == 0 || bytes.Equal(v.unsent.chat, chat)) {
		v.dropUnsent()
	}
	for id, b := range v.pending {
		if b.acct.A == a && (len(chat) == 0 || bytes.Equal(b.chat, chat)) {
			delete(v.pending, id)
		}
	}
	if v.acct.A == a && (len(chat) == 0 || bytes.Equal(v.chat, chat)) {
		if v.sending && v.m != nil {
			v.m.Cancel()
		}
		v.sending = false
		v.discard(s)
		v.acct, v.chat, v.quote = state.AcctRef{}, nil, quote{}
	}
}

// fail says why recording failed (zf:src/app.rs:9111): never what was said.
func (v *voice) fail(e *screen.Env, err error) {
	why := err.Error()
	switch {
	case errors.Is(err, errors.ErrUnsupported):
		why = i18n.T("no microphone")
	case errors.Is(err, perm.ErrNoUsageDescription):
		why = i18n.T("this build cannot ask for the microphone (run Zap from its app bundle)")
	case errors.Is(err, media.ErrNotAuthorized):
		why = i18n.T("microphone access is not allowed (System Settings, Privacy & Security, Microphone)")
	}
	slog.Warn("zapfast: recording failed", "err", err)
	e.S.Toast([]byte(i18n.Format(i18n.T("Could not record: {}"), "{}", why)), true)
}

// denied: the user said no, and only System Settings changes that now; it
// opens on the microphone's page.
func (v *voice) denied(e *screen.Env) {
	e.S.Toast([]byte(i18n.Format(i18n.T("Could not record: {}"), "{}",
		i18n.T("microphone access is off for Zap (System Settings, Privacy & Security, Microphone)"))), true)
	if err := v.m.Settings(); err != nil {
		slog.Warn("zapfast: System Settings did not open", "err", err)
	}
}

// keys are Enter (send) and Escape (discard) while recording: the text is
// hidden, so the bar takes them (zf:src/ui/keys.rs:171, :248).
func (v *voice) keys(s *Screen, e *screen.Env) {
	c := e.C()
	if c.ModalOpen() {
		return
	}
	if c.TakeKey(ui.KeyEscape, 0) {
		v.discard(s)
		s.st.Focus(e.K.Kit)
		e.S.Touch()
	}
	if v.on && c.TakeKey(ui.KeyEnter, 0) {
		v.send(s, e)
	}
}

// bar builds the recording bar in the field's place: Discard, the light
// and the time, the waveform of the last levels, newest by Send
// (zf:src/ui/conversation.rs:9451).
func (v *voice) bar(s *Screen, e *screen.Env) {
	k := e.K
	c := e.C()
	r := k.Theme()
	look := &rexui.ComposerLook
	el := v.m.Elapsed()
	if v.m.State() != media.Recording {
		el = v.stopped
	}
	c.NextBG(look.Fill.RGBA(r))
	c.NextBorder(look.Border.RGBA(r), e.Pt(look.BorderW))
	c.NextRadius(e.Pt(look.Radius))
	c.NextAxis(ui.AxisX)
	c.NextCross(ui.CrossCenter)
	c.NextGap(e.Pt(8))
	pad := e.Pt(look.Pad)
	c.NextPadding(pad, pad, pad, pad)
	c.NextSize(ui.Pct(1), ui.Children(1))
	c.NextRole(ui.RoleGroup)
	c.NextSemName(i18n.T("Recording"))
	c.Push(c.Box(ui.DrawBackground|ui.DrawBorder, "recording"))
	// round buttons as big as the field's send button, inside its capsule:
	// Discard red, as an alert's destructive button, Send the accent
	if circleButton(k.Kit, i18n.T("Discard"), s.ic.trash, &rexui.ButtonRecipes[rexui.ButtonDestructive]).Clicked {
		v.discard(s)
		s.st.Focus(e.K.Kit)
		e.S.Touch()
	}
	pulse := float32(0.55 + 0.45*math.Abs(math.Sin(el.Seconds()*3)))
	c.NextBG(theme.WithAlpha(r.Colors[theme.Danger], pulse))
	c.NextRadius(e.Pt(5))
	c.NextSize(ui.Px(e.Pt(10)), ui.Px(e.Pt(10)))
	c.Box(ui.DrawBackground, "light")
	v.clock = format.AppendDuration(v.clock[:0], uint32(el/time.Second))
	rexui.LabelBytes(k.Kit, v.clock, rexui.LabelOpts{MetaPt: 13})
	v.wave(e)
	if circleButton(k.Kit, i18n.T("Send"), s.ic.send, &rexui.ButtonRecipes[rexui.ButtonPrimary]).Clicked {
		v.send(s, e)
	}
	c.Pop()
}

// wave draws the levels across the room left, newest on the right
// (2 pt bars every 3 pt, 2 to 26 pt tall).
func (v *voice) wave(e *screen.Env) {
	k := e.K
	c := e.C()
	col := k.Theme().Colors[theme.Accent]
	h := e.Pt(28)
	c.NextSize(ui.Pct(1), ui.Px(h))
	c.NextAxis(ui.AxisX)
	c.NextCross(ui.CrossCenter)
	c.NextJustify(ui.JustifyEnd)
	c.NextGap(e.Pt(2))
	c.NextRole(ui.RoleImage)
	c.NextSemName(i18n.T("Recording"))
	id := c.BoxKeyed(ui.Clip, v.waveK, nil)
	c.Push(id)
	r := c.RectOf(v.waveK)
	room := int((r[2] - r[0]) / e.Pt(3))
	shown := min(v.n, len(v.levels), max(room, 0))
	for i := v.n - shown; i < v.n; i++ {
		lv := v.levels[i%len(v.levels)]
		c.NextBG(col)
		c.NextRadius(e.Pt(1))
		c.NextSize(ui.Px(e.Pt(2)), ui.Px(e.Pt(2+min(lv*4, 1)*24)))
		c.BoxKeyed(ui.DrawBackground, ui.KeyN(v.waveK, uint64(i%len(v.levels))+1), nil)
	}
	c.Pop()
}

// refused keeps a voice message the core refused for its chat; one refused
// before is dropped (zf:src/app.rs:4687).
func (v *voice) refused(s *Screen, a *state.Account, chat []byte, blob abi.BlobRef, samples []byte) {
	v.dropUnsent()
	u := &v.unsent
	u.acct, u.chat, u.blob = a.Ref(), append(u.chat[:0], chat...), blob
	u.conn = a.Conn
	u.samples = samples
	secs := uint32(max(math.Round(float64(max(blob.Len, uint64(len(samples))))/4/float64(core.VoiceRate)), 1))
	u.label = i18n.Format(i18n.T("Voice message ({duration}) not sent"), "{duration}", string(format.AppendDuration(nil, secs)))
	if a.Active() && bytes.Equal(chat, s.chat) {
		s.st.Focus(s.env.K.Kit)
	}
}

// dropUnsent releases a refused voice message's blob or unprepared PCM.
func (v *voice) dropUnsent() {
	u := &v.unsent
	if u.conn != nil && u.blob.ID != 0 {
		u.conn.FreeBlob(u.blob.ID)
	}
	u.acct, u.blob = state.AcctRef{}, abi.BlobRef{}
	u.conn, u.label = nil, ""
	u.samples = nil
	u.chat = u.chat[:0]
}

// unsentHere: a refused voice message waits in the chat the composer holds,
// and no edit hides it.
func (v *voice) unsentHere(s *Screen) bool {
	u := &v.unsent
	return u.acct.A == s.acct && u.acct.Valid() && bytes.Equal(u.chat, s.chat) && len(s.edit) == 0
}

// resend sends the refused voice message again, quoting the reply shown now
// (zf:src/app.rs:9046).
func (v *voice) resend(s *Screen, e *screen.Env) {
	if !v.unsentHere(s) {
		return
	}
	u := &v.unsent
	p := &e.S.Panes
	if e.S.QuoteHidden(u.acct.A) {
		quoteUnavailable(e.S) // cancelling the reply sends it unquoted
		return
	}
	var q quote
	q.set(p)
	if len(u.samples) > 0 {
		b := v.queueBlob(voiceBlob{acct: u.acct, conn: u.acct.A.Conn, chat: append([]byte(nil), u.chat...), samples: u.samples, quote: q})
		go putVoiceBlob(b, v.done, e.S.Wake)
	} else if err := v.sendVoice(u.acct.A, u.chat, u.blob, &q); err != nil {
		e.S.Toast([]byte(err.Error()), true)
		return
	}
	u.acct, u.blob = state.AcctRef{}, abi.BlobRef{}
	u.conn = nil
	u.samples = nil // the preparation worker or accepted command now owns it
	p.Reply.Reset()
	p.ReplyChat.Reset()
	s.reply, s.replyChat = s.reply[:0], s.replyChat[:0]
	s.st.Focus(e.K.Kit)
	e.S.Touch()
}

// unsentStrip is the refused voice message above the field, with Discard
// (zf:src/ui/conversation.rs:2100).
func (v *voice) unsentStrip(s *Screen, e *screen.Env) {
	k := e.K
	danger := rexui.Of(theme.Danger)
	beginStrip(e, "unsent voice")
	k.Icon(s.ic.mic, k.Theme().Colors[theme.Danger])
	rexui.Label(k.Kit, v.unsent.label, rexui.LabelOpts{Style: rexui.TextSemibold, Color: danger})
	k.Spacer()
	if s.closeButton(e, i18n.T("Discard voice message")) {
		v.dropUnsent()
		e.S.Touch()
	}
	endStrip(e)
}
