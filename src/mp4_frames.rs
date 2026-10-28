//! H.264 MP4 frames decoded in-process, as plain RGBA pictures.
//!
//! The `mp4` crate demuxes and `openh264` decodes the H.264 WhatsApp uses,
//! with samples converted from AVCC to Annex B. `crate::animation` turns these
//! frames into textures; a video being sent takes its poster from here.

use std::path::Path;
use std::time::Duration;

/// Maximum frame width uploaded to the GPU.
pub const MAX_WIDTH: u32 = 320;

/// Decodes up to `limit` frames of an MP4's video track in-process, each
/// scaled to at most [`MAX_WIDTH`] and passed through `convert`.
pub fn decode_mp4<T>(
    path: &Path,
    limit: usize,
    convert: impl FnMut(image::RgbaImage) -> T,
) -> Option<Vec<(T, Duration)>> {
    let file = std::fs::File::open(path).ok()?;
    let size = file.metadata().ok()?.len();
    decode_mp4_from(std::io::BufReader::new(file), size, limit, convert)
}

/// Decodes the video track of an MP4 of `size` bytes read from `reader`.
pub fn decode_mp4_from<R: std::io::Read + std::io::Seek, T>(
    reader: R,
    size: u64,
    limit: usize,
    mut convert: impl FnMut(image::RgbaImage) -> T,
) -> Option<Vec<(T, Duration)>> {
    let mut mp4 = mp4::Mp4Reader::read_header(reader, size).ok()?;
    let (track_id, timescale, sps, pps, count) = {
        let track = mp4
            .tracks()
            .values()
            .find(|track| track.track_type().ok() == Some(mp4::TrackType::Video))?;
        (
            track.track_id(),
            u64::from(track.timescale().max(1)),
            track.sequence_parameter_set().ok()?.to_vec(),
            track.picture_parameter_set().ok()?.to_vec(),
            track.sample_count(),
        )
    };
    let mut decoder = openh264::decoder::Decoder::new().ok()?;
    let mut frames: Vec<(T, Duration)> = Vec::new();
    let mut delays: std::collections::VecDeque<Duration> = std::collections::VecDeque::new();
    // Send parameter sets and samples to the decoder in Annex B format.
    let mut parameters = Vec::new();
    push_annex_b(&mut parameters, &sps);
    push_annex_b(&mut parameters, &pps);
    let _ = decoder.decode(&parameters);
    for sample_id in 1..=count {
        if frames.len() >= limit {
            break;
        }
        let Ok(Some(sample)) = mp4.read_sample(track_id, sample_id) else {
            break;
        };
        let delay =
            Duration::from_millis((u64::from(sample.duration) * 1000 / timescale).clamp(20, 1000));
        delays.push_back(delay);
        let mut annex_b = Vec::with_capacity(sample.bytes.len() + 16);
        avcc_to_annex_b(&mut annex_b, &sample.bytes);
        if let Ok(Some(yuv)) = decoder.decode(&annex_b) {
            let delay = delays.pop_front().unwrap_or(delay);
            if let Some(frame) = frame_of(&yuv) {
                frames.push((convert(frame), delay));
            }
        }
    }
    if let Ok(rest) = decoder.flush_remaining() {
        for yuv in &rest {
            if frames.len() >= limit {
                break;
            }
            let delay = delays.pop_front().unwrap_or(Duration::from_millis(66));
            if let Some(frame) = frame_of(yuv) {
                frames.push((convert(frame), delay));
            }
        }
    }
    (!frames.is_empty()).then_some(frames)
}

/// What an MP4 says about itself, with a picture from its start: what the
/// message sending it carries, so it shows as a video before it is downloaded.
pub struct Poster {
    /// A frame about a second in (openings are often black), scaled down.
    /// `None` when the codec is not one decoded in-process.
    pub picture: Option<image::RgbImage>,
    pub width: u32,
    pub height: u32,
    pub seconds: u32,
}

/// Reads the size, length, and a poster frame of the MP4 in `bytes`.
pub fn poster(bytes: &[u8]) -> Option<Poster> {
    let size = bytes.len() as u64;
    let mp4 = mp4::Mp4Reader::read_header(std::io::Cursor::new(bytes), size).ok()?;
    let track = mp4
        .tracks()
        .values()
        .find(|track| track.track_type().ok() == Some(mp4::TrackType::Video))?;
    let (width, height) = (u32::from(track.width()), u32::from(track.height()));
    let seconds = mp4.duration().as_secs_f64().round() as u32;
    // A second in, within what a sixty-frame clip costs to decode.
    let frames = (track.frame_rate().round() as usize).clamp(1, 60);
    let picture = decode_mp4_from(std::io::Cursor::new(bytes), size, frames, |frame| frame)
        .and_then(|decoded| decoded.into_iter().last())
        .map(|(frame, _)| image::DynamicImage::ImageRgba8(frame).into_rgb8());
    (width > 0 && height > 0).then_some(Poster {
        picture,
        width,
        height,
        seconds,
    })
}

/// Frame `index` of an H.264 MP4 (or its last, if shorter), for demo posters.
pub fn video_frame(path: &Path, index: usize) -> Option<image::RgbaImage> {
    decode_mp4(path, index + 1, |frame| frame)?
        .pop()
        .map(|(image, _)| image)
}

/// Converts one decoded frame, scaled to at most [`MAX_WIDTH`].
fn frame_of(yuv: &openh264::decoder::DecodedYUV<'_>) -> Option<image::RgbaImage> {
    use openh264::formats::YUVSource;

    let (width, height) = yuv.dimensions();
    if width == 0 || height == 0 {
        return None;
    }
    let mut rgba = vec![0u8; width * height * 4];
    yuv.write_rgba8(&mut rgba);
    let image = image::RgbaImage::from_raw(width as u32, height as u32, rgba)?;
    let out_width = (width as u32).min(MAX_WIDTH);
    let out_height = ((height as u64 * out_width as u64 / width as u64) as u32).max(1);
    Some(if out_width == width as u32 {
        image
    } else {
        image::imageops::resize(
            &image,
            out_width,
            out_height,
            image::imageops::FilterType::Triangle,
        )
    })
}

pub(crate) fn push_annex_b(out: &mut Vec<u8>, nal: &[u8]) {
    out.extend_from_slice(&[0, 0, 0, 1]);
    out.extend_from_slice(nal);
}

/// Converts length-prefixed AVCC NAL units to Annex B start codes.
pub(crate) fn avcc_to_annex_b(out: &mut Vec<u8>, sample: &[u8]) {
    let mut rest = sample;
    while rest.len() >= 4 {
        let length = u32::from_be_bytes([rest[0], rest[1], rest[2], rest[3]]) as usize;
        rest = &rest[4..];
        if length == 0 || length > rest.len() {
            break;
        }
        push_annex_b(out, &rest[..length]);
        rest = &rest[length..];
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    /// A video being sent gets its picture, size, and length from its bytes.
    #[test]
    fn a_video_says_its_size_and_length_and_shows_a_frame() {
        let bytes = include_bytes!("../tests/fixtures/video/sample.mp4");
        let poster = poster(bytes).expect("an MP4");
        assert_eq!((poster.width, poster.height, poster.seconds), (320, 180, 3));
        let picture = poster.picture.expect("H.264 decodes in-process");
        assert_eq!(picture.width() * 180, picture.height() * 320);
        // Bars of colour, not a blank frame.
        let first = picture.get_pixel(4, picture.height() / 2);
        let last = picture.get_pixel(picture.width() - 4, picture.height() / 2);
        assert_ne!(first, last);
        assert!(super::poster(b"not a video").is_none());
    }
}
