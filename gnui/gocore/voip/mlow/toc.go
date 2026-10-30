// Ported from whatsapp-rust 5e72baf wacore/src/voip/mlow/toc.rs, MIT.
//
// MLOW "smpl_toc": the first byte of a payload after negotiation selected the MLOW profile.
// `(b & 0xC0) == 0xC0` is its in-profile standard Opus/CELT escape; it is not a global codec
// discriminator.
//
// Bit layout (LSB = bit0): bit7=SID(DTX/CNG), bit6=VAD, bit5=internal rate(0->16k,1->32k),
// bits4:3->frame size index into {10,20,60,120}ms, bit2=low_rate, bit1=FEC, bit0=stereo.
//
// The bit-1 and bit-0 names are not guesses: `smpl_decode_toc` was extracted from the shipped
// WhatsApp Web VoIP module and executed against the C reference over all 192 in-profile bytes,
// and all eight fields agree. Bit 1 is an in-band LBRR/FEC flag rather than a voicing decision
// (voicing is decoded per subframe, further in), and bit 0 is the stereo flag that
// `MLOW_TOC_FIXED_MASK` (0x39) carries into a multiframe indicator.
//
// The Rust per-frame `log::trace!` calls are not ported (no logger in this package).

package mlow

// mlowToc is the decoded smpl TOC. `stdOpus` true means the remaining fields are unused and the
// frame is a standard Opus/CELT packet (decode with stock Opus, not the smpl path).
type mlowToc struct {
	stdOpus    bool
	sid        bool
	vad        bool
	sampleRate int32
	frameMs    int32
	// The packet carries an in-band LBRR/FEC copy of the previous frame.
	fec bool
	// The frame is coded as active voice, which gates two symbols in the LSF decode. NOT a claim
	// that the frame has audio: a frame coded inactive still carries a decodable body.
	active bool
	// The reduced-rate geometry (2x160 internal frames), which this decoder does not implement.
	lowRate bool
	stereo  bool
}

// celtEscapeFrameMs is the frame duration (ms) of the in-profile CELT escape.
//
// **This is not the RFC 6716 TOC layout**, and reading it as one was a live contradiction inside
// this crate: `packetize_opus_for_mlow` writes `0xC0 | mode << 2 | stereo << 1 | multi`, and
// `depacketize_opus_from_mlow` reverses exactly that, while this function used to recover the
// config as `b >> 3`. The two disagreed on the duration for 56 of the 64 escape bytes.
//
// The real layout, confirmed by executing `opus_smpl_decode_TOC` out of the shipped module: bits
// 5:2 are a CELT-only mode, its high two bits select the rate and its low two the duration from
// {2.5, 5, 10, 20} ms. Only CELT is reachable here, because the escape writer refuses anything
// else.
//
// 2.5 ms rounds up to 3: the smpl path only needs a length for a slot it fills with silence, and
// rounding down would under-fill it.
func celtEscapeFrameMs(b uint8) int32 {
	// Only the low two bits of the mode select the duration; the high two select the rate, which
	// this path does not need because it emits a slot at the decoder's own 16 kHz. Masking the
	// whole nibble first would be dead arithmetic, so the bits are taken directly.
	return [4]int32{3, 5, 10, 20}[(b>>2)&3]
}

// parseMlowToc parses the smpl TOC byte.
func parseMlowToc(b uint8) mlowToc {
	if b&0xC0 == 0xC0 {
		return mlowToc{
			stdOpus:    true,
			sid:        false,
			vad:        false,
			sampleRate: 16000,
			frameMs:    celtEscapeFrameMs(b),
			fec:        false,
			active:     false,
			lowRate:    false,
			stereo:     false,
		}
	}
	bit1 := (b>>1)&1 != 0
	vad := (b>>6)&1 != 0
	var sampleRate int32 = 16000
	if b&0x20 != 0 {
		sampleRate = 32000
	}
	return mlowToc{
		stdOpus:    false,
		sid:        b>>7 != 0,
		vad:        vad,
		sampleRate: sampleRate,
		frameMs:    [4]int32{10, 20, 60, 120}[(b>>3)&3],
		fec:        vad && bit1,
		active:     vad || bit1,
		lowRate:    (b>>2)&1 != 0,
		stereo:     b&1 != 0,
	}
}
