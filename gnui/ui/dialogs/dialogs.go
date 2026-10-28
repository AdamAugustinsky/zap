package dialogs

import (
	"hash/maphash"

	"gonativeui/app"
	"gonativeui/icon"
	"gonativeui/img"
	"gonativeui/rexui"
	"gonativeui/theme"
	"gonativeui/ui"
	"gonativeui/widget"

	"zapfast/abi"
	"zapfast/i18n"
	"zapfast/model"
	"zapfast/state"
	"zapfast/ui/screen"
)

// Screen draws the open dialog (state.Dialog). Every dialog of upstream's
// Dialog enum is here except the cropper's and the sticker maker's: each
// keeps its own state in the fields below, cleared when it opens (reset).
type Screen struct {
	open  bool
	cur   uint64 // identity of the dialog on screen (0: none)
	kind  state.DialogKind
	alert alerts // the native alert a question is shown in, if any
	// age counts the frames since the dialog opened: the first ones place
	// the keyboard focus.
	age int

	st   *state.State
	app  *app.App // the window's app: the lock withdraws its alert
	imgs *img.Cache
	msg  abi.Message

	// text is the dialog's prepared strings (built when it opens, so a frame
	// formats nothing).
	title string
	paras [3]string
	verb  string
	tbuf  []byte // a time being formatted for a label

	fwd     forward
	chat    newChat
	contact newContact
	pair    pairing
	unlock  unlock
	poll    pollDraft
	loc     location
	lab     labels
	inv     invite
	pack    stickerPack
	scroll  widget.ScrollState
}

var _ screen.Overlay = (*Screen)(nil)

// screenEnv is the Env every screen is given.
type screenEnv = screen.Env

// winPt is the window's size in points.
func winPt(e *screenEnv) (w, h float32) {
	pw, ph := e.C().Size()
	sc := e.K.Scale()
	return float32(pw) / sc, float32(ph) / sc
}

// New returns the dialogs.
func New() *Screen { return &Screen{} }

// Init readies the dialogs for a window: its listeners for the answers the
// core gives to a dialog's commands.
func (s *Screen) Init(e *screen.Env) error {
	s.st = e.S
	s.app = e.A
	// The lock screen builds no dialog, so the system's alert of a question
	// (naming a contact, a group, a chat) is withdrawn here, not in Build; the
	// question is asked again after the unlock.
	e.S.OnLock(s, func(*state.State) { s.withdrawAlert() })
	e.S.OnAccountGone(&s.lab, s.lab.gone)
	s.poll.removeIc = e.K.AddIcon(icon.Spec{Symbol: "xmark", Fallback: '✕', Pt: 12})
	s.lab.iconEdit = e.K.AddIcon(icon.Spec{Symbol: "pencil", Fallback: '✎', Pt: 13})
	s.lab.iconTrash = e.K.AddIcon(icon.Spec{Symbol: "trash", Fallback: '🗑', Pt: 13})
	s.fwd.search.Reserve(256)
	s.chat.search.Reserve(256)
	s.contact.phone.Reserve(64)
	s.contact.first.Reserve(256)
	s.contact.last.Reserve(256)
	s.pair.phone.Reserve(64)
	s.poll.question.Reserve(1024)
	for i := range s.poll.options {
		s.poll.options[i].Reserve(512)
	}
	s.loc.text.Reserve(2048)
	s.lab.name.Reserve(256)
	s.lab.rename.Reserve(256)
	e.S.Listen((*listener)(s),
		abi.EventTagChats, abi.EventTagChatUpdated, abi.EventTagLink, abi.EventTagAccountRemoved,
		abi.EventTagPollCreated, abi.EventTagInvitePreview, abi.EventTagInviteJoined,
		abi.EventTagContactFailed, abi.EventTagContactReady, abi.EventTagStickerPackPreview,
		abi.EventTagLabels)
	return nil
}

// identity hashes everything that makes one open dialog different from
// another, so reopening or replacing a dialog starts it afresh.
var seed = maphash.MakeSeed()

func identity(d *state.Dialog) uint64 {
	var h maphash.Hash
	h.SetSeed(seed)
	h.WriteByte(byte(d.Kind))
	var b [8]byte
	put := func(v uint64) {
		for i := range b {
			b[i] = byte(v >> (8 * i))
		}
		h.Write(b[:])
	}
	put(uint64(d.Chat))
	put(uint64(d.Account))
	put(uint64(d.N))
	if d.ForEveryone {
		h.WriteByte(1)
	}
	for i := 0; i < d.IDs.Len(); i++ {
		h.Write(d.IDs.At(i))
		h.WriteByte(0)
	}
	h.Write(d.Text)
	h.WriteByte(0)
	h.Write(d.Path)
	return h.Sum64() | 1
}

// width is the sheet's width in points (upstream's per-dialog set_width).
func width(k state.DialogKind, window float32) float32 {
	switch k {
	case state.DialogShortcuts:
		return shortcutsWidth(window)
	case state.DialogLabels:
		return 460
	case state.DialogNewChat, state.DialogForward, state.DialogCreatePoll, state.DialogSendLocation, state.DialogStickerPack:
		return 420
	case state.DialogPollResults, state.DialogInteractiveList:
		return min(420, max(window-64, 180))
	}
	return 380
}

// Build draws the open dialog.
func (s *Screen) Build(e *screen.Env) {
	a := e.Acct()
	s.app = e.A
	s.imgs = e.K.Images() // the window's cache; the window polls it
	d := &e.S.Dialog
	if d.Kind != state.DialogUnlockLockedChats {
		s.unlock.leave()
	}
	switch d.Kind {
	case state.DialogNone, state.DialogCropper, state.DialogStickerMaker:
		s.withdraw(e)
		s.cur = 0
		return
	case state.DialogMessageInfo:
		// Message info is a pane here (ui/msginfo); where upstream had no room
		// for the pane it used a dialog.
		e.S.Panes.Pane, e.S.Panes.MessageInfo = state.PaneMessageInfo, d.IDs
		e.S.CloseDialog()
		return
	}
	if a == nil {
		return
	}
	if id := identity(d); id != s.cur {
		s.withdraw(e)
		s.cur, s.kind, s.age = id, d.Kind, 0
		s.reset(e, a)
		if isQuestion(d.Kind) {
			s.ask(e, a)
		}
	}
	if s.alert.ticket != 0 {
		if s.answered(e, a) {
			return // the system's alert is on screen (or just answered)
		}
	}
	s.age++
	k := e.K
	winW, _ := winPt(e)
	s.open = true
	o := rexui.DialogOpts{Title: s.title, Width: width(d.Kind, winW)}
	dl := rexui.BeginDialog(k.Kit, "dialog", &s.open, o)
	if dl.Visible {
		s.body(e, a, &dl)
	}
	rexui.EndDialog(k.Kit, &dl)
	if !s.open && e.S.Dialog.Kind == d.Kind && identity(&e.S.Dialog) == s.cur {
		s.closed(e, a)
		e.S.CloseDialog()
	}
}

// next replaces the open dialog with d, from inside a dialog's action (which
// closes the sheet it was chosen in).
func (s *Screen) next(e *screen.Env, d state.Dialog) {
	e.S.OpenDialog(d)
	s.open = true
}

// closed runs when the dialog goes away without a decision (Escape, Cancel).
func (s *Screen) closed(e *screen.Env, a *state.Account) {
	switch e.S.Dialog.Kind {
	case state.DialogNewContact:
		s.contact.cancel(a)
	}
}

// reset prepares the dialog that just opened: its title and paragraphs, and
// whatever it asks the core for.
func (s *Screen) reset(e *screen.Env, a *state.Account) {
	d := &e.S.Dialog
	s.title, s.paras, s.verb = "", [3]string{}, ""
	s.scroll = widget.ScrollState{}
	switch d.Kind {
	case state.DialogForward:
		s.fwd.reset()
		s.title = forwardTitle(d.IDs.Len())
	case state.DialogNewChat:
		s.chat.reset()
		s.title = i18n.T("New chat")
	case state.DialogNewContact:
		s.contact.reset(e)
		s.title = i18n.T("New contact")
	case state.DialogPairWithPhone:
		s.pair.phone.Reset()
		s.title = i18n.T("Link with a phone number")
	case state.DialogUnlockLockedChats:
		s.unlock.reset(e)
		s.title = s.unlockTitle()
	case state.DialogCreatePoll:
		s.poll.reset(a, d)
		s.title = i18n.T("Create poll")
	case state.DialogSendLocation:
		s.loc.text.SetBytes(d.Text) // a spot that was refused comes back with the dialog
		s.title = i18n.T("Send location")
	case state.DialogLabels:
		s.lab.reset()
		s.title = i18n.T("Labels")
	case state.DialogJoinGroup:
		s.inv.reset(a, d)
		s.title = i18n.T("Group invite")
	case state.DialogStickerPack:
		s.pack.reset()
		s.title = i18n.T("Sticker pack")
		if c, ok := a.Model.Chats.Get(d.Chat); ok && d.IDs.Len() > 0 {
			cmd := a.Cmd(abi.CommandTagViewStickerPack)
			cmd.ViewStickerPack.Chat, cmd.ViewStickerPack.Message = c.ID(), d.IDs.At(0)
			a.Send(cmd)
		}
	case state.DialogPollResults:
		s.title = i18n.T("Poll results")
	case state.DialogInteractiveList:
		s.title = i18n.T("Choose an option")
	}
	s.prepare(e, a)
}

// body builds the dialog's content and actions.
func (s *Screen) body(e *screen.Env, a *state.Account, dl *rexui.Dialog) {
	d := &e.S.Dialog
	switch d.Kind {
	case state.DialogShortcuts:
		s.shortcuts(e, dl)
	case state.DialogAbout:
		s.about(e, dl)
	case state.DialogNewChat:
		s.newChat(e, a, dl)
	case state.DialogNewContact:
		s.newContact(e, a, dl)
	case state.DialogPairWithPhone:
		s.pairWithPhone(e, a, dl)
	case state.DialogUnlockLockedChats:
		s.unlockLocked(e, a, dl)
	case state.DialogForward:
		s.forward(e, a, dl)
	case state.DialogCreatePoll:
		s.createPoll(e, a, dl)
	case state.DialogPollResults:
		s.pollResults(e, a, dl)
	case state.DialogSendLocation:
		s.sendLocation(e, a, dl)
	case state.DialogInteractiveList:
		s.interactiveList(e, a, dl)
	case state.DialogLabels:
		s.labelManager(e, a, dl)
	case state.DialogJoinGroup:
		s.joinGroup(e, a, dl)
	case state.DialogStickerPack:
		s.stickerPack(e, a, dl)
	default:
		s.confirm(e, a, dl)
	}
}

// ---- small building blocks ----

var (
	secondary = rexui.Of(theme.Label2)
	tertiary  = rexui.Of(theme.Label3)
	danger    = rexui.Of(theme.Red)
)

// para builds a wrapped paragraph in the sheet's width.
func para(k *widget.Kit, s string, style rexui.TextStyle, tint rexui.Tint) {
	if s == "" {
		return
	}
	rexui.Label(k.Kit, s, rexui.LabelOpts{Style: style, Color: tint, Wrap: true})
}

// bytesLabel is a one-line label from the model's bytes.
func bytesLabel(k *widget.Kit, b []byte, style rexui.TextStyle, tint rexui.Tint) {
	rexui.LabelBytes(k.Kit, b, rexui.LabelOpts{Style: style, Color: tint})
}

// rowName is "title###id": the row's accessible name, its text, with an
// identity that survives the title changing.
func rowName(c *ui.Context, title []byte, id int) string {
	return c.Fmt().Str(model.S(title)).Str("###").Int(id).String()
}

// chatTitle is the name a chat is listed by.
func chatTitle(a *state.Account, c model.Chat) []byte {
	if n := c.Name(); len(n) > 0 {
		return n
	}
	return a.Model.DisplayName(c.ID())
}

// beginActions starts a dialog's row of buttons: rexui's, right-aligned, but a
// flow, so that in a language with long labels, or a narrow window, the
// buttons that do not fit go on a line of their own below, still right-aligned
// and in the order they were built (Cancel first, the default action last).
func beginActions(e *screenEnv) {
	k := e.K.Kit
	k.Space(rexui.DialogLook.ActionsGap - k.Theme().Space.ControlGap/k.Scale()) // above the panel's gap
	g := e.Pt(rexui.DialogLook.ButtonGap)
	e.C().BeginFlow("actions", ui.FlowOpts{GapX: g, GapY: g, Align: ui.FlowEnd, Cross: ui.CrossCenter})
}

func endActions(e *screenEnv) { e.C().EndFlow() }
