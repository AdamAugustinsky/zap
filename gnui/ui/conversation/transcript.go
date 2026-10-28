package conversation

import (
	"gonativeui/widget/base"

	"zapfast/model"
)

// transcript keeps the framework's VListState in step with the model's
// loaded messages (C1): one item per message, keyed by a hash of its id, in
// the model's order. Each model change is turned into the Splices that make
// the list's order the model's again, so the anchor holds when history is
// prepended, a delete for everyone removes a row mid-list or backfilled
// history arrives between rows (CV-008, CV-036).
type transcript struct {
	st   base.VListState
	ids  []uint64 // the list's order, as last synced: the model's order then
	next []uint64
	cur  idTable // ids → model index
	prev idTable
	conv *model.Conversation
	gen  uint32
	// resets and splices count the edits, for the tests.
	resets, splices int
}

// reserve sizes the list for n messages.
func (t *transcript) reserve(n int) {
	t.st.Reserve(n)
	if cap(t.ids) < n {
		t.ids = append(make([]uint64, 0, n), t.ids...)
		t.next = make([]uint64, 0, n)
	}
}

// order fills t.next with the model's ids and t.cur with their indexes.
func (t *transcript) order(v *model.Conversation) {
	n := 0
	if v != nil {
		n = v.Len()
	}
	t.next = t.next[:0]
	t.cur.reset(n)
	for i := 0; i < n; i++ {
		id := itemID(v.At(i).ID())
		for !t.cur.put(id, int32(i)) { // a hash collision: the next free value
			id = id*0x9E3779B97F4A7C15 + 1
			if id == base.NoID {
				id--
			}
		}
		t.next = append(t.next, id)
	}
}

// sync brings the list up to date with v (nil: no messages). A new
// conversation replaces the list (VListState.Reset: at the bottom).
func (t *transcript) sync(v *model.Conversation) {
	if v == t.conv && (v == nil || v.OrderGen() == t.gen) {
		return
	}
	fresh := v != t.conv
	t.conv = v
	if v != nil {
		t.gen = v.OrderGen()
		if n := v.Len(); n+n/4 > cap(t.ids) {
			t.reserve(max(n+n/2, 256))
		}
	}
	t.prev, t.cur = t.cur, t.prev
	t.order(v)
	if fresh || len(t.ids) == 0 {
		t.st.Reset(t.next)
		t.resets++
		t.ids, t.next = t.next, t.ids
		return
	}
	if !t.diff() {
		t.st.Reset(t.next)
		t.resets++
	}
	t.ids, t.next = t.next, t.ids
}

// diff turns the change from t.ids (indexed by t.prev) to t.next (t.cur)
// into Splices. It reports false when the order changed beyond inserts and
// removals (a message moved), which the caller replaces whole.
func (t *transcript) diff() bool {
	old, nw := t.ids, t.next
	i, j, pos := 0, 0, 0
	for i < len(old) || j < len(nw) {
		if i < len(old) && j < len(nw) && old[i] == nw[j] {
			i, j, pos = i+1, j+1, pos+1
			continue
		}
		if i < len(old) && t.cur.get(old[i]) < 0 {
			k := i
			for k < len(old) && t.cur.get(old[k]) < 0 {
				k++
			}
			t.st.Splice(pos, k-i, nil)
			t.splices++
			i = k
			continue
		}
		if j < len(nw) && t.prev.get(nw[j]) < 0 {
			k := j
			for k < len(nw) && t.prev.get(nw[k]) < 0 {
				k++
			}
			t.st.Splice(pos, 0, nw[j:k])
			t.splices++
			pos += k - j
			j = k
			continue
		}
		return false
	}
	return true
}

// index is the model index of item id, -1 when the model no longer has it.
func (t *transcript) index(id uint64) int {
	i := t.cur.get(id)
	if t.conv == nil || i < 0 || int(i) >= t.conv.Len() {
		return -1
	}
	return int(i)
}
