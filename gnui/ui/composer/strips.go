package composer

import (
	"bytes"
	"strings"

	"gonativeui/rexui"
	"gonativeui/theme"
	"gonativeui/ui"

	"zapfast/abi"
	"zapfast/i18n"
	"zapfast/model"
	"zapfast/state"
	ztheme "zapfast/theme"
	"zapfast/ui/screen"
)

// The strips above the field (zf:src/ui/conversation.rs:2055-2205) are in
// the field's colours, rounded like it.
const (
	stripRadius = 16
	stripPadL   = 14
	stripPadR   = 10
	stripPadY   = 6
)

// beginStrip opens a strip: a row in the field's fill.
func beginStrip(e *screen.Env, label string) {
	k := e.K
	c := e.C()
	r := k.Theme()
	c.NextBG(rexui.ComposerLook.Fill.RGBA(r))
	c.NextBorder(rexui.ComposerLook.Border.RGBA(r), r.Lines.Hairline)
	c.NextRadius(e.Pt(stripRadius))
	c.NextAxis(ui.AxisX)
	c.NextCross(ui.CrossCenter)
	c.NextGap(e.Pt(8))
	c.NextPadding(e.Pt(stripPadL), e.Pt(stripPadY), e.Pt(stripPadR), e.Pt(stripPadY))
	c.NextSize(ui.Pct(1), ui.Children(1))
	c.NextRole(ui.RoleGroup)
	c.NextSemName(label)
	c.Push(c.Box(ui.DrawBackground|ui.DrawBorder, label))
}

func endStrip(e *screen.Env) { e.C().Pop() }

// closeButton is a strip's ×.
func (s *Screen) closeButton(e *screen.Env, name string) bool {
	return rexui.IconButton(e.K.Kit, s.ic.xmark, name, rexui.IconButtonOpts{Variant: rexui.IconBorderless, Size: theme.SizeSmall}).Clicked
}

// strips builds a refused voice message's strip, and the edit strip or the
// reply strip, above the field.
func (s *Screen) strips(e *screen.Env, a *state.Account) {
	k := e.K
	accent := rexui.Of(theme.Accent)
	if s.voice.unsentHere(s) {
		s.voice.unsentStrip(s, e)
	}
	switch {
	case len(s.edit) > 0:
		beginStrip(e, "edit strip")
		k.Icon(s.ic.pencil, k.Theme().Colors[theme.Accent])
		rexui.Label(k.Kit, i18n.T("Editing message"), rexui.LabelOpts{Style: rexui.TextSemibold, Color: accent})
		k.Spacer()
		if s.closeButton(e, i18n.T("Stop editing (Esc)")) {
			s.cancelEdit(e)
		}
		endStrip(e)
	case len(s.reply) > 0:
		c := e.C()
		beginStrip(e, "reply strip")
		if s.replyGone {
			// Losing the group's message never turns a private reply into
			// an unquoted send: the core finds the original on Send
			// (zf:src/ui/conversation.rs:1426).
			k.NextWidth(ui.Pct(1))
			rexui.Label(k.Kit, i18n.T("Original group message unavailable"), rexui.LabelOpts{Color: rexui.Of(theme.Label2)})
		} else {
			c.NextBG(k.Theme().Colors[theme.Accent])
			c.NextRadius(e.Pt(2))
			c.NextSize(ui.Px(e.Pt(3)), ui.Px(e.Pt(34)))
			c.Box(ui.DrawBackground, "bar")
			k.NextWidth(ui.Pct(1))
			k.Column("quote")
			rexui.Label(k.Kit, s.replyWho, rexui.LabelOpts{Style: rexui.TextSemibold, Color: accent})
			rexui.LabelBytes(k.Kit, s.replyText, rexui.LabelOpts{Color: rexui.Of(theme.Label2)})
			k.EndColumn()
		}
		if s.closeButton(e, i18n.T("Cancel reply (Esc)")) {
			e.S.Panes.Reply.Reset()
			e.S.Panes.ReplyChat.Reset()
			s.reply, s.replyChat = s.reply[:0], s.replyChat[:0]
			e.S.Touch()
		}
		endStrip(e)
	}
}

// syncReply follows state.Panes.Reply (and ReplyChat, the quoted message's
// chat for a private reply): the strip names whom the reply quotes, and a
// reply started while editing starts a new message (zf:src/app.rs:6636).
func (s *Screen) syncReply(e *screen.Env, a *state.Account) {
	p := &e.S.Panes
	if p.Reply.Len() == 0 {
		s.reply, s.replyChat = s.reply[:0], s.replyChat[:0]
		p.ReplyChat.Reset()
		return
	}
	id := p.Reply.At(0)
	var from []byte
	if p.ReplyChat.Len() > 0 {
		from = p.ReplyChat.At(0)
	}
	// A private reply's quote hides while its chat may not be seen (a
	// locked chat with the folder closed, or one that is gone), and shows
	// again once it may (MM-007).
	hidden := e.S.QuoteHidden(a)
	if bytes.Equal(id, s.reply) && bytes.Equal(from, s.replyChat) && hidden == s.replyHidden {
		return
	}
	s.replyHidden = hidden
	if len(s.edit) > 0 {
		s.cancelEdit(e)
	}
	conv := a.Conversation()
	if from != nil {
		conv = nil
		if !hidden {
			conv = a.Model.Conversation(from)
		}
	}
	i := -1
	if conv != nil {
		i = conv.Find(id)
	}
	if i < 0 && from != nil {
		s.reply = append(s.reply[:0], id...)
		s.replyChat = append(s.replyChat[:0], from...)
		s.replyGone = true
		s.st.Focus(e.K.Kit)
		return
	}
	if i < 0 {
		if conv != nil && conv.Len() > 0 {
			p.Reply.Reset() // the quoted message is not in this chat
			e.S.Touch()
		}
		s.reply = s.reply[:0]
		return
	}
	s.reply = append(s.reply[:0], id...)
	s.replyChat = append(s.replyChat[:0], from...)
	s.replyGone = false
	m := conv.At(i)
	who := i18n.T("You")
	if !m.FromMe() {
		who = string(a.Model.PersonName(m.Sender(), m.SenderName()))
	}
	s.replyWho = i18n.Format(i18n.T("Replying to {who}"), "{who}", who)
	s.replyText = append(s.replyText[:0], summary(m)...)
	s.st.Focus(e.K.Kit)
}

// summary is a message's line in the reply strip: its text, or what it is.
func summary(m model.Message) []byte {
	if t := m.Text(); len(t) > 0 {
		return t
	}
	var what string
	switch m.Kind() {
	case abi.ContentTagImage:
		what = "Photo"
	case abi.ContentTagVideo:
		what = "Video"
	case abi.ContentTagAudio:
		what = "Voice message"
	case abi.ContentTagDocument:
		what = "Document"
	case abi.ContentTagSticker:
		what = "Sticker"
	case abi.ContentTagLocation, abi.ContentTagLiveLocation:
		what = "Location"
	case abi.ContentTagContact:
		what = "Contact"
	case abi.ContentTagPoll:
		what = "Poll"
	default:
		return nil
	}
	return []byte(i18n.T(what))
}

// readOnly is the notice that replaces the composer of a chat we cannot
// write in (zf:src/ui/conversation.rs:1347-1408).
func (s *Screen) readOnly(e *screen.Env, a *state.Account, ch model.Chat) {
	k := e.K
	c := e.C()
	dim := rexui.LabelOpts{Color: rexui.Of(theme.Label2)}
	c.NextAxis(ui.AxisX)
	c.NextJustify(ui.JustifyCenter)
	c.NextCross(ui.CrossCenter)
	c.NextGap(e.Pt(4))
	c.NextSize(ui.Pct(1), ui.Px(e.Pt(36)))
	c.Push(c.Box(0, "read only"))
	switch {
	case ch.Kind() == abi.ChatKindBroadcast && ch.Left():
		rexui.Label(k.Kit, i18n.T("You left this channel"), dim)
	case ch.Kind() == abi.ChatKindBroadcast:
		rexui.Label(k.Kit, i18n.T("Channels are read-only in Zap"), dim)
	case ch.Locked():
		rexui.Label(k.Kit, i18n.T("Locked chats are read-only in Zap"), dim)
	case s.leftGroup(a, ch):
		rexui.Label(k.Kit, i18n.T("You left this group"), dim)
	default:
		// "Only {admins} can send messages": the word is the accent label.
		before, after, _ := strings.Cut(i18n.T("Only {admins} can send messages"), "{admins}")
		if before != "" {
			rexui.Label(k.Kit, strings.TrimRight(before, " "), dim)
		}
		rexui.Label(k.Kit, i18n.T("admins"), rexui.LabelOpts{Style: rexui.TextSemibold, Color: rexui.Of(theme.Accent)})
		if after != "" {
			rexui.Label(k.Kit, strings.TrimLeft(after, " "), dim)
		}
	}
	c.Pop()
}

// leftGroup: we left the group here, or the phone's member list no longer
// names us.
func (s *Screen) leftGroup(a *state.Account, ch model.Chat) bool {
	if ch.Left() {
		return true
	}
	me, lid := a.Model.Me.ID(), a.Model.Me.LID()
	if len(me) == 0 && len(lid) == 0 || !s.rowOK || len(s.row.Participants) == 0 {
		return false
	}
	for _, p := range s.row.Participants {
		if len(p) > 0 && (bytes.Equal(p, me) || bytes.Equal(p, lid)) {
			return false
		}
	}
	return true
}

// hintStrings are the hints bar's texts for one Enter policy and language.
type hintStrings struct {
	ok      bool
	enter   bool
	loc     i18n.Locale
	hint    string
	all     string
	hideTip string
	// shown is the part of hint that fits a row rowW wide: whole hints,
	// the first ones (fit).
	shown string
	rowW  float32
}

// keyLabel is zf:src/ui/keys.rs:668's label on macOS: ⌘ for Ctrl, ⌥ for Alt.
func keyLabel(s string) string {
	var b []byte
	for i := 0; i < len(s); {
		switch {
		case hasAt(s, i, "Ctrl"):
			b, i = append(b, "⌘"...), i+4
		case hasAt(s, i, "Strg"):
			b, i = append(b, "⌘"...), i+4
		case hasAt(s, i, "Alt"):
			b, i = append(b, "⌥"...), i+3
		default:
			b, i = append(b, s[i]), i+1
		}
	}
	return string(b)
}

func hasAt(s string, i int, w string) bool { return len(s)-i >= len(w) && s[i:i+len(w)] == w }

// hintsBar is the shortcut hints under the composer
// (zf:src/ui/conversation.rs:1860-1899).
func (s *Screen) hintsBar(e *screen.Env, rowW float32) {
	h := &s.hints
	enter, loc := e.S.Settings.EnterSends, i18n.Current()
	if !h.ok || h.enter != enter || h.loc != loc {
		h.ok, h.enter, h.loc = true, enter, loc
		if enter {
			h.hint = keyLabel(i18n.T("Enter sends · Shift+Enter for a new line · *bold* _italic_ ~strike~ · Ctrl+V pastes a picture"))
		} else {
			h.hint = keyLabel(i18n.T("Ctrl+Enter sends · *bold* _italic_ ~strike~ · Ctrl+V pastes a picture"))
		}
		h.all = i18n.Format(i18n.T("All shortcuts ({})"), "{}", keyLabel("Ctrl+/"))
		h.hideTip = i18n.T("Hide shortcut hints (bring them back from Keyboard shortcuts)")
		h.rowW = -1
	}
	k := e.K
	if h.rowW != rowW {
		h.rowW = rowW
		h.shown = s.fitHints(e, rowW)
	}
	k.NextGap(hintsGap)
	k.NextCross(ui.CrossCenter)
	k.NextWidth(ui.Pct(1))
	k.Row("hints")
	if s.closeButton(e, h.hideTip) {
		e.S.Settings.ShowShortcutHints = false
		e.S.Touch()
	}
	if len(h.shown) > 0 { // in Rex's metadata face, as its own hint line
		rexui.Label(k.Kit, h.shown, rexui.LabelOpts{Style: rexui.TextSubheadline, Color: rexui.Of(theme.Label2), MetaPt: ztheme.T.MetaPt})
	}
	k.Spacer()
	if rexui.IconButton(k.Kit, s.ic.keyboard, h.all, rexui.IconButtonOpts{Variant: rexui.IconBorderless, Size: theme.SizeSmall}).Clicked {
		e.S.OpenDialog(state.Dialog{Kind: state.DialogShortcuts})
	}
	k.EndRow()
}

// hintsGap is the space between the hints row's parts.
const hintsGap = 4

// hintsSep divides the hints in the line.
const hintsSep = " · "

// fitHints is the hints that fit a row rowW wide beside its two buttons: all
// of them, or whole ones from the start with the last dropped while they do
// not fit, or none. The row is the composer's own width, which a side pane
// narrows; upstream's line is not fitted at all (egui clips it at the
// panel's edge: zf:src/ui/conversation.rs:1860-1899), and a hint cut in
// the middle of a word, or a button drawn under the pane, is not what
// anyone wants to see.
func (s *Screen) fitHints(e *screen.Env, rowW float32) string {
	k := e.K
	r := k.Theme()
	xw, _ := k.IconSize(s.ic.xmark)
	kw, _ := k.IconSize(s.ic.keyboard)
	icon := theme.Snap(r.Sizes[theme.SizeSmall].Icon)
	room := rowW - max(icon, xw) - max(icon, kw) - 3*hintsGap
	font := ztheme.Meta(k, ztheme.T.MetaPt) // the hints' size, the metadata's
	h := s.hints.hint
	for len(h) > 0 && k.TextWidth(font, h) > room {
		i := strings.LastIndex(h, hintsSep)
		if i < 0 {
			return ""
		}
		h = h[:i]
	}
	return h
}
