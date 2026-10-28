package wa

import (
	"bytes"
	"errors"
	"image/jpeg"
	"os"
	"testing"

	"go.mau.fi/whatsmeow/proto/waSyncAction"
	"go.mau.fi/whatsmeow/types"
	"go.mau.fi/whatsmeow/types/events"
	"google.golang.org/protobuf/proto"

	"zapfast/abi"
)

// inGroup is a linked harness with a group we administer, its info known.
func inGroup(t *testing.T) *harness {
	t.Helper()
	h := linked(t)
	x := h.f.more()
	x.groups[group] = &types.GroupInfo{
		JID:          group,
		GroupName:    types.GroupName{Name: "Team"},
		GroupTopic:   types.GroupTopic{Topic: "Plans"},
		Participants: []types.GroupParticipant{{JID: me, IsAdmin: true}, {JID: peer}},
	}
	h.receive(group, peer, "G1", t0, text("hi all"))
	h.send(abi.Command{Tag: abi.CommandTagLoadChat, LoadChat: abi.CommandLoadChat{Chat: chatID(group)}})
	evs := h.take()
	c := only(evs, abi.EventTagChatUpdated)
	if len(c) == 0 {
		t.Fatalf("no group info: %s", tags(evs))
	}
	if g := c[len(c)-1].ChatUpdated.Chat; string(g.Name) != "Team" || !g.Admin || string(g.GroupDescription) != "Plans" || len(g.Participants) != 2 {
		t.Fatalf("group info: %+v", g)
	}
	return h
}

func TestGroupName(t *testing.T) {
	h := inGroup(t)
	h.send(abi.Command{Tag: abi.CommandTagSetGroupName, SetGroupName: abi.CommandSetGroupName{Chat: chatID(group), Name: []byte(" Crew ")}})
	evs := h.take()
	expectTags(t, evs, "GroupSaving GroupSaving ChatUpdated")
	if !evs[0].GroupSaving.Saving || evs[1].GroupSaving.Saving || string(evs[2].ChatUpdated.Chat.Name) != "Crew" || h.f.called("SetGroupName:Crew") != 1 {
		t.Fatalf("renamed: %+v", evs)
	}

	// Refused: the rights are asked again, and the name stays.
	h.f.more().nameErr = errForbidden
	asked := h.f.called("GroupInfo")
	h.send(abi.Command{Tag: abi.CommandTagSetGroupName, SetGroupName: abi.CommandSetGroupName{Chat: chatID(group), Name: []byte("Other")}})
	evs = h.take()
	if lastError(t, evs) != groupEditRefused || h.f.called("GroupInfo") != asked+1 {
		t.Fatalf("refused: %s", tags(evs))
	}
	for _, ev := range only(evs, abi.EventTagChatUpdated) {
		if string(ev.ChatUpdated.Chat.Name) == "Other" {
			t.Fatal("a refused name was kept")
		}
	}
}

func TestGroupPicture(t *testing.T) {
	h := inGroup(t)
	var buf bytes.Buffer
	pic := writePNG(t, t.TempDir(), 900, 600, false)
	data, _ := os.ReadFile(pic)
	ref, err := h.c.PutBlob(data)
	if err != nil {
		t.Fatal(err)
	}
	h.send(abi.Command{Tag: abi.CommandTagSetGroupPicture, SetGroupPicture: abi.CommandSetGroupPicture{Chat: chatID(group), Jpeg: ref, HasJpeg: true}})
	evs := h.take()
	expectTags(t, evs, "GroupSaving GroupSaving Avatar Avatar")
	if a := evs[2].Avatar; string(a.ID) != group.String() || !a.HasPath {
		t.Fatalf("avatar: %+v", a)
	}
	if !bytes.Equal(h.f.more().photos[group], data) {
		t.Fatal("the picture was not set")
	}

	// Picked: squared to a JPEG first.
	h.send(abi.Command{Tag: abi.CommandTagPickGroupPicture, PickGroupPicture: abi.CommandPickGroupPicture{Chat: chatID(group)}})
	if req := h.pick(pic); req.Kind != abi.PickKindFile || string(req.Title) != "Choose a group photo" {
		t.Fatalf("dialog: %+v", req)
	}
	expectTags(t, h.take(), "GroupSaving GroupSaving Avatar Avatar")
	buf.Write(h.f.more().photos[group])
	if cfg, err := jpeg.DecodeConfig(&buf); err != nil || cfg.Width != 600 || cfg.Height != 600 {
		t.Fatalf("picked: %+v %v", cfg, err)
	}

	// Removed.
	h.send(abi.Command{Tag: abi.CommandTagSetGroupPicture, SetGroupPicture: abi.CommandSetGroupPicture{Chat: chatID(group)}})
	evs = h.take()
	if a := only(evs, abi.EventTagAvatar); len(a) != 2 || a[0].Avatar.HasPath || h.f.more().photos[group] != nil {
		t.Fatalf("removed: %s", tags(evs))
	}
}

func TestLeaveGroup(t *testing.T) {
	h := inGroup(t)
	h.f.more().leaveErr = errors.New("no")
	h.send(abi.Command{Tag: abi.CommandTagLeaveGroup, LeaveGroup: abi.CommandLeaveGroup{Chat: chatID(group), Archive: true}})
	evs := h.take()
	if lastError(t, evs) != "Could not leave the group." || one(t, evs, abi.EventTagChatUpdated).ChatUpdated.Chat.Left {
		t.Fatalf("refused: %s", tags(evs))
	}

	h.f.more().leaveErr = nil
	patches := len(h.f.patches)
	h.send(abi.Command{Tag: abi.CommandTagLeaveGroup, LeaveGroup: abi.CommandLeaveGroup{Chat: chatID(group), Archive: true}})
	c := one(t, h.take(), abi.EventTagChatUpdated).ChatUpdated.Chat
	if !c.Left || !c.Archived || c.Admin || len(c.Participants) != 1 {
		t.Fatalf("left: %+v", c)
	}
	if len(h.f.patches) != patches+1 || h.f.patches[patches].Mutations[0].Index[0] != "archive" {
		t.Fatal("the phone was not told to archive it")
	}
}

func TestGroupChangesFromWhatsApp(t *testing.T) {
	h := inGroup(t)
	h.inject(&events.GroupInfo{JID: group, Name: &types.GroupName{Name: "Renamed"}, Leave: []types.JID{peer}})
	c := one(t, h.take(), abi.EventTagChatUpdated).ChatUpdated.Chat
	if string(c.Name) != "Renamed" || len(c.Participants) != 1 {
		t.Fatalf("changed: %+v", c)
	}
}

func TestInvites(t *testing.T) {
	h := linked(t)
	x := h.f.more()
	x.invite = &types.GroupInfo{JID: group, GroupName: types.GroupName{Name: "Club"}, GroupTopic: types.GroupTopic{Topic: "Welcome"}, ParticipantCount: 12}
	h.send(abi.Command{Tag: abi.CommandTagPreviewInvite, PreviewInvite: abi.CommandPreviewInvite{Code: []byte("https://chat.whatsapp.com/AbC123")}})
	p := one(t, h.take(), abi.EventTagInvitePreview).InvitePreview
	if info := p.Result.Ok.Info; p.Result.Tag != abi.InviteResultTagOk || string(info.Subject) != "Club" || info.Members != 12 || string(info.Description) != "Welcome" || h.f.called("GroupInfoFromLink:AbC123") != 1 {
		t.Fatalf("preview: %+v", p)
	}

	x.inviteErr = errors.New("server returned error 410")
	h.send(abi.Command{Tag: abi.CommandTagPreviewInvite, PreviewInvite: abi.CommandPreviewInvite{Code: []byte("AbC123")}})
	if p := one(t, h.take(), abi.EventTagInvitePreview).InvitePreview; p.Result.Tag != abi.InviteResultTagErr || string(p.Result.Err.Error) != "This invite link has expired." {
		t.Fatalf("expired: %+v", p)
	}

	x.joined, x.groups[group] = group, &types.GroupInfo{JID: group, GroupName: types.GroupName{Name: "Club"}, Participants: []types.GroupParticipant{{JID: me}}}
	h.send(abi.Command{Tag: abi.CommandTagJoinInvite, JoinInvite: abi.CommandJoinInvite{Code: []byte("AbC123")}})
	evs := h.take()
	j := one(t, evs, abi.EventTagInviteJoined).InviteJoined
	if j.Result.Tag != abi.JoinResultTagOk || string(j.Result.Ok.Chat) != group.String() || j.Result.Ok.Pending {
		t.Fatalf("joined: %+v", j)
	}
	if c := only(evs, abi.EventTagChatUpdated); len(c) == 0 || string(c[len(c)-1].ChatUpdated.Chat.Name) != "Club" {
		t.Fatalf("the joined group: %s", tags(evs))
	}

	h.f.setConnected(false)
	h.send(abi.Command{Tag: abi.CommandTagJoinInvite, JoinInvite: abi.CommandJoinInvite{Code: []byte("AbC123")}})
	if j := one(t, h.take(), abi.EventTagInviteJoined).InviteJoined; j.Result.Tag != abi.JoinResultTagErr {
		t.Fatalf("offline: %+v", j)
	}
}

func TestContacts(t *testing.T) {
	h := linked(t)
	x := h.f.more()

	// The phone's address book changes.
	x.contacts[peer2] = types.ContactInfo{Found: true, FullName: "Grace Hopper", FirstName: "Grace", PushName: "gh"}
	h.inject(&events.Contact{JID: peer2, Action: &waSyncAction.ContactAction{FullName: proto.String("Grace Hopper")}})
	c := one(t, h.take(), abi.EventTagContacts).Contacts.Contacts
	if len(c) != 1 || string(c[0].ID) != peer2.String() || string(c[0].FullName) != "Grace Hopper" || string(c[0].FirstName) != "Grace" || string(c[0].PushName) != "gh" {
		t.Fatalf("contact: %+v", c)
	}

	h.send(abi.Command{Tag: abi.CommandTagSaveContact, SaveContact: abi.CommandSaveContact{ID: chatID(peer), FullName: []byte("Ada Lovelace"), FirstName: []byte("Ada"), HasFirstName: true, ToPhone: true}})
	evs := h.take()
	if c := one(t, evs, abi.EventTagContacts).Contacts.Contacts; len(c) != 1 || string(c[0].FullName) != "Ada Lovelace" {
		t.Fatalf("saved: %+v", c)
	}
	if string(one(t, evs, abi.EventTagInfo).Info.Text) != "Added Ada Lovelace to contacts" {
		t.Fatalf("saved: %s", tags(evs))
	}
	if p := h.f.patches[len(h.f.patches)-1].Mutations[0]; p.Index[0] != "contact" || p.Value.GetContactAction().GetFullName() != "Ada Lovelace" || !p.Value.GetContactAction().GetSaveOnPrimaryAddressbook() {
		t.Fatalf("patch: %+v", p)
	}

	// A new number: checked, then saved.
	x.onWhatsApp = []types.IsOnWhatsAppResponse{{Query: "+4917600000003", JID: types.NewJID("4917600000003", types.DefaultUserServer), IsIn: true}}
	h.send(abi.Command{Tag: abi.CommandTagNewContact, NewContact: abi.CommandNewContact{Request: 7, Phone: []byte("+49 176 00000003"), FullName: []byte("Lin"), HasFullName: true}})
	evs = h.take()
	r := one(t, evs, abi.EventTagContactReady).ContactReady
	if r.Request != 7 || string(r.ID) != "4917600000003@s.whatsapp.net" || string(r.Name) != "Lin" || h.f.called("IsOnWhatsApp:+4917600000003") != 1 {
		t.Fatalf("ready: %+v", r)
	}

	x.onWhatsApp = []types.IsOnWhatsAppResponse{{Query: "+4917600000004", IsIn: false}}
	h.send(abi.Command{Tag: abi.CommandTagNewContact, NewContact: abi.CommandNewContact{Request: 8, Phone: []byte("4917600000004")}})
	if f := one(t, h.take(), abi.EventTagContactFailed).ContactFailed; f.Request != 8 || string(f.Error) != "This number is not on WhatsApp" {
		t.Fatalf("failed: %+v", f)
	}
	h.send(abi.Command{Tag: abi.CommandTagNewContact, NewContact: abi.CommandNewContact{Request: 9, Phone: []byte("12ab")}})
	if f := one(t, h.take(), abi.EventTagContactFailed).ContactFailed; f.Request != 9 || string(f.Error) != "Enter a valid international phone number" {
		t.Fatalf("invalid: %+v", f)
	}
}
