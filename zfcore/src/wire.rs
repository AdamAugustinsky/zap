//! The record encoding the generated code (`abi_gen.rs`) is written in:
//! little-endian fixed-width numbers, a `u32` length before every string,
//! byte string, path and list, and a presence byte before every option.
//! Encoding refuses a field over its schema bound; decoding refuses anything
//! that does not read back exactly.

use std::path::{Path, PathBuf};

/// A value over its schema bound: the record is not built.
#[derive(Clone, Copy, Debug, PartialEq, Eq)]
pub struct TooLarge {
    /// The schema path of the field, `Type.field`.
    pub field: &'static str,
    pub len: usize,
    pub max: usize,
}

/// A record that does not decode: too short, too long, a bad tag, a value
/// over its bound, or invalid UTF-8.
#[derive(Clone, Copy, Debug, PartialEq, Eq)]
pub struct Malformed {
    pub reason: &'static str,
    /// The byte offset where decoding stopped.
    pub at: usize,
}

impl std::fmt::Display for Malformed {
    fn fmt(&self, f: &mut std::fmt::Formatter<'_>) -> std::fmt::Result {
        write!(f, "malformed record at byte {}: {}", self.at, self.reason)
    }
}

impl std::fmt::Display for TooLarge {
    fn fmt(&self, f: &mut std::fmt::Formatter<'_>) -> std::fmt::Result {
        write!(
            f,
            "{} is {} long, over its bound {}",
            self.field, self.len, self.max
        )
    }
}

/// A blob the core holds: read it with `zf_blob_read`, free it with
/// `zf_blob_free`.
#[derive(Clone, Copy, Debug, Default, PartialEq, Eq, Hash)]
pub struct BlobRef {
    pub id: u64,
    pub len: u64,
}

pub trait Wire: Sized {
    fn put(&self, w: &mut Writer) -> Result<(), TooLarge>;
    fn get(r: &mut Reader<'_>) -> Result<Self, Malformed>;
    /// The exact length `put` writes.
    fn wire_len(&self) -> usize;
}

/// Encodes `value` alone, with no record header (the open configuration).
pub fn encode_bare<T: Wire>(value: &T) -> Result<Vec<u8>, TooLarge> {
    let mut w = Writer::reuse(&mut Vec::new());
    value.put(&mut w)?;
    Ok(w.finish())
}

/// Decodes a bare `T` that must fill `buf`.
pub fn decode_bare<T: Wire>(buf: &[u8]) -> Result<T, Malformed> {
    let mut r = Reader::new(buf);
    let value = T::get(&mut r)?;
    r.finish()?;
    Ok(value)
}

pub struct Writer {
    buf: Vec<u8>,
}

impl Writer {
    /// Takes `out`'s allocation, cleared.
    pub fn reuse(out: &mut Vec<u8>) -> Self {
        let mut buf = std::mem::take(out);
        buf.clear();
        Self { buf }
    }

    pub fn finish(self) -> Vec<u8> {
        self.buf
    }

    pub fn len(&self) -> usize {
        self.buf.len()
    }

    pub fn is_empty(&self) -> bool {
        self.buf.is_empty()
    }

    pub fn bool(&mut self, v: bool) {
        self.buf.push(u8::from(v));
    }

    pub fn u8(&mut self, v: u8) {
        self.buf.push(v);
    }

    pub fn u16(&mut self, v: u16) {
        self.buf.extend_from_slice(&v.to_le_bytes());
    }

    pub fn u32(&mut self, v: u32) {
        self.buf.extend_from_slice(&v.to_le_bytes());
    }

    pub fn u64(&mut self, v: u64) {
        self.buf.extend_from_slice(&v.to_le_bytes());
    }

    pub fn i32(&mut self, v: i32) {
        self.buf.extend_from_slice(&v.to_le_bytes());
    }

    pub fn i64(&mut self, v: i64) {
        self.buf.extend_from_slice(&v.to_le_bytes());
    }

    pub fn f32(&mut self, v: f32) {
        self.buf.extend_from_slice(&v.to_bits().to_le_bytes());
    }

    pub fn f64(&mut self, v: f64) {
        self.buf.extend_from_slice(&v.to_bits().to_le_bytes());
    }

    pub fn str(&mut self, v: &str, max: usize, field: &'static str) -> Result<(), TooLarge> {
        self.bytes(v.as_bytes(), max, field)
    }

    pub fn bytes(&mut self, v: &[u8], max: usize, field: &'static str) -> Result<(), TooLarge> {
        self.count(v.len(), max, field)?;
        self.buf.extend_from_slice(v);
        Ok(())
    }

    pub fn path(&mut self, v: &Path, max: usize, field: &'static str) -> Result<(), TooLarge> {
        self.bytes(path_bytes(v), max, field)
    }

    pub fn blob(&mut self, v: &BlobRef) {
        self.u64(v.id);
        self.u64(v.len);
    }

    /// A length or element count, refused over `max`.
    pub fn count(&mut self, n: usize, max: usize, field: &'static str) -> Result<(), TooLarge> {
        if n > max {
            return Err(TooLarge { field, len: n, max });
        }
        self.u32(n as u32);
        Ok(())
    }

    /// Refuses a chunked list whose elements, written from `start`, exceed
    /// `budget` bytes.
    pub fn budget(
        &mut self,
        start: usize,
        budget: usize,
        field: &'static str,
    ) -> Result<(), TooLarge> {
        let len = self.buf.len() - start;
        if len > budget {
            return Err(TooLarge {
                field,
                len,
                max: budget,
            });
        }
        Ok(())
    }
}

pub struct Reader<'a> {
    buf: &'a [u8],
    pos: usize,
}

impl<'a> Reader<'a> {
    pub fn new(buf: &'a [u8]) -> Self {
        Self { buf, pos: 0 }
    }

    pub fn bad(&self, reason: &'static str) -> Malformed {
        Malformed {
            reason,
            at: self.pos,
        }
    }

    /// How many bytes have been read.
    pub fn pos(&self) -> usize {
        self.pos
    }

    /// Refuses a chunked list whose elements, read from `start`, took more
    /// than `budget` bytes: the encoder would not have written it.
    pub fn budget(&self, start: usize, budget: usize) -> Result<(), Malformed> {
        if self.pos - start > budget {
            return Err(self.bad("a chunk over its byte budget"));
        }
        Ok(())
    }

    fn take(&mut self, n: usize) -> Result<&'a [u8], Malformed> {
        if self.buf.len() - self.pos < n {
            return Err(self.bad("record ends early"));
        }
        let bytes = &self.buf[self.pos..self.pos + n];
        self.pos += n;
        Ok(bytes)
    }

    fn array<const N: usize>(&mut self) -> Result<[u8; N], Malformed> {
        Ok(self.take(N)?.try_into().expect("took N bytes"))
    }

    pub fn finish(&self) -> Result<(), Malformed> {
        if self.pos != self.buf.len() {
            return Err(self.bad("bytes after the record"));
        }
        Ok(())
    }

    pub fn flag(&mut self) -> Result<bool, Malformed> {
        match self.u8()? {
            0 => Ok(false),
            1 => Ok(true),
            _ => Err(self.bad("a flag that is neither 0 nor 1")),
        }
    }

    pub fn bool(&mut self) -> Result<bool, Malformed> {
        self.flag()
    }

    pub fn u8(&mut self) -> Result<u8, Malformed> {
        Ok(self.take(1)?[0])
    }

    pub fn u16(&mut self) -> Result<u16, Malformed> {
        Ok(u16::from_le_bytes(self.array()?))
    }

    pub fn u32(&mut self) -> Result<u32, Malformed> {
        Ok(u32::from_le_bytes(self.array()?))
    }

    pub fn u64(&mut self) -> Result<u64, Malformed> {
        Ok(u64::from_le_bytes(self.array()?))
    }

    pub fn i32(&mut self) -> Result<i32, Malformed> {
        Ok(i32::from_le_bytes(self.array()?))
    }

    pub fn i64(&mut self) -> Result<i64, Malformed> {
        Ok(i64::from_le_bytes(self.array()?))
    }

    pub fn f32(&mut self) -> Result<f32, Malformed> {
        Ok(f32::from_bits(self.u32()?))
    }

    pub fn f64(&mut self) -> Result<f64, Malformed> {
        Ok(f64::from_bits(self.u64()?))
    }

    /// An element count: at most `max`, and no more than the remaining
    /// bytes could hold at `min_size` each, so a bad count cannot allocate.
    pub fn count(&mut self, max: usize, min_size: usize) -> Result<usize, Malformed> {
        let n = self.u32()? as usize;
        if n > max {
            return Err(self.bad("a count over its bound"));
        }
        if n.saturating_mul(min_size.max(1)) > self.buf.len() - self.pos {
            return Err(self.bad("a count larger than the record"));
        }
        Ok(n)
    }

    pub fn bytes(&mut self, max: usize) -> Result<Vec<u8>, Malformed> {
        let n = self.count(max, 1)?;
        Ok(self.take(n)?.to_vec())
    }

    pub fn str(&mut self, max: usize) -> Result<String, Malformed> {
        let n = self.count(max, 1)?;
        let bytes = self.take(n)?;
        match std::str::from_utf8(bytes) {
            Ok(s) => Ok(s.to_owned()),
            Err(_) => Err(self.bad("invalid UTF-8")),
        }
    }

    pub fn path(&mut self, max: usize) -> Result<PathBuf, Malformed> {
        Ok(path_from(self.bytes(max)?))
    }

    pub fn blob(&mut self) -> Result<BlobRef, Malformed> {
        Ok(BlobRef {
            id: self.u64()?,
            len: self.u64()?,
        })
    }
}

#[cfg(unix)]
fn path_bytes(path: &Path) -> &[u8] {
    use std::os::unix::ffi::OsStrExt;
    path.as_os_str().as_bytes()
}

#[cfg(unix)]
fn path_from(bytes: Vec<u8>) -> PathBuf {
    use std::os::unix::ffi::OsStringExt;
    PathBuf::from(std::ffi::OsString::from_vec(bytes))
}

#[cfg(not(unix))]
fn path_bytes(path: &Path) -> &[u8] {
    path.as_os_str().as_encoded_bytes()
}

#[cfg(not(unix))]
fn path_from(bytes: Vec<u8>) -> PathBuf {
    PathBuf::from(String::from_utf8_lossy(&bytes).into_owned())
}

/// Sample values for the fixtures and the bound tests.
#[cfg(test)]
pub mod sample {
    use super::BlobRef;
    use std::path::PathBuf;

    /// Picks values: in `max` mode every bound is filled; otherwise values
    /// vary with a counter, and unions take variant `round % n`, so `n`
    /// rounds show every variant.
    pub struct Seed {
        pub n: u64,
        pub round: u64,
        pub max: bool,
    }

    impl Seed {
        pub fn new(round: u64, max: bool) -> Self {
            Self {
                n: round.wrapping_mul(0x9e37_79b9_7f4a_7c15) | 1,
                round,
                max,
            }
        }

        fn tick(&mut self) -> u64 {
            self.n = self
                .n
                .wrapping_mul(6_364_136_223_846_793_005)
                .wrapping_add(1_442_695_040_888_963_407);
            self.n >> 33
        }

        pub fn pick(&mut self, n: usize) -> usize {
            (self.round % n as u64) as usize
        }

        pub fn some(&mut self) -> bool {
            self.max || !self.tick().is_multiple_of(4)
        }

        pub fn count(&mut self, max: usize) -> usize {
            if self.max {
                max
            } else {
                (self.tick() % 3) as usize
            }
        }

        pub fn bool(&mut self) -> bool {
            self.tick() % 2 == 1
        }

        pub fn u8(&mut self) -> u8 {
            self.tick() as u8
        }

        pub fn u16(&mut self) -> u16 {
            self.tick() as u16
        }

        pub fn u32(&mut self) -> u32 {
            self.tick() as u32
        }

        pub fn u64(&mut self) -> u64 {
            self.tick() << 20 | self.tick()
        }

        pub fn i32(&mut self) -> i32 {
            self.tick() as i32 - (1 << 30)
        }

        pub fn i64(&mut self) -> i64 {
            self.u64() as i64
        }

        pub fn f32(&mut self) -> f32 {
            self.tick() as f32 * 0.25 - 1000.0
        }

        pub fn f64(&mut self) -> f64 {
            self.tick() as f64 * 0.125 - 90.0
        }

        pub fn str(&mut self, max: usize) -> String {
            if self.max {
                return "a".repeat(max);
            }
            let k = self.tick();
            let mut s = match k % 3 {
                0 => String::new(),
                1 => format!("s{k}"),
                _ => format!("é→{k}✓"),
            };
            while s.len() > max {
                s.pop();
            }
            s
        }

        pub fn bytes(&mut self, max: usize) -> Vec<u8> {
            if self.max {
                return vec![0xab; max];
            }
            let k = self.tick();
            (0..(k % 5) as usize)
                .map(|i| (k as u8).wrapping_add(i as u8))
                .take(max)
                .collect()
        }

        pub fn path(&mut self, max: usize) -> PathBuf {
            if self.max {
                return PathBuf::from("p".repeat(max));
            }
            let mut s = format!("/tmp/zf/{}", self.tick());
            s.truncate(max);
            PathBuf::from(s)
        }

        pub fn blob(&mut self) -> BlobRef {
            BlobRef {
                id: self.u64(),
                len: self.u64(),
            }
        }
    }
}
