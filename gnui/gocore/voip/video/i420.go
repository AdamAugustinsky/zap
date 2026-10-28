package video

import (
	"math"

	"zapfast/core"
)

// I420 is one YUV 4:2:0 picture as the UI draws it (zfcore's CallFrame,
// gnui/core.Frame): Y, then U, then V, one after the other in Px, Stride[i]
// bytes a row. Y has H rows, U and V (H+1)/2, each (W+1)/2 samples wide.
// NewI420 packs the planes tightly; a picture from elsewhere may have wider
// strides.
type I420 struct {
	W, H   int
	Stride [3]int
	Px     []byte
}

// NewI420 is a black picture of w by h, every plane tightly packed.
func NewI420(w, h int) *I420 {
	w, h = max(w, 0), max(h, 0)
	c := (w + 1) / 2
	p := &I420{W: w, H: h, Stride: [3]int{w, c, c}, Px: make([]byte, I420Len(w, h))}
	p.Fill(16, 128, 128)
	return p
}

// I420Len is the bytes of a tightly packed w by h picture.
func I420Len(w, h int) int {
	c := (w + 1) / 2
	return w*h + 2*c*((h+1)/2)
}

// ChromaW and ChromaH are the chroma planes' width and height.
func (p *I420) ChromaW() int { return (p.W + 1) / 2 }

// ChromaH is the chroma planes' height.
func (p *I420) ChromaH() int { return (p.H + 1) / 2 }

// Y, U and V are the planes, each Stride[i]*rows bytes.
func (p *I420) Y() []byte { return p.Px[:p.Stride[0]*p.H] }

// U is the Cb plane.
func (p *I420) U() []byte {
	at := p.Stride[0] * p.H
	return p.Px[at : at+p.Stride[1]*p.ChromaH()]
}

// V is the Cr plane.
func (p *I420) V() []byte {
	at := p.Stride[0]*p.H + p.Stride[1]*p.ChromaH()
	return p.Px[at : at+p.Stride[2]*p.ChromaH()]
}

// Valid says the strides cover the size and Px holds every plane.
func (p *I420) Valid() bool {
	if p == nil || p.W <= 0 || p.H <= 0 {
		return false
	}
	cw, ch := p.ChromaW(), p.ChromaH()
	if p.Stride[0] < p.W || p.Stride[1] < cw || p.Stride[2] < cw {
		return false
	}
	return len(p.Px) >= p.Stride[0]*p.H+(p.Stride[1]+p.Stride[2])*ch
}

// Fill paints every sample: y on Y, u and v on the chroma planes.
func (p *I420) Fill(y, u, v byte) {
	fill(p.Y(), y)
	fill(p.U(), u)
	fill(p.V(), v)
}

func fill(b []byte, v byte) {
	for i := range b {
		b[i] = v
	}
}

// Core is p as the UI's core.Frame, stamped pts. The frame shares Px.
func (p *I420) Core(pts int64) core.Frame {
	return core.Frame{Px: p.Px, W: p.W, H: p.H, Stride: p.Stride, Format: core.FrameI420, PTS: pts}
}

// Packed is p with tightly packed planes: p itself when it already is.
func (p *I420) Packed() *I420 {
	cw := p.ChromaW()
	if p.Stride == [3]int{p.W, cw, cw} && len(p.Px) == I420Len(p.W, p.H) {
		return p
	}
	q := NewI420(p.W, p.H)
	copyPlane(q.Y(), q.Stride[0], p.Y(), p.Stride[0], p.W, p.H)
	copyPlane(q.U(), q.Stride[1], p.U(), p.Stride[1], cw, p.ChromaH())
	copyPlane(q.V(), q.Stride[2], p.V(), p.Stride[2], cw, p.ChromaH())
	return q
}

func copyPlane(dst []byte, dstStride int, src []byte, srcStride, w, h int) {
	for r := range h {
		copy(dst[r*dstStride:r*dstStride+w], src[r*srcStride:r*srcStride+w])
	}
}

// FromNV12 converts a biplanar 4:2:0 picture (a camera's or VideoToolbox's
// 420v/420f: Y, then interleaved CbCr) into dst, which must be w by h.
func FromNV12(dst *I420, y []byte, yStride int, uv []byte, uvStride int) {
	copyPlane(dst.Y(), dst.Stride[0], y, yStride, dst.W, dst.H)
	cw, ch := dst.ChromaW(), dst.ChromaH()
	u, v := dst.U(), dst.V()
	for r := range ch {
		src := uv[r*uvStride : r*uvStride+2*cw]
		du := u[r*dst.Stride[1] : r*dst.Stride[1]+cw]
		dv := v[r*dst.Stride[2] : r*dst.Stride[2]+cw]
		for x := range cw {
			du[x], dv[x] = src[2*x], src[2*x+1]
		}
	}
}

// ToNV12 writes p as NV12 planes: y (yStride a row) and interleaved CbCr
// (uvStride a row).
func (p *I420) ToNV12(y []byte, yStride int, uv []byte, uvStride int) {
	copyPlane(y, yStride, p.Y(), p.Stride[0], p.W, p.H)
	cw, ch := p.ChromaW(), p.ChromaH()
	u, v := p.U(), p.V()
	for r := range ch {
		dst := uv[r*uvStride : r*uvStride+2*cw]
		su := u[r*p.Stride[1] : r*p.Stride[1]+cw]
		sv := v[r*p.Stride[2] : r*p.Stride[2]+cw]
		for x := range cw {
			dst[2*x], dst[2*x+1] = su[x], sv[x]
		}
	}
}

// Scale is p resized to w by h, by area averaging on the way down and
// nearest sample on the way up (zfcore's call_frame: luma averaged, chroma
// sampled when it is the UI's size). p itself when the size is the same.
func (p *I420) Scale(w, h int) *I420 {
	if w == p.W && h == p.H {
		return p
	}
	q := NewI420(w, h)
	scalePlane(q.Y(), q.Stride[0], w, h, p.Y(), p.Stride[0], p.W, p.H)
	scalePlane(q.U(), q.Stride[1], q.ChromaW(), q.ChromaH(), p.U(), p.Stride[1], p.ChromaW(), p.ChromaH())
	scalePlane(q.V(), q.Stride[2], q.ChromaW(), q.ChromaH(), p.V(), p.Stride[2], p.ChromaW(), p.ChromaH())
	return q
}

// scalePlane box-filters src (sw by sh) into dst (dw by dh): each output
// sample averages the source samples its area covers, at least one.
func scalePlane(dst []byte, ds, dw, dh int, src []byte, ss, sw, sh int) {
	if dw <= 0 || dh <= 0 || sw <= 0 || sh <= 0 {
		return
	}
	for r := range dh {
		y0 := r * sh / dh
		y1 := max((r+1)*sh/dh, y0+1)
		for c := range dw {
			x0 := c * sw / dw
			x1 := max((c+1)*sw/dw, x0+1)
			sum, n := 0, 0
			for y := y0; y < y1; y++ {
				row := src[y*ss:]
				for x := x0; x < x1; x++ {
					sum += int(row[x])
					n++
				}
			}
			dst[r*ds+c] = byte((sum + n/2) / n)
		}
	}
}

// Rotate is p turned clockwise by turns quarter turns (a peer's camera
// orientation); p itself for none.
func (p *I420) Rotate(turns int) *I420 {
	turns &= 3
	if turns == 0 {
		return p
	}
	w, h := p.W, p.H
	if turns%2 == 1 {
		w, h = h, w
	}
	q := NewI420(w, h)
	rotatePlane(q.Y(), q.Stride[0], p.Y(), p.Stride[0], p.W, p.H, turns)
	rotatePlane(q.U(), q.Stride[1], p.U(), p.Stride[1], p.ChromaW(), p.ChromaH(), turns)
	rotatePlane(q.V(), q.Stride[2], p.V(), p.Stride[2], p.ChromaW(), p.ChromaH(), turns)
	return q
}

// rotatePlane turns src (w by h) clockwise into dst. An odd-sized picture's
// chroma planes are a sample wider than half the turned picture's; the
// samples that fall outside are dropped and the ones not reached stay grey.
func rotatePlane(dst []byte, ds int, src []byte, ss, w, h, turns int) {
	dw, dh := w, h
	if turns%2 == 1 {
		dw, dh = h, w
	}
	rows := len(dst) / max(ds, 1)
	for y := range h {
		for x := range w {
			var dx, dy int
			switch turns {
			case 1:
				dx, dy = h-1-y, x
			case 2:
				dx, dy = w-1-x, h-1-y
			case 3:
				dx, dy = y, w-1-x
			}
			if dx < dw && dy < dh && dx < ds && dy < rows {
				dst[dy*ds+dx] = src[y*ss+x]
			}
		}
	}
}

// FitEven scales w by h to the largest even size of the same shape that
// fits bounds (long by short) either way round, never upscaling (camera.rs's
// fit_even).
func FitEven(w, h, long, short int) (int, int) {
	even := func(v float64) int { return max(int(math.Round(v))/2*2, 2) }
	long, short = max(long, short), min(long, short)
	if w <= 0 || h <= 0 {
		return even(float64(long)), even(float64(short))
	}
	fw, fh := long, short
	if w < h {
		fw, fh = short, long
	}
	scale := min(float64(fw)/float64(w), float64(fh)/float64(h), 1)
	return even(float64(w) * scale), even(float64(h) * scale)
}
