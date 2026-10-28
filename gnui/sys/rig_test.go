package sys

import (
	"errors"
	"strconv"
	"testing"
	"time"

	"gonativeui/app"
	"gonativeui/app/dock"
	"gonativeui/app/login"
	"gonativeui/app/notify"
	"gonativeui/app/tray"
	"gonativeui/menu"
	"gonativeui/platform"

	"zapfast/abi"
	"zapfast/core/fake"
	"zapfast/state"
)

// fakeCenter is the system's notification center: what was posted, what is
// still on the desktop, and the responses a test scripts.
type fakeCenter struct {
	available bool
	posts     []notify.Note
	shown     map[string]notify.Note // on the desktop
	removed   []string
	cats      []notify.Category
	asked     int
	statuses  int
	postErr   error
	n         int
	responses []notify.Response
	results   []notify.Result
}

func newCenter() *fakeCenter { return &fakeCenter{available: true, shown: map[string]notify.Note{}} }

func (c *fakeCenter) Available() bool { return c.available }
func (c *fakeCenter) Post(n notify.Note) (notify.PostResult, error) {
	if c.postErr != nil {
		return notify.PostResult{}, c.postErr
	}
	c.n++
	id := "n" + strconv.Itoa(c.n)
	c.posts = append(c.posts, n)
	c.shown[id] = n
	return notify.PostResult{ID: id, Request: notify.Ticket(c.n)}, nil
}
func (c *fakeCenter) Remove(ids ...string) error {
	for _, id := range ids {
		delete(c.shown, id)
		c.removed = append(c.removed, id)
	}
	return nil
}
func (c *fakeCenter) RegisterCategories(cs ...notify.Category) error {
	c.cats = append(c.cats, cs...)
	return nil
}
func (c *fakeCenter) RequestPermission(notify.Permission) (notify.Ticket, error) {
	c.asked++
	return notify.Ticket(1000 + c.asked), nil
}
func (c *fakeCenter) Status() (notify.Ticket, error) { c.statuses++; return notify.Ticket(2000), nil }

// The events of the center are indexes: a test scripts the n-th.
func (c *fakeCenter) Result(e *app.Event) (notify.Result, bool) {
	if e.Kind == app.EvService && e.Source == fakeResultSource {
		return c.results[e.ID], true
	}
	return notify.Result{}, false
}
func (c *fakeCenter) Response(e *app.Event) (notify.Response, bool) {
	if e.Kind == app.EvService && e.Source == fakeResponseSource {
		return c.responses[e.ID], true
	}
	return notify.Response{}, false
}

// answer scripts a result and returns the event that carries it.
func (c *fakeCenter) result(r notify.Result) *app.Event {
	c.results = append(c.results, r)
	return &app.Event{Kind: app.EvService, Source: fakeResultSource, ID: uint64(len(c.results) - 1)}
}

// respond scripts a response and returns the event that carries it.
func (c *fakeCenter) respond(r notify.Response) *app.Event {
	c.responses = append(c.responses, r)
	return &app.Event{Kind: app.EvService, Source: fakeResponseSource, ID: uint64(len(c.responses) - 1)}
}

type fakeDock struct {
	badges  []int
	bounces []bool
	cancels []dock.Attention
}

func (d *fakeDock) SetBadgeCount(n int) error { d.badges = append(d.badges, n); return nil }
func (d *fakeDock) Bounce(critical bool) (dock.Attention, error) {
	d.bounces = append(d.bounces, critical)
	return dock.Attention(len(d.bounces)), nil
}
func (d *fakeDock) CancelBounce(id dock.Attention) { d.cancels = append(d.cancels, id) }

type fakeItem struct {
	menus   []*menu.Model
	idx     []int
	removed bool
}

func (i *fakeItem) SetMenu(m *menu.Model, idx int) error {
	i.menus, i.idx = append(i.menus, m), append(i.idx, idx)
	return nil
}
func (i *fakeItem) Remove() { i.removed = true }

type fakeLogin struct {
	state   login.State
	enabled int
	off     int
	err     error
}

func (l *fakeLogin) Status() (login.State, error) { return l.state, l.err }
func (l *fakeLogin) Enable() (login.State, error) {
	l.enabled++
	if l.err == nil {
		l.state = login.RequiresApproval
	}
	return l.state, l.err
}
func (l *fakeLogin) Disable() (login.State, error) {
	l.off++
	l.state = login.NotRegistered
	return l.state, l.err
}

// fakeTone is the ringtone's output: whether it plays, and how many cycles
// it was started for.
type fakeTone struct {
	on            bool
	starts, stops int
	endedID       uint64 // the media event that ends a cycle
	replays       int
	err           error
}

func (t *fakeTone) Start() error {
	if t.err != nil {
		return t.err
	}
	t.on = true
	t.starts++
	return nil
}
func (t *fakeTone) Stop() { t.on = false; t.stops++ }
func (t *fakeTone) Ended(e *app.Event) bool {
	if e.Kind == app.EvService && e.Source == platform.SourceMedia && t.on {
		t.replays++
		return true
	}
	return false
}

// fakePanels are the app's sheet panels: what was asked, and the results a
// test scripts for them.
type fakePanels struct {
	opens   []platform.OpenSpec
	saves   []platform.SaveSpec
	results []platform.DialogResult
	err     error
	n       platform.DialogTicket
}

func (p *fakePanels) OpenPanel(s platform.OpenSpec) (platform.DialogTicket, error) {
	if p.err != nil {
		return 0, p.err
	}
	p.opens = append(p.opens, s)
	p.n++
	return p.n, nil
}
func (p *fakePanels) SavePanel(s platform.SaveSpec) (platform.DialogTicket, error) {
	if p.err != nil {
		return 0, p.err
	}
	p.saves = append(p.saves, s)
	p.n++
	return p.n, nil
}
func (p *fakePanels) TakeDialog() (platform.DialogResult, bool) {
	if len(p.results) == 0 {
		return platform.DialogResult{}, false
	}
	r := p.results[0]
	p.results = p.results[1:]
	return r, true
}

// ended scripts the end of the panel with ticket t and returns its event.
func (p *fakePanels) ended(t platform.DialogTicket, outcome platform.DialogOutcome, paths ...string) *app.Event {
	p.results = append(p.results, platform.DialogResult{Ticket: t, Outcome: outcome, Paths: paths})
	return &app.Event{Kind: app.EvService, Source: platform.SourceDialog, ID: uint64(t)}
}

type fakeWindow struct{ shows, hides, focuses int }

func (w *fakeWindow) Show()  { w.shows++ }
func (w *fakeWindow) Hide()  { w.hides++ }
func (w *fakeWindow) Focus() { w.focuses++ }

const (
	ada   = "1@s.whatsapp.net"
	grace = "2@s.whatsapp.net"
	group = "g1@g.us"
	me    = "99@s.whatsapp.net"
)

// rig is the services over a fake core, a fake center, Dock and status item.
type rig struct {
	t      *testing.T
	y      *System
	s      *state.State
	a      *state.Account
	f      *fake.Core
	c      *fakeCenter
	d      *fakeDock
	item   *fakeItem
	lg     *fakeLogin
	win    *fakeWindow // nil: no main window
	played []string
	tone   *fakeTone
	panels *fakePanels
	urls   map[uint64]string
	seq    int
	quit   int
	reload int
	now    time.Time
}

func ev(tag abi.EventTag, set func(e *abi.Event)) *abi.Event {
	e := new(abi.Event)
	e.Tag = tag
	set(e)
	return e
}

func chats() []abi.Chat {
	return []abi.Chat{
		{ID: []byte(ada), Name: []byte("Ada Lovelace"), LastActivity: 30, Unread: 1},
		{ID: []byte(grace), Name: []byte("Grace Hopper"), LastActivity: 20},
		{ID: []byte(group), Name: []byte("Engines"), Kind: abi.ChatKindGroup, GroupSubjectKnown: true, LastActivity: 10, Unread: 1,
			Participants: [][]byte{[]byte(ada), []byte(grace), []byte(me)}},
	}
}

type opt func(*rig)

func newRig(t *testing.T, opts ...opt) *rig {
	t.Helper()
	r := &rig{t: t, urls: map[uint64]string{}, now: time.Unix(1_800_000_000, 0), c: newCenter(), d: &fakeDock{}, item: &fakeItem{}, lg: &fakeLogin{}, win: &fakeWindow{}, tone: &fakeTone{}, panels: &fakePanels{}}
	r.f = fake.New(nil)
	r.s = state.New()
	r.s.Now = func() time.Time { return r.now }
	r.a = r.s.AddAccount("1", nil, r.f)
	y := New()
	y.center, y.dock, y.login = r.c, r.d, r.lg
	y.tone, y.panel = r.tone, r.panels
	y.play = func(p string) { r.played = append(r.played, p) }
	y.makeItem = func(tray.Options) (statusItem, error) { return r.item, nil }
	y.mainFn = func() window {
		if r.win == nil {
			return nil
		}
		return r.win
	}
	y.quit = func() { r.quit++ }
	y.reload = func() { r.reload++ }
	y.urlsOf = func(e *app.Event) []string { return []string{r.urls[e.ID]} }
	r.y = y
	for _, o := range opts {
		o(r)
	}
	r.f.Emit(
		ev(abi.EventTagLink, func(e *abi.Event) { e.Link.Status.Tag = abi.LinkStatusTagConnected }),
		ev(abi.EventTagMe, func(e *abi.Event) { e.Me.ID = []byte(me) }),
		ev(abi.EventTagChats, func(e *abi.Event) { e.Chats = abi.EventChats{Batch: 1, Last: true, Chats: chats()} }),
		ev(abi.EventTagContacts, func(e *abi.Event) {
			e.Contacts.Contacts = []abi.Contact{
				{ID: []byte(ada), FullName: []byte("Ada Lovelace"), HasFullName: true},
				{ID: []byte(grace), FullName: []byte("Grace Hopper"), HasFullName: true},
			}
		}),
	)
	y.begin(r.s, false)
	y.n.perm = permGranted
	r.s.Pump(0)
	r.frame()
	return r
}

func (r *rig) frame() { r.y.Frame(r.s) }

// pump applies the core's events and runs a frame.
func (r *rig) pump(events ...*abi.Event) {
	r.t.Helper()
	if len(events) > 0 {
		if err := r.f.Emit(events...); err != nil {
			r.t.Fatal(err)
		}
	}
	r.s.Pump(0)
	r.frame()
}

// notice queues a notification the way logic does and runs a frame.
func (r *rig) notice(n state.Notice) {
	r.t.Helper()
	r.s.Notices = append(r.s.Notices, n)
	r.s.Attention = true
	r.frame()
}

// incoming delivers an incoming message the way the core does (the chat
// updated, then the message), and queues the notification logic would.
func (r *rig) incoming(chat, from, text string, unread uint32) {
	r.t.Helper()
	id := r.incomingEvents(chat, from, text, unread)
	name := "Ada Lovelace"
	switch chat {
	case grace:
		name = "Grace Hopper"
	case group:
		name = "Engines"
	}
	r.notice(state.Notice{Account: "1", Chat: []byte(chat), Message: []byte(id), Title: name, Body: text,
		Sound: state.SoundChoice{Sound: state.SoundReceive}})
}

// incomingEvents is the core's side of incoming; it returns the message id.
func (r *rig) incomingEvents(chat, from, text string, unread uint32) string {
	r.t.Helper()
	r.seq++
	id := "m" + strconv.Itoa(r.seq)
	m := abi.Message{ID: []byte(id), Chat: []byte(chat), Sender: []byte(from), Timestamp: r.now.Unix()}
	m.Content.Tag = abi.ContentTagText
	m.Content.Text.Text = []byte(text)
	name, kind := "Ada Lovelace", abi.ChatKindDirect
	switch chat {
	case grace:
		name = "Grace Hopper"
	case group:
		name, kind = "Engines", abi.ChatKindGroup
	}
	r.pump(
		ev(abi.EventTagChatUpdated, func(e *abi.Event) {
			e.ChatUpdated.Chat = abi.Chat{ID: []byte(chat), Name: []byte(name), Kind: kind, Unread: unread, LastActivity: r.now.Unix()}
		}),
		ev(abi.EventTagIncoming, func(e *abi.Event) { e.Incoming.Chat, e.Incoming.Message = []byte(chat), m }),
	)
	return id
}

// read tells the model the chat has nothing unread, and queues the clear
// logic does for it.
func (r *rig) read(chat string) {
	r.t.Helper()
	name := "Ada Lovelace"
	if chat == group {
		name = "Engines"
	}
	r.pump(ev(abi.EventTagChatUpdated, func(e *abi.Event) {
		e.ChatUpdated.Chat = abi.Chat{ID: []byte(chat), Name: []byte(name), LastActivity: r.now.Unix()}
	}))
	r.s.NoticeClears = append(r.s.NoticeClears, state.NoticeClear{Account: "1", Chat: []byte(chat)})
	r.frame()
}

func (r *rig) openChat(chat string) {
	r.t.Helper()
	ref := r.a.Model.Chats.Find([]byte(chat))
	if ref == 0 {
		r.t.Fatalf("no chat %s", chat)
	}
	r.a.OpenChat(ref)
	r.frame()
}

func (r *rig) lastNote() notify.Note {
	r.t.Helper()
	if len(r.c.posts) == 0 {
		r.t.Fatal("nothing was posted")
	}
	return r.c.posts[len(r.c.posts)-1]
}

func (r *rig) sent(tag abi.CommandTag) []abi.Command {
	var out []abi.Command
	for _, c := range r.f.Sent() {
		if c.Tag == tag {
			out = append(out, c)
		}
	}
	return out
}

var errTest = errors.New("test")

// The fake center's events carry sources the framework does not use.
const (
	fakeResultSource   platform.Source = 200
	fakeResponseSource platform.Source = 201
)
