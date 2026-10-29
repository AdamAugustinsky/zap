package kinds

import (
	"bytes"
	"path/filepath"

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
	"zapfast/ui/screen"
)

// ---- a pressable region ----

// beginHit starts a button over the box (x, y, w, h): build its content,
// then endHit.
func (b *blk) beginHit(name string, n uint64, x, y, w, h float32) base.Press {
	b.c.NextPos(x, y)
	b.c.NextFlags(base.Floating)
	pr := b.k.PressState(b.lbl(name, n), base.HoverOn)
	b.k.BeginPress(&pr, ui.RoleButton, ui.Px(w), ui.Px(h), 0, nil)
	return pr
}

// beginLink is beginHit for something that opens an address.
func (b *blk) beginLink(name string, n uint64, x, y, w, h float32) base.Press {
	b.c.NextPos(x, y)
	b.c.NextFlags(base.Floating)
	pr := b.k.PressState(b.lbl(name, n), base.HoverOn)
	b.k.BeginPress(&pr, ui.RoleLink, ui.Px(w), ui.Px(h), 0, nil)
	return pr
}

func (b *blk) endHit(pr *base.Press, radius float32) base.Signal {
	b.k.FocusRing(pr.ID, pr.Visual, radius)
	return b.k.EndPress(pr)
}

// pressed tints a card while the pointer is on it.
func (b *blk) pressedTint(pr *base.Press, w, h, radius float32, n uint64) {
	if pr.Hot > 0.01 {
		b.fillBox(n, 0, 0, w, h, radius, theme.WithAlpha(b.hoverFill(false), pr.Hot))
	}
}

// textLink is a callout-sized text link at (x, y) that reports a click.
func (b *blk) textLink(s string, x, y float32) bool {
	b.c.NextPos(x, y)
	b.c.NextFlags(base.Floating)
	return rexui.Link(b.k, s, rexui.LinkOpts{Style: rexui.TextCallout}).Clicked
}

// ---- documents ----

func (b *blk) documentSize(maxW float32) (w, h float32) { return b.cardW(maxW), b.pt(68) }

func (b *blk) documentBuild(r screen.Rect) {
	md, _ := b.m.Media()
	d := b.decoded()
	doc := &d.Content.Document
	k := b.k
	radius := b.pt(ztheme.Nested())
	pr := b.beginHit(i18n.T("Document"), 1, 0, 0, r.W, r.H)
	b.fillBox(1, 0, 0, r.W, r.H, radius, b.card())
	b.pressedTint(&pr, r.W, r.H, radius, 2)
	disc := b.pt(36)
	pad := b.pt(10)
	b.fillBox(3, pad, (r.H-disc)/2, disc, disc, disc/2, theme.WithAlpha(b.accent(), 0.25))
	b.icon(icDoc, b.accent(), pad, (r.H-disc)/2, disc, disc)
	// the action at the end: open, loading, download
	act := b.pt(18)
	ax := r.W - pad - act
	hasFile := len(md.Path) > 0
	switch {
	case hasFile:
		b.icon(icExternal, b.dim(), ax, (r.H-act)/2, act, act)
	case md.State == abi.MediaStateTagDownloading:
		k.NextWidth(ui.Px(act))
		b.c.NextPos(ax, (r.H-act)/2)
		b.c.NextFlags(base.Floating)
		rexui.Spinner(k, i18n.T("Loading")+"###spin", rexui.SpinnerOpts{Size: theme.SizeSmall})
	default:
		b.icon(icDownload, b.dim(), ax, (r.H-act)/2, act, act)
	}
	tx := pad + disc + pad
	tw := ax - b.pt(8) - tx
	name := md.FileName
	if len(name) == 0 && hasFile {
		name = []byte(filepath.Base(string(md.Path)))
	}
	if len(name) == 0 {
		name = []byte(i18n.T("Document"))
	}
	var detail []byte
	failed := md.State == abi.MediaStateTagFailed && !hasFile
	if failed {
		detail = expand(b.c.Fmt(), i18n.T("{error}. Click to retry."), phBytes("error", b.decoded().Content.Document.Media.State.Failed.Error)).Bytes()
	} else {
		f := b.c.Fmt()
		if doc.HasPages {
			f.Uint64(uint64(doc.Pages))
			if doc.Pages == 1 {
				f.Str(" page")
			} else {
				f.Str(" pages")
			}
			f.Str(" · ")
		}
		detail = byteSizeTo(f, md.Size).Bytes()
	}
	lh := b.lineH(fBold) + b.lineH(fCallout) + b.pt(1)
	y0 := (r.H - lh) / 2
	b.labelB(name, fBold, b.ink(), tx, y0, tw)
	if failed {
		b.labelB(detail, fCallout, b.dim(), tx, y0+b.lineH(fBold)+b.pt(1), tw)
	} else {
		b.metaB(detail, b.dim(), tx, y0+b.lineH(fBold)+b.pt(1), tw)
	}
	sig := b.endHit(&pr, radius)
	switch {
	case hasFile:
		if sig.Clicked {
			b.openFile(md.Path)
		}
	case md.State != abi.MediaStateTagDownloading:
		if sig.Clicked {
			b.download(true, -1)
		} else if md.State == abi.MediaStateTagIdle && b.autoDownload(md.Size, false) {
			b.download(false, -1)
		}
	}
}

// ---- link previews ----

const previewThumb = 64.0

func (b *blk) previewOf() (*abi.LinkPreview, bool) {
	d := b.decoded()
	return &d.Content.Text.Preview, d.Content.Text.HasPreview
}

// domainOf is the host of a URL as the preview shows it.
func domainOf(u []byte) []byte {
	if i := bytes.Index(u, []byte("://")); i >= 0 {
		u = u[i+3:]
	}
	if i := bytes.IndexByte(u, '/'); i >= 0 {
		u = u[:i]
	}
	return u
}

// previewLayout is the heights of the text column of a preview of width w.
func (b *blk) previewLayout(p *abi.LinkPreview, w float32, thumb bool) (tw, th, dh, domH, total float32) {
	pad := b.pt(8)
	col := w - 2*pad
	if thumb {
		col -= b.pt(previewThumb + 8)
	}
	_, th, _ = b.wrapSize(p.Title, fBold, col, 2)
	_, dh, _ = b.wrapSize(p.Description, fCallout, col, 2)
	domH = b.lineH(fCallout)
	total = th + dh + domH + b.pt(2)*2
	if th == 0 {
		total -= b.pt(2)
	}
	if dh == 0 {
		total -= b.pt(2)
	}
	return col, th, dh, domH, total
}

func (b *blk) previewSize(maxW float32) (w, h float32) {
	p, _ := b.previewOf()
	w = b.cardW(maxW)
	_, hasThumb := b.m.Thumbnail()
	_, _, _, _, total := b.previewLayout(p, w, hasThumb)
	if hasThumb {
		total = max(total, b.pt(previewThumb))
	}
	return w, total + b.pt(16)
}

func (b *blk) previewBuild(r screen.Rect) {
	p, _ := b.previewOf()
	ref, hasThumb := b.m.Thumbnail()
	radius := b.pt(ztheme.Nested())
	pad := b.pt(ztheme.Space2)
	name := i18n.T("Link preview")
	if len(p.Title) > 0 {
		name = string(p.Title)
	}
	pr := b.beginLink(name, 1, 0, 0, r.W, r.H)
	b.fillBox(1, 0, 0, r.W, r.H, radius, b.card())
	b.pressedTint(&pr, r.W, r.H, radius, 2)
	x := pad
	if hasThumb {
		s := b.pt(previewThumb)
		if hd, ok := b.thumbImage(ref, int32(s), int32(s), img.Cover); ok {
			b.imageBox(3, hd, pad, pad, s, s, b.pt(ztheme.InnerRadius), ui.FitFill)
		}
		x += s + pad
	}
	col, th, dh, domH, _ := b.previewLayout(p, r.W, hasThumb)
	y := pad
	if th > 0 {
		b.wrapped(4, p.Title, fBold, b.ink(), x, y, col, th, 2)
		y += th + b.pt(2)
	}
	if dh > 0 {
		b.wrapped(5, p.Description, fCallout, b.dim(), x, y, col, dh, 2)
		y += dh + b.pt(2)
	}
	b.labelB(domainOf(p.URL), fCallout, b.faint(), x, y, col)
	_ = domH
	if sig := b.endHit(&pr, radius); sig.Clicked {
		b.openURL(string(p.URL))
	}
}

// ---- sticker packs ----

func (b *blk) packSize(maxW float32) (w, h float32) {
	pad := b.pt(noteIconW + noteGap)
	return min(b.pt(260), maxW), max(b.lineH(fBold)+b.lineH(fCallout)+b.lineH(fCallout)+b.pt(6), pad)
}

func (b *blk) packBuild(r screen.Rect) {
	d := b.decoded()
	sp := &d.Content.StickerPack
	col := b.pt(noteIconW + noteGap)
	b.icon(icSticker, b.accent(), 0, 0, b.pt(noteIconW), b.lineH(fBold))
	b.labelB(sp.Name, fBold, b.ink(), col, 0, r.W-col)
	f := b.c.Fmt()
	if len(bytes.TrimSpace(sp.Publisher)) > 0 {
		f.Str(model.S(sp.Publisher)).Str(" · ")
	}
	detail := expand(f, i18n.N("{} sticker", "{} stickers", int(sp.Count)), phInt("", int(sp.Count))).Bytes()
	y := b.lineH(fBold) + b.pt(1)
	b.labelB(detail, fCallout, b.dim(), col, y, r.W-col)
	y += b.lineH(fCallout) + b.pt(4)
	if b.textLink(i18n.T("View stickers"), col, y) && b.a != nil {
		cmd := b.a.Cmd(abi.CommandTagViewStickerPack)
		cmd.ViewStickerPack.Chat, cmd.ViewStickerPack.Message = d.Chat, b.m.ID()
		b.a.Send(cmd)
		b.e.S.OpenDialog(b.msgDialog(state.DialogStickerPack))
	}
}

// msgDialog is a dialog about this message.
func (b *blk) msgDialog(kind state.DialogKind) state.Dialog {
	d := state.Dialog{Kind: kind, Chat: b.a.Open}
	if ref := b.chatOf(b.decoded().Chat); ref != 0 {
		d.Chat = ref
	}
	d.IDs.Add(b.m.ID())
	return d
}
