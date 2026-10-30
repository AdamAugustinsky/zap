// Ported from whatsapp-rust 5e72baf wacore/src/voip/rtcp.rs, MIT.

// Package rtcp is WhatsApp's RTCP: standard sender and source reports, the
// compact reports (payload types 208/209), PLI, the compound parser, and
// receive statistics. The sender report's NTP time is a nowMS argument so
// the package reads no clock.
//
// pion/rtcp is not used on the wire: WhatsApp's native video profile sets
// bit 4 beside the report count / FMT (which pion reads as count 17 and
// rejects), appends 24 bytes of transport statistics to each report block,
// and uses the non-standard compact types 208/209. The tests cross-check the
// standard-conformant packets (plain SR, SDES, PLI) against pion/rtcp.
package rtcp

import (
	"encoding/binary"
	"math/bits"
	"slices"
)

// Payload types and sizes.
const (
	PTSR              uint8 = 200
	PTRR              uint8 = 201
	PTSDES            uint8 = 202
	PTRTPFB           uint8 = 205
	PTPSFB            uint8 = 206
	PTWACompact       uint8 = 208
	PTWACompact2      uint8 = 209
	HeaderLen               = 8
	SRTCPTrailerLen         = 14
	WhatsAppCNameLen        = 18
	ntpUnixOffsetSecs       = 2_208_988_800
	waReportExtLen          = 24
)

// IsRTCP reports a packet of at least 8 bytes with V=2 and a payload type in
// RTCP's mux range 192..223.
func IsRTCP(data []byte) bool {
	return len(data) >= HeaderLen && data[0]>>6 == 2 && data[1] >= 192 && data[1] <= 223
}

// PayloadType is the RTCP payload type of an RTCP packet.
func PayloadType(data []byte) (uint8, bool) {
	if !IsRTCP(data) {
		return 0, false
	}
	return data[1], true
}

// SenderSSRC is the clear sender SSRC of an RTCP packet.
func SenderSSRC(data []byte) (uint32, bool) {
	if !IsRTCP(data) {
		return 0, false
	}
	return binary.BigEndian.Uint32(data[4:]), true
}

// ReportBlock is one parsed report block; ProfileExtension is whatever
// follows the 24 RFC bytes (WhatsApp's 24 bytes of statistics).
type ReportBlock struct {
	SSRC                       uint32
	FractionLost               uint8
	CumulativeLost             int32
	ExtendedHighestSequence    uint32
	Jitter                     uint32
	LastSenderReport           uint32
	DelaySinceLastSenderReport uint32
	ProfileExtension           []byte
}

// Feedback is one RTPFB/PSFB message.
type Feedback struct {
	PacketType uint8
	FMT        uint8
	SenderSSRC uint32
	MediaSSRC  uint32
	FCI        []byte
}

// Summary is what Summarize found in a compound packet.
type Summary struct {
	PacketTypes     []uint8
	SenderSSRC      uint32
	ReferencedSSRCs []uint32
	ReportBlocks    []ReportBlock
	Feedback        []Feedback
	SDESCNameLens   []int
	// UsesWhatsAppProfileExtension: the native video profile's bit 4 beside
	// the count / FMT.
	UsesWhatsAppProfileExtension bool
}

func parseSDESCNameLens(p []byte, sources int) ([]int, bool) {
	cursor := 4
	lens := []int{}
	for range sources {
		cursor += 4
		if cursor > len(p) {
			return nil, false
		}
		for {
			if cursor >= len(p) {
				return nil, false
			}
			itemType := p[cursor]
			cursor++
			if itemType == 0 {
				for cursor&3 != 0 {
					if cursor >= len(p) || p[cursor] != 0 {
						return nil, false
					}
					cursor++
				}
				break
			}
			if cursor >= len(p) {
				return nil, false
			}
			n := int(p[cursor])
			cursor++
			if cursor+n > len(p) {
				return nil, false
			}
			if itemType == 1 {
				lens = append(lens, n)
			}
			cursor += n
		}
	}
	if cursor != len(p) {
		return nil, false
	}
	return lens, true
}

func parseReportBlocks(p []byte, start, rawCount int) ([]ReportBlock, bool, bool) {
	if start > len(p) {
		return nil, false, false
	}
	available := len(p) - start
	count := rawCount
	profile := false
	if available < count*24 && rawCount&0x10 != 0 {
		count = rawCount & 0x0f
		profile = true
	}
	if count == 0 {
		return []ReportBlock{}, profile, available == 0
	}
	if available < count*24 {
		return nil, false, false
	}
	stride := 24
	if available%count == 0 {
		stride = available / count
	}
	blocks := make([]ReportBlock, 0, count)
	for i := range count {
		at := start + i*stride
		if at+stride > len(p) {
			return nil, false, false
		}
		b := p[at : at+stride]
		raw := int32(uint32(b[5])<<16 | uint32(b[6])<<8 | uint32(b[7]))
		if raw&0x80_0000 != 0 {
			raw |= ^0x00ff_ffff
		}
		blocks = append(blocks, ReportBlock{
			SSRC:                       binary.BigEndian.Uint32(b[0:]),
			FractionLost:               b[4],
			CumulativeLost:             raw,
			ExtendedHighestSequence:    binary.BigEndian.Uint32(b[8:]),
			Jitter:                     binary.BigEndian.Uint32(b[12:]),
			LastSenderReport:           binary.BigEndian.Uint32(b[16:]),
			DelaySinceLastSenderReport: binary.BigEndian.Uint32(b[20:]),
			ProfileExtension:           append([]byte{}, b[24:]...),
		})
	}
	return blocks, profile, true
}

// Summarize parses a decrypted compound packet. Referenced SSRCs are those of
// SR/RR report blocks and the media targets of NACK, PLI and FIR, sorted and
// deduplicated.
func Summarize(data []byte) (Summary, bool) {
	var s Summary
	s.PacketTypes = []uint8{}
	s.ReferencedSSRCs = []uint32{}
	s.ReportBlocks = []ReportBlock{}
	s.Feedback = []Feedback{}
	s.SDESCNameLens = []int{}
	haveSender := false
	off := 0
	for off < len(data) {
		if off+4 > len(data) {
			return Summary{}, false
		}
		h := data[off : off+4]
		if h[0]>>6 != 2 || h[1] < 192 || h[1] > 223 {
			return Summary{}, false
		}
		plen := (int(binary.BigEndian.Uint16(h[2:])) + 1) * 4
		if off+plen > len(data) {
			return Summary{}, false
		}
		p := data[off : off+plen]
		if len(p) < HeaderLen {
			return Summary{}, false
		}
		if p[0]&0x20 != 0 {
			pad := int(p[len(p)-1])
			if off+plen != len(data) || pad == 0 || pad%4 != 0 || pad > len(p)-HeaderLen {
				return Summary{}, false
			}
			p = p[:len(p)-pad]
		}
		pt := p[1]
		rawCount := int(p[0] & 0x1f)
		sender := binary.BigEndian.Uint32(p[4:])
		if !haveSender {
			s.SenderSSRC, haveSender = sender, true
		}
		s.PacketTypes = append(s.PacketTypes, pt)

		start := -1
		switch pt {
		case PTSR:
			start = 28
		case PTRR:
			start = 8
		}
		if start >= 0 {
			blocks, profile, ok := parseReportBlocks(p, start, rawCount)
			if !ok {
				return Summary{}, false
			}
			s.UsesWhatsAppProfileExtension = s.UsesWhatsAppProfileExtension || profile
			for _, b := range blocks {
				s.ReferencedSSRCs = append(s.ReferencedSSRCs, b.SSRC)
			}
			s.ReportBlocks = append(s.ReportBlocks, blocks...)
		}

		switch {
		case (pt == PTRTPFB || pt == PTPSFB) && len(p) >= 12:
			fmtv := rawCount
			if rawCount&0x10 != 0 {
				s.UsesWhatsAppProfileExtension = true
				fmtv = rawCount & 0x0f
			}
			media := binary.BigEndian.Uint32(p[8:])
			s.Feedback = append(s.Feedback, Feedback{
				PacketType: pt, FMT: uint8(fmtv), SenderSSRC: sender, MediaSSRC: media,
				FCI: append([]byte{}, p[12:]...),
			})
			if media != 0 {
				s.ReferencedSSRCs = append(s.ReferencedSSRCs, media)
			}
			if pt == PTPSFB && fmtv == 4 {
				for fci := p[12:]; len(fci) >= 8; fci = fci[8:] {
					s.ReferencedSSRCs = append(s.ReferencedSSRCs, binary.BigEndian.Uint32(fci))
				}
			}
		case pt == PTWACompact && len(p) >= 12:
			s.ReferencedSSRCs = append(s.ReferencedSSRCs, binary.BigEndian.Uint32(p[8:]))
		case pt == PTSDES:
			lens, ok := parseSDESCNameLens(p, rawCount)
			if !ok {
				if rawCount&0x10 == 0 {
					return Summary{}, false
				}
				if lens, ok = parseSDESCNameLens(p, rawCount&0x0f); !ok {
					return Summary{}, false
				}
				s.UsesWhatsAppProfileExtension = true
			}
			s.SDESCNameLens = append(s.SDESCNameLens, lens...)
		}
		off += plen
	}
	if !haveSender {
		return Summary{}, false
	}
	slices.Sort(s.ReferencedSSRCs)
	s.ReferencedSSRCs = slices.Compact(s.ReferencedSSRCs)
	return s, true
}

// SenderStats is a sender report's counters.
type SenderStats struct {
	PacketsSent  uint32
	OctetsSent   uint32
	RTPTimestamp uint32
}

// ReceptionReport is one report block we send.
type ReceptionReport struct {
	SSRC                       uint32
	FractionLost               uint8
	CumulativeLost             int32
	ExtendedHighestSequence    uint32
	Jitter                     uint32
	LastSenderReport           uint32
	DelaySinceLastSenderReport uint32
}

// ReceptionStats tracks one inbound RTP stream for receiver reports (RFC
// 3550 A.3/A.8) and the peer's frame span.
type ReceptionStats struct {
	ssrc                 uint32
	haveSSRC             bool
	baseSequence         uint16
	maxSequence          uint16
	sequenceCycles       uint32
	received             uint32
	expectedPrior        uint32
	receivedPrior        uint32
	transit              uint32
	haveTransit          bool
	lastRTPTimestamp     uint32
	haveLastRTPTimestamp bool
	frameSpan            uint32
	haveFrameSpan        bool
	jitterQ4             uint64
	lastSenderReport     uint32
	lastSenderReportAtMS uint64
	haveLastSenderReport bool
}

// Observe folds one received packet into the statistics.
func (r *ReceptionStats) Observe(ssrc uint32, seq uint16, rtpTimestamp uint32, arrivalMS uint64, clockRate uint32) {
	var newest bool
	if !r.haveSSRC || r.ssrc != ssrc {
		*r = ReceptionStats{ssrc: ssrc, haveSSRC: true, baseSequence: seq, maxSequence: seq, received: 1}
		newest = true
	} else {
		delta := seq - r.maxSequence
		newest = delta != 0 && delta < 0x8000
		if newest {
			if seq < r.maxSequence {
				r.sequenceCycles += 1 << 16
			}
			r.maxSequence = seq
		}
		r.received++
	}

	arrivalRTP := uint32(mulDiv(arrivalMS, uint64(clockRate), 1000))
	transit := arrivalRTP - rtpTimestamp
	if r.haveTransit {
		d := int64(int32(transit - r.transit))
		if d < 0 {
			d = -d
		}
		decay := (r.jitterQ4 + 8) >> 4
		r.jitterQ4 = satSub(satAdd(r.jitterQ4, uint64(d)), decay)
	}
	r.transit, r.haveTransit = transit, true

	if newest {
		if r.haveLastRTPTimestamp {
			delta := rtpTimestamp - r.lastRTPTimestamp
			r.frameSpan, r.haveFrameSpan = delta, delta > 0 && delta <= clockRate
		}
		r.lastRTPTimestamp, r.haveLastRTPTimestamp = rtpTimestamp, true
	} else {
		r.haveFrameSpan = false
	}
}

// mulDiv is a*b/c in 128-bit arithmetic, truncated to 64 bits.
func mulDiv(a, b, c uint64) uint64 {
	hi, lo := bits.Mul64(a, b)
	q, _ := bits.Div64(hi%c, lo, c)
	return q
}

func satAdd(a, b uint64) uint64 {
	if s := a + b; s >= a {
		return s
	}
	return ^uint64(0)
}

func satSub(a, b uint64) uint64 {
	if a < b {
		return 0
	}
	return a - b
}

// SSRC is the tracked stream's SSRC.
func (r *ReceptionStats) SSRC() (uint32, bool) { return r.ssrc, r.haveSSRC }

// FrameSpan is the step between the last two in-order packets' RTP
// timestamps; unset after a step that is zero, backwards, or over a second,
// after a reordered packet, and on a new SSRC.
func (r *ReceptionStats) FrameSpan() (uint32, bool) {
	if !r.haveFrameSpan {
		return 0, false
	}
	return r.frameSpan, true
}

// ObserveSenderReport records a sender report of the tracked stream.
func (r *ReceptionStats) ObserveSenderReport(senderSSRC, ntpSeconds, ntpFraction uint32, arrivalMS uint64) {
	if !r.haveSSRC || r.ssrc != senderSSRC {
		return
	}
	r.lastSenderReport = ntpSeconds<<16 | ntpFraction>>16
	r.lastSenderReportAtMS, r.haveLastSenderReport = arrivalMS, true
}

// Report builds the next reception report.
func (r *ReceptionStats) Report(nowMS uint64) (ReceptionReport, bool) {
	if !r.haveSSRC {
		return ReceptionReport{}, false
	}
	expected := r.sequenceCycles + uint32(r.maxSequence) - uint32(r.baseSequence) + 1
	expectedInterval := expected - r.expectedPrior
	receivedInterval := r.received - r.receivedPrior
	lost := int64(expectedInterval) - int64(receivedInterval)
	var fraction uint8
	if expectedInterval != 0 && lost > 0 {
		fraction = uint8(min(lost*256/int64(expectedInterval), 255))
	}
	r.expectedPrior, r.receivedPrior = expected, r.received
	cumulative := int32(max(min(int64(expected)-int64(r.received), 0x7f_ffff), -0x80_0000))
	var dlsr uint32
	if r.haveLastSenderReport {
		elapsed := satSub(nowMS, r.lastSenderReportAtMS)
		dlsr = uint32(min(mulDiv(elapsed, 65_536, 1000), 0xffff_ffff))
	}
	return ReceptionReport{
		SSRC: r.ssrc, FractionLost: fraction, CumulativeLost: cumulative,
		ExtendedHighestSequence:    r.sequenceCycles | uint32(r.maxSequence),
		Jitter:                     uint32(min(r.jitterQ4>>4, 0xffff_ffff)),
		LastSenderReport:           r.lastSenderReport,
		DelaySinceLastSenderReport: dlsr,
	}, true
}

// SenderReportTiming finds the first SR in a compound packet and returns its
// sender SSRC and NTP seconds/fraction.
func SenderReportTiming(data []byte) (ssrc, ntpSeconds, ntpFraction uint32, ok bool) {
	off := 0
	for off < len(data) {
		if off+4 > len(data) {
			return 0, 0, 0, false
		}
		plen := (int(binary.BigEndian.Uint16(data[off+2:])) + 1) * 4
		if off+plen > len(data) {
			return 0, 0, 0, false
		}
		p := data[off : off+plen]
		if data[off+1] == PTSR && len(p) >= 28 {
			return binary.BigEndian.Uint32(p[4:]), binary.BigEndian.Uint32(p[8:]), binary.BigEndian.Uint32(p[12:]), true
		}
		off += plen
	}
	return 0, 0, 0, false
}

// WhatsAppCName is the native CNAME: eleven lowercase hex nibbles from
// entropy[6..] around a fixed "@pj....org" shape.
func WhatsAppCName(entropy [12]byte) [WhatsAppCNameLen]byte {
	const hexdig = "0123456789abcdef"
	var r [11]byte
	for i := range r {
		b := entropy[6+i/2]
		if i&1 == 0 {
			b >>= 4
		} else {
			b &= 0x0f
		}
		r[i] = hexdig[b]
	}
	var c [WhatsAppCNameLen]byte
	copy(c[:5], r[:5])
	copy(c[5:8], "@pj")
	copy(c[8:14], r[5:])
	copy(c[14:], ".org")
	return c
}

// Compact208 is the 12-byte compact report (PT 208, RC=1).
func Compact208(localSSRC, remoteSSRC uint32) [12]byte {
	var b [12]byte
	b[0], b[1], b[3] = 0x81, PTWACompact, 2
	binary.BigEndian.PutUint32(b[4:], localSSRC)
	binary.BigEndian.PutUint32(b[8:], remoteSSRC)
	return b
}

// Compact209 is the 8-byte pre-speech compact report (PT 209, RC=1).
func Compact209(localSSRC uint32) [8]byte {
	var b [8]byte
	b[0], b[1], b[3] = 0x81, PTWACompact2, 1
	binary.BigEndian.PutUint32(b[4:], localSSRC)
	return b
}

// PictureLossIndication is the 12-byte PLI (PT 206, FMT 1) without the
// sequence-number FCI: WhatsApp's deduplicator honours a sequence-less PLI
// unconditionally, while a sequenced one can be dropped forever after the
// peer resets its state.
func PictureLossIndication(senderSSRC, mediaSSRC uint32) [12]byte {
	var b [12]byte
	b[0], b[1], b[3] = 0x81, PTPSFB, 2
	binary.BigEndian.PutUint32(b[4:], senderSSRC)
	binary.BigEndian.PutUint32(b[8:], mediaSSRC)
	return b
}

// WhatsAppPictureLossIndication sets the native video profile's bit 4 when
// profileExtension is set; WhatsApp's transport recognises a video PLI only
// with it.
func WhatsAppPictureLossIndication(senderSSRC, mediaSSRC uint32, profileExtension bool) [12]byte {
	b := PictureLossIndication(senderSSRC, mediaSSRC)
	if profileExtension {
		b[0] |= 0x10
	}
	return b
}

// SenderReport is the 28-byte SR (PT 200, RC=0). nowMS is the wall clock;
// both NTP fields wrap to 32 bits like the reference encoder.
func SenderReport(localSSRC uint32, s SenderStats, nowMS uint64) [28]byte {
	var b [28]byte
	b[0], b[1], b[3] = 0x80, PTSR, 6
	binary.BigEndian.PutUint32(b[4:], localSSRC)
	ntpSec := uint32(nowMS/1000 + ntpUnixOffsetSecs)
	ntpFrac := uint32(float64(nowMS%1000) / 1000.0 * 4_294_967_296.0)
	binary.BigEndian.PutUint32(b[8:], ntpSec)
	binary.BigEndian.PutUint32(b[12:], ntpFrac)
	binary.BigEndian.PutUint32(b[16:], s.RTPTimestamp)
	binary.BigEndian.PutUint32(b[20:], s.PacketsSent)
	binary.BigEndian.PutUint32(b[24:], s.OctetsSent)
	return b
}

func appendReceptionReport(out []byte, r ReceptionReport) []byte {
	out = binary.BigEndian.AppendUint32(out, r.SSRC)
	out = append(out, r.FractionLost)
	lost := uint32(r.CumulativeLost)
	out = append(out, byte(lost>>16), byte(lost>>8), byte(lost))
	out = binary.BigEndian.AppendUint32(out, r.ExtendedHighestSequence)
	out = binary.BigEndian.AppendUint32(out, r.Jitter)
	out = binary.BigEndian.AppendUint32(out, r.LastSenderReport)
	return binary.BigEndian.AppendUint32(out, r.DelaySinceLastSenderReport)
}

func setLength(p []byte) {
	binary.BigEndian.PutUint16(p[2:], uint16(len(p)/4-1))
}

func senderReportWithReports(localSSRC uint32, s SenderStats, nowMS uint64, reports []ReceptionReport) []byte {
	reports = reports[:min(len(reports), 31)]
	sr := SenderReport(localSSRC, s, nowMS)
	p := append([]byte{}, sr[:]...)
	p[0] |= byte(len(reports))
	for _, r := range reports {
		p = appendReceptionReport(p, r)
	}
	setLength(p)
	return p
}

// SourceDescription is native WhatsApp's 32-byte SDES: one chunk, its
// 18-byte CNAME, END and padding.
func SourceDescription(localSSRC uint32, cname [WhatsAppCNameLen]byte) [32]byte {
	var p [32]byte
	p[0], p[1], p[3] = 0x81, PTSDES, 7
	binary.BigEndian.PutUint32(p[4:], localSSRC)
	p[8], p[9] = 1, WhatsAppCNameLen
	copy(p[10:28], cname[:])
	return p
}

// WhatsAppSourceDescription sets the profile bit when profileExtension is
// set.
func WhatsAppSourceDescription(localSSRC uint32, cname [WhatsAppCNameLen]byte, profileExtension bool) [32]byte {
	p := SourceDescription(localSSRC, cname)
	if profileExtension {
		p[0] |= 0x10
	}
	return p
}

// EmptySourceDescription is the 12-byte SDES sessions without a CNAME send.
func EmptySourceDescription(localSSRC uint32) [12]byte {
	var p [12]byte
	p[0], p[1], p[3] = 0x81, PTSDES, 2
	binary.BigEndian.PutUint32(p[4:], localSSRC)
	return p
}

// SenderReportWithSDES is the periodic compound SR + SDES.
func SenderReportWithSDES(localSSRC uint32, s SenderStats, nowMS uint64, cname [WhatsAppCNameLen]byte) []byte {
	return SenderReportWithSDESAndReports(localSSRC, s, nowMS, cname, nil)
}

// SenderReportWithSDESAndReports is SR (with up to 31 standard report
// blocks) + SDES.
func SenderReportWithSDESAndReports(localSSRC uint32, s SenderStats, nowMS uint64, cname [WhatsAppCNameLen]byte, reports []ReceptionReport) []byte {
	sdes := SourceDescription(localSSRC, cname)
	return append(senderReportWithReports(localSSRC, s, nowMS, reports), sdes[:]...)
}

// WhatsAppSenderReportWithSDES is the native 1:1 compound: the SR with at
// most one report block followed by 24 zero bytes of WhatsApp statistics,
// then SDES; profileExtension sets bit 4 on both.
func WhatsAppSenderReportWithSDES(localSSRC uint32, s SenderStats, nowMS uint64, cname [WhatsAppCNameLen]byte, report *ReceptionReport, profileExtension bool) []byte {
	sr := SenderReport(localSSRC, s, nowMS)
	p := append([]byte{}, sr[:]...)
	if profileExtension {
		p[0] |= 0x10
	}
	if report != nil {
		p[0] |= 1
		p = appendReceptionReport(p, *report)
		p = append(p, make([]byte, waReportExtLen)...)
		setLength(p)
	}
	sdes := WhatsAppSourceDescription(localSSRC, cname, profileExtension)
	return append(p, sdes[:]...)
}
