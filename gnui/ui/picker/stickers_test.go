package picker

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"slices"
	"testing"
	"time"

	"gonativeui/img"
	"gonativeui/ui"
	"gonativeui/widget/widgettest"

	"zapfast/abi"
	"zapfast/state"
)

// sticker files: n distinct files in one directory, returned in order.
func files(t *testing.T, n int) []string {
	names := make([]string, n)
	for i := range names {
		names[i] = string(rune('a'+i)) + ".webp"
	}
	return stickerFiles(t, names...)
}

func (r *rig) tiles() int {
	n := 0
	tr := r.Tree()
	for _, nd := range tr.Nodes {
		if nd.Role == ui.RoleButton && string(tr.Text(nd.Name)) == "Sticker" {
			n++
		}
	}
	return n
}

func (r *rig) hasTab(name string) bool {
	_, ok := r.Find(ui.RoleTab, name)
	return ok
}

// PK-007: the strip is Recent, Favorites, Received, each pack, and +.
func TestTheStickerStripHoldsTheShelvesThenPacksThenPlus(t *testing.T) {
	r := newRig(t)
	f := files(t, 3)
	r.open(state.PickerStickers)
	r.emit(stickersEvent(shelves{packs: []pack{
		{name: "Bom dia", dir: "/p/bom", local: true, stickers: f[:1]},
		{name: "Frogs", dir: "/p/frogs", stickers: f[1:]},
	}}))
	for _, name := range []string{"Recent", "Favorites", "Received", "Bom dia", "Frogs", "Add stickers"} {
		if !r.hasTab(name) {
			t.Errorf("no %q tab:\n%s", name, widgettest.Dump(r.Tree()))
		}
	}
	// the strip is in that order
	var order []string
	want := []string{"Recent", "Favorites", "Received", "Bom dia", "Frogs", "Add stickers"}
	tr := r.Tree()
	for _, n := range tr.Nodes {
		if n.Role == ui.RoleTab {
			for _, w := range want {
				if string(tr.Text(n.Name)) == w {
					order = append(order, w)
				}
			}
		}
	}
	if len(order) != len(want) {
		t.Fatalf("order %v", order)
	}
	for i := range want {
		if order[i] != want[i] {
			t.Fatalf("order %v, want %v", order, want)
		}
	}
	r.Click(ui.RoleTab, "Frogs")
	r.Frames(2)
	if r.tiles() != 2 {
		t.Errorf("the Frogs pack shows %d stickers", r.tiles())
	}
	if tab, _ := r.Find(ui.RoleTab, "Frogs"); tab.State&ui.SemSelected == 0 {
		t.Error("Frogs is not the chosen tab")
	}
}

// PK-008: clicking a sticker sends it to the open chat, quoting the reply,
// and closes the picker.
func TestClickingAStickerSendsIt(t *testing.T) {
	r := newRig(t)
	f := files(t, 3)
	r.s.Panes.Reply.Add([]byte(msgID))
	r.open(state.PickerStickers)
	r.emit(stickersEvent(shelves{recent: f}))
	if r.tiles() != 3 {
		t.Fatalf("%d tiles", r.tiles())
	}
	r.Play(widgettest.Click(ui.RoleButton, "Sticker").Nth(1), widgettest.Idle(2))
	sent := r.sent(abi.CommandTagSendSticker)
	if len(sent) != 1 || string(sent[0].SendSticker.Path) != f[1] || string(sent[0].SendSticker.Chat) != chatID {
		t.Fatalf("stickers sent: %+v", sent)
	}
	if !sent[0].SendSticker.HasQuoting || string(sent[0].SendSticker.Quoting.ID) != msgID {
		t.Errorf("no quote: %+v", sent[0].SendSticker)
	}
	if r.s.Panes.Picker != state.PickerNone || !r.s.Req.JumpToBottom || !r.s.Req.FocusComposer {
		t.Error("the picker stayed, or the composer and transcript were not told")
	}
}

// PK-009: Recent's menu removes a sticker here and on the phone.
func TestRemoveFromRecents(t *testing.T) {
	r := newRig(t)
	f := files(t, 3)
	r.open(state.PickerStickers)
	r.emit(stickersEvent(shelves{recent: f}))
	m := r.rightClick(ui.RoleButton, "Sticker", 0)
	if got := m.titles(); !slices.Equal(got, []string{"Add to favorites", "Remove from recents"}) {
		t.Fatalf("Recent's menu: %q", got)
	}
	r.choose(m, "Remove from recents")
	sent := r.sent(abi.CommandTagRemoveRecentSticker)
	if len(sent) != 1 || string(sent[0].RemoveRecentSticker.Path) != f[0] {
		t.Fatalf("removals: %+v", sent)
	}
	if r.tiles() != 2 {
		t.Errorf("%d tiles after removing one", r.tiles())
	}
	// the menu of a favorite does not offer it
	r.Click(ui.RoleTab, "Favorites")
	r.emit(stickersEvent(shelves{recent: f, favorites: f[:1]}))
	if got := r.rightClick(ui.RoleButton, "Sticker", 0).titles(); !slices.Equal(got, []string{"Remove from favorites"}) {
		t.Errorf("Favorites' menu: %q", got)
	}
}

// PK-011: right-click adds a sticker to the favorites, or takes it out.
func TestFavoritesFromTheMenu(t *testing.T) {
	r := newRig(t)
	f := files(t, 2)
	r.open(state.PickerStickers)
	r.emit(stickersEvent(shelves{recent: f}))
	r.choose(r.rightClick(ui.RoleButton, "Sticker", 1), "Add to favorites")
	saved := r.sent(abi.CommandTagSaveSticker)
	if len(saved) != 1 || string(saved[0].SaveSticker.Path) != f[1] {
		t.Fatalf("saved: %+v", saved)
	}
	if n := len(r.s.Toasts); n == 0 || string(r.s.Toasts[n-1].Text) != "Added to favorites" {
		t.Errorf("toasts %+v", r.s.Toasts)
	}
	r.emit(stickersEvent(shelves{recent: f, favorites: f[1:]}))
	r.Click(ui.RoleTab, "Favorites")
	r.Frames(2)
	if r.tiles() != 1 {
		t.Fatalf("%d favorites shown", r.tiles())
	}
	m := r.rightClick(ui.RoleButton, "Sticker", 0)
	if got := m.titles(); !slices.Equal(got, []string{"Remove from favorites"}) {
		t.Fatalf("a favorite's menu: %q", got)
	}
	r.choose(m, "Remove from favorites")
	forgot := r.sent(abi.CommandTagForgetSticker)
	if len(forgot) != 1 || string(forgot[0].ForgetSticker.Path) != f[1] {
		t.Fatalf("forgotten: %+v", forgot)
	}
}

// PK-010: Received lists what the core says, and says so when empty.
func TestReceivedShelf(t *testing.T) {
	r := newRig(t)
	f := files(t, 2)
	r.open(state.PickerStickers)
	r.emit(stickersEvent(shelves{received: f}))
	r.Click(ui.RoleTab, "Received")
	r.Frames(2)
	if r.tiles() != 2 {
		t.Errorf("%d received stickers", r.tiles())
	}
}

// PK-012: search finds by emoji, a word naming an emoji, or a pack's name.
func TestStickerSearch(t *testing.T) {
	r := newRig(t)
	f := files(t, 4)
	r.open(state.PickerStickers)
	r.emit(stickersEvent(shelves{
		recent: f[:1],
		packs:  []pack{{name: "Ducks", dir: "/p/ducks", stickers: f[1:3]}},
		tags: map[string][]string{
			f[0]: {"🐸"},
			f[1]: {"🦆", "😂"},
			f[3]: {"🐱"},
		},
		received: f[3:],
	}))
	search := func(q string) int {
		r.Click(ui.RoleSearchField, "Search by emoji, word, or pack")
		r.Key('A', ui.PrimaryMod())
		r.Type(q)
		r.Frames(3)
		return r.tiles()
	}
	if n := search("🦆"); n != 1 {
		t.Errorf("an emoji found %d", n)
	}
	if n := search("frog"); n != 1 {
		t.Errorf("a word naming an emoji found %d", n)
	}
	if n := search("duck"); n != 2 {
		t.Errorf("a pack's name found %d, want its two stickers", n)
	}
	if n := search("🐱"); n != 1 {
		t.Errorf("a received sticker found %d", n)
	}
	if n := search("zzzz"); n != 0 {
		t.Errorf("nothing matches but %d found", n)
	}
	if _, ok := r.Find(ui.RoleLabel, "No stickers match your search."); !ok {
		t.Error("no note for an empty search")
	}
}

func TestLibrarySearchOrderAndMatching(t *testing.T) {
	l := library{
		favorites: []string{"/fav/laugh.webp", "/fav/love.webp"},
		recent:    []string{"/recent/frog.webp", "/fav/laugh.webp"},
		packs: []pack{
			{name: "Ducks", dir: "/p/ducks", stickers: []string{"/packs/Ducks/1.webp", "/packs/Ducks/2.webp"}},
			{name: "Bom dia", dir: "/p/bom", stickers: []string{"/packs/Bom dia/1.webp"}},
		},
		received: []string{"/received/cat.webp"},
		emojis: map[string][]string{
			"/fav/laugh.webp": {"😂"}, "/fav/love.webp": {"❤️", "😍"}, "/packs/Ducks/1.webp": {"🦆", "😂"},
			"/recent/frog.webp": {"🐸"}, "/received/cat.webp": {"🐱"},
		},
	}
	eq := func(got []string, want ...string) {
		t.Helper()
		if len(got) != len(want) {
			t.Fatalf("got %v, want %v", got, want)
		}
		for i := range want {
			if got[i] != want[i] {
				t.Fatalf("got %v, want %v", got, want)
			}
		}
	}
	eq(l.search("😂"), "/fav/laugh.webp", "/packs/Ducks/1.webp")
	eq(l.search("❤"), "/fav/love.webp") // with or without the presentation selector
	eq(l.search("frog"), "/recent/frog.webp")
	eq(l.search("DUCK"), "/packs/Ducks/1.webp", "/packs/Ducks/2.webp")
	eq(l.search("bom"), "/packs/Bom dia/1.webp")
	eq(l.search("🐱"), "/received/cat.webp")
	eq(l.search("   "))
	eq(l.search("zzzz"))
	eq(l.search("😂 🦆"), "/packs/Ducks/1.webp") // every term must match
}

// PK-013: empty shelves explain themselves.
func TestEmptyShelvesExplainThemselves(t *testing.T) {
	r := newRig(t)
	r.open(state.PickerStickers)
	if _, ok := r.Find(ui.RoleLabel, "Loading your stickers…"); !ok {
		t.Error("no loading note while the lists are asked for")
	}
	r.emit(stickersEvent(shelves{}))
	for _, c := range []struct{ tab, note string }{
		{"Recent", "Stickers you send appear here."},
		{"Favorites", "Right-click any sticker to add it to your favorites. They stay in sync with your phone."},
		{"Received", "Stickers people send you appear here."},
	} {
		r.Click(ui.RoleTab, c.tab)
		r.Frames(2)
		if _, ok := r.Find(ui.RoleLabel, c.note); !ok {
			t.Errorf("%s: no %q", c.tab, c.note)
		}
	}
	r.emit(stickersEvent(shelves{packs: []pack{{name: "Mine", dir: "/p/mine", local: true}}}))
	r.Click(ui.RoleTab, "Mine")
	r.Frames(2)
	if _, ok := r.Find(ui.RoleLabel, "Nothing here yet. Right-click any sticker to add it to this pack."); !ok {
		t.Error("no note for an empty pack")
	}
}

// PK-014: the + page imports from a link or a file, and makes a pack.
func TestTheAddPage(t *testing.T) {
	r := newRig(t)
	r.open(state.PickerStickers)
	r.Click(ui.RoleTab, "Add stickers")
	r.Frames(2)
	link := "https://signal.art/addstickers/#pack_id=0123456789abcdef0123456789abcdef&pack_key=" +
		"0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"
	r.Click(ui.RoleTextField, "Paste a signal.art link")
	r.Type("https://signal.art/addstickers/#pack_id=short")
	r.Frames(2)
	if len(r.sent(abi.CommandTagImportStickerUrl)) != 0 {
		t.Error("imported from an incomplete link")
	}
	r.Key('A', ui.PrimaryMod())
	r.Type(link)
	r.Frames(3)
	imp := r.sent(abi.CommandTagImportStickerUrl)
	if len(imp) != 1 || string(imp[0].ImportStickerUrl.URL) != link {
		t.Fatalf("imports: %+v", imp)
	}
	if _, ok := r.Find(ui.RoleLabel, "Importing the pack…"); !ok {
		t.Error("no Importing the pack…")
	}
	r.emit(stickersEvent(shelves{}))
	if _, ok := r.Find(ui.RoleLabel, "Importing the pack…"); ok {
		t.Error("still importing when the lists came back")
	}
	r.Click(ui.RoleButton, "Open file")
	r.Frames(2)
	if len(r.sent(abi.CommandTagPickStickerArchive)) != 1 {
		t.Error("Open file did not ask the core for a file")
	}
	r.Click(ui.RoleButton, "Make a sticker from a picture…")
	r.Frames(2)
	if d := r.s.Dialog; d.Kind != state.DialogStickerMaker || d.Path != nil || d.Text != nil {
		t.Errorf("the sticker maker was not opened empty: %+v", d)
	}
	r.s.CloseDialog()
	r.Frames(2)
	r.Click(ui.RoleButton, "Find packs")
	r.wantOpened("https://signalstickers.org/")
	// a new pack: named, created, and shown once it is listed
	r.Click(ui.RoleTextField, "New pack…")
	r.Type("  Bom dia ")
	r.Click(ui.RoleButton, "Create pack")
	r.Frames(2)
	mk := r.sent(abi.CommandTagCreateStickerPack)
	if len(mk) != 1 || string(mk[0].CreateStickerPack.Name) != "Bom dia" {
		t.Fatalf("packs created: %+v", mk)
	}
	r.emit(stickersEvent(shelves{packs: []pack{{name: "Bom dia", dir: "/p/bom", local: true}}}))
	if r.pk.st.shelf != shelfPack || r.pk.st.shelfDir != "/p/bom" {
		t.Errorf("the new pack is not shown: shelf %v %q", r.pk.st.shelf, r.pk.st.shelfDir)
	}
}

func TestSignalLinks(t *testing.T) {
	id, key := "0123456789abcdef0123456789abcdef", "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"
	for url, want := range map[string]bool{
		"https://signal.art/addstickers/#pack_id=" + id + "&pack_key=" + key: true,
		"pack_id=" + id + "&pack_key=" + key:                                 true,
		"https://signal.art/addstickers/#pack_id=" + id:                      false,
		"https://signal.art/addstickers/#pack_id=" + id + "&pack_key=00":     false,
		"https://example.com": false,
	} {
		if got := looksLikeSignalURL(url); got != want {
			t.Errorf("%q: %v", url, got)
		}
	}
}

// PK-015: a pack's delete button asks first.
func TestRemovingAPackAsks(t *testing.T) {
	r := newRig(t)
	f := files(t, 1)
	r.open(state.PickerStickers)
	r.emit(stickersEvent(shelves{packs: []pack{{name: "Bom dia", dir: "/p/bom", local: true, stickers: f}}}))
	r.Click(ui.RoleTab, "Bom dia")
	r.Frames(2)
	r.Click(ui.RoleButton, "Remove this pack")
	r.Frames(2)
	d := r.s.Dialog
	if d.Kind != state.DialogConfirmRemoveStickerPack || string(d.Text) != "Bom dia" || string(d.Path) != "/p/bom" {
		t.Fatalf("dialog %+v", d)
	}
	if len(r.sent(abi.CommandTagDeleteStickerPack)) != 0 {
		t.Error("the pack was deleted before the question was answered")
	}
	// the dialog is open: a press on it does not close the picker
	if r.s.Panes.Picker != state.PickerStickers {
		t.Error("the dialog closed the picker")
	}
	// the pack goes: the strip leaves it and the tab falls back to Recent
	r.s.CloseDialog()
	r.emit(stickersEvent(shelves{}))
	if r.hasTab("Bom dia") || r.pk.st.shelf != shelfRecent {
		t.Errorf("the deleted pack stays: shelf %v", r.pk.st.shelf)
	}
}

// PK-016: the send arrow sends the pack to the open chat.
func TestSendingAPack(t *testing.T) {
	r := newRig(t)
	f := files(t, 2)
	r.open(state.PickerStickers)
	r.emit(stickersEvent(shelves{packs: []pack{{name: "Frogs", dir: "/p/frogs", stickers: f}}}))
	r.Click(ui.RoleTab, "Frogs")
	r.Frames(2)
	if _, ok := r.Find(ui.RoleButton, "Remove this pack"); !ok {
		t.Error("a pack with no remove button")
	}
	r.Click(ui.RoleButton, "Send this pack to the chat")
	r.Frames(2)
	sent := r.sent(abi.CommandTagSendStickerPack)
	if len(sent) != 1 || string(sent[0].SendStickerPack.Dir) != "/p/frogs" || string(sent[0].SendStickerPack.Chat) != chatID {
		t.Fatalf("packs sent: %+v", sent)
	}
	if n := len(r.s.Toasts); n == 0 || string(r.s.Toasts[n-1].Text) != "Sending the sticker pack…" {
		t.Errorf("toasts %+v", r.s.Toasts)
	}
	if r.s.Panes.Picker != state.PickerNone {
		t.Error("the picker stayed open")
	}
}

// PK-017: a sticker's menu lists the local packs, checks the ones that hold
// it, and choosing one adds or removes it.
func TestFilingAStickerIntoPacks(t *testing.T) {
	r := newRig(t)
	dir := t.TempDir()
	content := []byte("sun")
	sum := sha256.Sum256(content)
	hash := hex.EncodeToString(sum[:])
	sticker := filepath.Join(dir, "from-a-chat.webp")
	member := filepath.Join(dir, "pack", hash+".webp")
	if err := os.MkdirAll(filepath.Dir(member), 0o755); err != nil {
		t.Fatal(err)
	}
	for _, p := range []string{sticker, member} {
		if err := os.WriteFile(p, content, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	other := stickerFiles(t, "x.webp")[0]
	r.open(state.PickerStickers)
	r.emit(stickersEvent(shelves{
		recent: []string{sticker},
		packs: []pack{
			{name: "Has it", dir: "/p/has", local: true, stickers: []string{member}},
			{name: "Lacks it", dir: "/p/lacks", local: true, stickers: []string{other}},
			{name: "Shared", dir: "/p/shared", stickers: []string{other}},
		},
	}))
	m := r.rightClick(ui.RoleButton, "Sticker", 0)
	// upstream's order: favorites, recents, a rule, then the local packs
	// (not the shared one), checked where they hold the sticker
	want := []string{"Add to favorites", "Remove from recents", "-", "✓ Has it", "Lacks it"}
	if got := m.titles(); !slices.Equal(got, want) {
		t.Fatalf("the menu is %q, want %q", got, want)
	}
	r.choose(m, "Lacks it")
	set := r.sent(abi.CommandTagSetStickerPack)
	if len(set) != 1 || string(set[0].SetStickerPack.Pack) != "/p/lacks" || string(set[0].SetStickerPack.Sticker) != sticker || !set[0].SetStickerPack.Member {
		t.Fatalf("adds: %+v", set)
	}
	// the pack that holds it (one copy per content) takes it out
	r.choose(r.rightClick(ui.RoleButton, "Sticker", 0), "Has it")
	set = r.sent(abi.CommandTagSetStickerPack)
	if len(set) != 2 || string(set[1].SetStickerPack.Pack) != "/p/has" || set[1].SetStickerPack.Member {
		t.Fatalf("removals: %+v", set)
	}
}

func TestPackHoldsByContentHash(t *testing.T) {
	p := pack{stickers: []string{"/p/abc123.webp", "/p/def.webp"}}
	if !packHolds(&p, "abc123") || packHolds(&p, "abc") || packHolds(&p, "") {
		t.Error("packHolds")
	}
}

// PK-008: an animated sticker plays only while the pointer is on it; the
// others hold their first frame.
func TestAnimatedStickersPlayOnlyWhileHovered(t *testing.T) {
	r := newRig(t)
	if r.kit.k.Images() == nil {
		t.Skip("no renderer to draw pictures with")
	}
	moving := animatedFile(t, "moving.gif")
	still := stickerFiles(t, "still.png")[0]
	r.open(state.PickerStickers)
	r.emit(stickersEvent(shelves{recent: []string{moving, still}}))
	animation := func() img.Animation {
		tile := r.Must(ui.RoleButton, "Sticker")
		side := int32(tile.Bounds[2]-tile.Bounds[0]) - 8
		h, _ := r.kit.k.Images().Request(img.Key{Hash: img.HashString(moving) ^ r.a.Serial*0x9e3779b97f4a7c15, W: side, H: side, Flags: img.Animate}, img.File(moving))
		return r.kit.k.Images().Animation(h)
	}
	var an img.Animation
	for i := 0; i < 400 && !an.Valid(); i++ {
		time.Sleep(5 * time.Millisecond)
		r.Frames(1)
		an = animation()
	}
	if !an.Valid() || an.Frames() != 3 {
		t.Fatal("the animated sticker was not decoded as an animation")
	}
	if an.Stilled() {
		t.Skip("the system holds animations still")
	}
	r.Play(widgettest.Move(ui.RoleButton, "Sticker").Nth(1), widgettest.Pause(500))
	if f := animation().Frame(); f != 0 {
		t.Errorf("an unhovered sticker is on frame %d", f)
	}
	r.Play(widgettest.Move(ui.RoleButton, "Sticker").Nth(0), widgettest.Pause(450))
	if f := animation().Frame(); f == 0 {
		t.Error("a hovered sticker does not play")
	}
	r.Play(widgettest.Move(ui.RoleButton, "Sticker").Nth(1), widgettest.Pause(100))
	held := animation().Frame()
	r.Play(widgettest.Pause(500))
	if f := animation().Frame(); f != held {
		t.Errorf("the sticker kept playing once the pointer left: frame %d, then %d", held, f)
	}
}

// The shelf tabs and the Find packs button say what they are on hover, as
// upstream's do.
func TestTooltips(t *testing.T) {
	r := newRig(t)
	r.open(state.PickerStickers)
	r.emit(stickersEvent(shelves{packs: []pack{{name: "Bom dia", dir: "/p/bom", local: true}}}))
	r.Play(widgettest.Move(ui.RoleTab, "Favorites"), widgettest.Pause(1500))
	if _, ok := r.Find(ui.RoleTooltip, "Favorites"); !ok {
		t.Errorf("no tooltip on a shelf tab:\n%s", widgettest.Dump(r.Tree()))
	}
	r.Click(ui.RoleTab, "Add stickers")
	r.Play(widgettest.Move(ui.RoleButton, "Find packs"), widgettest.Pause(1500))
	if _, ok := r.Find(ui.RoleTooltip, "Browse signalstickers.org"); !ok {
		t.Errorf("no tooltip on Find packs:\n%s", widgettest.Dump(r.Tree()))
	}
	r.open(state.PickerEmoji)
	r.Play(widgettest.Move(ui.RoleTab, "Flags"), widgettest.Pause(1500))
	if _, ok := r.Find(ui.RoleTooltip, "Flags"); !ok {
		t.Errorf("no tooltip on a category tab:\n%s", widgettest.Dump(r.Tree()))
	}
}
