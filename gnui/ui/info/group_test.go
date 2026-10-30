package info_test

import (
	"strings"
	"testing"

	"gonativeui/ui"
	"gonativeui/widget/widgettest"

	"zapfast/abi"
	"zapfast/state"
	"zapfast/ui/info"
)

func teamChat(mut func(c *abi.Chat)) chatSpec {
	return chatSpec{id: team, name: "Team", mut: func(c *abi.Chat) {
		c.Participants = [][]byte{b(me), b(ada), b(grace), b("447700900123@s.whatsapp.net")}
		c.HasGroupDescription, c.GroupDescription = true, b("Weekly sync notes and general chatter")
		c.Admin = true
		if mut != nil {
			mut(c)
		}
	}}
}

// IN-008, IN-009: a group shows its members and description; a member opens
// their own panel.
func TestGroupShowsMembersAndOpensOne(t *testing.T) {
	r := newRig(t, teamChat(nil), chatSpec{id: ada, name: "Ada Lovelace"})
	r.open(team, team)
	r.want(ui.RoleLabel, "Group info")
	r.want(ui.RoleLabel, "Team")
	r.want(ui.RoleLabel, "4 members")
	r.want(ui.RoleLabel, "Description")
	r.want(ui.RoleLabel, "Weekly sync notes and general chatter")
	r.want(ui.RoleLabel, "Members (4)")
	// names first, then numbers, and ourselves last as You
	var order []string
	tr := r.Tree()
	for _, n := range tr.Nodes {
		if n.Role == ui.RoleButton {
			switch name := string(tr.Text(n.Name)); name {
			case "Ada Lovelace", "Grace Hopper", "+44 770 090 0123", "You":
				order = append(order, name)
			}
		}
	}
	if got := strings.Join(order, "|"); got != "Ada Lovelace|Grace Hopper|+44 770 090 0123|You" {
		t.Errorf("members in order %s\n%s", got, r.Dump())
	}
	r.wantNot(ui.RoleButton, "Message") // a group has nobody to message
	r.click(ui.RoleButton, "Ada Lovelace")
	if r.s.Panes.Pane != state.PaneInfo || string(r.s.Panes.Info.At(0)) != ada {
		t.Fatalf("pane %v info %q", r.s.Panes.Pane, r.s.Panes.Info.At(0))
	}
	r.want(ui.RoleLabel, "Contact info")
	r.want(ui.RoleLabel, "+39 333 123 4567")
	if c, _ := r.a.OpenChatView(); string(c.ID()) != team {
		t.Error("a member's panel changed the open chat")
	}
}

// You do not open your own panel.
func TestOurOwnRowOpensNothing(t *testing.T) {
	r := newRig(t, teamChat(nil))
	r.open(team, team)
	r.click(ui.RoleButton, "You")
	if string(r.s.Panes.Info.At(0)) != team {
		t.Error("our own row opened a panel")
	}
}

// IN-010: Leave group asks for confirmation; a chat we cannot leave has no
// button.
func TestLeaveGroupConfirms(t *testing.T) {
	r := newRig(t, teamChat(nil), chatSpec{id: "120363000000000002@g.us", name: "Gone", mut: func(c *abi.Chat) { c.Left = true }})
	r.open(team, team)
	r.click(ui.RoleButton, "Leave group")
	if d := r.s.Dialog; d.Kind != state.DialogConfirmLeaveGroup || d.Chat != r.a.Model.Chats.Find(b(team)) {
		t.Errorf("dialog %+v", d)
	}
	r.open("120363000000000002@g.us", "120363000000000002@g.us")
	r.wantNot(ui.RoleButton, "Leave group")
	r.s.CloseDialog()
	r2 := newRig(t, chatSpec{id: "120@newsletter", name: "News"})
	r2.open("120@newsletter", "120@newsletter")
	r2.want(ui.RoleButton, "Leave channel")
}

// IN-006: an admin renames the group with the pencil.
func TestGroupRename(t *testing.T) {
	r := newRig(t, teamChat(nil))
	r.open(team, team)
	r.click(ui.RoleButton, "Edit group name")
	r.want(ui.RoleTextField, "Group name")
	r.want(ui.RoleButton, "Save name (Enter)")
	r.want(ui.RoleButton, "Cancel (Escape)")
	r.Play(widgettest.Key('A', ui.PrimaryMod()), widgettest.Type("Sprint crew"), widgettest.Key(ui.KeyEnter, 0), widgettest.Idle(3))
	sg := r.sent(abi.CommandTagSetGroupName)
	if len(sg) != 1 || string(sg[0].SetGroupName.Chat) != team || string(sg[0].SetGroupName.Name) != "Sprint crew" {
		t.Fatalf("SetGroupName %+v", sg)
	}
	r.wantNot(ui.RoleTextField, "Group name")
	// the core says it is saving, then the name arrives
	r.f.Emit(ev(abi.EventTagGroupSaving, func(e *abi.Event) { e.GroupSaving.Chat, e.GroupSaving.Saving = b(team), true }))
	r.Frames(3)
	r.want(ui.RoleLabel, "Saving…")
	r.wantNot(ui.RoleButton, "Edit group name") // nothing is offered while a change is on its way
	r.f.Emit(ev(abi.EventTagGroupSaving, func(e *abi.Event) { e.GroupSaving.Chat, e.GroupSaving.Saving = b(team), false }))
	r.Frames(3)
	r.wantNot(ui.RoleLabel, "Saving…")
	r.want(ui.RoleButton, "Edit group name")
}

func TestGroupRenameCancelAndUnchanged(t *testing.T) {
	r := newRig(t, teamChat(nil))
	r.open(team, team)
	r.click(ui.RoleButton, "Edit group name")
	r.click(ui.RoleButton, "Cancel (Escape)")
	r.wantNot(ui.RoleTextField, "Group name")
	// an unchanged name sends nothing
	r.click(ui.RoleButton, "Edit group name")
	r.Play(widgettest.Key(ui.KeyEnter, 0), widgettest.Idle(3))
	// Escape in the field cancels
	r.click(ui.RoleButton, "Edit group name")
	r.Play(widgettest.Key(ui.KeyEscape, 0), widgettest.Idle(3))
	r.wantNot(ui.RoleTextField, "Group name")
	if len(r.sent(abi.CommandTagSetGroupName)) != 0 {
		t.Error("a name that did not change was sent")
	}
	if r.s.Panes.Pane != state.PaneInfo {
		t.Error("Escape in the editor closed the panel")
	}
}

// Where WhatsApp does not allow it, neither the pencil nor the photo menu is
// offered: not an admin and the group's info locked, or not known.
func TestGroupEditingNeedsPermission(t *testing.T) {
	r := newRig(t,
		teamChat(func(c *abi.Chat) { c.Admin, c.HasInfoLocked, c.InfoLocked = false, true, true }),
		chatSpec{id: "120363000000000009@g.us", name: "Open", mut: func(c *abi.Chat) { c.HasInfoLocked = true }},
	)
	r.open(team, team)
	r.wantNot(ui.RoleButton, "Edit group name")
	r.wantNot(ui.RoleButton, "Change group photo")
	r.open("120363000000000009@g.us", "120363000000000009@g.us")
	r.want(ui.RoleButton, "Edit group name") // info open to everyone
}

// IN-007: clicking the group photo, or right-clicking it, opens a native menu
// under it with Change photo and, when it has a photo, Remove photo.
func TestGroupPhotoMenu(t *testing.T) {
	r := newRig(t, teamChat(nil))
	r.Context().SetNativeMenus(true)
	r.open(team, team)
	scr := r.sh.Set.Info.(*info.Screen)

	titles := func(req ui.MenuRequest) []string {
		var out []string
		for _, it := range req.Items {
			out = append(out, it.Title)
		}
		return out
	}
	r.click(ui.RoleButton, "Change group photo")
	req, ok := r.Context().TakeMenuRequest()
	if !ok || req.Kind != ui.MenuItems || strings.Join(titles(req), "|") != "Change photo" {
		t.Fatalf("menu %+v (%v)", req, ok)
	}
	if req.Items[0].ID != info.MenuChangePhoto {
		t.Errorf("item id %d", req.Items[0].ID)
	}
	if n, _ := r.Find(ui.RoleButton, "Change group photo"); n.Bounds[3] > req.Rect[1]+1 || req.Rect[1] < n.Bounds[1] {
		t.Errorf("the menu is not under the picture: %v and %v", n.Bounds, req.Rect)
	}
	scr.PhotoChoice(r.a, req.Items[0].ID)
	if pg := r.sent(abi.CommandTagPickGroupPicture); len(pg) != 1 || string(pg[0].PickGroupPicture.Chat) != team {
		t.Errorf("PickGroupPicture %+v", pg)
	}

	// with a picture, it can be removed too
	r.f.Emit(ev(abi.EventTagAvatar, func(e *abi.Event) {
		e.Avatar.ID, e.Avatar.Full, e.Avatar.Path, e.Avatar.HasPath = b(team), true, b("/tmp/none.jpg"), true
	}))
	r.Frames(3)
	r.click(ui.RoleButton, "Change group photo")
	req, ok = r.Context().TakeMenuRequest()
	if !ok || strings.Join(titles(req), "|") != "Change photo|Remove photo" {
		t.Fatalf("menu with a picture %+v (%v)", req, ok)
	}
	scr.PhotoChoice(r.a, req.Items[1].ID)
	sp := r.sent(abi.CommandTagSetGroupPicture)
	if len(sp) != 1 || string(sp[0].SetGroupPicture.Chat) != team || sp[0].SetGroupPicture.HasJpeg {
		t.Errorf("SetGroupPicture %+v", sp)
	}

	// no photo menu where the group may not be edited
	r.s.Touch()
	r.Frames(2)
	if _, ok := r.Context().TakeMenuRequest(); ok {
		t.Error("a menu opened without a click")
	}
}

// Dragging the panel's edge resizes it (IN-014).
func TestDraggingTheEdgeResizes(t *testing.T) {
	r := newRig(t, direct()...)
	r.open(ada, ada)
	before := r.s.Settings.InfoPaneWidth
	r.Play(widgettest.Drag(ui.RoleSplitter, "Resize", -60, 6), widgettest.Idle(3))
	if got := r.s.Settings.InfoPaneWidth; got <= before+30 {
		t.Errorf("width %v after dragging the edge left from %v", got, before)
	}
	r.Play(widgettest.Drag(ui.RoleSplitter, "Resize", 2000, 6), widgettest.Idle(3))
	if got := r.s.Settings.InfoPaneWidth; got != 240 {
		t.Errorf("width %v after dragging it far right, want the minimum 240", got)
	}
}

// IN-008: the description can be selected and copied, and a long one scrolls
// in a box of its own instead of pushing the media and members away.
func TestDescriptionCopiesAndScrolls(t *testing.T) {
	long := strings.Repeat("Notes for the weekly sync and general chatter. ", 40)
	r := newRig(t, teamChat(func(c *abi.Chat) { c.GroupDescription = b(long) }))
	r.open(team, team)
	var clip []byte
	r.Context().ClipboardSet = func(p []byte) { clip = append(clip[:0], p...) }
	r.Play(widgettest.Click(ui.RoleScrollArea, "Description text"), widgettest.Idle(2),
		widgettest.Edit(ui.EditSelectAll), widgettest.Edit(ui.EditCopy), widgettest.Idle(2))
	if !strings.Contains(string(clip), "Notes for the weekly sync") {
		t.Errorf("copied %q", clip)
	}
	r.want(ui.RoleScrollArea, "Description text")
	box, _ := r.Find(ui.RoleScrollArea, "Description text")
	// the rig's scale is 1: pixels are points
	if h := box.Bounds[3] - box.Bounds[1]; h < 100 || h > 125 {
		t.Errorf("the description box is %v pt high, want 120", h)
	}
}

// A short description is as tall as its text; only a long one is capped.
func TestShortDescriptionIsNotPadded(t *testing.T) {
	r := newRig(t, teamChat(nil))
	r.open(team, team)
	box, ok := r.Find(ui.RoleScrollArea, "Description text")
	if !ok {
		t.Fatalf("no description\n%s", r.Dump())
	}
	if h := box.Bounds[3] - box.Bounds[1]; h <= 0 || h > 40 {
		t.Errorf("a one-line description box is %v pt high", h)
	}
}

// An editable group's name is drawn, centred over the picture, with the
// pencil right after it (it once had no width beside the pencil).
func TestGroupNameSitsBesideItsPencil(t *testing.T) {
	r := newRig(t, teamChat(nil))
	r.open(team, team)
	name, ok := r.Find(ui.RoleLabel, "Team")
	if !ok {
		t.Fatalf("no name\n%s", r.Dump())
	}
	pencil, _ := r.Find(ui.RoleButton, "Edit group name")
	members, _ := r.Find(ui.RoleLabel, "4 members")
	if w := name.Bounds[2] - name.Bounds[0]; w < 20 {
		t.Errorf("the name is %v px wide", w)
	}
	if pencil.Bounds[0] < name.Bounds[2]-1 || pencil.Bounds[0] > name.Bounds[2]+20 {
		t.Errorf("the pencil at %v is not just after the name ending at %v", pencil.Bounds[0], name.Bounds[2])
	}
	mid := func(n [4]float32) float32 { return (n[0] + n[2]) / 2 }
	if d := (name.Bounds[0]+pencil.Bounds[2])/2 - mid(members.Bounds); d > 12 || d < -12 {
		t.Errorf("the name and pencil are %v px off the centre", d)
	}
}

// A chat's boost folds under its row: its face, size and your bubbles'
// colour are the chat's, kept on the account, and a steady frame with them
// open makes nothing.
func TestAChatsBoost(t *testing.T) {
	r := newRig(t, teamChat(nil))
	r.open(team, team)
	if _, ok := r.Find(ui.RoleRadio, "Serif"); ok {
		t.Fatal("the boost's controls show before its row is opened")
	}
	r.click(ui.RoleButton, "How this chat reads")
	r.click(ui.RoleRadio, "Serif")
	r.click(ui.RoleRadio, "125%")
	r.click(ui.RoleButton, "Teal")
	if got := r.a.BoostOf(b(team)); got != (state.ChatBoost{Font: "serif", Size: 1.25, Tint: "teal"}) {
		t.Fatalf("the chat's boost is %+v", got)
	}
	r.Frames(10)
	if n := testing.AllocsPerRun(30, r.Frame); n != 0 {
		t.Errorf("the boost open: %v allocations per frame", n)
	}
	r.click(ui.RoleRadio, "Default")
	r.click(ui.RoleRadio, "100%")
	r.click(ui.RoleButton, "No colour")
	if len(r.a.Settings.Boosts) != 0 {
		t.Errorf("a boost back to none left %v", r.a.Settings.Boosts)
	}
}
