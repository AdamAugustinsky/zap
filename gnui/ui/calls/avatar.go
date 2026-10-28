package calls

import (
	"gonativeui/img"

	"zapfast/state"
	"zapfast/ui/screen"
)

// The pictures of the people a screen shows: the profile picture of a call's
// person on the ringing and live screens, in the group's tiles and in the
// history's rows. Each screen keeps its own, because an image cache belongs
// to a window (e.K.Images()), and shows the initials until a picture is in.

// bigPt is the side from which the large picture is asked for.
const bigPt = 64

// pic is one person's picture file and its place in the cache.
type pic struct {
	path string
	key  img.Key
	gen  uint32
}

// pics are the pictures a screen has asked for, by JID: the small one for
// avatars up to bigPt points, the large one above.
type pics struct {
	acct         uint64 // the account's Serial: a relinked number asks again
	thumb, large map[string]*pic
}

// reset forgets every picture (the window's image cache is new).
func (p *pics) reset() {
	p.acct, p.thumb, p.large = 0, nil, nil
}

// of is the handle of jid's picture shown sizePt points wide: the zero
// handle (the initials) until the core has one. It asks the core for the
// picture once, and requests it from the window's image cache each frame, as
// the chat list does.
func (p *pics) of(e *screen.Env, a *state.Account, jid []byte, sizePt float32) img.Handle {
	cache := e.K.Images()
	if cache == nil || len(jid) == 0 || a == nil {
		return 0
	}
	if p.acct != a.Serial || p.thumb == nil {
		p.acct, p.thumb, p.large = a.Serial, map[string]*pic{}, map[string]*pic{}
	}
	big := sizePt > bigPt
	m := p.thumb
	if big {
		m = p.large
	}
	pc := m[string(jid)]
	if pc == nil {
		pc = &pic{gen: ^uint32(0)}
		m[string(jid)] = pc
		a.FetchAvatar(jid, big)
	}
	if g := a.Model.Avatars.Gen(); pc.gen != g {
		pc.gen = g
		path, id, ok := a.Model.Avatars.Picture(jid, big)
		switch {
		case !ok:
			pc.path = ""
		default:
			if string(path) != pc.path {
				pc.path = string(path)
			}
			// the picture's identity: a new one reuses the last one's file
			pc.key.Hash = id
		}
	}
	if pc.path == "" {
		return 0
	}
	px := int32(sizePt*e.K.Scale() + 0.5)
	pc.key.W, pc.key.H, pc.key.Flags = px, px, img.Cover
	h, _ := cache.Request(pc.key, img.File(pc.path))
	return h
}
