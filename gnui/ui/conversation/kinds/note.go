package kinds

import (
	"strings"
	"time"

	"gonativeui/icon"
	"gonativeui/theme"
	"gonativeui/ui"
	"gonativeui/widget/base"

	"zapfast/abi"
	"zapfast/format"
	"zapfast/i18n"
	"zapfast/model"
	"zapfast/ui/screen"
)

// The one-row blocks: deleted messages, phone-only content, unsupported
// content and call-log entries (zf:src/ui/conversation.rs, Content::Revoked,
// PhoneOnly, Unsupported, Call; mirrored_row).

const (
	noteIconW   = 20.0 // the icon's column, points
	noteGap     = 8.0
	noteMaxLine = 4
)

// ph is one placeholder of a template: {name} and its value.
type ph struct {
	name string
	n    int
	s    []byte
	str  bool
}

func phInt(name string, n int) ph      { return ph{name: name, n: n} }
func phBytes(name string, s []byte) ph { return ph{name: name, s: s, str: true} }

// expand appends tmpl to f with each {name} replaced by its value.
func expand(f *ui.Fmt, tmpl string, phs ...ph) *ui.Fmt {
	for len(tmpl) > 0 {
		i := strings.IndexByte(tmpl, '{')
		if i < 0 {
			return f.Str(tmpl)
		}
		f.Str(tmpl[:i])
		tmpl = tmpl[i:]
		j := strings.IndexByte(tmpl, '}')
		if j < 0 {
			return f.Str(tmpl)
		}
		name := tmpl[1:j]
		done := false
		for k := range phs {
			if phs[k].name == name {
				if phs[k].str {
					f.Str(model.S(phs[k].s))
				} else {
					f.Int(phs[k].n)
				}
				done = true
				break
			}
		}
		if !done {
			f.Str(tmpl[:j+1])
		}
		tmpl = tmpl[j+1:]
	}
	return f
}

// clock appends a local time of day, "14:05" or "2:05 PM"
// (format.AppendClock).
func clock(dst []byte, sec int64) []byte { return format.AppendClock(dst, time.Unix(sec, 0)) }

// note is the content of a one-row block: an icon and up to two text lines.
type note struct {
	icon    icon.ID
	iconCol uint32
	title   []byte // medium when there is a detail, else regular
	detail  []byte
	titleF  fontID
	titleC  uint32
}

// noteOf fills n for the message, building its text in the frame's arena.
func (b *blk) noteOf() (n note) {
	c := b.c
	switch b.m.Kind() {
	case abi.ContentTagRevoked:
		n = note{icon: icBan, iconCol: b.faint(), title: base.StrBytes(i18n.T("This message was deleted")), titleF: fBody, titleC: b.dim()}
	case abi.ContentTagUnsupported:
		n = note{icon: icAlert, iconCol: b.faint(), titleF: fBody, titleC: b.dim()}
		n.title = expand(c.Fmt(), i18n.T("Unsupported: {what}"), phBytes("what", b.m.Text())).Bytes()
	case abi.ContentTagWaiting:
		n = note{icon: icClock, iconCol: b.faint(), title: base.StrBytes(i18n.T("Waiting for this message")), titleF: fBody, titleC: b.dim(),
			detail: base.StrBytes(i18n.T("This may take a while. Open WhatsApp on your phone."))}
	case abi.ContentTagPhoneOnly:
		d := b.decoded()
		p := &d.Content.PhoneOnly
		if p.LiveLocation {
			return note{icon: icPin, iconCol: b.accent(), title: base.StrBytes(i18n.T("Live location")), titleF: fBold, titleC: b.ink(),
				detail: base.StrBytes(i18n.T("Open WhatsApp on your phone to follow it."))}
		}
		var s string
		switch {
		case p.HasOnce && p.Once == abi.OnceMediaPhoto:
			s = i18n.T("View once photo. For your privacy, it opens only on your phone.")
		case p.HasOnce && p.Once == abi.OnceMediaVideo:
			s = i18n.T("View once video. For your privacy, it opens only on your phone.")
		case p.HasOnce && p.Once == abi.OnceMediaVoice:
			s = i18n.T("View once voice message. For your privacy, it opens only on your phone.")
		case p.HasOnce && p.Once == abi.OnceMediaAudio:
			s = i18n.T("View once audio. For your privacy, it opens only on your phone.")
		case p.ViewOnce:
			s = i18n.T("View once message. For your privacy, it opens only on your phone.")
		default:
			s = i18n.T("This message can only be seen on your phone.")
		}
		n = note{icon: icPhone, iconCol: b.faint(), title: base.StrBytes(s), titleF: fBody, titleC: b.dim()}
	case abi.ContentTagCall:
		d := b.decoded()
		cl := &d.Content.Call
		missed := cl.Status == abi.CallStatusMissed && !b.own
		n.titleF, n.titleC = fBold, b.ink()
		n.icon = callIcon(cl.Status, cl.Video, b.own)
		if cl.Status != abi.CallStatusAnswered && cl.Status != abi.CallStatusAnsweredElsewhere {
			n.iconCol = b.col(theme.Red)
		} else {
			n.iconCol = b.accent()
		}
		n.title = base.StrBytes(callTitle(missed, cl.Group, cl.Video))
		n.detail = b.callDetail(cl)
	}
	return n
}

func callIcon(st abi.CallStatus, video, own bool) icon.ID {
	switch st {
	case abi.CallStatusMissed, abi.CallStatusNoAnswer:
		return icCallMissed
	case abi.CallStatusDeclined, abi.CallStatusDeclinedElsewhere, abi.CallStatusBusy, abi.CallStatusFailed, abi.CallStatusConnectionLost:
		return icCallOff
	}
	switch {
	case video:
		return icVideo
	case own:
		return icCallOut
	}
	return icCallIn
}

func callTitle(missed, group, video bool) string {
	switch {
	case missed && !group && !video:
		return i18n.T("Missed voice call")
	case missed && !group && video:
		return i18n.T("Missed video call")
	case missed && group && !video:
		return i18n.T("Missed group voice call")
	case missed && group && video:
		return i18n.T("Missed group video call")
	case !group && !video:
		return i18n.T("Voice call")
	case !group && video:
		return i18n.T("Video call")
	case group && !video:
		return i18n.T("Group voice call")
	}
	return i18n.T("Group video call")
}

// callDetail is how the call went: its length when it was up, else what
// became of it (Entry::detail).
func (b *blk) callDetail(cl *abi.ContentCall) []byte {
	var s string
	switch cl.Status {
	case abi.CallStatusAnswered:
		if cl.Duration == 0 {
			return nil
		}
		return callLength(b.c.Fmt(), cl.Duration).Bytes()
	case abi.CallStatusMissed:
		return nil
	case abi.CallStatusAnsweredElsewhere:
		s = i18n.T("Answered on another device")
	case abi.CallStatusDeclined:
		s = i18n.T("Declined")
	case abi.CallStatusDeclinedElsewhere:
		s = i18n.T("Declined on another device")
	case abi.CallStatusBusy:
		s = i18n.T("Busy")
	case abi.CallStatusFailed:
		s = i18n.T("Could not connect")
	case abi.CallStatusNoAnswer:
		s = i18n.T("No answer")
	case abi.CallStatusConnectionLost:
		s = i18n.T("Connection lost")
	}
	return base.StrBytes(s)
}

// callLength appends a finished call's length in words
// (format.AppendCallLength).
func callLength(f *ui.Fmt, seconds uint64) *ui.Fmt {
	var tb [64]byte
	return f.Str(model.S(format.AppendCallLength(tb[:0], i18n.Current(), seconds)))
}

// noteSize: the icon's column and the text beside it.
func (b *blk) noteSize(maxW float32) (w, h float32) {
	n := b.noteOf()
	col := b.pt(noteIconW + noteGap)
	avail := max(maxW-col, 1)
	tw, th, _ := b.wrapSize(n.title, n.titleF, avail, noteMaxLine)
	dw, dh, _ := b.wrapSize(n.detail, fSub, avail, noteMaxLine)
	h = max(th+dh, b.lineH(fBody))
	if dh > 0 {
		h += b.pt(1)
	}
	return col + max(tw, dw), max(h, b.pt(20))
}

func (b *blk) noteBuild(r screen.Rect) {
	n := b.noteOf()
	col := b.pt(noteIconW + noteGap)
	avail := max(r.W-col, 1)
	_, th, _ := b.wrapSize(n.title, n.titleF, avail, noteMaxLine)
	_, dh, _ := b.wrapSize(n.detail, fSub, avail, noteMaxLine)
	total := th + dh
	if dh > 0 {
		total += b.pt(1)
	}
	y0 := max((r.H-total)/2, 0)
	b.icon(n.icon, n.iconCol, 0, y0, b.pt(noteIconW), b.lineH(n.titleF))
	b.wrapped(1, n.title, n.titleF, n.titleC, col, y0, avail, th, noteMaxLine)
	if dh > 0 {
		b.wrapped(2, n.detail, fSub, b.dim(), col, y0+th+b.pt(1), avail, dh, noteMaxLine)
	}
}
