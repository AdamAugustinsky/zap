package settings

import (
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"

	"gonativeui/app/paths"

	"zapfast/i18n"
	"zapfast/state"
	"zapfast/ui/screen"
)

// themeList is the theme menu: the three themes and the palettes in the
// themes folder (zf:src/theme.rs custom_themes). TODO(theme): package theme
// loads the palettes and owns their names; this only lists the files.
type themeList struct {
	mu      sync.Mutex
	found   []string
	has     bool
	scanned bool

	dir   string      // the themes folder
	files []string    // the palettes' file names
	opts  []string    // the menu: the three, then the palettes
	built int         // len(files) opts were built for, +1
	loc   i18n.Locale // the language the three names are in
}

// scan lists the themes folder on a goroutine.
func (t *themeList) scan(wake func()) {
	if dir, err := paths.Config(state.AppID); err == nil {
		t.dir = filepath.Join(dir, "themes")
	}
	dir := t.dir
	go func() {
		var names []string
		if dir != "" {
			if ents, err := os.ReadDir(dir); err == nil {
				for _, ent := range ents {
					if !ent.IsDir() && strings.EqualFold(filepath.Ext(ent.Name()), ".json") {
						names = append(names, ent.Name())
					}
				}
			}
		}
		sort.Strings(names)
		t.mu.Lock()
		t.found, t.has = names, true
		t.mu.Unlock()
		wake()
	}()
}

func (t *themeList) poll(s *Screen) {
	t.mu.Lock()
	has, found := t.has, t.found
	t.has = false
	t.mu.Unlock()
	if has || t.built == 0 || t.loc != s.tx.loc {
		if has {
			t.files = found
		}
		t.opts = append(t.opts[:0], s.tx.theme[:]...)
		for _, f := range t.files {
			t.opts = append(t.opts, displayName(f))
		}
		t.built, t.loc = 1, s.tx.loc
	}
}

// displayName is a palette's name in the menu: its file name without .json.
func displayName(file string) string { return strings.TrimSuffix(file, filepath.Ext(file)) }

// index is the menu's choice for the settings.
func (t *themeList) index(st *state.Settings) int {
	if len(st.CustomTheme) > 0 {
		for i, f := range t.files {
			if f == string(st.CustomTheme) {
				return len(themeChoices) + i
			}
		}
	}
	for i, c := range themeChoices {
		if c == st.Theme {
			return i
		}
	}
	return 0
}

// choose applies menu choice v (zf:src/app.rs SetTheme, SetCustomTheme). The
// theme package applies it.
func (t *themeList) choose(e *screen.Env, v int) {
	st := &e.S.Settings
	switch {
	case v >= 0 && v < len(themeChoices):
		st.Theme = themeChoices[v]
		st.CustomTheme = st.CustomTheme[:0]
	case v-len(themeChoices) < len(t.files):
		st.CustomTheme = append(st.CustomTheme[:0], t.files[v-len(themeChoices)]...)
	default:
		return
	}
	e.S.Touch()
}
