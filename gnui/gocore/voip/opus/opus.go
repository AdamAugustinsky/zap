// Ported from whatsapp-rust 5e72baf wacore/src/voip/opus_packet.rs, MIT.

// Package opus reads the header of a standard Opus packet (RFC 6716
// section 3.1) without a decoder.
//
// MLow and standard Opus share RTP payload type 120 and their first bytes
// collide: 0x58 is a 120 ms MLow packet under one grammar and a 60 ms SILK
// wideband Opus packet under the other. What resolves it is a second
// statement by the same peer, the RTP timestamp step: a packet whose Opus
// header declares a duration matching the step the peer advances by is
// Opus. The read is strict on purpose, and allocates nothing.
package opus

// Mode is the Opus coding mode a configuration selects.
type Mode uint8

const (
	// Silk is the linear-prediction coder.
	Silk Mode = iota
	// Hybrid is SILK below and CELT above the crossover.
	Hybrid
	// Celt is the transform coder.
	Celt
)

// String is the Rust variant name.
func (m Mode) String() string {
	switch m {
	case Silk:
		return "Silk"
	case Hybrid:
		return "Hybrid"
	case Celt:
		return "Celt"
	}
	return "Mode(?)"
}

// Bandwidth is the declared audio bandwidth.
type Bandwidth uint8

const (
	// Narrow is 4 kHz.
	Narrow Bandwidth = iota
	// Medium is 6 kHz.
	Medium
	// Wide is 8 kHz, the band a 16 kHz WhatsApp audio stream uses.
	Wide
	// SuperWide is 12 kHz.
	SuperWide
	// Full is 20 kHz.
	Full
)

// String is the Rust variant name.
func (b Bandwidth) String() string {
	switch b {
	case Narrow:
		return "Narrow"
	case Medium:
		return "Medium"
	case Wide:
		return "Wide"
	case SuperWide:
		return "SuperWide"
	case Full:
		return "Full"
	}
	return "Bandwidth(?)"
}

// PacketShape is what a well-formed Opus packet header says about itself.
type PacketShape struct {
	Mode      Mode
	Bandwidth Bandwidth
	Stereo    bool
	// Frames chained in the packet, 1..=48.
	Frames uint8
	// FrameDurationUS is the duration of ONE frame in microseconds (CELT
	// 2.5 ms is not a whole number of milliseconds).
	FrameDurationUS uint32
}

// TotalSamples is the number of samples per channel the packet claims to
// carry at clockRate; ok is false when that is not an exact number of
// samples.
func (s PacketShape) TotalSamples(clockRate uint32) (samples uint32, ok bool) {
	totalUS := uint64(s.FrameDurationUS) * uint64(s.Frames)
	if totalUS > 0xffffffff {
		return 0, false
	}
	n := totalUS * uint64(clockRate)
	if n%1_000_000 != 0 {
		return 0, false
	}
	return uint32(n / 1_000_000), true
}

const (
	maxFrames           = 48
	maxPacketDurationUS = 120_000
)

// Shape reads the header of a standard Opus packet. ok is false for
// anything that is not a structurally valid packet: a truncated code-2
// length, an odd body under code 1, a code-3 frame count of zero or past
// 48, padding or VBR lengths that overrun, a CBR body that does not divide,
// or a total duration past 120 ms.
func Shape(payload []byte) (PacketShape, bool) {
	if len(payload) == 0 {
		return PacketShape{}, false
	}
	toc, rest := payload[0], payload[1:]
	mode, bw, frameUS := decodeConfig(toc >> 3)
	var frames uint8
	switch toc & 0x03 {
	case 0:
		frames = 1
	case 1:
		if len(rest)%2 != 0 {
			return PacketShape{}, false
		}
		frames = 2
	case 2:
		if len(rest) == 0 {
			return PacketShape{}, false
		}
		first := rest[0]
		length, headerLen := int(first), 1
		if first >= 252 {
			if len(rest) < 2 {
				return PacketShape{}, false
			}
			length, headerLen = int(first)+int(rest[1])*4, 2
		}
		if length > len(rest)-headerLen {
			return PacketShape{}, false
		}
		frames = 2
	default:
		if len(rest) == 0 {
			return PacketShape{}, false
		}
		countByte := rest[0]
		remaining := rest[1:]
		frames = countByte & 0x3f
		if frames == 0 || frames > maxFrames {
			return PacketShape{}, false
		}
		// Padding is a run of 255s ended by a final byte; each 255 is worth
		// 254 bytes, so the total accumulates.
		padding := 0
		if countByte&0x40 != 0 {
			for {
				if len(remaining) == 0 {
					return PacketShape{}, false
				}
				pad := remaining[0]
				remaining = remaining[1:]
				if pad == 255 {
					padding += 254
				} else {
					padding += int(pad)
					break
				}
			}
		}
		bodies := len(remaining) - padding
		if bodies < 0 {
			return PacketShape{}, false
		}
		if countByte&0x80 == 0 {
			// CBR: every frame the same size (RFC 6716 section 3.2.5).
			if bodies%int(frames) != 0 {
				return PacketShape{}, false
			}
		} else {
			// VBR: M-1 explicit lengths, the last frame implicit.
			declared, header := 0, 0
			lengths := remaining[:bodies]
			for range int(frames) - 1 {
				if len(lengths) == 0 {
					return PacketShape{}, false
				}
				first := lengths[0]
				length, width := int(first), 1
				if first >= 252 {
					if len(lengths) < 2 {
						return PacketShape{}, false
					}
					length, width = int(first)+int(lengths[1])*4, 2
				}
				lengths = lengths[width:]
				declared += length
				header += width
			}
			if header+declared > bodies {
				return PacketShape{}, false
			}
		}
	}
	if uint64(frameUS)*uint64(frames) > maxPacketDurationUS {
		return PacketShape{}, false
	}
	return PacketShape{
		Mode:            mode,
		Bandwidth:       bw,
		Stereo:          toc&0x04 != 0,
		Frames:          frames,
		FrameDurationUS: frameUS,
	}, true
}

// decodeConfig is RFC 6716 table 2: the 32 configurations as (mode,
// bandwidth, frame duration in microseconds).
func decodeConfig(config uint8) (Mode, Bandwidth, uint32) {
	silkUS := [4]uint32{10_000, 20_000, 40_000, 60_000}
	celtUS := [4]uint32{2_500, 5_000, 10_000, 20_000}
	hybridUS := func() uint32 {
		if config&1 == 0 {
			return 10_000
		}
		return 20_000
	}
	low := config & 0x03
	switch {
	case config <= 3:
		return Silk, Narrow, silkUS[low]
	case config <= 7:
		return Silk, Medium, silkUS[low]
	case config <= 11:
		return Silk, Wide, silkUS[low]
	case config <= 13:
		return Hybrid, SuperWide, hybridUS()
	case config <= 15:
		return Hybrid, Full, hybridUS()
	case config <= 19:
		return Celt, Narrow, celtUS[low]
	case config <= 23:
		return Celt, Wide, celtUS[low]
	case config <= 27:
		return Celt, SuperWide, celtUS[low]
	}
	return Celt, Full, celtUS[low]
}
