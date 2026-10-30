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
	isMe   func(id string) bool
	state  func(chat, id string) abi.MediaState
	motion func(chat, id string) abi.MediaState
	// refreshing reports whether a chat's history is being asked for, which
	// a poll's refresh waits on.
	refreshing func(chat string) bool
	// cardState is a carousel card's picture's.
	cardState func(chat, id string, card int) abi.MediaState
	// person is the phone-number id of a privacy id WhatsApp has mapped,
	// else id: a mention names a contact by it.
	person func(id string) string
}

func (cv conv) mention(m store.Mention) abi.MentionRef {
	id := m.ID
	if cv.person != nil {
		id = cv.person(id)
	}
	return abi.MentionRef{User: b(m.User), ID: b(id)}
}

func (cv conv) motionState(chat, id string) abi.MediaState {
	if cv.motion == nil {
		return abi.MediaState{Tag: abi.MediaStateTagIdle}
	}
	return cv.motion(chat, id)
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
	if kind == abi.ChatKindGroup && c.Description != nil {
		x.GroupDescription, x.HasGroupDescription = b(out.Clip(*c.Description, 8192)), true
	}
	if c.Sound != nil {
		x.NotificationSound, x.HasNotificationSound = soundToABI(*c.Sound)
	}
	return x
}

// soundToABI is a chat's stored sound as the schema's; a custom sound
// whose path cannot be carried is left off.
func soundToABI(s store.Sound) (abi.NotificationSound, bool) {
	var n abi.NotificationSound
	switch s.Kind {
	case "receive":
		n.Tag = abi.NotificationSoundTagReceive
	case "alert":
		n.Tag = abi.NotificationSoundTagAlert
	case "system":
		n.Tag = abi.NotificationSoundTagSystem
	case "none":
		n.Tag = abi.NotificationSoundTagNone
	case "custom":
		if s.Path == "" || len(s.Path) > 4096 {
			return n, false
		}
		n.Tag = abi.NotificationSoundTagCustom
		n.Custom.Path = b(s.Path)
	default:
		return n, false
	}
	return n, true
}

// soundFromABI is SetChatSound's sound as the archive keeps it.
func soundFromABI(n abi.NotificationSound) store.Sound {
	switch n.Tag {
	case abi.NotificationSoundTagAlert:
		return store.Sound{Kind: "alert"}
	case abi.NotificationSoundTagSystem:
		return store.Sound{Kind: "system"}
	case abi.NotificationSoundTagNone:
		return store.Sound{Kind: "none"}
	case abi.NotificationSoundTagCustom:
		return store.Sound{Kind: "custom", Path: string(n.Custom.Path)}
	}
	return store.Sound{Kind: "receive"}
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
		Content:   cv.content(r, m),
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
		switch {
		case cv.isMe(q.Sender):
			x.Quoted.SenderName, x.Quoted.HasSenderName = b("You"), true
		case q.SenderName != "":
			x.Quoted.SenderName, x.Quoted.HasSenderName = b(out.Clip(q.SenderName, 1024)), true
		}
		if q.Chat != "" && q.Chat != m.ChatID {
			x.Quoted.Chat, x.Quoted.HasChat = b(q.Chat), true
		}
		for _, mention := range q.Mentions[:min(len(q.Mentions), mentionsMax)] {
			x.Quoted.Mentions = append(x.Quoted.Mentions, cv.mention(mention))
		}
	}
	// A message in a large group can hold more reactions than the bound;
	// the first ones show, and the message stays.
	for _, rc := range m.Reactions[:min(len(m.Reactions), 256)] {
		x.Reactions = append(x.Reactions, abi.Reaction{Sender: b(rc.Sender), FromMe: cv.isMe(rc.Sender), Emoji: b(rc.Emoji)})
	}
	for _, mention := range m.Mentions[:min(len(m.Mentions), mentionsMax)] {
		x.Mentions = append(x.Mentions, cv.mention(mention))
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

// content is dto.rs content() over the archive's kinds; a card's
// thumbnails become event blobs of r.
func (cv conv) content(r *out.Record, m store.Message) abi.Content {
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
		if card, ok := cv.interactiveCard(r, m); ok {
			c.Interactive.Card, c.Interactive.HasCard = card, true
		}
	case store.KindImage:
		c.Tag = abi.ContentTagImage
		c.Image.Caption, c.Image.HasCaption = caption(m.Text)
		c.Image.Media = cv.media(m)
		if mo := m.Motion; mo != nil {
			c.Image.HasMotion = true
			c.Image.Motion.State = cv.motionState(m.ChatID, m.ID)
			if mo.Path != "" {
				c.Image.Motion.Path, c.Image.Motion.HasPath = b(mo.Path), true
			}
		}
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
		c.Poll.State = cv.pollState(m)
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
	case kindWaiting:
		c.Tag = abi.ContentTagWaiting
	case kindPhoneOnly:
		c.Tag = abi.ContentTagPhoneOnly
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

// pollState is a poll's counts and who voted for what (each voter once,
// newest vote first, as the votes were counted).
func (cv conv) pollState(m store.Message) abi.PollState {
	s := abi.PollState{Selectable: uint64(max(m.PollSelectable, 0)), HistoryComplete: m.PollHistoryComplete, CanVote: true, RefreshNeeded: !m.PollHistoryComplete && !m.FromMe}
	if cv.refreshing != nil {
		s.Refreshing = cv.refreshing(m.ChatID)
	}
	type voter struct {
		at      int64
		choices []uint64
	}
	voters := map[string]*voter{}
	var order []string
	for i, o := range m.Poll[:min(len(m.Poll), 12)] {
		s.Counts = append(s.Counts, uint64(max(o.Votes, 0)))
		if o.Selected {
			s.Selected = append(s.Selected, uint64(i))
		}
		for k, v := range o.Voters {
			x := voters[v]
			if x == nil {
				x = &voter{}
				voters[v] = x
				order = append(order, v)
			}
			if k < len(o.VoterTimes) {
				x.at = max(x.at, o.VoterTimes[k])
			}
			x.choices = append(x.choices, uint64(i))
		}
	}
	s.Voters = uint64(len(voters))
	for _, id := range order[:min(len(order), 256)] {
		v := voters[id]
		s.Votes = append(s.Votes, abi.PollVoter{ID: b(id), FromMe: cv.isMe(id), Timestamp: v.at, Choices: v.choices})
	}
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

// interactiveCard is the card of a buttons, list, template or native-flow
// message: its reply buttons, its links and copy codes, a list's rows as
// one Select button, its header picture, and a carousel's cards.
func (cv conv) interactiveCard(r *out.Record, m store.Message) (abi.InteractiveCard, bool) {
	if len(m.Actions) == 0 && len(m.Cards) == 0 && m.Media == nil {
		return abi.InteractiveCard{}, false
	}
	card := abi.InteractiveCard{Body: b(out.Clip(m.Text, 16384)), Buttons: cardButtons(m.Actions, 10)}
	if m.Media != nil {
		card.Image, card.HasImage = cv.media(m), true
		if len(m.Media.Thumbnail) > 0 && r != nil {
			card.Thumbnail, card.HasThumbnail = r.Blob(m.Media.Thumbnail), true
		}
	}
	for i, c := range m.Cards[:min(len(m.Cards), 10)] {
		x := abi.CarouselCard{Body: b(out.Clip(strings.TrimSpace(c.Title+"\n"+c.Text), 2048)), Buttons: cardButtons(c.Actions, 2)}
		if c.Media != nil {
			x.Image, x.HasImage = cv.cardMedia(m, i, c.Media), true
			if len(c.Media.Thumbnail) > 0 && r != nil {
				x.Thumbnail, x.HasThumbnail = r.Blob(c.Media.Thumbnail), true
			}
		}
		// A card with no button this app can carry out still opens on
		// the phone.
		x.NeedsPhone = len(c.Actions) > 0 && len(x.Buttons) == 0
		card.Carousel = append(card.Carousel, x)
	}
	card.NeedsPhone = len(card.Buttons) == 0 && len(card.Carousel) == 0 && len(m.Actions) > 0
	return card, true
}

// cardMedia is a carousel card's picture, with its own download's state.
func (cv conv) cardMedia(m store.Message, card int, md *store.Media) abi.Media {
	x := abi.Media{Mime: b(md.MIME), Size: md.Size, State: abi.MediaState{Tag: abi.MediaStateTagIdle}}
	if cv.cardState != nil {
		x.State = cv.cardState(m.ChatID, m.ID, card)
	}
	if md.Width > 0 {
		x.Width, x.HasWidth = md.Width, true
	}
	if md.Height > 0 {
		x.Height, x.HasHeight = md.Height, true
	}
	if md.Path != "" {
		x.Path, x.HasPath = b(md.Path), true
	}
	return x
}

// cardButtons are actions as at most limit buttons: replies, links and
// copy codes in order, then a list's rows as one Select button.
func cardButtons(actions []store.InteractiveAction, limit int) []abi.InteractiveButton {
	var buttons []abi.InteractiveButton
	var list abi.InteractiveButton
	for _, a := range actions {
		switch a.Kind {
		case "list", "native-list":
			if len(list.Action.Select.Options) < 10 {
				list.Action.Select.Options = append(list.Action.Select.Options, abi.InteractiveOption{Section: b(out.Clip(a.Group, 128)), Title: b(out.Clip(a.Label, 128)), Description: b(out.Clip(a.Description, 384))})
			}
			continue
		}
		if len(buttons) == limit {
			continue
		}
		bt := abi.InteractiveButton{Label: b(out.Clip(a.Label, 128))}
		switch a.Kind {
		case "reply", "template-reply", "native-reply":
			bt.Action.Tag = abi.InteractiveActionTagReply
		case "url":
			if len(a.Value) > 1024 {
				continue
			}
			bt.URL, bt.HasURL = b(a.Value), true
		case "copy":
			if len(a.Value) > 1024 {
				continue
			}
			bt.Action.Tag = abi.InteractiveActionTagCopy
			bt.Action.Copy.Code = b(a.Value)
		}
		buttons = append(buttons, bt)
	}
	if len(list.Action.Select.Options) > 0 && len(buttons) < limit {
		list.Label = b("Select")
		list.Action.Tag = abi.InteractiveActionTagSelect
		buttons = append(buttons, list)
	}
	return buttons
}

// replyIndex is the action a pressed button (and a Select's choice) stands
// for, as cardButtons laid them out: its Index in the original message.
func replyIndex(actions []store.InteractiveAction, button int, choice int) (store.InteractiveAction, bool) {
	var buttons []store.InteractiveAction
	var list []store.InteractiveAction
	for _, a := range actions {
		switch a.Kind {
		case "list", "native-list":
			if len(list) < 10 {
				list = append(list, a)
			}
			continue
		}
		if len(buttons) == 10 || (a.Kind == "url" || a.Kind == "copy") && len(a.Value) > 1024 {
			continue
		}
		buttons = append(buttons, a)
	}
	if button < len(buttons) {
		return buttons[button], choice < 0
	}
	if button == len(buttons) && len(list) > 0 && choice >= 0 && choice < len(list) {
		return list[choice], true
	}
	return store.InteractiveAction{}, false
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
	case kindText:
		return firstOf(m.Text)
	case kindInteractive:
		if t := firstOf(m.Text); t != "" {
			return t
		}
		if len(m.Cards) > 0 {
			return firstOf(strings.TrimSpace(m.Cards[0].Title + "\n" + m.Cards[0].Text))
		}
		return "Interactive message"
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
	case kindWaiting:
		return "Waiting for this message"
	case kindPhoneOnly:
		return "This message can only be seen on your phone"
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
