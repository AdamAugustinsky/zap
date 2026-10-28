package shell

import (
	"log/slog"
	"math"

	"gonativeui/menu"
	"gonativeui/platform"
	"gonativeui/ui"

	"zapfast/abi"
	"zapfast/i18n"
	"zapfast/model"
	"zapfast/state"
	"zapfast/ui/screen"
)

// The app's commands: upstream's macOS menu bar (zf:src/macos.rs:47-110) and
// the shortcuts of zf:src/ui/keys.rs that are commands (screen.Shortcuts).
const (
	CmdAbout menu.ID = iota + 1
	CmdSettings
	CmdQuit
	CmdNewChat
	CmdCloseWindow
	CmdUndo
	CmdRedo
	CmdCut
	CmdCopy
	CmdPaste
	CmdSelectAll
	CmdFind
	CmdToggleSidebar
	CmdZoomIn
	CmdZoomOut
	CmdActualSize
	CmdMinimize
	CmdZoom
	CmdShowWindow
	CmdShortcuts
	CmdHelp
	CmdSearchChats
	CmdFocusComposer
	CmdPrevChat
	CmdNextChat
	CmdPrevChatWA
	CmdNextChatWA
	CmdChats
	CmdCalls
	CmdArchive
	CmdNextAccount
	CmdJumpToBottom
	CmdLock
	CmdChat1      // … CmdChat9: ⌘1 … ⌘9
	cmdChatLast   = CmdChat1 + 8
	CmdFullScreen = cmdChatLast + 1
	CmdHide       = CmdFullScreen + 1
	CmdHideOthers = CmdHide + 1
	CmdShowAll    = CmdHideOthers + 1
	CmdCloseTab   = CmdShowAll + 1
)

// helpURL is the user guide ZapFast Help opens (zf:src/macos.rs:234).
const helpURL = "https://zapfast.rocks/using-zapfast/"

// openLink opens url through the state's Shell (the browser; tests fake it),
// off the main thread: opening can take seconds.
func openLink(s *state.State, url string) {
	sh := s.Shell
	if sh == nil {
		return
	}
	go func() {
		if err := sh.Open(url); err != nil {
			slog.Warn("zapfast: opening a link", "url", url, "err", err)
		}
	}()
}

// newModel builds the menu model every window shares.
func newModel() (*menu.Model, error) { return buildModel(false) }

// newViewerModel is the viewer window's menu model: the shared one, but View's
// Zoom In, Zoom Out and Actual Size zoom the picture (screen.CmdPictureZoomIn
// …) instead of the app (upstream's ⌘+ ⌘- ⌘0 over the picture).
func newViewerModel() (*menu.Model, error) { return buildModel(true) }

func buildModel(viewer bool) (*menu.Model, error) {
	T, P := i18n.T, i18n.P
	prim := func(k ui.KeyCode, m ui.Mods) menu.Shortcut { return menu.Shortcut{Key: k, Mods: m, Primary: true} }
	sh := ui.ModShift
	cmds := []menu.Command{
		{ID: CmdAbout, Title: P("menu bar", "About Zap"), Role: menu.RoleAbout},
		{ID: CmdSettings, Title: P("menu bar", "Settings…"), Role: menu.RoleSettings, Shortcut: prim(',', 0)},
		{ID: CmdQuit, Title: P("menu bar", "Quit Zap"), Role: menu.RoleQuit, Shortcut: prim('Q', 0), InModal: true},
		{ID: CmdHide, Title: P("menu bar", "Hide Zap"), Role: menu.RoleHide, Shortcut: prim('H', 0)},
		{ID: CmdHideOthers, Title: P("menu bar", "Hide Others"), Role: menu.RoleHideOthers, Shortcut: prim('H', ui.ModAlt)},
		{ID: CmdShowAll, Title: P("menu bar", "Show All"), Role: menu.RoleShowAll},
		{ID: CmdNewChat, Title: P("menu bar", "New Chat…"), Shortcut: prim('N', 0)},
		// ⌘W closes the open chat's tab, as a browser's; the window moves to
		// ⇧⌘W, as Safari's.
		{ID: CmdCloseTab, Title: P("menu bar", "Close Tab"), Shortcut: prim('W', 0)},
		{ID: CmdCloseWindow, Title: P("menu bar", "Close Window"), Role: menu.RoleCloseWindow, Shortcut: prim('W', sh)},
		{ID: CmdUndo, Title: P("menu bar", "Undo"), Role: menu.RoleUndo, Shortcut: prim('Z', 0)},
		{ID: CmdRedo, Title: P("menu bar", "Redo"), Role: menu.RoleRedo, Shortcut: prim('Z', sh)},
		{ID: CmdCut, Title: P("menu bar", "Cut"), Role: menu.RoleCut, Shortcut: prim('X', 0)},
		{ID: CmdCopy, Title: P("menu bar", "Copy"), Role: menu.RoleCopy, Shortcut: prim('C', 0)},
		{ID: CmdPaste, Title: P("menu bar", "Paste"), Role: menu.RolePaste, Shortcut: prim('V', 0)},
		{ID: CmdSelectAll, Title: P("menu bar", "Select All"), Role: menu.RoleSelectAll, Shortcut: prim('A', 0)},
		{ID: CmdFind, Title: P("menu bar", "Find…"), Shortcut: prim('F', 0)},
		{ID: CmdToggleSidebar, Title: P("menu bar", "Toggle Sidebar"), Shortcut: prim('B', 0)},
		// Upstream's zoom (ZoomBy ±0.1 within 60–200 %, saved in the
		// settings) for every window, rather than the framework's per-window
		// zoom roles: their titles and keys without the role.
		titled(noRole(menu.StandardCommand(CmdZoomIn, menu.RoleZoomIn)), P("menu bar", "Zoom In")),
		titled(noRole(menu.StandardCommand(CmdZoomOut, menu.RoleZoomOut)), P("menu bar", "Zoom Out")),
		titled(noRole(menu.StandardCommand(CmdActualSize, menu.RoleActualSize)), P("menu bar", "Actual Size")),
		{ID: CmdMinimize, Title: P("menu bar", "Minimize"), Role: menu.RoleMinimize, Shortcut: prim('M', 0)},
		{ID: CmdZoom, Title: P("menu bar", "Zoom"), Role: menu.RoleZoom},
		{ID: CmdShowWindow, Title: P("menu bar", "Show Zap"), InModal: true},
		// AppKit's own toggleFullScreen: item (zf:src/macos.rs:90,
		// MB-021): "Enter Full Screen", ⌃⌘F, which AppKit retitles "Exit
		// Full Screen" and carries out itself.
		inModal(titled(menu.StandardCommand(CmdFullScreen, menu.RoleFullScreen), P("menu bar", "Enter Full Screen"))),
		{ID: CmdShortcuts, Title: P("menu bar", "Keyboard Shortcuts"), Shortcut: prim('/', 0)},
		{ID: CmdHelp, Title: P("menu bar", "Zap Help")},
		{ID: CmdSearchChats, Title: T("Search chats"), Shortcut: prim('K', 0), Alias: prim('F', sh)},
		{ID: CmdFocusComposer, Title: T("Focus the message input"), Shortcut: prim('L', 0)},
		// zf:src/ui/keys.rs:630-631's descriptions, which upstream shows as is.
		{ID: CmdPrevChat, Title: T("Previous / next chat"), Shortcut: menu.Shortcut{Key: ui.KeyUp, Mods: ui.ModAlt}},
		{ID: CmdNextChat, Title: T("Previous / next chat"), Shortcut: menu.Shortcut{Key: ui.KeyDown, Mods: ui.ModAlt}},
		{ID: CmdPrevChatWA, Title: T("Previous / next chat, as in WhatsApp"), Shortcut: prim('[', sh)},
		{ID: CmdNextChatWA, Title: T("Previous / next chat, as in WhatsApp"), Shortcut: prim(']', sh)},
		{ID: CmdChats, Title: T("Chats"), Shortcut: prim('1', sh)},
		{ID: CmdCalls, Title: T("Calls"), Shortcut: prim('2', sh)},
		{ID: CmdArchive, Title: T("Archive or unarchive the open chat"), Shortcut: prim('E', 0)},
		{ID: CmdNextAccount, Title: T("Switch to the next linked number"), Shortcut: prim('A', sh)},
		{ID: CmdJumpToBottom, Title: T("Jump to the newest message"), Shortcut: prim(ui.KeyEnd, 0)},
		{ID: CmdLock, Title: T("Lock Zap"), Shortcut: prim('L', sh)},
	}
	for i := 0; i < 9; i++ {
		cmds = append(cmds, menu.Command{ID: CmdChat1 + menu.ID(i), Title: T("Open a chat by its position in the chat list"), Shortcut: prim(ui.KeyCode('1'+i), 0)})
	}
	zoomIn, zoomOut, actual := CmdZoomIn, CmdZoomOut, CmdActualSize
	if viewer {
		zoomIn, zoomOut, actual = screen.CmdPictureZoomIn, screen.CmdPictureZoomOut, screen.CmdPictureActualSize
		for i := range cmds {
			var id menu.ID
			switch cmds[i].ID {
			case CmdZoomIn:
				id = zoomIn
			case CmdZoomOut:
				id = zoomOut
			case CmdActualSize:
				id = actual
			default:
				continue
			}
			cmds[i].ID = id
		}
	}
	item := func(id menu.ID) menu.Item { return menu.Item{Kind: menu.ItemCommand, Command: id} }
	sep := menu.Item{Kind: menu.ItemSeparator}
	// Upstream's bar titles are English in every language
	// (zf:src/macos.rs:47-110); here they follow the interface language, in
	// Apple's own wording ("menu bar" context), including the application
	// menu's system roles and Services submenu.
	menus := []menu.Menu{
		{Title: "Zap", Items: []menu.Item{item(CmdAbout), sep, item(CmdSettings), sep, item(CmdQuit)}},
		{Title: P("menu bar", "File"), Items: []menu.Item{item(CmdNewChat), sep, item(CmdCloseTab), item(CmdCloseWindow)}},
		{Title: P("menu bar", "Edit"), Items: []menu.Item{item(CmdUndo), item(CmdRedo), sep, item(CmdCut), item(CmdCopy), item(CmdPaste), item(CmdSelectAll), sep, item(CmdFind)}},
		{Title: P("menu bar", "View"), Items: []menu.Item{item(CmdToggleSidebar), sep, item(zoomIn), item(zoomOut), item(actual), sep, item(CmdFullScreen)}},
		{Title: P("menu bar", "Window"), Role: menu.MenuWindow, Items: []menu.Item{item(CmdMinimize), item(CmdZoom), sep, item(CmdShowWindow)}},
		{Title: P("menu bar", "Help"), Role: menu.MenuHelp, Items: []menu.Item{item(CmdShortcuts), item(CmdHelp)}},
		{Title: P("menu bar", "Services"), Role: menu.MenuServices},
	}
	return menu.NewModel(cmds, menus, []int{1, 2, 3, 4, 5})
}

// inModal marks a command as one that acts while a modal is open.
func inModal(c menu.Command) menu.Command {
	c.InModal = true
	return c
}

// titled sets c's title: the standard commands come with English ones.
func titled(c menu.Command, title string) menu.Command {
	c.Title = title
	return c
}

func noRole(c menu.Command) menu.Command {
	c.Role = menu.RoleNone
	return c
}

// zoomBy changes the interface zoom by delta within upstream's 60–200 %
// (Action::ZoomBy); 0 resets it (Action::ResetZoom). Every window applies
// it (screen.ApplyZoom).
func zoomBy(s *state.State, delta float32) {
	z := float32(1)
	if delta != 0 {
		z = float32(math.Round(float64(s.Settings.Zoom+delta)*10)) / 10
		z = min(max(z, 0.6), 2)
	}
	if z != s.Settings.Zoom {
		s.Settings.Zoom = z
		s.SettingsChanged()
	}
}

// RefreshMenus rebuilds the menu models in the interface language when it
// changed since they were built, and returns their revision
// (screen.MenuRefresher). A model that cannot be built is kept as it was.
func (sh *Shell) RefreshMenus() int {
	l := i18n.Current()
	if l == sh.menuLoc {
		return sh.menuRev
	}
	sh.menuLoc = l
	m, err := newModel()
	if err != nil {
		slog.Warn("zapfast: the menu bar", "err", err)
		return sh.menuRev
	}
	vm, err := newViewerModel()
	if err != nil {
		slog.Warn("zapfast: the menu bar", "err", err)
		return sh.menuRev
	}
	sh.model, sh.viewerM = m, vm
	sh.menuRev++
	return sh.menuRev
}

// Menu is the shared menu model (screen.Chrome).
func (sh *Shell) Menu() *menu.Model { return sh.model }

// MenuOf is window w's own menu model (screen.WindowMenus): the viewer's,
// nil for the shared one.
func (sh *Shell) MenuOf(w state.Window) *menu.Model {
	if w == state.WinViewer {
		return sh.viewerM
	}
	return nil
}

// Commands runs the app-wide commands that fired in a window's frame
// (screen.Chrome): every window calls it after Kit.Begin.
func (sh *Shell) Commands(e *screen.Env) {
	k, s := e.K, sh.S
	a := s.Acct()
	main := s.Lock.Locked == false && a != nil && a.Model.Linked() && !s.AddingAccount
	if s.Req.RaiseMain {
		s.Req.RaiseMain = false
		sh.raiseMain()
	}
	if k.Command(CmdAbout) {
		s.OpenDialog(state.Dialog{Kind: state.DialogAbout})
		sh.raiseMain()
	}
	if k.Command(CmdSettings) && !s.Lock.Locked {
		s.OpenWindow(state.WinSettings)
	}
	if k.Command(CmdQuit) {
		e.A.RequestQuit()
	}
	if k.Command(CmdShowWindow) {
		sh.raiseMain()
	}
	if k.Command(CmdShortcuts) && !s.Lock.Locked {
		s.OpenDialog(state.Dialog{Kind: state.DialogShortcuts})
		sh.raiseMain()
	}
	if k.Command(CmdHelp) {
		openLink(s, helpURL)
	}
	if k.Command(CmdCloseTab) {
		sh.closeTab(e, a, main)
	}
	if k.Command(CmdZoomIn) {
		zoomBy(s, 0.1)
	}
	if k.Command(CmdZoomOut) {
		zoomBy(s, -0.1)
	}
	if k.Command(CmdActualSize) {
		zoomBy(s, 0)
	}
	// ? lists the shortcuts when nothing takes typing (keys.rs:60-66).
	if c := k.Context(); e.A == sh.a && !s.Lock.Locked && !c.TextFieldFocused() && !c.ModalOpen() && c.TakeKey('/', ui.ModShift) {
		s.OpenDialog(state.Dialog{Kind: state.DialogShortcuts})
	}
	// Native menus carry the role out; drawn ones leave it to us.
	if k.Command(CmdFullScreen) && !k.Context().NativeMenus() {
		if w := e.A.Window(); w != nil {
			w.ToggleFullscreen()
		}
	}
	if k.Command(CmdLock) && len(s.Settings.AppLockHash) > 0 {
		s.Lock.Locked = true
		s.Touch()
	}
	if k.Command(CmdFind) {
		switch {
		case s.Win.Open[state.WinSettings] && e.A != sh.a:
			s.Req.FindSettings = true
		case main && a.Open != 0 && !a.LockedAway(a.Open):
			s.Req.FindInChat = true
		default:
			s.Req.FocusSearch = true
		}
		s.Touch()
	}
	if !main {
		return
	}
	// The chat's own shortcuts wait while something sits over it: a
	// dialog, the picker, a reaction, a recording (keys.rs:67-80, :589-619).
	busy := s.Dialog.Open() || s.Panes.Picker != state.PickerNone || s.Panes.ReactTarget.Open() || s.InUse()
	sh.noChatPaste(e, a)
	if k.Command(CmdNewChat) {
		s.OpenDialog(state.Dialog{Kind: state.DialogNewChat})
		sh.raiseMain()
	}
	if k.Command(CmdToggleSidebar) {
		s.Panes.ListHidden = !s.Panes.ListHidden
		s.Touch()
	}
	if k.Command(CmdSearchChats) {
		s.Page = state.PageChats
		a.SetMode(state.ListChats)
		s.Req.FocusSearch = true
		s.Panes.ListHidden = false
		s.Touch()
	}
	if k.Command(CmdFocusComposer) && !busy {
		s.Req.FocusComposer = true
		s.Touch()
	}
	if k.Command(CmdJumpToBottom) {
		s.Req.JumpToBottom = true
		s.Touch()
	}
	if k.Command(CmdChats) {
		s.Page = state.PageChats
		a.SetMode(state.ListChats)
		s.Touch()
	}
	if k.Command(CmdCalls) {
		s.Page = state.PageCalls
		s.Touch()
	}
	step := 0
	if k.Command(CmdPrevChat) || k.Command(CmdPrevChatWA) {
		step = -1
	}
	if k.Command(CmdNextChat) || k.Command(CmdNextChatWA) {
		step = 1
	}
	if step == 0 {
		step = fieldAltStep(k.Context())
	}
	if step != 0 {
		sh.stepChat(a, step)
	}
	for id := CmdChat1; id <= cmdChatLast; id++ {
		if k.Command(id) && !busy && s.Page == state.PageChats {
			if v := sh.visible(a); int(id-CmdChat1) < len(v) {
				sh.keepUnread(v[id-CmdChat1])
				a.OpenChat(v[id-CmdChat1])
			}
		}
	}
	if k.Command(CmdArchive) && !busy {
		if c, ok := a.OpenChatView(); ok {
			cmd := a.Cmd(abi.CommandTagSetArchived)
			cmd.SetArchived.Chat, cmd.SetArchived.Archived = c.ID(), !c.Archived()
			a.Send(cmd)
			a.OpenChat(0) // archiving closes the chat, as the chat menus do
		}
	}
	if k.Command(CmdNextAccount) && len(s.Accounts) > 1 && !busy && !s.AddingAccount {
		s.SwitchAccount((s.Active + 1) % len(s.Accounts))
	}
	sh.model.SetChecked(CmdToggleSidebar, !s.Panes.ListHidden)
}

// visible is the chat list's rows, including an empty filtered result. Only
// without a displayed list do we fall back to the model's order.
func (sh *Shell) visible(a *state.Account) []model.ChatRef {
	if len(sh.S.Visible) > 0 || sh.S.Page == state.PageChats && sh.Set.ChatList != nil {
		return sh.S.Visible
	}
	return a.Model.Chats.Order()
}

// stepChat opens the chat before or after the open one in the list.
func (sh *Shell) stepChat(a *state.Account, step int) {
	v := sh.visible(a)
	if len(v) == 0 {
		return
	}
	at := -1
	for i, ref := range v {
		if ref == a.Open {
			at = i
			break
		}
	}
	next := 0
	if at >= 0 {
		next = (at + step + len(v)) % len(v)
	}
	sh.keepUnread(v[next])
	a.OpenChat(v[next])
	// the chat the next step opens draws at once too
	a.Prefetch(v[(next+step%len(v)+len(v))%len(v)])
}

// keepUnread uses the list's click policy, not Account.OpenChat's jump policy.
func (sh *Shell) keepUnread(ref model.ChatRef) {
	if list, ok := sh.Set.ChatList.(interface {
		KeepUnread(*screen.Env, model.ChatRef)
	}); ok && sh.S.Page == state.PageChats {
		list.KeepUnread(&sh.env, ref)
	}
}

// pasteNoChat tags the shell's own clipboard request (pasteNoChat).
const pasteNoChat = 0x7a66_7368_656c // "zfshel"

// noChatPaste is ⌘V with no chat open and nothing that takes typing: a
// picture or files on the clipboard say "Open a chat first", as staging
// with no chat does upstream (zf:src/app.rs:5498-5500, CP-020).
func (sh *Shell) noChatPaste(e *screen.Env, a *state.Account) {
	k, c := e.K, e.C()
	if a.Open != 0 || e.A != sh.a {
		return
	}
	if p, ok := e.A.TakePaste(); ok {
		if p.Tag == pasteNoChat && (p.Kind == platform.PasteFiles || p.Kind == platform.PasteImage) {
			sh.S.Toast([]byte(i18n.T("Open a chat first")), true)
		}
		return
	}
	if c.TextFieldFocused() || c.ModalOpen() || k.MenuOpen() {
		return
	}
	paste := c.TakeKey('V', ui.PrimaryMod())
	in := c.Input()
	for i := 0; i < in.NEdit; i++ {
		paste = paste || in.Edit[i] == ui.EditPaste
	}
	if paste {
		e.A.RequestPaste(pasteNoChat, platform.WantImage|platform.WantFiles)
	}
}

// fieldAltStep is ⌥↑ or ⌥↓ that a focused text field claimed (the
// composer's, the search field's): upstream switches chats with them
// without leaving the field (keys.rs:254-268), so they step here too, -1
// or 1; 0 for none. Not inside a dialog or a menu.
func fieldAltStep(c *ui.Context) int {
	if !c.TextFieldFocused() || c.ModalOpen() {
		return 0
	}
	for i := 0; i < c.KeyCount(); i++ {
		e, claimed := c.KeyAt(i)
		if !claimed || !e.Down || e.Mods != ui.ModAlt {
			continue
		}
		switch e.Code {
		case ui.KeyUp:
			return -1
		case ui.KeyDown:
			return 1
		}
	}
	return 0
}

// closeTab is ⌘W. In the main window it closes the open chat: its tab, and
// the next tab opens (state.CloseChatTab). With no chat open, or in another
// window, it closes the window, as Close Window does.
func (sh *Shell) closeTab(e *screen.Env, a *state.Account, main bool) {
	s := sh.S
	if e.A == sh.a && main && s.Page == state.PageChats && a.Open != 0 {
		for i, ref := range a.ChatTabs() {
			if ref == a.Open {
				a.CloseChatTab(i)
				s.Touch()
				return
			}
		}
		a.OpenChat(0) // an open chat without a tab
		s.Touch()
		return
	}
	for w := state.Window(0); w < state.NumWindows; w++ {
		if h := sh.wins[w]; h != nil && h.App() == e.A {
			s.CloseWindow(w)
			return
		}
	}
	if win := e.A.Window(); win != nil {
		win.Close()
	}
}
