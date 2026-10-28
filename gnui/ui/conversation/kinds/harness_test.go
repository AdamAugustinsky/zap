package kinds_test

import (
	"errors"
	"image/png"
	"os"
	"testing"
	"time"

	"gonativeui/app"
	"gonativeui/theme"
	"gonativeui/ui"
	"gonativeui/widget/base"
	"gonativeui/widget/widgettest"

	"zapfast/abi"
	"zapfast/core/fake"
	"zapfast/model"
	"zapfast/state"
	"zapfast/ui/conversation/kinds"
	"zapfast/ui/screen"
)

const chatID = "1@s.whatsapp.net"

// page is a window that stands in for the transcript: a column of the open
// chat's blocks, each at the size Size gave it.
type page struct {
	s     *state.State
	env   screen.Env
	maxW  float32
	gap   float32
	rects map[string]screen.Rect // by message id, from the last frame
	only  map[string]bool
	sized map[string][2]float32
	quiet bool                                       // draw only: no records, which allocate
	probe func(e *screen.Env, v *model.Conversation) // runs in the frame, before the blocks
}

func (p *page) Init(a *app.App) error {
	p.env = screen.Env{K: screen.NewKit(a, nil), A: a, S: p.s}
	a.UI.Reserve(2048)
	p.rects = map[string]screen.Rect{}
	p.sized = map[string][2]float32{}
	return nil
}

func (p *page) Build(c *ui.Context) {
	p.s.Pump(0)
	e := &p.env
	e.K.Begin(c)
	a := p.s.Acct()
	if v := a.Conversation(); v != nil {
		sc := e.K.Scale()
		y := 8 * sc
		if p.probe != nil {
			p.probe(e, v)
			p.probe = nil
		}
		for i := 0; i < v.Len(); i++ {
			m := v.At(i)
			if !kinds.Has(m) {
				continue
			}
			if p.only != nil && !p.only[string(m.ID())] {
				continue
			}
			w, h := kinds.Size(e, m, p.maxW*sc)
			r := screen.Rect{X: 8 * sc, Y: y, W: w, H: h}
			if !p.quiet {
				id := string(m.ID())
				p.rects[id] = r
				p.sized[id] = [2]float32{w, h}
			}
			bubble(c, e, m, r, 8*sc)
			kinds.Build(e, m, r)
			y += h + p.gap*sc
		}
	}
	e.K.End()
}

// bubble draws the bubble a block sits in.
func bubble(c *ui.Context, e *screen.Env, m model.Message, r screen.Rect, pad float32) {
	th := e.K.Theme()
	col := th.Colors[theme.Fill1]
	if m.FromMe() {
		col = th.Colors[theme.Accent]
	}
	theme.At(c, r.X-pad, r.Y-pad, r.W+2*pad, r.H+2*pad)
	c.NextBG(col)
	c.NextRadius(12)
	c.BoxKeyed(base.Floating|ui.DrawBackground, ui.KeyN(ui.HashString(0, "bubble"), idh(m.ID())), nil)
}

func idh(b []byte) uint64 {
	h := uint64(14695981039346656037)
	for _, c := range b {
		h ^= uint64(c)
		h *= 1099511628211
	}
	return h
}

// rig is a page over a fake core with one open chat.
type rig struct {
	*widgettest.Rig
	t *testing.T
	p *page
	f *fake.Core
	s *state.State
	a *state.Account
}

func ev(tag abi.EventTag, set func(e *abi.Event)) *abi.Event {
	e := new(abi.Event)
	e.Tag = tag
	set(e)
	return e
}

func newRig(t *testing.T, msgs ...abi.Message) *rig {
	t.Helper()
	return newRigWith(t, nil, msgs...)
}

// noShell refuses to open anything.
type noShell struct{}

func (noShell) Open(string) error { return errors.ErrUnsupported }

func newRigWith(t *testing.T, set func(s *state.State), msgs ...abi.Message) *rig {
	t.Helper()
	kinds.Forget()
	f := fake.New(nil)
	s := state.New()
	s.Shell = noShell{} // nothing real opens from a test; SetOpeners' fakes win
	if set != nil {
		set(s)
	}
	a := s.AddAccount("1", nil, f)
	a.Settings.AutoDownload = false
	f.Emit(ev(abi.EventTagLink, func(e *abi.Event) { e.Link.Status.Tag = abi.LinkStatusTagConnected }),
		ev(abi.EventTagChats, func(e *abi.Event) {
			e.Chats = abi.EventChats{Batch: 1, Last: true, Chats: []abi.Chat{{ID: []byte(chatID), Name: []byte("Ada"), LastActivity: 1}}}
		}),
		ev(abi.EventTagMessages, func(e *abi.Event) {
			e.Messages = abi.EventMessages{Chat: []byte(chatID), Batch: 1, Last: true, Messages: msgs}
		}))
	p := &page{s: s, maxW: 330, gap: 8}
	r := &rig{t: t, p: p, f: f, s: s, a: a}
	cfg := app.Config{Title: "kinds", Width: 420, Height: 900, Clear: [4]float32{1, 1, 1, 1}}
	r.Rig = widgettest.Open(t, cfg, 420, 900, 1, p)
	r.Offscreen().UI.SetSystemAppearance(ui.Appearance{Known: true})
	r.Frames(2)
	a.OpenChat(a.Model.Chats.Find([]byte(chatID)))
	r.Frames(4)
	return r
}

func msg(id string, ts int64, from bool, c abi.Content) abi.Message {
	return abi.Message{ID: []byte(id), Chat: []byte(chatID), Sender: []byte(chatID), FromMe: from, Timestamp: ts, Content: c}
}

func (r *rig) view(id string) model.Message {
	v := r.a.Conversation()
	i := v.Find([]byte(id))
	if i < 0 {
		r.t.Fatalf("no message %q", id)
	}
	return v.At(i)
}

// shot writes the last frame to $KINDS_SHOT/name.png when that is set.
func (r *rig) shot(name string) {
	dir := os.Getenv("KINDS_SHOT")
	if dir == "" {
		return
	}
	f, err := os.Create(dir + "/" + name + ".png")
	if err != nil {
		r.t.Fatal(err)
	}
	defer f.Close()
	if err := png.Encode(f, r.Image()); err != nil {
		r.t.Fatal(err)
	}
}

func settle(r *rig) {
	r.Frames(3)
	time.Sleep(10 * time.Millisecond)
	r.Frames(3)
}

func TestSmoke(t *testing.T) {
	r := newRig(t,
		msg("a", 10, false, abi.Content{Tag: abi.ContentTagRevoked}),
		msg("b", 11, true, abi.Content{Tag: abi.ContentTagCall, Call: abi.ContentCall{Status: abi.CallStatusAnswered, Duration: 83}}),
		msg("c", 12, false, abi.Content{Tag: abi.ContentTagUnsupported, Unsupported: abi.ContentUnsupported{What: []byte("a gizmo")}}),
		msg("d", 13, false, abi.Content{Tag: abi.ContentTagPhoneOnly, PhoneOnly: abi.ContentPhoneOnly{ViewOnce: true, HasOnce: true, Once: abi.OnceMediaPhoto}}),
		msg("e", 14, false, abi.Content{Tag: abi.ContentTagCall, Call: abi.ContentCall{Status: abi.CallStatusMissed, Video: true}}),
	)
	settle(r)
	r.shot("smoke")
	t.Log(r.p.sized)
}

// names are the accessible names of the nodes with the role, in build order.
func (r *rig) names(role ui.Role) []string {
	t := r.Tree()
	var out []string
	for i := range t.Nodes {
		if t.Nodes[i].Role == role {
			out = append(out, string(t.Text(t.Nodes[i].Name)))
		}
	}
	return out
}

// count is how many nodes have the role and the name.
func (r *rig) count(role ui.Role, name string) (n int) {
	for _, s := range r.names(role) {
		if s == name {
			n++
		}
	}
	return n
}

// newRigAt is newRig with the app's clock at now (Unix seconds).
func newRigAt(t *testing.T, now int64, msgs ...abi.Message) *rig {
	t.Helper()
	return newRigWith(t, func(s *state.State) { s.Now = func() time.Time { return time.Unix(now, 0) } }, msgs...)
}
