package screen

// Shortcut is one row of the keyboard shortcuts (zf:src/ui/keys.rs:622):
// its keys as macOS shows them (upstream's label(): Ctrl → ⌘, Alt → ⌥),
// what it does (upstream's English: translate with i18n.T), and the package
// that handles it. The shell's menu model carries the ones that are
// commands; the others are keys their owner handles in its own widgets.
type Shortcut struct {
	Keys, What, Owner string
}

// Shortcuts are upstream's 29 shortcuts, in its order, with Zap's own after
// ⌘K (Spotlight) and the single keys (shell/keyboard.go) at the end: the
// help dialog (ui/dialogs, DialogShortcuts) lists them.
var Shortcuts = [...]Shortcut{
	{"⌘K", "Search everything: chats, people, messages, files", "shell → ui/spotlight (state.Spotlight)"},
	{"⌘Shift+F", "Search chats", "shell → ui/chatlist (state.Requests.FocusSearch)"},
	{"⌘F", "Search the open chat or the settings", "shell → ui/conversation (Requests.FindInChat), ui/chatlist, ui/settings"},
	{"↑ / ↓, Enter", "Walk the open chat's search results and jump to one", "ui/conversation (the search pane)"},
	{"⌘L", "Focus the message input", "shell → ui/composer (Requests.FocusComposer)"},
	{"⌥↑ / ⌥↓", "Previous / next chat", "shell (state.Visible)"},
	{"⌘Shift+[ / ]", "Previous / next chat, as in WhatsApp", "shell (state.Visible)"},
	{"⌘1..9", "Go to the tab at this place", "shell (state.ChatTabs)"},
	{"⌃1..9", "Go to the space at this place", "shell (state.Spaces)"},
	{"Two fingers across the list", "Next / previous space", "shell (spaces.go)"},
	{"⌘Shift+1", "Chats", "shell"},
	{"⌘Shift+2", "Calls", "shell"},
	{"↑", "Edit the previous message (when the input is empty)", "ui/composer"},
	{"Enter", "Send (Shift+Enter for a new line)", "ui/composer"},
	{"Escape", "Dismiss the current action, return from search, Calls or Starred, leave the message input, or close the chat", "each screen first, then the shell"},
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
	// the single keys, when not typing (shell/keyboard.go)
	{"/", "Search everything (when not typing)", "shell (keyboard.go)"},
	{"J / K", "Next / previous chat (when not typing)", "shell (keyboard.go)"},
	{"E", "Archive or unarchive the open chat (when not typing)", "shell (keyboard.go)"},
	{"M", "Mute the open chat for 8 hours, or unmute it (when not typing)", "shell (keyboard.go)"},
	{"P", "Pin or unpin the open chat (when not typing)", "shell (keyboard.go)"},
	{"U", "Mark the open chat as unread or read (when not typing)", "shell (keyboard.go)"},
	{"R", "Reply to the last message (when not typing)", "shell (keyboard.go)"},
	{"Z", "Undo the last change a single key made", "shell (keyboard.go)"},
	{"G then I / A / S / C", "Go to the chats, Archived, Starred or Calls", "shell (keyboard.go)"},
	{"Typing", "Starts a message in the open chat, or a search", "shell (keyboard.go)"},
	{"Space over a chat / ⌥-click", "Peek at a chat without reading it", "ui/chatlist, shell (ui/peek)"},
	{"⌃⌥Z", "Peek at the newest unread chat, from any app", "sys/peek.go (ui/peek.Window)"},
	{"⌃⌥V", "Split with the next tab", "shell (ui/shell/split.go)"},
	{"⌃⌥U", "Close the split", "shell (ui/shell/split.go)"},
	{"⌃⌥P", "Float the chat over other apps", "shell (ui/chatview.Pip)"},
	{"⌥⌘← / ⌥⌘→", "Previous pane / next pane", "shell (ui/shell/split.go)"},
	{"⌥⌘⇧← / ⌥⌘⇧→", "Move the pane left / right", "shell (ui/shell/split.go)"},
}
