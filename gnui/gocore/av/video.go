package av

import (
	"encoding/binary"
	"fmt"
	"math"
	"time"
)

// Video is what an MP4's header says about it: what a message sending it
// carries, so it shows as a video before it is downloaded
// (zf:src/mp4_frames.rs:89).
type Video struct {
	// Width and Height are the video track's (its avc1 entry's, else its
	// tkhd's), swapped when the track's matrix turns it a quarter, so they
	// match the upright poster.
	Width, Height uint32
	// Seconds is the movie's length, rounded.
	Seconds uint32
	// Poster is when the poster frame shows: about a second in (openings
	// are often black), within the first sixty frames.
	Poster time.Duration
}

// InspectVideo reads an MP4's size, length and poster time from its moov
// box, as the Rust core's poster did with the mp4 crate
// (zf:src/mp4_frames.rs:99). No sample is decoded.
func InspectVideo(data []byte) (Video, error) {
	moov, ok := child(data, "moov")
	if !ok {
		return Video{}, fmt.Errorf("%w: no moov box", ErrVideo)
	}
	mvhd, _ := child(moov, "mvhd")
	scale, length, ok := timing(mvhd)
	if !ok {
		return Video{}, fmt.Errorf("%w: no mvhd box", ErrVideo)
	}
	var v Video
	found := false
	each(moov, func(typ string, trak []byte) bool {
		if typ != "trak" {
			return true
		}
		mdia, _ := child(trak, "mdia")
		if hdlr, ok := child(mdia, "hdlr"); !ok || len(hdlr) < 12 || string(hdlr[8:12]) != "vide" {
			return true
		}
		found = true
		minf, _ := child(mdia, "minf")
		stbl, _ := child(minf, "stbl")
		tkhd, _ := child(trak, "tkhd")
		v.Width, v.Height = trackSize(tkhd, stbl)
		v.Poster = posterTime(mdia, stbl)
		return false
	})
	if !found {
		return Video{}, fmt.Errorf("%w: no video track", ErrVideo)
	}
	if v.Width == 0 || v.Height == 0 {
		return Video{}, fmt.Errorf("%w: no picture size", ErrVideo)
	}
	// The mp4 crate's length is in whole milliseconds.
	if scale > 0 {
		v.Seconds = uint32(min(math.Round(float64(length*1000/scale)/1000), math.MaxUint32))
	}
	return v, nil
}

// trackSize is the size of a video track: its avc1 entry's, else its tkhd's
// (16.16, the integer part), as the mp4 crate's width and height
// (zf:src/mp4_frames.rs:106). It is swapped when the tkhd matrix turns a
// quarter.
func trackSize(tkhd, stbl []byte) (w, h uint32) {
	at := 4 + 20 // version and flags; times, track ID, reserved, duration
	if len(tkhd) > 0 && tkhd[0] == 1 {
		at = 4 + 32
	}
	at += 16 // reserved, layer, alternate group, volume, reserved
	if len(tkhd) < at+44 {
		return 0, 0
	}
	m := tkhd[at : at+36]
	w, h = binary.BigEndian.Uint32(tkhd[at+36:])>>16, binary.BigEndian.Uint32(tkhd[at+40:])>>16
	if stsd, ok := child(stbl, "stsd"); ok && len(stsd) >= 8 {
		if avc1, ok := child(stsd[8:], "avc1"); ok && len(avc1) >= 28 {
			w, h = uint32(binary.BigEndian.Uint16(avc1[24:])), uint32(binary.BigEndian.Uint16(avc1[26:]))
		}
	}
	if binary.BigEndian.Uint32(m[0:]) == 0 && binary.BigEndian.Uint32(m[16:]) == 0 {
		w, h = h, w
	}
	return w, h
}

// posterTime is when the Rust core's poster frame shows: it decoded the
// first frame-rate frames (whole frames a second, the mp4 crate's), one to
// sixty, and kept the last (zf:src/mp4_frames.rs:109).
func posterTime(mdia, stbl []byte) time.Duration {
	mdhd, _ := child(mdia, "mdhd")
	scale, length, ok := timing(mdhd)
	count := sampleCount(stbl)
	if !ok || scale == 0 || count == 0 {
		return 0
	}
	ms := length * 1000 / scale
	if ms == 0 {
		return 0
	}
	frames := min(max(count*1000/ms, 1), 60, count)
	return time.Duration(float64(frames-1) * float64(length) / float64(count) / float64(scale) * float64(time.Second))
}

// sampleCount is the number of samples an stbl box's stsz (or stz2) lists.
func sampleCount(stbl []byte) uint64 {
	for _, typ := range []string{"stsz", "stz2"} {
		if b, ok := child(stbl, typ); ok && len(b) >= 12 {
			return uint64(binary.BigEndian.Uint32(b[8:]))
		}
	}
	return 0
}

// timing is the timescale and duration of an mvhd or mdhd box.
func timing(b []byte) (scale, length uint64, ok bool) {
	switch {
	case len(b) >= 32 && b[0] == 1: // version 1: 64-bit times
		return uint64(binary.BigEndian.Uint32(b[20:])), binary.BigEndian.Uint64(b[24:]), true
	case len(b) >= 20 && b[0] == 0:
		return uint64(binary.BigEndian.Uint32(b[12:])), uint64(binary.BigEndian.Uint32(b[16:])), true
	}
	return 0, 0, false
}

// child is the body of the first box of this type among b's boxes.
func child(b []byte, typ string) (body []byte, ok bool) {
	each(b, func(t string, inner []byte) bool {
		if t == typ {
			body, ok = inner, true
			return false
		}
		return true
	})
	return body, ok
}

// each calls fn with the type and body of each box in b, in order, until it
// returns false. A box that runs past b ends the walk.
func each(b []byte, fn func(typ string, body []byte) bool) {
	for len(b) >= 8 {
		size, head := uint64(binary.BigEndian.Uint32(b)), uint64(8)
		switch size {
		case 0: // to the end
			size = uint64(len(b))
		case 1: // a 64-bit size follows the type
			if len(b) < 16 {
				return
			}
			size, head = binary.BigEndian.Uint64(b[8:]), 16
		}
		if size < head || size > uint64(len(b)) {
			return
		}
		if !fn(string(b[4:8]), b[head:size]) {
			return
		}
		b = b[size:]
	}
}
