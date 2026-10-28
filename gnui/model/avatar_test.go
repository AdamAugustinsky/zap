package model_test

import (
	"testing"

	"zapfast/abi"
	"zapfast/core/fake"
	"zapfast/model"
)

// Every picture the core reports has an identity of its own, even at the
// path of the last one, in another store, or after the session ends: image
// caches key on it, never on the path (zf:src/paths.rs:350 reuses it).
func TestEveryPictureHasItsOwnIdentity(t *testing.T) {
	f := fake.New(nil)
	one, two := model.New(), model.New()
	ada := []byte("5511@s.whatsapp.net")
	pictureID := func(s *model.Store, full bool) uint64 {
		_, id, _ := s.Avatars.Picture(ada, full)
		return id
	}
	avatar := func(s *model.Store, full, has bool) uint64 {
		t.Helper()
		f.Emit(ev(abi.EventTagAvatar, func(e *abi.Event) {
			e.Avatar = abi.EventAvatar{ID: ada, Full: full, Path: []byte("/cache/avatars/ada.jpg"), HasPath: has}
		}))
		if _, err := f.Drain(s, 0); err != nil {
			t.Fatal(err)
		}
		return pictureID(s, full)
	}
	if pictureID(one, false) != 0 {
		t.Error("a JID with no picture has an identity")
	}
	seen := map[uint64]bool{}
	for i, id := range []uint64{avatar(one, false, true), avatar(one, false, true), avatar(one, true, true), avatar(two, false, true)} {
		if id == 0 || seen[id] {
			t.Fatalf("picture %d: identity %#x is zero or not new", i, id)
		}
		seen[id] = true
	}
	thumb := pictureID(one, false)
	f.Emit(ev(abi.EventTagLink, func(e *abi.Event) { e.Link.Status.Tag = abi.LinkStatusTagLoggedOut }))
	f.Drain(one, 0)
	if pictureID(one, false) != 0 {
		t.Error("the ended session's picture keeps its identity")
	}
	if id := avatar(one, false, true); id == thumb || seen[id] {
		t.Error("the next session's picture at the same path has an old identity")
	}
	if avatar(one, false, false) != 0 {
		t.Error("a JID whose picture went has an identity")
	}
}
