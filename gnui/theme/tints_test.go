package theme

import (
	"math"
	"testing"

	gtheme "gonativeui/theme"
)

// The harmonies turn the space's colour round the wheel and keep it: blue
// and its opposite, a third each way, its two neighbours; HSL goes there and
// back without drift.
func TestTintColors(t *testing.T) {
	var d [3]uint32
	for _, name := range []string{"blue", "red", "teal", "gray"} {
		c := TintColor(name)
		h, s, l := toHSL(c)
		if got := fromHSL(h, s, l); got != c {
			t.Errorf("%s: HSL round trip %08X, want %08X", name, got, c)
		}
	}
	if n := TintColors("", "triadic", &d); n != 0 {
		t.Fatalf("no colour gave %d", n)
	}
	for mix, want := range map[string]int{"": 1, "complementary": 2, "analogous": 3, "triadic": 3} {
		if n := TintColors("blue", mix, &d); n != want || d[0] != TintColor("blue") {
			t.Errorf("%q: %d colours, first %08X", mix, n, d[0])
		}
	}
	// the harmonies turn the hue in OKLCH and keep its lightness
	TintColors("blue", "complementary", &d)
	l0, _, h0 := toOKLCH(d[0])
	l1, _, h1 := toOKLCH(d[1])
	if dh := math.Abs(math.Mod(h1-h0+360, 360) - 180); dh > 3 {
		t.Errorf("blue's complement %08X is %.0f degrees from opposite", d[1], dh)
	}
	if math.Abs(l1-l0) > 0.02 {
		t.Errorf("blue's complement %08X is not as light (%.2f, %.2f)", d[1], l0, l1)
	}
	// the field: one distance looks equally light whatever the hue, and a
	// colour goes to its place and back
	for _, ang := range []float64{20, 110, 200, 290} {
		c := FieldColor(ang, 0.7)
		if l, _, _ := toOKLCH(c); math.Abs(l-(fieldLIn+(fieldLOut-fieldLIn)*0.7)) > 0.01 {
			t.Errorf("field at %.0f: lightness %.3f", ang, l)
		}
		if a, o := FieldPlace(c); math.Abs(math.Mod(a-ang+540, 360)-180) > 3 || math.Abs(o-0.7) > 0.02 {
			t.Errorf("field at %.0f, 0.7 placed back at %.0f, %.2f", ang, a, o)
		}
	}
	// a harmony's colours lie at one distance, whatever sRGB cuts
	var tri [3]uint32
	TintColors("purple", "triadic", &tri)
	_, o0 := FieldPlace(tri[0])
	for _, c := range tri[1:] {
		if _, o := FieldPlace(c); math.Abs(o-o0) > 0.02 {
			t.Errorf("purple's triad lies at %.2f and %.2f", o0, o)
		}
	}
	// the accent holds text on the panes, in light and dark, whatever the hue
	for _, name := range []string{"yellow", "green", "blue", "pink", "gray"} {
		c := TintColor(name)
		if a := TintAccent(c, false); gtheme.Contrast(a, accentSurface[0]) < 4.5 {
			t.Errorf("%s's light accent %08X reads at %.2f", name, a, gtheme.Contrast(a, accentSurface[0]))
		}
		if a := TintAccent(c, true); gtheme.Contrast(a, accentSurface[1]) < 4.5 {
			t.Errorf("%s's dark accent %08X reads at %.2f", name, a, gtheme.Contrast(a, accentSurface[1]))
		}
	}
	if TintAccent(TintColor("yellow"), false) == TintAccent(TintColor("yellow"), true) {
		t.Error("the accent is the same in light and dark")
	}
	// the guard lessens a colour that would sink the labels, not a pale one
	if a := GuardTint(0x1030C0FF, 0.6, 0xFFFFFFFF, 0x0000007F, 4.5); a >= 0.6 {
		t.Errorf("a deep blue at 0.6 behind secondary labels kept %.2f", a)
	}
	if a := GuardTint(0xF4F0FFFF, 0.6, 0xFFFFFFFF, 0x000000D8, 7); a != 0.6 {
		t.Errorf("a pale lilac was lessened to %.2f", a)
	}
	if deep := [3]uint32{0x102080FF}; SpaceAlpha(deep, 1, 0.5, false) >= 0.5 || SpaceAlpha(deep, 1, 0.5, true) != 0.5 {
		t.Error("a deep navy should be lessened on the light field and kept on the dark")
	}
	// a boost's colour shows on your bubbles in light and dark, as lightly
	// as Zap's green, and the text on them reads
	for _, dark := range []bool{false, true} {
		pane, a := uint32(0xFFFFFFFF), float32(ownTint)
		if dark {
			pane, a = darkPane, ownTintDark
		}
		f := BoostBubble("purple", pane, dark)
		if got := float32(f&0xFF) / 255; got < a*0.8 {
			t.Errorf("dark %v: purple bubbles at %.2f, Zap's green at %.2f", dark, got, a)
		}
		label, _ := labels(dark)
		if bg := gtheme.Over(f, pane); gtheme.ContrastOn(label, bg, bg) < 7 {
			t.Errorf("dark %v: text on purple bubbles reads at %.2f", dark, gtheme.ContrastOn(label, bg, bg))
		}
	}
}
