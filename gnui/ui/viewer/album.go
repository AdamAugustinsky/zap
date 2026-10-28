package viewer

import (
	"bytes"
	"sync"

	"gonativeui/img"

	"zapfast/abi"
	"zapfast/core"
	"zapfast/state"
)

// item is one picture or clip of the chat's album (abi.MediaItem, copied: the
// event's bytes are valid during the call only).
type item struct {
	id      []byte
	card    uint64
	hasCard bool
	video   bool
	path    string // "" until downloaded
	// thumb is the stored thumbnail (a small JPEG), read from its blob on a
	// worker; thumbHash names it in the image cache. Nil until it arrives.
	thumb     []byte
	thumbHash uint64
	blob      abi.BlobRef
	hasBlob   bool
}

func (it *item) is(id []byte, card uint64, hasCard bool) bool {
	return it.hasCard == hasCard && (!hasCard || it.card == card) && bytes.Equal(it.id, id)
}

// thumbDone is a thumbnail a worker has read.
type thumbDone struct {
	gen     uint32
	id      []byte
	card    uint64
	hasCard bool
	data    []byte
}

// thumbs is the hand-off from the blob workers to Build. It takes only the
// thumbnails of the album on screen, and none once the window has closed,
// so a late worker cannot leave its pictures behind.
type thumbs struct {
	mu    sync.Mutex
	gen   uint32 // the album (Screen.resetGen) whose thumbnails are wanted
	open  bool   // a window is up to take them
	ready []thumbDone
}

// expect wants the thumbnails of album gen while open, and drops what
// arrived for any other.
func (t *thumbs) expect(gen uint32, open bool) {
	t.mu.Lock()
	t.gen, t.open = gen, open
	clear(t.ready)
	t.ready = t.ready[:0]
	if !open {
		t.ready = nil
	}
	t.mu.Unlock()
}

// wanted reports whether thumbnails of album gen are still taken.
func (t *thumbs) wanted(gen uint32) bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.open && t.gen == gen
}

// put hands a thumbnail over; false (and not kept) when its album is gone or
// the window has closed.
func (t *thumbs) put(d thumbDone) bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	if !t.open || t.gen != d.gen {
		return false
	}
	t.ready = append(t.ready, d)
	return true
}

// take moves what has arrived into dst; the slots it leaves keep nothing.
func (t *thumbs) take(dst []thumbDone) []thumbDone {
	t.mu.Lock()
	dst = append(dst[:0], t.ready...)
	clear(t.ready)
	t.ready = t.ready[:0]
	t.mu.Unlock()
	return dst
}

// listenTags are the events the viewer reads besides the model's.
var listenTags = []abi.EventTag{
	abi.EventTagChatMedia, abi.EventTagMedia, abi.EventTagMotion, abi.EventTagMessageDeleted,
	abi.EventTagIncoming, abi.EventTagClipboardImage, abi.EventTagChatRemoved, abi.EventTagStickers,
}

// Event is the viewer's state.Listener: the album, the downloads and the
// clipboard answer. An account that is not on screen is ignored: the viewer
// belongs to the window.
func (s *Screen) Event(a *state.Account, e *abi.Event) {
	if !s.isLive() {
		return
	}
	if a.Index != s.acct {
		return
	}
	switch e.Tag {
	case abi.EventTagChatMedia:
		s.onAlbum(a, &e.ChatMedia)
	case abi.EventTagMedia:
		m := &e.Media
		if !bytes.Equal(m.Chat, s.chatID) {
			return
		}
		if m.HasCard {
			// A card's picture joins the album once it is downloaded.
			if m.Result.Tag == abi.PathResultTagOk {
				s.askAlbum(a)
			}
			return
		}
		s.onMedia(m.Message, &m.Result)
	case abi.EventTagMotion:
		m := &e.Motion
		if bytes.Equal(m.Chat, s.chatID) && bytes.Equal(m.Message, s.cur.id) {
			s.onMotion(&m.Result)
		}
	case abi.EventTagMessageDeleted:
		if bytes.Equal(e.MessageDeleted.Chat, s.chatID) {
			s.forget(e.MessageDeleted.ID)
			s.askAlbum(a)
		}
	case abi.EventTagIncoming:
		// A message that arrives while the viewer is up joins the album.
		if bytes.Equal(e.Incoming.Message.Chat, s.chatID) {
			s.askAlbum(a)
		}
	case abi.EventTagClipboardImage:
		s.onClipboard(a, &e.ClipboardImage)
	case abi.EventTagStickers:
		s.onStickers(&e.Stickers)
	}
}

// askAlbum asks the core for the album around the item on screen
// (Command::LoadChatMedia).
func (s *Screen) askAlbum(a *state.Account) {
	if a.Conn == nil || len(s.chatID) == 0 {
		return
	}
	cmd := a.Cmd(abi.CommandTagLoadChatMedia)
	cmd.LoadChatMedia.Chat = s.chatID
	cmd.LoadChatMedia.Around, cmd.LoadChatMedia.HasAround = s.cur.id, len(s.cur.id) > 0
	a.Send(cmd)
}

// onAlbum takes a record of the album. The records of one batch (chunked
// above 1024 items) share its number: a new number starts the album over.
func (s *Screen) onAlbum(a *state.Account, m *abi.EventChatMedia) {
	if !bytes.Equal(m.Chat, s.chatID) {
		return
	}
	if !s.hasBatch || m.Batch != s.batch {
		s.restartAlbum()
		s.batch, s.hasBatch = m.Batch, true
	}
	first := len(s.album)
	for i := range m.Items {
		mi := &m.Items[i]
		it := item{id: bytes.Clone(mi.ID), card: mi.Card, hasCard: mi.HasCard, video: mi.Video}
		if mi.HasPath {
			it.path = string(mi.Path)
		}
		if mi.HasThumbnail {
			it.blob, it.hasBlob = mi.Thumbnail, true
		}
		s.album = append(s.album, it)
	}
	s.dirty = true
	if first == len(s.album) {
		return
	}
	s.loadThumbs(a.Conn, first)
}

// loadThumbs reads the thumbnails of album[from:] on a worker: the blobs are
// the core's until they are freed.
func (s *Screen) loadThumbs(conn core.Conn, from int) {
	type job struct {
		id      []byte
		card    uint64
		hasCard bool
		ref     abi.BlobRef
	}
	var jobs []job
	for i := from; i < len(s.album); i++ {
		if it := &s.album[i]; it.hasBlob {
			jobs = append(jobs, job{it.id, it.card, it.hasCard, it.blob})
			it.hasBlob = false
		}
	}
	if len(jobs) == 0 || conn == nil {
		return
	}
	gen, out, wake := s.resetGen, &s.th, s.wake
	go func() {
		put := false
		for _, j := range jobs {
			if !out.wanted(gen) {
				// The album or the window went: the blob goes back unread.
				conn.FreeBlob(j.ref.ID)
				continue
			}
			data := readBlob(conn, j.ref)
			put = out.put(thumbDone{gen: gen, id: j.id, card: j.card, hasCard: j.hasCard, data: data}) || put
		}
		if put {
			wake()
		}
	}()
}

// restartAlbum empties the album for another (its thumbnails go with it)
// and turns away what the workers still read for the one before.
func (s *Screen) restartAlbum() {
	clear(s.album)
	s.album, s.hasBatch = s.album[:0], false
	s.resetGen++
	s.th.expect(s.resetGen, s.live)
}

// readBlob reads a whole blob and frees it; nil if it cannot be read.
func readBlob(conn core.Conn, ref abi.BlobRef) []byte {
	defer conn.FreeBlob(ref.ID)
	if ref.Len == 0 || ref.Len > 1<<30 {
		return nil
	}
	buf := make([]byte, ref.Len)
	for off := 0; off < len(buf); {
		n, err := conn.ReadBlob(ref.ID, uint64(off), buf[off:])
		if err != nil || n == 0 {
			return nil
		}
		off += n
	}
	return buf
}

// drainThumbs gives the thumbnails workers have read to their items.
func (s *Screen) drainThumbs() {
	s.thScratch = s.th.take(s.thScratch)
	for _, d := range s.thScratch {
		if d.gen != s.resetGen || d.data == nil {
			continue
		}
		for i := range s.album {
			if it := &s.album[i]; it.is(d.id, d.card, d.hasCard) {
				it.thumb, it.thumbHash = d.data, img.HashBytes(d.data)
				s.dirty = true
				break
			}
		}
	}
	clear(s.thScratch) // a thumbnail no item took is not kept here
	s.thScratch = s.thScratch[:0]
}

// position is where the item on screen sits in the album, -1 if the album
// does not hold it (yet).
func (s *Screen) position() int {
	for i := range s.album {
		if s.album[i].is(s.cur.id, s.cur.card, s.cur.hasCard) {
			return i
		}
	}
	return -1
}

// forget drops a deleted message from the album; when it was the one on
// screen the viewer moves to its neighbour, and closes if the album is empty.
func (s *Screen) forget(id []byte) {
	pos, shown := s.position(), bytes.Equal(id, s.cur.id)
	n := 0
	for i := range s.album {
		if bytes.Equal(s.album[i].id, id) {
			continue
		}
		s.album[n] = s.album[i]
		n++
	}
	if n == len(s.album) {
		return
	}
	clear(s.album[n:])
	s.album = s.album[:n]
	s.dirty = true
	if !shown || pos < 0 {
		return
	}
	if n == 0 {
		s.close(s.e)
		return
	}
	next := &s.album[min(pos, n-1)]
	s.setCur(next.id, next.card, next.hasCard)
}
