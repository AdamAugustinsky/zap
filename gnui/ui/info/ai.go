package info

import (
	"time"

	"gonativeui/rexui"
	"gonativeui/theme"
	"gonativeui/ui"
	"gonativeui/widget"
	"gonativeui/widget/base"

	"zapfast/agent"
	"zapfast/format"
	"zapfast/i18n"
	"zapfast/state"
	"zapfast/ui/screen"
)

// aiAccess is the chat's AI access (state/ai.go): whether an AI agent the
// person connected may read this chat, or read and answer it; whether an
// answer waits for them; whether a message arriving starts their agent; and
// what they asked it to do here. It folds under a row, as the boost does,
// and the row says what the chat has. A locked chat has no such row: it is
// never given to an agent.
type aiAccess struct {
	open  bool
	brief ui.TextBuffer
	// loaded is the brief the field was last filled from, so the person's
	// typing is not overwritten and another chat's brief is not kept.
	loaded  string
	has     bool
	focused bool // the field had the keyboard last frame
	levels  [3]string
}

func (x *aiAccess) init() {
	x.brief.Max = agent.BriefMax
	x.brief.Reserve(agent.BriefMax)
}

// reset is a new chat in the panel: folded unless the chat has access.
func (x *aiAccess) reset(a *state.Account, jid []byte) {
	g, ok := a.AIGrant(jid)
	x.open, x.has = ok, false
	x.fill(g)
}

func (x *aiAccess) fill(g agent.Grant) {
	x.loaded, x.has = g.Brief, true
	x.brief.SetString(g.Brief)
}

// aiSummary is what the row says of a grant.
func aiSummary(g agent.Grant, ok bool) string {
	switch {
	case !ok:
		return i18n.T("Off")
	case !g.Write:
		return i18n.T("Read only")
	case g.Auto:
		return i18n.T("Read and reply")
	}
	return i18n.T("Reply with approval")
}

func (s *Screen) ai(e *screen.Env, a *state.Account) {
	k := e.K
	c := e.C()
	th := k.Theme()
	x := &s.aiAcc
	jid := s.jid[:s.jidN]
	g, ok := a.AIGrant(jid)
	if !x.has || (g.Brief != x.loaded && !x.focused) {
		x.fill(g) // changed elsewhere (the chat's menu, Settings)
	}

	k.NextGap(8)
	k.BeginContainer(ui.AxisY, ui.CrossStart, "ai access", ui.Pct(1), ui.Children(1), 0)
	p := k.PressState(i18n.T("AI access"), base.HoverOn)
	c.NextAxis(ui.AxisX)
	c.NextCross(ui.CrossCenter)
	c.NextGap(k.Pt(4))
	c.NextRadius(k.Pt(6))
	if x.open {
		c.NextSemState(ui.SemExpanded)
	}
	k.BeginPress(&p, ui.RoleButton, ui.Pct(1), k.Fixed(36), 0, nil)
	k.Icon(s.ic.sparkles, th.Colors[theme.Label2])
	k.NextWidth(widget.Fill)
	c.NextSize(ui.Pct(1), ui.Px(k.Pt(18)))
	c.NextFont(s.font(e, 13.5, 400))
	c.NextTextColor(th.Colors[theme.Label2])
	c.BoxKeyed(ui.DrawText, ui.KeyN(p.Key, 1), base.StrBytes(p.Name))
	col := theme.Label3
	if ok {
		col = theme.Accent
	}
	rexui.Label(k.Kit, aiSummary(g, ok), rexui.LabelOpts{Style: rexui.TextCaption, Color: rexui.Of(col)})
	chev := s.ic.chevronRight
	if x.open {
		chev = s.ic.chevronDown
	}
	k.Icon(chev, th.Colors[theme.Label3])
	sig := k.EndPress(&p)
	e.TabStop(screen.StopInfoControl+mediaRowStop+2, sig.ID)
	if sig.Clicked {
		x.open = !x.open
	}
	if !x.open {
		x.focused = false
		k.EndContainer()
		return
	}

	x.levels = [3]string{i18n.T("Off"), i18n.P("AI access", "Read"), i18n.T("Read and reply")}
	level := 0
	switch {
	case ok && g.Write:
		level = 2
	case ok:
		level = 1
	}
	if rexui.SegmentedControl(k.Kit, i18n.T("What an AI agent may do in this chat"), &level, x.levels[:], rexui.SegmentedOpts{Size: theme.SizeSmall}).Changed {
		if level == 0 {
			a.ClearAIGrant(jid)
			ok = false
		} else {
			g.Write = level == 2
			g.Brief = x.text()
			a.SetAIGrant(jid, g)
			g, ok = a.AIGrant(jid)
		}
	}
	caption := func(text string) {
		rexui.Label(k.Kit, text, rexui.LabelOpts{Style: rexui.TextCaption, Color: rexui.Of(theme.Label2), Wrap: true})
	}
	if !ok {
		x.focused = false
		caption(i18n.T("An AI agent you connect in Settings sees only the chats you turn this on for."))
		k.EndContainer()
		return
	}
	if !e.S.Settings.AIAccess {
		// The switch is the person's: a chat's controls never turn it on.
		caption(i18n.T("AI access is turned off in Settings, so no agent can use this chat now."))
		if rexui.Button(k.Kit, i18n.T("Open Settings")+"###ai off", rexui.ButtonOpts{Size: theme.SizeSmall}).Clicked {
			e.S.OpenWindow(state.WinSettings)
		}
	}

	if g.Write {
		ask := !g.Auto
		if rexui.Switch(k.Kit, i18n.T("Ask me before each reply"), &ask, rexui.SwitchOpts{Size: theme.SizeSmall}).Changed {
			g.Auto = !ask
			a.SetAIGrant(jid, g)
		}
		if g.Auto {
			caption(i18n.T("Replies go out from your account without you seeing them first."))
		} else {
			caption(i18n.T("Each reply waits above the message field until you send or discard it."))
		}
	}
	wake := g.Wake
	if rexui.Switch(k.Kit, i18n.T("Start my agent when a message arrives"), &wake, rexui.SwitchOpts{Size: theme.SizeSmall}).Changed {
		g.Wake = wake
		a.SetAIGrant(jid, g)
	}
	if g.Wake && e.S.Settings.AIRunner.Kind == "" {
		caption(i18n.T("No agent is chosen yet. Choose one in Settings."))
		if rexui.Button(k.Kit, i18n.T("Open Settings"), rexui.ButtonOpts{Size: theme.SizeSmall}).Clicked {
			e.S.OpenWindow(state.WinSettings)
		}
	}

	rexui.Label(k.Kit, i18n.T("Instructions for the agent"), rexui.LabelOpts{Style: rexui.TextSemibold, Color: rexui.Of(theme.Label2)})
	// a row of its own, so the field is as wide as the panel's column
	k.BeginContainer(ui.AxisX, ui.CrossCenter, "brief", ui.Pct(1), ui.Children(1), 0)
	k.NextWidth(widget.Fill)
	f := rexui.TextField(k.Kit, i18n.T("Instructions for the agent"), &x.brief, i18n.T("What should it do in this chat?"), rexui.FieldOpts{})
	k.EndContainer()
	x.focused = f.Focused
	// what was typed is kept once the field is left, or Return is pressed
	if text := x.text(); text != g.Brief && (!f.Focused || f.Submitted) {
		g.Brief = text
		a.SetAIGrant(jid, g)
		x.loaded = text
	}

	if rep, ok := s.aiReport(e, a, jid); ok {
		title := i18n.T("The agent's last report")
		if rep.Failed {
			title = i18n.T("The agent's last run failed")
		}
		s.fmtBuf = format.AppendMomentStamp(s.fmtBuf[:0], i18n.Current(), time.Unix(rep.At, 0), s.now())
		rexui.Label(k.Kit, c.Fmt().Str(title).Str(" · ").Str(string(s.fmtBuf)).String(), rexui.LabelOpts{Style: rexui.TextSemibold, Color: rexui.Of(theme.Label2)})
		rexui.Label(k.Kit, rep.Text, rexui.LabelOpts{Style: rexui.TextCaption, Color: rexui.Of(theme.Label2), Wrap: true})
	} else if e.S.Agent != nil && e.S.Agent.Running(a.ID, string(jid)) {
		caption(i18n.T("Your agent is working on this chat…"))
	}
	k.EndContainer()
}

func (x *aiAccess) text() string { return x.brief.String() }

// aiReport is the last words of the chat's latest run, if it had one.
func (s *Screen) aiReport(e *screen.Env, a *state.Account, jid []byte) (agent.Report, bool) {
	if e.S.Agent == nil {
		return agent.Report{}, false
	}
	rep, ok := e.S.Agent.Report(a.ID, string(jid))
	return rep, ok && rep.Text != ""
}

// aiLeave keeps what was typed when the panel closes or shows another chat.
func (s *Screen) aiLeave(a *state.Account) {
	x := &s.aiAcc
	if a == nil || !x.has || s.jidN == 0 {
		return
	}
	jid := s.jid[:s.jidN]
	if g, ok := a.AIGrant(jid); ok && x.text() != g.Brief {
		g.Brief = x.text()
		a.SetAIGrant(jid, g)
	}
}
