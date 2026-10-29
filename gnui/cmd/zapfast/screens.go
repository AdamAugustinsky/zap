package main

import (
	"zapfast/logic"
	"zapfast/state"
	"zapfast/sys"
	"zapfast/theme"
	"zapfast/ui/accounts"
	"zapfast/ui/calls"
	"zapfast/ui/chatlist"
	"zapfast/ui/chatview"
	"zapfast/ui/composer"
	"zapfast/ui/conversation"
	"zapfast/ui/cropper"
	"zapfast/ui/dialogs"
	"zapfast/ui/info"
	"zapfast/ui/lock"
	"zapfast/ui/login"
	"zapfast/ui/msginfo"
	"zapfast/ui/peek"
	"zapfast/ui/picker"
	"zapfast/ui/screen"
	"zapfast/ui/settings"
	"zapfast/ui/spotlight"
	"zapfast/ui/stickermaker"
	"zapfast/ui/viewer"
)

// screens wires every screen package into its slot: the one place the app
// names them (ui/CONTRACT.md). A screen's owner changes only its own line
// here, if its entry point changes at all. The demo keeps the themes out of
// the app's data folder.
func screens(demo bool) *screen.Set {
	var th *theme.Theme
	if demo {
		th = theme.NewIn("", false)
	} else {
		th = theme.New()
	}
	ls := lock.New()
	wireLock(ls)
	st := settings.New()
	settings.LoginItem = sys.LoginItem
	return &screen.Set{
		Login:    login.New(),
		Lock:     ls,
		Accounts: accounts.New(),

		ChatList:     chatlist.New(),
		Conversation: conversation.New(),
		Search:       conversation.NewSearch(),
		Composer:     composer.New(),
		Info:         info.New(),
		MessageInfo:  msginfo.New(),
		CallsPage:    calls.NewPage(),
		CallsContent: calls.NewContent(),
		CallBar:      calls.NewBar(),

		Picker:     picker.New(),
		Dialogs:    dialogs.New(),
		Spotlight:  spotlight.New(),
		Peek:       peek.New(),
		PeekWindow: peek.NewWindow(),
		// the split view's second pane: the same screens, their own
		Conversation2: conversation.NewPane(),
		Composer2:     composer.NewPane(" 2"),
		PipWindow:     chatview.NewPip(),
		Cropper:       cropper.New(),
		StickerMaker:  stickermaker.New(),

		Settings: st,
		Viewer:   viewer.New(),
		Call:     calls.NewWindow(),

		Theme:  th,
		System: newSys(ls),
		Logic:  logic.New(),

		Updates: updates{st.Updates()},
		Tickers: []screen.Ticker{&avTicker{}, st.Updates()}, // the updater: selfupdate.Started after the first frames
	}
}

// updates is the settings' updater in the shell's slot: the daily check,
// and the version the banner announces.
type updates struct{ u *settings.Updates }

func (u updates) CheckIfDue(s *state.Settings) { u.u.CheckIfDue(s) }

func (u updates) Available() (string, bool) {
	r, ok := u.u.Available()
	return r.Version, ok
}
