package picker

import (
	"bytes"
	"image"
	"image/color"
	"image/draw"
	"image/gif"
	"image/png"
	"os"
	"path/filepath"
	"testing"
	"time"

	"gonativeui/img"
	"gonativeui/ui"
	"gonativeui/widget/widgettest"

	"zapfast/abi"
	"zapfast/core/fake"
	"zapfast/model"
	"zapfast/state"
	"zapfast/ui/screen"
	"zapfast/ui/shell"
)

// rig is the main window offscreen with the picker in its slot, a chat open
// and the fake core behind it.
type rig struct {
	*widgettest.Rig
	t  *testing.T
	s  *state.State
	f  *fake.Core
	a  *state.Account
	pk *Screen
	// kit is the main window's kit, for its image cache.
	kit *kitGrab
	mb  *menuBuilder
}

const (
	chatID = "1@s.whatsapp.net"
	msgID  = "m1"
)

func ev(tag abi.EventTag, set func(e *abi.Event)) *abi.Event {
	e := new(abi.Event)
	e.Tag = tag
	set(e)
	return e
}

func newRig(t *testing.T) *rig {
	t.Helper()
	f := fake.New(nil)
	s := state.New()
	pk := New()
	kg := &kitGrab{}
	sh, err := shell.New(s, &screen.Set{Picker: pk, Dialogs: kg})
	if err != nil {
		t.Fatal(err)
	}
	sh.Traffic = true
	s.Shell = shellLog{opened: make(chan string, 4)}
	a := s.AddAccount("1", nil, f)
	f.Emit(
		ev(abi.EventTagLink, func(e *abi.Event) { e.Link.Status.Tag = abi.LinkStatusTagConnected }),
		ev(abi.EventTagChats, func(e *abi.Event) {
			e.Chats = abi.EventChats{Batch: 1, Last: true, Chats: []abi.Chat{{ID: []byte(chatID), Name: []byte("Ada Lovelace"), LastActivity: 20}}}
		}),
		ev(abi.EventTagMessages, func(e *abi.Event) {
			var m abi.Message
			m.ID, m.Chat, m.Sender = []byte(msgID), []byte(chatID), []byte(chatID)
			m.Content.Tag = abi.ContentTagText
			m.Content.Text.Text = []byte("Hello there")
			m.Timestamp = 10
			e.Messages = abi.EventMessages{Chat: []byte(chatID), Batch: 1, Last: true, Messages: []abi.Message{m}}
		}))
	cfg := shell.Config(s)
	mb := &menuBuilder{Shell: sh}
	r := &rig{Rig: widgettest.Open(t, cfg, int(cfg.Width), int(cfg.Height), 1, mb), t: t, s: s, f: f, a: a, pk: pk, kit: kg, mb: mb}
	r.Offscreen().UI.SetSystemAppearance(ui.Appearance{Known: true})
	r.Offscreen().UI.SetNativeMenus(true)
	r.Frames(4)
	ref := a.Model.Chats.Find([]byte(chatID))
	if ref == 0 {
		t.Fatal("no chat")
	}
	a.OpenChat(ref)
	r.Frames(3)
	return r
}

// open opens the picker on a tab, as the composer's button does.
func (r *rig) open(tab state.Picker) {
	r.t.Helper()
	r.s.Panes.Picker = tab
	r.s.Touch()
	r.Frames(4)
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

func (r *rig) emit(events ...*abi.Event) {
	r.t.Helper()
	if err := r.f.Emit(events...); err != nil {
		r.t.Fatal(err)
	}
	r.Frames(3)
}

// insert is what the picker asked the composer to insert since the last
// call (State.TakeInsert).
func (r *rig) insert() (string, bool) {
	b := r.s.TakeInsert()
	return string(b), len(b) > 0
}

// kitGrab is an overlay that builds nothing and keeps the window's kit, so a
// test can reach the image cache the picker draws from.
type kitGrab struct {
	k interface{ Images() *img.Cache }
}

func (g *kitGrab) Init(e *screen.Env) error { g.k = e.K; return nil }
func (g *kitGrab) Build(*screen.Env)        {}

// menuBuilder is the shell with a way to answer a native context menu: the
// rig has no window to pop one up, so a test takes the request the picker
// made and delivers the outcome in the next frame, as the app's loop does.
type menuBuilder struct {
	*shell.Shell
	owner ui.Key
	item  int
	out   ui.MenuOutcome
}

func (m *menuBuilder) Build(c *ui.Context) {
	if m.out != ui.MenuPending {
		c.Input().SetMenuResult(m.owner, m.item, m.out)
		m.out = ui.MenuPending
	}
	m.Shell.Build(c)
}

// menu is the native menu the last right click asked for, with its items
// copied.
type menu struct {
	owner ui.Key
	items []ui.MenuItem
}

// rightClick right-clicks the nth node with the role and name and returns
// the native menu it asked for (nil items: none).
func (r *rig) rightClick(role ui.Role, name string, n int) menu {
	r.t.Helper()
	r.Play(widgettest.RClick(role, name).Nth(n), widgettest.Idle(2))
	req, ok := r.Offscreen().UI.TakeMenuRequest()
	if !ok || req.Kind != ui.MenuItems {
		return menu{}
	}
	return menu{owner: req.Owner, items: append([]ui.MenuItem(nil), req.Items...)}
}

// choose answers the menu with the item titled title (the person chose it).
func (r *rig) choose(m menu, title string) {
	r.t.Helper()
	for _, it := range m.items {
		if it.Title == title && !it.Disabled {
			r.mb.owner, r.mb.item, r.mb.out = m.owner, it.ID, ui.MenuChosen
			r.Frames(3)
			return
		}
	}
	r.t.Fatalf("no enabled item %q in %+v", title, m.items)
}

// titles lists the titles of a menu's items, a separator as "-" and a
// checked item with a leading "✓ ".
func (m menu) titles() []string {
	var out []string
	for _, it := range m.items {
		switch {
		case it.Separator:
			out = append(out, "-")
		case it.Mark == ui.MarkOn:
			out = append(out, "✓ "+it.Title)
		default:
			out = append(out, it.Title)
		}
	}
	return out
}

// wantOpened checks that the picker asked the state's Shell to open url.
func (r *rig) wantOpened(url string) {
	r.t.Helper()
	select {
	case got := <-r.s.Shell.(shellLog).opened:
		if got != url {
			r.t.Errorf("opened %q, want %q", got, url)
		}
	case <-time.After(2 * time.Second):
		r.t.Errorf("%s was not opened through the Shell", url)
	}
}

// shellLog is a core.Shell that records what it is asked to open instead of
// opening it.
type shellLog struct{ opened chan string }

func (l shellLog) Open(path string) error { l.opened <- path; return nil }

var _ = model.S

// stickerFiles writes one small picture per name, each its own colour, in
// a temporary directory, and returns their paths in order.
func stickerFiles(t *testing.T, names ...string) []string {
	t.Helper()
	dir := t.TempDir()
	var out []string
	for i, n := range names {
		im := image.NewRGBA(image.Rect(0, 0, 16, 16))
		draw.Draw(im, im.Bounds(), &image.Uniform{color.RGBA{uint8(40 * i), uint8(200 - 30*i), 120, 255}}, image.Point{}, draw.Src)
		var b bytes.Buffer
		if err := png.Encode(&b, im); err != nil {
			t.Fatal(err)
		}
		p := filepath.Join(dir, n)
		if err := os.WriteFile(p, b.Bytes(), 0o644); err != nil {
			t.Fatal(err)
		}
		out = append(out, p)
	}
	return out
}

// animatedFile writes a three-frame GIF, 100 ms a frame.
func animatedFile(t *testing.T, name string) string {
	t.Helper()
	g := &gif.GIF{}
	pal := color.Palette{color.RGBA{255, 0, 0, 255}, color.RGBA{0, 255, 0, 255}, color.RGBA{0, 0, 255, 255}}
	for i := 0; i < 3; i++ {
		im := image.NewPaletted(image.Rect(0, 0, 16, 16), pal)
		for j := range im.Pix {
			im.Pix[j] = uint8(i)
		}
		g.Image, g.Delay = append(g.Image, im), append(g.Delay, 10)
	}
	var b bytes.Buffer
	if err := gif.EncodeAll(&b, g); err != nil {
		t.Fatal(err)
	}
	p := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(p, b.Bytes(), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

// shelves is what a Stickers event carries.
type shelves struct {
	favorites, recent, received []string
	packs                       []pack
	tags                        map[string][]string
}

// stickersEvent is the core's Stickers event for sh: the rows in the order
// zfcore's dto sends them.
func stickersEvent(sh shelves) *abi.Event {
	return ev(abi.EventTagStickers, func(e *abi.Event) {
		var rows []abi.StickerEntry
		add := func(en abi.StickerEntry) { rows = append(rows, en) }
		for _, p := range sh.favorites {
			add(abi.StickerEntry{Tag: abi.StickerEntryTagFavorite, Favorite: abi.StickerEntryFavorite{Path: []byte(p)}})
		}
		for _, pk := range sh.packs {
			add(abi.StickerEntry{Tag: abi.StickerEntryTagPack, Pack: abi.StickerEntryPack{Name: []byte(pk.name), Dir: []byte(pk.dir), Local: pk.local}})
			for _, p := range pk.stickers {
				add(abi.StickerEntry{Tag: abi.StickerEntryTagPackSticker, PackSticker: abi.StickerEntryPackSticker{Path: []byte(p)}})
			}
		}
		for _, p := range sh.recent {
			add(abi.StickerEntry{Tag: abi.StickerEntryTagRecent, Recent: abi.StickerEntryRecent{Path: []byte(p)}})
		}
		for _, p := range sh.received {
			add(abi.StickerEntry{Tag: abi.StickerEntryTagReceived, Received: abi.StickerEntryReceived{Path: []byte(p)}})
		}
		for p, tags := range sh.tags {
			en := abi.StickerEntry{Tag: abi.StickerEntryTagTagged, Tagged: abi.StickerEntryTagged{Path: []byte(p)}}
			for _, t := range tags {
				en.Tagged.Emojis = append(en.Tagged.Emojis, []byte(t))
			}
			add(en)
		}
		e.Stickers = abi.EventStickers{Batch: 1, Last: true, Entries: rows}
	})
}

// anchorRect is a place for the reaction picker to open at.
func anchorRect() screen.Rect { return screen.Rect{X: 500, Y: 200, W: 30, H: 30} }
