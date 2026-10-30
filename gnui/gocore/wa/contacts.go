package wa

import (
	"cmp"
	"context"
	"log/slog"
	"strings"

	"go.mau.fi/whatsmeow/appstate"
	"go.mau.fi/whatsmeow/proto/waSyncAction"
	"go.mau.fi/whatsmeow/types"
	"go.mau.fi/whatsmeow/types/events"
	"google.golang.org/protobuf/proto"

	"zapfast/abi"
	"zapfast/gocore/out"
	"zapfast/gocore/store"
)

// Contacts are whatsmeow's contact store: the names the phone's address
// book gives (full and first) and the push names people chose. Saving a
// contact goes through the phone's app state first, as upstream's
// save_contact does.

func contactRecord(id string, c types.ContactInfo) abi.Contact {
	x := abi.Contact{ID: b(id)}
	if c.FullName != "" {
		x.FullName, x.HasFullName = b(out.Clip(c.FullName, 1024)), true
	}
	if c.FirstName != "" {
		x.FirstName, x.HasFirstName = b(out.Clip(c.FirstName, 1024)), true
	}
	if name := c.PushName; name != "" {
		x.PushName, x.HasPushName = b(out.Clip(name, 1024)), true
	} else if c.BusinessName != "" {
		x.PushName, x.HasPushName = b(out.Clip(c.BusinessName, 1024)), true
	}
	return x
}

// fillContact is c with the names it lacks taken from o. whatsmeow can keep
// one person's names under two entries, their number's and their privacy
// id's (the address book's name under one, the push name under the other):
// they name one person, and the number's entry comes first.
func fillContact(c, o types.ContactInfo) types.ContactInfo {
	c.Found = c.Found || o.Found
	c.FullName = cmp.Or(c.FullName, o.FullName)
	c.FirstName = cmp.Or(c.FirstName, o.FirstName)
	c.PushName = cmp.Or(c.PushName, o.PushName)
	c.BusinessName = cmp.Or(c.BusinessName, o.BusinessName)
	return c
}

// emitContacts sends every contact, once linked.
func (e *Engine) emitContacts() {
	if e.me() == "" {
		return
	}
	all, err := e.cli.Contacts(e.ctx)
	if err != nil {
		slog.Warn("zapfast wa: listing contacts", "err", err)
		return
	}
	byID := map[string]types.ContactInfo{}
	for j, c := range all {
		if !c.Found && c.FullName == "" && c.PushName == "" {
			continue
		}
		id := e.canonical(j)
		if old, ok := byID[id]; ok {
			if j.Server == types.HiddenUserServer {
				c = fillContact(old, c)
			} else {
				c = fillContact(c, old)
			}
		}
		byID[id] = c
	}
	seen := make(map[string]bool, len(byID))
	list := make([]abi.Contact, 0, len(byID))
	for id, c := range byID {
		seen[id] = true
		list = append(list, contactRecord(id, c))
	}
	list = append(list, e.seenNames(seen)...)
	out.List(e.out, list, func(batch uint32, last bool, chunk []abi.Contact) *abi.Event {
		return &abi.Event{Tag: abi.EventTagContacts, Contacts: abi.EventContacts{Batch: batch, Last: last, Contacts: chunk}}
	})
}

// emitContact sends one contact as whatsmeow's store has it now, its
// number's entry and its privacy id's together.
func (e *Engine) emitContact(j types.JID) {
	j = j.ToNonAD()
	c, err := e.cli.Contact(e.ctx, j)
	if err != nil {
		return
	}
	id := e.canonical(j)
	switch j.Server {
	case types.HiddenUserServer:
		if pn, ok := jidOf(id); ok && pn != j {
			if o, err := e.cli.Contact(e.ctx, pn); err == nil {
				c = fillContact(o, c)
			}
		}
	case types.DefaultUserServer:
		if lid, err := e.cli.LIDForPN(e.ctx, j); err == nil && !lid.IsEmpty() {
			if o, err := e.cli.Contact(e.ctx, lid.ToNonAD()); err == nil {
				c = fillContact(c, o)
			}
		}
	}
	list := []abi.Contact{contactRecord(id, c)}
	out.List(e.out, list, func(batch uint32, last bool, chunk []abi.Contact) *abi.Event {
		return &abi.Event{Tag: abi.EventTagContacts, Contacts: abi.EventContacts{Batch: batch, Last: last, Contacts: chunk}}
	})
}

// contactChanged is a contact edited on the phone, or a new push name: the
// contact, and its direct chat's name when the chat had none.
func (e *Engine) contactChanged(j types.JID, name string) {
	e.emitContact(j)
	chat := e.canonical(j)
	if c, err := e.arch.Chat(chat); err == nil && name != "" && (c.Name == "" || c.Name == j.User) && chatKind(c.Kind, chat) == abi.ChatKindDirect {
		if err := e.updateChat(chat, func(c *store.Chat) { c.Name = name }); err != nil {
			slog.Warn("zapfast wa: naming a chat", "err", err)
		}
	}
}

// contactPatch is the phone's address book entry (app state "contact").
func contactPatch(j types.JID, full, first string, toPhone bool) appstate.PatchInfo {
	action := &waSyncAction.ContactAction{FullName: proto.String(full), SaveOnPrimaryAddressbook: proto.Bool(toPhone)}
	if first != "" {
		action.FirstName = proto.String(first)
	}
	if j.Server == types.DefaultUserServer {
		action.PnJID = proto.String(j.String())
	}
	return appstate.PatchInfo{Type: appstate.WAPatchCriticalUnblockLow, Mutations: []appstate.MutationInfo{{
		Index:   []string{appstate.IndexContact, j.String()},
		Version: 2,
		Value:   &waSyncAction.SyncActionValue{ContactAction: action},
	}}}
}

// saveContact names a contact on WhatsApp (and, with toPhone, in the
// phone's address book), then here.
func (e *Engine) saveContact(id, full, first string, toPhone bool) {
	full, first = strings.TrimSpace(full), strings.TrimSpace(first)
	j, ok := jidOf(id)
	if !ok || !e.cli.IsConnected() {
		e.emitError("Not connected to WhatsApp")
		return
	}
	if full == "" {
		e.emitError("Could not save contact: a contact needs a name")
		return
	}
	patch := contactPatch(j, full, first, toPhone)
	cli, gen := e.cli, e.gen
	e.job(func(ctx context.Context) func() {
		err := cli.SendAppState(ctx, patch)
		if err == nil {
			err = cli.PutContactName(ctx, j, full, first)
		}
		return func() {
			if gen != e.gen {
				return
			}
			if err != nil {
				e.emitError("Could not save contact: " + err.Error())
				return
			}
			e.emitContact(j)
			ev := abi.Event{Tag: abi.EventTagInfo}
			ev.Info.Text = b(out.Toast("Added " + full + " to contacts"))
			e.out.Emit(&ev)
			e.emitChat(id)
		}
	})
}

// internationalPhone is a phone number as WhatsApp writes it, digits only
// with the country code, or "" when it is not one (upstream's
// util::international_phone).
func internationalPhone(s string) string {
	var digits strings.Builder
	for i, r := range strings.TrimSpace(s) {
		switch {
		case r >= '0' && r <= '9':
			digits.WriteRune(r)
		case r == '+' && i == 0, r == ' ', r == '-', r == '(', r == ')', r == '.':
		default:
			return ""
		}
	}
	d := strings.TrimLeft(digits.String(), "0")
	if len(d) < 7 || len(d) > 15 {
		return ""
	}
	return d
}

// newContact checks that a number is on WhatsApp, then (with a name) saves
// it, and answers with the chat to open.
func (e *Engine) newContact(c *abi.CommandNewContact) {
	request := c.Request
	fail := func(msg string) {
		ev := abi.Event{Tag: abi.EventTagContactFailed}
		ev.ContactFailed = abi.EventContactFailed{Request: request, Error: b(out.Toast(msg))}
		e.out.Emit(&ev)
	}
	phone := internationalPhone(string(c.Phone))
	if phone == "" {
		fail("Enter a valid international phone number")
		return
	}
	if !e.cli.IsConnected() {
		fail("Not connected to WhatsApp")
		return
	}
	full, first, toPhone := string(c.FullName), string(c.FirstName), c.ToPhone
	hasName := c.HasFullName && strings.TrimSpace(full) != ""
	cli, gen := e.cli, e.gen
	e.job(func(ctx context.Context) func() {
		found, err := cli.IsOnWhatsApp(ctx, []string{"+" + phone})
		return func() {
			if gen != e.gen {
				return
			}
			if err != nil {
				fail("Could not check the number. Please try again.")
				return
			}
			var j types.JID
			for _, r := range found {
				if r.IsIn {
					j = r.JID.ToNonAD()
				}
			}
			if j.IsEmpty() {
				fail("This number is not on WhatsApp")
				return
			}
			id := e.canonical(j)
			if hasName {
				e.saveContact(id, full, first, toPhone)
			}
			ev := abi.Event{Tag: abi.EventTagContactReady}
			ev.ContactReady = abi.EventContactReady{Request: request, ID: b(id)}
			if hasName {
				ev.ContactReady.Name, ev.ContactReady.HasName = b(out.Clip(strings.TrimSpace(full), 1024)), true
			}
			e.out.Emit(&ev)
		}
	})
}

// contactEvent files whatsmeow's contact events.
func (e *Engine) contactEvent(evt any) {
	switch v := evt.(type) {
	case *events.Contact:
		e.contactChanged(v.JID, v.Action.GetFullName())
	case *events.PushName:
		// whatsmeow keeps the name under both ids: the phone number is the
		// one chats are filed under (chatID), known or not to canonical.
		j := v.JID
		if j.Server == types.HiddenUserServer && v.JIDAlt.Server == types.DefaultUserServer {
			j = v.JIDAlt
		}
		e.contactChanged(j, v.NewPushName)
	}
}

// seenNames are contact records for the direct chats no contact names (not
// in seen): the name the person went by in their newest message here, as
// the phone shows "~ name" for a number that is not in the address book. A
// name saved later comes as a contact and goes before it.
func (e *Engine) seenNames(seen map[string]bool) []abi.Contact {
	chats, err := e.arch.Chats()
	if err != nil {
		return nil
	}
	var list []abi.Contact
	for _, c := range chats {
		if seen[c.ID] || c.Name != "" || chatKind(c.Kind, c.ID) != abi.ChatKindDirect || e.isMe(c.ID) {
			continue
		}
		page, _, err := e.arch.Messages(c.ID, "", 50)
		if err != nil {
			continue
		}
		for i := len(page) - 1; i >= 0; i-- { // newest last
			if m := page[i]; !m.FromMe && strings.TrimSpace(m.SenderName) != "" {
				list = append(list, abi.Contact{ID: b(c.ID), PushName: b(out.Clip(m.SenderName, 1024)), HasPushName: true})
				break
			}
		}
	}
	return list
}

// emitSeenNames sends seenNames for the chats no contact names, once a
// history sync has filed the messages they come from.
func (e *Engine) emitSeenNames() {
	all, err := e.cli.Contacts(e.ctx)
	if err != nil {
		return
	}
	seen := map[string]bool{}
	for j, c := range all {
		if c.FullName != "" || c.PushName != "" || c.BusinessName != "" {
			seen[e.canonical(j)] = true
		}
	}
	list := e.seenNames(seen)
	if len(list) == 0 {
		return
	}
	out.List(e.out, list, func(batch uint32, last bool, chunk []abi.Contact) *abi.Event {
		return &abi.Event{Tag: abi.EventTagContacts, Contacts: abi.EventContacts{Batch: batch, Last: last, Contacts: chunk}}
	})
}

// nameQuote gives a reply's quote the name its sender went by: the quoted
// message's, when it is here, else the contact's.
func (e *Engine) nameQuote(m *store.Message) {
	q := m.Quote
	if q == nil || q.SenderName != "" || q.Sender == "" || e.isMe(q.Sender) {
		return
	}
	chat := m.ChatID
	if q.Chat != "" {
		chat = q.Chat
	}
	if quoted, err := e.arch.Message(chat, q.ID); err == nil && quoted.SenderName != "" && !quoted.FromMe {
		q.SenderName = quoted.SenderName
		return
	}
	j, ok := jidOf(q.Sender)
	if !ok {
		return
	}
	if c, err := e.cli.Contact(e.ctx, j); err == nil {
		for _, name := range []string{c.FullName, c.FirstName, c.PushName, c.BusinessName} {
			if name != "" {
				q.SenderName = name
				return
			}
		}
	}
}
