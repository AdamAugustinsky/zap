package wa

import (
	"context"
	"database/sql"
	"errors"
	"log/slog"
	"net/http"
	"sync"
	"sync/atomic"
	"time"

	"go.mau.fi/whatsmeow/appstate"
	wastore "go.mau.fi/whatsmeow/store"
	"go.mau.fi/whatsmeow/types"
	waLog "go.mau.fi/whatsmeow/util/log"

	"zapfast/abi"
	"zapfast/gocore"
	"zapfast/gocore/calls"
	"zapfast/gocore/out"
	"zapfast/gocore/store"
)

// Options configure an Engine. The zero value is the app's: the Keychain
// under the Conn's keyring service, and whatsmeow.
type Options struct {
	// Keys keeps the account's archive and session keys; nil: the Keychain
	// under core.Config.KeyringService.
	Keys store.Keys
	// NewClient makes the client over the account's device; nil:
	// NewClient(device, DeviceName). Tests put a fake here.
	NewClient func(device *wastore.Device) Client
	// DeviceName is what the phone lists this computer as; empty: "Zap".
	DeviceName string
	// Web reads what is not WhatsApp's: GIPHY's search and clips, Signal's
	// sticker packs. nil: a client with a 30 s timeout over the proxy
	// SetProxy names. Tests put a local server's here.
	Web *http.Client
	// Log is whatsmeow's logger, for its client and its store; nil: none
	// (waLog.Noop). The app gives a redacting one (sys.WhatsmeowLog).
	Log waLog.Logger
	// CallMedia makes the account's call media; nil: the Go engine's. An
	// error turns calls off (calls.NotAvailable).
	CallMedia func() (calls.Media, error)
}

// answers are the commands this engine handles by tag: every command of
// the schema but the calls, which acceptCall decides (calls.go), and the
// demo's. PickAnswer never reaches an engine (the Conn's broker takes it).
var answers = func() gocore.Table {
	var tags []abi.CommandTag
	for i := range 1 << 10 {
		tag := abi.CommandTag(i)
		if tag.String() != "unknown" && !callCommands[tag] && !demoCommands[tag] {
			tags = append(tags, tag)
		}
	}
	return gocore.Answers(tags...)
}()

var demoCommands = map[abi.CommandTag]bool{
	abi.CommandTagDemoIncoming: true, abi.CommandTagDemoTyping: true, abi.CommandTagDemoPanic: true,
}

// Engine is the WhatsApp engine: a gocore.Engine over whatsmeow, the Go
// archive and whatsmeow's store (gocore/store). One goroutine (run) owns
// its state: the Conn's commands, whatsmeow's events and the answers of
// slow work (uploads, downloads, requests) all reach it through the
// inbox, in the order they came. Slow work runs on goroutines of the
// Host's (job) and never touches the state.
type Engine struct {
	opts Options
	h    *gocore.Host
	out  *out.Out

	qmu     sync.Mutex
	inbox   []func()
	running bool // an inbox item runs
	kick    chan struct{}
	busy    atomic.Int64 // jobs that have not posted their answer yet

	// Owned by run.
	ctx      context.Context
	acct     *store.Account
	arch     *store.Archive
	cli      Client
	gen      uint64 // the client's generation: a new one after a logout
	cacheDir string
	stateDir string
	dead     bool // the account could not open, or was removed
	started  bool

	status       abi.LinkStatus
	statusKey    string
	qr           string
	qrCancel     context.CancelFunc
	pairCode     string
	pairingPhone string

	syncing   bool
	syncTimer *time.Timer
	online    bool
	presence  map[string]bool // direct chats whose presence is followed
	watch     *abi.ReceiptTarget
	older     map[string]*olderRequest
	olderNext uint64
	downloads map[downloadKey]bool
	failed    map[string]error // the last failed download of a message
	avatars   map[avatarKey]int

	// WP4b's state.
	openFailed   bool                 // the account's storage did not open: Reconnect and StartOverArchive try again
	keepArchived bool                 // SetKeepChatsArchived: a new message leaves an archived chat archived
	prefetch     bool                 // SetHistoryPrefetch
	focused      string               // the chat the reader looks at, for the prefetch
	downloadDir  string               // SetDownloadFolder; "": the media cache
	proxy        string               // SetProxy
	stars        map[[2]string]uint64 // a star request in flight, by chat and message: its serial
	pins         map[[2]string]uint64 // likewise for message pins
	serial       uint64
	groupsAsked  map[string]bool    // groups whose info was asked for this session
	polls        map[[2]string]bool // votes in flight
	replies      map[[2]string]bool // interactive replies in flight
	recentHashes map[string]string  // a Recent sticker's file: its hash
	privacy      map[abi.PrivacyKind]abi.PrivacyChoice
	giphy        string                 // GIPHY's API, a test server's in the tests
	signal       string                 // Signal's sticker CDN, likewise
	stickerTags  map[string]stickerTags // the emojis of sticker files, by path
	pinLimit     bool                   // PinLimit was announced this session
	permitted    bool                   // Start came
	prefetched   map[string]bool        // chats the prefetch filled this session
	typing       []chatState            // chat states to send, in order
	settings     []appstate.PatchInfo   // chat settings to send to the phone, in order

	// calls is WP7's call bridge (calls.go): set by Open, before any
	// command, and never replaced.
	calls *calls.Calls

	// Timings, shortened by the tests.
	syncQuiet     time.Duration
	olderPatience time.Duration
}

var _ gocore.Engine = (*Engine)(nil)

// New returns an engine for one account; gocore.Open opens it.
func New(opts Options) *Engine {
	return &Engine{
		opts:          opts,
		kick:          make(chan struct{}, 1),
		presence:      map[string]bool{},
		older:         map[string]*olderRequest{},
		downloads:     map[downloadKey]bool{},
		failed:        map[string]error{},
		avatars:       map[avatarKey]int{},
		stars:         map[[2]string]uint64{},
		pins:          map[[2]string]uint64{},
		groupsAsked:   map[string]bool{},
		polls:         map[[2]string]bool{},
		replies:       map[[2]string]bool{},
		recentHashes:  map[string]string{},
		privacy:       map[abi.PrivacyKind]abi.PrivacyChoice{},
		giphy:         giphyAPI,
		signal:        signalCDN,
		stickerTags:   map[string]stickerTags{},
		prefetched:    map[string]bool{},
		syncQuiet:     syncQuiet,
		olderPatience: olderPatience,
	}
}

const (
	// syncQuiet is how long after the last history chunk the initial sync
	// counts as over (upstream's SYNC_QUIET).
	syncQuiet = 20 * time.Second
	// olderPatience is how long the phone has to answer a history request
	// (upstream's PHONE_PATIENCE).
	olderPatience = 30 * time.Second
)

// Name names the engine in its refusals.
func (e *Engine) Name() string { return "WhatsApp" }

// Accept answers every command but the demo's; a call command as the call
// bridge can take it (acceptCall).
func (e *Engine) Accept(cmd *abi.Command) error {
	if callCommands[cmd.Tag] {
		return e.acceptCall(cmd)
	}
	return answers.Accept(cmd)
}

// Open keeps h and starts the engine's goroutine, which opens the account
// (the Keychain may ask the user, so never on the UI thread) and lists the
// cached chats before the startup permit.
func (e *Engine) Open(h *gocore.Host) error {
	e.h, e.out = h, h.Out()
	e.openCalls()
	// First in the inbox: before Start and every command.
	e.post(e.open)
	if !h.Go("whatsapp", e.run) {
		return errors.New("wa: the core is closing")
	}
	return nil
}

// Start connects, once the account is open.
func (e *Engine) Start() { e.post(e.start) }

// Command queues cmd for the engine's goroutine.
func (e *Engine) Command(cmd *gocore.Command) { e.post(func() { e.command(cmd) }) }

// Stop has nothing of its own to do: the Host's context is cancelled, so
// run disconnects and closes the account, and Close waits for it.
func (e *Engine) Stop() {}

// post queues f for run. Nothing runs once the Host's context ends.
func (e *Engine) post(f func()) {
	e.qmu.Lock()
	e.inbox = append(e.inbox, f)
	e.qmu.Unlock()
	select {
	case e.kick <- struct{}{}:
	default:
	}
}

// next takes the first item of the inbox, nil when it is empty.
func (e *Engine) next() func() {
	e.qmu.Lock()
	defer e.qmu.Unlock()
	if len(e.inbox) == 0 {
		e.running = false
		return nil
	}
	f := e.inbox[0]
	e.inbox[0] = nil
	e.inbox = e.inbox[1:]
	e.running = true
	return f
}

// idle reports whether nothing is queued, running or still working: the
// tests' barrier.
func (e *Engine) idle() bool {
	e.qmu.Lock()
	defer e.qmu.Unlock()
	return len(e.inbox) == 0 && !e.running && e.busy.Load() == 0
}

func (e *Engine) run(ctx context.Context) {
	e.ctx = ctx
	defer e.shutdown()
	for {
		select {
		case <-ctx.Done():
			return
		case <-e.kick:
		}
		for f := e.next(); f != nil; f = e.next() {
			if ctx.Err() != nil {
				return
			}
			f()
		}
	}
}

// job runs slow work on a goroutine of the Host's; what it returns (if
// anything) runs on the engine's goroutine afterwards. It must not touch
// the engine's state itself.
func (e *Engine) job(f func(ctx context.Context) func()) {
	e.busy.Add(1)
	ok := e.h.Go("whatsapp-job", func(ctx context.Context) {
		defer e.busy.Add(-1)
		if done := f(ctx); done != nil {
			e.post(done)
		}
	})
	if !ok {
		e.busy.Add(-1)
	}
}

func (e *Engine) shutdown() {
	if e.syncTimer != nil {
		e.syncTimer.Stop()
	}
	for _, r := range e.older {
		r.timer.Stop()
	}
	if e.qrCancel != nil {
		e.qrCancel()
	}
	e.hangUpCalls()
	if e.cli != nil {
		e.cli.Disconnect()
	}
	if e.acct != nil {
		if err := e.acct.Close(); err != nil {
			slog.Warn("zapfast wa: closing the account", "err", err)
		}
	}
}

// open opens the account's storage and client, then lists what the
// archive holds: Me when linked, the chats and the drafts.
func (e *Engine) open() {
	state, cache := e.h.AccountDirs()
	e.cacheDir, e.stateDir = cache, state
	keys := e.opts.Keys
	if keys == nil {
		keys = store.Keychain(e.h.Config().KeyringService)
	}
	acct, err := store.OpenAccount(e.ctx, state, keys, e.opts.Log)
	if err != nil {
		e.openFailed = true
		e.fail("Could not open this account's archive: " + err.Error())
		return
	}
	e.openFailed = false
	e.acct, e.arch = acct, acct.Archive
	device, err := acct.Devices.GetFirstDevice(e.ctx)
	if err != nil {
		e.fail("Could not open this account's WhatsApp session: " + err.Error())
		return
	}
	if err := e.arch.RecoverOutbox(); err != nil {
		slog.Warn("zapfast wa: recovering the outbox", "err", err)
	}
	e.newClient(device)
	e.loadSettings()
	e.emitMe()
	e.emitChats()
	e.emitContacts()
}

// fail stops the engine for good: the account cannot be used.
func (e *Engine) fail(msg string) {
	e.dead = true
	e.setStatus(linkFailed(msg))
}

// newClient makes the client over device and takes its events, tagged with
// a new generation so a replaced client's late events are ignored.
func (e *Engine) newClient(device *wastore.Device) {
	e.gen++
	gen := e.gen
	if e.opts.NewClient != nil {
		e.cli = e.opts.NewClient(device)
	} else {
		e.cli = newClient(device, e.opts.DeviceName, e.opts.Log)
	}
	e.calls.SetClient(e.cli.Calls())
	if e.proxy != "" {
		if err := e.cli.SetProxy(e.proxy); err != nil {
			slog.Warn("zapfast wa: the proxy setting was not applied", "err", err)
		}
	}
	e.cli.AddEventHandler(func(evt any) {
		e.post(func() {
			if gen == e.gen && !e.dead {
				e.handle(evt)
			}
		})
	})
}

func (e *Engine) start() {
	e.permitted = true
	if e.dead || e.started {
		return
	}
	e.started = true
	e.connect()
}

// command handles one command from the UI.
func (e *Engine) command(cmd *gocore.Command) {
	if e.dead {
		// The storage did not open: the reader may try again, or set the
		// unreadable archive aside.
		if e.openFailed && (cmd.Tag == abi.CommandTagReconnect || cmd.Tag == abi.CommandTagStartOverArchive) {
			e.reopen(cmd.Tag == abi.CommandTagStartOverArchive)
		}
		return
	}
	switch cmd.Tag {
	case abi.CommandTagPairWithPhone:
		e.pairWithPhone(string(cmd.PairWithPhone.Phone))
	case abi.CommandTagUnlink:
		e.unlink()
	case abi.CommandTagRemoveAccount:
		e.removeAccount()
	case abi.CommandTagReconnect:
		e.reconnect()
	case abi.CommandTagSetOnline:
		e.setOnline(cmd.SetOnline.Online)
	case abi.CommandTagResumed:
		e.resumed()

	case abi.CommandTagLoadChat:
		c := &cmd.LoadChat
		var before *abi.PageKey
		if c.HasBefore {
			before = &c.Before
		}
		e.loadChat(string(c.Chat), before)
	case abi.CommandTagLoadUntil:
		c := &cmd.LoadUntil
		e.loadUntil(string(c.Chat), string(c.ID), c.Before)
	case abi.CommandTagFetchOlder:
		e.fetchOlder(string(cmd.FetchOlder.Chat), cmd.FetchOlder.Explicit)
	case abi.CommandTagReloadHistory:
		e.reloadHistory(string(cmd.ReloadHistory.Chat), string(cmd.ReloadHistory.Message))
	case abi.CommandTagEnsureChat:
		e.ensureChat(string(cmd.EnsureChat.Chat), string(cmd.EnsureChat.Name))
	case abi.CommandTagSaveDraft:
		if err := e.arch.SaveDraft(string(cmd.SaveDraft.Chat), string(cmd.SaveDraft.Text)); err != nil {
			slog.Warn("zapfast wa: draft not stored", "err", err)
		}
	case abi.CommandTagComposing:
		e.composing(string(cmd.Composing.Chat), cmd.Composing.Composing)
	case abi.CommandTagMarkRead:
		e.markRead(string(cmd.MarkRead.Chat), cmd.MarkRead.Receipts)
	case abi.CommandTagMarkUnread:
		e.markUnread(string(cmd.MarkUnread.Chat))
	case abi.CommandTagMarkPlayed:
		c := &cmd.MarkPlayed
		e.markPlayed(string(c.Chat), string(c.Message), string(c.Sender), c.Receipts)
	case abi.CommandTagWatchReceipts:
		e.watch = nil
		if cmd.WatchReceipts.HasTarget {
			t := cmd.WatchReceipts.Target
			e.watch = &abi.ReceiptTarget{Chat: t.Chat, Message: t.Message}
		}
		e.emitReceipts()
	case abi.CommandTagSetArchived:
		e.setArchived(string(cmd.SetArchived.Chat), cmd.SetArchived.Archived)
	case abi.CommandTagSetPinned:
		e.setPinned(string(cmd.SetPinned.Chat), cmd.SetPinned.Pinned)
	case abi.CommandTagSetMuted:
		c := &cmd.SetMuted
		e.setMuted(string(c.Chat), mutedToArchive(c.Until, c.HasUntil))
	case abi.CommandTagSetFavorite:
		e.setFavorite(string(cmd.SetFavorite.Chat), cmd.SetFavorite.Favorite)
	case abi.CommandTagDeleteChat:
		e.removeChat(string(cmd.DeleteChat.Chat), true)
	case abi.CommandTagClearChat:
		e.removeChat(string(cmd.ClearChat.Chat), false)

	case abi.CommandTagSendText:
		e.sendText(&cmd.SendText)
	case abi.CommandTagSendLocation:
		e.sendLocation(&cmd.SendLocation)
	case abi.CommandTagSendImage:
		e.sendImage(&cmd.SendImage, cmd.Blob)
	case abi.CommandTagSendFiles:
		e.sendFiles(&cmd.SendFiles)
	case abi.CommandTagSendVoice:
		e.sendVoice(&cmd.SendVoice, cmd.Blob)
	case abi.CommandTagForward:
		e.forward(&cmd.Forward)
	case abi.CommandTagForwardLimit:
		c := &cmd.ForwardLimit
		ev := abi.Event{Tag: abi.EventTagForwardLimit}
		ev.ForwardLimit = abi.EventForwardLimit{FromChat: c.FromChat, Messages: c.Messages, Limit: uint64(e.forwardLimit(string(c.FromChat), strs(c.Messages)))}
		e.out.Emit(&ev)
	case abi.CommandTagEditText:
		c := &cmd.EditText
		e.editText(string(c.Chat), string(c.ID), string(c.Text), strs(c.Mentions))
	case abi.CommandTagRevoke:
		e.revoke(string(cmd.Revoke.Chat), string(cmd.Revoke.ID))
	case abi.CommandTagDeleteLocal:
		e.deleteLocal(string(cmd.DeleteLocal.Chat), string(cmd.DeleteLocal.ID))
	case abi.CommandTagReact:
		c := &cmd.React
		e.react(string(c.Chat), string(c.Message), string(c.Emoji))

	case abi.CommandTagDownload:
		c := &cmd.Download
		card := -1
		if c.HasCard {
			card = int(min(c.Card, 1<<20))
		}
		e.download(string(c.Chat), string(c.Message), card, c.Manual)
	case abi.CommandTagFetchAvatar:
		e.fetchAvatar(string(cmd.FetchAvatar.ID), cmd.FetchAvatar.Full)
	case abi.CommandTagSearchMessages:
		e.searchMessages(string(cmd.SearchMessages.Query))
	case abi.CommandTagSearchChatMessages:
		e.searchChatMessages(&cmd.SearchChatMessages)
	default:
		if callCommands[cmd.Tag] {
			e.callCommand(cmd)
			return
		}
		e.more(cmd)
	}
}

func strs(list [][]byte) []string {
	out := make([]string, len(list))
	for i, s := range list {
		out[i] = string(s)
	}
	return out
}

// --- identities --------------------------------------------------------------

// jidOf parses a chat or user id, without its device.
func jidOf(id string) (types.JID, bool) {
	j, err := types.ParseJID(id)
	if err != nil || j.IsEmpty() || j.User == "" && j.Server != types.GroupServer {
		return types.EmptyJID, false
	}
	return j.ToNonAD(), true
}

// canonical is the id the archive files j under: its phone number when j
// is a privacy id WhatsApp has mapped, whose rows then move over.
func (e *Engine) canonical(j types.JID) string {
	j = j.ToNonAD()
	if j.Server == types.HiddenUserServer {
		if pn, err := e.cli.PNForLID(e.ctx, j); err == nil && !pn.IsEmpty() {
			to := pn.ToNonAD().String()
			if err := e.arch.Canonicalize(j.String(), to); err != nil {
				slog.Warn("zapfast wa: canonicalizing a chat", "err", err)
			}
			return to
		}
	}
	return j.String()
}

// me is the account's canonical id, empty while unlinked.
func (e *Engine) me() string {
	pn, lid := e.cli.OwnID()
	switch {
	case !pn.IsEmpty():
		return pn.String()
	case !lid.IsEmpty():
		return lid.String()
	}
	return ""
}

func (e *Engine) isMe(id string) bool {
	if id == "" {
		return false
	}
	pn, lid := e.cli.OwnID()
	return id == pn.String() || id == lid.String()
}

func (e *Engine) conv() conv {
	return conv{isMe: e.isMe, state: e.mediaState, motion: e.motionState, cardState: e.cardState,
		refreshing: func(chat string) bool { return e.older[chat] != nil }}
}

// --- emitting ------------------------------------------------------------------

func (e *Engine) emitError(text string) {
	ev := abi.Event{Tag: abi.EventTagError}
	ev.Error.Text = b(out.Toast(text))
	e.out.Emit(&ev)
}

// chatRow is a chat as a record, with its newest message and its oldest
// unread mention of the account.
func (e *Engine) chatRow(c store.Chat) abi.Chat {
	var last *store.Message
	if page, _, err := e.arch.Messages(c.ID, "", 1); err == nil && len(page) == 1 {
		last = &page[0]
	}
	mention := ""
	if c.Unread > 0 && chatKind(c.Kind, c.ID) == abi.ChatKindGroup {
		pn, lid := e.cli.OwnID()
		var ours []string
		for _, j := range []types.JID{pn, lid} {
			if !j.IsEmpty() {
				ours = append(ours, j.String())
			}
		}
		mention, _ = e.arch.UnreadMention(c.ID, c.Unread, ours)
	}
	return e.conv().chat(c, last, mention)
}

// emitChats sends the whole chat list, newest first, then the labels and
// every draft, as upstream's emit_chats does.
func (e *Engine) emitChats() {
	chats, err := e.arch.Chats()
	if err != nil {
		slog.Warn("zapfast wa: listing chats", "err", err)
		return
	}
	out.Chunks(e.out, chats, func(_ *out.Record, c store.Chat) abi.Chat { return e.chatRow(c) },
		func(batch uint32, last bool, chunk []abi.Chat) *abi.Event {
			return &abi.Event{Tag: abi.EventTagChats, Chats: abi.EventChats{Batch: batch, Last: last, Chats: chunk}}
		})
	e.emitLabels()
	var drafts []abi.Draft
	for _, c := range chats {
		if text, err := e.arch.Draft(c.ID); err == nil && text != "" {
			drafts = append(drafts, abi.Draft{Chat: b(c.ID), Text: b(text)})
		}
	}
	out.List(e.out, drafts, func(batch uint32, last bool, chunk []abi.Draft) *abi.Event {
		return &abi.Event{Tag: abi.EventTagDrafts, Drafts: abi.EventDrafts{Batch: batch, Last: last, Drafts: chunk}}
	})
}

func (e *Engine) emitChat(id string) {
	c, err := e.arch.Chat(id)
	if err != nil {
		return
	}
	ev := abi.Event{Tag: abi.EventTagChatUpdated}
	ev.ChatUpdated.Chat = e.chatRow(c)
	e.out.Emit(&ev)
}

// emitMessage sends a stored message that changed.
func (e *Engine) emitMessage(chat, id string) {
	m, err := e.arch.Message(chat, id)
	if err != nil {
		return
	}
	e.receiptTimes(&m)
	r := e.out.Record()
	ev := abi.Event{Tag: abi.EventTagMessageUpdated}
	ev.MessageUpdated.Message = e.conv().message(r, m)
	r.Emit(&ev)
}

// emitMessages sends a batch of one chat's messages, oldest first.
func (e *Engine) emitMessages(chat string, messages []store.Message, older, complete bool) {
	for i := range messages {
		e.receiptTimes(&messages[i])
	}
	cv := e.conv()
	out.Chunks(e.out, messages, cv.message, func(batch uint32, last bool, chunk []abi.Message) *abi.Event {
		return &abi.Event{Tag: abi.EventTagMessages, Messages: abi.EventMessages{Chat: b(chat), Batch: batch, Last: last, Older: older, Complete: complete && last, Messages: chunk}}
	})
}

// live announces a message stored live, ours or theirs, as upstream's
// archive_message does: the row as a batch of one, the chat, and Incoming
// when it is new, unread and not part of a sync.
func (e *Engine) live(m store.Message, unread bool) {
	e.emitMessages(m.ChatID, []store.Message{m}, false, false)
	e.emitChat(m.ChatID)
	if unread && !e.syncing {
		r := e.out.Record()
		ev := abi.Event{Tag: abi.EventTagIncoming}
		ev.Incoming = abi.EventIncoming{Chat: b(m.ChatID), Message: e.conv().message(r, m)}
		r.Emit(&ev)
	}
}

// receiptTimes gives one of our messages in a direct chat the times its
// receipts came (delivered_at, read_at).
func (e *Engine) receiptTimes(m *store.Message) {
	if !m.FromMe || len(m.Receipts) > 0 || chatKind("", m.ChatID) != abi.ChatKindDirect || m.Status == "pending" {
		return
	}
	if details, err := e.arch.ReceiptDetails(m.ChatID, m.ID); err == nil {
		m.Receipts = details
	}
}

// emitReceipts sends the followed message's receipts.
func (e *Engine) emitReceipts() {
	if e.watch == nil {
		return
	}
	chat, id := string(e.watch.Chat), string(e.watch.Message)
	details, err := e.arch.ReceiptDetails(chat, id)
	if err != nil {
		return
	}
	ev := abi.Event{Tag: abi.EventTagReceipts}
	ev.Receipts = abi.EventReceipts{Chat: e.watch.Chat, Message: e.watch.Message}
	for _, r := range details[:min(len(details), 2048)] {
		ev.Receipts.Recipients = append(ev.Receipts.Recipients, recipient(r))
	}
	e.out.Emit(&ev)
}

func noRows(err error) bool { return errors.Is(err, sql.ErrNoRows) }
