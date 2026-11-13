package agent

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"zapfast/abi"
)

// The socket's protocol: a caller connects, writes one Request as a line of
// JSON, reads one Reply as a line of JSON, and the connection ends. A Wait
// holds its connection until news comes, its time is up, or the caller goes.

// Request is one thing an agent asks.
type Request struct {
	// Op is "chats", "read", "search", "send" or "wait".
	Op string `json:"op"`
	// Token narrows the caller to the one chat a Wake started it for.
	Token string `json:"token,omitempty"`
	// Agent names the caller in the activity list (the MCP client's name).
	Agent string `json:"agent,omitempty"`
	// Chat is a chat's handle, from "chats".
	Chat    string `json:"chat,omitempty"`
	Before  string `json:"before,omitempty"`
	After   string `json:"after,omitempty"`
	Limit   int    `json:"limit,omitempty"`
	Query   string `json:"query,omitempty"`
	Text    string `json:"text,omitempty"`
	ReplyTo string `json:"reply_to,omitempty"`
	// Timeout is how long a wait may last, in seconds.
	Timeout int `json:"timeout,omitempty"`
}

// Reply answers a Request: Error, or the fields of its Op.
type Reply struct {
	Error    string     `json:"error,omitempty"`
	Chats    []ChatInfo `json:"chats,omitempty"`
	Chat     string     `json:"chat,omitempty"`
	Messages []Msg      `json:"messages,omitempty"`
	// More: earlier messages are stored; ask again with Before set to the
	// first message's id.
	More bool `json:"more,omitempty"`
	// Status of a send: "sent", or "waiting_for_approval".
	Status string `json:"status,omitempty"`
	Note   string `json:"note,omitempty"`
	// TimedOut: a wait ended with nothing new.
	TimedOut bool `json:"timed_out,omitempty"`
}

// ChatInfo is a chat an agent may use.
type ChatInfo struct {
	Chat string `json:"chat"`
	Name string `json:"name"`
	// Kind is "direct" or "group".
	Kind string `json:"kind"`
	// Access is "read", "reply" (replies go out at once) or
	// "reply_with_approval" (the owner sees each reply first).
	Access string `json:"access"`
	// Instructions are the owner's, for this chat.
	Instructions string `json:"instructions,omitempty"`
}

const (
	readDefault = 30
	readMax     = 100
	waitDefault = 50
	waitMax     = 100
	// requestMax bounds a request line, in bytes: a reply of TextMax bytes
	// with every byte escaped fits several times over.
	requestMax = 256 << 10
	// connsMax is how many callers are answered at once; more are hung up
	// on.
	connsMax = 64
)

// encode is v as a line of JSON, with <, > and & as they are: what an agent
// reads is text, not HTML.
func encode(v any) ([]byte, error) {
	var b bytes.Buffer
	enc := json.NewEncoder(&b)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(v); err != nil {
		return nil, err
	}
	return b.Bytes(), nil // Encode ends the line
}

func info(k key, e Entry) ChatInfo {
	c := ChatInfo{Chat: Handle(k.account, k.chat), Name: e.Name, Kind: "direct", Access: "read", Instructions: e.Grant.Brief}
	if e.Group {
		c.Kind = "group"
	}
	if e.Grant.Write {
		c.Access = "reply_with_approval"
		if e.Grant.Auto {
			c.Access = "reply"
		}
	}
	return c
}

// listen opens the socket; mu is held. A socket left by a copy of the app
// that is gone is replaced; one that answers is another copy's, and is left.
func (s *Server) listen() error {
	path := s.opts.Socket
	if c, err := net.DialTimeout("unix", path, 200*time.Millisecond); err == nil {
		c.Close()
		return errors.New("another copy of Zap answers the agents' socket")
	}
	os.Remove(path)
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	ln, err := net.Listen("unix", path)
	if err != nil {
		return err
	}
	if err := os.Chmod(path, 0o600); err != nil {
		ln.Close()
		os.Remove(path)
		return err
	}
	s.ln = ln
	s.wg.Add(1)
	go s.accept(ln)
	return nil
}

func (s *Server) unlisten() {
	if s.ln != nil {
		s.ln.Close()
		os.Remove(s.opts.Socket)
		s.ln = nil
	}
}

func (s *Server) accept(ln net.Listener) {
	defer s.wg.Done()
	room := make(chan struct{}, connsMax)
	for {
		c, err := ln.Accept()
		if err != nil {
			if errors.Is(err, net.ErrClosed) || s.ctx.Err() != nil {
				return
			}
			// Out of files, or the like: the door stays, and tries again.
			select {
			case <-time.After(200 * time.Millisecond):
				continue
			case <-s.ctx.Done():
				return
			}
		}
		select {
		case room <- struct{}{}:
		default:
			c.Close()
			continue
		}
		s.wg.Add(1)
		go func() {
			defer s.wg.Done()
			defer func() { <-room }()
			defer c.Close()
			s.serve(c)
		}()
	}
}

// serve answers the one request of a connection.
func (s *Server) serve(c net.Conn) {
	c.SetReadDeadline(time.Now().Add(10 * time.Second))
	r := bufio.NewReaderSize(c, 4096)
	line, err := readLine(r, requestMax)
	var req Request
	var rep Reply
	switch {
	case errors.Is(err, errLong):
		rep.Error = "the request is too long"
	case err != nil:
		return
	case json.Unmarshal(line, &req) != nil:
		rep.Error = "the request is not JSON"
	default:
		c.SetReadDeadline(time.Time{})
		// The caller hanging up ends a wait.
		ctx, cancel := context.WithCancel(s.ctx)
		go func() {
			var b [1]byte
			c.Read(b[:])
			cancel()
		}()
		rep = s.Do(ctx, req)
		cancel()
	}
	out, err := encode(rep)
	if err != nil {
		slog.Warn("zapfast agent: a reply did not encode", "err", err)
		return
	}
	c.SetWriteDeadline(time.Now().Add(10 * time.Second))
	c.Write(out)
}

var errLong = errors.New("the line is too long")

func readLine(r *bufio.Reader, max int) ([]byte, error) {
	var line []byte
	for {
		part, more, err := r.ReadLine()
		if err != nil {
			return nil, err
		}
		line = append(line, part...)
		if len(line) > max {
			return nil, errLong
		}
		if !more {
			return line, nil
		}
	}
}

// Do answers one request. It may block: a read on the account's core, a
// wait on its time.
func (s *Server) Do(ctx context.Context, req Request) Reply {
	var rep Reply
	var err error
	req.Agent = label(req.Agent, agentMax) // the caller's own word for itself
	switch req.Op {
	case "chats":
		rep, err = s.chats(req)
	case "read":
		rep, err = s.read(ctx, req)
	case "search":
		rep, err = s.search(ctx, req)
	case "send":
		rep, err = s.send(ctx, req)
	case "wait":
		rep, err = s.wait(ctx, req)
	default:
		err = errors.New("unknown request")
	}
	if err != nil {
		return Reply{Error: err.Error()}
	}
	return rep
}

func (s *Server) chats(req Request) (Reply, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	keys, entries, err := s.scopeLocked(req.Token)
	if err != nil {
		return Reply{}, err
	}
	rep := Reply{Chats: make([]ChatInfo, len(keys))}
	for i := range keys {
		rep.Chats[i] = info(keys[i], entries[i])
	}
	if len(keys) == 0 {
		rep.Note = "No chat has AI access. The owner turns it on per chat in Zap: right-click a chat, AI access."
	}
	return rep, nil
}

func (s *Server) lookup(req Request) (key, Entry, Account, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.grantLocked(req.Token, req.Chat)
}

func rendered(page []abi.Message, e Entry) []Msg {
	out := make([]Msg, len(page))
	for i := range page {
		out[i] = render(&page[i], e, time.Local)
	}
	return out
}

func (s *Server) read(ctx context.Context, req Request) (Reply, error) {
	k, e, core, err := s.lookup(req)
	if err != nil {
		return Reply{}, err
	}
	limit := req.Limit
	if limit <= 0 {
		limit = readDefault
	}
	page, more, err := core.Page(ctx, k.chat, req.Before, min(limit, readMax))
	if err != nil {
		return Reply{}, errors.New("the chat could not be read")
	}
	s.note(Did{Account: k.account, Chat: k.chat, Name: e.Name, Kind: DidRead, Agent: req.Agent, N: len(page)})
	return Reply{Chat: req.Chat, Messages: rendered(page, e), More: more}, nil
}

func (s *Server) search(ctx context.Context, req Request) (Reply, error) {
	k, e, core, err := s.lookup(req)
	if err != nil {
		return Reply{}, err
	}
	if req.Query == "" {
		return Reply{}, errors.New("the search needs words to look for")
	}
	limit := req.Limit
	if limit <= 0 {
		limit = readDefault
	}
	hits, err := core.Search(ctx, k.chat, req.Query, min(limit, readMax))
	if err != nil {
		return Reply{}, errors.New("the chat could not be searched")
	}
	s.note(Did{Account: k.account, Chat: k.chat, Name: e.Name, Kind: DidSearch, Agent: req.Agent, N: len(hits)})
	return Reply{Chat: req.Chat, Messages: rendered(hits, e)}, nil
}

// quotable reports whether id is one of chat's recent messages: a reply
// may quote only what is stored, or the core refuses it after the fact.
func quotable(ctx context.Context, core Account, chat, id string) bool {
	if len(id) > idMax {
		return false
	}
	page, _, err := core.Page(ctx, chat, "", readMax)
	return err == nil && slices.ContainsFunc(page, func(m abi.Message) bool { return string(m.ID) == id })
}

// send carries out a reply, or keeps it for the owner.
func (s *Server) send(ctx context.Context, req Request) (Reply, error) {
	if strings.TrimSpace(req.Text) == "" {
		return Reply{}, errors.New("the message is empty")
	}
	if len(req.Text) > TextMax {
		return Reply{}, errors.New("the message is too long")
	}
	if req.ReplyTo != "" {
		k, _, core, err := s.lookup(req)
		if err != nil {
			return Reply{}, err
		}
		if !quotable(ctx, core, k.chat, req.ReplyTo) {
			return Reply{}, errors.New("reply_to is not the id of one of this chat's last messages. Read the chat again, or send without it")
		}
	}
	s.mu.Lock()
	k, e, core, err := s.grantLocked(req.Token, req.Chat)
	if err != nil {
		s.mu.Unlock()
		return Reply{}, err
	}
	if !e.Grant.Write {
		s.mu.Unlock()
		return Reply{}, errors.New("this chat is read only: the owner did not allow replies")
	}
	if !e.Grant.Auto {
		n := 0
		for _, p := range s.pending {
			if p.Account == k.account && p.Chat == k.chat {
				n++
			}
		}
		if n >= proposalsMax {
			s.mu.Unlock()
			return Reply{}, errors.New("the owner has not answered your earlier replies to this chat yet. Do not send more until they do")
		}
		s.nextID++
		s.pending = append(s.pending, Proposal{
			ID: s.nextID, Account: k.account, Chat: k.chat, Name: e.Name,
			Text: req.Text, Quoting: req.ReplyTo, Agent: req.Agent, At: s.now(),
		})
		s.noteLocked(Did{Account: k.account, Chat: k.chat, Name: e.Name, Kind: DidPropose, Agent: req.Agent})
		s.mu.Unlock()
		s.changed()
		return Reply{Chat: req.Chat, Status: "waiting_for_approval",
			Note: "Not sent yet: the owner sees this reply in Zap and decides. Do not send it again."}, nil
	}
	if a := s.accounts[k.account]; a != nil && a.offline {
		s.mu.Unlock()
		return Reply{}, errors.New("Zap is not connected to WhatsApp right now, so nothing was sent. Try again later")
	}
	now := s.now()
	recent := slices.DeleteFunc(s.sent[k], func(t int64) bool { return now-t >= 3600 })
	if len(recent) >= s.cfg.Cap {
		s.sent[k] = recent
		s.noteLocked(Did{Account: k.account, Chat: k.chat, Name: e.Name, Kind: DidRefuse, Agent: req.Agent})
		s.mu.Unlock()
		s.changed()
		return Reply{}, errors.New("this chat's hourly limit of unattended replies is reached. Stop; the owner can raise it in Zap")
	}
	s.sent[k] = append(recent, now)
	s.mu.Unlock()
	if err := core.Send(k.chat, req.Text, req.ReplyTo); err != nil {
		return Reply{}, errors.New("Zap refused the message: " + err.Error())
	}
	s.note(Did{Account: k.account, Chat: k.chat, Name: e.Name, Kind: DidSend, Agent: req.Agent})
	return Reply{Chat: req.Chat, Status: "sent"}, nil
}

// Incoming tells the Server a message from someone else arrived in a chat:
// the waits on it end, and its Wake is due.
func (s *Server) Incoming(account, chat string) {
	k := key{account, chat}
	s.mu.Lock()
	a := s.accounts[account]
	if !s.cfg.On || a == nil {
		s.mu.Unlock()
		return
	}
	e, ok := a.entries[chat]
	if !ok || !e.Grant.Live(s.now()) {
		s.mu.Unlock()
		return
	}
	s.seq[k]++
	close(s.news)
	s.news = make(chan struct{})
	s.mu.Unlock()
	if e.Grant.Wake {
		s.due(k)
	}
}

// wait holds until a message arrives in the chat named (or, with none, in
// any chat the caller may use), then answers with what is new.
func (s *Server) wait(ctx context.Context, req Request) (Reply, error) {
	timeout := req.Timeout
	if timeout <= 0 {
		timeout = waitDefault
	}
	deadline := time.NewTimer(time.Duration(min(timeout, waitMax)) * time.Second)
	defer deadline.Stop()

	s.mu.Lock()
	var keys []key
	var err error
	if req.Chat != "" {
		var k key
		if k, _, _, err = s.grantLocked(req.Token, req.Chat); err == nil {
			keys = []key{k}
		}
	} else {
		keys, _, err = s.scopeLocked(req.Token)
	}
	if err != nil {
		s.mu.Unlock()
		return Reply{}, err
	}
	if len(keys) == 0 {
		s.mu.Unlock()
		return Reply{}, errNoChat
	}
	seen := make([]uint64, len(keys))
	for i, k := range keys {
		seen[i] = s.seq[k]
	}
	news := s.news
	s.mu.Unlock()

	// What came after the caller's last message is news already.
	if req.Chat != "" && req.After != "" {
		if rep, err := s.newer(ctx, req, req.After); err != nil || len(rep.Messages) > 0 {
			return rep, err
		}
	}
	for {
		select {
		case <-ctx.Done():
			return Reply{TimedOut: true}, nil
		case <-deadline.C:
			return Reply{TimedOut: true, Note: "Nothing new. Call again to keep waiting."}, nil
		case <-news:
		}
		s.mu.Lock()
		moved := -1
		for i, k := range keys {
			if s.seq[k] != seen[i] {
				moved, seen[i] = i, s.seq[k]
				break
			}
		}
		news = s.news
		s.mu.Unlock()
		if moved < 0 {
			continue
		}
		r := req
		r.Chat = Handle(keys[moved].account, keys[moved].chat)
		// The message is announced once it is stored, so the page has it.
		rep, err := s.newer(ctx, r, req.After)
		if err != nil {
			return rep, err
		}
		if len(rep.Messages) == 0 {
			continue
		}
		return rep, nil
	}
}

// newer are the chat's messages after the one named: when it is not among
// the newest, or none is named, the last message alone.
func (s *Server) newer(ctx context.Context, req Request, after string) (Reply, error) {
	k, e, core, err := s.lookup(req)
	if err != nil {
		return Reply{}, err
	}
	page, _, err := core.Page(ctx, k.chat, "", readDefault)
	if err != nil {
		return Reply{}, errors.New("the chat could not be read")
	}
	from := max(len(page)-1, 0)
	if after != "" {
		for i := range page {
			if string(page[i].ID) == after {
				from = i + 1
				break
			}
		}
	}
	page = page[from:]
	if len(page) > 0 {
		s.note(Did{Account: k.account, Chat: k.chat, Name: e.Name, Kind: DidRead, Agent: req.Agent, N: len(page)})
	}
	return Reply{Chat: req.Chat, Messages: rendered(page, e)}, nil
}

// Ask sends one request to the Server listening at socket and reads its
// reply: what the MCP server (`zapfast mcp`) does for each tool call.
func Ask(ctx context.Context, socket string, req Request) (Reply, error) {
	var d net.Dialer
	c, err := d.DialContext(ctx, "unix", socket)
	if err != nil {
		return Reply{}, err
	}
	defer c.Close()
	stop := context.AfterFunc(ctx, func() { c.Close() })
	defer stop()
	line, err := encode(req)
	if err != nil {
		return Reply{}, err
	}
	if _, err := c.Write(line); err != nil {
		return Reply{}, err
	}
	out, err := readLine(bufio.NewReaderSize(c, 64<<10), 8<<20)
	if err != nil {
		return Reply{}, err
	}
	var rep Reply
	if err := json.Unmarshal(out, &rep); err != nil {
		return Reply{}, err
	}
	return rep, nil
}
