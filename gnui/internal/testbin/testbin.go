// Package testbin tells a Go test binary (pkg.test) from the app by its name,
// as the framework's launchguard does, without linking package testing (and
// its flags) into the app the way testing.Testing would.
package testbin

import (
	"os"
	"path/filepath"
	"strings"
)

var is = named(os.Args[0])

func named(arg0 string) bool { return strings.HasSuffix(filepath.Base(arg0), ".test") }

// Is reports whether the process is a Go test binary.
func Is() bool { return is }
