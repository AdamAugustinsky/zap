// Ported from whatsapp-rust 5e72baf wacore/src/voip/driver.rs (run_call
// and its send queue and video pacer; the group controls and the relay
// reconnect, which a 1:1 engine never asks for, are not ported), MIT.

package engine

import (
	"context"
	"time"

	"zapfast/gocore/voip/demux"
	"zapfast/gocore/voip/relay"
	"zapfast/gocore/voip/rtp"
)

// PeerAnswer is what the callee's <accept> teaches the caller: the device
// that answered and, when its capability says, the codec.
type PeerAnswer struct {
	AnsweringLID string
	Codec        AudioCodec
	HasCodec     bool
}

// VideoInput is one encoded access unit and its RTP-clock capture time,
// under the input generation it was made for.
type VideoInput struct {
	Data       []byte
	Timestamp  uint32
	Generation uint64
}

// ControlKind tells the video controls apart.
type ControlKind int

const (
	// CtlInputGeneration: only VideoInput of Generation is sent from now.
	CtlInputGeneration ControlKind = iota
	// CtlTimestampStride: the source's nominal cadence (Stride).
	CtlTimestampStride
	// CtlEnable: the video plane up, outbound allowed.
	CtlEnable
	// CtlEnableAwaitingAccept: up, outbound held until CtlEnable.
	CtlEnableAwaitingAccept
	// CtlDisable: the plane down, queued video purged.
	CtlDisable
	// CtlDisableOutbound: our camera off, the peer's still decoding.
	CtlDisableOutbound
	// CtlRequireKeyframe: a new source; the next AU must be an IDR.
	CtlRequireKeyframe
	// CtlRequestPeerKeyframe: a PLI to the peer (Urgency).
	CtlRequestPeerKeyframe
	// CtlOrientation: the peer's orientation (Orientation).
	CtlOrientation
)

// Control is one mid-call video control.
type Control struct {
	Kind        ControlKind
	Generation  uint64
	Stride      uint32
	Urgency     KeyframeUrgency
	Orientation uint8
}

// Channels are what the driver bridges to the host. Nil channels are never
// ready. Speaker and VideoOut must not block: false is a frame the sink
// could not take, counted as dropped. Events must not block or lose an
// event.
type Channels struct {
	Mic      <-chan []int16
	Speaker  func([]int16) bool
	Rekey    <-chan PeerAnswer
	Video    <-chan VideoInput
	VideoOut func(VideoFrame) (full bool)
	Control  <-chan Control
	Events   func(Event)
	// Stats, when set, gets the counters whenever they moved.
	Stats func(Stats)
}

// CloseKind is why a drive ended.
type CloseKind int

const (
	// CloseLocal: the context ended (a hangup).
	CloseLocal CloseKind = iota
	// CloseRelayDisconnected: the relay channel went away.
	CloseRelayDisconnected
	// CloseSendFailed: a relay write failed.
	CloseSendFailed
	// CloseTerminated: the engine surfaced a terminal relay failure (its
	// event says which).
	CloseTerminated
	// CloseSetupFailed: the call's keys broke mid-call.
	CloseSetupFailed
)

// CloseReason is why Run returned.
type CloseReason struct {
	Kind CloseKind
	Msg  string
}

// The send queue's bounds.
const (
	sendQueueBatchCap = 64
	sendQueueByteCap  = 2 << 20
)

type batchKind int

const (
	batchControl batchKind = iota
	batchMedia
	batchVideo
	batchVideoRetransmission
)

func (k batchKind) isVideo() bool { return k == batchVideo || k == batchVideoRetransmission }

type sendBatch struct {
	packets  [][]byte
	bytes    int
	kind     batchKind
	started  bool
	keyframe bool
}

func packetBatch(p []byte) *sendBatch {
	k := batchMedia
	if demux.Classify(p) == demux.KindStun {
		k = batchControl
	}
	return &sendBatch{packets: [][]byte{p}, bytes: len(p), kind: k}
}

func videoBatch(packets [][]byte) *sendBatch {
	b := &sendBatch{packets: packets, kind: batchVideo}
	for _, p := range packets {
		b.bytes += len(p)
	}
	if len(packets) > 0 {
		if h, ok := rtp.ParseHeader(packets[0]); ok && h.HasVideo {
			b.keyframe = h.Video.MediaFrameInfo&rtp.VideoMediaFrameInfoIDR != 0
		}
	}
	return b
}

type dropped struct{ videoAUs, packets uint32 }

func (d *dropped) add(b *sendBatch) {
	d.packets += uint32(len(b.packets))
	if b.kind == batchVideo {
		d.videoAUs++
	}
}

// keyframeGate holds outbound video until an IDR; raised counts the
// requirements, so one satisfied and raised again in a single iteration is
// still a new one.
type keyframeGate struct {
	awaiting bool
	raised   uint64
}

func (g *keyframeGate) raise() {
	if !g.awaiting {
		g.awaiting = true
		g.raised++
	}
}

// sendQueue is the driver's outbound queue: whole access units as one
// unit, so backpressure keeps or drops a complete AU, control first, then
// audio, then (paced) video.
type sendQueue struct {
	batches []*sendBatch
	pending [][]byte // the video packets of an AU still being drained
	gate    keyframeGate
}

func (q *sendQueue) bytes() (n int) {
	for _, b := range q.batches {
		n += b.bytes
	}
	return n
}

func (q *sendQueue) remove(i int) *sendBatch {
	b := q.batches[i]
	q.batches = append(q.batches[:i], q.batches[i+1:]...)
	return b
}

func (q *sendQueue) find(f func(*sendBatch) bool) int {
	for i, b := range q.batches {
		if f(b) {
			return i
		}
	}
	return -1
}

// discardVideoUntilKeyframe drops unstarted video AUs up to the next IDR;
// true when none is queued (the gate must stay raised).
func (q *sendQueue) discardVideoUntilKeyframe(d *dropped) bool {
	for {
		i := q.find(func(b *sendBatch) bool { return !b.started && b.kind == batchVideo })
		if i < 0 {
			return true
		}
		if q.batches[i].keyframe {
			return false
		}
		d.add(q.remove(i))
	}
}

func (q *sendQueue) shedToCap() dropped {
	var d dropped
	for len(q.batches) > sendQueueBatchCap || q.bytes() > sendQueueByteCap {
		i := -1
		for _, k := range []batchKind{batchVideoRetransmission, batchVideo, batchMedia} {
			if i = q.find(func(b *sendBatch) bool { return !b.started && b.kind == k }); i >= 0 {
				break
			}
		}
		if i < 0 {
			i = q.find(func(b *sendBatch) bool { return !b.started })
		}
		if i < 0 {
			break
		}
		b := q.remove(i)
		d.add(b)
		if b.kind == batchVideo {
			if q.discardVideoUntilKeyframe(&d) {
				q.gate.raise()
			} else {
				q.gate.awaiting = false
			}
		}
	}
	return d
}

func (q *sendQueue) enqueue(b *sendBatch) dropped {
	if b.kind == batchVideo && q.gate.awaiting {
		if !b.keyframe {
			var d dropped
			d.add(b)
			return d
		}
		q.gate.awaiting = false
	}
	q.batches = append(q.batches, b)
	return q.shedToCap()
}

// transmit queues one engine Transmit: the video packets of an AU gather
// until its marker.
func (q *sendQueue) transmit(p []byte) dropped {
	if h, ok := rtp.ParseHeader(p); ok && h.PayloadType == rtp.PayloadTypeH264 {
		q.pending = append(q.pending, p)
		if h.Marker {
			return q.flushPending()
		}
		return dropped{}
	}
	d := q.flushPending()
	more := q.enqueue(packetBatch(p))
	return dropped{d.videoAUs + more.videoAUs, d.packets + more.packets}
}

func (q *sendQueue) flushPending() dropped {
	if len(q.pending) == 0 {
		return dropped{}
	}
	b := videoBatch(q.pending)
	q.pending = nil
	return q.enqueue(b)
}

func (q *sendQueue) purge(discard func(*sendBatch) bool) dropped {
	var d dropped
	kept := q.batches[:0]
	for _, b := range q.batches {
		if discard(b) {
			d.add(b)
		} else {
			kept = append(kept, b)
		}
	}
	q.batches = kept
	if len(q.pending) > 0 {
		d.videoAUs++
		d.packets += uint32(len(q.pending))
		q.pending = nil
	}
	if d.videoAUs != 0 {
		q.gate.raise()
	}
	return d
}

// pop is the next packet to send: control, then audio, then video while
// the pacer allows.
func (q *sendQueue) pop(videoReady bool) ([]byte, batchKind, bool) {
	i := q.find(func(b *sendBatch) bool { return b.kind == batchControl })
	if i < 0 {
		i = q.find(func(b *sendBatch) bool { return b.kind == batchMedia })
	}
	if i < 0 && videoReady {
		i = q.find(func(b *sendBatch) bool { return b.kind.isVideo() })
	}
	if i < 0 {
		return nil, 0, false
	}
	b := q.batches[i]
	b.started = true
	p := b.packets[0]
	b.packets = b.packets[1:]
	b.bytes -= len(p)
	if len(b.packets) == 0 {
		q.remove(i)
	}
	return p, b.kind, true
}

func (q *sendQueue) hasVideo() bool {
	return q.find(func(b *sendBatch) bool { return b.kind.isVideo() }) >= 0
}

// The video pacer: 2.5 times the measured video rate (WebRTC's factor),
// never below 1 Mbit/s, so a keyframe leaves over a few frame intervals
// instead of as one burst the relay's path takes as loss.
const (
	videoPaceFactorNum = 5
	videoPaceFactorDen = 2
	videoPaceFloorBPS  = 1_000_000
	videoPaceWindowMS  = 1_000
	videoPaceBurstUS   = 5_000
)

type pacedSend struct {
	at    Millis
	bytes int
}

type videoPacer struct {
	sent      []pacedSend
	sentBytes int
	nextUS    uint64
}

func (p *videoPacer) ready(now Millis) bool { return now*1000+videoPaceBurstUS >= p.nextUS }

func (p *videoPacer) readyAt() Millis {
	return (satSub(p.nextUS, videoPaceBurstUS) + 999) / 1000
}

func (p *videoPacer) rateBPS() uint64 {
	measured := uint64(p.sentBytes) * 8 * 1000 / videoPaceWindowMS
	return max(measured*videoPaceFactorNum/videoPaceFactorDen, videoPaceFloorBPS)
}

func (p *videoPacer) note(now Millis, bytes int) {
	for len(p.sent) > 0 && now-p.sent[0].at >= videoPaceWindowMS {
		p.sentBytes -= p.sent[0].bytes
		p.sent = p.sent[1:]
	}
	p.sent = append(p.sent, pacedSend{now, bytes})
	p.sentBytes += bytes
	start := max(p.nextUS, now*1000)
	p.nextUS = start + uint64(bytes)*8*1_000_000/p.rateBPS()
}

// Run drives one call over t until ctx ends, the relay channel goes, a
// send fails or the engine terminates. It starts the engine, and closes t
// on the way out.
func Run(ctx context.Context, t relay.Transport, ch Channels, e *Engine) CloseReason {
	epoch := time.Now()
	return run(ctx, t, ch, e, func() Millis { return Millis(time.Since(epoch).Milliseconds()) }, uint64(time.Now().UnixMilli()))
}

type sendJob struct {
	packet []byte
}

func run(ctx context.Context, t relay.Transport, ch Channels, e *Engine, now func() Millis, wall uint64) (reason CloseReason) {
	defer t.Close()
	e.Start(now(), wall)

	// One write in flight at a time, on its own goroutine, so a slow relay
	// write never holds up reception or playout.
	jobs := make(chan sendJob)
	results := make(chan error, 1)
	sendCtx, stopSend := context.WithCancel(context.Background())
	defer stopSend()
	go func() {
		for {
			select {
			case <-sendCtx.Done():
				return
			case j := <-jobs:
				err := t.Send(sendCtx, j.packet)
				select {
				case results <- err:
				case <-sendCtx.Done():
					return
				}
			}
		}
	}()
	inFlight := false

	var q sendQueue
	var pacer videoPacer
	var announced uint64
	var haveAnnounced bool
	var published Stats
	var videoGeneration uint64
	timer := time.NewTimer(time.Hour)
	defer timer.Stop()
	paceTimer := time.NewTimer(time.Hour)
	defer paceTimer.Stop()
	events := t.Events()
	mic, rekey, video, control := ch.Mic, ch.Rekey, ch.Video, ch.Control

	overdue := func() {
		if at, ok := e.PollTimeout(); ok && at != Never && now() >= at {
			e.Timeout(now())
		}
	}
	report := func(d dropped) {
		if d.packets != 0 {
			ch.Events(Event{Kind: EventOutboundMediaDropped, VideoAUs: d.videoAUs, Packets: d.packets})
		}
	}

	for {
		if ch.Stats != nil {
			if s := e.Stats(); s != published {
				ch.Stats(s)
				published = s
			}
		}
		var sinkDropped, videoSinkDropped uint32
	drain:
		for {
			o := e.PollOutput()
			switch o.Kind {
			case OutTransmit:
				report(q.transmit(o.Packet))
			case OutRetransmitVideo:
				report(q.enqueue(&sendBatch{packets: [][]byte{o.Packet}, bytes: len(o.Packet), kind: batchVideoRetransmission}))
			case OutPlayout:
				if !ch.Speaker(o.PCM) {
					sinkDropped++
				}
			case OutVideo:
				if ch.VideoOut(o.Video) {
					videoSinkDropped++
				}
			case OutEvent:
				ch.Events(o.Event)
			case OutTimeout:
				report(q.flushPending())
				break drain
			}
		}
		if sinkDropped != 0 {
			e.NoteAudioSinkDropped(sinkDropped)
		}
		if videoSinkDropped != 0 {
			e.NoteVideoSinkDropped(videoSinkDropped)
			e.RequestPeerKeyframe(now(), Coalesced)
		}
		// The queue's own IDR requirement (a shed AU), asked for once,
		// only where there is a picture to unblock.
		if q.gate.awaiting && e.VideoSendActive() {
			if !haveAnnounced || announced != q.gate.raised {
				announced, haveAnnounced = q.gate.raised, true
				ch.Events(Event{Kind: EventVideoKeyframeNeeded})
			}
		} else {
			haveAnnounced = false
		}
		if e.Terminated() {
			if ch.Stats != nil {
				ch.Stats(e.Stats())
			}
			return CloseReason{Kind: CloseTerminated}
		}

		n := now()
		if !inFlight {
			if p, kind, ok := q.pop(pacer.ready(n)); ok {
				if kind.isVideo() {
					pacer.note(n, len(p))
				}
				inFlight = true
				jobs <- sendJob{p}
			}
		}
		var paceC <-chan time.Time
		if !inFlight && q.hasVideo() {
			paceTimer.Reset(time.Duration(satSub(pacer.readyAt(), now())) * time.Millisecond)
			paceC = paceTimer.C
		}
		var timerC <-chan time.Time
		if at, ok := e.PollTimeout(); ok && at != Never {
			timer.Reset(time.Duration(satSub(at, now())) * time.Millisecond)
			timerC = timer.C
		}

		// The rekey goes before any relay packet, so the answering
		// device's first packet decrypts under the right keys.
		select {
		case a, ok := <-rekey:
			rekey = nil
			if ok {
				if !e.applyAnswer(now(), a, &q, report) {
					return CloseReason{Kind: CloseSetupFailed, Msg: "the call key is malformed"}
				}
			}
			continue
		default:
		}

		select {
		case <-ctx.Done():
			return CloseReason{Kind: CloseLocal}
		case err := <-results:
			inFlight = false
			if err != nil {
				return CloseReason{Kind: CloseSendFailed, Msg: err.Error()}
			}
		case a, ok := <-rekey:
			rekey = nil
			if ok && !e.applyAnswer(now(), a, &q, report) {
				return CloseReason{Kind: CloseSetupFailed, Msg: "the call key is malformed"}
			}
		case c, ok := <-control:
			if !ok {
				control = nil
				break
			}
			switch c.Kind {
			case CtlInputGeneration:
				videoGeneration = c.Generation
			case CtlTimestampStride:
				e.SetVideoTimestampStride(c.Stride)
			case CtlEnable:
				e.EnableVideo()
			case CtlEnableAwaitingAccept:
				e.EnableVideoGated()
			case CtlDisable:
				e.DisableVideo()
				report(q.purgeUnstartedVideo())
			case CtlDisableOutbound:
				e.GateVideoOutbound()
				report(q.purgeUnstartedVideo())
			case CtlRequireKeyframe:
				e.RequireVideoKeyframe()
			case CtlRequestPeerKeyframe:
				e.RequestPeerKeyframe(now(), c.Urgency)
			case CtlOrientation:
				e.SetPeerVideoOrientation(c.Orientation)
			}
			overdue()
		case ev, ok := <-events:
			if !ok {
				return CloseReason{Kind: CloseRelayDisconnected, Msg: "the relay channel closed"}
			}
			switch ev.Kind {
			case relay.EventPacket:
				e.RelayPacket(now(), ev.Packet)
			case relay.EventInboundDropped:
				e.NoteInboundDropped(ev.Dropped)
			case relay.EventDisconnected:
				return CloseReason{Kind: CloseRelayDisconnected, Msg: ev.Reason.String()}
			}
			overdue()
		case pcm, ok := <-mic:
			if !ok {
				// A closed microphone must not end the call.
				mic = nil
				break
			}
			e.MicFrame(now(), pcm)
			overdue()
		case v, ok := <-video:
			if !ok {
				video = nil
				break
			}
			if v.Generation == videoGeneration {
				e.VideoFrame(now(), v.Data, v.Timestamp)
			}
			overdue()
		case <-timerC:
			e.Timeout(now())
		case <-paceC:
		}
	}
}

func (q *sendQueue) purgeUnstartedVideo() dropped {
	var d dropped
	kept := q.batches[:0]
	for _, b := range q.batches {
		if !b.started && b.kind.isVideo() {
			d.add(b)
		} else {
			kept = append(kept, b)
		}
	}
	q.batches = kept
	if d.videoAUs != 0 {
		q.gate.raise()
	}
	return d
}

// applyAnswer is the caller's rekey: the codec the peer's capability
// chose first (queued audio of the old grammar is retired), then the
// answer arms the watchdog and the keys move to the answering device.
func (e *Engine) applyAnswer(now Millis, a PeerAnswer, q *sendQueue, report func(dropped)) bool {
	if a.HasCodec {
		before := e.ActiveAudioFormat()
		if err := e.SwitchAudioCodec(a.Codec, Negotiated); err == nil && e.ActiveAudioFormat() != before {
			report(q.purge(func(b *sendBatch) bool { return !b.started && b.kind == batchMedia }))
		}
	}
	e.PeerAnswered(now)
	return e.RekeyRecv(a.AnsweringLID)
}
