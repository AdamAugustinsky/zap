package main

import (
	"fmt"
	"path/filepath"
	"strings"

	appshell "gonativeui/app/shell"

	"zapfast/core"
)

// opener is the app's state.Shell, the only real one: web and mail links open
// in the browser through the framework (app/shell), a file (an absolute
// path) with open(1) as the core's opener does. Anything else, a file: URL
// or another scheme's handler, is refused. Inside a test binary it refuses
// (core.GuardOpen).
type opener struct{}

// openURL opens a link in the browser, openFilePath a file with open(1), and
// reveal shows files in Finder; tests replace them.
var (
	openURL      = appshell.OpenURL
	openFilePath = core.ExecShell{}.Open
	reveal       = appshell.Reveal
)

func (opener) Open(path string) error {
	link := isLink(path)
	if !link && !filepath.IsAbs(path) {
		return fmt.Errorf("%w: neither a web or mail address nor a file: %q", core.ErrOpenRefused, path)
	}
	if err := core.GuardOpen(); err != nil {
		return err
	}
	if link {
		return openURL(path)
	}
	return openFilePath(path)
}

// isLink reports whether s is a web or mail address: http, https and mailto
// are the only schemes the opener hands on.
func isLink(s string) bool {
	l := strings.ToLower(s)
	return strings.HasPrefix(l, "https://") || strings.HasPrefix(l, "http://") || strings.HasPrefix(l, "mailto:")
}

// Reveal shows paths in Finder (core.Revealer).
func (opener) Reveal(paths []string) error {
	if err := core.GuardOpen(); err != nil {
		return err
	}
	return reveal(paths)
}
