package core

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"

	"gonativeui/clib"
	gcore "gonativeui/core"

	"zapfast/abi"
)

// LibName is the core library's file name, in the bundle's Frameworks.
const LibName = "libzapfast_core.dylib"

// EnvLibrary names an environment variable that, when set, is the path of
// the core library to load. Unbundled development runs and tests only: a
// bundled app ignores it.
const EnvLibrary = "ZAPFAST_CORE"

// cargoProfiles are the target/ subdirectories a development build is
// looked for in.
var cargoProfiles = [...]string{"core-release", "debug", "release"}

// The loading policy. A bundled app (its executable in X.app/Contents/MacOS)
// loads only its own Contents/Frameworks/libzapfast_core.dylib, and fails
// closed without it: no $ZAPFAST_CORE, no cargo build, no explicit path.
// The library runs inside the app with the app's Keychain identity, so
// nothing outside the signed bundle may choose it. An unbundled run
// (go run, go test) is development: it takes an explicit path, then
// $ZAPFAST_CORE, then the newest cargo build above the working directory or
// the executable.
//
// The policy keys on the bundle at run time rather than on a build tag: the
// binary that is tested is the one that ships, and a bundle made for
// development (gnui run) follows the release rule too, so a forgotten tag
// can never ship the development search.

// ResolveLibrary returns the core library to load under the loading policy:
// explicit (Config.Library) when not empty, else the policy's search.
func ResolveLibrary(explicit string) (string, error) {
	exe, _ := executable()
	wd, _ := os.Getwd()
	return resolveLibrary(exe, wd, explicit, os.Getenv)
}

// FindLibrary is ResolveLibrary with no explicit path.
func FindLibrary() (string, error) { return ResolveLibrary("") }

// executable is the running executable, its symlinks resolved.
func executable() (string, error) {
	exe, err := os.Executable()
	if err != nil {
		return "", err
	}
	if r, err := filepath.EvalSymlinks(exe); err == nil {
		exe = r
	}
	return exe, nil
}

// bundleLibrary returns where the bundle keeps the library, and whether exe
// is an app bundle's executable at all.
func bundleLibrary(exe string) (string, bool) {
	macos := filepath.Dir(exe)
	contents := filepath.Dir(macos)
	if exe == "" || filepath.Base(macos) != "MacOS" || filepath.Base(contents) != "Contents" ||
		filepath.Ext(filepath.Dir(contents)) != ".app" {
		return "", false
	}
	return filepath.Join(contents, "Frameworks", LibName), true
}

func resolveLibrary(exe, wd, explicit string, getenv func(string) string) (string, error) {
	if lib, bundled := bundleLibrary(exe); bundled {
		if explicit != "" && filepath.Clean(explicit) != lib {
			return "", fmt.Errorf("zapfast core: a bundled app loads only %s, not %s", lib, explicit)
		}
		if !isFile(lib) {
			return "", fmt.Errorf("zapfast core: %s is missing from the app bundle", lib)
		}
		return lib, nil
	}
	if explicit == "" {
		explicit = getenv(EnvLibrary)
	}
	if explicit != "" {
		abs, err := filepath.Abs(explicit)
		if err != nil {
			return "", fmt.Errorf("zapfast core: %q: %w", explicit, err)
		}
		return abs, nil
	}
	var starts []string
	if wd != "" {
		starts = append(starts, wd)
	}
	if exe != "" {
		starts = append(starts, filepath.Dir(exe))
	}
	for _, dir := range starts {
		if p := cargoBuild(dir); p != "" {
			return p, nil
		}
	}
	return "", fmt.Errorf("zapfast core: %s not found: %s is unset and no cargo build is above %v (cargo build -p zapfast_core)", LibName, EnvLibrary, starts)
}

// allowed applies the bundle half of the policy to a path about to be
// loaded.
func allowed(path string) error {
	exe, _ := executable()
	if lib, bundled := bundleLibrary(exe); bundled && filepath.Clean(path) != lib {
		return fmt.Errorf("zapfast core: a bundled app loads only %s, not %s", lib, path)
	}
	return nil
}

// cargoBuild returns the newest cargo build of the library in dir's target/
// or the nearest parent's.
func cargoBuild(dir string) string {
	for {
		var best string
		var bestT time.Time
		for _, profile := range cargoProfiles {
			p := filepath.Join(dir, "target", profile, LibName)
			if st, err := os.Stat(p); err == nil && st.Mode().IsRegular() && st.ModTime().After(bestT) {
				best, bestT = p, st.ModTime()
			}
		}
		if best != "" {
			return best
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return ""
		}
		dir = parent
	}
}

func isFile(p string) bool {
	st, err := os.Stat(p)
	return err == nil && st.Mode().IsRegular()
}

// Lib is the loaded core library: its functions, looked up once.
type Lib struct {
	// Path is the file it was loaded from.
	Path string
	// LoadTime is how long loading took, PrewarmTime included.
	LoadTime time.Duration
	// PrewarmTime is the part spent before dlopen, outside dyld's lock
	// (prewarm): the kernel's first look at a newly written library.
	PrewarmTime time.Duration

	lib *clib.Lib
	fn  funcs

	genMu   gcore.Mutex
	genCall *clib.Call
}

type funcs struct {
	abiVersion, schemaHash                 clib.Func
	open, start, eventFD, drain, send      clib.Func
	blobRead, blobPut, blobFree, coreClose clib.Func
	frameAcquire, frameRelease             clib.Func
	// callGeneration, voiceDecode and setLog are zero in a library built
	// before them.
	callGeneration, voiceDecode, setLog clib.Func
}

var (
	loadMu sync.Mutex
	loaded = map[string]*Lib{}
)

// Load loads the core library at path (absolute) once per process, looks
// up its functions, and checks that its ZF_ABI and schema hash are the
// generated package abi's. A library that fails the check stays loaded but
// is never used. Load blocks for the dlopen (about 330 ms cold): call it
// off the UI thread, or through OpenAsync.
func Load(path string) (*Lib, error) {
	loadMu.Lock()
	defer loadMu.Unlock()
	if err := allowed(path); err != nil {
		return nil, err
	}
	if l := loaded[path]; l != nil {
		return l, nil
	}
	t0 := time.Now()
	prewarm(path)
	t1 := time.Now()
	cl, err := clib.Open(path)
	if err != nil {
		return nil, fmt.Errorf("zapfast core: %w", err)
	}
	l := &Lib{Path: path, lib: cl, genCall: clib.NewCall()}
	for _, f := range [...]struct {
		dst  *clib.Func
		name string
	}{
		{&l.fn.abiVersion, "zf_abi_version"},
		{&l.fn.schemaHash, "zf_schema_hash"},
		{&l.fn.open, "zf_core_open"},
		{&l.fn.start, "zf_core_start"},
		{&l.fn.eventFD, "zf_event_fd"},
		{&l.fn.drain, "zf_drain"},
		{&l.fn.send, "zf_send"},
		{&l.fn.blobRead, "zf_blob_read"},
		{&l.fn.blobPut, "zf_blob_put"},
		{&l.fn.blobFree, "zf_blob_free"},
		{&l.fn.coreClose, "zf_core_close"},
		{&l.fn.frameAcquire, "zf_frame_acquire"},
		{&l.fn.frameRelease, "zf_frame_release"},
	} {
		if *f.dst, err = cl.Func(f.name); err != nil {
			return nil, fmt.Errorf("zapfast core: %w", err)
		}
	}
	// Optional: CallGeneration falls back without it, and DecodeVoice
	// returns ErrUnsupported.
	l.fn.callGeneration, _ = cl.Func("zf_call_generation")
	l.fn.voiceDecode, _ = cl.Func("zf_voice_decode")
	l.fn.setLog, _ = cl.Func("zf_set_log")
	call := clib.NewCall()
	if v := call.Reset().Do(l.fn.abiVersion).Uint32(); v != abi.ABIVersion {
		return nil, fmt.Errorf("zapfast core: %s has ZF_ABI %d, this app needs %d: %w", path, v, abi.ABIVersion, ErrVersion)
	}
	if h := call.Reset().Do(l.fn.schemaHash).Uint(); h != abi.SchemaHash {
		return nil, fmt.Errorf("zapfast core: %s has schema %#x, this app was generated from %#x (rebuild one of them): %w", path, h, abi.SchemaHash, ErrVersion)
	}
	l.LoadTime, l.PrewarmTime = time.Since(t0), t1.Sub(t0)
	loaded[path] = l
	return l, nil
}

// ErrVersion is wrapped by Load's error when the library's ZF_ABI or schema
// hash is not the app's.
var ErrVersion = errors.New("the core library does not match this app")
