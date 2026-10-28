//! Voice-note playback: an Ogg/Opus file of the account's decoded to PCM
//! with upstream's `voice::decode` (`zf_voice_decode`), on the caller's
//! thread, never the core's.
//!
//! Only files under the account's own folders are read: its state and cache
//! (where downloads and sent media go), the download folder `SetDownloadFolder`
//! last named, and for the demo core its own media folder. Paths are
//! compared after symbolic links are resolved.

use std::path::{Path, PathBuf};

use crate::wire::BlobRef;

/// Samples a second, mono.
pub const RATE: u32 = zapfast::voice::RATE;
/// The longest note `SendVoice` takes: upstream's recorder stops there.
pub const LONGEST_RECORDING_SECONDS: u64 = 15 * 60;
/// The longest note decoded: 30 minutes.
pub const LONGEST_SECONDS: u64 = 30 * 60;
/// The largest file read.
pub const LARGEST_FILE: u64 = 64 << 20;

/// A decoded note: its PCM blob (the caller's), its length in samples, and
/// its 64-bar waveform.
#[derive(Debug)]
pub struct Decoded {
    pub blob: BlobRef,
    pub frames: u64,
    pub waveform: [u8; zapfast::voice::BARS],
}

#[derive(Debug, PartialEq, Eq)]
pub enum VoiceError {
    /// Not under one of the account's folders.
    Outside,
    NotFound,
    /// Over `LARGEST_FILE` or `LONGEST_SECONDS`.
    TooLong,
    /// Not Ogg/Opus, or damaged.
    Malformed(String),
    Io(String),
}

/// Whether `path`, resolved, lies under one of `roots`, resolved.
pub fn inside(path: &Path, roots: &[PathBuf]) -> Result<PathBuf, VoiceError> {
    let real = std::fs::canonicalize(path).map_err(|error| match error.kind() {
        std::io::ErrorKind::NotFound => VoiceError::NotFound,
        _ => VoiceError::Io(error.to_string()),
    })?;
    let allowed = roots
        .iter()
        .filter_map(|root| std::fs::canonicalize(root).ok())
        .any(|root| real.starts_with(&root) && real != root);
    if allowed {
        Ok(real)
    } else {
        Err(VoiceError::Outside)
    }
}

/// The granule position of the last Ogg page: the stream's length in
/// 48 kHz samples plus the encoder's pre-skip. `None` without a page.
fn last_granule(bytes: &[u8]) -> Option<u64> {
    let at = bytes.windows(4).rposition(|w| w == b"OggS")?;
    let granule = bytes.get(at + 6..at + 14)?;
    Some(u64::from_le_bytes(granule.try_into().ok()?))
}

/// Decodes the note at `path` into a new blob of `blobs`.
pub fn decode(
    path: &Path,
    roots: &[PathBuf],
    blobs: &crate::blobs::Blobs,
) -> Result<Decoded, VoiceError> {
    let real = inside(path, roots)?;
    let meta = std::fs::metadata(&real).map_err(|error| VoiceError::Io(error.to_string()))?;
    if !meta.is_file() {
        return Err(VoiceError::NotFound);
    }
    if meta.len() > LARGEST_FILE {
        return Err(VoiceError::TooLong);
    }
    let bytes = std::fs::read(&real).map_err(|error| VoiceError::Io(error.to_string()))?;
    // Told by the stream before it is decoded, so a note too long for memory
    // is never decoded at all.
    if last_granule(&bytes).is_some_and(|granule| granule > (LONGEST_SECONDS + 1) * u64::from(RATE))
    {
        return Err(VoiceError::TooLong);
    }
    let samples = zapfast::voice::decode(&bytes).map_err(VoiceError::Malformed)?;
    if samples.len() as u64 > (LONGEST_SECONDS + 1) * u64::from(RATE) {
        return Err(VoiceError::TooLong);
    }
    let mut waveform = [0u8; zapfast::voice::BARS];
    for (bar, value) in waveform.iter_mut().zip(zapfast::voice::waveform(&samples)) {
        *bar = value;
    }
    let mut pcm = Vec::with_capacity(samples.len() * 4);
    for sample in &samples {
        pcm.extend_from_slice(&sample.to_le_bytes());
    }
    Ok(Decoded {
        blob: blobs.put(pcm),
        frames: samples.len() as u64,
        waveform,
    })
}

#[cfg(test)]
mod tests {
    use super::*;

    fn tone(seconds: f32) -> Vec<f32> {
        (0..(seconds * RATE as f32) as usize)
            .map(|i| 0.5 * (i as f32 * 440.0 * std::f32::consts::TAU / RATE as f32).sin())
            .collect()
    }

    #[test]
    fn a_note_in_the_folders_decodes_to_mono_48_khz_and_others_are_refused() {
        let root = tempfile::tempdir().unwrap();
        let account = root.path().join("account");
        std::fs::create_dir_all(account.join("media")).unwrap();
        let note = account.join("media/note.ogg");
        std::fs::write(&note, zapfast::voice::encode(&tone(1.5)).unwrap()).unwrap();
        let blobs = crate::blobs::Blobs::default();
        let roots = vec![account.clone()];

        let decoded = decode(&note, &roots, &blobs).unwrap();
        // 1.5 s at 48 kHz, the pre-skip trimmed; the last 20 ms frame is
        // whole, as upstream's decode leaves it.
        assert!(
            (72_000..72_960).contains(&decoded.frames),
            "{}",
            decoded.frames
        );
        assert_eq!(decoded.blob.len, decoded.frames * 4);
        assert!(decoded.waveform.iter().all(|&bar| bar > 50));
        let mut first = [0u8; 4 * 48];
        blobs.read(decoded.blob.id, 4 * 4800, &mut first).unwrap();
        let peak = first
            .chunks(4)
            .map(|b| f32::from_le_bytes(b.try_into().unwrap()).abs())
            .fold(0.0f32, f32::max);
        assert!((0.3..0.7).contains(&peak), "the tone's level, got {peak}");

        // Outside the account, through a link out of it, or missing.
        let outside = root.path().join("elsewhere.ogg");
        std::fs::copy(&note, &outside).unwrap();
        assert_eq!(
            decode(&outside, &roots, &blobs).err(),
            Some(VoiceError::Outside)
        );
        let link = account.join("media/link.ogg");
        std::os::unix::fs::symlink(&outside, &link).unwrap();
        assert_eq!(
            decode(&link, &roots, &blobs).err(),
            Some(VoiceError::Outside)
        );
        let dotted = account.join("media/../../elsewhere.ogg");
        assert_eq!(
            decode(&dotted, &roots, &blobs).err(),
            Some(VoiceError::Outside)
        );
        assert_eq!(
            decode(&account.join("media/gone.ogg"), &roots, &blobs).err(),
            Some(VoiceError::NotFound)
        );
        assert_eq!(
            decode(&account, &roots, &blobs).err(),
            Some(VoiceError::Outside)
        );

        // Not Ogg/Opus.
        let text = account.join("media/note.txt");
        std::fs::write(&text, b"not a voice note").unwrap();
        assert!(matches!(
            decode(&text, &roots, &blobs),
            Err(VoiceError::Malformed(_))
        ));
        assert_eq!(blobs.count(), 1, "only the decoded note's blob");
    }

    #[test]
    fn a_stream_that_says_it_is_too_long_is_not_decoded() {
        let mut bytes = zapfast::voice::encode(&tone(0.1)).unwrap();
        let at = bytes.windows(4).rposition(|w| w == b"OggS").unwrap();
        let long = (LONGEST_SECONDS + 2) * u64::from(RATE);
        bytes[at + 6..at + 14].copy_from_slice(&long.to_le_bytes());
        let root = tempfile::tempdir().unwrap();
        let note = root.path().join("long.ogg");
        std::fs::write(&note, bytes).unwrap();
        let blobs = crate::blobs::Blobs::default();
        assert_eq!(
            decode(&note, &[root.path().to_path_buf()], &blobs).err(),
            Some(VoiceError::TooLong)
        );
    }
}
