package video

import (
	"bytes"
	"testing"

	"zapfast/abi"
	"zapfast/core"
)

// The layout gnui/core.Frame describes: Y of H rows, then U and V of
// (H+1)/2 rows, Stride[i] bytes a row, one after the other.
func TestI420Layout(t *testing.T) {
	for _, s := range [][2]int{{640, 360}, {321, 241}, {1, 1}, {2, 3}} {
		w, h := s[0], s[1]
		p := NewI420(w, h)
		c := (w + 1) / 2
		if p.Stride != [3]int{w, c, c} {
			t.Fatalf("%dx%d strides %v", w, h, p.Stride)
		}
		f := p.Core(7)
		if f.Format != core.FrameI420 || f.W != w || f.H != h || f.PTS != 7 {
			t.Fatalf("Core = %+v", f)
		}
		// What core.Core.Frame computes for the C side's frame.
		n := f.Stride[0]*f.H + (f.Stride[1]+f.Stride[2])*((f.H+1)/2)
		if n != len(f.Px) || n != I420Len(w, h) {
			t.Fatalf("%dx%d: %d bytes, the layout says %d", w, h, len(f.Px), n)
		}
		// The planes are where the layout says, in order.
		p.Fill(1, 2, 3)
		yEnd := w * h
		uEnd := yEnd + c*((h+1)/2)
		if bytes.Count(f.Px[:yEnd], []byte{1}) != yEnd ||
			bytes.Count(f.Px[yEnd:uEnd], []byte{2}) != uEnd-yEnd ||
			bytes.Count(f.Px[uEnd:], []byte{3}) != len(f.Px)-uEnd {
			t.Fatalf("%dx%d: the planes are not Y, U, V in order", w, h)
		}
		if !p.Valid() {
			t.Fatalf("%dx%d not valid", w, h)
		}
	}
	if (&I420{W: 4, H: 4, Stride: [3]int{4, 2, 2}, Px: make([]byte, 23)}).Valid() {
		t.Fatal("a short buffer is valid")
	}
}

func TestNV12RoundTrip(t *testing.T) {
	src := synthetic(64, 48, 3)
	ys, uvs := 80, 72 // padded rows, as a pixel buffer has
	y := make([]byte, ys*48)
	uv := make([]byte, uvs*24)
	src.ToNV12(y, ys, uv, uvs)
	if uv[0] != src.U()[0] || uv[1] != src.V()[0] {
		t.Fatal("CbCr is not interleaved Cb first")
	}
	back := NewI420(64, 48)
	FromNV12(back, y, ys, uv, uvs)
	if !bytes.Equal(back.Px, src.Px) {
		t.Fatal("NV12 round trip changed the picture")
	}
}

func TestPacked(t *testing.T) {
	src := synthetic(10, 6, 0)
	wide := &I420{W: 10, H: 6, Stride: [3]int{16, 8, 8}, Px: make([]byte, 16*6+16*3)}
	copyPlane(wide.Y(), 16, src.Y(), 10, 10, 6)
	copyPlane(wide.U(), 8, src.U(), 5, 5, 3)
	copyPlane(wide.V(), 8, src.V(), 5, 5, 3)
	if p := wide.Packed(); !bytes.Equal(p.Px, src.Px) || p.Stride != src.Stride {
		t.Fatal("Packed did not repack the planes")
	}
	if src.Packed() != src {
		t.Fatal("a packed picture was copied")
	}
}

func TestScale(t *testing.T) {
	p := NewI420(8, 4)
	y := p.Y()
	for r := range 4 {
		for c := range 8 {
			y[r*8+c] = byte(c * 10)
		}
	}
	q := p.Scale(4, 2)
	if q.W != 4 || q.H != 2 || len(q.Px) != I420Len(4, 2) {
		t.Fatalf("scaled to %dx%d", q.W, q.H)
	}
	// Each output sample averages two columns: (0+10)/2 = 5, …
	for c, want := range []byte{5, 25, 45, 65} {
		if q.Y()[c] != want {
			t.Fatalf("column %d = %d, want %d", c, q.Y()[c], want)
		}
	}
	up := q.Scale(8, 4)
	if up.W != 8 || up.Y()[1] != 5 {
		t.Fatalf("scaling up: %d", up.Y()[1])
	}
	if p.Scale(8, 4) != p {
		t.Fatal("the same size was copied")
	}
}

func TestRotate(t *testing.T) {
	p := synthetic(6, 4, 1)
	q := p.Rotate(1)
	if q.W != 4 || q.H != 6 {
		t.Fatalf("a quarter turn is %dx%d", q.W, q.H)
	}
	// Clockwise: the top-left goes to the top-right.
	if q.Y()[0*q.Stride[0]+3] != p.Y()[0] || q.Y()[5*q.Stride[0]+3] != p.Y()[5] {
		t.Fatal("not turned clockwise")
	}
	full := p.Rotate(1).Rotate(1).Rotate(1).Rotate(1)
	if !bytes.Equal(full.Px, p.Px) {
		t.Fatal("four quarter turns are not the picture")
	}
	if p.Rotate(2).Rotate(2).Y()[7] != p.Y()[7] || p.Rotate(4) != p {
		t.Fatal("half turns")
	}
}

func TestFitEven(t *testing.T) {
	for _, c := range []struct{ w, h, ww, wh int }{
		{1920, 1080, 1280, 720},
		{1080, 1920, 720, 1280},
		{640, 480, 640, 480},
		{1281, 721, 1278, 720},
		{0, 0, 1280, 720},
		{1440, 1080, 960, 720},
		{641, 361, 640, 360},
	} {
		if w, h := FitEven(c.w, c.h, 1280, 720); w != c.ww || h != c.wh {
			t.Errorf("FitEven(%d, %d) = %d, %d, want %d, %d", c.w, c.h, w, h, c.ww, c.wh)
		}
	}
}

func TestAnnexB(t *testing.T) {
	b := []byte{0, 0, 0, 1, 0x67, 1, 2, 0, 0, 1, 0x68, 3, 0, 0, 0, 0, 1, 0x65, 4, 5, 0}
	us := SplitAnnexB(b)
	if len(us) != 3 || NALType(us[0]) != NALSPS || NALType(us[1]) != NALPPS || NALType(us[2]) != NALIDR {
		t.Fatalf("units % x", us)
	}
	if !bytes.Equal(us[0], []byte{0x67, 1, 2}) || !bytes.Equal(us[2], []byte{0x65, 4, 5}) {
		t.Fatalf("unit bytes % x", us)
	}
	if !IsKeyframe(b) || IsKeyframe([]byte{0, 0, 1, 0x41, 9}) {
		t.Fatal("IsKeyframe")
	}
	var avcc []byte
	for _, u := range us {
		avcc = appendAVCC(avcc, u)
	}
	back, ok := avccToAnnexB(nil, avcc, 4)
	if !ok || len(SplitAnnexB(back)) != 3 {
		t.Fatal("AVCC round trip")
	}
	if _, ok := avccToAnnexB(nil, []byte{0, 0, 0, 9, 1}, 4); ok {
		t.Fatal("a length past the sample was taken")
	}
}

func liveUpdate(gen uint64, camera, remote bool, members ...abi.Participant) *abi.CallUpdate {
	u := &abi.CallUpdate{Generation: gen, Phase: abi.CallPhaseActive, CameraOn: camera, RemoteVideo: remote}
	if len(members) > 0 {
		u.HasGroup = true
		u.Group.Participants = members
	}
	return u
}

func TestFrames(t *testing.T) {
	f := NewFrames()
	pic := NewI420(4, 4)
	f.PutRemote(pic)
	if _, ok := f.Frame(core.StreamRemote); ok {
		t.Fatal("a frame kept with no call live")
	}
	f.OnUpdate(liveUpdate(1, true, true))
	f.PutLocal(pic)
	f.PutRemote(pic)
	l, ok := f.Frame(core.StreamLocal)
	if !ok || l.W != 4 || l.Format != core.FrameI420 {
		t.Fatalf("local %+v %v", l, ok)
	}
	r, _ := f.Frame(core.StreamRemote)
	if r.PTS <= l.PTS {
		t.Fatalf("PTS %d then %d: not increasing", l.PTS, r.PTS)
	}
	if _, ok := f.Frame(core.StreamLocal); ok {
		t.Fatal("a held stream handed out again")
	}
	f.ReleaseFrame(core.StreamLocal)
	if l2, ok := f.Frame(core.StreamLocal); !ok || l2.PTS != l.PTS {
		t.Fatal("the same frame after release")
	}
	f.ReleaseFrame(core.StreamLocal)
	f.ReleaseFrame(core.StreamRemote)

	// The camera off drops ours; the peer's stays.
	f.OnUpdate(liveUpdate(1, false, true))
	if _, ok := f.Frame(core.StreamLocal); ok {
		t.Fatal("our picture outlived the camera")
	}
	if _, ok := f.Frame(core.StreamRemote); !ok {
		t.Fatal("the peer's picture went with our camera")
	}
	f.ReleaseFrame(core.StreamRemote)

	// Group members follow their id through a reorder.
	a := abi.Participant{ID: []byte("a"), Video: true}
	b := abi.Participant{ID: []byte("b"), Video: true}
	f.OnUpdate(liveUpdate(1, false, false, a, b))
	pa, pb := NewI420(2, 2), NewI420(6, 6)
	f.PutMember("a", pa)
	f.PutMember("b", pb)
	if m, _ := f.Frame(core.StreamMember + 1); m.W != 6 {
		t.Fatalf("member 1 is %d wide", m.W)
	}
	f.ReleaseFrame(core.StreamMember + 1)
	f.OnUpdate(liveUpdate(1, false, false, b, a))
	if m, _ := f.Frame(core.StreamMember + 1); m.W != 2 {
		t.Fatalf("after the reorder member 1 is %d wide", m.W)
	}
	f.ReleaseFrame(core.StreamMember + 1)
	b.Video = false
	f.OnUpdate(liveUpdate(1, false, false, b, a))
	if _, ok := f.Frame(core.StreamMember); ok {
		t.Fatal("a member's picture outlived their video")
	}

	// Another call's end clears nothing; this one's clears everything.
	f.PutMember("a", pa)
	f.OnUpdate(&abi.CallUpdate{Generation: 9, Phase: abi.CallPhaseEnded})
	if _, ok := f.Frame(core.StreamMember + 1); !ok {
		t.Fatal("another call's end cleared this one")
	}
	f.ReleaseFrame(core.StreamMember + 1)
	f.OnUpdate(&abi.CallUpdate{Generation: 1, Phase: abi.CallPhaseEnded})
	if _, ok := f.Frame(core.StreamMember + 1); ok {
		t.Fatal("the call's end kept a picture")
	}
	f.PutLocal(pic)
	if _, ok := f.Frame(core.StreamLocal); ok {
		t.Fatal("a picture kept after the end")
	}
	f.PutLocal(&I420{})
}
