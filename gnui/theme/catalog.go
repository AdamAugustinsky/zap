package theme

import (
	"bytes"
	"embed"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"unicode/utf8"

	"zapfast/i18n"
)

// The limits of fastframe-theme's catalogue.
const (
	// MaxFileBytes is the largest palette file read.
	MaxFileBytes = 64 * 1024
	// MaxDirectoryEntries: a themes folder with more entries is not listed at
	// all, rather than a subset that depends on the filesystem's order.
	MaxDirectoryEntries = 512
	// MaxThemes is the most palettes listed.
	MaxThemes = 128
)

// installedRecord is the file in the themes folder that lists the shared
// palettes already installed there, one filename per line.
const installedRecord = ".installed-palettes"

// presetFS holds the shared palettes (Catppuccin, Nord, Rosé Pine …),
// embedded so every installation has them.
//
//go:embed presets/*.json
var presetFS embed.FS

// Problem is why the palettes could not all be listed.
type Problem uint8

const (
	// NoProblem: everything listed.
	NoProblem Problem = iota
	// ProblemUnreadable: the themes folder exists and could not be read.
	ProblemUnreadable
	// ProblemTooManyEntries: more than MaxDirectoryEntries entries.
	ProblemTooManyEntries
	// ProblemTooManyThemes: more than MaxThemes palettes; the rest are not
	// listed.
	ProblemTooManyThemes
	// ProblemLoaderFailed: the loader stopped without an answer.
	ProblemLoaderFailed
)

// Entry is a palette file, known by its file name in the themes folder.
type Entry struct {
	// File is the file name, "Nord.json".
	File string
	// Name is what the picker shows, "Nord".
	Name    string
	Palette Palette
}

// LoadError is a theme file that was skipped, and why (TH-008).
type LoadError struct {
	File   string
	Reason string
}

func (e LoadError) Error() string {
	return fmt.Sprintf("unable to load theme %q: %s", e.File, e.Reason)
}

// catalog is one scan's result.
type catalog struct {
	entries []Entry // sorted by file name
	problem Problem
	errs    []LoadError
	ok      bool // the scan finished (false: still the empty start)
}

func (c *catalog) find(file string) *Entry {
	for i := range c.entries {
		if c.entries[i].File == file {
			return &c.entries[i]
		}
	}
	return nil
}

// scanConfig is what a scan needs.
type scanConfig struct {
	dir      string
	selected []string // the saved choices, read first
	presets  bool     // install the shared palettes as files, once
}

// scan lists the themes folder: installs the shared palettes (first start
// only, never rewritten, a deleted one stays deleted), then reads every
// valid palette file, the selected ones first so a large folder cannot push
// them out. A broken file is skipped with its reason in errs.
func scan(cfg scanConfig) catalog {
	installed := map[string]bool{}
	if cfg.presets {
		var err error
		if installed, err = installPresets(cfg.dir); err != nil {
			installed = map[string]bool{}
			logf("unable to install the shared palettes: %v", err)
		}
	}
	var sel []string
	for _, f := range cfg.selected {
		if !cfg.presets || !isPreset(f) || fileExists(filepath.Join(cfg.dir, f)) {
			sel = append(sel, f)
		}
	}
	c := discover(cfg.dir, sel)
	if cfg.presets {
		for _, f := range presetNames() {
			if installed[f] {
				continue
			}
			listed := c.find(f) != nil
			if contains(sel, f) && fileExists(filepath.Join(cfg.dir, f)) && !listed {
				// A broken override of a selected preset keeps the cached
				// selection; it does not silently become the shared one.
				continue
			}
			if !listed {
				data, _ := presetFS.ReadFile("presets/" + f)
				if p, err := ParsePalette(data); err == nil {
					c.entries = append(c.entries, Entry{File: f, Name: DisplayName(f), Palette: p})
				}
			}
		}
		sortEntries(c.entries)
	}
	c.ok = true
	return c
}

func sortEntries(e []Entry) {
	sort.Slice(e, func(i, j int) bool { return e[i].File < e[j].File })
}

func contains(l []string, s string) bool {
	for _, v := range l {
		if v == s {
			return true
		}
	}
	return false
}

func fileExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

func presetNames() []string {
	ents, _ := presetFS.ReadDir("presets")
	names := make([]string, 0, len(ents))
	for _, e := range ents {
		names = append(names, e.Name())
	}
	return names
}

func isPreset(file string) bool { return contains(presetNames(), file) }

// installPresets writes each shared palette not yet recorded in the folder's
// record, unless something already has its name, and records it (TH-006).
// Files go through a temporary file and a rename, and nothing is written
// through a symbolic link. It returns the names recorded, now and before.
func installPresets(dir string) (map[string]bool, error) {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, err
	}
	record := filepath.Join(dir, installedRecord)
	installed := map[string]bool{}
	if b, err := os.ReadFile(record); err == nil {
		for _, l := range strings.Split(string(b), "\n") {
			if l != "" {
				installed[l] = true
			}
		}
	}
	before := len(installed)
	for _, name := range presetNames() {
		if installed[name] {
			continue
		}
		// Anything already there, even a broken link, is the user's.
		if _, err := os.Lstat(filepath.Join(dir, name)); err != nil {
			data, _ := presetFS.ReadFile("presets/" + name)
			if err := writeNew(dir, name, data); err != nil {
				return installed, err
			}
		}
		installed[name] = true
	}
	if len(installed) != before {
		names := make([]string, 0, len(installed))
		for n := range installed {
			names = append(names, n)
		}
		sort.Strings(names)
		var b bytes.Buffer
		for _, n := range names {
			b.WriteString(n + "\n")
		}
		if err := writeNew(dir, installedRecord, b.Bytes()); err != nil {
			return installed, err
		}
	}
	return installed, nil
}

// writeNew writes data to name in dir through a temporary file.
func writeNew(dir, name string, data []byte) error {
	tmp := filepath.Join(dir, "."+name+".tmp")
	err := os.WriteFile(tmp, data, 0o644)
	if err == nil {
		err = os.Rename(tmp, filepath.Join(dir, name))
	}
	if err != nil {
		os.Remove(tmp)
	}
	return err
}

// fileIsLocal reports whether file is a single .json file name, never a path.
func fileIsLocal(file string) bool {
	return file != "" && file != "." && file != ".." && !strings.ContainsAny(file, "/\\") &&
		filepath.Base(file) == file && strings.HasSuffix(file, ".json")
}

// readTheme reads one palette file in dir: a regular file, or a link to one
// (a palette another tool such as pywal writes elsewhere), at most
// MaxFileBytes, UTF-8, and a valid palette.
func readTheme(dir, file string) (Entry, error) {
	if !fileIsLocal(file) {
		return Entry{}, errors.New("expected a JSON filename in the themes folder")
	}
	path := filepath.Join(dir, file)
	st, err := os.Stat(path) // follows a link: a link to a folder or nothing is refused
	if err != nil {
		return Entry{}, unwrapPathError(err)
	}
	if !st.Mode().IsRegular() {
		return Entry{}, errors.New("expected a regular file, not a directory")
	}
	if st.Size() > MaxFileBytes {
		return Entry{}, errors.New("theme exceeds the 64 KiB file limit")
	}
	f, err := os.Open(path)
	if err != nil {
		return Entry{}, unwrapPathError(err)
	}
	defer f.Close()
	b, err := io.ReadAll(io.LimitReader(f, MaxFileBytes+1))
	if err != nil {
		return Entry{}, unwrapPathError(err)
	}
	if len(b) > MaxFileBytes {
		return Entry{}, errors.New("theme exceeds the 64 KiB file limit")
	}
	if !utf8.Valid(b) {
		return Entry{}, errors.New("expected UTF-8 JSON")
	}
	p, err := ParsePalette(b)
	if err != nil {
		return Entry{}, err
	}
	return Entry{File: file, Name: DisplayName(file), Palette: p}, nil
}

func unwrapPathError(err error) error {
	var pe *fs.PathError
	if errors.As(err, &pe) {
		return pe.Err
	}
	return err
}

// discover lists the palette files in dir, never recursing.
func discover(dir string, selected []string) catalog {
	var c catalog
	fail := func(file string, err error) {
		c.errs = append(c.errs, LoadError{File: file, Reason: err.Error()})
		logf("%v", c.errs[len(c.errs)-1])
	}
	for _, f := range selected {
		if e, err := readTheme(dir, f); err == nil {
			c.entries = append(c.entries, e)
		} else {
			fail(f, err)
		}
	}
	d, err := os.Open(dir)
	if err != nil {
		if !errors.Is(err, fs.ErrNotExist) {
			c.problem = ProblemUnreadable
			logf("unable to read themes at %s: %v", dir, err)
		}
		return c
	}
	defer d.Close()
	ents, err := d.ReadDir(MaxDirectoryEntries + 1)
	if err != nil && err != io.EOF {
		c.problem = ProblemUnreadable
		logf("unable to read themes at %s: %v", dir, err)
		return c
	}
	if len(ents) > MaxDirectoryEntries {
		// Offer no subset that depends on the filesystem's order.
		c.problem = ProblemTooManyEntries
		return c
	}
	var names []string
	for _, e := range ents {
		f := e.Name()
		if !fileIsLocal(f) || contains(selected, f) {
			continue
		}
		switch {
		case e.Type().IsRegular():
			names = append(names, f)
		case e.Type()&fs.ModeSymlink != 0: // a link counts when it leads to a regular file
			if st, err := os.Stat(filepath.Join(dir, f)); err == nil && st.Mode().IsRegular() {
				names = append(names, f)
			}
		}
	}
	sort.Strings(names)
	if len(names)+len(c.entries) > MaxThemes {
		c.problem = ProblemTooManyThemes
	}
	room := max(MaxThemes-len(c.entries), 0)
	for _, f := range names[:min(len(names), room)] {
		if e, err := readTheme(dir, f); err == nil {
			c.entries = append(c.entries, e)
		} else {
			fail(f, err)
		}
	}
	sortEntries(c.entries)
	return c
}

// problemText is what Settings says under the theme setting when the folder
// could not be listed (zf:src/theme.rs:35, upstream's exact English).
func problemText(p Problem) string {
	switch p {
	case ProblemUnreadable:
		return i18n.T("The themes folder could not be read. See the log for details.")
	case ProblemTooManyEntries:
		return i18n.T("The themes folder has more than 512 entries. Keep fewer files there to list the custom palettes.")
	case ProblemTooManyThemes:
		return i18n.T("Only 128 custom palettes can be listed. Keep fewer JSON files in the themes folder to see the rest.")
	case ProblemLoaderFailed:
		return i18n.T("Custom themes could not be loaded. Run zapfast reload-themes to try again.")
	}
	return ""
}
