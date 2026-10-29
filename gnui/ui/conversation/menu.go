package conversation

import (
	"bytes"
	"path/filepath"

	"gonativeui/icon"
	"gonativeui/rexui"
	"gonativeui/theme"
	"gonativeui/ui"
	"gonativeui/widget/base"

	"zapfast/abi"
	"zapfast/i18n"
	"zapfast/logic"
	"zapfast/model"
	"zapfast/state"
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

// contextMenu is a message's menu (zf:src/ui/conversation.rs
// context_menu): the quick reactions, then what can be done with it.
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
	p := rexui.BeginPopover(k, "message menu", s.menu.anchor, rexui.PopoverOpts{Width: 280, Behavior: rexui.PopoverTransient, Label: i18n.T("Message")})
	done := false
	chatID, msgID := ch.ID(), m.ID()
	revoked := m.Kind() == abi.ContentTagRevoked
	if m.Kind() == abi.ContentTagCall {
		// a call's menu calls back (CV-033)
		done = s.callMenu(e, a, ch, m)
		rexui.EndPopover(k, &p)
		if done || p.Dismissed {
			s.menu.open = false
		}
		return
	}

	if !revoked && m.Kind() != abi.ContentTagCall {
		c.NextSize(ui.Pct(1), ui.Children(1))
		c.NextAxis(ui.AxisX)
		c.NextJustify(ui.JustifySpaceBetween)
		c.NextCross(ui.CrossCenter)
		c.Push(c.Box(0, "reactions"))
		mine := ownReaction(m)
		for _, emoji := range s.quick(e, mine) {
			chosen := string(emoji) == model.S(mine)
			v := rexui.ButtonPlain
			if chosen {
				v = rexui.ButtonDefault
			}
			if rexui.Button(k, model.S(emoji), rexui.ButtonOpts{Variant: v}).Clicked {
				// choosing our own reaction again takes it back
				if chosen {
					emoji = nil
				}
				a.React(chatID, msgID, emoji)
				done = true
			}
		}
		plus := rexui.IconButton(k, s.icons.plus, i18n.T("React with any emoji"), rexui.IconButtonOpts{Variant: rexui.IconBordered})
		if plus.Clicked {
			rr := c.RectOf(c.KeyOf(plus.ID))
			e.S.OpenReaction(ch.Ref(), msgID, state.Rect{X: rr[0], Y: rr[1], W: rr[2] - rr[0], H: rr[3] - rr[1]})
			done = true
		}
		c.Pop()
		rexui.Separator(k, rexui.SeparatorOpts{})
	}
	ic := &s.icons
	pn := &e.S.Panes
	if !revoked && item(k, ic.reply, i18n.T("Reply")) {
		pn.Edit.Reset()
		pn.Reply.Reset()
		pn.Reply.Add(msgID)
		e.S.Req.FocusComposer = true
		e.S.Touch()
		done = true
	}
	starred := a.Model.Starred(chatID, msgID)
	star := i18n.T("Star")
	if starred {
		star = i18n.T("Unstar")
	}
	if !revoked && item(k, ic.star, star) {
		cmd := a.Cmd(abi.CommandTagSetStar)
		cmd.SetStar.Chat, cmd.SetStar.Message, cmd.SetStar.Starred = chatID, msgID, !starred
		a.Send(cmd)
		done = true
	}
	pinned := a.Model.PinnedMessage(chatID, msgID)
	pin := i18n.T("Pin message")
	if pinned {
		pin = i18n.T("Unpin message")
	}
	if !revoked && item(k, ic.pin, pin) {
		if !pinned && s.livePins(a, chatID) >= a.Model.PinLimit() {
			e.S.Toast([]byte(i18n.T("This chat already has three pinned messages")), false)
		} else {
			cmd := a.Cmd(abi.CommandTagSetMessagePinned)
			cmd.SetMessagePinned.Chat, cmd.SetMessagePinned.Message, cmd.SetMessagePinned.Pinned = chatID, msgID, !pinned
			a.Send(cmd)
		}
		done = true
	}
	if canSelect(m) && item(k, ic.forward, i18n.T("Forward")) {
		s.one.Reset()
		s.one.Add(msgID)
		logic.OpenForward(e.S, a, ch.Ref(), &s.one)
		done = true
	}
	md, hasMedia := m.Media()
	if hasMedia && len(md.Path) > 0 && (m.Kind() == abi.ContentTagImage || m.Kind() == abi.ContentTagVideo || m.Kind() == abi.ContentTagSticker) &&
		item(k, ic.photo, i18n.T("Open in the viewer")) {
		e.S.OpenViewer(ch.Ref(), msgID, model.PartMain)
		done = true
	}
	if canSelect(m) && item(k, ic.check, i18n.T("Select")) {
		logic.SelectMessage(e.S, a, msgID)
		done = true
	}
	if hasCopyText(m) && item(k, ic.copy, i18n.T("Copy text")) {
		s.copyMessageText(e, a, m)
		done = true
	}
	age := s.unix - m.Time()
	if m.FromMe() && m.Kind() == abi.ContentTagText && age <= editWindow && item(k, ic.edit, i18n.T("Edit")) {
		pn.Reply.Reset()
		pn.Edit.Reset()
		pn.Edit.Add(msgID)
		e.S.Req.FocusComposer = true
		e.S.Touch()
		done = true
	}
	if m.FromMe() && !revoked && age <= revokeWin && item(k, ic.trash, i18n.T("Delete for everyone")) {
		d := state.Dialog{Kind: state.DialogConfirmDeleteMessage, Chat: ch.Ref(), ForEveryone: true}
		d.IDs.Add(msgID)
		e.S.OpenDialog(d)
		done = true
	}
	if item(k, ic.trash, i18n.T("Delete for me")) {
		d := state.Dialog{Kind: state.DialogConfirmDeleteMessage, Chat: ch.Ref()}
		d.IDs.Add(msgID)
		e.S.OpenDialog(d)
		done = true
	}
	if hasMedia && m.Kind() == abi.ContentTagSticker && len(md.Path) > 0 && item(k, ic.star, i18n.T("Add to favorites")) {
		cmd := a.Cmd(abi.CommandTagSaveSticker)
		cmd.SaveSticker.Path = md.Path
		a.Send(cmd)
		done = true
	}
	if hasMedia {
		if len(md.Path) > 0 {
			open := "Open file"
			if m.Kind() == abi.ContentTagVideo && !md.Gif {
				open = i18n.T("Open in system player")
			}
			if item(k, ic.open, i18n.T(open)) {
				openPath(e, md.Path)
				done = true
			}
			if m.Kind() == abi.ContentTagImage && item(k, ic.copy, i18n.T("Copy image")) {
				cmd := a.Cmd(abi.CommandTagPrepareClipboardImage)
				cmd.PrepareClipboardImage.Path = md.Path
				a.Send(cmd)
				done = true
			}
			if item(k, ic.down, i18n.T("Save as…")) {
				cmd := a.Cmd(abi.CommandTagSaveAttachmentAs)
				cmd.SaveAttachmentAs.Source, cmd.SaveAttachmentAs.Name = md.Path, attachmentName(m, md.Path)
				a.Send(cmd)
				done = true
			}
			if item(k, ic.folder, i18n.T("Show in folder")) {
				openPath(e, []byte(filepath.Dir(string(md.Path))))
				done = true
			}
		} else {
			downloading := md.State == abi.MediaStateTagDownloading
			label := i18n.T("Download")
			if downloading {
				label = i18n.T("Downloading…")
			}
			if itemOff(k, ic.down, label, !downloading) {
				cmd := a.Cmd(abi.CommandTagDownload)
				cmd.Download.Manual, cmd.Download.Chat, cmd.Download.Message = true, chatID, msgID
				a.Send(cmd)
				done = true
			}
		}
	}
	if hasMedia && m.Kind() == abi.ContentTagAudio && len(md.Path) > 0 {
		rexui.Separator(k, rexui.SeparatorOpts{})
		done = s.speedRow(e) || done
	}
	rexui.Separator(k, rexui.SeparatorOpts{})
	if m.FromMe() && m.Status() != abi.DeliveryPending && m.Status() != abi.DeliveryFailed && item(k, ic.info, i18n.T("Message info")) {
		pn.MessageInfo.Reset()
		pn.MessageInfo.Add(msgID)
		pn.Pane = state.PaneMessageInfo
		e.S.Touch()
		done = true
	}
	reload := a.Model.Link.Connected() && m.Status() != abi.DeliveryPending && m.Status() != abi.DeliveryFailed
	if itemOff(k, ic.clock, i18n.T("Reload earlier messages"), reload) {
		cmd := a.Cmd(abi.CommandTagReloadHistory)
		cmd.ReloadHistory.Chat, cmd.ReloadHistory.Message = chatID, msgID
		a.Send(cmd)
		done = true
	}
	if item(k, ic.copy, i18n.T("Copy message ID")) {
		s.copied = append(s.copied[:0], msgID...)
		setClipboard(e, s.copied)
		done = true
	}
	rexui.EndPopover(k, &p)
	if done || p.Dismissed {
		s.menu.open = false
	}
}

// speedRow is the menu's row of every playback speed for a playable audio
// message (zf:src/ui/conversation.rs speed_menu_row), so 1.25x and 1.75x
// are a click away without cycling the chip; it reports a choice.
func (s *Screen) speedRow(e *screen.Env) (chosen bool) {
	c, k := e.C(), e.K.Kit
	r := k.Theme()
	cur := av.Speed(e.S)
	c.NextSize(ui.Pct(1), ui.Px(e.Pt(28)))
	c.NextAxis(ui.AxisX)
	c.NextCross(ui.CrossCenter)
	c.NextGap(e.Pt(4))
	c.NextPadding(e.Pt(8), 0, e.Pt(8), 0)
	c.Push(c.Box(0, "speeds"))
	for i, v := range av.Speeds {
		on := v == cur
		label := av.SpeedLabel(v)
		f := fillBytes(c.Fmt(), i18n.T("Playback speed {label}"), "{label}", base.StrBytes(label))
		fill, ink := r.Colors[theme.Fill1], r.Colors[theme.Label2]
		if on {
			fill, ink = theme.WithAlpha(r.Colors[theme.Accent], 0.30), r.Colors[theme.Accent]
		}
		c.NextSize(ui.Px(e.Pt(44)), ui.Px(e.Pt(24)))
		c.NextBG(fill)
		c.NextRadius(e.Pt(12))
		c.NextFont(s.look.smallMed)
		c.NextTextColor(ink)
		c.NextAlign(ui.AlignCenter)
		c.NextRole(ui.RoleButton)
		c.NextSemName(f.String())
		if on {
			c.NextSemState(ui.SemSelected)
		}
		c.NextCursor(ui.CursorPointer)
		id := c.BoxKeyed(ui.Clickable|ui.DrawBackground|ui.DrawText|ui.DrawHotEffects, ui.KeyN(c.ParentKey(), uint64(i)), base.StrBytes(label))
		if c.Signal(id).Clicked {
			av.SetSpeed(e.S, v)
			chosen = true
		}
	}
	c.Pop()
	return chosen
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

// moreMenu is the header's More menu (zf:src/ui/conversation.rs header).
func (s *Screen) moreMenu(e *screen.Env, a *state.Account, ch model.Chat, anchor [4]float32) {
	k := e.K.Kit
	p := rexui.BeginPopover(k, "chat menu", anchor, rexui.PopoverOpts{Width: 220, Edge: rexui.PopoverBelow, Behavior: rexui.PopoverTransient, Label: i18n.T("More")})
	ic := &s.icons
	done := false
	if item(k, ic.info, i18n.T("Info")) {
		e.S.Panes.SetInfo(ch.ID())
		e.S.Touch()
		done = true
	}
	if item(k, ic.check, i18n.T("Select messages")) {
		logic.StartSelection(e.S, a)
		done = true
	}
	pinTop := i18n.T("Pin to top")
	if ch.Pinned() {
		pinTop = i18n.T("Unpin")
	}
	if item(k, ic.pin, pinTop) {
		cmd := a.Cmd(abi.CommandTagSetPinned)
		cmd.SetPinned.Chat, cmd.SetPinned.Pinned = ch.ID(), !ch.Pinned()
		a.Send(cmd)
		done = true
	}
	arch := i18n.T("Archive")
	if ch.Archived() {
		arch = i18n.T("Unarchive")
	}
	if item(k, ic.archive, arch) {
		cmd := a.Cmd(abi.CommandTagSetArchived)
		cmd.SetArchived.Chat, cmd.SetArchived.Archived = ch.ID(), !ch.Archived()
		a.Send(cmd)
		done = true
	}
	if itemOff(k, ic.eraser, i18n.T("Clear chat"), a.Model.Link.Connected()) {
		e.S.OpenDialog(state.Dialog{Kind: state.DialogConfirmClearChat, Chat: ch.Ref()})
		done = true
	}
	rexui.Separator(k, rexui.SeparatorOpts{})
	if ch.Group() && !ch.Left() && !ch.ReadOnly() {
		leave := i18n.T("Leave group")
		if isChannel(ch.ID()) {
			leave = i18n.T("Leave channel")
		}
		if item(k, ic.close, leave) {
			e.S.OpenDialog(state.Dialog{Kind: state.DialogConfirmLeaveGroup, Chat: ch.Ref()})
			done = true
		}
	}
	if ph := model.Phone(ch.ID()); len(ph) > 0 && !ch.Group() && item(k, ic.copy, i18n.T("Copy number")) {
		s.copied = append(append(s.copied[:0], '+'), ph...)
		setClipboard(e, s.copied)
		done = true
	}
	if item(k, ic.close, i18n.T("Close chat")) {
		a.OpenChat(0)
		e.S.Touch()
		done = true
	}
	rexui.EndPopover(k, &p)
	if done || p.Dismissed {
		s.more = false
	}
}

func isChannel(jid []byte) bool {
	const suffix = "@newsletter"
	return len(jid) >= len(suffix) && string(jid[len(jid)-len(suffix):]) == suffix
}
