package theme

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"gonativeui/app"
	gtheme "gonativeui/theme"
	"gonativeui/ui"
	"gonativeui/widget"
	"gonativeui/widget/widgettest"

	"zapfast/state"
	"zapfast/ui/screen"
)

// host is a window with nothing in it but the kit the theme is applied to,
// as the shell's frame does: Apply, then Begin and End.
type host struct {
	th *Theme
	s  *state.State
	k  *widget.Kit
}

func (h *host) Init(a *app.App) error {
	h.k = screen.NewKit(a, nil)
	a.UI.Reserve(256)
	return nil
}

func (h *host) Build(c *ui.Context) {
	if w, hh := c.Size(); w <= 0 || hh <= 0 {
		return
	}
	h.th.Apply(h.k, h.s)
	h.k.Begin(c)
	h.k.End()
}

type rig struct {
	*widgettest.Rig
	*host
}

// open opens the window at scale, the system in dark mode or not before the
// first frame, with the theme th reading the settings s.
func open(t *testing.T, th *Theme, s *state.State, dark bool, scale float32) *rig {
	t.Helper()
	h := &host{th: th, s: s}
	cfg := app.Config{Title: "theme", Width: 320, Height: 200, Clear: [4]float32{1, 1, 1, 1}}
	r := &rig{host: h}
	r.Rig = widgettest.Open(t, cfg, int(320*scale), int(200*scale), scale, h)
	r.Offscreen().UI.SetSystemAppearance(ui.Appearance{Dark: dark, Known: true})
	return r
}

// settle runs frames until the fade has finished and the theme folder is in.
func (r *rig) settle(t *testing.T) {
	t.Helper()
	for i := 0; i < 400; i++ {
		r.Frame()
		if !r.th.Loading() {
			if tt := r.k.Theme().T; tt == 0 || tt == 1 {
				r.Frames(2)
				return
			}
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("the window did not settle (T %v, loading %v)", r.k.Theme().T, r.th.Loading())
}

func newState(c state.Theme) *state.State {
	s := state.New()
	s.Settings.Theme = c
	return s
}

// A theme folder of its own, with the shared palettes installed, per test.
func newTheme(t *testing.T, files map[string]string) (*Theme, string) {
	t.Helper()
	dir := filepath.Join(t.TempDir(), "themes")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	for name, text := range files {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(text), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	th := NewIn(dir, true)
	t.Cleanup(func() { cur = initialTheme() })
	return th, dir
}

// initialTheme is the package's initial theme, for tests that must not leak theirs.
func initialTheme() *Theme {
	t := &Theme{look: builtin(state.ThemeDark), gen: 1}
	t.mixed = t.look.dark
	return t
}

// The theme choice gives the mode and the palettes the conversation reads.
func TestChoiceSetsTheModeAndThePalette(t *testing.T) {
	for _, tc := range []struct {
		name       string
		choice     state.Theme
		systemDark bool
		dark       bool
		bubbleOut  uint32
	}{
		{"dark", state.ThemeDark, false, true, DarkPalette().BubbleOut},
		{"light", state.ThemeLight, true, false, LightPalette().BubbleOut},
		{"system in light", state.ThemeSystem, false, false, LightPalette().BubbleOut},
		{"system in dark", state.ThemeSystem, true, true, DarkPalette().BubbleOut},
	} {
		t.Run(tc.name, func(t *testing.T) {
			th := NewIn("", false)
			r := open(t, th, newState(tc.choice), tc.systemDark, 1)
			r.settle(t)
			if got := r.k.Mode() == gtheme.Dark; got != tc.dark {
				t.Errorf("dark = %v, want %v", got, tc.dark)
			}
			if Dark() != tc.dark {
				t.Errorf("Dark() = %v, want %v", Dark(), tc.dark)
			}
			if got := Bubble(true); got != tc.bubbleOut {
				t.Errorf("Bubble(true) = %08X, want %08X", got, tc.bubbleOut)
			}
		})
	}
}

// The first frame is already in the right mode: no light flash in dark.
func TestTheFirstFrameIsInTheSystemsMode(t *testing.T) {
	th := NewIn("", false)
	r := open(t, th, newState(state.ThemeSystem), true, 1)
	r.Frame()
	if r.k.Theme().T != 1 || !r.k.Theme().Dark() {
		t.Errorf("first frame mix = %v, want dark", r.k.Theme().T)
	}
	if !Dark() || Bubble(true) != DarkPalette().BubbleOut {
		t.Errorf("the conversation's palette is not dark in the first frame")
	}
}

// Follow system changes with the system, live; Light and Dark do not.
func TestFollowsTheSystemLive(t *testing.T) {
	th := NewIn("", false)
	s := newState(state.ThemeSystem)
	r := open(t, th, s, false, 1)
	r.settle(t)
	if Dark() {
		t.Fatal("dark under a light system")
	}
	r.Offscreen().UI.SetSystemAppearance(ui.Appearance{Dark: true, Known: true})
	r.settle(t)
	if !Dark() || r.k.Theme().T != 1 || Chat() != DarkPalette().Chat {
		t.Errorf("did not follow the system to dark: Dark %v, T %v", Dark(), r.k.Theme().T)
	}
	r.Offscreen().UI.SetSystemAppearance(ui.Appearance{Dark: false, Known: true})
	r.settle(t)
	if Dark() || Chat() != LightPalette().Chat {
		t.Error("did not follow the system back to light")
	}

	s.Settings.Theme = state.ThemeDark
	r.Offscreen().UI.SetSystemAppearance(ui.Appearance{Dark: false, Known: true})
	r.settle(t)
	if !Dark() {
		t.Error("Dark did not override a light system")
	}
	s.Settings.Theme = state.ThemeLight
	r.Offscreen().UI.SetSystemAppearance(ui.Appearance{Dark: true, Known: true})
	r.settle(t)
	if Dark() {
		t.Error("Light did not override a dark system")
	}
}

// A theme file recolours Rex's roles from its palette, in its base's mode.
func TestAThemeFileMapsOntoRexRoles(t *testing.T) {
	th, _ := newTheme(t, map[string]string{
		"mine.json": `{"base":"light","colors":{"window":"#112233","accent":"#445566","text":"#102030","danger":"#AA0000"}}`,
	})
	s := newState(state.ThemeDark)
	s.Settings.CustomTheme = []byte("mine.json")
	r := open(t, th, s, true, 1) // a dark system: the file's base is light
	r.settle(t)
	c := &r.k.Theme().Colors
	for _, w := range []struct {
		name string
		role gtheme.Role
		want uint32
	}{
		{"window", gtheme.Field, 0x112233FF},
		{"window", gtheme.Window, 0x112233FF},
		{"accent", gtheme.Accent, 0x445566FF},
		{"text", gtheme.Label, 0x102030FF},
		{"danger", gtheme.Red, 0xAA0000FF},
		{"panel", gtheme.Pane, opaque(LightPalette().Panel)},
		{"panel", gtheme.SidebarFill, opaque(LightPalette().Panel)},
		{"surface", gtheme.TextBackground, opaque(LightPalette().Surface)},
		{"surface_active", gtheme.SidebarSelUnemph, opaque(LightPalette().SurfaceActive)},
		{"outline", gtheme.Separator, opaque(LightPalette().Outline)},
		{"overlay", gtheme.Raised, opaque(LightPalette().Overlay)},
		{"secondary", gtheme.Label2, opaque(LightPalette().Secondary)},
		{"dim", gtheme.Placeholder, opaque(LightPalette().Dim)},
		{"on_accent", gtheme.OnAccent, opaque(LightPalette().OnAccent)},
		{"link (derived from accent)", gtheme.Link, 0x445566FF},
		{"shadow", gtheme.Shadow, LightPalette().Shadow},
	} {
		if got := c[w.role]; got != w.want {
			t.Errorf("%s: role %d = %08X, want %08X", w.name, w.role, got, w.want)
		}
	}
	if r.k.Mode() != gtheme.Light || Dark() {
		t.Error("the file's light base was not shown over a dark system")
	}
	// the chat colours are derived as upstream does (TH-005)
	p := Colors()
	if p.Chat != 0x112233FF || p.Link != 0x445566FF || p.Read != 0x445566FF {
		t.Errorf("derived chat %08X link %08X read %08X", p.Chat, p.Link, p.Read)
	}
	if want := lerpGamma(p.Surface, p.Accent, 0.24); p.BubbleOut != want {
		t.Errorf("bubble_out %08X, want %08X", p.BubbleOut, want)
	}
	if p.BubbleIn != LightPalette().BubbleIn { // not derived: the file gave no surface
		t.Errorf("bubble_in %08X", p.BubbleIn)
	}

	// choosing the built-in theme again gives Rex back
	s.Settings.CustomTheme = nil
	r.settle(t)
	if got := r.k.Theme().Colors[gtheme.Field]; got == 0x112233FF {
		t.Error("the file's colours stay after the choice is cleared")
	}
	if r.k.Mode() != gtheme.Dark {
		t.Error("Dark was not restored")
	}
}

// A window opened with a theme file is drawn with it from its first frame,
// at any scale (the kit resolves its own Rex first).
func TestAThemeFileShowsInTheFirstFrameAtScale2(t *testing.T) {
	th, _ := newTheme(t, map[string]string{"mine.json": `{"colors":{"accent":"#445566"}}`})
	// let the folder load before the window: the file is chosen at launch
	for th.Loading() {
		time.Sleep(time.Millisecond)
		th.poll()
	}
	s := newState(state.ThemeLight)
	s.Settings.CustomTheme = []byte("mine.json")
	r := open(t, th, s, false, 2)
	r.Frame()
	if got := r.k.Theme().Colors[gtheme.Accent]; got != 0x445566FF {
		t.Errorf("accent in the first frame = %08X", got)
	}
	if r.k.Theme().Scale != 2 {
		t.Errorf("scale = %v", r.k.Theme().Scale)
	}
}

// A broken file is skipped and reported, and the last working palette stays
// when the chosen file breaks (TH-008).
func TestABrokenFileIsSkippedAndTheLastPaletteStays(t *testing.T) {
	th, dir := newTheme(t, map[string]string{
		"good.json": `{"colors":{"accent":"#445566"}}`,
		"bad.json":  `{"colors":{"accent":"nope"}}`,
	})
	s := newState(state.ThemeDark)
	s.Settings.CustomTheme = []byte("good.json")
	var msgs []string
	old := logf
	logf = func(f string, a ...any) { msgs = append(msgs, f) }
	t.Cleanup(func() { logf = old })
	r := open(t, th, s, false, 1)
	r.settle(t)
	if th.Custom() != true || th.File() != "good.json" {
		t.Fatalf("good.json is not in force (file %q)", th.File())
	}
	if got := len(th.Errors()); got != 1 || th.Errors()[0].File != "bad.json" || !strings.Contains(th.Errors()[0].Reason, "accent") {
		t.Errorf("errors = %v", th.Errors())
	}
	if len(msgs) == 0 {
		t.Error("the skipped file is not in the log")
	}
	for _, e := range th.Entries() {
		if e.File == "bad.json" {
			t.Error("the broken file is listed")
		}
	}

	// the chosen file breaks, then reload: the last palette stays
	if err := os.WriteFile(filepath.Join(dir, "good.json"), []byte("{not json"), 0o644); err != nil {
		t.Fatal(err)
	}
	th.Reload()
	r.settle(t)
	if th.File() != "good.json" || r.k.Theme().Colors[gtheme.Accent] != 0x445566FF {
		t.Error("the last working palette did not stay")
	}
	if th.Status() == "" || !strings.Contains(th.Status(), "unavailable") {
		t.Errorf("status = %q", th.Status())
	}

	// and a file that never loaded falls back to the built-in theme
	s.Settings.CustomTheme = []byte("missing.json")
	r.settle(t)
	if th.Custom() {
		t.Error("a missing file is in force")
	}
}

func TestParsePalette(t *testing.T) {
	p, err := ParsePalette([]byte(`{"base":"light","colors":{"text":"#ebdbb2","shadow":"#00000080"}}`))
	if err != nil {
		t.Fatal(err)
	}
	if p.Dark || p.Window != LightPalette().Window || p.Text != 0xEBDBB2FF || p.Shadow != 0x00000080 {
		t.Errorf("palette %+v", p)
	}
	if p, err := ParsePalette([]byte(`{}`)); err != nil || p != DarkPalette() {
		t.Errorf("{} is not the dark base: %v", err)
	}
	for text, want := range map[string]string{
		`{"colors":{"text":"#fff"}}`:    "text: expected #RRGGBB or #RRGGBBAA",
		`{"colors":{"text":"#zzzzzz"}}`: "text:",
		`{"colors":{"text":"ffffff"}}`:  "text:",
		`{"colors":{"typo":"#ffffff"}}`: "unknown color: typo",
		`{"base":"system"}`:             "system",
		`{"typo":true}`:                 "typo",
		`not json`:                      "invalid",
	} {
		if _, err := ParsePalette([]byte(text)); err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("%s: err = %v, want %q", text, err, want)
		}
	}
	// explicit chat colours win over the derived ones
	p, err = ParsePalette([]byte(`{"colors":{"window":"#102030","chat":"#405060"}}`))
	if err != nil || p.Chat != 0x405060FF {
		t.Errorf("explicit chat: %08X, %v", p.Chat, err)
	}
	p, _ = ParsePalette([]byte(`{"colors":{"window":"#102030"}}`))
	if p.Chat != 0x102030FF {
		t.Errorf("derived chat %08X", p.Chat)
	}
}

func TestParseColor(t *testing.T) {
	for s, want := range map[string]uint32{"#0a0b0c": 0x0A0B0CFF, "#0a0b0c80": 0x0A0B0C80, "#FFFFFF": 0xFFFFFFFF} {
		if got, ok := ParseColor(s); !ok || got != want {
			t.Errorf("%q = %08X, %v", s, got, ok)
		}
	}
	for _, s := range []string{"", "#", "#12345", "#1234567", "0a0b0c", "#0a0b0g", "#+a0b0c"} {
		if _, ok := ParseColor(s); ok {
			t.Errorf("%q parsed", s)
		}
	}
}

// The shared palettes are written once as files; an edit is kept and a
// deleted one stays deleted (TH-006).
func TestSharedPalettesAreInstalledOnce(t *testing.T) {
	th, dir := newTheme(t, map[string]string{"Nord.json": `{"base":"light","colors":{"accent":"#102030"}}`})
	for th.Loading() {
		time.Sleep(time.Millisecond)
		th.poll()
	}
	for _, name := range presetNames() {
		if _, err := os.Stat(filepath.Join(dir, name)); err != nil {
			t.Errorf("%s is not installed: %v", name, err)
		}
	}
	if b, _ := os.ReadFile(filepath.Join(dir, "Nord.json")); !strings.Contains(string(b), "102030") {
		t.Error("the user's Nord was rewritten")
	}
	if n := len(th.Entries()); n != 8 {
		t.Errorf("%d palettes listed, want 8", n)
	}
	nord := th.cat.find("Nord.json")
	if nord == nil || nord.Palette.Dark || nord.Palette.Accent != 0x102030FF {
		t.Error("the user's Nord is not the one listed")
	}
	if e := th.cat.find("Catppuccin Latte.json"); e == nil || e.Palette.Dark || e.Name != "Catppuccin Latte" {
		t.Errorf("Catppuccin Latte = %+v", e)
	}

	if err := os.Remove(filepath.Join(dir, "Tokyo Night.json")); err != nil {
		t.Fatal(err)
	}
	th.Reload()
	for th.Loading() {
		time.Sleep(time.Millisecond)
		th.poll()
	}
	if th.cat.find("Tokyo Night.json") != nil || len(th.Entries()) != 7 {
		t.Error("a deleted palette came back")
	}
	if _, err := os.Stat(filepath.Join(dir, "Tokyo Night.json")); err == nil {
		t.Error("a deleted palette was written again")
	}
}

func TestFolderLimits(t *testing.T) {
	dir := t.TempDir()
	big := strings.Repeat(" ", MaxFileBytes+1) + "{}"
	os.WriteFile(filepath.Join(dir, "big.json"), []byte(big), 0o644)
	os.WriteFile(filepath.Join(dir, "ok.json"), []byte(`{}`), 0o644)
	os.WriteFile(filepath.Join(dir, "notes.txt"), []byte(`{}`), 0o644)
	os.Mkdir(filepath.Join(dir, "dir.json"), 0o755)
	c := scan(scanConfig{dir: dir})
	if len(c.entries) != 1 || c.entries[0].File != "ok.json" {
		t.Errorf("entries = %+v", c.entries)
	}
	if len(c.errs) != 1 || c.errs[0].File != "big.json" || !strings.Contains(c.errs[0].Reason, "64 KiB") {
		t.Errorf("errs = %+v", c.errs)
	}
	many := t.TempDir()
	for i := 0; i <= MaxDirectoryEntries; i++ {
		os.WriteFile(filepath.Join(many, strings.Repeat("a", 1)+string(rune('a'+i%26))+strings.Repeat("x", i/26)+".json"), []byte(`{}`), 0o644)
	}
	if c := scan(scanConfig{dir: many}); len(c.entries) != 0 || c.problem != ProblemTooManyEntries {
		t.Errorf("a folder with too many entries lists %d, problem %v", len(c.entries), c.problem)
	}
}

// Applying the theme each frame allocates nothing, with a theme file too.
func TestApplyAllocatesNothing(t *testing.T) {
	th, _ := newTheme(t, map[string]string{"mine.json": `{"colors":{"accent":"#445566"}}`})
	s := newState(state.ThemeSystem)
	r := open(t, th, s, true, 1)
	r.settle(t)
	if n := testing.AllocsPerRun(200, func() { th.Apply(r.k, s) }); n != 0 {
		t.Errorf("Apply allocates %v per frame (built-in theme)", n)
	}
	s.Settings.CustomTheme = []byte("mine.json")
	r.settle(t)
	if n := testing.AllocsPerRun(200, func() { th.Apply(r.k, s) }); n != 0 {
		t.Errorf("Apply allocates %v per frame (theme file)", n)
	}
	for _, f := range []func(){
		func() { Bubble(true) }, func() { OnBubble(true) }, func() { Chat() },
		func() { Link() },
	} {
		if n := testing.AllocsPerRun(100, f); n != 0 {
			t.Errorf("an API read allocates %v", n)
		}
	}
}

// Bubbles: contents stay readable.
func TestOnBubble(t *testing.T) {
	for _, p := range []Palette{DarkPalette(), LightPalette()} {
		for _, own := range []bool{false, true} {
			b := p.OnBubble(own)
			fill := p.BubbleIn
			if own {
				fill = p.BubbleOut
			}
			if c := contrast(b.Secondary, fill); c < 4.5 && contrast(p.Secondary, fill) < 4.5 {
				t.Errorf("dark=%v own=%v: secondary on the bubble %v:1", p.Dark, own, c)
			}
			if c := contrast(b.Read, fill); c < 3 && contrast(p.Read, fill) < 3 {
				t.Errorf("read ticks on the bubble %v:1", c)
			}
		}
	}
}

// A Spotifast palette (the sixteen interface colours) gets the chat colours
// derived from them (TH-005).
func TestSixteenColourPalettesDeriveTheChatColours(t *testing.T) {
	p, err := ParsePalette([]byte(`{"colors":{"window":"#102030","panel":"#142434","surface":"#203040","text":"#e0e0e0","accent":"#80a0c0"}}`))
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range []struct {
		name      string
		got, want uint32
	}{
		{"chat", p.Chat, p.Window},
		{"overlay", p.Overlay, p.Panel},
		{"link", p.Link, p.Accent},
		{"read", p.Read, p.Accent},
		{"bubble_in", p.BubbleIn, lerpGamma(p.Surface, p.Text, 0.05)},
		{"bubble_out", p.BubbleOut, lerpGamma(p.Surface, p.Accent, 0.24)},
	} {
		if c.got != c.want {
			t.Errorf("%s = %08X, want %08X", c.name, c.got, c.want)
		}
	}
}

// Under test the app's own themes folder is left alone.
func TestNewLeavesTheAppFolderAloneUnderTest(t *testing.T) {
	t.Setenv(DirEnv, "")
	if d := New().Dir(); d != "" {
		t.Errorf("New() lists %q under test", d)
	}
	t.Cleanup(func() { cur = initialTheme() })
}

func TestDisplayName(t *testing.T) {
	for file, want := range map[string]string{"Nord.json": "Nord", "Rose Pine Dawn.json": "Rose Pine Dawn", "mine.JSON": "mine", ".json": ".json", "notes": "notes"} {
		if got := DisplayName(file); got != want {
			t.Errorf("DisplayName(%q) = %q, want %q", file, got, want)
		}
	}
}

// OnBubble's kept colours are Palette.OnBubble's, and follow a change of
// the palette they came from.
func TestOnBubbleIsKeptRight(t *testing.T) {
	for _, mine := range []bool{false, true} {
		if got, want := OnBubble(mine), cur.mixed.OnBubble(mine); got != want {
			t.Fatalf("mine %v: kept %+v, want %+v", mine, got, want)
		}
	}
	was := cur.mixed
	defer func() { cur.mixed = was }()
	cur.mixed.Secondary ^= 0x40404000
	if got, want := OnBubble(true), cur.mixed.OnBubble(true); got != want {
		t.Fatalf("after a change: kept %08X, want %08X", got.Secondary, want.Secondary)
	}
}

// What a bubble's palette costs: worked out each time (Palette.OnBubble) and
// as the conversation asks for it, kept (OnBubble).
func BenchmarkOnBubble(b *testing.B) {
	b.Run("search", func(b *testing.B) {
		for i := 0; i < b.N; i++ {
			_ = cur.mixed.OnBubble(i&1 == 0)
		}
	})
	b.Run("kept", func(b *testing.B) {
		for i := 0; i < b.N; i++ {
			_ = OnBubble(i&1 == 0)
		}
	})
}
