package logic

import (
	"bytes"
	"time"

	"zapfast/abi"
	"zapfast/model"
	"zapfast/state"
)

// VoiceFetchHold is how long a run keeps other apps' media paused while
// the next clip downloads (zf:src/app.rs:170).
const VoiceFetchHold = 10 * time.Second

func isVoice(m model.Message) bool {
	md, ok := m.Media()
	return ok && m.Kind() == abi.ContentTagAudio && md.Voice
}

// NextVoiceAfter is the next voice message after finished that the reader
// has not heard: as on the phone, the run goes through consecutive voice
// messages, passing over the reader's own and those already played, and
// anything else, an audio file included, ends it (zf:src/app.rs:9016).
// Nil for none.
func NextVoiceAfter(v *model.Conversation, finished []byte, played *model.IDList) []byte {
	i := v.Find(finished)
	if i < 0 || !isVoice(v.At(i)) {
		return nil
	}
	for j := i + 1; j < v.Len(); j++ {
		m := v.At(j)
		if !isVoice(m) {
			return nil
		}
		if !m.FromMe() && !played.Has(m.ID()) {
			return m.ID()
		}
	}
	return nil
}

// PlayVoice records that the reader started a voice message in the open
// chat: the run belongs to that chat, a clip being fetched for it is no
// longer wanted, and the sender hears it was played (zf:src/app.rs:8937).
// ui/conversation/av calls it when it starts the player.
func PlayVoice(s *state.State, a *state.Account, msg []byte) {
	v := &s.Voice
	v.Want.Reset()
	v.Next.Reset()
	v.Chat = a.Open
	TellPlayed(a, msg)
}

// SeekVoice records that the reader moved a voice message's place (a
// click or a drag on its waveform): the run belongs to the open chat, and a
// clip a run is still fetching is no longer wanted. Unlike PlayVoice it
// sends no played receipt (zf:src/app.rs:7098, Action::SeekVoice).
// ui/conversation/av calls it, then seeks its player.
func SeekVoice(s *state.State, a *state.Account) {
	s.Voice.Want.Reset()
	s.Voice.Chat = a.Open
	s.Touch()
}

// TellPlayed sends the played receipt of an incoming voice (or round video)
// message of the open chat, once (zf:src/app.rs:8948).
func TellPlayed(a *state.Account, msg []byte) {
	c, ok := a.OpenChatView()
	if !ok || a.Played.Has(msg) {
		return
	}
	v := a.Conversation()
	if v == nil {
		return
	}
	i := v.Find(msg)
	if i < 0 || v.At(i).FromMe() {
		return
	}
	m := v.At(i)
	a.Played.Add(msg)
	cmd := a.Cmd(abi.CommandTagMarkPlayed)
	cmd.MarkPlayed.Chat, cmd.MarkPlayed.Message, cmd.MarkPlayed.Sender = c.ID(), msg, m.Sender()
	cmd.MarkPlayed.Receipts = a.Settings.SendReadReceipts
	a.Send(cmd)
}

// VoiceEnded carries a run on after a clip played to its end: the next
// unheard voice message plays (State.Voice.Next) or, not downloaded yet, is
// fetched first and plays when it lands. Only a clip that finished in the
// chat the reader is still in carries on (zf:src/app.rs:8978).
func VoiceEnded(s *state.State, a *state.Account, finished []byte) {
	v := &s.Voice
	if v.Chat == 0 || (v.Chat != a.Open && v.Chat != a.Split) {
		return // the run's chat is in neither pane
	}
	c, ok := a.Model.Chats.Get(v.Chat)
	if !ok {
		return
	}
	conv := a.Model.Conversation(c.ID())
	if conv == nil {
		return
	}
	next := NextVoiceAfter(conv, finished, &a.Played)
	if next == nil {
		return
	}
	m := conv.At(conv.Find(next))
	if md, _ := m.Media(); len(md.Path) > 0 {
		v.Next.Reset()
		v.Next.Add(next)
		v.Next.Add(md.Path)
		s.Touch()
		return
	}
	v.Want.Reset()
	v.Want.Add(next)
	v.WantAt = s.Now()
	a.Download(c.ID(), next, model.PartMain, false)
}

// StopVoiceRun ends the run: a video started, or another sound took over.
func StopVoiceRun(s *state.State) {
	s.Voice.Chat = 0
	s.Voice.Want.Reset()
	s.Voice.Next.Reset()
}

// FetchingNext reports whether a run is waiting for its next clip, for at
// most VoiceFetchHold: other apps' media stays paused meanwhile
// (zf:src/app.rs:8795).
func FetchingNext(s *state.State) bool {
	return s.Voice.Want.Len() > 0 && s.Now().Sub(s.Voice.WantAt) < VoiceFetchHold
}

// mediaArrived plays the clip a run was waiting for once its file is in
// (zf:src/app.rs:4907-4960).
func mediaArrived(s *state.State, a *state.Account, e *abi.EventMedia) {
	v := &s.Voice
	if !a.Active() || e.HasCard || v.Want.Len() == 0 || !bytes.Equal(v.Want.At(0), e.Message) {
		return
	}
	v.Want.Reset()
	if e.Result.Tag != abi.PathResultTagOk || !openIs(a, e.Chat) && !a.IsSplit(e.Chat) {
		return
	}
	v.Next.Reset()
	v.Next.Add(e.Message)
	v.Next.Add(e.Result.Ok.Path)
	s.Touch()
}

// voiceFrame ends a run whose chat was left (zf:src/app.rs:5230).
func voiceFrame(s *state.State) {
	a := s.Acct()
	if s.Voice.Chat != 0 && (a == nil || a.Open != s.Voice.Chat && a.Split != s.Voice.Chat) {
		StopVoiceRun(s)
	}
}
