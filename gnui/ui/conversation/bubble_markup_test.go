package conversation

import (
	"bytes"
	"testing"

	"zapfast/abi"
)

func TestBubbleMarkupRefreshesMentionNames(t *testing.T) {
	r := newRig(t)
	contact := func(name string) {
		r.a.Model.Event(&abi.Event{Tag: abi.EventTagContacts, Contacts: abi.EventContacts{
			Last: true, Contacts: []abi.Contact{{ID: []byte(ada), FullName: []byte(name), HasFullName: true}},
		}})
	}
	contact("Alice")
	m := msg(ada, "mention", base0, false, "Hello *@12345* example.com")
	m.Mentions = []abi.MentionRef{{User: []byte("12345"), ID: []byte(ada)}}
	r.open(ada, m)
	before := bytes.Clone(r.Image().Pix)
	contact("Beatrice")
	r.Frame()
	if bytes.Equal(before, r.Image().Pix) {
		t.Fatal("contact rename did not refresh the rendered mention")
	}
	// Copy can run after contacts change and before another bubble build.
	contact("Charlie")
	v := r.a.Model.Open([]byte(ada))
	r.scr.shown(r.a, v.At(0))
	if got := string(r.scr.body); got != "Hello @Charlie example.com" {
		t.Fatalf("copy text after rename = %q", got)
	}
}
