//go:build !darwin

package av

import (
	"image"
	"time"
)

// VideoPoster needs AVFoundation: elsewhere it fails with ErrUnavailable,
// and a sent video carries its size and length without a thumbnail.
func VideoPoster(path string, at time.Duration, maxSide int) (*image.RGBA, error) {
	return nil, &UnavailableError{What: "a video decoder"}
}
