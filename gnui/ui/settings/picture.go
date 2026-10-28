package settings

import (
	"gonativeui/img"

	"zapfast/state"
	"zapfast/ui/screen"
)

// picture is our profile picture in the window's image cache: the full one
// when the core has it, else the small one (zf:src/ui/settings.rs, avatar_full
// then avatar).
type picture struct {
	jid    []byte
	gen    uint32
	asked  bool
	path   string
	key    img.Key
	handle img.Handle
}

// sync asks for the picture once and keeps the cache's handle for it. It
// allocates only when a picture arrives or changes.
func (p *picture) sync(e *screen.Env, a *state.Account, jid []byte) {
	if len(jid) == 0 {
		return
	}
	av := &a.Model.Avatars
	if !p.asked || string(p.jid) != string(jid) {
		p.asked, p.jid, p.gen = true, append(p.jid[:0], jid...), 0
		a.FetchAvatar(jid, true)
		a.FetchAvatar(jid, false)
	}
	if g := av.Gen(); p.gen != g || p.gen == 0 {
		p.gen = g
		path, id, ok := av.Picture(jid, true)
		if !ok {
			path, id, ok = av.Picture(jid, false)
		}
		// keyed by the picture's identity: a new picture reuses the last
		// one's file
		switch {
		case !ok || len(path) == 0:
			p.path, p.handle = "", 0
		case string(path) != p.path || id != p.key.Hash:
			if string(path) != p.path {
				p.path = string(path)
			}
			p.key = img.Key{Hash: id}
			p.handle = 0
		}
	}
	if p.path == "" {
		return
	}
	cache := e.K.Images()
	if cache == nil {
		return
	}
	px := int32(avatarPt * e.K.Scale())
	p.key.W, p.key.H, p.key.Flags = px, px, img.Cover
	p.handle, _ = cache.Request(p.key, img.File(p.path))
}
