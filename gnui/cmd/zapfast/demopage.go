//go:build !gnui_release

package main

import (
	"log/slog"
	"strings"
	"time"

	"zapfast/abi"
	"zapfast/core"
	"zapfast/model"
	"zapfast/state"
	"zapfast/ui/screen"
)

// The demo core's samples (zfcore/src/demo.rs SAMPLES, GROUP_MEMBERS).
const (
	demoAda      = "393331234567@s.whatsapp.net"
	demoGrace    = "441632960123@s.whatsapp.net"
	demoKather   = "4915112345678@s.whatsapp.net"
	demoGroup    = "120363012345678901@g.us" // Rust Berlin: Ada, Grace, Katherine
	demoFamily   = "120363098765432109@g.us"
	demoMargaret = "14155550199@s.whatsapp.net" // a poll, a contact, a location, a round video, edits, reactions
	demoDorothy  = "15550004444@s.whatsapp.net" // locked: a voice note, a photo, a clip
	demoQR       = "2@ZapFastDemoQR,demo-public-key,demo-identity,demo-adv"
	demoPairCode = "FWAP1234"
)

// demoPage puts the demo in one of upstream's demo states (--demo-page
// NAME[,NAME…], zf:src/demo.rs apply_flags; DEMO.md): once the sample
// chats are in, each part changes the state as upstream's does, through
// State and the demo core's commands; parts that point at messages wait
// for the open chat's history. Parts the Go screens cannot show yet are
// logged and skipped. It is a Ticker: it runs in the live window and in an
// offscreen render alike.
type demoPage struct {
	parts []string
	done  bool
	// late: the parts on messages still wait for the history (frames
	// left before they give up).
	late    bool
	waitFor int
	settle  int // frames since the history came in
	// link keeps the account's link as the page shows it (the demo core
	// reports it connected): 0 for the core's.
	link abi.LinkStatusTag
}

// demoParts are the parts this port knows, for -demo-page help and tests.
var demoParts = []string{
	"chat", "empty", "light", "dark", "short-chat", "wide", "nosidebar", "rail",
	"search", "spotlight", "folders", "space", "info", "info-media", "info-docs", "info-links", "group-info", "select", "select-delete", "delete-selection",
	"settings", "themes", "app-lock-settings", "shortcuts", "about", "new-chat", "new-contact",
	"poll", "poll-create", "location", "unlink", "clear-media", "delete-chat", "clear-chat", "leave-group",
	"delete-message", "forward", "reply", "private-reply", "edit", "message-info", "unread-divider",
	"typers", "typing", "live", "preview", "toasts", "offline", "syncing", "login", "phone", "pair",
	"calls", "call-entries", "call", "call-video-incoming", "call-group", "starred", "archived",
	"account-menu", "accounts", "app-lock",
	// on the demo core's data
	"poll-voted", "poll-results", "shared-contact", "reactions", "edited", "voice", "video", "photos",
	"receipts-played", "message-info-partial", "message-info-unknown", "message-info-direct",
	"unread", "private", "favorites", "groups", "picker", "gifs", "gifs-badkey", "stickers",
	"drafts", "disconnected", "labels-dialog", "locked-setup", "chat-search", "staged",
	"sticker-pack-message", "sticker-pack-view", "locked", "locked-prompt", "locked-open",
}

// demoChatOf is the chat a part opens, where it is not the first (the
// pinned Ada): the sample whose data the part shows.
func demoChatOf(part string) string {
	switch part {
	case "group-info", "unread-divider", "typers", "private-reply", "call-group", "leave-group",
		"message-info", "message-info-partial":
		return demoGroup
	case "poll", "poll-voted", "poll-results", "shared-contact", "reactions", "edited":
		return demoMargaret
	case "message-info-unknown":
		return demoFamily // sent before receipts were recorded
	case "sticker-pack-message", "sticker-pack-view":
		return demoGroup // the shared "Launch party" pack
	case "locked-open":
		return demoDorothy
	case "receipts-played":
		return demoGrace // our voice message, played
	}
	return ""
}

// newDemoPage parses the page list; nil for none.
func newDemoPage(list string) *demoPage {
	if strings.TrimSpace(list) == "" {
		return nil
	}
	p := &demoPage{}
	for part := range strings.SplitSeq(list, ",") {
		p.parts = append(p.parts, strings.TrimSpace(part))
	}
	return p
}

func (p *demoPage) has(names ...string) bool {
	for _, part := range p.parts {
		for _, n := range names {
			if part == n {
				return true
			}
		}
	}
	return false
}

// size is the window size a page asks for (points), 0 for the default.
func (p *demoPage) size() (w, h float32) {
	switch {
	case p == nil:
	case p.has("short-chat"):
		return 620, 640
	case p.has("wide"):
		return 1440, 900
	}
	return 0, 0
}

// light reports whether the page asks for the light theme.
func (p *demoPage) light() bool { return p != nil && p.has("light") }

func (p *demoPage) Frame(e *screen.Env) {
	s := e.S
	a := s.Acct()
	if a == nil {
		return
	}
	if p.link != 0 && a.Model.Link.Status() != p.link {
		p.setLink(a)
	}
	switch {
	case !p.done:
		if !a.Model.Chats.Loaded() || a.Model.Chats.Len() == 0 {
			return
		}
		p.done, p.late, p.waitFor = true, true, 240
		p.apply(s, a)
	case p.late:
		if v := a.Conversation(); a.Open != 0 && (v == nil || v.Len() == 0) && p.waitFor > 0 {
			p.waitFor--
			return
		}
		// a few frames for the transcript to lay out what came in (a jump
		// before it has settled is undone by its scroll to the end)
		if p.settle < 8 {
			p.settle++
			e.S.Touch()
			return
		}
		p.late = false
		p.applyLate(s, a)
	}
}

// apply sets the page's parts that need only the chats.
func (p *demoPage) apply(s *state.State, a *state.Account) {
	chats := &a.Model.Chats
	// Most pages show the first chat (the sample's pinned one) open; some
	// the group, some none.
	var open model.ChatRef
	if p.has("locked-open") {
		a.Mode = state.ListLocked // unlocked, showing the folder (the core locks Dorothy's chat)
	}
	if !p.has("empty", "login", "phone", "pair", "calls", "call-entries", "account-menu", "accounts", "app-lock", "locked", "locked-prompt") {
		for _, part := range p.parts {
			if jid := demoChatOf(part); jid != "" && open == 0 {
				open = chats.Find([]byte(jid))
			}
		}
		if order := chats.Order(); open == 0 && len(order) > 0 {
			open = order[0]
		}
	}
	if open != 0 {
		a.OpenChat(open)
	}
	for _, part := range p.parts {
		switch part {
		case "", "chat", "empty", "short-chat", "light", "unread-divider",
			"shared-contact", "edited", "voice", "receipts-played":
		case "dark":
			s.Settings.Theme = state.ThemeDark
		case "wide":
			s.Settings.SidebarWidth = 520
		case "nosidebar", "rail":
			s.Panes.ListHidden = true
		case "search":
			s.Req.FocusSearch = true
		case "folders":
			seedDemoFolders(a)
		case "space":
			seedDemoFolders(a)
			if i := a.FolderByID("f1"); i >= 0 {
				s.GoToSpace(state.Space{Acct: a.Index, Folder: "f1"})
			}
		case "spotlight":
			s.Spotlight = true
			s.Req.SpotlightText = append(s.Req.SpotlightText, "an"...)
		case "info", "group-info", "info-media", "info-docs", "info-links":
			if c, ok := a.OpenChatView(); ok {
				s.Panes.SetInfo(c.ID())
				switch part {
				case "info-media":
					s.Panes.InfoView = state.InfoMedia
				case "info-docs":
					s.Panes.InfoView = state.InfoDocs
				case "info-links":
					s.Panes.InfoView = state.InfoLinks
				}
			}
		case "select":
			s.Panes.Selecting = true
		case "settings", "themes", "app-lock-settings":
			s.OpenWindow(state.WinSettings)
		case "shortcuts":
			s.OpenDialog(state.Dialog{Kind: state.DialogShortcuts})
		case "about":
			s.OpenDialog(state.Dialog{Kind: state.DialogAbout})
		case "new-chat":
			s.OpenDialog(state.Dialog{Kind: state.DialogNewChat})
		case "new-contact":
			s.OpenDialog(state.Dialog{Kind: state.DialogNewContact})
		case "poll-create":
			s.OpenDialog(state.Dialog{Kind: state.DialogCreatePoll, Chat: a.Open})
		case "location":
			s.OpenDialog(state.Dialog{Kind: state.DialogSendLocation, Chat: a.Open, Text: []byte("-23.5505, -46.6333")})
		case "unread":
			a.Filter = state.FilterUnread
		case "private":
			a.Filter = state.FilterPrivate
		case "favorites":
			a.Filter = state.FilterFavorites
		case "groups":
			a.Filter = state.FilterGroups
		case "picker":
			s.Panes.Picker = state.PickerEmoji
		case "gifs", "gifs-badkey":
			s.Settings.GiphyKey = []byte("zf-demo-key") // the demo core's GIFs
			if part == "gifs-badkey" {
				s.Settings.GiphyKey = []byte("zf-demo:bad-key")
			}
			s.Panes.Picker = state.PickerGifs
		case "stickers":
			s.Panes.Picker = state.PickerStickers
		case "drafts":
			// Unsent text in two chats besides the open one, whose draft
			// is in the composer.
			a.Model.SetDraft([]byte(demoKather), []byte("Bring the spare HDMI adapter"))
			a.Model.SetDraft([]byte(demoGrace), []byte("Sounds good, see you at\nthe station"))
			if c, ok := a.OpenChatView(); ok {
				s.Req.Restore = state.Prefill{Acct: a.Serial, Chat: append([]byte(nil), c.ID()...), Text: []byte("Still typing this one")}
			}
		case "disconnected":
			p.link = abi.LinkStatusTagDisconnected
			p.setLink(a)
		case "labels-dialog":
			s.OpenDialog(state.Dialog{Kind: state.DialogLabels})
		case "locked-setup":
			s.OpenDialog(state.Dialog{Kind: state.DialogUnlockLockedChats})
		case "chat-search":
			s.Panes.Pane = state.PaneSearch
		case "locked", "locked-open":
		case "locked-prompt":
			if len(s.Settings.ChatLockHash) == 0 {
				s.Settings.SetChatLockCode("demo-code")
			}
			s.OpenDialog(state.Dialog{Kind: state.DialogUnlockLockedChats})
		case "unlink":
			s.OpenDialog(state.Dialog{Kind: state.DialogConfirmUnlink})
		case "clear-media":
			s.OpenDialog(state.Dialog{Kind: state.DialogConfirmClearMediaCache, Account: a.Index})
		case "delete-chat":
			s.OpenDialog(state.Dialog{Kind: state.DialogConfirmDeleteChat, Chat: a.Open})
		case "clear-chat":
			s.OpenDialog(state.Dialog{Kind: state.DialogConfirmClearChat, Chat: a.Open})
		case "leave-group":
			s.OpenDialog(state.Dialog{Kind: state.DialogConfirmLeaveGroup, Chat: a.Open})
		case "toasts":
			s.Toast([]byte("Starred"), false)
			s.Toast([]byte("Could not open the attachment: no such file"), true)
		case "offline":
			p.link = abi.LinkStatusTagDisconnected
			p.setLink(a)
		case "login", "phone", "pair":
			// Unlinked with no archive: the linking screen (upstream's
			// unlink(app) empties the chats too).
			p.link = abi.LinkStatusTagUnlinked
			a.Model.Event(&abi.Event{Tag: abi.EventTagChats, Chats: abi.EventChats{Batch: 1, Last: true}})
			p.setLink(a)
		case "syncing":
			a.Model.Event(&abi.Event{Tag: abi.EventTagSyncing, Syncing: abi.EventSyncing{Syncing: true}})
			a.Model.Event(&abi.Event{Tag: abi.EventTagSyncProgress, SyncProgress: abi.EventSyncProgress{Percent: 42}})
		case "calls", "call-entries":
			s.Page = state.PageCalls
		case "call":
			demoRing(a, demoAda, demoAda, false)
		case "call-video-incoming":
			demoRing(a, demoAda, demoAda, true)
		case "call-group":
			demoRing(a, demoGroup, demoGrace, false)
		case "typers":
			// Two members typing at once (zf:src/demo.rs "typers").
			for _, who := range []string{demoAda, demoKather} {
				cmd := a.Cmd(abi.CommandTagDemoTyping)
				cmd.DemoTyping = abi.CommandDemoTyping{Chat: []byte(demoGroup), Sender: []byte(who), Composing: true}
				a.Send(cmd)
			}
		case "typing":
			// A long draft in the composer (zf:src/demo.rs "typing").
			if c, ok := a.OpenChatView(); ok {
				s.Req.Restore = state.Prefill{Acct: a.Serial, Chat: append([]byte(nil), c.ID()...), Text: []byte("line 1\nline 2\nline 3\nline 4\nline 5\nline 6\nline 7\nline 8\nline 9")}
			}
		case "live":
			// Messages arriving in the open chat while it is read.
			for i, text := range []string{"Are you coming tonight?", "Bring the notes.", "And the punch cards 🙂"} {
				cmd := a.Cmd(abi.CommandTagDemoIncoming)
				cmd.DemoIncoming = abi.CommandDemoIncoming{DelayMS: uint32(1500 * (i + 1)), Chat: []byte(demoAda), Sender: []byte(demoAda), Text: []byte(text)}
				a.Send(cmd)
			}
		case "starred":
			a.Mode = state.ListStarred
		case "archived":
			a.Mode = state.ListArchived
		case "account-menu", "accounts":
			s.AccountMenu = true
		case "app-lock":
			if len(s.Settings.AppLockHash) == 0 {
				s.Settings.AppLockHash = []byte("pbkdf2-sha256$600000$ZGVtbw$ZGVtbw") // never matches: the demo's screen only
			}
			s.Lock.Locked = true
		case "reply", "private-reply", "edit", "message-info", "forward", "select-delete", "delete-selection", "delete-message", "preview", "staged",
			"message-info-partial", "message-info-unknown", "message-info-direct", "video", "photos",
			"poll", "poll-voted", "poll-results", "reactions", "sticker-pack-message", "sticker-pack-view":
			// on the history: applyLate
		default:
			slog.Warn("zapfast: this demo page is not in the Go UI yet", "part", part)
		}
	}
	s.Touch()
}

// applyLate sets the parts that point at the open chat's messages.
func (p *demoPage) applyLate(s *state.State, a *state.Account) {
	v := a.Conversation()
	if v == nil || v.Len() == 0 {
		return
	}
	// The newest incoming message, the newest from a member with a chat of
	// their own (a private reply's), our newest text, the newest picture.
	var in, own, pic, video, poll, reacted, pack, member, memberChat []byte
	chatID := a.Model.Chats.At(a.Open).ID()
	for i := v.Len() - 1; i >= 0; i-- {
		m := v.At(i)
		switch {
		case !m.FromMe() && in == nil:
			in = m.ID()
		case m.FromMe() && own == nil && m.Kind() == abi.ContentTagText:
			own = m.ID()
		}
		if !m.FromMe() && member == nil && string(m.Sender()) != string(chatID) && a.Model.Chats.Find(m.Sender()) != 0 {
			member, memberChat = m.ID(), m.Sender()
		}
		if pic == nil && m.Kind() == abi.ContentTagImage {
			pic = m.ID()
		}
		if poll == nil && m.Kind() == abi.ContentTagPoll {
			poll = m.ID()
		}
		if pack == nil && m.Kind() == abi.ContentTagStickerPack {
			pack = m.ID()
		}
		if reacted == nil && m.Reactions() > 0 {
			reacted = m.ID()
		}
		if video == nil && m.Kind() == abi.ContentTagVideo {
			var d abi.Message
			if v.Decode(i, &d) == nil && !d.Content.Video.Gif && !d.Content.Video.Note {
				video = m.ID()
			}
		}
	}
	ids := func(list ...[]byte) model.IDList {
		var l model.IDList
		for _, id := range list {
			if id != nil {
				l.Add(id)
			}
		}
		return l
	}
	for _, part := range p.parts {
		switch part {
		case "reply":
			if in != nil {
				s.Panes.Reply.Reset()
				s.Panes.Reply.Add(in)
				s.Req.FocusComposer = true
			}
		case "private-reply":
			// A group message answered in its sender's own chat, quoting it.
			if member == nil {
				break
			}
			quote, group := append([]byte(nil), member...), append([]byte(nil), chatID...)
			if ref := a.Model.Chats.Find(memberChat); ref != 0 {
				a.OpenChat(ref)
				s.Panes.Reply.Reset()
				s.Panes.Reply.Add(quote)
				s.Panes.ReplyChat.Reset()
				s.Panes.ReplyChat.Add(group)
				s.Req.FocusComposer = true
			}
		case "edit":
			if own != nil {
				s.Panes.Edit.Reset()
				s.Panes.Edit.Add(own)
			}
		case "message-info", "message-info-partial", "message-info-unknown", "message-info-direct":
			if own != nil {
				s.Panes.Pane = state.PaneMessageInfo
				s.Panes.MessageInfo.Reset()
				s.Panes.MessageInfo.Add(own)
			}
		case "forward":
			if in != nil {
				s.OpenDialog(state.Dialog{Kind: state.DialogForward, Chat: a.Open, IDs: ids(in)})
			}
		case "select-delete", "delete-selection":
			s.Panes.Selecting = true
			s.Panes.Selection.Reset()
			for _, id := range [][]byte{own, in} {
				if id != nil {
					s.Panes.Selection.Add(id)
				}
			}
			if part == "delete-selection" {
				s.OpenDialog(state.Dialog{Kind: state.DialogConfirmDeleteSelection, Chat: a.Open, IDs: ids(own, in)})
			}
		case "delete-message":
			if own != nil {
				s.OpenDialog(state.Dialog{Kind: state.DialogConfirmDeleteMessage, Chat: a.Open, IDs: ids(own), ForEveryone: true})
			}
		case "preview":
			if pic != nil {
				s.OpenViewer(a.Open, pic, 0)
			}
		case "sticker-pack-view":
			if pack != nil {
				s.OpenDialog(state.Dialog{Kind: state.DialogStickerPack, Chat: a.Open, IDs: ids(pack)})
			}
		case "staged":
			// Files staged in the composer, its chips (zf:src/demo.rs
			// "staged", without the pixels the demo has no worker for).
			for i, name := range []string{"notes.pdf", "Quarterly report for the board, final version.pdf"} {
				s.Staged = append(s.Staged, state.Staged{ID: 0x5d_0000 + uint64(i), Kind: state.StagedDocument, Path: []byte("/tmp/" + name), Name: name, Ready: true})
			}
		case "photos", "video", "poll", "poll-voted", "poll-results", "reactions", "sticker-pack-message":
			// the chat at the message the page is about (the demo's
			// pictures and clip are days back)
			id := map[string][]byte{"photos": pic, "video": video, "poll": poll, "poll-voted": poll, "poll-results": poll, "reactions": reacted, "sticker-pack-message": pack}[part]
			if id != nil {
				a.OpenMessage(chatID, id)
			}
		}
	}
	s.Touch()
}

// setLink shows account a's link as the page has it: down (a timeout), or
// unlinked with a QR code (login, phone) or a pairing code (pair).
func (p *demoPage) setLink(a *state.Account) {
	st := abi.LinkStatus{Tag: p.link}
	switch {
	case p.link == abi.LinkStatusTagDisconnected && p.has("disconnected"):
		st.Disconnected.Reason = []byte("Offline")
	case p.link == abi.LinkStatusTagDisconnected:
		st.Disconnected.Reason = []byte("timeout")
	case p.has("pair"):
		st.Unlinked = abi.LinkStatusUnlinked{PairCode: []byte(demoPairCode), HasPairCode: true, PairingPhone: []byte("15550001111"), HasPairingPhone: true}
	default:
		st.Unlinked = abi.LinkStatusUnlinked{QR: []byte(demoQR), HasQR: true}
	}
	a.Model.Event(&abi.Event{Tag: abi.EventTagLink, Link: abi.EventLink{Status: st}})
}

// demoRing has the demo core ring a call from caller in chat.
func demoRing(a *state.Account, chat, caller string, video bool) {
	cmd := a.Cmd(abi.CommandTagDemoIncoming)
	core.DemoCall(cmd, chat, caller, video, 300*time.Millisecond)
	a.Send(cmd)
}

// seedDemoFolders gives the demo account two folders (state/folders.go), the
// first a space of its own, once: what the playground opens on.
func seedDemoFolders(a *state.Account) {
	if a == nil || len(a.Settings.Folders) > 0 {
		return
	}
	work := a.FolderByID(a.NewFolder("Work", "briefcase"))
	a.MoveToFolder([]byte(demoGroup), work)
	a.MoveToFolder([]byte(demoGrace), work)
	a.EditFolder(work, func(f *state.Folder) { f.Space = true })
	family := a.FolderByID(a.NewFolder("Family", "house"))
	a.MoveToFolder([]byte(demoFamily), family)
}
