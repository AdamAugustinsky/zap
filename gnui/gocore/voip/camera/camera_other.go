//go:build !darwin

package camera

// Off macOS there is no camera capture yet: Devices and Open fail with
// ErrUnavailable.

type sysCamera struct{}

func sysDevices() ([]Device, error) { return nil, ErrUnavailable }

func openSys(Config, *queue) (*sysCamera, opened, error) { return nil, opened{}, ErrUnavailable }

func (*sysCamera) alive() bool { return false }
func (*sysCamera) close()      {}
