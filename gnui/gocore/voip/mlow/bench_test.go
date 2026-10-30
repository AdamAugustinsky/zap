// Per-packet codec benchmarks. A packet is 60 ms (three 20 ms internal
// frames); ns/20ms reports the cost of one internal frame.

package mlow

import (
	"testing"
)

func benchSpeech() []float32 { return goldenSpeechLike(24, []int{8, 9}, 3) }

func BenchmarkEncode(b *testing.B) {
	pcm := benchSpeech()
	enc := NewEncoder()
	dst := make([]byte, 0, 1024)
	frames := len(pcm) / FrameSamples
	// Warm the encoder's state and scratch before measuring.
	for i := range frames {
		if _, err := enc.AppendEncode(dst[:0], pcm[i*FrameSamples:(i+1)*FrameSamples]); err != nil {
			b.Fatal(err)
		}
	}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; b.Loop(); i++ {
		f := i % frames
		if _, err := enc.AppendEncode(dst[:0], pcm[f*FrameSamples:(f+1)*FrameSamples]); err != nil {
			b.Fatal(err)
		}
	}
	b.ReportMetric(float64(b.Elapsed().Nanoseconds())/float64(b.N)/3, "ns/20ms")
}

func BenchmarkEncodeInt16(b *testing.B) {
	pcm := goldenToI16(benchSpeech())
	enc := NewEncoder()
	dst := make([]byte, 0, 1024)
	frames := len(pcm) / FrameSamples
	for i := range frames {
		if _, err := enc.AppendEncodeInt16(dst[:0], pcm[i*FrameSamples:(i+1)*FrameSamples]); err != nil {
			b.Fatal(err)
		}
	}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; b.Loop(); i++ {
		f := i % frames
		if _, err := enc.AppendEncodeInt16(dst[:0], pcm[f*FrameSamples:(f+1)*FrameSamples]); err != nil {
			b.Fatal(err)
		}
	}
	b.ReportMetric(float64(b.Elapsed().Nanoseconds())/float64(b.N)/3, "ns/20ms")
}

func BenchmarkDecode(b *testing.B) {
	pcm := benchSpeech()
	enc := NewEncoder()
	var packets [][]byte
	for i := 0; i+FrameSamples <= len(pcm); i += FrameSamples {
		p, err := enc.Encode(pcm[i : i+FrameSamples])
		if err != nil {
			b.Fatal(err)
		}
		packets = append(packets, p)
	}
	dec := NewDecoder()
	dst := make([]float32, 0, 2*FrameSamples)
	for _, p := range packets {
		dst = dec.AppendDecode(dst[:0], p)
	}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; b.Loop(); i++ {
		dst = dec.AppendDecode(dst[:0], packets[i%len(packets)])
	}
	b.ReportMetric(float64(b.Elapsed().Nanoseconds())/float64(b.N)/3, "ns/20ms")
}

// benchEnvelope is zfcalls/tests/mlow_golden.rs `envelope`: a multiframe envelope as the real
// client sends for a 120 ms frame length, with Opus-style one/two-byte lengths (`push_len`).
func benchEnvelope(frames [][]byte) []byte {
	out := []byte{0x82 | (frames[0][0] & 0x39), uint8(len(frames))}
	for _, f := range frames[:len(frames)-1] {
		if n := len(f); n < 252 {
			out = append(out, uint8(n))
		} else {
			b0 := 252 + (n & 3)
			out = append(out, uint8(b0), uint8((n-b0)>>2))
		}
	}
	for _, f := range frames {
		out = append(out, f...)
	}
	return out
}

// benchRed is zfcalls/tests/mlow_golden.rs `red`: a SplitRed payload carrying `prev` as one
// redundant block when it fits (nil = none).
func benchRed(prev, main []byte) []byte {
	var out []byte
	if prev != nil && len(prev) <= 255 {
		out = append(out, 0x80|3, uint8(len(prev)), 0x00)
		out = append(out, prev...)
	} else {
		out = append(out, 0x00)
	}
	return append(out, main...)
}

// TestSteadyStateAllocs pins the zero-allocation hot path: once warm, encoding (float and int16
// input) and decoding a packet into a buffer with capacity allocates nothing, including the
// SplitRed (SetRedundancy(1)) and multiframe-envelope decode paths.
func TestSteadyStateAllocs(t *testing.T) {
	if testing.CoverMode() != "" {
		t.Skip("coverage instrumentation allocates")
	}
	pcm := benchSpeech()
	pcm16 := goldenToI16(pcm)
	frames := len(pcm) / FrameSamples

	// Build the packet stream once with a separate encoder, as the golden scenarios do.
	var packets [][]byte
	{
		enc := NewEncoder()
		for i := range frames {
			p, err := enc.Encode(pcm[i*FrameSamples : (i+1)*FrameSamples])
			if err != nil {
				t.Fatal(err)
			}
			packets = append(packets, p)
		}
	}
	var reds, envelopes [][]byte
	for i := range packets {
		var prev []byte
		if i > 0 {
			prev = packets[i-1]
		}
		reds = append(reds, benchRed(prev, packets[i]))
	}
	for i := 0; i+1 < len(packets); i += 2 {
		envelopes = append(envelopes, benchEnvelope(packets[i:i+2]))
	}

	pkt := make([]byte, 0, 1024)
	encode := func(name string, run func(f int) error) {
		t.Helper()
		// Warm the encoder over the whole input before measuring.
		for f := range frames {
			if err := run(f); err != nil {
				t.Fatal(err)
			}
		}
		i := 0
		allocs := testing.AllocsPerRun(20, func() {
			f := i % frames
			i++
			if err := run(f); err != nil {
				t.Fatal(err)
			}
		})
		if allocs != 0 {
			t.Errorf("%s steady state allocates %v per packet", name, allocs)
		}
	}
	enc := NewEncoder()
	encode("AppendEncode", func(f int) error {
		var err error
		pkt, err = enc.AppendEncode(pkt[:0], pcm[f*FrameSamples:(f+1)*FrameSamples])
		return err
	})
	enc16 := NewEncoder()
	encode("AppendEncodeInt16", func(f int) error {
		var err error
		pkt, err = enc16.AppendEncodeInt16(pkt[:0], pcm16[f*FrameSamples:(f+1)*FrameSamples])
		return err
	})

	out := make([]float32, 0, maxPacketSamps)
	decode := func(name string, redundancy int32, payloads [][]byte, wantDecoded uint8) {
		t.Helper()
		dec := NewDecoder()
		dec.SetRedundancy(redundancy)
		for _, p := range payloads {
			out = dec.AppendDecode(out[:0], p)
			if r := dec.TakeFrameReport(); r.Decoded != wantDecoded {
				t.Fatalf("%s: warm-up report %+v, want %d decoded", name, r, wantDecoded)
			}
		}
		i := 0
		allocs := testing.AllocsPerRun(20, func() {
			out = dec.AppendDecode(out[:0], payloads[i%len(payloads)])
			i++
		})
		if allocs != 0 {
			t.Errorf("%s steady state allocates %v per packet", name, allocs)
		}
	}
	// Each packet chains three 20 ms internal frames; an envelope carries two packets.
	decode("AppendDecode", 0, packets, 3)
	decode("AppendDecode SplitRed", 1, reds, 3)
	decode("AppendDecode multiframe", 0, envelopes, 6)
}
