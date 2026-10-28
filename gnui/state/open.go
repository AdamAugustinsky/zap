package state

import (
	"errors"
	"os"
	"path/filepath"
	"strings"

	"zapfast/core"
	"zapfast/i18n"
)

// CanOpenAttachment reports whether a file named path is of a format that
// goes to its application (zf:src/safety.rs can_open_attachment): common
// documents and media only. Scripts, installers, app bundles and unknown
// files are shown in their folder instead; the sender's MIME type grants
// nothing.
func CanOpenAttachment(path string) bool {
	switch strings.ToLower(strings.TrimPrefix(filepath.Ext(path), ".")) {
	case "jpg", "jpeg", "png", "gif", "webp", "bmp", "tif", "tiff", "heic", "heif", "avif",
		"mp4", "m4v", "mov", "webm", "mkv", "3gp",
		"mp3", "m4a", "aac", "ogg", "opus", "wav", "flac",
		"pdf", "txt", "log", "csv", "docx", "xlsx", "pptx", "odt", "ods", "odp", "rtf":
		return true
	}
	return false
}

// OpenableFile reports whether path may be handed to its application: a
// regular file itself (not a folder, an app bundle or a symbolic link, which
// could lead to anything) of a format CanOpenAttachment allows. It reads the
// disk.
func OpenableFile(path string) bool {
	st, err := os.Lstat(path)
	return err == nil && st.Mode().IsRegular() && CanOpenAttachment(path)
}

// OpenFile is Action::OpenFile (zf:src/app.rs:6579), the one way a file is
// opened in its application: one OpenableFile allows is opened through the
// Shell; anything else is shown in its folder (core.Revealer) with a note,
// never opened. It works off the main thread (the disk, and opening, can
// take seconds); a failure is a toast. Any goroutine.
func (s *State) OpenFile(path string) { s.OpenFileVia(path, nil, nil) }

// OpenFileVia is OpenFile with what opens and what reveals given: a screen's
// test fakes; nil is the Shell's.
func (s *State) OpenFileVia(path string, open func(string) error, reveal func([]string) error) {
	if path == "" {
		return
	}
	if open == nil {
		sh := s.Shell
		open = func(p string) error {
			if sh == nil {
				return errors.ErrUnsupported
			}
			return sh.Open(p)
		}
	}
	if reveal == nil {
		reveal = func([]string) error { return errors.ErrUnsupported }
		if r, ok := s.Shell.(core.Revealer); ok {
			reveal = r.Reveal
		}
	}
	failed, safety := i18n.T("Could not open the attachment: {error}"), i18n.T("For safety, open this file yourself from its folder")
	go func() {
		if OpenableFile(path) {
			if err := open(path); err != nil {
				s.Toast([]byte(strings.Replace(failed, "{error}", err.Error(), 1)), true)
			}
			return
		}
		s.Toast([]byte(safety), false)
		_ = reveal([]string{path})
	}()
}
