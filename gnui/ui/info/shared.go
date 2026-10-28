package info

import (
	"bytes"
	"slices"
	"time"

	"zapfast/abi"
	"zapfast/core"
	"zapfast/i18n"
	"zapfast/model"
)

// span is a range of shared.buf.
type span struct{ off, n uint32 }

// mediaItem is one picture or video of the listing.
type mediaItem struct {
	chat, msg   span
	ts          int64
	video       bool
	seconds     uint32
	hasSeconds  bool
	path        span
	thumb       abi.BlobRef
	hasThumb    bool
	thumbTried  bool
	label       span // "Photo, 14:05"
	durationTxt span // "0:12" for a video
	caption     span
}

// docItem is one document.
type docItem struct {
	chat, msg    span
	ts           int64
	name, detail span
	label        span // "name, detail"
	caption      span
}

// linkItem is one web link.
type linkItem struct {
	msg          span
	ts           int64
	url          span
	title        span // the preview's title, else the address
	detail       span // "site · 14:05"
	label        span
	hasTitle     bool
	invite, safe bool
}

// shared is the panel's listing of the chat's pictures, documents and links
// (ListChatShared / ChatShared), in one reused buffer.
type shared struct {
	buf   []byte
	media []mediaItem
	docs  []docItem
	links []linkItem
	cut   [3]bool // the archive held more: media, docs, links

	chat    [128]byte // the chat the listing is of
	chatN   int
	request uint64 // the request being waited for / answered
	pending bool   // a request is out
	failed  bool
	stale   bool // the chat changed while a request was out: ask again
	loaded  bool // a listing is in
	asked   bool // a request was made for chat
	gen     uint32

	counts [3]int
}

func (l *shared) put(b []byte) span {
	off := len(l.buf)
	l.buf = append(l.buf, b...)
	return span{uint32(off), uint32(len(b))}
}

func (l *shared) putS(s string) span {
	off := len(l.buf)
	l.buf = append(l.buf, s...)
	return span{uint32(off), uint32(len(s))}
}

func (l *shared) bytes(s span) []byte { return l.buf[s.off : s.off+s.n : s.off+s.n] }
func (l *shared) str(s span) string   { return model.S(l.bytes(s)) }

func (l *shared) forChat(jid []byte) bool { return bytes.Equal(l.chat[:l.chatN], jid) }

// reset drops the listing (and frees its blobs through conn) and makes it
// the listing of jid, nothing asked yet.
func (l *shared) reset(conn core.Conn, jid []byte) {
	if conn != nil {
		for i := range l.media {
			if m := &l.media[i]; m.hasThumb {
				conn.FreeBlob(m.thumb.ID)
			}
		}
	}
	l.buf = l.buf[:0]
	l.media, l.docs, l.links = l.media[:0], l.docs[:0], l.links[:0]
	l.cut = [3]bool{}
	l.chatN = copy(l.chat[:], jid)
	l.pending, l.failed, l.stale, l.loaded, l.asked = false, false, false, false, false
	l.gen++
}

// begin starts collecting the answer to a new request.
func (l *shared) begin(conn core.Conn) {
	for i := range l.media {
		if m := &l.media[i]; m.hasThumb && conn != nil {
			conn.FreeBlob(m.thumb.ID)
		}
	}
	l.buf = l.buf[:0]
	l.media, l.docs, l.links = l.media[:0], l.docs[:0], l.links[:0]
	l.cut = [3]bool{}
}

// take adds a chunk of the answer. The event's bytes are copied.
func (l *shared) take(conn core.Conn, e *abi.EventChatShared) {
	if e.Batch == 0 {
		l.begin(conn)
	}
	for i := range e.Entries {
		en := &e.Entries[i]
		switch en.Tag {
		case abi.SharedEntryTagMedia:
			m := &en.Media.Message
			it := mediaItem{chat: l.put(m.Chat), msg: l.put(m.ID), ts: m.Timestamp}
			switch m.Content.Tag {
			case abi.ContentTagVideo:
				v := &m.Content.Video
				it.video, it.seconds, it.hasSeconds = true, v.Seconds, v.HasSeconds
				if v.Media.HasPath {
					it.path = l.put(v.Media.Path)
				}
				if v.HasCaption {
					it.caption = l.put(v.Caption)
				}
			case abi.ContentTagImage:
				im := &m.Content.Image
				if im.Media.HasPath {
					it.path = l.put(im.Media.Path)
				}
				if im.HasCaption {
					it.caption = l.put(im.Caption)
				}
			}
			if m.HasThumbnail {
				it.thumb, it.hasThumb = m.Thumbnail, true
			}
			l.media = append(l.media, it)
		case abi.SharedEntryTagDoc:
			m := &en.Doc.Message
			d := &m.Content.Document
			it := docItem{chat: l.put(m.Chat), msg: l.put(m.ID), ts: m.Timestamp, name: l.put(d.FileName)}
			if d.HasCaption {
				it.caption = l.put(d.Caption)
			}
			// the detail needs the size and pages: kept in the record's detail
			// span until finish() writes the text
			it.detail = span{uint32(d.Media.Size), 0}
			if d.HasPages {
				it.detail.n = d.Pages + 1
			}
			l.docs = append(l.docs, it)
		case abi.SharedEntryTagLink:
			k := &en.Link
			it := linkItem{msg: l.put(k.Message), ts: k.Timestamp, url: l.put(k.URL)}
			if k.HasTitle && len(k.Title) > 0 {
				it.title, it.hasTitle = l.put(k.Title), true
			}
			l.links = append(l.links, it)
		}
	}
	if e.Last {
		l.cut = [3]bool{e.MediaTruncated, e.DocsTruncated, e.LinksTruncated}
	}
}

// finish writes the display strings once the listing is whole: newest first,
// each row's label and detail.
func (l *shared) finish(now time.Time) {
	slices.SortStableFunc(l.media, func(a, b mediaItem) int { return cmpDesc(a.ts, b.ts) })
	slices.SortStableFunc(l.docs, func(a, b docItem) int { return cmpDesc(a.ts, b.ts) })
	slices.SortStableFunc(l.links, func(a, b linkItem) int { return cmpDesc(a.ts, b.ts) })
	for i := range l.media {
		m := &l.media[i]
		what := i18n.T("Photo")
		if m.video {
			what = i18n.T("Video")
		}
		m.label = l.putS(what + ", " + chatStamp(now, m.ts) + "###" + string(l.bytes(m.msg)))
		if m.video && m.hasSeconds {
			m.durationTxt = l.putS(duration(m.seconds))
		}
	}
	for i := range l.docs {
		d := &l.docs[i]
		size, pages, hasPages := uint64(d.detail.off), d.detail.n, d.detail.n > 0
		var det string
		if hasPages {
			det = i18n.Format(i18n.N("{} page", "{} pages", int(pages-1)), "{}", itoa(int(pages-1))) + " · "
		}
		det += bytesText(size) + " · " + chatStamp(now, d.ts)
		d.detail = l.putS(det)
		d.label = l.putS(l.str(d.name) + ", " + det + "###" + string(l.bytes(d.msg)))
	}
	for i := range l.links {
		k := &l.links[i]
		u := l.str(k.url)
		if !k.hasTitle {
			k.title = k.url
		}
		det := domain(u) + " · " + chatStamp(now, k.ts)
		k.detail = l.putS(det)
		k.label = l.putS(l.str(k.title) + ", " + det + "###" + string(l.bytes(k.msg)))
		k.safe = externalURL(u) != "" || inviteCode(u) != ""
		k.invite = inviteCode(u) != ""
	}
	l.counts = [3]int{len(l.media), len(l.docs), len(l.links)}
	l.gen++
}

func cmpDesc(a, b int64) int {
	switch {
	case a > b:
		return -1
	case a < b:
		return 1
	}
	return 0
}

func itoa(n int) string {
	var b [20]byte
	i := len(b)
	neg := n < 0
	if neg {
		n = -n
	}
	for {
		i--
		b[i] = byte('0' + n%10)
		n /= 10
		if n == 0 {
			break
		}
	}
	if neg {
		i--
		b[i] = '-'
	}
	return string(b[i:])
}
