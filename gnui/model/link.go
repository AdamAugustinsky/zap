package model

import "zapfast/abi"

// Link is the account's link status (zf:src/backend.rs:37): what the login
// screen shows (the QR code to scan, the pairing code) and the banner's
// connection state.
type Link struct {
	tag                          abi.LinkStatusTag
	b                            []byte
	qr, code, phone, reason, err Str
	has                          uint8 // 1 QR, 2 pair code, 4 pairing phone
	gen                          uint32
	known                        bool
}

func (l *Link) set(s *abi.LinkStatus) {
	l.b = l.b[:0]
	put := func(v []byte) Str {
		off := len(l.b)
		l.b = append(l.b, v...)
		return Str{uint32(off), uint32(len(v))}
	}
	l.tag, l.has, l.known = s.Tag, 0, true
	l.qr, l.code, l.phone, l.reason, l.err = Str{}, Str{}, Str{}, Str{}, Str{}
	switch s.Tag {
	case abi.LinkStatusTagUnlinked:
		u := &s.Unlinked
		if u.HasQR {
			l.qr, l.has = put(u.QR), l.has|1
		}
		if u.HasPairCode {
			l.code, l.has = put(u.PairCode), l.has|2
		}
		if u.HasPairingPhone {
			l.phone, l.has = put(u.PairingPhone), l.has|4
		}
	case abi.LinkStatusTagDisconnected:
		l.reason = put(s.Disconnected.Reason)
	case abi.LinkStatusTagFailed:
		l.err = put(s.Failed.Error)
	}
	l.gen++
}

func (l *Link) at(s Str) []byte { return l.b[s.Off : s.Off+s.Len : s.Off+s.Len] }

// Status is the state: Starting until the core says otherwise.
func (l *Link) Status() abi.LinkStatusTag { return l.tag }

// Known reports whether the core has sent a status yet.
func (l *Link) Known() bool { return l.known }

// Connected reports whether the account is online.
func (l *Link) Connected() bool { return l.tag == abi.LinkStatusTagConnected }

// QR is the pairing QR code's payload to draw (package gonativeui/qr);
// ok is false while there is none (Unlinked without one yet).
func (l *Link) QR() (payload []byte, ok bool) { return l.at(l.qr), l.has&1 != 0 }

// PairCode is the code to type on the phone, after PairWithPhone.
func (l *Link) PairCode() (code []byte, ok bool) { return l.at(l.code), l.has&2 != 0 }

// PairingPhone is the number a pairing code was asked for.
func (l *Link) PairingPhone() (phone []byte, ok bool) { return l.at(l.phone), l.has&4 != 0 }

// Reason is why a Disconnected link dropped.
func (l *Link) Reason() []byte { return l.at(l.reason) }

// Error is why a Failed link failed.
func (l *Link) Error() []byte { return l.at(l.err) }

// Gen moves with every status.
func (l *Link) Gen() uint32 { return l.gen }

// Me is the account's own identity.
type Me struct {
	b                     []byte
	id, lid, name, about  Str
	hasLID, hasName, hasA bool
	known                 bool
	gen                   uint32
}

func (m *Me) set(e *abi.EventMe) {
	m.b = m.b[:0]
	put := func(v []byte) Str {
		off := len(m.b)
		m.b = append(m.b, v...)
		return Str{uint32(off), uint32(len(v))}
	}
	m.id, m.lid, m.name, m.about = put(e.ID), put(e.LID), put(e.Name), put(e.About)
	m.hasLID, m.hasName, m.hasA, m.known = e.HasLID, e.HasName, e.HasAbout, true
	m.gen++
}

func (m *Me) at(s Str) []byte { return m.b[s.Off : s.Off+s.Len : s.Off+s.Len] }

// Known reports whether the core has said who we are.
func (m *Me) Known() bool { return m.known }

// ID is our JID; LID our privacy id.
func (m *Me) ID() []byte  { return m.at(m.id) }
func (m *Me) LID() []byte { return m.at(m.lid) }

// Name is our push name; About our status text. Empty when not set.
func (m *Me) Name() []byte  { return m.at(m.name) }
func (m *Me) About() []byte { return m.at(m.about) }

// Gen moves whenever Me changes.
func (m *Me) Gen() uint32 { return m.gen }
