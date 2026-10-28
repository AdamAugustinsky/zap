package model

import (
	"bytes"
	"encoding/binary"

	"zapfast/abi"
)

// marks are the account's starred and pinned messages
// (zf:src/app.rs:3143-3207): stars, the ids of pinned messages, and each
// chat's pins as the line under its header shows them.
type marks struct {
	stars kv // (chat, id) → nothing
	// starBatch is, per chat, the Stars batch arriving: its number and
	// whether more chunks follow.
	starBatch kv
	pinIDs    kv // (chat, id) → nothing
	pins      kv // chat → its Pinned records, each length-prefixed
	// The Pins batch arriving, which replaces the chat's at its last chunk.
	pinChat   []byte
	pinBuf    []byte
	pinBatch  uint32
	pinStream bool
	fbuf      []byte
	scratch   abi.Pinned
	gen       uint32
}

func (m *marks) starChunk(e *abi.EventStars) {
	var st [5]byte
	b, ok := m.starBatch.get(e.Chat)
	if !ok || len(b) != 5 || b[4] == 0 || binary.LittleEndian.Uint32(b) != e.Batch {
		m.stars.delPrefix(m.stars.prefix(e.Chat))
	}
	binary.LittleEndian.PutUint32(st[:], e.Batch)
	if !e.Last {
		st[4] = 1
	}
	m.starBatch.set(e.Chat, st[:])
	for i := range e.Ids {
		m.stars.set(m.stars.pair(e.Chat, e.Ids[i].ID), nil)
	}
	m.gen++
}

func (m *marks) setStar(chat, id []byte, on bool) {
	if on {
		m.stars.set(m.stars.pair(chat, id), nil)
	} else {
		m.stars.del(m.stars.pair(chat, id))
	}
	m.gen++
}

func (m *marks) pinChunk(e *abi.EventPins) {
	if !m.pinStream || e.Batch != m.pinBatch || !bytes.Equal(m.pinChat, e.Chat) {
		m.pinBuf = m.pinBuf[:0]
		m.pinChat = append(m.pinChat[:0], e.Chat...)
	}
	m.pinBatch, m.pinStream = e.Batch, !e.Last
	for i := range e.Items {
		at := len(m.pinBuf)
		m.pinBuf = append(m.pinBuf, 0, 0, 0, 0)
		var err error
		if m.pinBuf, err = e.Items[i].Encode(m.pinBuf); err != nil {
			m.pinBuf = m.pinBuf[:at]
			continue
		}
		binary.LittleEndian.PutUint32(m.pinBuf[at:], uint32(len(m.pinBuf)-at-4))
	}
	if !e.Last {
		return
	}
	m.pins.set(e.Chat, m.pinBuf)
	m.pinIDs.delPrefix(m.pinIDs.prefix(e.Chat))
	for i := range e.Items {
		m.pinIDs.set(m.pinIDs.pair(e.Chat, e.Items[i].ID), nil)
	}
	m.gen++
}

func (m *marks) setPinned(chat, id []byte, on bool) {
	if on {
		m.pinIDs.set(m.pinIDs.pair(chat, id), nil)
	} else {
		m.pinIDs.del(m.pinIDs.pair(chat, id))
	}
	m.gen++
}

// forget drops a message's star and pin (deleted, or revoked for everyone).
func (m *marks) forget(chat, id []byte) {
	m.stars.del(m.stars.pair(chat, id))
	m.pinIDs.del(m.pinIDs.pair(chat, id))
	b, ok := m.pins.get(chat)
	if !ok {
		m.gen++
		return
	}
	m.fbuf = m.fbuf[:0]
	dropped := false
	for rest := b; len(rest) >= 4; {
		n := int(binary.LittleEndian.Uint32(rest))
		if 4+n > len(rest) {
			break
		}
		rec := rest[:4+n]
		rest = rest[4+n:]
		if m.scratch.Decode(rec[4:]) == nil && bytes.Equal(m.scratch.ID, id) {
			dropped = true
			continue
		}
		m.fbuf = append(m.fbuf, rec...)
	}
	m.scratch.Release()
	if dropped {
		m.pins.set(chat, m.fbuf)
	}
	m.gen++
}

// forgetChat drops the chat's stars (removed or cleared) and, with pins,
// its pins (cleared: zf:src/app.rs:3656).
func (m *marks) forgetChat(chat []byte, pins bool) {
	m.stars.delPrefix(m.stars.prefix(chat))
	m.starBatch.del(chat)
	if pins {
		m.pinIDs.delPrefix(m.pinIDs.prefix(chat))
		m.pins.del(chat)
	}
	m.gen++
}

func pinRecord(b []byte, i int) []byte {
	for rest := b; len(rest) >= 4; i-- {
		n := int(binary.LittleEndian.Uint32(rest))
		if 4+n > len(rest) {
			return nil
		}
		if i == 0 {
			return rest[4 : 4+n]
		}
		rest = rest[4+n:]
	}
	return nil
}

// Starred reports whether a message is starred.
func (s *Store) Starred(chat, msg []byte) bool {
	return s.marks.stars.has(s.marks.stars.pair(chat, msg))
}

// PinnedMessage reports whether a message is pinned in its chat (the mark
// on its bubble).
func (s *Store) PinnedMessage(chat, msg []byte) bool {
	return s.marks.pinIDs.has(s.marks.pinIDs.pair(chat, msg))
}

// ChatPins is the number of the chat's pins the core listed (LoadPins asks;
// the line under the header). Pins past their ExpiresAt are listed until
// the core lists the chat again: the line skips them.
func (s *Store) ChatPins(chat []byte) int {
	b, _ := s.marks.pins.get(chat)
	n := 0
	for rest := b; len(rest) >= 4; n++ {
		k := int(binary.LittleEndian.Uint32(rest))
		if 4+k > len(rest) {
			break
		}
		rest = rest[4+k:]
	}
	return n
}

// ChatPin decodes the chat's pin i into dst (its bytes then point into the
// store, valid until the next apply).
func (s *Store) ChatPin(chat []byte, i int, dst *abi.Pinned) error {
	b, _ := s.marks.pins.get(chat)
	rec := pinRecord(b, i)
	if rec == nil {
		return errNoRecord
	}
	return dst.Decode(rec)
}

// MarksGen moves whenever a star or a pin changes.
func (s *Store) MarksGen() uint32 { return s.marks.gen }

type modelError string

func (e modelError) Error() string { return string(e) }

const errNoRecord = modelError("zapfast/model: no such record")
