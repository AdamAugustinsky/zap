package chatlist

import (
	"bytes"
	"strconv"
	"strings"
	"unicode"

	"zapfast/abi"
	"zapfast/i18n"
	"zapfast/model"
	"zapfast/state"
)

func itoa(n int) string { return strconv.Itoa(n) }

// selfTitle is our own chat's title, "Name (You)" as on the phone
// (zf:src/app.rs:2518).
func (s *Screen) selfTitle(a *state.Account) string {
	if n := bytes.TrimSpace(a.Model.Me.Name()); len(n) > 0 {
		return i18n.Format(i18n.T("{name} (You)"), "{name}", string(n))
	}
	return i18n.T("You")
}

func isSelf(a *state.Account, id []byte) bool {
	me := a.Model.Me.ID()
	return len(me) > 0 && bytes.Equal(me, id)
}

// titleBytes is the chat's title as a view when that costs nothing.
func (s *Screen) titleBytes(a *state.Account, ch model.Chat) []byte {
	id := ch.ID()
	switch {
	case isSelf(a, id):
		return []byte(s.selfTitle(a))
	case ch.Group():
		if n := bytes.TrimSpace(ch.Name()); len(n) > 0 && !(string(n) == "Group" && !ch.SubjectKnown()) {
			return ch.Name()
		}
		return []byte(s.title(a, ch))
	}
	return a.Model.DisplayName(id)
}

// title is the chat's title as the list writes it (zf:src/app.rs:2486): a
// group without a subject is named after its members.
func (s *Screen) title(a *state.Account, ch model.Chat) string {
	id := ch.ID()
	if isSelf(a, id) {
		return s.selfTitle(a)
	}
	if !ch.Group() {
		return string(a.Model.DisplayName(id))
	}
	n := bytes.TrimSpace(ch.Name())
	if len(n) > 0 && !(string(n) == "Group" && !ch.SubjectKnown()) {
		return string(ch.Name())
	}
	if err := ch.Decode(&s.dec); err == nil {
		var names []string
		for _, p := range s.dec.Participants {
			if !isSelf(a, p) {
				names = append(names, strings.TrimPrefix(string(a.Model.DisplayName(p)), "~"))
			}
		}
		if len(names) > 0 {
			return strings.Join(names, ", ")
		}
	}
	return i18n.T("Group")
}

// shortName is the name WhatsApp shows where room is short: the first name
// saved with the contact, else the first word (zf:src/app.rs:2453).
func (s *Screen) shortName(a *state.Account, who []byte) string {
	return string(s.shortNameBytes(a, who))
}

// shortNameBytes is shortName as a view of the store or of who.
func (s *Screen) shortNameBytes(a *state.Account, who []byte) []byte {
	if len(who) == 0 {
		return nil
	}
	m := a.Model
	name := who
	if bytes.IndexByte(who, '@') >= 0 {
		if ci := m.Contacts.Find(who); ci >= 0 {
			if first := bytes.TrimSpace(m.Contacts.At(ci).FirstName()); len(first) > 0 {
				return first
			}
		}
		name = m.DisplayName(who)
	}
	if bytes.HasPrefix(name, []byte("+")) {
		return name
	}
	// the first of bytes.Fields(name), without its slice
	if f := bytes.TrimLeftFunc(name, unicode.IsSpace); len(f) > 0 {
		if i := bytes.IndexFunc(f, unicode.IsSpace); i >= 0 {
			f = f[:i]
		}
		return f
	}
	return name
}

// draftFor updates the row's draft: the text of the chat's unsent message,
// on one line, except in the chat that is open (zf:src/app.rs:2996).
func (s *Screen) draftFor(a *state.Account, in *rowInfo, id []byte) {
	if a.Open != 0 {
		if open, ok := a.Model.Chats.Get(a.Open); ok && bytes.Equal(open.ID(), id) {
			in.draftOf, in.draftSrc = "", in.draftSrc[:0]
			return
		}
	}
	if d := a.Model.Draft(id); len(d) > 0 {
		if !bytes.Equal(d, in.draftSrc) {
			in.draftSrc = append(in.draftSrc[:0], d...)
			in.draftOf = oneLine(d)
		}
		return
	}
	in.draftSrc = in.draftSrc[:0]
	in.draftOf = ""
}

// decode scratch for titles.
var _ abi.Chat
