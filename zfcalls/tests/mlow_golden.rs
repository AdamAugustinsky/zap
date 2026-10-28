//! Golden MLow vectors for the Go port (gnui/gocore/voip/mlow), written by
//! whatsapp-rust 5e72baf's own encoder and decoder
//! (wacore/src/voip/mlow), and the check that the Go port's packets are
//! valid MLow for the real Rust decoder.
//!
//! `mlow_matches_the_go_golden_files` writes Rust's packets, decode reports
//! and decoded PCM. gnui/gocore/voip/mlow/rust_golden_test.go regenerates the
//! same synthetic input and holds the Go port to them: the framing (sample
//! counts, frame reports, declared packet lengths) exactly, the decoded
//! samples within a small tolerance (the transcendentals differ in the last
//! bits from the platform libm Rust calls), and the Go encoder to the audio
//! quality Rust's encoder reaches.
//!
//! `go_encoded_packets_are_valid_mlow` decodes the Go encoder's packets
//! (testdata/go_encoded.json, written by the Go test) with Rust's
//! MlowDecoder: every packet must decode as three coded frames with nothing
//! concealed, and the audio must reach the spectral SNR Rust's own encoder
//! reaches on the same input, less GO_SNR_MARGIN_DB.
//!
//! The input is synthetic only: tones, a sweep, LCG noise, a harmonic
//! "speech-like" signal with pauses, clipping and non-finite samples. The
//! generators use only +, -, *, / and round on f32, which Rust and Go evaluate
//! identically, so the Go side rebuilds the input instead of reading it; a
//! hash of each input guards that.
//!
//! `ZF_WRITE_MLOW_GOLDEN=1 cargo test -p zfcalls --test mlow_golden` writes
//! gnui/gocore/voip/mlow/testdata/rust_golden.json and rust_golden_pcm.f32
//! (every decode call's samples, in record order); without it the test
//! checks the committed files still match the Rust codec.

use std::path::PathBuf;

use serde_json::{Value, json};
use whatsapp_rust::wacore::voip::mlow::{MlowDecoder, MlowEncoder};

const FRAME: usize = 960;
const RATE: f32 = 16000.0;
const PI: f32 = std::f32::consts::PI;
const TWO_PI: f32 = 2.0 * std::f32::consts::PI;

/// Numerical Recipes' LCG.
struct Lcg(u32);

impl Lcg {
    fn next(&mut self) -> u32 {
        self.0 = self.0.wrapping_mul(1_664_525).wrapping_add(1_013_904_223);
        self.0
    }

    /// Uniform in [-1, 1).
    fn uniform(&mut self) -> f32 {
        (self.next() >> 8) as f32 / 8_388_608.0 - 1.0
    }
}

/// sin(x): reduce to [-pi, pi], then a degree-11 Taylor polynomial in Horner
/// form. Plain f32 arithmetic only, so Go reproduces every bit.
fn sin_approx(x: f32) -> f32 {
    let k = (x / TWO_PI).round();
    let r = x - k * TWO_PI;
    let r2 = r * r;
    r * (1.0 - r2 / 6.0 * (1.0 - r2 / 20.0 * (1.0 - r2 / 42.0 * (1.0 - r2 / 72.0 * (1.0 - r2 / 110.0)))))
}

/// Advance a phase by `step`, kept in [-pi, pi).
fn advance(phase: &mut f32, step: f32) {
    *phase += step;
    if *phase >= PI {
        *phase -= TWO_PI;
    }
}

fn tone(packets: usize, freq: f32, amp: f32) -> Vec<f32> {
    let step = TWO_PI * freq / RATE;
    let mut ph = 0.0f32;
    (0..packets * FRAME)
        .map(|_| {
            let v = amp * sin_approx(ph);
            advance(&mut ph, step);
            v
        })
        .collect()
}

fn sweep(packets: usize, f0: f32, f1: f32, amp: f32) -> Vec<f32> {
    let n = packets * FRAME;
    let mut ph = 0.0f32;
    (0..n)
        .map(|i| {
            let v = amp * sin_approx(ph);
            let f = f0 + (f1 - f0) * (i as f32 / n as f32);
            advance(&mut ph, TWO_PI * f / RATE);
            v
        })
        .collect()
}

fn noise(packets: usize, amp: f32, seed: u32) -> Vec<f32> {
    let mut lcg = Lcg(seed);
    (0..packets * FRAME).map(|_| amp * lcg.uniform()).collect()
}

/// Eight harmonics of a vibrato fundamental under a 4 Hz syllable envelope,
/// silenced in the packets `gaps` lists, over a faint noise floor: speech
/// enough to drive the VAD through active, hangover and inactive coding.
fn speech_like(packets: usize, gaps: &[usize], seed: u32) -> Vec<f32> {
    let mut lcg = Lcg(seed);
    let (mut f0_ph, mut vib_ph, mut env_ph) = (0.0f32, 0.0f32, 0.0f32);
    let mut out = Vec::with_capacity(packets * FRAME);
    for i in 0..packets * FRAME {
        let f0 = 140.0 + 30.0 * sin_approx(vib_ph);
        advance(&mut vib_ph, TWO_PI * 3.0 / RATE);
        advance(&mut f0_ph, TWO_PI * f0 / RATE);
        let mut v = 0.0f32;
        for k in 1..=8 {
            v += sin_approx(f0_ph * k as f32) / k as f32;
        }
        let env = 0.5 + 0.5 * sin_approx(env_ph);
        advance(&mut env_ph, TWO_PI * 4.0 / RATE);
        let gate = if gaps.contains(&(i / FRAME)) { 0.0 } else { 1.0 };
        out.push(0.12 * v * env * gate + 0.003 * lcg.uniform());
    }
    out
}

/// A 200 Hz square wave past full scale: the encoder clamps it.
fn loud(packets: usize) -> Vec<f32> {
    let step = TWO_PI * 200.0 / RATE;
    let mut ph = 0.0f32;
    let mut lcg = Lcg(7);
    (0..packets * FRAME)
        .map(|_| {
            let v = if sin_approx(ph) >= 0.0 { 1.5 } else { -1.5 };
            advance(&mut ph, step);
            v + 0.05 * lcg.uniform()
        })
        .collect()
}

/// Speech with NaN and infinities sprinkled in: the encoder zeroes NaN and
/// clamps the rest.
fn nonfinite(packets: usize) -> Vec<f32> {
    let mut x = speech_like(packets, &[], 11);
    for (i, s) in x.iter_mut().enumerate() {
        if i % 97 == 0 {
            *s = f32::NAN;
        } else if i % 193 == 0 {
            *s = f32::INFINITY;
        } else if i % 389 == 0 {
            *s = f32::NEG_INFINITY;
        }
    }
    x
}

/// A DC offset, a step, then a 50 Hz rectangle.
fn dc_steps(packets: usize) -> Vec<f32> {
    (0..packets * FRAME)
        .map(|i| match i / FRAME {
            0 | 1 => 0.2,
            2 => -0.3,
            _ => {
                if (i / 160) % 2 == 0 {
                    0.25
                } else {
                    -0.25
                }
            }
        })
        .collect()
}

fn to_i16(x: &[f32]) -> Vec<i16> {
    x.iter()
        .map(|&s| (s * 32767.0).round().clamp(-32768.0, 32767.0) as i16)
        .collect()
}

fn fnv1a(bytes: impl IntoIterator<Item = u8>) -> String {
    let mut h: u64 = 0xcbf2_9ce4_8422_2325;
    for b in bytes {
        h ^= u64::from(b);
        h = h.wrapping_mul(0x0100_0000_01b3);
    }
    format!("{h:016x}")
}

fn f32_hash(x: &[f32]) -> String {
    fnv1a(x.iter().flat_map(|s| s.to_bits().to_le_bytes()))
}

fn hex(bytes: &[u8]) -> String {
    bytes.iter().map(|b| format!("{b:02x}")).collect()
}

fn encode_f32(pcm: &[f32]) -> Vec<Vec<u8>> {
    let mut enc = MlowEncoder::new();
    pcm.chunks(FRAME).map(|f| enc.encode(f).expect("encode")).collect()
}

fn encode_i16(pcm: &[i16]) -> Vec<Vec<u8>> {
    let mut enc = MlowEncoder::new();
    pcm.chunks(FRAME)
        .map(|f| {
            let mut out = Vec::new();
            enc.encode_i16_into(f, &mut out).expect("encode");
            out
        })
        .collect()
}

/// One decode call: its length, the frame report and the declared packet
/// length afterwards. The samples go to `pcm`, in record order.
fn decode_record(dec: &mut MlowDecoder, payload: &[u8], pcm: &mut Vec<f32>) -> Value {
    let out = dec.decode(payload);
    let r = dec.take_frame_report();
    pcm.extend_from_slice(&out);
    json!({
        "len": out.len(),
        "report": [r.decoded, r.concealed, r.off_point, r.inactive_or_sid],
        "last_packet_samps": dec.last_packet_samps(),
    })
}

fn decode_all(dec: &mut MlowDecoder, payloads: &[Vec<u8>], pcm: &mut Vec<f32>) -> Vec<Value> {
    payloads.iter().map(|p| decode_record(dec, p, pcm)).collect()
}

/// Opus-style frame length: one byte below 252, else two.
fn push_len(out: &mut Vec<u8>, len: usize) {
    if len < 252 {
        out.push(len as u8);
    } else {
        let b0 = 252 + (len & 3);
        out.push(b0 as u8);
        out.push(((len - b0) >> 2) as u8);
    }
}

/// A multiframe envelope, as the real client sends for a 120 ms frame length.
fn envelope(frames: &[&[u8]]) -> Vec<u8> {
    let mut out = vec![0x82 | (frames[0][0] & 0x39), frames.len() as u8];
    for f in &frames[..frames.len() - 1] {
        push_len(&mut out, f.len());
    }
    for f in frames {
        out.extend_from_slice(f);
    }
    out
}

/// A SplitRed payload carrying `prev` as one redundant block when it fits.
fn red(prev: Option<&[u8]>, main: &[u8]) -> Vec<u8> {
    let mut out = Vec::new();
    match prev {
        Some(p) if p.len() <= 255 => {
            out.extend_from_slice(&[0x80 | 3, p.len() as u8, 0x00]);
            out.extend_from_slice(p);
        }
        _ => out.push(0x00),
    }
    out.extend_from_slice(main);
    out
}

fn corrupt(i: usize, p: &[u8], lcg: &mut Lcg) -> Vec<u8> {
    let mut q = p.to_vec();
    match i % 4 {
        0 => q.truncate(p.len() / 2),
        1 => q[p.len() / 3] ^= 0x10,
        2 => q.extend_from_slice(&[0xde, 0xad, 0xbe, 0xef, 0x01, 0x02, 0x03]),
        _ => {
            for b in &mut q[1..] {
                *b = (lcg.next() >> 24) as u8;
            }
        }
    }
    q
}

const SPEECH_GAPS: [usize; 7] = [8, 9, 10, 11, 18, 19, 20];

fn streams() -> Vec<(&'static str, Vec<f32>)> {
    vec![
        ("silence", vec![0.0; 8 * FRAME]),
        ("tone440", tone(10, 440.0, 0.3)),
        ("sweep", sweep(16, 100.0, 7000.0, 0.25)),
        ("noise", noise(10, 0.1, 1)),
        ("speech", speech_like(24, &SPEECH_GAPS, 3)),
        ("loud", loud(6)),
        ("nonfinite", nonfinite(4)),
        ("dc_steps", dc_steps(6)),
    ]
}

/// The i16 API's input: it normalizes by 1/32768 and gives the VAD the
/// original samples.
fn speech16() -> Vec<i16> {
    to_i16(&speech_like(12, &[5, 6], 5).iter().map(|s| s * 2.5).collect::<Vec<_>>())
}

/// Encoder state: a reset mid-stream.
fn reset_at_6(speech: &[f32]) -> Vec<Vec<u8>> {
    let mut enc = MlowEncoder::new();
    let mut packets = Vec::new();
    for (i, f) in speech.chunks(FRAME).take(12).enumerate() {
        if i == 6 {
            enc.reset();
        }
        packets.push(enc.encode(f).expect("encode"));
    }
    packets
}

/// Encoder state: the two input APIs alternating.
fn alternating_apis(speech16: &[i16]) -> Vec<Vec<u8>> {
    let mut enc = MlowEncoder::new();
    speech16
        .chunks(FRAME)
        .enumerate()
        .map(|(i, f)| {
            if i % 2 == 0 {
                let mut out = Vec::new();
                enc.encode_i16_into(f, &mut out).expect("encode");
                out
            } else {
                let norm: Vec<f32> = f.iter().map(|&s| s as f32 / 32768.0).collect();
                enc.encode(&norm).expect("encode")
            }
        })
        .collect()
}

fn golden() -> (Value, Vec<f32>) {
    let streams = streams();
    let mut all_pcm = Vec::new();
    let mut out_streams = Vec::new();
    let mut speech_packets = Vec::new();
    for (name, pcm) in &streams {
        let packets = encode_f32(pcm);
        let mut dec = MlowDecoder::new();
        let decoded = decode_all(&mut dec, &packets, &mut all_pcm);
        if *name == "speech" {
            speech_packets = packets.clone();
        }
        out_streams.push(json!({
            "name": name,
            "api": "f32",
            "input_fnv": f32_hash(pcm),
            "packets": packets.iter().map(|p| hex(p)).collect::<Vec<_>>(),
            "decoded": decoded,
        }));
    }

    let speech16 = speech16();
    let packets = encode_i16(&speech16);
    let mut dec = MlowDecoder::new();
    out_streams.push(json!({
        "name": "speech_i16",
        "api": "i16",
        "input_fnv": fnv1a(speech16.iter().flat_map(|s| s.to_le_bytes())),
        "packets": packets.iter().map(|p| hex(p)).collect::<Vec<_>>(),
        "decoded": decode_all(&mut dec, &packets, &mut all_pcm),
    }));

    let reset_packets = reset_at_6(&streams[4].1);
    let alt_packets = alternating_apis(&speech16);
    let encoder_scenarios = json!([
        {"name": "reset_at_6", "packets": reset_packets.iter().map(|p| hex(p)).collect::<Vec<_>>()},
        {"name": "alternating_apis", "packets": alt_packets.iter().map(|p| hex(p)).collect::<Vec<_>>()},
    ]);

    // Decoder scenarios over the speech packets.
    let mut scenarios = Vec::new();
    let mut scenario = |name: &str, redundancy: i32, reset_at: Option<usize>, payloads: Vec<Vec<u8>>| {
        let mut dec = MlowDecoder::new();
        dec.set_redundancy(redundancy);
        let mut outputs = Vec::new();
        for (i, p) in payloads.iter().enumerate() {
            if reset_at == Some(i) {
                dec.reset();
            }
            outputs.push(decode_record(&mut dec, p, &mut all_pcm));
        }
        scenarios.push(json!({
            "name": name,
            "redundancy": redundancy,
            "reset_at": reset_at,
            "payloads": payloads.iter().map(|p| hex(p)).collect::<Vec<_>>(),
            "outputs": outputs,
        }));
    };
    let sp = &speech_packets;
    scenario(
        "loss_every_third",
        0,
        None,
        sp.iter().enumerate().map(|(i, p)| if i % 3 == 2 { Vec::new() } else { p.clone() }).collect(),
    );
    scenario(
        "multiframe_pairs",
        0,
        None,
        sp.chunks(2).map(|c| envelope(&c.iter().map(Vec::as_slice).collect::<Vec<_>>())).collect(),
    );
    scenario(
        "split_red",
        1,
        None,
        (0..sp.len()).map(|i| red(i.checked_sub(1).map(|j| sp[j].as_slice()), &sp[i])).collect(),
    );
    let mut lcg = Lcg(99);
    scenario("corrupt", 0, None, sp.iter().enumerate().map(|(i, p)| corrupt(i, p, &mut lcg)).collect());
    scenario(
        "toc_sweep",
        0,
        None,
        (0..=255u8)
            .map(|b| {
                let mut p = sp[4].clone();
                p[0] = b;
                p
            })
            .collect(),
    );
    scenario("reset_at_12", 0, Some(12), sp.clone());

    let doc = json!({
        "source": "whatsapp-rust 5e72baf wacore/src/voip/mlow via zfcalls/tests/mlow_golden.rs",
        "frame": FRAME,
        "streams": out_streams,
        "encoder_scenarios": encoder_scenarios,
        "decoder_scenarios": scenarios,
    });
    (doc, all_pcm)
}

fn testdata() -> PathBuf {
    PathBuf::from(env!("CARGO_MANIFEST_DIR")).join("../gnui/gocore/voip/mlow/testdata")
}

#[test]
fn mlow_matches_the_go_golden_files() {
    let (doc, all_pcm) = golden();
    let json = serde_json::to_string_pretty(&doc).unwrap() + "\n";
    let pcm: Vec<u8> = all_pcm.iter().flat_map(|s| s.to_bits().to_le_bytes()).collect();
    let json_path = testdata().join("rust_golden.json");
    let pcm_path = testdata().join("rust_golden_pcm.f32");
    if std::env::var_os("ZF_WRITE_MLOW_GOLDEN").is_some() {
        std::fs::write(&json_path, &json).unwrap();
        std::fs::write(&pcm_path, &pcm).unwrap();
        return;
    }
    let committed = std::fs::read_to_string(&json_path)
        .unwrap_or_else(|e| panic!("{}: {e}; write it with ZF_WRITE_MLOW_GOLDEN=1", json_path.display()));
    assert!(committed == json, "the MLow golden vectors changed: rewrite them with ZF_WRITE_MLOW_GOLDEN=1");
    let committed = std::fs::read(&pcm_path).unwrap();
    assert!(committed == pcm, "the MLow golden PCM changed: rewrite it with ZF_WRITE_MLOW_GOLDEN=1");
}

/// The codec's delay from input to decoded output, in samples: where the
/// decoded noise stream correlates best with its input.
const CODEC_DELAY: usize = 80;

/// How far below Rust's own encoder the Go encoder's spectral SNR may land.
const GO_SNR_MARGIN_DB: f64 = 1.0;

/// Spectral SNR in dB of `output` (delayed by CODEC_DELAY) against `input`:
/// 320-sample Hann frames at a 160-sample hop, signal = sum of |X|^2, noise
/// = sum of (|X| - |Y|)^2 over bins 0..=160. A CELP codec with noise fill
/// does not keep the waveform's phase, so this compares the magnitude
/// spectra. None for a silent input.
fn spectral_snr_db(input: &[f32], output: &[f32]) -> Option<f64> {
    const N: usize = 320;
    const BINS: usize = N / 2 + 1;
    let mut cos = vec![0.0f64; N * BINS];
    let mut sin = vec![0.0f64; N * BINS];
    for k in 0..BINS {
        for j in 0..N {
            let a = 2.0 * std::f64::consts::PI * ((k * j) % N) as f64 / N as f64;
            cos[k * N + j] = a.cos();
            sin[k * N + j] = a.sin();
        }
    }
    let win: Vec<f64> =
        (0..N).map(|j| 0.5 - 0.5 * (2.0 * std::f64::consts::PI * j as f64 / N as f64).cos()).collect();
    let (mut sig, mut err) = (0.0f64, 0.0f64);
    let mut off = 0;
    while off + N <= input.len() && off + CODEC_DELAY + N <= output.len() {
        let x: Vec<f64> = (0..N).map(|j| win[j] * clean(input[off + j])).collect();
        let y: Vec<f64> = (0..N).map(|j| win[j] * f64::from(output[off + CODEC_DELAY + j])).collect();
        for k in 0..BINS {
            let (c, s) = (&cos[k * N..(k + 1) * N], &sin[k * N..(k + 1) * N]);
            let mag = |v: &[f64]| {
                let re: f64 = v.iter().zip(c).map(|(a, b)| a * b).sum();
                let im: f64 = v.iter().zip(s).map(|(a, b)| a * b).sum();
                re.hypot(im)
            };
            let (mx, my) = (mag(&x), mag(&y));
            sig += mx * mx;
            err += (mx - my) * (mx - my);
        }
        off += N / 2;
    }
    (sig > 0.0).then(|| 10.0 * (sig / err).log10())
}

/// The encoder's view of an input sample: NaN is zero, the rest is clamped
/// to full scale.
fn clean(x: f32) -> f64 {
    if x.is_nan() { 0.0 } else { f64::from(x.clamp(-1.0, 1.0)) }
}

fn unhex(s: &str) -> Vec<u8> {
    (0..s.len()).step_by(2).map(|i| u8::from_str_radix(&s[i..i + 2], 16).unwrap()).collect()
}

/// Decodes a stream of bare packets, requiring each to decode as three
/// coded frames with nothing concealed or refused.
fn decode_clean(who: &str, packets: &[Vec<u8>]) -> Vec<f32> {
    let mut dec = MlowDecoder::new();
    let mut pcm = Vec::new();
    for (i, p) in packets.iter().enumerate() {
        let out = dec.decode(p);
        let r = dec.take_frame_report();
        assert_eq!(out.len(), FRAME, "{who}: packet {i} decoded to {} samples", out.len());
        assert!(
            r.decoded == 3 && r.concealed == 0 && r.off_point == 0,
            "{who}: packet {i} report {r:?}: not three coded frames"
        );
        assert!(out.iter().all(|s| s.is_finite()), "{who}: packet {i} decoded to non-finite samples");
        pcm.extend_from_slice(&out);
    }
    pcm
}

#[test]
fn go_encoded_packets_are_valid_mlow() {
    let path = testdata().join("go_encoded.json");
    let doc: Value = serde_json::from_str(
        &std::fs::read_to_string(&path)
            .unwrap_or_else(|e| panic!("{}: {e}; the Go test writes it with ZF_WRITE_MLOW_GO=1", path.display())),
    )
    .unwrap();
    let streams = streams();
    let speech16 = speech16();
    let speech16_norm: Vec<f32> = speech16.iter().map(|&s| s as f32 / 32768.0).collect();
    let mut seen = 0;
    for entry in doc["streams"].as_array().unwrap() {
        let name = entry["name"].as_str().unwrap();
        let go: Vec<Vec<u8>> = entry["packets"].as_array().unwrap().iter().map(|p| unhex(p.as_str().unwrap())).collect();
        let (input, rust): (Vec<f32>, Vec<Vec<u8>>) = match name {
            "speech_i16" => (speech16_norm.clone(), encode_i16(&speech16)),
            "reset_at_6" => (streams[4].1[..12 * FRAME].to_vec(), reset_at_6(&streams[4].1)),
            "alternating_apis" => (speech16_norm.clone(), alternating_apis(&speech16)),
            _ => {
                let pcm = &streams.iter().find(|(n, _)| *n == name).unwrap_or_else(|| panic!("unknown {name}")).1;
                (pcm.clone(), encode_f32(pcm))
            }
        };
        assert_eq!(go.len(), rust.len(), "{name}: {} Go packets for {} Rust packets", go.len(), rust.len());
        let go_pcm = decode_clean(&format!("{name} (Go encoder)"), &go);
        let rust_pcm = decode_clean(&format!("{name} (Rust encoder)"), &rust);
        match (spectral_snr_db(&input, &go_pcm), spectral_snr_db(&input, &rust_pcm)) {
            (Some(g), Some(r)) => {
                println!("{name}: spectral SNR Go encoder {g:.2} dB, Rust encoder {r:.2} dB");
                assert!(g >= r - GO_SNR_MARGIN_DB, "{name}: Go encoder {g:.2} dB, Rust encoder {r:.2} dB");
            }
            (None, None) => {
                let rms = (go_pcm.iter().map(|s| f64::from(*s).powi(2)).sum::<f64>() / go_pcm.len() as f64).sqrt();
                println!("{name}: silent input, Go encoder output rms {rms:.2e}");
                assert!(rms < 3.2e-4, "{name}: silence decoded to rms {rms:.2e}, above -70 dBFS");
            }
            _ => unreachable!(),
        }
        seen += 1;
    }
    assert_eq!(seen, 11, "go_encoded.json should carry all 9 streams and both encoder scenarios");
}
