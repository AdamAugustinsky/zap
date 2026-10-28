package theme

import (
	"fmt"
	"log/slog"
	"os"
	"sync"
	"sync/atomic"
	"weak"

	gtheme "gonativeui/theme"
	"gonativeui/ui"
	"gonativeui/widget"

	"zapfast/core"
	"zapfast/i18n"
	"zapfast/internal/testbin"
	"zapfast/state"
	"zapfast/ui/screen"
)

// DirEnv names a folder that replaces the themes folder (tests, screenshots
// and the demo, which must not write to the app's data folder).
const DirEnv = "ZAPFAST_THEMES_DIR"

// logf reports a problem with a theme file; the log keeps no message
// contents, only file names and reasons (FL-004).
var logf = func(format string, args ...any) { slog.Warn(fmt.Sprintf(format, args...)) }

// look is what the settings resolve to.
type look struct {
	light, dark Palette
	pref        gtheme.Preference
	// custom is Rex recoloured by a palette file; nil for the stock Rex,
	// which follows the system's colours and accent.
	custom *gtheme.Theme
	file   string // the palette file of a custom look
}

func builtin(c state.Theme) look {
	switch c {
	case state.ThemeLight:
		return look{light: LightPalette(), dark: DarkPalette(), pref: gtheme.ForceLight}
	case state.ThemeSystem:
		return look{light: LightPalette(), dark: DarkPalette(), pref: gtheme.System}
	}
	return look{light: LightPalette(), dark: DarkPalette(), pref: gtheme.ForceDark}
}

func customLook(e *Entry) look {
	p := e.Palette
	l := look{light: p, dark: p, pref: gtheme.ForceLight, custom: rexTheme(&p), file: e.File}
	if p.Dark {
		l.pref = gtheme.ForceDark
	}
	return l
}

// kitState is what Apply knows of one window's kit.
type kitState struct {
	kit weak.Pointer[widget.Kit]
	th  *gtheme.Theme // the custom theme set in it, nil: the stock Rex
	set bool          // a theme of this package's is set in it (the kit starts with the framework's)
}

// Theme applies the settings' theme to the windows' kits and keeps the
// palettes the conversation draws with (see the package documentation).
//
// It is used from the main thread, except for its loader, which is its own.
type Theme struct {
	dir     string
	presets bool

	// loader
	mu      sync.Mutex
	running bool
	pending *scanConfig
	result  atomic.Pointer[catalog]
	loading atomic.Bool
	wake    atomic.Pointer[state.State]

	// main thread
	cat       catalog
	catGen    uint32
	selValid  bool
	selChoice state.Theme
	selFile   string
	selGen    uint32
	missing   bool // the selected file is not in the catalogue
	look      look
	good      look // the last working custom look
	hasGood   bool
	gen       uint32 // bumps when the look changes
	mixT      float32
	mixGen    uint32
	mixed     Palette
	kits      []kitState
	stock     *gtheme.Theme
}

var _ screen.Theme = (*Theme)(nil)

// cur is the theme the package's functions read: the one New made last.
var cur = func() *Theme {
	t := &Theme{look: builtin(state.ThemeDark), gen: 1}
	t.mixed = t.look.dark
	return t
}()

// New returns the theme for the app's themes folder (or $ZAPFAST_THEMES_DIR)
// and starts listing it on a worker, installing the shared palettes the
// first time. It becomes the theme the package's functions read.
func New() *Theme {
	dir := os.Getenv(DirEnv)
	presets := true
	if dir == "" && testbin.Is() {
		return NewIn("", false) // a test never touches the app's folder
	}
	if dir == "" {
		root, _, err := core.Dirs()
		if err != nil {
			logf("the themes folder is unavailable: %v", err)
			presets = false
		} else {
			dir = root + string(os.PathSeparator) + "themes"
		}
	}
	return NewIn(dir, presets)
}

// NewIn is New for the folder dir; installPresets says whether the shared
// palettes are written there on the first scan. An empty dir lists nothing.
func NewIn(dir string, installPresets bool) *Theme {
	t := &Theme{dir: dir, presets: installPresets, look: builtin(state.ThemeDark), gen: 1}
	t.mixed = t.look.dark
	cur = t
	if dir != "" {
		t.Reload()
	} else {
		t.cat.ok = true
	}
	return t
}

// Dir is the themes folder ("" when there is none).
func (t *Theme) Dir() string { return t.dir }

// EnsureDir creates the themes folder if needed (for Settings → Open themes
// folder) and returns it. It does file I/O: call it from a worker.
func (t *Theme) EnsureDir() (string, error) {
	if t.dir == "" {
		return "", fmt.Errorf("no themes folder")
	}
	return t.dir, os.MkdirAll(t.dir, 0o700)
}

// Reload lists the folder again in the background (`zapfast reload-themes`,
// and a change to the folder): the picker's list and the chosen theme follow
// once it finishes. A scan in progress is followed by one more, the earlier
// result being dropped. Main thread.
func (t *Theme) Reload() {
	if t.dir == "" {
		return
	}
	cfg := scanConfig{dir: t.dir, presets: t.presets}
	if t.selFile != "" {
		cfg.selected = []string{t.selFile}
	}
	t.loading.Store(true)
	t.mu.Lock()
	if t.running {
		t.pending = &cfg
		t.mu.Unlock()
		return
	}
	t.running = true
	t.mu.Unlock()
	go t.run(cfg)
}

func (t *Theme) run(cfg scanConfig) {
	for {
		res := scan(cfg)
		t.mu.Lock()
		if t.pending != nil { // the latest request wins; this result is stale
			cfg, t.pending = *t.pending, nil
			t.mu.Unlock()
			continue
		}
		t.running = false
		t.mu.Unlock()
		t.result.Store(&res)
		if s := t.wake.Load(); s != nil {
			s.Wake()
		}
		return
	}
}

// poll takes a finished scan. It reports whether the catalogue changed.
func (t *Theme) poll() bool {
	res := t.result.Swap(nil)
	if res == nil {
		return false
	}
	t.loading.Store(false)
	t.cat = *res
	t.catGen++
	return true
}

// Apply sets the window's appearance from the settings, once per frame
// before the kit's Begin: the preference (light, dark or the system's, so the
// first frame is already in the right mode), and Rex recoloured by a theme
// file when one is chosen. It is cheap when nothing changed and allocates
// nothing then.
func (t *Theme) Apply(k *widget.Kit, s *state.State) {
	if t.wake.Load() != s {
		t.wake.Store(s)
	}
	t.poll()
	st := &s.Settings
	if !t.selValid || t.selChoice != st.Theme || t.selGen != t.catGen || t.selFile != string(st.CustomTheme) {
		t.selValid, t.selChoice, t.selGen = true, st.Theme, t.catGen
		if t.selFile != string(st.CustomTheme) {
			t.selFile = string(st.CustomTheme)
		}
		t.resolve()
		t.gen++
	}
	k.SetPreference(t.look.pref)
	t.install(k)
	c := k.Context()

	// The palette the conversation reads: the kit's mix, or at the first
	// frame the mode it is heading for.
	mt := k.Theme().T
	if k.Scale() == 0 && c != nil {
		mt = 0
		if t.target(c) == gtheme.Dark {
			mt = 1
		}
	}
	if mt != t.mixT || t.mixGen != t.gen {
		t.mixT, t.mixGen = mt, t.gen
		t.mixed = mixPalette(&t.look.light, &t.look.dark, mt)
	}
	cur = t
}

// target is the mode the look heads for.
func (t *Theme) target(c *ui.Context) gtheme.Mode {
	switch t.look.pref {
	case gtheme.ForceLight:
		return gtheme.Light
	case gtheme.ForceDark:
		return gtheme.Dark
	}
	if a := c.SystemAppearance(); a.Known && a.Dark {
		return gtheme.Dark
	}
	return gtheme.Light
}

// resolve turns the settings into the look (TH-001..TH-005, TH-008).
func (t *Theme) resolve() {
	t.missing = false
	if t.selFile != "" {
		if e := t.cat.find(t.selFile); e != nil {
			t.look = customLook(e)
			t.good, t.hasGood = t.look, true
			return
		}
		if t.cat.ok {
			// Gone or broken: keep the last palette that worked.
			t.missing = true
			if t.hasGood && t.good.file == t.selFile {
				t.look = t.good
				return
			}
		} else if t.look.file == t.selFile {
			return // still loading: the look it has
		}
	}
	t.look = builtin(t.selChoice)
}

// install gives the kit the look's Rex: a recoloured one for a theme file,
// the stock one otherwise (Kit.SetTheme: the kit resolves it at its next
// Begin, keeping the light/dark mix, and again after every scale change).
func (t *Theme) install(k *widget.Kit) {
	ks := t.kitOf(k)
	want := t.look.custom
	if ks.set && ks.th == want {
		return
	}
	th := want
	if th == nil {
		if t.stock == nil {
			t.stock = Rex()
		}
		th = t.stock
	}
	k.SetTheme(th)
	ks.th, ks.set = want, true
}

// kitOf is the state of k, made on first sight; kits of closed windows are
// dropped.
func (t *Theme) kitOf(k *widget.Kit) *kitState {
	for i := range t.kits {
		if t.kits[i].kit.Value() == k {
			return &t.kits[i]
		}
	}
	live := t.kits[:0]
	for _, ks := range t.kits {
		if ks.kit.Value() != nil {
			live = append(live, ks)
		}
	}
	clear(t.kits[len(live):])
	t.kits = append(live, kitState{kit: weak.Make(k)})
	return &t.kits[len(t.kits)-1]
}

// Entries are the palettes the picker offers, sorted by file name. The slice
// is valid until the next frame.
func (t *Theme) Entries() []Entry { return t.cat.entries }

// Errors are the theme files the last scan skipped, with their reasons.
func (t *Theme) Errors() []LoadError { return t.cat.errs }

// Loading reports whether a scan is running.
func (t *Theme) Loading() bool { return t.loading.Load() }

// Status is what Settings says under the theme setting while the folder
// loads or when something went wrong; "" when there is nothing to say.
func (t *Theme) Status() string {
	switch {
	case t.Loading():
		return i18n.T("Loading local themes…")
	case t.missing:
		return i18n.T("The selected theme is unavailable. Keeping the last usable appearance. See the log for details.")
	}
	return problemText(t.cat.problem)
}

// Colors is the palette the conversation draws with, at the window's
// current light/dark mix: Rex's fade carries it. Read it each frame; do not
// keep it. Read-only.
func (t *Theme) Colors() *Palette { return &t.mixed }

// Custom reports whether a theme file is in force.
func (t *Theme) Custom() bool { return t.look.custom != nil }

// File is the theme file in force ("" for the built-in themes).
func (t *Theme) File() string { return t.look.file }
