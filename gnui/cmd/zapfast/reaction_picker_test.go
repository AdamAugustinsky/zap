package main

import (
	"testing"
	"time"

	"gonativeui/ui"
	"gonativeui/widget/widgettest"

	"zapfast/abi"
)

// MM-003: drive the conversation's + and the full picker in one wired
// window, not State.OpenReaction directly (src/ui/conversation.rs:5739-5766).
func TestConversationReactionPickerTogether(t *testing.T) {
	r, f := keyRig(t)
	r.open(adaJID)
	at := time.Date(2026, 10, 22, 12, 0, 0, 0, time.Local).Unix()
	f.Emit(ev(abi.EventTagMessages, func(e *abi.Event) {
		e.Messages = abi.EventMessages{Chat: []byte(adaJID), Batch: 1, Last: true, Complete: true,
			Messages: []abi.Message{{ID: []byte("react-me"), Chat: []byte(adaJID), Sender: []byte(adaJID), Timestamp: at,
				Content: abi.Content{Tag: abi.ContentTagText, Text: abi.ContentText{Text: []byte("React here")}}}}}
	}))
	r.Frames(4)
	r.Play(widgettest.Click(ui.RoleTextField, "Message"), widgettest.Type("keep this draft"),
		widgettest.RClick(ui.RoleGroup, "Ada Lovelace, 12:00"), widgettest.Idle(2),
		widgettest.Click(ui.RoleButton, "React with any emoji"), widgettest.Idle(3))
	r.Must(ui.RoleDialog, "React to message")
	if _, ok := r.Find(ui.RoleButton, "Copy message ID"); ok {
		t.Fatal("the message menu stayed open beside the picker")
	}
	r.Play(widgettest.Click(ui.RoleSearchField, "Search emoji"), widgettest.Type("crab"), widgettest.Idle(2),
		widgettest.Click(ui.RoleButton, "🦀"), widgettest.Idle(3))
	if n := sentTag(f, abi.CommandTagReact); n != 1 {
		t.Fatalf("sent %d reactions, want one", n)
	}
	for _, c := range f.Sent() {
		if c.Tag == abi.CommandTagReact && (string(c.React.Chat) != adaJID || string(c.React.Message) != "react-me" || string(c.React.Emoji) != "🦀") {
			t.Fatalf("wrong reaction: %+v", c.React)
		}
	}
	if _, ok := r.Find(ui.RoleDialog, "React to message"); ok || r.s.Panes.ReactTarget.Open() {
		t.Error("the reaction picker did not close")
	}
	composer := r.Must(ui.RoleTextField, "Message")
	if got := string(r.Tree().Text(composer.Value)); got != "keep this draft" {
		t.Errorf("reaction changed the composer to %q", got)
	}
}
