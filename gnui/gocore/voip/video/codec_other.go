//go:build !darwin

package video

// Off macOS there is no system H.264 codec yet: NewEncoder and NewDecoder
// fail with ErrUnavailable.

type sysEncoder struct{}

func newSysEncoder(EncoderConfig) (*sysEncoder, error) {
	return nil, &UnavailableError{What: "VideoToolbox"}
}

func (*sysEncoder) encode(*I420, int64, bool) ([]byte, error) { return nil, ErrUnavailable }
func (*sysEncoder) setBitrate(int) error                      { return ErrUnavailable }
func (*sysEncoder) close()                                    {}

type sysDecoder struct{}

func newSysDecoder() (*sysDecoder, error) { return nil, &UnavailableError{What: "VideoToolbox"} }

func (*sysDecoder) decode([]byte) (*I420, error) { return nil, ErrUnavailable }
func (*sysDecoder) close()                       {}
