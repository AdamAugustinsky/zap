// Package agent is Zap's door for AI agents: the one way a program outside
// the app (Claude Code, Codex, opencode, through the MCP server, `zapfast mcp`) reads or writes
// a chat. The app's owner opens it chat by chat; a chat without a grant does
// not exist here, whatever the caller asks.
//
// The Server listens on a Unix socket in the app's data folder (owner only,
// no network), answers one request a connection (wire.go), keeps the replies
// that wait for the owner's approval, counts what was sent, and starts the
// owner's agent when a message arrives in a chat that asks for it (wake.go).
// It holds no message: every read goes to the account's core, and nothing a
// message says is logged.
package agent

import (
	"context"
	"errors"
	"net"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"zapfast/abi"
)

// Grant is what one chat lets agents do. The zero value reads only.
type Grant struct {
	// Write lets an agent reply; without it the chat is read only.
	Write bool `json:"write,omitempty"`
	// Auto sends an agent's reply at once. Without it the reply waits for
	// the owner (Proposal).
	Auto bool `json:"auto,omitempty"`
	// Wake starts the owner's agent when a message arrives.
	Wake bool `json:"wake,omitempty"`
	// Brief is what the owner asked the agent to do in this chat.
	Brief string `json:"brief,omitempty"`
	// Expires ends the grant (Unix seconds); 0 never.
	Expires int64 `json:"expires,omitempty"`
}

// Live reports whether the grant still holds at now (Unix seconds).
func (g Grant) Live(now int64) bool { return g.Expires == 0 || now < g.Expires }

// BriefMax bounds a grant's brief, in bytes.
const BriefMax = 4000

// SocketName is the socket's file in the app's data folder.
const SocketName = "agent.sock"

// Entry is one chat with a grant, as the app names it.
type Entry struct {
	Chat  string // the chat's id
	Name  string // as the chat list shows it
	Group bool
	Grant Grant
}

// Account is one account's core, as the Server uses it. Its methods are
// called from the Server's goroutines and may block.
type Account interface {
	// Page reads at most limit stored messages of chat, oldest first: the
	// newest, or those before the message `before`. more: earlier ones are
	// stored. It tells the UI nothing.
	Page(ctx context.Context, chat, before string, limit int) (page []abi.Message, more bool, err error)
	// Search finds at most limit of chat's messages matching query, newest
	// first.
	Search(ctx context.Context, chat, query string, limit int) ([]abi.Message, error)
	// Send queues text for chat, as a message typed in the app; quoting
	// names the message it answers, or is empty.
	Send(chat, text, quoting string) error
}

// Config is the owner's settings for every chat.
type Config struct {
	// On opens the door: off, the socket is gone and nothing is answered.
	On bool
	// Cap is how many replies an hour an agent may send to one chat on its
	// own; 0: DefaultCap.
	Cap int
	// Runner is the agent a chat's Wake starts.
	Runner Runner
}

// DefaultCap is the hourly limit of unattended replies to one chat.
const DefaultCap = 20

// Proposal is a reply an agent wants to send to a chat whose grant is not
// Auto: it waits for the owner.
type Proposal struct {
	ID      uint64
	Account string
	Chat    string
	Name    string
	Text    string
	Quoting string
	Agent   string
	At      int64
}

// Kind says what an activity entry records.
type Kind uint8

const (
	DidRead Kind = iota + 1
	DidSearch
	DidSend     // sent on its own
	DidPropose  // waits for the owner
	DidApprove  // the owner sent a proposal
	DidDiscard  // the owner dropped a proposal
	DidRefuse   // a reply over the hourly limit
	RunStarted  // a Wake started the runner
	RunFinished // it ended
	RunFailed   // it could not start, failed or ran out of time
	RunSkipped  // too many runs this hour
	DidEdit     // the owner took a proposal to rewrite it
)

// Did is one line of the activity list. It never holds what a message said.
type Did struct {
	At      int64
	Account string
	Chat    string
	Name    string
	Kind    Kind
	Agent   string
	// N counts messages read or found; Note is a short reason for a failed
	// or skipped run.
	N    int
	Note string
}

const (
	activityMax = 200
	// proposalsMax is how many replies may wait for the owner in one chat.
	proposalsMax = 5
	// TextMax bounds a reply, in bytes.
	TextMax = 16000
	// nameMax and agentMax bound a chat's name and a caller's, in bytes.
	nameMax  = 120
	agentMax = 40
	// idMax bounds a message id (the records' own bound).
	idMax = 128
)

// label is s as one line of at most max bytes: no control character, no
// line break, the spaces around it gone. Names come from other people (a
// group's subject, a profile name) and from the caller (an MCP client's
// name); none may break a line where it is shown or told to an agent.
func label(s string, max int) string {
	s = strings.Map(func(r rune) rune {
		if r < ' ' || r == 0x7f || r == '\u2028' || r == '\u2029' || r == '\u0085' {
			return ' '
		}
		return r
	}, s)
	return strings.TrimSpace(clip(strings.Join(strings.Fields(s), " "), max))
}

// Options are a Server's fixed parts.
type Options struct {
	// Socket is where the Server listens while Config.On; empty: nowhere
	// (Do still answers).
	Socket string
	// Changed is called, from any goroutine, when Pending, Activity or
	// Reports would answer differently.
	Changed func()
	// Run starts the runner for a Wake; nil: Exec.
	Run func(ctx context.Context, r Run) (report string, err error)
	// Now is the clock; nil: time.Now.
	Now func() time.Time
	// Quiet is how long a chat must be silent before its Wake runs, and
	// Patience the longest a run waits for that; 0: 8 s and 45 s.
	Quiet, Patience time.Duration
}

// Server is the door. Its methods are safe from any goroutine.
type Server struct {
	opts Options

	mu       sync.Mutex
	cfg      Config
	ln       net.Listener
	accounts map[string]*account
	tokens   map[string]scope
	pending  []Proposal
	nextID   uint64
	activity []Did
	sent     map[key][]int64 // unattended replies, by time
	seq      map[key]uint64  // messages announced, for Wait
	news     chan struct{}   // closed and replaced when a seq moves
	wakes    map[key]*wake
	reports  map[key]Report
	closed   bool

	// gen counts the changes Options.Changed announced (Gen).
	gen atomic.Uint64

	wg     sync.WaitGroup
	ctx    context.Context
	cancel context.CancelFunc
}

type account struct {
	core    Account
	entries map[string]Entry
	// offline: the account is not connected to WhatsApp, so a message
	// queued now would be refused (SetOffline).
	offline bool
}

// key names a chat of an account.
type key struct{ account, chat string }

// scope is what a token is good for: one chat.
type scope = key

// New returns a closed door: Configure opens it.
func New(opts Options) *Server {
	if opts.Now == nil {
		opts.Now = time.Now
	}
	if opts.Quiet == 0 {
		opts.Quiet = 8 * time.Second
	}
	if opts.Patience == 0 {
		opts.Patience = 45 * time.Second
	}
	s := &Server{
		opts: opts, accounts: map[string]*account{}, tokens: map[string]scope{},
		sent: map[key][]int64{}, seq: map[key]uint64{}, news: make(chan struct{}),
		wakes: map[key]*wake{}, reports: map[key]Report{},
	}
	s.ctx, s.cancel = context.WithCancel(context.Background())
	return s
}

func (s *Server) now() int64 { return s.opts.Now().Unix() }

// Gen moves whenever what the Server shows the owner changed: the waiting
// proposals, the activity, a run's state or report.
func (s *Server) Gen() uint64 { return s.gen.Load() }

func (s *Server) changed() {
	s.gen.Add(1)
	if s.opts.Changed != nil {
		s.opts.Changed()
	}
}

// Configure applies the owner's settings: the socket opens with On and goes
// with it. Turning it off drops every waiting proposal and stops every run.
func (s *Server) Configure(c Config) error {
	if c.Cap <= 0 {
		c.Cap = DefaultCap
	}
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return nil
	}
	was := s.cfg.On
	s.cfg = c
	var err error
	switch {
	case c.On && s.ln == nil && s.opts.Socket != "":
		err = s.listen()
	case !c.On && s.ln != nil:
		s.unlisten()
	}
	if !c.On {
		s.pending = nil
		// The runs end, and a chat's next message waits its quiet again.
		for k, w := range s.wakes {
			w.stop()
			delete(s.wakes, k)
		}
	}
	s.mu.Unlock()
	if was != c.On {
		s.changed()
	}
	return err
}

// Config is the settings in force.
func (s *Server) Config() Config {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.cfg
}

// Attach gives the Server an account's core; Detach takes it back.
func (s *Server) Attach(id string, core Account) {
	s.mu.Lock()
	defer s.mu.Unlock()
	a := s.accounts[id]
	if a == nil {
		a = &account{entries: map[string]Entry{}}
		s.accounts[id] = a
	}
	a.core = core
}

// Detach forgets an account: its core, its chats and what waited for them.
func (s *Server) Detach(id string) {
	s.mu.Lock()
	delete(s.accounts, id)
	s.dropLocked(func(k key) bool { return k.account == id })
	s.mu.Unlock()
	s.changed()
}

// SetChats replaces the chats of account id that have a grant. A chat left
// out loses its waiting proposals, its tokens and its run.
func (s *Server) SetChats(id string, entries []Entry) {
	s.mu.Lock()
	a := s.accounts[id]
	if a == nil {
		a = &account{}
		s.accounts[id] = a
	}
	a.entries = make(map[string]Entry, len(entries))
	for _, e := range entries {
		// A name is other people's words (a group's subject, a profile
		// name): one line of it, wherever it is shown or told to an agent.
		e.Name = label(e.Name, nameMax)
		a.entries[e.Chat] = e
	}
	s.dropLocked(func(k key) bool {
		if k.account != id {
			return false
		}
		e, ok := a.entries[k.chat]
		return !ok || !e.Grant.Live(s.now())
	})
	// A chat that no longer writes keeps no proposal.
	s.pending = slices.DeleteFunc(s.pending, func(p Proposal) bool {
		return p.Account == id && !a.entries[p.Chat].Grant.Write
	})
	s.mu.Unlock()
	s.changed()
}

// SetOffline says whether account id is cut off from WhatsApp: a reply sent
// on its own is refused then, instead of being queued for a core that would
// hand it back to the owner's message field.
func (s *Server) SetOffline(id string, offline bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if a := s.accounts[id]; a != nil {
		a.offline = offline
	}
}

// Forget drops what the activity list and the reports say of a chat: one
// that became locked is named nowhere.
func (s *Server) Forget(account, chat string) {
	s.mu.Lock()
	s.activity = slices.DeleteFunc(s.activity, func(d Did) bool { return d.Account == account && d.Chat == chat })
	delete(s.reports, key{account, chat})
	s.mu.Unlock()
	s.changed()
}

// dropLocked forgets what is kept for the chats gone picks, and stops their
// runs.
func (s *Server) dropLocked(gone func(key) bool) {
	s.pending = slices.DeleteFunc(s.pending, func(p Proposal) bool { return gone(key{p.Account, p.Chat}) })
	for t, k := range s.tokens {
		if gone(k) {
			delete(s.tokens, t)
		}
	}
	for k, w := range s.wakes {
		if gone(k) {
			w.stop()
			delete(s.wakes, k)
		}
	}
	for k := range s.reports {
		if gone(k) {
			delete(s.reports, k)
		}
	}
}

// Pending are the replies waiting for the owner, oldest first.
func (s *Server) Pending() []Proposal {
	s.mu.Lock()
	defer s.mu.Unlock()
	return slices.Clone(s.pending)
}

// Resolve answers proposal id: send carries it out, as the owner's own
// message; otherwise it is dropped. text, when not empty, replaces the
// agent's words. It reports the proposal, and false when it is gone. A
// reply that could not be sent stays, for another try. The owner's answers
// come one at a time (the app's UI thread).
func (s *Server) Resolve(id uint64, send bool, text string) (Proposal, bool, error) {
	if !send {
		p, ok := s.take(id, DidDiscard)
		return p, ok, nil
	}
	s.mu.Lock()
	i := slices.IndexFunc(s.pending, func(p Proposal) bool { return p.ID == id })
	if i < 0 {
		s.mu.Unlock()
		return Proposal{}, false, nil
	}
	p := s.pending[i]
	var core Account
	offline := false
	if a := s.accounts[p.Account]; a != nil {
		core, offline = a.core, a.offline
	}
	s.mu.Unlock()
	switch {
	case core == nil:
		return p, true, errors.New("the account is not open")
	case offline:
		return p, true, errors.New("the account is not connected")
	}
	if text != "" {
		p.Text = text
	}
	if err := core.Send(p.Chat, p.Text, p.Quoting); err != nil {
		return p, true, err
	}
	s.take(id, DidApprove)
	return p, true, nil
}

// Take hands proposal id to the owner to rewrite: it waits no longer, and
// what goes out is theirs to send. It reports false when it is gone.
func (s *Server) Take(id uint64) (Proposal, bool) { return s.take(id, DidEdit) }

// take removes proposal id and notes what became of it.
func (s *Server) take(id uint64, kind Kind) (Proposal, bool) {
	s.mu.Lock()
	i := slices.IndexFunc(s.pending, func(p Proposal) bool { return p.ID == id })
	if i < 0 {
		s.mu.Unlock()
		return Proposal{}, false
	}
	p := s.pending[i]
	s.pending = slices.Delete(s.pending, i, i+1)
	s.noteLocked(Did{Account: p.Account, Chat: p.Chat, Name: p.Name, Kind: kind, Agent: p.Agent})
	s.mu.Unlock()
	s.changed()
	return p, true
}

// Activity is what agents did, newest first.
func (s *Server) Activity() []Did {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := slices.Clone(s.activity)
	slices.Reverse(out)
	return out
}

func (s *Server) noteLocked(d Did) {
	if d.At == 0 {
		d.At = s.now()
	}
	d.Note = clip(d.Note, 200)
	if n := len(s.activity); n >= activityMax {
		s.activity = append(s.activity[:0], s.activity[n-activityMax+1:]...)
	}
	s.activity = append(s.activity, d)
}

func (s *Server) note(d Did) {
	s.mu.Lock()
	s.noteLocked(d)
	s.mu.Unlock()
	s.changed()
}

// Close shuts the door for good: the socket, the runs and the waits.
func (s *Server) Close() {
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return
	}
	s.closed = true
	s.unlisten()
	for _, w := range s.wakes {
		w.stop()
	}
	s.mu.Unlock()
	s.cancel()
	s.wg.Wait()
}

// Handle names a chat to an agent: the account, a slash, the chat's id.
func Handle(account, chat string) string { return account + "/" + chat }

func splitHandle(h string) (key, bool) {
	a, c, ok := strings.Cut(h, "/")
	if !ok || a == "" || c == "" {
		return key{}, false
	}
	return key{a, c}, true
}

// errNoChat answers a chat that is unknown, has no grant, or is outside the
// caller's token: one answer for all, so a caller learns nothing of a chat
// it was not given.
var errNoChat = errors.New("no such chat, or this chat has no AI access. list_chats shows the chats you can use")

// errOff answers every request while the door is shut.
var errOff = errors.New("AI access is turned off in Zap")

// grantLocked finds the chat a request names, for a caller holding token.
func (s *Server) grantLocked(token, handle string) (key, Entry, Account, error) {
	if !s.cfg.On {
		return key{}, Entry{}, nil, errOff
	}
	k, ok := splitHandle(handle)
	if !ok {
		return key{}, Entry{}, nil, errNoChat
	}
	if token != "" {
		if sc, ok := s.tokens[token]; !ok || sc != k {
			return key{}, Entry{}, nil, errNoChat
		}
	}
	a := s.accounts[k.account]
	if a == nil || a.core == nil {
		return key{}, Entry{}, nil, errNoChat
	}
	e, ok := a.entries[k.chat]
	if !ok || !e.Grant.Live(s.now()) {
		return key{}, Entry{}, nil, errNoChat
	}
	return k, e, a.core, nil
}

// scopeLocked lists the chats a caller holding token may use, by account
// then name.
func (s *Server) scopeLocked(token string) ([]key, []Entry, error) {
	if !s.cfg.On {
		return nil, nil, errOff
	}
	var keys []key
	var entries []Entry
	now := s.now()
	if token != "" {
		sc, ok := s.tokens[token]
		if !ok {
			return nil, nil, errors.New("this run's access has ended")
		}
		if a := s.accounts[sc.account]; a != nil && a.core != nil {
			if e, ok := a.entries[sc.chat]; ok && e.Grant.Live(now) {
				return []key{sc}, []Entry{e}, nil
			}
		}
		return nil, nil, nil
	}
	for id, a := range s.accounts {
		if a.core == nil {
			continue
		}
		for _, e := range a.entries {
			if e.Grant.Live(now) {
				keys = append(keys, key{id, e.Chat})
				entries = append(entries, e)
			}
		}
	}
	order := make([]int, len(keys))
	for i := range order {
		order[i] = i
	}
	slices.SortFunc(order, func(i, j int) int {
		if c := strings.Compare(keys[i].account, keys[j].account); c != 0 {
			return c
		}
		if c := strings.Compare(strings.ToLower(entries[i].Name), strings.ToLower(entries[j].Name)); c != 0 {
			return c
		}
		return strings.Compare(keys[i].chat, keys[j].chat)
	})
	ks, es := make([]key, len(keys)), make([]Entry, len(keys))
	for n, i := range order {
		ks[n], es[n] = keys[i], entries[i]
	}
	return ks, es, nil
}

// clip cuts s to at most n bytes on a rune boundary.
func clip(s string, n int) string {
	if len(s) <= n {
		return s
	}
	for n > 0 && s[n]&0xC0 == 0x80 {
		n--
	}
	return s[:n]
}
