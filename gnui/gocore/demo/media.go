package demo

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"zapfast/abi"
	"zapfast/gocore/out"
)

// media is an attachment of a stock file: its size from the file, its
// width and height when known.
func media(mime, path string, w, h uint32) abi.Media {
	m := abi.Media{Mime: bs(mime), Size: fileSize(path)}
	if w > 0 {
		m.Width, m.HasWidth = w, true
	}
	if h > 0 {
		m.Height, m.HasHeight = h, true
	}
	if path != "" {
		m.Path, m.HasPath = opt(path)
	}
	return m
}

func photo(name string, w, h uint32, caption string) abi.Content {
	c := abi.Content{Tag: abi.ContentTagImage}
	c.Image.Media = media("image/jpeg", stock(name, asset("photos/"+name)), w, h)
	if caption != "" {
		c.Image.Caption, c.Image.HasCaption = opt(caption)
	}
	return c
}

func video(path string, w, h uint32, caption string, gif, note bool) abi.Content {
	c := abi.Content{Tag: abi.ContentTagVideo}
	c.Video = abi.ContentVideo{Media: media("video/mp4", path, w, h), Seconds: 4, HasSeconds: true, Gif: gif, Note: note}
	if caption != "" {
		c.Video.Caption, c.Video.HasCaption = opt(caption)
	}
	return c
}

func document(mime, path, name, caption string, pages uint32) abi.Content {
	c := abi.Content{Tag: abi.ContentTagDocument}
	c.Document = abi.ContentDocument{Media: media(mime, path, 0, 0), FileName: bs(name)}
	if caption != "" {
		c.Document.Caption, c.Document.HasCaption = opt(caption)
	}
	if pages > 0 {
		c.Document.Pages, c.Document.HasPages = pages, true
	}
	return c
}

func link(text, url, title string) abi.Content {
	c := textContent(text)
	if url != "" {
		c.Text.HasPreview = true
		c.Text.Preview.URL = bs(url)
		c.Text.Preview.Title, c.Text.Preview.HasTitle = opt(title)
	}
	return c
}

// posterOf is the thumbnail a real video message carries, for the demo's
// clips: the first frame of the stock clip it plays.
func posterOf(c *abi.Content) []byte {
	if c.Tag != abi.ContentTagVideo || c.Video.Gif {
		return nil
	}
	if c.Video.Note {
		return notePoster
	}
	return launchPoster
}

func stickerMedia(path string) abi.Media {
	animated := false
	stem := strings.TrimSuffix(filepath.Base(path), filepath.Ext(path))
	for _, s := range stickers {
		if s.name == stem {
			animated = s.animated
			break
		}
	}
	side := uint32(512)
	if animated {
		side = 256
	}
	return media("image/webp", path, side, side)
}

func stickerContent(path string) abi.Content {
	c := abi.Content{Tag: abi.ContentTagSticker}
	c.Sticker.Media = stickerMedia(path)
	c.Sticker.Animated = c.Sticker.Media.Width == 256
	return c
}

// addVoiceNotes files the demo's voice notes, before each chat's last line
// so the chat list does not change: one from Ada, one from Grace in Rust
// Berlin, one of ours to Grace, already played, and one from Dorothy.
func (s *state) addVoiceNotes() {
	notes := []struct {
		chat, sender   string
		mine           bool
		seconds, pitch float32
	}{
		{ada, ada, false, 4.2, 210},
		{berlin, grace, false, 6.5, 175},
		{grace, Me, true, 2.8, 125},
		{dorothy, dorothy, false, 5.0, 195},
	}
	for i, n := range notes {
		content := voiceNote(fmt.Sprintf("note-%d-v1.ogg", i), speech(n.seconds, n.pitch), false)
		lines, ok := s.messages[n.chat]
		if !ok {
			continue
		}
		at := max(len(lines)-1, 0)
		ts := now()
		if at < len(lines) {
			ts = lines[at].Timestamp - 30
		}
		m := textMessage(n.chat, fmt.Sprintf("DEMOVOICE%d", i), n.mine, n.sender, ts, "")
		m.Content = content
		if n.mine {
			m.Status = abi.DeliveryPlayed
		}
		s.messages[n.chat] = insert(lines, at, m)
	}
}

func insert(ms []*message, at int, m *message) []*message {
	ms = append(ms, nil)
	copy(ms[at+1:], ms[at:])
	ms[at] = m
	return ms
}

// addMedia is the demo's media, written once into MediaDir after the chat
// list went out: Ada's shared photos, clip, documents and links (her info
// panel), before her lines; a sticker from Katherine and one from Margaret
// in Family, a GIF from Katherine, and the stock pack shared by Grace in
// Rust Berlin, each before its chat's last line; Margaret's bubbles of
// each kind and Dorothy's photo and clip; the sticker shelves.
func (s *state) addMedia() {
	pdf := tinyPDF("Notes on the Analytical Engine", []string{
		"Note G: computing the Bernoulli numbers.",
		"The engine weaves algebraic patterns",
		"as the Jacquard loom weaves flowers and leaves.",
	})
	csv := []byte("n,B(n)\n0,1\n1,-1/2\n2,1/6\n4,-1/30\n6,1/42\n8,-1/30\n10,5/66\n")
	replay := func() string { return stock("replay.mp4", asset("videos/launch.mp4")) }
	adaMedia := []struct {
		mine    bool
		content abi.Content
	}{
		{false, photo("engine.jpg", 900, 1200, "The Difference Engine at the museum")},
		{true, photo("dusk.jpg", 1200, 800, "")},
		{false, photo("launch.jpg", 1024, 683, "Launch day!")},
		{false, video(replay(), 640, 360, "The replay", false, false)},
		{false, document("application/pdf", stock("Notes on the Analytical Engine.pdf", pdf), "Notes on the Analytical Engine.pdf", "My notes, as promised", 1)},
		{true, document("text/csv", stock("bernoulli.csv", csv), "bernoulli.csv", "", 0)},
		{false, link("Background reading: https://en.wikipedia.org/wiki/Analytical_engine",
			"https://en.wikipedia.org/wiki/Analytical_engine", "Analytical engine - Wikipedia")},
		{true, link("The museum's page https://www.computerhistory.org/babbage/ and the language I used https://www.rust-lang.org/", "", "")},
	}
	if lines, ok := s.messages[ada]; ok {
		first := now()
		if len(lines) > 0 {
			first = lines[0].Timestamp
		}
		var earlier []*message
		for i, am := range adaMedia {
			// Before her lines, a day apart: the photos and clip three days
			// back, the documents two, the links yesterday, each about two
			// hours earlier in the day than her first line, ten minutes
			// apart.
			days, nth := int64(1), i-6
			switch {
			case i <= 3:
				days, nth = 3, i
			case i <= 5:
				days, nth = 2, i-4
			}
			at := first - days*86_400 - 2*3600 + int64(nth)*600
			sender := ada
			if am.mine {
				sender = Me
			}
			m := textMessage(ada, fmt.Sprintf("Ada-media-%d", i), am.mine, sender, at, "")
			m.Content = am.content
			m.thumb = posterOf(&m.Content)
			earlier = append(earlier, m)
		}
		s.messages[ada] = append(earlier, lines...)
	}

	s.stickers = stockShelves()
	type beforeLast struct {
		chat, sender, id string
		content          abi.Content
		thumb            []byte
	}
	sticker := func(name string) abi.Content {
		if p := stickerFile(name); p != "" {
			return stickerContent(p)
		}
		return textContent("")
	}
	pack := abi.Content{Tag: abi.ContentTagStickerPack}
	pack.StickerPack = abi.ContentStickerPack{Name: bs(packName), Publisher: bs("Grace Hopper"), Count: uint32(len(stickers))}
	pack.StickerPack.Caption, pack.StickerPack.HasCaption = opt("For the meetup")
	list := []beforeLast{
		{katherine, katherine, "DEMOSTICKER0", sticker("goat"), nil},
		{katherine, katherine, "DEMOGIF0", video(stock("gif-launch.mp4", gifClip), 320, 240, "", true, false), gifStill(gifs[0].name)},
		{family, margaret, "DEMOSTICKER1", sticker("raccoon"), nil},
		{berlin, grace, "DEMOPACK0", pack, nil},
		{dorothy, dorothy, "DEMOLOCKED0", photo("launch.jpg", 1024, 683, "From the launch site"), nil},
		{dorothy, dorothy, "DEMOLOCKED1", video(replay(), 640, 360, "The test firing", false, false), nil},
	}
	// For upstream's bubble pages: Margaret's chat has a poll, a shared
	// contact, a location, a round video, an edited and a forwarded line,
	// and her first line our reaction and hers.
	poll := abi.Content{Tag: abi.ContentTagPoll}
	poll.Poll = abi.ContentPoll{
		Question: bs("Which mission next?"),
		Options:  [][]byte{bs("Apollo 11"), bs("Apollo 13"), bs("Skylab")},
		State:    abi.PollState{Selectable: 1, Counts: []uint64{2, 1, 0}, Voters: 3, CanVote: true, HistoryComplete: true},
	}
	card := abi.Content{Tag: abi.ContentTagContact}
	card.Contact = abi.ContentContact{
		DisplayName: bs("Dorothy Vaughan"),
		Vcard:       bs("BEGIN:VCARD\nVERSION:3.0\nFN:Dorothy Vaughan\nTEL;TYPE=CELL;waid=15550003333:+1 555-000-3333\nEND:VCARD"),
	}
	place := abi.Content{Tag: abi.ContentTagLocation}
	place.Location = abi.ContentLocation{Latitude: 28.5729, Longitude: -80.649}
	place.Location.Name, place.Location.HasName = opt("Kennedy Space Center")
	place.Location.Address, place.Location.HasAddress = opt("Merritt Island, FL, USA")
	for _, m := range []struct {
		id      string
		content abi.Content
	}{
		{"DEMOPOLL0", poll},
		{"DEMOCONTACT0", card},
		{"DEMOLOCATION0", place},
		{"DEMONOTE0", video(stock("note.mp4", asset("videos/note.mp4")), 360, 360, "", false, true)},
		{"DEMOEDITED0", textContent("Restart the guidance computer, it clears the alarms.")},
		{"DEMOFORWARDED0", textContent("Houston, Tranquility Base here. The Eagle has landed.")},
	} {
		list = append(list, beforeLast{margaret, margaret, m.id, m.content, nil})
	}
	if lines := s.messages[berlin]; len(lines) > 1 {
		at := (lines[0].Timestamp + lines[1].Timestamp) / 2
		s.messages[berlin] = insert(lines, 1, textMessage(berlin, "DEMORECEIPTS0", true, Me, at, "Who's bringing the projector?"))
	}
	for _, b := range list {
		lines, ok := s.messages[b.chat]
		if !ok {
			continue
		}
		at := max(len(lines)-1, 0)
		ts := now()
		if at < len(lines) {
			ts = lines[at].Timestamp - 20
		}
		m := textMessage(b.chat, b.id, false, b.sender, ts, "")
		m.Content = b.content
		m.thumb = b.thumb
		if m.thumb == nil {
			m.thumb = posterOf(&m.Content)
		}
		m.Edited = b.id == "DEMOEDITED0"
		m.Forwarded = b.id == "DEMOFORWARDED0"
		s.messages[b.chat] = insert(lines, at, m)
	}
	if lines := s.messages[margaret]; len(lines) > 0 {
		lines[0].Reactions = []abi.Reaction{
			{Sender: bs(Me), FromMe: true, Emoji: bs("❤️")},
			{Sender: bs(margaret), FromMe: false, Emoji: bs("🚀")},
		}
	}
}

// shelves are the sticker picker's, as the worker's emit_stickers lists
// them: Recent newest first, Favorites, Received (others' stickers in
// neither), and the packs.
type shelves struct {
	favorites, recent, received []string
	packs                       []stickerPack
	emojis                      map[string][]string
}

type stickerPack struct {
	name, dir string
	stickers  []string
	local     bool
}

// stockShelves is written once into MediaDir: Recent has four, Favorites
// two, Received the two others sent in their chats, and there is one pack
// of all eight and one local pack of our own.
func stockShelves() shelves {
	var all []string
	emojis := map[string][]string{}
	for _, st := range stickers {
		if p := stickerFile(st.name); p != "" {
			all = append(all, p)
		}
	}
	for i, p := range all {
		emojis[p] = stickers[i].emojis
	}
	pick := func(names ...string) []string {
		var ps []string
		for _, n := range names {
			if p := stickerFile(n); p != "" {
				ps = append(ps, p)
			}
		}
		return ps
	}
	mine := filepath.Join(MediaDir(), "packs", "mine")
	os.MkdirAll(mine, 0o755)
	return shelves{
		recent:    pick("pug", "frog", "astronaut-float", "cat"),
		favorites: pick("owl", "astronaut"),
		received:  pick("goat", "raccoon"),
		packs: []stickerPack{
			{name: packName, dir: filepath.Join(MediaDir(), "stickers"), stickers: all},
			{name: "My stickers", dir: mine, stickers: pick("astronaut", "owl"), local: true},
		},
		emojis: emojis,
	}
}

// used makes a sticker we sent Recent's newest, whatever was removed
// before.
func (sh *shelves) used(path string) {
	sh.recent = without(sh.recent, path)
	sh.received = without(sh.received, path)
	sh.recent = append([]string{path}, sh.recent...)
}

func without(paths []string, path string) []string {
	out := paths[:0:0]
	for _, p := range paths {
		if !samePath(p, path) {
			out = append(out, p)
		}
	}
	return out
}

// emitStickers is dto.rs's Stickers: the picker's order, then the emoji
// tags in path order so the same state always makes the same records.
func (s *state) emitStickers() {
	sh := &s.stickers
	var rows []abi.StickerEntry
	entry := func(tag abi.StickerEntryTag, path string) abi.StickerEntry {
		e := abi.StickerEntry{Tag: tag}
		switch tag {
		case abi.StickerEntryTagFavorite:
			e.Favorite.Path = bs(path)
		case abi.StickerEntryTagPackSticker:
			e.PackSticker.Path = bs(path)
		case abi.StickerEntryTagRecent:
			e.Recent.Path = bs(path)
		case abi.StickerEntryTagReceived:
			e.Received.Path = bs(path)
		}
		return e
	}
	for _, p := range sh.favorites {
		rows = append(rows, entry(abi.StickerEntryTagFavorite, p))
	}
	for _, pk := range sh.packs {
		e := abi.StickerEntry{Tag: abi.StickerEntryTagPack}
		e.Pack = abi.StickerEntryPack{Name: bs(pk.name), Dir: bs(pk.dir), Local: pk.local}
		rows = append(rows, e)
		for _, p := range pk.stickers {
			rows = append(rows, entry(abi.StickerEntryTagPackSticker, p))
		}
	}
	for _, p := range sh.recent {
		rows = append(rows, entry(abi.StickerEntryTagRecent, p))
	}
	for _, p := range sh.received {
		rows = append(rows, entry(abi.StickerEntryTagReceived, p))
	}
	tagged := make([]string, 0, len(sh.emojis))
	for p := range sh.emojis {
		tagged = append(tagged, p)
	}
	sort.Strings(tagged)
	for _, p := range tagged {
		e := abi.StickerEntry{Tag: abi.StickerEntryTagTagged}
		e.Tagged.Path = bs(p)
		for _, em := range sh.emojis[p] {
			e.Tagged.Emojis = append(e.Tagged.Emojis, bs(em))
		}
		rows = append(rows, e)
	}
	out.List(s.out, rows, func(batch uint32, last bool, entries []abi.StickerEntry) *abi.Event {
		return &abi.Event{Tag: abi.EventTagStickers, Stickers: abi.EventStickers{Batch: batch, Last: last, Entries: entries}}
	})
}
