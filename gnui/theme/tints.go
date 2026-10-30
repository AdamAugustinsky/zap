package theme

import (
	"math"

	"zapfast/state"
)

// The spaces' colours (state.SpaceTints), as Arc's: saturated enough to
// tell the spaces apart at a glance through the field, never a fill of
// their own. TintAlpha is how much of it the field takes in a dark
// appearance, TintAlphaLight in a light one.
const (
	TintAlpha      = 0.16
	TintAlphaLight = 0.11
)

// TintColor is space tint name's colour (0xRRGGBBAA); 0 for none.
func TintColor(name string) uint32 {
	switch name {
	case "blue":
		return 0x3B82F6FF
	case "purple":
		return 0x8B5CF6FF
	case "pink":
		return 0xEC4899FF
	case "red":
		return 0xEF4444FF
	case "orange":
		return 0xF97316FF
	case "yellow":
		return 0xEAB308FF
	case "green":
		return 0x22C55EFF
	case "teal":
		return 0x14B8A6FF
	case "gray":
		return 0x94A3B8FF
	}
	return 0
}

// TintColors are the colours of a space theme of colour name and mix (one
// of state.TintMixes), into dst, the space's own first: as Zen's harmonies,
// the others its hue turned round the wheel (MixTurns), keeping its
// saturation and lightness. It returns how many there are, 0 for none.
func TintColors(name, mix string, dst *[3]uint32) int {
	c := TintColor(name)
	if c == 0 {
		return 0
	}
	return Harmonize(c, mix, dst)
}

// SpaceColors are space theme t's colours into dst, the space's own first:
// the person's own (Dots) or its tint's (TintColors). It returns how many.
func SpaceColors(t state.SpaceTheme, dst *[3]uint32) int {
	if !t.Custom() {
		return TintColors(t.Tint, t.Mix, dst)
	}
	n := 0
	for _, c := range t.Dots {
		if c != 0 {
			dst[n] = c | 0xFF
			n++
		}
	}
	return n
}

// SpacePrimary is space theme t's own colour, the first of SpaceColors
// (without working out the others); 0 for none.
func SpacePrimary(t state.SpaceTheme) uint32 {
	if t.Custom() {
		return t.Dots[0] | 0xFF
	}
	return TintColor(t.Tint)
}

// MixTurns are harmony mix's turns of the hue from the first colour, in
// degrees, and how many colours it has (1 for none, "").
func MixTurns(mix string) ([2]float64, int) {
	switch mix {
	case "analogous":
		return [2]float64{50, -50}, 3
	case "complementary":
		return [2]float64{180}, 2
	case "split":
		return [2]float64{150, 210}, 3
	case "triadic":
		return [2]float64{120, 240}, 3
	}
	return [2]float64{}, 1
}

// Harmonize places mix's colours from c into dst (c first) and returns how
// many there are.
func Harmonize(c uint32, mix string, dst *[3]uint32) int {
	turns, n := MixTurns(mix)
	dst[0] = c
	h, s, l := toHSL(c)
	for i := 1; i < n; i++ {
		dst[i] = fromHSL(h+turns[i-1], s, l)
	}
	return n
}

// FieldColor is the colour at a place of the theme creator's field: the
// angle round its centre (degrees) is the hue and the distance out (0..1)
// the saturation, pale at the centre and full at the edge, as Zen's picker.
func FieldColor(angle, out float64) uint32 {
	out = min(max(out, 0), 1)
	return fromHSL(angle, out, 0.86-0.36*out)
}

// FieldPlace is where colour c lies on the field: FieldColor's angle and
// distance.
func FieldPlace(c uint32) (angle, out float64) {
	h, s, _ := toHSL(c)
	return h, s
}

// TintAccent is the accent a space of colour c takes, as Zen derives its
// interface accent from a theme: more saturated, and its lightness pulled
// most of the way to one that holds text on it (0.42 in light, 0.62 in dark).
func TintAccent(c uint32, dark bool) uint32 {
	h, s, l := toHSL(c)
	target := 0.42
	if dark {
		target = 0.62
	}
	return fromHSL(h, min(s+0.3, 1), l*0.4+target*0.6)
}

// toHSL is c's hue (degrees), saturation and lightness (0..1).
func toHSL(c uint32) (h, s, l float64) {
	r, g, b := float64(c>>24)/255, float64(c>>16&0xFF)/255, float64(c>>8&0xFF)/255
	hi, lo := max(r, g, b), min(r, g, b)
	l = (hi + lo) / 2
	d := hi - lo
	if d == 0 {
		return 0, 0, l
	}
	if l > 0.5 {
		s = d / (2 - hi - lo)
	} else {
		s = d / (hi + lo)
	}
	switch hi {
	case r:
		h = (g - b) / d
		if g < b {
			h += 6
		}
	case g:
		h = (b-r)/d + 2
	default:
		h = (r-g)/d + 4
	}
	return h * 60, s, l
}

// fromHSL is the opaque colour of hue h (degrees, any turn), saturation s and
// lightness l.
func fromHSL(h, s, l float64) uint32 {
	h = math.Mod(h, 360)
	if h < 0 {
		h += 360
	}
	q := l + s - l*s
	if l < 0.5 {
		q = l * (1 + s)
	}
	p := 2*l - q
	ch := func(t float64) uint32 {
		t = math.Mod(t+1, 1)
		var v float64
		switch {
		case t < 1.0/6:
			v = p + (q-p)*6*t
		case t < 0.5:
			v = q
		case t < 2.0/3:
			v = p + (q-p)*(2.0/3-t)*6
		default:
			v = p
		}
		return uint32(math.Round(min(max(v, 0), 1) * 255))
	}
	k := h / 360
	return ch(k+1.0/3)<<24 | ch(k)<<16 | ch(k-1.0/3)<<8 | 0xFF
}
