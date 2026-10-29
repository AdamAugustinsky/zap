// Package state is the app's UI state, ported from upstream's App
// (zf:src/app.rs:376-823), Account (zf:src/account.rs) and the navigation
// history (zf:src/nav.rs): the accounts, each with its core.Conn and its
// model; the open chat; the page and list mode; the panes beside the
// conversation; selection mode; the one dialog; which windows are open; the
// app lock; the settings. Every screen imports it.
//
// It is pointer-light: ids are kept as model.ChatRef or in model.IDList
// arenas, never as views. Everything runs on the main thread, except Wake
// and Toast.
package state

import (
	"bytes"
	"log/slog"
	"reflect"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"gonativeui/app/prefs"

	"zapfast/abi"
	"zapfast/core"
	"zapfast/i18n"
	"zapfast/model"
)

// Page is what the main window's list column shows (zf:src/model.rs:1594).
// Settings is the Settings window here.
type Page uint8

const (
	PageChats Page = iota
	PageCalls      // the call history, the open chat beside it
)

// ListMode is which chats the chat list shows: the rail's destinations
// under Chats.
type ListMode uint8

const (
	ListChats    ListMode = iota
	ListArchived          // the Archived folder
	ListStarred           // starred messages instead of chats
	ListLocked            // the Locked chats folder (after unlocking it)
)

// Filter is the chat list's chip (zf:src/model.rs:124).
type Filter uint8

const (
	FilterAll Filter = iota
	FilterUnread
	FilterPrivate
	FilterFavorites
	FilterGroups
	FilterChannels
	FilterLabel // the label in Account.Label
)

// Pane is the column beside the conversation (at most one shows).
type Pane uint8

const (
	PaneNone        Pane = iota
	PaneInfo             // the info panel (zf:src/ui/info.rs)
	PaneSearch           // the open chat's search (zf:src/ui/pane.rs)
	PaneMessageInfo      // a message's receipts (zf:src/ui/message_info.rs)
)

// InfoView is what the info panel shows (zf:src/model.rs:1998).
type InfoView uint8

const (
	InfoOverview InfoView = iota
	InfoMedia
	InfoDocs
	InfoLinks
)

// Window names the app's windows besides the main one.
type Window uint8

const (
	WinSettings   Window = iota // ⌘, (zf:src/ui/settings.rs)
	WinViewer                   // the media viewer (zf:src/ui/image_preview.rs)
	WinCall                     // the call (zf:src/ui/call.rs)
	WinPlayground               // the design playground (dev builds: cmd/zapfast -playground)
	NumWindows
)

// Picker is the picker above the composer (zf:src/model.rs:1611).
type Picker uint8

const (
	PickerNone Picker = iota
	PickerEmoji
	PickerGifs
	PickerStickers
)

// Account is one linked WhatsApp number: its core and its model. Views of
// its model are valid until the next Pump.
type Account struct {
	// Index is its place in State.Accounts.
	Index int
	// Serial is a process-unique cache identity, unchanged by reindexing.
	Serial uint64
	// ID is the account's folder name (digits, "1" for the first).
	ID string
	// Conn is the core, nil until open (Pending) or if it failed (Err).
	Conn    core.Conn
	Pending *core.Pending
	Err     string
	Started bool // zf_core_start sent, after the first frame
	// startOnOpen: the core was opened again (Reopen) after the cores
	// started, so it gets its permit as soon as it is open.
	startOnOpen bool
	// Removed: the core said the account is gone (AccountRemoved); the
	// state drops it after the drain (State.Pump).
	Removed bool
	// restored: the chat open at quit was reopened (or there was none).
	restored bool
	// Played are the voice messages we told the core were played
	// (upstream's played_told).
	Played model.IDList
	// Online is what SetOnline last told the core (logic keeps it).
	Online, OnlineSent bool

	Model    *model.Store
	Settings AccountSettings
	// SpaceFolder is the folder the account's space shows alone (folders.go),
	// "": the whole account.
	SpaceFolder string

	// The window's state that belongs to the account (zf:src/account.rs):
	// the open chat, the list's mode and chip, and the label the chip names.
	Open   model.ChatRef
	Mode   ListMode
	Filter Filter
	Label  model.IDList // one id: the label of FilterLabel
	// tabs are the chats in the titlebar's tabs (ChatTabs).
	tabs ChatTabs

	st  *State
	cmd abi.Command
	// What SetHistoryPrefetch last told the core.
	prefetchSent, prefetchOn bool
	prefetchChat             model.ChatRef
	settingsGen, savedGen    uint64
	savedHash                uint64
	prefs                    *prefs.Store[accountFile]
	// openID is the open chat's JID, kept for the OnChatClose hooks (the
	// chat may be gone from the model when it closes).
	openID []byte
	// unreadAt is how many messages of chat unreadRef were unread when it
	// opened (UnreadAtOpen).
	unreadRef model.ChatRef
	unreadAt  int
	// linkWas is the link status before the last link event; offline and
	// reconnecting are what has been announced of a drop
	// (zf:src/app.rs announced_offline, announced_connecting).
	linkWas                abi.LinkStatusTag
	offlineSaid, retrySaid bool
	// starting is the chat StartChat asked the core for, opened once it
	// is in (until startBy).
	starting []byte
	startBy  time.Time
	// removed is the ref of the chat a ChatRemoved being applied names.
	removed model.ChatRef
}

// Event applies one event (core.Handler): the model first, then the
// screens that listen for its tag.
func (a *Account) Event(e *abi.Event) {
	a.pinConversations() // also observes native viewer close / account switch
	if e.Tag == abi.EventTagChatRemoved {
		a.removed = a.Model.Chats.Find(e.ChatRemoved.Chat) // its ref, gone after the apply
	}
	a.lockingNotices(e)
	wasSyncing, _, _ := a.Model.Syncing()
	a.Model.Event(e)
	a.st.dispatch(a, e, wasSyncing)
}

// Active reports whether the account is the one on screen. Listeners update
// a hidden account only, never the window's composer, dialogs, playback or
// read state (upstream's events_hidden).
func (a *Account) Active() bool { return a.st.Active == a.Index }

// Cmd returns the account's reused command, cleared, with tag set: fill it
// and Send it. Byte fields may point at anything that lives until Send
// returns (it copies).
func (a *Account) Cmd(tag abi.CommandTag) *abi.Command {
	a.cmd = abi.Command{Tag: tag}
	return &a.cmd
}

// Send sends cmd to the account's core. Without a core (still opening, or
// failed) it reports ErrNoCore. A refusal is logged and returned.
func (a *Account) Send(cmd *abi.Command) error {
	if a.Conn == nil {
		// Try again on a core that failed to open opens it again.
		if cmd.Tag == abi.CommandTagReconnect && a.st != nil && a.st.Reopen(a) {
			return nil
		}
		return ErrNoCore
	}
	err := a.Conn.Send(cmd)
	if err != nil {
		slog.Warn("zapfast: a command was refused", "tag", cmd.Tag.String(), "err", err)
	}
	return err
}

// OpenChat opens the chat: its conversation is made ready in the model and
// its history asked for (LoadChat), and it is marked read at once, here and
// in the core (zf:src/app.rs:5261-5266, Action::OpenChat). 0 closes the
// open chat.
func (a *Account) OpenChat(ref model.ChatRef) {
	if ref != a.Open && a.Active() {
		if c, ok := a.Model.Chats.Get(ref); ok {
			if mention, has := c.UnreadMention(); has {
				a.OpenMessage(c.ID(), mention) // before openChat marks it read
				return
			}
		}
	}
	a.openChat(ref, true)
}

// openChat is OpenChat; read false leaves the chat's unread state alone
// (the chat open at quit, put back at start while the window is not
// looked at, as upstream's Account::new: it is read once it is).
func (a *Account) openChat(ref model.ChatRef, read bool) {
	if ref == a.Open {
		return
	}
	st := a.st
	if c, ok := a.Model.Chats.Get(ref); ok {
		// Notifications and stale requests never open a locked chat
		// outside the unlocked folder (zf:src/app.rs open_chat).
		if c.Locked() && a.Mode != ListLocked {
			return
		}
		// A chat that is not locked closes the locked folder.
		if !c.Locked() && a.Mode == ListLocked {
			a.CloseLockedFolder()
		}
	}
	if a.Active() {
		p := &st.Panes
		p.Selecting, p.Sweeping = false, false
		p.Selection.Reset()
		p.SelectAnchor.Reset()
		p.SweepBase.Reset()
		p.PendingDelete.Reset()
		p.Revoking.Reset()
		// The side panes belong to the chat they were opened in.
		p.Pane = PaneNone
		p.Info.Reset()
		p.MessageInfo.Reset()
		p.Jump.Chat = 0
		p.Jump.Msg.Reset()
		p.ReactTarget.Reset()
	}
	a.setOpen(ref)
	st.gen++
	c, ok := a.Model.Chats.Get(ref)
	if !ok {
		a.setOpen(0)
		return
	}
	id := c.ID()
	if a.Active() {
		st.Panes.Anchor.Reset()
	}
	a.Model.Open(id)
	cmd := a.Cmd(abi.CommandTagLoadChat)
	cmd.LoadChat.Chat = id
	a.Send(cmd)
	cmd = a.Cmd(abi.CommandTagLoadPins)
	cmd.LoadPins.Chat = id
	a.Send(cmd)
	if !bytes.Equal(a.Settings.LastChat, id) {
		a.Settings.LastChat = append(a.Settings.LastChat[:0], id...)
		a.SettingsChanged()
	}
	a.unreadRef, a.unreadAt = ref, c.Unread()
	if read && a.Active() && (c.Unread() > 0 || c.MarkedUnread()) {
		a.MarkRead(ref)
	}
	if a.Active() {
		st.history.visit(location{page: st.Page, chat: ref})
	}
}

// Prefetch readies chat ref's conversation before it opens (the pointer
// rests on its row): its latest messages are asked for once, so that opening
// it draws them at once instead of a moment later. A conversation already
// loaded, the open chat and a locked chat outside the locked folder are left
// alone; nothing else changes (it is not read, nor opened).
func (a *Account) Prefetch(ref model.ChatRef) {
	if ref == 0 || ref == a.Open || a.Conn == nil {
		return
	}
	c, ok := a.Model.Chats.Get(ref)
	if !ok || (c.Locked() && a.Mode != ListLocked) || a.Model.Conversation(c.ID()) != nil {
		return
	}
	a.Model.Open(c.ID())
	cmd := a.Cmd(abi.CommandTagLoadChat)
	cmd.LoadChat.Chat = c.ID()
	a.Send(cmd)
}

// setOpen makes ref the open chat, running the OnChatClose hooks for the
// one it replaces.
func (a *Account) setOpen(ref model.ChatRef) {
	if ref == a.Open {
		return
	}
	a.st.chatClosing(a)
	a.Open = ref
	a.openID = a.openID[:0]
	if c, ok := a.Model.Chats.Get(ref); ok {
		a.openID = append(a.openID, c.ID()...)
		a.tabs.note(a, ref)
	}
	a.pinConversations()
}

func (a *Account) pinConversations() {
	var viewer model.ChatRef
	if a.Active() && (a.st.Win.Want[WinViewer] || a.st.Win.Open[WinViewer]) {
		viewer = a.st.Win.ViewerChat
	}
	a.Model.PinConversations(a.Open, viewer)
}

// load makes the open chat's conversation ready and asks for its history
// and pins when they are not loaded (an account that comes on screen).
func (a *Account) load() {
	a.pinConversations()
	c, ok := a.Model.Chats.Get(a.Open)
	if !ok || a.Model.Conversation(c.ID()) != nil {
		return
	}
	a.Model.Open(c.ID())
	cmd := a.Cmd(abi.CommandTagLoadChat)
	cmd.LoadChat.Chat = c.ID()
	a.Send(cmd)
	cmd = a.Cmd(abi.CommandTagLoadPins)
	cmd.LoadPins.Chat = c.ID()
	a.Send(cmd)
}

// UnreadAtOpen is how many of chat ref's messages were unread when it
// opened, for the unread divider (upstream's UnreadDivider.count,
// zf:src/app.rs:5204-5211): opening marks it read at once. A chat not
// opened through OpenChat gives its count now.
func (a *Account) UnreadAtOpen(ref model.ChatRef) int {
	if ref != 0 && ref == a.unreadRef {
		return a.unreadAt
	}
	c, ok := a.Model.Chats.Get(ref)
	if !ok {
		return 0
	}
	return c.Unread()
}

// MarkRead marks the chat read, with read receipts as the settings say.
func (a *Account) MarkRead(ref model.ChatRef) {
	c, ok := a.Model.Chats.Get(ref)
	if !ok {
		return
	}
	// Read here at once, as upstream: the Dock badge and the list never
	// show the message the reader is looking at as unread while the core
	// confirms (zf:src/app.rs:5057-5064).
	a.Model.Chats.MarkRead(ref)
	cmd := a.Cmd(abi.CommandTagMarkRead)
	cmd.MarkRead.Chat = c.ID()
	cmd.MarkRead.Receipts = a.Settings.SendReadReceipts && !a.Model.ReceiptsOff()
	a.Send(cmd)
}

// SendText sends text to the chat (no quote, no mentions: the composer
// fills SendText itself for those).
func (a *Account) SendText(ref model.ChatRef, text []byte) error {
	c, ok := a.Model.Chats.Get(ref)
	if !ok {
		return ErrNoChat
	}
	cmd := a.Cmd(abi.CommandTagSendText)
	cmd.SendText.Chat, cmd.SendText.Text = c.ID(), text
	return a.Send(cmd)
}

// FetchAvatar asks for jid's picture once (model.Avatars.Ask).
func (a *Account) FetchAvatar(jid []byte, full bool) {
	if a.Conn == nil || !a.Model.Avatars.Ask(jid, full) {
		return
	}
	cmd := a.Cmd(abi.CommandTagFetchAvatar)
	cmd.FetchAvatar.ID, cmd.FetchAvatar.Full = jid, full
	a.Send(cmd)
}

// OpenChatView is the open chat's view; ok is false when none is open.
// A locked chat outside the open locked folder (LockedAway) gives no view,
// whatever left it open.
func (a *Account) OpenChatView() (model.Chat, bool) {
	c, ok := a.Model.Chats.Get(a.Open)
	if !ok || (c.Locked() && a.Mode != ListLocked) {
		return model.Chat{}, false
	}
	return c, true
}

// Conversation is the open chat's loaded messages, nil when none.
func (a *Account) Conversation() *model.Conversation {
	c, ok := a.Model.Chats.Get(a.Open)
	if !ok {
		return nil
	}
	return a.Model.Conversation(c.ID())
}

type stateError string

func (e stateError) Error() string { return string(e) }

// The errors of Send and its helpers.
const (
	ErrNoCore = stateError("zapfast: the account's core is not open")
	ErrNoChat = stateError("zapfast: no such chat")
)

// Panes is the main window's state beside the list: the column beside the
// conversation, selection mode and the picker.
type Panes struct {
	Pane Pane
	// Info is the info panel's chat (it can show a member of the open group,
	// so not always the open one) and view.
	Info     model.IDList // one JID
	InfoView InfoView
	// MessageInfo is the message whose receipts PaneMessageInfo shows.
	MessageInfo model.IDList // one message id, of the open chat
	// Selecting is selection mode; Selection the selected message ids of
	// the open chat, in the chat's order.
	Selecting bool
	Selection model.IDList
	// PendingDelete are messages being deleted for us, hidden until the core
	// confirms (logic owns the reducer); Revoking those being deleted for
	// everyone, drawn as deleted until the core's update.
	PendingDelete, Revoking model.IDList
	// SelectAnchor is the message the last click in selection mode was on
	// (shift-click selects from it); a mouse sweep runs from SweepAnchor to
	// SweepTo over what was selected when it began (SweepBase).
	SelectAnchor                    model.IDList
	Sweeping                        bool
	SweepAnchor, SweepTo, SweepBase model.IDList
	// Picker is the open picker, PickerNone for none.
	Picker Picker
	// ListHidden collapses the chat list into the rail (⌘B).
	ListHidden bool
	// Reply is the message the composer quotes, Edit the one it edits
	// (both message ids of the open chat; empty for none).
	Reply, Edit model.IDList
	// ReplyChat is the quoted message's chat (a JID) when it is not the
	// chat the reply goes to: a private reply to a group's message
	// (ReplyTarget.Chat). Empty: the open chat.
	ReplyChat model.IDList
	// Anchor is the message the transcript scrolls to once it is loaded
	// (a search hit, a quote, the oldest row before an older page): one
	// message id of the open chat, empty for none (upstream's
	// scroll_anchor). The state pages toward it; ui/conversation scrolls
	// and clears it.
	Anchor model.IDList
	// Jump is the message to bring into view and flash (OpenMessage).
	Jump Jump
	// PickerAnchor is the composer's emoji button (window pixels), which
	// the picker opens above: the composer sets it every frame it shows.
	PickerAnchor Rect
	// ReactTarget is the message the reaction picker is open for.
	ReactTarget ReactTarget
	// Covering: a narrow window gives the side pane the whole window (the
	// shell sets it each frame): the pane shows Back, and an item that goes
	// to a message closes it so the message can be seen.
	Covering bool
}

// SetInfo opens the info panel on jid (nil closes it).
func (p *Panes) SetInfo(jid []byte) {
	p.Info.Reset()
	if jid == nil {
		if p.Pane == PaneInfo {
			p.Pane = PaneNone
		}
		return
	}
	p.Info.Add(jid)
	p.Pane, p.InfoView = PaneInfo, InfoOverview
}

// Windows are the open requests for the app's other windows. A screen sets
// one (State.OpenWindow) and the shell opens or focuses the window at the
// next frame; closing the window clears it.
type Windows struct {
	Want [NumWindows]bool // asked for
	Open [NumWindows]bool // open now (the shell keeps it)
	Shut [NumWindows]bool // asked to close (CloseWindow)
	// Alone: asked for by OpenWindowAlone (it may open with no main
	// window, and comes forward).
	Alone [NumWindows]bool
	// Viewer is what the media viewer shows: a message of a chat (and a
	// carousel card of it, ViewerCard, when ViewerHasCard). OpenViewer sets
	// them.
	ViewerChat    model.ChatRef
	ViewerMsg     model.IDList
	ViewerCard    int
	ViewerHasCard bool
	// ViewerPlay: the viewer starts the video playing at once (a double
	// click on a clip in the transcript, or its "Fill the window":
	// upstream's ExpandVideo), from ViewerAt, where the bubble's clip was.
	// Set by OpenViewerPlaying, cleared by OpenViewer; the viewer takes it
	// with TakeViewerPlay once it shows the item.
	ViewerPlay bool
	ViewerAt   time.Duration
}

// Lock is the app lock (zf:src/app_lock.rs): the window shows only the lock
// screen while Locked.
type Lock struct {
	Locked bool
	// LastInput is when the user last did something, for Settings'
	// "lock after" (the lock screen's package runs the timer).
	LastInput time.Time
}

// Requests are one-shot asks from the shell's commands to the screens that
// own what they ask for: the owner acts and clears its flag.
type Requests struct {
	FocusSearch   bool // ⌘K, ⌘⇧F: ui/chatlist focuses its search field
	ClearSearch   bool // the locked folder closed: ui/chatlist empties its search
	FocusComposer bool // ⌘L: ui/composer takes the keyboard
	FindInChat    bool // ⌘F with a chat open: ui/conversation opens the search pane
	FindSettings  bool // ⌘F in the Settings window: ui/settings
	JumpToBottom  bool // ⌘End: ui/conversation scrolls to the newest message
	// RaiseMain brings the main window forward (State.RaiseMain): the shell.
	RaiseMain bool
	// Insert is text for the composer to insert at its cursor
	// (State.InsertEmoji; TakeInsert).
	Insert []byte
	// SpotlightText is the text Spotlight opens with (typing with no chat
	// open, shell/keyboard.go), which it takes.
	SpotlightText []byte
	// Prefill is a link's text for a chat's composer (PrefillComposer).
	Prefill Prefill
	// Restore is a refused send's text for the open chat's composer
	// (upstream's restore_text): the composer takes it into an empty
	// composer that is not editing, keeps it as the chat's draft (unlike a
	// Prefill), focuses itself, and clears the request; otherwise it drops
	// it.
	Restore Prefill
}

// Toast is a short message at the bottom of the window.
type Toast struct {
	Text  []byte // never changed once queued
	Error bool
	At    time.Time
	ID    uint64 // unique, rising: the shell dismisses by it
}

// Video is the speed and the sound the clips play with, the bubble's and
// the viewer's alike (upstream's one video::Player): what one switches,
// the next clip, here or there, plays with, until quitting. Never saved.
type Video struct {
	Rate  float32 // 0 is 1x
	Muted bool
}

// Speed is the rate to play at: Rate, 1 when unset.
func (v *Video) Speed() float32 {
	if v.Rate <= 0 {
		return 1
	}
	return v.Rate
}

// State is the app's state. Make it with New.
type State struct {
	Accounts []*Account
	Active   int
	Settings Settings

	Page   Page
	Panes  Panes
	Dialog Dialog
	Win    Windows
	Lock   Lock
	Video  Video

	// AddingAccount shows the login screen for a new account over the
	// linked one; AccountMenu is the switcher's popover.
	AddingAccount, AccountMenu bool
	// Spotlight is ⌘K's search over everything, open (ui/spotlight).
	Spotlight bool
	// SpaceGen counts the moves between spaces: the list slides in from the
	// side they came from (shell).
	SpaceGen uint32
	// Focused is whether the main window has the focus (read state).
	Focused bool

	// Toasts waiting to show, oldest first (the shell shows the first).
	Toasts []Toast

	// Req are the shell's one-shot asks to the screens that own them.
	Req Requests
	// Forwarding and Voice are logic's reducers' state: the forwarding
	// dialog's recipients, the voice-message run.
	Forwarding Forward
	Voice      Voice
	// Notices are the notifications logic decided to show, and the chats
	// whose notifications should go, oldest first: package sys posts them
	// and empties the lists. Attention asks for the dock's bounce.
	Notices      []Notice
	NoticeClears []NoticeClear
	Attention    bool
	// Staged are the open chat's attachments in the composer, which
	// ui/composer owns (it keeps other chats' with their drafts); the
	// cropper reaches a picture by ID (StagedByID).
	Staged []Staged
	// Visible is the chat list's rows as it shows them now, top to bottom
	// (ui/chatlist sets it every frame): ⌥↑/↓ and ⌘1…9 walk it. Empty:
	// the model's order.
	Visible []model.ChatRef

	// Wake asks for a frame from any goroutine (the shell sets it).
	Wake func()
	// OpenCore opens a new account's core (AddNewAccount): pending from
	// core.OpenAsync in the app, or a conn already open (a fake in tests).
	OpenCore func(id string) (*core.Pending, core.Conn)
	// ArchiveKeys is the OS keyring of the cores' archive keys
	// (core.KeyringService): a removed account's key is deleted once its
	// archive is gone. Nil (New, tests, the demo): no key is touched; only
	// cmd/zapfast installs the real one.
	ArchiveKeys KeyStore
	// Shell opens files and links: New's refuses, and only cmd/zapfast
	// installs the real opener (tests install fakes).
	Shell core.Shell
	// Demo: the accounts run the demo core, which keeps no settings of its
	// own and refuses the commands that set them (not sent).
	Demo bool
	// Now is the clock (tests replace it).
	Now func() time.Time

	history      history
	listeners    [256][]Listener
	hooks        hooks
	gen          uint64
	settingsGen  uint64
	persist      *persist
	beforeAdding string                 // the account shown before AddNewAccount
	coresStarted bool                   // StartCores ran
	quitting     bool                   // Quit ran
	removeFailed atomic.Pointer[string] // why a removed account's folders stayed (the first error)
	// toastMu guards Toasts and toastSeq: Toast may be called from any
	// goroutine.
	toastMu  sync.Mutex
	toastSeq uint64
	said     []announcement // what waits to be announced (toastMu too)
}

// New returns the state with the settings' defaults and no account.
func New() *State {
	return &State{Settings: DefaultSettings(), Now: time.Now, Wake: func() {}, Shell: &core.RefusingShell{}}
}

// Gen moves whenever the window's state changes through State's methods
// (screens that change fields directly call Touch).
func (s *State) Gen() uint64 { return s.gen }

// Touch marks the state changed.
func (s *State) Touch() { s.gen++ }

var accountSerial atomic.Uint64

// AddAccount adds an account opening on its core (pending, from
// core.OpenAsync, or conn when it is open already: a fake in tests).
func (s *State) AddAccount(id string, pending *core.Pending, conn core.Conn) *Account {
	a := &Account{Index: len(s.Accounts), Serial: accountSerial.Add(1), ID: id, Pending: pending, Conn: conn, Model: model.New(), Settings: DefaultAccountSettings(), st: s}
	s.attach(a)
	s.Accounts = append(s.Accounts, a)
	if conn != nil {
		a.opened()
	}
	s.gen++
	return a
}

// Acct is the account on screen, nil before there is one.
func (s *State) Acct() *Account {
	if s.Active < 0 || s.Active >= len(s.Accounts) {
		return nil
	}
	return s.Accounts[s.Active]
}

// SwitchAccount puts account i on screen (zf:src/app.rs:1608). The
// navigation history belongs to the account and is cleared; an account
// being added that was left before it linked is removed.
func (s *State) SwitchAccount(i int) {
	if i < 0 || i >= len(s.Accounts) || i == s.Active {
		return
	}
	var abandoned *Account
	if a := s.Acct(); a != nil {
		// The locked folder does not stay open behind the account's back
		// (zf:src/app.rs clear_account_ui): its locked chat closes.
		a.CloseLockedFolder()
		// The chat on screen goes with its account (parked, not closed).
		s.chatClosing(a)
		if s.AddingAccount && !a.Model.Linked() {
			abandoned = a
		}
	}
	s.clearAccountUI()
	s.Active = i
	s.AddingAccount = false
	if abandoned != nil {
		s.RemoveAccount(abandoned.Index)
	}
	s.Acct().load()
	// The Calls page stays open on the other account's calls.
	if a := s.Acct(); s.Page == PageCalls {
		a.Send(a.Cmd(abi.CommandTagLoadCallLog))
	} else {
		s.Page = PageChats
	}
	s.gen++
}

// OpenWindow asks the shell to open (or bring forward) window w.
func (s *State) OpenWindow(w Window) {
	s.Win.Want[w] = true
	s.gen++
	s.Wake()
}

// OpenWindowAlone opens window w and brings it forward without the main
// window: while the main window is closed, w opens by itself and the main
// window stays closed (a call that rings while ZapFast is in the
// background, CA-004); with the main window open it opens as OpenWindow
// does, in front. Wanted windows of other kinds wait for the main window.
func (s *State) OpenWindowAlone(w Window) {
	s.Win.Alone[w] = true
	s.OpenWindow(w)
}

// OpenDialog opens d, replacing any open dialog.
func (s *State) OpenDialog(d Dialog) {
	s.Dialog = d
	s.gen++
}

// CloseDialog closes the open dialog.
func (s *State) CloseDialog() {
	s.Dialog = Dialog{}
	s.gen++
}

// Toast queues a toast (its text is copied), from any goroutine: a worker
// that failed to open a file may call it. It wakes the window. As upstream
// keeps them (zf:src/app.rs:8321-8375), at most four info toasts (the
// oldest goes) and three errors (an error already shown moves to the end;
// the oldest goes).
//
// Toasts is read on the main thread through ToastsInto and changed through
// DismissToast; a test with no worker posting may read the field directly.
func (s *State) Toast(text []byte, isError bool) {
	s.toast(nil, text, isError)
}

// Toast queues an account's event toast on the main thread. Speech names
// the account when it is off screen; worker toasts use State.Toast instead.
func (a *Account) Toast(text []byte, isError bool) {
	a.st.toast(a, text, isError)
}

// toast is the account-bound event path (main thread). Keep the visual
// toast unchanged; only speech names an account that is not on screen
// (zf:src/app.rs:8299-8319). Plain Toast remains safe for workers.
func (s *State) toast(a *Account, text []byte, isError bool) {
	t := Toast{Text: append([]byte(nil), text...), Error: isError, At: s.Now()}
	said := t.Text
	if a != nil && !a.Active() {
		before, rest, _ := strings.Cut(i18n.T("{}: {}"), "{}")
		between, after, _ := strings.Cut(rest, "{}")
		said = []byte(before + a.Name() + between + string(text) + after)
	}
	s.toastMu.Lock()
	s.toastSeq++
	t.ID = s.toastSeq
	if isError {
		s.Toasts = slices.DeleteFunc(s.Toasts, func(o Toast) bool { return o.Error && bytes.Equal(o.Text, t.Text) })
	}
	limit := 4
	if isError {
		limit = 3
	}
	n := 0
	for _, o := range s.Toasts {
		if o.Error == isError {
			n++
		}
	}
	for ; n >= limit; n-- {
		if i := slices.IndexFunc(s.Toasts, func(o Toast) bool { return o.Error == isError }); i >= 0 {
			s.Toasts = slices.Delete(s.Toasts, i, i+1)
		}
	}
	s.Toasts = append(s.Toasts, t)
	// A toast says itself (zf:src/app.rs toast, toast_error).
	kind := AnnounceToast
	if isError {
		kind = AnnounceError
	}
	s.announceLocked(kind, said, t.At)
	s.toastMu.Unlock()
	if w := s.Wake; w != nil {
		w()
	}
}

// ToastsInto appends the waiting toasts, oldest first, to dst and returns
// it: a copy the main thread can show while workers post. With dst's room
// reused it allocates nothing.
func (s *State) ToastsInto(dst []Toast) []Toast {
	s.toastMu.Lock()
	dst = append(dst, s.Toasts...)
	s.toastMu.Unlock()
	return dst
}

// DismissToast removes the toast with id (its time ran out, or it was
// closed).
func (s *State) DismissToast(id uint64) {
	s.toastMu.Lock()
	s.Toasts = slices.DeleteFunc(s.Toasts, func(o Toast) bool { return o.ID == id })
	s.toastMu.Unlock()
	s.Touch()
}

// Listener is a screen or reducer that wants the events of some tags, after
// the model has applied them. The event's bytes are valid during the call
// only. a may be an account that is not on screen (Account.Active).
type Listener interface {
	Event(a *Account, e *abi.Event)
}

// Listen calls l for every event with one of the tags, in the order the
// listeners were added. Call it from a screen's Init.
//
// A listener already added for a tag is not added again (a screen's Init
// runs again when its window opens again).
func (s *State) Listen(l Listener, tags ...abi.EventTag) {
	same := reflect.TypeOf(l) != nil && reflect.TypeOf(l).Comparable()
next:
	for _, t := range tags {
		if int(t) >= len(s.listeners) {
			continue
		}
		if same {
			for _, have := range s.listeners[t] {
				if reflect.TypeOf(have) == reflect.TypeOf(l) && have == l {
					continue next
				}
			}
		}
		s.listeners[t] = append(s.listeners[t], l)
	}
}

func (s *State) dispatch(a *Account, e *abi.Event, wasSyncing bool) {
	ls := s.listeners[uint8(e.Tag)]
	for _, l := range ls {
		l.Event(a, e)
	}
	s.policy(a, e, len(ls), wasSyncing)
}

// Pump takes the cores that finished opening and drains every account's
// events into its model, within budget in all (2 ms a frame; 0: no limit).
// It reports whether anything was applied. The shell calls it at the start
// of the main window's frame (and package sys while no window is open).
func (s *State) Pump(budget time.Duration) bool {
	start := s.Now()
	applied := false
	for _, a := range s.Accounts {
		if a.Conn == nil && a.Pending != nil && a.Pending.Done() {
			c, err := a.Pending.Conn()
			a.Pending = nil
			switch {
			case err != nil:
				a.Err = err.Error()
				slog.Error("zapfast: opening the core", "account", a.ID, "err", err)
			case c != nil:
				a.Conn = c
				a.opened()
				a.startReopened()
			}
			s.gen++
		}
		if a.Conn == nil {
			continue
		}
		left := time.Duration(0)
		if budget > 0 {
			left = budget - s.Now().Sub(start)
			if left <= 0 {
				s.Wake() // the rest next frame
				break
			}
		}
		n, err := a.Conn.Drain(a, left)
		if err != nil && a.Err == "" {
			a.Err = err.Error()
			s.gen++
		}
		applied = applied || n > 0
	}
	s.drainRemoved()
	s.syncPrefetch()
	s.saveSettings(false)
	return applied
}

// StartCores gives every open core its startup permit (after the first
// frame).
func (s *State) StartCores() {
	s.coresStarted = true
	for _, a := range s.Accounts {
		if a.Conn != nil && !a.Started {
			a.Started = true
			if err := a.Conn.Start(); err != nil {
				slog.Warn("zapfast: starting the core", "account", a.ID, "err", err)
			}
		}
	}
}

// Reopen opens the core of account a again after it failed to open (Err
// is set, and there is no core nor one opening): the login screen's Try
// again. A core that could not take its account over from the Rust core
// (cmd/zapfast) gets its next chance this way. It reports whether it did.
func (s *State) Reopen(a *Account) bool {
	if s.OpenCore == nil || a.Conn != nil || a.Pending != nil || a.Err == "" || a.Removed {
		return false
	}
	pending, conn := s.OpenCore(a.ID)
	a.Err = ""
	a.Pending, a.Conn = pending, conn
	a.Started, a.startOnOpen = false, s.coresStarted
	if conn != nil {
		a.opened()
		a.startReopened()
	}
	s.gen++
	return true
}

// startReopened gives a reopened core its permit once it is open.
func (a *Account) startReopened() {
	if !a.startOnOpen || a.Started || a.Conn == nil {
		return
	}
	a.startOnOpen, a.Started = false, true
	if err := a.Conn.Start(); err != nil {
		slog.Warn("zapfast: starting the core", "account", a.ID, "err", err)
	}
}

// Back and Forward walk the navigation history (the mouse's buttons,
// zf:src/nav.rs, zf:src/app.rs navigate_back): a place the window can no
// longer show (its chat gone, or locked outside its folder) is forgotten,
// and the step goes on to the one before it. They report whether they
// moved.
func (s *State) Back() bool    { return s.walk(false) }
func (s *State) Forward() bool { return s.walk(true) }

func (s *State) walk(forward bool) bool {
	a := s.Acct()
	if a == nil {
		return false
	}
	for {
		var l location
		var ok bool
		if forward {
			l, ok = s.history.forward()
		} else {
			l, ok = s.history.back()
		}
		if !ok {
			return false
		}
		if s.show(a, l) {
			return true
		}
	}
}

// show puts the window at place l and reports whether it could
// (zf:src/app.rs show_location).
func (s *State) show(a *Account, l location) bool {
	if l.chat != a.Open {
		a.setOpen(0) // OpenChat would record the visit again
		s.history.paused = true
		if _, ok := a.Model.Chats.Get(l.chat); ok {
			a.OpenChat(l.chat)
		}
		s.history.paused = false
	}
	s.Page = l.page
	s.gen++
	if l.chat != 0 && a.Open != l.chat {
		s.history.forget(l.chat)
		return false
	}
	return true
}

// RecordPlace records where the window is (the page and the open chat) for
// the mouse's Back and Forward, when it changed: the shell calls it every
// frame, after the screens (zf:src/app.rs record_location). It allocates
// nothing.
func (s *State) RecordPlace() {
	if a := s.Acct(); a != nil {
		s.history.visit(location{page: s.Page, chat: a.Open})
	}
}

// CanBack and CanForward report whether there is somewhere to go.
func (s *State) CanBack() bool    { return len(s.history.past) > 0 }
func (s *State) CanForward() bool { return len(s.history.future) > 0 }

// location is a place in the interface: the page and the chat open on it.
type location struct {
	page Page
	chat model.ChatRef
}

// history is zf:src/nav.rs's History over refs, which are the account's:
// it is cleared when the account on screen changes.
type history struct {
	past, future []location
	current      location
	has, paused  bool
}

const historyLimit = 64

func (h *history) visit(l location) {
	if h.paused || (h.has && h.current == l) {
		return
	}
	if h.has {
		if h.past == nil {
			h.past = make([]location, 0, historyLimit+1) // its whole length at once: no growth in a frame
		}
		h.past = append(h.past, h.current)
		if len(h.past) > historyLimit {
			h.past = append(h.past[:0], h.past[1:]...)
		}
	}
	h.current, h.has = l, true
	h.future = h.future[:0]
}

func (h *history) back() (location, bool) {
	if len(h.past) == 0 {
		return location{}, false
	}
	t := h.past[len(h.past)-1]
	h.past = h.past[:len(h.past)-1]
	if h.has {
		h.future = append(h.future, h.current)
	}
	h.current, h.has = t, true
	return t, true
}

func (h *history) forward() (location, bool) {
	if len(h.future) == 0 {
		return location{}, false
	}
	t := h.future[len(h.future)-1]
	h.future = h.future[:len(h.future)-1]
	if h.has {
		h.past = append(h.past, h.current)
	}
	h.current, h.has = t, true
	return t, true
}

func (h *history) clear() { *h = history{past: h.past[:0], future: h.future[:0]} }

// forget drops every place that had chat open; the current one, if it
// did, is replaced by the next without being kept (zf:src/nav.rs forget).
func (h *history) forget(chat model.ChatRef) {
	keep := func(l []location) []location {
		out := l[:0]
		for _, x := range l {
			if x.chat != chat {
				out = append(out, x)
			}
		}
		return out
	}
	h.past, h.future = keep(h.past), keep(h.future)
	if h.has && h.current.chat == chat {
		h.has = false
	}
}
