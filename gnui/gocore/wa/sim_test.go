package wa

// The deterministic simulation of the WhatsApp engine, after TigerBeetle's
// VOPR and Turso's simulator: one seed drives a whole run of whatsmeow
// events and user commands into a real engine, Conn, archive and whatsmeow
// store (SQLCipher in a temporary folder, the fake client of fake_test.go),
// with faults injected, and an oracle checks the archive after every step
// and every reopen. Time is testing/synctest's: the engine's timers, the
// sync's quiet period and the history requests' patience run on the
// bubble's clock, so an hour of simulated time costs nothing.
//
// A failing run prints its seed and the command that replays it:
//
//	go test ./gocore/wa -run 'TestSimSeed$' -sim.seed=N [-sim.steps=S] [-sim.mode=M]
//
// TestSim runs a few fixed seeds in every `go test`; TestSimExplore runs
// random seeds for -sim.for (scripts/sim-explore.sh).
//
// What is acknowledged: an event from WhatsApp is acknowledged when the
// handler the engine registered returns, because whatsmeow then sends its
// receipt and the server never delivers it again; a message of ours is
// acknowledged once the UI was shown it. All data is synthetic.

import (
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io/fs"
	"log/slog"
	mrand "math/rand/v2"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"

	"go.mau.fi/whatsmeow/proto/waCommon"
	"go.mau.fi/whatsmeow/proto/waE2E"
	"go.mau.fi/whatsmeow/proto/waHistorySync"
	"go.mau.fi/whatsmeow/proto/waSyncAction"
	"go.mau.fi/whatsmeow/proto/waWeb"
	"go.mau.fi/whatsmeow/types"
	"go.mau.fi/whatsmeow/types/events"
	"google.golang.org/protobuf/proto"

	"zapfast/abi"
	"zapfast/gocore/store"
	"zapfast/gocore/store/sqlite"
)

var (
	simSeed  = flag.Uint64("sim.seed", 0, "TestSimSeed: the seed to run")
	simSteps = flag.Int("sim.steps", 0, "steps of a simulated run (0: the test's own)")
	simFor   = flag.Duration("sim.for", 0, "TestSimExplore: run random seeds for this long")
	simMode  = flag.String("sim.mode", "default", "the faults: default; queued (a crash with acknowledged events still queued), ingest (writes failing while WhatsApp's events are applied), lostack (commits that hold but report a failure), or ack (all three)")
)

// simConfig chooses a run's faults.
type simConfig struct {
	mode  string
	steps int
	// crash kills the process between steps and reopens the store from
	// what was on disk, at times with a torn write at the WAL's end.
	crash bool
	// busyCrash kills it with acknowledged events still queued for the
	// engine.
	busyCrash bool
	// commandFaults fail writes while a user command runs; ingestFaults
	// while an event from WhatsApp is applied.
	commandFaults, ingestFaults bool
	// lostAcks lets a COMMIT hold yet report a failure (fsync's ambiguity).
	lostAcks bool
	// network drops, repeats and reorders events, and takes the link down.
	network bool
}

func simConfigOf(mode string, steps int) simConfig {
	c := simConfig{mode: mode, steps: steps, crash: true, commandFaults: true, network: true}
	switch mode {
	case "default":
	case "queued":
		c.busyCrash = true
	case "ingest":
		c.ingestFaults = true
	case "lostack":
		c.lostAcks = true
	case "ack":
		c.busyCrash, c.ingestFaults, c.lostAcks = true, true, true
	default:
		panic("sim: unknown mode " + mode)
	}
	return c
}

// simMsg is the oracle's view of one message.
type simMsg struct {
	chat   *simChat
	id     string
	sender types.JID
	fromMe bool
	at     time.Time
	text   string
	// acked: the engine was handed it (or the UI showed ours); gone: it was
	// deleted here; revoked: deleted for everyone.
	acked, gone, revoked bool
	// reactions are the acknowledged ones, by sender.
	reactions map[string]string
	// receipt is the highest receipt acknowledged for a message of ours
	// (1 delivered, 2 read); rank the highest status the archive showed.
	receipt, rank int
}

type simChat struct {
	jid     types.JID
	group   bool
	members []types.JID
	name    string // a group's, once renamed
	unread  int
	order   []string // the archive's order of its messages at the last check
}

func (c *simChat) id() string { return c.jid.String() }

type sim struct {
	t    *testing.T
	seed uint64
	cfg  simConfig
	rng  *mrand.Rand

	h            *harness
	keys         *memKeys
	state, cache string
	reader       *sql.DB

	chats  []*simChat
	msgs   map[[2]string]*simMsg
	list   []*simMsg
	nextID int
	// lastSent is the fake's last message id: a new fake (after a crash)
	// goes on from it, as whatsmeow's random ids never repeat.
	lastSent int
	online   bool
	held     []any // events WhatsApp holds while the link is down

	ui    map[string]abi.Chat // what the UI was told of each chat
	shown map[[2]string]bool  // messages of ours the UI was shown

	// The fault hook's state: armed while a step may fail writes.
	frng    *mrand.Rand
	fmu     sync.Mutex
	armed   atomic.Bool
	faulted atomic.Int64

	step   int
	counts map[string]int // actions run, and faults injected
	trail  []string
	digest []byte
}

// runSim runs one seed and returns the digest of every step's archive,
// which a replay must reproduce.
func runSim(t *testing.T, seed uint64, cfg simConfig) string {
	if _, err := sqlite.Load(); errors.Is(err, sqlite.ErrNoLibrary) {
		t.Skipf("%v; build it with packaging/macos/sqlcipher/build.sh", err)
	}
	// The engine's log is noise here: a failure prints the run's steps.
	defer slog.SetDefault(slog.Default())
	slog.SetDefault(slog.New(slog.DiscardHandler))
	var digest string
	synctest.Test(t, func(t *testing.T) {
		s := newSim(t, seed, cfg)
		defer sqlite.InjectFaults(s.fault)()
		s.open()
		s.check("open")
		for s.step = 1; s.step <= cfg.steps; s.step++ {
			s.act()
			s.check("step")
		}
		s.closeReader()
		digest = hex.EncodeToString(s.digest)
		acked := 0
		for _, m := range s.list {
			if m.acked {
				acked++
			}
		}
		t.Logf("seed %d: %d messages acknowledged; %v", seed, acked, s.counts)
	})
	return digest
}

func newSim(t *testing.T, seed uint64, cfg simConfig) *sim {
	var k [32]byte
	binary.LittleEndian.PutUint64(k[:], seed)
	s := &sim{t: t, seed: seed, cfg: cfg,
		rng:   mrand.New(mrand.NewChaCha8(k)),
		frng:  mrand.New(mrand.NewPCG(seed, 0x5eed)),
		keys:  &memKeys{m: map[string][]byte{}},
		state: t.TempDir(), cache: t.TempDir(),
		msgs: map[[2]string]*simMsg{}, ui: map[string]abi.Chat{}, shown: map[[2]string]bool{}, counts: map[string]int{},
		online: true}
	t.Cleanup(s.closeReader)
	// Synthetic accounts: +1 555 numbers and a made-up group.
	for i := range 4 {
		s.chats = append(s.chats, &simChat{jid: types.NewJID(fmt.Sprintf("1555010%04d", i), types.DefaultUserServer)})
	}
	for i := range 2 {
		g := &simChat{jid: types.NewJID(fmt.Sprintf("1203630000000%05d", i), types.GroupServer), group: true}
		for j := range 3 {
			g.members = append(g.members, types.NewJID(fmt.Sprintf("1555020%02d%02d", i, j), types.DefaultUserServer))
		}
		s.chats = append(s.chats, g)
	}
	return s
}

// fail stops the run with the seed and the steps that led there.
func (s *sim) fail(format string, args ...any) {
	s.t.Helper()
	trail := s.trail[max(0, len(s.trail)-25):]
	s.t.Fatalf("sim seed %d, mode %s, step %d: %s\nlast steps:\n  %s\nreplay: go test ./gocore/wa -run 'TestSimSeed$' -sim.seed=%d -sim.steps=%d -sim.mode=%s -v",
		s.seed, s.cfg.mode, s.step, fmt.Sprintf(format, args...), strings.Join(trail, "\n  "), s.seed, s.cfg.steps, s.cfg.mode)
}

func (s *sim) note(format string, args ...any) {
	s.trail = append(s.trail, fmt.Sprintf("%d: ", s.step)+fmt.Sprintf(format, args...))
}

// --- the engine ----------------------------------------------------------------

// open starts an engine over the state folder, as the app's launch does,
// and brings the link up when WhatsApp is reachable.
func (s *sim) open() {
	s.h = newHarness(s.t, options{keys: s.keys, state: s.state, cache: s.cache})
	s.h.e.syncQuiet, s.h.e.olderPatience = syncQuiet, olderPatience
	s.h.f.mu.Lock()
	s.h.f.nextID = s.lastSent
	s.h.f.mu.Unlock()
	s.ui = map[string]abi.Chat{}
	s.absorb(s.h.take())
	if err := s.h.c.Start(); err != nil {
		s.fail("start: %v", err)
	}
	s.settle()
	if s.online {
		s.h.f.setConnected(true)
		s.h.inject(&events.Connected{})
		s.settle()
	}
	s.openReader()
}

// settle waits until every goroutine of the bubble is blocked and the
// engine has nothing queued, then takes what it emitted.
func (s *sim) settle() {
	for i := 0; ; i++ {
		synctest.Wait()
		if s.h.e.idle() {
			break
		}
		if i > 10000 {
			s.fail("the engine never went idle")
		}
		time.Sleep(time.Millisecond)
	}
	s.h.drain()
	s.absorb(s.h.events)
	s.h.events = nil
}

// absorb keeps what the UI would know of the chats and of our messages.
func (s *sim) absorb(evs []abi.Event) {
	for _, ev := range evs {
		switch ev.Tag {
		case abi.EventTagChats:
			if ev.Chats.Batch == 0 {
				s.ui = map[string]abi.Chat{}
			}
			for _, c := range ev.Chats.Chats {
				s.ui[string(c.ID)] = c
			}
		case abi.EventTagChatUpdated:
			s.ui[string(ev.ChatUpdated.Chat.ID)] = ev.ChatUpdated.Chat
		case abi.EventTagChatRemoved:
			delete(s.ui, string(ev.ChatRemoved.Chat))
		case abi.EventTagMessages:
			for _, m := range ev.Messages.Messages {
				if m.FromMe {
					s.shown[[2]string{string(ev.Messages.Chat), string(m.ID)}] = true
				}
			}
		}
	}
}

// deliver hands an event to the engine, which acknowledges it, or holds it
// while the link is down. It reports whether it was delivered.
func (s *sim) deliver(evt any) bool {
	if !s.online {
		s.held = append(s.held, evt)
		return false
	}
	s.h.inject(evt)
	s.acked(evt)
	return true
}

// acked applies an acknowledged event to the oracle.
func (s *sim) acked(evt any) {
	switch v := evt.(type) {
	case *events.Message:
		chat := s.chatOf(v.Info.Chat)
		p := v.Message
		pm := p.GetProtocolMessage()
		switch {
		case pm != nil && pm.GetType() == waE2E.ProtocolMessage_MESSAGE_EDIT:
			if m := s.msgs[[2]string{chat.id(), p.GetProtocolMessage().GetKey().GetID()}]; m != nil && m.acked && !m.gone && !m.revoked {
				m.text = textOf(p.GetProtocolMessage().GetEditedMessage())
			}
		case pm != nil && pm.GetType() == waE2E.ProtocolMessage_REVOKE:
			if m := s.msgs[[2]string{chat.id(), p.GetProtocolMessage().GetKey().GetID()}]; m != nil && m.acked && !m.gone {
				m.revoked = true
				m.reactions = nil
			}
		case p.GetReactionMessage() != nil:
			r := p.GetReactionMessage()
			if m := s.msgs[[2]string{chat.id(), r.GetKey().GetID()}]; m != nil && !m.gone && !m.revoked {
				// It may wait for its message (early.go): the oracle keeps it
				// either way and checks it once the message is here.
				if m.reactions == nil {
					m.reactions = map[string]string{}
				}
				m.reactions[v.Info.Sender.String()] = r.GetText()
			}
		default:
			m := s.msgs[[2]string{chat.id(), v.Info.ID}]
			if m == nil || m.acked {
				return // a repeat changes nothing
			}
			m.acked = true
			if m.fromMe {
				chat.unread = 0
			} else {
				chat.unread++
			}
		}
	case *events.Receipt:
		chat := s.chatOf(v.Chat)
		level := 1
		if v.Type == types.ReceiptTypeRead {
			level = 2
		}
		for _, id := range v.MessageIDs {
			if m := s.msgs[[2]string{chat.id(), id}]; m != nil {
				m.receipt = max(m.receipt, level)
			}
		}
	case *events.MarkChatAsRead:
		s.chatOf(v.JID).unread = 0
	case *events.HistorySync:
		for _, conv := range v.Data.GetConversations() {
			for _, hm := range conv.GetMessages() {
				if m := s.msgs[[2]string{conv.GetID(), hm.GetMessage().GetKey().GetID()}]; m != nil {
					m.acked = true
				}
			}
		}
	case *events.GroupInfo:
		if v.Name != nil {
			s.chatOf(v.JID).name = v.Name.Name
		}
	}
}

func (s *sim) chatOf(j types.JID) *simChat {
	for _, c := range s.chats {
		if c.jid == j.ToNonAD() {
			return c
		}
	}
	s.fail("no chat %s", j)
	return nil
}

func textOf(p *waE2E.Message) string {
	if t := p.GetConversation(); t != "" {
		return t
	}
	if t := p.GetExtendedTextMessage().GetText(); t != "" {
		return t
	}
	return p.GetImageMessage().GetCaption()
}

// --- faults --------------------------------------------------------------------

// fault is the store's fault hook: while a step is armed, about one write
// in five to the archive fails.
func (s *sim) fault(path string, op sqlite.FaultOp) sqlite.Fault {
	if !s.armed.Load() || filepath.Base(path) != store.ArchiveFile {
		return sqlite.FaultNone
	}
	s.fmu.Lock()
	defer s.fmu.Unlock()
	if s.frng.IntN(5) != 0 {
		return sqlite.FaultNone
	}
	s.faulted.Add(1)
	switch n := s.frng.IntN(10); {
	case n < 5:
		return sqlite.FaultIOErr
	case n < 8 || !s.cfg.lostAcks:
		return sqlite.FaultFull
	default:
		return sqlite.FaultLostAck
	}
}

// arm lets the writes of what runs until the returned function is called
// fail; it reports whether any did.
func (s *sim) arm(on bool) (disarm func() bool) {
	if !on {
		return func() bool { return false }
	}
	s.faulted.Store(0)
	s.armed.Store(true)
	return func() bool {
		s.armed.Store(false)
		n := s.faulted.Load()
		s.counts["writes failed"] += int(n)
		return n > 0
	}
}

// --- the generator ----------------------------------------------------------

type simAction struct {
	name   string
	weight int
	run    func(s *sim)
	when   func(c simConfig) bool
}

var simActions = []simAction{
	{"incoming", 30, (*sim).incoming, nil},
	{"incoming-image", 3, (*sim).incomingImage, nil},
	{"from-phone", 5, (*sim).fromPhone, nil},
	{"send", 10, (*sim).send, nil},
	{"edit", 5, (*sim).edit, nil},
	{"edit-ours", 2, (*sim).editOurs, nil},
	{"revoke", 3, (*sim).revoke, nil},
	{"revoke-ours", 1, (*sim).revokeOurs, nil},
	{"react", 5, (*sim).react, nil},
	{"react-ours", 2, (*sim).reactOurs, nil},
	{"receipt", 6, (*sim).receipt, nil},
	{"mark-read", 6, (*sim).markRead, nil},
	{"phone-read", 3, (*sim).phoneRead, nil},
	{"history", 3, (*sim).history, nil},
	{"group-rename", 2, (*sim).groupRename, nil},
	{"delete-local", 2, (*sim).deleteLocal, nil},
	{"archive", 2, (*sim).archive, nil},
	{"time", 5, (*sim).passTime, nil},
	{"repeat", 4, (*sim).repeat, func(c simConfig) bool { return c.network }},
	{"link", 3, (*sim).toggleLink, func(c simConfig) bool { return c.network }},
	{"early-reaction", 2, (*sim).earlyReaction, func(c simConfig) bool { return c.network }},
	{"lost", 2, (*sim).lost, func(c simConfig) bool { return c.network }},
	{"crash", 2, (*sim).crash, func(c simConfig) bool { return c.crash }},
}

// act runs one step: some simulated time passes, then one action.
func (s *sim) act() {
	time.Sleep(time.Duration(s.rng.IntN(30_000)) * time.Millisecond)
	total := 0
	for _, a := range simActions {
		if a.when == nil || a.when(s.cfg) {
			total += a.weight
		}
	}
	n := s.rng.IntN(total)
	for _, a := range simActions {
		if a.when != nil && !a.when(s.cfg) {
			continue
		}
		if n < a.weight {
			s.counts[a.name]++
			a.run(s)
			return
		}
		n -= a.weight
	}
}

var simWords = strings.Fields("alpha bravo charlie delta echo foxtrot golf hotel india juliet kilo lima mike november oscar papa quebec romeo sierra tango")

func (s *sim) words() string {
	n := 1 + s.rng.IntN(5)
	w := make([]string, n)
	for i := range w {
		w[i] = simWords[s.rng.IntN(len(simWords))]
	}
	return strings.Join(w, " ")
}

func (s *sim) newID() string {
	s.nextID++
	return fmt.Sprintf("SIM%06d", s.nextID)
}

func (s *sim) anyChat() *simChat { return s.chats[s.rng.IntN(len(s.chats))] }

// sender picks who writes in c: the peer, or a member of the group.
func (s *sim) sender(c *simChat) types.JID {
	if c.group {
		return c.members[s.rng.IntN(len(c.members))]
	}
	return c.jid
}

// pick returns a random message the predicate takes, nil when none.
func (s *sim) pick(ok func(m *simMsg) bool) *simMsg {
	var c []*simMsg
	for _, m := range s.list {
		if ok(m) {
			c = append(c, m)
		}
	}
	if len(c) == 0 {
		return nil
	}
	return c[s.rng.IntN(len(c))]
}

func live(m *simMsg) bool { return m.acked && !m.gone && !m.revoked }

// newMessage makes a message's whatsmeow event and its oracle entry.
func (s *sim) newMessage(c *simChat, sender types.JID, fromMe bool, at time.Time, p *waE2E.Message) (*simMsg, *events.Message) {
	m := &simMsg{chat: c, id: s.newID(), sender: sender, fromMe: fromMe, at: at.Truncate(time.Second), text: textOf(p)}
	s.msgs[[2]string{c.id(), m.id}] = m
	s.list = append(s.list, m)
	return m, s.event(m, p)
}

func (s *sim) event(m *simMsg, p *waE2E.Message) *events.Message {
	return &events.Message{Info: types.MessageInfo{
		MessageSource: types.MessageSource{Chat: m.chat.jid, Sender: m.sender, IsFromMe: m.fromMe, IsGroup: m.chat.group},
		ID:            m.id,
		PushName:      "Peer " + m.sender.User[len(m.sender.User)-2:],
		Timestamp:     m.at,
	}, Message: p}
}

// ingest delivers an event from WhatsApp, its writes failing when the
// run injects faults there.
func (s *sim) ingest(evt any) {
	disarm := s.arm(s.cfg.ingestFaults)
	s.deliver(evt)
	s.settle()
	if disarm() {
		s.note("  (writes failed)")
	}
}

func (s *sim) incoming() {
	c := s.anyChat()
	sender := s.sender(c)
	_, evt := s.newMessage(c, sender, false, time.Now(), text(s.words()))
	s.note("incoming %s in %s", evt.Info.ID, c.id())
	s.ingest(evt)
}

func (s *sim) incomingImage() {
	c := s.anyChat()
	p := &waE2E.Message{ImageMessage: &waE2E.ImageMessage{Caption: proto.String(s.words()), Mimetype: proto.String("image/jpeg"),
		URL: proto.String("https://mmg.example/sim"), DirectPath: proto.String("/v/sim"), MediaKey: []byte("simulated-media-key-32-bytes...."),
		FileSHA256: make([]byte, 32), FileEncSHA256: make([]byte, 32), FileLength: proto.Uint64(2048), Width: proto.Uint32(64), Height: proto.Uint32(48)}}
	_, evt := s.newMessage(c, s.sender(c), false, time.Now(), p)
	s.note("incoming image %s in %s", evt.Info.ID, c.id())
	s.ingest(evt)
}

// fromPhone is a message the account's phone sent.
func (s *sim) fromPhone() {
	c := s.anyChat()
	_, evt := s.newMessage(c, me, true, time.Now(), text(s.words()))
	s.note("from the phone %s in %s", evt.Info.ID, c.id())
	s.ingest(evt)
}

// command runs a user command, its writes failing when the run injects
// faults there; it reports whether any did.
func (s *sim) command(cmd abi.Command) bool {
	disarm := s.arm(s.cfg.commandFaults)
	s.h.send(cmd)
	s.settle()
	failed := disarm()
	if failed {
		s.note("  (writes failed)")
	}
	return failed
}

func (s *sim) send() {
	c := s.anyChat()
	if _, ok := s.chatRow(c.id()); !ok {
		// The UI opens a new chat before writing in it.
		s.h.send(abi.Command{Tag: abi.CommandTagEnsureChat, EnsureChat: abi.CommandEnsureChat{Chat: chatID(c.jid)}})
		s.settle()
	}
	body := s.words()
	before := s.nextSent()
	failed := s.command(abi.Command{Tag: abi.CommandTagSendText, SendText: abi.CommandSendText{Chat: chatID(c.jid), Text: []byte(body)}})
	// The fake numbers what we send; the UI showing it acknowledges it.
	for id := range s.sentSince(before) {
		k := [2]string{c.id(), id}
		if s.msgs[k] != nil {
			continue
		}
		m := &simMsg{chat: c, id: id, sender: me, fromMe: true, text: body, at: time.Now()}
		s.msgs[k] = m
		s.list = append(s.list, m)
		if s.shown[k] {
			m.acked = true
		} else if failed {
			s.adoptPresence(m)
		}
		s.note("send %s in %s (shown %v)", id, c.id(), m.acked)
	}
	if s.nextSent() == before {
		s.note("send in %s refused", c.id())
	}
	s.lastSent = s.nextSent()
}

// nextSent and sentSince read the fake's message ids: GenerateMessageID
// counts them.
func (s *sim) nextSent() int {
	s.h.f.mu.Lock()
	defer s.h.f.mu.Unlock()
	return s.h.f.nextID
}

func (s *sim) sentSince(n int) map[string]bool {
	ids := map[string]bool{}
	for i := n + 1; i <= s.nextSent(); i++ {
		ids[fmt.Sprintf("3EB0%04d", i)] = true
	}
	return ids
}

// adoptPresence takes the archive's word for a message a failed command
// may or may not have kept.
func (s *sim) adoptPresence(m *simMsg) {
	if _, ok := s.row(m.chat.id(), m.id); ok {
		m.acked, m.gone = true, false
	} else if m.acked {
		m.gone = true
	}
}

func (s *sim) edit() {
	m := s.pick(func(m *simMsg) bool { return live(m) && !m.fromMe })
	if m == nil {
		return
	}
	body := s.words()
	p := &waE2E.Message{ProtocolMessage: &waE2E.ProtocolMessage{Type: waE2E.ProtocolMessage_MESSAGE_EDIT.Enum(),
		Key: &waCommon.MessageKey{ID: proto.String(m.id), RemoteJID: proto.String(m.chat.id())}, EditedMessage: text(body)}}
	evt := s.event(&simMsg{chat: m.chat, id: s.newID(), sender: m.sender, at: time.Now().Truncate(time.Second)}, p)
	s.note("edit %s", m.id)
	s.ingest(evt)
}

func (s *sim) editOurs() {
	m := s.pick(func(m *simMsg) bool { return live(m) && m.fromMe && m.sender == me })
	if m == nil || !s.online {
		return
	}
	body := s.words()
	failed := s.command(abi.Command{Tag: abi.CommandTagEditText, EditText: abi.CommandEditText{Chat: chatID(m.chat.jid), ID: []byte(m.id), Text: []byte(body)}})
	s.note("edit ours %s", m.id)
	if r, ok := s.row(m.chat.id(), m.id); failed && ok {
		m.text = r.Text
	} else if !failed {
		m.text = body
	}
}

func (s *sim) revoke() {
	m := s.pick(func(m *simMsg) bool { return live(m) && !m.fromMe })
	if m == nil {
		return
	}
	p := &waE2E.Message{ProtocolMessage: &waE2E.ProtocolMessage{Type: waE2E.ProtocolMessage_REVOKE.Enum(),
		Key: &waCommon.MessageKey{ID: proto.String(m.id), RemoteJID: proto.String(m.chat.id())}}}
	evt := s.event(&simMsg{chat: m.chat, id: s.newID(), sender: m.sender, at: time.Now().Truncate(time.Second)}, p)
	s.note("revoke %s", m.id)
	s.ingest(evt)
}

func (s *sim) revokeOurs() {
	m := s.pick(func(m *simMsg) bool { return live(m) && m.fromMe })
	if m == nil || !s.online {
		return
	}
	failed := s.command(abi.Command{Tag: abi.CommandTagRevoke, Revoke: abi.CommandRevoke{Chat: chatID(m.chat.jid), ID: []byte(m.id)}})
	s.note("revoke ours %s", m.id)
	if r, ok := s.row(m.chat.id(), m.id); !failed || ok && r.Deleted {
		m.revoked, m.reactions = true, nil
	}
}

func (s *sim) react() {
	m := s.pick(live)
	if m == nil {
		return
	}
	from := s.sender(m.chat)
	emoji := []string{"👍", "❤️", "😂", ""}[s.rng.IntN(4)]
	s.note("react %q to %s", emoji, m.id)
	s.ingest(reaction(m.chat.jid, from, m.id, emoji, time.Now().UnixMilli()))
}

func (s *sim) reactOurs() {
	m := s.pick(live)
	if m == nil || !s.online {
		return
	}
	emoji := []string{"🙏", "🔥", ""}[s.rng.IntN(3)]
	failed := s.command(abi.Command{Tag: abi.CommandTagReact, React: abi.CommandReact{Chat: chatID(m.chat.jid), Message: []byte(m.id), Emoji: []byte(emoji)}})
	s.note("react ours %q to %s", emoji, m.id)
	mine := me.String()
	if m.reactions == nil {
		m.reactions = map[string]string{}
	}
	if failed {
		if r, ok := s.row(m.chat.id(), m.id); ok {
			delete(m.reactions, mine)
			for _, x := range r.Reactions {
				if x.Sender == mine {
					m.reactions[mine] = x.Emoji
				}
			}
		}
		return
	}
	m.reactions[mine] = emoji
}

// receipt is a peer's delivery or read receipt of messages of ours, in a
// direct chat.
func (s *sim) receipt() {
	m := s.pick(func(m *simMsg) bool { return live(m) && m.fromMe && !m.chat.group })
	if m == nil {
		return
	}
	kind := types.ReceiptTypeDelivered
	if s.rng.IntN(2) == 0 {
		kind = types.ReceiptTypeRead
	}
	s.note("receipt %s of %s", kind, m.id)
	s.ingest(theirReceipt(m.chat.jid, m.chat.jid, kind, time.Now(), m.id))
}

func (s *sim) markRead() {
	c := s.anyChat()
	before := c.unread
	failed := s.command(abi.Command{Tag: abi.CommandTagMarkRead, MarkRead: abi.CommandMarkRead{Chat: chatID(c.jid), Receipts: true}})
	s.note("mark read %s", c.id())
	if !failed {
		if _, ok := s.chatRow(c.id()); ok {
			c.unread = 0
		}
		return
	}
	if r, ok := s.chatRow(c.id()); ok && (r.Unread == 0 || r.Unread == before) {
		c.unread = r.Unread
	}
}

// phoneRead is the phone reading a chat (app state).
func (s *sim) phoneRead() {
	c := s.anyChat()
	if _, ok := s.chatRow(c.id()); !ok {
		return
	}
	s.note("the phone read %s", c.id())
	s.ingest(&events.MarkChatAsRead{JID: c.jid, Timestamp: time.Now(), Action: &waSyncAction.MarkChatAsReadAction{Read: proto.Bool(true)}})
}

// history is a chunk of history sync for a direct chat: older messages,
// and some the archive has already.
func (s *sim) history() {
	c := s.chats[s.rng.IntN(4)]
	earliest := time.Now()
	var known []*simMsg
	for _, m := range s.list {
		if m.chat == c && m.acked {
			if m.at.Before(earliest) {
				earliest = m.at
			}
			if !m.gone {
				known = append(known, m)
			}
		}
	}
	var msgs []*waHistorySync.HistorySyncMsg
	for range 1 + s.rng.IntN(4) {
		earliest = earliest.Add(-time.Duration(1+s.rng.IntN(3600)) * time.Second)
		fromMe := s.rng.IntN(3) == 0
		sender := c.jid
		if fromMe {
			sender = me
		}
		m, _ := s.newMessage(c, sender, fromMe, earliest, text(s.words()))
		msgs = append(msgs, s.webMessage(m, text(m.text)))
	}
	for range s.rng.IntN(3) {
		if len(known) > 0 {
			m := known[s.rng.IntN(len(known))]
			msgs = append(msgs, s.webMessage(m, text(m.text)))
		}
	}
	s.note("history for %s: %d messages", c.id(), len(msgs))
	s.ingest(&events.HistorySync{Data: &waHistorySync.HistorySync{SyncType: waHistorySync.HistorySync_RECENT.Enum(),
		Conversations: []*waHistorySync.Conversation{{ID: proto.String(c.id()), Messages: msgs}}}})
}

func (s *sim) webMessage(m *simMsg, p *waE2E.Message) *waHistorySync.HistorySyncMsg {
	return &waHistorySync.HistorySyncMsg{Message: &waWeb.WebMessageInfo{
		Key:              &waCommon.MessageKey{ID: proto.String(m.id), FromMe: proto.Bool(m.fromMe), RemoteJID: proto.String(m.chat.id())},
		MessageTimestamp: proto.Uint64(uint64(m.at.Unix())),
		Message:          p,
		Status:           waWeb.WebMessageInfo_READ.Enum(),
	}, MsgOrderID: proto.Uint64(uint64(m.at.Unix()))}
}

func (s *sim) groupRename() {
	g := s.chats[4+s.rng.IntN(2)]
	if _, ok := s.chatRow(g.id()); !ok {
		return
	}
	by := g.members[0]
	name := "Group " + s.words()
	s.note("rename %s", g.id())
	s.ingest(&events.GroupInfo{JID: g.jid, Sender: &by, Timestamp: time.Now(), Name: &types.GroupName{Name: name, NameSetAt: time.Now(), NameSetBy: by}})
}

func (s *sim) deleteLocal() {
	m := s.pick(func(m *simMsg) bool { return m.acked && !m.gone })
	if m == nil {
		return
	}
	failed := s.command(abi.Command{Tag: abi.CommandTagDeleteLocal, DeleteLocal: abi.CommandDeleteLocal{Chat: chatID(m.chat.jid), ID: []byte(m.id)}})
	s.note("delete %s here (online %v)", m.id, s.online)
	switch {
	case !s.online:
		// The phone deletes first: refused without the link.
	case failed:
		s.adoptPresence(m)
	default:
		m.gone = true
	}
}

func (s *sim) archive() {
	c := s.anyChat()
	if _, ok := s.chatRow(c.id()); !ok {
		return
	}
	on := s.rng.IntN(2) == 0
	s.note("archive %s: %v", c.id(), on)
	s.command(abi.Command{Tag: abi.CommandTagSetArchived, SetArchived: abi.CommandSetArchived{Chat: chatID(c.jid), Archived: on}})
}

// passTime lets up to an hour go by: the engine's timers fire.
func (s *sim) passTime() {
	d := time.Duration(s.rng.IntN(3600)) * time.Second
	s.note("time passes: %v", d)
	time.Sleep(d)
	s.settle()
}

// repeat is WhatsApp delivering a message again.
func (s *sim) repeat() {
	m := s.pick(func(m *simMsg) bool { return m.acked && !m.fromMe })
	if m == nil {
		return
	}
	s.note("repeat %s", m.id)
	s.ingest(s.event(m, text(m.text)))
}

// toggleLink takes the link down, or brings it back with what WhatsApp
// held in the meantime: each chat's in order, the chats interleaved.
func (s *sim) toggleLink() {
	if s.online {
		s.note("link down")
		s.online = false
		s.h.f.setConnected(false)
		s.h.inject(&events.Disconnected{})
		s.settle()
		return
	}
	s.note("link up, %d held", len(s.held))
	s.online = true
	s.h.f.setConnected(true)
	s.h.inject(&events.Connected{})
	s.settle()
	held := s.held
	s.held = nil
	for len(held) > 0 {
		// The next event of a random chat: the first held one of its chat.
		pick := held[s.rng.IntN(len(held))]
		first := slices.IndexFunc(held, func(e any) bool { return heldChat(e) == heldChat(pick) })
		evt := held[first]
		held = slices.Delete(held, first, first+1)
		s.ingest(evt)
	}
}

func heldChat(evt any) types.JID {
	switch v := evt.(type) {
	case *events.Message:
		return v.Info.Chat
	case *events.Receipt:
		return v.Chat
	case *events.MarkChatAsRead:
		return v.JID
	case *events.GroupInfo:
		return v.JID
	}
	return types.EmptyJID
}

// earlyReaction delivers a reaction before the message it reacts to.
func (s *sim) earlyReaction() {
	c := s.anyChat()
	m, evt := s.newMessage(c, s.sender(c), false, time.Now(), text(s.words()))
	r := reaction(c.jid, s.sender(c), m.id, "👀", time.Now().UnixMilli())
	s.note("reaction before %s", m.id)
	s.ingest(r)
	time.Sleep(time.Duration(s.rng.IntN(5)) * time.Second)
	s.ingest(evt)
}

// lost is a message WhatsApp never delivers; what refers to it may come.
func (s *sim) lost() {
	c := s.anyChat()
	m, _ := s.newMessage(c, s.sender(c), false, time.Now(), text(s.words()))
	s.note("lost %s", m.id)
	if s.rng.IntN(2) == 0 {
		s.ingest(reaction(c.jid, m.sender, m.id, "😮", time.Now().UnixMilli()))
	}
}

// --- crashes -----------------------------------------------------------------

// crash kills the process and starts it again over what was on disk. In
// the queued mode it may come while the engine still has acknowledged
// events queued, or be a quit (Conn.Close) rather than a kill; a torn
// write may leave half a frame at the WAL's end.
func (s *sim) crash() {
	var gate chan struct{}
	queued := 0
	if s.cfg.busyCrash && s.online && s.rng.IntN(2) == 0 {
		gate = make(chan struct{})
		s.h.e.post(func() { <-gate })
		synctest.Wait()
		for range 1 + s.rng.IntN(3) {
			c := s.anyChat()
			_, evt := s.newMessage(c, s.sender(c), false, time.Now(), text(s.words()))
			s.deliver(evt)
			queued++
		}
	}
	quit := gate != nil && s.rng.IntN(2) == 0
	if quit {
		s.note("quit (%d events queued)", queued)
	} else {
		s.note("crash (%d events queued)", queued)
	}
	s.closeReader()
	snap := s.t.TempDir()
	if !quit {
		copyTree(s.t, s.state, snap)
	}
	// Close cancels the engine before the gate opens, so what it does with
	// its queue does not depend on the scheduler.
	closed := make(chan error, 1)
	go func() { closed <- s.h.c.Close(10 * time.Second) }()
	synctest.Wait()
	if gate != nil {
		close(gate)
	}
	if err := <-closed; err != nil {
		s.fail("close: %v", err)
	}
	s.h.events = nil
	if !quit {
		if err := os.RemoveAll(s.state); err != nil {
			s.fail("%v", err)
		}
		copyTree(s.t, snap, s.state)
		if s.rng.IntN(3) == 0 {
			s.tear()
		}
	}
	s.failClosed()
	// Nothing is held across a crash: what WhatsApp held stays held.
	s.open()
	s.check("reopen")
}

// copyTree copies a state folder as it is on disk; the WAL index (-shm)
// is memory, rebuilt on open.
func copyTree(t *testing.T, from, to string) {
	t.Helper()
	err := filepath.WalkDir(from, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(from, p)
		target := filepath.Join(to, rel)
		if d.IsDir() {
			return os.MkdirAll(target, 0o700)
		}
		if strings.HasSuffix(p, "-shm") {
			return nil
		}
		data, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		return os.WriteFile(target, data, 0o600)
	})
	if err != nil {
		t.Fatal(err)
	}
}

// walFrame is the size of one WAL frame of the archive (4096-byte pages).
const walFrame = 24 + 4096

// tear leaves part of a frame at the archive WAL's end, as a write cut
// short by the crash leaves it: a copy of the last frame, truncated.
func (s *sim) tear() {
	wal := filepath.Join(account(s.state), store.ArchiveFile+"-wal")
	data, err := os.ReadFile(wal)
	if err != nil || len(data) < 32+walFrame || (len(data)-32)%walFrame != 0 {
		return
	}
	last := data[len(data)-walFrame:]
	n := 1 + s.rng.IntN(walFrame-1)
	s.note("torn write: %d bytes of a frame", n)
	if err := os.WriteFile(wal, append(data, last[:n]...), 0o600); err != nil {
		s.fail("%v", err)
	}
}

// failClosed checks what opening refuses, on copies: a wrong key, an
// archive a newer version wrote, and a damaged one, none of which may be
// changed by the attempt or read as if it were whole.
func (s *sim) failClosed() {
	dir := s.t.TempDir()
	copyTree(s.t, s.state, dir)
	path := filepath.Join(account(dir), store.ArchiveFile)
	if _, err := os.Stat(path); err != nil {
		return
	}
	key := s.archiveKey()
	switch s.rng.IntN(3) {
	case 0:
		wrong := make([]byte, 32)
		rand.Read(wrong)
		before := treeHash(s.t, dir)
		snapBefore := treeFiles(s.t, dir)
		if a, err := store.OpenArchive(path, wrong); err == nil || !errors.Is(err, sqlite.ErrWrongKey) {
			if a != nil {
				a.Close()
			}
			s.fail("a wrong key opened the archive: %v", err)
		}
		if treeHash(s.t, dir) != before {
			s.fail("a refused open changed the archive's files: %s", treeDiff(s.t, dir, snapBefore))
		}
	case 1:
		db, err := store.OpenDatabase(path, key)
		if err != nil {
			s.fail("open for the newer-schema check: %v", err)
		}
		_, err = db.Exec(fmt.Sprintf("PRAGMA user_version=%d", store.SchemaVersion+1))
		db.Close()
		if err != nil {
			s.fail("%v", err)
		}
		before := treeHash(s.t, dir)
		if a, err := store.OpenArchive(path, key); !errors.Is(err, store.ErrNewerArchive) {
			if a != nil {
				a.Close()
			}
			s.fail("an archive of a newer schema opened: %v", err)
		}
		if treeHash(s.t, dir) != before {
			s.fail("a refused newer archive was changed")
		}
	case 2:
		// One byte flipped in a page of the database file.
		data, err := os.ReadFile(path)
		if err != nil || len(data) < 2*4096 {
			return
		}
		off := 4096 + s.rng.IntN(len(data)-4096)
		data[off] ^= 0x40
		os.WriteFile(path, data, 0o600)
		a, err := store.OpenArchive(path, key)
		if err != nil {
			return // refused
		}
		defer a.Close()
		if a.Integrity() != nil {
			return // found out
		}
		// Whole by the check: then every message must read back.
		for _, m := range s.list {
			if !live(m) {
				continue
			}
			got, err := a.Message(m.chat.id(), m.id)
			if err != nil && !noRows(err) {
				return // the damage shows on reading
			}
			if noRows(err) || got.Text != m.text {
				s.fail("a damaged archive passed its integrity check and lost %s", m.id)
			}
		}
	}
}

// treeFiles and treeDiff say which files of a folder changed.
func treeFiles(t *testing.T, dir string) map[string][]byte {
	files := map[string][]byte{}
	filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() || strings.HasSuffix(p, "-shm") {
			return err
		}
		data, err := os.ReadFile(p)
		if err != nil {
			t.Fatal(err)
		}
		files[p] = data
		return nil
	})
	return files
}

func treeDiff(t *testing.T, dir string, before map[string][]byte) string {
	after := treeFiles(t, dir)
	var out []string
	for p, b := range before {
		a, ok := after[p]
		switch {
		case !ok:
			out = append(out, fmt.Sprintf("%s removed (%d bytes)", filepath.Base(p), len(b)))
		case !slices.Equal(a, b):
			out = append(out, fmt.Sprintf("%s changed (%d to %d bytes)", filepath.Base(p), len(b), len(a)))
		}
	}
	for p, a := range after {
		if _, ok := before[p]; !ok {
			out = append(out, fmt.Sprintf("%s created (%d bytes)", filepath.Base(p), len(a)))
		}
	}
	sort.Strings(out)
	return strings.Join(out, ", ")
}

func treeHash(t *testing.T, dir string) [32]byte {
	h := sha256.New()
	filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() || strings.HasSuffix(p, "-shm") {
			return err
		}
		data, err := os.ReadFile(p)
		if err != nil {
			t.Fatal(err)
		}
		fmt.Fprintf(h, "%s %d\n", p, len(data))
		h.Write(data)
		return nil
	})
	var sum [32]byte
	copy(sum[:], h.Sum(nil))
	return sum
}

// account is the account's folder in a state folder (gocore.Host's
// AccountDirs; the harness's account is "1").
func account(state string) string { return filepath.Join(state, "accounts", "1") }

func (s *sim) archiveKey() []byte {
	name, err := store.ArchiveKeyName(account(s.state))
	if err != nil {
		s.fail("%v", err)
	}
	key, err := s.keys.Get(name)
	if err != nil {
		s.fail("the archive key: %v", err)
	}
	return key
}

// --- the oracle ----------------------------------------------------------------

func (s *sim) archivePath() string {
	dir, err := filepath.EvalSymlinks(account(s.state))
	if err != nil {
		s.fail("%v", err)
	}
	return filepath.Join(dir, store.ArchiveFile)
}

// openReader opens a second connection to the archive, for the checks.
func (s *sim) openReader() {
	db, err := sqlite.Open(sqlite.Config{Path: s.archivePath(), Key: s.archiveKey()})
	if err != nil {
		s.fail("the checks' connection: %v", err)
	}
	db.SetMaxOpenConns(1)
	s.reader = db
}

func (s *sim) closeReader() {
	if s.reader != nil {
		s.reader.Close()
		s.reader = nil
	}
}

func (s *sim) row(chat, id string) (store.Message, bool) {
	var payload []byte
	err := s.reader.QueryRow("SELECT payload FROM messages WHERE chat=? AND id=?", chat, id).Scan(&payload)
	if errors.Is(err, sql.ErrNoRows) {
		return store.Message{}, false
	}
	var m store.Message
	if err == nil {
		err = json.Unmarshal(payload, &m)
	}
	if err != nil {
		s.fail("reading %s: %v", id, err)
	}
	return m, true
}

func (s *sim) chatRow(id string) (store.Chat, bool) {
	var payload []byte
	err := s.reader.QueryRow("SELECT payload FROM chats WHERE id=?", id).Scan(&payload)
	if errors.Is(err, sql.ErrNoRows) {
		return store.Chat{}, false
	}
	var c store.Chat
	if err == nil {
		err = json.Unmarshal(payload, &c)
	}
	if err != nil {
		s.fail("reading chat %s: %v", id, err)
	}
	return c, true
}

var statusRank = map[string]int{"failed": -1, "pending": 0, "sent": 1, "delivered": 2, "read": 3, "played": 4}

// check holds the archive to the oracle and to what the UI was told.
func (s *sim) check(where string) {
	s.t.Helper()
	var ok string
	if err := s.reader.QueryRow("PRAGMA integrity_check").Scan(&ok); err != nil || ok != "ok" {
		s.fail("%s: the archive's integrity check: %q %v", where, ok, err)
	}

	// Every row, and where each id is filed.
	type rowKey struct{ chat, id string }
	rows := map[rowKey]store.Message{}
	filed := map[string][]string{}
	maxTS := map[string]int64{}
	r, err := s.reader.Query("SELECT chat,id,ts,payload FROM messages")
	if err != nil {
		s.fail("%v", err)
	}
	for r.Next() {
		var chat, id string
		var ts int64
		var payload []byte
		if err := r.Scan(&chat, &id, &ts, &payload); err != nil {
			s.fail("%v", err)
		}
		var m store.Message
		if err := json.Unmarshal(payload, &m); err != nil {
			s.fail("%s/%s: %v", chat, id, err)
		}
		rows[rowKey{chat, id}] = m
		filed[id] = append(filed[id], chat)
		maxTS[chat] = max(maxTS[chat], ts)
	}
	r.Close()
	for id, chats := range filed {
		if len(chats) > 1 {
			s.fail("%s: message %s is filed in %d chats: %v", where, id, len(chats), chats)
		}
	}

	// No acknowledged message lost, and each as the oracle has it.
	for _, m := range s.list {
		if !m.acked || m.gone {
			continue
		}
		got, ok := rows[rowKey{m.chat.id(), m.id}]
		if !ok {
			s.fail("%s: acknowledged message %s of %s is lost", where, m.id, m.chat.id())
		}
		switch {
		case m.revoked && !got.Deleted:
			s.fail("%s: %s was revoked but shows %q", where, m.id, got.Text)
		case !m.revoked && got.Deleted:
			s.fail("%s: %s shows as revoked", where, m.id)
		case !m.revoked && got.Text != m.text:
			s.fail("%s: %s reads %q, want %q", where, m.id, got.Text, m.text)
		}
		if !m.revoked {
			want := map[string]string{}
			for k, v := range m.reactions {
				if v != "" {
					want[k] = v
				}
			}
			have := map[string]string{}
			for _, x := range got.Reactions {
				have[x.Sender] = x.Emoji
			}
			if fmt.Sprint(have) != fmt.Sprint(want) {
				s.fail("%s: %s has reactions %v, want %v", where, m.id, have, want)
			}
		}
		if m.fromMe {
			rank := statusRank[got.Status]
			if rank < m.rank && got.Status != "failed" {
				s.fail("%s: %s went back from rank %d to %q", where, m.id, m.rank, got.Status)
			}
			if m.receipt > 0 && rank < m.receipt+1 && got.Status != "failed" {
				s.fail("%s: %s is %q after a receipt of level %d", where, m.id, got.Status, m.receipt)
			}
			m.rank = max(m.rank, rank)
		}
	}
	for _, m := range s.list {
		if m.gone && m.acked {
			if _, ok := rows[rowKey{m.chat.id(), m.id}]; ok {
				s.fail("%s: %s was deleted here but is back", where, m.id)
			}
		}
	}

	// Each chat's order is stable, the list has every chat with messages,
	// and the counts are the oracle's and the UI's.
	for _, c := range s.chats {
		var order []string
		q, err := s.reader.Query("SELECT id FROM messages WHERE chat=? ORDER BY ts,ord,id", c.id())
		if err != nil {
			s.fail("%v", err)
		}
		for q.Next() {
			var id string
			q.Scan(&id)
			order = append(order, id)
		}
		q.Close()
		var kept []string
		for _, id := range order {
			if slices.Contains(c.order, id) {
				kept = append(kept, id)
			}
		}
		var before []string
		for _, id := range c.order {
			if slices.Contains(order, id) {
				before = append(before, id)
			}
		}
		if !slices.Equal(kept, before) {
			s.fail("%s: the order of %s changed:\n was %v\n now %v", where, c.id(), before, kept)
		}
		c.order = order

		row, ok := s.chatRow(c.id())
		if len(order) > 0 && !ok {
			s.fail("%s: %s has %d messages and no place in the chat list", where, c.id(), len(order))
		}
		if !ok {
			continue
		}
		if ts := maxTS[c.id()]; row.LastActivity < ts {
			s.fail("%s: %s's last activity %d is older than its newest message %d", where, c.id(), row.LastActivity, ts)
		}
		if row.Unread != c.unread {
			s.fail("%s: %s has %d unread, want %d", where, c.id(), row.Unread, c.unread)
		}
		if c.group && c.name != "" && row.Name != c.name {
			s.fail("%s: %s is named %q, want %q", where, c.id(), row.Name, c.name)
		}
		ui, shown := s.ui[c.id()]
		if !shown {
			s.fail("%s: %s is in the archive and not in the UI's list", where, c.id())
		}
		if int(ui.Unread) != row.Unread || ui.Archived != row.Archived {
			s.fail("%s: the UI shows %s with %d unread (archived %v), the archive %d (%v)", where, c.id(), ui.Unread, ui.Archived, row.Unread, row.Archived)
		}
	}

	// The digest a replay must reproduce.
	h := sha256.New()
	h.Write(s.digest)
	keys := make([]rowKey, 0, len(rows))
	for k := range rows {
		keys = append(keys, k)
	}
	sort.Slice(keys, func(i, j int) bool { return keys[i].chat+keys[i].id < keys[j].chat+keys[j].id })
	for _, k := range keys {
		m := rows[k]
		fmt.Fprintf(h, "%s %s %q %v %s %d|", k.chat, k.id, m.Text, m.Deleted, m.Status, m.Timestamp)
	}
	for _, c := range s.chats {
		row, _ := s.chatRow(c.id())
		fmt.Fprintf(h, "%s %d %d %v|", c.id(), row.Unread, row.LastActivity, row.Archived)
	}
	s.digest = h.Sum(nil)
}

// --- the tests -----------------------------------------------------------------

// simSeeds are the seeds every `go test` runs.
var simSeeds = []uint64{1, 2, 3, 20261030}

func TestSim(t *testing.T) {
	steps := 150
	if testing.Short() {
		steps = 40
	}
	if *simSteps > 0 {
		steps = *simSteps
	}
	for _, seed := range simSeeds {
		t.Run(fmt.Sprint(seed), func(t *testing.T) {
			runSim(t, seed, simConfigOf("default", steps))
		})
	}
	// A seed replays exactly.
	t.Run("replay", func(t *testing.T) {
		cfg := simConfigOf("default", min(steps, 60))
		a, b := runSim(t, simSeeds[0], cfg), runSim(t, simSeeds[0], cfg)
		if a != b {
			t.Fatalf("seed %d does not replay: %s, then %s", simSeeds[0], a, b)
		}
	})
}

// simRegressions are the seeds that found a bug, each run as it found it.
// One whose bug is open is skipped, with its issue.
var simRegressions = []struct {
	seed  uint64
	mode  string
	steps int
	found string
	issue string
}{
	{1, "default", 150, "a wrong key's connection checkpointed the WAL into the archive as it closed (sqlite proves the key read-only now)", ""},
	{3, "default", 150, "a send whose chat update failed left the chat list behind its message (store.SaveWithChat)", ""},
	{3, "lostack", 200, "a chat update whose commit held but reported a failure left the UI behind (updateChat sends the chat after a failure)", ""},
	{3065798142414315, "default", 300, "history filing a live row again moved it among the messages of its second (save keeps a live row's order)", ""},
	{2, "queued", 200, "a quit with an acknowledged message queued loses it", "https://github.com/AdamAugustinsky/zap/issues/2"},
	{1, "queued", 200, "a crash with acknowledged messages queued loses them", "https://github.com/AdamAugustinsky/zap/issues/2"},
	{4, "ingest", 200, "an acknowledged message whose archive write fails is lost", "https://github.com/AdamAugustinsky/zap/issues/2"},
}

func TestSimRegressions(t *testing.T) {
	for _, r := range simRegressions {
		t.Run(fmt.Sprintf("%s/%d", r.mode, r.seed), func(t *testing.T) {
			if r.issue != "" {
				t.Skipf("open data-loss bug, %s: %s", r.issue, r.found)
			}
			runSim(t, r.seed, simConfigOf(r.mode, r.steps))
		})
	}
}

// TestSimSeed replays one seed: -sim.seed, -sim.steps, -sim.mode.
func TestSimSeed(t *testing.T) {
	if *simSeed == 0 {
		t.Skip("give -sim.seed")
	}
	steps := *simSteps
	if steps == 0 {
		steps = 150
	}
	t.Logf("digest %s", runSim(t, *simSeed, simConfigOf(*simMode, steps)))
}

// TestSimExplore runs random seeds for -sim.for, printing each before it
// runs, until one fails.
func TestSimExplore(t *testing.T) {
	if *simFor == 0 {
		t.Skip("give -sim.for")
	}
	steps := *simSteps
	if steps == 0 {
		steps = 300
	}
	end := time.Now().Add(*simFor)
	for n := 0; time.Now().Before(end); n++ {
		var b [8]byte
		rand.Read(b[:])
		seed := binary.LittleEndian.Uint64(b[:])&(1<<53-1) | 1
		fmt.Fprintf(os.Stderr, "sim: run %d seed %d mode %s steps %d\n", n, seed, *simMode, steps)
		if !t.Run(fmt.Sprint(seed), func(t *testing.T) { runSim(t, seed, simConfigOf(*simMode, steps)) }) {
			fmt.Fprintf(os.Stderr, "sim: FAILED seed %d\n", seed)
			return
		}
	}
}
