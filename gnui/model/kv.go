package model

import "bytes"

// kv maps byte keys to byte values in one arena, pointer-free: drafts,
// presence, stars, pins, downloads. A deleted row stays (dead) until the
// next compaction, so the index never needs a tombstone of its own.
type kv struct {
	recs  []kvRec
	a     arena
	spare []byte
	idx   index
	dead  int
	gen   uint32
	kbuf  []byte // the composite key being looked up
	pbuf  []byte
}

type kvRec struct {
	k, v  Str
	alive bool
}

func (m *kv) key(row uint32) []byte { return m.a.at(m.recs[row].k) }

// pair is the key of (a, b): a, a zero byte, b. JIDs and message ids hold no
// zero byte, so the pairs of one a share the prefix a+0.
func (m *kv) pair(a, b []byte) []byte {
	m.kbuf = append(append(append(m.kbuf[:0], a...), 0), b...)
	return m.kbuf
}

// prefix is the prefix every pair of a starts with.
func (m *kv) prefix(a []byte) []byte {
	m.kbuf = append(append(m.kbuf[:0], a...), 0)
	return m.kbuf
}

func (m *kv) get(k []byte) ([]byte, bool) {
	i := m.idx.find(k, m)
	if i < 0 || !m.recs[i].alive {
		return nil, false
	}
	return m.a.at(m.recs[i].v), true
}

func (m *kv) has(k []byte) bool {
	_, ok := m.get(k)
	return ok
}

func (m *kv) set(k, v []byte) {
	m.gen++
	if i := m.idx.find(k, m); i >= 0 {
		r := &m.recs[i]
		if !r.alive {
			r.alive = true
			m.dead--
		}
		m.a.drop(r.v)
		r.v = m.a.put(v)
	} else {
		m.recs = append(m.recs, kvRec{k: m.a.put(k), v: m.a.put(v), alive: true})
		m.idx.add(k, uint32(len(m.recs)-1))
	}
	m.compact(false)
}

func (m *kv) del(k []byte) bool {
	i := m.idx.find(k, m)
	if i < 0 || !m.recs[i].alive {
		return false
	}
	r := &m.recs[i]
	r.alive = false
	m.a.drop(r.v)
	r.v = Str{}
	m.dead++
	m.gen++
	m.compact(false)
	return true
}

// delPrefix deletes every row whose key starts with p.
func (m *kv) delPrefix(p []byte) bool {
	m.pbuf = append(m.pbuf[:0], p...) // p may be kbuf
	p = m.pbuf
	hit := false
	for i := range m.recs {
		r := &m.recs[i]
		if r.alive && bytes.HasPrefix(m.a.at(r.k), p) {
			r.alive = false
			m.a.drop(r.v)
			r.v = Str{}
			m.dead++
			hit = true
		}
	}
	if hit {
		m.gen++
		m.compact(false)
	}
	return hit
}

// each calls f for every live row until it returns false.
func (m *kv) each(f func(k, v []byte) bool) {
	for i := range m.recs {
		if r := &m.recs[i]; r.alive && !f(m.a.at(r.k), m.a.at(r.v)) {
			return
		}
	}
}

func (m *kv) len() int { return len(m.recs) - m.dead }

func (m *kv) reset() {
	m.recs, m.dead = m.recs[:0], 0
	m.a.wipe()
	clear(m.spare)
	m.idx.reset(0)
	m.gen++
}

// compact drops the dead rows and the arena's garbage once they are worth
// it (or always, when force).
func (m *kv) compact(force bool) {
	if !force && !m.a.wasteful() && (m.dead < 64 || m.dead*2 < len(m.recs)) {
		return
	}
	old := m.a.b
	m.a.b, m.spare = m.spare[:0], old
	m.a.live = 0
	n := 0
	for _, r := range m.recs {
		if !r.alive {
			continue
		}
		m.recs[n] = kvRec{k: m.a.put(old[r.k.Off : r.k.Off+r.k.Len]), v: m.a.put(old[r.v.Off : r.v.Off+r.v.Len]), alive: true}
		n++
	}
	m.recs, m.dead = m.recs[:n], 0
	m.idx.reset(n)
	for i := range m.recs {
		m.idx.add(m.a.at(m.recs[i].k), uint32(i))
	}
}
