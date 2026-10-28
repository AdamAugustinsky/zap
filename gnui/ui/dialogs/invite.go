package dialogs

import (
	"strconv"

	"gonativeui/img"
	"gonativeui/rexui"
	"gonativeui/theme"
	"gonativeui/ui"
	"gonativeui/widget"

	"zapfast/abi"
	"zapfast/i18n"
	"zapfast/state"
)

type invitePhase uint8

const (
	inviteLoading invitePhase = iota
	inviteReady
	inviteJoining
	inviteFailed
)

// invite is a group invite being looked up (zf:src/model.rs:2165). The dialog
// is opened with the link's code in state.Dialog.Text.
type invite struct {
	code     string
	phase    invitePhase
	id       string
	subject  string
	desc     string
	hasDesc  bool
	members  uint64
	approval bool
	err      string
	descScr  widget.ScrollState
}

// reset starts the lookup (Command.PreviewInvite).
func (v *invite) reset(a *state.Account, d *state.Dialog) {
	*v = invite{code: string(d.Text), descScr: v.descScr}
	cmd := a.Cmd(abi.CommandTagPreviewInvite)
	cmd.PreviewInvite.Code = d.Text
	a.Send(cmd)
}

func (s *Screen) joinGroup(e *screenEnv, a *state.Account, dl *rexui.Dialog) {
	k, v := e.K, &s.inv
	switch v.phase {
	case inviteLoading:
		k.NextGap(8)
		k.BeginContainer(ui.AxisX, ui.CrossCenter, "looking", ui.Pct(1), ui.Children(1), 0)
		rexui.Spinner(k.Kit, i18n.T("Looking up the group…")+"###looking-spinner", rexui.SpinnerOpts{Size: 3})
		rexui.Label(k.Kit, i18n.T("Looking up the group…"), rexui.LabelOpts{Color: secondary})
		k.EndContainer()
		s.closeOnly(e, dl)
		return
	case inviteFailed:
		para(k, v.err, rexui.TextBody, rexui.Tint{})
		s.closeOnly(e, dl)
		return
	}
	joining := v.phase == inviteJoining
	rexui.Label(k.Kit, i18n.Format(i18n.N("{} member", "{} members", int(v.members)), "{}", strconv.FormatUint(v.members, 10)), rexui.LabelOpts{Color: secondary})
	if v.hasDesc {
		k.NextHeight(k.Fixed(min(140, 40+float32(len(v.desc))/3)))
		sv := k.BeginScroll("description", &v.descScr, widget.ScrollVertical)
		para(k, v.desc, rexui.TextBody, rexui.Tint{})
		k.EndScroll(sv)
	}
	member := a.Model.Chats.Find([]byte(v.id)) != 0
	if v.approval && !member {
		para(k, i18n.T("An admin must approve your request before you join."), rexui.TextCallout, secondary)
	}
	label := i18n.T("Join group")
	switch {
	case joining:
		label = i18n.T("Joining…")
	case member:
		label = i18n.T("Open chat")
	case v.approval:
		label = i18n.T("Request to join")
	}
	beginActions(e)
	dl.Action(i18n.T("Cancel"), rexui.ButtonDefault)
	k.BeginDisabled(joining)
	if dl.Action(label, rexui.ButtonPrimary) {
		if member {
			startChat(a, []byte(v.id), v.subject)
		} else {
			v.phase = inviteJoining
			cmd := a.Cmd(abi.CommandTagJoinInvite)
			cmd.JoinInvite.Code = []byte(v.code)
			a.Send(cmd)
			s.open = true
		}
	}
	k.EndDisabled()
	endActions(e)
}

// closeOnly is the actions row of a dialog with nothing to decide.
func (s *Screen) closeOnly(e *screenEnv, dl *rexui.Dialog) {
	beginActions(e)
	dl.Action(i18n.T("Close"), rexui.ButtonDefault)
	endActions(e)
}

// ---- sticker pack ----

type stickerPack struct {
	ready     bool
	name      string
	publisher string
	dir       string
	stickers  []string
}

func (p *stickerPack) reset() { *p = stickerPack{stickers: p.stickers[:0]} }

const packCols = 4

// stickerPack shows a sticker pack shared in a chat, and a button to add it
// (zf:src/ui/dialogs.rs:1055).
func (s *Screen) stickerPack(e *screenEnv, a *state.Account, dl *rexui.Dialog) {
	k, c, p := e.K, e.C(), &s.pack
	if !p.ready {
		k.NextGap(8)
		k.BeginContainer(ui.AxisX, ui.CrossCenter, "downloading", ui.Pct(1), ui.Children(1), 0)
		rexui.Spinner(k.Kit, i18n.T("Downloading the pack…")+"###downloading-spinner", rexui.SpinnerOpts{Size: 3})
		rexui.Label(k.Kit, i18n.T("Downloading the pack…"), rexui.LabelOpts{Style: rexui.TextCallout, Color: secondary})
		k.EndContainer()
		s.closeOnly(e, dl)
		return
	}
	if p.publisher != "" {
		rexui.Label(k.Kit, p.publisher, rexui.LabelOpts{Color: secondary})
	}
	const cell = 84
	rows := (len(p.stickers) + packCols - 1) / packCols
	k.NextHeight(k.Fixed(min(320, float32(rows)*cell+4)))
	v := k.BeginScroll("stickers", &s.scroll, widget.ScrollVertical)
	for r := 0; r < rows; r++ {
		k.BeginContainer(ui.AxisX, ui.CrossStart, c.Fmt().Str("row").Int(r).String(), ui.Pct(1), ui.Px(k.Pt(cell)), 0)
		for i := r * packCols; i < min((r+1)*packCols, len(p.stickers)); i++ {
			s.picture(e, uint64(i), p.stickers[i], cell-8, cell-8, 160)
		}
		k.EndContainer()
	}
	k.EndScroll(v)
	beginActions(e)
	dl.Action(i18n.T("Close"), rexui.ButtonDefault)
	if dl.Action(i18n.T("Add to my stickers"), rexui.ButtonPrimary) {
		cmd := a.Cmd(abi.CommandTagAddStickerPack)
		cmd.AddStickerPack.Dir, cmd.AddStickerPack.Name = []byte(p.dir), []byte(p.name)
		a.Send(cmd)
	}
	endActions(e)
}

// picture draws the image file at path into a box wpt × hpt points, decoded at
// decode pixels, contained; a track-coloured box until it is ready.
func (s *Screen) picture(e *screenEnv, n uint64, path string, wpt, hpt float32, decode int32) {
	k := e.K
	if s.imgs == nil {
		return
	}
	h, _ := s.imgs.Request(img.Key{Hash: img.HashString(path), W: decode, H: decode}, img.File(path))
	tex, uv, st := s.imgs.Use(h)
	c := e.C()
	k.NextSize(ui.Px(k.Pt(wpt)), ui.Px(k.Pt(hpt)))
	k.ApplySize(ui.Px(k.Pt(wpt)), ui.Px(k.Pt(hpt)))
	key := ui.KeyN(c.ParentKey(), 9100+n)
	if st == img.Ready {
		c.NextImageFit(tex, uv, ui.FitContain)
		c.NextTextColor(0xFFFFFFFF)
		c.BoxKeyed(ui.DrawImage, key, nil)
		return
	}
	c.NextBG(k.Theme().Colors[theme.Fill3])
	c.NextRadius(k.Pt(6))
	c.BoxKeyed(ui.DrawBackground, key, nil)
}
