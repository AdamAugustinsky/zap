package video

import (
	"errors"
	"fmt"
)

// ErrUnavailable is matched (errors.Is) by the error of a codec this system
// does not have: VideoToolbox off macOS, or a framework that would not
// load. It wraps errors.ErrUnsupported.
var ErrUnavailable = fmt.Errorf("video: not available: %w", errors.ErrUnsupported)

// ErrNeedKeyframe is the decoder's answer to a delta frame before any
// keyframe (or after the stream broke): the caller asks the peer for one.
var ErrNeedKeyframe = errors.New("video: waiting for a keyframe")

// ErrClosed is the error of an encoder or decoder used after Close.
var ErrClosed = errors.New("video: closed")

// ErrFrame is the error of a picture an encoder cannot take: an invalid
// I420, or one of another size than the encoder's.
var ErrFrame = errors.New("video: not a picture this encoder takes")

// StatusError is a framework call that failed: which, and its OSStatus.
type StatusError struct {
	// Op is the call ("VTCompressionSessionCreate").
	Op string
	// Status is its OSStatus.
	Status int32
}

func (e *StatusError) Error() string {
	return fmt.Sprintf("video: %s: %s", e.Op, osStatus(e.Status))
}

// IsBadData says the decoder refused the data itself (the frame is
// damaged), as opposed to a failure of the system.
func (e *StatusError) IsBadData() bool {
	return e.Status == vtBadData
}

// vtBadData is kVTVideoDecoderBadDataErr.
const vtBadData int32 = -12909

// osStatus prints a status as its four characters when it has them.
func osStatus(s int32) string {
	u := uint32(s)
	b := []byte{byte(u >> 24), byte(u >> 16), byte(u >> 8), byte(u)}
	for _, c := range b {
		if c < 0x20 || c > 0x7e {
			return fmt.Sprintf("OSStatus %d", s)
		}
	}
	return fmt.Sprintf("OSStatus '%s'", b)
}

// UnavailableError says which framework or codec is missing.
type UnavailableError struct {
	// What is the framework or codec ("VideoToolbox").
	What string
	// Err is the loader's error, if any.
	Err error
}

func (e *UnavailableError) Error() string {
	s := "video: " + e.What + " is not available"
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
