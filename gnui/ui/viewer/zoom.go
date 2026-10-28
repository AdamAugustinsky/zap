package viewer

import "math"

// The zoom model of src/image_preview.rs (PreviewState): the picture is
// either fitted to the stage, never enlarged past its own pixels, or shown at
// a zoom, 1 being one picture pixel per window pixel. Pan is where the
// picture's centre lies from the stage's centre, in window pixels.
const (
	minZoom  = 0.25
	maxZoom  = 4
	zoomStep = 1.25 // one header click or one wheel notch
)

type view struct {
	fit      bool
	zoom     float32    // while !fit
	fitScale float32    // the scale the fitted picture is drawn at
	pan      [2]float32 // the picture's centre from the stage's, pixels
}

func newView() view { return view{fit: true, zoom: 1, fitScale: 1} }

// scale is the scale on screen: the fitted one while fitting, else the zoom.
func (v *view) scale() float32 {
	if v.fit {
		return v.fitScale
	}
	return v.zoom
}

// fitScaleOf is the scale that fits a picture of nw×nh into sw×sh, never
// enlarging (image_preview.rs fit_size).
func fitScaleOf(nw, nh, sw, sh float32) float32 {
	if nw <= 0 || nh <= 0 || sw <= 0 || sh <= 0 {
		return 1
	}
	return min(sw/nw, sh/nh, 1)
}

// limit returns the zoom factor factor takes scale to within the limits, and
// never against the direction asked for: a picture fitted below the minimum
// does not grow when zoomed out, and one fitted above it does not jump.
func limit(scale, factor float32) float32 {
	if factor > 1 {
		return max(min(scale*factor, maxZoom), scale)
	}
	return min(max(scale*factor, minZoom), scale)
}

// reset goes back to fitting, centred.
func (v *view) reset() { *v = view{fit: true, zoom: 1, fitScale: v.fitScale, pan: [2]float32{}} }

// clamp keeps the picture where the stage can show it: centred on an axis
// along which it is smaller than the stage, never panned past its edge
// along one where it is larger.
func (v *view) clamp(nw, nh, sw, sh float32) {
	s := v.scale()
	for i, d := range [2][2]float32{{nw * s, sw}, {nh * s, sh}} {
		room := max(d[0]-d[1], 0) / 2
		v.pan[i] = min(max(v.pan[i], -room), room)
	}
}

// zoomAt scales what is on screen by factor, keeping the picture point under
// q (a stage point from the stage's centre) where it is.
func (v *view) zoomAt(factor float32, q [2]float32, nw, nh, sw, sh float32) {
	if factor <= 0 || math.IsNaN(float64(factor)) || math.IsInf(float64(factor), 0) {
		return
	}
	old := v.scale()
	next := limit(old, factor)
	if next == old {
		return
	}
	for i := range v.pan {
		v.pan[i] = q[i] - (q[i]-v.pan[i])*next/old
	}
	v.zoom, v.fit = next, false
	v.clamp(nw, nh, sw, sh)
}

// actualAt shows the original pixels with the picture point under q in the
// middle of the stage (the double click's behaviour).
func (v *view) actualAt(q [2]float32, nw, nh, sw, sh float32) {
	old := v.scale()
	for i := range v.pan {
		v.pan[i] = -(q[i] - v.pan[i]) / old
	}
	v.zoom, v.fit = 1, false
	v.clamp(nw, nh, sw, sh)
}

// toggleAt switches between fitting and the original size (double click,
// smart zoom).
func (v *view) toggleAt(q [2]float32, nw, nh, sw, sh float32) {
	if v.fit {
		v.actualAt(q, nw, nh, sw, sh)
		return
	}
	v.reset()
}

// panBy moves the picture by d pixels, within the clamp.
func (v *view) panBy(d [2]float32, nw, nh, sw, sh float32) {
	v.pan[0] += d[0]
	v.pan[1] += d[1]
	v.clamp(nw, nh, sw, sh)
}

// bucket is the decode size for a picture shown with longest side px: the
// next power of two from 512, so a pinch asks for a few decodes, not one per
// frame. 0 means the whole picture (the bucket reaches its own size).
func bucket(px, natural int32) int32 {
	b := int32(512)
	for b < px {
		b <<= 1
	}
	if b >= natural {
		return 0
	}
	return b
}
