package state_test

import (
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"zapfast/abi"
	"zapfast/core"
	"zapfast/core/fake"
	"zapfast/state"
)

const ada = "1@s.whatsapp.net"

func ev(tag abi.EventTag, set func(e *abi.Event)) *abi.Event {
	e := &abi.Event{Tag: tag}
	set(e)
	return e
}

func text(id string, ts int64) abi.Message {
	m := abi.Message{ID: []byte(id), Chat: []byte(ada), Sender: []byte(ada), Timestamp: ts}
	m.Content.Tag = abi.ContentTagText
	m.Content.Text.Text = []byte("m " + id)
	return m
}

// rig is a state over fake cores, one per account, with Ada's chat listed
// and the link connected.
type rig struct {
	t     *testing.T
	s     *state.State
	cores []*fake.Core
	clock time.Time
}

func newRig(t *testing.T, accounts int) *rig {
	r := &rig{t: t, s: state.New(), clock: time.Unix(1_800_000_000, 0)}
	r.s.Now = func() time.Time { return r.clock }
	for i := range accounts {
		f := fake.New(nil)
		r.cores = append(r.cores, f)
		a := r.s.AddAccount(fmt.Sprint(i+1), nil, f)
		a.Model.Now = r.s.Now
		f.Emit(ev(abi.EventTagLink, func(e *abi.Event) { e.Link.Status.Tag = abi.LinkStatusTagConnected }),
			ev(abi.EventTagChats, func(e *abi.Event) {
				e.Chats = abi.EventChats{Batch: 1, Last: true, Chats: []abi.Chat{{ID: []byte(ada), Name: []byte("Ada"), LastActivity: 5}}}
			}))
	}
	r.pump()
	return r
}

func (r *rig) pump() {
	r.t.Helper()
	r.s.Pump(0)
}

func (r *rig) sent(i int, tag abi.CommandTag) int {
	n := 0
	for _, c := range r.cores[i].Sent() {
		if c.Tag == tag {
			n++
		}
	}
	return n
}

func TestToastsOnlyForTheAccountOnScreen(t *testing.T) {
	r := newRig(t, 2)
	star := func(local bool) *abi.Event {
		return ev(abi.EventTagStarChanged, func(e *abi.Event) {
			e.StarChanged = abi.EventStarChanged{Chat: []byte(ada), Message: []byte("m"), Starred: true, Local: local}
		})
	}
	r.cores[0].Emit(star(false))
	r.cores[1].Emit(star(true))
	r.pump()
	if len(r.s.Toasts) != 0 {
		t.Errorf("toasts for a synced star or a hidden account: %q", r.s.Toasts[0].Text)
	}
	if !r.s.Accounts[1].Model.Starred([]byte(ada), []byte("m")) {
		t.Error("the hidden account's model was not updated")
	}
	r.cores[0].Emit(star(true))
	r.pump()
	if len(r.s.Toasts) != 1 || string(r.s.Toasts[0].Text) != "Starred" {
		t.Errorf("toasts %v", r.s.Toasts)
	}
}

func TestPagingAsksTheArchiveThenThePhone(t *testing.T) {
	r := newRig(t, 1)
	a := r.s.Accounts[0]
	a.OpenChat(a.Model.Chats.Find([]byte(ada)))
	r.cores[0].Emit(ev(abi.EventTagMessages, func(e *abi.Event) {
		e.Messages = abi.EventMessages{Chat: []byte(ada), Batch: 1, Last: true, Messages: []abi.Message{text("b", 20), text("c", 30)}}
	}))
	r.pump()
	a.LoadOlder(true)
	a.LoadOlder(true) // waits for the first
	if n := r.sent(0, abi.CommandTagLoadChat); n != 2 {
		t.Fatalf("%d LoadChat (the open, one page)", n)
	}
	if last := r.cores[0].Sent(); !last[len(last)-1].LoadChat.HasBefore || string(r.s.Panes.Anchor.At(0)) != "b" {
		t.Error("the page is not before the oldest message")
	}
	r.cores[0].Emit(ev(abi.EventTagMessages, func(e *abi.Event) {
		e.Messages = abi.EventMessages{Chat: []byte(ada), Batch: 2, Last: true, Older: true, Complete: true}
	}))
	r.pump()
	// The archive is complete: the phone is asked, once, then backs off.
	a.LoadOlder(true)
	a.LoadOlder(true)
	if n := r.sent(0, abi.CommandTagFetchOlder); n != 1 {
		t.Fatalf("%d FetchOlder", n)
	}
	r.cores[0].Emit(ev(abi.EventTagOlderFetched, func(e *abi.Event) { e.OlderFetched = abi.EventOlderFetched{Chat: []byte(ada), More: true} }))
	r.pump()
	a.FetchOlder([]byte(ada), true)
	if n := r.sent(0, abi.CommandTagFetchOlder); n != 1 {
		t.Error("asked again inside the cooldown")
	}
	r.clock = r.clock.Add(2 * state.PhoneCooldown)
	a.FetchOlder([]byte(ada), true)
	if n := r.sent(0, abi.CommandTagFetchOlder); n != 2 {
		t.Error("not asked after the cooldown")
	}
}

// A core that cannot ask the phone (the demo's) refuses FetchOlder: the chat
// is left as if the phone had nothing, not waiting for an answer.
func TestARefusedPhoneFetchDoesNotWait(t *testing.T) {
	r := newRig(t, 1)
	r.cores[0].Refuse(abi.CommandTagFetchOlder, core.EState, "the demo core does not answer FetchOlder")
	a := r.s.Accounts[0]
	a.OpenChat(a.Model.Chats.Find([]byte(ada)))
	r.cores[0].Emit(ev(abi.EventTagMessages, func(e *abi.Event) {
		e.Messages = abi.EventMessages{Chat: []byte(ada), Batch: 1, Last: true, Complete: true}
	}))
	r.pump()
	v := a.Model.Conversation([]byte(ada))
	for _, step := range []string{"the bare chat's fetch", "the reader's"} {
		if on, _ := v.FetchingPhone(); on || !v.PhoneExhausted() {
			t.Fatalf("after %s: fetching %v, exhausted %v", step, on, v.PhoneExhausted())
		}
		a.FetchOlder([]byte(ada), true)
	}
}

func TestDownloadMarksAndPrivacyWrites(t *testing.T) {
	r := newRig(t, 1)
	a := r.s.Accounts[0]
	a.Download([]byte(ada), []byte("p"), 2, true)
	sent := r.cores[0].Sent()
	if d := sent[len(sent)-1]; d.Tag != abi.CommandTagDownload || !d.Download.HasCard || d.Download.Card != 2 || !d.Download.Manual {
		t.Errorf("sent %+v", d.Download)
	}
	if _, st, _, ok := a.Model.File([]byte(ada), []byte("p"), 2); !ok || st != abi.MediaStateTagDownloading {
		t.Error("not marked downloading")
	}
	if a.SetPrivacy(abi.PrivacyKindAbout, abi.PrivacyChoiceNobody) {
		t.Error("wrote a privacy value before the phone's were known")
	}
	r.cores[0].Emit(ev(abi.EventTagAccountPrivacy, func(e *abi.Event) {
		e.AccountPrivacy.Values = []abi.PrivacyValue{{Kind: abi.PrivacyKindAbout, Choice: abi.PrivacyChoiceEveryone}}
	}))
	r.pump()
	if !a.SetPrivacy(abi.PrivacyKindAbout, abi.PrivacyChoiceNobody) || r.sent(0, abi.CommandTagSetAccountPrivacy) != 1 {
		t.Error("the write was not sent")
	}
}

func TestADeletedLabelStopsFiltering(t *testing.T) {
	r := newRig(t, 1)
	a := r.s.Accounts[0]
	labels := func(ids ...string) *abi.Event {
		return ev(abi.EventTagLabels, func(e *abi.Event) {
			e.Labels = abi.EventLabels{Batch: 1, Last: true}
			for _, id := range ids {
				e.Labels.Labels = append(e.Labels.Labels, abi.Label{ID: []byte(id), Name: []byte(id)})
			}
		})
	}
	r.cores[0].Emit(labels("work", "home"))
	r.pump()
	a.Filter = state.FilterLabel
	a.Label.Add([]byte("work"))
	r.cores[0].Emit(labels("home"))
	r.pump()
	if a.Filter != state.FilterAll || a.Label.Len() != 0 {
		t.Errorf("filter %v label %d", a.Filter, a.Label.Len())
	}
}

// Toasts as upstream keeps them: four info toasts and three errors at most,
// the oldest going; an error shown again moves to the end. Workers may post
// while the main thread reads them (ToastsInto) and dismisses them.
func TestToastsFromAnyGoroutine(t *testing.T) {
	s := state.New()
	var woken atomic.Int32
	s.Wake = func() { woken.Add(1) }
	for i := range 6 {
		s.Toast([]byte(fmt.Sprint("info ", i)), false)
	}
	s.Toast([]byte("e1"), true)
	s.Toast([]byte("e2"), true)
	s.Toast([]byte("e3"), true)
	s.Toast([]byte("e1"), true)
	s.Toast([]byte("e4"), true)
	var got []string
	for _, t := range s.ToastsInto(nil) {
		got = append(got, string(t.Text))
	}
	if want := "[info 2 info 3 info 4 info 5 e3 e1 e4]"; fmt.Sprint(got) != want {
		t.Errorf("toasts %v, want %s", got, want)
	}
	if woken.Load() != 11 {
		t.Errorf("woken %d times", woken.Load())
	}
	var wg sync.WaitGroup
	for g := range 8 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := range 200 {
				s.Toast([]byte(fmt.Sprint(g, ":", i)), i%2 == 0)
			}
		}()
	}
	var buf []state.Toast
	for range 500 {
		buf = s.ToastsInto(buf[:0])
		if len(buf) > 7 {
			t.Fatalf("%d toasts", len(buf))
		}
		if len(buf) > 0 {
			s.DismissToast(buf[0].ID)
		}
	}
	wg.Wait()
	if n := len(s.ToastsInto(nil)); n == 0 || n > 7 {
		t.Errorf("%d toasts at the end", n)
	}
}

// state.New's opener refuses: a link, a core's OpenPath and a file to show
// in its folder reach it, and it records them and opens nothing; only
// cmd/zapfast installs the real opener.
func TestNewsShellOpensNothing(t *testing.T) {
	r := newRig(t, 1)
	rs, ok := r.s.Shell.(*core.RefusingShell)
	if !ok {
		t.Fatalf("state.New's Shell is %T", r.s.Shell)
	}
	if err := r.s.Shell.Open("https://example.com"); !errors.Is(err, core.ErrOpenRefused) {
		t.Errorf("a link: %v", err)
	}
	r.cores[0].Emit(ev(abi.EventTagOpenPath, func(e *abi.Event) { e.OpenPath.Path = []byte("/tmp/Report.pdf") }))
	for i := 0; i < 200 && len(rs.Attempts()) < 2; i++ {
		r.pump()
		time.Sleep(time.Millisecond)
	}
	if err := r.s.Shell.(core.Revealer).Reveal([]string{"/tmp/run.command"}); !errors.Is(err, core.ErrOpenRefused) {
		t.Errorf("a reveal: %v", err)
	}
	if got := rs.Attempts(); fmt.Sprint(got) != "[https://example.com /tmp/Report.pdf /tmp/run.command]" {
		t.Errorf("attempts %q", got)
	}
}
