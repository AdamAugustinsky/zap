package model

import (
	"bytes"

	"zapfast/abi"
)

// contactRec is one address-book entry. Every Str points into raw.
type contactRec struct {
	raw                   Str
	id, full, first, push Str
	tilde                 Str // "~" and push, for a name that is not in the address book
	gen                   uint32
}

// Contacts are the account's contacts, by JID.
type Contacts struct {
	recs    []contactRec
	a       arena
	spare   []byte
	tmp     []byte
	idx     index
	gen     uint32
	base    uint32 // a new row's generation (Chats.base)
	scratch abi.Contact
}

func (c *Contacts) key(row uint32) []byte { return c.a.at(c.recs[row].id) }

// Gen moves whenever a contact changes or appears.
func (c *Contacts) Gen() uint32 { return c.gen }

// Len is the number of contacts.
func (c *Contacts) Len() int { return len(c.recs) }

// At is contact i's view (in the order they arrived; the new-chat dialog
// sorts them itself).
func (c *Contacts) At(i int) Contact { return Contact{c, uint32(i)} }

// Find is the index of the contact with the JID, or -1.
func (c *Contacts) Find(id []byte) int { return c.idx.find(id, c) }

// Decode decodes contact i's whole record into dst.
func (c *Contacts) Decode(i int, dst *abi.Contact) error {
	return dst.Decode(c.a.at(c.recs[i].raw))
}

func (c *Contacts) upsert(ct *abi.Contact) {
	if len(ct.ID) == 0 {
		return
	}
	row := c.idx.find(ct.ID, c)
	start := len(c.a.b)
	var err error
	c.a.b, err = ct.Encode(c.a.b)
	if err != nil {
		c.a.b = c.a.b[:start]
		return
	}
	raw := Str{uint32(start), uint32(len(c.a.b) - start)}
	c.a.live += int(raw.Len)
	d := &c.scratch
	defer d.Release() // the row keeps offsets; the views would pin a retired arena
	if d.Decode(c.a.at(raw)) != nil {
		return
	}
	r := contactRec{raw: raw, id: c.a.sub(raw, d.ID), full: c.a.sub(raw, d.FullName), first: c.a.sub(raw, d.FirstName), push: c.a.sub(raw, d.PushName)}
	if d.HasPushName && len(d.PushName) > 0 {
		c.tmp = append(append(c.tmp[:0], '~'), d.PushName...)
		r.tilde = c.a.put(c.tmp)
	}
	c.gen++
	if row >= 0 {
		c.a.drop(c.recs[row].raw)
		c.a.drop(c.recs[row].tilde)
		r.gen = c.recs[row].gen + 1
		c.recs[row] = r
	} else {
		r.gen = c.base
		c.recs = append(c.recs, r)
		c.idx.add(ct.ID, uint32(len(c.recs)-1))
	}
	if c.a.wasteful() {
		old := c.a.b
		c.a.b, c.spare = c.spare[:0], old
		c.a.live = 0
		for i := range c.recs {
			r := &c.recs[i]
			ob := r.raw
			nb := c.a.put(old[ob.Off : ob.Off+ob.Len])
			r.raw, r.id, r.full, r.first, r.push = nb, rebase(r.id, ob, nb), rebase(r.full, ob, nb), rebase(r.first, ob, nb), rebase(r.push, ob, nb)
			r.tilde = c.a.put(old[r.tilde.Off : r.tilde.Off+r.tilde.Len])
		}
	}
}

// Contact is a view of one contact, valid until the store is next applied.
type Contact struct {
	c *Contacts
	i uint32
}

func (v Contact) r() *contactRec { return &v.c.recs[v.i] }

// ID is the contact's JID.
func (v Contact) ID() []byte { return v.c.a.at(v.r().id) }

// FullName, FirstName and PushName are the address book's names and the
// name the person chose; each may be empty.
func (v Contact) FullName() []byte  { return v.c.a.at(v.r().full) }
func (v Contact) FirstName() []byte { return v.c.a.at(v.r().first) }
func (v Contact) PushName() []byte  { return v.c.a.at(v.r().push) }

// DisplayName is the saved full name, else the push name, else empty
// (zf:src/model.rs:1565).
func (v Contact) DisplayName() []byte {
	if n := bytes.TrimSpace(v.FullName()); len(n) > 0 {
		return v.FullName()
	}
	return v.PushName()
}

// Gen moves whenever the contact changes.
func (v Contact) Gen() uint32 { return v.r().gen }

// Phone is the digits of a <phone>@s.whatsapp.net JID, or nil
// (zf:src/model.rs:442).
func Phone(jid []byte) []byte {
	at := bytes.IndexByte(jid, '@')
	if at <= 0 || string(jid[at+1:]) != "s.whatsapp.net" {
		return nil
	}
	for _, b := range jid[:at] {
		if b < '0' || b > '9' {
			return nil
		}
	}
	return jid[:at]
}
