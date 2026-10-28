package picker

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"strings"

	"gonativeui/icon"
	"gonativeui/rexui"
	"gonativeui/theme"
	"gonativeui/ui"
	"gonativeui/widget"
	"gonativeui/widget/base"

	"zapfast/abi"
	"zapfast/i18n"
	"zapfast/state"
	"zapfast/ui/screen"
)

// The sticker tab (zf:src/ui/picker.rs:1088): the shelves (Recent,
// Favorites, Received, each pack, and the page for adding), search, the
// grid, the right-click menu and the page behind the plus.

type shelfKind uint8

const (
	shelfRecent shelfKind = iota
	shelfFavorites
	shelfReceived
	shelfPack
	shelfAdd
)

const (
	stickerColumns = 5
	shelfTabPt     = 34
)

type stickerState struct {
	lib     library
	batch   uint32
	loading bool // chunks of a batch are arriving
	pending bool // asked for, nothing yet (zf: stickers_pending)

	shelf    shelfKind
	shelfDir string // the pack, for shelfPack

	search   ui.TextBuffer
	cmd      fieldCmd
	found    []string
	foundFor string
	foundGen int
	gen      int

	link      ui.TextBuffer
	linkCmd   fieldCmd
	packName  ui.TextBuffer
	nameCmd   fieldCmd
	importing bool
	created   string // the name of a pack just asked for, to select once listed
	scroll    base.ScrollState
	addScroll base.ScrollState
}

func (st *stickerState) init() {
	st.search.Max, st.link.Max, st.packName.Max = 256, 4096, 256
	st.search.Reserve(256)
	st.link.Reserve(512)
	st.packName.Reserve(256)
	st.lib.emojis = map[string][]string{}
}

func (st *stickerState) reset() {
	st.search.Reset()
}

// apply takes a Stickers event (zf:src/app.rs, Event::Stickers): rows in
// order, favorites, then each pack with its stickers, then recent, received
// and the emoji tags; a row belongs to the pack opened by the rows before it.
func (st *stickerState) apply(e *abi.EventStickers) {
	if !st.loading || e.Batch != st.batch {
		st.batch, st.loading = e.Batch, true
		l := &st.lib
		l.favorites, l.recent, l.received, l.packs = nil, nil, nil, nil
		clear(l.emojis)
	}
	l := &st.lib
	for i := range e.Entries {
		en := &e.Entries[i]
		switch en.Tag {
		case abi.StickerEntryTagFavorite:
			l.favorites = append(l.favorites, string(en.Favorite.Path))
		case abi.StickerEntryTagPack:
			l.packs = append(l.packs, pack{name: string(en.Pack.Name), dir: string(en.Pack.Dir), local: en.Pack.Local})
		case abi.StickerEntryTagPackSticker:
			if n := len(l.packs); n > 0 {
				l.packs[n-1].stickers = append(l.packs[n-1].stickers, string(en.PackSticker.Path))
			}
		case abi.StickerEntryTagRecent:
			l.recent = append(l.recent, string(en.Recent.Path))
		case abi.StickerEntryTagReceived:
			l.received = append(l.received, string(en.Received.Path))
		case abi.StickerEntryTagTagged:
			tags := make([]string, len(en.Tagged.Emojis))
			for j := range tags {
				tags[j] = string(en.Tagged.Emojis[j])
			}
			l.emojis[string(en.Tagged.Path)] = tags
		}
	}
	st.gen++
	if !e.Last {
		return
	}
	st.loading, st.pending, st.importing = false, false, false
	// a pack made here shows as soon as it is listed (packs list newest first)
	if st.created != "" {
		for i := range l.packs {
			if l.packs[i].local && l.packs[i].name == st.created {
				st.shelf, st.shelfDir = shelfPack, l.packs[i].dir
				st.created = ""
				break
			}
		}
	}
	// a pack deleted elsewhere cannot stay selected
	if st.shelf == shelfPack && st.selectedPack() == nil {
		st.shelf, st.shelfDir = shelfRecent, ""
	}
}

// selectedPack is the pack the tab shows, when it still exists.
func (st *stickerState) selectedPack() *pack {
	if st.shelf != shelfPack {
		return nil
	}
	for i := range st.lib.packs {
		if st.lib.packs[i].dir == st.shelfDir {
			return &st.lib.packs[i]
		}
	}
	return nil
}

// opened is the tab opening (Action::TogglePicker for stickers).
func (s *Screen) stickersOpened(e *screen.Env) {
	st := &s.st
	st.reset()
	l := &st.lib
	st.pending = len(l.recent) == 0 && len(l.favorites) == 0 && len(l.packs) == 0
	if a := e.Acct(); a != nil {
		a.Send(a.Cmd(abi.CommandTagRecentStickers))
	}
}

func (s *Screen) setShelf(kind shelfKind, dir string) {
	st := &s.st
	st.shelf, st.shelfDir = kind, dir
	st.search.Reset()
}

// stickerBody builds the sticker tab.
func (s *Screen) stickerBody(e *screen.Env) {
	k := e.K
	c := k.Context()
	st := &s.st

	st.cmd = fieldCmd{}
	c.NextFieldCommander(&st.cmd, "")
	search := i18n.T("Search by emoji, word, or pack")
	rexui.SearchField(k.Kit, search, &st.search, search, rexui.FieldOpts{})
	if st.cmd.esc {
		s.wantClose = true
	}
	s.shelfStrip(e)
	query := strings.TrimSpace(string(st.search.Bytes()))
	if query == "" && st.shelf == shelfAdd {
		s.addPage(e)
		return
	}
	if query != "" {
		if st.foundFor != query || st.foundGen != st.gen {
			st.found, st.foundFor, st.foundGen = st.lib.search(query), query, st.gen
		}
		if len(st.found) == 0 {
			s.shelfHint(e, i18n.T("No stickers match your search."))
			return
		}
		s.stickerGrid(e, st.found, shelfPack)
		return
	}
	l := &st.lib
	switch st.shelf {
	case shelfRecent:
		if len(l.recent) == 0 {
			if st.pending {
				s.shelfHint(e, i18n.T("Loading your stickers…"))
			} else {
				s.shelfHint(e, i18n.T("Stickers you send appear here."))
			}
			return
		}
		s.stickerGrid(e, l.recent, shelfRecent)
	case shelfFavorites:
		if len(l.favorites) == 0 {
			s.shelfHint(e, i18n.T("Right-click any sticker to add it to your favorites. They stay in sync with your phone."))
			return
		}
		s.stickerGrid(e, l.favorites, shelfFavorites)
	case shelfReceived:
		if len(l.received) == 0 {
			s.shelfHint(e, i18n.T("Stickers people send you appear here."))
			return
		}
		s.stickerGrid(e, l.received, shelfReceived)
	case shelfPack:
		p := st.selectedPack()
		if p == nil {
			return
		}
		s.packHeader(e, p)
		if len(p.stickers) == 0 {
			s.shelfHint(e, i18n.T("Nothing here yet. Right-click any sticker to add it to this pack."))
			return
		}
		s.stickerGrid(e, p.stickers, shelfPack)
	}
}

// shelfHint is a short centred note in place of an empty grid.
func (s *Screen) shelfHint(e *screen.Env, text string) {
	k := e.K
	c := k.Context()
	c.NextSize(ui.Pct(1), ui.Children(1))
	c.NextAxis(ui.AxisY)
	c.NextCross(ui.CrossCenter)
	c.NextPadding(e.Pt(8), e.Pt(24), e.Pt(8), 0)
	c.Push(c.Box(0, "hint"))
	k.NextAlign(ui.AlignCenter)
	rexui.Label(k.Kit, text, rexui.LabelOpts{Wrap: true, Color: rexui.Of(theme.Label2)})
	c.Pop()
}

// shelfStrip is the row of tabs under the search field; packs show their
// first sticker.
func (s *Screen) shelfStrip(e *screen.Env) {
	k := e.K
	c := k.Context()
	st := &s.st
	side := e.Pt(shelfTabPt)
	c.NextSize(ui.Pct(1), ui.Px(side))
	c.NextAxis(ui.AxisX)
	c.NextGap(e.Pt(2))
	c.NextCross(ui.CrossCenter)
	c.Push(c.Box(ui.ScrollX|ui.Clip, "shelves"))
	if s.shelfTab(e, "Recent", s.ic.clock, "", st.shelf == shelfRecent, side) {
		s.setShelf(shelfRecent, "")
	}
	if s.shelfTab(e, "Favorites", s.ic.star, "", st.shelf == shelfFavorites, side) {
		s.setShelf(shelfFavorites, "")
	}
	if s.shelfTab(e, "Received", s.ic.bubble, "", st.shelf == shelfReceived, side) {
		s.setShelf(shelfReceived, "")
	}
	for i := range st.lib.packs {
		p := &st.lib.packs[i]
		cover := ""
		if len(p.stickers) > 0 {
			cover = p.stickers[0]
		}
		name := p.name
		if cover == "" {
			if s.shelfTab(e, s.idLabel(e, name, p.dir), s.ic.sticker, "", st.shelf == shelfPack && st.shelfDir == p.dir, side) {
				s.setShelf(shelfPack, p.dir)
			}
			continue
		}
		if s.shelfTab(e, s.idLabel(e, name, p.dir), 0, cover, st.shelf == shelfPack && st.shelfDir == p.dir, side) {
			s.setShelf(shelfPack, p.dir)
		}
	}
	if s.shelfTab(e, "Add stickers", s.ic.plus, "", st.shelf == shelfAdd, side) {
		s.setShelf(shelfAdd, "")
	}
	c.Pop()
}

// shelfTab is one tab of the strip: an icon, or a sticker. label is its
// name ("Name###id" for a pack). It reports a click.
func (s *Screen) shelfTab(e *screen.Env, label string, ic icon.ID, cover string, on bool, side float32) bool {
	k := e.K
	c, r := k.Context(), k.Theme()
	name, _ := ui.SplitLabel(label)
	if ic != 0 && cover == "" {
		name = i18n.T(name)
	}
	p := k.PressState(label, base.HoverOn)
	flags := ui.Flags(0)
	switch {
	case on:
		c.NextBG(theme.WithAlpha(r.Colors[theme.Accent], 0.22))
		flags = ui.DrawBackground
	case p.Hot > 0.004:
		c.NextBG(theme.WithAlpha(r.Colors[theme.Fill2], p.Hot))
		flags = ui.DrawBackground
	}
	c.NextRadius(e.Pt(6))
	c.NextAxis(ui.AxisY)
	c.NextCross(ui.CrossCenter)
	c.NextJustify(ui.JustifyCenter)
	if on {
		c.NextSemState(ui.SemSelected)
	}
	p.Name = name
	k.BeginPress(&p, ui.RoleTab, ui.Px(side), ui.Px(side), flags, nil)
	if cover != "" {
		s.picture(e, 2, cover, side-e.Pt(10), side-e.Pt(10), ui.FitContain, 0, false, false)
	} else if ic != 0 {
		col := r.Colors[theme.Label2]
		if on {
			col = r.Colors[theme.Label]
		}
		if f, disp := k.Icons().Prepare(c, ic, col); f != 0 {
			c.BoxKeyed(f, ui.KeyN(p.Key, 1), disp)
		}
	}
	if on {
		theme.At(c, e.Pt(6), side-e.Pt(2), side-e.Pt(12), e.Pt(2))
		c.NextBG(r.Colors[theme.Accent])
		c.Box(base.Floating|ui.DrawBackground, "underline")
	}
	sig := k.EndPress(&p)
	rexui.Tooltip(k.Kit, sig.ID, name)
	return sig.Clicked
}

// packHeader is the selected pack's name with its remove and send buttons.
func (s *Screen) packHeader(e *screen.Env, p *pack) {
	k := e.K
	c := k.Context()
	c.NextSize(ui.Pct(1), ui.Children(1))
	c.NextAxis(ui.AxisX)
	c.NextCross(ui.CrossCenter)
	c.NextGap(e.Pt(4))
	c.Push(c.Box(0, "pack header"))
	k.NextWidth(widget.Fill)
	rexui.LabelBytes(k.Kit, c.Fmt().Str(p.name).Bytes(), rexui.LabelOpts{Style: rexui.TextSemibold})
	if len(p.stickers) > 0 {
		if rexui.IconButton(k.Kit, s.ic.send, i18n.T("Send this pack to the chat"), rexui.IconButtonOpts{Variant: rexui.IconBorderless}).Clicked {
			s.sharePack(e, p.dir)
		}
	}
	if rexui.IconButton(k.Kit, s.ic.trash, i18n.T("Remove this pack"), rexui.IconButtonOpts{Variant: rexui.IconBorderless}).Clicked {
		e.S.OpenDialog(state.Dialog{Kind: state.DialogConfirmRemoveStickerPack, Text: []byte(p.name), Path: []byte(p.dir)})
	}
	c.Pop()
}

// stickerGrid is a grid of sticker tiles, five to a row: a click sends, a
// right click opens the native menu.
func (s *Screen) stickerGrid(e *screen.Env, items []string, kind shelfKind) {
	k := e.K
	c := k.Context()
	st := &s.st
	gap := e.Pt(6)
	v := k.BeginScroll("stickers", &st.scroll, base.ScrollVertical)
	cell := theme.Snap((s.viewWidth(e, &v) - gap*(stickerColumns-1)) / stickerColumns)
	rowPx := cell + gap
	rows := (len(items) + stickerColumns - 1) / stickerColumns
	first, last := c.ListRange(v.Inner, rows, rowPx, 1)
	for i := first; i < last; i++ {
		c.NextSize(ui.Pct(1), ui.Px(rowPx))
		c.NextAxis(ui.AxisX)
		c.NextGap(gap)
		c.NextCross(ui.CrossStart)
		c.Push(c.BoxKeyed(0, ui.KeyN(c.ParentKey(), uint64(i)+1), nil))
		for j := i * stickerColumns; j < min((i+1)*stickerColumns, len(items)); j++ {
			path := items[j]
			id, click := s.stickerTile(e, path, cell)
			if click {
				s.sendSticker(e, path)
			}
			if item, ok := rexui.OnContextMenu(k.Kit, id, func(m *[]ui.MenuItem) { s.stickerMenuItems(path, kind, m) }); ok {
				s.stickerMenuChosen(e, path, kind, item)
			}
		}
		c.Pop()
	}
	k.CloseScrollContent(&v)
	rexui.ScrollBars(k.Kit, &v)
	k.CloseScroll(&v)
}

// stickerTile is one sticker: still until the pointer is on it. It reports
// its box and whether it was clicked.
func (s *Screen) stickerTile(e *screen.Env, path string, side float32) (id ui.ID, click bool) {
	k := e.K
	c, r := k.Context(), k.Theme()
	key := ui.HashString(c.ParentKey(), path)
	hot := c.HotTOf(key)
	hovering := c.PeekSignal(key).Hovering
	flags := ui.Clickable | ui.Hoverable | ui.HotAnimation
	if hot > 0.004 {
		c.NextBG(theme.WithAlpha(r.Colors[theme.Fill2], hot))
		flags |= ui.DrawBackground
	}
	c.NextSize(ui.Px(side), ui.Px(side))
	c.NextRadius(e.Pt(8))
	c.NextPadding(e.Pt(4), e.Pt(4), e.Pt(4), e.Pt(4))
	c.NextRole(ui.RoleButton)
	c.NextSemName(i18n.T("Sticker"))
	id = c.BoxKeyed(flags, key, nil)
	c.Push(id)
	s.picture(e, 3, path, side-e.Pt(8), side-e.Pt(8), ui.FitContain, 0, hovering && !k.Inactive(), true)
	c.Pop()
	return id, c.Signal(id).Clicked
}

// The ids of a sticker menu's items: the favorite and recents entries, then
// the local packs from menuPack on (their index in the library).
const (
	menuFavorite = iota + 1
	menuUnrecent
	menuPack = 100
)

// stickerMenuItems fills the right-click menu of a sticker, as upstream's
// does: add to or remove from favorites, remove from recents (Recent only),
// then the local packs, checked where they hold the sticker.
func (s *Screen) stickerMenuItems(path string, kind shelfKind, m *[]ui.MenuItem) {
	if kind == shelfFavorites {
		*m = append(*m, ui.MenuItem{ID: menuFavorite, Title: i18n.T("Remove from favorites"), Symbol: "star.slash"})
	} else {
		*m = append(*m, ui.MenuItem{ID: menuFavorite, Title: i18n.T("Add to favorites"), Symbol: "star"})
	}
	if kind == shelfRecent {
		*m = append(*m, ui.MenuItem{ID: menuUnrecent, Title: i18n.T("Remove from recents"), Symbol: "xmark"})
	}
	hash, sep := "", false
	for i := range s.st.lib.packs {
		p := &s.st.lib.packs[i]
		if !p.local {
			continue
		}
		if !sep {
			*m = append(*m, ui.MenuSeparator)
			hash, sep = contentHash(path), true
		}
		mark := ui.MarkNone
		if packHolds(p, hash) {
			mark = ui.MarkOn
		}
		*m = append(*m, ui.MenuItem{ID: menuPack + i, Title: p.name, Mark: mark})
	}
}

// stickerMenuChosen does what the person chose in a sticker's menu.
func (s *Screen) stickerMenuChosen(e *screen.Env, path string, kind shelfKind, item int) {
	st := &s.st
	a := e.Acct()
	if a == nil {
		return
	}
	switch {
	case item == menuFavorite && kind == shelfFavorites:
		s.cmdPath(a, abi.CommandTagForgetSticker, path)
	case item == menuFavorite:
		s.cmdPath(a, abi.CommandTagSaveSticker, path)
		e.S.Toast([]byte(i18n.T("Added to favorites")), false)
	case item == menuUnrecent:
		l := &st.lib
		for i, p := range l.recent {
			if p == path {
				l.recent = append(l.recent[:i:i], l.recent[i+1:]...)
				break
			}
		}
		st.gen++
		s.cmdPath(a, abi.CommandTagRemoveRecentSticker, path)
	case item >= menuPack && item-menuPack < len(st.lib.packs):
		p := &st.lib.packs[item-menuPack]
		member := packHolds(p, contentHash(path))
		cmd := a.Cmd(abi.CommandTagSetStickerPack)
		cmd.SetStickerPack.Pack, cmd.SetStickerPack.Sticker, cmd.SetStickerPack.Member = []byte(p.dir), []byte(path), !member
		a.Send(cmd)
	}
}

// contentHash is the sticker file's content hash, which names it inside the
// local packs ("" when it cannot be read). It is read when a menu opens and
// when a pack is chosen from it, as upstream does (a sticker is a few KB).
func contentHash(path string) string {
	b, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	h := sha256.Sum256(b)
	return hex.EncodeToString(h[:])
}

// packHolds reports whether a local pack holds the sticker with this content
// hash: its files are named by it.
func packHolds(p *pack, hash string) bool {
	if hash == "" {
		return false
	}
	for _, f := range p.stickers {
		base := filepath.Base(f)
		if strings.TrimSuffix(base, filepath.Ext(base)) == hash {
			return true
		}
	}
	return false
}

func (s *Screen) cmdPath(a *state.Account, tag abi.CommandTag, path string) {
	cmd := a.Cmd(tag)
	switch tag {
	case abi.CommandTagSaveSticker:
		cmd.SaveSticker.Path = []byte(path)
	case abi.CommandTagForgetSticker:
		cmd.ForgetSticker.Path = []byte(path)
	case abi.CommandTagRemoveRecentSticker:
		cmd.RemoveRecentSticker.Path = []byte(path)
	}
	a.Send(cmd)
}

// sendSticker sends a sticker to the open chat (Action::SendSticker).
func (s *Screen) sendSticker(e *screen.Env, path string) {
	a := e.Acct()
	chat, ok := a.OpenChatView()
	if !ok {
		return
	}
	cmd := a.Cmd(abi.CommandTagSendSticker)
	cmd.SendSticker.Chat, cmd.SendSticker.Path = chat.ID(), []byte(path)
	s.quote(e, &cmd.SendSticker.Quoting, &cmd.SendSticker.HasQuoting)
	a.Send(cmd)
	s.afterSend(e)
}

// sharePack sends a pack to the open chat (Action::ShareStickerPack).
func (s *Screen) sharePack(e *screen.Env, dir string) {
	a := e.Acct()
	chat, ok := a.OpenChatView()
	if !ok {
		return
	}
	s.closePicker(e, false)
	e.S.Toast([]byte(i18n.T("Sending the sticker pack…")), false)
	cmd := a.Cmd(abi.CommandTagSendStickerPack)
	cmd.SendStickerPack.Chat, cmd.SendStickerPack.Dir = chat.ID(), []byte(dir)
	a.Send(cmd)
	e.S.Req.JumpToBottom = true
}

// addPage is the page behind the plus: import packs, start a pack, or make a
// sticker from a picture.
func (s *Screen) addPage(e *screen.Env) {
	k := e.K
	c, r := k.Context(), k.Theme()
	st := &s.st
	v := k.BeginScroll("add stickers", &st.addScroll, base.ScrollVertical)
	c.NextSize(ui.Pct(1), ui.Children(1))
	c.NextAxis(ui.AxisY)
	c.NextGap(e.Pt(4))
	c.NextPadding(0, e.Pt(4), e.Pt(scrollRoom), 0)
	c.Push(c.Box(0, "add page"))
	rexui.Label(k.Kit, i18n.T("Import a pack"), rexui.LabelOpts{Style: rexui.TextSemibold})
	rexui.Label(k.Kit, i18n.T("Paste a signal.art link from signalstickers.org, or open a .wastickers file."),
		rexui.LabelOpts{Style: rexui.TextCallout, Wrap: true, Color: rexui.Of(theme.Label2)})

	// the link and where it comes from
	c.NextSize(ui.Pct(1), ui.Children(1))
	c.NextAxis(ui.AxisX)
	c.NextGap(e.Pt(6))
	c.NextCross(ui.CrossCenter)
	c.Push(c.Box(0, "import row"))
	st.linkCmd = fieldCmd{}
	c.NextFieldCommander(&st.linkCmd, "")
	k.NextWidth(widget.Fill)
	link := i18n.T("Paste a signal.art link")
	lf := rexui.TextField(k.Kit, link, &st.link, link, rexui.FieldOpts{})
	text := strings.TrimSpace(st.link.String())
	if (lf.Changed && looksLikeSignalURL(text)) || (st.linkCmd.enter && text != "") {
		s.importURL(e, text)
	}
	find := rexui.ButtonIcon(k.Kit, s.ic.link, i18n.T("Find packs"), rexui.ButtonOpts{})
	rexui.Tooltip(k.Kit, find.ID, i18n.T("Browse signalstickers.org"))
	if find.Clicked {
		s.openURL(e, "https://signalstickers.org/")
	}
	if rexui.ButtonIcon(k.Kit, s.ic.doc, i18n.T("Open file"), rexui.ButtonOpts{}).Clicked {
		st.importing = true
		if a := e.Acct(); a != nil {
			a.Send(a.Cmd(abi.CommandTagPickStickerArchive))
		}
	}
	c.Pop()
	if st.importing {
		c.NextAxis(ui.AxisX)
		c.NextGap(e.Pt(6))
		c.NextCross(ui.CrossCenter)
		c.NextSize(ui.Pct(1), ui.Children(1))
		c.Push(c.Box(0, "importing"))
		rexui.Spinner(k.Kit, i18n.T("Importing"), rexui.SpinnerOpts{Size: theme.SizeSmall})
		rexui.Label(k.Kit, i18n.T("Importing the pack…"), rexui.LabelOpts{Style: rexui.TextCallout, Color: rexui.Of(theme.Label2)})
		c.Pop()
	}

	c.NextSize(ui.Pct(1), ui.Px(e.Pt(10)))
	c.Box(0, "gap")
	rexui.Label(k.Kit, i18n.T("Make your own"), rexui.LabelOpts{Style: rexui.TextSemibold})
	rexui.Label(k.Kit, i18n.T("Start a pack, then right-click any sticker to add it. Or turn a picture into a sticker."),
		rexui.LabelOpts{Style: rexui.TextCallout, Wrap: true, Color: rexui.Of(theme.Label2)})
	c.NextSize(ui.Pct(1), ui.Children(1))
	c.NextAxis(ui.AxisX)
	c.NextGap(e.Pt(6))
	c.NextCross(ui.CrossCenter)
	c.Push(c.Box(0, "pack row"))
	st.nameCmd = fieldCmd{}
	c.NextFieldCommander(&st.nameCmd, "")
	k.NextWidth(widget.Fill)
	newPack := i18n.T("New pack…")
	nf := rexui.TextField(k.Kit, newPack, &st.packName, newPack, rexui.FieldOpts{})
	create := st.nameCmd.enter
	if rexui.ButtonIcon(k.Kit, s.ic.plus, i18n.T("Create pack"), rexui.ButtonOpts{}).Clicked {
		create = true
	}
	_ = nf
	c.Pop()
	if name := strings.TrimSpace(st.packName.String()); create && name != "" {
		st.created = name
		if a := e.Acct(); a != nil {
			cmd := a.Cmd(abi.CommandTagCreateStickerPack)
			cmd.CreateStickerPack.Name = []byte(name)
			a.Send(cmd)
		}
		st.packName.Reset()
	}
	c.NextSize(ui.Pct(1), ui.Px(e.Pt(2)))
	c.Box(0, "gap 2")
	k.NextAlign(ui.AlignLeft)
	if rexui.ButtonIcon(k.Kit, s.ic.photo, i18n.T("Make a sticker from a picture…"), rexui.ButtonOpts{}).Clicked {
		e.S.OpenDialog(state.Dialog{Kind: state.DialogStickerMaker})
	}
	c.Pop()
	k.CloseScrollContent(&v)
	rexui.ScrollBars(k.Kit, &v)
	k.CloseScroll(&v)
	_ = r
}

// importURL asks the core to import the pack a link names.
func (s *Screen) importURL(e *screen.Env, url string) {
	st := &s.st
	st.importing = true
	st.link.Reset()
	if a := e.Acct(); a != nil {
		cmd := a.Cmd(abi.CommandTagImportStickerUrl)
		cmd.ImportStickerUrl.URL = []byte(url)
		a.Send(cmd)
	}
}

// looksLikeSignalURL reports whether text holds a complete signal.art pack
// link: a pack_id of 32 hex digits and a pack_key of 64
// (zf:src/backend/sticker_import.rs:9 parse_signal_url).
func looksLikeSignalURL(text string) bool {
	tail := text
	if i := strings.LastIndexByte(text, '#'); i >= 0 {
		tail = text[i+1:]
	}
	var id, key string
	for _, part := range strings.FieldsFunc(tail, func(r rune) bool { return r == '&' || r == '?' || r == ';' }) {
		if v, ok := strings.CutPrefix(part, "pack_id="); ok {
			id = strings.ToLower(strings.TrimSpace(v))
		}
		if v, ok := strings.CutPrefix(part, "pack_key="); ok {
			key = strings.ToLower(strings.TrimSpace(v))
		}
	}
	return len(id) == 32 && isHex(id) && len(key) == 64 && isHex(key)
}

func isHex(s string) bool {
	for i := 0; i < len(s); i++ {
		c := s[i]
		if !(c >= '0' && c <= '9' || c >= 'a' && c <= 'f') {
			return false
		}
	}
	return true
}

// idLabel is "name###id": a label shown as name and identified by id, built
// in the frame's arena.
func (s *Screen) idLabel(e *screen.Env, name, id string) string {
	return e.C().Fmt().Str(name).Str("###").Str(id).String()
}
