//go:build darwin

package main

import (
	"os"
	"testing"

	"gonativeui/ui"
	"gonativeui/widget/widgettest"

	"zapfast/core/fake"
	"zapfast/state"
	"zapfast/ui/shell"
)

// ⌘K opens Spotlight while the composer's hosted NSTextView has the
// keyboard: a key the text view holds reaches the app only as a menu item's
// key equivalent, so Search everything must be in the bar. It opens a
// window, so it runs only with ZAPFAST_LIVE=1.
func TestLiveCmdKFromTheComposer(t *testing.T) {
	if os.Getenv("ZAPFAST_LIVE") == "" {
		t.Skip("opens a window: set ZAPFAST_LIVE=1")
	}
	f := fake.New(nil)
	linked(f)
	s := state.New()
	sh, err := shell.New(s, testScreens(true))
	if err != nil {
		t.Fatal(err)
	}
	a := s.AddAccount("1", nil, f)
	l := widgettest.StartLive(t, shell.Config(s), sh)
	l.Until("the chats", 300, func() bool { return a.Model.Chats.Loaded() })
	l.Frames(10)
	l.Click(ui.RoleListItem, "Grace Hopper")
	l.Frames(10)
	l.Type("hi")
	l.Frames(5)
	l.Press(ui.KeyCode('K'), ui.ModSuper)
	l.Until("Spotlight opens", 60, func() bool { return s.Spotlight })
}
