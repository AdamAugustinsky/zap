//go:build !gnui_release

package main

import (
	"errors"
	"testing"
	"time"

	"zapfast/core"
	"zapfast/state"
	"zapfast/ui/shell"
)

// Every form package flag accepts parses, and an unknown flag (the removed
// -core and -engine among them) is refused.
func TestFlagsInEveryForm(t *testing.T) {
	for _, args := range [][]string{{"-o", "/x/out.png"}, {"--o", "/x/out.png"}, {"-o=/x/out.png"}, {"--o=/x/out.png"}} {
		o, err := parseOptions(args)
		if err != nil || o.out != "/x/out.png" {
			t.Errorf("%q: out %q, %v", args, o.out, err)
		}
	}
	for _, args := range [][]string{{"-core=/x/lib.dylib"}, {"-engine=rust"}} {
		if _, err := parseOptions(args); err == nil {
			t.Errorf("%q was accepted", args)
		}
	}
	if o, err := parseOptions([]string{"-demo", "--start-hidden"}); err != nil || !o.demo {
		t.Errorf("-demo --start-hidden: %+v, %v", o, err)
	}
	if _, err := parseOptions([]string{"-nope"}); err == nil {
		t.Error("an unknown flag was accepted")
	}
}

// Every screen package is wired into its slot.
func TestEverySlotIsWired(t *testing.T) {
	s := testScreens(true)
	for name, slot := range map[string]any{
		"Login": s.Login, "Lock": s.Lock, "Accounts": s.Accounts, "ChatList": s.ChatList,
		"Conversation": s.Conversation, "Search": s.Search, "Composer": s.Composer, "Info": s.Info,
		"MessageInfo": s.MessageInfo, "CallsPage": s.CallsPage, "Picker": s.Picker, "Dialogs": s.Dialogs,
		"Cropper": s.Cropper, "StickerMaker": s.StickerMaker, "Settings": s.Settings, "Viewer": s.Viewer,
		"Call": s.Call, "Theme": s.Theme, "System": s.System, "Logic": s.Logic,
	} {
		if slot == nil {
			t.Errorf("%s is not wired", name)
		}
	}
}

// A way out before the app ever started (here -bench without -demo; a
// startup errors go the same way), with
// a core that failed to open: the status is the error's, and closing the
// cores does not panic (a failed open once became a non-nil Conn, and
// closing it dereferenced nil).
func TestAnEarlyExitWithACoreThatFailedToOpen(t *testing.T) {
	s := state.New()
	p := core.OpenAsync(nil, func(*core.Timing) (core.Conn, error) { return nil, errors.New("no storage library") })
	if !p.Wait(5 * time.Second) {
		t.Fatal("the open did not end")
	}
	if c, err := p.Conn(); err == nil || c != nil {
		t.Fatal("a failed open has a core")
	}
	s.AddAccount("1", p, nil)
	sh, err := shell.New(s, testScreens(false))
	if err != nil {
		t.Fatal(err)
	}
	h := shell.NewApp(sh)
	var o options
	o.budget.bench = true
	if code := runBudgets(&o, s, sh, h); code != 1 {
		t.Errorf("status %d, want 1", code)
	}
	if a := s.Accounts[0]; a.Conn != nil || a.Pending != nil {
		t.Errorf("after closing: conn %v, pending %v", a.Conn, a.Pending)
	}
	closeCores(s) // again, as main's deferred call would
}
