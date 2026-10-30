package theme

import (
	"math"

	gtheme "gonativeui/theme"
)

// OKLCH (Björn Ottosson's Oklab, in polar form): a colour space where equal
// steps look equal, so colours of one lightness L look equally light
// whatever their hue, which HSL's do not (HSL's yellow at 50 % is far
// brighter than its blue). The space themes work in it: the theme creator's
// field, the harmonies' turns and the accent (tints.go), as Zen's Boosts
// tint in Oklab.

// toOKLCH is c's lightness (0..1), chroma (0..~0.37) and hue (degrees).
func toOKLCH(c uint32) (l, ch, h float64) {
	lin := func(v uint32) float64 {
		x := float64(v) / 255
		if x <= 0.04045 {
			return x / 12.92
		}
		return math.Pow((x+0.055)/1.055, 2.4)
	}
	r, g, b := lin(c>>24), lin(c>>16&0xFF), lin(c>>8&0xFF)
	lm := math.Cbrt(0.4122214708*r + 0.5363325363*g + 0.0514459929*b)
	mm := math.Cbrt(0.2119034982*r + 0.6806995451*g + 0.1073969566*b)
	sm := math.Cbrt(0.0883024619*r + 0.2817188376*g + 0.6299787005*b)
	l = 0.2104542553*lm + 0.7936177850*mm - 0.0040720468*sm
	a := 1.9779984951*lm - 2.4285922050*mm + 0.4505937099*sm
	bb := 0.0259040371*lm + 0.7827717662*mm - 0.8086757660*sm
	ch = math.Hypot(a, bb)
	h = math.Atan2(bb, a) * 180 / math.Pi
	if h < 0 {
		h += 360
	}
	return l, ch, h
}

// fromOKLCH is the opaque sRGB colour of lightness l, chroma ch and hue h
// (degrees, any turn). One sRGB cannot show is brought in by lessening its
// chroma, never its hue or lightness, so a harmony keeps its turns and its
// even lightness at the gamut's edge.
func fromOKLCH(l, ch, h float64) uint32 {
	l = min(max(l, 0), 1)
	ch = max(ch, 0)
	if r, g, b, ok := oklchLinear(l, ch, h); ok {
		return packLinear(r, g, b)
	}
	lo, hi := 0.0, ch
	for range 18 {
		mid := (lo + hi) / 2
		if _, _, _, ok := oklchLinear(l, mid, h); ok {
			lo = mid
		} else {
			hi = mid
		}
	}
	r, g, b, _ := oklchLinear(l, lo, h)
	return packLinear(r, g, b)
}

// oklchLinear is (l, ch, h) in linear sRGB, and whether it is in gamut.
func oklchLinear(l, ch, h float64) (r, g, b float64, ok bool) {
	rad := h * math.Pi / 180
	a, bb := ch*math.Cos(rad), ch*math.Sin(rad)
	lm := l + 0.3963377774*a + 0.2158037573*bb
	mm := l - 0.1055613458*a - 0.0638541728*bb
	sm := l - 0.0894841775*a - 1.2914855480*bb
	lm, mm, sm = lm*lm*lm, mm*mm*mm, sm*sm*sm
	r = 4.0767416621*lm - 3.3077115913*mm + 0.2309699292*sm
	g = -1.2684380046*lm + 2.6097574011*mm - 0.3413193965*sm
	b = -0.0041960863*lm - 0.7034186147*mm + 1.7076147010*sm
	const eps = 1e-4
	ok = r >= -eps && r <= 1+eps && g >= -eps && g <= 1+eps && b >= -eps && b <= 1+eps
	return r, g, b, ok
}

func packLinear(r, g, b float64) uint32 {
	enc := func(x float64) uint32 {
		x = min(max(x, 0), 1)
		if x <= 0.0031308 {
			x *= 12.92
		} else {
			x = 1.055*math.Pow(x, 1/2.4) - 0.055
		}
		return uint32(math.Round(min(max(x, 0), 1) * 255))
	}
	return enc(r)<<24 | enc(g)<<16 | enc(b)<<8 | 0xFF
}

// readableL is the lightness nearest from that colour (ch, h) needs to read
// at want:1 on bg (WCAG), going lighter or darker;
// from if it already does.
func readableL(from, ch, h float64, bg uint32, want float64, lighter bool) float64 {
	l := from
	for range 100 {
		if gtheme.Contrast(fromOKLCH(l, ch, h), bg|0xFF) >= want {
			return l
		}
		if lighter {
			l += 0.01
			if l >= 1 {
				return 1
			}
		} else {
			l -= 0.01
			if l <= 0 {
				return 0
			}
		}
	}
	return l
}

// GuardTint is alpha, lessened until field with col laid over it at that
// opacity keeps ink (drawn on the field, translucent as it is seen) at
// want:1, or at the contrast it had without the colour when that was already
// less: a space's colour never makes the window's own text hard to read.
func GuardTint(col uint32, alpha float32, field, ink uint32, want float64) float32 {
	base := field | 0xFF
	want = min(gtheme.ContrastOn(ink, base, base), want) - 0.01
	for a := alpha; a > 0.01; a *= 0.85 {
		bg := gtheme.MixRGBA(base, col|0xFF, a)
		if gtheme.ContrastOn(ink, bg, bg) >= want {
			return a
		}
	}
	return 0
}
