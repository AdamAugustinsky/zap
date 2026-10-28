//go:build !unix

package sys

import "errors"

// canDup: the core library shares the app's log on unix only
// (core/dup_other.go).
const canDup = false

func dupFd(int) (int, error) { return -1, errors.ErrUnsupported }
