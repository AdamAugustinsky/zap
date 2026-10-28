package dialogs

import (
	"gonativeui/rexui"
	"gonativeui/theme"
	"gonativeui/ui"
	"gonativeui/widget"
	"gonativeui/widget/base"

	"zapfast/abi"
	"zapfast/i18n"
	"zapfast/logic"
	"zapfast/model"
	"zapfast/state"
)

// interactiveList shows the choices of a list message; choosing one replies
// with it (Command.ReplyInteractive) and closes the dialog.
func (s *Screen) interactiveList(e *screenEnv, a *state.Account, dl *rexui.Dialog) {
	k, c := e.K, e.C()
	d := &e.S.Dialog
	m := &s.msg
	var btn *abi.InteractiveButton
	if ok, card := s.loadMessage(a, d, m); ok && m.Content.Tag == abi.ContentTagInteractive && m.Content.Interactive.HasCard {
		b := m.Content.Interactive.Card.Buttons
		if card >= 0 { // a carousel card answers for itself
			b = nil
			if cards := m.Content.Interactive.Card.Carousel; card < len(cards) {
				b = cards[card].Buttons
			}
		}
		if d.N >= 0 && d.N < len(b) {
			btn = &b[d.N]
		}
	}
	if btn == nil || btn.Action.Tag != abi.InteractiveActionTagSelect {
		para(k, i18n.T("This list is no longer available."), rexui.TextBody, secondary)
		beginActions(e)
		dl.Action(i18n.T("Close"), rexui.ButtonPrimary)
		endActions(e)
		return
	}
	s.title = string(btn.Label)
	chat, ok := a.Model.Chats.Get(d.Chat)
	available := !m.FromMe && !m.Edited && ok && logic.CanSend(chat)
	pending := ok && d.IDs.Len() > 0 && a.Model.InteractiveSending(chat.ID(), d.IDs.At(0))
	enabled := available && a.Model.Link.Connected() && !pending
	if !enabled {
		reason := i18n.T("Connect to WhatsApp to reply")
		switch {
		case !available:
			reason = i18n.T("This list can no longer receive replies.")
		case pending:
			reason = i18n.T("Sending reply…")
		}
		para(k, reason, rexui.TextCallout, secondary)
	}
	_, h := winPt(e)
	k.NextHeight(k.Fixed(min(max(h-200, 100), 420)))
	v := k.BeginScroll("interactive-list", &s.scroll, widget.ScrollVertical)
	k.BeginDisabled(!enabled)
	options := btn.Action.Select.Options
	var section []byte
	choice := -1
	for i := range options {
		o := &options[i]
		if string(o.Section) != string(section) {
			section = o.Section
			if len(section) > 0 {
				rexui.LabelBytes(k.Kit, section, rexui.LabelOpts{Style: rexui.TextSemibold, Color: secondary})
			}
		}
		if s.option(e, c.Fmt().Str(model.S(o.Title)).Str("###option").Int(i).String(), o) {
			choice = i
		}
	}
	k.EndDisabled()
	k.EndScroll(v)
	beginActions(e)
	dl.Action(i18n.T("Close"), rexui.ButtonDefault)
	endActions(e)
	if choice < 0 {
		return
	}
	cmd := a.Cmd(abi.CommandTagReplyInteractive)
	ri := &cmd.ReplyInteractive
	ri.Chat, ri.Message, ri.Button = chat.ID(), d.IDs.At(0), uint64(d.N)
	ri.Choice, ri.HasChoice = uint64(choice), true
	a.Send(cmd)
	s.open = false
}

// option is one choice: its title, with its description under it, pressed as
// one button.
func (s *Screen) option(e *screenEnv, label string, o *abi.InteractiveOption) bool {
	k, c := e.K, e.C()
	p := k.PressState(label, base.HoverOn)
	c.NextAxis(ui.AxisY)
	k.NextPadding(10, 8, 10, 8)
	k.NextGap(2)
	c.NextRadius(k.Pt(8))
	if p.Hot > 0 {
		c.NextBG(theme.WithAlpha(k.Theme().Colors[theme.Fill1], p.Hot))
	}
	k.BeginPress(&p, ui.RoleButton, ui.Pct(1), ui.Children(1), ui.DrawBackground, nil)
	rexui.LabelBytes(k.Kit, o.Title, rexui.LabelOpts{Style: rexui.TextSemibold, Wrap: true})
	if len(o.Description) > 0 {
		rexui.LabelBytes(k.Kit, o.Description, rexui.LabelOpts{Style: rexui.TextCallout, Color: secondary, Wrap: true})
	}
	return k.EndPress(&p).Clicked
}
