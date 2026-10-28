// From zapfast-go 3104c46 (internal/backend/parse.go, live_location.go), MIT.
// Changed for Zap: the archive's store.Message; mentions and link previews
// are kept; a location keeps its name and address, and a contact its name
// and vCard, apart (first line, then the rest), so convert.go can give them
// back to the abi fields.

package wa

import (
	"math"
	"strings"

	"go.mau.fi/whatsmeow/proto/waE2E"
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
	kindViewOnce     = "view-once"
	kindUnsupported  = "unsupported"
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
		}
		if next == nil {
			return p
		}
		p = next
	}
	return p
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
	if raw, err := proto.Marshal(p); err == nil {
		m.Raw = raw
	}
	var ctx *waE2E.ContextInfo
	if e.IsViewOnce || p.GetViewOnceMessage() != nil || p.GetViewOnceMessageV2() != nil || p.GetViewOnceMessageV2Extension() != nil {
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
		m.Kind = kindInteractive
		m.Text = x.GetBody().GetText()
		ctx = x.GetContextInfo()
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
		m.Text = "message"
		if e.Info.MediaType == "livelocation" {
			m.Kind = kindLiveLocation
			m.LiveLocation = &store.LiveLocation{UpdatedAt: m.Timestamp}
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
			m.Quote = &store.Quote{ID: ctx.GetStanzaID(), Sender: ctx.GetParticipant(), Text: quoted.Text, Kind: quoted.Kind}
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
