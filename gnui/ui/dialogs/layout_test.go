package dialogs

import (
	"fmt"
	"image/png"
	"os"
	"strconv"
	"testing"

	"gonativeui/ui"

	"zapfast/abi"
	"zapfast/i18n"
	"zapfast/state"
)

// Every dialog in the languages with the longest words, at the default window
// and at the narrowest one: no control runs past its dialog, no label is
// clipped, nothing overlaps (the framework's layout audit).
func TestEveryDialogFitsInTheLongestLanguages(t *testing.T) {
	langs := []i18n.Locale{i18n.English, i18n.German, i18n.Russian, i18n.French}
	sizes := []struct{ w, h int }{{760, 900}, {480, 900}}
	interactive := func() abi.Message {
		list := abi.InteractiveButton{Label: []byte("Menu"), Action: abi.InteractiveAction{Tag: abi.InteractiveActionTagSelect}}
		list.Action.Select.Options = []abi.InteractiveOption{
			{Section: []byte("Starters"), Title: []byte("Soup"), Description: []byte("Hot")},
			{Section: []byte("Mains"), Title: []byte("Pasta")},
		}
		m := abi.Message{ID: []byte("i1"), Chat: []byte("1@s.whatsapp.net"), Sender: []byte("1@s.whatsapp.net"), Timestamp: 10, Content: abi.Content{Tag: abi.ContentTagInteractive}}
		m.Content.Interactive = abi.ContentInteractive{Text: []byte("Pick"), HasCard: true, Card: abi.InteractiveCard{Buttons: []abi.InteractiveButton{list}}}
		return m
	}
	messages := ev(abi.EventTagMessages, func(e *abi.Event) {
		e.Messages = abi.EventMessages{Chat: []byte("1@s.whatsapp.net"), Batch: 1, Last: true, Messages: []abi.Message{
			pollMessage(abi.PollVoter{ID: []byte("1@s.whatsapp.net"), Name: []byte("Ada"), Timestamp: 5, Choices: []uint64{0}}),
			interactive(),
		}}
	})
	preview := func(approval bool, id string) *abi.Event {
		return ev(abi.EventTagInvitePreview, func(e *abi.Event) {
			e.InvitePreview = abi.EventInvitePreview{Code: []byte("AbC"), Result: abi.InviteResult{Tag: abi.InviteResultTagOk,
				Ok: abi.InviteResultOk{Info: abi.InviteInfo{ID: []byte(id), Subject: []byte("Go devs"), Description: []byte("Where Go people talk about Go."), HasDescription: true, Members: 12, Approval: approval}}}}
		})
	}
	scenes := []struct {
		name  string
		setup func(r *rig)
	}{
		{"new contact", func(r *rig) { r.open(state.Dialog{Kind: state.DialogNewContact}) }},
		{"new contact, named", func(r *rig) {
			r.open(state.Dialog{Kind: state.DialogNewContact})
			r.typeInto(i18n.T("Phone number"), "+442079460958")
			r.typeInto(i18n.T("First name"), "Ada")
		}},
		{"new contact, bad number", func(r *rig) {
			r.open(state.Dialog{Kind: state.DialogNewContact})
			r.typeInto(i18n.T("Phone number"), "555-ABC")
		}},
		{"new contact, checking", func(r *rig) {
			r.open(state.Dialog{Kind: state.DialogNewContact})
			r.typeInto(i18n.T("Phone number"), "+442079460958")
			r.click(ui.RoleButton, i18n.T("Message"))
		}},
		{"new chat", func(r *rig) { r.open(state.Dialog{Kind: state.DialogNewChat}) }},
		{"pair with a phone", func(r *rig) { r.open(state.Dialog{Kind: state.DialogPairWithPhone}) }},
		{"set up locked chats", func(r *rig) { r.open(state.Dialog{Kind: state.DialogUnlockLockedChats}) }},
		{"forward", func(r *rig) { r.openForward(r.ref("1@s.whatsapp.net"), oneID("m1")) }},
		{"forward, review", func(r *rig) {
			r.openForward(r.ref("1@s.whatsapp.net"), oneID("m1"))
			r.click(ui.RoleCheckbox, "Grace Hopper")
			r.click(ui.RoleCheckbox, "Analytical Engine")
			r.click(ui.RoleButton, i18n.T("Review recipients"))
		}},
		{"create poll", func(r *rig) { r.open(state.Dialog{Kind: state.DialogCreatePoll, Chat: r.ref("1@s.whatsapp.net")}) }},
		{"poll results", func(r *rig) {
			ada := r.ref("1@s.whatsapp.net")
			r.a.OpenChat(ada)
			r.Frames(2)
			r.open(state.Dialog{Kind: state.DialogPollResults, Chat: ada, IDs: *oneID("p1")})
		}},
		{"location", func(r *rig) { r.open(state.Dialog{Kind: state.DialogSendLocation, Chat: r.ref("1@s.whatsapp.net")}) }},
		{"location, short link", func(r *rig) {
			r.open(state.Dialog{Kind: state.DialogSendLocation, Chat: r.ref("1@s.whatsapp.net")})
			r.typeInto(i18n.T("Paste a map link or coordinates"), "https://maps.app.goo.gl/abc")
		}},
		{"location, a spot", func(r *rig) {
			r.open(state.Dialog{Kind: state.DialogSendLocation, Chat: r.ref("1@s.whatsapp.net"), Text: []byte("40.71280, -74.00600")})
		}},
		{"labels", func(r *rig) { r.open(state.Dialog{Kind: state.DialogLabels}) }},
		{"invite, looking up", func(r *rig) { r.open(state.Dialog{Kind: state.DialogJoinGroup, Text: []byte("AbC")}) }},
		{"invite, ready", func(r *rig) {
			r.open(state.Dialog{Kind: state.DialogJoinGroup, Text: []byte("AbC")})
			r.f.Emit(preview(false, "777@g.us"))
			r.Frames(3)
		}},
		{"invite, approval", func(r *rig) {
			r.open(state.Dialog{Kind: state.DialogJoinGroup, Text: []byte("AbC")})
			r.f.Emit(preview(true, "777@g.us"))
			r.Frames(3)
		}},
		{"sticker pack", func(r *rig) {
			r.open(state.Dialog{Kind: state.DialogStickerPack, Chat: r.ref("1@s.whatsapp.net"), IDs: *oneID("m1")})
		}},
		{"interactive list", func(r *rig) {
			ada := r.ref("1@s.whatsapp.net")
			r.a.OpenChat(ada)
			r.Frames(2)
			r.open(state.Dialog{Kind: state.DialogInteractiveList, Chat: ada, IDs: *oneID("i1")})
		}},
		{"about", func(r *rig) { r.open(state.Dialog{Kind: state.DialogAbout}) }},
		{"shortcuts", func(r *rig) { r.open(state.Dialog{Kind: state.DialogShortcuts}) }},
	}
	for _, q := range questions(nativeProbe(t)) {
		scenes = append(scenes, struct {
			name  string
			setup func(r *rig)
		}{"question: " + q.name, func(r *rig) {
			r.a.OpenChat(r.ref("1@s.whatsapp.net"))
			q := questionOf(r, q.name)
			r.open(q.d)
		}})
	}
	for _, lang := range langs {
		for _, sz := range sizes {
			for _, sc := range scenes {
				name := lang.Tag() + "/" + sc.name + "/" + strconv.Itoa(sz.w)
				t.Run(name, func(t *testing.T) {
					old := i18n.Current()
					i18n.SetLocale(lang)
					t.Cleanup(func() { i18n.SetLocale(old) })
					r := newRigSize(t, sz.w, sz.h, append(baseEvents(), messages)...)
					sc.setup(r)
					r.Frames(3)
					r.AuditLayout(name)
				})
			}
		}
	}
}

// nativeProbe is a rig only to name the questions (they carry chat refs).
func nativeProbe(t *testing.T) *rig { return newRig(t, baseEvents()...) }

func questionOf(r *rig, name string) question {
	for _, q := range questions(r) {
		if q.name == name {
			return q
		}
	}
	panic("no question " + name)
}

// TestDialogShots writes the New contact dialog in each language to the
// directory in ZF_SHOTS (skipped without it), to look at.
func TestDialogShots(t *testing.T) {
	dir := os.Getenv("ZF_SHOTS")
	if dir == "" {
		t.Skip("ZF_SHOTS not set")
	}
	for _, lang := range []i18n.Locale{i18n.German, i18n.Russian, i18n.French} {
		for _, w := range []int{760, 480} {
			i18n.SetLocale(lang)
			r := newRigSize(t, w, 560, baseEvents()...)
			r.open(state.Dialog{Kind: state.DialogNewContact})
			r.typeInto(i18n.T("Phone number"), "+442079460958")
			r.typeInto(i18n.T("First name"), "Ada")
			r.Frames(3)
			f, err := os.Create(fmt.Sprintf("%s/new-contact-%s-%d.png", dir, lang.Tag(), w))
			if err != nil {
				t.Fatal(err)
			}
			if err := png.Encode(f, r.Image()); err != nil {
				t.Fatal(err)
			}
			f.Close()
			i18n.SetLocale(i18n.English)
		}
	}
}
