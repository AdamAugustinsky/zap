//go:build darwin

package camera

import (
	"testing"
	"unsafe"

	"github.com/ebitengine/purego"
	"github.com/ebitengine/purego/objc"

	"zapfast/gocore/voip/video"
)

func TestNSStringRoundTrip(t *testing.T) {
	if err := load(); err != nil {
		t.Fatal(err)
	}
	var got string
	pooled(func() { got = goString(nsString("Câmera — 1")) })
	if got != "Câmera — 1" {
		t.Fatalf("round trip %q", got)
	}
}

// The delegate class, sent a synthetic NV12 sample buffer as the capture
// output would, queues the picture as I420 at the camera's size. No camera
// is opened.
func TestDelegateTakesASampleBuffer(t *testing.T) {
	if err := load(); err != nil {
		t.Fatal(err)
	}
	cm, _ := purego.Dlopen(pathCM, purego.RTLD_LAZY)
	cv, _ := purego.Dlopen(pathCV, purego.RTLD_LAZY)
	var (
		pbCreate func(alloc uintptr, w, h int, format uint32, attrs uintptr, out *uintptr) int32
		descNew  func(alloc, pb uintptr, out *uintptr) int32
		sbNew    func(alloc, pb, desc uintptr, timing unsafe.Pointer, out *uintptr) int32
	)
	reg := func(lib uintptr, dst any, name string) {
		sym, err := purego.Dlsym(lib, name)
		if err != nil {
			t.Fatal(err)
		}
		purego.RegisterFunc(dst, sym)
	}
	reg(cv, &pbCreate, "CVPixelBufferCreate")
	reg(cm, &descNew, "CMVideoFormatDescriptionCreateForImageBuffer")
	reg(cm, &sbNew, "CMSampleBufferCreateReadyWithImageBuffer")

	src := video.NewI420(64, 48)
	y := src.Y()
	for i := range y {
		y[i] = byte(i)
	}
	for i := range src.U() {
		src.U()[i], src.V()[i] = 77, 99
	}
	var pb uintptr
	if st := pbCreate(0, 64, 48, pix420v, 0, &pb); st != 0 {
		t.Fatalf("CVPixelBufferCreate: %d", st)
	}
	defer av.release(pb)
	av.pbLock(pb, 0)
	ys, uvs := av.planeBPR(pb, 0), av.planeBPR(pb, 1)
	src.ToNV12(cBytes(av.planeBase(pb, 0), ys*48), ys, cBytes(av.planeBase(pb, 1), uvs*24), uvs)
	av.pbUnlock(pb, 0)
	var desc, sb uintptr
	if st := descNew(0, pb, &desc); st != 0 {
		t.Fatalf("format description: %d", st)
	}
	defer av.release(desc)
	timing := [9]int64{} // CMSampleTimingInfo: three invalid CMTimes
	if st := sbNew(0, pb, desc, unsafe.Pointer(&timing[0]), &sb); st != 0 {
		t.Fatalf("sample buffer: %d", st)
	}
	defer av.release(sb)

	q := newQueue()
	var delegate objc.ID
	pooled(func() { delegate = objc.ID(av.delegate).Send(sel.alloc).Send(sel.init) })
	defer delegate.Send(sel.release)
	// Asked for 32 by 24: the picture is scaled.
	cameras.Store(delegate, &sysCamera{w: 32, h: 24, q: q})
	defer cameras.Delete(delegate)
	delegate.Send(objc.RegisterName("captureOutput:didOutputSampleBuffer:fromConnection:"), uintptr(0), sb, uintptr(0))
	select {
	case f := <-q.ch:
		if f.Pic.W != 32 || f.Pic.H != 24 || !f.Pic.Valid() || f.At.IsZero() {
			t.Fatalf("frame %dx%d", f.Pic.W, f.Pic.H)
		}
		if f.Pic.U()[0] != 77 || f.Pic.V()[0] != 99 {
			t.Fatalf("chroma %d %d", f.Pic.U()[0], f.Pic.V()[0])
		}
	default:
		t.Fatal("the delegate queued nothing")
	}
	// Unscaled, the picture is the buffer's own.
	cameras.Store(delegate, &sysCamera{w: 64, h: 48, q: q})
	delegate.Send(objc.RegisterName("captureOutput:didOutputSampleBuffer:fromConnection:"), uintptr(0), sb, uintptr(0))
	if f := <-q.ch; string(f.Pic.Px) != string(src.Px) {
		t.Fatal("the NV12 picture did not come through unchanged")
	}
	// A delegate no camera owns does nothing.
	cameras.Delete(delegate)
	delegate.Send(objc.RegisterName("captureOutput:didOutputSampleBuffer:fromConnection:"), uintptr(0), sb, uintptr(0))
	if len(q.ch) != 0 {
		t.Fatal("an orphan delegate queued a frame")
	}
}
