package state_test

import (
	"fmt"
	"testing"
	"time"

	"zapfast/abi"
	"zapfast/i18n"
	"zapfast/state"
)

// AX-001 (zf:src/announcer.rs tests): errors first, the rest in order, one
// text; stale ones dropped but errors wait; private ones silent while
// locked; the queue empties.
func TestAnnouncementsWaitTheirTurn(t *testing.T) {
	s := state.New()
	now := time.Unix(1000, 0)
	s.Now = func() time.Time { return now }
	s.Announce(state.AnnounceStatus, "Connection lost")
	s.Toast([]byte("Copied"), false)
	s.Toast([]byte("Could not send"), true)
	now = now.Add(state.AnnounceFresh + time.Millisecond)
	s.Announce(state.AnnounceStatus, "Back online")
	got, assertive := s.TakeAnnouncement(nil, true)
	if string(got) != "Could not send. Back online" || !assertive {
		t.Errorf("said %q (assertive %v)", got, assertive)
	}
	if got, _ := s.TakeAnnouncement(nil, true); len(got) != 0 {
		t.Errorf("said again: %q", got)
	}
	// Locked: the connection is said, a toast is not, nor later.
	s.Toast([]byte("Ada Lovelace: hi"), false)
	s.Announce(state.AnnounceStatus, "Connecting")
	if got, assertive := s.TakeAnnouncement(nil, false); string(got) != "Connecting" || assertive {
		t.Errorf("locked said %q (assertive %v)", got, assertive)
	}
	if got, _ := s.TakeAnnouncement(nil, true); len(got) != 0 {
		t.Errorf("the locked toast came back: %q", got)
	}
	// Nothing waiting allocates nothing.
	buf := make([]byte, 0, 64)
	if n := testing.AllocsPerRun(100, func() { buf, _ = s.TakeAnnouncement(buf[:0], true) }); n != 0 {
		t.Errorf("%v allocations with nothing to say", n)
	}
}

// AX-001: upstream's toast_said names the off-screen account, not the
// account currently on screen. The visual toast itself stays unprefixed.
func TestAccountToastAnnouncements(t *testing.T) {
	for _, tc := range []struct {
		name, message string
		tag           abi.EventTag
		err           bool
	}{
		{"info", "Fixture news", abi.EventTagInfo, false},
		{"error", "Fixture news", abi.EventTagError, true},
		{"poll created", "Fixture news", abi.EventTagPollCreated, true},
		{"poll voted", "Fixture news", abi.EventTagPollVoted, true},
		{"invite", "Request sent. An admin must approve it before you join.", abi.EventTagInviteJoined, false},
		{"refused", "Not sent: Zap is not connected to WhatsApp.", abi.EventTagSendRefused, true},
	} {
		for _, offscreen := range []bool{false, true} {
			for _, locked := range []bool{false, true} {
				t.Run(fmt.Sprintf("%s/offscreen=%v/locked=%v", tc.name, offscreen, locked), func(t *testing.T) {
					r := newRig(t, 2)
					i := 0
					if offscreen {
						i = 1
					}
					r.cores[i].Emit(ev(abi.EventTagMe, func(e *abi.Event) { e.Me.Name, e.Me.HasName = []byte("Studio"), true }))
					r.s.Lock.Locked = locked
					r.cores[i].Emit(ev(tc.tag, func(e *abi.Event) {
						text := []byte("Fixture news")
						e.Info.Text, e.Error.Text = text, text
						e.PollCreated.Error, e.PollCreated.HasError = text, true
						e.PollVoted.Error, e.PollVoted.HasError = text, true
						e.InviteJoined.Result.Ok.Pending = true
						e.SendRefused.Chat = []byte(ada)
					}))
					r.pump()
					if len(r.s.Toasts) != 1 || string(r.s.Toasts[0].Text) != tc.message || r.s.Toasts[0].Error != tc.err {
						t.Fatalf("visual toasts: %+v", r.s.Toasts)
					}
					want := tc.message
					if offscreen {
						want = "Studio: " + want
					}
					if locked {
						want = ""
					}
					if got, urgent := r.s.TakeAnnouncement(nil, !r.s.Lock.Locked); string(got) != want || urgent != (tc.err && !locked) {
						t.Errorf("announcement %q, urgent=%v; want %q", got, urgent, want)
					}
					if got, _ := r.s.TakeAnnouncement(nil, true); len(got) != 0 {
						t.Errorf("announcement replayed after unlock: %q", got)
					}
				})
			}
		}
	}
}

func TestOffscreenAccountToastKinds(t *testing.T) {
	r := newRig(t, 2)
	f := r.cores[1]
	f.Emit(ev(abi.EventTagMe, func(e *abi.Event) { e.Me.Name, e.Me.HasName = []byte("Studio"), true }),
		ev(abi.EventTagSyncing, func(e *abi.Event) { e.Syncing.Syncing = true }),
		ev(abi.EventTagLink, func(e *abi.Event) { e.Link.Status.Tag = abi.LinkStatusTagDisconnected }),
		ev(abi.EventTagStarChanged, func(e *abi.Event) { e.StarChanged.Local, e.StarChanged.Starred = true, true }),
		ev(abi.EventTagPinChanged, func(e *abi.Event) { e.PinChanged.Pinned = true }),
		ev(abi.EventTagStorageStats, func(e *abi.Event) { e.StorageStats.Cleared = true }))
	r.pump()
	if got, _ := r.s.TakeAnnouncement(nil, true); len(got) != 0 || len(r.s.Toasts) != 0 {
		t.Fatalf("off-screen status/star/pin/cache events surfaced: %q, %+v", got, r.s.Toasts)
	}
	f.Emit(ev(abi.EventTagSyncing, func(e *abi.Event) { e.Syncing.Syncing = false }),
		ev(abi.EventTagLink, func(e *abi.Event) { e.Link.Status.Tag = abi.LinkStatusTagConnected }))
	r.pump()
	if got, urgent := r.s.TakeAnnouncement(nil, true); string(got) != "Studio: History loaded. Studio: Back online" || urgent {
		t.Errorf("completed history/reconnect: %q, urgent=%v", got, urgent)
	}
}

func TestAccountToastPrefixUsesTranslatedTemplate(t *testing.T) {
	old := i18n.Current()
	t.Cleanup(func() { i18n.SetLocale(old) })
	for _, tc := range []struct{ tag, want string }{
		{"fr", "Studio {} : Fixture {}"},
		{"zh-Hans", "Studio {}：Fixture {}"},
	} {
		loc, _ := i18n.ParseTag(tc.tag)
		i18n.SetLocale(loc)
		r := newRig(t, 2)
		r.cores[1].Emit(ev(abi.EventTagMe, func(e *abi.Event) { e.Me.Name, e.Me.HasName = []byte("Studio {}"), true }),
			ev(abi.EventTagInfo, func(e *abi.Event) { e.Info.Text = []byte("Fixture {}") }))
		r.pump()
		if got, _ := r.s.TakeAnnouncement(nil, true); string(got) != tc.want {
			t.Errorf("%s: %q, want %q", tc.tag, got, tc.want)
		}
	}
}
