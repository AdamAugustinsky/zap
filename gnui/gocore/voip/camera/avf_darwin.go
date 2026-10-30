//go:build darwin

package camera

import (
	"fmt"
	"runtime"
	"sync"
	"time"
	"unsafe"

	"github.com/ebitengine/purego"
	"github.com/ebitengine/purego/objc"

	"zapfast/gocore/voip/video"
)

// AVFoundation through purego's Objective-C runtime: an AVCaptureSession
// with the device's input and an AVCaptureVideoDataOutput that scales to
// the frames' size and hands over NV12 ('420v') on a serial dispatch
// queue, to a delegate class registered once. The delegate finds its
// camera by its own id, converts the picture to I420 and queues it; no Go
// pointer is handed to the system.

const (
	pathAVF  = "/System/Library/Frameworks/AVFoundation.framework/AVFoundation"
	pathCM   = "/System/Library/Frameworks/CoreMedia.framework/CoreMedia"
	pathCV   = "/System/Library/Frameworks/CoreVideo.framework/CoreVideo"
	pathCF   = "/System/Library/Frameworks/CoreFoundation.framework/CoreFoundation"
	pathObjC = "/usr/lib/libobjc.A.dylib"
	pathSys  = "/usr/lib/libSystem.B.dylib"
)

// cmTime is CMTime.
type cmTime struct {
	Value     int64
	Timescale int32
	Flags     uint32
	Epoch     int64
}

// cmDims is CMVideoDimensions.
type cmDims struct{ W, H int32 }

var pix420v = uint32('4')<<24 | uint32('2')<<16 | uint32('0')<<8 | uint32('v')
var pix420f = uint32('4')<<24 | uint32('2')<<16 | uint32('0')<<8 | uint32('f')

var av struct {
	once sync.Once
	err  error

	sendTime  func(id objc.ID, sel objc.SEL, t cmTime)
	poolPush  func() uintptr
	poolPop   func(uintptr)
	video     objc.ID   // AVMediaTypeVideo
	types     []objc.ID // the device types listed
	clsDevice objc.ID
	clsDisc   objc.ID
	clsInput  objc.ID
	clsSess   objc.ID
	clsOutput objc.ID
	clsString objc.ID
	clsArray  objc.ID
	delegate  objc.Class

	sbImage   func(sb uintptr) uintptr
	dims      func(desc uintptr) cmDims
	pbLock    func(pb uintptr, flags uint64) int32
	pbUnlock  func(pb uintptr, flags uint64) int32
	planeBase func(pb uintptr, plane int) uintptr
	planeBPR  func(pb uintptr, plane int) int
	planeH    func(pb uintptr, plane int) int
	pbWidth   func(pb uintptr) int
	pbHeight  func(pb uintptr) int
	pbFormat  func(pb uintptr) uint32
	keyPixFmt uintptr
	keyWidth  uintptr
	keyHeight uintptr

	release      func(p uintptr)
	dictCreate   func(alloc uintptr, capacity int, keyCB, valueCB uintptr) uintptr
	dictSet      func(d, key, value uintptr)
	numberCreate func(alloc uintptr, typ int, value unsafe.Pointer) uintptr
	keyCB        uintptr
	valueCB      uintptr

	queueCreate func(label unsafe.Pointer, attr uintptr) uintptr
	queueSync   func(q, ctx, fn uintptr)
	queueFree   func(q uintptr)
	noop        uintptr
}

// sel are the selectors, registered once.
var sel struct {
	alloc, init, release, retain, count, objectAt, utf8, withUTF8, arrayWith       objc.SEL
	discover, devices, uniqueID, name, withID, authStatus, formats, formatDesc     objc.SEL
	rateRanges, maxRate, minRate, minDuration, lock, unlock, setFormat, setMinDur  objc.SEL
	setMaxDur, inputWith, canAddInput, addInput, canAddOutput, addOutput, settings objc.SEL
	discardLate, setDelegate, begin, commit, startRunning, stopRunning, running    objc.SEL
	connected, domain, code, desc                                                  objc.SEL
}

// queueLabel is the capture queue's label; global, so it never moves or
// goes.
var queueLabel = []byte("ai.a3lab.zap.camera\x00")

func load() error {
	av.once.Do(func() { av.err = loadAVF() })
	return av.err
}

type binder struct {
	lib uintptr
	err error
}

func open(path string) *binder {
	lib, err := purego.Dlopen(path, purego.RTLD_LAZY|purego.RTLD_GLOBAL)
	return &binder{lib: lib, err: err}
}

func (b *binder) sym(name string) uintptr {
	if b.err != nil {
		return 0
	}
	s, err := purego.Dlsym(b.lib, name)
	if err != nil {
		b.err = fmt.Errorf("%s: %w", name, err)
		return 0
	}
	return s
}

func (b *binder) fn(dst any, name string) {
	if s := b.sym(name); s != 0 {
		purego.RegisterFunc(dst, s)
	}
}

func (b *binder) val(name string) uintptr {
	if s := b.sym(name); s != 0 {
		return deref(s)
	}
	return 0
}

// optional is a constant newer systems have: 0 elsewhere.
func (b *binder) optional(name string) uintptr {
	if b.err != nil {
		return 0
	}
	s, err := purego.Dlsym(b.lib, name)
	if err != nil {
		return 0
	}
	return deref(s)
}

func deref(a uintptr) uintptr { return **(**uintptr)(unsafe.Pointer(&a)) }

func cBytes(p uintptr, n int) []byte {
	if p == 0 || n <= 0 {
		return nil
	}
	return unsafe.Slice(*(**byte)(unsafe.Pointer(&p)), n)
}

func loadAVF() error {
	o := open(pathObjC)
	o.fn(&av.sendTime, "objc_msgSend")
	o.fn(&av.poolPush, "objc_autoreleasePoolPush")
	o.fn(&av.poolPop, "objc_autoreleasePoolPop")
	if o.err != nil {
		return &unavailableError{"the Objective-C runtime", o.err}
	}
	a := open(pathAVF)
	av.video = objc.ID(a.val("AVMediaTypeVideo"))
	for _, name := range []string{
		"AVCaptureDeviceTypeBuiltInWideAngleCamera",
		"AVCaptureDeviceTypeExternal",         // macOS 14
		"AVCaptureDeviceTypeContinuityCamera", // macOS 14
	} {
		if t := a.optional(name); t != 0 {
			av.types = append(av.types, objc.ID(t))
		}
	}
	if len(av.types) < 2 {
		// Before macOS 14 external cameras had their own type.
		if t := a.optional("AVCaptureDeviceTypeExternalUnknown"); t != 0 {
			av.types = append(av.types, objc.ID(t))
		}
	}
	if a.err != nil {
		return &unavailableError{"AVFoundation", a.err}
	}
	cm := open(pathCM)
	cm.fn(&av.sbImage, "CMSampleBufferGetImageBuffer")
	cm.fn(&av.dims, "CMVideoFormatDescriptionGetDimensions")
	cv := open(pathCV)
	cv.fn(&av.pbLock, "CVPixelBufferLockBaseAddress")
	cv.fn(&av.pbUnlock, "CVPixelBufferUnlockBaseAddress")
	cv.fn(&av.planeBase, "CVPixelBufferGetBaseAddressOfPlane")
	cv.fn(&av.planeBPR, "CVPixelBufferGetBytesPerRowOfPlane")
	cv.fn(&av.planeH, "CVPixelBufferGetHeightOfPlane")
	cv.fn(&av.pbWidth, "CVPixelBufferGetWidth")
	cv.fn(&av.pbHeight, "CVPixelBufferGetHeight")
	cv.fn(&av.pbFormat, "CVPixelBufferGetPixelFormatType")
	av.keyPixFmt = cv.val("kCVPixelBufferPixelFormatTypeKey")
	av.keyWidth = cv.val("kCVPixelBufferWidthKey")
	av.keyHeight = cv.val("kCVPixelBufferHeightKey")
	cf := open(pathCF)
	cf.fn(&av.release, "CFRelease")
	cf.fn(&av.dictCreate, "CFDictionaryCreateMutable")
	cf.fn(&av.dictSet, "CFDictionarySetValue")
	cf.fn(&av.numberCreate, "CFNumberCreate")
	av.keyCB = cf.sym("kCFTypeDictionaryKeyCallBacks")
	av.valueCB = cf.sym("kCFTypeDictionaryValueCallBacks")
	sys := open(pathSys)
	sys.fn(&av.queueCreate, "dispatch_queue_create")
	sys.fn(&av.queueSync, "dispatch_sync_f")
	sys.fn(&av.queueFree, "dispatch_release")
	for _, b := range []*binder{cm, cv, cf, sys} {
		if b.err != nil {
			return &unavailableError{"camera capture", b.err}
		}
	}
	av.noop = purego.NewCallback(func(uintptr) {})

	for _, s := range []struct {
		p    *objc.SEL
		name string
	}{
		{&sel.alloc, "alloc"}, {&sel.init, "init"}, {&sel.release, "release"}, {&sel.retain, "retain"},
		{&sel.count, "count"}, {&sel.objectAt, "objectAtIndex:"}, {&sel.utf8, "UTF8String"},
		{&sel.withUTF8, "stringWithUTF8String:"}, {&sel.arrayWith, "arrayWithObjects:count:"},
		{&sel.discover, "discoverySessionWithDeviceTypes:mediaType:position:"}, {&sel.devices, "devices"},
		{&sel.uniqueID, "uniqueID"}, {&sel.name, "localizedName"}, {&sel.withID, "deviceWithUniqueID:"},
		{&sel.authStatus, "authorizationStatusForMediaType:"}, {&sel.formats, "formats"},
		{&sel.formatDesc, "formatDescription"}, {&sel.rateRanges, "videoSupportedFrameRateRanges"},
		{&sel.maxRate, "maxFrameRate"}, {&sel.minRate, "minFrameRate"}, {&sel.minDuration, "minFrameDuration"},
		{&sel.lock, "lockForConfiguration:"}, {&sel.unlock, "unlockForConfiguration"},
		{&sel.setFormat, "setActiveFormat:"}, {&sel.setMinDur, "setActiveVideoMinFrameDuration:"},
		{&sel.setMaxDur, "setActiveVideoMaxFrameDuration:"}, {&sel.inputWith, "deviceInputWithDevice:error:"},
		{&sel.canAddInput, "canAddInput:"}, {&sel.addInput, "addInput:"}, {&sel.canAddOutput, "canAddOutput:"},
		{&sel.addOutput, "addOutput:"}, {&sel.settings, "setVideoSettings:"},
		{&sel.discardLate, "setAlwaysDiscardsLateVideoFrames:"}, {&sel.setDelegate, "setSampleBufferDelegate:queue:"},
		{&sel.begin, "beginConfiguration"}, {&sel.commit, "commitConfiguration"},
		{&sel.startRunning, "startRunning"}, {&sel.stopRunning, "stopRunning"}, {&sel.running, "isRunning"},
		{&sel.connected, "isConnected"}, {&sel.domain, "domain"}, {&sel.code, "code"},
		{&sel.desc, "localizedDescription"},
	} {
		*s.p = objc.RegisterName(s.name)
	}
	for _, c := range []struct {
		p    *objc.ID
		name string
	}{
		{&av.clsDevice, "AVCaptureDevice"}, {&av.clsDisc, "AVCaptureDeviceDiscoverySession"},
		{&av.clsInput, "AVCaptureDeviceInput"}, {&av.clsSess, "AVCaptureSession"},
		{&av.clsOutput, "AVCaptureVideoDataOutput"}, {&av.clsString, "NSString"}, {&av.clsArray, "NSArray"},
	} {
		*c.p = objc.ID(objc.GetClass(c.name))
		if *c.p == 0 {
			return &unavailableError{"AVFoundation", fmt.Errorf("no class %s", c.name)}
		}
	}
	var protocols []*objc.Protocol
	if p := objc.GetProtocol("AVCaptureVideoDataOutputSampleBufferDelegate"); p != nil {
		protocols = append(protocols, p)
	}
	cls, err := objc.RegisterClass("ZapCameraDelegate", objc.GetClass("NSObject"), protocols, nil, []objc.MethodDef{{
		Cmd: objc.RegisterName("captureOutput:didOutputSampleBuffer:fromConnection:"),
		Fn:  didOutput,
	}})
	if err != nil {
		return &unavailableError{"camera capture", err}
	}
	av.delegate = cls
	return nil
}

type unavailableError struct {
	what string
	err  error
}

func (e *unavailableError) Error() string {
	return "camera: " + e.what + " is not available: " + e.err.Error()
}

func (e *unavailableError) Unwrap() []error { return []error{ErrUnavailable, e.err} }

// pooled runs f in an autorelease pool on one thread, so what the system
// autoreleases for it is let go when it returns.
func pooled(f func()) {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	p := av.poolPush()
	defer av.poolPop(p)
	f()
}

// goString is an NSString's contents.
func goString(s objc.ID) string {
	if s == 0 {
		return ""
	}
	p := uintptr(s.Send(sel.utf8))
	if p == 0 {
		return ""
	}
	n := 0
	for cBytes(p+uintptr(n), 1)[0] != 0 {
		n++
	}
	return string(cBytes(p, n))
}

// nsString is an autoreleased NSString of s.
func nsString(s string) objc.ID {
	b := append([]byte(s), 0)
	return av.clsString.Send(sel.withUTF8, unsafe.Pointer(&b[0]))
}

func nsError(op string, e objc.ID) error {
	if e == 0 {
		return &SystemError{Op: op}
	}
	return &SystemError{
		Op:     op,
		Domain: goString(e.Send(sel.domain)),
		Code:   int(e.Send(sel.code)),
		Desc:   goString(e.Send(sel.desc)),
	}
}

func each(arr objc.ID, f func(objc.ID)) {
	if arr == 0 {
		return
	}
	n := int(arr.Send(sel.count))
	for i := range n {
		f(arr.Send(sel.objectAt, uintptr(i)))
	}
}

// deviceList is the cameras, as AVCaptureDevices (autoreleased).
func deviceList() []objc.ID {
	types := av.clsArray.Send(sel.arrayWith, unsafe.Pointer(&av.types[0]), uintptr(len(av.types)))
	disc := av.clsDisc.Send(sel.discover, types, av.video, uintptr(0))
	var out []objc.ID
	each(disc.Send(sel.devices), func(d objc.ID) { out = append(out, d) })
	return out
}

func sysDevices() ([]Device, error) {
	if err := load(); err != nil {
		return nil, err
	}
	var out []Device
	pooled(func() {
		seen := map[string]bool{}
		for _, d := range deviceList() {
			id := goString(d.Send(sel.uniqueID))
			if id == "" || seen[id] {
				continue
			}
			seen[id] = true
			name := goString(d.Send(sel.name))
			if name == "" {
				name = "Camera"
			}
			out = append(out, Device{ID: id, Label: name})
		}
	})
	return out, nil
}

// AVAuthorizationStatus.
const (
	authRestricted = 1
	authDenied     = 2
)

// cameras are the open cameras whose delegate may run, by the delegate.
var cameras sync.Map // objc.ID → *sysCamera

type sysCamera struct {
	device, input, session, output, delegate objc.ID
	queue                                    uintptr
	w, h                                     int
	q                                        *queue
}

func openSys(cfg Config, q *queue) (*sysCamera, opened, error) {
	if err := load(); err != nil {
		return nil, opened{}, err
	}
	var (
		s    *sysCamera
		info opened
		err  error
	)
	pooled(func() { s, info, err = start(cfg, q) })
	return s, info, err
}

func start(cfg Config, q *queue) (*sysCamera, opened, error) {
	switch int(av.clsDevice.Send(sel.authStatus, av.video)) {
	case authRestricted, authDenied:
		return nil, opened{}, ErrPermission
	}
	var dev objc.ID
	if cfg.Device != "" {
		dev = av.clsDevice.Send(sel.withID, nsString(cfg.Device))
	} else if list := deviceList(); len(list) > 0 {
		dev = list[0]
	}
	if dev == 0 {
		return nil, opened{}, ErrNoCamera
	}
	// The modes: each format's size and best rate.
	var formats []objc.ID
	var modes []Mode
	each(dev.Send(sel.formats), func(f objc.ID) {
		d := av.dims(uintptr(f.Send(sel.formatDesc)))
		best := 0.0
		each(f.Send(sel.rateRanges), func(r objc.ID) { best = max(best, objc.Send[float64](r, sel.maxRate)) })
		formats = append(formats, f)
		modes = append(modes, Mode{W: int(d.W), H: int(d.H), FPS: best})
	})
	m, ok := ChooseMode(modes, cfg.Long, cfg.Short, cfg.FPS)
	if !ok {
		return nil, opened{}, ErrNoMode
	}
	format := formats[indexOf(modes, m)]
	w, h := video.FitEven(m.W, m.H, cfg.Long, cfg.Short)
	dur, fps := frameDuration(format, cfg.FPS)

	var nsErr objc.ID
	input := av.clsInput.Send(sel.inputWith, dev, unsafe.Pointer(&nsErr))
	if input == 0 {
		return nil, opened{}, nsError("AVCaptureDeviceInput", nsErr)
	}
	s := &sysCamera{device: dev.Send(sel.retain), input: input.Send(sel.retain), w: w, h: h, q: q}
	s.session = av.clsSess.Send(sel.alloc).Send(sel.init)
	s.output = av.clsOutput.Send(sel.alloc).Send(sel.init)
	s.delegate = objc.ID(av.delegate).Send(sel.alloc).Send(sel.init)
	s.queue = av.queueCreate(unsafe.Pointer(&queueLabel[0]), 0)

	settings := av.dictCreate(0, 3, av.keyCB, av.valueCB)
	setNumber(settings, av.keyPixFmt, int32(pix420v))
	setNumber(settings, av.keyWidth, int32(w))
	setNumber(settings, av.keyHeight, int32(h))
	s.output.Send(sel.settings, settings)
	av.release(settings)
	s.output.Send(sel.discardLate, true)
	cameras.Store(s.delegate, s)
	s.output.Send(sel.setDelegate, s.delegate, s.queue)

	s.session.Send(sel.begin)
	if !objc.Send[bool](s.session, sel.canAddInput, s.input) || !objc.Send[bool](s.session, sel.canAddOutput, s.output) {
		s.session.Send(sel.commit)
		s.close()
		return nil, opened{}, &SystemError{Op: "AVCaptureSession addInput/addOutput"}
	}
	s.session.Send(sel.addInput, s.input)
	s.session.Send(sel.addOutput, s.output)
	// The format is set with the input on the session and kept locked
	// until it runs, or the session's preset would replace it.
	nsErr = 0
	locked := objc.Send[bool](dev, sel.lock, unsafe.Pointer(&nsErr))
	if locked {
		dev.Send(sel.setFormat, format)
		if dur.Timescale != 0 {
			av.sendTime(dev, sel.setMinDur, dur)
			av.sendTime(dev, sel.setMaxDur, dur)
		}
	}
	s.session.Send(sel.commit)
	s.session.Send(sel.startRunning)
	if locked {
		dev.Send(sel.unlock)
	}
	if !objc.Send[bool](s.session, sel.running) {
		s.close()
		return nil, opened{}, &SystemError{Op: "AVCaptureSession startRunning"}
	}
	if !locked {
		fps = m.FPS
	}
	return s, opened{id: goString(dev.Send(sel.uniqueID)), w: w, h: h, fps: fps}, nil
}

func indexOf(modes []Mode, m Mode) int {
	for i, x := range modes {
		if x == m {
			return i
		}
	}
	return 0
}

// frameDuration is the frame duration to ask of format for fps: 1/fps
// when one of its ranges holds it, else the fastest the nearest range
// below has (the range's own duration, so it is accepted exactly).
func frameDuration(format objc.ID, fps int) (cmTime, float64) {
	want := float64(fps)
	var (
		best     cmTime
		bestRate float64
	)
	each(format.Send(sel.rateRanges), func(r objc.ID) {
		lo, hi := objc.Send[float64](r, sel.minRate), objc.Send[float64](r, sel.maxRate)
		if lo <= want-0.01 && want+0.01 <= hi {
			if bestRate != want {
				best, bestRate = cmTime{Value: 1, Timescale: int32(fps), Flags: 1}, want
			}
			return
		}
		if bestRate == want {
			return
		}
		// The range nearest to the rate wanted: the fastest below it, or
		// the slowest above it when none is below.
		better := bestRate == 0 ||
			(hi <= want && (bestRate > want || hi > bestRate)) ||
			(hi > want && bestRate > want && hi < bestRate)
		if better {
			best, bestRate = objc.Send[cmTime](r, sel.minDuration), hi
		}
	})
	return best, bestRate
}

func setNumber(d, key uintptr, v int32) {
	n := av.numberCreate(0, 3, unsafe.Pointer(&v)) // kCFNumberSInt32Type
	av.dictSet(d, key, n)
	av.release(n)
}

func (s *sysCamera) alive() bool {
	return objc.Send[bool](s.device, sel.connected) && objc.Send[bool](s.session, sel.running)
}

func (s *sysCamera) close() {
	pooled(func() {
		s.session.Send(sel.stopRunning)
		s.output.Send(sel.setDelegate, uintptr(0), uintptr(0))
		// Whatever the queue is running finishes before anything goes.
		av.queueSync(s.queue, 0, av.noop)
		cameras.Delete(s.delegate)
		for _, o := range []objc.ID{s.session, s.output, s.input, s.device, s.delegate} {
			o.Send(sel.release)
		}
		av.queueFree(s.queue)
	})
}

// didOutput is the delegate's captureOutput:didOutputSampleBuffer:
// fromConnection:, on the capture queue.
func didOutput(self objc.ID, _ objc.SEL, _ objc.ID, sb objc.ID, _ objc.ID) {
	at := time.Now()
	v, ok := cameras.Load(self)
	if !ok {
		return
	}
	s := v.(*sysCamera)
	pb := av.sbImage(uintptr(sb))
	if pb == 0 {
		return
	}
	pic := readNV12(pb)
	if pic == nil {
		return
	}
	if pic.W != s.w || pic.H != s.h {
		pic = pic.Scale(s.w, s.h)
	}
	s.q.put(Frame{Pic: pic, At: at})
}

// readNV12 copies a biplanar 4:2:0 pixel buffer into a new I420; nil for
// any other format.
func readNV12(pb uintptr) *video.I420 {
	if f := av.pbFormat(pb); f != pix420v && f != pix420f {
		return nil
	}
	if av.pbLock(pb, 1) != 0 {
		return nil
	}
	defer av.pbUnlock(pb, 1)
	w, h := av.pbWidth(pb), av.pbHeight(pb)
	if w < 2 || h < 2 {
		return nil
	}
	ys, uvs := av.planeBPR(pb, 0), av.planeBPR(pb, 1)
	y := cBytes(av.planeBase(pb, 0), ys*av.planeH(pb, 0))
	uv := cBytes(av.planeBase(pb, 1), uvs*av.planeH(pb, 1))
	p := video.NewI420(w, h)
	if len(y) < ys*h || len(uv) < uvs*p.ChromaH() {
		return nil
	}
	video.FromNV12(p, y, ys, uv, uvs)
	return p
}
