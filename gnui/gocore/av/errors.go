package av

import (
	"errors"
	"fmt"
	"strings"
)

// ErrUnavailable is matched (errors.Is) by the error of anything that needs
// a codec or library this system does not have: Opus off macOS, or
// libzfwebp.dylib not found. It wraps errors.ErrUnsupported.
var ErrUnavailable = fmt.Errorf("av: not available: %w", errors.ErrUnsupported)

// UnavailableError says which codec or library is missing, and where it was
// looked for.
type UnavailableError struct {
	// What is the codec or library ("Opus", "libzfwebp.dylib").
	What string
	// Path is the library looked for or loaded; empty when nothing was found.
	Path string
	// Searched lists the places looked in, for a library not found.
	Searched []string
	// Err is the loader's or the system's error, if any.
	Err error
}

func (e *UnavailableError) Error() string {
	var b strings.Builder
	b.WriteString("av: " + e.What + " is not available")
	if e.Path != "" {
		b.WriteString(" (" + e.Path + ")")
	} else if len(e.Searched) > 0 {
		b.WriteString(" (looked above " + strings.Join(e.Searched, ", ") + ")")
	}
	if e.Err != nil {
		b.WriteString(": " + e.Err.Error())
	}
	return b.String()
}

func (e *UnavailableError) Unwrap() []error {
	if e.Err == nil {
		return []error{ErrUnavailable}
	}
	return []error{ErrUnavailable, e.Err}
}

// Voice errors, as zfcore's VoiceError.
var (
	// ErrOutside: the file is not under one of the allowed folders.
	ErrOutside = errors.New("av: the file is not in the account's folders")
	// ErrNotFound: there is no such file.
	ErrNotFound = errors.New("av: no such file")
	// ErrTooLong: a recording over 15 minutes, or a note over 30 minutes or
	// 64 MiB.
	ErrTooLong = errors.New("av: the voice note is too long")
	// ErrMalformed: not Ogg/Opus, or damaged. The error that matches it says
	// why.
	ErrMalformed = errors.New("av: not a voice note")
)

func malformed(why string) error { return fmt.Errorf("%w: %s", ErrMalformed, why) }

// Picture errors.
var (
	// ErrPicture: the bytes are not a picture this package reads. The error
	// that matches it says why.
	ErrPicture = errors.New("av: this picture could not be read")
	// ErrTooLarge: a picture over 100 megapixels or 16384 pixels a side, or
	// a sticker that does not fit WhatsApp's limit.
	ErrTooLarge = errors.New("av: too large")
)
