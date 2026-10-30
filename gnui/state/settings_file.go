package state

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
)

// The settings files keep upstream's format (zf:src/settings.rs): the same
// file names, keys and values, so a file either release wrote reads in the
// other. Fields missing from a file keep their defaults; unknown ones are
// ignored and dropped by the next save.

// settingsFile is settings.json (zf:src/settings.rs:413), in upstream's
// field order.
type settingsFile struct {
	present           bool            // set only by the actual JSON read, never serialized
	Version           uint32          `json:"version"`
	Theme             string          `json:"theme"`
	Font              string          `json:"font"`
	CustomTitlebar    bool            `json:"custom_titlebar"`
	InterfaceLanguage *string         `json:"interface_language"`
	CustomTheme       *string         `json:"custom_theme"`
	CustomThemeCache  json.RawMessage `json:"custom_theme_cache,omitempty"`
	SystemThemeCache  json.RawMessage `json:"system_theme_cache,omitempty"`
	Zoom              float32         `json:"zoom"`
	SidebarWidth      float32         `json:"sidebar_width"`
	SearchPaneWidth   float32         `json:"search_pane_width"`
	InfoPaneWidth     float32         `json:"info_pane_width"`
	EnterSends        bool            `json:"enter_sends"`
	SendReadReceipts  bool            `json:"send_read_receipts"`
	SendTyping        bool            `json:"send_typing"`
	AutoDownload      bool            `json:"auto_download"`
	// AutoDownloadImages is auto_download's former name, read only.
	AutoDownloadImages    *bool           `json:"auto_download_images,omitempty"`
	ShowWallpaper         bool            `json:"show_wallpaper"`
	WallpaperColor        string          `json:"wallpaper_color"`
	DarkWallpaperColor    string          `json:"dark_wallpaper_color"`
	WallpaperImage        *string         `json:"wallpaper_image"`
	LastChat              *string         `json:"last_chat"`
	ShowShortcutHints     bool            `json:"show_shortcut_hints"`
	Transparency          *bool           `json:"transparency,omitempty"` // the Go port's; absent: on
	RecentEmoji           []string        `json:"recent_emoji"`
	ReactionEmoji         []reactionCount `json:"reaction_emoji"`
	GiphyKey              string          `json:"giphy_key"`
	KeepRunning           bool            `json:"keep_running_in_background"`
	Notifications         bool            `json:"notifications"`
	NotificationContent   bool            `json:"notification_content"`
	MessageSound          soundFile       `json:"message_sound"`
	MentionSound          soundFile       `json:"mention_sound"`
	GroupSounds           bool            `json:"group_sounds"`
	DownloadFolder        *string         `json:"download_folder"`
	KeepChatsArchived     bool            `json:"keep_chats_archived"`
	Proxy                 string          `json:"proxy"`
	CheckForUpdates       bool            `json:"check_for_updates"`
	DownloadUpdates       bool            `json:"download_updates_automatically"`
	VoiceSpeed            float32         `json:"voice_speed"`
	PauseOtherMedia       bool            `json:"pause_other_media"`
	PauseWhileRecording   *bool           `json:"pause_media_while_recording,omitempty"` // read only
	PauseWhilePlaying     *bool           `json:"pause_media_while_playing,omitempty"`   // read only
	SaveContactsToPhone   bool            `json:"save_contacts_to_phone"`
	ChatLockCode          *string         `json:"chat_lock_code,omitempty"` // legacy plaintext, read only
	ChatLockCodeHash      *string         `json:"chat_lock_code_hash"`
	ChatLockHintDismissed bool            `json:"chat_lock_hint_dismissed"`
	AppLockHash           *string         `json:"app_lock_hash"`
	AppLockAfter          string          `json:"app_lock_after"`
	WindowWidth           *float32        `json:"window_width"`
	WindowHeight          *float32        `json:"window_height"`
	WindowX               *float32        `json:"window_x"`
	WindowY               *float32        `json:"window_y"`
	WindowMaximized       bool            `json:"window_maximized"`
}

// accountFile is accounts/<id>/settings.json (zf:src/settings.rs:828).
type accountFile struct {
	present             bool         // distinguishes a read {} from a missing file
	SendReadReceipts    bool         `json:"send_read_receipts"`
	SendTyping          bool         `json:"send_typing"`
	AutoDownload        bool         `json:"auto_download"`
	AutoDownloadImages  *bool        `json:"auto_download_images,omitempty"` // read only
	LastChat            *string      `json:"last_chat"`
	Notifications       bool         `json:"notifications"`
	LimitNotifications  string       `json:"limit_notifications"`
	SaveContactsToPhone bool         `json:"save_contacts_to_phone"`
	WallpaperImage      *string      `json:"wallpaper_image"`
	HistoryPrefetch     bool         `json:"history_prefetch"`
	ListFilter          *string      `json:"list_filter"`
	CallFilter          *string      `json:"call_filter"`
	Folders             []folderFile `json:"folders,omitempty"`
	SpaceTint           string       `json:"space_tint,omitempty"`
	SpaceTintMix        string       `json:"space_tint_mix,omitempty"`
	SpaceTintLevel      float32      `json:"space_tint_level,omitempty"`
	SpaceTintColors     []string     `json:"space_tint_colors,omitempty"`
	SpaceTintMode       string       `json:"space_tint_mode,omitempty"`
	SpaceTintGrain      float32      `json:"space_tint_grain,omitempty"`
}

// folderFile is a Folder in the account's settings file.
type folderFile struct {
	ID         string   `json:"id"`
	Name       string   `json:"name"`
	Icon       string   `json:"icon,omitempty"`
	Chats      []string `json:"chats,omitempty"`
	Collapsed  bool     `json:"collapsed,omitempty"`
	Space      bool     `json:"space,omitempty"`
	Tint       string   `json:"tint,omitempty"`
	TintMix    string   `json:"tint_mix,omitempty"`
	TintLevel  float32  `json:"tint_level,omitempty"`
	TintColors []string `json:"tint_colors,omitempty"`
	TintMode   string   `json:"tint_mode,omitempty"`
	TintGrain  float32  `json:"tint_grain,omitempty"`
}

func foldersToFile(fs []Folder) []folderFile {
	if len(fs) == 0 {
		return nil
	}
	out := make([]folderFile, len(fs))
	for i, f := range fs {
		out[i] = folderFile{ID: f.ID, Name: f.Name, Icon: f.Icon, Collapsed: f.Collapsed, Space: f.Space, Tint: f.Tint, TintMix: f.TintMix, TintLevel: f.TintLevel, TintColors: dotsToFile(f.TintDots), TintMode: f.TintMode, TintGrain: f.TintGrain}
		for _, c := range f.Chats {
			out[i].Chats = append(out[i].Chats, string(c))
		}
	}
	return out
}

// dotsToFile are a space theme's own colours as the file keeps them,
// "#rrggbb" each; nil for none.
func dotsToFile(d [3]uint32) []string {
	var out []string
	for _, c := range d {
		if c != 0 {
			out = append(out, fmt.Sprintf("#%06x", c>>8))
		}
	}
	return out
}

// dotsFromFile reads dotsToFile's colours back; what does not read is left
// out.
func dotsFromFile(cs []string) [3]uint32 {
	var d [3]uint32
	n := 0
	for _, s := range cs {
		v, err := strconv.ParseUint(strings.TrimPrefix(s, "#"), 16, 32)
		if err != nil || len(s) != 7 || n == len(d) {
			continue
		}
		d[n] = uint32(v)<<8 | 0xFF
		n++
	}
	return d
}

func foldersFromFile(fs []folderFile) []Folder {
	if len(fs) == 0 {
		return nil
	}
	out := make([]Folder, len(fs))
	for i, f := range fs {
		out[i] = Folder{ID: f.ID, Name: f.Name, Icon: f.Icon, Collapsed: f.Collapsed, Space: f.Space, Tint: f.Tint, TintMix: f.TintMix, TintLevel: f.TintLevel, TintDots: dotsFromFile(f.TintColors), TintMode: f.TintMode, TintGrain: f.TintGrain}
		for _, c := range f.Chats {
			out[i].Chats = append(out[i].Chats, []byte(c))
		}
	}
	return out
}

func (f *settingsFile) UnmarshalJSON(data []byte) error {
	type fields settingsFile
	if err := json.Unmarshal(data, (*fields)(f)); err != nil {
		return err
	}
	f.present = true
	return nil
}

func (f *accountFile) UnmarshalJSON(data []byte) error {
	type fields accountFile
	if err := json.Unmarshal(data, (*fields)(f)); err != nil {
		return err
	}
	f.present = true
	return nil
}

// Roster is accounts.json: which accounts exist on this computer, in the
// switcher's order (zf:src/settings.rs:915).
type Roster struct {
	NextID uint32   `json:"next_id"`
	Active string   `json:"active"`
	Order  []string `json:"order"`
}

// UnmarshalJSON matches AccountRoster's required serde fields, not settings'
// missing-field defaults. Unknown fields remain forward-compatible.
func (r *Roster) UnmarshalJSON(data []byte) error {
	d := json.NewDecoder(bytes.NewReader(data))
	if first, err := d.Token(); err != nil || first != json.Delim('{') {
		return errors.New("account roster must be an object")
	}
	var next *uint32
	var active *string
	var order []*string
	var seen uint8
	for d.More() {
		key, err := d.Token()
		if err != nil {
			return err
		}
		var bit uint8
		var target any
		var ignored json.RawMessage
		switch key {
		case "next_id":
			bit, target = 1, &next
		case "active":
			bit, target = 2, &active
		case "order":
			bit, target = 4, &order
		default:
			target = &ignored
		}
		if seen&bit != 0 {
			return errors.New("duplicate account roster field")
		}
		seen |= bit
		if err := d.Decode(target); err != nil {
			return err
		}
	}
	if _, err := d.Token(); err != nil {
		return err
	}
	if next == nil || active == nil || order == nil {
		return errors.New("account roster requires non-null next_id, active and order")
	}
	out := Roster{NextID: *next, Active: *active, Order: make([]string, len(order))}
	for i, id := range order {
		if id == nil {
			return errors.New("account roster order must contain only strings")
		}
		out.Order[i] = *id
	}
	*r = out
	return nil
}

// DefaultRoster is the first account alone.
func DefaultRoster() Roster { return Roster{NextID: 2, Active: "1", Order: []string{"1"}} }

// SafeAccountID reports whether id can be an account's folder name: digits,
// no leading zero (zf:src/model.rs, AccountId::is_safe).
func SafeAccountID(id string) bool {
	if id == "" || id[0] == '0' {
		return false
	}
	for i := 0; i < len(id); i++ {
		if id[i] < '0' || id[i] > '9' {
			return false
		}
	}
	return true
}

// Sanitize drops ids that cannot be folder names and repeated ones (the
// first copy stays), so a hand-edited file cannot point the state or a
// later delete at an arbitrary path (zf:src/settings.rs:945).
func (r *Roster) Sanitize() {
	seen := map[string]bool{}
	order := r.Order[:0]
	highest := uint32(1)
	for _, id := range r.Order {
		if !SafeAccountID(id) || seen[id] {
			continue
		}
		seen[id] = true
		order = append(order, id)
		if n, err := strconv.ParseUint(id, 10, 32); err == nil {
			highest = max(highest, uint32(n))
		}
	}
	r.Order = order
	if !seen[r.Active] {
		r.Active = "1"
		if len(order) > 0 {
			r.Active = order[0]
		}
	}
	r.NextID = max(r.NextID, highest+1, 2)
}

// Allocate adds the next account and makes it the active one.
func (r *Roster) Allocate() string {
	id := strconv.FormatUint(uint64(r.NextID), 10)
	r.NextID++
	r.Order = append(r.Order, id)
	r.Active = id
	return id
}

// reactionCount is one of reaction_emoji's [emoji, count] pairs.
type reactionCount struct {
	Emoji string
	Count uint32
}

func (r reactionCount) MarshalJSON() ([]byte, error) {
	return json.Marshal([2]any{r.Emoji, r.Count})
}

func (r *reactionCount) UnmarshalJSON(b []byte) error {
	var pair []json.RawMessage
	if err := json.Unmarshal(b, &pair); err != nil {
		return err
	}
	if len(pair) != 2 {
		return errors.New("zapfast/state: a reaction count is [emoji, count]")
	}
	if err := json.Unmarshal(pair[0], &r.Emoji); err != nil {
		return err
	}
	return json.Unmarshal(pair[1], &r.Count)
}

// soundFile is a NotificationSound as serde writes it: "receive", "alert",
// "system", "none", or {"custom": "/path"}; "chime" and "ripple" are
// Receive's and Alert's former names.
type soundFile struct {
	Name string
	Path string
}

func (s soundFile) MarshalJSON() ([]byte, error) {
	if s.Name == "custom" {
		return json.Marshal(map[string]string{"custom": s.Path})
	}
	return json.Marshal(s.Name)
}

func (s *soundFile) UnmarshalJSON(b []byte) error {
	if bytes.HasPrefix(bytes.TrimSpace(b), []byte("{")) {
		var m map[string]string
		if err := json.Unmarshal(b, &m); err != nil {
			return err
		}
		p, ok := m["custom"]
		if !ok {
			return errors.New("zapfast/state: unknown notification sound")
		}
		*s = soundFile{Name: "custom", Path: p}
		return nil
	}
	return json.Unmarshal(b, &s.Name)
}

var soundNames = [...]string{SoundReceive: "receive", SoundAlert: "alert", SoundSystem: "system", SoundNone: "none", SoundCustom: "custom"}

func soundToFile(c SoundChoice) soundFile {
	if int(c.Sound) >= len(soundNames) {
		return soundFile{Name: "receive"}
	}
	return soundFile{Name: soundNames[c.Sound], Path: string(c.Path)}
}

func soundFromFile(f soundFile, def SoundChoice) SoundChoice {
	switch f.Name {
	case "receive", "chime":
		return SoundChoice{Sound: SoundReceive}
	case "alert", "ripple":
		return SoundChoice{Sound: SoundAlert}
	case "system":
		return SoundChoice{Sound: SoundSystem}
	case "none":
		return SoundChoice{Sound: SoundNone}
	case "custom":
		return SoundChoice{Sound: SoundCustom, Path: []byte(f.Path)}
	}
	return def
}

var (
	themeNames = [...]string{ThemeDark: "dark", ThemeLight: "light", ThemeSystem: "system"}
	fontNames  = [...]string{FontSystem: "system", FontInter: "inter"}
	lockNames  = [...]string{LockOneMinute: "one_minute", LockFifteenMinutes: "fifteen_minutes", LockOneHour: "one_hour"}
	limitNames = [...]string{LimitOff: "off", LimitDirect: "direct", LimitGroup: "group", LimitBoth: "both"}
)

func nameOf[E ~uint8](names []string, v E) string {
	if int(v) < len(names) {
		return names[v]
	}
	return names[0]
}

func parseName[E ~uint8](names []string, s string, def E) E {
	for i, n := range names {
		if n == s {
			return E(i)
		}
	}
	return def
}

func wallpaperKey(w Wallpaper) string {
	if w >= numWallpapers {
		return "theme"
	}
	return wallpapers[w]
}

func parseWallpaper(s string, def Wallpaper) Wallpaper {
	for i := range wallpapers {
		if wallpapers[i] == s {
			return Wallpaper(i)
		}
	}
	return def
}

func optString(b []byte) *string {
	if b == nil {
		return nil
	}
	s := string(b)
	return &s
}

func fromOpt(s *string) []byte {
	if s == nil {
		return nil
	}
	return []byte(*s)
}

func optFloat(v float32, has bool) *float32 {
	if !has {
		return nil
	}
	return &v
}

// toFile is the settings as the file stores them.
func (st *Settings) toFile() settingsFile {
	f := settingsFile{
		Version: max(st.Version, SettingsVersion), Theme: nameOf(themeNames[:], st.Theme), Font: nameOf(fontNames[:], st.Font),
		CustomTitlebar: st.CustomTitlebar, InterfaceLanguage: optString(nonEmpty(st.InterfaceLanguage)),
		CustomTheme: optString(nonEmpty(st.CustomTheme)), CustomThemeCache: json.RawMessage(nonEmpty(st.CustomThemeCache)),
		SystemThemeCache: json.RawMessage(nonEmpty(st.SystemThemeCache)),
		Zoom:             st.Zoom, SidebarWidth: st.SidebarWidth, SearchPaneWidth: st.SearchPaneWidth, InfoPaneWidth: st.InfoPaneWidth,
		EnterSends: st.EnterSends, SendReadReceipts: st.SendReadReceipts, SendTyping: st.SendTyping, AutoDownload: st.AutoDownload,
		ShowWallpaper: st.ShowWallpaper, WallpaperColor: wallpaperKey(st.WallpaperColor), DarkWallpaperColor: wallpaperKey(st.DarkWallpaper),
		WallpaperImage: optString(nonEmpty(st.WallpaperImage)), LastChat: optString(nonEmpty(st.LastChat)),
		ShowShortcutHints: st.ShowShortcutHints, RecentEmoji: make([]string, 0, len(st.RecentEmoji)),
		ReactionEmoji: make([]reactionCount, 0, len(st.ReactionEmoji)), GiphyKey: string(st.GiphyKey),
		KeepRunning: st.KeepRunning, Notifications: st.Notifications, NotificationContent: st.NotificationText,
		MessageSound: soundToFile(st.MessageSound), MentionSound: soundToFile(st.MentionSound), GroupSounds: st.GroupSounds,
		DownloadFolder: optString(nonEmpty(st.DownloadFolder)), KeepChatsArchived: st.KeepChatsArchived, Proxy: string(st.Proxy),
		CheckForUpdates: st.CheckForUpdates, DownloadUpdates: st.AutoUpdate, VoiceSpeed: st.VoiceSpeed,
		PauseOtherMedia: st.PauseOtherMedia, SaveContactsToPhone: st.SaveContactsToPhone,
		ChatLockCodeHash: optString(nonEmpty(st.ChatLockHash)), ChatLockHintDismissed: st.ChatLockHintGone,
		AppLockHash: optString(nonEmpty(st.AppLockHash)), AppLockAfter: nameOf(lockNames[:], st.AppLockAfter),
		WindowWidth: optFloat(st.WindowW, st.WindowW > 0 && st.WindowH > 0), WindowHeight: optFloat(st.WindowH, st.WindowW > 0 && st.WindowH > 0),
		WindowX: optFloat(st.WindowX, st.HasWindowPos), WindowY: optFloat(st.WindowY, st.HasWindowPos),
		WindowMaximized: st.WindowMaximized,
	}
	if !st.Transparency { // on, the default, is left out
		f.Transparency = &st.Transparency
	}
	for _, e := range st.RecentEmoji {
		f.RecentEmoji = append(f.RecentEmoji, string(e))
	}
	for _, r := range st.ReactionEmoji {
		f.ReactionEmoji = append(f.ReactionEmoji, reactionCount{string(r.Emoji), r.Count})
	}
	return f
}

// compact is a cached palette as one line (the file indents it).
func compact(raw json.RawMessage) []byte {
	var b bytes.Buffer
	if len(raw) == 0 || json.Compact(&b, raw) != nil {
		return nil
	}
	return b.Bytes()
}

func nonEmpty(b []byte) []byte {
	if len(b) == 0 {
		return nil
	}
	return b
}

// fromFile reads a loaded file over the defaults, then migrates it
// (zf:src/settings.rs:687-775). It reports whether the file needs writing
// again (a migration or the legacy locked-chat code's verifier).
func (f *settingsFile) settings() (Settings, bool) {
	d := DefaultSettings()
	st := Settings{
		Version: f.Version, Theme: parseName(themeNames[:], f.Theme, d.Theme), Font: parseName(fontNames[:], f.Font, d.Font),
		CustomTitlebar: f.CustomTitlebar, InterfaceLanguage: fromOpt(f.InterfaceLanguage), CustomTheme: fromOpt(f.CustomTheme),
		CustomThemeCache: compact(f.CustomThemeCache), SystemThemeCache: compact(f.SystemThemeCache),
		Zoom: f.Zoom, SidebarWidth: f.SidebarWidth, SearchPaneWidth: f.SearchPaneWidth, InfoPaneWidth: f.InfoPaneWidth,
		EnterSends: f.EnterSends, SendReadReceipts: f.SendReadReceipts, SendTyping: f.SendTyping, AutoDownload: f.AutoDownload,
		ShowWallpaper: f.ShowWallpaper, WallpaperColor: parseWallpaper(f.WallpaperColor, d.WallpaperColor),
		DarkWallpaper: parseWallpaper(f.DarkWallpaperColor, d.DarkWallpaper), WallpaperImage: fromOpt(f.WallpaperImage),
		LastChat: fromOpt(f.LastChat), ShowShortcutHints: f.ShowShortcutHints, GiphyKey: []byte(f.GiphyKey),
		KeepRunning: f.KeepRunning, Notifications: f.Notifications, NotificationText: f.NotificationContent,
		MessageSound: soundFromFile(f.MessageSound, d.MessageSound), MentionSound: soundFromFile(f.MentionSound, d.MentionSound),
		GroupSounds: f.GroupSounds, DownloadFolder: fromOpt(f.DownloadFolder), KeepChatsArchived: f.KeepChatsArchived,
		Proxy: []byte(f.Proxy), CheckForUpdates: f.CheckForUpdates, AutoUpdate: f.DownloadUpdates, VoiceSpeed: f.VoiceSpeed,
		PauseOtherMedia: f.PauseOtherMedia, SaveContactsToPhone: f.SaveContactsToPhone,
		ChatLockHash: fromOpt(f.ChatLockCodeHash), ChatLockHintGone: f.ChatLockHintDismissed,
		AppLockHash: fromOpt(f.AppLockHash), AppLockAfter: parseName(lockNames[:], f.AppLockAfter, d.AppLockAfter),
		WindowMaximized: f.WindowMaximized,
	}
	if f.AutoDownloadImages != nil {
		st.AutoDownload = *f.AutoDownloadImages
	}
	st.Transparency = d.Transparency
	if f.Transparency != nil {
		st.Transparency = *f.Transparency
	}
	for _, e := range f.RecentEmoji {
		st.RecentEmoji = append(st.RecentEmoji, []byte(e))
	}
	for _, r := range f.ReactionEmoji {
		st.ReactionEmoji = append(st.ReactionEmoji, EmojiCount{[]byte(r.Emoji), r.Count})
	}
	// A size or a position counts only whole.
	if f.WindowWidth != nil && f.WindowHeight != nil {
		st.WindowW, st.WindowH = *f.WindowWidth, *f.WindowHeight
	}
	if f.WindowX != nil && f.WindowY != nil {
		st.WindowX, st.WindowY, st.HasWindowPos = *f.WindowX, *f.WindowY, true
	}
	dirty := false
	// The Messages theme is gone: its "messages" (development builds) is
	// Follow system, as upstream's "system" with the messages_theme mark is.
	if f.Theme == "messages" {
		st.Theme = ThemeSystem
	}
	if st.Version < 1 {
		// Old files store every field, so a default colour cannot be told
		// from a chosen one: the old defaults move to the new one.
		if st.WallpaperColor == WallpaperBeige {
			st.WallpaperColor = WallpaperTheme
		}
		if st.DarkWallpaper == WallpaperBlack {
			st.DarkWallpaper = WallpaperTheme
		}
		dirty = true
	}
	st.Version = max(st.Version, SettingsVersion)
	// The former recording and playback switches fold into one: media keeps
	// pausing only if neither was turned off.
	if f.PauseWhileRecording != nil || f.PauseWhilePlaying != nil {
		st.PauseOtherMedia = (f.PauseWhileRecording == nil || *f.PauseWhileRecording) && (f.PauseWhilePlaying == nil || *f.PauseWhilePlaying)
		dirty = true
	}
	if f.ChatLockCode != nil {
		st.SetChatLockCode(*f.ChatLockCode)
		dirty = true
	}
	return st, dirty
}

// defaultSettingsFile is what a missing field reads as. A file without a
// version predates it: version 0, which the migrations read.
func defaultSettingsFile() settingsFile {
	d := DefaultSettings()
	f := d.toFile()
	f.Version = 0
	return f
}

func (a *AccountSettings) toFile() accountFile {
	return accountFile{
		SendReadReceipts: a.SendReadReceipts, SendTyping: a.SendTyping, AutoDownload: a.AutoDownload,
		LastChat: optString(nonEmpty(a.LastChat)), Notifications: a.Notifications,
		LimitNotifications: nameOf(limitNames[:], a.LimitNotifications), SaveContactsToPhone: a.SaveContactsToPhone,
		WallpaperImage: optString(nonEmpty(a.WallpaperImage)), HistoryPrefetch: a.HistoryPrefetch,
		ListFilter: optString(nonEmpty(a.ListFilter)), CallFilter: optString(nonEmpty(a.CallFilter)),
		Folders: foldersToFile(a.Folders), SpaceTint: a.SpaceTint,
		SpaceTintMix: a.SpaceTintMix, SpaceTintLevel: a.SpaceTintLevel, SpaceTintColors: dotsToFile(a.SpaceTintDots), SpaceTintMode: a.SpaceTintMode, SpaceTintGrain: a.SpaceTintGrain,
	}
}

func (f *accountFile) settings() AccountSettings {
	a := AccountSettings{
		SendReadReceipts: f.SendReadReceipts, SendTyping: f.SendTyping, AutoDownload: f.AutoDownload,
		LastChat: fromOpt(f.LastChat), Notifications: f.Notifications,
		LimitNotifications: parseName(limitNames[:], f.LimitNotifications, LimitOff), SaveContactsToPhone: f.SaveContactsToPhone,
		WallpaperImage: fromOpt(f.WallpaperImage), HistoryPrefetch: f.HistoryPrefetch,
		ListFilter: fromOpt(f.ListFilter), CallFilter: fromOpt(f.CallFilter),
		Folders: foldersFromFile(f.Folders), SpaceTint: f.SpaceTint,
		SpaceTintMix: f.SpaceTintMix, SpaceTintLevel: f.SpaceTintLevel, SpaceTintDots: dotsFromFile(f.SpaceTintColors), SpaceTintMode: f.SpaceTintMode, SpaceTintGrain: f.SpaceTintGrain,
	}
	if f.AutoDownloadImages != nil {
		a.AutoDownload = *f.AutoDownloadImages
	}
	return a
}

func defaultAccountFile() accountFile {
	d := DefaultAccountSettings()
	return d.toFile()
}
