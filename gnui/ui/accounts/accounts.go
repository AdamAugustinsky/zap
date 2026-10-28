package accounts

import (
	"gonativeui/icon"
	"gonativeui/img"
	"gonativeui/rexui"
	"gonativeui/theme"
	"gonativeui/ui"
	"gonativeui/widget/base"

	"zapfast/i18n"
	"zapfast/state"
	"zapfast/ui/accounts/acct"
	"zapfast/ui/screen"
)

const (
	menuWidth = 280 // points
	rowHeight = 48
	avatarPt  = 32
)

// entry is what the switcher lists for one account.
type entry struct {
	name, detail string
	label        string // what a screen reader says
	unread       int
	linked       bool
	picture      string  // the profile picture's file, if the core has one
	key          img.Key // names the picture in the image cache
}

// Screen is the account switcher.
type Screen struct {
	entries []entry
	sig     uint64 // what entries were made from
	made    bool

	check, plus icon.ID
}

var _ screen.Popover = (*Screen)(nil)

// New returns the switcher.
func New() *Screen { return &Screen{} }

// Init readies the switcher for a window: its icons.
func (s *Screen) Init(e *screen.Env) error {
	s.check = e.K.AddIcon(icon.Spec{Symbol: "checkmark", Fallback: '✓', Pt: 14, Weight: 0.3})
	s.plus = e.K.AddIcon(icon.Spec{Symbol: "plus", Fallback: '+', Pt: 14, Weight: 0.3})
	return nil
}

// refresh remakes the entries when the accounts changed.
func (s *Screen) refresh(st *state.State) {
	sig := st.Gen()<<8 + uint64(st.Active)
	for _, a := range st.Accounts {
		sig = sig*31 + a.Model.Gen() + uint64(a.Model.Me.Gen()) + uint64(a.Model.Link.Gen()) + uint64(a.Model.Avatars.Gen())
	}
	if s.made && sig == s.sig && len(s.entries) == len(st.Accounts) {
		return
	}
	s.made, s.sig = true, sig
	s.entries = s.entries[:0]
	now := st.Now().Unix()
	for _, a := range st.Accounts {
		var en entry
		en.name = acct.Label(a, i18n.T("Account {id}"))
		en.linked = a.Model.Linked()
		if en.linked {
			if p := acct.Phone(a); p != en.name {
				en.detail = p
			}
		} else {
			en.detail = i18n.T("Not linked")
		}
		en.unread = acct.Unread(a, now)
		if me := a.Model.Me.ID(); en.linked && len(me) > 0 {
			a.FetchAvatar(me, false) // asked once; the file arrives as an event
			if p, id, ok := a.Model.Avatars.Picture(me, false); ok {
				en.picture = string(p)
				en.key = img.Key{Hash: id} // not the path: a new picture reuses its file
			}
		}
		en.label = en.name
		if en.detail != "" {
			en.label += ", " + en.detail
		}
		if en.unread > 0 {
			en.label += ", " + i18n.Format(i18n.T("{count} unread chats"), "{count}", itoa(en.unread))
		}
		s.entries = append(s.entries, en)
	}
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var b [20]byte
	i := len(b)
	for ; n > 0; n /= 10 {
		i--
		b[i] = byte('0' + n%10)
	}
	return string(b[i:])
}

// Build shows the switcher at anchor while e.S.AccountMenu: the accounts with
// their picture, name or number and unread chats, a check on the one on
// screen, and Add account.
func (s *Screen) Build(e *screen.Env, anchor screen.Rect) {
	st, k := e.S, e.K
	s.refresh(st)
	c := e.C()
	p := rexui.BeginPopover(k.Kit, "accounts", [4]float32{anchor.X, anchor.Y, anchor.X + anchor.W, anchor.Y + anchor.H},
		rexui.PopoverOpts{Edge: rexui.PopoverRight, Label: i18n.T("Accounts"), Width: menuWidth, NoPad: true})
	k.NextPad(6)
	k.NextGap(0)
	k.BeginContainer(ui.AxisY, ui.CrossStart, "menu", ui.Pct(1), ui.Children(1), 0)
	switchTo := -1
	for i := range s.entries {
		if s.row(e, i) && i != st.Active {
			switchTo = i
		}
	}
	k.Space(4)
	rexui.Separator(k.Kit, rexui.SeparatorOpts{})
	k.Space(4)
	add := s.addRow(e)
	k.EndContainer()
	rexui.EndPopover(k.Kit, &p)
	switch {
	case switchTo >= 0:
		st.AccountMenu = false
		acct.Switch(st, switchTo)
	case add:
		st.AccountMenu = false
		acct.Add(st)
		st.Touch()
	case p.Dismissed:
		st.AccountMenu = false
		st.Touch()
	}
	_ = c
}

// row is one account: it reports whether it was chosen.
func (s *Screen) row(e *screen.Env, i int) bool {
	k, st := e.K, e.S
	c := e.C()
	en := &s.entries[i]
	current := i == st.Active
	r := k.Theme()
	pr := k.PressState(c.Fmt().Str("account###").Int(i).String(), base.HoverOn)
	pr.Name = en.label
	if current {
		c.NextSemState(ui.SemChecked)
	}
	if current || pr.Hot > 0 {
		role := theme.Fill4
		if pr.Hot > 0 {
			role = theme.Fill3
		}
		c.NextBG(r.Colors[role])
		c.NextRadius(e.Pt(6))
	}
	c.NextAxis(ui.AxisX)
	c.NextCross(ui.CrossCenter)
	c.NextGap(e.Pt(10))
	c.NextPadding(e.Pt(8), 0, e.Pt(10), 0)
	flags := ui.Flags(0)
	if current || pr.Hot > 0 {
		flags = ui.DrawBackground
	}
	k.BeginPress(&pr, ui.RoleRadio, ui.Pct(1), ui.Px(e.Pt(rowHeight)), flags, nil)
	var pic img.Handle
	if ic := k.Images(); ic != nil && en.picture != "" {
		key := en.key
		key.W, key.H, key.Flags = int32(e.Pt(avatarPt)), int32(e.Pt(avatarPt)), img.Cover
		pic, _ = ic.Request(key, img.File(en.picture))
	}
	rexui.Avatar(k.Kit, en.name, rexui.AvatarOpts{Size: avatarPt, Image: pic})
	k.NextGap(1)
	k.BeginContainer(ui.AxisY, ui.CrossStart, "names", ui.Pct(1), ui.Children(1), 0)
	rexui.Label(k.Kit, en.name, rexui.LabelOpts{Style: rexui.TextSemibold})
	if en.detail != "" {
		rexui.Label(k.Kit, en.detail, rexui.LabelOpts{Style: rexui.TextCallout, Color: rexui.Of(theme.Label2)})
	}
	k.EndContainer()
	switch {
	case current:
		k.Icon(s.check, r.Colors[theme.Accent])
	case en.unread > 0:
		rexui.BadgeCountN(k.Kit, en.unread, rexui.BadgeOpts{})
	}
	return k.EndPress(&pr).Clicked
}

// addRow is "Add account": it reports whether it was chosen.
func (s *Screen) addRow(e *screen.Env) bool {
	k := e.K
	c := e.C()
	r := k.Theme()
	pr := k.PressState("add###account", base.HoverOn)
	pr.Name = i18n.T("Add account")
	flags := ui.Flags(0)
	if pr.Hot > 0 {
		c.NextBG(r.Colors[theme.Fill3])
		c.NextRadius(e.Pt(6))
		flags = ui.DrawBackground
	}
	c.NextAxis(ui.AxisX)
	c.NextCross(ui.CrossCenter)
	c.NextGap(e.Pt(10))
	c.NextPadding(e.Pt(8), 0, e.Pt(10), 0)
	k.BeginPress(&pr, ui.RoleMenuItem, ui.Pct(1), ui.Px(e.Pt(36)), flags, nil)
	k.Icon(s.plus, r.Colors[theme.Label])
	rexui.Label(k.Kit, pr.Name, rexui.LabelOpts{})
	return k.EndPress(&pr).Clicked
}
