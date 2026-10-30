//go:build darwin

package av

import (
	"fmt"
	"image"
	"runtime"
	"sync"
	"time"
	"unsafe"

	"github.com/ebitengine/purego"
	"github.com/ebitengine/purego/objc"
)

// The poster frame through AVFoundation: an AVAssetImageGenerator on the
// file, turned upright by its track's transform, scaled within the side
// asked for and drawn into an sRGB bitmap. The Rust core decoded H.264 in
// process with openh264 (zf:src/mp4_frames.rs:99); the system's decoder
// also reads HEVC. AppKit is opened before AVFoundation, as gonativeui's
// media does (its D58).

const (
	pathAppKit = "/System/Library/Frameworks/AppKit.framework/AppKit"
	pathAVF    = "/System/Library/Frameworks/AVFoundation.framework/AVFoundation"
	pathCG     = "/System/Library/Frameworks/CoreGraphics.framework/CoreGraphics"
	pathObjC   = "/usr/lib/libobjc.A.dylib"
)

// cmTime is CMTime.
type cmTime struct {
	Value     int64
	Timescale int32
	Flags     uint32
	Epoch     int64
}

type cgSize struct{ W, H float64 }

type cgRect struct{ X, Y, W, H float64 }

var vt struct {
	once sync.Once
	err  error

	poolPush func() uintptr
	poolPop  func(uintptr)
	sendStr  func(id objc.ID, sel objc.SEL, s *byte) objc.ID
	sendSize func(id objc.ID, sel objc.SEL, s cgSize)
	sendTime func(id objc.ID, sel objc.SEL, t cmTime)
	copyAt   func(id objc.ID, sel objc.SEL, t cmTime, actual, err uintptr) uintptr

	imgW, imgH   func(img uintptr) uintptr
	imgRelease   func(img uintptr)
	spaceCreate  func(name uintptr) uintptr
	spaceRelease func(space uintptr)
	bitmap       func(data unsafe.Pointer, w, h, bits, row, space uintptr, info uint32) uintptr
	draw         func(ctx uintptr, r cgRect, img uintptr)
	ctxRelease   func(ctx uintptr)
	srgb         uintptr

	clsString, clsURL, clsAsset, clsGen                                                 objc.ID
	withUTF8, fileURL, assetWith, genWith, transform, maxSize, before, after, copyImage objc.SEL
}

func loadPoster() error {
	vt.once.Do(func() {
		lib := func(path string) uintptr {
			h, err := purego.Dlopen(path, purego.RTLD_LAZY|purego.RTLD_GLOBAL)
			if err != nil && vt.err == nil {
				vt.err = err
			}
			return h
		}
		sym := func(h uintptr, name string) uintptr {
			if vt.err != nil {
				return 0
			}
			p, err := purego.Dlsym(h, name)
			if err != nil {
				vt.err = fmt.Errorf("%s: %w", name, err)
			}
			return p
		}
		fn := func(dst any, h uintptr, name string) {
			if p := sym(h, name); p != 0 {
				purego.RegisterFunc(dst, p)
			}
		}
		lib(pathAppKit)
		lib(pathAVF)
		o, cg := lib(pathObjC), lib(pathCG)
		fn(&vt.poolPush, o, "objc_autoreleasePoolPush")
		fn(&vt.poolPop, o, "objc_autoreleasePoolPop")
		for _, dst := range []any{&vt.sendStr, &vt.sendSize, &vt.sendTime, &vt.copyAt} {
			fn(dst, o, "objc_msgSend")
		}
		fn(&vt.imgW, cg, "CGImageGetWidth")
		fn(&vt.imgH, cg, "CGImageGetHeight")
		fn(&vt.imgRelease, cg, "CGImageRelease")
		fn(&vt.spaceCreate, cg, "CGColorSpaceCreateWithName")
		fn(&vt.spaceRelease, cg, "CGColorSpaceRelease")
		fn(&vt.bitmap, cg, "CGBitmapContextCreate")
		fn(&vt.draw, cg, "CGContextDrawImage")
		fn(&vt.ctxRelease, cg, "CGContextRelease")
		if p := sym(cg, "kCGColorSpaceSRGB"); p != 0 {
			vt.srgb = **(**uintptr)(unsafe.Pointer(&p))
		}
		if vt.err != nil {
			return
		}
		vt.clsString, vt.clsURL = objc.ID(objc.GetClass("NSString")), objc.ID(objc.GetClass("NSURL"))
		vt.clsAsset, vt.clsGen = objc.ID(objc.GetClass("AVURLAsset")), objc.ID(objc.GetClass("AVAssetImageGenerator"))
		if vt.clsAsset == 0 || vt.clsGen == 0 {
			vt.err = fmt.Errorf("AVFoundation's asset classes are missing")
			return
		}
		vt.withUTF8, vt.fileURL = objc.RegisterName("stringWithUTF8String:"), objc.RegisterName("fileURLWithPath:")
		vt.assetWith, vt.genWith = objc.RegisterName("URLAssetWithURL:options:"), objc.RegisterName("assetImageGeneratorWithAsset:")
		vt.transform, vt.maxSize = objc.RegisterName("setAppliesPreferredTrackTransform:"), objc.RegisterName("setMaximumSize:")
		vt.before, vt.after = objc.RegisterName("setRequestedTimeToleranceBefore:"), objc.RegisterName("setRequestedTimeToleranceAfter:")
		vt.copyImage = objc.RegisterName("copyCGImageAtTime:actualTime:error:")
	})
	if vt.err != nil {
		return &UnavailableError{What: "AVFoundation", Err: vt.err}
	}
	return nil
}

// VideoPoster is the frame of the video file at path showing at at, turned
// upright and scaled down (never up) to at most maxSide pixels a side:
// opaque RGBA. It blocks while the frame decodes; call it off the UI thread.
// An error matches ErrUnavailable without AVFoundation, ErrVideo when the
// file has no frame there.
func VideoPoster(path string, at time.Duration, maxSide int) (*image.RGBA, error) {
	if err := loadPoster(); err != nil {
		return nil, err
	}
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	pool := vt.poolPush()
	defer vt.poolPop(pool)
	cpath := append([]byte(path), 0)
	str := vt.sendStr(vt.clsString, vt.withUTF8, &cpath[0])
	url := str
	if str != 0 {
		url = vt.clsURL.Send(vt.fileURL, str)
	}
	var asset, gen objc.ID
	if url != 0 {
		asset = vt.clsAsset.Send(vt.assetWith, url, 0)
	}
	if asset != 0 {
		gen = vt.clsGen.Send(vt.genWith, asset)
	}
	if gen == 0 {
		return nil, fmt.Errorf("%w: AVFoundation cannot open it", ErrVideo)
	}
	gen.Send(vt.transform, true)
	vt.sendSize(gen, vt.maxSize, cgSize{float64(maxSide), float64(maxSide)})
	// The frame showing then, not the nearest keyframe.
	exact := cmTime{Timescale: 1, Flags: 1}
	vt.sendTime(gen, vt.before, exact)
	vt.sendTime(gen, vt.after, exact)
	img := vt.copyAt(gen, vt.copyImage, cmTime{Value: at.Microseconds(), Timescale: 1_000_000, Flags: 1}, 0, 0)
	if img == 0 {
		return nil, fmt.Errorf("%w: no frame at %v", ErrVideo, at)
	}
	defer vt.imgRelease(img)
	w, h := int(vt.imgW(img)), int(vt.imgH(img))
	if w <= 0 || h <= 0 || w > maxSide || h > maxSide {
		return nil, fmt.Errorf("%w: a %d×%d frame", ErrVideo, w, h)
	}
	m := image.NewRGBA(image.Rect(0, 0, w, h))
	space := vt.spaceCreate(vt.srgb)
	defer vt.spaceRelease(space)
	// kCGImageAlphaPremultipliedLast, kCGBitmapByteOrder32Big: R, G, B, A.
	ctx := vt.bitmap(unsafe.Pointer(&m.Pix[0]), uintptr(w), uintptr(h), 8, uintptr(m.Stride), space, 1|4<<12)
	if ctx == 0 {
		return nil, fmt.Errorf("%w: no bitmap for a %d×%d frame", ErrVideo, w, h)
	}
	vt.draw(ctx, cgRect{W: float64(w), H: float64(h)}, img)
	vt.ctxRelease(ctx)
	runtime.KeepAlive(m.Pix)
	return m, nil
}
