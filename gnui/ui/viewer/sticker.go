package viewer

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"gonativeui/img"
	"gonativeui/rexui"
	"gonativeui/theme"
	"gonativeui/ui"
	"gonativeui/widget/base"

	"zapfast/abi"
	"zapfast/i18n"
	"zapfast/model"
	"zapfast/state"
	"zapfast/ui/screen"
)

// The sticker view (image_preview.rs show_sticker): a sticker opened from its
// bubble is shown large on a square card, with Add to or Remove from
// favorites under it. A click on the sticker, outside it, or Escape closes.
// There is no album, no zoom and no header but the way out: it is not a
// photograph.
const (
	stickerSide   = 300 // the sticker's side at most (STICKER_VIEW_SIDE), points
	stickerRoom   = 110 // kept below it for the button and the card's margins
	stickerMargin = 20
)

// sticker is the state of the sticker view.
type sticker struct {
	on      bool   // the item on screen is a sticker
	checked bool   // the message was looked at for this item and conversation
	gen     uint32 // the conversation's generation it was looked at in
	path    string // the sticker's file
	hash    uint64 // path's image key hash
	hr      *hashResult
	hashed  string // the file's content hash, which names its favorite copy

	// The favorites the core listed (a batch of Stickers events), and the
	// favorite copy of this sticker among them.
	saved    []string
	batch    uint32
	hasBatch bool
	dirty    bool // saved or hashed changed: look for the favorite copy again
	fav      bool
	favPath  string
}

// hashResult is a worker's answer: the content hash of a sticker's file,
// read off the main thread.
type hashResult struct {
	mu   sync.Mutex
	path string
	hash string
	done bool
}

func (s *Screen) resetSticker() {
	saved := s.stk.saved[:0]
	s.stk = sticker{saved: saved}
}

// checkSticker looks at the message on screen: a sticker puts the window in
// the sticker view. The conversation holds the message (it opened it); one
// that arrives later is looked at again.
func (s *Screen) checkSticker(a *state.Account) {
	conv := a.Model.Conversation(s.chatID)
	if conv == nil || s.cur.hasCard {
		return
	}
	k := &s.stk
	if k.checked && conv.Gen() == k.gen {
		return
	}
	k.checked, k.gen = true, conv.Gen()
	i := conv.Find(s.cur.id)
	if i < 0 || conv.At(i).Kind() != abi.ContentTagSticker {
		if k.on {
			k.on = false
			s.show()
		}
		return
	}
	var path string
	if md, ok := conv.At(i).Media(); ok {
		path = string(md.Path)
	}
	if k.on && path == k.path {
		return
	}
	first := !k.on
	k.on, k.path, k.hash = true, path, img.HashString(path)
	k.hashed, k.fav, k.favPath, k.dirty = "", false, "", true
	k.hr = nil
	if path != "" {
		hr := &hashResult{path: path}
		k.hr = hr
		wake := s.wake
		go func() {
			var h string
			if b, err := os.ReadFile(path); err == nil {
				sum := sha256.Sum256(b)
				h = hex.EncodeToString(sum[:])
			}
			hr.mu.Lock()
			hr.hash, hr.done = h, true
			hr.mu.Unlock()
			wake()
		}()
	}
	if first {
		// The favorites are listed by the core on request.
		a.Send(a.Cmd(abi.CommandTagRecentStickers))
	}
	s.pic = picture{}
}

// onStickers takes a list of the core's stickers; only the favorites matter.
func (s *Screen) onStickers(e *abi.EventStickers) {
	k := &s.stk
	if !k.on {
		return
	}
	if !k.hasBatch || e.Batch != k.batch {
		k.saved, k.batch, k.hasBatch = k.saved[:0], e.Batch, true
	}
	for i := range e.Entries {
		if en := &e.Entries[i]; en.Tag == abi.StickerEntryTagFavorite {
			k.saved = append(k.saved, string(en.Favorite.Path))
		}
	}
	if e.Last {
		k.dirty = true
		s.wake()
	}
}

// refreshFavorite finds the favorite copy of the sticker: the saved file
// named by its content hash. It runs when the hash or the list changed.
func (s *Screen) refreshFavorite() {
	k := &s.stk
	if hr := k.hr; hr != nil {
		hr.mu.Lock()
		if hr.done && hr.path == k.path {
			k.hashed, k.hr = hr.hash, nil
			k.dirty = true
		}
		hr.mu.Unlock()
	}
	if !k.dirty || k.hr != nil {
		return
	}
	k.dirty = false
	k.fav, k.favPath = false, ""
	if k.hashed == "" || !k.hasBatch {
		return
	}
	for _, p := range k.saved {
		base := filepath.Base(p)
		if strings.TrimSuffix(base, filepath.Ext(base)) == k.hashed {
			k.fav, k.favPath = true, p
			return
		}
	}
}

// toggleFavorite adds the sticker to the favorites, or takes it out
// (Action::SaveSticker, Action::ForgetSticker).
func (s *Screen) toggleFavorite(e *screen.Env) {
	a := s.account()
	k := &s.stk
	if a == nil || k.path == "" {
		return
	}
	if k.fav {
		if k.favPath != "" {
			cmd := a.Cmd(abi.CommandTagForgetSticker)
			cmd.ForgetSticker.Path = []byte(k.favPath)
			a.Send(cmd)
		}
		k.fav, k.favPath = false, ""
		return
	}
	cmd := a.Cmd(abi.CommandTagSaveSticker)
	cmd.SaveSticker.Path = []byte(k.path)
	a.Send(cmd)
	// The core's favorites list supplies the saved copy's path for Remove.
	e.S.Toast([]byte(i18n.T("Added to favorites")), false)
}

// stickerWindow builds the window in the sticker view.
func (s *Screen) stickerWindow(e *screen.Env, ch model.Chat) {
	c, k := e.C(), e.K
	w, h := c.Size()
	W, H := float32(w), float32(h)
	r := k.Theme()
	tbH := theme.Snap(r.Window.Toolbars[theme.ToolbarRex].Height)
	s.refreshFavorite()

	c.NextBG(r.Colors[theme.Field])
	theme.At(c, 0, 0, W, H)
	c.Push(c.Box(base.Floating|ui.DrawBackground, "viewer"))
	if !c.ModalOpen() && c.TakeKey(ui.KeyEscape, 0) {
		s.close(e)
		c.Pop()
		return
	}
	s.stickerStage(e, screen.Rect{X: 0, Y: tbH, W: W, H: H - tbH})
	s.stickerToolbar(e, ch)
	c.Pop()
}

// stickerStage is the field under the toolbar: the card, and the click that
// closes whatever it lands on but the button.
func (s *Screen) stickerStage(e *screen.Env, r screen.Rect) {
	c, k := e.C(), e.K
	th := k.Theme()
	theme.At(c, r.X, r.Y, r.W, r.H)
	c.NextRole(ui.RoleGroup)
	c.NextSemName(i18n.T("Sticker"))
	id := c.Box(base.Floating|ui.Clickable|ui.Clip, "stage")
	c.Push(id)
	defer c.Pop()
	stk := &s.stk

	side := min(r.H-e.Pt(stickerRoom), r.W-e.Pt(48))
	side = max(min(side, e.Pt(stickerSide)), e.Pt(64))
	pad, gap, bh := e.Pt(stickerMargin), e.Pt(16), e.Pt(28)
	cw, ch := side+2*pad, side+2*pad+gap+bh
	x, y := (r.W-cw)/2, (r.H-ch)/2
	s.mediaRect = [4]float32{r.X + x, r.Y + y, r.X + x + cw, r.Y + y + ch}

	theme.At(c, x, y, cw, ch)
	c.NextBG(th.Colors[theme.Raised])
	c.NextBorder(th.Colors[theme.Separator], 1)
	c.NextRadius(e.Pt(14))
	c.NextAxis(ui.AxisY)
	c.NextCross(ui.CrossCenter)
	c.NextPadding(pad, pad, pad, pad)
	c.NextGap(gap)
	c.Push(c.Box(base.Floating|ui.DrawBackground, "card"))

	var hd img.Handle
	if stk.path != "" && s.cache != nil {
		px := int32(side)
		hd, _ = s.cache.Request(img.Key{Hash: stk.hash, W: px, H: px, Flags: img.Animate}, img.File(stk.path))
	}
	k.NextSize(ui.Px(side), ui.Px(side))
	k.Image(1, hd, ui.FitContain, 0, i18n.T("Sticker"))

	label, ic := i18n.T("Add to favorites"), s.ic.star
	if stk.fav {
		label, ic = i18n.T("Remove from favorites"), s.ic.starOff
	}
	if rexui.ButtonIcon(k.Kit, ic, label, rexui.ButtonOpts{}).Clicked {
		s.toggleFavorite(e)
	}
	c.Pop()
	if c.Signal(id).Clicked {
		s.close(e)
	}
}

// stickerToolbar is the header of the sticker view: the chat's name and the
// way out.
func (s *Screen) stickerToolbar(e *screen.Env, ch model.Chat) {
	k := e.K
	title := model.S(ch.Name())
	if title == "" {
		title = i18n.T("Sticker")
	}
	tb := rexui.BeginToolbar(k.Kit, "Toolbar", rexui.ToolbarOpts{Style: theme.ToolbarRex, Title: title, Traffic: s.Traffic})
	tb.Title()
	if tb.Item(s.ic.close, i18n.T("Close preview (Esc)")).Clicked {
		s.close(e)
	}
	tb.End()
}
