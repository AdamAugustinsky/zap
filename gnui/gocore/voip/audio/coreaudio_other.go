//go:build !darwin

package audio

// Off macOS there is no device backend yet: Open and the device lists fail
// with ErrUnavailable.

func system() (backend, error) { return nil, &UnavailableError{What: "CoreAudio"} }
