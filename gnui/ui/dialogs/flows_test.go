package dialogs

import (
	"strings"
	"testing"

	"gonativeui/ui"
	"gonativeui/widget/widgettest"

	"zapfast/abi"
	"zapfast/lockhash"
	"zapfast/model"
	"zapfast/state"
)

// DL-005: a named number is saved as a contact; the command carries the
// digits, the composed name and the phone choice.
func TestNewContactSendsLookup(t *testing.T) {
	r := newRig(t, baseEvents()...)
	r.open(state.Dialog{Kind: state.DialogNewContact})
	r.typeInto("Phone number", "+44 20 7946 0958")
	r.typeInto("First name", "Ada")
	r.typeInto("Surname", "King")
	r.click(ui.RoleButton, "Save contact")
	got := r.sent(abi.CommandTagNewContact)
	if len(got) != 1 {
		t.Fatalf("sent %v\n%s", r.f.SentTags(), widgettest.Dump(r.Tree()))
	}
	nc := got[0].NewContact
	if string(nc.Phone) != "442079460958" || string(nc.FullName) != "Ada King" || !nc.HasFullName {
		t.Errorf("lookup %+v", nc)
	}
	if !r.s.Dialog.Open() {
		t.Error("the sheet closed before the core answered")
	}
}

// DL-005: a number without a name is only messaged, not saved.
func TestNewContactWithoutNameMessagesOnly(t *testing.T) {
	r := newRig(t, baseEvents()...)
	r.open(state.Dialog{Kind: state.DialogNewContact})
	r.typeInto("Phone number", "+442079460958")
	r.click(ui.RoleButton, "Message")
	got := r.sent(abi.CommandTagNewContact)
	if len(got) != 1 || got[0].NewContact.HasFullName {
		t.Fatalf("sent %v", r.f.SentTags())
	}
}

// DL-005: nothing is sent for a number that is not international.
func TestNewContactRejectsShortNumber(t *testing.T) {
	r := newRig(t, baseEvents()...)
	r.open(state.Dialog{Kind: state.DialogNewContact})
	r.typeInto("Phone number", "12")
	r.click(ui.RoleButton, "Message")
	if n := len(r.sent(abi.CommandTagNewContact)); n != 0 {
		t.Fatalf("sent %d lookups for 12", n)
	}
}

// DL-006: Pair with phone asks for a code with the digits only.
func TestPairAsksForCode(t *testing.T) {
	r := newRig(t, baseEvents()...)
	r.open(state.Dialog{Kind: state.DialogPairWithPhone})
	r.typeInto("Phone number", "1 555 123 4567")
	r.click(ui.RoleButton, "Get a code")
	var found bool
	for _, c := range r.f.Sent() {
		if c.Tag == abi.CommandTagPairWithPhone && string(c.PairWithPhone.Phone) == "15551234567" {
			found = true
		}
	}
	if !found {
		t.Fatalf("sent %v", r.f.SentTags())
	}
}

// DL-012: a poll needs a question and two different answers; Send poll
// carries them in the draft.
func TestPollNeedsQuestionAndAnswers(t *testing.T) {
	r := newRig(t, baseEvents()...)
	ada := r.ref("1@s.whatsapp.net")
	r.open(state.Dialog{Kind: state.DialogCreatePoll, Chat: ada})
	r.click(ui.RoleButton, "Send poll")
	if len(r.sent(abi.CommandTagCreatePoll)) != 0 {
		t.Fatal("an empty poll was sent")
	}
	if !r.has(ui.RoleLabel, "Enter a question of up to 255 characters.") {
		t.Errorf("no hint\n%s", widgettest.Dump(r.Tree()))
	}
	r.typeInto("Ask a question", "Lunch?")
	r.typeInto("Answer 1", "Soup")
	r.typeInto("Answer 2", "Soup")
	if !r.has(ui.RoleLabel, "Each answer must be different.") {
		t.Error("duplicate answers pass")
	}
	r.Play(widgettest.Click(ui.RoleTextField, "Answer 2"), widgettest.Key('A', ui.ModSuper), widgettest.Type("Salad"))
	r.Frames(2)
	r.click(ui.RoleButton, "Send poll")
	got := r.sent(abi.CommandTagCreatePoll)
	if len(got) != 1 {
		t.Fatalf("sent %v\n%s", r.f.SentTags(), widgettest.Dump(r.Tree()))
	}
	d := got[0].CreatePoll.Draft
	if string(d.Question) != "Lunch?" || len(d.Options) != 2 || string(d.Options[0]) != "Soup" || string(d.Options[1]) != "Salad" {
		t.Errorf("draft %+v", d)
	}
}

// DL-013: pasted coordinates send a location; text that is no place keeps
// Send from doing anything.
func TestLocationSendsSpot(t *testing.T) {
	r := newRig(t, baseEvents()...)
	ada := r.ref("1@s.whatsapp.net")
	r.open(state.Dialog{Kind: state.DialogSendLocation, Chat: ada})
	r.typeInto("Paste a map link or coordinates", "hello")
	if !r.has(ui.RoleLabel, "That is not a location.") {
		t.Errorf("no complaint\n%s", widgettest.Dump(r.Tree()))
	}
	r.click(ui.RoleButton, "Send")
	if len(r.sent(abi.CommandTagSendLocation)) != 0 {
		t.Fatal("sent a non-location")
	}
	r.Play(widgettest.Click(ui.RoleTextField, "Paste a map link or coordinates"), widgettest.Key('A', ui.ModSuper), widgettest.Type("48.8584, 2.2945"))
	r.Frames(2)
	r.click(ui.RoleButton, "Send")
	got := r.sent(abi.CommandTagSendLocation)
	if len(got) != 1 {
		t.Fatalf("sent %v", r.f.SentTags())
	}
}

// DL-014: labels are created from a name; the limit is explained.
func TestLabelsCreate(t *testing.T) {
	r := newRig(t, baseEvents()...)
	r.open(state.Dialog{Kind: state.DialogLabels})
	if !r.has(ui.RoleLabel, "Labels stay on this computer. They do not reach your phone or anyone you chat with.") {
		t.Fatalf("no explanation\n%s", widgettest.Dump(r.Tree()))
	}
	r.typeInto("New label", "Work")
	r.click(ui.RoleButton, "Create")
	if len(r.sent(abi.CommandTagCreateLabel)) != 1 {
		t.Fatalf("sent %v", r.f.SentTags())
	}
}

// DL-002: the help dialog lists every shortcut.
func TestShortcutsListsThem(t *testing.T) {
	r := newRig(t, baseEvents()...)
	r.open(state.Dialog{Kind: state.DialogShortcuts})
	if !r.has(ui.RoleDialog, "Keyboard shortcuts") {
		t.Fatalf("no dialog\n%s", widgettest.Dump(r.Tree()))
	}
	if !r.has(ui.RoleLabel, "Search chats") {
		t.Error("no Search chats row")
	}
	r.Key(ui.KeyEscape, 0)
	r.Frames(2)
	if r.s.Dialog.Open() {
		t.Error("Escape leaves it open")
	}
}

// DL-001: About names the version.
func TestAbout(t *testing.T) {
	r := newRig(t, baseEvents()...)
	r.open(state.Dialog{Kind: state.DialogAbout})
	found := false
	for _, n := range widgettest.Dump(r.Tree()) {
		_ = n
		found = true
	}
	_ = found
	if !strings.Contains(widgettest.Dump(r.Tree()), state.Version) {
		t.Errorf("no version %s\n%s", state.Version, widgettest.Dump(r.Tree()))
	}
}

// DL-003: forwarding needs a chosen chat; the review step lists it, and
// Forward sends the message ids.
func TestForwardChoosesAndSends(t *testing.T) {
	r := newRig(t, baseEvents()...)
	ada := r.ref("1@s.whatsapp.net")
	var ids model.IDList
	ids.Add([]byte("m1"))
	ids.Add([]byte("m2"))
	r.openForward(ada, &ids)
	if !r.has(ui.RoleDialog, "Forward 2 messages") {
		t.Fatalf("title\n%s", widgettest.Dump(r.Tree()))
	}
	if len(r.sent(abi.CommandTagForward)) != 0 {
		t.Fatal("sent before choosing")
	}
	r.click(ui.RoleCheckbox, "Grace Hopper")
	if !r.has(ui.RoleLabel, "1 recipient selected") {
		t.Fatalf("no count\n%s", widgettest.Dump(r.Tree()))
	}
	r.click(ui.RoleButton, "Forward")
	got := r.sent(abi.CommandTagForward)
	if len(got) != 1 {
		t.Fatalf("sent %v", r.f.SentTags())
	}
	fw := got[0].Forward
	if string(fw.FromChat) != "1@s.whatsapp.net" || len(fw.Messages) != 2 || len(fw.ToChats) != 1 || string(fw.ToChats[0]) != "2@s.whatsapp.net" {
		t.Errorf("forward %+v", fw)
	}
}

func TestPhoneAndName(t *testing.T) {
	for _, c := range []struct {
		in, want string
		ok       bool
	}{
		{"+44 20 7946 0958", "442079460958", true},
		{"(555) 123-4567", "5551234567", true},
		{"555-12", "", false},
		{"0123456789", "", false},
		{"+1 (555) 123-4567", "15551234567", true},
		{"", "", false},
	} {
		got, ok := InternationalPhone(c.in)
		if got != c.want || ok != c.ok {
			t.Errorf("InternationalPhone(%q) = %q, %v; want %q, %v", c.in, got, ok, c.want, c.ok)
		}
	}
	full, short, named := ComposeName("  Ada ", " King ")
	if full != "Ada King" || short != "Ada" || !named {
		t.Errorf("ComposeName = %q %q %v", full, short, named)
	}
	if _, _, named := ComposeName(" ", ""); named {
		t.Error("blank names are named")
	}
}

func TestParseSpot(t *testing.T) {
	for _, c := range []struct {
		in       string
		lat, lon float64
		ok       bool
	}{
		{"48.8584, 2.2945", 48.8584, 2.2945, true},
		{"geo:51.5,-0.12", 51.5, -0.12, true},
		{"https://www.google.com/maps/@40.7128,-74.006,15z", 40.7128, -74.006, true},
		{"https://www.openstreetmap.org/#map=17/52.52/13.405", 52.52, 13.405, true},
		{"https://maps.apple.com/?ll=37.33,-122.03", 37.33, -122.03, true},
		{"91, 0", 0, 0, false},
		{"hello", 0, 0, false},
	} {
		sp, ok := ParseSpot(c.in)
		if ok != c.ok {
			t.Errorf("ParseSpot(%q) ok = %v", c.in, ok)
			continue
		}
		if ok && (sp.Latitude != c.lat || sp.Longitude != c.lon) {
			t.Errorf("ParseSpot(%q) = %v,%v", c.in, sp.Latitude, sp.Longitude)
		}
	}
	if !IsShortLink("https://goo.gl/maps/abc") {
		t.Error("goo.gl is a short link")
	}
}

// The locked chats' code (zf:src/ui/dialogs.rs unlock_locked_chats): typed
// masked, set up when there is none, then checked; a wrong code keeps the
// folder shut.
func TestChatLockCode(t *testing.T) {
	r := newRig(t, linkedEvent())
	st := &r.s.Settings
	r.open(state.Dialog{Kind: state.DialogUnlockLockedChats})
	if !r.has(ui.RoleTextField, "Confirm code") {
		t.Fatal("no confirmation while setting up")
	}
	r.typeInto("Local code", " 1234")
	r.typeInto("Confirm code", "1234 ")
	if v, w := r.value("Local code"), r.value("Confirm code"); v != "•••••" || w != "•••••" {
		t.Errorf("the code shows as %q and %q", v, w)
	}
	r.click(ui.RoleButton, "Set code and open")
	if len(st.ChatLockHash) == 0 {
		t.Fatal("no code was set")
	}
	if !lockhash.VerifiesChat(string(st.ChatLockHash), []byte("1234")) || lockhash.VerifiesChat(string(st.ChatLockHash), []byte("4321")) {
		t.Error("the stored code does not check")
	}
	if r.a.Mode != state.ListLocked || r.s.Dialog.Open() {
		t.Fatalf("mode %v, dialog %v", r.a.Mode, r.s.Dialog.Kind)
	}

	r.a.Mode = state.ListChats
	r.open(state.Dialog{Kind: state.DialogUnlockLockedChats})
	if r.has(ui.RoleTextField, "Confirm code") {
		t.Error("a confirmation with a code set")
	}
	r.typeInto("Local code", "4321")
	r.click(ui.RoleButton, "Open locked chats")
	if r.a.Mode == state.ListLocked || !r.has(ui.RoleLabel, "That code did not match. Try again.") {
		t.Fatalf("a wrong code: mode %v", r.a.Mode)
	}
	if r.value("Local code") != "" {
		t.Error("the wrong code stays in the field")
	}
	r.typeInto("Local code", "1234")
	r.click(ui.RoleButton, "Open locked chats")
	if r.a.Mode != state.ListLocked {
		t.Errorf("the right code: mode %v", r.a.Mode)
	}
}

// zeroed reports whether every byte the buffer's storage can hold is zero.
func zeroed(b []byte) bool {
	for _, x := range b[:cap(b)] {
		if x != 0 {
			return false
		}
	}
	return true
}

// The code fields are secure fields (bullets only in the semantic tree, the
// secure subrole, secure keyboard entry while one has the keyboard), and what
// was typed is wiped, storage too, on submit, on Cancel and when the dialog
// closes.
func TestChatLockFieldsAreSecureAndWiped(t *testing.T) {
	r := newRig(t, linkedEvent())
	u := &r.h.scr.unlock
	r.open(state.Dialog{Kind: state.DialogUnlockLockedChats})
	for _, name := range []string{"Local code", "Confirm code"} {
		r.typeInto(name, "hunter2")
		if r.Must(ui.RoleTextField, name).State&ui.SemSecure == 0 {
			t.Errorf("%s: not a secure text field", name)
		}
		if strings.Contains(widgettest.Dump(r.Tree()), "hunter") || r.value(name) != strings.Repeat("•", 7) {
			t.Errorf("%s announces %q", name, r.value(name))
		}
	}
	if !r.Context().SecureFieldFocused() {
		t.Error("no secure keyboard entry with a code field in focus")
	}
	code, confirm := u.code.Bytes(), u.confirm.Bytes()
	r.click(ui.RoleButton, "Set code and open")
	if u.code.Len() != 0 || u.confirm.Len() != 0 || !zeroed(code) || !zeroed(confirm) {
		t.Error("submitting left the code in the fields")
	}
	if r.Context().SecureFieldFocused() {
		t.Error("secure keyboard entry stays on after the dialog closed")
	}
	// Cancel with a half-typed code.
	r.a.Mode = state.ListChats
	r.open(state.Dialog{Kind: state.DialogUnlockLockedChats})
	r.typeInto("Local code", "half")
	code = u.code.Bytes()
	r.click(ui.RoleButton, "Cancel")
	if u.code.Len() != 0 || !zeroed(code) {
		t.Error("Cancel left the half-typed code")
	}
}
