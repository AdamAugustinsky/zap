package screen

import (
	"gonativeui/img"

	"zapfast/state"
)

// Avatar is jid's picture, px pixels square, from the window's image cache:
// the large one with full while there is one, else the small one. A
// picture there is none of yet is asked for (once); 0 until it is ready.
func Avatar(cache *img.Cache, a *state.Account, jid []byte, full bool, px int32) img.Handle {
	path, id, ok := a.Model.Avatars.Picture(jid, full)
	if !ok && full {
		path, id, ok = a.Model.Avatars.Picture(jid, false)
	}
	if !ok || len(path) == 0 {
		a.FetchAvatar(jid, full)
		return 0
	}
	if cache == nil {
		return 0
	}
	// keyed by the picture's identity: a new picture reuses the last one's file
	h, _ := cache.Request(img.Key{Hash: id, W: px, H: px, Flags: img.Cover}, img.File(string(path)))
	return h
}
