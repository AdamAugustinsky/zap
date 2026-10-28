package stickermaker

import (
	"sync"
	"testing"

	"gonativeui/app"
	"gonativeui/img"
	"gonativeui/ui"
	"gonativeui/widget/widgettest"

	"zapfast/abi"
	"zapfast/core/fake"
	"zapfast/state"
	"zapfast/ui/screen"
)

// host is a window holding only the sticker maker, over a state with one account
// on a fake core.
type host struct {
	s     *state.State
	scr   *Screen
	env   screen.Env
	cache *img.Cache
}

func (h *host) Init(a *app.App) error {
	h.env = screen.Env{K: screen.NewKit(a, nil), A: a, S: h.s}
	// The window's one image cache, as the shell and the window helper make it.
	h.cache = img.NewCache(img.Options{Renderer: a.R, GPUBytes: 32 << 20, CPUBytes: 32 << 20, Workers: 2, Entries: 64})
	h.env.K.SetImages(h.cache)
	a.UI.Reserve(2048)
	return h.scr.Init(&h.env)
}

func (h *host) Build(c *ui.Context) {
	h.s.Pump(0)
	h.env.K.Begin(c)
	if h.cache.Poll() {
		c.RequestFrame()
	}
	h.scr.Build(&h.env)
	h.env.K.End()
}

type rig struct {
	*widgettest.Rig
	t *testing.T
	s *state.State
	f *fake.Core
	h *host
	a *state.Account
}

func ev(tag abi.EventTag, set func(e *abi.Event)) *abi.Event {
	e := new(abi.Event)
	e.Tag = tag
	set(e)
	return e
}

func chatsEvent(chats ...abi.Chat) *abi.Event {
	return ev(abi.EventTagChats, func(e *abi.Event) {
		e.Chats = abi.EventChats{Batch: 1, Last: true, Chats: chats}
	})
}

func linkedEvent() *abi.Event {
	return ev(abi.EventTagLink, func(e *abi.Event) { e.Link.Status.Tag = abi.LinkStatusTagConnected })
}

func newRig(t *testing.T, events ...*abi.Event) *rig { return newRigSize(t, 900, 700, events...) }

// newRigSize is newRig with a window of w × h points.
func newRigSize(t *testing.T, w, h int, events ...*abi.Event) *rig {
	t.Helper()
	f := fake.New(nil)
	f.Emit(events...)
	s := state.New()
	s.Shell = &fakeShell{} // never the real opener: no test may open a link
	a := s.AddAccount("1", nil, f)
	hst := &host{s: s, scr: New()}
	cfg := app.Config{Title: "stickermaker", Width: float32(w), Height: float32(h)}
	r := &rig{Rig: widgettest.Open(t, cfg, w, h, 1, hst), t: t, s: s, f: f, h: hst, a: a}
	r.Offscreen().UI.SetSystemAppearance(ui.Appearance{Known: true})
	r.Frames(3)
	return r
}

// open shows dialog d and lets it build.
func (r *rig) open(d state.Dialog) {
	r.t.Helper()
	r.s.OpenDialog(d)
	r.Frames(4)
}

// sent returns the commands with the tag sent so far.
func (r *rig) sent(tag abi.CommandTag) []abi.Command {
	var out []abi.Command
	for _, c := range r.f.Sent() {
		if c.Tag == tag {
			out = append(out, c)
		}
	}
	return out
}

func (r *rig) has(role ui.Role, name string) bool {
	_, ok := r.Find(role, name)
	return ok
}

func (r *rig) click(role ui.Role, name string) {
	r.t.Helper()
	r.Play(widgettest.Click(role, name))
	r.Frames(3)
}

func chat(id, name string, last int64) abi.Chat {
	return abi.Chat{ID: []byte(id), Name: []byte(name), LastActivity: last}
}

// fakeShell records what would have been opened.
type fakeShell struct {
	mu     sync.Mutex
	opened []string
	got    chan string
}

func (f *fakeShell) Open(path string) error {
	f.mu.Lock()
	f.opened = append(f.opened, path)
	f.mu.Unlock()
	return nil
}

func (f *fakeShell) paths() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.opened...)
}
