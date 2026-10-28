package viewer

import (
	"bytes"
	"image"
	"image/png"
	"path/filepath"
	"sync"

	"gonativeui/platform"
	"gonativeui/rexui"
	"gonativeui/theme"
	"gonativeui/ui"

	"zapfast/abi"
	"zapfast/i18n"
	"zapfast/model"
	"zapfast/state"
	"zapfast/ui/screen"
)

// toolbar is the header: the chat's name, the actions that reach the message,
// the zoom controls and the ways out (image_preview.rs header).
func (s *Screen) toolbar(e *screen.Env, ch model.Chat) {
	c, k := e.C(), e.K
	title := model.S(ch.Name())
	if title == "" {
		if a := s.account(); a != nil {
			title = model.S(a.Model.DisplayName(ch.ID()))
		}
	}
	if title == "" && s.r.path != "" {
		title = filepath.Base(s.r.path)
	}
	if title == "" {
		title = i18n.T("Image")
	}
	tb := rexui.BeginToolbar(k.Kit, "Toolbar", rexui.ToolbarOpts{Style: theme.ToolbarRex, Title: title, Traffic: s.Traffic})
	tb.Title()
	hasFile, video := s.r.path != "", s.showsClip()

	if tb.Item(s.ic.reply, i18n.T("Reply")).Clicked {
		s.reply(e)
	}
	if tb.Item(s.ic.react, i18n.T("React")).Clicked {
		s.react(e)
	}
	if tb.Item(s.ic.forward, i18n.T("Forward")).Clicked {
		s.forward(e)
	}
	if hasFile {
		// Already on the computer: hand it to the save panel, as the message
		// menu does.
		if tb.Item(s.ic.save, i18n.T("Save as…")).Clicked {
			s.saveAs(e)
		}
	} else if tb.Item(s.ic.download, i18n.T("Download")).Clicked {
		s.download()
	}
	if tb.Item(s.ic.chat, i18n.T("Show in the chat")).Clicked {
		s.showInChat(e)
	}
	if hasFile && !video {
		tb.Space(8)
		if tb.Item(s.ic.minus, i18n.T("Zoom out")).Clicked {
			s.cmd = zoomOut
			c.RequestFrame()
		}
		s.zoomButton(e, &tb)
		if tb.Item(s.ic.plus, i18n.T("Zoom in")).Clicked {
			s.cmd = zoomIn
			c.RequestFrame()
		}
		tb.Space(8)
	}
	s.motionItem(e, &tb)
	if hasFile && !video {
		name := c.Fmt().Str(i18n.T("Copy image")).Str(" (⌘C)").String()
		if tb.Item(s.ic.copy, name).Clicked {
			s.copyImage(e)
		}
	}
	if hasFile {
		if tb.Item(s.ic.open, i18n.T("Open in another app")).Clicked {
			s.openExternal(e)
		}
	}
	full := e.A.Window() != nil && e.A.Window().Fullscreen()
	if full {
		if tb.Item(s.ic.exitFull, i18n.T("Exit Full Screen")).Clicked {
			e.A.Window().ToggleFullscreen()
		}
	} else if tb.Item(s.ic.fullscreen, i18n.T("Enter Full Screen")).Clicked {
		if w := e.A.Window(); w != nil {
			w.ToggleFullscreen()
		}
	}
	if tb.Item(s.ic.close, i18n.T("Close preview (Esc)")).Clicked {
		s.close(e)
	}
	tb.End()
}

// zoomButton is the one control that shows the scale and switches between
// fitting the window and the original size.
func (s *Screen) zoomButton(e *screen.Env, tb *rexui.Toolbar) {
	c, k := e.C(), e.K
	label := i18n.T("Fit")
	hint := i18n.T("Show at original size")
	if !s.v.fit {
		pct := int(s.v.zoom*100 + 0.5)
		label = c.Fmt().Int(pct).Str("%").String()
		hint = i18n.T("Fit to the window (0)")
	}
	tb.BeginCustom("zoom", ui.Px(e.Pt(60)))
	k.BeginContainer(ui.AxisX, ui.CrossCenter, "scale", ui.Pct(1), ui.Pct(1), 0)
	b := rexui.Button(k.Kit, label, rexui.ButtonOpts{Variant: rexui.ButtonPlain})
	rexui.Tooltip(k.Kit, b.ID, hint)
	k.EndContainer()
	tb.EndCustom()
	if b.Clicked {
		if s.v.fit {
			s.cmd = zoomActual
		} else {
			s.cmd = zoomFit
		}
		c.RequestFrame()
	}
}

// The ids of the picture's right-click menu.
const (
	menuCopy = iota + 1
	menuSave
	menuOpen
)

// pictureMenu is the native menu a right click on the picture opens: the
// three actions the header offers, as upstream's (image_preview.rs:589). A
// right click on the empty field opens nothing. It is OnContextMenu, with the
// request held back for the empty field.
func (s *Screen) pictureMenu(e *screen.Env, id ui.ID, sig ui.Signal) {
	c, k := e.C(), e.K
	in := c.Input()
	if sig.RightClicked && s.inMedia(in.MouseX, in.MouseY) {
		items := k.MenuScratch()
		*items = append(*items,
			ui.MenuItem{ID: menuCopy, Title: i18n.T("Copy image"), Symbol: "doc.on.doc"},
			ui.MenuItem{ID: menuSave, Title: i18n.T("Save as…"), Symbol: "square.and.arrow.down"},
			ui.MenuItem{ID: menuOpen, Title: i18n.T("Open in another app"), Symbol: "arrow.up.forward.square"})
		c.ContextMenu(id, in.MouseX, in.MouseY, *items)
	}
	item, out := c.ContextMenuResult(id)
	if out != ui.MenuChosen {
		return
	}
	switch item {
	case menuCopy:
		s.copyImage(e)
	case menuSave:
		s.saveAs(e)
	case menuOpen:
		s.openExternal(e)
	}
}

// ---- commands ----

// react opens the full reaction picker for the message, as upstream does: it
// closes the viewer, shows the message in the main window and opens the
// picker beside its bubble (the empty rect: the conversation knows where).
func (s *Screen) react(e *screen.Env) {
	a := s.account()
	if a == nil || len(s.cur.id) == 0 {
		return
	}
	a.OpenMessage(s.chatID, s.cur.id)
	e.S.OpenReaction(s.chat, s.cur.id, state.Rect{})
	e.S.RaiseMain()
	s.close(e)
}

// download fetches the attachment of the item on screen (Command::Download,
// asked for in the viewer).
func (s *Screen) download() {
	a := s.account()
	if a == nil || len(s.cur.id) == 0 {
		return
	}
	if s.dlOf().downloading {
		return
	}
	s.dl[string(s.cur.id)] = dlState{downloading: true}
	cmd := a.Cmd(abi.CommandTagDownload)
	cmd.Download.Manual = true
	cmd.Download.Chat, cmd.Download.Message = s.chatID, s.cur.id
	cmd.Download.Card, cmd.Download.HasCard = s.cur.card, s.cur.hasCard
	a.Send(cmd)
}

// saveAs hands the file to the core, which shows the save panel; like
// upstream it closes the viewer first.
func (s *Screen) saveAs(e *screen.Env) {
	a := s.account()
	if a == nil || s.r.path == "" {
		return
	}
	cmd := a.Cmd(abi.CommandTagSaveAttachmentAs)
	cmd.SaveAttachmentAs.Source = []byte(s.r.path)
	cmd.SaveAttachmentAs.Name = []byte(filepath.Base(s.r.path))
	a.Send(cmd)
	s.close(e)
}

// forward opens the forward dialog on the message and leaves the viewer.
func (s *Screen) forward(e *screen.Env) {
	d := state.Dialog{Kind: state.DialogForward, Chat: s.chat}
	d.IDs.Add(s.cur.id)
	e.S.OpenDialog(d)
	e.S.Wake()
	e.S.RaiseMain()
	s.close(e)
}

// reply quotes the message in the composer and leaves the viewer.
func (s *Screen) reply(e *screen.Env) {
	st := e.S
	if a := s.account(); a != nil {
		a.OpenChat(s.chat)
	}
	st.Panes.Reply.Reset()
	st.Panes.Reply.Add(s.cur.id)
	st.Panes.Edit.Reset()
	st.Req.FocusComposer = true
	st.Touch()
	st.Wake()
	st.RaiseMain()
	s.close(e)
}

// showInChat jumps to the message in the main window and leaves the viewer.
func (s *Screen) showInChat(e *screen.Env) {
	if a := s.account(); a != nil {
		a.OpenMessage(s.chatID, s.cur.id)
	}
	e.S.RaiseMain()
	s.close(e)
}

// openExternal opens the file in the system's default application, off the
// main thread.
func (s *Screen) openExternal(e *screen.Env) { s.openPath(e, s.r.path) }

// openPath opens path in the system's default application, through the
// state's one checked route.
func (s *Screen) openPath(e *screen.Env, path string) { e.S.OpenFile(path) }

// ---- copy image ----

// copyImage asks the core to decode the photo for the clipboard; the answer
// arrives as a ClipboardImage event (VW-003: never a clip).
func (s *Screen) copyImage(e *screen.Env) {
	a := s.account()
	if a == nil || s.r.path == "" || s.isVideo() || s.copyPending {
		return
	}
	s.copyPending = true
	cmd := a.Cmd(abi.CommandTagPrepareClipboardImage)
	cmd.PrepareClipboardImage.Path = []byte(s.r.path)
	a.Send(cmd)
}

// clipboardPNG is the hand-off of the encoded picture from a worker to the
// frame, which owns the pasteboard (main thread).
type clipboardPNG struct {
	mu   sync.Mutex
	png  []byte
	fail bool
	set  bool
}

func (c *clipboardPNG) reset() {
	c.mu.Lock()
	c.png, c.fail, c.set = nil, false, false
	c.mu.Unlock()
}

func (c *clipboardPNG) put(png []byte, fail bool) {
	c.mu.Lock()
	c.png, c.fail, c.set = png, fail, true
	c.mu.Unlock()
}

func (c *clipboardPNG) take() (png []byte, fail, ok bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if !c.set {
		return nil, false, false
	}
	png, fail, ok = c.png, c.fail, true
	c.png, c.fail, c.set = nil, false, false
	return
}

// onClipboard takes the core's answer: the decoded picture's pixels are a
// blob; a worker reads and encodes them.
func (s *Screen) onClipboard(a *state.Account, r *abi.EventClipboardImage) {
	if !s.copyPending {
		return
	}
	if r.Result.Tag != abi.ClipboardImageResultTagOk {
		s.clipPNG.put(nil, true)
		s.wake()
		return
	}
	im, conn := r.Result.Ok.Image, a.Conn
	go func() {
		pix := readBlob(conn, im.RGBA)
		data, ok := encodePNG(int(im.Width), int(im.Height), pix)
		s.clipPNG.put(data, !ok)
		s.wake()
	}()
}

// setClipboardImage puts a PNG on the system clipboard (main thread); the
// tests replace it.
var setClipboardImage = func(png []byte) error { return platform.SetClipboardImage(png) }

// finishClipboard writes the picture a worker prepared to the system
// clipboard, and says how it went.
func (s *Screen) finishClipboard(e *screen.Env) {
	png, fail, ok := s.clipPNG.take()
	if !ok {
		return
	}
	s.copyPending = false
	if !fail {
		if err := setClipboardImage(png); err != nil {
			fail = true
		}
	}
	if fail {
		e.S.Toast([]byte(i18n.T("Failed to copy image")), true)
	} else {
		e.S.Toast([]byte(i18n.T("Copied image")), false)
	}
	e.S.Wake()
}

// encodePNG makes a PNG of straight RGBA pixels.
func encodePNG(w, h int, rgba []byte) ([]byte, bool) {
	if w <= 0 || h <= 0 || len(rgba) < w*h*4 {
		return nil, false
	}
	m := &image.NRGBA{Pix: rgba[:w*h*4], Stride: w * 4, Rect: image.Rect(0, 0, w, h)}
	var b bytes.Buffer
	if err := png.Encode(&b, m); err != nil {
		return nil, false
	}
	return b.Bytes(), true
}
