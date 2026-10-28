//go:build !darwin

package procinfo

// AgeNs returns -1: not measured on this system yet.
func AgeNs() int64 { return -1 }

// Read reports false: not measured on this system yet.
func Read() (Usage, bool) { return Usage{}, false }
