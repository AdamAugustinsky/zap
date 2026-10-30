// From zapfast-go 3104c46 (internal/backend/parse.go, live_location.go), MIT.
// Changed for Zap: the archive's store.Message; mentions and link previews
// are kept; a location keeps its name and address, and a contact its name
// and vCard, apart (first line, then the rest), so convert.go can give them
// back to the abi fields.

package wa

import (
	"encoding/json"
	"math"
	"net/url"
	"strings"
	"unicode"

	"go.mau.fi/whatsmeow/proto/waE2E"
	"go.mau.fi/whatsmeow/proto/waWeb"
	"go.mau.fi/whatsmeow/types"
	"go.mau.fi/whatsmeow/types/events"
	"google.golang.org/protobuf/proto"

	"zapfast/gocore/store"
)

// Kinds a parsed message may have besides the archive's indexed ones
// (store.KindImage and the rest). Reactions, poll votes and protocol
// messages change another message and are never stored as rows.
const (
	kindText         = "text"
	kindVideoNote    = "video-note"
	kindAudio        = "audio"
	kindVoice        = "voice"
	kindSticker      = "sticker"
	kindStickerPack  = "sticker-pack"
	kindLocation     = "location"
	kindLiveLocation = "live-location"
	kindContact      = "contact"
	kindPoll         = "poll"
	kindInteractive  = "interactive"
	kindReaction     = "reaction"
	kindPollUpdate   = "poll-update"
	kindProtocol     = "protocol"
	kindDeleted      = "deleted"
	kindPin          = "pin"
	kindViewOnce     = "view-once"
	kindUnsupported  = "unsupported"
	kindPhoneOnly    = "phone-only"
	// kindWaiting is a message this device could not decrypt yet.
	kindWaiting = "waiting"
)

// deletedText is what a revoked message's row says.
const deletedText = "This message was deleted"

// thumbnailMax is the largest inline thumbnail kept: a bigger one is a
// sender's mistake or an attack on the reader's memory.
const thumbnailMax = 512 << 10

func unwrap(p *waE2E.Message) *waE2E.Message {
	for i := 0; i < 12 && p != nil; i++ {
		var next *waE2E.Message
		switch {
		case p.GetDeviceSentMessage() != nil:
			next = p.GetDeviceSentMessage().GetMessage()
		case p.GetEphemeralMessage() != nil:
			next = p.GetEphemeralMessage().GetMessage()
		case p.GetDocumentWithCaptionMessage() != nil:
			next = p.GetDocumentWithCaptionMessage().GetMessage()
		case p.GetEditedMessage() != nil:
			next = p.GetEditedMessage().GetMessage()
		case p.GetBotInvokeMessage() != nil:
			next = p.GetBotInvokeMessage().GetMessage()
		}
		if next == nil {
			return p
		}
		p = next
	}
	return p
}

// signalling reports whether p, recognised as no content, only carries
// protocol state: group sender keys travel in a message of their own
// beside the one they unlock, and nothing in such a message is shown
// (zf:src/backend/worker.rs:11119).
func signalling(p *waE2E.Message) bool {
	if p.GetSenderKeyDistributionMessage() != nil || p.GetFastRatchetKeySenderKeyDistributionMessage() != nil ||
		p.GetEncReactionMessage() != nil || p.GetEncCommentMessage() != nil || p.GetEncEventResponseMessage() != nil ||
		p.GetKeepInChatMessage() != nil || p.GetStickerSyncRmrMessage() != nil || p.GetSecretEncryptedMessage() != nil ||
		p.GetMessageHistoryBundle() != nil || p.GetMessageHistoryNotice() != nil || p.GetBotInvokeMessage() != nil ||
		p.GetGroupRootKeyShare() != nil || p.GetRootSecretDistributeMessage() != nil || p.GetPollAddOptionMessage() != nil ||
		p.GetBotTaskMessage() != nil || p.GetStatusNotificationMessage() != nil || p.GetAlbumMessage() != nil {
		return true
	}
	// Most messages carry context info (secrets, bot metadata): alone it
	// marks nothing.
	bare := proto.Clone(p).(*waE2E.Message)
	bare.MessageContextInfo = nil
	return proto.Size(bare) == 0
}

// stub is what a history entry with no message, only a stub type (a
// system notice: "messages are end-to-end encrypted", a security code that
// changed, a member who joined), files as: a missed call is a call, as the
// phone lists it; any other notice is no message, as the phone mostly
// hides them (kindProtocol: not filed).
func stub(t waWeb.WebMessageInfo_StubType, m *store.Message) {
	switch t {
	case waWeb.WebMessageInfo_CALL_MISSED_VOICE, waWeb.WebMessageInfo_CALL_MISSED_VIDEO,
		waWeb.WebMessageInfo_CALL_MISSED_GROUP_VOICE, waWeb.WebMessageInfo_CALL_MISSED_GROUP_VIDEO:
		m.Kind = store.KindCall
		m.Call = &store.Call{
			Status: "missed",
			Video:  t == waWeb.WebMessageInfo_CALL_MISSED_VIDEO || t == waWeb.WebMessageInfo_CALL_MISSED_GROUP_VIDEO,
			Group:  t == waWeb.WebMessageInfo_CALL_MISSED_GROUP_VOICE || t == waWeb.WebMessageInfo_CALL_MISSED_GROUP_VIDEO,
		}
	default:
		m.Kind = kindProtocol
	}
}

// hdCopy reports whether p is the HD copy of a photo or video, which
// WhatsApp sends after the standard one as its associated child.
func hdCopy(p *waE2E.Message) bool {
	switch p.GetMessageContextInfo().GetMessageAssociation().GetAssociationType() {
	case waE2E.MessageAssociation_HD_IMAGE_DUAL_UPLOAD, waE2E.MessageAssociation_HD_VIDEO_DUAL_UPLOAD:
		return p.GetAssociatedChildMessage() != nil
	}
	return false
}

// unsupportedWhat names what an unshown message is, for its row.
func unsupportedWhat(p *waE2E.Message) string {
	switch {
	case p.GetGroupInviteMessage() != nil:
		return "group invite"
	case p.GetEventMessage() != nil:
		return "event"
	case p.GetProductMessage() != nil, p.GetOrderMessage() != nil:
		return "product"
	case p.GetSendPaymentMessage() != nil, p.GetRequestPaymentMessage() != nil, p.GetPaymentInviteMessage() != nil, p.GetInvoiceMessage() != nil:
		return "payment"
	case p.GetScheduledCallCreationMessage() != nil:
		return "call"
	case p.GetLottieStickerMessage() != nil:
		return "animated sticker"
	}
	return "message"
}

// botUnreadable is what a Meta AI reply with no readable part is called in
// its placeholder (zf:src/backend/worker/bot_replies.rs:14).
const botUnreadable = "Meta AI reply"

// botReply is a Meta AI reply as the text it reads as: its text, code (in a
// WhatsApp monospace block), table, formula and image-text parts, or a
// placeholder when it has none (zf:src/backend/worker/bot_replies.rs:26).
func botReply(reply *waE2E.AIRichResponseMessage) (kind, text string) {
	var parts []string
	for _, sub := range reply.GetSubmessages() {
		var pieces []string
		if t := sub.GetMessageText(); strings.TrimSpace(t) != "" {
			pieces = append(pieces, strings.TrimRightFunc(t, unicode.IsSpace))
		}
		if code := sub.GetCodeMetadata(); code != nil {
			var body strings.Builder
			for _, block := range code.GetCodeBlocks() {
				body.WriteString(block.GetCodeContent())
			}
			if b := strings.Trim(body.String(), "\n"); strings.TrimSpace(b) != "" {
				pieces = append(pieces, "```\n"+b+"\n```")
			}
		}
		if table := sub.GetTableMetadata(); table != nil {
			var rows []string
			if title := table.GetTitle(); strings.TrimSpace(title) != "" {
				rows = append(rows, title)
			}
			for _, row := range table.GetRows() {
				if len(row.GetItems()) > 0 {
					rows = append(rows, strings.Join(row.GetItems(), " | "))
				}
			}
			if len(rows) > 0 {
				pieces = append(pieces, strings.Join(rows, "\n"))
			}
		}
		if t := sub.GetLatexMetadata().GetText(); strings.TrimSpace(t) != "" {
			pieces = append(pieces, strings.TrimRightFunc(t, unicode.IsSpace))
		}
		if t := sub.GetImageMetadata().GetImageText(); strings.TrimSpace(t) != "" {
			pieces = append(pieces, strings.TrimRightFunc(t, unicode.IsSpace))
		}
		if len(pieces) > 0 {
			parts = append(parts, strings.Join(pieces, "\n\n"))
		}
	}
	if len(parts) == 0 {
		return kindUnsupported, botUnreadable
	}
	return kindText, strings.Join(parts, "\n\n")
}

// callLogEntry is the call a call-log message files in its chat
// (zf:src/backend/worker/calls.rs:1054).
func callLogEntry(log *waE2E.CallLogMessage) *store.Call {
	status := "missed" // missed, or silenced: nobody answered
	switch log.GetCallOutcome() {
	case waE2E.CallLogMessage_CONNECTED, waE2E.CallLogMessage_ONGOING:
		status = "answered"
	case waE2E.CallLogMessage_REJECTED:
		status = "declined"
	case waE2E.CallLogMessage_ACCEPTED_ELSEWHERE:
		status = "answered-elsewhere"
	case waE2E.CallLogMessage_FAILED:
		status = "failed"
	}
	return &store.Call{Video: log.GetIsVideo(), Status: status, Seconds: uint32(min(max(log.GetDurationSecs(), 0), math.MaxUint32)), Group: len(log.GetParticipants()) > 1}
}

// parseMessage turns a whatsmeow message into the archive's row for chat
// and sender (canonical ids). The original protobuf goes in Raw, which
// quotes, forwards and downloads read back; a view-once message keeps none.
func parseMessage(e *events.Message, chat, sender string) store.Message {
	return parseDepth(e, chat, sender, 0)
}

func parseDepth(e *events.Message, chat, sender string, depth int) store.Message {
	m := store.Message{ID: e.Info.ID, ChatID: chat, Sender: sender, SenderName: e.Info.PushName, FromMe: e.Info.IsFromMe, Timestamp: e.Info.Timestamp.Unix(), Kind: kindUnsupported, Status: "delivered", Edited: e.IsEdit}
	p := unwrap(e.Message)
	if p == nil {
		if depth == 0 && e.Message == nil {
			stub(e.SourceWebMsg.GetMessageStubType(), &m)
		}
		return m
	}
	if hdCopy(p) {
		// The HD copy of a photo or video sent before it: the phone shows
		// the one picture.
		m.Kind = kindProtocol
		return m
	}
	if e.SourceWebMsg != nil {
		switch e.SourceWebMsg.GetStatus().String() {
		case "PENDING":
			m.Status = "pending"
		case "SERVER_ACK":
			m.Status = "sent"
		case "DELIVERY_ACK":
			m.Status = "delivered"
		case "READ":
			m.Status = "read"
		case "PLAYED":
			m.Status = "played"
		case "ERROR":
			m.Status = "failed"
		}
	}
	// Business messages come wrapped as view-once ones: their cards are
	// not once-media.
	if inner := viewOnceInner(p); inner != nil && isInteractive(inner) {
		p = inner
	}
	if raw, err := proto.Marshal(p); err == nil {
		m.Raw = raw
	}
	var ctx *waE2E.ContextInfo
	if !isInteractive(p) && (e.IsViewOnce || viewOnceInner(p) != nil) {
		m.Kind = kindViewOnce
		m.Text = viewOnceKind(p)
		m.Raw = nil
		return m
	}
	switch {
	case p.GetConversation() != "":
		m.Kind = kindText
		m.Text = p.GetConversation()
	case p.GetExtendedTextMessage() != nil:
		x := p.GetExtendedTextMessage()
		m.Kind = kindText
		m.Text = x.GetText()
		ctx = x.GetContextInfo()
		if link := x.GetMatchedText(); link != "" {
			m.LinkPreview = &store.LinkPreview{URL: link, Title: x.GetTitle(), Description: x.GetDescription()}
		}
	case p.GetImageMessage() != nil:
		x := p.GetImageMessage()
		m.Kind = store.KindImage
		m.Text = x.GetCaption()
		ctx = x.GetContextInfo()
		m.Media = &store.Media{MIME: x.GetMimetype(), Size: x.GetFileLength(), Width: x.GetWidth(), Height: x.GetHeight(), Thumbnail: x.GetJPEGThumbnail()}
	case p.GetVideoMessage() != nil:
		x := p.GetVideoMessage()
		m.Kind = store.KindVideo
		m.Text = x.GetCaption()
		ctx = x.GetContextInfo()
		m.Media = &store.Media{MIME: x.GetMimetype(), Size: x.GetFileLength(), Width: x.GetWidth(), Height: x.GetHeight(), Seconds: x.GetSeconds(), Thumbnail: x.GetJPEGThumbnail(), Animated: x.GetGifPlayback()}
	case p.GetPtvMessage() != nil:
		x := p.GetPtvMessage()
		m.Kind = kindVideoNote
		ctx = x.GetContextInfo()
		m.Media = &store.Media{MIME: x.GetMimetype(), Size: x.GetFileLength(), Width: x.GetWidth(), Height: x.GetHeight(), Seconds: x.GetSeconds(), Thumbnail: x.GetJPEGThumbnail()}
	case p.GetAudioMessage() != nil:
		x := p.GetAudioMessage()
		m.Kind = kindAudio
		ctx = x.GetContextInfo()
		if x.GetPTT() {
			m.Kind = kindVoice
		}
		m.Media = &store.Media{MIME: x.GetMimetype(), Size: x.GetFileLength(), Seconds: x.GetSeconds(), PTT: x.GetPTT(), Waveform: x.GetWaveform()}
	case p.GetDocumentMessage() != nil:
		x := p.GetDocumentMessage()
		m.Kind = store.KindDocument
		m.Text = x.GetCaption()
		ctx = x.GetContextInfo()
		m.Media = &store.Media{MIME: x.GetMimetype(), Name: x.GetFileName(), Size: x.GetFileLength(), Thumbnail: x.GetJPEGThumbnail()}
	case p.GetStickerPackMessage() != nil:
		x := p.GetStickerPackMessage()
		m.Kind = kindStickerPack
		m.Text = x.GetName()
		m.Media = &store.Media{MIME: "application/zip", Name: x.GetName() + ".wastickers", Size: x.GetFileLength()}
		ctx = x.GetContextInfo()
	case p.GetStickerMessage() != nil:
		x := p.GetStickerMessage()
		m.Kind = kindSticker
		ctx = x.GetContextInfo()
		m.Media = &store.Media{MIME: x.GetMimetype(), Size: x.GetFileLength(), Width: x.GetWidth(), Height: x.GetHeight(), Animated: x.GetIsAnimated()}
	case p.GetLocationMessage() != nil:
		x := p.GetLocationMessage()
		m.Kind = kindLocation
		m.Text = x.GetName() + "\n" + x.GetAddress()
		m.Latitude = x.GetDegreesLatitude()
		m.Longitude = x.GetDegreesLongitude()
		ctx = x.GetContextInfo()
		if x.GetIsLive() {
			m.Kind = kindLiveLocation
			m.Text = ""
			m.LiveLocation = &store.LiveLocation{UpdatedAt: m.Timestamp, Available: x.DegreesLatitude != nil && x.DegreesLongitude != nil, AccuracyM: clonePointer(x.AccuracyInMeters), SpeedMPS: clonePointer(x.SpeedInMps), HeadingDeg: clonePointer(x.DegreesClockwiseFromMagneticNorth)}
		}
	case p.GetLiveLocationMessage() != nil:
		x := p.GetLiveLocationMessage()
		parseLiveLocation(&m, x)
		ctx = x.GetContextInfo()
	case p.GetContactMessage() != nil:
		x := p.GetContactMessage()
		m.Kind = kindContact
		m.Text = x.GetDisplayName() + "\n" + x.GetVcard()
		ctx = x.GetContextInfo()
	case p.GetContactsArrayMessage() != nil:
		x := p.GetContactsArrayMessage()
		m.Kind = kindContact
		m.Text = x.GetDisplayName() + "\n"
		for _, c := range x.GetContacts() {
			m.Text += c.GetVcard() + "\n"
		}
		ctx = x.GetContextInfo()
	case p.GetPollCreationMessage() != nil:
		parsePoll(&m, p.GetPollCreationMessage())
		ctx = p.GetPollCreationMessage().GetContextInfo()
	case p.GetPollCreationMessageV2() != nil:
		parsePoll(&m, p.GetPollCreationMessageV2())
		ctx = p.GetPollCreationMessageV2().GetContextInfo()
	case p.GetPollCreationMessageV3() != nil:
		parsePoll(&m, p.GetPollCreationMessageV3())
		ctx = p.GetPollCreationMessageV3().GetContextInfo()
	case p.GetButtonsMessage() != nil:
		x := p.GetButtonsMessage()
		m.Kind = kindInteractive
		m.Text = x.GetContentText()
		ctx = x.GetContextInfo()
		for i, a := range x.GetButtons() {
			if a.GetType() == waE2E.ButtonsMessage_Button_RESPONSE {
				m.Actions = append(m.Actions, store.InteractiveAction{Label: a.GetButtonText().GetDisplayText(), Kind: "reply", Index: i})
			}
		}
	case p.GetListMessage() != nil:
		x := p.GetListMessage()
		m.Kind = kindInteractive
		m.Text = x.GetTitle() + "\n" + x.GetDescription()
		ctx = x.GetContextInfo()
		index := 0
		for _, section := range x.GetSections() {
			for _, row := range section.GetRows() {
				m.Actions = append(m.Actions, store.InteractiveAction{Label: row.GetTitle(), Kind: "list", Index: index, Group: section.GetTitle(), Description: row.GetDescription()})
				index++
			}
		}
	case p.GetTemplateMessage() != nil:
		x := p.GetTemplateMessage()
		m.Kind = kindInteractive
		ctx = x.GetContextInfo()
		h := x.GetHydratedTemplate()
		if h == nil {
			h = x.GetHydratedFourRowTemplate()
		}
		if h != nil {
			m.Text = h.GetHydratedContentText()
			for i, b := range h.GetHydratedButtons() {
				switch {
				case b.GetQuickReplyButton() != nil:
					m.Actions = append(m.Actions, store.InteractiveAction{Label: b.GetQuickReplyButton().GetDisplayText(), Kind: "template-reply", Index: i})
				case b.GetUrlButton() != nil:
					m.Actions = append(m.Actions, store.InteractiveAction{Label: b.GetUrlButton().GetDisplayText(), Kind: "url", Value: b.GetUrlButton().GetURL(), Index: i})
				case b.GetCallButton() != nil:
					m.Actions = append(m.Actions, store.InteractiveAction{Label: b.GetCallButton().GetDisplayText(), Kind: "phone", Value: b.GetCallButton().GetPhoneNumber(), Index: i})
				}
			}
		}
	case p.GetInteractiveMessage() != nil:
		x := p.GetInteractiveMessage()
		parseInteractive(&m, x)
		ctx = x.GetContextInfo()
	case p.GetButtonsResponseMessage() != nil:
		x := p.GetButtonsResponseMessage()
		m.Kind, m.Text = kindText, x.GetSelectedDisplayText()
		ctx = x.GetContextInfo()
	case p.GetListResponseMessage() != nil:
		x := p.GetListResponseMessage()
		m.Kind, m.Text = kindText, x.GetTitle()
		ctx = x.GetContextInfo()
	case p.GetTemplateButtonReplyMessage() != nil:
		x := p.GetTemplateButtonReplyMessage()
		m.Kind, m.Text = kindText, x.GetSelectedDisplayText()
		ctx = x.GetContextInfo()
	case p.GetInteractiveResponseMessage() != nil:
		x := p.GetInteractiveResponseMessage()
		m.Kind, m.Text = kindText, x.GetBody().GetText()
		ctx = x.GetContextInfo()
	case p.GetRichResponseMessage() != nil:
		m.Kind, m.Text = botReply(p.GetRichResponseMessage())
		ctx = p.GetRichResponseMessage().GetContextInfo()
	case p.GetCallLogMesssage() != nil:
		m.Kind = store.KindCall
		m.Call = callLogEntry(p.GetCallLogMesssage())
	case p.GetPinInChatMessage() != nil:
		m.Kind = kindPin
	case p.GetReactionMessage() != nil:
		m.Kind = kindReaction
		m.Text = p.GetReactionMessage().GetText()
	case p.GetPollUpdateMessage() != nil:
		m.Kind = kindPollUpdate
	case p.GetProtocolMessage() != nil:
		x := p.GetProtocolMessage()
		m.Kind = kindProtocol
		if x.GetType() == waE2E.ProtocolMessage_REVOKE {
			m.ID = x.GetKey().GetID()
			m.Deleted = true
			m.Kind = kindDeleted
			m.Text = deletedText
		}
		if x.GetType() == waE2E.ProtocolMessage_MESSAGE_EDIT && x.GetEditedMessage() != nil {
			copy := *e
			copy.Message = x.GetEditedMessage()
			copy.IsEdit = true
			if depth >= 8 {
				return m
			}
			m = parseDepth(&copy, chat, sender, depth+1)
			m.ID = x.GetKey().GetID()
			m.Edited = true
		}
	default:
		switch {
		case e.Info.MediaType == "livelocation":
			m.Text = "message"
			m.Kind = kindLiveLocation
			m.LiveLocation = &store.LiveLocation{UpdatedAt: m.Timestamp}
		case p.GetPlaceholderMessage() != nil:
			// The phone masks some messages from linked devices and sends
			// this stand-in so the chat still shows them.
			m.Kind = kindPhoneOnly
		case signalling(p):
			m.Kind = kindProtocol
		default:
			m.Text = unsupportedWhat(p)
		}
	}
	if m.LiveLocation != nil && e.SourceWebMsg != nil && e.SourceWebMsg.GetFinalLiveLocation() != nil {
		final := e.SourceWebMsg.GetFinalLiveLocation()
		parseLiveLocation(&m, final)
		m.LiveLocation.Ended = true
		m.LiveLocation.UpdatedAt = m.Timestamp + int64(final.GetTimeOffset())
	}
	if ctx != nil {
		m.Forwarded = ctx.GetIsForwarded() || ctx.GetForwardingScore() > 0
		if ctx.GetStanzaID() != "" {
			q := &events.Message{Message: ctx.GetQuotedMessage()}
			quoted := store.Message{Text: "", Kind: kindUnsupported}
			if depth < 8 {
				quoted = parseDepth(q, chat, ctx.GetParticipant(), depth+1)
			}
			m.Quote = &store.Quote{ID: ctx.GetStanzaID(), Sender: ctx.GetParticipant(), Text: quoted.Text, Kind: quoted.Kind, Mentions: quoted.Mentions}
			if remote := ctx.GetRemoteJID(); remote != "" {
				if j, err := types.ParseJID(remote); err == nil && j.ToNonAD().String() != chat {
					m.Quote.Chat = j.ToNonAD().String()
				}
			}
		}
		for _, jid := range ctx.GetMentionedJID() {
			if len(m.Mentions) == mentionsMax {
				break
			}
			m.Mentions = append(m.Mentions, mentionOf(jid))
		}
	}
	// Avoid a malicious inline thumbnail causing unbounded memory.
	if m.Media != nil && len(m.Media.Thumbnail) > thumbnailMax {
		m.Media.Thumbnail = nil
	}
	return m
}

// mentionsMax is how many mentions a message or a command carries
// (dto.rs MENTIONS).
const mentionsMax = 256

// mentionOf is the stored mention of a JID: the user part is the visible
// @token.
func mentionOf(jid string) store.Mention {
	j, err := types.ParseJID(jid)
	if err != nil {
		return store.Mention{User: jid, ID: jid}
	}
	return store.Mention{User: j.User, ID: j.ToNonAD().String()}
}

// viewOnceInner is what a view-once wrapper holds, nil for none.
func viewOnceInner(p *waE2E.Message) *waE2E.Message {
	switch {
	case p.GetViewOnceMessage() != nil:
		return p.GetViewOnceMessage().GetMessage()
	case p.GetViewOnceMessageV2() != nil:
		return p.GetViewOnceMessageV2().GetMessage()
	case p.GetViewOnceMessageV2Extension() != nil:
		return p.GetViewOnceMessageV2Extension().GetMessage()
	}
	return nil
}

func isInteractive(p *waE2E.Message) bool {
	return p.GetInteractiveMessage() != nil || p.GetButtonsMessage() != nil || p.GetListMessage() != nil || p.GetTemplateMessage() != nil
}

// viewOnceKind records which once-media a view-once message held, for
// PhoneOnly's `once`.
func viewOnceKind(p *waE2E.Message) string {
	inner := p.GetViewOnceMessage().GetMessage()
	if inner == nil {
		inner = p.GetViewOnceMessageV2().GetMessage()
	}
	if inner == nil {
		inner = p.GetViewOnceMessageV2Extension().GetMessage()
	}
	if inner == nil {
		inner = p
	}
	switch {
	case inner.GetImageMessage() != nil:
		return "photo"
	case inner.GetVideoMessage() != nil:
		return "video"
	case inner.GetAudioMessage() != nil && inner.GetAudioMessage().GetPTT():
		return "voice"
	case inner.GetAudioMessage() != nil:
		return "audio"
	}
	return ""
}

func parsePoll(m *store.Message, p *waE2E.PollCreationMessage) {
	m.Kind = kindPoll
	m.Text = p.GetName()
	m.PollSelectable = int(p.GetSelectableOptionsCount())
	if m.PollSelectable == 0 {
		m.PollSelectable = len(p.GetOptions())
	}
	for _, option := range p.GetOptions() {
		m.Poll = append(m.Poll, store.PollOption{Name: option.GetOptionName()})
	}
}

// parseInteractive reads a native-flow message: its body, its header
// picture, its reply, list, copy and link buttons, and a carousel's cards
// (from zapfast-go 3104c46 internal/backend/interactive.go, MIT).
func parseInteractive(m *store.Message, im *waE2E.InteractiveMessage) {
	m.Kind = kindInteractive
	m.Text = im.GetBody().GetText()
	if t := im.GetHeader().GetTitle(); t != "" && m.Text == "" {
		m.Text = t
	}
	m.Media = interactiveMedia(im)
	for i, c := range nativeChoices(im) {
		m.Actions = append(m.Actions, store.InteractiveAction{Label: c.label, Kind: c.kind, Value: c.value, Index: i, Group: c.group, Description: c.description})
	}
	for _, card := range im.GetCarouselMessage().GetCards() {
		if len(m.Cards) >= 10 {
			break
		}
		view := store.InteractiveCard{Title: card.GetHeader().GetTitle(), Text: card.GetBody().GetText(), Media: interactiveMedia(card)}
		for i, c := range nativeChoices(card) {
			kind := c.kind
			if kind != "url" && kind != "copy" {
				// A card's reply goes from the phone.
				kind = "unavailable"
			}
			view.Actions = append(view.Actions, store.InteractiveAction{Label: c.label, Kind: kind, Value: c.value, Index: i, Group: c.group, Description: c.description})
		}
		m.Cards = append(m.Cards, view)
	}
}

func interactiveMedia(im *waE2E.InteractiveMessage) *store.Media {
	header := im.GetHeader()
	var md *store.Media
	switch {
	case header.GetImageMessage() != nil:
		x := header.GetImageMessage()
		md = &store.Media{MIME: x.GetMimetype(), Size: x.GetFileLength(), Width: x.GetWidth(), Height: x.GetHeight(), Thumbnail: x.GetJPEGThumbnail()}
	case header.GetVideoMessage() != nil:
		x := header.GetVideoMessage()
		md = &store.Media{MIME: x.GetMimetype(), Size: x.GetFileLength(), Width: x.GetWidth(), Height: x.GetHeight(), Seconds: x.GetSeconds(), Thumbnail: x.GetJPEGThumbnail()}
	case header.GetDocumentMessage() != nil:
		x := header.GetDocumentMessage()
		md = &store.Media{MIME: x.GetMimetype(), Name: x.GetFileName(), Size: x.GetFileLength(), Thumbnail: x.GetJPEGThumbnail()}
	}
	if md != nil && len(md.Thumbnail) > thumbnailMax {
		md.Thumbnail = nil
	}
	return md
}

type nativeChoice struct{ label, id, kind, value, group, description string }

// nativeChoices are a native-flow message's buttons this app can show:
// quick replies, a single-select list's rows, copy codes and web links.
func nativeChoices(im *waE2E.InteractiveMessage) []nativeChoice {
	var out []nativeChoice
	for _, button := range im.GetNativeFlowMessage().GetButtons() {
		if len(button.GetButtonParamsJSON()) > 65536 {
			continue
		}
		var params struct {
			ID       string `json:"id"`
			Display  string `json:"display_text"`
			Copy     string `json:"copy_code"`
			URL      string `json:"url"`
			Sections []struct {
				Title string `json:"title"`
				Rows  []struct {
					ID          string `json:"id"`
					Title       string `json:"title"`
					Description string `json:"description"`
				} `json:"rows"`
			} `json:"sections"`
		}
		if json.Unmarshal([]byte(button.GetButtonParamsJSON()), &params) != nil {
			continue
		}
		switch button.GetName() {
		case "quick_reply":
			if params.ID != "" && params.Display != "" {
				out = append(out, nativeChoice{label: params.Display, id: params.ID, kind: "native-reply"})
			}
		case "single_select":
			for _, section := range params.Sections {
				for _, row := range section.Rows {
					if row.ID != "" && row.Title != "" && len(out) < 256 {
						out = append(out, nativeChoice{label: row.Title, id: row.ID, kind: "native-list", group: section.Title, description: row.Description})
					}
				}
			}
		case "cta_copy":
			if params.Copy != "" {
				out = append(out, nativeChoice{label: params.Display, kind: "copy", value: params.Copy})
			}
		case "cta_url":
			if safeURL(params.URL) {
				out = append(out, nativeChoice{label: params.Display, kind: "url", value: params.URL})
			}
		}
	}
	return out
}

// safeURL is a web address with a host and no credentials.
func safeURL(raw string) bool {
	u, err := url.Parse(raw)
	return err == nil && u.Host != "" && (u.Scheme == "http" || u.Scheme == "https") && u.User == nil
}

// motionClip is the video half of a motion photo that arrived as a message
// of its own: the photo's id and the clip as a bare video message, as
// upstream's motion_clip finds them.
func motionClip(p *waE2E.Message) (string, []byte, bool) {
	child := p.GetAssociatedChildMessage().GetMessage()
	if child == nil {
		return "", nil, false
	}
	isMotion := func(m *waE2E.Message) bool {
		return m.GetMessageContextInfo().GetMessageAssociation().GetAssociationType() == waE2E.MessageAssociation_MOTION_PHOTO
	}
	if !isMotion(p) && !isMotion(child) && child.GetVideoMessage().GetContextInfo().GetPairedMediaType() != waE2E.ContextInfo_MOTION_PHOTO_CHILD {
		return "", nil, false
	}
	video := child.GetVideoMessage()
	if video == nil {
		return "", nil, false
	}
	parent := p.GetMessageContextInfo().GetMessageAssociation().GetParentMessageKey().GetID()
	if parent == "" {
		parent = child.GetMessageContextInfo().GetMessageAssociation().GetParentMessageKey().GetID()
	}
	if parent == "" {
		return "", nil, false
	}
	raw, err := proto.Marshal(&waE2E.Message{VideoMessage: video})
	if err != nil {
		return "", nil, false
	}
	return parent, raw, true
}

func clonePointer[T any](value *T) *T {
	if value == nil {
		return nil
	}
	copy := *value
	return &copy
}

func parseLiveLocation(m *store.Message, live *waE2E.LiveLocationMessage) {
	m.Kind, m.Text = kindLiveLocation, live.GetCaption()
	m.Latitude, m.Longitude = live.GetDegreesLatitude(), live.GetDegreesLongitude()
	m.LiveLocation = &store.LiveLocation{Sequence: live.GetSequenceNumber(), UpdatedAt: m.Timestamp, Available: live.DegreesLatitude != nil && live.DegreesLongitude != nil, AccuracyM: clonePointer(live.AccuracyInMeters), SpeedMPS: clonePointer(live.SpeedInMps), HeadingDeg: clonePointer(live.DegreesClockwiseFromMagneticNorth)}
	if math.IsNaN(m.Latitude) || math.IsNaN(m.Longitude) || math.IsInf(m.Latitude, 0) || math.IsInf(m.Longitude, 0) || m.Latitude < -90 || m.Latitude > 90 || m.Longitude < -180 || m.Longitude > 180 {
		m.LiveLocation.Available = false
		m.Latitude, m.Longitude = 0, 0
	}
	if speed := m.LiveLocation.SpeedMPS; speed != nil && (math.IsNaN(float64(*speed)) || math.IsInf(float64(*speed), 0) || *speed < 0) {
		m.LiveLocation.SpeedMPS = nil
	}
}

func messageContext(p *waE2E.Message) *waE2E.ContextInfo {
	switch {
	case p.GetExtendedTextMessage() != nil:
		return p.GetExtendedTextMessage().GetContextInfo()
	case p.GetImageMessage() != nil:
		return p.GetImageMessage().GetContextInfo()
	case p.GetVideoMessage() != nil:
		return p.GetVideoMessage().GetContextInfo()
	case p.GetPtvMessage() != nil:
		return p.GetPtvMessage().GetContextInfo()
	case p.GetDocumentMessage() != nil:
		return p.GetDocumentMessage().GetContextInfo()
	case p.GetAudioMessage() != nil:
		return p.GetAudioMessage().GetContextInfo()
	case p.GetStickerPackMessage() != nil:
		return p.GetStickerPackMessage().GetContextInfo()
	case p.GetStickerMessage() != nil:
		return p.GetStickerMessage().GetContextInfo()
	case p.GetLocationMessage() != nil:
		return p.GetLocationMessage().GetContextInfo()
	case p.GetLiveLocationMessage() != nil:
		return p.GetLiveLocationMessage().GetContextInfo()
	case p.GetContactMessage() != nil:
		return p.GetContactMessage().GetContextInfo()
	}
	return nil
}

func setContext(p *waE2E.Message, c *waE2E.ContextInfo) {
	switch {
	case p.GetExtendedTextMessage() != nil:
		p.ExtendedTextMessage.ContextInfo = c
	case p.GetImageMessage() != nil:
		p.ImageMessage.ContextInfo = c
	case p.GetVideoMessage() != nil:
		p.VideoMessage.ContextInfo = c
	case p.GetPtvMessage() != nil:
		p.PtvMessage.ContextInfo = c
	case p.GetDocumentMessage() != nil:
		p.DocumentMessage.ContextInfo = c
	case p.GetAudioMessage() != nil:
		p.AudioMessage.ContextInfo = c
	case p.GetStickerPackMessage() != nil:
		p.StickerPackMessage.ContextInfo = c
	case p.GetStickerMessage() != nil:
		p.StickerMessage.ContextInfo = c
	case p.GetLocationMessage() != nil:
		p.LocationMessage.ContextInfo = c
	case p.GetLiveLocationMessage() != nil:
		p.LiveLocationMessage.ContextInfo = c
	case p.GetContactMessage() != nil:
		p.ContactMessage.ContextInfo = c
	}
}

// firstLine splits text at its first newline: a location's name and
// address, a contact's name and vCard.
func firstLine(text string) (string, string) {
	first, rest, _ := strings.Cut(text, "\n")
	return first, rest
}
