package msginfo

import (
	"bytes"
	"slices"
	"strings"
	"time"

	"gonativeui/icon"
	"gonativeui/rexui"
	"gonativeui/theme"
	"gonativeui/ui"
	"gonativeui/widget"
	"gonativeui/widget/base"

	"zapfast/abi"
	"zapfast/format"
	"zapfast/i18n"
	"zapfast/model"
	"zapfast/state"
	"zapfast/ui/screen"
)

// Layout, in points (zf:src/ui/message_info.rs).
const (
	margin   = 16
	headerPt = 52
	jidMax   = 128
	avatarPt = 34
	rowPt    = 40
)

type span struct{ off, n uint32 }

// recipient is one row of a group message's receipts.
type recipient struct {
	id, name, when span
	at             int64
	hasAt          bool
}

// receipts are the per-recipient receipts of one of our group messages, as
// far as they are known, sorted into the sections of the pane
// (MessageReceipts in zf:src/model.rs).
type receipts struct {
	buf       []byte
	chat, msg [jidMax]byte
	chatN     int
	msgN      int
	have      bool
	rawBuf    []byte
	sections  [3][]recipient // played, read, delivered
	remaining int
	known     bool
	empty     bool
	namesGen  uint32
}

func (r *receipts) put(b []byte) span {
	off := len(r.buf)
	r.buf = append(r.buf, b...)
	return span{uint32(off), uint32(len(b))}
}

func (r *receipts) putS(s string) span {
	off := len(r.buf)
	r.buf = append(r.buf, s...)
	return span{uint32(off), uint32(len(s))}
}

func (r *receipts) str(s span) string { return model.S(r.buf[s.off : s.off+s.n : s.off+s.n]) }

// stored is a recipient as the event gave it, with its id in rawBuf.
type stored struct {
	id                               span
	expected                         bool
	delivered, read, played          int64
	hasDelivered, hasRead, hasPlayed bool
}

// message is what the pane shows of the message, read when it changes.
type message struct {
	ok          bool
	fromMe      bool
	direct      bool
	status      abi.Delivery
	delivery    abi.Delivery // what its ticks show
	deliveredAt int64
	readAt      int64
	hasDeliv    bool
	hasRead     bool
	ts          int64
	summary     string
	clock       string
	conv, gen   uint32
	index       int
	scratch     abi.Message
	acct        int
}

// Screen is the message info pane.
type Screen struct {
	env *screen.Env
	ic  struct{ closeX, back icon.ID }

	acct      int
	chat, msg [jidMax]byte
	chatN     int
	msgN      int
	valid     bool

	m  message
	r  receipts
	st []stored

	watching   bool
	watchChat  [jidMax]byte
	watchChatN int
	watchMsg   [jidMax]byte
	watchMsgN  int

	hdr     header // what the header says this frame
	inBand  bool   // Toolbar built the header in the toolbar band (this frame, after Build)
	bandNow bool   // so it did last frame: the pane leaves its header out

	remainText string // "N remaining", made when N changes
	remainN    int
	remainLang i18n.Locale

	scroll  widget.ScrollState
	styles  ui.RichStyles
	stylesK float32
}

var _ screen.SidePane = (*Screen)(nil)
var _ state.Listener = (*Screen)(nil)
var _ screen.PaneCloser = (*Screen)(nil)
var _ screen.ToolbarPart = (*Screen)(nil)

// New returns the pane.
func New() *Screen { return &Screen{} }

// Init readies the pane for a window: its symbols, and the receipts it keeps.
func (s *Screen) Init(e *screen.Env) error {
	s.env = e
	s.ic.closeX = e.K.AddIcon(icon.Spec{Symbol: "xmark", Fallback: '✕', Pt: 13})
	s.ic.back = e.K.AddIcon(icon.Spec{Symbol: "chevron.left", Fallback: '‹', Pt: 13})
	e.S.Listen(s, abi.EventTagReceipts)
	return nil
}

// Width is the pane's width.
func (s *Screen) Width(e *screen.Env) float32 { return e.Pt(e.S.Settings.InfoPaneWidth) }

func (s *Screen) now() time.Time { return s.env.S.Now() }

// Event keeps the receipts of the message the pane shows. An answer for a
// message the pane no longer shows is stale, and stops the core following it.
func (s *Screen) Event(a *state.Account, ev *abi.Event) {
	e := &ev.Receipts
	showing := a.Active() && a.Index == s.acct && s.valid && s.env.S.Panes.Pane == state.PaneMessageInfo
	if showing && bytes.Equal(e.Chat, s.chat[:s.chatN]) && bytes.Equal(e.Message, s.msg[:s.msgN]) {
		s.r.take(e, s)
		s.rebuild(a)
		s.env.S.Touch()
		return
	}
	// the pane closed or moved on while the core still follows the message:
	// stop (upstream's follow_receipts)
	if s.watching && a.Index == s.acct && bytes.Equal(e.Chat, s.watchChat[:s.watchChatN]) && bytes.Equal(e.Message, s.watchMsg[:s.watchMsgN]) {
		s.unwatch(a)
	}
}

// Closed stops following the message when the pane closes, or its chat or
// account changes.
func (s *Screen) Closed(e *screen.Env) {
	if s.watching && s.acct >= 0 && s.acct < len(e.S.Accounts) {
		s.unwatch(e.S.Accounts[s.acct])
	}
	s.valid = false
	s.m.ok = false
	s.r.have = false
}

func (s *Screen) unwatch(a *state.Account) {
	s.watching = false
	if a.Conn == nil {
		return
	}
	cmd := a.Cmd(abi.CommandTagWatchReceipts)
	a.Send(cmd)
}

// follow keeps the core following receipts for exactly the group message the
// pane shows. A direct message's times are on its row.
func (s *Screen) follow(a *state.Account) {
	group := bytes.HasSuffix(s.chat[:s.chatN], []byte("@g.us"))
	want := s.valid && group && s.m.ok
	if !want {
		if s.watching {
			s.unwatch(a)
		}
		return
	}
	if s.watching && s.acct == a.Index && bytes.Equal(s.watchChat[:s.watchChatN], s.chat[:s.chatN]) && bytes.Equal(s.watchMsg[:s.watchMsgN], s.msg[:s.msgN]) {
		return
	}
	cmd := a.Cmd(abi.CommandTagWatchReceipts)
	cmd.WatchReceipts.Target.Chat, cmd.WatchReceipts.Target.Message, cmd.WatchReceipts.HasTarget = s.chat[:s.chatN], s.msg[:s.msgN], true
	if a.Send(cmd) != nil {
		return
	}
	s.watching = true
	s.watchChatN = copy(s.watchChat[:], s.chat[:s.chatN])
	s.watchMsgN = copy(s.watchMsg[:], s.msg[:s.msgN])
}

// take stores the event's receipts.
func (r *receipts) take(e *abi.EventReceipts, s *Screen) {
	r.have = true
	r.chatN = copy(r.chat[:], e.Chat)
	r.msgN = copy(r.msg[:], e.Message)
	s.st = s.st[:0]
	r.rawBuf = r.rawBuf[:0]
	for i := range e.Recipients {
		x := &e.Recipients[i]
		off := len(r.rawBuf)
		r.rawBuf = append(r.rawBuf, x.ID...)
		s.st = append(s.st, stored{
			id: span{uint32(off), uint32(len(x.ID))}, expected: x.Expected,
			delivered: x.DeliveredAt, hasDelivered: x.HasDeliveredAt,
			read: x.ReadAt, hasRead: x.HasReadAt,
			played: x.PlayedAt, hasPlayed: x.HasPlayedAt,
		})
	}
}

// rebuild sorts the receipts into the sections and writes their names and
// times.
func (s *Screen) rebuild(a *state.Account) {
	r := &s.r
	if !r.have {
		return
	}
	st := a.Model
	now := s.now()
	r.buf = r.buf[:0]
	for i := range r.sections {
		r.sections[i] = r.sections[i][:0]
	}
	r.remaining, r.known, r.empty = 0, false, len(s.st) == 0
	idOf := func(x *stored) []byte { return r.rawBuf[x.id.off : x.id.off+x.id.n] }
	add := func(sec int, x *stored, at int64, has bool) {
		id := idOf(x)
		name := displayName(st, id)
		rc := recipient{id: r.put(id), name: r.putS(name), at: at, hasAt: has}
		if has && at > 0 { // a time of zero or less is unknown, not the Unix epoch
			rc.when = r.putS(momentStamp(now, at))
		}
		r.sections[sec] = append(r.sections[sec], rc)
	}
	for i := range s.st {
		x := &s.st[i]
		if x.expected {
			r.known = true
		}
		switch {
		case x.hasPlayed:
			add(0, x, x.played, true)
		case x.hasRead:
			add(1, x, x.read, true)
		case x.hasDelivered:
			add(2, x, x.delivered, true)
		case x.expected:
			r.remaining++
		}
	}
	for i := range r.sections {
		sec := r.sections[i]
		slices.SortStableFunc(sec, func(a, b recipient) int {
			if a.at != b.at {
				if a.at > b.at {
					return -1
				}
				return 1
			}
			return strings.Compare(r.str(a.id), r.str(b.id))
		})
	}
	r.namesGen = st.Contacts.Gen() + st.Chats.Gen()<<16
}

func displayName(st *model.Store, jid []byte) string {
	if len(jid) > 0 && (bytes.Equal(st.Me.ID(), jid) || bytes.Equal(st.Me.LID(), jid)) {
		return i18n.T("You")
	}
	return string(st.DisplayName(jid))
}

// summary is the one-line summary of a message's content (zf:src/model.rs),
// in English as upstream keeps it.
func summary(m *abi.Message) string {
	c := &m.Content
	first := func(b []byte) string {
		if i := bytes.IndexByte(b, '\n'); i >= 0 {
			b = b[:i]
		}
		return string(b)
	}
	withCaption := func(label string, caption []byte, has bool) string {
		if cap := first(caption); has && cap != "" {
			return label + ": " + cap
		}
		return label
	}
	switch c.Tag {
	case abi.ContentTagText:
		return first(c.Text.Text)
	case abi.ContentTagInteractive:
		return first(c.Interactive.Text)
	case abi.ContentTagImage:
		return withCaption(i18n.T("Photo"), c.Image.Caption, c.Image.HasCaption)
	case abi.ContentTagVideo:
		label := i18n.T("Video")
		if c.Video.Gif {
			label = i18n.T("GIF")
		} else if c.Video.Note {
			label = i18n.T("Video message")
		}
		return withCaption(label, c.Video.Caption, c.Video.HasCaption)
	case abi.ContentTagAudio:
		label := i18n.T("Audio")
		if c.Audio.VoiceNote {
			label = i18n.T("Voice message")
		}
		if c.Audio.HasSeconds {
			return label + " (" + duration(c.Audio.Seconds) + ")"
		}
		return label
	case abi.ContentTagDocument:
		return i18n.Format(i18n.T("Document: {name}"), "{name}", string(c.Document.FileName))
	case abi.ContentTagSticker:
		return i18n.T("Sticker")
	case abi.ContentTagStickerPack:
		return i18n.Format(i18n.T("Sticker pack: {name}"), "{name}", string(c.StickerPack.Name))
	case abi.ContentTagLocation:
		if c.Location.HasName {
			return i18n.Format(i18n.T("Location: {name}"), "{name}", string(c.Location.Name))
		}
		return i18n.T("Location")
	case abi.ContentTagLiveLocation:
		if c.LiveLocation.Ended {
			return i18n.T("Live location ended")
		}
		return i18n.T("Live location")
	case abi.ContentTagContact:
		return i18n.Format(i18n.T("Contact: {name}"), "{name}", string(c.Contact.DisplayName))
	case abi.ContentTagPoll:
		return i18n.Format(i18n.T("Poll: {question}"), "{question}", string(c.Poll.Question))
	case abi.ContentTagRevoked:
		return i18n.T("This message was deleted")
	case abi.ContentTagUnsupported:
		return i18n.Format(i18n.T("Unsupported message ({what})"), "{what}", string(c.Unsupported.What))
	case abi.ContentTagPhoneOnly:
		p := &c.PhoneOnly
		switch {
		case p.LiveLocation:
			return i18n.T("Live location")
		case p.HasOnce:
			return [...]string{i18n.T("View once photo"), i18n.T("View once video"), i18n.T("View once voice message"), i18n.T("View once audio")}[min(int(p.Once), 3)]
		case p.ViewOnce:
			return i18n.T("View once message")
		}
		return i18n.T("Message on your phone")
	case abi.ContentTagCall:
		k := &c.Call
		label := i18n.T("Voice call")
		switch {
		case k.Status == abi.CallStatusMissed && k.Video:
			label = i18n.T("Missed video call")
		case k.Status == abi.CallStatusMissed:
			label = i18n.T("Missed voice call")
		case k.Video:
			label = i18n.T("Video call")
		}
		if k.Status == abi.CallStatusAnswered && k.Duration > 0 {
			return label + " (" + callLength(k.Duration) + ")"
		}
		return label
	}
	return ""
}

// callLength is a call's length in the summary's words, which are English
// (zf:src/model.rs summary).
func callLength(sec uint64) string { return string(format.AppendCallLength(nil, i18n.English, sec)) }

func chatIsGroup(chat []byte) bool { return bytes.HasSuffix(chat, []byte("@g.us")) }

// sync follows the state: the message the pane shows, read when it changes.
// It reports whether there is a pane to draw.
func (s *Screen) sync(e *screen.Env, a *state.Account) bool {
	p := &e.S.Panes
	ch, ok := a.OpenChatView()
	if !ok || p.MessageInfo.Len() == 0 {
		return false
	}
	chat, msg := ch.ID(), p.MessageInfo.At(0)
	if len(chat) > jidMax || len(msg) > jidMax {
		return false
	}
	fresh := !s.valid || a.Index != s.acct || !bytes.Equal(chat, s.chat[:s.chatN]) || !bytes.Equal(msg, s.msg[:s.msgN])
	if fresh {
		s.chatN = copy(s.chat[:], chat)
		s.msgN = copy(s.msg[:], msg)
		s.acct, s.valid = a.Index, true
		s.m.ok = false
		s.r.have = false // receipts of another message are stale
		s.st = s.st[:0]
		s.scroll = widget.ScrollState{}
	}
	conv := a.Model.Conversation(chat)
	var idx = -1
	if conv != nil {
		idx = conv.Find(msg)
	}
	if idx < 0 {
		s.m.ok = false
	} else {
		m := conv.At(idx)
		if !s.m.ok || fresh || s.m.conv != conv.Gen() || s.m.gen != m.Gen() || s.m.index != idx {
			if m.Decode(&s.m.scratch) == nil {
				s.loadMessage(a, &s.m.scratch, conv.Gen(), m.Gen(), idx)
			} else {
				s.m.ok = false
			}
		}
	}
	s.follow(a)
	// names change when contacts do
	st := a.Model
	if s.r.have && s.r.namesGen != st.Contacts.Gen()+st.Chats.Gen()<<16 {
		s.rebuild(a)
	}
	return true
}

func (s *Screen) loadMessage(a *state.Account, m *abi.Message, conv, gen uint32, idx int) {
	x := &s.m
	x.ok, x.conv, x.gen, x.index = true, conv, gen, idx
	x.fromMe = m.FromMe
	x.direct = !chatIsGroup(s.chat[:s.chatN])
	x.status = m.Status
	x.delivery = m.Status
	if a.Model.ReceiptsOff() && x.direct && x.fromMe && m.Status == abi.DeliveryRead {
		x.delivery = abi.DeliveryDelivered
	}
	x.deliveredAt, x.hasDeliv = m.DeliveredAt, m.HasDeliveredAt
	x.readAt, x.hasRead = m.ReadAt, m.HasReadAt
	x.ts = m.Timestamp
	x.summary = summary(m)
	x.clock = clock(s.now(), m.Timestamp)
}

// header is what the pane's header says this frame.
type header struct {
	have  bool
	ic    icon.ID
	label string
}

// Toolbar is the pane's header in the window's toolbar band, over the pane
// (screen.ToolbarPart): the shell calls it after the conversation's items, in
// the inspector section. The shell is the one that opens the band, so until it
// calls this the pane builds its header itself.
func (s *Screen) Toolbar(e *screen.Env, tb *rexui.Toolbar) {
	h := s.hdr
	if !h.have {
		return
	}
	s.inBand = true
	btn := tb.Item(h.ic, h.label)
	k, c := e.K, e.C()
	tb.BeginCustom("message-info-title", ui.Children(1))
	c.NextSize(ui.Children(1), ui.Px(tb.Height()))
	c.NextAxis(ui.AxisX)
	c.NextCross(ui.CrossCenter)
	c.NextPadding(e.Pt(8), 0, e.Pt(8), 0)
	c.Push(c.Box(0, "message-info-title-row"))
	rexui.Label(k.Kit, i18n.T("Message info"), rexui.LabelOpts{Style: rexui.TextHeadline})
	c.Pop()
	tb.EndCustom()
	if btn.Clicked {
		s.closePane(e)
	}
}

// close closes the pane.
func (s *Screen) closePane(e *screen.Env) {
	e.S.Panes.Pane = state.PaneNone
	e.S.Req.FocusComposer = true
	e.S.Touch()
}

// Build builds the pane at r.
func (s *Screen) Build(e *screen.Env, r screen.Rect) {
	a := e.Acct()
	if a == nil || !a.Model.Linked() || !s.sync(e, a) {
		return
	}
	k := e.K
	s.hdr = header{}
	s.bandNow, s.inBand = s.inBand, false // Toolbar says again, after this, if the shell still hosts it
	e.Begin(r, "Message info")
	k.NextGap(0)
	k.BeginContainer(ui.AxisY, ui.CrossStart, "message info", ui.Px(r.W), ui.Px(r.H), 0)
	ic, label := s.ic.closeX, i18n.T("Close")
	if e.S.Panes.Covering {
		ic, label = s.ic.back, i18n.T("Back")
	}
	s.hdr = header{have: true, ic: ic, label: label}
	if !s.bandNow {
		k.NextPadding(margin, 10, margin, 10)
		k.NextGap(6)
		k.BeginContainer(ui.AxisX, ui.CrossCenter, "header", ui.Pct(1), k.Fixed(headerPt), 0)
		closed := rexui.IconButton(k.Kit, ic, label, rexui.IconButtonOpts{Variant: rexui.IconBorderless}).Clicked
		rexui.Label(k.Kit, i18n.T("Message info"), rexui.LabelOpts{Style: rexui.TextTitle3})
		k.EndContainer()
		rexui.Separator(k.Kit, rexui.SeparatorOpts{Hairline: true})
		if closed {
			s.closePane(e)
		}
	}
	if !s.m.ok {
		k.NextPadding(margin, 12, margin, 0)
		k.BeginContainer(ui.AxisY, ui.CrossStart, "gone", ui.Pct(1), ui.Children(1), 0)
		rexui.Label(k.Kit, i18n.T("This message is no longer available."), rexui.LabelOpts{Style: rexui.TextBody, Color: rexui.Of(theme.Label2), Wrap: true})
		k.EndContainer()
	} else {
		k.NextSize(ui.Pct(1), widget.Fill)
		v := k.BeginScroll(i18n.T("Receipts"), &s.scroll, base.ScrollVertical)
		k.NextPadding(margin, 8, margin, 8)
		k.NextGap(0)
		k.BeginContainer(ui.AxisY, ui.CrossStart, "content", ui.Pct(1), ui.Children(1), 0)
		s.preview(e, r)
		if s.m.direct {
			s.direct(e)
		} else {
			s.group(e, a)
		}
		k.EndContainer()
		k.EndScroll(v)
	}
	k.EndContainer()
	e.End()
}

// preview is the message as its bubble shows it, shortened to a few lines,
// with its time and ticks.
func (s *Screen) preview(e *screen.Env, r screen.Rect) {
	k := e.K
	c := e.C()
	colW := r.W/k.Scale() - 2*margin - 17
	if s.stylesK != k.Scale() || s.styles.N == 0 {
		variant := rexui.BubbleOutgoing
		rexui.BubbleRichStyles(k.Kit, variant, &s.styles)
		s.stylesK = k.Scale()
	}
	body := shorten(s.m.summary, int(colW/7)*4)
	variant := rexui.BubbleOutgoing
	if !s.m.fromMe {
		variant = rexui.BubbleIncoming
	}
	k.NextPadding(0, 4, 0, 4)
	k.BeginContainer(ui.AxisY, ui.CrossStart, "bubble", ui.Pct(1), ui.Children(1), 0)
	rexui.MessageBubble(k.Kit, ui.KeyN(c.ParentKey(), 1), base.StrBytes(body), nil, rexui.MessageOpts{
		BubbleOpts: rexui.BubbleOpts{Variant: variant, Tail: true, ColumnW: colW, Name: i18n.T("Message")},
		Styles:     &s.styles,
	})
	k.NextJustify(ui.JustifyEnd)
	k.NextGap(4)
	k.BeginContainer(ui.AxisX, ui.CrossCenter, "meta", ui.Pct(1), ui.Children(1), 0)
	rexui.Label(k.Kit, s.m.clock, rexui.LabelOpts{Style: rexui.TextCallout, Color: rexui.Of(theme.Label2)})
	if s.m.fromMe {
		s.ticks(e, s.m.delivery)
	}
	k.EndContainer()
	k.EndContainer()
}

// shorten cuts text to n runes with an ellipsis.
func shorten(t string, n int) string {
	if n < 8 {
		n = 8
	}
	i := 0
	for j := range t {
		if i == n {
			return t[:j] + "…"
		}
		i++
	}
	return t
}

// ticks draws the delivery state's ticks: one when sent, two when delivered
// and in the read colour when read or played.
func (s *Screen) ticks(e *screen.Env, d abi.Delivery) {
	var glyph string
	col := theme.Label2
	switch d {
	case abi.DeliveryPending:
		glyph = "◷"
	case abi.DeliverySent:
		glyph = "✓"
	case abi.DeliveryDelivered:
		glyph = "✓✓"
	case abi.DeliveryRead, abi.DeliveryPlayed:
		glyph, col = "✓✓", theme.Accent
	case abi.DeliveryFailed:
		glyph, col = "!", theme.Red
	default:
		return
	}
	rexui.Label(e.K.Kit, glyph, rexui.LabelOpts{Style: rexui.TextCallout, Color: rexui.Of(col)})
}

// direct is a direct chat's single recipient: the times its ticks turned.
func (s *Screen) direct(e *screen.Env) {
	k := e.K
	d := s.m.delivery
	read := d == abi.DeliveryRead || d == abi.DeliveryPlayed
	delivered := read || d == abi.DeliveryDelivered
	label := i18n.T("Read")
	if d == abi.DeliveryPlayed {
		label = i18n.T("Played")
	}
	now := s.now()
	stages := [2]struct {
		name    string
		reached bool
		at      int64
		has     bool
		col     theme.Role
	}{
		{label, read, s.m.readAt, s.m.hasRead, theme.Label2},
		{i18n.T("Delivered"), delivered, s.m.deliveredAt, s.m.hasDeliv, theme.Label2},
	}
	if read {
		stages[0].col = theme.Accent
	}
	for i, st := range stages {
		when := "—"
		switch {
		case st.reached && st.has && st.at > 0:
			when = momentStamp(now, st.at)
		case st.reached:
			when = i18n.T("Time not recorded")
		}
		k.NextPadding(0, 4, 0, 0)
		k.NextGap(8)
		k.BeginContainer(ui.AxisX, ui.CrossStart, "stage "+itoa(i), ui.Pct(1), ui.Children(1), 0)
		rexui.Label(k.Kit, "✓✓", rexui.LabelOpts{Style: rexui.TextBody, Color: rexui.Of(st.col)})
		k.NextGap(2)
		k.BeginContainer(ui.AxisY, ui.CrossStart, "text", ui.Children(1), ui.Children(1), 0)
		rexui.Label(k.Kit, st.name, rexui.LabelOpts{Style: rexui.TextSemibold})
		rexui.Label(k.Kit, when, rexui.LabelOpts{Style: rexui.TextCallout, Color: rexui.Of(theme.Label2)})
		k.EndContainer()
		k.EndContainer()
	}
}

// group lists who played, read and received a group message.
func (s *Screen) group(e *screen.Env, a *state.Account) {
	k := e.K
	if !s.r.have {
		note(e, "loading", i18n.T("Loading…"), 12)
		return
	}
	r := &s.r
	if r.empty {
		note(e, "none", i18n.T("No receipts were recorded for this message. Details are available for messages sent from now on."), 8)
		return
	}
	if !r.known {
		note(e, "partial", i18n.T("Some receipts for this message were not recorded. Details are complete for messages sent from now on."), 8)
	}
	if len(r.sections[0]) > 0 {
		s.section(e, a, 0, i18n.T("Played by"), theme.Accent)
	}
	s.section(e, a, 1, i18n.T("Read by"), theme.Accent)
	s.section(e, a, 2, i18n.T("Delivered to"), theme.Label2)
	if r.remaining > 0 {
		k.NextPadding(0, 12, 0, 0)
		k.BeginContainer(ui.AxisX, ui.CrossStart, "remaining", ui.Pct(1), ui.Children(1), 0)
		if s.remainText == "" || s.remainN != r.remaining || s.remainLang != i18n.Current() {
			s.remainN, s.remainLang = r.remaining, i18n.Current()
			s.remainText = i18n.Format(i18n.N("{} remaining", "{} remaining", r.remaining), "{}", itoa(r.remaining))
		}
		rexui.Label(k.Kit, s.remainText, rexui.LabelOpts{Style: rexui.TextBody, Color: rexui.Of(theme.Label2)})
		k.EndContainer()
	}
}

func note(e *screen.Env, name, text string, top float32) {
	k := e.K
	k.NextPadding(0, top, 0, 0)
	k.BeginContainer(ui.AxisX, ui.CrossStart, name, ui.Pct(1), ui.Children(1), 0)
	rexui.Label(k.Kit, text, rexui.LabelOpts{Style: rexui.TextBody, Color: rexui.Of(theme.Label2), Wrap: true})
	k.EndContainer()
}

// section is one titled list of recipients, or "No one yet".
func (s *Screen) section(e *screen.Env, a *state.Account, sec int, title string, col theme.Role) {
	k := e.K
	list := s.r.sections[sec]
	k.NextPadding(0, 12, 0, 0)
	k.NextGap(6)
	k.BeginContainer(ui.AxisY, ui.CrossStart, "section "+itoa(sec), ui.Pct(1), ui.Children(1), 0)
	k.NextGap(6)
	k.BeginContainer(ui.AxisX, ui.CrossCenter, "title", ui.Pct(1), ui.Children(1), 0)
	rexui.Label(k.Kit, "✓✓", rexui.LabelOpts{Style: rexui.TextCallout, Color: rexui.Of(col)})
	rexui.Label(k.Kit, title, rexui.LabelOpts{Style: rexui.TextSemibold})
	k.EndContainer()
	if len(list) == 0 {
		rexui.Label(k.Kit, i18n.T("No one yet"), rexui.LabelOpts{Style: rexui.TextCallout, Color: rexui.Of(theme.Label2)})
	}
	for i := range list {
		s.row(e, a, sec, i, &list[i])
	}
	k.EndContainer()
}

func (s *Screen) row(e *screen.Env, a *state.Account, sec, i int, rc *recipient) {
	k := e.K
	id := s.r.buf[rc.id.off : rc.id.off+rc.id.n]
	k.NextGap(10)
	k.BeginContainer(ui.AxisX, ui.CrossCenter, "recipient "+itoa(i), ui.Pct(1), ui.Children(1), 0)
	opts := rexui.AvatarOpts{Size: avatarPt, Image: screen.Avatar(k.Images(), a, id, false, int32(k.Pt(avatarPt)))}
	name := s.r.str(rc.name)
	rexui.Avatar(k.Kit, strings.TrimPrefix(name, "~"), opts)
	k.NextGap(2)
	k.BeginContainer(ui.AxisY, ui.CrossStart, "text", ui.Children(1), ui.Children(1), 0)
	rexui.Label(k.Kit, name, rexui.LabelOpts{Style: rexui.TextBody})
	if rc.when.n > 0 {
		rexui.Label(k.Kit, s.r.str(rc.when), rexui.LabelOpts{Style: rexui.TextCallout, Color: rexui.Of(theme.Label2)})
	}
	k.EndContainer()
	k.EndContainer()
}
