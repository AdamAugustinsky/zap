package info

import (
	"strings"

	"gonativeui/icon"
	"gonativeui/img"
	"gonativeui/rexui"
	"gonativeui/text"
	"gonativeui/theme"
	"gonativeui/ui"
	"gonativeui/widget"
	"gonativeui/widget/base"

	"zapfast/abi"
	"zapfast/i18n"
	"zapfast/model"
	"zapfast/state"
	"zapfast/ui/screen"
)

// contactEdit is the first name and surname being typed for Rename or Add to
// contacts (zf:src/ui/info.rs, contact_edit).
type contactEdit struct {
	on          bool
	first, last ui.TextBuffer
	focus       bool
}

// renameEdit is the group name being typed.
type renameEdit struct {
	on    bool
	name  ui.TextBuffer
	focus bool
}

const (
	itemName = iota
	itemPhone
	itemDesc
)

// header is the pane's header: a button at the left, then the title. Where
// the shell hosts it in the window's toolbar band (Toolbar, over the pane) it
// is built there; else it is the pane's first row. It reports whether the
// button was clicked in the pane.
func (s *Screen) header(e *screen.Env, ic icon.ID, label, title string, back bool) bool {
	s.hdr = header{have: true, ic: ic, label: label, title: title, back: back}
	if s.bandNow {
		return false
	}
	k := e.K
	k.NextPadding(margin, 10, margin, 10)
	k.NextGap(6)
	k.BeginContainer(ui.AxisX, ui.CrossCenter, "header", ui.Pct(1), k.Fixed(headerPt), 0)
	btn := rexui.IconButton(k.Kit, ic, label, rexui.IconButtonOpts{Variant: rexui.IconBorderless})
	e.TabStop(screen.StopInfoClose, btn.ID)
	clicked := btn.Clicked
	rexui.Label(k.Kit, title, rexui.LabelOpts{Style: rexui.TextTitle3})
	k.EndContainer()
	rexui.Separator(k.Kit, rexui.SeparatorOpts{Hairline: true})
	return clicked
}

// header is what the pane's header says this frame.
type header struct {
	have  bool
	ic    icon.ID
	label string
	title string
	back  bool // the button goes back to the overview, else it closes the panel
}

// Toolbar is the pane's header in the window's toolbar band, over the pane
// (screen.ToolbarPart): the shell calls it after the conversation's items, in
// the inspector section. The shell is the one that opens the band, so until it
// calls this the pane builds its header itself.
func (s *Screen) Toolbar(e *screen.Env, tb *rexui.Toolbar) {
	h := s.hdr
	if !h.have {
		return
	}
	s.inBand = true
	btn := tb.Item(h.ic, h.label)
	e.TabStop(screen.StopInfoClose, btn.ID)
	k, c := e.K, e.C()
	tb.BeginCustom("info-title", ui.Children(1))
	c.NextSize(ui.Children(1), ui.Px(tb.Height()))
	c.NextAxis(ui.AxisX)
	c.NextCross(ui.CrossCenter)
	c.NextPadding(e.Pt(8), 0, e.Pt(8), 0)
	c.Push(c.Box(0, "info-title-row"))
	rexui.Label(k.Kit, h.title, rexui.LabelOpts{Style: rexui.TextHeadline})
	c.Pop()
	tb.EndCustom()
	if btn.Clicked {
		if h.back {
			e.S.Panes.InfoView = state.InfoOverview
			e.S.Touch()
		} else {
			s.closePanel(e)
		}
	}
}

func (s *Screen) title() string {
	switch {
	case s.v.group:
		return i18n.T("Group info")
	case s.v.channel:
		return i18n.T("Channel info")
	}
	return i18n.T("Contact info")
}

// overview builds the chat's picture, name, details, actions, media row and
// members.
func (s *Screen) overview(e *screen.Env, a *state.Account, r screen.Rect) {
	k := e.K
	c := e.C()
	ic, label := s.ic.closeX, i18n.T("Close")
	if e.S.Panes.Covering {
		ic, label = s.ic.back, i18n.T("Back")
	}
	if s.header(e, ic, label, s.title(), false) {
		s.closePanel(e)
		return
	}
	scale := k.Scale()
	contentPt := r.W/scale - 2*margin - 17
	k.NextSize(ui.Pct(1), widget.Fill)
	v := k.BeginScroll(i18n.T("Chat info"), &s.scroll, base.ScrollVertical)
	k.NextPadding(margin, 12, margin, 12)
	k.NextGap(8)
	k.BeginContainer(ui.AxisY, ui.CrossCenter, "content", ui.Pct(1), ui.Children(1), 0)

	c.BeginSelection(&s.sel, ui.ScopeOpts{Text: s})
	s.identity(e, a, contentPt)
	s.actions(e, a)
	s.description(e)
	if s.allowed(a) {
		s.mediaRow(e, a, contentPt)
	}
	if s.v.group && s.v.members > 0 {
		s.members(e, a, r)
	}
	if s.v.canLeave {
		label := i18n.T("Leave group")
		if s.v.channel {
			label = i18n.T("Leave channel")
		}
		leave := rexui.Button(k.Kit, label, rexui.ButtonOpts{Variant: rexui.ButtonDestructive})
		e.TabStop(screen.StopInfoControl+leaveStop, leave.ID)
		if leave.Clicked {
			if s.v.ref != 0 {
				e.S.OpenDialog(state.Dialog{Kind: state.DialogConfirmLeaveGroup, Chat: s.v.ref})
			}
		}
	}
	c.EndSelection()
	k.EndContainer()
	k.EndScroll(v)
}

// selectable draws text s as a selectable, centred paragraph of the panel's
// selection scope.
func (s *Screen) selectable(e *screen.Env, item uint64, b []byte, f text.FontKey, col uint32) {
	k := e.K
	c := e.C()
	s.selText[item] = b
	c.NextSize(ui.Pct(1), ui.TextSize(0))
	c.NextFont(f)
	c.NextTextColor(col)
	c.NextAlign(ui.AlignCenter)
	c.RichText(ui.KeyN(c.ParentKey(), 9100+item), b, nil, nil, ui.RichOpts{Item: item})
	_ = k
}

func (s *Screen) font(e *screen.Env, pt float32, weight int) text.FontKey {
	return e.K.TextSystem().Font(text.FontDesc{SizePt: pt, Weight: weight, Scale: e.K.Scale()})
}

// line is one centred line of text in the panel's secondary colours.
func (s *Screen) line(e *screen.Env, b string, st rexui.TextStyle, col theme.Role) {
	if b == "" {
		return
	}
	rexui.Label(e.K.Kit, b, rexui.LabelOpts{Style: st, Color: rexui.Of(col)})
}

// picture is the chat's picture, if its file is known and the image cache is
// the window's.
func (s *Screen) picture(e *screen.Env, a *state.Account, size float32) (img.Handle, bool) {
	jid := s.jid[:s.jidN]
	_, _, full := a.Model.Avatars.Picture(jid, true)
	_, _, thumb := a.Model.Avatars.Picture(jid, false)
	return screen.Avatar(e.K.Images(), a, jid, true, int32(size*e.K.Scale())), full || thumb
}

// identity is the picture, name and details.
func (s *Screen) identity(e *screen.Env, a *state.Account, contentPt float32) {
	k := e.K
	c := e.C()
	r := k.Theme()
	photo := min(max(0.55*contentPt, 120), 200)
	name := model.S(s.v.name)
	handle, hasPic := s.picture(e, a, photo)
	saving := s.isSaving(s.jid[:s.jidN])
	editable := s.v.canEdit && !saving
	av := rexui.AvatarOpts{Size: photo, Image: handle}
	if editable {
		p := k.PressState(i18n.T("Change group photo"), base.HoverOn)
		k.BeginPress(&p, ui.RoleButton, ui.Px(k.Pt(photo)), ui.Px(k.Pt(photo)), 0, nil)
		rexui.Avatar(k.Kit, name, av)
		sig := k.EndPress(&p)
		e.TabStop(screen.StopInfoControl+photoStop, sig.ID)
		build := func(m *[]ui.MenuItem) { photoItems(m, hasPic) }
		if sig.Clicked {
			// the menu opens under the picture, for a click and for Enter alike
			r := c.RectOf(p.Key)
			items := k.MenuScratch()
			build(items)
			c.ContextMenu(p.ID, r[0], r[3], *items)
		}
		if item, ok := rexui.OnContextMenu(k.Kit, p.ID, build); ok {
			s.photoChoice(a, item)
		}
	} else {
		rexui.Avatar(k.Kit, name, av)
	}
	big := s.font(e, 19, theme.WeightBold)
	if (s.rename.on && editable) || (s.contact.on && !editable && s.canRename()) {
		e.TabInBuildOrder() // Tab goes between the name's fields, as upstream's
	}
	switch {
	case s.rename.on && editable:
		s.renameField(e, a)
	case s.rename.on:
		s.rename.on = false
	case editable:
		s.nameWithPencil(e, big, r.Colors[theme.Label])
	case s.contact.on && s.canRename():
		s.contactFields(e, a)
	default:
		s.selectable(e, itemName, s.v.name, big, r.Colors[theme.Label])
	}
	if s.v.phone != "" {
		s.selectable(e, itemPhone, base.StrBytes(s.v.phone), s.font(e, 13.5, 400), r.Colors[theme.Label2])
	}
	if saving {
		s.line(e, i18n.T("Saving…"), rexui.TextCallout, theme.Label3)
	}
	s.line(e, s.v.memberLine, rexui.TextBody, theme.Label2)
	s.line(e, s.v.status, rexui.TextCallout, theme.Label3)
	s.line(e, s.v.mutedLine, rexui.TextCallout, theme.Label2)
	s.line(e, s.v.timer, rexui.TextCallout, theme.Label2)
}

func (s *Screen) canRename() bool { return s.v.phone != "" && !s.v.mine }

// nameWithPencil is a group's name and the pencil that edits it.
func (s *Screen) nameWithPencil(e *screen.Env, f text.FontKey, col uint32) {
	k := e.K
	k.NextGap(4)
	k.NextJustify(ui.JustifyCenter)
	k.BeginContainer(ui.AxisX, ui.CrossCenter, "name", ui.Pct(1), ui.Children(1), 0)
	c := e.C()
	s.selText[itemName] = s.v.name
	c.NextSize(ui.TextSize(0), ui.TextSize(0))
	c.NextFont(f)
	c.NextTextColor(col)
	c.RichText(ui.KeyN(c.ParentKey(), 9100+itemName), s.v.name, nil, nil, ui.RichOpts{Item: itemName, MaxW: k.Pt(260)})
	pencil := rexui.IconButton(k.Kit, s.ic.pencil, i18n.T("Edit group name"), rexui.IconButtonOpts{Variant: rexui.IconBorderless})
	e.TabStop(screen.StopInfoControl+pencilStop, pencil.ID)
	if pencil.Clicked {
		s.rename.on, s.rename.focus = true, true
		if len(strings.TrimSpace(string(s.v.subject))) == 0 || (string(s.v.subject) == "Group" && !s.v.subjectKnown) {
			s.rename.name.Reset()
		} else {
			s.rename.name.SetBytes(s.v.subject)
		}
		s.rename.name.SelectAll()
	}
	k.EndContainer()
}

// renameField is the group name being typed, with its check and cross.
func (s *Screen) renameField(e *screen.Env, a *state.Account) {
	k := e.K
	k.NextGap(4)
	k.NextJustify(ui.JustifyCenter)
	k.BeginContainer(ui.AxisX, ui.CrossCenter, "rename", ui.Pct(1), ui.Children(1), 0)
	k.NextWidth(k.Fixed(200))
	f := rexui.TextField(k.Kit, i18n.T("Group name"), &s.rename.name, i18n.T("Group name"), rexui.FieldOpts{})
	if s.rename.focus {
		s.rename.focus = false
		k.Focus(f.Key)
	}
	submit := f.Submitted
	cancel := f.Cancelled
	if rexui.IconButton(k.Kit, s.ic.check, i18n.T("Save name (Enter)"), rexui.IconButtonOpts{Variant: rexui.IconBorderless}).Clicked {
		submit = true
	}
	if rexui.IconButton(k.Kit, s.ic.closeX, i18n.T("Cancel (Escape)"), rexui.IconButtonOpts{Variant: rexui.IconBorderless}).Clicked {
		cancel = true
	}
	k.EndContainer()
	switch {
	case submit:
		typed := truncateRunes(strings.TrimSpace(s.rename.name.String()), maxName)
		s.rename.on = false
		if typed != "" && typed != string(s.v.subject) {
			cmd := a.Cmd(abi.CommandTagSetGroupName)
			cmd.SetGroupName.Chat, cmd.SetGroupName.Name = s.jid[:s.jidN], []byte(typed)
			a.Send(cmd)
			s.setSaving(s.jid[:s.jidN], true)
		}
	case cancel:
		s.rename.on = false
	}
}

func truncateRunes(s string, n int) string {
	i := 0
	for j := range s {
		if i == n {
			return s[:j]
		}
		i++
	}
	return s
}

const (
	menuChangePhoto = iota + 1
	menuRemovePhoto
)

// photoItems is the group picture's menu: Change photo, and Remove photo when
// there is a photo to remove.
func photoItems(m *[]ui.MenuItem, hasPic bool) {
	*m = append(*m, ui.MenuItem{ID: menuChangePhoto, Title: i18n.T("Change photo"), Symbol: "photo"})
	if hasPic {
		*m = append(*m, ui.MenuItem{ID: menuRemovePhoto, Title: i18n.T("Remove photo"), Symbol: "trash"})
	}
}

func (s *Screen) photoChoice(a *state.Account, item int) {
	switch item {
	case menuChangePhoto:
		cmd := a.Cmd(abi.CommandTagPickGroupPicture)
		cmd.PickGroupPicture.Chat = s.jid[:s.jidN]
		a.Send(cmd)
	case menuRemovePhoto:
		cmd := a.Cmd(abi.CommandTagSetGroupPicture)
		cmd.SetGroupPicture.Chat = s.jid[:s.jidN]
		a.Send(cmd)
	}
}

// contactFields are the first name and surname fields with their check.
func (s *Screen) contactFields(e *screen.Env, a *state.Account) {
	k := e.K
	k.NextGap(4)
	k.NextJustify(ui.JustifyCenter)
	k.BeginContainer(ui.AxisX, ui.CrossCenter, "contact", ui.Pct(1), ui.Children(1), 0)
	k.NextWidth(k.Fixed(104))
	first := rexui.TextField(k.Kit, i18n.T("First name"), &s.contact.first, i18n.T("First name"), rexui.FieldOpts{})
	if s.contact.focus {
		s.contact.focus = false
		k.Focus(first.Key)
	}
	k.NextWidth(k.Fixed(104))
	last := rexui.TextField(k.Kit, i18n.T("Surname"), &s.contact.last, i18n.T("Surname"), rexui.FieldOpts{})
	submit := first.Submitted || last.Submitted
	if rexui.IconButton(k.Kit, s.ic.check, i18n.T("Save name (Enter)"), rexui.IconButtonOpts{Variant: rexui.IconBorderless}).Clicked {
		submit = true
	}
	k.EndContainer()
	switch {
	case first.Cancelled || last.Cancelled:
		s.contact.on = false
	case submit:
		f, l := strings.TrimSpace(s.contact.first.String()), strings.TrimSpace(s.contact.last.String())
		if f == "" && l == "" {
			return
		}
		s.contact.on = false
		full := f
		switch {
		case f == "":
			full = l
		case l != "":
			full = f + " " + l
		}
		cmd := a.Cmd(abi.CommandTagSaveContact)
		cmd.SaveContact.ID, cmd.SaveContact.FullName = s.jid[:s.jidN], []byte(full)
		if f != "" {
			cmd.SaveContact.FirstName, cmd.SaveContact.HasFirstName = []byte(f), true
		}
		cmd.SaveContact.ToPhone = a.Settings.SaveContactsToPhone
		a.Send(cmd)
	}
}

// editorNames splits a saved name for the contact editor: the first name
// saved with it, and what follows (zf:src/util.rs editor_names).
func editorNames(name, first string) (string, string) {
	name = strings.TrimSpace(name)
	if first = strings.TrimSpace(first); first != "" {
		if rest, ok := strings.CutPrefix(name, first); ok && (rest == "" || rest[0] == ' ' || rest[0] == '\t') {
			return first, strings.TrimSpace(rest)
		}
	}
	return name, ""
}

// action is one button of the actions rows.
type action struct {
	ic    icon.ID
	label string
}

// actions are the chat's buttons, wrapping onto centred lines.
func (s *Screen) actions(e *screen.Env, a *state.Account) {
	k := e.K
	var list [8]action
	n := 0
	add := func(ic icon.ID, label string) { list[n] = action{ic, label}; n++ }
	if !s.v.group && !s.v.mine {
		add(s.ic.message, i18n.T("Message"))
	}
	if s.canRename() {
		if s.v.savedName {
			add(s.ic.person, i18n.T("Rename"))
		} else {
			add(s.ic.person, i18n.T("Add to contacts"))
		}
	}
	if s.v.phone != "" {
		add(s.ic.copy, i18n.T("Copy number"))
	}
	if s.v.hasChat {
		if s.v.muted {
			add(s.ic.bell, i18n.T("Unmute"))
		} else {
			add(s.ic.bellOff, i18n.T("Mute"))
		}
		if s.v.pinned {
			add(s.ic.pinOff, i18n.T("Unpin"))
		} else {
			add(s.ic.pin, i18n.T("Pin"))
		}
		if s.v.archived {
			add(s.ic.unarchive, i18n.T("Unarchive"))
		} else {
			add(s.ic.archive, i18n.T("Archive"))
		}
	}
	// the buttons wrap onto lines, each line centred
	c := e.C()
	c.BeginFlow("actions", ui.FlowOpts{GapX: k.Pt(8), GapY: k.Pt(8), Align: ui.FlowCenter, Cross: ui.CrossCenter})
	for i := 0; i < n; i++ {
		b := rexui.ButtonIcon(k.Kit, list[i].ic, list[i].label, rexui.ButtonOpts{})
		e.TabStop(screen.StopInfoControl+actionStop+screen.TabStop(min(i, int(mediaRowStop-actionStop-1))), b.ID)
		if b.Clicked {
			s.act(e, a, list[i].label)
		}
	}
	c.EndFlow()
}

// The panel's controls' places after its close button (StopInfoControl +
// these, zf:src/ui/info.rs:28-35): the photo, the name's pencil, the
// actions, the media row, the media tabs, Leave.
const (
	photoStop    screen.TabStop = 0
	pencilStop   screen.TabStop = 1
	actionStop   screen.TabStop = 3
	mediaRowStop screen.TabStop = 10
	tabStop      screen.TabStop = 20
	findStop     screen.TabStop = 25 // the media view's search, after its tabs
	leaveStop    screen.TabStop = 30
)

// act runs the button the label names.
func (s *Screen) act(e *screen.Env, a *state.Account, label string) {
	jid := s.jid[:s.jidN]
	switch label {
	case i18n.T("Message"):
		s.startChat(e, a)
		s.closePanel(e)
	case i18n.T("Rename"), i18n.T("Add to contacts"):
		name := strings.TrimPrefix(string(s.v.name), "~")
		f, l := editorNames(name, string(s.v.first))
		s.contact.first.SetString(f)
		s.contact.last.SetString(l)
		s.contact.on, s.contact.focus = true, true
		s.rename.on = false
	case i18n.T("Copy number"):
		if c := e.C(); c.ClipboardSet != nil {
			c.ClipboardSet([]byte("+" + s.v.phoneDigits))
		}
		e.S.Toast([]byte(i18n.T("Copied")), false)
	case i18n.T("Mute"):
		cmd := a.Cmd(abi.CommandTagSetMuted)
		cmd.SetMuted.Chat, cmd.SetMuted.Until, cmd.SetMuted.HasUntil = jid, 0, true
		a.Send(cmd)
	case i18n.T("Unmute"):
		cmd := a.Cmd(abi.CommandTagSetMuted)
		cmd.SetMuted.Chat = jid
		a.Send(cmd)
	case i18n.T("Pin"):
		if s.pinnedCount(a) >= a.Model.PinLimit() {
			e.S.Toast([]byte(i18n.Format(i18n.T("You can only pin {} chats"), "{}", itoa(a.Model.PinLimit()))), false)
			return
		}
		fallthrough
	case i18n.T("Unpin"):
		cmd := a.Cmd(abi.CommandTagSetPinned)
		cmd.SetPinned.Chat, cmd.SetPinned.Pinned = jid, label == i18n.T("Pin")
		a.Send(cmd)
	case i18n.T("Archive"), i18n.T("Unarchive"):
		archive := label == i18n.T("Archive")
		cmd := a.Cmd(abi.CommandTagSetArchived)
		cmd.SetArchived.Chat, cmd.SetArchived.Archived = jid, archive
		a.Send(cmd)
		if archive && s.v.ref != 0 && s.v.ref == a.Open {
			a.OpenChat(0)
		}
		s.closePanel(e)
	}
}

func (s *Screen) pinnedCount(a *state.Account) int {
	n := 0
	for _, ref := range a.Model.Chats.Order() {
		if !a.Model.Chats.At(ref).Pinned() {
			break // pinned chats come first
		}
		n++
	}
	return n
}

// startChat opens the chat with the person, making it first when the core
// does not have it (Action::StartChat).
func (s *Screen) startChat(e *screen.Env, a *state.Account) {
	jid := s.jid[:s.jidN]
	if ref := a.Model.Chats.Find(jid); ref != 0 {
		a.OpenChat(ref)
		return
	}
	cmd := a.Cmd(abi.CommandTagEnsureChat)
	cmd.EnsureChat.Chat, cmd.EnsureChat.Name = jid, s.v.name
	if a.Send(cmd) == nil {
		s.openN = copy(s.openJID[:], jid)
		s.openAcct = a.Index
	}
}

// description is a group's description, selectable, scrolling when long.
func (s *Screen) description(e *screen.Env) {
	if !s.v.group || !s.v.hasDesc {
		return
	}
	k := e.K
	c := e.C()
	r := k.Theme()
	k.NextGap(4)
	k.BeginContainer(ui.AxisY, ui.CrossStart, "description", ui.Pct(1), ui.Children(1), 0)
	rexui.Label(k.Kit, i18n.T("Description"), rexui.LabelOpts{Style: rexui.TextSemibold, Color: rexui.Of(theme.Label2)})
	// a long description scrolls in place (up to 120 pt), so the media and
	// the members stay close under it
	k.NextSize(ui.Pct(1), ui.Children(1))
	k.ScrollUpTo(i18n.T("Description text"), &s.descSc, base.ScrollVertical, descMaxPt, func() {
		s.selText[itemDesc] = s.v.description
		c.NextSize(ui.Pct(1), ui.TextSize(0))
		c.NextFont(s.font(e, 13.5, 400))
		c.NextTextColor(r.Colors[theme.Label])
		c.RichText(ui.KeyN(c.ParentKey(), 9100+itemDesc), s.v.description, nil, nil, ui.RichOpts{Item: itemDesc})
	})
	k.EndContainer()
}

const descMaxPt = 120 // the tallest the description grows before it scrolls (zf:src/ui/info.rs:527)
