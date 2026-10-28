package picker

import (
	"testing"

	"gonativeui/ui"

	"zapfast/abi"
	"zapfast/core/fake"
	"zapfast/state"
)

func linkEvent(status abi.LinkStatusTag) *abi.Event {
	return ev(abi.EventTagLink, func(e *abi.Event) { e.Link.Status.Tag = status })
}

// Unlink must discard the picker immediately, even without a linked-window
// Build. Relinking the same slot must not expose any of the old shelves or GIFs.
func TestPickerUnlinkRelinkSameSlot(t *testing.T) {
	r := newRig(t)
	r.s.Settings.GiphyKey = []byte("test-key")
	paths := files(t, 1)
	r.open(state.PickerGifs)
	r.Type("old search")
	r.Key(ui.KeyEnter, 0)
	r.emit(gifsEvent("old search", "old-gif"))
	r.open(state.PickerStickers)
	r.emit(stickersEvent(shelves{recent: paths, favorites: paths, received: paths,
		packs: []pack{{name: "Old pack", dir: "/old", stickers: paths}},
		tags:  map[string][]string{paths[0]: {"😀"}}}))
	r.pk.st.search.SetString("old")
	r.Frames(2) // populate the cached search results
	r.pk.st.link.SetString("old import")
	r.pk.st.packName.SetString("old name")
	r.pk.st.created = "old name"
	r.pk.gif.key.SetString("old key draft")
	serial, index := r.a.Serial, r.a.Index
	if err := r.f.Emit(linkEvent(abi.LinkStatusTagLoggedOut)); err != nil {
		t.Fatal(err)
	}
	r.s.Pump(0) // no Build: the unlink hook alone must forget the old session
	if r.a.Serial == serial || r.a.Index != index {
		t.Fatal("fixture did not rotate the serial in the same slot")
	}
	if len(r.pk.gif.results) != 0 || r.pk.gif.query.Len() != 0 || r.pk.gif.sent != "" || r.pk.gif.key.Len() != 0 {
		t.Error("unlink retained GIF results, search or key draft")
	}
	l := &r.pk.st.lib
	if len(l.packs)+len(l.recent)+len(l.favorites)+len(l.received)+len(l.emojis)+len(r.pk.st.found) != 0 ||
		r.pk.st.search.Len()+r.pk.st.link.Len()+r.pk.st.packName.Len() != 0 || r.pk.st.created != "" {
		t.Error("unlink retained sticker library, cached search or import draft")
	}
	r.emit(linkEvent(abi.LinkStatusTagConnected))
	r.open(state.PickerStickers)
	if r.hasTab("Old pack") || r.tiles() != 0 {
		t.Error("the relinked slot shows the old sticker library")
	}
	r.Click(ui.RoleTab, "Favorites")
	if r.tiles() != 0 {
		t.Error("the relinked slot shows old favorites")
	}
	r.Click(ui.RoleTab, "Received")
	if r.tiles() != 0 {
		t.Error("the relinked slot shows old received stickers")
	}
	r.open(state.PickerGifs)
	if _, ok := r.Find(ui.RoleButton, "old-gif"); ok {
		t.Error("the relinked slot shows old GIFs")
	}
	asks := r.sent(abi.CommandTagSearchGifs)
	if len(asks) != 3 || len(asks[2].SearchGifs.Query) != 0 {
		t.Error("relink did not request fresh trending GIFs")
	}
	r.emit(gifsEvent("", "new-gif"))
	if _, ok := r.Find(ui.RoleButton, "new-gif"); !ok {
		t.Error("the relinked slot did not accept its GIFs")
	}
}

// The new account's events may arrive before its first Build. Binding only
// in Build would erase those results again, leaving the picker empty.
func TestPickerSwitchBindsBeforeEvents(t *testing.T) {
	r := newRig(t)
	r.s.Settings.GiphyKey = []byte("test-key")
	r.open(state.PickerStickers)
	r.emit(stickersEvent(shelves{packs: []pack{{name: "Old pack", dir: "/old"}}}))
	f := fake.New(nil)
	a := r.s.AddAccount("2", nil, f)
	r.s.SwitchAccount(a.Index)
	if err := f.Emit(linkEvent(abi.LinkStatusTagConnected), gifsEvent("", "new-gif"), stickersEvent(shelves{packs: []pack{{name: "New pack", dir: "/new"}}})); err != nil {
		t.Fatal(err)
	}
	r.s.Pump(0)
	r.open(state.PickerGifs)
	if _, ok := r.Find(ui.RoleButton, "new-gif"); !ok {
		t.Error("switch lost the new account's GIFs")
	}
	r.open(state.PickerStickers)
	if !r.hasTab("New pack") || r.hasTab("Old pack") {
		t.Error("switch lost the new account's packs or kept the old ones")
	}
	// A hidden account's unlink cannot erase the visible account's library.
	r.emit(linkEvent(abi.LinkStatusTagLoggedOut))
	if !r.hasTab("New pack") {
		t.Error("hidden unlink cleared the active picker")
	}
}

func TestPickerRemovalReusesIndex(t *testing.T) {
	r := newRig(t)
	f := fake.New(nil)
	a := r.s.AddAccount("2", nil, f)
	if err := f.Emit(linkEvent(abi.LinkStatusTagConnected)); err != nil {
		t.Fatal(err)
	}
	r.open(state.PickerStickers)
	r.emit(stickersEvent(shelves{packs: []pack{{name: "Removed pack", dir: "/removed"}}}))
	oldIndex := r.a.Index
	r.s.RemoveAccount(oldIndex)
	r.emit(ev(abi.EventTagAccountRemoved, func(*abi.Event) {}))
	if r.s.Acct() != a || a.Index != oldIndex {
		t.Fatal("fixture did not replace the active slot")
	}
	r.open(state.PickerStickers)
	if r.hasTab("Removed pack") {
		t.Error("removal exposed the previous occupant's packs")
	}
	if err := f.Emit(stickersEvent(shelves{packs: []pack{{name: "Survivor pack", dir: "/survivor"}}})); err != nil {
		t.Fatal(err)
	}
	r.Frames(3)
	if !r.hasTab("Survivor pack") {
		t.Error("the surviving account's packs did not show")
	}
}

func TestPickerReindexKeepsTheSameSessionsPacks(t *testing.T) {
	r := newRig(t)
	f := fake.New(nil)
	a := r.s.AddAccount("2", nil, f)
	r.s.SwitchAccount(a.Index)
	if err := f.Emit(linkEvent(abi.LinkStatusTagConnected)); err != nil {
		t.Fatal(err)
	}
	r.open(state.PickerStickers)
	if err := f.Emit(stickersEvent(shelves{packs: []pack{{name: "Survivor pack", dir: "/survivor"}}})); err != nil {
		t.Fatal(err)
	}
	r.Frames(3)
	r.s.RemoveAccount(0)
	r.emit(ev(abi.EventTagAccountRemoved, func(*abi.Event) {}))
	if r.s.Acct() != a || a.Index != 0 {
		t.Fatal("fixture did not reindex the active account")
	}
	if !r.hasTab("Survivor pack") {
		t.Error("reindexing discarded the same session's packs")
	}
}

// Even a reused file path must request a fresh image for a new session.
func TestPickerImagesAreSessionScoped(t *testing.T) {
	r := newRig(t)
	ic := r.kit.k.Images()
	if ic == nil {
		t.Skip("no renderer to draw pictures with")
	}
	paths := files(t, 1)
	r.open(state.PickerStickers)
	r.emit(stickersEvent(shelves{recent: paths}))
	before := ic.Stats().Entries
	r.emit(linkEvent(abi.LinkStatusTagLoggedOut), linkEvent(abi.LinkStatusTagConnected))
	r.open(state.PickerStickers)
	r.emit(stickersEvent(shelves{recent: paths}))
	if after := ic.Stats().Entries; after != before+1 {
		t.Errorf("new session reused the old image: entries %d -> %d", before, after)
	}
	if got := r.allocs(60); got != 0 {
		t.Errorf("relinked picker: %d allocations in 60 steady frames", got)
	}
}
