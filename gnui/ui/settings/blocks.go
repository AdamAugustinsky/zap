package settings

import (
	"gonativeui/rexui"
	"gonativeui/theme"
	"gonativeui/ui"

	"zapfast/state"
	"zapfast/ui/screen"
)

const (
	linkAuthor = "https://paolino.me"
	linkSource = "https://github.com/crmne/zapfast"
	linkThemes = "https://zapfast.rocks/themes/"
)

// block builds the rows that lay themselves out.
func (s *Screen) block(e *screen.Env, d *rowDef) {
	k := e.K.Kit
	c := e.C()
	switch d.id {
	case rThemeLinks:
		k.NextGap(8)
		k.BeginContainer(ui.AxisX, ui.CrossCenter, "theme links", ui.Pct(1), ui.Children(1), 0)
		k.Spacer()
		if button(e, s.tx.openThemes) {
			s.openFolder(e, s.themes.dir)
		}
		if button(e, s.tx.howTheme) {
			s.openURL(linkThemes)
		}
		k.EndContainer()
	case rLockForm:
		s.lockFormBlock(e)
	case rPrivNote:
		rexui.Label(k, s.privacyNote(e, e.Acct()), rexui.LabelOpts{Style: rexui.TextCallout, Color: rexui.Of(theme.Label2), Wrap: true})
	case rUpdateFlow:
		s.updateBlock(e)
	case rAccount:
		s.accountBlock(e)
	case rStorage:
		s.storageBlock(e)
	case rAbout:
		s.aboutBlock(e)
	}
	_ = c
}

// bytesIn builds dynamic text as a label in a box of its own, n of the key:
// the key of a label of bytes is its parent's, so a parent holds one.
func bytesIn(e *screen.Env, n uint64, w ui.Size, b []byte, o rexui.LabelOpts) {
	c := e.C()
	c.NextSize(w, ui.Children(1))
	c.Push(c.BoxKeyed(0, ui.KeyN(c.ParentKey(), n), nil))
	rexui.LabelBytes(e.K.Kit, b, o)
	c.Pop()
}

// raiseMain brings the main window forward: the dialogs this window opens
// are the main window's.
func raiseMain(e *screen.Env) {
	if e.A == nil {
		return
	}
	if ws := e.A.Windows(nil); len(ws) > 0 {
		ws[0].Show()
		ws[0].Focus()
	}
}

// openDialog opens a dialog of the main window and brings it forward.
func openDialog(e *screen.Env, d state.Dialog) {
	e.S.OpenDialog(d)
	raiseMain(e)
}

// aboutBlock is the version, the links and who made it (SE-073).
func (s *Screen) aboutBlock(e *screen.Env) {
	k := e.K.Kit
	rexui.Label(k, s.tx.versionLine, rexui.LabelOpts{Style: rexui.TextHeadline})
	rexui.Label(k, s.tx.tagline, rexui.LabelOpts{Style: rexui.TextCallout, Color: rexui.Of(theme.Label2), Wrap: true})
	k.NextGap(8)
	k.BeginContainer(ui.AxisX, ui.CrossCenter, "about buttons", ui.Pct(1), ui.Children(1), 0)
	if rel, ok := s.up.Available(); ok {
		if button(e, s.tx.updateTo(rel.Version)) {
			openUpdate(s)
		}
	}
	if button(e, s.tx.shortcuts) {
		openDialog(e, state.Dialog{Kind: state.DialogShortcuts})
	}
	if button(e, s.tx.about) {
		openDialog(e, state.Dialog{Kind: state.DialogAbout})
	}
	if button(e, s.tx.sourceCode) {
		s.openURL(linkSource)
	}
	k.EndContainer()
	k.NextGap(0)
	k.BeginContainer(ui.AxisX, ui.CrossCenter, "credit", ui.Pct(1), ui.Children(1), 0)
	rexui.Label(k, "♥  ", rexui.LabelOpts{Color: rexui.Of(theme.Danger)})
	rexui.Label(k, s.tx.creditBefore, rexui.LabelOpts{Color: rexui.Of(theme.Label2)})
	if rexui.Link(k, "Carmine Paolino", rexui.LinkOpts{}).Clicked {
		s.openURL(linkAuthor)
	}
	if s.tx.creditAfter != "" {
		rexui.Label(k, s.tx.creditAfter, rexui.LabelOpts{Color: rexui.Of(theme.Label2)})
	}
	k.EndContainer()
}

// openUpdate scrolls to the update block, which sits under Updates.
func openUpdate(s *Screen) { s.scrollToUpdates = true }
