package main

import (
	"slices"
	"testing"

	"gonativeui/ui"
	"gonativeui/widget/widgettest"

	"zapfast/abi"
	"zapfast/core/fake"
	"zapfast/state"
)

func unreadRig(t *testing.T) *rig {
	t.Helper()
	f := fake.New(nil)
	f.Emit(ev(abi.EventTagLink, func(e *abi.Event) { e.Link.Status.Tag = abi.LinkStatusTagConnected }),
		ev(abi.EventTagChats, func(e *abi.Event) {
			e.Chats = abi.EventChats{Batch: 1, Last: true, Chats: []abi.Chat{
				{ID: []byte(adaJID), Name: []byte("Ada"), LastActivity: 30, Unread: 1},
				{ID: []byte(graceJID), Name: []byte("Grace"), LastActivity: 20, Unread: 1},
				{ID: []byte("3@s.whatsapp.net"), Name: []byte("Katherine"), LastActivity: 10, Unread: 1},
			}}
		}))
	r := newRig(t, f)
	r.Play(widgettest.Click(ui.RoleCheckbox, "Unread"), widgettest.Idle(3))
	return r
}

func unreadNames(r *rig) []string {
	var names []string
	for _, ref := range r.s.Visible {
		if ch, ok := r.s.Acct().Model.Chats.Get(ref); ok {
			names = append(names, string(ch.Name()))
		}
	}
	return names
}

// CL-032: run the real shell, list and read reducer. Keyboard navigation must
// retain the just-read row exactly as a click does, then clear retention when
// the user leaves and returns to Unread. Numbered shortcuts share upstream's
// KeepUnread action; explicit OpenChat requests do not.
func TestUnreadKeyboardNavigationRetainsRowsLikeClicking(t *testing.T) {
	for _, tc := range []struct {
		name       string
		startGrace bool
		step       widgettest.Step
	}{
		{"click", false, widgettest.Click(ui.RoleListItem, "Ada")},
		{"alt-next", false, widgettest.Key(ui.KeyDown, ui.ModAlt)},
		{"alt-previous", true, widgettest.Key(ui.KeyUp, ui.ModAlt)},
		{"whatsapp-next", false, widgettest.Key(']', ui.PrimaryMod()|ui.ModShift)},
		{"whatsapp-previous", true, widgettest.Key('[', ui.PrimaryMod()|ui.ModShift)},
		{"numbered", false, widgettest.Key('1', ui.PrimaryMod())},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := unreadRig(t)
			if tc.startGrace {
				r.Play(widgettest.Click(ui.RoleListItem, "Grace"), widgettest.Idle(3))
			}
			r.Play(tc.step, widgettest.Idle(4))
			a := r.s.Acct()
			ch, ok := a.OpenChatView()
			if !ok || string(ch.Name()) != "Ada" || ch.Unread() != 0 {
				t.Fatalf("navigation did not open/read Ada: %v", unreadNames(r))
			}
			if got := unreadNames(r); !slices.Equal(got, []string{"Ada", "Grace", "Katherine"}) {
				t.Fatalf("reading shifted the Unread list: %v", got)
			}
			r.Play(widgettest.Key(ui.KeyDown, ui.ModAlt), widgettest.Idle(4))
			if ch, _ := a.OpenChatView(); string(ch.Name()) != "Grace" {
				t.Fatal("next skipped Grace")
			}
			r.Play(widgettest.Click(ui.RoleCheckbox, "Unread"), widgettest.Idle(2),
				widgettest.Click(ui.RoleCheckbox, "Unread"), widgettest.Idle(3))
			if got := unreadNames(r); !slices.Equal(got, []string{"Katherine"}) {
				t.Fatalf("filter change kept read rows: %v", got)
			}
		})
	}
}

func TestExplicitChatOpenDoesNotRetainAnUnreadRow(t *testing.T) {
	r := unreadRig(t)
	a := r.s.Acct()
	a.OpenChat(a.Model.Chats.Find([]byte(adaJID)))
	r.Frames(4)
	if got := unreadNames(r); !slices.Equal(got, []string{"Grace", "Katherine"}) {
		t.Fatalf("explicit jump retained a read row: %v", got)
	}
	if a.Filter != state.FilterUnread {
		t.Fatal("explicit jump changed the filter")
	}
}

func TestUnreadKeyboardRetentionRespectsSearchFolderAndAccount(t *testing.T) {
	t.Run("search", func(t *testing.T) {
		r := unreadRig(t)
		r.Play(widgettest.Click(ui.RoleSearchField, "Search"), widgettest.Type("Ada"), widgettest.Idle(3),
			widgettest.Key(ui.KeyDown, ui.ModAlt), widgettest.Idle(4),
			widgettest.Key(ui.KeyEscape, 0), widgettest.Idle(4))
		if got := unreadNames(r); !slices.Equal(got, []string{"Grace", "Katherine"}) {
			t.Fatalf("search navigation retained a read row: %v", got)
		}
	})
	t.Run("archive", func(t *testing.T) {
		r := unreadRig(t)
		a := r.s.Acct()
		update := func(archived bool, unread uint32) {
			a.Conn.(*fake.Core).Emit(ev(abi.EventTagChatUpdated, func(e *abi.Event) {
				e.ChatUpdated.Chat = abi.Chat{ID: []byte(adaJID), Name: []byte("Ada"), LastActivity: 30, Unread: unread, Archived: archived}
			}))
		}
		update(true, 1)
		a.Mode = state.ListArchived
		r.s.Touch()
		r.Frames(3)
		r.Play(widgettest.Key(ui.KeyDown, ui.ModAlt), widgettest.Idle(4))
		if ch, ok := a.OpenChatView(); !ok || string(ch.Name()) != "Ada" || ch.Unread() != 0 {
			t.Fatal("archive shortcut did not open/read Ada")
		}
		update(false, 0)
		a.Mode = state.ListChats
		r.s.Touch()
		r.Frames(3)
		if got := unreadNames(r); !slices.Equal(got, []string{"Grace", "Katherine"}) {
			t.Fatalf("archive navigation retained a read row: %v", got)
		}
	})
	t.Run("account", func(t *testing.T) {
		r := unreadRig(t)
		r.Play(widgettest.Key(ui.KeyDown, ui.ModAlt), widgettest.Idle(4))
		g := fake.New(nil)
		g.Emit(ev(abi.EventTagLink, func(e *abi.Event) { e.Link.Status.Tag = abi.LinkStatusTagConnected }),
			ev(abi.EventTagChats, func(e *abi.Event) {
				e.Chats = abi.EventChats{Batch: 1, Last: true, Chats: []abi.Chat{
					{ID: []byte(adaJID), Name: []byte("Other Ada"), LastActivity: 30, Unread: 1},
					{ID: []byte(graceJID), Name: []byte("Other Grace"), LastActivity: 20, Unread: 1},
				}}
			}))
		r.s.AddAccount("2", nil, g)
		r.s.SwitchAccount(1)
		r.s.Acct().Filter = state.FilterUnread
		// Navigate before the new account's first list build. Its retention
		// must survive that build, without carrying the previous account's set.
		r.Play(widgettest.Key(ui.KeyDown, ui.ModAlt), widgettest.Idle(4))
		if got := unreadNames(r); !slices.Equal(got, []string{"Other Ada", "Other Grace"}) {
			t.Fatalf("new account lost its retained row: %v", got)
		}
		r.s.SwitchAccount(0)
		r.Frames(4)
		if got := unreadNames(r); !slices.Equal(got, []string{"Grace", "Katherine"}) {
			t.Fatalf("old account retained rows after being left: %v", got)
		}
	})
}
