package dialogs

import (
	"gonativeui/rexui"
	"gonativeui/theme"
	"gonativeui/ui"
	"gonativeui/widget"

	"zapfast/i18n"
	"zapfast/state"
	"zapfast/ui/screen"
)

// SourceURL and the other links of About.
const (
	SourceURL       = "https://github.com/crmne/zapfast"
	WhatsAppRustURL = "https://github.com/oxidezap/whatsapp-rust"
	AuthorURL       = "https://paolino.me"
)

// openURL opens a link in the default browser, off the main thread.
func openURL(st *state.State, url string) {
	sh := st.Shell
	go func() { _ = sh.Open(url) }()
}

// about is the About dialog (zf:src/ui/dialogs.rs:869).
func (s *Screen) about(e *screenEnv, dl *rexui.Dialog) {
	k := e.K
	rexui.Label(k.Kit, "Zap", rexui.LabelOpts{Style: rexui.TextTitle2})
	if s.paras[2] == "" {
		s.paras[2] = i18n.Format(i18n.T("Version {version}"), "{version}", state.Version)
	}
	rexui.Label(k.Kit, s.paras[2], rexui.LabelOpts{Style: rexui.TextCallout, Color: secondary})
	para(k, i18n.T("A native WhatsApp client written in Go with gonativeui. It connects through whatsapp-rust. Messages are end-to-end encrypted on this device."), rexui.TextBody, rexui.Tint{})
	para(k, i18n.T("This is an unofficial client. Using it may be against WhatsApp's terms of service and could get an account suspended."), rexui.TextCallout, secondary)

	k.NextGap(8)
	k.BeginContainer(ui.AxisX, ui.CrossCenter, "links", ui.Pct(1), ui.Children(1), 0)
	if rexui.Link(k.Kit, i18n.T("Source code"), rexui.LinkOpts{}).Clicked {
		openURL(e.S, SourceURL)
	}
	rexui.Label(k.Kit, "·", rexui.LabelOpts{Color: tertiary})
	if rexui.Link(k.Kit, "whatsapp-rust", rexui.LinkOpts{}).Clicked {
		openURL(e.S, WhatsAppRustURL)
	}
	k.EndContainer()

	// "Built with love by {name}": the name is a link.
	sentence := i18n.T("Built with love by {name}")
	before, after := splitName(sentence)
	k.NextGap(0)
	k.BeginContainer(ui.AxisX, ui.CrossCenter, "credit", ui.Pct(1), ui.Children(1), 0)
	rexui.Label(k.Kit, e.C().Fmt().Str("♥  ").Str(before).String(), rexui.LabelOpts{Color: secondary})
	if rexui.Link(k.Kit, "Carmine Paolino", rexui.LinkOpts{}).Clicked {
		openURL(e.S, AuthorURL)
	}
	if after != "" {
		rexui.Label(k.Kit, after, rexui.LabelOpts{Color: secondary})
	}
	k.EndContainer()

	beginActions(e)
	dl.Action(i18n.T("Close"), rexui.ButtonPrimary)
	endActions(e)
}

// splitName splits a sentence around its {name} placeholder.
func splitName(s string) (before, after string) {
	const ph = "{name}"
	for i := 0; i+len(ph) <= len(s); i++ {
		if s[i:i+len(ph)] == ph {
			return s[:i], s[i+len(ph):]
		}
	}
	return s, ""
}

// ---- keyboard shortcuts ----

// The shortcuts dialog's width with its two columns, and with one.
const (
	shortcutsWide   = 920
	shortcutsNarrow = 540
	shortcutsGutter = 32
	shortcutsKeys   = 150
)

// shortcutsWidth is the dialog's width in a window winW points wide: two
// columns where they fit, one otherwise, and never wider than the window.
func shortcutsWidth(winW float32) float32 {
	room := winW - 64
	if room >= shortcutsWide {
		return shortcutsWide
	}
	return max(min(shortcutsNarrow, room), 180)
}

// barGutterPt is the room left at the end of a scrolled list for the
// overlay scroll bar, in points.
const barGutterPt = 14

// shortcuts lists the 29 shortcuts with this platform's key names, and the
// switch that brings back the hint bar under the message box.
func (s *Screen) shortcuts(e *screenEnv, dl *rexui.Dialog) {
	k := e.K
	winW, winH := winPt(e)
	h := winH
	wide := width(state.DialogShortcuts, winW) >= shortcutsWide
	list := screen.Shortcuts[:]
	cols := 1
	if wide {
		cols = 2
	}
	per := (len(list) + cols - 1) / cols
	scrollH := max(h-190, 120)
	k.NextHeight(k.Fixed(scrollH))
	v := k.BeginScroll("shortcuts", &s.scroll, widget.ScrollVertical)
	k.NextGap(shortcutsGutter)
	// a gutter at the end for the overlay scroll bar, which would otherwise
	// lie over the descriptions' last letters
	k.NextPadding(0, 0, barGutterPt, 0)
	k.BeginContainer(ui.AxisX, ui.CrossStart, "columns", ui.Pct(1), ui.Children(1), 0)
	for col := 0; col < cols; col++ {
		lo, hi := col*per, min((col+1)*per, len(list))
		k.NextGap(8)
		k.BeginContainer(ui.AxisY, ui.CrossStart, e.C().Fmt().Str("column").Int(col).String(), ui.Pct(1), ui.Children(1), 0)
		for i := lo; i < hi; i++ {
			k.NextGap(18)
			k.BeginContainer(ui.AxisX, ui.CrossStart, e.C().Fmt().Str("row").Int(i).String(), ui.Pct(1), ui.Children(1), 0)
			k.NextWidth(k.Fixed(shortcutsKeys))
			rexui.Label(k.Kit, list[i].Keys, rexui.LabelOpts{Style: rexui.TextSemibold})
			k.NextWidth(widget.Fill)
			rexui.Label(k.Kit, i18n.T(list[i].What), rexui.LabelOpts{Style: rexui.TextBody, Color: secondary, Wrap: true})
			k.EndContainer()
		}
		k.EndContainer()
	}
	k.EndContainer()
	k.EndScroll(v)

	// The hint bar's × hides it; this brings it back.
	hints := e.S.Settings.ShowShortcutHints
	// BeginDialog bounds the parent's width, so the label wraps within it.
	label := e.C().Fmt().Str(i18n.T("Show shortcut hints under the message box")).Str("###hints").String()
	if rexui.Checkbox(k.Kit, label, &hints, rexui.CheckboxOpts{Wrap: true}).Changed {
		e.S.Settings.ShowShortcutHints = hints
		e.S.Touch()
	}
	beginActions(e)
	dl.Action(i18n.T("Close"), rexui.ButtonPrimary)
	endActions(e)
}

var _ = theme.Label
