// Package buildinfo is the build's identity: the revisions of Zap and of the
// framework it was built against, which packaging/macos/gnui-package.sh
// stamps in (-ldflags -X, from scripts/gnui/release_identity.py, after it
// has checked the framework), with what the Go toolchain records itself
// (runtime/debug.ReadBuildInfo: the go version, the tags and Zap's own VCS
// revision, never the framework's, which a replaced module does not carry).
// The bundle's Contents/Resources/build-manifest.json has the rest.
package buildinfo

import (
	"io"
	"log/slog"
	"runtime/debug"
)

// Set by the packaging script; empty in a build it did not make.
var (
	revision          string
	dirty             string
	frameworkRevision string
	frameworkDirty    string
	frameworkPin      string // "pinned" (gnui/release-deps.json) or "override"
)

// Info is one build's identity.
type Info struct {
	Revision string // Zap's commit, "" when unknown
	Dirty    bool   // Zap's checkout had uncommitted changes
	// FrameworkRevision is the framework's commit and FrameworkPin
	// "pinned" or "override", both "" when the packaging script did not
	// build this binary. They are the packager's assertion.
	FrameworkRevision, FrameworkPin string
	FrameworkDirty                  bool
	Go, Tags                        string // the toolchain and the -tags it built with
}

// Read returns this binary's identity.
func Read() Info {
	i := Info{
		Revision: revision, Dirty: dirty == "true",
		FrameworkRevision: frameworkRevision, FrameworkPin: frameworkPin, FrameworkDirty: frameworkDirty == "true",
	}
	if bi, ok := debug.ReadBuildInfo(); ok {
		i.fill(bi)
	}
	return i
}

// fill takes what the stamps leave out from the toolchain's record: its
// version and tags, and the VCS revision a plain go build stamps.
func (i *Info) fill(bi *debug.BuildInfo) {
	i.Go = bi.GoVersion
	stamped := i.Revision != ""
	for _, s := range bi.Settings {
		switch s.Key {
		case "-tags":
			i.Tags = s.Value
		case "vcs.revision":
			if !stamped {
				i.Revision = s.Value
			}
		case "vcs.modified":
			if !stamped {
				i.Dirty = s.Value == "true"
			}
		}
	}
}

// Short is Zap's revision for display: its first 12 characters, with
// "-dirty" for uncommitted changes, or "" when unknown.
func (i Info) Short() string {
	if i.Revision == "" {
		return ""
	}
	s := i.Revision[:min(12, len(i.Revision))]
	if i.Dirty {
		s += "-dirty"
	}
	return s
}

// Log writes the identity to w as one slog text record at info level,
// whatever the default logger's level: the first line of a run's log, which
// bug reports carry. It names revisions and versions only.
func Log(w io.Writer, version string) {
	if w == nil {
		return
	}
	i := Read()
	slog.New(slog.NewTextHandler(w, nil)).Info("build",
		"version", version, "revision", orUnknown(i.Revision), "dirty", i.Dirty,
		"framework", orUnknown(i.FrameworkRevision), "framework_pin", orUnknown(i.FrameworkPin),
		"framework_dirty", i.FrameworkDirty, "go", i.Go, "tags", i.Tags)
}

func orUnknown(s string) string {
	if s == "" {
		return "unknown"
	}
	return s
}
