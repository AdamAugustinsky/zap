package calls

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"
	"unsafe"

	"gonativeui/clib"

	"zapfast/abi"
	"zapfast/core"
)

// LibName is the call library's file name, in the bundle's Frameworks.
const LibName = "libzfcalls.dylib"

// EnvLibrary names the call library to load in an unbundled run
// (development, tests); a bundled app ignores it.
const EnvLibrary = "ZAPFAST_CALLS"

// abiVersion is the ZC_ABI this package speaks (zfcalls.h).
const abiVersion = 1

// The zc_* status codes (zfcalls.h).
const (
	zcOK    = 0
	zcSmall = -2
)

// ErrNoLibrary is wrapped by LoadLib's error when the call library is not
// there: a build without it places and takes no calls.
var ErrNoLibrary = errors.New("the call library is not installed")

// The loading policy is the core library's (core.ResolveLibrary): a bundled
// app loads only its Contents/Frameworks/libzfcalls.dylib; an unbundled run
// takes $ZAPFAST_CALLS, then the newest cargo build (target/<profile>, and
// $CARGO_TARGET_DIR's) above the working directory or the executable.
func resolveLib(exe, wd string, getenv func(string) string) (string, error) {
	if lib, bundled := bundleLib(exe); bundled {
		if !isFile(lib) {
			return "", fmt.Errorf("zapfast calls: %s is missing from the app bundle: %w", lib, ErrNoLibrary)
		}
		return lib, nil
	}
	if p := getenv(EnvLibrary); p != "" {
		abs, err := filepath.Abs(p)
		if err != nil {
			return "", fmt.Errorf("zapfast calls: %q: %w", p, err)
		}
		if !isFile(abs) {
			return "", fmt.Errorf("zapfast calls: %s: %w", abs, ErrNoLibrary)
		}
		return abs, nil
	}
	var roots []string
	if t := getenv("CARGO_TARGET_DIR"); t != "" {
		roots = append(roots, t)
	}
	for _, dir := range []string{wd, filepath.Dir(exe)} {
		for dir != "" && dir != "." {
			roots = append(roots, filepath.Join(dir, "target"))
			parent := filepath.Dir(dir)
			if parent == dir {
				break
			}
			dir = parent
		}
	}
	var best string
	var bestT time.Time
	for _, root := range roots {
		for _, profile := range [...]string{"core-release", "debug", "release"} {
			p := filepath.Join(root, profile, LibName)
			if st, err := os.Stat(p); err == nil && st.Mode().IsRegular() && st.ModTime().After(bestT) {
				best, bestT = p, st.ModTime()
			}
		}
		if best != "" {
			return best, nil
		}
	}
	return "", fmt.Errorf("zapfast calls: %s not found (%s is unset and no cargo build of zfcalls is above the working directory): %w", LibName, EnvLibrary, ErrNoLibrary)
}

func bundleLib(exe string) (string, bool) {
	macos := filepath.Dir(exe)
	contents := filepath.Dir(macos)
	if exe == "" || filepath.Base(macos) != "MacOS" || filepath.Base(contents) != "Contents" ||
		filepath.Ext(filepath.Dir(contents)) != ".app" {
		return "", false
	}
	return filepath.Join(contents, "Frameworks", LibName), true
}

func isFile(p string) bool {
	st, err := os.Stat(p)
	return err == nil && st.Mode().IsRegular()
}

// Lib is the loaded call library.
type Lib struct {
	Path string
	fn   struct {
		abi, newCtx, free, open, command, close   clib.Func
		next, devices, frameAcquire, frameRelease clib.Func
	}
}

var (
	libOnce sync.Once
	libVal  *Lib
	libErr  error
)

// LoadLib loads the call library once per process, under the loading
// policy. Its error wraps ErrNoLibrary when there is none to load.
func LoadLib() (*Lib, error) {
	libOnce.Do(func() {
		exe, _ := os.Executable()
		if r, err := filepath.EvalSymlinks(exe); err == nil {
			exe = r
		}
		wd, _ := os.Getwd()
		path, err := resolveLib(exe, wd, os.Getenv)
		if err != nil {
			libErr = err
			return
		}
		libVal, libErr = openLib(path)
	})
	return libVal, libErr
}

func openLib(path string) (*Lib, error) {
	cl, err := clib.Open(path)
	if err != nil {
		return nil, fmt.Errorf("zapfast calls: %w", err)
	}
	l := &Lib{Path: path}
	for _, f := range [...]struct {
		dst  *clib.Func
		name string
	}{
		{&l.fn.abi, "zc_abi_version"},
		{&l.fn.newCtx, "zc_new"},
		{&l.fn.free, "zc_free"},
		{&l.fn.open, "zc_open"},
		{&l.fn.command, "zc_command"},
		{&l.fn.close, "zc_close"},
		{&l.fn.next, "zc_next_event"},
		{&l.fn.devices, "zc_devices"},
		{&l.fn.frameAcquire, "zc_frame_acquire"},
		{&l.fn.frameRelease, "zc_frame_release"},
	} {
		if *f.dst, err = cl.Func(f.name); err != nil {
			return nil, fmt.Errorf("zapfast calls: %w", err)
		}
	}
	if v := clib.NewCall().Do(l.fn.abi).Uint32(); v != abiVersion {
		return nil, fmt.Errorf("zapfast calls: %s has ZC_ABI %d, this app needs %d", path, v, abiVersion)
	}
	return l, nil
}

// zcErr is zc_err.
type zcErr struct {
	code int32
	len  uint32
	msg  [256]byte
}

func (e *zcErr) err(op string) error {
	return &Error{Op: op, Code: e.code, Msg: string(e.msg[:min(int(e.len), len(e.msg))])}
}

// Error is a zc_* call's failure.
type Error struct {
	Op   string // the C function
	Code int32  // a ZC_E_* code
	Msg  string
}

func (e *Error) Error() string { return "zapfast calls: " + e.Op + ": " + e.Msg }

// errClosed: the context was freed.
var errClosed = errors.New("zapfast calls: the call context is closed")

// zcFrame is zc_frame (zf_frame's layout).
type zcFrame struct {
	px     uint64 // C memory
	w, h   uint32
	stride [3]uint32
	fmt    uint32
	pts    int64
}

// libMedia is Media over one zc_ctx. Each entry point has its own Call
// under its own lock, so the event wait never holds up a command.
type libMedia struct {
	lib *Lib
	ctx unsafe.Pointer

	mu  sync.Mutex // open, command, close, devices
	c   *clib.Call
	err zcErr

	nextMu  sync.Mutex
	nextC   *clib.Call
	nextErr zcErr
	buf     []byte

	frameMu  sync.Mutex
	frameC   *clib.Call
	frameErr zcErr
	frame    zcFrame
}

// New makes one account's call context.
func (l *Lib) New() (Media, error) {
	m := &libMedia{lib: l, c: clib.NewCall(), nextC: clib.NewCall(), frameC: clib.NewCall(), buf: make([]byte, 4096)}
	ctx := m.c.Reset().Ptr(unsafe.Pointer(&m.err)).Do(l.fn.newCtx).Ptr()
	if ctx == nil {
		return nil, m.err.err("zc_new")
	}
	m.ctx = ctx
	return m, nil
}

func (m *libMedia) send(op string, fn clib.Func, generation uint64, data []byte, withGen, withData bool) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.ctx == nil {
		return errClosed
	}
	c := m.c.Reset().Ptr(m.ctx)
	if withGen {
		c.Uint(generation)
	}
	if withData {
		if len(data) == 0 {
			data = []byte("{}")
		}
		c.Ptr(unsafe.Pointer(&data[0])).Uint(uint64(len(data)))
	}
	if st := c.Ptr(unsafe.Pointer(&m.err)).Do(fn).Int32(); st != zcOK {
		return m.err.err(op)
	}
	return nil
}

func (m *libMedia) Open(p OpenParams) error {
	data, err := json.Marshal(p)
	if err != nil {
		return err
	}
	return m.send("zc_open", m.lib.fn.open, 0, data, false, true)
}

func (m *libMedia) Command(generation uint64, cmd MediaCommand) error {
	data, err := json.Marshal(cmd)
	if err != nil {
		return err
	}
	return m.send("zc_command", m.lib.fn.command, generation, data, true, true)
}

func (m *libMedia) Close(generation uint64) error {
	return m.send("zc_close", m.lib.fn.close, generation, nil, true, false)
}

func (m *libMedia) Next(timeout time.Duration) (MediaEvent, bool, error) {
	m.nextMu.Lock()
	defer m.nextMu.Unlock()
	if m.ctx == nil {
		return MediaEvent{}, false, errClosed
	}
	ms := uint32(min(max(timeout.Milliseconds(), 0), 1<<31))
	for {
		n := m.nextC.Reset().Ptr(m.ctx).Uint32(ms).Ptr(unsafe.Pointer(&m.buf[0])).Uint(uint64(len(m.buf))).
			Ptr(unsafe.Pointer(&m.nextErr)).Do(m.lib.fn.next).Int()
		switch {
		case n == 0:
			return MediaEvent{}, false, nil
		case n == zcSmall:
			m.buf = make([]byte, 2*len(m.buf))
			continue
		case n < 0:
			return MediaEvent{}, false, m.nextErr.err("zc_next_event")
		}
		var ev MediaEvent
		if err := json.Unmarshal(m.buf[:n], &ev); err != nil {
			return MediaEvent{}, false, fmt.Errorf("zapfast calls: an event: %w", err)
		}
		return ev, true, nil
	}
}

func (m *libMedia) Devices() (abi.DeviceList, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.ctx == nil {
		return abi.DeviceList{}, errClosed
	}
	buf := make([]byte, 16<<10)
	for {
		n := m.c.Reset().Ptr(m.ctx).Ptr(unsafe.Pointer(&buf[0])).Uint(uint64(len(buf))).
			Ptr(unsafe.Pointer(&m.err)).Do(m.lib.fn.devices).Int()
		switch {
		case n == zcSmall:
			buf = make([]byte, 2*len(buf))
			continue
		case n < 0:
			return abi.DeviceList{}, m.err.err("zc_devices")
		}
		return decodeDevices(buf[:n])
	}
}

// Frame takes the newest frame of stream (core.Frames), held until
// ReleaseFrame.
func (m *libMedia) Frame(stream uint32) (core.Frame, bool) {
	m.frameMu.Lock()
	defer m.frameMu.Unlock()
	if m.ctx == nil {
		return core.Frame{}, false
	}
	f := &m.frame
	*f = zcFrame{}
	st := m.frameC.Reset().Ptr(m.ctx).Uint32(stream).Ptr(unsafe.Pointer(f)).Ptr(unsafe.Pointer(&m.frameErr)).
		Do(m.lib.fn.frameAcquire).Int32()
	if st != zcOK || f.px == 0 {
		return core.Frame{}, false
	}
	n := int(f.stride[0]) * int(f.h)
	n += (int(f.stride[1]) + int(f.stride[2])) * int((f.h+1)/2)
	px := *(*unsafe.Pointer)(unsafe.Pointer(&f.px)) // C memory, as core.Core.Frame reads it
	return core.Frame{
		Px: unsafe.Slice((*byte)(px), n),
		W:  int(f.w), H: int(f.h),
		Stride: [3]int{int(f.stride[0]), int(f.stride[1]), int(f.stride[2])},
		Format: f.fmt,
		PTS:    f.pts,
	}, true
}

func (m *libMedia) ReleaseFrame(stream uint32) {
	m.frameMu.Lock()
	defer m.frameMu.Unlock()
	if m.ctx != nil {
		m.frameC.Reset().Ptr(m.ctx).Uint32(stream).Do(m.lib.fn.frameRelease)
	}
}

func (m *libMedia) Free() {
	m.mu.Lock()
	m.nextMu.Lock()
	m.frameMu.Lock()
	defer m.mu.Unlock()
	defer m.nextMu.Unlock()
	defer m.frameMu.Unlock()
	if m.ctx != nil {
		m.c.Reset().Ptr(m.ctx).Do(m.lib.fn.free)
		m.ctx = nil
	}
}
