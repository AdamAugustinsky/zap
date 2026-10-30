package theme

import (
	"math"

	gtheme "gonativeui/theme"

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
// many there are: c's hue turned in OKLCH (oklab.go) at c's lightness and
// chroma, so that the colours look equally light, as a yellow and a blue of
// one HSL lightness do not.
func Harmonize(c uint32, mix string, dst *[3]uint32) int {
	turns, n := MixTurns(mix)
	dst[0] = c
	l, ch, h := toOKLCH(c)
	for i := 1; i < n; i++ {
		dst[i] = fromOKLCH(l, ch, h+turns[i-1])
	}
	return n
}

// fieldChroma is the chroma at the theme creator's field's edge, and
// fieldL the lightness at its centre and the edge's: pale at the centre,
// full at the edge, one lightness for one distance whatever the hue.
const (
	fieldChroma = 0.19
	fieldLIn    = 0.95
	fieldLOut   = 0.68
)

// FieldColor is the colour at a place of the theme creator's field: the
// angle round its centre (degrees) is the OKLCH hue and the distance out
// (0..1) the chroma, pale at the centre and full at the edge, as Zen's
// picker; dots at one distance look equally light.
func FieldColor(angle, out float64) uint32 {
	out = min(max(out, 0), 1)
	return fromOKLCH(fieldLIn+(fieldLOut-fieldLIn)*out, fieldChroma*out, angle)
}

// FieldPlace is where colour c lies on the field: its hue round it, and out
// as far as its lightness (FieldColor's lightness for a distance, which
// fromOKLCH keeps where sRGB cuts the chroma), so that colours of one
// lightness, a harmony's, lie at one distance. A grey, with no hue to place
// it by, lies by its chroma, near the centre.
func FieldPlace(c uint32) (angle, out float64) {
	l, ch, h := toOKLCH(c)
	if ch < 0.02 {
		return h, min(ch/fieldChroma, 1)
	}
	return h, min(max((fieldLIn-l)/(fieldLIn-fieldLOut), 0), 1)
}

// SpaceAlpha is alpha, the opacity a space's colours cols (n of them) are
// laid over the field at, lessened as far as the field's labels need: its
// primary text keeps 7:1 and its secondary 3:1 (GuardTint), so a deep or
// strong colour shows as much of itself as reading allows.
func SpaceAlpha(cols [3]uint32, n int, alpha float32, dark bool) float32 {
	f := uint32(lightField)
	if dark {
		f = darkField
	}
	label, label2 := labels(dark)
	for _, c := range cols[:n] {
		alpha = GuardTint(c, alpha, f, label, 7)
		alpha = GuardTint(c, alpha, f, label2, secondaryFloor)
	}
	return alpha
}

// secondaryFloor is the contrast secondary text keeps over a colour: 3:1,
// what macOS's own secondaryLabelColor has on a light window (white gives it
// under 4:1), so a colour is not refused for lowering text that never
// reached 4.5:1. Primary text keeps 7:1.
const secondaryFloor = 3

// labels are the primary and secondary text colours (labelColor and
// secondaryLabelColor) in light or dark.
func labels(dark bool) (label, label2 uint32) {
	if dark {
		return 0xFFFFFFD8, 0xFFFFFF8C
	}
	return 0x000000D8, 0x0000007F
}

// BoostFace is the family of a chat boost's face (state.BoostFonts), or the
// system's monospace: faces every Mac has, so no fallback is needed, and
// few of them, quiet ones: a serif for long reading, a humanist sans, code.
func BoostFace(name string) (family string, mono bool) {
	switch name {
	case "serif":
		return "Charter", false
	case "humanist":
		return "Avenir Next", false
	case "mono":
		return "", true
	}
	return "", false
}

// BoostBubble is your bubbles' fill in a chat whose boost gives them colour
// name (a state.SpaceTints name), over the pane pane: the colour laid on
// as lightly as Zap's green is, and lighter still where the text on it
// would stop reading (GuardTint); 0 for no colour.
func BoostBubble(name string, pane uint32, dark bool) uint32 {
	col := TintColor(name)
	if col == 0 {
		return 0
	}
	a := float32(ownTint)
	if dark {
		a = ownTintDark
	}
	label, label2 := labels(dark)
	a = GuardTint(col, a, pane, label, 7)
	a = GuardTint(col, a, pane, label2, secondaryFloor)
	return gtheme.WithAlpha(col, a)
}

// accentSurface is what the accent reads on: the panes, white in light and
// Zap's dark pane in dark.
var accentSurface = [2]uint32{0xFFFFFFFF, darkPane}

// TintAccent is the accent a space of colour c takes, as Zen derives its
// interface accent from a theme (more chroma, the lightness of a colour that
// holds text) but held, as Zen's is not, to 4.5:1 on the panes: its hue
// kept, its lightness taken from 0.58 (light) or 0.70 (dark) toward darker or
// lighter until text in it reads, whatever the hue.
func TintAccent(c uint32, dark bool) uint32 {
	_, ch, h := toOKLCH(c)
	if ch > 0.02 {
		ch = min(ch*1.15+0.02, 0.2)
	}
	if dark {
		return fromOKLCH(readableL(0.70, ch, h, accentSurface[1], 4.5, true), ch, h)
	}
	return fromOKLCH(readableL(0.58, ch, h, accentSurface[0], 4.5, false), ch, h)
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
