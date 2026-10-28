package kinds_test

import (
	"os"
	"strings"
	"testing"
	"time"

	"gonativeui/ui"
	"gonativeui/widget/widgettest"

	"zapfast/abi"
	"zapfast/state"
	"zapfast/ui/conversation/kinds"
)

func document(id string, path string, st abi.MediaStateTag) abi.Message {
	m := msg(id, 10, false, abi.Content{Tag: abi.ContentTagDocument})
	d := &m.Content.Document
	d.FileName, d.Pages, d.HasPages = []byte("Report.pdf"), 12, true
	d.Media.Mime, d.Media.Size = []byte("application/pdf"), 1_234_567
	if path != "" {
		d.Media.Path, d.Media.HasPath = []byte(path), true
	}
	d.Media.State.Tag = st
	return m
}

func TestDocumentOpensInItsApplicationOrDownloads(t *testing.T) {
	dir := t.TempDir()
	path := dir + "/Report.pdf"
	os.WriteFile(path, []byte("%PDF-1.4"), 0o644)
	opened := make(chan string, 1)
	defer kinds.SetOpeners(func(s string) error { opened <- s; return nil }, nil, nil)()
	r := newRig(t, document("have", path, abi.MediaStateTagIdle), document("miss", "", abi.MediaStateTagIdle))
	if s := r.p.sized["have"]; s[0] != 320 || s[1] != 68 {
		t.Errorf("document size %v", s)
	}
	r.Play(widgettest.Click(ui.RoleButton, "Document").Nth(0))
	if got := <-opened; got != path {
		t.Errorf("opened %q", got)
	}
	r.Play(widgettest.Click(ui.RoleButton, "Document").Nth(1))
	r.Frames(2)
	got := commands(r, abi.CommandTagDownload)
	if len(got) != 1 || !got[0].Download.Manual || string(got[0].Download.Message) != "miss" {
		t.Errorf("downloads %+v", got)
	}
}

// A document of a kind that could run code is shown in its folder instead,
// with a note, never opened.
func TestDocumentOfAnUnknownKindIsNotOpened(t *testing.T) {
	dir := t.TempDir()
	path := dir + "/run.command"
	os.WriteFile(path, []byte("#!/bin/sh"), 0o644)
	revealed := make(chan []string, 1)
	defer kinds.SetOpeners(func(s string) error { t.Errorf("opened %q", s); return nil }, nil,
		func(p []string) error { revealed <- p; return nil })()
	r := newRig(t, document("x", path, abi.MediaStateTagIdle))
	r.Play(widgettest.Click(ui.RoleButton, "Document"))
	if got := <-revealed; len(got) != 1 || got[0] != path {
		t.Errorf("revealed %v", got)
	}
	if n := len(r.s.Toasts); n == 0 || !strings.Contains(string(r.s.Toasts[n-1].Text), "open this file yourself") {
		t.Errorf("toasts %v", r.s.Toasts)
	}
}

// Without a test's fake, a file that is not to be opened is shown in its
// folder through the state's Shell (core.Revealer), never by kinds itself.
func TestDocumentIsRevealedThroughTheStatesShell(t *testing.T) {
	dir := t.TempDir()
	path := dir + "/run.command"
	os.WriteFile(path, []byte("#!/bin/sh"), 0o644)
	sh := &revealShell{t: t, revealed: make(chan []string, 1)}
	r := newRigWith(t, func(s *state.State) { s.Shell = sh }, document("x", path, abi.MediaStateTagIdle))
	r.Play(widgettest.Click(ui.RoleButton, "Document"))
	select {
	case got := <-sh.revealed:
		if len(got) != 1 || got[0] != path {
			t.Errorf("revealed %v", got)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("nothing was shown through the state's Shell")
	}
}

// revealShell is a state Shell that shows files in their folder; it opens
// nothing.
type revealShell struct {
	t        *testing.T
	revealed chan []string
}

func (r *revealShell) Open(p string) error {
	r.t.Errorf("opened %q", p)
	return nil
}

func (r *revealShell) Reveal(p []string) error {
	r.revealed <- p
	return nil
}

func TestLocationCardOpensAMap(t *testing.T) {
	loc := msg("l", 10, false, abi.Content{Tag: abi.ContentTagLocation})
	loc.Content.Location = abi.ContentLocation{Latitude: -23.5505, Longitude: -46.6333}
	named := msg("n", 11, false, abi.Content{Tag: abi.ContentTagLocation})
	named.Content.Location = abi.ContentLocation{Latitude: 1.5, Longitude: 2, Name: []byte("Café Central"), HasName: true, Address: []byte("Rua A, 12"), HasAddress: true}
	urls := make(chan string, 2)
	defer kinds.SetOpeners(nil, func(u string) error { urls <- u; return nil }, nil)()
	r := newRig(t, loc, named)
	if s := r.p.sized["l"]; s[0] != 300 {
		t.Errorf("location card %v", s)
	}
	r.Play(widgettest.Click(ui.RoleLink, "Open in a map").Nth(0))
	if got := <-urls; got != "https://www.openstreetmap.org/?mlat=-23.5505&mlon=-46.6333#map=16/-23.5505/-46.6333" {
		t.Errorf("map url %q", got)
	}
	for _, name := range []string{"Café Central", "Rua A, 12", "Location", "-23.55050, -46.63330"} {
		if !hasText(r, name) {
			t.Errorf("no %q on screen", name)
		}
	}
}

// hasText reports whether a label with the text is on screen.
func hasText(r *rig, s string) bool { return r.count(ui.RoleLabel, s) > 0 }

func TestLiveLocationSaysWhenItWasUpdatedAndWhenItEnded(t *testing.T) {
	now := int64(1_800_000_000)
	live := msg("live", now-60, false, abi.Content{Tag: abi.ContentTagLiveLocation})
	live.Content.LiveLocation = abi.ContentLiveLocation{Latitude: 1, Longitude: 2, Updated: now - 30, NewerOnPhone: true, SpeedMPS: 5, HasSpeedMPS: true, AccuracyM: 12, HasAccuracyM: true}
	ended := msg("ended", now-60, false, abi.Content{Tag: abi.ContentTagLiveLocation})
	ended.Content.LiveLocation = abi.ContentLiveLocation{Latitude: 1, Longitude: 2, Ended: true}
	old := msg("old", now-9*3600, false, abi.Content{Tag: abi.ContentTagLiveLocation})
	f := newRigAt(t, now, live, ended, old)
	if !hasTextPrefix(f, "Updated ") {
		t.Error("no Updated line")
	}
	if !hasText(f, "Open WhatsApp on your phone to follow it.") {
		t.Error("no phone note")
	}
	if n := countText(f, "Live location ended"); n != 2 {
		t.Errorf("%d ended shares, want 2 (ended and out of time)", n)
	}
	if !hasTextPrefix(f, "Live location") {
		t.Error("no live title")
	}
}

func hasTextPrefix(r *rig, prefix string) bool {
	for _, n := range r.names(ui.RoleLabel) {
		if strings.HasPrefix(n, prefix) {
			return true
		}
	}
	return false
}

func countText(r *rig, s string) int { return r.count(ui.RoleLabel, s) }

const vcard = "BEGIN:VCARD\r\nVERSION:3.0\r\nN:Lovelace;Ada Augusta;;;\r\nFN:Ada Augusta Lovelace\r\nTEL;type=CELL;waid=5511999990001:+55 11 99999-0001\r\nEND:VCARD"

func contact(id, card, name string) abi.Message {
	m := msg(id, 10, false, abi.Content{Tag: abi.ContentTagContact})
	m.Content.Contact = abi.ContentContact{DisplayName: []byte(name), Vcard: []byte(card)}
	return m
}

func TestSharedContactStartsAChatOrIsAdded(t *testing.T) {
	r := newRig(t, contact("c", vcard, "Ada"), contact("local", "BEGIN:VCARD\r\nFN:Bob\r\nTEL:011 99999-0002\r\nEND:VCARD", "Bob"))
	if !hasText(r, "Ada Augusta Lovelace") {
		t.Error("the card's name is not shown")
	}
	if !hasText(r, "011 99999-0002") || !hasText(r, "Bob") {
		t.Error("a local number shows as it is written")
	}
	if n := r.count(ui.RoleButton, "Chat"); n != 1 {
		t.Errorf("%d Chat buttons, want 1 (a local number names no account)", n)
	}
	r.Play(widgettest.Click(ui.RoleButton, "Add"), widgettest.Idle(2))
	got := commands(r, abi.CommandTagNewContact)
	if len(got) != 1 {
		t.Fatalf("new contact commands %+v", got)
	}
	nc := got[0].NewContact
	if string(nc.Phone) != "5511999990001" || string(nc.FullName) != "Ada Augusta Lovelace" || string(nc.FirstName) != "Ada Augusta" {
		t.Errorf("%q %q %q", nc.Phone, nc.FullName, nc.FirstName)
	}
	r.Play(widgettest.Click(ui.RoleButton, "Chat"), widgettest.Idle(2))
	ens := commands(r, abi.CommandTagEnsureChat)
	if len(ens) != 1 || string(ens[0].EnsureChat.Chat) != "5511999990001@s.whatsapp.net" {
		t.Errorf("ensure chat %+v", ens)
	}
}

func TestStickerPackViewsItsStickers(t *testing.T) {
	m := msg("pack", 10, false, abi.Content{Tag: abi.ContentTagStickerPack})
	m.Content.StickerPack = abi.ContentStickerPack{Name: []byte("Cats"), Publisher: []byte("Ada"), Count: 3}
	r := newRig(t, m)
	if !hasText(r, "Cats") || !hasText(r, "Ada · 3 stickers") {
		t.Error("the pack's name and size are not shown")
	}
	r.Play(widgettest.Click(ui.RoleLink, "View stickers"), widgettest.Idle(2))
	got := commands(r, abi.CommandTagViewStickerPack)
	if len(got) != 1 || string(got[0].ViewStickerPack.Message) != "pack" {
		t.Errorf("commands %+v", got)
	}
	if r.s.Dialog.Kind != state.DialogStickerPack {
		t.Errorf("dialog %v", r.s.Dialog.Kind)
	}
}

func TestLinkPreviewOpensItsLink(t *testing.T) {
	m := msg("pre", 10, false, abi.Content{Tag: abi.ContentTagText})
	m.Content.Text = abi.ContentText{Text: []byte("look https://example.com/a"), HasPreview: true,
		Preview: abi.LinkPreview{URL: []byte("https://example.com/a"), Title: []byte("An example"), HasTitle: true, Description: []byte("About it"), HasDescription: true}}
	plain := msg("plain", 11, false, abi.Content{Tag: abi.ContentTagText})
	plain.Content.Text.Text = []byte("just text")
	urls := make(chan string, 1)
	defer kinds.SetOpeners(nil, func(u string) error { urls <- u; return nil }, nil)()
	r := newRig(t, m, plain)
	if kinds.Has(r.view("plain")) || !kinds.Has(r.view("pre")) {
		t.Fatal("only a text with a preview has a block")
	}
	if !hasText(r, "An example") || !hasText(r, "About it") || !hasText(r, "example.com") {
		t.Error("the preview's title, description or domain is not shown")
	}
	r.Play(widgettest.Click(ui.RoleLink, "An example"))
	if got := <-urls; got != "https://example.com/a" {
		t.Errorf("opened %q", got)
	}
}

func TestLinksTheAppCannotOpenSayTheyCannot(t *testing.T) {
	m := msg("pre", 10, false, abi.Content{Tag: abi.ContentTagText})
	m.Content.Text = abi.ContentText{Text: []byte("x"), HasPreview: true, Preview: abi.LinkPreview{URL: []byte("javascript:alert(1)"), Title: []byte("Odd"), HasTitle: true}}
	defer kinds.SetOpeners(nil, func(u string) error { t.Errorf("opened %q", u); return nil }, nil)()
	r := newRig(t, m)
	r.Play(widgettest.Click(ui.RoleLink, "Odd"), widgettest.Idle(2))
	if n := len(r.s.Toasts); n == 0 || string(r.s.Toasts[n-1].Text) != "This link type cannot be opened from Zap" {
		t.Errorf("toasts %v", r.s.Toasts)
	}
}

func TestInviteLinkOpensTheJoinDialog(t *testing.T) {
	m := msg("pre", 10, false, abi.Content{Tag: abi.ContentTagText})
	m.Content.Text = abi.ContentText{Text: []byte("x"), HasPreview: true, Preview: abi.LinkPreview{URL: []byte("https://chat.whatsapp.com/AbCdEf1234567890"), Title: []byte("Join"), HasTitle: true}}
	r := newRig(t, m)
	r.Play(widgettest.Click(ui.RoleLink, "Join"), widgettest.Idle(2))
	if r.s.Dialog.Kind != state.DialogJoinGroup {
		t.Errorf("dialog %v", r.s.Dialog.Kind)
	}
	got := commands(r, abi.CommandTagPreviewInvite)
	if len(got) != 1 || string(got[0].PreviewInvite.Code) != "AbCdEf1234567890" {
		t.Errorf("preview commands %+v", got)
	}
}
