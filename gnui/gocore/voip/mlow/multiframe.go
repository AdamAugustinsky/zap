// Ported from whatsapp-rust 5e72baf wacore/src/voip/mlow/multiframe.rs, MIT.
//
// MLow's multiframe envelope: several complete MLow packets carried in one RTP payload.
//
// This is how the real client reaches a packet longer than its frame length. Its encoder caches a
// fixed three blocks of 20 ms, so a `frame_length_ms` of 120 does not produce one 120 ms block: it
// produces two 60 ms blocks aggregated here. A decoder that does not implement this reads the
// envelope's first byte as a TOC, finds bit 7 set, calls it a SID, and emits comfort noise for
// every packet of the call. The call goes completely silent with no error anywhere, which is worse
// than the bug this port was written to fix, because at least that one logged something.
//
// The envelope indicator is `(b & 0xC0) != 0xC0 && (b & 0x82) == 0x82`. That point is unreachable
// as a real TOC by construction: bit 7 is SID and bit 1 is FEC, and the encoder asserts a SID frame
// never carries FEC, so no frame it emits can have both set. The collision is deliberate.

package mlow

import (
	"fmt"
	"math/bits"
)

// maxFrames is the number of frames the real parser accepts in one envelope. Above this it refuses
// the packet outright.
const maxFrames = 18

// countMask is the bits of the count byte that hold the frame count; the rest are the padding flag
// and one the client ignores.
const countMask uint8 = 0x3f

// paddingFlag: Opus-style padding follows the count byte when this is set.
const paddingFlag uint8 = 0x40

type multiframeErrorKind uint8

const (
	// the envelope ended before its header did
	multiframeErrorKindTruncated multiframeErrorKind = iota + 1
	// frame count {0} is outside 1..=18
	multiframeErrorKindBadFrameCount
	// a declared frame length runs past the end of the envelope
	multiframeErrorKindLengthOverrun
	// a sub-frame is empty
	multiframeErrorKindEmptySubFrame
)

// multiframeError is why an envelope could not be split. `count` is the payload of
// BadFrameCount (zero for the other kinds), so values compare like the Rust enum.
type multiframeError struct {
	kind  multiframeErrorKind
	count int
}

var (
	multiframeErrorTruncated     = multiframeError{kind: multiframeErrorKindTruncated}
	multiframeErrorLengthOverrun = multiframeError{kind: multiframeErrorKindLengthOverrun}
	multiframeErrorEmptySubFrame = multiframeError{kind: multiframeErrorKindEmptySubFrame}
)

func multiframeErrorBadFrameCount(n int) multiframeError {
	return multiframeError{kind: multiframeErrorKindBadFrameCount, count: n}
}

func (e multiframeError) Error() string {
	switch e.kind {
	case multiframeErrorKindTruncated:
		return "the envelope ended before its header did"
	case multiframeErrorKindBadFrameCount:
		return fmt.Sprintf("frame count %d is outside 1..=18", e.count)
	case multiframeErrorKindLengthOverrun:
		return "a declared frame length runs past the end of the envelope"
	case multiframeErrorKindEmptySubFrame:
		return "a sub-frame is empty"
	}
	return "multiframe error"
}

// isMultiframe reports whether this payload is a multiframe envelope rather than a single MLow
// frame.
//
// Checked before the TOC is interpreted, because under the TOC grammar these bytes read as a SID
// and would be silently turned into comfort noise.
func isMultiframe(payload []byte) bool {
	// Two bytes minimum: the indicator and the count. The real parser requires the same.
	return len(payload) >= 2 && payload[0]&0xC0 != 0xC0 && payload[0]&0x82 == 0x82
}

// multiframeCheckedAdd is usize::checked_add.
func multiframeCheckedAdd(a, b int) (int, bool) {
	s, carry := bits.Add64(uint64(a), uint64(b), 0)
	if carry != 0 || s > uint64(^uint(0)>>1) {
		return 0, false
	}
	return int(s), true
}

// splitMultiframe splits one envelope into the complete MLow packets it carries, in transmission
// order.
//
// Each element is a whole packet with its own TOC, decodable exactly as if it had arrived alone.
// The caller decodes them in order and concatenates: the envelope's RTP timestamp belongs to the
// **last** sub-frame, and the earlier ones run backwards from it, so playing them in array order
// is what puts the audio back in time order.
//
// Borrows the input; no allocation beyond the returned slice of slices, which is bounded by
// [maxFrames]. A non-nil error is always a multiframeError.
func splitMultiframe(payload []byte) ([][]byte, error) {
	return splitMultiframeInto(nil, payload)
}

// splitMultiframeInto is [splitMultiframe] appending the sub-frames to `dst[:0]` instead of a
// fresh Vec; with `cap(dst) >= maxFrames` it does not allocate.
func splitMultiframeInto(dst [][]byte, payload []byte) ([][]byte, error) {
	if len(payload) < 2 {
		return nil, multiframeErrorTruncated
	}
	countByte := payload[1]
	frames := int(countByte & countMask)
	if frames == 0 || frames > maxFrames {
		return nil, multiframeErrorBadFrameCount(frames)
	}
	rest := payload[2:]

	// Opus padding: a run of 255s, then a final byte giving the remaining padding length. The bytes
	// themselves are meaningless; only the accounting matters, and it has to not overrun.
	padding := 0
	if countByte&paddingFlag != 0 {
		for {
			if len(rest) == 0 {
				return nil, multiframeErrorTruncated
			}
			b := rest[0]
			rest = rest[1:]
			var ok bool
			if b == 255 {
				if padding, ok = multiframeCheckedAdd(padding, 254); !ok {
					return nil, multiframeErrorLengthOverrun
				}
			} else {
				if padding, ok = multiframeCheckedAdd(padding, int(b)); !ok {
					return nil, multiframeErrorLengthOverrun
				}
				break
			}
		}
	}

	// One explicit length per frame except the last, which takes whatever is left. Same one-or-two
	// byte encoding standard Opus uses.
	var lengths [maxFrames]int
	declared := 0
	for i := 0; i < frames-1; i++ {
		if len(rest) == 0 {
			return nil, multiframeErrorTruncated
		}
		first := rest[0]
		next := rest[1:]
		var length int
		if first < 252 {
			rest = next
			length = int(first)
		} else {
			if len(next) == 0 {
				return nil, multiframeErrorTruncated
			}
			second := next[0]
			rest = next[1:]
			length = int(first) + int(second)*4
		}
		lengths[i] = length
		var ok bool
		if declared, ok = multiframeCheckedAdd(declared, length); !ok {
			return nil, multiframeErrorLengthOverrun
		}
	}

	if len(rest) < padding {
		return nil, multiframeErrorLengthOverrun
	}
	bodies := len(rest) - padding
	if bodies < declared {
		return nil, multiframeErrorLengthOverrun
	}
	last := bodies - declared
	if last == 0 {
		return nil, multiframeErrorEmptySubFrame
	}
	lengths[frames-1] = last

	out := dst[:0]
	if cap(out) < frames {
		out = make([][]byte, 0, frames)
	}
	cursor := rest
	for _, length := range lengths[:frames] {
		if length == 0 {
			return nil, multiframeErrorEmptySubFrame
		}
		if length > len(cursor) {
			return nil, multiframeErrorLengthOverrun
		}
		out = append(out, cursor[:length])
		cursor = cursor[length:]
	}
	return out, nil
}
