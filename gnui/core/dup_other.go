//go:build !unix

package core

import "errors"

// dupCloexec is unix-only: the library is macOS-only for now.
func dupCloexec(int) (int, error) { return -1, errors.ErrUnsupported }
