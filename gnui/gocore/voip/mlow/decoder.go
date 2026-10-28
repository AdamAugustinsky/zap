// Ported from whatsapp-rust 5e72baf wacore/src/voip/mlow/decoder.rs, MIT.
//
// MLow top-level decoder: RED strip -> TOC routing -> active-frame decode (chained 20 ms internal
// frames: LSF -> pulses -> pitch/gains -> CELP synthesis) -> PCM. The synthesis
// (`smpl_celpdec`) runs the excitation in the codec's float domain (gen_noise + LPC synthesis). The
// cross-frame predictor and synthesis history persist across calls because the stream is
// continuous.
//
// Go port note: Rust's decode/decode_inner/decode_multiframe/decode_frame/decode_active_frame each
// return a fresh Vec<f32>. Here each takes a dst slice and appends its PCM to it, so a caller that
// reuses its buffer decodes without allocating; the structure and the order of the work are
// otherwise unchanged.

package mlow

import (
	"context"
	"fmt"
	"log/slog"
)

// outputSamplesPerMs: the decoder synthesises at 16 kHz regardless of the internal rate a TOC
// declares.
const outputSamplesPerMs int32 = 16

// maxPacketSamps is the longest audio one packet may carry, in samples at 16 kHz.
//
// 120 ms, which is both the longest duration a TOC can declare and the ceiling the shipped
// client applies when it totals a multiframe envelope. It is a hard bound and not a hint: without
// it an envelope of eighteen sub-frames turns a few hundred bytes into tens of thousands of
// samples, and the jitter buffer on the other side of this function has no way to tell that
// expansion from real audio.
const maxPacketSamps = 1920

// decoderLevelTrace stands in for log::trace!, which slog has no level for.
const decoderLevelTrace = slog.LevelDebug - 4

// decoderLogEnabled guards the debug/trace logs that sit on per-frame paths, so their arguments
// are only boxed (and allocated) when someone is listening.
func decoderLogEnabled(l slog.Level) bool {
	return slog.Default().Enabled(context.Background(), l)
}

// silenceSamps is the number of samples a frame that produced no audio should occupy.
//
// Its own declared duration, not a fixed 60 ms slot. A fixed slot makes a run of concealed 20 ms
// frames arrive at three times real time, which floods the jitter buffer and gets speech trimmed
// off its head to hold the latency ceiling: the concealment evicts the audio that did decode.
func silenceSamps(declared int) int {
	return min(max(declared, 1), maxPacketSamps)
}

// internalFrames is the number of internal 20 ms frames chained inside one packet, or ok=false for
// a duration this decoder cannot run. A packet is not a single unit of decode: the reference
// derives the loop count from the declared duration while the geometry inside each iteration stays
// fixed, so 20/60/120 ms differ only in how many times the same decode repeats.
//
// 10 ms is the exception and stays unsupported: it halves the internal frame length and the
// subframe count, which the synthesis does not implement, and decoding it under the wrong geometry
// would consume the payload with the wrong symbol count and desync the range coder.
func internalFrames(frameMs int32) (int, bool) {
	if frameMs > 10 {
		return int((frameMs + 10) / 20), true
	}
	return 0, false
}

// endpointIsValid: does a decode that ended after `consumed` bytes of a `storage`-byte body land
// where a valid stream can end? Under-running is always malformed; the upper slack absorbs the
// range coder's final carry bytes, which the encoder does not emit.
//
// The bound is four, taken from the shipped decoder rather than from the C fork:
// `WhatsAppNative.dll` @ `0x180337e30` does `add r8d, 0x4` before its second compare, while the
// fork's `smpl_check_end_result` allows only `+2`. Two is the stricter of the pair, so it would
// conceal frames the official client plays — a false report of corruption, silencing audio that
// is fine.
func endpointIsValid(storage, consumed uint32) bool {
	return storage <= consumed && consumed <= storage+4
}

// FrameReport is what one call to [Decoder.Decode] did with the packet it was given (Rust
// MlowFrameReport).
//
// The decoder answers with PCM either way, so without this a concealed frame, a frame outside the
// operating point and a frame of genuine background noise are the same observation: silence. That
// ambiguity is what let issue #1105 stay open, and it is the whole reason the engine counts.
//
// Counts rather than flags because one packet can chain several internal frames, and a multiframe
// envelope can chain several packets.
type FrameReport struct {
	// Decoded counts frames that produced coded audio.
	Decoded uint8
	// Concealed counts frames concealed: an empty payload, a RED envelope that would not unwrap,
	// or a body whose decode did not end where the body said it should.
	Concealed uint8
	// OffPoint counts frames refused by the operating-point guard, including a standard-Opus
	// escape this decoder does not run.
	OffPoint uint8
	// InactiveOrSID counts frames that carried no coded voice (SID / comfort noise).
	InactiveOrSID uint8
}

// Decoder is the stateful pure-Go MLow decoder (Rust MlowDecoder). It decodes one RTP payload (a
// bare MLow frame, or a SplitRed packet when redundancy was negotiated) into a PCM frame at
// 16 kHz, one 20 ms internal frame per chained frame in the packet.
type Decoder struct {
	state      smplDecoderState
	redundancy int32
	// hadErrorFlag (Rust `had_error`; renamed because Go cannot have a field and a method of one
	// name) is sticky: set whenever the inner range decoder raised its error flag during any
	// decode. That flag reflects a degenerate decode table, not arbitrary frame corruption
	// (over-reads return zero silently), so it does not detect a tampered payload. Read via
	// `hadError`. Diagnostic only, never gates output.
	hadErrorFlag bool
	// droppedUnsupported counts inbound frames dropped because they fall outside this decoder's
	// single operating point (16kHz wideband, low_rate=0, and a duration whose internal geometry
	// it implements). Such a frame would desync the range coder if decoded, so it is dropped
	// (treated as a lost frame). The count drives a once + every-100th `warn` naming the offending
	// dimension.
	droppedUnsupported uint32
	// malformed counts frames concealed because the decode did not end where the body said it
	// should. Drives a once + every-100th `warn`, so a peer sending a stream this decoder cannot
	// read is visible in a log rather than silently quiet.
	malformed uint32
	// stateBackup is a snapshot of the part of `state` an active decode advances, so a malformed
	// body can be undone.
	//
	// Owned by the decoder and reused: see the `save` in `decodeActiveFrame` for why this is a
	// field rather than a local, and [smplDecodeRollback] for what it leaves out.
	stateBackup smplDecodeRollback
	// report is what the in-flight `decode` call has done so far. Reset at the top of every
	// `decode` and drained by the caller through [Decoder.TakeFrameReport].
	report FrameReport
	// lastPacketSamps is the number of samples the last packet DECLARED, from its TOC, which is
	// not always what `decode` returned: a SID, a drop or a standard-Opus escape emits a fixed
	// slot regardless of duration. Consumers sizing a jitter cushion need the declared value,
	// since that is what sets arrival cadence.
	lastPacketSamps int
	// packetLags is decodeActiveFrame's per-packet lag buffer (a Vec::with_capacity per call in
	// Rust), kept here so steady-state decoding does not allocate.
	packetLags []float32
	// pulses is decodeSmplPulsesInto's scratch (Rust `decode_smpl_pulses` allocates its pulse
	// vector and position/magnitude lists per internal frame).
	pulses smplPulseScratch
	// nlsf receives each internal frame's reconstructed NLSF (a fresh Vec per frame in Rust)
	// before it is copied into `state.prevNlsf`.
	nlsf [smplOrder]float32
	// red and multiframe are the scratch for the RED depacketizer and the multiframe splitter
	// (Rust returns fresh Vecs from both).
	red        redScratch
	multiframe [maxFrames][]byte
}

// NewDecoder returns a decoder with fresh cross-frame state.
func NewDecoder() *Decoder {
	return &Decoder{
		state:           defaultSmplDecoderState(),
		stateBackup:     defaultSmplDecodeRollback(),
		redundancy:      0,
		hadErrorFlag:    false,
		report:          FrameReport{},
		lastPacketSamps: opusFrameSamps,
		packetLags:      make([]float32, 0, 6*8),
	}
}

// hadError reports whether any decode since construction (or `Reset`) raised the range decoder's
// error flag (a degenerate decode table). It does not flag a corrupted payload, which the decoder
// absorbs. Diagnostic only; consumed by the regression suites.
func (d *Decoder) hadError() bool {
	return d.hadErrorFlag
}

// LastPacketSamples is the number of samples in the most recent packet as its TOC declared them,
// independent of what `Decode` emitted for it. Starts at a 60 ms packet.
func (d *Decoder) LastPacketSamples() int {
	return d.lastPacketSamps
}

// TakeFrameReport takes what the last [Decoder.Decode] did, clearing it.
//
// Cleared on read so a caller that forgets to drain cannot silently accumulate one packet's
// verdict into the next one's.
func (d *Decoder) TakeFrameReport() FrameReport {
	r := d.report
	d.report = FrameReport{}
	return r
}

// SetRedundancy sets the negotiated RED redundancy level (0 = bare frames, the common case).
func (d *Decoder) SetRedundancy(n int32) {
	d.redundancy = n
}

// Reset clears the cross-frame state (call at a stream discontinuity).
func (d *Decoder) Reset() {
	d.state = defaultSmplDecoderState()
	d.stateBackup = defaultSmplDecodeRollback()
	d.hadErrorFlag = false
}

// decoderAppendZeros appends n zero samples to dst (Rust `vec![0.0; n]`).
func decoderAppendZeros(dst []float32, n int) []float32 {
	start := len(dst)
	dst = decoderGrowF32(dst, n)
	clear(dst[start:])
	return dst
}

// decoderGrowF32 extends dst by n elements (contents of the new tail unspecified), reallocating only
// when the capacity is short.
func decoderGrowF32(dst []float32, n int) []float32 {
	if need := len(dst) + n; need <= cap(dst) {
		return dst[:need]
	}
	out := make([]float32, len(dst)+n, 2*cap(dst)+n)
	copy(out, dst)
	return out
}

// Decode decodes one RTP MLow payload into a PCM frame, float in [-1, 1]. The sample count follows
// the packet's declared duration; a dropped or silenced frame yields a 60 ms slot. A nil or empty
// payload is a lost packet and is concealed with silence. It allocates the result; see
// [Decoder.AppendDecode] for the allocation-free form.
func (d *Decoder) Decode(packet []byte) []float32 {
	return d.AppendDecode(nil, packet)
}

// AppendDecode is [Decoder.Decode] appending the PCM to dst and returning the extended slice. It
// does not allocate in steady state when dst has the capacity for the packet.
func (d *Decoder) AppendDecode(dst []float32, payload []byte) []float32 {
	d.report = FrameReport{}
	if len(payload) == 0 {
		d.report.Concealed = satAddU8(d.report.Concealed, 1)
		return decoderAppendZeros(dst, opusFrameSamps)
	}
	if d.redundancy > 0 {
		frames, err := depackSplitRedInto(&d.red, payload)
		if err != nil {
			slog.Warn(fmt.Sprintf("mlow RED depacketization failed: %v", err))
			d.report.Concealed = satAddU8(d.report.Concealed, 1)
			return decoderAppendZeros(dst, opusFrameSamps)
		}
		// the main (current) frame is last; its slice borrows `payload`, so it can drive the
		// decode directly (no copy).
		//
		// Through `decodeInner`, not `decodeFrame`: a redundancy wrapper is chosen by the
		// payload type, and nothing stops the frame inside it from being a multiframe
		// envelope. Going straight to `decodeFrame` would read that envelope's indicator
		// as a SID and answer comfort noise -- the same total silence this triage exists to
		// prevent, just reached through the RED payload type instead of the bare one.
		if len(frames) > 0 {
			return d.decodeInner(dst, frames[len(frames)-1].data)
		}
		return d.decodeFrame(dst, nil)
	}
	return d.decodeInner(dst, payload)
}

// decodeInner routes one bare MLow payload: multiframe envelope, or a single frame.
//
// The envelope is checked before the TOC is read, because under the TOC grammar its first byte
// reads as a SID (bit 7 set). A decoder that skips this check answers every packet of such a
// call with comfort noise and reports nothing: total silence with no symptom at all.
func (d *Decoder) decodeInner(dst []float32, payload []byte) []float32 {
	if isMultiframe(payload) {
		return d.decodeMultiframe(dst, payload)
	}
	return d.decodeFrame(dst, payload)
}

// decodeMultiframe decodes every sub-frame of an envelope, in transmission order, into one
// contiguous packet.
//
// Order matters and is not obvious: the envelope's RTP timestamp belongs to the LAST sub-frame
// and the earlier ones run backwards from it, so array order is time order and concatenating
// is what puts the audio back where it belongs.
func (d *Decoder) decodeMultiframe(dst []float32, payload []byte) []float32 {
	frames, err := splitMultiframeInto(d.multiframe[:0], payload)
	if err != nil {
		d.malformed++
		if d.malformed == 1 || d.malformed%100 == 0 {
			slog.Warn(fmt.Sprintf(
				"mlow: failed to parse multiframe packet #%d (indicator 0x%02x): %v",
				d.malformed, payload[0], err))
		}
		d.report.Concealed = satAddU8(d.report.Concealed, 1)
		d.lastPacketSamps = opusFrameSamps
		return decoderAppendZeros(dst, opusFrameSamps)
	}
	// Refuse before decoding anything, not while appending: a packet whose sub-frames total
	// more than the format allows is malformed as a whole, and half-decoding it would leave the
	// predictor advanced by frames the peer never meant as one packet. The shipped client
	// applies the same 120 ms ceiling when it totals an envelope.
	declared := 0
	for _, frame := range frames {
		var ms int32
		if len(frame) > 0 {
			ms = parseMlowToc(frame[0]).frameMs
		}
		declared += silenceSamps(int(16 * max(ms, 0)))
	}
	if declared == 0 || declared > maxPacketSamps {
		d.malformed++
		if d.malformed == 1 || d.malformed%100 == 0 {
			slog.Warn(fmt.Sprintf(
				"mlow: multiframe packet #%d declares %d samples, past the %d ceiling",
				d.malformed, declared, maxPacketSamps))
		}
		d.report.Concealed = satAddU8(d.report.Concealed, 1)
		d.lastPacketSamps = opusFrameSamps
		return decoderAppendZeros(dst, opusFrameSamps)
	}

	for _, frame := range frames {
		dst = d.decodeFrame(dst, frame)
	}
	// The playout cushion sizes to the whole envelope, not to one sub-frame: the packet is what
	// arrives on the wire, and its cadence is what the buffer has to absorb.
	d.lastPacketSamps = declared
	return dst
}

func (d *Decoder) decodeFrame(dst []float32, frame []byte) []float32 {
	if len(frame) == 0 {
		d.report.Concealed = satAddU8(d.report.Concealed, 1)
		return decoderAppendZeros(dst, opusFrameSamps)
	}
	toc := parseMlowToc(frame[0])
	// In OUTPUT samples, always 16 kHz: the synthesis runs at one rate, so a frame declaring a
	// 32 kHz internal rate still occupies its duration's worth of 16 kHz samples. Sizing this
	// from the declared rate would double the cushion for a frame the decoder then drops.
	if toc.frameMs > 0 {
		d.lastPacketSamps = int(outputSamplesPerMs * toc.frameMs)
	}
	if toc.stdOpus {
		outLen := silenceSamps(int(outputSamplesPerMs * toc.frameMs))
		if decoderLogEnabled(slog.LevelDebug) {
			slog.Debug(fmt.Sprintf("mlow: standard-opus TOC 0x%02x -> %d samples silence", frame[0], outLen))
		}
		d.report.OffPoint = satAddU8(d.report.OffPoint, 1)
		return decoderAppendZeros(dst, outLen)
	}
	// A SID (DTX/CNG) frame carries comfort noise rather than coded voice and is silenced without
	// opening the range coder, so its geometry can never desync. Handle it before the
	// operating-point guard so an off-point SID is the benign silence it is rather than tripping
	// the "dropped" canary. A full 60ms slot keeps the playout cadence regardless of the frame's
	// nominal duration.
	//
	// A frame that is merely coded inactive is NOT silence: with DTX off the encoder keeps sending
	// background noise this way, and the reference decodes it. It goes through the normal path.
	if toc.sid {
		samps := silenceSamps(d.lastPacketSamps)
		if decoderLogEnabled(decoderLevelTrace) {
			slog.Log(context.Background(), decoderLevelTrace,
				fmt.Sprintf("mlow: SID TOC 0x%02x -> %d samples of silence", frame[0], samps))
		}
		d.report.InactiveOrSID = satAddU8(d.report.InactiveOrSID, 1)
		return decoderAppendZeros(dst, samps)
	}
	// Operating-point guard for active frames: a different internal rate, the low_rate=1 2x160
	// geometry, or a duration whose internal geometry differs would consume the payload with the
	// wrong symbol count and desync the range coder (garbage plus a poisoned cross-frame predictor
	// that propagates to later packets). Drop those as lost frames so the predictor holds its last
	// good values. flag2 is the smpl TOC's low_rate bit.
	frames, framesOK := internalFrames(toc.frameMs)
	var (
		offPoint bool
		dim      string
		val      int64
	)
	if toc.sampleRate != 16000 {
		offPoint, dim, val = true, "rate", int64(toc.sampleRate/1000)
	} else if toc.lowRate {
		offPoint, dim, val = true, "low_rate", 1
	} else if !framesOK {
		offPoint, dim, val = true, "frame_ms", int64(toc.frameMs)
	}
	if offPoint {
		d.droppedUnsupported++
		if d.droppedUnsupported == 1 || d.droppedUnsupported%100 == 0 {
			slog.Warn(fmt.Sprintf(
				"mlow: dropping out-of-operating-point frame #%d (%s=%d, TOC 0x%02x); "+
					"the decoder runs 16kHz / low_rate=0 / 20-120ms",
				d.droppedUnsupported, dim, val, frame[0]))
		}
		d.report.OffPoint = satAddU8(d.report.OffPoint, 1)
		return decoderAppendZeros(dst, silenceSamps(d.lastPacketSamps))
	}
	// The guard above rejected every unsupported duration.
	return d.decodeActiveFrame(dst, frame, frames*int(smplIntfLen), frames, toc.active)
}

// decodeActiveFrame: `codedAsActiveVoice` gates two symbols that a frame coded inactive never
// puts on the wire; reading them would consume symbols that were never written and desync
// everything after.
func (d *Decoder) decodeActiveFrame(
	dst []float32,
	frame []byte,
	outLen int,
	frames int,
	codedAsActiveVoice bool,
) []float32 {
	config := int(frame[0]>>2) & 1
	tbl := loadSmplTables()
	synthT := loadSmplSynthTables()
	mem := loadSmplMem()
	cc := loadCcTables()
	dec := newRangeDecoder(frame[1:])

	// The low_rate bit of the smpl TOC (this capture is low_rate==0; the synth gates on it).
	lowRate := (frame[0]>>2)&1 != 0

	// The overrun that invalidates a body is only detectable after the last internal frame, by
	// which point the loop has already advanced the LSF predictor, the CELP history and
	// `prevNlsf`. Keep a copy so concealment can undo them: parameters invented past the end of
	// a bad body must not seed the next packet. The reference leaves them advanced, but it never
	// meets a stream it cannot read; this decoder does, and the leak is audible in the frame
	// after.
	//
	// Saved into a buffer the decoder owns, not a fresh clone. Every field here is a slice of
	// fixed length, so copying into an existing snapshot reuses its allocations and the copy
	// costs a `memcpy`; a fresh clone allocates four buffers on every packet, which at ~17
	// packets a second is pure churn on a heap that an ESP32 target has to keep unfragmented.
	//
	// And it copies only what this loop can reach: the harmonic postfilter runs after the
	// endpoint check below, so a concealed packet never advanced it. See [smplDecodeRollback].
	d.stateBackup.save(&d.state)

	start := len(dst)
	// Collect the per-40-block lags (8 per internal frame) and the average normalized bitrate
	// for the per-packet harmonic postfilter.
	packetLags := d.packetLags[:0]
	var avgNormBr float32 = 0.0
	for f := 0; f < frames; f++ {
		lsf := decodeSmplLsf(
			&dec,
			tbl,
			&d.state.lstate,
			config,
			f,
			codedAsActiveVoice,
		)
		pulses := decodeSmplPulsesInto(
			&d.pulses,
			&dec,
			cc,
			int32(smplIntfLen),
			4,
			b2i32(codedAsActiveVoice),
			int32(config),
			lsf.stage1,
		)
		voiced := lsf.stage1 == 1
		var totalPulses int32
		for _, p := range pulses.subfr {
			totalPulses += p
		}
		params := celpDecParams{
			voiced:       voiced,
			sfPulses:     pulses.subfr,
			fcbgIdx:      [4]int32{},
			nrgresDbqQ14: [4]int32{},
			acbgIdx:      [4]int32{},
			blockLags:    [8]float32{},
			totalPulses:  totalPulses,
		}
		if voiced {
			pr := decodeSmplPitch(
				&dec,
				mem,
				cc,
				&d.state.lstate,
				int32(smplIntfLen),
				4,
				int32(config),
				pulses.subfr,
			)
			// lag = laginds*0.5 + SMPL_MIN_PITCH_LAG, clamped; one per 40-block, 8 per frame.
			for b := 0; b < 8; b++ {
				params.blockLags[b] =
					float32(minF64(float64(float64(pr.blockLags[b])*0.5)+32.0, 320.0))
			}
			for sf := 0; sf < 4; sf++ {
				params.acbgIdx[sf] = pr.gainIdx[sf]
				// The voiced FCB gain index is decoded in the pitch block (filt_idx).
				params.fcbgIdx[sf] = max(pr.filtIdx[sf], 0)
			}
		} else {
			g := decodeSmplGains(&dec, cc, 4, pulses.subfr)
			// The unvoiced gains decode yields gain_q (the nrgres_dbq_Q14 field) and nrg_res (the
			// fcbg_idx field).
			params.nrgresDbqQ14 = g.gainQ
			params.fcbgIdx = g.nrgRes
		}
		packetLags = append(packetLags, params.blockLags[:]...)
		avgNormBr += smplGetNormalizedBitrate(
			params.totalPulses,
			int32(smplIntfLen),
		)

		nlsf := smplReconstructNlsfInto(
			d.nlsf[:],
			synthT,
			int(lsf.stage1),
			config,
			int(lsf.grid),
			&lsf.stage2,
			d.state.prevNlsf,
		)
		var sig [smplIntfLen]float32
		d.state.celp.synthFrame(
			nlsf,
			int(lsf.extra),
			pulses.pulses,
			&params,
			lowRate,
			int32(smplIntfLen),
			sig[:],
		)
		// Rust moves the Vec (`prev_nlsf = nlsf`); copying into the existing buffer keeps the
		// steady state allocation-free.
		d.state.prevNlsf = append(d.state.prevNlsf[:0], nlsf...)
		dst = append(dst, sig[:]...)
	}
	d.packetLags = packetLags

	// Endpoint check, before the postfilter as in the reference: the range decoder returns zero
	// past either end of its storage WITHOUT flagging it, so an impossible stream decodes into
	// plausible-looking symbols and the synthesis can diverge to full scale. Comparing where the
	// decode ended against what the body actually held is the only way to see it. Anything
	// outside the accepted window is a malformed frame, concealed as a lost one rather than
	// synthesized, and the state the loop advanced is rolled back below -- deliberately unlike
	// the reference, for the reason given where the snapshot is taken.
	// div_ceil(8): tell is at most i32::MAX, so the +7 cannot wrap.
	consumedBytes := (uint32(max(dec.tell(), 0)) + 7) / 8
	body := dec.storage()
	if !endpointIsValid(body, consumedBytes) || dec.err != 0 {
		// Sticky flag first: this branch swallows the range-decoder failure it is reporting, and
		// `hadError` is what the suites read to see it.
		if dec.err != 0 {
			d.hadErrorFlag = true
		}
		// Copied back rather than swapped: the snapshot is narrower than the state, so it has
		// no `harm` to trade for the live one. It keeps its own buffers either way, which is
		// what the swap was protecting.
		d.stateBackup.restore(&d.state)
		d.malformed++
		if d.malformed == 1 || d.malformed%100 == 0 {
			slog.Warn(fmt.Sprintf(
				"mlow: concealing malformed frame #%d (TOC 0x%02x: decode ended at %d bytes "+
					"of a %d-byte body)",
				d.malformed, frame[0], consumedBytes, body))
		}
		d.report.Concealed = satAddU8(d.report.Concealed, 1)
		return decoderAppendZeros(dst[:start], outLen)
	}
	d.report.Decoded = satAddU8(d.report.Decoded, uint8(frames))

	// Per-packet harmonic postfilter (the codec's final pitch comb + 48-sample group delay), run
	// once over the whole packet with the 24 per-40-block lags and the average normalized bitrate.
	out := dst[start:]
	plen := len(out)
	smplHarmPostfilter(
		&d.state.harm,
		out,
		plen,
		packetLags,
		len(packetLags),
		avgNormBr/float32(frames),
	)

	// The C-domain synthesis output is already float in [-1, 1]; clamp in place.
	for i := range out {
		out[i] = clampF32(out[i], -1.0, 1.0)
	}
	if outLen > 0 && outLen != len(out) {
		if outLen < len(out) {
			dst = dst[:start+outLen]
		} else {
			dst = decoderAppendZeros(dst, outLen-len(out))
		}
	}
	if dec.err != 0 {
		// Sticky flag for `hadError`; does not alter `out` (the frame still plays).
		d.hadErrorFlag = true
		slog.Warn("mlow: range decoder raised its error flag after active-frame decode")
	}
	if decoderLogEnabled(decoderLevelTrace) {
		slog.Log(context.Background(), decoderLevelTrace,
			fmt.Sprintf("mlow: active frame decoded -> %d samples (config=%d)", len(dst)-start, config))
	}
	return dst
}
