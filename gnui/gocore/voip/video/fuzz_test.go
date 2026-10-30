package video

// Native fuzz target for the Annex-B reader a received access unit goes
// through before the decoder sees it (codec_darwin.go turns it into an AVCC
// sample with SplitAnnexB and appendAVCC).
//
//	CGO_ENABLED=0 go test ./gocore/voip/video -run '^$' -fuzz '^FuzzAnnexB$' -fuzztime 45s

import (
	"bytes"
	"slices"
	"testing"
	"unsafe"
)

// FuzzAnnexB splits an arbitrary access unit. Every unit is non-empty,
// lies inside the input, follows the one before it, holds no start code
// and does not end in a zero byte; IsKeyframe is exactly "some unit is an
// IDR slice"; and the AVCC sample built from the units converts back to
// the same units, each behind a four-byte start code.
func FuzzAnnexB(f *testing.F) {
	f.Add([]byte{0, 0, 0, 1, 0x67, 0x42, 0, 0, 1, 0x68, 0xce, 0, 0, 1, 0x65, 0x88, 0x84})
	f.Add([]byte{0, 0, 0, 1, NALAUD, 0xf0, 0, 0, 0, 1, 0x41, 0x9a, 0, 0})
	f.Add([]byte{0x65, 1, 2, 3})
	f.Add([]byte{0, 0, 1, 0, 0, 1})
	f.Fuzz(func(t *testing.T, au []byte) {
		units := SplitAnnexB(au)
		base := uintptr(unsafe.Pointer(unsafe.SliceData(au)))
		next := uintptr(0)
		key := false
		for i, u := range units {
			at := uintptr(unsafe.Pointer(unsafe.SliceData(u))) - base
			if len(u) == 0 || at < next || at+uintptr(len(u)) > uintptr(len(au)) {
				t.Fatalf("unit %d (%d bytes at %d) outside the input or out of order", i, len(u), at)
			}
			next = at + uintptr(len(u))
			if bytes.Contains(u, []byte{0, 0, 1}) || u[len(u)-1] == 0 {
				t.Fatalf("unit %d %x holds a start code or a trailing zero", i, u)
			}
			key = key || NALType(u) == NALIDR
		}
		if IsKeyframe(au) != key {
			t.Fatalf("IsKeyframe %v, units say %v", IsKeyframe(au), key)
		}

		var sample, want []byte
		for _, u := range units {
			sample = appendAVCC(sample, u)
			want = appendAnnexB(want, u)
		}
		got, ok := avccToAnnexB(nil, sample, 4)
		if !ok || !bytes.Equal(got, want) {
			t.Fatalf("AVCC round trip: %v", ok)
		}
		if again := SplitAnnexB(got); !slices.EqualFunc(again, units, bytes.Equal) {
			t.Fatalf("re-splitting gives %d units, not %d", len(again), len(units))
		}
	})
}

// FuzzAVCC converts an arbitrary AVCC sample of 1- to 4-byte lengths. On
// success the output is the input's units behind start codes, no more than
// the input plus four bytes a unit; a length past the sample is refused.
func FuzzAVCC(f *testing.F) {
	f.Add([]byte{0, 0, 0, 2, 0x65, 1, 0, 0, 0, 1, 0x41}, uint8(4))
	f.Add([]byte{2, 0x65, 1, 9}, uint8(1))
	f.Add([]byte{0, 0, 0, 0}, uint8(4))
	f.Fuzz(func(t *testing.T, sample []byte, lenSize uint8) {
		n := 1 + int(lenSize%4)
		got, ok := avccToAnnexB(nil, sample, n)
		units := 0
		rest := sample
		valid := true
		for len(rest) > 0 {
			if len(rest) < n {
				valid = false
				break
			}
			l := 0
			for _, c := range rest[:n] {
				l = l<<8 | int(c)
			}
			rest = rest[n:]
			if l > len(rest) {
				valid = false
				break
			}
			rest = rest[l:]
			units++
		}
		if ok != valid {
			t.Fatalf("accepted %v, the sample is valid %v", ok, valid)
		}
		if ok && len(got) > len(sample)+4*units {
			t.Fatalf("%d bytes out of %d", len(got), len(sample))
		}
	})
}
