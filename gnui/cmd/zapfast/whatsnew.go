//go:build !gnui_release

package main

import (
	"gonativeui/ui"
	"gonativeui/widget/script"

	"zapfast/state"
)

// whatsNew is the what's-new tour (zf:src/demo/tour/whats_new.rs, 86 s)
// as far as the Go screens and the demo core's data go: the plus menu and
// the poll dialog, the chat search pane and its calendar, sticker shelves and their search, a
// group message's info, the chips and a chat's menu, a voice message's
// speed, the folded list, selecting messages and Forward, and the light
// theme.
func (t *tour) whatsNew() []script.Step {
	prim := ui.PrimaryMod()
	click, key, pause, move := script.Click, script.Key, script.Pause, script.Move
	esc := func(label string) []script.Step { return t.label("Esc · "+label, key(ui.KeyEscape, 0)) }
	var st []script.Step
	add := func(steps ...script.Step) { st = append(st, steps...) }
	add(script.Idle(20))
	// The rounded composer and its plus menu.
	add(pause(800), move(ui.RoleButton, "Attach"), pause(400))
	add(click(ui.RoleButton, "Attach"), pause(1200))
	add(move(ui.RoleButton, "Create poll"), pause(400))
	add(click(ui.RoleButton, "Create poll"), pause(2200))
	add(esc("Cancel the poll")...)
	add(pause(700))
	// Search inside the chat.
	// (the demo core does not answer a search: the pane and its calendar)
	add(t.label("⌘ + F · Search this chat", key('F', prim))...)
	add(pause(1600))
	add(move(ui.RoleButton, "Filter by date"), pause(400))
	add(click(ui.RoleButton, "Filter by date"), pause(1500))
	add(esc("Close the calendar")...)
	add(pause(600))
	add(esc("Close search")...)
	add(pause(600))
	// Sticker shelves and search.
	add(move(ui.RoleButton, "Emoji, GIFs, and stickers"), pause(400))
	add(click(ui.RoleButton, "Emoji, GIFs, and stickers"), pause(600))
	add(move(ui.RoleTab, "Stickers"), pause(400))
	add(click(ui.RoleTab, "Stickers"), pause(1300))
	add(move(ui.RoleTab, "Favorites"), pause(400))
	add(click(ui.RoleTab, "Favorites"), pause(1200))
	add(move(ui.RoleTab, "Received"), pause(400))
	add(click(ui.RoleTab, "Received"), pause(1200))
	add(move(ui.RoleSearchField, "Search by emoji, word, or pack"), pause(400))
	add(click(ui.RoleSearchField, "Search by emoji, word, or pack"), pause(300))
	add(typing("🐸", 70)...)
	add(pause(1500), key(ui.KeyBackspace, 0), pause(200))
	add(typing("launch", 70)...) // the shared pack's name (upstream: "bom dia")
	add(pause(1300))
	add(esc("Close stickers")...)
	add(pause(600))
	// Who read a group message.
	add(move(ui.RoleListItem, "Rust Berlin"), pause(400))
	add(click(ui.RoleListItem, "Rust Berlin"), pause(800))
	add(script.RClick(ui.RoleLabel, "I'll be there around 19:00"), pause(700))
	// the menu's: each of our bubbles' time and ticks is a Message info
	// button too, and both of ours in view come first
	add(move(ui.RoleButton, "Message info").Nth(2), pause(400))
	add(click(ui.RoleButton, "Message info").Nth(2), pause(3100))
	add(esc("Close message info")...)
	add(pause(600))
	// The chips (Favorites folds into More at the sidebar's width, whose
	// native menu a script cannot click), and a chat's menu.
	add(move(ui.RoleCheckbox, "Unread"), pause(400))
	add(click(ui.RoleCheckbox, "Unread"), pause(1600))
	add(move(ui.RoleCheckbox, "All"), pause(400))
	add(click(ui.RoleCheckbox, "All"), pause(600))
	add(move(ui.RoleListItem, "Katherine Johnson"), pause(400))
	add(script.RClick(ui.RoleListItem, "Katherine Johnson"), pause(2200))
	add(esc("Close the menu")...)
	add(pause(600))
	// A voice message's playback speed.
	add(move(ui.RoleListItem, "Grace Hopper"), pause(400))
	add(click(ui.RoleListItem, "Grace Hopper"), pause(800))
	add(script.RClick(ui.RoleSlider, "Voice message"), pause(700))
	add(move(ui.RoleButton, "Playback speed 1.75x"), pause(400))
	add(click(ui.RoleButton, "Playback speed 1.75x"), pause(900))
	// The chat list folds to avatars with unread badges.
	add(t.label("⌘ + B · Collapse the chat list", key('B', prim))...)
	add(pause(2400))
	add(t.label("⌘ + B · Expand the chat list", key('B', prim))...)
	add(pause(700))
	// Several messages selected and forwarded at once.
	add(script.RClick(ui.RoleLabel, "Found the bug. It was a moth."), pause(600))
	add(move(ui.RoleButton, "Select"), pause(400))
	add(click(ui.RoleButton, "Select"), pause(800))
	// two more ticked by their checks (they come after the filter chips,
	// four at the sidebar's width: three and More; Grace's chat has four
	// messages)
	add(move(ui.RoleCheckbox, "").Nth(6), pause(400))
	add(click(ui.RoleCheckbox, "").Nth(6), pause(500))
	add(move(ui.RoleCheckbox, "").Nth(7), pause(300))
	add(click(ui.RoleCheckbox, "").Nth(7), pause(800))
	add(move(ui.RoleButton, "Forward…"), pause(400))
	add(click(ui.RoleButton, "Forward…"), pause(2200))
	add(esc("Close")...)
	add(pause(600))
	add(esc("Cancel the selection")...)
	add(pause(800))
	// The light theme (upstream: through Settings).
	add(script.Do(func() {
		t.s.Settings.Theme = state.ThemeLight
		t.s.Touch()
	}), pause(2800))
	return st
}
