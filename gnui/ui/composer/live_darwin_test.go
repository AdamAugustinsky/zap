//go:build darwin

package composer

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"gonativeui/app"
	"gonativeui/platform"
	"gonativeui/ui"
	"gonativeui/widget/widgettest"

	"zapfast/abi"
	"zapfast/core"
	"zapfast/core/fake"
	"zapfast/state"
	"zapfast/ui/peek"
	"zapfast/ui/screen"
	"zapfast/ui/shell"
)

// The live test needs the main thread (a real window and its NSTextView).
func TestMain(m *testing.M) { widgettest.RunMain(m) }

// The hosted NSTextView composer in a real window: typing, the mention
// completion through the host commands, Shift+Enter lines growing the band,
// Enter sending, the picker's emoji going in, and the view going with the
// composer while the selection bar has its place. It opens a window,
// so it runs only with ZAPFAST_LIVE=1 (GNUI_GNUISHOT=1 also captures it).
func TestLiveHostedComposer(t *testing.T) {
	if os.Getenv("ZAPFAST_LIVE") == "" {
		t.Skip("opens a window: set ZAPFAST_LIVE=1")
	}
	f := fake.New(nil)
	f.Emit(
		ev(abi.EventTagLink, func(e *abi.Event) { e.Link.Status.Tag = abi.LinkStatusTagConnected }),
		ev(abi.EventTagMe, func(e *abi.Event) { e.Me.ID = []byte(me) }),
		ev(abi.EventTagChats, func(e *abi.Event) {
			e.Chats = abi.EventChats{Batch: 1, Last: true, Chats: append(defaultChats(),
				abi.Chat{ID: []byte("g2@g.us"), Name: []byte("Gone"), Kind: abi.ChatKindGroup, Left: true, LastActivity: 3},
				abi.Chat{ID: []byte("3@s.whatsapp.net"), Name: []byte("Kept"), Locked: true, LastActivity: 2})}
		}),
		ev(abi.EventTagContacts, func(e *abi.Event) {
			e.Contacts.Contacts = []abi.Contact{
				{ID: []byte(ada), FullName: []byte("Ada Lovelace"), HasFullName: true},
				{ID: []byte(grace), FullName: []byte("Grace Hopper"), HasFullName: true},
			}
		}),
	)
	s := state.New()
	scr := New()
	bar := &selBar{}
	sh, err := shell.New(s, &screen.Set{Composer: scr, Conversation: bar, Peek: peek.New()})
	if err != nil {
		t.Fatal(err)
	}
	a := s.AddAccount("1", nil, f)
	l := widgettest.StartLive(t, shell.Config(s), sh)
	// find looks in the frame's tree; Until's conditions run on the main
	// thread, where LiveApp.Find (which hands over to it) cannot.
	var sem ui.SemTree
	find := func(role ui.Role, name string) bool {
		sh.Context().Semantics(&sem)
		for _, n := range sem.Nodes {
			if n.Role == role && string(sem.Text(n.Name)) == name {
				return true
			}
		}
		return false
	}
	says := func(text string) bool {
		sh.Context().Semantics(&sem)
		for _, n := range sem.Nodes {
			if strings.Contains(string(sem.Text(n.Name)), text) {
				return true
			}
		}
		return false
	}
	shot := func(name string) {
		if os.Getenv("GNUI_GNUISHOT") != "" {
			l.Frames(6)
			widgettest.Capture(t, l, name)
		}
	}
	emit := func(e *abi.Event) {
		l.Do(func(ap *app.App) {
			f.Emit(e)
			ap.Window().Wake()
		})
	}
	l.Until("the chats", 300, func() bool { return a.Model.Chats.Loaded() })
	l.Do(func(*app.App) { a.OpenChat(a.Model.Chats.Find([]byte(group))) })
	l.Until("the composer", 300, func() bool { return find(ui.RoleTextField, "Message") })
	if !scr.st.Hosted() {
		t.Fatal("the composer is not the hosted NSTextView")
	}
	text := func() string { return string(scr.st.Text()) } // in Until's conditions: the main thread
	var closed bool
	l.Do(func(a *app.App) {
		a.Window().Show()
		a.Window().Focus()
	})
	if !l.Within("the window becomes key", 240, func() bool { return l.App().Win.Focused() }) {
		t.Skip("the window cannot become key here")
	}
	l.Do(func(*app.App) { scr.st.Focus(scr.env.K.Kit) })
	l.Until("the composer has the keyboard", 120, func() bool { return scr.focused })
	l.Type("1 @") // a digit first: no capital for the sentence's first word
	l.Until("the suggestions", 300, func() bool { return find(ui.RoleList, "Suggestions") })
	l.Type("gr")
	l.Until("Grace alone", 300, func() bool { return find(ui.RoleListItem, "Grace Hopper") && !find(ui.RoleListItem, "Ada Lovelace") })
	l.Press(ui.KeyEnter, 0)
	l.Until("the accept", 300, func() bool { return text() == "1 @Grace Hopper " })
	if os.Getenv("GNUI_GNUISHOT") != "" {
		widgettest.Capture(t, l, "composer-mention")
	}
	// CP-006: ⌘Z (the Edit menu, to the hosted editor) takes the accept
	// back in one step; ⌘⇧Z puts it back.
	l.Press('Z', ui.PrimaryMod())
	l.Until("one undo takes the accept back", 300, func() bool { return text() == "1 @gr" })
	l.Press('Z', ui.PrimaryMod()|ui.ModShift)
	l.Until("redo puts it back", 300, func() bool { return text() == "1 @Grace Hopper " })
	var h0 float32
	l.Do(func(*app.App) { h0 = scr.lastH })
	for i := 0; i < 3; i++ {
		l.Press(ui.KeyEnter, ui.ModShift)
		l.Type("more")
	}
	l.Until("the band grows", 300, func() bool {
		return scr.lastH > h0
	})
	if os.Getenv("GNUI_GNUISHOT") != "" {
		widgettest.Capture(t, l, "composer-lines")
	}
	if len(f.SentTags()) > 0 {
		for _, c := range f.Sent() {
			if c.Tag == abi.CommandTagSendText {
				t.Fatal("Shift+Enter sent")
			}
		}
	}
	l.Press(ui.KeyEnter, 0)
	l.Until("the send", 300, func() bool {
		for _, c := range f.Sent() {
			if c.Tag == abi.CommandTagSendText {
				return true
			}
		}
		return false
	})
	for _, c := range f.Sent() {
		// The text system may capitalize a line's first word.
		if c.Tag == abi.CommandTagSendText && (!strings.EqualFold(string(c.SendText.Text), "1 @2 \nmore\nmore\nmore") || len(c.SendText.Mentions) != 1) {
			t.Errorf("sent %q mentioning %q", c.SendText.Text, c.SendText.Mentions)
		}
	}
	l.Until("the composer empties", 300, func() bool { return text() == "" })
	// CP-010: the empty composer's microphone takes the send button's place.
	l.Until("the microphone in the send button's place", 300, func() bool {
		return find(ui.RoleButton, micName) && !find(ui.RoleButton, "Send")
	})
	// KB-010: Up in the empty composer edits the last message.
	emit(ev(abi.EventTagMessages, func(e *abi.Event) {
		m := textMsg("mine1", me, true, time.Now().Unix()-60, "edit me")
		m.Chat = []byte(group)
		e.Messages.Chat = []byte(group)
		e.Messages.Messages = []abi.Message{m}
	}))
	l.Until("the message", 300, func() bool { c := a.Conversation(); return c != nil && c.Find([]byte("mine1")) >= 0 })
	l.Press(ui.KeyUp, 0)
	l.Until("Up edits it", 300, func() bool { return s.Panes.Edit.Len() == 1 && text() == "edit me" })
	l.Press(ui.KeyEscape, 0)
	l.Until("Escape stops editing", 300, func() bool { return s.Panes.Edit.Len() == 0 && text() == "" })
	l.Frames(4)
	if l.Do(func(*app.App) { closed = a.Open == 0 }); closed {
		t.Fatal("the Escape that stopped the edit closed the chat")
	}

	// The picker's emoji goes in at the caret.
	l.Do(func(a *app.App) {
		s.InsertEmoji([]byte("🎉"))
		a.Window().Wake()
	})
	l.Until("the picker's emoji", 300, func() bool { return text() == "🎉" })

	// SL-005: the selection bar takes the band, and the hosted view goes
	// with the composer; it comes back when the selection ends.
	host := uint32(scr.st.Editor().Host())
	var regs []ui.HostRegion
	shown := func() bool {
		regs = sh.Context().HostRegions(regs[:0])
		for _, h := range regs {
			if h.ID == host && !h.Hidden {
				return true
			}
		}
		return false
	}
	l.Until("the editor shows", 300, shown)
	select1 := func(on bool) {
		l.Do(func(a *app.App) {
			s.Panes.Selecting = on
			s.Touch()
			a.Window().Wake()
		})
	}
	select1(true)
	l.Until("the editor goes with the composer", 300, func() bool { return !shown() && bar.bars > 0 })
	if os.Getenv("GNUI_GNUISHOT") != "" {
		widgettest.Capture(t, l, "composer-selecting")
	}
	select1(false)
	l.Until("the editor comes back, as it was", 300, func() bool { return shown() && text() == "🎉" })

	// A peek over the window is a modal: the hosted view, which lies above
	// everything drawn, hides under it, and comes back when it closes.
	peekAt := func(open bool) {
		l.Do(func(w *app.App) {
			if open {
				s.PeekChat(a, a.Model.Chats.Find([]byte(group)), [4]float32{0, 0, 10, 10})
			} else {
				s.ClosePeek()
			}
			w.Window().Wake()
		})
	}
	peekAt(true)
	l.Until("the editor hides under the peek", 300, func() bool { return !shown() })
	peekAt(false)
	l.Until("the editor comes back after the peek", 300, shown)
	shot("compose-emoji") // CP-026

	// CP-023, CP-024: a recording (the demo's microphone: this Mac grants
	// no access to a test) takes the field's place and the hosted view
	// goes; Escape discards it; Enter sends one.
	l.Do(func(a *app.App) {
		scr.voice.newMic = func(_ bool, now func() time.Time, wake func()) mic { return newMic(true, now, wake) }
		_ = scr.st.SetText(nil)
		s.Touch()
		a.Window().Wake()
	})
	l.Until("the microphone", 300, func() bool { return find(ui.RoleButton, micName) })
	record := func() {
		l.Click(ui.RoleButton, micName)
		l.Until("the recording bar", 300, func() bool { return find(ui.RoleButton, "Discard") && !shown() })
	}
	record()
	l.Until("a second recorded", 300, func() bool { return scr.voice.m.Elapsed() > time.Second })
	if os.Getenv("GNUI_GNUISHOT") != "" {
		widgettest.Capture(t, l, "composer-recording")
	}
	l.Press(ui.KeyEscape, 0)
	l.Until("Escape discards", 300, func() bool { return !scr.voice.recording() && shown() && find(ui.RoleButton, micName) })
	record()
	l.Until("most of a second recorded", 300, func() bool { return scr.voice.m.Elapsed() > 700*time.Millisecond })
	l.Press(ui.KeyEnter, 0)
	l.Until("the voice message", 300, func() bool {
		for _, c := range f.Sent() {
			if c.Tag == abi.CommandTagSendVoice && eq(c.SendVoice.Chat, group) && c.SendVoice.Samples.Len > 4*core.VoiceRate/2 {
				return true
			}
		}
		return false
	})
	l.Until("the field is back", 300, shown)
	shot("chat") // CP-010, CP-022: the empty composer's microphone, the hints

	// CP-024: a refused voice message waits above the field; Enter in the
	// empty hosted editor tries it again (SendKey), Discard drops it.
	refuse := func() abi.BlobRef {
		t.Helper()
		blob, err := f.PutBlob(make([]byte, 4*12*core.VoiceRate))
		if err != nil {
			t.Fatal(err)
		}
		emit(ev(abi.EventTagSendRefused, func(e *abi.Event) {
			e.SendRefused.Chat = []byte(group)
			e.SendRefused.Unsent.Tag = abi.UnsentTagVoice
			e.SendRefused.Unsent.Voice.Samples = blob
		}))
		l.Until("the unsent voice message", 300, func() bool { return says("Voice message (0:12) not sent") })
		l.Until("the composer has the keyboard", 120, func() bool { return scr.focused })
		return blob
	}
	blob := refuse()
	shot("unsent-voice")
	l.Press(ui.KeyEnter, 0)
	l.Until("Enter tries it again", 300, func() bool {
		for _, c := range f.Sent() {
			if c.Tag == abi.CommandTagSendVoice && c.SendVoice.Samples.ID == blob.ID {
				return !says("not sent")
			}
		}
		return false
	})
	refuse()
	l.Click(ui.RoleButton, "Discard voice message")
	l.Until("the strip goes", 300, func() bool { return !says("not sent") })

	// CP-003, CP-016: picked files wait above the text.
	emit(ev(abi.EventTagPicked, func(e *abi.Event) {
		e.Picked.Chat = []byte(group)
		e.Picked.Paths = [][]byte{[]byte("/tmp/report.pdf"), []byte("/tmp/photo.png")}
	}))
	l.Until("the staged files", 300, func() bool { return says("report.pdf") })
	shot("staged")
	l.Press(ui.KeyEscape, 0)
	l.Until("Escape drops them", 300, func() bool { return !says("report.pdf") })

	// CV-032, LK-007: read-only chats say why.
	for _, c := range []struct{ jid, says, shot string }{
		{"g2@g.us", "You left this group", "left-group"},
		{"3@s.whatsapp.net", "Locked chats are read-only in Zap", "locked-open"},
	} {
		l.Do(func(ap *app.App) {
			if c.shot == "locked-open" {
				a.Mode = state.ListLocked // a locked chat opens only in its folder
			}
			a.OpenChat(a.Model.Chats.Find([]byte(c.jid)))
			ap.Window().Wake()
		})
		l.Until(c.says, 300, func() bool { return says(c.says) && !find(ui.RoleTextField, "Message") })
		shot(c.shot)
	}

	// CP-014: files copied in the Finder and pasted become attachments. The
	// system clipboard is shared with every other process (other test runs
	// copy to it too), and the framework reads only that one: a paste that
	// finds another process's copy there is tried once more, then the check
	// is skipped, never failed. It comes last for that. The clipboard's
	// text is put back afterwards.
	l.Do(func(ap *app.App) {
		a.OpenChat(a.Model.Chats.Find([]byte(group)))
		ap.Window().Wake()
	})
	l.Until("the group's composer", 300, func() bool { return find(ui.RoleTextField, "Message") })
	l.Do(func(*app.App) { scr.st.Focus(scr.env.K.Kit) })
	l.Until("the composer has the keyboard", 120, func() bool { return scr.focused })
	file := filepath.Join(t.TempDir(), "pasteme.txt")
	if err := os.WriteFile(file, []byte("hi"), 0o644); err != nil {
		t.Fatal(err)
	}
	var clip string
	l.Do(func(*app.App) { clip = platform.Clipboard() })
	defer l.Do(func(*app.App) { platform.SetClipboard(clip) })
	// ours reports whether the clipboard still holds the test's file.
	ours := func() bool {
		out, err := exec.Command("osascript", "-e", `POSIX path of (the clipboard as «class furl»)`).Output()
		return err == nil && strings.TrimSpace(string(out)) == file
	}
	for try := 1; ; try++ {
		if out, err := exec.Command("osascript", "-e", `set the clipboard to (POSIX file "`+file+`")`).CombinedOutput(); err != nil {
			t.Fatalf("osascript: %v %s", err, out)
		}
		l.Press('V', ui.PrimaryMod())
		if l.Within("the pasted file is staged", 300, func() bool { return says("pasteme.txt") && text() == "" }) {
			break
		}
		var got string
		var staged int
		l.Do(func(*app.App) { got, staged = text(), len(s.Staged) })
		if ours() && got == "" {
			t.Fatalf("the Finder paste left text %q and %d staged", got, staged)
		}
		// Another process copied meanwhile: what it copied is dropped.
		l.Do(func(ap *app.App) {
			_ = scr.st.SetText(nil)
			s.Staged = s.Staged[:0]
			ap.Window().Wake()
		})
		if try == 2 {
			t.Skipf("the system clipboard changed under the Finder paste twice (another process copied to it); CP-014 not checked this run")
		}
	}
	shot("finder-paste")
	l.Press(ui.KeyEscape, 0)
	l.Until("Escape drops it", 300, func() bool { return !says("pasteme.txt") })
}
