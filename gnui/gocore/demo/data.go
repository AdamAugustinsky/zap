package demo

import (
	"embed"
	"fmt"
	"strings"
	"time"
)

// The people and lines are upstream's demo samples (src/demo.rs), as
// zfcore's demo.rs has them; the pictures upstream's CC0 and public-domain
// portraits (assets/SOURCES.md).

// Me is the demo's own account.
const Me = "15550001111@s.whatsapp.net"

const (
	ada       = "393331234567@s.whatsapp.net"
	grace     = "441632960123@s.whatsapp.net"
	katherine = "4915112345678@s.whatsapp.net"
	margaret  = "14155550199@s.whatsapp.net"
	berlin    = "120363012345678901@g.us"
	family    = "120363098765432109@g.us"
	// dorothy is the chat the demo keeps locked: it lives in the locked
	// folder, whose code is the UI's.
	dorothy = "15550004444@s.whatsapp.net"
)

// locked are the chats that start locked.
var locked = []string{dorothy}

type line struct {
	mine bool
	text string
}

type sample struct {
	id, name   string
	minutesAgo int64
	unread     uint32
	pinned     bool
	muted      bool
	lines      []line
}

var samples = []sample{
	{ada, "Ada Lovelace", 3, 2, true, false, []line{
		{false, "Did the analytical engine build finish?"},
		{true, "Yes! It compiles on stable now, no nightly needed."},
		{false, "That is wonderful news. Send me the branch when you can."},
	}},
	{berlin, "Rust Berlin", 25, 14, false, true, []line{
		{false, "Anyone at the meetup tonight?"},
		{true, "I'll be there around 19:00"},
		{false, "Same. Bringing the egui demo"},
		{false, "Save me a seat 🙏"},
	}},
	{grace, "Grace Hopper", 90, 0, false, false, []line{
		{true, "Found the bug. It was a moth."},
		{false, "Literally?"},
		{true, "Literally. Taped it into the logbook."},
	}},
	{katherine, "Katherine Johnson", 60 * 26, 0, false, false, []line{
		{false, "Talk is cheap. Show me the code."},
		{true, "Pushed 😌"},
	}},
	{family, "Family", 60 * 50, 0, false, false, []line{
		{false, "Dinner on Sunday at 13:00?"},
		{true, "We'll be there"},
		{false, "Bring the good bread 🥖"},
	}},
	{margaret, "Margaret Hamilton", 60 * 24 * 4, 0, false, false, []line{
		{false, "The landing software held up."},
		{true, "Never doubted it."},
	}},
	// Locked, with a photo, a clip and a voice note.
	{dorothy, "Dorothy Vaughan", 60 * 24 * 5, 0, false, false, []line{
		{false, "The trajectory numbers check out."},
		{true, "Thank you. I'll tell the team."},
		{false, "Keep these between us until the launch."},
	}},
}

// The members of the demo's groups besides us, for their calls.
var groupMembers = map[string][]string{
	berlin: {ada, grace, katherine},
	family: {margaret, grace},
}

// linkMembers are who is on a call link the demo joins.
var linkMembers = []string{ada, katherine}

func membersOf(chat string) []string { return groupMembers[chat] }

//go:embed assets
var assets embed.FS

func asset(name string) []byte {
	b, err := assets.ReadFile("assets/" + name)
	if err != nil {
		panic("demo: no asset " + name) // embedded: a mistake in this package
	}
	return b
}

// avatars are upstream's demo portraits, by owner.
var avatars = []struct{ owner, name string }{
	{Me, "astronaut.jpg"},
	{ada, "ada.jpg"},
	{grace, "grace.jpg"},
	{katherine, "katherine.jpg"},
	{margaret, "earthrise.jpg"},
	{berlin, "berlin.jpg"},
	{family, "bread.jpg"},
}

// The first frames of the stock clips, as their messages' thumbnails.
var (
	launchPoster = asset("posters/launch.jpg")
	notePoster   = asset("posters/note.jpg")
)

// gifs are the GIF results the demo finds: upstream's stock stills (320 ×
// 240), with the words a search finds each by. An empty search lists them
// all, as GIPHY's trending does.
var gifs = []struct {
	name string
	tags []string
}{
	{"launch", []string{"launch", "rocket", "space", "ship it", "go"}},
	{"fireworks", []string{"fireworks", "party", "celebrate", "yay", "happy"}},
	{"pug", []string{"pug", "dog", "puppy", "cute", "aww"}},
	{"frog", []string{"frog", "ribbit", "hello", "hi"}},
	{"goat", []string{"goat", "no", "nope", "stop"}},
	{"raccoon", []string{"raccoon", "sneaky", "night", "trash"}},
}

func gifStill(name string) []byte { return asset("gifs/" + name + ".jpg") }

// BadGifKey is the GIPHY key the demo rejects, for the bad-key state.
const BadGifKey = "zf-demo:bad-key"

// gifClip is the clip a GIF the demo sends plays: upstream's stock launch
// cut.
var gifClip = asset("videos/launch.mp4")

// stickers are upstream's stock stickers: name, whether animated, emojis.
var stickers = []struct {
	name     string
	animated bool
	emojis   []string
}{
	{"astronaut", false, []string{"🧑‍🚀", "🚀"}},
	{"astronaut-float", true, []string{"🧑‍🚀", "✨"}},
	{"cat", false, []string{"🐱", "😴"}},
	{"frog", false, []string{"🐸"}},
	{"goat", false, []string{"🐐", "🙅"}},
	{"owl", false, []string{"🦉"}},
	{"pug", false, []string{"🐶", "🥺"}},
	{"raccoon", false, []string{"🦝"}},
}

// packName is the demo's pack of all its stickers, shared in Rust Berlin.
const packName = "Launch party"

// The DemoIncoming texts that ring a call (core.DemoCallText and
// core.DemoVideoCallText).
const (
	callText      = "zf-demo:call"
	videoCallText = "zf-demo:video-call"
)

// MissedAfter is how long a call rung in waits before it is missed.
const MissedAfter = 30 * time.Second

// A placed call: until its offer goes out, until the peer's phone rings,
// until they answer, and until the media is up; frames at about 15 a
// second.
const (
	placing    = 150 * time.Millisecond
	dialing    = 400 * time.Millisecond
	ringing    = 1500 * time.Millisecond
	connecting = 300 * time.Millisecond
	frameEvery = 66 * time.Millisecond
)

// Upstream's call entries carry ids under this prefix, and a call link's
// entry this chat (model.rs).
const (
	callEntryPrefix = "zapfast-call-"
	callLinkChat    = "zapfast-call-link"
)

// tinyPDF is a one-page PDF of title and lines, in Helvetica: the demo's
// own document, so it needs no file of anyone else's.
func tinyPDF(title string, lines []string) []byte {
	text := func(s string) string {
		return strings.NewReplacer(`\`, `\\`, `(`, `\(`, `)`, `\)`).Replace(s)
	}
	content := fmt.Sprintf("BT /F1 20 Tf 72 720 Td (%s) Tj ET\n", text(title))
	for i, l := range lines {
		content += fmt.Sprintf("BT /F1 12 Tf 72 %d Td (%s) Tj ET\n", 684-18*i, text(l))
	}
	objects := []string{
		"<< /Type /Catalog /Pages 2 0 R >>",
		"<< /Type /Pages /Kids [3 0 R] /Count 1 >>",
		"<< /Type /Page /Parent 2 0 R /MediaBox [0 0 612 792] /Resources << /Font << /F1 5 0 R >> >> /Contents 4 0 R >>",
		fmt.Sprintf("<< /Length %d >>\nstream\n%sendstream", len(content), content),
		"<< /Type /Font /Subtype /Type1 /BaseFont /Helvetica >>",
	}
	pdf := []byte("%PDF-1.4\n")
	var offsets []int
	for i, o := range objects {
		offsets = append(offsets, len(pdf))
		pdf = fmt.Appendf(pdf, "%d 0 obj\n%s\nendobj\n", i+1, o)
	}
	xref := len(pdf)
	pdf = fmt.Appendf(pdf, "xref\n0 %d\n0000000000 65535 f \n", len(objects)+1)
	for _, o := range offsets {
		pdf = fmt.Appendf(pdf, "%010d 00000 n \n", o)
	}
	return fmt.Appendf(pdf, "trailer\n<< /Size %d /Root 1 0 R >>\nstartxref\n%d\n%%%%EOF\n", len(objects)+1, xref)
}
