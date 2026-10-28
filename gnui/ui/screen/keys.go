package screen

// Shortcut is one row of the keyboard shortcuts (zf:src/ui/keys.rs:622):
// its keys as macOS shows them (upstream's label(): Ctrl → ⌘, Alt → ⌥),
// what it does (upstream's English: translate with i18n.T), and the package
// that handles it. The shell's menu model carries the ones that are
// commands; the others are keys their owner handles in its own widgets.
type Shortcut struct {
	Keys, What, Owner string
}

// Shortcuts are the 29 shortcuts, in upstream's order: the help dialog
// (ui/dialogs, DialogShortcuts) lists them.
var Shortcuts = [...]Shortcut{
	{"⌘K / ⌘Shift+F", "Search chats", "shell → ui/chatlist (state.Requests.FocusSearch)"},
	{"⌘F", "Search the open chat or the settings", "shell → ui/conversation (Requests.FindInChat), ui/chatlist, ui/settings"},
	{"↑ / ↓, Enter", "Walk the open chat's search results and jump to one", "ui/conversation (the search pane)"},
	{"⌘L", "Focus the message input", "shell → ui/composer (Requests.FocusComposer)"},
	{"⌥↑ / ⌥↓", "Previous / next chat", "shell (state.Visible)"},
	{"⌘Shift+[ / ]", "Previous / next chat, as in WhatsApp", "shell (state.Visible)"},
	{"⌘1..9", "Open a chat by its position in the chat list", "shell (state.Visible)"},
	{"⌘Shift+1", "Chats", "shell"},
	{"⌘Shift+2", "Calls", "shell"},
	{"↑", "Edit the previous message (when the input is empty)", "ui/composer"},
	{"Enter", "Send (Shift+Enter for a new line)", "ui/composer"},
	{"Escape", "Dismiss the current action, return from search, Calls or Starred, or close the chat", "each screen first, then the shell"},
	{"⌘N", "New chat or message yourself", "shell (DialogNewChat)"},
	{"⌘E", "Archive or unarchive the open chat", "shell"},
	{"⌘Shift+A", "Switch to the next linked number", "shell"},
	{"⌘V", "Paste text, or stage a picture from the clipboard", "ui/composer"},
	{"⌘C", "Copy the selected messages", "ui/conversation"},
	{"⌘B", "Collapse or expand the chat list", "shell"},
	{"⌘End", "Jump to the newest message", "shell → ui/conversation (Requests.JumpToBottom)"},
	{"PgUp / PgDn", "Scroll the open chat by a page", "ui/conversation"},
	{"Home / End", "Top / bottom of the open chat (when the input is empty)", "ui/conversation"},
	{"⌘,", "Settings", "shell (the Settings window)"},
	{"⌘+ / ⌘-", "Zoom in / out", "shell (menu zoom roles)"},
	{"⌘0", "Reset zoom", "shell (menu zoom roles)"},
	{"← / →", "Previous / next item in the media viewer", "ui/viewer"},
	{"? / ⌘/", "Keyboard shortcuts (? when not typing)", "shell (DialogShortcuts)"},
	{"⌘Shift+L", "Lock Zap (with an app lock password)", "shell → ui/lock"},
	{"⌘W", "Close the window (Zap remains in the tray)", "shell (RoleCloseWindow)"},
	{"⌘Q", "Quit", "shell (RoleQuit)"},
}
