package settings

import (
	"bytes"
	"image"
	"image/png"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"gonativeui/app"
	"gonativeui/img"
	"gonativeui/ui"
	"gonativeui/widget/widgettest"

	"zapfast/abi"
	"zapfast/core/fake"
	"zapfast/i18n"
	"zapfast/lockhash"
	"zapfast/state"
)

// SE-003: the search finds a row by its name or its description, in the
// interface language or in English; a section's name keeps the whole section;
// nothing found says so.
func TestSearchFindsSettings(t *testing.T) {
	r := newRig(t, 760, 700, nil)
	connect(r)
	search := func(q string) {
		r.Play(widgettest.Click(ui.RoleSearchField, "Search settings"), widgettest.Key('A', ui.PrimaryMod()), widgettest.Type(q), widgettest.Idle(2))
	}
	search("SOUND")
	for _, want := range []string{"Message sound", "Mention sound", "Play sounds for group messages"} {
		if !r.text(want) {
			t.Errorf("sound: %q is not shown", want)
		}
	}
	if r.text("Enter sends") || r.text("Theme") {
		t.Error("sound: rows that do not match are shown")
	}
	search("github") // a description
	if !r.text("Check for updates") || r.text("Message sound") {
		t.Error("github: want only Check for updates")
	}
	search("notifications") // a section's name keeps every row in it
	for _, want := range []string{"Desktop notifications", "Show message content in notifications", "Limit repeat notifications"} {
		if !r.text(want) {
			t.Errorf("section: %q is not shown", want)
		}
	}
	search("zzqx")
	if !r.text("No settings match “zzqx”.") {
		t.Error("no match: the line is not shown")
	}
	// Escape clears the field before anything else
	r.Play(widgettest.Key(ui.KeyEscape, 0), widgettest.Idle(2))
	if r.scr.query.Len() != 0 || !r.text("Enter sends") {
		t.Errorf("Escape left %q", r.scr.query.String())
	}
}

// SE-003: found in the interface language and in English; accents fold.
func TestSearchInTheInterfaceLanguage(t *testing.T) {
	i18n.SetLocale(i18n.German)
	t.Cleanup(func() { i18n.SetLocale(i18n.English) })
	r := newRig(t, 760, 700, nil)
	connect(r)
	search := func(q string) {
		r.Play(widgettest.Click(ui.RoleSearchField, i18n.T("Search settings")), widgettest.Key('A', ui.PrimaryMod()), widgettest.Type(q), widgettest.Idle(2))
	}
	german := i18n.T("Play sounds for group messages")
	for _, q := range []string{"tone", "group messages"} { // "Töne", and the English source
		search(q)
		if !r.text(german) {
			t.Errorf("%q does not find %q", q, german)
		}
	}
}

// ⌘F (the shell's FindSettings request) focuses the search field.
func TestFindFocusesTheSearch(t *testing.T) {
	r := newRig(t, 760, 700, nil)
	r.s.Req.FindSettings = true
	r.Frames(3)
	if n, ok := r.Focused(); !ok || n.Role != ui.RoleSearchField {
		t.Errorf("focused %v %v", n.Role, ok)
	}
	if r.s.Req.FindSettings {
		t.Error("the request was not cleared")
	}
}

// SE-002, SE-020, 021, 022, 023, 030, 031, 033, 040, 041, 050, 052: the
// switches change their setting at once, and the ones that reach the core
// send their command.
func TestSwitchesChangeSettings(t *testing.T) {
	r := newRig(t, 760, 700, nil)
	connect(r)
	st, a := &r.s.Settings, r.acct()
	flip := func(name string, get func() bool) {
		t.Helper()
		gen, before := r.s.Gen(), get()
		r.click(ui.RoleSwitch, name)
		if get() == before {
			t.Errorf("%s: did not change", name)
		}
		if r.s.Gen() == gen {
			t.Errorf("%s: the state does not know it changed", name)
		}
	}
	flip("Enter sends", func() bool { return st.EnterSends })
	flip("Download files automatically", func() bool { return a.Settings.AutoDownload })
	flip("Show message content in notifications", func() bool { return st.NotificationText })
	flip("Play sounds for group messages", func() bool { return st.GroupSounds })
	flip("Send read receipts", func() bool { return a.Settings.SendReadReceipts })
	flip("Show when you are typing", func() bool { return a.Settings.SendTyping })
	flip("Keep running when the window closes", func() bool { return st.KeepRunning })
	flip("Check for updates", func() bool { return st.CheckForUpdates })
	flip("Download updates automatically", func() bool { return st.AutoUpdate })

	// The core already heard both settings when the account opened; only
	// what the switch sends counts.
	n := len(r.sent(abi.CommandTagSetHistoryPrefetch))
	flip("Fetch older history", func() bool { return a.Settings.HistoryPrefetch })
	if got := r.sent(abi.CommandTagSetHistoryPrefetch)[n:]; len(got) != 1 || !got[0].SetHistoryPrefetch.On {
		t.Errorf("SetHistoryPrefetch %v", got)
	}
	n = len(r.sent(abi.CommandTagSetKeepChatsArchived))
	flip("Keep chats archived", func() bool { return st.KeepChatsArchived })
	if got := r.sent(abi.CommandTagSetKeepChatsArchived)[n:]; len(got) != 1 || got[0].SetKeepChatsArchived.Keep {
		t.Errorf("SetKeepChatsArchived %v", got)
	}

	// SE-030: with the account's notifications off, its sound rows go away
	if !r.has(ui.RoleComboBox, "Message sound") {
		t.Fatal("no Message sound with notifications on")
	}
	flip("Desktop notifications", func() bool { return a.Settings.Notifications })
	for _, gone := range []string{"Message sound", "Limit repeat notifications"} {
		if r.has(ui.RoleComboBox, gone) {
			t.Errorf("%s is shown with notifications off", gone)
		}
	}
	if r.has(ui.RoleSwitch, "Show message content in notifications") {
		t.Error("the content switch is shown with notifications off")
	}
}

// SE-040: the receipts row says when the account has them off.
func TestReceiptsNote(t *testing.T) {
	r := newRig(t, 760, 700, nil)
	connect(r)
	if !r.text("Let people see when you read their messages.") {
		t.Error("no usual note")
	}
	r.emit(ev(abi.EventTagReceiptsPrivacy, func(e *abi.Event) { e.ReceiptsPrivacy.Disabled = true }))
	if !r.text("Off for your account, so only groups get them.") {
		t.Error("the account's receipts are off and the row does not say so")
	}
}

// SE-010, 011, 013, 014, 034: the pop-ups and the zoom.
func TestChoicesChangeSettings(t *testing.T) {
	r := newRig(t, 760, 700, nil)
	connect(r)
	st, a := &r.s.Settings, r.acct()

	r.choose("Theme", "Follow system")
	if st.Theme != state.ThemeSystem {
		t.Errorf("theme %v", st.Theme)
	}
	r.choose("Theme", "Dark")
	if st.Theme != state.ThemeDark || len(st.CustomTheme) != 0 {
		t.Errorf("theme %v %q", st.Theme, st.CustomTheme)
	}
	r.choose("Font", "Inter")
	if st.Font != state.FontInter {
		t.Errorf("font %v", st.Font)
	}
	r.choose("Limit repeat notifications", "Direct messages and groups")
	if a.Settings.LimitNotifications != state.LimitBoth {
		t.Errorf("limit %v", a.Settings.LimitNotifications)
	}

	r.click(ui.RoleButton, "Larger")
	r.click(ui.RoleButton, "Larger")
	if st.Zoom != 1.2 || !r.text("120%") {
		t.Errorf("zoom %v", st.Zoom)
	}
	for i := 0; i < 20; i++ {
		r.click(ui.RoleButton, "Smaller")
	}
	if st.Zoom != 0.6 || !r.text("60%") {
		t.Errorf("zoom %v, want the least, 0.6", st.Zoom)
	}

	i18n.SetLocale(i18n.English)
	t.Cleanup(func() { i18n.SetLocale(i18n.English) })
	r.choose("Language", "Deutsch")
	if string(st.InterfaceLanguage) != "de" || i18n.Current() != i18n.German {
		t.Errorf("language %q %v", st.InterfaceLanguage, i18n.Current())
	}
	r.Frames(2)
	if !r.has(ui.RoleSwitch, i18n.T("Enter sends")) {
		t.Error("the window is not in German")
	}
	r.choose(i18n.T("Language"), i18n.T("Auto"))
	if len(st.InterfaceLanguage) != 0 {
		t.Errorf("language %q after Auto", st.InterfaceLanguage)
	}
}

// SE-024: the code is stored as a salted slow verifier, never the code; Clear
// removes it.
func TestLockedChatsCode(t *testing.T) {
	r := newRig(t, 760, 700, nil)
	connect(r)
	r.typeIn("Locked chats code", "secret")
	if v := r.value("Locked chats code"); v != strings.Repeat("•", 6) {
		t.Errorf("the code shows as %q", v)
	}
	r.wait("the verifier", func() bool { return len(r.s.Settings.ChatLockHash) > 0 })
	h := string(r.s.Settings.ChatLockHash)
	if strings.Contains(h, "secret") || !strings.Contains(h, "$") {
		t.Errorf("verifier %q", h)
	}
	if !lockhash.VerifiesChat(string(r.s.Settings.ChatLockHash), []byte("secret")) || lockhash.VerifiesChat(string(r.s.Settings.ChatLockHash), []byte("other")) {
		t.Error("the verifier does not check the code")
	}
	r.click(ui.RoleButton, "Clear")
	if len(r.s.Settings.ChatLockHash) != 0 {
		t.Error("Clear left the code")
	}
	if r.has(ui.RoleButton, "Clear") {
		t.Error("Clear is shown without a code")
	}
}

// SE-032: the sound menus, and Choose a file….
func TestSounds(t *testing.T) {
	var played []state.Sound
	old := playSound
	playSound = func(sc state.SoundChoice) { played = append(played, sc.Sound) }
	t.Cleanup(func() { playSound = old })
	r := newRig(t, 760, 700, nil)
	connect(r)
	st := &r.s.Settings
	r.choose("Message sound", "System default")
	if st.MessageSound.Sound != state.SoundSystem {
		t.Fatalf("sound %v", st.MessageSound)
	}
	if n := countRole(r, ui.RoleButton, "Play"); n != 1 {
		t.Errorf("%d Play buttons; the system sound has none, the mention sound has one", n)
	}
	r.choose("Message sound", "None")
	if st.MessageSound.Sound != state.SoundNone {
		t.Errorf("sound %v", st.MessageSound)
	}
	r.choose("Mention sound", "Choose a file…")
	if got := r.sent(abi.CommandTagPickNotificationSound); len(got) != 1 || !got[0].PickNotificationSound.Mention {
		t.Fatalf("PickNotificationSound %v", got)
	}
	r.emit(ev(abi.EventTagNotificationSoundPicked, func(e *abi.Event) {
		e.NotificationSoundPicked.Mention, e.NotificationSoundPicked.Path = true, []byte("/tmp/chime.aiff")
	}))
	if st.MentionSound.Sound != state.SoundCustom || string(st.MentionSound.Path) != "/tmp/chime.aiff" {
		t.Errorf("mention sound %v", st.MentionSound)
	}
	if len(played) != 1 || played[0] != state.SoundCustom {
		t.Errorf("the picked sound did not play: %v", played)
	}
	if n, ok := r.Find(ui.RoleComboBox, "Mention sound"); !ok || !strings.Contains(string(r.Tree().Text(n.Value)), "chime.aiff") {
		t.Error("the menu does not show the file")
	}
	r.Play(widgettest.Click(ui.RoleButton, "Play"), widgettest.Idle(1))
	if len(played) != 2 {
		t.Errorf("Play did not play: %v", played)
	}
}

func countRole(r *rig, role ui.Role, name string) int {
	n := 0
	for _, nd := range r.Tree().Nodes {
		if nd.Role == role && string(r.Tree().Text(nd.Name)) == name {
			n++
		}
	}
	return n
}

// SE-042: the account's privacy: notes while it cannot be changed, the values
// once fetched, writes, and a refused write coming back.
func TestAccountPrivacy(t *testing.T) {
	r := newRig(t, 760, 900, nil)
	if !r.text("Connect to WhatsApp to change your account privacy.") {
		t.Error("no offline note")
	}
	connect(r)
	if !r.text("Loading your account privacy…") {
		t.Error("no loading note")
	}
	if len(r.sent(abi.CommandTagFetchAccountPrivacy)) != 1 {
		t.Errorf("sent %v", r.f.SentTags())
	}
	if r.has(ui.RoleComboBox, "Last seen") {
		t.Error("a value is shown before the phone answered")
	}
	values := func(failed bool) *abi.Event {
		return ev(abi.EventTagAccountPrivacy, func(e *abi.Event) {
			e.AccountPrivacy.Failed = failed
			e.AccountPrivacy.Values = []abi.PrivacyValue{
				{Kind: abi.PrivacyKindLastSeen, Choice: abi.PrivacyChoiceExcept},
				{Kind: abi.PrivacyKindOnline, Choice: abi.PrivacyChoiceSameAsLastSeen},
				{Kind: abi.PrivacyKindGroupAdd, Choice: abi.PrivacyChoiceContacts},
			}
		})
	}
	r.emit(values(false))
	for _, name := range []string{"Last seen", "Online", "Groups"} {
		if !r.has(ui.RoleComboBox, name) {
			t.Errorf("no %q row", name)
		}
	}
	if r.has(ui.RoleComboBox, "Profile photo") {
		t.Error("a category the phone did not report is shown")
	}
	if r.text("Loading your account privacy…") {
		t.Error("the loading note stays")
	}
	if !r.text("Change who is excluded on your phone.") {
		t.Error("an Except value does not say it is edited on the phone")
	}
	if n, _ := r.Find(ui.RoleComboBox, "Last seen"); string(r.Tree().Text(n.Value)) != "My contacts except…" {
		t.Errorf("Last seen shows %q", r.Tree().Text(n.Value))
	}

	r.choose("Groups", "Everyone")
	got := r.sent(abi.CommandTagSetAccountPrivacy)
	if len(got) != 1 || got[0].SetAccountPrivacy.Kind != abi.PrivacyKindGroupAdd || got[0].SetAccountPrivacy.Choice != abi.PrivacyChoiceEveryone {
		t.Fatalf("SetAccountPrivacy %v", got)
	}
	if n, _ := r.Find(ui.RoleComboBox, "Groups"); string(r.Tree().Text(n.Value)) != "Everyone" {
		t.Error("the choice is not shown at once")
	}
	// the write is in flight: the pop-up cannot be used again
	if n, _ := r.Find(ui.RoleComboBox, "Groups"); n.State&ui.SemDisabled == 0 {
		t.Error("the pop-up can be used while its write is in flight")
	}
	r.emit(ev(abi.EventTagAccountPrivacyFailed, func(e *abi.Event) { e.AccountPrivacyFailed.Kind = abi.PrivacyKindGroupAdd }))
	if n, _ := r.Find(ui.RoleComboBox, "Groups"); string(r.Tree().Text(n.Value)) != "My contacts" {
		t.Errorf("a refused write left %q", r.Tree().Text(n.Value))
	}
	// a failed fetch keeps the values, says so, and cannot be edited
	r.emit(values(true))
	if !r.text("Could not load your account privacy. Trying again when Zap reconnects.") {
		t.Error("no failure note")
	}
	if n, _ := r.Find(ui.RoleComboBox, "Groups"); n.State&ui.SemDisabled == 0 {
		t.Error("a stale value can be written")
	}
}

// SE-053: a proxy is checked, saved, and sent to every core; a bad one is
// refused with a toast.
func TestProxy(t *testing.T) {
	r := newRig(t, 760, 700, nil)
	connect(r)
	r.typeIn("Proxy", "ftp://proxy.lan")
	r.click(ui.RoleSwitch, "Enter sends") // the field loses the focus
	if len(r.s.Settings.Proxy) != 0 || !strings.Contains(r.toast(), "Use an http://, socks5://, or socks5h:// proxy") {
		t.Errorf("proxy %q, toast %q", r.s.Settings.Proxy, r.toast())
	}
	if len(r.sent(abi.CommandTagSetProxy)) != 0 {
		t.Error("a bad proxy was sent")
	}
	r.Play(widgettest.Click(ui.RoleTextField, "Proxy"), widgettest.Key('A', ui.PrimaryMod()), widgettest.Type("proxy.lan:3128"), widgettest.Key(ui.KeyEnter, 0), widgettest.Idle(2))
	got := r.sent(abi.CommandTagSetProxy)
	if string(r.s.Settings.Proxy) != "proxy.lan:3128" || len(got) != 1 || string(got[0].SetProxy.Proxy) != "proxy.lan:3128" {
		t.Errorf("proxy %q, sent %v", r.s.Settings.Proxy, got)
	}
}

func TestProxyFromTheEnvironment(t *testing.T) {
	t.Setenv("ALL_PROXY", "socks5h://me:secret@127.0.0.1:9050")
	r := newRig(t, 760, 700, func(s *state.State, f *fake.Core, scr *Screen) { scr.files.envDone = false })
	connect(r)
	if !r.text("Using socks5h://me:***@127.0.0.1:9050 from the environment.") {
		t.Error("the environment's proxy is not named, or not redacted")
	}
}

// SE-054: the GIPHY key is kept as it is typed.
func TestGiphyKey(t *testing.T) {
	r := newRig(t, 760, 700, nil)
	connect(r)
	r.typeIn("GIPHY API key", "abc123")
	if string(r.s.Settings.GiphyKey) != "abc123" {
		t.Errorf("key %q", r.s.Settings.GiphyKey)
	}
}

// SE-051: Start at login calls the login item; a refusal is shown and undone,
// an approval is asked for.
func TestStartAtLogin(t *testing.T) {
	old := LoginItem
	t.Cleanup(func() { LoginItem = old })
	var calls []bool
	LoginItem = func(on bool) (bool, error) { calls = append(calls, on); return true, nil }
	r := newRig(t, 760, 700, nil)
	connect(r)
	r.click(ui.RoleSwitch, "Start at login")
	r.wait("the approval", func() bool { return r.text("Allow Zap in System Settings, under Login Items.") })
	if !r.s.Settings.StartAtLogin || len(calls) != 1 || !calls[0] {
		t.Errorf("start at login %v, calls %v", r.s.Settings.StartAtLogin, calls)
	}
	if !strings.Contains(r.toast(), "Allow Zap in System Settings, under Login Items.") {
		t.Errorf("toast %q", r.toast())
	}
	LoginItem = func(bool) (bool, error) { return false, errTest }
	r.click(ui.RoleSwitch, "Start at login")
	r.wait("the failure", func() bool { return strings.Contains(r.toast(), "Could not change the login item") })
	if !r.s.Settings.StartAtLogin {
		t.Error("the switch stayed off after the failed change back")
	}
}

type testError string

func (e testError) Error() string { return string(e) }

var errTest = testError("no permission")

// SE-060, SE-061: the profile, edited in place, and the account buttons.
func TestAccount(t *testing.T) {
	r := newRig(t, 760, 900, nil)
	connect(r)
	for _, want := range []string{"Ada Lovelace", "+55 (11) 99999-0000", "Analytical"} {
		if !r.text(want) {
			t.Errorf("%q is not shown", want)
		}
	}
	r.click(ui.RoleButton, "Change profile picture")
	if len(r.sent(abi.CommandTagPickProfilePicture)) != 1 {
		t.Error("no PickProfilePicture")
	}
	r.click(ui.RoleButton, "Edit your name and About")
	r.Play(widgettest.Click(ui.RoleTextField, "Your name"), widgettest.Key('A', ui.PrimaryMod()), widgettest.Type("Ada King"), widgettest.Idle(1))
	r.click(ui.RoleButton, "Save")
	got := r.sent(abi.CommandTagSetProfile)
	if len(got) != 1 || !got[0].SetProfile.HasName || string(got[0].SetProfile.Name) != "Ada King" || got[0].SetProfile.HasAbout {
		t.Fatalf("SetProfile %+v", got)
	}
	if r.has(ui.RoleTextField, "Your name") {
		t.Error("the editor is still open after Save")
	}
	r.click(ui.RoleButton, "Edit your name and About")
	r.click(ui.RoleButton, "Cancel")
	if len(r.sent(abi.CommandTagSetProfile)) != 1 || r.has(ui.RoleTextField, "Your name") {
		t.Error("Cancel saved or stayed")
	}

	r.click(ui.RoleButton, "Unlink this computer")
	if r.s.Dialog.Kind != state.DialogConfirmUnlink {
		t.Errorf("dialog %v", r.s.Dialog.Kind)
	}
	r.click(ui.RoleButton, "Add account")
	if !r.s.AddingAccount {
		t.Error("Add account did not start adding one")
	}

	// with several numbers, it is this one that is removed
	r.s.AddingAccount = false
	r.s.AddAccount("2", nil, fake.New(nil))
	r.Frames(3)
	if r.has(ui.RoleButton, "Unlink this computer") {
		t.Error("Unlink is offered with two accounts")
	}
	r.click(ui.RoleButton, "Remove this account")
	if r.s.Dialog.Kind != state.DialogConfirmRemoveAccount || r.s.Dialog.Account != 0 {
		t.Errorf("dialog %+v", r.s.Dialog)
	}
}

// SE-070: the download folder, picked through the core and applied to every
// account's core; the log opens through the core.
func TestFiles(t *testing.T) {
	r := newRig(t, 760, 1000, nil)
	connect(r)
	f2 := fake.New(nil)
	r.s.AddAccount("2", nil, f2)
	r.Frames(2)
	if r.has(ui.RoleButton, "Use default") {
		t.Error("Use default without a folder")
	}
	r.click(ui.RoleButton, "Change…")
	if len(r.sent(abi.CommandTagPickDownloadFolder)) != 1 {
		t.Error("no PickDownloadFolder")
	}
	r.emit(ev(abi.EventTagDownloadFolderPicked, func(e *abi.Event) { e.DownloadFolderPicked.Path = []byte("/tmp/zf-downloads") }))
	if string(r.s.Settings.DownloadFolder) != "/tmp/zf-downloads" || !r.text("/tmp/zf-downloads") {
		t.Errorf("folder %q", r.s.Settings.DownloadFolder)
	}
	for _, f := range []*fake.Core{r.f, f2} {
		var n int
		for _, c := range f.Sent() {
			if c.Tag == abi.CommandTagSetDownloadFolder && c.SetDownloadFolder.HasPath && string(c.SetDownloadFolder.Path) == "/tmp/zf-downloads" {
				n++
			}
		}
		if n != 1 {
			t.Errorf("a core was told %d times", n)
		}
	}
	r.click(ui.RoleButton, "Use default")
	if len(r.s.Settings.DownloadFolder) != 0 || r.has(ui.RoleButton, "Use default") {
		t.Error("Use default left the folder")
	}
	r.click(ui.RoleButton, "Open")
	if got := r.sent(abi.CommandTagOpenLog); len(got) != 1 || !strings.HasSuffix(string(got[0].OpenLog.Path), "zapfast.log") {
		t.Errorf("OpenLog %v", got)
	}
}

// SE-071: the media cache, split by kind, with Clear… asking first.
func TestStorage(t *testing.T) {
	r := newRig(t, 760, 1100, nil)
	connect(r)
	if len(r.sent(abi.CommandTagLoadStorageStats)) != 1 {
		t.Errorf("sent %v", r.f.SentTags())
	}
	if r.text("Images") {
		t.Error("kinds are shown before they were measured")
	}
	n, _ := r.Find(ui.RoleButton, "Clear…")
	if n.State&ui.SemDisabled == 0 {
		t.Error("Clear… is enabled with nothing measured")
	}
	r.emit(ev(abi.EventTagStorageStats, func(e *abi.Event) {
		e.StorageStats.HasStats = true
		e.StorageStats.Stats = abi.StorageStats{Images: 3, ImageBytes: 3 << 20, Videos: 1, VideoBytes: 2048, Stickers: 0}
	}))
	for _, want := range []string{"Images", "Videos and GIFs", "Stickers", "3.0 MB", "3 · 3.0 MB", "1 · 2.0 KB", "0 · 0 B"} {
		if !r.text(want) {
			t.Errorf("%q is not shown", want)
		}
	}
	if r.text("Other") {
		t.Error("Other shows with nothing in it")
	}
	r.click(ui.RoleButton, "Clear…")
	if r.s.Dialog.Kind != state.DialogConfirmClearMediaCache || r.s.Dialog.Account != 0 {
		t.Errorf("dialog %+v", r.s.Dialog)
	}
}

// SE-073: the version, the shortcuts and About, and the credit.
func TestAbout(t *testing.T) {
	r := newRig(t, 760, 1100, nil)
	sh := r.s.Shell.(*fakeShell)
	connect(r)
	r.Play(widgettest.Wheel(ui.RoleScrollArea, "Settings", -3000, 3))
	r.Frames(2)
	if !r.text("Zap "+state.Version) || !r.text("Built with love by") && !r.text("Built with love by ") {
		t.Error("the version or the credit is not shown")
	}
	r.click(ui.RoleButton, "Keyboard shortcuts")
	if r.s.Dialog.Kind != state.DialogShortcuts {
		t.Errorf("dialog %v", r.s.Dialog.Kind)
	}
	r.click(ui.RoleButton, "About")
	if r.s.Dialog.Kind != state.DialogAbout {
		t.Errorf("dialog %v", r.s.Dialog.Kind)
	}
	r.click(ui.RoleButton, "Source code")
	r.click(ui.RoleLink, "Carmine Paolino")
	r.wait("the links", func() bool { return len(sh.all()) == 2 })
	opened := sh.all()
	if opened[0] != "https://github.com/crmne/zapfast" || opened[1] != "https://paolino.me" {
		t.Errorf("opened %v", opened)
	}
}

// A steady frame allocates nothing, in each of the window's states.
func TestSteadyFramesAllocateNothing(t *testing.T) {
	steady := func(t *testing.T, r *rig) {
		t.Helper()
		r.Frames(8)
		// One run of 30 frames: AllocsPerRun's average would round a stray
		// allocation away, and this one counts them all.
		if n := testing.AllocsPerRun(1, func() {
			for range 30 {
				r.Frame()
			}
		}); n != 0 {
			t.Fatalf("%.0f allocations in 30 frames", n)
		}
	}
	filled := func(r *rig) {
		connect(r)
		pic := writePicture(t)
		r.emit(ev(abi.EventTagAvatar, func(e *abi.Event) {
			e.Avatar.ID, e.Avatar.Full, e.Avatar.Path, e.Avatar.HasPath = []byte("5511999990000:3@s.whatsapp.net"), true, []byte(pic), true
		}), ev(abi.EventTagAccountPrivacy, func(e *abi.Event) {
			e.AccountPrivacy.Values = []abi.PrivacyValue{{Kind: abi.PrivacyKindLastSeen, Choice: abi.PrivacyChoiceExcept}, {Kind: abi.PrivacyKindGroupAdd, Choice: abi.PrivacyChoiceContacts}}
		}), ev(abi.EventTagStorageStats, func(e *abi.Event) {
			e.StorageStats.HasStats = true
			e.StorageStats.Stats = abi.StorageStats{Images: 3, ImageBytes: 3 << 20, Videos: 1, VideoBytes: 2048, Other: 2, OtherBytes: 10}
		}))
	}
	t.Run("settings", func(t *testing.T) {
		r := newRig(t, 760, 900, nil)
		filled(r)
		steady(t, r)
	})
	t.Run("search", func(t *testing.T) {
		r := newRig(t, 760, 900, nil)
		filled(r)
		r.Play(widgettest.Click(ui.RoleSearchField, "Search settings"), widgettest.Type("sound"), widgettest.Idle(2))
		steady(t, r)
	})
	t.Run("app lock form and profile editor", func(t *testing.T) {
		r := newRig(t, 760, 1400, nil)
		filled(r)
		r.click(ui.RoleButton, "Set password…")
		r.typeIn("New password, at least 6 characters", "abc") // a password field with the focus
		r.click(ui.RoleButton, "Edit your name and About")
		steady(t, r)
	})
	t.Run("update", func(t *testing.T) {
		f, pub := makeFeed(t, "0.20.0", []byte("update"))
		r := updRig(t, f, pub, nil)
		filled(r)
		r.click(ui.RoleButton, "Check now")
		r.wait("the release", func() bool { return r.has(ui.RoleButton, "Download update") })
		steady(t, r)
	})
}

// Idle requests no frames: once what the window shows has settled, nothing
// is owed.
func TestIdleRequestsNoFrames(t *testing.T) {
	r := newRig(t, 760, 900, nil)
	connect(r)
	r.Frames(30)
	if r.Context().Animating() {
		t.Error("a settled window asks for another frame")
	}
}

// writePicture writes a small PNG and returns its path.
func writePicture(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "me.png")
	im := image.NewNRGBA(image.Rect(0, 0, 64, 64))
	for i := range im.Pix {
		im.Pix[i] = 0xc0
	}
	var buf bytes.Buffer
	if err := png.Encode(&buf, im); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, buf.Bytes(), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

// SE-060: our profile picture shows from the window's image cache (the full
// one when the core has it), and pressing it changes it.
func TestProfilePicture(t *testing.T) {
	r := newRig(t, 760, 900, nil)
	connect(r)
	jid := "5511999990000:3@s.whatsapp.net"
	asked := r.sent(abi.CommandTagFetchAvatar)
	if len(asked) != 2 || string(asked[0].FetchAvatar.ID) != jid || !asked[0].FetchAvatar.Full || asked[1].FetchAvatar.Full {
		t.Fatalf("FetchAvatar %+v", asked)
	}
	path := writePicture(t)
	r.emit(ev(abi.EventTagAvatar, func(e *abi.Event) {
		e.Avatar.ID, e.Avatar.Full, e.Avatar.Path, e.Avatar.HasPath = []byte(jid), true, []byte(path), true
	}))
	in := r.scr.info(r.acct())
	r.wait("the picture", func() bool {
		r.Frames(2)
		return in.pic.path == path && in.pic.handle != 0 && r.scr.env.K.ImageState(in.pic.handle) == img.Ready
	})
	r.click(ui.RoleButton, "Change profile picture")
	if len(r.sent(abi.CommandTagPickProfilePicture)) != 1 {
		t.Error("pressing the picture asks for a new one")
	}
}

// SE-024: of several codes typed in a row, the verifier stored is the last
// one's, even when an earlier, shorter code's hashing finishes after it and
// before the window takes the results.
func TestChatLockKeepsTheLatestCode(t *testing.T) {
	gates := map[string]chan struct{}{"a": make(chan struct{}), "ab": make(chan struct{})}
	var done sync.WaitGroup
	done.Add(2)
	old := chatVerifier
	chatVerifier = func(code []byte) string {
		c := string(code)
		<-gates[c]
		defer done.Done()
		return "verifier:" + c
	}
	t.Cleanup(func() { chatVerifier = old })
	r := newRig(t, 760, 700, nil)
	connect(r)
	r.Play(widgettest.Click(ui.RoleTextField, "Locked chats code"), widgettest.Type("a"), widgettest.Idle(2))
	r.Play(widgettest.Type("b"), widgettest.Idle(2))
	// the last code finishes first and the earlier one after it, with no frame between
	pending := func() uint64 {
		r.scr.async.mu.Lock()
		defer r.scr.async.mu.Unlock()
		return r.scr.async.chatLock.seq
	}
	close(gates["ab"])
	for i := 0; pending() == 0 && i < 500; i++ {
		time.Sleep(time.Millisecond)
	}
	close(gates["a"])
	done.Wait()
	r.wait("the verifier", func() bool { return len(r.s.Settings.ChatLockHash) > 0 })
	r.Frames(3)
	if got := string(r.s.Settings.ChatLockHash); got != "verifier:ab" {
		t.Errorf("the stored verifier is %q, want the last code's", got)
	}
}

// The Theme pop-up's built-in names follow a language switch made while the
// window is open (they were built once, in the language at that time).
func TestThemeNamesFollowTheLanguage(t *testing.T) {
	i18n.SetLocale(i18n.English)
	t.Cleanup(func() { i18n.SetLocale(i18n.English) })
	r := newRig(t, 760, 700, nil)
	connect(r)
	r.choose("Theme", "Light") // builds the menu in English
	r.choose("Language", "Deutsch")
	r.Frames(2)
	dark := i18n.T("Dark")
	if dark == "Dark" {
		t.Fatal("the German catalog has no Dark")
	}
	r.Play(widgettest.Click(ui.RoleComboBox, i18n.T("Theme")), widgettest.Idle(2))
	if !r.has(ui.RoleListItem, dark) || r.has(ui.RoleListItem, "Dark") {
		t.Errorf("the theme options are not in German\n%s", widgettest.Dump(r.Tree()))
	}
}

// The window's title follows a language switch made while it is open.
func TestWindowTitleFollowsTheLanguage(t *testing.T) {
	i18n.SetLocale(i18n.English)
	t.Cleanup(func() { i18n.SetLocale(i18n.English); setWindowTitle = (*app.Win).SetTitle })
	r := newRig(t, 760, 700, nil)
	connect(r)
	var got []string
	setWindowTitle = func(_ *app.Win, title string) { got = append(got, title) }
	r.scr.win.Store(&app.Win{}) // the rig has no window of its own
	r.choose("Language", "Deutsch")
	r.Frames(2)
	if len(got) != 1 || got[0] != i18n.T("Settings") || got[0] == "Settings" {
		t.Errorf("titles %q, want one, in German", got)
	}
	r.Frames(3)
	if len(got) != 1 {
		t.Errorf("titled again without a change: %q", got)
	}
	r.scr.win.Store(nil)
}

// SE-024: a new locked chats code takes effect even when the Settings window
// is closed before its slow hash finishes: the shell's per-frame and per-drain
// hook stores it, with no Settings frame built.
func TestChatLockIsAppliedWithSettingsClosed(t *testing.T) {
	gate := make(chan struct{})
	var done sync.WaitGroup
	done.Add(1)
	old := chatVerifier
	chatVerifier = func(code []byte) string {
		c := string(code)
		<-gate
		defer done.Done()
		return "verifier:" + c
	}
	t.Cleanup(func() { chatVerifier = old })
	r := newRig(t, 760, 700, nil)
	connect(r)
	r.s.Settings.ChatLockHash = []byte("verifier:old")
	r.typeIn("Locked chats code", "new")
	r.win.Closed() // the window is closed before the hash lands
	close(gate)
	done.Wait()
	for i := 0; !r.scr.chatLockReady.Load() && i < 500; i++ {
		time.Sleep(time.Millisecond)
	}
	// what the shell calls every frame, and every drain with no window; no
	// Settings frame is built
	r.scr.up.CheckIfDue(&r.s.Settings)
	if got := string(r.s.Settings.ChatLockHash); got != "verifier:new" {
		t.Errorf("the code in force is %q, want the new one", got)
	}
}
