package login

import (
	"strings"

	"gonativeui/icon"
	"gonativeui/qr"
	"gonativeui/rexui"
	"gonativeui/theme"
	"gonativeui/ui"
	"gonativeui/widget/base"

	"zapfast/abi"
	"zapfast/i18n"
	"zapfast/model"
	"zapfast/state"
	"zapfast/ui/accounts/acct"
	"zapfast/ui/screen"
)

const (
	cardWidth = 460 // points
	qrSide    = 260
	minDigits = 7 // the shortest number "Get a code" takes
)

func tr(s string) string { return i18n.T(s) }

// Screen is the login screen.
type Screen struct {
	code     qr.Code // the payload drawn, encoded once per account and status
	codeAcct uint64  // the account's Serial
	codeGen  uint32
	codeOK   bool
	coded    bool
	// acct is the account (Serial) the screen's own state below belongs to:
	// another account, or a relinked one, starts without it.
	acct uint64

	phoning       bool // asking for the number to pair with
	phone         ui.TextBuffer
	focusPhone    bool
	confirmStart  bool // "Start over?" is open
	confirmRemove bool // "Remove this account?" is open
	toRemove      *state.Account

	scroll                 Scroller
	card                   rexui.Card
	phoneIc, alertIc, copy icon.ID
}

var _ screen.Pane = (*Screen)(nil)

// New returns the screen.
func New() *Screen { return &Screen{} }

// Init readies the screen for a window.
func (s *Screen) Init(e *screen.Env) error {
	s.phoneIc = e.K.AddIcon(icon.Spec{Symbol: "iphone", Fallback: '☎', Pt: 28})
	s.alertIc = e.K.AddIcon(icon.Spec{Symbol: "exclamationmark.circle", Fallback: '!', Pt: 28})
	s.copy = e.K.AddIcon(icon.Spec{Symbol: "doc.on.doc", Fallback: '⧉', Pt: 13})
	return nil
}

// Build builds the linking screen at r: the centred card with the logo's
// name, a line under it and, by the account's link status, what to do next.
func (s *Screen) Build(e *screen.Env, r screen.Rect) {
	k := e.K
	e.Begin(r, "Login")
	s.scroll.Begin(e, r, "Login")
	k.NextWidth(ui.Px(min(e.Pt(cardWidth), max(r.W-e.Pt(24), 0))))
	s.card = rexui.BeginCard(k.Kit, "card", rexui.CardOpts{Variant: rexui.CardBox})
	k.NextPad(20)
	k.NextGap(8)
	k.BeginContainer(ui.AxisY, ui.CrossCenter, "body", ui.Pct(1), ui.Children(1), 0)
	// the spaces are tight enough for the card, the QR code with it, to fit
	// a window of the default 1100 x 720 points with its gutter
	Mark(e)
	rexui.Label(k.Kit, "Zap", rexui.LabelOpts{Style: rexui.TextTitle1})
	rexui.Label(k.Kit, tr("A native WhatsApp client."), rexui.LabelOpts{Color: rexui.Of(theme.Label2)})
	k.Space(4)
	s.body(e)
	s.choices(e)
	k.Space(2)
	rexui.Label(k.Kit, tr("Unofficial client. Using it may be against WhatsApp's terms of service."),
		rexui.LabelOpts{Style: rexui.TextCaption, Color: rexui.Of(theme.Label2), Wrap: true})
	k.EndContainer() // body
	rexui.EndCard(k.Kit, &s.card)
	s.scroll.End(e)
	s.dialogs(e)
	e.End()
}

// body is the card's content by the link status.
func (s *Screen) body(e *screen.Env) {
	k := e.K
	a := e.Acct()
	if a == nil {
		busy(e, tr("Connecting to WhatsApp…"))
		return
	}
	if a.Serial != s.acct {
		s.acct = a.Serial
		s.phoning, s.focusPhone, s.confirmStart = false, false, false
		s.phone.Reset()
	}
	if a.Err != "" {
		s.failed(e, a, a.Err)
		return
	}
	l := &a.Model.Link
	switch l.Status() {
	case abi.LinkStatusTagStarting, abi.LinkStatusTagConnecting:
		busy(e, tr("Connecting to WhatsApp…"))
	case abi.LinkStatusTagConnected, abi.LinkStatusTagDisconnected:
		busy(e, tr("Linked. Waiting for your chats…"))
	case abi.LinkStatusTagLoggedOut:
		k.Icon(s.phoneIc, k.Theme().Colors[theme.Orange])
		rexui.Label(k.Kit, tr("This computer was unlinked from your phone. Requesting a new code."), rexui.LabelOpts{Wrap: true})
		k.Space(8)
		busy(e, tr("Requesting a new code…"))
	case abi.LinkStatusTagFailed:
		s.failed(e, a, model.S(l.Error()))
	default: // Unlinked
		code, hasCode := l.PairCode()
		phone, hasPhone := l.PairingPhone()
		qrp, hasQR := l.QR()
		switch {
		case s.phoning:
			s.phoneEntry(e, a)
		case hasCode:
			s.pairCode(e, code, phone, hasPhone)
		case hasPhone:
			busy(e, i18n.Format(tr("Requesting a code for +{phone}…"), "{phone}", model.S(phone)))
		case hasQR:
			s.qrView(e, a, qrp)
		default:
			busy(e, tr("Waiting for a code from WhatsApp…"))
		}
	}
}

// busy is a spinner and what is being waited for.
func busy(e *screen.Env, label string) {
	k := e.K
	k.NextGap(8)
	k.BeginContainer(ui.AxisX, ui.CrossCenter, "busy", ui.Children(1), ui.Children(1), 0)
	rexui.Spinner(k.Kit, tr("Busy")+"###spin", rexui.SpinnerOpts{Size: theme.SizeSmall})
	rexui.Label(k.Kit, label, rexui.LabelOpts{Style: rexui.TextHeadline})
	k.EndContainer()
}

// failed is the link's error, with Try again, and Start over… when the
// archive's key is lost for good (a locked or unavailable keyring is not
// that: Try again can fix it, and nothing connects until it opens).
func (s *Screen) failed(e *screen.Env, a *state.Account, message string) {
	k := e.K
	k.Icon(s.alertIc, k.Theme().Colors[theme.Red])
	rexui.Label(k.Kit, message, rexui.LabelOpts{Color: rexui.Of(theme.Red), Wrap: true})
	k.Space(8)
	k.NextGap(8)
	k.BeginContainer(ui.AxisX, ui.CrossCenter, "actions", ui.Children(1), ui.Children(1), 0)
	if rexui.Button(k.Kit, tr("Try again"), rexui.ButtonOpts{Variant: rexui.ButtonPrimary, Size: theme.SizeLarge}).Clicked {
		send(a, abi.CommandTagReconnect)
	}
	if archiveKeyLost(message) {
		if rexui.Button(k.Kit, tr("Start over…"), rexui.ButtonOpts{Size: theme.SizeLarge}).Clicked {
			s.confirmStart = true
		}
	}
	k.EndContainer()
}

// archiveKeyLost reports whether the archive's key is gone for good, rather
// than the keyring being locked or unavailable, which "Try again" can fix.
func archiveKeyLost(message string) bool {
	return strings.Contains(message, "OS keyring key is missing") ||
		strings.Contains(message, "archive key in the OS keyring is invalid") ||
		strings.Contains(message, "could not be unlocked with its OS keyring key")
}

func send(a *state.Account, tag abi.CommandTag) {
	a.Send(a.Cmd(tag))
}

// step is one numbered line of the instructions.
func step(e *screen.Env, n int, text string) {
	k := e.K
	k.NextGap(6)
	k.BeginContainer(ui.AxisX, ui.CrossCenter, text, ui.Pct(1), ui.Children(1), 0)
	rexui.Label(k.Kit, e.C().Fmt().Int(n).Str(".").String(), rexui.LabelOpts{Style: rexui.TextSemibold, Color: rexui.Of(theme.Accent)})
	rexui.Label(k.Kit, text, rexui.LabelOpts{Color: rexui.Of(theme.Label2), Wrap: true})
	k.EndContainer()
}

// qrView is the QR code to scan and the three steps, with the way to link by
// phone number instead.
func (s *Screen) qrView(e *screen.Env, a *state.Account, payload []byte) {
	k := e.K
	c := e.C()
	rexui.Label(k.Kit, tr("Link this computer"), rexui.LabelOpts{Style: rexui.TextHeadline})
	// Link.Gen is the account's own: another account's may be the same.
	if g := a.Model.Link.Gen(); !s.coded || g != s.codeGen || a.Serial != s.codeAcct {
		s.coded, s.codeGen, s.codeAcct = true, g, a.Serial
		s.codeOK = qr.Encode(&s.code, payload, qr.LevelM) == nil
	}
	if s.codeOK {
		// Scanners want dark on light, whatever the appearance.
		c.NextRole(ui.RoleImage)
		c.NextSemName(tr("Link this computer"))
		qr.Draw(c, ui.HashString(c.ParentKey(), "qr"), &s.code, e.Pt(qrSide), 0x000000FF, 0xFFFFFFFF)
	} else {
		k.Icon(s.alertIc, k.Theme().Colors[theme.Red])
	}
	k.Space(4)
	step(e, 1, tr("Open WhatsApp on your phone"))
	step(e, 2, tr("Tap Menu or Settings, then Linked devices"))
	step(e, 3, tr("Tap Link a device and point the phone at this code"))
	k.Space(6)
	if rexui.Link(k.Kit, tr("Link with phone number instead"), rexui.LinkOpts{}).Clicked {
		s.phoning, s.focusPhone = true, true
		s.phone.Reset()
	}
}

// pairCode is the eight-character code to type on the phone, with its steps.
func (s *Screen) pairCode(e *screen.Env, code, phone []byte, hasPhone bool) {
	k := e.K
	c := e.C()
	rexui.Label(k.Kit, tr("Enter this code on your phone"), rexui.LabelOpts{Style: rexui.TextHeadline})
	if hasPhone {
		rexui.Label(k.Kit, i18n.Format(tr("for +{phone}"), "{phone}", model.S(phone)), rexui.LabelOpts{Color: rexui.Of(theme.Label2)})
	}
	k.Space(4)
	f := c.Fmt()
	if len(code) == 8 && !strings.Contains(model.S(code), "-") {
		f.Str(model.S(code[:4])).Byte('-').Str(model.S(code[4:]))
	} else {
		f.Str(model.S(code))
	}
	shown := f.String()
	r := k.Theme()
	c.NextBG(r.Colors[theme.Fill3])
	c.NextRadius(e.Pt(10))
	k.NextPadding(22, 10, 22, 10)
	k.BeginContainer(ui.AxisY, ui.CrossCenter, "code", ui.Children(1), ui.Children(1), ui.DrawBackground)
	rexui.Label(k.Kit, shown, rexui.LabelOpts{Style: rexui.TextTitle1, MonoPt: 22})
	k.EndContainer()
	k.Space(4)
	step(e, 1, tr("Open WhatsApp on your phone"))
	step(e, 2, tr("Tap Menu or Settings, then Linked devices"))
	step(e, 3, tr("Tap Link a device, then Link with phone number instead"))
	k.Space(6)
	if rexui.ButtonIcon(k.Kit, s.copy, tr("Copy code"), rexui.ButtonOpts{Size: theme.SizeLarge}).Clicked {
		if c.ClipboardSet != nil {
			c.ClipboardSet(code)
		}
		e.S.Toast([]byte(tr("Copied")), false)
	}
}

// phoneEntry asks for the number to pair with: its country code, no plus or
// leading zero (ui/dialogs.rs pair_with_phone; here, where the dialogs are not
// drawn).
func (s *Screen) phoneEntry(e *screen.Env, a *state.Account) {
	k := e.K
	c := e.C()
	rexui.Label(k.Kit, tr("Link with a phone number"), rexui.LabelOpts{Style: rexui.TextHeadline})
	rexui.Label(k.Kit, tr("Enter the WhatsApp phone number with its country code. Do not include a plus sign or leading zero. You will get a code to enter on the phone."),
		rexui.LabelOpts{Color: rexui.Of(theme.Label2), Wrap: true})
	k.Space(4)
	name := tr("Phone number")
	key := base.FieldKey(c.ParentKey(), name)
	if s.focusPhone {
		s.focusPhone = false
		c.SetFocus(key)
	}
	sig := rexui.TextField(k.Kit, name, &s.phone, "15551234567", rexui.FieldOpts{Size: theme.SizeLarge})
	digits := 0
	for _, b := range s.phone.Bytes() {
		if b >= '0' && b <= '9' {
			digits++
		}
	}
	// Stay the main action, but read as unavailable until the number is long
	// enough.
	ready := digits >= minDigits
	k.Space(6)
	k.NextGap(8)
	k.BeginContainer(ui.AxisX, ui.CrossCenter, "actions", ui.Pct(1), ui.Children(1), 0)
	k.Spacer()
	cancel := rexui.Button(k.Kit, tr("Cancel"), rexui.ButtonOpts{Size: theme.SizeLarge}).Clicked
	k.BeginDisabled(!ready)
	get := rexui.Button(k.Kit, tr("Get a code"), rexui.ButtonOpts{Variant: rexui.ButtonPrimary, Size: theme.SizeLarge})
	k.EndDisabled()
	k.EndContainer()
	switch {
	case cancel || c.TakeKey(ui.KeyEscape, 0):
		s.phoning = false
	case (get.Clicked || sig.Submitted) && ready:
		s.pairWith(e, a)
	}
}

// pairWith is Action::PairWithPhone: the digits of the number go to the core.
func (s *Screen) pairWith(e *screen.Env, a *state.Account) {
	var digits []byte
	for _, b := range s.phone.Bytes() {
		if b >= '0' && b <= '9' {
			digits = append(digits, b)
		}
	}
	if len(digits) < minDigits {
		e.S.Toast([]byte(tr("Enter the phone number with its country code, using digits only")), true)
		return
	}
	cmd := a.Cmd(abi.CommandTagPairWithPhone)
	cmd.PairWithPhone.Phone = digits
	a.Send(cmd)
	s.phoning = false
	s.phone.Reset()
}

// choices are the ways back with several accounts here: Cancel when adding a
// number, else Use another account, and for a number the phone unlinked,
// Remove this account (zf:src/ui/accounts.rs login_choices).
func (s *Screen) choices(e *screen.Env) {
	st, k := e.S, e.K
	if !acct.Several(st) {
		return
	}
	k.Space(6)
	k.NextGap(24)
	k.BeginContainer(ui.AxisX, ui.CrossCenter, "choices", ui.Children(1), ui.Children(1), 0)
	back := tr("Use another account")
	if st.AddingAccount {
		back = tr("Cancel")
	}
	if rexui.Link(k.Kit, back, rexui.LinkOpts{}).Clicked {
		if st.AddingAccount {
			acct.CancelAdd(st)
		} else {
			st.AccountMenu = true
			st.Touch()
		}
	}
	if !st.AddingAccount {
		if rexui.Link(k.Kit, tr("Remove this account"), rexui.LinkOpts{}).Clicked {
			s.confirmRemove, s.toRemove = true, e.Acct()
		}
	}
	k.EndContainer()
}

// dialogs are the two confirmations: Start over and Remove this account. Enter
// is deliberately not bound to either: a stray key must not destroy anything.
func (s *Screen) dialogs(e *screen.Env) {
	k := e.K
	if s.confirmStart {
		d := rexui.BeginDialog(k.Kit, "start-over", &s.confirmStart, rexui.DialogOpts{Title: tr("Start over?"), Width: 380})
		if d.Visible {
			rexui.Label(k.Kit, tr("Zap keeps your unreadable archive as a separate file, creates a new one, and asks you to link again. Linking again brings back recent history from your phone. Afterwards, remove the old ZapFast entry under Linked devices on your phone."), rexui.LabelOpts{Wrap: true})
			rexui.Label(k.Kit, tr("If you can restore the original keyring instead, choose Cancel and Try again: nothing is lost that way."), rexui.LabelOpts{Style: rexui.TextCallout, Color: rexui.Of(theme.Label2), Wrap: true})
			d.BeginActions()
			d.Action(tr("Cancel"), rexui.ButtonDefault)
			if d.Action(tr("Start over"), rexui.ButtonDestructive) {
				if a := e.Acct(); a != nil {
					send(a, abi.CommandTagStartOverArchive)
				}
			}
			d.EndActions()
		}
		rexui.EndDialog(k.Kit, &d)
	}
	if s.confirmRemove {
		d := rexui.BeginDialog(k.Kit, "remove-account", &s.confirmRemove, rexui.DialogOpts{Title: tr("Remove this account?"), Width: 380})
		if d.Visible {
			rexui.Label(k.Kit, tr("This unlinks the number on this computer and deletes its local chats. Other accounts stay."), rexui.LabelOpts{Wrap: true})
			d.BeginActions()
			d.Action(tr("Cancel"), rexui.ButtonDefault)
			if d.Action(tr("Remove"), rexui.ButtonDestructive) {
				if a := s.toRemove; a != nil {
					acct.Remove(e.S, a)
				}
			}
			d.EndActions()
		}
		rexui.EndDialog(k.Kit, &d)
	}
}
