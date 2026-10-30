package mlow

// Native fuzz targets for everything in this package that reads a received
// RTP payload: the decoder end to end, the RED and multiframe envelopes, the
// TOC byte and the range decoder. Each checks properties, not only the
// absence of a panic: bounded output, envelopes that stay inside the
// payload and re-encode to the same bytes, determinism, and encode/decode
// round trips.
//
//	CGO_ENABLED=0 go test ./gocore/voip/mlow -run '^$' -fuzz '^FuzzDecode$' -fuzztime 45s

import (
	"bytes"
	"errors"
	"io"
	"log/slog"
	"math"
	"runtime"
	"testing"
	"unsafe"
)

// fuzzQuietLogs silences the decoder's per-frame warnings for the length of a
// fuzz target: a fuzzer feeds it malformed frames by the million.
func fuzzQuietLogs(f *testing.F) {
	old := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(io.Discard, &slog.HandlerOptions{Level: slog.LevelError})))
	f.Cleanup(func() { slog.SetDefault(old) })
}

// fuzzHeapBytes is how many bytes one call of fn allocates, averaged over
// eight calls: runtime.MemStats counts the whole process, and a fuzz
// worker's own goroutines allocate a few KiB now and then, so a single call
// is not a reliable measurement. Callers allow a fixed slack on top of
// their bound for the same reason.
func fuzzHeapBytes(fn func()) uint64 {
	const runs = 8
	var before, after runtime.MemStats
	runtime.ReadMemStats(&before)
	for range runs {
		fn()
	}
	runtime.ReadMemStats(&after)
	return (after.TotalAlloc - before.TotalAlloc) / runs
}

// fuzzWithin reports whether sub lies inside whole's backing array.
func fuzzWithin(sub, whole []byte) bool {
	if len(sub) == 0 {
		return true
	}
	if len(whole) == 0 {
		return false
	}
	s := uintptr(unsafe.Pointer(unsafe.SliceData(sub)))
	w := uintptr(unsafe.Pointer(unsafe.SliceData(whole)))
	return s >= w && s+uintptr(len(sub)) <= w+uintptr(len(whole))
}

// fuzzMlowFrames is every captured frame in testdata, the seeds of the
// decoder targets.
func fuzzMlowFrames(f *testing.F) [][]byte {
	var out [][]byte
	for _, name := range []string{
		"inbound_capture_frames.json",
		"mlow_120ms_frames.json",
		"mlow_dtx_off_frames.json",
		"wasm_derived_frames.json",
		"wasm_derived_120ms_frames.json",
	} {
		out = append(out, decoderFrames(f, name)...)
	}
	return out
}

// fuzzCheckPCM is what every decode must give: at most a packet's worth of
// samples (maxPacketSamps, 120 ms at 16 kHz), each finite and in [-1, 1].
func fuzzCheckPCM(t *testing.T, pcm []float32, input []byte) {
	t.Helper()
	if len(pcm) == 0 || len(pcm) > maxPacketSamps {
		t.Fatalf("decode of %d bytes gave %d samples, want 1..%d", len(input), len(pcm), maxPacketSamps)
	}
	for i, s := range pcm {
		if math.IsNaN(float64(s)) || s < -1 || s > 1 {
			t.Fatalf("decode of %x: sample %d is %v, outside [-1, 1]", input, i, s)
		}
	}
}

func fuzzSameBits(a, b []float32) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if math.Float32bits(a[i]) != math.Float32bits(b[i]) {
			return false
		}
	}
	return true
}

// FuzzDecode decodes one payload, bare and as a RED packet, with fresh
// decoders. Properties: the output is bounded by maxPacketSamps and is
// finite and clamped; it is as long as LastPacketSamples says the packet
// declared (what the jitter buffer sizes to); the report counts the packet;
// a second fresh decoder gives the same bits; and with a warm buffer one
// decode allocates at most a small, size-independent amount.
func FuzzDecode(f *testing.F) {
	fuzzQuietLogs(f)
	for _, fr := range fuzzMlowFrames(f) {
		f.Add(fr, false)
		f.Add(append([]byte{0x00}, fr...), true)
	}
	f.Add([]byte{0x82, 0x02, 0x01, 0x50, 0x50}, false)
	f.Add([]byte{0xc4}, false)
	f.Fuzz(func(t *testing.T, payload []byte, red bool) {
		decode := func() ([]float32, *Decoder) {
			d := NewDecoder()
			if red {
				d.SetRedundancy(1)
			}
			return d.AppendDecode(make([]float32, 0, maxPacketSamps), payload), d
		}
		out, d := decode()
		fuzzCheckPCM(t, out, payload)
		r := d.TakeFrameReport()
		if int(r.Decoded)+int(r.Concealed)+int(r.OffPoint)+int(r.InactiveOrSID) == 0 {
			t.Fatalf("decode of %x reported nothing: %+v", payload, r)
		}
		unwrapped := len(payload) > 0
		if red && unwrapped {
			_, err := depackSplitRed(payload)
			unwrapped = err == nil
		}
		if unwrapped && len(out) != d.LastPacketSamples() {
			t.Fatalf("decode of %x gave %d samples but declares %d", payload, len(out), d.LastPacketSamples())
		}
		again, _ := decode()
		if !fuzzSameBits(out, again) {
			t.Fatalf("decode of %x is not deterministic", payload)
		}

		// A warm decoder with room in dst: whatever the payload claims, one
		// decode must not allocate in proportion to it.
		dst := make([]float32, 0, maxPacketSamps)
		if n := fuzzHeapBytes(func() { dst = d.AppendDecode(dst[:0], payload) }); n > 16<<10 {
			t.Fatalf("decode of %d bytes allocated %d bytes", len(payload), n)
		}
	})
}

// FuzzDecodeStream feeds a run of packets (each one length byte, then that
// many bytes) through one decoder, so a poisoned frame's leftovers reach the
// next. Properties: every packet's output is bounded, finite and clamped,
// and two fresh decoders fed the same run agree bit for bit.
func FuzzDecodeStream(f *testing.F) {
	fuzzQuietLogs(f)
	frames := fuzzMlowFrames(f)
	var stream []byte
	for i, fr := range frames {
		if len(fr) > 255 {
			continue
		}
		stream = append(stream, byte(len(fr)))
		stream = append(stream, fr...)
		if i%8 == 7 {
			f.Add(stream)
			stream = nil
		}
	}
	f.Fuzz(func(t *testing.T, data []byte) {
		var packets [][]byte
		for len(data) > 0 && len(packets) < 64 {
			n := min(int(data[0]), len(data)-1)
			packets = append(packets, data[1:1+n])
			data = data[1+n:]
		}
		run := func() [][]float32 {
			d := NewDecoder()
			var outs [][]float32
			for _, p := range packets {
				pcm := d.Decode(p)
				fuzzCheckPCM(t, pcm, p)
				outs = append(outs, pcm)
			}
			return outs
		}
		a, b := run(), run()
		for i := range a {
			if !fuzzSameBits(a[i], b[i]) {
				t.Fatalf("packet %d (%x) decodes differently on a second run", i, packets[i])
			}
		}
	})
}

// FuzzSplitMultiframe splits an envelope. On success the sub-frames number
// 1..maxFrames, are non-empty, lie inside the payload in order without
// overlapping, and an envelope rebuilt from them splits into the same
// frames. Any error is a multiframeError.
func FuzzSplitMultiframe(f *testing.F) {
	for _, fr := range fuzzMlowFrames(f) {
		if isMultiframe(fr) {
			f.Add(fr)
		}
	}
	f.Add([]byte{0x82, 0x02, 0x03, 0x50, 1, 2, 0x50, 3})
	f.Add([]byte{0x82, 0x42, 0xff, 0x02, 0x01, 0x50, 0x50, 0, 0})
	f.Add([]byte{0x82, 0x02, 0xfc, 0x01, 0x50})
	f.Fuzz(func(t *testing.T, payload []byte) {
		frames, err := splitMultiframe(payload)
		if err != nil {
			var me multiframeError
			if !errors.As(err, &me) {
				t.Fatalf("error %T is not a multiframeError", err)
			}
			if frames != nil {
				t.Fatalf("error with frames")
			}
			return
		}
		if len(frames) < 1 || len(frames) > maxFrames {
			t.Fatalf("%d frames", len(frames))
		}
		end := 0 // offset just past the previous frame
		for i, fr := range frames {
			if len(fr) == 0 || !fuzzWithin(fr, payload) {
				t.Fatalf("frame %d (%d bytes) is empty or outside the payload", i, len(fr))
			}
			at := int(uintptr(unsafe.Pointer(unsafe.SliceData(fr))) - uintptr(unsafe.Pointer(unsafe.SliceData(payload))))
			if at < end || at < 2 {
				t.Fatalf("frame %d starts at %d, before %d", i, at, end)
			}
			end = at + len(fr)
		}

		// Rebuilt without padding, with the same one- or two-byte lengths.
		env := []byte{payload[0], byte(len(frames))}
		for _, fr := range frames[:len(frames)-1] {
			n := len(fr)
			if n < 252 {
				env = append(env, byte(n))
			} else {
				if n > 255+255*4 {
					t.Fatalf("frame of %d bytes has no two-byte length", n)
				}
				first := 252 + (n-252)%4
				env = append(env, byte(first), byte((n-first)/4))
			}
		}
		for _, fr := range frames {
			env = append(env, fr...)
		}
		again, err := splitMultiframe(env)
		if err != nil || len(again) != len(frames) {
			t.Fatalf("rebuilt envelope %x: %d frames, %v", env, len(again), err)
		}
		for i := range frames {
			if !bytes.Equal(frames[i], again[i]) {
				t.Fatalf("rebuilt envelope frame %d differs", i)
			}
		}
	})
}

// FuzzDepackSplitRed unwraps a SplitRed packet. On success the main frame
// is last and non-empty, every frame lies inside the packet, the header and
// bodies account for every byte, and packing the frames again gives the
// packet back byte for byte. Any error is a redError.
func FuzzDepackSplitRed(f *testing.F) {
	for _, fr := range fuzzMlowFrames(f) {
		f.Add(append([]byte{0x00}, fr...))
		if len(fr) < 255 {
			f.Add(append(append([]byte{0x81, byte(len(fr)), 0x01}, fr...), fr...))
		}
	}
	f.Add([]byte{0x81, 0x02, 0x05, 0xaa, 0xbb, 0x50, 0x01})
	f.Add([]byte{0x80})
	f.Fuzz(func(t *testing.T, p []byte) {
		frames, err := depackSplitRed(p)
		if err != nil {
			var re redError
			if !errors.As(err, &re) {
				t.Fatalf("error %T is not a redError", err)
			}
			return
		}
		if len(frames) == 0 || !frames[len(frames)-1].isMain || len(frames[len(frames)-1].data) == 0 {
			t.Fatalf("no non-empty main frame last: %+v", frames)
		}
		var packed []byte
		for _, fr := range frames[:len(frames)-1] {
			if fr.isMain || fr.timeCode > 0x7f || len(fr.data) > 255 {
				t.Fatalf("bad redundant block %+v", fr)
			}
			packed = append(packed, 0x80|fr.timeCode, byte(len(fr.data)))
		}
		main := frames[len(frames)-1]
		packed = append(packed, main.timeCode)
		for _, fr := range frames {
			if !fuzzWithin(fr.data, p) {
				t.Fatalf("frame outside the packet")
			}
			packed = append(packed, fr.data...)
		}
		if !bytes.Equal(packed, p) {
			t.Fatalf("repacked %x, want %x", packed, p)
		}
	})
}

// FuzzToc parses a TOC byte. Every field lies in its documented set, and
// for an MLow (non-escape) byte the fields give the byte back: the FEC and
// active flags together still carry bit 1. An escape keeps only its
// duration bits.
func FuzzToc(f *testing.F) {
	for _, b := range []byte{0x00, 0x10, 0x12, 0x48, 0x50, 0x82, 0xa0, 0xc0, 0xcc, 0xff} {
		f.Add(b)
	}
	f.Fuzz(func(t *testing.T, b byte) {
		toc := parseMlowToc(b)
		if toc.stdOpus != (b&0xc0 == 0xc0) {
			t.Fatalf("%#02x: stdOpus %v", b, toc.stdOpus)
		}
		if toc.stdOpus {
			if toc.frameMs != [4]int32{3, 5, 10, 20}[(b>>2)&3] || toc.sampleRate != 16000 {
				t.Fatalf("%#02x: escape %+v", b, toc)
			}
			return
		}
		var rebuilt byte
		if toc.sid {
			rebuilt |= 0x80
		}
		if toc.vad {
			rebuilt |= 0x40
		}
		switch toc.sampleRate {
		case 16000:
		case 32000:
			rebuilt |= 0x20
		default:
			t.Fatalf("%#02x: rate %d", b, toc.sampleRate)
		}
		idx := map[int32]byte{10: 0, 20: 1, 60: 2, 120: 3}
		i, ok := idx[toc.frameMs]
		if !ok {
			t.Fatalf("%#02x: frame %d ms", b, toc.frameMs)
		}
		rebuilt |= i << 3
		if toc.lowRate {
			rebuilt |= 0x04
		}
		if toc.fec && !toc.vad {
			t.Fatalf("%#02x: FEC without VAD", b)
		}
		if (toc.vad && toc.fec) || (!toc.vad && toc.active) {
			rebuilt |= 0x02
		}
		if toc.stereo {
			rebuilt |= 0x01
		}
		if rebuilt != b {
			t.Fatalf("%#02x parses to %+v, which rebuilds as %#02x", b, toc, rebuilt)
		}
	})
}

// fuzzRCTables are the tables FuzzRangeCoder codes against, the ones the
// unit round trip uses.
var (
	fuzzRCIcdf = [6]uint8{255, 200, 150, 90, 30, 0}
	fuzzRCCdf0 = [5]uint16{0, 3, 7, 12, 20}
	fuzzRCCdf1 = [4]uint16{5, 8, 14, 30}
)

// fuzzRCScript turns bytes into a script of range-coder operations, three
// bytes each, every symbol in its alphabet.
func fuzzRCScript(b []byte) []rangecoderOp {
	var ops []rangecoderOp
	for ; len(b) >= 3 && len(ops) < 512; b = b[3:] {
		v := uint32(b[1]) | uint32(b[2])<<8
		var op rangecoderOp
		switch b[0] % 7 {
		case 0:
			op = rangecoderOp{kind: rangecoderOpKindIcdf, s: int32(v % 6)}
		case 1:
			op = rangecoderOp{kind: rangecoderOpKindCdf0, s: int32(v % 4)}
		case 2:
			op = rangecoderOp{kind: rangecoderOpKindCdf1, s: int32(v % 3)}
		case 3:
			n := 1 + uint32(b[0]>>3)%16
			op = rangecoderOp{kind: rangecoderOpKindBits, a: v & (1<<n - 1), b: n}
		case 4:
			n := 1 + uint32(b[0]>>3)%6
			op = rangecoderOp{kind: rangecoderOpKindRawSym, a: v & (1<<n - 1), b: n}
		case 5:
			op = rangecoderOp{kind: rangecoderOpKindFine, s: int32(v % 64)}
		default:
			ft := 2 + uint32(b[0]>>3)*97 // 2..3009: both uint branches
			op = rangecoderOp{kind: rangecoderOpKindUint, a: v % ft, b: ft}
		}
		ops = append(ops, op)
	}
	return ops
}

// fuzzRCDecode decodes op's symbol from d.
func fuzzRCDecode(d *rangeDecoder, op rangecoderOp) int64 {
	switch op.kind {
	case rangecoderOpKindIcdf:
		return int64(d.decodeIcdf(fuzzRCIcdf[:], 8))
	case rangecoderOpKindCdf0:
		return int64(d.decodeCdf(fuzzRCCdf0[:]))
	case rangecoderOpKindCdf1:
		return int64(d.decodeCdf(fuzzRCCdf1[:]))
	case rangecoderOpKindBits:
		return int64(d.bitsN(op.b))
	case rangecoderOpKindRawSym:
		return int64(d.decodeRawSymbol(op.b))
	case rangecoderOpKindFine:
		return int64(d.decode64FineSym())
	}
	return int64(d.decodeUint(op.b))
}

// fuzzRCAlphabet is the number of symbols op's alphabet has.
func fuzzRCAlphabet(op rangecoderOp) int64 {
	switch op.kind {
	case rangecoderOpKindIcdf:
		return int64(len(fuzzRCIcdf)) // the terminating 0 is the last symbol's bound
	case rangecoderOpKindCdf0:
		return 4
	case rangecoderOpKindCdf1:
		return 3
	case rangecoderOpKindBits, rangecoderOpKindRawSym:
		return 1 << op.b
	case rangecoderOpKindFine:
		return 64
	}
	return int64(op.b)
}

// FuzzRangeCoder encodes a script and decodes it back, then decodes the
// same script out of arbitrary received bytes. The round trip recovers
// every symbol with no error; the arbitrary decode never panics, keeps
// every symbol inside its alphabet, and is deterministic.
func FuzzRangeCoder(f *testing.F) {
	f.Add([]byte{0, 1, 2, 1, 3, 4, 3, 0xff, 0xff, 6, 7, 7, 5, 9, 9}, []byte{0x50, 0x12, 0x34})
	for _, fr := range fuzzMlowFrames(f)[:8] {
		f.Add(fr, fr)
	}
	f.Fuzz(func(t *testing.T, script, received []byte) {
		ops := fuzzRCScript(script)
		e := newRangeEncoder(4096)
		for _, op := range ops {
			switch op.kind {
			case rangecoderOpKindIcdf:
				e.encodeIcdf(op.s, fuzzRCIcdf[:], 8)
			case rangecoderOpKindCdf0:
				e.encodeCdf(op.s, fuzzRCCdf0[:])
			case rangecoderOpKindCdf1:
				e.encodeCdf(op.s, fuzzRCCdf1[:])
			case rangecoderOpKindBits:
				e.bitsN(op.a, op.b)
			case rangecoderOpKindRawSym:
				e.encodeRawSymbol(op.a, op.b)
			case rangecoderOpKindFine:
				e.encode64FineSym(op.s)
			case rangecoderOpKindUint:
				e.encodeUint(op.a, op.b)
			}
		}
		e.done()
		if e.err() != 0 {
			t.Fatalf("encoder error on a %d-op script", len(ops))
		}
		d := newRangeDecoder(append([]byte(nil), e.bytes()...))
		for i, op := range ops {
			want := int64(op.s)
			if op.kind == rangecoderOpKindBits || op.kind == rangecoderOpKindRawSym || op.kind == rangecoderOpKindUint {
				want = int64(op.a)
			}
			if got := fuzzRCDecode(&d, op); got != want {
				t.Fatalf("op %d (%+v): decoded %d", i, op, got)
			}
		}
		if d.err != 0 {
			t.Fatalf("decoder error after a clean round trip")
		}

		run := func() ([]int64, int32) {
			d := newRangeDecoder(received)
			var syms []int64
			for _, op := range ops {
				s := fuzzRCDecode(&d, op)
				if s < 0 || s >= fuzzRCAlphabet(op) {
					t.Fatalf("op %+v decoded %d from %x, outside its alphabet", op, s, received)
				}
				syms = append(syms, s)
			}
			return syms, d.tell()
		}
		a, ta := run()
		b, tb := run()
		if ta != tb || len(a) != len(b) {
			t.Fatalf("decode of %x is not deterministic", received)
		}
		for i := range a {
			if a[i] != b[i] {
				t.Fatalf("decode of %x is not deterministic at op %d", received, i)
			}
		}
	})
}

// FuzzEncodeDecode encodes a 60 ms frame of arbitrary 16-bit PCM and decodes
// it. The packet fits the encoder's buffer, carries one of the three TOCs
// the encoder documents, decodes to exactly FrameSamples with all three
// internal frames decoded and none concealed, and encoding is deterministic.
func FuzzEncodeDecode(f *testing.F) {
	tone := make([]byte, 2*FrameSamples)
	for i := range FrameSamples {
		v := int16(8000 * math.Sin(2*math.Pi*440*float64(i)/SampleRate))
		tone[2*i], tone[2*i+1] = byte(v), byte(uint16(v)>>8)
	}
	f.Add(tone)
	f.Add([]byte{})
	f.Add([]byte{0xff, 0x7f, 0x00, 0x80})
	f.Fuzz(func(t *testing.T, raw []byte) {
		pcm := make([]int16, FrameSamples)
		if n := len(raw) / 2; n > 0 {
			for i := range pcm {
				j := i % n
				pcm[i] = int16(uint16(raw[2*j]) | uint16(raw[2*j+1])<<8)
			}
		}
		enc := func() []byte {
			p, err := NewEncoder().EncodeInt16(pcm)
			if errors.Is(err, ErrBufferOverflow) {
				t.Skip("range-coder buffer overflow is a documented encode error")
			}
			if err != nil {
				t.Fatalf("encode: %v", err)
			}
			return p
		}
		packet := enc()
		if !bytes.Equal(packet, enc()) {
			t.Fatalf("encode is not deterministic")
		}
		if len(packet) < 1 || len(packet) > 1+smplEncodeBufBytes {
			t.Fatalf("packet of %d bytes", len(packet))
		}
		if b := packet[0]; b != 0x50 && b != 0x12 && b != 0x10 {
			t.Fatalf("TOC %#02x is not one the encoder documents", b)
		}
		d := NewDecoder()
		out := d.Decode(packet)
		r := d.TakeFrameReport()
		if len(out) != FrameSamples || r.Decoded != 3 || r.Concealed != 0 {
			t.Fatalf("own packet %x decodes to %d samples, report %+v", packet, len(out), r)
		}
		fuzzCheckPCM(t, out, packet)
	})
}
