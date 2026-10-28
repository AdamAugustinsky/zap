//! YUV 4:2:0 pictures converted to upright, scaled RGBA, for calls and the
//! video player.
//!
//! With `ui` an [`Image`] is egui's `ColorImage`, so the window uploads call
//! frames as before. Without it, it is a plain RGBA picture with the same
//! `size` and `as_raw()`, for an interface to hand on.

#[cfg(feature = "ui")]
pub type Image = egui::ColorImage;

#[cfg(feature = "ui")]
type Pixel = egui::Color32;

/// One RGBA picture, row by row from the top, every pixel opaque.
#[cfg(not(feature = "ui"))]
#[derive(Clone, Debug, PartialEq)]
pub struct Image {
    /// Width and height in pixels.
    pub size: [usize; 2],
    pub pixels: Vec<[u8; 4]>,
}

#[cfg(not(feature = "ui"))]
type Pixel = [u8; 4];

#[cfg(not(feature = "ui"))]
impl Image {
    fn new(size: [usize; 2], pixels: Vec<Pixel>) -> Self {
        Self { size, pixels }
    }

    /// The pixels as RGBA bytes.
    pub fn as_raw(&self) -> &[u8] {
        self.pixels.as_flattened()
    }
}

#[cfg(feature = "ui")]
fn opaque(r: u8, g: u8, b: u8) -> Pixel {
    egui::Color32::from_rgb(r, g, b)
}

#[cfg(not(feature = "ui"))]
fn opaque(r: u8, g: u8, b: u8) -> Pixel {
    [r, g, b, 255]
}

/// A call's video frame on its way to the interface. With `ui` it is the
/// window's RGBA image, as before.
#[cfg(feature = "ui")]
pub type CallFrame = Image;

/// A call's video frame on its way to the interface: YUV 4:2:0 (I420), upright,
/// at the size the call screen draws it, for an interface that uploads the
/// planes itself. The Y plane, then U, then V, follow each other in `data`,
/// each `strides[i]` bytes a row; the chroma planes have `(height + 1) / 2`
/// rows.
#[cfg(not(feature = "ui"))]
#[derive(Clone, Debug, PartialEq)]
pub struct CallFrame {
    /// Width and height in pixels.
    pub size: [usize; 2],
    pub strides: [usize; 3],
    pub data: Vec<u8>,
}

#[cfg(not(feature = "ui"))]
impl CallFrame {
    /// An empty frame of `size`, every plane tightly packed.
    pub fn new(size: [usize; 2]) -> Self {
        let chroma = size[0].div_ceil(2);
        let len = size[0] * size[1] + 2 * chroma * size[1].div_ceil(2);
        Self {
            size,
            strides: [size[0], chroma, chroma],
            data: vec![0; len],
        }
    }

    /// The three planes, mutable.
    pub fn planes_mut(&mut self) -> (&mut [u8], &mut [u8], &mut [u8]) {
        let luma = self.strides[0] * self.size[1];
        let chroma = self.strides[1] * self.size[1].div_ceil(2);
        let (y, rest) = self.data.split_at_mut(luma);
        let (u, v) = rest.split_at_mut(chroma);
        (y, u, &mut v[..self.strides[2] * self.size[1].div_ceil(2)])
    }
}

/// A call frame from a YUV 4:2:0 picture, scaled to `out` and turned clockwise by `turns`
/// quarter turns, as [`rgb_image`] makes one.
#[cfg(feature = "ui")]
pub(crate) fn call_frame(
    y: &[u8],
    u: &[u8],
    v: &[u8],
    strides: (usize, usize, usize),
    size: (usize, usize),
    out: (usize, usize),
    turns: u8,
) -> CallFrame {
    rgb_image(y, u, v, strides, size, out, turns)
}

/// A call frame from a YUV 4:2:0 picture, scaled to `out` and turned clockwise by `turns`
/// quarter turns, as [`rgb_image`] does, but left in YUV: luma is averaged as there, chroma is
/// sampled.
#[cfg(not(feature = "ui"))]
pub(crate) fn call_frame(
    y: &[u8],
    u: &[u8],
    v: &[u8],
    strides: (usize, usize, usize),
    size: (usize, usize),
    out: (usize, usize),
    turns: u8,
) -> CallFrame {
    let (width, height) = size;
    let (out_width, out_height) = out;
    let turned = if turns % 2 == 1 {
        [out_height, out_width]
    } else {
        [out_width, out_height]
    };
    let mut frame = CallFrame::new(turned);
    if width == 0 || height == 0 || out_width == 0 || out_height == 0 {
        return frame;
    }
    let [tw, th] = turned;
    let st = frame.strides;
    let (dy, du, dv) = frame.planes_mut();
    if out == size && turns % 4 == 0 {
        // The decoder's own size, upright: the rows as they are.
        for row in 0..height {
            dy[row * st[0]..row * st[0] + width]
                .copy_from_slice(&y[row * strides.0..row * strides.0 + width]);
        }
        let cw = width.div_ceil(2);
        for row in 0..height.div_ceil(2) {
            du[row * st[1]..row * st[1] + cw]
                .copy_from_slice(&u[row * strides.1..row * strides.1 + cw]);
            dv[row * st[2]..row * st[2] + cw]
                .copy_from_slice(&v[row * strides.2..row * strides.2 + cw]);
        }
        return frame;
    }
    let scaling = out != size;
    // The source pixel behind each pixel of the turned frame: the inverse of `convert`'s turn,
    // then its scaling.
    let source = |tx: usize, ty: usize| {
        let (ox, oy) = match turns % 4 {
            1 => (ty, out_height - 1 - tx),
            2 => (out_width - 1 - tx, out_height - 1 - ty),
            3 => (out_width - 1 - ty, tx),
            _ => (tx, ty),
        };
        (
            (ox * width / out_width).min(width - 1),
            (oy * height / out_height).min(height - 1),
        )
    };
    for ty in 0..th {
        for tx in 0..tw {
            let (sx, sy) = source(tx, ty);
            let (right, below) = if scaling {
                ((sx + 1).min(width - 1), (sy + 1).min(height - 1))
            } else {
                (sx, sy)
            };
            let at = |x: usize, row: usize| u32::from(y[row * strides.0 + x]);
            dy[ty * st[0] + tx] =
                ((at(sx, sy) + at(right, sy) + at(sx, below) + at(right, below) + 2) / 4) as u8;
        }
    }
    for cy in 0..th.div_ceil(2) {
        for cx in 0..tw.div_ceil(2) {
            let (sx, sy) = source((cx * 2).min(tw - 1), (cy * 2).min(th - 1));
            du[cy * st[1] + cx] = u[sy / 2 * strides.1 + sx / 2];
            dv[cy * st[2] + cx] = v[sy / 2 * strides.2 + sx / 2];
        }
    }
    frame
}

/// An all-black call frame, for tests.
#[cfg(all(test, feature = "ui"))]
pub(crate) fn black(size: [usize; 2]) -> CallFrame {
    Image::new(size, vec![opaque(0, 0, 0); size[0] * size[1]])
}

/// An all-black call frame, for tests.
#[cfg(all(test, not(feature = "ui")))]
pub(crate) fn black(size: [usize; 2]) -> CallFrame {
    let mut frame = CallFrame::new(size);
    let (y, u, v) = frame.planes_mut();
    y.fill(16);
    u.fill(128);
    v.fill(128);
    frame
}

/// Converts a YUV 4:2:0 picture to RGB, scaled to `out` when it differs from `size`.
///
/// Shared with the call screen, which has raw camera planes and decoded peer frames rather than a
/// whole file to play. `turns` is quarter turns clockwise to apply while converting; a call passes
/// [`orientation_turns`] of the peer's `device_orientation`, and `out` stays the size the picture
/// has *before* those turns, since the rotation swaps the result's sides.
#[cfg_attr(not(feature = "ui"), allow(dead_code))]
pub(crate) fn rgb_image(
    y: &[u8],
    u: &[u8],
    v: &[u8],
    strides: (usize, usize, usize),
    size: (usize, usize),
    out: (usize, usize),
    turns: u8,
) -> Image {
    convert(
        &Planes {
            y,
            u,
            v,
            strides,
            size,
        },
        out,
        turns,
    )
}

/// How many quarter turns clockwise a peer's `device_orientation` asks the picture to take.
///
/// WhatsApp's low two bits are a counter-clockwise quarter-turn count, not a clockwise one: the
/// shipped web renderer applies `rotate(Math.PI * (orientation - 1) / 2)`, and whatsapp-rust's own
/// `voip-cli` example undoes the same value with `transpose=cclock` for 1 and `transpose=clock`
/// for 3 (`examples/voip-cli/src/video.rs`). [`convert`] turns clockwise, so the count flips; 2 is
/// its own inverse. Anything else, including the 0 an upright camera reports, is no turn at all.
pub(crate) fn orientation_turns(orientation: u8) -> u8 {
    match orientation & 0x03 {
        1 => 3,
        2 => 2,
        3 => 1,
        _ => 0,
    }
}

/// A YUV 4:2:0 picture as the decoder leaves it.
pub(crate) struct Planes<'a> {
    pub(crate) y: &'a [u8],
    pub(crate) u: &'a [u8],
    pub(crate) v: &'a [u8],
    pub(crate) strides: (usize, usize, usize),
    pub(crate) size: (usize, usize),
}

/// Converts limited-range BT.601 YUV to RGB at `out` size, turned clockwise
/// by `turns` quarter turns. Scaling happens during the conversion, one
/// sample per output pixel, so a large video costs about as much as a small
/// one; when scaling, luma is averaged over the first two by two source
/// pixels each output pixel covers, which keeps downscaled edges smooth.
pub(crate) fn convert(planes: &Planes<'_>, out: (usize, usize), turns: u8) -> Image {
    let (width, height) = planes.size;
    let (out_width, out_height) = out;
    let turned = if turns % 2 == 1 {
        [out_height, out_width]
    } else {
        [out_width, out_height]
    };
    let scaling = out != planes.size;
    let mut pixels = vec![opaque(0, 0, 0); out_width * out_height];
    for oy in 0..out_height {
        let sy = (oy * height / out_height).min(height - 1);
        let below = if scaling {
            (sy + 1).min(height - 1)
        } else {
            sy
        };
        let luma = &planes.y[sy * planes.strides.0..];
        let luma_below = &planes.y[below * planes.strides.0..];
        let u = &planes.u[sy / 2 * planes.strides.1..];
        let v = &planes.v[sy / 2 * planes.strides.2..];
        for ox in 0..out_width {
            let sx = (ox * width / out_width).min(width - 1);
            let right = if scaling { (sx + 1).min(width - 1) } else { sx };
            let y = (u32::from(luma[sx])
                + u32::from(luma[right])
                + u32::from(luma_below[sx])
                + u32::from(luma_below[right])
                + 2)
                / 4;
            let (tx, ty) = match turns {
                1 => (out_height - 1 - oy, ox),
                2 => (out_width - 1 - ox, out_height - 1 - oy),
                3 => (oy, out_width - 1 - ox),
                _ => (ox, oy),
            };
            pixels[ty * turned[0] + tx] = rgb(y as i32, i32::from(u[sx / 2]), i32::from(v[sx / 2]));
        }
    }
    Image::new(turned, pixels)
}

/// One limited-range BT.601 pixel in RGB.
pub(crate) fn rgb(y: i32, u: i32, v: i32) -> Pixel {
    let (c, d, e) = (298 * (y - 16), u - 128, v - 128);
    let channel = |value: i32| ((value + 128) >> 8).clamp(0, 255) as u8;
    opaque(
        channel(c + 409 * e),
        channel(c - 100 * d - 208 * e),
        channel(c + 516 * d),
    )
}

#[cfg(all(test, not(feature = "ui")))]
mod tests {
    use super::*;

    /// A 4 × 2 picture whose luma numbers its pixels and whose chroma
    /// numbers its 2 × 2 blocks, with padded rows.
    fn planes() -> (Vec<u8>, Vec<u8>, Vec<u8>) {
        let y = vec![0, 1, 2, 3, 99, 99, 4, 5, 6, 7, 99, 99];
        (y, vec![10, 11, 99], vec![20, 21, 99])
    }

    #[test]
    fn a_frame_at_its_own_size_is_its_rows_without_padding() {
        let (y, u, v) = planes();
        let frame = call_frame(&y, &u, &v, (6, 3, 3), (4, 2), (4, 2), 0);
        assert_eq!(frame.size, [4, 2]);
        assert_eq!(frame.strides, [4, 2, 2]);
        assert_eq!(frame.data, [0, 1, 2, 3, 4, 5, 6, 7, 10, 11, 20, 21]);
    }

    #[test]
    fn a_quarter_turn_clockwise_turns_every_plane() {
        let (y, u, v) = planes();
        let frame = call_frame(&y, &u, &v, (6, 3, 3), (4, 2), (4, 2), 1);
        assert_eq!(frame.size, [2, 4]);
        // The bottom-left pixel (4) comes to the top-left, the top-left (0)
        // to the top-right.
        assert_eq!(&frame.data[..8], &[4, 0, 5, 1, 6, 2, 7, 3]);
        assert_eq!(&frame.data[8..10], &[10, 11], "U: one column, two rows");
        assert_eq!(&frame.data[10..12], &[20, 21]);
    }

    #[test]
    fn a_smaller_frame_averages_luma_and_keeps_odd_sides() {
        let (y, u, v) = planes();
        let frame = call_frame(&y, &u, &v, (6, 3, 3), (4, 2), (1, 1), 0);
        assert_eq!(frame.size, [1, 1]);
        assert_eq!(frame.data, [(0 + 1 + 4 + 5 + 2) / 4, 10, 20]);
    }
}
