package chatlist

import (
	"gonativeui/ui"

	"zapfast/i18n"
	"zapfast/model"
	"zapfast/state"
	"zapfast/ui/screen"
)

// The chat menu's AI access (state/ai.go): what an AI agent the person
// connected may do in the chat, chosen without leaving the list. What needs
// typing, the instructions, is in the chat's info.

// aiItems are the AI access submenu of chat ch.
func aiItems(a *state.Account, ch model.Chat) []ui.MenuItem {
	g, ok := a.AIGrant(ch.ID())
	items := []ui.MenuItem{
		{ID: miAIOff, Title: i18n.T("Off")},
		{ID: miAIRead, Title: i18n.T("Read only")},
		{ID: miAIWrite, Title: i18n.T("Read and reply")},
		ui.MenuSeparator,
		{ID: miAIAsk, Title: i18n.T("Ask me before each reply"), Disabled: !ok || !g.Write},
		{ID: miAIWake, Title: i18n.T("Start my agent when a message arrives"), Disabled: !ok},
		ui.MenuSeparator,
		{ID: miAIMore, Title: i18n.T("Instructions…")},
	}
	for i, on := range [...]bool{!ok, ok && !g.Write, ok && g.Write, false, ok && g.Write && !g.Auto, ok && g.Wake} {
		if on {
			items[i].Mark = ui.MarkOn
		}
	}
	return items
}

// aiAct does what the AI access submenu's item chose, and reports whether
// item was one of its.
func aiAct(e *screen.Env, a *state.Account, ref model.ChatRef, id []byte, item int) bool {
	g, ok := a.AIGrant(id)
	switch item {
	case miAIOff:
		a.ClearAIGrant(id)
	case miAIRead:
		g.Write = false
		a.SetAIGrant(id, g)
	case miAIWrite:
		g.Write = true
		a.SetAIGrant(id, g)
	case miAIAsk:
		if ok && g.Write {
			g.Auto = !g.Auto
			a.SetAIGrant(id, g)
		}
	case miAIWake:
		if ok {
			g.Wake = !g.Wake
			a.SetAIGrant(id, g)
		}
	case miAIMore:
		a.OpenChat(ref) // the panel sits beside the conversation
		e.S.Panes.SetInfo(id)
	default:
		return false
	}
	return true
}
