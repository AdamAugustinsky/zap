package kinds

import (
	"gonativeui/icon"
	"gonativeui/img"
	"gonativeui/rexui"
	"gonativeui/theme"
	"gonativeui/ui"
	"gonativeui/widget/base"

	"zapfast/abi"
	"zapfast/i18n"
	"zapfast/model"
	"zapfast/state"
	ztheme "zapfast/theme"
	"zapfast/ui/conversation/mkey"
	"zapfast/ui/screen"
)

// Interactive messages: template and button messages with an image and
// option rows, list buttons, and carousels (zf:src/ui/conversation.rs
// interactive_buttons, carousel, carousel_picture, carousel_arrow).

const (
	buttonMinH   = 44.0
	buttonPadY   = 22.0
	buttonInset  = 48.0 // the label's share of the row's width not available to it
	phoneNoteGap = 4.0
	cardPadX     = ztheme.BubblePadY // the card's corners nest its picture as a bubble's do
	cardPadTop   = ztheme.BubblePadY
	cardPadBot   = ztheme.BubblePadY
	cardRadius   = ztheme.BubbleRadius
	cardGapY     = ztheme.Space2
	arrowSize    = 40.0
	bodyMaxLines = 8
)

func (b *blk) cardOf() (*abi.InteractiveCard, bool) {
	d := b.decoded()
	return &d.Content.Interactive.Card, d.Content.Interactive.HasCard
}

// ---- buttons ----

// btn is the geometry of one option row.
type btnGeom struct {
	h, lw, lh float32
}

func (b *blk) btnGeom(btn *abi.InteractiveButton, w float32) (g btnGeom) {
	g.lw, g.lh, _ = b.wrapSize(btn.Label, fBold, max(w-b.pt(buttonInset), 1), 2)
	g.h = max(g.lh+b.pt(buttonPadY), b.pt(buttonMinH))
	return g
}

// buttonsHeight is the height of the rows and the "more content" note.
func (b *blk) buttonsHeight(buttons []abi.InteractiveButton, needsPhone bool, w float32) (h float32) {
	for i := range buttons {
		h += b.btnGeom(&buttons[i], w).h
	}
	if needsPhone {
		h += b.pt(phoneNoteGap) + b.lineH(fCallout)
	}
	return h
}

// canSend is chat.can_send() for the message's chat.
func (b *blk) canSend() bool {
	if b.a == nil {
		return false
	}
	ref := b.chatOf(b.decoded().Chat)
	if ref == 0 {
		ref = b.a.Open
	}
	ch, ok := b.a.Model.Chats.Get(ref)
	if !ok {
		return false
	}
	return !ch.Locked() && !ch.ReadOnly() && !ch.Left() && ch.Kind() != abi.ChatKindBroadcast
}

// buttons builds the option rows from y down, in the width w. msg is the id
// replies and the list name (a carousel card's own); it reports the height
// it took.
func (b *blk) buttons(buttons []abi.InteractiveButton, needsPhone bool, y, w float32, msg []byte, nBase uint64) float32 {
	y0 := y
	connected := b.variant()&1 != 0
	canSend := connected && b.canSend()
	pending := b.a != nil && b.loc().replying.has(flightKey(b.a.Serial, b.decoded().Chat, msg))
	for i := range buttons {
		btn := &buttons[i]
		g := b.btnGeom(btn, w)
		enabled := btn.HasURL
		sends := btn.Action.Tag == abi.InteractiveActionTagReply || btn.Action.Tag == abi.InteractiveActionTagSelect
		if !enabled {
			switch btn.Action.Tag {
			case abi.InteractiveActionTagCopy:
				enabled = true
			case abi.InteractiveActionTagReply, abi.InteractiveActionTagSelect:
				enabled = canSend && !b.own && !b.decoded().Edited && !pending
			}
		}
		b.button(btn, i, enabled, sends, pending, canSend, y, w, g, msg, nBase+uint64(i)*8)
		y += g.h
	}
	if needsPhone {
		y += b.pt(phoneNoteGap)
		b.label(i18n.T("More content in WhatsApp Web or on your phone"), fCallout, b.faint(), 0, y, w)
		y += b.lineH(fCallout)
	}
	return y - y0
}

func (b *blk) button(btn *abi.InteractiveButton, i int, enabled, sends, pending, canSend bool, y, w float32, g btnGeom, msg []byte, n uint64) {
	k, c := b.k, b.c
	col := b.dim()
	if enabled {
		col = b.link()
	}
	var ic icon.ID
	switch {
	case btn.HasURL:
		ic = icExternal
	case btn.Action.Tag == abi.InteractiveActionTagSelect:
		ic = icList
	case btn.Action.Tag == abi.InteractiveActionTagCopy:
		ic = icCopy
	case btn.Action.Tag == abi.InteractiveActionTagUnavailable:
		ic = icPhone
	}
	c.NextPos(0, y)
	c.NextFlags(base.Floating)
	pr := k.PressState(c.Fmt().Str(model.S(btn.Label)).Str("###btn").Int(i).String(), base.HoverOn)
	k.BeginDisabled(!enabled)
	k.BeginPress(&pr, ui.RoleButton, ui.Px(w), ui.Px(g.h), 0, nil)
	if enabled && (pr.Hot > 0.01 || pr.Down > 0.01) {
		b.fillBox(n, 0, 0, w, g.h, 0, theme.WithAlpha(b.hoverFill(pr.Down > 0.5), max(pr.Hot, pr.Down)))
	}
	b.fillBox(n+1, 0, 0, w, 1, 0, b.hairline())
	inset := float32(0)
	if ic != 0 {
		inset = b.pt(22)
	}
	cw := min(g.lw+inset, w-b.pt(buttonInset)+inset)
	left := (w - cw) / 2
	if ic != 0 {
		b.icon(ic, col, left, (g.h-b.pt(14))/2, b.pt(14), b.pt(14))
	}
	b.wrapped(n+2, btn.Label, fBold, col, left+inset, (g.h-g.lh)/2, max(cw-inset, 1)+b.pt(2), g.lh, 2)
	sig := b.endHit(&pr, 0)
	k.EndDisabled()
	if !enabled {
		var reason string
		switch {
		case sends && pending:
			reason = i18n.T("Sending reply…")
		case sends && b.variant()&1 == 0:
			reason = i18n.T("Connect to WhatsApp to reply")
		case sends && !canSend:
			reason = i18n.T("This conversation is read-only")
		case sends && b.own:
			reason = i18n.T("Reply options are for the recipient")
		default:
			reason = i18n.T("Open this option in WhatsApp Web or on your phone")
		}
		rexui.Tooltip(k, pr.ID, c.Fmt().Str(model.S(btn.Label)).Byte('\n').Str(reason).String())
		return
	}
	switch {
	case btn.HasURL:
		rexui.Tooltip(k, pr.ID, c.Fmt().Str(model.S(btn.Label)).Byte('\n').Str(model.S(btn.URL)).String())
		if sig.Clicked {
			b.openURL(string(btn.URL))
		}
	case btn.Action.Tag == abi.InteractiveActionTagReply:
		rexui.Tooltip(k, pr.ID, expand(c.Fmt(), i18n.T("Send reply: {label}"), phBytes("label", btn.Label)).String())
		if sig.Clicked {
			b.reply(msg, uint64(i), 0, false)
		}
	case btn.Action.Tag == abi.InteractiveActionTagCopy:
		rexui.Tooltip(k, pr.ID, i18n.T("Copy code"))
		if sig.Clicked {
			if c.ClipboardSet != nil {
				c.ClipboardSet(btn.Action.Copy.Code)
			}
			b.e.S.Toast(base.StrBytes(i18n.T("Copied")), false)
		}
	case btn.Action.Tag == abi.InteractiveActionTagSelect:
		if sig.Clicked && b.a != nil {
			d := state.Dialog{Kind: state.DialogInteractiveList, Chat: b.a.Open, N: i}
			if ref := b.chatOf(b.decoded().Chat); ref != 0 {
				d.Chat = ref
			}
			d.IDs.Add(msg)
			b.e.S.OpenDialog(d)
		}
	}
}

// reply sends a button's reply (Action::ReplyInteractive).
func (b *blk) reply(msg []byte, button, choice uint64, hasChoice bool) {
	if b.a == nil || b.variant()&1 == 0 || !b.canSend() {
		return
	}
	cmd := b.a.Cmd(abi.CommandTagReplyInteractive)
	cmd.ReplyInteractive.Chat, cmd.ReplyInteractive.Message = b.decoded().Chat, msg
	cmd.ReplyInteractive.Button = button
	cmd.ReplyInteractive.Choice, cmd.ReplyInteractive.HasChoice = choice, hasChoice
	b.a.Send(cmd)
}

// ---- the single card: its image over its rows ----

// cardPic is the card's image as a picture.
func (b *blk) cardPic(card *abi.InteractiveCard, n int) pic {
	md := &card.Image
	p := pic{mime: md.Mime, size: md.Size, state: md.State.Tag, err: md.State.Failed.Error, card: n,
		sticker: false}
	if md.HasPath {
		p.path = md.Path
	}
	if md.HasWidth {
		p.w = md.Width
	}
	if md.HasHeight {
		p.h = md.Height
	}
	if card.HasThumbnail {
		p.thumb, p.hasThumb = card.Thumbnail, true
	} else if ref, ok := b.m.Thumbnail(); ok && n < 0 {
		p.thumb, p.hasThumb = ref, true
	}
	return p
}

type cardGeom struct {
	w, pic, buttons, h float32
}

func (b *blk) cardGeom(card *abi.InteractiveCard, maxW float32) (g cardGeom) {
	g.w = b.cardW(maxW)
	if len(card.Carousel) > 0 {
		return g
	}
	if card.HasImage {
		p := b.cardPic(card, -1)
		_, g.pic = b.fitPicture(b.pt(float32(p.w)), b.pt(float32(p.h)), min(g.w, b.pt(pictureWidth)), b.pt(pictureHeight))
		g.h += g.pic + b.pt(cardGapY)
	}
	g.buttons = b.buttonsHeight(card.Buttons, card.NeedsPhone, g.w)
	g.h += g.buttons
	return g
}

func (b *blk) interactiveSize(maxW float32) (w, h float32) {
	card, _ := b.cardOf()
	if len(card.Carousel) > 0 {
		return b.carouselSize(card, maxW)
	}
	g := b.cardGeom(card, maxW)
	return g.w, g.h
}

func (b *blk) interactiveBuild(r screen.Rect) {
	card, _ := b.cardOf()
	if len(card.Carousel) > 0 {
		b.carouselBuild(card, r)
		return
	}
	g := b.cardGeom(card, r.W)
	y := float32(0)
	if card.HasImage {
		p := b.cardPic(card, -1)
		pw, ph := b.fitPicture(b.pt(float32(p.w)), b.pt(float32(p.h)), min(r.W, b.pt(pictureWidth)), b.pt(pictureHeight))
		b.picture(&p, 0, 0, pw, ph, b.pt(6), 1)
		y += g.pic + b.pt(cardGapY)
	}
	b.buttons(card.Buttons, card.NeedsPhone, y, r.W, b.m.ID(), 200)
}

// ---- carousels ----

type carGeom struct {
	cw, card, h float32 // card content width, card width, strip height
	pic         float32
	n           int
	stepGap     float32
}

func (b *blk) carGeom(card *abi.InteractiveCard, w float32) (g carGeom) {
	inset := float32(20)
	if len(card.Carousel) > 1 {
		inset = 36
	}
	g.cw = min(b.pt(carouselCard), max(w-b.pt(inset), b.pt(140)))
	g.card = g.cw + 2*b.pt(cardPadX)
	g.pic = float32(int(g.cw * 0.56))
	g.n = len(card.Carousel)
	g.stepGap = b.pt(carouselGap)
	for i := range card.Carousel {
		cd := &card.Carousel[i]
		h := b.pt(cardPadTop) + b.pt(cardPadBot)
		if cd.HasImage {
			h += g.pic + b.pt(cardGapY)
		}
		if len(cd.Body) > 0 {
			_, bh, _ := b.wrapSize(cd.Body, fBody, g.cw, bodyMaxLines)
			h += bh + b.pt(cardGapY)
		}
		h += b.buttonsHeight(cd.Buttons, cd.NeedsPhone, g.card)
		g.h = max(g.h, h)
	}
	return g
}

// carouselSize: short carousels keep their cards together; wider strips take
// the width and scroll.
func (b *blk) carouselSize(card *abi.InteractiveCard, maxW float32) (w, h float32) {
	g := b.carGeom(card, maxW)
	all := float32(g.n)*g.card + float32(g.n-1)*g.stepGap
	return min(all, maxW), g.h
}

var scrolls struct {
	m map[mkey.K]*base.ScrollState
}

func (b *blk) carouselBuild(card *abi.InteractiveCard, r screen.Rect) {
	k, c := b.k, b.c
	g := b.carGeom(card, r.W)
	if scrolls.m == nil || len(scrolls.m) > 256 {
		scrolls.m = make(map[mkey.K]*base.ScrollState)
	}
	id := mkey.Of(b.a, b.m.ID())
	st := scrolls.m[id]
	if st == nil {
		st = new(base.ScrollState)
		scrolls.m[id] = st
	}
	total := float32(g.n)*g.card + float32(g.n-1)*g.stepGap
	c.NextPos(0, 0)
	c.NextFlags(base.Floating)
	k.NextSize(ui.Px(r.W), ui.Px(r.H))
	sv := k.BeginScroll(i18n.T("Carousel"), st, base.ScrollHorizontal)
	b.shiftWheel(sv.Inner, r)
	// the strip: the cards side by side
	c.NextSize(ui.Px(total), ui.Px(r.H))
	c.NextAxis(ui.AxisX)
	c.NextGap(g.stepGap)
	c.Push(c.BoxKeyed(0, b.kn(1), nil))
	for i := range card.Carousel {
		b.carouselCard(card, i, &g, r.H)
	}
	c.Pop()
	k.CloseScrollContent(&sv)
	k.CloseScroll(&sv)
	// the arrows, over the strip's ends, while there is more to see
	content, view := c.ScrollExtent(sv.Inner)
	x, _ := c.Scroll(sv.Inner)
	if limit := float64(content[0] - view[0]); limit > 1 {
		step := float64(g.card + g.stepGap)
		if x > 1 {
			if b.arrow(true, r) {
				c.ScrollTo(sv.Inner, max(x-step, 0), 0)
			}
		}
		if x < limit-1 {
			if b.arrow(false, r) {
				c.ScrollTo(sv.Inner, min(x+step, limit), 0)
			}
		}
	}
}

// shiftWheel scrolls the strip sideways with Shift and the wheel, for mice
// that send a vertical wheel only.
func (b *blk) shiftWheel(inner ui.ID, r screen.Rect) {
	c := b.c
	in := c.Input()
	if in.Mods&ui.ModShift == 0 || in.ScrollY == 0 || in.ScrollX != 0 || !in.Inside {
		return
	}
	box := c.Rect(inner)
	if in.MouseX < box[0] || in.MouseX > box[2] || in.MouseY < box[1] || in.MouseY > box[3] {
		return
	}
	step := float64(1)
	if !in.Precise {
		step = float64(c.WheelStep)
	}
	x, _ := c.Scroll(inner)
	c.ScrollTo(inner, max(x-float64(in.ScrollY)*step, 0), 0)
}

func (b *blk) arrow(previous bool, r screen.Rect) bool {
	k, c := b.k, b.c
	d := b.pt(arrowSize)
	x := b.pt(26) - d/2
	ic, name := icon.ChevronLeft, i18n.T("Previous card")
	if !previous {
		x = r.W - b.pt(26) - d/2
		ic, name = icon.ChevronRight, i18n.T("Next card")
	}
	c.NextPos(x, (r.H-d)/2)
	c.NextFlags(base.Floating)
	k.NextSize(ui.Px(d), ui.Px(d))
	return rexui.IconButton(k, ic, name, rexui.IconButtonOpts{Size: theme.SizeLarge}).Clicked
}

// carouselCard builds card i of the strip.
func (b *blk) carouselCard(card *abi.InteractiveCard, i int, g *carGeom, h float32) {
	c := b.c
	cd := &card.Carousel[i]
	c.NextSize(ui.Px(g.card), ui.Px(h))
	c.NextBG(b.card())
	c.NextRadius(b.pt(cardRadius))
	cid := c.BoxKeyed(ui.DrawBackground|ui.Clip, b.kn(10+uint64(i)), nil)
	c.Push(cid)
	y := b.pt(cardPadTop)
	px := b.pt(cardPadX)
	if cd.HasImage {
		p := b.carouselPic(card, cd, i)
		b.picture(&p, px, y, g.cw, g.pic, b.pt(ztheme.Nested()), 1)
		y += g.pic + b.pt(cardGapY)
	}
	if len(cd.Body) > 0 {
		_, bh, _ := b.wrapSize(cd.Body, fBody, g.cw, bodyMaxLines)
		b.wrapped(2, cd.Body, fBody, b.ink(), px, y, g.cw, bh, bodyMaxLines)
		y += bh + b.pt(cardGapY)
	}
	// the card's rows answer for the card: its message is the parent's id and -card-i
	cardMsg := c.Fmt().Str(model.S(b.m.ID())).Str("-card-").Int(i).Bytes()
	b.cardButtons(cd, y, g.card, cardMsg, uint64(300+i*64))
	c.Pop()
}

func (b *blk) cardButtons(cd *abi.CarouselCard, y, w float32, msg []byte, n uint64) {
	// a card carries the fields the rows need; buttons reads them the same way
	b.buttons(cd.Buttons, cd.NeedsPhone, y, w, msg, n)
}

func (b *blk) carouselPic(card *abi.InteractiveCard, cd *abi.CarouselCard, i int) pic {
	md := &cd.Image
	p := pic{mime: md.Mime, size: md.Size, state: md.State.Tag, err: md.State.Failed.Error, card: i}
	if md.HasPath {
		p.path = md.Path
	}
	if md.HasWidth {
		p.w = md.Width
	}
	if md.HasHeight {
		p.h = md.Height
	}
	if cd.HasThumbnail {
		p.thumb, p.hasThumb = cd.Thumbnail, true
	}
	return p
}

var _ = img.Cover
