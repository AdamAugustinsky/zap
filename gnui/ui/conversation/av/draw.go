package av

import (
	"gonativeui/icon"
	"gonativeui/img"
	"gonativeui/theme"
	"gonativeui/ui"
	"gonativeui/widget/base"

	"zapfast/abi"
	"zapfast/model"
	"zapfast/state"
	"zapfast/ui/conversation/av/thumbnail"
	"zapfast/ui/conversation/mkey"
	"zapfast/ui/screen"
)

// blk is what one Size or Build call works with: the window's kit and
// theme, the message and its attachment. There is one, reused (the
// transcript builds on the main thread, one block at a time).
type blk struct {
	e   *screen.Env
	k   *base.Kit
	c   *ui.Context
	r   *theme.Resolved
	a   *state.Account
	m   model.Message
	md  model.Media
	sc  float32
	key ui.Key
	dm  *abi.Message // the decoded message, nil until decoded
	nic uint64       // icons drawn so far, to key them apart
}

var bk blk

// use points the shared blk at a call's window and message.
func use(e *screen.Env, m model.Message) *blk {
	b := &bk
	*b = blk{e: e, k: e.K.Kit, c: e.K.Context(), a: e.Acct(), m: m}
	b.md, _ = m.Media()
	b.r = b.k.Theme()
	b.sc = b.k.Scale()
	return b
}

// done ends a call: the shared blk lets go of the window's kit and the
// scratch of the message's views (keeping its capacity), so neither
// outlives the call, its window or its conversation's arena.
func (b *blk) done() {
	*b = blk{}
	dec.Release()
}

// decoded decodes the whole message once per call, into a scratch that the
// next call reuses.
func (b *blk) decoded() *abi.Message {
	if b.dm == nil {
		if b.m.Decode(&dec) != nil {
			dec = abi.Message{}
		}
		b.dm = &dec
	}
	return b.dm
}

var dec abi.Message

// pt is points in pixels, snapped.
func (b *blk) pt(v float32) float32 { return theme.Snap(v * b.sc) }

// ---- colours ----

func (b *blk) col(role theme.Role) uint32 { return b.r.Colors[role] }

// accent is the player's colour on either side's bubble: yours carry Rex's
// tint of the accent, not the accent.
func (b *blk) accent() uint32 { return b.col(theme.Accent) }

// dim is secondary text: the duration.
func (b *blk) dim() uint32 { return b.col(theme.Label2) }

// ink is the text's colour in the bubble.
func (b *blk) ink() uint32 { return b.col(theme.Label) }

// quiet is the unplayed part of the waveform or the track
// (palette.secondary at 0.7).
func (b *blk) quiet() uint32 { return theme.WithAlpha(b.col(theme.Label2), 0.7) }

// ---- boxes ----

// begin is the block's root: a floating box at r (relative to the box being
// built) keyed by the message, so the transcript can build many blocks in one
// parent.
func (b *blk) begin(r screen.Rect) {
	c := b.c
	b.key = ui.KeyN(ui.HashString(c.ParentKey(), "av"), idHash(b.m.ID()))
	theme.At(c, r.X, r.Y, r.W, r.H)
	c.Push(c.BoxKeyed(base.Floating, b.key, nil))
}

func (b *blk) end() { b.c.Pop() }

// kn is the key of the n-th box of the current parent.
func (b *blk) kn(n uint64) ui.Key { return ui.KeyN(b.c.ParentKey(), n) }

// fillBox draws a coloured rounded rectangle.
func (b *blk) fillBox(n uint64, x, y, w, h, radius float32, col uint32) {
	c := b.c
	theme.At(c, x, y, w, h)
	c.NextBG(col)
	c.NextRadius(radius)
	c.BoxKeyed(base.Floating|ui.DrawBackground, b.kn(n), nil)
}

// text draws one line of s at (x, y), at most w wide, in the subheadline
// font (upstream's 11.5 points), as a label named by its text.
func (b *blk) text(n uint64, s []byte, col uint32, x, y, w float32) {
	c := b.c
	st := &b.r.Styles[theme.StyleSubheadline]
	theme.At(c, x, y, w, st.Line)
	c.NextFont(st.Font)
	c.NextTextColor(col)
	c.NextRole(ui.RoleLabel)
	c.NextSemNameBytes(s)
	c.BoxKeyed(base.Floating|ui.DrawText|ui.FadeOverflowX, b.kn(n), s)
}

// textW is the width of s in the subheadline font.
func (b *blk) textW(s []byte) float32 {
	return b.k.TextWidthBytes(b.r.Styles[theme.StyleSubheadline].Font, s)
}

func (b *blk) lineH() float32 { return b.r.Styles[theme.StyleSubheadline].Line }

// lbl is "name###n": a press label whose identity is n, built in the frame's
// arena.
func (b *blk) lbl(name string, n uint64) string {
	return b.c.Fmt().Str(name).Str("###").Uint64(n).String()
}

// ---- icons ----

// iconSet is an icon atlas of the package's own, per kit: the kit's atlas is
// fixed when its first frame starts, before a package without an Init can
// add to it.
type iconSet struct {
	k     *base.Kit
	atlas *icon.Atlas
	scale float32
}

var iconSets []iconSet

// The icons the blocks draw (the first extra IDs of the set's atlas).
const (
	icPlay icon.ID = iota + icon.NumStandard
	icPause
	icDownload
	icAlert
	icMusic
	icVideo
	icPlayBig
	icPauseBig
	icAlertBig
	icExternal
	icMaximize
	icVolume
	icMuted
)

func iconSpecs() []icon.Spec {
	s := func(sym string, fb rune, pt float32) icon.Spec {
		return icon.Spec{Symbol: sym, Fallback: fb, Pt: pt}
	}
	return []icon.Spec{
		s("play.fill", '▶', 16),
		s("pause.fill", '⏸', 16),
		s("arrow.down", '⬇', 16),
		s("exclamationmark.circle", '!', 18),
		s("music.note", '♪', 18),
		s("video.fill", '▶', 16),
		s("play.fill", '▶', 22),
		s("pause.fill", '⏸', 22),
		s("exclamationmark.circle", '!', 22),
		s("arrow.up.right.square", '↗', 14), // as a document's (kinds)
		s("arrow.up.left.and.arrow.down.right", '⤢', 16),
		s("speaker.wave.2.fill", '♪', 16),
		s("speaker.slash.fill", '×', 16),
	}
}

func (b *blk) icons() *icon.Atlas {
	var set *iconSet
	for i := range iconSets {
		if iconSets[i].k == b.k {
			set = &iconSets[i]
			break
		}
	}
	if set == nil {
		if len(iconSets) > 8 {
			iconSets = iconSets[:0]
		}
		iconSets = append(iconSets, iconSet{k: b.k, atlas: icon.New(b.k.TextSystem(), iconSpecs()...)})
		set = &iconSets[len(iconSets)-1]
	}
	if set.scale != b.sc {
		set.atlas.Raster(b.sc)
		set.scale = b.sc
	}
	set.atlas.Attach(b.c.Uploader())
	return set.atlas
}

// icon draws icon id in col centred in the box (x, y, w, h).
func (b *blk) icon(id icon.ID, col uint32, x, y, w, h float32) {
	at := b.icons()
	iw, ih := at.Size(id)
	flags, disp := at.Prepare(b.c, id, col)
	if flags == 0 {
		return
	}
	b.c.NextPos(x+(w-iw)/2, y+(h-ih)/2)
	b.c.NextFlags(base.Floating)
	b.nic++
	b.c.BoxKeyed(flags, ui.KeyN(b.c.ParentKey(), 7000+b.nic), disp)
}

// ---- keys ----

// idHash is a hash of a message id, for keys and caches.
func idHash(id []byte) uint64 {
	h := uint64(14695981039346656037)
	for _, c := range id {
		h ^= uint64(c)
		h *= 1099511628211
	}
	return h
}

// ---- inline thumbnails (posters) ----

var thumbs = thumbnail.New()

const maxThumbs = 256

// thumbnail snapshots an inline thumbnail's bytes, nil while
// they are read (on a worker; the window is woken when they are in).
func (b *blk) thumbnail(ref abi.BlobRef) img.Lease {
	return thumbs.Get(b.e.S, b.a, ref, maxThumbs)
}

// poster asks the window's image cache for the message's thumbnail decoded
// to cover w×h pixels; 0 until it is ready (or with no cache).
func (b *blk) poster(w, h float32) img.Handle {
	ref, ok := b.m.Thumbnail()
	ca := b.k.Images()
	if !ok || ca == nil || b.a == nil {
		return 0
	}
	key := img.Key{Hash: ref.ID ^ b.a.Serial*0x9e3779b97f4a7c15 ^ 0x6176706f73746572, W: int32(w), H: int32(h), Flags: img.Cover}
	if hd, st, ok := ca.Get(key); ok {
		if st == img.Failed {
			return 0
		}
		return hd
	}
	t := b.thumbnail(ref)
	if t == nil {
		return 0
	}
	hd, st := ca.Request(key, img.Leased(t))
	if st == img.Failed {
		return 0
	}
	return hd
}

// filePoster is upstream's local-file fallback (conversation.rs:8516): the
// first frame, decoded off-thread at 320 points wide, cropped to fill the
// bubble (the message's size may not be the video's; playing, the bubble
// takes the video's).
// The window cache owns pending results, failures and texture lifetimes.
func (b *blk) filePoster(w, h, radius float32) bool {
	ca := b.k.Images()
	if ca == nil || b.a == nil {
		return false
	}
	key := img.Key{Hash: b.a.Serial*0x9e3779b97f4a7c15 ^ idHash(b.m.ID()) ^ img.HashBytes(b.md.Path) ^ 0x617666696c65, W: int32(b.pt(pictureWidth))}
	hd, _, found := ca.Get(key)
	if !found {
		hd, _ = ca.Request(key, img.VideoPoster(string(b.md.Path)))
	}
	_, _, st := ca.Use(hd) // keep pending uploads wanted without drawing a skeleton
	if st != img.Ready {
		return false
	}
	b.c.NextPos(0, 0)
	b.c.NextSize(ui.Px(w), ui.Px(h))
	b.c.NextFlags(base.Floating)
	b.k.Image(41, hd, ui.FitFill, radius, "")
	return true
}

// image draws the handle in the box (x, y, w, h).
func (b *blk) image(n uint64, hd img.Handle, x, y, w, h, radius float32, alt string) {
	c := b.c
	c.NextPos(x, y)
	c.NextSize(ui.Px(w), ui.Px(h))
	c.NextFlags(base.Floating)
	b.k.Image(n, hd, ui.FitFill, radius, alt)
}

// ---- downloads ----

// maxDownload is the largest attachment fetched without being asked
// (zf:src/model.rs ATTACHMENT_DOWNLOAD_LIMIT).
const maxDownload = 64 << 20

// requested are the automatic downloads asked for, by message and
// generation: one per message state.
var requested [64]struct {
	id  mkey.K
	gen uint32
}

// download asks the core for the message's file (Action::Download).
func (b *blk) download(manual bool) {
	if b.a == nil {
		return
	}
	id, gen := mkey.Of(b.a, b.m.ID()), b.m.Gen()
	slot := &requested[id.Slot(len(requested))]
	if !manual && slot.id == id && slot.gen == gen {
		return
	}
	slot.id, slot.gen = id, gen
	b.a.Download(b.decoded().Chat, b.m.ID(), model.PartMain, manual)
}

// autoDownload starts the download of a block in view, as upstream does when
// automatic downloads are on and the file is within the limit.
func (b *blk) autoDownload() {
	if len(b.md.Path) == 0 && b.md.State == abi.MediaStateTagIdle && b.a != nil &&
		b.a.Settings.AutoDownload && b.md.Size <= maxDownload {
		b.download(false)
	}
}

// failure is the core's reason a download failed.
func (b *blk) failure() []byte {
	d := b.decoded()
	switch d.Content.Tag {
	case abi.ContentTagAudio:
		return d.Content.Audio.Media.State.Failed.Error
	case abi.ContentTagVideo:
		return d.Content.Video.Media.State.Failed.Error
	}
	return nil
}

// byteSize is util::bytes: "12 B", "1.5 MB".
func byteSize(f *ui.Fmt, n uint64) *ui.Fmt {
	units := [...]string{"B", "KB", "MB", "GB"}
	v, u := float64(n), 0
	for v >= 1000 && u < len(units)-1 {
		v /= 1024
		u++
	}
	if u == 0 {
		return f.Uint64(n).Str(" B")
	}
	return f.Float(v, 1).Byte(' ').Str(units[u])
}
