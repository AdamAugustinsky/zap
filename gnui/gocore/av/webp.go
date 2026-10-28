package av

import (
	"errors"
	"fmt"
	"image"
	"image/draw"
	"math"
	"os"
	"path/filepath"
	"sync"
	"time"
	"unsafe"

	"gonativeui/clib"
)

// WebPLibrary is the WebP encoder's file name, in the bundle's Frameworks:
// libwebp and a small shim (packaging/macos/libwebp/).
const WebPLibrary = "libzfwebp.dylib"

// EnvWebPLibrary names an environment variable that, when set, is the path
// of the library to load. Unbundled development runs and tests only: a
// bundled app ignores it.
const EnvWebPLibrary = "ZAPFAST_LIBWEBP"

// webpDevDir is where build.sh puts the library by default, under the
// repository root.
const webpDevDir = "target/libwebp"

// webpShimVersion is the zf_webp_version this binding speaks.
const webpShimVersion = 1

// libwebp is the library, loaded on first use.
var libwebp struct {
	once sync.Once
	err  error

	free, encode, animNew, animAdd, animFinish, animDelete clib.Func
}

// The loading policy is the core library's (gnui/core/lib.go): a bundled app
// (its executable in X.app/Contents/MacOS) loads only its own
// Contents/Frameworks/libzfwebp.dylib; an unbundled run (go run, go test) is
// development: $ZAPFAST_LIBWEBP, then target/libwebp/ above the working
// directory or the executable.

func loadWebP() error {
	libwebp.once.Do(func() {
		exe, _ := os.Executable()
		if r, err := filepath.EvalSymlinks(exe); err == nil {
			exe = r
		}
		wd, _ := os.Getwd()
		path, err := findWebP(exe, wd, os.Getenv)
		if err != nil {
			libwebp.err = err
			return
		}
		lib, err := clib.Open(path)
		if err != nil {
			libwebp.err = &UnavailableError{What: WebPLibrary, Path: path, Err: err}
			return
		}
		version, err := lib.Func("zf_webp_version")
		if err == nil {
			if v := clib.NewCall().Do(version).Int32(); v != webpShimVersion {
				err = fmt.Errorf("its zf_webp_version is %d, not %d: rebuild it", v, webpShimVersion)
			}
		}
		for _, f := range []struct {
			fn   *clib.Func
			name string
		}{
			{&libwebp.free, "zf_webp_free"}, {&libwebp.encode, "zf_webp_encode"},
			{&libwebp.animNew, "zf_webp_anim_new"}, {&libwebp.animAdd, "zf_webp_anim_add"},
			{&libwebp.animFinish, "zf_webp_anim_finish"}, {&libwebp.animDelete, "zf_webp_anim_delete"},
		} {
			if err != nil {
				break
			}
			*f.fn, err = lib.Func(f.name)
		}
		if err != nil {
			lib.Close()
			libwebp.err = &UnavailableError{What: WebPLibrary, Path: path, Err: err}
		}
	})
	return libwebp.err
}

func findWebP(exe, wd string, getenv func(string) string) (string, error) {
	macos := filepath.Dir(exe)
	contents := filepath.Dir(macos)
	if exe != "" && filepath.Base(macos) == "MacOS" && filepath.Base(contents) == "Contents" &&
		filepath.Ext(filepath.Dir(contents)) == ".app" {
		lib := filepath.Join(contents, "Frameworks", WebPLibrary)
		if !isFile(lib) {
			return "", &UnavailableError{What: WebPLibrary, Path: lib}
		}
		return lib, nil
	}
	if p := getenv(EnvWebPLibrary); p != "" {
		abs, err := filepath.Abs(p)
		if err != nil || !isFile(abs) {
			return "", &UnavailableError{What: WebPLibrary, Path: p, Err: err}
		}
		return abs, nil
	}
	var starts []string
	for _, dir := range []string{wd, filepath.Dir(exe)} {
		if dir == "" || dir == "." {
			continue
		}
		starts = append(starts, dir)
		for {
			if p := filepath.Join(dir, webpDevDir, WebPLibrary); isFile(p) {
				return p, nil
			}
			parent := filepath.Dir(dir)
			if parent == dir {
				break
			}
			dir = parent
		}
	}
	return "", &UnavailableError{What: WebPLibrary, Searched: starts}
}

func isFile(p string) bool {
	info, err := os.Stat(p)
	return err == nil && info.Mode().IsRegular()
}

// webpCall is one call into the library and what it passes by pointer, in
// one heap object, as clib asks.
type webpCall struct {
	call *clib.Call
	out  *byte // C memory, freed with zf_webp_free
}

// takeOutput copies n bytes of the library's output and frees it.
func (c *webpCall) takeOutput(n uint64) []byte {
	if c.out == nil {
		return nil
	}
	b := append([]byte(nil), unsafe.Slice(c.out, n)...)
	c.call.Reset().Ptr(unsafe.Pointer(c.out)).Do(libwebp.free)
	c.out = nil
	return b
}

// webpSide is the largest picture libwebp encodes.
const webpSide = 16383

func webpPixels(m image.Image) (*image.NRGBA, error) {
	if m == nil {
		return nil, errors.New("av: no picture")
	}
	b := m.Bounds()
	if b.Dx() < 1 || b.Dy() < 1 || b.Dx() > webpSide || b.Dy() > webpSide {
		return nil, fmt.Errorf("%w: a WebP of %d × %d", ErrTooLarge, b.Dx(), b.Dy())
	}
	n := nrgba(m)
	if n.Stride != 4*n.Rect.Dx() { // a sub-image: the library takes packed rows
		c := image.NewNRGBA(n.Rect)
		draw.Draw(c, c.Rect, n, image.Point{}, draw.Src)
		n = c
	}
	return n, nil
}

func webpQuality(q float32) error {
	if math.IsNaN(float64(q)) || q < 0 || q > 100 {
		return fmt.Errorf("av: a WebP quality of %v, not 0 to 100", q)
	}
	return nil
}

// EncodeWebP is a still lossy WebP of m at quality (0 to 100), its alpha
// kept: libwebp's WebPEncodeRGBA, as upstream encodes stickers. Without the
// library it fails with ErrUnavailable.
func EncodeWebP(m image.Image, quality float32) ([]byte, error) {
	if err := webpQuality(quality); err != nil {
		return nil, err
	}
	px, err := webpPixels(m)
	if err != nil {
		return nil, err
	}
	if err := loadWebP(); err != nil {
		return nil, err
	}
	c := &webpCall{call: clib.NewCall()}
	n := c.call.Reset().Ptr(unsafe.Pointer(&px.Pix[0])).Int32(int32(px.Rect.Dx())).Int32(int32(px.Rect.Dy())).
		Float32(quality).Ptr(unsafe.Pointer(&c.out)).Do(libwebp.encode).Uint()
	out := c.takeOutput(n)
	if n == 0 || len(out) == 0 {
		return nil, errors.New("av: could not encode the WebP")
	}
	return out, nil
}

// WebPFrame is one frame of an animation and how long it shows.
type WebPFrame struct {
	Image    image.Image
	Duration time.Duration
}

// Animation limits: WhatsApp's animated stickers are at most 10 seconds;
// these bound the work a damaged source can ask for.
const (
	maxFrames     = 500
	maxAnimation  = 5 * time.Minute
	minFrameTime  = 10 * time.Millisecond
	frameFallback = 100 * time.Millisecond
)

// EncodeAnimatedWebP is a lossy animated WebP of frames, all the first's
// size, looping forever. A frame shorter than 10 ms shows for 100 ms, as
// browsers do. Without the library it fails with ErrUnavailable.
func EncodeAnimatedWebP(frames []WebPFrame, quality float32) ([]byte, error) {
	if err := webpQuality(quality); err != nil {
		return nil, err
	}
	if len(frames) < 1 || len(frames) > maxFrames {
		return nil, fmt.Errorf("av: an animation of %d frames", len(frames))
	}
	first, err := webpPixels(frames[0].Image)
	if err != nil {
		return nil, err
	}
	if err := loadWebP(); err != nil {
		return nil, err
	}
	w, h := first.Rect.Dx(), first.Rect.Dy()
	c := &webpCall{call: clib.NewCall()}
	enc := c.call.Reset().Int32(int32(w)).Int32(int32(h)).Float32(quality).Do(libwebp.animNew).Uint()
	if enc == 0 {
		return nil, errors.New("av: could not start the WebP animation")
	}
	defer func() { c.call.Reset().Uint(enc).Do(libwebp.animDelete) }()
	var at time.Duration
	for i, f := range frames {
		px := first
		if i > 0 {
			if px, err = webpPixels(f.Image); err != nil {
				return nil, err
			}
		}
		if px.Rect.Dx() != w || px.Rect.Dy() != h {
			return nil, errors.New("av: the animation's frames differ in size")
		}
		if c.call.Reset().Uint(enc).Ptr(unsafe.Pointer(&px.Pix[0])).Int32(int32(at.Milliseconds())).Do(libwebp.animAdd).Int32() == 0 {
			return nil, fmt.Errorf("av: could not encode frame %d of the WebP animation", i)
		}
		d := f.Duration
		if d < minFrameTime {
			d = frameFallback
		}
		if at += d; at > maxAnimation {
			return nil, fmt.Errorf("%w: an animation over %v", ErrTooLarge, maxAnimation)
		}
	}
	n := c.call.Reset().Uint(enc).Int32(int32(at.Milliseconds())).Ptr(unsafe.Pointer(&c.out)).Do(libwebp.animFinish).Uint()
	out := c.takeOutput(n)
	if n == 0 || len(out) == 0 {
		return nil, errors.New("av: could not assemble the WebP animation")
	}
	return out, nil
}
