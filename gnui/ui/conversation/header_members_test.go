package conversation

import (
	"bytes"
	"slices"
	"strconv"
	"strings"
	"testing"

	"zapfast/abi"
	"zapfast/model"
	"zapfast/state"
)

func mixedGroup() (*state.Account, model.Chat, []string) {
	a := &state.Account{Model: model.New(), Serial: 1}
	ch := abi.Chat{ID: []byte(group), Kind: abi.ChatKindGroup}
	names := make([]string, 500)
	stems := []string{"ALIce", "bob", "ÉLOdie", "čEněk", "İPEK", "Σίγμα", "ẞeta", "ångStröm"}
	contacts := make([]abi.Contact, len(names))
	for i := range names {
		name := stems[i%len(stems)] + strconv.Itoa((i*137)%503)
		// Include case-equivalent names to exercise the old tie ordering.
		if i%23 == 0 {
			name = "aLiCE"
		}
		if i%29 == 0 {
			name = "Alice"
		}
		names[i] = name
		id := []byte(strconv.Itoa(i+10000) + "@s.whatsapp.net")
		contacts[i] = abi.Contact{ID: id, FullName: []byte(name), HasFullName: true}
		ch.Participants = append(ch.Participants, id)
	}
	a.Model.Event(&abi.Event{Tag: abi.EventTagContacts, Contacts: abi.EventContacts{Last: true, Contacts: contacts}})
	a.Model.Event(&abi.Event{Tag: abi.EventTagChatUpdated, ChatUpdated: abi.EventChatUpdated{Chat: ch}})
	return a, a.Model.Chats.At(a.Model.Chats.Find(ch.ID)), names
}

func TestGroupMembersFoldOnce(t *testing.T) {
	a, ch, names := mixedGroup()
	// Reference the previous comparator, including Unicode ToLower and ties.
	slices.SortFunc(names, func(x, y string) int { return bytes.Compare(bytes.ToLower([]byte(x)), bytes.ToLower([]byte(y))) })
	var want []string
	for i := 0; i < len(names); {
		j := i + 1
		for j < len(names) && bytes.EqualFold([]byte(names[i]), []byte(names[j])) {
			j++
		}
		n := names[i]
		if j-i > 1 {
			n += " x" + strconv.Itoa(j-i)
		}
		want = append(want, n)
		i = j
	}
	s := New()
	if got := string(s.members(a, ch)); got != strings.Join(want, ", ") {
		t.Fatalf("member ordering or original names changed:\ngot %s\nwant %s", got, strings.Join(want, ", "))
	}
	refresh := testing.AllocsPerRun(10, func() { s.mem.ok = false; s.members(a, ch) })
	steady := testing.AllocsPerRun(100, func() { s.members(a, ch) })
	t.Logf("500 members: refresh=%g allocations, steady=%g", refresh, steady)
	if refresh > 510 {
		t.Errorf("refresh allocated %g times: names should be folded at most once", refresh)
	}
	if steady != 0 {
		t.Errorf("steady members allocated %g times", steady)
	}
}

func TestLargeGroupSteadyFramesAllocateNothing(t *testing.T) {
	r := newRig(t)
	a, _, _ := mixedGroup()
	r.a.Model = a.Model
	r.a.Model.Now = r.s.Now
	r.open(group, msg(group, "members", base0, false, "Hello everyone"))
	r.Frames(10)
	if n := testing.AllocsPerRun(60, r.Frame); n != 0 {
		t.Errorf("500-member group: %g allocations per steady frame", n)
	}
}
