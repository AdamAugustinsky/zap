package conversation

import (
	"testing"

	"gonativeui/ui"
	"zapfast/abi"
)

func TestTranscriptContentAndOrderChanges(t *testing.T) {
	r := newRig(t)
	a := msg(ada, "a", base0, true, "first")
	b := msg(ada, "b", base0+120, false, "second")
	r.open(ada, a, b)
	tr := &r.scr.t
	v := r.a.Model.Conversation([]byte(ada))
	check := func(ids ...string) {
		t.Helper()
		if len(tr.ids) != len(ids) || tr.st.Len() != len(ids) {
			t.Fatalf("rows %d/%d, want %d", len(tr.ids), tr.st.Len(), len(ids))
		}
		var bottom float32
		for i, id := range ids {
			if tr.ids[i] != itemID([]byte(id)) || tr.index(tr.ids[i]) != i || string(v.At(i).ID()) != id {
				t.Fatalf("row %d: want %s", i, id)
			}
			bounds := r.node(ui.RoleLabel, string(v.At(i).Text())).Bounds
			if i > 0 && bounds[1] < bottom {
				t.Fatalf("rendered row %s is above its predecessor", id)
			}
			bottom = bounds[3]
		}
	}
	update := func(m abi.Message) {
		r.f.Emit(&abi.Event{Tag: abi.EventTagMessageUpdated, MessageUpdated: abi.EventMessageUpdated{Message: m}})
		r.Frames(3)
	}
	// The buffers swap only on an order rebuild, even if the diff is empty.
	for _, event := range []string{"reaction", "delivery", "edit", "identical-page", "loading"} {
		t.Run(event, func(t *testing.T) {
			order := &tr.ids[0]
			switch event {
			case "reaction":
				a.Reactions = []abi.Reaction{{Sender: []byte(ada), Emoji: []byte("👍")}}
				update(a)
				r.must(ui.RoleButton, "👍")
			case "delivery":
				a.Status = abi.DeliveryDelivered
				update(a)
				if v.At(0).Status() != a.Status {
					t.Fatal("stale delivery")
				}
			case "edit":
				a.Content.Text.Text = []byte("edited first")
				a.Edited = true
				update(a)
				r.mustNot(ui.RoleLabel, "first")
			case "identical-page":
				r.page(ada, false, true, a, b)
			case "loading":
				v.SetLoadingOlder(true)
				r.Frames(3)
			}
			check("a", "b")
			if order != &tr.ids[0] {
				t.Fatal("content-only event rebuilt order")
			}
		})
	}
	r.page(ada, true, true, msg(ada, "p", base0-60, false, "prepended"))
	check("p", "a", "b")
	r.page(ada, true, true, msg(ada, "f", base0+60, false, "backfill"))
	check("p", "a", "f", "b")
	a.Timestamp = base0 + 180
	update(a)
	check("p", "f", "b", "a")
	r.f.Emit(&abi.Event{Tag: abi.EventTagMessageDeleted, MessageDeleted: abi.EventMessageDeleted{Chat: []byte(ada), ID: []byte("f")}})
	r.Frames(3)
	check("p", "b", "a")
	r.mustNot(ui.RoleLabel, "backfill")
}
