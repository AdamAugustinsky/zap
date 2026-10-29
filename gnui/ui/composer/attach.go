package composer

import (
	"bytes"
	"image"
	"image/draw"
	"image/png"
	"log/slog"
	"path/filepath"

	"gonativeui/app"
	"gonativeui/platform"
	"gonativeui/rexui"
	"gonativeui/theme"
	"gonativeui/ui"
	"gonativeui/widget/base"

	"zapfast/abi"
	"zapfast/core"
	"zapfast/i18n"
	"zapfast/model"
	"zapfast/state"
	"zapfast/ui/screen"
)

// Attachments staged in the composer (zf:src/app.rs:892-990): files and
// documents by path, pictures from the clipboard or a drop as straight-alpha
// RGBA in a core blob. The open chat's are state.Staged (the cropper reaches
// them by ID); another chat's wait in its aside.

// canStage: pasting or dropping stages nothing while a menu, the picker, a
// recording or the full-size viewer is open (zf:src/app.rs:9212).
func (s *Screen) canStage(e *screen.Env) bool {
	return !s.tools && !e.K.MenuOpen() && e.S.Panes.Picker == state.PickerNone && !s.voice.recording() && !e.S.Win.Open[state.WinViewer]
}

// newStaged is a file attachment of kind with a fresh ID.
func (s *Screen) newStaged(kind state.StagedKind, path []byte) state.Staged {
	s.nextID++
	return state.Staged{ID: s.nextID, Kind: kind, Path: append([]byte(nil), path...), Name: filepath.Base(string(path))}
}

// stage adds attachments of kind to the composer.
func (s *Screen) stage(e *screen.Env, kind state.StagedKind, paths ...[]byte) {
	for _, p := range paths {
		e.S.Staged = append(e.S.Staged, s.newStaged(kind, p))
	}
	if len(paths) > 0 {
		s.st.Focus(e.K.Kit)
		e.S.Touch()
	}
}

// stagePathsTo stages files a dialog picked for chat: in the composer when
// it is the open chat, else in that chat's aside.
func (s *Screen) stagePathsTo(a *state.Account, chat []byte, paths [][]byte, kind state.StagedKind) {
	if a == s.acct && bytes.Equal(chat, s.chat) {
		s.pendingPicks = append(s.pendingPicks, pick{kind: kind})
		pk := &s.pendingPicks[len(s.pendingPicks)-1]
		for _, p := range paths {
			pk.paths = append(pk.paths, append([]byte(nil), p...))
		}
		return
	}
	as := s.asides.get(a, chat, true)
	for _, p := range paths {
		as.staged = append(as.staged, s.newStaged(kind, p))
	}
}

// pick is files a dialog picked for the open chat, staged at the next frame.
type pick struct {
	kind  state.StagedKind
	paths [][]byte
}

// unstage removes attachment i.
func (s *Screen) unstage(a *state.Account, i int) {
	list := &s.state.Staged
	delete(s.paste.failed, (*list)[i].ID)
	freeStaged(a, &(*list)[i])
	*list = append((*list)[:i], (*list)[i+1:]...)
	if len(*list) == 0 {
		s.paste.sendQueued = false
	}
}

// freeStaged gives a staged picture's pixels back to the core.
func freeStaged(a *state.Account, st *state.Staged) {
	if st.Kind == state.StagedPicture && st.Ready && a != nil && a.Conn != nil {
		a.Conn.FreeBlob(st.Blob.ID)
		st.Ready = false
	}
}

// clearStaged removes every attachment (Escape).
func (s *Screen) clearStaged(a *state.Account) {
	for i := len(s.state.Staged) - 1; i >= 0; i-- {
		s.unstage(a, i)
	}
}

// picturesReady: no staged picture is still being read.
func (s *Screen) picturesReady() bool {
	for i := range s.state.Staged {
		if st := &s.state.Staged[i]; st.Kind == state.StagedPicture && !st.Ready && !st.Failed {
			return false
		}
	}
	return true
}

// pasteState is a paste of a picture or files on its way: the clipboard's
// answer, then the worker reading the picture.
type pasteState struct {
	tag        uint64
	done       chan picture
	failed     map[uint64]picture // original PNG and error, until retry or removal
	sendQueued bool               // a send waits for the pictures
}

// picture is a worker's answer for a staged picture, whose blob is conn's.
type picture struct {
	acct state.AcctRef
	id   uint64
	w, h uint32
	blob abi.BlobRef
	ok   bool
	conn core.Conn
	data []byte // retained only if preparation failed
	err  error
}

const pasteTag = 0x7a66_636f_6d70 // "zfcomp": no text field's key

func (p *pasteState) init() {
	p.tag = pasteTag
	if p.done == nil {
		p.done = make(chan picture, 16) // workers outlive their window
	}
}

// requestPaste asks the clipboard for the picture or the files the editor
// would not paste as text (⌘V of an image).
func (s *Screen) requestPaste(e *screen.Env) {
	if !s.canStage(e) {
		return
	}
	e.A.RequestPaste(s.paste.tag, platform.WantImage|platform.WantFiles)
}

// takePaste stages what the clipboard answered, the pictures the workers
// have read and the files a dialog picked.
func (s *Screen) takePaste(e *screen.Env) {
	for {
		select {
		case p := <-s.paste.done:
			s.pictureRead(p)
			continue
		default:
		}
		break
	}
	for i := range s.pendingPicks {
		pk := &s.pendingPicks[i]
		s.stage(e, pk.kind, pk.paths...)
	}
	s.pendingPicks = s.pendingPicks[:0]
	if p, ok := e.A.TakePaste(); ok && p.Tag == s.paste.tag {
		s.pasted(e, p)
	}
}

// pasted stages a paste's files or picture.
func (s *Screen) pasted(e *screen.Env, p app.Paste) {
	switch {
	case p.Failed:
		// Only the reason: never the paths or the pixels (zf:src/app.rs:9267).
		slog.Info("zapfast: Ctrl+V found nothing to attach", "too big", p.TooBig)
	case p.Kind == platform.PasteFiles:
		for _, path := range platform.PasteFilePaths(p.Data) {
			s.stage(e, state.StagedFile, []byte(path))
		}
	case p.Kind == platform.PasteImage:
		s.stagePNG(e, p.Data)
	}
}

// stagePNG stages a picture (a PNG file's bytes, copied): a worker reads
// its pixels into a core blob while it waits in the row.
func (s *Screen) stagePNG(e *screen.Env, data []byte) {
	a := s.acct
	if a == nil {
		return
	}
	s.nextID++
	id := s.nextID
	e.S.Staged = append(e.S.Staged, state.Staged{ID: id, Kind: state.StagedPicture, Name: i18n.T("Photo")})
	s.readPNG(e, id, append([]byte(nil), data...))
	s.st.Focus(e.K.Kit)
	e.S.Touch()
}

// readPNG prepares owned PNG bytes, also used when Send retries a failed input.
func (s *Screen) readPNG(e *screen.Env, id uint64, data []byte) {
	a := s.acct
	conn, done, wake := a.Conn, s.paste.done, e.S.Wake
	p := picture{id: id, acct: a.Ref(), conn: conn}
	if conn == nil {
		p.data, p.err = data, state.ErrNoCore
		s.pictureRead(p)
		return
	}
	go func() {
		img, err := png.Decode(bytes.NewReader(data))
		p.err = err
		if err == nil {
			b := img.Bounds()
			rgba, ok := img.(*image.NRGBA)
			if !ok || rgba.Stride != 4*b.Dx() {
				rgba = image.NewNRGBA(image.Rect(0, 0, b.Dx(), b.Dy()))
				draw.Draw(rgba, rgba.Bounds(), img, b.Min, draw.Src)
			}
			p.blob, p.err = conn.PutBlob(rgba.Pix)
			p.w, p.h, p.ok = uint32(b.Dx()), uint32(b.Dy()), p.err == nil
		}
		if !p.ok {
			p.data = data
		}
		done <- p
		wake()
	}()
}

// pictureRead puts a worker's answer on its attachment, here or in the
// aside of the chat it was staged in; one that was removed meanwhile gives
// its blob back.
func (s *Screen) pictureRead(p picture) {
	if !p.acct.Valid() {
		if p.ok {
			p.conn.FreeBlob(p.blob.ID)
		}
		return
	}
	st := s.state.StagedByID(p.id)
	if st == nil {
		st = s.asides.staged(p.id)
	}
	switch {
	case st == nil:
		if p.ok {
			p.conn.FreeBlob(p.blob.ID)
		}
	case p.ok:
		st.W, st.H, st.Blob, st.Ready = p.w, p.h, p.blob, true
		st.Failed, st.Name = false, i18n.T("Photo")
		delete(s.paste.failed, p.id)
	default:
		st.Failed = true
		st.Name = i18n.T("Could not use this image") + ": " + p.err.Error()
		if s.paste.failed == nil {
			s.paste.failed = make(map[uint64]picture)
		}
		s.paste.failed[p.id] = p
		if s.state.StagedByID(p.id) != nil {
			s.paste.sendQueued = false // retry only on another explicit Send
		}
		s.state.Toast([]byte(st.Name), true)
	}
}

// The plus menu (zf:src/ui/conversation.rs:2009).
var toolNames = [4]string{"Send files", "Send as document", "Send location", "Create poll"}

// toolLabels are the plus menu's items in the interface language.
func toolLabels(dst *[4]string) {
	for i, n := range toolNames {
		dst[i] = i18n.T(n)
	}
}

// tool runs item i of the plus menu.
func (s *Screen) tool(e *screen.Env, a *state.Account, ch model.Chat, i int) {
	s.tools = false
	switch i {
	case 0, 1:
		cmd := a.Cmd(abi.CommandTagPickFiles)
		cmd.PickFiles.Chat, cmd.PickFiles.Documents = s.chat, i == 1
		a.Send(cmd)
	case 2:
		e.S.OpenDialog(state.Dialog{Kind: state.DialogSendLocation, Chat: ch.Ref()})
	case 3:
		e.S.OpenDialog(state.Dialog{Kind: state.DialogCreatePoll, Chat: ch.Ref()})
	}
	e.S.Touch()
}

// toolsMenu is the drawn plus menu, where the window has no native menus
// (offscreen): a popover of the items above the plus.
func (s *Screen) toolsMenu(e *screen.Env, a *state.Account, ch model.Chat) {
	k := e.K
	toolLabels(&s.toolLabels)
	pop := rexui.BeginPopover(k.Kit, i18n.T("Attach")+"###composer tools", s.plusRect, rexui.PopoverOpts{Edge: rexui.PopoverAbove, Width: 190, Label: i18n.T("Attach")})
	pick := -1
	for i, n := range s.toolLabels {
		k.NextWidth(ui.Pct(1))
		if rexui.Button(k.Kit, n, rexui.ButtonOpts{Variant: rexui.ButtonPlain}).Clicked {
			pick = i
		}
	}
	rexui.EndPopover(k.Kit, &pop)
	if pop.Dismissed {
		s.tools = false
		e.S.Touch()
	}
	if pick >= 0 {
		s.tool(e, a, ch, pick)
	}
}

// dropState is the drop target over the conversation: files and pictures
// dropped on the open chat are staged (zf:src/app.rs:9212, zf:src/ui/mod.rs:468).
type dropState struct {
	key   ui.Key
	label string
	for_  []byte
	paths []string
}

// build declares the drop target over the conversation and its band, and
// shows where a drag will go while it is over it.
func (d *dropState) build(s *Screen, e *screen.Env, r screen.Rect, ch model.Chat) {
	if !canSend(&ch) {
		return
	}
	k := e.K
	c := e.C()
	if d.key == 0 {
		d.key = ui.HashString(0, "zapfast composer drop")
	}
	top := theme.Snap(k.Theme().Window.Toolbars[theme.ToolbarRex].Height)
	theme.At(c, r.X, top, r.W, r.Y+r.H-top)
	c.NextAxis(ui.AxisY)
	c.NextCross(ui.CrossCenter)
	c.NextJustify(ui.JustifyCenter)
	id := c.BoxKeyed(base.Floating, d.key, nil)
	c.AcceptDrop(id, ui.DropSpec{Kinds: ui.DropFiles | ui.DropImage, Ops: ui.DropCopy})
	sig := c.DropSignal(id)
	if sig.Dropped {
		s.dropped(e, c.Drop())
	}
	if !sig.Over {
		return
	}
	name := ch.Name()
	if len(name) == 0 {
		name = s.acct.Model.DisplayName(ch.ID())
	}
	if !bytes.Equal(name, d.for_) || d.label == "" {
		d.for_ = append(d.for_[:0], name...)
		d.label = i18n.Format(i18n.T("Drop to send to {name}"), "{name}", string(name))
	}
	rr := k.Theme()
	c.Push(id)
	c.NextBG(rr.Colors[theme.Raised])
	c.NextBorder(rr.Colors[theme.Accent], e.Pt(2))
	c.NextRadius(e.Pt(14))
	c.NextAxis(ui.AxisY)
	c.NextCross(ui.CrossCenter)
	c.NextGap(e.Pt(8))
	c.NextPadding(e.Pt(28), e.Pt(20), e.Pt(28), e.Pt(20))
	c.NextSize(ui.Children(1), ui.Children(1))
	c.NextRole(ui.RoleGroup)
	c.NextSemName(d.label)
	c.Push(c.Box(ui.DrawBackground|ui.DrawBorder, "drop card"))
	k.Icon(s.ic.clip, rr.Colors[theme.Accent])
	rexui.Label(k.Kit, d.label, rexui.LabelOpts{Style: rexui.TextTitle3})
	c.Pop()
	c.Pop()
}

// dropped stages what was dropped on the chat: files, or a picture.
func (s *Screen) dropped(e *screen.Env, data ui.DropData) {
	if !s.canStage(e) || data.Failed {
		return
	}
	d := &s.drop
	switch data.Kind {
	case ui.DropFiles:
		d.paths = data.Paths(d.paths[:0])
		for _, p := range d.paths {
			s.stage(e, state.StagedFile, []byte(p))
		}
	case ui.DropImage:
		s.stagePNG(e, data.Data)
	}
}
