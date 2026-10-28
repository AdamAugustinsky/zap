package dialogs

import (
	"strings"

	"gonativeui/rexui"
	"gonativeui/ui"
	"gonativeui/widget"
	"gonativeui/widget/base"

	"zapfast/abi"
	"zapfast/i18n"
	"zapfast/state"
)

// location is the Send location dialog's state (zf:src/ui/locations.rs).
type location struct{ text ui.TextBuffer }

// sendLocation takes the spot as text and shows what it read before anything
// goes out, so a pair read the wrong way round is seen here, not in someone
// else's chat.
func (s *Screen) sendLocation(e *screenEnv, a *state.Account, dl *rexui.Dialog) {
	k, c, l := e.K, e.C(), &s.loc
	f := rexui.TextField(k.Kit, i18n.T("Paste a map link or coordinates"), &l.text, i18n.T("Paste a map link or coordinates"), rexui.FieldOpts{})
	if s.age == 2 {
		c.SetFocus(f.Key)
	}
	typed := strings.TrimSpace(l.text.String())
	sp, ok := ParseSpot(typed)
	switch {
	case ok:
		rexui.Label(k.Kit, sp.Text(), rexui.LabelOpts{})
		if rexui.Link(k.Kit, i18n.T("Open in a map"), rexui.LinkOpts{Style: rexui.TextCallout}).Clicked {
			openURL(e.S, MapURL(sp))
		}
	case typed == "":
	case IsShortLink(typed):
		// A short link names its spot only on the server it redirects
		// through, and ZapFast asks no server, so it asks for the full link.
		para(k, i18n.T("Short links only open online. Paste the full map link or the coordinates."), rexui.TextCaption, secondary)
	default:
		para(k, i18n.T("That is not a location."), rexui.TextCaption, secondary)
	}
	beginActions(e)
	dl.Action(i18n.T("Cancel"), rexui.ButtonDefault)
	k.BeginDisabled(!ok || !a.Model.Link.Connected())
	if dl.Action(i18n.T("Send"), rexui.ButtonPrimary) || (f.Submitted && ok && a.Model.Link.Connected()) {
		s.sendSpot(e, a, sp)
		s.open = false
	}
	k.EndDisabled()
	endActions(e)
}

func (s *Screen) sendSpot(e *screenEnv, a *state.Account, sp Spot) {
	chat, ok := a.Model.Chats.Get(e.S.Dialog.Chat)
	if !ok {
		return
	}
	cmd := a.Cmd(abi.CommandTagSendLocation)
	sl := &cmd.SendLocation
	sl.Chat, sl.Latitude, sl.Longitude = chat.ID(), sp.Latitude, sp.Longitude
	if r := &e.S.Panes.Reply; r.Len() > 0 {
		sl.Quoting, sl.HasQuoting = abi.ReplyTarget{ID: r.At(0)}, true
	}
	a.Send(cmd)
}

var _ = widget.Fill
var _ base.Signal
