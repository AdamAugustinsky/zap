//go:build darwin

package procinfo

import (
	"os"
	"sync"
	"unsafe"

	"gonativeui/clib"
)

// rusageInfo is rusage_info_v4 (<sys/resource.h>), read up to the start time.
type rusageInfo struct {
	uuid           [16]byte
	userTime       uint64
	systemTime     uint64
	pkgIdleWkups   uint64
	interruptWkups uint64
	pageins        uint64
	wiredSize      uint64
	residentSize   uint64
	physFootprint  uint64
	procStartAbs   uint64
	rest           [80]uint64 // the remaining v4 fields, with room to spare
}

var (
	mu              sync.Mutex
	ready           bool
	call            *clib.Call
	info            = new(rusageInfo)
	tb              = new([2]uint32) // mach_timebase_info: numer, denom
	fnRusage, fnAbs clib.Func
)

func setup() bool {
	if ready {
		return true
	}
	lib, err := clib.Open("/usr/lib/libSystem.B.dylib")
	if err != nil {
		return false
	}
	tbFn, err1 := lib.Func("mach_timebase_info")
	r, err2 := lib.Func("proc_pid_rusage")
	a, err3 := lib.Func("mach_absolute_time")
	if err1 != nil || err2 != nil || err3 != nil {
		return false
	}
	call = clib.NewCall()
	if call.Reset().Ptr(unsafe.Pointer(tb)).Do(tbFn).Int32() != 0 || tb[1] == 0 {
		return false
	}
	fnRusage, fnAbs, ready = r, a, true
	return true
}

// AgeNs returns nanoseconds since the kernel created this process, or -1.
func AgeNs() int64 {
	mu.Lock()
	defer mu.Unlock()
	if !setup() {
		return -1
	}
	const rusageInfoV4 = 4
	if call.Reset().Int32(int32(os.Getpid())).Int32(rusageInfoV4).Ptr(unsafe.Pointer(info)).Do(fnRusage).Int32() != 0 {
		return -1
	}
	now := call.Reset().Do(fnAbs).Uint()
	return int64((now - info.procStartAbs) * uint64(tb[0]) / uint64(tb[1]))
}

// Read returns the process's usage so far (proc_pid_rusage); ok is false
// where it cannot be read.
func Read() (u Usage, ok bool) {
	mu.Lock()
	defer mu.Unlock()
	if !setup() {
		return u, false
	}
	const rusageInfoV4 = 4
	if call.Reset().Int32(int32(os.Getpid())).Int32(rusageInfoV4).Ptr(unsafe.Pointer(info)).Do(fnRusage).Int32() != 0 {
		return u, false
	}
	abs := func(t uint64) int64 { return int64(t * uint64(tb[0]) / uint64(tb[1])) }
	return Usage{
		Footprint: info.physFootprint, Resident: info.residentSize,
		CPUNs:       abs(info.userTime) + abs(info.systemTime),
		IdleWakeups: info.pkgIdleWkups, Wakeups: info.interruptWkups,
	}, true
}
