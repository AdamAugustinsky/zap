package state_test

import (
	"context"
	"strings"
	"testing"
	"time"

	"zapfast/abi"
	"zapfast/agent"
	"zapfast/core"
	"zapfast/gocore/demo"
	"zapfast/state"
)

const (
	demoAda   = "393331234567@s.whatsapp.net"
	demoGrace = "441632960123@s.whatsapp.net"
)

// An agent against the demo core, through the state as the app wires it:
// it reads the chat the person gave it and no other, its reply goes out as
// the person's own message, and a message arriving ends its wait.
func TestAnAgentUsesOnlyTheChatsItWasGiven(t *testing.T) {
	if testing.Short() {
		t.Skip("the demo core takes seconds")
	}
	c, err := demo.Open(core.Config{Wake: func() {}})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { c.Close(5 * time.Second) })
	s := state.New()
	s.Demo = true
	s.Agent = agent.New(agent.Options{})
	t.Cleanup(s.Agent.Close)
	a := s.AddAccount("1", nil, c)
	s.StartCores()
	pump := func(what string, ok func() bool) {
		t.Helper()
		for deadline := time.Now().Add(10 * time.Second); !ok(); {
			if time.Now().After(deadline) {
				t.Fatalf("no %s within 10 s", what)
			}
			s.Pump(0)
			time.Sleep(2 * time.Millisecond)
		}
		s.Pump(0)
	}
	pump("chats", func() bool { return a.Model.Chats.Loaded() })
	ada, dorothy := "1/"+demoAda, "1/"+findLocked(t, a)
	ask := func(req agent.Request) agent.Reply { return s.Agent.Do(context.Background(), req) }

	// Nothing is given until the person gives it.
	if rep := ask(agent.Request{Op: "read", Chat: ada}); rep.Error == "" {
		t.Fatalf("read with no grant: %+v", rep)
	}
	a.SetAIGrant([]byte(demoAda), agent.Grant{Write: true, Auto: true, Brief: "  be brief  "})
	// A locked chat is never given, whatever its grant says.
	a.SetAIGrant([]byte(strings.TrimPrefix(dorothy, "1/")), agent.Grant{})
	if !s.Settings.AIAccess {
		t.Fatal("the first grant did not turn AI access on")
	}
	s.Pump(0)
	if _, ok := a.AIGrant([]byte(strings.TrimPrefix(dorothy, "1/"))); ok {
		t.Fatal("a locked chat kept its grant")
	}

	list := ask(agent.Request{Op: "chats"})
	if len(list.Chats) != 1 || list.Chats[0].Name != "Ada Lovelace" || list.Chats[0].Instructions != "be brief" || list.Chats[0].Access != "reply" {
		t.Fatalf("chats = %+v", list)
	}
	for _, chat := range []string{dorothy, "1/" + demoGrace} {
		if rep := ask(agent.Request{Op: "read", Chat: chat}); rep.Error == "" {
			t.Fatalf("read %s: %+v", chat, rep)
		}
	}
	// The demo's chat with Ada: pictures, a clip, files and a voice note
	// by kind, the words as they were written.
	rep := ask(agent.Request{Op: "read", Chat: ada, Limit: 3})
	if n := len(rep.Messages); n != 3 || !rep.More ||
		rep.Messages[0].From != "me" || rep.Messages[0].Text != "Yes! It compiles on stable now, no nightly needed." ||
		rep.Messages[1].Kind != "voice" ||
		rep.Messages[2].From != "Ada Lovelace" || rep.Messages[2].Text != "That is wonderful news. Send me the branch when you can." {
		t.Fatalf("read = %+v", rep)
	}
	last := rep.Messages[2].ID
	if older := ask(agent.Request{Op: "read", Chat: ada, Before: rep.Messages[0].ID, Limit: 1}); len(older.Messages) != 1 || older.Messages[0].Text != "Did the analytical engine build finish?" {
		t.Fatalf("the page before = %+v", older)
	}
	if hits := ask(agent.Request{Op: "search", Chat: ada, Query: "nightly"}); len(hits.Messages) != 1 {
		t.Fatalf("search = %+v", hits)
	}

	// A reply is the person's own message, in the chat for the screens too.
	if rep := ask(agent.Request{Op: "send", Chat: ada, Text: "Branch pushed."}); rep.Status != "sent" {
		t.Fatalf("send = %+v", rep)
	}
	ref := a.Model.Chats.Find([]byte(demoAda))
	pump("the reply in the chat list", func() bool {
		ch, _ := a.Model.Chats.Get(ref)
		return string(ch.Preview()) == "Branch pushed."
	})

	// A message arriving ends a wait with what is new.
	got := make(chan agent.Reply, 1)
	go func() { got <- ask(agent.Request{Op: "wait", Chat: ada, Timeout: 20}) }()
	time.Sleep(50 * time.Millisecond)
	cmd := a.Cmd(abi.CommandTagDemoIncoming)
	cmd.DemoIncoming.Chat, cmd.DemoIncoming.Sender, cmd.DemoIncoming.Text = []byte(demoAda), []byte(demoAda), []byte("Thank you!")
	if err := a.Send(cmd); err != nil {
		t.Fatal(err)
	}
	var news agent.Reply
	pump("the wait's end", func() bool {
		select {
		case news = <-got:
			return true
		default:
			return false
		}
	})
	if n := len(news.Messages); n == 0 || news.Messages[n-1].Text != "Thank you!" || news.Messages[n-1].ID == last {
		t.Fatalf("wait = %+v", news)
	}

	// Taking the grant away closes the chat again; turning AI access off
	// closes them all.
	a.ClearAIGrant([]byte(demoAda))
	s.Pump(0)
	if rep := ask(agent.Request{Op: "read", Chat: ada}); rep.Error == "" {
		t.Fatalf("read after the grant went: %+v", rep)
	}
	a.SetAIGrant([]byte(demoAda), agent.Grant{})
	s.Settings.AIAccess = false
	s.SettingsChanged()
	s.Pump(0)
	if rep := ask(agent.Request{Op: "chats"}); !strings.Contains(rep.Error, "turned off") {
		t.Fatalf("chats while off: %+v", rep)
	}
	// The switch is the person's: with it off, changing a chat's grant
	// opens nothing.
	a.SetAIGrant([]byte(demoAda), agent.Grant{Write: true})
	a.SetAIGrant([]byte(demoGrace), agent.Grant{})
	s.Pump(0)
	if s.Settings.AIAccess {
		t.Fatal("a grant's change turned AI access back on")
	}
}

// findLocked is the demo's locked chat.
func findLocked(t *testing.T, a *state.Account) string {
	t.Helper()
	for _, ref := range a.Model.Chats.Order() {
		if ch, ok := a.Model.Chats.Get(ref); ok && ch.Locked() {
			return string(ch.ID())
		}
	}
	t.Fatal("the demo has no locked chat")
	return ""
}
