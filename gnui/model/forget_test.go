package model

import (
	"bytes"
	"testing"

	"zapfast/abi"
)

// A removed chat's bytes are overwritten in the store, not only unlisted: its
// last message's words and its messages are not in memory afterwards.
func TestARemovedChatsBytesAreWiped(t *testing.T) {
	s := New()
	ch := abi.Chat{ID: []byte("1@s.whatsapp.net"), Name: []byte("Ada"), HasLast: true, LastActivity: 5}
	ch.Last.Summary, ch.Last.Full = []byte("summary secret"), []byte("full secret")
	s.Event(&abi.Event{Tag: abi.EventTagChats, Chats: abi.EventChats{Batch: 1, Last: true, Chats: []abi.Chat{ch}}})
	m := abi.Message{ID: []byte("m1"), Chat: ch.ID, Sender: ch.ID, Timestamp: 3}
	m.Content.Tag, m.Content.Text.Text = abi.ContentTagText, []byte("message secret")
	s.Open(ch.ID)
	s.Event(&abi.Event{Tag: abi.EventTagMessages, Messages: abi.EventMessages{Chat: ch.ID, Last: true, Messages: []abi.Message{m}}})
	if !bytes.Contains(s.Chats.a.b, []byte("summary secret")) {
		t.Fatal("set-up: the row's bytes are not where the test looks")
	}
	s.Event(&abi.Event{Tag: abi.EventTagChatRemoved, ChatRemoved: abi.EventChatRemoved{Chat: ch.ID}})
	for _, w := range []string{"summary secret", "full secret", "1@s.whatsapp.net"} {
		if bytes.Contains(s.Chats.a.b[:cap(s.Chats.a.b)], []byte(w)) {
			t.Errorf("%q is still in the chat list's bytes", w)
		}
	}
	for i := range s.convs {
		if b := s.convs[i].a.b; bytes.Contains(b[:cap(b)], []byte("message secret")) {
			t.Error("the removed chat's message is still in a conversation's bytes")
		}
	}
}
