package kinds

import (
	"bytes"

	"gonativeui/img"
	"gonativeui/rexui"
	"gonativeui/theme"
	"gonativeui/ui"
	"gonativeui/widget/base"

	"zapfast/abi"
	"zapfast/i18n"
	"zapfast/model"
	ztheme "zapfast/theme"
	"zapfast/ui/conversation/av"
	"zapfast/ui/screen"
)

// Photos, stickers, GIFs and the pictures of interactive cards
// (zf:src/ui/conversation.rs: picture, motion_clip, carousel_picture,
// fit_picture, fit_sticker).

// pic is a picture to draw: where its file is (once downloaded), what the
// core is doing about it, and its inline thumbnail.
type pic struct {
	path     []byte
	mime     []byte
	size     uint64
	w, h     uint32 // pixels, 0 unknown
	state    abi.MediaStateTag
	err      []byte
	thumb    abi.BlobRef
	hasThumb bool
	card     int // the card of a carousel, -1 for the message's own picture
	animated bool
	sticker  bool
}

// picOf is the message's own picture (an image or a sticker).
func (b *blk) picOf() pic {
	md, _ := b.m.Media()
	p := pic{path: md.Path, mime: md.Mime, size: md.Size, w: md.Width, h: md.Height, state: md.State, thumb: md.Thumb,
		hasThumb: md.HasThumb, card: -1, animated: md.Animated, sticker: b.m.Kind() == abi.ContentTagSticker}
	if md.State == abi.MediaStateTagFailed {
		p.err = b.decoded().Content.Image.Media.State.Failed.Error
		if p.sticker {
			p.err = b.decoded().Content.Sticker.Media.State.Failed.Error
		}
	}
	return p
}

// movingMime reports a sticker or picture file that may animate: GIF, WebP
// and APNG. Phones leave a sticker's animated flag out at times, so a WebP
// is asked for as an animation whatever the flag says.
func movingMime(mime []byte) bool {
	return bytes.Contains(mime, []byte("gif")) || bytes.Contains(mime, []byte("webp")) || bytes.Contains(mime, []byte("apng"))
}

// stickerFit is fit_sticker: the sticker's shape inside 180 points square,
// enlarged to fill it.
func (b *blk) stickerFit(w, h uint32) (float32, float32) {
	fw, fh := float32(w), float32(h)
	if w == 0 || h == 0 {
		fw, fh = 1, 1
	}
	s := min(b.pt(stickerSide)/fw, b.pt(stickerSide)/fh)
	return fw * s, fh * s
}

func (b *blk) pictureSize(maxW float32) (w, h float32) {
	p := b.picOf()
	return b.fitPicture(b.pt(float32(p.w)), b.pt(float32(p.h)), min(maxW, b.pt(pictureWidth)), b.pt(pictureHeight))
}

func (b *blk) stickerSize() (w, h float32) {
	p := b.picOf()
	return b.stickerFit(p.w, p.h)
}

func (b *blk) pictureBuild(r screen.Rect) {
	p := b.picOf()
	radius := b.pt(ztheme.Nested())
	if b.flush {
		radius = b.pt(ztheme.T.BubbleRadius)
	}
	b.picture(&p, 0, 0, r.W, r.H, radius, 1)
	d := b.decoded()
	if d.Content.Image.HasMotion {
		// av plays the clip over the photo while it is wanted
		st := av.Motion(b.e, b.m, screen.Rect{W: r.W, H: r.H}, radius)
		b.motionBadge(&d.Content.Image.Motion, st)
	}
}

func (b *blk) stickerBuild(r screen.Rect) {
	p := b.picOf()
	b.picture(&p, 0, 0, r.W, r.H, 0, 1)
}

// picName is the picture button's accessible name.
func picName(p *pic, hasFile bool) string {
	switch {
	case p.card >= 0 && hasFile:
		return i18n.T("Open card image")
	case p.card >= 0 && p.state == abi.MediaStateTagFailed:
		return i18n.T("Retry card image download")
	case p.card >= 0 && p.state == abi.MediaStateTagDownloading:
		return i18n.T("Downloading card image")
	case p.card >= 0:
		return i18n.T("Download card image")
	case hasFile && p.sticker:
		return i18n.T("Sticker")
	case hasFile:
		return i18n.T("Photo")
	case p.state == abi.MediaStateTagFailed:
		return i18n.T("Retry download")
	case p.state == abi.MediaStateTagDownloading:
		return i18n.T("Downloading")
	case p.sticker:
		return i18n.T("Download sticker")
	}
	return i18n.T("Download photo")
}

// picture draws p in the box (x, y, w, h) and does what a click on it does
// (Action::PreviewImage, Action::OpenFile, Action::Download); n tells the
// block's pictures apart.
func (b *blk) picture(p *pic, x, y, w, h, radius float32, n uint64) {
	k, c := b.k, b.c
	hasFile := len(p.path) > 0
	// the press box: one named button over the picture
	c.NextPos(x, y)
	c.NextFlags(base.Floating)
	pr := k.PressState(b.lbl(picName(p, hasFile), n), base.HoverOn)
	id := k.BeginPress(&pr, ui.RoleButton, ui.Px(w), ui.Px(h), 0, nil)
	fl := img.Flags(0)
	fit := ui.FitFill
	if p.sticker || p.card >= 0 {
		fit = ui.FitContain
	}
	if p.card >= 0 {
		fl = img.Cover
		fit = ui.FitFill
	}
	moving := p.animated || (p.sticker && movingMime(p.mime)) || bytes.Contains(p.mime, []byte("gif"))
	if moving {
		fl |= img.Animate
	}
	pw, ph := int32(w), int32(h)
	failed := false
	base0 := uint64(0)
	// the thumbnail first, under whatever comes over it
	showedThumb := false
	if p.hasThumb && !p.sticker {
		if hd, ok := b.thumbImage(p.thumb, pw, ph, fl&^img.Animate); ok && hd != 0 {
			b.imageBox(base0+1, hd, 0, 0, w, h, radius, ui.FitFill)
			showedThumb = true
		}
	}
	if hasFile {
		hd, st := b.file(p.path, pw, ph, fl)
		switch st {
		case img.Ready:
			b.imageBox(base0+2, hd, 0, 0, w, h, radius, fit)
		case img.Failed:
			failed = true
		default:
			if hd != 0 {
				b.imageBox(base0+2, hd, 0, 0, w, h, radius, fit) // skeleton until it is ready
			}
			if !showedThumb {
				b.fillBox(base0+3, 0, 0, w, h, radius, b.col(theme.Fill3))
			}
			b.spinner(base0+4, w/2, h/2)
		}
		if failed {
			b.fillBox(base0+5, 0, 0, w, h, radius, b.col(theme.Fill3))
			b.icon(icAlert, b.col(theme.Red), 0, h/2-b.pt(30), w, b.pt(24))
			b.pill(base0+6, i18n.T("Could not display this picture. Click to open it."), w/2, h/2+b.pt(4), w)
		}
	} else {
		if showedThumb {
			b.fillBox(base0+3, 0, 0, w, h, radius, 0x0000003C) // upstream's 60/255 black over the preview
		} else {
			b.fillBox(base0+3, 0, 0, w, h, radius, b.col(theme.Fill3))
		}
		b.transfer(base0+7, p, w, h)
	}
	if k.Scale() > 0 {
		k.FocusRing(pr.ID, pr.Visual, radius)
	}
	sig := k.EndPress(&pr)
	if !hasFile && p.state == abi.MediaStateTagFailed && len(p.err) > 0 && c.Signal(id).Hovering {
		// the reason, over this item alone (an album's others go on)
		rexui.Tooltip(k, id, c.Fmt().Str(model.S(p.err)).Str(" · Click to retry").String())
	}
	b.pictureClicked(p, sig.Clicked, hasFile, failed)
}

// pictureClicked acts on a click, and starts the automatic download of a
// picture that is on screen.
func (b *blk) pictureClicked(p *pic, clicked, hasFile, failed bool) {
	switch {
	case hasFile:
		if !clicked {
			return
		}
		switch {
		case failed:
			b.openFile(p.path)
		case p.card >= 0 && viewable(p.path):
			b.openViewer(p.card) // a carousel card's picture, by its index
		case p.sticker || viewable(p.path):
			b.openViewer(model.PartMain)
		default:
			b.openFile(p.path) // a GIF animates in the system's viewer
		}
	case p.state != abi.MediaStateTagDownloading:
		if clicked {
			b.download(true, p.card)
		} else if p.state == abi.MediaStateTagIdle && b.autoDownload(p.size, p.sticker) {
			b.download(false, p.card)
		}
	}
}

// imageBox is an image of the handle in a box.
func (b *blk) imageBox(n uint64, hd img.Handle, x, y, w, h, radius float32, fit ui.ImageFit) {
	c := b.c
	c.NextPos(x, y)
	c.NextSize(ui.Px(w), ui.Px(h))
	c.NextFlags(base.Floating)
	b.k.Image(n, hd, fit, radius, "")
}

// spinner is a small spinner at (cx, cy) on a disc, shown while something
// loads.
func (b *blk) spinner(n uint64, cx, cy float32) {
	d := b.pt(34)
	b.fillBox(n*8+100, cx-d/2, cy-d/2, d, d, d/2, theme.WithAlpha(b.col(theme.Window), 0.85))
	s := b.pt(16)
	b.c.NextPos(cx-s/2, cy-s/2)
	b.c.NextFlags(base.Floating)
	rexui.Spinner(b.k, "Loading###spin", rexui.SpinnerOpts{Size: theme.SizeSmall})
}

// pill is a line of white text on a dark pill, centred on cx.
func (b *blk) pill(n uint64, s string, cx, y, maxW float32) {
	tw := min(b.widthS(fSub, s), maxW-b.pt(16))
	w, h := tw+b.pt(16), b.lineH(fSub)+b.pt(6)
	b.fillBox(n*8+101, cx-w/2, y, w, h, h/2, 0x00000099)
	b.place(cx-tw/2, y+b.pt(3), tw, fSub)
	rexui.Label(b.k, s, rexui.LabelOpts{Style: rexui.TextSubheadline, Color: rexui.OfRGBA(0xFFFFFFFF)})
}

// transfer is the badge over a picture that is not downloaded: the arrow and
// the size, the spinner while it loads, the alert and the reason after a
// failure.
func (b *blk) transfer(n uint64, p *pic, w, h float32) {
	d := b.pt(44)
	cx, cy := w/2, h/2
	switch p.state {
	case abi.MediaStateTagDownloading:
		b.spinner(n, cx, cy)
	case abi.MediaStateTagFailed:
		b.fillBox(n*8+102, cx-d/2, cy-d/2, d, d, d/2, 0x00000078)
		b.icon(icAlert, b.col(theme.Red), cx-d/2, cy-d/2, d, d)
		b.pill(n, i18n.T("Download failed. Click to retry."), cx, cy+b.pt(30), w)
	default:
		b.fillBox(n*8+102, cx-d/2, cy-d/2, d, d, d/2, 0x00000078)
		ic := icDownload
		if p.sticker {
			ic = icSticker
		}
		b.icon(ic, 0xFFFFFFFF, cx-d/2, cy-d/2, d, d)
		if !p.sticker && p.card < 0 {
			b.pill(n, byteSize(b.c.Fmt(), p.size), cx, cy+b.pt(30), w)
		}
	}
}

// byteSize is util::bytes: "12 B", "1.5 MB".
func byteSize(f *ui.Fmt, n uint64) string { return byteSizeTo(f, n).String() }

func byteSizeTo(f *ui.Fmt, n uint64) *ui.Fmt {
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

// motionBadge is a motion photo's badge in the picture's corner
// (motion_clip): a click plays the clip over the photo, muted and looping,
// or goes back to the photo (av.ToggleMotion, which downloads the clip
// first if need be). It pauses while the clip plays, and spins while the
// clip downloads or loads.
func (b *blk) motionBadge(mo *abi.Motion, st av.MotionState) {
	k, c := b.k, b.c
	size := b.pt(28)
	name := i18n.T("Play motion photo")
	waiting := mo.State.Tag == abi.MediaStateTagDownloading || st == av.MotionWaiting
	switch {
	case st == av.MotionPlaying:
		name = i18n.T("Pause")
	case waiting:
		name = i18n.T("Downloading motion photo")
	}
	c.NextPos(b.pt(6), b.pt(6))
	c.NextFlags(base.Floating)
	pr := k.PressState(b.lbl(name, 0), base.HoverOn)
	k.BeginPress(&pr, ui.RoleButton, ui.Px(size), ui.Px(size), 0, nil)
	b.fillBox(300, 0, 0, size, size, size/2, 0x0000008C)
	switch {
	case st == av.MotionPlaying:
		b.icon(icPause, 0xFFFFFFFF, 0, 0, size, size)
	case waiting:
		rexui.Spinner(k, i18n.T("Loading")+"###spin", rexui.SpinnerOpts{Size: theme.SizeSmall})
	case mo.State.Tag == abi.MediaStateTagFailed:
		b.icon(icAlert, b.col(theme.Red), 0, 0, size, size)
	default:
		b.icon(icPlay, 0xFFFFFFFF, 0, 0, size, size)
	}
	k.FocusRing(pr.ID, pr.Visual, size/2)
	if k.EndPress(&pr).Clicked && b.a != nil {
		av.ToggleMotion(b.e, b.m)
	}
}
