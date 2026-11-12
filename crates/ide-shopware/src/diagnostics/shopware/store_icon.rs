//! The extension icon the Store shows: an existing PNG of at most 30 kB,
//! 84 to 256 pixels wide and high.

use std::io::Read;

/// What is wrong with an icon; the composer.json and manifest analyzers
/// report each under a code of their own.
#[derive(Clone, Copy, PartialEq, Eq)]
pub(crate) enum IconIssue {
    /// No such file, or not a PNG.
    Unusable,
    /// Too many bytes, or too few or too many pixels.
    Size,
}

const MAX_BYTES: u64 = 30 * 1024;
const MIN_PIXELS: u32 = 84;
const MAX_PIXELS: u32 = 256;
const PNG_SIGNATURE: &[u8] = b"\x89PNG\r\n\x1a\n";

/// What is wrong with the icon file at `path` (shown as `shown`), with its
/// message. `None` for a valid icon, and for a file that
/// exists but cannot be read.
pub(crate) fn icon_finding(path: &str, shown: &str) -> Option<(IconIssue, String)> {
    let metadata = match std::fs::metadata(path) {
        Ok(metadata) if metadata.is_file() => metadata,
        Ok(_) => return Some(missing(shown)),
        Err(error) if error.kind() == std::io::ErrorKind::NotFound => return Some(missing(shown)),
        Err(_) => return None,
    };
    if metadata.len() > MAX_BYTES {
        return Some((
            IconIssue::Size,
            format!(
                "Store: the extension icon {shown} is larger than 30 kB ({} kB)",
                metadata.len().div_ceil(1024)
            ),
        ));
    }
    // Signature (8), IHDR length and type (8), width and height (4 each).
    let mut header = [0_u8; 24];
    let read = std::fs::File::open(path).and_then(|mut file| file.read_exact(&mut header));
    match read {
        Ok(()) => {}
        Err(error) if error.kind() == std::io::ErrorKind::UnexpectedEof => {
            return Some(not_png(shown));
        }
        Err(_) => return None,
    }
    if !header.starts_with(PNG_SIGNATURE) || &header[12..16] != b"IHDR" {
        return Some(not_png(shown));
    }
    let pixels = |at: usize| {
        u32::from_be_bytes([header[at], header[at + 1], header[at + 2], header[at + 3]])
    };
    let (width, height) = (pixels(16), pixels(20));
    let allowed = MIN_PIXELS..=MAX_PIXELS;
    if allowed.contains(&width) && allowed.contains(&height) {
        return None;
    }
    Some((
        IconIssue::Size,
        format!(
            "Store: the extension icon {shown} is {width}x{height} pixels; it must be between \
             {MIN_PIXELS}x{MIN_PIXELS} and {MAX_PIXELS}x{MAX_PIXELS}"
        ),
    ))
}

fn missing(shown: &str) -> (IconIssue, String) {
    (
        IconIssue::Unusable,
        format!("Store: the extension icon {shown} does not exist"),
    )
}

fn not_png(shown: &str) -> (IconIssue, String) {
    (
        IconIssue::Unusable,
        format!("Store: the extension icon {shown} is not a PNG image"),
    )
}
