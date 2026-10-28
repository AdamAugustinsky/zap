package av

import (
	"bytes"
	"errors"
	"strings"
	"sync"
	"time"

	"gonativeui/app"
	"gonativeui/media"
	"gonativeui/ui"

	"zapfast/i18n"
	"zapfast/logic"
	"zapfast/model"
	"zapfast/state"
	"zapfast/ui/conversation/av/thumbnail"
	"zapfast/ui/screen"
)

// Player is what voice notes and audio files play on: a *media.Player, or a
// test's fake. Every method runs on the main thread; Ready, Err, Position
// and Playing are read every frame and must not allocate.
type Player interface {
	ID() uint64
	// Ready reports a Player that can play: always one of PCM, and one of a
	// file once the file has opened (media.OpenPlayer).
	Ready() bool
	// Err is why a file did not open, else nil.
	Err() error
	Duration() time.Duration
	Play() error
	Pause()
	Playing() bool
	Position() time.Duration
	Seek(t time.Duration)
	SetRate(r float32)
	Close()
}

// newPlayer makes the Player of a decoded clip (media.NewPlayer: about 8 ms
// on the main thread once the audio stack is warm, which ui/composer sees to
// on an open chat's first frame); tests replace it.
var newPlayer = func(pcm []byte, f media.Format) (Player, error) {
	p, err := media.NewPlayer(pcm, f)
	if err != nil {
		return nil, err
	}
	return p, nil
}

// openPlayer makes the Player of an audio file the core does not decode (an
// MP3, AAC or M4A attachment): it returns at once and opens the file on a
// goroutine (media.OpenPlayer); tests replace it.
var openPlayer = func(path string) (Player, error) {
	p, err := media.OpenPlayer(path)
	if err != nil {
		return nil, err
	}
	return p, nil
}

// phase is a clip's state (upstream's audio::State).
type phase uint8

const (
	idle    phase = iota // not loaded, or loaded and played to its end
	loading              // being decoded
	playing
	paused
)

// The speeds (zf:src/audio.rs SPEEDS): the chip cycles 1x, 1.5x and 2x; the
// message menu offers them all.
var Speeds = [...]float32{1, 1.25, 1.5, 1.75, 2}

var speedLabels = [...]string{"1x", "1.25x", "1.5x", "1.75x", "2x"}

// cycled are the speeds the chip cycles through (CYCLED_SPEEDS).
var cycled = [...]float32{1, 1.5, 2}

// supported is the speed of Speeds nearest to v (supported_speed); 1 for
// anything not finite.
func supported(v float32) float32 {
	if v != v || v > 1e9 || v < -1e9 {
		return Speeds[0]
	}
	best := Speeds[0]
	for _, s := range Speeds {
		if abs(s-v) < abs(best-v) {
			best = s
		}
	}
	return best
}

// nextCycled is the chip's next speed (next_cycled_speed): the next faster
// cycled speed, from 2x back to 1x.
func nextCycled(v float32) float32 {
	for _, s := range cycled {
		if s > v {
			return s
		}
	}
	return cycled[0]
}

// SpeedLabel is a speed's label, as upstream's speed_label: "1x", "1.25x".
func SpeedLabel(v float32) string {
	v = supported(v)
	for i, s := range Speeds {
		if s == v {
			return speedLabels[i]
		}
	}
	return speedLabels[0]
}

func abs(v float32) float32 {
	if v < 0 {
		return -v
	}
	return v
}

// Speed is the voice speed that plays: the setting, snapped to Speeds.
func Speed(s *state.State) float32 { return supported(s.Settings.VoiceSpeed) }

// SetSpeed sets the voice speed (Action::SetVoiceSpeed): the setting,
// remembered for later notes, and the clip playing now; the pitch is kept.
// The message menu's speed row (MM-019) calls it too.
func SetSpeed(s *state.State, v float32) {
	v = supported(v)
	s.Settings.VoiceSpeed = v
	s.SettingsChanged()
	if pb.p != nil {
		pb.p.SetRate(v)
	}
}

// playback is the one loaded clip (upstream's audio::Player): its message,
// its state and its Player.
type playback struct {
	acct  *state.Account
	chat  model.ChatRef
	msg   []byte
	path  []byte
	voice bool // a voice note: a run carries on after it
	file  bool // p is a file's Player, loading until it is Ready
	ph    phase
	p     Player
	start float32 // where it starts once decoded, 0 to 1
	gen   uint64  // the decode asked for; another's result is dropped
	total time.Duration
	bars  [bars]byte
	// endAt is when it should end, as last seen playing: an end noticed long
	// after it (nothing polled it) does not start the next note.
	endAt time.Time
}

// bars is the waveform's length (zf:src/voice.rs BARS).
const bars = 64

var pb playback

// endSlack: a player that stopped this near either end played to its end.
const endSlack = 30 * time.Millisecond

// staleEnd: an end noticed later than this does not carry the run on.
const staleEnd = 2 * time.Second

// is reports whether the loaded clip is message id of the open chat of a.
func (p *playback) is(a *state.Account, id []byte) bool {
	return p.acct != nil && p.acct == a && a.Open == p.chat && bytes.Equal(p.msg, id)
}

// status is message id's state, position and length (Player::status).
func status(a *state.Account, id []byte) (ph phase, pos, total time.Duration) {
	if !pb.is(a, id) {
		return idle, 0, 0
	}
	switch pb.ph {
	case playing, paused:
		if pb.p != nil {
			pos = min(max(pb.p.Position(), 0), pb.total)
		}
		return pb.ph, pos, pb.total
	}
	return pb.ph, 0, pb.total
}

// stop closes the loaded clip (Player::stop): nothing plays or decodes.
func stop() {
	if pb.p != nil {
		pb.p.Close()
	}
	gen := pb.gen + 1
	pb = playback{gen: gen, msg: pb.msg[:0], path: pb.path[:0]}
}

// Playing reports whether a voice note or audio file is playing or being
// decoded to play (Player::is_playing), or a video plays in its bubble with
// its sound on: what pauses other apps' media.
func Playing() bool { return pb.ph == playing || pb.ph == loading || videoPlaying() }

// toggle plays or pauses message id (Action::PlayVoice, play_voice): a
// finished clip starts over, another is decoded first. The first play sends
// the played receipt.
func toggle(e *screen.Env, a *state.Account, id, path []byte, voice bool) {
	if pb.is(a, id) && pb.p != nil && pb.ph != loading {
		var err error
		switch pb.ph {
		case playing:
			pb.p.Pause()
			pb.ph = paused
		case paused:
			err = pb.p.Play()
			pb.ph = playing
		default: // done: from the start
			pb.p.Seek(0)
			err = pb.p.Play()
			pb.ph = playing
		}
		if err != nil {
			fail(e.S, err)
			return
		}
		pb.expect(e.S)
	} else {
		load(e.S, a, id, path, voice, 0)
	}
	waiting.reset()
	logic.PlayVoice(e.S, a, id)
}

// seek plays message id from fraction f (Action::SeekVoice): one sound at a
// time, and the clip picked wins over one a run is still fetching. As
// upstream's, it sends no played receipt.
func seek(e *screen.Env, a *state.Account, id, path []byte, voice bool, f float32) {
	f = min(max(f, 0), 1)
	waiting.reset()
	logic.SeekVoice(e.S, a)
	if !pb.is(a, id) || (pb.p == nil && pb.ph != loading) {
		load(e.S, a, id, path, voice, f)
		return
	}
	if pb.ph == loading {
		pb.start = f
		return
	}
	pb.p.Seek(time.Duration(float64(pb.total) * float64(f)))
	if pb.ph != playing {
		if err := pb.p.Play(); err != nil {
			fail(e.S, err)
			return
		}
		pb.ph = playing
	}
	pb.expect(e.S)
}

// seekBy moves the playing or paused clip by d.
func seekBy(id []byte, a *state.Account, d time.Duration) {
	if !pb.is(a, id) || pb.p == nil || (pb.ph != playing && pb.ph != paused) {
		return
	}
	pb.p.Seek(min(max(pb.p.Position()+d, 0), pb.total))
}

// load closes the clip loaded and decodes message id's file, to play from
// fraction start (Player::load). One sound at a time: a video in its bubble
// stops.
func load(s *state.State, a *state.Account, id, path []byte, voice bool, start float32) {
	stop()
	stopVideo()
	pb.acct, pb.chat, pb.voice, pb.ph, pb.start = a, a.Open, voice, loading, start
	pb.msg = append(pb.msg[:0], id...)
	pb.path = append(pb.path[:0], path...)
	pb.gen++
	gen, conn, p, wake := pb.gen, a.Conn, string(path), s.Wake
	go func() {
		c, err := decodeVoice(conn, p)
		results.put(result{gen: gen, clip: c, err: err})
		if wake != nil {
			wake()
		}
	}()
}

// result is a finished decode, for the main thread.
type result struct {
	gen  uint64
	clip Clip
	err  error
}

// slot hands the workers' decodes to the main thread: the latest only.
type slot struct {
	sync.Mutex
	r   result
	has bool
}

var results slot

func (q *slot) put(r result) {
	q.Lock()
	if !q.has || r.gen > q.r.gen { // a slower, older decode never hides a newer one
		q.r, q.has = r, true
	}
	q.Unlock()
}

func (q *slot) take() (result, bool) {
	q.Lock()
	defer q.Unlock()
	if !q.has {
		return result{}, false
	}
	r := q.r
	q.r, q.has = result{}, false
	return r, true
}

// loaded makes the decoded clip's Player and plays it. An audio file the
// core does not decode opens in a file Player instead, which Frame starts
// once it is ready.
func loaded(s *state.State, r result) {
	if r.gen != pb.gen || pb.ph != loading {
		return
	}
	if r.err != nil {
		if !pb.voice && errors.Is(r.err, errUndecodable) {
			p, err := openPlayer(string(pb.path))
			if err != nil { // no player for files here: the system's has it
				path := string(pb.path)
				stop()
				openFile(s, path)
				return
			}
			pb.p, pb.file = p, true
			return
		}
		fail(s, r.err)
		return
	}
	c := &r.clip
	if c.HasBars {
		pb.bars = c.Bars
		rememberBars(pb.acct, pb.chat, pb.msg, &c.Bars)
	}
	p, err := newPlayer(c.PCM, c.Format)
	if err != nil {
		fail(s, err)
		return
	}
	pb.p = p
	begin(s)
}

// begin plays the loaded clip's Player, ready now, from where it was asked
// to start, at the voice speed.
func begin(s *state.State) {
	p := pb.p
	pb.total = p.Duration()
	p.SetRate(Speed(s))
	if pb.start > 0 {
		p.Seek(time.Duration(float64(pb.total) * float64(pb.start)))
	}
	if err := p.Play(); err != nil {
		fail(s, err)
		return
	}
	pb.ph = playing
	pb.expect(s)
}

// opening follows a file Player while it opens: it plays once ready; a file
// the system cannot decode either goes to the system's player, and one that
// cannot be read says why.
func opening(s *state.State, c *ui.Context) {
	p := pb.p
	switch {
	case p.Ready():
		begin(s)
	case p.Err() != nil:
		err, path := p.Err(), string(pb.path)
		if errors.Is(err, media.ErrFormat) {
			stop()
			openFile(s, path)
			return
		}
		fail(s, errors.New(strings.Replace(i18n.T("Could not read the audio: {error}"), "{error}", err.Error(), 1)))
	default:
		c.WakeIn(int64(frameStep)) // Opened wakes the app too; this is in case
	}
}

// fail stops the clip and says why (toast_error).
func fail(s *state.State, err error) {
	stop()
	s.Toast([]byte(err.Error()), true)
}

// expect notes when the playing clip should end, at the speed it plays.
func (p *playback) expect(s *state.State) {
	if p.p == nil {
		return
	}
	left := float64(p.total-p.p.Position()) / float64(Speed(s))
	p.endAt = s.Now().Add(time.Duration(left))
}

// ended is a clip that played to its end: it stays loaded, at the start, and
// a voice note carries the run on to the next unheard one, unless the end
// was noticed long after it happened.
func ended(s *state.State, now time.Time) {
	pb.ph = idle
	if pb.voice && pb.acct != nil && !now.After(pb.endAt.Add(staleEnd)) {
		logic.VoiceEnded(s, pb.acct, pb.msg)
	}
}

var ticked struct {
	c     *ui.Context
	frame uint32
}

// lockOwner is this package's owner of its State.OnLock hook.
var lockOwner struct{ s *state.State }

// Frame keeps playback going, once per frame of the main window: it takes a
// finished decode, notices a clip that played to its end, starts the next
// note of a run, and stops playback when the account on screen changes or
// the chat goes. Size and Build call it; the transcript (or the shell) may
// call it every frame so that runs carry on while no voice note is on
// screen.
func Frame(e *screen.Env) {
	c := e.C()
	if ticked.c == c && ticked.frame == c.Frame() {
		return
	}
	ticked.c, ticked.frame = c, c.Frame()
	thumbnail.Frame(c.Frame())
	s := e.S
	if lockOwner.s != s {
		lockOwner.s = s
		s.OnLock(&lockOwner, func(*state.State) {
			stop()
			stopVideo()
			closeGIF()
			stopMotion()
			waiting.reset()
		})
	}
	a := e.Acct()
	if pb.acct != nil && (pb.acct != a || !chatExists(a, pb.chat)) {
		stop()
	}
	if r, ok := results.take(); ok {
		loaded(s, r)
	}
	if pb.ph == loading && pb.file && pb.p != nil {
		opening(s, c)
	}
	if t := later.take(); t != "" {
		s.Toast([]byte(t), true)
	}
	now := s.Now()
	if pb.ph == playing && pb.p != nil {
		switch pos := pb.p.Position(); {
		case !pb.p.Playing():
			if pos <= endSlack || pos >= pb.total-endSlack {
				ended(s, now)
			} else {
				pb.ph = paused // the system paused it (an output that went away)
			}
		case pb.total > 0 && pos >= pb.total:
			// The last sample has played: AVAudioPlayer says it stopped some
			// 65-120 ms later. Upstream's run carries on once its sink has
			// drained, which is about now.
			ended(s, now)
		}
	}
	if v := &s.Voice; v.Next.Len() >= 2 && a != nil && a.Open != 0 {
		load(s, a, v.Next.At(0), v.Next.At(1), true, 0)
		v.Next.Reset()
		logic.PlayVoice(s, a, pb.msg)
	}
	if pb.ph == playing && pb.p != nil {
		// a frame at the end, to notice it; Build asks for more while the
		// note is on screen
		pb.expect(s)
		c.WakeIn(int64(pb.endAt.Sub(now) + 5*time.Millisecond))
	}
	videoFrame(e, a, now)
}

func chatExists(a *state.Account, ref model.ChatRef) bool {
	if a == nil || a.Model == nil {
		return false
	}
	_, ok := a.Model.Chats.Get(ref)
	return ok
}

// Event takes the framework's media events: a clip that played to its end
// (media.Ended), stopped on an error or did not open (media.Failed), or a
// file that opened (media.Opened), and a video's end. It reports whether
// the event was for one of this package's players. The app's handler passes
// it every app event (State.OnAppEvent); without it Frame notices the same
// by polling.
func Event(s *state.State, ev *app.Event) bool {
	m, ok := media.EventOf(ev)
	if !ok {
		return false
	}
	if videoEvent(s, m) {
		return true
	}
	if pb.p == nil || m.ID != pb.p.ID() {
		return false // another package's player, or one closed since
	}
	switch {
	case pb.ph == playing && m.Kind == media.Ended:
		ended(s, s.Now())
	case pb.ph == playing && m.Kind == media.Failed:
		pb.ph = idle
	}
	s.Wake() // a file that opened (or did not) is taken up by Frame
	return true
}

// frameStep is how often the progress moves while a note on screen plays
// (upstream's 40 ms).
const frameStep = 40 * time.Millisecond
