package state

import (
	"context"
	"log/slog"
	"slices"
	"strings"

	"zapfast/abi"
	"zapfast/agent"
	"zapfast/core"
	"zapfast/i18n"
)

// AI access: the chats the person lets AI agents read or answer (package
// agent holds the door; this is what the person chose, and how the door
// learns of it). A chat's grant is the person's, per chat and per account,
// kept in the account's settings file; the switch, the hourly limit and the
// agent a chat wakes are the app's, in settings.json. Nothing reaches an
// agent unless Settings.AIAccess is on and the chat has a grant, and a
// locked chat is never given, whatever its grant says.

// AIGrant is chat jid's grant on a; false when it has none.
func (a *Account) AIGrant(jid []byte) (agent.Grant, bool) {
	g, ok := a.Settings.AI[string(jid)]
	return g, ok
}

// SetAIGrant gives chat jid on a the grant g and saves it. The very first
// grant turns AI access on: the person asked for it by giving one. After
// that the switch is theirs alone: with it off, changing a chat's grant
// opens nothing.
func (a *Account) SetAIGrant(jid []byte, g agent.Grant) {
	if len(jid) == 0 {
		return
	}
	first := true
	for _, b := range a.st.Accounts {
		first = first && len(b.Settings.AI) == 0
	}
	g.Brief = strings.TrimSpace(clipBytes(g.Brief, agent.BriefMax))
	if !g.Write {
		g.Auto = false
	}
	if a.Settings.AI == nil {
		a.Settings.AI = map[string]agent.Grant{}
	}
	a.Settings.AI[string(jid)] = g
	a.SettingsChanged()
	if first && !a.st.Settings.AIAccess {
		a.st.Settings.AIAccess = true
		a.st.SettingsChanged()
	}
}

// ClearAIGrant takes chat jid's grant away.
func (a *Account) ClearAIGrant(jid []byte) {
	if _, ok := a.Settings.AI[string(jid)]; !ok {
		return
	}
	delete(a.Settings.AI, string(jid))
	a.SettingsChanged()
}

// ClearAIGrants takes every chat's grant away, on every account.
func (s *State) ClearAIGrants() {
	for _, a := range s.Accounts {
		if len(a.Settings.AI) > 0 {
			a.Settings.AI = nil
			a.SettingsChanged()
		}
	}
}

func clipBytes(s string, n int) string {
	if len(s) <= n {
		return s
	}
	for n > 0 && s[n]&0xC0 == 0x80 {
		n--
	}
	return s[:n]
}

// AIProposal is the oldest reply an agent waits to send to chat jid of a,
// and how many wait there; false when none does.
func (s *State) AIProposal(a *Account, jid []byte) (p agent.Proposal, n int, ok bool) {
	for i := range s.aiPending {
		if q := &s.aiPending[i]; q.Account == a.ID && q.Chat == string(jid) {
			if n == 0 {
				p = *q
			}
			n++
		}
	}
	return p, n, n > 0
}

// AIWaiting reports whether an agent's reply to chat jid of a waits.
func (s *State) AIWaiting(a *Account, jid []byte) bool {
	for i := range s.aiPending {
		if q := &s.aiPending[i]; q.Account == a.ID && q.Chat == string(jid) {
			return true
		}
	}
	return false
}

// AIResolve answers a waiting reply: send carries it out as it is,
// otherwise it is dropped.
func (s *State) AIResolve(id uint64, send bool) {
	if s.Agent == nil {
		return
	}
	if _, ok, err := s.Agent.Resolve(id, send, ""); ok && err != nil {
		slog.Warn("zapfast: an agent's reply was not sent", "err", err)
		s.Toast([]byte(i18n.T("The reply could not be sent")), true)
	}
	s.aiRefresh()
}

// AITake hands a waiting reply to the person to rewrite: it waits no
// longer, and its words are theirs to put in the message field.
func (s *State) AITake(id uint64) (agent.Proposal, bool) {
	if s.Agent == nil {
		return agent.Proposal{}, false
	}
	p, ok := s.Agent.Take(id)
	s.aiRefresh()
	return p, ok
}

// aiRefresh reads the waiting replies again, and tells the person of one
// that is new and that they are not looking at.
func (s *State) aiRefresh() {
	s.aiGen = s.Agent.Gen()
	s.aiPending = s.Agent.Pending()
	s.gen++
	for i := range s.aiPending {
		p := &s.aiPending[i]
		if p.ID <= s.aiSeen {
			continue
		}
		s.aiSeen = p.ID
		a := s.AccountByID(p.Account)
		if a == nil {
			continue
		}
		ref := a.Model.Chats.Find([]byte(p.Chat))
		if a.Active() && s.Focused && !s.Lock.Locked && s.Page == PageChats && a.Open == ref {
			continue // the strip above the message field is in front of them
		}
		n := Notice{Account: a.ID, Chat: []byte(p.Chat), Hidden: true, Sound: s.Settings.MessageSound,
			Title: p.Name, Body: i18n.T("Your agent has a reply ready. Open the chat to send or discard it.")}
		if s.Lock.Locked || !s.Settings.NotificationText || n.Title == "" {
			n.Title = "Zap"
		}
		if a.Settings.Notifications {
			s.Notices = append(s.Notices, n)
			s.Attention = true
		}
	}
}

// agentCore is an account's core as the agents' door uses it.
type agentCore struct{ conn core.Conn }

func (c agentCore) Page(ctx context.Context, chat, before string, limit int) ([]abi.Message, bool, error) {
	h, ok := c.conn.(core.History)
	if !ok {
		return nil, false, core.ErrNoHistory
	}
	return h.History(ctx, chat, before, limit)
}

func (c agentCore) Search(ctx context.Context, chat, query string, limit int) ([]abi.Message, error) {
	h, ok := c.conn.(core.History)
	if !ok {
		return nil, core.ErrNoHistory
	}
	return h.SearchHistory(ctx, chat, query, limit)
}

// Send is SendText, as the composer sends it: Conn.Send takes it from any
// goroutine, and the message comes back to the screens as any other.
func (c agentCore) Send(chat, text, quoting string) error {
	cmd := abi.Command{Tag: abi.CommandTagSendText}
	cmd.SendText.Chat, cmd.SendText.Text = []byte(chat), []byte(text)
	if quoting != "" {
		cmd.SendText.Quoting, cmd.SendText.HasQuoting = abi.ReplyTarget{ID: []byte(quoting)}, true
	}
	return c.conn.Send(&cmd)
}

// aiSync is what the door was last told of an account.
type aiSync struct {
	conn    core.Conn
	offline bool // what the door was told of the link, once told
	told    bool
	entries []agent.Entry
	gen     uint64 // the account's settingsGen
	chats   uint32 // the chats' generation
	names   uint32 // the contacts' generation
	done    bool
}

// syncAgent tells the door what changed: the settings, each account's core
// and the chats that have a grant, by the names the list shows. Pump calls
// it; it does nothing while nothing moved.
func (s *State) syncAgent() {
	if s.Agent == nil {
		return
	}
	defer func() {
		if s.Agent.Gen() != s.aiGen {
			s.aiRefresh()
		}
	}()
	cfg := agent.Config{On: s.Settings.AIAccess, Cap: s.Settings.AICap, Runner: s.Settings.AIRunner}
	if !s.aiConfigured || cfg != s.aiConfig {
		s.aiConfigured, s.aiConfig = true, cfg
		if err := s.Agent.Configure(cfg); err != nil {
			slog.Warn("zapfast: the agents' socket did not open", "err", err)
			s.Toast([]byte(i18n.T("AI access could not start")), true)
		}
	}
	for _, a := range s.Accounts {
		y := &a.ai
		if a.Conn != y.conn {
			y.conn, y.done, y.told = a.Conn, false, false
			if a.Conn == nil {
				s.Agent.Detach(a.ID)
				continue
			}
			s.Agent.Attach(a.ID, agentCore{a.Conn})
		}
		if a.Conn == nil {
			continue
		}
		// Offline once the core has said so; until it says anything, the
		// core itself decides about a send.
		if off := a.Model.Link.Known() && !a.Model.Link.Connected(); off != y.offline || !y.told {
			y.offline, y.told = off, true
			s.Agent.SetOffline(a.ID, off)
		}
		chats, names := a.Model.Chats.Gen(), a.Model.Contacts.Gen()
		if y.done && y.gen == a.settingsGen && y.chats == chats && y.names == names {
			continue
		}
		a.aiDropLocked()
		y.done, y.gen, y.chats, y.names = true, a.settingsGen, chats, names
		entries := a.aiEntries()
		if slices.Equal(entries, y.entries) {
			continue
		}
		y.entries = entries
		s.Agent.SetChats(a.ID, entries)
	}
}

// aiDropLocked takes the grant from a chat that became locked: a locked
// chat is never given, and is named nowhere (Settings, the activity list)
// while its folder is closed.
func (a *Account) aiDropLocked() {
	for jid := range a.Settings.AI {
		if ch, ok := a.Model.Chats.Get(a.Model.Chats.Find([]byte(jid))); ok && ch.Locked() {
			delete(a.Settings.AI, jid)
			a.SettingsChanged()
			a.st.Agent.Forget(a.ID, jid)
		}
	}
}

// aiEntries are a's chats with a grant, by id: the ones the model holds and
// that are not locked.
func (a *Account) aiEntries() []agent.Entry {
	var out []agent.Entry
	for jid, g := range a.Settings.AI {
		ch, ok := a.Model.Chats.Get(a.Model.Chats.Find([]byte(jid)))
		if !ok || ch.Locked() {
			continue
		}
		out = append(out, agent.Entry{Chat: jid, Name: string(a.Model.ChatName(ch)), Group: ch.Group(), Grant: g})
	}
	slices.SortFunc(out, func(x, y agent.Entry) int { return strings.Compare(x.Chat, y.Chat) })
	return out
}

// forgetAgent takes a removed account away from the door.
func (s *State) forgetAgent(a *Account) {
	if s.Agent != nil {
		s.Agent.Detach(a.ID)
	}
}
