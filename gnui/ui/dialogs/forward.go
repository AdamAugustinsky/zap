package dialogs

import (
	"slices"
	"strconv"
	"strings"

	"gonativeui/rexui"
	"gonativeui/ui"
	"gonativeui/widget"

	"zapfast/i18n"
	"zapfast/logic"
	"zapfast/model"
	"zapfast/state"
)

// forward is the picker's own view state; the recipients, the limit and the
// review are logic's (state.Forwarding), which opens the dialog
// (logic.OpenForward) and sends the batch (logic.ConfirmForward).
type forward struct {
	search ui.TextBuffer
	cands  []model.ChatRef
	built  bool
	rev    uint32
	gen    uint32
	// the counts as text, made when a count changes
	selN, limN         int
	selText, limitText string
}

func (f *forward) reset() {
	f.search.Reset()
	f.built = false
	f.selText, f.limitText = "", ""
}

func forwardTitle(n int) string {
	if n == 1 {
		return i18n.T("Forward message")
	}
	return i18n.Format(i18n.N("Forward {count} message", "Forward {count} messages", n), "{count}", strconv.Itoa(n))
}

// candidates lists the writable chats matching the search, most recent first.
func (f *forward) candidates(a *state.Account) {
	needle := strings.ToLower(strings.TrimSpace(f.search.String()))
	f.cands = f.cands[:0]
	for _, ref := range a.Model.Chats.Order() {
		c := a.Model.Chats.At(ref)
		if !logic.CanSend(c) {
			continue
		}
		if needle != "" && !strings.Contains(strings.ToLower(string(chatTitle(a, c))), needle) &&
			!strings.Contains(string(model.Phone(c.ID())), needle) {
			continue
		}
		f.cands = append(f.cands, ref)
	}
	slices.SortStableFunc(f.cands, func(x, y model.ChatRef) int {
		return int(a.Model.Chats.At(y).Time() - a.Model.Chats.At(x).Time())
	})
}

func (s *Screen) forward(e *screenEnv, a *state.Account, dl *rexui.Dialog) {
	k, c, f := e.K, e.C(), &s.fwd
	fw := &e.S.Forwarding
	if fw.Reviewing {
		s.forwardReview(e, a, dl)
		return
	}
	sig := rexui.SearchField(k.Kit, i18n.T("Search chats"), &f.search, i18n.T("Search chats"), rexui.FieldOpts{})
	if s.age == 2 {
		c.SetFocus(sig.Key)
	}
	if !f.built || f.rev != f.search.Rev() || f.gen != a.Model.Chats.Gen() {
		f.candidates(a)
		f.built, f.rev, f.gen = true, f.search.Rev(), a.Model.Chats.Gen()
	}
	full := fw.Limit > 1 && fw.Recipients.Len() >= fw.Limit
	if len(f.cands) == 0 {
		para(k, i18n.T("No writable chats found"), rexui.TextCallout, secondary)
	}
	const rowPt = 52
	k.NextHeight(k.Fixed(min(listHeight(e, rowPt), float32(max(len(f.cands), 1))*rowPt+6)))
	v := k.BeginScroll("forward-chats", &s.scroll, widget.ScrollVertical)
	first, last := c.ListRange(v.Inner, len(f.cands), k.Pt(rowPt), 2)
	toggle := model.ChatRef(0)
	for i := first; i < last; i++ {
		ref := f.cands[i]
		chat := a.Model.Chats.At(ref)
		sel := fw.Recipients.Has(chat.ID())
		k.NextGap(12)
		k.BeginContainer(ui.AxisX, ui.CrossCenter, c.Fmt().Str("recipient").Int(int(ref)).String(), ui.Pct(1), ui.Px(k.Pt(rowPt)), 0)
		rexui.Avatar(k.Kit, string(chatTitle(a, chat)), rexui.AvatarOpts{Size: 38, Image: s.avatar(a, chat.ID())})
		k.BeginDisabled(full && !sel)
		k.NextWidth(widget.Fill)
		on := sel
		if rexui.Checkbox(k.Kit, rowName(c, chatTitle(a, chat), int(ref)), &on, rexui.CheckboxOpts{}).Changed {
			toggle = ref
		}
		k.EndDisabled()
		k.EndContainer()
	}
	k.EndScroll(v)
	if toggle != 0 {
		logic.ToggleForwardRecipient(e.S, a, a.Model.Chats.At(toggle).ID())
	}

	// Says why no more chats can be picked, as WhatsApp does.
	switch {
	case fw.Limit == 1:
		para(k, i18n.T("Messages forwarded many times can be sent to one chat at a time"), rexui.TextCallout, secondary)
	case full:
		if f.limitText == "" || f.limN != fw.Limit {
			f.limN = fw.Limit
			f.limitText = i18n.Format(i18n.N("You can forward to up to {count} chat", "You can forward to up to {count} chats", fw.Limit), "{count}", strconv.Itoa(fw.Limit))
		}
		para(k, f.limitText, rexui.TextCallout, secondary)
	}
	n := fw.Recipients.Len()
	if f.selText == "" || f.selN != n {
		f.selN = n
		f.selText = i18n.Format(i18n.N("{count} recipient selected", "{count} recipients selected", n), "{count}", strconv.Itoa(n))
	}
	para(k, f.selText, rexui.TextCallout, secondary)

	// One recipient is sent to at once; several are confirmed first.
	beginActions(e)
	dl.Action(i18n.T("Cancel"), rexui.ButtonDefault)
	label := i18n.T("Forward")
	if n > 1 {
		label = i18n.T("Review recipients")
	}
	k.BeginDisabled(n == 0)
	if dl.Action(label, rexui.ButtonPrimary) {
		if n > 1 {
			logic.ReviewForward(e.S)
			s.open = true
		} else if !logic.ConfirmForward(e.S, a) {
			s.open = true // a destination is no longer writable: back to the choice
		}
	}
	k.EndDisabled()
	endActions(e)
}

func (s *Screen) forwardReview(e *screenEnv, a *state.Account, dl *rexui.Dialog) {
	k := e.K
	fw := &e.S.Forwarding
	rexui.Label(k.Kit, i18n.T("Forward to these chats?"), rexui.LabelOpts{Style: rexui.TextHeadline})
	k.NextHeight(k.Fixed(min(float32(fw.Recipients.Len())*24+8, 260)))
	v := k.BeginScroll("forward-review", &s.scroll, widget.ScrollVertical)
	for i := 0; i < fw.Recipients.Len(); i++ {
		if chat, ok := a.Model.Chats.Get(a.Model.Chats.Find(fw.Recipients.At(i))); ok {
			rexui.LabelBytes(k.Kit, chatTitle(a, chat), rexui.LabelOpts{})
		}
	}
	k.EndScroll(v)
	beginActions(e)
	if dl.Action(i18n.T("Back"), rexui.ButtonDefault) {
		logic.BackToForwardSelection(e.S)
		s.open = true
	}
	if dl.Action(i18n.T("Forward"), rexui.ButtonPrimary) {
		if !logic.ConfirmForward(e.S, a) {
			s.open = true
		}
	}
	endActions(e)
}
