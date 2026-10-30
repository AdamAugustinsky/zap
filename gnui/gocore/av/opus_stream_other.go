//go:build !darwin

package av

// Off macOS there is no system Opus codec yet: a call's Opus streams fail
// with ErrUnavailable.

// OpusStreamEncoder encodes a call's microphone to Opus (macOS only).
type OpusStreamEncoder struct{}

// NewOpusStreamEncoder fails with ErrUnavailable off macOS.
func NewOpusStreamEncoder(rate, frameSamples, bitrate int) (*OpusStreamEncoder, error) {
	return nil, &UnavailableError{What: "Opus"}
}

// Encode is never reached: there is no encoder.
func (e *OpusStreamEncoder) Encode(dst []byte, pcm []int16) ([]byte, error) {
	return dst, &UnavailableError{What: "Opus"}
}

// Close does nothing.
func (e *OpusStreamEncoder) Close() {}

// OpusStreamDecoder decodes a call's peer from Opus (macOS only).
type OpusStreamDecoder struct{}

// NewOpusStreamDecoder fails with ErrUnavailable off macOS.
func NewOpusStreamDecoder(rate int) (*OpusStreamDecoder, error) {
	return nil, &UnavailableError{What: "Opus"}
}

// Decode is never reached: there is no decoder.
func (d *OpusStreamDecoder) Decode(dst []int16, packet []byte) ([]int16, error) {
	return dst, &UnavailableError{What: "Opus"}
}

// Close does nothing.
func (d *OpusStreamDecoder) Close() {}
