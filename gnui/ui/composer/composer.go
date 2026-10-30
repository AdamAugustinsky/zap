package composer

import (
	"bytes"
	"log/slog"
	"time"

	"gonativeui/app"
	"gonativeui/icon"
	"gonativeui/platform"
	"gonativeui/rexui"
	"gonativeui/theme"
	"gonativeui/ui"
	"gonativeui/widget/base"

	"zapfast/abi"
	"zapfast/i18n"
	"zapfast/model"
	"zapfast/state"
	ztheme "zapfast/theme"
	"zapfast/ui/screen"
)

// Layout, in points (zf:src/ui/conversation.rs:1326-1941).
const (
	marginPt = 8 // the band's inset at the sides and the bottom
	stripGap = 8 // from a strip (reply, edit) to the field
	maxText  = 65536
	// composingTimeout is how long typing pauses before "typing…" stops
	// (zf:src/app.rs:161).
	composingTimeout = 4 * time.Second
	// editWindow is how long a sent text stays editable (zf:src/app.rs:40).
	editWindow = 15 * 60
)

// Screen is the composer: the band at the foot of the open chat.
type Screen struct {
	name string // the split view's second pane's ("" the first): its keys
	ic   struct{ plus, smile, mic, send, check, xmark, pencil, keyboard, clip, trash icon.ID }

	env   *screen.Env // the main window's (Init's)
	state *state.State
	st    *rexui.ComposerState
	enter platform.EnterPolicy
	hint  string // the placeholder shown
	win   *platform.Window

	ready, making bool // the native editor belongs to this window

	// The composer's accessory in place of the send button: the microphone
	// of an empty composer, or Send for a refused voice message (method
	// values, made once).
	micButton, resendButton func(k *base.Kit)

	// The chat the composer holds now: its account and its JID. A frame
	// with another open chat parks this one's draft first.
	acct *state.Account
	ref  model.ChatRef
	chat []byte

	asides   asides
	mentions []mention // whom the open chat's text mentions by name
	nextID   uint64
	// prefill is a link's text the composer took and the reader has not
	// changed (it is not their draft), and since when a link's text has
	// waited for its chat to open.
	prefill struct {
		text  []byte
		since time.Time
	}
	// dirty: the text changed since the archive last had it, at lastEdit.
	dirty    bool
	lastEdit time.Time

	// edit is the id of the message the composer edits (state.Panes.Edit as
	// last seen), reply the one the reply strip shows and replyChat its chat
	// when it is another (a private reply; Panes.ReplyChat).
	edit, reply, replyChat []byte
	replyWho               string // "Replying to {who}" of reply
	replyText              []byte
	replyGone              bool // a private reply whose message cannot be shown
	replyHidden            bool // QuoteHidden when the reply was last synced

	comp completion

	// Composing: whether "typing…" was sent, and when the last keystroke was.
	composing bool
	lastKey   time.Time
	timer     *time.Timer
	prevText  []byte // the text as of the last change

	// row is the open chat's whole record (its members), decoded when the
	// chat's row changes.
	row    abi.Chat
	rowRef model.ChatRef
	rowGen uint32
	rowOK  bool

	// Layout: the band's key and its height in the last frame, the field's
	// box, and the frame the composer was last built in.
	bandKey   ui.Key
	lastH     float32
	fieldID   ui.ID
	accID     ui.ID // the microphone or resend button in Send's place, this frame
	lastFrame uint32
	focused   bool // the text had the keyboard in the last frame

	tools      bool // the drawn plus menu is open
	toolsKey   ui.Key
	plusRect   [4]float32
	toolLabels [4]string

	pendingPicks []pick
	out          sendBufs

	paste pasteState
	drop  dropState
	crop  cropState
	voice voice

	hints     hintStrings
	scratch   []byte
	spans     []platform.TextSpan
	attach    []rexui.ComposerAttachment
	ids       [][]byte
	quoteName []byte
}

var _ screen.ComposerPane = (*Screen)(nil)

// New returns the composer.
func New() *Screen { return &Screen{} }

// NewPane is the composer of the split view's second pane: its boxes keyed
// apart from the first's.
func NewPane(name string) *Screen { return &Screen{name: name} }

// Init readies the icons, buffered text and listeners. The native editor
// waits until a chat actually needs the composer.
func (s *Screen) Init(e *screen.Env) error {
	k := e.K
	add := func(sym string, fb rune, pt float32) icon.ID {
		return k.AddIcon(icon.Spec{Symbol: sym, Fallback: fb, Pt: pt})
	}
	s.ic.plus = k.AddIcon(ztheme.LargeIcon("plus", '+'))
	s.ic.smile = k.AddIcon(ztheme.LargeIcon("face.smiling", '☺'))
	s.ic.mic = k.AddIcon(ztheme.LargeIcon("mic", '●'))
	s.ic.send = add("arrow.up", '↑', 14)
	s.ic.check = add("checkmark", '✓', 14)
	s.ic.xmark = k.AddIcon(ztheme.SmallIcon("xmark", '×'))
	s.ic.pencil = add("pencil", '✎', 13)
	s.ic.keyboard = k.AddIcon(ztheme.SmallIcon("keyboard", '⌨'))
	s.ic.clip = add("paperclip", '⎘', 26)
	s.ic.trash = add("trash", '⌫', 15)
	s.env, s.win, s.state = e, e.A.Win, e.S
	s.enter = enterPolicy(e.S)
	var text []byte
	if s.st != nil {
		text = s.st.Text()
	}
	_ = s.makeEditor(nil, text)
	s.ready, s.making = s.win == nil, false
	s.bandKey = ui.HashString(0, "zapfast composer band"+s.name)
	s.toolsKey = ui.HashString(0, "zapfast composer tools"+s.name)
	s.asides.init()
	s.paste.init()
	s.crop.init()
	s.crop.key = ui.HashString(0, "zapfast composer crop"+s.name)
	s.scratch = make([]byte, 0, 1024)
	s.spans = make([]platform.TextSpan, 0, 32)
	wake := e.S.Wake
	s.timer = time.AfterFunc(time.Hour, func() { wake() })
	s.timer.Stop()
	e.S.Listen(s, abi.EventTagPicked, abi.EventTagPictureEdited, abi.EventTagSendRefused, abi.EventTagChatRemoved, abi.EventTagAccountRemoved)
	// The lock stores the open chat's text and ends a recording
	// (zf:src/app.rs:8591); a recording keeps the app in use.
	e.S.OnLock(s, func(*state.State) {
		s.flushOpen()
		s.voice.discard(s)
	})
	// Quitting stores the open chat's text; a chat that closes, by any
	// route, is parked: its text goes to the archive, the rest to its aside.
	e.S.OnQuit(s, func(*state.State) { s.flushOpen() })
	e.S.OnChatClose(s, func(_ *state.State, a *state.Account, chat []byte) {
		if a == s.acct && len(s.chat) > 0 && bytes.Equal(chat, s.chat) {
			s.park(s.env)
			s.chat = s.chat[:0]
		}
	})
	// An unlinked number's unsent text, quote and attachments go, unstored.
	e.S.OnUnlinked(s, func(_ *state.State, a *state.Account) { s.unlinked(a) })
	// A chat that is gone takes its text, quote and attachments with it.
	e.S.OnChatGone(s, func(_ *state.State, a *state.Account, chat []byte) { s.chatRemoved(a, chat) })
	s.micButton, s.resendButton = s.mic, s.resend
	// The system's answer about the microphone, and a recording read.
	e.S.OnAppEvent(s, func(st *state.State, ev *app.Event) bool { return s.voice.appEvent(st, ev) })
	e.S.OnInUse(s, func(*state.State) bool { return s.voice.recording() })
	s.voice.init(e)
	s.voice.waveK = ui.HashString(0, "zapfast composer waveform"+s.name)
	return nil
}

// Event takes the files a file dialog picked for a chat, a cropped picture,
// and what a refused send held (package state restores refused text).
func (s *Screen) Event(a *state.Account, ev *abi.Event) {
	switch ev.Tag {
	case abi.EventTagPicked:
		if !a.Active() || len(ev.Picked.Paths) == 0 {
			return
		}
		kind := state.StagedFile
		if ev.Picked.Documents {
			kind = state.StagedDocument
		}
		s.stagePathsTo(a, ev.Picked.Chat, ev.Picked.Paths, kind)
	case abi.EventTagPictureEdited:
		s.edited(a, &ev.PictureEdited)
	case abi.EventTagSendRefused:
		s.refused(a, &ev.SendRefused)
	case abi.EventTagChatRemoved:
		s.chatRemoved(a, ev.ChatRemoved.Chat)
	case abi.EventTagAccountRemoved:
		s.unlinked(a) // release media before the state closes this core
	}
}

// enterPolicy is the Enter policy the settings ask for.
func enterPolicy(st *state.State) platform.EnterPolicy {
	if st.Settings.EnterSends {
		return platform.EnterSends
	}
	return platform.CmdEnterSends
}

// makeEditor makes the composer's state (a hosted NSTextView in a real
// window, a drawn field offscreen), with text in it.
func (s *Screen) makeEditor(w *platform.Window, text []byte) error {
	s.hint = i18n.T("Type a message")
	st, err := rexui.NewComposer(w, rexui.ComposerSetup{Placeholder: s.hint, MaxLen: maxText, Enter: s.enter})
	s.st = st
	// Up on the first line, or in an empty composer, is reported (EdgeUp):
	// in an empty one it edits the last message.
	st.SetEdgeArrows(true)
	if len(text) > 0 {
		_ = st.SetText(text)
	}
	return err
}

// Height is the band's height for width w: the last frame's (it grows with
// the text, the strips and the attachments).
func (s *Screen) Height(e *screen.Env, w float32) float32 {
	if s.selecting(e) {
		return 0
	}
	if r := e.C().RectOf(s.bandKey); r[3] > r[1] {
		h := r[3] - r[1]
		if h != s.lastH {
			s.lastH = h
			e.C().RequestFrame()
		}
		return h
	}
	h := e.Pt(36 + marginPt + 4)
	if e.S.Settings.ShowShortcutHints {
		h += e.Pt(18)
	}
	return h
}

// selecting: selection mode's bar replaces the composer (ui/conversation's).
func (s *Screen) selecting(e *screen.Env) bool { return e.S.Panes.Selecting }

// Build builds the composer at r: the read-only notice, or the strips above
// the field, the field with its buttons, and the shortcut hints.
func (s *Screen) Build(e *screen.Env, r screen.Rect) {
	a := e.Acct()
	if a == nil {
		return
	}
	ch, ok := a.OpenChatView()
	if !ok {
		return
	}
	if !s.ready {
		if !s.making {
			s.making = true
			warmEmoji() // ready before the first keystroke, not on the chat list
			w := s.win
			e.A.AtSafePoint(func() {
				if s.win != w { // a replacement window owns the next request
					return
				}
				s.making = false
				if a := e.Acct(); e.A.Win != w || e.S.Lock.Locked || a == nil || a.Open == 0 {
					return // closed, locked or switched away before the safe point
				}
				if err := s.makeEditor(w, s.st.Text()); err != nil {
					slog.Warn("zapfast: the composer's editor", "err", err)
				}
				s.ready = true
				s.styleMentions(e)
				e.S.Wake()
			})
		}
		return // native hosts must be made between frames, never in Build
	}
	if p := enterPolicy(e.S); p != s.enter {
		s.enter = p
		s.st.SetEnter(p)
	}
	c := e.C()
	if f := c.Frame(); f != s.lastFrame+1 {
		// Not built in the frame before (the chat was closed, or the
		// selection bar had the place): what it saw then is stale.
		s.focused, s.tools = false, false
	}
	s.lastFrame = c.Frame()
	s.follow(e, a, ch)
	s.syncRow(a, ch)
	s.tick(e, a)
	s.drop.build(s, e, r, ch)
	s.voice.frame(s, e)
	if s.selecting(e) {
		return
	}
	s.syncEdit(e, a)
	s.syncReply(e, a)
	if !e.S.Side { // the one-shot asks are the pane in use's
		s.takePrefill(e, a, ch)
		s.takeRestore(e, a)
	}
	s.takePaste(e)
	if !e.S.Side {
		if ins := e.S.TakeInsert(); len(ins) > 0 && canSend(&ch) {
			s.insert(e, a, ins)
		}
	}
	if e.S.Req.FocusComposer && !e.S.Side {
		e.S.Req.FocusComposer = false
		if canSend(&ch) && !s.voice.recording() {
			s.st.Focus(e.K.Kit)
		}
	}

	theme.At(c, r.X, r.Y, r.W, r.H)
	c.NextAxis(ui.AxisY)
	m := e.Pt(marginPt)
	c.NextPadding(m, 0, m, m)
	c.NextGap(e.Pt(stripGap))
	c.NextSize(ui.Px(r.W), ui.Children(1))
	c.Push(c.BoxKeyed(base.Floating, s.bandKey, nil))
	if !canSend(&ch) {
		s.readOnly(e, a, ch)
		c.Pop()
		return
	}
	if s.voice.recording() {
		s.voice.keys(s, e)
	}
	if s.voice.recording() {
		// The bar takes the field's place, under the reply it quotes; Tab
		// keeps build order among its buttons, as upstream's.
		e.TabInBuildOrder()
		s.strips(e, a)
		s.voice.bar(s, e)
		c.Pop()
		return
	}
	s.takeEscape(e, a)
	if s.paste.sendQueued && s.picturesReady() {
		s.send(e, a, ch)
	}
	s.strips(e, a)
	s.field(e, a, ch)
	if e.S.Settings.ShowShortcutHints {
		s.hintsBar(e, r.W-2*m)
	}
	c.Pop()
	if len(e.S.Staged) > 0 {
		s.chipButtons(e, s.fieldID)
	}
}

// insert puts text (the picker's emoji) at the caret and gives the composer
// the keyboard (zf:src/app.rs:7238).
func (s *Screen) insert(e *screen.Env, a *state.Account, text []byte) {
	at := min(max(s.caret(), 0), len(s.st.Text()))
	end := at + len(text)
	if s.st.Replace(platform.TextRange{Start: at, End: at}, text, platform.TextRange{Start: end, End: end}) != nil {
		return
	}
	s.seen()
	s.keep(e, a)
	s.styleMentions(e)
	s.st.Focus(e.K.Kit)
}

// canSend is upstream's Chat::can_send (zf:src/model.rs:360).
func canSend(ch *model.Chat) bool {
	return !ch.Locked() && !ch.ReadOnly() && !ch.Left() && ch.Kind() != abi.ChatKindBroadcast
}

// syncRow decodes the open chat's record when its row changed.
func (s *Screen) syncRow(a *state.Account, ch model.Chat) {
	if s.rowOK && s.rowRef == ch.Ref() && s.rowGen == ch.Gen() {
		return
	}
	s.rowRef, s.rowGen = ch.Ref(), ch.Gen()
	s.rowOK = ch.Decode(&s.row) == nil
	if !s.rowOK {
		s.row = abi.Chat{}
	}
}

// field builds the line of the composer: the plus and emoji buttons, and
// the field (attachments, text, and send or the microphone).
func (s *Screen) field(e *screen.Env, a *state.Account, ch model.Chat) {
	k := e.K
	c := e.C()
	k.NextGap(ztheme.T.DockGap) // between the dock's parts: the plus, the emoji button, the field
	k.NextCross(ui.CrossEnd)
	k.NextWidth(ui.Pct(1))
	k.Row("line")
	editing := len(s.edit) > 0
	if !editing {
		// Each tool sits in a band as tall as the send button, so that it
		// stays beside the last line as the text grows.
		c.NextSize(ui.Children(1), ui.Px(e.Pt(36)))
		c.NextAxis(ui.AxisX)
		c.NextCross(ui.CrossCenter)
		c.NextGap(e.Pt(ztheme.T.DockGap))
		c.Push(c.Box(0, "tools"))
		c.NextSemState(semOn(s.tools))
		plus := toolButton(k.Kit, i18n.T("Attach"), s.ic.plus, s.tools)
		e.TabStop(screen.StopAttach, plus.ID)
		s.plusRect = c.RectOf(c.KeyOf(plus.ID))
		toolLabels(&s.toolLabels)
		if plus.Clicked {
			if e.S.Panes.Picker != state.PickerNone {
				e.S.Panes.Picker = state.PickerNone
			}
			if c.NativeMenus() {
				c.RequestMenu(ui.MenuRequest{Kind: ui.MenuList, Owner: c.KeyOf(plus.ID), Options: s.toolLabels[:], Selected: -1, Rect: s.plusRect})
			} else {
				s.tools = !s.tools
			}
			e.S.Touch()
		}
		if i, ok := c.MenuChoice(plus.ID, s.toolLabels[:]); ok {
			s.tool(e, a, ch, i)
		}
		c.NextSemState(semOn(e.S.Panes.Picker != state.PickerNone))
		smile := toolButton(k.Kit, i18n.T("Emoji, GIFs, and stickers"), s.ic.smile, e.S.Panes.Picker != state.PickerNone)
		e.TabStop(screen.StopEmoji, smile.ID)
		// The picker opens above the emoji button.
		if sr := c.RectOf(c.KeyOf(smile.ID)); sr[2] > sr[0] {
			e.S.Panes.PickerAnchor = state.Rect{X: sr[0], Y: sr[1], W: sr[2] - sr[0], H: sr[3] - sr[1]}
		}
		if smile.Clicked {
			s.tools = false
			if e.S.Panes.Picker == state.PickerNone {
				e.S.Panes.Picker = state.PickerEmoji
			} else {
				e.S.Panes.Picker = state.PickerNone
			}
			e.S.Touch()
		}
		c.Pop()
	}
	s.composer(e, a, ch)
	k.EndRow()
	if s.tools {
		s.toolsMenu(e, a, ch)
	}
}

// placeholder is the empty text's hint: a caption's while something is
// staged (zf:src/ui/conversation.rs:1657).
func (s *Screen) placeholder() string {
	if len(s.state.Staged) > 0 {
		return i18n.T("Add a caption")
	}
	return i18n.T("Type a message")
}

// mic is the microphone in the send button's place (Action::StartRecording).
func (s *Screen) mic(k *base.Kit) {
	sig := circleButton(k, i18n.T("Record a voice message"), s.ic.mic, &rexui.ButtonRecipes[rexui.ButtonDefault])
	s.accID = sig.ID
	if sig.Clicked {
		s.voice.start(s, s.env)
	}
}

// resend is Send for a refused voice message waiting in an empty composer.
func (s *Screen) resend(k *base.Kit) {
	sig := circleButton(k, i18n.T("Send"), s.ic.send, &rexui.ButtonRecipes[rexui.ButtonPrimary])
	s.accID = sig.ID
	if sig.Clicked {
		s.voice.resend(s, s.env)
	}
}

// circleButton is a round button as big as the composer's send button, in
// its place.
func circleButton(k *base.Kit, name string, ic icon.ID, rc *rexui.ButtonRecipe) base.Signal {
	c, r := k.Context(), k.Theme()
	p := k.PressState(name, base.HoverOff)
	fill, fg, _ := rc.Colors(r, p.Visual)
	side := rexui.ComposerLook.SendSize * k.Scale()
	c.NextBG(fill)
	c.NextRadius(side / 2)
	c.NextAxis(ui.AxisX)
	c.NextCross(ui.CrossCenter)
	c.NextJustify(ui.JustifyCenter)
	k.BeginPress(&p, ui.RoleButton, ui.Px(side), ui.Px(side), ui.DrawBackground, nil)
	if f, disp := k.Icons().Prepare(c, ic, fg); f != 0 {
		c.BoxKeyed(f, ui.KeyN(p.Key, 1), disp)
	}
	k.FocusRing(p.ID, p.Visual, side/2)
	return k.EndPress(&p)
}

// toolButton is one of the dock's tools beside the field: a round plate as
// tall as the field (HitLarge), quiet on the pane, lifting under the pointer
// and while what it opens is open (on), so that the plus, the emoji button
// and the field read as three things side by side.
func toolButton(k *base.Kit, name string, ic icon.ID, on bool) base.Signal {
	c, r := k.Context(), k.Theme()
	p := k.PressState(name, base.HoverOn)
	side := theme.Snap(ztheme.T.HitLarge * k.Scale())
	t := ztheme.T.ToolRest + ztheme.T.ToolLift*max(p.Hot, p.Down)
	if on {
		t = ztheme.T.ToolRest + ztheme.T.ToolLift
	}
	c.NextBG(theme.WithAlpha(r.Colors[theme.Label], t))
	c.NextRadius(side / 2)
	c.NextAxis(ui.AxisX)
	c.NextCross(ui.CrossCenter)
	c.NextJustify(ui.JustifyCenter)
	k.BeginPress(&p, ui.RoleButton, ui.Px(side), ui.Px(side), ui.DrawBackground, nil)
	if f, disp := k.Icons().Prepare(c, ic, r.Colors[theme.Label2]); f != 0 {
		c.BoxKeyed(f, ui.KeyN(p.Key, 1), disp)
	}
	k.FocusRing(p.ID, p.Visual, side/2)
	return k.EndPress(&p)
}

// How much of the label colour a tool's plate lays over the pane at rest,
// and how much more under the pointer or while its menu or picker is open,
// are ztheme.T.ToolRest and ToolLift (tune.go).

// keyDown reports whether the frame's input has code going down with mods.
func keyDown(in *ui.Input, code ui.KeyCode, mods ui.Mods) bool {
	for i := 0; i < in.NKeys; i++ {
		if k := &in.Keys[i]; k.Down && k.Code == code && k.Mods == mods {
			return true
		}
	}
	return false
}

// semOn is the semantic state of a toggled tool.
func semOn(on bool) ui.SemState {
	if on {
		return ui.SemSelected
	}
	return 0
}

// empty reports whether the composer has nothing to send.
func (s *Screen) empty() bool {
	return !hasContent(s.st.Text()) && len(s.state.Staged) == 0
}

// hasContent reports whether b holds anything but white space.
func hasContent(b []byte) bool {
	for _, c := range b {
		switch c {
		case ' ', '\t', '\n', '\r':
		default:
			return true
		}
	}
	return false
}

// composer builds rexui's composer and acts on what it reports.
func (s *Screen) composer(e *screen.Env, a *state.Account, ch model.Chat) {
	k := e.K
	c := e.C()
	o := rexui.ComposerOpts{SendIcon: s.ic.send, HasSendIcon: true}
	if len(s.edit) > 0 {
		o.SendIcon = s.ic.check
	}
	s.attach = s.attach[:0]
	for i := range e.S.Staged {
		st := &e.S.Staged[i]
		s.attach = append(s.attach, rexui.ComposerAttachment{Name: st.Name, ID: st.ID})
	}
	o.Attachments = s.attach
	if s.comp.open() && len(s.comp.items) > 0 {
		o.Completion, o.Items = &s.comp.list, s.comp.items
	}
	// An empty composer's send button records, or sends a refused voice
	// message waiting here; an edit's is always the check
	// (zf:src/ui/conversation.rs:1792).
	if len(s.edit) == 0 && s.empty() {
		o.Accessory = s.micButton
		if s.voice.unsentHere(s) {
			o.Accessory = s.resendButton
		}
	}
	if h := s.placeholder(); h != s.hint {
		s.hint = h
		s.st.SetPlaceholder(h)
	}
	// The drawn field's Enter policy is the composer's own (the hosted
	// editor has its own; the drawn field is one line and submits on any
	// Enter).
	in := c.Input()
	drawnCmd := !s.st.Hosted() && s.enter == platform.CmdEnterSends
	cmdSend := drawnCmd && s.focused && keyDown(in, ui.KeyEnter, ui.PrimaryMod())
	k.NextWidth(ui.Pct(1))
	s.accID = 0
	sig := rexui.Composer(k.Kit, i18n.P("message field", "Message")+"###message", s.st, o)
	s.focused, s.fieldID = sig.Focused, sig.ID
	if s.focused && e.S.Side {
		// typed into straight in its native view (no press reached the
		// window): this pane is the one in use now
		e.S.Req.UsePane = a.Open // the pane's chat, traded in while it builds
	}
	// The Tab order: the text, then Send (or the microphone in its place),
	// zf:src/ui/conversation.rs:1726-1823; Send is a stop only while
	// enabled.
	e.TabStopKey(screen.StopComposer, sig.Key)
	switch {
	case s.accID != 0:
		e.TabStop(screen.StopSend, s.accID)
	case sig.Send != 0 && !s.empty():
		e.TabStop(screen.StopSend, sig.Send)
	}

	if sig.Removed >= 0 && sig.Removed < len(e.S.Staged) {
		s.unstage(a, sig.Removed)
	}
	s.completionSignals(e, a, ch, &sig)
	if sig.Changed {
		s.changed(e, a, ch, sig.Composing)
	} else if sig.Selected {
		s.comp.caretMoved(s, e, a, ch)
	}
	cancel := sig.Cancelled && o.Completion == nil
	if sig.Focused && s.st.Hosted() && keyDown(in, ui.KeyEscape, 0) && !c.TakeKey(ui.KeyEscape, 0) {
		// The hosted editor reports Escape (Cancelled) and passes the key on
		// to the window too, where the shell's last resort would close the
		// chat after an edit ends: the composer takes it, unless a screen
		// built before it (the picker) took it already.
		cancel = false
	}
	if cancel {
		s.escape(e, a)
	}
	if sig.PastedImage || sig.PastedFiles {
		s.requestPaste(e)
	}
	// Up in an empty composer edits the last message (zf:src/ui/keys.rs:312).
	if sig.EdgeUp && len(s.st.Text()) == 0 && len(e.S.Staged) == 0 && len(s.edit) == 0 &&
		!c.ModalOpen() && e.S.Panes.Picker == state.PickerNone {
		s.editPrevious(e, a)
	}
	// The send key by the Enter policy, whether or not there is anything
	// to send (SendKey).
	sendKey := sig.SendKey
	if drawnCmd {
		sig.Submitted = cmdSend && sig.Completion != rexui.ResultAccept && sig.Chosen < 0
		sendKey = sig.Submitted
	}
	if sig.Submitted && !sig.Composing && !s.empty() {
		s.send(e, a, ch)
	} else if sendKey && !sig.Composing && s.empty() && s.voice.unsentHere(s) {
		// The send key with nothing typed tries a refused voice message
		// again (zf:src/ui/conversation.rs:1854; CP-024).
		s.voice.resend(s, e)
	}
}

// changed is a change of the text: "typing…", the completion and the
// mentions' styles.
func (s *Screen) changed(e *screen.Env, a *state.Account, ch model.Chat, composing bool) {
	text := s.st.Text()
	typed := justTyped(s.prevText, text, s.caret())
	s.seen()
	if composing {
		// An input method composes: the popup closes, and the text is its
		// own until it commits.
		s.comp.close(s)
		return
	}
	s.keystroke(e, a)
	s.keep(e, a)
	s.comp.textChanged(s, e, a, ch, typed)
	s.styleMentions(e)
}

// justTyped reports whether the character before the caret is new: the
// change from old to text inserted it (keys typed between two frames come
// as one change).
func justTyped(old, text []byte, caret int) bool {
	if len(text) <= len(old) || caret <= 0 {
		return false
	}
	p := 0
	for p < len(old) && old[p] == text[p] {
		p++
	}
	return caret-1 >= p && caret <= p+len(text)-len(old)
}

// seen notes the editor's text as the one the next change is from.
func (s *Screen) seen() { s.prevText = append(s.prevText[:0], s.st.Text()...) }

// keystroke sends "typing…" once, and starts its timeout over
// (zf:src/app.rs:5309).
func (s *Screen) keystroke(e *screen.Env, a *state.Account) {
	s.lastKey = e.S.Now()
	if !s.composing && a.Settings.SendTyping && len(s.chat) > 0 {
		s.composing = true
		cmd := a.Cmd(abi.CommandTagComposing)
		cmd.Composing.Chat, cmd.Composing.Composing = s.chat, true
		a.Send(cmd)
	}
	s.timer.Reset(composingTimeout + 50*time.Millisecond)
}

// stopComposing ends "typing…" (zf:src/app.rs:5323).
func (s *Screen) stopComposing(a *state.Account) {
	if s.composing && a != nil && len(s.chat) > 0 {
		cmd := a.Cmd(abi.CommandTagComposing)
		cmd.Composing.Chat, cmd.Composing.Composing = s.chat, false
		a.Send(cmd)
	}
	s.composing = false
	s.lastKey = time.Time{}
	s.timer.Stop()
}

// tick ends "typing…" after a pause, and then stores the draft in the
// archive (a restart finds it there).
func (s *Screen) tick(e *screen.Env, a *state.Account) {
	now := e.S.Now()
	if s.composing && now.Sub(s.lastKey) > composingTimeout {
		s.stopComposing(a)
	}
	if s.dirty && now.Sub(s.lastEdit) > composingTimeout {
		s.flushOpen()
	}
}

// escape is Escape in the composer (zf:src/ui/keys.rs:164): it closes the
// picker, then leaves Starred for the chats, then drops what the composer
// was in the middle of (the attachments, the edit, the reply), then the pane
// beside the chat, then lets go of the keyboard (the single keys work then,
// shell/keyboard.go), then the chat.
func (s *Screen) escape(e *screen.Env, a *state.Account) {
	p := &e.S.Panes
	switch {
	case p.Picker != state.PickerNone:
		p.Picker = state.PickerNone
	case s.tools:
		s.tools = false
	case a.Mode == state.ListStarred:
		a.SetMode(state.ListChats)
	case len(e.S.Staged) > 0:
		s.clearStaged(a)
	case len(s.edit) > 0:
		s.cancelEdit(e)
	case p.Reply.Len() > 0:
		p.Reply.Reset()
		p.ReplyChat.Reset()
	case p.Pane != state.PaneNone:
		p.Pane = state.PaneNone
	case s.focused:
		e.C().SetFocus(0)
		s.focused = false
	default:
		a.OpenChat(0) // the chat's text goes to the archive (OnChatClose)
	}
	e.S.Touch()
}

// takeEscape takes Escape outside the composer while it has something to
// drop: the global key chain reaches the composer's state after the picker.
func (s *Screen) takeEscape(e *screen.Env, a *state.Account) {
	c := e.C()
	if s.focused || c.ModalOpen() || c.TextFieldFocused() || e.S.Panes.Picker != state.PickerNone || s.tools {
		return
	}
	if len(e.S.Staged) == 0 && len(s.edit) == 0 && e.S.Panes.Reply.Len() == 0 && a.Mode != state.ListStarred {
		return
	}
	if c.TakeKey(ui.KeyEscape, 0) {
		s.escape(e, a)
	}
}
