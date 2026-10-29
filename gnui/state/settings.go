package state

import (
	"strings"
	"time"

	"zapfast/lockhash"
)

// The settings model, ported from zf:src/settings.rs: every field of
// upstream's Settings and AccountSettings, their defaults, the files'
// format and migrations (settings_file.go), and their saving (persist.go).
// Screens edit State.Settings and Account.Settings in memory; the state
// notices the change and saves it a moment later, off the main thread.

// Theme is the appearance (zf:src/settings.rs:33).
type Theme uint8

const (
	ThemeDark Theme = iota // upstream's default
	ThemeLight
	ThemeSystem
)

// Font is the interface font (zf:src/settings.rs:62).
type Font uint8

const (
	FontSystem Font = iota
	FontInter
)

// Wallpaper is upstream's conversation background colour
// (zf:src/settings.rs:85). Nothing draws it: it is kept so that the settings
// file keeps upstream's value.
type Wallpaper uint8

const (
	WallpaperTheme Wallpaper = iota
	WallpaperBeige
	WallpaperCruise
	WallpaperScandal
	WallpaperMonteCarlo
	WallpaperHawkesBlue
	WallpaperDowny
	WallpaperSeagull
	WallpaperQuartz
	WallpaperVeryLightGrey
	WallpaperOrinoco
	WallpaperTusk
	WallpaperCapeHoney
	WallpaperCaramel
	WallpaperRoseBud
	WallpaperBittersweet
	WallpaperRadicalRed
	WallpaperMandarinOrange
	WallpaperFlamingo
	WallpaperBuccaneer
	WallpaperBreakerBay
	WallpaperPelorous
	WallpaperToryBlue
	WallpaperFiord
	WallpaperCinder
	WallpaperTolopea
	WallpaperSolitude
	WallpaperCanary
	WallpaperWillowBrook
	WallpaperBlack
	WallpaperNordic
	WallpaperCardinGreen
	WallpaperTangaroa
	WallpaperTiber
	WallpaperBlackRussian
	WallpaperNero
	WallpaperMarshland
	WallpaperMaire
	WallpaperBlackMagic
	WallpaperCocoaBrown
	WallpaperWoodBark
	WallpaperSealBrown
	WallpaperSealBrownDarker
	WallpaperSealBrownLight
	WallpaperCyprus
	WallpaperBlueWhale
	WallpaperBlackPearl
	WallpaperDarkTolopea
	WallpaperWoodsmoke
	WallpaperMaireTwo
	numWallpapers
)

// wallpapers are each colour's name in the file (zf:src/settings.rs:203-313).
var wallpapers = [numWallpapers]string{
	"theme", "beige", "cruise", "scandal", "monte_carlo", "hawkes_blue", "downy", "seagull", "quartz",
	"very_light_grey", "orinoco", "tusk", "cape_honey", "caramel", "rose_bud", "bittersweet",
	"radical_red", "mandarin_orange", "flamingo", "buccaneer", "breaker_bay", "pelorous", "tory_blue",
	"fiord", "cinder", "tolopea", "solitude", "canary", "willow_brook", "black", "nordic",
	"cardin_green", "tangaroa", "tiber", "black_russian", "nero", "marshland", "maire", "black_magic",
	"cocoa_brown", "wood_bark", "seal_brown", "seal_brown_darker", "seal_brown_light", "cyprus",
	"blue_whale", "black_pearl", "dark_tolopea", "woodsmoke", "maire_two",
}

// AutoLock is when the app lock engages after the last input
// (zf:src/settings.rs:330).
type AutoLock uint8

const (
	LockOneMinute AutoLock = iota
	LockFifteenMinutes
	LockOneHour
)

// Duration is how long the app may go unused before it locks.
func (l AutoLock) Duration() time.Duration {
	switch l {
	case LockOneMinute:
		return time.Minute
	case LockOneHour:
		return time.Hour
	}
	return 15 * time.Minute
}

// Sound is a notification sound (zf:src/settings.rs:394). Pidgin's sounds
// are GPL-2.0 and not shipped: Receive and Alert map to system sounds.
type Sound uint8

const (
	SoundReceive Sound = iota
	SoundAlert
	SoundSystem
	SoundNone
	SoundCustom // the file in the setting's Path
)

// SoundChoice is a sound and, for SoundCustom, its file.
type SoundChoice struct {
	Sound Sound
	Path  []byte
}

// NotificationLimit limits notifications to some chats
// (zf:src/settings.rs:360): one notification, then ten quiet minutes.
type NotificationLimit uint8

const (
	LimitOff NotificationLimit = iota
	LimitDirect
	LimitGroup
	LimitBoth
)

// Applies reports whether a chat of this kind is limited.
func (l NotificationLimit) Applies(group bool) bool {
	switch l {
	case LimitDirect:
		return !group
	case LimitGroup:
		return group
	case LimitBoth:
		return true
	}
	return false
}

// EmojiCount is one emoji's reaction count on this device.
type EmojiCount struct {
	Emoji []byte
	Count uint32
}

// SettingsVersion is the settings file's format (zf:src/settings.rs:620).
const SettingsVersion = 1

// Settings are the process-wide settings (zf:src/settings.rs:413).
type Settings struct {
	Version           uint32
	Theme             Theme
	Font              Font
	InterfaceLanguage []byte // a locale's code ("de"), "" for the system's
	CustomTheme       []byte // a theme file's name, "" for none
	Zoom              float32
	SidebarWidth      float32 // points
	SearchPaneWidth   float32
	InfoPaneWidth     float32
	EnterSends        bool
	ShowWallpaper     bool
	WallpaperColor    Wallpaper
	DarkWallpaper     Wallpaper
	WallpaperImage    []byte // a path, "" for none; kept for the file, like the colours
	ShowShortcutHints bool
	Transparency      bool     // the system material behind the main window (shell.Config), as Rex's
	RecentEmoji       [][]byte // newest first
	GiphyKey          []byte   // the user's; empty: the built-in one, if any
	KeepRunning       bool     // keep running in the background (the status item)
	Notifications     bool
	NotificationText  bool // show the chat, sender and message, else "New message"
	MessageSound      SoundChoice
	MentionSound      SoundChoice
	GroupSounds       bool
	DownloadFolder    []byte // "" for the cache
	KeepChatsArchived bool
	Proxy             []byte
	CheckForUpdates   bool
	AutoUpdate        bool // download verified updates in the background
	VoiceSpeed        float32
	PauseOtherMedia   bool
	ChatLockHash      []byte // the locked chats' code: "salt$hash" (SetChatLockCode)
	ChatLockHintGone  bool
	AppLockHash       []byte // the app lock's verifier; nil: no lock
	AppLockAfter      AutoLock
	WindowW, WindowH  float32 // the main window's last size in points, 0: the default
	WindowX, WindowY  float32
	HasWindowPos      bool
	WindowMaximized   bool
	StartAtLogin      bool // Settings → Open at login: the system's, not saved here

	CustomTitlebar   bool   // Windows and Linux only upstream; kept for the file
	CustomThemeCache []byte // the custom theme's palette, as the file holds it (raw JSON)
	SystemThemeCache []byte // the desktop theme's palette (raw JSON)
	// SendReadReceipts, SendTyping, AutoDownload, LastChat and
	// SaveContactsToPhone are the single-account releases' own, which the
	// first account's settings start from (AccountSettings has them now);
	// SaveContactsToPhone also starts the new-contact dialog's box.
	SendReadReceipts    bool
	SendTyping          bool
	AutoDownload        bool
	LastChat            []byte
	SaveContactsToPhone bool
	ReactionEmoji       []EmojiCount // most used first
}

// DefaultSettings are upstream's defaults (zf:src/settings.rs:556).
func DefaultSettings() Settings {
	return Settings{
		Version: SettingsVersion, Theme: ThemeDark, Font: FontSystem, Zoom: 1,
		SidebarWidth: 300, SearchPaneWidth: 380, InfoPaneWidth: 380,
		EnterSends: true, ShowWallpaper: true, ShowShortcutHints: true, Transparency: true,
		KeepRunning: true, Notifications: true, NotificationText: true,
		MessageSound: SoundChoice{Sound: SoundReceive}, MentionSound: SoundChoice{Sound: SoundAlert},
		GroupSounds: true, KeepChatsArchived: true, CheckForUpdates: true,
		VoiceSpeed: 1, PauseOtherMedia: true, AppLockAfter: LockFifteenMinutes,
		SendReadReceipts: true, SendTyping: true, AutoDownload: true, SaveContactsToPhone: true,
	}
}

// GiphyKeyFor is the key GIF search uses: the user's, trimmed, else
// builtIn (zf:src/settings.rs:656); empty for none.
func (st *Settings) GiphyKeyFor(builtIn string) string {
	if k := strings.TrimSpace(string(st.GiphyKey)); k != "" {
		return k
	}
	return strings.TrimSpace(builtIn)
}

// SetChatLockCode stores the locked chats' code as a salted, slow verifier
// (zf:src/settings.rs:777, package lockhash); an empty code removes it.
func (st *Settings) SetChatLockCode(code string) {
	st.ChatLockHash = []byte(lockhash.ChatVerifier([]byte(code)))
}

// VerifyChatLockCode reports whether code is the locked chats' code. It is
// deliberately slow (tens of ms): callers memoize the answer per string.
func (st *Settings) VerifyChatLockCode(code string) bool {
	return lockhash.VerifiesChat(string(st.ChatLockHash), []byte(code))
}

// AccountSettings are one account's settings (zf:src/settings.rs:828).
type AccountSettings struct {
	SendReadReceipts    bool
	SendTyping          bool
	AutoDownload        bool
	LastChat            []byte // the chat open at quit, reopened at start
	Notifications       bool
	LimitNotifications  NotificationLimit
	SaveContactsToPhone bool
	WallpaperImage      []byte   // this account's wallpaper image, kept for the file
	HistoryPrefetch     bool     // ask the phone for older history in the background
	ListFilter          []byte   // the chat list's chip or "label:<id>" (ListFilterKey), nil: All
	CallFilter          []byte   // the Calls page's chip: "missed", nil: All
	Folders             []Folder // the chat list's folders (folders.go)
}

// DefaultAccountSettings are upstream's defaults (zf:src/settings.rs:852).
func DefaultAccountSettings() AccountSettings {
	return AccountSettings{SendReadReceipts: true, SendTyping: true, AutoDownload: true, Notifications: true, SaveContactsToPhone: true}
}

// AccountSettingsFrom are the first account's settings when it has none:
// the single-account releases' (zf:src/settings.rs:872).
func AccountSettingsFrom(st *Settings) AccountSettings {
	return AccountSettings{
		SendReadReceipts: st.SendReadReceipts, SendTyping: st.SendTyping, AutoDownload: st.AutoDownload,
		LastChat: clone(st.LastChat), Notifications: st.Notifications, SaveContactsToPhone: st.SaveContactsToPhone,
		WallpaperImage: clone(st.WallpaperImage),
	}
}

func clone(b []byte) []byte {
	if len(b) == 0 {
		return nil
	}
	return append([]byte(nil), b...)
}

const labelFilterPrefix = "label:"

// ListFilterKey is how the settings store the chat list's filter
// (zf:src/model.rs:150): nil for All.
func ListFilterKey(f Filter, label []byte) []byte {
	if f == FilterLabel && len(label) > 0 {
		return append([]byte(labelFilterPrefix), label...)
	}
	switch f {
	case FilterUnread:
		return []byte("unread")
	case FilterPrivate:
		return []byte("private")
	case FilterFavorites:
		return []byte("favorites")
	case FilterGroups:
		return []byte("groups")
	case FilterChannels:
		return []byte("channels")
	}
	return nil
}

// ParseListFilter reads a stored filter; anything unknown shows every chat
// (zf:src/model.rs:167).
func ParseListFilter(key []byte) (f Filter, label []byte) {
	k := string(key)
	if l, ok := strings.CutPrefix(k, labelFilterPrefix); ok {
		if l == "" {
			return FilterAll, nil
		}
		return FilterLabel, []byte(l)
	}
	switch k {
	case "unread":
		return FilterUnread, nil
	case "private":
		return FilterPrivate, nil
	case "favorites":
		return FilterFavorites, nil
	case "groups":
		return FilterGroups, nil
	case "channels":
		return FilterChannels, nil
	}
	return FilterAll, nil
}

// SettingsChanged marks the process-wide settings changed. The state
// notices a change by itself within a quarter second; this saves sooner.
func (s *State) SettingsChanged() {
	s.settingsGen++
	s.gen++
}

// SettingsChanged marks the account's settings changed (as
// State.SettingsChanged).
func (a *Account) SettingsChanged() {
	a.settingsGen++
	a.st.gen++
}
