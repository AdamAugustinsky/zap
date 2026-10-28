package state

import (
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"
)

// The tests of zf:src/settings.rs:981-1484, on the Go files.

func loadDir(t *testing.T, dir string) *Loaded {
	t.Helper()
	l, ok := LoadSettings(Paths{Config: dir, Data: dir}).Wait(5 * time.Second)
	if !ok {
		t.Fatal("the settings did not load")
	}
	// Every store it opened is closed (a second Close waits for the first)
	// before the temporary folder goes: a store saving into it as it is
	// removed failed the cleanup ("directory not empty").
	t.Cleanup(func() {
		l.global.Close()
		l.roster.Close()
		for _, as := range l.accounts {
			as.store.Close()
		}
	})
	return l
}

// loadFrom loads contents, saves the result whole, and returns both.
func loadFrom(t *testing.T, contents string) (Settings, map[string]any) {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "settings.json")
	if err := os.WriteFile(path, []byte(contents), 0o600); err != nil {
		t.Fatal(err)
	}
	l := loadDir(t, dir)
	l.global.Set(l.Settings.toFile())
	if err := l.global.Close(); err != nil {
		t.Fatal(err)
	}
	return l.Settings, readJSON(t, path)
}

func readJSON(t *testing.T, path string) map[string]any {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var m map[string]any
	if err := json.Unmarshal(b, &m); err != nil {
		t.Fatal(err)
	}
	return m
}

func saveLoad(t *testing.T, st Settings) Settings {
	t.Helper()
	dir := t.TempDir()
	l := loadDir(t, dir)
	l.global.Set(st.toFile())
	l.global.Close()
	return loadDir(t, dir).Settings
}

func TestEarlierSoundNamesAndRemovedGroupSound(t *testing.T) {
	st, _ := loadFrom(t, `{"message_sound":"chime","mention_sound":"ripple"}`)
	if st.MessageSound.Sound != SoundReceive || st.MentionSound.Sound != SoundAlert {
		t.Errorf("sounds %v %v", st.MessageSound, st.MentionSound)
	}
	st, stored := loadFrom(t, `{"message_sound":"system","group_sound":"none"}`)
	if st.MessageSound.Sound != SoundSystem || st.MentionSound.Sound != SoundAlert || !st.GroupSounds {
		t.Errorf("group sound: %v %v %v", st.MessageSound, st.MentionSound, st.GroupSounds)
	}
	if _, ok := stored["group_sound"]; ok {
		t.Error("the removed field was saved")
	}
}

func TestTheFontAndTheGoneMessagesTheme(t *testing.T) {
	st, _ := loadFrom(t, `{"theme":"light"}`)
	if st.Font != FontSystem || st.Theme != ThemeLight {
		t.Errorf("font %v theme %v", st.Font, st.Theme)
	}
	st, stored := loadFrom(t, `{"font":"inter"}`)
	if st.Font != FontInter || stored["font"] != "inter" {
		t.Errorf("font %v saved %v", st.Font, stored["font"])
	}
	// The Messages theme is gone: upstream's mark and development builds'
	// "messages" both load as Follow system.
	for _, file := range []string{`{"theme":"system","messages_theme":true}`, `{"theme":"messages"}`} {
		if st, stored := loadFrom(t, file); st.Theme != ThemeSystem || stored["theme"] != "system" {
			t.Errorf("%s: %v, stored %v", file, st.Theme, stored["theme"])
		}
	}
}

func TestUnknownAndMissingFieldsAndTheWindow(t *testing.T) {
	st, _ := loadFrom(t, `{"theme":"light","future_field":1,"window_width":1280,"window_x":100}`)
	if st.Theme != ThemeLight || !st.EnterSends || !st.CheckForUpdates || st.AutoUpdate || !st.ShowWallpaper ||
		st.WallpaperColor != WallpaperTheme || !st.PauseOtherMedia || st.WindowMaximized || !st.Transparency {
		t.Errorf("defaults: %+v", st)
	}
	if st.WindowW != 0 || st.WindowH != 0 || st.HasWindowPos {
		t.Error("half a size or position counted")
	}
	in := DefaultSettings()
	in.Zoom, in.WindowW, in.WindowH, in.WindowX, in.WindowY, in.HasWindowPos, in.WindowMaximized = 1.25, 1280, 800, 100, 50, true, true
	if out := saveLoad(t, in); out.WindowW != 1280 || out.WindowH != 800 || out.WindowX != 100 || out.WindowY != 50 || !out.HasWindowPos || !out.WindowMaximized || out.Zoom != 1.25 {
		t.Errorf("geometry round trip: %+v", out)
	}
}

func TestRemovedSettingsAndTheMediaPauseSwitches(t *testing.T) {
	st, stored := loadFrom(t, `{"enter_sends":false,"label_chips":true,"show_sender_pictures":true,
		"names_from_contacts":false,"collapse_chat_list":false,"save_contacts_to_phone":false,"keep_chat_list_position":true}`)
	if st.EnterSends || st.SaveContactsToPhone {
		t.Error("the rest of the file did not load")
	}
	for _, k := range []string{"label_chips", "show_sender_pictures", "names_from_contacts", "collapse_chat_list",
		"pause_media_while_recording", "pause_media_while_playing", "keep_chat_list_position"} {
		if _, ok := stored[k]; ok {
			t.Errorf("%s is saved", k)
		}
	}
	for contents, want := range map[string]bool{
		`{"pause_media_while_recording":true,"pause_media_while_playing":true}`:  true,
		`{"pause_media_while_recording":false,"pause_media_while_playing":true}`: false,
		`{"pause_media_while_recording":true,"pause_media_while_playing":false}`: false,
		`{"pause_media_while_playing":false}`:                                    false,
		`{"pause_media_while_recording":true}`:                                   true,
		`{}`:                                                                     true,
		`{"pause_other_media":false}`:                                            false,
	} {
		if st, _ := loadFrom(t, contents); st.PauseOtherMedia != want {
			t.Errorf("%s: %v", contents, st.PauseOtherMedia)
		}
	}
}

func TestRoundTripsThroughDisk(t *testing.T) {
	in := DefaultSettings()
	in.Zoom, in.EnterSends, in.VoiceSpeed, in.PauseOtherMedia = 1.25, false, 1.5, false
	in.InterfaceLanguage = []byte("de")
	in.MessageSound = SoundChoice{Sound: SoundNone}
	in.MentionSound = SoundChoice{Sound: SoundCustom, Path: []byte("/sounds/ding.wav")}
	in.GroupSounds, in.Transparency = false, false
	in.RecentEmoji = [][]byte{[]byte("😀"), []byte("👍")}
	in.ReactionEmoji = []EmojiCount{{[]byte("❤️"), 4}}
	in.WallpaperImage = []byte("/w/wallpaper.png")
	in.CustomThemeCache = []byte(`{"palette":{"dark":true}}`)
	out := saveLoad(t, in)
	if hashOf(&out) != hashOf(&in) {
		t.Errorf("round trip:\n in %+v\nout %+v", in, out)
	}
	dir := t.TempDir()
	l := loadDir(t, dir)
	l.global.Set(in.toFile())
	l.global.Close()
	b, _ := os.ReadFile(filepath.Join(dir, "settings.json"))
	for _, want := range []string{`"interface_language": "de"`, `"custom": "/sounds/ding.wav"`, `"transparency": false`, `"reaction_emoji": [`} {
		if !strings.Contains(string(b), want) {
			t.Errorf("the file lacks %s:\n%s", want, b)
		}
	}
}

func TestTheChatLockCode(t *testing.T) {
	var st Settings
	st.SetChatLockCode(" 1234 ")
	if !st.VerifyChatLockCode("1234") || st.VerifyChatLockCode("1235") || st.VerifyChatLockCode("") {
		t.Error("the verifier")
	}
	first := string(st.ChatLockHash)
	st.SetChatLockCode("1234")
	if string(st.ChatLockHash) == first || !st.VerifyChatLockCode("1234") {
		t.Error("the same code stored the same value")
	}
	st.SetChatLockCode("")
	if st.VerifyChatLockCode("1234") {
		t.Error("a removed code still verifies")
	}
	// A legacy plaintext code is rewritten as a verifier.
	st, stored := loadFrom(t, `{"chat_lock_code":"1234"}`)
	if !st.VerifyChatLockCode("1234") {
		t.Error("the legacy code does not verify")
	}
	v, _ := stored["chat_lock_code_hash"].(string)
	if _, ok := stored["chat_lock_code"]; ok || !strings.Contains(v, "$") || strings.TrimSpace(v) == "1234" {
		t.Errorf("stored %v", stored)
	}
}

// Nothing draws the wallpaper, but the file keeps upstream's values.
func TestWallpapers(t *testing.T) {
	// The old default colours become the theme's, once.
	st, stored := loadFrom(t, `{"wallpaper_color":"beige","dark_wallpaper_color":"black"}`)
	if st.WallpaperColor != WallpaperTheme || st.DarkWallpaper != WallpaperTheme || stored["version"] != float64(SettingsVersion) || stored["wallpaper_color"] != "theme" {
		t.Errorf("migration: %v %v %v", st.WallpaperColor, st.DarkWallpaper, stored["version"])
	}
	if st, _ := loadFrom(t, `{"wallpaper_color":"cruise","dark_wallpaper_color":"nordic"}`); st.WallpaperColor != WallpaperCruise || st.DarkWallpaper != WallpaperNordic {
		t.Error("chosen colours did not survive")
	}
	if st, _ := loadFrom(t, `{"wallpaper_color":"beige","dark_wallpaper_color":"tiber"}`); st.WallpaperColor != WallpaperTheme || st.DarkWallpaper != WallpaperTiber {
		t.Error("partial migration")
	}
	if st, _ := loadFrom(t, `{"version":1,"wallpaper_color":"beige","dark_wallpaper_color":"black"}`); st.WallpaperColor != WallpaperBeige || st.DarkWallpaper != WallpaperBlack {
		t.Error("a migrated file's Beige and Black did not stick")
	}
}

func TestTheRoster(t *testing.T) {
	for _, id := range []string{"1", "12"} {
		if !SafeAccountID(id) {
			t.Errorf("%q unsafe", id)
		}
	}
	for _, id := range []string{"", "0", "01", "..", "/tmp", "1/../etc"} {
		if SafeAccountID(id) {
			t.Errorf("%q safe", id)
		}
	}
	r := Roster{NextID: 4, Active: "../etc", Order: []string{"1", "..", "/tmp"}}
	r.Sanitize()
	if !slices.Equal(r.Order, []string{"1"}) || r.Active != "1" || r.NextID <= 1 {
		t.Errorf("unsafe ids: %+v", r)
	}
	r = Roster{NextID: 3, Active: "1", Order: []string{"1", "1", "2"}}
	r.Sanitize()
	if !slices.Equal(r.Order, []string{"1", "2"}) {
		t.Errorf("repeated id: %v", r.Order)
	}
	r = DefaultRoster()
	if id := r.Allocate(); id != "2" || r.NextID != 3 || r.Active != "2" || !slices.Equal(r.Order, []string{"1", "2"}) {
		t.Errorf("allocate: %q %+v", id, r)
	}
	if l := loadDir(t, t.TempDir()); !slices.Equal(l.Roster.Order, []string{"1"}) || l.Roster.Active != "1" {
		t.Errorf("a missing roster: %+v", l.Roster)
	}
}

func TestAccountSettings(t *testing.T) {
	for _, c := range []struct {
		l             NotificationLimit
		direct, group bool
	}{{LimitOff, false, false}, {LimitDirect, true, false}, {LimitGroup, false, true}, {LimitBoth, true, true}} {
		if c.l.Applies(false) != c.direct || c.l.Applies(true) != c.group {
			t.Errorf("%v", c.l)
		}
	}
	dir := t.TempDir()
	// The first account starts from the single-account settings.
	os.WriteFile(filepath.Join(dir, "settings.json"), []byte(`{"version":1,"send_read_receipts":false,"last_chat":"1@s.whatsapp.net"}`), 0o600)
	l := loadDir(t, dir)
	a1 := l.accounts["1"]
	if a1.st.SendReadReceipts || string(a1.st.LastChat) != "1@s.whatsapp.net" || a1.st.LimitNotifications != LimitOff {
		t.Errorf("migrated: %+v", a1.st)
	}
	a1.st.LimitNotifications, a1.st.HistoryPrefetch = LimitGroup, true
	a1.store.Set(a1.st.toFile())
	a1.store.Close()
	stored := readJSON(t, filepath.Join(dir, "accounts", "1", "settings.json"))
	if stored["limit_notifications"] != "group" || stored["history_prefetch"] != true {
		t.Errorf("stored %v", stored)
	}
	if got := loadDir(t, dir).accounts["1"].st; got.LimitNotifications != LimitGroup || got.SendReadReceipts {
		t.Errorf("reloaded %+v", got)
	}
}

// A screen changes a setting without saying so: the state saves it within
// a moment, and every account's, and the roster.
func TestTheStateSavesWhatChanged(t *testing.T) {
	dir := t.TempDir()
	s := New()
	clock := time.Unix(1_800_000_000, 0)
	s.Now = func() time.Time { return clock }
	s.UseSettings(loadDir(t, dir))
	a := s.AddAccount("1", nil, nil)
	b := s.AddAccount(s.NextAccountID(), nil, nil)
	s.Settings.EnterSends = false
	a.Filter = FilterUnread
	b.Settings.HistoryPrefetch = true
	s.Active = 1
	clock = clock.Add(checkEvery)
	s.Pump(0)
	s.CloseSettings()
	if g := readJSON(t, filepath.Join(dir, "settings.json")); g["enter_sends"] != false {
		t.Errorf("settings %v", g["enter_sends"])
	}
	if f := readJSON(t, filepath.Join(dir, "accounts", "1", "settings.json")); f["list_filter"] != "unread" {
		t.Errorf("account 1 %v", f["list_filter"])
	}
	if f := readJSON(t, filepath.Join(dir, "accounts", "2", "settings.json")); f["history_prefetch"] != true {
		t.Errorf("account 2 %v", f)
	}
	r := readJSON(t, filepath.Join(dir, "accounts.json"))
	if r["active"] != "2" || len(r["order"].([]any)) != 2 || r["next_id"] != float64(3) {
		t.Errorf("roster %v", r)
	}
	// And they come back.
	s2 := New()
	s2.UseSettings(loadDir(t, dir))
	a2 := s2.AddAccount("1", nil, nil)
	if s2.Settings.EnterSends || a2.Filter != FilterUnread || s2.Roster().Active != "2" {
		t.Errorf("reloaded: enter %v filter %v roster %+v", s2.Settings.EnterSends, a2.Filter, s2.Roster())
	}
}
