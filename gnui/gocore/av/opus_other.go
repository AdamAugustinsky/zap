//go:build !darwin

package av

// Off macOS there is no system Opus codec yet: encoding and decoding voice
// notes fail with ErrUnavailable.

func newOpusEncoder() (opusEncoder, error) { return nil, &UnavailableError{What: "Opus"} }

func newOpusDecoder(int) (opusDecoder, error) { return nil, &UnavailableError{What: "Opus"} }
