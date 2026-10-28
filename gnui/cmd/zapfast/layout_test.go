//go:build !gnui_release

package main

import (
	"image/png"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"gonativeui/app"
	"gonativeui/ui"
	"gonativeui/widget/widgettest"

	"zapfast/core"
	"zapfast/i18n"
	"zapfast/state"
	"zapfast/ui/screen"
)

// sweepPage is a demo page the layout sweep shows: its parts, whether it
// is heavy on buttons (French too), and the window it is about ("" the
// main window, "viewer", "call"; "call-active" answers the call first).
type sweepPage struct {
	name, parts string
	french      bool
	window      string
}

// sweepPages cover every screen: the list and its filters, a 1:1 and a
// group chat, selection, the composer's chips, the reply and edit bars,
// the info panes, message info, the picker, the dialogs, the viewer, the
// calls, the lock screen and linking.
var sweepPages = []sweepPage{
	{name: "chat", parts: "chat"},
	{name: "list-unread", parts: "empty,unread"},
	{name: "list-archived", parts: "empty,archived"},
	{name: "starred", parts: "empty,starred"},
	{name: "group-chat", parts: "typers"},
	{name: "select", parts: "select-delete", french: true},
	{name: "staged", parts: "staged"},
	{name: "drafts", parts: "drafts"},
	{name: "reply", parts: "reply"},
	{name: "private-reply", parts: "private-reply"},
	{name: "edit", parts: "edit"},
	{name: "chat-search", parts: "chat-search", french: true},
	{name: "info", parts: "info", french: true},
	{name: "group-info", parts: "group-info", french: true},
	{name: "message-info", parts: "message-info", french: true},
	{name: "picker", parts: "picker"},
	{name: "gifs", parts: "gifs"},
	{name: "stickers", parts: "stickers"},
	{name: "poll-create", parts: "poll-create", french: true},
	{name: "new-contact", parts: "new-contact", french: true},
	{name: "location", parts: "location", french: true},
	{name: "labels", parts: "labels-dialog", french: true},
	{name: "about", parts: "about", french: true},
	{name: "shortcuts", parts: "shortcuts"},
	{name: "delete-message", parts: "delete-message", french: true},
	{name: "delete-chat", parts: "delete-chat", french: true},
	{name: "forward", parts: "forward", french: true},
	{name: "new-chat", parts: "new-chat", french: true},
	{name: "delete-selection", parts: "delete-selection", french: true},
	{name: "leave-group", parts: "leave-group", french: true},
	{name: "unlink", parts: "unlink", french: true},
	{name: "clear-media", parts: "clear-media", french: true},
	{name: "locked-prompt", parts: "locked-prompt", french: true},
	{name: "sticker-pack-view", parts: "sticker-pack-view"},
	{name: "poll-results", parts: "poll-results"},
	{name: "shared-contact", parts: "shared-contact"},
	{name: "reactions", parts: "reactions"},
	{name: "video", parts: "video"},
	{name: "info-media", parts: "info-media", french: true},
	{name: "account-menu", parts: "empty,account-menu", french: true},
	{name: "toasts", parts: "toasts"},
	{name: "offline", parts: "disconnected"},
	{name: "syncing", parts: "syncing"},
	{name: "viewer", parts: "preview", french: true, window: "viewer"},
	{name: "calls", parts: "calls", french: true},
	{name: "call-ringing", parts: "call", french: true, window: "call"},
	{name: "call-active", parts: "call", french: true, window: "call-active"},
	{name: "lock", parts: "app-lock", french: true},
	{name: "login", parts: "login", french: true},
	{name: "pair", parts: "pair", french: true},
}

// sweepSizes are the default window and the smallest it goes (shell.Config).
var sweepSizes = []struct {
	name string
	w, h float32
}{{"default", 0, 0}, {"min", 600, 400}}

// sweepAllow are findings that are right as they are.
var sweepAllow = []widgettest.Allow{{
	// The faces of who is typing in a group overlap on purpose, a stack
	// as upstream draws it (Ada's and Katherine's in the demo's group).
	Check: ui.AuditCovered, Where: `"AL" in ListItem in List "Messages"`, Other: `"KJ" in ListItem in List "Messages"`,
	Reason: "the typers' avatars are stacked",
}, {
	// The day of the messages at the top floats over them while the
	// transcript is scrolled, as upstream's sticky date (a label right in
	// the window over a message of the list).
	Check: ui.AuditCovered, Where: `in ListItem in List "Messages"`, Other: `" in Window`,
	Reason: "the sticky day pill floats over the scrolled messages",
}}

// knownLayoutIssue is a real finding in another package, on the pages
// named (a page name prefix), until its owner fixes it: the sweep fails on
// anything new, and the list shrinks.
type knownLayoutIssue struct {
	owner string
	pages []string
	allow widgettest.Allow
}

// prefix is the first n runes of s, as the audit names a long text.
func prefix(s string, n int) string {
	for i := range s {
		if n == 0 {
			return s[:i]
		}
		n--
	}
	return s
}

// knownLayoutIssues are the open findings, in the current language.
func knownLayoutIssues() []knownLayoutIssue {
	return nil
}

// allowedOn is sweepAllow and the known issues of page.
func allowedOn(page string) []widgettest.Allow {
	out := append([]widgettest.Allow(nil), sweepAllow...)
	for _, k := range knownLayoutIssues() {
		for _, p := range k.pages {
			if strings.HasPrefix(page, p) {
				out = append(out, k.allow)
				break
			}
		}
	}
	return out
}

// The whole app in its longest languages (German, Russian; French for the
// pages heavy on buttons) and in English, at the default window size and at
// the smallest: the framework's layout audit finds no text past its box, no
// controls on top of each other, no clipped buttons (the settings window
// has its own, ui/settings). It needs the demo core (ZAPFAST_CORE);
// ZAPFAST_SWEEP_SHOTS=dir saves each page's picture there.
func TestEveryScreenFitsInTheLongestLanguages(t *testing.T) {
	if testing.Short() {
		t.Skip("the layout sweep renders some 250 pages")
	}
	if _, err := core.ResolveLibrary(""); err != nil && os.Getenv("ZAPFAST_CORE") == "" {
		t.Skip("no core library:", err)
	}
	only := os.Getenv("ZAPFAST_SWEEP_ONLY")
	for _, p := range sweepPages {
		langs := []i18n.Locale{i18n.English, i18n.German, i18n.Russian}
		if p.french {
			langs = append(langs, i18n.French)
		}
		for _, lang := range langs {
			for _, sz := range sweepSizes {
				name := p.name + "/" + lang.Tag() + "/" + sz.name
				if only != "" && !strings.Contains(name, only) {
					continue
				}
				t.Run(name, func(t *testing.T) { sweep(t, name, p, lang, sz.w, sz.h) })
			}
		}
	}
}

// sweep shows page p in lang at w×h points (0: the default) and audits it.
func sweep(t *testing.T, name string, p sweepPage, lang i18n.Locale, w, h float32) {
	old := i18n.Current()
	i18n.SetLocale(lang)
	t.Cleanup(func() { i18n.SetLocale(old) })
	r := newRigSized(t, demoCore(t), w, h)
	page := newDemoPage(p.parts)
	r.sh.Set.Tickers = append(r.sh.Set.Tickers, page)
	for i := 0; i < 600 && (!page.done || page.late); i++ {
		time.Sleep(2 * time.Millisecond)
		r.Frames(1)
	}
	for range 12 { // the core's own updates after the page, pictures
		time.Sleep(5 * time.Millisecond)
		r.Frames(2)
	}
	audited := r.Rig
	if p.window != "" {
		audited = sweepWindow(t, r, p.window, w > 0)
	}
	if dir := os.Getenv("ZAPFAST_SWEEP_SHOTS"); dir != "" {
		saveShot(t, filepath.Join(dir, strings.ReplaceAll(name, "/", "-")+".png"), audited)
	}
	widgettest.AuditLayout(t, name, audited.Context(), allowedOn(p.name)...)
}

// sweepWindow opens the page's own window (the viewer, the call) beside
// the main one, offscreen at its default size or its smallest, and lets it
// settle while the main window pumps the core.
func sweepWindow(t *testing.T, r *rig, which string, small bool) *widgettest.Rig {
	t.Helper()
	win, scr := state.WinViewer, r.sh.Set.Viewer
	if which != "viewer" {
		win, scr = state.WinCall, r.sh.Set.Call
	}
	for i := 0; i < 200 && !r.s.Win.Want[win]; i++ {
		time.Sleep(5 * time.Millisecond)
		r.Frames(1)
	}
	if !r.s.Win.Want[win] {
		t.Fatalf("the page did not ask for its window (%s)", which)
	}
	r.s.Win.Want[win], r.s.Win.Open[win] = false, true
	wc := scr.Config(r.s)
	cfg := app.Config{Title: wc.Title, Width: wc.Width, Height: wc.Height, MinWidth: wc.MinWidth, MinHeight: wc.MinHeight, Materials: wc.Materials}
	if small && wc.MinWidth > 0 && wc.MinHeight > 0 {
		cfg.Width, cfg.Height = wc.MinWidth, wc.MinHeight
	}
	wr := widgettest.Open(t, cfg, int(cfg.Width), int(cfg.Height), 1, screen.NewWindow(r.s, win, scr, r.sh.Set.Theme, r.sh))
	wr.Offscreen().UI.SetSystemAppearance(ui.Appearance{Known: true})
	step := func(n int) {
		for range n {
			time.Sleep(5 * time.Millisecond)
			r.Frames(1)
			wr.Frames(1)
		}
	}
	step(30)
	if which == "call-active" {
		wr.Click(ui.RoleButton, i18n.T("Accept"))
		step(40)
	}
	return wr
}

func saveShot(t *testing.T, path string, r *widgettest.Rig) {
	t.Helper()
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if err := png.Encode(f, r.Image()); err != nil {
		t.Fatal(err)
	}
}
