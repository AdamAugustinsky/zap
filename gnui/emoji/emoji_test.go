package emoji

import "testing"

func TestLookups(t *testing.T) {
	tb := Get()
	if len(tb.All) != 3944 {
		t.Fatalf("%d emoji", len(tb.All))
	}
	crab := tb.Get("🦀")
	if crab < 0 || tb.All[crab].Name != "crab" || tb.All[crab].Codes != "crab" || tb.All[crab].Skin {
		t.Fatalf("crab: %d %+v", crab, tb.All[max(crab, 0)])
	}
	// with or without the presentation selector
	if h := tb.Get("❤️"); h < 0 || h != tb.Get("❤") {
		t.Errorf("heart: %d vs %d", h, tb.Get("❤"))
	}
	if tb.Get("not an emoji") != -1 || tb.GetBytes([]byte("x")) != -1 {
		t.Error("found what is not there")
	}
	wave := tb.Get("👋🏽")
	if wave < 0 || !tb.All[wave].Skin || tb.All[tb.Get("👋")].Skin {
		t.Error("skin-tone variants are not told from the default")
	}
	if !tb.Matches(int(crab), "cra") || tb.Matches(int(crab), "zzz") {
		t.Error("Matches")
	}
}

// Lookups allocate nothing.
func TestLookupsAllocateNothing(t *testing.T) {
	tb := Get()
	b := []byte("🦀")
	if n := testing.AllocsPerRun(100, func() {
		tb.GetBytes(b)
		tb.Get("🇧🇷")
		tb.Matches(5, "face")
	}); n != 0 {
		t.Errorf("%v allocations", n)
	}
}
