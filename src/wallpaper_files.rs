//! The chat wallpaper image's file: ZapFast's own copy, made from the image
//! the reader chose, and its removal. Drawing it is `crate::wallpaper`'s.

use std::path::{Path, PathBuf};

/// The longest side a wallpaper image keeps, which bounds its texture to about
/// 26 MB even for a square picture while staying sharp on a 1440p display.
pub const MAX_SIDE: u32 = 2560;

/// The extensions a copied wallpaper image may have.
const EXTENSIONS: [&str; 4] = ["jpg", "png", "webp", "gif"];

pub(crate) fn bounded(image: image::DynamicImage) -> image::DynamicImage {
    if image.width().max(image.height()) > MAX_SIDE {
        image.resize(MAX_SIDE, MAX_SIDE, image::imageops::FilterType::Triangle)
    } else {
        image
    }
}

/// Copies a chosen image to ZapFast's own `wallpaper.<ext>`, so the original
/// may move or go. An image larger than [`MAX_SIDE`] is scaled down first and
/// stored as JPEG, or PNG when it has transparency; a smaller one is copied as
/// it is. Earlier copies are removed once the new one is in place.
pub fn import(source: &Path, state: &Path) -> Result<PathBuf, String> {
    let bytes = std::fs::read(source).map_err(|error| error.to_string())?;
    let format = image::guess_format(&bytes).map_err(|_| "not an image".to_owned())?;
    let decoded =
        image::load_from_memory_with_format(&bytes, format).map_err(|error| error.to_string())?;
    let (contents, extension) = if decoded.width().max(decoded.height()) > MAX_SIDE {
        encode(&bounded(decoded))?
    } else {
        let extension = match format {
            image::ImageFormat::Jpeg => "jpg",
            image::ImageFormat::Png => "png",
            image::ImageFormat::WebP => "webp",
            image::ImageFormat::Gif => "gif",
            _ => return Err("unsupported image format".to_owned()),
        };
        (bytes, extension)
    };
    let target = state.join(format!("wallpaper.{extension}"));
    fastframe_file::write(&target, contents).map_err(|error| error.to_string())?;
    for other in EXTENSIONS.into_iter().filter(|other| *other != extension) {
        let _ = std::fs::remove_file(state.join(format!("wallpaper.{other}")));
    }
    Ok(target)
}

fn encode(image: &image::DynamicImage) -> Result<(Vec<u8>, &'static str), String> {
    let mut contents = Vec::new();
    let extension = if image.color().has_alpha() {
        image
            .write_to(
                &mut std::io::Cursor::new(&mut contents),
                image::ImageFormat::Png,
            )
            .map_err(|error| error.to_string())?;
        "png"
    } else {
        image::codecs::jpeg::JpegEncoder::new_with_quality(&mut contents, 90)
            .encode_image(&image.to_rgb8())
            .map_err(|error| error.to_string())?;
        "jpg"
    };
    Ok((contents, extension))
}

/// Deletes ZapFast's copy of the wallpaper image, whatever its extension.
pub fn remove(state: &Path) {
    for extension in EXTENSIONS {
        match std::fs::remove_file(state.join(format!("wallpaper.{extension}"))) {
            Ok(()) => {}
            Err(error) if error.kind() == std::io::ErrorKind::NotFound => {}
            Err(error) => log::debug!("could not delete the wallpaper image: {error}"),
        }
    }
}
