package chatlist

import (
	"image"
	"image/png"
	"os"
	"path/filepath"
	"testing"

	"gonativeui/platform"
	"gonativeui/ui"
	"gonativeui/widget/widgettest"

	"zapfast/abi"
	"zapfast/state"
)

// TestCaptureScenes writes the scenes the parity table's capture rows name to
// the directory in ZAPFAST_CAPTURE (docs/parity/chatlist), the list's left
// 500 points of the main window at the window's scale. It is a generator, not a
// check: without the variable it skips.
func TestCaptureScenes(t *testing.T) {
	dir := os.Getenv("ZAPFAST_CAPTURE")
	if dir == "" {
		t.Skip("set ZAPFAST_CAPTURE=<dir> to write the scenes")
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	save := func(r *rig, name string) {
		r.Frames(3)
		img := r.Image()
		w := min(img.Bounds().Dx(), 520)
		sub := img.SubImage(image.Rect(0, 0, w, img.Bounds().Dy()))
		f, err := os.Create(filepath.Join(dir, name+".png"))
		if err != nil {
			t.Fatal(err)
		}
		defer f.Close()
		if err := png.Encode(f, sub); err != nil {
			t.Fatal(err)
		}
	}
	demo := func(r *rig) {
		a := chat("1", "Ada Lovelace", 50)
		a.Unread, a.Pinned, a.PinnedAt = 3, true, 1
		a.HasLast, a.Last = true, textMsg("1@s.whatsapp.net", false, "That is wonderful news. Send me the notes tonight.")
		g := chat("2", "Rust Berlin", 40)
		g.Kind, g.Unread, g.HasMutedUntil = abi.ChatKindGroup, 14, true
		g.HasUnreadMention, g.UnreadMention = true, []byte("me")
		g.HasLast, g.Last = true, textMsg("9@s.whatsapp.net", false, "Save me a seat")
		g.Last.SenderName, g.Last.HasSenderName = []byte("Rust Fan"), true
		h := chat("3", "Grace Hopper", 30)
		h.HasLast, h.Last = true, textMsg("me", true, "Literally. Taped it into the logbook.")
		h.Last.Status = abi.DeliveryRead
		d := chat("4", "Katherine Johnson", 20)
		d.HasEphemeralExpiration, d.EphemeralExpiration = true, 86400
		d.MarkedUnread = true
		d.HasLast, d.Last = true, textMsg("4@s.whatsapp.net", false, "Pushed")
		r.f.Emit(ev(abi.EventTagLabels, func(e *abi.Event) {
			e.Labels = abi.EventLabels{Batch: 1, Last: true, Labels: []abi.Label{
				{ID: []byte("l1"), Name: []byte("Work"), ColorHex: []byte("#3b82f6")}, {ID: []byte("l2"), Name: []byte("Family"), ColorHex: []byte("#22c55e")}}}
		}))
		r.f.Emit(ev(abi.EventTagDrafts, func(e *abi.Event) {
			e.Drafts = abi.EventDrafts{Batch: 1, Last: true, Drafts: []abi.Draft{{Chat: jid("3"), Text: []byte("On my way")}}}
		}))
		r.f.Emit(ev(abi.EventTagMe, func(e *abi.Event) { e.Me = abi.EventMe{ID: jid("5511999"), Name: []byte("Dana"), HasName: true} }))
		r.chats(a, g, h, d)
	}
	always := newRig(t) // "Show scroll bars: Always", the narrowest list: the chips do not fit, and no bar shows under them
	always.s.Settings.SidebarWidth = 240
	always.Context().SetSystemValues(2, &platform.SystemValues{Known: true, LegacyScrollers: true})
	demo(always)
	save(always, "chips-scroll-always")
	r := newRig(t)
	r.s.Settings.SidebarWidth = 360
	demo(r)
	save(r, "chat")
	save(r, "label-chips")
	r.Play(widgettest.Click(ui.RoleCheckbox, "Unread"), widgettest.Idle(2))
	save(r, "unread")
	r.Play(widgettest.Click(ui.RoleCheckbox, "Unread"), widgettest.Idle(2))
	searchFor(r, "a")
	save(r, "search")
	r.Play(widgettest.Key(ui.KeyEscape, 0), widgettest.Idle(2))
	searchFor(r, "dana")
	save(r, "new-chat")
	r.Play(widgettest.Key(ui.KeyEscape, 0), widgettest.Idle(2))
	r.acct().Mode = state.ListArchived
	r.s.Touch()
	save(r, "archived")
	r.acct().Mode = state.ListLocked
	r.s.Touch()
	save(r, "locked-empty")
	r.acct().Mode = state.ListStarred
	r.s.Touch()
	r.Frames(2)
	r.f.Emit(ev(abi.EventTagStarredList, func(e *abi.Event) {
		e.StarredList = abi.EventStarredList{Batch: 1, Last: true, Starred: []abi.Starred{{StarredAt: 9, Message: abi.Message{
			Chat: jid("1"), ID: []byte("m1"), Timestamp: 100,
			Content: abi.Content{Tag: abi.ContentTagText, Text: abi.ContentText{Text: []byte("remember the milk")}}}}}}
	}))
	save(r, "starred")
	r.acct().Mode = state.ListChats
	r.s.OpenDialog(state.Dialog{Kind: state.DialogLabels})
	save2 := func(name string) {
		r.Frames(3)
		f, _ := os.Create(filepath.Join(dir, name+".png"))
		defer f.Close()
		png.Encode(f, r.Image())
	}
	save2("labels-dialog")
}
