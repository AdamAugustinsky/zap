package audio

import (
	"errors"
	"fmt"
)

// ErrUnavailable is matched (errors.Is) by the error of anything this
// system does not have: CoreAudio off macOS, voice processing that will
// not open. It wraps errors.ErrUnsupported.
var ErrUnavailable = fmt.Errorf("audio: not available: %w", errors.ErrUnsupported)

// ErrNoDevice is the error of a device id the system does not list, or of
// a default when there is no device at all.
var ErrNoDevice = errors.New("audio: no such device")

// StatusError is a CoreAudio call that failed: which, and its OSStatus.
type StatusError struct {
	// Op is the call ("AudioUnitInitialize").
	Op string
	// Status is its OSStatus.
	Status int32
}

func (e *StatusError) Error() string {
	u := uint32(e.Status)
	b := []byte{byte(u >> 24), byte(u >> 16), byte(u >> 8), byte(u)}
	for _, c := range b {
		if c < 0x20 || c > 0x7e {
			return fmt.Sprintf("audio: %s: OSStatus %d", e.Op, e.Status)
		}
	}
	return fmt.Sprintf("audio: %s: OSStatus '%s'", e.Op, b)
}

// UnavailableError says which framework is missing.
type UnavailableError struct {
	What string
	Err  error
}

func (e *UnavailableError) Error() string {
	s := "audio: " + e.What + " is not available"
	if e.Err != nil {
		s += ": " + e.Err.Error()
	}
	return s
}

func (e *UnavailableError) Unwrap() []error {
	if e.Err == nil {
		return []error{ErrUnavailable}
	}
	return []error{ErrUnavailable, e.Err}
}
