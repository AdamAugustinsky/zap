package settings

import (
	"bytes"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"gonativeui/rexui"
	"gonativeui/ui"

	"zapfast/abi"
	"zapfast/i18n"
	"zapfast/lockhash"
	"zapfast/state"
	"zapfast/ui/screen"
)

// playSound plays a sound's preview; tests replace it.
var playSound = previewSound

// chatVerifier works out the locked chats code's verifier; tests replace it.
var chatVerifier = lockhash.ChatVerifier

// async is what the goroutines the window starts hand back: toasts, the
// login item's outcome and a chat lock code's verifier. poll takes them in
// a frame; the state is never touched off the main thread.
type async struct {
	mu     sync.Mutex
	toasts []pendingToast
	login  struct {
		has, on, approval bool
		err               string
	}
	chatLock struct {
		seq  uint64
		hash []byte
	}
}

type pendingToast struct {
	text  string
	isErr bool
}

// post queues a toast for the next frame, from any goroutine.
func (s *Screen) post(text string, isErr bool) {
	s.async.mu.Lock()
	s.async.toasts = append(s.async.toasts, pendingToast{text, isErr})
	s.async.mu.Unlock()
	s.wake()
}

// poll applies what the goroutines finished.
func (s *Screen) poll(e *screen.Env) {
	s.files.sync(e.Acct())
	s.themes.poll(s)
	s.pollLock(e)
	a := &s.async
	a.mu.Lock()
	toasts := a.toasts
	a.toasts = nil
	login := a.login
	a.login.has = false
	a.mu.Unlock()
	for _, t := range toasts {
		e.S.Toast([]byte(t.text), t.isErr)
	}
	if login.has {
		st := &e.S.Settings
		switch {
		case login.err != "":
			st.StartAtLogin = !login.on
			e.S.Toast([]byte(i18n.Format(i18n.T("Could not change the login item: {error}"), "{error}", login.err)), true)
		case login.approval && login.on:
			s.login.approval = true
			e.S.Toast([]byte(s.tx.approval), false)
		default:
			s.login.approval = false
		}
		s.changed(e)
	}
}

// applyChatLock stores a finished locked chats verifier. It runs from the
// updater's per-frame and per-drain hook (Updates.CheckIfDue), not from the
// Settings window's build: the window may be closed before the slow hash
// lands, and the old code must not stay in force (or the new one be lost at
// quit) until the window is opened again. Only the code typed last is
// stored: an earlier, shorter prefix's verifier that finished late is zeroed
// and dropped. Main thread.
func (s *Screen) applyChatLock(settings *state.Settings) {
	if !s.chatLockReady.Load() {
		return
	}
	s.chatLockReady.Store(false)
	a := &s.async
	a.mu.Lock()
	hash, seq := a.chatLock.hash, a.chatLock.seq
	a.chatLock.seq, a.chatLock.hash = 0, nil
	a.mu.Unlock()
	switch {
	case seq == 0:
	case seq == s.chatLockSeq:
		settings.ChatLockHash = hash
		if st := s.up.attached(); st != nil {
			st.SettingsChanged()
		}
	default:
		clear(hash)
	}
}

// chatLockField is the locked chats code: a masked field that sets the code
// as it is typed, and Clear (zf:src/ui/settings.rs, SetChatLockCode).
func (s *Screen) chatLockField(e *screen.Env, title string) {
	k := e.K.Kit
	k.NextWidth(k.Fixed(s.ctrlW(e, 170)))
	if sig := rexui.TextField(k, title, &s.chatLock, i18n.T("Secret code"), rexui.FieldOpts{Secure: true}); sig.Changed {
		s.setChatLock(e, bytes.Clone(s.chatLock.Bytes()))
	}
	if len(e.S.Settings.ChatLockHash) > 0 && button(e, s.tx.clear) {
		s.chatLock.Wipe()
		s.setChatLock(e, nil)
	}
}

// setChatLock sets the locked chats' code (blank: none), and zeroes code.
// The verifier is slow on purpose, so a goroutine works it out.
func (s *Screen) setChatLock(e *screen.Env, code []byte) {
	s.chatLockSeq++
	seq := s.chatLockSeq
	if len(bytes.TrimSpace(code)) == 0 {
		clear(code)
		e.S.Settings.ChatLockHash = nil
		s.changed(e)
		return
	}
	st := e.S
	go func() {
		h := []byte(chatVerifier(code))
		clear(code)
		s.async.mu.Lock()
		if seq > s.async.chatLock.seq { // a late, older result never replaces a newer one
			clear(s.async.chatLock.hash)
			s.async.chatLock.seq, s.async.chatLock.hash = seq, h
		} else {
			clear(h)
		}
		s.async.mu.Unlock()
		s.chatLockReady.Store(true)
		s.wake()
		if st != nil {
			st.Wake() // the main window, or the drain with none
		}
	}()
}

// proxyField is the proxy: edited freely, applied when the field loses the
// focus or Enter is pressed (zf:src/app.rs SetProxy).
func (s *Screen) proxyField(e *screen.Env, title string) {
	k := e.K.Kit
	k.NextWidth(k.Fixed(s.ctrlW(e, 240)))
	sig := rexui.TextField(k, title, &s.proxy, "socks5h://127.0.0.1:9050", rexui.FieldOpts{})
	if sig.Submitted || (s.proxyEditing && !sig.Focused) {
		s.setProxy(e, trim(s.proxy.String()))
	}
	s.proxyEditing = sig.Focused
}

func (s *Screen) setProxy(e *screen.Env, value string) {
	st := &e.S.Settings
	if value == string(st.Proxy) {
		return
	}
	if value != "" {
		if err := checkProxy(value); err != "" {
			e.S.Toast([]byte(err), true)
			s.proxy.SetBytes(st.Proxy)
			return
		}
	}
	st.Proxy = append(st.Proxy[:0], value...)
	s.changed(e)
	s.eachAccount(e, func(a *state.Account) {
		cmd := a.Cmd(abi.CommandTagSetProxy)
		cmd.SetProxy.Proxy = st.Proxy
		a.Send(cmd)
	})
}

// checkProxy is zf:src/proxy.rs Proxy::parse's validation: "" when value is
// a proxy address, else what is wrong with it. A bare host:port is HTTP.
func checkProxy(value string) string {
	if !strings.Contains(value, "://") {
		value = "http://" + value
	}
	u, err := url.Parse(value)
	if err != nil {
		return i18n.T("Enter a proxy address like socks5://127.0.0.1:1080")
	}
	switch u.Scheme {
	case "http", "socks5", "socks", "socks5h":
	default:
		return i18n.T("Use an http://, socks5://, or socks5h:// proxy")
	}
	if u.Hostname() == "" {
		return i18n.T("The proxy address needs a host")
	}
	if (u.Path != "" && u.Path != "/") || u.RawQuery != "" {
		return i18n.T("A proxy address has no path")
	}
	return ""
}

// soundOpts are a sound menu's options: the four sounds, a custom file when
// one is chosen, and Choose a file….
type soundOpts struct {
	loc  i18n.Locale
	path string
	n    int
	opts [6]string
}

func (so *soundOpts) sync(t *textCache, sc *state.SoundChoice) {
	if so.n != 0 && so.loc == t.loc && so.path == string(sc.Path) {
		return
	}
	so.loc, so.path = t.loc, string(sc.Path)
	copy(so.opts[:4], t.sound[:])
	so.n = 4
	if sc.Sound == state.SoundCustom {
		so.opts[so.n] = filepath.Base(so.path)
		so.n++
	}
	so.opts[so.n] = t.choose
	so.n++
}

// soundControl is a notification sound's menu and its Play button.
func (s *Screen) soundControl(e *screen.Env, title string, mention bool) {
	st := &e.S.Settings
	sc, so := &st.MessageSound, &s.sounds[0]
	if mention {
		sc, so = &st.MentionSound, &s.sounds[1]
	}
	so.sync(&s.tx, sc)
	k := e.K.Kit
	if sc.Sound != state.SoundSystem && sc.Sound != state.SoundNone {
		if rexui.IconButton(k, s.icons.play, s.tx.play, rexui.IconButtonOpts{}).Clicked {
			playSound(*sc)
		}
	}
	sel := int(sc.Sound)
	v, ok := s.popup(e, title, sel, so.opts[:so.n], 170)
	if !ok {
		return
	}
	switch {
	case v == so.n-1: // Choose a file…
		if a := e.Acct(); a != nil {
			cmd := a.Cmd(abi.CommandTagPickNotificationSound)
			cmd.PickNotificationSound.Mention = mention
			a.Send(cmd)
		}
	case v < 4:
		*sc = state.SoundChoice{Sound: state.Sound(v)}
		s.changed(e)
	}
}

// downloadsControls are Open folder, Change… and, with a folder chosen, Use
// default.
func (s *Screen) downloadsControls(e *screen.Env) {
	st := &e.S.Settings
	if button(e, s.tx.openFolder) {
		dir := s.files.media
		if len(st.DownloadFolder) > 0 {
			dir = string(st.DownloadFolder)
		}
		s.openFolder(e, dir)
	}
	if button(e, s.tx.change) {
		if a := e.Acct(); a != nil {
			a.Send(a.Cmd(abi.CommandTagPickDownloadFolder))
		}
	}
	if len(st.DownloadFolder) > 0 && button(e, s.tx.useDefault) {
		s.setDownloadFolder(e, nil)
	}
}

// setDownloadFolder sets the folder new downloads go to (nil: the cache) in
// every account's core.
func (s *Screen) setDownloadFolder(e *screen.Env, path []byte) {
	e.S.SetDownloadFolder(path)
	s.changed(e)
}

// openFolder opens dir in Finder, making it first (the downloads folder may
// not exist yet), on a goroutine.
func (s *Screen) openFolder(e *screen.Env, dir string) {
	sh := e.S.Shell
	go func() {
		if st, err := os.Stat(dir); err != nil || !st.IsDir() {
			_ = os.MkdirAll(dir, 0o700)
		}
		if st, err := os.Stat(dir); err != nil || !st.IsDir() {
			s.post(i18n.T("The folder is unavailable"), true)
			return
		}
		if err := sh.Open(dir); err != nil {
			s.post(i18n.Format(i18n.T("Could not open the folder: {error}"), "{error}", err.Error()), true)
		}
	}()
}

// openURL opens a link in the browser, on a goroutine: a web address only
// (the release's comes from the update feed, which is not signed).
func (s *Screen) openURL(link string) {
	if l := strings.ToLower(link); !strings.HasPrefix(l, "https://") && !strings.HasPrefix(l, "http://") {
		s.post(i18n.T("This link type cannot be opened from Zap"), true)
		return
	}
	sh := s.env.S.Shell
	go func() {
		if err := sh.Open(link); err != nil {
			s.post(err.Error(), true)
		}
	}()
}

var _ = ui.AlignLeft
