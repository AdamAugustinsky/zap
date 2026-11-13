package agent

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"zapfast/abi"
)

// fakeCore is an account's core: a chat's messages in memory.
type fakeCore struct {
	mu       sync.Mutex
	messages map[string][]abi.Message
	sent     []string // "chat|text|quoting"
	refuse   error
}

func text(id, from, body string, me bool) abi.Message {
	m := abi.Message{ID: []byte(id), Sender: []byte(from), FromMe: me, Timestamp: 1_700_000_000}
	m.Content.Tag = abi.ContentTagText
	m.Content.Text.Text = []byte(body)
	return m
}

func (f *fakeCore) add(chat string, m abi.Message) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.messages == nil {
		f.messages = map[string][]abi.Message{}
	}
	f.messages[chat] = append(f.messages[chat], m)
}

func (f *fakeCore) Page(_ context.Context, chat, before string, limit int) ([]abi.Message, bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	all := f.messages[chat]
	end := len(all)
	if before != "" {
		for i := range all {
			if string(all[i].ID) == before {
				end = i
			}
		}
	}
	start := max(end-limit, 0)
	return append([]abi.Message(nil), all[start:end]...), start > 0, nil
}

func (f *fakeCore) Search(_ context.Context, chat, query string, limit int) ([]abi.Message, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	var hits []abi.Message
	for _, m := range f.messages[chat] {
		if strings.Contains(string(m.Content.Text.Text), query) && len(hits) < limit {
			hits = append(hits, m)
		}
	}
	return hits, nil
}

func (f *fakeCore) Send(chat, body, quoting string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.refuse != nil {
		return f.refuse
	}
	f.sent = append(f.sent, chat+"|"+body+"|"+quoting)
	return nil
}

func (f *fakeCore) sends() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.sent...)
}

const (
	vivo  = "5511999990000@s.whatsapp.net"
	team  = "120363000000000001@g.us"
	other = "5511888880000@s.whatsapp.net"
)

// world is a Server over one account with three chats: vivo may be read and
// answered on its own, team may only be read, and other has no grant.
func world(t *testing.T, opts Options) (*Server, *fakeCore) {
	t.Helper()
	s := New(opts)
	t.Cleanup(s.Close)
	core := &fakeCore{}
	core.add(vivo, text("v1", vivo, "Olá! Como posso ajudar?", false))
	core.add(vivo, text("v2", "me", "Quero rever meu plano.", true))
	core.add(team, text("t1", "5511777770000@s.whatsapp.net", "the deploy doc is at https://example.com/doc", false))
	core.add(other, text("o1", other, "private", false))
	s.Attach("1", core)
	s.SetChats("1", []Entry{
		{Chat: vivo, Name: "VIVO", Grant: Grant{Write: true, Auto: true, Brief: "get a better price"}},
		{Chat: team, Name: "Team", Group: true},
	})
	if err := s.Configure(Config{On: true}); err != nil {
		t.Fatal(err)
	}
	return s, core
}

func do(s *Server, req Request) Reply { return s.Do(context.Background(), req) }

func TestOnlyGrantedChatsExist(t *testing.T) {
	s, core := world(t, Options{})
	rep := do(s, Request{Op: "chats"})
	if len(rep.Chats) != 2 || rep.Chats[0].Name != "Team" || rep.Chats[1].Name != "VIVO" {
		t.Fatalf("chats = %+v", rep.Chats)
	}
	if c := rep.Chats[1]; c.Access != "reply" || c.Instructions != "get a better price" || c.Chat != "1/"+vivo {
		t.Fatalf("vivo = %+v", c)
	}
	if c := rep.Chats[0]; c.Access != "read" || c.Kind != "group" {
		t.Fatalf("team = %+v", c)
	}
	// A chat with no grant answers as one that does not exist, for every
	// request, and nothing is sent to it.
	for _, op := range []string{"read", "search", "send", "wait"} {
		for _, chat := range []string{"1/" + other, "1/nobody@s.whatsapp.net", "2/" + vivo, vivo, ""} {
			if op == "wait" && chat == "" {
				continue // a wait on every shared chat
			}
			rep := do(s, Request{Op: op, Chat: chat, Query: "private", Text: "hi", Timeout: 1})
			if rep.Error != errNoChat.Error() {
				t.Errorf("%s %q: %+v", op, chat, rep)
			}
		}
	}
	if len(core.sends()) != 0 {
		t.Fatalf("sent %v", core.sends())
	}
	rep = do(s, Request{Op: "read", Chat: "1/" + vivo})
	if len(rep.Messages) != 2 || rep.Messages[0].From != "VIVO" || rep.Messages[1].From != "me" || rep.Messages[0].Text != "Olá! Como posso ajudar?" {
		t.Fatalf("read = %+v", rep)
	}
}

func TestOffAndExpiredAnswerNothing(t *testing.T) {
	now := time.Unix(1_700_000_000, 0)
	s, _ := world(t, Options{Now: func() time.Time { return now }})
	s.SetChats("1", []Entry{{Chat: vivo, Name: "VIVO", Grant: Grant{Expires: now.Unix() + 60}}})
	if rep := do(s, Request{Op: "read", Chat: "1/" + vivo}); rep.Error != "" {
		t.Fatal(rep.Error)
	}
	now = now.Add(2 * time.Minute)
	if rep := do(s, Request{Op: "read", Chat: "1/" + vivo}); rep.Error != errNoChat.Error() {
		t.Fatalf("an expired grant read: %+v", rep)
	}
	if rep := do(s, Request{Op: "chats"}); len(rep.Chats) != 0 {
		t.Fatalf("an expired grant listed: %+v", rep)
	}
	s.Configure(Config{On: false})
	for _, op := range []string{"chats", "read", "send", "wait"} {
		if rep := do(s, Request{Op: op, Chat: "1/" + vivo, Text: "x"}); rep.Error != errOff.Error() {
			t.Errorf("%s while off: %+v", op, rep)
		}
	}
}

func TestSendNeedsWriteAndStopsAtTheCap(t *testing.T) {
	s, core := world(t, Options{})
	if rep := do(s, Request{Op: "send", Chat: "1/" + team, Text: "hello"}); !strings.Contains(rep.Error, "read only") {
		t.Fatalf("a read-only chat took a reply: %+v", rep)
	}
	s.Configure(Config{On: true, Cap: 2})
	for i := range 2 {
		if rep := do(s, Request{Op: "send", Chat: "1/" + vivo, Text: fmt.Sprint("offer ", i), ReplyTo: "v1"}); rep.Status != "sent" {
			t.Fatalf("send %d: %+v", i, rep)
		}
	}
	if rep := do(s, Request{Op: "send", Chat: "1/" + vivo, Text: "one more"}); !strings.Contains(rep.Error, "hourly limit") {
		t.Fatalf("the cap let a third reply through: %+v", rep)
	}
	if got := core.sends(); len(got) != 2 || got[0] != vivo+"|offer 0|v1" {
		t.Fatalf("sent %v", got)
	}
}

func TestReplyWaitsForTheOwner(t *testing.T) {
	changed := 0
	s, core := world(t, Options{Changed: func() { changed++ }})
	s.SetChats("1", []Entry{{Chat: vivo, Name: "VIVO", Grant: Grant{Write: true}}})
	rep := do(s, Request{Op: "send", Chat: "1/" + vivo, Text: "R$ 89?", Agent: "claude-code"})
	if rep.Status != "waiting_for_approval" || len(core.sends()) != 0 {
		t.Fatalf("rep %+v sent %v", rep, core.sends())
	}
	do(s, Request{Op: "send", Chat: "1/" + vivo, Text: "drop me"})
	p := s.Pending()
	if len(p) != 2 || p[0].Text != "R$ 89?" || p[0].Agent != "claude-code" || p[0].Name != "VIVO" {
		t.Fatalf("pending = %+v", p)
	}
	if changed == 0 {
		t.Fatal("the app was not told a proposal waits")
	}
	if _, ok, err := s.Resolve(p[1].ID, false, ""); !ok || err != nil {
		t.Fatal(ok, err)
	}
	// The owner may change the words before sending.
	if _, ok, err := s.Resolve(p[0].ID, true, "R$ 85?"); !ok || err != nil {
		t.Fatal(ok, err)
	}
	if got := core.sends(); len(got) != 1 || got[0] != vivo+"|R$ 85?|" {
		t.Fatalf("sent %v", got)
	}
	if _, ok, _ := s.Resolve(p[0].ID, true, ""); ok {
		t.Fatal("a proposal was answered twice")
	}
	// Taking the grant away drops what waits.
	do(s, Request{Op: "send", Chat: "1/" + vivo, Text: "later"})
	s.SetChats("1", nil)
	if len(s.Pending()) != 0 {
		t.Fatalf("pending after the grant went: %+v", s.Pending())
	}
}

func TestWaitReturnsWhatArrives(t *testing.T) {
	s, core := world(t, Options{})
	// What came after the caller's last message is news at once.
	if rep := do(s, Request{Op: "wait", Chat: "1/" + vivo, After: "v1", Timeout: 5}); len(rep.Messages) != 1 || rep.Messages[0].ID != "v2" {
		t.Fatalf("after v1: %+v", rep)
	}
	got := make(chan Reply, 1)
	go func() { got <- do(s, Request{Op: "wait", After: "v2", Timeout: 20}) }()
	time.Sleep(50 * time.Millisecond)
	s.Incoming("1", other) // no grant: no news
	select {
	case rep := <-got:
		t.Fatalf("a chat without a grant ended the wait: %+v", rep)
	case <-time.After(100 * time.Millisecond):
	}
	core.add(vivo, text("v3", vivo, "Temos R$ 99.", false))
	s.Incoming("1", vivo)
	select {
	case rep := <-got:
		if rep.Chat != "1/"+vivo || len(rep.Messages) != 1 || rep.Messages[0].Text != "Temos R$ 99." {
			t.Fatalf("wait = %+v", rep)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the wait did not end")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if rep := s.Do(ctx, Request{Op: "wait", Chat: "1/" + vivo, After: "v3", Timeout: 20}); !rep.TimedOut {
		t.Fatalf("a caller that left: %+v", rep)
	}
}

func TestWakeRunsOnceAndItsTokenIsForOneChat(t *testing.T) {
	type started struct {
		run  Run
		list Reply
		leak Reply
	}
	runs := make(chan started, 4)
	release := make(chan struct{})
	var s *Server
	opts := Options{Quiet: 30 * time.Millisecond, Patience: time.Second, Socket: "/nowhere/agent.sock"}
	opts.Run = func(ctx context.Context, r Run) (string, error) {
		st := started{run: r}
		st.list = do(s, Request{Op: "chats", Token: r.Token})
		st.leak = do(s, Request{Op: "read", Token: r.Token, Chat: "1/" + team})
		runs <- st
		<-release
		return "they offered R$ 99", nil
	}
	opts.Socket = "" // no listener: the test calls Do
	s, core := world(t, opts)
	s.Configure(Config{On: true, Runner: Runner{Kind: RunnerCustom, Command: "true"}})
	s.SetChats("1", []Entry{
		{Chat: vivo, Name: "VIVO", Grant: Grant{Write: true, Wake: true, Brief: "negotiate"}},
		{Chat: team, Name: "Team", Group: true},
	})
	// A burst is one run.
	for i := range 3 {
		core.add(vivo, text(fmt.Sprint("b", i), vivo, "…", false))
		s.Incoming("1", vivo)
	}
	s.Incoming("1", team) // no Wake
	var st started
	select {
	case st = <-runs:
	case <-time.After(5 * time.Second):
		t.Fatal("no run")
	}
	if st.run.Chat != "1/"+vivo || !strings.Contains(st.run.Prompt, "negotiate") || !strings.Contains(st.run.Prompt, "with approval") {
		t.Fatalf("run = %+v", st.run)
	}
	if len(st.list.Chats) != 1 || st.list.Chats[0].Name != "VIVO" {
		t.Fatalf("a run's token lists %+v", st.list)
	}
	if st.leak.Error != errNoChat.Error() {
		t.Fatalf("a run's token read another chat: %+v", st.leak)
	}
	if !s.Running("1", vivo) {
		t.Fatal("not running")
	}
	// A message during the run starts one more, after it.
	s.Incoming("1", vivo)
	select {
	case <-runs:
		t.Fatal("two runs at once for one chat")
	case <-time.After(100 * time.Millisecond):
	}
	release <- struct{}{}
	select {
	case <-runs:
	case <-time.After(5 * time.Second):
		t.Fatal("no second run")
	}
	release <- struct{}{}
	deadline := time.Now().Add(5 * time.Second)
	for s.Running("1", vivo) && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	if rep := do(s, Request{Op: "chats", Token: st.run.Token}); rep.Error == "" {
		t.Fatalf("the token outlived its run: %+v", rep)
	}
	if r := s.Reports()[[2]string{"1", vivo}]; r.Text != "they offered R$ 99" || r.Failed {
		t.Fatalf("report = %+v", r)
	}
}

func TestSocketIsTheOwnersAndGoesWhenOff(t *testing.T) {
	dir, err := os.MkdirTemp("/tmp", "zap-agent-test-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(dir) })
	sock := filepath.Join(dir, "sub", SocketName)
	s, _ := world(t, Options{Socket: sock})
	st, err := os.Stat(sock)
	if err != nil {
		t.Fatal(err)
	}
	if st.Mode().Perm() != 0o600 {
		t.Fatalf("the socket's mode is %v", st.Mode().Perm())
	}
	rep, err := Ask(context.Background(), sock, Request{Op: "read", Chat: "1/" + team})
	if err != nil || len(rep.Messages) != 1 {
		t.Fatalf("over the socket: %+v, %v", rep, err)
	}
	s.Configure(Config{On: false})
	if _, err := os.Stat(sock); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("the socket is still there: %v", err)
	}
	if _, err := Ask(context.Background(), sock, Request{Op: "chats"}); err == nil {
		t.Fatal("answered while off")
	}
}

func TestExecGivesACustomRunnerItsPromptAndToken(t *testing.T) {
	r := Run{Chat: "1/" + vivo, Name: "VIVO", Token: "tok", Socket: "/s", Prompt: "the prompt",
		Runner: Runner{Kind: RunnerCustom, Command: `cat; printf ' %s %s ' "$ZAP_AGENT_TOKEN" "$ZAP_CHAT"; cat "$ZAP_MCP_CONFIG"`}}
	out, err := Exec(context.Background(), r)
	if err != nil {
		t.Fatal(err, out)
	}
	if !strings.HasPrefix(out, "the prompt tok 1/"+vivo) || !strings.Contains(out, `"ZAP_AGENT_TOKEN":"tok"`) || !strings.Contains(out, `"args":["mcp"]`) {
		t.Fatalf("out = %q", out)
	}
	r.Runner.Command = "echo why >&2; exit 3"
	if out, err := Exec(context.Background(), r); err == nil || out != "why" {
		t.Fatalf("a failed runner: %q, %v", out, err)
	}
}

func TestRenderSaysWhatAMessageIsWithoutItsFile(t *testing.T) {
	e := Entry{Name: "Team", Group: true}
	m := text("1", "5511777770000@s.whatsapp.net", "", false)
	m.Content = abi.Content{Tag: abi.ContentTagImage}
	m.Content.Image.Caption, m.Content.Image.HasCaption = []byte("the invoice"), true
	m.Content.Image.Media.Path, m.Content.Image.Media.HasPath = []byte("/secret/path.jpg"), true
	m.HasQuoted = true
	m.Quoted.ID, m.Quoted.Summary = []byte("0"), []byte("send it please")
	x := render(&m, e, time.UTC)
	if x.Kind != "image" || x.Text != "the invoice" || x.From != "+5511777770000" || x.ReplyTo == nil || x.ReplyTo.Text != "send it please" {
		t.Fatalf("%+v", x)
	}
	m.Content = abi.Content{Tag: abi.ContentTagPhoneOnly}
	m.Content.PhoneOnly.ViewOnce = true
	if x := render(&m, e, time.UTC); x.Kind != "unavailable" {
		t.Fatalf("view once: %+v", x)
	}
	m.Content = abi.Content{Tag: abi.ContentTagInteractive}
	m.Content.Interactive.Text = []byte("Escolha:")
	m.Content.Interactive.HasCard = true
	m.Content.Interactive.Card.Buttons = []abi.InteractiveButton{{Label: []byte("2ª via")}, {Label: []byte("Planos")}}
	if x := render(&m, e, time.UTC); x.Text != "Escolha:\n[2ª via]\n[Planos]" {
		t.Fatalf("buttons: %q", x.Text)
	}
}
