package chatlist

import (
	"gonativeui/icon"
	"gonativeui/rexui"
	"gonativeui/theme"
	"gonativeui/ui"
	"gonativeui/widget/base"

	"zapfast/i18n"
	"zapfast/model"
	"zapfast/state"
	ztheme "zapfast/theme"
	"zapfast/ui/screen"
)

// Folders in the list (state/folders.go): a row with its icon and name that
// opens and closes over its chats, a menu to rename it, change its icon,
// make it a space or delete it, and in each chat's menu a Folder submenu to
// move the chat. A new folder starts renaming at once.

// folderRowPt is a folder's row: quieter than a chat's, as a section is.
const folderRowPt = 36

// folderUI is the list's folder state: the icons, and the row being renamed.
type folderUI struct {
	icons    []icon.ID // FolderIcons' symbols, in order
	open     icon.ID   // the chevron of an open folder
	shut     icon.ID   // and of a closed one
	renaming string    // the folder whose name is being edited ("" none)
	name     ui.TextBuffer
	age      int // frames since the renaming began: the field takes the focus
}

func (f *folderUI) add(k *base.Kit) {
	f.icons = f.icons[:0]
	for _, sym := range state.FolderIcons {
		f.icons = append(f.icons, k.AddIcon(ztheme.ToolIcon(sym, '▤')))
	}
	f.open = k.AddIcon(ztheme.SmallIcon("chevron.down", '▾'))
	f.shut = k.AddIcon(ztheme.SmallIcon("chevron.right", '▸'))
}

// iconOf is the atlas icon of a folder's symbol (the first for one unknown).
func (f *folderUI) iconOf(sym string) icon.ID {
	for i, s := range state.FolderIcons {
		if s == sym && i < len(f.icons) {
			return f.icons[i]
		}
	}
	if len(f.icons) > 0 {
		return f.icons[0]
	}
	return 0
}

// folderRow builds folder fi's row: a click opens or closes it, a right
// click its menu.
func (s *Screen) folderRow(e *screen.Env, a *state.Account, fi int) {
	if fi >= len(a.Settings.Folders) {
		return
	}
	fd := &a.Settings.Folders[fi]
	k := e.K
	c, r := k.Context(), k.Theme()
	rc := &rexui.ChatRowLook
	sc := k.Scale()
	inset, pad := theme.Snap(rc.Inset*sc), theme.Snap(rc.Pad*sc)
	renaming := s.fdr.renaming == fd.ID
	p := k.PressState(c.Fmt().Str("folder ").Str(fd.ID).String(), base.HoverOn)
	p.Name = fd.Name
	c.NextPadding(inset, 0, inset, 0)
	c.NextSemState(semExpanded(!fd.Collapsed))
	k.BeginPress(&p, ui.RoleButton, ui.Pct(1), ui.Px(theme.Snap(folderRowPt*sc)), 0, nil)
	if t := ztheme.T.RowHover*p.Hot + ztheme.T.RowPress*p.Down; t > 0 && !renaming {
		c.NextBG(theme.WithAlpha(r.Colors[theme.Label], t))
	}
	c.NextRadius(theme.Snap(rc.Radius * sc))
	c.NextAxis(ui.AxisX)
	c.NextCross(ui.CrossCenter)
	c.NextGap(e.Pt(ztheme.Space2))
	c.NextPadding(pad, 0, pad, 0)
	c.NextSize(ui.Pct(1), ui.Pct(1))
	c.Push(c.BoxKeyed(ui.DrawBackground, ui.KeyN(p.Key, 1), nil))
	chev := s.fdr.open
	if fd.Collapsed {
		chev = s.fdr.shut
	}
	k.Icon(chev, r.Colors[theme.Label3])
	k.Icon(s.fdr.iconOf(fd.Icon), r.Colors[theme.Label2])
	if renaming {
		k.NextWidth(ui.Pct(1))
		f := rexui.TextField(k.Kit, i18n.T("Folder name")+"###folder name", &s.fdr.name, i18n.T("Folder name"), rexui.FieldOpts{Size: theme.SizeSmall})
		if s.fdr.age++; s.fdr.age <= 2 {
			c.SetFocus(f.Key)
		}
		switch {
		case f.Submitted || (s.fdr.age > 2 && !f.Focused):
			if name := s.fdr.name.String(); name != "" {
				a.EditFolder(fi, func(x *state.Folder) { x.Name = name })
			}
			s.fdr.renaming = ""
		case f.Cancelled:
			s.fdr.renaming = ""
		}
	} else {
		k.NextWidth(ui.Pct(1))
		rexui.Label(k.Kit, fd.Name, rexui.LabelOpts{Style: rexui.TextSemibold})
		if n := folderUnread(a, fd); fd.Collapsed && n > 0 {
			rexui.Badge(k.Kit, badgeCount(n), rexui.BadgeOpts{Variant: rexui.BadgeCount, Name: i18n.T("Unread chats")})
		}
	}
	c.Pop()
	sig := k.EndPress(&p)
	if sig.Clicked && !renaming {
		a.EditFolder(fi, func(x *state.Folder) { x.Collapsed = !x.Collapsed })
	}
	if !s.native(e, sig.ID, menuFolder, 0, fi) && sig.RightClicked {
		in := c.Input()
		s.menu.showFolder(fi, in.MouseX, in.MouseY)
	}
}

// rule is the line under the folders: the chats under it are in none.
func (s *Screen) rule(e *screen.Env) {
	c, r := e.C(), e.K.Theme()
	sc := e.K.Scale()
	inset := theme.Snap((rexui.ChatRowLook.Inset + rexui.ChatRowLook.Pad) * sc)
	c.NextPadding(inset, e.Pt(ztheme.Space2), inset, e.Pt(ztheme.Space2))
	c.NextSize(ui.Pct(1), ui.Children(1))
	c.Push(c.Box(0, "rule"))
	c.NextSize(ui.Pct(1), ui.Px(r.Lines.Hairline))
	c.NextBG(r.Colors[theme.Separator])
	c.Box(ui.DrawBackground, "line")
	c.Pop()
}

// folderUnread is how many of a folder's chats have unread messages.
func folderUnread(a *state.Account, fd *state.Folder) int {
	n := 0
	for _, jid := range fd.Chats {
		if ch, ok := a.Model.Chats.Get(a.Model.Chats.Find(jid)); ok && (ch.Unread() > 0 || ch.MarkedUnread()) && !ch.Archived() {
			n++
		}
	}
	return n
}

// startRename edits folder id's name in its row.
func (s *Screen) startRename(a *state.Account, id string) {
	if i := a.FolderByID(id); i >= 0 {
		s.fdr.renaming, s.fdr.age = id, 0
		s.fdr.name.SetString(a.Settings.Folders[i].Name)
		s.fdr.name.SetSelection(0, len(a.Settings.Folders[i].Name))
	}
}

// folderMenu is a folder's menu.
func (s *Screen) folderMenu(items *[]ui.MenuItem, a *state.Account, fi int) {
	if fi >= len(a.Settings.Folders) {
		return
	}
	fd := &a.Settings.Folders[fi]
	add := func(it ui.MenuItem) { *items = append(*items, it) }
	add(ui.MenuItem{ID: miFolderRename, Title: i18n.T("Rename…"), Symbol: "pencil"})
	var icons []ui.MenuItem
	for i, sym := range state.FolderIcons {
		it := ui.MenuItem{ID: miFolderIcon + i, Title: sym, Symbol: sym}
		if sym == fd.Icon {
			it.Mark = ui.MarkOn
		}
		icons = append(icons, it)
	}
	add(ui.MenuItem{Title: i18n.T("Icon"), Symbol: fd.Icon, Sub: icons})
	// its space's colour, as Arc's (state.SpaceTints)
	tints := []ui.MenuItem{{ID: miFolderTint, Title: i18n.T("No colour")}}
	for i, name := range state.SpaceTints {
		tints = append(tints, ui.MenuItem{ID: miFolderTint + 1 + i, Title: tintLabel(name), Symbol: "circle.fill"})
	}
	for i := range tints {
		if name := tintAt(tints[i].ID - miFolderTint); name == fd.Tint && fd.TintDots[0] == 0 {
			tints[i].Mark = ui.MarkOn
		}
	}
	add(ui.MenuItem{Title: i18n.T("Colour"), Symbol: "paintpalette", Sub: tints})
	if fd.Space {
		add(ui.MenuItem{ID: miFolderSpace, Title: i18n.T("Stop showing it as a space"), Symbol: "square.stack"})
	} else {
		add(ui.MenuItem{ID: miFolderSpace, Title: i18n.T("Show it as a space (⌃1–9)"), Symbol: "square.stack"})
	}
	add(ui.MenuSeparator)
	add(ui.MenuItem{ID: miFolderDelete, Title: i18n.T("Delete folder (its chats stay)"), Symbol: "trash", Destructive: true})
}

// folderAct does what a folder's menu chose.
func (s *Screen) folderAct(e *screen.Env, a *state.Account, fi, item int) {
	if fi >= len(a.Settings.Folders) {
		return
	}
	switch {
	case item == miFolderRename:
		s.startRename(a, a.Settings.Folders[fi].ID)
	case item == miFolderSpace:
		a.EditFolder(fi, func(x *state.Folder) { x.Space = !x.Space })
		if !a.Settings.Folders[fi].Space && a.SpaceFolder == a.Settings.Folders[fi].ID {
			e.S.GoToSpace(state.Space{Acct: a.Index})
		}
	case item == miFolderDelete:
		a.DeleteFolder(fi)
	case item >= miFolderTint && item <= miFolderTint+len(state.SpaceTints):
		name := tintAt(item - miFolderTint)
		a.EditFolder(fi, func(x *state.Folder) { x.Tint, x.TintDots = name, [3]uint32{} })
	case item >= miFolderIcon && item < miFolderIcon+len(state.FolderIcons):
		sym := state.FolderIcons[item-miFolderIcon]
		a.EditFolder(fi, func(x *state.Folder) { x.Icon = sym })
	}
}

// folderItems is a chat's Folder submenu: each folder, none, a new one.
func (s *Screen) folderItems(a *state.Account, ch model.Chat) []ui.MenuItem {
	in := a.FolderOf(ch.ID())
	var out []ui.MenuItem
	for i := range a.Settings.Folders {
		fd := &a.Settings.Folders[i]
		it := ui.MenuItem{ID: miFolder + i, Title: fd.Name, Symbol: fd.Icon}
		if i == in {
			it.Mark = ui.MarkOn
		}
		out = append(out, it)
	}
	if in >= 0 {
		out = append(out, ui.MenuItem{ID: miNoFolder, Title: i18n.T("Take out of the folder")})
	}
	if len(out) > 0 {
		out = append(out, ui.MenuSeparator)
	}
	return append(out, ui.MenuItem{ID: miNewFolder, Title: i18n.T("New folder…"), Symbol: "folder.badge.plus"})
}

// chatFolderAct moves a chat as its Folder submenu chose; it reports
// whether item was one of the submenu's.
func (s *Screen) chatFolderAct(a *state.Account, ch model.Chat, item int) bool {
	switch {
	case item == miNoFolder:
		a.MoveToFolder(ch.ID(), -1)
	case item == miNewFolder:
		id := a.NewFolder(i18n.T("New folder"), "")
		a.MoveToFolder(ch.ID(), a.FolderByID(id))
		s.startRename(a, id)
	case item >= miFolder && item < miFolder+len(a.Settings.Folders):
		a.MoveToFolder(ch.ID(), item-miFolder)
	default:
		return false
	}
	return true
}

// tintAt is the colour at place i of the Colour submenu ("" none first).
func tintAt(i int) string {
	if i <= 0 || i > len(state.SpaceTints) {
		return ""
	}
	return state.SpaceTints[i-1]
}

// tintLabel is a space colour's name as the person reads it.
func tintLabel(name string) string {
	switch name {
	case "blue":
		return i18n.T("Blue")
	case "purple":
		return i18n.T("Purple")
	case "pink":
		return i18n.T("Pink")
	case "red":
		return i18n.T("Red")
	case "orange":
		return i18n.T("Orange")
	case "yellow":
		return i18n.T("Yellow")
	case "green":
		return i18n.T("Green")
	case "teal":
		return i18n.T("Teal")
	case "gray":
		return i18n.T("Gray")
	}
	return i18n.T("No colour")
}

// badgeCount is a count as a badge shows it: 99+ past 99.
func badgeCount(n int) string {
	if n > 99 {
		return "99+"
	}
	return itoa(n)
}

// semExpanded is a folder row's accessible state: open or closed.
func semExpanded(open bool) ui.SemState {
	if open {
		return ui.SemExpanded
	}
	return 0
}
