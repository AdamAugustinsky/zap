//! What a video being sent carries before it is downloaded: its thumbnail,
//! its size in pixels and its length, from the MP4's poster.

use crate::mp4_frames::Poster;

/// What a video message carries; each part is `None` when the poster could
/// not say.
#[derive(Debug, PartialEq)]
pub(super) struct Preview {
    /// The JPEG thumbnail.
    pub thumbnail: Option<Vec<u8>>,
    /// Width and height.
    pub size: Option<(u32, u32)>,
    /// Whole seconds.
    pub seconds: Option<u32>,
}

pub(super) fn of(poster: Option<&Poster>) -> Preview {
    Preview {
        thumbnail: poster
            .and_then(|poster| poster.picture.clone())
            .and_then(|picture| super::thumbnail_jpeg(&image::DynamicImage::ImageRgb8(picture))),
        size: poster.map(|poster| (poster.width, poster.height)),
        seconds: poster.map(|poster| poster.seconds),
    }
}

#[cfg(test)]
mod tests {
    use sha2::Digest;

    fn sha256(bytes: &[u8]) -> String {
        sha2::Sha256::digest(bytes)
            .iter()
            .map(|byte| format!("{byte:02x}"))
            .collect()
    }

    /// The poster moved out of the egui animation code into `mp4_frames`. A
    /// sent video must still carry what it did before: these are the
    /// thumbnail, size and length the `ui` build produced at 1f31b06 (where
    /// `animation::poster` decoded through egui's `ColorImage`), and the
    /// test passes with and without `ui`.
    #[test]
    fn a_sent_video_carries_the_same_thumbnail_size_and_length() {
        let golden = [
            (
                "tests/fixtures/video/sample.mp4",
                (320, 180),
                3,
                "2dca5a4fd176737c171eaedf5cb796df00803d80789117b38993c0db7e4cb36c",
                "c0e184fd050b0ebb93059fa16ddaddb5c5a543c5707bcacd2b34ca4362939279",
            ),
            (
                "assets/demo/videos/launch.mp4",
                (640, 360),
                4,
                "36ff1ab7709848c9fb57c85677e6da01cdfede04c5fc3d0afa7cf68332f62c43",
                "346a9a67edacf99c74d1c8d076056dc3d10f3384a96d0306d6e080ebd0bc4baa",
            ),
            (
                "assets/demo/videos/note.mp4",
                (360, 360),
                4,
                "5d1adba581aa114de75e92793bcff3090328bb7525f6f8f525bc32abb9d1b724",
                "56ae63c128f9010e3687cf86df17d3f05657c065c1da15ee267ae399691a1f10",
            ),
        ];
        for (path, size, seconds, picture, thumbnail) in golden {
            let bytes = std::fs::read(path).unwrap();
            let poster = crate::mp4_frames::poster(&bytes).expect("an MP4");
            let decoded = poster.picture.as_ref().expect("H.264 decodes in-process");
            assert_eq!(sha256(decoded.as_raw()), picture, "{path}: poster picture");
            let preview = super::of(Some(&poster));
            assert_eq!(preview.size, Some(size), "{path}");
            assert_eq!(preview.seconds, Some(seconds), "{path}");
            let jpeg = preview.thumbnail.expect("a thumbnail");
            assert_eq!(sha256(&jpeg), thumbnail, "{path}: thumbnail");
        }
        assert_eq!(
            super::of(None),
            super::Preview {
                thumbnail: None,
                size: None,
                seconds: None,
            }
        );
    }
}
