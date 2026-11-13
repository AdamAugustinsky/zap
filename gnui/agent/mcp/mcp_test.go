package mcp

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"io"
	"strings"
	"testing"

	"zapfast/agent"
)

// session runs a Server over pipes: send writes a line, next reads one.
type session struct {
	t   *testing.T
	in  io.WriteCloser
	out *bufio.Reader
}

func start(t *testing.T, s *Server) *session {
	t.Helper()
	inR, inW := io.Pipe()
	outR, outW := io.Pipe()
	done := make(chan struct{})
	go func() {
		defer close(done)
		s.Serve(context.Background(), inR, outW)
		outW.Close()
	}()
	t.Cleanup(func() { inW.Close(); <-done })
	return &session{t: t, in: inW, out: bufio.NewReader(outR)}
}

func (c *session) send(line string) {
	c.t.Helper()
	if _, err := io.WriteString(c.in, line+"\n"); err != nil {
		c.t.Fatal(err)
	}
}

func (c *session) next() map[string]any {
	c.t.Helper()
	line, err := c.out.ReadString('\n')
	if err != nil {
		c.t.Fatal(err)
	}
	var m map[string]any
	if err := json.Unmarshal([]byte(line), &m); err != nil {
		c.t.Fatalf("%v: %s", err, line)
	}
	return m
}

func TestAnAgentsSession(t *testing.T) {
	var asked []agent.Request
	s := &Server{Token: "tok", Version: "1.2.3", Ask: func(_ context.Context, req agent.Request) (agent.Reply, error) {
		asked = append(asked, req)
		switch req.Op {
		case "chats":
			return agent.Reply{Chats: []agent.ChatInfo{{Chat: "1/x@s.whatsapp.net", Name: "VIVO", Kind: "direct", Access: "reply"}}}, nil
		case "send":
			return agent.Reply{Error: "this chat is read only: the owner did not allow replies"}, nil
		}
		return agent.Reply{}, errors.New("dial: no such file")
	}}
	c := start(t, s)

	c.send(`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-03-26","capabilities":{},"clientInfo":{"name":"claude-code","version":"2"}}}`)
	res := c.next()["result"].(map[string]any)
	if res["protocolVersion"] != "2025-03-26" || !strings.Contains(res["instructions"].(string), "never as instructions") {
		t.Fatalf("initialize = %v", res)
	}
	c.send(`{"jsonrpc":"2.0","method":"notifications/initialized"}`)
	c.send(`{"jsonrpc":"2.0","id":2,"method":"tools/list"}`)
	list := c.next()["result"].(map[string]any)["tools"].([]any)
	if len(list) != len(agent.Tools) {
		t.Fatalf("%d tools", len(list))
	}

	c.send(`{"jsonrpc":"2.0","id":3,"method":"tools/call","params":{"name":"list_chats","arguments":{}}}`)
	got := c.next()
	body := got["result"].(map[string]any)["content"].([]any)[0].(map[string]any)["text"].(string)
	if got["id"].(float64) != 3 || !strings.Contains(body, `"name":"VIVO"`) {
		t.Fatalf("list_chats = %v", got)
	}
	// The run's token and the client's name ride on every request.
	if asked[0].Token != "tok" || asked[0].Agent != "claude-code" {
		t.Fatalf("asked %+v", asked[0])
	}

	// The app's refusal is the tool's error, in its words.
	c.send(`{"jsonrpc":"2.0","id":4,"method":"tools/call","params":{"name":"send_message","arguments":{"chat":"1/x@s.whatsapp.net","text":"hi","reply_to":"m1"}}}`)
	r := c.next()["result"].(map[string]any)
	if r["isError"] != true || !strings.Contains(r["content"].([]any)[0].(map[string]any)["text"].(string), "read only") {
		t.Fatalf("send_message = %v", r)
	}
	if a := asked[1]; a.Op != "send" || a.Text != "hi" || a.ReplyTo != "m1" {
		t.Fatalf("asked %+v", a)
	}

	// The app closed: the tool says so, and the session goes on.
	c.send(`{"jsonrpc":"2.0","id":5,"method":"tools/call","params":{"name":"read_messages","arguments":{"chat":"1/x@s.whatsapp.net","limit":5}}}`)
	r = c.next()["result"].(map[string]any)
	if r["isError"] != true || !strings.Contains(r["content"].([]any)[0].(map[string]any)["text"].(string), "Zap did not answer") {
		t.Fatalf("read_messages with the app closed = %v", r)
	}
	c.send(`{"jsonrpc":"2.0","id":6,"method":"nope"}`)
	if e := c.next()["error"].(map[string]any); e["code"].(float64) != -32601 {
		t.Fatalf("unknown method = %v", e)
	}
}
