// Ported from whatsapp-rust 5e72baf wacore/src/voip/mlow/encode.rs (#[cfg(test)] mod tests), MIT.

package mlow

import (
	"bytes"
	"errors"
	"fmt"
	"math"
	"os"
	"slices"
	"strconv"
	"strings"
	"testing"
)

func TestEncode_I16EncodingMatchesNormalizedPcmWithReusedOutput(t *testing.T) {
	normalized := NewEncoder()
	integer := NewEncoder()
	output := bytes.Repeat([]byte{0xaa}, 2048)
	var expected []byte
	for frame := 0; frame < 72; frame++ {
		if frame == 68 {
			normalized.Reset()
			integer.Reset()
		}
		pcm := make([]int16, opusFrameSamps)
		for i := range pcm {
			pcm[i] = int16(frame*opusFrameSamps + i)
		}
		floats := make([]float32, len(pcm))
		for i, s := range pcm {
			floats[i] = float32(s) / 32768.0
		}
		var err error
		expected, err = normalized.AppendEncode(expected[:0], floats)
		if err != nil {
			t.Fatal(err)
		}
		output, err = integer.AppendEncodeInt16(output[:0], pcm)
		if err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(output, expected) {
			t.Fatalf("frame %d: %x != %x", frame, output, expected)
		}
		if !slices.Equal(integer.clean, floats) {
			t.Fatalf("frame %d: clean scratch differs from the normalized samples", frame)
		}
		before := slices.Clone(output)
		got, err := integer.AppendEncodeInt16(output, pcm[:959])
		var fle FrameLengthError
		if !errors.As(err, &fle) || fle.Got != 959 {
			t.Fatalf("frame %d: want FrameLength{got: 959}, got %v", frame, err)
		}
		if !bytes.Equal(got, before) || len(got) != len(before) {
			t.Fatalf("frame %d: an error changed the output", frame)
		}
		for len(output) < 2048 {
			output = append(output, 0xaa)
		}
	}
}

func TestEncode_AlternatingInputApisPreservesVadAndEncoderState(t *testing.T) {
	reference := NewEncoder()
	mixed := NewEncoder()
	var expected, output []byte
	for frame := 0; frame < 96; frame++ {
		if frame == 48 {
			reference.Reset()
			mixed.Reset()
		}
		pcm := make([]int16, opusFrameSamps)
		for i := range pcm {
			if frame%11 < 3 {
				pcm[i] = 0
			} else {
				pcm[i] = int16(frame*opusFrameSamps + i)
			}
		}
		normalized := make([]float32, len(pcm))
		for i, s := range pcm {
			normalized[i] = float32(s) / 32768.0
		}
		var err error
		expected, err = reference.AppendEncode(expected[:0], normalized)
		if err != nil {
			t.Fatal(err)
		}
		if frame%2 == 0 {
			output, err = mixed.AppendEncodeInt16(output[:0], pcm)
		} else {
			output, err = mixed.AppendEncode(output[:0], normalized)
		}
		if err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(output, expected) {
			t.Fatalf("frame %d", frame)
		}
	}
}

func TestEncode_I16LongStreamMatchesFloatEncoding(t *testing.T) {
	if testing.Short() {
		t.Skip("2048-frame stream; skipped with -short")
	}
	normalized := NewEncoder()
	integer := NewEncoder()
	decoder := NewDecoder()
	var output, expected []byte
	for frame := 0; frame < 2048; frame++ {
		pcm := make([]int16, opusFrameSamps)
		for i := range pcm {
			if frame%97 < 5 {
				pcm[i] = 0
			} else {
				pcm[i] = f32ToI16(sinF32(float32(frame*opusFrameSamps+i)*0.07) * 10000.0)
			}
		}
		floats := make([]float32, len(pcm))
		for i, s := range pcm {
			floats[i] = float32(s) / 32768.0
		}
		var err error
		expected, err = normalized.AppendEncode(expected[:0], floats)
		if err != nil {
			t.Fatal(err)
		}
		output, err = integer.AppendEncodeInt16(output[:0], pcm)
		if err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(output, expected) {
			t.Fatalf("frame %d", frame)
		}
		decoded := decoder.Decode(output)
		if len(decoded) != opusFrameSamps {
			t.Fatalf("frame %d: decoded %d samples", frame, len(decoded))
		}
		for _, s := range decoded {
			if math.IsNaN(float64(s)) || math.IsInf(float64(s), 0) {
				t.Fatalf("frame %d: non-finite sample", frame)
			}
		}
	}
}

// Isolated voiced pitch-block round-trip: encode the gains + the estimator contour
// (`blocksegIdx`/`laginds`) then decode them back; the decoder's `blockLags` must equal the
// encoded `laginds`, proving the wire encode is the inverse of `decodeSmplPitch`.
func TestEncode_PitchBlockRoundTripsContour(t *testing.T) {
	mem := loadSmplMem()
	cc := loadCcTables()
	cases := []struct {
		bsx     int
		laginds [8]int32
		gains   [4]int32
	}{
		{142, [8]int32{128, 129, 129, 118, 118, 121, 121, 123}, [4]int32{5, 2, 2, 2}},
		{142, [8]int32{128, 129, 129, 118, 118, 121, 121, 123}, [4]int32{5, 6, 2, 2}},
		{59, [8]int32{123, 123, 123, 123, 128, 128, 132, 132}, [4]int32{2, 6, 6, 6}},
	}
	for _, c := range cases {
		pp := smplPitchParams{
			gainIdx:     c.gains,
			filtIdx:     [4]int32{},
			blocksegIdx: c.bsx,
			laginds:     c.laginds,
		}
		// subfr_counts > 0 so the filt_idx path fires (as in the real voiced encode).
		subfr := [4]int32{1, 1, 1, 1}
		for sf := 0; sf < 4; sf++ {
			pp.filtIdx[sf] = 0
		}
		enc := newRangeEncoder(64)
		est := defaultSmplLsfState()
		est.prevLag = -1
		est.prevFracLag = -1
		est.prevLagblk = -1
		est.prevLagidx = -1
		encodeSmplPitch(&enc, mem, cc, &est, 320, 4, 0, subfr, &pp)
		enc.done()
		n := enc.consumedLen()
		b := slices.Clone(enc.bytes()[:n])
		dec := newRangeDecoder(b)
		dst := defaultSmplLsfState()
		dst.prevLag = -1
		dst.prevFracLag = -1
		pr := decodeSmplPitch(&dec, mem, cc, &dst, 320, 4, 0, subfr)
		if pr.blockLags != c.laginds {
			t.Fatalf("bsx=%d: decoded block_lags != encoded laginds: %v vs %v", c.bsx, pr.blockLags, c.laginds)
		}
	}
}

func encodeCorr(a, b []float32) float64 {
	var sxy, sxx, syy float64
	for i := range a {
		x, y := float64(a[i]), float64(b[i])
		sxy += float64(x * y)
		sxx += float64(x * x)
		syy += float64(y * y)
	}
	if sxx < 1e-12 || syy < 1e-12 {
		return 0.0
	}
	return sxy / math.Sqrt(sxx*syy)
}

// Closed-loop: encode a tone and decode it back with the (byte-exact) decoder; the LB-core
// reconstruction must track the input waveform shape (correlation). Proves the analysis ->
// entropy-encode -> decode chain produces a frame that reconstructs the input audio.
func TestEncode_EncodeRoundTripsATone(t *testing.T) {
	enc := NewEncoder()
	dec := NewDecoder()
	best := 0.0
	for f := 0; f < 8; f++ {
		pcm := make([]float32, 960)
		for i := range pcm {
			tt := float64(f*960+i) / 16000.0
			pcm[i] = float32(0.5 * math.Sin(float64(2.0)*math.Pi*550.0*tt))
		}
		frame, err := enc.Encode(pcm)
		if err != nil {
			t.Fatalf("encode: %v", err)
		}
		if frame[0] != 0x50 {
			t.Fatalf("active frame TOC: got 0x%02x", frame[0])
		}
		out := dec.Decode(frame)
		// The decoder's harmonic postfilter adds 48 samples of group delay; align before
		// correlating.
		const harmDelay = 48
		best = maxF64(best, encodeCorr(pcm[:len(pcm)-harmDelay], out[harmDelay:]))
	}
	if !(best > 0.7) {
		t.Fatalf("encode->decode round-trip correlation too low: %v", best)
	}
}

// encodeSynthInput is a deterministic synthetic signal cycling voiced tone / unvoiced noise /
// voiced+noise / silence, so encode tests exercise both the voiced (LTP) and unvoiced (gains)
// paths from code alone. Kept local to this file so the encoder's self-contained guards need no
// data file.
func encodeSynthInput() []float32 {
	const frame = 960
	const packets = 24
	out := make([]float32, 0, packets*frame)
	seed := uint32(0x9e37_79b9)
	twoPi := float32(2.0) * smplPi
	for i := 0; i < packets*frame; i++ {
		tt := float32(i) / 16000.0
		seed = seed*1664525 + 1013904223
		noise := float32(float32(seed>>8)/8_388_608.0) - 1.0
		var v float32
		switch (i / frame) % 4 {
		case 0:
			v = float32(0.30*sinF32(twoPi*150.0*tt)) + float32(0.10*sinF32(twoPi*300.0*tt))
		case 1:
			v = 0.18 * noise
		case 2:
			v = float32(0.25*sinF32(twoPi*120.0*tt)) + float32(0.05*noise)
		default:
			v = 0.0
		}
		out = append(out, v)
	}
	return out
}

func encodeAll(enc *Encoder, input []float32) [][]byte {
	var out [][]byte
	for i := 0; i+960 <= len(input); i += 960 {
		f, err := enc.Encode(input[i : i+960])
		if err != nil {
			panic(fmt.Sprintf("encode: %v", err))
		}
		out = append(out, f)
	}
	return out
}

func encodeFramesEqual(a, b [][]byte) bool {
	return slices.EqualFunc(a, b, bytes.Equal)
}

// R4: two fresh encoders fed the same input must emit byte-identical frames. Catches any
// nondeterminism (map iteration order, uninitialized state, time/RNG seeding).
func TestEncode_EncodeIsDeterministicAcrossFreshInstances(t *testing.T) {
	input := encodeSynthInput()
	a := encodeAll(NewEncoder(), input)
	b := encodeAll(NewEncoder(), input)
	if !encodeFramesEqual(a, b) {
		t.Fatal("two fresh encoders produced different frames")
	}
}

// R5 (encoder): Reset() must return the encoder to a fresh state. After N frames, reset then
// re-encode the same input; output must equal a fresh instance's. Guards a Reset() that forgets a
// smplEncoderState field (the analysis predictor history).
func TestEncode_EncoderResetReturnsToFreshState(t *testing.T) {
	input := encodeSynthInput()
	fresh := encodeAll(NewEncoder(), input)

	enc := NewEncoder()
	_ = encodeAll(enc, input) // run N frames to populate predictor state
	enc.Reset()
	afterReset := encodeAll(enc, input)
	if !encodeFramesEqual(afterReset, fresh) {
		t.Fatal("encoder Reset() did not restore fresh state")
	}
}

// R5 (decoder): Reset() must return the decoder to a fresh state. Guards a Reset() that forgets
// prev_nlsf / harm / lstate (the cross-frame predictor and postfilter history).
func TestEncode_DecoderResetReturnsToFreshState(t *testing.T) {
	input := encodeSynthInput()
	frames := encodeAll(NewEncoder(), input)

	decodeAll := func(dec *Decoder) []float32 {
		var out []float32
		for _, f := range frames {
			out = append(out, dec.Decode(f)...)
		}
		return out
	}

	fresh := decodeAll(NewDecoder())

	dec := NewDecoder()
	_ = decodeAll(dec) // run N frames to populate predictor/postfilter state
	dec.Reset()
	afterReset := decodeAll(dec)
	if !slices.Equal(afterReset, fresh) {
		t.Fatal("decoder Reset() did not restore fresh state")
	}
}

// R9: the encoder is config-0 / no-DTX / 16 kHz / 60 ms / mono, so the only bits that vary are
// the two activity bits. Codec routing comes from negotiation, not from interpreting this byte.
func TestEncode_EncoderEmitsOnlyConfig060msTocs(t *testing.T) {
	input := encodeSynthInput()
	for _, frame := range encodeAll(NewEncoder(), input) {
		toc := frame[0]
		if toc != 0x50 && toc != 0x12 && toc != 0x10 {
			t.Fatalf("encoder emitted non-config-0 TOC 0x%02x", toc)
		}
		parsed := parseMlowToc(toc)
		if parsed.sid || parsed.lowRate || parsed.stereo || parsed.stdOpus {
			t.Fatalf("TOC 0x%02x parsed with sid/low_rate/stereo/std_opus set", toc)
		}
		if parsed.sampleRate != 16000 || parsed.frameMs != 60 {
			t.Fatalf("TOC 0x%02x: (%d, %d) != (16000, 60)", toc, parsed.sampleRate, parsed.frameMs)
		}
	}
}

// encodeSpeechSilenceSpeech is 1 s of a speech-like tone, 3 s of a -60 dBFS comfort-noise floor,
// 1 s of tone again.
func encodeSpeechSilenceSpeech() []float32 {
	seed := uint32(0x1234_5678)
	noise := func() float32 {
		seed = seed*1664525 + 1013904223
		return float32(float32(seed>>8)/8_388_608.0) - 1.0
	}
	twoPi := float32(2.0) * smplPi
	tone := func(i int) float32 {
		tt := float32(i) / 16000.0
		return float32(0.25*sinF32(twoPi*150.0*tt)) + float32(0.05*sinF32(twoPi*1700.0*tt))
	}
	sig := make([]float32, 0, 80_000)
	for i := 0; i < 16_000; i++ {
		sig = append(sig, tone(i))
	}
	for i := 0; i < 48_000; i++ {
		sig = append(sig, 0.001*noise())
	}
	for i := 0; i < 16_000; i++ {
		sig = append(sig, tone(i))
	}
	return sig
}

// The whole point of coding a frame inactive: a receiver can tell the sender's background noise
// apart from its speech. The reference emits `0x10` over silence with DTX off, promoting to
// `0x12` for the hangover packet after a talkspurt, and this encoder has to agree, because a
// stream that claims active voice at a comfort-noise level for seconds on end is a shape no
// real client produces.
func TestEncode_SilenceIsCodedInactiveAndSpeechIsNot(t *testing.T) {
	var tocs []byte
	for _, f := range encodeAll(NewEncoder(), encodeSpeechSilenceSpeech()) {
		tocs = append(tocs, f[0])
	}
	run := func(from int, want byte) int {
		n := 0
		for _, b := range tocs[from:] {
			if b != want {
				break
			}
			n++
		}
		return n
	}
	if run(0, 0x50) < 14 {
		t.Fatalf("the whole first talkspurt is active voice: %x", tocs)
	}
	silenceAt := 36
	if run(silenceAt, 0x10) < 25 {
		t.Fatalf("a settled comfort-noise floor stays coded inactive: %x", tocs)
	}
	last := bytes.LastIndexByte(tocs, 0x10)
	if last < 0 {
		t.Fatal("the silence is coded inactive")
	}
	speechAgain := last + 1
	if run(speechAgain, 0x50) < 14 {
		t.Fatalf("speech after the silence is active voice again: %x", tocs)
	}
	if !slices.Contains(tocs, 0x12) {
		t.Fatalf("the DTX hangover packet after a talkspurt keeps the frame coded active with the VAD bit clear: %x", tocs)
	}
	// Pure digital silence from the first frame: no talkspurt to hang over from, so inactive
	// almost immediately.
	zeros := make([]float32, 960*20)
	var z []byte
	for _, f := range encodeAll(NewEncoder(), zeros) {
		z = append(z, f[0])
	}
	for _, t2 := range z[1:] {
		if t2 != 0x10 {
			t.Fatalf("digital silence is coded inactive: %x", z)
		}
	}
}

// An inactive frame leaves two LSF symbols off the wire, so a writer that still emits them (or a
// reader that still consumes them) desyncs the range coder and poisons the cross-frame predictor
// for every packet after it. The canary is the talkspurt AFTER the silence: it only decodes at
// full level if nothing drifted through 44 inactive frames.
func TestEncode_InactiveFramesDecodeWithoutDesync(t *testing.T) {
	frames := encodeAll(NewEncoder(), encodeSpeechSilenceSpeech())
	dec := NewDecoder()
	levels := make([]float32, 0, len(frames))
	var decoded, concealed, offPoint uint32
	for _, f := range frames {
		pcm := dec.Decode(f)
		r := dec.TakeFrameReport()
		decoded += uint32(r.Decoded)
		concealed += uint32(r.Concealed)
		offPoint += uint32(r.OffPoint)
		if len(pcm) != 960 {
			t.Fatalf("every frame fills its 60 ms slot: got %d", len(pcm))
		}
		sum := analysisNegZero
		for _, s := range pcm {
			if math.IsNaN(float64(s)) || math.IsInf(float64(s), 0) {
				t.Fatal("finite output")
			}
			sum += float32(s * s)
		}
		rms := sqrtF32(sum / float32(len(pcm)))
		levels = append(levels, 20.0*log10F32(rms+1e-12))
	}
	if concealed != 0 || offPoint != 0 {
		t.Fatalf("our own bitstream must never be concealed or refused: (%d, %d)", concealed, offPoint)
	}
	// `decoded` counts internal frames, three to a 60 ms packet.
	if int(decoded) != len(frames)*3 {
		t.Fatalf("every internal frame carried coded audio: %d != %d", decoded, len(frames)*3)
	}
	speechBefore := levels[8]
	silence := levels[40]
	speechAfter := levels[75]
	if !(speechBefore > -25.0 && speechAfter > -25.0) {
		t.Fatalf("both talkspurts decode at speech level: %.1f then %.1f dBFS", speechBefore, speechAfter)
	}
	if !(absF32(speechAfter-speechBefore) < 6.0) {
		t.Fatalf("the talkspurt after 44 inactive frames must decode like the one before it (%.1f vs %.1f dBFS): a range-coder desync shows up here", speechBefore, speechAfter)
	}
	if !(silence < speechBefore-20.0) {
		t.Fatalf("the inactive stretch stays a floor, not noise blown up by a desync (%.1f dBFS)", silence)
	}
}

// encodeParseHexLine decodes one hex frame line (Rust `u8::from_str_radix` per byte pair).
func encodeParseHexLine(t *testing.T, line string) []byte {
	t.Helper()
	frame := make([]byte, 0, len(line)/2)
	for i := 0; i < len(line); i += 2 {
		b, err := strconv.ParseUint(line[i:i+2], 16, 8)
		if err != nil {
			t.Fatalf("hex: %v", err)
		}
		frame = append(frame, byte(b))
	}
	return frame
}

// Dev oracle: decode hex frames (MLOW_HEX_PITCH) through `decodeSmplPitch`, dumping each voiced
// frame's reconstructed `blockLags` (the `laginds` domain) to MLOW_PITCH_DUMP. Used to prove the
// decoder reconstructs `laginds` from reference-encoded bytes (representation equivalence).
func TestEncode_DumpDecodedPitchFromHex(t *testing.T) {
	hexpath, ok := os.LookupEnv("MLOW_HEX_PITCH")
	if !ok {
		t.Skip("MLOW_HEX_PITCH unset")
	}
	out, ok := os.LookupEnv("MLOW_PITCH_DUMP")
	if !ok {
		t.Fatal("MLOW_PITCH_DUMP path")
	}
	text, err := os.ReadFile(hexpath)
	if err != nil {
		t.Fatalf("read hex: %v", err)
	}
	mem := loadSmplMem()
	cc := loadCcTables()
	tbl := loadSmplTables()
	var recs []string
	for pkt, line := range strings.Split(string(text), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		frame := encodeParseHexLine(t, line)
		if len(frame) == 0 || frame[0] != 0x50 {
			continue
		}
		config := int(frame[0]>>2) & 1
		dec := newRangeDecoder(frame[1:])
		lstate := defaultSmplLsfState()
		for f := 0; f < 3; f++ {
			lsf := decodeSmplLsf(&dec, tbl, &lstate, config, f, true)
			pulses := decodeSmplPulses(&dec, cc, 320, 4, 1, int32(config), lsf.stage1)
			if lsf.stage1 == 1 {
				pr := decodeSmplPitch(&dec, mem, cc, &lstate, 320, 4, int32(config), pulses.subfr)
				lags := make([]string, len(pr.blockLags))
				for i, v := range pr.blockLags {
					lags[i] = strconv.Itoa(int(v))
				}
				recs = append(recs, fmt.Sprintf(`{"pkt":%d,"frame":%d,"voiced":1,"block_lags":[%s]}`, pkt, f, strings.Join(lags, ", ")))
			} else {
				decodeSmplGains(&dec, cc, 4, pulses.subfr)
				recs = append(recs, fmt.Sprintf(`{"pkt":%d,"frame":%d,"voiced":0}`, pkt, f))
			}
		}
	}
	if err := os.WriteFile(out, []byte("["+strings.Join(recs, ",")+"]"), 0o644); err != nil {
		t.Fatalf("write dump: %v", err)
	}
}

// encodeReadMic reads an i16 little-endian mono raw file.
func encodeReadMic(t *testing.T, path string) []int16 {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read mic: %v", err)
	}
	samples := make([]int16, len(b)/2)
	for i := range samples {
		samples[i] = int16(uint16(b[2*i]) | uint16(b[2*i+1])<<8)
	}
	return samples
}

// encodeI16Raw clamps decoded samples to i16 and serializes them little-endian.
func encodeI16Raw(pcm []float32) []byte {
	buf := make([]byte, 0, len(pcm)*2)
	for _, s := range pcm {
		v := f32ToI16(clampF32(s*32768.0, -32768.0, 32767.0))
		buf = append(buf, byte(uint16(v)), byte(uint16(v)>>8))
	}
	return buf
}

// Dev harness: encode an i16 mono 16 kHz raw file (env MLOW_MIC) into 60 ms MLow frames and write
// them as hex (one per line) to MLOW_OUT, so the peer's reference decoder can round-trip them
// against the mic. Gated on env vars so the normal suite never touches the (large,
// machine-local) mic clip.
func TestEncode_EncodeMicDumpHex(t *testing.T) {
	mic, ok := os.LookupEnv("MLOW_MIC")
	if !ok {
		t.Skip("MLOW_MIC unset")
	}
	out, ok := os.LookupEnv("MLOW_OUT")
	if !ok {
		t.Fatal("MLOW_OUT path")
	}
	samples := encodeReadMic(t, mic)
	enc := NewEncoder()
	var lines strings.Builder
	for i := 0; i+opusFrameSamps <= len(samples); i += opusFrameSamps {
		chunk := samples[i : i+opusFrameSamps]
		pcm := make([]float32, len(chunk))
		for j, s := range chunk {
			pcm[j] = float32(s) / 32768.0
		}
		frame, err := enc.Encode(pcm)
		if err != nil {
			t.Fatalf("encode: %v", err)
		}
		for _, b := range frame {
			fmt.Fprintf(&lines, "%02x", b)
		}
		lines.WriteByte('\n')
	}
	if err := os.WriteFile(out, []byte(lines.String()), 0o644); err != nil {
		t.Fatalf("write frames: %v", err)
	}
}

// Dev harness: encode the mic (MLOW_MIC) and decode it back through OUR own Decoder, writing the
// reconstruction as i16 raw to MLOW_SELFDEC_OUT. Lets the codec round-trip be measured against
// our decoder independently of the peer's reference decoder.
func TestEncode_EncodeMicSelfdecodeRaw(t *testing.T) {
	mic, ok := os.LookupEnv("MLOW_MIC")
	if !ok {
		t.Skip("MLOW_MIC unset")
	}
	out, ok := os.LookupEnv("MLOW_SELFDEC_OUT")
	if !ok {
		t.Fatal("MLOW_SELFDEC_OUT path")
	}
	samples := encodeReadMic(t, mic)
	enc := NewEncoder()
	dec := NewDecoder()
	var pcmOut []float32
	for i := 0; i+opusFrameSamps <= len(samples); i += opusFrameSamps {
		chunk := samples[i : i+opusFrameSamps]
		pcm := make([]float32, len(chunk))
		for j, s := range chunk {
			pcm[j] = float32(s) / 32768.0
		}
		frame, err := enc.Encode(pcm)
		if err != nil {
			t.Fatalf("encode: %v", err)
		}
		pcmOut = append(pcmOut, dec.Decode(frame)...)
	}
	if err := os.WriteFile(out, encodeI16Raw(pcmOut), 0o644); err != nil {
		t.Fatalf("write selfdec: %v", err)
	}
}

// Dev oracle: decode hex frames (MLOW_HEX, one per line) through OUR Decoder, write i16 raw to
// MLOW_DEC_OUT. Used to confirm our decoder reconstructs reference-encoded frames, isolating the
// decoder from the encoder.
func TestEncode_DecodeHexFramesRaw(t *testing.T) {
	hexpath, ok := os.LookupEnv("MLOW_HEX")
	if !ok {
		t.Skip("MLOW_HEX unset")
	}
	out, ok := os.LookupEnv("MLOW_DEC_OUT")
	if !ok {
		t.Fatal("MLOW_DEC_OUT path")
	}
	text, err := os.ReadFile(hexpath)
	if err != nil {
		t.Fatalf("read hex: %v", err)
	}
	dec := NewDecoder()
	var pcmOut []float32
	for _, line := range strings.Split(string(text), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		pcmOut = append(pcmOut, dec.Decode(encodeParseHexLine(t, line))...)
	}
	if err := os.WriteFile(out, encodeI16Raw(pcmOut), 0o644); err != nil {
		t.Fatalf("write dec: %v", err)
	}
}

// Go-only: the realtime path must not allocate per frame once warm. Reported, not asserted, while
// callees in other files still allocate per call (see the port report).
func TestEncode_SteadyStateAllocs(t *testing.T) {
	enc := NewEncoder()
	input := encodeSynthInput()
	dst := make([]byte, 0, 1+smplEncodeBufBytes)
	frames := len(input) / 960
	for i := 0; i < frames; i++ {
		if _, err := enc.AppendEncode(dst[:0], input[i*960:(i+1)*960]); err != nil {
			t.Fatal(err)
		}
	}
	k := 0
	allocs := testing.AllocsPerRun(48, func() {
		_, _ = enc.AppendEncode(dst[:0], input[(k%frames)*960:(k%frames+1)*960])
		k++
	})
	t.Logf("allocations per AppendEncode in steady state: %v", allocs)
}

func BenchmarkEncode_AppendEncode(b *testing.B) {
	enc := NewEncoder()
	input := encodeSynthInput()
	frames := len(input) / 960
	dst := make([]byte, 0, 1+smplEncodeBufBytes)
	for i := 0; i < frames; i++ {
		_, _ = enc.AppendEncode(dst[:0], input[i*960:(i+1)*960])
	}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		k := i % frames
		_, _ = enc.AppendEncode(dst[:0], input[k*960:(k+1)*960])
	}
}
