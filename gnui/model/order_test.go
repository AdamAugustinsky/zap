package model

import (
	"bytes"
	"fmt"
	"math/rand/v2"
	"slices"
	"testing"

	"zapfast/abi"
)

func TestOrderedSortKeepsIndex(t *testing.T) {
	var v Conversation
	v.reset([]byte("ordered@demo.invalid"))
	v.upsert(&abi.Message{ID: []byte("a"), Timestamp: 1})
	// A valid over-reserved table makes an unnecessary reindex observable.
	v.idx.reset(64)
	v.idx.add(v.At(0).ID(), 0)
	n := len(v.idx.slots)
	v.sort()
	if len(v.idx.slots) != n || v.Find([]byte("a")) != 0 {
		t.Fatal("already ordered rows rebuilt their index")
	}
}

func TestSortMatchesStableReference(t *testing.T) {
	rng := rand.New(rand.NewPCG(1, 2))
	var got, want Conversation
	got.reset([]byte("sort@demo.invalid"))
	want.reset(got.Chat())
	for step := 0; step < 1000; step++ {
		id := step
		ts := int64(step / 3) // include stable ties
		if step > 0 {
			switch rng.IntN(4) {
			case 0: // content edit, retaining the ordering keys
				row := rng.IntN(want.Len())
				if err := want.Decode(row, &want.edit); err != nil {
					t.Fatal(err)
				}
				m := want.edit
				m.Content.Text.Text = []byte(fmt.Sprint("edit ", step))
				got.upsert(&m)
				want.upsert(&m)
			case 1: // prepend
				ts = -int64(step)
			case 2: // timestamp / history-order change of an existing ID
				id = rng.IntN(step)
				ts = int64(rng.IntN(step))
			}
		}
		m := abi.Message{ID: []byte(fmt.Sprint(id)), Timestamp: ts,
			HasHistoryOrder: true, HistoryOrder: int64(rng.IntN(3))}
		m.Content.Tag = abi.ContentTagText
		m.Content.Text.Text = []byte(fmt.Sprint("body ", step))
		got.upsert(&m)
		want.upsert(&m)
		got.sort()
		// The former implementation, retained only as the reference.
		slices.SortStableFunc(want.recs, compareMsg)
		want.reindex()
		if got.Len() != want.Len() {
			t.Fatalf("step %d: lengths differ", step)
		}
		for i := range want.recs {
			a, b := got.At(i), want.At(i)
			if !bytes.Equal(a.ID(), b.ID()) || !bytes.Equal(a.Text(), b.Text()) || got.Find(b.ID()) != i {
				t.Fatalf("step %d row %d: got %q want %q, lookup %d", step, i, a.ID(), b.ID(), got.Find(b.ID()))
			}
		}
	}
}

func TestOrderGenerationSlotReuse(t *testing.T) {
	var v Conversation
	v.reset([]byte("first@demo.invalid"))
	m := abi.Message{ID: []byte("a"), Timestamp: 1}
	v.upsert(&m)
	before := v.OrderGen()
	m.Timestamp++ // changed key, but no actual move
	v.upsert(&m)
	v.sort()
	if v.OrderGen() != before {
		t.Fatal("unchanged order advanced its generation")
	}
	v.reset([]byte("second@demo.invalid"))
	v.upsert(&m)
	if v.OrderGen() == before {
		t.Fatal("reused slot did not invalidate its order")
	}
}
