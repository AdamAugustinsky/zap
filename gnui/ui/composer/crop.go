package composer

import (
	"path/filepath"
	"strings"

	"gonativeui/theme"
	"gonativeui/ui"
	"gonativeui/widget/base"

	"zapfast/abi"
	"zapfast/state"
	"zapfast/ui/screen"
)

// Cropping a staged picture before it goes (zf:src/app.rs:7003,
// Action::EditPicture; zf:src/ui/conversation.rs:10989): a click on a
// picture's chip opens the cropper (ui/cropper, state.DialogCropper) on it,
// and the file the core writes (PictureEdited) takes the attachment's place.

// cropState keys the buttons laid over the croppable chips.
type cropState struct {
	key ui.Key
}

func (cs *cropState) init() {
	cs.key = ui.HashString(0, "zapfast composer crop")
}

// isPictureFile: a staged file the cropper can open (upstream asks whether
// its type is an image; ui/cropper's list).
func isPictureFile(path []byte) bool {
	switch strings.ToLower(strings.TrimPrefix(filepath.Ext(string(path)), ".")) {
	case "png", "jpg", "jpeg", "jpe", "gif", "webp", "bmp", "tif", "tiff", "heic", "heif", "avif", "ico":
		return true
	}
	return false
}

// croppable: a picture can be cropped before it goes; a document goes as
// the file it is, and a picture still being read is not yet anything.
func croppable(st *state.Staged) bool {
	switch st.Kind {
	case state.StagedPicture:
		return st.Ready
	case state.StagedFile:
		return isPictureFile(st.Path)
	}
	return false
}

// chipKeys are the keys rexui gives attachment id's chip in the composer
// whose field box has key field, its remove button, and the row they sit in
// (rexui/composer.go composerAttachments). rexui reports only the remove
// button's press, so the composer lays its own button over the chip.
func chipKeys(field ui.Key, id uint64) (chip, remove, row ui.Key) {
	row = ui.KeyN(field, 0xA7)
	chip = ui.KeyN(ui.KeyN(row, 0xA8), id)
	return chip, ui.KeyN(chip, 2), row
}

// chipButtons lays a button over each croppable attachment's chip, up to
// its remove button, where last frame's layout put it: a click crops the
// picture, and hovering shows the pencil. At the window's root, after the
// band.
//
// The buttons follow the chips a frame late. While the chips still move (a
// chip just staged, the band growing under it), the framework's settle
// frames (ui.Context.Settling: the RectOf reads here, and the band's in
// Height) rebuild them, so that the window never rests with a button beside
// its chip, where a click on the picture would miss it.
func (s *Screen) chipButtons(e *screen.Env, field ui.ID) {
	k := e.K
	c := e.C()
	fk := c.KeyOf(field)
	r := k.Theme()
	for i := range e.S.Staged {
		st := &e.S.Staged[i]
		if !croppable(st) {
			continue
		}
		ck, rk, rowK := chipKeys(fk, st.ID)
		cr, row := c.RectOf(ck), c.RectOf(rowK)
		x0, y0, x1, y1 := max(cr[0], row[0]), max(cr[1], row[1]), min(cr[2], row[2]), min(cr[3], row[3])
		if rm := c.RectOf(rk); rm[2] > rm[0] {
			x1 = min(x1, rm[0])
		}
		if x1 <= x0 || y1 <= y0 {
			continue
		}
		key := ui.KeyN(s.crop.key, st.ID)
		v := k.VisualOf(key, base.HoverOn)
		theme.At(c, x0, y0, x1-x0, y1-y0)
		c.NextRadius((cr[3] - cr[1]) / 2)
		c.NextBG(theme.WithAlpha(r.Colors[theme.Fill2], v.Hot))
		c.NextAxis(ui.AxisX)
		c.NextCross(ui.CrossCenter)
		c.NextJustify(ui.JustifyCenter)
		c.NextRole(ui.RoleButton)
		c.NextSemName(st.Name)
		id := c.BoxKeyed(base.Floating|ui.DrawBackground|k.InteractFlags(base.HoverOn), key, nil)
		if v.Hot > 0 {
			c.Push(id)
			k.Icon(s.ic.pencil, theme.WithAlpha(r.Colors[theme.Label], v.Hot))
			c.Pop()
		}
		if k.SignalOf(id).Clicked {
			s.cropStaged(e, st)
			return
		}
	}
}

// cropStaged opens the cropper on a staged picture. The cropper finds it by
// ID (State.StagedByID): a file by its path, a pasted picture by the pixels
// in its blob, which stay there until the attachment goes (freeStaged).
func (s *Screen) cropStaged(e *screen.Env, st *state.Staged) {
	e.S.OpenDialog(state.Dialog{Kind: state.DialogCropper, N: int(st.ID)})
}

// edited puts the cropped file in its attachment's place, while that
// attachment is still staged in the account on screen: a result for one
// removed meanwhile, or after another chat or account took the composer,
// is dropped (zf:src/app.rs:3941). The cropper says why one failed.
func (s *Screen) edited(a *state.Account, p *abi.EventPictureEdited) {
	if !a.Active() || a != s.acct || p.Result.Tag != abi.PathResultTagOk {
		return
	}
	st := s.state.StagedByID(p.Target)
	if st == nil {
		return
	}
	freeStaged(a, st)
	st.Kind, st.Path = state.StagedFile, append(st.Path[:0], p.Result.Ok.Path...)
	st.W, st.H, st.Blob, st.Ready, st.Failed = 0, 0, abi.BlobRef{}, false, false
	s.state.Touch()
}
