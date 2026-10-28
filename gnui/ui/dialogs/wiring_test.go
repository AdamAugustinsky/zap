package dialogs

import (
	"testing"
	"time"

	"gonativeui/ui"
	"gonativeui/widget/widgettest"

	"zapfast/abi"
	"zapfast/model"
	"zapfast/state"
)

func (r *rig) shell() *fakeShell { return r.s.Shell.(*fakeShell) }

// waitOpened waits for the opener the dialog ran off the main thread.
func (r *rig) waitOpened(n int) []string {
	r.t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for len(r.shell().paths()) < n && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	return r.shell().paths()
}

// DL-016: About's links go to the shell's opener, never a real one.
func TestAboutLinksOpenThroughTheShell(t *testing.T) {
	r := newRig(t, baseEvents()...)
	r.open(state.Dialog{Kind: state.DialogAbout})
	r.click(ui.RoleLink, "Source code")
	if got := r.waitOpened(1); len(got) != 1 || got[0] != SourceURL {
		t.Fatalf("opened %v", got)
	}
}

// DL-013: a pasted spot opens in a map through the shell.
func TestMapLinkOpensThroughTheShell(t *testing.T) {
	r := newRig(t, baseEvents()...)
	r.open(state.Dialog{Kind: state.DialogSendLocation, Chat: r.ref("1@s.whatsapp.net")})
	r.typeInto("Paste a map link or coordinates", "48.8584, 2.2945")
	r.click(ui.RoleLink, "Open in a map")
	sp, _ := ParseSpot("48.8584, 2.2945")
	if got := r.waitOpened(1); len(got) != 1 || got[0] != MapURL(sp) {
		t.Fatalf("opened %v", got)
	}
}

// DL-016: About names the build's version.
func TestAboutShowsTheStateVersion(t *testing.T) {
	r := newRig(t, baseEvents()...)
	r.open(state.Dialog{Kind: state.DialogAbout})
	if !r.has(ui.RoleLabel, "Version "+state.Version) {
		t.Fatalf("no version\n%s", widgettest.Dump(r.Tree()))
	}
}

// DL-008: a number the core has looked up opens its chat, asking the core
// to make it first when the account has none.
func TestContactReadyStartsTheChat(t *testing.T) {
	r := newRig(t, baseEvents()...)
	r.open(state.Dialog{Kind: state.DialogNewContact})
	r.typeInto("Phone number", "+442079460958")
	r.click(ui.RoleButton, "Message")
	nc := r.sent(abi.CommandTagNewContact)
	if len(nc) != 1 {
		t.Fatalf("sent %v", r.f.SentTags())
	}
	r.f.Emit(ev(abi.EventTagContactReady, func(e *abi.Event) {
		e.ContactReady = abi.EventContactReady{Request: nc[0].NewContact.Request, ID: []byte("442079460958@s.whatsapp.net")}
	}))
	r.Frames(3)
	if r.s.Dialog.Open() {
		t.Error("the dialog stays open after the number is ready")
	}
	got := r.sent(abi.CommandTagEnsureChat)
	if len(got) != 1 || string(got[0].EnsureChat.Chat) != "442079460958@s.whatsapp.net" {
		t.Fatalf("sent %v", r.f.SentTags())
	}
	// The chat opens once the core has made it.
	r.f.Emit(chatsEvent(chat("442079460958@s.whatsapp.net", "442079460958", 30)))
	r.Frames(3)
	if ref := r.a.Model.Chats.Find([]byte("442079460958@s.whatsapp.net")); ref == 0 || r.a.Open != ref {
		t.Errorf("open chat %v", r.a.Open)
	}
}

// DL-014: the invite code arrives in Dialog.Text.
func TestJoinGroupLooksTheCodeUp(t *testing.T) {
	r := newRig(t, baseEvents()...)
	r.open(state.Dialog{Kind: state.DialogJoinGroup, Text: []byte("AbC123")})
	got := r.sent(abi.CommandTagPreviewInvite)
	if len(got) != 1 || string(got[0].PreviewInvite.Code) != "AbC123" {
		t.Fatalf("sent %v", r.f.SentTags())
	}
}

func carousel(id string, cards ...abi.CarouselCard) abi.Message {
	m := abi.Message{ID: []byte(id), Chat: []byte("1@s.whatsapp.net"), Sender: []byte("1@s.whatsapp.net"), Timestamp: 10,
		Content: abi.Content{Tag: abi.ContentTagInteractive}}
	m.Content.Interactive = abi.ContentInteractive{Text: []byte("Pick"), HasCard: true, Card: abi.InteractiveCard{Carousel: cards}}
	return m
}

func listCard(label string, options ...string) abi.CarouselCard {
	b := abi.InteractiveButton{Label: []byte(label), Action: abi.InteractiveAction{Tag: abi.InteractiveActionTagSelect}}
	for _, o := range options {
		b.Action.Select.Options = append(b.Action.Select.Options, abi.InteractiveOption{Title: []byte(o)})
	}
	return abi.CarouselCard{Buttons: []abi.InteractiveButton{b}}
}

// A carousel card's list answers for the card: its id is "<message>-card-<i>".
func TestCarouselCardListReplies(t *testing.T) {
	r := newRig(t, append(baseEvents(), ev(abi.EventTagMessages, func(e *abi.Event) {
		e.Messages = abi.EventMessages{Chat: []byte("1@s.whatsapp.net"), Batch: 1, Last: true, Messages: []abi.Message{
			carousel("c", listCard("First list", "A1", "A2"), listCard("Second list", "B1", "B2", "B3")),
		}}
	}))...)
	ada := r.ref("1@s.whatsapp.net")
	r.a.OpenChat(ada)
	r.Frames(2)
	var ids model.IDList
	ids.Add([]byte("c-card-1"))
	r.open(state.Dialog{Kind: state.DialogInteractiveList, Chat: ada, IDs: ids, N: 0})
	if !r.has(ui.RoleDialog, "Second list") || !r.has(ui.RoleButton, "B2") || r.has(ui.RoleButton, "A1") {
		t.Fatalf("not the second card's list\n%s", widgettest.Dump(r.Tree()))
	}
	r.click(ui.RoleButton, "B2")
	got := r.sent(abi.CommandTagReplyInteractive)
	if len(got) != 1 {
		t.Fatalf("sent %v", r.f.SentTags())
	}
	ri := got[0].ReplyInteractive
	if string(ri.Message) != "c-card-1" || ri.Button != 0 || !ri.HasChoice || ri.Choice != 1 {
		t.Errorf("reply %+v", ri)
	}
}

func TestSplitCard(t *testing.T) {
	for _, c := range []struct {
		id     string
		parent string
		card   int
		ok     bool
	}{
		{"abc-card-0", "abc", 0, true},
		{"3EB0-card-12", "3EB0", 12, true},
		{"abc", "", -1, false},
		{"abc-card-", "", -1, false},
		{"abc-card-x", "", -1, false},
		{"-card-1", "", 1, true},
	} {
		p, n, ok := splitCard([]byte(c.id))
		if ok != c.ok || (ok && (string(p) != c.parent || n != c.card)) {
			t.Errorf("splitCard(%q) = %q, %d, %v", c.id, p, n, ok)
		}
	}
}
