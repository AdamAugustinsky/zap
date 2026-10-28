package composer

import (
	"bytes"
	"strings"
	"testing"
	"time"

	"gonativeui/app"
	"gonativeui/app/perm"
	"gonativeui/ui"
	"gonativeui/widget/widgettest"

	"zapfast/abi"
	"zapfast/core/fake"
	"zapfast/model"
	"zapfast/state"
	"zapfast/ui/screen"
	"zapfast/ui/shell"
)

// harness is the shell with the composer in its slot; pre runs before the
// next frame's build, with the frame's input (a drop, a composition).
type harness struct {
	sh  *shell.Shell
	pre func(c *ui.Context)
}

func (h *harness) Init(a *app.App) error { return h.sh.Init(a) }

func (h *harness) Build(c *ui.Context) {
	if f := h.pre; f != nil {
		h.pre = nil
		f(c)
	}
	h.sh.Build(c)
}

// rig is the main window offscreen at 1100×720 points, scale 1, with the
// composer wired, over the fake core.
type rig struct {
	*widgettest.Rig
	t   *testing.T
	s   *state.State
	f   *fake.Core
	a   *state.Account
	scr *Screen
	mic *fakeMic
	h   *harness
	now time.Time
}

const (
	ada   = "1@s.whatsapp.net"
	grace = "2@s.whatsapp.net"
	group = "g1@g.us"
	me    = "99@s.whatsapp.net"
)

func ev(tag abi.EventTag, set func(e *abi.Event)) *abi.Event {
	e := new(abi.Event)
	e.Tag = tag
	set(e)
	return e
}

// defaultChats are a direct chat with Ada, one with Grace and a group of
// the three of us.
func defaultChats() []abi.Chat {
	return []abi.Chat{
		{ID: []byte(ada), Name: []byte("Ada Lovelace"), LastActivity: 30},
		{ID: []byte(grace), Name: []byte("Grace Hopper"), LastActivity: 20},
		{ID: []byte(group), Name: []byte("Engines"), Kind: abi.ChatKindGroup, GroupSubjectKnown: true, LastActivity: 10,
			Participants: [][]byte{[]byte(ada), []byte(grace), []byte(me)}},
	}
}

func newRig(t *testing.T, chats []abi.Chat, setup func(s *state.State)) *rig {
	t.Helper()
	return newRigWith(t, chats, setup, nil)
}

// newRigWith is newRig with more slots filled by slots.
func newRigWith(t *testing.T, chats []abi.Chat, setup func(s *state.State), slots func(set *screen.Set)) *rig {
	t.Helper()
	return newRigSized(t, 0, 0, chats, setup, slots)
}

// newRigSized is newRigWith in a window w by h points (the default size for 0).
func newRigSized(t *testing.T, w, h int, chats []abi.Chat, setup func(s *state.State), slots func(set *screen.Set)) *rig {
	t.Helper()
	f := fake.New(nil)
	if chats == nil {
		chats = defaultChats()
	}
	f.Emit(
		ev(abi.EventTagLink, func(e *abi.Event) { e.Link.Status.Tag = abi.LinkStatusTagConnected }),
		ev(abi.EventTagMe, func(e *abi.Event) { e.Me.ID = []byte(me) }),
		ev(abi.EventTagChats, func(e *abi.Event) { e.Chats = abi.EventChats{Batch: 1, Last: true, Chats: chats} }),
		ev(abi.EventTagContacts, func(e *abi.Event) {
			e.Contacts.Contacts = []abi.Contact{
				{ID: []byte(ada), FullName: []byte("Ada Lovelace"), HasFullName: true},
				{ID: []byte(grace), FullName: []byte("Grace Hopper"), HasFullName: true},
			}
		}),
	)
	s := state.New()
	r := &rig{t: t, s: s, f: f, now: time.Unix(1_800_000_000, 0)}
	s.Now = func() time.Time { return r.now }
	if setup != nil {
		setup(s)
	}
	r.scr = New()
	// The microphone is a fake (the demo's is its own), and the audio stack
	// is never started.
	r.mic = &fakeMic{access: perm.Authorized}
	r.scr.voice.newMic = func(demo bool, now func() time.Time, wake func()) mic {
		if demo {
			return newMic(true, now, wake)
		}
		return r.mic
	}
	r.scr.voice.warm = func() {}
	set := &screen.Set{Composer: r.scr}
	if slots != nil {
		slots(set)
	}
	sh, err := shell.New(s, set)
	if err != nil {
		t.Fatal(err)
	}
	sh.Traffic = true
	r.a = s.AddAccount("1", nil, f)
	r.h = &harness{sh: sh}
	cfg := shell.Config(s)
	if w == 0 {
		w, h = int(cfg.Width), int(cfg.Height)
	}
	r.Rig = widgettest.Open(t, cfg, w, h, 1, r.h)
	r.Offscreen().UI.SetSystemAppearance(ui.Appearance{Known: true})
	r.Frames(4)
	return r
}

// open opens the chat with the JID.
func (r *rig) open(jid string) {
	r.t.Helper()
	ref := r.a.Model.Chats.Find([]byte(jid))
	if ref == 0 {
		r.t.Fatalf("no chat %s", jid)
	}
	r.a.OpenChat(ref)
	r.Frames(3)
}

// messages gives the open chat these messages (as LoadChat's answer would).
func (r *rig) messages(chat string, ms ...abi.Message) {
	for i := range ms {
		ms[i].Chat = []byte(chat)
	}
	r.f.Emit(ev(abi.EventTagMessages, func(e *abi.Event) {
		e.Messages.Chat = []byte(chat)
		e.Messages.Messages = ms
	}))
	r.Frames(2)
}

func textMsg(id, from string, mine bool, at int64, text string) abi.Message {
	m := abi.Message{ID: []byte(id), Sender: []byte(from), FromMe: mine, Timestamp: at}
	m.Content.Tag = abi.ContentTagText
	m.Content.Text.Text = []byte(text)
	return m
}

// focus clicks the composer's text.
func (r *rig) focus() {
	r.t.Helper()
	r.Play(widgettest.Click(ui.RoleTextField, "Message"), widgettest.Idle(2))
}

// typeText types into the focused composer.
func (r *rig) typeText(s string) {
	r.t.Helper()
	r.Play(widgettest.Type(s), widgettest.Idle(2))
}

func (r *rig) key(code ui.KeyCode, mods ui.Mods) {
	r.t.Helper()
	r.Play(widgettest.Key(code, mods), widgettest.Idle(2))
}

func (r *rig) text() string { return string(r.scr.st.Text()) }

// sent is the commands sent with tag, oldest first.
func (r *rig) sent(tag abi.CommandTag) []abi.Command {
	var out []abi.Command
	for _, c := range r.f.Sent() {
		if c.Tag == tag {
			out = append(out, c)
		}
	}
	return out
}

// last is the last command sent with tag.
func (r *rig) last(tag abi.CommandTag) (abi.Command, bool) {
	s := r.sent(tag)
	if len(s) == 0 {
		return abi.Command{}, false
	}
	return s[len(s)-1], true
}

// has reports whether the tree has a node of role whose name is name.
func (r *rig) has(role ui.Role, name string) bool {
	_, ok := r.Find(role, name)
	return ok
}

// hasText reports whether any node's name holds s.
func (r *rig) hasText(s string) bool {
	t := r.Tree()
	for _, n := range t.Nodes {
		if strings.Contains(string(t.Text(n.Name)), s) {
			return true
		}
	}
	return false
}

// before runs f with the next frame's context, before it builds.
func (r *rig) before(f func(c *ui.Context)) {
	r.h.pre = f
	r.Frames(1)
}

// ref is the chat's ref.
func (r *rig) ref(jid string) model.ChatRef { return r.a.Model.Chats.Find([]byte(jid)) }

func eq(b []byte, s string) bool { return bytes.Equal(b, []byte(s)) }
