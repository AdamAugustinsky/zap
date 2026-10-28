package model

import (
	"strconv"
	"testing"
)

type indexKeys [][]byte

func (k indexKeys) key(row uint32) []byte { return k[row] }

func TestIndexGrowthAfterShrink(t *testing.T) {
	var x index
	x.reset(128)
	x.reset(8) // reindex after removal retains the large backing array
	keys := make(indexKeys, 9)
	for i := range keys {
		keys[i] = []byte(strconv.Itoa(i))
		x.add(keys[i], uint32(i))
	}
	for i, key := range keys {
		if got := x.find(key, keys); got != i {
			t.Fatalf("row %d: lookup %d after growth in reused storage", i, got)
		}
	}
}

func TestIndexCapacityAndCollisions(t *testing.T) {
	var x index
	x.reset(0) // Conversation.reset starts with an empty, initialized table.
	keys := make(indexKeys, 10000)
	for i := range keys {
		keys[i] = []byte(strconv.Itoa(i))
		x.add(keys[i], uint32(i))
	}
	if cap(x.slots) != 32768 {
		t.Errorf("10k rows retain %d slots, want 32768", cap(x.slots))
	}
	for i, id := range keys {
		if got := x.find(id, keys); got != i {
			t.Fatalf("row %d: lookup %d", i, got)
		}
	}
	// Force long probe chains across every growth boundary, including wrap.
	x = index{}
	keys = nil
	for i := 0; len(keys) < 128; i++ {
		id := []byte(strconv.Itoa(i))
		if hashOf(id)&1023 != 1023 {
			continue
		}
		keys = append(keys, id)
		x.add(id, uint32(len(keys)-1))
		for row, key := range keys {
			if got := x.find(key, keys); got != row {
				t.Fatalf("collision row %d: lookup %d", row, got)
			}
		}
	}
	if x.find([]byte("missing"), keys) != -1 {
		t.Fatal("absent ID found")
	}
}
