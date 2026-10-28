package theme

// The read-only API other packages draw with. Every function reads the
// palette of the window being built (the theme's Apply keeps it at the
// current light/dark mix, so it fades with Rex's own fade) and costs a field
// read: call them each frame instead of keeping a colour. Main thread only.

// Colors is the palette the conversation draws with: read-only, valid for
// the frame.
func Colors() *Palette { return cur.Colors() }

// Bubble is a message bubble's fill: yours (mine) or others'.
func Bubble(mine bool) uint32 {
	if mine {
		return cur.mixed.BubbleOut
	}
	return cur.mixed.BubbleIn
}

// OnBubble is the palette for a bubble's contents (text, secondary and dim
// text, links, read ticks, accent): adapted so they stay readable on the
// bubble. It copies the palette (a few hundred bytes, no allocation).
func OnBubble(mine bool) Palette { return cur.mixed.OnBubble(mine) }

// Chat is the palette's colour behind the bubbles (upstream's wallpaper
// "Theme" colour).
func Chat() uint32 { return cur.mixed.Chat }

// Link is the colour of links in messages, and of @mentions.
func Link() uint32 { return cur.mixed.Link }

// Mention is the colour of an @mention: the link colour.
func Mention() uint32 { return cur.mixed.Link }

// Read is the colour of the ticks of read messages (on the page; on a bubble
// use OnBubble(mine).Read).
func Read() uint32 { return cur.mixed.Read }

// Dark reports whether the palette in force is the dark one (at a mix of
// half or more).
func Dark() bool { return cur.mixed.Dark }

// Sender is the colour of a group sender's name, from the sender's avatar
// hue in degrees.
func Sender(hue float32) uint32 { return cur.mixed.Sender(hue) }

// AvatarColor is an avatar's background from the chat's hue in degrees.
func AvatarColor(hue float32) uint32 { return cur.mixed.Avatar(hue) }

// BubbleShadow is the colour of the soft shadow under bubbles and date chips
// (2 points down, a 6-point blur).
func BubbleShadow() uint32 { return cur.mixed.BubbleShadow() }

// RaisedEdge is the faint light along the top of a raised surface of colour
// fill.
func RaisedEdge(fill uint32) uint32 { return cur.mixed.RaisedEdge(fill) }

// The picker and the status line (Settings → Appearance), and the folder.

// Entries are the theme files the picker lists, sorted by file name; valid
// for the frame.
func Entries() []Entry { return cur.Entries() }

// LoadErrors are the theme files the last scan skipped, with their reasons.
func LoadErrors() []LoadError { return cur.Errors() }

// Status is what Settings says under the theme setting ("" for nothing).
func Status() string { return cur.Status() }

// Reload lists the themes folder again in the background
// (`zapfast reload-themes`, Settings → Appearance); the list and the chosen
// theme follow when it finishes.
func Reload() { cur.Reload() }

// Dir is the themes folder; EnsureDir creates it (call it from a worker) so
// Settings can open it.
func Dir() string                { return cur.Dir() }
func EnsureDir() (string, error) { return cur.EnsureDir() }
