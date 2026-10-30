// Ported from whatsapp-rust 5e72baf wacore/src/voip/session.rs, MIT.

package engine

import (
	"math"
	"slices"

	"zapfast/gocore/voip/h264"
	"zapfast/gocore/voip/kdf"
	"zapfast/gocore/voip/rtcp"
	"zapfast/gocore/voip/rtp"
	"zapfast/gocore/voip/srtp"
)

// srtcpSender is one stream's RTCP sender state: its SRTCP keys and index
// (libSRTP increments before the first packet, so it starts at 1), its
// CNAME and the counters its sender reports carry.
type srtcpSender struct {
	keys       srtp.Keys
	cname      [rtcp.WhatsAppCNameLen]byte
	index      uint32
	packets    uint32
	octets     uint32
	profileExt bool
}

func newSRTCPSender(callKey []byte, selfLID string, cname [rtcp.WhatsAppCNameLen]byte, profileExt bool) (srtcpSender, bool) {
	k, ok := srtp.DeriveSRTCPKeys(callKey, kdf.FormatParticipantID(selfLID))
	return srtcpSender{keys: k, cname: cname, index: 1, profileExt: profileExt}, ok
}

func (s *srtcpSender) record(octets int) {
	s.packets++
	s.octets += uint32(octets)
}

func (s *srtcpSender) protect(ssrc uint32, plain []byte) []byte {
	out := srtp.ProtectSRTCP(&s.keys, ssrc, s.index, plain)
	s.index++
	return out
}

func (s *srtcpSender) pli(ssrc, media uint32) []byte {
	p := rtcp.WhatsAppPictureLossIndication(ssrc, media, s.profileExt)
	return s.protect(ssrc, p[:])
}

func (s *srtcpSender) sourceDescription(ssrc uint32) []byte {
	p := rtcp.WhatsAppSourceDescription(ssrc, s.cname, s.profileExt)
	return s.protect(ssrc, p[:])
}

func (s *srtcpSender) senderReport(ssrc, ts uint32, nowMS uint64, report *rtcp.ReceptionReport) []byte {
	stats := rtcp.SenderStats{PacketsSent: s.packets, OctetsSent: s.octets, RTPTimestamp: ts}
	return s.protect(ssrc, rtcp.WhatsAppSenderReportWithSDES(ssrc, stats, nowMS, s.cname, report, s.profileExt))
}

// e2eKeys derives a participant's SRTP and SRTCP keys.
func e2eKeys(callKey []byte, lid string) (rtpKeys, rtcpKeys srtp.Keys, ok bool) {
	id := kdf.FormatParticipantID(lid)
	r, ok1 := srtp.DeriveE2EKeys(callKey, id)
	c, ok2 := srtp.DeriveSRTCPKeys(callKey, id)
	return r, c, ok1 && ok2
}

// mediaPipeline is the audio half: protect with our keys, unprotect with
// the peer's (the HKDF info is the sender's own participant id), and the
// receive state of the peer's other streams, which can carry its audio
// piggybacked.
type mediaPipeline struct {
	sendKeys, recvKeys srtp.Keys
	tagLen             int
	rtp                *rtp.Stream
	sendROC            srtp.SendROC
	recv               srtp.RecvStreams
	auxRecv            srtp.RecvStreams
	videoRecv          srtp.RecvStreams // the peer's video before a plane exists
	srtcp              srtcpSender
	recvSRTCPKeys      srtp.Keys
	recvSRTCPReplay    srtp.SRTCPReplay
}

func newMediaPipeline(callKey []byte, selfLID, peerLID string, ssrc, samplesPerPacket uint32, tagLen int, cname [rtcp.WhatsAppCNameLen]byte) (*mediaPipeline, bool) {
	if tagLen < 1 || tagLen > 20 {
		return nil, false
	}
	send, _, ok1 := e2eKeys(callKey, selfLID)
	recv, recvRTCP, ok2 := e2eKeys(callKey, peerLID)
	sender, ok3 := newSRTCPSender(callKey, selfLID, cname, false)
	if !ok1 || !ok2 || !ok3 {
		return nil, false
	}
	return &mediaPipeline{
		sendKeys: send, recvKeys: recv, tagLen: tagLen, rtp: rtp.NewStream(ssrc, samplesPerPacket, false),
		srtcp: sender, recvSRTCPKeys: recvRTCP,
	}, true
}

func (m *mediaPipeline) sendSSRC() uint32 { return m.rtp.SSRC }

// protectAudio frames one codec payload: the WARP RTP header, AES-CTR,
// the MI tag.
func (m *mediaPipeline) protectAudio(payload []byte) []byte {
	h := m.rtp.NextPacket(payload, false)
	roc := m.sendROC.Advance(h.SequenceNumber)
	m.srtcp.record(len(payload))
	return srtp.ProtectRTP(&m.sendKeys, h, roc, m.tagLen, payload)
}

func (m *mediaPipeline) unprotectAudio(p []byte) (rtp.Header, []byte, error) {
	return m.recv.Unprotect(&m.recvKeys, m.tagLen, p)
}

// acceptCarriedAudio admits audio carried inside another authenticated
// packet against the audio stream's own replay and rollover state.
func (m *mediaPipeline) acceptCarriedAudio(h rtp.Header) bool {
	roc := m.recv.EstimateROC(h.SSRC, h.SequenceNumber)
	if roc == math.MaxUint32 {
		return false
	}
	return m.recv.Accept(h.SSRC, h.SequenceNumber, roc)
}

func (m *mediaPipeline) unprotectAuxiliary(p []byte) (rtp.Header, []byte, error) {
	return m.auxRecv.Unprotect(&m.recvKeys, m.tagLen, p)
}

func (m *mediaPipeline) unprotectVideoWithoutPlane(p []byte) (rtp.Header, []byte, error) {
	return m.videoRecv.Unprotect(&m.recvKeys, m.tagLen, p)
}

func (m *mediaPipeline) unprotectRTCP(p []byte) ([]byte, bool) {
	sender, ok := rtcp.SenderSSRC(p)
	if !ok {
		return nil, false
	}
	plain, index, ok := srtp.UnprotectSRTCP(&m.recvSRTCPKeys, sender, p)
	if !ok || !m.recvSRTCPReplay.Accept(sender, index) {
		return nil, false
	}
	return plain, true
}

// rekeyRecv re-derives the receive keys for the device that answered.
func (m *mediaPipeline) rekeyRecv(callKey []byte, lid string) bool {
	r, c, ok := e2eKeys(callKey, lid)
	if !ok {
		return false
	}
	m.recvKeys, m.recvSRTCPKeys = r, c
	m.recv, m.auxRecv, m.videoRecv, m.recvSRTCPReplay = srtp.RecvStreams{}, srtp.RecvStreams{}, srtp.RecvStreams{}, srtp.SRTCPReplay{}
	return true
}

// rekeyRecvPreservingROC installs a group epoch for one participant
// without resetting its authenticated RTP rollover or SRTCP replay state.
func (m *mediaPipeline) rekeyRecvPreservingROC(epoch []byte, lid string) bool {
	r, c, ok := e2eKeys(epoch, lid)
	if ok {
		m.recvKeys, m.recvSRTCPKeys = r, c
	}
	return ok
}

// sendRekey is a group epoch's send keys, derived before any pipeline
// changes so a failure leaves every pipeline on the old epoch.
type sendRekey struct{ rtp, rtcp srtp.Keys }

func prepareSendRekey(epoch []byte, selfLID string) (sendRekey, bool) {
	r, c, ok := e2eKeys(epoch, selfLID)
	return sendRekey{r, c}, ok
}

// commitSendRekey swaps the send keys; the RTP sequence, timestamp and ROC
// and the SRTCP index, CNAME and counters carry on.
func (m *mediaPipeline) commitSendRekey(k sendRekey) { m.sendKeys, m.srtcp.keys = k.rtp, k.rtcp }

func (m *mediaPipeline) setSendSSRC(ssrc uint32) { m.rtp.SSRC = ssrc }

func (m *mediaPipeline) audioSenderReport(nowMS uint64, report *rtcp.ReceptionReport) []byte {
	return m.srtcp.senderReport(m.rtp.SSRC, m.rtp.RTPTimestamp(), nowMS, report)
}

func (m *mediaPipeline) audioSourceDescription() []byte { return m.srtcp.sourceDescription(m.rtp.SSRC) }

// How a renumbering peer's stragglers are told from its current stream.
const (
	retiredSSRCGracePackets  = 64
	retiredSSRCMemory        = 4
	retiredSSRCResumePackets = 3
)

// completedAU is a reassembled access unit, its RTP timestamp and the
// rotation its frame info carried (-1 when none).
type completedAU struct {
	timestamp   uint32
	au          []byte
	orientation int
}

// videoPipeline is the video sibling: the same participant keys, H.264
// packetization, its own SSRC and sequencer, and reassembly that follows
// one inbound stream at a time.
type videoPipeline struct {
	sendKeys, recvKeys srtp.Keys
	tagLen             int
	rtp                *rtp.VideoStream
	sendROC            srtp.SendROC
	recv               srtp.RecvStreams
	depack             h264.Depacketizer
	orientTS           uint32
	orient             int
	haveOrient         bool
	depackSSRC         uint32
	haveDepackSSRC     bool
	retired            []uint32
	contender          uint32
	haveContender      bool
	sinceChange        uint32
	retiredRun         uint32
	scratch            h264.PacketizedAU
	srtcp              srtcpSender
}

func newVideoPipeline(callKey []byte, selfLID, peerLID string, ssrc, stride uint32, tagLen int, cname [rtcp.WhatsAppCNameLen]byte) (*videoPipeline, bool) {
	if tagLen < 1 || tagLen > 20 || stride == 0 {
		return nil, false
	}
	send, _, ok1 := e2eKeys(callKey, selfLID)
	recv, _, ok2 := e2eKeys(callKey, peerLID)
	sender, ok3 := newSRTCPSender(callKey, selfLID, cname, true)
	vs, ok4 := rtp.NewVideoStream(ssrc, stride)
	if !ok1 || !ok2 || !ok3 || !ok4 {
		return nil, false
	}
	return &videoPipeline{sendKeys: send, recvKeys: recv, tagLen: tagLen, rtp: vs, srtcp: sender}, true
}

func (v *videoPipeline) sendSSRC() uint32 { return v.rtp.SSRC }

func (v *videoPipeline) inboundSSRC() (uint32, bool) { return v.depackSSRC, v.haveDepackSSRC }

// pli asks the stream being reassembled for a keyframe; none before one
// authenticated.
func (v *videoPipeline) pli() ([]byte, bool) {
	if !v.haveDepackSSRC {
		return nil, false
	}
	return v.srtcp.pli(v.rtp.SSRC, v.depackSSRC), true
}

func (v *videoPipeline) senderReport(nowMS uint64, report *rtcp.ReceptionReport) []byte {
	return v.srtcp.senderReport(v.rtp.SSRC, v.rtp.RTPTimestamp(), nowMS, report)
}

func (v *videoPipeline) rekeyRecv(callKey []byte, lid string) bool {
	r, _, ok := e2eKeys(callKey, lid)
	if !ok {
		return false
	}
	v.recvKeys = r
	v.recv = srtp.RecvStreams{}
	v.resetDepacketizer()
	return true
}

func (v *videoPipeline) rekeyRecvPreservingROC(epoch []byte, lid string) bool {
	r, _, ok := e2eKeys(epoch, lid)
	if ok {
		v.recvKeys = r
	}
	return ok
}

func (v *videoPipeline) commitSendRekey(k sendRekey) { v.sendKeys, v.srtcp.keys = k.rtp, k.rtcp }

func (v *videoPipeline) setSendSSRC(ssrc uint32) { v.rtp.SSRC = ssrc }

// resetReassembly drops reassembly but remembers which streams the peer
// left (a change on our side).
func (v *videoPipeline) resetReassembly() {
	v.depack.Reset()
	v.haveOrient, v.haveDepackSSRC, v.haveContender = false, false, false
	v.sinceChange, v.retiredRun = 0, 0
}

func (v *videoPipeline) resetDepacketizer() {
	v.resetReassembly()
	v.retired = v.retired[:0]
}

// adoptRecvState takes the video receive state collected before a plane
// existed, so its rollover counter and replay window carry on.
func (v *videoPipeline) adoptRecvState(m *mediaPipeline) {
	v.recv, m.videoRecv = m.videoRecv, srtp.RecvStreams{}
}

// protectVideo packetizes one Annex-B access unit and protects each packet.
func (v *videoPipeline) protectVideo(au []byte) [][]byte {
	if len(au) > h264.MaxAUBytes {
		return nil
	}
	h264.Packetize(au, &v.scratch)
	info := rtp.VideoMediaFrameInfoDelta
	if h264.AUHasIDR(au) {
		info = rtp.VideoMediaFrameInfoIDR
	}
	n := v.scratch.Len()
	out := make([][]byte, 0, n)
	for i := range n {
		payload := v.scratch.At(i)
		h := v.rtp.NextVideoPacket(i == n-1, info)
		roc := v.sendROC.Advance(h.SequenceNumber)
		v.srtcp.record(len(payload))
		out = append(out, srtp.ProtectRTP(&v.sendKeys, h, roc, v.tagLen, payload))
	}
	return out
}

func (v *videoPipeline) unprotectWithoutPicture(p []byte) (rtp.Header, []byte, error) {
	return v.recv.Unprotect(&v.recvKeys, v.tagLen, p)
}

// unprotectSplit authenticates and decrypts one video packet, splits off
// the audio it carried, and feeds the rest to reassembly.
func (v *videoPipeline) unprotectSplit(p []byte) (rtp.Header, []completedAU, []byte, error) {
	h, payload, err := v.recv.Unprotect(&v.recvKeys, v.tagLen, p)
	if err != nil {
		return h, nil, nil, err
	}
	media, audio, split := rtp.SplitAudioPiggyback(p, payload)
	if split {
		payload = media
	} else {
		audio = nil
	}
	v.sinceChange++
	if v.haveDepackSSRC && v.depackSSRC == h.SSRC {
		v.retiredRun = 0
	} else {
		if slices.Contains(v.retired, h.SSRC) {
			if !v.haveContender || v.contender != h.SSRC {
				v.contender, v.haveContender, v.retiredRun = h.SSRC, true, 0
			}
			v.retiredRun++
			if v.sinceChange <= retiredSSRCGracePackets || v.retiredRun < retiredSSRCResumePackets {
				return h, nil, audio, nil
			}
		}
		if v.haveDepackSSRC {
			v.depack.Reset()
			v.haveOrient = false
			left := v.depackSSRC
			v.retired = slices.DeleteFunc(v.retired, func(s uint32) bool { return s == left })
			if len(v.retired) == retiredSSRCMemory {
				v.retired = v.retired[1:]
			}
			v.retired = append(v.retired, left)
		}
		v.retired = slices.DeleteFunc(v.retired, func(s uint32) bool { return s == h.SSRC })
		v.depackSSRC, v.haveDepackSSRC = h.SSRC, true
		v.sinceChange, v.retiredRun, v.haveContender = 0, 0, false
	}
	prevTS, prevOrient, prevHave := v.orientTS, v.orient, v.haveOrient
	rotation := -1
	if info, ok := rtp.ParseMediaFrameInfo(p); ok {
		rotation = int(info & 3)
	}
	switch {
	case v.haveOrient && v.orientTS == h.Timestamp:
		if rotation >= 0 {
			v.orient = rotation
		}
	case v.haveOrient && int32(h.Timestamp-v.orientTS) <= 0:
	default:
		v.orientTS, v.orient, v.haveOrient = h.Timestamp, rotation, true
	}
	var done []completedAU
	ts, au, ok := v.depack.Push(h.SequenceNumber, h.Timestamp, payload, h.Marker)
	for ok {
		o := -1
		switch {
		case v.haveOrient && v.orientTS == ts:
			o = v.orient
		case prevHave && prevTS == ts:
			o = prevOrient
		}
		done = append(done, completedAU{timestamp: ts, au: au, orientation: o})
		ts, au, ok = v.depack.PopReady()
	}
	return h, done, audio, nil
}
