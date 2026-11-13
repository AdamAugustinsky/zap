package agent

import (
	"fmt"
	"strings"
	"time"

	"zapfast/abi"
)

// Msg is a message as an agent reads it: words, never files. What only the
// phone shows (view once) stays there.
type Msg struct {
	ID   string `json:"id"`
	Time string `json:"time"`
	// From is "me" for the owner's own messages, else the sender's name,
	// or their number when WhatsApp gave no name. A name is its sender's
	// choice: Mine alone says a message is the owner's.
	From string `json:"from"`
	Mine bool   `json:"mine,omitempty"`
	// Kind is empty for plain text: "image", "video", "voice", "audio",
	// "document", "sticker", "location", "contact", "poll", "buttons",
	// "call", "deleted", "unavailable".
	Kind      string `json:"kind,omitempty"`
	Text      string `json:"text,omitempty"`
	ReplyTo   *Ref   `json:"reply_to,omitempty"`
	Edited    bool   `json:"edited,omitempty"`
	Forwarded bool   `json:"forwarded,omitempty"`
}

// Ref is the message a reply answers.
type Ref struct {
	ID   string `json:"id,omitempty"`
	From string `json:"from,omitempty"`
	Text string `json:"text,omitempty"`
}

// msgTextMax bounds one message's text as an agent gets it, in bytes.
const msgTextMax = 8000

// render is m for an agent. chat names the other side of a direct chat.
func render(m *abi.Message, e Entry, loc *time.Location) Msg {
	x := Msg{
		ID:     string(m.ID),
		Time:   time.Unix(m.Timestamp, 0).In(loc).Format(time.RFC3339),
		From:   from(m.FromMe, m.Sender, m.SenderName, m.HasSenderName, e),
		Mine:   m.FromMe,
		Edited: m.Edited, Forwarded: m.Forwarded,
	}
	x.Kind, x.Text = content(&m.Content)
	if len(x.Text) > msgTextMax {
		x.Text = clip(x.Text, msgTextMax) + " […cut]"
	}
	if m.HasQuoted {
		q := &m.Quoted
		if q.HasChat && string(q.Chat) != string(m.Chat) {
			// A private reply quotes another chat, which this agent was
			// not given: that it answers something, and no more.
			x.ReplyTo = &Ref{Text: "(a message in another chat)"}
		} else {
			x.ReplyTo = &Ref{ID: string(q.ID), Text: clip(string(q.Summary), 300)}
			if q.HasSenderName {
				x.ReplyTo.From = label(string(q.SenderName), nameMax)
			}
		}
	}
	return x
}

func from(me bool, sender, name []byte, hasName bool, e Entry) string {
	switch {
	case me:
		return "me"
	case hasName && notMe(label(string(name), nameMax)):
		return label(string(name), nameMax)
	case !e.Group && notMe(e.Name):
		return e.Name
	}
	user, _, _ := strings.Cut(string(sender), "@")
	if user == "" {
		return "unknown"
	}
	if user[0] >= '0' && user[0] <= '9' {
		return "+" + user
	}
	return user
}

// notMe reports whether name can stand for someone else: it is not empty,
// and not the word the owner's own messages carry.
func notMe(name string) bool { return name != "" && !strings.EqualFold(name, "me") }

func clock(seconds uint64) string { return fmt.Sprintf("%d:%02d", seconds/60, seconds%60) }

func captioned(caption []byte, has bool) string {
	if has {
		return string(caption)
	}
	return ""
}

// content is a message's kind and words.
func content(c *abi.Content) (kind, text string) {
	switch c.Tag {
	case abi.ContentTagText:
		return "", string(c.Text.Text)
	case abi.ContentTagInteractive:
		return "buttons", interactive(&c.Interactive)
	case abi.ContentTagImage:
		return "image", captioned(c.Image.Caption, c.Image.HasCaption)
	case abi.ContentTagVideo:
		return "video", captioned(c.Video.Caption, c.Video.HasCaption)
	case abi.ContentTagAudio:
		kind = "audio"
		if c.Audio.VoiceNote {
			kind = "voice"
		}
		if c.Audio.HasSeconds {
			text = clock(uint64(c.Audio.Seconds)) + " long, not transcribed"
		}
		return kind, text
	case abi.ContentTagDocument:
		text = string(c.Document.FileName)
		if c.Document.HasCaption && len(c.Document.Caption) > 0 {
			text += "\n" + string(c.Document.Caption)
		}
		return "document", text
	case abi.ContentTagSticker:
		return "sticker", ""
	case abi.ContentTagStickerPack:
		return "sticker", "sticker pack: " + string(c.StickerPack.Name)
	case abi.ContentTagLocation:
		l := &c.Location
		text = fmt.Sprintf("%.5f, %.5f", l.Latitude, l.Longitude)
		if l.HasName {
			text = string(l.Name) + " (" + text + ")"
		}
		if l.HasAddress {
			text += "\n" + string(l.Address)
		}
		return "location", text
	case abi.ContentTagLiveLocation:
		return "location", fmt.Sprintf("live location: %.5f, %.5f", c.LiveLocation.Latitude, c.LiveLocation.Longitude)
	case abi.ContentTagContact:
		return "contact", string(c.Contact.DisplayName)
	case abi.ContentTagPoll:
		var b strings.Builder
		b.Write(c.Poll.Question)
		for i, o := range c.Poll.Options {
			fmt.Fprintf(&b, "\n- %s", o)
			if i < len(c.Poll.State.Counts) {
				fmt.Fprintf(&b, " (%d)", c.Poll.State.Counts[i])
			}
		}
		return "poll", b.String()
	case abi.ContentTagRevoked:
		return "deleted", ""
	case abi.ContentTagCall:
		what := "voice call"
		if c.Call.Video {
			what = "video call"
		}
		if c.Call.Duration > 0 {
			what += ", " + clock(c.Call.Duration)
		}
		return "call", what
	case abi.ContentTagUnsupported:
		return "unavailable", string(c.Unsupported.What)
	case abi.ContentTagPhoneOnly:
		return "unavailable", "shown on the phone only"
	case abi.ContentTagWaiting:
		return "unavailable", "not received yet"
	}
	return "unavailable", ""
}

// interactive is a company's message with buttons: its words, then what it
// offers. An agent answers one by sending the option's words.
func interactive(x *abi.ContentInteractive) string {
	var b strings.Builder
	b.Write(x.Text)
	if !x.HasCard {
		return b.String()
	}
	card := &x.Card
	if len(card.Body) > 0 && string(card.Body) != string(x.Text) {
		if b.Len() > 0 {
			b.WriteByte('\n')
		}
		b.Write(card.Body)
	}
	buttons := func(list []abi.InteractiveButton) {
		for i := range list {
			bt := &list[i]
			fmt.Fprintf(&b, "\n[%s]", bt.Label)
			if bt.HasURL {
				fmt.Fprintf(&b, " %s", bt.URL)
			}
			if bt.Action.Tag == abi.InteractiveActionTagSelect {
				for _, o := range bt.Action.Select.Options {
					fmt.Fprintf(&b, "\n  - %s", o.Title)
					if len(o.Description) > 0 {
						fmt.Fprintf(&b, ": %s", o.Description)
					}
				}
			}
		}
	}
	buttons(card.Buttons)
	for i := range card.Carousel {
		fmt.Fprintf(&b, "\n%s", card.Carousel[i].Body)
		buttons(card.Carousel[i].Buttons)
	}
	return b.String()
}
