package picker

import (
	"gonativeui/img"
	"slices"
	"strings"
	"testing"
	"time"

	"gonativeui/ui"
	"gonativeui/widget/widgettest"

	"zapfast/abi"
	"zapfast/core"
	"zapfast/gocore/demo"
	"zapfast/state"
	"zapfast/ui/screen"
	"zapfast/ui/shell"
)

// The picker against the real demo core (the library: $ZAPFAST_CORE or a
// cargo build; skipped without one, and with -short). The demo answers GIF
// searches from six stock stills (a bad-key answer for the key
// zf-demo:bad-key), lists a shelf of stickers (Recent, Favorites, Received,
// a shared pack and a local one), and sends into the chat as upstream's core.
type demoRig struct {
	*rig
	chat []byte
}

func newDemoRig(t *testing.T) *demoRig {
	t.Helper()
	if testing.Short() {
		t.Skip("the demo core takes seconds")
	}
	if err := demo.Ready(""); err != nil {
		t.Skipf("no core library: %v", err)
	}
	c, err := demo.OpenChosen(core.Config{Wake: func() {}})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { c.Close(5 * time.Second) })
	s := state.New()
	s.Shell, s.Demo = shellLog{opened: make(chan string, 4)}, true
	a := s.AddAccount("1", nil, c)
	pk := New()
	kg := &kitGrab{}
	sh, err := shell.New(s, &screen.Set{Picker: pk, Dialogs: kg})
	if err != nil {
		t.Fatal(err)
	}
	mb := &menuBuilder{Shell: sh}
	cfg := shell.Config(s)
	r := &rig{Rig: widgettest.Open(t, cfg, int(cfg.Width), int(cfg.Height), 1, mb), t: t, s: s, a: a, pk: pk, kit: kg, mb: mb}
	r.Offscreen().UI.SetSystemAppearance(ui.Appearance{Known: true})
	r.Offscreen().UI.SetNativeMenus(true)
	d := &demoRig{rig: r}
	d.wait("chats", func() bool { return a.Model.Chats.Loaded() })
	var ref = a.Model.Chats.Find([]byte("393331234567@s.whatsapp.net")) // Ada Lovelace
	if ref == 0 {
		t.Fatal("the demo has no Ada Lovelace chat")
	}
	a.OpenChat(ref)
	ch, _ := a.Model.Chats.Get(ref)
	d.chat = append([]byte(nil), ch.ID()...)
	d.wait("the chat's messages", func() bool { v := a.Model.Conversation(d.chat); return v != nil && v.Len() > 0 })
	return d
}

// wait runs frames until ok, for up to ten seconds.
func (d *demoRig) wait(what string, ok func() bool) {
	d.t.Helper()
	for deadline := time.Now().Add(10 * time.Second); ; {
		d.Frames(1)
		if ok() {
			return
		}
		if time.Now().After(deadline) {
			d.t.Fatalf("no %s within 10 s:\n%s", what, widgettest.Dump(d.Tree()))
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// last is the chat's newest message, decoded.
func (d *demoRig) last() (m abi.Message, n int) {
	v := d.a.Model.Conversation(d.chat)
	if v == nil || v.Len() == 0 {
		return m, 0
	}
	v.Decode(v.Len()-1, &m)
	return m, v.Len()
}

func (d *demoRig) gifTiles() int {
	n := 0
	tr := d.Tree()
	for _, nd := range tr.Nodes {
		if nd.Role == ui.RoleButton && len(tr.Text(nd.Name)) > len("demo-gif-") && string(tr.Text(nd.Name)[:9]) == "demo-gif-" {
			n++
		}
	}
	return n
}

// DM-008, PK-005: the GIF tab lists the demo's six stills, narrows with the
// search, and sends the chosen one into the chat as a GIF.
func TestDemoGifs(t *testing.T) {
	d := newDemoRig(t)
	d.s.Settings.GiphyKey = []byte("zf-demo-key")
	d.open(state.PickerGifs)
	d.wait("six GIF stills", func() bool { return d.gifTiles() == 6 })
	d.Type("goat")
	d.Key(ui.KeyEnter, 0)
	d.wait("the search narrowed", func() bool { return d.gifTiles() < 6 })
	if _, ok := d.Find(ui.RoleButton, "demo-gif-goat"); !ok {
		t.Fatalf("the goat is not among the results:\n%s", widgettest.Dump(d.Tree()))
	}
	_, before := d.last()
	d.Click(ui.RoleButton, "demo-gif-goat")
	d.wait("the GIF in the chat", func() bool { _, n := d.last(); return n == before+1 })
	m, _ := d.last()
	if m.Content.Tag != abi.ContentTagVideo || !m.Content.Video.Gif || !m.FromMe {
		t.Errorf("the chat's newest message is not our GIF: %+v", m.Content.Tag)
	}
	if d.s.Panes.Picker != state.PickerNone {
		t.Error("the picker stayed open after sending")
	}
	if n := len(d.s.Toasts); n == 0 || string(d.s.Toasts[n-1].Text) != "Sending GIF…" {
		t.Errorf("toasts %+v", d.s.Toasts)
	}
}

// PK-006: the demo's bad key brings the prompt back with GIPHY's message.
func TestDemoGifBadKey(t *testing.T) {
	d := newDemoRig(t)
	d.s.Settings.GiphyKey = []byte("zf-demo:bad-key")
	d.open(state.PickerGifs)
	d.wait("the rejected-key prompt", func() bool {
		_, ok := d.Find(ui.RoleLabel, "This GIPHY API key was rejected. Create a free key at developers.giphy.com and paste it here. It is saved in your settings.")
		return ok
	})
	if _, ok := d.Find(ui.RoleLabel, "GIPHY rejected the API key (error 401)."); !ok {
		t.Errorf("GIPHY's message is not shown:\n%s", widgettest.Dump(d.Tree()))
	}
	// a good key clears it
	d.Click(ui.RoleTextField, "GIPHY API key")
	d.Key('A', ui.PrimaryMod())
	d.Type("zf-demo-key")
	d.Key(ui.KeyEnter, 0)
	d.wait("six GIF stills", func() bool { return d.gifTiles() == 6 })
}

// openStickers opens the Stickers tab and waits for the demo's shelves.
func (d *demoRig) openStickers() *library {
	d.t.Helper()
	d.open(state.PickerStickers)
	l := &d.pk.st.lib
	d.wait("the sticker shelves", func() bool { return len(l.recent) > 0 })
	return l
}

// DM-008, PK-007, PK-010, PK-013: the demo's shelves show as Recent,
// Favorites, Received, a shared pack and a local one, each with its tiles.
func TestDemoStickerShelves(t *testing.T) {
	d := newDemoRig(t)
	l := d.openStickers()
	for _, name := range []string{"Recent", "Favorites", "Received", "Launch party", "My stickers", "Add stickers"} {
		if !d.hasTab(name) {
			t.Errorf("no %q tab", name)
		}
	}
	for _, c := range []struct {
		tab  string
		want int
	}{{"Recent", len(l.recent)}, {"Favorites", len(l.favorites)}, {"Received", len(l.received)}, {"Launch party", 8}, {"My stickers", 2}} {
		d.Click(ui.RoleTab, c.tab)
		d.Frames(3)
		if c.want == 0 || d.tiles() != c.want {
			t.Errorf("%s shows %d tiles, the core listed %d", c.tab, d.tiles(), c.want)
		}
	}
	// the shared pack is not offered in a sticker's menu; the local one is
	d.Click(ui.RoleTab, "Recent")
	m := d.rightClick(ui.RoleButton, "Sticker", 0)
	want := []string{"Add to favorites", "Remove from recents", "-", "My stickers"}
	if got := m.titles(); !slices.Equal(got, want) {
		t.Errorf("Recent's menu %q, want %q", got, want)
	}
}

// PK-011: favouriting a sticker puts it first among the favourites (and
// takes it out of Received, as the core does); unfavouriting takes it out.
func TestDemoFavorites(t *testing.T) {
	d := newDemoRig(t)
	l := d.openStickers()
	was := len(l.favorites)
	pick := l.recent[0]
	d.choose(d.rightClick(ui.RoleButton, "Sticker", 0), "Add to favorites")
	d.wait("the favourite", func() bool { return len(l.favorites) == was+1 })
	if l.favorites[0] != pick {
		t.Errorf("favourites start with %q, want %q", l.favorites[0], pick)
	}
	d.Click(ui.RoleTab, "Favorites")
	d.Frames(3)
	if d.tiles() != was+1 {
		t.Errorf("%d favourites shown, want %d", d.tiles(), was+1)
	}
	m := d.rightClick(ui.RoleButton, "Sticker", 0)
	if got := m.titles(); !slices.Equal(got, []string{"Remove from favorites", "-", "My stickers"}) {
		t.Errorf("a favourite's menu %q", got)
	}
	d.choose(m, "Remove from favorites")
	d.wait("the favourite gone", func() bool { return len(l.favorites) == was })
	if slices.Contains(l.favorites, pick) {
		t.Error("the sticker is still a favourite")
	}
}

// PK-009: a sticker sent from the picker lands in the chat and becomes
// Recent's newest; Remove from recents takes one out.
func TestDemoSendAndRecents(t *testing.T) {
	d := newDemoRig(t)
	l := d.openStickers()
	n := len(l.recent)
	pick := l.recent[2]
	_, before := d.last()
	d.Play(widgettest.Click(ui.RoleButton, "Sticker").Nth(2))
	d.wait("the sticker in the chat", func() bool { _, k := d.last(); return k == before+1 })
	if m, _ := d.last(); m.Content.Tag != abi.ContentTagSticker || !m.FromMe {
		t.Errorf("the chat's newest message is %v, not our sticker", m.Content.Tag)
	}
	if d.s.Panes.Picker != state.PickerNone {
		t.Error("the picker stayed open after sending")
	}
	// the core's new list has it first, with nothing lost or doubled
	d.wait("the sent sticker first", func() bool { return len(l.recent) == n && l.recent[0] == pick })
	d.openStickers()
	d.choose(d.rightClick(ui.RoleButton, "Sticker", 1), "Remove from recents")
	d.wait("one fewer", func() bool { return len(l.recent) == n-1 })
	if l.recent[0] != pick {
		t.Error("the sent sticker lost its place")
	}
}

// PK-016: a local pack goes to the chat as a sticker pack message.
func TestDemoSendPack(t *testing.T) {
	d := newDemoRig(t)
	d.openStickers()
	d.Click(ui.RoleTab, "My stickers")
	d.Frames(2)
	_, before := d.last()
	d.Click(ui.RoleButton, "Send this pack to the chat")
	d.wait("the pack in the chat", func() bool { _, k := d.last(); return k == before+1 })
	if m, _ := d.last(); m.Content.Tag != abi.ContentTagStickerPack || string(m.Content.StickerPack.Name) != "My stickers" {
		t.Errorf("the chat's newest message: %v %+v", m.Content.Tag, m.Content.StickerPack)
	}
}

// PK-008: the demo's animated WebP sticker (astronaut-float) is decoded as an
// animation and plays only while the pointer is on it.
func TestDemoAnimatedWebPPlaysOnHover(t *testing.T) {
	d := newDemoRig(t)
	l := d.openStickers()
	moving := -1
	for i, p := range l.recent {
		if strings.HasSuffix(p, "astronaut-float.webp") {
			moving = i
		}
	}
	if moving < 0 || d.kit.k.Images() == nil {
		t.Skip("no animated sticker in Recent, or no renderer")
	}
	animation := func() img.Animation {
		tile := d.Must(ui.RoleButton, "Sticker")
		side := int32(tile.Bounds[2]-tile.Bounds[0]) - 8
		h, _ := d.kit.k.Images().Request(img.Key{Hash: img.HashString(l.recent[moving]) ^ d.a.Serial*0x9e3779b97f4a7c15, W: side, H: side, Flags: img.Animate}, img.File(l.recent[moving]))
		return d.kit.k.Images().Animation(h)
	}
	var an img.Animation
	for i := 0; i < 400 && !an.Valid(); i++ {
		time.Sleep(5 * time.Millisecond)
		d.Frames(1)
		an = animation()
	}
	if !an.Valid() || an.Frames() < 2 {
		t.Fatal("the animated WebP was not decoded as an animation")
	}
	if an.Stilled() {
		t.Skip("the system holds animations still")
	}
	other := (moving + 1) % len(l.recent)
	d.Play(widgettest.Move(ui.RoleButton, "Sticker").Nth(other), widgettest.Pause(500))
	if f := animation().Frame(); f != 0 {
		t.Errorf("an unhovered sticker is on frame %d", f)
	}
	d.Play(widgettest.Move(ui.RoleButton, "Sticker").Nth(moving), widgettest.Pause(1200))
	if animation().Frame() == 0 {
		t.Error("a hovered animated WebP does not play")
	}
}
