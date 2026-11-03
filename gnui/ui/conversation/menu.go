package conversation

import (
	"bytes"
	"path/filepath"

	"gonativeui/icon"
	"gonativeui/rexui"
	"gonativeui/ui"
	"gonativeui/widget/base"

	"zapfast/abi"
	"zapfast/i18n"
	"zapfast/logic"
	"zapfast/model"
	"zapfast/state"
	"zapfast/ui/calls"
	"zapfast/ui/conversation/av"
	"zapfast/ui/screen"
)

// quickReactions are the menu's reactions (zf:src/ui/conversation.rs
// QUICK_REACTIONS).
var quickReactions = [...]string{"👍", "❤️", "😂", "😮", "😢", "🙏"}

// item is one row of a drawn menu: a plain button the menu's width.
func item(k *base.Kit, ic icon.ID, label string) bool {
	k.NextWidth(ui.Pct(1))
	k.NextAlign(ui.AlignLeft)
	return rexui.ButtonIcon(k, ic, label, rexui.ButtonOpts{Variant: rexui.ButtonPlain}).Clicked
}

func itemOff(k *base.Kit, ic icon.ID, label string, on bool) bool {
	k.BeginDisabled(!on)
	clicked := item(k, ic, label)
	k.EndDisabled()
	return clicked && on
}

// The message menu's items (mm*); a reaction is mmReact + its index in quick,
// a playback speed mmSpeed + its index in av.Speeds.
const (
	mmReply = iota + 1
	mmMoreReactions
	mmForward
	mmCopyText
	mmEdit
	mmStar
	mmPin
	mmSelect
	mmViewer
	mmOpen
	mmCopyImage
	mmSaveAs
	mmShowFolder
	mmDownload
	mmFavSticker
	mmInfo
	mmReload
	mmCopyID
	mmRevoke
	mmDeleteMine
	mmVoiceCall
	mmVideoCall
	mmReact = 100
	mmSpeed = 200
)

// contextMenu is a message's menu (zf:src/ui/conversation.rs context_menu),
// opened by a right click on its bubble (openMenu): a native menu on macOS
// and rexui's drawn one elsewhere (rexui.MenuAt), in WhatsApp's groups:
// answering it (Reply, React, Forward, Copy, Edit), keeping it (Star, Pin,
// Select), its media, what it is (Info, Reload, Copy message ID), and
// deleting it, set apart.
func (s *Screen) contextMenu(e *screen.Env, a *state.Account, ch model.Chat, v *model.Conversation) {
	if v == nil || s.menu.msg.Len() == 0 {
		s.menu.open = false
		return
	}
	mi := v.Find(s.menu.msg.At(0))
	if mi < 0 {
		s.menu.open = false
		return
	}
	m := v.At(mi)
	c, k := e.C(), e.K.Kit
	opening := s.menu.opening
	s.menu.opening = false
	at := s.menu.anchor
	item, ok, closed := rexui.MenuAt(k, c.Parent(), opening, at[0], at[1], func(items *[]ui.MenuItem) { s.messageItems(e, a, ch, m, items) })
	if ok {
		s.messageAct(e, a, ch, m, item)
	}
	if closed {
		s.menu.open = false
	}
}

// messageItems describes m's menu.
func (s *Screen) messageItems(e *screen.Env, a *state.Account, ch model.Chat, m model.Message, items *[]ui.MenuItem) {
	add := func(it ui.MenuItem) { *items = append(*items, it) }
	if m.Kind() == abi.ContentTagCall { // a call's menu calls back (CV-033)
		switch {
		case !canCall(a, ch):
		case ch.Kind() == abi.ChatKindDirect && m.Decode(&s.dec) == nil && s.dec.Content.Call.Group:
			add(ui.MenuItem{Title: i18n.T("Group calls happen on the phone"), Disabled: true})
		default:
			add(ui.MenuItem{ID: mmVoiceCall, Title: i18n.T("Voice call"), Symbol: "phone", Icon: "phone"})
			add(ui.MenuItem{ID: mmVideoCall, Title: i18n.T("Video call"), Symbol: "video", Icon: "video"})
		}
		return
	}
	chatID, msgID := ch.ID(), m.ID()
	revoked := m.Kind() == abi.ContentTagRevoked
	age := s.unix - m.Time()
	if !revoked {
		add(ui.MenuItem{ID: mmReply, Title: i18n.T("Reply"), Symbol: "arrowshape.turn.up.left", Icon: "reply"})
		mine := ownReaction(m)
		var react []ui.MenuItem
		for i, emoji := range s.quick(e, mine) {
			it := ui.MenuItem{ID: mmReact + i, Title: model.S(emoji)}
			if string(emoji) == model.S(mine) {
				it.Mark = ui.MarkOn
			}
			react = append(react, it)
		}
		react = append(react, ui.MenuSeparator, ui.MenuItem{ID: mmMoreReactions, Title: i18n.T("React with any emoji")})
		add(ui.MenuItem{Title: i18n.T("React"), Symbol: "face.smiling", Icon: "smile-plus", Sub: react})
	}
	if canSelect(m) {
		add(ui.MenuItem{ID: mmForward, Title: i18n.T("Forward"), Symbol: "arrowshape.turn.up.right", Icon: "forward"})
	}
	if hasCopyText(m) {
		add(ui.MenuItem{ID: mmCopyText, Title: i18n.T("Copy text"), Symbol: "doc.on.doc", Icon: "copy"})
	}
	if m.FromMe() && m.Kind() == abi.ContentTagText && age <= editWindow {
		add(ui.MenuItem{ID: mmEdit, Title: i18n.T("Edit"), Symbol: "pencil", Icon: "pencil"})
	}
	add(ui.MenuSeparator)
	if !revoked {
		if a.Model.Starred(chatID, msgID) {
			add(ui.MenuItem{ID: mmStar, Title: i18n.T("Unstar"), Symbol: "star.slash", Icon: "star-off"})
		} else {
			add(ui.MenuItem{ID: mmStar, Title: i18n.T("Star"), Symbol: "star", Icon: "star"})
		}
		if a.Model.PinnedMessage(chatID, msgID) {
			add(ui.MenuItem{ID: mmPin, Title: i18n.T("Unpin message"), Symbol: "pin.slash", Icon: "pin-off"})
		} else {
			add(ui.MenuItem{ID: mmPin, Title: i18n.T("Pin message"), Symbol: "pin", Icon: "pin"})
		}
	}
	if canSelect(m) {
		add(ui.MenuItem{ID: mmSelect, Title: i18n.T("Select"), Symbol: "checkmark.circle", Icon: "circle-check"})
	}
	add(ui.MenuSeparator)
	md, hasMedia := m.Media()
	if hasMedia {
		visual := m.Kind() == abi.ContentTagImage || m.Kind() == abi.ContentTagVideo || m.Kind() == abi.ContentTagSticker
		if len(md.Path) > 0 {
			if visual {
				add(ui.MenuItem{ID: mmViewer, Title: i18n.T("Open in the viewer"), Symbol: "photo", Icon: "image"})
			}
			open := "Open file"
			if m.Kind() == abi.ContentTagVideo && !md.Gif {
				open = "Open in system player"
			}
			add(ui.MenuItem{ID: mmOpen, Title: i18n.T(open), Symbol: "arrow.up.forward.app", Icon: "external-link"})
			if m.Kind() == abi.ContentTagImage {
				add(ui.MenuItem{ID: mmCopyImage, Title: i18n.T("Copy image"), Symbol: "doc.on.clipboard", Icon: "clipboard-copy"})
			}
			add(ui.MenuItem{ID: mmSaveAs, Title: i18n.T("Save as…"), Symbol: "square.and.arrow.down", Icon: "download"})
			add(ui.MenuItem{ID: mmShowFolder, Title: i18n.T("Show in folder"), Symbol: "folder", Icon: "folder-open"})
			if m.Kind() == abi.ContentTagSticker {
				add(ui.MenuItem{ID: mmFavSticker, Title: i18n.T("Add to favorites"), Symbol: "star", Icon: "star"})
			}
			if m.Kind() == abi.ContentTagAudio {
				cur := av.Speed(e.S)
				var sp []ui.MenuItem
				for i, v := range av.Speeds {
					it := ui.MenuItem{ID: mmSpeed + i, Title: av.SpeedLabel(v)}
					if v == cur {
						it.Mark = ui.MarkOn
					}
					sp = append(sp, it)
				}
				add(ui.MenuItem{Title: i18n.T("Playback speed"), Symbol: "gauge.with.dots.needle.67percent", Icon: "gauge", Sub: sp})
			}
		} else {
			downloading := md.State == abi.MediaStateTagDownloading
			label := i18n.T("Download")
			if downloading {
				label = i18n.T("Downloading…")
			}
			add(ui.MenuItem{ID: mmDownload, Title: label, Symbol: "arrow.down.circle", Icon: "circle-arrow-down", Disabled: downloading})
		}
		add(ui.MenuSeparator)
	}
	sent := m.Status() != abi.DeliveryPending && m.Status() != abi.DeliveryFailed
	if m.FromMe() && sent {
		add(ui.MenuItem{ID: mmInfo, Title: i18n.T("Message info"), Symbol: "info.circle", Icon: "info"})
	}
	add(ui.MenuItem{ID: mmReload, Title: i18n.T("Reload earlier messages"), Symbol: "clock.arrow.circlepath", Icon: "history",
		Disabled: !(a.Model.Link.Connected() && sent)})
	add(ui.MenuItem{ID: mmCopyID, Title: i18n.T("Copy message ID"), Symbol: "number", Icon: "hash"})
	if m.FromMe() && !revoked && age <= revokeWin {
		add(ui.MenuItem{ID: mmRevoke, Title: i18n.T("Delete for everyone"), Symbol: "trash", Icon: "trash-2", Destructive: true})
	}
	add(ui.MenuItem{ID: mmDeleteMine, Title: i18n.T("Delete for me"), Symbol: "trash", Icon: "trash-2", Destructive: true})
}

// messageAct does what item of m's menu chose.
func (s *Screen) messageAct(e *screen.Env, a *state.Account, ch model.Chat, m model.Message, item int) {
	chatID, msgID := ch.ID(), m.ID()
	pn := &e.S.Panes
	md, _ := m.Media()
	switch {
	case item == mmVoiceCall || item == mmVideoCall:
		calls.Place(e.S, a, chatID, item == mmVideoCall)
	case item == mmReply:
		pn.Edit.Reset()
		pn.Reply.Reset()
		pn.Reply.Add(msgID)
		e.S.Req.FocusComposer = true
		e.S.Touch()
	case item >= mmReact && item < mmSpeed:
		mine := ownReaction(m)
		q := s.quick(e, mine)
		if i := item - mmReact; i < len(q) {
			emoji := q[i]
			if string(emoji) == model.S(mine) {
				emoji = nil // choosing our own reaction again takes it back
			}
			a.React(chatID, msgID, emoji)
		}
	case item == mmMoreReactions:
		r := s.menu.anchor
		e.S.OpenReaction(ch.Ref(), msgID, state.Rect{X: r[0], Y: r[1], W: r[2] - r[0], H: r[3] - r[1]})
	case item == mmForward:
		s.one.Reset()
		s.one.Add(msgID)
		logic.OpenForward(e.S, a, ch.Ref(), &s.one)
	case item == mmCopyText:
		s.copyMessageText(e, a, m)
	case item == mmEdit:
		pn.Reply.Reset()
		pn.Edit.Reset()
		pn.Edit.Add(msgID)
		e.S.Req.FocusComposer = true
		e.S.Touch()
	case item == mmStar:
		cmd := a.Cmd(abi.CommandTagSetStar)
		cmd.SetStar.Chat, cmd.SetStar.Message, cmd.SetStar.Starred = chatID, msgID, !a.Model.Starred(chatID, msgID)
		a.Send(cmd)
	case item == mmPin:
		pinned := a.Model.PinnedMessage(chatID, msgID)
		if !pinned && s.livePins(a, chatID) >= a.Model.PinLimit() {
			e.S.Toast([]byte(i18n.T("This chat already has three pinned messages")), false)
			return
		}
		cmd := a.Cmd(abi.CommandTagSetMessagePinned)
		cmd.SetMessagePinned.Chat, cmd.SetMessagePinned.Message, cmd.SetMessagePinned.Pinned = chatID, msgID, !pinned
		a.Send(cmd)
	case item == mmSelect:
		logic.SelectMessage(e.S, a, msgID)
	case item == mmViewer:
		e.S.OpenViewer(ch.Ref(), msgID, model.PartMain)
	case item == mmOpen:
		openPath(e, md.Path)
	case item == mmCopyImage:
		cmd := a.Cmd(abi.CommandTagPrepareClipboardImage)
		cmd.PrepareClipboardImage.Path = md.Path
		a.Send(cmd)
	case item == mmSaveAs:
		cmd := a.Cmd(abi.CommandTagSaveAttachmentAs)
		cmd.SaveAttachmentAs.Source, cmd.SaveAttachmentAs.Name = md.Path, attachmentName(m, md.Path)
		a.Send(cmd)
	case item == mmShowFolder:
		openPath(e, []byte(filepath.Dir(string(md.Path))))
	case item == mmDownload:
		cmd := a.Cmd(abi.CommandTagDownload)
		cmd.Download.Manual, cmd.Download.Chat, cmd.Download.Message = true, chatID, msgID
		a.Send(cmd)
	case item == mmFavSticker:
		cmd := a.Cmd(abi.CommandTagSaveSticker)
		cmd.SaveSticker.Path = md.Path
		a.Send(cmd)
	case item >= mmSpeed && item < mmSpeed+len(av.Speeds):
		av.SetSpeed(e.S, av.Speeds[item-mmSpeed])
	case item == mmInfo:
		pn.MessageInfo.Reset()
		pn.MessageInfo.Add(msgID)
		pn.Pane = state.PaneMessageInfo
		e.S.Touch()
	case item == mmReload:
		cmd := a.Cmd(abi.CommandTagReloadHistory)
		cmd.ReloadHistory.Chat, cmd.ReloadHistory.Message = chatID, msgID
		a.Send(cmd)
	case item == mmCopyID:
		s.copied = append(s.copied[:0], msgID...)
		setClipboard(e, s.copied)
	case item == mmRevoke || item == mmDeleteMine:
		d := state.Dialog{Kind: state.DialogConfirmDeleteMessage, Chat: ch.Ref(), ForEveryone: item == mmRevoke}
		d.IDs.Add(msgID)
		e.S.OpenDialog(d)
	}
}

// attachmentName is the name Save as… suggests (zf:src/ui/conversation.rs
// attachment_name): a document's own, else the file's, with no path
// separators. It runs on a click.
func attachmentName(m model.Message, path []byte) []byte {
	name := []byte(filepath.Base(string(path)))
	if md, ok := m.Media(); ok && m.Kind() == abi.ContentTagDocument && len(bytes.TrimSpace(md.FileName)) > 0 {
		name = append([]byte(nil), md.FileName...)
	}
	if len(name) == 0 || string(name) == "." || string(name) == "/" {
		name = []byte("attachment")
	}
	for i, c := range name {
		if c == '/' || c == '\\' {
			name[i] = '_'
		}
	}
	return name
}

// quick are the menu's reactions (zf:src/ui/conversation.rs
// quick_reactions): the most used first, then the defaults, six in all, and
// our own reaction when it is none of them.
func (s *Screen) quick(e *screen.Env, mine []byte) [][]byte {
	q := s.quickBuf[:0]
	has := func(b []byte) bool {
		for _, x := range q {
			if string(x) == string(b) {
				return true
			}
		}
		return false
	}
	for _, r := range e.S.Settings.ReactionEmoji {
		if len(q) == len(quickReactions) {
			break
		}
		if len(r.Emoji) > 0 && !has(r.Emoji) {
			q = append(q, r.Emoji)
		}
	}
	for _, d := range quickReactions {
		if len(q) == len(quickReactions) {
			break
		}
		if b := base.StrBytes(d); !has(b) {
			q = append(q, b)
		}
	}
	if len(mine) > 0 && !has(mine) {
		q = append(q, mine)
	}
	s.quickBuf = q
	return q
}

// ownReaction is the emoji we reacted to m with, nil for none.
func ownReaction(m model.Message) []byte {
	if !m.Reacted() {
		return nil
	}
	for j := 0; j < m.Reactions(); j++ {
		if emoji, _, mine := m.Reaction(j); mine {
			return emoji
		}
	}
	return nil
}

func openPath(e *screen.Env, path []byte) { e.S.OpenFile(string(path)) }

// The More menu's items.
const (
	moreInfo = iota + 1
	moreSelect
	morePin
	moreArchive
	moreClear
	moreLeave
	moreCopyNumber
	moreClose
)

// moreItems describes the header's More menu (zf:src/ui/conversation.rs
// header), a drawn dropdown under its button (rexui.DropdownMenu).
func (s *Screen) moreItems(a *state.Account, ch model.Chat, items *[]ui.MenuItem) {
	add := func(it ui.MenuItem) { *items = append(*items, it) }
	add(ui.MenuItem{ID: moreInfo, Title: i18n.T("Info"), Symbol: "info.circle", Icon: "info"})
	add(ui.MenuItem{ID: moreSelect, Title: i18n.T("Select messages"), Symbol: "checkmark.circle", Icon: "circle-check"})
	if ch.Pinned() {
		add(ui.MenuItem{ID: morePin, Title: i18n.T("Unpin"), Symbol: "pin.slash", Icon: "pin-off"})
	} else {
		add(ui.MenuItem{ID: morePin, Title: i18n.T("Pin to top"), Symbol: "pin", Icon: "pin"})
	}
	if ch.Archived() {
		add(ui.MenuItem{ID: moreArchive, Title: i18n.T("Unarchive"), Symbol: "archivebox", Icon: "archive-restore"})
	} else {
		add(ui.MenuItem{ID: moreArchive, Title: i18n.T("Archive"), Symbol: "archivebox", Icon: "archive"})
	}
	if ph := model.Phone(ch.ID()); len(ph) > 0 && !ch.Group() {
		add(ui.MenuItem{ID: moreCopyNumber, Title: i18n.T("Copy number"), Symbol: "doc.on.doc", Icon: "copy"})
	}
	add(ui.MenuItem{ID: moreClose, Title: i18n.T("Close chat"), Symbol: "xmark", Icon: "x"})
	add(ui.MenuSeparator)
	add(ui.MenuItem{ID: moreClear, Title: i18n.T("Clear chat"), Symbol: "eraser", Icon: "eraser", Destructive: true, Disabled: !a.Model.Link.Connected()})
	if ch.Group() && !ch.Left() && !ch.ReadOnly() {
		leave := i18n.T("Leave group")
		if isChannel(ch.ID()) {
			leave = i18n.T("Leave channel")
		}
		add(ui.MenuItem{ID: moreLeave, Title: leave, Symbol: "rectangle.portrait.and.arrow.right", Icon: "log-out", Destructive: true})
	}
}

// moreAct does what item of the More menu chose.
func (s *Screen) moreAct(e *screen.Env, a *state.Account, ch model.Chat, item int) {
	switch item {
	case moreInfo:
		e.S.Panes.SetInfo(ch.ID())
		e.S.Touch()
	case moreSelect:
		logic.StartSelection(e.S, a)
	case morePin:
		cmd := a.Cmd(abi.CommandTagSetPinned)
		cmd.SetPinned.Chat, cmd.SetPinned.Pinned = ch.ID(), !ch.Pinned()
		a.Send(cmd)
	case moreArchive:
		cmd := a.Cmd(abi.CommandTagSetArchived)
		cmd.SetArchived.Chat, cmd.SetArchived.Archived = ch.ID(), !ch.Archived()
		a.Send(cmd)
	case moreClear:
		e.S.OpenDialog(state.Dialog{Kind: state.DialogConfirmClearChat, Chat: ch.Ref()})
	case moreLeave:
		e.S.OpenDialog(state.Dialog{Kind: state.DialogConfirmLeaveGroup, Chat: ch.Ref()})
	case moreCopyNumber:
		if ph := model.Phone(ch.ID()); len(ph) > 0 {
			s.copied = append(append(s.copied[:0], '+'), ph...)
			setClipboard(e, s.copied)
		}
	case moreClose:
		a.OpenChat(0)
		e.S.Touch()
	}
}

func isChannel(jid []byte) bool {
	const suffix = "@newsletter"
	return len(jid) >= len(suffix) && string(jid[len(jid)-len(suffix):]) == suffix
}
