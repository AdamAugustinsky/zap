package wa

import (
	"context"
	"log/slog"
	"time"

	"go.mau.fi/whatsmeow/types"

	"zapfast/abi"
	"zapfast/core"
	"zapfast/gocore"
	"zapfast/gocore/calls"
	"zapfast/gocore/out"
	"zapfast/gocore/store"
)

// Calls (WP7): the 1:1 call signaling of gocore/calls over the client,
// with the media of the Go call engine. Without media every call command but
// LoadCallLog is refused, and an offer is refused to its caller. Group
// calls and call links are not bridged: StartCall to a group fails as the
// worker fails it, a group's offer is refused, and JoinCallLink is refused.

// callCommands are the schema's call commands, which gocore/calls answers.
var callCommands = map[abi.CommandTag]bool{
	abi.CommandTagStartCall: true, abi.CommandTagJoinCallLink: true, abi.CommandTagCancelCall: true,
	abi.CommandTagAnswerCall: true, abi.CommandTagDeclineCall: true, abi.CommandTagHangupCall: true,
	abi.CommandTagSetCallMuted: true, abi.CommandTagSetCallCamera: true, abi.CommandTagSetCallMicrophone: true,
	abi.CommandTagSetCallSpeaker: true, abi.CommandTagSetCallCameraDevice: true, abi.CommandTagRefreshCallDevices: true,
	abi.CommandTagLoadCallLog: true,
}

const noCallLinks = "Call links are not available in Zap yet"

// acceptCall decides a call command, on the caller's goroutine.
func (e *Engine) acceptCall(cmd *abi.Command) error {
	switch {
	case cmd.Tag == abi.CommandTagLoadCallLog:
		return nil
	case cmd.Tag == abi.CommandTagJoinCallLink:
		return gocore.Refuse(noCallLinks)
	case e.calls == nil || !e.calls.Available():
		return gocore.Refuse(calls.NotAvailable)
	}
	if err := e.calls.Accept(cmd); err != nil {
		return gocore.Refuse(err.Error())
	}
	return nil
}

// openCalls makes the account's calls, before any command: the media is
// made here, once.
func (e *Engine) openCalls() {
	newMedia := e.opts.CallMedia
	if newMedia == nil {
		newMedia = systemMedia
	}
	media, err := newMedia()
	if err != nil {
		slog.Info("zapfast wa: calls are off", "err", err)
		media = nil
	}
	e.calls = calls.New(calls.Host{
		Context: e.h.Context(),
		Post:    e.post,
		Go: func(name string, f func(ctx context.Context)) bool {
			e.job(func(ctx context.Context) func() { f(ctx); return nil })
			return true
		},
		Background: e.h.Go,
		Emit:       func(ev *abi.Event) { e.out.Emit(ev) },
		Chat:       func(user types.JID) string { return e.canonical(user) },
		File:       e.fileCall,
		Error:      e.emitError,
	}, media, calls.DefaultTiming)
}

// systemMedia is the Go call engine on this system's devices.
func systemMedia() (calls.Media, error) { return calls.NewSystemMedia(), nil }

// callStatusName is a call's outcome as the archive files it
// (store.Call.Status), the inverse of callStatus.
func callStatusName(s abi.CallStatus) string {
	switch s {
	case abi.CallStatusAnsweredElsewhere:
		return "answered-elsewhere"
	case abi.CallStatusMissed:
		return "missed"
	case abi.CallStatusDeclined:
		return "declined"
	case abi.CallStatusDeclinedElsewhere:
		return "declined-elsewhere"
	case abi.CallStatusBusy:
		return "busy"
	case abi.CallStatusFailed:
		return "failed"
	case abi.CallStatusNoAnswer:
		return "no-answer"
	case abi.CallStatusConnectionLost:
		return "connection-lost"
	}
	return "answered"
}

// fileCall files a finished call among its chat's messages, unless the
// chat already has an entry for it (the phone's own call-log message), as
// the worker's file_call_entry does. The entry is local: never sent, never
// unread.
func (e *Engine) fileCall(c calls.Entry) {
	if c.Chat == "" || c.CallID == "" || c.Time <= 0 {
		return
	}
	id := store.CallEntryPrefix + c.CallID
	if near, err := e.arch.CallEntryNear(c.Chat, c.Time, c.FromMe, id); err != nil || near {
		if err != nil {
			slog.Warn("zapfast wa: the chat's call entries could not be read", "err", err)
		}
		return
	}
	e.ensureChat(c.Chat, "")
	sender := c.Chat
	if c.FromMe {
		sender = e.me()
	}
	m := store.Message{ID: id, ChatID: c.Chat, Sender: sender, FromMe: c.FromMe, Timestamp: c.Time, Kind: store.KindCall,
		Call: &store.Call{Video: c.Video, Status: callStatusName(c.Status), Seconds: c.Seconds}}
	if err := e.arch.SaveMessages([]store.Message{m}); err != nil {
		slog.Warn("zapfast wa: a call entry could not be filed", "err", err)
		return
	}
	if err := e.arch.UpdateChat(c.Chat, func(ch *store.Chat) {
		if c.Time >= ch.LastActivity {
			ch.LastActivity, ch.Preview = c.Time, summary(m)
		}
	}); err != nil {
		slog.Warn("zapfast wa: a call entry's chat was not updated", "err", err)
	}
	if stored, err := e.arch.Message(c.Chat, id); err == nil {
		e.emitMessages(c.Chat, []store.Message{stored}, false, false)
	}
	e.emitChat(c.Chat)
}

// emitCallLog hands the Calls page the account's calls, newest first.
func (e *Engine) emitCallLog() {
	list, err := e.arch.CallLog(store.CallLogLimit)
	if err != nil {
		slog.Warn("zapfast wa: the call log could not be read", "err", err)
		list = nil
	}
	cv := e.conv()
	out.Chunks(e.out, list, cv.message, func(batch uint32, last bool, chunk []abi.Message) *abi.Event {
		return &abi.Event{Tag: abi.EventTagCallLog, CallLog: abi.EventCallLog{Batch: batch, Last: last, Messages: chunk}}
	})
}

// callCommand handles a call command on the engine's goroutine.
func (e *Engine) callCommand(cmd *gocore.Command) {
	if cmd.Tag == abi.CommandTagLoadCallLog {
		e.emitCallLog()
		return
	}
	e.calls.Command(&cmd.Command)
}

// callSignal routes a whatsmeow call event; false when evt is none.
func (e *Engine) callSignal(evt any) bool {
	s, ok := calls.FromEvent(evt)
	if ok {
		e.calls.Signal(s)
	}
	return ok
}

// Frame and ReleaseFrame are the call's video frames (core.Frames), from
// the UI's goroutine.
func (e *Engine) Frame(stream uint32) (core.Frame, bool) {
	if e.calls == nil {
		return core.Frame{}, false
	}
	return e.calls.Frame(stream)
}

func (e *Engine) ReleaseFrame(stream uint32) {
	if e.calls != nil {
		e.calls.ReleaseFrame(stream)
	}
}

// hangUpCalls ends the call up as the engine closes, while the socket is
// still there.
func (e *Engine) hangUpCalls() {
	if e.calls == nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	e.calls.Shutdown(ctx)
}
