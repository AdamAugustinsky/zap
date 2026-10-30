package kinds

import (
	"gonativeui/icon"
	"gonativeui/rexui"
	"gonativeui/text"
	"gonativeui/theme"
	"gonativeui/ui"
	"gonativeui/widget/base"

	"zapfast/abi"
	"zapfast/model"
	"zapfast/state"
	"zapfast/ui/screen"
)

// blk is what one Size or Build call works with: the window's kit and
// theme, the message, and the colours of its bubble. There is one, reused
// (the transcript builds on the main thread, one block at a time).
type blk struct {
	e   *screen.Env
	k   *base.Kit
	c   *ui.Context
	r   *theme.Resolved
	a   *state.Account
	m   model.Message
	sc  float32
	own bool
	// flush: the block fills its bubble to the edges (BuildFlush)
	flush bool
	key   ui.Key
	dm    *abi.Message // the decoded message, nil until decoded
	nic   uint64       // icons drawn so far, to key them apart
	// span is the bubble's settled width (SizeIn), 0 for none: a card
	// spans it.
	span float32
}

var cur blk

// use points the shared blk at a call's window and message.
func use(e *screen.Env, m model.Message) *blk {
	b := &cur
	*b = blk{e: e, k: e.K.Kit, c: e.K.Context(), a: e.Acct(), m: m, own: m.FromMe()}
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

// decoded decodes the whole message once per call (into a scratch that the
// next call reuses).
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

// col is a role's colour. Both sides' bubbles are quiet (yours carry Rex's
// tint of the accent, not the accent), so a block is drawn alike in either.
func (b *blk) col(role theme.Role) uint32 { return b.r.Colors[role] }

func (b *blk) ink() uint32    { return b.col(theme.Label) }
func (b *blk) dim() uint32    { return b.col(theme.Label2) }
func (b *blk) faint() uint32  { return b.col(theme.Label3) }
func (b *blk) link() uint32   { return b.col(theme.Link) }
func (b *blk) accent() uint32 { return b.col(theme.Accent) }

// card is the fill of a card inside the bubble.
func (b *blk) card() uint32 { return b.col(theme.Fill3) }

// hover is the tint of a pressed or hovered row.
func (b *blk) hoverFill(down bool) uint32 {
	a := float32(0.06)
	if down {
		a = 0.12
	}
	return theme.WithAlpha(b.col(theme.Label), a)
}

func (b *blk) hairline() uint32 { return theme.WithAlpha(b.col(theme.Label), 0.15) }

func (b *blk) tint(c uint32) rexui.Tint { return tintOf(c, b.r) }

// tintOf finds the Tint that gives c: a role, or Label at the alpha used
// here; rexui's Labels take roles, not raw colours.
func tintOf(c uint32, r *theme.Resolved) rexui.Tint {
	for _, role := range [...]theme.Role{theme.Label, theme.Label2, theme.Label3, theme.Link, theme.Accent, theme.Red} {
		if r.Colors[role] == c {
			return rexui.Of(role)
		}
	}
	if r.Colors[theme.Label]&^0xFF == c&^0xFF {
		a := float32(c&0xFF) / 255
		return rexui.OfAlpha(theme.Label, a, a)
	}
	return rexui.Of(theme.Label)
}

// ---- fonts and lines ----

type fontID uint8

const (
	fBody fontID = iota
	fBold
	fCallout
	fSub
	fFoot
)

func (b *blk) style(f fontID) *theme.StyleFont {
	switch f {
	case fBold:
		return &b.r.Styles[theme.StyleBodySemibold]
	case fCallout:
		return &b.r.Styles[theme.StyleCallout]
	case fSub:
		return &b.r.Styles[theme.StyleSubheadline]
	case fFoot:
		return &b.r.Styles[theme.StyleFootnote]
	}
	return &b.r.Styles[theme.StyleBody]
}

func (b *blk) font(f fontID) text.FontKey { return b.style(f).Font }

func (b *blk) lineH(f fontID) float32 { return b.style(f).Line }

func (b *blk) width(f fontID, s []byte) float32 { return b.k.TextWidthBytes(b.font(f), s) }

func (b *blk) widthS(f fontID, s string) float32 { return b.k.TextWidth(b.font(f), s) }

// ---- boxes ----

// begin is the block's root: a floating box at r (relative to the box being
// built) keyed by the message, so the transcript can build many blocks in one
// parent.
func (b *blk) begin(r screen.Rect) {
	c := b.c
	b.key = ui.KeyN(ui.HashString(c.ParentKey(), "kinds"), idHash(b.m.ID()))
	theme.At(c, r.X, r.Y, r.W, r.H)
	id := c.BoxKeyed(base.Floating, b.key, nil)
	c.Push(id)
}

func (b *blk) end() { b.c.Pop() }

// at places the next box, relative to the block (pixels).
func (b *blk) at(x, y, w, h float32) {
	b.c.NextPos(x, y)
	b.c.NextSize(ui.Px(w), ui.Px(h))
	b.c.NextFlags(base.Floating)
}

// sub is a floating child box at (x, y) with the size, keyed by n among the
// block's boxes; Push it, build into it, and Pop.
func (b *blk) sub(n uint64, x, y, w, h float32, fl ui.Flags) ui.ID {
	c := b.c
	theme.At(c, x, y, w, h)
	id := c.BoxKeyed(base.Floating|fl, b.kn(n), nil)
	return id
}

// fill draws a coloured rounded rectangle.
func (b *blk) fillBox(n uint64, x, y, w, h, radius float32, col uint32) {
	c := b.c
	theme.At(c, x, y, w, h)
	c.NextBG(col)
	c.NextRadius(radius)
	c.BoxKeyed(base.Floating|ui.DrawBackground, b.kn(n), nil)
}

// ring draws a rounded outline.
func (b *blk) ring(n uint64, x, y, w, h, radius float32, col uint32, lw float32) {
	c := b.c
	theme.At(c, x, y, w, h)
	c.NextBorder(col, lw)
	c.NextRadius(radius)
	c.BoxKeyed(base.Floating|ui.DrawBorder, b.kn(n), nil)
}

// label puts one line of text at (x, y), at most w wide (it fades past
// that); the label's string is its identity among the block's labels.
func (b *blk) label(s string, f fontID, col uint32, x, y, w float32) {
	b.place(x, y, w, f)
	rexui.Label(b.k, s, b.labelOpts(f, col))
}

// labelB is label for bytes (a name, a caption); the bytes are copied.
func (b *blk) labelB(s []byte, f fontID, col uint32, x, y, w float32) {
	b.place(x, y, w, f)
	rexui.LabelBytes(b.k, s, b.labelOpts(f, col))
}

// metaB is labelB for metadata (a size, a count of pages), which Rex sets in
// its metadata face (the UI face, tabular figures), on fCallout's line.
func (b *blk) metaB(s []byte, col uint32, x, y, w float32) {
	b.place(x, y, w, fCallout)
	o := b.labelOpts(fCallout, col)
	o.MetaPt = 11
	rexui.LabelBytes(b.k, s, o)
}

func (b *blk) place(x, y, w float32, f fontID) {
	b.c.NextPos(x, y)
	b.k.NextSize(ui.Px(w), ui.Px(b.lineH(f)))
	b.c.NextFlags(base.Floating)
}

func (b *blk) labelOpts(f fontID, col uint32) rexui.LabelOpts {
	o := rexui.LabelOpts{Color: b.tint(col)}
	switch f {
	case fBold:
		o.Style = rexui.TextSemibold
	case fCallout:
		o.Style = rexui.TextCallout
	case fSub:
		o.Style = rexui.TextSubheadline
	case fFoot:
		o.Style = rexui.TextFootnote
	}
	return o
}

// ---- measuring wrapped text ----

var (
	paraFonts [1]text.FontKey
	paraBuf   text.Para
)

// wrapSize is the size in pixels of s wrapped to w pixels in font f, at most
// maxLines lines (0: no limit), and how many lines that is.
func (b *blk) wrapSize(s []byte, f fontID, w float32, maxLines int) (tw, h float32, lines int) {
	if len(s) == 0 || w < 1 {
		return 0, 0, 0
	}
	paraFonts[0] = b.font(f)
	paraBuf = text.Para{Text: s, Fonts: paraFonts[:], Width: w, MaxLines: maxLines, Ellipsis: maxLines > 0, LineHeight: b.lineH(f)}
	ref := b.e.A.Text.Layout(&paraBuf)
	v := b.e.A.Text.Paragraph(ref)
	return v.Width, v.Height, len(v.Lines)
}

// wrapped builds s wrapped at the box's width in font f and colour col, at
// most maxLines lines with an ellipsis; the box is placed at x, y and w wide
// and h tall (h from wrapHeight). styles is a RichStyles with the fonts.
func (b *blk) wrapped(n uint64, s []byte, f fontID, col uint32, x, y, w, h float32, maxLines int) {
	c := b.c
	st := b.richStyles(f)
	b.at(x, y, w, h)
	c.NextTextColor(col)
	c.RichText(b.kn(n), s, nil, st, ui.RichOpts{MaxLines: maxLines, Ellipsis: maxLines > 0})
}

var richCache struct {
	font text.FontKey
	st   ui.RichStyles
	ok   bool
}

func (b *blk) richStyles(f fontID) *ui.RichStyles {
	fk := b.font(f)
	if !richCache.ok || richCache.font != fk {
		richCache.st = ui.RichStyles{}
		richCache.st.Set(0, ui.RichStyle{Font: fk})
		richCache.font, richCache.ok = fk, true
	}
	return &richCache.st
}

// ---- icons ----

// iconSet is an icon atlas of the block's own, per kit: the kit's atlas is
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
	icDoc icon.ID = iota + icon.NumStandard
	icExternal
	icDownload
	icAlert
	icPin
	icPerson
	icSticker
	icBan
	icPhone
	icCopy
	icList
	icCall
	icCallIn
	icCallOut
	icCallMissed
	icCallOff
	icVideo
	icPlay
	icCircleCheck
	icCircle
	icMap
	icVoteDone
	icPause
	icClock
)

func iconSpecs() []icon.Spec {
	s := func(sym string, fb rune, pt float32, w float32) icon.Spec {
		return icon.Spec{Symbol: sym, Fallback: fb, Pt: pt, Weight: w}
	}
	return []icon.Spec{
		s("doc.text", '▤', 18, 0),
		s("arrow.up.right.square", '↗', 14, 0),
		s("arrow.down.circle", '⬇', 20, 0),
		s("exclamationmark.circle", '!', 20, 0),
		s("mappin.circle.fill", '⌖', 16, 0),
		s("person.crop.circle", '☺', 18, 0),
		s("face.smiling", '☺', 20, 0),
		s("nosign", '⊘', 13, 0),
		s("iphone", '▯', 13, 0),
		s("doc.on.doc", '⧉', 14, 0),
		s("list.bullet", '≡', 14, 0),
		s("phone", '☎', 18, 0),
		s("phone.arrow.down.left", '☎', 18, 0),
		s("phone.arrow.up.right", '☎', 18, 0),
		s("phone.arrow.down.left", '☎', 18, 0.3),
		s("phone.down", '☎', 18, 0),
		s("video", '▶', 18, 0),
		s("play.fill", '▶', 14, 0),
		s("checkmark.circle.fill", '●', 16, 0),
		s("circle", '○', 16, 0),
		s("map", '▦', 18, 0),
		s("checkmark", '✓', 10, 0.56),
		s("pause.fill", '⏸', 14, 0),
		s("clock", '◷', 18, 0),
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

// icon draws icon id in col centred in the box (x, y, w, h). A symbol can
// be taller than the line it is centred on (the sticker pack's, the
// contact's and the place's are, by up to 4.5 px), and its picture is that
// tall: it hangs over the bubble's padding, where the block ends, and moving
// or clipping it would change what is drawn. The layout audit is told so
// for such a glyph.
func (b *blk) icon(id icon.ID, col uint32, x, y, w, h float32) {
	at := b.icons()
	iw, ih := at.Size(id)
	flags, disp := at.Prepare(b.c, id, col)
	if flags == 0 {
		return
	}
	b.c.NextPos(x+(w-iw)/2, y+(h-ih)/2)
	b.c.NextFlags(base.Floating)
	if iw > w+0.5 || ih > h+0.5 {
		b.c.NextAuditExempt(ui.AuditOverflow.Mask())
	}
	b.nic++
	b.c.BoxKeyed(flags, ui.KeyN(b.c.ParentKey(), 7000+b.nic), disp)
}

// stdIcon draws one of the kit's standard icons the same way.
func (b *blk) stdIcon(id icon.ID, col uint32, x, y, w, h float32) {
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

// kn is the key of the n-th box of the current parent.
func (b *blk) kn(n uint64) ui.Key { return ui.KeyN(b.c.ParentKey(), n) }

// lbl is "name###n": a label whose identity is n, built in the frame's arena.
func (b *blk) lbl(name string, n uint64) string {
	return b.c.Fmt().Str(name).Str("###").Uint64(n).String()
}
