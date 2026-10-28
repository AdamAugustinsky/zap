//go:build !gnui_release

package main

import (
	"path/filepath"
	"testing"
	"time"

	"zapfast/core"
	"zapfast/state"
	"zapfast/ui/shell"
)

// Every form package flag accepts names the library.
func TestCoreFlagInEveryFlagForm(t *testing.T) {
	for _, args := range [][]string{
		{"-core", "/x/lib.dylib"},
		{"--core", "/x/lib.dylib"},
		{"-core=/x/lib.dylib"},
		{"--core=/x/lib.dylib"},
		{"-demo", "-core=/x/lib.dylib"},
	} {
		o, err := parseOptions(args)
		if err != nil || o.core != "/x/lib.dylib" {
			t.Errorf("%q: core %q, %v", args, o.core, err)
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
// cores does not panic (the failed open's nil *Core became a non-nil Conn,
// and closing it dereferenced nil).
func TestAnEarlyExitWithACoreThatFailedToOpen(t *testing.T) {
	s := state.New()
	p := core.OpenAsync(core.Config{Library: filepath.Join(t.TempDir(), "missing.dylib"), Demo: true, Account: "1"})
	if !p.Wait(5 * time.Second) {
		t.Fatal("the open did not end")
	}
	if _, err := p.Result(); err == nil {
		t.Fatal("a missing library opened")
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
