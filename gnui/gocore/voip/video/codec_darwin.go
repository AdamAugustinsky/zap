//go:build darwin

package video

import (
	"bytes"
	"runtime"
	"sync"
	"sync/atomic"
	"unsafe"
)

// sessions are the encoders and decoders whose callbacks may run, by the
// id their callbacks get.
var (
	sessions sync.Map // uintptr → *sysEncoder or *sysDecoder
	nextID   atomic.Uintptr
)

// sysEncoder is a VTCompressionSession. Encode is synchronous: the frame
// is handed over and VTCompressionSessionCompleteFrames waits for its
// output, which the callback leaves in out.
type sysEncoder struct {
	id      uintptr
	session uintptr
	w, h    int
	fps     int

	mu  sync.Mutex // out and err, written by the callback
	out []byte
	err error
}

func newSysEncoder(cfg EncoderConfig) (*sysEncoder, error) {
	if err := loadVT(); err != nil {
		return nil, err
	}
	e := &sysEncoder{id: nextID.Add(1), w: cfg.Width, h: cfg.Height, fps: cfg.FPS}
	sessions.Store(e.id, e)
	src := pixelAttrs(pixNV12Video, cfg.Width, cfg.Height)
	defer vt.release(src)
	create := func(spec uintptr) int32 {
		return vt.compCreate(0, int32(cfg.Width), int32(cfg.Height), codecH264, spec, src, 0,
			vt.encodeOutput, e.id, &e.session)
	}
	st := int32(-1)
	if vt.kLowLatency != 0 {
		// Low-latency rate control (macOS 11.3): no lookahead, the rate kept
		// frame by frame, as a call wants.
		spec := cfDict()
		vt.dictSet(spec, vt.kLowLatency, vt.cfTrue)
		st = create(spec)
		vt.release(spec)
	}
	if st != 0 {
		st = create(0)
	}
	if st != 0 {
		sessions.Delete(e.id)
		return nil, &StatusError{Op: "VTCompressionSessionCreate", Status: st}
	}
	s := e.session
	vt.setProperty(s, vt.kRealTime, vt.cfTrue)
	vt.setProperty(s, vt.kReorder, vt.cfFalse)
	profile := vt.kConstrainedBaseline
	if profile == 0 || vt.setProperty(s, vt.kProfile, profile) != 0 {
		vt.setProperty(s, vt.kProfile, vt.kBaseline)
	}
	setSessionNumber(s, vt.kMaxKeyInterval, cfInt(int32(2*cfg.FPS)))
	setSessionNumber(s, vt.kMaxKeyDuration, cfFloat(2))
	setSessionNumber(s, vt.kFrameRate, cfInt(int32(cfg.FPS)))
	if err := e.setBitrate(cfg.Bitrate); err != nil {
		e.close()
		return nil, err
	}
	if st := vt.compPrepare(s); st != 0 {
		e.close()
		return nil, &StatusError{Op: "VTCompressionSessionPrepareToEncodeFrames", Status: st}
	}
	return e, nil
}

func (e *sysEncoder) setBitrate(bps int) error {
	if st := setSessionNumber(e.session, vt.kAvgBitRate, cfInt(int32(min(bps, 1<<31-1)))); st != 0 {
		return &StatusError{Op: "VTSessionSetProperty(AverageBitRate)", Status: st}
	}
	// A hard ceiling half again over the average across one second, so a
	// busy scene does not send what the path cannot carry. Some encoders
	// lack it; the average still holds.
	limit := [2]uintptr{cfInt(int32(min(bps/8*3/2, 1<<31-1))), cfFloat(1)}
	arr := vt.arrayCreate(0, unsafe.Pointer(&limit[0]), 2, vt.arrayCB)
	vt.setProperty(e.session, vt.kRateLimits, arr)
	vt.release(arr)
	vt.release(limit[0])
	vt.release(limit[1])
	return nil
}

func (e *sysEncoder) encode(p *I420, frame int64, key bool) ([]byte, error) {
	var pb uintptr
	if pool := vt.compPool(e.session); pool == 0 || vt.poolCreate(0, pool, &pb) != 0 {
		attrs := pixelAttrs(pixNV12Video, 0, 0)
		st := vt.pbCreate(0, e.w, e.h, pixNV12Video, attrs, &pb)
		vt.release(attrs)
		if st != 0 {
			return nil, &StatusError{Op: "CVPixelBufferCreate", Status: st}
		}
	}
	defer vt.pbRelease(pb)
	if st := vt.pbLock(pb, 0); st != 0 {
		return nil, &StatusError{Op: "CVPixelBufferLockBaseAddress", Status: st}
	}
	ys, uvs := vt.pbPlaneBPR(pb, 0), vt.pbPlaneBPR(pb, 1)
	y := cBytes(vt.pbPlaneBase(pb, 0), ys*vt.pbPlaneH(pb, 0))
	uv := cBytes(vt.pbPlaneBase(pb, 1), uvs*vt.pbPlaneH(pb, 1))
	if len(y) < ys*p.H || len(uv) < uvs*p.ChromaH() {
		vt.pbUnlock(pb, 0)
		return nil, &StatusError{Op: "CVPixelBufferGetBaseAddressOfPlane", Status: -1}
	}
	p.ToNV12(y, ys, uv, uvs)
	vt.pbUnlock(pb, 0)

	var props uintptr
	if key {
		props = cfDict()
		vt.dictSet(props, vt.kForceKeyFrame, vt.cfTrue)
		defer vt.release(props)
	}
	e.mu.Lock()
	e.out, e.err = nil, nil
	e.mu.Unlock()
	pts := cmTime{Value: frame, Timescale: int32(e.fps), Flags: cmTimeValid}
	var flags uint32
	if st := vt.compEncode(e.session, pb, pts, cmTimeInvalid, props, 0, &flags); st != 0 {
		return nil, &StatusError{Op: "VTCompressionSessionEncodeFrame", Status: st}
	}
	if st := vt.compComplete(e.session, cmTimeInvalid); st != 0 {
		return nil, &StatusError{Op: "VTCompressionSessionCompleteFrames", Status: st}
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	out, err := e.out, e.err
	e.out, e.err = nil, nil
	return out, err
}

func (e *sysEncoder) close() {
	if e.session != 0 {
		vt.compComplete(e.session, cmTimeInvalid)
		vt.compInvalidate(e.session)
		vt.release(e.session)
		e.session = 0
	}
	sessions.Delete(e.id)
}

// encodeOutput is the VTCompressionOutputCallback: the sample buffer
// becomes an Annex-B access unit, AUD first, a keyframe's SPS and PPS
// before its slices.
func encodeOutput(refcon, _ uintptr, status, _ uintptr, sb uintptr) {
	v, ok := sessions.Load(refcon)
	if !ok {
		return
	}
	e, ok := v.(*sysEncoder)
	if !ok {
		return
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	if st := int32(status); st != 0 {
		e.err = &StatusError{Op: "VTCompressionOutputCallback", Status: st}
		return
	}
	if sb == 0 {
		return // dropped
	}
	unit := append([]byte(nil), aud...)
	desc := vt.sbFormat(sb)
	headerLen := int32(4)
	if desc != 0 {
		// The first parameter set also says how many there are and the
		// length size of the sample's NAL units.
		var count, ptr, size uintptr
		if vt.paramSet(desc, 0, &ptr, &size, &count, &headerLen) == 0 && keyframeSample(sb) {
			unit = appendAnnexB(unit, cBytes(ptr, int(size)))
			for i := uintptr(1); i < count; i++ {
				if vt.paramSet(desc, i, &ptr, &size, nil, nil) != 0 {
					break
				}
				unit = appendAnnexB(unit, cBytes(ptr, int(size)))
			}
		}
	}
	bb := vt.sbDataBuffer(sb)
	if bb == 0 {
		e.err = &StatusError{Op: "CMSampleBufferGetDataBuffer", Status: -1}
		return
	}
	n := vt.bbLength(bb)
	data := make([]byte, n)
	if n > 0 {
		if st := vt.bbCopy(bb, 0, n, unsafe.Pointer(&data[0])); st != 0 {
			e.err = &StatusError{Op: "CMBlockBufferCopyDataBytes", Status: st}
			return
		}
	}
	if headerLen <= 0 || headerLen > 4 {
		headerLen = 4
	}
	unit, ok = avccToAnnexB(unit, data, int(headerLen))
	if !ok {
		e.err = &StatusError{Op: "VTCompressionOutputCallback: a NAL length past the sample", Status: -1}
		return
	}
	e.out = unit
}

// keyframeSample says a sample is a sync sample: its attachments do not
// say NotSync.
func keyframeSample(sb uintptr) bool {
	arr := vt.sbAttachments(sb, false)
	if arr == 0 || vt.arrayCount(arr) == 0 {
		return true
	}
	d := vt.arrayAt(arr, 0)
	if d == 0 {
		return true
	}
	ns := vt.dictGet(d, vt.keyNotSync)
	return ns == 0 || ns == vt.cfFalse
}

// sysDecoder is a VTDecompressionSession for the stream's current SPS and
// PPS. Decoding is synchronous: the callback has run when
// VTDecompressionSessionDecodeFrame returns, and leaves its picture in out.
type sysDecoder struct {
	id       uintptr
	session  uintptr
	desc     uintptr
	sps, pps []byte
	started  bool // a keyframe has been decoded since the last failure

	mu  sync.Mutex
	out *I420
	err error
}

func newSysDecoder() (*sysDecoder, error) {
	if err := loadVT(); err != nil {
		return nil, err
	}
	d := &sysDecoder{id: nextID.Add(1)}
	sessions.Store(d.id, d)
	return d, nil
}

func (d *sysDecoder) decode(unit []byte) (*I420, error) {
	var sample []byte
	key, params := false, false
	for _, u := range SplitAnnexB(unit) {
		switch NALType(u) {
		case NALSPS:
			if !bytes.Equal(u, d.sps) {
				d.sps, params = append([]byte(nil), u...), true
			}
		case NALPPS:
			if !bytes.Equal(u, d.pps) {
				d.pps, params = append([]byte(nil), u...), true
			}
		case NALAUD:
		case NALIDR:
			key = true
			sample = appendAVCC(sample, u)
		default:
			sample = appendAVCC(sample, u)
		}
	}
	if params && d.sps != nil && d.pps != nil {
		if err := d.format(); err != nil {
			return nil, err
		}
	}
	if !hasSlice(unit) {
		return nil, nil
	}
	if d.desc == 0 || (!d.started && !key) {
		return nil, ErrNeedKeyframe
	}
	if d.session == 0 {
		if err := d.open(); err != nil {
			return nil, err
		}
	}
	p, err := d.decodeSample(sample)
	if err != nil {
		d.started = false
		return nil, err
	}
	if key {
		d.started = true
	}
	return p, nil
}

func hasSlice(unit []byte) bool {
	for _, u := range SplitAnnexB(unit) {
		if t := NALType(u); t >= NALSlice && t <= NALIDR {
			return true
		}
	}
	return false
}

// format makes the format description of the current SPS and PPS, and lets
// the session go if it cannot take it.
func (d *sysDecoder) format() error {
	var pin runtime.Pinner
	defer pin.Unpin()
	pin.Pin(&d.sps[0])
	pin.Pin(&d.pps[0])
	ptrs := [2]unsafe.Pointer{unsafe.Pointer(&d.sps[0]), unsafe.Pointer(&d.pps[0])}
	sizes := [2]uintptr{uintptr(len(d.sps)), uintptr(len(d.pps))}
	var desc uintptr
	if st := vt.descFromParams(0, 2, unsafe.Pointer(&ptrs[0]), unsafe.Pointer(&sizes[0]), 4, &desc); st != 0 {
		d.sps, d.pps = nil, nil
		return &StatusError{Op: "CMVideoFormatDescriptionCreateFromH264ParameterSets", Status: st}
	}
	if d.session != 0 && !vt.decAccepts(d.session, desc) {
		d.closeSession()
	}
	if d.desc != 0 {
		vt.release(d.desc)
	}
	d.desc = desc
	d.started = false
	return nil
}

func (d *sysDecoder) open() error {
	attrs := pixelAttrs(pixNV12Video, 0, 0)
	defer vt.release(attrs)
	cb := vtCallbackRecord{proc: vt.decodeOutput, refcon: d.id}
	if st := vt.decCreate(0, d.desc, 0, attrs, &cb, &d.session); st != 0 {
		d.session = 0
		return &StatusError{Op: "VTDecompressionSessionCreate", Status: st}
	}
	return nil
}

func (d *sysDecoder) decodeSample(sample []byte) (*I420, error) {
	var bb uintptr
	n := uintptr(len(sample))
	if st := vt.bbCreate(0, 0, n, 0, 0, 0, n, 1, &bb); st != 0 { // kCMBlockBufferAssureMemoryNowFlag
		return nil, &StatusError{Op: "CMBlockBufferCreateWithMemoryBlock", Status: st}
	}
	defer vt.release(bb)
	if st := vt.bbReplace(unsafe.Pointer(&sample[0]), bb, 0, n); st != 0 {
		return nil, &StatusError{Op: "CMBlockBufferReplaceDataBytes", Status: st}
	}
	var sb uintptr
	size := n
	if st := vt.sbCreateReady(0, bb, d.desc, 1, 0, 0, 1, &size, &sb); st != 0 {
		return nil, &StatusError{Op: "CMSampleBufferCreateReady", Status: st}
	}
	defer vt.release(sb)
	d.mu.Lock()
	d.out, d.err = nil, nil
	d.mu.Unlock()
	var info uint32
	if st := vt.decDecode(d.session, sb, 0, 0, &info); st != 0 {
		return nil, &StatusError{Op: "VTDecompressionSessionDecodeFrame", Status: st}
	}
	vt.decWait(d.session)
	d.mu.Lock()
	defer d.mu.Unlock()
	out, err := d.out, d.err
	d.out, d.err = nil, nil
	return out, err
}

func (d *sysDecoder) closeSession() {
	if d.session != 0 {
		vt.decWait(d.session)
		vt.decInvalidate(d.session)
		vt.release(d.session)
		d.session = 0
	}
}

func (d *sysDecoder) close() {
	d.closeSession()
	if d.desc != 0 {
		vt.release(d.desc)
		d.desc = 0
	}
	sessions.Delete(d.id)
}

// decodeOutput is the VTDecompressionOutputCallback. Its CMTime arguments
// follow imageBuffer and are not read.
func decodeOutput(refcon, _ uintptr, status, _ uintptr, image uintptr) {
	v, ok := sessions.Load(refcon)
	if !ok {
		return
	}
	d, ok := v.(*sysDecoder)
	if !ok {
		return
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	if st := int32(status); st != 0 {
		d.err = &StatusError{Op: "VTDecompressionOutputCallback", Status: st}
		return
	}
	if image == 0 {
		return
	}
	d.out, d.err = readPixelBuffer(image)
}
