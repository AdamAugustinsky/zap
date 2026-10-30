// Ported from whatsapp-rust 5e72baf wacore/src/voip/h264.rs, MIT.

// Package h264 handles H.264 Annex-B for the WhatsApp video media plane:
// NAL splitting, RFC 6184 packetization (single NAL, STAP-A and FU-A out;
// single NAL, STAP-A and FU-A in), and an access-unit splitter for byte
// streams.
//
// It never encodes or decodes pixels: callers hand in encoded Annex-B
// access units and get reassembled ones back.
//
// This is a port of whatsapp-rust's packetizer, not pion/rtp's
// H264Payloader: WhatsApp's sender aggregates any run of small non-VCL NALs
// into one STAP-A (pion only pairs SPS with PPS), strips leading SEI and
// every AUD, ORs the F bit and takes the highest NRI of the members for the
// STAP-A indicator, and fragments at an 800-byte payload. Its depacketizer
// also drops a NAL with a lost FU-A fragment and splits access units on
// timestamp changes, which pion's H264Packet does not.
package h264

import (
	"encoding/binary"
	"iter"
)

const (
	// SingleNALMax is the largest RTP payload emitted before a NAL is
	// fragmented into FU-A units; the reference relay MTU budget.
	SingleNALMax = 800
	// fuaFragSize is SingleNALMax minus the FU indicator and header.
	fuaFragSize = SingleNALMax - 2
	// MaxAUBytes caps reassembly: a NAL or access unit past this is
	// garbage or an attack, not video.
	MaxAUBytes = 4 * 1024 * 1024
)

const (
	nalIDR   = 5
	nalSEI   = 6
	nalSPS   = 7
	nalPPS   = 8
	nalAUD   = 9
	nalSTAPA = 24
	nalFUA   = 28
)

var startCode = [4]byte{0, 0, 0, 1}

// NALUnitType is the type of a NAL (its low five bits), 0 for an empty one.
func NALUnitType(nal []byte) uint8 {
	if len(nal) == 0 {
		return 0
	}
	return nal[0] & 0x1f
}

// firstMBInSlice is first_mb_in_slice of a VCL NAL: the first Exp-Golomb
// code of the slice header, 0 on a picture's first slice. ok is false when
// the NAL is too short or carries no decodable code. Emulation-prevention
// bytes are removed before reading.
func firstMBInSlice(nal []byte) (uint32, bool) {
	const maxBytes = 8
	var raw [maxBytes]byte
	n := 0
	zeros := 0
	for i := 1; i < len(nal); i++ {
		b := nal[i]
		if zeros >= 2 && b == 0x03 {
			zeros = 0
			continue
		}
		if b == 0 {
			zeros++
		} else {
			zeros = 0
		}
		if n == maxBytes {
			break
		}
		raw[n] = b
		n++
	}
	totalBits := n * 8
	bit := func(pos int) uint32 { return uint32(raw[pos/8]>>(7-pos%8)) & 1 }
	consumed := 0
	leading := 0
	for {
		if consumed >= totalBits {
			return 0, false
		}
		if bit(consumed) != 0 {
			break
		}
		consumed++
		leading++
		if leading > 31 {
			return 0, false
		}
	}
	consumed++
	if consumed+leading > totalBits {
		return 0, false
	}
	var value uint32
	for range leading {
		value = value<<1 | bit(consumed)
		consumed++
	}
	return (uint32(1) << leading) - 1 + value, true
}

// SplitAnnexB yields the NAL units of an Annex-B buffer, start codes
// stripped and empty NALs skipped. The slices alias data.
func SplitAnnexB(data []byte) iter.Seq[[]byte] {
	return func(yield func([]byte) bool) {
		pos := 0
		for {
			_, nalBegin, ok := findStartCode(data, pos)
			if !ok {
				return
			}
			nalEnd := len(data)
			if next, _, ok := findStartCode(data, nalBegin); ok {
				nalEnd = next
			}
			pos = nalEnd
			if nal := data[nalBegin:nalEnd:nalEnd]; len(nal) > 0 && !yield(nal) {
				return
			}
		}
	}
}

// findStartCode finds the next start code at or after from: begin is its
// first byte (a preceding zero folded in, so the 4-byte form stays whole),
// end the first NAL byte after it.
func findStartCode(data []byte, from int) (begin, end int, ok bool) {
	if from > len(data) {
		return 0, 0, false
	}
	hay := data[from:]
	for i := 0; i+3 <= len(hay); i++ {
		if hay[i] == 0 && hay[i+1] == 0 {
			if hay[i+2] == 1 {
				b := i
				if i > 0 && hay[i-1] == 0 {
					b = i - 1
				}
				return from + b, from + i + 3, true
			}
		}
	}
	return 0, 0, false
}

// AUIsKeyframe reports whether the access unit contains an IDR slice or a
// parameter set, the points a decoder can (re)sync from. Use AUHasIDR to
// gate a resume after loss.
func AUIsKeyframe(au []byte) bool {
	for nal := range SplitAnnexB(au) {
		switch NALUnitType(nal) {
		case nalIDR, nalSPS, nalPPS:
			return true
		}
	}
	return false
}

// AUHasIDR reports whether the access unit carries an IDR slice, a
// self-contained decodable restart point.
func AUHasIDR(au []byte) bool {
	for nal := range SplitAnnexB(au) {
		if NALUnitType(nal) == nalIDR {
			return true
		}
	}
	return false
}

// Capacity a PacketizedAU keeps between frames: an ordinary stream's
// working set, without pinning an outlier frame's buffer for the call.
const (
	packetizedRetainedBytes    = 128 * 1024
	packetizedRetainedPayloads = packetizedRetainedBytes/SingleNALMax + 1
)

// PacketizedAU is one access unit's RTP payloads, packed back to back in a
// single buffer. The send path keeps one per call and refills it per frame,
// so a steady stream packetizes without allocating. Payloads alias the
// buffer and are valid until the next Packetize into it.
type PacketizedAU struct {
	data []byte
	// ends holds each payload's end offset in data; a payload starts where
	// the previous one ended.
	ends []int
}

// Len is the number of payloads.
func (p *PacketizedAU) Len() int { return len(p.ends) }

// At is payload i.
func (p *PacketizedAU) At(i int) []byte {
	start := 0
	if i > 0 {
		start = p.ends[i-1]
	}
	end := p.ends[i]
	return p.data[start:end:end]
}

// All yields every payload in order.
func (p *PacketizedAU) All() iter.Seq[[]byte] {
	return func(yield func([]byte) bool) {
		for i := range p.ends {
			if !yield(p.At(i)) {
				return
			}
		}
	}
}

func (p *PacketizedAU) clear() {
	p.data = p.data[:0]
	p.ends = p.ends[:0]
}

// releaseOutlierCapacity hands back the capacity an outlier frame grew, so
// a spike does not pin its buffer for the rest of the call, while a stream
// that stays large keeps its buffer.
func (p *PacketizedAU) releaseOutlierCapacity() {
	if len(p.data) <= packetizedRetainedBytes && cap(p.data) > packetizedRetainedBytes {
		p.data = append(make([]byte, 0, packetizedRetainedBytes), p.data...)
	}
	if len(p.ends) <= packetizedRetainedPayloads && cap(p.ends) > packetizedRetainedPayloads {
		p.ends = append(make([]int, 0, packetizedRetainedPayloads), p.ends...)
	}
}

func (p *PacketizedAU) finishPayload() { p.ends = append(p.ends, len(p.data)) }

// Packetize packetizes one Annex-B access unit into WhatsApp RTP payloads
// (no RTP headers): each VCL NAL as a single-NAL payload when it fits or a
// run of FU-A fragments otherwise, runs of small non-VCL NALs aggregated
// into STAP-A. AUDs are omitted, and so is SEI before the first VCL NAL.
// out is cleared and refilled.
func Packetize(au []byte, out *PacketizedAU) { packetize(au, out, false) }

// PacketizeKeepSEI is Packetize keeping every SEI unit, for a peer that
// needs the metadata.
func PacketizeKeepSEI(au []byte, out *PacketizedAU) { packetize(au, out, true) }

func packetize(au []byte, out *PacketizedAU, keepSEI bool) {
	out.clear()
	seenVCL := false
	// Open STAP-A: offset of its indicator in out.data, and the F bit (OR)
	// and NRI (max) of its members, backpatched at flush.
	stapOpen := false
	var stapStart int
	var stapF, stapNRI byte
	flush := func() {
		if stapOpen {
			out.data[stapStart] = stapF | stapNRI | nalSTAPA
			out.finishPayload()
			stapOpen = false
		}
	}
	for nal := range SplitAnnexB(au) {
		t := NALUnitType(nal)
		if t == nalAUD {
			continue
		}
		if !keepSEI && !seenVCL && t == nalSEI {
			continue
		}
		if t >= 1 && t <= 5 {
			flush()
			seenVCL = true
			if len(nal) <= SingleNALMax {
				out.data = append(out.data, nal...)
				out.finishPayload()
			} else {
				fragmentNAL(nal, out)
			}
			continue
		}
		// Non-VCL: aggregate while the run fits, else start a new STAP-A;
		// a unit too big even for an empty STAP-A goes alone.
		stapCost := 2 + len(nal)
		var fits bool
		if stapOpen {
			fits = len(out.data)-stapStart+stapCost <= SingleNALMax
		} else {
			fits = stapCost < SingleNALMax
		}
		if !fits {
			flush()
			if stapCost >= SingleNALMax {
				if len(nal) <= SingleNALMax {
					out.data = append(out.data, nal...)
					out.finishPayload()
				} else {
					fragmentNAL(nal, out)
				}
				continue
			}
		}
		if stapOpen {
			stapF |= nal[0] & 0x80
			stapNRI = max(stapNRI, nal[0]&0x60)
		} else {
			stapStart = len(out.data)
			out.data = append(out.data, 0)
			stapOpen, stapF, stapNRI = true, nal[0]&0x80, nal[0]&0x60
		}
		out.data = binary.BigEndian.AppendUint16(out.data, uint16(len(nal)))
		out.data = append(out.data, nal...)
	}
	flush()
	out.releaseOutlierCapacity()
}

// fragmentNAL fragments one oversized NAL into an FU-A run (RFC 6184
// section 5.8).
func fragmentNAL(nal []byte, out *PacketizedAU) {
	indicator := nal[0]&0xe0 | nalFUA
	origType := nal[0] & 0x1f
	body := nal[1:]
	for start := 0; start < len(body); start += fuaFragSize {
		end := min(start+fuaFragSize, len(body))
		header := origType
		if start == 0 {
			header |= 0x80 // S
		}
		if end == len(body) {
			header |= 0x40 // E
		}
		out.data = append(out.data, indicator, header)
		out.data = append(out.data, body[start:end]...)
		out.finishPayload()
	}
}

// maxReadyAUs caps the completed-but-unreturned backlog.
const maxReadyAUs = 4

type readyAU struct {
	timestamp uint32
	au        []byte
}

// Depacketizer reassembles RTP payloads into Annex-B access units. A
// completed AU is returned when its marker arrives or when a new RTP
// timestamp begins the next AU, so a lost marker does not merge two
// frames. Malformed or partially lost input degrades to dropped NALs.
// The zero value is ready to use.
type Depacketizer struct {
	auBuf    []byte
	fuBuf    []byte
	fuActive bool
	// fuNextSeq is the sequence number the next fragment of the FU in
	// progress must carry; a gap drops the partial NAL.
	fuNextSeq uint16
	// auTimestamp is the RTP timestamp of the AU in auBuf, if any.
	auTimestamp    uint32
	hasAUTimestamp bool
	// lastCompleted keeps a marker-completed AU closed when one of its
	// packets arrives late.
	lastCompleted    uint32
	hasLastCompleted bool
	ready            []readyAU
}

// Reset drops all reassembly state.
func (d *Depacketizer) Reset() {
	d.auBuf = d.auBuf[:0]
	d.fuBuf = d.fuBuf[:0]
	d.fuActive = false
	d.hasAUTimestamp = false
	d.hasLastCompleted = false
	d.ready = d.ready[:0]
}

func (d *Depacketizer) queueReady(ts uint32, au []byte) {
	if len(d.ready) >= maxReadyAUs {
		d.ready = d.ready[1:]
	}
	d.ready = append(d.ready, readyAU{ts, au})
}

// PopReady takes another completed AU with its RTP timestamp.
func (d *Depacketizer) PopReady() (timestamp uint32, au []byte, ok bool) {
	if len(d.ready) == 0 {
		return 0, nil, false
	}
	r := d.ready[0]
	d.ready = d.ready[1:]
	return r.timestamp, r.au, true
}

func (d *Depacketizer) dropPartialFU() {
	d.fuBuf = d.fuBuf[:0]
	d.fuActive = false
}

func (d *Depacketizer) appendNAL(nal []byte) {
	if len(nal) == 0 || len(d.auBuf)+len(startCode)+len(nal) > MaxAUBytes {
		return
	}
	d.auBuf = append(d.auBuf, startCode[:]...)
	d.auBuf = append(d.auBuf, nal...)
}

// takeAU hands over the AU buffer; the next AU starts a fresh one.
func (d *Depacketizer) takeAU() []byte {
	au := d.auBuf
	d.auBuf = nil
	return au
}

// Push feeds one payload with its RTP sequence number, timestamp and
// marker bit, and returns a completed AU if one is ready.
func (d *Depacketizer) Push(seq uint16, timestamp uint32, payload []byte, marker bool) (uint32, []byte, bool) {
	if d.hasAUTimestamp {
		cur := d.auTimestamp
		if timestamp != cur {
			// A forward jump (wrap-aware) begins a new AU: flush the
			// buffered one even without its marker. A backward one is a
			// reordered packet from a past AU: discard it.
			if int32(timestamp-cur) > 0 {
				d.dropPartialFU()
				if len(d.auBuf) > 0 {
					d.queueReady(cur, d.takeAU())
				}
				d.lastCompleted, d.hasLastCompleted = cur, true
				d.auTimestamp = timestamp
			} else {
				return d.PopReady()
			}
		}
	} else {
		if d.hasLastCompleted && int32(timestamp-d.lastCompleted) <= 0 {
			return d.PopReady()
		}
		d.auTimestamp, d.hasAUTimestamp = timestamp, true
	}
	switch t := NALUnitType(payload); {
	case t == nalSTAPA:
		d.dropPartialFU()
		rest := payload[1:]
		for len(rest) >= 2 {
			n := int(binary.BigEndian.Uint16(rest))
			rest = rest[2:]
			if n == 0 || n > len(rest) {
				break
			}
			d.appendNAL(rest[:n])
			rest = rest[n:]
		}
	case t == nalFUA && len(payload) >= 2:
		fuHeader := payload[1]
		start := fuHeader&0x80 != 0
		end := fuHeader&0x40 != 0
		if start {
			d.dropPartialFU()
			d.fuActive = true
			d.fuBuf = append(d.fuBuf, payload[0]&0xe0|fuHeader&0x1f)
		} else if d.fuActive && seq != d.fuNextSeq {
			// A lost or reordered fragment: drop the partial NAL rather
			// than hand the decoder a truncated slice.
			d.dropPartialFU()
		}
		if d.fuActive {
			d.fuNextSeq = seq + 1
			if len(d.fuBuf)+len(payload) > MaxAUBytes {
				d.dropPartialFU()
			} else {
				d.fuBuf = append(d.fuBuf, payload[2:]...)
				if end {
					d.fuActive = false
					d.appendNAL(d.fuBuf)
					d.fuBuf = d.fuBuf[:0]
				}
			}
		}
		// A middle fragment with no start on record waits for the next start.
	case t >= 1 && t <= 23:
		d.dropPartialFU()
		d.appendNAL(payload)
	}
	if marker && len(d.auBuf) > 0 {
		d.dropPartialFU()
		completed := timestamp
		if d.hasAUTimestamp {
			completed = d.auTimestamp
		}
		d.hasAUTimestamp = false
		d.lastCompleted, d.hasLastCompleted = completed, true
		d.queueReady(completed, d.takeAU())
	}
	return d.PopReady()
}

// AnnexBAUSplitter splits a raw Annex-B byte stream (an encoder's output)
// into access units: at AUD NALs, or for AUD-less encoders at picture
// boundaries. SPS opens a group once a picture is buffered, and a VCL slice
// starting a new picture (first_mb_in_slice zero) closes the previous one.
// PPS alone never cuts. Once an AUD is seen, AUDs own the framing. A
// trailing SEI run rides with the group or picture after it. The zero value
// is ready to use.
type AnnexBAUSplitter struct {
	buf []byte
	// scanPos: everything before it was already searched.
	scanPos int
	// seenAUD: an AUD was observed, so SPS/VCL cutting is off.
	seenAUD bool
	// bufHasVCL: the buffered bytes hold a VCL NAL.
	bufHasVCL bool
	// pendingSEIStart is the start of a trailing all-SEI run after the
	// last VCL NAL, if any.
	pendingSEIStart    int
	hasPendingSEIStart bool
}

// dropRunaway drops a buffer past MaxAUBytes and every framing fact about
// it, so AUD-less framing can recover.
func (s *AnnexBAUSplitter) dropRunaway() {
	s.buf = s.buf[:0]
	s.scanPos = 0
	s.seenAUD = false
	s.bufHasVCL = false
	s.hasPendingSEIStart = false
}

// Push feeds arbitrary chunks and returns the access units they complete.
func (s *AnnexBAUSplitter) Push(data []byte) [][]byte {
	var out [][]byte
	s.buf = append(s.buf, data...)
	for {
		begin, end, ok := findStartCode(s.buf, s.scanPos)
		if !ok {
			// No start code: cap the buffer, and keep the last few bytes
			// so a start code split across chunks still reassembles.
			if len(s.buf) > MaxAUBytes {
				s.dropRunaway()
			}
			s.scanPos = max(len(s.buf)-3, 0)
			break
		}
		if end >= len(s.buf) {
			// Start code at the buffer edge: wait for the type byte.
			s.scanPos = begin
			if len(s.buf) > MaxAUBytes {
				s.dropRunaway()
			}
			break
		}
		unitType := s.buf[end] & 0x1f
		if unitType == nalAUD {
			s.seenAUD = true
		}
		isVCL := unitType >= 1 && unitType <= 5
		nalEnd := len(s.buf)
		if next, _, ok := findStartCode(s.buf, end); ok {
			nalEnd = next
		}
		// Only slice NALs (1, 2, 5) start with first_mb_in_slice; data
		// partitions 3 and 4 start with slice_id.
		picture, pictureOK := uint32(1), true
		if unitType == 1 || unitType == 2 || unitType == 5 {
			picture, pictureOK = firstMBInSlice(s.buf[end:nalEnd])
		}
		if isVCL && !pictureOK && nalEnd == len(s.buf) {
			// The slice header may not have arrived yet: rewind and wait.
			s.scanPos = begin
			if len(s.buf) > MaxAUBytes {
				s.dropRunaway()
			}
			break
		}
		newPicture := pictureOK && picture == 0
		audlessCut := !s.seenAUD &&
			((unitType == nalSPS && s.bufHasVCL) || (isVCL && s.bufHasVCL && newPicture))
		seiHandoff := s.hasPendingSEIStart && s.pendingSEIStart > 0 && audlessCut
		if begin > 0 && (unitType == nalAUD || audlessCut) {
			at := begin
			if seiHandoff {
				at = s.pendingSEIStart
			}
			out = append(out, s.buf[:at:at])
			s.buf = append([]byte(nil), s.buf[at:]...)
			s.bufHasVCL = false
			s.hasPendingSEIStart = false
			if seiHandoff {
				// The current NAL was evaluated: resume past it, keeping
				// the three-byte overlap for a split start code.
				s.scanPos = max(nalEnd-at-3, 0)
			} else {
				s.scanPos = 0
			}
		} else {
			s.scanPos = end
		}
		if isVCL {
			s.bufHasVCL = true
			s.hasPendingSEIStart = false
		} else if unitType == nalSEI && !s.hasPendingSEIStart {
			s.pendingSEIStart, s.hasPendingSEIStart = begin, true
		}
		if len(s.buf) > MaxAUBytes {
			s.dropRunaway()
		}
	}
	return out
}

// Finish flushes the trailing AU at end of stream and resets the framing
// state, so the splitter can be reused.
func (s *AnnexBAUSplitter) Finish() ([]byte, bool) {
	s.scanPos = 0
	s.seenAUD = false
	s.bufHasVCL = false
	s.hasPendingSEIStart = false
	if len(s.buf) == 0 {
		return nil, false
	}
	au := s.buf
	s.buf = nil
	return au, true
}
