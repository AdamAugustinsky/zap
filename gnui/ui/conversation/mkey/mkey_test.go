package mkey

import (
	"testing"

	"zapfast/abi"
	"zapfast/core/fake"
	"zapfast/state"
)

// One message id in two accounts and in two chats is three keys, and
// making one allocates nothing.
func TestKeysTellAccountsAndChatsApart(t *testing.T) {
	s := state.New()
	a1, a2 := s.AddAccount("1", nil, nil), s.AddAccount("2", nil, nil)
	id := []byte("3EB0DUPLICATE")
	a1.Open, a2.Open = 7, 7
	k1, k2 := Of(a1, id), Of(a2, id)
	a1.Open = 8
	k3 := Of(a1, id)
	if k1 == k2 || k1 == k3 || k2 == k3 {
		t.Errorf("keys alike: %+v %+v %+v", k1, k2, k3)
	}
	if k3 != Of(a1, id) {
		t.Error("a key is not its message's")
	}
	if n := testing.AllocsPerRun(100, func() { _ = Of(a1, id).Slot(512) }); n != 0 {
		t.Errorf("a key allocates %v", n)
	}
}

func TestKeysSurviveAccountReindex(t *testing.T) {
	s := state.New()
	f := fake.New(nil)
	a, b := s.AddAccount("1", nil, f), s.AddAccount("2", nil, nil)
	a.Open, b.Open = 1, 1
	id := []byte("same-group-message")
	ka, kb := Of(a, id), Of(b, id)
	f.Emit(&abi.Event{Tag: abi.EventTagAccountRemoved})
	s.Pump(0)
	c := s.AddAccount("3", nil, nil)
	c.Open = 1
	if b.Index != 0 || c.Index != 1 {
		t.Fatal("fixture did not reindex B and reuse its old slot for C")
	}
	if got := Of(b, id); got != kb {
		t.Errorf("B's key changed: %v -> %v", kb, got)
	}
	if Of(b, id) == ka || Of(c, id) == ka || Of(c, id) == kb {
		t.Error("a surviving or new account inherited another account's key")
	}
	Enter(b)
	t.Cleanup(func() { Enter(nil) })
	if Here(id) != kb || In(b, b.Open, id) != kb {
		t.Error("Here or In lost B's identity")
	}
	d := state.New().AddAccount("1", nil, nil)
	d.Open = 1
	if Of(d, id) == ka || Of(d, id) == kb || Of(d, id) == Of(c, id) || Of(nil, id) == Of(d, id) {
		t.Error("identity was reused across app states or for nil")
	}
	if n := testing.AllocsPerRun(100, func() {
		_ = Of(b, id).Slot(512)
		_ = In(b, b.Open, id)
		_ = Here(id)
	}); n != 0 {
		t.Errorf("keys allocate %v", n)
	}
}
