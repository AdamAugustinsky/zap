// Golden values for gocore/av's voice tests, from upstream's src/voice.rs.
// normalize, waveform and mono_at_rate below are copied from it unchanged.
// Regenerate: rustc -O voice_rs_golden.rs -o /tmp/golden && /tmp/golden > voice_rs.json

const RATE: u32 = 48_000;
const BARS: usize = 64;

/// Normalizes a quiet recording to just below full scale, with capped gain.
pub fn normalize(samples: &mut [f32]) {
    let peak = samples
        .iter()
        .fold(0.0f32, |peak, sample| peak.max(sample.abs()));
    if peak <= 0.0 {
        return;
    }
    let gain = (0.89 / peak).clamp(1.0, 10.0);
    if gain > 1.0 {
        for sample in samples {
            *sample *= gain;
        }
    }
}

/// Builds WhatsApp's 0–100 waveform bars from clip loudness.
pub fn waveform(samples: &[f32]) -> Vec<u8> {
    if samples.is_empty() {
        return vec![0; BARS];
    }
    let slice = samples.len().div_ceil(BARS);
    let loudness: Vec<f32> = samples
        .chunks(slice)
        .map(|chunk| (chunk.iter().map(|s| s * s).sum::<f32>() / chunk.len() as f32).sqrt())
        .collect();
    let loudest = loudness.iter().copied().fold(0.0f32, f32::max);
    let mut bars: Vec<u8> = loudness
        .iter()
        .map(|value| {
            if loudest > 0.0 {
                (value / loudest * 100.0).round() as u8
            } else {
                0
            }
        })
        .collect();
    bars.resize(BARS, 0);
    bars
}

/// Mixes interleaved channels to mono and resamples to `RATE`.
pub fn mono_at_rate(interleaved: &[f32], channels: u16, rate: u32) -> Vec<f32> {
    let channels = usize::from(channels.max(1));
    let mono: Vec<f32> = interleaved
        .chunks_exact(channels)
        .map(|frame| frame.iter().sum::<f32>() / channels as f32)
        .collect();
    if rate == RATE || rate == 0 || mono.is_empty() {
        return mono;
    }
    let ratio = f64::from(rate) / f64::from(RATE);
    let count = (mono.len() as f64 / ratio).floor() as usize;
    (0..count)
        .map(|index| {
            let position = index as f64 * ratio;
            let left = position.floor() as usize;
            let t = (position - left as f64) as f32;
            let a = mono[left.min(mono.len() - 1)];
            let b = mono.get(left + 1).copied().unwrap_or(a);
            a + (b - a) * t
        })
        .collect()
}

struct Lcg(u32);

impl Lcg {
    /// The next sample, exactly representable: [-1, 1) in steps of 2^-23.
    fn next(&mut self) -> f32 {
        self.0 = self.0.wrapping_mul(1_664_525).wrapping_add(1_013_904_223);
        ((self.0 >> 8) as f32) * (1.0 / 8_388_608.0) - 1.0
    }
    fn take(&mut self, n: usize, scale: f32) -> Vec<f32> {
        (0..n).map(|_| self.next() * scale).collect()
    }
}

fn fnv(samples: &[f32]) -> u64 {
    let mut h: u64 = 0xcbf2_9ce4_8422_2325;
    for s in samples {
        for b in s.to_bits().to_le_bytes() {
            h ^= u64::from(b);
            h = h.wrapping_mul(0x0000_0100_0000_01b3);
        }
    }
    h
}

fn bars(name: &str, samples: &[f32]) {
    println!("{{\"case\":\"waveform {name}\",\"bars\":{:?}}}", waveform(samples));
}

fn main() {
    let mut r = Lcg(1);
    let n = 48_000;
    let ramp: Vec<f32> = r.take(n, 1.0).iter().enumerate().map(|(i, s)| s * (i as f32 / n as f32)).collect();
    bars("ramp", &ramp);
    for len in [1usize, 2, 63, 64, 65, 100, 4097] {
        bars(&format!("noise {len}"), &r.take(len, 0.3));
    }
    let mut gaps = r.take(9_600, 0.5);
    for s in &mut gaps[2_000..5_000] {
        *s = 0.0;
    }
    bars("gaps", &gaps);
    bars("silence", &vec![0.0; 1000]);
    bars("empty", &[]);

    for (name, scale) in [("quiet", 0.05f32), ("faint", 0.001), ("loud", 0.95), ("silent", 0.0)] {
        let mut s = r.take(4_800, scale);
        normalize(&mut s);
        println!("{{\"case\":\"normalize {name}\",\"len\":{},\"fnv\":{},\"first\":{}}}", s.len(), fnv(&s), s[0].to_bits());
    }
    for (name, channels, rate) in [("stereo 44100", 2u16, 44_100u32), ("three 16000", 3, 16_000), ("mono 48000", 1, 48_000), ("mono 96000", 1, 96_000), ("stereo 8000", 2, 8_000)] {
        let s = r.take(9_001, 0.8);
        let m = mono_at_rate(&s, channels, rate);
        println!("{{\"case\":\"mono {name}\",\"len\":{},\"fnv\":{}}}", m.len(), fnv(&m));
    }
}
