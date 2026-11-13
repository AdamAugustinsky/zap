package settings

import (
	"strconv"
	"strings"
	"time"

	"gonativeui/rexui"
	"gonativeui/theme"
	"gonativeui/ui"

	"zapfast/agent"
	"zapfast/format"
	"zapfast/i18n"
	"zapfast/state"
	"zapfast/ui/screen"
)

// The AI access section (state/ai.go, package agent): the switch that opens
// the door for AI agents, how to connect one, the agent a chat's message
// starts, the hourly limit of replies no one looked at, every chat that has
// access, and what agents did lately. A chat is given access in its own menu
// or its info; here they are all in one place, to look over and take back.

// aiCaps are the choices of the hourly limit.
var aiCaps = [4]int{5, 10, 20, 50}

// aiSettings is the section's widget state.
type aiSettings struct {
	command        ui.TextBuffer
	commandEditing bool
	runners        [3]string
	caps           [4]string
	stamp          []byte
}

func (x *aiSettings) init(st *state.Settings) {
	x.command.Max = 2000
	x.command.Reserve(2000)
	x.command.SetString(st.AIRunner.Command)
	for i, n := range aiCaps {
		x.caps[i] = strconv.Itoa(n)
	}
}

// aiRunnerControl chooses the agent a chat's message starts.
func (s *Screen) aiRunnerControl(e *screen.Env, title string, pt float32) {
	st := &e.S.Settings
	s.ai.runners = [3]string{i18n.P("agent", "None"), "Claude Code", i18n.T("Custom command")}
	sel := 0
	switch st.AIRunner.Kind {
	case agent.RunnerClaude:
		sel = 1
	case agent.RunnerCustom:
		sel = 2
	}
	if v, ok := s.popup(e, title, sel, s.ai.runners[:], pt); ok {
		st.AIRunner.Kind = [3]string{"", agent.RunnerClaude, agent.RunnerCustom}[v]
		s.changed(e)
	}
}

// aiCommandField is the custom agent's command, kept when the field is left.
func (s *Screen) aiCommandField(e *screen.Env, title string, pt float32) {
	k := e.K.Kit
	st := &e.S.Settings
	k.NextWidth(k.Fixed(s.ctrlW(e, pt)))
	sig := rexui.TextField(k, title, &s.ai.command, "my-agent --mcp-config \"$ZAP_MCP_CONFIG\"", rexui.FieldOpts{})
	if sig.Submitted || (s.ai.commandEditing && !sig.Focused) {
		if v := strings.TrimSpace(s.ai.command.String()); v != st.AIRunner.Command {
			st.AIRunner.Command = v
			s.changed(e)
		}
	}
	s.ai.commandEditing = sig.Focused
}

// aiCapControl is the hourly limit of replies an agent sends on its own.
func (s *Screen) aiCapControl(e *screen.Env, title string, pt float32) {
	st := &e.S.Settings
	limit := st.AICap
	if limit <= 0 {
		limit = agent.DefaultCap
	}
	sel := 2
	for i, n := range aiCaps {
		if n == limit {
			sel = i
		}
	}
	if v, ok := s.popup(e, title, sel, s.ai.caps[:], pt); ok {
		st.AICap = aiCaps[v]
		s.changed(e)
	}
}

// aiConnectBlock says how an agent is connected: one command each, copied.
func (s *Screen) aiConnectBlock(e *screen.Env) {
	k := e.K.Kit
	rexui.Label(k, i18n.T("Connect an agent"), rexui.LabelOpts{Style: rexui.TextSemibold})
	rexui.Label(k, i18n.T("Run one of these once in a terminal. The agent then finds Zap's tools by itself, while Zap is open."),
		rexui.LabelOpts{Style: rexui.TextCallout, Color: rexui.Of(theme.Label2), Wrap: true})
	mcp := `"` + agent.MCPPath() + `" ` + agent.MCPArg
	for i, it := range [...]struct{ name, command string }{
		{"Claude Code", "claude mcp add --scope user zap -- " + mcp},
		{"Codex", "codex mcp add zap -- " + mcp},
		{"opencode", "opencode mcp add --global zap -- " + mcp},
	} {
		k.NextGap(8)
		k.BeginContainer(ui.AxisX, ui.CrossCenter, it.name, ui.Pct(1), ui.Children(1), 0)
		rexui.Label(k, it.name, rexui.LabelOpts{})
		k.Spacer()
		label := e.C().Fmt().Str(i18n.T("Copy command")).Str("###copy").Int(i).String()
		if button(e, label) {
			if set := e.C().ClipboardSet; set != nil {
				set([]byte(it.command))
				e.S.Toast([]byte(i18n.T("Copied")), false)
			}
		}
		k.EndContainer()
	}
}

// aiGrantLine is what a chat's grant allows, in a line.
func aiGrantLine(g agent.Grant) string {
	var line string
	switch {
	case !g.Write:
		line = i18n.T("Read only")
	case g.Auto:
		line = i18n.T("Read and reply")
	default:
		line = i18n.T("Reply with approval")
	}
	if g.Wake {
		line += " · " + i18n.T("starts your agent")
	}
	return line
}

// aiChatsBlock lists every chat with access, on every account, each with
// the button that takes it back.
func (s *Screen) aiChatsBlock(e *screen.Env) {
	k := e.K.Kit
	c := e.C()
	total := 0
	for _, a := range e.S.Accounts {
		total += len(a.Settings.AI)
	}
	k.BeginContainer(ui.AxisX, ui.CrossCenter, "ai chats head", ui.Pct(1), ui.Children(1), 0)
	rexui.Label(k, i18n.T("Chats with AI access"), rexui.LabelOpts{Style: rexui.TextSemibold})
	k.Spacer()
	k.BeginDisabled(total == 0)
	if rexui.Button(k, i18n.T("Remove all"), rexui.ButtonOpts{Variant: rexui.ButtonDestructive}).Clicked {
		e.S.ClearAIGrants()
	}
	k.EndDisabled()
	k.EndContainer()
	if total == 0 {
		rexui.Label(k, i18n.T("No chat has AI access. Right-click a chat and choose AI access."),
			rexui.LabelOpts{Style: rexui.TextCallout, Color: rexui.Of(theme.Label2), Wrap: true})
		return
	}
	n := 0
	for _, a := range e.S.Accounts {
		for _, jid := range sortedGrants(a) {
			g := a.Settings.AI[jid]
			name := jid
			if ch, ok := a.Model.Chats.Get(a.Model.Chats.Find([]byte(jid))); ok {
				if ch.Locked() {
					continue // never given, and not named here (state/ai.go drops its grant)
				}
				name = string(a.Model.ChatName(ch))
			}
			line := aiGrantLine(g)
			n++
			k.NextGap(8)
			k.BeginContainer(ui.AxisX, ui.CrossCenter, c.Fmt().Str("ai chat ").Int(n).String(), ui.Pct(1), ui.Children(1), 0)
			k.NextGap(0)
			k.BeginContainer(ui.AxisY, ui.CrossStart, "words", ui.Children(1), ui.Children(1), 0)
			rexui.Label(k, name, rexui.LabelOpts{})
			rexui.Label(k, line, rexui.LabelOpts{Style: rexui.TextCaption, Color: rexui.Of(theme.Label2)})
			k.EndContainer()
			k.Spacer()
			if button(e, c.Fmt().Str(i18n.T("Remove")).Str("###remove").Int(n).String()) {
				a.ClearAIGrant([]byte(jid))
			}
			k.EndContainer()
		}
	}
}

// sortedGrants are the ids of a's chats with access, in a steady order.
func sortedGrants(a *state.Account) []string {
	ids := make([]string, 0, len(a.Settings.AI))
	for jid := range a.Settings.AI {
		ids = append(ids, jid)
	}
	for i := 1; i < len(ids); i++ { // a few chats: insertion sort
		for j := i; j > 0 && ids[j] < ids[j-1]; j-- {
			ids[j], ids[j-1] = ids[j-1], ids[j]
		}
	}
	return ids
}

// aiDid says what an activity entry records.
func aiDid(d *agent.Did) string {
	switch d.Kind {
	case agent.DidRead:
		return i18n.Format(i18n.T("read {} messages"), "{}", strconv.Itoa(d.N))
	case agent.DidSearch:
		return i18n.T("searched the chat")
	case agent.DidSend:
		return i18n.T("sent a reply")
	case agent.DidPropose:
		return i18n.T("proposed a reply")
	case agent.DidApprove:
		return i18n.T("you sent its reply")
	case agent.DidDiscard:
		return i18n.T("you discarded its reply")
	case agent.DidEdit:
		return i18n.T("you rewrote its reply")
	case agent.DidRefuse:
		return i18n.T("was stopped by the hourly limit")
	case agent.RunStarted:
		return i18n.T("was started by a message")
	case agent.RunFinished:
		return i18n.T("finished")
	case agent.RunFailed:
		return i18n.T("failed")
	case agent.RunSkipped:
		return i18n.T("was not started: too many runs this hour")
	}
	return ""
}

// aiActivityShown is how many of the latest entries the section lists.
const aiActivityShown = 8

// aiActivityBlock is what agents did lately, newest first. It names chats
// and counts messages, and never shows what one said.
func (s *Screen) aiActivityBlock(e *screen.Env) {
	k := e.K.Kit
	c := e.C()
	rexui.Label(k, i18n.T("Recent activity"), rexui.LabelOpts{Style: rexui.TextSemibold})
	var list []agent.Did
	if e.S.Agent != nil {
		list = e.S.Agent.Activity()
	}
	if len(list) == 0 {
		rexui.Label(k, i18n.T("Nothing yet."), rexui.LabelOpts{Style: rexui.TextCallout, Color: rexui.Of(theme.Label2)})
		return
	}
	now := e.S.Now()
	for i := range list[:min(len(list), aiActivityShown)] {
		d := &list[i]
		s.ai.stamp = format.AppendMomentStamp(s.ai.stamp[:0], i18n.Current(), time.Unix(d.At, 0), now)
		who := d.Agent
		if who == "" {
			who = i18n.T("An agent")
		}
		line := c.Fmt().Str(string(s.ai.stamp)).Str(" · ").Str(d.Name).Str(" · ").Str(who).Str(" · ").Str(aiDid(d)).String()
		k.BeginContainer(ui.AxisX, ui.CrossCenter, c.Fmt().Str("ai did ").Int(i).String(), ui.Pct(1), ui.Children(1), 0)
		rexui.Label(k, line, rexui.LabelOpts{Style: rexui.TextCallout, Color: rexui.Of(theme.Label2), Wrap: true})
		k.EndContainer()
	}
}
