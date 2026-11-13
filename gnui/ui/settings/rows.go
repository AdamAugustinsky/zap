package settings

import (
	"strings"

	"zapfast/abi"
	"zapfast/i18n"
)

// tx is a setting's string: upstream's English msgid and, for the few that
// carry one, its gettext context.
type tx struct{ ctx, id string }

func (x tx) shown() string {
	if x.ctx != "" {
		return i18n.P(x.ctx, x.id)
	}
	return i18n.T(x.id)
}

// keyed is a string that names keys: Ctrl and Alt read as ⌘ and ⌥ on a Mac
// (zf:src/ui/keys.rs label).
func keyed(s string) string {
	s = strings.ReplaceAll(s, "Ctrl", "⌘")
	s = strings.ReplaceAll(s, "Strg", "⌘")
	return strings.ReplaceAll(s, "Alt", "⌥")
}

type secID uint8

const (
	secAppearance secID = iota
	secChats
	secNotifications
	secPrivacy
	secAI
	secSystem
	secUpdates
	secAccount
	secFiles
	secAbout
	numSections
)

var secTitle = [numSections]string{
	"Appearance", "Chats", "Notifications", "Privacy", "AI access", "System", "Updates", "Account", "Files", "About",
}

type rowID uint8

const (
	rTheme rowID = iota
	rThemeLinks
	rSpaceColour
	rFont
	rTransparency
	rZoom
	rLanguage

	rEnterSends
	rAutoDownload
	rHistory
	rKeepArchived
	rChatLock

	rNotify
	rShowContent
	rMsgSound
	rLimit
	rGroupSounds
	rMentionSound

	rReceipts
	rTyping
	rAppLock
	rLockForm
	rLockAfter
	rPrivNote
	rPriv0 // rPriv0…rPriv6: the account privacy categories, in the phone's order
	rPriv1
	rPriv2
	rPriv3
	rPriv4
	rPriv5
	rPriv6

	rAIAccess
	rAIConnect
	rAIRunner
	rAICommand
	rAICap
	rAIChats
	rAIActivity

	rKeepRunning
	rStartLogin
	rCheckUpdates
	rAutoUpdate
	rProxy
	rGiphy

	rVersion
	rUpdateStatus
	rUpdateFlow

	rAccount

	rArchive
	rDownloads
	rStorage
	rLog

	rAbout
	numRows
)

// rowDef is a settings row: its words, which the search finds it by, and how
// wide its control is. A row with no title lays itself out (a block).
type rowDef struct {
	id          rowID
	sec         secID
	title, desc tx
	kw          []tx
	ctrl        float32 // the control's width in points (0: a block)
	always      bool    // the search never hides it
}

func m(id string) tx { return tx{id: id} }

var privKinds = [7]abi.PrivacyKind{
	abi.PrivacyKindLastSeen, abi.PrivacyKindOnline, abi.PrivacyKindProfile, abi.PrivacyKindAbout,
	abi.PrivacyKindGroupAdd, abi.PrivacyKindReadReceipts, abi.PrivacyKindCallAdd,
}

func privTitle(i int) tx {
	return tx{"privacy", [7]string{"Last seen", "Online", "Profile photo", "About", "Groups", "Read receipts", "Calls"}[i]}
}

func privHint(i int) tx {
	switch i {
	case 4:
		return m("Who can add you to groups.")
	case 5:
		return m("For your whole account. Groups always send them.")
	}
	return tx{}
}

// rowDefs are the rows in page order (zf:src/ui/settings.rs sections()).
var rowDefs = func() []rowDef {
	d := []rowDef{
		{id: rTheme, sec: secAppearance, title: m("Theme"), ctrl: 200},
		{id: rThemeLinks, sec: secAppearance, kw: []tx{m("Open themes folder"), m("How to make a theme")}},
		{id: rSpaceColour, sec: secAppearance, title: m("Space colour"), desc: m("Colours and grain for the space on screen. With no folders, that is your account's main view."), ctrl: 120},
		{id: rFont, sec: secAppearance, title: m("Font"), desc: m("System is your desktop's interface font. Inter looks the same on every computer."), ctrl: 200},
		{id: rTransparency, sec: secAppearance, title: m("Transparency"), desc: m("The desktop shows softly through the window's background. Off, windows moving behind Zap cost it no graphics work; the change is complete the next time Zap opens. The system's Reduce transparency setting turns it off too."), ctrl: 54},
		{id: rZoom, sec: secAppearance, title: m("Zoom"), desc: m("Ctrl+Plus and Ctrl+Minus work anywhere, and Ctrl+0 resets it."), ctrl: 144},
		{id: rLanguage, sec: secAppearance, title: m("Language"), ctrl: 200},

		{id: rEnterSends, sec: secChats, title: m("Enter sends"), desc: m("When off, Ctrl+Enter sends."), ctrl: 54},
		{id: rAutoDownload, sec: secChats, title: m("Download files automatically"), desc: m("Files up to 64 MiB download as they come into view."), ctrl: 54},
		{id: rHistory, sec: secChats, title: m("Fetch older history"), desc: m("Asks your phone in the background for older messages in the open, pinned and ten most recent chats."), ctrl: 54},
		{id: rKeepArchived, sec: secChats, title: m("Keep chats archived"), desc: m("When off, a new message brings an archived chat back to the list."), ctrl: 54},
		{id: rChatLock, sec: secChats, title: m("Locked chats code"), desc: m("Opens the Locked tab on this computer. It hides chats, it does not encrypt them."), ctrl: 260},

		{id: rNotify, sec: secNotifications, title: m("Desktop notifications"), desc: m("For chats you are not looking at. Muted chats stay quiet."), ctrl: 54},
		{id: rShowContent, sec: secNotifications, title: m("Show message content in notifications"), desc: m("When off, they say only “New message”."), ctrl: 54},
		{id: rMsgSound, sec: secNotifications, title: m("Message sound"), ctrl: 230},
		{id: rLimit, sec: secNotifications, title: m("Limit repeat notifications"), desc: m("After one notification, a chat stays quiet for 10 minutes. Opening the chat resets this. In groups, mentions and replies to you still notify."), ctrl: 230},
		{id: rGroupSounds, sec: secNotifications, title: m("Play sounds for group messages"), desc: m("When off, only mentions and replies to you make a sound."), ctrl: 54},
		{id: rMentionSound, sec: secNotifications, title: m("Mention sound"), desc: m("When a group mentions you or replies to you."), ctrl: 230},

		{id: rReceipts, sec: secPrivacy, title: m("Send read receipts"), desc: m("Let people see when you read their messages."), ctrl: 54},
		{id: rTyping, sec: secPrivacy, title: m("Show when you are typing"), ctrl: 54},
		{id: rAppLock, sec: secPrivacy, title: m("App lock"), desc: m("Asks for a password at start and after a while unused. It keeps people using this computer out of your chats and encrypts nothing more. A forgotten password means unlinking."), ctrl: 250},
		{id: rLockForm, sec: secPrivacy, kw: []tx{m("App lock"), m("Lock after")}},
		{id: rLockAfter, sec: secPrivacy, title: m("Lock after"), desc: m("Time without using Zap, also counted while it is in the tray. Ctrl+Shift+L locks it at once."), ctrl: 200},
		{id: rPrivNote, sec: secPrivacy},
	}
	for i := range privKinds {
		d = append(d, rowDef{id: rPriv0 + rowID(i), sec: secPrivacy, title: privTitle(i), desc: privHint(i), ctrl: 220})
	}
	d = append(d,
		rowDef{id: rAIAccess, sec: secAI, title: m("Let AI agents use chosen chats"), desc: m("An agent you connect reads only the chats you turn this on for, and answers only where you allow it. What it reads there goes to that agent and the company that runs it."), ctrl: 54},
		rowDef{id: rAIConnect, sec: secAI, kw: []tx{m("Connect an agent"), {id: "Claude Code"}, {id: "Codex"}, {id: "opencode"}, {id: "MCP"}}},
		rowDef{id: rAIRunner, sec: secAI, title: m("Agent to start"), desc: m("Started when a message arrives in a chat that asks for it. Claude Code runs with Zap's tools only: no files, no shell, no web."), ctrl: 200},
		rowDef{id: rAICommand, sec: secAI, title: m("Custom command"), desc: m("Runs in your shell with the prompt on its standard input. Zap cannot limit what it does: give it nothing but Zap's tools."), ctrl: 260},
		rowDef{id: rAICap, sec: secAI, title: m("Replies per hour"), desc: m("The most an agent sends to one chat in an hour without asking you."), ctrl: 120},
		rowDef{id: rAIChats, sec: secAI, kw: []tx{m("Chats with AI access"), m("Remove all")}},
		rowDef{id: rAIActivity, sec: secAI, kw: []tx{m("Recent activity")}},

		rowDef{id: rKeepRunning, sec: secSystem, title: m("Keep running when the window closes"), desc: m("Quit from the tray or with Ctrl+Q."), ctrl: 54},
		rowDef{id: rStartLogin, sec: secSystem, title: m("Start at login"), desc: m("Starts in the tray, without a window."), ctrl: 54},
		rowDef{id: rCheckUpdates, sec: secSystem, title: m("Check for updates"), desc: m("Asks GitHub once a day, sending only the Zap version."), ctrl: 54},
		rowDef{id: rAutoUpdate, sec: secSystem, title: m("Download updates automatically"), desc: m("You still choose when to restart. Package managers and Flatpak update Zap themselves."), ctrl: 54},
		rowDef{id: rProxy, sec: secSystem, title: m("Proxy"), desc: m("For WhatsApp, media, and updates. Empty uses ALL_PROXY or HTTPS_PROXY."), ctrl: 240},
		rowDef{id: rGiphy, sec: secSystem, title: m("GIPHY API key"), desc: m("For GIF search. Get a free key at developers.giphy.com."), ctrl: 240},

		rowDef{id: rVersion, sec: secUpdates, title: m("Version"), ctrl: 200},
		rowDef{id: rUpdateStatus, sec: secUpdates, title: m("Status"), ctrl: 120},
		rowDef{id: rUpdateFlow, sec: secUpdates, kw: []tx{m("Update Zap"), m("Release notes"), m("Download update"), m("Restart to update")}},

		rowDef{id: rAccount, sec: secAccount, kw: []tx{m("Your name"), {"profile", "About"}, m("Change profile picture"), m("Unlink this computer"), m("Add account"), m("Remove this account")}},

		rowDef{id: rArchive, sec: secFiles, title: m("Message archive"), ctrl: 120},
		rowDef{id: rDownloads, sec: secFiles, title: m("Downloads"), ctrl: 280},
		rowDef{id: rStorage, sec: secFiles, kw: []tx{m("Storage used"), m("Media cache"), m("Images"), m("Videos and GIFs"), m("Stickers"), m("Other"), m("Clear…")}},
		rowDef{id: rLog, sec: secFiles, title: m("Log"), ctrl: 120},

		rowDef{id: rAbout, sec: secAbout, kw: []tx{{id: "Zap"}, m("Keyboard shortcuts"), m("Source code")}},
	)
	return d
}()
