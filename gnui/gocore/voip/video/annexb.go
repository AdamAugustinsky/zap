package video

import "encoding/binary"

// H.264 NAL unit types this package looks at.
const (
	NALSlice = 1 // a coded slice of a delta picture
	NALIDR   = 5 // a coded slice of a keyframe
	NALSEI   = 6 // supplemental enhancement information
	NALSPS   = 7 // a sequence parameter set
	NALPPS   = 8 // a picture parameter set
	NALAUD   = 9 // an access unit delimiter
)

// aud is the access unit delimiter every access unit Encoder makes starts
// with (primary_pic_type 7, any slice), as the Rust encoder wrote one, so
// the peer's depacketizer sees the boundaries.
var aud = []byte{0, 0, 0, 1, NALAUD, 0xF0}

// SplitAnnexB is the NAL units of an Annex-B access unit, without their
// start codes (three or four bytes). Empty units are skipped. The units
// share b.
func SplitAnnexB(b []byte) [][]byte {
	var units [][]byte
	start := -1
	for i := 0; i+2 < len(b); {
		if b[i] == 0 && b[i+1] == 0 && b[i+2] == 1 {
			if start >= 0 {
				units = appendUnit(units, b[start:i])
			}
			i += 3
			start = i
			continue
		}
		i++
	}
	if start >= 0 {
		units = appendUnit(units, b[start:])
	} else if len(b) > 0 {
		// No start code: one bare unit.
		units = appendUnit(units, b)
	}
	return units
}

// appendUnit keeps u without the zero bytes that belong to the next start
// code (a four-byte one, or trailing_zero_8bits).
func appendUnit(units [][]byte, u []byte) [][]byte {
	for len(u) > 0 && u[len(u)-1] == 0 {
		u = u[:len(u)-1]
	}
	if len(u) == 0 {
		return units
	}
	return append(units, u)
}

// NALType is u's nal_unit_type; 0 for an empty unit.
func NALType(u []byte) int {
	if len(u) == 0 {
		return 0
	}
	return int(u[0] & 0x1F)
}

// IsKeyframe says whether the Annex-B access unit b carries an IDR slice.
func IsKeyframe(b []byte) bool {
	for _, u := range SplitAnnexB(b) {
		if NALType(u) == NALIDR {
			return true
		}
	}
	return false
}

// appendAnnexB appends u to dst behind a four-byte start code.
func appendAnnexB(dst, u []byte) []byte {
	dst = append(dst, 0, 0, 0, 1)
	return append(dst, u...)
}

// avccToAnnexB appends the length-prefixed NAL units of an AVCC sample
// (lenSize bytes a length, big-endian) to dst as Annex-B. ok is false when a
// length runs past the sample.
func avccToAnnexB(dst, sample []byte, lenSize int) ([]byte, bool) {
	for len(sample) > 0 {
		if len(sample) < lenSize {
			return dst, false
		}
		n := 0
		for _, c := range sample[:lenSize] {
			n = n<<8 | int(c)
		}
		sample = sample[lenSize:]
		if n > len(sample) {
			return dst, false
		}
		dst = appendAnnexB(dst, sample[:n])
		sample = sample[n:]
	}
	return dst, true
}

// appendAVCC appends u to dst behind its four-byte big-endian length.
func appendAVCC(dst, u []byte) []byte {
	dst = binary.BigEndian.AppendUint32(dst, uint32(len(u)))
	return append(dst, u...)
}
