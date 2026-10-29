package sys

import (
	"gonativeui/app"
	"gonativeui/app/tray"
	"gonativeui/menu"

	"zapfast/i18n"
	"zapfast/state"
)

// The status item (NT-009, NT-010, NT-012): a template icon in the menu bar
// whose menu shows or hides the window, locks the app while a password is
// set, and quits. Its menu never names a chat. Clicking the icon opens the
// menu, as upstream's macOS item does.

// The status menu's commands. The shell's are below 100.
const (
	cmdTrayShow menu.ID = 1001 + iota
	cmdTrayLock
	cmdTrayQuit
)

type trayState struct {
	item     statusItem
	model    *menu.Model
	lockable bool // the menu with Lock ZapFast is the one shown
}

func trayModel() (*menu.Model, error) {
	T := i18n.T
	cmds := []menu.Command{
		{ID: cmdTrayShow, Title: T("Show or hide Zap")},
		{ID: cmdTrayLock, Title: T("Lock Zap")},
		{ID: cmdTrayQuit, Title: T("Quit")},
	}
	item := func(id menu.ID) menu.Item { return menu.Item{Kind: menu.ItemCommand, Command: id} }
	sep := menu.Item{Kind: menu.ItemSeparator}
	return menu.NewModel(cmds, []menu.Menu{
		{Title: "Zap", Items: []menu.Item{item(cmdTrayShow), sep, item(cmdTrayQuit)}},
		{Title: "Zap", Items: []menu.Item{item(cmdTrayShow), item(cmdTrayLock), sep, item(cmdTrayQuit)}},
	}, nil)
}

// startTray makes the item. Where there is no status area (another OS, a run
// outside the app) the app runs without one.
func (y *System) startTray() {
	if y.makeItem == nil {
		return
	}
	m, err := trayModel()
	if err != nil {
		logWarn("the status menu", "err", err)
		return
	}
	it, err := y.makeItem(tray.Options{Icon: tray.SymbolIcon("bubble.left.fill"), Tooltip: "Zap"})
	if err != nil {
		return
	}
	y.t.item, y.t.model = it, m
	y.t.lockable = y.s != nil && len(y.s.Settings.AppLockHash) > 0
	_ = it.SetMenu(m, y.menuIndex())
}

func (y *System) menuIndex() int {
	if y.t.lockable {
		return 1
	}
	return 0
}

// syncTrayMenu offers Lock ZapFast exactly while an app lock password is
// set, so setting or removing one changes the menu at once.
func (y *System) syncTrayMenu(s *state.State) {
	if y.t.item == nil || s == nil {
		return
	}
	if lockable := len(s.Settings.AppLockHash) > 0; lockable != y.t.lockable {
		y.t.lockable = lockable
		_ = y.t.item.SetMenu(y.t.model, y.menuIndex())
	}
}

// command handles the status menu's commands.
func (y *System) command(e *app.Event) bool {
	if y.peekCommand(e) {
		return true
	}
	switch menu.ID(e.ID) {
	case cmdTrayShow:
		// Show or hide: a window that is shown hides, and keeps the app
		// linked in the background; anything else shows it.
		if w := y.mainFn(); w != nil && y.shown {
			y.shown = false
			w.Hide()
			return true
		}
		y.showMain()
		return true
	case cmdTrayLock:
		if y.LockNow != nil && len(y.s.Settings.AppLockHash) > 0 {
			y.LockNow(y.s)
		}
		return true
	case cmdTrayQuit:
		y.quit()
		return true
	}
	return false
}
