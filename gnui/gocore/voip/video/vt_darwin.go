//go:build darwin

package video

import (
	"fmt"
	"sync"
	"unsafe"

	"github.com/ebitengine/purego"
)

// VideoToolbox, Core Media, Core Video and Core Foundation, loaded on first
// use with purego. The encoder's and decoder's output callbacks are made
// once and find their session by an id, so no Go pointer is handed to C as
// user data.

const (
	pathCF = "/System/Library/Frameworks/CoreFoundation.framework/CoreFoundation"
	pathCV = "/System/Library/Frameworks/CoreVideo.framework/CoreVideo"
	pathCM = "/System/Library/Frameworks/CoreMedia.framework/CoreMedia"
	pathVT = "/System/Library/Frameworks/VideoToolbox.framework/VideoToolbox"
)

// cmTime is CMTime.
type cmTime struct {
	Value     int64
	Timescale int32
	Flags     uint32
	Epoch     int64
}

const cmTimeValid = 1

var cmTimeInvalid = cmTime{}

// vtCallbackRecord is VTDecompressionOutputCallbackRecord.
type vtCallbackRecord struct {
	proc, refcon uintptr
}

// Pixel formats (Core Video four-character codes).
var (
	pixNV12Video = fourCC("420v") // kCVPixelFormatType_420YpCbCr8BiPlanarVideoRange
	pixNV12Full  = fourCC("420f") // kCVPixelFormatType_420YpCbCr8BiPlanarFullRange
	pixI420      = fourCC("y420") // kCVPixelFormatType_420YpCbCr8Planar
	codecH264    = fourCC("avc1") // kCMVideoCodecType_H264
)

func fourCC(s string) uint32 {
	return uint32(s[0])<<24 | uint32(s[1])<<16 | uint32(s[2])<<8 | uint32(s[3])
}

var vt struct {
	once sync.Once
	err  error

	// Core Foundation
	release        func(p uintptr)
	dictCreate     func(alloc uintptr, capacity int, keyCB, valueCB uintptr) uintptr
	dictSet        func(d, key, value uintptr)
	dictGet        func(d, key uintptr) uintptr
	numberCreate   func(alloc uintptr, typ int, value unsafe.Pointer) uintptr
	arrayCreate    func(alloc uintptr, values unsafe.Pointer, n int, cb uintptr) uintptr
	arrayCount     func(a uintptr) int
	arrayAt        func(a uintptr, i int) uintptr
	keyCB, valueCB uintptr // &kCFTypeDictionaryKeyCallBacks, &kCFTypeDictionaryValueCallBacks
	arrayCB        uintptr // &kCFTypeArrayCallBacks
	cfTrue         uintptr // kCFBooleanTrue
	cfFalse        uintptr

	// Core Video
	pbCreate    func(alloc uintptr, w, h int, format uint32, attrs uintptr, out *uintptr) int32
	pbLock      func(pb uintptr, flags uint64) int32
	pbUnlock    func(pb uintptr, flags uint64) int32
	pbPlaneBase func(pb uintptr, plane int) uintptr
	pbPlaneBPR  func(pb uintptr, plane int) int
	pbPlaneH    func(pb uintptr, plane int) int
	pbWidth     func(pb uintptr) int
	pbHeight    func(pb uintptr) int
	pbFormat    func(pb uintptr) uint32
	pbRelease   func(pb uintptr)
	poolCreate  func(alloc, pool uintptr, out *uintptr) int32
	keyPixFmt   uintptr // kCVPixelBufferPixelFormatTypeKey
	keyWidth    uintptr
	keyHeight   uintptr
	keyIOSurf   uintptr

	// Core Media
	sbDataBuffer   func(sb uintptr) uintptr
	sbFormat       func(sb uintptr) uintptr
	sbAttachments  func(sb uintptr, create bool) uintptr
	sbCreateReady  func(alloc, data, format uintptr, n, nTiming int, timing uintptr, nSizes int, sizes *uintptr, out *uintptr) int32
	bbLength       func(bb uintptr) uintptr
	bbCopy         func(bb uintptr, offset, length uintptr, dst unsafe.Pointer) int32
	bbCreate       func(alloc, mem uintptr, length uintptr, blockAlloc, source uintptr, offset, dataLength uintptr, flags uint32, out *uintptr) int32
	bbReplace      func(src unsafe.Pointer, bb uintptr, offset, length uintptr) int32
	paramSet       func(desc uintptr, i uintptr, ptr *uintptr, size *uintptr, count *uintptr, headerLen *int32) int32
	descFromParams func(alloc uintptr, n uintptr, ptrs unsafe.Pointer, sizes unsafe.Pointer, headerLen int32, out *uintptr) int32
	keyNotSync     uintptr // kCMSampleAttachmentKey_NotSync

	// VideoToolbox
	compCreate     func(alloc uintptr, w, h int32, codec uint32, spec, srcAttrs, dataAlloc, cb, refcon uintptr, out *uintptr) int32
	compPrepare    func(s uintptr) int32
	compEncode     func(s, pb uintptr, pts, dur cmTime, props, frameRef uintptr, flags *uint32) int32
	compComplete   func(s uintptr, until cmTime) int32
	compPool       func(s uintptr) uintptr
	compInvalidate func(s uintptr)
	setProperty    func(s, key, value uintptr) int32
	decCreate      func(alloc, desc, spec, attrs uintptr, cb *vtCallbackRecord, out *uintptr) int32
	decDecode      func(s, sb uintptr, flags uint32, frameRef uintptr, info *uint32) int32
	decAccepts     func(s, desc uintptr) bool
	decWait        func(s uintptr) int32
	decInvalidate  func(s uintptr)

	kRealTime, kProfile, kReorder, kAvgBitRate, kRateLimits uintptr
	kMaxKeyInterval, kMaxKeyDuration, kFrameRate            uintptr
	kConstrainedBaseline, kBaseline                         uintptr
	kForceKeyFrame, kLowLatency                             uintptr

	encodeOutput, decodeOutput uintptr // the callbacks
}

// cfNumber types.
const (
	cfSInt32  = 3
	cfFloat64 = 6
)

func loadVT() error {
	vt.once.Do(func() { vt.err = loadFrameworks() })
	return vt.err
}

type binder struct {
	lib  uintptr
	path string
	err  error
}

func open(path string) *binder {
	lib, err := purego.Dlopen(path, purego.RTLD_LAZY|purego.RTLD_LOCAL)
	return &binder{lib: lib, path: path, err: err}
}

func (b *binder) fn(dst any, name string) {
	if b.err != nil {
		return
	}
	sym, err := purego.Dlsym(b.lib, name)
	if err != nil {
		b.err = fmt.Errorf("%s: %w", name, err)
		return
	}
	purego.RegisterFunc(dst, sym)
}

// addr is the address of a data symbol (a struct the API takes by pointer).
func (b *binder) addr(name string) uintptr {
	if b.err != nil {
		return 0
	}
	sym, err := purego.Dlsym(b.lib, name)
	if err != nil {
		b.err = fmt.Errorf("%s: %w", name, err)
		return 0
	}
	return sym
}

// val is the value of a pointer-sized data symbol (a CFStringRef constant).
func (b *binder) val(name string) uintptr {
	a := b.addr(name)
	if a == 0 {
		return 0
	}
	return deref(a)
}

// optional is val for a constant older systems lack: 0 there.
func (b *binder) optional(name string) uintptr {
	if b.err != nil {
		return 0
	}
	sym, err := purego.Dlsym(b.lib, name)
	if err != nil {
		return 0
	}
	return deref(sym)
}

// deref reads the pointer-sized word at a C address.
func deref(a uintptr) uintptr { return **(**uintptr)(unsafe.Pointer(&a)) }

// cBytes is n bytes of C memory at p, valid as long as C keeps them.
func cBytes(p uintptr, n int) []byte {
	if p == 0 || n <= 0 {
		return nil
	}
	return unsafe.Slice(*(**byte)(unsafe.Pointer(&p)), n)
}

func loadFrameworks() error {
	cf := open(pathCF)
	cf.fn(&vt.release, "CFRelease")
	cf.fn(&vt.dictCreate, "CFDictionaryCreateMutable")
	cf.fn(&vt.dictSet, "CFDictionarySetValue")
	cf.fn(&vt.dictGet, "CFDictionaryGetValue")
	cf.fn(&vt.numberCreate, "CFNumberCreate")
	cf.fn(&vt.arrayCreate, "CFArrayCreate")
	cf.fn(&vt.arrayCount, "CFArrayGetCount")
	cf.fn(&vt.arrayAt, "CFArrayGetValueAtIndex")
	vt.keyCB = cf.addr("kCFTypeDictionaryKeyCallBacks")
	vt.valueCB = cf.addr("kCFTypeDictionaryValueCallBacks")
	vt.arrayCB = cf.addr("kCFTypeArrayCallBacks")
	vt.cfTrue = cf.val("kCFBooleanTrue")
	vt.cfFalse = cf.val("kCFBooleanFalse")
	if cf.err != nil {
		return &UnavailableError{What: "CoreFoundation", Err: cf.err}
	}

	cv := open(pathCV)
	cv.fn(&vt.pbCreate, "CVPixelBufferCreate")
	cv.fn(&vt.pbLock, "CVPixelBufferLockBaseAddress")
	cv.fn(&vt.pbUnlock, "CVPixelBufferUnlockBaseAddress")
	cv.fn(&vt.pbPlaneBase, "CVPixelBufferGetBaseAddressOfPlane")
	cv.fn(&vt.pbPlaneBPR, "CVPixelBufferGetBytesPerRowOfPlane")
	cv.fn(&vt.pbPlaneH, "CVPixelBufferGetHeightOfPlane")
	cv.fn(&vt.pbWidth, "CVPixelBufferGetWidth")
	cv.fn(&vt.pbHeight, "CVPixelBufferGetHeight")
	cv.fn(&vt.pbFormat, "CVPixelBufferGetPixelFormatType")
	cv.fn(&vt.pbRelease, "CVPixelBufferRelease")
	cv.fn(&vt.poolCreate, "CVPixelBufferPoolCreatePixelBuffer")
	vt.keyPixFmt = cv.val("kCVPixelBufferPixelFormatTypeKey")
	vt.keyWidth = cv.val("kCVPixelBufferWidthKey")
	vt.keyHeight = cv.val("kCVPixelBufferHeightKey")
	vt.keyIOSurf = cv.val("kCVPixelBufferIOSurfacePropertiesKey")
	if cv.err != nil {
		return &UnavailableError{What: "CoreVideo", Err: cv.err}
	}

	cm := open(pathCM)
	cm.fn(&vt.sbDataBuffer, "CMSampleBufferGetDataBuffer")
	cm.fn(&vt.sbFormat, "CMSampleBufferGetFormatDescription")
	cm.fn(&vt.sbAttachments, "CMSampleBufferGetSampleAttachmentsArray")
	cm.fn(&vt.sbCreateReady, "CMSampleBufferCreateReady")
	cm.fn(&vt.bbLength, "CMBlockBufferGetDataLength")
	cm.fn(&vt.bbCopy, "CMBlockBufferCopyDataBytes")
	cm.fn(&vt.bbCreate, "CMBlockBufferCreateWithMemoryBlock")
	cm.fn(&vt.bbReplace, "CMBlockBufferReplaceDataBytes")
	cm.fn(&vt.paramSet, "CMVideoFormatDescriptionGetH264ParameterSetAtIndex")
	cm.fn(&vt.descFromParams, "CMVideoFormatDescriptionCreateFromH264ParameterSets")
	vt.keyNotSync = cm.val("kCMSampleAttachmentKey_NotSync")
	if cm.err != nil {
		return &UnavailableError{What: "CoreMedia", Err: cm.err}
	}

	v := open(pathVT)
	v.fn(&vt.compCreate, "VTCompressionSessionCreate")
	v.fn(&vt.compPrepare, "VTCompressionSessionPrepareToEncodeFrames")
	v.fn(&vt.compEncode, "VTCompressionSessionEncodeFrame")
	v.fn(&vt.compComplete, "VTCompressionSessionCompleteFrames")
	v.fn(&vt.compPool, "VTCompressionSessionGetPixelBufferPool")
	v.fn(&vt.compInvalidate, "VTCompressionSessionInvalidate")
	v.fn(&vt.setProperty, "VTSessionSetProperty")
	v.fn(&vt.decCreate, "VTDecompressionSessionCreate")
	v.fn(&vt.decDecode, "VTDecompressionSessionDecodeFrame")
	v.fn(&vt.decAccepts, "VTDecompressionSessionCanAcceptFormatDescription")
	v.fn(&vt.decWait, "VTDecompressionSessionWaitForAsynchronousFrames")
	v.fn(&vt.decInvalidate, "VTDecompressionSessionInvalidate")
	vt.kRealTime = v.val("kVTCompressionPropertyKey_RealTime")
	vt.kProfile = v.val("kVTCompressionPropertyKey_ProfileLevel")
	vt.kReorder = v.val("kVTCompressionPropertyKey_AllowFrameReordering")
	vt.kAvgBitRate = v.val("kVTCompressionPropertyKey_AverageBitRate")
	vt.kRateLimits = v.val("kVTCompressionPropertyKey_DataRateLimits")
	vt.kMaxKeyInterval = v.val("kVTCompressionPropertyKey_MaxKeyFrameInterval")
	vt.kMaxKeyDuration = v.val("kVTCompressionPropertyKey_MaxKeyFrameIntervalDuration")
	vt.kFrameRate = v.val("kVTCompressionPropertyKey_ExpectedFrameRate")
	vt.kBaseline = v.val("kVTProfileLevel_H264_Baseline_AutoLevel")
	vt.kConstrainedBaseline = v.optional("kVTProfileLevel_H264_ConstrainedBaseline_AutoLevel")
	vt.kForceKeyFrame = v.val("kVTEncodeFrameOptionKey_ForceKeyFrame")
	vt.kLowLatency = v.optional("kVTVideoEncoderSpecification_EnableLowLatencyRateControl")
	if v.err != nil {
		return &UnavailableError{What: "VideoToolbox", Err: v.err}
	}
	vt.encodeOutput = purego.NewCallback(encodeOutput)
	vt.decodeOutput = purego.NewCallback(decodeOutput)
	return nil
}

// cfDict is a new mutable CFDictionary of CF values; the caller releases
// it.
func cfDict() uintptr { return vt.dictCreate(0, 0, vt.keyCB, vt.valueCB) }

// setInt sets key to an SInt32 CFNumber.
func setInt(d, key uintptr, v int32) {
	n := vt.numberCreate(0, cfSInt32, unsafe.Pointer(&v))
	vt.dictSet(d, key, n)
	vt.release(n)
}

// cfInt is a new SInt32 CFNumber; cfFloat a Float64 one.
func cfInt(v int32) uintptr { return vt.numberCreate(0, cfSInt32, unsafe.Pointer(&v)) }

func cfFloat(v float64) uintptr { return vt.numberCreate(0, cfFloat64, unsafe.Pointer(&v)) }

// setSessionNumber sets a session property to a new CFNumber.
func setSessionNumber(s, key, n uintptr) int32 {
	st := vt.setProperty(s, key, n)
	vt.release(n)
	return st
}

// pixelAttrs is a destination or source image-buffer attribute dictionary
// for format (and w by h when not 0), IOSurface-backed.
func pixelAttrs(format uint32, w, h int) uintptr {
	d := cfDict()
	setInt(d, vt.keyPixFmt, int32(format))
	if w > 0 && h > 0 {
		setInt(d, vt.keyWidth, int32(w))
		setInt(d, vt.keyHeight, int32(h))
	}
	io := cfDict()
	vt.dictSet(d, vt.keyIOSurf, io)
	vt.release(io)
	return d
}

// readPixelBuffer copies a locked-for-reading 4:2:0 CVPixelBuffer (NV12 or
// planar) into a new packed I420.
func readPixelBuffer(pb uintptr) (*I420, error) {
	if st := vt.pbLock(pb, 1); st != 0 { // kCVPixelBufferLock_ReadOnly
		return nil, &StatusError{Op: "CVPixelBufferLockBaseAddress", Status: st}
	}
	defer vt.pbUnlock(pb, 1)
	w, h := vt.pbWidth(pb), vt.pbHeight(pb)
	if w <= 0 || h <= 0 {
		return nil, &StatusError{Op: "CVPixelBufferGetWidth", Status: -1}
	}
	p := NewI420(w, h)
	plane := func(i int) ([]byte, int) {
		bpr, rows := vt.pbPlaneBPR(pb, i), vt.pbPlaneH(pb, i)
		return cBytes(vt.pbPlaneBase(pb, i), bpr*rows), bpr
	}
	switch f := vt.pbFormat(pb); f {
	case pixNV12Video, pixNV12Full:
		y, ys := plane(0)
		uv, uvs := plane(1)
		if len(y) < ys*h || len(uv) < uvs*p.ChromaH() {
			return nil, &StatusError{Op: "CVPixelBufferGetBaseAddressOfPlane", Status: -1}
		}
		FromNV12(p, y, ys, uv, uvs)
	case pixI420:
		y, ys := plane(0)
		u, us := plane(1)
		v, vs := plane(2)
		copyPlane(p.Y(), p.Stride[0], y, ys, w, h)
		copyPlane(p.U(), p.Stride[1], u, us, p.ChromaW(), p.ChromaH())
		copyPlane(p.V(), p.Stride[2], v, vs, p.ChromaW(), p.ChromaH())
	default:
		return nil, &StatusError{Op: fmt.Sprintf("CVPixelBuffer format %08x", f), Status: -1}
	}
	return p, nil
}
