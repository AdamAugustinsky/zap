package settings

import (
	"unicode/utf8"

	"gonativeui/rexui"
	"gonativeui/theme"
	"gonativeui/ui"

	"zapfast/abi"
	"zapfast/format"
	"zapfast/model"
	"zapfast/state"
	"zapfast/ui/screen"
)

// profile is the name and About being edited.
type profile struct {
	editing     bool
	submit      bool
	name, about ui.TextBuffer
}

const (
	nameLimit  = 25
	aboutLimit = 139
	avatarPt   = 56
)

// accountBlock is our WhatsApp profile, which can be edited in place, and the
// account buttons (SE-060, SE-061).
func (s *Screen) accountBlock(e *screen.Env) {
	k := e.K.Kit
	a := e.Acct()
	me := &a.Model.Me
	name := model.S(me.Name())
	about := model.S(me.About())
	in := s.info(a)
	if !in.meOK || in.meGen != me.Gen() {
		in.meOK, in.meGen = true, me.Gen()
		in.phone = []byte(phoneOf(me.ID()))
	}
	// The buttons sit at the trailing edge of the picture's line; when they
	// and the name do not both fit (a long language, a narrow window) they go
	// on a line of their own under the name, and stack if that is too narrow
	// too. Upstream's line (egui) does neither. Their width is last frame's.
	c := e.C()
	if r := c.RectOf(s.acctBtn.key); s.acctBtn.key != 0 && !s.acctBtn.stacked && r[2] > r[0] {
		s.acctBtn.w = r[2] - r[0]
	}
	gap, indent := e.Pt(16), e.Pt(avatarPt+16)
	wrap := s.acctBtn.w > 0 && s.innerPx-indent-2*gap-s.acctBtn.w < e.Pt(160) // three gaps in the line
	stack := wrap && s.acctBtn.w > s.innerPx-indent
	k.NextGap(12)
	k.BeginContainer(ui.AxisY, ui.CrossStart, "profile block", ui.Pct(1), ui.Children(1), 0)
	k.NextGap(16)
	k.BeginContainer(ui.AxisX, ui.CrossStart, "profile", ui.Pct(1), ui.Children(1), 0)

	// the picture, which changes it when pressed
	shown := name
	if shown == "" {
		shown = model.S(in.phone)
	}
	in.pic.sync(e, a, me.ID())
	av := rexui.Avatar(k, shown, rexui.AvatarOpts{Size: avatarPt, Image: in.pic.handle, Pressable: true, Label: s.tx.changePicture})
	if k.SignalOf(av).Clicked {
		a.Send(a.Cmd(abi.CommandTagPickProfilePicture))
	}

	infoW := max(s.innerPx-indent-2*gap-s.acctBtn.w, e.Pt(160))
	if wrap {
		infoW = max(s.innerPx-indent, e.Pt(160))
	}
	k.NextGap(4)
	k.BeginContainer(ui.AxisY, ui.CrossStart, "info", ui.Px(infoW), ui.Children(1), 0)
	if s.profile.editing {
		s.profileField(e, s.tx.yourName, &s.profile.name, nameLimit)
		s.profileField(e, s.tx.aboutLabel, &s.profile.about, aboutLimit)
	} else {
		k.NextGap(8)
		k.BeginContainer(ui.AxisX, ui.CrossCenter, "name", ui.Pct(1), ui.Children(1), 0)
		line := me.Name()
		if len(line) == 0 {
			line = s.tx.linkedDevice
		}
		bytesIn(e, 1, ui.Children(1), line, rexui.LabelOpts{Style: rexui.TextHeadline})
		if rexui.IconButton(k, s.icons.pencil, s.tx.editProfile, rexui.IconButtonOpts{Variant: rexui.IconBorderless}).Clicked {
			s.profile.editing = true
			s.profile.name.SetString(name)
			s.profile.about.SetString(about)
		}
		k.EndContainer()
		bytesIn(e, 2, ui.Pct(1), in.phone, rexui.LabelOpts{Color: rexui.Of(theme.Label2)})
		if len(me.About()) > 0 {
			bytesIn(e, 3, ui.Pct(1), me.About(), rexui.LabelOpts{Color: rexui.Of(theme.Label2), Wrap: true})
		}
	}
	k.EndContainer()

	if wrap {
		k.EndContainer() // the picture's line
		k.NextPadding(indent, 0, 0, 0)
	} else {
		k.Spacer()
	}
	k.NextGap(8)
	axis, cross := ui.AxisX, ui.CrossCenter
	if stack {
		axis, cross = ui.AxisY, ui.CrossStart
	}
	btns := k.BeginContainer(axis, cross, "account buttons", ui.Children(1), ui.Children(1), 0)
	s.acctBtn.key, s.acctBtn.stacked = c.KeyOf(btns), stack
	if button(e, s.tx.addAccount) {
		e.S.AddingAccount = true
		e.S.Page = state.PageChats
		e.S.Touch()
		raiseMain(e)
	}
	if len(e.S.Accounts) > 1 {
		if button(e, s.tx.removeAccount) {
			openDialog(e, state.Dialog{Kind: state.DialogConfirmRemoveAccount, Account: a.Index})
		}
	} else if button(e, s.tx.unlink) {
		openDialog(e, state.Dialog{Kind: state.DialogConfirmUnlink})
	}
	k.EndContainer()
	if !wrap {
		k.EndContainer() // the picture's line
	}
	k.EndContainer() // the block

	if s.profile.editing {
		s.profileButtons(e, a, name, about)
	}
}

// profileField is a text field with its caption above it; Enter saves.
func (s *Screen) profileField(e *screen.Env, label string, buf *ui.TextBuffer, limit int) {
	k := e.K.Kit
	rexui.Label(k, label, rexui.LabelOpts{Style: rexui.TextCallout, Color: rexui.Of(theme.Label2)})
	sig := rexui.TextField(k, label, buf, "", rexui.FieldOpts{})
	if sig.Changed && utf8.RuneCountInString(buf.String()) > limit {
		buf.SetString(string([]rune(buf.String())[:limit]))
	}
	if sig.Submitted {
		s.profile.submit = true
	}
}

// profileButtons are Save and Cancel under the fields.
func (s *Screen) profileButtons(e *screen.Env, a *state.Account, name, about string) {
	k := e.K.Kit
	k.NextGap(8)
	k.NextPadding(avatarPt+16, 0, 0, 0)
	k.BeginContainer(ui.AxisX, ui.CrossCenter, "profile buttons", ui.Pct(1), ui.Children(1), 0)
	save := rexui.Button(k, s.tx.save, rexui.ButtonOpts{Variant: rexui.ButtonPrimary}).Clicked || s.profile.submit
	cancel := button(e, s.tx.cancel)
	k.EndContainer()
	s.profile.submit = false
	if save {
		newName, newAbout := trim(s.profile.name.String()), trim(s.profile.about.String())
		changeName := newName != "" && newName != name
		changeAbout := newAbout != about
		if changeName || changeAbout {
			cmd := a.Cmd(abi.CommandTagSetProfile)
			cmd.SetProfile.Name, cmd.SetProfile.HasName = []byte(newName), changeName
			cmd.SetProfile.About, cmd.SetProfile.HasAbout = []byte(newAbout), changeAbout
			a.Send(cmd)
		}
	}
	if save || cancel {
		s.profile.editing = false
	}
}

// phoneOf is the number a JID names, the digits before @ or :, with a plus
// and grouped by country (format.Phone).
func phoneOf(jid []byte) string {
	n := 0
	for n < len(jid) && jid[n] >= '0' && jid[n] <= '9' {
		n++
	}
	if n == 0 {
		return string(jid)
	}
	return format.Phone(string(jid[:n]))
}
