package main

import (
	"slices"
	"testing"

	"gonativeui/ui"
	"gonativeui/widget/widgettest"

	"zapfast/abi"
	"zapfast/core/fake"
)

// KB-005/006: upstream keys.rs wraps within visible_chats(), and with no
// current visible chat chooses the first row in either direction.
func TestKeyboardChatNavigationWrapsWithinTheVisibleList(t *testing.T) {
	for _, route := range []string{"alt", "whatsapp"} {
		for _, tc := range []struct {
			name        string
			n           int
			unread      []int
			start, want string
			step        int
		}{
			{"empty-next", 0, nil, "", "", 1}, {"empty-previous", 0, nil, "", "", -1},
			{"filtered-empty", 3, nil, "", "", 1},
			{"filtered-empty-previous", 3, nil, "", "", -1},
			{"one-next", 1, []int{0}, "Ada", "Ada", 1}, {"one-previous", 1, []int{0}, "Ada", "Ada", -1},
			{"first-to-last", 3, []int{0, 1, 2}, "Ada", "Katherine", -1},
			{"last-to-first", 3, []int{0, 1, 2}, "Katherine", "Ada", 1},
			{"no-current-previous", 3, []int{0, 1, 2}, "", "Ada", -1},
			{"no-current-next", 3, []int{0, 1, 2}, "", "Ada", 1},
			{"filtered-one", 3, []int{1}, "Grace", "Grace", 1},
		} {
			t.Run(route+"/"+tc.name, func(t *testing.T) {
				f := fake.New(nil)
				names := []string{"Ada", "Grace", "Katherine"}
				var visible []string
				f.Emit(ev(abi.EventTagLink, func(e *abi.Event) { e.Link.Status.Tag = abi.LinkStatusTagConnected }),
					ev(abi.EventTagChats, func(e *abi.Event) {
						e.Chats = abi.EventChats{Batch: 1, Last: true}
						for i := 0; i < tc.n; i++ {
							ch := abi.Chat{ID: []byte(string(rune('1'+i)) + "@s.whatsapp.net"), Name: []byte(names[i]), LastActivity: int64(30 - i)}
							if slices.Contains(tc.unread, i) {
								ch.Unread = 1
								visible = append(visible, names[i])
							}
							e.Chats.Chats = append(e.Chats.Chats, ch)
						}
					}))
				r := newRig(t, f)
				r.Play(widgettest.Click(ui.RoleCheckbox, "Unread"), widgettest.Idle(3))
				if tc.start != "" {
					r.Play(widgettest.Click(ui.RoleListItem, tc.start), widgettest.Idle(3))
					r.Play(widgettest.Click(ui.RoleTextField, "Message"), widgettest.Idle(2))
					if r.focusedName() != "Message" {
						t.Fatal("composer did not take focus")
					}
				}
				key, mods := ui.KeyDown, ui.ModAlt
				if tc.step < 0 {
					key = ui.KeyUp
				}
				if route == "whatsapp" {
					key, mods = ']', ui.PrimaryMod()|ui.ModShift
					if tc.step < 0 {
						key = '['
					}
				}
				r.Play(widgettest.Key(key, mods), widgettest.Idle(4))
				got := ""
				if ch, ok := r.s.Acct().OpenChatView(); ok {
					got = string(ch.Name())
				}
				if got != tc.want {
					t.Fatalf("opened %q, want %q", got, tc.want)
				}
				if tc.start != "" && r.focusedName() != "Message" {
					t.Fatal("navigation left the composer")
				}
				if got := unreadNames(r); !slices.Equal(got, visible) {
					t.Fatalf("navigation changed filter membership: %v, want %v", got, visible)
				}
			})
		}
	}
}

func TestNumberedChatShortcutDoesNotEscapeAnEmptyFilter(t *testing.T) {
	r := unreadRig(t)
	a := r.s.Acct()
	for _, id := range []string{adaJID, graceJID, "3@s.whatsapp.net"} {
		a.OpenChat(a.Model.Chats.Find([]byte(id))) // explicit opens do not retain
		r.Frames(3)
	}
	a.OpenChat(0)
	r.Frames(3)
	if len(unreadNames(r)) != 0 {
		t.Fatal("Unread filter is not empty")
	}
	r.Play(widgettest.Key('1', ui.PrimaryMod()), widgettest.Idle(3))
	if a.Open != 0 {
		t.Fatal("numbered shortcut escaped the empty visible list")
	}
}
