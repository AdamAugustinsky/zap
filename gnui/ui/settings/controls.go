package settings

import (
	"gonativeui/rexui"
	"gonativeui/theme"
	"gonativeui/ui"

	"zapfast/abi"
	"zapfast/i18n"
	"zapfast/state"
	"zapfast/ui/screen"
)

// popup builds a pop-up button named title of width pt points over options,
// showing sel; it returns the new choice and whether it changed.
func (s *Screen) popup(e *screen.Env, title string, sel int, options []string, pt float32) (int, bool) {
	k := e.K
	k.Kit.NextWidth(k.Kit.Fixed(s.ctrlW(e, pt)))
	return sel, k.Select(title, &sel, options).Changed
}

// ctrlW is the width in points of a control that is pt wide in a roomy window:
// at most about two fifths of the row, so that in a narrow window the label and
// its description keep a column to wrap in (upstream's rows are one line of
// egui: its controls never take more than the room there is).
func (s *Screen) ctrlW(e *screen.Env, pt float32) float32 {
	return min(pt, max(120, 0.42*s.innerPx/e.K.Scale()))
}

// button builds a push button; it reports a click.
func button(e *screen.Env, label string) bool {
	return rexui.Button(e.K.Kit, label, rexui.ButtonOpts{}).Clicked
}

// control builds the control of row d at the trailing edge of its row.
func (s *Screen) control(e *screen.Env, d *rowDef, title string) {
	k := e.K.Kit
	st := &e.S.Settings
	a := e.Acct()
	switch d.id {
	case rTheme:
		sel := s.themes.index(st)
		if v, ok := s.popup(e, title, sel, s.themes.opts, d.ctrl); ok {
			s.themes.choose(e, v)
		}
	case rFont:
		if v, ok := s.popup(e, title, int(st.Font), s.tx.font[:], d.ctrl); ok {
			st.Font = state.Font(v)
			s.changed(e)
		}
	case rLanguage:
		if v, ok := s.popup(e, title, languageIndex(st), s.tx.lang[:], d.ctrl); ok {
			s.setLanguage(e, v)
		}
	case rTransparency:
		s.flip(e, title, &st.Transparency)
	case rZoom:
		s.zoom(e)

	case rEnterSends:
		s.flip(e, title, &st.EnterSends)
	case rAutoDownload:
		if s.flip(e, title, &a.Settings.AutoDownload) {
			s.changed(e)
		}
	case rHistory:
		if s.flip(e, title, &a.Settings.HistoryPrefetch) {
			s.sendPrefetch(e, a)
		}
	case rKeepArchived:
		if s.flip(e, title, &st.KeepChatsArchived) {
			s.eachAccount(e, func(a *state.Account) {
				cmd := a.Cmd(abi.CommandTagSetKeepChatsArchived)
				cmd.SetKeepChatsArchived.Keep = st.KeepChatsArchived
				a.Send(cmd)
			})
		}
	case rChatLock:
		s.chatLockField(e, title)

	case rNotify:
		s.flip(e, title, &a.Settings.Notifications)
	case rShowContent:
		s.flip(e, title, &st.NotificationText)
	case rMsgSound:
		s.soundControl(e, title, false)
	case rMentionSound:
		s.soundControl(e, title, true)
	case rLimit:
		if v, ok := s.popup(e, title, int(a.Settings.LimitNotifications), s.tx.limit[:], d.ctrl); ok {
			a.Settings.LimitNotifications = limitChoices[v]
			s.changed(e)
		}
	case rGroupSounds:
		s.flip(e, title, &st.GroupSounds)

	case rReceipts:
		s.flip(e, title, &a.Settings.SendReadReceipts)
	case rTyping:
		s.flip(e, title, &a.Settings.SendTyping)
	case rAppLock:
		s.appLockButtons(e)
	case rLockAfter:
		if v, ok := s.popup(e, title, int(st.AppLockAfter), s.tx.lockOpts[:], d.ctrl); ok {
			st.AppLockAfter = state.AutoLock(v)
			e.S.Lock.LastInput = e.S.Now()
			s.changed(e)
		}

	case rKeepRunning:
		s.flip(e, title, &st.KeepRunning)
	case rStartLogin:
		s.startAtLogin(e, title)
	case rCheckUpdates:
		s.flip(e, title, &st.CheckForUpdates)
	case rAutoUpdate:
		s.flip(e, title, &st.AutoUpdate)
	case rProxy:
		s.proxyField(e, title)
	case rGiphy:
		k.NextWidth(k.Fixed(s.ctrlW(e, d.ctrl)))
		if sig := rexui.TextField(k, title, &s.giphy, "", rexui.FieldOpts{}); sig.Changed {
			st.GiphyKey = append(st.GiphyKey[:0], s.giphy.Bytes()...)
			s.changed(e)
		}

	case rVersion:
		rexui.Label(k, s.tx.versionLine, rexui.LabelOpts{Color: rexui.Of(theme.Label2)})
	case rUpdateStatus:
		k.BeginDisabled(s.up.Snapshot().Checking)
		if button(e, s.tx.checkNow) {
			s.up.Check()
		}
		k.EndDisabled()

	case rArchive:
		if button(e, s.tx.openFolder) {
			s.openFolder(e, s.files.state)
		}
	case rDownloads:
		s.downloadsControls(e)
	case rLog:
		if button(e, s.tx.open) {
			if a != nil {
				cmd := a.Cmd(abi.CommandTagOpenLog)
				cmd.OpenLog.Path = []byte(s.files.log)
				a.Send(cmd)
			}
		}
	}
	if d.id >= rPriv0 && d.id <= rPriv6 {
		s.privacyControl(e, d, title)
	}
}

func languageIndex(st *state.Settings) int {
	if len(st.InterfaceLanguage) == 0 {
		return 0
	}
	if l, ok := i18n.ParseTag(string(st.InterfaceLanguage)); ok {
		for i, x := range i18n.All {
			if x == l {
				return 1 + i
			}
		}
	}
	return 0
}

// setLanguage applies the interface language choice: 0 is Auto.
func (s *Screen) setLanguage(e *screen.Env, v int) {
	st := &e.S.Settings
	if v <= 0 || v > len(i18n.All) {
		st.InterfaceLanguage = st.InterfaceLanguage[:0]
		i18n.SetLocale(i18n.Detect())
	} else {
		st.InterfaceLanguage = append(st.InterfaceLanguage[:0], i18n.All[v-1].Tag()...)
		i18n.SetLocale(i18n.All[v-1])
	}
	s.changed(e)
}

// zoom is Larger, the percentage and Smaller (zf:src/ui/settings.rs, 0.6 to
// 2.0 in steps of 0.1).
func (s *Screen) zoom(e *screen.Env) {
	k := e.K.Kit
	st := &e.S.Settings
	if rexui.IconButton(k, s.icons.minus, s.tx.smaller, rexui.IconButtonOpts{}).Clicked {
		s.zoomBy(e, -0.1)
	}
	c := e.C()
	k.NextWidth(k.Fixed(48))
	c.NextSize(ui.Px(theme.Snap(k.Pt(48))), ui.Children(1))
	c.Push(c.BoxKeyed(0, ui.HashString(c.ParentKey(), "zoom"), nil))
	rexui.LabelBytes(k, c.Fmt().Int(int(st.Zoom*100+0.5)).Str("%").Bytes(), rexui.LabelOpts{})
	c.Pop()
	if rexui.IconButton(k, s.icons.plus, s.tx.larger, rexui.IconButtonOpts{}).Clicked {
		s.zoomBy(e, 0.1)
	}
}

func (s *Screen) zoomBy(e *screen.Env, delta float32) {
	z := (e.S.Settings.Zoom*10 + delta*10 + 0.5)
	z = float32(int(z)) / 10
	e.S.Settings.Zoom = min(max(z, 0.6), 2.0)
	s.changed(e)
}

// eachAccount calls f for every account that has a core.
func (s *Screen) eachAccount(e *screen.Env, f func(a *state.Account)) {
	for _, a := range e.S.Accounts {
		if a.Conn != nil {
			f(a)
		}
	}
}

// sendPrefetch tells the core whether it may fetch older history in the
// background, with the open chat first (zf:src/app.rs sync_prefetch).
func (s *Screen) sendPrefetch(e *screen.Env, a *state.Account) {
	s.changed(e)
	cmd := a.Cmd(abi.CommandTagSetHistoryPrefetch)
	cmd.SetHistoryPrefetch.On = a.Settings.HistoryPrefetch
	if a.Open != 0 && a.Active() {
		if c, ok := a.OpenChatView(); ok {
			cmd.SetHistoryPrefetch.Focused, cmd.SetHistoryPrefetch.HasFocused = c.ID(), true
		}
	}
	a.Send(cmd)
}
