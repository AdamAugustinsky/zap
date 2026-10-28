package wa

import (
	"errors"
	"fmt"
	"log/slog"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"go.mau.fi/whatsmeow/appstate"
	"go.mau.fi/whatsmeow/proto/waSyncAction"
	"go.mau.fi/whatsmeow/types"
	"go.mau.fi/whatsmeow/types/events"
	"google.golang.org/protobuf/proto"

	"zapfast/abi"
	"zapfast/gocore/store"
)

// The settings the UI hands the engine (upstream keeps them in memory, from
// the app's settings file), the chat settings that are not WhatsApp's chat
// actions, and starting over when the archive cannot be read.

// keepArchivedKey is where the phone's "Keep chats archived" is kept.
const keepArchivedKey = "keep-chats-archived"

// prefetchChats is how many recent chats the history prefetch fills
// (upstream's prefetch.rs: the ten most recent), and prefetchFill how many
// archived messages count as enough.
const (
	prefetchChats = 10
	prefetchFill  = 5 * page
)

// loadSettings reads what the archive keeps of the settings.
func (e *Engine) loadSettings() {
	if v, err := e.arch.Metadata(keepArchivedKey); err == nil {
		e.keepArchived = string(v) == "1"
	}
}

// setKeepChatsArchived is the app's setting: a new message leaves an
// archived chat archived.
func (e *Engine) setKeepChatsArchived(keep bool) {
	e.keepArchived = keep
	v := "0"
	if keep {
		v = "1"
	}
	if err := e.arch.SetMetadata(keepArchivedKey, []byte(v)); err != nil {
		slog.Warn("zapfast wa: keeping a setting", "err", err)
	}
}

// unarchiveSetting is the phone's "Keep chats archived".
func (e *Engine) unarchiveSetting(v *events.UnarchiveChatsSetting) {
	e.setKeepChatsArchived(!v.Action.GetUnarchiveChats())
}

// setDownloadFolder names the folder downloads go to (none: the cache).
// Interrupted downloads left hidden files behind in it.
func (e *Engine) setDownloadFolder(folder string, has bool) {
	if !has {
		e.downloadDir = ""
		return
	}
	e.downloadDir = folder
	if entries, err := os.ReadDir(folder); err == nil {
		for _, en := range entries {
			if !en.IsDir() && len(en.Name()) > 10 && en.Name()[:10] == ".download-" {
				os.Remove(filepath.Join(folder, en.Name()))
			}
		}
	}
}

// setProxy routes the connection through a proxy (empty: none), and
// reconnects so the new route is used.
func (e *Engine) setProxy(proxy string) {
	addr, err := proxyURL(proxy)
	if err != nil {
		e.emitError(err.Error())
		return
	}
	e.proxy = addr
	if err := e.cli.SetProxy(addr); err != nil {
		e.emitError("Could not use the proxy: " + err.Error())
		return
	}
	if e.started && e.cli.IsConnected() {
		e.cli.Disconnect()
		e.connect()
	}
}

// proxyURL is a proxy setting as a URL, as upstream's Proxy::parse reads
// it: a bare host:port is an HTTP proxy.
func proxyURL(setting string) (string, error) {
	setting = strings.TrimSpace(setting)
	if setting == "" {
		return "", nil
	}
	scheme, rest, ok := strings.Cut(setting, "://")
	if !ok {
		scheme, rest = "http", setting
	}
	switch scheme {
	case "http", "socks5", "socks5h":
	case "socks":
		scheme = "socks5"
	default:
		return "", errors.New("Use an http://, socks5://, or socks5h:// proxy")
	}
	u, err := url.Parse(scheme + "://" + rest)
	if err != nil || u.Hostname() == "" {
		return "", errors.New("Enter a proxy address like socks5://127.0.0.1:1080")
	}
	if u.Path != "" && u.Path != "/" || u.RawQuery != "" {
		return "", errors.New("A proxy address has no path")
	}
	return scheme + "://" + rest, nil
}

// setHistoryPrefetch turns the background history fill on or off; focused
// is the chat the reader looks at, filled first.
func (e *Engine) setHistoryPrefetch(on bool, focused string) {
	e.prefetch, e.focused = on, focused
	e.pumpPrefetch()
}

// pumpPrefetch asks the phone for one recent chat's older messages at a
// time while the prefetch is on, until each holds prefetchFill messages or
// its history is complete; each chat once a session.
func (e *Engine) pumpPrefetch() {
	if !e.prefetch || !e.cli.IsConnected() || len(e.older) > 0 {
		return
	}
	chats, err := e.arch.Chats()
	if err != nil {
		return
	}
	var order []string
	if e.focused != "" {
		order = append(order, e.focused)
	}
	for _, c := range chats[:min(len(chats), prefetchChats)] {
		order = append(order, c.ID)
	}
	for _, id := range order {
		if e.prefetched[id] {
			continue
		}
		e.prefetched[id] = true
		c, err := e.arch.Chat(id)
		if err != nil || c.HistoryStart || c.Locked {
			continue
		}
		if n, err := e.arch.MessageCount(id); err != nil || n >= prefetchFill {
			continue
		}
		e.fetchOlder(id, false)
		return
	}
}

// setLocked locks a chat here and on the phone.
func (e *Engine) setLocked(chat string, locked bool) {
	j, ok := jidOf(chat)
	if !ok {
		return
	}
	if err := e.updateChat(chat, func(c *store.Chat) {
		c.Locked, c.LockKnown = locked, true
		c.LockUpdatedAt = max(c.LockUpdatedAt, time.Now().Unix())
	}); err != nil {
		return
	}
	e.tellPhone(lockPatch(j, locked))
}

// lockPatch is the phone's chat lock (whatsapp-rust's LOCK_CHAT schema:
// regular_low, index ["lock", jid], version 7), from zapfast-go 3104c46
// internal/backend/chat_lock.go, MIT.
func lockPatch(j types.JID, locked bool) appstate.PatchInfo {
	return appstate.PatchInfo{Type: appstate.WAPatchRegularLow, Mutations: []appstate.MutationInfo{{
		Index:   []string{appstate.IndexLock, j.String()},
		Version: 7,
		Value:   &waSyncAction.SyncActionValue{LockChatAction: &waSyncAction.LockChatAction{Locked: proto.Bool(locked)}},
	}}}
}

// setChatSound gives a chat its own notification sound; none goes back to
// the app's.
func (e *Engine) setChatSound(chat string, sound abi.NotificationSound, has bool) {
	if err := e.updateChat(chat, func(c *store.Chat) {
		c.Sound = nil
		if has {
			s := soundFromABI(sound)
			c.Sound = &s
		}
	}); err != nil {
		slog.Warn("zapfast wa: setting a chat's sound", "err", err)
	}
}

// reopen tries the account's storage again after it failed to open; with
// startOver, the unreadable archive is set aside first (kept, renamed) and
// the WhatsApp session forgotten, as upstream's
// set_aside_unreadable_archive does.
func (e *Engine) reopen(startOver bool) {
	if startOver {
		kept, err := e.setAsideArchive()
		if err != nil {
			e.setStatus(linkFailed("Could not set the old archive aside: " + err.Error()))
			return
		}
		slog.Warn("zapfast wa: set an unreadable archive aside", "kept", filepath.Base(kept))
	}
	e.dead = false
	e.open()
	if !e.dead && e.permitted {
		e.start()
	}
}

func (e *Engine) setAsideArchive() (string, error) {
	state, _ := e.h.AccountDirs()
	stamp := time.Now().Format("20060102-150405")
	var kept string
	for _, name := range []string{store.ArchiveFile, "archive.db"} {
		path := filepath.Join(state, name)
		if _, err := os.Stat(path); err != nil {
			continue
		}
		to := filepath.Join(state, fmt.Sprintf("%s-unreadable-%s.db", name[:len(name)-len(filepath.Ext(name))], stamp))
		if err := os.Rename(path, to); err != nil {
			return "", err
		}
		for _, suffix := range []string{"-wal", "-shm", "-journal"} {
			if err := os.Rename(path+suffix, to+suffix); err != nil && !errors.Is(err, os.ErrNotExist) {
				return "", err
			}
		}
		kept = to
	}
	for _, suffix := range []string{"", "-wal", "-shm", "-journal"} {
		if err := os.Remove(filepath.Join(state, store.DevicesFile) + suffix); err != nil && !errors.Is(err, os.ErrNotExist) {
			return "", err
		}
	}
	return kept, nil
}

// forgetAccount clears what belonged to the account that logged out: the
// archive, the media and avatar caches, and the session's state, and
// sends the emptied lists.
func (e *Engine) forgetAccount() {
	if err := e.arch.Clear(); err != nil {
		slog.Warn("zapfast wa: clearing the archive", "err", err)
	}
	for _, dir := range []string{e.mediaCache(), filepath.Join(e.cacheDir, "avatars")} {
		if err := os.RemoveAll(dir); err != nil {
			slog.Warn("zapfast wa: clearing a cache", "err", err)
		}
	}
	clear(e.downloads)
	clear(e.failed)
	clear(e.stars)
	clear(e.pins)
	clear(e.groupsAsked)
	clear(e.polls)
	clear(e.replies)
	clear(e.recentHashes)
	clear(e.privacy)
	clear(e.prefetched)
	e.pinLimit = false
	e.emitChats()
}
