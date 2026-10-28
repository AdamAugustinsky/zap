// Package kinds draws the content blocks of messages that are not plain
// text, inside the transcript's bubbles: photos, stickers, GIFs, documents,
// locations, contact cards, link previews, interactive messages (buttons,
// lists, carousels), polls, and the placeholders for deleted and system
// messages. Voice notes and videos are package av.
//
// It ports those parts of zf:src/ui/conversation.rs, zf:src/ui/polls.rs and
// zf:src/ui/locations.rs. The transcript (package conversation) owns the
// bubble, the sender line, the caption text, reactions and the timestamp; it
// asks this package for the block's size and has it build the block.
//
// # What a block is
//
// A block is the part of a message upstream draws above (or, for buttons,
// below) its text: the picture, the card, the poll's options, the buttons.
// The text the transcript draws is Message.Text(): a photo's caption, a
// poll's question, a link preview's message, a sticker pack's caption. Where
// that text goes, relative to the block, is Caption.
//
// # Contract with the transcript
//
//   - Has(m) says whether the message has a block; Size and Build are called
//     only for those. Videos and audio are not mine (package av), though an
//     animated GIF sent as an image is.
//   - Size(e, m, maxW) is cheap and allocates nothing once warm: it is cached
//     per message, generation, width and scale.
//   - Build(e, m, r) builds the block in r and handles its input. r is in
//     the coordinates of the box being built (ui.Context.NextPos): the
//     transcript pushes a box of the size Size gave and passes {0, 0, w, h};
//     at the window's root they are window pixels. It asks the core for
//     downloads that upstream starts when a block comes into view, so call
//     it only for messages that are on screen.
//   - Caption(m) tells where the text goes: under the block (the default),
//     above it (a poll's question, an interactive message's body) or nowhere
//     (the block draws the text itself: a contact's name, a location's
//     name, an unsupported message's description).
//   - NoBubble(m) tells the transcript to draw the message without a bubble
//     round it.
package kinds

import (
	"math"

	"zapfast/abi"
	"zapfast/model"
	"zapfast/ui/conversation/mkey"
	"zapfast/ui/screen"
)

// Place is where the transcript draws Message.Text() relative to the block.
type Place uint8

const (
	// Below: under the block (a photo's caption). The zero value.
	Below Place = iota
	// Above: over the block (a poll's question, an interactive message's body).
	Above
	// Nowhere: the block draws the text itself.
	Nowhere
)

// Caption is where the message's text goes.
func Caption(m model.Message) Place {
	switch m.Kind() {
	case abi.ContentTagPoll, abi.ContentTagInteractive:
		return Above
	case abi.ContentTagLocation, abi.ContentTagContact, abi.ContentTagUnsupported:
		return Nowhere
	}
	return Below
}

// Has reports whether m has a content block drawn by this package.
func Has(m model.Message) bool {
	switch m.Kind() {
	case abi.ContentTagImage, abi.ContentTagSticker, abi.ContentTagStickerPack, abi.ContentTagDocument,
		abi.ContentTagLocation, abi.ContentTagLiveLocation, abi.ContentTagContact, abi.ContentTagPoll,
		abi.ContentTagRevoked, abi.ContentTagUnsupported, abi.ContentTagPhoneOnly, abi.ContentTagCall:
		return true
	case abi.ContentTagText, abi.ContentTagInteractive:
		return cachedHas(m)
	}
	return false
}

// Size is the block's size in pixels for a bubble whose content is at most
// maxW pixels wide. It is called while the transcript lays out, possibly for
// messages that are not on screen, and must not allocate once warm.
func Size(e *screen.Env, m model.Message, maxW float32) (w, h float32) { return SizeIn(e, m, maxW, 0) }

// SizeIn is Size in a bubble that settled at span pixels wide, its text's
// width (zf:src/ui/conversation.rs settled_width): a card (Card) spans it
// when it is wider than the card's own width, up to maxW.
func SizeIn(e *screen.Env, m model.Message, maxW, span float32) (w, h float32) {
	mkey.Enter(e.Acct()) // the caches Has asks are the account's
	if !Has(m) || maxW < 1 {
		return 0, 0
	}
	b := use(e, m)
	defer b.done()
	b.span = span
	id, gen, variant := mkey.Of(b.a, m.ID()), m.Gen(), b.variant()
	slot := &sizes[id.Slot(len(sizes))]
	if slot.ok && slot.id == id && slot.gen == gen && slot.variant == variant && slot.maxW == maxW && slot.span == span && slot.sc == b.sc {
		return slot.w, slot.h
	}
	w, h = b.measure(maxW)
	*slot = sizeEnt{ok: true, id: id, gen: gen, variant: variant, maxW: maxW, span: span, sc: b.sc, w: w, h: h}
	return w, h
}

// Card reports whether m's block is a card, which spans the bubble's
// settled width (SizeIn): a link preview, a document, a poll, an
// interactive message's card.
func Card(m model.Message) bool {
	switch m.Kind() {
	case abi.ContentTagDocument, abi.ContentTagPoll:
		return true
	case abi.ContentTagText:
		return cachedHas(m)
	case abi.ContentTagInteractive:
		return hasOf(m) == hasYes
	}
	return false
}

// NoBubble reports whether m is drawn without a bubble round it: a sticker
// and a carousel (zf:src/ui/conversation.rs bubbleless). Round video
// messages are package av's.
func NoBubble(m model.Message) bool {
	switch m.Kind() {
	case abi.ContentTagSticker:
		return true
	case abi.ContentTagInteractive:
		return hasOf(m) == hasCarousel
	}
	return false
}

// Build builds the block at r, in the coordinates of the box being built
// (see the package's doc), and handles its input.
func Build(e *screen.Env, m model.Message, r screen.Rect) {
	mkey.Enter(e.Acct())
	if !Has(m) || r.W < 1 || r.H < 1 {
		return
	}
	b := use(e, m)
	defer b.done()
	b.span = r.W // Size's width: a card fills it
	b.begin(r)
	b.paint(r)
	b.end()
}

// sizeEnt is one slot of the size cache.
type sizeEnt struct {
	ok       bool
	variant  uint8
	id       mkey.K
	gen      uint32
	maxW, sc float32
	span     float32
	w, h     float32
}

var sizes [2048]sizeEnt

// hasEnt is one slot of the Has cache: whether a text or interactive
// message carries a link preview or a card, and whether the card is a
// carousel.
type hasEnt struct {
	id  mkey.K
	gen uint32
	v   uint8 // 0 empty, else hasNo, hasYes or hasCarousel
}

const (
	hasNo uint8 = 1 + iota
	hasYes
	hasCarousel
)

var (
	hases  [2048]hasEnt
	hasDec abi.Message
)

func cachedHas(m model.Message) bool { return hasOf(m) >= hasYes }

func hasOf(m model.Message) uint8 {
	id, gen := mkey.Here(m.ID()), m.Gen()
	slot := &hases[id.Slot(len(hases))]
	if slot.v != 0 && slot.id == id && slot.gen == gen {
		return slot.v
	}
	// One decoded message, reused: a miss (the cache is smaller than a long
	// transcript) must not allocate either.
	d := &hasDec
	v := hasNo
	if m.Decode(d) == nil {
		switch d.Content.Tag {
		case abi.ContentTagText:
			if d.Content.Text.HasPreview {
				v = hasYes
			}
		case abi.ContentTagInteractive:
			if in := &d.Content.Interactive; in.HasCard && len(in.Card.Carousel) > 0 {
				v = hasCarousel
			} else if in.HasCard {
				v = hasYes
			}
		}
	}
	d.Release()
	slot.id, slot.gen, slot.v = id, gen, v
	return v
}

// measure is the block's size for maxW.
func (b *blk) measure(maxW float32) (w, h float32) {
	switch b.m.Kind() {
	case abi.ContentTagImage:
		return b.pictureSize(maxW)
	case abi.ContentTagSticker:
		return b.stickerSize()
	case abi.ContentTagDocument:
		return b.documentSize(maxW)
	case abi.ContentTagText:
		return b.previewSize(maxW)
	case abi.ContentTagLocation, abi.ContentTagLiveLocation:
		return b.locationSize(maxW)
	case abi.ContentTagContact:
		return b.contactSize(maxW)
	case abi.ContentTagStickerPack:
		return b.packSize(maxW)
	case abi.ContentTagPoll:
		return b.pollSize(maxW)
	case abi.ContentTagInteractive:
		return b.interactiveSize(maxW)
	}
	return b.noteSize(maxW)
}

func (b *blk) paint(r screen.Rect) {
	switch b.m.Kind() {
	case abi.ContentTagImage:
		b.pictureBuild(r)
	case abi.ContentTagSticker:
		b.stickerBuild(r)
	case abi.ContentTagDocument:
		b.documentBuild(r)
	case abi.ContentTagText:
		b.previewBuild(r)
	case abi.ContentTagLocation, abi.ContentTagLiveLocation:
		b.locationBuild(r)
	case abi.ContentTagContact:
		b.contactBuild(r)
	case abi.ContentTagStickerPack:
		b.packBuild(r)
	case abi.ContentTagPoll:
		b.pollBuild(r)
	case abi.ContentTagInteractive:
		b.interactiveBuild(r)
	default:
		b.noteBuild(r)
	}
}

// variant is what besides the message and the width changes a block's size.
func (b *blk) variant() uint8 {
	var v uint8
	if b.a != nil && b.a.Model.Link.Connected() {
		v |= 1
	}
	return v
}

// ---- shared geometry (points, as upstream's constants) ----

const (
	cardWidth     = 320.0 // CARD_WIDTH
	carouselCard  = 280.0 // CAROUSEL_CARD_WIDTH
	locationWidth = 300.0 // LOCATION_CARD_WIDTH
	carouselGap   = 8.0   // CAROUSEL_GAP
	pictureWidth  = 320.0
	pictureHeight = 440.0
	stickerSide   = 180.0
)

// cardW is the width of the card-shaped blocks: upstream's floor, 320 points,
// or all there is.
func (b *blk) cardW(maxW float32) float32 { return min(max(b.pt(cardWidth), b.span), maxW) }

// fitPicture is upstream's fit_picture in pixels: within the bounds, never
// enlarged, at least 120 points wide and 90 high.
func (b *blk) fitPicture(w, h, maxW, maxH float32) (float32, float32) {
	if w <= 0 || h <= 0 {
		w, h = 4, 3
	}
	maxW, maxH = max(maxW, 0), max(maxH, 0)
	scale := float32(math.Min(float64(maxW/w), float64(maxH/h)))
	scale = min(max(scale, 0), 1)
	if w*scale < b.pt(120) {
		scale = max(min(b.pt(120)/w, maxW/w), 0)
	}
	return max(w*scale, 0), max(max(h*scale, b.pt(90)), 0)
}
