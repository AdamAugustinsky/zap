package av_test

import (
	"image/png"
	"os"
	"sync"
	"testing"
	"time"

	"gonativeui/app"
	"gonativeui/gpu"
	"gonativeui/media"
	"gonativeui/platform"
	"gonativeui/theme"
	"gonativeui/ui"
	"gonativeui/widget/base"
	"gonativeui/widget/widgettest"

	"zapfast/abi"
	"zapfast/core"
	"zapfast/core/fake"
	"zapfast/logic"
	"zapfast/model"
	"zapfast/state"
	"zapfast/ui/conversation/av"
	"zapfast/ui/conversation/kinds"
	"zapfast/ui/screen"
)

const chatID = "1@s.whatsapp.net"

// page stands in for the transcript: a column of the open chat's blocks,
// each in a box of the size Size gave it, built with coordinates local to
// that box, as ui/conversation builds them. The reducers run every frame,
// as the shell runs them.
type page struct {
	s     *state.State
	l     *logic.Logic
	env   screen.Env
	maxW  float32
	rects map[string]screen.Rect // by message id, window pixels, from the last frame
	quiet bool                   // draw only: no records, which allocate
	// skip are blocks not built (scrolled out of the transcript), and shift
	// moves the column down (pixels: past the window's bottom, blocks are
	// built but do not show).
	skip              map[string]bool
	shift             float32
	hidden, unfocused bool
	// realClock gives the frames the platform's clock, as a window's are,
	// in place of the rig's synthetic one: media.Video pulls its frames by
	// it (the live tests).
	realClock bool
}

func (p *page) Init(a *app.App) error {
	p.env = screen.Env{K: screen.NewKit(a, nil), A: a, S: p.s}
	screen.NewImages(a, p.env.K, nil)
	a.UI.Reserve(2048)
	p.rects = map[string]screen.Rect{}
	return nil
}

func (p *page) Build(c *ui.Context) {
	c.Input().Hidden = p.hidden
	if p.unfocused {
		c.Input().Focused = false
	}
	if p.realClock {
		c.Input().Time = platform.Now()
	}
	p.s.Pump(0)
	p.l.Frame(p.s)
	e := &p.env
	e.K.Begin(c)
	screen.PollImages(e.K, c)
	av.Frame(e) // as the shell would every frame (the report's shared need)
	if v := p.s.Acct().Conversation(); v != nil {
		sc := e.K.Scale()
		y := 12 * sc
		for i := 0; i < v.Len(); i++ {
			m := v.At(i)
			isAV := av.Has(m)
			if !isAV && !kinds.Has(m) || p.skip[string(m.ID())] {
				continue
			}
			var w, h float32
			if isAV {
				w, h = av.Size(e, m, p.maxW*sc)
			} else { // a photo: its motion clip is av's
				w, h = kinds.Size(e, m, p.maxW*sc)
			}
			x, by := 12*sc, y+p.shift
			if !p.quiet {
				p.rects[string(m.ID())] = screen.Rect{X: x, Y: by, W: w, H: h}
			}
			if !av.NoBubble(m) && !kinds.NoBubble(m) {
				th := e.K.Theme()
				col := th.Colors[theme.Fill1]
				if m.FromMe() {
					col = th.Colors[theme.Accent]
				}
				pad := 8 * sc
				theme.At(c, x-pad, by-pad, w+2*pad, h+2*pad)
				c.NextBG(col)
				c.NextRadius(12 * sc)
				c.BoxKeyed(base.Floating|ui.DrawBackground, ui.KeyN(ui.HashString(0, "bubble"), uint64(i)), nil)
			}
			theme.At(c, x, by, w, h)
			c.Push(c.BoxKeyed(base.Floating, ui.KeyN(ui.HashString(0, "block"), uint64(i)), nil))
			if isAV {
				av.Build(e, m, screen.Rect{W: w, H: h})
			} else {
				kinds.Build(e, m, screen.Rect{W: w, H: h})
			}
			c.Pop()
			y += h + 20*sc
		}
	}
	e.K.End()
}

// fakePlayer is a Player whose clock the test moves. One of a file
// (OpenPlayer) is opening until the test says it opened (or failed).
type fakePlayer struct {
	id      uint64
	dur     time.Duration
	pos     time.Duration
	rate    float32
	playing bool
	closed  bool
	plays   int
	seeks   []time.Duration
	pcm     []byte
	format  media.Format
	path    string
	opening bool
	err     error
}

func (p *fakePlayer) ID() uint64              { return p.id }
func (p *fakePlayer) Ready() bool             { return !p.opening && p.err == nil && !p.closed }
func (p *fakePlayer) Err() error              { return p.err }
func (p *fakePlayer) Duration() time.Duration { return p.dur }
func (p *fakePlayer) Play() error {
	if !p.Ready() {
		return media.ErrState
	}
	p.playing = true
	p.plays++
	return nil
}
func (p *fakePlayer) Pause()                  { p.playing = false }
func (p *fakePlayer) Playing() bool           { return p.playing && !p.closed }
func (p *fakePlayer) Position() time.Duration { return p.pos }
func (p *fakePlayer) Seek(t time.Duration)    { p.pos = t; p.seeks = append(p.seeks, t) }
func (p *fakePlayer) SetRate(r float32)       { p.rate = r }
func (p *fakePlayer) Close()                  { p.closed, p.playing = true, false }

// end plays it to its end, as AVAudioPlayer does: stopped, back at 0.
func (p *fakePlayer) end() { p.playing, p.pos = false, 0 }

// fakeVideo is a Video whose clock and loading the test moves. Its frame is
// a texture of one colour, made on the window's renderer.
type fakeVideo struct {
	id       uint64
	path     string
	opts     media.VideoOptions
	state    media.VideoState
	dur, pos time.Duration
	rate     float32
	playing  bool
	ended    bool
	closed   bool
	muted    bool
	plays    int
	pauses   int
	seeks    int
	seekTo   time.Duration
	frames   int // Frame calls that gave a picture
	tex      gpu.TextureID
	r        *gpu.Renderer
}

func (v *fakeVideo) ID() uint64              { return v.id }
func (v *fakeVideo) State() media.VideoState { return v.state }
func (v *fakeVideo) Err() error {
	if v.state == media.VideoFailed {
		return media.ErrFormat
	}
	return nil
}
func (v *fakeVideo) Duration() time.Duration {
	if v.state != media.VideoReady {
		return 0
	}
	return v.dur
}
func (v *fakeVideo) Position() time.Duration { return v.pos }
func (v *fakeVideo) Play() {
	if v.ended {
		v.pos, v.ended = 0, false
	}
	v.playing = true
	v.plays++
}
func (v *fakeVideo) Pause()        { v.playing = false; v.pauses++ }
func (v *fakeVideo) Playing() bool { return v.playing }
func (v *fakeVideo) Ended() bool   { return v.ended }
func (v *fakeVideo) Seek(d time.Duration) {
	v.pos, v.seekTo, v.ended = d, d, false
	v.seeks++
}
func (v *fakeVideo) SetMuted(m bool)   { v.muted = m }
func (v *fakeVideo) SetRate(r float32) { v.rate = r }
func (v *fakeVideo) Frame(r *gpu.Renderer, now int64) (gpu.TextureID, int, int, int64) {
	if v.closed || r == nil || v.state != media.VideoReady {
		return 0, 0, 0, 0
	}
	if v.tex == 0 {
		v.r, v.tex = r, r.NewTexture(gpu.TexRGBA8, 16, 9)
		pix := make([]byte, 16*9*4)
		for i := 0; i < len(pix); i += 4 {
			pix[i], pix[i+1], pix[i+2], pix[i+3] = 200, 30, 200, 255 // magenta
		}
		r.Upload(v.tex, 0, 0, 16, 9, pix, 16*4)
	}
	v.frames++
	var next int64
	if v.playing {
		next = int64(33 * time.Millisecond)
	}
	return v.tex, 16, 9, next
}
func (v *fakeVideo) Close() {
	if v.closed {
		return
	}
	v.closed, v.playing = true, false
	if v.tex != 0 {
		v.r.FreeTexture(v.tex)
		v.tex = 0
	}
}

// end plays it to its end, as media.Video does: paused on its last frame,
// Ended until it plays again.
func (v *fakeVideo) end() { v.playing, v.ended, v.pos = false, true, v.dur }

// opener records what would have opened in the system.
type opener struct {
	mu    sync.Mutex
	paths []string
}

func (o *opener) Open(p string) error {
	o.mu.Lock()
	o.paths = append(o.paths, p)
	o.mu.Unlock()
	return nil
}

func (o *opener) opened() []string {
	o.mu.Lock()
	defer o.mu.Unlock()
	return append([]string(nil), o.paths...)
}

type rig struct {
	*widgettest.Rig
	t       *testing.T
	p       *page
	f       *fake.Core
	s       *state.State
	a       *state.Account
	sh      *opener
	now     time.Time
	players []*fakePlayer
	files   []*fakePlayer // from OpenPlayer
	videos  []*fakeVideo
	// loadVideos: videos open loading (Ready when the test says); badVideos:
	// paths that fail to load.
	loadVideos bool
	badVideos  map[string]bool

	mu      sync.Mutex
	decoded []string // the paths decoded, in order
}

func ev(tag abi.EventTag, set func(e *abi.Event)) *abi.Event {
	e := new(abi.Event)
	e.Tag = tag
	set(e)
	return e
}

func newRig(t *testing.T, msgs ...abi.Message) *rig {
	t.Helper()
	return newRigWith(t, false, msgs...)
}

// newRigWith is newRig; viaCore, notes are decoded by the fake core's
// DecodeVoice (the real decoding path: the blob read and freed), not the
// rig's.
func newRigWith(t *testing.T, viaCore bool, msgs ...abi.Message) *rig {
	t.Helper()
	av.Forget()
	f := fake.New(nil)
	s := state.New()
	r := &rig{t: t, f: f, s: s, sh: &opener{}, now: time.Unix(1_792_000_000, 0)}
	s.Shell = r.sh
	s.Now = func() time.Time { return r.now }
	restore := av.SetHooks(r.hooks(viaCore))
	t.Cleanup(func() { av.Forget(); restore() })
	a := s.AddAccount("1", nil, f)
	a.Settings.AutoDownload = false
	r.a = a
	f.Emit(ev(abi.EventTagLink, func(e *abi.Event) { e.Link.Status.Tag = abi.LinkStatusTagConnected }),
		ev(abi.EventTagChats, func(e *abi.Event) {
			e.Chats = abi.EventChats{Batch: 1, Last: true, Chats: []abi.Chat{{ID: []byte(chatID), Name: []byte("Ada"), LastActivity: 1}}}
		}),
		ev(abi.EventTagMessages, func(e *abi.Event) {
			e.Messages = abi.EventMessages{Chat: []byte(chatID), Batch: 1, Last: true, Messages: msgs}
		}))
	l := logic.New()
	l.Init(s)
	r.p = &page{s: s, l: l, maxW: 330}
	cfg := app.Config{Title: "av", Width: 420, Height: 1400, Clear: [4]float32{1, 1, 1, 1}}
	r.Rig = widgettest.Open(t, cfg, 420, 1400, 1, r.p)
	r.Offscreen().UI.SetSystemAppearance(ui.Appearance{Known: true})
	r.Frames(2)
	a.OpenChat(a.Model.Chats.Find([]byte(chatID)))
	r.Frames(4)
	return r
}

// hooks are the rig's fakes; viaCore, notes are decoded by the core's
// DecodeVoice, not the rig's.
func (r *rig) hooks(viaCore bool) av.Hooks {
	h := av.Hooks{Player: r.newPlayer, OpenPlayer: r.openPlayer, Decode: r.decode, OpenVideo: r.openVideo}
	if viaCore {
		h.Decode = nil
	}
	return h
}

func (r *rig) openPlayer(path string) (av.Player, error) {
	p := &fakePlayer{id: uint64(500 + len(r.files)), rate: 1, path: path, opening: true}
	r.files = append(r.files, p)
	return p, nil
}

func (r *rig) openVideo(path string, o media.VideoOptions) (av.Video, error) {
	v := &fakeVideo{id: uint64(1000 + len(r.videos)), path: path, opts: o, dur: 12 * time.Second, rate: 1, muted: o.Muted, state: media.VideoReady}
	if r.loadVideos {
		v.state = media.VideoLoading
	}
	if r.badVideos[path] {
		v.state = media.VideoFailed
	}
	r.videos = append(r.videos, v)
	return v, nil
}

// video is the n-th Video opened (from 0), once it is.
func (r *rig) video(n int) *fakeVideo {
	r.t.Helper()
	r.until("video opened", func() bool { return len(r.videos) > n })
	return r.videos[n]
}

// openVideos counts the Videos not closed.
func (r *rig) openVideos() (n int) {
	for _, v := range r.videos {
		if !v.closed {
			n++
		}
	}
	return n
}

func (r *rig) newPlayer(pcm []byte, f media.Format) (av.Player, error) {
	p := &fakePlayer{id: uint64(len(r.players) + 1), dur: f.Duration(len(pcm)), rate: 1, pcm: append([]byte(nil), pcm...), format: f}
	r.players = append(r.players, p)
	return p, nil
}

// decode is the core's decoding: a clip of the length the path's name
// says ("/v/7.ogg": 7 seconds, silent), on the worker that calls it.
func (r *rig) decode(_ core.Conn, path string) (av.Clip, error) {
	r.mu.Lock()
	r.decoded = append(r.decoded, path)
	r.mu.Unlock()
	n := 0
	for _, c := range path {
		if c >= '0' && c <= '9' {
			n = n*10 + int(c-'0')
		}
	}
	f := media.Format{SampleRate: core.VoiceRate, Channels: 1, Sample: media.Float32}
	return av.Clip{PCM: make([]byte, n*f.SampleRate*f.FrameBytes()), Format: f}, nil
}

func (r *rig) decodes() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]string(nil), r.decoded...)
}

// until runs frames until cond, for at most two seconds (decodes run on
// workers).
func (r *rig) until(what string, cond func() bool) {
	r.t.Helper()
	for i := 0; i < 400; i++ {
		r.Frames(1)
		if cond() {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	r.t.Fatalf("never: %s\n%s", what, widgettest.Dump(r.Tree()))
}

// player is the n-th Player made (from 0), once it is.
func (r *rig) player(n int) *fakePlayer {
	r.t.Helper()
	r.until("player made", func() bool { return len(r.players) > n })
	return r.players[n]
}

func (r *rig) sent(tag abi.CommandTag) []abi.Command {
	var out []abi.Command
	for _, c := range r.f.Sent() {
		if c.Tag == tag {
			out = append(out, c)
		}
	}
	return out
}

func (r *rig) must(role ui.Role, name string) ui.SemNode {
	r.t.Helper()
	n, ok := r.Find(role, name)
	if !ok {
		r.t.Fatalf("no %v %q in\n%s", role, name, widgettest.Dump(r.Tree()))
	}
	return n
}

func (r *rig) count(role ui.Role, name string) (n int) {
	t := r.Tree()
	for i := range t.Nodes {
		if t.Nodes[i].Role == role && string(t.Text(t.Nodes[i].Name)) == name {
			n++
		}
	}
	return n
}

func (r *rig) view(id string) model.Message {
	v := r.a.Conversation()
	i := v.Find([]byte(id))
	if i < 0 {
		r.t.Fatalf("no message %q", id)
	}
	return v.At(i)
}

func waveform() []byte {
	w := make([]byte, 64)
	for i := range w {
		w[i] = byte(i * 100 / 63)
	}
	return w
}

// voice is a voice note of the chat, downloaded to path unless it is "".
func voice(id string, ts int64, fromMe bool, path string, seconds uint32) abi.Message {
	m := abi.Message{ID: []byte(id), Chat: []byte(chatID), Sender: []byte(chatID), FromMe: fromMe, Timestamp: ts}
	if fromMe {
		m.Sender = []byte("9@s.whatsapp.net")
	}
	m.Content.Tag = abi.ContentTagAudio
	au := &m.Content.Audio
	au.VoiceNote, au.Seconds, au.HasSeconds, au.Waveform = true, seconds, true, waveform()
	au.Media = abi.Media{Mime: []byte("audio/ogg; codecs=opus"), Size: 12_000}
	if path != "" {
		au.Media.Path, au.Media.HasPath = []byte(path), true
	}
	return m
}

func text(id string, ts int64) abi.Message {
	m := abi.Message{ID: []byte(id), Chat: []byte(chatID), Sender: []byte(chatID), Timestamp: ts}
	m.Content.Tag = abi.ContentTagText
	m.Content.Text.Text = []byte("hello")
	return m
}

// video is an inline video (note: a round video note) with a poster.
func video(id string, ts int64, path string, note, gif bool) abi.Message {
	m := abi.Message{ID: []byte(id), Chat: []byte(chatID), Sender: []byte(chatID), Timestamp: ts}
	m.Content.Tag = abi.ContentTagVideo
	vd := &m.Content.Video
	vd.Seconds, vd.HasSeconds, vd.Note, vd.Gif = 12, true, note, gif
	vd.Media = abi.Media{Mime: []byte("video/mp4"), Size: 2_400_000, Width: 1280, HasWidth: true, Height: 720, HasHeight: true}
	if path != "" {
		vd.Media.Path, vd.Media.HasPath = []byte(path), true
	}
	return m
}

// shot writes the last frame to $AV_SHOT/name.png when that is set (for a
// look by eye; nothing compares it).
func (r *rig) shot(name string) {
	dir := os.Getenv("AV_SHOT")
	if dir == "" {
		return
	}
	f, err := os.Create(dir + "/" + name + ".png")
	if err != nil {
		r.t.Fatal(err)
	}
	defer f.Close()
	if err := png.Encode(f, r.Image()); err != nil {
		r.t.Fatal(err)
	}
}
