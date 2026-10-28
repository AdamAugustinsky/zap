package calls

import (
	"time"

	"gonativeui/gpu"
	"gonativeui/ui"
	"gonativeui/widget/base"

	"zapfast/abi"
	"zapfast/core"
	"zapfast/ui/screen"
)

// Video is the only call media that crosses to Go (D33): the core keeps the
// newest I420 frame of each stream until it is released (core.Frames, over
// zf_frame_acquire and zf_frame_release). The call screen takes one per
// stream it draws, per frame, copies its planes into the stream's YUV texture
// (gpu.Renderer.StageYUV, so a frame is copied before it is given back) and
// draws that texture covering the tile.

// frames is the video source of the call's account: its core, nil while the
// core is still opening.
func (h *hub) frames() core.Frames {
	if a := h.account(); a != nil {
		if f, ok := a.Conn.(core.Frames); ok {
			return f
		}
	}
	return nil
}

// streamOn is whether the call has a picture on stream now: ours while our
// camera is on, the peer's while theirs is, and each member's whose camera
// is on.
func streamOn(u *abi.CallUpdate, stream uint32) bool {
	switch {
	case !u.Video && !u.HasGroup:
		return false
	case stream == core.StreamLocal:
		return u.CameraOn
	case stream == core.StreamRemote:
		return !u.HasGroup && u.RemoteVideo
	}
	i := int(stream - core.StreamMember)
	return u.HasGroup && i < len(u.Group.Participants) && u.Group.Participants[i].Video
}

// frameGap is how soon the window asks for the next frame while a video
// shows: a call's camera runs at up to 30 frames a second.
const frameGap = 33 * time.Millisecond

// maxStreams bounds the textures: the local and remote streams and the
// members of a group call.
const maxStreams = 64

// yuvSink is what the textures are made on: the window's renderer
// (*gpu.Renderer), or a spy in tests.
type yuvSink interface {
	NewYUVTexture(f gpu.YUVFormat, w, h int, sp gpu.YUVSpace) (gpu.TextureID, error)
	StageYUV(t gpu.TextureID, im *gpu.YUVImage)
	FreeTexture(t gpu.TextureID)
}

// streamTex is one stream's texture: the picture last copied into it.
type streamTex struct {
	tex    gpu.TextureID
	w, h   int
	pts    int64
	staged bool // a frame is in it (pts is its)
}

// videoTex are the call's textures, one per stream it has drawn, on the
// renderer of the window that drew them: reused while the frame size holds
// and not written again while the frame is the one in it.
type videoTex struct {
	rp   *gpu.Renderer
	sink yuvSink
	wrap func(yuvSink) yuvSink // tests: wraps the renderer
	st   []streamTex
	img  gpu.YUVImage // the planes of the frame being copied
}

// at is stream's slot, made when first asked for.
func (v *videoTex) at(stream uint32) *streamTex {
	for int(stream) >= len(v.st) {
		v.st = append(v.st, streamTex{})
	}
	return &v.st[stream]
}

// use makes r the renderer the textures are on; textures made on another
// one (it was destroyed with its windows) are forgotten.
func (v *videoTex) use(r *gpu.Renderer) {
	if v.rp != r {
		v.st = v.st[:0]
		v.rp = r
		v.sink = nil
	}
	if v.sink == nil {
		v.sink = r
		if v.wrap != nil {
			v.sink = v.wrap(r)
		}
	}
}

// drop frees stream's texture.
func (v *videoTex) drop(stream uint32) {
	if int(stream) >= len(v.st) {
		return
	}
	st := &v.st[stream]
	if st.tex != 0 && v.sink != nil {
		v.sink.FreeTexture(st.tex)
	}
	*st = streamTex{}
}

// release frees every texture (the call is over, or its window is).
func (v *videoTex) release() {
	for i := range v.st {
		v.drop(uint32(i))
	}
	v.st = v.st[:0]
}

// has is whether stream has a picture to draw.
func (v *videoTex) has(stream uint32) bool {
	return int(stream) < len(v.st) && v.st[stream].tex != 0
}

// size is the size of the last picture of stream (0, 0 before one).
func (v *videoTex) size(stream uint32) (w, h int) {
	if int(stream) < len(v.st) {
		return v.st[stream].w, v.st[stream].h
	}
	return 0, 0
}

// stage copies f into stream's texture: made on the first frame and again
// when the size changes, and written only when f is not the picture already
// in it. The planes are the core's: the Y plane, then U and V, each with its
// own stride (zfcore.h, limited-range BT.601).
func (v *videoTex) stage(stream uint32, f *core.Frame) {
	cw, ch := (f.W+1)/2, (f.H+1)/2
	if f.Format != core.FrameI420 || f.W <= 0 || f.H <= 0 || f.Stride[0] < f.W || f.Stride[1] < cw || f.Stride[2] < cw {
		return
	}
	yEnd := f.Stride[0] * f.H
	uEnd := yEnd + f.Stride[1]*ch
	vEnd := uEnd + f.Stride[2]*ch
	if len(f.Px) < vEnd || v.sink == nil {
		return
	}
	st := v.at(stream)
	if st.tex != 0 && (st.w != f.W || st.h != f.H) {
		v.drop(stream)
	}
	if st.tex == 0 {
		tex, err := v.sink.NewYUVTexture(gpu.YUVI420, f.W, f.H, gpu.BT601Video)
		if err != nil {
			return
		}
		*st = streamTex{tex: tex, w: f.W, h: f.H}
	} else if st.staged && st.pts == f.PTS {
		return
	}
	v.img = gpu.YUVImage{
		Y: f.Px[:yEnd], U: f.Px[yEnd:uEnd], V: f.Px[uEnd:vEnd],
		YStride: f.Stride[0], UStride: f.Stride[1], VStride: f.Stride[2],
	}
	v.sink.StageYUV(st.tex, &v.img)
	v.img = gpu.YUVImage{}
	st.pts, st.staged = f.PTS, true
}

// drawVideo draws stream's newest picture as a w × h box at the top left of
// the box being built, placed by fit (the tiles cover theirs, cropped to
// their shape from the middle as WhatsApp's grid does; the peer's fits inside
// the window) with corner radius (pixels), and reports whether it drew one. It is not mirrored. Without a picture the caller draws the
// person's tile instead.
func (h *hub) drawVideo(e *screen.Env, w, ht float32, stream uint32, fit ui.ImageFit, radius float32) bool {
	v := &h.vid
	src := h.frames()
	if src == nil || !h.call.on || !streamOn(&h.call.u, stream) || e.A == nil || e.A.R == nil || stream >= maxStreams {
		v.drop(stream)
		return false
	}
	v.use(e.A.R)
	k, c := e.K.Kit, e.C()
	n := uint64(stream)
	// A picture that does not show (a hidden window, a tile scrolled away)
	// takes no frame and asks for no more.
	if k.ImageShown(n) {
		if f, ok := src.Frame(stream); ok {
			v.stage(stream, &f)
			src.ReleaseFrame(stream)
		}
		c.WakeIn(int64(frameGap))
	}
	st := v.at(stream)
	if st.tex == 0 {
		return false
	}
	c.NextPos(0, 0)
	k.NextSize(ui.Px(w), ui.Px(ht))
	c.NextFlags(base.Floating)
	k.ExternalImage(n, st.tex, st.w, st.h, fit, radius, "")
	return true
}

// fitInside is the largest size of the shape iw × ih that fits in bw × bh.
func fitInside(bw, bh float32, iw, ih int) (w, h float32) {
	if iw <= 0 || ih <= 0 {
		return bw, bh
	}
	w, h = bw, bw*float32(ih)/float32(iw)
	if h > bh {
		w, h = bh*float32(iw)/float32(ih), bh
	}
	return w, h
}
