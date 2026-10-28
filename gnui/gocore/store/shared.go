package store

import (
	"strings"
)

// GalleryPage is how many items a page of a chat's media holds (upstream's
// GALLERY_PAGE).
const GalleryPage = 200

// Link is one web link a chat's message holds.
type Link struct {
	// Message is the id of the message that holds it.
	Message   string
	Timestamp int64
	URL       string
	// Title is the link card's, when the message has one for this link.
	Title string
}

// Shared is what a chat's info panel files under media, documents and
// links, each newest first.
type Shared struct {
	// Media are the pictures and clips (not GIFs).
	Media []Message
	// Docs are the documents.
	Docs  []Message
	Links []Link
	// The archive held more of a kind than the limit.
	MediaTruncated, DocsTruncated, LinksTruncated bool
}

// Shared lists one chat's media, documents and links, at most limit of
// each (GalleryPage when limit <= 0).
func (s *Archive) Shared(chat string, limit int) (Shared, error) {
	if limit <= 0 {
		limit = GalleryPage
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	var sh Shared
	var err error
	if sh.Media, sh.MediaTruncated, err = s.ofKinds(chat, limit, KindImage, KindVideo); err != nil {
		return Shared{}, err
	}
	if sh.Docs, sh.DocsTruncated, err = s.ofKinds(chat, limit, KindDocument); err != nil {
		return Shared{}, err
	}
	rows, err := s.db.Query(`SELECT id,ts,url,title FROM message_links WHERE chat=?
ORDER BY ts DESC,ord DESC,id DESC,rowid LIMIT ?`, chat, limit+1)
	if err != nil {
		return Shared{}, err
	}
	defer rows.Close()
	for rows.Next() {
		var l Link
		if err = rows.Scan(&l.Message, &l.Timestamp, &l.URL, &l.Title); err != nil {
			return Shared{}, err
		}
		sh.Links = append(sh.Links, l)
	}
	if err = rows.Err(); err != nil {
		return Shared{}, err
	}
	if len(sh.Links) > limit {
		sh.Links, sh.LinksTruncated = sh.Links[:limit], true
	}
	return sh, nil
}

// ofKinds lists a chat's newest messages of the kinds, at most limit, and
// whether there were more.
func (s *Archive) ofKinds(chat string, limit int, kinds ...string) ([]Message, bool, error) {
	args := []any{chat}
	for _, k := range kinds {
		args = append(args, k)
	}
	args = append(args, limit+1)
	rows, err := s.db.Query(`SELECT m.payload,m.raw FROM messages m JOIN message_flags f ON f.chat=m.chat AND f.id=m.id
WHERE m.chat=? AND f.kind IN (?`+strings.Repeat(",?", len(kinds)-1)+`)
ORDER BY m.ts DESC,m.ord DESC,m.id DESC LIMIT ?`, args...)
	if err != nil {
		return nil, false, err
	}
	defer rows.Close()
	var list []Message
	for rows.Next() {
		m, err := scanMessage(rows)
		if err != nil {
			return nil, false, err
		}
		list = append(list, m)
	}
	if err = rows.Err(); err != nil {
		return nil, false, err
	}
	if len(list) > limit {
		return list[:limit], true, nil
	}
	return list, false, nil
}

// ChatMedia is a page of one chat's pictures and clips, oldest first, for
// the viewer: the newest limit when around is empty or not in the album,
// else the items around that message, half of them after it, so stepping
// works both ways from where the viewer opened.
func (s *Archive) ChatMedia(chat, around string, limit int) ([]Message, error) {
	if limit <= 0 {
		limit = GalleryPage
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	const media = `SELECT m.payload,m.raw FROM messages m JOIN message_flags f ON f.chat=m.chat AND f.id=m.id
WHERE m.chat=? AND f.kind IN ('` + KindImage + `','` + KindVideo + `')`
	var ts, ord int64
	anchored := false
	if around != "" {
		err := s.db.QueryRow(`SELECT m.ts,m.ord FROM messages m JOIN message_flags f ON f.chat=m.chat AND f.id=m.id
WHERE m.chat=? AND m.id=? AND f.kind IN ('`+KindImage+`','`+KindVideo+`')`, chat, around).Scan(&ts, &ord)
		anchored = err == nil
	}
	ahead := 0
	if anchored {
		ahead = limit / 2
	}
	behindQuery := media + " ORDER BY m.ts DESC,m.ord DESC,m.id DESC LIMIT ?"
	behindArgs := []any{chat, limit - ahead}
	if anchored {
		behindQuery = media + " AND (m.ts<? OR (m.ts=? AND m.ord<?) OR (m.ts=? AND m.ord=? AND m.id<=?)) ORDER BY m.ts DESC,m.ord DESC,m.id DESC LIMIT ?"
		behindArgs = []any{chat, ts, ts, ord, ts, ord, around, limit - ahead}
	}
	list, err := s.messages(behindQuery, behindArgs...)
	if err != nil {
		return nil, err
	}
	for i, j := 0, len(list)-1; i < j; i, j = i+1, j-1 {
		list[i], list[j] = list[j], list[i]
	}
	if ahead > 0 {
		newer, err := s.messages(media+" AND (m.ts>? OR (m.ts=? AND m.ord>?) OR (m.ts=? AND m.ord=? AND m.id>?)) ORDER BY m.ts,m.ord,m.id LIMIT ?",
			chat, ts, ts, ord, ts, ord, around, ahead)
		if err != nil {
			return nil, err
		}
		list = append(list, newer...)
	}
	return list, nil
}

func (s *Archive) messages(query string, args ...any) ([]Message, error) {
	rows, err := s.db.Query(query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var list []Message
	for rows.Next() {
		m, err := scanMessage(rows)
		if err != nil {
			return nil, err
		}
		list = append(list, m)
	}
	return list, rows.Err()
}

// UnreadMention is the oldest of a chat's unread newest incoming messages
// that mentions us, under any id in ours (the phone JID and the privacy
// JID), or everyone; "" for none. As src/archive.rs unread_mention.
func (s *Archive) UnreadMention(chat string, unread int, ours []string) (string, error) {
	if unread <= 0 {
		return "", nil
	}
	cond := "mm.jid=?"
	args := []any{chat, unread, MentionAll}
	for _, id := range ours {
		user, _, _ := strings.Cut(id, "@")
		cond += " OR mm.jid=? OR mm.user=?"
		args = append(args, id, user)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	ids, err := queryStrings(s.db, `SELECT r.id FROM (
SELECT m.chat,m.id,m.ts,m.ord FROM messages m JOIN message_flags f ON f.chat=m.chat AND f.id=m.id
WHERE m.chat=? AND f.from_me=0 ORDER BY m.ts DESC,m.ord DESC,m.id DESC LIMIT ?) r
WHERE EXISTS(SELECT 1 FROM message_mentions mm WHERE mm.chat=r.chat AND mm.id=r.id AND (`+cond+`))
ORDER BY r.ts,r.ord,r.id LIMIT 1`, args...)
	if err != nil || len(ids) == 0 {
		return "", err
	}
	return ids[0], nil
}
