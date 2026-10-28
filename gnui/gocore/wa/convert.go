package wa

import (
	"fmt"
	"strings"

	"zapfast/abi"
	"zapfast/gocore/out"
	"zapfast/gocore/store"
)

// The model⇄abi conversion, in one place: the role zfcore/src/dto.rs plays
// for the Rust core, field by field. The archive's rows (store.Chat,
// store.Message) become the schema's records here and nowhere else, with
// dto.rs's bounds: display text (names, summaries, errors) is clipped at a
// grapheme, lists that can outgrow their bound (participants, labels,
// reactions, mentions, poll voters) keep their first elements, and a link
// preview whose address cannot be carried is left off rather than losing
// the message. Ids, paths and message bodies are never cut: a record whose
// id is over its bound is dropped by gocore/out as EventDropped.

// conv is what a conversion needs from the engine: who the account is,
// and the transient state of a message's attachment.
type conv struct {
	isMe  func(id string) bool
	state func(chat, id string) abi.MediaState
}

func b(s string) []byte { return []byte(s) }

// delivery is a stored status. Upstream gives a received message no
// delivery of its own.
func delivery(status string, fromMe bool) abi.Delivery {
	if !fromMe {
		return abi.DeliveryNone
	}
	switch status {
	case "pending":
		return abi.DeliveryPending
	case "sent":
		return abi.DeliverySent
	case "delivered":
		return abi.DeliveryDelivered
	case "read":
		return abi.DeliveryRead
	case "played":
		return abi.DeliveryPlayed
	case "failed":
		return abi.DeliveryFailed
	}
	return abi.DeliveryNone
}

// chatKind is ChatKind::from_id, with the archive's kind first.
func chatKind(kind, id string) abi.ChatKind {
	switch kind {
	case "group":
		return abi.ChatKindGroup
	case "channel", "broadcast":
		return abi.ChatKindBroadcast
	case "direct":
		return abi.ChatKindDirect
	}
	switch {
	case strings.HasSuffix(id, "@g.us"):
		return abi.ChatKindGroup
	case strings.HasSuffix(id, "@newsletter"), strings.HasSuffix(id, "@broadcast"):
		return abi.ChatKindBroadcast
	}
	return abi.ChatKindDirect
}

// archiveKind is the archive's kind of a chat id.
func archiveKind(id string) string {
	switch chatKind("", id) {
	case abi.ChatKindGroup:
		return "group"
	case abi.ChatKindBroadcast:
		return "channel"
	}
	return "direct"
}

// mutedFromArchive is a chat's mute as upstream's muted_until: none when
// not muted, 0 for always, else the Unix second it ends. The archive keeps
// 0 for not muted and -1 for always.
func mutedFromArchive(until int64) (int64, bool) {
	switch {
	case until == 0:
		return 0, false
	case until < 0:
		return 0, true
	}
	return until, true
}

// mutedToArchive is SetMuted's `until` as the archive keeps it.
func mutedToArchive(until int64, has bool) int64 {
	switch {
	case !has:
		return 0
	case until == 0:
		return -1
	}
	return until
}

// chat is dto.rs chat(): last is the chat's newest message (nil for none),
// mention its oldest unread mention of the account.
func (cv conv) chat(c store.Chat, last *store.Message, mention string) abi.Chat {
	kind := chatKind(c.Kind, c.ID)
	x := abi.Chat{
		ID:                b(c.ID),
		Name:              b(out.Clip(c.Name, 1024)),
		GroupSubjectKnown: kind == abi.ChatKindGroup && c.Name != "",
		Kind:              kind,
		LastActivity:      c.LastActivity,
		Unread:            uint32(max(c.Unread, 0)),
		MarkedUnread:      c.MarkedUnread,
		Archived:          c.Archived,
		Pinned:            c.Pinned,
		PinnedAt:          c.PinnedAt,
		Favorite:          c.Favorite,
		FavoritePosition:  uint32(max(c.FavoritePosition, 0)),
		ReadOnly:          c.ReadOnly,
		Left:              c.Left,
		Admin:             c.Admin,
		Locked:            c.Locked,
	}
	x.MutedUntil, x.HasMutedUntil = mutedFromArchive(c.MutedUntil)
	if last != nil {
		x.Last, x.HasLast = cv.lastMessage(*last), true
	}
	// A group larger than the bound shows its first members only, which
	// beats losing the chat.
	for _, p := range c.Participants[:min(len(c.Participants), 1024)] {
		x.Participants = append(x.Participants, b(p))
	}
	if kind == abi.ChatKindGroup {
		x.InfoLocked, x.HasInfoLocked = c.InfoLocked, true
	}
	if c.EphemeralSeconds > 0 {
		x.EphemeralExpiration, x.HasEphemeralExpiration = c.EphemeralSeconds, true
	}
	for _, l := range c.Labels[:min(len(c.Labels), 64)] {
		x.Labels = append(x.Labels, b(l))
	}
	if mention != "" {
		x.UnreadMention, x.HasUnreadMention = b(mention), true
	}
	return x
}

func (cv conv) lastMessage(m store.Message) abi.LastMessage {
	l := abi.LastMessage{
		FromMe:  m.FromMe,
		Sender:  b(m.Sender),
		Summary: b(out.Clip(summary(m), 1024)),
		Full:    b(out.Clip(m.Text, 196608)),
		Status:  delivery(m.Status, m.FromMe),
	}
	if m.SenderName != "" && !m.FromMe {
		l.SenderName, l.HasSenderName = b(out.Clip(m.SenderName, 1024)), true
	}
	return l
}

// message is dto.rs message(): its thumbnail becomes an event blob of r.
func (cv conv) message(r *out.Record, m store.Message) abi.Message {
	x := abi.Message{
		ID:        b(m.ID),
		Chat:      b(m.ChatID),
		Sender:    b(m.Sender),
		FromMe:    m.FromMe,
		Timestamp: m.Timestamp,
		Content:   cv.content(m),
		Status:    delivery(m.Status, m.FromMe),
		Edited:    m.Edited && !m.Deleted,
		Forwarded: m.Forwarded,
	}
	if m.SenderName != "" {
		x.SenderName, x.HasSenderName = b(out.Clip(m.SenderName, 1024)), true
	}
	if m.Order != 0 {
		x.HistoryOrder, x.HasHistoryOrder = m.Order, true
	}
	for _, rc := range m.Receipts {
		if rc.DeliveredAt > 0 && (!x.HasDeliveredAt || rc.DeliveredAt > x.DeliveredAt) {
			x.DeliveredAt, x.HasDeliveredAt = rc.DeliveredAt, true
		}
		if rc.ReadAt > 0 && (!x.HasReadAt || rc.ReadAt > x.ReadAt) {
			x.ReadAt, x.HasReadAt = rc.ReadAt, true
		}
	}
	if q := m.Quote; q != nil {
		x.Quoted, x.HasQuoted = abi.Quoted{
			ID:      b(q.ID),
			Sender:  b(q.Sender),
			Summary: b(out.Clip(summary(store.Message{Kind: q.Kind, Text: q.Text}), 1024)),
		}, true
		if cv.isMe(q.Sender) {
			x.Quoted.SenderName, x.Quoted.HasSenderName = b("You"), true
		}
	}
	// A message in a large group can hold more reactions than the bound;
	// the first ones show, and the message stays.
	for _, rc := range m.Reactions[:min(len(m.Reactions), 256)] {
		x.Reactions = append(x.Reactions, abi.Reaction{Sender: b(rc.Sender), FromMe: cv.isMe(rc.Sender), Emoji: b(rc.Emoji)})
	}
	for _, mention := range m.Mentions[:min(len(m.Mentions), mentionsMax)] {
		x.Mentions = append(x.Mentions, abi.MentionRef{User: b(mention.User), ID: b(mention.ID)})
	}
	if m.Media != nil && len(m.Media.Thumbnail) > 0 && !m.Deleted {
		x.Thumbnail, x.HasThumbnail = r.Blob(m.Media.Thumbnail), true
	}
	return x
}

func (cv conv) media(m store.Message) abi.Media {
	x := abi.Media{State: cv.state(m.ChatID, m.ID)}
	if md := m.Media; md != nil {
		x.Mime, x.Size = b(md.MIME), md.Size
		if md.Width > 0 {
			x.Width, x.HasWidth = md.Width, true
		}
		if md.Height > 0 {
			x.Height, x.HasHeight = md.Height, true
		}
		if md.Path != "" {
			x.Path, x.HasPath = b(md.Path), true
		}
	}
	return x
}

func caption(text string) ([]byte, bool) {
	if text == "" {
		return nil, false
	}
	return b(text), true
}

func seconds(m store.Message) (uint32, bool) {
	if m.Media == nil || m.Media.Seconds == 0 {
		return 0, false
	}
	return m.Media.Seconds, true
}

// content is dto.rs content() over the archive's kinds.
func (cv conv) content(m store.Message) abi.Content {
	var c abi.Content
	if m.Deleted || m.Kind == kindDeleted {
		c.Tag = abi.ContentTagRevoked
		return c
	}
	switch m.Kind {
	case kindText:
		c.Tag = abi.ContentTagText
		c.Text.Text = b(m.Text)
		// A preview is decoration: one whose address cannot be carried is
		// left off, and its text is cut, rather than losing the message.
		if p := m.LinkPreview; p != nil && p.URL != "" && len(p.URL) <= 4096 {
			c.Text.Preview, c.Text.HasPreview = abi.LinkPreview{URL: b(p.URL)}, true
			if p.Title != "" {
				c.Text.Preview.Title, c.Text.Preview.HasTitle = b(out.Clip(p.Title, 1024)), true
			}
			if p.Description != "" {
				c.Text.Preview.Description, c.Text.Preview.HasDescription = b(out.Clip(p.Description, 4096)), true
			}
		}
	case kindInteractive:
		c.Tag = abi.ContentTagInteractive
		c.Interactive.Text = b(m.Text)
		if card, ok := interactiveCard(m); ok {
			c.Interactive.Card, c.Interactive.HasCard = card, true
		}
	case store.KindImage:
		c.Tag = abi.ContentTagImage
		c.Image.Caption, c.Image.HasCaption = caption(m.Text)
		c.Image.Media = cv.media(m)
	case store.KindVideo, kindVideoNote:
		c.Tag = abi.ContentTagVideo
		c.Video.Caption, c.Video.HasCaption = caption(m.Text)
		c.Video.Media = cv.media(m)
		c.Video.Seconds, c.Video.HasSeconds = seconds(m)
		c.Video.Gif = m.Media != nil && m.Media.Animated
		c.Video.Note = m.Kind == kindVideoNote
	case kindAudio, kindVoice:
		c.Tag = abi.ContentTagAudio
		c.Audio.Media = cv.media(m)
		c.Audio.Seconds, c.Audio.HasSeconds = seconds(m)
		c.Audio.VoiceNote = m.Kind == kindVoice
		if m.Media != nil {
			c.Audio.Waveform = m.Media.Waveform[:min(len(m.Media.Waveform), 64)]
		}
	case store.KindDocument:
		c.Tag = abi.ContentTagDocument
		c.Document.Media = cv.media(m)
		if m.Media != nil {
			c.Document.FileName = b(m.Media.Name)
		}
		c.Document.Caption, c.Document.HasCaption = caption(m.Text)
	case kindSticker:
		c.Tag = abi.ContentTagSticker
		c.Sticker.Media = cv.media(m)
		c.Sticker.Animated = m.Media != nil && m.Media.Animated
	case kindStickerPack:
		c.Tag = abi.ContentTagStickerPack
		c.StickerPack.Name = b(m.Text)
	case kindLocation:
		c.Tag = abi.ContentTagLocation
		c.Location.Latitude, c.Location.Longitude = m.Latitude, m.Longitude
		name, address := firstLine(m.Text)
		if name != "" {
			c.Location.Name, c.Location.HasName = b(name), true
		}
		if address != "" {
			c.Location.Address, c.Location.HasAddress = b(address), true
		}
	case kindLiveLocation:
		l := m.LiveLocation
		if l == nil || !l.Available {
			c.Tag = abi.ContentTagPhoneOnly
			c.PhoneOnly.LiveLocation = true
			return c
		}
		c.Tag = abi.ContentTagLiveLocation
		x := &c.LiveLocation
		x.Latitude, x.Longitude = m.Latitude, m.Longitude
		if l.AccuracyM != nil {
			x.AccuracyM, x.HasAccuracyM = *l.AccuracyM, true
		}
		if l.SpeedMPS != nil {
			x.SpeedMPS, x.HasSpeedMPS = *l.SpeedMPS, true
		}
		if l.HeadingDeg != nil {
			x.HeadingDeg, x.HasHeadingDeg = *l.HeadingDeg, true
		}
		x.Sequence, x.Ended, x.Updated = l.Sequence, l.Ended, l.UpdatedAt
	case kindContact:
		c.Tag = abi.ContentTagContact
		name, card := firstLine(m.Text)
		c.Contact.DisplayName, c.Contact.Vcard = b(out.Clip(name, 1024)), b(card)
	case kindPoll:
		c.Tag = abi.ContentTagPoll
		c.Poll.Question = b(m.Text)
		c.Poll.State = pollState(m)
		for _, o := range m.Poll[:min(len(m.Poll), 12)] {
			c.Poll.Options = append(c.Poll.Options, b(o.Name))
		}
	case kindViewOnce:
		c.Tag = abi.ContentTagPhoneOnly
		c.PhoneOnly.ViewOnce = true
		switch m.Text {
		case "photo":
			c.PhoneOnly.Once, c.PhoneOnly.HasOnce = abi.OnceMediaPhoto, true
		case "video":
			c.PhoneOnly.Once, c.PhoneOnly.HasOnce = abi.OnceMediaVideo, true
		case "voice":
			c.PhoneOnly.Once, c.PhoneOnly.HasOnce = abi.OnceMediaVoice, true
		case "audio":
			c.PhoneOnly.Once, c.PhoneOnly.HasOnce = abi.OnceMediaAudio, true
		}
	case store.KindCall:
		c.Tag = abi.ContentTagCall
		if call := m.Call; call != nil {
			c.Call = abi.ContentCall{Video: call.Video, Status: callStatus(call.Status), Duration: uint64(call.Seconds), Group: call.Group}
		}
	default:
		c.Tag = abi.ContentTagUnsupported
		c.Unsupported.What = b(out.Clip(m.Text, 1024))
	}
	return c
}

func pollState(m store.Message) abi.PollState {
	s := abi.PollState{Selectable: uint64(max(m.PollSelectable, 0)), HistoryComplete: m.PollHistoryComplete, CanVote: true}
	voters := map[string]bool{}
	for i, o := range m.Poll[:min(len(m.Poll), 12)] {
		s.Counts = append(s.Counts, uint64(max(o.Votes, 0)))
		if o.Selected {
			s.Selected = append(s.Selected, uint64(i))
		}
		for _, v := range o.Voters {
			voters[v] = true
		}
	}
	s.Voters = uint64(len(voters))
	return s
}

func callStatus(s string) abi.CallStatus {
	switch s {
	case "answered-elsewhere":
		return abi.CallStatusAnsweredElsewhere
	case "missed":
		return abi.CallStatusMissed
	case "declined":
		return abi.CallStatusDeclined
	case "declined-elsewhere":
		return abi.CallStatusDeclinedElsewhere
	case "busy":
		return abi.CallStatusBusy
	case "failed":
		return abi.CallStatusFailed
	case "no-answer":
		return abi.CallStatusNoAnswer
	case "connection-lost":
		return abi.CallStatusConnectionLost
	}
	return abi.CallStatusAnswered
}

// interactiveCard is the card of a buttons, list or template message: its
// reply buttons, its links, and a list's rows as one Select button.
func interactiveCard(m store.Message) (abi.InteractiveCard, bool) {
	if len(m.Actions) == 0 {
		return abi.InteractiveCard{}, false
	}
	card := abi.InteractiveCard{Body: b(m.Text)}
	var list abi.InteractiveButton
	for _, a := range m.Actions {
		switch a.Kind {
		case "list":
			if len(list.Action.Select.Options) < 10 {
				list.Action.Select.Options = append(list.Action.Select.Options, abi.InteractiveOption{Section: b(out.Clip(a.Group, 128)), Title: b(out.Clip(a.Label, 128)), Description: b(out.Clip(a.Description, 384))})
			}
			continue
		}
		if len(card.Buttons) == 10 {
			continue
		}
		bt := abi.InteractiveButton{Label: b(out.Clip(a.Label, 128))}
		switch a.Kind {
		case "reply", "template-reply":
			bt.Action.Tag = abi.InteractiveActionTagReply
		case "url":
			if len(a.Value) <= 1024 {
				bt.URL, bt.HasURL = b(a.Value), true
			}
		}
		card.Buttons = append(card.Buttons, bt)
	}
	if len(list.Action.Select.Options) > 0 && len(card.Buttons) < 10 {
		list.Label = b("Select")
		list.Action.Tag = abi.InteractiveActionTagSelect
		card.Buttons = append(card.Buttons, list)
	}
	if len(card.Body) > 16384 {
		card.Body = b(out.Clip(m.Text, 16384))
	}
	return card, true
}

// summary is upstream's Content::summary: the line a chat row and a quote
// show.
func summary(m store.Message) string {
	firstOf := func(s string) string { first, _, _ := strings.Cut(s, "\n"); return first }
	withCaption := func(label string) string {
		if c := firstOf(m.Text); c != "" {
			return label + ": " + c
		}
		return label
	}
	if m.Deleted || m.Kind == kindDeleted {
		return deletedText
	}
	switch m.Kind {
	case kindText, kindInteractive:
		return firstOf(m.Text)
	case store.KindImage:
		return withCaption("Photo")
	case store.KindVideo:
		if m.Media != nil && m.Media.Animated {
			return withCaption("GIF")
		}
		return withCaption("Video")
	case kindVideoNote:
		return withCaption("Video message")
	case kindAudio, kindVoice:
		label := "Audio"
		if m.Kind == kindVoice {
			label = "Voice message"
		}
		if s, ok := seconds(m); ok {
			return fmt.Sprintf("%s (%d:%02d)", label, s/60, s%60)
		}
		return label
	case store.KindDocument:
		name := ""
		if m.Media != nil {
			name = m.Media.Name
		}
		return "Document: " + name
	case kindSticker:
		return "Sticker"
	case kindStickerPack:
		return "Sticker pack: " + m.Text
	case kindLocation:
		if name, _ := firstLine(m.Text); name != "" {
			return "Location: " + name
		}
		return "Location"
	case kindLiveLocation:
		if m.LiveLocation != nil && m.LiveLocation.Ended {
			return "Live location ended"
		}
		return "Live location"
	case kindContact:
		name, _ := firstLine(m.Text)
		return "Contact: " + name
	case kindPoll:
		return "Poll: " + m.Text
	case kindViewOnce:
		switch m.Text {
		case "photo":
			return "View once photo"
		case "video":
			return "View once video"
		case "voice":
			return "View once voice message"
		case "audio":
			return "View once audio"
		}
		return "View once message"
	case store.KindCall:
		video := m.Call != nil && m.Call.Video
		missed := m.Call != nil && m.Call.Status == "missed"
		switch {
		case missed && video:
			return "Missed video call"
		case missed:
			return "Missed voice call"
		case video:
			return "Video call"
		}
		return "Voice call"
	}
	return "Unsupported message (" + m.Text + ")"
}

// recipient is one row of the followed message's receipts.
func recipient(r store.MessageReceipt) abi.Recipient {
	x := abi.Recipient{ID: b(r.Sender), Expected: true}
	if r.DeliveredAt > 0 {
		x.DeliveredAt, x.HasDeliveredAt = r.DeliveredAt, true
	}
	if r.ReadAt > 0 {
		x.ReadAt, x.HasReadAt = r.ReadAt, true
	}
	if r.PlayedAt > 0 {
		x.PlayedAt, x.HasPlayedAt = r.PlayedAt, true
	}
	return x
}

// replyTarget is dto.rs reply_target(): a quote as the command named it,
// for SendRefused to hand back.
func replyTarget(id, chat string, hasChat bool) abi.ReplyTarget {
	t := abi.ReplyTarget{ID: b(id)}
	if hasChat {
		t.Chat, t.HasChat = b(chat), true
	}
	return t
}

// pathResult is dto.rs path_result().
func pathResult(path string, err error) abi.PathResult {
	if err != nil {
		var r abi.PathResult
		r.Tag = abi.PathResultTagErr
		r.Err.Error = b(out.Toast(err.Error()))
		return r
	}
	var r abi.PathResult
	r.Tag = abi.PathResultTagOk
	r.Ok.Path = b(path)
	return r
}

// failedState is a download that failed, for a Media's state.
func failedState(err error) abi.MediaState {
	var s abi.MediaState
	s.Tag = abi.MediaStateTagFailed
	s.Failed.Error = b(out.Clip(err.Error(), 1024))
	return s
}
