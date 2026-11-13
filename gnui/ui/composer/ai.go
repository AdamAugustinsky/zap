package composer

import (
	"strconv"
	"strings"
	"time"

	"gonativeui/rexui"
	"gonativeui/theme"
	"gonativeui/ui"

	"zapfast/i18n"
	"zapfast/state"
	"zapfast/ui/screen"
)

// A reply an agent wrote for a chat that asks the person first (state/ai.go,
// package agent) waits in a strip above the field: its words, and Send,
// Edit and Discard. Nothing of it has left the app. Send sends it as it
// stands, as the person's own message; Edit moves the words into the field,
// where what goes out is theirs to change and send; Discard drops it. The
// oldest one shows, and the strip says how many more wait.

const (
	// proposalShown bounds the words the strip shows, in bytes and lines. A
	// longer reply is not offered Send: the person has not read its end, so
	// Edit puts all of it in the field first.
	proposalShown = 600
	proposalLines = 8
	// proposalSettle is how long a reply has been in the strip before its
	// buttons answer: the second click of a double-click, or a held Return,
	// does not answer the next reply, which took the first one's place
	// unread.
	proposalSettle = 700 * time.Millisecond
)

// shownProposal is the part of text the strip shows, and whether that is
// all of it.
func shownProposal(text string) (string, bool) {
	n, lines := len(text), 1
	for i := 0; i < len(text); i++ {
		if text[i] != '\n' {
			continue
		}
		if lines++; lines > proposalLines {
			n = i
			break
		}
	}
	if n > proposalShown {
		n = proposalShown
		for n > 0 && text[n]&0xC0 == 0x80 { // not in the middle of a character
			n--
		}
	}
	return text[:n], n == len(text)
}

// proposal builds the strip of the reply that waits for this chat, if one
// does.
func (s *Screen) proposal(e *screen.Env, a *state.Account) {
	p, n, ok := e.S.AIProposal(a, s.chat)
	if !ok {
		s.aiShown = 0
		return
	}
	now := e.S.Now()
	if p.ID != s.aiShown {
		s.aiShown, s.aiSince = p.ID, now
	}
	k := e.K
	th := k.Theme()
	text, whole := shownProposal(strings.TrimSpace(p.Text))

	beginStrip(e, "agent reply strip")
	k.NextWidth(ui.Pct(1))
	k.NextGap(e.Pt(4))
	k.Column("agent reply")

	k.NextCross(ui.CrossCenter)
	k.NextGap(e.Pt(4))
	k.NextWidth(ui.Pct(1))
	k.Row("title")
	k.Icon(s.ic.sparkles, th.Colors[theme.Accent])
	rexui.Label(k.Kit, i18n.T("Your agent wrote this reply"), rexui.LabelOpts{Style: rexui.TextSemibold, Color: rexui.Of(theme.Accent)})
	k.Spacer()
	if n > 1 {
		more := i18n.Format(i18n.T("{} more waiting"), "{}", strconv.Itoa(n-1))
		rexui.Label(k.Kit, more, rexui.LabelOpts{Style: rexui.TextCaption, Color: rexui.Of(theme.Label2)})
	}
	k.EndRow()

	rexui.Label(k.Kit, text, rexui.LabelOpts{Wrap: true})
	if !whole {
		rexui.Label(k.Kit, i18n.T("This reply is longer than what is shown. Edit puts all of it in the message field."),
			rexui.LabelOpts{Style: rexui.TextCaption, Color: rexui.Of(theme.Label2), Wrap: true})
	}

	k.NextCross(ui.CrossCenter)
	k.NextGap(e.Pt(8))
	k.NextWidth(ui.Pct(1))
	k.Row("answers")
	rexui.Label(k.Kit, i18n.T("Not sent yet"), rexui.LabelOpts{Style: rexui.TextCaption, Color: rexui.Of(theme.Label2)})
	k.Spacer()
	discard := rexui.Button(k.Kit, i18n.T("Discard"), rexui.ButtonOpts{Size: theme.SizeSmall})
	edit := rexui.Button(k.Kit, i18n.T("Edit"), rexui.ButtonOpts{Size: theme.SizeSmall})
	send := false
	if whole {
		sig := rexui.Button(k.Kit, i18n.T("Send"), rexui.ButtonOpts{Variant: rexui.ButtonPrimary, Size: theme.SizeSmall})
		e.TabStop(screen.StopSend+1, sig.ID)
		send = sig.Clicked
	}
	e.TabStop(screen.StopSend+2, edit.ID)
	e.TabStop(screen.StopSend+3, discard.ID)
	k.EndRow()

	k.EndColumn()
	endStrip(e)

	if now.Sub(s.aiSince) < proposalSettle {
		return // not looked at yet
	}
	switch {
	case send:
		e.S.AIResolve(p.ID, true)
	case discard.Clicked:
		e.S.AIResolve(p.ID, false)
	case edit.Clicked:
		s.editProposal(e, a, p.ID)
	}
}

// editProposal moves a waiting reply's words into the field, at the caret,
// with the message it answers quoted: from here it is the person's own
// message, and nothing is sent until they send it.
func (s *Screen) editProposal(e *screen.Env, a *state.Account, id uint64) {
	p, ok := e.S.AITake(id)
	if !ok {
		return
	}
	if len(s.edit) > 0 {
		s.cancelEdit(e) // the agent's words are a new message, not the edit of an old one
	}
	if p.Quoting != "" && e.S.Panes.Reply.Len() == 0 {
		e.S.Panes.Reply.Add([]byte(p.Quoting))
	}
	s.insert(e, a, []byte(strings.TrimSpace(p.Text)))
	e.S.Touch()
}
