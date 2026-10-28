// Ported from whatsapp-rust 5e72baf wacore/src/voip/mlow/red.rs, MIT.
//
// MLow RED ("SplitRed") depacketization: the OUTERMOST wire layer of a WhatsApp MLow RTP audio
// payload.
//
// **The payload type selects this, not the redundancy level.** The shipped client applies the
// wrapper to everything it sends on the `mlow-red-1` payload type, and a redundancy level of zero
// still gets one: a single `0x00` marker byte and then the frame. Only a payload arriving on the
// bare MLow payload type is unwrapped, and applying this to one would misread its TOC (whose high
// bit is set) as a redundant block header.
//
// The engine keys off the payload type for exactly that reason; the level is a bitrate decision,
// not a framing one, and treating it as framing means a zero-redundancy stream on the RED payload
// type loses its first byte to the TOC.
//
// On-wire (N = redundancy): `[ red_hdr[0..N] (2B each) ][ main_marker (1B) ][ red_payloads ][ main_payload ]`.
// `red_hdr[i]`: byte0 = `0x80 | (time_code & 0x7f)`, byte1 = payload size. `main_marker`: high bit
// clear, low 7 bits = main time offset. It is not RFC 2198: no block carries a payload type, and
// every block is decoded by the codec the payload type already selected.
//
// The Rust `log::debug!` call is not ported (no logger in this package).

package mlow

// mlowFrame is one frame extracted from a SplitRed payload: raw MLow frame bytes (TOC + body) plus
// RED metadata. `data` borrows the input payload (no copy).
type mlowFrame struct {
	data     []byte
	timeCode uint8
	isMain   bool
}

type redError uint8

const (
	redErrorPktSizeZero redError = iota + 1
	redErrorHeaderTooShort
	redErrorRedundantTooShort
	redErrorMainTooShort
)

func (e redError) Error() string {
	switch e {
	case redErrorPktSizeZero:
		return "PktSizeZero"
	case redErrorHeaderTooShort:
		return "HeaderTooShort"
	case redErrorRedundantTooShort:
		return "RedundantTooShort"
	case redErrorMainTooShort:
		return "MainTooShort"
	}
	return "redError"
}

type redBlock struct {
	code uint8
	size uint8
}

// depackSplitRed parses a SplitRed RED packet into its frames (redundant blocks in header order,
// then the main frame last). Errors on malformed RED inputs. Only call when RED was negotiated: a
// bare MLow frame would be misparsed here (an active-voice TOC like 0x50 has its high bit clear
// and reads as a 0-redundancy main marker, not an error), so for a bare-frame stream feed the
// whole RTP payload as one MLow frame instead.
//
// A non-nil error is always a redError.
func depackSplitRed(p []byte) ([]mlowFrame, error) {
	var s redScratch
	return depackSplitRedInto(&s, p)
}

// redScratch holds the two Vecs Rust `depack_split_red` allocates per call (the header blocks and
// the returned frames), so a decoder can reuse them.
type redScratch struct {
	red    []redBlock
	frames []mlowFrame
}

// depackSplitRedInto is [depackSplitRed] building into `s`'s buffers. The returned frames alias
// `s.frames` and stay valid until the next call with the same scratch.
func depackSplitRedInto(s *redScratch, p []byte) ([]mlowFrame, error) {
	n := len(p)
	if n == 0 {
		return nil, redErrorPktSizeZero
	}
	red := s.red[:0]
	cur := 0
	rem := n
	for {
		if rem == 0 {
			return nil, redErrorHeaderTooShort
		}
		b0 := p[cur]
		if b0 < 0x80 {
			// main marker (high bit clear) terminates the header run
			if rem <= 1 {
				return nil, redErrorMainTooShort
			}
			break
		}
		if rem <= 2 {
			return nil, redErrorRedundantTooShort
		}
		size := p[cur+1]
		if int(size)+2 >= rem {
			return nil, redErrorRedundantTooShort
		}
		red = append(red, redBlock{
			code: b0 & 0x7f,
			size: size,
		})
		cur += 2
		rem -= int(size) + 2
	}

	s.red = red
	mainCode := p[cur] & 0x7f
	cur++

	frames := s.frames[:0]
	for _, r := range red {
		frames = append(frames, mlowFrame{
			data:     p[cur : cur+int(r.size)],
			timeCode: r.code,
			isMain:   false,
		})
		cur += int(r.size)
	}
	mainSize := rem - 1 // total - header_size - sum(redundant sizes)
	frames = append(frames, mlowFrame{
		data:     p[cur : cur+mainSize],
		timeCode: mainCode,
		isMain:   true,
	})
	s.frames = frames
	return frames, nil
}
