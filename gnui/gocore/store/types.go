// From zapfast-go 3104c46 (internal/model/model.go), MIT. The archive's
// types; Zap's additions are marked. Kept as JSON inside SQLCipher, so a
// field's json name is part of the archive's format.

package store

// Chat is one conversation as the archive keeps it.
type Chat struct {
	ID               string   `json:"id"`
	Name             string   `json:"name"`
	Kind             string   `json:"kind"`
	Avatar           string   `json:"avatar,omitempty"`
	LastActivity     int64    `json:"last_activity"`
	Preview          string   `json:"preview"`
	Unread           int      `json:"unread"`
	MarkedUnread     bool     `json:"marked_unread"`
	Archived         bool     `json:"archived"`
	Pinned           bool     `json:"pinned"`
	PinnedAt         int64    `json:"pinned_at"`
	Favorite         bool     `json:"favorite"`
	FavoritePosition int      `json:"favorite_position"`
	MutedUntil       int64    `json:"muted_until"`
	ReadOnly         bool     `json:"read_only"`
	Locked           bool     `json:"locked"`
	LockKnown        bool     `json:"lock_known"`
	LockUpdatedAt    int64    `json:"lock_updated_at,omitempty"`
	Left             bool     `json:"left"`
	Admin            bool     `json:"admin"`
	InfoLocked       bool     `json:"info_locked"`
	Participants     []string `json:"participants,omitempty"`
	Labels           []string `json:"labels,omitempty"`
	EphemeralSeconds uint32   `json:"ephemeral_seconds"`
	HistoryStart     bool     `json:"history_start"`
	// Zap: a group's description, nil until WhatsApp has said (empty: it
	// has none).
	Description *string `json:"description,omitempty"`
	// Zap: the chat's own notification sound, nil for the app's.
	Sound *Sound `json:"sound,omitempty"`
}

// Sound is a chat's notification sound (Zap): Kind is "receive", "alert",
// "system", "none" or "custom", with the file's Path for a custom one.
type Sound struct {
	Kind string `json:"kind"`
	Path string `json:"path,omitempty"`
}

func (c Chat) CanSend() bool { return !c.Locked && !c.ReadOnly && !c.Left && c.Kind != "channel" }

type Reaction struct {
	Sender    string `json:"sender"`
	Emoji     string `json:"emoji"`
	Timestamp int64  `json:"timestamp"`
}

// MessageReceipt keeps only observed stage timestamps. A read receipt does not
// invent an earlier delivery time; zero means that time was not supplied.
type MessageReceipt struct {
	Sender      string `json:"sender"`
	DeliveredAt int64  `json:"delivered_at,omitempty"`
	ReadAt      int64  `json:"read_at,omitempty"`
	PlayedAt    int64  `json:"played_at,omitempty"`
}

type Quote struct {
	ID     string `json:"id"`
	Sender string `json:"sender"`
	Text   string `json:"text"`
	Kind   string `json:"kind"`
	// Zap: the quoted message's chat when it is another one, the name its
	// sender went by, and the people it mentions.
	Chat       string    `json:"chat,omitempty"`
	SenderName string    `json:"sender_name,omitempty"`
	Mentions   []Mention `json:"mentions,omitempty"`
}

type PollOption struct {
	Name       string   `json:"name"`
	Votes      int      `json:"votes"`
	Selected   bool     `json:"selected"`
	Voters     []string `json:"voters,omitempty"`
	VoterTimes []int64  `json:"voter_times,omitempty"`
}

type InteractiveAction struct {
	Label       string `json:"label"`
	Kind        string `json:"kind"`
	Value       string `json:"value,omitempty"`
	Index       int    `json:"index"`
	Group       string `json:"group,omitempty"`
	Description string `json:"description,omitempty"`
}

type InteractiveCard struct {
	Title   string              `json:"title"`
	Text    string              `json:"text"`
	Media   *Media              `json:"media,omitempty"`
	Actions []InteractiveAction `json:"actions,omitempty"`
}

type Media struct {
	Path      string `json:"path,omitempty"`
	MIME      string `json:"mime"`
	Name      string `json:"name,omitempty"`
	Size      uint64 `json:"size"`
	Width     uint32 `json:"width"`
	Height    uint32 `json:"height"`
	Seconds   uint32 `json:"seconds"`
	Thumbnail []byte `json:"thumbnail,omitempty"`
	Waveform  []byte `json:"waveform,omitempty"`
	PTT       bool   `json:"ptt"`
	Animated  bool   `json:"animated"`
}

type LiveLocation struct {
	Sequence   int64    `json:"sequence"`
	UpdatedAt  int64    `json:"updated_at"`
	Ended      bool     `json:"ended"`
	Available  bool     `json:"available"`
	AccuracyM  *uint32  `json:"accuracy_m,omitempty"`
	SpeedMPS   *float32 `json:"speed_mps,omitempty"`
	HeadingDeg *uint32  `json:"heading_deg,omitempty"`
}

const LiveLocationLimit int64 = 8 * 60 * 60

func (m Message) LiveLocationOver(now int64) bool {
	return m.LiveLocation != nil && (m.LiveLocation.Ended || now > m.Timestamp && now-m.Timestamp > LiveLocationLimit)
}

// Mention is one person a message names (Zap: abi MentionRef, as the Rust
// core stores it). User is the visible token after the @ (the phone number,
// or "all"); ID is their JID, or MentionAll for everyone.
type Mention struct {
	User string `json:"user"`
	ID   string `json:"id"`
}

// MentionAll is the stored ID of an @all mention, which names everyone. It
// has no @, so it is never parsed as a chat.
const MentionAll = "mention-all"

// LinkPreview is the card WhatsApp attaches to a link (Zap).
type LinkPreview struct {
	URL         string `json:"url"`
	Title       string `json:"title,omitempty"`
	Description string `json:"description,omitempty"`
}

// Call is a call-log entry's details, on a message of kind "call" (Zap).
type Call struct {
	Video bool `json:"video"`
	// Status is "missed", "declined", "answered" or the like.
	Status string `json:"status"`
	// Seconds is how long it lasted, 0 when it was not answered.
	Seconds uint32 `json:"seconds"`
	Group   bool   `json:"group"`
}

// Message kinds the archive indexes (Message.Kind).
const (
	KindImage    = "image"
	KindVideo    = "video"
	KindDocument = "document"
	KindCall     = "call"
)

type Message struct {
	ID                  string              `json:"id"`
	ChatID              string              `json:"chat_id"`
	Sender              string              `json:"sender"`
	SenderName          string              `json:"sender_name"`
	FromMe              bool                `json:"from_me"`
	Timestamp           int64               `json:"timestamp"`
	Order               int64               `json:"order"`
	Kind                string              `json:"kind"`
	Text                string              `json:"text"`
	Status              string              `json:"status"`
	Edited              bool                `json:"edited"`
	Deleted             bool                `json:"deleted"`
	Starred             bool                `json:"starred"`
	Forwarded           bool                `json:"forwarded"`
	Quote               *Quote              `json:"quote,omitempty"`
	Media               *Media              `json:"media,omitempty"`
	Reactions           []Reaction          `json:"reactions,omitempty"`
	Receipts            []MessageReceipt    `json:"receipts,omitempty"`
	Poll                []PollOption        `json:"poll,omitempty"`
	PollSelectable      int                 `json:"poll_selectable"`
	PollHistoryComplete bool                `json:"poll_history_complete"`
	Actions             []InteractiveAction `json:"actions,omitempty"`
	Cards               []InteractiveCard   `json:"cards,omitempty"`
	Latitude            float64             `json:"latitude,omitempty"`
	Longitude           float64             `json:"longitude,omitempty"`
	LiveLocation        *LiveLocation       `json:"live_location,omitempty"`
	// Zap: kept with the message, indexed by user (MentionedAfter).
	Mentions []Mention `json:"mentions,omitempty"`
	// Zap: the link card, indexed with the links of the text (Shared).
	LinkPreview *LinkPreview `json:"link_preview,omitempty"`
	// Zap: set on a call-log entry (Kind "call").
	Call *Call `json:"call,omitempty"`
	// Zap: a motion photo's clip, on the photo's row.
	Motion *Motion `json:"motion,omitempty"`
	Raw    []byte  `json:"-"`
}

// Motion is the video half of a motion photo (Zap): the clip's message as
// WhatsApp sent it (a protobuf holding its download keys) and the file it
// was downloaded to.
type Motion struct {
	Clip []byte `json:"clip,omitempty"`
	Path string `json:"path,omitempty"`
}

// Label is a local chat label. CreatedAt (Zap) orders them.
type Label struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	Color     string `json:"color"`
	CreatedAt int64  `json:"created_at"`
}

type SearchHit struct {
	ChatID   string  `json:"chat_id"`
	ChatName string  `json:"chat_name"`
	Message  Message `json:"message"`
}
