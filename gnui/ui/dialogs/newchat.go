package dialogs

import (
	"bytes"
	"slices"
	"strings"

	"gonativeui/img"
	"gonativeui/rexui"
	"gonativeui/ui"
	"gonativeui/widget"
	"gonativeui/widget/base"

	"zapfast/abi"
	"zapfast/format"
	"zapfast/i18n"
	"zapfast/lockhash"
	"zapfast/model"
	"zapfast/state"
	"zapfast/ui/screen"
)

// ---- starting a chat ----

// startChat opens the chat with jid, asking the core to make it first when it
// is not in the list yet (upstream's StartChat).
func startChat(a *state.Account, jid []byte, name string) {
	a.StartChat(jid, []byte(name), nil)
}

// ---- New chat ----

type contactRow struct {
	id          []byte
	name, phone string
	self        bool
}

type newChat struct {
	search ui.TextBuffer
	rows   []contactRow
	me     contactRow // the "message yourself" row
	needle string     // the search, folded
	rev    uint32
	gens   [3]uint32
	built  bool
	focus  bool
}

func (n *newChat) reset() {
	n.search.Reset()
	n.built = false
}

// offersSelf reports whether the search offers the chat with ourselves, by
// our name, number, "You" or "Message yourself" (zf:src/app.rs:2532).
func offersSelf(a *state.Account, needle string) bool {
	me := a.Model.Me.ID()
	if len(me) == 0 {
		return false
	}
	if ref := a.Model.Chats.Find(me); ref != 0 {
		if c := a.Model.Chats.At(ref); c.Locked() {
			return false
		}
	}
	if needle == "" {
		return true
	}
	for _, c := range []string{i18n.T("Message yourself"), i18n.T("You"), "message yourself", "you", string(a.Model.Me.Name()), string(model.Phone(me))} {
		if strings.Contains(format.Fold(c), needle) {
			return true
		}
	}
	return false
}

func selfTitle(a *state.Account) string {
	if n := a.Model.Me.Name(); len(strings.TrimSpace(string(n))) > 0 {
		return i18n.Format(i18n.T("{name} (You)"), "{name}", string(n))
	}
	return i18n.T("You")
}

// contactChoices lists the people New chat and New call offer, by name: the
// contacts and the one-to-one chats, without ourselves or a locked chat,
// matching needle (a fold) by name or number (zf:src/ui/dialogs.rs:449).
func contactChoices(dst []contactRow, a *state.Account, needle string) []contactRow {
	dst = dst[:0]
	m := a.Model
	me := m.Me.ID()
	seen := map[string]bool{}
	add := func(id []byte) {
		phone := model.Phone(id)
		if phone == nil || string(id) == string(me) || seen[string(id)] {
			return
		}
		if ref := m.Chats.Find(id); ref != 0 && m.Chats.At(ref).Locked() {
			return
		}
		name := string(m.DisplayName(id))
		if needle != "" && !strings.Contains(format.Fold(name), needle) && !strings.Contains(string(phone), needle) {
			return
		}
		seen[string(id)] = true
		dst = append(dst, contactRow{id: append([]byte(nil), id...), name: name, phone: format.Phone(model.S(phone))})
	}
	for i := 0; i < m.Contacts.Len(); i++ {
		add(m.Contacts.At(i).ID())
	}
	for _, ref := range m.Chats.Order() {
		if c := m.Chats.At(ref); c.Kind() == abi.ChatKindDirect {
			add(c.ID())
		}
	}
	slices.SortFunc(dst, func(x, y contactRow) int { return strings.Compare(format.Fold(x.name), format.Fold(y.name)) })
	return dst
}

// avatar is the picture of jid, asked for once; the zero handle until it is
// ready.
func (s *Screen) avatar(a *state.Account, jid []byte) img.Handle {
	return screen.Avatar(s.imgs, a, jid, false, 96)
}

// listHeight is the height in points of a dialog's scrolling list.
func listHeight(e *screenEnv, rowPt float32) float32 {
	_, h := winPt(e)
	return min(max(h-280, rowPt*3), 420)
}

func (s *Screen) newChat(e *screenEnv, a *state.Account, dl *rexui.Dialog) {
	k, c, n := e.K, e.C(), &s.chat
	addContact := rexui.Button(k.Kit, i18n.T("Add contact"), rexui.ButtonOpts{}).Clicked
	sig := rexui.SearchField(k.Kit, i18n.T("Search contacts"), &n.search, i18n.T("Search contacts"), rexui.FieldOpts{})
	if s.age == 2 {
		c.SetFocus(sig.Key)
	}
	gens := [3]uint32{a.Model.Contacts.Gen(), a.Model.Chats.Gen(), a.Model.Me.Gen()}
	if !n.built || n.rev != n.search.Rev() || n.gens != gens {
		n.needle = format.Fold(strings.TrimSpace(n.search.String()))
		n.rows = contactChoices(n.rows, a, n.needle)
		n.me = contactRow{id: append([]byte(nil), a.Model.Me.ID()...), name: selfTitle(a), phone: i18n.T("Message yourself"), self: true}
		n.built, n.rev, n.gens = true, n.search.Rev(), gens
	}
	self := offersSelf(a, n.needle)
	total := len(n.rows)
	if self {
		total++
	}
	if total == 0 {
		para(k, i18n.T("No matching contacts"), rexui.TextCallout, secondary)
	}
	const rowPt = 56
	k.NextHeight(k.Fixed(min(listHeight(e, rowPt), float32(max(total, 1))*rowPt+6)))
	v := k.BeginScroll("new-chat-contacts", &s.scroll, widget.ScrollVertical)
	first, last := c.ListRange(v.Inner, total, k.Pt(rowPt), 2)
	var pick []byte
	var pickName string
	var pickSelf bool
	for i := first; i < last; i++ {
		var r contactRow
		if self && i == 0 {
			r = n.me
		} else {
			j := i
			if self {
				j--
			}
			r = n.rows[j]
		}
		label := c.Fmt().Str("contact").Int(i).String()
		opts := rexui.ChatRowOpts{Image: s.avatar(a, r.id)}
		if rexui.ChatListRow(k.Kit, label, r.name, r.phone, "", opts).Clicked {
			pick, pickName, pickSelf = r.id, r.name, r.self
		}
	}
	k.EndScroll(v)
	beginActions(e)
	dl.Action(i18n.T("Cancel"), rexui.ButtonDefault)
	endActions(e)
	if addContact {
		s.next(e, state.Dialog{Kind: state.DialogNewContact})
		return
	}
	if pick == nil {
		return
	}
	if pickSelf && locked(a, pick) {
		s.next(e, state.Dialog{Kind: state.DialogUnlockLockedChats})
		return
	}
	startChat(a, pick, pickName)
	s.open = false
}

func locked(a *state.Account, jid []byte) bool {
	ref := a.Model.Chats.Find(jid)
	return ref != 0 && a.Model.Chats.At(ref).Locked()
}

// ---- New contact ----

const (
	invalidNumber  = "Enter a phone number with its country code, using only digits, spaces, hyphens and brackets"
	checkingNumber = "Checking the number…"
)

type newContact struct {
	phone, first, last ui.TextBuffer
	toPhone            bool
	pending            bool
	request, seq       uint64
}

func (n *newContact) reset(e *screenEnv) {
	n.phone.Reset()
	n.first.Reset()
	n.last.Reset()
	n.pending, n.request = false, 0
	n.toPhone = false
	if a := e.Acct(); a != nil {
		n.toPhone = a.Settings.SaveContactsToPhone
	}
}

// cancel forgets a lookup the person walked away from.
func (n *newContact) cancel(a *state.Account) {
	n.pending, n.request = false, 0
}

func (s *Screen) newContact(e *screenEnv, a *state.Account, dl *rexui.Dialog) {
	k, c, n := e.K, e.C(), &s.contact
	para(k, i18n.T("Enter a phone number with its country code. Add a name to save the contact, or leave it blank to message the number without saving it. WhatsApp uses the first name as the display name."), rexui.TextCallout, secondary)
	pf := rexui.TextField(k.Kit, i18n.T("Phone number"), &n.phone, "+1 555 123 4567", rexui.FieldOpts{})
	if s.age == 2 {
		c.SetFocus(pf.Key)
	}
	k.NextGap(8)
	k.BeginContainer(ui.AxisX, ui.CrossCenter, "names", ui.Pct(1), ui.Children(1), 0)
	k.NextWidth(widget.Fill)
	ff := rexui.TextField(k.Kit, i18n.T("First name"), &n.first, i18n.T("First name"), rexui.FieldOpts{})
	k.NextWidth(widget.Fill)
	lf := rexui.TextField(k.Kit, i18n.T("Surname"), &n.last, i18n.T("Surname"), rexui.FieldOpts{})
	k.EndContainer()

	digits, valid := InternationalPhone(n.phone.String())
	ready := valid && !n.pending
	if !valid && n.phone.Len() > 0 {
		// Upstream says this on the disabled buttons' tooltip.
		para(k, i18n.T(invalidNumber), rexui.TextCaption, secondary)
	}
	full, short, named := ComposeName(n.first.String(), n.last.String())

	k.BeginDisabled(!named)
	rexui.Checkbox(k.Kit, i18n.T("Also save to your phone's contacts"), &n.toPhone, rexui.CheckboxOpts{})
	k.EndDisabled()

	if n.pending {
		k.NextGap(8)
		k.BeginContainer(ui.AxisX, ui.CrossCenter, "checking", ui.Pct(1), ui.Children(1), 0)
		rexui.Spinner(k.Kit, i18n.T(checkingNumber)+"###checking-spinner", rexui.SpinnerOpts{Size: 3})
		rexui.Label(k.Kit, i18n.T(checkingNumber), rexui.LabelOpts{Style: rexui.TextCallout, Color: secondary})
		k.EndContainer()
	}

	enter := (pf.Submitted || ff.Submitted || lf.Submitted)
	beginActions(e)
	dl.Action(i18n.T("Cancel"), rexui.ButtonDefault)
	k.BeginDisabled(!ready)
	save := dl.Action(i18n.T("Save contact"), variant(named))
	msg := dl.Action(i18n.T("Message"), variant(!named))
	k.EndDisabled()
	endActions(e)

	// Enter saves a named contact or opens an unnamed chat.
	switch {
	case ready && (save || (enter && named)):
		s.lookup(e, a, digits, full, short, named, n.toPhone)
	case ready && (msg || enter):
		s.lookup(e, a, digits, "", "", false, a.Settings.SaveContactsToPhone)
	default:
		return
	}
	s.open = true // the sheet stays until the core has looked the number up
}

func variant(primary bool) rexui.ButtonVariant {
	if primary {
		return rexui.ButtonPrimary
	}
	return rexui.ButtonDefault
}

// lookup sends the NewContact command (upstream's Action::NewContact).
func (s *Screen) lookup(e *screenEnv, a *state.Account, phone, full, short string, named, toPhone bool) {
	n := &s.contact
	n.pending = true
	n.seq++
	n.request = n.seq
	// The dialog's choice starts the next one.
	if named && toPhone != a.Settings.SaveContactsToPhone {
		a.Settings.SaveContactsToPhone = toPhone
		e.S.Touch()
	}
	cmd := a.Cmd(abi.CommandTagNewContact)
	nc := &cmd.NewContact
	nc.Request, nc.Phone, nc.ToPhone = n.request, []byte(phone), toPhone
	if named {
		nc.FullName, nc.HasFullName = []byte(full), true
		if short != "" {
			nc.FirstName, nc.HasFirstName = []byte(short), true
		}
	}
	a.Send(cmd)
}

// contactFailure localizes the known number-lookup failures.
func contactFailure(err string) string {
	switch err {
	case "Could not check the number. Please try again.", "This number is not on WhatsApp",
		"Not connected to WhatsApp", "Enter a valid international phone number":
		return i18n.T(err)
	}
	return err
}

// ---- Link with a phone number ----

type pairing struct{ phone ui.TextBuffer }

func (s *Screen) pairWithPhone(e *screenEnv, a *state.Account, dl *rexui.Dialog) {
	k, c, p := e.K, e.C(), &s.pair
	para(k, i18n.T("Enter the WhatsApp phone number with its country code. Do not include a plus sign or leading zero. You will get a code to enter on the phone."), rexui.TextCallout, secondary)
	f := rexui.TextField(k.Kit, i18n.T("Phone number"), &p.phone, "15551234567", rexui.FieldOpts{})
	if s.age == 2 {
		c.SetFocus(f.Key)
	}
	digits := make([]byte, 0, 16)
	for _, b := range p.phone.Bytes() {
		if b >= '0' && b <= '9' {
			digits = append(digits, b)
		}
	}
	ready := len(digits) >= 7
	beginActions(e)
	dl.Action(i18n.T("Cancel"), rexui.ButtonDefault)
	k.BeginDisabled(!ready)
	if dl.Action(i18n.T("Get a code"), rexui.ButtonPrimary) || (f.Submitted && ready) {
		cmd := a.Cmd(abi.CommandTagPairWithPhone)
		cmd.PairWithPhone.Phone = digits
		a.Send(cmd)
		s.open = false
	}
	k.EndDisabled()
	endActions(e)
}

// ---- Locked chats ----

type unlock struct {
	code, confirm ui.TextBuffer // secure fields; wiped once used
	wrong         bool
	setup         bool
	ready         bool // a code is typed (and, setting one up, typed twice)
}

func (u *unlock) reset(e *screenEnv) {
	u.leave()
	u.wrong, u.ready = false, false
	u.setup = len(e.S.Settings.ChatLockHash) == 0
}

// leave forgets what was typed: the dialog is closing or not on screen. The
// framework gives secure event input back when the field goes.
func (u *unlock) leave() {
	u.code.Wipe()
	u.confirm.Wipe()
}

// check works out ready from the fields (zf:src/ui/dialogs.rs: the trimmed
// code is not empty and, setting one up, matches the confirmation).
func (u *unlock) check() {
	c := bytes.TrimSpace(u.code.Bytes())
	u.ready = len(c) > 0 && (!u.setup || bytes.Equal(c, bytes.TrimSpace(u.confirm.Bytes())))
}

func (s *Screen) unlockTitle() string {
	if s.unlock.setup {
		return i18n.T("Set up locked chats")
	}
	return i18n.T("Locked chats")
}

// enterLocked opens the Locked chats folder.
func enterLocked(e *screenEnv, a *state.Account) {
	a.SetMode(state.ListLocked)
	a.Filter = state.FilterAll
	e.S.Page = state.PageChats
	e.S.Panes.ListHidden = false
	e.S.Touch()
}

func (s *Screen) unlockLocked(e *screenEnv, a *state.Account, dl *rexui.Dialog) {
	k, c, u := e.K, e.C(), &s.unlock
	if u.setup {
		para(k, i18n.T("Choose a local code to open the Locked tab. It is separate from your phone's code. This hides chats in Zap, it does not add another encryption layer."), rexui.TextCallout, secondary)
	} else {
		para(k, i18n.T("Enter your local Zap code. Leaving the Locked tab or closing the window locks it again."), rexui.TextCallout, secondary)
	}
	f := rexui.TextField(k.Kit, i18n.T("Local code"), &u.code, i18n.T("Local code"), rexui.FieldOpts{Secure: true})
	if s.age == 2 {
		c.SetFocus(f.Key)
	}
	var f2 base.FieldSignal
	if u.setup {
		f2 = rexui.TextField(k.Kit, i18n.T("Confirm code"), &u.confirm, i18n.T("Confirm code"), rexui.FieldOpts{Secure: true})
	}
	if u.wrong {
		para(k, i18n.T("That code did not match. Try again."), rexui.TextCallout, danger)
	}
	if f.Changed || f2.Changed {
		u.check()
	}
	ready := u.ready
	label := i18n.T("Open locked chats")
	if u.setup {
		label = i18n.T("Set code and open")
	}
	beginActions(e)
	dl.Action(i18n.T("Cancel"), rexui.ButtonDefault)
	k.BeginDisabled(!ready)
	submit := dl.Action(label, rexui.ButtonPrimary) || ((f.Submitted || f2.Submitted) && ready)
	k.EndDisabled()
	endActions(e)
	if !submit {
		return
	}
	code := bytes.Clone(u.code.Bytes())
	defer clear(code)
	u.leave()
	u.ready = false
	st := &e.S.Settings
	switch {
	case u.setup:
		// zf:src/app.rs CreateChatLockCode: never over a code set meanwhile.
		if len(st.ChatLockHash) == 0 {
			st.ChatLockHash = []byte(lockhash.ChatVerifier(code))
			enterLocked(e, a)
		} else {
			s.open = true
		}
	case lockhash.VerifiesChat(string(st.ChatLockHash), code):
		enterLocked(e, a)
	default:
		u.wrong = true
		s.open = true
	}
}
