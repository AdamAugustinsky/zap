package av

import (
	"bytes"
	"time"

	"gonativeui/gpu"
	"gonativeui/media"
	"gonativeui/ui"

	"zapfast/i18n"
	"zapfast/logic"
	"zapfast/model"
	"zapfast/state"
	"zapfast/ui/conversation/mkey"
	"zapfast/ui/screen"
)

// Videos playing in their bubbles (zf:src/video.rs Player, and the video
// actions of zf:src/app.rs: play_video, PlayVideoWhenDownloaded, SeekVideo,
// ToggleVideoSound, CycleVideoSpeed, ExpandVideo), on media.Video.
//
// One clip plays at a time, with sound: starting it stops the voice note
// and ends a voice run, and a voice note started later stops it. A round
// note's first play sends its played receipt. A clip plays only while its
// box shows: scrolled away or under another window it pauses at once, and
// after upstream's 1.5 s unseen it stops and lets its player go. Played to
// its end, it goes back to its poster. The speed (1x, 1.5x, 2x, pitch kept)
// and the sound switch carry over to the next clip until quitting, as
// upstream's do. A clip the system cannot play opens in the system's player
// ("This video opens in your system player").
//
// A downloaded GIF video loops muted while the pointer is over it in the
// focused window (upstream's animation::frame with view.animate and the
// hover), and shows its poster otherwise; a click opens it in the system's
// player.

// Video is what clips play on in their bubbles: a *media.Video, or a test's
// fake. Its methods run on the main thread; State, Duration, Position,
// Playing, Ended and Frame are called every frame and must not allocate.
type Video interface {
	ID() uint64
	State() media.VideoState
	Err() error
	Duration() time.Duration
	Position() time.Duration
	Play()
	Pause()
	Playing() bool
	Ended() bool
	Seek(d time.Duration)
	SetMuted(m bool)
	SetRate(r float32)
	Frame(r *gpu.Renderer, now int64) (tex gpu.TextureID, w, h int, next int64)
	Close()
}

// openVideo opens a clip, paused at its start, loading on a goroutine
// (media.OpenVideo); tests replace it.
var openVideo = func(path string, o media.VideoOptions) (Video, error) {
	v, err := media.OpenVideo(path, o)
	if err != nil {
		return nil, err
	}
	return v, nil
}

// posterSize is the longest side of a clip's poster frame: about twice the
// widest bubble (upstream's MAX_SIDE).
const posterSize = 720

// unseen is how long a clip may go without showing before it stops
// (upstream's UNSEEN). It pauses as soon as it does not show.
const unseen = 1500 * time.Millisecond

// clip is the one video loaded in its bubble (upstream's video::Player
// session): its message, its Video, and whether it should play.
type clip struct {
	acct *state.Account
	chat model.ChatRef
	msg  []byte
	path []byte
	note bool
	v    Video
	// want is upstream's "playing or about to" (is_active): Play was asked
	// and nothing paused it since.
	want bool
	next int64 // retain the last frame deadline if Build omits the clip
	// seen and seenFrame are when its box last showed.
	seen      time.Time
	seenFrame uint32
	// drag is where the seek bar is being dragged to, 0 to 1; -1: it is not.
	drag float32
	// muted is State.Video.Muted as the clip was given it.
	muted bool
}

var vc = clip{drag: -1}

// The video speed and sound switch (VO-004) are State.Video, shared with
// the viewer: kept from one clip to the next until quitting, never saved
// (upstream's video::Player speed and muted).

// is reports whether the loaded clip is message id of the open chat of a.
func (p *clip) is(a *state.Account, id []byte) bool {
	return p.v != nil && p.acct != nil && p.acct == a && a.Open == p.chat && bytes.Equal(p.msg, id)
}

// stopVideo closes the loaded clip (Player::stop): its player, frames and
// sound go.
func stopVideo() {
	if vc.v != nil {
		vc.v.Close()
	}
	vc = clip{msg: vc.msg[:0], path: vc.path[:0], drag: -1}
}

// clipStatus is a block's clip as it stands (video::Status).
type clipStatus struct {
	loaded     bool // the block's clip is the one loaded
	ph         phase
	pos, total time.Duration
}

func (s clipStatus) frac() float32 {
	if s.total <= 0 {
		return 0
	}
	return min(max(float32(float64(s.pos)/float64(s.total)), 0), 1)
}

// clipStatus is the status of the block's clip; not loaded unless it is the
// one in place.
func (b *blk) clipStatus() (st clipStatus) {
	if !vc.is(b.a, b.m.ID()) {
		return st
	}
	v := vc.v
	st.loaded = true
	st.total = v.Duration()
	if st.total <= 0 && b.md.HasSeconds {
		st.total = secs(b.md.Seconds)
	}
	st.pos = min(max(v.Position(), 0), st.total)
	switch {
	case v.State() == media.VideoLoading:
		st.ph = loading
	case v.Playing():
		st.ph = playing
	default:
		st.ph = paused
	}
	return st
}

// playVideo plays or pauses message id's clip in its bubble (play_video):
// one sound at a time, so the voice note stops and its run ends; a clip not
// loaded yet starts from its start. A round note's first play sends its
// played receipt.
func playVideo(s *state.State, a *state.Account, id, path []byte, note bool) {
	stop()
	logic.StopVoiceRun(s)
	waiting.reset()
	starting := !vc.is(a, id)
	switch {
	case starting:
		startVideo(s, a, id, path, note)
	case vc.want:
		vc.v.Pause()
		vc.want = false
	default:
		// the speed and sound the viewer may have switched meanwhile
		vc.v.SetRate(s.Video.Speed())
		vc.v.SetMuted(s.Video.Muted)
		vc.muted = s.Video.Muted
		vc.v.Play() // from the start once it has ended
		vc.want = true
	}
	if starting && note {
		logic.TellPlayed(a, id)
	}
}

// startVideo loads message id's clip and plays it, at the video speed and
// with the sound as last switched.
func startVideo(s *state.State, a *state.Account, id, path []byte, note bool) {
	stopVideo()
	v, err := openVideo(string(path), media.VideoOptions{Muted: s.Video.Muted, PosterSize: posterSize})
	if err != nil {
		unsupported(s, path)
		return
	}
	v.SetRate(s.Video.Speed())
	vc.muted = s.Video.Muted
	v.Play()
	vc.acct, vc.chat, vc.note, vc.v, vc.want = a, a.Open, note, v, true
	vc.msg = append(vc.msg[:0], id...)
	vc.path = append(vc.path[:0], path...)
	vc.seen, vc.seenFrame = s.Now(), ticked.frame
}

// unsupported hands a clip the system cannot play here to its player
// (Notice::Unsupported).
func unsupported(s *state.State, path []byte) {
	s.Toast([]byte(i18n.T("This video opens in your system player")), false)
	openFile(s, string(path))
}

// seekVideo moves the loaded clip to fraction f of it (Player::seek),
// playing or paused as it was.
func seekVideo(f float32) {
	if vc.v == nil {
		return
	}
	if total := vc.v.Duration(); total > 0 {
		vc.v.Seek(time.Duration(float64(total) * float64(min(max(f, 0), 1))))
	}
}

// seekVideoBy moves the loaded clip by d.
func seekVideoBy(d time.Duration) {
	if vc.v == nil {
		return
	}
	if total := vc.v.Duration(); total > 0 {
		vc.v.Seek(min(max(vc.v.Position()+d, 0), total))
	}
}

// cycleVideoSpeed plays at the next of 1x, 1.5x and 2x (CycleVideoSpeed).
func cycleVideoSpeed(s *state.State) {
	s.Video.Rate = supported(nextCycled(s.Video.Speed()))
	if vc.v != nil {
		vc.v.SetRate(s.Video.Rate)
	}
}

// toggleVideoSound mutes or unmutes the clips (ToggleVideoSound).
func toggleVideoSound(s *state.State) {
	s.Video.Muted = !s.Video.Muted
	vc.muted = s.Video.Muted
	if vc.v != nil {
		vc.v.SetMuted(s.Video.Muted)
	}
}

// expandVideo plays message id in the viewer window (ExpandVideo): playing
// at once, from where the bubble's clip stood if it was this one's (else
// from its start), as upstream's one player carries on there. The clip in
// the bubble stops, so that its sound does not carry on under it.
func (b *blk) expandVideo() {
	var at time.Duration
	if vc.is(b.a, b.m.ID()) {
		at = vc.v.Position()
		stopVideo()
	}
	b.e.S.OpenViewerPlayingAt(b.chatRef(), b.m.ID(), at)
}

// shownFrame is the loaded clip's picture this frame (it must be the
// block's clip) while its block shows (vis, from shown); one that does not
// show pauses. A clip that played to its end goes back to its poster (tex
// 0).
func (b *blk) shownFrame(vis bool) (tex gpu.TextureID, w, h int) {
	v := vc.v
	if !vis {
		if vc.want {
			v.Pause()
			vc.want = false
		}
		return 0, 0, 0
	}
	vc.seen, vc.seenFrame = b.e.S.Now(), ticked.frame
	tex, w, h, next := v.Frame(b.e.A.R, b.c.Input().Time)
	vc.next = next
	if next > 0 {
		b.c.WakeIn(next)
	}
	if v.Ended() {
		stopVideo()
		return 0, 0, 0
	}
	return tex, w, h
}

// shown reports whether the box keyed key shows, as the last layout placed
// it (one not laid out yet shows): the window is not hidden, and some of the
// box is inside the window and the clips round it. It watches the box, so a
// layout that reveals or hides it builds it again (Kit.ImageShown's rule,
// on a box that is there whatever is drawn in it). Call it before the box
// is built, every frame it is.
func (b *blk) shown(key ui.Key) bool {
	c := b.c
	if c.Input().Hidden {
		return false
	}
	vis := true
	if id, ok := c.BoxOf(key); ok {
		r := c.ShownRect(id)
		vis = r[2] > r[0] && r[3] > r[1]
	}
	c.WatchShown(key, vis)
	return vis
}

// waiting is a clip clicked before its file was here
// (PlayVideoWhenDownloaded): it plays once the file lands, if its chat is
// still open.
var waiting wanted

type wanted struct {
	acct *state.Account
	chat model.ChatRef
	msg  []byte
}

func (w *wanted) reset() { *w = wanted{msg: w.msg[:0]} }

// playWhenDownloaded remembers message id's clip to play when its file
// lands; a voice run stops waiting for its next note.
func playWhenDownloaded(s *state.State, a *state.Account, id []byte) {
	logic.StopVoiceRun(s)
	waiting.acct, waiting.chat = a, a.Open
	waiting.msg = append(waiting.msg[:0], id...)
}

// gifPlay is the GIF video looping muted under the pointer.
type gifPlay struct {
	acct *state.Account
	key  mkey.K // waveKey of its message
	v    Video
	on   bool  // playing: the pointer is over it
	next int64 // last frame's wake, also owed if the next Build omits the block
	// seen and seenFrame are when it last played on screen.
	seen      time.Time
	seenFrame uint32
	// failed is a GIF the system cannot play: it keeps its badge, and a
	// click opens it in the system's player.
	failed mkey.K
}

var gp gifPlay

func closeGIF() {
	if gp.v != nil {
		gp.v.Close()
	}
	gp = gifPlay{failed: gp.failed}
}

// gifFrame is the GIF's picture while the pointer is over it (over), the
// window has the focus and it shows (vis); tex 0 shows the poster.
func (b *blk) gifFrame(vis, over bool) (tex gpu.TextureID, w, h int) {
	key := waveKey(b.a, b.m.ID())
	if gp.failed == key {
		return 0, 0, 0
	}
	play := vis && over && b.c.Input().Focused
	if gp.v == nil || gp.key != key || gp.acct != b.a {
		if !play {
			return 0, 0, 0
		}
		closeGIF()
		v, err := openVideo(string(b.md.Path), media.VideoOptions{Loop: true, Muted: true, PosterSize: posterSize})
		if err != nil {
			gp.failed = key
			return 0, 0, 0
		}
		gp.acct, gp.key, gp.v = b.a, key, v
	}
	v := gp.v
	if v.State() == media.VideoFailed {
		closeGIF()
		gp.failed = key
		return 0, 0, 0
	}
	if !vis {
		play = false
	}
	switch {
	case play && !gp.on:
		v.Seek(0) // from its first frame, as upstream's restarts
		v.Play()
		gp.on = true
	case !play && gp.on:
		v.Pause()
		gp.on = false
	}
	if !gp.on {
		return 0, 0, 0
	}
	gp.seen, gp.seenFrame = b.e.S.Now(), ticked.frame
	tex, w, h, next := v.Frame(b.e.A.R, b.c.Input().Time)
	gp.next = next
	if next > 0 {
		b.c.WakeIn(next)
	}
	return tex, w, h
}

// gifFailed reports a GIF the system cannot play.
func (b *blk) gifFailed() bool { return gp.failed != (mkey.K{}) && gp.failed == waveKey(b.a, b.m.ID()) }

// viewerWas is whether the viewer window was wanted last frame: opening it
// stops the clip in the chat behind (open_viewer).
var viewerWas bool

// videoFrame keeps the clips going, once per frame (Frame): a clip whose
// account, chat or file is gone stops, one that did not show last frame
// pauses and after a while stops, one the system cannot play goes to its
// player, and a clip clicked before its file was here plays once it is.
func videoFrame(e *screen.Env, a *state.Account, now time.Time) {
	s, c := e.S, e.C()
	frame := ticked.frame
	hidden := c.Input().Hidden
	viewer := s.Win.Want[state.WinViewer]
	if viewer && !viewerWas {
		stopVideo()
	}
	viewerWas = viewer
	if vc.v != nil {
		switch {
		case vc.acct != a || a == nil || a.Open != vc.chat:
			stopVideo()
		case vc.v.State() == media.VideoFailed:
			path := string(vc.path)
			stopVideo()
			s.Toast([]byte(i18n.T("This video opens in your system player")), false)
			openFile(s, path)
		case vc.v.Ended():
			stopVideo()
		case hidden:
			if vc.want {
				vc.v.Pause()
				vc.want = false
			}
			vc.seen, vc.seenFrame = now, frame // nothing shows; it is paused, not gone
		case vc.seenFrame+1 < frame:
			if vc.want {
				vc.v.Pause()
				vc.want = false
			}
			if left := unseen - now.Sub(vc.seen); left <= 0 {
				stopVideo()
			} else {
				c.WakeIn(int64(left + time.Millisecond))
			}
		default:
			if vc.want && vc.next > 0 {
				c.WakeIn(vc.next)
			}
		}
	}
	if gp.v != nil {
		switch {
		case gp.acct != a || a == nil:
			closeGIF()
		case hidden:
			if gp.on {
				gp.v.Pause()
				gp.on = false
			}
			gp.seen, gp.seenFrame = now, frame
		case !gp.on || gp.seenFrame+1 < frame:
			if gp.on {
				gp.v.Pause()
				gp.on = false
			}
			if left := unseen - now.Sub(gp.seen); left <= 0 {
				closeGIF()
			} else {
				c.WakeIn(int64(left + time.Millisecond))
			}
		default:
			// Build may omit the block this frame. Keep its last wake until
			// the next ticker can observe that and pause it.
			if gp.next > 0 {
				c.WakeIn(gp.next)
			}
		}
	}
	motionFrame(c, a, now, frame)
	if waiting.acct != nil {
		if waiting.acct != a || a == nil || a.Open != waiting.chat {
			waiting.reset()
		} else if v := a.Conversation(); v != nil {
			if i := v.Find(waiting.msg); i >= 0 {
				m := v.At(i)
				if md, ok := m.Media(); ok && len(md.Path) > 0 {
					waiting.reset()
					playVideo(s, a, m.ID(), md.Path, md.VideoNote)
				}
			}
		}
	}
}

// videoEvent takes a media event for the clip or the GIF, and reports
// whether it was one of theirs.
func videoEvent(s *state.State, m media.Event) bool {
	switch {
	case vc.v != nil && m.ID == vc.v.ID():
		if m.Kind == media.Ended {
			stopVideo() // back to the poster, ready to play again
			s.Wake()
		}
		return true
	case gp.v != nil && m.ID == gp.v.ID(), mp.v != nil && m.ID == mp.v.ID():
		return true
	}
	return false
}

// videoPlaying reports a clip playing, or about to, with its sound on:
// what keeps other apps' media paused (zf:src/app.rs:8753).
func videoPlaying() bool { return vc.v != nil && vc.want && !vc.muted }
