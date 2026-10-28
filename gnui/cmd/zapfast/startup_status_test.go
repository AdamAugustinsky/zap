package main

import (
	"testing"

	"gonativeui/ui"

	"zapfast/abi"
	"zapfast/core/fake"
)

// LG-014: archive readiness is independent of the network. No timers or live
// flags: withhold the archive, release a partial batch, then complete it.
func TestArchiveLoadingStatusFollowsArchiveReadiness(t *testing.T) {
	for _, tag := range []abi.LinkStatusTag{abi.LinkStatusTagConnected, abi.LinkStatusTagConnecting} {
		t.Run(tag.String(), func(t *testing.T) {
			f := fake.New(nil)
			f.Emit(ev(abi.EventTagLink, func(e *abi.Event) { e.Link.Status.Tag = tag }))
			r := newRig(t, f)
			loading := func() bool { _, ok := r.Find(ui.RoleGroup, "Info: Loading chat history…"); return ok }
			if _, ok := r.Find(ui.RoleGroup, "Loading your chats"); !ok {
				t.Fatal("no archive skeleton")
			}
			// A passing state's banner waits (bannerGrace): a quick start never
			// shows it, so the chat does not move down and back up.
			if _, ok := r.Find(ui.RoleGroup, "Info: Connecting to WhatsApp…"); ok || loading() {
				t.Fatal("the banner showed at once")
			}
			r.Frames(100)
			if tag == abi.LinkStatusTagConnected && !loading() {
				t.Fatal("connected link hid archive-loading status")
			}
			if tag == abi.LinkStatusTagConnecting {
				if _, ok := r.Find(ui.RoleGroup, "Info: Connecting to WhatsApp…"); !ok {
					t.Fatal("no connecting status")
				}
			}
			f.Emit(ev(abi.EventTagChats, func(e *abi.Event) {
				e.Chats = abi.EventChats{Batch: 1, Chats: []abi.Chat{{ID: []byte(adaJID), Name: []byte("Ada"), LastActivity: 1}}}
			}))
			r.Frames(3)
			if _, ok := r.Find(ui.RoleListItem, "Ada"); !ok {
				t.Fatal("partial cached rows were hidden")
			}
			if tag == abi.LinkStatusTagConnected && !loading() {
				t.Fatal("status vanished before the archive batch completed")
			}
			f.Emit(ev(abi.EventTagChats, func(e *abi.Event) { e.Chats = abi.EventChats{Batch: 1, Last: true} }),
				ev(abi.EventTagLink, func(e *abi.Event) { e.Link.Status.Tag = abi.LinkStatusTagConnected }))
			r.Frames(3)
			if loading() {
				t.Fatal("archive status stayed after completion")
			}
			if _, ok := r.Find(ui.RoleGroup, "Loading your chats"); ok {
				t.Fatal("skeleton stayed after completion")
			}
		})
	}
}

func TestReadyCachedArchiveShowsNoLoadingStatus(t *testing.T) {
	for _, empty := range []bool{false, true} {
		f := fake.New(nil)
		f.Emit(ev(abi.EventTagLink, func(e *abi.Event) { e.Link.Status.Tag = abi.LinkStatusTagConnected }),
			ev(abi.EventTagChats, func(e *abi.Event) {
				e.Chats = abi.EventChats{Batch: 1, Last: true}
				if !empty {
					e.Chats.Chats = []abi.Chat{{ID: []byte(adaJID), Name: []byte("Ada"), LastActivity: 1}}
				}
			}))
		r := newRig(t, f)
		for _, name := range []string{"Loading your chats", "Info: Loading chat history…", "Info: Connecting to WhatsApp…"} {
			if _, ok := r.Find(ui.RoleGroup, name); ok {
				t.Fatalf("ready archive empty=%v showed %q", empty, name)
			}
		}
		if !empty {
			if _, ok := r.Find(ui.RoleListItem, "Ada"); !ok {
				t.Fatal("cached row missing")
			}
		}
	}
}
