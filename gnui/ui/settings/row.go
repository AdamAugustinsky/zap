package settings

import (
	"runtime"

	"gonativeui/rexui"
	"gonativeui/theme"
	"gonativeui/ui"

	"zapfast/ui/screen"
)

// exists reports whether this setup offers the row now: the rows that
// belong to a setting that is on, or to a value the phone reported
// (zf:src/ui/settings.rs sections()).
func (s *Screen) exists(e *screen.Env, d *rowDef) bool {
	a := e.Acct()
	switch d.id {
	case rAutoDownload, rHistory, rNotify, rReceipts, rTyping:
		return a != nil
	case rShowContent, rMsgSound, rLimit, rGroupSounds, rMentionSound:
		return a != nil && a.Settings.Notifications
	case rLockForm:
		return s.lock.open
	case rLockAfter:
		return len(e.S.Settings.AppLockHash) > 0
	case rPrivNote:
		return a != nil && s.privacyNote(e, a) != ""
	case rStartLogin, rTransparency: // the window's material is macOS's (shell.Config)
		return runtime.GOOS == "darwin"
	case rAccount, rStorage, rArchive, rDownloads:
		return a != nil
	case rUpdateFlow:
		return s.up.Snapshot().Release.Version != ""
	}
	if d.id >= rPriv0 && d.id <= rPriv6 {
		return a != nil && s.info(a).privacy.has(d.id-rPriv0)
	}
	return true
}

// detailLines is the most lines a description takes: upstream's longest is
// three at the column's width, and a narrow window needs more before the
// framework cuts it with an ellipsis.
const detailLines = 6

// row builds row n of the open section. A row with a title is a form row: its
// label with the description wrapped under it (RowOpts.Detail), and its
// control at the trailing edge. A row with no title is a block that lays
// itself out. The form draws the line above a row of its own; before the
// first of them, and before a block, the line is ours. nForm counts the
// section's form rows.
func (s *Screen) row(e *screen.Env, f *rexui.Form, d *rowDef, n int, nForm *int) {
	k := e.K.Kit
	c := e.C()
	parent := c.ParentKey()
	title := s.tx.title[d.id]
	if n > 0 && (d.ctrl == 0 || *nForm == 0) {
		c.NextSize(ui.Pct(1), ui.Px(theme.Snap(k.Pt(rexui.CardLook.DividerW))))
		c.NextBG(rexui.CardLook.Divider.RGBA(k.Theme()))
		c.BoxKeyed(ui.DrawBackground, ui.KeyN(parent, 100+uint64(n)), nil)
	}
	if d.ctrl == 0 {
		label := s.tx.block[d.id]
		k.NextPadding(padPt, 8, padPt, 8)
		k.NextGap(8)
		c.NextRole(ui.RoleGroup)
		id := k.BeginContainer(ui.AxisY, ui.CrossStart, label, ui.Pct(1), ui.Children(1), 0)
		if d.id == rUpdateFlow {
			s.updKey = c.KeyOf(id)
		}
		s.block(e, d)
		k.EndContainer()
		return
	}
	*nForm++
	f.Row(k, title, rexui.RowOpts{Detail: s.describe(e, d), MaxLines: detailLines})
	s.control(e, d, title)
	f.EndRow(k)
}

// describe is the row's description now: its text, or the variant its
// setting's state asks for.
func (s *Screen) describe(e *screen.Env, d *rowDef) string {
	switch d.id {
	case rReceipts:
		if a := e.Acct(); a != nil && s.info(a).receiptsOff {
			return s.tx.receiptsOff
		}
	case rProxy:
		if len(e.S.Settings.Proxy) == 0 && s.files.proxyEnv != "" {
			return s.files.proxyNote
		}
	case rArchive:
		return s.files.archive
	case rDownloads:
		return s.files.downloads(e)
	case rLog:
		return s.files.log
	case rStartLogin:
		if s.login.approval {
			return s.tx.approval
		}
	case rUpdateStatus:
		return s.statusText(e)
	}
	if d.id >= rPriv0 && d.id <= rPriv6 {
		return s.privacyHint(e, int(d.id-rPriv0))
	}
	return s.tx.desc[d.id]
}

// flip builds a switch that edits *on and notes the change. The row's label
// names it (Row's NameFirstUnnamed), as AppKit's form rows do.
func (s *Screen) flip(e *screen.Env, title string, on *bool) bool {
	k := e.K.Kit
	if rexui.Switch(k, "", on, rexui.SwitchOpts{}).Changed {
		s.changed(e)
		return true
	}
	return false
}
