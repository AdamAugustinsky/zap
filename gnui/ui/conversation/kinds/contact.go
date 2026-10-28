package kinds

import (
	"strings"
	"unicode"

	"gonativeui/rexui"
	"gonativeui/theme"
	"gonativeui/widget/base"

	"zapfast/abi"
	"zapfast/i18n"
	"zapfast/ui/conversation/mkey"
	"zapfast/ui/screen"
)

// Shared contacts (zf:src/ui/conversation.rs shared_contact_details and the
// Content::Contact arm).

// sharedContact is the first card of a shared contact, as the bubble uses it.
type sharedContact struct {
	name      string
	number    string
	account   string // digits of the WhatsApp account behind the number, "" for none
	firstName string // from the card's N property, "" for none
	ok        bool   // the card has a valid telephone number
}

// vcardTelPreference is the best PREF rank in a TEL property's parameters.
func vcardTelPreference(property string) (rank int, ok bool) {
	parts := strings.Split(property, ";")
	for _, param := range parts[1:] {
		name, value, found := strings.Cut(param, "=")
		if !found {
			name, value = "TYPE", param
		}
		value = strings.Trim(value, `"`)
		r := 0
		switch {
		case strings.EqualFold(name, "PREF"):
			n := 0
			valid := value != ""
			for _, ch := range value {
				if ch < '0' || ch > '9' || n > 255 {
					valid = false
					break
				}
				n = n*10 + int(ch-'0')
			}
			if valid && n >= 1 && n <= 100 {
				r = n
			}
		case strings.EqualFold(name, "TYPE"):
			for _, v := range strings.Split(value, ",") {
				if strings.EqualFold(v, "PREF") {
					r = 1
				}
			}
		}
		if r != 0 && (!ok || r < rank) {
			rank, ok = r, true
		}
	}
	return rank, ok
}

// vcardFirstName is the given and additional names of an N value
// (family;given;additional;prefix;suffix), split at unescaped semicolons.
func vcardFirstName(value string) string {
	parts := []strings.Builder{{}}
	esc := false
	for _, ch := range value {
		switch {
		case esc:
			esc = false
			if ch == 'n' || ch == 'N' {
				parts[len(parts)-1].WriteRune(' ')
			} else {
				parts[len(parts)-1].WriteRune(ch)
			}
		case ch == '\\':
			esc = true
		case ch == ';':
			parts = append(parts, strings.Builder{})
		default:
			parts[len(parts)-1].WriteRune(ch)
		}
	}
	part := func(i int) string {
		if i < len(parts) {
			return strings.TrimSpace(parts[i].String())
		}
		return ""
	}
	var out []string
	for _, p := range [...]string{part(1), part(2)} {
		if p != "" {
			out = append(out, p)
		}
	}
	return strings.Join(out, " ")
}

func digitsOf(s string) string {
	var sb strings.Builder
	for _, ch := range s {
		if ch >= '0' && ch <= '9' {
			sb.WriteRune(ch)
		}
	}
	return sb.String()
}

func vcardTelAccount(property, value string) string {
	parts := strings.Split(property, ";")
	for _, param := range parts[1:] {
		if name, v, ok := strings.Cut(param, "="); ok && strings.EqualFold(name, "WAID") {
			d := digitsOf(strings.Trim(v, `"`))
			if len(d) >= 7 {
				return d
			}
			return ""
		}
	}
	if strings.HasPrefix(strings.TrimSpace(value), "+") {
		if d := digitsOf(value); len(d) >= 7 {
			return d
		}
	}
	return ""
}

// parseVcard is shared_contact_details.
func parseVcard(vcard, fallback string) (c sharedContact) {
	var card []string
	lines := strings.Split(strings.ReplaceAll(vcard, "\r\n", "\n"), "\n")
	for _, line := range lines {
		line = strings.TrimRight(line, "\r")
		if strings.HasPrefix(line, " ") || strings.HasPrefix(line, "\t") {
			if n := len(card); n > 0 {
				card[n-1] += strings.TrimLeftFunc(line, unicode.IsSpace)
			}
			continue
		}
		if strings.EqualFold(line, "END:VCARD") && len(card) > 0 {
			break
		}
		if strings.EqualFold(line, "BEGIN:VCARD") {
			if len(card) == 0 {
				card = append(card, line)
			}
			continue
		}
		if len(card) > 0 {
			card = append(card, line)
		}
	}
	if len(card) == 0 {
		card = lines
	}
	var name, first string
	type tel struct{ number, account string }
	var firstTel *tel
	var preferred *tel
	bestRank := 0
	for _, line := range card {
		property, value, ok := strings.Cut(line, ":")
		if !ok {
			continue
		}
		raw := property
		if i := strings.IndexByte(property, ';'); i >= 0 {
			raw = property[:i]
		}
		if i := strings.LastIndexByte(raw, '.'); i >= 0 {
			raw = raw[i+1:]
		}
		switch {
		case strings.EqualFold(raw, "FN"):
			name = strings.TrimSpace(value)
		case strings.EqualFold(raw, "N"):
			first = vcardFirstName(value)
		case strings.EqualFold(raw, "TEL"):
			number := strings.TrimSpace(value)
			if len(digitsOf(number)) < 7 {
				continue
			}
			t := &tel{number, vcardTelAccount(property, value)}
			if rank, ok := vcardTelPreference(property); ok {
				if preferred == nil || rank < bestRank {
					preferred, bestRank = t, rank
				}
			} else if firstTel == nil {
				firstTel = t
			}
		}
	}
	t := preferred
	if t == nil {
		t = firstTel
	}
	if t == nil {
		return sharedContact{name: fallback}
	}
	if name == "" {
		name = fallback
	}
	return sharedContact{name: name, number: t.number, account: t.account, firstName: first, ok: true}
}

// contacts is the parsed cards, by message and generation.
var contacts struct {
	m map[mkey.K]contactEnt
}

type contactEnt struct {
	gen uint32
	c   sharedContact
}

func (b *blk) contactOf() sharedContact {
	id := mkey.Of(b.a, b.m.ID())
	if e, ok := contacts.m[id]; ok && e.gen == b.m.Gen() {
		return e.c
	}
	if contacts.m == nil || len(contacts.m) > 512 {
		contacts.m = make(map[mkey.K]contactEnt)
	}
	d := b.decoded()
	c := parseVcard(string(d.Content.Contact.Vcard), string(d.Content.Contact.DisplayName))
	contacts.m[id] = contactEnt{b.m.Gen(), c}
	return c
}

const (
	contactButtonGap = 6.0
	contactMaxName   = 2
)

type contactGeom struct {
	w, nameH, rowH, h, col float32
	chatW, addW            float32
	addShown               bool
}

func (b *blk) contactGeom(c *sharedContact, maxW float32) (g contactGeom) {
	g.col = b.pt(noteIconW + noteGap)
	g.w = min(b.pt(250), maxW)
	_, g.nameH, _ = b.wrapSize(base.StrBytes(c.name), fBold, g.w-g.col, contactMaxName)
	g.nameH = max(g.nameH, b.lineH(fBold))
	g.h = g.nameH
	sz := &b.r.Sizes[theme.SizeSmall]
	switch {
	case c.account != "":
		g.rowH = theme.Snap(sz.Height)
		g.chatW = b.widthSized(i18n.T("Chat"), sz) + 2*sz.PadX
		g.h += b.pt(5) + g.rowH
	case c.ok:
		g.rowH = b.lineH(fCallout)
		g.h += b.pt(5) + g.rowH
	}
	return g
}

func (b *blk) widthSized(s string, sz *theme.SizeSpec) float32 { return b.k.TextWidth(sz.Font.Font, s) }

func (b *blk) contactSize(maxW float32) (w, h float32) {
	c := b.contactOf()
	g := b.contactGeom(&c, maxW)
	return g.w, g.h
}

// contactSeq numbers the contacts this package asks the core to save.
var contactSeq uint64 = 1 << 40

func (b *blk) contactBuild(r screen.Rect) {
	c := b.contactOf()
	g := b.contactGeom(&c, r.W)
	b.icon(icPerson, b.accent(), 0, 0, b.pt(noteIconW), b.lineH(fBold))
	b.wrapped(1, base.StrBytes(c.name), fBold, b.ink(), g.col, 0, r.W-g.col, g.nameH, contactMaxName)
	y := g.nameH + b.pt(5)
	switch {
	case c.account != "":
		jid := b.c.Fmt().Str(c.account).Str("@s.whatsapp.net").Bytes()
		sz := &b.r.Sizes[theme.SizeSmall]
		b.c.NextPos(g.col, y)
		b.c.NextFlags(base.Floating)
		if rexui.Button(b.k, i18n.T("Chat"), rexui.ButtonOpts{Variant: rexui.ButtonPrimary, Size: theme.SizeSmall}).Clicked {
			b.startChat(jid, c.name)
		}
		if b.a != nil && b.a.Model.Contacts.Find(jid) < 0 {
			b.c.NextPos(g.col+g.chatW+b.pt(contactButtonGap), y)
			b.c.NextFlags(base.Floating)
			if rexui.Button(b.k, i18n.T("Add"), rexui.ButtonOpts{Size: theme.SizeSmall}).Clicked {
				b.addContact(c.account, c.name, c.firstName)
			}
		}
		_ = sz
	case c.ok:
		b.labelB(base.StrBytes(c.number), fCallout, b.dim(), g.col, y, r.W-g.col)
	}
}

// startChat opens the conversation with jid, asking the core to make it when
// the account has none (Action::StartChat).
func (b *blk) startChat(jid []byte, name string) {
	if b.a == nil {
		return
	}
	if ref := b.chatOf(jid); ref != 0 {
		b.a.OpenChat(ref)
		return
	}
	cmd := b.a.Cmd(abi.CommandTagEnsureChat)
	cmd.EnsureChat.Chat, cmd.EnsureChat.Name = jid, []byte(name)
	b.a.Send(cmd)
}

// addContact saves the card's number as a contact (Action::NewContact with
// the card's name, split where its first name ends).
func (b *blk) addContact(account, name, first string) {
	if b.a == nil {
		return
	}
	name = strings.TrimSpace(name)
	f, l := name, ""
	first = strings.TrimSpace(first)
	if first != "" && strings.HasPrefix(name, first) {
		if rest := name[len(first):]; rest == "" || unicode.IsSpace(rune(rest[0])) {
			f, l = first, strings.TrimSpace(rest)
		}
	}
	full := f
	if l != "" && f != "" {
		full = f + " " + l
	} else if f == "" {
		full = l
	}
	contactSeq++
	cmd := b.a.Cmd(abi.CommandTagNewContact)
	cmd.NewContact.Request = contactSeq
	cmd.NewContact.Phone = []byte(account)
	if full != "" {
		cmd.NewContact.FullName, cmd.NewContact.HasFullName = []byte(full), true
	}
	if f != "" {
		cmd.NewContact.FirstName, cmd.NewContact.HasFirstName = []byte(f), true
	}
	cmd.NewContact.ToPhone = b.a.Settings.SaveContactsToPhone
	b.a.Send(cmd)
}
