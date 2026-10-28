package kinds

import (
	"strconv"
	"time"

	"gonativeui/img"
	"gonativeui/ui"
	"gonativeui/widget/base"

	"zapfast/abi"
	"zapfast/i18n"
	"zapfast/ui/screen"
)

// Locations and live locations (zf:src/ui/conversation.rs location_card,
// zf:src/ui/locations.rs, zf:src/geo.rs map_url).

// liveLimit is how long a live location lasts (model::LIVE_LOCATION_LIMIT).
const liveLimit = 8 * 60 * 60

// loc is what a location card says.
type loc struct {
	title, detail, note []byte
	lat, lon            float64
	preview             bool // the sender sent a map preview
	over, live          bool
}

func (b *blk) locOf() (l loc) {
	d := b.decoded()
	_, l.preview = b.m.Thumbnail()
	if d.Content.Tag == abi.ContentTagLiveLocation {
		ll := &d.Content.LiveLocation
		l.live, l.lat, l.lon = true, ll.Latitude, ll.Longitude
		now := b.e.S.Now().Unix()
		l.over = ll.Ended || now-b.m.Time() > liveLimit
		if l.over {
			l.title = base.StrBytes(i18n.T("Live location ended"))
			return l
		}
		l.title = base.StrBytes(i18n.T("Live location"))
		at := ll.Updated
		if at <= 0 {
			at = b.m.Time()
		}
		var tb [16]byte
		f := expand(b.c.Fmt(), i18n.T("Updated {time}"), phBytes("time", clock(tb[:0], at)))
		if ll.HasSpeedMPS && ll.SpeedMPS >= 0.5 {
			f.Str(" · ").Int(int(ll.SpeedMPS*3.6 + 0.5)).Str(" km/h")
		}
		if ll.HasAccuracyM {
			f.Str(" · ±").Uint64(uint64(ll.AccuracyM)).Str(" m")
		}
		l.detail = f.Bytes()
		if ll.NewerOnPhone {
			l.note = base.StrBytes(i18n.T("Open WhatsApp on your phone to follow it."))
		}
		return l
	}
	lc := &d.Content.Location
	l.lat, l.lon = lc.Latitude, lc.Longitude
	if lc.HasName && len(lc.Name) > 0 {
		l.title = lc.Name
	} else {
		l.title = base.StrBytes(i18n.T("Location"))
	}
	if lc.HasAddress && len(lc.Address) > 0 {
		l.detail = lc.Address
	} else {
		// a spot sent from here names no place: the coordinates take the line
		l.detail = b.c.Fmt().Float(lc.Latitude, 5).Str(", ").Float(lc.Longitude, 5).Bytes()
	}
	return l
}

// mapURL is the link to open for a spot; the browser fetches it, never
// ZapFast (geo::map_url).
func mapURL(lat, lon float64) string {
	la := strconv.FormatFloat(lat, 'f', -1, 64)
	lo := strconv.FormatFloat(lon, 'f', -1, 64)
	return "https://www.openstreetmap.org/?mlat=" + la + "&mlon=" + lo + "#map=16/" + la + "/" + lo
}

type locGeom struct {
	w, pic, titleH, detailH, noteH, linkH, gap, h float32
}

func (b *blk) locGeom(l *loc, maxW float32) (g locGeom) {
	g.w = min(b.pt(locationWidth), maxW)
	g.gap = b.pt(2)
	if l.preview {
		g.pic = float32(int(g.w * 0.56))
	}
	g.titleH = b.lineH(fBold)
	_, g.detailH, _ = b.wrapSize(l.detail, fCallout, g.w, 2)
	_, g.noteH, _ = b.wrapSize(l.note, fCallout, g.w, 2)
	g.linkH = b.lineH(fCallout)
	g.h = g.titleH + g.linkH + g.gap
	if g.pic > 0 {
		g.h += g.pic + b.pt(4)
	}
	if g.detailH > 0 {
		g.h += g.detailH + g.gap
	}
	if g.noteH > 0 {
		g.h += g.noteH + g.gap
	}
	return g
}

func (b *blk) locationSize(maxW float32) (w, h float32) {
	l := b.locOf()
	g := b.locGeom(&l, maxW)
	return g.w, g.h
}

func (b *blk) locationBuild(r screen.Rect) {
	l := b.locOf()
	g := b.locGeom(&l, r.W)
	y := float32(0)
	if g.pic > 0 {
		ref, _ := b.m.Thumbnail()
		if hd, ok := b.thumbImage(ref, int32(g.w), int32(g.pic), img.Cover); ok {
			b.imageBox(1, hd, 0, 0, g.w, g.pic, b.pt(8), ui.FitFill)
		} else {
			b.fillBox(1, 0, 0, g.w, g.pic, b.pt(8), b.card())
		}
		y += g.pic + b.pt(4)
	}
	pin := b.pt(18)
	b.icon(icPin, b.accent(), 0, y, pin, g.titleH)
	b.labelB(l.title, fBold, b.ink(), pin+b.pt(4), y, g.w-pin-b.pt(4))
	y += g.titleH + g.gap
	if g.detailH > 0 {
		b.wrapped(2, l.detail, fCallout, b.dim(), 0, y, g.w, g.detailH, 2)
		y += g.detailH + g.gap
	}
	if g.noteH > 0 {
		b.wrapped(3, l.note, fCallout, b.dim(), 0, y, g.w, g.noteH, 2)
		y += g.noteH + g.gap
	}
	if b.textLink(i18n.T("Open in a map"), 0, y) {
		b.openURL(mapURL(l.lat, l.lon))
	}
	if l.live && !l.over {
		// the share ends by itself: draw again when it does
		left := int64(liveLimit) - (b.e.S.Now().Unix() - b.m.Time()) + 1
		if left > 0 {
			b.c.WakeIn(int64(time.Duration(left) * time.Second))
		}
	}
}
