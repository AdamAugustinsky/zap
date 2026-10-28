// From zapfast-go 3104c46 (internal/migration/transform.go), MIT: the Rust
// archive's rows as Go archive chats and messages. Changed for Zap's Rust
// core (upstream a95d8f0, src/model.rs Content): link cards, mentions, call
// entries, sticker packs and view-once kinds, and media adopted in place.

package migrate

import (
	"encoding/json"
	"math"
	"strings"

	"zapfast/gocore/store"
)

// rustWaiting is what the Rust core stored for a message it could not
// decrypt yet (zf:src/backend/worker.rs:4770).
const rustWaiting = "Waiting for this message. Open WhatsApp on your phone"

type rustMedia struct {
	MIME   string `json:"mime"`
	Size   uint64 `json:"size"`
	Width  uint32 `json:"width"`
	Height uint32 `json:"height"`
	Path   string `json:"path"`
}

type rustButton struct {
	Label  string          `json:"label"`
	URL    string          `json:"url"`
	Action json.RawMessage `json:"action"`
}

type rustCard struct {
	Body       string       `json:"body"`
	Buttons    []rustButton `json:"buttons"`
	Image      *rustMedia   `json:"image"`
	Carousel   []rustCard   `json:"carousel"`
	Thumbnail  []byte       `json:"thumbnail"`
	NeedsPhone bool         `json:"needs_phone"`
}

// rustContent is every variant of the Rust core's Content, tagged by kind.
type rustContent struct {
	Kind, Text, Caption, Question, What string
	Preview                             *struct{ URL, Title, Description string }
	Media                               *rustMedia
	Card                                *rustCard
	Seconds                             uint32
	GIF                                 bool `json:"gif"`
	Note                                bool
	VoiceNote                           bool `json:"voice_note"`
	Animated                            bool
	Waveform                            []byte
	FileName                            string `json:"file_name"`
	Latitude, Longitude                 float64
	AccuracyM                           *uint32  `json:"accuracy_m"`
	SpeedMPS                            *float32 `json:"speed_mps"`
	HeadingDeg                          *uint32  `json:"heading_deg"`
	Sequence, Updated                   int64
	Ended                               bool
	Name, Address, Publisher            string
	DisplayName                         string `json:"display_name"`
	Options                             []string
	State                               struct {
		Selectable      int
		Counts          []int
		Selected        []int
		HistoryComplete bool `json:"history_complete"`
	}
	// A call entry.
	Video    bool
	Status   string
	Duration uint64
	Group    bool
	// A view-once message.
	ViewOnce bool `json:"view_once"`
}

// delivery is a Rust delivery rank's name in the Go archive.
func delivery(value int64) string {
	switch value {
	case 1:
		return "pending"
	case 2:
		return "sent"
	case 3:
		return "delivered"
	case 4:
		return "read"
	case 5:
		return "played"
	case 6:
		return "failed"
	default:
		return ""
	}
}

func (imp *importer) chat(row map[string]any) store.Chat {
	id := stringValue(row, "id")
	kind := strings.ToLower(stringValue(row, "kind"))
	switch kind {
	case "broadcast":
		kind = "channel"
	case "direct", "group", "channel":
	default:
		kind = "direct"
		if strings.HasSuffix(id, "@g.us") {
			kind = "group"
		}
		if strings.HasSuffix(id, "@newsletter") {
			kind = "channel"
		}
	}
	chat := store.Chat{ID: id, Name: stringValue(row, "name"), Kind: kind, LastActivity: intValue(row, "last_activity"), Unread: int(intValue(row, "unread")), Archived: boolValue(row, "archived"), Pinned: boolValue(row, "pinned"), PinnedAt: intValue(row, "pinned_at"), Locked: boolValue(row, "locked"), ReadOnly: boolValue(row, "read_only"), Left: boolValue(row, "left"), Admin: boolValue(row, "group_admin"), InfoLocked: boolValue(row, "info_locked"), HistoryStart: boolValue(row, "history_start"), MarkedUnread: boolValue(row, "marked_unread"), EphemeralSeconds: uint32(max(0, intValue(row, "ephemeral_expiration")))}
	if row["lock_updated_at"] != nil {
		chat.LockKnown = true
		chat.LockUpdatedAt = intValue(row, "lock_updated_at")
	}
	if row["muted_until"] != nil {
		// Rust's Some(0) is muted for good.
		if chat.MutedUntil = intValue(row, "muted_until"); chat.MutedUntil == 0 {
			chat.MutedUntil = math.MaxInt64
		}
	}
	if kind == "group" && row["info_locked"] == nil {
		// Not yet known: the safe side until the group's metadata says.
		chat.InfoLocked = true
	}
	_ = json.Unmarshal([]byte(stringValue(row, "participants")), &chat.Participants)
	if chat.Name == "" {
		chat.Name = id
	}
	return chat
}

func (imp *importer) message(row map[string]any) (store.Message, bool) {
	m := store.Message{ID: stringValue(row, "id"), ChatID: stringValue(row, "chat"), Sender: stringValue(row, "sender"), SenderName: stringValue(row, "sender_name"), FromMe: boolValue(row, "from_me"), Timestamp: intValue(row, "timestamp"), Order: intValue(row, "history_order"), Status: delivery(intValue(row, "status")), Edited: boolValue(row, "edited"), Forwarded: boolValue(row, "forwarded"), Raw: bytesValue(row, "raw")}
	if row["history_order"] == nil {
		m.Order = math.MaxInt64/2 + min(intValue(row, "__rowid"), math.MaxInt64/2)
	}
	if mentions := stringValue(row, "mentions"); mentions != "" {
		_ = json.Unmarshal([]byte(mentions), &m.Mentions)
	}
	if quote := stringValue(row, "quoted"); quote != "" {
		var q struct{ ID, Sender, Summary string }
		if json.Unmarshal([]byte(quote), &q) == nil {
			m.Quote = &store.Quote{ID: q.ID, Sender: q.Sender, Text: q.Summary}
		}
	}
	if reactions := stringValue(row, "reactions"); reactions != "" {
		_ = json.Unmarshal([]byte(reactions), &m.Reactions)
	}
	var content rustContent
	if json.Unmarshal([]byte(stringValue(row, "content")), &content) != nil {
		m.Kind = "unsupported"
		m.Text = "Imported message with unavailable stored content"
		return m, true
	}
	m.Kind, m.Text = content.Kind, content.Text
	unsupported := false
	switch content.Kind {
	case "text":
		if p := content.Preview; p != nil && p.URL != "" {
			m.LinkPreview = &store.LinkPreview{URL: p.URL, Title: p.Title, Description: p.Description}
		}
	case "interactive":
		m.Kind = "text"
		if content.Card != nil {
			m.Actions = importButtons(content.Card.Buttons)
			if content.Card.Image != nil {
				m.Media = imp.media(content.Card.Image)
				m.Media.Thumbnail = content.Card.Thumbnail
				m.Kind = "image"
			}
			for _, card := range content.Card.Carousel {
				view := store.InteractiveCard{Text: card.Body, Actions: importButtons(card.Buttons), Media: imp.media(card.Image)}
				if view.Media != nil {
					view.Media.Thumbnail = card.Thumbnail
				}
				m.Cards = append(m.Cards, view)
			}
		}
		unsupported = true
	case "image", "video", "audio", "document", "sticker":
		m.Media = imp.media(content.Media)
		m.Text = content.Caption
		if m.Media != nil {
			m.Media.Seconds = content.Seconds
			m.Media.Animated = content.Animated || content.GIF
			m.Media.Waveform = content.Waveform
			m.Media.PTT = content.VoiceNote
			m.Media.Name = content.FileName
			m.Media.Thumbnail = bytesValue(row, "thumbnail")
		}
		if content.Kind == "video" && content.Note {
			m.Kind = "ptv"
		}
		if content.Kind == "audio" && content.VoiceNote {
			m.Kind = "voice"
		}
	case "location", "livelocation":
		m.Kind = "location"
		m.Latitude, m.Longitude = content.Latitude, content.Longitude
		m.Text = strings.TrimSpace(content.Name + " " + content.Address)
		if content.Kind == "livelocation" {
			m.Kind = "live-location"
			m.LiveLocation = &store.LiveLocation{Available: true, AccuracyM: content.AccuracyM, SpeedMPS: content.SpeedMPS, HeadingDeg: content.HeadingDeg, Sequence: content.Sequence, UpdatedAt: content.Updated, Ended: content.Ended}
			if m.LiveLocation.UpdatedAt == 0 {
				m.LiveLocation.UpdatedAt = m.Timestamp
			}
		}
	case "call":
		m.Kind = store.KindCall
		m.Call = &store.Call{Video: content.Video, Status: content.Status, Seconds: uint32(min(content.Duration, math.MaxUint32)), Group: content.Group}
	case "contact":
		m.Kind = "unsupported"
		if m.Text = content.DisplayName; m.Text == "" {
			m.Text = "Shared contact"
		}
		unsupported = true
	case "sticker_pack":
		m.Kind = "unsupported"
		if m.Text = strings.TrimSpace(content.Name + " " + content.Caption); m.Text == "" {
			m.Text = "Sticker pack"
		}
		unsupported = true
	case "poll":
		m.Text = content.Question
		if m.PollSelectable = content.State.Selectable; m.PollSelectable <= 0 {
			m.PollSelectable = 1
		}
		m.PollHistoryComplete = content.State.HistoryComplete
		for i, name := range content.Options {
			option := store.PollOption{Name: name}
			if i < len(content.State.Counts) {
				option.Votes = content.State.Counts[i]
			}
			for _, selected := range content.State.Selected {
				if selected == i {
					option.Selected = true
				}
			}
			m.Poll = append(m.Poll, option)
		}
		imp.polls[rowKey(m.ChatID, m.ID)] = content.Options
	case "revoked":
		m.Kind = "text"
		m.Deleted = true
		m.Text = "This message was deleted"
	case "phoneonly":
		m.Kind = "phone-only"
		m.Text = "Open this message on your phone"
		unsupported = true
	case "unsupported":
		// The Rust core filed a message it could not decrypt yet as
		// unsupported with this text; the Go engine has a kind for it.
		if content.What == rustWaiting {
			m.Kind, m.Text = "waiting", ""
			break
		}
		if m.Text = content.What; m.Text == "" {
			m.Text = "Unsupported imported message"
		}
		unsupported = true
	default:
		m.Kind = "unsupported"
		m.Text = "Imported " + content.Kind + " message is unavailable on this linked device"
		unsupported = true
	}
	return m, unsupported
}

func (imp *importer) media(source *rustMedia) *store.Media {
	if source == nil {
		return nil
	}
	return &store.Media{MIME: source.MIME, Size: source.Size, Width: source.Width, Height: source.Height, Path: imp.adoptMedia(source.Path)}
}

func importButtons(buttons []rustButton) []store.InteractiveAction {
	out := make([]store.InteractiveAction, 0, len(buttons))
	for i, button := range buttons {
		a := store.InteractiveAction{Label: button.Label, Kind: "unavailable", Index: i}
		if button.URL != "" {
			a.Kind, a.Value = "url", button.URL
		} else {
			var action map[string]json.RawMessage
			if json.Unmarshal(button.Action, &action) == nil {
				if value, ok := action["Copy"]; ok {
					var text string
					if json.Unmarshal(value, &text) == nil {
						a.Kind, a.Value = "copy", text
					}
				}
			}
		}
		out = append(out, a)
	}
	return out
}
