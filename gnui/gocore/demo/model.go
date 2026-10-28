package demo

import (
	"fmt"
	"net/url"
	"path/filepath"
	"strconv"
	"strings"

	"zapfast/abi"
	"zapfast/markup"
)

// The demo keeps its chats and messages as the abi's records, with
// upstream's model rules (src/model.rs) where it needs them: a chat's kind
// from its id, a message's summary, a chat's shared links and media.

// message is one message the demo holds: the record, and its thumbnail's
// bytes, which become an event blob each time it goes out.
type message struct {
	abi.Message
	thumb []byte
}

func (m *message) id() string { return string(m.ID) }

func bs(s string) []byte { return []byte(s) }

func opt(s string) ([]byte, bool) { return []byte(s), true }

// chatKind is upstream's ChatKind::from_id.
func chatKind(id string) abi.ChatKind {
	switch id[strings.LastIndexByte(id, '@')+1:] {
	case "g.us":
		return abi.ChatKindGroup
	case "newsletter", "broadcast":
		return abi.ChatKindBroadcast
	}
	return abi.ChatKindDirect
}

// newChat is upstream's Chat::new.
func newChat(id, name string) *abi.Chat {
	return &abi.Chat{ID: bs(id), Name: bs(name), Kind: chatKind(id)}
}

// textMessage is zfcore's text_message: ours are read, theirs have no
// status.
func textMessage(chat, id string, fromMe bool, sender string, ts int64, text string) *message {
	m := &message{Message: abi.Message{
		ID: bs(id), Chat: bs(chat), Sender: bs(sender), FromMe: fromMe, Timestamp: ts,
		Content: textContent(text),
	}}
	if fromMe {
		m.Status = abi.DeliveryRead
	}
	return m
}

func textContent(text string) abi.Content {
	c := abi.Content{Tag: abi.ContentTagText}
	c.Text.Text = bs(text)
	return c
}

// lastOf is the chat row's latest message.
func lastOf(m *message) abi.LastMessage {
	s := bs(summary(&m.Content))
	return abi.LastMessage{
		FromMe: m.FromMe, Sender: m.Sender, SenderName: m.SenderName, HasSenderName: m.HasSenderName,
		Summary: s, Full: s, Status: m.Status,
	}
}

// summary is upstream's Content::summary: the one line chat rows and
// quotes show.
func summary(c *abi.Content) string {
	switch c.Tag {
	case abi.ContentTagText:
		return firstLine(string(c.Text.Text))
	case abi.ContentTagInteractive:
		return firstLine(string(c.Interactive.Text))
	case abi.ContentTagImage:
		return withCaption("Photo", c.Image.Caption, c.Image.HasCaption)
	case abi.ContentTagVideo:
		return withCaption(videoLabel(c.Video.Gif, c.Video.Note), c.Video.Caption, c.Video.HasCaption)
	case abi.ContentTagAudio:
		label := "Audio"
		if c.Audio.VoiceNote {
			label = "Voice message"
		}
		if c.Audio.HasSeconds {
			s := c.Audio.Seconds
			return fmt.Sprintf("%s (%d:%02d)", label, s/60, s%60)
		}
		return label
	case abi.ContentTagDocument:
		return "Document: " + string(c.Document.FileName)
	case abi.ContentTagSticker:
		return "Sticker"
	case abi.ContentTagStickerPack:
		return "Sticker pack: " + string(c.StickerPack.Name)
	case abi.ContentTagLocation:
		if c.Location.HasName {
			return "Location: " + string(c.Location.Name)
		}
		return "Location"
	case abi.ContentTagLiveLocation:
		if c.LiveLocation.Ended {
			return "Live location ended"
		}
		return "Live location"
	case abi.ContentTagContact:
		return "Contact: " + string(c.Contact.DisplayName)
	case abi.ContentTagPoll:
		return "Poll: " + string(c.Poll.Question)
	case abi.ContentTagRevoked:
		return "This message was deleted"
	case abi.ContentTagUnsupported:
		return "Unsupported message (" + string(c.Unsupported.What) + ")"
	case abi.ContentTagWaiting:
		return "Waiting for this message"
	case abi.ContentTagPhoneOnly:
		p := &c.PhoneOnly
		switch {
		case p.LiveLocation:
			return "Live location"
		case p.HasOnce:
			switch p.Once {
			case abi.OnceMediaPhoto:
				return "View once photo"
			case abi.OnceMediaVideo:
				return "View once video"
			case abi.OnceMediaVoice:
				return "View once voice message"
			}
			return "View once audio"
		case p.ViewOnce:
			return "View once message"
		}
		return "Message on your phone"
	case abi.ContentTagCall:
		k := &c.Call
		label := "Voice call"
		switch {
		case k.Status == abi.CallStatusMissed && k.Video:
			label = "Missed video call"
		case k.Status == abi.CallStatusMissed:
			label = "Missed voice call"
		case k.Video:
			label = "Video call"
		}
		if k.Status == abi.CallStatusAnswered && k.Duration > 0 {
			return label + " (" + callLength(k.Duration) + ")"
		}
		return label
	}
	return ""
}

// firstLine is Rust's text.lines().next(): up to the first \n, without a
// \r before it.
func firstLine(s string) string {
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		s = s[:i]
	}
	return strings.TrimSuffix(s, "\r")
}

func withCaption(label string, caption []byte, has bool) string {
	if has {
		if l := firstLine(string(caption)); l != "" {
			return label + ": " + l
		}
	}
	return label
}

func videoLabel(gif, note bool) string {
	switch {
	case gif:
		return "GIF"
	case note:
		return "Video message"
	}
	return "Video"
}

// callLength is upstream's util::call_length in English.
func callLength(seconds uint64) string {
	h, m, s := seconds/3600, seconds%3600/60, seconds%60
	n := strconv.FormatUint
	switch {
	case h == 0 && m == 0:
		return n(s, 10) + " s"
	case h == 0 && s == 0:
		return n(m, 10) + " min"
	case h == 0:
		return n(m, 10) + " min " + n(s, 10) + " s"
	case m == 0:
		return n(h, 10) + " h"
	}
	return n(h, 10) + " h " + n(m, 10) + " min"
}

// webLinks is upstream's Content::web_links: a text's preview address with
// its title, then the others written in it, each once.
func webLinks(c *abi.Content) (links []abi.SharedEntryLink) {
	if c.Tag != abi.ContentTagText {
		return nil
	}
	add := func(u string, title []byte, hasTitle bool) {
		for _, l := range links {
			if string(l.URL) == u {
				return
			}
		}
		links = append(links, abi.SharedEntryLink{URL: bs(u), Title: title, HasTitle: hasTitle})
	}
	if p := &c.Text.Preview; c.Text.HasPreview {
		has := p.HasTitle && strings.TrimSpace(string(p.Title)) != ""
		var title []byte
		if has {
			title = p.Title
		}
		// Listed even when an earlier one matches: the preview comes first.
		links = append(links, abi.SharedEntryLink{URL: bs(previewURL(string(p.URL))), Title: title, HasTitle: has})
	}
	for _, u := range markup.Links(c.Text.Text) {
		add(previewURL(u), nil, false)
	}
	return links
}

// previewURL is upstream's safety::preview_url as a normaliser: a web
// address, with https:// when it has no scheme, as the URL standard writes
// it; the address itself when it is not one.
func previewURL(value string) string {
	if strings.ContainsFunc(value, func(r rune) bool { return r < 0x20 || r == 0x7f || r == '\\' }) {
		return value
	}
	v := strings.TrimSpace(value)
	u, err := url.Parse(v)
	if err != nil || u.Scheme == "" || u.Host == "" {
		u, err = url.Parse("https://" + v)
		if err != nil {
			return value
		}
	}
	if (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return value
	}
	u.Scheme = strings.ToLower(u.Scheme)
	u.Host = strings.ToLower(u.Host)
	if u.Path == "" && u.Opaque == "" {
		u.Path = "/"
	}
	return u.String()
}

// galleryKind is upstream's Content::gallery_kind: a photo, or a video
// that is not a GIF.
func galleryKind(c *abi.Content) (video, ok bool) {
	switch c.Tag {
	case abi.ContentTagImage:
		return false, true
	case abi.ContentTagVideo:
		return true, !c.Video.Gif
	}
	return false, false
}

// galleryFile reports whether the viewer shows the file: upstream's
// gallery_kind_for_path.
func galleryFile(path string) bool {
	switch strings.ToLower(strings.TrimPrefix(filepath.Ext(path), ".")) {
	case "jpg", "jpeg", "png", "webp", "mp4", "m4v", "mov", "webm", "mkv", "3gp", "3gpp":
		return true
	}
	return false
}

// contentMedia is the attachment of a message's content.
func contentMedia(c *abi.Content) *abi.Media {
	switch c.Tag {
	case abi.ContentTagImage:
		return &c.Image.Media
	case abi.ContentTagVideo:
		return &c.Video.Media
	case abi.ContentTagAudio:
		return &c.Audio.Media
	case abi.ContentTagDocument:
		return &c.Document.Media
	case abi.ContentTagSticker:
		return &c.Sticker.Media
	}
	return nil
}

// samePath is PathBuf equality: component by component.
func samePath(a, b string) bool { return filepath.Clean(a) == filepath.Clean(b) }
