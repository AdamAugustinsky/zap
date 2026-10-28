// Package mlow is a pure-Go port of MLow (Meta's "smpl_audio_codec"), the
// Opus-forked split-band CELP codec WhatsApp 1:1 calls use by default.
//
// Ported from whatsapp-rust 5e72baf wacore/src/voip/mlow (MIT, Copyright (c)
// 2025 João Lucas de Oliveira Lopes). Every Go file mirrors one Rust file of
// the same name, so the two can be diffed side by side; each carries its own
// attribution header.
//
// The codec runs at one operating point: 16 kHz mono, 60 ms packets of three
// 20 ms internal frames (960 samples), config 0. [Encoder] turns 960 samples
// into one packet; [Decoder] turns a packet (bare, multiframe envelope or
// SplitRed) back into PCM and conceals what it cannot decode with silence of
// the declared duration.
//
// # Exactness and determinism
//
// The port is deterministic: the same input gives the same packet bytes and
// the same samples on every platform Go runs on. Go may fuse x*y+z into one
// FMA instruction (it does on arm64), which Rust never does, so every float
// product that feeds an addition or subtraction is wrapped in an explicit
// float32(...) or float64(...) conversion: the Go spec guarantees a
// conversion rounds and prevents fusion. Rust's float semantics that Go does
// not share (NaN-ignoring max/min, saturating float-to-int casts, powi) live
// in rustmath.go. The transcendentals (exp, log, log2, log10, pow, sin, cos,
// tanh) come from detmath.go, written for this package from their textbook
// series, in float64 and rounded to float32 where Rust works in f32. They
// are not bit-identical to the platform libm Rust calls, so neither is the
// port's audio, and it need not be: a WhatsApp peer decodes any valid MLow
// bitstream.
//
// What the tests hold the port to (rust_golden_test.go against vectors from
// zfcalls/tests/mlow_golden.rs, plus the per-stage fixture suites ported
// from the Rust module):
//
//   - Bit for bit: the range coder, the TOC, multiframe and SplitRed
//     framing, the decoder's frame reports and declared lengths, and the
//     integer-based stages on Rust's fixtures (VAD, pulse decoding, gain and
//     parameter decoding, LSF quantization indices).
//   - Within a tolerance: the decoder's samples on Rust's packets, at most
//     1e-4 from Rust's on any sample and 90 dB SNR against them per stream
//     (measured: 1e-5 and 105 dB at worst, most streams under 3e-7 and
//     above 140 dB).
//   - By quality: the encoder's packets must decode as coded frames, with
//     nothing concealed, in both the Go decoder and Rust's (the Rust test
//     decodes testdata/go_encoded.json), and reach the spectral SNR against
//     the input that Rust's encoder reaches on the same input, less 1 dB.
//     Where the arithmetic agrees the packets are Rust's byte for byte; a
//     last-bit difference that flips one of the encoder's tie-breaking
//     decisions makes them a different, equally valid encoding.
//   - The Rust module's own fixture gates keep their tolerances.
package mlow
