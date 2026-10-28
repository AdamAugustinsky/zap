package model

import (
	"bytes"
	"encoding/binary"

	"zapfast/abi"
)

// The parts of a message a download is for (File, MarkDownloading). A
// carousel card's picture is the card's index, 0 and up.
const (
	PartMain   = -1 // the picture, video, audio, document or sticker (or an interactive card's image)
	PartMotion = -2 // a motion photo's clip (DownloadMotion)
)

// mediaPart is the part's Media in c, nil when c has none there.
func mediaPart(c *abi.Content, part int) *abi.Media {
	switch {
	case part == PartMain:
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
		case abi.ContentTagInteractive:
			if x := &c.Interactive; x.HasCard && x.Card.HasImage {
				return &x.Card.Image
			}
		}
	case part >= 0:
		if x := &c.Interactive; c.Tag == abi.ContentTagInteractive && x.HasCard && part < len(x.Card.Carousel) && x.Card.Carousel[part].HasImage {
			return &x.Card.Carousel[part].Image
		}
	}
	return nil
}

// setPart puts a download's state on the message's part; false when the
// message has no such part.
func setPart(m *abi.Message, part int, state abi.MediaStateTag, path, errText []byte) bool {
	var p *[]byte
	var has *bool
	var st *abi.MediaState
	if part == PartMotion {
		if m.Content.Tag != abi.ContentTagImage || !m.Content.Image.HasMotion {
			return false
		}
		mo := &m.Content.Image.Motion
		p, has, st = &mo.Path, &mo.HasPath, &mo.State
	} else {
		md := mediaPart(&m.Content, part)
		if md == nil {
			return false
		}
		p, has, st = &md.Path, &md.HasPath, &md.State
	}
	*st = abi.MediaState{Tag: state}
	switch state {
	case abi.MediaStateTagFailed:
		st.Failed.Error = errText
	case abi.MediaStateTagIdle:
		if len(path) > 0 {
			*p, *has = path, true
		}
	}
	return true
}

// carryMedia keeps, in an updated message, the download states (and files)
// the old copy had: the core's update knows nothing of them
// (zf:src/app.rs:3489-3531).
func carryMedia(dst, old *abi.Message) {
	carry := func(n, o *abi.Media) {
		if n == nil || o == nil {
			return
		}
		n.State = o.State
		if !n.HasPath && o.HasPath {
			n.Path, n.HasPath = o.Path, true
		}
	}
	carry(mediaPart(&dst.Content, PartMain), mediaPart(&old.Content, PartMain))
	if dst.Content.Tag == abi.ContentTagInteractive && old.Content.Tag == abi.ContentTagInteractive {
		for i := range min(len(dst.Content.Interactive.Card.Carousel), len(old.Content.Interactive.Card.Carousel)) {
			carry(mediaPart(&dst.Content, i), mediaPart(&old.Content, i))
		}
	}
	if n, o := &dst.Content, &old.Content; n.Tag == abi.ContentTagImage && o.Tag == abi.ContentTagImage && n.Image.HasMotion && o.Image.HasMotion {
		n.Image.Motion.State = o.Image.Motion.State
		if !n.Image.Motion.HasPath && o.Image.Motion.HasPath {
			n.Image.Motion.Path, n.Image.Motion.HasPath = o.Image.Motion.Path, true
		}
	}
}

// carries reports whether a row can hold download state worth carrying.
func (r *msgRec) carries() bool {
	return r.flags&msgHasMedia != 0 || r.kind == abi.ContentTagInteractive
}

// setMedia applies a download's state to the loaded message's part; false
// when the message is not loaded or has no such part.
func (v *Conversation) setMedia(id []byte, part int, state abi.MediaStateTag, path, errText []byte) bool {
	i := v.Find(id)
	if i < 0 {
		return false
	}
	d := &v.edit
	defer d.Release()
	if d.Decode(v.a.at(v.recs[i].raw)) != nil || !setPart(d, part, state, path, errText) {
		return false
	}
	v.upsert(d)
	return true
}

// forgetFiles drops the downloaded files under dir (all, when dir is empty)
// from the loaded messages: the media cache was cleared, and what is on
// screen downloads again rather than drawing as broken.
func (v *Conversation) forgetFiles(dir []byte) {
	for i := range v.recs {
		if !v.recs[i].carries() {
			continue
		}
		d := &v.edit
		if d.Decode(v.a.at(v.recs[i].raw)) != nil {
			continue
		}
		changed := false
		drop := func(p *[]byte, has *bool) {
			if *has && bytes.HasPrefix(*p, dir) {
				*p, *has = nil, false
				changed = true
			}
		}
		if md := mediaPart(&d.Content, PartMain); md != nil {
			drop(&md.Path, &md.HasPath)
		}
		if d.Content.Tag == abi.ContentTagImage && d.Content.Image.HasMotion {
			drop(&d.Content.Image.Motion.Path, &d.Content.Image.Motion.HasPath)
		}
		if d.Content.Tag == abi.ContentTagInteractive {
			for j := range d.Content.Interactive.Card.Carousel {
				if md := mediaPart(&d.Content, j); md != nil {
					drop(&md.Path, &md.HasPath)
				}
			}
		}
		if changed {
			v.upsert(d)
		}
	}
	v.edit.Release()
}

// files are the downloads the core reported, by (chat, message, part),
// whether or not the message is loaded: the viewer's album and the info
// panel's listings hold messages the transcript has not paged in
// (zf:src/app.rs:4835).
type files struct {
	m   kv
	val []byte
}

func (f *files) key(chat, id []byte, part int) []byte {
	k := f.m.pair(chat, id)
	k = binary.LittleEndian.AppendUint32(append(k, 0), uint32(int32(part)))
	f.m.kbuf = k
	return k
}

func (f *files) set(chat, id []byte, part int, state abi.MediaStateTag, b []byte) {
	f.val = append(append(f.val[:0], byte(state)), b...)
	f.m.set(f.key(chat, id, part), f.val)
}

// File is what the store knows of a download: state Idle with the file's
// path once it is there, Downloading, or Failed with the core's reason; ok
// is false when nothing was asked or reported. part is PartMain, PartMotion
// or a carousel card's index.
func (s *Store) File(chat, msg []byte, part int) (path []byte, state abi.MediaStateTag, errText []byte, ok bool) {
	v, ok := s.files.m.get(s.files.key(chat, msg, part))
	if !ok || len(v) == 0 {
		return nil, 0, nil, false
	}
	state = abi.MediaStateTag(v[0])
	if state == abi.MediaStateTagFailed {
		return nil, state, v[1:], true
	}
	return v[1:], state, nil, true
}

// FilesGen moves whenever a download starts, ends or fails.
func (s *Store) FilesGen() uint32 { return s.files.m.gen }

// MarkDownloading records that a download was asked for (Download or
// DownloadMotion): the bubble and the viewer show it in progress until the
// core's Media or Motion event. Account.Download sends and marks.
func (s *Store) MarkDownloading(chat, msg []byte, part int) {
	s.files.set(chat, msg, part, abi.MediaStateTagDownloading, nil)
	if v := s.Conversation(chat); v != nil {
		v.setMedia(msg, part, abi.MediaStateTagDownloading, nil, nil)
	}
	s.gen++
}

func (s *Store) mediaResult(chat, msg []byte, part int, r *abi.PathResult) {
	state, b := abi.MediaStateTagIdle, r.Ok.Path
	if r.Tag != abi.PathResultTagOk {
		state, b = abi.MediaStateTagFailed, r.Err.Error
	}
	s.files.set(chat, msg, part, state, b)
	if v := s.Conversation(chat); v != nil {
		if state == abi.MediaStateTagFailed {
			v.setMedia(msg, part, state, nil, b)
		} else {
			v.setMedia(msg, part, state, b, nil)
		}
	}
}

// Storage is Settings → Storage's numbers (zf:src/app.rs:3070-3099). Its
// screen asks with Ask or Clear, which give the token to send in
// LoadStorageStats or ClearMediaCache; only the answer to the latest counts.
type Storage struct {
	Stats    abi.StorageStats
	Has      bool   // Stats were measured once
	Token    uint64 // the request being waited for
	Clearing bool   // ClearMediaCache sent, not answered yet
	gen      uint32
}

// Ask is the token of a new LoadStorageStats.
func (st *Storage) Ask() uint64 { st.Token++; st.gen++; return st.Token }

// Clear is the token of a new ClearMediaCache.
func (st *Storage) Clear() uint64 { st.Token++; st.Clearing = true; st.gen++; return st.Token }

// Gen moves whenever the numbers or the state change.
func (st *Storage) Gen() uint32 { return st.gen }

func (s *Store) storageEvent(e *abi.EventStorageStats) {
	if e.Cleared {
		for i := range s.convs {
			if v := &s.convs[i]; v.inUse {
				v.forgetFiles(s.CacheDir)
			}
		}
		gone := &s.forgotten
		gone.Reset()
		s.files.m.each(func(k, v []byte) bool {
			if len(v) > 1 && abi.MediaStateTag(v[0]) == abi.MediaStateTagIdle && bytes.HasPrefix(v[1:], s.CacheDir) {
				gone.b = append(gone.b, k...)
				gone.ends = append(gone.ends, uint32(len(gone.b)))
			}
			return true
		})
		for i := range gone.Len() {
			s.files.m.del(gone.At(i))
		}
		gone.Reset()
	}
	st := &s.Storage
	if e.Token == st.Token {
		if e.Cleared {
			st.Clearing = false
		}
		if e.HasStats {
			st.Stats, st.Has = e.Stats, true
		}
		st.gen++
	}
}

// CallUpdate is the account's call as the core last reported it (one at a
// time), nil before any: a view of Answer(EventTagCall), valid until the
// next apply. ui/calls decides what to show (zf:src/app/calls.rs:259).
func (s *Store) CallUpdate() *abi.CallUpdate {
	if e := s.Answer(abi.EventTagCall).Event(); e != nil {
		return &e.Call.Update
	}
	return nil
}

// CallLive reports whether a call phase is under way (zf:src/calls.rs:100).
func CallLive(p abi.CallPhase) bool { return p < abi.CallPhaseEnded }
