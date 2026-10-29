package av

import (
	"bytes"
	"time"

	"gonativeui/gpu"
	"gonativeui/media"
	"gonativeui/theme"
	"gonativeui/ui"
	"gonativeui/widget/base"

	"zapfast/abi"
	"zapfast/model"
	"zapfast/state"
	"zapfast/ui/screen"
)

// Motion photos (zf:src/ui/conversation.rs motion_clip, Action::ToggleMotion):
// a click on the photo's badge (package kinds draws it) plays its clip over
// the photo, muted and looping, and a second click goes back to the photo.
// One plays at a time, in the open chat. A clip not downloaded yet
// downloads first (DownloadMotion) and plays once it is here; one whose
// download fails is let go. It plays while the window has the focus
// (upstream's view.animate) and holds its frame otherwise. Like the clips,
// it pauses when its photo does not show, and after 1.5 s unseen its
// player goes (it opens again when the photo shows). A clip the system
// cannot play opens in the system's player.

// MotionState is what a motion photo's badge shows (motion_clip's icon).
type MotionState uint8

const (
	// MotionStill: the photo shows; the badge plays the clip.
	MotionStill MotionState = iota
	// MotionPlaying: the clip plays over the photo; the badge pauses it.
	MotionPlaying
	// MotionWaiting: the clip is wanted, and downloads or loads: a spinner.
	MotionWaiting
)

// motionPlay is the motion photo whose clip plays (upstream's
// motion_playing), and its player once the file is here.
type motionPlay struct {
	acct *state.Account
	chat model.ChatRef
	msg  []byte
	v    Video
	on   bool // playing (else holding its frame)
	// seen and seenFrame are when its photo last showed.
	seen      time.Time
	seenFrame uint32
}

var mp motionPlay

func (p *motionPlay) is(a *state.Account, id []byte) bool {
	return p.acct != nil && p.acct == a && a.Open == p.chat && bytes.Equal(p.msg, id)
}

// stopMotion goes back to the photo.
func stopMotion() {
	releaseMotion()
	mp = motionPlay{msg: mp.msg[:0]}
}

// releaseMotion lets the clip's player go; it stays wanted.
func releaseMotion() {
	if mp.v != nil {
		mp.v.Close()
	}
	mp.v, mp.on = nil, false
}

// ToggleMotion plays message m's motion clip over its photo, or goes back
// to the photo (Action::ToggleMotion). A clip not downloaded yet downloads
// first and plays once it is here.
func ToggleMotion(e *screen.Env, m model.Message) {
	a := e.Acct()
	if a == nil {
		return
	}
	if mp.is(a, m.ID()) {
		stopMotion()
		return
	}
	stopMotion()
	b := use(e, m)
	d := b.decoded()
	if d.Content.Tag != abi.ContentTagImage || !d.Content.Image.HasMotion {
		return
	}
	mp.acct, mp.chat = a, a.Open
	mp.msg = append(mp.msg[:0], m.ID()...)
	mp.seen, mp.seenFrame = e.S.Now(), ticked.frame
	if mo := &d.Content.Image.Motion; !hasMotionFile(mo) && mo.State.Tag != abi.MediaStateTagDownloading {
		a.Download(d.Chat, m.ID(), model.PartMotion, true)
	}
	e.C().WakeIn(1)
}

// Motion draws message m's motion clip over its photo while it plays: in r
// (the photo's box, in the coordinates of the box being built), on black,
// fitted in it, with the photo's corner radius. It reports what the badge
// shows. kinds calls it after the photo and before the badge, every frame
// the photo is built; for a photo whose clip is not wanted it does nothing.
func Motion(e *screen.Env, m model.Message, r screen.Rect, radius float32) MotionState {
	if !mp.is(e.Acct(), m.ID()) {
		return MotionStill
	}
	Frame(e)
	if !mp.is(e.Acct(), m.ID()) {
		return MotionStill
	}
	b := use(e, m)
	d := b.decoded()
	if d.Content.Tag != abi.ContentTagImage || !d.Content.Image.HasMotion {
		stopMotion()
		return MotionStill
	}
	mo := &d.Content.Image.Motion
	if !hasMotionFile(mo) {
		switch mo.State.Tag {
		case abi.MediaStateTagDownloading:
			return MotionWaiting
		case abi.MediaStateTagFailed:
			stopMotion() // the badge says why
		}
		return MotionStill
	}
	if mp.v == nil {
		v, err := openVideo(string(mo.Path), media.VideoOptions{Loop: true, Muted: true, PosterSize: posterSize})
		if err != nil {
			stopMotion()
			openFile(e.S, string(mo.Path))
			return MotionStill
		}
		mp.v = v
	}
	v := mp.v
	if v.State() == media.VideoFailed { // no player here plays it: the system's gets it
		stopMotion()
		openFile(e.S, string(mo.Path))
		return MotionStill
	}
	c := b.c
	key := ui.KeyN(ui.HashString(c.ParentKey(), "avmotion"), idHash(m.ID()))
	vis := b.shown(key)
	theme.At(c, r.X, r.Y, r.W, r.H)
	c.Push(c.BoxKeyed(base.Floating, key, nil))
	play := vis && c.Input().Focused
	switch {
	case play && !mp.on:
		v.Play()
		mp.on = true
	case !play && mp.on:
		v.Pause()
		mp.on = false
	}
	var (
		tex  gpu.TextureID
		w, h int
	)
	if vis {
		mp.seen, mp.seenFrame = e.S.Now(), ticked.frame
		var next int64
		tex, w, h, next = v.Frame(e.A.R, c.Input().Time)
		if next > 0 {
			c.WakeIn(next)
		}
	}
	if tex != 0 {
		b.fillBox(2, 0, 0, r.W, r.H, radius, 0x000000FF)
		b.frame(1, tex, w, h, 0, 0, r.W, r.H, radius, ui.FitContain)
	}
	c.Pop()
	if tex == 0 && v.State() == media.VideoLoading {
		return MotionWaiting
	}
	return MotionPlaying
}

// hasMotionFile reports a clip that is here. Path alone is not enough: the
// decoded message is a reused scratch, which keeps the last one's Path when
// this one has none.
func hasMotionFile(mo *abi.Motion) bool { return mo.HasPath && len(mo.Path) > 0 }

// motionFrame keeps the motion clip in step, once per frame (Frame): it
// goes with its account or chat, pauses when its photo did not show last
// frame, and lets its player go after a while unseen.
func motionFrame(c *ui.Context, a *state.Account, now time.Time, frame uint32) {
	if mp.acct == nil {
		return
	}
	switch {
	case mp.acct != a || a == nil || !shown(a, mp.chat):
		stopMotion()
	case mp.v == nil:
	case c.Input().Hidden:
		mp.seen, mp.seenFrame = now, frame
	case mp.seenFrame+1 < frame:
		if mp.on {
			mp.v.Pause()
			mp.on = false
		}
		if left := unseen - now.Sub(mp.seen); left <= 0 {
			releaseMotion()
		} else {
			c.WakeIn(int64(left + time.Millisecond))
		}
	}
}
