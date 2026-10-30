package theme

import "testing"

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
	TintColors("blue", "complementary", &d)
	if h0, _, _ := toHSL(d[0]); func() bool { h1, _, _ := toHSL(d[1]); dh := h1 - h0; return dh < 178 && dh > -178 }() {
		t.Errorf("blue's complement %08X is not opposite", d[1])
	}
	// the accent holds text: lighter in dark, deeper in light
	if TintAccent(TintColor("yellow"), false) == TintAccent(TintColor("yellow"), true) {
		t.Error("the accent is the same in light and dark")
	}
	if _, _, l := toHSL(TintAccent(TintColor("yellow"), false)); l > 0.5 {
		t.Errorf("yellow's light accent is too light (%.2f)", l)
	}
}
